// Generates a cron golden file: what croner 10.0.1 (the original's dependency, under Node) answers for a deterministic corpus of
// six-field expressions, in several time zones: the validation error (or none) and the next runs after reference instants.
// The Go port (extensions/schedule-prompt/internal/croner) is compared with it by TestCronGolden.
//
//   node gen-cron-golden.mjs [--out cron-golden.json.gz] [--exprs 1200] [--refs 10] [--seed 1] [--croner <dir with croner.js>]
//
// croner is loaded from --croner (default: ../oracle/node_modules/croner/dist, after `npm ci` in ../oracle).
import { gzipSync } from "node:zlib";
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const args = Object.fromEntries(process.argv.slice(2).reduce((acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc), []));
const OUT = args.out || "cron-golden.json.gz";
const EXPRS = Number(args.exprs || 1200);
const REFS = Number(args.refs || 10);
const SEED = Number(args.seed || 1);
const CRONER = resolve(args.croner || new URL("../oracle/node_modules/croner/dist", import.meta.url).pathname);
const { Cron } = await import(pathToFileURL(CRONER + "/croner.js").href);

const ZONES = ["UTC", "America/New_York", "Europe/London", "Australia/Lord_Howe", "America/Sao_Paulo", "Asia/Kolkata", "Pacific/Chatham"];

function mulberry32(a) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rnd = mulberry32(20261021 + SEED);
const pick = (a) => a[Math.floor(rnd() * a.length)];
const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));
const chance = (p) => rnd() < p;

const MON = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec", "JAN", "Mar", "DEC"];
const DOW = ["sun", "mon", "tue", "wed", "thu", "fri", "sat", "SUN", "Fri", "WED"];

// one field: lo..hi are the valid bounds; names optionally replace numbers
function field(lo, hi, opts = {}) {
  const k = rnd();
  const num = () => String(chance(0.04) ? pick([lo - 1, hi + 1, hi + 5, 0, 99, -1]) : int(lo, hi));
  const nm = (n) => (opts.names && chance(0.5) ? pick(opts.names) : n);
  const n1 = () => { const v = int(lo, hi); return opts.names ? nm(String(v)) : String(v); };
  if (k < 0.18) return "*";
  if (k < 0.22) return "?";
  if (k < 0.50) return chance(0.5) ? num() : n1();
  if (k < 0.60) { const a = int(lo, hi), b = int(a, hi); return `${a}-${b}`; }
  if (k < 0.66 && opts.names) { const a = int(lo, hi), b = int(a, hi); return `${nm(String(a))}-${nm(String(b))}`; }
  if (k < 0.74) return `*/${pick([1, 2, 3, 5, 7, 10, 15, 30, 59, 60, 61, 0])}`;
  if (k < 0.80) { const a = int(lo, hi), b = int(a, hi); return `${a}-${b}/${pick([1, 2, 3, 5, 0, 99])}`; }
  if (k < 0.90) return Array.from({ length: int(2, 4) }, () => (chance(0.3) && opts.names ? n1() : num())).join(",");
  if (k < 0.93) return `${int(lo, hi)}/${int(1, 9)}`;
  if (k < 0.95) return `/${int(1, 9)}`;
  if (k < 0.97) return pick(["", "x", "1-", "-5", "1--2", "5-1", ",", "1,", ",1", "*/", "*//2", "1.5", "1e1", "0x1", "٣"]);
  return String(hi + int(1, 3));
}

function domField() {
  const k = rnd();
  if (k < 0.12) return pick(["L", "LW", "lw", "l", "L,15", "15,L", "1,L", "LW,1"]);
  if (k < 0.2) return pick(["15W", "1W", "31W", "30W", "W", "1W,15W", "15W-20", "*/5W", "5-9W", "LW,15W", "0W", "32W"]);
  if (k < 0.24) return pick(["L-3", "3L", "L/2", "L-L"]);
  return field(1, 31);
}

function dowField() {
  const k = rnd();
  if (k < 0.12) return `${pick(DOW.concat([0, 1, 2, 3, 4, 5, 6, 7].map(String)))}#${pick(["1", "2", "3", "4", "5", "L", "l", "6", "0", "00", "2.5", "", "*"])}`;
  if (k < 0.18) return `${pick(["0", "1", "5", "7", "fri", "SUN", "mon", "8"])}${pick(["L", "l"])}`;
  if (k < 0.22) return "+" + field(0, 7, { names: DOW });
  if (k < 0.28) return pick(["mon-fri", "MON-FRI", "sat-sun", "sun-sat", "fri-mon", "0-7", "1-5", "5-1", "sun,sat", "6,0", "mon,wed,fri", "*/2", "1-7", "0-6/2", "*/7", "*/8"]);
  return field(0, 7, { names: DOW });
}

function depthFamily() {
  // patterns that search many years before they give up or find a match: the recursion depth of croner (and V8's stack) matters
  const months = [...Array(12).keys()].map((i) => i + 1).sort(() => rnd() - 0.5).slice(0, int(1, 7)).join(",");
  const dom = pick(["29", "30", "31", "30,31", "29-31", "31W", "L"]);
  const dow = pick(["*", "*", "*", "1", "mon-fri", "+1", "+7", "sat,sun"]);
  return `${pick(["0", "5", "*/20"])} ${pick(["0", "30"])} ${pick(["0", "12", "*/6"])} ${dom} ${months} ${dow}`;
}

function expr() {
  if (args.family === "depth") return depthFamily();
  const k = rnd();
  if (k < 0.07) {
    return pick(["* * * * * *", "0 * * * * *", "0 0 * * * *", "0 0 0 * * *", "0 */5 * * * *", "0 0 9 * * 1-5", "0 0 0 1 * *", "0 0 0 * * 0", "*/10 * * * * *", "0 0 12 * * ?", "0 30 9 15 * *", "0 0 0 29 2 *", "0 0 0 31 * *", "0 0 0 30 2 *", "0 0 2 * * *", "0 30 2 * * *", "0 30 1 * * *", "0 0 3 * * *", "0 0 0 1 1 *", "59 59 23 31 12 *", "0 0 0 29 2 1", "0 0 0 13 * 5"]);
  }
  if (k < 0.1) return pick(["0 0 9:00 * * *", "2026-02-13T15:30:00 * * * * *", "0 0 12:30 * * *", "1 1 1:1 1 1", "2027-01-01T00:00:00Z", "2030-06-01T12:00:00 a b c d", "Feb 13 2027 15:30 * * * *", "0 0 0:0 0 0 0", "9:00 * * * * *"]);
  const parts = [field(0, 59), field(0, 59), field(0, 23), domField(), field(1, 12, { names: MON }), dowField()];
  if (chance(0.03)) parts[int(0, 5)] = pick(["@", "@daily", "?", "*?", "**", "*-*", "L", "W", "#", "1#2", "-", "/", ",,"]);
  let sep = " ";
  if (chance(0.05)) sep = pick(["\t", "  ", " \u00a0", "\u2003", "\n"]);
  let s = parts.join(sep);
  if (chance(0.03)) s = " " + s + " ";
  if (chance(0.03)) s += pick([" *", " 2027", " 2030-2040"]);
  if (chance(0.02)) s = s.slice(0, int(0, s.length));
  return s;
}

const seen = new Set();
const exprs = [];
let guard = 0;
while (exprs.length < EXPRS && guard++ < EXPRS * 200) {
  const wantValid = exprs.length % 10 < 7;
  const e = expr();
  // the original's own gate: `expression.trim().split(/\s+/)` must have six fields
  if (e.trim().split(/\s+/).length !== 6) continue;
  if (seen.has(e)) continue;
  // 70% of the corpus is valid in croner (the interesting next-run cases); the rest is any shape, rejected or not
  if (wantValid) {
    try { new Cron(e); } catch { continue; } // valid syntax (a stack overflow in the search is kept in)
  }
  seen.add(e);
  exprs.push(e);
}

function refsFor(zone) {
  process.env.TZ = zone;
  const off = (t) => -new Date(t).getTimezoneOffset();
  const refs = [];
  const start = Date.UTC(2025, 0, 1), end = Date.UTC(2029, 0, 1);
  // transitions found by an hourly scan; references just before, at and after them
  let prev = off(start);
  const trans = [];
  for (let t = start; t < end; t += 3600000) {
    const o = off(t);
    if (o !== prev) trans.push(t);
    prev = o;
  }
  for (let i = 0; i < Math.min(REFS, 4) && trans.length; i++) {
    const t = pick(trans);
    refs.push(t - pick([1000, 1800000, 3599000, 3600000, 7200000, 500]));
  }
  refs.push(Date.UTC(2028, 1, 28, 12, 0, 0, 500)); // leap year
  refs.push(Date.UTC(2026, 11, 31, 23, 59, 59, 999));
  while (refs.length < REFS) refs.push(start + Math.floor(rnd() * (end - start)));
  return refs.slice(0, REFS);
}

const golden = { croner: "10.0.1", node: process.version, zones: ZONES, exprs, refs: [], results: [] };
for (const zone of ZONES) {
  const refs = refsFor(zone);
  golden.refs.push(refs);
  process.env.TZ = zone;
  const rows = exprs.map((e) => {
    let c;
    try {
      // as validateCronExpression does: the constructor with a function starts croner's timer loop, whose first search can throw
      new Cron(e, () => {}).stop();
      c = new Cron(e); // stopped jobs have no next run: the chains come from a fresh instance
    } catch (err) {
      return { e: err instanceof Error ? err.message : String(err), t: err && err.constructor ? err.constructor.name : "" };
    }
    // delta encoded chains: next run after the reference, then after that run
    return refs.map((r) => {
      const out = [];
      let prevMs = r;
      let prevDate = new Date(r);
      for (let i = 0; i < 2; i++) {
        let n;
        try {
          n = c.nextRun(prevDate);
        } catch (err) {
          out.push(String(err && err.message));
          break;
        }
        if (n === null) { out.push(null); break; }
        const ms = n.getTime();
        out.push(Number.isNaN(ms) ? "NaN" : ms - prevMs);
        prevMs = ms; prevDate = n;
      }
      return out;
    });
  });
  golden.results.push(rows);
}
process.env.TZ = "UTC";
writeFileSync(OUT, gzipSync(JSON.stringify(golden)));
const bad = golden.results[0].filter((r) => !Array.isArray(r)).length;
console.log(`exprs ${exprs.length} (${bad} rejected in UTC), zones ${ZONES.length}, refs ${REFS}, out ${OUT}`);
