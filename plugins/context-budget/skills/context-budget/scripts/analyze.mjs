#!/usr/bin/env node
// Summarise ctx-proxy JSONL logs into a per-component context budget.
//
//   node analyze.mjs                       # latest run in the default log dir
//   node analyze.mjs --log <dir|file>
//   node analyze.mjs --compare A B         # diff two runs (config experiments)
//
// Component figures are character-based estimates. The `usage` column is the
// provider's own count, so treat the component split as proportions rather than
// absolute token counts.

import fs from "node:fs"
import path from "node:path"

const argv = process.argv.slice(2)
const flag = (n) => argv.includes(`--${n}`)
const arg = (n, d) => {
  const i = argv.indexOf(`--${n}`)
  return i !== -1 && argv[i + 1] ? argv[i + 1] : d
}

const LOG_DIR = arg("log", process.env.CTX_PROXY_LOG || path.join(process.cwd(), ".ctxproxy", "logs"))

function resolveRun(target) {
  const p = path.resolve(target)
  const st = fs.statSync(p)
  if (st.isFile()) return p
  const runs = fs
    .readdirSync(p)
    .filter((f) => f.endsWith(".jsonl"))
    .map((f) => path.join(p, f))
    .sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs)
  if (!runs.length) throw new Error(`no .jsonl runs found in ${p}`)
  return runs[0]
}

function load(file) {
  const reqs = []
  const resps = []
  for (const line of fs.readFileSync(file, "utf8").split("\n")) {
    if (!line.trim()) continue
    let e
    try {
      e = JSON.parse(line)
    } catch {
      continue
    }
    if (e.type === "request") reqs.push(e)
    else if (e.type === "response") resps.push(e)
  }
  return { file, reqs, resps }
}

function median(nums) {
  if (!nums.length) return null
  const s = [...nums].sort((a, b) => a - b)
  const m = s.length >> 1
  return s.length % 2 ? s[m] : Math.round((s[m - 1] + s[m]) / 2)
}

function stats(run) {
  const chat = run.reqs.filter((r) => r.tools)
  const usageIn = run.resps.map((r) => r.usage?.input_tokens).filter((n) => typeof n === "number")
  const usageCache = run.resps
    .map((r) => r.usage?.cache_read_input_tokens)
    .filter((n) => typeof n === "number")
  const usageOut = run.resps.map((r) => r.usage?.output_tokens).filter((n) => typeof n === "number")
  const usageTotal = run.resps.map(totalIn).filter((n) => typeof n === "number")
  return {
    label: run.reqs[0]?.run ?? path.basename(run.file),
    file: run.file,
    requests: run.reqs.length,
    chatRequests: chat.length,
    models: [...new Set(chat.map((r) => r.model).filter(Boolean))],
    toolsCount: median(chat.map((r) => r.tools.count)),
    toolNames: [...new Set(chat.flatMap((r) => r.tools.names ?? []))],
    estTools: median(chat.map((r) => r.tools.estTokens)),
    estSystem: median(chat.map((r) => r.system.estTokens)),
    estMessages: median(chat.map((r) => r.messages.estTokens)),
    estOther: median(chat.map((r) => r.other?.estTokens ?? 0)),
    estTotal: median(chat.map((r) => r.estTotalIn)),
    usageIn: median(usageIn),
    usageCache: median(usageCache),
    usageOut: median(usageOut),
    usageTotal: median(usageTotal),
    byModel: byModel(run),
  }
}

// Logs from before total_input_tokens existed fall back to input_tokens.
function totalIn(resp) {
  return resp.usage?.total_input_tokens ?? resp.usage?.input_tokens
}

// Requests and responses are logged in order per path, so pair each chat
// request with the next response on the same path to attribute usage to a model.
// Side calls (titles, topic detection, compaction) show up here as their own rows.
function byModel(run) {
  const queues = new Map()
  for (const r of run.resps) {
    if (!queues.has(r.path)) queues.set(r.path, [])
    queues.get(r.path).push(r)
  }
  const rows = new Map()
  for (const req of run.reqs.filter((r) => r.tools)) {
    const resp = queues.get(req.path)?.shift()
    const key = req.model || "unknown"
    if (!rows.has(key)) rows.set(key, { model: key, requests: 0, tools: [], totalIn: [] })
    const row = rows.get(key)
    row.requests++
    row.tools.push(req.tools.count)
    const t = resp && totalIn(resp)
    if (typeof t === "number") row.totalIn.push(t)
  }
  return [...rows.values()].map((r) => ({
    model: r.model,
    requests: r.requests,
    tools: median(r.tools),
    totalIn: median(r.totalIn),
    sumIn: r.totalIn.reduce((a, b) => a + b, 0),
  }))
}

const num = (n) => (n == null ? "n/a" : n.toLocaleString("en-US"))

function report(s) {
  console.log(`\nrun: ${s.label}`)
  console.log(`file: ${s.file}`)
  console.log(`requests: ${s.requests} (chat: ${s.chatRequests})  models: ${s.models.join(", ") || "n/a"}`)

  const total = s.estTotal || 0
  const parts = [
    ["tool schemas", s.estTools],
    ["system prompt", s.estSystem],
    ["messages", s.estMessages],
    ["other body", s.estOther],
  ]
  console.log(`\n  ${"component".padEnd(16)}${"est tokens".padStart(12)}${"share".padStart(9)}`)
  console.log(`  ${"-".repeat(37)}`)
  for (const [name, v] of parts) {
    const share = total ? ((v / total) * 100).toFixed(1) + "%" : "n/a"
    console.log(`  ${name.padEnd(16)}${num(v).padStart(12)}${share.padStart(9)}`)
  }
  console.log(`  ${"-".repeat(37)}`)
  console.log(`  ${"TOTAL (est)".padEnd(16)}${num(total).padStart(12)}`)

  console.log(`\n  provider-reported usage (median):`)
  console.log(`    total input (incl cache): ${num(s.usageTotal)}`)
  console.log(`    input_tokens (uncached):  ${num(s.usageIn)}`)
  console.log(`    cache_read_input_tokens:  ${num(s.usageCache)}`)
  console.log(`    output_tokens:            ${num(s.usageOut)}`)

  console.log(`\n  ${"by model".padEnd(34)}${"reqs".padStart(6)}${"tools".padStart(7)}${"med in".padStart(10)}${"sum in".padStart(12)}`)
  for (const m of s.byModel) {
    console.log(`  ${m.model.slice(0, 33).padEnd(34)}${String(m.requests).padStart(6)}${num(m.tools).padStart(7)}${num(m.totalIn).padStart(10)}${num(m.sumIn).padStart(12)}`)
  }

  if (s.usageTotal && total) {
    const ratio = s.usageTotal / total
    console.log(`\n  estimate calibration: usage/est = ${ratio.toFixed(2)}x`)
    console.log(`  scale the component figures above by ~${ratio.toFixed(2)} for absolute tokens.`)
  }

  console.log(`\n  tools exposed (${s.toolsCount}):`)
  for (const n of [...s.toolNames].sort()) console.log(`    - ${n}`)
  return s
}

if (flag("compare")) {
  const [a, b] = argv.slice(argv.indexOf("--compare") + 1)
  if (!a || !b) {
    console.error("usage: analyze.mjs --compare <runA> <runB>")
    process.exit(1)
  }
  const A = stats(load(resolveRun(a)))
  const B = stats(load(resolveRun(b)))
  report(A)
  report(B)
  console.log(`\n  ${"component".padEnd(16)}${"A".padStart(12)}${"B".padStart(12)}${"delta".padStart(12)}`)
  console.log(`  ${"-".repeat(52)}`)
  const rows = [
    ["tool count", A.toolsCount, B.toolsCount],
    ["est tools", A.estTools, B.estTools],
    ["est system", A.estSystem, B.estSystem],
    ["est total in", A.estTotal, B.estTotal],
    ["usage total in", A.usageTotal, B.usageTotal],
    ["usage input", A.usageIn, B.usageIn],
    ["usage cache read", A.usageCache, B.usageCache],
  ]
  for (const [name, x, y] of rows) {
    const d = x != null && y != null ? y - x : null
    const ds = d == null ? "n/a" : (d > 0 ? "+" : "") + d.toLocaleString("en-US")
    console.log(`  ${name.padEnd(16)}${num(x).padStart(12)}${num(y).padStart(12)}${ds.padStart(12)}`)
  }
  const dropped = A.toolNames.filter((n) => !B.toolNames.includes(n))
  const added = B.toolNames.filter((n) => !A.toolNames.includes(n))
  if (dropped.length) console.log(`\n  removed in B: ${dropped.join(", ")}`)
  if (added.length) console.log(`  added in B:   ${added.join(", ")}`)
} else {
  try {
    report(stats(load(resolveRun(LOG_DIR))))
  } catch (err) {
    console.error(`error: ${err.message}`)
    process.exit(1)
  }
}