package websearch

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"
)

// Port of querit.ts: Querit search and Contents.

const (
	queritSearchURL      = "https://api.querit.ai/v1/search"
	queritContentsURL    = "https://api.querit.ai/v1/contents"
	queritSearchTimeout  = 60 * time.Second
	queritContentsTimout = 60 * time.Second
	queritMaxContentURLs = 10
)

// IsQueritAvailable reports whether a Querit credential source is configured.
func IsQueritAvailable() bool {
	return hasProviderCredential("Querit", "queritApiKey", "QUERIT_API_KEY")
}

func queritKey(ctx context.Context) (string, error) {
	return requireProviderKey(ctx, "Querit API", "Querit", "queritApiKey", "QUERIT_API_KEY", "Create a key at https://www.querit.ai/en/dashboard/api-keys")
}

var queritRecency = map[string]string{"day": "d1", "week": "w1", "month": "m1", "year": "y1"}

func queritBody(query string, o SearchOptions) map[string]any {
	f := normalizeDomainFilters(o.DomainFilter)
	filters := map[string]any{}
	if len(f.allowed) > 0 || len(f.blocked) > 0 {
		sites := map[string]any{}
		if len(f.allowed) > 0 {
			sites["include"] = f.allowed
		}
		if len(f.blocked) > 0 {
			sites["exclude"] = f.blocked
		}
		filters["sites"] = sites
	}
	if d := queritRecency[o.RecencyFilter]; d != "" {
		filters["timeRange"] = map[string]any{"date": d}
	}
	body := map[string]any{"query": query, "count": NormalizeSearchResultCount(o.NumResults)}
	if len(filters) > 0 {
		body["filters"] = filters
	}
	return body
}

func queritContentsTimeout(ms *int64) time.Duration {
	if ms == nil {
		return queritContentsTimout
	}
	return time.Duration(max(int64(1), *ms)) * time.Millisecond
}

func queritCrawlSeconds(ms *int64) int {
	if ms == nil {
		return 10
	}
	return int(math.Max(1, math.Min(math.Ceil(float64(*ms)/1000), 60)))
}

// queritRequest posts one request. A request that hit its own deadline (not the caller's) is a
// "timed out" failure, as the original's TimeoutError.
func queritRequest(parent context.Context, label, rawURL, key string, body map[string]any, timeout time.Duration) (any, error) {
	tctx, cancel := withTimeout(parent, timeout)
	defer cancel()
	data, err := hostedJSON(tctx, "Querit "+label, hostedCall{URL: rawURL, Body: jsonBytes(body), Key: key, Timeout: timeout + time.Second,
		Header: http.Header{"Authorization": {"Bearer " + key}, "Accept": {"application/json"}, "Content-Type": {"application/json"}}, Credentials: []string{"Authorization"}})
	if err != nil && parent.Err() == nil && tctx.Err() != nil {
		return nil, fmt.Errorf("Querit %s API request timed out after %d seconds", label, int(math.Ceil(timeout.Seconds())))
	}
	return data, err
}

func queritAssertSuccess(label string, data any) error {
	m := asMap(data)
	code, isNum := m["error_code"].(float64)
	if s, ok := m["error_code"].(string); ok {
		if n, ok := jsToNumber(s); ok {
			code, isNum = n, true
		}
	}
	if isNum && code == 200 {
		return nil
	}
	rendered := "unknown"
	if v, ok := m["error_code"]; ok && v != nil {
		rendered = jsString(v)
	}
	msg := ""
	if s, ok := strField(m, "error_msg"); ok && jsTrim(s) != "" {
		msg = ": " + jsTrim(s)
	}
	return fmt.Errorf("Querit %s API returned error %s%s", label, rendered, msg)
}

type queritContents struct {
	Results  []any
	Statuses []any
}

func queritFetchBatch(ctx context.Context, urls []string, key string, o ExtractOptions) (*queritContents, error) {
	body := map[string]any{"urls": urls, "format": "markdown", "crawlTimeout": queritCrawlSeconds(o.TimeoutMs), "extrasMeta": true}
	data, err := queritRequest(ctx, "Contents", queritContentsURL, key, body, queritContentsTimeout(o.TimeoutMs))
	if err != nil {
		return nil, err
	}
	if err := queritAssertSuccess("Contents", data); err != nil {
		return nil, err
	}
	m := asMap(data)
	res, ok1 := m["results"].([]any)
	st, ok2 := m["statuses"].([]any)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("Querit Contents API returned an unexpected response shape")
	}
	return &queritContents{Results: res, Statuses: st}, nil
}

func queritFindResult(d *queritContents, requested string, index, count int) map[string]any {
	for _, r := range d.Results {
		m := asMap(r)
		if u, _ := strField(m, "url"); u == requested {
			return m
		}
		if u, _ := strField(asMap(m["extrasMeta"]), "url"); u == requested {
			return m
		}
	}
	if count == 1 && len(d.Results) > 0 {
		return asMap(d.Results[0])
	}
	if len(d.Results) == count {
		return asMap(d.Results[index])
	}
	return nil
}

func queritMap(result map[string]any, requested string) *ExtractedContent {
	if result == nil {
		return nil
	}
	c, ok := strField(result, "content")
	if !ok || jsTrim(c) == "" {
		return nil
	}
	title := ""
	if t, ok := strField(asMap(result["extrasMeta"]), "title"); ok {
		title = jsTrim(t)
	}
	return &ExtractedContent{URL: requested, Title: title, Content: jsTrim(c)}
}

func queritFailedStatus(d *queritContents, result map[string]any, index int) bool {
	var st map[string]any
	if id, _ := strField(result, "id"); result != nil && id != "" {
		for _, s := range d.Statuses {
			if m := asMap(s); m != nil && m["id"] == id {
				st = m
				break
			}
		}
	} else if index < len(d.Statuses) {
		st = asMap(d.Statuses[index])
	}
	s, _ := strField(st, "status")
	return s == "failed"
}

// SearchWithQuerit queries Querit and, with includeContent, Querit Contents (ten URLs a batch).
func SearchWithQuerit(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	key, err := queritKey(ctx)
	if err != nil {
		return nil, err
	}
	data, err := queritRequest(ctx, "Search", queritSearchURL, key, queritBody(query, o), queritSearchTimeout)
	if err != nil {
		return nil, err
	}
	if err := queritAssertSuccess("Search", data); err != nil {
		return nil, err
	}
	items, ok := asMap(asMap(data)["results"])["result"].([]any)
	if !ok {
		return nil, fmt.Errorf("Querit Search API returned an unexpected response shape")
	}
	results := []SearchResult{}
	for _, it := range items {
		item := asMap(it)
		u, ok := strField(item, "url")
		if !ok || jsTrim(u) == "" {
			continue
		}
		u = jsTrim(u)
		title := u
		if t, ok := strField(item, "title"); ok && jsTrim(t) != "" {
			title = jsTrim(t)
		}
		snippet := ""
		if s, ok := strField(item, "snippet"); ok {
			snippet = collapseWS(s)
		}
		results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
	}
	resp := &SearchResponse{Answer: sourceLines(results), Results: results}
	if o.IncludeContent && len(results) > 0 {
		var inline []ExtractedContent
		for offset := 0; offset < len(results); offset += queritMaxContentURLs {
			end := min(offset+queritMaxContentURLs, len(results))
			batch := make([]string, 0, end-offset)
			for _, r := range results[offset:end] {
				batch = append(batch, r.URL)
			}
			d, err := queritFetchBatch(ctx, batch, key, ExtractOptions{})
			if err != nil {
				return nil, err
			}
			for i, u := range batch {
				if m := queritMap(queritFindResult(d, u, i, len(batch)), u); m != nil {
					inline = append(inline, *m)
				}
			}
		}
		if len(inline) > 0 {
			resp.InlineContent = inline
		}
	}
	return resp, nil
}

// ExtractWithQuerit fetches one URL through Querit Contents.
func ExtractWithQuerit(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	key, err := queritKey(ctx)
	if err != nil {
		return nil, err
	}
	d, err := queritFetchBatch(ctx, []string{rawURL}, key, o)
	if err != nil {
		return nil, err
	}
	result := queritFindResult(d, rawURL, 0, 1)
	if m := queritMap(result, rawURL); m != nil {
		return m, nil
	}
	if queritFailedStatus(d, result, 0) {
		return nil, fmt.Errorf("Querit Contents API failed to crawl %s", rawURL)
	}
	return nil, nil
}
