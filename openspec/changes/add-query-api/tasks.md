# Tasks

## 1. Read token configuration

- [ ] 1.1 Add `ReadToken` to `config.Config` with `--read-token` / `PULSEBOARD_READ_TOKEN` (optional, default empty, flag over env) and extend `config_test.go` with env-applies, flag-overrides-env and unset cases; verify `go test ./internal/config` passes
- [ ] 1.2 Add `read token required: true|false` to the startup log line in `cmd/pulseboard/serve.go` (never the token itself) and document `--read-token` / `PULSEBOARD_READ_TOKEN` in the README configuration table; verify `PULSEBOARD_INGEST_TOKEN=dev go run ./cmd/pulseboard serve` logs `false`, and logs `true` with `PULSEBOARD_READ_TOKEN=x`

## 2. Store read side

- [ ] 2.1 Add the `Reader` interface and the `Metric`, `Series` and `PointRange` model types to `internal/store/store.go` as in design.md, and embed `Reader` in `Store`; verify `go build ./...` fails only where `sqlite.Store` lacks the new methods
- [ ] 2.2 Implement `Metrics` and `Series` in `internal/store/sqlite` (sorted by name; series by ID, using the `UNIQUE(metric, labels)` index; type `""` for an unknown metric) with tests on a temp-file database, including labels decoded back from canonical JSON; verify `go test -race ./internal/store/...` passes
- [ ] 2.3 Implement `ReadPoints` as one keyset range scan per series inside a single read transaction (inclusive `[start, end]`, `ts > cursor` only on the cursor's series, stop at the page limit) with tests for ordering, inclusive bounds, resuming mid-series and resuming at the next series; verify `go test -race ./internal/store/...` passes
- [ ] 2.4 Implement `CountPoints` (raw `COUNT(*)` and bucketed `COUNT(DISTINCT floorDiv(ts, step))`) and `PrevPoint`, with tests including an empty range and a step that splits points across buckets; verify `go test -race ./internal/store/...` passes
- [ ] 2.5 Add a store test that writes 20 points, reads a page of 10, runs `DeleteBefore` on already-read points and writes new ones, then reads the next page, and asserts no repeats and no missed surviving points; verify it passes under `-race`

## 3. Query core (pure functions)

- [ ] 3.1 Create `internal/query` with the closed error-code constants, an `Error` type and a `writeError` that emits `{"error":code,"message":text}`; verify with a unit test of the body and status for each code
- [ ] 3.2 Implement `parse(url.Values, now) (Params, *Error)`: name and label rules shared with ingest (export or reuse the regexes and limits from `internal/ingest`; don't copy them), label split at the first colon, duplicate keys, more than 10 filters, integer times with the one-hour default, `start ≤ end`, `step ≥ 1000`, `step`/`agg` together, a known `agg`, `1 ≤ limit ≤ 50000` (`limit_too_large` above), `count` true/false, in the validation order from design.md; verify with a table test covering every row of the "Rejected parameters" scenario plus the valid defaults
- [ ] 3.3 Implement the cursor: JSON `{v,s,t,a,b,h}`, base64url, HMAC-SHA256 with a per-process key, the query hash over name, sorted labels, step and agg, and resolved-range enforcement; verify table tests for round-trip, a tampered payload, a wrong signature, changed filters, changed explicit `start`/`end`, and omitted `start`/`end` reusing the cursor's range
- [ ] 3.4 Implement the bucket aggregator over ordered `(ts, value)` streams: epoch-aligned `floorDiv` buckets (including negative timestamps), empty buckets omitted, gauge `avg|min|max|sum|last`, counter `increase|rate|last|max` with the reset rule, the first point in range contributing 0, and a seed value for page resumes; verify with table tests mirroring every Bucketed query, Gauge aggregations and Counter semantics scenario in `specs/query/spec.md`
- [ ] 3.5 Implement the type-fit check (`unsupported_agg` for an existing metric only) and label-subset matching over `[]store.Series`; verify unit tests for both scenarios in "Aggregation must fit the metric type", subset match and a label value containing a colon

## 4. Read endpoints

- [ ] 4.1 Implement the `/api/v1/metrics`, `/api/v1/metrics/{name}/labels` and `/api/v1/metrics/{name}/series` handlers against a fake `store.Reader` (404 `unknown_metric`, 400 for a malformed name, sorted keys and values, `{"metrics":[]}` when empty); verify handler tests for each scenario under List metrics, List a metric's labels, List a metric's series and Unknown metric on discovery
- [ ] 4.2 Implement the `/api/v1/query` handler: parse, cursor check, series lookup and filter, type check, raw or bucketed read up to `limit` (calling `PrevPoint` to seed `increase` on bucketed resumes), `next_cursor`, `total` on `count=true` without a cursor, and `type: null` plus an empty `series` for no match; verify handler tests for every scenario under Query series selection, Query time range, Raw query response, Query matching nothing, Query pagination, Cursor bound to its query and Opt-in total count
- [ ] 4.3 Add a handler test that walks a bucketed counter query with a page break inside a reset and asserts the buckets equal a single unpaginated walk; verify it passes

## 5. Server wiring and read auth

- [ ] 5.1 Add the `requireReadToken` middleware (constant-time compare, `WWW-Authenticate: Bearer`, `unauthorized` body, pass-through when the token is empty), change `server.New` to take the read handler and token and register the four `GET` patterns behind it, and wire it in `cmd/pulseboard/serve.go`; verify server tests for all four Read authentication scenarios, and that `POST /api/v1/ingest` still requires only the ingest token
- [ ] 5.2 Add an end-to-end test in `internal/server` on a temp-file SQLite database: ingest gauges and counters, then hit all four read endpoints and check the bodies; verify `go test -race ./...` passes
- [ ] 5.3 Add a "Querying" section to `README.md` with `curl` examples for each endpoint (raw, bucketed, label filter, cursor walk, `count=true`, read token header), the "unknown name returns empty; check `/api/v1/metrics`" note, and the read-token guidance for exposed deployments; verify each `curl` runs as written against `go run ./cmd/pulseboard serve` after ingesting the README's sample point
- [ ] 5.4 Update `AGENTS.md`: `internal/query` in the package list, `openapi.yaml` at the root, and the `query` capability in the main specs list; verify the listed paths exist

## 6. OpenAPI contract

- [ ] 6.1 Write `openapi.yaml` (OpenAPI 3.0.3) covering `POST /api/v1/ingest`, `GET /healthz`, the four read endpoints, both error shapes, the `bearerAuth` scheme and the `[ts, value]` tuple schema; verify `scripts/check.sh openapi_lint` reports PASS
- [ ] 6.2 Add `github.com/getkin/kin-openapi` (test-only import) and `internal/server/contract_test.go` with `TestContract…` functions that start the real server through `httptest` on a temp database and validate success and error responses of every endpoint with `openapi3filter`; verify `scripts/check.sh openapi_contract` reports PASS, and fails when a response field is renamed in the handler
- [ ] 6.3 Update the `scripts/check.sh` description in `README.md` and `AGENTS.md` so OpenAPI lint and contract are no longer described as skipped until `openapi.yaml` exists; verify the text matches `scripts/check.sh` behavior

## 7. Counter semantics and container check

- [ ] 7.1 Add an ingest handler test where a counter series receives 112 and then 3 at a later timestamp, and both are accepted (Counter reset is accepted); verify `go test ./internal/ingest` passes with no ingest code change
- [ ] 7.2 Extend `check_container` in `scripts/check.sh` to query `/api/v1/metrics` against the running image and assert `jobs_done` is listed; verify `scripts/check.sh docker container` passes with a Docker daemon and reports SKIP without one

## 8. Helm read token

- [ ] 8.1 Add `readToken: ""` and `existingSecretReadTokenKey: read-token` to `values.yaml` with comments (reads are open when unset; rotating needs a rollout restart), write `read-token` into `secret.yaml` only when `readToken` is set, and add `PULSEBOARD_READ_TOKEN` to `deployment.yaml` from `secretKeyRef` with `optional: true` (chart Secret key `read-token`, or `existingSecretReadTokenKey` on `existingSecret`); verify `helm lint` passes
- [ ] 8.2 Add `check_helm` assertions: no `read-token` in the Secret by default; present with `readToken=r`; the env entry is `optional: true`; `existingSecret=pb-token` uses key `read-token` and a custom `existingSecretReadTokenKey`; an empty `existingSecretReadTokenKey` falls back to `read-token`; verify `scripts/check.sh helm` passes, and fails when `optional: true` is removed
- [ ] 8.3 Document `readToken` and `existingSecret` with a `read-token` key in the README "Run on Kubernetes" section, and print the read-token state in `NOTES.txt`; verify by rendering with `helm install --dry-run`

## 9. Integration checks

- [ ] 9.1 Run `scripts/check.sh` with Docker and `helm` available, and verify every check reports PASS, including `openapi_lint` and `openapi_contract`
- [ ] 9.2 On minikube, install the chart with `readToken`, then with an `existingSecret` that has and lacks a `read-token` key, and verify each Read token source scenario in `specs/deployment/spec.md` with `curl` through a port-forward
- [ ] 9.3 Run `openspec validate add-query-api --strict` and verify it reports the change as valid
