package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Port of serply.ts.

const serplySearchURL = "https://api.serply.io/v1/search"

// IsSerplyAvailable reports whether a Serply credential source is configured (serply.ts:123).
func IsSerplyAvailable() bool {
	return hasProviderCredential("Serply", "serplyApiKey", "SERPLY_API_KEY")
}

// SearchWithSerply queries the Serply Google search API (serply.ts:127). The key travels in the
// X-Api-Key header, never the query string, and is dropped when a redirect leaves the origin.
func SearchWithSerply(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "Serply", "Serply", "serplyApiKey", "SERPLY_API_KEY", "https://serply.io")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	params := []string{"q=" + formEscape(buildSiteQuery(query, filters)), "num=" + strconv.Itoa(googleRequestCount(o, numResults))}
	if o.RecencyFilter != "" {
		params = append(params, "tbs="+formEscape(googleRecencyTBS[o.RecencyFilter]))
	}
	act := logAPIStart(query)
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	var entries []any
	_, err = restCallB(ctx, tctx, act, "Serply", apiKey, serplySearchURL+"?"+strings.Join(params, "&"),
		RequestInit{Method: "GET", Header: http.Header{"Accept": {"application/json"}, "X-Api-Key": {apiKey}}}, []string{"X-Api-Key"}, func(data any) error {
			envelope, ok := jsonObject(data)
			if !ok {
				return fmt.Errorf("Serply returned invalid response: expected an object envelope")
			}
			if msg, ok := trimmedString(envelope["detail"]); ok {
				return fmt.Errorf("Serply returned invalid response: %s", msg)
			}
			list, ok := envelope["results"].([]any)
			if !ok {
				return fmt.Errorf("Serply returned invalid response: expected results array")
			}
			entries = list
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
		if !ok || link == "" {
			continue
		}
		resultURL, err := ParseURL(link, nil)
		if err != nil || (resultURL.Scheme != "http" && resultURL.Scheme != "https") {
			continue
		}
		href := resultURL.String()
		if !filters.matches(href) {
			continue
		}
		title, ok := trimmedString(entry["title"])
		if !ok {
			title = fmt.Sprintf("Source %d", len(results)+1)
		}
		snippet, _ := entry["description"].(string)
		results = append(results, SearchResult{Title: title, URL: href, Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
