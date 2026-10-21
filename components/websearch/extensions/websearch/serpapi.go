package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Port of serpapi.ts.

const serpapiSearchURL = "https://serpapi.com/search.json"

// IsSerpAPIAvailable reports whether a SerpApi credential source is configured (serpapi.ts:128;
// the environment variable is SERPAPI_KEY).
func IsSerpAPIAvailable() bool {
	return hasProviderCredential("SerpApi", "serpapiApiKey", "SERPAPI_KEY")
}

// SearchWithSerpAPI queries SerpApi's Google engine (serpapi.ts:132).
func SearchWithSerpAPI(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "SerpApi", "SerpApi", "serpapiApiKey", "SERPAPI_KEY", "https://serpapi.com/manage-api-key")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	params := []string{"engine=google", "q=" + formEscape(buildSiteQuery(query, filters)), "api_key=" + formEscape(apiKey), "num=" + strconv.Itoa(googleRequestCount(o, numResults))}
	if o.RecencyFilter != "" {
		params = append(params, "tbs="+formEscape(googleRecencyTBS[o.RecencyFilter]))
	}
	act := logAPIStart(query)
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	var entries []any
	_, err = restCallB(ctx, tctx, act, "SerpApi", apiKey, serpapiSearchURL+"?"+strings.Join(params, "&"),
		RequestInit{Method: "GET", Header: http.Header{"Accept": {"application/json"}}}, nil, func(data any) error {
			envelope, ok := jsonObject(data)
			if !ok {
				return fmt.Errorf("SerpApi returned invalid response: expected an object envelope")
			}
			if msg, ok := trimmedString(envelope["error"]); ok {
				return fmt.Errorf("SerpApi returned invalid response: %s", msg)
			}
			organic, ok := envelope["organic_results"].([]any)
			if !ok {
				return fmt.Errorf("SerpApi returned invalid response: expected organic_results array")
			}
			entries = organic
			return nil
		})
	if err != nil {
		return nil, err
	}
	results := []SearchResult{}
	for _, raw := range entries {
		entry, ok := jsonObject(raw)
		if !ok {
			continue
		}
		link, ok := entry["link"].(string)
		if !ok || link == "" || !filters.matches(link) {
			continue
		}
		title, ok := trimmedString(entry["title"])
		if !ok {
			title = fmt.Sprintf("Source %d", len(results)+1)
		}
		snippet, _ := entry["snippet"].(string)
		results = append(results, SearchResult{Title: title, URL: link, Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
