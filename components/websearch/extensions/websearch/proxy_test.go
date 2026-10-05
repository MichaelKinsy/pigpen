package websearch

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Twins of proxy-transport.test.mjs. The original tunnels proxied requests through a `curl`
// child process; Go's net/http speaks the proxy protocols itself, so the curl-specific cases
// are named skips and the scoping, validation and routing cases assert the same behaviour on
// the proxy each request carries.

func TestUpstream_proxy_transport(t *testing.T) {
	const F = "proxy-transport"
	sptr := func(s string) *string { return &s }
	tskip(t, F, "extension initialization preserves fetch identity and installs proxy transport on first proxied tool call",
		"there is no global fetch to wrap in Go: the proxy travels in the request context (WithProxy) instead")
	tskip(t, F, "proxy curl redirects strip caller headers across origins", "the original spawns a curl child process for proxied requests and Go's net/http speaks the proxy protocol itself, so there is no child process to observe; cross-origin header stripping is covered by the FetchRemoteURL redirect twins")
	tskip(t, F, "proxy curl redirects keep caller headers on the same origin", "the original spawns a curl child process for proxied requests and Go's net/http speaks the proxy protocol itself, so there is no child process to observe; same-origin header handling is covered by the FetchRemoteURL redirect twins")
	tskip(t, F, "proxy curl keeps manual redirects as redirect responses", "the original spawns a curl child process for proxied requests and Go's net/http speaks the proxy protocol itself, so there is no child process to observe; the guarded fetch never follows redirects itself")
	tskip(t, F, "proxy transport does not spawn curl for pre-aborted requests", "the original spawns a curl child process for proxied requests and Go's net/http speaks the proxy protocol itself, so there is no child process to observe; pre-aborted requests are covered by the extraction cancellation twins")
	tskip(t, F, "websearch command scopes searches but not model callbacks to configured proxy", "the /websearch curator command is deferred (docs/PORT.md)")

	tw(t, F, "configured proxy is scoped to web operations while empty string forces direct access", func(t *testing.T) {
		extractEnv(t, `{"proxy":"http://global-proxy.example:8080"}`)
		active := func(param *string) string {
			ctx, err := ScopeProxy(context.Background(), param)
			noErr(t, err)
			return activeProxy(ctx)
		}
		got := []string{activeProxy(context.Background()), active(nil), active(sptr("")), active(sptr("http://call-proxy.example:8080")), activeProxy(context.Background())}
		want := []string{"", "http://global-proxy.example:8080", "", "http://call-proxy.example:8080", ""}
		if !eqStrings(got, want) {
			t.Fatalf("%q", got)
		}
	})
	tw(t, F, "omitted proxy preserves trusted environment proxy routing when no proxy is configured", func(t *testing.T) {
		extractEnv(t, "")
		unsetenv(t, "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy")
		t.Setenv("HTTPS_PROXY", "http://env-proxy.example:8080")
		ctx, err := ScopeProxy(context.Background(), nil)
		noErr(t, err)
		lookups := 0
		_, err = ValidateRemoteURL(ctx, "https://public.example.test/", ValidationOptions{TrustEnvProxy: true, Lookup: func(context.Context, string) ([]LookupAddress, error) {
			lookups++
			return []LookupAddress{{Address: "10.0.0.10", Family: 4}}, nil
		}})
		noErr(t, err)
		if lookups != 0 || hasScopedProxyDecision(ctx) {
			t.Fatalf("lookups=%d scoped=%v", lookups, hasScopedProxyDecision(ctx))
		}
	})
	tw(t, F, "invalid configured proxy fails closed instead of direct fetching", func(t *testing.T) {
		extractEnv(t, `{"proxy":"ftp://proxy.example:21"}`)
		_, err := ScopeProxy(context.Background(), nil)
		wantErr(t, err, `proxy.*must use the http://, https://, or socks scheme`)
	})
	tw(t, F, "invalid configured proxy reaches background fetch rejection handling", func(t *testing.T) {
		r, h := started(t, `{"provider":"tavily"}`)
		t.Setenv("TAVILY_API_KEY", "proxy-background-test-key")
		useNet(t, func(c netCall) netReply {
			if c.URL != "https://api.tavily.com/search" {
				t.Errorf("unexpected fetch %s", c.URL)
			}
			// The config turns invalid while the search is in flight, like the original fixture.
			writeConfig(t, ConfigDir(), `{"provider":"tavily","proxy":"ftp://proxy.example:21"}`)
			ResetCaches()
			return reply(200, tavilyBody("Search answer", map[string]string{"title": "Source", "url": "https://93.184.216.34/source", "content": "c"}))
		})
		out := run(t, mustTool(t, r, "web_search"), map[string]any{"query": "proxy cleanup", "provider": "tavily", "workflow": "none", "includeContent": true})
		matchRE(t, `Content fetching in background`, out.Text())
		r.WaitBackground()
		h.mu.Lock()
		defer h.mu.Unlock()
		var errs []string
		for _, m := range h.messages {
			if m.Type == "web-search-error" {
				errs = append(errs, m.Content)
			}
		}
		if len(errs) != 1 {
			t.Fatalf("%v", h.messages)
		}
		matchRE(t, `proxy.*must use the http://, https://, or socks scheme`, errs[0])
	})
	tw(t, F, "proxy transport errors redact proxy credentials", func(t *testing.T) {
		extractEnv(t, "")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ctx, err := ScopeProxy(ctx, sptr("http://user:secret@127.0.0.1:1"))
		noErr(t, err)
		// The real transport, aimed at a closed local port: the failure must not echo the credentials.
		res := ExtractContent(ctx, "https://93.184.216.34/missing", ExtractOptions{})
		if res.Error == nil {
			t.Fatal("expected a proxy failure")
		}
		if strings.Contains(*res.Error, "user:secret") || strings.Contains(*res.Error, "secret") {
			t.Fatalf("credentials leaked: %s", *res.Error)
		}
		if got := RedactProxyURL("http://user:secret@proxy.example:8080/"); got != "http://redacted:redacted@proxy.example:8080/" {
			t.Fatal(got)
		}
	})
	tw(t, F, "source_check fetchContent uses the explicit proxy for result pages", func(t *testing.T) {
		adapted(t, "provider tavily instead of openai (not ported); recorded proxy of each request instead of curl arguments")
		r, _ := newRuntime(t, `{}`)
		t.Setenv("TAVILY_API_KEY", "source-check-proxy-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.tavily.com/search" {
				return reply(200, tavilyBody("", map[string]string{"title": "API docs", "url": "https://93.184.216.34/api", "content": ""}))
			}
			return netReply{Status: 200, Body: "<html><title>API docs</title><body>The API docs are available.</body></html>", Header: hdr("content-type", "text/html")}
		})
		out := run(t, mustTool(t, r, "source_check"), map[string]any{"claim": "API docs", "provider": "tavily", "fetchContent": true, "proxy": "http://call-proxy.example:8080"})
		if dInt(t, out, "sourceCount") != 1 {
			t.Fatalf("%v", out.Details)
		}
		seen := map[string]string{}
		for _, c := range fn.calls {
			seen[c.URL] = c.Proxy
		}
		if seen["https://api.tavily.com/search"] != "http://call-proxy.example:8080" || seen["https://93.184.216.34/api"] != "http://call-proxy.example:8080" {
			t.Fatalf("%v", seen)
		}
	})
	tw(t, F, "fetch_content passes the explicit proxy through queued extraction", func(t *testing.T) {
		adapted(t, "the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete")
		r, _ := newRuntime(t, `{}`)
		fn := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "<html><title>Proxy page</title><body><p>Fetched through the requested proxy. " + strings.Repeat("More readable words for the page. ", 30) + "</p></body></html>", Header: hdr("content-type", "text/html")}
		})
		out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page", "proxy": "http://call-proxy.example:8080"})
		if dInt(t, out, "successful") != 1 || len(fn.calls) != 1 || fn.calls[0].Proxy != "http://call-proxy.example:8080" {
			t.Fatalf("%v %+v", out.Details, fn.calls)
		}
	})
	tw(t, F, "configured socks5h proxy is accepted and routed to curl", func(t *testing.T) {
		adapted(t, "asserts the accepted proxy the request carries instead of curl arguments")
		extractEnv(t, `{"proxy":"socks5h://proxy.example:9050"}`)
		r, _ := newRuntime(t, `{"proxy":"socks5h://proxy.example:9050"}`)
		fn := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "through socks proxy", Header: hdr("content-type", "text/plain")}
		})
		out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page"})
		if out.Text() != "through socks proxy" || fn.calls[0].Proxy != "socks5h://proxy.example:9050" {
			t.Fatalf("%q %+v", out.Text(), fn.calls)
		}
	})
	tw(t, F, "per-call ${scheme} proxy is routed unchanged to curl", func(t *testing.T) {
		for _, scheme := range []string{"socks4", "socks4a", "socks5", "socks5h"} {
			t.Run(scheme, func(t *testing.T) {
				if scheme == "socks4" || scheme == "socks4a" {
					t.Skip("Go's net/http cannot dial " + scheme + " proxies: the URL validates like the original but the connection fails closed (TestSocks4FailsClosed)")
				}
				r, _ := newRuntime(t, `{}`)
				fn := useNet(t, func(netCall) netReply {
					return netReply{Status: 200, Body: "through socks proxy", Header: hdr("content-type", "text/plain")}
				})
				proxy := scheme + "://proxy.example:9050"
				out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page", "proxy": proxy})
				if out.Text() != "through socks proxy" || fn.calls[0].Proxy != proxy {
					t.Fatalf("%q %+v", out.Text(), fn.calls)
				}
			})
		}
	})
	tw(t, F, "generic socks proxy is rejected before transport runs", func(t *testing.T) {
		r, _ := newRuntime(t, `{}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, "x") })
		out, err := mustTool(t, r, "fetch_content").Execute(bg(), map[string]any{"url": "https://93.184.216.34/page", "proxy": "socks://proxy.example:9050"}, nil)
		if err == nil {
			matchRE(t, `proxy.*must use the http://, https://, or socks scheme`, out.Text())
		} else {
			wantErr(t, err, `proxy.*must use the http://, https://, or socks scheme`)
		}
		if len(fn.calls) != 0 {
			t.Fatalf("transport ran: %+v", fn.calls)
		}
	})
}

// Go's net/http has no SOCKS4 dialer: the request fails closed instead of going out directly.
func TestSocks4FailsClosed(t *testing.T) {
	extractEnv(t, "")
	ctx, err := ScopeProxy(bg(), func() *string { s := "socks4://127.0.0.1:1"; return &s }())
	noErr(t, err)
	res := ExtractContent(ctx, "https://93.184.216.34/page", ExtractOptions{})
	if res.Error == nil || res.Content != "" {
		t.Fatalf("%+v", res)
	}
}
