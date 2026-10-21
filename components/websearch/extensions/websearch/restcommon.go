package websearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
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
func decodeJSON(text string) (any, error) { return unmarshalAny(text) }

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

// transportFailure gives a failure of the connection itself the message Node's fetch gives it:
// "fetch failed" for a request that could not be made or completed, "terminated" for a body cut
// short. The routing classifier reads "fetch failed" as a network failure, which is what lets
// searchRouting fall through; Go's own texts ("EOF", "connection refused") are not recognised.
// Anything else (a deadline, a cancellation, an error this extension made) is returned as it is.
func transportFailure(err error, body bool) error {
	if err == nil || strings.Contains(err.Error(), "Blocked internal address") {
		return err
	}
	var done *redactedError
	if errors.As(err, &done) && (done.msg == "fetch failed" || done.msg == "terminated") {
		return err
	}
	var ne net.Error
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var invalidCert x509.CertificateInvalidError
	var hostErr x509.HostnameError
	transport := (errors.As(err, &ne) && !ne.Timeout()) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.As(err, &certErr) || errors.As(err, &unknownAuth) ||
		errors.As(err, &invalidCert) || errors.As(err, &hostErr)
	if !transport || errors.Is(err, context.Canceled) {
		return err
	}
	if body {
		return &redactedError{"terminated", err}
	}
	return &redactedError{"fetch failed", err}
}

// successStatus is `response.ok`.
func successStatus(resp *http.Response) bool { return resp.StatusCode >= 200 && resp.StatusCode <= 299 }

// bodyRejection is what the body stream of a response rejects with when the body cannot be read
// after the headers arrived (`await response.text()` or `.json()`): the signal's reason once the
// caller aborted ("This operation was aborted") or the provider's own deadline fired ("The
// operation was aborted due to timeout"), "terminated" when the connection dropped.
func bodyRejection(parent, child context.Context, err error) error {
	if parent.Err() != nil {
		return &redactedError{"This operation was aborted", err}
	}
	if child.Err() == context.DeadlineExceeded {
		return &redactedError{providerTimeoutMessage, err}
	}
	return transportFailure(err, true)
}

// invalidJSONRead is the catch block around `await response.json()` of a successful response, which
// words every body that cannot be read, whatever the cause, as `<what> returned invalid JSON:
// <message of the rejection>`.
func invalidJSONRead(parent, child context.Context, what string, err error) error {
	return fmt.Errorf("%s returned invalid JSON: %s", what, bodyRejection(parent, child, err).Error())
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
	return redactErr(transportFailure(err, false), apiKey)
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
//
// The activity entry act is closed as those files do: a transport failure through the catch block
// (an abort completes it with status 0, anything else records the redacted message), a non-2xx
// status and an invalid JSON body complete it with the status. A successful call returns the status
// and leaves the entry open: the caller completes it once its envelope check ran.
func restCallA(ctx, tctx context.Context, act activityRef, api, apiKey, rawURL string, init RequestInit, credentialHeaders []string) (any, int, error) {
	resp, err := FetchWithCredentialRedirects(tctx, rawURL, init, credentialHeaders)
	if err != nil {
		return nil, 0, act.failedErr(redactErr(timeoutFix(ctx, tctx, transportFailure(err, false)), apiKey))
	}
	text, err := readBody(resp)
	ok := successStatus(resp)
	if err != nil {
		act.complete(resp.StatusCode)
		if ok {
			return nil, 0, invalidJSONRead(ctx, tctx, api, err)
		}
		return nil, 0, redactErr(timeoutFix(ctx, tctx, transportFailure(err, true)), apiKey)
	}
	if !ok {
		act.complete(resp.StatusCode)
		return nil, 0, fmt.Errorf("%s error %d: %s", api, resp.StatusCode, slice300(RedactCredential(text, apiKey)))
	}
	data, err := decodeJSON(text)
	if err != nil {
		act.complete(resp.StatusCode)
		return nil, 0, fmt.Errorf("%s returned invalid JSON: %v", api, err)
	}
	return data, resp.StatusCode, nil
}

// restCallB is the request/response mechanics of serpapi.ts, serply.ts and you.ts: the same
// status and JSON handling, with every failure (including the caller's envelope check, passed as
// check) going through restFailure so a deadline or a cancellation keeps its own message. The
// activity entry is closed in the same catch block: a caller that cancelled completes it with
// status 0, any other failure records the message, and success completes it with the status.
func restCallB(ctx, tctx context.Context, act activityRef, name, apiKey, rawURL string, init RequestInit, credentialHeaders []string, check func(any) error) (any, error) {
	fail := func(err error) (any, error) {
		return nil, act.failedBy(ctx, restFailure(ctx, tctx, name, apiKey, err), apiKey)
	}
	resp, err := FetchWithCredentialRedirects(tctx, rawURL, init, credentialHeaders)
	if err != nil {
		return fail(err)
	}
	text, err := readBody(resp)
	if err != nil {
		if successStatus(resp) {
			// serpapi.ts rethrows a TimeoutError out of the JSON catch block and its outer catch block answers a
			// cancelled caller first: restFailure sorts both out whatever the wording of err.
			return fail(invalidJSONRead(ctx, tctx, name, err))
		}
		return fail(transportFailure(err, true))
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
	act.complete(resp.StatusCode)
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
