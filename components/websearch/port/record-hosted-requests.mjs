// Records the requests the ORIGINAL hosted providers send, and what they return, for the providers
// whose endpoints are fixed (no configurable base URL), so a pigeq scenario server cannot receive
// their traffic. Each case runs the vendored original module in a child process with
// `globalThis.fetch` replaced by a recorder that answers canned responses, then the inputs and the
// observed requests and results are written to extensions/websearch/testdata/provider-wire.json. The Go twin
// (provider_wire_test.go) replays the same inputs against the port and compares request for request.
//
//   cd components/websearch/port && node record-hosted-requests.mjs          # rewrites provider-wire.json
//   node record-hosted-requests.mjs --check                                   # fails when the recording differs
//
// Run it with the oracle's node_modules in place (port/oracle/node_modules) and Node 22.6+.
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = join(here, "oracle");
const outFile = join(here, "..", "extensions", "websearch", "testdata", "provider-wire.json");

const ok = (body, status = 200) => ({ status, body });
const mcp = (result) => ok({ jsonrpc: "2.0", id: 1, result });

// module: file of the original; fn: exported function; args: its arguments after the query/url
// (for an extract the signal slot is skipped); lookup: the extract takes a DNS lookup seam.
const cases = [
	{ name: "tinyfish search with filters, recency and inline content", module: "tinyfish.ts", fn: "searchWithTinyFish", kind: "search", env: { TINYFISH_API_KEY: "wire-tinyfish-key" },
		args: ["tinyfish docs", { includeContent: true, numResults: 2, recencyFilter: "week", domainFilter: ["docs.tinyfish.ai", "-example.com"] }],
		responses: [
			ok({ query: "tinyfish docs", results: [{ title: "TinyFish Docs", snippet: "  Search   and fetch  ", url: "https://docs.tinyfish.ai/" }, { title: "", snippet: "x", url: "https://docs.tinyfish.ai/" }, { snippet: "no title", url: "https://example.org/b" }] }),
			ok({ results: [{ url: "https://docs.tinyfish.ai/", final_url: "https://docs.tinyfish.ai/", title: "TinyFish Docs", text: "# Full" }, { url: "https://example.org/b", text: { a: 1, b: [2] } }], errors: [] }),
		] },
	{ name: "tinyfish fetch with a purpose and a timeout", module: "tinyfish.ts", fn: "extractWithTinyFish", kind: "extract", env: { TINYFISH_API_KEY: "wire-tinyfish-key" },
		args: [{ prompt: "  Read the article  ", timeoutMs: 45000 }],
		url: "https://example.com/good",
		responses: [ok({ results: [{ url: "https://example.com/good", final_url: "https://example.com/good", title: " Good ", text: "  # Rendered body  " }], errors: [] })] },
	{ name: "search1api search with deep crawling", module: "search1api.ts", fn: "searchWithSearch1API", kind: "search", env: { SEARCH1API_KEY: "wire-search1api-key" },
		args: ["search1api docs", { includeContent: true, numResults: 3, recencyFilter: "month", domainFilter: ["https://www.search1api.com/docs", "-example.com"] }],
		responses: [ok({ searchParameters: {}, results: [{ title: "Docs", link: "https://www.search1api.com/docs", snippet: " a   b ", content: "# Full" }, { link: "  ", title: "dropped" }] })] },
	{ name: "search1api crawl", module: "search1api.ts", fn: "extractWithSearch1API", kind: "extract", env: { SEARCH1API_KEY: "wire-search1api-key" }, args: [{ timeoutMs: 45000 }],
		url: "https://example.com/article", responses: [ok({ crawlParameters: { url: "https://example.com/article" }, results: { title: " Example article ", link: "https://example.com/article", content: " # Article body " } })] },
	{ name: "searchinfinity search with filters and a business error", module: "searchinfinity.ts", fn: "searchWithSearchinfinity", kind: "search", env: { SEARCHINFINITY_API_KEY: "wire-searchinfinity-key" },
		args: ["searchinfinity docs", { numResults: 2, recencyFilter: "year", domainFilter: ["https://www.byteplus.com/docs", "a.example.com", "-example.com", "-b.example.com"] }],
		responses: [ok({ ResponseMetadata: { RequestId: "r" }, Result: { WebResults: [{ Title: "Docs", Url: "https://www.byteplus.com/docs", Snippet: " short ", Summary: " Model   summary " }, { Url: "https://x.example/y", Snippet: "only snippet" }] } })] },
	{ name: "searchinfinity business error", module: "searchinfinity.ts", fn: "searchWithSearchinfinity", kind: "search", env: { SEARCHINFINITY_API_KEY: "wire-searchinfinity-key" },
		args: ["bad key", {}], responses: [ok({ ResponseMetadata: { Error: { CodeN: 700429, Code: "700429", Message: "QPS Exceeded" } }, Result: null })] },
	{ name: "querit search and contents", module: "querit.ts", fn: "searchWithQuerit", kind: "search", env: { QUERIT_API_KEY: "wire-querit-key" },
		args: ["querit docs", { includeContent: true, numResults: 2, recencyFilter: "day", domainFilter: ["https://www.querit.ai/docs", "-example.com"] }],
		responses: [
			ok({ error_code: 200, error_msg: "", results: { result: [{ title: "Querit docs", url: "https://www.querit.ai/en/docs", snippet: " Real-time   search " }, { url: "https://www.querit.ai/two" }] } }),
			ok({ error_code: 200, error_msg: "", results: [{ id: "a", url: "https://www.querit.ai/en/docs", content: "# Doc", extrasMeta: { title: "Doc title" } }, { id: "b", url: "https://www.querit.ai/two", content: "  " }], statuses: [{ id: "a", status: "success" }, { id: "b", status: "failed" }] }),
		] },
	{ name: "querit contents extraction", module: "querit.ts", fn: "extractWithQuerit", kind: "extract", env: { QUERIT_API_KEY: "wire-querit-key" }, args: [{ timeoutMs: 45000 }],
		url: "https://example.com/article", responses: [ok({ error_code: 200, error_msg: "", results: [{ id: "x", url: "https://example.com/article", content: "# Article body", extrasMeta: { title: "Example article" } }], statuses: [{ id: "x", status: "success" }] })] },
	{ name: "querit API-level failure", module: "querit.ts", fn: "searchWithQuerit", kind: "search", env: { QUERIT_API_KEY: "wire-querit-key" },
		args: ["quota", {}], responses: [ok({ error_code: 429, error_msg: "quota exceeded", search_id: 301 })] },
	{ name: "parallel search with a source policy", module: "parallel.ts", fn: "searchWithParallel", kind: "search", env: { PARALLEL_API_KEY: "pk_live_wire_parallel_key" },
		args: ["parallel docs", { includeContent: true, numResults: 3, domainFilter: ["docs.parallel.ai", "-example.com"] }],
		responses: [ok({ results: [{ title: "Parallel Docs", url: "https://docs.parallel.ai/search", excerpts: ["First excerpt", "Second"] }, { url: "https://x.example/none", excerpts: [] }] })] },
	{ name: "parallel extract retries for full content", module: "parallel.ts", fn: "extractWithParallel", kind: "extract", env: { PARALLEL_API_KEY: "pk_live_wire_parallel_key" }, args: [{ prompt: " Read it " }],
		url: "https://example.com", responses: [ok({ results: [{ url: "https://example.com", title: "Short", excerpts: ["too short"] }] }), ok({ results: [{ url: "https://example.com", title: "Full", full_content: "# Full\n" + "x".repeat(600) }] })] },
	{ name: "parallel mcp search anonymous with filters", module: "parallel-mcp.ts", fn: "searchWithParallelMcp", kind: "search", env: {},
		args: ["docs", { numResults: 2, domainFilter: ["example.com", "-private.docs.example.com"], recencyFilter: "week", includeContent: true }],
		responses: [mcp({ structuredContent: { results: [{ title: "Allowed", url: "https://docs.example.com/a", excerpts: ["Allowed excerpt"] }, { title: "Excluded", url: "https://private.docs.example.com/b", excerpts: ["x"] }, { title: "Outside", url: "https://example.net/c", excerpts: ["y"] }] } })] },
	{ name: "parallel mcp text blocks with a key", module: "parallel-mcp.ts", fn: "searchWithParallelMcp", kind: "search", env: { PARALLEL_API_KEY: "pk_live_wire_parallel_key" }, args: ["text", {}],
		responses: [mcp({ content: [{ type: "text", text: "Title: One\nURL: https://example.com/1\nText: First result\n---\nTitle: Two\nURL: https://example.com/2\nText: Second result\n---" }] })] },
	{ name: "parallel mcp web_fetch", module: "parallel-mcp.ts", fn: "extractWithParallelMcp", kind: "extract", env: {}, args: [{ prompt: "  find the price  " }],
		url: "https://example.com/article", responses: [mcp({ structuredContent: { results: [{ url: "https://example.com/article", title: "Article", excerpts: [], full_content: "# Full article\n\nUseful content." }] } })] },
	{ name: "ollama search and fetch", module: "ollama.ts", fn: "searchWithOllama", kind: "search", env: { OLLAMA_API_KEY: "wire-ollama-key" }, args: ["cloud search", { numResults: 20, includeContent: true }],
		responses: [ok({ results: [{ title: "Ollama result", url: "https://example.com/ollama", content: "Ollama snippet" }, { title: "Empty", url: "https://example.com/empty", content: "  " }] })] },
	{ name: "ollama web fetch", module: "ollama.ts", fn: "extractWithOllama", kind: "extract", env: { OLLAMA_API_KEY: "wire-ollama-key" }, lookup: true, args: [{}],
		url: "https://example.com/ollama", responses: [ok({ title: "Fetched", content: "  Fetched markdown ", links: [] })] },
	{ name: "kagi search", module: "kagi.ts", fn: "searchWithKagi", kind: "search", env: { KAGI_API_KEY: "wire-kagi-key" }, args: ["premium search", { numResults: 3, includeContent: true }],
		responses: [ok({ data: { search: [{ title: "Kagi result", url: "https://example.com/kagi", snippet: "Kagi snippet", markdown: "Body" }] } })] },
	{ name: "kagi extract", module: "kagi.ts", fn: "extractWithKagi", kind: "extract", env: { KAGI_API_KEY: "wire-kagi-key" }, lookup: true, args: [{}],
		url: "https://example.com/kagi", responses: [ok({ data: [{ url: "https://example.com/kagi", title: "Kagi extract", markdown: "Kagi markdown" }] })] },
	{ name: "brightdata serp with filters", module: "brightdata.ts", fn: "searchWithBrightData", kind: "search", env: { BRIGHTDATA_API_KEY: "wire-bd-key", BRIGHTDATA_SERP_ZONE: "pi_serp" },
		args: ["vector databases", { numResults: 3, recencyFilter: "week", domainFilter: ["github.com", "example.com", "-gist.github.com"] }],
		responses: [ok({ organic: [{ link: "https://github.com/a/b", title: "Repo", description: "repo   description" }, { link: "https://gist.github.com/x", title: "Gist" }, { link: "https://example.com/c" }, { title: "no link" }] })] },
	{ name: "brightdata unlocker", module: "brightdata-unlocker.ts", fn: "extractWithBrightDataUnlocker", kind: "extract", env: { BRIGHTDATA_API_KEY: "wire-bd-key", BRIGHTDATA_UNLOCKER_ZONE: "pi_unlocker" }, lookup: true, args: [{}],
		url: "https://example.com/article", responses: [ok("# Unlocked Title\n\nUnlocked body.\n")] },
];

const lookupSrc = `async () => [{ address: "93.184.216.34", family: 4 }]`;

function run(c) {
	const home = mkdtempSync(join(tmpdir(), "wire-"));
	const agent = join(home, "agent");
	mkdirSync(agent, { recursive: true });
	writeFileSync(join(agent, "web-search.json"), "{}\n");
	const opts = JSON.stringify(c.args[c.args.length - 1]);
	const script = `
		const calls = [];
		const responses = ${JSON.stringify(c.responses)};
		let i = 0;
		globalThis.fetch = async (url, init = {}) => {
			calls.push({ method: init.method || "GET", url: String(url), headers: Object.fromEntries(new Headers(init.headers || {})), body: init.body ? JSON.parse(init.body) : null });
			const r = responses[Math.min(i++, responses.length - 1)];
			return new Response(typeof r.body === "string" ? r.body : JSON.stringify(r.body), { status: r.status });
		};
		const mod = await import(${JSON.stringify(join(oracle, c.module))});
		let result = null, error = null;
		try {
			${c.kind === "search"
				? `result = await mod.${c.fn}(${JSON.stringify(c.args[0])}, ${opts});`
				: `result = await mod.${c.fn}(${JSON.stringify(c.url)}, undefined, { ...${opts}${c.lookup ? `, lookup: ${lookupSrc}` : ""} });`}
		} catch (e) { error = e.message; }
		console.log(JSON.stringify({ calls, result, error }));
	`;
	const env = { PATH: process.env.PATH, HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: agent, ...c.env };
	const r = spawnSync(process.execPath, ["--input-type=module"], { input: script, encoding: "utf8", env, maxBuffer: 4 << 20 });
	if (r.status !== 0) throw new Error(`${c.name}: ${r.stderr}`);
	return JSON.parse(r.stdout.trim().split("\n").pop());
}

const recorded = cases.map((c) => {
	const out = run(c);
	return { name: c.name, kind: c.kind, fn: c.fn, env: c.env, args: c.kind === "search" ? c.args : [c.url, ...c.args], responses: c.responses, expect: out };
});
const text = JSON.stringify(recorded, null, 1) + "\n";
if (process.argv.includes("--check")) {
	if (readFileSync(outFile, "utf8") !== text) { console.error("provider-wire.json differs from a fresh recording"); process.exit(1); }
	console.log(`provider-wire.json matches (${recorded.length} cases)`);
} else {
	writeFileSync(outFile, text);
	console.log(`wrote ${outFile} (${recorded.length} cases)`);
}
