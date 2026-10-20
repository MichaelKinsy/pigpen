package websearch

import (
	"context"
	"strings"
	"testing"
)

// Twins of proxy-transport.test.mjs. The original tunnels proxied requests through a `curl`
// child process and so does this port, through the host's command runner: the curl cases run
// against fakeCurl (proxy_curl_test.go), the twin of the original's fake `curl` script.

func TestUpstream_proxy_transport(t *testing.T) {
	const F = "proxy-transport"
	sptr := func(s string) *string { return &s }
	tskip(t, F, "extension initialization preserves fetch identity and installs proxy transport on first proxied tool call",
		"there is no global fetch to wrap in Go: the proxy travels in the request context (WithProxy) instead")
	tskip(t, F, "proxy curl redirects strip caller headers across origins", "the original's fetch wrapper follows redirects itself (up to 20) when the caller does not ask for manual ones; every proxied request of this port is a single hop (FetchRemoteURL and FetchWithCredentialRedirects follow redirects by hand, as the original's fetchRemoteUrl does with redirect manual), so there is no follow loop to strip headers in; the curl arguments of each hop are covered by TestCurlTransportArguments")
	tskip(t, F, "proxy curl redirects keep caller headers on the same origin", "the original's fetch wrapper follows redirects itself (up to 20) when the caller does not ask for manual ones; every proxied request of this port is a single hop (FetchRemoteURL and FetchWithCredentialRedirects follow redirects by hand), so there is no follow loop to keep headers in; see the cross-origin twin")
	tw(t, F, "proxy curl keeps manual redirects as redirect responses", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/start": {Status: 302, StatusText: "Found", Location: "https://other.example/final"}})
		resp, err := guardedFetch(WithProxy(bg(), "http://proxy.example:8080/"), mustURL(t, "https://93.184.216.34/start"), RequestInit{Method: "GET"}, ValidationOptions{})
		noErr(t, err)
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://other.example/final" || len(f.snapshot()) != 1 {
			t.Fatalf("%d %v %d", resp.StatusCode, resp.Header, len(f.snapshot()))
		}
	})
	tw(t, F, "proxy transport does not spawn curl for pre-aborted requests", func(t *testing.T) {
		extractEnv(t, "")
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/abort": {Status: 200, StatusText: "OK", Body: "late"}})
		ctx, cancel := context.WithCancel(WithProxy(bg(), "http://proxy.example:8080/"))
		cancel()
		_, err := guardedFetch(ctx, mustURL(t, "https://93.184.216.34/abort"), RequestInit{Method: "GET"}, ValidationOptions{})
		wantErr(t, err, `(?i)error.*abort|abort`)
		if len(f.snapshot()) != 0 {
			t.Fatalf("curl ran: %+v", f.snapshot())
		}
	})
	tskip(t, F, "websearch command scopes searches but not model callbacks to configured proxy", "the /websearch curator command is deferred (docs/PORT.md)")

	tw(t, F, "configured proxy is scoped to web operations while empty string forces direct access", func(t *testing.T) {
		extractEnv(t, `{"proxy":"http://global-proxy.example:8080"}`)
		active := func(param *string) string {
			ctx, err := ScopeProxy(context.Background(), param)
			noErr(t, err)
			return activeProxy(ctx)
		}
		got := []string{activeProxy(context.Background()), active(nil), active(sptr("")), active(sptr("http://call-proxy.example:8080")), activeProxy(context.Background())}
		want := []string{"", "http://global-proxy.example:8080/", "", "http://call-proxy.example:8080/", ""}
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
		useCurl(t, map[string]curlRoute{})
		ctx, err := ScopeProxy(bg(), sptr("http://user:secret@proxy.example:8080"))
		noErr(t, err)
		_, err = guardedFetch(ctx, mustURL(t, "https://93.184.216.34/missing"), RequestInit{Method: "GET"}, ValidationOptions{})
		wantErr(t, err, `http://redacted:redacted@proxy\.example:8080/`)
		if strings.Contains(err.Error(), "user:secret") {
			t.Fatalf("credentials leaked: %v", err)
		}
		res := ExtractContent(ctx, "https://93.184.216.34/missing", ExtractOptions{})
		if res.Error == nil || strings.Contains(*res.Error, "secret") || !strings.Contains(*res.Error, "http://redacted:redacted@proxy.example:8080/") {
			t.Fatalf("%v", res.Error)
		}
	})
	tw(t, F, "source_check fetchContent uses the explicit proxy for result pages", func(t *testing.T) {
		adapted(t, "provider tavily instead of openai (not ported here); the result page is an IP literal so no DNS is needed")
		r, _ := newRuntime(t, `{}`)
		t.Setenv("TAVILY_API_KEY", "source-check-proxy-test-key")
		f := useCurl(t, map[string]curlRoute{
			"https://api.tavily.com/search": {Status: 200, StatusText: "OK", Body: tavilyBody("", map[string]string{"title": "API docs", "url": "https://93.184.216.34/api", "content": ""})},
			"https://93.184.216.34/api":     {Status: 200, StatusText: "OK", Body: "<html><title>API docs</title><body>The API docs are available.</body></html>", Dump: strp("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n")},
		})
		out := run(t, mustTool(t, r, "source_check"), map[string]any{"claim": "API docs", "provider": "tavily", "fetchContent": true, "proxy": "http://call-proxy.example:8080"})
		if dInt(t, out, "sourceCount") != 1 {
			t.Fatalf("%v", out.Details)
		}
		apiCall, pageCall := f.argsOf("https://api.tavily.com/search"), f.argsOf("https://93.184.216.34/api")
		if apiCall == nil || pageCall == nil || valueAfter(apiCall, "-x") != "http://call-proxy.example:8080/" || valueAfter(pageCall, "-x") != "http://call-proxy.example:8080/" {
			t.Fatalf("%+v", f.snapshot())
		}
	})
	tw(t, F, "fetch_content passes the explicit proxy through queued extraction", func(t *testing.T) {
		adapted(t, "the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete")
		r, _ := newRuntime(t, `{}`)
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": {Status: 200, StatusText: "OK",
			Body: "<html><title>Proxy page</title><body><p>Fetched through the requested proxy. " + strings.Repeat("More readable words for the page. ", 30) + "</p></body></html>",
			Dump: strp("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n")}})
		out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page", "proxy": "http://call-proxy.example:8080"})
		pageCall := f.argsOf("https://93.184.216.34/page")
		if dInt(t, out, "successful") != 1 || pageCall == nil || valueAfter(pageCall, "-x") != "http://call-proxy.example:8080/" {
			t.Fatalf("%v %+v", out.Details, f.snapshot())
		}
	})
	tw(t, F, "configured socks5h proxy is accepted and routed to curl", func(t *testing.T) {
		extractEnv(t, `{"proxy":"socks5h://proxy.example:9050"}`)
		r, _ := newRuntime(t, `{"proxy":"socks5h://proxy.example:9050"}`)
		f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": {Status: 200, StatusText: "OK", Body: "through socks proxy", Dump: strp("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n")}})
		ctx, err := ScopeProxy(bg(), nil)
		noErr(t, err)
		if activeProxy(ctx) != "socks5h://proxy.example:9050" {
			t.Fatalf("active proxy %q", activeProxy(ctx))
		}
		out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page"})
		pageCall := f.argsOf("https://93.184.216.34/page")
		if out.Text() != "through socks proxy" || pageCall == nil || valueAfter(pageCall, "-x") != "socks5h://proxy.example:9050" {
			t.Fatalf("%+v %+v", out.Text(), f.snapshot())
		}
	})
	tw(t, F, "per-call ${scheme} proxy is routed unchanged to curl", func(t *testing.T) {
		for _, scheme := range []string{"socks4", "socks4a", "socks5", "socks5h"} {
			t.Run(scheme, func(t *testing.T) {
				r, _ := newRuntime(t, `{}`)
				f := useCurl(t, map[string]curlRoute{"https://93.184.216.34/page": {Status: 200, StatusText: "OK", Body: "through socks proxy", Dump: strp("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n")}})
				proxy := scheme + "://proxy.example:9050"
				out := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": "https://93.184.216.34/page", "proxy": proxy})
				calls := f.snapshot()
				if out.Text() != "through socks proxy" || len(calls) != 1 || valueAfter(calls[0].Args, "-x") != proxy {
					t.Fatalf("%+v %+v", out.Text(), calls)
				}
			})
		}
	})
	tw(t, F, "generic socks proxy is rejected before transport runs", func(t *testing.T) {
		r, _ := newRuntime(t, `{}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, "x") })
		f := useCurl(t, nil)
		out, err := mustTool(t, r, "fetch_content").Execute(bg(), map[string]any{"url": "https://93.184.216.34/page", "proxy": "socks://proxy.example:9050"}, nil)
		if err == nil {
			matchRE(t, `proxy.*must use the http://, https://, or socks scheme`, out.Text())
		} else {
			wantErr(t, err, `proxy.*must use the http://, https://, or socks scheme`)
		}
		if len(fn.calls) != 0 || len(f.snapshot()) != 0 {
			t.Fatalf("transport ran: %+v %+v", fn.calls, f.snapshot())
		}
	})
}
