package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Port of serpbase.ts.

const serpbaseAPIURL = "https://api.serpbase.dev/google/search"

// IsSerpBaseAvailable reports whether a SerpBase credential source is configured (serpbase.ts:187).
func IsSerpBaseAvailable() bool {
	return hasProviderCredential("SerpBase", "serpbaseApiKey", "SERPBASE_API_KEY")
}

// SearchWithSerpBase queries SerpBase's Google endpoint (serpbase.ts:191). Its default result
// count is 10, not the shared 5, and it authenticates with an api_key query parameter.
func SearchWithSerpBase(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "SerpBase", "SerpBase", "serpbaseApiKey", "SERPBASE_API_KEY", "https://serpbase.dev")
	if err != nil {
		return nil, err
	}
	numResults := normalizeCountDefault(o.NumResults, 10)
	filters := normalizeDomainFilters(o.DomainFilter)
	params := []string{"q=" + formEscape(buildSiteQuery(query, filters)), "api_key=" + formEscape(apiKey), "num=" + strconv.Itoa(numResults)}
	if tbs, ok := googleRecencyTBS[o.RecencyFilter]; ok {
		params = append(params, "tbs="+formEscape(tbs))
	}
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	data, err := restCallA(ctx, tctx, "SerpBase API", apiKey, serpbaseAPIURL+"?"+strings.Join(params, "&"),
		RequestInit{Method: "GET", Header: http.Header{"Accept": {"application/json"}}}, nil)
	if err != nil {
		return nil, err
	}
	envelope, ok := jsonObject(data)
	if !ok {
		return nil, fmt.Errorf("SerpBase API returned invalid response: expected an object envelope")
	}
	if msg, ok := envelope["error"].(string); ok && jsTrim(msg) != "" {
		suffix := ""
		switch status := envelope["status"].(type) {
		case float64:
			suffix = " (status " + jsNumber(status) + ")"
		case string:
			suffix = " (status " + status + ")"
		}
		return nil, fmt.Errorf("SerpBase API returned invalid response: %s%s", msg, suffix)
	}
	var organic []any
	var found bool
	for _, key := range []string{"organic_results", "organic", "results"} {
		// `a ?? b ?? c`: the first key that is present and not null, even when it is not an array.
		if v, present := envelope[key]; present && v != nil {
			organic, found = v.([]any)
			if !found {
				return nil, fmt.Errorf("SerpBase API returned invalid response: expected organic_results array")
			}
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("SerpBase API returned invalid response: expected organic_results array")
	}
	results := []SearchResult{}
	for _, raw := range organic {
		item, ok := jsonObject(raw)
		if !ok {
			continue
		}
		link := ""
		if s, ok := item["link"].(string); ok {
			link = s
		} else if s, ok := item["url"].(string); ok {
			link = s
		}
		if link == "" || !filters.matches(link) {
			continue
		}
		title := fmt.Sprintf("Source %d", len(results)+1)
		if s, ok := item["title"].(string); ok && jsTrim(s) != "" {
			title = s
		}
		snippet := ""
		if s, ok := item["snippet"].(string); ok {
			snippet = s
		} else if s, ok := item["description"].(string); ok {
			snippet = s
		}
		results = append(results, SearchResult{Title: title, URL: link, Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
