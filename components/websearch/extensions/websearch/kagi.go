package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Port of kagi.ts: search and Kagi Extract.

const (
	kagiSearchURL     = "https://kagi.com/api/v1/search"
	kagiExtractURL    = "https://kagi.com/api/v1/extract"
	kagiSearchTimeout = 60 * time.Second
)

// IsKagiAvailable reports whether a Kagi credential source is configured.
func IsKagiAvailable() bool { return hasProviderCredential("Kagi", "kagiApiKey", "KAGI_API_KEY") }

func kagiInvalidResponse(msg string) error {
	return fmt.Errorf("Kagi API returned invalid response: %s", msg)
}

func firstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && jsTrim(s) != "" {
			return jsTrim(s)
		}
	}
	return ""
}

func kagiAppend(value any, results *[]SearchResult, inline *[]ExtractedContent) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			kagiAppend(item, results, inline)
		}
	case map[string]any:
		u := firstString(v["url"], v["href"], v["link"])
		if u == "" {
			return
		}
		title := firstString(v["title"], v["name"])
		if title == "" {
			title = u
		}
		snippet := firstString(v["snippet"], v["description"], v["summary"], v["content"], v["markdown"], v["text"])
		*results = append(*results, SearchResult{Title: title, URL: u, Snippet: snippet})
		if content := firstString(v["markdown"], v["content"], v["text"]); content != "" {
			*inline = append(*inline, ExtractedContent{URL: u, Title: title, Content: content})
		}
	}
}

func kagiErrors(value any) string {
	env, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	raw, ok := env["errors"]
	if !ok || raw == nil {
		raw = env["error"]
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	msgs := make([]string, len(list))
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			msgs[i] = jsString(e)
			continue
		}
		if s := firstString(m["message"], m["msg"], m["code"]); s != "" {
			msgs[i] = s
		} else {
			b, _ := json.Marshal(m)
			msgs[i] = string(b)
		}
	}
	return strings.Join(msgs, "; ")
}

// SearchWithKagi queries the Kagi search API.
func SearchWithKagi(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := resolveProviderCredential(ctx, "Kagi", "kagiApiKey", "KAGI_API_KEY")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Kagi API key not found. Either:\n  1. Create %s with { \"kagiApiKey\": \"your-key\" }\n  2. Set KAGI_API_KEY environment variable\nCreate a key at https://kagi.com/settings?p=api", ConfigPath())
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	payload, _ := json.Marshal(map[string]any{"query": query, "limit": numResults})
	tctx, cancel := withTimeout(ctx, kagiSearchTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, kagiSearchURL, RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}, "Accept": {"application/json"}}}, []string{"Authorization"})
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Kagi API error %d: %s", resp.StatusCode, slice300(RedactCredential(body, apiKey)))
	}
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("Kagi API returned invalid JSON: %v", err)
	}
	env, ok := raw.(map[string]any)
	if !ok {
		return nil, kagiInvalidResponse("expected an object envelope")
	}
	if msg := kagiErrors(env); msg != "" {
		return nil, kagiInvalidResponse(msg)
	}
	items := env["data"]
	if obj, ok := items.(map[string]any); ok {
		items = obj["search"]
	}
	var results []SearchResult
	var inline []ExtractedContent
	kagiAppend(items, &results, &inline)
	if len(results) > numResults {
		results = results[:numResults]
	}
	out := &SearchResponse{Answer: sourceLines(results), Results: results}
	if results == nil {
		out.Results = []SearchResult{}
	}
	if o.IncludeContent {
		urls := map[string]bool{}
		for _, r := range results {
			urls[r.URL] = true
		}
		for _, c := range inline {
			if urls[c.URL] {
				out.InlineContent = append(out.InlineContent, c)
			}
		}
	}
	return out, nil
}

// IsKagiExtractAvailable is isKagiExtractAvailable.
func IsKagiExtractAvailable() bool { return IsKagiAvailable() }

func kagiRequireKey(ctx context.Context) (string, error) {
	return requireProviderKey(ctx, "Kagi API", "Kagi", "kagiApiKey", "KAGI_API_KEY", "Create a key at https://kagi.com/settings?p=api")
}

// ExtractWithKagi fetches one URL through Kagi Extract, after the SSRF guard (kagi.ts
// extractWithKagi).
func ExtractWithKagi(ctx context.Context, rawURL string, o ExtractOptions, ssrf SsrfConfig) (*ExtractedContent, error) {
	if err := validateTarget(ctx, rawURL, ssrf, o.Lookup, true); err != nil {
		return nil, err
	}
	key, err := kagiRequireKey(ctx)
	if err != nil {
		return nil, err
	}
	resp, tctx, cancel, err := guardedAPI(ctx, kagiExtractURL, RequestInit{Method: "POST", Body: jsonBytes(map[string]any{"pages": []any{map[string]any{"url": rawURL}}}),
		Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}, "Accept": {"application/json"}}},
		timeoutMsOr(o.TimeoutMs, kagiSearchTimeout), FetchRemoteOptions{ValidationOptions: ssrfValidation(ssrf, o.Lookup, false), OnRedirect: redirectKeepsOnlyJSON(true)})
	defer cancel()
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), key)
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), key)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Kagi Extract API error %d: %s", resp.StatusCode, slice300(RedactCredential(body, key)))
	}
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("Kagi Extract API returned invalid JSON: %v", err)
	}
	env, ok := raw.(map[string]any)
	if !ok {
		return nil, kagiInvalidResponse("expected extract object envelope")
	}
	if msg := kagiErrors(env); msg != "" {
		return nil, kagiInvalidResponse(msg)
	}
	candidates, isList := env["data"].([]any)
	if !isList {
		if d, ok := env["data"]; ok && d != nil {
			candidates = []any{d}
		} else {
			candidates = []any{env}
		}
	}
	for _, c := range candidates {
		item, ok := c.(map[string]any)
		if !ok {
			continue
		}
		content := firstString(item["markdown"], item["content"], item["text"])
		if content == "" {
			continue
		}
		u := firstString(item["url"], item["href"], item["link"])
		if u == "" {
			u = rawURL
		}
		title := firstString(item["title"], item["name"])
		if title == "" {
			title = rawURL
		}
		return &ExtractedContent{URL: u, Title: title, Content: content}, nil
	}
	return nil, nil
}
