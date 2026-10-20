package websearch

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Port of search1api.ts: Search1API search (optionally with deep crawling) and Crawl.

const (
	search1apiSearchURL     = "https://api.search1api.com/search"
	search1apiCrawlURL      = "https://api.search1api.com/crawl"
	search1apiSearchTimeout = 60 * time.Second
	search1apiCrawlTimeout  = 60 * time.Second
	search1apiGetKeyAt      = "Create a key at https://dashboard.search1api.com"
)

// IsSearch1APIAvailable reports whether a Search1API credential source is configured.
func IsSearch1APIAvailable() bool {
	return hasProviderCredential("Search1API", "search1apiApiKey", "SEARCH1API_KEY")
}

func search1apiRequest(ctx context.Context, label, rawURL, key string, body map[string]any, timeout time.Duration) (any, error) {
	return hostedJSON(ctx, "Search1API "+label, hostedCall{URL: rawURL, Body: jsonBytes(body), Key: key, Timeout: timeout,
		Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}}, Credentials: []string{"Authorization"}})
}

func search1apiBody(query string, o SearchOptions) map[string]any {
	n := NormalizeSearchResultCount(o.NumResults)
	f := normalizeDomainFilters(o.DomainFilter)
	crawl := 0
	if o.IncludeContent {
		crawl = n
	}
	body := map[string]any{"query": query, "max_results": n, "crawl_results": crawl}
	if len(f.allowed) > 0 {
		body["include_sites"] = f.allowed
	}
	if len(f.blocked) > 0 {
		body["exclude_sites"] = f.blocked
	}
	if o.RecencyFilter != "" {
		body["time_range"] = o.RecencyFilter
	}
	return body
}

// SearchWithSearch1API queries Search1API.
func SearchWithSearch1API(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	key, err := requireHostedKey(ctx, "Search1API", "Search1API", "search1apiApiKey", "SEARCH1API_KEY", search1apiGetKeyAt)
	if err != nil {
		return nil, err
	}
	data, err := search1apiRequest(ctx, "Search", search1apiSearchURL, key, search1apiBody(query, o), search1apiSearchTimeout)
	if err != nil {
		return nil, err
	}
	items, ok := asMap(data)["results"].([]any)
	if !ok {
		return nil, fmt.Errorf("Search1API Search API returned an unexpected response shape")
	}
	results := []SearchResult{}
	var inline []ExtractedContent
	for _, it := range items {
		item := asMap(it)
		link, ok := strField(item, "link")
		if !ok || jsTrim(link) == "" {
			continue
		}
		link = jsTrim(link)
		title := link
		if t, ok := strField(item, "title"); ok && jsTrim(t) != "" {
			title = jsTrim(t)
		}
		snippet := ""
		if s, ok := strField(item, "snippet"); ok {
			snippet = collapseWS(s)
		}
		results = append(results, SearchResult{Title: title, URL: link, Snippet: snippet})
		if c, ok := strField(item, "content"); ok && jsTrim(c) != "" {
			t, _ := strField(item, "title")
			inline = append(inline, ExtractedContent{URL: link, Title: jsTrim(t), Content: c})
		}
	}
	resp := &SearchResponse{Answer: sourceLines(results), Results: results}
	if o.IncludeContent && len(inline) > 0 {
		resp.InlineContent = inline
	}
	return resp, nil
}

// ExtractWithSearch1API fetches one URL through Search1API Crawl.
func ExtractWithSearch1API(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	key, err := requireHostedKey(ctx, "Search1API", "Search1API", "search1apiApiKey", "SEARCH1API_KEY", search1apiGetKeyAt)
	if err != nil {
		return nil, err
	}
	timeout := search1apiCrawlTimeout
	if o.TimeoutMs != nil {
		timeout = time.Duration(max(int64(1), *o.TimeoutMs)) * time.Millisecond
	}
	data, err := search1apiRequest(ctx, "Crawl", search1apiCrawlURL, key, map[string]any{"url": rawURL}, timeout)
	if err != nil {
		return nil, err
	}
	result := asMap(asMap(data)["results"])
	if result == nil {
		return nil, fmt.Errorf("Search1API Crawl API returned an unexpected response shape")
	}
	c, _ := strField(result, "content")
	content := jsTrim(c)
	if content == "" {
		return nil, nil
	}
	t, _ := strField(result, "title")
	return &ExtractedContent{URL: rawURL, Title: jsTrim(t), Content: content}, nil
}
