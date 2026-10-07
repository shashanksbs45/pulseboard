# context-budget

A Claude Code plugin that shows where per-request context tokens go and cuts them.

- **Skill `context-budget`**: Claude knows how to measure, read reports and recommend profiles.
- **`/context-budget:audit [runA runB]`**: report on this project's latest run, or compare two.
- **`/context-budget:apply-profile [lean] [local|project|user]`**: make the lean deny list permanent (asks first).
- **`skills/context-budget/scripts/ctx-run.sh`**: fail-open launcher that runs one session through a private proxy.

## Install

```sh
claude plugin marketplace add <path-or-git-url-of-pulseboard>
claude plugin install context-budget@pulseboard-tools
```

## Daily use

```sh
alias claude-ctx="$HOME/.claude/plugins/.../skills/context-budget/scripts/ctx-run.sh"   # ask Claude for the exact path
claude-ctx --profile lean            # a session through the proxy with the lean profile
claude-ctx --profile lean --no-proxy # profile only, nothing in the request path
```

Then run `/context-budget:audit` in any later session.

## Guarantees

- **Fail-open:** no node, or no healthy proxy within ~5s → Claude starts directly.
- **No project pollution:** logs go to `~/.ctxproxy/logs/<repo>/`.
- **Gateways keep working:** an existing `ANTHROPIC_BASE_URL` becomes the upstream.
- **Private:** credentials redacted, no conversation content stored (unless `--dump`).
- **Per session:** profiles never edit settings; only `apply-profile` does, after confirmation.

Requires node 18+ and curl. Bedrock/Vertex and WebSocket transports bypass the proxy.
