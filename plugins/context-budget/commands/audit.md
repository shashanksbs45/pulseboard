---
description: Report where this project's Claude Code context tokens go, or compare two ctx-proxy runs
argument-hint: "[runA runB]"
---

Use the context-budget skill to produce a context-cost report for this project.

1. Find the log directory: `$CTX_PROXY_LOG` if set, else `~/.ctxproxy/logs/<repo name>` where
   the repo name is the basename of `git rev-parse --show-toplevel` (or the current directory).
2. If it has no `.jsonl` runs, tell the user there is nothing to analyse yet and give them the
   exact `ctx-run.sh --profile baseline` command (absolute path) to record a session. Stop.
3. Arguments: $ARGUMENTS
   - Two arguments: run `analyze.mjs --compare <A> <B>` (resolve names against the log directory).
   - Otherwise: run `analyze.mjs --log <log dir>` for the newest run.
4. Summarise in a few lines: total input tokens per request (incl cache), the share taken by
   tool schemas, the biggest tool or MCP groups, and side-call models if any.
5. Recommend one profile (`lean` by default) with the expected saving, and say how to try it.
