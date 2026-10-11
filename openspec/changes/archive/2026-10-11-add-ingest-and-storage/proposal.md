# Proposal

## Why

Pulseboard has no code yet. Before anything can be queried or visualized, applications need a way to push metrics and have them durably stored. This change lays that foundation: a single Go binary that accepts metric pushes over HTTP and persists them, sized for a homelab (~10 sources, ~1k points/sec).

## What Changes

- New Go module and `pulseboard serve` command that starts one HTTP server, configured via flags or environment variables.
- New `POST /api/v1/ingest` endpoint accepting JSON batches (up to 5,000 points) of `gauge` and `counter` points with optional labels and optional millisecond timestamps.
- Static bearer-token authentication on the ingest endpoint.
- Validation with Prometheus-style naming rules and hard limits (name ≤200 chars, ≤10 labels per point, label value ≤128 chars, ≤10,000 active series). Invalid points are rejected per index; valid points in the same batch are still accepted.
- A metric name keeps the type it was first stored with; points that conflict with it are rejected.
- Durable storage in a single SQLite file behind a storage interface.
- 7-day retention of raw points, enforced by a background deletion job.

Out of scope (later changes): query and discovery APIs, dashboard, histograms, downsampling, OTLP/Prometheus compatibility, dashboard auth, Dockerfile and CI.

## Capabilities

### New Capabilities
- `server`: How the pulseboard process starts, is configured, and shuts down.
- `ingestion`: The HTTP contract for pushing metric points: authentication, payload format, validation rules, limits, and per-point results.
- `storage`: How accepted points are persisted, how metric types and series are tracked, and how long data is retained.

### Modified Capabilities
<!-- None: no specs exist yet. -->

## Impact

- **Code:** new Go module at the repo root (`cmd/pulseboard`, `internal/...`).
- **APIs:** introduces `POST /api/v1/ingest` and a health endpoint.
- **Dependencies:** Go toolchain (1.26 is installed locally), `modernc.org/sqlite` (pure Go, no CGO).
- **Docs:** once code lands, `AGENTS.md` gains the real build and test commands (`go build`, `go test`).
- **Data:** creates a SQLite database file at a configurable path.
