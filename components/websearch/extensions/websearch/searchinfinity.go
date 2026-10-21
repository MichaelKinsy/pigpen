package websearch

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Port of searchinfinity.ts: BytePlus Search Infinity web search.

const (
	searchinfinityURL     = "https://torchlight.byteintlapi.com/search_api/web_search"
	searchinfinityTimeout = 30 * time.Second
)

// IsSearchinfinityAvailable reports whether a Searchinfinity credential source is configured.
func IsSearchinfinityAvailable() bool {
	return hasProviderCredential("Searchinfinity", "searchinfinityApiKey", "SEARCHINFINITY_API_KEY")
}

var searchinfinityRecency = map[string]string{"day": "OneDay", "week": "OneWeek", "month": "OneMonth", "year": "OneYear"}

func searchinfinityBody(query string, o SearchOptions) map[string]any {
	var include, block []string
	for _, raw := range o.DomainFilter {
		d := NormalizeDomain(raw)
		if d == "" {
			continue
		}
		target := &include
		if strings.HasPrefix(jsTrim(raw), "-") {
			target = &block
		}
		if len(*target) < 5 && !sliceHas(*target, d) {
			*target = append(*target, d)
		}
	}
	body := map[string]any{"Query": query, "Count": NormalizeSearchResultCount(o.NumResults)}
	filter := map[string]any{}
	if len(include) > 0 {
		filter["Sites"] = strings.Join(include, "|")
	}
	if len(block) > 0 {
		filter["BlockHosts"] = strings.Join(block, "|")
	}
	if len(filter) > 0 {
		body["Filter"] = filter
	}
	if t := searchinfinityRecency[o.RecencyFilter]; t != "" {
		body["TimeRange"] = t
	}
	return body
}

var searchinfinityQuotaRE = regexp.MustCompile(`(?i)quota|exhaust`)

// searchinfinityStatus maps business error codes to the closest HTTP status (searchinfinity.ts:
// businessErrorStatus); 0 means none.
func searchinfinityStatus(codeN *float64, code, message string) int {
	is := func(n float64, slug string) bool { return codeN != nil && *codeN == n || slug != "" && code == slug }
	switch {
	case is(700901, "invalid_api_key"):
		return 401
	case is(700429, "700429"):
		return 429
	case is(10400, "10400"):
		return 400
	case is(10500, "10500"):
		return 500
	case is(10403, "10403"):
		if searchinfinityQuotaRE.MatchString(message) {
			return 429
		}
		return 403
	}
	return 0
}

// SearchWithSearchinfinity queries Search Infinity.
func SearchWithSearchinfinity(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	key, err := requireHostedKey(ctx, "Searchinfinity API", "Searchinfinity", "searchinfinityApiKey", "SEARCHINFINITY_API_KEY", "Create a key at https://console.byteplus.com/search-infinity/api-key")
	if err != nil {
		return nil, err
	}
	act := logAPIStart(query)
	return logged(act, key, func() (*SearchResponse, error) {
		data, err := hostedJSON(ctx, "Searchinfinity Search", hostedCall{URL: searchinfinityURL, Body: jsonBytes(searchinfinityBody(query, o)), Key: key, Timeout: searchinfinityTimeout,
			Header: http.Header{"Authorization": {"Bearer " + key}, "Content-Type": {"application/json"}}, Credentials: []string{"Authorization"}})
		if err != nil {
			return nil, err
		}
		root := asMap(data)
		if be := asMap(asMap(root["ResponseMetadata"])["Error"]); be != nil {
			codeStr, _ := strField(be, "Code")
			msgStr, _ := strField(be, "Message")
			if codeStr != "" || msgStr != "" {
				code := "unknown"
				if codeStr != "" {
					code = codeStr
				}
				message := "unknown error"
				if msgStr != "" {
					message = msgStr
				}
				var codeN *float64
				if n, ok := be["CodeN"].(float64); ok {
					codeN = &n
				}
				status := "unknown"
				if s := searchinfinityStatus(codeN, code, message); s != 0 {
					status = fmt.Sprint(s)
				}
				label := code
				if codeN != nil {
					label = jsNumber(*codeN) + " " + code
				}
				return nil, fmt.Errorf("Searchinfinity Search API error %s: %s (code %s)", status, message, label)
			}
		}
		items, ok := asMap(root["Result"])["WebResults"].([]any)
		if !ok {
			return nil, fmt.Errorf("Searchinfinity Search API returned an unexpected response shape")
		}
		results := []SearchResult{}
		for _, it := range items {
			item := asMap(it)
			u, ok := strField(item, "Url")
			if !ok || jsTrim(u) == "" {
				continue
			}
			u = jsTrim(u)
			title := u
			if t, ok := strField(item, "Title"); ok && jsTrim(t) != "" {
				title = jsTrim(t)
			}
			summary, snippet := "", ""
			if s, ok := strField(item, "Summary"); ok {
				summary = collapseWS(s)
			}
			if s, ok := strField(item, "Snippet"); ok {
				snippet = collapseWS(s)
			}
			if summary != "" {
				snippet = summary
			}
			results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
		}
		return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
	})
}
