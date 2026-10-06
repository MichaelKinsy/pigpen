// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The shared HTTP seam. upstream: providers/fetch-helpers.ts plus the per-provider fetch calls.
//
// Go has no global fetch to stub, so the seam is this interface: the arms take one, the twins hand in a canned
// response, and production hands in the net/http client. The provider arms keep the original's shape — one guard, one
// request, one status check, one normalisation.

// httpResponse is the response the arms read: status, body and the two headers they care about. upstream: the Response
// object of providers/fetch-helpers.ts.
type httpResponse struct {
	Status           int
	Body             string
	ContentType      string
	HasContentType   bool
	ContentLength    string
	HasContentLength bool
}

// httpRequest is one outgoing request. upstream: the RequestInit built by buildFetchRequestInit plus each arm's own
// method, headers and body.
type httpRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// httpDoer performs a request. upstream: the global fetch the arms call.
type httpDoer interface {
	Do(req httpRequest) (httpResponse, error)
}

// The shared request defaults. upstream: providers/fetch-helpers.ts USER_AGENT and FETCH_ACCEPT_HEADER.
const (
	userAgent         = "Mozilla/5.0 (compatible; rpiv-pi/1.0)"
	fetchAcceptHeader = "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5"
	htmlContentType   = "text/html"
)

// binaryContentTypePrefixes are the content types web_fetch refuses. upstream: providers/fetch-helpers.ts
// BINARY_CONTENT_TYPE_PREFIXES.
var binaryContentTypePrefixes = []string{"image/", "video/", "audio/"}

// htmlToText is the shared HTML-to-text conversion the wrapped providers use: drop the non-content blocks, turn block
// closers into newlines, drop the remaining tags, decode the entities, collapse the whitespace. upstream:
// providers/fetch-helpers.ts htmlToText.
func htmlToText(html string) string {
	text := stripNonContentBlocks(html)
	text = convertBlockTagsToNewlines(text)
	text = stripRemainingTags(text)
	text = decodeHTMLEntities(text)
	text = collapseWhitespace(text)
	return strings.TrimSpace(text)
}

var (
	scriptBlockRegex     = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`)
	styleBlockRegex      = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`)
	noscriptBlockRegex   = regexp.MustCompile(`(?is)<noscript[\s\S]*?</noscript>`)
	blockCloserRegex     = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br|blockquote|pre|section|article|header|footer|nav|details|summary)>`)
	selfClosingBrRegex   = regexp.MustCompile(`(?i)<br\s*/?>`)
	anyRemainingTagRegex = regexp.MustCompile(`<[^>]+>`)
	titleTagRegex        = regexp.MustCompile(`(?is)<title[^>]*>([\s\S]*?)</title>`)
	numericEntityRegex   = regexp.MustCompile(`&#(\d+);`)
	horizontalSpaceRun   = regexp.MustCompile(`[ \t]+`)
	blankLineRun         = regexp.MustCompile(`\n{3,}`)
)

func stripNonContentBlocks(html string) string {
	out := scriptBlockRegex.ReplaceAllString(html, "")
	out = styleBlockRegex.ReplaceAllString(out, "")
	return noscriptBlockRegex.ReplaceAllString(out, "")
}

func convertBlockTagsToNewlines(text string) string {
	out := blockCloserRegex.ReplaceAllString(text, "\n")
	return selfClosingBrRegex.ReplaceAllString(out, "\n")
}

func stripRemainingTags(text string) string { return anyRemainingTagRegex.ReplaceAllString(text, " ") }

// decodeHTMLEntities decodes the six named entities and any numeric one, in the original's order so a double-encoded
// sequence decodes the way it does there. upstream: providers/fetch-helpers.ts decodeHtmlEntities.
func decodeHTMLEntities(text string) string {
	replacements := [][2]string{
		{"&amp;", "&"},
		{"&lt;", "<"},
		{"&gt;", ">"},
		{"&quot;", `"`},
		{"&#39;", "'"},
		{"&nbsp;", " "},
	}
	for _, r := range replacements {
		text = strings.ReplaceAll(text, r[0], r[1])
	}
	return numericEntityRegex.ReplaceAllStringFunc(text, func(m string) string {
		code, err := strconv.Atoi(numericEntityRegex.FindStringSubmatch(m)[1])
		if err != nil {
			return m
		}
		return string(rune(code))
	})
}

func collapseWhitespace(text string) string {
	out := horizontalSpaceRun.ReplaceAllString(text, " ")
	return blankLineRun.ReplaceAllString(out, "\n\n")
}

// extractTitle is the page title with its tags stripped, or absent when the document has none or an empty one. upstream:
// providers/fetch-helpers.ts extractTitle.
func extractTitle(html string) (string, bool) {
	match := titleTagRegex.FindStringSubmatch(html)
	if match == nil {
		return "", false
	}
	title := strings.TrimSpace(anyRemainingTagRegex.ReplaceAllString(match[1], ""))
	if title == "" {
		return "", false
	}
	return title, true
}

// isHTMLContentType reports whether a body should be run through the HTML pipeline. upstream:
// providers/fetch-helpers.ts isHtmlContentType.
func isHTMLContentType(contentType string) bool {
	return strings.Contains(contentType, htmlContentType)
}

// assertTextContentType refuses the binary types web_fetch cannot read. upstream: providers/fetch-helpers.ts
// assertTextContentType.
func assertTextContentType(contentType string) error {
	for _, prefix := range binaryContentTypePrefixes {
		if strings.Contains(contentType, prefix) {
			return fmt.Errorf("Unsupported content type: %s. web_fetch supports text pages only.", contentType)
		}
	}
	return nil
}

// buildFetchRequestInit is the header set the generic path sends. upstream: providers/fetch-helpers.ts
// buildFetchRequestInit.
func buildFetchRequestInit() map[string]string {
	return map[string]string{"User-Agent": userAgent, "Accept": fetchAcceptHeader}
}

// fetchURLOrThrow performs the request and turns a non-2xx into the uniform HTTP error. upstream:
// providers/fetch-helpers.ts fetchUrlOrThrow.
func fetchURLOrThrow(client httpDoer, url string) (httpResponse, error) {
	res, err := client.Do(httpRequest{Method: "GET", URL: url, Headers: buildFetchRequestInit()})
	if err != nil {
		return httpResponse{}, err
	}
	if !isOK(res.Status) {
		return httpResponse{}, fmt.Errorf("HTTP %d %s for %s", res.Status, statusText(res.Status), url)
	}
	return res, nil
}

// isOK is the 2xx test every arm and helper uses. upstream: the `!res.ok` checks.
func isOK(status int) bool { return status >= 200 && status <= 299 }

// statusText is the reason phrase for the statuses the port names; an unknown code reads as "Unknown Status", the way a
// runtime with no reason phrase does. upstream: Response.statusText.
func statusText(status int) string {
	switch status {
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	}
	return "Unknown Status"
}

// extractBodyAsText is the body, converted and titled when the response is HTML and raw was not asked for. upstream:
// providers/fetch-helpers.ts extractBodyAsText.
func extractBodyAsText(res httpResponse, contentType string, raw bool) (string, string, bool) {
	if !raw && isHTMLContentType(contentType) {
		title, hasTitle := extractTitle(res.Body)
		return htmlToText(res.Body), title, hasTitle
	}
	return res.Body, "", false
}

// fetchViaGenericHTML is the one-stop path for providers with no native fetch endpoint (Brave, Serper, SearXNG): request,
// assert the content type, extract the body, and build the envelope. upstream: providers/fetch-helpers.ts
// fetchViaGenericHtml.
func fetchViaGenericHTML(client httpDoer, url string, raw bool) (fetchResponse, error) {
	res, err := fetchURLOrThrow(client, url)
	if err != nil {
		return fetchResponse{}, err
	}
	contentType := ""
	if res.HasContentType {
		contentType = res.ContentType
	}
	if err := assertTextContentType(contentType); err != nil {
		return fetchResponse{}, err
	}
	text, title, hasTitle := extractBodyAsText(res, contentType, raw)
	out := fetchResponse{Text: text, Title: title, HasTitle: hasTitle}
	if contentType != "" {
		out.ContentType, out.HasContentType = contentType, true
	}
	if res.HasContentLength && res.ContentLength != "" {
		if n, err := strconv.ParseFloat(res.ContentLength, 64); err == nil {
			out.ContentLength = &n
		}
	}
	return out, nil
}
