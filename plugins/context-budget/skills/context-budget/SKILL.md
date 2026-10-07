---
name: context-budget
description: Measure and cut Claude Code's per-request context cost. Use when the user asks where their tokens or context go, why sessions are expensive, how big tool/MCP schemas or the system prompt are, wants to compare two configurations, or wants to trim tools or MCP servers. Covers the ctx-proxy logging proxy, its token report, and ready-made tool/MCP profiles.
---

# Context budget

Every Claude Code request re-sends tool schemas, the system prompt and the conversation.
Tool schemas are usually the largest part (about 72% in local tests) and many tools go unused.
This skill measures that overhead with a local logging proxy and cuts it with profiles.

All paths below are relative to this skill's base directory (shown when the skill loads).
Resolve them to absolute paths before giving commands to the user.

| Path | What it is |
| --- | --- |
| `scripts/ctx-run.sh` | Fail-open launcher: starts Claude Code through a private proxy for one session |
| `scripts/proxy.mjs` | The logging proxy (forwards requests unchanged, records sizes + provider `usage`) |
| `scripts/analyze.mjs` | Report on a run, or `--compare A B` two runs |
| `profiles/claude/` | `settings.lean.json` (deny list) and `mcp-none.json` (empty MCP config) |
| `profiles/opencode/` | `lean.json`, `minimal.json` for OpenCode (`OPENCODE_CONFIG=... opencode --standalone`) |

## Profiles

| Profile | Cuts | Local test: input tokens/request |
| --- | --- | --- |
| `baseline` | nothing | 50,843 |
| `nomcp` | every MCP server, incl. claude.ai connectors | 35,229 (-31%) |
| `lean` | rarely used built-ins + Indeed/Calendar connectors | 30,133 (-41%) |
| `minimal` | all MCP; built-ins except Bash, Read, Edit, Write, Skill | 14,917 (-71%) |

Recommend `lean` as the daily default; `minimal` only for focused coding sessions
(it removes Agent, WebFetch, WebSearch). These figures come from one single-turn test;
the user's own numbers will differ with their tools and connectors.

## Measuring (the user runs this in their own terminal)

Claude cannot launch a nested interactive session for the user, so hand them the command:

```sh
<base>/scripts/ctx-run.sh --profile baseline     # then work normally, exit when done
<base>/scripts/ctx-run.sh --profile lean         # same work, other profile
```

Suggest an alias once: `alias claude-ctx='<base>/scripts/ctx-run.sh'`.

The launcher is fail-open: if node is missing or the proxy is not healthy within ~5s,
Claude starts directly. An existing `ANTHROPIC_BASE_URL` (corporate gateway) becomes the
proxy's upstream, so gateways keep working. Logs go to `~/.ctxproxy/logs/<repo name>/`
(override with `CTX_PROXY_LOG`), never into the project.

## Reading results

```sh
node <base>/scripts/analyze.mjs --log ~/.ctxproxy/logs/<repo>              # newest run
node <base>/scripts/analyze.mjs --compare <runA.jsonl> <runB.jsonl>        # diff two runs
```

- Compare `total input (incl cache)`, never `input_tokens`: Claude Code caches most of the
  prompt, so `input_tokens` is often single digits.
- Component figures (tool schemas, system prompt, messages) are character estimates;
  scale by the printed `usage/est` ratio. Only provider `usage` is exact.
- `tools exposed` is the proof a cut worked: a tool still listed is still being sent.
- The `by model` table separates side calls (titles, compaction) that run on smaller models.
- `count_tokens` calls are logged but excluded from stats.

## Applying a profile permanently

Profiles via the launcher last one session. To make a cut stick, merge the deny entries
from `profiles/claude/settings.lean.json` into the user's settings — use the
`/context-budget:apply-profile` command, which confirms before writing.
MCP servers are better disabled in `/mcp` than denied.

## Privacy and limits

- Logs hold sizes, tool names and token counts only; credentials are redacted and
  conversation content is not stored (unless the proxy is started with `--dump`).
- Mid-session proxy crashes are not recovered; restart the session (or use `--no-proxy`).
- Bedrock/Vertex users and WebSocket transports bypass `ANTHROPIC_BASE_URL`, so the proxy sees nothing.
