// Generates date-golden.json.gz: what Node's V8 (Date.parse, new Date(y,m,d,...), local getters, toISOString) answers for a
// deterministic corpus, in each of several time zones. The Go port (extensions/schedule-prompt/internal/jsdate) is compared with it by
// TestDateGolden. Usage: node gen-date-golden.mjs   (writes the file under internal/jsdate/testdata; prints the V8 and tzdata versions)
import { gzipSync } from "node:zlib";
import { writeFileSync } from "node:fs";

const ZONES = ["UTC", "America/New_York", "Europe/London", "Australia/Lord_Howe", "America/Sao_Paulo", "Asia/Kolkata", "Pacific/Apia", "Asia/Kathmandu", "Europe/Dublin", "America/St_Johns", "Pacific/Chatham", "America/Santiago"];

function mulberry32(a) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rnd = mulberry32(20261021);
const pick = (a) => a[Math.floor(rnd() * a.length)];
const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));
const pad = (n, w) => String(n).padStart(w, "0");

const strings = new Set();
const add = (s) => strings.add(s);

// Fixed, hand-picked cases: formats an agent is likely to send, and the edges of V8's parser.
for (const s of [
  "2026-02-13T15:30:00Z", "2026-02-13T15:30:00", "2026-02-13T15:30:00.5", "2026-02-13T15:30:00.123456Z", "2026-02-13T15:30", "2026-02-13T15:30Z",
  "2026-02-13", "2026-02", "2026", "2026-02-13 15:30:00", "2026-02-13 15:30", "2026-02-13 15:30:00Z", "2026-02-13 15:30:00 UTC", "2026/02/13", "2026/02/13 15:30",
  "02/13/2026", "2/13/2026 3:30 PM", "13 Feb 2026 15:30 GMT", "Feb 13, 2026 15:30", "February 13, 2026", "Feb 13 2026 3:30pm", "Fri Feb 13 2026 15:30:00 GMT+0100",
  "Fri, 13 Feb 2026 15:30:00 GMT", "Fri Feb 13 2026 15:30:00 GMT-0500 (Eastern Standard Time)", "2026-02-13T15:30:00+01:00", "2026-02-13T15:30:00-0500",
  "2026-02-13T24:00:00", "2026-02-13T24:00:01", "2026-02-13T25:00:00", "2026-13-01", "2026-02-30", "2026-02-29", "2024-02-29T12:00:00Z", "+002026-02-13T00:00:00Z", "-000001-01-01T00:00:00Z",
  "-000000-01-01T00:00:00Z", "+275760-09-13T00:00:00.000Z", "+275760-09-13T00:00:00.001Z", "-271821-04-20T00:00:00.000Z", "-271821-04-19T23:59:59.999Z", "0000-01-01T00:00:00Z", "0099-01-01", "99-01-01",
  "tomorrow", "tomorrow 9am", "next friday", "in 5 minutes", "now", "", " ", "not-a-date", "+10s", "+5m", "5m", "0 * * * * *", "12:30", "3pm", "15:30:00", "Z", "T", "2026-02-13T",
  "2026-02-13T15", "2026-02-13T15:3", "2026-02-13t15:30:00z", "2026-02-13 T15:30", "Thu, 01 Jan 1970 00:00:00 GMT", "1970-01-01T00:00:00.000Z", "1 jan 2026", "jan 1", "1 jan", "2026 jan 1",
  "12/31/1999 11:59:59 PM", "12/31/99", "1/1/49", "1/1/50", "March 7 2021 02:30 AM", "Mar 14 2021 02:30:00", "Nov 7 2021 01:30:00", "2021-03-14T02:30:00", "2021-11-07T01:30:00",
  "2026-02-13T15:30:00.1234567890123Z", "2026-02-13T15:30:00,5Z", "(comment) 2026-02-13", "2026-02-13 (comment)", "2026-02-13 ()", "2026-02-13 (", "2026-02-13 )", "Feb 13 2026 UTC+1", "Feb 13 2026 UTC+01:00", "Feb 13 2026 UTC-8", "Feb 13 2026 UTC+12345",
  "Feb 13 2026 15:30 EST", "Feb 13 2026 15:30 EDT", "Feb 13 2026 15:30 PST", "Feb 13 2026 15:30 foo", "foo Feb 13 2026", "foo 2026", "2026 foo", "2026-02-13T15:30:00 foo", "100000000000000000", "1e3", "0",
  "2026-02-13T15:30:00\u00a0", "\u00a02026-02-13", "2026-02-13\u2028", "2026\u2028", "Feb\u00a013\u00a02026", "13.02.2026", "13-02-2026", "2026.02.13", "20260213", "20260213T153000Z",
]) add(s);

const months = ["Jan", "feb", "March", "APR", "may", "June", "Jul", "aug", "Sept", "oct", "nov", "december", "Janx", "Mayo", "Mar."];
const days = ["Mon", "tue", "Wednesday", "Thu", "FRI", "sat", "Sun"];
const zonesw = ["Z", "UTC", "GMT", "UT", "EST", "EDT", "CST", "CDT", "MST", "MDT", "PST", "PDT", "GMT+1", "GMT-0800", "UTC+0530", "UTC+05:30", "+01:00", "-0800", "+1", "-12", "+0", "z", "utc"];
const seps = [" ", "-", "/", ".", ", ", " at ", "T", " T", ""];

function isoLike() {
  const y = pick([2026, 2026, 2024, 1999, 2000, 1970, 1969, 1900, 275760, 99, 0, 12345, -1, 100, 9999, 10000]);
  const mo = int(0, 14), d = int(0, 33);
  let s = (y < 0 ? "-" + pad(-y, 6) : y > 9999 ? "+" + pad(y, 6) : pad(y, pick([4, 4, 4, 2, 3, 5])));
  const parts = int(0, 3);
  if (parts >= 1) s += "-" + pad(mo, pick([2, 2, 2, 1]));
  if (parts >= 2) s += "-" + pad(d, pick([2, 2, 2, 1]));
  const tpart = int(0, 5);
  if (tpart > 0) {
    s += pick(["T", "T", "T", " ", "t", "T "]) + pad(int(0, 26), pick([2, 2, 2, 1]));
    if (tpart > 1) s += ":" + pad(int(0, 61), pick([2, 2, 2, 1]));
    if (tpart > 2) s += ":" + pad(int(0, 61), pick([2, 2, 2, 1]));
    if (tpart > 3) s += pick([".", ".", ",", ":"]) + pad(int(0, 99999), int(1, 7));
    if (rnd() < 0.7) s += pick(["Z", "z", "+01:00", "-05:00", "+0100", "-0800", "+13:45", "+24:00", "+01", "-1", " UTC", " GMT+1", "+01:60", "+5:30"]);
  }
  return s;
}

function legacyLike() {
  const n = int(2, 8), out = [];
  for (let i = 0; i < n; i++) {
    const k = rnd();
    if (k < 0.18) out.push(pick(months));
    else if (k < 0.26) out.push(pick(days));
    else if (k < 0.36) out.push(String(int(1, 31)));
    else if (k < 0.44) out.push(String(pick([1999, 2026, 2030, 49, 50, 99, 0, 100, 12345])));
    else if (k < 0.58) out.push(pad(int(0, 24), int(1, 2)) + ":" + pad(int(0, 61), int(1, 2)) + (rnd() < 0.5 ? ":" + pad(int(0, 61), int(1, 2)) : "") + (rnd() < 0.25 ? "." + pad(int(0, 9999), int(1, 4)) : ""));
    else if (k < 0.66) out.push(pick(["AM", "PM", "am", "pm", "a.m."]));
    else if (k < 0.78) out.push(pick(zonesw));
    else if (k < 0.84) out.push(pick(["(PST)", "(Eastern Standard Time)", "()", "(a(b)c)", "(unterminated"]));
    else if (k < 0.92) out.push(pick(["2/13/2026", "13/2/2026", "2026/2/13", "1-2-3", "2026-2-13", "13.2.2026"]));
    else out.push(pick([",", ".", "-", "+", ")", ":", "::", "foo", "of", "the", "\u00a0", "\t"]));
    out.push(pick([" ", " ", " ", ", ", "", "-", " "]));
  }
  return out.join("").slice(0, 60);
}

function mutate(s) {
  const i = int(0, s.length);
  const k = rnd();
  if (k < 0.3) return s.slice(0, i) + s.slice(i + 1);
  if (k < 0.6) return s.slice(0, i) + pick([" ", ":", "-", "+", ".", "Z", "T", "0", "9", "(", ")", "/", ","]) + s.slice(i);
  return s.slice(0, i) + pick(["", "x", "\u00a0", "\n", "\u2029"]) + s.slice(i + 1);
}

for (let i = 0; i < 9000; i++) add(isoLike());
for (let i = 0; i < 9000; i++) add(legacyLike());
for (let i = 0; i < 4000; i++) { const base = pick([...strings]); add(mutate(base)); }
const list = [...strings];

const out = { v8: process.versions.v8, node: process.version, tz: process.versions.tz, zones: ZONES, strings: list, parse: [], local: [] };

function ms(x) { return Number.isNaN(x) ? null : x; }

for (const zone of ZONES) {
  process.env.TZ = zone;
  out.parse.push(list.map((s) => ms(Date.parse(s))));
  // Local construction and getters around every transition of 2019..2036 (hourly offset scan), local time +-3h in 15 minute steps.
  const rows = [];
  const start = Date.UTC(2019, 0, 1), end = Date.UTC(2036, 0, 1);
  const off = (t) => -new Date(t).getTimezoneOffset();
  let prev = off(start);
  for (let t = start; t < end; t += 3600000) {
    const o = off(t);
    if (o !== prev) {
      for (let dt = -3 * 3600000; dt <= 3 * 3600000; dt += 900000) {
        const inst = t + dt;
        const d = new Date(inst);
        // the local wall clock at inst, rebuilt through the constructor (exercises skipped and repeated times) and as getters
        rows.push([inst, d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), d.getSeconds(), d.getDay(),
          new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), d.getSeconds(), 0).getTime(),
          // local time shifted by one hour, which lands in a gap or an overlap near the transition
          ms(new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours() + 1, d.getMinutes(), d.getSeconds(), 0).getTime()),
          ms(new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours() - 1, d.getMinutes(), d.getSeconds(), 0).getTime()),
          ms(new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1, d.getHours(), d.getMinutes(), d.getSeconds(), 0).getTime())]);
      }
    }
    prev = o;
  }
  // plain instants spread over the range: getters and ISO text
  for (let i = 0; i < 600; i++) {
    const inst = start + Math.floor(rnd() * (end - start));
    const d = new Date(inst);
    rows.push([inst, d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), d.getSeconds(), d.getDay(),
      new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), d.getSeconds(), 0).getTime()]);
  }
  out.local.push(rows);
}
process.env.TZ = "UTC";
out.iso = [];
for (const t of [0, 1, -1, 86399999, 1e12, -1e12, 8.64e15, -8.64e15, 253402300799999, 253402300800000, -62198755200000, -62167219200001, -62167219200000]) out.iso.push([t, new Date(t).toISOString()]);
writeFileSync(new URL("../extensions/schedule-prompt/internal/jsdate/testdata/date-golden.json.gz", import.meta.url), gzipSync(JSON.stringify(out)));
console.log(`strings ${list.length}, zones ${ZONES.length}, local rows ${out.local.map((r) => r.length).join(",")}, v8 ${process.versions.v8}, tz ${process.versions.tz}`);
