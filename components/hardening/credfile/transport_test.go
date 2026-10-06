package credfile

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

type recorder struct {
	mu   sync.Mutex
	seen []string // the credential header of each request, "" when absent
	srv  *httptest.Server
}

func newRecorder(t *testing.T, header string, handler http.HandlerFunc) *recorder {
	r := &recorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, req.Header.Get(header))
		r.mu.Unlock()
		if handler != nil {
			handler(w, req)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func get(t *testing.T, c *http.Client, url string, hdr ...string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return c.Do(req)
}

func originOf(r *recorder) string { return r.srv.URL }

func TestOnlyTheBoundOriginGetsTheCredential(t *testing.T) {
	bound := newRecorder(t, "Authorization", nil)
	other := newRecorder(t, "Authorization", nil)
	f := newFixture(t)
	f.cfg.Origin = originOf(bound)
	f.write(oauth("tok-A", t0.Add(24*365*time.Hour)))
	s, err := New("websearch", &f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: s.RoundTripper(nil)}
	for _, u := range []string{bound.srv.URL + "/a", bound.srv.URL, other.srv.URL + "/b"} {
		resp, err := get(t, c, u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if g := bound.got(); len(g) != 2 || g[0] != "Bearer tok-A" || g[1] != "Bearer tok-A" {
		t.Fatalf("bound: %q", g)
	}
	if g := other.got(); len(g) != 1 || g[0] != "" {
		t.Fatalf("other origin received a credential: %q", g)
	}
}

func TestACrossOriginRedirectDoesNotCarryTheCredential(t *testing.T) {
	// A header that net/http does not strip on redirect (it only strips Authorization and Cookie).
	for _, header := range []string{"Authorization", "X-Api-Key"} {
		target := newRecorder(t, header, nil)
		start := newRecorder(t, header, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.srv.URL+"/landed", http.StatusFound)
		})
		f := newFixture(t)
		f.cfg.Origin = originOf(start)
		if header != "Authorization" {
			f.cfg.Header, f.cfg.Scheme = header, ""
		}
		f.write(`{"search":{"type":"api_key","key":"sk-live"}}`)
		s, _ := New("websearch", &f.cfg)
		c := &http.Client{Transport: s.RoundTripper(nil)}
		resp, err := get(t, c, start.srv.URL+"/go")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if g := start.got(); len(g) != 1 || !strings.HasSuffix(g[0], "sk-live") {
			t.Fatalf("%s: start received %q", header, g)
		}
		if g := target.got(); len(g) != 1 || g[0] != "" {
			t.Fatalf("%s: the redirect target received %q", header, g)
		}
	}
}

func TestASameOriginRedirectKeepsTheCredential(t *testing.T) {
	var rec *recorder
	rec = newRecorder(t, "Authorization", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			http.Redirect(w, r, "/second", http.StatusFound)
		}
	})
	f := newFixture(t)
	f.cfg.Origin = originOf(rec)
	f.write(`{"search":{"type":"api_key","key":"sk-live"}}`)
	s, _ := New("websearch", &f.cfg)
	resp, err := get(t, &http.Client{Transport: s.RoundTripper(nil)}, rec.srv.URL+"/first")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if g := rec.got(); len(g) != 2 || g[0] != "Bearer sk-live" || g[1] != "Bearer sk-live" {
		t.Fatalf("%q", g)
	}
}

func TestACredentialHeaderTheCallerSetForAnotherOriginIsRemoved(t *testing.T) {
	bound := newRecorder(t, "Authorization", nil)
	other := newRecorder(t, "Authorization", nil)
	f := newFixture(t)
	f.cfg.Origin = originOf(bound)
	f.write(`{"search":{"type":"api_key","key":"sk-live"}}`)
	s, _ := New("websearch", &f.cfg)
	c := &http.Client{Transport: s.RoundTripper(nil)}
	req, _ := http.NewRequest(http.MethodGet, other.srv.URL, nil)
	req.Header.Set("Authorization", "Bearer sk-live")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if g := other.got(); len(g) != 1 || g[0] != "" {
		t.Fatalf("%q", g)
	}
	if req.Header.Get("Authorization") != "Bearer sk-live" {
		t.Fatal("the caller's request was modified")
	}
	// And a header the caller set for the bound origin is replaced, not appended to.
	req, _ = http.NewRequest(http.MethodGet, bound.srv.URL, nil)
	req.Header.Set("Authorization", "Bearer caller-chosen")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if g := bound.got(); len(g) != 1 || g[0] != "Bearer sk-live" {
		t.Fatalf("%q", g)
	}
	if req.Header.Get("Authorization") != "Bearer caller-chosen" {
		t.Fatal("the caller's request was modified")
	}
}

func TestADifferentPortOrSchemeIsAnotherOrigin(t *testing.T) {
	a := newRecorder(t, "Authorization", nil)
	b := newRecorder(t, "Authorization", nil) // same host, another port
	f := newFixture(t)
	f.cfg.Origin = originOf(a)
	f.write(`{"search":{"type":"api_key","key":"sk-live"}}`)
	s, _ := New("websearch", &f.cfg)
	c := &http.Client{Transport: s.RoundTripper(nil)}
	resp, _ := get(t, c, b.srv.URL)
	resp.Body.Close()
	if g := b.got(); g[0] != "" {
		t.Fatalf("%q", g)
	}
	// the same host and port over https is another origin (the request fails: the server speaks http)
	tlsURL := strings.Replace(a.srv.URL, "http://", "https://", 1)
	seen := &captureTransport{}
	s.RoundTripper(seen).RoundTrip(mustReq(t, tlsURL))
	if seen.header != "" {
		t.Fatalf("https request on the http origin's port carried %q", seen.header)
	}
}

type captureTransport struct{ header string }

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.header = r.Header.Get("Authorization")
	return &http.Response{StatusCode: 200, Body: http.NoBody, Request: r}, nil
}

func mustReq(t *testing.T, url string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestAnUnavailableCredentialFailsTheRequestBeforeAnythingIsSent(t *testing.T) {
	bound := newRecorder(t, "Authorization", nil)
	f := newFixture(t)
	f.cfg.Origin = originOf(bound)
	s, _ := New("websearch", &f.cfg)
	c := &http.Client{Transport: s.RoundTripper(nil)}
	for name, content := range map[string]string{"missing": "", "expired": oauth("t", time.Now().Add(-time.Hour)), "malformed": "{"} {
		if content != "" {
			f.write(content)
		}
		_, err := get(t, c, bound.srv.URL)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		code := profile.CodeOf(err)
		if code != profile.CredentialUnavailable && code != profile.CredentialExpired && code != profile.CredentialMalformed {
			t.Fatalf("%s: err = %v", name, err)
		}
		if strings.Contains(err.Error(), f.path) {
			t.Fatalf("%s: error names the path: %v", name, err)
		}
	}
	if g := bound.got(); len(g) != 0 {
		t.Fatalf("a request was sent without a credential: %q", g)
	}
	// There is no fallback: another origin is untouched by the failure.
	other := newRecorder(t, "Authorization", nil)
	resp, err := get(t, c, other.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestTheRequestBodyIsClosedWhenTheCredentialFails(t *testing.T) {
	f := newFixture(t)
	f.cfg.Origin = "http://127.0.0.1:1"
	s, _ := New("websearch", &f.cfg)
	body := &closeTracker{Reader: strings.NewReader("payload")}
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/x", body)
	if _, err := s.RoundTripper(nil).RoundTrip(req); err == nil {
		t.Fatal("no error")
	}
	if !body.closed {
		t.Fatal("body not closed")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func TestARotatedFileIsUsedByTheNextRequest(t *testing.T) {
	bound := newRecorder(t, "Authorization", nil)
	f := newFixture(t)
	f.cfg.Origin = originOf(bound)
	s, _ := New("websearch", &f.cfg)
	c := &http.Client{Transport: s.RoundTripper(nil)}
	f.write(oauth("one", time.Now().Add(time.Hour)))
	resp, err := get(t, c, bound.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f.write(oauth("two", time.Now().Add(time.Hour)))
	resp, err = get(t, c, bound.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if g := bound.got(); len(g) != 2 || g[0] != "Bearer one" || g[1] != "Bearer two" {
		t.Fatalf("%q", g)
	}
}

func TestACancelledRequestFailsAsCancelled(t *testing.T) {
	bound := newRecorder(t, "Authorization", nil)
	f := newFixture(t)
	f.cfg.Origin = originOf(bound)
	f.write(oauth("one", time.Now().Add(time.Hour)))
	s, _ := New("websearch", &f.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, bound.srv.URL, nil)
	_, err := s.RoundTripper(nil).RoundTrip(req)
	if !profile.IsCode(err, profile.Cancelled) {
		t.Fatalf("%v", err)
	}
}
