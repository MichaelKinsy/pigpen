// Golden vectors of lane LsExt, produced by the real vendored JavaScript (the npm 0.2.1 build of the extension in
// port/oracle/dist, with its langsmith 0.10.5 and zod 4 dependencies). Nothing here re-implements the code under test.
//
//   LS_CPUS=3 ~/work/ls-tools/ls run "node components/langsmith/port/tools/gen/ext-vectors.mjs"
//
// Output (extensions/langsmith/testdata): ext_config.json, ext_convert.json, ext_remote.json, ext_lower.json,
// ext_expected_trees.json.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const oracle = path.resolve(here, "../../oracle");
// The container's tree is a throw-away copy: run with EXT_OUT=<dir under /home/kinsy/ls-work> and copy the files back.
const testdata = process.env.EXT_OUT || path.resolve(here, "../../../extensions/langsmith/testdata");
const scratch = process.env.EXT_SCRATCH || "/home/kinsy/ls-work/scratch/LsExt";
const runDir = process.env.LS_ORACLE_RUN || "/home/kinsy/ls-work/oracle-run";

fs.mkdirSync(testdata, { recursive: true });
fs.rmSync(path.join(scratch, "oracle"), { recursive: true, force: true });
fs.mkdirSync(path.join(scratch, "oracle"), { recursive: true });
fs.cpSync(path.join(oracle, "dist"), path.join(scratch, "oracle/dist"), { recursive: true });
fs.symlinkSync(path.join(runDir, "node_modules"), path.join(scratch, "oracle/node_modules"));
fs.writeFileSync(path.join(scratch, "oracle/package.json"), '{"type":"module"}');
// The host package is not installed here: the extension only reads its VERSION.
fs.writeFileSync(path.join(scratch, "oracle/pi-stub.js"), 'export const VERSION = "1.5.0";\n');
for (const f of ["index.js", "metadata.js"]) {
  const p = path.join(scratch, "oracle/dist", f);
  let s = fs.readFileSync(p, "utf8");
  s = s.replace(/from "@earendil-works\/pi-coding-agent"/g, 'from "../pi-stub.js"');
  if (f === "index.js") s += "\nexport { convertProviderPayload };\n";
  if (f === "metadata.js") s += "\nexport { parseRemote };\n";
  fs.writeFileSync(p, s);
}
const load = (f) => import(pathToFileURL(path.join(scratch, "oracle/dist", f)).href);
const index = await load("index.js");
const metadata = await load("metadata.js");
const config = await load("config.js");

const write = (name, v) => {
  fs.writeFileSync(path.join(testdata, name), JSON.stringify(v) + "\n");
  console.log("wrote", name);
};

// ── config ──────────────────────────────────────────────────────────────────────────────────────────────────
const configCases = [];
const addConfig = (c) => configCases.push(c);
for (const v of ["1", "true", "TRUE", " yes ", "On", "0", "false", "NO", "off", "", "maybe", "\u00a0true\u00a0", "\ufefftrue", "tru", "2", "truee"]) {
  addConfig({ env: { TRACE_TO_LANGSMITH: v } });
}
addConfig({ env: {} });
addConfig({ env: { TRACE_TO_LANGSMITH: "true", LANGSMITH_PI_API_KEY: "k1", LANGSMITH_API_KEY: "k2", LANGSMITH_PI_ENDPOINT: "http://pi", LANGSMITH_ENDPOINT: "http://ls", LANGSMITH_PI_PROJECT: "p1", LANGSMITH_PROJECT: "p2" } });
addConfig({ env: { TRACE_TO_LANGSMITH: "true", LANGSMITH_API_KEY: "k2", LANGSMITH_ENDPOINT: "http://ls", LANGSMITH_PROJECT: "p2" } });
addConfig({ env: { LANGSMITH_PI_API_KEY: "", LANGSMITH_API_KEY: "k2", LANGSMITH_PI_PROJECT: "" } });
addConfig({ env: { LANGSMITH_PI_METADATA: '{"b":1,"a":{"y":2,"x":[1,2]},"10":"i","2":"j","c":null}' } });
addConfig({ env: { LANGSMITH_PI_METADATA: "  " } });
addConfig({ env: { LANGSMITH_PI_METADATA: "{not json" } });
addConfig({ env: { LANGSMITH_METADATA: '{"fallback":true}' } });
addConfig({ env: { LANGSMITH_PI_METADATA: "{}" } });
addConfig({ env: { LANGSMITH_PI_METADATA: '{"__proto__":1,"k":2}' } });
for (const m of ["null", "[1]", "5", '"x"', "true", "false", "1e400", "-1e400", "0", "-0", "1.5"]) addConfig({ env: { LANGSMITH_PI_METADATA: m } });
addConfig({ env: { LANGSMITH_PI_RUNS_ENDPOINTS: "[]" } });
for (const r of ["1e400", "-1e400", "[1e400]", '[{"api_url":1e400}]', '[{"updates":-1e400}]', '[{"api_key":1e999,"project":-1e999}]']) addConfig({ env: { LANGSMITH_PI_RUNS_ENDPOINTS: r } });
addConfig({ env: { LANGSMITH_PI_RUNS_ENDPOINTS: '[{"api_url":"http://a","api_key":"ka","project":"pa","updates":{"z":1,"a":2}},{"apiUrl":"http://b","apiKey":"kb","projectName":"pb"},{"api_url":null,"apiUrl":"http://c"},{"primary":true,"reroot":true,"workspaceId":"w","api_url":"http://d"},{}]' } });
addConfig({ env: { LANGSMITH_RUNS_ENDPOINTS: '[{"api_url":"http://a"}]' } });
for (const r of ["{}", "null", "7", '"s"', "true", '[null,1,[],{"api_url":3,"updates":[]},{"updates":null},"x",true]', '[{"api_url":"a","api_key":5,"project":false,"updates":"u"}]', '[{"api_url":null,"apiUrl":4}]', '{"https://a":"k"}']) {
  addConfig({ env: { LANGSMITH_PI_RUNS_ENDPOINTS: r } });
}
addConfig({ env: { LANGSMITH_PI_METADATA: "5", LANGSMITH_PI_RUNS_ENDPOINTS: "7", TRACE_TO_LANGSMITH: "true" } });
// files
const fileCase = (home, cwd, env = {}) => addConfig({ home, cwd, env });
const G = (o) => (typeof o === "string" ? o : JSON.stringify(o));
fileCase(G({ enabled: true, api_key: "gk", api_url: "http://g", project: "gp", metadata: { g: 1, shared: "g" }, replicas: [{ api_url: "http://gr" }], extra: 1 }), null);
fileCase(null, G({ enabled: true, api_key: "lk", metadata: { l: 1, shared: "l" } }));
fileCase(G({ enabled: false, api_key: "gk", project: "gp", metadata: { g: 1 } }), G({ enabled: true, api_key: "lk", metadata: { l: 1 } }), { LANGSMITH_PI_PROJECT: "ep", TRACE_TO_LANGSMITH: "false" });
fileCase(G({ enabled: true, api_key: null }), null);
fileCase(G({ enabled: "yes" }), G({ api_key: "lk" }));
fileCase("{not json", G({ enabled: true }));
fileCase("[]", "null");
fileCase("5", "\"x\"");
fileCase(G({ replicas: [{ apiUrl: "http://r", apiKey: "rk", projectName: "rp", updates: { u: 1 } }] }), null);
fileCase(G({ replicas: [null] }), G({ enabled: true }));
fileCase(G({ metadata: [] }), G({ enabled: true }));
fileCase('{"enabled":true,"project":"\\u00e9\\ud83d\\ude00"}', null);
fileCase("\ufeff" + G({ enabled: true }), null);
fileCase(G({ enabled: true, project: "" }), null);
fileCase(G({ metadata: { b: 1, a: 2, 1: "x" } }), G({ metadata: { c: 3 } }));
fileCase(G({ enabled: true, api_key: "gk" }), G({ api_key: "lk" }), { LANGSMITH_PI_API_KEY: "ek" });

const configVectors = [];
for (const c of configCases) {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "ext-cfg-"));
  const home = path.join(base, "home");
  const cwd = path.join(base, "cwd");
  fs.mkdirSync(path.join(home, ".pi"), { recursive: true });
  fs.mkdirSync(path.join(cwd, ".pi"), { recursive: true });
  if (typeof c.home === "string") fs.writeFileSync(path.join(home, ".pi/langsmith.json"), c.home);
  if (typeof c.cwd === "string") fs.writeFileSync(path.join(cwd, ".pi/langsmith.json"), c.cwd);
  const v = { env: c.env || {}, home: c.home ?? null, cwd: c.cwd ?? null };
  try {
    v.result = JSON.stringify(await config.getConfig({ home, cwd, env: v.env }));
  } catch (e) {
    v.error = { name: e.name, message: e.message };
  }
  configVectors.push(v);
  fs.rmSync(base, { recursive: true, force: true });
}
write("ext_config.json", configVectors);

// ── conversion, driven by the four recordings and by hand-written branch cases ─────────────────────────────────
const convertVectors = [];
const add = (fn, input, run) => {
  const v = { fn, input };
  try {
    const out = run(JSON.parse(input));
    v.output = out === undefined ? null : JSON.stringify(out);
    if (out === undefined) v.undefined = true;
  } catch (e) {
    v.error = e.message;
  }
  convertVectors.push(v);
};
const recDir = path.join(oracle, "test/recordings");
for (const f of fs.readdirSync(recDir).sort()) {
  const seen = new Set();
  for (const line of fs.readFileSync(path.join(recDir, f), "utf8").trim().split("\n")) {
    // The line's JSON text is kept so the Go side parses the same key order.
    const arr = JSON.parse(line);
    const ev = arr[1];
    const text = (obj) => JSON.stringify(obj);
    const arg = arr[2];
    if (ev === "before_provider_request") add("convertProviderPayload", text(arg.payload), index.convertProviderPayload);
    if (ev === "message_end" || ev === "turn_end") {
      add("convertMessages", text([arg.message]), index.convertMessages);
      add("extractUsageMetadata", text(arg.message), index.extractUsageMetadata);
    }
    if (ev === "agent_end") add("convertMessages", text(arg.messages), index.convertMessages);
    if (ev === "tool_execution_end") add("convertToolOutputs", text(arg), (e) => index.convertToolOutputs(e));
    seen.add(ev);
  }
}
const P = (o) => JSON.stringify(o);
// OpenAI Responses
add("convertProviderPayload", P({ model: "m", input: [{ role: "user", content: [{ type: "input_text", text: "hi", extra: 1 }, { type: "input_image", image_url: "u" }, "s", null] }, { role: "user", content: "plain" }, { role: "assistant", content: [{ type: "input_text" }] }, { type: "function_call_output", call_id: "c", output: "o" }, "str", 5, null, [{ role: "user", content: [{ type: "input_text", text: "nested" }] }, "x"], { role: "user", content: [] }], stream: true, tools: [] }), index.convertProviderPayload);
add("convertProviderPayload", P({ input: [], messages: null, z: 1 }), index.convertProviderPayload);
add("convertProviderPayload", P({ input: [{ role: "user" }], messages: [{ role: "user", content: "x" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ input: [{ role: "user", content: [{ type: "input_text", text: "t", role: "x", content: 3 }] }], messages: 5 }), index.convertProviderPayload);
// Gemini
add("convertProviderPayload", P({ model: "g", contents: [{ role: "user", parts: [{ text: "hi", thought: true, type: "keep" }, { functionCall: { id: "i", name: "n", args: { a: 1 }, z: 1 }, sig: "s" }, { functionCall: { name: "n2" } }, { functionResponse: { name: "n", response: { r: 1 } }, extra: 2 }, { imageUrl: { url: "u" }, k: 1 }, { inlineData: { data: "d" } }, "p", null, { functionCall: "notrecord", text: 5 }], extra: "e" }, { role: "model", parts: [{ text: "ok" }] }, { role: "tool", parts: [] }, { role: "user" }, { role: 5, parts: [] }, { parts: [] }, "str", null, { role: "constructor", parts: [] }, { role: "__proto__", parts: [] }, { role: "toString", parts: [] }, { role: "hasOwnProperty", parts: [] }], systemInstruction: { parts: [{ text: "sys" }] }, generationConfig: { t: 1 } }), index.convertProviderPayload);
add("convertProviderPayload", P({ contents: [], messages: null }), index.convertProviderPayload);
add("convertProviderPayload", P({ contents: [{ role: "user", parts: [] }], messages: 0 }), index.convertProviderPayload);
// messages (Anthropic and chat completions)
add("convertProviderPayload", P({ model: "a", system: [{ type: "text", text: "sys", cache_control: { type: "ephemeral" } }], messages: [{ role: "user", content: [{ type: "tool_result", tool_use_id: "t1", content: "r1" }, { type: "tool_result", tool_use_id: "t2", content: [{ type: "text", text: "r2" }], is_error: true }], cache: 1 }, { role: "user", content: [{ type: "text", text: "hi" }] }, { role: "user", content: [] }, { role: "assistant", content: [{ type: "tool_use", id: "t1" }] }, "s", null, [{ role: "user", content: "arr" }]], max_tokens: 5 }), (p) => { try { return index.convertProviderPayload(p); } catch (e) { return undefined; } });
add("convertProviderPayload", P({ system: [{ type: "text", text: "a" }, { type: "text", text: "b" }], messages: [{ role: "user", content: "x" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: [{ type: "text", text: "only" }], messages: [{ role: "user", content: "x" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: [{ type: "text", text: "" }], messages: [] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: [{ type: "text", text: 5 }], messages: [] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: "plain", messages: [{ role: "user", content: "x" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: null, messages: [{ role: "user", content: "x" }], z: 1 }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: "s", messages: [{ role: "system", content: "have" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: "s", messages: [{ role: "developer", content: "have" }, null] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: "s", messages: [null, { role: "system" }] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: "s", messages: ["a", 1, true, [1]] }), index.convertProviderPayload);
add("convertProviderPayload", P({ system: 0, messages: [] }), index.convertProviderPayload);
add("convertProviderPayload", P({ messages: [{ role: "user", content: [{ type: "tool_result", content: "c", role: "x", messages: 1 }] }], system: false }), index.convertProviderPayload);
// other shapes
for (const p of [null, 5, "str", true, [1, 2], {}, { model: "m" }, { input: "str" }, { input: null, messages: null }, { messages: "x" }, { contents: "x" }]) {
  add("convertProviderPayload", P(p), index.convertProviderPayload);
}
// messages
const msgs = [
  { role: "user", content: [{ type: "text", text: "t" }, { type: "image", mimeType: "image/png", data: "AAA", extra: 1 }, { type: "image", mimeType: "image/png" }, { type: "image", data: "x" }, { type: "image", mimeType: 1, data: "x" }, { type: "image", mime_type: "k", mimeType: "m", data: "d", base64: "b" }, "s", null, 5] },
  { role: "toolResult", toolName: "read", content: [{ type: "image", mimeType: "image/png", data: "AAA" }], isError: false },
  { role: "assistant", content: [{ type: "toolCall", id: "1", name: "n", arguments: { a: 1 }, extra: 2 }, { type: "toolCall" }, { type: "toolCall", args: "mine", arguments: 5 }], z: 1 },
  { role: "user", content: "str" },
  { role: "user" },
  { content: [] },
  { role: "assistant", content: null },
  { role: "x", content: { type: "toolCall" }, "5": 1, "1": 2, b: 3, a: 4 },
];
for (const m of msgs) add("convertMessages", P([m]), index.convertMessages);
add("convertMessages", P([]), index.convertMessages);
add("convertMessages", P(msgs), index.convertMessages);
// tool outputs
for (const r of [
  { result: { content: [{ type: "text", text: "t" }, { type: "image", mimeType: "i", data: "d" }], details: { a: 1 } } },
  { result: { content: "files" } },
  { result: { content: null, x: 1 } },
  { result: { x: 1 } },
  { result: { role: "r", content: [], z: 1 } },
  { result: "ok" },
  { result: [1] },
  { result: null },
  {},
  { result: { content: 0 } },
  { result: { content: false, a: 1 } },
]) add("convertToolOutputs", P(r), (e) => index.convertToolOutputs(e));
// usage
const usages = [
  { role: "assistant", usage: { input: 2, output: 246, cacheRead: 2755, cacheWrite: 98, totalTokens: 3101, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } },
  { role: "assistant", usage: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4, totalTokens: 10, cost: { input: 0.1, output: 0.2, cacheRead: 0.3, cacheWrite: 0.4, total: 1 } } },
  { role: "assistant", usage: { input: 1, output: 2, totalTokens: 3, cost: { total: 0.5 } } },
  { role: "assistant", usage: { totalTokens: 0, cost: { total: 0 } } },
  { role: "assistant", usage: {} },
  { role: "assistant" },
  { role: "assistant", usage: null },
  { role: "assistant", usage: 5 },
  { role: "assistant", usage: { input: null, cacheRead: null, cacheWrite: null, output: null, totalTokens: null, cost: null } },
  { role: "assistant", usage: { input: 0, output: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } },
  { role: "assistant", usage: { totalTokens: 5, cost: { input: 1, total: 0 } } },
  { role: "assistant", usage: { totalTokens: 5 } },
  { role: "assistant", usage: { totalTokens: -1, cost: { total: 0 } } },
  { role: "assistant", usage: { input: 0.1, cacheRead: 0.2, totalTokens: 1, cost: { total: 0.1, input: 0.1, cacheRead: 0.2 } } },
  { role: "user", usage: { totalTokens: 5 } },
  { role: "toolResult" },
  {},
  null,
];
for (const u of usages) add("extractUsageMetadata", P(u), index.extractUsageMetadata);
write("ext_convert.json", convertVectors);

// ── git remotes ─────────────────────────────────────────────────────────────────────────────────────────────
const remotes = [
  "https://github.com/langchain-ai/langsmith-pi-extension.git",
  "https://github.com/langchain-ai/langsmith-pi-extension",
  "https://github.com/a/b/",
  "https://github.com//a/b.git",
  "http://GitHub.com/A/B.git",
  "https://user:pw@gitlab.example.com:8443/group/sub/repo.git",
  "https://gitlab.com:443/group/repo.git",
  "http://example.com:80/x.git",
  "http://example.com:81/x.git",
  "http://example.com:0081/x.git",
  "http://example.com:65536/x.git",
  "http://example.com:abc/x.git",
  "http://example.com:/x.git",
  "ssh://git@github.com/org/repo.git",
  "ssh://git@github.com:22/org/repo.git",
  "ssh://git@github.com:2222/org/repo.git",
  "ssh://git@github.com:0/org/repo.git",
  "ssh://git@bitbucket.org/team/repo",
  "git://example.org/pub/scm/repo.git",
  "git+ssh://git@host.xz/path/to/repo.git",
  "git@github.com:org/repo.git",
  "git@gitlab.com:group/sub/repo.git",
  "user@host.example:path/to/repo",
  "me@bitbucket.org:team/repo.git",
  "git@github.com:/org/repo.git",
  "git@github.com:",
  "git@:repo",
  "@host:repo",
  "a@b@c:repo",
  "a/b@c:repo",
  "git@host/x:repo",
  "git@github.com:org/repo.git/",
  "file:///srv/git/repo.git",
  "file://localhost/srv/git/repo.git",
  "file://server/share/repo.git",
  "/srv/git/repo.git",
  "../relative/repo.git",
  "C:\\repos\\proj.git",
  "c:/repos/proj",
  "ssh://github.com",
  "ssh://github.com/",
  "https://github.com",
  "https://github.com/",
  "https://github.com/.git",
  "https://github.com/a b/c d.git",
  "https://github.com/a%20b/c.git",
  "https://github.com/a/../b/./c.git",
  "https://github.com/a/%2e%2e/b",
  "https://github.com/a/b?x=1#frag",
  "https://github.com/a/b.git?x=1",
  "https://github.com\\a\\b.git",
  "https://github.com/ü/é.git",
  "https://exämple.com/a/b",
  "https://EXAMPLE.COM./a/b",
  "https://[::1]/a/b",
  "https://[2001:db8::1]:8443/a/b",
  "https://[::ffff:1.2.3.4]/a",
  "https://127.0.0.1/a/b",
  "https://0x7f.1/a/b",
  "https://1.2.3.4.5/a/b",
  "https://300.1.1.1/a",
  "https://a b/c",
  "https:///github.com/a/b",
  "https:github.com/a/b",
  "https:/github.com/a/b",
  "ssh:/github.com/a/b",
  "ssh:github.com/a/b",
  "ssh://:22/a",
  "ssh://u@/a",
  "https://@github.com/a/b",
  "https://u@/a",
  "ftp://ftp.example.com/pub/repo.git",
  "ws://h/x",
  "wss://h:443/x",
  "ssh://user@host with space/x",
  "ssh://Host.Example/x",
  "ssh://ho%41st/x",
  "https://ho%41st/x",
  "weird://h/p^q`r{s}t|u",
  "https://h/p^q`r{s}t|u\"v<w>x",
  "https://h/\t\nx/y",
  "  https://github.com/a/b.git  ",
  "x",
  "http://",
  "ssh://git@github.com:org/repo.git",
  "git@github.com:org/repo.git\u2028x",
  "https://h/a/b.git.git",
  "https://h/a/.git.git",
  "https://h/../..",
  "https://h/a/..",
  "https://h/a/.",
  "https://h/%2E",
  "ssh://h/..",
  "gitlab://x/y",
  "file://C:/repos/proj.git",
  "file://D:\\repos\\proj.git",
  "file://c|/repos/p",
  "file://c:\\a",
  "file://C:",
  "file://C:x/y",
  "file://server:1/x/y.git",
  "file://u@server/x/y.git",
  "file://server/x/y.git",
  "file://SERVER/X/Y.git",
  "file://server/C:/x/y.git",
  "file://server/C|/x/../../y.git",
  "file://server/C:/../y.git",
  "file://localhost/srv/x.git",
  "file://LOCALHOST/srv/x.git",
  "file:///C:/repos/p.git",
  "file:C:/x/y.git",
  "file:/srv/a/b.git",
  "file:srv/a.git",
  "file://",
  "file://server",
  "file://server/",
  "file://ser%76er/x/y.git",
  "file://[::1]/x/y.git",
  "file://a b/x",
  "file://1.2.3.4/x/y.git",
  "file:\\\\server\\x\\y.git",
  "file:////server/x/y.git",
  "file://:80/x",
  "file://@/x",
  "file://server/../x/y.git",
  "file://xn--/x/y.git",
  "file://é/x/y.git",
];
const remoteVectors = [];
for (const remote of remotes) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ext-git-"));
  const run = (...a) => execFileSync("git", a, { cwd: dir, stdio: "ignore" });
  const v = { remote };
  try {
    run("init", "-q");
    run("config", "user.email", "a@b.c");
    run("config", "user.name", "n");
    run("config", "commit.gpgsign", "false");
    run("config", "remote.origin.url", remote);
    run("commit", "-q", "--allow-empty", "-m", "m");
    const m = metadata.codingAgentMetadata({ agentType: "root", cwd: dir });
    v.repository = {
      repository_url: m.repository_url ?? null,
      repository_provider: m.repository_provider ?? null,
      repository_name: m.repository_name ?? null,
    };
    // parseRemote on the remote exactly as git returns it (trimmed).
    const got = execFileSync("git", ["config", "--get", "remote.origin.url"], { cwd: dir, encoding: "utf8" }).trim();
    v.stored = got;
    const parsed = metadata.parseRemote(got);
    v.parsed = parsed === undefined ? null : parsed;
  } catch (e) {
    v.skipped = String(e.message).split("\n")[0];
  }
  fs.rmSync(dir, { recursive: true, force: true });
  remoteVectors.push(v);
}
write("ext_remote.json", remoteVectors);

// ── lower-casing ────────────────────────────────────────────────────────────────────────────────────────────
const lowers = [
  "Claude Opus 4.8", "GPT-5.5", "İSTANBUL", "İ", "ΑΣ", "ΑΣ ΑΣ", "Σ", "ΑΣΑ", "ΑΣ.", "Α.Σ", "Α.Σ.Β", "ΑΣ'Β", "ΑΣ\u0301", "ΑΣ\u0301Β", "ὈΔΥΣΣΕΎΣ", "Σ Σ", "ΑΣ1", "1Σ", "ΑΣ\u00ad", "ΑΣ\u00adΒ", "ǅ", "ẞ", "K", "Å", "Ⅷ", "ⓐⒶ", "ΑΣ\u2019Β", "Aı", "ΑΣ\u200b", "ΑΣ\u200bΒ", "ªΣ", "ᵃΣ", "Σ\u02b0", "𐐀𐐁", "ΑΣ𝐀", "ß", "Ǆǅǆ", "ΣΑ", "ΧΡΌΝΟΣ", "ΧΡΌΝΟΣ.", "ΧΡΌΝΟΣ ΧΡΌΝΟΣ",
];
write("ext_lower.json", lowers.map((s) => ({ in: s, out: s.toLowerCase(), locale: s.toLocaleLowerCase() })));

// ── expected trees of the upstream tests ────────────────────────────────────────────────────────────────────
// The tests' asTree blocks are evaluated with asymmetric matchers recorded as markers.
const expectStub = {
  stringContaining: (s) => ({ $sc: s }),
  objectContaining: (o) => ({ $oc: o }),
  arrayContaining: (a) => ({ $ac: a }),
};
function asTree(cb) {
  const acc = { nodes: [], edges: [], data: {} };
  const run = (rawId) => {
    const id = rawId.join("");
    const name = id.split(":")[0];
    acc.nodes.push(id);
    return (props, ...children) => {
      for (const c of children) acc.edges.push([id, c]);
      acc.data[id] = { name, ...props };
      return id;
    };
  };
  cb(run);
  const order = new Map(acc.nodes.map((id, i) => [id, i]));
  return { ...acc, edges: [...acc.edges].sort(([, l], [, r]) => (order.get(l) ?? 0) - (order.get(r) ?? 0)) };
}
const trees = {};
for (const file of ["trace.test.ts", "late-settle.test.ts", "legacy-runtime.test.ts"]) {
  const src = fs.readFileSync(path.join(oracle, "test", file), "utf8");
  const re = /^it\("((?:[^"\\]|\\.)*)"[\s\S]*?const expected = (asTree\(\(run\) => \{[\s\S]*?\n  \}\));/gm;
  let m;
  while ((m = re.exec(src))) {
    const title = JSON.parse(`"${m[1]}"`);
    trees[title] = new Function("asTree", "expect", `return ${m[2]};`)(asTree, expectStub);
  }
}
write("ext_expected_trees.json", trees);
