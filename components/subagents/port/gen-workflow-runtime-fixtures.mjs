// Records the original's runWorkflowScript over a corpus of scripts with a deterministic fake host, into
// extensions/pi-subagents/testdata/workflow-runtime.json (see workflow_runtime_diff_test.go).
//   WFS_ORACLE=/tmp/oracle node --experimental-strip-types port/gen-workflow-runtime-fixtures.mjs <out.json>
import fs from "node:fs";
const { runWorkflowScript } = await import(`${process.env.WFS_ORACLE}/src/workflows/scripted-workflow.ts`);
const corpus = [];
const add = (script, extra) => corpus.push({ script, ...(extra ?? {}) });
const run = (key, extra = "") => `runs.run("${key}", { agent: "worker", task: "t-${key}"${extra} })`;
// results and basics
add(`return 1 + 1;`);
add(`return undefined;`);
add(`return { a: undefined, b: [undefined, 1], c: null };`);
add(`return { a: 1 };\n`);
add(``, {});
add(`const r = await ${run("a")}; return r.output;`);
add(`const r = await ${run("a")}; return String(r);`);
add(`const r = await ${run("a")}; return runs.ref(r) + "|" + runs.refs([r, r]);`);
add(`return runs.ref(null);`);
add(`return runs.refs(1);`);
add(`const r = await ${run("a")}; return Object.keys(r);`);
add(`const r = await ${run("a")}; return JSON.stringify(r);`);
add(`const rs = await runs.all([{ key: "a", agent: "w", task: "x" }, { key: "b", agent: "w", task: "y" }]); return rs.map((r) => r.output);`);
add(`const rs = await runs.all([{ key: "a", agent: "w", task: "x" }, { key: "fail1", agent: "w", task: "y" }]); return rs.map((r) => [r.key, r.ok, r.error]);`);
add(`const rs = await runs.all([{ key: "a", agent: "w", task: "x" }]); return rs.analysis;`);
add(`const rs = await runs.all([{ key: "a", agent: "w", task: "x" }]); const [one] = rs; return [one.key, Array.isArray(rs), rs.length, typeof rs.then, JSON.stringify(rs)];`);
add(`const rs = await runs.all([{ key: "a", agent: "w", task: "x" }]); emit(rs); return rs.length;`);
add(`await runs.run("fail1", { agent: "w", task: "x" }); return 1;`);
add(`try { await runs.run("fail1", { agent: "w", task: "x" }); } catch (e) { return e.message; } return 2;`);
add(`await runs.run("detach1", { agent: "w", task: "x" }); return 1;`);
add(`const r = await runs.run("run1", { agent: "w", task: "x" }); return r;`);
add(`const r = await runs.run("stop1", { agent: "w", task: "x" }); return r.stopped;`);
// validation of run calls
for (const params of [`null`, `[]`, `{ action: "list" }`, `{ workflowScript: "x" }`, `{ tasks: [] }`, `{ clarify: true }`, `{ worktree: "yes" }`, `{ baseRef: "HEAD~1" }`, `{ lane: { version: 2, key: "k" } }`, `{ lane: { version: 1, key: "other" } }`, `{ lane: { version: 1, key: "k", mode: "x" } }`, `{ lane: { version: 1, key: "k", extra: 1 } }`, `{ gate: "" }`, `{ gate: { command: "x", output: "yaml" } }`, `{ gate: { command: "x", schema: {} } }`, `{ gate: "x", acceptance: true }`, `{ gate: "x", resume: "r", task: "t" }`, `{ resume: "r", agent: "w", task: "t" }`, `{ resume: "r" }`, `{ resume: "" , task: "t"}`, `{ resume: { workflowRunId: "w", key: "k", latest: false }, task: "t" }`, `{ resume: { workflowRunId: "", key: "k", latest: true }, task: "t" }`, `{ resume: { workflowRunId: "w", key: "bad key", latest: true }, task: "t" }`, `{ resume: { extra: 1 }, task: "t" }`, `{ resume: 5, task: "t" }`, `{ extensionBindings: [] }`, `{ extensionBindings: { "bad": {} } }`, `{ extensionBindings: { "ns/1": 1 }, resume: "r", task: "t" }`, `{ agent: "w", task: () => 1 }`, `{ agent: "w", task: undefined }`, `{ agent: "w", nested: { a: [1, , 2] } }`, `{ agent: "w", d: new Date(0) }`, `{ agent: "w", n: NaN }`, `{ agent: "w", b: 1n }`, `{ agent: "w", [Symbol("s")]: 1 }`]) {
  add(`return await runs.run("k", ${params});`);
}
add(`return await runs.run("bad key", { agent: "w" });`);
add(`return await runs.run(5, { agent: "w" });`);
add(`await runs.host("h1", { kind: "command", command: "x", timeoutMs: 5 }); return await runs.run("h1", { agent: "w" });`);
add(`const a = ${run("dup")}; const b = runs.run("dup", { agent: "worker", task: "other" }); return [await a, await b];`);
add(`const a = ${run("dup")}; const b = ${run("dup")}; const [x, y] = await Promise.all([a, b]); return [x.key, y.key, x === y];`);
// runs.all validation
add(`return await runs.all("x");`);
add(`return await runs.all([1]);`);
add(`return await runs.all([[]]);`);
add(`return await runs.all([ , { key: "a", agent: "w" }]);`);
add(`return await runs.all([{ key: "a", agent: "w" }, { key: "a", agent: "w", task: "other" }]);`);
add(`return await runs.all([{ key: "a", agent: "w", task: "x" }, { key: "a", agent: "w", task: "x" }]);`);
add(`await runs.run("a", { agent: "w", task: "x" }); return await runs.all([{ key: "a", agent: "w", task: "other" }]);`);
// steer / status
add(`return await runs.steer("a", "m");`);
add(`const p = ${run("a")}; const s = await runs.steer("a", "  go  ", { mode: "steer" }); await p; return s;`);
add(`return runs.steer(5, "m");`);
add(`return runs.steer("a", "");`);
add(`return runs.steer("a", "m", []);`);
add(`return runs.steer("a", "m", { x: 1 });`);
add(`return runs.steer("a", "m", { mode: "x" });`);
add(`return runs.steer("a", "m", { index: -1 });`);
add(`return runs.steer("a", "m", { ackTimeoutMs: 0 });`);
add(`return await runs.status("");`);
add(`await ${run("a")}; const s = await runs.status("a"); return s.output;`);
add(`return await runs.status("fail1");`);
add(`return await runs.status(5);`);
// host
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5 });`);
add(`return await runs.host("t", { kind: "command", command: "fail", timeoutMs: 5 });`);
add(`return await runs.host("bad key", { kind: "command", command: "x", timeoutMs: 5 });`);
add(`return await runs.host("t", { kind: "x", command: "x", timeoutMs: 5 });`);
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5, cwd: "/tmp" });`);
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 0 });`);
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5, output: "../x" });`);
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5, role: "bad" });`);
add(`return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5, provider: "" });`);
add(`await runs.host("t", { kind: "command", command: "x", timeoutMs: 5 }); return await runs.host("t", { kind: "command", command: "x", timeoutMs: 5 });`);
add(`runs.host("t", { kind: "command", command: "x", timeoutMs: 5 }); return 1;`);
add(`const h = await runs.host("t", { kind: "command", command: "x", timeoutMs: 5, role: "ci" }); return Object.keys(h);`);
// promise observation: accepted
add(`return ${run("a")};`);
add(`return await ${run("a")};`);
add(`const p = ${run("a")}; return await p;`);
add(`const p = ${run("a")}; await 1; return await p;`);
add(`const p = ${run("a")}; const q = ${run("b")}; return await Promise.all([p, q]);`);
add(`const p = ${run("a")}; const q = ${run("b")}; return await Promise.race([p, q]);`);
add(`const p = ${run("a")}; const q = ${run("b")}; await Promise.race([p, q]); await p; await q; return 1;`);
add(`const p = ${run("a")}; const q = ${run("b")}; await Promise.race([p, q]); return 1;`);
add(`const p = ${run("a")}; const q = ${run("b")}; return await Promise.allSettled([p, q]);`);
add(`const p = ${run("a")}; const q = ${run("fail1")}; const r = await Promise.allSettled([p, q]); return r.map((x) => x.status);`);
add(`const p = ${run("a")}; const q = ${run("b")}; return await Promise.any([p, q]);`);
add(`return await Promise.all([${run("a")}, ${run("b")}].map((p) => p.then((r) => r.key)));`);
add(`return await ${run("a")}.then((r) => r.key);`);
add(`return ${run("a")}.then((r) => r.key);`);
add(`return await ${run("fail1")}.catch((e) => e.message);`);
add(`return await ${run("a")}.finally(() => {});`);
add(`return ${run("a")}.then((r) => ${run("b")});`);
add(`return await ${run("a")}.then((r) => ${run("b")}).then((r) => r.key);`);
add(`const x = await ${run("a")}.then((r) => ${run("b")}); return x.key;`);
add(`${run("a")}.then((r) => ${run("b")}); return 1;`);
add(`${run("a")}.then(() => {}); return 1;`);
add(`${run("a")}.catch(() => {}); return 1;`);
add(`${run("a")}.finally(() => {}); return 1;`);
add(`${run("a")}; return 1;`);
add(`const p = ${run("a")}; return 1;`);
add(`const p = ${run("a")}; p.then(() => {}); return 1;`);
add(`const p = ${run("a")}; await p.then(() => {}); return 1;`);
add(`const p = ${run("a")}; p.then(() => {}); await p; return 1;`);
add(`Promise.all([${run("a")}]); return 1;`);
add(`await Promise.all([${run("a")}]); return 1;`);
add(`Promise.race([${run("a")}]); return 1;`);
add(`Promise.allSettled([${run("a")}]); return 1;`);
add(`Promise.any([${run("a")}]); return 1;`);
add(`Promise.resolve(${run("a")}); return 1;`);
add(`await Promise.resolve(${run("a")}); return 1;`);
add(`new Promise((resolve) => resolve(${run("a")})); return 1;`);
add(`await new Promise((resolve) => resolve(${run("a")})); return 1;`);
add(`await new Promise((resolve, reject) => { resolve(1); resolve(${run("a")}); }); return 1;`);
add(`await new Promise((resolve) => { resolve("done"); Promise.resolve().then(() => resolve(${run("a")})); }); return 1;`);
add(`await Promise.resolve(); ${run("a")}.then(() => {}); return 1;`);
add(`await Promise.resolve(); await ${run("a")}; return 1;`);
add(`await 1; ${run("a")}; return 1;`);
add(`function helper(k) { return ${run("a")}; } const c = await helper(); return c.key;`);
add(`function helper(k) { return runs.run(k, { agent: "w", task: "t" }); } const cs = await Promise.all(["a", "b"].map(helper)); return cs.map((c) => c.key);`);
add(`function helper(k) { return runs.run(k, { agent: "w", task: "t" }); } ["a", "b"].map(helper); return 1;`);
add(`const ps = ["a", "b"].map((k) => runs.run(k, { agent: "w", task: "t" })); return (await Promise.all(ps)).length;`);
add(`const ps = ["a", "b"].map((k) => runs.run(k, { agent: "w", task: "t" })); await ps[0]; return 1;`);
add(`const ps = ["a", "b"].map((k) => runs.run(k, { agent: "w", task: "t" })); for (const p of ps) await p; return 1;`);
add(`for (const k of ["a", "b"]) { await runs.run(k, { agent: "w", task: "t" }); } return 1;`);
add(`const pending = [{ key: "a", promise: ${run("a")}.then((r) => ({ key: "a", result: r })) }, { key: "b", promise: ${run("b")}.then((r) => ({ key: "b", result: r })) }]; const first = await Promise.race(pending.map((p) => p.promise)); const rest = pending.filter((p) => p.key !== first.key); await Promise.all(rest.map((p) => p.promise)); return first.key;`);
add(`const p = ${run("a")}; try { await p; } finally { }; return 1;`);
add(`const p = ${run("a")}; if (false) { await p; } return 1;`);
add(`const p = ${run("a")}; const f = async () => { await p; }; return 1;`);
add(`const p = ${run("a")}; return { p };`);
add(`const p = ${run("a")}; return [p];`);
add(`return ${run("a")}.output;`);
add(`const p = ${run("a")}; emit(p); return 1;`);
add(`const p = ${run("a")}; const r = await p; return r.then;`);
add(`const p = ${run("a")}; return [typeof p.then, typeof p.catch, typeof p.finally, p instanceof Promise, Object.prototype.toString.call(p)];`);
add(`const p = Promise.resolve(1); return [p instanceof Promise, typeof Promise.all, Promise.name, typeof Promise.withResolvers];`);
add(`const { promise, resolve } = Promise.withResolvers(); resolve(${run("a")}); await promise; return 1;`);
add(`return await Promise.try(() => ${run("a")});`);
add(`await Promise.reject(new Error("x")).catch((e) => e.message); return 1;`);
add(`await Promise.all([]); return await Promise.race([Promise.resolve(3)]);`);
add(`class P extends Promise {} return typeof P;`);
// lanes
add(`return await runs.lanes([{ key: "lane", stages: [{ key: "w", agent: "w", task: "t" }, { key: "r", agent: "r", task: "u" }] }]);`);
add(`return await runs.lanes([{ key: "lane", stages: [{ key: "w", agent: "w", task: "t" }, { key: "r", resume: "previous", task: "u" }] }]);`);
add(`return await runs.lanes([{ key: "l1", stages: [{ key: "w", agent: "w", task: "t" }] }, { key: "l2", stages: [{ key: "w", agent: "w", task: "t" }, { key: "r", agent: "r", task: "u" }] }]);`);
add(`return await runs.lanes([{ key: "lane", stages: [{ key: "w", agent: "w", task: "t" }, { key: "r", agent: "r", task: "u" }] }]).then((b) => b[0].state);`);
add(`runs.lanes([{ key: "lane", stages: [{ key: "w", agent: "w", task: "t" }] }]); return 1;`);
add(`return await runs.lanes([{ key: "lane", stages: [{ key: "fail1", agent: "w", task: "t" }, { key: "r", agent: "r", task: "u" }] }]);`);
add(`return await runs.lanes([]);`);
add(`return await runs.lanes("x");`);
add(`return await runs.lanes([{ key: "l", stages: [] }]);`);
add(`return await runs.lanes([{ key: "l", extra: 1, stages: [{ key: "s", agent: "w" }] }]);`);
add(`return await runs.lanes([{ key: "l", stages: [{ key: "s", agent: "w" }, { key: "s", agent: "w" }] }]);`);
add(`return await runs.lanes([{ key: "l", stages: [{ key: "s", resume: "r1", task: "t" }] }]);`);
add(`return await runs.lanes([{ key: "l", stages: [{ key: "s", resume: "previous", task: "t" }] }]);`);
add(`return await runs.lanes([{ key: "l", stages: [{ key: "s", agent: "w", task: "t" }, { key: "t", resume: "x", task: "t" }] }]);`);
add(`return await runs.lanes([{ key: "bad key", stages: [{ key: "s", agent: "w" }] }]);`);
add(`return await runs.lanes(Array.from({ length: 33 }, (_, i) => ({ key: "l" + i, stages: [{ key: "s", agent: "w", task: "t" }] })));`);
add(`return await runs.lanes([{ key: "l", stages: Array.from({ length: 17 }, (_, i) => ({ key: "s" + i, agent: "w", task: "t" })) }]);`);
// emit / console / state / args
add(`emit({ a: 1 }); emit([1, 2]); emit("s"); emit(null); return 1;`);
add(`emit(undefined);`);
add(`emit(() => 1);`);
add(`emit({ a: NaN });`);
add(`emit(new Date(0));`);
add(`emit({ a: [1, , 3] });`);
add(`const o = {}; o.self = o; emit(o);`);
add(`emit(new Map());`);
add(`emit(1n);`);
add(`emit(Symbol("s"));`);
add(`emit({ [Symbol("s")]: 1 });`);
add(`console.log("a", 1, { b: 2 }); console.info("i"); console.warn("w"); console.error("e"); return typeof console.debug;`);
add(`console.log([1, 2, 3], new Set([1, 2]), new Map([["a", 1]]), null, undefined, true, 5n, Symbol("x"));`);
add(`console.log(function foo() {}, class A {}, () => 1, new Date(0), /x/gi, new Error("boom").message);`);
add(`console.log({ a: { b: { c: { d: { e: 1 } } } } });`);
add(`console.log("x".repeat(200), ["y".repeat(100), "z".repeat(100)]);`);
add(`console.log(Array.from({ length: 30 }, (_, i) => i));`);
add(`console.log(Array.from({ length: 120 }, (_, i) => i));`);
add(`console.log({ "a-b": 1, 2: 3, c: undefined, d: () => 1, e: Symbol("s") });`);
add(`console.log([, 1, , , 2]);`);
add(`console.log(Object.create(null), Object.create({ x: 1 }), new (class Foo { constructor() { this.a = 1; } })());`);
add(`console.log("%s %d", "a", 1);`);
add(`console.log({ get a() { return 1; }, set b(v) {}, get c() { return 1; }, set c(v) {} });`);
add(`const o = { a: 1 }; o.self = o; console.log(o);`);
add(`console.log(new Error("e") instanceof Error, [new Error("x")].length);`);
add(`console.log(Promise.resolve(1), (async () => 1)());`);
add(`console.log(["a\\nb", "it's", 'say "hi"', "back\`tick", "both ' \\" "]);`);
add(`console.log("multi\\nline", { s: "line1\\nline2\\nline3 that is long enough to be split across lines in the inspect output okay" });`);
add(`console.log(-0, 0, 1e21, 1e-7, 0.1 + 0.2, [-0]);`);
add(`console.log(new Number(1), new String("s"), new Boolean(false));`);
add(`console.log(Object.assign(() => {}, { a: 1 }));`);
add(`console.log(new Uint8Array([1, 2, 3]));`);
add(`console.log([[[[[1]]]]], { a: [[[[1]]]] });`);
add(`console.log(args);`, { args: { a: [1, { b: 2 }], s: "x" } });
add(`return [Object.isFrozen(args), Object.isFrozen(args.a), Object.isFrozen(args.a[1]), args.a[1].b];`, { args: { a: [1, { b: 2 }] } });
add(`args.x = 1; return args;`, { args: { a: 1 } });
add(`"use strict"; args.x = 1; return args;`, { args: { a: 1 } });
add(`return args;`);
add(`return args.constructor.constructor("return 1")();`, { args: { a: 1 } });
add(`return Object.keys(args.a.__proto__.constructor);`, { args: { a: {} } });
add(`return typeof state;`);
add(`return await state.get("k");`, { state: true });
add(`await state.set("k", { a: 1 }); return await state.get("k");`, { state: true });
add(`return await state.get("bad key");`, { state: true });
add(`return await state.set("k", () => 1);`, { state: true });
add(`state.set("k", 1); return 1;`, { state: true });
// sandbox surface
add(`return [typeof require, typeof process, typeof module, typeof setTimeout, typeof setInterval, typeof setImmediate, typeof queueMicrotask, typeof fetch, typeof structuredClone, typeof Buffer, typeof TextEncoder, typeof URL, typeof globalThis, typeof window, typeof self];`);
add(`return [typeof Object, typeof Array, typeof JSON, typeof Math, typeof Date, typeof Map, typeof Set, typeof WeakMap, typeof RegExp, typeof Symbol, typeof Proxy, typeof Reflect, typeof BigInt, typeof Uint8Array, typeof WeakRef];`);
add(`return eval("1");`);
add(`return new Function("return 1")();`);
add(`return (function () {}).constructor("return 1")();`);
add(`return (async function () {}).constructor("return 1")();`);
add(`return (function* () {}).constructor("return 1")();`);
add(`return [].map.constructor("return 1")();`);
add(`return globalThis.eval("1");`);
add(`return (0, eval)("1");`);
add(`return Object.getPrototypeOf(async function () {}).constructor("return 1")();`);
add(`return Object.isFrozen(runs) + "|" + Object.keys(runs).join(",") + "|" + Object.isFrozen(console) + "|" + Object.keys(console).join(",");`);
add(`runs.run = 1; return typeof runs.run;`);
add(`return Object.getOwnPropertyNames(globalThis).filter((k) => ["runs", "emit", "console", "args", "state", "Promise"].includes(k)).sort();`);
add(`return this === globalThis;`);
add(`return typeof this;`);
add(`return (() => { return typeof arguments; })();`);
// errors thrown by script
add(`throw new Error("boom");`);
add(`throw "str";`);
add(`throw { a: 1 };`);
add(`throw null;`);
add(`throw new TypeError("tt");`);
add(`class E extends Error { constructor(m) { super(m); this.name = "E"; } } throw new E("custom");`);
add(`undefined.x;`);
add(`const o = {}; o.f();`);
add(`const o = {}; o.a.b;`);
add(`undefinedFn();`);
add(`const n = 1; n();`);
add(`JSON.parse("bad");`);
add(`JSON.parse("");`);
add(`JSON.parse("{");`);
add(`JSON.parse('{"a":1,}');`);
add(`JSON.parse("[1,]");`);
add(`JSON.parse("undefined");`);
add(`JSON.parse('{"a":}');`);
add(`JSON.parse("{'a':1}");`);
add(`JSON.parse('{"a":1} x');`);
add(`JSON.parse('{"aaaaaaaaaaaaaaaaaaaaaa": tru}');`);
add(`try { JSON.parse("{"); } catch (e) { return [e.name, e.message]; }`);
add(`try { undefinedFn(); } catch (e) { return [e.name, e.message]; }`);
add(`try { new Array(-1); } catch (e) { return [e.name, e.message]; }`);
add(`try { Object.freeze({a:1}).a = 2; return "no throw"; } catch (e) { return [e.name, e.message]; }`);
add(`try { Symbol() + ""; } catch (e) { return [e.name, e.message]; }`);
add(`try { 1n + 1; } catch (e) { return [e.name, e.message]; }`);
add(`return await Promise.reject(new TypeError("tt"));`);
add(`await Promise.reject(new Error("later"));`);
add(`Promise.reject(new Error("unhandled")); await new Promise((r) => r(1)); return 1;`);
add(`return [1, 2, 3].at(-1) + [1, [2]].flat().length + Object.groupBy([1, 2], (x) => x % 2 ? "o" : "e").o.length + "x".at(0).length;`);
add(`return [new Date(0).toISOString(), [3, 1, 2].toSorted().join(), Object.fromEntries([["a", 1]]), "a-b".replaceAll("-", "+"), structuredClone === undefined];`);
add(`return (1234567.891).toLocaleString();`);
add(`return ["b", "a", "C"].sort((x, y) => x.localeCompare(y));`);
add(`return JSON.stringify({ d: new Date(0), u: undefined, n: null, f() {} });`);
add(`return String(Symbol("x")) + [1, [2, 3]].toString() + String(null) + `+"`${1 + 1}`"+`;`);
add(`const { a, ...rest } = { a: 1, b: 2, c: 3 }; return [a, rest, [...[1, 2]], { ...{ x: 1 } }];`);
add(`label: for (const i of [1, 2]) { for (const j of [1, 2]) { if (j === 2) continue label; } } return 1;`);
add(`return [typeof Symbol.asyncIterator, typeof Array.fromAsync, typeof Object.hasOwn, typeof structuredClone];`);
add(`return await (async () => 1)();`);
add(`const f = async () => 1; return 1;`);
add(`function* g() { yield 1; } return [...g()];`);
add(`const it = { async *[Symbol.asyncIterator]() { yield 1; } }; return 1;`);
add(`for await (const x of [Promise.resolve(1), 2]) { if (x === 2) return x; }`);
add(`let i = 0; while (true) { i++; if (i > 100000) break; } return i;`);
add(`let s = 0; for (let i = 0; i < 1e6; i++) s += i; return s;`);
add(`return [1, 2, 3].map(async (x) => x);`);
const normalize = (m) => String(m).split("\n    at ")[0];
const results = [];
for (const c of corpus) {
  const trace = [];
  const launchLog = [];
  const opts = {
    script: c.script,
    ...(c.args ? { args: c.args } : {}),
    timeoutMs: 8000,
    async launch(key, params) {
      launchLog.push(key);
      if (key.startsWith("slow")) await new Promise((r) => setTimeout(r, 30));
      if (key.startsWith("fail")) return { key, ok: false, output: "child failed " + key, artifactPaths: [] };
      if (key.startsWith("detach")) return { key, ok: false, detached: true, output: "child detached " + key, error: "detached " + key, artifactPaths: [] };
      if (key.startsWith("run")) return { key, ok: true, state: "running", output: "", artifactPaths: [], runId: "run-" + key };
      if (key.startsWith("stop")) return { key, ok: false, stopped: true, output: "stopped " + key, artifactPaths: [] };
      return { key, ok: true, output: "out-" + key, runId: "id-" + key + "-12345678", artifactPaths: [], ...(params.resume ? { resumedFrom: params.resume } : {}) };
    },
    async status(key) { return key.startsWith("fail") ? { key, ok: false, output: "no run " + key, artifactPaths: [] } : { key, ok: true, output: "status " + key, artifactPaths: [] }; },
    async steer(key, message, options) { return { key, state: "delivered", requestId: "req-1" }; },
    async host(key, params) { return params.command === "fail" ? { key, kind: "command", ok: false, state: "failed", exitCode: 1, stdout: "", stderr: "bad", outputPath: "/tmp/x/out.log", durationMs: 3, error: "failed" } : { key, kind: "command", ok: true, state: "passed", exitCode: 0, stdout: "o", stderr: "", outputPath: "/tmp/x/out.log", durationMs: 3 }; },
    ...(c.state ? { state: (() => { const m = new Map(); return { get: async (k) => m.get(k) ?? null, set: async (k, v) => { m.set(k, v); } }; })() } : {}),
    onTrace(t) { trace.length = 0; trace.push(...t.map((e) => ({ ...e, durationMs: e.durationMs === undefined ? undefined : "n" }))); },
  };
  let out;
  try {
    const r = await runWorkflowScript(opts);
    out = { ok: true, value: r.value, emits: r.emits, console: r.console, children: r.children.map((x) => x.key), trace: r.trace.map((e) => ({ ...e, durationMs: e.durationMs === undefined ? undefined : "n" })), launches: launchLog };
  } catch (e) {
    out = { ok: false, kind: e.errorKind ?? null, message: normalize(e.message), emits: e.partial?.emits, console: e.partial?.console, children: e.partial?.children?.map((x) => x.key), trace: e.partial?.trace?.map((x) => ({ ...x, durationMs: x.durationMs === undefined ? undefined : "n" })), launches: launchLog };
  }
  results.push({ ...c, expected: JSON.parse(JSON.stringify(out)) });
}
fs.writeFileSync(process.argv[2], JSON.stringify(results, null, 1));
console.log(results.length, "cases;", results.filter((r) => !r.expected.ok).length, "fail");
