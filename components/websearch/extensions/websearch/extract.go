package websearch

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/htmlindex"
)

// Port of extract.ts: fetch one URL and return readable text. What is ported here is the direct
// HTTP path (SSRF-checked, redirect-limited, size-capped, raw and readable modes, images,
// Cloudflare challenge detection), the keyless Jina Reader fallback (opt-in), the fetch routing
// configuration, and the failure guidance. The hosted extraction providers, PDF text, video,
// YouTube and GitHub-clone extraction are named gaps (docs/PORT.md): their routing entries exist
// so configuration validates, but they are never called.

const (
	minUsefulContent  = 500
	concurrentLimit   = 3
	userAgentFetch    = "OpenAI File Downloader, XaiImageApiFetch/1.0"
	jinaReaderBase    = "https://r.jina.ai/"
	pageMaxBytes      = 5 * 1024 * 1024
	abortedByDeadline = "The operation was aborted."
)

var nonRecoverableErrors = []string{"Unsupported content type", "Response too large", "PDF extraction is disabled", "Image fetching is disabled"}

var supportedImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

// RegisteredToolNames are the tool names failure guidance may point at, so guidance never names a
// tool the session does not have.
type RegisteredToolNames struct{ WebSearch, FetchContent string }

// ExtractOptions tune one extraction.
type ExtractOptions struct {
	// TimeoutMs overrides fetch.timeout for the direct HTTP/Jina budget.
	TimeoutMs *int64
	// Mode is "readable" (default), "raw" or "answer".
	Mode       string
	Frames     int
	Timestamp  string
	ForceClone bool
	Prompt     string
	Model      string
	ToolNames  RegisteredToolNames
	// Proxy routes the direct fetch through an HTTP(S) or SOCKS proxy.
	Proxy string
	// Lookup replaces the DNS resolver used for SSRF validation (test seam).
	Lookup Lookup
}

var (
	pageFetchMu sync.Mutex
	pageFetch   FetchFunc

	// observeTimeout is told each direct-fetch budget ("http" or "jina"); a test seam.
	observeTimeout func(kind string, ms int64)
	// extractHook is called between the phases of HTML/image processing ("before-parse",
	// "processing"); a test seam for late-success deadline handling.
	extractHook = func(stage string) {}
)

// SetPageFetch replaces the page transport (a test seam; nil restores the SSRF-guarded default)
// and returns a restore function.
func SetPageFetch(f FetchFunc) func() {
	pageFetchMu.Lock()
	prev := pageFetch
	pageFetch = f
	pageFetchMu.Unlock()
	return func() {
		pageFetchMu.Lock()
		pageFetch = prev
		pageFetchMu.Unlock()
	}
}

func currentPageFetch() FetchFunc {
	pageFetchMu.Lock()
	defer pageFetchMu.Unlock()
	return pageFetch
}

func errp(s string) *string { return &s }

func abortedResult(u string) ExtractedContent {
	return ExtractedContent{URL: u, Error: errp("Aborted")}
}

func failed(u, msg string) ExtractedContent { return ExtractedContent{URL: u, Error: errp(msg)} }

func mustBase(s string) *url.URL {
	u, err := ParseURL(s, nil)
	if err != nil {
		return nil
	}
	return u
}

func isConfigParseError(msg string) bool { return strings.HasPrefix(msg, "Failed to parse ") }

func isRedirectPolicyError(m string) bool {
	return strings.HasPrefix(m, "Authenticated fetch refused cross-origin redirect") ||
		strings.HasPrefix(m, "Blocked internal ") ||
		strings.HasPrefix(m, "Blocked hostname by fetch_content domain policy") ||
		strings.HasPrefix(m, "Hostname not allowed by fetch_content domain policy") ||
		strings.HasPrefix(m, "Too many redirects fetching ") ||
		m == "Only HTTP and HTTPS URLs can be fetched remotely" ||
		m == "URL must include a hostname" ||
		strings.HasPrefix(m, "Failed to resolve ")
}

func imageGateError() string {
	enabled, err := IsImageEnabled()
	switch {
	case err != nil:
		return err.Error()
	case !enabled:
		return "Image fetching is disabled by image.enabled"
	}
	return ""
}

// httpExtracted is an HTTP result plus the links the page declares.
type httpExtracted struct {
	ExtractedContent
	declared []DeclaredWebLink
}

func notFoundGuidance(r ExtractedContent, names RegisteredToolNames) string {
	status := 0
	if r.Status != nil {
		status = *r.Status
	}
	first := "HTTP " + strconv.Itoa(status)
	if r.Error != nil {
		first = *r.Error
	}
	lines := []string{first, "", fmt.Sprintf("The origin server says this page does not exist (HTTP %d), so extraction providers cannot retrieve it.", status)}
	switch {
	case names.WebSearch != "" && names.FetchContent != "":
		lines = append(lines, fmt.Sprintf("The page may have moved or been renamed. Use %s to find the current URL, then retry %s with it.", names.WebSearch, names.FetchContent))
	case names.WebSearch != "":
		lines = append(lines, fmt.Sprintf("The page may have moved or been renamed. Use %s to find the current URL.", names.WebSearch))
	default:
		lines = append(lines, "The page may have moved or been renamed. Find the current URL, then retry the fetch with it.")
	}
	return strings.Join(lines, "\n")
}

func jinaReaderGuidance(routing fetchRouting, order []string, needsRemoteOptIn bool) string {
	if sliceHas(order, "jina") {
		return ""
	}
	const privacy = "target URLs are fetched through Jina's infrastructure"
	const optIn = "this also allows the other hosted providers in your fetch provider order"
	path := ConfigPath()
	switch {
	case sliceHas(routing.Providers, "jina"):
		return fmt.Sprintf("  • Enable the keyless Jina Reader fallback: set fetchRouting.allowRemoteHostedProviders to true in %s (Jina is already in your fetch provider order; %s; %s)", path, optIn, privacy)
	case needsRemoteOptIn:
		return fmt.Sprintf(`  • Enable the keyless Jina Reader fallback: add "jina" to your existing fetchRouting.providers and set fetchRouting.allowRemoteHostedProviders to true in %s (%s; %s)`, path, optIn, privacy)
	}
	return fmt.Sprintf(`  • Enable the keyless Jina Reader fallback: add "jina" to your existing fetchRouting.providers in %s (%s)`, path, privacy)
}

// ExtractContent fetches one URL. Failures are reported in the result's Error, never as a Go
// error, and a cancelled ctx gives Error "Aborted".
func ExtractContent(ctx context.Context, rawURL string, o ExtractOptions) ExtractedContent {
	if ctx.Err() != nil {
		return abortedResult(rawURL)
	}
	if o.Proxy != "" {
		ctx = WithProxy(ctx, o.Proxy)
	}
	var remote *url.URL
	if u, err := ParseURL(rawURL, nil); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		remote = u
	}
	if remote != nil {
		ssrf, err := LoadSsrfConfig()
		if err != nil {
			return failed(rawURL, err.Error())
		}
		policy, err := LoadFetchContentDomainPolicy()
		if err != nil {
			return failed(rawURL, err.Error())
		}
		if _, err := ValidateRemoteURL(ctx, rawURL, ValidationOptions{Lookup: o.Lookup, DomainPolicy: &policy, AllowRanges: ssrf.AllowRanges, TrustEnvProxy: ssrf.TrustEnvProxy}); err != nil {
			return failed(rawURL, err.Error())
		}
	}
	if o.Mode == "raw" {
		timeout, err := ResolveFetchTimeoutMs(o.TimeoutMs)
		if err != nil {
			return failed(rawURL, err.Error())
		}
		return extractViaHTTP(ctx, rawURL, timeout, o).ExtractedContent
	}
	if o.Frames > 0 || o.Timestamp != "" {
		if msg := imageGateError(); msg != "" {
			return failed(rawURL, msg)
		}
		return failed(rawURL, "Video frame and timestamp extraction is not available in this Go port yet (planned for a later slice)")
	}
	if remote == nil {
		if _, err := ParseURL(rawURL, nil); err != nil {
			return failed(rawURL, err.Error())
		}
	}
	if ctx.Err() != nil {
		return abortedResult(rawURL)
	}
	timeout, err := ResolveFetchTimeoutMs(o.TimeoutMs)
	if err != nil {
		return failed(rawURL, err.Error())
	}
	routing, err := loadFetchRouting()
	if err != nil {
		return failed(rawURL, err.Error())
	}
	order := routing.Providers
	if remote != nil && !routing.AllowRemoteHostedProviders {
		order = nil
		for _, p := range routing.Providers {
			if !sliceHas(remoteHostedFetchNames, p) {
				order = append(order, p)
			}
		}
	}
	if len(order) == 0 {
		return failed(rawURL, "Remote hosted fetch providers are disabled unless fetchRouting.allowRemoteHostedProviders is true")
	}

	var httpResult *ExtractedContent
	var declared []DeclaredWebLink
	withDeclared := func(r ExtractedContent) ExtractedContent {
		r.Content = AppendDeclaredWebLinks(r.Content, declared)
		return r
	}
	runHTTP := func() *ExtractedContent {
		res := extractViaHTTP(ctx, rawURL, timeout, o)
		r := res.ExtractedContent
		httpResult = &r
		declared = res.declared
		if ctx.Err() != nil {
			a := abortedResult(rawURL)
			return &a
		}
		if r.Error == nil {
			return &r
		}
		for _, p := range nonRecoverableErrors {
			if strings.HasPrefix(*r.Error, p) {
				return &r
			}
		}
		if isRedirectPolicyError(*r.Error) || isConfigParseError(*r.Error) {
			return &r
		}
		return nil
	}

	if remote != nil && order[0] != "http" {
		if r := runHTTP(); r != nil {
			return *r
		}
	}
	for _, p := range order {
		if ctx.Err() != nil {
			return abortedResult(rawURL)
		}
		switch p {
		case "http":
			if r := runHTTP(); r != nil {
				return *r
			}
		case "jina":
			if r := extractWithJinaReader(ctx, rawURL, timeout, o.Lookup); r != nil {
				return withDeclared(*r)
			}
		}
		// Every other hosted provider is not ported yet (docs/PORT.md): it is never called.
	}

	if ctx.Err() != nil {
		return abortedResult(rawURL)
	}
	if httpResult != nil && len(declared) > 0 {
		r := *httpResult
		r.Error = nil
		return r
	}
	if httpResult != nil && httpResult.Status != nil && (*httpResult.Status == 404 || *httpResult.Status == 410) {
		r := *httpResult
		r.Error = errp(notFoundGuidance(r, o.ToolNames))
		return r
	}
	first := "No fetch_content provider returned content"
	if httpResult != nil && httpResult.Error != nil {
		first = *httpResult.Error
	}
	lines := []string{first, "", "Fallback options:"}
	if hint := jinaReaderGuidance(routing, order, remote != nil && !routing.AllowRemoteHostedProviders); hint != "" {
		lines = append(lines, hint)
	}
	// The original also lists Firecrawl, Crawl4AI, TinyFish, Search1API, Querit, Kagi, Ollama,
	// Parallel, Bright Data, the Gemini API and Gemini Web (browser cookies). None of them is a
	// fetch provider in this port, so suggesting them would send the user to settings that do
	// nothing (docs/PORT.md, deliberate differences).
	if o.ToolNames.WebSearch != "" {
		lines = append(lines, "  • Use "+o.ToolNames.WebSearch+" to find content about this topic")
	}
	if len(lines) == 3 {
		lines = append(lines, "  • none in this port: the other fetch providers of pi-web-access are not ported yet")
	}
	base := ExtractedContent{URL: rawURL}
	if httpResult != nil {
		base = *httpResult
	}
	base.Error = errp(strings.Join(lines, "\n"))
	return base
}

// extractWithJinaReader is the keyless hosted fallback. It runs only after the URL passed the
// same SSRF and domain-policy validation as the direct fetch, and only when the user opted in to
// remote hosted providers. Any failure means "no result" so routing continues.
func extractWithJinaReader(ctx context.Context, rawURL string, timeoutMs int64, lookup Lookup) *ExtractedContent {
	ssrf, err := LoadSsrfConfig()
	if err != nil {
		return nil
	}
	policy, err := LoadFetchContentDomainPolicy()
	if err != nil {
		return nil
	}
	if _, err := ValidateRemoteURL(ctx, rawURL, ValidationOptions{Lookup: lookup, DomainPolicy: &policy, AllowRanges: ssrf.AllowRanges, TrustEnvProxy: ssrf.TrustEnvProxy}); err != nil {
		return nil
	}
	if observeTimeout != nil {
		observeTimeout("jina", timeoutMs)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, jinaReaderBase+rawURL, RequestInit{Method: "GET",
		Header: http.Header{"Accept": {"text/markdown"}, "X-No-Cache": {"true"}}}, nil)
	if err != nil {
		return nil
	}
	body, err := readBody(resp)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil
	}
	const marker = "Markdown Content:"
	start := strings.Index(body, marker)
	if start < 0 {
		return nil
	}
	md := jsTrim(body[start+len(marker):])
	if jsLen(md) < 100 || strings.HasPrefix(md, "Loading...") || strings.HasPrefix(md, "Please enable JavaScript") {
		return nil
	}
	return &ExtractedContent{URL: rawURL, Title: textTitle(md, rawURL), Content: md}
}

// ExtractHeadingTitle is the first "# " or "## " heading of Markdown text.
func ExtractHeadingTitle(text string) (string, bool) {
	m := headingRE.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	cleaned := jsTrim(strings.ReplaceAll(m[1], "*", ""))
	return cleaned, cleaned != ""
}

var headingRE = regexp.MustCompile(`(?m)^#{1,2}\s+(.+)`)

func lastPathSegment(u string) string {
	parsed, err := ParseURL(u, nil)
	if err != nil {
		return u
	}
	p := parsed.EscapedPath()
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return u
	}
	return p
}

func textTitle(text, u string) string {
	if t, ok := ExtractHeadingTitle(text); ok {
		return t
	}
	return lastPathSegment(u)
}

func responseURL(resp *http.Response, fallback string) string {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return fallback
}

func roundMB(n int64) int64 { return int64(math.Floor(float64(n)/1024/1024 + 0.5)) }

func sizeLimitError(maxBytes int64) error {
	return fmt.Errorf("Response too large (%dMB)", roundMB(maxBytes))
}

// readLimited reads at most max bytes; more is an error, so a chunked oversized body stops early.
func readLimited(r io.Reader, max int64, tooBig func() error) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, tooBig()
	}
	return data, nil
}

func decodeText(data []byte, contentType string) string {
	charset := ""
	if m := charsetRE.FindStringSubmatch(contentType); m != nil {
		charset = m[1]
	}
	if charset != "" {
		if enc, err := htmlindex.Get(charset); err == nil {
			if out, err := enc.NewDecoder().Bytes(data); err == nil {
				return string(out)
			}
		}
	}
	return string([]rune(string(data)))
}

var charsetRE = regexp.MustCompile(`(?i)charset\s*=\s*["']?([^;"'\s]+)`)

func isTextContentType(contentType string) bool {
	mime := strings.ToLower(jsTrim(strings.SplitN(contentType, ";", 2)[0]))
	return strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/ld+json" ||
		mime == "application/xml" || mime == "application/xhtml+xml" || mime == "application/javascript" ||
		mime == "application/x-javascript" || strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

func isPDF(u, contentType string) bool {
	if strings.Contains(contentType, "application/pdf") {
		return true
	}
	parsed, err := ParseURL(u, nil)
	return err == nil && strings.HasSuffix(strings.ToLower(parsed.Path), ".pdf")
}

var (
	cfTitleRE = regexp.MustCompile(`(?i)<title>\s*Just a moment\.\.\.\s*</title>`)
	bodyRE    = regexp.MustCompile(`(?is)<body[^>]*>(.*?)</body>`)
	scriptRE  = regexp.MustCompile(`(?is)<script.*?</script>`)
	styleRE   = regexp.MustCompile(`(?is)<style.*?</style>`)
	tagRE     = regexp.MustCompile(`<[^>]+>`)
	scriptTag = regexp.MustCompile(`(?i)<script`)
)

// isCloudflareChallenge: the cf-mitigated header is authoritative for any 200 text response; the
// body check is HTML-only and needs both challenge-platform markers so a generic "Just a
// moment..." page never matches.
func isCloudflareChallenge(resp *http.Response, text string, isHTML bool) bool {
	if resp.StatusCode != 200 {
		return false
	}
	if resp.Header.Get("cf-mitigated") == "challenge" {
		return true
	}
	return isHTML && cfTitleRE.MatchString(text) && strings.Contains(text, "window._cf_chl_opt") && strings.Contains(text, "/cdn-cgi/challenge-platform/")
}

func isLikelyJSRendered(src string) bool {
	m := bodyRE.FindStringSubmatch(src)
	if m == nil {
		return false
	}
	text := scriptRE.ReplaceAllString(m[1], "")
	text = styleRE.ReplaceAllString(text, "")
	text = jsTrim(wsRE.ReplaceAllString(tagRE.ReplaceAllString(text, ""), " "))
	return jsLen(text) < 500 && len(scriptTag.FindAllString(src, -1)) > 3
}

// extractViaHTTP is the direct fetch. A late success (the work finished after the deadline, or
// the caller cancelled meanwhile) is never returned.
func extractViaHTTP(ctx context.Context, rawURL string, timeoutMs int64, o ExtractOptions) httpExtracted {
	if observeTimeout != nil {
		observeTimeout("http", timeoutMs)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	started := nowMs()
	res := doHTTP(tctx, rawURL, o)
	if ctx.Err() != nil {
		return httpExtracted{ExtractedContent: abortedResult(rawURL)}
	}
	if tctx.Err() != nil || nowMs()-started >= timeoutMs {
		return httpExtracted{ExtractedContent: failed(rawURL, abortedByDeadline)}
	}
	return res
}

func doHTTP(ctx context.Context, rawURL string, o ExtractOptions) httpExtracted {
	fail := func(msg string) httpExtracted { return httpExtracted{ExtractedContent: failed(rawURL, msg)} }
	ssrf, err := LoadSsrfConfig()
	if err != nil {
		return fail(err.Error())
	}
	policy, err := LoadFetchContentDomainPolicy()
	if err != nil {
		return fail(err.Error())
	}
	trustEnvProxy := o.Proxy == "" && ssrf.TrustEnvProxy
	resp, err := FetchRemoteURL(ctx, rawURL, RequestInit{Method: "GET", Header: http.Header{
		"User-Agent":                {userAgentFetch},
		"Accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
		"Accept-Language":           {"en-US,en;q=0.9"},
		"Cache-Control":             {"no-cache"},
		"Sec-Fetch-Dest":            {"document"},
		"Sec-Fetch-Mode":            {"navigate"},
		"Sec-Fetch-Site":            {"none"},
		"Sec-Fetch-User":            {"?1"},
		"Upgrade-Insecure-Requests": {"1"},
	}}, FetchRemoteOptions{
		ValidationOptions: ValidationOptions{Lookup: o.Lookup, DomainPolicy: &policy, AllowRanges: ssrf.AllowRanges, TrustEnvProxy: trustEnvProxy},
		Fetch:             currentPageFetch(),
	})
	if err != nil {
		return fail(err.Error())
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	statusPtr := &status
	statusText := http.StatusText(status)

	if (status < 200 || status > 299) && o.Mode != "raw" {
		return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp(fmt.Sprintf("HTTP %d: %s", status, statusText)), Status: statusPtr}}
	}
	contentType := resp.Header.Get("Content-Type")
	mime := strings.ToLower(jsTrim(strings.SplitN(contentType, ";", 2)[0]))
	isPDFContent := isPDF(rawURL, contentType)
	var pdf *pdfConfig
	if isPDFContent {
		cfg, err := loadPDFConfig()
		if err != nil {
			return fail(err.Error())
		}
		pdf = &cfg
		if !cfg.Enabled {
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp("PDF extraction is disabled by pdf.enabled"), MimeType: mime, Status: statusPtr}}
		}
	}
	maxBytes := int64(pageMaxBytes)
	if pdf != nil {
		maxBytes = int64(pdf.MaxSizeMB * 1024 * 1024)
	}
	sizeErr := func() error { return sizeLimitError(maxBytes) }
	if pdf != nil {
		sizeErr = func() error {
			return fmt.Errorf("PDF exceeds configured pdf.maxSizeMB limit (%s MB)", formatNumber(pdf.MaxSizeMB))
		}
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(leadingDigits(cl), 10, 64); err == nil && n > maxBytes {
			if pdf != nil {
				return fail(sizeErr().Error())
			}
			return fail(fmt.Sprintf("Response too large (%dMB)", roundMB(n)))
		}
	}

	if o.Mode == "raw" {
		if !isTextContentType(contentType) {
			m := mime
			if m == "" {
				m = "missing"
			}
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp("Unsupported content type in raw mode: " + m), MimeType: mime, Status: statusPtr}}
		}
		data, err := readLimited(resp.Body, maxBytes, sizeErr)
		if err != nil {
			return fail(err.Error())
		}
		text := decodeText(data, contentType)
		return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Title: textTitle(text, rawURL), Content: text, MimeType: mime, Status: statusPtr}}
	}

	if supportedImageTypes[mime] {
		if msg := imageGateError(); msg != "" {
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp(msg), MimeType: mime, Status: statusPtr}}
		}
		data, err := readLimited(resp.Body, maxBytes, sizeErr)
		if err != nil {
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp(err.Error()), MimeType: mime, Status: statusPtr}}
		}
		img, err := resizeImage(data, mime, 2000, 2000)
		extractHook("processing")
		if err != nil {
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp(err.Error()), MimeType: mime, Status: statusPtr}}
		}
		if img == nil {
			return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp("Could not decode image: " + mime), MimeType: mime, Status: statusPtr}}
		}
		title := lastPathSegment(responseURL(resp, rawURL))
		if p, err := ParseURL(responseURL(resp, rawURL), nil); err == nil {
			title = p.Path[strings.LastIndex(p.Path, "/")+1:]
			if title == "" {
				title = rawURL
			}
		}
		return httpExtracted{ExtractedContent: ExtractedContent{
			URL: rawURL, Title: title,
			Content:   fmt.Sprintf("Image fetched (%d×%d, %s)", img.width, img.height, img.mime),
			Thumbnail: &Image{Data: img.data, MimeType: img.mime}, MimeType: img.mime, Status: statusPtr,
		}}
	}

	if pdf != nil {
		if _, err := readLimited(resp.Body, maxBytes, sizeErr); err != nil {
			return fail(err.Error())
		}
		return fail("PDF extraction failed: PDF text extraction is not available in this Go port yet (planned for a later slice)")
	}

	if strings.Contains(contentType, "application/octet-stream") || strings.Contains(contentType, "image/") ||
		strings.Contains(contentType, "audio/") || strings.Contains(contentType, "video/") || strings.Contains(contentType, "application/zip") {
		return fail("Unsupported content type: " + strings.SplitN(contentType, ";", 2)[0])
	}

	data, err := readLimited(resp.Body, maxBytes, sizeErr)
	if err != nil {
		return fail(err.Error())
	}
	text := decodeText(data, contentType)
	isHTML := strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")

	if isCloudflareChallenge(resp, text, isHTML) {
		return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Error: errp(fmt.Sprintf("HTTP %d: Blocked by Cloudflare challenge page", status)), Status: statusPtr}}
	}
	if !isHTML {
		return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Title: textTitle(text, rawURL), Content: text}}
	}

	extractHook("before-parse")
	if ctx.Err() != nil {
		return fail(abortedByDeadline)
	}
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return fail(err.Error())
	}
	docTitle := documentTitle(doc)
	var linkHeader *string
	if v := resp.Header.Get("Link"); v != "" {
		linkHeader = &v
	}
	declared := DiscoverDeclaredWebLinks(doc, linkHeader, responseURL(resp, rawURL))
	articleTitleText, markdown, ok := readableArticle(doc)
	extractHook("processing")

	incomplete := func(title, content, msg string) httpExtracted {
		return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Title: title, Content: AppendDeclaredWebLinks(content, declared), Error: errp(msg)}, declared: declared}
	}
	jsMsg := func(fallback string) string {
		if isLikelyJSRendered(text) {
			return "Page appears to be JavaScript-rendered (content loads dynamically)"
		}
		return fallback
	}
	// RSC payloads and Defuddle are not ported (docs/PORT.md): both fallbacks yield nothing.
	if !ok {
		return incomplete(docTitle, "", jsMsg("Could not extract readable content from HTML structure"))
	}
	title := articleTitleText
	if title == "" {
		title = docTitle
	}
	if jsLen(markdown) < minUsefulContent {
		return incomplete(title, markdown, jsMsg("Extracted content appears incomplete"))
	}
	return httpExtracted{ExtractedContent: ExtractedContent{URL: rawURL, Title: title, Content: AppendDeclaredWebLinks(markdown, declared)}, declared: declared}
}

func leadingDigits(s string) string {
	s = jsTrim(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end]
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// FetchAllContent extracts several URLs with at most three in flight; results keep input order.
func FetchAllContent(ctx context.Context, urls []string, o ExtractOptions) []ExtractedContent {
	results := make([]ExtractedContent, len(urls))
	sem := make(chan struct{}, concurrentLimit)
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer recoverInto("Fetch", func(err error) { results[i] = failed(u, err.Error()) })
			results[i] = ExtractContent(ctx, u, o)
		}(i, u)
	}
	wg.Wait()
	if o.Mode == "raw" {
		return results
	}
	// Inline data: URIs in extracted markdown would otherwise flow into tool results and the fetch
	// cache as opaque base64; typed thumbnail/frame image blocks are deliberate outputs.
	for i, r := range results {
		if r.Content == "" {
			continue
		}
		if text, omissions := SanitizeInlineDataURIs(r.Content, "urls["+strconv.Itoa(i)+"].content"); len(omissions) > 0 {
			results[i].Content = text
		}
	}
	return results
}
