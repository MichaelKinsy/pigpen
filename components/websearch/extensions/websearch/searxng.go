package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Port of searxng.ts.

const searxngTimeout = 30 * time.Second

// searxngBaseURL is getBaseUrl(): the environment variable wins when it is set (even empty, which
// is invalid), else the configured searxngBaseUrl.
func searxngBaseURL() string {
	if v, ok := os.LookupEnv("SEARXNG_BASE_URL"); ok {
		return normalizeSearxngBase(v)
	}
	s, _ := configAny("searxngBaseUrl").(string)
	return normalizeSearxngBase(s)
}

// normalizeSearxngBase is normalizeBaseUrl (searxng.ts:52): an http(s) URL without credentials,
// its query and fragment dropped and trailing slashes removed; "" when it is not one.
func normalizeSearxngBase(value string) string {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return ""
	}
	u, err := ParseURL(trimmed, nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

// IsSearXNGAvailable is isSearXNGAvailable (searxng.ts:180): a valid base URL, and a readable
// ssrf configuration (a malformed one is an error, not "unavailable", so hosted providers never
// run in its place).
func IsSearXNGAvailable() (bool, error) {
	if searxngBaseURL() == "" {
		return false, nil
	}
	if _, err := LoadSsrfConfig(); err != nil {
		return false, err
	}
	return true, nil
}

var headerTokenRE = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// validHeaderValue is "new Headers({x: value}) does not throw".
func validHeaderValue(v string) bool {
	for _, r := range v {
		if r == 0 || r == '\n' || r == '\r' || r > 0xFF {
			return false
		}
	}
	return true
}

type headerPair struct{ name, value string }

// searxngConfiguredHeaders is getConfiguredHeaders(): string values with a token name and a valid
// value, in file order (a later entry for the same name, in any case, replaces an earlier one).
func searxngConfiguredHeaders() []headerPair {
	obj, ok := configAny("searxngHeaders").(map[string]any)
	if !ok {
		return nil
	}
	var out []headerPair
	for _, key := range configObjectKeyOrder("searxngHeaders") {
		s, ok := obj[key].(string)
		if !ok {
			continue
		}
		name := jsTrim(key)
		if name == "" || !headerTokenRE.MatchString(name) || !validHeaderValue(s) {
			continue
		}
		out = append(out, headerPair{name, s})
	}
	return out
}

// configObjectKeyOrder lists the keys of the top-level object `field` of web-search.json in file
// order (the decoded map has none).
func configObjectKeyOrder(field string) []string {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil
		}
		if keyTok != field {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return nil
			}
			continue
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil
		}
		var keys []string
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil
			}
			keys = append(keys, k.(string))
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return nil
			}
		}
		return keys
	}
	return nil
}

// mergeSearxngHeaders is mergeDefaultHeaders: Accept first, replaced (any case) by a configured one.
func mergeSearxngHeaders(configured []headerPair) http.Header {
	pairs := []headerPair{{"Accept", "application/json"}}
	for _, c := range configured {
		kept := pairs[:0:0]
		for _, e := range pairs {
			if !strings.EqualFold(e.name, c.name) {
				kept = append(kept, e)
			}
		}
		pairs = append(kept, c)
	}
	h := http.Header{}
	for _, p := range pairs {
		h[p.name] = []string{p.value}
	}
	return h
}

func searxngQuery(query string, f domainFilters) string {
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
		parts = append(parts, "-site:"+d)
	}
	return strings.Join(parts, " ")
}

// SearchWithSearXNG queries a SearXNG instance (searxng.ts:187). The request goes through the
// SSRF guard; configured headers are dropped when a redirect leaves the origin.
func SearchWithSearXNG(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if err := restConfigErr(); err != nil {
		return nil, err
	}
	base := searxngBaseURL()
	if base == "" {
		return nil, fmt.Errorf("SearXNG base URL is invalid or missing. Either:\n  1. Create %s with { \"searxngBaseUrl\": \"https://search.example.com\" }\n  2. Set SEARXNG_BASE_URL to an HTTP(S) URL", ConfigPath())
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	rawURL := base + "/search?q=" + formEscape(searxngQuery(query, filters)) + "&format=json"
	switch o.RecencyFilter {
	case "day", "week", "month", "year":
		rawURL += "&time_range=" + o.RecencyFilter
	}
	act := logAPIStart(searxngQuery(query, filters))
	ssrf, err := LoadSsrfConfig()
	if err != nil {
		return nil, act.failedErr(err)
	}
	tctx, cancel := withTimeout(ctx, restTimeout(searxngTimeout))
	defer cancel()
	resp, err := FetchRemoteURL(tctx, rawURL, RequestInit{Method: "GET", Header: mergeSearxngHeaders(searxngConfiguredHeaders())}, FetchRemoteOptions{
		ValidationOptions: ValidationOptions{AllowRanges: ssrf.AllowRanges, TrustEnvProxy: ssrf.TrustEnvProxy},
		Fetch:             currentPageFetch(),
		OnRedirect: func(a RedirectArgs) RequestInit {
			if Origin(a.From) == Origin(a.To) {
				return a.Init
			}
			a.Init.Header = http.Header{"Accept": {"application/json"}}
			return a.Init
		},
	})
	if err != nil {
		return nil, act.failedErr(timeoutFix(ctx, tctx, transportFailure(err, false)))
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, act.failedErr(timeoutFix(ctx, tctx, transportFailure(err, true)))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		act.logError(fmt.Sprintf("HTTP %d", resp.StatusCode))
		return nil, act.failedErr(fmt.Errorf("SearXNG search error %d: %s", resp.StatusCode, slice300(text)))
	}
	data, err := decodeJSON(text)
	if err != nil {
		return nil, act.failedErr(fmt.Errorf("SearXNG returned invalid JSON: %v", err))
	}
	act.complete(resp.StatusCode)
	envelope, _ := jsonObject(data)
	results := []SearchResult{}
	items, _ := envelope["results"].([]any)
	for _, raw := range items {
		item, _ := jsonObject(raw)
		u, _ := item["url"].(string)
		if u == "" || !filters.matches(u) {
			continue
		}
		title, _ := item["title"].(string)
		if title == "" {
			title = u
		}
		snippet, _ := item["content"].(string)
		results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
		if len(results) >= numResults {
			break
		}
	}
	var parts []string
	answers, _ := envelope["answers"].([]any)
	for _, a := range answers {
		if s, ok := a.(string); ok && jsTrim(s) != "" {
			parts = append(parts, jsTrim(s))
		}
	}
	for _, r := range results {
		if r.Snippet != "" {
			parts = append(parts, r.Snippet+"\nSource: "+r.Title+" ("+r.URL+")")
		} else {
			parts = append(parts, "Source: "+r.Title+" ("+r.URL+")")
		}
	}
	return &SearchResponse{Answer: strings.Join(parts, "\n\n"), Results: results}, nil
}
