package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Port of tavily.ts, including the numbered key pool (TAVILY_API_KEY_1..20).

const (
	tavilyAPIBaseURL = "https://api.tavily.com"
	tavilyTimeout    = 60 * time.Second
)

var tavilyRetryStatuses = map[int]bool{401: true, 402: true, 403: true, 429: true, 432: true}

func tavilyKeyPool() []string {
	type slot struct {
		n   int
		key string
	}
	var slots []slot
	for n := 1; n <= 20; n++ {
		if key := jsTrim(os.Getenv("TAVILY_API_KEY_" + strconv.Itoa(n))); key != "" {
			slots = append(slots, slot{n, key})
		}
	}
	requested := parseIntPrefix(os.Getenv("TAVILY_API_KEY_INDEX"))
	if requested == 0 {
		requested = 1
	}
	start := 0
	for i, s := range slots {
		if s.n >= requested {
			start = i
			break
		}
	}
	var keys []string
	seen := map[string]bool{}
	for _, s := range append(append([]slot{}, slots[start:]...), slots[:start]...) {
		if !seen[s.key] {
			seen[s.key] = true
			keys = append(keys, s.key)
		}
	}
	return keys
}

// parseIntPrefix is Number.parseInt(value, 10) with NaN as 0.
func parseIntPrefix(s string) int {
	s = jsTrim(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || end == 0 && (s[end] == '-' || s[end] == '+')) {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return n
}

// IsTavilyAvailable is true with a configured/environment key or any pool key.
func IsTavilyAvailable() bool {
	return hasProviderCredential("Tavily", "tavilyApiKey", "TAVILY_API_KEY") || len(tavilyKeyPool()) > 0
}

func tavilyKeyMissing() error {
	return fmt.Errorf("Tavily API key not found. Either:\n  1. Create %s with { \"tavilyApiKey\": \"your-key\" }\n  2. Set TAVILY_API_KEY environment variable\nGet a key at https://app.tavily.com/", ConfigPath())
}

func tavilyDomainFilter(filter []string) map[string]any {
	f := normalizeDomainFilters(filter)
	out := map[string]any{}
	if len(f.allowed) > 0 {
		out["include_domains"] = f.allowed
	}
	if len(f.blocked) > 0 {
		out["exclude_domains"] = f.blocked
	}
	return out
}

type tavilyResult struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Content    any    `json:"content"`
	RawContent any    `json:"raw_content"`
}

var tavilyStatusRE = regexp.MustCompile(`^Tavily API error (\d{3}):`)

func tavilyErrorStatus(err error) int {
	if m := tavilyStatusRE.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// SearchWithTavily queries the Tavily search API, failing over across pooled keys on quota/auth
// statuses.
func SearchWithTavily(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	base, err := apiBase("tavilyBaseUrl", "TAVILY_BASE_URL", tavilyAPIBaseURL)
	if err != nil {
		return nil, err
	}
	apiURL := base + "/search"
	numResults := NormalizeSearchResultCount(o.NumResults)
	body := map[string]any{"query": query, "search_depth": "basic", "max_results": numResults, "include_answer": "basic", "include_raw_content": false}
	if o.IncludeContent {
		body["include_raw_content"] = "markdown"
	}
	if o.RecencyFilter != "" {
		body["time_range"] = o.RecencyFilter
	}
	for k, v := range tavilyDomainFilter(o.DomainFilter) {
		body[k] = v
	}
	tctx, cancel := withTimeout(ctx, tavilyTimeout)
	defer cancel()
	data, err := tavilyWithKeyFailover(ctx, tctx, apiURL, body)
	if err != nil {
		return nil, err
	}
	resp := &SearchResponse{Results: []SearchResult{}}
	if s, ok := data.Answer.(string); ok {
		resp.Answer = s
	}
	for _, item := range data.Results {
		if item.URL == "" {
			continue
		}
		title := item.Title
		if title == "" {
			title = "Source " + strconv.Itoa(len(resp.Results)+1)
		}
		snippet := ""
		if s, ok := item.Content.(string); ok {
			snippet = collapseWS(s)
		}
		resp.Results = append(resp.Results, SearchResult{Title: title, URL: item.URL, Snippet: snippet})
		if len(resp.Results) >= numResults {
			break
		}
	}
	if o.IncludeContent {
		for _, item := range data.Results {
			if s, ok := item.RawContent.(string); ok && item.URL != "" && jsTrim(s) != "" {
				resp.InlineContent = append(resp.InlineContent, ExtractedContent{URL: item.URL, Title: item.Title, Content: s})
			}
		}
	}
	return resp, nil
}

type tavilyResponse struct {
	Answer  any            `json:"answer"`
	Results []tavilyResult `json:"results"`
}

func tavilyWithKeyFailover(parent, ctx context.Context, apiURL string, body map[string]any) (*tavilyResponse, error) {
	pool := tavilyKeyPool()
	keys := pool
	fallbackChecked := len(pool) == 0
	if len(pool) == 0 {
		key, err := resolveProviderCredential(ctx, "Tavily", "tavilyApiKey", "TAVILY_API_KEY")
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, tavilyKeyMissing()
		}
		keys = []string{key}
	}
	for i := 0; ; i++ {
		data, err := tavilyRequest(parent, ctx, apiURL, keys[i], body)
		if err == nil {
			return data, nil
		}
		if !tavilyRetryStatuses[tavilyErrorStatus(err)] {
			return nil, err
		}
		if i+1 < len(keys) {
			continue
		}
		if fallbackChecked {
			return nil, err
		}
		// Resolve the standalone credential only after every numbered pool key failed.
		fallbackChecked = true
		fallback, ferr := resolveProviderCredential(ctx, "Tavily", "tavilyApiKey", "TAVILY_API_KEY")
		if ferr != nil {
			return nil, ferr
		}
		if fallback == "" || sliceHas(keys, fallback) {
			return nil, err
		}
		keys = append(keys, fallback)
	}
}

func tavilyRequest(parent, ctx context.Context, apiURL, apiKey string, body map[string]any) (*tavilyResponse, error) {
	payload, _ := json.Marshal(body)
	resp, err := FetchWithCredentialRedirects(ctx, apiURL, RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}}}, []string{"Authorization"})
	if err != nil {
		return nil, redactErr(timeoutFix(parent, ctx, err), apiKey)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(parent, ctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Tavily API error %d: %s", resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	var data tavilyResponse
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("Tavily API returned invalid JSON: %v", err)
	}
	return &data, nil
}

var _ = strings.TrimSpace
