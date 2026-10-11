// Package store defines the storage boundary shared by ingest, retention and
// the server. Only store/sqlite talks to the database.
package store

import "context"

// Metric types.
const (
	Gauge   = "gauge"
	Counter = "counter"
)

// Rejection reason codes. This is the closed set returned to ingest clients.
const (
	ReasonInvalidName         = "invalid_name"
	ReasonInvalidType         = "invalid_type"
	ReasonInvalidValue        = "invalid_value"
	ReasonInvalidLabel        = "invalid_label"
	ReasonTooManyLabels       = "too_many_labels"
	ReasonTimestampOutOfRange = "timestamp_out_of_range"
	ReasonTypeConflict        = "type_conflict"
	ReasonSeriesLimitExceeded = "series_limit_exceeded"
)

// Point is one validated metric sample.
type Point struct {
	Name   string
	Type   string // Gauge or Counter
	Labels map[string]string
	Value  float64
	TS     int64 // Unix time in milliseconds
}

// Rejection reports why the point at Index was not stored.
type Rejection struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// Store persists points and enforces the checks that depend on stored state.
type Store interface {
	// Write stores pts in one transaction. It returns a Rejection (indexed
	// into pts) for each point rejected with ReasonTypeConflict or
	// ReasonSeriesLimitExceeded; every other point is stored. A non-nil error
	// means nothing was stored.
	Write(ctx context.Context, pts []Point) ([]Rejection, error)
	// DeleteBefore removes points with TS < cutoffMs, then any series and
	// metrics left without points. It returns the number of points deleted.
	DeleteBefore(ctx context.Context, cutoffMs int64) (int64, error)
	// Ping reports whether the database is reachable.
	Ping(ctx context.Context) error
	Close() error
}
