package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Port of brave.ts.

const (
	braveAPIBaseURL   = "https://api.search.brave.com/res/v1"
	braveSearchTimout = 30 * time.Second
)

func braveKeyMissing() error {
	return fmt.Errorf("Brave Search API key not found. Either:\n  1. Create %s with { \"braveApiKey\": \"your-key\" }\n  2. Set BRAVE_API_KEY environment variable\nGet a key at https://brave.com/search/api/", ConfigPath())
}

// IsBraveAvailable reports whether a Brave credential source is configured.
func IsBraveAvailable() bool { return hasProviderCredential("Brave", "braveApiKey", "BRAVE_API_KEY") }

func buildBraveQuery(query string, f domainFilters) string {
	parts := []string{query}
	if len(f.allowed) == 1 {
		parts = append(parts, "site:"+f.allowed[0])
	} else if len(f.allowed) > 1 {
		sites := make([]string, len(f.allowed))
		for i, d := range f.allowed {
			sites[i] = "site:" + d
		}
		parts = append(parts, strings.Join(sites, " OR "))
	}
	for _, d := range f.blocked {
		parts = append(parts, "NOT site:"+d)
	}
	return strings.Join(parts, " ")
}

// SearchWithBrave queries the Brave Search API.
func SearchWithBrave(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	base, err := apiBase("braveBaseUrl", "BRAVE_BASE_URL", braveAPIBaseURL)
	if err != nil {
		return nil, err
	}
	apiKey, err := resolveProviderCredential(ctx, "Brave", "braveApiKey", "BRAVE_API_KEY")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, braveKeyMissing()
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	count := numResults
	if len(o.DomainFilter) > 0 {
		count = 20
	}
	params := "q=" + formEscape(buildBraveQuery(query, filters)) + "&count=" + strconv.Itoa(count)
	if o.RecencyFilter != "" {
		if f, ok := map[string]string{"day": "pd", "week": "pw", "month": "pm", "year": "py"}[o.RecencyFilter]; ok {
			params += "&freshness=" + f
		}
	}
	tctx, cancel := withTimeout(ctx, braveSearchTimout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, base+"/web/search?"+params, RequestInit{Method: "GET",
		Header: http.Header{"X-Subscription-Token": {apiKey}, "Accept": {"application/json"}}}, []string{"X-Subscription-Token"})
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Brave Search API error %d: %s", resp.StatusCode, slice300(RedactCredential(body, apiKey)))
	}
	var data struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, fmt.Errorf("Brave Search API returned invalid JSON: %v", err)
	}
	results := []SearchResult{}
	for _, item := range data.Web.Results {
		if item.URL == "" || !filters.matches(item.URL) {
			continue
		}
		title := item.Title
		if title == "" {
			title = item.URL
		}
		results = append(results, SearchResult{Title: title, URL: item.URL, Snippet: item.Description})
		if len(results) >= numResults {
			break
		}
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
