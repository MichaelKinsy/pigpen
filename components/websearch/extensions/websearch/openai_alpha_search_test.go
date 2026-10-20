package websearch

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Twins of test/openai-alpha-search.test.mjs (the scenario runner is openai_scenario_test.go).

func routeRaw(extra string) json.RawMessage {
	return json.RawMessage(strings.Replace(searchRoute, "}", extra+"}", 1))
}

func sourcesN(prefix string, n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = oaSource(fmt.Sprintf("%s%d", prefix, i), "Source", "Evidence")
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestUpstream_openai_alpha_search_alpha(t *testing.T) {
	const f = "openai-alpha-search"

	tw(t, f, "Responses remains unchanged with alpha flag ${flag}", func(t *testing.T) {
		for _, flag := range []any{absent{}, false} {
			out := runOpenAI(t, oaScenario{config: map[string]any{"openaiUseAlphaSearch": flag}, payload: legacyPayload})
			out.mustOK(t)
			req := out.requests[0]
			tools, _ := req.Body["tools"].([]any)
			if req.URL != "https://api.openai.com/v1/responses" || headerOf(req, "OpenAI-Beta") != "responses=experimental" ||
				tools[0].(map[string]any)["type"] != "web_search" || req.Body["stream"] != true || req.Body["commands"] != nil ||
				out.searchResponse(t).Answer != "Responses answer" {
				t.Fatalf("flag %v: %+v", flag, req)
			}
		}
	})

	tw(t, f, "alpha uses a separate JSON protocol and maps plaintext sources", func(t *testing.T) {
		if openAISearchTimeout != 60*time.Second {
			t.Fatalf("deadline %v", openAISearchTimeout)
		}
		out := runOpenAI(t, oaScenario{calls: 2, config: map[string]any{"openaiSearchModel": "gpt-5.6-luna"}})
		out.mustOK(t)
		req := out.requests[0]
		if req.URL != "https://api.openai.com/v1/alpha/search" || !reflect.DeepEqual(sortedKeys(req.Body), []string{"commands", "id", "model"}) {
			t.Fatalf("%s %v", req.URL, req.Body)
		}
		id := req.Body["id"].(string)
		if !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(id) || id == out.requests[1].Body["id"] {
			t.Fatalf("ids %q %q", id, out.requests[1].Body["id"])
		}
		if req.Body["model"] != "gpt-5.6-luna" ||
			!reflect.DeepEqual(req.Body["commands"], map[string]any{"search_query": []any{map[string]any{"q": "test query"}}}) {
			t.Fatalf("%v", req.Body)
		}
		if headerOf(req, "Authorization") != "Bearer alpha-test-key" || headerOf(req, "Accept") != "application/json" ||
			headerOf(req, "Originator") != "codex_cli_rs" || hasHeader(req, "OpenAI-Beta") {
			t.Fatalf("%v", req.Header)
		}
		want := &SearchResponse{Answer: "Plain search output", Results: []SearchResult{{Title: "Source", URL: "https://example.com/a", Snippet: "Evidence"}}}
		if !reflect.DeepEqual(out.searchResponse(t), want) {
			t.Fatalf("%+v", out.result)
		}
	})

	tw(t, f, "alpha preserves explicit gateway origin, port, path prefix and query parameters", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{
			registry: true, model: map[string]any{"provider": "gateway", "baseUrl": "http://localhost:8921/api/v4"},
			config: map[string]any{"openaiSearchProviders": []string{"gateway"}, "openaiResponsesUrl": "http://localhost:8921/api/v4/responses/?version=1", "openaiSearchModel": "custom-search-model"},
			headers: map[string]any{"X-Gateway": "keep", "authorization": "stale", "OpenAI-Beta": "responses=experimental", "session_id": "private", "Conversation_ID": "private",
				"X-Codex-Beta-Features": "private", "X-Codex-Turn-State": "private", "x-openai-internal-codex-responses-lite": "private", "X-Codex-Turn-Metadata": "keep"},
		})
		out.mustOK(t)
		req := out.requests[0]
		if req.URL != "http://localhost:8921/api/v4/alpha/search?version=1" || req.Body["model"] != "custom-search-model" ||
			headerOf(req, "Authorization") != "Bearer registry-test-key" || headerOf(req, "X-Gateway") != "keep" || headerOf(req, "X-Codex-Turn-Metadata") != "keep" {
			t.Fatalf("%s %v %v", req.URL, req.Body, req.Header)
		}
		for _, key := range []string{"openai-beta", "session_id", "conversation_id", "x-codex-beta-features", "x-codex-turn-state", "x-openai-internal-codex-responses-lite"} {
			if hasHeader(req, key) {
				t.Fatalf("%s was sent", key)
			}
		}
	})

	tw(t, f, "alpha keeps Codex endpoint/account selection (current model: ${current})", func(t *testing.T) {
		for _, current := range []bool{false, true} {
			out := runOpenAI(t, oaScenario{current: current, registry: true, key: strp(codexToken()),
				model: map[string]any{"provider": "openai-codex", "api": "openai-codex-responses", "baseUrl": "https://chatgpt.com/backend-api"}})
			out.mustOK(t)
			if out.requests[0].URL != "https://chatgpt.com/backend-api/codex/alpha/search" || headerOf(out.requests[0], "chatgpt-account-id") != "test-account" {
				t.Fatalf("current %v: %s %v", current, out.requests[0].URL, out.requests[0].Header)
			}
		}
	})

	tw(t, f, "alpha current-model routing keeps official model and ignores configured gateway/model", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{current: true, config: map[string]any{"openaiResponsesUrl": "http://gateway.invalid/responses", "openaiSearchModel": "not-current"}})
		out.mustOK(t)
		if out.requests[0].URL != "https://api.openai.com/v1/alpha/search" || out.requests[0].Body["model"] != "gpt-5.6-luna" {
			t.Fatalf("%s %v", out.requests[0].URL, out.requests[0].Body)
		}
	})

	tw(t, f, "alpha does not weaken custom-baseURL credential or current-model guards", func(t *testing.T) {
		for _, current := range []bool{false, true} {
			out := runOpenAI(t, oaScenario{current: current, registry: true, model: map[string]any{"baseUrl": "https://gateway.invalid/v1"}})
			out.wantNoRequests(t)
			re := `custom baseUrl`
			if current {
				re = `not eligible`
			}
			wantErr(t, out.err, re)
		}
	})

	tw(t, f, "alpha flag rejects non-boolean ${JSON.stringify(value)}", func(t *testing.T) {
		for _, value := range []any{"true", 1, nil, map[string]any{}} {
			out := runOpenAI(t, oaScenario{config: map[string]any{"openaiUseAlphaSearch": value}})
			out.wantNoRequests(t)
			wantErr(t, out.err, `openaiUseAlphaSearch.*must be a boolean`)
		}
	})

	tw(t, f, "alpha rejects endpoints without a responses suffix before sending credentials", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{config: map[string]any{"openaiResponsesUrl": "https://gateway.invalid/v1/chat/completions"}})
		out.wantNoRequests(t)
		wantErr(t, out.err, `must.*/responses`)
	})

	tw(t, f, "alpha maps ${recencyFilter} recency and allowed domains", func(t *testing.T) {
		for recency, days := range map[string]float64{"day": 1, "week": 7, "month": 30, "year": 365} {
			out := runOpenAI(t, oaScenario{options: SearchOptions{RecencyFilter: recency, DomainFilter: []string{"https://www.example.com/path", "www.example.com"}}})
			out.mustOK(t)
			got := out.requests[0].Body["commands"].(map[string]any)["search_query"]
			want := []any{map[string]any{"q": "test query", "recency": days, "domains": []any{"www.example.com"}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: %v", recency, got)
			}
		}
	})

	tw(t, f, "alpha rejects excluded domains instead of silently ignoring them", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{options: SearchOptions{DomainFilter: []string{"example.com", "-blocked.example"}}})
		out.wantNoRequests(t)
		wantErr(t, out.err, `(?i)unsupported.*excluded domains`)
	})

	tw(t, f, "alpha ignores malformed/non-text sources, cleans URLs, deduplicates and caps results", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{options: SearchOptions{NumResults: nf(2)}, payload: map[string]any{"output": "  Plaintext  ", "results": []any{
			nil, map[string]any{"type": "image_result", "url": "https://ignored.example"}, oaSource("javascript:alert(1)", "Source", "Evidence"), oaSource("not a URL", "Source", "Evidence"),
			oaSource("https://example.com/a?utm_source=openai", "Source", "Evidence"), oaSource("https://example.com/a", "Source", "Evidence"),
			oaSource("https://example.com/b", "", 42), oaSource("https://example.com/c", "Source", "Evidence"),
		}}})
		out.mustOK(t)
		want := &SearchResponse{Answer: "Plaintext", Results: []SearchResult{
			{Title: "Source", URL: "https://example.com/a", Snippet: "Evidence"},
			{Title: "https://example.com/b", URL: "https://example.com/b", Snippet: ""},
		}}
		if !reflect.DeepEqual(out.searchResponse(t), want) {
			t.Fatalf("%+v", out.searchResponse(t))
		}
	})

	tw(t, f, "alpha defaults to five deduplicated sources when numResults is omitted (tool: ${tool})", func(t *testing.T) {
		sources := sourcesN("https://example.com/default-result-", 25)
		var wantURLs []string
		for _, s := range sources[:5] {
			wantURLs = append(wantURLs, s.(map[string]any)["url"].(string))
		}
		for _, tool := range []bool{false, true} {
			results := append([]any{nil, map[string]any{"type": "image_result", "url": "https://ignored.example"}, oaSource("javascript:alert(1)", "Source", "Evidence"),
				oaSource("https://example.com/default-result-0?utm_source=openai", "Source", "Evidence"), sources[0]}, sources...)
			out := runOpenAI(t, oaScenario{tool: tool, payload: map[string]any{"output": "Keep the plaintext answer", "results": results}})
			out.mustOK(t)
			if tool {
				if out.isError || len(out.stored) != 2 {
					t.Fatalf("%v %d", out.isError, len(out.stored))
				}
				for _, rs := range out.stored {
					if !eqStrings(urlsOf(rs), wantURLs) {
						t.Fatalf("%v", urlsOf(rs))
					}
				}
				if !strings.Contains(out.retrieved, "default-result-4") || regexp.MustCompile(`default-result-5\b`).MatchString(out.retrieved) {
					t.Fatal(out.retrieved)
				}
			} else if r := out.searchResponse(t); r.Answer != "Keep the plaintext answer" || !eqStrings(urlsOf(r.Results), wantURLs) {
				t.Fatalf("%+v", r)
			}
		}
	})

	tw(t, f, "alpha caps requested source count at 20", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{options: SearchOptions{NumResults: nf(50)}, payload: map[string]any{"results": sourcesN("https://example.com/", 25)}})
		out.mustOK(t)
		if n := len(out.searchResponse(t).Results); n != 20 {
			t.Fatal(n)
		}
	})

	tw(t, f, `alpha accepts ${response.output ? "plaintext only" : "structured sources only"}`, func(t *testing.T) {
		for _, payload := range []map[string]any{{"output": "Only text"}, {"results": []any{oaSource("https://example.com/a", "Source", "Evidence")}}} {
			out := runOpenAI(t, oaScenario{payload: payload})
			out.mustOK(t)
			if r := out.searchResponse(t); r.Answer == "" && len(r.Results) == 0 {
				t.Fatalf("%v", payload)
			}
		}
	})

	tw(t, f, "alpha rejects unusable response ${raw}", func(t *testing.T) {
		for _, raw := range []string{"not JSON", "null", "[]", "{}", `{"encrypted_output":"ciphertext"}`, `{"output":[{"type":"web_search_call"}]}`} {
			out := runOpenAI(t, oaScenario{raw: strp(raw)})
			wantErr(t, out.err, `(?i)invalid JSON|invalid response|no parseable results`)
			if out.result != nil && !reflect.ValueOf(out.result).IsNil() {
				t.Fatalf("%s: result %+v", raw, out.result)
			}
		}
	})

	routed := func() map[string]any {
		return map[string]any{"tavilyApiKey": "tavily-test-key", "searchRouting": routeRaw("")}
	}

	tw(t, f, "alpha HTTP ${status} follows configured provider fallback without a Responses retry", func(t *testing.T) {
		for _, status := range []int{404, 405, 501, 503, 429} {
			out := runOpenAI(t, oaScenario{route: true, status: status, raw: strp("Endpoint error"), config: routed()})
			out.mustOK(t)
			if out.result.(*AttributedSearchResponse).Provider != "tavily" || len(out.requests) != 2 ||
				!strings.HasSuffix(out.requests[0].URL, "/alpha/search") || !strings.Contains(out.requests[1].URL, "api.tavily.com") {
				t.Fatalf("%d: %v", status, out.urls())
			}
		}
	})

	tw(t, f, `alpha fallback covers ${scenario.options ? "unsupported filter" : scenario.raw ? "invalid response" : "network error"}`, func(t *testing.T) {
		for _, sc := range []oaScenario{
			{options: SearchOptions{DomainFilter: []string{"-example.com"}}},
			{raw: strp("bad JSON")},
			{networkError: true},
		} {
			sc.route, sc.config = true, routed()
			out := runOpenAI(t, sc)
			out.mustOK(t)
			if out.result.(*AttributedSearchResponse).Provider != "tavily" {
				t.Fatalf("%+v", out.result)
			}
		}
	})

	tw(t, f, "alpha respects fallback policy and keeps HTTP auth errors terminal/redacted", func(t *testing.T) {
		for _, status := range []int{401, 403, 404} {
			cfg := routed()
			cfg["searchRouting"] = json.RawMessage(`{"providers":["openai","tavily"],"fallbackOn":["network"]}`)
			out := runOpenAI(t, oaScenario{route: true, status: status, raw: strp("Error alpha-test-key"), config: cfg})
			want := "auth"
			if status == 404 {
				want = "unsupported"
			}
			if out.kind != want || out.err == nil || strings.Contains(out.err.Error(), "alpha-test-key") || len(out.requests) != 1 {
				t.Fatalf("%d: kind %q err %v requests %d", status, out.kind, out.err, len(out.requests))
			}
		}
	})

	tw(t, f, "alpha keeps pre-abort, in-flight cancellation and the existing 60s deadline", func(t *testing.T) {
		before := runOpenAI(t, oaScenario{preAbort: true})
		before.wantNoRequests(t)
		if !isAbortError(before.err) {
			t.Fatalf("pre-abort: %v", before.err)
		}
		during := runOpenAI(t, oaScenario{hang: "abort", route: true, config: routed()})
		if during.kind != "aborted" || len(during.requests) > 1 {
			t.Fatalf("in-flight: kind %q requests %d err %v", during.kind, len(during.requests), during.err)
		}
		if openAISearchTimeout != 60*time.Second {
			t.Fatalf("the deadline is %v", openAISearchTimeout)
		}
		timeout := runOpenAI(t, oaScenario{hang: "timeout"})
		if !isTimeout(timeout.err) || len(timeout.requests) != 1 {
			t.Fatalf("deadline: %v, %d requests", timeout.err, len(timeout.requests))
		}
	})

	tw(t, f, "alpha traverses configured current-model routing", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{route: true, current: true, config: map[string]any{"searchRouting": routeRaw(`,"useCurrentModel":true`)}})
		out.mustOK(t)
		if out.result.(*AttributedSearchResponse).Provider != "openai" || out.requests[0].URL != "https://api.openai.com/v1/alpha/search" || out.requests[0].Body["model"] != "gpt-5.6-luna" {
			t.Fatalf("%+v %v", out.result, out.urls())
		}
	})

	tw(t, f, "alpha executes through registered web_search and preserves stored-content retrieval", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{tool: true})
		out.mustOK(t)
		if out.isError || out.searchID == "" || !strings.Contains(out.text, "Plain search output") ||
			!strings.Contains(out.retrieved, "Plain search output") || !strings.Contains(out.retrieved, "example.com") {
			t.Fatalf("%v %q %q", out.isError, out.text, out.retrieved)
		}
		// Two queries, no Responses or summary request. The two run at once, so the order of arrival
		// is not the order of the queries (the upstream fetch order follows the start order).
		if len(out.requests) != 2 {
			t.Fatalf("%d requests", len(out.requests))
		}
		var qs []string
		for _, r := range out.requests {
			if !strings.HasSuffix(r.URL, "/alpha/search") {
				t.Fatal(r.URL)
			}
			qs = append(qs, r.Body["commands"].(map[string]any)["search_query"].([]any)[0].(map[string]any)["q"].(string))
		}
		if !eqStrings(sortedCopy(qs), []string{"second query", "test query"}) {
			t.Fatal(qs)
		}
	})

	tw(t, f, "alpha keeps ${bodyHang} active during response body consumption", func(t *testing.T) {
		abort := runOpenAI(t, oaScenario{bodyHang: "abort"})
		if !isAbortError(abort.err) || len(abort.requests) != 1 {
			t.Fatalf("abort: %v %d", abort.err, len(abort.requests))
		}
		timeout := runOpenAI(t, oaScenario{bodyHang: "timeout"})
		if !isTimeout(timeout.err) || len(timeout.requests) != 1 {
			t.Fatalf("timeout: %v %d", timeout.err, len(timeout.requests))
		}
	})

	tw(t, f, "alpha caller cancellation takes precedence over unsupported filters", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{preAbort: true, options: SearchOptions{DomainFilter: []string{"-example.com"}}})
		out.wantNoRequests(t)
		if !isAbortError(out.err) {
			t.Fatalf("%v", out.err)
		}
	})
}
