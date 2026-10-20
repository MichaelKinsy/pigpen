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

// Twin of test/querit-provider.

func queritOK(extra map[string]any) map[string]any {
	m := map[string]any{"error_code": 200, "error_msg": ""}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestUpstream_querit_provider(t *testing.T) {
	const f = "querit-provider"

	tw(t, f, "Querit availability reads environment and config credentials", func(t *testing.T) {
		_, dir := isolate(t)
		if IsQueritAvailable() {
			t.Fatal("available without a credential")
		}
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-env-key")
		if !IsQueritAvailable() {
			t.Fatal("env key ignored")
		}
		unsetenv(t, "QUERIT_API_KEY")
		writeConfig(t, dir, `{"queritApiKey":"synthetic-querit-config-key"}`)
		if !IsQueritAvailable() {
			t.Fatal("config key ignored")
		}
	})

	tw(t, f, "Querit resolves command credentials only when a request starts", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell credential command")
		}
		_, dir := isolate(t)
		marker := filepath.Join(t.TempDir(), "ran")
		writeConfig(t, dir, fmt.Sprintf(`{"queritApiKey":%q}`, "!touch "+marker+" && printf synthetic-querit-command-key"))
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(queritOK(map[string]any{"search_id": 1, "results": map[string]any{"result": []any{}}}))
		})
		availableBefore := IsQueritAvailable()
		_, ranBeforeErr := os.Stat(marker)
		_, err := SearchWithQuerit(bg(), "credential timing", SearchOptions{})
		noErr(t, err)
		_, ranAfterErr := os.Stat(marker)
		if !availableBefore || !os.IsNotExist(ranBeforeErr) || ranAfterErr != nil || fn.calls[0].Header.Get("Authorization") != "Bearer synthetic-querit-command-key" {
			t.Fatalf("%v %v %v", availableBefore, ranBeforeErr, ranAfterErr)
		}
	})

	tw(t, f, "Querit search maps SDK filters and retrieves inline contents", func(t *testing.T) {
		isolate(t)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasSuffix(c.URL, "/v1/search") {
				return jsonReply(queritOK(map[string]any{"search_id": 101, "results": map[string]any{"result": []map[string]any{{"title": "Querit docs", "url": "https://www.querit.ai/en/docs", "snippet": "  Real-time   search for LLMs. ", "page_age": "1 day ago"}}}}))
			}
			return jsonReply(queritOK(map[string]any{"search_id": 102, "results": []map[string]any{{"id": "fetch-1", "url": "https://www.querit.ai/en/docs", "content": "# Full Querit documentation",
				"extrasMeta": map[string]any{"title": "Querit Documentation", "url": "https://www.querit.ai/en/docs"}}}, "statuses": []map[string]any{{"id": "fetch-1", "status": "success"}}, "searchTime": 1}))
		})
		res, err := SearchWithQuerit(bg(), "querit docs", SearchOptions{IncludeContent: true, NumResults: nf(1), RecencyFilter: "week", DomainFilter: []string{"https://www.querit.ai/docs", "-example.com"}})
		noErr(t, err)
		if len(fn.calls) != 2 {
			t.Fatalf("%d calls", len(fn.calls))
		}
		for _, c := range fn.calls {
			if c.Header.Get("Authorization") != "Bearer synthetic-querit-test-key" || c.Header.Get("Accept") != "application/json" || c.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("%v", c.Header)
			}
		}
		wantSearch := map[string]any{"query": "querit docs", "count": 1.0, "filters": map[string]any{"sites": map[string]any{"include": []any{"www.querit.ai"}, "exclude": []any{"example.com"}}, "timeRange": map[string]any{"date": "w1"}}}
		wantContents := map[string]any{"urls": []any{"https://www.querit.ai/en/docs"}, "format": "markdown", "crawlTimeout": 10.0, "extrasMeta": true}
		if fn.calls[0].URL != "https://api.querit.ai/v1/search" || fn.calls[1].URL != "https://api.querit.ai/v1/contents" ||
			!reflect.DeepEqual(jsonBody(t, fn.calls[0]), wantSearch) || !reflect.DeepEqual(jsonBody(t, fn.calls[1]), wantContents) {
			t.Fatalf("%v", fn.calls)
		}
		if !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Querit docs", URL: "https://www.querit.ai/en/docs", Snippet: "Real-time search for LLMs."}}) ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://www.querit.ai/en/docs", Title: "Querit Documentation", Content: "# Full Querit documentation"}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Querit contents requests are batched at ten URLs", func(t *testing.T) {
		isolate(t)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		var batches []int
		useNet(t, func(c netCall) netReply {
			if strings.HasSuffix(c.URL, "/v1/search") {
				var rs []map[string]any
				for i := range 11 {
					rs = append(rs, map[string]any{"title": fmt.Sprintf("Result %d", i), "url": fmt.Sprintf("https://example.com/%d", i), "snippet": fmt.Sprintf("Snippet %d", i)})
				}
				return jsonReply(queritOK(map[string]any{"search_id": 1, "results": map[string]any{"result": rs}}))
			}
			urls := jsonBody(t, c)["urls"].([]any)
			batches = append(batches, len(urls))
			var rs, st []map[string]any
			for i, u := range urls {
				rs = append(rs, map[string]any{"id": fmt.Sprintf("id-%d", i), "url": u, "content": "Content " + u.(string)})
				st = append(st, map[string]any{"id": fmt.Sprintf("id-%d", i), "status": "success"})
			}
			return jsonReply(queritOK(map[string]any{"search_id": 2, "results": rs, "statuses": st, "searchTime": 1}))
		})
		res, err := SearchWithQuerit(bg(), "batch", SearchOptions{IncludeContent: true, NumResults: nf(11)})
		noErr(t, err)
		if !reflect.DeepEqual(batches, []int{10, 1}) || len(res.InlineContent) != 11 {
			t.Fatalf("%v %d", batches, len(res.InlineContent))
		}
	})

	tw(t, f, "Querit extraction maps contents metadata and timeout", func(t *testing.T) {
		isolate(t)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(queritOK(map[string]any{"search_id": 201, "results": []map[string]any{{"id": "fetch-article", "url": "https://example.com/article", "content": "# Article body", "extrasMeta": map[string]any{"title": "Example article"}}},
				"statuses": []map[string]any{{"id": "fetch-article", "status": "success"}}, "searchTime": 1}))
		})
		ms := int64(45000)
		res, err := ExtractWithQuerit(bg(), "https://example.com/article", ExtractOptions{TimeoutMs: &ms})
		noErr(t, err)
		if fn.calls[0].URL != "https://api.querit.ai/v1/contents" ||
			!reflect.DeepEqual(jsonBody(t, fn.calls[0]), map[string]any{"urls": []any{"https://example.com/article"}, "format": "markdown", "crawlTimeout": 45.0, "extrasMeta": true}) {
			t.Fatalf("%v", fn.calls[0])
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/article", Title: "Example article", Content: "# Article body"}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Querit errors redact credentials and surface API-level failures", func(t *testing.T) {
		isolate(t)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-secret")
		useNet(t, func(netCall) netReply { return reply(401, "rejected synthetic-querit-secret") })
		_, err := SearchWithQuerit(bg(), "redact me", SearchOptions{})
		wantErr(t, err, `Querit Search API error 401`)
		if strings.Contains(err.Error(), "synthetic-querit-secret") || !strings.Contains(strings.ToLower(err.Error()), "[redacted]") {
			t.Fatal(err)
		}
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"error_code": 429, "error_msg": "quota exceeded", "search_id": 301})
		})
		_, err = SearchWithQuerit(bg(), "quota", SearchOptions{})
		if err == nil || err.Error() != "Querit Search API returned error 429: quota exceeded" {
			t.Fatal(err)
		}
	})

	tw(t, f, "configured search routing can select Querit", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["querit"],"fallbackOn":["network"]}}`)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(queritOK(map[string]any{"search_id": 401, "results": map[string]any{"result": []map[string]any{{"title": "Routed", "url": "https://example.com/routed", "snippet": "Querit route"}}}}))
		})
		res, err := Search(bg(), "route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if res.Provider != "querit" || res.Results[0].Title != "Routed" {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "fetch_content uses Querit after local and earlier hosted extraction fail", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/app":
				return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: tinyfishShell}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(503, "")
			case c.URL == "https://api.querit.ai/v1/contents":
				return jsonReply(queritOK(map[string]any{"search_id": 402, "results": []map[string]any{{"id": "fetch-app", "url": "https://example.com/app", "content": "# Querit rendered content", "extrasMeta": map[string]any{"title": "Rendered"}}},
					"statuses": []map[string]any{{"id": "fetch-app", "status": "success"}}, "searchTime": 1}))
			}
			t.Fatalf("Unexpected fetch %s %s", c.URL, c.Method)
			return netReply{}
		})
		r := ExtractContent(bg(), "https://example.com/app", pageOpts())
		wantCalls(t, fn, "https://example.com/app", "https://r.jina.ai/https://example.com/app", "https://api.querit.ai/v1/contents")
		if r.Error != nil || r.Title != "Rendered" || r.Content != "# Querit rendered content" {
			t.Fatalf("%+v", r)
		}
	})

	tw(t, f, "Querit provider timeout continues to the next fetch_content fallback", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true}}`)
		t.Setenv("QUERIT_API_KEY", "synthetic-querit-test-key")
		t.Setenv("PARALLEL_API_KEY", "synthetic-parallel-test-key")
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == "https://example.com/timeout":
				return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: tinyfishShell}
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(503, "")
			case c.URL == "https://api.querit.ai/v1/contents":
				return netReply{Hang: true}
			case c.URL == "https://api.parallel.ai/v1/extract":
				return jsonReply(map[string]any{"results": []map[string]any{{"url": "https://example.com/timeout", "title": "Parallel fallback", "full_content": "# Recovered by Parallel\n" + strings.Repeat("content ", 100)}}})
			}
			t.Fatalf("Unexpected fetch %s", c.URL)
			return netReply{}
		})
		ms := int64(1)
		o := pageOpts()
		o.TimeoutMs = &ms
		r := ExtractContent(bg(), "https://example.com/timeout", o)
		urls := fn.urls()
		if !sliceHas(urls, "https://api.querit.ai/v1/contents") || !sliceHas(urls, "https://api.parallel.ai/v1/extract") || r.Title != "Parallel fallback" || !strings.Contains(r.Content, "Recovered by Parallel") {
			t.Fatalf("%v %+v", urls, r)
		}
	})
}
