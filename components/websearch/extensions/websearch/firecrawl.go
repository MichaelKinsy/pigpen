package websearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Port of firecrawl.ts: a self-hosted Firecrawl instance (base URL required) for fetch_content and
// web_search. The API base may be loopback; every other hop passes the SSRF guard.

const (
	firecrawlDefaultVersion = "v2"
	firecrawlTimeout        = 60 * time.Second
)

func firecrawlBase() (string, bool, error) {
	if base, ok, err := normalizeConfiguredBase(envOrNil("FIRECRAWL_BASE_URL"), "Firecrawl"); err != nil || ok {
		return base, ok, err
	}
	return normalizeConfiguredBase(configAny("firecrawlBaseUrl"), "Firecrawl")
}

func firecrawlRequireBase() (string, error) {
	base, ok, err := firecrawlBase()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("Firecrawl base URL not configured. Either:\n  1. Set firecrawlBaseUrl in %s\n  2. Set FIRECRAWL_BASE_URL environment variable", ConfigPath())
	}
	return base, nil
}

// IsFirecrawlAvailable reports whether a base URL is configured; a malformed one counts as
// unavailable here and is reported by the request.
func IsFirecrawlAvailable() bool {
	_, ok, err := firecrawlBase()
	return err == nil && ok
}

// firecrawlConfigured is the availability the fetch routing uses: like the original, a malformed
// base URL surfaces as the provider's error instead of silently skipping it.
func firecrawlConfigured() (bool, error) {
	_, ok, err := firecrawlBase()
	return ok, err
}

func firecrawlVersion() (string, error) {
	raw := any(nil)
	if env := jsTrim(os.Getenv("FIRECRAWL_API_VERSION")); env != "" {
		raw = env
	} else {
		raw = configAny("firecrawlApiVersion")
	}
	if raw == nil {
		return firecrawlDefaultVersion, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf(`firecrawlApiVersion in %s must be a string ("v1" or "v2")`, ConfigPath())
	}
	n := strings.ToLower(jsTrim(s))
	if n == "" {
		return firecrawlDefaultVersion, nil
	}
	if n != "v1" && n != "v2" {
		return "", fmt.Errorf(`Unsupported Firecrawl API version "%s". Supported versions: v1, v2`, s)
	}
	return n, nil
}

func firecrawlFreshScrape() (bool, error) {
	if env, ok := os.LookupEnv("FIRECRAWL_FRESH_SCRAPE"); ok {
		return env == "1" || strings.EqualFold(env, "true"), nil
	}
	v := configAny("firecrawlFreshScrape")
	if v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("firecrawlFreshScrape in %s must be a boolean", ConfigPath())
	}
	return b, nil
}

func firecrawlSSRF(o *SsrfConfig) (SsrfConfig, error) {
	if o != nil {
		return *o, nil
	}
	return LoadSsrfConfig()
}

// firecrawlCall is firecrawlFetch.
func firecrawlCall(ctx context.Context, endpoint string, body map[string]any, timeout time.Duration, ssrf SsrfConfig, lookup Lookup, label string) (map[string]any, error) {
	base, err := firecrawlRequireBase()
	if err != nil {
		return nil, err
	}
	version, err := firecrawlVersion()
	if err != nil {
		return nil, err
	}
	key, err := resolveProviderCredential(ctx, "Firecrawl", "firecrawlApiKey", "FIRECRAWL_API_KEY")
	if err != nil {
		return nil, err
	}
	h := jsonHeader()
	if key != "" {
		h.Set("Authorization", "Bearer "+key)
	}
	requestURL := base + "/" + version + "/" + endpoint
	tctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	redact := func(s string) string {
		if key == "" {
			return s
		}
		return RedactCredential(s, key)
	}
	resp, err := fetchAPIManual(tctx, requestURL, RequestInit{Method: "POST", Header: h, Body: jsonBytes(body)}, ssrfValidation(ssrf, lookup, isLoopbackHost(requestURL)))
	if err != nil {
		return nil, timeoutFix(ctx, tctx, err)
	}
	text, rerr := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if rerr != nil {
			text = ""
		}
		return nil, fmt.Errorf("Firecrawl %s error %d: %s", label, resp.StatusCode, redact(jsSlice(text, 0, 300)))
	}
	if rerr != nil {
		return nil, timeoutFix(ctx, tctx, rerr)
	}
	data, err := decodeAny(text)
	if err != nil {
		return nil, fmt.Errorf("Firecrawl %s returned invalid JSON: %v", label, err)
	}
	env := asMap(data)
	if env == nil {
		return nil, fmt.Errorf("Firecrawl %s returned an unexpected response shape", label)
	}
	switch env["success"] {
	case false:
		reason := "unknown error"
		if s, ok := strField(env, "error"); ok && jsTrim(s) != "" {
			reason = s
		}
		return nil, fmt.Errorf("Firecrawl %s unsuccessful: %s", label, redact(reason))
	case true:
		return env, nil
	}
	return nil, fmt.Errorf("Firecrawl %s returned an unexpected response shape", label)
}

func firecrawlScrapeOptions(fresh bool) map[string]any {
	o := map[string]any{"formats": []string{"markdown"}, "onlyMainContent": true}
	if !fresh {
		o["lockdown"] = true
	}
	return o
}

var firecrawlRecency = map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}

// SearchWithFirecrawl searches through the configured Firecrawl instance.
func SearchWithFirecrawl(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if _, err := firecrawlRequireBase(); err != nil {
		return nil, err
	}
	n := NormalizeSearchResultCount(o.NumResults)
	f := normalizeDomainFilters(o.DomainFilter)
	body := map[string]any{"query": query, "limit": n, "sources": []string{"web"}}
	if len(f.allowed) > 0 {
		body["includeDomains"] = f.allowed
	} else if len(f.blocked) > 0 {
		body["excludeDomains"] = f.blocked
	}
	if t := firecrawlRecency[o.RecencyFilter]; t != "" {
		body["tbs"] = t
	}
	if o.IncludeContent {
		fresh, err := firecrawlFreshScrape()
		if err != nil {
			return nil, err
		}
		body["scrapeOptions"] = firecrawlScrapeOptions(fresh)
	}
	ssrf, err := LoadSsrfConfig()
	if err != nil {
		return nil, err
	}
	env, err := firecrawlCall(ctx, "search", body, timeoutMsOr(o.TimeoutMs, firecrawlTimeout), ssrf, o.Lookup, "search")
	if err != nil {
		return nil, err
	}
	web, ok := env["data"].([]any)
	if !ok {
		web, ok = asMap(env["data"])["web"].([]any)
	}
	if !ok {
		return nil, errors.New("Firecrawl search returned an unexpected web result shape")
	}
	resp := &SearchResponse{Results: []SearchResult{}}
	var inline []ExtractedContent
	seen := map[string]bool{}
	for _, raw := range web {
		item := asMap(raw)
		if item == nil {
			continue
		}
		meta := asMap(item["metadata"])
		u := firstString(item["url"], meta["sourceURL"], meta["url"])
		if u == "" || seen[u] || !f.matches(u) {
			continue
		}
		seen[u] = true
		title := firstString(item["title"], meta["title"])
		if title == "" {
			title = u
		}
		snippet := firstString(item["description"], item["snippet"], meta["description"])
		resp.Results = append(resp.Results, SearchResult{Title: title, URL: u, Snippet: snippet})
		if md := firstString(item["markdown"]); md != "" {
			inline = append(inline, ExtractedContent{URL: u, Title: title, Content: md})
		}
		if len(resp.Results) >= n {
			break
		}
	}
	resp.Answer = sourceLines(resp.Results)
	if o.IncludeContent && len(inline) > 0 {
		resp.InlineContent = inline
	}
	return resp, nil
}

// ExtractWithFirecrawl fetches one URL through the configured Firecrawl instance.
func ExtractWithFirecrawl(ctx context.Context, rawURL string, o ExtractOptions, ssrfOpt *SsrfConfig) (*ExtractedContent, error) {
	if _, err := firecrawlRequireBase(); err != nil {
		return nil, err
	}
	ssrf, err := firecrawlSSRF(ssrfOpt)
	if err != nil {
		return nil, err
	}
	if err := validateTarget(ctx, rawURL, ssrf, o.Lookup, false); err != nil {
		return nil, err
	}
	fresh, err := firecrawlFreshScrape()
	if err != nil {
		return nil, err
	}
	body := map[string]any{"url": rawURL, "formats": []string{"markdown"}, "onlyMainContent": true}
	if !fresh {
		body["lockdown"] = true
	}
	env, err := firecrawlCall(ctx, "scrape", body, timeoutMsOr(o.TimeoutMs, firecrawlTimeout), ssrf, o.Lookup, "scrape")
	if err != nil {
		return nil, err
	}
	data := asMap(env["data"])
	if data == nil {
		return nil, errors.New("Firecrawl scrape returned an unexpected data shape")
	}
	md, ok := strField(data, "markdown")
	if !ok {
		return nil, errors.New("Firecrawl scrape returned markdown in an unexpected shape")
	}
	content := jsTrim(md)
	if content == "" {
		return nil, nil
	}
	title := ""
	if t, ok := strField(asMap(data["metadata"]), "title"); ok && jsTrim(t) != "" {
		title = jsTrim(t)
	} else if t, ok := strField(data, "title"); ok {
		title = jsTrim(t)
	}
	return &ExtractedContent{URL: rawURL, Title: title, Content: content}, nil
}
