package websearch

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Port of anysearch.ts.

const (
	anysearchAPIURL  = "https://api.anysearch.com/v1/search"
	anysearchTimeout = 30 * time.Second
)

// IsAnySearchAvailable is always true: AnySearch works without a key (anysearch.ts:117).
func IsAnySearchAvailable() bool { return true }

type anysearchBody struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type anysearchResult struct{ title, url, snippet, content string }

// SearchWithAnySearch queries AnySearch (anysearch.ts:121). The credential is optional: without
// one the request carries no Authorization header; a configured one is sent as a bearer token.
func SearchWithAnySearch(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if err := restConfigErr(); err != nil {
		return nil, err
	}
	apiKey, err := resolveProviderCredential(ctx, "AnySearch", "anysearchApiKey", "ANYSEARCH_API_KEY")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	header := http.Header{"Content-Type": {"application/json"}}
	if apiKey != "" {
		header.Set("Authorization", "Bearer "+apiKey)
	}
	tctx, cancel := withTimeout(ctx, restTimeout(anysearchTimeout))
	defer cancel()
	data, err := restCallA(ctx, tctx, "AnySearch API", apiKey, anysearchAPIURL, RequestInit{Method: "POST",
		Body: marshalJS(anysearchBody{Query: query, MaxResults: numResults}), Header: header}, []string{"Authorization"})
	if err != nil {
		return nil, err
	}
	parsed, err := parseAnySearch(data)
	if err != nil {
		return nil, err
	}
	if len(parsed) > numResults {
		parsed = parsed[:numResults]
	}
	out := &SearchResponse{Results: make([]SearchResult, len(parsed))}
	for i, r := range parsed {
		out.Results[i] = SearchResult{Title: r.title, URL: r.url, Snippet: r.snippet}
	}
	out.Answer = sourceLines(out.Results)
	if o.IncludeContent {
		for _, r := range parsed {
			if r.content != "" {
				out.InlineContent = append(out.InlineContent, ExtractedContent{URL: r.url, Title: r.title, Content: r.content})
			}
		}
	}
	return out, nil
}

func parseAnySearch(data any) ([]anysearchResult, error) {
	invalid := func(msg string) error { return fmt.Errorf("AnySearch API returned invalid response: %s", msg) }
	envelope, ok := jsonObject(data)
	if !ok {
		return nil, invalid("expected an object envelope")
	}
	if code, ok := envelope["code"].(float64); !ok || code != 0 {
		return nil, invalid("expected code 0")
	}
	d, ok := jsonObject(envelope["data"])
	if !ok {
		return nil, invalid("expected data object")
	}
	list, ok := d["results"].([]any)
	if !ok {
		return nil, invalid("expected data.results array")
	}
	if _, ok := jsonObject(d["metadata"]); !ok {
		return nil, invalid("expected data.metadata object")
	}
	results := make([]anysearchResult, 0, len(list))
	for i, raw := range list {
		item, ok := jsonObject(raw)
		if !ok {
			return nil, invalid(fmt.Sprintf("expected data.results[%d] object", i))
		}
		title, ok := item["title"].(string)
		if !ok {
			return nil, invalid(fmt.Sprintf("expected data.results[%d].title string", i))
		}
		u, ok := item["url"].(string)
		if !ok {
			return nil, invalid(fmt.Sprintf("expected data.results[%d].url string", i))
		}
		snippet, ok := item["snippet"].(string)
		if !ok {
			return nil, invalid(fmt.Sprintf("expected data.results[%d].snippet string", i))
		}
		content := ""
		if c := item["content"]; c != nil {
			s, ok := c.(string)
			if !ok {
				return nil, invalid(fmt.Sprintf("expected data.results[%d].content string", i))
			}
			content = s
		}
		if u == "" {
			return nil, invalid(fmt.Sprintf("expected data.results[%d].url to be non-empty", i))
		}
		results = append(results, anysearchResult{title, u, snippet, content})
	}
	return results, nil
}
