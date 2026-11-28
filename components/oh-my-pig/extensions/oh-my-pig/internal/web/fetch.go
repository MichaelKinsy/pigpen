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
// upstream: packages/coding-agent/src/web/scrapers/types.ts:54
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

// upstream: packages/coding-agent/src/web/scrapers/types.ts:57-61
var fetchUserAgents = []string{
	"curl/8.0",
	"Mozilla/5.0 (compatible; TextBot/1.0)",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
}

var (
	collapsedSchemeRe = regexp.MustCompile(`(?i)^(https?):/([^/])`)
	multiBlankLineRe  = regexp.MustCompile(`\n{3,}`)
	linkTagRe         = regexp.MustCompile(`(?i)<link\b[^>]*>`)
	feedSkipRe        = regexp.MustCompile(`/feed(?:/|$|\?)`)
	commentsSkipRe    = regexp.MustCompile(`/comments?/`)
	headOpenRe        = regexp.MustCompile(`(?i)<head[\s>]`)
	headCloseRe       = regexp.MustCompile(`(?i)</head\s*>`)
	cdataStripRe      = regexp.MustCompile(`<!\[CDATA\[|\]\]>`)
	anyHTMLTagRe      = regexp.MustCompile(`<[^>]+>`)
)

// repairCollapsedScheme fixes `https:/example.com` -> `https://example.com`.
// upstream: packages/tui/src/tools/fetch.ts:43-46
func repairCollapsedScheme(raw string) string {
	return collapsedSchemeRe.ReplaceAllString(raw, "$1://$2")
}

// normalizeFetchURL repairs a collapsed scheme and adds https:// when missing.
// upstream: packages/coding-agent/src/tools/fetch.ts:141-147
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
// upstream: packages/coding-agent/src/tools/fetch.ts:202-204
func normalizeMime(contentType string) string {
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = contentType[:idx]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// finalizeOutput collapses runs of 3+ newlines and trims surrounding whitespace.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:119-121
func finalizeOutput(content string) string {
	return strings.TrimSpace(multiBlankLineRe.ReplaceAllString(content, "\n\n"))
}

// looksLikeHTML inspects the leading tag to decide whether content is HTML.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:417-425
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
// upstream: packages/coding-agent/src/tools/fetch.ts:845-857
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

const (
	// challengePageMaxChars caps the HTML length inspected for a live js_challenge input.
	// upstream: packages/coding-agent/src/web/scrapers/types.ts:71
	challengePageMaxChars = 64 * 1024
	// minRenderedChars is the minimum rendered character length accepted from a reader.
	// upstream: packages/coding-agent/src/tools/fetch.ts:580
	minRenderedChars = 100
	// maxOmittedMetaNames caps the <meta> names listed in meta_omitted.
	// upstream: packages/coding-agent/src/tools/fetch.ts:577
	maxOmittedMetaNames = 25
)

var (
	// blockedStatuses trigger user-agent rotation regardless of body content.
	// upstream: packages/coding-agent/src/web/scrapers/types.ts:68
	blockedStatuses = map[int]bool{
		403: true,
		429: true,
		503: true,
		999: true,
	}

	// readerMetaNames are the lowercased <meta> names preserved in reader front matter.
	// upstream: packages/coding-agent/src/tools/fetch.ts:566-571
	readerMetaNames = map[string]bool{
		"description":            true,
		"author":                 true,
		"article:published_time": true,
		"article:modified_time":  true,
	}

	// jsGatePhrases identify pages that render nothing without JavaScript.
	// upstream: packages/coding-agent/src/tools/fetch.ts:640-647
	jsGatePhrases = []string{
		"enable javascript",
		"javascript required",
		"javascript is required",
		"turn on javascript",
		"please enable javascript",
		"browser not supported",
	}

	frontMatterKeyRe = regexp.MustCompile(`^(?:("(?:\\.|[^\\"])*")|([^"][^\n]*?)): `)
	htmlCommentRe    = regexp.MustCompile(`<!--[\s\S]*?-->`)
	templateElemRe   = regexp.MustCompile(`(?is)<template\b[^>]*>[\s\S]*?</template>`)
	scriptStyleRe    = regexp.MustCompile(`(?is)<(?:script|style|textarea|title)\b[^>]*>[\s\S]*?</(?:script|style|textarea|title)>`)
	inputTagRe       = regexp.MustCompile(`(?i)<input\b[^>]*>`)
)

// hasChallengeForm reports whether a small HTML page contains a live <input name="js_challenge"> outside <template>/comments/raw-text.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:80-96
func hasChallengeForm(rawHTML string) bool {
	if len(rawHTML) > challengePageMaxChars {
		return false
	}
	cleaned := htmlCommentRe.ReplaceAllString(rawHTML, "")
	cleaned = templateElemRe.ReplaceAllString(cleaned, "")
	cleaned = scriptStyleRe.ReplaceAllString(cleaned, "")
	for _, tag := range inputTagRe.FindAllString(cleaned, -1) {
		_, attrs, _, _, ok := parseStartTag(tag, 0)
		if ok && attrs != nil && attrs["name"] == "js_challenge" {
			return true
		}
	}
	return false
}

// botWallOf names the bot protection serving a challenge or block page, if any.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:103-113
func botWallOf(resp *http.Response, contentType, content string) string {
	if !strings.Contains(contentType, "html") {
		return ""
	}
	if resp.Header.Get("Cf-Mitigated") == "challenge" {
		return "Cloudflare"
	}
	if hasChallengeForm(content) {
		return "JavaScript challenge"
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return ""
	}
	if strings.Contains(content, "_cf_chl_opt") {
		return "Cloudflare"
	}
	if strings.Contains(content, "captcha-delivery.com") {
		return "DataDome"
	}
	if strings.Contains(content, "px-captcha") {
		return "PerimeterX"
	}
	if strings.Contains(content, "blocked by network security") {
		return "network security block"
	}
	return ""
}

func isLowQualityOutput(content string) bool {
	if len(content) >= 1024 {
		return false
	}
	lower := strings.ToLower(content)
	for _, phrase := range jsGatePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// pruneFrontMatter drops native front-matter <meta> keys outside readerMetaNames and appends a meta_omitted summary.
// upstream: packages/coding-agent/src/tools/fetch.ts:587-623
func pruneFrontMatter(markdown string) string {
	if !strings.HasPrefix(markdown, "---\n") {
		return markdown
	}
	endRel := strings.Index(markdown[3:], "\n---\n")
	if endRel < 0 {
		return markdown
	}
	end := 3 + endRel
	var kept, omitted []string
	for _, line := range strings.Split(markdown[4:end], "\n") {
		m := frontMatterKeyRe.FindStringSubmatch(line)
		if m == nil {
			return markdown
		}
		var name string
		if m[1] != "" {
			if err := json.Unmarshal([]byte(m[1]), &name); err != nil {
				return markdown
			}
		} else {
			name = m[2]
		}
		if strings.HasPrefix(name, "meta-") && !readerMetaNames[strings.ToLower(name[5:])] {
			omitted = append(omitted, name[5:])
		} else {
			kept = append(kept, line)
		}
	}
	if len(omitted) == 0 {
		return markdown
	}
	rest := markdown[end:]
	head := strings.Join(kept, "\n")
	var remaining string
	if head != "" {
		remaining = "---\n" + head + rest
	} else {
		remaining = rest[5:]
	}
	if len(strings.TrimSpace(remaining)) <= minRenderedChars {
		return markdown
	}
	shown := omitted
	more := 0
	if len(omitted) > maxOmittedMetaNames {
		shown = omitted[:maxOmittedMetaNames]
		more = len(omitted) - maxOmittedMetaNames
	}
	note := strings.Join(shown, ", ")
	if more > 0 {
		note += fmt.Sprintf(" (+%d more)", more)
	}
	note += "; read the URL with :raw for their values"
	quotedBytes, _ := json.Marshal(note)
	quoted := string(quotedBytes)
	quoted = strings.ReplaceAll(quoted, "\u0085", `\u0085`)
	quoted = strings.ReplaceAll(quoted, "\u2028", `\u2028`)
	quoted = strings.ReplaceAll(quoted, "\u2029", `\u2029`)
	if head != "" {
		return "---\n" + head + "\nmeta_omitted: " + quoted + rest
	}
	return "---\nmeta_omitted: " + quoted + rest
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
	ok             bool
	truncated      bool
	bodyless       bool
	blockedBy      string
	errMsg         string
}

// loadPage fetches a URL with omp v19.1.1's status-based user-agent rotation (403/429/503/999),
// bot-wall detection, bounded 429 retry on the last agent, and streaming byte cap.
// upstream: packages/coding-agent/src/web/scrapers/types.ts:172-304
func loadPage(ctx context.Context, client *http.Client, targetURL string, maxBytes int64) (loadedPage, error) {
	return loadPageWithHeaders(ctx, client, targetURL, maxBytes, fetchDefaultTimeout, nil)
}

func loadPageWithHeaders(
	ctx context.Context,
	client *http.Client,
	targetURL string,
	maxBytes int64,
	timeout time.Duration,
	extraHeaders map[string]string,
) (loadedPage, error) {
	var lastErr string
	var lastBlocked *loadedPage
	retried429 := false

	rotates := true
	for k := range extraHeaders {
		if strings.EqualFold(k, "user-agent") {
			rotates = false
			break
		}
	}

	for attempt := 0; attempt < len(fetchUserAgents); attempt++ {
		if err := ctx.Err(); err != nil {
			return loadedPage{}, err
		}
		reqCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return loadedPage{}, err
		}
		req.Header.Set("User-Agent", fetchUserAgents[attempt])
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		req.Header.Set("Accept-Encoding", "identity")
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			if ctx.Err() != nil {
				return loadedPage{}, ctx.Err()
			}
			lastErr = err.Error()
			if attempt == len(fetchUserAgents)-1 {
				if lastBlocked != nil {
					return *lastBlocked, nil
				}
				return loadedPage{finalURL: targetURL, ok: false, errMsg: lastErr}, nil
			}
			continue
		}

		finalURL := targetURL
		if resp.Request != nil && resp.Request.URL != nil {
			finalURL = resp.Request.URL.String()
		}
		rawCT := resp.Header.Get("Content-Type")
		mime := normalizeMime(rawCT)
		respOK := resp.StatusCode >= 200 && resp.StatusCode < 300
		lastAgent := !rotates || attempt == len(fetchUserAgents)-1

		// Rotate before reading body or sleeping on 429 for any refused status (403, 429, 503, 999).
		// upstream: packages/coding-agent/src/web/scrapers/types.ts:216-233
		if !lastAgent && blockedStatuses[resp.StatusCode] {
			if lastBlocked == nil {
				if wall := botWallOf(resp, mime, ""); wall != "" {
					cp := loadedPage{
						mime:      mime,
						finalURL:  finalURL,
						status:    resp.StatusCode,
						ok:        false,
						blockedBy: wall,
					}
					lastBlocked = &cp
				}
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if cancel != nil {
				cancel()
			}
			continue
		}

		// Rate limited under the last user agent: retry once, honoring bounded Retry-After.
		// upstream: packages/coding-agent/src/web/scrapers/types.ts:235-249
		if resp.StatusCode == http.StatusTooManyRequests && !retried429 {
			retried429 = true
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if cancel != nil {
				cancel()
			}
			if err := fetchRetrySleep(ctx, delay); err != nil {
				return loadedPage{}, err
			}
			attempt--
			continue
		}

		data, truncated, readErr := readBodyCapped(resp.Body, maxBytes)
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return loadedPage{}, ctx.Err()
			}
			lastErr = readErr.Error()
			if attempt == len(fetchUserAgents)-1 {
				if lastBlocked != nil {
					return *lastBlocked, nil
				}
				return loadedPage{finalURL: targetURL, ok: false, errMsg: lastErr}, nil
			}
			continue
		}

		if len(data) == 0 {
			if !respOK && lastBlocked != nil {
				if !lastAgent {
					continue
				}
				return *lastBlocked, nil
			}
			return loadedPage{
				rawContentType: rawCT,
				mime:           mime,
				finalURL:       finalURL,
				status:         resp.StatusCode,
				ok:             respOK,
				bodyless:       true,
			}, nil
		}

		content := decodeBody(data, rawCT)
		blockedBy := botWallOf(resp, mime, content)
		res := loadedPage{
			rawBytes:       data,
			content:        content,
			rawContentType: rawCT,
			mime:           mime,
			finalURL:       finalURL,
			status:         resp.StatusCode,
			ok:             respOK && blockedBy == "",
			truncated:      truncated,
			blockedBy:      blockedBy,
		}
		if blockedBy != "" && !lastAgent {
			if lastBlocked == nil || respOK {
				cp := res
				lastBlocked = &cp
			}
			continue
		}
		if !respOK && lastBlocked != nil && (blockedBy == "" || lastBlocked.status < 300) {
			if !lastAgent {
				continue
			}
			return *lastBlocked, nil
		}
		return res, nil
	}

	if lastBlocked != nil {
		return *lastBlocked, nil
	}
	return loadedPage{finalURL: targetURL, ok: false, errMsg: lastErr}, nil
}

// Fetch retrieves a URL and renders it to text (or returns the raw body verbatim when raw is true)
// following omp v19.1.1's limits, redirect handling, user-agent rotation, bot-wall detection,
// site scrapers, content-type handling, and HTML-to-Markdown conversion.
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

	// Try site-specific scrapers first when not in raw mode; each request gets its own timeout.
	// upstream: packages/coding-agent/src/tools/fetch.ts (specialHandlers)
	if !raw {
		if res := runSiteScrapers(ctx, client, normalized); res != nil {
			finalURL := res.finalURL
			if finalURL == "" {
				finalURL = normalized
			}
			ct := res.contentType
			if ct == "" {
				ct = "text/markdown"
			}
			return buildURLReadOutput(finalURL, ct, res.method, res.notes, finalizeOutput(res.content)), nil
		}
	}

	page, err := loadPage(ctx, client, normalized, maxBytes)
	if err != nil {
		return "", err
	}

	// A 2xx challenge page is reported as Method: blocked (unless :raw requests it verbatim).
	// upstream: packages/coding-agent/src/tools/fetch.ts:1230-1269
	challenge := page.blockedBy != "" && page.status > 0 && page.status < 300
	if challenge && !raw {
		wall := fmt.Sprintf("bot protection (%s, HTTP %d)", page.blockedBy, page.status)
		return buildURLReadOutput(
			page.finalURL,
			page.mime,
			"blocked",
			[]string{fmt.Sprintf("Blocked by %s: the site served a challenge instead of the page, and no reader backend got past it; the browser tool runs the page's JavaScript like Chrome and may get through", wall)},
			"",
		), nil
	}

	// Failed fetches return a formatted result with Method: failed, matching omp.
	// Raw mode returns only the content (empty string), matching buildUrlReadOutput.
	// upstream: packages/coding-agent/src/tools/fetch.ts:1271-1286, 1978
	if !page.ok && !(challenge && raw) {
		if raw {
			return "", nil
		}
		finalURL := page.finalURL
		if finalURL == "" {
			finalURL = normalized
		}
		ct := page.mime
		if ct == "" {
			ct = "unknown"
		}
		var failNotes []string
		if page.status > 0 {
			failNotes = append(failNotes, fmt.Sprintf("Failed to fetch URL (HTTP %d)", page.status))
		} else {
			failNotes = append(failNotes, "Failed to fetch URL")
		}
		if page.blockedBy != "" {
			failNotes = append(failNotes, fmt.Sprintf("Blocked by bot protection (%s)", page.blockedBy))
		}
		if page.errMsg != "" {
			failNotes = append(failNotes, "Cause: "+page.errMsg)
		}
		return buildURLReadOutput(finalURL, ct, "failed", failNotes, ""), nil
	}

	if page.status == http.StatusNoContent {
		if raw {
			return "", nil
		}
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
	// Raw mode returns only the notice content without the wrapper header.
	// upstream: packages/coding-agent/src/tools/fetch.ts:845-857, 1978
	if isNonTextMime(mime) || sampleLooksBinary(page.content) {
		ct := mime
		if ct == "" {
			ct = "application/octet-stream"
		}
		notice := fmt.Sprintf("[Binary content: %s, %s] %s", ct, formatBytes(int64(len(page.rawBytes))), page.finalURL)
		if raw {
			return notice, nil
		}
		return buildURLReadOutput(page.finalURL, ct, "binary", notes, notice), nil
	}

	// Raw mode returns the response body verbatim with no wrapper header and no trimming.
	// upstream: packages/coding-agent/src/tools/fetch.ts:1473-1484, 1978
	if raw {
		return page.content, nil
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
		// 5A: Page-specific markdown alternate (try only the first markdown alternate).
		// upstream: packages/coding-agent/src/tools/fetch.ts:1531-1550
		alternates := parseAlternateLinks(page.content, page.finalURL)
		for _, alt := range alternates {
			if alt.kind != "markdown" {
				continue
			}
			altPage, altErr := loadPage(ctx, client, alt.href, maxBytes)
			if altErr == nil && altPage.ok &&
				len(strings.TrimSpace(altPage.content)) > minRenderedChars && !looksLikeHTML(altPage.content) {
				altNotes := append([]string{}, notes...)
				altNotes = append(altNotes, "Used markdown alternate: "+alt.href)
				if altPage.truncated {
					altNotes = append(altNotes, fmt.Sprintf("Response body exceeded %s and was cut mid-stream; content is incomplete", formatBytes(maxBytes)))
				}
				return buildURLReadOutput(altPage.finalURL, "text/markdown", "alternate-markdown", altNotes, finalizeOutput(altPage.content)), nil
			}
			break
		}
		// Feed alternate helper: used only when page rendering fails or is low quality.
		// upstream: packages/coding-agent/src/tools/fetch.ts:1574-1593
		tryFeedFallback := func() (string, bool) {
			checked := 0
			for _, alt := range alternates {
				if alt.kind != "feed" {
					continue
				}
				if checked >= 2 {
					break
				}
				checked++
				altPage, altErr := loadPage(ctx, client, alt.href, maxBytes)
				if altErr == nil && altPage.ok && len(strings.TrimSpace(altPage.content)) > 200 {
					altNotes := append([]string{}, notes...)
					altNotes = append(altNotes, "Used feed alternate: "+alt.href)
					return buildURLReadOutput(altPage.finalURL, "application/feed", "alternate-feed", altNotes, finalizeOutput(parseFeedToMarkdown(altPage.content, 10))), true
				}
			}
			return "", false
		}

		// 5C: Native HTML-to-Markdown rendering with 100-char minimum and JS-gate check.
		// upstream: packages/coding-agent/src/tools/fetch.ts:773-793, 1597-1719
		md := HTMLToMarkdown(page.content, page.finalURL)
		if len(strings.TrimSpace(md)) <= minRenderedChars {
			notes = append(notes, "html rendering failed (no reader backend produced usable output)")
			if out, ok := tryFeedFallback(); ok {
				return out, nil
			}
			return buildURLReadOutput(page.finalURL, mime, "raw-html", notes, finalizeOutput(page.content)), nil
		}

		if isLowQualityOutput(md) {
			if out, ok := tryFeedFallback(); ok {
				return out, nil
			}
			notes = append(notes, "Page appears to require JavaScript or is mostly navigation")
		}

		outMD := finalizeOutput(pruneFrontMatter(md))
		return buildURLReadOutput(page.finalURL, mime, "native", notes, outMD), nil
	}

	return buildURLReadOutput(page.finalURL, mime, "raw", notes, finalizeOutput(page.content)), nil
}

// buildURLReadOutput formats the fetched URL metadata header and body matching omp's buildUrlReadOutput.
// upstream: packages/coding-agent/src/tools/fetch.ts:1977-1989
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
// upstream: packages/coding-agent/src/tools/fetch.ts:356-399
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
			feedSkipRe.MatchString(href) ||
			commentsSkipRe.MatchString(href) ||
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
// upstream: packages/coding-agent/src/tools/fetch.ts:467-518
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
