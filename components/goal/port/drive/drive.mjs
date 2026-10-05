// Runs the ORIGINAL extension in-process with a scripted host and records every effect, in order, plus the files it leaves under
// .pi. Layer-1 oracle for the Go port: a Pi RPC trace shows no files and no generated ids; this does.
//   node --experimental-strip-types drive/drive.mjs cases.json golden.json
// Time and randomness are deterministic: Date.now is the step's `at`, Math.random counts 0.01, 0.02, ... per call.
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readdirSync, statSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const oracle = path.join(here, "..", "oracle", "extensions");
const goalExtension = (await import(path.join(oracle, "goal.ts"))).default;

let now = Date.UTC(2026, 0, 2, 3, 4, 5);
Date.now = () => now;
let rnd = 0;
Math.random = () => { rnd += 1; return rnd / 100 + 0.001; };

function tree(root) {
  const out = {};
  const walk = (dir) => {
    for (const name of readdirSync(dir).sort()) {
      if (name === ".goals-pool-snapshot.json") continue; // a cache keyed by directory mtimes: not compared
      const p = path.join(dir, name);
      const rel = path.relative(root, p).split(path.sep).join("/");
      if (statSync(p).isDirectory()) { out[rel + "/"] = null; walk(p); } else out[rel] = readFileSync(p, "utf8");
    }
  };
  try { walk(path.join(root, ".pi")); } catch {}
  return out;
}

async function runCase(c) {
  const cwd = mkdtempSync(path.join(tmpdir(), "goaldrv-"));
  const agent = mkdtempSync(path.join(tmpdir(), "goalagent-"));
  process.env.PI_CODING_AGENT_DIR = agent;
  for (const k of Object.keys(process.env)) if (k.startsWith("PI_GOAL_")) delete process.env[k];
  for (const [k, v] of Object.entries(c.env ?? {})) process.env[k] = v;
  for (const [rel, text] of Object.entries(c.files ?? {})) { mkdirSync(path.dirname(path.join(cwd, rel)), { recursive: true }); writeFileSync(path.join(cwd, rel), text); }
  for (const [rel, text] of Object.entries(c.agentFiles ?? {})) { mkdirSync(path.dirname(path.join(agent, rel)), { recursive: true }); writeFileSync(path.join(agent, rel), text); }
  rnd = 0; now = Date.UTC(2026, 0, 2, 3, 4, 5);
  const events = [];
  const handlers = new Map();
  const commands = new Map();
  let entries = JSON.parse(JSON.stringify(c.entries ?? []));
  let confirmAnswers = [];
  let duringConfirm = [];
  const pi = {
    registerTool() {}, registerMessageRenderer() {}, sendMessage: (m) => events.push(["sendMessage", JSON.stringify(m)]),
    sendUserMessage: (m, o) => events.push(["sendUserMessage", String(m).slice(0, 60), JSON.stringify(o ?? null)]),
    registerCommand: (n, d) => commands.set(n, d),
    on: (e, h) => handlers.set(e, h),
    appendEntry: (t, d) => { entries.push({ type: "custom", customType: t, data: JSON.parse(JSON.stringify(d)) }); events.push(["entry", t, JSON.stringify(d)]); },
    getActiveTools: () => ["read", "bash", "edit", "write"], setActiveTools: () => {}, hasUI: !c.noUI,
  };
  // A case's `noUI` is a host without a user interface (print or json mode); `busy` is a host whose agent is streaming.
  const ctx = {
    cwd, hasUI: !c.noUI, modelRegistry: { getAvailable: () => [] }, model: undefined,
    sessionManager: { getBranch: () => entries, getCwd: () => cwd, getSessionId: () => c.sessionId ?? "drive-session", getRoot: () => cwd },
    ui: {
      notify: (m, l) => events.push(["notify", l ?? null, m]),
      setStatus: (k, v) => events.push(["status", k, v ?? null]),
      setWidget: (k, w) => events.push(["widget", k, w === undefined ? "clear" : "factory"]),
      onTerminalInput: () => () => {},
      select: async () => undefined, input: async () => undefined,
      confirm: async (t, m) => {
        // A step's `duringConfirm` changes the workspace while the dialog is open (another process at work).
        for (const rel of duringConfirm) rmSync(path.join(cwd, rel), { force: true });
        events.push(["confirm", t, m]);
        return confirmAnswers.length ? confirmAnswers.shift() : false;
      },
      custom: async () => undefined,
    },
    getSystemPrompt: () => "base", isIdle: () => !c.busy, hasPendingMessages: () => false, abort: () => events.push(["abort"]),
  };
  goalExtension(pi);
  const flush = () => new Promise((r) => setImmediate(r));
  const out = [];
  for (const s of c.steps) {
    if (s.at !== undefined) now = Date.UTC(2026, 0, 2, 3, 4, 5) + s.at;
    events.length = 0;
    confirmAnswers = s.confirm !== undefined ? [s.confirm] : [];
    duringConfirm = s.duringConfirm?.remove ?? [];
    for (const [rel, text] of Object.entries(s.write ?? {})) { mkdirSync(path.dirname(path.join(cwd, rel)), { recursive: true }); writeFileSync(path.join(cwd, rel), text); }
    for (const rel of s.remove ?? []) rmSync(path.join(cwd, rel), { force: true });
    if (s.before) for (const rel of s.before.remove ?? []) rmSync(path.join(cwd, rel), { force: true });
    if (s.cmd) await commands.get(s.cmd).handler(s.args ?? "", ctx);
    else if (s.event === "new_session") { await handlers.get("session_shutdown")?.({ reason: "new" }, ctx); entries = []; await handlers.get("session_start")?.({ reason: "new" }, ctx); }
    else if (s.event) { if (s.event === "session_shutdown" || s.event === "session_start") await handlers.get(s.event)?.({ reason: s.reason ?? "startup" }, ctx); }
    await flush();
    out.push({ step: s.cmd ? "/" + s.cmd : (s.event ?? "files"), events: events.map((e) => e.slice()) });
  }
  const files = tree(cwd);
  rmSync(cwd, { recursive: true, force: true }); rmSync(agent, { recursive: true, force: true });
  return { name: c.name, steps: out, files };
}

const cases = JSON.parse(readFileSync(process.argv[2], "utf8"));
const result = {};
for (const c of cases) result[c.name] = await runCase(c);
writeFileSync(process.argv[3], JSON.stringify(result, null, 1) + "\n");
console.log(Object.keys(result).length, "cases recorded");
