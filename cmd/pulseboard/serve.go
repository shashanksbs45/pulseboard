package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/config"
	"github.com/shashanksbs45/pulseboard/internal/ingest"
	"github.com/shashanksbs45/pulseboard/internal/retention"
	"github.com/shashanksbs45/pulseboard/internal/server"
	"github.com/shashanksbs45/pulseboard/internal/store/sqlite"
)

// serve runs the HTTP server until it receives SIGINT or SIGTERM.
func serve(args []string, stderr io.Writer) error {
	cfg, err := config.Parse(args, os.Getenv, stderr)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	st, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return err
	}

	srv := server.New(st, &ingest.Handler{
		Store:     st,
		Token:     cfg.IngestToken,
		Retention: cfg.Retention,
		Logger:    logger,
	})
	srv.Background(func(ctx context.Context) {
		ticker := time.NewTicker(retention.Interval)
		defer ticker.Stop()
		retention.Run(ctx, st, cfg.Retention, time.Now, ticker.C, logger)
	})

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		st.Close()
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("pulseboard listening", "addr", ln.Addr().String(), "db", cfg.DBPath, "retention", cfg.Retention)
	if err := srv.Serve(ctx, ln); err != nil {
		return err
	}
	logger.Info("pulseboard stopped")
	return nil
}
