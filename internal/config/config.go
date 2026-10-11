// Package config turns `serve` flags and environment variables into a Config.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

// Defaults applied when neither a flag nor an environment variable is set.
const (
	DefaultAddr      = ":8080"
	DefaultDB        = "./pulseboard.db"
	DefaultRetention = 168 * time.Hour
)

// Config holds the settings for `pulseboard serve`.
type Config struct {
	Addr        string
	DBPath      string
	IngestToken string
	Retention   time.Duration
}

// Parse builds a Config from the `serve` arguments. Each environment variable
// (read through getenv) supplies a setting's default; an explicit flag
// overrides it. Usage and flag errors are written to out.
func Parse(args []string, getenv func(string) string, out io.Writer) (Config, error) {
	envOr := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}

	retentionDefault := DefaultRetention
	if v := getenv("PULSEBOARD_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid retention %q in PULSEBOARD_RETENTION: %v", v, err)
		}
		retentionDefault = d
	}

	var cfg Config
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&cfg.Addr, "addr", envOr("PULSEBOARD_ADDR", DefaultAddr), "listen address (env PULSEBOARD_ADDR)")
	fs.StringVar(&cfg.DBPath, "db", envOr("PULSEBOARD_DB", DefaultDB), "SQLite database path (env PULSEBOARD_DB)")
	fs.StringVar(&cfg.IngestToken, "ingest-token", getenv("PULSEBOARD_INGEST_TOKEN"), "bearer token for the ingest endpoint (env PULSEBOARD_INGEST_TOKEN)")
	fs.DurationVar(&cfg.Retention, "retention", retentionDefault, "how long raw points are kept (env PULSEBOARD_RETENTION)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports the first setting that would prevent startup.
func (c Config) Validate() error {
	if c.IngestToken == "" {
		return errors.New("ingest token is required: set --ingest-token or PULSEBOARD_INGEST_TOKEN")
	}
	if c.Retention <= 0 {
		return fmt.Errorf("retention must be a positive duration, got %s", c.Retention)
	}
	return nil
}
