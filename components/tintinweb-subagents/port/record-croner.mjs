#!/usr/bin/env node
// Records the differential fixtures of the Go cron parser (extensions/tintinweb-subagents/cron.go) from the original's
// dependency, croner 10.0.1 (port/oracle/node_modules/croner, after `npm ci` in port/oracle). Node is used here, to
// record, and never by the extension at runtime.
//
//   node port/record-croner.mjs > extensions/tintinweb-subagents/testdata/croner-vectors.json
//
// Each vector is one `new Cron(pattern, fn)` exactly as src/schedule.ts builds it (no options, so the process's local
// time zone), asked for its next run after `from`, then after that run, and so on (Cron.nextRun(Date) chained).
// `valid` is whether the constructor accepted the pattern, which is what SubagentScheduler.validateCronExpression
// reports. The corpus is deterministic: curated patterns plus a seeded generator. The time zone is the TZ the
// recorder runs under, so the script re-runs itself once per zone and merges the results.
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
// CRONER_FROM overrides the directory croner is resolved from (default: port/oracle, after `npm ci` there).
const require = createRequire(join(process.env.CRONER_FROM || join(here, "oracle"), "package.json"));
const { Cron } = require("croner");

const ZONES = ["UTC", "America/New_York", "Europe/Berlin", "Australia/Lord_Howe", "Asia/Kolkata", "America/St_Johns"];

// An instant is [ISO string, free text]. UTC runs them all; the DST zones only the ones near a transition.
const ANCHORS = [
  "2026-10-20T09:00:00.000Z",
  "2026-10-20T09:00:00.500Z",
  "2026-12-31T23:59:59.500Z",
  "2028-02-28T23:59:58.000Z",
  "2026-03-08T06:30:00.000Z",
  "2026-11-01T05:30:00.000Z",
  "2026-03-29T00:30:00.000Z",
  "2026-10-25T00:30:00.000Z",
  "2026-04-04T14:30:00.000Z",
  "2026-10-03T14:30:00.000Z",
  "2999-12-31T23:59:59.000Z",
  "2100-02-28T12:00:00.000Z",
];
const DST_ANCHORS = ANCHORS.slice(4, 10);

const CURATED = [
  "* * * * * *", "0 0 9 * * 1", "0 0 9 * * *", "*/5 * * * * *", "0 */15 * * * *", "0 0 */2 * * *",
  "0 30 8-17 * * 1-5", "0 0 0 1 * *", "0 0 0 1 1 *", "0 0 0 29 2 *", "0 0 0 30 2 *", "0 0 0 31 2 *", "0 0 0 31 * *",
  "0 0 0 31 4 *", "0 0 0 L * *", "0 0 0 LW * *", "0 0 0 15W * *", "0 0 0 1W * *", "0 0 0 31W * *", "0 0 0 30W 2 *",
  "0 0 12 * * 5L", "0 0 12 * * FRI#2", "0 0 12 * * 1#1", "0 0 12 * * 3#5", "0 0 12 * * 7", "0 0 12 * * 0",
  "0 0 12 * * SUN", "0 0 12 * * mon-fri", "0 0 12 * * sat,sun", "0 0 12 * JAN,MAR *", "0 0 12 * jan-mar *",
  "0 0 12 * FEB-feb *", "0 0 12 15 * 1", "0 0 12 15 * +1", "0 0 12 L * 5", "0 0 12 L * +5", "0 0 12 ? * ?",
  "0 0 12 * * ?", "0 0 12 ? * MON", "5-10/2 * * * * *", "0-59/20 * * * * *", "0 0-23/6 * * * *", "0 0 0 1-31/10 * *",
  "0 0 0 * 1-12/3 *", "0 0 0 * */4 *", "0 0 12 * * */2", "0 0 12 * * 1-5#2", "0 0 12 * * 1#2#3", "0 0 12 * * 1#",
  "0 0 12 * * 5#2,1L", "0 0 12 * * FRIL", "0 0 12 * * frid", "0 0 12 * * L", "0 0 12 * * 5#0", "0 0 12 * * 5#6",
  "0 0 12 * * 5#x", "0 0 12 * * 1-5/2", "0 0 12 * * SUN-SAT", "0 0 12 * * mon-sun", "0 0 12 * * 6-0",
  "60 * * * * *", "* 60 * * * *", "* * 24 * * *", "* * * 0 * *", "* * * 32 * *", "* * * * 13 *", "* * * * 0 *",
  "* * * * * 8", "* * * * * -1", "0 0 0 * * 7#2", "1,,2 * * * * *", ", * * * * *", "1-2-3 * * * * *", "*/2/3 * * * * *",
  "1/5 * * * * *", "/5 * * * * *", "*/ * * * * *", "*/0 * * * * *", "*/61 * * * * *", "*/60 * * * * *", "5-1 * * * * *",
  "a b c d e f", "@daily * * * * *", "@yearly x x x x x", "* * * * * * *", "* * * * *", "0 0 9:00 * * 1", "0 0 12:00 * * *",
  "1W-5W * * * * *", "0 0 0 W * *", "0 0 0 1W-3 * *", "0 0 0 5W/2 * *", "0 0 0 L-3 * *", "0 0 0 LL * *", "0 0 0 15L * *",
  "0 0 0 1,L * *", "0 0 0 L,15 * *", "0 0 0 LW,1 * *", "0 0 0 1,15W * *", "* * * * * +1", "* * * * * +", "* * * * * +*",
  "0 0 0 * * 1,3,5", "0 0 0 * * 5-7", "0 0 0 * * 0-7", "0 0 0 1 1 1", "0 0 0 1 1 +1", "0 0 0 13 * 5", "0 0 0 13 * +5",
  "0 0 0 * * 1#1,1#3", "0 0 0 * * 1L,2L", "0 0 0 * * MON#1", "0 0 0 * * mon#L", "0 0 0 * * 1#l", "0 0 0 * Feb *",
  "0 0 0 * MAY-AUG *", "0 0 0 * dec-jan *", "0 0 0 * 12-1 *", "0 0 3 * * *", "0 0 2 * * *", "0 30 2 * * *", "0 0 1 * * *",
  "0 15 2 * * *", "0 59 1 * * *", "0 0 0 * * *", "59 59 23 * * *", "0 0 12 29 2 *", "0 0 12 29 2 1", "0 0 12 28-31 * *",
  "30 4 1,15 * 5", "0 0 * * * *", "0 * * * * *", "*/15 */2 * * * *", "1,2,3,4 5,6 7,8 9 10 *", "0 0 0 31 12 *",
  "0 0 0 1 JAN *", "  0   0   9   *   *   1  ", "0\t0\t9\t*\t*\t1", "0 0 9 * * 1\n", "０ 0 9 * * 1", "0 0 9 * * 1;", "0 0 9 * * MON-FRI,SAT",
  "0 0 9 * * mon,Tue,WED", "0 0 9 * * 1,2,3,4,5,6,7", "0 0 0 * * 2-6", "99 * * * * *", "-1 * * * * *", "1.5 * * * * *",
  "0x1 * * * * *", "1e1 * * * * *", "+1 * * * * *", "0 0 0 31 * 5", "0 0 0 31 * +5", "0 0 0 30 2 5", "0 0 0 30 2 +5",
  "0 0 0 * * 1#2,3#4,5L", "0 0 0 1-5 * 6", "0 0 0 1W 3 *", "0 0 0 15W 8 *", "0 0 0 LW 2 *", "0 0 0 L 2 *", "0 0 0 L 2 1",
  // A pattern with a `:` is a date for Date.parse, and a one-shot: the six-word forms the original's gate lets through.
  "Tue Jan 01 2030 10:00:00 GMT+0100", "Tue, 01 Jan 2030 10:00:00 GMT", "Wed Dec 31 2025 23:59:59 GMT-0500", "Thu, 29 Feb 2024 12:00:00 GMT",
  "tue jan 01 2030 10:00 gmt", "Mon Jan 01 2030 10:00:00 GMT+0100", "Mon Jan 01 2001 00:00:00 GMT+0000", "Tue Jan 01 2030 25:00:00 GMT",
];

// A small seeded generator (mulberry32), so the corpus is the same every run.
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = rng(20261020);
const pick = (n) => Math.floor(rand() * n);
const FIELDS = [
  { max: 59, min: 0 }, { max: 59, min: 0 }, { max: 23, min: 0 }, { max: 31, min: 1 }, { max: 12, min: 1 }, { max: 7, min: 0 },
];
const NAMES_M = ["jan", "FEB", "Mar", "apr", "may", "JUN", "jul", "aug", "SEP", "oct", "nov", "Dec"];
const NAMES_D = ["sun", "MON", "Tue", "wed", "thu", "FRI", "sat"];
function field(i) {
  const { min, max } = FIELDS[i];
  const over = rand() < 0.06 ? 3 : 0; // now and then an out-of-range value
  const n = () => min + pick(max - min + 1 + over);
  // The extensions: `L`, `LW` and `nW` for the day of month; names for the month; `nL`, `n#k`, names and the `+`
  // (day-of-month AND day-of-week) prefix for the day of week.
  if (i === 3 && rand() < 0.2) return ["L", "LW", `${1 + pick(31)}W`, `${1 + pick(31)}W,${1 + pick(28)}`, `L,${1 + pick(28)}`][pick(5)];
  if (i === 4 && rand() < 0.2) { const a = pick(12), b = pick(12); return rand() < 0.5 ? NAMES_M[a] : `${NAMES_M[Math.min(a, b)]}-${NAMES_M[Math.max(a, b)]}`; }
  if (i === 5 && rand() < 0.4) {
    const k = [() => `${pick(7)}L`, () => `${pick(7)}#${1 + pick(5)}`, () => NAMES_D[pick(7)], () => `${NAMES_D[pick(7)]}#${1 + pick(5)}`,
      () => `+${pick(7)}`, () => `+${pick(7)}#${1 + pick(5)}`, () => `${pick(7)}#${1 + pick(5)},${pick(7)}L`, () => `${NAMES_D[pick(7)]}-fri`][pick(8)];
    return k();
  }
  if (i < 2 && rand() < 0.4) return "0"; // fixed seconds and minutes make a run a day or a month apart, which is where the calendar walk matters
  switch (pick(7)) {
    case 0: return "*";
    case 1: return String(n());
    case 2: { const a = n(), b = n(); return `${Math.min(a, b)}-${Math.max(a, b)}`; }
    case 3: return `*/${1 + pick(Math.max(1, Math.floor(max / 2)))}`;
    case 4: { const a = n(), b = n(); return `${Math.min(a, b)}-${Math.max(a, b)}/${1 + pick(5)}`; }
    case 5: return [n(), n(), n()].join(",");
    default: return String(n());
  }
}
const GENERATED = [];
for (let k = 0; k < 450; k++) GENERATED.push([0, 1, 2, 3, 4, 5].map(field).join(" "));
// Instants anywhere in 2026-2045, to the millisecond (the same in every run).
const RANDOM_ANCHORS = [];
for (let k = 0; k < 3; k++) RANDOM_ANCHORS.push(new Date(Date.UTC(2026, 0, 1) + Math.floor(rand() * 20 * 365.25 * 86400000)).toISOString());

// The ones a daylight-saving transition changes: a wall-clock hour that does not exist or happens twice.
const DST_PATTERNS = ["0 30 2 * * *", "0 0 2 * * *", "0 0 3 * * *", "0 0 1 * * *", "0 15 2 * * *", "0 59 1 * * *", "0 30 1 * * *",
  "0 0 */1 * * *", "0 */30 * * * *", "*/20 * * * * *", "0 0 2 * * 0", "0 0 0 * * *", "0 0 12 * * *", "0 0 0 L * *"];
const PATTERNS = [...new Set([...CURATED, ...GENERATED])];

// Strings for `new Date(string)`, which src/schedule.ts uses on anything that starts like an ISO date-time. Recorded
// under UTC and a zone with daylight saving, since a date-time without an offset is local time.
const DATES = [
  "2030-01-01T10:00", "2030-01-01T10:00:00", "2030-01-01T10:00:00.5", "2030-01-01T10:00:00.123456", "2030-01-01T10:00:00.1234",
  "2030-01-01T10:00Z", "2030-01-01T10:00:00+01:00", "2030-01-01T10:00:00+0100", "2030-01-01T10:00:00+01", "2030-01-01T10",
  "2030-01-01T10:00:00z", "2030-01-01T24:00:00", "2030-01-01T24:00:01", "2030-01-01T24:00:00.001", "2030-02-30T10:00",
  "2030-02-29T10:00", "2030-04-31T10:00", "2030-01-32T10:00", "2030-01-00T10:00", "2030-00-10T10:00", "2030-13-01T10:00",
  "2030-01-01T10:60", "2030-01-01T10:00:60", "2030-01-01T10:00:00 +01:00", "2030-01-01T10:00:00Z junk", "2030-01-01T10:00:00.Z",
  "2030-01-01T10:00:00,5Z", "2030-01-01Tfoo", "2030-01-01T", "2030-01-01T10:00:00 PST", "2030-01-01T10:00:00 GMT", "2030-01-01T10:0",
  "2030-01-01T1:00", "2030-01-01T10:00:0", "2030-01-01T10:00:00-00:00", "2030-01-01T10:00:00+24:00", "2030-01-01T10:00:00+23:59",
  "2030-01-01T10:00:00+00:60", "2030-01-01T10:00:00.123+05:30", "2030-01-01T10:00:00Z ", "2030-01-01T10:00:00 ", "2030-01-01T10:00:00\n",
  "2030-01-01T23:59:59.999999999Z", "2030-01-01T10:00:00+5", "2030-01-01T10:00:00+05:3", "2030-01-01T10:00:00-01", "9999-12-31T23:59:59.999Z",
  "9999-12-31T23:59:59.999", "2030-01-01T10:00:00.", "2030-01-01T10.5", "2030-01-01T10:00:00E", "2030-01-01T10:00:00 Z", "1999-12-31T23:59:59Z",
  "2026-03-08T02:30", "2026-03-08T02:30:00", "2026-03-08T03:30", "2026-11-01T01:30", "2026-11-01T01:30:00.500", "2026-11-01T00:59:59.999",
  "2026-11-01T02:00", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "-271821-04-20T00:00:00.000Z", "-000000-01-01T00:00:00Z",
  "+002030-01-01T10:00:00Z", "+002030-01-01T10:00", "0099-01-01T00:00:00Z", "1970-01-01T00:00:00Z", "1969-12-31T23:59:59.999Z", "2030-01-01t10:00",
  "2030-01-01T10:00:00+0530", "2030-01-01T10:00:00-0800", "2030-01-01T10:00:00-08:00", "2100-02-29T10:00Z", "2030-01-01T10:00:00.0Z", "2030-01-01T10:00:00.000000Z",
];

function accepts(pattern) {
  try {
    // Not stopped here: a stopped Cron answers nextRun with null. chainFor stops it.
    return { ok: true, c: new Cron(pattern, () => {}) };
  } catch (e) {
    return { ok: false, error: String(e && e.message) };
  }
}

function chainFor(pattern, from, length) {
  const { ok, c } = accepts(pattern);
  if (!ok) return null;
  const out = [];
  let prev = new Date(from);
  try {
    for (let i = 0; i < length; i++) {
      const n = c.nextRun(prev);
      if (!n) break;
      out.push(n.getTime());
      prev = n;
    }
  } catch (e) {
    return { error: String(e && e.message) };
  } finally {
    c.stop();
  }
  return out;
}

// ---- the stack depth: how many nested calls of croner's `recurse` a search makes, and when V8 gives up ----
// croner's constructor searches for the first run, and the search recurses once per field it settles or carries, with
// nothing unwinding until it is done. A pattern that never runs and fails at the day level makes thousands of calls on
// the way to the year 3000 and V8 throws "Maximum call stack size exceeded" from `new Cron`. cron.go counts the same
// calls. The count is read by a copy of croner whose `recurse` increments a counter; each probe is a fresh process (the
// depth V8 allows depends on how warm the code is, and the original builds its Cron cold).
const STACK_EXTRA = ["0 0 8 27 * +6#3", "0 0 0 31 2 *", "0 0 0 30 2 *", "0 0 0 31 4,6,9,11 *", "0 0 0 31 * +2#1", "0 0 0 1 * +1#2",
  "0 0 0 1-7 * +1#2", "0 0 12 * * +1#5", "0 0 0 15W * +6", "0 0 0 LW * +6", "0 0 0 L * +1#1", "0 0 0 29 2 +2#1", "0 0 0 29 2 *",
  "0 0 0 29 2 +1", "0 0 0 31 * 5", "0 0 0 31 * +5", "0 0 0 30 2 +0", "0 0 0 13 * +5", "0 0 0 13 * +5#1", "0 0 0 5W * +0", "0 0 0 L 2 +1#5"];

if (process.argv[2] === "--probe") {
  const [file, pattern, from] = process.argv.slice(3);
  const { Cron } = await import(file);
  globalThis.__calls = 0;
  let outcome;
  try {
    outcome = new Cron(pattern).nextRun(new Date(from)) ? "ok" : "none";
  } catch (e) {
    outcome = /call stack/i.test(String(e && e.message)) ? "overflow" : "error";
  }
  process.stdout.write(JSON.stringify({ calls: globalThis.__calls, outcome }));
} else if (process.argv[2] === "--stack") {
  const { spawn } = await import("node:child_process");
  const { mkdtempSync, readFileSync, writeFileSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { pathToFileURL } = await import("node:url");
  const dir = join(process.env.CRONER_FROM || join(here, "oracle"), "node_modules", "croner", "dist");
  const source = readFileSync(join(dir, "croner.js"), "utf8");
  if (source.split("recurse(e,t,r){").length !== 2) throw new Error("croner's recurse is not where the counter expects it");
  const tmp = mkdtempSync(join(tmpdir(), "croner-count-"));
  writeFileSync(join(tmp, "croner.mjs"), source.replace("recurse(e,t,r){", "recurse(e,t,r){globalThis.__calls=(globalThis.__calls||0)+1;"));
  const file = pathToFileURL(join(tmp, "croner.mjs")).href;
  const patterns = [...new Set([...PATTERNS, ...STACK_EXTRA])].filter((p) => p.trim().split(/\s+/).length === 6 && !p.includes(":"));
  const froms = [ANCHORS[0], ANCHORS[2], ...RANDOM_ANCHORS.slice(0, 2)];
  const jobs = patterns.flatMap((pattern) => froms.map((from) => ({ pattern, from })));
  const results = [];
  let next = 0;
  const worker = async () => {
    while (next < jobs.length) {
      const job = jobs[next++];
      const out = await new Promise((resolve) => {
        let text = "";
        const child = spawn(process.execPath, [fileURLToPath(import.meta.url), "--probe", file, job.pattern, job.from], { env: { ...process.env, TZ: "UTC" } });
        child.stdout.on("data", (d) => { text += d; });
        child.on("close", () => resolve(text));
      });
      const got = JSON.parse(out);
      if (got.outcome !== "error") results.push({ pattern: job.pattern, from: Date.parse(job.from), calls: got.calls, outcome: got.outcome });
    }
  };
  await Promise.all(Array.from({ length: 12 }, worker));
  results.sort((a, b) => a.pattern.localeCompare(b.pattern) || a.from - b.from);
  const done = results.filter((r) => r.outcome !== "overflow").map((r) => r.calls);
  const over = results.filter((r) => r.outcome === "overflow").map((r) => r.calls);
  const most = Math.max(...done), fewest = over.length ? Math.min(...over) : Infinity;
  console.error(`stack: ${results.length} probes, ${over.length} overflowed (calls ${fewest}..${over.length ? Math.max(...over) : "-"}), the most a completed search made ${most}`);
  if (over.length) console.error(`LIMIT ${Math.floor((most + fewest) / 2)}`);
  if (most >= fewest) console.error("WARNING: a completed search made more calls than the fewest at which one overflowed; the limit is not clean");
  const keep = results.filter((r, i) => r.outcome === "overflow" || r.calls >= most * 0.4 || i % 12 === 0);
  process.stdout.write(JSON.stringify({ croner: "10.0.1", node: process.version, probes: results.length, entries: keep }, null, 0).replace(/\},\{/g, "},\n{") + "\n");
} else if (process.argv[2] === "--zone") {
  const zone = process.env.TZ;
  const at = Date.now(); // `new Cron` searches for the first run from now: a pattern that overflows does so relative to this
  const vectors = [];
  const patterns = zone === "UTC" ? PATTERNS : [...new Set([...PATTERNS.filter((_, i) => i % 7 === 0), ...DST_PATTERNS])];
  const anchors = [...(zone === "UTC" ? [0, 1, 2, 3, 10, 11].map((i) => ANCHORS[i]) : DST_ANCHORS), ...RANDOM_ANCHORS];
  for (const pattern of patterns) {
    const valid = accepts(pattern);
    if (valid.ok) valid.c.stop();
    if (!valid.ok) {
      if (zone === "UTC") vectors.push({ zone, pattern, valid: false });
      continue;
    }
    for (const from of anchors) {
      const chain = chainFor(pattern, from, 3);
      vectors.push(chain && !Array.isArray(chain)
        ? { zone, pattern, valid: true, from: Date.parse(from), throws: chain.error }
        : { zone, pattern, valid: true, from: Date.parse(from), chain });
    }
  }
  const dates = zone === "UTC" || zone === "America/New_York"
    ? DATES.map((input) => { const t = new Date(input).getTime(); return { zone, input, ms: Number.isNaN(t) ? null : t }; })
    : [];
  process.stdout.write(JSON.stringify({ at, vectors, dates }));
} else {
  const all = [];
  const dates = [];
  const at = {};
  for (const zone of ZONES) {
    const r = spawnSync(process.execPath, [fileURLToPath(import.meta.url), "--zone"], {
      env: { ...process.env, TZ: zone }, encoding: "utf8", maxBuffer: 1 << 28,
    });
    if (r.status !== 0) throw new Error(`zone ${zone}: ${r.stderr}`);
    const got = JSON.parse(r.stdout);
    at[zone] = got.at;
    all.push(...got.vectors);
    dates.push(...got.dates);
  }
  process.stdout.write(JSON.stringify({ croner: "10.0.1", at, vectors: all, dates }, null, 0).replace(/\},\{/g, "},\n{") + "\n");
}
