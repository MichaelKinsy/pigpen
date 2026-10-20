package websearch

import (
	"encoding/json"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Twins of auth-fetch.test.mjs. The upstream tests import the modules in a child process with a
// stubbed fetch and program shims on PATH; here fakeNet stands in for fetch and the host
// command runner is canned (cookies_fixture_test.go).

func authEnv(t *testing.T, config map[string]any) (home string, s *shims, agentDir string) {
	t.Helper()
	home, agentDir = isolate(t)
	s = installShims(t, runtime.GOOS)
	s.password(passwordCommand(runtime.GOOS), "peanuts")
	b, _ := json.Marshal(config)
	writeConfig(t, agentDir, string(b)+"\n")
	return home, s, agentDir
}

func TestAuthFetch(t *testing.T) {
	const F = "auth-fetch"

	tw(t, F, "authFetch profiles resolve named, single true, and host policy", func(t *testing.T) {
		authEnv(t, map[string]any{"authFetch": map[string]any{"social": []any{"x.com"}, "work": map[string]any{"hosts": []any{"docs.company.com"}, "cache": "off"}}})
		work, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
		noErr(t, err)
		if work.Cache != "off" {
			t.Fatalf("cache = %q", work.Cache)
		}
		_, err = ResolveAuthFetchProfile(AuthSpec{True: true})
		wantErr(t, err, `exactly one`)
		_, err = AssertAuthFetchURL(work, "http://docs.company.com/private")
		wantErr(t, err, `HTTPS`)
		_, err = AssertAuthFetchURL(work, "https://evil.example/private")
		wantErr(t, err, `not allowed`)
		_, err = AssertAuthFetchURL(work, "https://evildocs.company.com/private")
		wantErr(t, err, `not allowed`)
		u, err := AssertAuthFetchURL(work, "https://sub.docs.company.com/private")
		noErr(t, err)
		if u.Hostname() != "sub.docs.company.com" {
			t.Fatalf("host = %q", u.Hostname())
		}
	})

	tw(t, F, "authFetch true selects the only profile", func(t *testing.T) {
		authEnv(t, map[string]any{"authFetch": map[string]any{"work": []any{"example.com"}}})
		p, err := ResolveAuthFetchProfile(AuthSpec{True: true})
		noErr(t, err)
		if p.Name != "work" || len(p.Hosts) != 1 || p.Hosts[0] != "example.com" {
			t.Fatalf("profile = %+v", p)
		}
	})

	tw(t, F, "authenticated fetch sends only browser-scoped cookies with raw values", func(t *testing.T) {
		home, _, _ := authEnv(t, map[string]any{"authFetch": map[string]any{"work": []any{"example.com"}}})
		const far = "20000000000000000"
		createAuthFixture(t, home, [][]any{
			{"parentDomain", "parent=a=b/%", ".example.com", "/", nil, far, ""},
			{"targetHost", "host=a=b/%", "app.example.com", "/", nil, far, ""},
			{"targetDomain", "domain=a=b/%", ".app.example.com", "/private", nil, far, ""},
			{"sid", "root", ".app.example.com", "/", nil, far, ""},
			{"sid", "private=a=b/%", ".app.example.com", "/private", nil, far, ""},
			{"sessionCookie", "session", ".app.example.com", "/private", nil, 0, ""},
			{"expired", "expired", ".app.example.com", "/private", nil, 1, ""},
			{"wrongPath", "settings", ".app.example.com", "/settings", nil, far, ""},
			{"parentHostOnly", "parent-host", "example.com", "/", nil, far, ""},
			{"siblingDomain", "sibling", ".other.example.com", "/", nil, far, ""},
			{"childDomain", "child", ".child.app.example.com", "/", nil, far, ""},
			{"partitioned", "partitioned", ".app.example.com", "/private", nil, far, "https://other.example"},
		})
		f := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "private body", Header: http.Header{"Content-Type": {"text/plain"}}}
		})
		p, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
		noErr(t, err)
		res := ExtractContent(bg(), "https://app.example.com/private/page", ExtractOptions{Mode: "raw", AuthProfile: &p, Lookup: publicLookup})
		if len(f.calls) != 1 || f.calls[0].URL != "https://app.example.com/private/page" {
			t.Fatalf("calls = %+v", f.calls)
		}
		cookie := f.calls[0].Header.Get("Cookie")
		for _, want := range []string{`parentDomain=parent=a=b/%`, `targetHost=host=a=b/%`, `targetDomain=domain=a=b/%`, `sessionCookie=session`} {
			if !strings.Contains(cookie, want) {
				t.Errorf("cookie %q lacks %q", cookie, want)
			}
		}
		if strings.Index(cookie, "sid=private=a=b/%") > strings.Index(cookie, "sid=root") || !strings.Contains(cookie, "sid=private=a=b/%") {
			t.Errorf("the longer path does not come first in %q", cookie)
		}
		for _, bad := range []string{"expired=expired", "wrongPath=settings", "parentHostOnly=parent-host", "siblingDomain=sibling", "childDomain=child", "partitioned=partitioned"} {
			if strings.Contains(cookie, bad) {
				t.Errorf("cookie %q holds %q", cookie, bad)
			}
		}
		if res.Content != "private body" || res.Error != nil {
			t.Fatalf("result = %+v", res)
		}
	})

	tw(t, F, "authenticated same-origin redirects recompute path-scoped cookies", func(t *testing.T) {
		home, _, _ := authEnv(t, map[string]any{"authFetch": map[string]any{"work": []any{"example.com"}}})
		createAuthFixture(t, home, [][]any{
			{"start", "start", ".example.com", "/start", nil, "20000000000000000", ""},
			{"next", "next", ".example.com", "/next", nil, "20000000000000000", ""},
		})
		f := useNet(t, func(c netCall) netReply {
			if c.URL == "https://app.example.com/start/page" {
				return redirectTo(302, "/next/page")
			}
			return netReply{Status: 200, Body: "redirected body", Header: http.Header{"Content-Type": {"text/plain"}}}
		})
		p, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
		noErr(t, err)
		res := ExtractContent(bg(), "https://app.example.com/start/page", ExtractOptions{Mode: "raw", AuthProfile: &p, Lookup: publicLookup})
		if len(f.calls) != 2 || f.calls[1].URL != "https://app.example.com/next/page" {
			t.Fatalf("calls = %+v", f.calls)
		}
		first, second := f.calls[0].Header.Get("Cookie"), f.calls[1].Header.Get("Cookie")
		if !strings.Contains(first, "start=start") || strings.Contains(first, "next=next") {
			t.Errorf("first cookie = %q", first)
		}
		if !strings.Contains(second, "next=next") || strings.Contains(second, "start=start") {
			t.Errorf("second cookie = %q", second)
		}
		if res.Content != "redirected body" {
			t.Fatalf("result = %+v", res)
		}
	})

	tw(t, F, "authenticated fetch failure does not fall through to hosted providers", func(t *testing.T) {
		authEnv(t, map[string]any{"authFetch": map[string]any{"work": []any{"example.com"}}, "fetchRouting": map[string]any{"providers": []any{"http", "jina"}, "allowRemoteHostedProviders": true}})
		f := useNet(t, func(netCall) netReply { return reply(200, "hosted") })
		p, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
		noErr(t, err)
		res := ExtractContent(bg(), "https://example.com/private", ExtractOptions{AuthProfile: &p, Lookup: publicLookup})
		if len(f.calls) != 0 {
			t.Fatalf("calls = %+v", f.calls)
		}
		if res.Error == nil || !regexp.MustCompile(`could not read browser cookies|No detected Chromium profile`).MatchString(*res.Error) {
			t.Fatalf("result = %+v", res)
		}
	})

	tw(t, F, "authenticated Cloudflare challenge stays direct-only", func(t *testing.T) {
		home, _, _ := authEnv(t, map[string]any{"authFetch": map[string]any{"work": []any{"example.com"}}, "fetchRouting": map[string]any{"providers": []any{"http", "jina"}, "allowRemoteHostedProviders": true}})
		createAuthFixture(t, home, [][]any{{"sid", "session", ".example.com", "/", nil, "20000000000000000", ""}})
		f := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Body: "<html><head><title>Just a moment...</title></head><body></body></html>", Header: http.Header{"Content-Type": {"text/html"}, "Cf-Mitigated": {"challenge"}}}
		})
		p, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
		noErr(t, err)
		res := ExtractContent(bg(), "https://app.example.com/private", ExtractOptions{AuthProfile: &p, Lookup: publicLookup})
		if urls := f.urls(); len(urls) != 1 || urls[0] != "https://app.example.com/private" {
			t.Fatalf("urls = %v", urls)
		}
		if res.Error == nil || !strings.HasPrefix(*res.Error, "HTTP 200: Blocked by Cloudflare challenge page") {
			t.Fatalf("result = %+v", res)
		}
	})
}
