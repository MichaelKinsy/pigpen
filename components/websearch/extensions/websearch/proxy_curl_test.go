package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// W15 + W12: a proxied request runs `curl` through the host exactly as utils.ts fetchViaCurl does
// (arguments, header dump, write-out JSON, temporary files), so SOCKS4 and SOCKS4a come with it.
// fakeCurl is the twin of the fake `curl` script of proxy-transport.test.mjs (withFakeCurl): it
// records the arguments, writes the header dump and the body to the files the arguments name, and
// prints the write-out JSON.

type curlRoute struct {
	Status     int
	StatusText string
	Location   string
	Body       string
	// Dump replaces the header dump; Stdout the write-out JSON; Code and Stderr make curl fail.
	Dump   *string
	Stdout *string
	Code   int
	Stderr string
	// Delay holds curl before it answers.
	Delay time.Duration
}

type curlCall struct {
	Command string
	Args    []string
	Timeout time.Duration
	// RequestBody is the content of the file --data-binary names, read while curl "runs".
	RequestBody *string
	// Dir is the temporary directory of the files -D, --output and the request body name.
	Dir string
}

type fakeCurl struct {
	mu     sync.Mutex
	calls  []curlCall
	routes map[string]curlRoute
	// err makes the host refuse to run the command.
	err error
}

func valueAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *fakeCurl) run(ctx context.Context, command string, args []string, timeout time.Duration) (ExecResult, error) {
	call := curlCall{Command: command, Args: append([]string(nil), args...), Timeout: timeout}
	headerFile, bodyFile := valueAfter(args, "-D"), valueAfter(args, "--output")
	call.Dir = filepath.Dir(headerFile)
	if data := valueAfter(args, "--data-binary"); strings.HasPrefix(data, "@") {
		b, err := os.ReadFile(data[1:])
		if err == nil {
			s := string(b)
			call.RequestBody = &s
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	route, ok := f.routes[args[len(args)-1]]
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return ExecResult{}, err
	}
	if route.Delay > 0 {
		select {
		case <-time.After(route.Delay):
		case <-ctx.Done():
			return ExecResult{Killed: true}, nil
		}
	}
	if !ok {
		return ExecResult{Code: 1, Stderr: "unexpected url " + args[len(args)-1]}, nil
	}
	if route.Code != 0 && route.Stdout == nil {
		return ExecResult{Code: route.Code, Stderr: route.Stderr}, nil
	}
	dump := "HTTP/1.1 " + itoa(route.Status) + " " + route.StatusText + "\r\n"
	if route.Location != "" {
		dump += "Location: " + route.Location + "\r\n"
	}
	dump += "\r\n"
	if route.Dump != nil {
		dump = *route.Dump
	}
	if err := os.WriteFile(headerFile, []byte(dump), 0o600); err != nil {
		return ExecResult{}, err
	}
	if err := os.WriteFile(bodyFile, []byte(route.Body), 0o600); err != nil {
		return ExecResult{}, err
	}
	out := `{"url_effective":` + jsonString(args[len(args)-1]) + `,"num_redirects":0}`
	if route.Stdout != nil {
		out = *route.Stdout
	}
	return ExecResult{Stdout: out, Code: route.Code, Stderr: route.Stderr}, nil
}

// useCurl installs the fake as the host's command runner and keeps the environment's proxy rules
// out of the way (the fixture sets NO_PROXY to "" for the same reason).
func useCurl(t *testing.T, routes map[string]curlRoute) *fakeCurl {
	t.Helper()
	unsetenv(t, "NO_PROXY", "no_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy")
	f := &fakeCurl{routes: routes}
	t.Cleanup(SetExecRunner(f.run))
	return f
}

func (f *fakeCurl) snapshot() []curlCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]curlCall(nil), f.calls...)
}

func (f *fakeCurl) argsOf(target string) []string {
	for _, c := range f.snapshot() {
		if len(c.Args) > 0 && c.Args[len(c.Args)-1] == target {
			return c.Args
		}
	}
	return nil
}

func headerValues(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "-H" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	noErr(t, err)
	return u
}

const curlProxy = "http://proxy.example:8080/"

func TestCurlTransportArguments(t *testing.T) {
	ok := curlRoute{Status: 200, StatusText: "OK", Body: "ok"}

	t.Run("a GET runs curl with the original's arguments in the original's order", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": ok})
		resp, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/page"), RequestInit{Method: "GET", Header: http.Header{
			"User-Agent": {"ua"}, "Accept": {"text/html", "*/*"}, "X-Empty": {""}, "Cache-Control": {"no-cache"},
		}}, curlProxy)
		noErr(t, err)
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "ok" || resp.StatusCode != 200 {
			t.Fatalf("%d %q", resp.StatusCode, body)
		}
		calls := f.snapshot()
		if len(calls) != 1 || calls[0].Command != "curl" || calls[0].Timeout != 0 {
			t.Fatalf("%+v", calls)
		}
		a := calls[0].Args
		dir := calls[0].Dir
		if !strings.HasPrefix(filepath.Base(dir), "pi-web-access-proxy-") {
			t.Fatalf("temporary directory %q", dir)
		}
		want := []string{"--silent", "--show-error", "--compressed", "--connect-timeout", "20", "-x", curlProxy,
			"-D", filepath.Join(dir, "headers"), "--output", filepath.Join(dir, "body"), "--write-out", "%{json}",
			"-H", "accept: text/html, */*", "-H", "cache-control: no-cache", "-H", "user-agent: ua",
			"https://93.184.216.34/page"}
		if !reflect.DeepEqual(a, want) {
			t.Fatalf("got  %q\nwant %q", a, want)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("the temporary directory was not removed: %v", err)
		}
	})

	t.Run("a POST adds -X, the headers and the body file", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://api.example.test/v1": ok})
		_, err := curlFetchOnce(bg(), mustURL(t, "https://api.example.test/v1"), RequestInit{Method: "POST", Body: []byte(`{"q":1}`),
			Header: http.Header{"Content-Type": {"application/json"}}}, "socks5h://proxy.example:9050")
		noErr(t, err)
		c := f.snapshot()[0]
		dir := c.Dir
		want := []string{"--silent", "--show-error", "--compressed", "--connect-timeout", "20", "-x", "socks5h://proxy.example:9050",
			"-D", filepath.Join(dir, "headers"), "--output", filepath.Join(dir, "body"), "--write-out", "%{json}",
			"-X", "POST", "-H", "content-type: application/json",
			"--data-binary", "@" + filepath.Join(dir, "request-body"), "https://api.example.test/v1"}
		if !reflect.DeepEqual(c.Args, want) {
			t.Fatalf("got  %q\nwant %q", c.Args, want)
		}
		if c.RequestBody == nil || *c.RequestBody != `{"q":1}` {
			t.Fatalf("request body %v", c.RequestBody)
		}
	})

	t.Run("a GET with a body puts -X GET first", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://api.example.test/v1": ok})
		_, err := curlFetchOnce(bg(), mustURL(t, "https://api.example.test/v1"), RequestInit{Method: "GET", Body: []byte("x")}, curlProxy)
		noErr(t, err)
		a := f.snapshot()[0].Args
		if a[0] != "-X" || a[1] != "GET" || a[2] != "--silent" {
			t.Fatalf("%q", a)
		}
	})

	t.Run("HEAD sends no -X", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/h": ok})
		_, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/h"), RequestInit{Method: "head"}, curlProxy)
		noErr(t, err)
		for _, a := range f.snapshot()[0].Args {
			if a == "-X" {
				t.Fatalf("%+v", f.snapshot()[0].Args)
			}
		}
	})

	t.Run("an empty method is a GET", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/h": ok})
		_, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/h"), RequestInit{}, curlProxy)
		noErr(t, err)
		for _, a := range f.snapshot()[0].Args {
			if a == "-X" {
				t.Fatalf("%+v", f.snapshot()[0].Args)
			}
		}
	})
}

func TestCurlTransportResponse(t *testing.T) {
	get := func(t *testing.T, route curlRoute) (*http.Response, string, error) {
		t.Helper()
		extractEnv(t, "")
		useCurl(t, map[string]curlRoute{"https://93.184.216.34/p": route})
		resp, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/p"), RequestInit{Method: "GET"}, curlProxy)
		if err != nil {
			return nil, "", err
		}
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b), nil
	}
	dump := func(s string) *string { return &s }

	t.Run("the last status block of the header dump wins and content-encoding and content-length are dropped", func(t *testing.T) {
		resp, body, err := get(t, curlRoute{Body: "decoded", Dump: dump(
			"HTTP/1.1 100 Continue\r\n\r\nHTTP/2 200 \r\nContent-Type: text/html; charset=utf-8\r\nContent-Encoding: gzip\r\nContent-Length: 99\r\nX-Multi: a\r\nSet-Cookie: s=1\r\n\r\n")})
		noErr(t, err)
		if resp.StatusCode != 200 || body != "decoded" || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
			resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Content-Length") != "" || resp.Header.Get("X-Multi") != "a" {
			t.Fatalf("%d %v %q", resp.StatusCode, resp.Header, body)
		}
	})
	t.Run("the status text comes from the dump", func(t *testing.T) {
		resp, _, err := get(t, curlRoute{Status: 404, StatusText: "Not Found"})
		noErr(t, err)
		if resp.StatusCode != 404 || resp.Status != "404 Not Found" {
			t.Fatalf("%d %q", resp.StatusCode, resp.Status)
		}
	})
	t.Run("204, 205 and 304 have no body", func(t *testing.T) {
		for _, s := range []int{204, 205, 304} {
			resp, body, err := get(t, curlRoute{Status: s, Body: "ignored"})
			noErr(t, err)
			if resp.StatusCode != s || body != "" {
				t.Fatalf("%d %q", s, body)
			}
		}
	})
	t.Run("the effective URL of the write-out JSON is the response URL", func(t *testing.T) {
		out := `{"url_effective":"https://93.184.216.34/final","num_redirects":1}`
		resp, _, err := get(t, curlRoute{Status: 200, StatusText: "OK", Stdout: &out})
		noErr(t, err)
		if resp.Request == nil || resp.Request.URL.String() != "https://93.184.216.34/final" {
			t.Fatalf("%+v", resp.Request)
		}
	})
	t.Run("write-out output that is not JSON keeps the requested URL", func(t *testing.T) {
		out := "not json"
		resp, _, err := get(t, curlRoute{Status: 200, StatusText: "OK", Stdout: &out})
		noErr(t, err)
		if resp.Request.URL.String() != "https://93.184.216.34/p" {
			t.Fatalf("%v", resp.Request.URL)
		}
	})
	t.Run("a redirect is returned as it is, not followed", func(t *testing.T) {
		resp, _, err := get(t, curlRoute{Status: 302, StatusText: "Found", Location: "https://other.example/final"})
		noErr(t, err)
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://other.example/final" {
			t.Fatalf("%d %v", resp.StatusCode, resp.Header)
		}
	})
	t.Run("no HTTP status in the dump is an error", func(t *testing.T) {
		_, _, err := get(t, curlRoute{Dump: dump("garbage\r\n\r\n")})
		wantErr(t, err, `^Proxy fetch to https://93\.184\.216\.34/p via http://proxy\.example:8080/ returned no HTTP status$`)
	})
	t.Run("curl failing with no output reports its exit code, stderr and the redacted proxy", func(t *testing.T) {
		extractEnv(t, "")
		useCurl(t, map[string]curlRoute{"https://93.184.216.34/p": {Code: 7, Stderr: "curl: (7) Failed to connect\n"}})
		_, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/p"), RequestInit{Method: "GET"}, "http://user:secret@proxy.example:8080/")
		wantErr(t, err, `^curl exited with code 7 via http://redacted:redacted@proxy\.example:8080/: curl: \(7\) Failed to connect$`)
		if strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	})
	t.Run("curl failing after it printed output still yields the response", func(t *testing.T) {
		out := `{"url_effective":"https://93.184.216.34/p","num_redirects":0}`
		resp, body, err := get(t, curlRoute{Status: 200, StatusText: "OK", Body: "partial", Code: 18, Stdout: &out, Stderr: "curl: (18) transfer closed"})
		noErr(t, err)
		if resp.StatusCode != 200 || body != "partial" {
			t.Fatalf("%d %q", resp.StatusCode, body)
		}
	})
	t.Run("a host that cannot run curl is reported as a failure to start", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, nil)
		f.err = errors.New("exec refused")
		_, err := curlFetchOnce(bg(), mustURL(t, "https://93.184.216.34/p"), RequestInit{Method: "GET"}, curlProxy)
		wantErr(t, err, `^curl failed to start: exec refused$`)
	})
}

func TestCurlTransportCancellation(t *testing.T) {
	t.Run("a pre-aborted request does not start curl", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/abort": {Status: 200, StatusText: "OK", Body: "late"}})
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := curlFetchOnce(ctx, mustURL(t, "https://93.184.216.34/abort"), RequestInit{Method: "GET"}, curlProxy)
		wantErr(t, err, `(?i)abort`)
		if n := len(f.snapshot()); n != 0 {
			t.Fatalf("curl ran %d times", n)
		}
	})
	t.Run("an abort while curl runs is an abort and removes the temporary files", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/slow": {Status: 200, StatusText: "OK", Delay: 5 * time.Second}})
		ctx, cancel := cancelAfter(40 * time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := curlFetchOnce(ctx, mustURL(t, "https://93.184.216.34/slow"), RequestInit{Method: "GET"}, curlProxy)
		wantErr(t, err, `(?i)abort`)
		if time.Since(started) > 3*time.Second {
			t.Fatal("the abort did not interrupt the wait")
		}
		if dir := f.snapshot()[0].Dir; dir != "" {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("temporary directory left behind: %v", err)
			}
		}
	})
}

func TestProxyBypass(t *testing.T) {
	unsetenv(t, "NO_PROXY", "no_proxy")
	t.Setenv("NO_PROXY", "internal.example, .corp.test:8443,[::1],*.skipped")
	for target, bypass := range map[string]bool{
		"https://api.example.com/x":   false,
		"http://localhost:8080/":      true,
		"http://a.localhost/":         true,
		"http://127.0.0.1/":           true,
		"http://[::1]:9/":             true,
		"https://internal.example/":   true,
		"https://x.internal.example/": true,
		"https://x.corp.test/":        true,
		"https://corp.test/":          false, // ".corp.test" only matches subdomains
		"https://notinternal.example": false,
	} {
		if got := isProxyBypassedURL(mustURL(t, target)); got != bypass {
			t.Errorf("%s: bypassed=%v, want %v", target, got, bypass)
		}
	}
	t.Setenv("NO_PROXY", "*")
	if !isProxyBypassedURL(mustURL(t, "https://anything.example/")) {
		t.Error("NO_PROXY=* must bypass every host")
	}
}

func TestProxiedRequestsRunCurl(t *testing.T) {
	ok := curlRoute{Status: 200, StatusText: "OK", Body: "through the proxy"}

	t.Run("the page fetch of a proxied scope goes through curl, not net/http", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": ok})
		ctx := WithProxy(bg(), "http://call-proxy.example:8080/")
		resp, err := guardedFetch(ctx, mustURL(t, "https://93.184.216.34/page"), RequestInit{Method: "GET", Header: http.Header{"Accept": {"text/html"}}}, ValidationOptions{})
		noErr(t, err)
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "through the proxy" || valueAfter(f.snapshot()[0].Args, "-x") != "http://call-proxy.example:8080/" {
			t.Fatalf("%s %+v", b, f.snapshot())
		}
	})

	t.Run("a manual redirect is a redirect response and one curl call", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/start": {Status: 302, StatusText: "Found", Location: "https://other.example/final"}})
		ctx := WithProxy(bg(), curlProxy)
		resp, err := guardedFetch(ctx, mustURL(t, "https://93.184.216.34/start"), RequestInit{Method: "GET"}, ValidationOptions{})
		noErr(t, err)
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://other.example/final" || len(f.snapshot()) != 1 {
			t.Fatalf("%d %v %d", resp.StatusCode, resp.Header, len(f.snapshot()))
		}
	})

	t.Run("a pre-aborted proxied page fetch does not start curl", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/abort": ok})
		ctx, cancel := context.WithCancel(WithProxy(bg(), curlProxy))
		cancel()
		_, err := guardedFetch(ctx, mustURL(t, "https://93.184.216.34/abort"), RequestInit{Method: "GET"}, ValidationOptions{})
		wantErr(t, err, `(?i)abort`)
		if len(f.snapshot()) != 0 {
			t.Fatal("curl ran")
		}
	})

	t.Run("a bypassed host is fetched directly, still behind the dial-time address check", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, nil)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct") }))
		defer srv.Close()
		ctx := WithProxy(bg(), curlProxy)
		u := mustURL(t, srv.URL)
		if _, err := guardedFetch(ctx, u, RequestInit{Method: "GET"}, ValidationOptions{}); err == nil {
			t.Fatal("the loopback address must stay blocked at connect")
		}
		resp, err := guardedFetch(ctx, u, RequestInit{Method: "GET"}, ValidationOptions{AllowLoopback: true})
		noErr(t, err)
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "direct" || len(f.snapshot()) != 0 {
			t.Fatalf("%s, curl ran %d times", b, len(f.snapshot()))
		}
	})

	t.Run("provider API requests of a proxied scope go through curl with the body and headers", func(t *testing.T) {
		extractEnv(t, "")
		t.Setenv("TAVILY_API_KEY", "tvly-key")
		f := useCurl(t, map[string]curlRoute{"https://api.tavily.com/search": {Status: 200, StatusText: "OK", Body: tavilyBody("an answer", map[string]string{"title": "T", "url": "https://93.184.216.34/s", "content": "c"})}})
		ctx := WithProxy(bg(), "http://call-proxy.example:8080/")
		r, err := SearchWithTavily(ctx, "q", SearchOptions{})
		noErr(t, err)
		if r.Answer != "an answer" {
			t.Fatalf("%+v", r)
		}
		c := f.snapshot()[0]
		if valueAfter(c.Args, "-X") != "POST" || valueAfter(c.Args, "-x") != "http://call-proxy.example:8080/" ||
			!reflect.DeepEqual(headerValues(c.Args), []string{"authorization: Bearer tvly-key", "content-type: application/json"}) ||
			c.RequestBody == nil || !strings.Contains(*c.RequestBody, `"query":"q"`) {
			t.Fatalf("%q %v", c.Args, c.RequestBody)
		}
	})
}

func TestSocksProxiesGoToCurl(t *testing.T) {
	for _, scheme := range []string{"socks4", "socks4a", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			extractEnv(t, "")
			f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": {Status: 200, StatusText: "OK", Body: "through socks proxy", Dump: strp("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n")}})
			proxy := scheme + "://proxy.example:9050"
			ctx, err := ScopeProxy(bg(), &proxy)
			noErr(t, err)
			res := ExtractContent(ctx, "https://93.184.216.34/page", ExtractOptions{})
			if res.Error != nil || res.Content != "through socks proxy" {
				t.Fatalf("%+v", res)
			}
			if got := valueAfter(f.snapshot()[0].Args, "-x"); got != proxy {
				t.Fatalf("-x %q, want %q", got, proxy)
			}
		})
	}
}

func strp(s string) *string { return &s }

func TestProxyNormalizationMatchesTheURLClass(t *testing.T) {
	// utils.ts normalizeProxyUrl returns URL.toString(): an http(s) proxy gains the root path,
	// a socks proxy does not; curl receives that string.
	for in, want := range map[string]string{
		"http://global-proxy.example:8080":       "http://global-proxy.example:8080/",
		"https://Proxy.Example:8443?x=1#frag":    "https://proxy.example:8443/",
		"http://proxy.example:80":                "http://proxy.example/",
		"http://user:pw@proxy.example:3128/path": "http://user:pw@proxy.example:3128/path",
		"socks5h://proxy.example:9050":           "socks5h://proxy.example:9050",
		"socks4a://proxy.example:1080?x=1#frag":  "socks4a://proxy.example:1080",
		" http://proxy.example:8080 ":            "http://proxy.example:8080/",
	} {
		got, _, err := NormalizeProxyURL(in, "proxy")
		noErr(t, err)
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
