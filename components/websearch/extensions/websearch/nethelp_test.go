package websearch

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeNet stands in for `globalThis.fetch` of the upstream tests: it records every request the
// providers send and answers from a handler.
type fakeNet struct {
	mu    sync.Mutex
	calls []netCall
	fn    func(c netCall) netReply
}

// netCall is one recorded request.
type netCall struct {
	URL    string
	Method string
	Header http.Header
	Body   string
	// Proxy is the proxy the call's context scopes ("" = none / direct).
	Proxy string
	// Ctx is the request's context, for assertions on its deadline and cancellation.
	Ctx context.Context
}

// netReply is a canned response; Err simulates a transport failure.
type netReply struct {
	Status int
	Body   string
	Header http.Header
	Err    error
	// Hang holds the request until its context ends, like a server that never answers.
	Hang bool
	// BodyErr makes reading the body fail after the status and headers arrived.
	BodyErr error
	// BodyStall makes a read of the body wait until the request's context ends and fail with its error: a
	// server that sent the headers and then went quiet, cut off by the deadline.
	BodyStall bool
}

func reply(status int, body string) netReply { return netReply{Status: status, Body: body} }

func redirectTo(status int, location string) netReply {
	return netReply{Status: status, Header: http.Header{"Location": {location}}}
}

func (f *fakeNet) Do(r *http.Request) (*http.Response, error) {
	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	c := netCall{URL: r.URL.String(), Method: r.Method, Header: r.Header.Clone(), Body: body, Proxy: activeProxy(r.Context()), Ctx: r.Context()}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	rep := f.fn(c)
	if rep.Hang {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	if rep.Err != nil {
		return nil, rep.Err
	}
	h := rep.Header
	if h == nil {
		h = http.Header{}
	}
	var respBody io.ReadCloser = io.NopCloser(strings.NewReader(rep.Body))
	if rep.BodyErr != nil {
		respBody = brokenBody{rep.BodyErr}
	}
	if rep.BodyStall {
		respBody = stalledBody{ctx: r.Context()}
	}
	return &http.Response{StatusCode: rep.Status, Status: http.StatusText(rep.Status), Header: h, Body: respBody, Request: r}, nil
}

// useNet installs a fake transport for the test.
func useNet(t *testing.T, fn func(c netCall) netReply) *fakeNet {
	t.Helper()
	f := &fakeNet{fn: fn}
	t.Cleanup(SetHTTP(f))
	// Page fetches (fetch_content) go through the same fake, like globalThis.fetch upstream.
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
		method := init.Method
		if method == "" {
			method = "GET"
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(string(init.Body)))
		if err != nil {
			return nil, err
		}
		req.Header = init.Header.Clone()
		if req.Header == nil {
			req.Header = http.Header{}
		}
		return f.Do(req)
	}))
	return f
}

func (f *fakeNet) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.URL
	}
	return out
}

func (f *fakeNet) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// query returns one query parameter of a recorded URL.
func query(rawURL, key string) string {
	u, _ := url.Parse(rawURL)
	return u.Query().Get(key)
}

func nf(v float64) *float64 { return &v }

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
