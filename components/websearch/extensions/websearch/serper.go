package websearch

import (
	"context"
	"fmt"
)

// Port of serper.ts.

const serperSearchURL = "https://google.serper.dev/search"

// IsSerperAvailable reports whether a Serper credential source is configured (serper.ts:127).
func IsSerperAvailable() bool {
	return hasProviderCredential("Serper", "serperApiKey", "SERPER_API_KEY")
}

type serperBody struct {
	Q   string `json:"q"`
	Num int    `json:"num"`
	Tbs string `json:"tbs,omitempty"`
}

// SearchWithSerper queries the Serper Google search API (serper.ts:131).
func SearchWithSerper(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "Serper", "Serper", "serperApiKey", "SERPER_API_KEY", "https://serper.dev")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	body := serperBody{Q: buildSiteQuery(query, filters), Num: googleRequestCount(o, numResults)}
	if o.RecencyFilter != "" {
		body.Tbs = googleRecencyTBS[o.RecencyFilter]
	}
	act := logAPIStart(query)
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	data, status, err := restCallA(ctx, tctx, act, "Serper API", apiKey, serperSearchURL, RequestInit{Method: "POST", Body: marshalJS(body),
		Header: headerOf("X-API-KEY", apiKey, "Content-Type", "application/json", "Accept", "application/json")}, []string{"X-API-KEY"})
	if err != nil {
		return nil, err
	}
	// Every outcome after a readable body completes the entry with the status, whatever the envelope check says.
	act.complete(status)
	envelope, ok := jsonObject(data)
	if !ok {
		return nil, fmt.Errorf("Serper API returned invalid response: expected an object envelope")
	}
	organic, ok := envelope["organic"].([]any)
	if !ok {
		return nil, fmt.Errorf("Serper API returned invalid response: expected organic array")
	}
	results := []SearchResult{}
	for _, raw := range organic {
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
