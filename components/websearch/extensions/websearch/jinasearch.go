package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Port of jina-search.ts (the s.jina.ai search API; the r.jina.ai Reader fetch fallback lives in
// extract.go).

const jinaSearchBaseURL = "https://s.jina.ai/"

// IsJinaSearchAvailable reports whether a Jina credential source is configured
// (jina-search.ts:177).
func IsJinaSearchAvailable() bool {
	return hasProviderCredential("Jina Search", "jinaApiKey", "JINA_API_KEY")
}

// jsEncodeURIComponent is encodeURIComponent.
func jsEncodeURIComponent(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// a lone surrogate throws URIError upstream; the replacement character is the closest
			// a Go string can hold
			r = '\uFFFD'
			size = 1
		}
		if r < utf8.RuneSelf && strings.IndexByte(unreserved, byte(r)) >= 0 {
			b.WriteByte(byte(r))
		} else {
			var enc [4]byte
			n := utf8.EncodeRune(enc[:], r)
			for _, c := range enc[:n] {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		i += size
	}
	return b.String()
}

// jinaRequestFailure is rethrowRequestError (jina-search.ts:139): Jina's own deadline is an HTTP
// 408 (transient), never a cancellation; a cancellation keeps the abort message; anything else has
// the credential removed.
func jinaRequestFailure(ctx, tctx context.Context, apiKey string, err error) error {
	if isProviderTimeout(ctx, tctx, err) {
		return errors.New("Jina Search API error 408: request timed out")
	}
	if ctx.Err() != nil {
		return &redactedError{"This operation was aborted", err}
	}
	return redactErr(transportFailure(err, false), apiKey)
}

type jinaItem struct{ title, url, snippet, content string }

// SearchWithJina queries the Jina search API (jina-search.ts:190).
func SearchWithJina(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "Jina Search", "Jina Search", "jinaApiKey", "JINA_API_KEY", "https://jina.ai/api-dashboard")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	filters := normalizeDomainFilters(o.DomainFilter)
	recency := ""
	if o.RecencyFilter != "" {
		recency = " published in the past " + o.RecencyFilter
	}
	exclusions := ""
	for _, d := range filters.blocked {
		exclusions += " -site:" + d
	}
	constrained := jsTrim(jsTrim(query) + exclusions + recency)
	rawURL := jinaSearchBaseURL + jsEncodeURIComponent(constrained) + "?count=" + strconv.Itoa(numResults)
	for _, d := range filters.allowed {
		rawURL += "&site=" + formEscape(d)
	}
	respond := "no-content"
	if o.IncludeContent {
		respond = "content"
	}
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, rawURL, RequestInit{Method: "GET", Header: http.Header{
		"Accept": {"application/json"}, "Authorization": {"Bearer " + apiKey}, "User-Agent": {"pi-web-access"},
		"X-Respond-With": {respond}, "X-Retain-Images": {"none"}}}, []string{"Authorization"})
	if err != nil {
		return nil, jinaRequestFailure(ctx, tctx, apiKey, err)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, jinaRequestFailure(ctx, tctx, apiKey, transportFailure(err, true))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Jina Search API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	raw, err := decodeJSON(text)
	if err != nil {
		return nil, errors.New("Jina Search API returned invalid JSON")
	}
	items, err := parseJinaItems(raw)
	if err != nil {
		return nil, redactErr(err, apiKey)
	}
	out := &SearchResponse{Results: []SearchResult{}}
	var content []ExtractedContent
	seen := map[string]bool{}
	for _, v := range items {
		item, _ := jsonObject(v)
		u, _ := item["url"].(string)
		u = jsTrim(u)
		if u == "" || seen[u] || !jinaPassesDomain(u, filters) {
			continue
		}
		seen[u] = true
		title, ok := trimmedString(item["title"])
		if !ok {
			title = fmt.Sprintf("Source %d", len(out.Results)+1)
		}
		snippet := ""
		if d, ok := item["description"].(string); ok {
			snippet = jsCollapse(d)
		}
		out.Results = append(out.Results, SearchResult{Title: title, URL: u, Snippet: snippet})
		if c, ok := trimmedString(item["content"]); ok {
			content = append(content, ExtractedContent{URL: u, Title: title, Content: c})
		}
		if len(out.Results) >= numResults {
			break
		}
	}
	out.Answer = sourceLines(out.Results)
	if o.IncludeContent && len(content) > 0 {
		out.InlineContent = content
	}
	return out, nil
}

func parseJinaItems(v any) ([]any, error) {
	if list, ok := v.([]any); ok {
		return list, nil
	}
	envelope, ok := jsonObject(v)
	if !ok {
		return nil, errors.New("Jina Search API returned invalid response: expected an object or array")
	}
	if code, ok := envelope["code"].(float64); ok && code != 200 {
		return nil, fmt.Errorf("Jina Search API error %s: response envelope reported failure", jsNumber(code))
	}
	data, ok := envelope["data"].([]any)
	if !ok {
		return nil, errors.New("Jina Search API returned invalid response: expected data array")
	}
	return data, nil
}

// jinaPassesDomain is passesDomainFilter (jina-search.ts:121): the URL must parse even without a
// filter.
func jinaPassesDomain(raw string, f domainFilters) bool {
	u, err := ParseURL(raw, nil)
	if err != nil {
		return false
	}
	host := strings.ToLower(JSHostname(u))
	if len(f.allowed) > 0 {
		ok := false
		for _, d := range f.allowed {
			ok = ok || hostMatchesDomain(host, d)
		}
		if !ok {
			return false
		}
	}
	for _, d := range f.blocked {
		if hostMatchesDomain(host, d) {
			return false
		}
	}
	return true
}
