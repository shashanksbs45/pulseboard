// Package server hosts every pulseboard endpoint on one http.Server and owns
// the shutdown order: drain HTTP, stop background tasks, close the store.
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

// ShutdownTimeout bounds how long in-flight requests may run after shutdown
// begins.
const ShutdownTimeout = 10 * time.Second

// Server is the pulseboard HTTP server.
type Server struct {
	http  *http.Server
	store store.Store
	bg    []func(context.Context)
}

// New routes the ingest handler and the health check.
func New(st store.Store, ingest http.Handler) *Server {
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/ingest", ingest)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n")) // nothing useful to do if the client went away
	})
	return &Server{
		http:  &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		store: st,
	}
}

// Background registers a task (such as the retention loop) that runs while
// the server is up. Its context is cancelled after HTTP requests drain, and
// the store is closed only once it returns.
func (s *Server) Background(task func(context.Context)) {
	s.bg = append(s.bg, task)
}

// Serve accepts connections on ln until ctx is cancelled, then shuts down:
// stop accepting, finish in-flight requests for up to ShutdownTimeout, stop
// background tasks, and close the store.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	bgCtx, stopBg := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, task := range s.bg {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task(bgCtx)
		}()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.http.Serve(ln) }()

	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if shutdownErr := s.http.Shutdown(shutdownCtx); shutdownErr != nil && err == nil {
		err = shutdownErr
	}
	stopBg()
	wg.Wait()
	if closeErr := s.store.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}
