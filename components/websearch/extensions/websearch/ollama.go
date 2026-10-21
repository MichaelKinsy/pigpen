package websearch

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"
)

// Port of ollama.ts: Ollama Cloud web search and Web Fetch.

const (
	ollamaSearchURL     = "https://ollama.com/api/web_search"
	ollamaFetchURL      = "https://ollama.com/api/web_fetch"
	ollamaSearchTimeout = 60 * time.Second
	ollamaGetKeyAt      = "Create a key at https://ollama.com/settings/keys"
)

// IsOllamaAvailable reports whether an Ollama credential source is configured.
func IsOllamaAvailable() bool {
	return hasProviderCredential("Ollama", "ollamaApiKey", "OLLAMA_API_KEY")
}

func ollamaInvalid(msg string) error {
	return fmt.Errorf("Ollama API returned invalid response: %s", msg)
}

// ollamaCount is normalizeCount: 1..10, default 5.
func ollamaCount(v *float64) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return 5
	}
	return int(math.Max(1, math.Min(math.Floor(*v), 10)))
}

// SearchWithOllama queries the Ollama web_search endpoint.
func SearchWithOllama(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	key, err := requireHostedKey(ctx, "Ollama API", "Ollama", "ollamaApiKey", "OLLAMA_API_KEY", ollamaGetKeyAt)
	if err != nil {
		return nil, err
	}
	n := ollamaCount(o.NumResults)
	act := logAPIStart(query)
	status, text, err := hostedSend(ctx, hostedCall{URL: ollamaSearchURL, Body: jsonBytes(map[string]any{"query": query, "max_results": n}), Key: key, Timeout: ollamaSearchTimeout,
		Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}}, Credentials: []string{"Authorization"}, Act: act})
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		act.complete(status)
		return nil, fmt.Errorf("Ollama API error %d: %s", status, slice300(RedactCredential(text, key)))
	}
	raw, err := decodeAny(text)
	if err != nil {
		act.complete(status)
		return nil, fmt.Errorf("Ollama API returned invalid JSON: %v", err)
	}
	env := asMap(raw)
	if env == nil {
		return nil, ollamaInvalid("expected an object envelope")
	}
	list, ok := env["results"].([]any)
	if !ok {
		return nil, ollamaInvalid("expected results array")
	}
	type item struct{ title, url, content string }
	var items []item
	for i, v := range list {
		m := asMap(v)
		if m == nil {
			return nil, ollamaInvalid(fmt.Sprintf("expected results[%d] object", i))
		}
		title, ok := strField(m, "title")
		if !ok {
			return nil, ollamaInvalid(fmt.Sprintf("expected results[%d].title string", i))
		}
		u, ok := strField(m, "url")
		if !ok || u == "" {
			return nil, ollamaInvalid(fmt.Sprintf("expected results[%d].url non-empty string", i))
		}
		content, ok := strField(m, "content")
		if !ok {
			return nil, ollamaInvalid(fmt.Sprintf("expected results[%d].content string", i))
		}
		items = append(items, item{title, u, content})
	}
	act.complete(status)
	if len(items) > n {
		items = items[:n]
	}
	results := make([]SearchResult, len(items))
	for i, it := range items {
		results[i] = SearchResult{Title: it.title, URL: it.url, Snippet: it.content}
	}
	resp := &SearchResponse{Answer: sourceLines(results), Results: results}
	if o.IncludeContent {
		for _, it := range items {
			if jsTrim(it.content) != "" {
				resp.InlineContent = append(resp.InlineContent, ExtractedContent{URL: it.url, Title: it.title, Content: it.content})
			}
		}
	}
	return resp, nil
}

// IsOllamaFetchAvailable is isOllamaFetchAvailable.
func IsOllamaFetchAvailable() bool { return IsOllamaAvailable() }

// ExtractWithOllama fetches one URL through Ollama Web Fetch, after the SSRF guard.
func ExtractWithOllama(ctx context.Context, rawURL string, o ExtractOptions, ssrf SsrfConfig) (*ExtractedContent, error) {
	if err := validateTarget(ctx, rawURL, ssrf, o.Lookup, true); err != nil {
		return nil, err
	}
	key, err := requireHostedKey(ctx, "Ollama API", "Ollama", "ollamaApiKey", "OLLAMA_API_KEY", ollamaGetKeyAt)
	if err != nil {
		return nil, err
	}
	act := logAPIStart("ollama fetch: " + rawURL)
	resp, tctx, cancel, err := guardedAPI(ctx, ollamaFetchURL, RequestInit{Method: "POST", Body: jsonBytes(map[string]any{"url": rawURL}),
		Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}}},
		timeoutMsOr(o.TimeoutMs, ollamaSearchTimeout), FetchRemoteOptions{ValidationOptions: ssrfValidation(ssrf, o.Lookup, false), OnRedirect: redirectKeepsOnlyJSON(false)})
	defer cancel()
	if err != nil {
		return nil, act.failedErr(redactErr(timeoutFix(ctx, tctx, err), key))
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), key)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		act.complete(resp.StatusCode)
		return nil, fmt.Errorf("Ollama Web Fetch error %d: %s", resp.StatusCode, slice300(RedactCredential(text, key)))
	}
	raw, err := decodeAny(text)
	if err != nil {
		act.complete(resp.StatusCode)
		return nil, fmt.Errorf("Ollama Web Fetch returned invalid JSON: %v", err)
	}
	env := asMap(raw)
	if env == nil {
		return nil, ollamaInvalid("expected fetch object envelope")
	}
	title, ok := strField(env, "title")
	if !ok {
		return nil, ollamaInvalid("expected title string")
	}
	content, ok := strField(env, "content")
	if !ok {
		return nil, ollamaInvalid("expected content string")
	}
	act.complete(resp.StatusCode)
	content = jsTrim(content)
	if content == "" {
		return nil, nil
	}
	return &ExtractedContent{URL: rawURL, Title: title, Content: content}, nil
}
