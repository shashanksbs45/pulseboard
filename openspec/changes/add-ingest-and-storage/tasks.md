# Tasks

## 1. Module scaffold

- [x] 1.1 Run `go mod init github.com/shashanksbs45/pulseboard`, create the package directories from design.md, and add a stub `cmd/pulseboard/main.go`; verify `go build ./...` succeeds
- [x] 1.2 Add `modernc.org/sqlite` and verify `go mod tidy && go build ./...` succeeds with `CGO_ENABLED=0`
- [x] 1.3 Add `.gitignore` entries for the built binary and `*.db*` files, and verify that `git status` stays clean after a local build and run

## 2. Configuration and CLI

- [x] 2.1 Implement `internal/config`: the `serve` flag set, env-var defaults, flag-over-env precedence, defaults (`:8080`, `./pulseboard.db`, `168h`), and startup validation (empty token, non-positive retention); verify table tests cover each setting's env/flag/default case and both validation errors
- [x] 2.2 Implement subcommand dispatch in `cmd/pulseboard` (`serve`; any other subcommand prints usage to stderr and exits non-zero); verify with a test that runs the built binary with an unknown subcommand and asserts the exit status and stderr

## 3. SQLite store

- [x] 3.1 Define the `Store` interface, the `Point`/`Rejection` types, and the reason-code constants in `internal/store`; verify `go vet ./...` passes
- [x] 3.2 Implement open with pragmas (WAL, `synchronous=FULL`, `busy_timeout`, foreign keys) and the `user_version` migration to the version-1 schema; verify a test that opens a temp DB twice asserts `user_version = 1` and that the migration is idempotent
- [x] 3.3 Implement canonical label encoding (sorted keys, `{}` when empty); verify unit tests for label order, empty labels, and unicode values
- [x] 3.4 Implement `Write` as one `BEGIN IMMEDIATE` transaction: metric type lookup/insert with `type_conflict` (stored and in-batch), series lookup/insert with the 10,000 `series_limit_exceeded` check, and point upsert that overwrites on a duplicate `(series, ts)`; verify integration tests on a temp DB cover each case, and that the series limit is configurable in tests so they don't need 10k series
- [x] 3.5 Verify durability: write a batch, close the store, reopen the same file, and assert that every point round-trips with name, type, labels, value, and millisecond timestamp
- [x] 3.6 Verify concurrency: run concurrent `Write` calls racing for the last series slot under `go test -race`, and assert that exactly one wins
- [x] 3.7 Implement `DeleteBefore` with chunked point deletes, then cleanup of orphaned series and metrics, plus `Ping`/`Close`; verify tests that expired points are removed, recent ones kept, the series slot freed, and the metric type redefinable after full expiry

## 4. Ingest endpoint

- [x] 4.1 Implement the pure per-point validation (name regex and length, type, value present/number/non-negative counter, label count/key/value rules, timestamp bounds against an injected clock and retention, receive time when `ts` is absent); verify table-driven tests map every spec scenario to the expected reason code
- [x] 4.2 Implement the handler: constant-time bearer-token check (401), 5 MiB body limit (413), array decode (400 for non-array or empty), the 5,000-point cap (413), merging validation and store rejections by original index, and 200/207/422 status selection; verify `httptest` integration tests against a temp SQLite store cover each scenario in `specs/ingestion/spec.md`, including the mixed-batch response body
- [x] 4.3 Verify that requests rejected with 400/401/413 store nothing, by querying the store directly after each such test

## 5. Retention and server lifecycle

- [x] 5.1 Implement the retention loop (a pass at startup, then hourly via an injectable ticker/clock, stopping on context cancel); verify a test with a fake clock that a pass removes expired points and that the loop exits on cancel
- [x] 5.2 Implement `internal/server`: routing for `POST /api/v1/ingest` and `GET /healthz` (200 when `Ping` succeeds, 503 otherwise), and graceful shutdown on SIGINT/SIGTERM with a 10s drain that then closes the store; verify tests for both health states and that an in-flight ingest completes during `Shutdown`
- [x] 5.3 Wire `serve` in `cmd/pulseboard` (config → store → retention → server); verify that `go run ./cmd/pulseboard serve` without a token exits non-zero, and that with a token it starts and responds 200 on `/healthz`

## 6. Documentation

- [x] 6.1 Add a `README.md` covering how to build and run, the configuration table, an example `curl` ingest, the response format and reason codes, and a recommendation to batch points; verify that the documented `curl` command works against a locally running server
- [x] 6.2 Update `AGENTS.md` with the real commands (`go build ./...`, `go test -race ./...`, `go vet ./...`), and remove the "no application code" and "no build/test" statements; verify that each listed command runs successfully

## 7. Integration check

- [x] 7.1 End to end: start the built binary on a temp DB, push a mixed batch with `curl`, stop it with SIGTERM, restart it, and confirm the accepted points are still in the DB (via `sqlite3` or a test helper) and that `/healthz` returns 200; record the commands in the README's development section
