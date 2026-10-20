// Latency of the original Gemini API search on the input of BenchmarkGeminiSearch (key auth, one answer
// of 2,400 characters, eight grounding chunks without a redirect lookup), `fetch` replaced by an
// in-process stub (the network is not measured), for the performance comparison of the Gemini PR.
//   node port/bench-gemini.mjs [iterations]
// Prints one JSON line: milliseconds per search and the heap growth per search (approximate: the heap
// is not collected between searches, so this is a lower bound of the allocation).
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const dir = await mkdtemp(join(tmpdir(), "pigpen-bench-"));
process.env.PI_CODING_AGENT_DIR = dir;
for (const name of Object.keys(process.env)) if (/_API_KEY$|_BASE_URL$/.test(name)) delete process.env[name];
process.env.GEMINI_API_KEY = "bench-key";
const iterations = Number(process.argv[2] ?? 2000);

const body = JSON.stringify({
	candidates: [{
		content: { parts: [{ text: "answer text ".repeat(200) }] },
		groundingMetadata: { groundingChunks: Array.from({ length: 8 }, () => ({ web: { uri: "https://example.com/a", title: "A" } })) },
	}],
});
globalThis.fetch = async () => new Response(body, { status: 200, headers: { "content-type": "application/json" } });

const { search } = await import(new URL("./oracle/gemini-search.ts", import.meta.url).href);
const call = () => search("benchmark query", { provider: "gemini" });
for (let i = 0; i < 200; i++) await call();
const heap = process.memoryUsage().heapUsed;
const start = process.hrtime.bigint();
for (let i = 0; i < iterations; i++) await call();
const ns = Number(process.hrtime.bigint() - start);
const grown = process.memoryUsage().heapUsed - heap;
console.log(JSON.stringify({ case: "gemini-search", msPerOp: ns / iterations / 1e6, heapBytesPerOp: grown / iterations }));
