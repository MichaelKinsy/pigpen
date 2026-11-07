// run_trees.js RunTree golden vectors (lane LsRunTree, langsmith 0.10.5).
//
// Runs the real vendored RunTree over the scenarios below with a recording fake Client and writes
//   extensions/langsmith/testdata/runtree_cases.json   the scenarios (the Go test replays the same file)
//   extensions/langsmith/testdata/runtree_golden.json  what the JS produced: every createRun/updateRun call
//                                                      (JSON.stringify of the payload), console.warn/error text,
//                                                      thrown errors and the final state of every run
// Run: ls run "RUNTREE_OUT=/home/kinsy/ls-work/scratch/runtree node components/langsmith/port/tools/gen/runtree-golden.mjs"
// then copy the two files from that directory into extensions/langsmith/testdata.
//
// Determinism: Date.now()/new Date() and crypto.getRandomValues are stubbed. The JS generates run ids with random
// low bits; the Go test normalises generated ids (`<time>-7000-8000-<random>`), declared ids are fixed.
import fs from "node:fs";
import path from "node:path";
import { load, testdata } from "./wire-common.mjs";

const { RunTree } = await load("run_trees.js");
const warnMod = await load("utils/warn.js");
const envMod = await load("utils/env.js");

// ---------------------------------------------------------------- scenarios
const T0 = 1700000000000;
const id = (n) => `0194ab00-0000-7abc-8abc-${String(n).padStart(12, "0")}`;
const root = (as, config, extra = {}) => ({ op: "root", as, config, ...extra });
const child = (of, as, config, extra = {}) => ({ op: "child", of, as, config, ...extra });
const post = (run, extra = {}) => ({ op: "post", run, ...extra });
const patch = (run, extra = {}) => ({ op: "patch", run, ...extra });
const end = (run, extra = {}) => ({ op: "end", run, ...extra });
const event = (run, extra) => ({ op: "event", run, ...extra });
const now = (ms) => ({ op: "now", ms });
const base = (n, extra = {}) => ({
  id: id(n),
  start_time: T0 + n * 1000,
  name: `run ${n}`,
  project_name: "proj",
  ...extra,
});
const tree = (extra = {}) => [
  root("r", base(1, { inputs: { prompt: "hi", imageCount: 0 }, metadata: { a: 1, b: "two" }, tags: ["pi", "coding-agent"], ...extra })),
  child("r", "c1", { id: id(2), start_time: T0 + 2000, name: "child 1", run_type: "llm", inputs: { x: 1 }, metadata: { b: "child", c: 3 } }),
  child("c1", "g1", { id: id(3), start_time: T0 + 3000, name: "grandchild", run_type: "tool", inputs: { y: 2 } }),
  child("r", "c2", { id: id(4), start_time: T0 + 4000, name: "child 2", run_type: "tool", inputs: { z: 3 } }),
];
const lifecycle = (...runs) =>
  runs.flatMap((r, i) => [post(r), now(T0 + 10000 + i * 100), end(r, { outputs: { done: r }, endTime: T0 + 20000 + i }), patch(r)]);

const replicaA = { apiUrl: "https://a.example", apiKey: "ka", projectName: "proj-a" };
const replicaB = { apiUrl: "https://b.example", apiKey: "kb", projectName: "proj-b", workspaceId: "ws-b", updates: { tags: ["replica-b"], extra: { x: 1 } } };

export const cases = [
  { name: "root basic", now: T0, steps: [root("r", base(1, { inputs: { prompt: "hi" }, metadata: { k: "v" }, tags: ["pi"] })), ...lifecycle("r")] },
  { name: "root defaults", now: T0, steps: [root("r", { id: id(1), name: "" }), post("r"), patch("r")] },
  { name: "root defaults without id", now: T0, steps: [root("r", { name: "gen" }), post("r"), end("r", { outputs: { a: 1 } }), patch("r")] },
  { name: "default project LANGSMITH_PROJECT", now: T0, env: { LANGSMITH_PROJECT: "from-ls", LANGCHAIN_PROJECT: "from-lc", LANGCHAIN_SESSION: "sess" }, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "default project LANGCHAIN_PROJECT", now: T0, env: { LANGCHAIN_PROJECT: "from-lc", LANGCHAIN_SESSION: "sess" }, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "default project LANGCHAIN_SESSION", now: T0, env: { LANGCHAIN_SESSION: "sess" }, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "default project empty LANGSMITH_PROJECT", now: T0, env: { LANGSMITH_PROJECT: "", LANGCHAIN_SESSION: "sess" }, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "default project empty LANGCHAIN_PROJECT", now: T0, env: { LANGCHAIN_PROJECT: "", LANGCHAIN_SESSION: "sess" }, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "default project none", now: T0, steps: [root("r", { id: id(1), name: "p" }), post("r")] },
  { name: "explicit empty project", now: T0, env: { LANGSMITH_PROJECT: "x" }, steps: [root("r", { id: id(1), name: "p", project_name: "" }), post("r"), patch("r")] },
  { name: "inputs null", now: T0, steps: [root("r", base(1, { inputs: null })), post("r"), patch("r", { excludeInputs: false })] },
  {
    name: "extra runtime library kept",
    now: T0,
    steps: [root("r", base(1, { extra: { runtime: { library: "mine", sdk: "x" }, other: 1 }, metadata: { m: 1 } })), post("r"), patch("r")],
  },
  {
    name: "extra runtime merged by falsy keys",
    now: T0,
    steps: [root("r", base(1, { extra: { runtime: { runtime: "", sdk: "keep", extra_key: 0 } } })), post("r"), patch("r")],
  },
  { name: "extra runtime null", now: T0, steps: [root("r", base(1, { extra: { runtime: null } })), post("r")] },
  { name: "extra runtime false", now: T0, steps: [root("r", base(1, { extra: { runtime: false } })), post("r")] },
  { name: "extra runtime array", now: T0, steps: [root("r", base(1, { extra: { runtime: [1] } })), post("r")] },
  {
    name: "metadata dedupe",
    now: T0,
    steps: [
      root("r", base(1, { metadata: { a: 1, b: 2, "2": "x", "1": "y" }, extra: { metadata: { b: 3, c: 4 }, k: "kv" } })),
      post("r"),
      patch("r"),
    ],
  },
  { name: "extra only", now: T0, steps: [root("r", base(1, { extra: { first: 1, metadata: { m: 1 }, last: 2 } })), post("r"), patch("r")] },
  { name: "tags empty", now: T0, steps: [root("r", base(1, { tags: [] })), post("r"), patch("r")] },
  { name: "children tree", now: T0, steps: [...tree(), ...lifecycle("r", "c1", "g1", "c2")] },
  {
    name: "children generated ids",
    now: T0,
    steps: [
      root("r", { name: "root", project_name: "proj", metadata: { a: 1 } }),
      child("r", "c1", { name: "c1" }),
      now(T0 + 5),
      child("c1", "g1", { name: "g1", run_type: "tool" }),
      child("r", "c2", { name: "c2" }),
      ...lifecycle("r", "c1", "g1", "c2"),
    ],
  },
  {
    name: "metadata inheritance",
    now: T0,
    steps: [
      root("r", base(1, { metadata: { p1: "root", shared: "root", n: { deep: 1 } } })),
      child("r", "c1", { id: id(2), name: "c1", metadata: { shared: "child", own: 1 } }),
      child("c1", "g1", { id: id(3), name: "g1", metadata: { own: "g" } }),
      child("r", "c2", { id: id(4), name: "c2" }),
      child("r", "c3", { id: id(5), name: "c3", extra: { metadata: { fromExtra: 1 }, k: 1 }, metadata: { shared: "meta" } }),
      post("r"), post("c1"), post("g1"), post("c2"), post("c3"),
    ],
  },
  {
    name: "metadata inheritance empty parent",
    now: T0,
    steps: [root("r", base(1)), child("r", "c1", { id: id(2), name: "c1", metadata: { own: 1 } }), child("c1", "g1", { id: id(3), name: "g1" }), post("c1"), post("g1")],
  },
  {
    name: "execution order propagation",
    now: T0,
    steps: [
      root("r", base(1)),
      child("r", "c1", { id: id(2), start_time: T0 + 1, name: "c1" }),
      child("c1", "g1", { id: id(3), start_time: T0 + 2, name: "g1" }),
      child("g1", "h1", { id: id(4), start_time: T0 + 3, name: "h1" }),
      child("r", "c2", { id: id(5), start_time: T0 + 4, name: "c2" }),
      child("c1", "g2", { id: id(6), start_time: T0 + 5, name: "g2" }),
      post("h1"), post("c2"), post("g2"), patch("c2"),
    ],
  },
  {
    name: "execution order three digits",
    now: T0,
    steps: [root("r", base(1)), child("r", "c", { id: id(2), start_time: T0 + 5, name: "many", count: 1001 }), post("c"), patch("c")],
  },
  {
    name: "end first wins",
    now: T0,
    steps: [
      root("r", base(1)),
      post("r"),
      end("r", { outputs: { first: 1 }, error: "boom", endTime: T0 + 100, metadata: { m: 1 } }),
      end("r", { outputs: { second: 2 }, error: "later", endTime: T0 + 200, metadata: { m: 2, n: 3 } }),
      patch("r"),
    ],
  },
  {
    name: "end null outputs replaced",
    now: T0,
    steps: [root("r", base(1)), end("r", { outputs: null, endTime: T0 + 1 }), end("r", { outputs: { late: true }, error: "e" }), patch("r")],
  },
  {
    name: "end no values",
    now: T0,
    steps: [root("r", base(1)), post("r"), now(T0 + 777), end("r", {}), patch("r")],
  },
  { name: "end empty error string", now: T0, steps: [root("r", base(1)), end("r", { error: "", outputs: {} }), end("r", { error: "real", outputs: { z: 1 } }), patch("r")] },
  { name: "end empty metadata ignored", now: T0, steps: [root("r", base(1)), post("r"), end("r", { outputs: { a: 1 }, metadata: {} }), patch("r")] },
  {
    name: "end metadata after child inheritance",
    now: T0,
    steps: [root("r", base(1, { metadata: { a: 1 } })), post("r"), end("r", { outputs: {}, metadata: { late: true } }), child("r", "c", { id: id(2), name: "c" }), post("c"), patch("r")],
  },
  {
    name: "events",
    now: T0,
    steps: [
      root("r", base(1)),
      child("r", "t", { id: id(2), name: "tool", run_type: "tool", inputs: { args: {} } }),
      post("t"),
      now(T0 + 111),
      event("t", { name: "tool_update", kwargs: { partialResult: { text: "a" } } }),
      now(T0 + 222),
      event("t", { message: "plain string event" }),
      end("t", { outputs: { ok: true }, endTime: T0 + 300 }),
      patch("t"),
    ],
  },
  {
    name: "events before post",
    now: T0,
    steps: [root("r", base(1)), now(T0 + 5), event("r", { name: "early", kwargs: { a: 1 } }), post("r"), patch("r")],
  },
  ...[
    ["unset", {}],
    ["false", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "false" }],
    ["true", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "true" }],
    ["TRUE", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "TRUE" }],
    ["one", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "1" }],
    ["zero", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "0" }],
    ["yes", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "yes" }],
    ["ls empty only", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "" }],
    ["lc empty only", { LANGCHAIN_EXCLUDE_INPUTS_ON_PATCH: "" }],
    ["ls empty lc false", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "", LANGCHAIN_EXCLUDE_INPUTS_ON_PATCH: "false" }],
    ["ls empty lc true", { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "", LANGCHAIN_EXCLUDE_INPUTS_ON_PATCH: "true" }],
    ["lc one", { LANGCHAIN_EXCLUDE_INPUTS_ON_PATCH: "1" }],
  ].map(([n, env]) => ({
    name: `patch excludeInputs env ${n}`,
    now: T0,
    env,
    steps: [root("r", base(1, { inputs: { q: 1 } })), post("r"), patch("r"), patch("r", { excludeInputs: false }), patch("r", { excludeInputs: true })],
  })),
  {
    name: "patch excludeInputs env with replicas",
    now: T0,
    env: { LANGSMITH_EXCLUDE_INPUTS_ON_PATCH: "false" },
    steps: [root("r", base(1, { inputs: { q: 1 }, replicas: [replicaA, replicaB] })), post("r"), patch("r"), patch("r", { excludeInputs: true })],
  },
  {
    name: "patch without post",
    now: T0,
    steps: [root("r", base(1)), patch("r")],
  },
  {
    name: "patch without post replicas",
    now: T0,
    steps: [root("r", base(1, { replicas: [replicaA] })), patch("r")],
  },
  { name: "replica same project", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "proj", updates: { tags: ["t"] } }] })), ...tree().slice(1), ...lifecycle("r", "c1", "g1", "c2")] },
  { name: "replica no project", now: T0, steps: [root("r", base(1, { replicas: [{ apiUrl: "https://x.example", apiKey: "kx" }] })), post("r"), patch("r")] },
  { name: "replica other project", now: T0, steps: [root("r", base(1, { replicas: [replicaA] })), ...tree().slice(1), ...lifecycle("r", "c1", "g1", "c2")] },
  { name: "replicas multiple", now: T0, steps: [...tree({ replicas: [replicaA, replicaB, { projectName: "proj" }] }), ...lifecycle("r", "c1", "g1", "c2")] },
  { name: "replica updates override", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "p2", updates: { name: "renamed", outputs: { forced: 1 }, inputs: { upd: 1 }, session_name: "zz", extra: { u: 1 } } }] })), post("r"), end("r", { outputs: { o: 1 } }), patch("r"), patch("r", { excludeInputs: false })] },
  { name: "replica primary true", now: T0, steps: [...tree({ replicas: [{ projectName: "proj-p", primary: true, apiKey: "kp" }, replicaA] }), ...lifecycle("r", "c1", "g1")] },
  { name: "replica primary false same project", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "proj", primary: false }] })), post("r"), patch("r")] },
  { name: "replica primary true same project", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "proj", primary: true }] })), post("r"), patch("r")] },
  { name: "replica tuple", now: T0, steps: [root("r", base(1, { replicas: [["tuple-proj", { tags: ["x"] }], ["tuple2"]] })), post("r"), patch("r")] },
  { name: "replicas explicit empty beats env", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '[{"api_url":"https://e","api_key":"k"}]' }, steps: [root("r", base(1, { replicas: [] })), post("r"), patch("r")] },
  {
    name: "replica client",
    now: T0,
    steps: [root("r", base(1, { replicas: [{ projectName: "pa", client: "alt" }, { projectName: "pb" }] })), post("r"), patch("r")],
  },
  {
    name: "child explicit replicas",
    now: T0,
    steps: [
      root("r", base(1, { replicas: [replicaA] })),
      child("r", "c1", { id: id(2), name: "c1", replicas: [replicaB] }),
      child("r", "c2", { id: id(3), name: "c2", replicas: [] }),
      child("r", "c3", { id: id(4), name: "c3" }),
      post("c1"), post("c2"), post("c3"), patch("c1"), patch("c2"), patch("c3"),
    ],
  },
  {
    name: "child inherits replicas strips reroot",
    now: T0,
    steps: [
      root("r", base(1, { replicas: [{ projectName: "rr", reroot: true, apiKey: "k" }, { projectName: "plain" }] })),
      child("r", "c1", { id: id(2), name: "c1" }),
      post("r"), post("c1"), patch("c1"), patch("r"),
    ],
  },
  { name: "dotted order given", now: T0, steps: [root("r", { name: "hdr", project_name: "proj", id: id(1), trace_id: id(9), dotted_order: `20231114T221320000001Z${id(9)}.20231114T221321000001Z${id(1)}`, parent_run_id: id(9) }), child("r", "c", { id: id(2), name: "c" }), post("r"), post("c"), patch("c")] },
  { name: "dotted order given generated id", now: T0, steps: [root("r", { name: "hdr", project_name: "proj", dotted_order: `20231114T221320000001Z${id(9)}`, start_time: T0 + 5 }), post("r")] },
  { name: "trace id given", now: T0, steps: [root("r", base(1, { trace_id: id(77) })), child("r", "c", { id: id(2), name: "c" }), post("c")] },
  // reroot (replica.reroot) and distributed parent
  {
    name: "reroot child replica",
    now: T0,
    steps: [
      root("r", base(1)),
      child("r", "c1", { id: id(2), start_time: T0 + 2000, name: "c1", replicas: [{ projectName: "rr", reroot: true, apiKey: "k" }] }),
      child("c1", "g1", { id: id(3), start_time: T0 + 3000, name: "g1", replicas: [{ projectName: "rr", apiKey: "k" }] }),
      child("g1", "h1", { id: id(4), start_time: T0 + 4000, name: "h1", replicas: [{ projectName: "rr", apiKey: "k" }] }),
      child("r", "c2", { id: id(5), start_time: T0 + 5000, name: "c2", replicas: [{ projectName: "rr", apiKey: "k" }] }),
      post("c1"), post("g1"), post("h1"), post("c2"), patch("h1"), patch("g1"), patch("c1"),
    ],
  },
  {
    name: "reroot after children exist",
    now: T0,
    steps: [
      root("r", base(1, { replicas: [{ projectName: "rr", reroot: true }] })),
      child("r", "c1", { id: id(2), name: "c1" }),
      child("c1", "g1", { id: id(3), name: "g1" }),
      post("r"), post("c1"), post("g1"), patch("g1"), patch("r"),
    ],
  },
  {
    name: "reroot with distributed parent",
    now: T0,
    steps: [
      root("r", base(1)),
      child("r", "c1", { id: id(2), start_time: T0 + 2000, name: "c1", replicas: [{ projectName: "rr", reroot: true }] }),
      child("c1", "g1", { id: id(3), start_time: T0 + 3000, name: "g1", replicas: [{ projectName: "rr" }] }),
      { op: "set", run: "c1", distributedParentId: id(1) },
      post("c1"), post("g1"), patch("c1"),
    ],
  },
  {
    name: "reroot with distributed parent self",
    now: T0,
    steps: [
      root("r", base(1)),
      child("r", "c1", { id: id(2), start_time: T0 + 2000, name: "c1", replicas: [{ projectName: "rr", reroot: true }] }),
      { op: "set", run: "c1", distributedParentId: id(2) },
      post("c1"), patch("c1"),
    ],
  },
  {
    name: "reroot shared context vars",
    now: T0,
    steps: [
      root("r", base(1, { replicas: [{ projectName: "rr", reroot: true }, { projectName: "other" }] })),
      post("r"),
      child("r", "c1", { id: id(2), name: "c1" }),
      post("c1"),
      child("c1", "g1", { id: id(3), name: "g1" }),
      post("g1"), patch("g1"),
    ],
  },
  { name: "reroot primary", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "rr", reroot: true, primary: true }] })), child("r", "c1", { id: id(2), name: "c1" }), post("r"), post("c1"), patch("c1")] },
  // environment replicas
  {
    name: "env replicas array",
    now: T0,
    env: { LANGSMITH_RUNS_ENDPOINTS: JSON.stringify([{ api_url: "https://e1.example/", api_key: "k1", project_name: "p1" }, { api_url: "https://e2.example", api_key: "k2", project_name: null }, { api_url: "https://e3.example//", api_key: "k3", primary: true }, { api_url: "https://e4.example", api_key: "k4", primary: false, project_name: "proj" }]) },
    steps: [...tree(), ...lifecycle("r", "c1")],
  },
  {
    name: "env replicas array invalid items",
    now: T0,
    env: { LANGSMITH_RUNS_ENDPOINTS: JSON.stringify([1, "s", null, true, [], {}, { api_url: 1, api_key: "k" }, { api_url: "u" }, { api_url: "u", api_key: 5 }, { api_url: "u", api_key: "k", project_name: 5 }, { api_url: "u", api_key: "k", primary: "yes" }, { api_url: "u", api_key: "k", primary: null }, { api_url: "https://ok", api_key: "k", project_name: "", extra: 1 }]) },
    steps: [root("r", base(1)), post("r"), patch("r")],
  },
  {
    name: "env replicas object",
    now: T0,
    env: { LANGSMITH_RUNS_ENDPOINTS: '{"https://b.example/":"kb","https://a.example":"ka","https://bad.example":5,"https://n.example":null,"https://o.example":{},"https://arr.example":[],"https://t.example":true}' },
    steps: [root("r", base(1)), post("r"), patch("r")],
  },
  {
    name: "env replicas object index keys duplicates",
    now: T0,
    env: { LANGSMITH_RUNS_ENDPOINTS: '{"zeta":"kz","10":"k10","2":"k2","alpha":"ka","zeta":"kz2"}' },
    steps: [root("r", base(1)), post("r"), patch("r")],
  },
  { name: "env replicas object conflicting endpoint LANGSMITH", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '{"https://a":"k"}', LANGSMITH_ENDPOINT: "https://x" }, steps: [root("r", base(1)), root("r2", { ...base(2), replicas: [] }), post("r2")] },
  { name: "env replicas object conflicting endpoint LANGCHAIN", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '{"https://a":"k"}', LANGCHAIN_ENDPOINT: "https://x" }, steps: [root("r", base(1))] },
  { name: "env replicas object endpoint empty", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '{"https://a":"k"}', LANGSMITH_ENDPOINT: "" }, steps: [root("r", base(1)), post("r")] },
  { name: "env replicas empty object with endpoint", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "{}", LANGSMITH_ENDPOINT: "https://x" }, steps: [root("r", base(1)), post("r")] },
  { name: "env replicas array with endpoint", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '[{"api_url":"https://a","api_key":"k"}]', LANGSMITH_ENDPOINT: "https://x" }, steps: [root("r", base(1)), post("r")] },
  { name: "env replicas conflicting but explicit replicas", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '{"https://a":"k"}', LANGSMITH_ENDPOINT: "https://x" }, steps: [root("r", base(1, { replicas: [] })), post("r")] },
  { name: "env replicas invalid json", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "{not json" }, steps: [root("r", base(1)), post("r")] },
  { name: "env replicas trailing garbage", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "[] x" }, steps: [root("r", base(1))] },
  { name: "env replicas scalar string", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '"abc"' }, steps: [root("r", base(1))] },
  { name: "env replicas scalar number", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "12" }, steps: [root("r", base(1))] },
  { name: "env replicas scalar bool", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "true" }, steps: [root("r", base(1))] },
  { name: "env replicas null", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "null" }, steps: [root("r", base(1))] },
  { name: "env replicas empty", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: "" }, steps: [root("r", base(1)), post("r")] },
  { name: "env replicas LANGCHAIN name ignored", now: T0, env: { LANGCHAIN_RUNS_ENDPOINTS: '{"https://a":"k"}' }, steps: [root("r", base(1)), post("r")] },
  {
    name: "env replicas inherited by children",
    now: T0,
    env: { LANGSMITH_RUNS_ENDPOINTS: '[{"api_url":"https://a","api_key":"k","project_name":"pa"}]' },
    steps: [root("r", base(1)), child("r", "c", { id: id(2), name: "c" }), post("r"), post("c"), patch("c")],
  },
  { name: "two primaries explicit", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "a", primary: true }, { projectName: "b", primary: true }] }))] },
  { name: "two primaries env", now: T0, env: { LANGSMITH_RUNS_ENDPOINTS: '[{"api_url":"a","api_key":"k","primary":true},{"api_url":"b","api_key":"k","primary":true}]' }, steps: [root("r", base(1))] },
  { name: "two primaries child", now: T0, steps: [root("r", base(1)), child("r", "c", { id: id(2), name: "c", replicas: [{ primary: true }, { primary: true }] })] },
  { name: "one primary one false", now: T0, steps: [root("r", base(1, { replicas: [{ projectName: "a", primary: true }, { projectName: "b", primary: false }] })), post("r")] },
  // client failures
  { name: "postRun failure", now: T0, client: { failCreate: true }, steps: [root("r", base(1)), child("r", "c", { id: id(2), name: "c" }), post("r"), patch("r")] },
  { name: "patchRun failure", now: T0, client: { failUpdate: true }, steps: [root("r", base(1)), post("r"), patch("r")] },
  { name: "postRun failure replicas", now: T0, client: { failCreate: true }, steps: [root("r", base(1, { replicas: [replicaA, replicaB] })), post("r")] },
  { name: "patchRun failure replicas", now: T0, client: { failUpdate: true }, steps: [root("r", base(1, { replicas: [replicaA, replicaB] })), child("r", "c", { id: id(2), name: "c" }), post("r"), patch("r")] },
  { name: "postRun replica client failure", now: T0, client: { failCreate: false, failAlt: true }, steps: [root("r", base(1, { replicas: [{ projectName: "pa", client: "alt" }, { projectName: "pb" }] })), post("r")] },
  // deprecated child posting and toJSON
  {
    name: "postRun excludeChildRuns false",
    now: T0,
    steps: [...tree(), post("r", { excludeChildRuns: false }), post("r", { excludeChildRuns: false })],
  },
  {
    name: "postRun excludeChildRuns false replicas",
    now: T0,
    steps: [...tree({ replicas: [replicaA] }), post("r", { excludeChildRuns: false })],
  },
  { name: "toJSON", now: T0, steps: [...tree({ replicas: [replicaA] }), { op: "toJSON", run: "r" }, post("r"), { op: "toJSON", run: "r" }] },
  { name: "post clears children", now: T0, steps: [...tree(), post("r"), { op: "toJSON", run: "r" }, patch("r")] },
  { name: "unicode and escapes", now: T0, steps: [root("r", base(1, { name: "日本語 \u2028 \ud83d\ude00 \"q\"", inputs: { s: "a\nb\u0007", n: [1, 2.5, -0, 1e21] }, metadata: { "k\"1": "ü" } })), post("r"), end("r", { outputs: { e: "😀" } }), patch("r")] },
];

// ---------------------------------------------------------------- JS side
const realDate = Date;
let clock = 0;
globalThis.Date = class extends realDate {
  constructor(...a) {
    if (a.length === 0) super(clock);
    else super(...a);
  }
  static now() {
    return clock;
  }
};
let rndCounter = 0;
Object.defineProperty(globalThis.crypto, "getRandomValues", {
  configurable: true,
  value: (arr) => {
    for (let i = 0; i < arr.length; i++) arr[i] = (rndCounter++ * 37 + 11) & 0xff;
    return arr;
  },
});

let calls;
let logs;
console.warn = (...a) => logs.push({ kind: "warn", msg: String(a[0]) });
console.error = (...a) => logs.push({ kind: "error", msg: String(a[0]), detail: a[1] instanceof Error ? a[1].message : a[1] === undefined ? null : String(a[1]) });

const clients = {};
const mkClient = (name, fail) => ({
  async createRun(run, opts) {
    calls.push({ client: name, fn: "createRun", run: JSON.stringify(run), opts: opts === undefined ? null : JSON.stringify(opts) });
    if (fail.create) throw new Error("fake create failure");
  },
  async updateRun(runId, run, opts) {
    calls.push({ client: name, fn: "updateRun", id: runId, run: JSON.stringify(run), opts: opts === undefined ? null : JSON.stringify(opts) });
    if (fail.update) throw new Error("fake update failure");
  },
});

const sortedCopy = (o) => Object.fromEntries(Object.keys(o).sort().map((k) => [k, o[k]]));
const state = (r) =>
  JSON.stringify({
    id: r.id,
    name: r.name,
    run_type: r.run_type,
    project_name: r.project_name,
    parent_run_id: r.parent_run?.id ?? null,
    trace_id: r.trace_id,
    dotted_order: r.dotted_order,
    execution_order: r.execution_order,
    child_execution_order: r.child_execution_order,
    start_time: r.start_time,
    serialized_start_time: r._serialized_start_time ?? null,
    end_time: r.end_time ?? null,
    error: r.error ?? null,
    outputs: r.outputs ?? null,
    inputs: r.inputs,
    extra: r.extra,
    tags: r.tags ?? null,
    events: r.events ?? null,
    replicas: r.replicas.map((x) => sortedCopy(Object.fromEntries(Object.entries(x).filter(([k]) => k !== "client")))),
    child_run_ids: r.child_runs.map((c) => c.id),
    distributed_parent_id: r.distributedParentId ?? null,
  });

function toConfig(spec, fakes) {
  const c = { ...spec };
  delete c.count;
  if (Array.isArray(c.replicas))
    c.replicas = c.replicas.map((rep) => (rep && !Array.isArray(rep) && rep.client ? { ...rep, client: fakes[rep.client] } : rep));
  return c;
}

async function runCase(cs, runtimeEnv) {
  calls = [];
  logs = [];
  warnMod._resetWarnedMessages();
  for (const k of Object.keys(process.env)) if (/^(LANGSMITH|LANGCHAIN)_/.test(k)) delete process.env[k];
  Object.assign(process.env, cs.env ?? {});
  clock = cs.now;
  const fail = { create: !!cs.client?.failCreate, update: !!cs.client?.failUpdate };
  const main = mkClient("main", fail);
  const alt = mkClient("alt", { create: !!cs.client?.failAlt, update: false });
  const fakes = { main, alt };
  const runs = {};
  const order = [];
  const results = [];
  const toJSONs = [];
  for (const step of cs.steps) {
    let error = null;
    try {
      switch (step.op) {
        case "root": {
          const cfg = toConfig(step.config, fakes);
          runs[step.as] = new RunTree({ ...cfg, client: main });
          order.push(step.as);
          break;
        }
        case "child": {
          let last;
          const n = step.config.count ?? 1;
          for (let i = 0; i < n; i++) last = runs[step.of].createChild(toConfig(step.config, fakes));
          runs[step.as] = last;
          order.push(step.as);
          break;
        }
        case "post":
          await runs[step.run].postRun(step.excludeChildRuns);
          break;
        case "patch":
          await runs[step.run].patchRun(step.excludeInputs === undefined ? undefined : { excludeInputs: step.excludeInputs });
          break;
        case "end":
          await runs[step.run].end(step.outputs, step.error, step.endTime, step.metadata);
          break;
        case "event":
          runs[step.run].addEvent(step.message !== undefined ? step.message : { name: step.name, kwargs: step.kwargs });
          break;
        case "now":
          clock = step.ms;
          break;
        case "set":
          runs[step.run].distributedParentId = step.distributedParentId;
          break;
        case "toJSON":
          toJSONs.push(JSON.stringify(runs[step.run]));
          break;
        default:
          throw new Error(`unknown op ${step.op}`);
      }
    } catch (e) {
      error = { name: e.name, message: e.message, code: e.code ?? null };
    }
    results.push(error);
  }
  return { name: cs.name, results, calls, logs, states: order.map((n) => state(runs[n])), toJSONs };
}

const runtimeEnv = envMod.getRuntimeEnvironment();
const golden = { sdk_version: "0.10.5", runtimeEnv, cases: [] };
for (const cs of cases) golden.cases.push(await runCase(cs, runtimeEnv));

// RUNTREE_OUT redirects the output (the ls tool's container does not write back into the synced tree).
const out = process.env.RUNTREE_OUT || testdata;
fs.mkdirSync(out, { recursive: true });
fs.writeFileSync(path.join(out, "runtree_cases.json"), JSON.stringify(cases, null, 1) + "\n");
fs.writeFileSync(path.join(out, "runtree_golden.json"), JSON.stringify(golden, null, 1) + "\n");
console.log(`wrote ${cases.length} cases, ${golden.cases.reduce((n, c) => n + c.calls.length, 0)} client calls`);
