package websearch

import (
	"context"
	"strings"
)

// The four public tool definitions: names, descriptions and parameter schemas of index.ts, in
// the original's wording and key order (they are compared by hash).
//
// The fetch_content description still advertises YouTube, GitHub, PDF and local-video support:
// it is kept identical to the original so the definitions stay interchangeable; those inputs
// are named gaps in this port (docs/PORT.md) and fall back to plain HTTP or an explicit error.

func (r *Runtime) webSearchSpec() ToolSpec {
	policy := r.allPolicyDescription()
	allowed := r.cfg.AllowedProviders
	props := obj{
		{"query", sString("Single search query. For research tasks, prefer 'queries' with multiple varied angles instead.")},
		{"queries", sStringArray("Multiple queries searched concurrently (up to three at a time), each returning source-linked search results or a provider answer. Prefer this for research \u2014 vary phrasing, scope, and angle across 2-4 queries to maximize coverage. Good: ['React vs Vue performance benchmarks 2026', 'React vs Vue developer experience comparison', 'React ecosystem size vs Vue ecosystem']. Bad: ['React vs Vue', 'React vs Vue comparison', 'React vs Vue review'] (too similar, redundant results).")},
		{"numResults", sInteger(intp(1), intp(20), "Results per query (default: 5, max: 20)")},
		{"includeContent", sBool("Fetch full page content (async)")},
		{"recencyFilter", sEnum([]string{"day", "week", "month", "year"}, "Filter by recency")},
		{"domainFilter", sStringArray("Limit to domains (prefix with - to exclude)")},
		{"provider", searchProviderSchema("Search provider or non-empty list of allowed providers to search simultaneously; "+policy+"; omit this field to use the configured provider, or use auto when none is configured", allowed)},
		{"workflow", sEnum([]string{"none", "summary-review", "auto-summary"}, "Search workflow mode: none = no curator (default), summary-review = open curator with auto summary draft, auto-summary = generate summary without opening curator")},
		{"proxy", sString("http(s) or socks proxy URL (e.g. http://host:port or socks5h://host:port) used for every outbound request in this call (search APIs and content fetches). Node fetch ignores HTTP(S)_PROXY env vars, so set this (or `proxy` in web-search.json) when direct access is blocked; empty string forces direct access.")},
	}
	return ToolSpec{
		Name:          r.cfg.Names.WebSearch,
		Label:         "Web Search",
		Description:   "Search the web with " + labels(allowed) + ". Provider arrays run simultaneously; " + policy + ". The default workflow is none: it returns bounded source-linked search results or provider answers without a curator or generated summary, identifies the providers used, and stores full results for retrieval by responseId. For comprehensive research, prefer queries (plural) with 2-4 varied angles over a single query. When includeContent is true, full page content is fetched in the background. Set workflow to \"summary-review\" to open the curator with an auto-generated summary draft or \"auto-summary\" to generate a summary without the browser curator. The configured provider is used when provider is omitted or set to auto; omit provider unless explicitly overriding it.",
		PromptSnippet: "Use for web research questions. Prefer {queries:[...]} with 2-4 varied angles over a single query for broader coverage. Omit provider unless explicitly overriding the configured default.",
		parameters:    sObject(nil, props),
		Execute:       r.webSearch,
	}
}

func (r *Runtime) sourceCheckSpec() ToolSpec {
	props := obj{
		{"claim", sString("The assertion to gather web sources for.")},
		{"queries", sStringArray("Search queries (default: the claim).")},
		{"numResults", sInteger(intp(1), intp(20), "Results per query (default: 5, max: 20).")},
		{"fetchContent", sBool("Fetch up to 5 result pages for exact passage extraction.")},
		{"recencyFilter", sEnum([]string{"day", "week", "month", "year"}, "Filter by recency.")},
		{"domainFilter", sStringArray("Limit to domains; prefix with - to exclude.")},
		{"provider", searchProviderSchema("Search provider or non-empty list of allowed providers to search simultaneously; "+r.allPolicyDescription(), r.cfg.AllowedProviders)},
		{"proxy", sString("http(s) or socks proxy URL (e.g. http://host:port or socks5h://host:port) used for every outbound request in this call (search APIs and result-page fetches). Empty string forces direct access.")},
	}
	return ToolSpec{
		Name:          r.cfg.Names.SourceCheck,
		Label:         "Source Check",
		Description:   "Gather web sources for a claim and return a bounded machine-readable research artifact with exact passage citations for manual review.",
		PromptSnippet: "Gather structured source evidence and passage-level citations for manual semantic review of a claim.",
		parameters:    sObject([]string{"claim"}, props),
		Execute:       r.sourceCheck,
	}
}

func (r *Runtime) fetchModeDescription() string {
	parts := make([]string, len(r.cfg.AllowedModes))
	for i, m := range r.cfg.AllowedModes {
		def := ""
		if m == r.cfg.DefaultMode {
			def = " (default)"
		}
		parts[i] = m + def + ": " + fetchModeDescriptions[m]
	}
	return strings.Join(parts, "; ")
}

func (r *Runtime) fetchContentSpec() ToolSpec {
	answer := sliceHas(r.cfg.AllowedModes, "answer")
	promptDesc := "Question or instruction for video analysis."
	if answer {
		promptDesc = "Question or instruction for video analysis, or the page-local question required by answer mode."
	}
	props := obj{
		{"url", sString("Single URL to fetch")},
		{"urls", sStringArray("Multiple URLs (parallel)")},
		{"forceClone", sBool("Force cloning large GitHub repositories that exceed the size threshold")},
		{"prompt", sString(promptDesc)},
		{"mode", sEnum(r.cfg.AllowedModes, "Fetch mode. "+r.fetchModeDescription()+".")},
	}
	if answer {
		props = append(props, kv{"answerModel", sString("Optional provider/model-id override for answer mode. Defaults to fetch.answerProvider + fetch.answerModel when configured, otherwise the current Pi model.")})
	}
	props = append(props,
		kv{"timestamp", sString("Extract video frame(s) at a timestamp or time range. Single: '1:23:45', '23:45', or '85' (seconds). Range: '23:41-25:00' extracts evenly-spaced frames across that span (default 6). Use frames with ranges to control density; single+frames uses a fixed 5s interval. YouTube requires yt-dlp + ffmpeg; local videos require ffmpeg. Use a range when you know the approximate area but not the exact moment \u2014 you'll get a contact sheet to visually identify the right frame.")},
		kv{"frames", sInteger(intp(1), intp(12), "Number of frames to extract. Use with timestamp range for custom density, with single timestamp to get N frames at 5s intervals, or alone to sample across the entire video. Requires yt-dlp + ffmpeg for YouTube, ffmpeg for local video.")},
		kv{"model", sString("Override the Gemini model for video/YouTube analysis (e.g. 'gemini-3.6-flash'). Defaults to config or gemini-3.6-flash.")},
		kv{"auth", withDescription(obj{{"anyOf", []any{sString(""), sBool("")}}}, "Opt into an authFetch profile for local browser-cookie fetching. Use a profile name, or true only when exactly one profile exists.")},
		kv{"proxy", sString("http(s) or socks proxy URL (e.g. http://host:port or socks5h://host:port) used for this fetch. Needed when the target is unreachable directly; localhost and NO_PROXY hosts always bypass the proxy. Empty string forces direct access.")},
	)
	storageNote := "Full original content is stored internally, but the retrieval tool is not registered."
	if r.cfg.GetSearchContent {
		storageNote = "Full original content is stored for retrieval with " + r.cfg.Names.GetSearchContent + "."
	}
	return ToolSpec{
		Name:          r.cfg.Names.FetchContent,
		Label:         "Fetch Content",
		Description:   "Fetch URL(s). Available modes: " + r.fetchModeDescription() + ". Direct image URLs return resized image content when supported by the selected mode. Supports YouTube transcripts, GitHub repositories, PDFs, and local videos when supported by the selected mode. " + storageNote,
		PromptSnippet: "Use to fetch URL content, direct images, GitHub repos, and videos.",
		parameters:    sObject(nil, props),
		Execute:       r.fetchContent,
	}
}

func (r *Runtime) getSearchContentSpec() ToolSpec {
	maxChars := MaxInlineContentChars(r.cfg.Root)
	sources := r.storedContentSources()
	queryDesc := "Get content for a stored search query"
	if r.cfg.WebSearch {
		queryDesc = "Get content for this query (" + r.cfg.Names.WebSearch + ")"
	}
	findString := func() obj { return append(sString(""), kv{"minLength", 1}, kv{"maxLength", 500}) }
	props := obj{
		{"responseId", sString("The responseId from " + sources)},
		{"query", sString(queryDesc)},
		{"queryIndex", sInteger(intp(0), nil, "Get content for query at index")},
		{"url", sString("Get content for this URL")},
		{"urlIndex", sInteger(intp(0), nil, "Get content for URL at index")},
		{"offset", sInteger(intp(0), nil, "Character offset in stored search or fetched URL content (default 0). Ignored when findText is supplied.")},
		{"limit", sInteger(intp(1), intp(maxChars), "Requested maximum stored-content characters (default and max use maxInlineContentChars). Search-page continuation guidance shares the global output cap and may reduce returnedChars. Ignored when findText is supplied.")},
		{"findText", withDescription(obj{{"anyOf", []any{
			findString(),
			obj{{"type", "array"}, {"items", findString()}, {"minItems", 1}, {"maxItems", 10}},
		}}}, "Text or texts to find in the selected stored content. When supplied, offset and limit are ignored.")},
		{"findMode", sEnum([]string{"exact", "case-insensitive", "fuzzy"}, "Matching mode for findText (default: case-insensitive). Requires findText.")},
	}
	return ToolSpec{
		Name:          r.cfg.Names.GetSearchContent,
		Label:         "Get Search Content",
		Description:   "Retrieve bounded pages of full stored search results or fetched content, or find matching passages, from a previous " + sources + " call.",
		PromptSnippet: "Use after " + sources + " to retrieve stored content via responseId. Use findText to locate passages without paging through the full content.",
		parameters:    sObject([]string{"responseId"}, props),
		Execute: func(ctx context.Context, params map[string]any, _ func(ToolOutput)) (ToolOutput, error) {
			return r.getSearchContent(params, maxChars)
		},
	}
}

// ---- web_enable ---------------------------------------------------------------------------

type activationTool struct{ name, capability string }

var capabilityLabels = map[string]string{
	"search": "web search", "source-check": "source checking", "fetch": "content fetching", "stored-content": "stored-result retrieval",
}

func (r *Runtime) activationTools() []activationTool {
	var out []activationTool
	if r.cfg.WebSearch {
		out = append(out, activationTool{r.cfg.Names.WebSearch, "search"})
	}
	if r.cfg.SourceCheck {
		out = append(out, activationTool{r.cfg.Names.SourceCheck, "source-check"})
	}
	if r.cfg.FetchContent {
		out = append(out, activationTool{r.cfg.Names.FetchContent, "fetch"})
	}
	if r.cfg.GetSearchContent {
		out = append(out, activationTool{r.cfg.Names.GetSearchContent, "stored-content"})
	}
	return out
}
