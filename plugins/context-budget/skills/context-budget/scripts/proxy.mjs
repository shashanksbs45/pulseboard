#!/usr/bin/env node
// Context-proxy: a transparent logging proxy for OpenCode provider traffic.
//
// Point a provider's settings.baseURL at this server and every request is
// forwarded verbatim (headers included, so OpenCode keeps using its own
// credential) while the request body and the response usage are recorded.
//
// Usage:
//   node proxy.mjs --upstream https://api.example.com [--port 4096] [--log <dir>]
//
// Environment:
//   CTX_PROXY_UPSTREAM  default upstream origin (no trailing slash)
//   CTX_PROXY_PORT      default listen port
//   CTX_PROXY_LOG       default log directory
//
// Authorization headers are never written to disk. Component token counts are
// estimates; the authoritative total is `usage` from the provider response.

import http from "node:http"
import https from "node:https"
import fs from "node:fs"
import path from "node:path"
import os from "node:os"

const argv = process.argv.slice(2)
function arg(name, fallback) {
  const i = argv.indexOf(`--${name}`)
  return i !== -1 && argv[i + 1] ? argv[i + 1] : fallback
}

const UPSTREAM = (arg("upstream", process.env.CTX_PROXY_UPSTREAM) || "").replace(/\/+$/, "")
const PORT = Number(arg("port", process.env.CTX_PROXY_PORT || 4096))
const LOG_DIR = arg("log", process.env.CTX_PROXY_LOG) || path.join(process.cwd(), ".ctxproxy", "logs")

if (!UPSTREAM) {
  console.error("error: --upstream (or CTX_PROXY_UPSTREAM) is required, e.g. https://api.anthropic.com")
  process.exit(1)
}

// Fail at startup, not on the first request, so launchers fall back cleanly.
try {
  if (!/^https?:$/.test(new URL(UPSTREAM).protocol)) throw new Error("not http(s)")
} catch {
  console.error(`error: --upstream must be an http(s) URL, got "${UPSTREAM}"`)
  process.exit(1)
}

fs.mkdirSync(LOG_DIR, { recursive: true })

// One JSONL file per process start keeps concurrent sessions from interleaving.
const RUN_ID = new Date().toISOString().replace(/[:.]/g, "-")
const LOG_FILE = path.join(LOG_DIR, `run-${RUN_ID}.jsonl`)
const LABEL = arg("label", RUN_ID)

// Optional raw-body capture, for diffing what two configs actually send.
// Off by default because request bodies contain conversation content.
const DUMP_FILE = arg("dump", null)

// Rough token estimate. Real totals come from provider `usage` when present.
const CHARS_PER_TOKEN = 3.6
function estTokens(value) {
  if (value == null) return 0
  const s = typeof value === "string" ? value : JSON.stringify(value)
  return Math.round(s.length / CHARS_PER_TOKEN)
}

const REDACT = /^(authorization|x-api-key|api-key|cookie|proxy-authorization)$/i

// Credentials are replaced wholesale; only the auth scheme survives, so logs
// still show whether a key or a bearer token was used.
function redact(v) {
  const s = String(v ?? "")
  return s.includes(" ") ? `${s.split(" ")[0]} [redacted]` : "[redacted]"
}

function summarizeBody(body) {
  const out = {
    format: null,
    model: null,
    stream: false,
    maxTokens: null,
    tools: { count: 0, names: [], entryKeys: [], estTokens: 0 },
    system: { chars: 0, estTokens: 0, blocks: 0 },
    messages: { count: 0, estTokens: 0, chars: 0 },
    other: { estTokens: 0, keys: [] },
  }
  if (!body || typeof body !== "object") return out

  out.model = body.model ?? null
  out.stream = body.stream === true
  out.maxTokens = body.max_tokens ?? body.max_completion_tokens ?? body.maxOutputTokens ?? null

  // Anthropic Messages API: a top-level `system` block, or tools declared flat.
  // OpenAI Chat also sends `tools`, so require an Anthropic-specific signal
  // rather than treating "has tools" as Anthropic.
  const tools = Array.isArray(body.tools) ? body.tools : []
  const openAiTool = tools.some((t) => t && typeof t === "object" && ("function" in t || "type" in t))
  const isAnthropic =
    body.system != null ||
    (body.input == null &&
      Array.isArray(body.messages) &&
      tools.length > 0 &&
      !openAiTool &&
      !body.messages.some((m) => m && ("tool_calls" in m || "tool_call_id" in m)))

  if (isAnthropic) {
    out.format = "anthropic-messages"
    out.system.chars = typeof body.system === "string" ? body.system.length : JSON.stringify(body.system ?? "").length
    out.system.estTokens = estTokens(body.system)
    out.system.blocks = Array.isArray(body.system) ? body.system.length : body.system ? 1 : 0
    if (Array.isArray(body.tools)) {
      out.tools.count = body.tools.length
      out.tools.names = body.tools.map((t) => t?.name).filter(Boolean)
      out.tools.estTokens = estTokens(body.tools)
    }
    if (Array.isArray(body.messages)) {
      out.messages.count = body.messages.length
      out.messages.chars = JSON.stringify(body.messages).length
      out.messages.estTokens = estTokens(body.messages)
    }
    const known = new Set(["model", "stream", "system", "messages", "tools", "max_tokens", "metadata", "tool_choice", "temperature", "thinking"])
    out.other.keys = Object.keys(body).filter((k) => !known.has(k))
    out.other.estTokens = estTokens(
      Object.fromEntries(Object.entries(body).filter(([k]) => !known.has(k))),
    )
    return out
  }

  // OpenAI Chat Completions: { messages, tools }
  if (Array.isArray(body.messages) && body.tools != null && body.system == null) {
    out.format = "openai-chat"
  }
  // OpenAI Responses API: { input, instructions, tools }
  if (body.input != null) {
    out.format = "openai-responses"
    out.system.chars = typeof body.instructions === "string" ? body.instructions.length : 0
    out.system.estTokens = estTokens(body.instructions)
    out.system.blocks = body.instructions ? 1 : 0
    out.messages.count = Array.isArray(body.input) ? body.input.length : 1
    out.messages.chars = JSON.stringify(body.input).length
    out.messages.estTokens = estTokens(body.input)
  }

  if (Array.isArray(body.tools)) {
    out.tools.count = body.tools.length
    // Tool entries vary by provider: flat {name}, nested {function:{name}},
    // bare strings, or a {type} discriminator only.
    out.tools.names = body.tools
      .map((t) =>
        typeof t === "string"
          ? t
          : t?.name || t?.function?.name || t?.type,
      )
      .filter(Boolean)
    out.tools.entryKeys =
      body.tools.length && typeof body.tools[0] === "object" && body.tools[0] !== null
        ? Object.keys(body.tools[0])
        : typeof body.tools[0] === "string"
          ? ["<string>"]
          : []
    out.tools.estTokens = estTokens(body.tools)
  }
  if (out.format === "openai-chat" && Array.isArray(body.messages)) {
    const systemMsgs = body.messages.filter((m) => m?.role === "system" || m?.role === "developer")
    const rest = body.messages.filter((m) => m?.role !== "system" && m?.role !== "developer")
    out.system.estTokens = estTokens(systemMsgs)
    out.system.chars = JSON.stringify(systemMsgs).length
    out.system.blocks = systemMsgs.length
    out.messages.count = rest.length
    out.messages.estTokens = estTokens(rest)
    out.messages.chars = JSON.stringify(rest).length
  }
  const known = new Set([
    "model", "stream", "system", "messages", "tools", "max_tokens", "max_completion_tokens",
    "metadata", "tool_choice", "temperature", "thinking", "input", "instructions",
  ])
  out.other.keys = Object.keys(body).filter((k) => !known.has(k))
  out.other.estTokens = estTokens(Object.fromEntries(Object.entries(body).filter(([k]) => !known.has(k))))
  return out
}

function record(entry) {
  const line = JSON.stringify({ ts: new Date().toISOString(), run: LABEL, ...entry })
  fs.appendFileSync(LOG_FILE, line + "\n")
}

function dumpBody(name, body) {
  if (!DUMP_FILE) return
  // DUMP_FILE is a directory, so create that directory itself.
  fs.mkdirSync(DUMP_FILE, { recursive: true })
  fs.writeFileSync(`${DUMP_FILE}/${name}-${Date.now()}.json`, JSON.stringify(body, null, 2))
}

/** Map Anthropic or OpenAI usage onto one canonical shape. */
function normalizeUsage(u) {
  if (!u || typeof u !== "object") return null
  const out = {
    input_tokens: u.input_tokens ?? u.prompt_tokens ?? null,
    output_tokens: u.output_tokens ?? u.completion_tokens ?? null,
    cache_read_input_tokens:
      u.cache_read_input_tokens ?? u.prompt_tokens_details?.cached_tokens ?? null,
    cache_creation_input_tokens:
      u.cache_creation_input_tokens ?? u.prompt_tokens_details?.cache_creation_tokens ?? null,
  }
  out.total_input_tokens = totalInput(out, u.prompt_tokens != null)
  return out
}

// Anthropic's input_tokens excludes cached tokens, so the full prompt is the
// sum of all three. OpenAI's prompt_tokens already includes cached tokens.
function totalInput(u, openAi) {
  if (u.input_tokens == null) return null
  if (openAi) return u.input_tokens
  return u.input_tokens + (u.cache_read_input_tokens ?? 0) + (u.cache_creation_input_tokens ?? 0)
}

/**
 * Reads usage out of an SSE stream.
 * Anthropic: message_start carries input/cache tokens, message_delta output.
 * OpenAI (OpenCode models): a terminal chunk carries `usage` when
 * stream_options.include_usage is on, using prompt_/completion_ names.
 */
function sseUsageAccumulator() {
  const usage = {
    input_tokens: null,
    output_tokens: null,
    cache_read_input_tokens: null,
    cache_creation_input_tokens: null,
  }
  return {
    usage,
    rest: "",
    feed(text) {
      // Events can straddle network chunks; keep the trailing partial line.
      const lines = (this.rest + text).split("\n")
      this.rest = lines.pop()
      for (const line of lines) {
        if (!line.startsWith("data:")) continue
        const payload = line.slice(5).trim()
        if (!payload || payload === "[DONE]") continue
        let evt
        try {
          evt = JSON.parse(payload)
        } catch {
          continue
        }
        const u = normalizeUsage(evt?.usage || evt?.message?.usage)
        if (u) {
          for (const k of Object.keys(usage)) {
            if (typeof u[k] === "number") usage[k] = u[k]
          }
          usage.total_input_tokens = totalInput(usage, evt?.usage?.prompt_tokens != null)
        }
      }
    },
  }
}

const server = http.createServer(async (req, res) => {
  const started = Date.now()
  const url = req.url || "/"

  // Answered locally so launchers can check readiness without an upstream call.
  if (url === "/__ctxproxy/health") {
    res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ ok: true, upstream: UPSTREAM }))
    return
  }

  if (req.method === "OPTIONS") {
    res.writeHead(204, cors()).end()
    return
  }

  // Buffer the request body so we can both forward and analyse it.
  const chunks = []
  for await (const c of req) chunks.push(c)
  const rawReq = Buffer.concat(chunks)

  let body = null
  try {
    body = rawReq.length ? JSON.parse(rawReq.toString("utf8")) : null
  } catch {
    /* non-JSON body: forwarded unanalysed */
  }

  const headers = {}
  for (const [k, v] of Object.entries(req.headers)) {
    headers[k] = REDACT.test(k) ? redact(v) : v
  }

  // Only record chat-ish endpoints; skip model listings and other noise.
  // Claude Code's /v1/messages/count_tokens carries a full prompt but is not a
  // turn, so it is logged as a plain request rather than counted as chat.
  const isChat = /messages|chat\/completions|responses|generateContent/.test(url) && !/count_tokens/.test(url)

  // Match the upstream scheme; an https origin needs the https client.
  const upstreamURL = new URL(UPSTREAM)
  const client = upstreamURL.protocol === "https:" ? https : http

  let upstreamReq
  try {
    upstreamReq = client.request(
      UPSTREAM + url,
      {
        method: req.method,
        // Ask for an uncompressed response so the SSE usage events are readable;
        // clients like Claude Code advertise gzip/br/zstd by default.
        headers: { ...req.headers, host: upstreamURL.host, "accept-encoding": "identity" },
        timeout: 10 * 60 * 1000,
      },
      (upRes) => {
      const entry = {
        type: "request",
        method: req.method,
        path: url,
        status: upRes.statusCode,
        headers,
        reqBytes: rawReq.length,
      }
      if (isChat && body) {
        const s = summarizeBody(body)
        entry.model = s.model
        entry.format = s.format
        entry.stream = s.stream
        entry.tools = s.tools
        entry.system = s.system
        entry.messages = s.messages
        entry.other = s.other
        entry.estTotalIn = s.tools.estTokens + s.system.estTokens + s.messages.estTokens + s.other.estTokens
      }
      record(entry)
      if (isChat && body && rawReq.length > 2000) dumpBody(entry.model || "req", body)

      res.writeHead(upRes.statusCode, upRes.headers)

      const isSSE = String(upRes.headers["content-type"] || "").includes("text/event-stream")
      const acc = isSSE ? sseUsageAccumulator() : null
      // Non-SSE bodies are teed so `usage` can be read at the end without
      // delaying the response: chunks still reach the client as they arrive.
      const buffered = isSSE ? null : []
      let resBytes = 0

      upRes.on("data", (c) => {
        resBytes += c.length
        if (acc) acc.feed(c.toString("utf8"))
        else buffered.push(c)
        res.write(c)
      })

      upRes.on("end", () => {
        res.end()
        if (!isChat) return
        let usage = null
        if (isSSE) {
          usage = acc.usage
        } else {
          try {
            usage = normalizeUsage(JSON.parse(Buffer.concat(buffered).toString("utf8"))?.usage)
          } catch {
            usage = null
          }
        }
        record({
          type: "response",
          path: url,
          status: upRes.statusCode,
          ms: Date.now() - started,
          resBytes,
          usage,
        })
      })
      },
    )
  } catch (err) {
    record({ type: "error", path: url, message: err.message, ms: Date.now() - started })
    if (!res.headersSent) res.writeHead(502, { "content-type": "application/json" })
    return res.end(JSON.stringify({ error: { message: `ctx-proxy upstream error: ${err.message}` } }))
  }

  upstreamReq.on("timeout", () => upstreamReq.destroy(new Error("upstream timeout")))
  upstreamReq.on("error", (err) => {
    record({ type: "error", path: url, message: err.message, ms: Date.now() - started })
    if (!res.headersSent) res.writeHead(502, { "content-type": "application/json" })
    res.end(JSON.stringify({ error: { message: `ctx-proxy upstream error: ${err.message}` } }))
  })

  upstreamReq.end(rawReq)
})

function cors() {
  return {
    "access-control-allow-origin": "*",
    "access-control-allow-headers": "*",
    "access-control-allow-methods": "GET,POST,DELETE,OPTIONS",
  }
}

server.listen(PORT, "127.0.0.1", () => {
  console.log(`ctx-proxy listening on http://127.0.0.1:${PORT}`)
  console.log(`  upstream: ${UPSTREAM}`)
  console.log(`  log:      ${LOG_FILE}`)
  console.log(`\nWire it up in opencode.json(c):`)
  console.log(`  { "providers": { "<id>": { "settings": { "baseURL": "http://127.0.0.1:${PORT}" } } } }`)
  console.log(`\nOr for Claude Code:`)
  console.log(`  ANTHROPIC_BASE_URL=http://127.0.0.1:${PORT} claude`)
})