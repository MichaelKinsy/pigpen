// Latency of the original model-backed providers on the inputs of the wire fixtures, `fetch` replaced by
// an in-process stub (the network is not measured), for the performance comparison of the port PR.
//   node port/bench-model-providers.mjs [iterations]
// Prints one JSON line per case: milliseconds per search and the heap growth per search (approximate:
// the heap is not collected between searches, so this is a lower bound of the allocation).
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const dir = await mkdtemp(join(tmpdir(), "pigpen-bench-"));
process.env.PI_CODING_AGENT_DIR = dir;
for (const name of Object.keys(process.env)) if (/_API_KEY$|_BASE_URL$/.test(name)) delete process.env[name];
const url = (f) => new URL(`./oracle/${f}`, import.meta.url).href;
const iterations = Number(process.argv[2] ?? 2000);

const bodies = {
	xai: `{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://x.ai/news","title":"xAI News"}]}},{"type":"message","content":[{"type":"output_text","text":"Grok Imagine v1.5 shipped with reference images.","annotations":[{"type":"url_citation","url":"https://x.ai/news/grok-imagine","title":"Grok Imagine","start_index":0,"end_index":12}]}]}],"citations":["https://x.ai/news/grok-imagine","https://x.ai/citation-only"]}`,
	mistral: `{"outputs":[{"type":"tool.execution","name":"web_search"},{"type":"message.output","content":[{"type":"text","text":"Mistral found the answer."},{"type":"tool_reference","tool":"web_search","title":"Mistral source","url":"https://example.com/source","description":"A useful source."},{"type":"text","text":"It returned references."}]}]}`,
	kimi: `{"search_results":[{"title":"Kimi result","url":"https://example.com/one","snippet":"First snippet"},{"title":"   ","url":"https://example.com/two"},{"title":"Blocked","url":"https://ads.example.com/x","snippet":"blocked"}]}`,
	responses: `{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://example.com/raw","title":"Raw source"}]}},{"type":"message","content":[{"type":"output_text","text":"See [the docs](https://example.com/docs) for more.","annotations":[{"type":"url_citation","url":"https://example.com/docs?utm_source=openai&x=1","title":"Docs","start_index":4,"end_index":20}]}]}]}`,
	alpha: `{"output":"Alpha plaintext","results":[{"type":"text_result","url":"https://example.com/alpha?utm_source=openai","title":"Alpha","snippet":"Evidence"},{"type":"image_result","url":"https://ignored.example"}]}`,
};
let body = "";
globalThis.fetch = async () => new Response(body, { status: 200 });

const kimiCtx = { modelRegistry: { getAll: () => [{ provider: "kimi-code", id: "compat" }, { provider: "kimi-coding", id: "primary" }], getApiKeyAndHeaders: async () => ({ ok: true, apiKey: "kimi-token", headers: { "X-Registry": "forwarded" } }) } };
const models = [{ provider: "openai", id: "gpt-5.6-terra", api: "openai-responses", baseUrl: "https://api.openai.com/v1" }, { provider: "openai", id: "gpt-5.7", api: "openai-responses", baseUrl: "https://api.openai.com/v1" }, { provider: "openai", id: "gpt-5.5-pro", api: "openai-responses", baseUrl: "https://api.openai.com/v1" }];
const openaiCtx = { modelRegistry: { getAll: () => models, find: () => undefined, getApiKeyAndHeaders: async () => ({ ok: true, apiKey: "sk-registry", headers: { "X-Org": "acme" } }) } };
const filters = { recencyFilter: "week", numResults: 3, domainFilter: ["example.com", "-spam.example"] };

const cases = [
	["xai", { xaiApiKey: "k" }, "xai-search.ts", "searchWithXai", bodies.xai, (m) => m("what shipped?", filters)],
	["mistral", { mistralApiKey: "k" }, "mistral-search.ts", "searchWithMistral", bodies.mistral, (m) => m("what shipped?", filters)],
	["openai-responses", { openaiApiKey: "k" }, "openai-search.ts", "searchWithOpenAI", bodies.responses, (m) => m("what shipped?", filters)],
	["openai-registry", {}, "openai-search.ts", "searchWithOpenAI", bodies.responses, (m) => m("what shipped?", filters, openaiCtx)],
	["openai-alpha", { openaiApiKey: "k", openaiUseAlphaSearch: true, openaiSearchModel: "gpt-5.6-luna" }, "openai-search.ts", "searchWithOpenAI", bodies.alpha, (m) => m("alpha", { recencyFilter: "day", domainFilter: ["www.example.com"] })],
	["kimi", {}, "kimi-search.ts", "searchWithKimi", bodies.kimi, (m) => m("what shipped?", filters, kimiCtx)],
];
let n = 0;
for (const [name, config, file, fn, response, call] of cases) {
	await writeFile(join(dir, "web-search.json"), JSON.stringify(config));
	body = response;
	const mod = await import(url(file) + `?bench=${++n}`);
	for (let i = 0; i < 200; i++) await call(mod[fn]);
	const heap = process.memoryUsage().heapUsed;
	const start = process.hrtime.bigint();
	for (let i = 0; i < iterations; i++) await call(mod[fn]);
	const ns = Number(process.hrtime.bigint() - start);
	const grown = process.memoryUsage().heapUsed - heap;
	console.log(JSON.stringify({ case: name, msPerOp: ns / iterations / 1e6, heapBytesPerOp: grown / iterations }));
}
