// Latency of the original's Defuddle fallback (extract.ts extractWithDefuddle: linkedom, Defuddle 0.19.3 with
// { markdown: true, useAsync: false }) on the pages of BenchmarkDefuddle (defuddle_bench_test.go). Prints the median
// of N runs per page in ms and, first, the time to load the two modules (the cold start of the original).
//   node port/bench-defuddle.mjs [runs=15]
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";

const runs = Number(process.argv[2] ?? 15);
const require = createRequire(new URL("./oracle/package.json", import.meta.url));
const t0 = process.hrtime.bigint();
const { Defuddle } = await import(new URL("./oracle/node_modules/defuddle/dist/node.js", import.meta.url).href);
const { parseHTML } = require("linkedom");
console.log(`load: ${(Number(process.hrtime.bigint() - t0) / 1e6).toFixed(1)} ms`);
const pages = ["port--rsc-fallback-null-readability", "general--daringfireball.net-2025-02-the_iphone_16e", "general--lesswrong.com-s-N7nDePaNabJdnbXeE-p-vJFdjigzmcXMhNTsx", "math--mathjax", "footnotes--maggieappleton.com-xanadu-patterns"];
const names = ["small", "article", "large", "math", "footnotes"];
const warn = console.warn;
console.warn = () => {};
const err = console.error;
console.error = () => {};
for (const [i0, file] of pages.entries()) {
	const name = names[i0];
	const { url } = JSON.parse(readFileSync(new URL(`../extensions/websearch/testdata/defuddle/${file}.expected.json`, import.meta.url), "utf8"));
	const html = new TextDecoder().decode(readFileSync(new URL(`../extensions/websearch/testdata/defuddle/${file}.html`, import.meta.url)));
	const once = async () => {
		const { document } = parseHTML(html);
		Object.defineProperty(document, "location", { value: new URL(url), configurable: true });
		return Defuddle(document, url, { markdown: true, useAsync: false });
	};
	await once();
	const t = [];
	for (let i = 0; i < runs; i++) {
		const s = process.hrtime.bigint();
		await once();
		t.push(Number(process.hrtime.bigint() - s) / 1e6);
	}
	t.sort((a, b) => a - b);
	console.log(`${name}: median ${t[t.length >> 1].toFixed(2)} ms over ${runs} runs (${html.length} chars)`);
}
console.warn = warn;
console.error = err;
process.exit(0);
