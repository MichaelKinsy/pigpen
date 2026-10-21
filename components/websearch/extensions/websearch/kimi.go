package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Port of kimi-search.ts: Kimi Code's search endpoint, authenticated with the login of a Kimi
// Code Plan account that the model registry holds. There is no API-key path.

const (
	kimiSearchURL     = "https://api.kimi.com/coding/v1/search"
	kimiSearchTimeout = 30 * time.Second
)

// kimiProviders are the registry providers that carry the login, primary first.
var kimiProviders = []string{"kimi-coding", "kimi-code"}

type kimiAuth struct {
	apiKey  string
	headers providerHeaders
}

// kimiBearerToken finds a bearer token in an Authorization header (kimi-search.ts:42-49).
func kimiBearerToken(h providerHeaders) string {
	for _, name := range h.sortedNames() {
		v := h[name]
		if strings.ToLower(name) != "authorization" || v == nil {
			continue
		}
		if m := bearerTokenRE.FindStringSubmatch(jsTrim(*v)); m != nil && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// resolveKimiAuth returns the first usable registry credential of the Kimi providers. A
// credential failure is kept and re-raised only when no later model resolves.
func resolveKimiAuth(ec ExtensionContext) (*kimiAuth, error) {
	if ec == nil {
		return nil, nil
	}
	reg := ec.ModelRegistry()
	models, err := reg.GetAll()
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, provider := range kimiProviders {
		for _, model := range models {
			if p, _ := mpString(model["provider"]); p != provider {
				continue
			}
			resolved, err := registryAuthFor(reg, model)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if !resolved.OK {
				continue
			}
			apiKey := resolved.APIKey
			if apiKey == "" {
				apiKey = kimiBearerToken(resolved.Headers)
			}
			if apiKey != "" {
				return &kimiAuth{apiKey: apiKey, headers: resolved.Headers}, nil
			}
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, nil
}

// IsKimiSearchAvailable is isKimiSearchAvailable: any failure is "unavailable".
func IsKimiSearchAvailable(ec ExtensionContext) bool {
	auth, err := resolveKimiAuth(ec)
	return err == nil && auth != nil
}

func kimiRequestHeaders(auth *kimiAuth) *jsHeaders {
	h := newJSHeaders()
	for _, name := range auth.headers.sortedNames() {
		if v := auth.headers[name]; v != nil {
			h.set(name, *v)
		}
	}
	h.set("Authorization", "Bearer "+auth.apiKey)
	h.set("Content-Type", "application/json")
	h.set("X-Msh-Tool-Call-Id", uuidV4())
	return h
}

// normalizeKimiResultURL is normalizeResultUrl: trimmed, absolute http(s), in href form.
func normalizeKimiResultURL(v any) (string, bool) {
	s, ok := mpString(v)
	if !ok {
		return "", false
	}
	input := jsTrim(s)
	if input == "" {
		return "", false
	}
	u, err := ParseURL(input, nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.String(), true
}

func parseKimiResults(value any, o SearchOptions) ([]SearchResult, error) {
	m, _ := mpObject(value)
	items, ok := mpArray(m["search_results"])
	if !ok {
		return nil, errors.New("Kimi Code search API returned an invalid response: search_results must be an array")
	}
	filters := normalizeDomainFilters(o.DomainFilter)
	numResults := NormalizeSearchResultCount(o.NumResults)
	results := []SearchResult{}
	for _, it := range items {
		record, ok := mpObject(it)
		if !ok {
			continue
		}
		u, ok := normalizeKimiResultURL(record["url"])
		if !ok || !filters.matches(u) {
			continue
		}
		title := u
		if s, ok := mpString(record["title"]); ok && jsTrim(s) != "" {
			title = s
		}
		snippet, _ := mpString(record["snippet"])
		results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	return results, nil
}

// SearchWithKimi is searchWithKimi.
func SearchWithKimi(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	auth, err := resolveKimiAuth(ExtensionContextFrom(ctx))
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("Kimi Code web search unavailable. Run /login kimi-coding to authenticate a Kimi Code Plan account.")
	}
	payload, err := encodeJSONBody(map[string]string{"text_query": query})
	if err != nil {
		return nil, err
	}
	act := logAPIStart(query)
	status := 0
	tctx, cancel := withTimeout(ctx, kimiSearchTimeout)
	defer cancel()
	result, err := func() (*SearchResponse, error) {
		resp, err := FetchWithCredentialRedirects(tctx, kimiSearchURL, RequestInit{Method: "POST", Header: kimiRequestHeaders(auth).http(), Body: payload}, plainFetchCredentialHeaders)
		if err != nil {
			return nil, err
		}
		status = resp.StatusCode
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			act.logError(fmt.Sprintf("HTTP %d", resp.StatusCode))
		}
		text, err := readBody(resp)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("Kimi Code search API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, auth.apiKey)))
		}
		parsed, err := decodeResponseJSON(text)
		if err != nil {
			return nil, fmt.Errorf("Kimi Code search API returned invalid JSON: %s", err.Error())
		}
		results, err := parseKimiResults(parsed, o)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, errors.New("Kimi Code search API returned no results")
		}
		return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
	}()
	if err != nil {
		// The catch block of kimi-search.ts:191-210: the caller's abort, the provider's own timeout, then the message.
		if ctx.Err() != nil {
			act.complete(0)
		} else if tctx.Err() == context.DeadlineExceeded || isTimeout(err) {
			act.logError("Kimi Code search API timed out")
			return nil, errors.New("Kimi Code search API timed out")
		}
		out := redactErr(err, auth.apiKey)
		if ctx.Err() == nil {
			act.failed(out.Error())
		}
		return nil, out
	}
	act.complete(status)
	return result, nil
}
