package rpiv_web_tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The built-in fetch pipeline: HTTP client, content-type guards and HTML-to-text extraction, used when the
// active provider has no fetch of its own. upstream: providers/fetch-helpers.ts.

const (
	userAgent         = "Mozilla/5.0 (compatible; rpiv-pi/1.0)"
	fetchAcceptHeader = "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5"
)

var numericEntityRe = regexp.MustCompile(`&#(\d+);`)

func decodeHTMLEntities(text string) string {
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", `"`)
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	if !strings.Contains(text, "&#") {
		return text
	}
	return numericEntityRe.ReplaceAllStringFunc(text, func(m string) string {
		n, err := strconv.ParseFloat(numericEntityRe.FindStringSubmatch(m)[1], 64)
		if err != nil {
			return m
		}
		// String.fromCharCode keeps the low 16 bits; a lone surrogate cannot be UTF-8 and reads as U+FFFD.
		r := rune(uint64(n) & 0xFFFF)
		if !utf8.ValidRune(r) {
			r = utf8.RuneError
		}
		return string(r)
	})
}

func isHTMLContentType(ct string) bool { return strings.Contains(ct, "text/html") }

// assertTextContentType refuses image, video and audio bodies.
func assertTextContentType(ct string) error {
	for _, p := range []string{"image/", "video/", "audio/"} {
		if strings.Contains(ct, p) {
			return fmt.Errorf("Unsupported content type: %s. web_fetch supports text pages only.", ct)
		}
	}
	return nil
}

// fetchViaGenericHTML fetches a URL with the built-in pipeline: href is the parsed URL that is requested, rawURL the
// one the caller gave, which an HTTP error names. upstream: fetch-helpers.ts:124-139.
func fetchViaGenericHTML(ctx context.Context, href, rawURL string, raw bool) (*fetchResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", href, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", fetchAcceptHeader)
	res, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d %s for %s", res.StatusCode, http.StatusText(res.StatusCode), rawURL)
	}
	contentType := res.Header.Get("Content-Type")
	if err := assertTextContentType(contentType); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	body := string(data)
	out := &fetchResponse{Text: body, ContentType: contentType}
	if !raw && isHTMLContentType(contentType) {
		out.Title = extractTitle(body)
		out.Text = htmlToText(body)
	}
	if cl := res.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseFloat(cl, 64); err == nil {
			out.ContentLength = &n
		}
	}
	return out, nil
}
