// Golden vectors for the pure functions of client*.go, produced by the real langsmith 0.10.5 code:
// estimateSerializedSize, AutoBatchQueue (push/pop), mergeRuntimeEnvIntoRun, parseFloat/parseInt (the engine's).
//   node client-units.mjs   writes testdata/client_units.json
import { load, writeVectors } from "./wire-common.mjs";

const { estimateSerializedSize } = await load("utils/fast-safe-stringify/index.js");
const { AutoBatchQueue, mergeRuntimeEnvIntoRun } = await load("client.js");

const revive = (v) => {
  if (Array.isArray(v)) return v.map(revive);
  if (v && typeof v === "object") {
    if (v.$undef) return undefined;
    const out = {};
    for (const [k, x] of Object.entries(v)) out[k] = revive(x);
    return out;
  }
  return v;
};

// ---- estimateSerializedSize ----
const U = { $undef: true };
const estimateCases = [
  null, true, false, 0, -0, 1, -1, 12345, 1.5, -2.25, 1e21, 1e-7, 0.1, 123456789012345680000, 5e-324, 1.7976931348623157e308,
  "", "abc", "é", "日本語", "😀", "a\u0000b", "quote\"and\\slash", "\ud800", "x\udc00y", "line\nbreak",
  [], {}, [1, 2, 3], [U, null, "x"], { a: U, b: 1 }, { a: { b: { c: [1, { d: "e" }] } } },
  { id: "0195b2a0-7e41-7c3d-9f00-000000000001", name: "run", extra: { metadata: { a: "é😀", n: 3 }, runtime: { library: "langsmith" } }, tags: ["a", "b"], inputs: { messages: [{ role: "user", content: "hello" }] }, events: [], child_runs: [], outputs: U },
  { "é": 1, "😀": 2, "": 3 },
  [[], [[]], [{}]],
  { "1": "a", "0": "b", z: 1 },
];

// ---- AutoBatchQueue ----
function pad(n) { return "x".repeat(n); }
const queueCases = [
  { max: 1000, items: [10, 10, 10, 10], pops: [[100000, 100], [1, 100]] },
  { max: 100000, items: [10, 20, 30, 40, 50], pops: [[150, 100], [60, 2], [1, 100], [1000, 100]] },
  { max: 300, items: [10, 10, 10, 200, 10, 10], pops: [[400, 100]] },
  { max: 100000, items: [500, 10, 10], pops: [[100, 100], [100, 100], [100, 100]] },
  { max: 100000, items: [10, 10, 10], pops: [[0, 100], [100, 0], [100000, 2.5], [100000, 100]] },
  { max: 10, items: [5], pops: [[100000, 100]] },
];

async function runQueue(c) {
  const q = new AutoBatchQueue(c.max);
  const warns = [];
  const realWarn = console.warn;
  console.warn = (m) => warns.push(m);
  const pushed = [];
  c.items.forEach((n, i) => {
    const before = q.items.length;
    const item = { id: `id-${i}`, pad: pad(n) };
    q.push({ action: "create", item });
    pushed.push({ id: item.id, accepted: q.items.length > before, size: q.items[q.items.length - 1]?.size, sizeBytes: q.sizeBytes });
  });
  console.warn = realWarn;
  const pops = [];
  for (const [bytes, n] of c.pops) {
    try {
      const [batch] = q.pop({ upToSizeBytes: bytes, upToSize: n });
      pops.push({ ids: batch.map((b) => b.item.id), sizeBytes: q.sizeBytes, left: q.items.length });
    } catch (e) {
      pops.push({ error: e.message });
    }
  }
  return { pushed, warns, pops };
}

// ---- mergeRuntimeEnvIntoRun ----
const mergeCases = [
  { run: { id: "r1" }, env: {}, omit: false },
  { run: { id: "r1", extra: { metadata: { a: 1 } } }, env: { LANGSMITH_X: "1", LANGCHAIN_Y: "2" }, omit: false },
  { run: { id: "r1", extra: { runtime: { library: "custom", extra: 1 }, metadata: { LANGSMITH_X: "mine" } } }, env: { LANGSMITH_X: "env" }, omit: false },
  { run: { id: "r1" }, env: { revision_id: "rev-env" }, omit: false },
  { run: { id: "r1", revision_id: "rev-run" }, env: { revision_id: "rev-env" }, omit: false },
  { run: { id: "r1", revision_id: "rev-run" }, env: {}, omit: false },
  { run: { id: "r1", revision_id: U }, env: { revision_id: "rev-env" }, omit: false },
  { run: { id: "r1", revision_id: null }, env: { revision_id: "rev-env" }, omit: false },
  { run: { id: "r1", revision_id: "" }, env: {}, omit: false },
  { run: { id: "r1", extra: { metadata: { a: 1 } } }, env: { LANGSMITH_X: "1" }, omit: false, rate: 0.25 },
  { run: { id: "r1", extra: { metadata: U, other: 5 } }, env: {}, omit: false, rate: 1 },
  { run: { id: "r1", extra: null }, env: { LANGSMITH_X: "1" }, omit: false },
  { run: { id: "r1", extra: { other: [1, 2], metadata: { ls_tracing_sample_rate: 9 } } }, env: {}, omit: false, rate: 0.5 },
  { run: { id: "r1", extra: { metadata: { a: 1 } } }, env: { LANGSMITH_X: "1" }, omit: true },
];

const canonRuntime = (s) =>
  s.replaceAll('"sdk":"langsmith-js"', '"sdk":"<sdk>"').replaceAll('"sdk_version":"0.10.5"', '"sdk_version":"<sdkv>"').replaceAll('"runtime":"node"', '"runtime":"<rt>"');

// ---- number parsing ----
const parseStrings = [
  "", " ", "0", "1", "-1", "+1", "1.5", ".5", "-.5", "5.", "1e3", "1E3", "1e+3", "1e-3", "1e", "1e+", "e5", "abc", "1abc", "  12  ", "\n\t 7", "\u00a07", "\ufeff7", "\u20287",
  "Infinity", "-Infinity", "+Infinity", "infinity", "Infinityx", "NaN", "0x10", "0b1", "1_000", "1,5", "0.5.5", "--1", "+-1", "1e400", "-1e400", "1e-400", "00012", "0.0000001", "123456789012345678901234567890",
  "0.5", "1.0", "-0", "2", "1.5", "1.51", "3 ", "1 2", "0.", ".", "-", "+", "٣", "１２", "1.5e2x", "4e2.5", "９",
];

// trimQuotes is private in the SDK (client.js:102-110); the expression is copied verbatim.
const trimQuotes = (str) => str.trim().replace(/^"(.*)"$/, "$1").replace(/^'(.*)'$/, "$1");
const trimStrings = ["abc", '"abc"', "'abc'", `"'abc'"`, `'"abc"'`, '  "x"  ', '"', '""', '"a"b"', '"a\nb"', '"a\u2028b"', '\u00a0"x"\u00a0', "' '", '"abc', 'abc"', "'", "''", '"""', " ' x ' ", '"é😀"', "\ufeff'bom'", '"\r"', " ", "", "key-with-'quotes'", `"it's"`];

const out = {
  trim: trimStrings.map((s) => ({ s, out: trimQuotes(s) })),
  estimate: estimateCases.map((v) => ({ value: v, size: estimateSerializedSize(revive(v)).size })),
  queue: await Promise.all(queueCases.map(async (c) => ({ ...c, result: await runQueue(c) }))),
  merge: mergeCases.map((c) => {
    const run = revive(structuredClone(c.run));
    const env = { ...c.env };
    const res = mergeRuntimeEnvIntoRun(run, env, c.omit, c.rate);
    return { ...c, result: canonRuntime(JSON.stringify(res)) };
  }),
  parse: parseStrings.map((s) => {
    const f = parseFloat(s);
    const i = parseInt(s, 10);
    const enc = (n) => (Number.isNaN(n) ? "NaN" : n === Infinity ? "Infinity" : n === -Infinity ? "-Infinity" : Object.is(n, -0) ? "-0" : n);
    return { s, float: enc(f), int: enc(i) };
  }),
};
writeVectors("client_units.json", out);
