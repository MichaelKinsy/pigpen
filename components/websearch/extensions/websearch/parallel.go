package websearch

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// Port of parallel.ts: Parallel search and extract (the API; parallel-mcp.go is the MCP server).

const (
	parallelSearchURL     = "https://api.parallel.ai/v1/search"
	parallelExtractURL    = "https://api.parallel.ai/v1/extract"
	parallelMinKeyLength  = 8
	parallelMinUseful     = 500
	parallelSearchTimeout = 60 * time.Second
)

var parallelPlaceholders = map[string]bool{
	"replace_with_your_parallel_api_key": true, "parallel_api_key": true, "your-key": true, "your-key-here": true,
	"your-api-key-here": true, "dummy": true, "placeholder": true, "changeme": true, "insert-your-key": true,
	"insert-your-key-here": true, "api-key": true, "xxx": true,
}

func parallelNormalizeKey(v any) string {
	s, _ := v.(string)
	return jsTrim(s)
}

func parallelIsPlaceholder(key string) bool {
	k := jsTrim(key)
	return jsLen(k) < parallelMinKeyLength || parallelPlaceholders[strings.ToLower(k)]
}

func parallelIsSourceRef(k string) bool {
	return strings.HasPrefix(k, "$") || strings.HasPrefix(k, "!")
}

// resolveParallelAPIKey is resolveParallelApiKey: an environment or configured key that is not a
// placeholder, or a `$`/`!` credential source resolved now. "" means none.
func resolveParallelAPIKey(ctx context.Context) (string, error) {
	cfg := parallelNormalizeKey(configAny("parallelApiKey"))
	if parallelIsSourceRef(cfg) {
		resolved, err := resolveProviderCredential(ctx, "Parallel", "parallelApiKey", "PARALLEL_API_KEY")
		if err != nil {
			return "", err
		}
		if resolved != "" && !parallelIsPlaceholder(resolved) {
			return resolved, nil
		}
		return "", nil
	}
	if env := parallelNormalizeKey(envOrNil("PARALLEL_API_KEY")); env != "" && !parallelIsPlaceholder(env) {
		return env, nil
	}
	if cfg != "" && !parallelIsPlaceholder(cfg) {
		return cfg, nil
	}
	return "", nil
}

// IsParallelAvailable is hasParallelApiKey.
func IsParallelAvailable() bool {
	cfg := parallelNormalizeKey(configAny("parallelApiKey"))
	if parallelIsSourceRef(cfg) {
		return hasProviderCredential("Parallel", "parallelApiKey", "PARALLEL_API_KEY")
	}
	env := parallelNormalizeKey(envOrNil("PARALLEL_API_KEY"))
	return env != "" && !parallelIsPlaceholder(env) || cfg != "" && !parallelIsPlaceholder(cfg)
}

func parallelKey(ctx context.Context) (string, error) {
	key, err := resolveParallelAPIKey(ctx)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", missingKey("Parallel", "parallelApiKey", "PARALLEL_API_KEY", "Get a key at https://platform.parallel.ai")
	}
	return key, nil
}

func parallelAfterDate(filter string) string {
	days := map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}[filter]
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02")
}

// parallelCount is Math.max(1, Math.min(Math.floor(numResults ?? 5), 20)); NaN stays NaN.
func parallelCount(v *float64) float64 {
	if v == nil {
		return 5
	}
	return math.Max(1, math.Min(math.Floor(*v), 20))
}

func parallelBody(query string, o SearchOptions) map[string]any {
	n := parallelCount(o.NumResults)
	policy := map[string]any{}
	f := normalizeDomainFilters(o.DomainFilter)
	if len(f.allowed) > 0 {
		policy["include_domains"] = f.allowed
	}
	if len(f.blocked) > 0 {
		policy["exclude_domains"] = f.blocked
	}
	if o.RecencyFilter != "" {
		policy["after_date"] = parallelAfterDate(o.RecencyFilter)
	}
	adv := map[string]any{"max_results": nil}
	if !math.IsNaN(n) {
		adv["max_results"] = int(n)
	}
	if len(policy) > 0 {
		adv["source_policy"] = policy
	}
	return map[string]any{"objective": query, "search_queries": []string{query}, "advanced_settings": adv}
}

func parallelExcerpts(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok && jsTrim(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func parallelTitle(item map[string]any, i int) string {
	if t, _ := strField(item, "title"); t != "" {
		return t
	}
	return fmt.Sprintf("Source %d", i+1)
}

// parallelCall is parallelFetch: one POST with x-api-key.
func parallelCall(ctx context.Context, rawURL string, body map[string]any) (map[string]any, error) {
	key, err := parallelKey(ctx)
	if err != nil {
		return nil, err
	}
	status, text, err := hostedSend(ctx, hostedCall{URL: rawURL, Body: jsonBytes(body), Key: key, Timeout: parallelSearchTimeout,
		Header: http.Header{"X-Api-Key": {key}, "Content-Type": {"application/json"}}, Credentials: []string{"X-Api-Key"}})
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("Parallel API error %d: %s", status, jsSlice(RedactCredential(text, key), 0, 300))
	}
	data, err := parseJSONObjectLoose(text)
	if err != nil {
		return nil, fmt.Errorf("Parallel API returned invalid JSON: %v", err)
	}
	return data, nil
}

// SearchWithParallel queries the Parallel search API.
func SearchWithParallel(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	data, err := parallelCall(ctx, parallelSearchURL, parallelBody(query, o))
	if err != nil {
		return nil, err
	}
	items, _ := data["results"].([]any)
	resp := &SearchResponse{Results: []SearchResult{}}
	var parts []string
	var inline []ExtractedContent
	for i, it := range items {
		item := asMap(it)
		u, _ := strField(item, "url")
		if u == "" {
			continue
		}
		ex := parallelExcerpts(item["excerpts"])
		snippet := ""
		if len(ex) > 0 {
			snippet = jsSlice(collapseWS(ex[0]), 0, 200)
		}
		resp.Results = append(resp.Results, SearchResult{Title: parallelTitle(item, i), URL: u, Snippet: snippet})
		if len(ex) > 0 {
			parts = append(parts, strings.Join(ex, " ")+"\nSource: "+parallelTitle(item, i)+" ("+u+")")
			t, _ := strField(item, "title")
			inline = append(inline, ExtractedContent{URL: u, Title: t, Content: strings.Join(ex, "\n\n")})
		}
	}
	resp.Answer = strings.Join(parts, "\n\n")
	if o.IncludeContent && len(inline) > 0 {
		resp.InlineContent = inline
	}
	return resp, nil
}

func parallelContent(r map[string]any) string {
	if s, ok := strField(r, "full_content"); ok && jsTrim(s) != "" {
		return jsTrim(s)
	}
	return strings.Join(parallelExcerpts(r["excerpts"]), "\n\n")
}

func parallelExtractBody(rawURL string, o ExtractOptions, full bool) map[string]any {
	body := map[string]any{"urls": []string{rawURL}}
	if p := jsTrim(o.Prompt); p != "" {
		body["objective"] = p
	}
	if full {
		body["advanced_settings"] = map[string]any{"full_content": true}
	}
	return body
}

func parallelExtractOnce(ctx context.Context, rawURL string, body map[string]any) (*ExtractedContent, map[string]any, error) {
	data, err := parallelCall(ctx, parallelExtractURL, body)
	if err != nil {
		return nil, nil, err
	}
	if errs, ok := data["errors"].([]any); ok {
		for _, e := range errs {
			if s, ok := e.(string); ok && s == rawURL {
				return nil, nil, nil
			}
			if u, _ := strField(asMap(e), "url"); u == rawURL {
				return nil, nil, nil
			}
		}
	}
	var result map[string]any
	results, _ := data["results"].([]any)
	for _, r := range results {
		if u, _ := strField(asMap(r), "url"); u == rawURL {
			result = asMap(r)
			break
		}
	}
	if result == nil && len(results) > 0 {
		result = asMap(results[0])
	}
	if result == nil {
		return nil, nil, nil
	}
	u, _ := strField(result, "url")
	content := parallelContent(result)
	if u == "" || jsLen(content) < parallelMinUseful {
		return nil, result, nil
	}
	t, _ := strField(result, "title")
	return &ExtractedContent{URL: u, Title: jsTrim(t), Content: content}, result, nil
}

// ExtractWithParallel fetches one URL through Parallel Extract, retrying for the full content
// when the excerpts are too short.
func ExtractWithParallel(ctx context.Context, rawURL string, o ExtractOptions) (*ExtractedContent, error) {
	first, result, err := parallelExtractOnce(ctx, rawURL, parallelExtractBody(rawURL, o, false))
	if err != nil || first != nil {
		return first, err
	}
	if result == nil || jsLen(parallelContent(result)) >= parallelMinUseful {
		return nil, nil
	}
	retry, _, err := parallelExtractOnce(ctx, rawURL, parallelExtractBody(rawURL, o, true))
	return retry, err
}
