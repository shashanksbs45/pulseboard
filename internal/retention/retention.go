// Package retention deletes points older than the retention period.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

// Interval is how often a deletion pass runs after the startup pass.
const Interval = time.Hour

// Pass deletes every point older than now - retention.
func Pass(ctx context.Context, st store.Store, retention time.Duration, now time.Time) (int64, error) {
	return st.DeleteBefore(ctx, now.Add(-retention).UnixMilli())
}

// Run performs a pass immediately, then one per value received on ticks,
// until ctx is cancelled. Pass errors are logged and the loop continues.
func Run(ctx context.Context, st store.Store, retention time.Duration, now func() time.Time, ticks <-chan time.Time, logger *slog.Logger) {
	pass := func() {
		start := time.Now()
		n, err := Pass(ctx, st, retention, now())
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("retention pass failed", "err", err)
			}
			return
		}
		logger.Info("retention pass complete", "deleted", n, "took", time.Since(start))
	}

	pass()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			pass()
		}
	}
}
