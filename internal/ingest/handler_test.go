package ingest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store/sqlite"
)

const testToken = "secret"

type fixture struct {
	srv    *httptest.Server
	dbPath string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &Handler{Store: st, Token: testToken, Retention: testRetention, Now: func() time.Time { return testNow }}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, dbPath: dbPath}
}

// post sends body with the given Authorization header ("" omits it).
func (f *fixture) post(t *testing.T, auth, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func (f *fixture) postAuthed(t *testing.T, body string) (int, string) {
	return f.post(t, "Bearer "+testToken, body)
}

// storedPoints queries the database file directly, bypassing the handler.
func (f *fixture) storedPoints(t *testing.T) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT m.name, m.type, se.labels, p.value, p.ts
		FROM points p JOIN series se ON se.id = p.series_id JOIN metrics m ON m.name = se.metric
		ORDER BY m.name, se.labels, p.ts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, typ, labels string
		var value float64
		var ts int64
		if err := rows.Scan(&name, &typ, &labels, &value, &ts); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %s %g %d", name, typ, labels, value, ts))
	}
	return out
}

func (f *fixture) assertNothingStored(t *testing.T) {
	t.Helper()
	if pts := f.storedPoints(t); len(pts) != 0 {
		t.Errorf("expected no stored points, found %v", pts)
	}
}

const validBatch = `[{"name":"queue_depth","type":"gauge","value":42,"labels":{"host":"pi-1"},"ts":1759480000000}]`

func gaugeBatch(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf(`{"name":"m","type":"gauge","value":%d,"ts":%d}`, i, nowMs-int64(i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestIngestAuth(t *testing.T) {
	tests := []struct {
		name string
		auth string
	}{
		{"missing token", ""},
		{"wrong token", "Bearer wrong"},
		{"wrong scheme", "Basic " + testToken},
		{"token without scheme", testToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			status, _ := f.post(t, tt.auth, validBatch)
			if status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", status)
			}
			f.assertNothingStored(t)
		})
	}
}

func TestIngestValidBatch(t *testing.T) {
	f := newFixture(t)
	status, body := f.postAuthed(t, validBatch)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != `{"accepted":1,"rejected":[]}` {
		t.Errorf("body = %s", body)
	}
	want := []string{`queue_depth gauge {"host":"pi-1"} 42 1759480000000`}
	if got := f.storedPoints(t); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored %v, want %v", got, want)
	}
}

func TestIngestMissingTimestampUsesReceiveTime(t *testing.T) {
	f := newFixture(t)
	status, _ := f.postAuthed(t, `[{"name":"m","type":"gauge","value":1}]`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	want := []string{fmt.Sprintf(`m gauge {} 1 %d`, nowMs)}
	if got := f.storedPoints(t); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored %v, want %v", got, want)
	}
}

func TestIngestRequestErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"malformed JSON", `{not json`, http.StatusBadRequest},
		{"object not array", `{"name":"m","type":"gauge","value":1}`, http.StatusBadRequest},
		{"null body", `null`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"empty array", `[]`, http.StatusBadRequest},
		{"array of non-objects", `[1,2]`, http.StatusBadRequest},
		{"valid point then non-object", `[{"name":"m","type":"gauge","value":1},null]`, http.StatusBadRequest},
		{"trailing garbage", validBatch + `x`, http.StatusBadRequest},
		{"5,001 points", gaugeBatch(5001), http.StatusRequestEntityTooLarge},
		{"body over 5 MiB", `[{"name":"m","type":"gauge","value":1,"labels":{"k":"` + strings.Repeat("x", MaxBodyBytes) + `"}}]`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			status, body := f.postAuthed(t, tt.body)
			if status != tt.status {
				t.Errorf("status = %d, want %d (body %s)", status, tt.status, body)
			}
			f.assertNothingStored(t)
		})
	}
}

func TestIngestMaxBatchAccepted(t *testing.T) {
	f := newFixture(t)
	status, body := f.postAuthed(t, gaugeBatch(MaxPoints))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.200s)", status, body)
	}
	if n := len(f.storedPoints(t)); n != MaxPoints {
		t.Errorf("stored %d points, want %d", n, MaxPoints)
	}
}

func TestIngestPerPointRejections(t *testing.T) {
	hourAhead := nowMs + 3600*1000
	eightDaysAgo := nowMs - 8*24*3600*1000
	tests := []struct {
		name   string
		point  string
		reason string
	}{
		{"invalid name", `{"name":"2xx-rate","type":"gauge","value":1}`, "invalid_name"},
		{"unknown type", `{"name":"m","type":"histogram","value":1}`, "invalid_type"},
		{"negative counter", `{"name":"m","type":"counter","value":-1}`, "invalid_value"},
		{"too many labels", `{"name":"m","type":"gauge","value":1,"labels":` + manyLabels(11) + `}`, "too_many_labels"},
		{"label value too long", `{"name":"m","type":"gauge","value":1,"labels":{"k":"` + strings.Repeat("x", 129) + `"}}`, "invalid_label"},
		{"future timestamp", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, hourAhead), "timestamp_out_of_range"},
		{"expired timestamp", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, eightDaysAgo), "timestamp_out_of_range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			status, body := f.postAuthed(t, "["+tt.point+"]")
			if status != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", status)
			}
			want := fmt.Sprintf(`{"accepted":0,"rejected":[{"index":0,"reason":%q}]}`, tt.reason)
			if body != want {
				t.Errorf("body = %s, want %s", body, want)
			}
			f.assertNothingStored(t)
		})
	}
}

func TestIngestMixedBatch(t *testing.T) {
	f := newFixture(t)
	status, body := f.postAuthed(t, `[
		{"name":"a","type":"gauge","value":1,"ts":1759480000000},
		{"name":"2xx-rate","type":"gauge","value":1},
		{"name":"b","type":"counter","value":2,"ts":1759480000000}
	]`)
	if status != http.StatusMultiStatus {
		t.Errorf("status = %d, want 207", status)
	}
	if want := `{"accepted":2,"rejected":[{"index":1,"reason":"invalid_name"}]}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
	want := []string{`a gauge {} 1 1759480000000`, `b counter {} 2 1759480000000`}
	if got := f.storedPoints(t); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored %v, want %v", got, want)
	}
}

func TestIngestAllRejected(t *testing.T) {
	f := newFixture(t)
	status, body := f.postAuthed(t, `[{"name":"2xx","type":"gauge","value":1},{"name":"m","type":"bogus","value":1}]`)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", status)
	}
	if want := `{"accepted":0,"rejected":[{"index":0,"reason":"invalid_name"},{"index":1,"reason":"invalid_type"}]}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
	f.assertNothingStored(t)
}

func TestIngestTypeConflictWithStored(t *testing.T) {
	f := newFixture(t)
	f.postAuthed(t, `[{"name":"jobs_done","type":"counter","value":1}]`)
	status, body := f.postAuthed(t, `[{"name":"jobs_done","type":"gauge","value":1}]`)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", status)
	}
	if want := `{"accepted":0,"rejected":[{"index":0,"reason":"type_conflict"}]}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestIngestTypeConflictInBatch(t *testing.T) {
	f := newFixture(t)
	// The invalid point at index 0 checks that store rejections are mapped
	// back to their original batch index.
	status, body := f.postAuthed(t, `[
		{"name":"2xx","type":"gauge","value":1},
		{"name":"new_metric","type":"counter","value":1},
		{"name":"new_metric","type":"gauge","value":1}
	]`)
	if status != http.StatusMultiStatus {
		t.Errorf("status = %d, want 207", status)
	}
	want := `{"accepted":1,"rejected":[{"index":0,"reason":"invalid_name"},{"index":2,"reason":"type_conflict"}]}`
	if body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestIngestSeriesLimit(t *testing.T) {
	f := newFixture(t)
	// 10,000 distinct series in two maximum-size batches.
	for batch := range 2 {
		parts := make([]string, MaxPoints)
		for i := range MaxPoints {
			parts[i] = fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"labels":{"i":"%d"}}`, batch*MaxPoints+i)
		}
		if status, body := f.postAuthed(t, "["+strings.Join(parts, ",")+"]"); status != http.StatusOK {
			t.Fatalf("seeding batch %d: status %d (body %.200s)", batch, status, body)
		}
	}
	status, body := f.postAuthed(t, `[
		{"name":"m","type":"gauge","value":2,"labels":{"i":"0"}},
		{"name":"m","type":"gauge","value":2,"labels":{"i":"new"}}
	]`)
	if status != http.StatusMultiStatus {
		t.Errorf("status = %d, want 207", status)
	}
	if want := `{"accepted":1,"rejected":[{"index":1,"reason":"series_limit_exceeded"}]}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestIngestResponseIsJSON(t *testing.T) {
	f := newFixture(t)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL, strings.NewReader(validBatch))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var r Response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
}
