package rpiv_web_tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The web_search and web_fetch tools, the provider resolution and the URL guard. upstream: web-tools.ts.

const (
	minSearchResults     = 1
	maxSearchResults     = 10
	defaultSearchResults = 5
	defaultProviderName  = "brave"
	legacyKeyProvider    = "brave" // the only provider whose key was ever stored at the top level (config.apiKey)
	defaultMaxLines      = 2000
	defaultMaxBytes      = 50 * 1024
)

var (
	defaultWebSearchSnippet    = "Search the web for up-to-date information"
	defaultWebSearchGuidelines = []string{
		"Use web_search for information beyond your training data — recent events, current library versions, live API documentation.",
		`Use the current year from "Current date:" in your context when searching for recent information or documentation.`,
		`After answering using search results, include a "Sources:" section listing relevant URLs as markdown hyperlinks: [Title](URL). Never skip this.`,
		"Domain filtering is supported to include or block specific websites.",
		"If no API key is configured, ask the user to run /web-tools before proceeding.",
	}
	defaultWebFetchSnippet    = "Fetch and read content from a specific URL"
	defaultWebFetchGuidelines = []string{
		"Use web_fetch to read the full content of a specific URL — documentation pages, blog posts, API references found via web_search.",
		"web_fetch is complementary to web_search: search finds URLs, fetch reads them.",
		`After answering using fetched content, include a "Sources:" section with a markdown hyperlink to the fetched URL.`,
		"Large responses are truncated and spilled to a temp file — the temp path is reported in the result details.",
	}
)

func envTrim(name string) string { return jsTrim(os.Getenv(name)) }

// resolveProviderAPIKey: env var, then apiKeys.<provider>, then (brave only) the legacy top-level apiKey.
// upstream: web-tools.ts:98-112.
func resolveProviderAPIKey(name string, cfg webToolsConfig) (string, bool) {
	meta := metaOf(name)
	if meta == nil {
		return "", false
	}
	if meta.EnvVar != "" {
		if k := envTrim(meta.EnvVar); k != "" {
			return k, true
		}
	}
	if k, _ := cfg.apiKey(name); jsTrim(k) != "" {
		return jsTrim(k), true
	}
	if name == legacyKeyProvider {
		if k, _ := cfg.str("apiKey"); jsTrim(k) != "" {
			return jsTrim(k), true
		}
	}
	return "", false
}

// resolveProviderBaseURL: env var, then baseUrls.<provider>, then the default. upstream: web-tools.ts:114-125.
func resolveProviderBaseURL(meta *providerMeta, cfg webToolsConfig) string {
	if meta.BaseURLEnvVar == "" {
		return ""
	}
	if u := envTrim(meta.BaseURLEnvVar); u != "" {
		return u
	}
	if u, _ := cfg.baseURL(meta.Name); jsTrim(u) != "" {
		return jsTrim(u)
	}
	return meta.DefaultBaseURL
}

func knownProviderNames() []string {
	out := make([]string, len(providers))
	for i, p := range providers {
		out[i] = p.Name
	}
	return out
}

func assertKnownProvider(name string) error {
	if metaOf(name) == nil {
		return fmt.Errorf("Unknown web_search provider: \"%s\". Valid providers: %s.", name, strings.Join(knownProviderNames(), ", "))
	}
	return nil
}

// activeProvider is the provider for display and selection: env over config over the default, not validated.
// upstream: web-tools.ts:147-157.
func activeProvider(cfg webToolsConfig) (name, source string) {
	if e := envTrim("WEB_SEARCH_PROVIDER"); e != "" {
		return e, "env"
	}
	if p, _ := cfg.str("provider"); p != "" {
		return p, "config"
	}
	return defaultProviderName, "default"
}

// instantiateProvider resolves the provider (override, WEB_SEARCH_PROVIDER, config, default), its credentials and
// builds it. An override wins without consulting the env, so a bogus env var cannot defeat a valid per-call
// override. upstream: web-tools.ts:176-200.
func instantiateProvider(cfg webToolsConfig, override *string) (string, provider, error) {
	var name string
	if override != nil {
		if err := assertKnownProvider(*override); err != nil {
			return "", nil, err
		}
		name = *override
	} else {
		n, source := activeProvider(cfg)
		if source == "env" {
			if err := assertKnownProvider(n); err != nil {
				return "", nil, err
			}
		}
		name = n
	}
	apiKey, _ := resolveProviderAPIKey(name, cfg)
	meta := metaOf(name)
	baseURL := ""
	if meta != nil && meta.BaseURLEnvVar != "" {
		baseURL = resolveProviderBaseURL(meta, cfg)
	}
	p, err := createSearchProvider(name, apiKey, baseURL)
	return name, p, err
}

// maskAPIKey shows the first and last four characters of a key.
func maskAPIKey(key string, set bool) string {
	if !set || key == "" {
		return "(not set)"
	}
	u := []rune(key)
	head, tail := string(u[:min(4, len(u))]), string(u[max(0, len(u)-4):])
	return head + "..." + tail
}

func clampSearchResultCount(requested *float64) int {
	v := float64(defaultSearchResults)
	if requested != nil {
		v = *requested
	}
	return int(min(max(v, minSearchResults), maxSearchResults))
}

// isPrivateOrLoopbackHostname: localhost and *.localhost, IPv6 loopback, unspecified, link-local and
// unique-local, and the IPv4 literals of loopback and private ranges. upstream: web-tools.ts:243-258.
func isPrivateOrLoopbackHostname(hostname string) bool {
	h := strings.ToLower(hostname)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if h == "::1" || h == "::" || strings.HasPrefix(h, "fe80:") || strings.HasPrefix(h, "fc") || strings.HasPrefix(h, "fd") {
		return true
	}
	parts := strings.Split(h, ".")
	if len(parts) != 4 {
		return false
	}
	var n [4]int
	for i, p := range parts {
		if len(p) < 1 || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		n[i] = atoi(p)
	}
	a, b := n[0], n[1]
	switch {
	case a == 0 || a == 127 || a == 10:
		return true
	case a == 169 && b == 254:
		return true
	case a == 172 && b >= 16 && b <= 31:
		return true
	case a == 192 && b == 168:
		return true
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// parseAndAssertHTTPURL refuses a malformed URL, a protocol other than http(s) and a private or loopback host. The
// host is checked as the URL parser serializes it (`127.1` is `127.0.0.1`, `[0::1]` is `[::1]`), and the parsed URL
// is the one the built-in fetch requests, so the check and the request see the same address.
func parseAndAssertHTTPURL(raw string) (*jsURL, error) {
	u, err := parseJSURL(raw)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("Unsupported URL protocol: %s:. Only http and https are supported.", u.Scheme)
	}
	if isPrivateOrLoopbackHostname(u.Hostname) {
		return nil, fmt.Errorf("Refusing to fetch private/loopback address: %s", u.Hostname)
	}
	return u, nil
}

// truncation is Pi's truncateHead result. upstream: pi-coding-agent core/tools/truncate.js.
type truncation struct {
	Content               string  `json:"content"`
	Truncated             bool    `json:"truncated"`
	TruncatedBy           *string `json:"truncatedBy"`
	TotalLines            int     `json:"totalLines"`
	TotalBytes            int     `json:"totalBytes"`
	OutputLines           int     `json:"outputLines"`
	OutputBytes           int     `json:"outputBytes"`
	LastLinePartial       bool    `json:"lastLinePartial"`
	FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
	MaxLines              int     `json:"maxLines"`
	MaxBytes              int     `json:"maxBytes"`
}

func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func truncateHead(content string, maxLines, maxBytes int) truncation {
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	t := truncation{Content: content, TotalLines: len(lines), TotalBytes: totalBytes, OutputLines: len(lines), OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes}
	if len(lines) <= maxLines && totalBytes <= maxBytes {
		return t
	}
	bytesBy, linesBy := "bytes", "lines"
	t.Truncated = true
	if len(lines[0]) > maxBytes {
		t.Content, t.TruncatedBy, t.OutputLines, t.OutputBytes, t.FirstLineExceedsLimit = "", &bytesBy, 0, 0, true
		return t
	}
	var out []string
	count := 0
	by := &linesBy
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++
		}
		if count+lineBytes > maxBytes {
			by = &bytesBy
			break
		}
		out = append(out, lines[i])
		count += lineBytes
	}
	if len(out) >= maxLines && count <= maxBytes {
		by = &linesBy
	}
	t.Content = strings.Join(out, "\n")
	t.TruncatedBy, t.OutputLines, t.OutputBytes = by, len(out), len(t.Content)
	return t
}

// formatSize is Pi's: bytes, KB or MB with one decimal.
func formatSize(b int) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
}

func formatTruncationFooter(t truncation, tempFile string) string {
	return fmt.Sprintf("\n\n[Content truncated: showing %d of %d lines (%s of %s). %d lines (%s) omitted. Full content saved to: %s]",
		t.OutputLines, t.TotalLines, formatSize(t.OutputBytes), formatSize(t.TotalBytes),
		t.TotalLines-t.OutputLines, formatSize(t.TotalBytes-t.OutputBytes), tempFile)
}

func formatFetchHeader(url, title, contentType string) string {
	lines := []string{"**Fetched:** " + url}
	if title != "" {
		lines = append(lines, "**Title:** "+title)
	}
	if contentType != "" {
		lines = append(lines, "**Content-Type:** "+contentType)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func formatSearchResultsBody(resp *searchResponse) string {
	text := fmt.Sprintf("**Search results for \"%s\":**\n\n", resp.Query)
	for i, r := range resp.Results {
		text += fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimRight(text, " \t\n\v\f\r")
}

// spillFullContentToTempFile writes the full body under a new temp directory. upstream: web-tools.ts:213-217.
func spillFullContentToTempFile(content string) (string, error) {
	dir, err := os.MkdirTemp("", "rpiv-fetch-")
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, "content.txt")
	return file, os.WriteFile(file, []byte(content), 0o644)
}

// searchTool runs web_search. upstream: web-tools.ts:300-352.
func searchTool(ctx context.Context, query string, maxResults *float64, providerOverride *string, onUpdate func(text string, details any)) (string, any, error) {
	n := clampSearchResultCount(maxResults)
	cfg := readConfig()
	name, p, err := instantiateProvider(cfg, providerOverride)
	if err != nil {
		return "", nil, err
	}
	if onUpdate != nil {
		onUpdate(fmt.Sprintf("Searching %s for: \"%s\"...", p.label(), query), ordered{{"query", query}, {"backend", name}, {"resultCount", 0}})
	}
	resp, err := p.search(ctx, query, n)
	if err != nil {
		return "", nil, err
	}
	if len(resp.Results) == 0 {
		return fmt.Sprintf("No results found for \"%s\".", query), ordered{{"query", query}, {"backend", name}, {"resultCount", 0}}, nil
	}
	return formatSearchResultsBody(resp), ordered{{"query", query}, {"backend", name}, {"resultCount", len(resp.Results)}, {"results", resp.Results}}, nil
}

// fetchTool runs web_fetch. upstream: web-tools.ts:420-470.
func fetchTool(ctx context.Context, rawURL string, raw bool, onUpdate func(text string, details any)) (string, any, error) {
	target, err := parseAndAssertHTTPURL(rawURL)
	if err != nil {
		return "", nil, err
	}
	if onUpdate != nil {
		onUpdate(fmt.Sprintf("Fetching: %s...", rawURL), ordered{{"url", rawURL}})
	}
	cfg := readConfig()
	_, p, err := instantiateProvider(cfg, nil)
	if err != nil {
		return "", nil, err
	}
	var resp *fetchResponse
	if fp, ok := p.(fetchProvider); ok {
		resp, err = fp.fetch(ctx, rawURL, raw)
	} else {
		resp, err = fetchViaGenericHTML(ctx, target.Href, rawURL, raw)
	}
	if err != nil {
		return "", nil, err
	}
	t := truncateHead(resp.Text, defaultMaxLines, defaultMaxBytes)
	details := ordered{{"url", rawURL}}
	if resp.Title != "" {
		details = append(details, kv{"title", resp.Title})
	}
	if resp.ContentType != "" {
		details = append(details, kv{"contentType", resp.ContentType})
	}
	if resp.ContentLength != nil {
		details = append(details, kv{"contentLength", *resp.ContentLength})
	}
	output := t.Content
	if t.Truncated {
		tempFile, err := spillFullContentToTempFile(resp.Text)
		if err != nil {
			return "", nil, err
		}
		details = append(details, kv{"truncation", t}, kv{"fullOutputPath", tempFile})
		output += formatTruncationFooter(t, tempFile)
	}
	return formatFetchHeader(rawURL, resp.Title, resp.ContentType) + output, details, nil
}

var _ = utf8.RuneError
