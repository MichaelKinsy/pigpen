// Records what the ORIGINAL curator page (port/oracle/curator-page.ts, generateCuratorPage) renders for
// the inputs the Go twins use, and derives the page asset the port embeds.
//
//   node port/record-curator-page.mjs
//
// Writes (relative to the component):
//   extensions/websearch/curator_page.html          the original's page with three markers where
//                                                   it interpolates its inputs (timeout, provider
//                                                   buttons, inline data); the port fills them in
//   extensions/websearch/testdata/curator-page.json the inputs of every case with the length and
//                                                   SHA-256 of the original's output, and the
//                                                   complete output of the first (minimal) case in
//                                                   testdata/curator-page-minimal.html
//
// The Go twin (curator_page_test.go) renders every case with the port and requires the identical
// bytes (length and SHA-256; the minimal case byte for byte).
//
// How the asset is derived: the original builds the page from a template literal that interpolates
// the timeout, the provider buttons and one JSON blob. This script renders the original once with
// inputs that make those three spots recognisable (the timeout is a marker string, no provider is
// available so the button container is empty, JSON.stringify of the inline data returns a marker),
// checks that each marker occurs exactly once and writes the result. Nothing else of the page is
// edited: CSS, markup and script are the original's bytes.
//
// Run it with the oracle's node_modules and Node 24 (type stripping), e.g. in the tern-ci container:
//   cd components/websearch/port && node record-curator-page.mjs
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const component = dirname(here);
const ext = join(component, "extensions", "websearch");
const { generateCuratorPage } = await import(join(here, "oracle", "curator-page.ts"));

const TIMEOUT_MARK = "@@CURATOR_TIMEOUT@@";
const BUTTONS_MARK = "@@CURATOR_PROVIDER_BUTTONS@@";
const INLINE_MARK = "@@CURATOR_INLINE_DATA@@";
const SENTINEL_TOKEN = "sentinel-session-token";

function deriveAsset() {
	const realStringify = JSON.stringify;
	JSON.stringify = (value, ...rest) =>
		value && typeof value === "object" && value.sessionToken === SENTINEL_TOKEN ? INLINE_MARK : realStringify(value, ...rest);
	let page;
	try {
		page = generateCuratorPage([], SENTINEL_TOKEN, TIMEOUT_MARK, { all: false }, "exa", "auto", [], null);
	} finally {
		JSON.stringify = realStringify;
	}
	const emptyButtons = 'id="provider-buttons"></div>';
	const count = (text, needle) => text.split(needle).length - 1;
	for (const [needle, label] of [[`value="${TIMEOUT_MARK}"`, "timeout"], [emptyButtons, "provider buttons"], [INLINE_MARK, "inline data"]]) {
		if (count(page, needle) !== 1) throw new Error(`the original page has ${count(page, needle)} occurrences of the ${label} spot, expected 1`);
	}
	if (page.includes(BUTTONS_MARK)) throw new Error("marker collides with the page");
	return page.replace(emptyButtons, `id="provider-buttons">${BUTTONS_MARK}</div>`);
}

const available = (pairs) => Object.fromEntries(pairs);
const allProviders = [
	"all", "openai", "exa", "brave", "parallel", "parallel-mcp", "tinyfish", "search1api", "searchinfinity", "querit", "tavily",
	"firecrawl", "jina", "serpdive", "kagi", "bocha", "ollama", "searxng", "duckduckgo", "perplexity", "gemini", "kimi", "anysearch",
	"xcrawl", "xai", "mistral", "brightdata", "serpbase", "serpapi", "serper", "serply", "you", "baizhi", "valyu",
];
// Key order of getProviderAvailability() in index.ts: all, then the providers in its literal's order.
const curatorOrder = [
	"all", "openai", "brave", "parallel", "parallel-mcp", "tinyfish", "search1api", "searchinfinity", "querit", "tavily", "you",
	"firecrawl", "jina", "serpdive", "kagi", "bocha", "ollama", "searxng", "duckduckgo", "perplexity", "exa", "gemini", "kimi",
	"anysearch", "xcrawl", "xai", "mistral", "brightdata", "serpbase", "serpapi", "serper", "serply", "baizhi", "valyu",
];

export const cases = [
	{ id: "minimal", queries: [], sessionToken: "t", timeout: 20, available: [["all", false]], defaultProvider: "exa", searchProvider: "auto", summaryModels: [], defaultSummaryModel: null },
	{ id: "one-query-exa-loading", queries: ["test query"], sessionToken: "test-token", timeout: 1, available: [["all", true], ["openai", false], ["brave", false], ["exa", true]], defaultProvider: "exa", searchProvider: "exa", summaryModels: [], defaultSummaryModel: null },
	{ id: "no-initial-queries-default-idle", queries: [], sessionToken: "test-token", timeout: 20, available: [["all", true], ["exa", true], ["brave", true]], defaultProvider: "brave", searchProvider: "auto", summaryModels: [], defaultSummaryModel: null },
	{ id: "all-providers-available", queries: ["a", "b"], sessionToken: "00000000-0000-4000-8000-000000000000", timeout: 60, available: curatorOrder.map((k) => [k, true]), defaultProvider: "searxng", searchProvider: "all", summaryModels: [{ value: "anthropic/claude-haiku-4-5", label: "anthropic/claude-haiku-4-5" }], defaultSummaryModel: "anthropic/claude-haiku-4-5" },
	{ id: "hostile-strings", queries: ["</script><script>alert(1)</script>", "a & b < c > d", "line\u2028sep\u2029para", "quote \" and \\ backslash", "emoji \u{1F50D} and \u00e9", ""], sessionToken: "tok</script>", timeout: 600, available: [["all", false], ["tavily", true]], defaultProvider: "tavily", searchProvider: "tavily", summaryModels: [{ value: "a/b:high", label: "<b>x</b> & y" }], defaultSummaryModel: "a/b:high" },
	{ id: "sparse-baizhi", queries: ["q"], sessionToken: "token", timeout: 20, available: [["all", false], ["baizhi", true]], defaultProvider: "baizhi", searchProvider: "baizhi", summaryModels: [], defaultSummaryModel: null },
	{ id: "sparse-serpapi", queries: ["query"], sessionToken: "token", timeout: 20, available: [["all", false], ["serpapi", true]], defaultProvider: "serpapi", searchProvider: "serpapi", summaryModels: [], defaultSummaryModel: null },
	{ id: "sparse-serply", queries: ["query"], sessionToken: "token", timeout: 20, available: [["all", false], ["serply", true]], defaultProvider: "serply", searchProvider: "serply", summaryModels: [], defaultSummaryModel: null },
	{ id: "sparse-you", queries: ["query"], sessionToken: "token", timeout: 20, available: [["all", false], ["you", true]], defaultProvider: "you", searchProvider: "you", summaryModels: [], defaultSummaryModel: null },
	{ id: "serper-and-valyu", queries: ["query"], sessionToken: "session-token", timeout: 20, available: [["all", false], ["openai", false], ["brave", false], ["serper", true], ["valyu", true]], defaultProvider: "serper", searchProvider: "valyu", summaryModels: [], defaultSummaryModel: null },
	{ id: "parallel-mcp", queries: ["query"], sessionToken: "session-token", timeout: 20, available: [["all", false], ["parallel-mcp", true]], defaultProvider: "parallel-mcp", searchProvider: "parallel-mcp", summaryModels: [], defaultSummaryModel: null },
	{ id: "brightdata", queries: ["brightdata query"], sessionToken: "session-token", timeout: 20, available: [["openai", false], ["brave", false], ["parallel", false], ["tinyfish", false], ["tavily", false], ["serpdive", false], ["brightdata", true]], defaultProvider: "brightdata", searchProvider: "brightdata", summaryModels: [], defaultSummaryModel: null },
	{ id: "jina", queries: ["jina query"], sessionToken: "session-token", timeout: 20, available: [["all", false], ["jina", true]], defaultProvider: "jina", searchProvider: "jina", summaryModels: [], defaultSummaryModel: null },
	{ id: "mistral-and-xai", queries: ["query"], sessionToken: "token", timeout: 20, available: [["all", false], ["mistral", true], ["xai", true]], defaultProvider: "mistral", searchProvider: "xai", summaryModels: [], defaultSummaryModel: null },
	{ id: "bocha", queries: ["bocha query"], sessionToken: "session-token", timeout: 20, available: [["all", false], ["bocha", true]], defaultProvider: "bocha", searchProvider: "bocha", summaryModels: [], defaultSummaryModel: null },
	{ id: "querit-search1api-searchinfinity-tinyfish", queries: ["q"], sessionToken: "session-token", timeout: 20, available: [["all", false], ["tinyfish", true], ["search1api", true], ["searchinfinity", true], ["querit", true]], defaultProvider: "querit", searchProvider: "querit", summaryModels: [], defaultSummaryModel: null },
	{ id: "unknown-default-provider", queries: ["q"], sessionToken: "t", timeout: 20, available: allProviders.map((k) => [k, true]), defaultProvider: "not-a-provider", searchProvider: "auto", summaryModels: [], defaultSummaryModel: null },
];

function render(c) {
	return generateCuratorPage(c.queries, c.sessionToken, c.timeout, available(c.available), c.defaultProvider, c.searchProvider, c.summaryModels, c.defaultSummaryModel);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const asset = deriveAsset();
	mkdirSync(join(ext, "testdata"), { recursive: true });
	writeFileSync(join(ext, "curator_page.html"), asset);
	const out = cases.map((c) => {
		const html = render(c);
		return { ...c, bytes: Buffer.byteLength(html), sha256: createHash("sha256").update(html).digest("hex") };
	});
	writeFileSync(join(ext, "testdata", "curator-page.json"), JSON.stringify({ markers: { timeout: TIMEOUT_MARK, providerButtons: BUTTONS_MARK, inlineData: INLINE_MARK }, cases: out }, null, 1) + "\n");
	writeFileSync(join(ext, "testdata", "curator-page-minimal.html"), render(cases[0]));
	console.log(`asset ${Buffer.byteLength(asset)} bytes, ${out.length} cases`);
}
