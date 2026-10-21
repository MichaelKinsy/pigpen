// Records what the ORIGINAL provider modules (port/oracle/*.ts) send and return for the inputs the
// Go twins use, so the twins compare against the original's behaviour rather than a reading of it.
//
//   node port/record-provider-requests.mjs            # rewrites extensions/websearch/testdata/provider-requests.json
//
// Every case runs in its own Node process (the provider modules cache their configuration and
// read PI_CODING_AGENT_DIR at import time). In that process globalThis.fetch is a recorder that
// answers with the canned responses of the case; the script writes the original's requests
// (method, URL, lower-cased headers, body, redirect mode) and its outcome (the returned
// SearchResponse or the error message). The Go twin (provider_fixtures_test.go) replays each case
// against the port with the same canned responses and requires identical requests and outcomes.
//
// Limit: these providers call fixed https endpoints with no base-URL setting, so a process-level
// differential run (pigeq, a fake server) cannot reach them; this fixture is the oracle-derived
// proof for their wire shape. SearXNG has a base URL and also has pigeq scenarios.
//
// Run it with the oracle's node_modules and Node 24 (type stripping), e.g. in the tern-ci
// container: `cd port && node record-provider-requests.mjs`.
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = join(here, "oracle");
const MCP_URL = "https://agent-toolkit.app.baizhi.cloud/mcp";

const r = (body, status = 200, headers = {}) => ({ status, body, headers });
const json = (value, status = 200) => ({ status, json: value });
const serpEnvelope = (organic) => ({
	search_metadata: { id: "01KMMKX1EC62VPCD7FW8YZ8QVS", status: "completed", total_time_taken: 2 },
	search_parameters: { engine: "google_search", q: "example query" },
	total_credits_used: 1,
	organic_results: organic ?? [
		{ position: 1, title: "First result", link: "https://www.example.com/first", snippet: "First result snippet." },
		{ position: 2, title: null, link: "https://sub.other-domain.org/second", snippet: "Second result snippet with no title." },
	],
});
const googleOrganic = [
	{ title: "Allowed", link: "https://docs.example.com/a", snippet: "result" },
	{ title: "Excluded", link: "https://private.docs.example.com/b", snippet: "private" },
	{ title: "Outside", link: "https://example.net/c", snippet: "outside" },
];

export const cases = [
	// ── serper ──
	{ id: "serper-env-credential", module: "serper.ts", fn: "searchWithSerper", env: { SERPER_API_KEY: "serper-test-key" }, query: "google query", options: { numResults: 4 },
		responses: [json({ organic: [{ title: "Serper result", link: "https://example.com/serper", snippet: "Google result", position: 1 }] })] },
	{ id: "serper-filters", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "serper-test-key" }, query: "docs", options: { numResults: 3, domainFilter: ["example.com", "-private.docs.example.com"], recencyFilter: "week" },
		responses: [json({ organic: googleOrganic })] },
	{ id: "serper-multi-include", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "serper-test-key" }, query: "docs", options: { numResults: 19, domainFilter: ["a.example.com", "b.example.org", "A.Example.com", "not a domain"], recencyFilter: "year" },
		responses: [json({ organic: [{ title: "A", link: "https://a.example.com/1", snippet: "a" }, { title: "B", link: "https://x.b.example.org/2", snippet: "b" }, { title: "C", link: "https://c.example.net/3", snippet: "c" }] })] },
	{ id: "serper-entries", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "serper-test-key" }, query: "entries", options: {},
		responses: [json({ organic: [null, 5, { title: "  Trim me  ", link: "https://example.com/1", snippet: "s" }, { link: "https://example.com/2" }, { title: "x", link: 5 }, { title: "x", link: "" }, { title: "Snip", link: "https://example.com/3", snippet: 7 }] })] },
	{ id: "serper-401-redacted", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "serper-secret" }, query: "redact", options: {}, responses: [r("secret serper-secret", 401)] },
	{ id: "serper-long-error", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "serper-secret" }, query: "long", options: {}, responses: [r("e".repeat(400), 503)] },
	{ id: "serper-missing-key", module: "serper.ts", fn: "searchWithSerper", config: {}, query: "q", options: {}, responses: [] },
	{ id: "serper-bad-envelope", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "k" }, query: "q", options: {}, responses: [json({ results: [] })] },
	{ id: "serper-array-envelope", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "k" }, query: "q", options: {}, responses: [json([])] },
	{ id: "serper-invalid-json", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "k" }, query: "q", options: {}, responses: [r("not json")] },
	{ id: "serper-config-root", module: "serper.ts", fn: "searchWithSerper", configRaw: "[]", query: "q", options: {}, responses: [] },
	{ id: "serper-recency-day", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "k-day" }, query: "q", options: { recencyFilter: "day" }, responses: [json({ organic: [] })] },
	{ id: "serper-recency-month", module: "serper.ts", fn: "searchWithSerper", config: { serperApiKey: "k-month" }, query: "q", options: { recencyFilter: "month" }, responses: [json({ organic: [] })] },
	// ── serpapi ──
	{ id: "serpapi-env-filters", module: "serpapi.ts", fn: "searchWithSerpApi", env: { SERPAPI_KEY: "serpapi-test-key" }, query: "google query", options: { numResults: 3, domainFilter: ["example.com", "-private.docs.example.com"], recencyFilter: "week" },
		responses: [json({ organic_results: googleOrganic })] },
	{ id: "serpapi-error-envelope-redacted", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "unused-config-key" }, env: { SERPAPI_KEY: "resolved-serpapi-secret" }, query: "redact envelope", options: {},
		responses: [json({ error: "invalid api_key resolved-serpapi-secret" })] },
	{ id: "serpapi-401-redacted", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "serpapi-secret" }, query: "redact", options: {}, responses: [r("invalid serpapi-secret", 401)] },
	{ id: "serpapi-missing-organic", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "k" }, query: "q", options: {}, responses: [json({})] },
	{ id: "serpapi-invalid-json", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "k" }, query: "q", options: {}, responses: [r("{")] },
	{ id: "serpapi-timeout", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "k" }, query: "q", options: {}, patchTimeout: true, responses: [{ throw: "timeout" }] },
	{ id: "serpapi-caller-abort", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "k" }, query: "q", options: {}, preAborted: true, responses: [{ throw: "abort" }] },
	{ id: "serpapi-network", module: "serpapi.ts", fn: "searchWithSerpApi", config: { serpapiApiKey: "serpapi-secret" }, query: "q", options: {}, responses: [{ throw: "plain", message: "connect failed for serpapi-secret" }] },
	// ── serpbase ──
	{ id: "serpbase-map", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "serpbase-test-key" }, query: "google query", options: { numResults: 4, recencyFilter: "week", domainFilter: ["example.com"] },
		responses: [json({ organic_results: [{ title: "SerpBase result", link: "https://example.com/result", snippet: "Google snippet" }] })] },
	{ id: "serpbase-default-count-and-fields", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k" }, query: "q", options: {},
		responses: [json({ results: [{ title: "  Untrimmed  ", url: "https://example.com/u", description: "desc" }, { link: "https://example.com/n", snippet: "s" }, { title: "none" }] })] },
	{ id: "serpbase-error-envelope", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "secret-sb" }, query: "q", options: {}, responses: [json({ error: "bad key secret-sb", status: 403 })] },
	{ id: "serpbase-error-envelope-no-status", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k" }, query: "q", options: {}, responses: [json({ error: "nope" })] },
	{ id: "serpbase-bad-envelope", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k" }, query: "q", options: {}, responses: [json({ status: 200 })] },
	{ id: "serpbase-429", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k" }, query: "q", options: { numResults: 50 }, responses: [r("slow down", 429)] },
	{ id: "serpbase-missing-key", module: "serpbase.ts", fn: "searchWithSerpBase", config: {}, query: "q", options: {}, responses: [] },
	{ id: "serpbase-recency-day-and-year", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k-sb" }, query: "q", options: { recencyFilter: "day" }, responses: [json({ organic_results: [] })] },
	{ id: "serpbase-recency-month", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k-sb" }, query: "q", options: { recencyFilter: "month" }, responses: [json({ organic_results: [] })] },
	{ id: "serpbase-recency-year", module: "serpbase.ts", fn: "searchWithSerpBase", config: { serpbaseApiKey: "k-sb" }, query: "q", options: { recencyFilter: "year" }, responses: [json({ organic_results: [] })] },
	// ── serply ──
	{ id: "serply-env-filters", module: "serply.ts", fn: "searchWithSerply", env: { SERPLY_API_KEY: "serply-test-key" }, query: "google query", options: { numResults: 3, domainFilter: ["example.com", "-private.docs.example.com"], recencyFilter: "week" },
		responses: [json({ results: [{ title: "Allowed", link: "https://docs.example.com/a", description: "result" }, { title: "Excluded", link: "https://private.docs.example.com/b", description: "private" }, { title: "Outside", link: "https://example.net/c", description: "outside" }] })] },
	{ id: "serply-redirects", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "serply-test-key" }, query: "redirect", options: {},
		responses: [r(null, 302, { location: "/v1/redirected" }), r(null, 307, { location: "https://results.example/search" }), json({ results: [{ title: "Result", link: "https://example.com/result" }] })] },
	{ id: "serply-links", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "serply-test-key" }, query: "links", options: { numResults: 10 },
		responses: [json({ results: [{ title: "HTTPS", link: "https://example.com/secure" }, { title: "HTTP", link: "http://example.com/plain" }, { title: "Host only", link: "https://Example.COM" }, { title: "Relative", link: "/relative" }, { title: "Malformed", link: "not a url" }, { title: "File", link: "file:///tmp/result" }, { title: "Data", link: "data:text/plain,result" }] })] },
	{ id: "serply-detail-redacted", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "unused-config-key" }, env: { SERPLY_API_KEY: "resolved-serply-secret" }, query: "redact envelope", options: {},
		responses: [json({ detail: "Invalid API key resolved-serply-secret" })] },
	{ id: "serply-401-redacted", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "serply-secret" }, query: "redact", options: {}, responses: [r("invalid serply-secret", 401)] },
	{ id: "serply-timeout", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "k" }, query: "q", options: {}, patchTimeout: true, responses: [{ throw: "timeout" }] },
	{ id: "serply-caller-abort", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "k" }, query: "q", options: {}, preAborted: true, responses: [{ throw: "abort" }] },
	{ id: "serply-missing-results", module: "serply.ts", fn: "searchWithSerply", config: { serplyApiKey: "k" }, query: "q", options: {}, responses: [json({ organic: [] })] },
	// ── valyu ──
	{ id: "valyu-content-bounds", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "valyu-test-key" }, query: "research", options: { numResults: 7, includeContent: true },
		responses: [json({ success: true, results: [{ title: "Valyu result", url: "https://example.com/valyu", description: "description", content: "x".repeat(5000), relevance_score: 1 }] })] },
	{ id: "valyu-filters-recency", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "valyu-test-key" }, query: "research", options: { domainFilter: ["docs.example.com", "-private.example.com"], recencyFilter: "week" }, now: "2026-03-10T12:00:00.000Z",
		responses: [json({ success: true, results: [] })] },
	{ id: "valyu-entries", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k" }, query: "q", options: { numResults: 2, includeContent: true },
		responses: [json({ success: true, results: [null, { title: "  A  \n title ", url: " https://example.com/a ", description: "d  d", content: "c\n\nc" }, { url: "" }, { url: "https://example.com/b", description: "only description" }, { url: "https://example.com/c" }] })] },
	{ id: "valyu-not-success", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k" }, query: "q", options: {}, responses: [json({ success: false, results: [] })] },
	{ id: "valyu-missing-results", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k" }, query: "q", options: {}, responses: [json({ success: true })] },
	{ id: "valyu-401-redacted", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "valyu-secret" }, query: "redact", options: {}, responses: [r("secret valyu-secret", 401)] },
	{ id: "valyu-recency-day", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k-v" }, query: "q", options: { recencyFilter: "day" }, now: "2026-03-10T12:00:00.000Z", responses: [json({ success: true, results: [] })] },
	{ id: "valyu-recency-month", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k-v" }, query: "q", options: { recencyFilter: "month" }, now: "2026-03-10T12:00:00.000Z", responses: [json({ success: true, results: [] })] },
	{ id: "valyu-recency-year", module: "valyu.ts", fn: "searchWithValyu", config: { valyuApiKey: "k-v" }, query: "q", options: { recencyFilter: "year" }, now: "2026-03-10T12:00:00.000Z", responses: [json({ success: true, results: [] })] },
	// ── bocha ──
	{ id: "bocha-filters", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-test-key" }, query: "bocha query", options: { numResults: 5, recencyFilter: "week", domainFilter: ["example.com", "-private.docs.example.com"] },
		responses: [json({ code: 200, data: { webPages: { value: [{ name: "Bocha result", url: "https://docs.example.com/bocha", summary: "Bocha snippet" }, { name: "Blocked result", url: "https://private.docs.example.com/bocha", summary: "Blocked snippet" }, { name: "Outside result", url: "https://outside.example.net/bocha", summary: "Outside snippet" }] } } })] },
	{ id: "bocha-api-error-redacted", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-test-key" }, query: "bad query", options: {}, responses: [json({ code: 500, msg: "invalid request bocha-test-key" })] },
	{ id: "bocha-string-code-and-fields", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "k" }, query: "q", options: {},
		responses: [json({ code: "200", data: { webPages: { value: [{ link: "https://example.com/l", title: " T ", snippet: " S " }, { href: "https://example.com/h", content: "C" }, { name: "no url" }, [], null, { url: "https://example.com/u", description: "D" }] } } })] },
	{ id: "bocha-code-without-msg", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-k" }, query: "q", options: {}, responses: [json({ code: 403 })] },
	{ id: "bocha-missing-array", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "k" }, query: "q", options: {}, responses: [json({ data: { webPages: {} } })] },
	{ id: "bocha-no-code", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "k" }, query: "q", options: { numResults: 2, recencyFilter: "month" }, responses: [json({ data: { webPages: { value: [{ url: "https://a.example.com/1" }, { url: "https://a.example.com/2" }, { url: "https://a.example.com/3" }] } } })] },
	{ id: "bocha-401", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-secret" }, query: "q", options: {}, responses: [r("bad bocha-secret", 401)] },
	{ id: "bocha-freshness-day", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-k" }, query: "q", options: { recencyFilter: "day" }, responses: [json({ code: 200, data: { webPages: { value: [] } } })] },
	{ id: "bocha-freshness-year", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-k" }, query: "q", options: { recencyFilter: "year" }, responses: [json({ code: 200, data: { webPages: { value: [] } } })] },
	{ id: "bocha-freshness-none", module: "bocha.ts", fn: "searchWithBocha", config: { bochaApiKey: "bocha-k" }, query: "q", options: {}, responses: [json({ code: 200, data: { webPages: { value: [] } } })] },
	// ── you ──
	{ id: "you-map", module: "you.ts", fn: "searchWithYou", env: { YDC_API_KEY: "you-test-key" }, query: "you query", options: { numResults: 3, recencyFilter: "week", domainFilter: ["example.com", "-private.example.com"] },
		responses: [json({ results: { web: [{ title: "Docs", url: "https://docs.example.com/a", description: "described" }, { title: "Snippet only", url: "https://example.com/b", snippets: ["first snippet"] }, { title: "Not a link", url: "not-a-url" }, { title: "Script", url: "javascript:alert(1)" }, { url: "https://example.com", description: "   ", snippets: [3] }] } })] },
	{ id: "you-401-redacted", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "you-secret" }, query: "redact", options: {}, responses: [r("invalid you-secret", 401)] },
	{ id: "you-invalid-response", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "k" }, query: "q", options: {}, responses: [json({ results: {} })] },
	{ id: "you-invalid-json", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "k" }, query: "q", options: {}, responses: [r("nope")] },
	{ id: "you-timeout", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "k" }, query: "q", options: {}, patchTimeout: true, responses: [{ throw: "timeout" }] },
	{ id: "you-caller-abort", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "k" }, query: "q", options: {}, preAborted: true, responses: [{ throw: "abort" }] },
	{ id: "you-redirect-strips-key", module: "you.ts", fn: "searchWithYou", config: { youApiKey: "k" }, query: "q", options: {}, responses: [r(null, 307, { location: "https://other.example/search" }), json({ results: { web: [] } })] },
	{ id: "you-missing-key", module: "you.ts", fn: "searchWithYou", config: {}, query: "q", options: {}, responses: [] },
	// ── anysearch ──
	{ id: "anysearch-anonymous", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "anonymous", options: { numResults: 3, recencyFilter: "week", domainFilter: ["example.com"] },
		responses: [json({ code: 0, data: { results: [{ title: "AnySearch result", url: "https://example.com/result", snippet: "A useful snippet", content: "full page" }], metadata: {} } })] },
	{ id: "anysearch-include-content", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "anonymous", options: { includeContent: true },
		responses: [json({ code: 0, data: { results: [{ title: "AnySearch result", url: "https://example.com/result", snippet: "A useful snippet", content: "full page" }], metadata: {} } })] },
	{ id: "anysearch-null-content", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "partial content", options: { includeContent: true },
		responses: [json({ code: 0, data: { results: [{ title: "Missing content", url: "https://example.com/missing", snippet: "missing" }, { title: "Null content", url: "https://example.com/null", snippet: "null", content: null }, { title: "Full content", url: "https://example.com/full", snippet: "full", content: "page body" }], metadata: {} } })] },
	{ id: "anysearch-invalid-content", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "invalid content", options: {},
		responses: [json({ code: 0, data: { results: [{ title: "Invalid content", url: "https://example.com/invalid", snippet: "invalid", content: 123 }], metadata: {} } })] },
	{ id: "anysearch-keyed", module: "anysearch.ts", fn: "searchWithAnySearch", config: { anysearchApiKey: "anysearch-key" }, query: "keyed", options: { numResults: 1 },
		responses: [json({ code: 0, data: { results: [{ title: "a", url: "https://example.com/a", snippet: "s" }, { title: "b", url: "https://example.com/b", snippet: "s" }], metadata: {} } })] },
	{ id: "anysearch-no-metadata", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "malformed", options: {}, responses: [json({ code: 0, data: { results: [] } })] },
	{ id: "anysearch-bad-code", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "q", options: {}, responses: [json({ code: 1, data: { results: [], metadata: {} } })] },
	{ id: "anysearch-empty-url", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "q", options: {}, responses: [json({ code: 0, data: { results: [{ title: "t", url: "", snippet: "s" }], metadata: {} } })] },
	{ id: "anysearch-bad-fields", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "q", options: {}, responses: [json({ code: 0, data: { results: [5], metadata: {} } })] },
	{ id: "anysearch-402", module: "anysearch.ts", fn: "searchWithAnySearch", config: {}, query: "q", options: {}, responses: [r("quota", 402)] },
	{ id: "anysearch-401-redacted", module: "anysearch.ts", fn: "searchWithAnySearch", config: { anysearchApiKey: "anysearch-secret" }, query: "redact", options: {}, responses: [r("secret anysearch-secret", 401)] },
	{ id: "anysearch-config-root", module: "anysearch.ts", fn: "searchWithAnySearch", configRaw: "null", query: "malformed config", options: {}, responses: [] },
	// ── xcrawl ──
	{ id: "xcrawl-map", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-test-key" }, query: "research", options: { numResults: 7 }, responses: [json(serpEnvelope())] },
	{ id: "xcrawl-relative-links", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-test-key" }, query: "links", options: {},
		responses: [json(serpEnvelope([{ position: 1, title: "Redirect result", link: "/goto?url=CAESAAAA", snippet: "Obfuscated link." }, { position: 2, title: null, link: "https://www.example.com/absolute", snippet: "Absolute link." }]))] },
	{ id: "xcrawl-blank-link", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-test-key" }, query: "blank link", options: {}, responses: [json(serpEnvelope([{ position: 1, title: "Blank link", link: "   ", snippet: "Invalid link." }]))] },
	{ id: "xcrawl-include-filter", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { domainFilter: ["example.com"] }, responses: [json(serpEnvelope())] },
	{ id: "xcrawl-exclude-filter", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { domainFilter: ["-example.com"] }, responses: [json(serpEnvelope())] },
	{ id: "xcrawl-fuzzy-filter", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { domainFilter: [" https://www.Example.COM/path "] }, responses: [json(serpEnvelope())] },
	{ id: "xcrawl-www-include", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { domainFilter: ["www.example.com"] },
		responses: [json(serpEnvelope([{ position: 1, title: "WWW result", link: "https://www.example.com/first", snippet: "WWW result snippet." }, { position: 2, title: "Blog result", link: "https://blog.example.com/second", snippet: "Blog result snippet." }]))] },
	{ id: "xcrawl-www-exclude", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { domainFilter: ["-www.example.com"] },
		responses: [json(serpEnvelope([{ position: 1, title: "WWW result", link: "https://www.example.com/first", snippet: "WWW result snippet." }, { position: 2, title: "Blog result", link: "https://blog.example.com/second", snippet: "Blog result snippet." }]))] },
	{ id: "xcrawl-cap", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: { numResults: 1 },
		responses: [json(serpEnvelope([1, 2, 3].map((n) => ({ position: n, title: `Result ${n}`, link: `https://www.example.com/${n}`, snippet: `Snippet ${n}` }))))] },
	{ id: "xcrawl-401-message", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-secret-key" }, query: "boom", options: {}, responses: [json({ message: "invalid api key" }, 401)] },
	{ id: "xcrawl-401-error-field", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-secret-key" }, query: "boom", options: {}, responses: [json({ error: "denied xc-secret-key" }, 403)] },
	{ id: "xcrawl-500-text", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-secret-key" }, query: "boom", options: {}, responses: [r("upstream xc-secret-key down", 502)] },
	{ id: "xcrawl-bad-status", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "bad status", options: {}, responses: [json({ search_metadata: { status: "failed" } })] },
	{ id: "xcrawl-missing-results", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "missing results", options: {}, responses: [json({ search_metadata: { status: "completed" } })] },
	{ id: "xcrawl-bad-entry", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: {}, responses: [json({ search_metadata: {}, organic_results: [[]] })] },
	{ id: "xcrawl-bad-title", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: {}, responses: [json({ search_metadata: {}, organic_results: [{ link: "https://a.example.com", title: 5 }] })] },
	{ id: "xcrawl-bad-snippet", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: {}, responses: [json({ search_metadata: {}, organic_results: [{ link: "https://a.example.com", snippet: 5 }] })] },
	{ id: "xcrawl-bad-metadata", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: {}, responses: [json({ organic_results: [] })] },
	{ id: "xcrawl-invalid-json", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "k" }, query: "q", options: {}, responses: [r("nope")] },
	{ id: "xcrawl-timeout", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-test-key" }, query: "slow", options: {}, patchTimeout: true, responses: [{ throw: "timeout" }] },
	{ id: "xcrawl-caller-abort", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-test-key" }, query: "cancel", options: {}, preAborted: true, responses: [{ throw: "timeout" }] },
	{ id: "xcrawl-network-redacted", module: "xcrawl.ts", fn: "searchWithXCrawl", config: { xcrawlApiKey: "xc-secret-key" }, query: "q", options: {}, responses: [{ throw: "plain", message: "refused xc-secret-key" }] },
	{ id: "xcrawl-missing-key", module: "xcrawl.ts", fn: "searchWithXCrawl", config: {}, query: "q", options: {}, responses: [] },
	// ── jina search ──
	{ id: "jina-filters-content", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "synthetic-jina-test-key" }, query: "jina search docs", options: { includeContent: true, numResults: 2, recencyFilter: "week", domainFilter: ["https://docs.jina.ai/search", "-reddit.com"] },
		responses: [json({ code: 200, status: 20000, data: [{ title: "Jina Docs", url: "https://docs.jina.ai/search", description: "  Search   docs. ", content: "# Full Jina docs" }, { title: "Duplicate", url: "https://docs.jina.ai/search", description: "duplicate", content: "duplicate" }, { title: "Jina Reader", url: "https://jina.ai/reader", description: "Reader docs", content: "# Reader docs" }] })] },
	{ id: "jina-array-response", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "synthetic-jina-test-key" }, query: "basic search", options: { numResults: 99 },
		responses: [json([{ title: "Result", url: "https://example.com/result", description: "Result snippet", content: "must stay hidden" }])] },
	{ id: "jina-query-encoding", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "  what's up? ünï/#x  ", options: { domainFilter: ["a.example.com", "b.example.org", "-c.example.net"] },
		responses: [json({ code: 200, data: [{ url: "https://a.example.com/x" }, { title: "  ", url: " https://b.example.org/y ", description: 5 }, { url: "https://other.example.com/z" }, null] })] },
	{ id: "jina-401-redacted", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "synthetic-jina-secret" }, query: "redact me", options: {}, responses: [json({ detail: "rejected synthetic-jina-secret" }, 401)] },
	{ id: "jina-invalid-json", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "synthetic-jina-secret" }, query: "invalid json", options: {}, responses: [r("synthetic-jina-secret malformed")] },
	{ id: "jina-envelope-429", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "activity envelope failure", options: {}, responses: [json({ code: 429, message: "quota exhausted", data: [] })] },
	{ id: "jina-envelope-no-data", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "q", options: {}, responses: [json({ code: 200 })] },
	{ id: "jina-scalar-response", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "q", options: {}, responses: [json(5)] },
	{ id: "jina-timeout", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "q", options: {}, patchTimeout: true, responses: [{ throw: "timeout" }] },
	{ id: "jina-body-timeout", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "q", options: {}, patchTimeout: true, responses: [{ bodyThrow: "timeout" }] },
	{ id: "jina-caller-abort", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "k" }, query: "q", options: {}, preAborted: true, responses: [{ throw: "abort" }] },
	{ id: "jina-network-redacted", module: "jina-search.ts", fn: "searchWithJina", env: { JINA_API_KEY: "jina-net-secret" }, query: "q", options: {}, responses: [{ throw: "plain", message: "refused jina-net-secret" }] },
	{ id: "jina-missing-key", module: "jina-search.ts", fn: "searchWithJina", config: {}, query: "q", options: {}, responses: [] },
	// ── searxng (base URL configurable; also has pigeq scenarios) ──
	{ id: "searxng-direct", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/", ssrf: { allowRanges: ["127.0.0.1"] } }, query: "self hosted", options: { domainFilter: ["example.com", "-private.example.net"], numResults: 2, recencyFilter: "month" },
		responses: [json({ answers: ["SearXNG answer", "  ", 5], results: [{ title: "Allowed", url: "https://docs.example.com/a", content: "allowed" }, { title: "Blocked", url: "https://private.example.net/b", content: "blocked" }, { url: "https://example.com/c" }] })] },
	{ id: "searxng-headers", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/", searxngHeaders: { "CF-Access-Client-Id": "client-id.access", "CF-Access-Client-Secret": "client-secret", "X-Empty-Skip": 123, "X-Bad-Value": "bad\r\nvalue", "Bad Name": "nope" }, ssrf: { allowRanges: ["127.0.0.1"] } }, query: "headers", options: {},
		responses: [json({ results: [{ title: "Allowed", url: "https://docs.example.com/a", content: "allowed" }] })] },
	{ id: "searxng-accept-override", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/", searxngHeaders: { accept: "application/custom-json" }, ssrf: { allowRanges: ["127.0.0.1"] } }, query: "headers", options: {}, responses: [json({ results: [] })] },
	{ id: "searxng-redirect-strips-headers", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/", searxngHeaders: { "CF-Access-Client-Id": "client-id.access", "CF-Access-Client-Secret": "client-secret" }, ssrf: { allowRanges: ["127.0.0.1"] } }, query: "headers redirect", options: {},
		responses: [r(null, 302, { location: "http://127.0.0.1:8444/search" }), json({ results: [{ title: "Allowed", url: "https://docs.example.com/a", content: "allowed" }] })] },
	{ id: "searxng-redirect-private", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/", ssrf: { allowRanges: ["127.0.0.1"] } }, query: "redirect", options: {}, responses: [r(null, 302, { location: "http://127.0.0.2:8443/search" })] },
	{ id: "searxng-error-status", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443/base/", ssrf: { allowRanges: ["127.0.0.1"] } }, query: "boom", options: {}, responses: [r("e".repeat(400), 500)] },
	{ id: "searxng-invalid-json", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:8443", ssrf: { allowRanges: ["127.0.0.1"] } }, query: "boom", options: {}, responses: [r("<html>")] },
	{ id: "searxng-missing-base", module: "searxng.ts", fn: "searchWithSearXNG", config: {}, query: "q", options: {}, responses: [] },
	{ id: "searxng-base-with-credentials", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://user:pw@127.0.0.1:8443/", ssrf: { allowRanges: ["127.0.0.1"] } }, query: "q", options: {}, responses: [] },
	{ id: "searxng-env-base", module: "searxng.ts", fn: "searchWithSearXNG", config: { searxngBaseUrl: "http://127.0.0.1:1/" }, env: { SEARXNG_BASE_URL: "http://93.184.216.34:8080/x/?a=1#h" }, query: "q", options: {}, responses: [json({ results: [] })] },
	// ── baizhi (MCP over streamable HTTP) ──
	{ id: "baizhi-json", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "  中文 query  ", options: { numResults: 3, recencyFilter: "week", domainFilter: ["example.com", "-private.example.com", "127.0.0.1"] }, baizhi: "json", unordered: true },
	{ id: "baizhi-text", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "text", unordered: true },
	{ id: "baizhi-sse", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "sse", unordered: true },
	{ id: "baizhi-redact", module: "baizhi.ts", fn: "searchWithBaizhi", config: { baizhiApiKey: "$SYNTHETIC_BAIZHI_NAMED" }, env: { SYNTHETIC_BAIZHI_NAMED: "synthetic-baizhi-credential", BAIZHI_API_KEY: "wrong-fallback" }, query: "q", options: {}, baizhi: "redact", unordered: true },
	{ id: "baizhi-tool-error", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "tool-error", unordered: true },
	{ id: "baizhi-empty", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "empty", unordered: true },
	{ id: "baizhi-http401", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "wrong-synthetic" }, query: "q", options: {}, baizhi: "http401", unordered: true },
	{ id: "baizhi-http503", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "http503", unordered: true },
	{ id: "baizhi-redirect", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "redirect", unordered: true },
	{ id: "baizhi-network", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "network", unordered: true },
	{ id: "baizhi-invalid-json", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-baizhi-credential" }, query: "q", options: {}, baizhi: "invalid-json", unordered: true },
	{ id: "baizhi-missing-key", module: "baizhi.ts", fn: "searchWithBaizhi", config: {}, query: "q", options: {}, baizhi: "json" },
	{ id: "baizhi-empty-query", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "k" }, query: " ", options: {}, baizhi: "json" },
	{ id: "baizhi-invalid-filter", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "k" }, query: "q", options: { domainFilter: ["invalid"] }, baizhi: "json" },
	{ id: "baizhi-pre-aborted", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "k" }, query: "q", options: {}, preAborted: true, baizhi: "json" },
	{ id: "baizhi-malformed-credential", module: "baizhi.ts", fn: "searchWithBaizhi", env: { BAIZHI_API_KEY: "synthetic-invalid-header\nprivate-value" }, query: "q", options: {}, baizhi: "json" },
	{ id: "baizhi-env-empty-named", module: "baizhi.ts", fn: "searchWithBaizhi", config: { baizhiApiKey: "$SYNTHETIC_BAIZHI_NAMED" }, env: { BAIZHI_API_KEY: "k" }, query: "q", options: {}, baizhi: "json" },
	{ id: "baizhi-config-root", module: "baizhi.ts", fn: "searchWithBaizhi", configRaw: "[1]", env: { BAIZHI_API_KEY: "k" }, query: "q", options: {}, baizhi: "json" },
];

// ── activity outcomes ──
// The entries the original's activity monitor holds depend on how the call ends: a status, an error message, an
// abort (status 0) or an entry left open. Every provider function gets the same set of endings so that each call
// site of the port that logs one is replayed against the original's entries.
const exaResults = [{ title: "Exa result", url: "https://example.com/exa", highlights: ["highlight"] }];
cases.push(
	{ id: "brave-act-ok", module: "brave.ts", fn: "searchWithBrave", env: { BRAVE_API_KEY: "brave-act-key" }, query: "brave query", options: {},
		responses: [json({ web: { results: [{ title: "Brave result", url: "https://example.com/brave", description: "Brave snippet" }] } })] },
	{ id: "perplexity-act-ok", module: "perplexity.ts", fn: "searchWithPerplexity", env: { PERPLEXITY_API_KEY: "pplx-act-key" }, query: "pplx query", options: {},
		responses: [json({ choices: [{ message: { content: "Answer [1]" } }], citations: ["https://example.com/pplx"] })] },
	{ id: "serpdive-act-ok", module: "serpdive.ts", fn: "searchWithSerpdive", env: { SERPDIVE_API_KEY: "serpdive-act-key" }, query: "serpdive query", options: {},
		responses: [json({ answer: "Answer", results: [{ title: "SERPdive result", url: "https://example.com/serpdive", snippet: "snippet" }] })] },
	{ id: "duckduckgo-act-ok", module: "duckduckgo.ts", fn: "searchWithDuckDuckGo", query: "ddg query", options: {},
		responses: [r('<html><body><div class="result"><a class="result__a" href="https://example.com/ddg">DDG result</a><a class="result__snippet">DDG snippet</a></div></body></html>')] },
	{ id: "tavily-act-ok", module: "tavily.ts", fn: "searchWithTavily", env: { TAVILY_API_KEY: "tavily-act-key" }, query: "tavily query", options: {},
		responses: [json({ answer: "Answer", results: [{ title: "Tavily result", url: "https://example.com/tavily", content: "content" }] })] },
	{ id: "exa-act-ok", module: "exa.ts", fn: "searchWithExa", env: { EXA_API_KEY: "exa-act-key" }, query: "exa query", options: {},
		responses: [json({ results: exaResults })] },
	{ id: "exa-mcp-act-ok", module: "exa.ts", fn: "searchWithExa", query: "exa mcp query", options: {},
		responses: [json({ jsonrpc: "2.0", id: 1, result: { content: [{ type: "text", text: "Title: Exa MCP\nURL: https://example.com/mcp\nText: body\n---" }] } })] },
	{ id: "kagi-act-ok", module: "kagi.ts", fn: "searchWithKagi", env: { KAGI_API_KEY: "kagi-act-key" }, query: "kagi query", options: {},
		responses: [json({ data: { search: [{ title: "Kagi result", url: "https://example.com/kagi", snippet: "Kagi snippet" }] } })] },
	{ id: "ollama-act-ok", module: "ollama.ts", fn: "searchWithOllama", env: { OLLAMA_API_KEY: "ollama-act-key" }, query: "ollama query", options: {},
		responses: [json({ results: [{ title: "Ollama result", url: "https://example.com/ollama", content: "content" }] })] },
);
const endings = [
	["http500", { responses: [r("boom", 500)] }],
	["transport", { responses: [{ throw: "plain", message: "connect failed" }] }],
	["abort", { preAborted: true, responses: [{ throw: "abort" }] }],
	["invalid-json", { responses: [r("<<not json")] }],
	// The headers arrive and the body read fails: the provider's own deadline cuts the transfer, the caller
	// cancels during it, the connection drops. Perplexity's fetch has no deadline of its own.
	["body-timeout", { patchTimeout: true, responses: [{ bodyThrow: "timeout" }] }],
	["body-abort", { callerAbortsBody: true, responses: [{ bodyThrow: "abort" }] }],
	["body-terminated", { responses: [{ bodyThrow: "terminated" }] }],
	// The same on an error status, where the original reads the body with response.text() and has no catch block of its own.
	["http500-body-timeout", { patchTimeout: true, responses: [{ status: 500, bodyThrow: "timeout" }] }],
];
// brave.ts and exa.ts let the JSON parser's own message escape ("Unexpected token ..." of V8), which Go cannot produce.
const rawJsonError = new Set(["searchWithBrave", "searchWithExa"]);
const seenFns = new Set();
for (const base of [...cases]) {
	const key = base.id.startsWith("exa-mcp") ? "searchWithExaMcp" : base.fn;
	if (base.baizhi || seenFns.has(key)) continue;
	seenFns.add(key);
	for (const [name, change] of endings) {
		if (name === "invalid-json" && rawJsonError.has(base.fn)) continue;
		if (name.endsWith("body-timeout") && base.fn === "searchWithPerplexity") continue;
		const { preAborted, patchTimeout, callerAbortsBody, ...rest } = base;
		cases.push({ ...rest, ...change, id: `${base.id}-${name}` });
	}
}

// ── child: one case ──
function baizhiResponse(mode, req, key) {
	const rpc = req.body ? JSON.parse(req.body) : null;
	const auth = req.headers.authorization === `Bearer ${key}`;
	const jsonHeaders = { "content-type": "application/json" };
	const payload = { status: "success", results: [{ unknown_service_field: "opaque search result; https://example.com/reference" }] };
	if (mode === "network") throw new TypeError("fetch failed");
	if (mode === "http401" || !auth && mode !== "json") return new Response(`bad key ${key}`, { status: 401 });
	if (req.method === "GET") return new Response(null, { status: 405 });
	if (req.method === "DELETE") return new Response(null, { status: 200 });
	if (mode === "redirect") return new Response(null, { status: 307, headers: { location: "https://untrusted.example/mcp" } });
	if (mode === "http503") return new Response("busy", { status: 503 });
	if (rpc.method === "initialize") {
		if (mode === "invalid-json") return new Response("not json", { status: 200, headers: jsonHeaders });
		return new Response(JSON.stringify({ jsonrpc: "2.0", id: rpc.id, result: { protocolVersion: "2025-11-25", capabilities: { tools: {} }, serverInfo: { name: "synthetic-only", version: "1.0.0" } } }), { status: 200, headers: { ...jsonHeaders, "mcp-session-id": "synthetic-session" } });
	}
	if (rpc.id === undefined) return new Response(null, { status: 202 });
	const result = mode === "empty" ? { content: [] }
		: mode === "tool-error" ? { content: [{ type: "text", text: `secret ${key}` }], isError: true }
		: mode === "text" ? { content: [{ type: "text", text: JSON.stringify(payload) }] }
		: mode === "redact" ? { content: [{ type: "text", text: key }], structuredContent: { echoed: key } }
		: { content: [], structuredContent: payload };
	const message = { jsonrpc: "2.0", id: rpc.id, result };
	if (mode === "sse") return new Response(`event: message\ndata: ${JSON.stringify(message)}\n\n`, { status: 200, headers: { "content-type": "text/event-stream" } });
	return new Response(JSON.stringify(message), { status: 200, headers: jsonHeaders });
}

async function runChild(index) {
	const c = cases[index];
	const requests = [];
	const home = process.env.PI_CODING_AGENT_DIR;
	// The parser's own text after "invalid JSON:" differs between V8 and Go and is dropped; the texts of a body that
	// could not be read at all (the deadline, the caller's abort, a dropped connection) are kept.
	const bodyReadTexts = new Set(["The operation was aborted due to timeout", "This operation was aborted", "terminated"]);
	const normalize = (s) => String(s).split(join(home, "web-search.json")).join("<CONFIG>").replace(/(invalid JSON|not valid JSON): ([\s\S]*)$/, (m, head, tail) => (bodyReadTexts.has(tail) ? m : head)).replace(/(Failed to parse <CONFIG>): [\s\S]*$/, "$1");
	if (c.now) { const fixed = Date.parse(c.now); Date.now = () => fixed; }
	if (c.patchTimeout) AbortSignal.timeout = () => AbortSignal.abort(new DOMException("The operation was aborted due to timeout", "TimeoutError"));
	const callerController = c.callerAbortsBody ? new AbortController() : undefined;
	const caller = c.preAborted ? AbortSignal.abort() : callerController?.signal;
	let n = 0;
	globalThis.fetch = async (url, init = {}) => {
		const headers = Object.fromEntries([...new Headers(init.headers ?? {})].sort(([a], [b]) => (a < b ? -1 : 1)));
		let body = init.body ?? null;
		if (typeof body === "string") { try { body = JSON.parse(body); } catch { /* keep text */ } }
		const req = { method: init.method ?? "GET", url: String(url), headers, body, redirect: init.redirect ?? null };
		requests.push(req);
		if (c.baizhi) {
			const res = baizhiResponse(c.baizhi, { ...req, body: typeof init.body === "string" ? init.body : null }, c.env?.BAIZHI_API_KEY === undefined ? "synthetic-baizhi-credential" : process.env.SYNTHETIC_BAIZHI_NAMED ?? process.env.BAIZHI_API_KEY);
			if (init.redirect === "error" && res.status >= 300 && res.status < 400) throw new TypeError("fetch failed");
			return res;
		}
		const spec = c.responses[Math.min(n++, c.responses.length - 1)];
		if (spec.throw === "timeout") throw init.signal?.reason ?? new DOMException("The operation was aborted due to timeout", "TimeoutError");
		if (spec.throw === "abort") throw init.signal?.reason ?? new DOMException("This operation was aborted", "AbortError");
		if (spec.throw === "network") throw new TypeError("fetch failed");
		if (spec.throw === "plain") throw new Error(spec.message);
		if (spec.bodyThrow) {
			// What a body stream rejects with once the request's signal fired (or the connection dropped) after the headers.
			const fail = () => {
				if (spec.bodyThrow === "terminated") throw new TypeError("terminated");
				if (spec.bodyThrow === "abort") { callerController.abort(); throw (init.signal ?? callerController.signal).reason; }
				if (!init.signal) { process.stderr.write(`recorder: ${c.id} sends its request without a deadline signal\n`); process.exit(3); }
				throw init.signal.reason;
			};
			const status = spec.status ?? 200;
			return { ok: status >= 200 && status < 300, status, async json() { fail(); }, async text() { fail(); } };
		}
		const text = spec.json !== undefined ? JSON.stringify(spec.json) : spec.body;
		const headersOut = spec.json !== undefined ? { "content-type": "application/json", ...spec.headers } : spec.headers ?? {};
		return new Response(text, { status: spec.status, headers: headersOut });
	};
	const mod = await import(pathToFileURL(join(oracle, c.module)).href);
	let outcome;
	try {
		const options = { ...c.options, ...(caller ? { signal: caller } : {}) };
		const result = await mod[c.fn](c.query, options);
		outcome = { result: JSON.parse(JSON.stringify(result)) };
	} catch (err) {
		outcome = { error: normalize(err instanceof Error ? err.message : String(err)) };
	}
	for (const req of requests) {
		if (typeof req.body === "string") req.body = normalize(req.body);
	}
	// The activity entries the original's activity monitor holds after the call (activity.ts), without ids and times.
	const { activityMonitor } = await import(pathToFileURL(join(oracle, "activity.ts")).href);
	const activity = activityMonitor.getEntries().map(({ type, query, url, status, error }) => ({ type, query, url, status, error: error === undefined ? undefined : normalize(error) }));
	process.stdout.write(JSON.stringify({ id: c.id, requests, outcome, activity }));
}

if (process.argv[2] === "--case") {
	await runChild(Number(process.argv[3]));
} else if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const out = [];
	for (let i = 0; i < cases.length; i++) {
		const c = cases[i];
		const home = mkdtempSync(join(tmpdir(), "pi-provider-record-"));
		try {
			if (c.configRaw !== undefined) writeFileSync(join(home, "web-search.json"), c.configRaw);
			else if (c.config !== undefined) writeFileSync(join(home, "web-search.json"), JSON.stringify(c.config));
			const env = { PATH: process.env.PATH, HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: home, ...(c.env ?? {}) };
			const child = spawnSync(process.execPath, [fileURLToPath(import.meta.url), "--case", String(i)], { env, encoding: "utf8", cwd: oracle });
			if (child.status !== 0) throw new Error(`case ${c.id} failed: ${child.stderr}`);
			const rec = JSON.parse(child.stdout);
			out.push({ id: c.id, provider: c.module.replace(/\.ts$/, ""), fn: c.fn, config: c.config ?? null, configRaw: c.configRaw ?? null, env: c.env ?? {}, query: c.query, options: c.options, responses: c.responses ?? null, baizhi: c.baizhi ?? null, preAborted: !!c.preAborted, callerAbortsBody: !!c.callerAbortsBody, patchTimeout: !!c.patchTimeout, now: c.now ?? null, unordered: !!c.unordered, ...rec });
		} finally {
			rmSync(home, { recursive: true, force: true });
		}
	}
	writeFileSync(join(here, "..", "extensions", "websearch", "testdata", "provider-requests.json"), JSON.stringify(out, null, 1) + "\n");
	process.stdout.write(`wrote ${out.length} cases\n`);
}
