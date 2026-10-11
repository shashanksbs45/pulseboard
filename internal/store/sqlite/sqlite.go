// Package sqlite implements store.Store on a single SQLite file.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/shashanksbs45/pulseboard/internal/store"
)

const (
	// DefaultSeriesLimit is the maximum number of active series.
	DefaultSeriesLimit = 10000
	// deleteChunk is how many points one retention DELETE removes, so the
	// write lock is released between chunks.
	deleteChunk = 10000
)

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE metrics (
		name TEXT PRIMARY KEY,
		type TEXT NOT NULL CHECK (type IN ('gauge','counter'))
	);
	CREATE TABLE series (
		id     INTEGER PRIMARY KEY,
		metric TEXT NOT NULL REFERENCES metrics(name),
		labels TEXT NOT NULL,
		UNIQUE (metric, labels)
	);
	CREATE TABLE points (
		series_id INTEGER NOT NULL REFERENCES series(id),
		ts        INTEGER NOT NULL,
		value     REAL NOT NULL,
		PRIMARY KEY (series_id, ts)
	) WITHOUT ROWID;
	CREATE INDEX points_ts ON points(ts);`,
}

// Store is a store.Store backed by SQLite.
type Store struct {
	db          *sql.DB
	seriesLimit int
	deleteChunk int
}

var _ store.Store = (*Store)(nil)

// Open opens (creating if needed) the database at path, applies the
// connection pragmas, and migrates the schema to the latest version.
func Open(path string) (*Store, error) {
	// _txlock=immediate makes every transaction BEGIN IMMEDIATE, taking the
	// write lock up front so concurrent batches serialize.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	s := &Store{db: db, seriesLimit: DefaultSeriesLimit, deleteChunk: deleteChunk}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this binary supports (%d)", version, len(migrations))
	}
	for v := version; v < len(migrations); v++ {
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			return fmt.Errorf("migrate to schema version %d: %w", v+1, err)
		}
	}
	// PRAGMA does not accept bound parameters.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

// canonicalLabels encodes labels as JSON with sorted keys, or "{}" when there
// are none, so series identity does not depend on label order.
func canonicalLabels(labels map[string]string) (string, error) {
	if len(labels) == 0 {
		return "{}", nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json writes map keys in sorted order.
	if err := enc.Encode(labels); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// Write stores pts in one BEGIN IMMEDIATE transaction.
func (s *Store) Write(ctx context.Context, pts []store.Point) ([]store.Rejection, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin write: %w", err)
	}
	defer tx.Rollback()

	rejected := []store.Rejection{}
	types := map[string]string{} // metric name -> stored or in-batch type
	series := map[string]int64{} // metric + "\x00" + labels -> series id
	seriesCount := -1            // loaded on first new series

	for i, p := range pts {
		typ, known := types[p.Name]
		if !known {
			err := tx.QueryRowContext(ctx, `SELECT type FROM metrics WHERE name = ?`, p.Name).Scan(&typ)
			switch {
			case err == nil:
				known = true
				types[p.Name] = typ
			case !errors.Is(err, sql.ErrNoRows):
				return nil, fmt.Errorf("look up metric %s: %w", p.Name, err)
			}
		}
		if known && typ != p.Type {
			rejected = append(rejected, store.Rejection{Index: i, Reason: store.ReasonTypeConflict})
			continue
		}

		labels, err := canonicalLabels(p.Labels)
		if err != nil {
			return nil, fmt.Errorf("encode labels: %w", err)
		}
		key := p.Name + "\x00" + labels
		id, ok := series[key]
		if !ok && known {
			err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE metric = ? AND labels = ?`, p.Name, labels).Scan(&id)
			switch {
			case err == nil:
				ok = true
				series[key] = id
			case !errors.Is(err, sql.ErrNoRows):
				return nil, fmt.Errorf("look up series: %w", err)
			}
		}
		if !ok {
			if seriesCount < 0 {
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM series`).Scan(&seriesCount); err != nil {
					return nil, fmt.Errorf("count series: %w", err)
				}
			}
			if seriesCount >= s.seriesLimit {
				rejected = append(rejected, store.Rejection{Index: i, Reason: store.ReasonSeriesLimitExceeded})
				continue
			}
			if !known {
				if _, err := tx.ExecContext(ctx, `INSERT INTO metrics (name, type) VALUES (?, ?)`, p.Name, p.Type); err != nil {
					return nil, fmt.Errorf("insert metric %s: %w", p.Name, err)
				}
				types[p.Name] = p.Type
			}
			if err := tx.QueryRowContext(ctx,
				`INSERT INTO series (metric, labels) VALUES (?, ?) RETURNING id`, p.Name, labels).Scan(&id); err != nil {
				return nil, fmt.Errorf("insert series: %w", err)
			}
			series[key] = id
			seriesCount++
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO points (series_id, ts, value) VALUES (?, ?, ?)
			 ON CONFLICT (series_id, ts) DO UPDATE SET value = excluded.value`,
			id, p.TS, p.Value); err != nil {
			return nil, fmt.Errorf("insert point: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit write: %w", err)
	}
	return rejected, nil
}

// DeleteBefore removes points older than cutoffMs in chunks, then the series
// and metrics left without points.
func (s *Store) DeleteBefore(ctx context.Context, cutoffMs int64) (int64, error) {
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM points WHERE (series_id, ts) IN (
				SELECT series_id, ts FROM points WHERE ts < ? LIMIT ?)`,
			cutoffMs, s.deleteChunk)
		if err != nil {
			return total, fmt.Errorf("delete points: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(s.deleteChunk) {
			break
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return total, fmt.Errorf("begin cleanup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM series WHERE NOT EXISTS (SELECT 1 FROM points WHERE points.series_id = series.id)`); err != nil {
		return total, fmt.Errorf("delete empty series: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM metrics WHERE NOT EXISTS (SELECT 1 FROM series WHERE series.metric = metrics.name)`); err != nil {
		return total, fmt.Errorf("delete empty metrics: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return total, fmt.Errorf("commit cleanup: %w", err)
	}
	return total, nil
}

// Ping runs a trivial query to confirm the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
