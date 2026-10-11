package ingest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

const nowMs = 1759480000000

var (
	testNow       = time.UnixMilli(nowMs)
	testRetention = 7 * 24 * time.Hour
)

func manyLabels(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf(`"l%d":"v"`, i)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestValidateReasons(t *testing.T) {
	hourAhead := nowMs + time.Hour.Milliseconds()
	eightDaysAgo := nowMs - 8*24*time.Hour.Milliseconds()
	tests := []struct {
		name string
		raw  string
		want string // "" means accepted
	}{
		// Accepted shapes.
		{"valid gauge", `{"name":"queue_depth","type":"gauge","value":42,"labels":{"host":"pi-1"},"ts":1759480000000}`, ""},
		{"valid counter zero", `{"name":"jobs","type":"counter","value":0}`, ""},
		{"negative gauge ok", `{"name":"temp","type":"gauge","value":-3.5}`, ""},
		{"null labels ok", `{"name":"m","type":"gauge","value":1,"labels":null}`, ""},
		{"10 labels ok", `{"name":"m","type":"gauge","value":1,"labels":` + manyLabels(10) + `}`, ""},
		{"128-char label ok", `{"name":"m","type":"gauge","value":1,"labels":{"k":"` + strings.Repeat("x", 128) + `"}}`, ""},
		{"128 multibyte chars ok", `{"name":"m","type":"gauge","value":1,"labels":{"k":"` + strings.Repeat("ü", 128) + `"}}`, ""},
		{"200-char name ok", `{"name":"` + strings.Repeat("a", 200) + `","type":"gauge","value":1}`, ""},
		{"ts 10 min ahead ok", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, nowMs+10*60*1000), ""},
		{"ts at retention edge ok", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, nowMs-testRetention.Milliseconds()), ""},

		// Names.
		{"invalid name 2xx-rate", `{"name":"2xx-rate","type":"gauge","value":1}`, store.ReasonInvalidName},
		{"missing name", `{"type":"gauge","value":1}`, store.ReasonInvalidName},
		{"empty name", `{"name":"","type":"gauge","value":1}`, store.ReasonInvalidName},
		{"non-string name", `{"name":5,"type":"gauge","value":1}`, store.ReasonInvalidName},
		{"201-char name", `{"name":"` + strings.Repeat("a", 201) + `","type":"gauge","value":1}`, store.ReasonInvalidName},

		// Types.
		{"unknown type histogram", `{"name":"m","type":"histogram","value":1}`, store.ReasonInvalidType},
		{"missing type", `{"name":"m","value":1}`, store.ReasonInvalidType},
		{"non-string type", `{"name":"m","type":1,"value":1}`, store.ReasonInvalidType},

		// Values.
		{"negative counter", `{"name":"m","type":"counter","value":-1}`, store.ReasonInvalidValue},
		{"missing value", `{"name":"m","type":"gauge"}`, store.ReasonInvalidValue},
		{"null value", `{"name":"m","type":"gauge","value":null}`, store.ReasonInvalidValue},
		{"string value", `{"name":"m","type":"gauge","value":"42"}`, store.ReasonInvalidValue},
		{"overflowing value", `{"name":"m","type":"gauge","value":1e400}`, store.ReasonInvalidValue},

		// Labels.
		{"11 labels", `{"name":"m","type":"gauge","value":1,"labels":` + manyLabels(11) + `}`, store.ReasonTooManyLabels},
		{"129-char label value", `{"name":"m","type":"gauge","value":1,"labels":{"k":"` + strings.Repeat("x", 129) + `"}}`, store.ReasonInvalidLabel},
		{"empty label value", `{"name":"m","type":"gauge","value":1,"labels":{"k":""}}`, store.ReasonInvalidLabel},
		{"bad label key", `{"name":"m","type":"gauge","value":1,"labels":{"bad-key":"v"}}`, store.ReasonInvalidLabel},
		{"non-string label value", `{"name":"m","type":"gauge","value":1,"labels":{"k":1}}`, store.ReasonInvalidLabel},
		{"labels not object", `{"name":"m","type":"gauge","value":1,"labels":["a"]}`, store.ReasonInvalidLabel},

		// Timestamps.
		{"ts 1 hour ahead", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, hourAhead), store.ReasonTimestampOutOfRange},
		{"ts 8 days ago", fmt.Sprintf(`{"name":"m","type":"gauge","value":1,"ts":%d}`, eightDaysAgo), store.ReasonTimestampOutOfRange},
		{"fractional ts", `{"name":"m","type":"gauge","value":1,"ts":1759480000000.5}`, store.ReasonTimestampOutOfRange},
		{"string ts", `{"name":"m","type":"gauge","value":1,"ts":"now"}`, store.ReasonTimestampOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, reason := validate(json.RawMessage(tt.raw), testNow, testRetention)
			if reason != tt.want {
				t.Errorf("reason = %q, want %q", reason, tt.want)
			}
		})
	}
}

func TestValidateDecodesPoint(t *testing.T) {
	p, reason := validate(json.RawMessage(
		`{"name":"queue_depth","type":"gauge","value":42.5,"labels":{"host":"pi-1"},"ts":1759480000123}`,
	), testNow, testRetention)
	if reason != "" {
		t.Fatalf("rejected: %s", reason)
	}
	want := store.Point{Name: "queue_depth", Type: store.Gauge, Labels: map[string]string{"host": "pi-1"}, Value: 42.5, TS: 1759480000123}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestValidateMissingTimestampUsesReceiveTime(t *testing.T) {
	p, reason := validate(json.RawMessage(`{"name":"m","type":"gauge","value":1}`), testNow, testRetention)
	if reason != "" {
		t.Fatalf("rejected: %s", reason)
	}
	if p.TS != nowMs {
		t.Errorf("ts = %d, want %d", p.TS, nowMs)
	}
}
