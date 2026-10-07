#!/usr/bin/env bash
# Fail-open launcher: one Claude Code session through a private ctx-proxy.
#
#   ctx-run.sh [--profile baseline|nomcp|lean|minimal] [--no-proxy] [-- claude args]
#
# The proxy gets a free port for this session only and stops when Claude exits.
# If node is missing or the proxy is not healthy within ~5s, Claude starts
# directly against the API instead, so a broken proxy never blocks a session.
#
# Environment:
#   CTX_PROXY_LOG       log directory (default ~/.ctxproxy/logs/<repo name>)
#   CTX_PROXY_UPSTREAM  upstream origin (default: an existing ANTHROPIC_BASE_URL,
#                       else https://api.anthropic.com)

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
P="$HERE/../profiles/claude"

PROFILE=baseline
USE_PROXY=1
while [ $# -gt 0 ]; do
  case "$1" in
    --profile) PROFILE="${2:-}"; shift 2 ;;
    --no-proxy) USE_PROXY=0; shift ;;
    --) shift; break ;;
    *) break ;;
  esac
done

NOMCP=(--strict-mcp-config --mcp-config "$P/mcp-none.json")
case "$PROFILE" in
  baseline) ARGS=() ;;
  nomcp)    export ENABLE_CLAUDEAI_MCP_SERVERS=false; ARGS=("${NOMCP[@]}") ;;
  lean)     ARGS=(--settings "$P/settings.lean.json") ;;
  minimal)  export ENABLE_CLAUDEAI_MCP_SERVERS=false; ARGS=("${NOMCP[@]}" --tools "Bash,Read,Edit,Write,Skill") ;;
  *)
    echo "usage: $0 [--profile baseline|nomcp|lean|minimal] [--no-proxy] [-- claude args]" >&2
    exit 2
    ;;
esac

warn() { echo "ctx-run: $*" >&2; }

PROXY_PID=""
cleanup() { [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null; }
trap cleanup EXIT INT TERM

if [ "$USE_PROXY" = 1 ]; then
  if ! command -v node >/dev/null 2>&1; then
    warn "node not found; starting Claude without the proxy"
  else
    REPO="$(basename "$(git rev-parse --show-toplevel 2>/dev/null || pwd)")"
    LOG="${CTX_PROXY_LOG:-$HOME/.ctxproxy/logs/$REPO}"
    UPSTREAM="${CTX_PROXY_UPSTREAM:-${ANTHROPIC_BASE_URL:-https://api.anthropic.com}}"
    mkdir -p "$LOG"
    PORT="$(node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')"

    node "$HERE/proxy.mjs" --upstream "$UPSTREAM" --port "$PORT" --log "$LOG" --label "$PROFILE" \
      > "$LOG/.proxy-$PORT.out" 2>&1 &
    PROXY_PID=$!

    HEALTHY=0
    for _ in $(seq 1 25); do
      if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/__ctxproxy/health" 2>/dev/null; then HEALTHY=1; break; fi
      kill -0 "$PROXY_PID" 2>/dev/null || break
      sleep 0.2
    done

    if [ "$HEALTHY" = 1 ]; then
      export ANTHROPIC_BASE_URL="http://127.0.0.1:$PORT"
      warn "profile=$PROFILE  proxy=$ANTHROPIC_BASE_URL  logs=$LOG"
    else
      warn "proxy did not start (see $LOG/.proxy-$PORT.out); starting Claude without it"
      cleanup; PROXY_PID=""
    fi
  fi
else
  warn "profile=$PROFILE  (no proxy)"
fi

claude ${ARGS[@]+"${ARGS[@]}"} "$@"
