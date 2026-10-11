package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/ingest"
	"github.com/shashanksbs45/pulseboard/internal/store"
	"github.com/shashanksbs45/pulseboard/internal/store/sqlite"
)

const token = "secret"

func openStore(t *testing.T) (*sqlite.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st, path
}

// start runs srv on a loopback port and returns its base URL, a cancel func
// that begins shutdown, and a channel that yields Serve's result.
func start(t *testing.T, srv *Server) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- srv.Serve(ctx, ln)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return "http://" + ln.Addr().String(), cancel, done
}

func ingestHandler(st store.Store) http.Handler {
	return &ingest.Handler{Store: st, Token: token, Retention: 7 * 24 * time.Hour}
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestHealthzOK(t *testing.T) {
	st, _ := openStore(t)
	base, _, _ := start(t, New(st, ingestHandler(st)))
	if got := getStatus(t, base+"/healthz"); got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
}

// pingFails wraps a store and makes Ping fail.
type pingFails struct{ store.Store }

func (pingFails) Ping(context.Context) error { return errors.New("database unreachable") }

func TestHealthzUnavailable(t *testing.T) {
	st, _ := openStore(t)
	broken := pingFails{st}
	base, _, _ := start(t, New(broken, ingestHandler(broken)))
	if got := getStatus(t, base+"/healthz"); got != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got)
	}
}

func TestIngestRouteRequiresPost(t *testing.T) {
	st, _ := openStore(t)
	base, _, _ := start(t, New(st, ingestHandler(st)))
	if got := getStatus(t, base+"/api/v1/ingest"); got != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", got)
	}
}

// slowStore blocks Write until release is closed, signalling entered first.
type slowStore struct {
	store.Store
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (s *slowStore) Write(ctx context.Context, pts []store.Point) ([]store.Rejection, error) {
	close(s.entered)
	<-s.release
	return s.Store.Write(ctx, pts)
}

func (s *slowStore) Close() error {
	close(s.closed)
	return s.Store.Close()
}

func TestInFlightIngestCompletesDuringShutdown(t *testing.T) {
	st, path := openStore(t)
	slow := &slowStore{Store: st, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	srv := New(slow, ingestHandler(slow))
	bgStopped := make(chan struct{})
	srv.Background(func(ctx context.Context) {
		<-ctx.Done()
		close(bgStopped)
	})
	base, cancel, done := start(t, srv)

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/ingest",
			strings.NewReader(`[{"name":"m","type":"gauge","value":1}]`))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b)}
	}()

	<-slow.entered
	cancel() // shutdown begins while the request is inside Write

	select {
	case <-slow.closed:
		t.Fatal("store closed while a request was still in flight")
	case <-time.After(200 * time.Millisecond):
	}
	close(slow.release)

	res := <-resCh
	if res.err != nil {
		t.Fatalf("in-flight request failed: %v", res.err)
	}
	if res.status != http.StatusOK {
		t.Errorf("status = %d, want 200 (body %s)", res.status, res.body)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v, want nil", err)
	}
	select {
	case <-bgStopped:
	default:
		t.Error("background task was not stopped")
	}
	select {
	case <-slow.closed:
	default:
		t.Error("store was not closed")
	}

	// The accepted point survives a reopen.
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if n, err := reopened.DeleteBefore(context.Background(), 1<<62); err != nil || n != 1 {
		t.Errorf("after restart found %d points (err %v), want 1", n, err)
	}
}
