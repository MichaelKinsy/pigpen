package websearch

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Port of valyu.ts.

const (
	valyuSearchURL     = "https://api.valyu.ai/v1/search"
	valyuMaxSnippet    = 2500
	valyuMaxContent    = 4000
	valyuRecencyLayout = "2006-01-02"
)

// restNow is Date.now() for the providers that turn a recency filter into a date.
var restNow = time.Now

// IsValyuAvailable reports whether a Valyu credential source is configured (valyu.ts:84).
func IsValyuAvailable() bool { return hasProviderCredential("Valyu", "valyuApiKey", "VALYU_API_KEY") }

// jsCollapse is `s.replace(/\s+/g, " ").trim()`.
func jsCollapse(s string) string { return strings.Join(strings.FieldsFunc(s, isJSSpace), " ") }

// valyuText is text(): collapse whitespace, trim, cut to limit UTF-16 code units.
func valyuText(v any, limit int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = jsCollapse(s)
	if limit > 0 {
		s = jsSlice(s, 0, limit)
	}
	return s
}

// valyuBody keeps the upstream key order: query, max_num_results, the sources, start_date.
type valyuBody struct {
	Query     string   `json:"query"`
	Max       int      `json:"max_num_results"`
	Included  []string `json:"included_sources,omitempty"`
	Excluded  []string `json:"excluded_sources,omitempty"`
	StartDate string   `json:"start_date,omitempty"`
}

// SearchWithValyu queries the Valyu search API (valyu.ts:88). The domain filter goes to the API
// as included/excluded sources and the recency filter as a start date; content is bounded.
func SearchWithValyu(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "Valyu", "Valyu", "valyuApiKey", "VALYU_API_KEY", "https://platform.valyu.ai")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	body := valyuBody{Query: query, Max: numResults, Included: filters.allowed, Excluded: filters.blocked}
	if days, ok := map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}[o.RecencyFilter]; ok {
		body.StartDate = restNow().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(valyuRecencyLayout)
	}
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	data, err := restCallA(ctx, tctx, "Valyu API", apiKey, valyuSearchURL, RequestInit{Method: "POST", Body: marshalJS(body),
		Header: headerOf("x-api-key", apiKey, "Content-Type", "application/json", "Accept", "application/json")}, []string{"x-api-key"})
	if err != nil {
		return nil, err
	}
	envelope, ok := jsonObject(data)
	if !ok {
		return nil, fmt.Errorf("Valyu API returned invalid response: expected an object envelope")
	}
	if envelope["success"] != true {
		return nil, fmt.Errorf("Valyu API returned invalid response: expected success true")
	}
	entries, ok := envelope["results"].([]any)
	if !ok {
		return nil, fmt.Errorf("Valyu API returned invalid response: expected results array")
	}
	out := &SearchResponse{Results: []SearchResult{}}
	for _, raw := range entries {
		entry, ok := jsonObject(raw)
		if !ok {
			continue
		}
		u := valyuText(entry["url"], 0)
		if u == "" {
			continue
		}
		title := valyuText(entry["title"], 0)
		if title == "" {
			title = fmt.Sprintf("Source %d", len(out.Results)+1)
		}
		content := valyuText(entry["content"], valyuMaxContent)
		description := valyuText(entry["description"], valyuMaxSnippet)
		snippet := content
		if snippet == "" {
			snippet = description
		}
		out.Results = append(out.Results, SearchResult{Title: title, URL: u, Snippet: jsSlice(snippet, 0, valyuMaxSnippet)})
		if o.IncludeContent && content != "" {
			out.InlineContent = append(out.InlineContent, ExtractedContent{URL: u, Title: title, Content: content})
		}
		if len(out.Results) >= numResults {
			break
		}
	}
	out.Answer = sourceLines(out.Results)
	return out, nil
}
