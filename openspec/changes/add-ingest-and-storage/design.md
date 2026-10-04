# Design

## Context

The repo has no application code. It contains only the OpenSpec scaffold, agent skills, and tooling (`plugins/`, `tools/`), so this change creates the Go module from scratch. Go 1.26 is installed locally, and the remote is `github.com/shashanksbs45/pulseboard`. Planning settled the stack: Go, a single binary, SQLite, and pulseboard's own JSON format. See proposal.md for motivation and specs/ for the behavior contract.

Later changes add a query API, discovery endpoints, and a server-rendered dashboard to this same binary and store, so the boundaries chosen here need to accommodate them.

## Goals / Non-Goals

**Goals:**
- A package layout that the query API and dashboard changes can extend without restructuring.
- A storage boundary (`Store` interface) narrow enough to swap SQLite for an embedded KV store later.
- Validation that is pure and table-testable, separate from HTTP and storage.
- Deterministic tests: an injectable clock and a temp-file database per test.

**Non-Goals:**
- Read and query paths beyond what ingestion and retention need. Query methods arrive in the next change.
- Throughput beyond the stated small scale (~1k points/sec). There is no in-memory write buffering or batching across requests.
- Metrics about pulseboard itself (self-monitoring).

## Architecture (C4)

The architecture is described top-down with the [C4 model](https://c4model.com): system context, then containers, then the components inside the server container. The container diagram is the editable draw.io file [`c4_pulseboard.drawio`](./c4_pulseboard.drawio) (open it in draw.io desktop or app.diagrams.net). The text sketch below mirrors it for readers who can't open the file.

### Level 1: System context

| Element | C4 type | Role in this change |
|---|---|---|
| Homelab Operator | Person | Starts `pulseboard serve`, supplies config through flags or env vars, stops it with SIGINT/SIGTERM. |
| Pulseboard | Software system | Accepts metric pushes, stores them durably, and expires them after the retention period. |
| Metric Sources | External system | About 10 homelab apps and agents pushing gauges and counters, around 1k points/sec in total. |
| Health Checker | External system | An uptime monitor or process supervisor that probes `/healthz`. |

Nothing reads metrics back out yet. The query API and dashboard arrive in later changes, inside the same system.

### Level 2: Containers

```
                     [Homelab Operator]
                             │ CLI flags / env vars / signals
┌ Pulseboard ────────────────▼───────────────────────────────────┐
│  ┌──────────────────────┐  SQL: ingest txn       ┌───────────┐ │
│  │ Pulseboard Server    │ ─────────────────────▶ │ Metrics   │ │
│  │ Go binary            │  SQL: retention        │ Database  │ │
│  │ `pulseboard serve`   │ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─▶ │ SQLite WAL│ │
│  └──────────────────────┘  (async, hourly)       └───────────┘ │
└────────▲─────────────────────▲─────────────────────────────────┘
         │ POST /api/v1/ingest │ GET /healthz
         │ HTTP/JSON + Bearer  │ HTTP, no auth
   [Metric Sources]      [Health Checker]
```

| Container | Technology | Responsibility |
|---|---|---|
| Pulseboard Server | Go 1.26, single static binary (no CGO) | Runs the HTTP server, authenticates and validates ingest batches, writes points, runs the retention loop, reports health. |
| Metrics Database | SQLite file in WAL mode, accessed through `modernc.org/sqlite` | Holds metric types, series and points. The schema is versioned with `PRAGMA user_version`. |

| From → To | Interaction | Protocol | Style |
|---|---|---|---|
| Operator → Server | Start, configure, stop | CLI flags, env vars, POSIX signals | sync |
| Metric Sources → Server | Push a batch of up to 5,000 points | `POST /api/v1/ingest`, HTTP/JSON, bearer token | sync |
| Health Checker → Server | Liveness and DB reachability | `GET /healthz`, HTTP, unauthenticated | sync |
| Server → Database | One `BEGIN IMMEDIATE` transaction per ingest batch | SQL | sync |
| Server → Database | Delete expired points in 10k-row chunks every hour | SQL | async (background ticker) |

Both containers deploy together: the database is a file the binary opens at `--db` / `PULSEBOARD_DB`. Later changes add their endpoints to the same Server container instead of adding new containers.

### Level 3: Components of the Pulseboard Server

Each component is a Go package in module `github.com/shashanksbs45/pulseboard`:

| Component | Package | Responsibility | Depends on |
|---|---|---|---|
| Entrypoint | `cmd/pulseboard/` | Subcommand dispatch and wiring | config, server, store/sqlite, retention |
| Config | `internal/config/` | Flags + env → `Config`, startup validation | — |
| HTTP Server | `internal/server/` | `http.Server`, routing, `/healthz`, graceful shutdown | ingest, store |
| Ingest | `internal/ingest/` | HTTP handler, payload decoding, per-point validation | store (interface only) |
| Store | `internal/store/` | `Store` interface and model types | — |
| SQLite Store | `internal/store/sqlite/` | `Store` implementation, schema and migrations | store, Metrics Database |
| Retention | `internal/retention/` | Background deletion loop | store (interface only) |

`internal/` keeps the API private until there's a reason to export. Only `store/sqlite` touches the database. Every other component sees the `Store` interface, which is the seam the query API will extend and the place a different storage engine would plug in. *Alternative:* a flat `main` package. It's quicker to start, but the query and dashboard changes would force a reshuffle.

## Decisions

Decisions are grouped by the C4 element they shape.

### Pulseboard Server container

#### CLI and config: standard library only
`flag.NewFlagSet("serve")` handles flags. For each setting, an env var supplies the default and an explicit flag overrides it. *Alternative:* cobra plus viper. That's two dependencies for one subcommand and four settings, which isn't worth it yet.

#### Graceful shutdown
`signal.NotifyContext(SIGINT, SIGTERM)` stops the server. Shutdown then runs `http.Server.Shutdown` with a 10s timeout, stops the retention loop, and closes the store.

#### Clock injection
`ingest` and `retention` take a `func() time.Time`. Tests pin it, which makes the receive-time, future-timestamp and expiry scenarios deterministic.

### Ingest component

#### Handler pipeline
1. Check the bearer token with `crypto/subtle.ConstantTimeCompare`.
2. Read the body through `http.MaxBytesReader` (5 MiB, returning 413).
3. Decode into `[]json.RawMessage`. A non-array body or an empty array returns 400, and more than 5,000 elements returns 413.
4. Decode and validate each element with a pure function `validate(raw, now, retention) (Point, reason)`. `value` decodes into `*float64`, so a missing value and a non-number value are both detectable. `ts` decodes into `*int64`.
5. Send the valid points to `Store.Write`, then merge its rejections with the validation rejections by original index.
6. Choose the status (200, 207 or 422) from the counts.

The rejection reason codes are a closed set defined as constants: `invalid_name`, `invalid_type`, `invalid_value`, `invalid_label`, `too_many_labels`, `timestamp_out_of_range`, `type_conflict`, `series_limit_exceeded`.

### Store component (interface)

```go
type Store interface {
    Write(ctx context.Context, pts []Point) ([]Rejection, error) // per-point type_conflict / series_limit_exceeded
    DeleteBefore(ctx context.Context, cutoffMs int64) (int64, error)
    Ping(ctx context.Context) error
    Close() error
}
```
`Write` owns the checks that need stored state (type conflict and the series limit), because they must be atomic with the insert. Stateless validation stays in `ingest`.

### SQLite Store component and Metrics Database container

#### Driver: `modernc.org/sqlite`
This driver is pure Go, so the build needs no CGO and cross-compiles cleanly, which keeps the later Dockerfile simple. *Alternative:* `mattn/go-sqlite3`. It's faster, but it needs CGO.

Pragmas: `journal_mode=WAL`, `synchronous=FULL` (required by the durable-writes requirement), `busy_timeout=5000`, `foreign_keys=ON`.

#### Schema
```sql
CREATE TABLE metrics (name TEXT PRIMARY KEY, type TEXT NOT NULL CHECK (type IN ('gauge','counter')));
CREATE TABLE series  (id INTEGER PRIMARY KEY, metric TEXT NOT NULL REFERENCES metrics(name),
                      labels TEXT NOT NULL, UNIQUE(metric, labels));
CREATE TABLE points  (series_id INTEGER NOT NULL REFERENCES series(id), ts INTEGER NOT NULL,
                      value REAL NOT NULL, PRIMARY KEY (series_id, ts)) WITHOUT ROWID;
CREATE INDEX points_ts ON points(ts);
```
- `labels` holds **canonical JSON**: keys sorted, with `{}` when there are none. This makes series identity independent of label order.
- `PRIMARY KEY (series_id, ts)` plus `INSERT … ON CONFLICT DO UPDATE` implements duplicate-timestamp overwrite. The same key also serves the next change's range queries.
- `points_ts` supports the retention delete.
- Schema versioning uses `PRAGMA user_version`, with migrations applied at startup in order. Version 1 is this schema.

*Alternative:* one wide `points(name, labels, ts, value)` table. It's simpler, but it repeats the labels on every row and makes the active-series count expensive.

#### Write path: one transaction per batch, writes serialized
This is the synchronous Server → Database relationship. Each ingest request becomes a single `BEGIN IMMEDIATE` transaction:
1. Look up or insert each metric's type, rejecting `type_conflict`. Remembering types within the batch covers the in-batch conflict case.
2. Look up or insert each series. When a new series is needed and `COUNT(*) FROM series` (cached once per transaction) is already at 10,000, reject it with `series_limit_exceeded`.
3. Upsert the points.
4. Commit, then respond.

`BEGIN IMMEDIATE` takes the write lock up front, so two concurrent batches can't both claim the last series slot. *Alternative:* an in-memory series cache. It's faster, but it adds cache-invalidation work with retention, which isn't needed at this scale.

### Retention component

This is the asynchronous Server → Database relationship. The loop runs once at startup and then on a `time.Ticker` (1h). Each pass:
- deletes points in chunks of 10k rows (`DELETE … WHERE rowid IN (SELECT … LIMIT 10000)`, or the equivalent keyed on `(series_id, ts)`), so the write lock is never held for long;
- deletes series that no longer have points;
- deletes metrics that no longer have series.

The chunking keeps ingest latency low during large deletes.

## Risks / Trade-offs

- [Many tiny requests are slow with `synchronous=FULL`, since each one fsyncs] → Document batching in the README. WAL keeps reads unblocked. At about 1k points/sec in reasonable batches this is well within SQLite's capacity.
- [`COUNT(*)` on series runs once per write transaction] → This is trivial at 10k rows. Revisit if the limit grows.
- [Server clock skew wrongly rejects client timestamps] → The future window is 10 minutes, and `ts` is optional so clients can rely on receive time.
- [A large retention backlog after long downtime] → The chunked deletes yield the lock between chunks.
- [A canonical-label JSON encoding bug splits one series into two] → Unit-test canonicalization directly, including label-order and empty-label cases.

## Migration Plan

This is a greenfield change with no existing data. A new database file starts at `user_version = 0` and is migrated to version 1 on first startup. Rolling back means deleting the binary and the database file.
