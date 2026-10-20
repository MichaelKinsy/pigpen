// Times the ORIGINAL pi-goal-x (TypeScript, port/oracle) on the same operations and fixture as the Go benchmarks in
// extensions/pi-goal-x/perf_bench_test.go, for the performance section of port/PORT.md.
//
//   node --experimental-strip-types --expose-gc --min-semi-space-size=1024 --max-semi-space-size=1024 \
//        port/perf/bench-original.mjs [--n 300] [--only Name,Name]
//
// Prints one line per operation: "<Go benchmark name> ns/op=<n> B/op=<n>" (see below for B/op). The extension is loaded the way the
// original's own tests load it (a fake pi that records the registered tools, commands and handlers; a ctx with a scripted ui) and
// driven in process: no host, no IPC. Pass 1 times the operation; pass 2 estimates the V8 heap bytes allocated per operation from the
// growth of used_heap_size over the timed sections (the semi-space is sized so that no scavenge runs; a run in which a collection ran
// reports B/op=gc and no number). Like Go's B/op it excludes the restore work done outside the timed region. Timers are parked
// (setTimeout returns a handle that never fires), as the Go benchmark parks its timers.
import { mkdtempSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import v8 from "node:v8";
import { PerformanceObserver } from "node:perf_hooks";

const here = path.dirname(fileURLToPath(import.meta.url));
const oracle = path.join(here, "..", "oracle", "extensions");
const args = process.argv.slice(2);
const opt = (name, def) => { const i = args.indexOf(name); return i >= 0 ? args[i + 1] : def; };
const N = Number(opt("--n", "300"));
const only = opt("--only", "") ? new Set(opt("--only", "").split(",")) : null;

const goalExtension = (await import(path.join(oracle, "goal.ts"))).default;
const { invalidateGoalPoolCache } = await import(path.join(oracle, "storage", "goal-files.ts"));
const { invalidateGoalLedgerCache } = await import(path.join(oracle, "goal-ledger.ts"));
const { invalidateGoalSettingsCache } = await import(path.join(oracle, "goal-settings.ts"));

globalThis.setTimeout = () => ({ unref() { return this; }, ref() { return this; }, hasRef: () => false, refresh() { return this; } });
globalThis.clearTimeout = () => {};

process.env.PI_GOAL_AUTO_CONFIRM = "1";
for (const k of Object.keys(process.env)) if (k.startsWith("PI_GOAL_") && k !== "PI_GOAL_AUTO_CONFIRM") delete process.env[k];
process.env.PI_CODING_AGENT_DIR = mkdtempSync(path.join(tmpdir(), "goalperf-agent-"));

const identity = (_c, s) => s;
const theme = { fg: identity, bg: identity, bold: (s) => s, italic: (s) => s, underline: (s) => s, strikethrough: (s) => s, inverse: (s) => s };
const flush = () => new Promise((r) => setImmediate(r));

function tasks() {
  const out = [];
  for (let g = 1; g <= 3; g++) {
    const parent = `group-${g}`;
    out.push({ id: parent, title: `Milestone ${g} of the quarterly report`, verification_contract: "The section is written and reviewed." });
    for (let s = 1; s <= 3; s++) out.push({ id: `${parent}-step-${s}`, parent_id: parent, title: `Step ${s} of milestone ${g}` });
  }
  return out;
}

class Fixture {
  constructor(settings) {
    this.cwd = mkdtempSync(path.join(tmpdir(), "goalperf-"));
    mkdirSync(path.join(this.cwd, ".pi"), { recursive: true });
    writeFileSync(path.join(this.cwd, ".pi", "pi-goal-x-settings.json"), settings);
    this.entries = [];
    this.settings = settings;
  }
  async boot() {
    invalidateGoalPoolCache(); invalidateGoalLedgerCache(); invalidateGoalSettingsCache();
    const handlers = new Map(), commands = new Map(), tools = new Map();
    this.handlers = handlers; this.commands = commands; this.tools = tools;
    this.widgetFactory = null; this.keyCb = null; this.events = [];
    const entries = this.entries;
    const cwd = this.cwd;
    const pi = {
      registerTool: (def) => tools.set(def.name, def), registerCommand: (n, d) => commands.set(n, d), registerMessageRenderer() {},
      on: (e, h) => { if (!handlers.has(e)) handlers.set(e, []); handlers.get(e).push(h); },
      appendEntry: (t, d) => entries.push({ type: "custom", customType: t, data: JSON.parse(JSON.stringify(d)) }),
      sendMessage: (m) => this.events.push(m), sendUserMessage: (m) => this.events.push(m),
      getActiveTools: () => ["read", "bash", "edit", "write"], setActiveTools: () => {}, hasUI: true,
    };
    this.pi = pi;
    this.ctx = {
      cwd, hasUI: true, modelRegistry: { getAvailable: () => [] }, model: undefined,
      sessionManager: { getBranch: () => entries, getCwd: () => cwd, getSessionId: () => "perf-session", getRoot: () => cwd },
      ui: {
        notify: (m, l) => this.events.push([m, l]), setStatus: () => {},
        setWidget: (k, w) => { if (typeof w === "function") this.widgetFactory = w; },
        onTerminalInput: (cb) => { this.keyCb = cb; return () => {}; },
        select: async () => undefined, input: async () => undefined, confirm: async () => false, custom: async () => undefined, theme,
      },
      getSystemPrompt: () => "base", isIdle: () => true, hasPendingMessages: () => false, abort: () => {},
    };
    goalExtension(pi);
    this.core = pi._goalCore;
    await this.fire("session_start", { reason: "startup" });
    this.events.length = 0;
  }
  async fire(event, payload) {
    for (const h of this.handlers.get(event) ?? []) await h(payload, this.ctx);
    await flush();
  }
  async tool(name, params) {
    const r = await this.tools.get(name).execute("call-1", params, undefined, undefined, this.ctx);
    await flush();
    return r;
  }
  async command(name, a) { await this.commands.get(name).handler(a, this.ctx); await flush(); }
  snapshot() {
    this.files = {};
    const walk = (dir) => {
      for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) walk(p); else this.files[path.relative(this.cwd, p)] = readFileSync(p, "utf8");
      }
    };
    walk(path.join(this.cwd, ".pi"));
    this.snapshotEntries = JSON.parse(JSON.stringify(this.entries));
  }
  async restore() {
    rmSync(path.join(this.cwd, ".pi"), { recursive: true, force: true });
    for (const [rel, text] of Object.entries(this.files)) { mkdirSync(path.dirname(path.join(this.cwd, rel)), { recursive: true }); writeFileSync(path.join(this.cwd, rel), text); }
    this.entries.length = 0;
    this.entries.push(...JSON.parse(JSON.stringify(this.snapshotEntries)));
    await this.boot();
  }
  component() {
    if (!this.comp) this.comp = this.widgetFactory({ terminal: { rows: 24, columns: 100 }, requestRender() {}, children: [] }, theme);
    return this.comp;
  }
}

async function fixture(settings, ledgerRepeat = 0) {
  const f = new Fixture(settings);
  await f.boot();
  for (let i = 1; i <= 5; i++) await f.tool("create_goal", { objective: `Write the quarterly report ${i}\nSuccess criteria: three sections, each reviewed, with the figures checked against the ledger.` });
  await f.tool("set_goal_tasks", { tasks: tasks() });
  await f.tool("update_goal_task", { task_id: "group-1-step-1", status: "start" });
  f.snapshot();
  if (ledgerRepeat > 0) {
    const ledger = path.join(".pi", "goals", "goal_events.jsonl");
    if (!f.files[ledger]) throw new Error("the fixture has no ledger");
    f.files[ledger] = f.files[ledger].repeat(ledgerRepeat);
    delete f.files[path.join(".pi", "goals", ".goal-ledger-checkpoint.json")];
  }
  await f.restore();
  return f;
}

const OFF = '{"maxAutonomousRuns": 0}';
const ops = {
  SessionStart: { settings: OFF, mode: "read", op: (f) => f.fire("session_start", { reason: "startup" }) },
  ToolGetGoalSummary: { settings: OFF, mode: "read", op: (f) => f.tool("get_goal", {}) },
  ToolGetGoalTasks: { settings: OFF, mode: "read", op: (f) => f.tool("get_goal", { section: "tasks" }) },
  ToolCreateGoal: { settings: OFF, mode: "write", op: (f) => f.tool("create_goal", { objective: "Deploy to production\nSuccess criteria: the checks are green." }) },
  ToolUpdateGoalPause: { settings: OFF, mode: "write", op: (f) => f.tool("update_goal", { status: "paused", reason: "Waiting for the review of the second milestone.", suggested_action: "Ask the reviewer." }) },
  ToolSetGoalTasks: { settings: OFF, mode: "write", op: (f) => f.tool("set_goal_tasks", { tasks: tasks(), block_completion: true }) },
  ToolUpdateGoalTask: { settings: OFF, mode: "write", op: (f) => f.tool("update_goal_task", { task_id: "group-1-step-1", status: "complete", evidence: "The step is written." }) },
  CmdGoalList: { settings: OFF, mode: "read", op: (f) => f.command("goal-list", "") },
  CmdGoalStatus: { settings: OFF, mode: "read", op: (f) => f.command("goal-status", "") },
  CmdGoalStatusVerbose: { settings: OFF, mode: "read", op: (f) => f.command("goal-status", "verbose") },
  CmdGoalRefresh: { settings: OFF, mode: "read", op: (f) => f.command("goal-refresh", "") },
  CmdGoalDirect: { settings: OFF, mode: "write", op: (f) => f.command("goal-direct", "Ship the release\nSuccess criteria: tagged and published.") },
  RenderDashboardCompact: { settings: OFF, mode: "read", sync: true, setup: (f) => { f.component(); }, op: (f) => { if (f.core.isDashboardExpanded?.()) f.core.toggleDashboardExpanded(); f.component().render(100); } },
  RenderDashboardExpanded: { settings: OFF, mode: "read", sync: true, setup: (f) => { f.component(); }, op: (f) => { if (!f.core.isDashboardExpanded?.()) f.core.toggleDashboardExpanded(); f.component().render(100); } },
  KeyPlain: { settings: OFF, mode: "read", sync: true, op: (f) => { if (f.keyCb("a")) throw new Error("a plain key must not be consumed"); } },
  KeyToggleExpand: { settings: OFF, mode: "read", sync: true, op: (f) => { const r = f.keyCb("\x1b[116;6u"); if (!r?.consume) throw new Error("the toggle chord must be consumed"); } },
  SchedulerTick: { settings: '{"maxAutonomousRuns": 10}', mode: "write", op: async (f) => {
    f.core.scheduler.begin(f.ctx);
    await f.tool("update_goal", { continuation: { kind: "ready" } });
    f.core.scheduler.settled(f.ctx, true);
    await flush();
  } },
};

// The same operations over a ledger of about 16,000 events (the fixture's ledger repeated 2,000 times).
for (const [name, spec] of Object.entries({ SessionStart: ops.SessionStart, ToolGetGoalSummary: ops.ToolGetGoalSummary, CmdGoalStatus: ops.CmdGoalStatus, RenderDashboardCompact: ops.RenderDashboardCompact, KeyToggleExpand: ops.KeyToggleExpand, ToolUpdateGoalTask: ops.ToolUpdateGoalTask })) {
  const alias = { ToolGetGoalSummary: "GetGoalSummary", CmdGoalStatus: "GoalStatus", RenderDashboardCompact: "Render", ToolUpdateGoalTask: "UpdateGoalTask" }[name] ?? name;
  ops[`BigLedger${alias}`] = { ...spec, big: true };
}

let gcRuns = 0;
new PerformanceObserver((list) => { gcRuns += list.getEntries().length; }).observe({ entryTypes: ["gc"] });

async function run(name, spec, n) {
  const f = await fixture(spec.settings, spec.big ? 2000 : 0);
  if (spec.setup) await spec.setup(f);
  const prep = async () => {
    if (spec.mode === "write") { await f.restore(); if (spec.setup) await spec.setup(f); }
    f.events.length = 0;
  };
  let ns, bytes, gc;
  if (spec.sync) {
    // A synchronous read: one clock reading around the whole loop.
    for (let i = 0; i < 200; i++) spec.op(f);
    let t0 = process.hrtime.bigint();
    for (let i = 0; i < n; i++) spec.op(f);
    ns = Number(process.hrtime.bigint() - t0) / n;
    global.gc?.();
    await flush(); await flush();
    const before = gcRuns;
    const h0 = v8.getHeapStatistics().used_heap_size;
    for (let i = 0; i < n; i++) spec.op(f);
    bytes = (v8.getHeapStatistics().used_heap_size - h0) / n;
    await flush(); await flush();
    gc = gcRuns - before;
  } else {
    for (let i = 0; i < Math.min(20, n); i++) { await prep(); await spec.op(f); } // warm-up
    let total = 0n;
    for (let i = 0; i < n; i++) {
      await prep();
      const t0 = process.hrtime.bigint();
      await spec.op(f);
      total += process.hrtime.bigint() - t0;
    }
    ns = Number(total) / n;
    global.gc?.();
    await flush(); await flush();
    const before = gcRuns;
    bytes = 0;
    for (let i = 0; i < n; i++) {
      await prep();
      const h0 = v8.getHeapStatistics().used_heap_size;
      await spec.op(f);
      bytes += v8.getHeapStatistics().used_heap_size - h0;
    }
    bytes /= n;
    await flush(); await flush();
    gc = gcRuns - before;
  }
  rmSync(f.cwd, { recursive: true, force: true });
  console.log(`Benchmark${name} ns/op=${Math.round(ns)} B/op=${gc > 0 ? "gc" : Math.round(bytes)}`);
}

for (const [name, spec] of Object.entries(ops)) {
  if (only && !only.has(name) && !only.has(`Benchmark${name}`)) continue;
  await run(name, spec, N);
}
process.exit(0);
