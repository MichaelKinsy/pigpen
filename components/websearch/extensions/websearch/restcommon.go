package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Helpers shared by the keyed REST search providers (serper, serpapi, serpbase, serply, valyu,
// bocha, you, anysearch, xcrawl, jina-search). Each of those upstream files repeats the same
// config load, error and timeout handling; the Go ports keep one file per provider and share the
// mechanics here.

// restTimeout is the request deadline of a provider. Tests shorten it (the upstream tests patch
// AbortSignal.timeout to do the same).
var restTimeout = func(d time.Duration) time.Duration { return d }

const restSearchTimeout = 60 * time.Second

// restConfigErr is the first thing every upstream provider module does: load web-search.json,
// failing on a malformed file or a non-object root.
func restConfigErr() error {
	_, err := ReadConfigRoot()
	return err
}

// marshalJS is JSON.stringify for a value built from structs and ordered fields: no HTML escaping.
func marshalJS(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// normalizeCountDefault is the local `normalizeCount` of serpbase.ts (default 10) and bocha.ts
// (default 8): the shared 1..20 clamp with a provider default for an unset or non-finite value.
func normalizeCountDefault(v *float64, def int) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return def
	}
	n := math.Floor(*v)
	if n > 20 {
		n = 20
	}
	if n < 1 {
		n = 1
	}
	return int(n)
}

// decodeJSON parses a response body into a generic value, rejecting trailing data like JSON.parse.
func decodeJSON(text string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// jsonObject is the `value && typeof value === "object" && !Array.isArray(value)` guard.
func jsonObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// nonEmptyString is `typeof v === "string" && v.trim()` followed by the trimmed value.
func trimmedString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return "", false
	}
	return jsTrim(s), true
}

// isProviderTimeout reports whether err is the provider's own deadline (as opposed to the
// caller's cancellation): the child context expired, or the transport says it timed out.
func isProviderTimeout(parent, child context.Context, err error) bool {
	if parent.Err() != nil {
		return false
	}
	if child.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// restFailure is the catch block of serpapi.ts, serply.ts, you.ts and xcrawl.ts: a caller that
// cancelled gets "Aborted", the provider's own deadline becomes "<what> timed out after 60s"
// (a retriable network failure, never an abort), anything else has the credential removed.
func restFailure(parent, child context.Context, what, apiKey string, err error) error {
	if parent.Err() != nil {
		return errors.New("Aborted")
	}
	if isProviderTimeout(parent, child, err) {
		return fmt.Errorf("%s request timed out after 60s", what)
	}
	return redactErr(err, apiKey)
}

// providerKeyMissing builds the "<Label> API key not found. Either:" message of the providers
// that print the same three lines.
func providerKeyMissing(label, configKey, env, getKeyURL string) error {
	return fmt.Errorf("%s API key not found. Either:\n  1. Create %s with { \"%s\": \"your-key\" }\n  2. Set %s environment variable\nGet a key at %s", label, ConfigPath(), configKey, env, getKeyURL)
}

// requireProviderKey resolves a credential and fails with the provider's missing-key message.
func requireProviderKey(ctx context.Context, credProvider, label, configKey, env, getKeyURL string) (string, error) {
	if err := restConfigErr(); err != nil {
		return "", err
	}
	key, err := resolveProviderCredential(ctx, credProvider, configKey, env)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", providerKeyMissing(label, configKey, env, getKeyURL)
	}
	return key, nil
}

// googleRecencyTBS is RECENCY_TBS of serper.ts, serpapi.ts, serpbase.ts and serply.ts.
var googleRecencyTBS = map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}

// buildSiteQuery is buildQuery of the Google-style providers: a single include is `site:x`,
// several are a parenthesised OR group, every exclude is `-site:x`.
func buildSiteQuery(query string, f domainFilters) string {
	parts := []string{query}
	if len(f.allowed) == 1 {
		parts = append(parts, "site:"+f.allowed[0])
	} else if len(f.allowed) > 1 {
		sites := make([]string, len(f.allowed))
		for i, d := range f.allowed {
			sites[i] = "site:" + d
		}
		parts = append(parts, "("+strings.Join(sites, " OR ")+")")
	}
	for _, d := range f.blocked {
		parts = append(parts, "-site:"+d)
	}
	return strings.Join(parts, " ")
}

// googleRequestCount is requestCount of the Google-style providers: five more than asked when a
// domain filter will thin the results, at most 20.
func googleRequestCount(o SearchOptions, numResults int) int {
	if len(o.DomainFilter) > 0 {
		if numResults+5 < 20 {
			return numResults + 5
		}
		return 20
	}
	return numResults
}

// restCallA is the request/response mechanics of serper.ts, serpbase.ts, valyu.ts, bocha.ts and
// anysearch.ts: a transport failure has the credential removed, a non-2xx status quotes up to 300
// characters of the (redacted) body, and a body that is not JSON is "<api> returned invalid JSON".
// api is the upstream name ("Serper API"). The caller owns the deadline context (tctx).
func restCallA(ctx, tctx context.Context, api, apiKey, rawURL string, init RequestInit, credentialHeaders []string) (any, error) {
	resp, err := FetchWithCredentialRedirects(tctx, rawURL, init, credentialHeaders)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	text, err := readBody(resp)
	if err != nil {
		return nil, redactErr(timeoutFix(ctx, tctx, err), apiKey)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s error %d: %s", api, resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	data, err := decodeJSON(text)
	if err != nil {
		return nil, fmt.Errorf("%s returned invalid JSON: %v", api, err)
	}
	return data, nil
}

// restCallB is the request/response mechanics of serpapi.ts, serply.ts and you.ts: the same
// status and JSON handling, with every failure (including the caller's envelope check, passed as
// check) going through restFailure so a deadline or a cancellation keeps its own message.
func restCallB(ctx, tctx context.Context, name, apiKey, rawURL string, init RequestInit, credentialHeaders []string, check func(any) error) (any, error) {
	fail := func(err error) (any, error) { return nil, restFailure(ctx, tctx, name, apiKey, err) }
	resp, err := FetchWithCredentialRedirects(tctx, rawURL, init, credentialHeaders)
	if err != nil {
		return fail(err)
	}
	text, err := readBody(resp)
	if err != nil {
		return fail(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail(fmt.Errorf("%s error %d: %s", name, resp.StatusCode, slice300(RedactCredential(text, apiKey))))
	}
	data, err := decodeJSON(text)
	if err != nil {
		return fail(fmt.Errorf("%s returned invalid JSON: %v", name, err))
	}
	if err := check(data); err != nil {
		return fail(err)
	}
	return data, nil
}

// headerOf builds a request header from name/value pairs with canonical names, so that dropping a
// credential header on a cross-origin redirect finds it whatever case the upstream wrote.
func headerOf(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}
