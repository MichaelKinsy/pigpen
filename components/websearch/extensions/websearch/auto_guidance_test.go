package websearch

import (
	"errors"
	"testing"
)

// The "No search provider available" error of auto (gemini-search.ts:820-828) lists every provider
// the port has, including item 5 (Gemini Web in a Chromium browser), which the browser cookie slice
// ported.
func TestAutoNoProviderGuidanceListsEveryProvider(t *testing.T) {
	geminiAuto(t, `{"webSearch":{"allowedProviders":["duckduckgo"]}}`)
	useNet(t, func(netCall) netReply { return netReply{Err: errors.New("no request is expected")} })
	_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
	want := "No search provider available. Either:\n" +
		"  1. Use /login to sign in with a Codex subscription for OpenAI web search\n" +
		"  2. Set openaiApiKey, braveApiKey, parallelApiKey, tinyfishApiKey, search1apiApiKey, searchinfinityApiKey, queritApiKey, tavilyApiKey, firecrawlBaseUrl, jinaApiKey, serpdiveApiKey, kagiApiKey, ollamaApiKey, searxngBaseUrl, perplexityApiKey, exaApiKey, geminiApiKey, bochaApiKey, or cloudflareApiKey in " + ConfigPath() + "\n" +
		"  3. Set OPENAI_API_KEY, BRAVE_API_KEY, PARALLEL_API_KEY, TINYFISH_API_KEY, SEARCH1API_KEY, SEARCHINFINITY_API_KEY, QUERIT_API_KEY, TAVILY_API_KEY, FIRECRAWL_BASE_URL, JINA_API_KEY, SERPDIVE_API_KEY, KAGI_API_KEY, BOCHA_API_KEY, OLLAMA_API_KEY, SEARXNG_BASE_URL, EXA_API_KEY, PERPLEXITY_API_KEY, GEMINI_API_KEY, or CLOUDFLARE_API_KEY env vars\n" +
		"  4. Set GOOGLE_GEMINI_BASE_URL with CLOUDFLARE_API_KEY for Cloudflare AI Gateway routing\n" +
		"  5. Sign into gemini.google.com in a supported Chromium-based browser\n" +
		"  6. Explicitly select provider: \"anysearch\" for anonymous AnySearch, \"xcrawl\" for XCrawl, \"xai\" for Grok, \"mistral\" for Mistral Conversations web search, \"brightdata\" with brightdataSerpZone for paid Bright Data SERP, \"serpbase\", \"serpapi\", \"serper\", or \"serply\" for Google SERP, \"you\" for You.com, or \"valyu\" for research search"
	if err == nil || err.Error() != want {
		t.Fatalf("%v", err)
	}
}
