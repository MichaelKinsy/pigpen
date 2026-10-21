// Records what the original model-backed providers (openai-search.ts, xai-search.ts, kimi-search.ts,
// mistral-search.ts) send and return, with `globalThis.fetch` replaced by a recorder. These providers
// call fixed endpoints, so a pigeq scenario server cannot receive their traffic; the fixture below is
// the oracle-derived wire proof instead. The Go twins (model_wire_fixtures_test.go) replay each case
// against the port and compare the requests (method, URL, headers, raw body) and the result.
//
//   node port/record-model-requests.mjs            # rewrite port/fixtures/model-provider-requests.json
//   node port/record-model-requests.mjs --check    # fail when the recorded fixture differs
//
// Run it in the component directory with the oracle's node_modules (Node 22.6 or later: the original
// is TypeScript and Node strips the types).

import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = (name) => new URL(`./oracle/${name}`, import.meta.url).href;
const fixturePath = join(here, "fixtures", "model-provider-requests.json");

const CODEX_KEY = `header.${Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "test-account" } })).toString("base64url")}.signature`;
const openaiOfficial = { id: "gpt-5.6-terra", provider: "openai", api: "openai-responses", baseUrl: "https://api.openai.com/v1" };

const xaiBody = {
	output: [
		{ type: "web_search_call", action: { sources: [{ url: "https://x.ai/news", title: "xAI News" }] } },
		{ type: "message", content: [{ type: "output_text", text: "Grok Imagine v1.5 shipped with reference images.",
			annotations: [{ type: "url_citation", url: "https://x.ai/news/grok-imagine", title: "Grok Imagine", start_index: 0, end_index: 12 }] }] },
	],
	citations: ["https://x.ai/news/grok-imagine", "https://x.ai/citation-only"],
};
const xSearchBody = {
	output: [
		{ type: "x_search_call", action: { sources: [{ url: "https://x.com/i/status/raw", title: "Raw X post" }, { ignored: true }] }, sources: ["https://x.com/i/status/source-string"] },
		{ type: "message", content: [{ type: "output_text", text: "People are discussing the release on X.",
			annotations: [{ type: "url_citation", url: "https://x.com/i/status/answer", title: "Answer citation", start_index: 7, end_index: 15 }] }] },
	],
	citations: ["https://x.com/i/status/citation-only", { url: "https://x.com/i/status/citation-object", title: "Citation object" }, 42],
};
const mistralBody = { outputs: [
	{ type: "tool.execution", name: "web_search" },
	{ type: "message.output", content: [
		{ type: "text", text: "Mistral found the answer." },
		{ type: "tool_reference", tool: "web_search", title: "Mistral source", url: "https://example.com/source", description: "A useful source." },
		{ type: "text", text: "It returned references." },
	] },
] };
const kimiBody = { search_results: [
	{ title: "Kimi result", url: "https://example.com/one", snippet: "First snippet" },
	{ title: "   ", url: "https://example.com/two" },
	{ title: "Blocked", url: "https://ads.example.com/x", snippet: "blocked" },
] };
const responsesBody = { output: [
	{ type: "web_search_call", action: { sources: [{ url: "https://example.com/raw", title: "Raw source" }] } },
	{ type: "message", content: [{ type: "output_text", text: "See [the docs](https://example.com/docs) for more.",
		annotations: [{ type: "url_citation", url: "https://example.com/docs?utm_source=openai&x=1", title: "Docs", start_index: 4, end_index: 20 }] }] },
] };
const alphaBody = { output: "Alpha plaintext", results: [
	{ type: "text_result", url: "https://example.com/alpha?utm_source=openai", title: "Alpha", snippet: "Evidence" },
	{ type: "image_result", url: "https://ignored.example" },
] };
const sse = `event: response.created\ndata: {"type":"response.created"}\n\n` +
	`data: {"type":"response.output_item.done","item":{"type":"web_search_call"}}\n\n` +
	`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"Streamed answer","annotations":[{"type":"url_citation","url":"https://example.com/streamed","title":"Streamed","start_index":0,"end_index":8}]}]}}\n\n` +
	`data: {"type":"response.completed","response":{"output":[]}}\n\ndata: [DONE]\n`;

// One case: the provider, web-search.json, options, the Pi registry (models and the auth each model
// resolves to), the session, and what the fake endpoint answers.
const cases = [
	{ name: "xai default body", provider: "xai", config: { xaiApiKey: "xai-key" }, query: "what shipped?", response: { json: xaiBody } },
	{ name: "xai web and x tools", provider: "xai", config: { xaiApiKey: "xai-key", xaiSearchTools: ["web_search", "x_search"] }, query: "what are people saying?", response: { json: xSearchBody } },
	{ name: "xai filters in the prompt", provider: "xai", config: { xaiApiKey: "xai-key", xaiSearchModel: " grok-5 " }, query: "filtered", options: { recencyFilter: "week", domainFilter: ["example.com", "-spam.example"], numResults: 4 }, response: { json: xaiBody } },
	{ name: "xai registry login", provider: "xai", config: {}, query: "subscription", registry: { models: [{ provider: "xai", id: "grok-4.5" }], auth: { xai: { ok: true, apiKey: "grok-subscription", headers: { "X-Grok": "yes" } } } }, response: { json: xaiBody } },
	{ name: "xai http error", provider: "xai", config: { xaiApiKey: "xai-secret" }, query: "denied", response: { status: 401, text: "denied for xai-secret" } },
	{ name: "kimi registry login", provider: "kimi", config: {}, query: "Kimi Code Plan search", options: { numResults: 2, domainFilter: ["example.com", "-ads.example.com"] },
		registry: { models: [{ provider: "kimi-code", id: "compat" }, { provider: "kimi-coding", id: "primary" }], auth: { "kimi-coding": { ok: true, apiKey: "kimi-token", headers: { "X-Registry": "forwarded", "X-Nullable": null, authorization: "Bearer stale", "content-type": "text/plain" } } } }, response: { json: kimiBody } },
	{ name: "kimi bearer in header only", provider: "kimi", config: {}, query: "header auth", registry: { models: [{ provider: "kimi-coding", id: "primary" }], auth: { "kimi-coding": { ok: true, headers: { Authorization: "Bearer header-token" } } } }, response: { json: kimiBody } },
	{ name: "mistral default", provider: "mistral", config: { mistralApiKey: "mistral-key" }, query: "what shipped?", response: { json: mistralBody } },
	{ name: "mistral premium and filters", provider: "mistral", config: { mistralApiKey: "mistral-key", mistralSearchModel: " custom ", mistralSearchTool: "web_search_premium" }, query: "filtered query", options: { numResults: 3, recencyFilter: "week", domainFilter: [" docs.example.com ", "-ads.example.com"] }, response: { json: mistralBody } },
	{ name: "openai standalone key, default endpoint", provider: "openai", config: { openaiApiKey: "sk-standalone" }, query: "hosted search", options: { recencyFilter: "week", numResults: 3, domainFilter: ["example.com", "-spam.example"] }, response: { json: responsesBody } },
	{ name: "openai streamed response", provider: "openai", config: { openaiApiKey: "sk-standalone" }, query: "stream", response: { text: sse } },
	{ name: "openai alpha default endpoint", provider: "openai", config: { openaiApiKey: "sk-standalone", openaiUseAlphaSearch: true, openaiSearchModel: "gpt-5.6-luna" }, query: "alpha", options: { recencyFilter: "day", domainFilter: ["www.example.com"] }, response: { json: alphaBody } },
	{ name: "openai registry login picks the terra model", provider: "openai", config: {}, query: "registry", registry: { models: [openaiOfficial, { ...openaiOfficial, id: "gpt-5.7" }, { ...openaiOfficial, id: "gpt-5.5-pro" }], auth: { openai: { ok: true, apiKey: "sk-registry", headers: { "X-Org": "acme" } } } }, response: { json: responsesBody } },
	{ name: "openai codex login", provider: "openai", config: {}, query: "codex", registry: { models: [{ provider: "openai-codex", id: "gpt-5.6-terra", api: "openai-codex-responses", baseUrl: "https://chatgpt.com/backend-api" }], auth: { "openai-codex": { ok: true, apiKey: CODEX_KEY, headers: {} } } }, response: { json: responsesBody } },
	{ name: "openai codex alpha", provider: "openai", config: { openaiUseAlphaSearch: true }, query: "codex alpha", registry: { models: [{ provider: "openai-codex", id: "gpt-5.6-terra", api: "openai-codex-responses", baseUrl: "https://chatgpt.com/backend-api" }], auth: { "openai-codex": { ok: true, apiKey: CODEX_KEY, headers: { "OpenAI-Beta": "x", Session_ID: "s" } } } }, response: { json: alphaBody } },
	{ name: "openai opencode attribution", provider: "openai", config: { openaiSearchProviders: ["opencode-go"], openaiUseProviderBaseUrl: true }, query: "opencode", sessionId: "session-1",
		registry: { models: [{ provider: "opencode-go", id: "gpt-6-luna", api: "openai-responses", baseUrl: "https://opencode.ai/zen/go/v1" }, { provider: "opencode-go", id: "gpt-5.6-luna", api: "openai-responses", baseUrl: "https://opencode.ai/zen/go/v1" }], auth: { "opencode-go": { ok: true, apiKey: "oc-key", headers: { "x-existing": "kept", "X-OpenCode-Session": "stale" } } } }, response: { json: responsesBody } },
	{ name: "openai current model", provider: "openai-current", config: {}, query: "current model", currentModel: openaiOfficial, registry: { models: [openaiOfficial], auth: { openai: { ok: true, apiKey: "sk-current", headers: {} } } }, response: { json: responsesBody } },
	{ name: "openai http error", provider: "openai", config: { openaiApiKey: "sk-secret" }, query: "denied", response: { status: 429, text: "slow down sk-secret" } },
];

const dir = await mkdtemp(join(tmpdir(), "pigpen-model-provider-requests-"));
process.env.PI_CODING_AGENT_DIR = dir;
for (const name of Object.keys(process.env)) if (/_API_KEY$|_BASE_URL$/.test(name)) delete process.env[name];

const uuid = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;
const real = globalThis.fetch;
let counter = 0;

async function run(c) {
	await writeFile(join(dir, "web-search.json"), JSON.stringify(c.config));
	const requests = [];
	globalThis.fetch = async (url, init) => {
		requests.push({
			method: init.method,
			url: String(url),
			headers: Object.fromEntries([...new Headers(init.headers)].sort(([a], [b]) => (a < b ? -1 : 1)).map(([k, v]) => [k, v.replace(uuid, "<uuid>")])),
			body: String(init.body).replace(uuid, "<uuid>"),
		});
		if (c.response.json !== undefined) return new Response(JSON.stringify(c.response.json), { status: c.response.status ?? 200 });
		return new Response(c.response.text, { status: c.response.status ?? 200 });
	};
	let ctx;
	if (c.registry) {
		const models = c.registry.models;
		ctx = {
			model: c.currentModel ?? models[0],
			modelRegistry: {
				getAll: () => models,
				find: (provider, id) => models.find((m) => m.provider === provider && m.id === id),
				getApiKeyAndHeaders: async (m) => c.registry.auth[m.provider] ?? { ok: false, error: "no auth" },
			},
			sessionManager: { getSessionId: () => c.sessionId },
		};
	}
	const options = c.options ?? {};
	const suffix = `?case=${++counter}`;
	let result, error;
	const { activityMonitor } = await import(oracle("activity.ts"));
	activityMonitor.clear();
	try {
		if (c.provider === "xai") result = await (await import(oracle("xai-search.ts") + suffix)).searchWithXai(c.query, options, ctx);
		else if (c.provider === "kimi") result = await (await import(oracle("kimi-search.ts") + suffix)).searchWithKimi(c.query, options, ctx);
		else if (c.provider === "mistral") result = await (await import(oracle("mistral-search.ts") + suffix)).searchWithMistral(c.query, options);
		else if (c.provider === "openai-current") result = await (await import(oracle("openai-search.ts") + suffix)).searchWithCurrentModelOpenAI(c.query, options, ctx);
		else result = await (await import(oracle("openai-search.ts") + suffix)).searchWithOpenAI(c.query, options, ctx);
	} catch (err) {
		error = err.message;
	}
	// What the original's activity monitor holds after the call (activity.ts), without ids and times.
	const activity = activityMonitor.getEntries().map(({ type, query, url, status, error }) => ({ type, query, url, status, error }));
	return { requests, ...(error === undefined ? { result } : { error }), activity };
}

const recorded = [];
for (const c of cases) recorded.push({ ...c, ...(await run(c)) });
globalThis.fetch = real;

const text = JSON.stringify({ oracle: "pi-web-access 0.33.0", cases: recorded }, null, 1) + "\n";
if (process.argv.includes("--check")) {
	const current = await readFile(fixturePath, "utf8").catch(() => "");
	if (current !== text) {
		console.error("port/fixtures/model-provider-requests.json differs from what the original sends; rerun without --check");
		process.exit(1);
	}
	console.log(`checked ${recorded.length} cases`);
} else {
	await writeFile(fixturePath, text);
	console.log(`recorded ${recorded.length} cases`);
}
