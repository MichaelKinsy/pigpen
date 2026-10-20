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

// Port of tinyfish.ts: TinyFish Search (GET, paged) and TinyFish Fetch (POST, batched).

const (
	tinyfishSearchURL          = "https://api.search.tinyfish.ai"
	tinyfishFetchURL           = "https://api.fetch.tinyfish.ai"
	tinyfishSearchTimeout      = 60 * time.Second
	tinyfishFetchTimeout       = 150 * time.Second
	tinyfishMaxFetchURLs       = 10
	tinyfishMaxPerURLTimeoutMs = 110000
	tinyfishGetKeyAt           = "Get a key at https://agent.tinyfish.ai/api-keys"
)

// IsTinyFishAvailable reports whether a TinyFish credential source is configured.
func IsTinyFishAvailable() bool {
	return hasProviderCredential("TinyFish", "tinyfishApiKey", "TINYFISH_API_KEY")
}

func tinyfishKey(ctx context.Context) (string, error) {
	return requireProviderKey(ctx, "TinyFish", "TinyFish", "tinyfishApiKey", "TINYFISH_API_KEY", tinyfishGetKeyAt)
}

// tinyfishRecencyMinutes is tinyfish.ts recencyMinutes.
var tinyfishRecencyMinutes = map[string]int{"day": 1440, "week": 10080, "month": 43200, "year": 525600}

func tinyfishSearchURLFor(query string, o SearchOptions, page int) string {
	params := []string{"query=" + formEscape(query)}
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.allowed) > 0 {
		params = append(params, "include_domains="+formEscape(strings.Join(f.allowed, ",")))
	}
	if len(f.blocked) > 0 {
		params = append(params, "exclude_domains="+formEscape(strings.Join(f.blocked, ",")))
	}
	if m, ok := tinyfishRecencyMinutes[o.RecencyFilter]; ok {
		params = append(params, "recency_minutes="+strconv.Itoa(m))
	}
	if page > 0 {
		params = append(params, "page="+strconv.Itoa(page))
	}
	return tinyfishSearchURL + "?" + strings.Join(params, "&")
}

func tinyfishRequest(ctx context.Context, label, rawURL, method, key string, body []byte, timeout time.Duration) (any, error) {
	h := http.Header{"X-Api-Key": {key}}
	if body != nil {
		h.Set("Content-Type", "application/json")
	}
	return hostedJSON(ctx, "TinyFish "+label, hostedCall{URL: rawURL, Method: method, Body: body, Header: h, Key: key, Timeout: timeout, Credentials: []string{"X-Api-Key"}})
}

func tinyfishMapSearch(results []any) []SearchResult {
	out := []SearchResult{}
	for _, r := range results {
		item := asMap(r)
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
		out = append(out, SearchResult{Title: title, URL: u, Snippet: snippet})
	}
	return out
}

func dedupeResults(results []SearchResult, limit int) []SearchResult {
	seen := map[string]bool{}
	out := []SearchResult{}
	for _, r := range results {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func tinyfishPerURLTimeout(ms *int64) int64 {
	if ms == nil {
		return tinyfishMaxPerURLTimeoutMs
	}
	v := *ms
	if v > tinyfishMaxPerURLTimeoutMs {
		v = tinyfishMaxPerURLTimeoutMs
	}
	if v < 1 {
		v = 1
	}
	return v
}

type tinyfishFetchResponse struct {
	Results []map[string]json.RawMessage
	Errors  []map[string]any
	hasRes  bool
	hasErrs bool
}

func tinyfishFetchBatch(ctx context.Context, urls []string, key string, o ExtractOptions) (*tinyfishFetchResponse, error) {
	body := map[string]any{"urls": urls, "format": "markdown", "per_url_timeout_ms": tinyfishPerURLTimeout(o.TimeoutMs)}
	if purpose := jsTrim(o.Prompt); purpose != "" {
		body["purpose"] = jsSlice(purpose, 0, 2000)
	}
	status, text, err := hostedSend(ctx, hostedCall{URL: tinyfishFetchURL, Method: "POST", Body: jsonBytes(body), Header: http.Header{"X-Api-Key": {key}, "Content-Type": {"application/json"}},
		Key: key, Timeout: tinyfishFetchTimeout, Credentials: []string{"X-Api-Key"}})
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("TinyFish Fetch API error %d: %s", status, slice300(RedactCredential(text, key)))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		var probe any
		if json.Unmarshal([]byte(text), &probe) != nil {
			return nil, fmt.Errorf("TinyFish Fetch API returned invalid JSON: %v", err)
		}
		// Valid JSON that is not an object has neither array.
		return &tinyfishFetchResponse{}, nil
	}
	r := &tinyfishFetchResponse{}
	if v, ok := raw["results"]; ok && json.Unmarshal(v, &r.Results) == nil && string(v) != "null" {
		r.hasRes = true
	}
	if v, ok := raw["errors"]; ok && json.Unmarshal(v, &r.Errors) == nil && string(v) != "null" {
		r.hasErrs = true
	}
	return r, nil
}

func (r *tinyfishFetchResponse) shapeOK() error {
	if !r.hasRes || !r.hasErrs {
		return fmt.Errorf("TinyFish Fetch API returned an unexpected response shape")
	}
	return nil
}

func rawString(m map[string]json.RawMessage, k string) (string, bool) {
	var s string
	if v, ok := m[k]; ok && json.Unmarshal(v, &s) == nil && len(v) > 0 && v[0] == '"' {
		return s, true
	}
	return "", false
}

func tinyfishContent(result map[string]json.RawMessage) string {
	if s, ok := rawString(result, "text"); ok {
		return jsTrim(s)
	}
	if v := result["text"]; len(v) > 0 && (v[0] == '{' || v[0] == '[') {
		return indentJSON(v)
	}
	return ""
}

func tinyfishMapFetch(result map[string]json.RawMessage, requested string) *ExtractedContent {
	if result == nil {
		return nil
	}
	content := tinyfishContent(result)
	if content == "" {
		return nil
	}
	title := ""
	if t, ok := rawString(result, "title"); ok {
		title = jsTrim(t)
	}
	return &ExtractedContent{URL: requested, Title: title, Content: content}
}

func findFetchResult(results []map[string]json.RawMessage, u string) map[string]json.RawMessage {
	for _, r := range results {
		if s, _ := rawString(r, "url"); s == u {
			return r
		}
		if s, _ := rawString(r, "final_url"); s == u {
			return r
		}
	}
	return nil
}

func tinyfishInline(ctx context.Context, urls []string, key string) ([]ExtractedContent, error) {
	var content []ExtractedContent
	for offset := 0; offset < len(urls); offset += tinyfishMaxFetchURLs {
		end := offset + tinyfishMaxFetchURLs
		if end > len(urls) {
			end = len(urls)
		}
		batch := urls[offset:end]
		data, err := tinyfishFetchBatch(ctx, batch, key, ExtractOptions{})
		if err != nil {
			return nil, err
		}
		if err := data.shapeOK(); err != nil {
			return nil, err
		}
		for _, u := range batch {
			if m := tinyfishMapFetch(findFetchResult(data.Results, u), u); m != nil {
				content = append(content, *m)
			}
		}
	}
	return content, nil
}

// SearchWithTinyFish queries TinyFish Search (two pages when more than ten results are wanted)
// and, with includeContent, TinyFish Fetch for the result pages.
func SearchWithTinyFish(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	key, err := tinyfishKey(ctx)
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	combined := []SearchResult{}
	pages := 1
	if numResults > 10 {
		pages = 2
	}
	for page := 0; page < pages; page++ {
		data, err := tinyfishRequest(ctx, "Search", tinyfishSearchURLFor(query, o, page), "GET", key, nil, tinyfishSearchTimeout)
		if err != nil {
			return nil, err
		}
		results, ok := asMap(data)["results"].([]any)
		if !ok {
			return nil, fmt.Errorf("TinyFish Search API returned an unexpected response shape")
		}
		combined = append(combined, tinyfishMapSearch(results)...)
		if len(results) < 10 {
			break
		}
	}
	results := dedupeResults(combined, numResults)
	resp := &SearchResponse{Answer: sourceLines(results), Results: results}
	if o.IncludeContent && len(results) > 0 {
		urls := make([]string, len(results))
		for i, r := range results {
			urls[i] = r.URL
		}
		inline, err := tinyfishInline(ctx, urls, key)
		if err != nil {
			return nil, err
		}
		if len(inline) > 0 {
			resp.InlineContent = inline
		}
	}
	return resp, nil
}

// ExtractWithTinyFish fetches one URL through TinyFish Fetch.
func ExtractWithTinyFish(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	key, err := tinyfishKey(ctx)
	if err != nil {
		return nil, err
	}
	data, err := tinyfishFetchBatch(ctx, []string{rawURL}, key, o)
	if err != nil {
		return nil, err
	}
	if err := data.shapeOK(); err != nil {
		return nil, err
	}
	result := findFetchResult(data.Results, rawURL)
	if result == nil && len(data.Results) > 0 {
		result = data.Results[0]
	}
	if m := tinyfishMapFetch(result, rawURL); m != nil {
		return m, nil
	}
	var fetchErr map[string]any
	for _, e := range data.Errors {
		if u, _ := e["url"].(string); u == rawURL {
			fetchErr = e
			break
		}
	}
	if fetchErr == nil && len(data.Errors) > 0 {
		fetchErr = data.Errors[0]
	}
	if fetchErr != nil {
		status := ""
		if n, ok := fetchErr["status"].(float64); ok {
			status = " (HTTP " + jsNumber(n) + ")"
		}
		msg := "unknown error"
		if s, _ := fetchErr["error"].(string); s != "" {
			msg = s
		}
		return nil, fmt.Errorf("TinyFish Fetch failed for %s: %s%s", rawURL, msg, status)
	}
	return nil, nil
}
