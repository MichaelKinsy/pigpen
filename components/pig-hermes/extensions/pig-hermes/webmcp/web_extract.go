package webmcp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
)

const (
	defaultExtractMaxChars = 20000
	maxExtractURLs         = 5
	defaultExtractTimeout  = 30 * time.Second
	maxExtractRespBytes    = 8 << 20 // 8 MiB
	maxExtractRedirects    = 10
)

// ExtractedPage is the structured result for a single extracted URL.
type ExtractedPage struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ExtractResponse is the structured response returned by web_extract.
type ExtractResponse struct {
	Pages []ExtractedPage `json:"pages"`
}

var (
	reScriptBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyleBlock  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reNoScript    = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript>`)
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reTitleTag    = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	reHeadingTag  = regexp.MustCompile(`(?is)<h([1-6])\b[^>]*>(.*?)</h[1-6]>`)
	reAnchorTag   = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))[^>]*>(.*?)</a>`)
	reListItem    = regexp.MustCompile(`(?is)<li\b[^>]*>(.*?)</li>`)
	reBreakTag    = regexp.MustCompile(`(?is)<br\s*/?>`)
	reBlockClose  = regexp.MustCompile(`(?is)</?(?:p|div|section|article|header|footer|main|aside|nav|blockquote|pre|ul|ol|table|tr|hr)\b[^>]*>`)
	reAnyTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reHorizSpace  = regexp.MustCompile(`[ \t\f\r]+`)
	reMultiBlank  = regexp.MustCompile(`\n{3,}`)
)

func registerWebExtractTool(ext *sdk.Extension, _ *config.Config) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "web_extract",
		Label:         "Web Extract",
		Description:   "Extract clean readable text/markdown content from up to 5 HTTP/HTTPS URLs. Strips scripts and styles, preserves headings, links, and paragraphs, and truncates to max_chars (default 20000).",
		PromptSnippet: "Extract readable markdown/text from web page URLs",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"urls": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "List of URLs to extract content from (max 5 URLs per call).",
					"maxItems":    maxExtractURLs,
				},
				"url": map[string]any{
					"type":        "string",
					"description": "Single URL to extract content from (alternative to urls).",
				},
				"max_chars": map[string]any{
					"type":        "integer",
					"description": "Maximum characters per page (default 20000).",
					"minimum":     100,
					"default":     defaultExtractMaxChars,
				},
				"char_limit": map[string]any{
					"type":        "integer",
					"description": "Alias for max_chars.",
					"minimum":     100,
				},
			},
		},
		Execute: func(sdkCtx sdk.Context, params map[string]any) (any, error) {
			ctx, cancel := contextFromSDK(sdkCtx, defaultExtractTimeout)
			defer cancel()
			return handleWebExtract(ctx, params), nil
		},
	})
}

func handleWebExtract(ctx context.Context, params map[string]any) sdk.ToolResult {
	urls, err := collectExtractURLs(params)
	if err != nil {
		return toolErrorResult(fmt.Sprintf("web_extract: %v", err))
	}
	if len(urls) == 0 {
		return toolErrorResult("web_extract: at least one URL is required (url or urls)")
	}
	if len(urls) > maxExtractURLs {
		return toolErrorResult(fmt.Sprintf("web_extract: maximum %d URLs per call (got %d)", maxExtractURLs, len(urls)))
	}

	maxChars := defaultExtractMaxChars
	if _, hasMax := params["max_chars"]; hasMax {
		n, err := intParam(params, "max_chars", defaultExtractMaxChars)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("web_extract: %v", err))
		}
		if n > 0 {
			maxChars = n
		}
	} else if _, hasCharLimit := params["char_limit"]; hasCharLimit {
		n, err := intParam(params, "char_limit", defaultExtractMaxChars)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("web_extract: %v", err))
		}
		if n > 0 {
			maxChars = n
		}
	}

	allowLocalhost := allowLocalhostFetch()
	client := newSafeExtractClient(defaultExtractTimeout, allowLocalhost)

	pages := make([]ExtractedPage, 0, len(urls))
	allFailed := true
	for _, rawURL := range urls {
		page := extractSingleURL(ctx, client, rawURL, maxChars, allowLocalhost)
		if page.Error == "" {
			allFailed = false
		}
		pages = append(pages, page)
	}

	resp := ExtractResponse{Pages: pages}
	res := sdk.ToolResult{
		Content:           formatExtractMarkdown(resp),
		StructuredContent: resp,
		Details:           resp,
		IsError:           allFailed,
	}
	return res
}

func collectExtractURLs(params map[string]any) ([]string, error) {
	urls, err := stringSliceParam(params, "urls")
	if err != nil {
		return nil, err
	}
	if single := strings.TrimSpace(stringParam(params, "url")); single != "" {
		urls = append(urls, single)
	}
	return urls, nil
}

func allowLocalhostFetch() bool {
	v := strings.TrimSpace(os.Getenv("HERMES_ALLOW_LOCALHOST_FETCH"))
	return v == "1" || strings.EqualFold(v, "true")
}

func validateFetchURL(rawURL string, allowLocalhost bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q (only http and https are allowed)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("URL is missing host")
	}
	if err := checkHostAllowed(host, allowLocalhost); err != nil {
		return nil, err
	}
	return u, nil
}

func checkHostAllowed(host string, allowLocalhost bool) error {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		if allowLocalhost {
			return nil
		}
		return fmt.Errorf("blocked loopback host %q (set HERMES_ALLOW_LOCALHOST_FETCH=1 to allow)", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		return checkIPAllowed(ip, allowLocalhost)
	}
	return nil
}

func checkIPAllowed(ip net.IP, allowLocalhost bool) error {
	if ip == nil {
		return errors.New("empty IP address")
	}
	if ip.IsLoopback() {
		if allowLocalhost {
			return nil
		}
		return fmt.Errorf("blocked loopback IP %s", ip.String())
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("blocked non-public IP %s", ip.String())
	}
	// Block CGNAT 100.64.0.0/10 and cloud metadata 169.254.169.254 (already link-local).
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return fmt.Errorf("blocked shared address space IP %s", ip.String())
		}
	}
	return nil
}

func newSafeExtractClient(timeout time.Duration, allowLocalhost bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil {
			if err := checkIPAllowed(ip, allowLocalhost); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no IP addresses found for %s", host)
		}
		for _, ipAddr := range ips {
			if err := checkIPAllowed(ipAddr.IP, allowLocalhost); err != nil {
				return nil, err
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxExtractRedirects {
				return fmt.Errorf("stopped after %d redirects", maxExtractRedirects)
			}
			if _, err := validateFetchURL(req.URL.String(), allowLocalhost); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}

func extractSingleURL(ctx context.Context, client *http.Client, rawURL string, maxChars int, allowLocalhost bool) ExtractedPage {
	u, err := validateFetchURL(rawURL, allowLocalhost)
	if err != nil {
		return ExtractedPage{URL: rawURL, Error: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ExtractedPage{URL: rawURL, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "pig-hermes/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return ExtractedPage{URL: rawURL, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxExtractRespBytes))
	if err != nil {
		return ExtractedPage{URL: rawURL, Error: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExtractedPage{
			URL:   rawURL,
			Error: fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	rawText := string(body)

	var title, text string
	if strings.Contains(contentType, "html") || looksLikeHTML(rawText) {
		title, text = htmlToMarkdown(rawText)
	} else {
		text = strings.TrimSpace(rawText)
	}

	truncated := false
	if maxChars > 0 && utf8.RuneCountInString(text) > maxChars {
		text = truncateRunes(text, maxChars) + "\n\n[truncated]"
		truncated = true
	}

	return ExtractedPage{
		URL:       rawURL,
		Title:     title,
		Content:   text,
		Truncated: truncated,
	}
}

func looksLikeHTML(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) > 512 {
		trimmed = trimmed[:512]
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<!doctype html") ||
		strings.HasPrefix(lower, "<html") ||
		strings.Contains(lower, "<body") ||
		strings.Contains(lower, "<head")
}

// htmlToMarkdown strips scripts/styles/comments, preserves headings, links,
// list items, and paragraph breaks, and decodes HTML entities.
func htmlToMarkdown(raw string) (title string, content string) {
	if m := reTitleTag.FindStringSubmatch(raw); len(m) == 2 {
		title = normalizeInlineText(m[1])
	}

	s := reHTMLComment.ReplaceAllString(raw, "")
	s = reScriptBlock.ReplaceAllString(s, "")
	s = reStyleBlock.ReplaceAllString(s, "")
	s = reNoScript.ReplaceAllString(s, "")
	s = reTitleTag.ReplaceAllString(s, "")

	// Convert headings to markdown headings.
	s = reHeadingTag.ReplaceAllStringFunc(s, func(match string) string {
		parts := reHeadingTag.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		level := int(parts[1][0] - '0')
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		text := normalizeInlineText(parts[2])
		if text == "" {
			return "\n\n"
		}
		return "\n\n" + strings.Repeat("#", level) + " " + text + "\n\n"
	})

	// Convert anchors to markdown links [text](href).
	s = reAnchorTag.ReplaceAllStringFunc(s, func(match string) string {
		parts := reAnchorTag.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		href := parts[1]
		if href == "" {
			href = parts[2]
		}
		if href == "" {
			href = parts[3]
		}
		href = strings.TrimSpace(html.UnescapeString(href))
		label := normalizeInlineText(parts[4])
		if href == "" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return label
		}
		if label == "" {
			return href
		}
		return fmt.Sprintf("[%s](%s)", label, href)
	})

	// Convert list items.
	s = reListItem.ReplaceAllStringFunc(s, func(match string) string {
		parts := reListItem.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		text := normalizeInlineText(parts[1])
		if text == "" {
			return "\n"
		}
		return "\n- " + text + "\n"
	})

	s = reBreakTag.ReplaceAllString(s, "\n")
	s = reBlockClose.ReplaceAllString(s, "\n\n")
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	// Normalize whitespace line by line.
	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = reHorizSpace.ReplaceAllString(line, " ")
		line = strings.TrimSpace(line)
		cleaned = append(cleaned, line)
	}
	joined := strings.Join(cleaned, "\n")
	joined = reMultiBlank.ReplaceAllString(joined, "\n\n")
	return title, strings.TrimSpace(joined)
}

func normalizeInlineText(s string) string {
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = reHorizSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

func formatExtractMarkdown(resp ExtractResponse) string {
	var b strings.Builder
	for i, p := range resp.Pages {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		if p.Title != "" {
			fmt.Fprintf(&b, "# %s\n", p.Title)
		}
		fmt.Fprintf(&b, "URL: %s\n", p.URL)
		if p.Error != "" {
			fmt.Fprintf(&b, "Error: %s", p.Error)
			continue
		}
		if p.Content != "" {
			b.WriteString("\n")
			b.WriteString(p.Content)
		}
	}
	return strings.TrimSpace(b.String())
}
