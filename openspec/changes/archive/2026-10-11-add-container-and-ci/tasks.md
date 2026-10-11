# Tasks

## 1. Container image (landed in 2d42331)

- [x] 1.1 Add the two-stage `Dockerfile` (static `CGO_ENABLED=0` build, distroless `nonroot` runtime, `USER 65532:65532`, `/data` volume owned by 65532, `PULSEBOARD_ADDR`/`PULSEBOARD_DB` defaults, `ENTRYPOINT` pulseboard, `CMD serve`); verified by `scripts/check.sh docker` passing
- [x] 1.2 Add `.dockerignore` excluding `.git`, agent and personal tooling, `openspec/`, `*.md`, local `*.db*` files and the built binary; verified by the same image build

## 2. Quality checks (landed in 2d42331)

- [x] 2.1 Add `scripts/check.sh` with the named checks, the PASS/FAIL/SKIP summary, exit code 77 for SKIP and a non-zero exit on any failure; verified by `scripts/check.sh build gofmt vet lint vuln docker openapi_lint` printing the summary with `openapi_lint` as SKIP
- [x] 2.2 Add `.golangci.yml` (golangci-lint v2, `errcheck` exclusions for best-effort cleanup) and fix the lint findings in `internal/ingest/handler.go` and `internal/server/server.go`; verified by `scripts/check.sh lint` passing
- [x] 2.3 Document `scripts/check.sh` in `README.md` and `AGENTS.md`; verified by the documented commands running as written
- [x] 2.4 Pin `@redocly/cli` in `check_openapi_lint` to a fixed version, matching how golangci-lint and govulncheck are pinned; verify `grep -n '@redocly/cli@' scripts/check.sh` shows no `@latest`, and that `scripts/check.sh openapi_lint` still reports SKIP

## 3. CI (landed in 61d5fa0)

- [x] 3.1 Add `.github/workflows/ci.yml` running `scripts/check.sh` on pushes to `main`, pull requests and `workflow_dispatch`, with read-only permissions and cancellation of superseded runs; verified by reading the workflow
- [x] 3.2 Confirm CI passes on GitHub for this branch's pull request, including the `docker` check actually running (PASS, not SKIP) on the Ubuntu runner; verify in the job log's summary

## 4. Container runtime checks

- [x] 4.1 Add `check_container` to `scripts/check.sh` and to `ALL`: skip with 77 when no Docker daemon is reachable, build the image under a unique tag, and remove the containers, volume and tag through a `trap`; verify `scripts/check.sh container` passes and that `docker ps -a` and `docker volume ls` show nothing left behind
- [x] 4.2 In `check_container`, cover "Container starts with defaults" and "Server runs as non-root": run with a token, a named volume and a published port, poll `/healthz` for 200, and assert `docker top` shows UID 65532; verify by temporarily changing the expected UID to 0 and seeing the check fail
- [x] 4.3 Cover "No ingest token": run without a token and assert a non-zero exit status and a log line mentioning the ingest token; verify the check passes
- [x] 4.4 Cover "Operator overrides the listen address" (`PULSEBOARD_ADDR=:9090`, `/healthz` 200 on 9090) and "Operator passes serve flags" (command `serve --retention 24h`, startup log shows `retention=24h0m0s`); verify the check passes
- [x] 4.5 Cover "Data survives container replacement": post a `counter` point, replace the container with a new one on the same volume, post a `gauge` with the same name, and expect `type_conflict` in the response; verify by temporarily switching to an anonymous volume and seeing the check fail
- [x] 4.6 Cover "Idle container stops cleanly": `docker stop` a running container and assert exit status 0 from `docker inspect`; verify the check passes
- [x] 4.7 Update the `scripts/check.sh` lists in `README.md` and `AGENTS.md` to include `container`; verify `scripts/check.sh` with no arguments runs it

## 5. Running the container (docs)

- [x] 5.1 Add a "Run with Docker" section to `README.md`: `docker build`, then `docker run` with `PULSEBOARD_INGEST_TOKEN`, `-p 8080:8080` and a named volume on `/data`; `chown 65532:65532` for bind mounts; and `--stop-timeout 15` (or `stop_grace_period: 15s` in Compose) because the server drains for up to 10 seconds; verify by running the documented commands and getting 200 from `/healthz`, then stopping with exit status 0
