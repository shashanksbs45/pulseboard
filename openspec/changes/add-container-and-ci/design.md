# Design

## Context

See proposal.md for why this change exists. Most of the work is already on the branch:

- `Dockerfile`: a two-stage build. `golang:1.26-alpine` builds a `CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"` binary. The runtime stage is `gcr.io/distroless/static-debian12:nonroot` with `ENV PULSEBOARD_ADDR=:8080 PULSEBOARD_DB=/data/pulseboard.db`, `USER 65532:65532`, `VOLUME ["/data"]`, `ENTRYPOINT ["/usr/local/bin/pulseboard"]` and `CMD ["serve"]`. `/data` is created in the build stage and owned by 65532, because the runtime image has no shell to create it.
- `.dockerignore`: excludes `.git`, agent and personal tooling directories, `openspec/`, `*.md`, local `*.db*` files and the built binary.
- `scripts/check.sh`: runs named checks (`build gofmt vet lint test race vuln docker openapi_lint openapi_contract`), keeps going after a failure, prints a PASS/FAIL/SKIP summary, and exits non-zero if any check failed. A check returns 77 to report SKIP.
- `.golangci.yml`: golangci-lint v2 with default linters. `errcheck` excludes best-effort `Close`, `Rollback` and `fmt.Fprint*` calls.
- `.github/workflows/ci.yml`: one `check` job on `ubuntu-latest` that checks out the repo, installs Go from `go.mod` and runs `scripts/check.sh`. It runs on pushes to `main`, on pull requests and on `workflow_dispatch`, with read-only permissions and cancellation of superseded runs.

The `docker` check only builds the image. Nothing runs it, so none of the `deployment` requirements are tested yet.

## Goals / Non-Goals

**Goals:**
- Every `deployment` scenario is checked automatically, both locally and in CI, through `scripts/check.sh`.
- Local runs and CI keep one source of truth for the checks.
- Every tool version the checks use is pinned.

**Non-Goals:**
- A Go test harness for containers (testcontainers or similar). A shell smoke test is enough for six scenarios.
- Pinning GitHub Actions to commit SHAs. Major-version tags are acceptable for a personal repo; revisit if the repo takes outside contributions.
- Changing Go code. The `server` capability already provides everything the container needs.

## Decisions

### Distroless static image, non-root
The binary is static because `modernc.org/sqlite` is pure Go, so the image needs no libc, shell or package manager. `distroless/static:nonroot` gives a small image with no shell for an attacker to use, and a fixed non-root UID. *Alternatives:* `alpine` (has a shell and `wget` for a `HEALTHCHECK`, but a larger attack surface) or `scratch` (smaller still, but no CA certificates or `/etc/passwd` entry for the non-root user).

### No `HEALTHCHECK` in the image
Distroless has no HTTP client, and adding a `pulseboard healthcheck` subcommand only for Docker would change the CLI contract. Orchestrators probe `GET /healthz` from outside the container instead. The proposal records this as out of scope.

### `check.sh` is the single entry point; CI only calls it
CI has one step, `scripts/check.sh`, so a check added to the script runs in CI automatically and developers reproduce CI with one command. *Alternative:* separate workflow steps or jobs per check. That gives nicer GitHub UI and parallelism, but the list of checks would then live in two places. The repo instructions also say new checks go in `scripts/check.sh`.

### Pinned linters, run via `go run` when not installed
golangci-lint `v2.14.0` and govulncheck `v1.8.0` are pinned in `check.sh`. If a binary is on `PATH`, it is used; otherwise the pinned version runs with `go run`, so CI needs no install step. golangci-lint is built with `CGO_ENABLED=0` so it doesn't depend on the local macOS linker. The OpenAPI linter (`@redocly/cli@latest`) is the one unpinned tool, and this change pins it to a fixed version.

### New `container` check: build, run and probe the image
A new `check_container` in `scripts/check.sh` runs the `deployment` scenarios against the built image, using only the host's `docker` and `curl`. Like `docker`, it returns 77 when no Docker daemon is reachable. It is a separate check so `scripts/check.sh docker` stays a fast build-only check. It:

1. builds the image (layer cache makes this cheap after `docker`), with a unique tag and container names so parallel runs don't collide;
2. runs it without a token and asserts a non-zero exit and an "ingest token" log line;
3. runs it with a token, a named volume on `/data` and a published port, waits for `/healthz` to return 200, and checks `docker top` for UID 65532;
4. posts a `counter` point, replaces the container with a new one on the same volume, then posts a `gauge` with the same name and expects `type_conflict`. There is no query API yet, so this rejection is the observable proof that the first point survived;
5. runs with `PULSEBOARD_ADDR=:9090` and with the command `serve --retention 24h`, and checks `/healthz` and the startup log line respectively;
6. stops a container with `docker stop` and asserts exit status 0;
7. removes its containers, volume and image tag on exit, through a `trap`, even when a step fails.

*Alternative:* a Go test behind a build tag that shells out to Docker. It's more structured, but needs Docker inside `go test` and duplicates what a 60-line script does.

### Stop grace period
Docker's default `docker stop` timeout is 10 seconds, the same as the server's drain timeout (`server.ShutdownTimeout`). A request still running at 10 seconds can be killed with SIGKILL before the database closes. SQLite in WAL mode with `synchronous=FULL` keeps committed data safe, but the exit is not clean. The README will tell operators to use `--stop-timeout 15` (or `stop_grace_period: 15s` in Compose). *Alternative:* shorten the server's drain timeout. That would change the `server` spec's 10-second promise just to fit a Docker default.

## Risks / Trade-offs

- [A bind-mounted host directory is owned by root, so UID 65532 can't create the database and the container exits at startup] → The README recommends a named volume, and for bind mounts, `chown 65532:65532` on the directory first.
- [`docker stop` uses the default 10-second timeout and kills a slow in-flight request] → Document `--stop-timeout 15`; committed batches are durable either way.
- [The container check adds time to CI (image build plus several container starts)] → Build layers are cached across the two Docker checks, and each container starts in well under a second. Accept a minute or so.
- [`check_test` and `check_race` both run the test suite] → Accepted: `test` gives a fast signal when you run checks selectively, and the full run in CI pays for both.
- [Upstream base image updates change the runtime image without a code change] → Accepted for now. Pinning base images by digest can come with release tagging.

## Migration Plan

Nothing to migrate. Operators who build the image today get the same behavior. The new check runs in CI from the commit that adds it.
