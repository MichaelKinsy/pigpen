package web

// URL fetcher ported from omp's tools/fetch.ts and web/scrapers/types.ts.
// Exported for file tools (`read`) to call on http(s) URLs in a later wave.
// upstream: packages/coding-agent/src/tools/fetch.ts, packages/coding-agent/src/web/scrapers/types.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FetchMaxBytes is omp's 50 MiB network read cap for fetched response bodies.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:40
const FetchMaxBytes int64 = 50 * 1024 * 1024

const (
	fetchDefaultTimeout  = 30 * time.Second
	fetchRetryAfterMax   = 10 * time.Second
	fetchBinarySampleLen = 4096
)

// fetchMaxBytes is a var so tests can verify oversized body handling without allocating 50 MiB.
var fetchMaxBytes = FetchMaxBytes

// fetchRetrySleep is overridable in tests for deterministic 429 backoff tests.
var fetchRetrySleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// upstream: packages/coding-agent/src/web/scrapers/types.ts:42-46
var fetchUserAgents = []string{
	"curl/8.0",
	"Mozilla/5.0 (compatible; TextBot/1.0)",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
}

var (
	collapsedSchemeRe = regexp.MustCompile(`(?i)^(https?):/([^/])`)
	multiBlankLineRe  = regexp.MustCompile(`\n{3,}`)
	linkTagRe         = regexp.MustCompile(`(?i)<link\b[^>]*>`)
	headOpenRe        = regexp.MustCompile(`(?i)<head[\s>]`)
	headCloseRe       = regexp.MustCompile(`(?i)</head\s*>`)
	cdataStripRe      = regexp.MustCompile(`<!\[CDATA\[|\]\]>`)
	anyHTMLTagRe      = regexp.MustCompile(`<[^>]+>`)
)

// repairCollapsedScheme fixes `https:/example.com` -> `https://example.com`.
// upstream: packages/tui/src/tools/fetch.ts:41-44
func repairCollapsedScheme(raw string) string {
	return collapsedSchemeRe.ReplaceAllString(raw, "$1://$2")
}

// normalizeFetchURL repairs a collapsed scheme and adds https:// when missing.
// upstream: packages/coding-agent/src/tools/fetch.ts:132-138
func normalizeFetchURL(raw string) (string, error) {
	u := strings.TrimSpace(repairCollapsedScheme(raw))
	if u == "" {
		return "", errors.New("URL is required")
	}
	if strings.HasPrefix(u, "pi-internal://") {
		return u, nil
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid URL: %q", raw)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q: only http and https are supported", parsed.Scheme)
	}
	return parsed.String(), nil
}

// normalizeMime extracts the lowercased MIME type without parameters.
// upstream: packages/coding-agent/src/tools/fetch.ts:196-198
func normalizeMime(contentType string) string {
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = contentType[:idx]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// finalizeOutput collapses runs of 3+ newlines and trims surrounding whitespace.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:67-69
func finalizeOutput(content string) string {
	return strings.TrimSpace(multiBlankLineRe.ReplaceAllString(content, "\n\n"))
}

// looksLikeHTML inspects the leading tag to decide whether content is HTML.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:331-340
func looksLikeHTML(content string) bool {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if len(trimmed) > 16 {
		trimmed = trimmed[:16]
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<!doctype") ||
		strings.HasPrefix(lower, "<html") ||
		strings.HasPrefix(lower, "<head") ||
		strings.HasPrefix(lower, "<body")
}

// sampleLooksBinary checks the first 4096 runes for NUL bytes or >1% replacement chars.
// upstream: packages/coding-agent/src/tools/fetch.ts:768-780
func sampleLooksBinary(text string) bool {
	if len(text) == 0 {
		return false
	}
	replacements := 0
	count := 0
	for _, r := range text {
		if count >= fetchBinarySampleLen {
			break
		}
		if r == 0 {
			return true
		}
		if r == '\uFFFD' {
			replacements++
		}
		count++
	}
	return replacements >= 3 && float64(replacements)/float64(count) > 0.01
}

// isNonTextMime reports whether mime is a known binary/non-text content type.
func isNonTextMime(mime string) bool {
	if mime == "" {
		return false
	}
	if strings.HasPrefix(mime, "text/") {
		return false
	}
	if strings.Contains(mime, "json") ||
		strings.Contains(mime, "xml") ||
		strings.Contains(mime, "html") ||
		strings.Contains(mime, "javascript") ||
		strings.Contains(mime, "ecmascript") ||
		strings.Contains(mime, "yaml") ||
		strings.Contains(mime, "toml") ||
		strings.Contains(mime, "rss") ||
		strings.Contains(mime, "atom") ||
		strings.Contains(mime, "feed") ||
		mime == "application/x-www-form-urlencoded" ||
		mime == "application/sql" ||
		mime == "application/graphql" {
		return false
	}
	if strings.HasPrefix(mime, "image/") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") ||
		strings.HasPrefix(mime, "font/") ||
		strings.HasPrefix(mime, "application/") ||
		strings.HasPrefix(mime, "binary/") {
		return true
	}
	return false
}

func isBotBlocked(status int, content string) bool {
	if status != http.StatusForbidden && status != http.StatusServiceUnavailable {
		return false
	}
	lower := strings.ToLower(content)
	for _, marker := range []string{"cloudflare", "captcha", "challenge", "blocked", "access denied", "bot detection"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseRetryAfter(val string) time.Duration {
	val = strings.TrimSpace(val)
	if val == "" {
		return time.Second
	}
	if secs, err := strconv.ParseFloat(val, 64); err == nil && secs >= 0 {
		d := time.Duration(secs * float64(time.Second))
		if d > fetchRetryAfterMax {
			return fetchRetryAfterMax
		}
		return d
	}
	if t, err := http.ParseTime(val); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		if d > fetchRetryAfterMax {
			return fetchRetryAfterMax
		}
		return d
	}
	return time.Second
}

type loadedPage struct {
	rawBytes       []byte
	content        string
	rawContentType string
	mime           string
	finalURL       string
	status         int
	truncated      bool
	bodyless       bool
}

// loadPage fetches a URL with omp's user-agent rotation on bot-blocks, bounded 429 retry,
// and fetchMaxBytes streaming read cap.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:115-218
func loadPage(ctx context.Context, client *http.Client, targetURL string, maxBytes int64) (loadedPage, error) {
	var lastErr error
	retried429 := false

	for attempt := 0; attempt < len(fetchUserAgents); attempt++ {
		if err := ctx.Err(); err != nil {
			return loadedPage{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return loadedPage{}, err
		}
		req.Header.Set("User-Agent", fetchUserAgents[attempt])
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		req.Header.Set("Accept-Encoding", "identity")

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return loadedPage{}, ctx.Err()
			}
			lastErr = err
			continue
		}

		finalURL := targetURL
		if resp.Request != nil && resp.Request.URL != nil {
			finalURL = resp.Request.URL.String()
		}
		rawCT := resp.Header.Get("Content-Type")
		mime := normalizeMime(rawCT)

		if resp.StatusCode == http.StatusTooManyRequests && !retried429 {
			retried429 = true
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if err := fetchRetrySleep(ctx, delay); err != nil {
				return loadedPage{}, err
			}
			attempt--
			continue
		}

		data, truncated, readErr := readBodyCapped(resp.Body, maxBytes)
		resp.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return loadedPage{}, ctx.Err()
			}
			lastErr = readErr
			continue
		}

		content := decodeBody(data, rawCT)
		if isBotBlocked(resp.StatusCode, content) && attempt < len(fetchUserAgents)-1 {
			continue
		}

		return loadedPage{
			rawBytes:       data,
			content:        content,
			rawContentType: rawCT,
			mime:           mime,
			finalURL:       finalURL,
			status:         resp.StatusCode,
			truncated:      truncated,
			bodyless:       len(data) == 0,
		}, nil
	}

	if lastErr != nil {
		return loadedPage{}, lastErr
	}
	return loadedPage{}, errors.New("Failed to fetch URL")
}

// Fetch retrieves a URL and renders it to text (or returns the raw body when raw is true)
// following omp's limits, redirect handling, content-type handling, and HTML-to-Markdown conversion.
// All requests go through the package's single http.Client.
//
// upstream: packages/coding-agent/src/tools/fetch.ts:renderUrl, buildUrlReadOutput
func Fetch(ctx context.Context, rawURL string, raw bool) (string, error) {
	return fetchWithClient(ctx, httpClient, rawURL, raw, fetchMaxBytes)
}

func fetchWithClient(ctx context.Context, client *http.Client, rawURL string, raw bool, maxBytes int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	normalized, err := normalizeFetchURL(rawURL)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(normalized, "pi-internal://") {
		return buildURLReadOutput(normalized, "text/plain", "internal", []string{"Internal protocol URL - no external content"}, ""), nil
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fetchDefaultTimeout)
		defer cancel()
	}

	page, err := loadPage(ctx, client, normalized, maxBytes)
	if err != nil {
		return "", fmt.Errorf("Failed to fetch URL: %w", err)
	}
	if page.status < 200 || page.status >= 300 {
		return "", fmt.Errorf("Failed to fetch URL (HTTP %d)", page.status)
	}
	if page.status == http.StatusNoContent {
		ct := page.mime
		if ct == "" {
			ct = "unknown"
		}
		return buildURLReadOutput(page.finalURL, ct, "no-content", []string{"HTTP 204 No Content"}, ""), nil
	}

	var notes []string
	if page.truncated {
		notes = append(notes, fmt.Sprintf("Response body exceeded %s and was cut mid-stream; content is incomplete", formatBytes(maxBytes)))
	}

	mime := page.mime

	// Non-text / binary payloads return omp's binary notice rather than corrupting text output.
	// upstream: packages/coding-agent/src/tools/fetch.ts:843-846, 1030-1040, 1347-1349
	if isNonTextMime(mime) || sampleLooksBinary(page.content) {
		ct := mime
		if ct == "" {
			ct = "application/octet-stream"
		}
		notice := fmt.Sprintf("[Binary content: %s, %s] %s", ct, formatBytes(int64(len(page.rawBytes))), page.finalURL)
		return buildURLReadOutput(page.finalURL, ct, "binary", notes, notice), nil
	}

	if raw {
		return buildURLReadOutput(page.finalURL, mime, "raw", notes, finalizeOutput(page.content)), nil
	}

	isHTML := strings.Contains(mime, "html") || strings.Contains(mime, "xhtml")
	isJSON := strings.Contains(mime, "json")
	isXML := strings.Contains(mime, "xml") && !isHTML
	isText := strings.Contains(mime, "text/plain") || strings.Contains(mime, "text/markdown")
	isFeed := strings.Contains(mime, "rss") || strings.Contains(mime, "atom") || strings.Contains(mime, "feed")

	if isJSON {
		return buildURLReadOutput(page.finalURL, mime, "json", notes, finalizeOutput(formatJSON(page.content))), nil
	}
	if isFeed || (isXML && (strings.Contains(page.content, "<rss") || strings.Contains(page.content, "<feed"))) {
		return buildURLReadOutput(page.finalURL, mime, "feed", notes, finalizeOutput(parseFeedToMarkdown(page.content, 10))), nil
	}
	if isText && !looksLikeHTML(page.content) {
		return buildURLReadOutput(page.finalURL, mime, "text", notes, finalizeOutput(page.content)), nil
	}

	if isHTML || looksLikeHTML(page.content) {
		// Check page-specific markdown / feed alternates from <head>.
		// upstream: packages/coding-agent/src/tools/fetch.ts:1408-1484
		alternates := parseAlternateLinks(page.content, page.finalURL)
		for _, alt := range alternates {
			if alt.kind != "markdown" {
				continue
			}
			altPage, altErr := loadPage(ctx, client, alt.href, maxBytes)
			if altErr == nil && altPage.status >= 200 && altPage.status < 300 &&
				len(strings.TrimSpace(altPage.content)) > 100 && !looksLikeHTML(altPage.content) {
				altNotes := append([]string{}, notes...)
				altNotes = append(altNotes, "Used markdown alternate: "+alt.href)
				if altPage.truncated {
					altNotes = append(altNotes, fmt.Sprintf("Response body exceeded %s and was cut mid-stream; content is incomplete", formatBytes(maxBytes)))
				}
				return buildURLReadOutput(page.finalURL, "text/markdown", "alternate-markdown", altNotes, finalizeOutput(altPage.content)), nil
			}
		}
		feedChecked := 0
		for _, alt := range alternates {
			if alt.kind != "feed" {
				continue
			}
			if feedChecked >= 2 {
				break
			}
			feedChecked++
			altPage, altErr := loadPage(ctx, client, alt.href, maxBytes)
			if altErr == nil && altPage.status >= 200 && altPage.status < 300 && len(strings.TrimSpace(altPage.content)) > 200 {
				altNotes := append([]string{}, notes...)
				altNotes = append(altNotes, "Used feed alternate: "+alt.href)
				return buildURLReadOutput(page.finalURL, "application/feed", "alternate-feed", altNotes, finalizeOutput(parseFeedToMarkdown(altPage.content, 10))), nil
			}
		}

		md := HTMLToMarkdown(page.content, page.finalURL)
		if strings.TrimSpace(md) != "" {
			return buildURLReadOutput(page.finalURL, mime, "native", notes, md), nil
		}
		return buildURLReadOutput(page.finalURL, mime, "raw-html", notes, finalizeOutput(page.content)), nil
	}

	return buildURLReadOutput(page.finalURL, mime, "raw", notes, finalizeOutput(page.content)), nil
}

// buildURLReadOutput formats the fetched URL metadata header and body matching omp's buildUrlReadOutput.
// upstream: packages/coding-agent/src/tools/fetch.ts:1745-1756
func buildURLReadOutput(finalURL, contentType, method string, notes []string, content string) string {
	var b strings.Builder
	b.WriteString("URL: ")
	b.WriteString(finalURL)
	b.WriteByte('\n')
	b.WriteString("Content-Type: ")
	b.WriteString(contentType)
	b.WriteByte('\n')
	b.WriteString("Method: ")
	b.WriteString(method)
	b.WriteByte('\n')
	if len(notes) > 0 {
		b.WriteString("Notes: ")
		b.WriteString(strings.Join(notes, "; "))
		b.WriteByte('\n')
	}
	b.WriteString("\n---\n\n")
	b.WriteString(content)
	return b.String()
}

func formatJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}

type alternateLink struct {
	href string
	kind string // "markdown" or "feed"
}

func extractHeadHTML(rawHTML string) string {
	const scanLimit = 256 * 1024
	window := rawHTML
	if len(window) > scanLimit {
		window = window[:scanLimit]
	}
	loc := headOpenRe.FindStringIndex(window)
	if loc == nil {
		if len(rawHTML) > 32*1024 {
			return rawHTML[:32*1024]
		}
		return rawHTML
	}
	headStart := loc[0]
	tagEndRel := strings.IndexByte(rawHTML[headStart:], '>')
	if tagEndRel < 0 || tagEndRel > 4096 {
		end := min(headStart+32*1024, len(rawHTML))
		return rawHTML[headStart:end]
	}
	bodyStart := headStart + tagEndRel + 1
	tailEnd := min(bodyStart+128*1024, len(rawHTML))
	tail := rawHTML[bodyStart:tailEnd]
	if closeLoc := headCloseRe.FindStringIndex(tail); closeLoc != nil {
		return rawHTML[headStart : bodyStart+closeLoc[1]]
	}
	return rawHTML[headStart:tailEnd]
}

// parseAlternateLinks finds page-specific markdown and feed alternates in <head>.
// upstream: packages/coding-agent/src/tools/fetch.ts:390-432
func parseAlternateLinks(rawHTML, pageURL string) []alternateLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	pagePath := strings.TrimRight(base.Path, "/")
	head := extractHeadHTML(rawHTML)
	tags := linkTagRe.FindAllString(head, -1)

	var links []alternateLink
	for _, tag := range tags {
		_, attrs, _, _, ok := parseStartTag(tag, 0)
		if !ok || attrs == nil {
			continue
		}
		rel := strings.ToLower(attrs["rel"])
		isAlt := false
		for _, tok := range strings.Fields(rel) {
			if tok == "alternate" {
				isAlt = true
				break
			}
		}
		if !isAlt {
			continue
		}
		href := strings.TrimSpace(attrs["href"])
		typ := strings.ToLower(strings.TrimSpace(attrs["type"]))
		if href == "" {
			continue
		}
		if strings.Contains(href, "RecentChanges") ||
			strings.Contains(href, "Special:") ||
			strings.Contains(href, "/feed/") ||
			strings.Contains(href, "action=feed") {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref)
		altPath := resolved.Path
		pageSpecific := altPath == pagePath ||
			(pagePath != "" && (strings.HasPrefix(altPath, pagePath+"/") || strings.HasPrefix(altPath, pagePath+".")))
		if !pageSpecific {
			continue
		}
		if strings.Contains(typ, "markdown") {
			links = append(links, alternateLink{href: resolved.String(), kind: "markdown"})
		} else if pagePath != "" && (strings.Contains(typ, "rss") || strings.Contains(typ, "atom") || strings.Contains(typ, "feed")) {
			links = append(links, alternateLink{href: resolved.String(), kind: "feed"})
		}
	}
	return links
}

func cleanFeedText(raw string) string {
	s := cdataStripRe.ReplaceAllString(raw, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`).Replace(s)
	s = anyHTMLTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// parseFeedToMarkdown renders RSS 2.0 or Atom feeds into omp's Markdown format.
// upstream: packages/coding-agent/src/tools/fetch.ts:484-535
func parseFeedToMarkdown(content string, maxItems int) string {
	var rss struct {
		Channel *struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(content), &rss); err == nil && rss.Channel != nil {
		title := cleanFeedText(rss.Channel.Title)
		if title == "" {
			title = "RSS Feed"
		}
		var b strings.Builder
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
		items := rss.Channel.Items
		if len(items) > maxItems {
			items = items[:maxItems]
		}
		for _, item := range items {
			itTitle := cleanFeedText(item.Title)
			if itTitle == "" {
				itTitle = "Untitled"
			}
			link := cleanFeedText(item.Link)
			pubDate := cleanFeedText(item.PubDate)
			desc := cleanFeedText(item.Description)

			b.WriteString("## ")
			b.WriteString(itTitle)
			b.WriteByte('\n')
			if pubDate != "" {
				b.WriteByte('*')
				b.WriteString(pubDate)
				b.WriteString("*\n\n")
			}
			if desc != "" {
				runes := []rune(desc)
				if len(runes) > 500 {
					b.WriteString(string(runes[:500]))
					b.WriteString("...")
				} else {
					b.WriteString(desc)
				}
				b.WriteString("\n\n")
			}
			if link != "" {
				b.WriteString("[Read more](")
				b.WriteString(link)
				b.WriteString(")\n\n")
			}
			b.WriteString("---\n\n")
		}
		return b.String()
	}

	var atom struct {
		XMLName xml.Name `xml:"feed"`
		Title   string   `xml:"title"`
		Entries []struct {
			Title string `xml:"title"`
			Links []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
			Updated string `xml:"updated"`
			Summary string `xml:"summary"`
			Content string `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal([]byte(content), &atom); err == nil && atom.XMLName.Local == "feed" {
		title := cleanFeedText(atom.Title)
		if title == "" {
			title = "Atom Feed"
		}
		var b strings.Builder
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
		entries := atom.Entries
		if len(entries) > maxItems {
			entries = entries[:maxItems]
		}
		for _, entry := range entries {
			etTitle := cleanFeedText(entry.Title)
			if etTitle == "" {
				etTitle = "Untitled"
			}
			link := ""
			if len(entry.Links) > 0 {
				link = strings.TrimSpace(entry.Links[0].Href)
			}
			updated := cleanFeedText(entry.Updated)
			summary := cleanFeedText(entry.Summary)
			if summary == "" {
				summary = cleanFeedText(entry.Content)
			}

			b.WriteString("## ")
			b.WriteString(etTitle)
			b.WriteByte('\n')
			if updated != "" {
				b.WriteByte('*')
				b.WriteString(updated)
				b.WriteString("*\n\n")
			}
			if summary != "" {
				runes := []rune(summary)
				if len(runes) > 500 {
					b.WriteString(string(runes[:500]))
					b.WriteString("...")
				} else {
					b.WriteString(summary)
				}
				b.WriteString("\n\n")
			}
			if link != "" {
				b.WriteString("[Read more](")
				b.WriteString(link)
				b.WriteString(")\n\n")
			}
			b.WriteString("---\n\n")
		}
		return b.String()
	}

	return content
}
