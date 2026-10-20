package websearch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Boundaries of the hosted providers that no upstream case reaches: pinned here so a changed constant
// or branch fails a test.

func TestHostedEdges(t *testing.T) {
	t.Run("TinyFish fetches inline content in batches of ten", func(t *testing.T) {
		isolate(t)
		t.Setenv("TINYFISH_API_KEY", "k")
		var batches []int
		useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.search.tinyfish.ai?") {
				page := query(c.URL, "page")
				var rs []map[string]any
				n := 10
				if page == "1" {
					n = 1
				}
				for i := range n {
					rs = append(rs, map[string]any{"title": "T", "url": fmt.Sprintf("https://example.com/%s/%d", page, i)})
				}
				return jsonReply(map[string]any{"results": rs})
			}
			batches = append(batches, len(jsonBody(t, c)["urls"].([]any)))
			return jsonReply(map[string]any{"results": []any{}, "errors": []any{}})
		})
		_, err := SearchWithTinyFish(bg(), "q", SearchOptions{NumResults: nf(11), IncludeContent: true})
		noErr(t, err)
		if len(batches) != 2 || batches[0] != 10 || batches[1] != 1 {
			t.Fatalf("%v", batches)
		}
	})

	remaining := func(c netCall) time.Duration {
		dl, ok := c.Ctx.Deadline()
		if !ok {
			return -1
		}
		return time.Until(dl)
	}

	t.Run("Search1API crawl honours the fetch timeout", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCH1API_KEY", "k")
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"results":{"content":"x"}}`) })
		ms := int64(12345)
		_, err := ExtractWithSearch1API(bg(), "https://example.com/", ExtractOptions{TimeoutMs: &ms})
		noErr(t, err)
		if r := remaining(fn.calls[0]); r < 10*time.Second || r > 13*time.Second {
			t.Fatalf("%v", r)
		}
	})

	t.Run("Searchinfinity sends at most five included and five blocked sites", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCHINFINITY_API_KEY", "k")
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"Result":{"WebResults":[]}}`) })
		var filter []string
		for i := range 7 {
			filter = append(filter, fmt.Sprintf("a%d.example.com", i), fmt.Sprintf("-b%d.example.com", i))
		}
		_, err := SearchWithSearchinfinity(bg(), "q", SearchOptions{DomainFilter: filter})
		noErr(t, err)
		f := jsonBody(t, fn.calls[0])["Filter"].(map[string]any)
		if got := strings.Count(f["Sites"].(string), "|") + 1; got != 5 {
			t.Fatalf("%v", f)
		}
		if got := strings.Count(f["BlockHosts"].(string), "|") + 1; got != 5 {
			t.Fatalf("%v", f)
		}
	})

	t.Run("Searchinfinity maps 10403 to a quota or a permission status", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCHINFINITY_API_KEY", "k")
		for message, status := range map[string]string{"daily Quota used": "429", "credits exhausted": "429", "forbidden here": "403"} {
			useNet(t, func(netCall) netReply {
				return jsonReply(map[string]any{"ResponseMetadata": map[string]any{"Error": map[string]any{"CodeN": 10403, "Code": "10403", "Message": message}}})
			})
			_, err := SearchWithSearchinfinity(bg(), "q", SearchOptions{})
			wantErr(t, err, `Searchinfinity Search API error `+status+`: `+message)
		}
	})

	t.Run("Querit caps the crawl timeout at sixty seconds and rounds up", func(t *testing.T) {
		isolate(t)
		t.Setenv("QUERIT_API_KEY", "k")
		for ms, want := range map[int64]float64{600000: 60, 1500: 2, 1: 1} {
			fn := useNet(t, func(netCall) netReply {
				return jsonReply(queritOK(map[string]any{"results": []any{}, "statuses": []any{}}))
			})
			_, err := ExtractWithQuerit(bg(), "https://example.com/", ExtractOptions{TimeoutMs: &ms})
			noErr(t, err)
			if got := jsonBody(t, fn.calls[0])["crawlTimeout"]; got != want {
				t.Fatalf("%d: %v", ms, got)
			}
		}
	})

	t.Run("Parallel treats short keys and placeholders as no key", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{}`)
		for key, want := range map[string]bool{"short": false, "1234567": false, "12345678": true, "Your-Key": false, "CHANGEME": false} {
			t.Setenv("PARALLEL_API_KEY", key)
			if got := IsParallelAvailable(); got != want {
				t.Fatalf("%q: %v", key, got)
			}
		}
	})

	t.Run("Parallel MCP web_fetch asks for full content", func(t *testing.T) {
		isolate(t)
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": map[string]any{"results": []any{}}}})
		})
		_, err := ExtractWithParallelMCP(bg(), "https://example.com/", ExtractOptions{})
		noErr(t, err)
		args := jsonBody(t, fn.calls[0])["params"].(map[string]any)["arguments"].(map[string]any)
		if args["full_content"] != true {
			t.Fatalf("%v", args)
		}
	})

	t.Run("Ollama asks for at most ten results", func(t *testing.T) {
		isolate(t)
		t.Setenv("OLLAMA_API_KEY", "k")
		for n, want := range map[float64]float64{20: 10, 7: 7, 0: 1} {
			fn := useNet(t, func(netCall) netReply { return reply(200, `{"results":[]}`) })
			_, err := SearchWithOllama(bg(), "q", SearchOptions{NumResults: nf(n)})
			noErr(t, err)
			if got := jsonBody(t, fn.calls[0])["max_results"]; got != want {
				t.Fatalf("%v: %v", n, got)
			}
		}
	})

	t.Run("fetch guidance lists the failures in the original's order", func(t *testing.T) {
		extractEnv(t, `{"fetchRouting":{"allowRemoteHostedProviders":true,"providers":["http","kagi","querit","tinyfish"]}}`)
		t.Setenv("KAGI_API_KEY", "k")
		t.Setenv("QUERIT_API_KEY", "k")
		t.Setenv("TINYFISH_API_KEY", "k")
		useNet(t, func(c netCall) netReply {
			if c.URL == "https://example.com/p" {
				return reply(403, "blocked")
			}
			return reply(502, "gateway")
		})
		msg := errOf(ExtractContent(bg(), "https://example.com/p", pageOpts()))
		ti, qi, ki := strings.Index(msg, "TinyFish fallback failed"), strings.Index(msg, "Querit fallback failed"), strings.Index(msg, "Kagi fallback failed")
		if ti < 0 || qi < 0 || ki < 0 || !(ti < qi && qi < ki) {
			t.Fatalf("%d %d %d\n%s", ti, qi, ki, msg)
		}
	})
}
