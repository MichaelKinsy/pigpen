package websearch

import (
	"context"
	"errors"
)

// The hosted fetch providers of extract.ts:830-1010 and the failure guidance of extract.ts:1030-1065.

// hostedFetchOrder lists the providers whose failure the guidance reports, in the original's
// order, with the label of "<label> fallback failed: <message>".
var hostedFetchOrder = []struct{ name, label string }{
	{"firecrawl", "Firecrawl"}, {"crawl4ai", "Crawl4AI"}, {"tinyfish", "TinyFish"}, {"search1api", "Search1API"},
	{"querit", "Querit"}, {"kagi", "Kagi"}, {"ollama", "Ollama"}, {"parallel", "Parallel"},
	{"parallel-mcp", "Parallel MCP"}, {"brightdata", "Bright Data"},
}

func isHostedFetchProvider(p string) bool {
	for _, h := range hostedFetchOrder {
		if h.name == p {
			return true
		}
	}
	return false
}

// runHostedFetch is one provider's `try` block: (nil, nil) when it is unavailable or found
// nothing, the content on success, an error when the provider failed.
func runHostedFetch(ctx context.Context, p, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	switch p {
	case "firecrawl", "crawl4ai", "kagi", "ollama", "brightdata":
		var ok bool
		var err error
		switch p {
		case "firecrawl":
			ok, err = firecrawlConfigured()
		case "crawl4ai":
			ok, err = crawl4aiConfigured()
		case "kagi":
			ok = IsKagiExtractAvailable()
		case "ollama":
			ok = IsOllamaFetchAvailable()
		case "brightdata":
			ok, err = brightdataUnlockerAvailableErr()
		}
		if err != nil || !ok {
			return nil, err
		}
		ssrf, err := LoadSsrfConfig()
		if err != nil {
			return nil, err
		}
		switch p {
		case "firecrawl":
			return ExtractWithFirecrawl(ctx, rawURL, o, &ssrf)
		case "crawl4ai":
			return ExtractWithCrawl4ai(ctx, rawURL, o, &ssrf)
		case "kagi":
			return ExtractWithKagi(ctx, rawURL, o, ssrf)
		case "ollama":
			return ExtractWithOllama(ctx, rawURL, o, ssrf)
		}
		return ExtractWithBrightDataUnlocker(ctx, rawURL, o, ssrf)
	case "tinyfish":
		if IsTinyFishAvailable() {
			return ExtractWithTinyFish(ctx, rawURL, o)
		}
	case "search1api":
		if IsSearch1APIAvailable() {
			return ExtractWithSearch1API(ctx, rawURL, o)
		}
	case "querit":
		if IsQueritAvailable() {
			return ExtractWithQuerit(ctx, rawURL, o)
		}
	case "parallel":
		if IsParallelAvailable() {
			return ExtractWithParallel(ctx, rawURL, o)
		}
	case "parallel-mcp":
		return ExtractWithParallelMCP(ctx, rawURL, o)
	}
	return nil, nil
}

// hostedAborted is the catch block's abort test: the message names an abort, except for Crawl4AI,
// which only treats a cancelled call or its own deadline as one (crawl4ai.ts isAbortException).
func hostedAborted(ctx context.Context, p string, err error) bool {
	if p == "crawl4ai" {
		return ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)
	}
	return isAbortError(err)
}

// hostedGuidanceLines are the "<label> fallback failed" lines, in order.
func hostedGuidanceLines(failures map[string]string) []string {
	var lines []string
	for _, h := range hostedFetchOrder {
		if msg := failures[h.name]; msg != "" {
			lines = append(lines, h.label+" fallback failed: "+msg)
		}
	}
	return lines
}

// hostedFallbackOptions are the "Set ..." suggestions of the Fallback options list.
func hostedFallbackOptions() []string {
	path := ConfigPath()
	return []string{
		"  • Set firecrawlBaseUrl in " + path + " to a self-hosted Firecrawl instance",
		"  • Set crawl4aiBaseUrl in " + path + " to a self-hosted Crawl4AI instance",
		"  • Set tinyfishApiKey in " + path + " or TINYFISH_API_KEY",
		"  • Set search1apiApiKey in " + path + " or SEARCH1API_KEY",
		"  • Set queritApiKey in " + path + " or QUERIT_API_KEY",
		"  • Set kagiApiKey in " + path + " or KAGI_API_KEY",
		"  • Set ollamaApiKey in " + path + " or OLLAMA_API_KEY",
		"  • Set parallelApiKey in " + path + " or PARALLEL_API_KEY",
		"  • Set brightdataApiKey and brightdataUnlockerZone in " + path + " or BRIGHTDATA_API_KEY and BRIGHTDATA_UNLOCKER_ZONE",
	}
}
