package websearch

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/kimi-provider.test.mjs. registryContext is the upstream helper of the same name.

const kimiURL = "https://api.kimi.com/coding/v1/search"

func registryContext(models []map[string]any, auth func(map[string]any) (map[string]any, error)) *fakeExt {
	if models == nil {
		models = []map[string]any{model("kimi-coding", "kimi-for-coding")}
	}
	if auth == nil {
		auth = staticAuth(authOK("kimi-oauth-token", map[string]any{}))
	}
	return &fakeExt{reg: &fakeRegistry{models: models, auth: auth}}
}

func kimiBody(results ...map[string]any) string {
	items := make([]any, len(results))
	for i, r := range results {
		items[i] = r
	}
	return string(mustJSON(map[string]any{"search_results": items}))
}

func TestUpstream_kimi_provider(t *testing.T) {
	const f = "kimi-provider"

	tw(t, f, "Kimi resolves primary model-registry auth and sends the verified search request", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{
			model("kimi-code", "compat-model"), model("other", "unrelated"), model("kimi-coding", "primary-model"),
		}, staticAuth(authOK("kimi-registry-token", map[string]any{
			"X-Registry": "forwarded", "X-Nullable": nil, "authorization": "Bearer stale-token",
			"content-type": "text/plain", "x-msh-tool-call-id": "stale-call-id",
		})))
		fn := useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(
				map[string]any{"title": "Kimi result", "url": "https://example.com/one", "snippet": "First snippet", "site_name": "Example"},
				map[string]any{"title": "   ", "url": "https://example.com/two"},
				map[string]any{"title": "Missing URL", "snippet": "must be ignored"},
			))
		})
		ctx := withExt(ec)
		if !IsKimiSearchAvailable(ec) {
			t.Fatal("unavailable")
		}
		result, err := SearchWithKimi(ctx, "Kimi Code Plan search", SearchOptions{})
		noErr(t, err)

		if !eqStrings(ec.reg.selected, []string{"kimi-coding/primary-model", "kimi-coding/primary-model"}) {
			t.Fatal(ec.reg.selected)
		}
		c := fn.calls[0]
		if c.URL != kimiURL || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer kimi-registry-token" ||
			c.Header.Get("Content-Type") != "application/json" || c.Header.Get("X-Registry") != "forwarded" {
			t.Fatalf("%s %s %v", c.URL, c.Method, c.Header)
		}
		if _, has := c.Header["X-Nullable"]; has {
			t.Fatal("a null header was sent")
		}
		id := c.Header.Get("X-Msh-Tool-Call-Id")
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) || id == "stale-call-id" {
			t.Fatalf("tool call id %q", id)
		}
		if c.Body != `{"text_query":"Kimi Code Plan search"}` {
			t.Fatal(c.Body)
		}
		want := &SearchResponse{
			Answer: "First snippet\nSource: Kimi result (https://example.com/one)\n\nSource: https://example.com/two (https://example.com/two)",
			Results: []SearchResult{
				{Title: "Kimi result", URL: "https://example.com/one", Snippet: "First snippet"},
				{Title: "https://example.com/two", URL: "https://example.com/two", Snippet: ""},
			},
		}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("%+v", result)
		}
	})

	tw(t, f, "Kimi accepts Pi OAuth returned only in the Authorization header", func(t *testing.T) {
		isolate(t)
		ec := registryContext(nil, staticAuth(map[string]any{"ok": true, "headers": map[string]any{"Authorization": "Bearer kimi-header-token"}}))
		fn := useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(map[string]any{"title": "Header auth", "url": "https://example.com/header", "snippet": "ok"}))
		})
		result, err := SearchWithKimi(withExt(ec), "header auth", SearchOptions{})
		noErr(t, err)
		if fn.calls[0].Header.Get("Authorization") != "Bearer kimi-header-token" || result.Results[0].URL != "https://example.com/header" {
			t.Fatalf("%v %+v", fn.calls[0].Header, result)
		}
	})

	tw(t, f, "Kimi accepts the kimi-code registry provider as a compatibility fallback", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{model("kimi-code", "compat-only")}, nil)
		if !IsKimiSearchAvailable(ec) || !eqStrings(ec.reg.selected, []string{"kimi-code/compat-only"}) {
			t.Fatal(ec.reg.selected)
		}
	})

	tw(t, f, "Kimi without model-registry auth fails before making a request", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{model("kimi-coding", "signed-out")}, staticAuth(map[string]any{"ok": false, "error": "not authenticated"}))
		n := noNet(t)
		if IsKimiSearchAvailable(ec) {
			t.Fatal("available")
		}
		_, err := SearchWithKimi(withExt(ec), "signed out", SearchOptions{})
		wantErr(t, err, `(?i)error.*/login kimi-coding|/login kimi-coding`)
		if n.count() != 0 {
			t.Fatal("reached the network")
		}
	})

	tw(t, f, "Kimi preserves model-registry credential failures", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{model("kimi-coding", "broken-refresh")}, func(map[string]any) (map[string]any, error) {
			return nil, errors.New("credential refresh failed")
		})
		n := noNet(t)
		_, err := SearchWithKimi(withExt(ec), "broken auth", SearchOptions{})
		wantErr(t, err, `credential refresh failed`)
		if n.count() != 0 {
			t.Fatal("reached the network")
		}
	})

	tw(t, f, "Kimi tries later registry models after an earlier credential failure", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{model("kimi-coding", "broken-refresh"), model("kimi-coding", "usable-refresh")}, func(m map[string]any) (map[string]any, error) {
			if m["id"] == "broken-refresh" {
				return nil, errors.New("credential refresh failed")
			}
			return authOK("kimi-later-token", map[string]any{}), nil
		})
		fn := useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(map[string]any{"title": "Later auth", "url": "https://example.com/later", "snippet": "ok"}))
		})
		result, err := SearchWithKimi(withExt(ec), "later auth", SearchOptions{})
		noErr(t, err)
		if !eqStrings(ec.reg.selected, []string{"kimi-coding/broken-refresh", "kimi-coding/usable-refresh"}) ||
			fn.calls[0].Header.Get("Authorization") != "Bearer kimi-later-token" || result.Results[0].URL != "https://example.com/later" {
			t.Fatalf("%v %v %+v", ec.reg.selected, fn.calls[0].Header, result)
		}
	})

	tw(t, f, "Kimi availability treats registry credential failures as unavailable", func(t *testing.T) {
		isolate(t)
		ec := registryContext([]map[string]any{model("kimi-coding", "broken-refresh")}, func(map[string]any) (map[string]any, error) {
			return nil, errors.New("credential refresh failed")
		})
		if IsKimiSearchAvailable(ec) {
			t.Fatal("available")
		}
	})

	tw(t, f, "Kimi API errors redact the model-registry credential", func(t *testing.T) {
		isolate(t)
		const secret = "kimi-oauth-secret"
		ec := registryContext(nil, staticAuth(authOK(secret, map[string]any{})))
		useNet(t, func(netCall) netReply { return reply(401, "denied for "+secret) })
		_, err := SearchWithKimi(withExt(ec), "redact", SearchOptions{})
		if err == nil || !strings.Contains(err.Error(), "Kimi Code search API error 401") || !strings.Contains(err.Error(), "[redacted]") || strings.Contains(err.Error(), secret) {
			t.Fatalf("%v", err)
		}
	})

	tw(t, f, "Kimi applies normalized domain filters and numResults locally", func(t *testing.T) {
		isolate(t)
		ec := registryContext(nil, nil)
		fn := useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(
				map[string]any{"title": "First", "url": "https://allowed.example/first", "snippet": "one"},
				map[string]any{"title": "Blocked", "url": "https://ads.allowed.example/tracker", "snippet": "blocked"},
				map[string]any{"title": "Second", "url": "https://docs.allowed.example/second", "snippet": "two"},
				map[string]any{"title": "Other", "url": "https://other.example/no", "snippet": "other"},
				map[string]any{"title": "Third", "url": "https://allowed.example/third", "snippet": "three"},
			))
		})
		resp, err := SearchWithKimi(withExt(ec), "local filtering", SearchOptions{
			NumResults:   nf(2),
			DomainFilter: []string{" HTTPS://Allowed.Example/scope ", "-https://ADS.allowed.example/path", "not a domain"},
		})
		noErr(t, err)
		if fn.calls[0].Body != `{"text_query":"local filtering"}` {
			t.Fatal(fn.calls[0].Body)
		}
		if !eqStrings(urlsOf(resp.Results), []string{"https://allowed.example/first", "https://docs.allowed.example/second"}) ||
			resp.Results[0].Title != "First" || resp.Results[1].Title != "Second" {
			t.Fatalf("%+v", resp.Results)
		}
		if resp.Answer != "one\nSource: First (https://allowed.example/first)\n\ntwo\nSource: Second (https://docs.allowed.example/second)" {
			t.Fatal(resp.Answer)
		}
	})

	tw(t, f, "Kimi drops malformed and non-web result URLs before citation", func(t *testing.T) {
		isolate(t)
		ec := registryContext(nil, nil)
		useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(
				map[string]any{"title": "Script", "url": "javascript:alert(1)", "snippet": "bad"},
				map[string]any{"title": "FTP", "url": "ftp://example.com/file", "snippet": "bad"},
				map[string]any{"title": "Relative", "url": "/relative", "snippet": "bad"},
				map[string]any{"title": "Malformed", "url": "not a url", "snippet": "bad"},
				map[string]any{"title": "HTTPS", "url": " https://example.com/ok ", "snippet": "good"},
				map[string]any{"title": "HTTP", "url": "http://example.org/path", "snippet": "also good"},
			))
		})
		resp, err := SearchWithKimi(withExt(ec), "safe urls", SearchOptions{})
		noErr(t, err)
		want := []SearchResult{
			{Title: "HTTPS", URL: "https://example.com/ok", Snippet: "good"},
			{Title: "HTTP", URL: "http://example.org/path", Snippet: "also good"},
		}
		if !reflect.DeepEqual(resp.Results, want) || regexp.MustCompile(`javascript:|ftp:|relative|not a url`).MatchString(resp.Answer) {
			t.Fatalf("%+v %q", resp.Results, resp.Answer)
		}
	})

	tw(t, f, "Kimi rejects invalid JSON and empty result envelopes", func(t *testing.T) {
		isolate(t)
		ec := registryContext(nil, nil)
		useNet(t, func(netCall) netReply { return reply(200, "{") })
		_, err := SearchWithKimi(withExt(ec), "invalid", SearchOptions{})
		wantErr(t, err, `returned invalid JSON`)
		useNet(t, func(netCall) netReply { return reply(200, `{"search_results":[]}`) })
		_, err = SearchWithKimi(withExt(ec), "empty", SearchOptions{})
		wantErr(t, err, `returned no results`)
	})

	tw(t, f, "explicit kimi routing dispatches through the Kimi Code search endpoint", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{}`)
		ec := registryContext(nil, nil)
		fn := useNet(t, func(netCall) netReply {
			return reply(200, kimiBody(map[string]any{"title": "Dispatched", "url": "https://example.com/dispatch", "snippet": "ok"}))
		})
		r, err := Search(withExt(ec), "dispatch", FullSearchOptions{Provider: ProviderSelection{Name: "kimi"}})
		noErr(t, err)
		if r.Provider != "kimi" || !eqStrings(fn.urls(), []string{kimiURL}) {
			t.Fatalf("%s %v", r.Provider, fn.urls())
		}
	})

	routing := `{"braveApiKey":"brave-routing-key","searchRouting":{"providers":["kimi","brave"],"fallbackOn":["network"]}}`

	tw(t, f, "configured routing falls back when Kimi hits its provider timeout", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, routing)
		ec := registryContext(nil, nil)
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == kimiURL:
				// The upstream fetch rejects with a DOMException named TimeoutError.
				return netReply{Err: context.DeadlineExceeded}
			case strings.HasPrefix(c.URL, "https://api.search.brave.com/res/v1/web/search?"):
				return braveOK("Brave fallback", "https://example.com/brave", "fallback ok")
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(withExt(ec), "fallback after kimi timeout", FullSearchOptions{})
		noErr(t, err)
		if r.Provider != "brave" || !reflect.DeepEqual(r.Results, []SearchResult{{Title: "Brave fallback", URL: "https://example.com/brave", Snippet: "fallback ok"}}) {
			t.Fatalf("%s %+v", r.Provider, r.Results)
		}
		urls := fn.urls()
		if urls[0] != kimiURL || !strings.HasPrefix(urls[1], "https://api.search.brave.com/res/v1/web/search?") {
			t.Fatal(urls)
		}
	})

	tw(t, f, "configured routing does not fall back when the caller cancels Kimi", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, routing)
		ec := registryContext(nil, nil)
		ctx, cancel := context.WithCancel(withExt(ec))
		defer cancel()
		fn := useNet(t, func(c netCall) netReply {
			if c.URL != kimiURL {
				return netReply{Err: errors.New("Unexpected fallback fetch " + c.URL)}
			}
			// The caller aborts while the request is in flight; the fetch rejects with an AbortError.
			cancel()
			return netReply{Err: context.Canceled}
		})
		_, err := Search(ctx, "cancel kimi", FullSearchOptions{})
		wantErr(t, err, `(?i)aborted`)
		if !eqStrings(fn.urls(), []string{kimiURL}) {
			t.Fatal(fn.urls())
		}
	})
}
