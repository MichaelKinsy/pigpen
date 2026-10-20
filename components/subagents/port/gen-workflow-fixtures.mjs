// Records the original's validateWorkflowScript over a corpus into extensions/pi-subagents/testdata/workflow-validate.json.
// The original needs its dependencies: copy port/oracle somewhere, `npm install acorn@8.18.0` there and point WFS_ORACLE at it.
//   WFS_ORACLE=/tmp/oracle node --experimental-strip-types port/gen-workflow-fixtures.mjs extensions/pi-subagents/testdata/workflow-validate.json
import fs from "node:fs";
const { validateWorkflowScript } = await import(`${process.env.WFS_ORACLE ?? new URL("./oracle", import.meta.url).pathname}/src/workflows/scripted-workflow.ts`);
const corpus = [];
const add = (script, opts) => corpus.push({ script, ...(opts ? { opts } : {}) });
// syntax
add("const value = 1;\nreturn (;");
add("const task = `Run:\n```bash\nnpm test\n```;\nreturn task;");
add("return 1 +;");
add("let a = 1; let a = 2;");
add("foo(");
add("const x = 'abc");
add("const x = `abc");
add("   \n ");
add("if (x) {");
add("return await");
add("for (;;");
add("x = = 1");
add("class { }");
add("try { x }");
// nested async
add("async function helper() { return 1; }\nreturn 1;");
add("const f = async () => 1;\nreturn f;");
add("const o = { async m() { return 1; } };\nreturn o;");
add("class A { async m() { return 1; } }\nreturn A;");
add("const f = async function () {};\nreturn f;");
add("const g = async function* () {};\nreturn g;");
add("async function* gen() { yield 1; }\nreturn gen;");
add("for await (const x of []) { }\nreturn 1;");
add("const r = /async function x() {}/;\nreturn r;");
add("return `${(async () => 1)()}`;");
// runs.run keys/params
add('return runs.run("bad key", { agent: "worker" });');
add('return runs.run("single", { agent: "worker", task: undefined });');
add('return runs.all([{ key: "same", agent: "worker", task: undefined }]);');
add('return runs.run("single", { agent: "worker", task: undefined, task: "real" });');
add('return runs.all([{ ...{ key: "bad key" }, agent: "worker" }]);');
add('return runs.run("same", { agent: selectedAgent });');
add('return runs.run("k", { agent: "a", task: () => 1 });');
add('return runs.run("k", { agent: "a", task: 10n });');
add('return runs.run("k", { agent: "a", task: /x/ });');
add('return runs.run("k", { agent: "a", task: [1,,2] });');
add('return runs.run("k", { agent: "a", task: [undefined] });');
add('return runs.run("k", { agent: "a", nested: { x: void 0 } });');
add('return runs.run(`k`, { agent: "a" });');
add('return runs["run"]("bad key", { agent: "a" });');
add('const runs = {}; return runs.run("bad key", {});');
add('return runs.run(`${x}`, {});');
// baseRef
for (const [call, value] of [["run", JSON.stringify("a".repeat(40))], ["run", JSON.stringify("A".repeat(64))], ["all", JSON.stringify("A".repeat(40))], ["all", "`" + "a".repeat(64) + "`"], ["run", '"HEAD~1"'], ["all", '"refs/heads/bad..ref"'], ["run", "null"], ["all", "42"], ["run", "false"]]) {
  add([call === "run" ? 'return runs.run("child", {' : "return runs.all([{", '  key: "child", agent: "worker", task: "Check",', `  baseRef: ${value}`, call === "run" ? "});" : "}]);"].join("\n"));
}
for (const fields of ["", 'baseRef: "HEAD"', 'baseRef: "refs/heads/release"', 'baseRef: "refs/tags/v1"', 'baseRef: "origin/main"', 'baseRef: "HEAD~1", baseRef: "HEAD"', 'baseRef: "HEAD~1", ["baseRef"]: `HEAD`', 'baseRef: "HEAD~1", baseRef: selectedRef', 'baseRef: "HEAD~1", baseRef: "refs/heads/" + branch', 'baseRef: "HEAD~1", ...overrides', 'baseRef: "HEAD~1", [field]: "HEAD"', 'baseRef: "HEAD~1", get baseRef() { return "HEAD"; }', 'baseRef: "HEAD~1", set baseRef(value) {}', 'baseRef: "HEAD", baseRef: "HEAD~1"', '...defaults, baseRef: "HEAD~1"', '[field]: "HEAD", ["baseRef"]: "HEAD~1"', 'get baseRef() { return "HEAD"; }, baseRef: "HEAD~1"']) {
  add(`return runs.run("child", { agent: "worker", task: "Check", ${fields} });`);
  add(`return runs.all([{ key: "child", agent: "worker", task: "Check", ${fields} }]);`);
}
add('return runs.run("child", { baseRef() { return "HEAD"; } });');
// host
add('return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, output: "reports/tests.log", role: "ci" });');
add('return runs.host("tests", { command: "npm test", timeoutMs: 1000 });');
add('return runs.host("tests", { kind: "http", command: "npm test", timeoutMs: 1000 });');
add('return runs.host("tests", { kind: "command", command: "npm test" });');
add('return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, output: "../tests.log" });');
add('return runs.host("tests", { kind: "command", command: "npm test", timeoutMs: 1000, cwd: "/tmp" });');
add('return runs.host("bad key", { kind: "command", command: " ", timeoutMs: 1.5, role: "x", output: " ", extra: 1 });');
add('return runs.host("t", { kind: "command", command: "x", timeoutMs: 0 });');
add('return runs.host("t", { kind: "command", command: "x", timeoutMs: 5, role: "gate", output: "/abs" });');
add('return runs.host("t", { ...x });');
// emit / state / return
add('emit(undefined); emit(() => 1); emit({a: 10n}); return undefined;');
add('state.set("k", undefined); return void 0;');
add('return () => 1;');
add('function f() { return undefined; } return 1;');
add('return [undefined, 1];');
add('return { a: undefined, b: function(){} };');
// keyed access
add('const results = await runs.all([{ key: "bad key", agent: "worker" }, { key: "someChild", agent: "reviewer" }]);\nasync function helper() { return "no"; }\nreturn results.someChild;');
add('const r = await runs.all([{ key: "analysis", agent: "a", task: "t" }]);\nreturn r.analysis.output;');
add('const r = await runs.all([{ key: "map", agent: "a", task: "t" }, { key: "0", agent: "a", task: "t" }, { key: "length", agent: "a", task: "t" }, { key: "5", agent: "a", task: "t" }]);\nreturn [r.map, r["0"], r.length, r[5], r.constructor];');
add('let r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nr = [];\nreturn r.x;');
add('const r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\n{ const r = {}; r.x; }\nreturn 1;');
add('var r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nr = {};\nr.x;\nreturn r;');
add('const r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nreturn r["x"];');
add('const r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nreturn r?.x;');
add('const r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nfunction f(r) { return r.x; }\nreturn f;');
add('const r = await runs.all([{ key: "x", agent: "a", task: "t" }]);\nfor (const r of []) { r.x; }\ntry {} catch (r) { r.x; }\nreturn r.x;');
// agent names / budgets
const agentErr = (name) => name === "ghost" ? `Unknown agent '${name}'.` : undefined;
add('return runs.run("a", { agent: "ghost", task: "x" });', { agent: true });
add('return runs.all([{ key: "a", agent: "ghost", task: "x" }, { key: "b", agent: "worker", task: "y" }]);', { agent: true });
add('return runs.lanes([{ key: "l", stages: [{ key: "s", agent: "ghost", task: "x" }] }]);', { agent: true });
add('return runs.run("a", { agent: "ghost", cwd: "/x", task: "x" });', { agent: true });
add('return runs.run("a", { agent: "ghost", resume: "r", task: "x" });', { agent: true });
add('return runs.run("a", { ...x, agent: "ghost" });', { agent: true });
add('const runs = {}; return runs.run("a", { agent: "ghost" });', { agent: true });
add('return runs.run("a", { agent: `ghost`, task: "x" });', { agent: true });
add('return runs.run("a", { ["agent"]: "ghost" });', { agent: true });
add('return runs.lanes([{ ...l, stages: [{ key: "s", agent: "ghost" }] }]);', { agent: true });
for (const budget of [0, 1, 2, 5]) {
  add('await runs.run("a", { agent: "w", task: "x" });\nawait runs.all([{ key: "b", agent: "w", task: "y" }, { key: "c", agent: "w", task: "z" }]);\nreturn 1;', { budget });
  add('const keys = ["a","b"];\nawait runs.all(keys.map((k) => ({ key: k, agent: "w", task: "x" })));\nreturn 1;', { budget });
  add('for (const k of ["a","b"]) { await runs.run(k, { agent: "w", task: "x" }); }\nreturn 1;', { budget });
  add('await runs.run("a", { agent: "w", task: "x" });\nif (x) return 1;\nawait runs.run("b", { agent: "w", task: "x" });\nreturn 2;', { budget });
  add('await runs.run("a", { agent: "w", task: "x" });\nreturn 1;\nawait runs.run("b", { agent: "w", task: "x" });', { budget });
  add('const r = runs; await r.run("a", {});\nreturn 1;', { budget });
  add('var runs = 1; await runs.run("a", {});\nreturn 1;', { budget });
  add('await runs.run("a", { agent: "w", task: (await runs.run("inner", {})).output });\nreturn 1;', { budget });
  add('return await runs.lanes([{ key: "l", stages: [{ key: "s", agent: "w" }] }]);', { budget });
  add('await runs.all(items);\nreturn 1;', { budget });
  add('await runs.all([{ key: k, agent: "w" }]);\nreturn 1;', { budget });
  add('await runs.all([{ ...x, key: "a" }]);\nreturn 1;', { budget });
}
// misc syntax coverage for the tree converter
add('const {a, b: {c = 1}, ...rest} = args; const [x, , y = 2, ...z] = rest.list; return {a, c, x, y, z};');
add('label: for (const x of [1]) { if (x) continue label; else break label; } switch (1) { case 1: break; default: }');
add('const t = tag`a${1}b`; const o = { get a() { return 1; }, set a(v) {}, [k]: 1, "s": 2, 3: 4 };');
add('class A extends B { static x = 1; #p = 2; static { this.y = 3; } get v() { return this.#p; } static async s() {} }');
add('do { x++; } while (x < 3); while (x) { x--; } for (var i = 0, j = 1; i < 2; i++, j--) {} for (const k in o) {} with (o) {}');
add('const f = (a, b = 1, ...c) => a + b; const g = function* () { yield* [1]; }; x ??= 1; y ||= 2; z &&= 3; return a?.b?.[c]?.(d);');
add('return new.target; ');
add('debugger; ; return typeof x === "undefined" ? void 0 : delete x.y, !x, -x, +x, ~x;');
add('return 0x10 + 1_000 + .5 + 1e3 + 0b11 + 0o7;');
add('const x = "\\u00e9" + \'\\x41\'; const r = /[a-z]+/giu; return [x, r];');
const results = corpus.map((c) => {
  const opts = c.opts?.agent ? { agentNameError: agentErr } : c.opts?.budget !== undefined ? { maxSubagentSpawnsPerRun: c.opts.budget } : {};
  return { ...c, result: validateWorkflowScript(c.script, opts) };
});
fs.writeFileSync(process.argv[2], JSON.stringify(results, null, 1));
console.log(results.length, "cases", results.filter((r) => !r.result.ok).length, "not ok");
