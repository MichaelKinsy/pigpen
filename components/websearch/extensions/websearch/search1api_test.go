package websearch

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Twin of test/search1api.

func TestUpstream_search1api(t *testing.T) {
	const f = "search1api"

	tw(t, f, "Search1API availability reads environment and config credentials", func(t *testing.T) {
		_, dir := isolate(t)
		if IsSearch1APIAvailable() {
			t.Fatal("available without a credential")
		}
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-env-key")
		if !IsSearch1APIAvailable() {
			t.Fatal("env key ignored")
		}
		unsetenv(t, "SEARCH1API_KEY")
		writeConfig(t, dir, `{"search1apiApiKey":"synthetic-search1api-config-key"}`)
		if !IsSearch1APIAvailable() {
			t.Fatal("config key ignored")
		}
	})

	tw(t, f, "Search1API resolves command credentials only when a request starts", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell credential command")
		}
		_, dir := isolate(t)
		marker := filepath.Join(t.TempDir(), "ran")
		writeConfig(t, dir, fmt.Sprintf(`{"search1apiApiKey":%q}`, "!touch "+marker+" && printf synthetic-search1api-command-key"))
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"searchParameters":{},"results":[]}`) })
		availableBefore := IsSearch1APIAvailable()
		_, ranBeforeErr := os.Stat(marker)
		_, err := SearchWithSearch1API(bg(), "credential timing", SearchOptions{})
		noErr(t, err)
		_, ranAfterErr := os.Stat(marker)
		if !availableBefore || !os.IsNotExist(ranBeforeErr) || ranAfterErr != nil || fn.calls[0].Header.Get("Authorization") != "Bearer synthetic-search1api-command-key" {
			t.Fatalf("%v %v %v", availableBefore, ranBeforeErr, ranAfterErr)
		}
	})

	tw(t, f, "explicit Search1API search maps filters, deep content, and results", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"search1api"}`)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-test-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"searchParameters": map[string]any{"query": "search1api docs", "max_results": 1}, "results": []map[string]any{{
				"title": "Search1API Docs", "link": "https://www.search1api.com/docs", "snippet": "  Search,   crawl, and extract. ", "content": "# Full Search1API documentation"}}})
		})
		res, err := Search(bg(), "search1api docs", FullSearchOptions{Provider: ProviderSelection{Name: "search1api"}, SearchOptions: SearchOptions{IncludeContent: true, NumResults: nf(1), RecencyFilter: "week",
			DomainFilter: []string{"https://www.search1api.com/docs", "-example.com"}}})
		noErr(t, err)
		c := fn.calls[0]
		want := map[string]any{"query": "search1api docs", "max_results": 1.0, "crawl_results": 1.0, "include_sites": []any{"www.search1api.com"}, "exclude_sites": []any{"example.com"}, "time_range": "week"}
		if len(fn.calls) != 1 || c.URL != "https://api.search1api.com/search" || c.Header.Get("Authorization") != "Bearer synthetic-search1api-test-key" ||
			c.Header.Get("Content-Type") != "application/json" || !reflect.DeepEqual(jsonBody(t, c), want) {
			t.Fatalf("%v", c)
		}
		if res.Provider != "search1api" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Search1API Docs", URL: "https://www.search1api.com/docs", Snippet: "Search, crawl, and extract."}}) ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://www.search1api.com/docs", Title: "Search1API Docs", Content: "# Full Search1API documentation"}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Search1API search does not request paid page crawling unless includeContent is true", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-test-key")
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"searchParameters":{},"results":[]}`) })
		_, err := SearchWithSearch1API(bg(), "basic search", SearchOptions{NumResults: nf(20)})
		noErr(t, err)
		if !reflect.DeepEqual(jsonBody(t, fn.calls[0]), map[string]any{"query": "basic search", "max_results": 20.0, "crawl_results": 0.0}) {
			t.Fatal(fn.calls[0].Body)
		}
	})

	tw(t, f, "Search1API Crawl maps extracted content", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-test-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"crawlParameters": map[string]any{"url": "https://example.com/article"}, "results": map[string]any{
				"title": "Example article", "link": "https://example.com/article", "content": "# Article body", "metadata": map[string]any{"language": "en"}}})
		})
		ms := int64(45000)
		res, err := ExtractWithSearch1API(bg(), "https://example.com/article", ExtractOptions{TimeoutMs: &ms})
		noErr(t, err)
		c := fn.calls[0]
		if len(fn.calls) != 1 || c.URL != "https://api.search1api.com/crawl" || c.Header.Get("Authorization") != "Bearer synthetic-search1api-test-key" ||
			!reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/article"}) {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/article", Title: "Example article", Content: "# Article body"}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Search1API errors redact credentials", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-secret")
		useNet(t, func(netCall) netReply { return reply(401, "rejected synthetic-search1api-secret") })
		_, err := SearchWithSearch1API(bg(), "redact me", SearchOptions{})
		wantErr(t, err, `Search1API Search API error 401`)
		if strings.Contains(err.Error(), "synthetic-search1api-secret") || !strings.Contains(strings.ToLower(err.Error()), "[redacted]") {
			t.Fatal(err)
		}
	})

	tw(t, f, "fetch_content uses Search1API after local and Jina extraction fail", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-test-key")
		t.Setenv("PARALLEL_API_KEY", "synthetic-parallel-test-key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/app":
				return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: tinyfishShell}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(503, "")
			case c.URL == "https://api.search1api.com/crawl":
				return jsonReply(map[string]any{"crawlParameters": map[string]any{"url": "https://example.com/app"}, "results": map[string]any{"title": "Rendered", "link": "https://example.com/app", "content": "# Search1API rendered content"}})
			}
			t.Fatalf("Unexpected fetch %s %s", c.URL, c.Method)
			return netReply{}
		})
		r := ExtractContent(bg(), "https://example.com/app", pageOpts())
		wantCalls(t, fn, "https://example.com/app", "https://r.jina.ai/https://example.com/app", "https://api.search1api.com/crawl")
		if r.Error != nil || r.Title != "Rendered" || r.Content != "# Search1API rendered content" {
			t.Fatalf("%+v", r)
		}
	})

	tw(t, f, "configured searchRouting can select Search1API", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["search1api"],"fallbackOn":["network"]}}`)
		t.Setenv("SEARCH1API_KEY", "synthetic-search1api-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"searchParameters": map[string]any{"query": "route"}, "results": []map[string]any{{"title": "Routed", "snippet": "Search1API route", "link": "https://example.com/routed"}}})
		})
		res, err := Search(bg(), "route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if res.Provider != "search1api" || res.Results[0].Title != "Routed" {
			t.Fatalf("%+v", res)
		}
	})
}
