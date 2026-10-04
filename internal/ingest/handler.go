// Package ingest implements POST /api/v1/ingest: authentication, payload
// decoding, per-point validation, and the partial-success response.
package ingest

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/shashanksbs45/pulseboard/internal/store"
)

// Request-level limits from the ingestion spec.
const (
	MaxBodyBytes = 5 << 20 // 5 MiB
	MaxPoints    = 5000
)

// Handler serves the ingest endpoint.
type Handler struct {
	Store     store.Store
	Token     string
	Retention time.Duration
	Now       func() time.Time // injectable clock; defaults to time.Now
	Logger    *slog.Logger     // defaults to slog.Default()
}

// Response is the body of a 200, 207 or 422 ingest response.
type Response struct {
	Accepted int               `json:"accepted"`
	Rejected []store.Rejection `json:"rejected"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 5 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	var raws []json.RawMessage
	if firstByte(body) != '[' || json.Unmarshal(body, &raws) != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON array of point objects")
		return
	}
	if len(raws) == 0 {
		writeError(w, http.StatusBadRequest, "batch is empty")
		return
	}
	if len(raws) > MaxPoints {
		writeError(w, http.StatusRequestEntityTooLarge, "batch exceeds 5,000 points")
		return
	}
	for _, raw := range raws {
		if firstByte(raw) != '{' {
			writeError(w, http.StatusBadRequest, "body must be a JSON array of point objects")
			return
		}
	}

	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	receivedAt := now()

	rejected := []store.Rejection{}
	valid := make([]store.Point, 0, len(raws))
	origIndex := make([]int, 0, len(raws)) // valid[i] came from raws[origIndex[i]]
	for i, raw := range raws {
		p, reason := validate(raw, receivedAt, h.Retention)
		if reason != "" {
			rejected = append(rejected, store.Rejection{Index: i, Reason: reason})
			continue
		}
		valid = append(valid, p)
		origIndex = append(origIndex, i)
	}

	if len(valid) > 0 {
		storeRejected, err := h.Store.Write(r.Context(), valid)
		if err != nil {
			h.logger().Error("ingest write failed", "err", err, "points", len(valid))
			writeError(w, http.StatusInternalServerError, "failed to store points")
			return
		}
		for _, rej := range storeRejected {
			rejected = append(rejected, store.Rejection{Index: origIndex[rej.Index], Reason: rej.Reason})
		}
		sort.Slice(rejected, func(i, j int) bool { return rejected[i].Index < rejected[j].Index })
	}

	resp := Response{Accepted: len(raws) - len(rejected), Rejected: rejected}
	status := http.StatusOK
	switch {
	case resp.Accepted == 0:
		status = http.StatusUnprocessableEntity
	case len(rejected) > 0:
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, resp)
}

func (h *Handler) authorized(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	want := "Bearer " + h.Token
	return h.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// firstByte returns the first non-whitespace byte of b, or 0.
func firstByte(b []byte) byte {
	b = bytes.TrimLeft(b, " \t\r\n")
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
