# Proposal

## Why

`add-ingest-and-storage` deferred the Dockerfile and CI to a later change. They landed anyway in commits `2d42331` and `61d5fa0` without a change behind them, so nothing records what the container image promises operators or which checks must pass before a merge. This change records that work after the fact, closes the gaps it left untested, and keeps the image's runtime contract from changing without notice.

## What Changes

- A container image built from `Dockerfile` (already present): a static `CGO_ENABLED=0` binary on `gcr.io/distroless/static-debian12:nonroot`, running as UID/GID 65532, with the database at `/data/pulseboard.db` on a `/data` volume, listening on `:8080`, and running `serve` by default.
- `.dockerignore` (already present) keeps planning docs, personal tooling and local databases out of the build context.
- `scripts/check.sh` (already present) is the single entry point for quality gates: go build, gofmt, go vet, golangci-lint (pinned `v2.14.0`, configured by `.golangci.yml`), tests, race tests, govulncheck (pinned `v1.8.0`), the Docker image build, and the OpenAPI lint and contract checks. It skips a check (exit code 77) when its prerequisite is missing, such as no Docker daemon or no `openapi.yaml`.
- GitHub Actions CI (`.github/workflows/ci.yml`, already present) runs `scripts/check.sh` on pushes to `main`, on pull requests, and on manual dispatch.
- New work: verify the image's runtime behavior end to end, document how to run the container in `README.md`, and fix the gaps found while recording this change: the stop grace period is no longer than the server's drain, and the OpenAPI linter isn't pinned.

Out of scope: publishing the image to a registry, multi-arch builds, release tagging, a Docker `HEALTHCHECK` (the distroless image has no shell or HTTP client), and creating `openapi.yaml` (the query API change owns that).

## Capabilities

### New Capabilities
- `deployment`: How pulseboard runs as a container image: its default command, listen address, database location and volume, the user it runs as, startup without a token, and stopping on `docker stop`.

### Modified Capabilities
<!-- None. The `server` capability (in add-ingest-and-storage, not yet archived) already defines flags, env vars, health and shutdown; the container uses them unchanged. -->

## Impact

- **Code:** none in Go packages. `Dockerfile`, `.dockerignore`, `.golangci.yml`, `scripts/check.sh` and `.github/workflows/ci.yml` already exist. This change may make small edits to `scripts/check.sh` and `README.md`.
- **Dependencies:** build-time only: `golang:1.26-alpine`, `gcr.io/distroless/static-debian12:nonroot`, golangci-lint, govulncheck, `@redocly/cli` (via `npx`, only once `openapi.yaml` exists), and the `actions/checkout@v5` and `actions/setup-go@v6` actions.
- **Systems:** GitHub Actions runs on every PR. The Docker build check needs a Docker daemon, which GitHub-hosted Ubuntu runners provide.
- **Operators:** a supported way to run pulseboard: `docker run` with `PULSEBOARD_INGEST_TOKEN` and a volume on `/data`.
