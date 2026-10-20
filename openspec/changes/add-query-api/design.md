# Design

## Context

See proposal.md for motivation and specs/ for the behavior contract. This document covers how the read side fits into the existing binary.

What exists today:
- `internal/server` builds one `http.ServeMux`. It routes `POST /api/v1/ingest` and `GET /healthz`, and owns the shutdown order.
- `internal/store` defines a `Store` interface with only write, retention, ping and close methods. `internal/store/sqlite` is the only code that touches the database.
- The schema has three tables. `metrics(name PK, type)`. `series(id INTEGER PK, metric, labels UNIQUE per metric)`, where `labels` is canonical JSON with sorted keys. `points(series_id, ts, value)` with `PRIMARY KEY (series_id, ts) WITHOUT ROWID`, plus an index on `ts` for retention.
- Retention deletes points in chunks, then deletes series and metrics that have no points left.
- The total number of series is capped at 10,000.
- Ingest errors are JSON `{"error":"<message>"}` and the bearer check uses `crypto/subtle`.
- `scripts/check.sh` already has `openapi_lint` (Redocly via `npx`) and `openapi_contract` (`go test -run Contract ./...`), both skipped until `openapi.yaml` exists.

## Goals / Non-Goals

**Goals:**
- Serve the four read endpoints from the existing binary and database, with no schema migration.
- Keep every page of `/query` a bounded amount of work: indexed range scans on `(series_id, ts)`, and no `OFFSET`.
- Keep read authorization behind a single middleware that the future entitlements system can replace without touching handlers.
- Keep parameter parsing, cursor encoding and bucketing as pure functions that can be table-tested without HTTP or SQLite.

**Non-Goals:**
- Caching, precomputed rollups or downsampling. Bucketing reads raw points every time.
- A read-specific timeout or concurrency limit. The request context, which is cancelled when the client disconnects or the server shuts down, is the only bound beyond the page limit.
- Changing the ingest error format to match the read error format.

## Architecture (C4)

The editable C4 container diagram is [`c4-container.drawio`](c4-container.drawio). Open it in draw.io desktop or at app.diagrams.net. The text version below shows the same elements and relationships. Items marked **NEW** are added by this change.

```
     [Homelab Operator]                    [Metrics Reader]  NEW
            │ Configures and stops                 │ Lists metrics, labels, series; queries points
            │ [CLI flags, env vars, signals]       │ [GET /api/v1/metrics*, /api/v1/query,
            │                                      │  HTTP/JSON, optional read token]  NEW
┌ Pulseboard [Software System] ──▼─────────────────▼────────────────────────────────────────────┐
│  ┌──────────────────────────────────┐  Writes points; reads metrics,  ┌──────────────────────┐ │
│  │ Pulseboard Server                │  series and points [SQL]  NEW   │ Metrics Database     │ │
│  │ [Go 1.26, pulseboard serve]      │ ──────────────────────────────▶ │ [SQLite WAL file]    │ │
│  │ Ingests points, serves discovery │  Deletes expired points,        │ metrics, series,     │ │
│  │ and range queries, runs          │  hourly [SQL]                   │ points (schema       │ │
│  │ retention, reports health        │ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ▶ │ unchanged)           │ │
│  └──────────────────────────────────┘                                 └──────────────────────┘ │
└─────────────▲──────────────────────▲──────────────────────────────────────────────────────────┘
              │ Pushes points        │ Probes health
              │ [POST /api/v1/ingest │ [GET /healthz, HTTP, no auth]
              │  HTTP/JSON + token]  │
      [Metric Sources]       [Health Checker]
      (external system)      (external system)

  ──▶ synchronous     ─ ─▶ asynchronous
```

This change adds one person, the **Metrics Reader**, and one relationship: Metrics Reader → Pulseboard Server, `GET /api/v1/metrics*` and `/api/v1/query` over HTTP/JSON with an optional read token. The Server → Metrics Database relationship now reads as well as writes. No container is added and the database schema doesn't change.

Inside the Pulseboard Server container, the components change as follows:

| Component | Package | Responsibility | Depends on |
|---|---|---|---|
| HTTP Server | `internal/server` | Routes the four read patterns through the read-auth middleware | ingest, query, store |
| **Query** (new) | `internal/query` | Read handlers, parameter validation, cursors, bucketing, error bodies | store (`Reader` interface only) |
| Store | `internal/store` | Gains a `Reader` interface and read model types | — |
| SQLite Store | `internal/store/sqlite` | Implements `Reader` | store, Metrics Database |
| Config | `internal/config` | Gains `ReadToken` | — |
| Entrypoint | `cmd/pulseboard` | Builds the query handler and passes it, with the read token, to `server.New` | all of the above |

## Decisions

### Store: a separate `Reader` interface

```go
type Reader interface {
    Metrics(ctx context.Context) ([]Metric, error)                    // name + type, sorted by name
    Series(ctx context.Context, name string) (typ string, s []Series, err error) // typ "" when unknown; Series{ID, Labels}, by ID
    ReadPoints(ctx context.Context, q PointRange, fn func(seriesID, ts int64, v float64) error) error
    CountPoints(ctx context.Context, q PointRange, stepMs int64) (int64, error)
    PrevPoint(ctx context.Context, seriesID, afterOrAt, before int64) (ts int64, v float64, ok bool, err error)
}
type Store interface { Reader; /* existing methods */ }
```

`PointRange` holds the matching series IDs in ascending order, the inclusive `[start, end]`, and the keyset position to continue after. `query` depends on `Reader` only, so tests can fake it and the planned Postgres store would implement the same interface.

*Alternative:* adding the methods to `Store` directly. That works, but it gives `ingest` and `retention` read methods they don't need.

### Series selection happens in Go, not SQL

`Series(name)` loads every series of a metric. There are at most 10,000, about 1 MB of JSON labels at worst, and one indexed lookup on `UNIQUE(metric, labels)` finds them. The handler keeps the series whose labels contain every filter pair. The labels endpoint is built from the same list.

*Alternatives:*
- `json_extract` in SQL: it can't use an index on a subset of labels either, and it moves the matching rules into SQL strings.
- A `series_labels(series_id, key, value)` table: that needs a schema migration, which the query API doesn't need at today's cap. If the series cap grows a lot, that table becomes worthwhile.

### Points: one keyset range scan per series

For each matching series in ID order, starting at the cursor's series:

```sql
SELECT ts, value FROM points
WHERE series_id = ? AND ts BETWEEN ? AND ? AND ts > ?   -- last clause only on the cursor's series
ORDER BY ts LIMIT ?
```

This uses the `(series_id, ts)` primary key directly and stops as soon as the page is full. Every page costs the same no matter how deep the walk is. That's the reason for the "no offset" decision.

The queries for one page run inside one read transaction, so the page sees a consistent snapshot. Across pages, the keyset position (rather than a snapshot) guarantees that no point repeats and that points stored for the whole walk aren't skipped. WAL mode means these reads never block ingest.

*Alternative:* one query with `series_id IN (...)` and a row-value comparison `(series_id, ts) > (?, ?)`. It's fewer round trips, but with thousands of IDs SQLite's planner may not do a clean range scan per series. In-process round trips are cheap.

### Bucketing: streamed in Go, one code path for every aggregation

The handler streams raw rows from `ReadPoints` through an aggregator that keeps one open bucket per series. It emits `[bucketStart, value]` when the bucket's start time changes, and stops once the page holds `limit` buckets. `bucketStart = floorDiv(ts, step) * step`, which floors correctly for negative values too.

- **Gauges:** `avg` keeps a running sum and count. `min`, `max` and `sum` are folds. `last` keeps the value with the greatest timestamp.
- **Counters:** `increase` keeps the previous value. Each point adds `v - prev`, or `v` when `v < prev` (a reset). The first point in the query range adds 0. `rate = increase / (step / 1000)`. `max` and `last` work as for gauges.
- **Page boundaries:** when a bucketed page resumes at bucket B of series S, the handler calls `PrevPoint(S, start, B)` to get the last point before B that is still inside the query range. That seeds `increase`, so a bucket's value doesn't depend on where the page break fell.

*Alternative:* SQL `GROUP BY ts / step`, with window functions (`LAG`) for counters. That moves less data out of SQLite, but it's two code paths, the reset rule ends up in SQL, and `last` relies on SQLite's bare-column-with-`max()` quirk. The Go aggregator is a pure function over `(ts, value)` pairs, and the spec examples translate directly into tests for it.

### Count

`count=true` runs `COUNT(*)` per matching series over `[start, end]` for raw queries, and `COUNT(DISTINCT floorDiv(ts, step))` for bucketed ones. Both run on the primary-key range. It only runs on requests without a cursor.

### Cursor: an opaque, signed, versioned token

Contents (JSON, then base64url): `{"v":1,"s":<seriesID>,"t":<last ts or bucket start>,"a":<start>,"b":<end>,"h":<hash>}`. `h` is a SHA-256 hash of the canonical query: name, sorted label filters, step and agg. The token is signed with HMAC-SHA256 using a random 32-byte key generated when the process starts.

- **Fixed range.** `a` and `b` carry the resolved range, so a defaulted `end` stays fixed for the whole walk. If a later request gives `start` or `end` explicitly and they differ, or the hash differs, the response is `invalid_cursor`.
- **Signed.** "A cursor the server issued" is enforced by checking the HMAC. The signature also stops a client from forging a position into another query's range.
- **Version field.** The `v` field lets the format change later.

*Alternatives:*
- An unsigned cursor: decoding and checking the parameters would catch most mistakes, but the spec promises that tampered cursors are rejected.
- A key derived from the read token: reads may have no token, so there'd be nothing to derive from.

Cost: a restart invalidates in-flight cursors (see Risks).

### Read authentication: one middleware

```go
func requireReadToken(token string, next http.Handler) http.Handler // token == "" → next unchanged
```

`server.New` wraps the four read routes with this middleware. It compares `Authorization` against `"Bearer " + token` using `subtle.ConstantTimeCompare`, which is the same pattern as ingest. On failure it sends 401 with `WWW-Authenticate: Bearer` and the read error body. The ingest token is never compared here, so it can't authorize reads, as the spec requires. The startup log line records whether reads require a token, so an operator can see that reads are open.

This middleware is the seam for the planned auth/entitlements system: replacing it changes no handler.

### Routing

`server.New(st, ingest, read)` registers the following on the existing mux, using Go 1.22 method and wildcard patterns:
- `GET /api/v1/metrics`
- `GET /api/v1/metrics/{name}/labels`
- `GET /api/v1/metrics/{name}/series`
- `GET /api/v1/query`

The mux's own 404 and 405 responses for paths and methods outside these patterns stay plain text. That's why the read-error-body requirement covers 400, 401 and 404 from the read endpoints only.

### Validation order and error codes

`query` parses everything into a `Params` value with a pure function, `parse(url.Values, now) (Params, *Error)`. The checks run in a fixed order, so the error a client sees is deterministic:
1. Shape: `name`, then labels, then times, `step`/`agg`, `limit`, `count` → `invalid_param`
2. `limit > 50000` → `limit_too_large`
3. The cursor → `invalid_cursor`

Only then does the handler hit the store. Next comes the type check (`unsupported_agg`, when the metric exists), then the read.

Error bodies are `{"error": code, "message": text}`, with the code from a closed set of constants. This differs from ingest's `{"error": message}`. The planning interview chose to leave ingest unchanged, and `openapi.yaml` documents both shapes.

### Response encoding

Points are `[ts, value]` JSON arrays rather than objects, which roughly halves the response size at 50,000 points. Values are finite `float64`: ingest rejects non-numbers, and `rate` divides by `step ≥ 1000`, so it can never produce NaN or infinity. A series split across pages appears on each page it has points on, with the same `labels`. `total` is omitted (not `null`) when `count` isn't requested.

### OpenAPI and the contract test

- `openapi.yaml` sits at the repo root and is written by hand. It uses **OpenAPI 3.0.3**, because `kin-openapi`'s response validation fully supports 3.0 and only partly supports 3.1. It documents ingest, `/healthz`, the four read endpoints, both error shapes and the `bearerAuth` scheme.
- `internal/server/contract_test.go` has `TestContract…` functions. They start the real server, with real handlers on a temporary SQLite database, through `httptest`, and exercise each endpoint's success and error responses. They then validate every request and response pair with `openapi3filter.ValidateResponse` against `../../openapi.yaml`. A response that drifts from the spec fails `scripts/check.sh openapi_contract`.
- `github.com/getkin/kin-openapi` is imported only from `_test.go` files, so it never reaches the binary. It still appears in `go.mod`, so `govulncheck` covers it.

*Alternative:* `oapi-codegen` for server stubs. The planning interview rejected it, because it adds generation steps to a five-route API.

### Helm chart

- New values:
  - `readToken: ""`
  - `existingSecretReadTokenKey: read-token`
- `secret.yaml` adds `read-token` only when `readToken` is set.
- The Deployment always adds `PULSEBOARD_READ_TOKEN` from `secretKeyRef` with `optional: true`. It reads key `read-token` from the chart's own Secret, or `existingSecretReadTokenKey` from `existingSecret`.
  - If the key is missing, Kubernetes leaves the variable unset and the pod starts with reads open. That one mechanism covers the "no read token" and "existing Secret without the key" scenarios with no template branching.
- The `helm` check in `scripts/check.sh` asserts:
  - `read-token` is absent from the Secret without `readToken` and present with it
  - The env entry is `optional: true`
  - The existing-Secret key is used when `existingSecret` is set

## Risks / Trade-offs

- **[Reads are open by default]** Anyone who can reach the port can read every metric. → This is deliberate for trusted homelab networks. The README "Querying" section, the Helm values comments and the startup log all say how to set `PULSEBOARD_READ_TOKEN`. The entitlements change will replace this.
- **[A bucketed page can scan many raw rows]** `limit` caps buckets, not the raw points behind them. A week at `step=1h` over many series reads every raw point in the range. → At the stated scale (about 1k points/sec in total), a full week across all series is about 600M rows, but a single metric's series are a small fraction of that. The request context stops the scan when the client disconnects or the server drains on shutdown. Downsampling, already planned, is the long-term fix.
- **[`count=true` is O(rows in range)]** → It's opt-in, first page only, and documented as costly for long ranges.
- **[Cursors don't survive a restart]** The HMAC key is per process, so a pod restart mid-walk returns `invalid_cursor`. → The client restarts the walk from page 1. Walks are short-lived and restarts are rare. A persisted key can be added if this ever matters.
- **[A series can disappear mid-walk]** If retention removes all of a series' points between pages, its row is deleted. If it reappears, it may get a different ID. → Its old points weren't "stored for the whole walk", and new points are new writes, so the spec's exactly-once guarantee still holds. The walk just continues from the cursor's ID.
- **[Two error shapes]** Ingest uses `{"error": message}` and reads use `{"error": code, "message": ...}`. → Both are documented in `openapi.yaml`. Aligning them would be a breaking change to ingest, so it would be its own change.
- **[Typos look like quiet periods on `/query`]** → This is a deliberate product decision. The README points clients at `/api/v1/metrics` to check whether a name exists.

## Migration Plan

- No schema migration and no change to existing endpoints. Deploying the new binary or image adds the read endpoints with reads open. Setting `PULSEBOARD_READ_TOKEN` (or the Helm `readToken`) closes them.
- Rollback means deploying the previous version. The database is untouched, so nothing needs undoing. Clients of the read API lose it until they upgrade again.
