// Latency of PDF text extraction: the original (pdf-extract.ts, unpdf) under Node against the Go
// port's benchmark, interleaved on one host.
//
//   node port/bench-pdf-extract.mjs one                 # the original: JSON with per-call milliseconds
//   node port/bench-pdf-extract.mjs drive <go-test-binary> [runs]
//
// The driver alternates `runs` (default 10) runs of each side and prints medians. The Go side is the
// package's test binary (go test -c) running BenchmarkPDFExtractReport30Pages and BenchmarkPDFExtractOnePage;
// each run measures N calls after one warm-up call. Node is Node 24 with the oracle's dependencies installed.
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const fixtures = join(here, "..", "extensions", "websearch", "testdata", "pdf");
const CALLS = 20;

async function one() {
	const dir = mkdtempSync(join(tmpdir(), "pdf-bench-"));
	process.env.PI_CODING_AGENT_DIR = dir;
	for (const k of ["GEMINI_API_KEY", "DATALAB_API_KEY", "GOOGLE_GEMINI_BASE_URL", "CLOUDFLARE_API_KEY"]) delete process.env[k];
	writeFileSync(join(dir, "web-search.json"), JSON.stringify({ pdf: { provider: "unpdf" } }));
	const started = performance.now();
	const { extractPDFToMarkdown } = await import(new URL(`file://${join(here, "oracle", "pdf-extract.ts")}`).href);
	const out = { startMs: 0, files: {} };
	for (const file of ["report-30p.pdf", "text.pdf"]) {
		const bytes = readFileSync(join(fixtures, file));
		const times = [];
		for (let i = 0; i < CALLS + 1; i++) {
			const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
			const t = performance.now();
			await extractPDFToMarkdown(buffer, "https://example.test/report.pdf", { outputDir: dir });
			times.push(performance.now() - t);
		}
		out.files[file] = { firstMs: times[0], warmMs: times.slice(1) };
		if (file === "report-30p.pdf") out.startMs = performance.now() - started - times.reduce((a, b) => a + b, 0);
	}
	out.maxRSSkB = process.resourceUsage().maxRSS;
	process.stdout.write(JSON.stringify(out));
}

const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];

function goRun(bin, name) {
	const r = spawnSync(bin, ["-test.run=NONE", `-test.bench=^${name}$`, `-test.benchtime=${CALLS}x`, "-test.benchmem"], { encoding: "utf8" });
	const m = r.stdout.match(/([\d.]+) ns\/op\s+[\d.]+ MB\/s\s+(\d+) B\/op\s+(\d+) allocs\/op/);
	if (!m) throw new Error(`no benchmark line: ${r.stdout}${r.stderr}`);
	return { ms: Number(m[1]) / 1e6, bytes: Number(m[2]), allocs: Number(m[3]) };
}

function drive(bin, runs) {
	const node = { "report-30p.pdf": [], "text.pdf": [] };
	const nodeFirst = { "report-30p.pdf": [], "text.pdf": [] };
	const rss = [];
	const go = { BenchmarkPDFExtractReport30Pages: [], BenchmarkPDFExtractOnePage: [] };
	for (let i = 0; i < runs; i++) {
		const n = JSON.parse(spawnSync(process.execPath, [fileURLToPath(import.meta.url), "one"], { encoding: "utf8", maxBuffer: 1 << 24 }).stdout);
		for (const f of Object.keys(node)) {
			node[f].push(median(n.files[f].warmMs));
			nodeFirst[f].push(n.files[f].firstMs);
		}
		rss.push(n.maxRSSkB);
		for (const name of Object.keys(go)) go[name].push(goRun(bin, name));
	}
	const report = {
		runs,
		callsPerRun: CALLS,
		node: Object.fromEntries(Object.keys(node).map((f) => [f, { warmMedianMs: median(node[f]), firstCallMedianMs: median(nodeFirst[f]) }])),
		nodeMaxRSSMedianMB: median(rss) / 1024,
		go: Object.fromEntries(Object.entries(go).map(([k, v]) => [k, { warmMedianMs: median(v.map((x) => x.ms)), bytesPerOp: median(v.map((x) => x.bytes)), allocsPerOp: median(v.map((x) => x.allocs)) }])),
	};
	process.stdout.write(JSON.stringify(report, null, 1) + "\n");
}

if (process.argv[2] === "one") await one();
else if (process.argv[2] === "drive") drive(process.argv[3], Number(process.argv[4] ?? 10));
else throw new Error("usage: one | drive <go-test-binary> [runs]");
