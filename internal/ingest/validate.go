package ingest

import (
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

// Per-point limits from the ingestion spec.
const (
	maxNameLen       = 200
	maxLabels        = 10
	maxLabelValueLen = 128
	maxFutureSkew    = 10 * time.Minute
)

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// rawPoint keeps each field undecoded so a wrong JSON type maps to that
// field's reason code instead of failing the whole point.
type rawPoint struct {
	Name   json.RawMessage `json:"name"`
	Type   json.RawMessage `json:"type"`
	Value  json.RawMessage `json:"value"`
	Labels json.RawMessage `json:"labels"`
	TS     json.RawMessage `json:"ts"`
}

// isNull reports whether a field is absent or JSON null.
func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// validate decodes one point object and checks it against the stateless
// rules. It returns the point, or the reason it was rejected. now is the
// receive time used when ts is absent and for the timestamp bounds.
func validate(raw json.RawMessage, now time.Time, retention time.Duration) (store.Point, string) {
	var rp rawPoint
	if err := json.Unmarshal(raw, &rp); err != nil {
		// The handler only passes JSON objects; a field-level type error
		// cannot happen with RawMessage fields, so this is a malformed object.
		return store.Point{}, store.ReasonInvalidName
	}
	var p store.Point

	if isNull(rp.Name) || json.Unmarshal(rp.Name, &p.Name) != nil ||
		len(p.Name) > maxNameLen || !identRe.MatchString(p.Name) {
		return store.Point{}, store.ReasonInvalidName
	}

	if isNull(rp.Type) || json.Unmarshal(rp.Type, &p.Type) != nil ||
		(p.Type != store.Gauge && p.Type != store.Counter) {
		return store.Point{}, store.ReasonInvalidType
	}

	var value *float64
	if json.Unmarshal(rp.Value, &value) != nil || value == nil ||
		(p.Type == store.Counter && *value < 0) {
		return store.Point{}, store.ReasonInvalidValue
	}
	p.Value = *value

	if !isNull(rp.Labels) {
		if err := json.Unmarshal(rp.Labels, &p.Labels); err != nil {
			return store.Point{}, store.ReasonInvalidLabel
		}
		if len(p.Labels) > maxLabels {
			return store.Point{}, store.ReasonTooManyLabels
		}
		for k, v := range p.Labels {
			if !identRe.MatchString(k) || v == "" || utf8.RuneCountInString(v) > maxLabelValueLen {
				return store.Point{}, store.ReasonInvalidLabel
			}
		}
	}

	nowMs := now.UnixMilli()
	if isNull(rp.TS) {
		p.TS = nowMs
	} else {
		var ts *int64
		if json.Unmarshal(rp.TS, &ts) != nil || ts == nil {
			return store.Point{}, store.ReasonTimestampOutOfRange
		}
		if *ts > nowMs+maxFutureSkew.Milliseconds() || *ts < nowMs-retention.Milliseconds() {
			return store.Point{}, store.ReasonTimestampOutOfRange
		}
		p.TS = *ts
	}

	return p, ""
}
