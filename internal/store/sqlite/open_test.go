package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// openTemp opens a fresh store in a per-test temp directory.
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenMigratesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		var version int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 1 {
			t.Errorf("Open #%d: user_version = %d, want 1", i+1, version)
		}
		var tables int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('metrics','series','points')`,
		).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if tables != 3 {
			t.Errorf("Open #%d: found %d tables, want 3", i+1, tables)
		}
		s.Close()
	}
}

func TestOpenPragmas(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	// Pin one connection so all pragmas are read from the same one.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	checks := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "2"}, // FULL
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
	}
	for _, c := range checks {
		var got string
		if err := conn.QueryRowContext(ctx, "PRAGMA "+c.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", c.pragma, err)
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %s, want %s", c.pragma, got, c.want)
		}
	}
}

func TestOpenFailsOnUnusablePath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing-dir", "test.db")); err == nil {
		t.Fatal("expected an error opening a database in a missing directory")
	}
}
