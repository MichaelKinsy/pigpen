package websearch

import (
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/parallel and test/parallel-mcp-provider.

func TestUpstream_parallel(t *testing.T) {
	const f = "parallel"

	tw(t, f, "Parallel availability reads env and config keys while rejecting placeholders", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"parallelApiKey":"your-key"}`)
		t.Setenv("PARALLEL_API_KEY", "")
		if IsParallelAvailable() {
			t.Fatal("a placeholder counts as a key")
		}
		t.Setenv("PARALLEL_API_KEY", "pk_live_parallel_test_key")
		if !IsParallelAvailable() {
			t.Fatal("env key ignored")
		}
		writeConfig(t, dir, `{"parallelApiKey":"pk_live_parallel_config_key"}`)
		t.Setenv("PARALLEL_API_KEY", "")
		if !IsParallelAvailable() {
			t.Fatal("config key ignored")
		}
	})

	tw(t, f, "explicit Parallel search routes through Parallel API and maps results", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"parallel"}`)
		t.Setenv("PARALLEL_API_KEY", "pk_live_parallel_test_key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"results": []map[string]any{{"title": "Parallel Docs", "url": "https://docs.parallel.ai/search", "excerpts": []string{"Parallel search excerpt"}}}})
		})
		res, err := Search(bg(), "parallel search docs", FullSearchOptions{Provider: ProviderSelection{Name: "parallel"}, SearchOptions: SearchOptions{IncludeContent: true, NumResults: nf(3), DomainFilter: []string{"docs.parallel.ai", "-example.com"}}})
		noErr(t, err)
		c := fn.calls[0]
		adv := jsonBody(t, c)["advanced_settings"]
		want := map[string]any{"max_results": 3.0, "source_policy": map[string]any{"include_domains": []any{"docs.parallel.ai"}, "exclude_domains": []any{"example.com"}}}
		if c.URL != "https://api.parallel.ai/v1/search" || c.Header.Get("x-api-key") != "pk_live_parallel_test_key" || !reflect.DeepEqual(adv, want) {
			t.Fatalf("%v", c)
		}
		if res.Provider != "parallel" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Parallel Docs", URL: "https://docs.parallel.ai/search", Snippet: "Parallel search excerpt"}}) ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://docs.parallel.ai/search", Title: "Parallel Docs", Content: "Parallel search excerpt"}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Parallel extract retries full content when excerpts are too short", func(t *testing.T) {
		isolate(t)
		t.Setenv("PARALLEL_API_KEY", "pk_live_parallel_test_key")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://example.com", "title": "Short", "excerpts": []string{"too short"}}}})
			}
			return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://example.com", "title": "Full", "full_content": "# Full\n" + strings.Repeat("x", 600)}}})
		})
		res, err := ExtractWithParallel(bg(), "https://example.com", ExtractOptions{})
		noErr(t, err)
		if len(fn.calls) != 2 || !reflect.DeepEqual(jsonBody(t, fn.calls[1])["advanced_settings"], map[string]any{"full_content": true}) || res.Title != "Full" || res.Error != nil || !strings.HasPrefix(res.Content, "# Full") {
			t.Fatalf("%v %+v", fn.calls, res)
		}
	})

	// test/parallel.test.mjs:165: Parallel is a hosted fetch provider, Gemini the next one after it.
	tw(t, f, "fetch_content continues to Gemini when Parallel extract fails", func(t *testing.T) {
		extractEnv(t, `{"geminiApiKey":"gemini-test-key","fetchRouting":{"allowRemoteHostedProviders":true}}`)
		t.Setenv("PARALLEL_API_KEY", "pk_live_parallel_test_key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/app":
				return netReply{Status: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: "<html><body><script></script><script></script><script></script><script></script>Loading</body></html>"}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(503, "")
			case c.URL == "https://api.parallel.ai/v1/extract":
				return reply(500, "parallel exploded")
			case strings.Contains(c.URL, "generativelanguage.googleapis.com"):
				return geminiText("# Gemini fallback\n" + strings.Repeat("Recovered content ", 20))
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r := ExtractContent(bg(), "https://example.com/app", pageOpts())
		if !sliceHas(fn.urls(), "https://api.parallel.ai/v1/extract") {
			t.Fatalf("Parallel was not called: %v", fn.urls())
		}
		gemini := false
		for _, u := range fn.urls() {
			gemini = gemini || strings.Contains(u, "generativelanguage.googleapis.com")
		}
		if !gemini || r.Error != nil || !regexp.MustCompile(`Gemini fallback`).MatchString(r.Content) {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
	})
}

func TestUpstream_parallel_mcp_provider(t *testing.T) {
	const f = "parallel-mcp-provider"
	mcp := func(result map[string]any) netReply {
		return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}

	tw(t, f, "Parallel MCP searches anonymously, maps filters, and supports explicit routing", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["parallel-mcp"],"fallbackOn":["network"]}}`)
		fn := useNet(t, func(netCall) netReply {
			return mcp(map[string]any{"structuredContent": map[string]any{"results": []map[string]any{
				{"title": "Allowed", "url": "https://docs.example.com/a", "excerpts": []string{"Allowed excerpt"}},
				{"title": "Excluded", "url": "https://private.docs.example.com/b", "excerpts": []string{"Excluded excerpt"}},
				{"title": "Outside", "url": "https://example.net/c", "excerpts": []string{"Outside excerpt"}},
			}}})
		})
		direct, err := SearchWithParallelMCP(bg(), "docs", SearchOptions{NumResults: nf(2), DomainFilter: []string{"example.com", "-private.docs.example.com"}, RecencyFilter: "week", IncludeContent: true})
		noErr(t, err)
		routed, err := Search(bg(), "route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		c := fn.calls[0]
		args := jsonBody(t, c)["params"].(map[string]any)
		q := args["arguments"].(map[string]any)["search_queries"].([]any)[0].(string)
		if c.URL != "https://search.parallel.ai/mcp" || c.Header.Get("Authorization") != "" || args["name"] != "web_search" {
			t.Fatalf("%v", c)
		}
		for _, re := range []string{`site:example\.com`, `-site:private\.docs\.example\.com`, `past week`} {
			matchRE(t, re, q)
		}
		if !reflect.DeepEqual(urlsOf(direct.Results), []string{"https://docs.example.com/a"}) || direct.InlineContent[0].Content != "Allowed excerpt" || routed.Provider != "parallel-mcp" {
			t.Fatalf("%+v %s", direct, routed.Provider)
		}
	})

	tw(t, f, "Parallel MCP maps text-block search results", func(t *testing.T) {
		isolate(t)
		useNet(t, func(netCall) netReply {
			return mcp(map[string]any{"content": []map[string]any{{"type": "text", "text": "Title: Text result\nURL: https://example.com/text\nText: Result from a text block"}}})
		})
		res, err := SearchWithParallelMCP(bg(), "text", SearchOptions{})
		noErr(t, err)
		if !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Text result", URL: "https://example.com/text", Snippet: "Result from a text block"}}) {
			t.Fatalf("%+v", res.Results)
		}
	})

	tw(t, f, "Parallel MCP sends optional Bearer auth and redacts it from errors", func(t *testing.T) {
		const secret = "parallel-mcp-secret"
		_, dir := isolate(t)
		writeConfig(t, dir, `{"parallelApiKey":"`+secret+`"}`)
		fn := useNet(t, func(netCall) netReply { return reply(401, "credential "+secret) })
		_, err := SearchWithParallelMCP(bg(), "secret", SearchOptions{})
		if fn.calls[0].Header.Get("Authorization") != "Bearer "+secret || err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("%v %v", fn.calls[0].Header, err)
		}
	})

	tw(t, f, "Parallel MCP web_fetch is available only through explicit hosted fetch routing", func(t *testing.T) {
		const page = "https://example.com/article"
		handler := func(c netCall) netReply {
			if c.URL == "https://search.parallel.ai/mcp" {
				return mcp(map[string]any{"structuredContent": map[string]any{"results": []map[string]any{{"url": page, "title": "Article", "excerpts": []string{}, "full_content": "# Full article\n\nUseful content."}}}})
			}
			return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: "<html><body></body></html>"}
		}
		extractEnv(t, `{"fetchRouting":{"providers":["parallel-mcp"],"allowRemoteHostedProviders":true}}`)
		fn := useNet(t, handler)
		r := ExtractContent(bg(), page, pageOpts())
		if !sliceHas(fn.urls(), "https://search.parallel.ai/mcp") || r.Content != "# Full article\n\nUseful content." {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
		extractEnv(t, `{"fetchRouting":{"providers":["parallel-mcp"],"allowRemoteHostedProviders":false}}`)
		fn = useNet(t, handler)
		r = ExtractContent(bg(), page, pageOpts())
		if sliceHas(fn.urls(), "https://search.parallel.ai/mcp") || !regexp.MustCompile(`Remote hosted fetch providers are disabled`).MatchString(errOf(r)) {
			t.Fatalf("%v %v", fn.urls(), errOf(r))
		}
	})

	tw(t, f, "Parallel MCP stays outside provider all and the default fetch route", func(t *testing.T) {
		isolate(t)
		fn := useNet(t, func(netCall) netReply { return reply(500, "failure") })
		_, _ = Search(bg(), "all", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		ExtractContent(bg(), "https://example.com/article", pageOpts())
		if sliceHas(fn.urls(), "https://search.parallel.ai/mcp") {
			t.Fatal(fn.urls())
		}
	})
}
