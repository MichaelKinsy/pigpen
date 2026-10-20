package websearch

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Port of bocha.ts.

const bochaSearchURL = "https://api.bochaai.com/v1/web-search"

// IsBochaAvailable reports whether a Bocha credential source is configured (bocha.ts:148).
func IsBochaAvailable() bool { return hasProviderCredential("Bocha", "bochaApiKey", "BOCHA_API_KEY") }

func bochaFreshness(recency string) string {
	switch recency {
	case "day":
		return "oneDay"
	case "week":
		return "oneWeek"
	case "month":
		return "oneMonth"
	case "year":
		return "oneYear"
	}
	return "noLimit"
}

type bochaBody struct {
	Query     string `json:"query"`
	Count     int    `json:"count"`
	Freshness string `json:"freshness"`
	Summary   bool   `json:"summary"`
}

// jsNumberOf is Number(v) for the JSON value kinds an envelope code can take.
func jsNumberOf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case nil:
		return 0
	case string:
		s := jsTrim(x)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "_pP") {
			return f
		}
		return math.NaN()
	}
	return math.NaN()
}

// jsStringOf is String(v) for the same kinds.
func jsStringOf(v any) string {
	switch x := v.(type) {
	case float64:
		return jsNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	case string:
		return x
	case []any:
		return "[object Array]"
	}
	return "[object Object]"
}

func firstTrimmed(values ...any) (string, bool) {
	for _, v := range values {
		if s, ok := trimmedString(v); ok {
			return s, true
		}
	}
	return "", false
}

// SearchWithBocha queries the Bocha web search API (bocha.ts:152).
func SearchWithBocha(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	apiKey, err := requireProviderKey(ctx, "Bocha", "Bocha", "bochaApiKey", "BOCHA_API_KEY", "https://open.bochaai.com/")
	if err != nil {
		return nil, err
	}
	numResults := normalizeCountDefault(o.NumResults, 8)
	filters := normalizeDomainFilters(o.DomainFilter)
	tctx, cancel := withTimeout(ctx, restTimeout(restSearchTimeout))
	defer cancel()
	data, err := restCallA(ctx, tctx, "Bocha API", apiKey, bochaSearchURL, RequestInit{Method: "POST",
		Body:   marshalJS(bochaBody{Query: query, Count: numResults, Freshness: bochaFreshness(o.RecencyFilter), Summary: true}),
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}, "Accept": {"application/json"}}}, []string{"Authorization"})
	if err != nil {
		return nil, err
	}
	results, err := parseBochaResponse(data)
	if err != nil {
		return nil, redactErr(err, apiKey)
	}
	kept := []SearchResult{}
	for _, r := range results {
		if filters.matches(r.URL) {
			kept = append(kept, r)
		}
	}
	if len(kept) > numResults {
		kept = kept[:numResults]
	}
	return &SearchResponse{Answer: sourceLines(kept), Results: kept}, nil
}

func parseBochaResponse(data any) ([]SearchResult, error) {
	invalid := func(msg string) error { return fmt.Errorf("Bocha API returned invalid response: %s", msg) }
	envelope, ok := jsonObject(data)
	if !ok {
		return nil, invalid("expected an object envelope")
	}
	if code, present := envelope["code"]; present && jsNumberOf(code) != 200 {
		msg, ok := firstTrimmed(envelope["msg"])
		if !ok {
			msg = "unknown error"
		}
		return nil, invalid("code " + jsStringOf(code) + ": " + msg)
	}
	var items []any
	if d, ok := jsonObject(envelope["data"]); ok {
		if pages, ok := jsonObject(d["webPages"]); ok {
			items, ok = pages["value"].([]any)
			if !ok {
				return nil, invalid("missing data.webPages.value array")
			}
		} else {
			return nil, invalid("missing data.webPages.value array")
		}
	} else {
		return nil, invalid("missing data.webPages.value array")
	}
	results := []SearchResult{}
	for _, raw := range items {
		entry, ok := jsonObject(raw)
		if !ok {
			continue
		}
		u, ok := firstTrimmed(entry["url"], entry["link"], entry["href"])
		if !ok {
			continue
		}
		title, ok := firstTrimmed(entry["title"], entry["name"])
		if !ok {
			title = u
		}
		snippet, _ := firstTrimmed(entry["summary"], entry["snippet"], entry["description"], entry["content"])
		results = append(results, SearchResult{Title: title, URL: u, Snippet: snippet})
	}
	return results, nil
}
