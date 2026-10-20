// Latency of the original's HTML pipeline (oracle/extract.ts extractContent: fetch from a loopback server, linkedom,
// Readability, turndown) on two fixtures of extensions/websearch/testdata/html, to set against the Go benchmark
// BenchmarkExtractFixture (same files, the fetch replaced by memory). Prints the median of N runs per fixture, in ms.
//   node port/bench-html.mjs [runs=15]
import { createServer } from "node:http";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const runs = Number(process.argv[2] ?? 15);
const cfg = await mkdtemp(join(tmpdir(), "bench-html-"));
await writeFile(join(cfg, "web-search.json"), JSON.stringify({ ssrf: { allowRanges: ["127.0.0.0/8"] } }));
process.env.PI_CODING_AGENT_DIR = cfg;
const { extractContent } = await import(new URL("./oracle/extract.ts", import.meta.url).href);
const names = ["article-basic", "large-page"];
const bodies = Object.fromEntries(await Promise.all(names.map(async (n) => [n, await readFile(new URL(`../extensions/websearch/testdata/html/${n}.html`, import.meta.url))])));
const server = createServer((req, res) => { res.writeHead(200, { "content-type": "text/html" }); res.end(bodies[req.url.slice(1)]); });
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const origin = `http://127.0.0.1:${server.address().port}`;
for (const n of names) {
	await extractContent(`${origin}/${n}`);
	const t = [];
	for (let i = 0; i < runs; i++) {
		const s = process.hrtime.bigint();
		await extractContent(`${origin}/${n}`);
		t.push(Number(process.hrtime.bigint() - s) / 1e6);
	}
	t.sort((a, b) => a - b);
	console.log(`${n}: median ${t[Math.floor(t.length / 2)].toFixed(2)} ms over ${runs} runs (${bodies[n].length} bytes)`);
}
server.close();
process.exit(0);
