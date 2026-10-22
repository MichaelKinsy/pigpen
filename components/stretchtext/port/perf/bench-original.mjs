// Times the original's parser and word-by-word growth (TypeScript, port/oracle, run with Node's type transform) on the
// inputs of inputs.json, the same ones as extensions/stretchtext/bench_test.go, for the PR's performance section.
//
//   node --experimental-transform-types port/perf/bench-original.mjs [seconds per benchmark]
//
// Prints "<name> ns/op=<n>", the mean of the iterations.
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = resolve(here, "../oracle");
const inputs = JSON.parse(readFileSync(join(here, "inputs.json"), "utf8"));
const { parse } = await import(pathToFileURL(join(oracle, "src/parse.ts")).href);
const { grow } = await import(pathToFileURL(join(oracle, "src/grow.ts")).href);
const seconds = Number(process.argv[2] ?? 3);

function bench(name, fn) {
	for (let i = 0; i < 200; i++) fn();
	let n = 0;
	const start = process.hrtime.bigint();
	const deadline = start + BigInt(Math.floor(seconds * 1e9));
	while (process.hrtime.bigint() < deadline) {
		for (let i = 0; i < 50; i++) fn();
		n += 50;
	}
	const ns = Number(process.hrtime.bigint() - start) / n;
	console.log(`${name} ns/op=${Math.round(ns)}`);
}

const long = parse(inputs.long);
bench("Parse", () => parse(inputs.reply));
bench("Grow", () => {
	for (const p of inputs.progress) grow(inputs.short, long, p, (n) => `<${n.key}>`, (w) => `[${w}]`);
});
