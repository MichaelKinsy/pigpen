package websearch

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Port of gemini-web.ts: Gemini through the signed-in browser session. The cookies come from
// chrome_cookies.go (opt-in, see gemini_web_config.go); the requests go to Google's web frontend
// with the cookies of one origin, never across origins.

const (
	geminiAppURL            = "https://gemini.google.com/app"
	geminiStreamGenerateURL = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	geminiUploadURL         = "https://content-push.googleapis.com/upload"
	geminiUploadPushID      = "feeds/mcudyrk2a4khkz"
	googleListAccountsURL   = "https://accounts.google.com/ListAccounts?gpsia=1&source=ChromiumBrowser&laf=b64bin&json=standard"
	geminiWebUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	geminiModelHeaderName   = "x-goog-ext-525001261-jspb"

	// GeminiMaxHeaderSize is GEMINI_MAX_HEADER_SIZE: Google's /app response headers exceed the
	// 16 KiB budget a host's default HTTP/1.1 client gives, so Gemini Web uses its own transport
	// with 4 MiB.
	GeminiMaxHeaderSize = 4 * 1024 * 1024
	// DefaultGeminiWebModel is DEFAULT_GEMINI_WEB_MODEL.
	DefaultGeminiWebModel = "gemini-3.1-pro"

	geminiWebDefaultTimeout = 120 * time.Second
)

var geminiWebModelHeaders = map[string]string{
	DefaultGeminiWebModel: `[1,null,null,null,"9d8ca3786ebdfbea",null,null,0,[4]]`,
	"gemini-2.5-pro":      `[1,null,null,null,"4af6c7f5da75d65d",null,null,0,[4]]`,
	"gemini-2.5-flash":    `[1,null,null,null,"9ec249fc9ad08861",null,null,0,[4]]`,
}

var geminiWebRequiredCookies = []string{"__Secure-1PSID", "__Secure-1PSIDTS"}

// GeminiWebRequest is one request of Gemini Web as the transport sees it. Redirect is "manual"
// (the 3xx response is returned) or "error" (a 3xx response fails the request), as fetch's
// redirect option.
type GeminiWebRequest struct {
	Method   string
	URL      string
	Header   http.Header
	Body     []byte
	Redirect string
}

// GeminiWebFetch performs one Gemini Web request.
type GeminiWebFetch func(ctx context.Context, r GeminiWebRequest) (*http.Response, error)

// GeminiWebOptions are the options of queryWithCookies.
type GeminiWebOptions struct {
	YoutubeURL string
	Model      string
	Files      []string
	// Timeout is the whole query's budget; zero means two minutes.
	Timeout time.Duration
}

var (
	geminiWebFetchMu       sync.Mutex
	geminiWebFetchOverride GeminiWebFetch

	geminiWebClientMu   sync.Mutex
	geminiWebClientBuilt *http.Client
)

// SetGeminiWebFetch replaces the Gemini Web transport (setGeminiFetchOverrideForTests) and
// returns a restore function.
func SetGeminiWebFetch(f GeminiWebFetch) func() {
	geminiWebFetchMu.Lock()
	prev := geminiWebFetchOverride
	geminiWebFetchOverride = f
	geminiWebFetchMu.Unlock()
	return func() {
		geminiWebFetchMu.Lock()
		geminiWebFetchOverride = prev
		geminiWebFetchMu.Unlock()
	}
}

func geminiWebFetch(ctx context.Context, r GeminiWebRequest) (*http.Response, error) {
	geminiWebFetchMu.Lock()
	f := geminiWebFetchOverride
	geminiWebFetchMu.Unlock()
	if f != nil {
		return f(ctx, r)
	}
	client, err := resolveGeminiWebClient()
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Host") {
			req.Host = vs[0]
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, err
	}
	if r.Redirect == "error" && redirectStatuses[resp.StatusCode] {
		_ = resp.Body.Close()
		return nil, errors.New("fetch failed: unexpected redirect")
	}
	return resp, nil
}

// newGeminiWebTransport is createGeminiFetch's dedicated agent (an EnvHttpProxyAgent upstream):
// HTTP/1.1 only, the proxy environment honoured, a 30 s connect timeout and the 4 MiB response
// header budget. A proxy variable that is not a URL fails here, as the agent's construction
// fails upstream.
func newGeminiWebTransport() (*http.Transport, error) {
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		if v := os.Getenv(name); v != "" {
			if _, err := ParseURL(v, nil); err != nil {
				return nil, err
			}
		}
	}
	return &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		MaxResponseHeaderBytes: GeminiMaxHeaderSize,
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
		ForceAttemptHTTP2:      false,
		DisableKeepAlives:      false,
	}, nil
}

// resolveGeminiWebClient is resolveGeminiFetch: the dedicated client, built once.
func resolveGeminiWebClient() (*http.Client, error) {
	geminiWebClientMu.Lock()
	defer geminiWebClientMu.Unlock()
	if geminiWebClientBuilt != nil {
		return geminiWebClientBuilt, nil
	}
	tr, err := newGeminiWebTransport()
	if err != nil {
		return nil, err
	}
	geminiWebClientBuilt = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return geminiWebClientBuilt, nil
}

// IsGeminiWebAvailable is isGeminiWebAvailable: the cookies of the signed-in profile, or nil.
func IsGeminiWebAvailable(ctx context.Context, chromeProfile string) (CookieMap, error) {
	allowed, err := IsBrowserCookieAccessAllowed()
	if err != nil || !allowed {
		return nil, err
	}
	selection, err := BrowserCookieSelectionFromConfig()
	if err != nil {
		return nil, err
	}
	profile := normalizeChromeProfile(chromeProfile)
	if profile == "" {
		profile = selection.Profile
	}
	res, err := GetGoogleCookies(ctx, GoogleCookieOptions{Browser: selection.Browser, Profile: profile, RequiredCookies: geminiWebRequiredCookies})
	if err != nil || res == nil {
		return nil, err
	}
	return res.Cookies, nil
}

// GeminiWebAvailabilityDiagnosticDetails is getGeminiWebAvailabilityDiagnosticDetails.
func GeminiWebAvailabilityDiagnosticDetails() *BrowserCookieDiagnosticDetails {
	if allowed, err := IsBrowserCookieAccessAllowed(); err != nil || !allowed {
		return nil
	}
	return LastGoogleCookieDiagnosticDetails()
}

// geminiWebAvailabilityDiagnostic is getGeminiWebAvailabilityDiagnostic.
func geminiWebAvailabilityDiagnostic() string {
	if allowed, err := IsBrowserCookieAccessAllowed(); err != nil || !allowed {
		return ""
	}
	return LastGoogleCookieDiagnostic()
}

// cookieGeminiWeb is the GeminiWebClient of this port: Gemini Web over the browser cookies.
type cookieGeminiWeb struct{}

func (cookieGeminiWeb) Available(ctx context.Context) (GeminiWebCookies, error) {
	cookies, err := IsGeminiWebAvailable(ctx, "")
	if cookies == nil {
		return nil, err
	}
	return GeminiWebCookies(cookies), err
}

func (cookieGeminiWeb) Diagnostic() string { return geminiWebAvailabilityDiagnostic() }

func (cookieGeminiWeb) Query(ctx context.Context, prompt string, cookies GeminiWebCookies, timeout time.Duration) (string, error) {
	return QueryGeminiWebWithCookies(ctx, prompt, CookieMap(cookies), GeminiWebOptions{Timeout: timeout})
}

var errGeminiTimeout = errors.New("The operation was aborted due to timeout")

// geminiWebTimeoutErr reports the original's timeout error when the budget, not the caller, ended
// the request.
func geminiWebTimeoutErr(parent, child context.Context, err error) error {
	if err != nil && parent.Err() == nil && child.Err() == context.DeadlineExceeded {
		return errGeminiTimeout
	}
	return err
}

// ActiveGoogleEmail is getActiveGoogleEmail: the signed-in account's address, "" when none is
// found. It tries the Gemini app page, then the account list, with 10 s each.
func ActiveGoogleEmail(ctx context.Context, cookies CookieMap) string {
	header := buildGeminiCookieHeader(cookies)
	if header == "" {
		return ""
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	html, err := fetchWithCookieRedirects(tctx, geminiAppURL, header, 10)
	cancel()
	if err == nil {
		if email := extractEmailFromGeminiHTML(html); email != "" {
			return email
		}
	}
	tctx, cancel = context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	text, err := fetchWithCookieRedirects(tctx, googleListAccountsURL, header, 10)
	if err != nil {
		return ""
	}
	return extractEmailFromListAccounts(text)
}

// QueryGeminiWebWithCookies is queryWithCookies.
func QueryGeminiWebWithCookies(ctx context.Context, prompt string, cookies CookieMap, o GeminiWebOptions) (string, error) {
	model := o.Model
	if model == "" {
		model = DefaultGeminiWebModel
	}
	if _, ok := geminiWebModelHeaders[model]; !ok {
		return "", fmt.Errorf("Gemini Web does not support model %s; configure Gemini API or choose a supported Gemini Web model.", model)
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = geminiWebDefaultTimeout
	}
	full := prompt
	if o.YoutubeURL != "" {
		full += "\n\nYouTube video: " + o.YoutubeURL
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	text, err := runGeminiWebOnce(tctx, full, cookies, model, o.Files)
	if err != nil {
		return "", geminiWebTimeoutErr(ctx, tctx, err)
	}
	if text == "" {
		return "", errors.New("Gemini Web returned empty response")
	}
	return text, nil
}

func runGeminiWebOnce(ctx context.Context, prompt string, cookies CookieMap, model string, files []string) (string, error) {
	cookieHeader := buildGeminiCookieHeader(cookies)
	accessToken, err := fetchGeminiAccessToken(ctx, cookieHeader)
	if err != nil {
		return "", err
	}
	var uploaded []geminiUpload
	for _, path := range files {
		u, err := uploadGeminiFile(ctx, path, cookieHeader)
		if err != nil {
			return "", err
		}
		uploaded = append(uploaded, u)
	}
	fReq, err := buildFReqPayload(prompt, uploaded)
	if err != nil {
		return "", err
	}
	body := formEncode([][2]string{{"at", accessToken}, {"f.req", fReq}})
	header := http.Header{}
	header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	header.Set("Host", "gemini.google.com")
	header.Set("Origin", "https://gemini.google.com")
	header.Set("Referer", "https://gemini.google.com/")
	header.Set("X-Same-Domain", "1")
	header.Set("User-Agent", geminiWebUserAgent)
	header.Set("Cookie", cookieHeader)
	header[geminiModelHeaderCanonical] = []string{geminiWebModelHeaders[model]}
	resp, err := geminiWebFetch(ctx, GeminiWebRequest{Method: "POST", URL: geminiStreamGenerateURL, Header: header, Body: []byte(body), Redirect: "error"})
	if err != nil {
		return "", err
	}
	raw, err := readGeminiWebBody(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("Gemini request failed: %d", resp.StatusCode)
	}
	return parseStreamGenerateResponse(raw)
}

var geminiModelHeaderCanonical = http.CanonicalHeaderKey(geminiModelHeaderName)

func readGeminiWebBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fetchGeminiAccessToken(ctx context.Context, cookieHeader string) (string, error) {
	html, err := fetchWithCookieRedirects(ctx, geminiAppURL, cookieHeader, 10)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"SNlM0e", "thykhd"} {
		re := regexp.MustCompile(`"` + key + `":"([^\n\r\x{2028}\x{2029}]*?)"`)
		if m := re.FindStringSubmatch(html); m != nil && m[1] != "" {
			return m[1], nil
		}
	}
	return "", errors.New("Unable to authenticate with Gemini. Make sure you're signed into gemini.google.com in a supported Chromium-based browser.")
}

// fetchWithCookieRedirects follows redirects by hand and refuses to send the cookies to
// another origin.
func fetchWithCookieRedirects(ctx context.Context, rawURL, cookieHeader string, maxRedirects int) (string, error) {
	current := rawURL
	start, err := ParseURL(rawURL, nil)
	if err != nil {
		return "", err
	}
	allowedOrigin := originOf(start)
	for i := 0; i <= maxRedirects; i++ {
		header := http.Header{}
		header.Set("User-Agent", geminiWebUserAgent)
		header.Set("Cookie", cookieHeader)
		resp, err := geminiWebFetch(ctx, GeminiWebRequest{Method: "GET", URL: current, Header: header, Redirect: "manual"})
		if err != nil {
			return "", err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			if location := resp.Header.Get("Location"); location != "" {
				_ = resp.Body.Close()
				base, _ := ParseURL(current, nil)
				next, err := ParseURL(location, base)
				if err != nil {
					return "", err
				}
				if originOf(next) != allowedOrigin {
					return "", fmt.Errorf("Refusing to send Google cookies across origins: %s -> %s", allowedOrigin, originOf(next))
				}
				current = next.String()
				continue
			}
		}
		return readGeminiWebBody(resp)
	}
	return "", fmt.Errorf("Too many redirects (>%d)", maxRedirects)
}

func buildGeminiCookieHeader(cookies CookieMap) string {
	names := make([]string, 0, len(cookies))
	for n, v := range cookies {
		if v != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + "=" + cookies[n]
	}
	return strings.Join(parts, "; ")
}

type geminiUpload struct{ id, name string }

func uploadGeminiFile(ctx context.Context, path, cookieHeader string) (geminiUpload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return geminiUpload{}, fmt.Errorf("ENOENT: no such file or directory, open '%s'", path)
		}
		return geminiUpload{}, err
	}
	name := filepath.Base(path)
	boundary := "----FormBoundary" + randomBase36(11)
	var body bytes.Buffer
	body.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + name + "\"\r\nContent-Type: application/octet-stream\r\n\r\n")
	body.Write(data)
	body.WriteString("\r\n--" + boundary + "--\r\n")
	header := http.Header{}
	header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	header.Set("Push-Id", geminiUploadPushID)
	header.Set("User-Agent", geminiWebUserAgent)
	header.Set("Cookie", cookieHeader)
	resp, err := geminiWebFetch(ctx, GeminiWebRequest{Method: "POST", URL: geminiUploadURL, Header: header, Body: body.Bytes(), Redirect: "error"})
	if err != nil {
		return geminiUpload{}, err
	}
	text, err := readGeminiWebBody(resp)
	if err != nil {
		return geminiUpload{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return geminiUpload{}, fmt.Errorf("File upload failed: %d (%s)", resp.StatusCode, jsSlice(text, 0, 200))
	}
	return geminiUpload{id: text, name: name}, nil
}

func randomBase36(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, big.NewInt(36))
		if err != nil {
			out[i] = '0'
			continue
		}
		out[i] = digits[v.Int64()]
	}
	return string(out)
}

// formEncode is URLSearchParams.toString: space becomes +, and only alphanumerics and *-._ stay.
func formEncode(pairs [][2]string) string {
	const hexUpper = "0123456789ABCDEF"
	enc := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			c := s[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
				b.WriteByte(c)
			case c == ' ':
				b.WriteByte('+')
			default:
				b.WriteByte('%')
				b.WriteByte(hexUpper[c>>4])
				b.WriteByte(hexUpper[c&15])
			}
		}
		return b.String()
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = enc(p[0]) + "=" + enc(p[1])
	}
	return strings.Join(parts, "&")
}

// jsStringify is JSON.stringify: <, > and & stay, and U+2028 and U+2029 are written raw.
func jsStringify(v any) (string, error) {
	b, err := marshalNoEscape(v)
	if err != nil {
		return "", err
	}
	s := string(b)
	if !strings.Contains(s, `\u202`) {
		return s, nil
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == 'u' && i+6 <= len(s) && (s[i+2:i+6] == "2028" || s[i+2:i+6] == "2029") {
				if s[i+5] == '8' {
					out.WriteString("\u2028")
				} else {
					out.WriteString("\u2029")
				}
				i += 5
				continue
			}
			out.WriteByte(s[i])
			out.WriteByte(s[i+1])
			i++
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String(), nil
}

func buildFReqPayload(prompt string, uploaded []geminiUpload) (string, error) {
	promptPayload := []any{prompt}
	if len(uploaded) > 0 {
		files := make([]any, len(uploaded))
		for i, f := range uploaded {
			files[i] = []any{[]any{f.id, 1}}
		}
		promptPayload = []any{prompt, 0, nil, files}
	}
	inner, err := jsStringify([]any{promptPayload, nil, nil})
	if err != nil {
		return "", err
	}
	return jsStringify([]any{nil, inner})
}

func nestedValue(v any, path ...int) any {
	cur := v
	for _, p := range path {
		arr, ok := cur.([]any)
		if !ok {
			return nil
		}
		if p >= len(arr) {
			return nil
		}
		cur = arr[p]
	}
	return cur
}

func trimJSONEnvelope(text string) (string, error) {
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end <= start {
		return "", errors.New("Gemini response did not contain a JSON payload.")
	}
	return text[start : end+1], nil
}

var cardContentRE = regexp.MustCompile(`^http://googleusercontent\.com/card_content/\d+`)

func extractCandidateText(candidate any) string {
	text, _ := nestedValue(candidate, 1, 0).(string)
	if cardContentRE.MatchString(text) {
		if alt, _ := nestedValue(candidate, 22, 0).(string); alt != "" {
			text = alt
		}
	}
	return text
}

// parseStreamGenerateResponse is parseStreamGenerateResponse: the latest non-empty answer of the
// stream, else the first candidate's text.
func parseStreamGenerateResponse(raw string) (string, error) {
	envelope, err := trimJSONEnvelope(raw)
	if err != nil {
		return "", err
	}
	var response any
	if err := json.Unmarshal([]byte(envelope), &response); err != nil {
		return "", err
	}
	parts, _ := response.([]any)
	var firstCandidate any
	seen := false
	latest := ""
	for _, part := range parts {
		body, _ := nestedValue(part, 2).(string)
		if body == "" {
			continue
		}
		var parsed any
		if json.Unmarshal([]byte(body), &parsed) != nil {
			continue
		}
		candidates, _ := nestedValue(parsed, 4).([]any)
		if len(candidates) == 0 {
			continue
		}
		if !seen {
			firstCandidate, seen = candidates[0], true
		}
		if text := extractCandidateText(candidates[0]); text != "" {
			latest = text
		}
	}
	if latest != "" {
		return latest, nil
	}
	return extractCandidateText(firstCandidate), nil
}

// --- account email -------------------------------------------------------------------------

var (
	emailInHTMLPatterns = []*regexp.Regexp{
		regexp.MustCompile(`"oPEP7c"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`aria-label="Google Account:[^"]*?\(([^)]+)\)"`),
		regexp.MustCompile(`"displayEmail"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"defaultEmail"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"email"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"identifier"\s*:\s*"([^"]+)"`),
		regexp.MustCompile(`"gaiaIdentifier"\s*:\s*"([^"]+)"`),
	}
	emailRE     = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	emailExactRE = regexp.MustCompile(`(?i)^[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}$`)
	emailEscapes = regexp.MustCompile(`(?i)\\u0040|\\x40|&#64;|&commat;`)
	listAccountsPrefix = regexp.MustCompile(`^\)\]\}'\s*`)
)

func extractEmailFromGeminiHTML(html string) string {
	for _, re := range emailInHTMLPatterns {
		if m := re.FindStringSubmatch(html); m != nil {
			if email := normalizeEmail(m[1]); email != "" {
				return email
			}
		}
	}
	return findFirstEmail(html)
}

func extractEmailFromListAccounts(text string) string {
	trimmed := listAccountsPrefix.ReplaceAllString(text, "")
	if v, err := decodeOrdered(trimmed); err == nil {
		if email := findEmailInValue(v); email != "" {
			return email
		}
	}
	return findFirstEmail(trimmed)
}

func findFirstEmail(text string) string {
	return emailRE.FindString(decodeEmailEscapes(text))
}

func normalizeEmail(value string) string {
	if value == "" {
		return ""
	}
	n := decodeEmailEscapes(jsTrim(value))
	if emailExactRE.MatchString(n) {
		return n
	}
	return ""
}

func decodeEmailEscapes(v string) string {
	v = emailEscapes.ReplaceAllString(v, "@")
	v = strings.ReplaceAll(v, `\"`, `"`)
	return strings.ReplaceAll(v, `\\`, `\`)
}

// orderedObject is a decoded JSON object that keeps its key order (Object.values upstream).
type orderedObject []any

func decodeOrdered(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	v, err := decodeOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func decodeOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if d == '[' {
		var arr []any
		for dec.More() {
			v, err := decodeOrderedValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	var values orderedObject
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		v, err := decodeOrderedValue(dec)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	_, err = dec.Token()
	return values, err
}

func findEmailInValue(v any) string {
	switch x := v.(type) {
	case string:
		return normalizeEmail(x)
	case []any:
		for _, item := range x {
			if e := findEmailInValue(item); e != "" {
				return e
			}
		}
	case orderedObject:
		for _, item := range x {
			if e := findEmailInValue(item); e != "" {
				return e
			}
		}
	}
	return ""
}
