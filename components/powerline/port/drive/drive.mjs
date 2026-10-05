// Drives the ORIGINAL extension (port/oracle/index.ts) in-process with a scripted Pi host and records what it
// renders: the primary bar widget, the footer (secondary row) and the notification status widget, at several widths.
// Usage: node --experimental-strip-types drive/drive.mjs ../extensions/powerline-footer/testdata/render-states.json ../extensions/powerline-footer/testdata/render-golden.json
// The golden is produced by the original's code, not by the Go port.
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync } from "node:fs";
import { execFileSync } from "node:child_process";
import os, { tmpdir } from "node:os";
import { syncBuiltinESMExports } from "node:module";
import { join, resolve } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";

const here = resolve(import.meta.dirname);
const oracle = resolve(here, "..", "oracle");
const [scenFile, outFile] = process.argv.slice(2);
const scenarios = JSON.parse(readFileSync(scenFile, "utf8"));

// ThemeColor tokens Pi knows; Theme.fg throws on any other, as Pi's does.
const dts = readFileSync(join(oracle, "node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/theme/theme.d.ts"), "utf8");
const known = [...dts.match(/export type ThemeColor =([^;]*);/)[1].matchAll(/"(\w+)"/g)].map((m) => m[1]);
export const tokenCode = (t) => 17 + known.indexOf(t);
const theme = {
  fg(token, text) {
    if (!known.includes(token)) throw new Error(`Unknown theme color: ${token}`);
    return `\x1b[38;5;${tokenCode(token)}m${text}\x1b[39m`;
  },
  bg: (t, s) => s, bold: (s) => s,
};

// A frozen clock, so time segments are deterministic: TZ=UTC, Date.now() = clock.ms.
const clock = { ms: 0 };
const RealDate = Date;
globalThis.Date = class extends RealDate {
  constructor(...a) { a.length ? super(...a) : super(clock.ms); }
  static now() { return clock.ms; }
};

const noop = () => undefined;
const lenient = (o) => new Proxy(o, { get: (t, k) => (k in t ? t[k] : noop) });

// A fixed host name keeps the layout (it depends on the width of the host segment) the same on every machine.
os.hostname = () => "builder.local";
syncBuiltinESMExports();
const scrub = (s) => s;
const results = {};

for (const sc of scenarios) {
  // currency-rates.ts fixes its cache path at first load, so a state that needs its own cache runs in its own process.
  if (sc.isolate && !process.env.PL_CHILD) {
    const dir = mkdtempSync(join(tmpdir(), "pl-iso-"));
    writeFileSync(join(dir, "in.json"), JSON.stringify([sc]));
    execFileSync(process.execPath, ["--experimental-strip-types", fileURLToPath(import.meta.url), join(dir, "in.json"), join(dir, "out.json")], { env: { ...process.env, PL_CHILD: "1" }, stdio: "inherit" });
    results[sc.name] = JSON.parse(readFileSync(join(dir, "out.json"), "utf8"))[sc.name];
    continue;
  }
  const home = mkdtempSync(join(tmpdir(), "pl-drive-"));
  const agentDir = join(home, "agent");
  mkdirSync(agentDir, { recursive: true });
  writeFileSync(join(agentDir, "settings.json"), sc.rawSettings ?? JSON.stringify(sc.settings ?? {}));
  if (sc.currencyRates) { mkdirSync(join(agentDir, "powerline-footer"), { recursive: true }); writeFileSync(join(agentDir, "powerline-footer", "currency-rates.json"), JSON.stringify({ timestamp: Date.parse(sc.start ?? "2026-01-02T03:04:05Z"), rates: sc.currencyRates })); }
  if (sc.themeJson) { mkdirSync(join(agentDir, "extensions", "powerline-footer"), { recursive: true }); writeFileSync(join(agentDir, "extensions", "powerline-footer", "theme.json"), JSON.stringify(sc.themeJson)); }
  const cwd = (sc.cwd ?? "{HOME}/work/proj").replace("{HOME}", home);
  try { mkdirSync(cwd, { recursive: true }); } catch {}
  if (sc.projectSettings) { mkdirSync(join(cwd, ".pi"), { recursive: true }); writeFileSync(join(cwd, ".pi", "settings.json"), JSON.stringify(sc.projectSettings)); }
  if (sc.repo) {
    const git = (...a) => execFileSync("git", a, { cwd, stdio: "pipe", env: { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@t", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@t", GIT_AUTHOR_DATE: "2026-01-01T00:00:00Z", GIT_COMMITTER_DATE: "2026-01-01T00:00:00Z", GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_SYSTEM: "/dev/null" } });
    const r = sc.repo; const total = (r.unstaged ?? 0) + (r.staged ?? 0);
    git("init", "-q", "-b", r.branch ?? "main");
    if (r.remote) git("remote", "add", "origin", r.remote);
    for (let i = 0; i < Math.max(total, 1); i++) writeFileSync(join(cwd, `b${i}`), "base");
    git("add", "."); git("commit", "-q", "-m", "base");
    for (let i = 0; i < (r.unstaged ?? 0); i++) writeFileSync(join(cwd, `b${i}`), "changed");
    for (let i = 0; i < (r.staged ?? 0); i++) { writeFileSync(join(cwd, `b${(r.unstaged ?? 0) + i}`), "staged"); git("add", `b${(r.unstaged ?? 0) + i}`); }
    for (let i = 0; i < (r.untracked ?? 0); i++) writeFileSync(join(cwd, `u${i}`), "new");
    if (r.detached) git("checkout", "-q", "--detach");
  }
  for (const k of ["POWERLINE_NERD_FONTS", "TERM_PROGRAM", "TERM", "GHOSTTY_RESOURCES_DIR"]) delete process.env[k];
  Object.assign(process.env, { HOME: home, PI_CODING_AGENT_DIR: agentDir, POWERLINE_NERD_FONTS: "0", ...(sc.env ?? {}) });
  clock.ms = RealDate.parse(sc.start ?? "2026-01-02T03:04:05Z");

  // a fresh module graph per scenario (module state: theme caches, config)
  const mod = await import(pathToFileURL(join(oracle, "index.ts")).href + `?s=${encodeURIComponent(sc.name)}`);
  const handlers = {}; const commands = {}; const widgets = {}; const notes = []; let footer = null; const statuses = new Map(Object.entries(sc.statuses ?? {}));
  const pi = lenient({
    on: (ev, fn) => { (handlers[ev] ??= []).push(fn); },
    registerCommand: (n, o) => { commands[n] = o; },
    getThinkingLevel: () => sc.thinkingLevel ?? "off",
  });
  mod.default(pi);
  const entries = (sc.branch ?? []).map((e, i) => ({ id: `e${i}`, parentId: i ? `e${i - 1}` : null, timestamp: "2026-01-02T03:04:05Z", ...e }));
  const ui = lenient({
    setWidget: (k, f, o) => { if (f === undefined) delete widgets[k]; else widgets[k] = { f, o }; },
    setFooter: (f) => { footer = f; },
    setStatus: (k, v) => { if (v === undefined) statuses.delete(k); else statuses.set(k, v); },
    notify: (m, t) => notes.push([t ?? "info", m]),
    getEditorText: () => "", theme,
    getEditorComponent: () => undefined,
  });
  const state = { model: sc.model, thinkingLevel: sc.thinkingLevel ?? "off" };
  const ctx = lenient({
    hasUI: sc.hasUI !== false, mode: sc.mode ?? "tui", cwd, ui,
    get thinkingLevel() { return state.thinkingLevel; }, get model() { return state.model; },
    modelRegistry: { isUsingOAuth: () => !!sc.usingOAuth },
    settingsManager: { getCompactionSettings: () => ({ enabled: sc.autoCompact ?? true }) },
    sessionManager: lenient({
      getBranch: () => entries, getEntries: () => entries, getCwd: () => cwd, getLeafId: () => (entries.at(-1)?.id ?? null),
      getSessionId: () => sc.sessionId, getSessionName: () => sc.sessionName, buildContextEntries: () => [],
    }),
    getContextUsage: () => (sc.contextUsage === undefined ? undefined : sc.contextUsage),
    getSystemPrompt: () => "",
  });
  const fire = async (ev, data) => { for (const fn of handlers[ev] ?? []) await fn(data, ctx); };
  await fire("session_start", { type: "session_start", reason: sc.reason ?? "startup" });
  for (const step of sc.steps ?? []) {
    if (step.command) await commands[step.command].handler(step.args ?? "", ctx);
    if (step.event === "model_select") state.model = step.data.model;
    if (step.event === "thinking_level_select") state.thinkingLevel = step.data.level;
    if (step.event) await fire(step.event, step.data ?? {});
    if (step.advanceMs) clock.ms += step.advanceMs;
    if (step.status) { for (const [k, v] of Object.entries(step.status)) v === null ? statuses.delete(k) : statuses.set(k, v); }
  }
  clock.ms += sc.elapsedMs ?? 0;
  const footerData = { getExtensionStatuses: () => statuses, getGitBranch: () => sc.gitBranch ?? null, onBranchChange: () => noop, setExtensionStatus: noop };
  const tui = lenient({ requestRender: noop, terminal: { rows: 24, columns: 100 } });
  const comps = {};
  if (footer) comps.footer = footer(tui, theme, footerData);
  for (const [k, w] of Object.entries(widgets)) comps[k] = w.f(tui, theme);
  let settingsAfter = null;
  try { settingsAfter = JSON.parse(readFileSync(join(agentDir, "settings.json"), "utf8")); } catch {}
  let settingsRaw = null;
  try { settingsRaw = readFileSync(join(agentDir, "settings.json"), "utf8"); } catch {}
  const out = { notes, settingsAfter, settingsRaw, placement: {}, widths: {} };
  for (const [k, w] of Object.entries(widgets)) out.placement[k] = w.o?.placement ?? "aboveEditor";
  // git and currency data arrive asynchronously in the original: render once, wait for them, then record.
  if (sc.repo || sc.currencyRates) {
    for (const k of Object.keys(comps)) comps[k].render((sc.widths ?? [120])[0] + 7); // another width than the first recorded one, so the layout cache is not reused
    const gs = await import(pathToFileURL(join(oracle, "git-status.ts")).href);
    await gs.waitForGitUpdates();
    await new Promise((r) => setTimeout(r, 100));
    // The remote host is fetched separately and waitForGitUpdates does not cover it: poll until it is known.
    for (let i = 0; sc.repo?.remote && i < 30 && !gs.getGitRemoteHost(cwd); i++) await new Promise((r) => setTimeout(r, 100));
    await gs.waitForGitUpdates();
    await new Promise((r) => setTimeout(r, 50));
    for (const k of Object.keys(comps)) comps[k].render((sc.widths ?? [120])[0] + 9);
  }
  for (const width of sc.widths ?? [120]) {
    const row = {};
    for (const k of Object.keys(comps)) row[k] = comps[k].render(width).map(scrub);
    out.widths[width] = row;
  }
  results[sc.name] = out;
}
results._meta = { themeTokens: known, note: "hostname is builder.local; TZ=UTC; clock frozen" };
writeFileSync(outFile, JSON.stringify(results, null, 1) + "\n");
process.exit(0);
