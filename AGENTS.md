# AGENTS.md

## What this repo is

`pulseboard` — a platform to collect, store, and visualize system/application metrics. It is a single Go binary (module `github.com/shashanksbs45/pulseboard`, Go 1.26): `cmd/pulseboard` (CLI entrypoint, `serve` subcommand) and `internal/` packages (`config`, `server`, `ingest`, `store`, `store/sqlite`, `retention`). Storage is SQLite via the pure-Go `modernc.org/sqlite` driver, so builds need no CGO. All product work starts as an OpenSpec change. There is no CI yet.

## Commands

- Build: `go build ./...`
- Test: `go test -race ./...` (tests use temp-file SQLite databases; nothing external is needed)
- Vet: `go vet ./...`
- Run locally: `PULSEBOARD_INGEST_TOKEN=dev go run ./cmd/pulseboard serve` (see `README.md` for config and the end-to-end check)

## Source of truth

- `openspec/config.yaml` — schema is `spec-driven`. Specs live in `openspec/specs/`, change proposals in `openspec/changes/`, completed work archives to `openspec/changes/archive/`. All three are currently empty (only `.gitkeep`).
- `process.md` — how this repo was bootstrapped (`openspec init`, `npx skills@latest add mattpocock/skills --skill=grill-me`, `openspec config profile`; note `opespec` on line 1 is a typo).
- `skills-lock.json` — skills installed from `mattpocock/skills`: `grill-me`, `grill-with-docs`, `grilling` (also present in `.agents/skills/`). Reinstall/update via the `skills` CLI, not by hand-editing.

## How work proceeds here

- Drive changes through the OpenSpec workflow rather than editing code directly: `openspec-propose` / `openspec-new-change` → `openspec-apply-change` → `openspec-verify-change` → `openspec-sync-specs` → `openspec-archive-change`. In Claude these surface as `/opsx:*` commands (`.claude/commands/opsx/`) and matching `.claude/skills/`; in OpenCode use the `openspec-*` skills.
- `grilling` / `grill-me` / `grill-with-docs` skills are the repo owner's chosen planning workflow — expect to be interviewed before implementing.
- Spec scenarios for observable behavior are written in Gherkin with the `gherkin-authoring` skill (`.agents/skills/gherkin-authoring`); the exact format is in `rules.specs` in `openspec/config.yaml`.

## Gotchas

- Do not invent npm scripts, Makefiles or extra test commands; the Go toolchain commands above are the only ones configured.
- `opencode.json`, `.claude-plugin/`, `plugins/`, `tools/` and `.claude/settings.local.json` are personal tooling (the context-budget plugin and ctx-proxy; `opencode.json` only routes OpenCode through that proxy) and are gitignored — keep agent guidance in this file, not in OpenCode config.
