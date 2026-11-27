package webmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

const (
	defaultSearchLimit   = 5
	maxSearchLimit       = 20
	defaultSearchTimeout = 30 * time.Second
	maxSearchRespBytes   = 4 << 20 // 4 MiB

	defaultTavilyBaseURL     = "https://api.tavily.com"
	defaultBraveBaseURL      = "https://api.search.brave.com"
	defaultDuckDuckGoBaseURL = "https://html.duckduckgo.com/html"
)

var (
	ddgResultLinkRe    = regexp.MustCompile(`(?is)<a\b[^>]*class="[^"]*\bresult__a\b[^"]*"[^>]*>(.*?)</a>`)
	ddgResultSnippetRe = regexp.MustCompile(`(?is)<(?:a|div|span)\b[^>]*class="[^"]*\bresult__snippet\b[^"]*"[^>]*>(.*?)</(?:a|div|span)>`)
	ddgHrefAttrRe      = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	ddgHTMLTagRe       = regexp.MustCompile(`(?s)<[^>]+>`)
	ddgWhitespaceRe    = regexp.MustCompile(`\s+`)
)

// SearchResult is a normalized web search hit.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchResponse is the structured response returned by web_search.
type SearchResponse struct {
	Query    string         `json:"query"`
	Provider string         `json:"provider"`
	Results  []SearchResult `json:"results"`
}

func registerWebSearchTool(ext *sdk.Extension, cfg *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "web_search",
		Label:         "Web Search",
		Description:   "Search the web: returns titles, URLs and snippets (5 by default). Read pages with web_extract. Supports tavily, brave, searxng, duckduckgo, or auto provider selection.",
		PromptSnippet: "Search the web for titles, URLs, and snippets",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query to look up on the web.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default 5, max 20).",
					"minimum":     1,
					"maximum":     maxSearchLimit,
					"default":     defaultSearchLimit,
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "Search provider override: tavily, brave, searxng, duckduckgo, or auto.",
					"enum":        []string{"auto", "tavily", "brave", "searxng", "duckduckgo"},
				},
			},
			"required": []string{"query"},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			ctx, cancel := contextFromSDK(sdkCtx, defaultSearchTimeout)
			defer cancel()
			return handleWebSearch(ctx, params, cfg), nil
		},
	})
}

func handleWebSearch(ctx context.Context, params map[string]any, cfg *config.Config) sdk.ToolResult {
	query := strings.TrimSpace(stringParam(params, "query"))
	if query == "" {
		return toolErrorResult("web_search: query is required")
	}
	limit, err := intParam(params, "limit", defaultSearchLimit)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("web_search: %v", err))
	}
	if limit < 1 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	requestedProvider := strings.ToLower(strings.TrimSpace(stringParam(params, "provider")))
	if requestedProvider == "" {
		requestedProvider = "auto"
	}
	provider, err := resolveSearchProvider(requestedProvider, cfg)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("web_search: %v", err))
	}

	client := newHTTPClient(defaultSearchTimeout)
	results, err := executeWebSearch(ctx, client, provider, query, limit, cfg)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("web_search (%s): %v", provider, err))
	}
	if len(results) > limit {
		results = results[:limit]
	}

	resp := SearchResponse{
		Query:    query,
		Provider: provider,
		Results:  results,
	}
	return sdk.ToolResult{
		Content:           formatSearchMarkdown(resp),
		StructuredContent: resp,
		Details:           resp,
	}
}

func resolveSearchProvider(requested string, cfg *config.Config) (string, error) {
	switch requested {
	case "tavily", "brave", "searxng", "duckduckgo":
		return requested, nil
	case "", "auto":
		if cfg != nil && cfg.Raw != nil {
			for _, k := range []string{"web.search_backend", "web.backend"} {
				v := strings.ToLower(strings.TrimSpace(cfg.Raw[k]))
				switch v {
				case "tavily", "brave", "searxng", "duckduckgo":
					return v, nil
				case "brave-free":
					return "brave", nil
				case "ddgs":
					return "duckduckgo", nil
				}
			}
		}
		if strings.TrimSpace(cfg.Getenv("TAVILY_API_KEY")) != "" {
			return "tavily", nil
		}
		if braveAPIKey(cfg) != "" {
			return "brave", nil
		}
		if strings.TrimSpace(cfg.Getenv("SEARXNG_URL")) != "" {
			return "searxng", nil
		}
		return "duckduckgo", nil
	default:
		return "", fmt.Errorf("unsupported provider %q (expected tavily, brave, searxng, duckduckgo, or auto)", requested)
	}
}

func braveAPIKey(cfg *config.Config) string {
	if v := strings.TrimSpace(cfg.Getenv("BRAVE_API_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(cfg.Getenv("BRAVE_SEARCH_API_KEY"))
}

func searchBaseURL(defaultURL string, cfg *config.Config) string {
	if custom := strings.TrimSpace(cfg.Getenv("HERMES_WEB_SEARCH_BASE_URL")); custom != "" {
		return strings.TrimRight(custom, "/")
	}
	return strings.TrimRight(defaultURL, "/")
}

func newHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func executeWebSearch(ctx context.Context, client *http.Client, provider, query string, limit int, cfg *config.Config) ([]SearchResult, error) {
	switch provider {
	case "tavily":
		return searchTavily(ctx, client, query, limit, cfg)
	case "brave":
		return searchBrave(ctx, client, query, limit, cfg)
	case "searxng":
		return searchSearXNG(ctx, client, query, limit, cfg)
	case "duckduckgo":
		return searchDuckDuckGo(ctx, client, query, limit, cfg)
	default:
		return nil, fmt.Errorf("unknown search provider %q", provider)
	}
}

func searchTavily(ctx context.Context, client *http.Client, query string, limit int, cfg *config.Config) ([]SearchResult, error) {
	apiKey := strings.TrimSpace(cfg.Getenv("TAVILY_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("TAVILY_API_KEY is not set")
	}
	payload := map[string]any{
		"api_key":     apiKey,
		"query":       query,
		"max_results": limit,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := searchBaseURL(defaultTavilyBaseURL, cfg) + "/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(client, req, maxSearchRespBytes)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	out := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		snippet := strings.TrimSpace(r.Content)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Snippet)
		}
		out = append(out, SearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: snippet,
		})
	}
	return out, nil
}

func searchBrave(ctx context.Context, client *http.Client, query string, limit int, cfg *config.Config) ([]SearchResult, error) {
	apiKey := braveAPIKey(cfg)
	if apiKey == "" {
		return nil, errors.New("BRAVE_API_KEY is not set")
	}
	endpoint := searchBaseURL(defaultBraveBaseURL, cfg) + "/res/v1/web/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	raw, err := doHTTPRead(client, req, maxSearchRespBytes)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	out := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		out = append(out, SearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: strings.TrimSpace(r.Description),
		})
	}
	return out, nil
}

func searchSearXNG(ctx context.Context, client *http.Client, query string, limit int, cfg *config.Config) ([]SearchResult, error) {
	base := strings.TrimSpace(cfg.Getenv("SEARXNG_URL"))
	if base == "" {
		base = strings.TrimSpace(cfg.Getenv("HERMES_WEB_SEARCH_BASE_URL"))
	}
	if base == "" {
		return nil, errors.New("SEARXNG_URL is not set")
	}
	endpoint := strings.TrimRight(base, "/") + "/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(client, req, maxSearchRespBytes)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	out := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		out = append(out, SearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: strings.TrimSpace(r.Content),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func searchDuckDuckGo(ctx context.Context, client *http.Client, query string, limit int, cfg *config.Config) ([]SearchResult, error) {
	endpoint := searchBaseURL(defaultDuckDuckGoBaseURL, cfg) + "/"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; pig-hermes/1.0)")

	raw, err := doHTTPRead(client, req, maxSearchRespBytes)
	if err != nil {
		return nil, err
	}

	return parseDuckDuckGoHTML(string(raw), limit), nil
}

func parseDuckDuckGoHTML(body string, limit int) []SearchResult {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	linkLocs := ddgResultLinkRe.FindAllStringSubmatchIndex(body, -1)
	if len(linkLocs) == 0 {
		return nil
	}
	out := make([]SearchResult, 0, limit)
	for i, loc := range linkLocs {
		if len(out) >= limit {
			break
		}
		fullTag := body[loc[0]:loc[1]]
		innerTitle := body[loc[2]:loc[3]]
		rawHref := extractDDGHref(fullTag)
		resolvedURL := resolveDDGRedirectURL(rawHref)
		title := cleanDDGHTMLText(innerTitle)
		if title == "" && resolvedURL == "" {
			continue
		}
		if title == "" {
			title = resolvedURL
		}

		// Search for the matching result__snippet between this link and the next link.
		chunkEnd := len(body)
		if i+1 < len(linkLocs) {
			chunkEnd = linkLocs[i+1][0]
		}
		chunk := body[loc[1]:chunkEnd]
		var snippet string
		if sm := ddgResultSnippetRe.FindStringSubmatch(chunk); len(sm) >= 2 {
			snippet = cleanDDGHTMLText(sm[1])
		}

		out = append(out, SearchResult{
			Title:   title,
			URL:     resolvedURL,
			Snippet: snippet,
		})
	}
	return out
}

func extractDDGHref(anchorTag string) string {
	m := ddgHrefAttrRe.FindStringSubmatch(anchorTag)
	if len(m) < 3 {
		return ""
	}
	href := m[1]
	if href == "" {
		href = m[2]
	}
	return strings.TrimSpace(html.UnescapeString(href))
}

func resolveDDGRedirectURL(rawHref string) string {
	rawHref = strings.TrimSpace(rawHref)
	if rawHref == "" {
		return ""
	}
	// Handle protocol-relative DuckDuckGo redirect links like //duckduckgo.com/l/?uddg=...
	parseTarget := rawHref
	if strings.HasPrefix(parseTarget, "//") {
		parseTarget = "https:" + parseTarget
	}
	if u, err := url.Parse(parseTarget); err == nil {
		if uddg := strings.TrimSpace(u.Query().Get("uddg")); uddg != "" {
			return uddg
		}
		if strings.HasPrefix(rawHref, "//") {
			return parseTarget
		}
	}
	return rawHref
}

func cleanDDGHTMLText(s string) string {
	stripped := ddgHTMLTagRe.ReplaceAllString(s, " ")
	unescaped := html.UnescapeString(stripped)
	return strings.TrimSpace(ddgWhitespaceRe.ReplaceAllString(unescaped, " "))
}

func doHTTPRead(client *http.Client, req *http.Request, maxBytes int64) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		if snippet != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
		}
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
	}
	return body, nil
}

func formatSearchMarkdown(resp SearchResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q (provider: %s)\n", resp.Query, resp.Provider)
	if len(resp.Results) == 0 {
		b.WriteString("\nNo results found.")
		return b.String()
	}
	for i, r := range resp.Results {
		title := r.Title
		if title == "" {
			title = r.URL
		}
		fmt.Fprintf(&b, "\n%d. [%s](%s)\n", i+1, title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
