#!/usr/bin/env node
// gen/sdkdiff-ops.mjs: writes ../sdkdiff/ops/*.json, the op scripts sdkdrive.mjs (real JS SDK) and sdkdiff_test.go (the
// Go port) both replay. Fixed ids and times; the only nondeterministic bytes on the wire are the multipart boundary,
// the `time` of addEvent events and the mockls port, all normalised by lsdiff.
//   node gen/sdkdiff-ops.mjs
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "..", "..", "sdkdiff", "ops");

const T0 = 1790000000000;
const id = (n) => `0198a000-0000-7000-8000-${String(n).padStart(12, "0")}`;
const INFO = {
  version: "mock",
  license_expiration_time: null,
  batch_ingest_config: { use_multipart_endpoint: true, scale_up_qsize_trigger: 1000, scale_up_nthreads_limit: 16, scale_down_nempty_trigger: 4, size_limit: 100, size_limit_bytes: 20971520 },
  instance_flags: { gzip_body_enabled: false },
};
const info = (bic = {}, flags = {}) => ({ ...INFO, batch_ingest_config: { ...INFO.batch_ingest_config, ...bic }, instance_flags: { ...INFO.instance_flags, ...flags } });
const client = (config = {}, as = "c") => ({ op: "client", as, config: { apiUrl: "{{endpoint}}", apiKey: "ls-key", ...config } });
const root = (n, config = {}, as = "root", cl = "c") => ({ op: "runTree", as, client: cl, config: { id: id(n), name: "Pi agent run", run_type: "chain", start_time: T0, project_name: "proj", inputs: { prompt: "hi", imageCount: 0 }, ...config } });
const child = (parent, n, as, config = {}) => ({ op: "createChild", parent, as, config: { id: id(n), start_time: T0 + 10 * n, ...config } });
const end = (run, outputs, o = {}) => ({ op: "end", run, outputs, endTime: T0 + 5000, ...o });
const post = (run) => ({ op: "postRun", run });
const patch = (run) => ({ op: "patchRun", run });
const flush = (c = "c") => ({ op: "awaitPendingTraceBatches", client: c });

const list = [];
const add = (name, description, body) => list.push({ name, description, ...body });

add("basic-post-patch", "A root run: postRun, end, patchRun in one batch (create and update merge into one multipart create).", {
  ops: [client(), root(1), post("root"), end("root", { messages: [{ role: "assistant", content: "ok" }] }), patch("root"), flush()],
});
add("separate-batches", "postRun, a pause longer than the 250 ms aggregation window, then patchRun: a create-only and an update-only request.", {
  ops: [client(), root(1), post("root"), { op: "sleep", ms: 600 }, end("root", { done: true }), patch("root"), flush()],
});
add("tree-llm-tool", "Root, turn, llm and tool children with metadata, tags, an event and an error end.", {
  ops: [
    client(),
    root(1, { metadata: { ls_agent_type: "root", turn_number: 1 }, tags: ["pi", "coding-agent"] }),
    post("root"),
    child("root", 2, "turn", { name: "Pi turn 0", run_type: "chain", inputs: { turnIndex: 0 }, metadata: { ls_provider: "openai", ls_model_name: "gpt-x", timestamp: T0 + 20, pi_loop_index: 0 } }),
    post("turn"),
    child("turn", 3, "llm", { name: "openai", run_type: "llm", inputs: { messages: [{ role: "user", content: "hi" }], model: "gpt-x", stream: true }, metadata: { ls_provider: "openai" } }),
    post("llm"),
    end("llm", { messages: [{ role: "assistant", content: [{ type: "text", text: "hello" }] }] }, { metadata: { stop_reason: "stop", usage_metadata: { input_tokens: 3, output_tokens: 2, total_tokens: 5, input_token_details: { cache_read: 0 } } } }),
    patch("llm"),
    child("turn", 4, "tool", { name: "read", run_type: "tool", inputs: { toolCallId: "call_1", args: { path: "a.txt" } }, metadata: { toolName: "read", toolCallId: "call_1" } }),
    post("tool"),
    { op: "addEvent", run: "tool", event: { name: "tool_update", kwargs: { partialResult: { content: [{ type: "text", text: "par" }] } } } },
    end("tool", undefined, { error: "Tool execution failed" }),
    patch("tool"),
    end("turn", { messages: [{ role: "assistant", content: "hello" }], toolResults: [] }),
    patch("turn"),
    end("root", { messages: [], context_usage: { tokens: 10, contextWindow: 100, percent: 10 } }),
    patch("root"),
    flush(),
  ],
});
add("auto-batch-off", "autoBatchTracing:false (the upstream tests' setting): POST /runs and PATCH /runs/<id> directly.", {
  ops: [client({ autoBatchTracing: false }), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("batch-endpoint", "/info says use_multipart_endpoint=false: runs go to /runs/batch as JSON.", {
  mockls: { info: info({ use_multipart_endpoint: false }) },
  ops: [client(), root(1), post("root"), child("root", 2, "k", { name: "k", run_type: "tool", inputs: { a: 1 } }), post("k"), end("k", { r: 1 }), patch("k"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("multipart-gzip", "gzip_body_enabled: the multipart body is gzip-encoded (Content-Encoding: gzip).", {
  mockls: { info: info({}, { gzip_body_enabled: true }) },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("multipart-404-fallback", "/runs/multipart answers 404: the batch is re-sent to /runs/batch and multipart is disabled afterwards.", {
  mockls: { faults: [{ route: "multipart", calls: [1], status: 404, body: "not found" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), { op: "sleep", ms: 600 }, root(2, {}, "r2"), post("r2"), end("r2", { ok: 2 }), patch("r2"), flush()],
});
add("multipart-422-buffered", "A 422 on the streamed multipart upload: console.warn, then a buffered (content-length) retry; later requests stay buffered.", {
  mockls: { faults: [{ route: "multipart", calls: [1], status: 422, body: "chunked unsupported" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), { op: "sleep", ms: 600 }, root(2, {}, "r2"), post("r2"), end("r2", { ok: 2 }), patch("r2"), flush()],
});
add("multipart-429-retry", "429 with Retry-After: the caller waits and retries the same request.", {
  mockls: { faults: [{ route: "multipart", calls: [1], status: 429, headers: { "Retry-After": "1" }, body: "slow down" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("multipart-500-retries", "Two 500s then success: exponential backoff retries.", {
  mockls: { faults: [{ route: "multipart", calls: [1, 2], status: 500, body: "oops" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("multipart-400-no-retry", "A 400 is not retried: console.warn with the failure and the context line.", {
  mockls: { faults: [{ route: "multipart", status: 400, body: "bad request" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("buffered-env", "LANGSMITH_DISABLE_MULTIPART_STREAMING=true: buffered multipart (content-length).", {
  env: { LANGSMITH_DISABLE_MULTIPART_STREAMING: "true" },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("info-unavailable", "GET /info answers 500: the SDK keeps its defaults and still sends.", {
  mockls: { faults: [{ route: "info", status: 500, body: "boom" }] },
  ops: [client(), root(1), post("root"), end("root", { ok: 1 }), patch("root"), flush()],
});
add("size-limit-flush", "batch_ingest_config.size_limit=2: the queue drains as soon as it is over the limit into three concurrent batches; which reaches the wire first is scheduler order, so the replay compares them as a multiset (unorderedDrain).", {
  unorderedDrain: true,
  mockls: { info: info({ size_limit: 2 }) },
  ops: [client(), root(1), post("root"), child("root", 2, "a", { name: "a" }), post("a"), child("root", 3, "b", { name: "b" }), post("b"), end("b", { r: 1 }), patch("b"), end("a", { r: 1 }), patch("a"), end("root", { r: 1 }), patch("root"), flush()],
});
add("hide-inputs-outputs-env", "LANGSMITH_HIDE_INPUTS/OUTPUTS=true.", {
  env: { LANGSMITH_HIDE_INPUTS: "true", LANGSMITH_HIDE_OUTPUTS: "true" },
  ops: [client(), root(1), post("root"), end("root", { secret: "x" }), patch("root"), flush()],
});
add("hide-metadata-env", "LANGSMITH_HIDE_METADATA=true.", {
  env: { LANGSMITH_HIDE_METADATA: "true" },
  ops: [client(), root(1, { metadata: { team: "core" } }), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("sampling-one", "LANGSMITH_TRACING_SAMPLING_RATE=1: metadata.ls_tracing_sample_rate.", {
  env: { LANGSMITH_TRACING_SAMPLING_RATE: "1" },
  ops: [client(), root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("sampling-zero", "LANGSMITH_TRACING_SAMPLING_RATE=0: nothing is sent.", {
  env: { LANGSMITH_TRACING_SAMPLING_RATE: "0" },
  ops: [client(), root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("sampling-out-of-range", "LANGSMITH_TRACING_SAMPLING_RATE=2: the Client constructor throws.", {
  env: { LANGSMITH_TRACING_SAMPLING_RATE: "2" },
  ops: [client(), root(1), post("root"), flush()],
});
add("workspace-header", "LANGSMITH_WORKSPACE_ID becomes x-tenant-id.", {
  env: { LANGSMITH_WORKSPACE_ID: "ws-123" },
  ops: [client(), root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("env-metadata", "LANGSMITH_*/LANGCHAIN_* variables that are not secrets land in extra.metadata of every run.", {
  env: { LANGSMITH_PROJECT: "env-proj", LANGSMITH_NOTE: "n", LANGCHAIN_NOTE: "m", LANGSMITH_SECRET_THING: "hidden", LANGSMITH_PI_PROJECT: "pi-proj" },
  ops: [client(), root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("exclude-inputs-on-patch", "LANGSMITH_EXCLUDE_INPUTS_ON_PATCH=true.", {
  env: { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "true" },
  ops: [client(), root(1), post("root"), { op: "sleep", ms: 600 }, end("root", { r: 1 }), patch("root"), flush()],
});
add("background-false", "LANGSMITH_TRACING_BACKGROUND=false.", {
  env: { LANGSMITH_TRACING_BACKGROUND: "false" },
  ops: [client(), root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("replicas-projects", "Replicas with another project (ids remapped), the primary project (ids kept) and updates applied to PATCH only.", {
  unorderedDrain: true,
  ops: [
    client(),
    root(1, { project_name: "main", replicas: [{ apiUrl: "{{endpoint}}", apiKey: "k-main", projectName: "main" }, { apiUrl: "{{endpoint}}", apiKey: "k-other", projectName: "other", updates: { metadata: { replica: "other" }, tags: ["r"] } }] }),
    post("root"),
    child("root", 2, "k", { name: "k", run_type: "tool", inputs: { a: 1 } }),
    post("k"),
    end("k", { r: 1 }),
    patch("k"),
    end("root", { r: 1 }),
    patch("root"),
    flush(),
  ],
});
add("replicas-env", "LANGSMITH_RUNS_ENDPOINTS read by the RunTree (object form url -> key).", {
  env: { LANGSMITH_RUNS_ENDPOINTS: JSON.stringify([{ api_url: "{{endpoint}}", api_key: "k-a", project_name: "proj-a" }]) },
  ops: [client(), root(1, { project_name: "main" }), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("unicode-lengths", "Non-ASCII payloads: the part length= counts UTF-8 bytes of the serialised part; JSON.stringify leaves U+2028 and astral characters as they are.", {
  ops: [client(), root(1, { inputs: { prompt: "héllo wörld 😀 \u2028 \"q\" \\" } }), post("root"), end("root", { text: "Grüße 🌍" }), patch("root"), flush()],
});
add("profile-api-key", "No env credentials: the CLI profile (LANGSMITH_CONFIG_FILE, relative) supplies api_key, api_url and workspace.", {
  env: { LANGSMITH_CONFIG_FILE: "profile.json" },
  files: { "profile.json": { current_profile: "dev", profiles: { dev: { api_key: "profile-key", api_url: "{{endpoint}}", workspace_id: "pw-1" } } } },
  ops: [{ op: "client", as: "c", config: {} }, root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("profile-oauth-refresh", "Expired OAuth token: discovery (metadata 404 -> /oauth/token), form POST, Bearer on the retry, profile file rewritten.", {
  env: { LANGSMITH_CONFIG_FILE: "profile.json" },
  files: { "profile.json": { current_profile: "dev", profiles: { dev: { api_url: "{{endpoint}}", oauth: { access_token: "tok-old", refresh_token: "ref-1", expires_at: "2000-01-01T00:00:00.000Z" } } } } },
  readFiles: ["profile.json"],
  mockls: { token: { access_token: "tok-new", refresh_token: "ref-2", expires_in: 3600 } },
  ops: [{ op: "client", as: "c", config: {} }, root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("profile-oauth-metadata", "OAuth discovery document served: the token endpoint comes from the document.", {
  env: { LANGSMITH_CONFIG_FILE: "profile.json" },
  files: { "profile.json": { current_profile: "dev", profiles: { dev: { api_url: "{{endpoint}}", oauth: { refresh_token: "ref-1" } } } } },
  readFiles: ["profile.json"],
  mockls: { oauthMetadata: { issuer: "{{base}}", token_endpoint: "{{base}}/custom/oauth/token", device_authorization_endpoint: "{{base}}/dev" }, token: { access_token: "tok-meta", expires_in: 60 } },
  ops: [{ op: "client", as: "c", config: {} }, root(1), post("root"), end("root", { r: 1 }), patch("root"), flush()],
});
add("create-update-run-direct", "Client.createRun / updateRun called directly with prepared run objects.", {
  ops: [
    client(),
    { op: "createRun", client: "c", run: { id: id(1), trace_id: id(1), dotted_order: "20261010T000000000000Z" + id(1), name: "direct", run_type: "chain", start_time: T0, inputs: { a: 1 }, session_name: "proj" } },
    { op: "updateRun", client: "c", id: id(1), run: { id: id(1), trace_id: id(1), dotted_order: "20261010T000000000000Z" + id(1), end_time: T0 + 1, outputs: { b: 2 }, session_name: "proj" } },
    flush(),
  ],
});

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });
for (const s of list) {
  const { name, ...rest } = s;
  writeFileSync(join(outDir, `${name}.json`), JSON.stringify({ name, ...rest }, null, 2) + "\n");
}
console.log(`${list.length} op scripts in ${outDir}`);
