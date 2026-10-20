package websearch

import (
	"context"
	"fmt"
)

// Port of you.ts.

const youSearchURL = "https://ydc-index.io/v1/search"

// IsYouAvailable reports whether a You.com credential source is configured (you.ts:96; the
// environment variable is YDC_API_KEY).
func IsYouAvailable() bool { return hasProviderCredential("You.com", "youApiKey", "YDC_API_KEY") }

// youBody keeps the upstream key order: query, count, freshness, include_domains, exclude_domains.
type youBody struct {
	Query     string   `json:"query"`
	Count     int      `json:"count"`
	Freshness string   `json:"freshness,omitempty"`
	Include   []string `json:"include_domains,omitempty"`
	Exclude   []string `json:"exclude_domains,omitempty"`
}

// SearchWithYou queries the You.com search API (you.ts:100).
func SearchWithYou(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "You.com", "You.com", "youApiKey", "YDC_API_KEY", "https://you.com/platform/api-keys")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	body := youBody{Query: query, Count: numResults, Freshness: o.RecencyFilter, Include: filters.allowed, Exclude: filters.blocked}
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	var entries []any
	_, err = restCallB(ctx, tctx, "You.com", apiKey, youSearchURL, RequestInit{Method: "POST", Body: marshalJS(body),
		Header: headerOf("Accept", "application/json", "Content-Type", "application/json", "X-API-Key", apiKey)}, []string{"X-API-Key"}, func(data any) error {
		results, _ := jsonObject(data)
		var web any
		if m, ok := jsonObject(results["results"]); ok {
			web = m["web"]
		}
		list, ok := web.([]any)
		if !ok {
			return fmt.Errorf("You.com returned invalid response: expected results.web array")
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
		link, ok := entry["url"].(string)
		if !ok || link == "" {
			continue
		}
		resultURL, err := ParseURL(link, nil)
		if err != nil || (resultURL.Scheme != "http" && resultURL.Scheme != "https") {
			continue
		}
		snippet := ""
		if d, ok := entry["description"].(string); ok && jsTrim(d) != "" {
			snippet = d
		} else if snippets, ok := entry["snippets"].([]any); ok && len(snippets) > 0 {
			if s, ok := snippets[0].(string); ok {
				snippet = s
			}
		}
		title, ok := trimmedString(entry["title"])
		if !ok {
			title = fmt.Sprintf("Source %d", len(results)+1)
		}
		results = append(results, SearchResult{Title: title, URL: resultURL.String(), Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
