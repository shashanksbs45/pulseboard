package retention

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store"
	"github.com/shashanksbs45/pulseboard/internal/store/sqlite"
)

const day = 24 * time.Hour

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeStore records each DeleteBefore cutoff.
type fakeStore struct {
	store.Store
	cutoffs chan int64
}

func (f *fakeStore) DeleteBefore(_ context.Context, cutoffMs int64) (int64, error) {
	f.cutoffs <- cutoffMs
	return 0, nil
}

func TestPassRemovesExpiredKeepsRecent(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.UnixMilli(1759480000000)
	expired := now.Add(-(7*day + 2*time.Hour)).UnixMilli()
	recent := now.Add(-(7*day - time.Hour)).UnixMilli()
	ctx := context.Background()
	if _, err := st.Write(ctx, []store.Point{
		{Name: "m", Type: store.Gauge, Value: 1, TS: expired},
		{Name: "m", Type: store.Gauge, Value: 2, TS: recent},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := Pass(ctx, st, 7*day, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("deleted %d points, want 1", n)
	}
	// A second pass finds nothing left to delete: the recent point stays.
	if n, _ := Pass(ctx, st, 7*day, now); n != 0 {
		t.Errorf("second pass deleted %d points, want 0", n)
	}
}

func TestRunPassesAtStartupAndOnTickThenExits(t *testing.T) {
	fs := &fakeStore{cutoffs: make(chan int64, 4)}
	clock := time.UnixMilli(1759480000000)
	now := func() time.Time { return clock }
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, fs, 7*day, now, ticks, discard)
		close(done)
	}()

	want := clock.Add(-7 * day).UnixMilli()
	if got := <-fs.cutoffs; got != want {
		t.Errorf("startup cutoff = %d, want %d", got, want)
	}

	// Advancing the clock before the send is race-free: the send happens
	// before Run reads the clock for the next pass.
	clock = clock.Add(Interval)
	ticks <- clock
	want = clock.Add(-7 * day).UnixMilli()
	if got := <-fs.cutoffs; got != want {
		t.Errorf("tick cutoff = %d, want %d", got, want)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}
