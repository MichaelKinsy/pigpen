package web

// Ported HTTP + environment-key web search providers from omp.
// Selection order follows omp's default web role chain (behavior.kdl:521-533)
// followed by remaining credentialed HTTP+key providers in provider.ts:15-42 order.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Provider base URLs (vars so tests can inspect or override if needed, though tests
// primarily route through httpClient).
var (
	parallelSearchURL   = "https://api.parallel.ai/v1beta/search"
	exaSearchURL        = "https://api.exa.ai/search"
	firecrawlDefaultURL = "https://api.firecrawl.dev/v2"
	perplexityChatURL   = "https://api.perplexity.ai/chat/completions"
	tinyfishSearchURL   = "https://api.search.tinyfish.ai"
	jinaSearchURL       = "https://s.jina.ai"
	kagiSearchURL       = "https://kagi.com/api/v1/search"
	tavilySearchURL     = "https://api.tavily.com/search"
	braveSearchURL      = "https://api.search.brave.com/res/v1/web/search"
	deepseekSearchURL   = "https://api.deepseek.com/v1/tools/search"
	kimiSearchURL       = "https://api.kimi.com/coding/v1/search"
	syntheticSearchURL  = "https://api.synthetic.new/v2/search"
	ollamaSearchURL     = "https://ollama.com/api/web_search"
)

const (
	parallelBetaHeader    = "search-extract-2025-10-10"
	maxExaSnippetChars    = 500
	maxExaAnswerSummaries = 3
	maxProviderRespBytes  = 2 * 1024 * 1024
	maxProviderErrBytes   = 8 * 1024
)

func envTrimmed(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func doJSONRequest(
	ctx context.Context,
	client *http.Client,
	method string,
	targetURL string,
	headers map[string]string,
	body any,
) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return client.Do(req)
}

func withProviderTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultWebSearchTimeoutSeconds * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

// =============================================================================
// 1. Parallel (PARALLEL_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/parallel.ts, web/parallel.ts
// =============================================================================

type parallelProvider struct{}

func (parallelProvider) ID() string        { return "parallel" }
func (parallelProvider) Label() string     { return "Parallel" }
func (parallelProvider) IsAvailable() bool { return true }
func (parallelProvider) IsKeyless() bool   { return envTrimmed("PARALLEL_API_KEY") == "" }

var parallelQuerySyntax = QuerySyntax{Phrases: true, Negation: true, Or: true}

type parallelSourcePolicy struct {
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
	AfterDate      string   `json:"after_date,omitempty"`
}

func toParallelSourcePolicy(parsed StructuredQuery, recency string) *parallelSourcePolicy {
	var p parallelSourcePolicy
	include := siteHosts(parsed.Sites)
	exclude := siteHosts(parsed.ExcludedSites)
	if len(include) > 0 {
		p.IncludeDomains = include
	} else if len(exclude) > 0 {
		p.ExcludeDomains = exclude
	}
	if parsed.After != "" {
		p.AfterDate = parsed.After
	} else if recency != "" {
		p.AfterDate = recencyAfterDate(recency)
	}
	if len(p.IncludeDomains) == 0 && len(p.ExcludeDomains) == 0 && p.AfterDate == "" {
		return nil
	}
	return &p
}

func (parallelProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	numResults := clampNumResults(params.requestedResults(), 10, 40)
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, parallelQuerySyntax)
	}
	policy := toParallelSourcePolicy(parsed, params.Recency)
	apiKey := envTrimmed("PARALLEL_API_KEY")
	if apiKey == "" {
		rawMCPQuery := params.Query
		if parsed.HasDirectives {
			rawMCPQuery = FormatQuery(parsed, parallelMCPQuerySyntax)
		}
		mcpQuery := rawMCPQuery
		if parsed.After == "" && policy != nil && policy.AfterDate != "" {
			mcpQuery = rawMCPQuery + " after:" + policy.AfterDate
		}
		tctx, cancel := withProviderTimeout(ctx, params.Timeout)
		defer cancel()
		return searchParallelPublicMCP(tctx, client, query, mcpQuery, numResults)
	}
	reqBody := map[string]any{
		"objective":      query,
		"search_queries": []string{query},
		"mode":           "fast",
		"excerpts": map[string]any{
			"max_chars_per_result": 10000,
		},
	}
	if policy := toParallelSourcePolicy(parsed, params.Recency); policy != nil {
		reqBody["source_policy"] = policy
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, parallelSearchURL, map[string]string{
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"x-api-key":     apiKey,
		"parallel-beta": parallelBetaHeader,
	}, reqBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "parallel", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("parallel", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		detail := extractParallelError(errText)
		msg := fmt.Sprintf("Parallel API error (%d)", resp.StatusCode)
		if detail != "" {
			msg = fmt.Sprintf("Parallel API error (%d): %s", resp.StatusCode, detail)
		}
		return SearchResponse{}, &SearchProviderError{Provider: "parallel", Message: msg, Status: resp.StatusCode}
	}

	raw, err := readLimitedText(resp, "parallel", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		SearchID string `json:"search_id"`
		Results  []struct {
			Title       string   `json:"title"`
			URL         string   `json:"url"`
			PublishDate string   `json:"publish_date"`
			Excerpts    []string `json:"excerpts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{
			Provider: "parallel",
			Message:  fmt.Sprintf("Parallel search returned invalid JSON: %v", err),
		}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := strings.Join(r.Excerpts, "\n\n")
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       snippet,
			PublishedDate: r.PublishDate,
			AgeSeconds:    dateToAgeSeconds(r.PublishDate),
		})
		if len(sources) >= numResults {
			break
		}
	}

	return SearchResponse{
		Provider:  "parallel",
		Sources:   sources,
		RequestID: payload.SearchID,
		AuthMode:  "api_key",
	}, nil
}

func extractParallelError(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return trimmed
	}
	for _, k := range []string{"message", "detail", "error"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if nested, ok := m["error"].(map[string]any); ok {
		for _, k := range []string{"message", "detail"} {
			if s, ok := nested[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return trimmed
}

// =============================================================================
// 2. Exa (EXA_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/exa.ts
// =============================================================================

type exaProvider struct{}

func (exaProvider) ID() string        { return "exa" }
func (exaProvider) Label() string     { return "Exa" }
func (exaProvider) IsAvailable() bool { return envTrimmed("EXA_API_KEY") != "" }

type exaResultItem struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"`
	Author        string   `json:"author"`
	Summary       string   `json:"summary"`
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
}

func synthesizeExaAnswer(results []exaResultItem) string {
	var parts []string
	for _, r := range results {
		if len(parts) >= maxExaAnswerSummaries {
			break
		}
		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			continue
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.URL
		}
		if title == "" {
			title = "Untitled"
		}
		parts = append(parts, fmt.Sprintf("**%s**: %s", title, summary))
	}
	return strings.Join(parts, "\n\n")
}

func (exaProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("EXA_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "exa",
			Message:  "Exa credentials not found. Set EXA_API_KEY.",
			Status:   401,
		}
	}
	parsed := parsedOrParse(params)
	query := params.Query
	var includeDomains, excludeDomains []string
	var startDate, endDate string
	if parsed.HasDirectives {
		query = FormatQuery(parsed, QuerySyntax{Phrases: true})
		includeDomains = siteHosts(parsed.Sites)
		excludeDomains = siteHosts(parsed.ExcludedSites)
		startDate = parsed.After
		endDate = parsed.Before
	}
	if startDate == "" && params.Recency != "" {
		startDate = recencyAfterDate(params.Recency)
	}

	numResults := params.requestedResults()
	if numResults <= 0 {
		numResults = 10
	}

	reqBody := map[string]any{
		"query":      query,
		"numResults": numResults,
		"type":       "auto",
		"contents": map[string]any{
			"summary": map[string]any{"query": query},
		},
	}
	if len(includeDomains) > 0 {
		reqBody["includeDomains"] = includeDomains
	}
	if len(excludeDomains) > 0 {
		reqBody["excludeDomains"] = excludeDomains
	}
	if startDate != "" {
		reqBody["startPublishedDate"] = startDate
	}
	if endDate != "" {
		reqBody["endPublishedDate"] = endDate
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, exaSearchURL, map[string]string{
		"Content-Type": "application/json",
		"x-api-key":    apiKey,
	}, reqBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "exa", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("exa", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "exa",
			Message:  fmt.Sprintf("Exa API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "exa", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		RequestID string          `json:"requestId"`
		Results   []exaResultItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "exa", Message: "Exa API returned invalid JSON", Status: 500}
	}

	var withURL []exaResultItem
	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		withURL = append(withURL, r)
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := r.Summary
		if snippet == "" {
			snippet = r.Text
		}
		if snippet == "" && len(r.Highlights) > 0 {
			snippet = strings.Join(r.Highlights, " ")
		}
		if len([]rune(snippet)) > maxExaSnippetChars {
			snippet = string([]rune(snippet)[:maxExaSnippetChars])
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       snippet,
			PublishedDate: r.PublishedDate,
			AgeSeconds:    dateToAgeSeconds(r.PublishedDate),
			Author:        r.Author,
		})
	}
	if len(sources) > numResults {
		sources = sources[:numResults]
	}

	return SearchResponse{
		Provider:  "exa",
		Answer:    synthesizeExaAnswer(withURL),
		Sources:   sources,
		RequestID: payload.RequestID,
		AuthMode:  "api_key",
	}, nil
}

// =============================================================================
// 3. Firecrawl (FIRECRAWL_API_KEY, optional FIRECRAWL_BASE_URL / FIRECRAWL_API_URL)
// upstream: packages/coding-agent/src/web/search/providers/firecrawl.ts, web/firecrawl.ts
// =============================================================================

type firecrawlProvider struct{}

func (firecrawlProvider) ID() string        { return "firecrawl" }
func (firecrawlProvider) Label() string     { return "Firecrawl" }
func (firecrawlProvider) IsAvailable() bool { return true }
func (firecrawlProvider) IsKeyless() bool {
	return envTrimmed("FIRECRAWL_API_KEY") == "" && envTrimmed("FIRECRAWL_BASE_URL") == "" && envTrimmed("FIRECRAWL_API_URL") == ""
}

var (
	firecrawlVersionRe = regexp.MustCompile(`(?i)/v[12]$`)
	firecrawlRecency   = map[string]string{
		"day":   "qdr:d",
		"week":  "qdr:w",
		"month": "qdr:m",
		"year":  "qdr:y",
	}
)

// resolveFirecrawlURL resolves the Firecrawl endpoint applying self-hosted overrides.
// upstream: packages/coding-agent/src/web/firecrawl.ts:25-48
func resolveFirecrawlURL(endpoint string) (string, error) {
	configured := envTrimmed("FIRECRAWL_BASE_URL")
	if configured == "" {
		configured = envTrimmed("FIRECRAWL_API_URL")
	}
	if configured == "" {
		return firecrawlDefaultURL + endpoint, nil
	}
	u, err := url.Parse(configured)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("Invalid Firecrawl base URL: expected an HTTP or HTTPS URL")
	}
	if u.User != nil {
		return "", errors.New("Invalid Firecrawl base URL: URL credentials are not allowed")
	}
	u.RawQuery = ""
	u.Fragment = ""
	basePath := strings.TrimRight(u.Path, "/")
	if !firecrawlVersionRe.MatchString(basePath) {
		basePath += "/v2"
	}
	u.Path = basePath + endpoint
	return u.String(), nil
}

func toGoogleDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	return parts[1] + "/" + parts[2] + "/" + parts[0]
}

func buildFirecrawlDateTbs(parsed StructuredQuery) string {
	if parsed.After == "" && parsed.Before == "" {
		return ""
	}
	parts := []string{"cdr:1"}
	if parsed.After != "" {
		parts = append(parts, "cd_min:"+toGoogleDate(parsed.After))
	}
	if parsed.Before != "" {
		parts = append(parts, "cd_max:"+toGoogleDate(parsed.Before))
	}
	return strings.Join(parts, ",")
}

type firecrawlWebItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
	Markdown    string `json:"markdown"`
}

func (firecrawlProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	endpoint, err := resolveFirecrawlURL("/search")
	if err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "firecrawl", Message: err.Error(), Status: 400}
	}
	parsed := parsedOrParse(params)
	query := params.Query
	tbs := ""
	if parsed.HasDirectives {
		tbs = buildFirecrawlDateTbs(parsed)
		syntax := GoogleQuerySyntax
		if tbs != "" {
			syntax.DateRange = false
		}
		query = FormatQuery(parsed, syntax)
	}
	if tbs == "" && params.Recency != "" {
		tbs = firecrawlRecency[params.Recency]
	}

	numResults := clampNumResults(params.requestedResults(), 10, 100)
	reqBody := map[string]any{
		"query":   query,
		"limit":   numResults,
		"sources": []map[string]string{{"type": "web"}},
	}
	if tbs != "" {
		reqBody["tbs"] = tbs
	}

	headers := map[string]string{"Content-Type": "application/json"}
	apiKey := envTrimmed("FIRECRAWL_API_KEY")
	authMode := "keyless"
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
		authMode = "api_key"
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, endpoint, headers, reqBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "firecrawl", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("firecrawl", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "firecrawl",
			Message:  fmt.Sprintf("Firecrawl API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "firecrawl", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var envelope struct {
		Success *bool              `json:"success"`
		Error   string             `json:"error"`
		ID      string             `json:"id"`
		Data    json.RawMessage    `json:"data"`
		Results []firecrawlWebItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "firecrawl", Message: "Firecrawl API returned invalid JSON", Status: 500}
	}
	if envelope.Success != nil && !*envelope.Success {
		msg := strings.TrimSpace(envelope.Error)
		if msg == "" {
			msg = "Firecrawl request failed"
		}
		return SearchResponse{}, &SearchProviderError{Provider: "firecrawl", Message: msg}
	}

	var items []firecrawlWebItem
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, &items); err != nil {
			var nested struct {
				Web []firecrawlWebItem `json:"web"`
			}
			if err2 := json.Unmarshal(envelope.Data, &nested); err2 == nil {
				items = nested.Web
			}
		}
	}
	if len(items) == 0 {
		items = envelope.Results
	}

	sources := make([]SearchSource, 0, len(items))
	for _, r := range items {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := r.Description
		if snippet == "" {
			snippet = r.Snippet
		}
		if snippet == "" {
			snippet = r.Markdown
		}
		sources = append(sources, SearchSource{
			Title:   title,
			URL:     r.URL,
			Snippet: snippet,
		})
		if len(sources) >= numResults {
			break
		}
	}

	return SearchResponse{
		Provider:  "firecrawl",
		Sources:   sources,
		RequestID: envelope.ID,
		AuthMode:  authMode,
	}, nil
}

// =============================================================================
// 4. SearXNG (SEARXNG_ENDPOINT, optional SEARXNG_BASIC_USERNAME/PASSWORD or SEARXNG_TOKEN)
// upstream: packages/coding-agent/src/web/search/providers/searxng.ts
// =============================================================================

// ctxAvailableProvider is an optional interface for providers whose
// availability depends on session settings read via ctx (e.g. searxng.endpoint).
type ctxAvailableProvider interface {
	IsAvailableCtx(ctx context.Context) bool
}

type searxngProvider struct{}

func (searxngProvider) ID() string    { return "searxng" }
func (searxngProvider) Label() string { return "SearXNG" }
func (searxngProvider) IsAvailable() bool {
	return searxngEndpoint(context.Background()) != ""
}
func (searxngProvider) IsAvailableCtx(ctx context.Context) bool {
	return searxngEndpoint(ctx) != ""
}

// searxngEndpoint reads searxng.endpoint from settings, falling back to SEARXNG_ENDPOINT
// when blank/unset (upstream: packages/coding-agent/src/web/settings.ts:33-44).
func searxngEndpoint(ctx context.Context) string {
	if v, ok := settingString(ctx, "searxng.endpoint"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return envTrimmed("SEARXNG_ENDPOINT")
}

// searxngToken reads searxng.token from settings, falling back to SEARXNG_TOKEN
// when blank/unset (upstream: packages/coding-agent/src/web/settings.ts:46-52).
func searxngToken(ctx context.Context) string {
	if v, ok := settingString(ctx, "searxng.token"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return envTrimmed("SEARXNG_TOKEN")
}

// searxngBasicCred reads searxng.basicUsername/basicPassword from settings,
// falling back to the env var when unset (preserving empty strings per RFC 7617).
// upstream: packages/coding-agent/src/web/settings.ts:54-68
func searxngBasicCred(ctx context.Context, settingKey, envKey string) (string, bool) {
	if v, ok := settingString(ctx, settingKey); ok {
		return v, true
	}
	return os.LookupEnv(envKey)
}

var searxngRecencyMap = map[string]string{
	"day":   "day",
	"week":  "month",
	"month": "month",
	"year":  "year",
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if (r >= 0x00 && r <= 0x1F) || (r >= 0x7F && r <= 0x9F) {
			return true
		}
	}
	return false
}

func findSearXNGAuth(ctx context.Context) (string, error) {
	user, hasUser := searxngBasicCred(ctx, "searxng.basicUsername", "SEARXNG_BASIC_USERNAME")
	pass, hasPass := searxngBasicCred(ctx, "searxng.basicPassword", "SEARXNG_BASIC_PASSWORD")
	if hasUser || hasPass {
		if !hasUser || !hasPass {
			return "", errors.New("SearXNG Basic auth requires both searxng.basicUsername and searxng.basicPassword, or SEARXNG_BASIC_USERNAME and SEARXNG_BASIC_PASSWORD.")
		}
		if strings.Contains(user, ":") {
			return "", errors.New("SearXNG Basic auth username cannot contain ':' because RFC 7617 uses it as the separator.")
		}
		if hasControlChars(user) || hasControlChars(pass) {
			return "", errors.New("SearXNG Basic auth credentials must not contain RFC 7617 control characters.")
		}
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)), nil
	}
	if tok := searxngToken(ctx); tok != "" {
		return "Bearer " + tok, nil
	}
	return "", nil
}

func stripExternalBangs(query string) string {
	fields := strings.Fields(query)
	var kept []string
	for _, f := range fields {
		if !strings.HasPrefix(f, "!!") {
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
}

func extractSearXNGAnswerText(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		if s, ok := v["answer"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if trs, ok := v["translations"].([]any); ok {
			var lines []string
			for _, item := range trs {
				if m, ok := item.(map[string]any); ok {
					if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
						lines = append(lines, strings.TrimSpace(t))
						if len(lines) == 3 {
							break
						}
					}
				}
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\n")
			}
		}
		if cur, ok := v["current"].(map[string]any); ok {
			if s, ok := cur["summary"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
			var parts []string
			if loc, ok := cur["location"].(map[string]any); ok {
				if name, ok := loc["name"].(string); ok && strings.TrimSpace(name) != "" {
					parts = append(parts, strings.TrimSpace(name))
				}
			}
			if temp, ok := cur["temperature"].(map[string]any); ok {
				unit, _ := temp["unit"].(string)
				switch tv := temp["val"].(type) {
				case string:
					if tv != "" {
						parts = append(parts, tv+unit)
					}
				case float64:
					parts = append(parts, strconv.FormatFloat(tv, 'f', -1, 64)+unit)
				}
			}
			if cond, ok := cur["condition"].(string); ok && strings.TrimSpace(cond) != "" {
				parts = append(parts, strings.TrimSpace(cond))
			}
			if len(parts) > 0 {
				return strings.Join(parts, ": ")
			}
		}
	}
	return ""
}

func (searxngProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	endpoint := searxngEndpoint(ctx)
	if endpoint == "" {
		return SearchResponse{}, errors.New("SearXNG endpoint not configured. Set searxng.endpoint in settings or SEARXNG_ENDPOINT in environment.")
	}
	authHeader, err := findSearXNGAuth(ctx)
	if err != nil {
		return SearchResponse{}, err
	}

	categories, _ := settingString(ctx, "searxng.categories")
	engines, _ := settingString(ctx, "searxng.engines")
	language, _ := settingString(ctx, "searxng.language")
	safesearchVal, hasSafesearch := settingFloat(ctx, "searxng.safesearch")
	if hasSafesearch && safesearchVal != 0 && safesearchVal != 1 && safesearchVal != 2 {
		return SearchResponse{}, errors.New("searxng.safesearch must be 0 (off), 1 (moderate), or 2 (strict).")
	}

	numResults := clampNumResults(params.requestedResults(), 10, 20)
	parsed := parsedOrParse(params)
	query := stripExternalBangs(FormatScraperQuery(params.Query, &parsed, GoogleQuerySyntax))

	base := strings.TrimRight(endpoint, "/")
	u, err := url.Parse(base + "/search")
	if err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "searxng", Message: fmt.Sprintf("Invalid SearXNG endpoint: %v", err), Status: 400}
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("pageno", "1")
	if params.Recency != "" {
		if tr, ok := searxngRecencyMap[params.Recency]; ok {
			q.Set("time_range", tr)
		}
	}
	if strings.TrimSpace(categories) != "" {
		q.Set("categories", strings.TrimSpace(categories))
	}
	if strings.TrimSpace(engines) != "" {
		q.Set("engines", strings.TrimSpace(engines))
	}
	if hasSafesearch {
		q.Set("safesearch", strconv.Itoa(int(safesearchVal)))
	}
	if parsed.Lang != "" {
		language = parsed.Lang
	}
	if strings.TrimSpace(language) != "" {
		q.Set("language", strings.TrimSpace(language))
	}
	u.RawQuery = q.Encode()

	headers := map[string]string{"Accept": "application/json"}
	if authHeader != "" {
		headers["Authorization"] = authHeader
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodGet, u.String(), headers, nil)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "searxng", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("searxng", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "searxng",
			Message:  fmt.Sprintf("SearXNG API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "searxng", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Results []struct {
			Title          string `json:"title"`
			URL            string `json:"url"`
			Content        string `json:"content"`
			Snippet        string `json:"snippet"`
			PublishedDate  string `json:"publishedDate"`
			PublishedDate2 string `json:"published_date"`
		} `json:"results"`
		Answers             []any      `json:"answers"`
		Suggestions         []string   `json:"suggestions"`
		UnresponsiveEngines [][]string `json:"unresponsive_engines"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "searxng", Message: "SearXNG API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := strings.TrimSpace(r.Content)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Snippet)
		}
		pub := r.PublishedDate
		if pub == "" {
			pub = r.PublishedDate2
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       snippet,
			PublishedDate: pub,
			AgeSeconds:    dateToAgeSeconds(pub),
		})
		if len(sources) >= numResults {
			break
		}
	}

	if len(sources) == 0 && len(payload.UnresponsiveEngines) > 0 {
		var parts []string
		for _, pair := range payload.UnresponsiveEngines {
			if len(pair) >= 2 {
				parts = append(parts, pair[0]+": "+pair[1])
			} else if len(pair) == 1 {
				parts = append(parts, pair[0])
			}
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "searxng",
			Message:  "SearXNG returned no usable results; upstream engines failed: " + strings.Join(parts, "; "),
			Status:   503,
		}
	}

	var answerParts []string
	for _, a := range payload.Answers {
		if text := extractSearXNGAnswerText(a); text != "" {
			answerParts = append(answerParts, text)
			if len(answerParts) == 3 {
				break
			}
		}
	}

	return SearchResponse{
		Provider:         "searxng",
		Answer:           strings.Join(answerParts, "\n\n"),
		Sources:          sources,
		RelatedQuestions: payload.Suggestions,
	}, nil
}

// =============================================================================
// 5. Perplexity (PERPLEXITY_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/perplexity.ts
// =============================================================================

type perplexityProvider struct{}

func (perplexityProvider) ID() string        { return "perplexity" }
func (perplexityProvider) Label() string     { return "Perplexity" }
func (perplexityProvider) IsAvailable() bool { return envTrimmed("PERPLEXITY_API_KEY") != "" }

var (
	perplexityQuerySyntax = QuerySyntax{
		Phrases:  true,
		Negation: true,
		Or:       true,
		InURL:    true,
		InTitle:  true,
		Filetype: true,
	}
	twoLetterLangRe = regexp.MustCompile(`^([a-z]{2})(?:[-_]|$)`)
)

func toPerplexityDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	mo, _ := strconv.Atoi(parts[1])
	d, _ := strconv.Atoi(parts[2])
	return fmt.Sprintf("%d/%d/%s", mo, d, parts[0])
}

func (perplexityProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("PERPLEXITY_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{Provider: "perplexity", Message: "Perplexity credentials not found. Set PERPLEXITY_API_KEY.", Status: 401}
	}

	parsed := parsedOrParse(params)
	query := params.Query
	var domainFilter []string
	var afterDate, beforeDate string
	var langFilter []string
	if parsed.HasDirectives {
		query = FormatQuery(parsed, perplexityQuerySyntax)
		seen := map[string]struct{}{}
		for _, h := range siteHosts(parsed.Sites) {
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				domainFilter = append(domainFilter, h)
			}
		}
		for _, h := range siteHosts(parsed.ExcludedSites) {
			entry := "-" + h
			if _, ok := seen[entry]; !ok {
				seen[entry] = struct{}{}
				domainFilter = append(domainFilter, entry)
			}
		}
		if len(domainFilter) > 20 {
			domainFilter = domainFilter[:20]
		}
		if parsed.After != "" {
			afterDate = toPerplexityDate(parsed.After)
		}
		if parsed.Before != "" {
			beforeDate = toPerplexityDate(parsed.Before)
		}
		if parsed.Lang != "" {
			if m := twoLetterLangRe.FindStringSubmatch(parsed.Lang); len(m) > 1 {
				langFilter = []string{m[1]}
			}
		}
	}

	var messages []map[string]string
	if params.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": params.SystemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": query})

	model := envTrimmed("PI_PERPLEXITY_API_MODEL")
	if model == "" {
		model = "sonar-pro"
	}
	maxTokens := params.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	temp := 0.2
	if params.Temperature != nil {
		temp = *params.Temperature
	}
	numSearchResults := params.NumSearchResults
	if numSearchResults <= 0 {
		numSearchResults = 20
	}

	reqBody := map[string]any{
		"model":                    model,
		"messages":                 messages,
		"stream":                   false,
		"max_tokens":               maxTokens,
		"temperature":              temp,
		"search_mode":              "web",
		"num_search_results":       numSearchResults,
		"web_search_options":       map[string]string{"search_type": "pro", "search_context_size": "high"},
		"enable_search_classifier": true,
		"reasoning_effort":         "medium",
		"language_preference":      "en",
		"return_related_questions": true,
	}
	if len(domainFilter) > 0 {
		reqBody["search_domain_filter"] = domainFilter
	}
	if afterDate != "" {
		reqBody["search_after_date_filter"] = afterDate
	}
	if beforeDate != "" {
		reqBody["search_before_date_filter"] = beforeDate
	}
	if len(langFilter) > 0 {
		reqBody["search_language_filter"] = langFilter
	}
	if params.Recency != "" && afterDate == "" && beforeDate == "" {
		reqBody["search_recency_filter"] = params.Recency
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, perplexityChatURL, map[string]string{
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, reqBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "perplexity", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("perplexity", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "perplexity",
			Message:  fmt.Sprintf("Perplexity API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "perplexity", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Citations     []string `json:"citations"`
		SearchResults []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Date    string `json:"date"`
			Snippet string `json:"snippet"`
		} `json:"search_results"`
		RelatedQuestions []string `json:"related_questions"`
		Usage            *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "perplexity", Message: "Perplexity API returned invalid JSON", Status: 500}
	}

	answer := ""
	if len(payload.Choices) > 0 {
		answer = payload.Choices[0].Message.Content
	}

	var sources []SearchSource
	var citations []SearchCitation
	if len(payload.Citations) > 0 {
		for _, cu := range payload.Citations {
			if cu == "" {
				continue
			}
			title := cu
			var snippet, pubDate string
			for _, sr := range payload.SearchResults {
				if sr.URL == cu {
					if sr.Title != "" {
						title = sr.Title
					}
					snippet = sr.Snippet
					pubDate = sr.Date
					break
				}
			}
			sources = append(sources, SearchSource{
				Title:         title,
				URL:           cu,
				Snippet:       snippet,
				PublishedDate: pubDate,
				AgeSeconds:    dateToAgeSeconds(pubDate),
			})
			citations = append(citations, SearchCitation{URL: cu, Title: title})
		}
	} else {
		for _, sr := range payload.SearchResults {
			if sr.URL == "" {
				continue
			}
			title := sr.Title
			if title == "" {
				title = sr.URL
			}
			sources = append(sources, SearchSource{
				Title:         title,
				URL:           sr.URL,
				Snippet:       sr.Snippet,
				PublishedDate: sr.Date,
				AgeSeconds:    dateToAgeSeconds(sr.Date),
			})
		}
	}
	if params.Limit > 0 && len(sources) > params.Limit {
		sources = sources[:params.Limit]
	}

	var related []string
	for _, rq := range payload.RelatedQuestions {
		if strings.TrimSpace(rq) != "" {
			related = append(related, rq)
		}
	}

	var usage *SearchUsage
	if payload.Usage != nil && (payload.Usage.PromptTokens != 0 || payload.Usage.CompletionTokens != 0 || payload.Usage.TotalTokens != 0) {
		usage = &SearchUsage{
			InputTokens:  payload.Usage.PromptTokens,
			OutputTokens: payload.Usage.CompletionTokens,
			TotalTokens:  payload.Usage.TotalTokens,
		}
	}

	return SearchResponse{
		Provider:         "perplexity",
		Answer:           answer,
		Sources:          sources,
		Citations:        citations,
		RelatedQuestions: related,
		Usage:            usage,
		Model:            payload.Model,
		RequestID:        payload.ID,
		AuthMode:         "api_key",
	}, nil
}

// =============================================================================
// 6. TinyFish (TINYFISH_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/tinyfish.ts
// =============================================================================

type tinyfishProvider struct{}

func (tinyfishProvider) ID() string        { return "tinyfish" }
func (tinyfishProvider) Label() string     { return "TinyFish" }
func (tinyfishProvider) IsAvailable() bool { return envTrimmed("TINYFISH_API_KEY") != "" }

var (
	tinyfishQuerySyntax = QuerySyntax{Phrases: true, Negation: true, Filetype: true}
	tinyfishRecencyMins = map[string]int{
		"day":   1440,
		"week":  10080,
		"month": 43200,
		"year":  525600,
	}
	tinyfishLocaleRe = regexp.MustCompile(`^([a-z]{2})(?:[-_]([a-z]{2}))?(?:[-_]|$)`)
)

func (tinyfishProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("TINYFISH_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "tinyfish",
			Message:  `TinyFish credentials not found. Set TINYFISH_API_KEY or configure an API key for provider "tinyfish".`,
			Status:   401,
		}
	}

	numResults := clampNumResults(params.requestedResults(), 10, 20)
	pageSize := min(numResults, 10)
	parsed := parsedOrParse(params)
	query := params.Query
	var includeDomains, excludeDomains []string
	if parsed.HasDirectives {
		query = FormatQuery(parsed, tinyfishQuerySyntax)
		includeDomains = siteHosts(parsed.Sites)
		excludeDomains = siteHosts(parsed.ExcludedSites)
	}
	var lang, loc string
	if parsed.Lang != "" {
		if m := tinyfishLocaleRe.FindStringSubmatch(strings.ToLower(parsed.Lang)); m != nil {
			lang = m[1]
			if m[2] != "" {
				loc = strings.ToUpper(m[2])
			}
		}
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	var collected []SearchSource
	seen := map[string]struct{}{}
	for page := 0; page <= 10 && len(collected) < numResults; page++ {
		u, err := url.Parse(tinyfishSearchURL)
		if err != nil {
			return SearchResponse{}, err
		}
		q := u.Query()
		q.Set("query", query)
		q.Set("num_results", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))
		if params.Recency != "" {
			if mins, ok := tinyfishRecencyMins[params.Recency]; ok {
				q.Set("recency_minutes", strconv.Itoa(mins))
			}
		}
		if len(includeDomains) > 0 {
			q.Set("include_domains", strings.Join(includeDomains, ","))
		}
		if len(excludeDomains) > 0 {
			q.Set("exclude_domains", strings.Join(excludeDomains, ","))
		}
		if loc != "" {
			q.Set("location", loc)
		}
		if lang != "" {
			q.Set("language", lang)
		}
		u.RawQuery = q.Encode()

		resp, err := doJSONRequest(tctx, client, http.MethodGet, u.String(), map[string]string{
			"Accept":    "application/json",
			"X-API-Key": apiKey,
		}, nil)
		if err != nil {
			return SearchResponse{}, err
		}
		status := resp.StatusCode
		raw, readErr := readLimitedText(resp, "tinyfish", maxProviderRespBytes, status < 200 || status >= 300)
		resp.Body.Close()
		if readErr != nil {
			return SearchResponse{}, readErr
		}
		if status < 200 || status >= 300 {
			if classified := classifyProviderHttpError("tinyfish", status, raw); classified != nil {
				return SearchResponse{}, classified
			}
			return SearchResponse{}, &SearchProviderError{
				Provider: "tinyfish",
				Message:  fmt.Sprintf("TinyFish API error (%d): %s", status, raw),
				Status:   status,
			}
		}

		var pageData struct {
			Results []struct {
				Title    string `json:"title"`
				URL      string `json:"url"`
				Snippet  string `json:"snippet"`
				SiteName string `json:"site_name"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(raw), &pageData); err != nil || pageData.Results == nil {
			return SearchResponse{}, &SearchProviderError{
				Provider: "tinyfish",
				Message:  "TinyFish Search API returned an unexpected response shape",
				Status:   500,
			}
		}
		for _, r := range pageData.Results {
			uStr := strings.TrimSpace(r.URL)
			if uStr == "" {
				continue
			}
			if _, dup := seen[uStr]; dup {
				continue
			}
			seen[uStr] = struct{}{}
			siteName := strings.TrimSpace(r.SiteName)
			title := strings.TrimSpace(r.Title)
			if title == "" {
				title = siteName
			}
			if title == "" {
				title = uStr
			}
			collected = append(collected, SearchSource{
				Title:   title,
				URL:     uStr,
				Snippet: normalizeSearchText(r.Snippet),
				Author:  siteName,
			})
		}
		if len(pageData.Results) < pageSize {
			break
		}
	}
	if len(collected) > numResults {
		collected = collected[:numResults]
	}

	return SearchResponse{
		Provider: "tinyfish",
		Sources:  collected,
		AuthMode: "api_key",
	}, nil
}

// =============================================================================
// 7. Jina (JINA_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/jina.ts
// =============================================================================

type jinaProvider struct{}

func (jinaProvider) ID() string        { return "jina" }
func (jinaProvider) Label() string     { return "Jina" }
func (jinaProvider) IsAvailable() bool { return envTrimmed("JINA_API_KEY") != "" }

type jinaResultItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

func (jinaProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("JINA_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "jina",
			Message:  `Jina credentials not found. Set JINA_API_KEY or configure an API key for provider "jina".`,
			Status:   401,
		}
	}
	numResults := clampNumResults(params.requestedResults(), 5, 20)
	parsed := parsedOrParse(params)
	query := params.Query
	siteHeader := ""
	if parsed.HasDirectives {
		if len(parsed.Sites) == 1 {
			siteHeader = strings.SplitN(parsed.Sites[0], "/", 2)[0]
		}
		query = FormatQuery(parsed, QuerySyntax{
			Phrases:  true,
			Negation: true,
			Site:     siteHeader == "",
			InTitle:  true,
			InURL:    true,
			Filetype: true,
		})
	}

	reqURL := strings.TrimRight(jinaSearchURL, "/") + "/" + url.PathEscape(query) + "?count=" + strconv.Itoa(numResults)
	headers := map[string]string{
		"Accept":          "application/json",
		"Authorization":   "Bearer " + apiKey,
		"X-Respond-With":  "no-content",
		"X-Retain-Images": "none",
	}
	if siteHeader != "" {
		headers["X-Site"] = siteHeader
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodGet, reqURL, headers, nil)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "jina", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("jina", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "jina",
			Message:  fmt.Sprintf("Jina API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "jina", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var items []jinaResultItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		var envelope struct {
			Code *int             `json:"code"`
			Data []jinaResultItem `json:"data"`
		}
		if err2 := json.Unmarshal([]byte(raw), &envelope); err2 != nil {
			return SearchResponse{}, &SearchProviderError{Provider: "jina", Message: "Jina API returned invalid response: expected an object or array"}
		}
		if envelope.Code != nil && *envelope.Code != 200 {
			return SearchResponse{}, &SearchProviderError{
				Provider: "jina",
				Message:  fmt.Sprintf("Jina API response reported failure (%d)", *envelope.Code),
				Status:   *envelope.Code,
			}
		}
		if envelope.Data == nil {
			return SearchResponse{}, &SearchProviderError{Provider: "jina", Message: "Jina API returned invalid response: expected data array"}
		}
		items = envelope.Data
	}

	sources := make([]SearchSource, 0, len(items))
	for _, r := range items {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		snippet := strings.TrimSpace(r.Description)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Content)
		}
		sources = append(sources, SearchSource{
			Title:   title,
			URL:     r.URL,
			Snippet: snippet,
		})
		if len(sources) >= numResults {
			break
		}
	}

	return SearchResponse{
		Provider: "jina",
		Sources:  sources,
		AuthMode: "api_key",
	}, nil
}

// =============================================================================
// 8. Kagi (KAGI_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/kagi.ts, web/kagi.ts
// =============================================================================

type kagiProvider struct{}

func (kagiProvider) ID() string        { return "kagi" }
func (kagiProvider) Label() string     { return "Kagi" }
func (kagiProvider) IsAvailable() bool { return envTrimmed("KAGI_API_KEY") != "" }

func kagiRecencyToDate(recency string) string {
	d := nowFunc().UTC()
	switch recency {
	case "day":
		d = d.AddDate(0, 0, -1)
	case "week":
		d = d.AddDate(0, 0, -7)
	case "month":
		d = d.AddDate(0, -1, 0)
	case "year":
		d = d.AddDate(-1, 0, 0)
	default:
		return ""
	}
	return d.Format("2006-01-02")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

type kagiBucketItem struct {
	URL         string         `json:"url"`
	Href        string         `json:"href"`
	Link        string         `json:"link"`
	Title       string         `json:"title"`
	Name        string         `json:"name"`
	Snippet     string         `json:"snippet"`
	Description string         `json:"description"`
	Summary     string         `json:"summary"`
	Time        string         `json:"time"`
	Props       map[string]any `json:"props"`
}

func appendKagiBucket(sources *[]SearchSource, items []kagiBucketItem, tag string) {
	for _, item := range items {
		u := firstNonEmpty(item.URL, item.Href, item.Link)
		if u == "" {
			continue
		}
		title := firstNonEmpty(item.Title, item.Name)
		if title == "" {
			title = u
		}
		if tag != "" {
			title = tag + " " + title
		}
		pub := firstNonEmpty(item.Time)
		*sources = append(*sources, SearchSource{
			Title:         title,
			URL:           u,
			Snippet:       firstNonEmpty(item.Snippet, item.Description, item.Summary),
			PublishedDate: pub,
			AgeSeconds:    dateToAgeSeconds(pub),
		})
	}
}

func kagiQuestionOf(item kagiBucketItem) string {
	if item.Props != nil {
		if q, ok := item.Props["question"].(string); ok && strings.TrimSpace(q) != "" {
			return strings.TrimSpace(q)
		}
		if q, ok := item.Props["query"].(string); ok && strings.TrimSpace(q) != "" {
			return strings.TrimSpace(q)
		}
	}
	return strings.TrimSpace(item.Title)
}

func extractKagiError(m map[string]any) (string, int) {
	for _, k := range []string{"message", "detail"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), 0
		}
	}
	for _, k := range []string{"error", "errors"} {
		switch v := m[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), 0
			}
		case []any:
			for _, entry := range v {
				em, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				code := 0
				if c, ok := em["code"].(float64); ok {
					code = int(c)
				}
				for _, fk := range []string{"message", "msg"} {
					if s, ok := em[fk].(string); ok && strings.TrimSpace(s) != "" {
						return strings.TrimSpace(s), code
					}
				}
				if code != 0 {
					return strconv.Itoa(code), code
				}
			}
		}
	}
	return "", 0
}

func (kagiProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("KAGI_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "kagi",
			Message:  "Kagi credentials not found. Set KAGI_API_KEY or login with 'omp /login kagi'.",
			Status:   401,
		}
	}

	numResults := clampNumResults(params.requestedResults(), 10, 40)
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, GoogleQuerySyntax)
	}

	reqBody := map[string]any{
		"query":    query,
		"workflow": "search",
		"limit":    numResults,
	}
	if params.Recency != "" {
		if after := kagiRecencyToDate(params.Recency); after != "" {
			reqBody["filters"] = map[string]string{"after": after}
		}
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, kagiSearchURL, map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}, reqBody)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "kagi", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("kagi", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		trimmed := strings.TrimSpace(errText)
		detail := trimmed
		var em map[string]any
		if trimmed != "" && json.Unmarshal([]byte(trimmed), &em) == nil {
			if msg, _ := extractKagiError(em); msg != "" {
				detail = msg
			}
		}
		msg := fmt.Sprintf("Kagi API error (%d)", resp.StatusCode)
		if detail != "" {
			msg = fmt.Sprintf("Kagi API error (%d): %s", resp.StatusCode, detail)
		}
		return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: msg, Status: resp.StatusCode}
	}

	raw, err := readLimitedText(resp, "kagi", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var generic any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: "Kagi API returned an invalid response: invalid JSON", Status: resp.StatusCode}
	}
	obj, ok := generic.(map[string]any)
	if !ok {
		return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: "Kagi API returned an invalid response: expected an object envelope", Status: resp.StatusCode}
	}
	if _, hasErr := obj["error"]; hasErr {
		if msg, code := extractKagiError(obj); msg != "" {
			if code == 0 {
				code = resp.StatusCode
			}
			return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: fmt.Sprintf("Kagi API error (%d): %s", code, msg), Status: code}
		}
	}
	if _, hasErrs := obj["errors"]; hasErrs {
		if msg, code := extractKagiError(obj); msg != "" {
			if code == 0 {
				code = resp.StatusCode
			}
			return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: fmt.Sprintf("Kagi API error (%d): %s", code, msg), Status: code}
		}
	}

	var payload struct {
		Meta *struct {
			Trace string `json:"trace"`
			ID    string `json:"id"`
		} `json:"meta"`
		Data *struct {
			Search           []kagiBucketItem `json:"search"`
			Video            []kagiBucketItem `json:"video"`
			News             []kagiBucketItem `json:"news"`
			Infobox          []kagiBucketItem `json:"infobox"`
			AdjacentQuestion []kagiBucketItem `json:"adjacent_question"`
			RelatedSearch    []kagiBucketItem `json:"related_search"`
			DirectAnswer     []kagiBucketItem `json:"direct_answer"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "kagi", Message: "Kagi API returned an invalid response: expected data to be an object", Status: resp.StatusCode}
	}

	var sources []SearchSource
	var related []string
	var answer string
	if payload.Data != nil {
		appendKagiBucket(&sources, payload.Data.Search, "")
		appendKagiBucket(&sources, payload.Data.Video, "[Video]")
		appendKagiBucket(&sources, payload.Data.News, "[News]")
		appendKagiBucket(&sources, payload.Data.Infobox, "[Info]")
		for _, item := range payload.Data.AdjacentQuestion {
			if q := kagiQuestionOf(item); q != "" {
				related = append(related, q)
			}
		}
		for _, item := range payload.Data.RelatedSearch {
			if q := kagiQuestionOf(item); q != "" {
				related = append(related, q)
			}
		}
		if len(payload.Data.DirectAnswer) > 0 {
			answer = firstNonEmpty(payload.Data.DirectAnswer[0].Snippet, payload.Data.DirectAnswer[0].Title)
		}
	}
	if len(sources) > numResults {
		sources = sources[:numResults]
	}

	reqID := ""
	if payload.Meta != nil {
		reqID = firstNonEmpty(payload.Meta.Trace, payload.Meta.ID)
	}

	return SearchResponse{
		Provider:         "kagi",
		Answer:           answer,
		Sources:          sources,
		RelatedQuestions: related,
		RequestID:        reqID,
		AuthMode:         "api_key",
	}, nil
}

// =============================================================================
// 9. Tavily (TAVILY_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/tavily.ts
// =============================================================================

type tavilyProvider struct{}

func (tavilyProvider) ID() string        { return "tavily" }
func (tavilyProvider) Label() string     { return "Tavily" }
func (tavilyProvider) IsAvailable() bool { return envTrimmed("TAVILY_API_KEY") != "" }

type tavilyRequest struct {
	query          string
	numResults     int
	recency        string
	includeDomains []string
	excludeDomains []string
	startDate      string
	endDate        string
}

func buildTavilyBody(r tavilyRequest) map[string]any {
	body := map[string]any{
		"query":               r.query,
		"search_depth":        "basic",
		"max_results":         clampNumResults(r.numResults, 5, 20),
		"include_answer":      "advanced",
		"include_raw_content": false,
	}
	if len(r.includeDomains) > 0 {
		body["include_domains"] = r.includeDomains
	}
	if len(r.excludeDomains) > 0 {
		body["exclude_domains"] = r.excludeDomains
	}
	if r.startDate != "" {
		body["start_date"] = r.startDate
	}
	if r.endDate != "" {
		body["end_date"] = r.endDate
	}
	if r.recency != "" && r.startDate == "" && r.endDate == "" {
		body["time_range"] = r.recency
	}
	return body
}

func extractTavilyError(val any) string {
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, k := range []string{"detail", "error", "message"} {
			if msg := extractTavilyError(v[k]); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func callTavilyOnce(ctx context.Context, client *http.Client, apiKey string, req tavilyRequest) (SearchResponse, error) {
	numResults := clampNumResults(req.numResults, 5, 20)
	resp, err := doJSONRequest(ctx, client, http.MethodPost, tavilySearchURL, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, buildTavilyBody(req))
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "tavily", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("tavily", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		msg := strings.TrimSpace(errText)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		} else {
			var parsed any
			if json.Unmarshal([]byte(errText), &parsed) == nil {
				if extracted := extractTavilyError(parsed); extracted != "" {
					msg = extracted
				}
			}
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "tavily",
			Message:  fmt.Sprintf("Tavily API error (%d): %s", resp.StatusCode, msg),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "tavily", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Answer    string `json:"answer"`
		RequestID string `json:"request_id"`
		Results   []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "tavily", Message: "Tavily API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       r.Content,
			PublishedDate: r.PublishedDate,
			AgeSeconds:    dateToAgeSeconds(r.PublishedDate),
		})
		if len(sources) >= numResults {
			break
		}
	}

	return SearchResponse{
		Provider:  "tavily",
		Answer:    strings.TrimSpace(payload.Answer),
		Sources:   sources,
		RequestID: payload.RequestID,
		AuthMode:  "api_key",
	}, nil
}

func (tavilyProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("TAVILY_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "tavily",
			Message:  `Tavily credentials not found. Set TAVILY_API_KEY or configure an API key for provider "tavily".`,
			Status:   401,
		}
	}

	parsed := parsedOrParse(params)
	treq := tavilyRequest{
		query:      params.Query,
		numResults: params.requestedResults(),
		recency:    params.Recency,
	}
	if parsed.HasDirectives {
		treq.query = FormatQuery(parsed, QuerySyntax{Phrases: true, Negation: true})
		treq.includeDomains = siteHosts(parsed.Sites)
		treq.excludeDomains = siteHosts(parsed.ExcludedSites)
		treq.startDate = parsed.After
		treq.endDate = parsed.Before
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	res, err := callTavilyOnce(tctx, client, apiKey, treq)
	if err != nil {
		return SearchResponse{}, err
	}
	hasTimeFilter := treq.recency != "" || treq.startDate != "" || treq.endDate != ""
	if !hasTimeFilter || res.Answer != "" || len(res.Sources) > 0 {
		return res, nil
	}

	// Time filters commonly zero out results; retry once without them.
	// upstream: packages/coding-agent/src/web/search/providers/tavily.ts:216-220
	treq.recency = ""
	treq.startDate = ""
	treq.endDate = ""
	return callTavilyOnce(tctx, client, apiKey, treq)
}

// =============================================================================
// 10. Brave (BRAVE_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/brave.ts
// =============================================================================

type braveProvider struct{}

func (braveProvider) ID() string        { return "brave" }
func (braveProvider) Label() string     { return "Brave" }
func (braveProvider) IsAvailable() bool { return envTrimmed("BRAVE_API_KEY") != "" }

var (
	braveQuerySyntax = QuerySyntax{
		Phrases: true, Negation: true, Or: true,
		Site: true, InURL: true, InTitle: true, InText: true, Filetype: true,
	}
	braveRecencyMap = map[string]string{
		"day":   "pd",
		"week":  "pw",
		"month": "pm",
		"year":  "py",
	}
	htmlTagStripRe = regexp.MustCompile(`<[^>]*>`)
)

func braveFreshness(parsed StructuredQuery, recency string) string {
	if parsed.After != "" || parsed.Before != "" {
		start := parsed.After
		if start == "" {
			start = "1970-01-01"
		}
		end := parsed.Before
		if end == "" {
			end = nowFunc().UTC().Format("2006-01-02")
		}
		return start + "to" + end
	}
	return braveRecencyMap[recency]
}

func braveNormalizeText(val string, maxLen int) string {
	text := normalizeSearchText(htmlTagStripRe.ReplaceAllString(val, " "))
	if text == "" {
		return ""
	}
	return truncateText(text, maxLen)
}

func isHTTPURL(raw string) (string, bool) {
	if len(raw) == 0 || len(raw) > 2048 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

func (braveProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("BRAVE_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "brave",
			Message:  `Brave credentials not found. Set BRAVE_API_KEY or configure an API key for provider "brave".`,
			Status:   401,
		}
	}

	numResults := clampNumResults(params.requestedResults(), 10, 20)
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, braveQuerySyntax)
	}
	if len([]rune(query)) > 500 {
		return SearchResponse{}, &SearchProviderError{
			Provider: "brave",
			Message:  "Brave search queries cannot exceed 500 characters",
			Status:   400,
		}
	}

	u, err := url.Parse(braveSearchURL)
	if err != nil {
		return SearchResponse{}, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(numResults))
	q.Set("extra_snippets", "true")
	q.Set("text_decorations", "false")
	q.Set("safesearch", "moderate")
	if freshness := braveFreshness(parsed, params.Recency); freshness != "" {
		q.Set("freshness", freshness)
	}
	u.RawQuery = q.Encode()

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodGet, u.String(), map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": apiKey,
	}, nil)
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "brave", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("brave", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "brave",
			Message:  fmt.Sprintf("Brave API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "brave", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Web *struct {
			Results []struct {
				Title         string   `json:"title"`
				URL           string   `json:"url"`
				Description   string   `json:"description"`
				ExtraSnippets []string `json:"extra_snippets"`
				Age           string   `json:"age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "brave", Message: "Brave API returned invalid JSON", Status: 500}
	}

	var sources []SearchSource
	if payload.Web != nil {
		for _, r := range payload.Web.Results {
			normURL, ok := isHTTPURL(r.URL)
			if !ok {
				continue
			}
			title := braveNormalizeText(r.Title, 300)
			if title == "" {
				title = normURL
			}
			seenSnip := map[string]struct{}{}
			var snips []string
			if desc := braveNormalizeText(r.Description, 8000); desc != "" {
				seenSnip[desc] = struct{}{}
				snips = append(snips, desc)
			}
			for _, ex := range r.ExtraSnippets {
				if s := braveNormalizeText(ex, 8000); s != "" {
					if _, dup := seenSnip[s]; !dup {
						seenSnip[s] = struct{}{}
						snips = append(snips, s)
					}
				}
			}
			snippet := truncateText(strings.Join(snips, "\n"), 8000)
			pubDate := braveNormalizeText(r.Age, 100)
			sources = append(sources, SearchSource{
				Title:         title,
				URL:           normURL,
				Snippet:       snippet,
				PublishedDate: pubDate,
				AgeSeconds:    dateToAgeSeconds(pubDate),
			})
			if len(sources) >= numResults {
				break
			}
		}
	}

	reqID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("request-id"))
	return SearchResponse{
		Provider:  "brave",
		Sources:   sources,
		RequestID: reqID,
		AuthMode:  "api_key",
	}, nil
}

// =============================================================================
// 11. DeepSeek (DEEPSEEK_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/deepseek.ts
// =============================================================================

type deepseekProvider struct{}

func (deepseekProvider) ID() string        { return "deepseek" }
func (deepseekProvider) Label() string     { return "DeepSeek" }
func (deepseekProvider) IsAvailable() bool { return envTrimmed("DEEPSEEK_API_KEY") != "" }

func buildDeepSeekQuery(parsed StructuredQuery, recency string) string {
	after := parsed.After
	if after == "" && recency != "" {
		after = recencyAfterDate(recency)
	}
	if !parsed.HasDirectives && after == "" {
		return strings.TrimSpace(parsed.Raw)
	}
	stripped := parsed
	stripped.Raw = ""
	stripped.Sites = nil
	stripped.ExcludedSites = nil
	stripped.After = ""
	stripped.Before = ""
	text := FormatQuery(stripped, QuerySyntax{Phrases: true, Negation: true})
	var parts []string
	if text != "" {
		parts = append(parts, text)
	}
	if sites := siteHosts(parsed.Sites); len(sites) > 0 {
		clauses := make([]string, len(sites))
		for i, s := range sites {
			clauses[i] = "site:" + s
		}
		parts = append(parts, strings.Join(clauses, " OR "))
	}
	for _, s := range siteHosts(parsed.ExcludedSites) {
		parts = append(parts, "-site:"+s)
	}
	if parsed.Lang != "" {
		lang := strings.SplitN(parsed.Lang, "-", 2)[0]
		if lang != "" {
			parts = append(parts, "lang:"+lang)
		}
	}
	if after != "" {
		parts = append(parts, "after:"+after)
	}
	if parsed.Before != "" {
		parts = append(parts, "before:"+parsed.Before)
	}
	return strings.Join(parts, " ")
}

func (deepseekProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("DEEPSEEK_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "deepseek",
			Message:  `DeepSeek credentials not found. Set DEEPSEEK_API_KEY or configure an API key for provider "deepseek".`,
			Status:   401,
		}
	}
	count := clampNumResults(params.requestedResults(), 10, 50)
	parsed := parsedOrParse(params)
	query := buildDeepSeekQuery(parsed, params.Recency)
	if len([]rune(query)) > 128 {
		return SearchResponse{}, &SearchProviderError{
			Provider: "deepseek",
			Message:  "DeepSeek search queries cannot exceed 128 characters",
			Status:   400,
		}
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, deepseekSearchURL, map[string]string{
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, map[string]any{"query": query, "count": count})
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "deepseek", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("deepseek", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "deepseek",
			Message:  fmt.Sprintf("DeepSeek search API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "deepseek", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Summary string `json:"summary"`
			Date    string `json:"date"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "deepseek", Message: "DeepSeek search API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		normURL, ok := isHTTPURL(r.URL)
		if !ok {
			continue
		}
		title := normalizeSearchText(r.Title)
		if title == "" {
			title = normURL
		}
		snippet := normalizeSearchText(r.Summary)
		if snippet == "" {
			snippet = normalizeSearchText(r.Snippet)
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           normURL,
			Snippet:       snippet,
			PublishedDate: r.Date,
			AgeSeconds:    dateToAgeSeconds(r.Date),
		})
		if len(sources) >= count {
			break
		}
	}

	return SearchResponse{
		Provider: "deepseek",
		Sources:  sources,
		AuthMode: "api_key",
	}, nil
}

// =============================================================================
// 12. Kimi (MOONSHOT_SEARCH_API_KEY / KIMI_SEARCH_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/kimi.ts
// =============================================================================

type kimiProvider struct{}

func (kimiProvider) ID() string    { return "kimi" }
func (kimiProvider) Label() string { return "Kimi" }
func (kimiProvider) IsAvailable() bool {
	return firstNonEmpty(envTrimmed("MOONSHOT_SEARCH_API_KEY"), envTrimmed("KIMI_SEARCH_API_KEY")) != ""
}

var kimiQuerySyntax = QuerySyntax{
	Phrases: true, Negation: true, Site: true, InTitle: true, InURL: true, Filetype: true,
}

func (kimiProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := firstNonEmpty(envTrimmed("MOONSHOT_SEARCH_API_KEY"), envTrimmed("KIMI_SEARCH_API_KEY"))
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "kimi",
			Message:  "Kimi search credentials not found. Kimi web search uses the Kimi Code service (api.kimi.com); set MOONSHOT_SEARCH_API_KEY / KIMI_SEARCH_API_KEY to a Kimi Code Console key, or login with 'omp /login kimi-code'. A Moonshot Open Platform key (MOONSHOT_API_KEY) is not accepted here.",
			Status:   401,
		}
	}

	endpoint := firstNonEmpty(envTrimmed("MOONSHOT_SEARCH_BASE_URL"), envTrimmed("KIMI_SEARCH_BASE_URL"), kimiSearchURL)
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, kimiQuerySyntax)
	}
	limit := clampNumResults(params.requestedResults(), 10, 20)

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, endpoint, map[string]string{
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, map[string]any{
		"text_query":           query,
		"limit":                limit,
		"enable_page_crawling": false,
		"timeout_seconds":      30,
	})
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "kimi", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("kimi", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "kimi",
			Message:  fmt.Sprintf("Kimi search API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "kimi", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		SearchResults []struct {
			SiteName string `json:"site_name"`
			Title    string `json:"title"`
			URL      string `json:"url"`
			Snippet  string `json:"snippet"`
			Content  string `json:"content"`
			Date     string `json:"date"`
		} `json:"search_results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "kimi", Message: "Kimi search API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.SearchResults))
	for _, r := range payload.SearchResults {
		if r.URL == "" {
			continue
		}
		title := firstNonEmpty(r.Title, r.URL)
		snippet := firstNonEmpty(r.Snippet, r.Content)
		pub := strings.TrimSpace(r.Date)
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       snippet,
			PublishedDate: pub,
			AgeSeconds:    dateToAgeSeconds(pub),
			Author:        strings.TrimSpace(r.SiteName),
		})
		if len(sources) >= limit {
			break
		}
	}

	reqID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("x-msh-request-id"))
	return SearchResponse{
		Provider:  "kimi",
		Sources:   sources,
		RequestID: reqID,
		AuthMode:  "api_key",
	}, nil
}

// =============================================================================
// 13. Synthetic (SYNTHETIC_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/synthetic.ts
// =============================================================================

type syntheticProvider struct{}

func (syntheticProvider) ID() string        { return "synthetic" }
func (syntheticProvider) Label() string     { return "Synthetic" }
func (syntheticProvider) IsAvailable() bool { return envTrimmed("SYNTHETIC_API_KEY") != "" }

func (syntheticProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("SYNTHETIC_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "synthetic",
			Message:  "Synthetic credentials not found. Set SYNTHETIC_API_KEY or login with 'omp /login synthetic'.",
			Status:   401,
		}
	}
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, QuerySyntax{Phrases: true, Negation: true, Site: true})
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, syntheticSearchURL, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, map[string]string{"query": query})
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "synthetic", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("synthetic", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "synthetic",
			Message:  fmt.Sprintf("Synthetic API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "synthetic", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Results []struct {
			URL       string `json:"url"`
			Title     string `json:"title"`
			Text      string `json:"text"`
			Published string `json:"published"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "synthetic", Message: "Synthetic API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL == "" {
			continue
		}
		title := r.Title
		if title == "" {
			title = r.URL
		}
		sources = append(sources, SearchSource{
			Title:         title,
			URL:           r.URL,
			Snippet:       r.Text,
			PublishedDate: r.Published,
		})
	}
	if limit := params.requestedResults(); limit > 0 && len(sources) > limit {
		sources = sources[:limit]
	}

	return SearchResponse{
		Provider: "synthetic",
		Sources:  sources,
		AuthMode: "api_key",
	}, nil
}

// =============================================================================
// 14. Ollama (OLLAMA_CLOUD_API_KEY)
// upstream: packages/coding-agent/src/web/search/providers/ollama.ts
// =============================================================================

type ollamaProvider struct{}

func (ollamaProvider) ID() string        { return "ollama" }
func (ollamaProvider) Label() string     { return "Ollama" }
func (ollamaProvider) IsAvailable() bool { return envTrimmed("OLLAMA_CLOUD_API_KEY") != "" }

func (ollamaProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	apiKey := envTrimmed("OLLAMA_CLOUD_API_KEY")
	if apiKey == "" {
		return SearchResponse{}, &SearchProviderError{
			Provider: "ollama",
			Message:  `Ollama Cloud credentials not found. Set OLLAMA_CLOUD_API_KEY or configure an API key for provider "ollama-cloud".`,
			Status:   401,
		}
	}
	numResults := clampNumResults(params.requestedResults(), 5, 10)
	parsed := parsedOrParse(params)
	query := params.Query
	if parsed.HasDirectives {
		query = FormatQuery(parsed, QuerySyntax{Phrases: true, Negation: true, Site: true})
	}

	tctx, cancel := withProviderTimeout(ctx, params.Timeout)
	defer cancel()

	resp, err := doJSONRequest(tctx, client, http.MethodPost, ollamaSearchURL, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}, map[string]any{"query": query, "max_results": numResults})
	if err != nil {
		return SearchResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText, _ := readLimitedText(resp, "ollama", maxProviderErrBytes, true)
		if classified := classifyProviderHttpError("ollama", resp.StatusCode, errText); classified != nil {
			return SearchResponse{}, classified
		}
		return SearchResponse{}, &SearchProviderError{
			Provider: "ollama",
			Message:  fmt.Sprintf("Ollama API error (%d): %s", resp.StatusCode, errText),
			Status:   resp.StatusCode,
		}
	}

	raw, err := readLimitedText(resp, "ollama", maxProviderRespBytes, false)
	if err != nil {
		return SearchResponse{}, err
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return SearchResponse{}, &SearchProviderError{Provider: "ollama", Message: "Ollama API returned invalid JSON", Status: 500}
	}

	sources := make([]SearchSource, 0, len(payload.Results))
	for _, r := range payload.Results {
		u := normalizeSearchText(r.URL)
		if u == "" {
			continue
		}
		title := normalizeSearchText(r.Title)
		if title == "" {
			title = u
		}
		sources = append(sources, SearchSource{
			Title:   title,
			URL:     u,
			Snippet: normalizeSearchText(r.Content),
		})
		if len(sources) >= numResults {
			break
		}
	}

	return SearchResponse{
		Provider: "ollama",
		Sources:  sources,
		AuthMode: "api_key",
	}, nil
}

func parsedOrParse(params SearchParams) StructuredQuery {
	if params.ParsedQuery != nil {
		return *params.ParsedQuery
	}
	return ParseSearchQuery(params.Query)
}

// defaultProviders defines the search provider selection order:
//  1. omp's default web role chain (catalog/src/compat/rules/runtime/behavior.kdl:521-533):
//     parallel (keyed or keyless MCP), exa, firecrawl (keyed/self-hosted or keyless), searxng, duckduckgo (keyless HTML)
//  2. Port extension (because there is no modelRoles.web config here): remaining credentialed
//     HTTP+env-key providers when their env key is set, in coding-agent/src/web/search/provider.ts order.
var defaultProviders = []SearchProvider{
	parallelProvider{},
	exaProvider{},
	firecrawlProvider{},
	searxngProvider{},
	duckDuckGoProvider{},
	perplexityProvider{},
	tinyfishProvider{},
	keenableProvider{},
	sofyaProvider{},
	jinaProvider{},
	kagiProvider{},
	tavilyProvider{},
	braveProvider{},
	deepseekProvider{},
	kimiProvider{},
	syntheticProvider{},
	ollamaProvider{},
}
