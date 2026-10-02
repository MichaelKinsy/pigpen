// Records the behaviour of the official SDK (@typesafe-ai/sdk 0.6.0, built from the pinned
// commit) for every scenario in scenarios.json against a local scripted HTTP server.
// Usage: node record_js.mjs <path to dist/index.mjs of the pinned typesafe-sdk-js build> > golden.json
// No network beyond 127.0.0.1; the API key is a fixed dummy value.
import http from "node:http";
import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { pathToFileURL } from "node:url";

const dist = process.argv[2];
if (!dist) throw new Error("usage: node record_js.mjs <dist/index.mjs>");
const sdk = await import(pathToFileURL(dist).href);
const scenarios = JSON.parse(readFileSync(new URL("./scenarios.json", import.meta.url)));
const KEEP = (n) => n.startsWith("x-") || ["authorization", "accept", "content-type", "content-length"].includes(n);

// Canonical form shared with the Go test: sorted keys, compact, non-ASCII as \\uXXXX.
const esc = (str) => JSON.stringify(str).replace(/[\u007f-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`);
const canon = (v) =>
  v === null || typeof v !== "object"
    ? typeof v === "string" ? esc(v) : JSON.stringify(v)
    : Array.isArray(v) ? `[${v.map(canon).join(",")}]`
    : `{${Object.keys(v).sort().map((k) => `${esc(k)}:${canon(v[k])}`).join(",")}}`;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function withServer(steps, fn) {
  const requests = [];
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", async () => {
      const headers = {};
      for (const [k, v] of Object.entries(req.headers)) if (KEEP(k)) headers[k] = v;
      requests.push({ method: req.method, path: req.url, headers, body: Buffer.concat(chunks).toString("utf8") });
      const step = steps[requests.length - 1] ?? { status: 599, bodyText: "unscripted request" };
      if (step.delayMs) await sleep(step.delayMs);
      if (step.action === "destroy") return req.socket.destroy();
      res.statusCode = step.status;
      for (const [k, v] of Object.entries(step.headers ?? {})) res.setHeader(k, v);
      let body = "";
      if (step.bodyText !== undefined) body = step.bodyText;
      else if (step.body !== undefined) {
        body = JSON.stringify(step.body);
        res.setHeader("content-type", "application/json");
      }
      res.end(body);
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  try {
    return await fn(`http://127.0.0.1:${server.address().port}`, requests);
  } finally {
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  }
}

const out = {};
for (const sc of scenarios) {
  out[sc.name] = await withServer(sc.responses, async (base, requests) => {
    const c = sc.config;
    const client = new sdk.TypeSafeClient({
      apiKey: "test-key-not-real",
      baseURL: base + (c.baseURLSuffix ?? ""),
      logLevel: "off",
      retry: c.retry,
      timeout: c.timeoutMs,
      defaultHeaders: c.defaultHeaders,
    });
    const o = sc.call.options ?? {};
    const opts = { headers: o.headers, retry: o.retry, timeout: o.timeoutMs };
    let outcome;
    try {
      const r = sc.call.kind === "modelsList" ? await client.models.list(opts) : await client.systemOne(sc.call.request, opts);
      outcome = { ok: true, result: r === undefined ? null : JSON.parse(JSON.stringify(r)) };
    } catch (e) {
      outcome = { ok: false, class: e?.constructor?.name, message: String(e?.message) };
      if (e instanceof sdk.APIError) {
        outcome.status = e.status;
        outcome.requestId = e.requestID ?? e.requestId ?? null;
        outcome.body = e.body === undefined ? null : e.body;
      }
    }
    return { requests, outcome };
  });
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");

// WorkflowEvals calls: the request body the official SDK sends for each real question set.
// Usage: node record_js.mjs <dist> <cases.json> <out.json>  (cases.json: workflowevals.json or a local full file)
if (process.argv[3]) {
  const cases = JSON.parse(readFileSync(process.argv[3]));
  const bodies = [];
  for (const c of cases) {
    const body = await withServer([{ status: 200, body: { model: "m", answers: {}, usage: { input_tokens: 0, output_tokens: 0 } } }], async (base, requests) => {
      const client = new sdk.TypeSafeClient({ apiKey: "test-key-not-real", baseURL: base, logLevel: "off", retry: { maxRetries: 0 } });
      await client.systemOne({ state: c.state, questions: c.questions, model: "m" });
      return requests[0].body;
    });
    const parsed = JSON.parse(body);
    bodies.push({ sha256: createHash("sha256").update(canon(parsed)).digest("hex"), order: Object.keys(parsed).join(",") });
  }
  writeFileSync(process.argv[4], JSON.stringify(bodies, null, 1) + "\n");
}
