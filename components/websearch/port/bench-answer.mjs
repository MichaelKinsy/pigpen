// Latency of the model side calls: the original (page-query.ts, summary-review.ts) under Node against
// the Go port's benchmarks, interleaved on one host. The model is scripted on both sides, so what is
// measured is the code around the call (page cut, prompt, candidate resolution, result handling).
//
//   node port/bench-answer.mjs one                      # the original: JSON with per-call milliseconds
//   node port/bench-answer.mjs drive <go-test-binary> [runs]
//
// The Go side is the package's test binary (go test -c) running BenchmarkAnswerFromPage, BenchmarkAnswerFromPageSerialized
// and BenchmarkGenerateSummaryDraft; each run measures CALLS calls. Node is Node 24 with the oracle's
// dependencies installed.
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const CALLS = 200;

async function one() {
	const dir = mkdtempSync(join(tmpdir(), "answer-bench-"));
	process.env.PI_CODING_AGENT_DIR = dir;
	const { answerFromPage } = await import(new URL(`file://${join(here, "oracle", "page-query.ts")}`).href);
	const { generateSummaryDraft } = await import(new URL(`file://${join(here, "oracle", "summary-review.ts")}`).href);

	const pageModel = { api: "custom-page-api", provider: "test", id: "page-model", input: ["text"], contextWindow: 200000 };
	const answer = { stopReason: "stop", content: [{ type: "text", text: "The answer." }] };
	const pageCtx = {
		model: pageModel,
		modelRegistry: { getApiKeyAndHeaders: async () => ({ ok: true, apiKey: "k" }), complete: async () => answer },
		scopedModels: [],
		sessionManager: { getSessionId: () => "s" },
	};
	const page = "The page text goes on and on. ".repeat(70000);

	const haiku = { api: "anthropic-messages", provider: "anthropic", id: "claude-haiku-4-5", input: ["text"], contextWindow: 200000 };
	const summaryCtx = {
		model: undefined,
		modelRegistry: {
			find: (p, id) => (p === haiku.provider && id === haiku.id ? haiku : undefined),
			getAvailable: () => [haiku],
			getApiKeyAndHeaders: async () => ({ ok: true, apiKey: "k" }),
			complete: async () => answer,
		},
		scopedModels: [],
		sessionManager: { getSessionId: () => "s" },
	};
	const results = Array.from({ length: 8 }, () => ({
		query: "query", answer: "An answer sentence. ".repeat(40), provider: "tavily",
		results: [{ title: "One", url: "https://a.test/1" }, { title: "Two", url: "https://a.test/2" }],
	}));

	// The model call that serializes its request, as the host call does before sending it: the
	// page text of the original is a lazily joined string until something reads it.
	const serializingCtx = { ...pageCtx, modelRegistry: { ...pageCtx.modelRegistry, complete: async (_model, context) => { JSON.stringify(context); return answer; } } };

	const out = {};
	const cases = {
		AnswerFromPage: () => answerFromPage({ question: "Q", pageText: page, sourceUrl: "https://example.com" }, pageCtx),
		AnswerFromPageSerialized: () => answerFromPage({ question: "Q", pageText: page, sourceUrl: "https://example.com" }, serializingCtx),
		GenerateSummaryDraft: () => generateSummaryDraft(results, summaryCtx),
	};
	for (const [name, fn] of Object.entries(cases)) {
		await fn();
		const times = [];
		for (let i = 0; i < CALLS; i++) {
			const t = performance.now();
			await fn();
			times.push(performance.now() - t);
		}
		out[name] = times;
	}
	process.stdout.write(JSON.stringify(out));
}

const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];

function goRun(bin, name) {
	const r = spawnSync(bin, ["-test.run=NONE", `-test.bench=^Benchmark${name}$`, `-test.benchtime=${CALLS}x`, "-test.benchmem"], { encoding: "utf8" });
	const m = r.stdout.match(/([\d.]+) ns\/op.*?(\d+) B\/op\s+(\d+) allocs\/op/);
	if (!m) throw new Error(`no benchmark line: ${r.stdout}${r.stderr}`);
	return { ms: Number(m[1]) / 1e6, bytes: Number(m[2]), allocs: Number(m[3]) };
}

function drive(bin, runs) {
	const names = ["AnswerFromPage", "AnswerFromPageSerialized", "GenerateSummaryDraft"];
	const node = Object.fromEntries(names.map((n) => [n, []]));
	const go = Object.fromEntries(names.map((n) => [n, []]));
	for (let i = 0; i < runs; i++) {
		const n = JSON.parse(spawnSync(process.execPath, [fileURLToPath(import.meta.url), "one"], { encoding: "utf8", maxBuffer: 1 << 26 }).stdout);
		for (const name of names) {
			node[name].push(median(n[name]));
			go[name].push(goRun(bin, name));
		}
	}
	const report = { runs, callsPerRun: CALLS, node: {}, go: {} };
	for (const name of names) {
		report.node[name] = { medianMs: median(node[name]) };
		report.go[name] = { medianMs: median(go[name].map((x) => x.ms)), bytesPerOp: median(go[name].map((x) => x.bytes)), allocsPerOp: median(go[name].map((x) => x.allocs)) };
	}
	process.stdout.write(JSON.stringify(report, null, 1) + "\n");
}

if (process.argv[2] === "one") await one();
else if (process.argv[2] === "drive") drive(process.argv[3], Number(process.argv[4] ?? 10));
else throw new Error("usage: one | drive <go-test-binary> [runs]");
