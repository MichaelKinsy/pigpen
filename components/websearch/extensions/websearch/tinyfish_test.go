package websearch

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// Twin of test/tinyfish. The fixtures and assertions are the upstream ones; `fetch` is fakeNet.

const tinyfishShell = "<html><body><script></script><script></script><script></script><script></script>Loading</body></html>"

func TestUpstream_tinyfish(t *testing.T) {
	const f = "tinyfish"

	tw(t, f, "TinyFish availability reads environment and config credentials", func(t *testing.T) {
		_, dir := isolate(t)
		if IsTinyFishAvailable() {
			t.Fatal("available without a credential")
		}
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-env-key")
		if !IsTinyFishAvailable() {
			t.Fatal("env key ignored")
		}
		unsetenv(t, "TINYFISH_API_KEY")
		writeConfig(t, dir, `{"tinyfishApiKey":"synthetic-tinyfish-config-key"}`)
		if !IsTinyFishAvailable() {
			t.Fatal("config key ignored")
		}
	})

	tw(t, f, "explicit TinyFish search maps filters, results, and full inline content", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"tinyfish"}`)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-test-key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://api.search.tinyfish.ai?"):
				return jsonReply(map[string]any{"query": "tinyfish docs", "results": []map[string]any{
					{"position": 1, "site_name": "docs.tinyfish.ai", "title": "TinyFish Docs", "snippet": "  Search   and fetch  ", "url": "https://docs.tinyfish.ai/"},
					{"position": 2, "site_name": "example.com", "title": "Second", "snippet": "Second result", "url": "https://example.com/second"},
				}, "total_results": 2, "page": 0})
			case c.URL == "https://api.fetch.tinyfish.ai":
				return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://docs.tinyfish.ai/", "final_url": "https://docs.tinyfish.ai/", "title": "TinyFish Docs", "text": "# Full TinyFish documentation", "format": "markdown"}}, "errors": []any{}})
			}
			t.Fatalf("Unexpected fetch %s", c.URL)
			return netReply{}
		})
		res, err := Search(bg(), "tinyfish docs", FullSearchOptions{Provider: Auto, SearchOptions: SearchOptions{IncludeContent: true, NumResults: nf(1), RecencyFilter: "week", DomainFilter: []string{"docs.tinyfish.ai", "-example.com"}}})
		noErr(t, err)
		if len(fn.calls) != 2 {
			t.Fatalf("%d calls", len(fn.calls))
		}
		u, _ := url.Parse(fn.calls[0].URL)
		q := u.Query()
		if u.Scheme+"://"+u.Host+u.Path != "https://api.search.tinyfish.ai" || q.Get("query") != "tinyfish docs" || q.Get("include_domains") != "docs.tinyfish.ai" ||
			q.Get("exclude_domains") != "example.com" || q.Get("recency_minutes") != "10080" || fn.calls[0].Header.Get("X-API-Key") != "synthetic-tinyfish-test-key" {
			t.Fatalf("%v", fn.calls[0])
		}
		if !reflect.DeepEqual(jsonBody(t, fn.calls[1]), map[string]any{"urls": []any{"https://docs.tinyfish.ai/"}, "format": "markdown", "per_url_timeout_ms": 110000.0}) {
			t.Fatalf("%v", fn.calls[1].Body)
		}
		if res.Provider != "tinyfish" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "TinyFish Docs", URL: "https://docs.tinyfish.ai/", Snippet: "Search and fetch"}}) {
			t.Fatalf("%+v", res)
		}
		if !reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://docs.tinyfish.ai/", Title: "TinyFish Docs", Content: "# Full TinyFish documentation"}}) {
			t.Fatalf("%+v", res.InlineContent)
		}
	})

	tw(t, f, "TinyFish search paginates when more than ten results are requested", func(t *testing.T) {
		isolate(t)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-test-key")
		fn := useNet(t, func(c netCall) netReply {
			page := 0
			fmt.Sscan(query(c.URL, "page"), &page)
			var rs []map[string]any
			for i := range 10 {
				rs = append(rs, map[string]any{"title": fmt.Sprintf("Result %d", page*10+i), "snippet": "Snippet", "url": fmt.Sprintf("https://example.com/%d", page*10+i)})
			}
			return jsonReply(map[string]any{"query": "many", "results": rs, "total_results": 10, "page": page})
		})
		res, err := SearchWithTinyFish(bg(), "many", SearchOptions{NumResults: nf(15)})
		noErr(t, err)
		if len(fn.calls) != 2 || query(fn.calls[1].URL, "page") != "1" || len(res.Results) != 15 || res.Results[14].URL != "https://example.com/14" {
			t.Fatalf("%v %d", fn.urls(), len(res.Results))
		}
	})

	tw(t, f, "TinyFish extraction maps successful content and reports per-URL errors", func(t *testing.T) {
		isolate(t)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-test-key")
		attempt := 0
		fn := useNet(t, func(c netCall) netReply {
			attempt++
			if attempt == 1 {
				return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://example.com/good", "final_url": "https://example.com/good", "title": "Good", "text": "# Rendered body", "format": "markdown"}}, "errors": []any{}})
			}
			return jsonReply(map[string]any{"results": []any{}, "errors": []map[string]any{{"url": "https://example.com/blocked", "error": "bot_blocked", "status": 403}}})
		})
		ms := int64(45000)
		good, err := ExtractWithTinyFish(bg(), "https://example.com/good", ExtractOptions{Prompt: "Read the article", TimeoutMs: &ms})
		noErr(t, err)
		_, blocked := ExtractWithTinyFish(bg(), "https://example.com/blocked", ExtractOptions{})
		if !reflect.DeepEqual(jsonBody(t, fn.calls[0]), map[string]any{"urls": []any{"https://example.com/good"}, "format": "markdown", "per_url_timeout_ms": 45000.0, "purpose": "Read the article"}) {
			t.Fatalf("%v", fn.calls[0].Body)
		}
		if !reflect.DeepEqual(*good, ExtractedContent{URL: "https://example.com/good", Title: "Good", Content: "# Rendered body"}) {
			t.Fatalf("%+v", good)
		}
		wantErr(t, blocked, `TinyFish Fetch failed .*bot_blocked \(HTTP 403\)`)
	})

	tw(t, f, "TinyFish API errors redact credentials", func(t *testing.T) {
		isolate(t)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-secret")
		useNet(t, func(netCall) netReply { return reply(401, "rejected synthetic-tinyfish-secret") })
		_, err := SearchWithTinyFish(bg(), "redact me", SearchOptions{})
		wantErr(t, err, `TinyFish Search API error 401`)
		if strings.Contains(err.Error(), "synthetic-tinyfish-secret") || !strings.Contains(strings.ToLower(err.Error()), "[redacted]") {
			t.Fatal(err)
		}
	})

	tw(t, f, "fetch_content uses TinyFish before Parallel after local and Jina extraction fail", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-test-key")
		t.Setenv("PARALLEL_API_KEY", "synthetic-parallel-test-key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/app":
				return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: tinyfishShell}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(503, "")
			case c.URL == "https://api.fetch.tinyfish.ai":
				return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://example.com/app", "final_url": "https://example.com/app", "title": "Rendered", "text": "# TinyFish rendered content", "format": "markdown"}}, "errors": []any{}})
			case c.URL == "https://api.parallel.ai/v1/extract":
				t.Fatal("Parallel must not run")
			}
			t.Fatalf("Unexpected fetch %s", c.URL)
			return netReply{}
		})
		r := ExtractContent(bg(), "https://example.com/app", pageOpts())
		wantCalls(t, fn, "https://example.com/app", "https://r.jina.ai/https://example.com/app", "https://api.fetch.tinyfish.ai")
		if r.Error != nil || r.Title != "Rendered" || r.Content != "# TinyFish rendered content" {
			t.Fatalf("%+v", r)
		}
	})

	tw(t, f, "configured searchRouting can select TinyFish", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["tinyfish"],"fallbackOn":["network"]}}`)
		t.Setenv("TINYFISH_API_KEY", "synthetic-tinyfish-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"query": "route", "results": []map[string]any{{"title": "Routed", "snippet": "TinyFish route", "url": "https://example.com/routed"}}, "total_results": 1, "page": 0})
		})
		res, err := Search(bg(), "route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if res.Provider != "tinyfish" || res.Results[0].Title != "Routed" {
			t.Fatalf("%+v", res)
		}
	})
}
