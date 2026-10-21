package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Port of xcrawl.ts.

const xcrawlAPIURL = "https://run.xcrawl.com/v1/serp"

// IsXcrawlAvailable reports whether an XCrawl credential source is configured (xcrawl.ts:63).
func IsXcrawlAvailable() bool {
	return hasProviderCredential("XCrawl", "xcrawlApiKey", "XCRAWL_API_KEY")
}

var absoluteHTTPRE = regexp.MustCompile(`(?i)^https?://`)

// xcrawlAbsolutize resolves a link relative to the API origin (xcrawl.ts:82); a link that cannot
// be resolved is returned unchanged.
func xcrawlAbsolutize(link string) string {
	if absoluteHTTPRE.MatchString(link) {
		return link
	}
	base, err := ParseURL(xcrawlAPIURL, nil)
	if err != nil {
		return link
	}
	u, err := ParseURL(link, base)
	if err != nil {
		return link
	}
	return u.String()
}

type xcrawlBody struct {
	Engine string `json:"engine"`
	Q      string `json:"q"`
}

// SearchWithXCrawl queries XCrawl's Google SERP API (xcrawl.ts:164). The API has no domain
// filter, so the shared filter is applied to what comes back.
func SearchWithXCrawl(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if err := restConfigErr(); err != nil {
		return nil, err
	}
	apiKey, err := resolveProviderCredential(ctx, "XCrawl", "xcrawlApiKey", "XCRAWL_API_KEY")
	if err != nil {
		return nil, err
	}
	numResults := NormalizeSearchResultCount(o.NumResults)
	if apiKey == "" {
		return nil, fmt.Errorf("XCrawl search requires an API key. Set xcrawlApiKey in %s or export XCRAWL_API_KEY. Get one at https://dash.xcrawl.com/", ConfigPath())
	}
	act := logAPIStart(query)
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, xcrawlAPIURL, RequestInit{Method: "POST", Body: marshalJS(xcrawlBody{"google_search", query}),
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}}}, []string{"Authorization"})
	if err != nil {
		return nil, act.failedBy(ctx, restFailure(ctx, tctx, "XCrawl search", apiKey, err), apiKey)
	}
	act.complete(resp.StatusCode)
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		errorText := RedactCredential(text, apiKey)
		detail := slice300(errorText)
		var parsed struct {
			Message any `json:"message"`
			Error   any `json:"error"`
		}
		if json.Unmarshal([]byte(errorText), &parsed) == nil {
			if s, ok := parsed.Message.(string); ok {
				detail = slice300(s)
			} else if s, ok := parsed.Error.(string); ok {
				detail = slice300(s)
			}
		}
		return nil, fmt.Errorf("XCrawl API error (%d): %s", resp.StatusCode, detail)
	}
	payload, err := decodeJSON(text)
	if err != nil {
		return nil, fmt.Errorf("XCrawl API returned invalid response: response body is not valid JSON: %v", err)
	}
	results, err := parseXCrawl(payload)
	if err != nil {
		return nil, err
	}
	if len(o.DomainFilter) > 0 {
		results = xcrawlFilter(results, o.DomainFilter)
	}
	if len(results) > numResults {
		results = results[:numResults]
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}

func xcrawlFilter(results []SearchResult, filter []string) []SearchResult {
	var includes, excludes []string
	for _, raw := range filter {
		d := NormalizeDomain(raw)
		if d == "" {
			continue
		}
		if strings.HasPrefix(jsTrim(raw), "-") {
			excludes = append(excludes, d)
		} else {
			includes = append(includes, d)
		}
	}
	if len(includes) == 0 && len(excludes) == 0 {
		return results
	}
	out := []SearchResult{}
	for _, r := range results {
		u, err := ParseURL(r.URL, nil)
		if err != nil {
			continue
		}
		host := strings.ToLower(JSHostname(u))
		if host == "" {
			continue
		}
		matches := func(list []string) bool {
			for _, d := range list {
				if hostMatchesDomain(host, d) {
					return true
				}
			}
			return false
		}
		if matches(excludes) || (len(includes) > 0 && !matches(includes)) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func parseXCrawl(value any) ([]SearchResult, error) {
	invalid := func(msg string) error { return fmt.Errorf("XCrawl API returned invalid response: %s", msg) }
	envelope, ok := jsonObject(value)
	if !ok {
		return nil, invalid("expected an object envelope")
	}
	metadata, ok := jsonObject(envelope["search_metadata"])
	if !ok {
		return nil, invalid("expected search_metadata object")
	}
	if status, present := metadata["status"]; present && status != "completed" {
		b, _ := json.Marshal(status)
		return nil, invalid(`expected search_metadata.status "completed", got ` + string(b))
	}
	list, ok := envelope["organic_results"].([]any)
	if !ok {
		return nil, invalid("expected organic_results array")
	}
	results := []SearchResult{}
	for i, raw := range list {
		entry, ok := jsonObject(raw)
		if !ok {
			return nil, invalid(fmt.Sprintf("expected organic_results[%d] object", i))
		}
		link, ok := entry["link"].(string)
		if !ok || jsTrim(link) == "" {
			return nil, invalid(fmt.Sprintf("expected organic_results[%d].link to be a non-empty string", i))
		}
		titleVal := entry["title"]
		if _, isString := titleVal.(string); titleVal != nil && !isString {
			return nil, invalid(fmt.Sprintf("expected organic_results[%d].title to be a string or null", i))
		}
		snippetVal := entry["snippet"]
		if _, isString := snippetVal.(string); snippetVal != nil && !isString {
			return nil, invalid(fmt.Sprintf("expected organic_results[%d].snippet to be a string or null", i))
		}
		resolved := xcrawlAbsolutize(jsTrim(link))
		title := resolved
		if s, ok := titleVal.(string); ok && len(jsTrim(s)) > 0 {
			title = s
		}
		snippet, _ := snippetVal.(string)
		results = append(results, SearchResult{Title: title, URL: resolved, Snippet: snippet})
	}
	return results, nil
}
