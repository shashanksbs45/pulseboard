package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

// dump reads every stored point back, ordered by name, labels and ts.
func dump(t *testing.T, s *Store) []store.Point {
	t.Helper()
	rows, err := s.db.Query(`
		SELECT m.name, m.type, se.labels, p.value, p.ts
		FROM points p JOIN series se ON se.id = p.series_id JOIN metrics m ON m.name = se.metric
		ORDER BY m.name, se.labels, p.ts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pts := []store.Point{}
	for rows.Next() {
		var p store.Point
		var labels string
		if err := rows.Scan(&p.Name, &p.Type, &labels, &p.Value, &p.TS); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(labels), &p.Labels); err != nil {
			t.Fatal(err)
		}
		if len(p.Labels) == 0 {
			p.Labels = nil
		}
		pts = append(pts, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return pts
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func write(t *testing.T, s *Store, pts ...store.Point) []store.Rejection {
	t.Helper()
	rej, err := s.Write(context.Background(), pts)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return rej
}

func gauge(name string, ts int64, v float64, labels map[string]string) store.Point {
	return store.Point{Name: name, Type: store.Gauge, Labels: labels, Value: v, TS: ts}
}

func counter(name string, ts int64, v float64, labels map[string]string) store.Point {
	return store.Point{Name: name, Type: store.Counter, Labels: labels, Value: v, TS: ts}
}

func TestWriteStoresPoints(t *testing.T) {
	s, _ := openTemp(t)
	rej := write(t, s,
		gauge("queue_depth", 1000, 42, map[string]string{"host": "pi-1"}),
		gauge("queue_depth", 2000, 43, map[string]string{"host": "pi-1"}),
		counter("jobs_done", 1000, 7, nil),
	)
	if len(rej) != 0 {
		t.Fatalf("unexpected rejections: %v", rej)
	}
	want := []store.Point{
		counter("jobs_done", 1000, 7, nil),
		gauge("queue_depth", 1000, 42, map[string]string{"host": "pi-1"}),
		gauge("queue_depth", 2000, 43, map[string]string{"host": "pi-1"}),
	}
	if got := dump(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("stored %+v, want %+v", got, want)
	}
	if n := count(t, s, "series"); n != 2 {
		t.Errorf("series = %d, want 2", n)
	}
}

func TestWriteLabelOrderSameSeries(t *testing.T) {
	s, _ := openTemp(t)
	// Separate batches so the in-transaction cache can't hide a lookup bug.
	write(t, s, gauge("m", 1, 1, map[string]string{"a": "1", "b": "2"}))
	write(t, s, gauge("m", 2, 2, map[string]string{"b": "2", "a": "1"}))
	if n := count(t, s, "series"); n != 1 {
		t.Errorf("series = %d, want 1", n)
	}
}

func TestWriteTypeConflictWithStored(t *testing.T) {
	s, _ := openTemp(t)
	write(t, s, counter("jobs_done", 1, 1, nil))
	rej := write(t, s, gauge("jobs_done", 2, 1, nil), counter("jobs_done", 3, 2, nil))
	want := []store.Rejection{{Index: 0, Reason: store.ReasonTypeConflict}}
	if !reflect.DeepEqual(rej, want) {
		t.Errorf("rejections = %v, want %v", rej, want)
	}
	if n := len(dump(t, s)); n != 2 {
		t.Errorf("stored %d points, want 2", n)
	}
}

func TestWriteTypeConflictInBatch(t *testing.T) {
	s, _ := openTemp(t)
	rej := write(t, s, counter("new_metric", 1, 1, nil), gauge("new_metric", 2, 1, nil))
	want := []store.Rejection{{Index: 1, Reason: store.ReasonTypeConflict}}
	if !reflect.DeepEqual(rej, want) {
		t.Errorf("rejections = %v, want %v", rej, want)
	}
	got := dump(t, s)
	if len(got) != 1 || got[0].Type != store.Counter {
		t.Errorf("stored %+v, want one counter point", got)
	}
}

func TestWriteSeriesLimit(t *testing.T) {
	s, _ := openTemp(t)
	s.seriesLimit = 2
	write(t, s,
		gauge("m", 1, 1, map[string]string{"i": "1"}),
		gauge("m", 1, 1, map[string]string{"i": "2"}),
	)
	rej := write(t, s,
		gauge("m", 2, 2, map[string]string{"i": "1"}), // existing series
		gauge("m", 2, 2, map[string]string{"i": "3"}), // new series, over the limit
		gauge("other", 2, 2, nil),                     // new metric, over the limit
	)
	want := []store.Rejection{
		{Index: 1, Reason: store.ReasonSeriesLimitExceeded},
		{Index: 2, Reason: store.ReasonSeriesLimitExceeded},
	}
	if !reflect.DeepEqual(rej, want) {
		t.Errorf("rejections = %v, want %v", rej, want)
	}
	if n := count(t, s, "series"); n != 2 {
		t.Errorf("series = %d, want 2", n)
	}
	// A metric rejected for the series limit must not claim its type.
	if n := count(t, s, "metrics"); n != 1 {
		t.Errorf("metrics = %d, want 1", n)
	}
	if n := len(dump(t, s)); n != 3 {
		t.Errorf("stored %d points, want 3", n)
	}
}

func TestWriteSeriesLimitWithinBatch(t *testing.T) {
	s, _ := openTemp(t)
	s.seriesLimit = 1
	rej := write(t, s,
		gauge("m", 1, 1, map[string]string{"i": "1"}),
		gauge("m", 1, 1, map[string]string{"i": "2"}),
		gauge("m", 2, 1, map[string]string{"i": "1"}),
	)
	want := []store.Rejection{{Index: 1, Reason: store.ReasonSeriesLimitExceeded}}
	if !reflect.DeepEqual(rej, want) {
		t.Errorf("rejections = %v, want %v", rej, want)
	}
}

func TestWriteDuplicateTimestampOverwrites(t *testing.T) {
	s, _ := openTemp(t)
	labels := map[string]string{"host": "a"}
	write(t, s, gauge("m", 100, 5, labels))
	write(t, s, gauge("m", 100, 7, labels))
	got := dump(t, s)
	want := []store.Point{gauge("m", 100, 7, labels)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored %+v, want %+v", got, want)
	}
}

func TestWriteDurableAcrossReopen(t *testing.T) {
	s, path := openTemp(t)
	in := []store.Point{
		counter("jobs_done", 1759480000123, 12, map[string]string{"queue": "emails", "host": "pi-1"}),
		gauge("queue_depth", 1759480000000, 42.5, nil),
		gauge("temp_c", 1759480000001, -3.25, map[string]string{"room": "Küche"}),
	}
	write(t, s, in...)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := dump(t, reopened)
	sort.Slice(in, func(i, j int) bool { return in[i].Name < in[j].Name })
	if !reflect.DeepEqual(got, in) {
		t.Errorf("after reopen got %+v, want %+v", got, in)
	}
}

func TestWriteConcurrentLastSeriesSlot(t *testing.T) {
	s, _ := openTemp(t)
	s.seriesLimit = 1
	const writers = 8

	var wg sync.WaitGroup
	results := make([][]store.Rejection, writers)
	errs := make([]error, writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = s.Write(context.Background(),
				[]store.Point{gauge("m", 1, 1, map[string]string{"writer": fmt.Sprint(i)})})
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range writers {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		switch {
		case len(results[i]) == 0:
			winners++
		case results[i][0].Reason != store.ReasonSeriesLimitExceeded:
			t.Errorf("writer %d rejected with %s", i, results[i][0].Reason)
		}
	}
	if winners != 1 {
		t.Errorf("%d writers won the last series slot, want 1", winners)
	}
	if n := count(t, s, "series"); n != 1 {
		t.Errorf("series = %d, want 1", n)
	}
}

func TestDeleteBefore(t *testing.T) {
	s, _ := openTemp(t)
	s.deleteChunk = 2 // exercise several chunks
	write(t, s,
		gauge("old_metric", 10, 1, nil),
		gauge("old_metric", 20, 1, nil),
		gauge("old_metric", 30, 1, nil),
		gauge("mixed", 10, 1, map[string]string{"s": "old"}),
		gauge("mixed", 15, 1, map[string]string{"s": "new"}),
		gauge("mixed", 100, 1, map[string]string{"s": "new"}),
	)

	n, err := s.DeleteBefore(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("deleted %d points, want 5", n)
	}
	want := []store.Point{gauge("mixed", 100, 1, map[string]string{"s": "new"})}
	if got := dump(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("remaining %+v, want %+v", got, want)
	}
	if c := count(t, s, "series"); c != 1 {
		t.Errorf("series = %d, want 1", c)
	}
	if c := count(t, s, "metrics"); c != 1 {
		t.Errorf("metrics = %d, want 1", c)
	}
}

func TestDeleteBeforeFreesSeriesSlot(t *testing.T) {
	s, _ := openTemp(t)
	s.seriesLimit = 2
	write(t, s, gauge("old_metric", 10, 1, nil), gauge("live", 100, 1, nil))
	if rej := write(t, s, gauge("fresh", 100, 1, nil)); len(rej) != 1 {
		t.Fatalf("expected the limit to be reached first, got %v", rej)
	}
	if _, err := s.DeleteBefore(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if rej := write(t, s, gauge("fresh", 100, 1, nil)); len(rej) != 0 {
		t.Errorf("new series rejected after expiry: %v", rej)
	}
}

func TestDeleteBeforeReleasesType(t *testing.T) {
	s, _ := openTemp(t)
	write(t, s, counter("old_metric", 10, 1, nil))
	if _, err := s.DeleteBefore(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if rej := write(t, s, gauge("old_metric", 100, 1, nil)); len(rej) != 0 {
		t.Errorf("gauge rejected after counter expired: %v", rej)
	}
}

func TestPingAndClose(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on open store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping succeeded on a closed store")
	}
}
