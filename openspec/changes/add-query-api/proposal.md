# Proposal

## Why

pulseboard stores metrics but has no way to read them back. The only proof that data exists is indirect: the deployment scenarios use a `type_conflict` rejection to show a counter survived. Every earlier change deferred reading to "the query API change", the dashboard can't be built without it, and `scripts/check.sh` has two OpenAPI checks that are skipped until this change adds `openapi.yaml`.

## What Changes

- Four read endpoints under `/api/v1`, all `GET`:
  - `/api/v1/metrics` lists every stored metric as `{"name","type"}` objects.
  - `/api/v1/metrics/{name}/labels` lists a metric's label keys and the values seen for each key.
  - `/api/v1/metrics/{name}/series` lists a metric's series as `{"labels":{...}}` objects.
  - `/api/v1/query` returns points for one metric over a time range.
  Discovery responses are objects rather than bare strings, so later changes (e.g. metric units and descriptions) can add fields without breaking clients.
- Series selection on `/query`: a required metric `name` plus optional exact label matches (`label=key:value`, repeatable, split at the first colon). A series matches when it has every given label.
- Time range: `start` and `end` in Unix milliseconds, both inclusive. If omitted, `end` is the server's current time and `start` is one hour before `end`.
- **Raw by default.** `/query` returns exactly the stored points unless the caller asks for bucketing with both `step` (milliseconds, at least 1000) and `agg`. Buckets are aligned to multiples of `step` since the Unix epoch, labelled by their start time, and empty buckets are left out.
  - Gauges allow `avg`, `min`, `max`, `sum` and `last`.
  - Counters allow `increase`, `rate`, `last` and `max`. `increase` and `rate` treat a drop in value as a counter reset.
- Cursor pagination on `/query`. `limit` sets points per page (default 10,000, maximum 50,000). A response carries `next_cursor`, which is `null` on the last page. Pages are ordered by series, then time. A cursor is bound to the query parameters that produced it. There is no offset pagination.
- Opt-in `count=true` adds the total number of matching points (or buckets) to the first page.
- Not found versus empty: `/metrics/{name}/labels` and `/metrics/{name}/series` return 404 for an unknown metric. `/query` returns 200 with an empty `series` list whenever nothing matches, including an unknown metric name.
- Malformed requests get 400. Examples are an invalid metric name or label filter, a duplicate label key, more than 10 label filters, a non-integer or reversed time range, a bad `step`, `limit`, `count` or `cursor`, `step` given without `agg` (or the reverse), and an `agg` that names no aggregation. An `agg` that doesn't fit an existing metric's type also gets 400.
- Read errors are JSON `{"error":"<code>","message":"<text>"}` using a closed set of codes: `invalid_param`, `invalid_cursor`, `limit_too_large`, `unsupported_agg`, `unknown_metric` and `unauthorized`.
- An optional read token, `--read-token` / `PULSEBOARD_READ_TOKEN`. When it's set, every read endpoint requires `Authorization: Bearer <read token>` and answers 401 without it. When it's unset, reads are open. Ingest keeps requiring the ingest token, and `/healthz` stays open.
- The ingestion spec gains a statement of what a counter means: a running total that only goes up, except when it resets to a lower value (e.g. on a process restart). Ingest validation doesn't change.
- The Helm chart gains an optional `readToken` value and an optional read-token key in `existingSecret`.
- `openapi.yaml` at the repo root describes the whole HTTP API: ingest, health and the four read endpoints. A Go contract test checks real handler responses against it. This turns on the `openapi_lint` and `openapi_contract` checks in `scripts/check.sh`.
- `README.md` gains a "Querying" section with `curl` examples, including a note that `/query` can't tell a misspelled metric from a quiet one and how `/api/v1/metrics` answers that.

### Decisions recorded from the planning interview

| Topic | Decision |
|---|---|
| Read auth | Optional read token now. A real auth and entitlements system is a separate future change. |
| Endpoints | All four, in one change |
| Series selection | Name plus exact label matches only |
| Aggregation | Opt-in only. The server never aggregates on its own. |
| Time format | Unix ms. The default range is the last hour. |
| Large results | Cursor pagination with a hard page-size limit, rather than rejecting or truncating |
| Count | Opt-in, on the first page only |
| Searchable fields | Name, labels and time. Values aren't searchable. |
| Unknown metric | 404 on the per-metric discovery endpoints, 200 empty on `/query` |
| Malformed input | 400 everywhere, including `/query` |
| Counter meaning | A running total that can reset |
| OpenAPI | Hand-written, with a contract test. No code generation. |
| Schema | No database changes. The query API runs on the existing tables and keys. |

### Acceptance criteria

1. All four endpoints behave as the `query` spec scenarios describe.
2. Walking a query with cursors returns every stored point in the range exactly once, even when points are written and retention runs between pages.
3. Counter `increase` and `rate` are correct across a counter reset.
4. With a read token set, reads without it get 401. Without one, reads are open. Ingest needs the ingest token in both cases.
5. `openapi.yaml` covers ingest, health and all read endpoints. `scripts/check.sh` reports PASS (not SKIP) for `openapi_lint` and `openapi_contract`, and the full script passes.
6. `README.md` has a "Querying" section with working `curl` examples.
7. The Helm chart lints and renders with and without a read token, and the `helm` check asserts both.

### Out of scope

- An authentication and entitlements system beyond the single read token
- Moving storage off SQLite (e.g. to PostgreSQL)
- New columns: metric description, unit and timestamps, and series `created_at` and `last_seen`. These are planned as their own change.
- Filtering by point value, thresholds and alerting
- Regex and negative label matchers (`!=`, `=~`, `!~`)
- Offset pagination
- Relative or RFC 3339 times
- The dashboard
- Downsampling, histograms and Prometheus or OTLP compatibility
- Rewriting the deployment scenarios that use `type_conflict` as proof of persisted data. They stay valid, and switching them to read through the query API can happen later.

## Capabilities

### New Capabilities
- `query`: the read side of the HTTP API. It covers metric discovery, the range query with optional bucketing, pagination and counts, read authentication, request validation and read error responses.

### Modified Capabilities
- `ingestion`: adds a requirement that defines counter semantics (a running total that may reset). Existing ingestion requirements don't change.
- `server`: the configuration requirement gains the optional read token (`--read-token` / `PULSEBOARD_READ_TOKEN`).
- `deployment`: adds a requirement for the Helm chart's optional read token, from a value or from the existing Secret.

## Impact

- **Code:** new `internal/query` package (HTTP handlers, parameter parsing, cursors and bucketing). New read methods on the `store.Store` interface and their SQLite implementation. Read routes and the read-token option in `internal/server` and `internal/config`, wired up in `cmd/pulseboard/serve.go`. No schema migration.
- **API:** four new `GET` endpoints. Existing endpoints are unchanged. Nothing breaks.
- **Files:** new `openapi.yaml`. Edits to `README.md`, `AGENTS.md`, `deploy/helm/pulseboard/` (values, Secret and Deployment templates) and `scripts/check.sh` (`helm` check assertions).
- **Dependencies:** one new Go test-only module for checking responses against OpenAPI (`github.com/getkin/kin-openapi`). The `openapi_lint` check already runs Redocly through `npx`, so CI needs Node, which `ubuntu-latest` has.
- **Operators:** reads are open by default. Anyone exposing pulseboard beyond a trusted network should set `PULSEBOARD_READ_TOKEN` (or `readToken` / the existing Secret in Helm).
