// Latency of the original's curator page (oracle/curator-page.ts generateCuratorPage) and of serving it from the
// original's server (oracle/curator-server.ts startCuratorServer, one GET of the page over loopback), with the
// inputs of the Go benchmarks BenchmarkGenerateCuratorPage and BenchmarkCuratorPageServe (curator_bench_test.go).
// Prints the median of N runs per operation, in microseconds.
//   node port/bench-curator.mjs [runs=200]
import http from "node:http";

const runs = Number(process.argv[2] ?? 200);
const { generateCuratorPage } = await import(new URL("./oracle/curator-page.ts", import.meta.url).href);
const { startCuratorServer } = await import(new URL("./oracle/curator-server.ts", import.meta.url).href);

const token = "00000000-0000-4000-8000-000000000000";
const avail = { all: true, exa: true, brave: true, tavily: true };

function median(label, times) {
	times.sort((a, b) => a - b);
	console.log(`${label}: median ${times[Math.floor(times.length / 2)].toFixed(1)} us over ${times.length} runs`);
}

for (let i = 0; i < 50; i++) generateCuratorPage(["first query", "second query"], token, 20, avail, "exa", "auto", [], null);
const gen = [];
for (let i = 0; i < runs; i++) {
	const s = process.hrtime.bigint();
	generateCuratorPage(["first query", "second query"], token, 20, avail, "exa", "auto", [], null);
	gen.push(Number(process.hrtime.bigint() - s) / 1e3);
}
median("generate page", gen);

const handle = await startCuratorServer(
	{ queries: ["test query"], sessionToken: token, timeout: 20, availableProviders: { all: true, exa: true }, defaultProvider: "exa", searchProvider: "exa", summaryModels: [], defaultSummaryModel: null },
	{ onSubmit() {}, onCancel() {}, onProviderChange() {}, onAddSearch: async () => [], onAddSearchResults() {}, onSummarize: async () => ({ summary: "", meta: {} }), onRewriteQuery: async (q) => q },
);
const get = () => new Promise((resolve, reject) => {
	http.get(handle.url, (res) => { let n = 0; res.on("data", (c) => { n += c.length; }); res.on("end", () => resolve(n)); }).on("error", reject);
});
for (let i = 0; i < 50; i++) await get();
const serve = [];
for (let i = 0; i < runs; i++) {
	const s = process.hrtime.bigint();
	await get();
	serve.push(Number(process.hrtime.bigint() - s) / 1e3);
}
median("serve page over loopback", serve);
handle.close();
process.exit(0);
