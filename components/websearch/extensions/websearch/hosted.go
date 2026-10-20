package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Helpers shared by the hosted search and fetch providers (tinyfish.ts, search1api.ts,
// searchinfinity.ts, querit.ts, parallel.ts, parallel-mcp.ts, ollama.ts, kagi.ts, firecrawl.ts,
// crawl4ai.ts, brightdata.ts, brightdata-unlocker.ts). Each of those files repeats the same
// request, redaction and error wording; the Go port keeps the wording per provider and shares the
// plumbing here.

// hostedCall is one provider API request.
type hostedCall struct {
	URL     string
	Method  string
	Body    []byte
	Header  http.Header
	Key     string
	Timeout time.Duration
	// Credentials names the headers the redirect handling strips when the origin changes.
	Credentials []string
}

// hostedSend sends the request and reads the whole body. Transport errors have the credential
// redacted, as every provider's catch block does.
func hostedSend(parent context.Context, c hostedCall) (int, string, error) {
	tctx, cancel := withTimeout(parent, c.Timeout)
	defer cancel()
	method := c.Method
	if method == "" {
		method = "POST"
	}
	resp, err := FetchWithCredentialRedirects(tctx, c.URL, RequestInit{Method: method, Body: c.Body, Header: c.Header}, c.Credentials)
	if err != nil {
		return 0, "", redactErr(timeoutFix(parent, tctx, err), c.Key)
	}
	text, err := readBody(resp)
	if err != nil {
		return 0, "", redactErr(timeoutFix(parent, tctx, err), c.Key)
	}
	return resp.StatusCode, text, nil
}

// hostedJSON is the request shape of tinyfish, search1api, querit and parallel: a non-2xx status
// is "<label> API error N: <redacted body, 300 chars>", an unparseable body "<label> API returned
// invalid JSON".
func hostedJSON(parent context.Context, label string, c hostedCall) (any, error) {
	status, text, err := hostedSend(parent, c)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%s API error %d: %s", label, status, slice300(RedactCredential(text, c.Key)))
	}
	var data any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, fmt.Errorf("%s API returned invalid JSON: %v", label, err)
	}
	return data, nil
}

func jsonBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// strField is `typeof m[k] === "string"`.
func strField(m map[string]any, k string) (string, bool) {
	s, ok := m[k].(string)
	return s, ok
}

// timeoutMsOr is the `options.timeoutMs ?? default` of the fetch providers.
func timeoutMsOr(ms *int64, def time.Duration) time.Duration {
	if ms == nil {
		return def
	}
	return time.Duration(*ms) * time.Millisecond
}

func missingKey(label, cfgKey, env, getKeyAt string) error {
	return fmt.Errorf("%s key not found. Either:\n  1. Create %s with { \"%s\": \"your-key\" }\n  2. Set %s environment variable\n%s", label, ConfigPath(), cfgKey, env, getKeyAt)
}

// requireProviderKey resolves a credential or fails with the provider's "key not found" text.
func requireProviderKey(ctx context.Context, label, name, cfgKey, env, getKeyAt string) (string, error) {
	key, err := resolveProviderCredential(ctx, name, cfgKey, env)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", missingKey(label, cfgKey, env, getKeyAt)
	}
	return key, nil
}

// normalizeConfiguredBase is the normalizeBaseUrl of firecrawl.ts and crawl4ai.ts: nil when the
// value is not a non-blank string, an error naming the setting when it is not an HTTP(S) URL
// without credentials, and the URL without query, fragment or trailing slashes otherwise.
func normalizeConfiguredBase(v any, label string) (string, bool, error) {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return "", false, nil
	}
	s = jsTrim(s)
	bad := fmt.Errorf("Invalid %s base URL in %s: expected an HTTP or HTTPS URL", label, ConfigPath())
	u, err := ParseURL(s, nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, bad
	}
	if u.User != nil {
		return "", false, fmt.Errorf("Invalid %s base URL in %s: URL credentials are not allowed", label, ConfigPath())
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), true, nil
}

// stripSensitiveHeaders drops the headers withoutSensitiveHeaders removes upstream.
func stripSensitiveHeaders(h http.Header) http.Header {
	n := h.Clone()
	for _, k := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		n.Del(k)
	}
	return n
}

// ssrfValidation is the ValidationOptions a hosted fetch provider hands to the SSRF guard.
func ssrfValidation(ssrf SsrfConfig, lookup Lookup, allowLoopback bool) ValidationOptions {
	return ValidationOptions{Lookup: lookup, AllowRanges: ssrf.AllowRanges, TrustEnvProxy: ssrf.TrustEnvProxy, AllowLoopback: allowLoopback}
}

func isLoopbackHost(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && IsLoopbackHostname(JSHostname(u))
}

// validateTarget is validateRemoteUrl(url, { allowRanges, trustEnvProxy, domainPolicy, lookup }).
func validateTarget(ctx context.Context, raw string, ssrf SsrfConfig, lookup Lookup, withPolicy bool) error {
	o := ssrfValidation(ssrf, lookup, false)
	if withPolicy {
		policy, err := LoadFetchContentDomainPolicy()
		if err != nil {
			return err
		}
		o.DomainPolicy = &policy
	}
	_, err := ValidateRemoteURL(ctx, raw, o)
	return err
}

// guardedAPI fetches a provider API endpoint with the SSRF guard on every hop (fetchRemoteUrl).
func guardedAPI(parent context.Context, raw string, init RequestInit, timeout time.Duration, o FetchRemoteOptions) (*http.Response, context.Context, context.CancelFunc, error) {
	tctx, cancel := withTimeout(parent, timeout)
	o.Fetch = currentPageFetch()
	resp, err := FetchRemoteURL(tctx, raw, init, o)
	return resp, tctx, cancel, err
}

var jsonHeader = func() http.Header { return http.Header{"Content-Type": {"application/json"}} }

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// indentJSON is JSON.stringify(value, null, 2) for a value kept as raw JSON.
func indentJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return string(raw)
	}
	return b.String()
}

// jsToNumber is Number(s) for a string: ok is false for NaN.
func jsToNumber(s string) (float64, bool) {
	t := jsTrim(s)
	if t == "" {
		return 0, true
	}
	n, err := strconv.ParseFloat(t, 64)
	return n, err == nil
}

// parseJSONObjectLoose decodes a JSON object body; any other JSON value decodes to nil like
// `response.json() as Record`, which the callers treat as an object without fields.
func parseJSONObjectLoose(text string) (map[string]any, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func decodeAny(text string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(text), &v)
	return v, err
}

// redirectKeepsOnlyJSON is the onRedirect of kagi.ts and ollama.ts: a redirect to another origin
// keeps only the content headers (the credential is dropped); accept adds Accept: application/json.
func redirectKeepsOnlyJSON(accept bool) func(RedirectArgs) RequestInit {
	return func(a RedirectArgs) RequestInit {
		if Origin(a.To) == Origin(a.From) {
			return a.Init
		}
		init := a.Init
		init.Header = http.Header{"Content-Type": {"application/json"}}
		if accept {
			init.Header.Set("Accept", "application/json")
		}
		return init
	}
}

// rawGuardedFetch sends one request without following redirects, over the page transport seam
// when a test installed one and the SSRF-guarded transport otherwise.
func rawGuardedFetch(ctx context.Context, u *url.URL, init RequestInit, o ValidationOptions) (*http.Response, error) {
	if f := currentPageFetch(); f != nil {
		return f(ctx, u, init)
	}
	return guardedFetch(ctx, u, init, o)
}

// fetchAPIManual is fetchFirecrawlApi and fetchBrightDataApi: the redirect chain is walked by
// hand, every hop passes the SSRF guard, and a hop to another origin drops the credential
// headers. The method and body are replayed on every hop.
func fetchAPIManual(ctx context.Context, raw string, init RequestInit, o ValidationOptions) (*http.Response, error) {
	current, err := ValidateRemoteURL(ctx, raw, o)
	if err != nil {
		return nil, err
	}
	req := init
	for redirects := 0; ; redirects++ {
		resp, err := rawGuardedFetch(ctx, current, req, o)
		if err != nil {
			return nil, err
		}
		if !redirectStatuses[resp.StatusCode] {
			return resp, nil
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return resp, nil
		}
		_ = resp.Body.Close()
		if redirects == defaultMaxRedirects {
			return nil, fmt.Errorf("Too many redirects fetching %s", current.String())
		}
		nextURL, err := ParseURL(location, current)
		if err != nil {
			return nil, err
		}
		next, err := validateParsedRemoteURL(ctx, nextURL, o)
		if err != nil {
			return nil, err
		}
		if Origin(next) != Origin(current) {
			req.Header = stripSensitiveHeaders(req.Header)
		}
		current = next
	}
}
