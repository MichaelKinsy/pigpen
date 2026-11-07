#!/usr/bin/env node
// gen/scenarios.mjs: writes ../scenarios/*.json (pigeq scenarios that run unchanged under `pigeq run|record|check`,
// the LangSmith endpoint being a pigeq `servers.ls` entry) and ../scenarios-wire/*.json (scenarios whose wire bytes
// only mockls can compare: gzip, framing; run them with run-lane.sh -s). Regenerate with
//   node gen/scenarios.mjs
// run-lane.sh turns either directory into mockls lanes (lane/materialise.mjs).
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const portDir = join(here, "..", "..");

const INFO = {
  version: "mock",
  license_expiration_time: null,
  batch_ingest_config: {
    use_multipart_endpoint: true,
    scale_up_qsize_trigger: 1000,
    scale_up_nthreads_limit: 16,
    scale_down_nempty_trigger: 4,
    size_limit: 100,
    size_limit_bytes: 20971520,
  },
  instance_flags: { gzip_body_enabled: false },
};
const info = (patch = {}) => ({
  ...INFO,
  batch_ingest_config: { ...INFO.batch_ingest_config, ...(patch.batch_ingest_config ?? {}) },
  instance_flags: { ...INFO.instance_flags, ...(patch.instance_flags ?? {}) },
});

// N7 rules: the values that differ on every run or between the SDKs by decision D1 (see lane-harness.md). The
// strict, structure-aware comparison is lsdiff's; these keep pigeq's trace comparison usable.
const NORMALIZE = [
  { pattern: "----LangSmithFormBoundary[0-9a-z]+", replacement: "<boundary>" },
  { pattern: "[0-9]{8}T[0-9]{12}Z", replacement: "<ts>" },
  { pattern: "\"(start_time|end_time|timestamp|durationMs)\":[0-9]+", replacement: "\"$1\":<ts:num>" },
  // The host's own system prompt (Pi's and PiG's differ in wording and doc paths): a system message's text and the
  // `sections` the host attaches to it. lsdiff does the same for a Pi-versus-PiG pair only (N8), never for one host.
  { pattern: "\"role\":\"system\",\"content\":\"(?:[^\"\\\\]|\\\\.)*\"", replacement: "\"role\":\"system\",\"content\":\"<host-prompt>\"" },
  { pattern: "\"sections\":\\{.*?\\},\"timestamp\"", replacement: "\"sections\":\"<host-prompt>\",\"timestamp\"" },
  // metadata.ts: os.userInfo().username of whoever records or checks.
  { pattern: "\"local_username\":\"[^\"]*\"", replacement: "\"local_username\":\"<user>\"" },
  { pattern: "\"runtime\":\"(node|go)\"", replacement: "\"runtime\":\"<d1>\"" },
  { pattern: "\"sdk\":\"[^\"]*\"", replacement: "\"sdk\":\"<d1>\"" },
  { pattern: "\"sdk_version\":\"[^\"]*\"", replacement: "\"sdk_version\":\"<d1>\"" },
  { pattern: "length=[0-9]+", replacement: "length=<n>" },
];

// Host events in the trace. The agent/turn/message lifecycle is left out: where the extension's own requests (GET /info,
// the multipart POST) fall between those events depends on the host's output pacing, which differs between Pi and PiG
// and is not the extension's behaviour. The extension's UI calls, its tool-visible effects and its requests stay.
const CAPTURE = ["response", "extension_ui_request", "extension_error", "tool_execution_start", "tool_execution_end"];

const ENV = {
  TRACE_TO_LANGSMITH: "true",
  LANGSMITH_PI_ENDPOINT: "{{server:ls}}",
  LANGSMITH_PI_API_KEY: "ls-test-key",
};

const ok = { json: {} };
// failures: [{route: "multipart"|"batch"|"runs"|"patch"|"token"..., times, status, ...}] come first.
const PATHS = { multipart: ["POST", "/runs/multipart"], batch: ["POST", "/runs/batch"], runs: ["POST", "/runs"], patch: ["PATCH", "/runs/*"], token: ["POST", "/oauth/token"] };
function routes({ info: inf = info(), failures = [], extra = [] } = {}) {
  const out = [];
  for (const f of failures) {
    const { route, ...rest } = f;
    const [method, path] = PATHS[route];
    out.push({ method, path, ...rest });
  }
  out.push(...extra);
  out.push({ method: "GET", path: "/info", json: inf });
  out.push({ method: "POST", path: "/runs/multipart", ...ok });
  out.push({ method: "POST", path: "/runs/batch", ...ok });
  out.push({ method: "POST", path: "/runs", ...ok });
  out.push({ method: "PATCH", path: "/runs/*", ...ok });
  return out;
}

const prompt = (message, extra = {}) => ({ rpc: { type: "prompt", message }, ...extra });
const cmd = (message, extra = {}) => ({ rpc: { type: "prompt", message }, ...extra });

function scenario(o) {
  const sc = {
    name: o.name,
    description: o.description,
    ...(o.setup ? { setup: o.setup } : {}),
    // metadata.ts runs `git` (branch, commit) for the coding-agent-v1 metadata: recorded, run for real.
    commands: { git: {}, ...(o.commands ?? {}) },
    ...(o.files ? { files: o.files } : {}),
    ...(o.agentFiles ? { agentFiles: o.agentFiles } : {}),
    ...(o.homeFiles ? { homeFiles: o.homeFiles } : {}),
    ...(o.args ? { args: o.args } : {}),
    servers: { ls: { routes: o.routes ?? routes(), recordHeaders: ["x-api-key", "x-tenant-id", "authorization", "content-encoding"] }, ...(o.servers ?? {}) },
    env: o.noEnv ? (o.env ?? {}) : { ...ENV, ...(o.env ?? {}) },
    ...(o.llm ? { llm: o.llm } : {}),
    steps: o.steps ?? [prompt("hello")],
    settleMs: o.settleMs ?? 700,
    tailMs: o.tailMs ?? 1500,
    capture: CAPTURE,
    normalize: [...NORMALIZE, ...(o.normalize ?? [])],
  };
  if (!o.llm && !o.noLlm && (o.steps ?? [prompt("hello")]).some((s) => s.rpc.type === "prompt" && !s.rpc.message.startsWith("/"))) {
    sc.llm = [{ text: "ok" }];
  }
  return sc;
}

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
const read = (path) => ({ name: "read", arguments: { path } });
const bash = (command) => ({ name: "bash", arguments: { command } });

const S = []; // pigeq-native
const W = []; // wire only

// ---- the agent loop ------------------------------------------------------------------------------------------
S.push(scenario({ name: "trace-basic-turn", description: "One prompt, one scripted answer: root run, turn, llm run, shutdown flush.", steps: [prompt("hello there")], llm: [{ text: "Hi! How can I help?" }] }));
S.push(scenario({
  name: "trace-tool-read", description: "A tool call (read) then the final answer: tool run inputs/outputs, two turns, two llm runs.",
  files: { "notes.txt": "alpha\nbeta\ngamma\n" },
  llm: [{ toolCalls: [read("notes.txt")] }, { text: "The file has three lines." }],
  steps: [prompt("What is in notes.txt?")],
}));
S.push(scenario({
  name: "trace-tool-error", description: "A failing tool result: error 'Tool execution failed' on the tool run.",
  llm: [{ toolCalls: [read("missing-file.txt")] }, { text: "That file does not exist." }],
  steps: [prompt("read missing-file.txt")],
}));
S.push(scenario({
  name: "trace-bash-tool", description: "bash tool: text content result and exit handling.",
  llm: [{ toolCalls: [bash("echo traced && exit 0")] }, { text: "done" }],
  steps: [prompt("run echo")],
}));
S.push(scenario({
  name: "trace-two-prompts", description: "Two user prompts in one session: turn_number 1 then 2, two separate traces.",
  llm: [{ text: "first answer" }, { text: "second answer" }],
  steps: [prompt("first"), prompt("second")],
}));
S.push(scenario({
  name: "trace-image-prompt", description: "A prompt with an image: imageCount 1 and the multimodal conversion of the user message.",
  llm: [{ text: "A tiny picture." }],
  steps: [{ rpc: { type: "prompt", message: "what is this", images: [{ type: "image", data: PNG, mimeType: "image/png" }] } }],
}));
S.push(scenario({
  name: "trace-tool-image-result", description: "read of an image file: the tool result carries an image part (mimeType/data -> mime_type/base64).",
  setup: [["sh", "-c", `printf '%s' ${PNG} | base64 -d > pic.png`]],
  llm: [{ toolCalls: [read("pic.png")] }, { text: "I see a pixel." }],
  steps: [prompt("read pic.png")],
}));

// ---- commands, status, configuration -------------------------------------------------------------------------
S.push(scenario({
  name: "disabled-by-default", description: "No TRACE_TO_LANGSMITH: status 'LangSmith: disabled', no request, the command warns.",
  noEnv: true, steps: [prompt("hello"), cmd("/langsmith-tracing")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "disabled-explicit-false", description: "TRACE_TO_LANGSMITH=0 beats an enabling config file.",
  env: { TRACE_TO_LANGSMITH: "0" },
  files: { ".pi/langsmith.json": JSON.stringify({ enabled: true, api_url: "{{server:ls}}", api_key: "k" }) },
  steps: [prompt("hello"), cmd("/langsmith-tracing")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "command-tracing-enabled", description: "/langsmith-tracing before a run and while a run is active.",
  steps: [cmd("/langsmith-tracing"), prompt("hello"), cmd("/langsmith-tracing")], llm: [{ text: "ok" }],
}));
for (const [v, label] of [["yes", "yes"], ["On", "on"], [" TRUE ", "padded-true"], ["maybe", "unparsable"]]) {
  S.push(scenario({ name: `enabled-value-${label}`, description: `TRACE_TO_LANGSMITH=${JSON.stringify(v)} (parseBoolean).`, env: { TRACE_TO_LANGSMITH: v }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
}
S.push(scenario({
  name: "config-env-project-metadata", description: "LANGSMITH_PI_PROJECT, LANGSMITH_PI_METADATA (JSON) and non-secret LANGSMITH_* variables end up in session_name and metadata.",
  env: { LANGSMITH_PI_PROJECT: "pi-proj", LANGSMITH_PI_METADATA: JSON.stringify({ team: "core", nested: { b: 2, a: 1 } }), LANGSMITH_NOTE: "hello" },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-env-metadata-invalid", description: "LANGSMITH_PI_METADATA that is not JSON is ignored.",
  env: { LANGSMITH_PI_METADATA: "{not json" }, steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-env-fallback-names", description: "LANGSMITH_PROJECT / LANGSMITH_API_KEY / LANGSMITH_ENDPOINT without the PI_ prefix.",
  noEnv: true,
  env: { TRACE_TO_LANGSMITH: "true", LANGSMITH_ENDPOINT: "{{server:ls}}", LANGSMITH_API_KEY: "plain-key", LANGSMITH_PROJECT: "plain-proj" },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-file-workspace", description: "Config only from <workspace>/.pi/langsmith.json (enabled, api_url, api_key, project, metadata).",
  noEnv: true,
  files: { ".pi/langsmith.json": JSON.stringify({ enabled: true, api_url: "{{server:ls}}", api_key: "file-key", project: "file-proj", metadata: { from: "file" } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-file-env-wins", description: "Environment variables override the workspace config file field by field.",
  env: { LANGSMITH_PI_PROJECT: "env-proj" },
  files: { ".pi/langsmith.json": JSON.stringify({ enabled: false, api_key: "file-key", project: "file-proj", metadata: { from: "file", keep: 1 } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-file-invalid", description: "An invalid workspace config file is ignored (defaults: disabled).",
  noEnv: true, files: { ".pi/langsmith.json": "{ nope" }, steps: [prompt("hello"), cmd("/langsmith-tracing")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-file-schema-violation", description: "A config file that fails the schema (enabled: 'yes') is ignored as a whole.",
  noEnv: true, env: { TRACE_TO_LANGSMITH: "true", LANGSMITH_PI_ENDPOINT: "{{server:ls}}", LANGSMITH_PI_API_KEY: "k" },
  files: { ".pi/langsmith.json": JSON.stringify({ enabled: "yes", project: "from-file" }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));

// ---- replicas, sampling, redaction -----------------------------------------------------------------------------
const ls = "{{server:ls}}";
S.push(scenario({
  name: "replicas-env", description: "LANGSMITH_PI_RUNS_ENDPOINTS: runs go only to the replicas (own key, project); updates apply to PATCH only.",
  env: { LANGSMITH_PI_RUNS_ENDPOINTS: JSON.stringify([{ api_url: ls, api_key: "key-a", project: "proj-a" }, { api_url: ls, api_key: "key-b", project: "proj-b", updates: { metadata: { replica: "b" }, tags: ["rb"] } }]) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "replicas-camel-case", description: "Replica keys apiUrl/apiKey/projectName are accepted next to the snake_case ones; primary/reroot/workspaceId are stripped.",
  env: { LANGSMITH_PI_RUNS_ENDPOINTS: JSON.stringify([{ apiUrl: ls, apiKey: "key-c", projectName: "proj-c", primary: true, workspaceId: "w1", reroot: true }]) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "replicas-same-project", description: "A replica naming the primary project keeps the original run ids; a second one with another project remaps them.",
  env: { LANGSMITH_PI_PROJECT: "main-proj", LANGSMITH_PI_RUNS_ENDPOINTS: JSON.stringify([{ api_url: ls, api_key: "k1", project: "main-proj" }, { api_url: ls, api_key: "k2", project: "other-proj" }]) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "replicas-sdk-env-array", description: "LANGSMITH_RUNS_ENDPOINTS (no PI_ prefix) is read by the extension's own config as a fallback: the array form works, project_name is not a replica field of the extension (ignored).",
  env: { LANGSMITH_RUNS_ENDPOINTS: JSON.stringify([{ api_url: ls, api_key: "sdk-key", project_name: "ignored-name" }]) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "replicas-file", description: "replicas from <workspace>/.pi/langsmith.json.",
  files: { ".pi/langsmith.json": JSON.stringify({ replicas: [{ api_url: ls, api_key: "fkey", project: "fproj", updates: { extra: { runtime: { file: true } } } }] }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({ name: "sampling-zero", description: "LANGSMITH_TRACING_SAMPLING_RATE=0: no run is sent (ls_tracing_sample_rate stays in metadata of none).", env: { LANGSMITH_TRACING_SAMPLING_RATE: "0" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "sampling-one", description: "LANGSMITH_TRACING_SAMPLING_RATE=1: every run is sent, metadata carries ls_tracing_sample_rate.", env: { LANGSMITH_TRACING_SAMPLING_RATE: "1" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "hide-inputs-outputs", description: "LANGSMITH_HIDE_INPUTS/OUTPUTS=true: inputs and outputs are replaced by {}.", env: { LANGSMITH_HIDE_INPUTS: "true", LANGSMITH_HIDE_OUTPUTS: "true" }, llm: [{ toolCalls: [bash("echo secret")] }, { text: "done" }], steps: [prompt("run it")] }));
S.push(scenario({ name: "hide-metadata", description: "LANGSMITH_HIDE_METADATA=true: run metadata is masked.", env: { LANGSMITH_HIDE_METADATA: "true", LANGSMITH_PI_METADATA: "{\"team\":\"core\"}" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "workspace-id", description: "LANGSMITH_WORKSPACE_ID becomes the x-tenant-id header.", env: { LANGSMITH_WORKSPACE_ID: "ws-123" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "langchain-env-names", description: "LANGCHAIN_* fallbacks (HIDE_INPUTS) and metadata carrying LANGCHAIN_PROJECT.", env: { LANGCHAIN_HIDE_INPUTS: "true", LANGCHAIN_PROJECT: "lc-proj" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "exclude-inputs-on-patch", description: "LANGSMITH_EXCLUDE_INPUTS_ON_PATCH=true keeps inputs out of PATCH (run_trees.js).", env: { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "true" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "tracing-background-false", description: "LANGSMITH_TRACING_BACKGROUND=false: each run operation is awaited (no 250 ms batching of this process).", env: { LANGSMITH_TRACING_BACKGROUND: "false" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));

// ---- ingestion behaviours --------------------------------------------------------------------------------------
S.push(scenario({ name: "ingest-no-multipart", description: "/info says use_multipart_endpoint=false: runs go to /runs/batch.", routes: routes({ info: info({ batch_ingest_config: { use_multipart_endpoint: false } }) }), steps: [prompt("hello")], llm: [{ text: "ok" }] }));
S.push(scenario({ name: "ingest-multipart-404-fallback", description: "/runs/multipart answers 404 once: the batch falls back to /runs/batch and multipart stays off.", routes: routes({ failures: [{ route: "multipart", status: 404, times: 1, body: "not found" }] }), steps: [prompt("one"), prompt("two")], llm: [{ text: "a" }, { text: "b" }] }));
S.push(scenario({ name: "ingest-disable-compression-env", description: "LANGSMITH_DISABLE_RUN_COMPRESSION=true with a gzip-capable server (no compression in this SDK path; wire only).", env: { LANGSMITH_DISABLE_RUN_COMPRESSION: "true" }, routes: routes({ info: info({ instance_flags: { gzip_body_enabled: false } }) }), steps: [prompt("hello")], llm: [{ text: "ok" }] }));

// ---- profiles -----------------------------------------------------------------------------------------------
const PROFILE_ENV = { TRACE_TO_LANGSMITH: "true", LANGSMITH_CONFIG_FILE: "ls-profile.json" };
S.push(scenario({
  name: "profile-api-key", description: "No API key in the environment: the CLI profile (LANGSMITH_CONFIG_FILE) supplies api_key, api_url and workspace.",
  noEnv: true, env: PROFILE_ENV,
  files: { "ls-profile.json": JSON.stringify({ current_profile: "dev", profiles: { dev: { api_key: "profile-key", api_url: ls, workspace_id: "pw-1" } } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "profile-oauth-fresh-token", description: "Profile with an unexpired OAuth access token: Authorization: Bearer, no refresh.",
  noEnv: true, env: PROFILE_ENV,
  files: { "ls-profile.json": JSON.stringify({ current_profile: "dev", profiles: { dev: { api_url: ls, oauth: { access_token: "tok-live", refresh_token: "ref-1", expires_at: "2999-01-01T00:00:00.000Z" } } } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "profile-oauth-refresh", description: "Expired OAuth token: the SDK discovers the token endpoint (404 metadata -> <api_url>/oauth/token), refreshes, sends the new Bearer token and rewrites the profile file.",
  noEnv: true, env: PROFILE_ENV,
  files: { "ls-profile.json": JSON.stringify({ current_profile: "dev", profiles: { dev: { api_url: ls, oauth: { access_token: "tok-old", refresh_token: "ref-1", expires_at: "2000-01-01T00:00:00.000Z" } } } }) },
  routes: routes({ extra: [{ method: "POST", path: "/oauth/token", json: { access_token: "tok-new", refresh_token: "ref-2", expires_in: 3600 } }] }),
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "profile-oauth-refresh-rejected", description: "The token endpoint answers 400: the stale token stays and the run is sent with it.",
  noEnv: true, env: PROFILE_ENV,
  files: { "ls-profile.json": JSON.stringify({ current_profile: "dev", profiles: { dev: { api_url: ls, oauth: { access_token: "tok-old", refresh_token: "ref-1", expires_at: "2000-01-01T00:00:00.000Z" } } } }) },
  routes: routes({ failures: [{ route: "token", status: 400, body: "bad refresh" }] }),
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "profile-env-key-wins", description: "An API key in the environment is used instead of the profile's OAuth credentials.",
  env: { ...PROFILE_ENV, LANGSMITH_PI_API_KEY: "env-key" },
  files: { "ls-profile.json": JSON.stringify({ current_profile: "dev", profiles: { dev: { api_url: ls, api_key: "profile-key", oauth: { access_token: "tok" } } } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));

// ---- compaction, abort ----------------------------------------------------------------------------------------
S.push(scenario({
  name: "compaction-manual", description: "A manual compaction after two turns: session_compact -> 'Context Compaction (manual)' run.",
  llm: [{ text: "first" }, { text: "second" }, { text: "Summary of the conversation so far." }],
  steps: [prompt("one"), prompt("two"), { rpc: { type: "compact" } , wait: "response", settleMs: 1500 }],
}));
S.push(scenario({
  name: "abort-running-tool", description: "The run is aborted while a bash tool runs: the open tool/llm/turn runs are ended with the extension's error text.",
  llm: [{ toolCalls: [bash("sleep 30")] }, { text: "never" }],
  steps: [{ rpc: { type: "prompt", message: "sleep" }, wait: "response", settleMs: 2000 }, { rpc: { type: "abort" }, wait: "agent_end" }],
}));

// ---- wire-only (mockls) ---------------------------------------------------------------------------------------
W.push(scenario({ name: "wire-gzip", description: "gzip_body_enabled: the multipart body is gzip-encoded.", routes: routes({ info: info({ instance_flags: { gzip_body_enabled: true } }) }), steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-gzip-tools", description: "gzip with a tool call and several parts.", files: { "n.txt": "x\n" }, routes: routes({ info: info({ instance_flags: { gzip_body_enabled: true } }) }), llm: [{ toolCalls: [read("n.txt")] }, { text: "ok" }], steps: [prompt("read n.txt")] }));
W.push(scenario({ name: "wire-buffered-multipart", description: "LANGSMITH_DISABLE_MULTIPART_STREAMING=true: the multipart body is buffered (content-length, not chunked).", env: { LANGSMITH_DISABLE_MULTIPART_STREAMING: "true" }, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-422-buffered-retry", description: "A 422 on the streamed multipart upload: the SDK warns and retries buffered; streaming stays off afterwards.", routes: routes({ failures: [{ route: "multipart", status: 422, times: 1, body: "chunked not supported" }] }), steps: [prompt("one"), prompt("two")], llm: [{ text: "a" }, { text: "b" }] }));
W.push(scenario({ name: "wire-429-retry", description: "429 with Retry-After on the multipart endpoint: the caller waits and retries the same body.", routes: routes({ failures: [{ route: "multipart", status: 429, times: 1, headers: { "Retry-After": "1" }, body: "slow down" }] }), tailMs: 4000, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-500-retries", description: "Two 500s then success: the caller's exponential backoff retries.", routes: routes({ failures: [{ route: "multipart", status: 500, times: 2, body: "oops" }] }), tailMs: 9000, steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-failed-traces-dir", description: "A permanently failing server with LANGSMITH_FAILED_TRACES_DIR set: requests after retries (wire) and the fallback write.", env: { LANGSMITH_FAILED_TRACES_DIR: "failed-traces" }, routes: routes({ failures: [{ route: "multipart", status: 400, body: "bad request" }] }), steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-batch-gzip-ignored", description: "use_multipart_endpoint=false with gzip_body_enabled: /runs/batch carries no gzip.", routes: routes({ info: info({ batch_ingest_config: { use_multipart_endpoint: false }, instance_flags: { gzip_body_enabled: true } }) }), steps: [prompt("hello")], llm: [{ text: "ok" }] }));
W.push(scenario({ name: "wire-unicode-payload", description: "Non-ASCII prompt and tool args: part lengths count UTF-8 bytes; escapes follow JSON.stringify.", llm: [{ text: "Grüße 🌍 — done \u2028 line" }], steps: [prompt("héllo wörld 😀 \"quoted\" \\ back")] }));
W.push(scenario({ name: "wire-many-tools", description: "Several tool calls over several turns, one per turn (bigger batch, merged create+update). Parallel tool calls in one turn are left out: Pi and PiG may finish them in different orders.", files: { "a.txt": "A\n", "b.txt": "B\n", "c.txt": "C\n" }, llm: [{ toolCalls: [read("a.txt")] }, { toolCalls: [read("b.txt")] }, { toolCalls: [read("c.txt")] }, { text: "all read" }], steps: [prompt("read all")] }));

const homeConfig = JSON.stringify({ enabled: true, api_url: "{{server:ls}}", api_key: "home-key", project: "home-proj", metadata: { a: "home", b: "home" } });
W.push(scenario({
  name: "config-home-file", description: "User-level $HOME/.pi/langsmith.json, overridden by the workspace file for the keys both set. The file is written by a setup command under the lane's HOME (pigeq's homeFiles would put it in PIG_HOME on a pig lane, where the original, which reads process.env.HOME, cannot see it): every lane sees it at the same path.",
  noEnv: true,
  setup: [["sh", "-c", `mkdir -p "$HOME/.pi" && printf '%s' '${homeConfig}' > "$HOME/.pi/langsmith.json"`]],
  files: { ".pi/langsmith.json": JSON.stringify({ project: "ws-proj", metadata: { b: "ws" } }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));


// ---- configuration fallbacks (config.ts getVar: LANGSMITH_PI_X ?? LANGSMITH_X) ------------------------------------
S.push(scenario({
  name: "config-pi-prefix-wins", description: "LANGSMITH_PI_* beats LANGSMITH_* for api key, endpoint, project and metadata when both are set (the plain endpoint is a dead address).",
  env: { LANGSMITH_PI_PROJECT: "pi-proj", LANGSMITH_PROJECT: "plain-proj", LANGSMITH_PI_METADATA: JSON.stringify({ src: "pi" }), LANGSMITH_METADATA: JSON.stringify({ src: "plain" }), LANGSMITH_ENDPOINT: "http://127.0.0.1:9", LANGSMITH_API_KEY: "plain-key" },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-metadata-fallback", description: "LANGSMITH_METADATA (no PI_) is the metadata fallback; LANGSMITH_PROJECT the project fallback.",
  noEnv: true, env: { TRACE_TO_LANGSMITH: "true", LANGSMITH_ENDPOINT: ls, LANGSMITH_API_KEY: "plain-key", LANGSMITH_PROJECT: "plain-proj", LANGSMITH_METADATA: JSON.stringify({ src: "plain", n: 1 }) },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-langchain-endpoint-key", description: "No LANGSMITH_* endpoint or key at all: the SDK's own LANGCHAIN_ENDPOINT / LANGCHAIN_API_KEY fallbacks supply them (the extension passes undefined).",
  noEnv: true, env: { TRACE_TO_LANGSMITH: "true", LANGCHAIN_ENDPOINT: ls, LANGCHAIN_API_KEY: "lc-key" },
  steps: [prompt("hello")], llm: [{ text: "ok" }],
}));
S.push(scenario({
  name: "config-empty-project", description: "LANGSMITH_PI_PROJECT set to the empty string: `??` keeps it, so the project is \"\" (not the default).",
  env: { LANGSMITH_PI_PROJECT: "" }, steps: [prompt("hello")], llm: [{ text: "ok" }],
}));

// ---- the model side: custom providers served by a pigeq `model` server (provider-native payloads, errors) -------
const SSE = { "Content-Type": "text/event-stream" };
const modelsJson = (provider, api, id, baseSuffix = "") => JSON.stringify({
  providers: { [provider]: { baseUrl: `{{server:model}}${baseSuffix}`, api, apiKey: "eq-key", models: [{ id, name: id, reasoning: false, input: ["text"], contextWindow: 100000, maxTokens: 4096, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } },
});
const modelScenario = (o) => scenario({
  noLlm: true,
  agentFiles: { "models.json": modelsJson(o.provider, o.api, o.id, o.baseSuffix), ...(o.rest.agentFiles ?? {}) },
  args: ["--provider", o.provider, "--model", o.id],
  servers: { model: { routes: o.modelRoutes } },
  ...o.rest,
  agentFiles: { "models.json": modelsJson(o.provider, o.api, o.id, o.baseSuffix), ...(o.rest.agentFiles ?? {}) },
});
// OpenAI chat completions
const chatChunk = (delta, finish = null) => `data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m-1", choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`;
const chatSse = ({ text = "", toolCalls = [] }) =>
  chatChunk({ role: "assistant", content: "" }) + (text ? chatChunk({ content: text }) : "") +
  toolCalls.map((c, j) => chatChunk({ tool_calls: [{ index: j, id: `call_${j}`, type: "function", function: { name: c.name, arguments: JSON.stringify(c.arguments) } }] })).join("") +
  chatChunk({}, toolCalls.length ? "tool_calls" : "stop") +
  `data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m-1", choices: [], usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 } })}\n\ndata: [DONE]\n\n`;
const chatRoute = (o) => ({ method: "POST", path: "/v1/chat/completions", headers: SSE, ...o });
// Gemini streamGenerateContent
const gemChunk = (parts, finish = "STOP") => `data: ${JSON.stringify({ candidates: [{ content: { role: "model", parts }, finishReason: finish, index: 0 }], usageMetadata: { promptTokenCount: 5, candidatesTokenCount: 3, totalTokenCount: 8 }, modelVersion: "m-1" })}\r\n\r\n`;
// OpenAI Responses
const respEvent = (type, data) => `event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`;
const respUsage = { input_tokens: 9, output_tokens: 4, total_tokens: 13, input_tokens_details: { cached_tokens: 2 }, output_tokens_details: { reasoning_tokens: 0 } };
const respText = (text, id = "msg_1") => {
  const item = { type: "message", id, status: "completed", role: "assistant", content: [{ type: "output_text", text, annotations: [] }] };
  return respEvent("response.created", { response: { id: "resp_1", object: "response", status: "in_progress", model: "m-1", output: [] } }) +
    respEvent("response.output_item.added", { output_index: 0, item: { type: "message", id, status: "in_progress", role: "assistant", content: [] } }) +
    respEvent("response.content_part.added", { item_id: id, output_index: 0, content_index: 0, part: { type: "output_text", text: "", annotations: [] } }) +
    respEvent("response.output_text.delta", { item_id: id, output_index: 0, content_index: 0, delta: text }) +
    respEvent("response.output_text.done", { item_id: id, output_index: 0, content_index: 0, text }) +
    respEvent("response.content_part.done", { item_id: id, output_index: 0, content_index: 0, part: item.content[0] }) +
    respEvent("response.output_item.done", { output_index: 0, item }) +
    respEvent("response.completed", { response: { id: "resp_1", object: "response", status: "completed", model: "m-1", output: [item], usage: respUsage } });
};
const respCall = (name, args, id = "fc_1", callId = "call_1") => {
  const argStr = JSON.stringify(args);
  const item = { type: "function_call", id, call_id: callId, name, arguments: argStr, status: "completed" };
  return respEvent("response.created", { response: { id: "resp_1", object: "response", status: "in_progress", model: "m-1", output: [] } }) +
    respEvent("response.output_item.added", { output_index: 0, item: { type: "function_call", id, call_id: callId, name, arguments: "", status: "in_progress" } }) +
    respEvent("response.function_call_arguments.delta", { item_id: id, output_index: 0, delta: argStr }) +
    respEvent("response.function_call_arguments.done", { item_id: id, output_index: 0, arguments: argStr }) +
    respEvent("response.output_item.done", { output_index: 0, item }) +
    respEvent("response.completed", { response: { id: "resp_1", object: "response", status: "completed", model: "m-1", output: [item], usage: respUsage } });
};

S.push(modelScenario({
  provider: "gem", api: "google-generative-ai", id: "gem-1",
  modelRoutes: [
    { method: "POST", path: "/*", headers: SSE, times: 1, body: gemChunk([{ functionCall: { name: "read", args: { path: "notes.txt" } } }]) },
    { method: "POST", path: "/*", headers: SSE, body: gemChunk([{ text: "The file has three lines." }]) },
  ],
  rest: { name: "payload-gemini-tool-call", description: "Gemini provider payload (contents/parts: text, functionCall, functionResponse -> messages) through a tool call and a final answer.", files: { "notes.txt": "alpha\nbeta\ngamma\n" }, steps: [prompt("What is in notes.txt?")] },
}));
S.push(modelScenario({
  provider: "oair", api: "openai-responses", id: "resp-1", baseSuffix: "/v1",
  modelRoutes: [
    { method: "POST", path: "/v1/responses", headers: SSE, times: 1, body: respCall("read", { path: "notes.txt" }) },
    { method: "POST", path: "/v1/responses", headers: SSE, body: respText("The file has three lines.") },
  ],
  rest: { name: "payload-openai-responses-tool-call", description: "OpenAI Responses payload (`input` items, function_call / function_call_output -> messages) through a tool call and a final answer.", files: { "notes.txt": "alpha\nbeta\ngamma\n" }, steps: [prompt("What is in notes.txt?")] },
}));
// Anthropic messages
const antEvent = (type, data) => `event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`;
const antStart = (id) => antEvent("message_start", { message: { id, type: "message", role: "assistant", model: "m-1", content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 11, output_tokens: 1, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } } });
const antText = (text) => antStart("msg_1") +
  antEvent("content_block_start", { index: 0, content_block: { type: "text", text: "" } }) +
  antEvent("content_block_delta", { index: 0, delta: { type: "text_delta", text } }) +
  antEvent("content_block_stop", { index: 0 }) +
  antEvent("message_delta", { delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 5 } }) +
  antEvent("message_stop", {});
const antCall = (name, input) => antStart("msg_1") +
  antEvent("content_block_start", { index: 0, content_block: { type: "tool_use", id: "toolu_1", name, input: {} } }) +
  antEvent("content_block_delta", { index: 0, delta: { type: "input_json_delta", partial_json: JSON.stringify(input) } }) +
  antEvent("content_block_stop", { index: 0 }) +
  antEvent("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 7 } }) +
  antEvent("message_stop", {});
S.push(modelScenario({
  provider: "ant", api: "anthropic-messages", id: "claude-x",
  modelRoutes: [
    { method: "POST", path: "/*", headers: SSE, times: 1, body: antCall("read", { path: "notes.txt" }) },
    { method: "POST", path: "/*", headers: SSE, body: antText("The file has three lines.") },
  ],
  rest: { name: "payload-anthropic-tool-call", description: "Anthropic payload (system blocks, tool_use, a tool_result user message relabelled as a tool message) through a tool call and a final answer.", files: { "notes.txt": "alpha\nbeta\ngamma\n" }, steps: [prompt("What is in notes.txt?")] },
}));
S.push(modelScenario({
  provider: "oai", api: "openai-completions", id: "m-1", baseSuffix: "/v1",
  modelRoutes: [chatRoute({ status: 400, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ error: { message: "the fake provider rejects this request", type: "invalid_request_error" } }) })],
  rest: { name: "error-provider-rejects", description: "The provider answers 400: the assistant message ends with stopReason error and its errorMessage; the llm run ends with that error, the turn and the root carry it.", steps: [prompt("hello")] },
}));
S.push(modelScenario({
  provider: "oai", api: "openai-completions", id: "m-1", baseSuffix: "/v1",
  modelRoutes: [chatRoute({ delayMs: 60000, body: chatSse({ text: "never seen" }) })],
  rest: { name: "error-aborted-stream", description: "The run is aborted while the model is still answering: stopReason aborted, root error 'Assistant message was aborted'.", steps: [{ rpc: { type: "prompt", message: "hello" }, wait: "response", settleMs: 1500 }, { rpc: { type: "abort" }, wait: "agent_end" }] },
}));
S.push(modelScenario({
  provider: "oai", api: "openai-completions", id: "m-1", baseSuffix: "/v1",
  modelRoutes: [
    chatRoute({ times: 1, body: chatSse({ text: "first answer" }) }),
    chatRoute({ times: 1, body: chatSse({ text: "second answer" }) }),
    chatRoute({ times: 1, status: 400, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ error: { message: "prompt is too long: 100500 tokens > 100000 maximum", type: "invalid_request_error", param: "messages", code: "context_length_exceeded" } }) }),
    chatRoute({ times: 1, body: chatSse({ text: "Summary of the conversation so far." }) }),
    chatRoute({ body: chatSse({ text: "answer after the retry" }) }),
  ],
  rest: { name: "compaction-overflow", description: "A context overflow error from the provider: Pi compacts automatically (session_compact reason overflow, willRetry true) and retries inside the same agent run. keepRecentTokens is lowered so that the short history has something to summarise.", settleMs: 1500, agentFiles: { "settings.json": JSON.stringify({ compaction: { enabled: true, keepRecentTokens: 1 } }) }, steps: [prompt("one"), prompt("two"), prompt("three")] },
}));
S.push(scenario({
  name: "trace-tool-update", description: "A streaming bash tool: tool_execution_update events become tool_update events on the tool run.",
  llm: [{ toolCalls: [bash("printf one; sleep 1; printf two; sleep 1; printf three")] }, { text: "done" }],
  steps: [prompt("stream output")],
}));
S.push(scenario({
  name: "shutdown-while-running", description: "The host shuts down while a tool runs: session_shutdown ends the open llm/turn/tool runs and the root with 'Pi session shut down before run completed'.",
  llm: [{ toolCalls: [bash("sleep 20")] }, { text: "never" }],
  steps: [{ rpc: { type: "prompt", message: "sleep" }, wait: "response", settleMs: 1500 }],
}));

// Scenarios that must FAIL TO LOAD in every lane (run-lane.sh looks for <name>.expect: an extended regexp the refusal must match).
const F = [];
F.push({ expect: "replicas", sc: scenario({
  name: "config-runs-endpoints-object-form", description: "LANGSMITH_PI_RUNS_ENDPOINTS in the SDK's object form {url: key}: the extension's schema wants an array, so the extension does not load (original: zod error on `replicas`).",
  env: { LANGSMITH_PI_RUNS_ENDPOINTS: JSON.stringify({ [ls]: "obj-key" }) }, steps: [prompt("hello")], llm: [{ text: "ok" }],
}) });
F.push({ expect: "replicas", sc: scenario({
  name: "config-runs-endpoints-fallback-object-form", description: "The same through the unprefixed LANGSMITH_RUNS_ENDPOINTS fallback.",
  env: { LANGSMITH_RUNS_ENDPOINTS: JSON.stringify({ [ls]: "obj-key" }) }, steps: [prompt("hello")], llm: [{ text: "ok" }],
}) });
F.push({ expect: "sampling", sc: scenario({
  name: "config-sampling-rate-out-of-range", description: "LANGSMITH_TRACING_SAMPLING_RATE=2: the Client constructor throws 'LANGSMITH_TRACING_SAMPLING_RATE must be between 0 and 1 if set. Got: 2'.",
  env: { LANGSMITH_TRACING_SAMPLING_RATE: "2" }, steps: [prompt("hello")], llm: [{ text: "ok" }],
}) });

// lsdiff options per scenario (lane/lsdiff-flags.txt, read by run-lane.sh).
const LSDIFF_FLAGS = {
  "replicas-env": "-unordered-drain",
  "replicas-same-project": "-unordered-drain",
  // Pi's Gemini provider names a tool call read_<Date.now()>_<counter>.
  "payload-gemini-tool-call": "-rule read_[0-9]{13}_[0-9]+=>read_<ms>_<n>",
};

// Scenarios that pass on the wire but cannot be a pigeq-native scenario, because the trace holds what a server received and:
//  - numbers inside a decoded JSON body (/runs/batch) are outside pigeq's string normalisation (end_time);
//  - two upstream requests race (the OAuth refresh against the model request; one request per replica);
//  - the abort scenario: the end of an aborted run differs between hosts in the trace body (wire is identical after lsdiff's rules);
//  - the custom-provider scenarios: the model server's recorded request body holds the host's system prompt (Pi's and PiG's differ), and
//    shutdown-while-running makes PiG report an extension_error in its own turn_end handling (wire identical).
// Found by `pigeq record`'s host check (Pi against PiG-Node). They stay lsdiff scenarios (run-lane.sh -s ../scenarios-wire).
const WIRE_ONLY = new Set(["payload-gemini-tool-call", "payload-openai-responses-tool-call", "payload-anthropic-tool-call", "error-provider-rejects", "error-aborted-stream", "compaction-overflow", "shutdown-while-running", "abort-running-tool", "ingest-no-multipart", "ingest-multipart-404-fallback", "profile-oauth-refresh", "profile-oauth-refresh-rejected", "replicas-same-project"]);
for (const sc of [...S]) {
  if (WIRE_ONLY.has(sc.name)) {
    S.splice(S.indexOf(sc), 1);
    W.push(sc);
  }
}

function emit(dir, list) {
  const out = join(portDir, dir);
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  for (const sc of list) {
    writeFileSync(join(out, `${sc.name}.json`), JSON.stringify(sc, null, 2) + "\n");
  }
  console.log(`${dir}: ${list.length} scenarios`);
}
emit("scenarios", S);
emit("scenarios-wire", W);
emit("scenarios-fail", F.map((f) => f.sc));
for (const f of F) writeFileSync(join(portDir, "scenarios-fail", `${f.sc.name}.expect`), f.expect + "\n");
writeFileSync(join(here, "..", "lane", "lsdiff-flags.txt"), Object.entries(LSDIFF_FLAGS).map(([n, f]) => `${n} ${f}\n`).join(""));
