// Parse latency of the original's WASM parser (web-tree-sitter 0.26.9 + tree-sitter-bash 0.25.1) on
// bench-inputs.json: cold start (init, load, first parse) and the median ns/op of N parses per input.
// Run from an installed oracle copy: node bench.mjs <oracle>/packages/pi-permission-system <inputs.json>
import { createRequire } from "node:module";
import fs from "node:fs";

const [pkg, inputsPath] = process.argv.slice(2);
const req = createRequire(pkg + "/");
const inputs = JSON.parse(fs.readFileSync(inputsPath, "utf8"));
const t0 = process.hrtime.bigint();
const { Parser, Language } = await import(req.resolve("web-tree-sitter"));
await Parser.init({ locateFile: () => req.resolve("web-tree-sitter/web-tree-sitter.wasm") });
const parser = new Parser();
parser.setLanguage(await Language.load(req.resolve("tree-sitter-bash/tree-sitter-bash.wasm")));
const first = parser.parse(Object.values(inputs)[0]);
first.delete();
const cold = Number(process.hrtime.bigint() - t0) / 1e6;
const out = { cold_ms: cold };
for (const [name, s] of Object.entries(inputs)) {
  for (let i = 0; i < 200; i++) parser.parse(s)?.delete(); // warm
  const runs = [];
  for (let r = 0; r < 15; r++) {
    const start = process.hrtime.bigint();
    const n = 300;
    for (let i = 0; i < n; i++) parser.parse(s)?.delete();
    runs.push(Number(process.hrtime.bigint() - start) / n);
  }
  runs.sort((a, b) => a - b);
  out[name] = runs[Math.floor(runs.length / 2)];
}
console.log("WASM " + JSON.stringify(out));
