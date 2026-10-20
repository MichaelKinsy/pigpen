// Drives the ORIGINAL extension (port/oracle/index.ts) in-process with a scripted Pi host and records what its prompt queue does:
// the notifications of /queue and of the delivery, the messages it sends (pi.sendUserMessage and its options), the overlays of
// the queue picker (the real pi-tui SelectList, rendered at several widths after every key), the primary bar's queue segment, the
// queue preview widget and the inbox file afterwards.
// Usage (from port/): node --experimental-strip-types drive/queue-drive.mjs ../extensions/powerline-footer/testdata/queue-states.json ../extensions/powerline-footer/testdata/queue-golden.json
// The golden is produced by the original's code, not by the Go port. TZ=UTC; Date.now() is frozen, so updatedAt is deterministic.
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync } from "node:fs";
import os, { tmpdir } from "node:os";
import { syncBuiltinESMExports } from "node:module";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const here = resolve(import.meta.dirname);
const oracle = resolve(here, "..", "oracle");
const [scenFile, outFile] = process.argv.slice(2);
const scenarios = JSON.parse(readFileSync(scenFile, "utf8"));

// ThemeColor tokens Pi knows; Theme.fg throws on any other, as Pi's does.
const dts = readFileSync(join(oracle, "node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/theme/theme.d.ts"), "utf8");
const known = [...dts.match(/export type ThemeColor =([^;]*);/)[1].matchAll(/"(\w+)"/g)].map((m) => m[1]);
const theme = {
  fg(token, text) {
    if (!known.includes(token)) throw new Error(`Unknown theme color: ${token}`);
    return `\x1b[38;5;${17 + known.indexOf(token)}m${text}\x1b[39m`;
  },
  bg: (t, s) => s, bold: (s) => s,
};

const clock = { ms: 0 };
const RealDate = Date;
globalThis.Date = class extends RealDate {
  constructor(...a) { a.length ? super(...a) : super(clock.ms); }
  static now() { return clock.ms; }
};
os.hostname = () => "builder.local";
syncBuiltinESMExports();

const noop = () => undefined;
const lenient = (o) => new Proxy(o, { get: (t, k) => (k in t ? t[k] : noop) });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const results = {};

for (const sc of scenarios) {
  const home = mkdtempSync(join(tmpdir(), "pl-queue-"));
  const agentDir = join(home, "agent");
  mkdirSync(join(agentDir, "powerline-footer"), { recursive: true });
  const cwd = join(home, "work", "proj");
  mkdirSync(cwd, { recursive: true });
  const sub = (v) => JSON.parse(JSON.stringify(v).replaceAll("{CWD}", cwd).replaceAll("{HOME}", home));
  writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ powerline: { welcome: false, ...(sc.powerline ?? {}) } }));
  if (sc.inbox) writeFileSync(join(agentDir, "powerline-footer", "inbox.jsonl"), sub(sc.inbox).map((i) => JSON.stringify(i)).join("\n") + "\n");
  if (sc.aliases) writeFileSync(join(agentDir, "powerline-footer", "projects.json"), JSON.stringify(sub(sc.aliases), null, 2) + "\n");
  for (const k of ["POWERLINE_NERD_FONTS", "TERM_PROGRAM", "TERM", "GHOSTTY_RESOURCES_DIR"]) delete process.env[k];
  Object.assign(process.env, { HOME: home, PI_CODING_AGENT_DIR: agentDir, POWERLINE_NERD_FONTS: "0" });
  clock.ms = RealDate.parse("2026-01-02T03:04:05Z");

  const mod = await import(pathToFileURL(join(oracle, "index.ts")).href + `?s=${encodeURIComponent(sc.name)}`);
  const handlers = {}; const commands = {}; const widgets = {}; const notes = []; const sends = [];
  let editorText = null; let idle = sc.idle ?? true; let compacts = 0;
  const pi = lenient({
    on: (ev, fn) => { (handlers[ev] ??= []).push(fn); },
    registerCommand: (n, o) => { commands[n] = o; },
    sendUserMessage: (text, options) => {
      sends.push([text, options ?? null]);
      if (sc.sendError) throw new Error(sc.sendError);
      if (sc.ack) void fire("before_agent_start", { type: "before_agent_start", prompt: text });
    },
  });
  mod.default(pi);
  // The overlays the picker opens: ui.custom(factory, options) with the real pi-tui SelectList behind the factory.
  const tui = lenient({ requestRender: noop, terminal: { rows: 24, columns: 100 } });
  const overlays = []; let waiter = null;
  const ui = lenient({
    setWidget: (k, f, o) => { if (f === undefined) delete widgets[k]; else widgets[k] = { f, o }; },
    notify: (m, t) => notes.push([t ?? "info", m]),
    setEditorText: (t) => { editorText = t; },
    getEditorText: () => "", theme,
    getEditorComponent: () => undefined,
    custom: (factory, options) => new Promise((resolveResult) => {
      const overlay = { value: "open", options };
      overlay.component = factory(tui, theme, lenient({}), (v) => { overlay.value = v ?? null; resolveResult(v); });
      overlays.push(overlay);
      waiter?.();
    }),
  });
  const ctx = lenient({
    hasUI: sc.hasUI !== false, mode: sc.mode ?? "tui", cwd, ui,
    isIdle: () => idle, compact: () => { compacts++; },
    modelRegistry: { isUsingOAuth: () => false },
    settingsManager: { getCompactionSettings: () => ({ enabled: true }) },
    sessionManager: lenient({ getBranch: () => [], getEntries: () => [], getCwd: () => cwd, getLeafId: () => null, getSessionId: () => sc.sessionId, getSessionName: () => undefined, buildContextEntries: () => [] }),
    getContextUsage: () => undefined,
    getSystemPrompt: () => "",
  });
  async function fire(ev, data) { for (const fn of handlers[ev] ?? []) await fn(data, ctx); }

  const nextOverlay = async () => {
    for (let i = 0; i < 200 && overlays.length === 0; i++) await new Promise((r) => { waiter = r; setTimeout(r, 10); });
    waiter = null;
    return overlays.shift();
  };
  const widths = sc.widths ?? [100];
  const recorded = [];

  await fire("session_start", { type: "session_start", reason: "startup" });
  for (const step of sc.steps ?? []) {
    if (step.idle !== undefined) idle = step.idle;
    if (step.wait) await sleep(step.wait);
    if (step.event) await fire(step.event, { type: step.event, ...(step.data ?? {}) });
    if (step.command) {
      const done = commands[step.command].handler(sub(step.args ?? ""), ctx);
      let settled = false;
      const finished = Promise.resolve(done).then(() => { settled = true; }, (e) => { settled = true; notes.push(["throw", String(e.message ?? e)]); });
      for (const level of step.levels ?? []) {
        const overlay = await Promise.race([nextOverlay(), finished.then(() => undefined)]);
        if (!overlay) break;
        const entry = { options: overlay.options ? { overlay: overlay.options.overlay ?? null, aligned: overlay.options.overlayOptions?.() ?? null } : null, frames: [], result: undefined };
        const frame = (after) => { const f = { after }; for (const w of level.widths ?? [60, 100]) f[w] = overlay.component.render(w); entry.frames.push(f); };
        frame("");
        for (const key of level.keys ?? []) {
          overlay.component.handleInput(key);
          await sleep(0);
          if (overlay.value !== "open") break;
          frame(key);
        }
        entry.result = overlay.value;
        recorded.push(entry);
      }
      await Promise.race([finished, sleep(500)]);
      if (!settled) notes.push(["hang", step.command]);
    }
  }
  const comps = {};
  for (const [k, w] of Object.entries(widgets)) comps[k] = w.f(tui, theme);
  const out = { notes, sends, editorText, compacts, overlays: recorded, widths: {}, placement: {} };
  for (const [k, w] of Object.entries(widgets)) out.placement[k] = w.o?.placement ?? "aboveEditor";
  for (const width of widths) {
    const row = {};
    for (const k of Object.keys(comps)) if (k === "powerline-top" || k === "powerline-queue-preview" || k === "powerline-pending-send") row[k] = comps[k].render(width);
    out.widths[width] = row;
  }
  const read = (name) => { try { return readFileSync(join(agentDir, "powerline-footer", name), "utf8").replaceAll(cwd, "{CWD}").replaceAll(home, "{HOME}"); } catch { return null; } };
  out.inbox = read("inbox.jsonl");
  out.aliases = read("projects.json");
  await fire("session_shutdown", { type: "session_shutdown", reason: "quit" });
  out.inboxAfterShutdown = read("inbox.jsonl");
  results[sc.name] = JSON.parse(JSON.stringify(out).replaceAll(cwd, "{CWD}").replaceAll(home, "{HOME}"));
}
results._meta = { themeTokens: known, note: "TZ=UTC; clock frozen; paths are {CWD} and {HOME}" };
writeFileSync(outFile, JSON.stringify(results, null, 1) + "\n");
process.exit(0);
