package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Port of serpdive.ts. The default model is the free tier: installing this provider must never
// start spending on a user's behalf without them choosing it.

const (
	serpdiveAPIURL  = "https://api.serpdive.com/v1/search"
	serpdiveTimeout = 60 * time.Second
)

// IsSerpdiveAvailable reports whether a SERPdive credential source is configured.
func IsSerpdiveAvailable() bool {
	return hasProviderCredential("SERPdive", "serpdiveApiKey", "SERPDIVE_API_KEY")
}

func serpdiveModel() string {
	var raw any
	if v, ok := os.LookupEnv("SERPDIVE_MODEL"); ok {
		raw = v
	} else {
		raw = configAny("serpdiveModel")
	}
	s, _ := raw.(string)
	switch v := strings.ToLower(jsTrim(s)); v {
	case "krill", "mako", "moby":
		return v
	}
	return "krill"
}

// SearchWithSerpdive queries the SERPdive search API.
func SearchWithSerpdive(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := resolveProviderCredential(ctx, "SERPdive", "serpdiveApiKey", "SERPDIVE_API_KEY")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("SERPdive API key not found. Either:\n  1. Create %s with { \"serpdiveApiKey\": \"your-key\" }\n  2. Set SERPDIVE_API_KEY environment variable\nGet a key at https://serpdive.com/dashboard/keys", ConfigPath())
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	model := serpdiveModel()
	q := query
	if hint := map[string]string{"day": "past 24 hours", "week": "past week", "month": "past month", "year": "past year"}[o.RecencyFilter]; hint != "" {
		q += " " + hint
	}
	max := numResults
	if max > 10 {
		max = 10
	}
	body := map[string]any{"query": q, "model": model, "max_results": max}
	if model != "krill" {
		body["answer"] = true
	}
	payload, _ := json.Marshal(body)
	tctx, cancel := withTimeout(ctx, serpdiveTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, serpdiveAPIURL, RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}}}, nil)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("SERPdive API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	var data struct {
		Answer  any `json:"answer"`
		Results []struct {
			URL     string `json:"url"`
			Title   any    `json:"title"`
			Content any    `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("SERPdive API returned invalid JSON: %v", err)
	}
	// SERPdive has no domain parameter: the filter narrows what came back, no more.
	passes := func(u string) bool {
		if len(filters.allowed) == 0 && len(filters.blocked) == 0 {
			return true
		}
		parsed, err := ParseURL(u, nil)
		if err != nil {
			return false
		}
		host := strings.ToLower(JSHostname(parsed))
		for _, d := range filters.blocked {
			if hostMatchesDomain(host, d) {
				return false
			}
		}
		if len(filters.allowed) == 0 {
			return true
		}
		for _, d := range filters.allowed {
			if hostMatchesDomain(host, d) {
				return true
			}
		}
		return false
	}
	out := &SearchResponse{Results: []SearchResult{}}
	for _, item := range data.Results {
		if item.URL == "" || !passes(item.URL) {
			continue
		}
		title, _ := item.Title.(string)
		if title == "" {
			title = "Source " + strconv.Itoa(len(out.Results)+1)
		}
		snippet := ""
		if s, ok := item.Content.(string); ok {
			snippet = collapseWS(s)
		}
		out.Results = append(out.Results, SearchResult{Title: title, URL: item.URL, Snippet: snippet})
		if len(out.Results) >= numResults {
			break
		}
	}
	if s, ok := data.Answer.(string); ok && jsTrim(s) != "" {
		out.Answer = s
	} else {
		out.Answer = sourceLines(out.Results)
	}
	if o.IncludeContent {
		for _, item := range data.Results {
			s, ok := item.Content.(string)
			if item.URL == "" || !passes(item.URL) || !ok || jsTrim(s) == "" {
				continue
			}
			title, _ := item.Title.(string)
			out.InlineContent = append(out.InlineContent, ExtractedContent{URL: item.URL, Title: title, Content: s})
		}
	}
	return out, nil
}

const providerDefaultTimeout = 60 * time.Second
