package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
)

// Port of perplexity.ts.

const (
	perplexityAPIURL   = "https://api.perplexity.ai/chat/completions"
	perplexityMaxReqs  = 10
	perplexityWindowMs = 60 * 1000
	maxCitations       = 20
)

var (
	perplexityMu         sync.Mutex
	perplexityTimestamps []int64
)

// resetProviderState clears in-memory provider state (the Perplexity rate window).
func resetProviderState() {
	perplexityMu.Lock()
	perplexityTimestamps = nil
	perplexityMu.Unlock()
}

func checkPerplexityRateLimit() error {
	perplexityMu.Lock()
	defer perplexityMu.Unlock()
	now := nowMs()
	windowStart := now - perplexityWindowMs
	for len(perplexityTimestamps) > 0 && perplexityTimestamps[0] < windowStart {
		perplexityTimestamps = perplexityTimestamps[1:]
	}
	if len(perplexityTimestamps) >= perplexityMaxReqs {
		wait := perplexityTimestamps[0] + perplexityWindowMs - now
		return fmt.Errorf("Rate limited. Try again in %ds", int(math.Ceil(float64(wait)/1000)))
	}
	perplexityTimestamps = append(perplexityTimestamps, now)
	return nil
}

var perplexityDomain = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*\.[a-zA-Z]{2,}$`)
var citationRef = regexp.MustCompile(`\[(\d{1,3})\]`)

func citationsToKeep(answer string, available, numResults int) int {
	highest := 0
	for _, m := range citationRef.FindAllStringSubmatch(answer, -1) {
		if n, _ := strconv.Atoi(m[1]); n > highest {
			highest = n
		}
	}
	keep := numResults
	if highest > keep {
		keep = highest
	}
	if keep > maxCitations {
		keep = maxCitations
	}
	if available < keep {
		keep = available
	}
	return keep
}

// SearchWithPerplexity asks Perplexity's sonar model and returns its cited sources.
func SearchWithPerplexity(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	if err := checkPerplexityRateLimit(); err != nil {
		return nil, err
	}
	apiKey, err := resolveProviderCredential(ctx, "Perplexity", "perplexityApiKey", "PERPLEXITY_API_KEY")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Perplexity API key not found. Either:\n  1. Create %s with { \"perplexityApiKey\": \"your-key\" }\n  2. Set PERPLEXITY_API_KEY environment variable\nGet a key at https://perplexity.ai/settings/api", ConfigPath())
	}
	numResults := 5
	if o.NumResults != nil && !math.IsNaN(*o.NumResults) && !math.IsInf(*o.NumResults, 0) {
		numResults = int(math.Max(1, math.Min(math.Floor(*o.NumResults), 20)))
	}
	body := map[string]any{"model": "sonar", "messages": []map[string]any{{"role": "user", "content": query}}, "max_tokens": 1024, "return_related_questions": false}
	if o.RecencyFilter != "" {
		body["search_recency_filter"] = o.RecencyFilter
	}
	var domains []string
	for _, d := range o.DomainFilter {
		name := d
		if len(name) > 0 && name[0] == '-' {
			name = name[1:]
		}
		if perplexityDomain.MatchString(name) {
			domains = append(domains, d)
		}
	}
	if len(domains) > 0 {
		body["search_domain_filter"] = domains
	}
	payload, _ := json.Marshal(body)
	tctx, cancel := withTimeout(ctx, providerDefaultTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, perplexityAPIURL, RequestInit{Method: "POST", Body: payload,
		Header: http.Header{"Authorization": {"Bearer " + apiKey}, "Content-Type": {"application/json"}}}, nil)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("Perplexity API error %d: %s", resp.StatusCode, RedactCredential(text, apiKey))
	}
	var data struct {
		Choices   []struct{ Message struct{ Content string } } `json:"choices"`
		Citations []any                                        `json:"citations"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("Perplexity API returned invalid JSON: %v", err)
	}
	answer := ""
	if len(data.Choices) > 0 {
		answer = data.Choices[0].Message.Content
	}
	results := []SearchResult{}
	n := citationsToKeep(answer, len(data.Citations), numResults)
	for i := 0; i < n; i++ {
		switch c := data.Citations[i].(type) {
		case string:
			results = append(results, SearchResult{Title: fmt.Sprintf("Source %d", i+1), URL: c})
		case map[string]any:
			if u, ok := c["url"].(string); ok {
				title, _ := c["title"].(string)
				if title == "" {
					title = fmt.Sprintf("Source %d", i+1)
				}
				results = append(results, SearchResult{Title: title, URL: u})
			}
		}
	}
	return &SearchResponse{Answer: answer, Results: results}, nil
}

// configValue returns a web-search.json value; a config that cannot be read counts as absent
// here (the parse error surfaces from the call that needs the config, ReadConfigRoot).
func configValue(key string) any {
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return nil
	}
	return root[key]
}

// IsPerplexityAvailable reports whether a Perplexity credential source is configured.
func IsPerplexityAvailable() bool {
	return hasProviderCredential("Perplexity", "perplexityApiKey", "PERPLEXITY_API_KEY")
}

// PerplexityAvailable is the earlier name of IsPerplexityAvailable.
func PerplexityAvailable() bool { return IsPerplexityAvailable() }

// envOrNil is `process.env.X`: undefined when unset, the (possibly empty) value otherwise.
func envOrNil(name string) any {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return nil
}
