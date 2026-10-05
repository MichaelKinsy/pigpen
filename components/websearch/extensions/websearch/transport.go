package websearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// HTTPDoer is the transport seam: tests substitute it, the extension uses net/http.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// httpDoer is used for provider API calls (no SSRF guard: the endpoints are fixed or come from
// validated configuration). Tests replace it with SetHTTP.
var httpDoer HTTPDoer = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// SetHTTP replaces the transport used for provider API calls and returns a restore function.
func SetHTTP(d HTTPDoer) func() {
	prev := httpDoer
	httpDoer = d
	return func() { httpDoer = prev }
}

// guardedFetch sends one request over a transport that checks the resolved address when it
// connects (so a name that changes between validation and connection cannot reach an internal
// address) and that never follows redirects. Requests through an explicit proxy skip the dial
// check, because the proxy resolves the target; the URL was validated before.
func guardedFetch(ctx context.Context, u *url.URL, init RequestInit, o ValidationOptions) (*http.Response, error) {
	proxy := activeProxy(ctx)
	ranges, err := ParseAllowRanges(o.AllowRanges)
	if err != nil {
		return nil, err
	}
	if o.AllowLoopback {
		loop, _ := ParseAllowRanges(loopbackAllowRanges)
		ranges = append(append([]CIDR{}, ranges...), loop...)
	}
	tr := &http.Transport{
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 0,
		ForceAttemptHTTP2:     true,
	}
	switch {
	case proxy != "":
		p, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(p)
		tr.DialContext = (&net.Dialer{Timeout: 20 * time.Second}).DialContext
	case o.TrustEnvProxy && !hasScopedProxyDecision(ctx):
		tr.Proxy = http.ProxyFromEnvironment
		tr.DialContext = (&net.Dialer{Timeout: 20 * time.Second}).DialContext
	default:
		tr.DialContext = (&net.Dialer{Timeout: 20 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			return assertPublicAddress(host, u.Hostname(), ranges)
		}}).DialContext
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	method := init.Method
	if method == "" {
		method = "GET"
	}
	var body *bytes.Reader
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if init.Body != nil {
		body = bytes.NewReader(init.Body)
		req.Body = nopCloser{body}
		req.ContentLength = int64(len(init.Body))
	}
	for k, vs := range init.Header {
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
	return resp, nil
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

var apiRedirectStatuses = map[int]bool{301: true, 302: true, 303: true, 307: true, 308: true}

const maxAPIRedirects = 5

// FetchWithCredentialRedirects sends an API request and follows redirects by hand, dropping the
// credential headers when a redirect leaves the origin and the body headers when a 301/302 POST
// or a 303 turns into a GET.
func FetchWithCredentialRedirects(ctx context.Context, rawURL string, init RequestInit, credentialHeaders []string) (*http.Response, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	req := init
	req.Header = init.Header.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	for redirects := 0; ; redirects++ {
		resp, err := doAPI(ctx, current, req)
		if err != nil {
			return nil, err
		}
		if !apiRedirectStatuses[resp.StatusCode] {
			return resp, nil
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return resp, nil
		}
		if redirects == maxAPIRedirects {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("Too many API redirects from %s", rawURL)
		}
		_ = resp.Body.Close()
		next, err := current.Parse(location)
		if err != nil {
			return nil, err
		}
		if next.Scheme != "http" && next.Scheme != "https" {
			return nil, fmt.Errorf("API redirect from %s must use HTTP(S)", Origin(current))
		}
		method := strings.ToUpper(req.Method)
		if method == "" {
			method = "GET"
		}
		if ((resp.StatusCode == 301 || resp.StatusCode == 302) && method == "POST") || (resp.StatusCode == 303 && method != "GET" && method != "HEAD") {
			for _, name := range []string{"Content-Encoding", "Content-Language", "Content-Location", "Content-Type"} {
				req.Header.Del(name)
			}
			req.Body = nil
			req.Method = "GET"
		}
		if Origin(next) != Origin(current) {
			for _, name := range credentialHeaders {
				req.Header.Del(name)
			}
		}
		current = next
	}
}

func doAPI(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
	method := init.Method
	if method == "" {
		method = "GET"
	}
	var req *http.Request
	var err error
	if init.Body != nil {
		req, err = http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(init.Body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
	}
	if err != nil {
		return nil, err
	}
	for k, vs := range init.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := apiClientFor(ctx).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
	}
	return resp, err
}

// apiClientFor returns the transport for provider API calls: the scoped proxy applies to every
// outbound request of a call (search APIs included), like the original's proxied fetch.
func apiClientFor(ctx context.Context) HTTPDoer {
	if proxy := activeProxy(ctx); proxy != "" {
		if _, isDefault := httpDoer.(*http.Client); isDefault {
			if p, err := url.Parse(proxy); err == nil {
				return &http.Client{
					Transport:     &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) { return proxyOrBypass(r, p) }},
					CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				}
			}
		}
	}
	return httpDoer
}

// proxyOrBypass keeps localhost and NO_PROXY hosts off the proxy (isProxyBypassedUrl upstream).
func proxyOrBypass(r *http.Request, proxy *url.URL) (*url.URL, error) {
	host := strings.ToLower(strings.Trim(r.URL.Hostname(), "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "127.0.0.1" || host == "::1" {
		return nil, nil
	}
	for _, entry := range strings.Split(envNoProxy(), ",") {
		if noProxyEntryMatches(host, strings.TrimSpace(entry)) {
			return nil, nil
		}
	}
	return proxy, nil
}

func noProxyEntryMatches(hostname, entry string) bool {
	if entry == "" {
		return false
	}
	if entry == "*" {
		return true
	}
	host := entry
	if strings.HasPrefix(host, "[") {
		if closing := strings.Index(host, "]"); closing > 0 {
			host = host[:closing+1]
		}
	} else if colon := strings.LastIndex(host, ":"); colon > -1 && digitsOnly.MatchString(host[colon+1:]) {
		host = host[:colon]
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if hostname == host {
		return true
	}
	if strings.HasPrefix(host, ".") {
		return strings.HasSuffix(hostname, host)
	}
	return strings.HasSuffix(hostname, "."+host)
}
