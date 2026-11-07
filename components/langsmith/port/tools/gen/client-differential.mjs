// Differential vectors for client*.go: drives the REAL langsmith 0.10.5 Client (client.js) against a loopback
// server with the scenarios below and records what the server received, canonicalised only where the wire identity
// differs by decision (User-Agent, runtime/sdk/sdk_version values, multipart boundary, temp paths, timestamps in
// file names). The Go test (client_golden_test.go) replays the same scenarios against the Go client and compares.
//   node client-differential.mjs            writes testdata/client_scenarios.json and testdata/client_golden.json
// Env: LS_SDK_DIST (default /home/kinsy/ls-work/oracle-run/node_modules/langsmith/dist).
import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import util from "node:util";
import { DIST, load, testdata, writeVectors } from "./wire-common.mjs";

const { Client } = await load("client.js");

// ---------- scenario construction ----------

const uuid = (n) => `0195b2a0-7e41-7c3d-9f00-${n.toString(16).padStart(12, "0")}`;
const UNDEF = { $undef: true };
const T0 = (ms) => `2024-03-01T10:00:${String(Math.floor(ms / 1000)).padStart(2, "0")}.${String(ms % 1000).padStart(3, "0")}000`;
const dotted = (ms, id) => `20240301T1000${String(Math.floor(ms / 1000)).padStart(2, "0")}${String(ms % 1000).padStart(3, "0")}000Z${id}`;

// A run tree's `_convertToCreate` payload (run_trees.js:479-520).
function createPayload(n, o = {}) {
  const id = uuid(n);
  const trace = o.parent ? o.parent.trace_id : id;
  const dot = o.parent ? `${o.parent.dotted_order}.${dotted(n * 10, id)}` : dotted(n * 10, id);
  return {
    id,
    name: o.name ?? `run-${n}`,
    start_time: T0(n * 10),
    end_time: o.end_time ?? UNDEF,
    run_type: o.run_type ?? "chain",
    reference_example_id: UNDEF,
    extra: o.extra ?? { metadata: { turn: n, note: "ü😀" }, runtime: { library: "langsmith" } },
    serialized: o.serialized ?? UNDEF,
    error: o.error ?? UNDEF,
    inputs: o.inputs ?? { q: `question ${n}`, nested: { a: [1, 2.5, null, true] } },
    outputs: o.outputs ?? UNDEF,
    session_name: o.session ?? "proj",
    child_runs: [],
    parent_run_id: o.parent ? o.parent.id : UNDEF,
    trace_id: trace,
    dotted_order: dot,
    tags: o.tags ?? ["t1", "t2"],
    attachments: o.attachments ?? UNDEF,
    events: o.events ?? UNDEF,
  };
}
// RunTree.patchRun's runUpdate (run_trees.js:803-820): no inputs (excluded on patch), no id.
function updatePayload(c, o = {}) {
  const u = {
    name: c.name,
    run_type: c.run_type,
    start_time: c.start_time,
    end_time: o.end_time ?? T0(5000),
    error: o.error ?? UNDEF,
    outputs: o.outputs ?? { answer: "42" },
    parent_run_id: c.parent_run_id,
    reference_example_id: UNDEF,
    extra: o.extra ?? { metadata: { turn: 1, done: true } },
    events: o.events ?? UNDEF,
    dotted_order: c.dotted_order,
    trace_id: c.trace_id,
    tags: c.tags,
    attachments: UNDEF,
    session_name: c.session_name,
  };
  if (o.inputs) u.inputs = o.inputs;
  return u;
}
const create = (run, dest) => ({ op: "create", run, ...(dest ? { dest } : {}) });
const update = (run, id, dest) => ({ op: "update", id, run, ...(dest ? { dest } : {}) });
const awaitOp = { op: "await" };
const wait = (ms) => ({ op: "wait", ms });

const INFO = (over = {}) => ({
  version: "mockls",
  batch_ingest_config: { use_multipart_endpoint: true, size_limit: 100, size_limit_bytes: 20971520, ...(over.batch ?? {}) },
  instance_flags: { gzip_body_enabled: false, ...(over.flags ?? {}) },
});

function buildScenarios() {
  const S = [];
  const add = (s) => S.push({ options: { apiKey: "key-1", apiUrl: "{base}" }, env: {}, server: {}, ops: [], ...s });

  // 1 direct (autoBatchTracing false): POST /runs and PATCH /runs/<id>.
  {
    const c = createPayload(1);
    add({ name: "direct-create-update", options: { apiKey: "key-1", apiUrl: "{base}/", autoBatchTracing: false, workspaceId: "ws-1" },
      ops: [create(c), update(updatePayload(c), c.id), create(createPayload(2), { apiKey: "replica-key", apiUrl: "{base}", workspaceId: "ws-r" }), awaitOp] });
  }
  add({ name: "direct-hide-flags", env: { LANGSMITH_HIDE_INPUTS: "true", LANGCHAIN_HIDE_OUTPUTS: "true", LANGSMITH_HIDE_METADATA: "true" },
    options: { apiKey: "key-1", apiUrl: "{base}", autoBatchTracing: false },
    ops: (() => { const c = createPayload(1); return [create(c), update(updatePayload(c), c.id)]; })() });
  add({ name: "direct-errors", options: { apiKey: "key-1", apiUrl: "{base}", autoBatchTracing: false }, fastRetry: true,
    server: { runs: [{ status: 400, body: "bad request body" }, { status: 500 }, { status: 200 }], patch: [{ status: 403, json: { error: "org_scoped_key_requires_workspace" } }] },
    ops: (() => { const a = createPayload(1), b = createPayload(2); return [create(a), create(b), update(updatePayload(a), a.id)]; })(), errors: true });
  add({ name: "direct-no-trace-id", options: { apiKey: "key-1", apiUrl: "{base}" },
    ops: [create({ id: uuid(9), name: "x", start_time: T0(90), run_type: "tool", inputs: { a: 1 }, project_name: "pp", session_name: "ignored-order" }), update({ outputs: { b: 2 }, extra: { metadata: { m: 1 } } }, uuid(9)), awaitOp] });

  // 2 batched: create+update of one run merge into one `post`; standalone patch for another.
  {
    const a = createPayload(1), b = createPayload(2, { parent: a }), c = createPayload(3);
    add({ name: "multipart-merge", ops: [create(a), create(b), update(updatePayload(a), a.id), update(updatePayload(c), c.id), awaitOp] });
  }
  {
    const a = createPayload(1);
    add({ name: "multipart-update-only-and-events", ops: [update(updatePayload(a, { events: [{ name: "new_token", time: "t", kwargs: { token: "x" } }, { name: "other", kwargs: { k: 1 } }] }), a.id), awaitOp] });
  }
  {
    const a = createPayload(1, { attachments: { "file1": ["text/plain", { $bytes: Buffer.from("hello\n").toString("base64") }], "img": { mimeType: "image/png", data: { $bytes: "iVBORw0K" } }, "bad.name": ["text/plain", { $bytes: "AAEC" }] } });
    add({ name: "multipart-attachments", ops: [create(a), update(updatePayload(a), a.id), awaitOp] });
  }
  // 3 timer: every op resets the 250 ms (here 120 ms) aggregation timer.
  {
    const runs = [1, 2, 3, 4].map((n) => createPayload(n));
    add({ name: "timer-reset-single-batch", aggregationDelayMs: 150,
      ops: [create(runs[0]), wait(70), create(runs[1]), wait(70), create(runs[2]), wait(70), create(runs[3]), awaitOp] });
    add({ name: "timer-two-batches", aggregationDelayMs: 100,
      ops: [create(runs[0]), create(runs[1]), wait(450), create(runs[2]), create(runs[3]), awaitOp] });
  }
  // 4 limits
  {
    const runs = [1, 2, 3, 4, 5].map((n) => createPayload(n));
    add({ name: "limit-count-from-info", unordered: true, server: { info: [{ status: 200, json: INFO({ batch: { size_limit: 2 } }) }] }, ops: [...runs.map((r) => create(r)), awaitOp] });
    add({ name: "limit-bytes-from-info", unordered: true, server: { info: [{ status: 200, json: INFO({ batch: { size_limit_bytes: 700 } }) }] }, ops: [...runs.map((r) => create(r)), awaitOp] });
    add({ name: "limit-count-option", unordered: true, options: { apiKey: "key-1", apiUrl: "{base}", batchSizeLimit: 3 }, ops: [...runs.map((r) => create(r)), awaitOp] });
  }
  // 5 gzip, no multipart, 404 fallback, streaming disabled, compression disabled
  {
    const a = createPayload(1);
    const ops = [create(a), update(updatePayload(a), a.id), awaitOp];
    add({ name: "gzip", server: { info: [{ status: 200, json: INFO({ flags: { gzip_body_enabled: true } }) }] }, ops });
    add({ name: "gzip-disabled-by-env", env: { LANGSMITH_DISABLE_RUN_COMPRESSION: "true" }, server: { info: [{ status: 200, json: INFO({ flags: { gzip_body_enabled: true } }) }] }, ops });
    add({ name: "gzip-buffered-not-compressed", env: { LANGSMITH_DISABLE_MULTIPART_STREAMING: "true" }, server: { info: [{ status: 200, json: INFO({ flags: { gzip_body_enabled: true } }) }] }, ops });
    add({ name: "streaming-disabled-buffered", env: { LANGCHAIN_DISABLE_MULTIPART_STREAMING: "true" }, ops });
    add({ name: "batch-endpoint-from-info", server: { info: [{ status: 200, json: INFO({ batch: { use_multipart_endpoint: false } }) }] }, ops: [create(a), create(createPayload(2)), update(updatePayload(a), a.id), update(updatePayload(createPayload(7)), uuid(7)), awaitOp] });
    add({ name: "multipart-404-fallback", server: { multipart: [{ status: 404, body: "not found" }] },
      ops: [create(a), update(updatePayload(a), a.id), awaitOp, create(createPayload(2)), awaitOp] });
  }
  // 6 failures: warn text, retry, failed-trace dir
  {
    const a = createPayload(1);
    const ops = [create(a), update(updatePayload(a), a.id), awaitOp];
    add({ name: "multipart-400-warn", server: { multipart: [{ status: 400, body: "  invalid payload \n" }] }, ops });
    add({ name: "multipart-422-warn", server: { multipart: [{ status: 422, body: "cannot parse" }] }, ops });
    add({ name: "multipart-500-then-ok", fastRetry: true, server: { multipart: [{ status: 500, body: "boom" }, { status: 503 }, { status: 200 }] }, ops });
    add({ name: "multipart-500-exhausted", fastRetry: true, server: { multipart: [{ status: 500, body: "boom" }] }, ops });
    add({ name: "failed-traces-dir", env: { LANGSMITH_FAILED_TRACES_DIR: "{tmp}/failed" }, server: { multipart: [{ status: 400, body: "nope" }] }, ops, failedDir: "{tmp}/failed" });
    add({ name: "failed-traces-over-budget", env: { LANGSMITH_FAILED_TRACES_DIR: "{tmp}/failed", LANGSMITH_FAILED_TRACES_MAX_MB: "1" }, server: { multipart: [{ status: 400, body: "nope" }] },
      prefill: { dir: "{tmp}/failed", file: "trace_1_aaaaaaaa.json", bytes: 1048576 }, ops, failedDir: "{tmp}/failed" });
    add({ name: "batch-endpoint-400", server: { info: [{ status: 200, json: INFO({ batch: { use_multipart_endpoint: false } }) }], batch: [{ status: 400, body: "bad batch" }] }, ops });
  }
  // 7 /info failures
  {
    const runs = [1, 2, 3].map((n) => createPayload(n));
    add({ name: "info-500-reprobe", fastRetry: true, server: { info: [{ status: 500, body: "down" }] }, ops: [create(runs[0]), awaitOp, create(runs[1]), awaitOp] });
    add({ name: "info-404", server: { info: [{ status: 404, body: "no info" }] }, ops: [create(runs[0]), create(runs[1]), awaitOp] });
    add({ name: "info-bad-json", server: { info: [{ status: 200, body: "not json" }] }, ops: [create(runs[0]), awaitOp] });
    add({ name: "info-recovers", unordered: true, fastRetry: true, server: { info: [{ status: 404, body: "no info" }, { status: 200, json: INFO({ batch: { size_limit: 1 } }) }] }, ops: [create(runs[0]), awaitOp, create(runs[1]), create(runs[2]), awaitOp] });
  }
  // 8 sampling, metadata, env
  {
    const runs = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((n) => createPayload(n));
    add({ name: "sampling-half", options: { apiKey: "key-1", apiUrl: "{base}", tracingSamplingRate: 0.5 }, ops: [...runs.map((r) => create(r)), ...runs.slice(0, 5).map((r) => update(updatePayload(r), r.id)), awaitOp] });
    add({ name: "sampling-env-zero", env: { LANGSMITH_TRACING_SAMPLING_RATE: "0" }, ops: [create(runs[0]), awaitOp] });
    add({ name: "sampling-env-nan", env: { LANGCHAIN_TRACING_SAMPLING_RATE: "abc" }, ops: [create(runs[0]), awaitOp] });
    add({ name: "sampling-env-range", env: { LANGSMITH_TRACING_SAMPLING_RATE: "1.5" }, ops: [], ctorError: true });
    add({ name: "env-metadata", env: { LANGSMITH_PI_PROJECT: "p1", LANGSMITH_TEAM: "x", LANGCHAIN_REVISION_ID: "rev-7", LANGSMITH_API_KEY_EXTRA: "secretvalue" }, ops: [create(runs[0]), update(updatePayload(runs[0]), runs[0].id), awaitOp] });
    add({ name: "hide-batched", env: { LANGSMITH_HIDE_INPUTS: "true", LANGSMITH_HIDE_OUTPUTS: "true", LANGSMITH_HIDE_METADATA: "true" }, ops: [create(runs[0]), update(updatePayload(runs[0]), runs[0].id), awaitOp] });
    add({ name: "no-api-key-quirk", options: { apiUrl: "{base}" }, ops: [create(runs[0]), awaitOp] });
    add({ name: "concurrency-error", options: { apiKey: "key-1", apiUrl: "{base}", traceBatchConcurrency: 0 }, ops: [], ctorError: true });
    add({ name: "quoted-values", env: { LANGSMITH_WORKSPACE_ID: "  'ws-quoted' " }, options: { apiKey: ' "key-quoted" ', apiUrl: "{base}/" }, ops: [create(runs[0]), awaitOp] });
    add({ name: "langchain-fallback-env", env: { LANGCHAIN_API_KEY: "lc-key", LANGCHAIN_ENDPOINT: "{base}/", LANGCHAIN_WORKSPACE_ID: "lc-ws" }, options: {}, ops: [create(runs[0]), awaitOp] });
  }
  // 8b profile authentication, debug logging, update id validation
  {
    const runs = [1, 2].map((n) => createPayload(n));
    const cfg = (profile) => JSON.stringify({ current_profile: "p", profiles: { p: profile } });
    const farFuture = "2999-01-01T00:00:00.000Z";
    const ops = [create(runs[0]), update(updatePayload(runs[0]), runs[0].id), awaitOp];
    add({ name: "profile-api-key", options: {}, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json" },
      files: [{ path: "{tmp}/cfg.json", text: cfg({ api_url: "{base}/", api_key: " 'pk-1' ", workspace_id: "pw" }) }], ops });
    add({ name: "profile-oauth-batched", options: {}, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json" },
      files: [{ path: "{tmp}/cfg.json", text: cfg({ api_url: "{base}", oauth: { access_token: "at-1", refresh_token: "rt-1", expires_at: farFuture } }) }], ops });
    add({ name: "profile-oauth-direct", options: { autoBatchTracing: false }, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json" },
      files: [{ path: "{tmp}/cfg.json", text: cfg({ api_url: "{base}", oauth: { access_token: "at-1", refresh_token: "rt-1", expires_at: farFuture } }) }], ops });
    add({ name: "profile-env-key-wins", options: {}, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json", LANGSMITH_API_KEY: "env-key" },
      files: [{ path: "{tmp}/cfg.json", text: cfg({ api_url: "{base}", api_key: "pk-1" }) }], ops });
    add({ name: "profile-selected-by-env", options: {}, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json", LANGSMITH_PROFILE: "other" },
      files: [{ path: "{tmp}/cfg.json", text: JSON.stringify({ current_profile: "p", profiles: { p: { api_url: "http://wrong.invalid", api_key: "pk-1" }, other: { api_url: "{base}", api_key: "pk-other" } } }) }], ops });
    const oauthFiles = [{ path: "{tmp}/cfg.json", text: cfg({ api_url: "{base}", oauth: { access_token: "at-1", refresh_token: "rt-1", expires_at: farFuture } }) }];
    const replicaOps = [create(runs[0], { apiKey: "key-r", apiUrl: "{base}" }), update(updatePayload(runs[0]), runs[0].id, { apiKey: "key-r", apiUrl: "{base}" }), create(runs[1], { workspaceId: "ws-r", apiUrl: "{base}" }), awaitOp];
    add({ name: "profile-oauth-replica-key-batched", options: {}, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json" }, files: oauthFiles, unordered: true, ops: replicaOps });
    add({ name: "profile-oauth-replica-key-direct", options: { autoBatchTracing: false }, env: { LANGSMITH_CONFIG_FILE: "{tmp}/cfg.json" }, files: oauthFiles, ops: replicaOps });
    add({ name: "apikey-replica-direct", options: { apiKey: "key-1", apiUrl: "{base}", autoBatchTracing: false }, ops: replicaOps });
    add({ name: "debug-logs", env: { LANGSMITH_DEBUG: "true" }, ops });
    add({ name: "update-invalid-uuid", ops: [update({ outputs: { a: 1 } }, "not-a-uuid"), awaitOp] });
  }
  // 9 destinations
  {
    const runs = [1, 2, 3, 4].map((n) => createPayload(n));
    add({ name: "replica-destinations", unordered: true, ops: [create(runs[0]), create(runs[1], { apiKey: "key-r", apiUrl: "{base}", workspaceId: "ws-r" }), create(runs[2], { apiKey: "key-1", apiUrl: "{base}" }), create(runs[3], { apiKey: "key-pipe|x", apiUrl: "{base}" }), awaitOp] });
  }
  // 9b the ingest memory budget (batchIngestCaller.callWithOptions sizeBytes): batches that do not fit are refused
  {
    const runs = [1, 2, 3, 4, 5].map((n) => createPayload(n));
    const ops = [create(runs[0]), wait(60), create(runs[1]), wait(60), create(runs[2]), wait(60), create(runs[3]), wait(60), create(runs[4]), awaitOp];
    const options = { apiKey: "key-1", apiUrl: "{base}", maxIngestMemoryBytes: 1200, traceBatchConcurrency: 1 };
    add({ name: "ingest-budget-refused-multipart", noInfoDelay: true, options, aggregationDelayMs: 20, server: { multipart: [{ status: 200, json: {}, delayMs: 300 }] }, ops });
    add({ name: "ingest-budget-refused-batch-endpoint", noInfoDelay: true, options, aggregationDelayMs: 20,
      server: { info: [{ status: 200, json: INFO({ batch: { use_multipart_endpoint: false } }) }], batch: [{ status: 200, json: {}, delayMs: 300 }] }, ops });
  }
  // 10 queue drop and root finalization
  {
    const runs = [1, 2, 3].map((n) => createPayload(n));
    add({ name: "queue-size-drop", options: { apiKey: "key-1", apiUrl: "{base}", maxIngestMemoryBytes: 800 }, ops: [...runs.map((r) => create(r)), awaitOp] });
    add({ name: "block-on-root", env: { LANGSMITH_TRACING_BACKGROUND: "false" }, aggregationDelayMs: 100, ops: [create(runs[0]), update(updatePayload(runs[0]), runs[0].id), create(createPayload(5, { parent: runs[0] })), update(updatePayload(createPayload(5, { parent: runs[0] })), uuid(5)), awaitOp] });
    add({ name: "block-on-root-option", options: { apiKey: "key-1", apiUrl: "{base}", blockOnRootRunFinalization: true }, aggregationDelayMs: 100, ops: [create(runs[1]), update(updatePayload(runs[1]), runs[1].id), awaitOp] });
  }
  return S;
}

// ---------- scenario execution against the real SDK ----------

const SHA_VARS = ["VERCEL_GIT_COMMIT_SHA", "NEXT_PUBLIC_VERCEL_GIT_COMMIT_SHA", "COMMIT_REF", "RENDER_GIT_COMMIT", "CI_COMMIT_SHA", "CIRCLE_SHA1", "CF_PAGES_COMMIT_SHA", "REACT_APP_GIT_SHA", "SOURCE_VERSION", "GITHUB_SHA", "TRAVIS_COMMIT", "GIT_COMMIT", "BUILD_VCS_NUMBER", "bamboo_planRepository_revision", "Build.SourceVersion", "BITBUCKET_COMMIT", "DRONE_COMMIT_SHA", "SEMAPHORE_GIT_SHA", "BUILDKITE_COMMIT"];

export const subst = (v, base, tmp) =>
  typeof v === "string" ? v.replaceAll("{base}", base).replaceAll("{tmp}", tmp)
  : Array.isArray(v) ? v.map((x) => subst(x, base, tmp))
  : v && typeof v === "object" ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, subst(x, base, tmp)]))
  : v;

const revive = (v) => {
  if (Array.isArray(v)) return v.map(revive);
  if (v && typeof v === "object") {
    if (v.$undef) return undefined;
    if (v.$bytes !== undefined) return new Uint8Array(Buffer.from(v.$bytes, "base64"));
    const out = {};
    for (const [k, x] of Object.entries(v)) out[k] = revive(x);
    return out;
  }
  return v;
};

function framing(h) {
  return h["transfer-encoding"] ? "chunked" : "length";
}

// canonicalisation shared in meaning with client_golden_test.go
let canonBase = "";
let canonTmp = "";
export function canonText(s) {
  return s
    .replaceAll(canonBase, "{base}")
    .replaceAll(canonTmp, "{tmp}")
    .replaceAll('"sdk":"langsmith-js"', '"sdk":"<sdk>"')
    .replaceAll('"sdk":"pigpen-langsmith-go"', '"sdk":"<sdk>"')
    .replaceAll('"sdk_version":"0.10.5"', '"sdk_version":"<sdkv>"')
    .replaceAll('"sdk_version":"0.1.0"', '"sdk_version":"<sdkv>"')
    .replaceAll('"runtime":"node"', '"runtime":"<rt>"')
    .replaceAll('"runtime":"go"', '"runtime":"<rt>"');
}
const canonCT = (s) => s.replace(/boundary=----LangSmithFormBoundary[0-9a-z]+/, "boundary=<boundary>");

export function parseMultipart(body, boundary) {
  const delim = Buffer.from(`--${boundary}`);
  let rest = body.subarray(delim.length);
  const parts = [];
  for (;;) {
    if (rest.subarray(0, 2).toString() === "--") return parts;
    if (rest.subarray(0, 2).toString() !== "\r\n") throw new Error("bad multipart framing");
    rest = rest.subarray(2);
    const he = rest.indexOf("\r\n\r\n");
    const header = rest.subarray(0, he).toString();
    rest = rest.subarray(he + 4);
    const end = rest.indexOf(Buffer.concat([Buffer.from("\r\n"), delim]));
    const content = rest.subarray(0, end);
    rest = rest.subarray(end + 2 + delim.length);
    const lines = header.split("\r\n");
    const isJSON = lines.some((l) => /^Content-Type: application\/json/.test(l));
    if (!isJSON) {
      parts.push({ header: lines, b64: content.toString("base64") });
      continue;
    }
    // The declared length must be the byte length of the part; after canonicalisation it is replaced by the
    // canonical text's length so two senders that differ only in the runtime values compare equal.
    const declared = Number(/length=(\d+)$/.exec(lines[lines.length - 1])[1]);
    if (declared !== content.length) throw new Error(`part length ${declared} != ${content.length}`);
    const text = canonText(content.toString("utf8"));
    lines[lines.length - 1] = lines[lines.length - 1].replace(/length=\d+$/, `length=${Buffer.byteLength(text)}`);
    parts.push({ header: lines, text });
  }
}

export function canonRequest(route, req, body, base) {
  const h = {};
  const get = (n) => req.headers[n];
  if (get("user-agent")) h["user-agent"] = "<ua>";
  for (const n of ["x-api-key", "x-tenant-id", "authorization", "content-encoding"]) if (get(n) !== undefined) h[n] = get(n);
  if (get("content-type")) h["content-type"] = canonCT(get("content-type"));
  if (get("accept") === "application/json") h.accept = "application/json";
  const out = { route, method: req.method, path: req.url, headers: h, framing: body.length || req.method !== "GET" ? framing(req.headers) : "none" };
  let raw = body;
  if (get("content-encoding") === "gzip") raw = zlib.gunzipSync(body);
  const ct = get("content-type") ?? "";
  const m = /boundary=(.+)$/.exec(ct);
  if (m) out.parts = parseMultipart(raw, m[1]);
  else if (raw.length) out.body = canonText(raw.toString("utf8"));
  return out;
}

const DEFAULT_RESP = {
  info: { status: 200, json: INFO() },
  multipart: { status: 200, json: {} },
  batch: { status: 200, json: {} },
  runs: { status: 200, json: {} },
  patch: { status: 200, json: {} },
};

function routeOf(req) {
  const u = req.url.split("?")[0];
  if (u === "/info") return "info";
  if (u === "/runs/multipart") return "multipart";
  if (u === "/runs/batch") return "batch";
  if (u === "/runs" && req.method === "POST") return "runs";
  if (u.startsWith("/runs/") && req.method === "PATCH") return "patch";
  return "other";
}

export function canonLog(s, base, tmp) {
  return s
    .replaceAll(base, "{base}")
    .replaceAll(tmp, "{tmp}")
    .replace(/trace_\d+_[0-9a-f]{8}\.json/g, "trace_<ts>_<id>.json")
    .replace(/\n {4}at [\s\S]*$/, ""); // console.error(err): drop the stack and the inspected properties after it
}

async function runScenario(sc) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "lsclient-"));
  for (const k of Object.keys(process.env)) if (/^(LANGSMITH_|LANGCHAIN_|OTEL_)/.test(k) || SHA_VARS.includes(k)) delete process.env[k];
  process.env.HOME = tmp;
  process.env.LANGSMITH_CONFIG_FILE = path.join(tmp, "none.json");

  const counters = {};
  const requests = [];
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", async () => {
      const route = routeOf(req);
      counters[route] = (counters[route] ?? 0) + 1;
      const list = sc.server?.[route] ?? [DEFAULT_RESP[route] ?? { status: 200, json: {} }];
      const step = list[Math.min(counters[route] - 1, list.length - 1)];
      requests.push({ route, req, body: Buffer.concat(chunks) });
      if (step.delayMs) await new Promise((r) => setTimeout(r, step.delayMs));
      const payload = step.json !== undefined ? JSON.stringify(step.json) : (step.body ?? "");
      res.writeHead(step.status, { "content-type": step.json !== undefined ? "application/json" : "text/plain", "content-length": Buffer.byteLength(payload) });
      res.end(payload);
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;
  canonBase = base;
  canonTmp = tmp;
  const sub = (v) => subst(v, base, tmp);

  for (const f of sc.files ?? []) {
    const fp = sub(f.path);
    fs.mkdirSync(path.dirname(fp), { recursive: true });
    fs.writeFileSync(fp, sub(f.text));
  }
  if (sc.prefill) {
    const d = sub(sc.prefill.dir);
    fs.mkdirSync(d, { recursive: true });
    fs.writeFileSync(path.join(d, sc.prefill.file), Buffer.alloc(sc.prefill.bytes, 97));
  }
  for (const [k, v] of Object.entries(sc.env ?? {})) process.env[k] = sub(v);

  const logs = [];
  const real = { warn: console.warn, error: console.error, log: console.log };
  for (const k of ["warn", "error", "log"]) console[k] = (...a) => logs.push([k, canonLog(util.format(...a), base, tmp)]);
  // Backoff sleeps are recorded and fired at once.
  const realSetTimeout = globalThis.setTimeout;
  if (sc.fastRetry) {
    globalThis.setTimeout = (fn, ms, ...a) => (typeof ms === "number" && ms >= 1000 && new Error().stack.includes("p-retry") ? realSetTimeout(fn, 0, ...a) : realSetTimeout(fn, ms, ...a));
  }
  const errors = [];
  let ctorError = null;
  try {
    const o = sub(sc.options);
    let client;
    try {
      client = new Client(o);
    } catch (e) {
      ctorError = e.message;
    }
    if (client) {
      if (sc.aggregationDelayMs) client.autoBatchAggregationDelayMs = sc.aggregationDelayMs;
      for (const op of sc.ops) {
        if (op.op === "wait") await new Promise((r) => realSetTimeout(r, op.ms));
        else if (op.op === "await") await client.awaitPendingTraceBatches();
        else {
          try {
            if (op.op === "create") await client.createRun(revive(sub(op.run)), op.dest ? sub(op.dest) : undefined);
            else await client.updateRun(op.id, revive(sub(op.run)), op.dest ? sub(op.dest) : undefined);
          } catch (e) {
            errors.push(`${e.name}: ${e.message}`);
          }
        }
      }
      await client.awaitPendingTraceBatches();
    }
  } finally {
    globalThis.setTimeout = realSetTimeout;
    Object.assign(console, real);
  }
  await new Promise((r) => server.close(r));
  const out = { requests: requests.map((r) => canonRequest(r.route, r.req, r.body, base)), logs, errors, ctorError };
  if (sc.failedDir) {
    const d = sub(sc.failedDir);
    const files = fs.existsSync(d) ? fs.readdirSync(d).sort() : [];
    out.failedFiles = files.map((f) => {
      const st = fs.statSync(path.join(d, f));
      if (!/^trace_\d+_[0-9a-f]{8}\.json$/.test(f) || st.size > 100000) return { name: f.replace(/trace_\d+_[0-9a-f]{8}\.json/, "trace_<ts>_<id>.json"), size: st.size };
      const env = JSON.parse(fs.readFileSync(path.join(d, f), "utf8"));
      const body = Buffer.from(env.body_base64, "base64");
      const ct = env.headers["Content-Type"];
      return { name: "trace_<ts>_<id>.json", keys: Object.keys(env), version: env.version, endpoint: env.endpoint, headers: { "Content-Type": canonCT(ct) },
        parts: parseMultipart(body, /boundary=(.+)$/.exec(ct)[1])};
    });
    out.failedDirExists = files.length > 0;
  }
  fs.rmSync(tmp, { recursive: true, force: true });
  return out;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const scenarios = buildScenarios();
  const golden = {};
  for (const sc of scenarios) {
    golden[sc.name] = await runScenario(sc);
    console.log(sc.name, golden[sc.name].requests.length, "requests");
  }
  writeVectors("client_scenarios.json", scenarios);
  writeVectors("client_golden.json", golden);
}
