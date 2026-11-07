// Golden vectors for caller.go: runs the real langsmith 0.10.5 AsyncCaller / raiseForStatus against a loopback
// server and records what it does (attempts, backoff delays, outcome). Usage:
//   node caller-differential.mjs <out.json>
// Env: LS_SDK_DIST (default /home/kinsy/ls-work/oracle-run/node_modules/langsmith/dist).
import http from "node:http";
import { writeFileSync } from "node:fs";

const SDK = process.env.LS_SDK_DIST ?? "/home/kinsy/ls-work/oracle-run/node_modules/langsmith/dist";
const { AsyncCaller } = await import(`${SDK}/utils/async_caller.js`);
const { raiseForStatus } = await import(`${SDK}/utils/error.js`);

const b64 = (s) => Buffer.from(s, "latin1").toString("base64");
const st = (status, body = "", reason = http.STATUS_CODES[status], extra) => ({ kind: "status", status, reason, body: b64(body), ...extra });
const u8 = (s) => b64(Buffer.from(s, "utf8").toString("latin1"));

// A step is what the server does for the n-th request (the last step repeats).
async function serve(steps) {
  let n = 0;
  const sockets = new Set();
  const server = http.createServer((req, res) => {
    const step = steps[Math.min(n, steps.length - 1)];
    n++;
    req.resume();
    if (step.kind === "reset") return req.socket.destroy();
    if (step.kind === "hang") return;
    res.statusMessage = step.reason;
    res.writeHead(step.status, { "content-length": Buffer.from(step.body, "base64").length });
    res.end(Buffer.from(step.body, "base64"));
  });
  server.on("connection", (s) => {
    sockets.add(s);
    s.on("close", () => sockets.delete(s));
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    requests: () => n,
    close: async () => {
      for (const s of sockets) s.destroy();
      await new Promise((r) => server.close(r));
    },
  };
}

const realRandom = Math.random;
const realSetTimeout = globalThis.setTimeout;

async function runRetry(sc) {
  const srv = await serve(sc.steps);
  const delays = [];
  let ji = 0;
  Math.random = () => sc.jitter[ji++ % sc.jitter.length];
  // The backoff sleeps (>= 1000 ms, set from p-retry) are recorded and fired at once.
  globalThis.setTimeout = (fn, ms, ...a) => {
    if (typeof ms === "number" && ms >= 1000 && new Error().stack.includes("p-retry")) {
      delays.push(ms);
      return realSetTimeout(fn, 0, ...a);
    }
    return realSetTimeout(fn, ms, ...a);
  };
  const params = {};
  if (sc.maxRetries !== null) params.maxRetries = sc.maxRetries;
  const caller = new AsyncCaller(params);
  let expect;
  try {
    const res = await caller.call(async () => {
      const r = await fetch(`${srv.url}/x`, { signal: AbortSignal.timeout(sc.timeoutMs ?? 5000) });
      await raiseForStatus(r, "fetch /x", true);
      return r;
    });
    expect = { outcome: "ok", status: res.status };
  } catch (e) {
    let kind = "other";
    if (e.name === "TimeoutError") kind = "timeout";
    else if (e.name === "TypeError") kind = "network";
    else if (e.status !== undefined) kind = "status";
    expect = { outcome: "error", kind, name: e.name, status: e.status ?? null, message: kind === "status" ? e.message : "" };
  } finally {
    Math.random = realRandom;
    globalThis.setTimeout = realSetTimeout;
  }
  expect.attempts = srv.requests();
  expect.delaysMs = delays;
  await srv.close();
  return { name: sc.name, maxRetries: sc.maxRetries, jitter: sc.jitter, timeoutMs: sc.timeoutMs ?? 5000, steps: sc.steps, expect };
}

async function runRaise(sc) {
  const srv = await serve([sc.step]);
  const r = await fetch(`${srv.url}/x`);
  let expect;
  try {
    await raiseForStatus(r, sc.op ?? "fetch /x", sc.consume ?? false);
    expect = { outcome: "ok" };
  } catch (e) {
    expect = { outcome: "error", name: e.name, status: e.status ?? null, message: e.message };
  }
  await srv.close();
  return { name: sc.name, op: sc.op ?? "fetch /x", consume: sc.consume ?? false, step: sc.step, expect };
}

const J = [0, 0.25, 0.5, 0.999];
const retry = [];
const add = (name, steps, o = {}) => retry.push({ name, steps, maxRetries: 4, jitter: J, ...o });
add("ok first try", [st(200, "ok")]);
add("created", [st(201, "")]);
add("no content", [st(204)]);
for (const s of [408, 425, 429, 500, 502, 503, 504]) add(`retryable ${s} then ok`, [st(s, `e${s}`), st(200)]);
for (const s of [400, 401, 403, 404, 409, 422, 501, 505, 302, 418, 451]) add(`terminal ${s}`, [st(s, `e${s}`), st(200)]);
add("500 forever (4 retries)", [st(500, "boom")]);
add("503 forever, jitter 0", [st(503)], { jitter: [0] });
add("503 forever, jitter near 1", [st(503)], { jitter: [0.9999999] });
add("500 forever, maxRetries 0", [st(500, "boom")], { maxRetries: 0 });
add("500 forever, maxRetries 1", [st(500, "boom")], { maxRetries: 1 });
add("500 forever, default retries (6)", [st(500, "boom")], { maxRetries: null });
add("429 429 ok", [st(429), st(429), st(200)]);
add("500 then 400 stops", [st(500), st(400, "bad")]);
add("404 then ok", [st(404, "nope"), st(200)]);
add("409 then ok", [st(409, "dup"), st(200)]);
add("reset then ok", [{ kind: "reset" }, st(200)]);
add("reset forever", [{ kind: "reset" }]);
add("reset, 500, 200", [{ kind: "reset" }, st(500), st(200)]);
add("hang times out (no retry)", [{ kind: "hang" }], { timeoutMs: 150 });
add("403 org scoped", [st(403, JSON.stringify({ error: "org_scoped_key_requires_workspace" })), st(200)]);
add("500 unicode body", [st(500, "caf\u00e9 \u{1F600}")].map((s) => ({ ...s, body: u8("caf\u00e9 \u{1F600}") })));

const raise = [];
const addR = (name, step, o = {}) => raise.push({ name, step, ...o });
addR("200 not consumed", st(200, "fine"));
addR("200 consumed", st(200, "fine"), { consume: true });
addR("204 consumed", st(204), { consume: true });
for (const s of [400, 401, 402, 404, 409, 410, 422, 429, 500, 502, 503]) addR(`status ${s}`, st(s, `body ${s}`));
addR("418 reason", st(418, "tea", "I'm a teapot"));
addR("500 empty body", st(500, ""));
addR("500 custom reason", st(500, "x", "Totally Broken"));
addR("403 org scoped", st(403, '{"error":"org_scoped_key_requires_workspace"}'));
addR("403 org scoped with spaces", st(403, ' { "error" : "org_scoped_key_requires_workspace" , "x": 1 } '));
addR("403 org scoped escaped", st(403, '{"error":"org_scoped_key_requires_\\u0077orkspace"}'));
addR("403 org scoped uppercase key", st(403, '{"ERROR":"org_scoped_key_requires_workspace"}'));
addR("403 other error", st(403, '{"error":"forbidden"}'));
addR("403 error not string", st(403, '{"error":5}'));
addR("403 non json", st(403, "<html>denied</html>"));
addR("403 array", st(403, "[]"));
addR("403 null", st(403, "null"));
addR("403 string", st(403, '"org_scoped_key_requires_workspace"'));
addR("403 empty", st(403, ""));
addR("403 duplicate key", st(403, '{"error":"x","error":"org_scoped_key_requires_workspace"}'));
addR("404 org scoped is not rewritten", st(404, '{"error":"org_scoped_key_requires_workspace"}'));
addR("op with status text", st(500, "x"), { op: "Failed to send multipart request" });
// Body decoding: response.text() is a WHATWG UTF-8 decode.
const raw = (name, s) => addR(`utf8 ${name}`, st(500, s));
raw("bom", "\xef\xbb\xbfhello");
raw("bom only", "\xef\xbb\xbf");
raw("double bom", "\xef\xbb\xbf\xef\xbb\xbfx");
raw("triple bom", "\xef\xbb\xbf\xef\xbb\xbf\xef\xbb\xbfx");
raw("two boms only", "\xef\xbb\xbf\xef\xbb\xbf");
raw("bom text bom", "\xef\xbb\xbfa\xef\xbb\xbf");
raw("truncated bom", "\xef\xbb");
raw("lone continuation", "a\x80b");
raw("invalid lead ff", "a\xffb");
raw("overlong c0 80", "a\xc0\x80b");
raw("truncated 2-byte at end", "a\xc3");
raw("truncated 3-byte at end", "a\xe2\x82");
raw("truncated 4-byte at end", "a\xf0\x9f\x98");
raw("truncated 3-byte then ascii", "a\xe2\x82z");
raw("surrogate ed a0 80", "a\xed\xa0\x80b");
raw("e0 80 overlong", "a\xe0\x80\x80b");
raw("f0 80 overlong", "a\xf0\x80\x80\x80b");
raw("f4 90 above max", "a\xf4\x90\x80\x80b");
raw("f5 invalid", "a\xf5\x80\x80\x80b");
raw("bad continuation then valid", "\xe2\x28\xa1");
raw("valid emoji", "\xf0\x9f\x98\x80");
raw("two truncated in a row", "\xe2\x82\xe2\x82");
raw("f1 then 2 ok then bad", "\xf1\x80\x80\x28");

// p-retry calculateDelay (utils/p-retry/index.js:46-51) as AsyncCaller configures it (randomize, minTimeout 1000,
// factor 2, maxTimeout Infinity), for edge and pseudo-random jitters. The function is not exported by the SDK, so
// the vectors re-run the recorded setTimeout through a real pRetry: one attempt per retriesConsumed value.
const { default: pRetry } = await import(`${SDK}/utils/p-retry/index.js`);
async function jsDelay(retriesConsumed, jitter) {
  Math.random = () => jitter;
  let got;
  globalThis.setTimeout = (fn, ms, ...a) => {
    if (new Error().stack.includes("p-retry")) got = ms;
    return realSetTimeout(fn, 0, ...a);
  };
  let n = 0;
  try {
    await pRetry(async () => { if (n++ <= retriesConsumed) throw new Error("x"); }, { retries: retriesConsumed + 1, randomize: true });
  } finally {
    Math.random = realRandom;
    globalThis.setTimeout = realSetTimeout;
  }
  return got;
}
const delays = [];
let seed = 12345;
const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
const edge = [0, 0.0005, 0.0004999, 0.5, 0.25, 0.75, 0.999, 0.9999999, 0.1234567, 1e-9];
for (let n = 0; n < 12; n++) for (const j of edge) delays.push([n, j]);
for (let i = 0; i < 300; i++) delays.push([Math.floor(rnd() * 14), rnd()]);
const delayVectors = [];
for (const [n, j] of delays) delayVectors.push({ retriesConsumed: n, jitter: j, ms: await jsDelay(n, j) });

const out = { sdk: "langsmith 0.10.5", retry: [], raise: [], delay: delayVectors };
for (const sc of retry) out.retry.push(await runRetry(sc));
for (const sc of raise) out.raise.push(await runRaise(sc));
writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + "\n");
console.log(`wrote ${out.retry.length} retry + ${out.raise.length} raise scenarios`);
