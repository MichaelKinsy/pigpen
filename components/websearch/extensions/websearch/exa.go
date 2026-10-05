package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Port of exa.ts: the keyed /search API and the keyless Exa MCP endpoint.
//
// Privacy note: with no key configured the query is sent to the hosted Exa MCP service
// (mcp.exa.ai), exactly as the original does. That endpoint is free-tier and rate limited, not
// paid; set "exaApiKey" for the authenticated API.

const (
	exaAPIBaseURL      = "https://api.exa.ai"
	exaMCPURL          = "https://mcp.exa.ai/mcp"
	exaMCPAdvancedTool = "web_search_advanced_exa"
	exaMCPBasicTool    = "web_search_exa"
	exaTimeout         = 60 * time.Second
)

// IsExaAvailable is always true: without a key the keyless MCP endpoint is used.
func IsExaAvailable() bool { return true }

// HasExaAPIKey reports whether an Exa credential source is configured (a command source is not run).
func HasExaAPIKey() bool { return hasProviderCredential("Exa", "exaApiKey", "EXA_API_KEY") }

type exaItem struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Text       any    `json:"text"`
	Highlights any    `json:"highlights"`
}

func fallbackSourceLabel(rawURL string, index int) string {
	if rawURL != "" {
		if u, err := ParseURL(rawURL, nil); err == nil {
			if h := JSHostname(u); h != "" {
				return h
			}
		}
	}
	return "Source " + strconv.Itoa(index+1)
}

func recencyToStartDate(filter string) string {
	days := map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}[filter]
	return time.UnixMilli(nowMs()).Add(-time.Duration(days) * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
}

func exaSearchArgs(query string, o SearchOptions) map[string]any {
	n := 5.0
	if o.NumResults != nil && !math.IsNaN(*o.NumResults) && !math.IsInf(*o.NumResults, 0) {
		n = *o.NumResults
	}
	args := map[string]any{"query": query, "type": "auto", "numResults": n}
	var include, exclude []string
	for _, d := range o.DomainFilter {
		switch {
		case !strings.HasPrefix(d, "-") && jsTrim(d) != "":
			include = append(include, jsTrim(d))
		case strings.HasPrefix(d, "-"):
			if e := jsTrim(d[1:]); e != "" {
				exclude = append(exclude, e)
			}
		}
	}
	if len(include) > 0 {
		args["includeDomains"] = include
	}
	if len(exclude) > 0 {
		args["excludeDomains"] = exclude
	}
	if o.RecencyFilter != "" {
		args["startPublishedDate"] = recencyToStartDate(o.RecencyFilter)
	}
	return args
}

func normalizeHighlights(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok && jsTrim(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildAnswerFromSearchResults(items []exaItem) string {
	var parts []string
	for i, item := range items {
		if item.URL == "" {
			continue
		}
		content := ""
		if h := normalizeHighlights(item.Highlights); len(h) > 0 {
			content = strings.Join(h, " ")
		} else if s, ok := item.Text.(string); ok {
			content = jsSlice(jsTrim(s), 0, 1000)
		}
		if content == "" {
			continue
		}
		title := item.Title
		if title == "" {
			title = fallbackSourceLabel(item.URL, i)
		}
		parts = append(parts, content+"\nSource: "+title+" ("+item.URL+")")
	}
	return strings.Join(parts, "\n\n")
}

func mapExaResults(items []exaItem) []SearchResult {
	out := []SearchResult{}
	for i, item := range items {
		if item.URL == "" {
			continue
		}
		title := item.Title
		if title == "" {
			title = fallbackSourceLabel(item.URL, i)
		}
		out = append(out, SearchResult{Title: title, URL: item.URL})
	}
	return out
}

func mapExaInline(items []exaItem) []ExtractedContent {
	var out []ExtractedContent
	for i, item := range items {
		if s, ok := item.Text.(string); ok && item.URL != "" && s != "" {
			title := item.Title
			if title == "" {
				title = fallbackSourceLabel(item.URL, i)
			}
			out = append(out, ExtractedContent{URL: item.URL, Title: title, Content: s})
		}
	}
	return out
}

func toSearchResponse(answer string, results []SearchResult, inline []ExtractedContent) *SearchResponse {
	r := &SearchResponse{Answer: answer, Results: results}
	if len(inline) > 0 {
		r.InlineContent = inline
	}
	return r
}

// callExaMCP posts a tools/call to the Exa MCP endpoint and returns the first text content.
func callExaMCP(ctx context.Context, tool string, args map[string]any) (string, error) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	tctx, cancel := withTimeout(ctx, exaTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, exaMCPURL+"?tools="+tool, RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}, "X-Exa-Source": {"pi-web-access"}}}, nil)
	if err != nil {
		return "", timeoutFix(ctx, tctx, err)
	}
	body, err := readBody(resp)
	if err != nil {
		return "", timeoutFix(ctx, tctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == 429 {
			return "", fmt.Errorf("Exa MCP rate limit reached (429). Add \"exaApiKey\" to %s for unthrottled Exa search: %s", ConfigPath(), jsSlice(body, 0, 200))
		}
		return "", fmt.Errorf("Exa MCP error %d: %s", resp.StatusCode, slice300(body))
	}
	type rpc struct {
		Result *struct {
			Content []struct{ Type, Text string } `json:"content"`
			IsError bool                          `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    *float64 `json:"code"`
			Message string   `json:"message"`
		} `json:"error"`
	}
	var parsed *rpc
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := jsTrim(line[5:])
		if payload == "" {
			continue
		}
		var c rpc
		if json.Unmarshal([]byte(payload), &c) == nil && (c.Result != nil || c.Error != nil) {
			parsed = &c
			break
		}
	}
	if parsed == nil {
		var c rpc
		if json.Unmarshal([]byte(body), &c) == nil && (c.Result != nil || c.Error != nil) {
			parsed = &c
		}
	}
	if parsed == nil {
		return "", fmt.Errorf("Exa MCP returned an empty response")
	}
	if parsed.Error != nil {
		code := ""
		if parsed.Error.Code != nil {
			code = " " + strconv.FormatFloat(*parsed.Error.Code, 'f', -1, 64)
		}
		msg := parsed.Error.Message
		if msg == "" {
			msg = "Unknown error"
		}
		return "", fmt.Errorf("Exa MCP error%s: %s", code, msg)
	}
	if parsed.Result.IsError {
		for _, c := range parsed.Result.Content {
			if c.Type == "text" {
				if m := jsTrim(c.Text); m != "" {
					return "", fmt.Errorf("%s", m)
				}
				break
			}
		}
		return "", fmt.Errorf("Exa MCP returned an error")
	}
	for _, c := range parsed.Result.Content {
		if c.Type == "text" && jsTrim(c.Text) != "" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("Exa MCP returned empty content")
}

type mcpParsed struct{ title, url, content string }

var (
	mcpTitleRE      = regexp.MustCompile(`(?m)^Title: (.+)`)
	mcpURLRE        = regexp.MustCompile(`(?m)^URL: (.+)`)
	mcpHighlightsRE = regexp.MustCompile(`\nHighlights:\s*\n`)
	mcpTrailerRE    = regexp.MustCompile(`\n---\s*$`)
)

// splitBeforeTitle is text.split(/(?=^Title: )/m).
func splitBeforeTitle(text string) []string {
	var blocks []string
	start := 0
	for i := 0; i < len(text); i++ {
		if strings.HasPrefix(text[i:], "Title: ") && (i == 0 || text[i-1] == '\n') && i > start {
			blocks = append(blocks, text[start:i])
			start = i
		}
	}
	return append(blocks, text[start:])
}

func parseMCPResults(text string) []mcpParsed {
	var out []mcpParsed
	for _, block := range splitBeforeTitle(text) {
		if jsTrim(block) == "" {
			continue
		}
		var title, u string
		if m := mcpTitleRE.FindStringSubmatch(block); m != nil {
			title = jsTrim(m[1])
		}
		if m := mcpURLRE.FindStringSubmatch(block); m != nil {
			u = jsTrim(m[1])
		}
		content := ""
		if i := strings.Index(block, "\nText: "); i >= 0 {
			content = jsTrim(block[i+7:])
		} else if loc := mcpHighlightsRE.FindStringIndex(block); loc != nil {
			content = jsTrim(block[loc[1]:])
		}
		content = jsTrim(mcpTrailerRE.ReplaceAllString(content, ""))
		if u != "" {
			out = append(out, mcpParsed{title, u, content})
		}
	}
	return out
}

func buildMCPQuery(query string, o SearchOptions) string {
	parts := []string{query}
	for _, d := range o.DomainFilter {
		if strings.HasPrefix(d, "-") {
			parts = append(parts, "-site:"+d[1:])
		} else {
			parts = append(parts, "site:"+d)
		}
	}
	now := time.UnixMilli(nowMs())
	switch o.RecencyFilter {
	case "day":
		parts = append(parts, "past 24 hours")
	case "week":
		parts = append(parts, "past week")
	case "month":
		parts = append(parts, now.Month().String()+" "+strconv.Itoa(now.Year()))
	case "year":
		parts = append(parts, strconv.Itoa(now.Year()))
	}
	return strings.Join(parts, " ")
}

func searchWithExaMCPTool(ctx context.Context, tool string, args map[string]any, o SearchOptions) (*SearchResponse, error) {
	text, err := callExaMCP(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	// web_search_advanced_exa returns the raw Exa search JSON; web_search_exa a formatted block.
	var asJSON struct {
		Results []exaItem `json:"results"`
	}
	if json.Unmarshal([]byte(text), &asJSON) == nil && len(asJSON.Results) > 0 {
		var inline []ExtractedContent
		if o.IncludeContent {
			inline = mapExaInline(asJSON.Results)
		}
		return toSearchResponse(buildAnswerFromSearchResults(asJSON.Results), mapExaResults(asJSON.Results), inline), nil
	}
	parsed := parseMCPResults(text)
	if len(parsed) == 0 {
		return nil, nil
	}
	var answerParts []string
	results := []SearchResult{}
	var inline []ExtractedContent
	for i, r := range parsed {
		title := r.title
		if title == "" {
			title = fallbackSourceLabel(r.url, i)
		}
		if snippet := jsSlice(collapseWS(r.content), 0, 500); snippet != "" {
			answerParts = append(answerParts, snippet+"\nSource: "+title+" ("+r.url+")")
		}
		results = append(results, SearchResult{Title: title, URL: r.url})
		if o.IncludeContent && r.content != "" {
			inline = append(inline, ExtractedContent{URL: r.url, Title: title, Content: r.content})
		}
	}
	return toSearchResponse(strings.Join(answerParts, "\n\n"), results, inline), nil
}

func searchWithExaMCP(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	n := 5.0
	if o.NumResults != nil && !math.IsNaN(*o.NumResults) && !math.IsInf(*o.NumResults, 0) {
		n = *o.NumResults
	}
	basic := map[string]any{"query": buildMCPQuery(query, o), "numResults": n}
	filtered := o.IncludeContent || o.RecencyFilter != "" || len(o.DomainFilter) > 0
	if !filtered {
		return searchWithExaMCPTool(ctx, exaMCPBasicTool, basic, o)
	}
	// Filtered searches need the advanced tool, which not every deployment exposes.
	args := exaSearchArgs(query, o)
	args["enableHighlights"] = true
	args["textMaxCharacters"] = 3000
	if o.IncludeContent {
		args["textMaxCharacters"] = 50000
	}
	r, err := searchWithExaMCPTool(ctx, exaMCPAdvancedTool, args, o)
	if err != nil {
		if isAbortError(err) {
			return nil, err
		}
		// The basic tool ignores every argument except query/numResults, so the filters degrade
		// into the query text.
		return searchWithExaMCPTool(ctx, exaMCPBasicTool, basic, o)
	}
	return r, nil
}

// SearchWithExa searches with the keyed API when a credential is configured and with the keyless
// MCP endpoint otherwise. A nil response (and nil error) means "no results".
func SearchWithExa(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := resolveProviderCredential(ctx, "Exa", "exaApiKey", "EXA_API_KEY")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return searchWithExaMCP(ctx, query, o)
	}
	base, err := apiBase("exaBaseUrl", "EXA_BASE_URL", exaAPIBaseURL)
	if err != nil {
		return nil, err
	}
	body := exaSearchArgs(query, o)
	if o.IncludeContent {
		body["contents"] = map[string]any{"text": true, "highlights": true}
	} else {
		body["contents"] = map[string]any{"highlights": true}
	}
	payload, _ := json.Marshal(body)
	tctx, cancel := withTimeout(ctx, exaTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, base+"/search", RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"X-Api-Key": {apiKey}, "Content-Type": {"application/json"}, "X-Exa-Integration": {"pi-web-access"}}}, []string{"x-api-key"})
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Exa API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	var data struct {
		Results []exaItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("Exa API returned invalid JSON: %v", err)
	}
	var inline []ExtractedContent
	if o.IncludeContent {
		inline = mapExaInline(data.Results)
	}
	return toSearchResponse(buildAnswerFromSearchResults(data.Results), mapExaResults(data.Results), inline), nil
}
