package websearch

import (
	"errors"
	"reflect"
	"testing"
)

// Twins of the Ollama and Kagi cases of test/new-search-providers (the SerpBase and Bocha cases
// belong to the REST providers).

func TestUpstream_new_search_providers_hosted(t *testing.T) {
	const f = "new-search-providers"

	tw(t, f, "Ollama search and Web Fetch use the Cloud REST endpoints", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"ollamaApiKey":"ollama-test-key"}`)
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case "https://ollama.com/api/web_search":
				return jsonReply(map[string]any{"results": []map[string]any{{"title": "Ollama result", "url": "https://example.com/ollama", "content": "Ollama snippet"}}})
			case "https://ollama.com/api/web_fetch":
				return jsonReply(map[string]any{"title": "Fetched", "content": "Fetched markdown", "links": []any{}})
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		search, err := SearchWithOllama(bg(), "cloud search", SearchOptions{NumResults: nf(7), IncludeContent: true})
		noErr(t, err)
		fetched, err := ExtractWithOllama(bg(), "https://example.com/ollama", pageOpts(), SsrfConfig{})
		noErr(t, err)
		if fn.calls[0].URL != "https://ollama.com/api/web_search" || fn.calls[0].Header.Get("Authorization") != "Bearer ollama-test-key" ||
			!reflect.DeepEqual(jsonBody(t, fn.calls[0]), map[string]any{"query": "cloud search", "max_results": 7.0}) {
			t.Fatalf("%v", fn.calls[0])
		}
		if !reflect.DeepEqual(search.InlineContent, []ExtractedContent{{URL: "https://example.com/ollama", Title: "Ollama result", Content: "Ollama snippet"}}) {
			t.Fatalf("%+v", search.InlineContent)
		}
		if fn.calls[1].URL != "https://ollama.com/api/web_fetch" || !reflect.DeepEqual(jsonBody(t, fn.calls[1]), map[string]any{"url": "https://example.com/ollama"}) || fetched.Content != "Fetched markdown" {
			t.Fatalf("%v %+v", fn.calls[1], fetched)
		}
	})

	tw(t, f, "Kagi search maps v1 results and Extract maps markdown", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"kagiApiKey":"kagi-test-key"}`)
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case "https://kagi.com/api/v1/search":
				return jsonReply(map[string]any{"data": map[string]any{"search": []map[string]any{{"title": "Kagi result", "url": "https://example.com/kagi", "snippet": "Kagi snippet"}}}})
			case "https://kagi.com/api/v1/extract":
				return jsonReply(map[string]any{"data": []map[string]any{{"url": "https://example.com/kagi", "title": "Kagi extract", "markdown": "Kagi markdown"}}})
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		search, err := SearchWithKagi(bg(), "premium search", SearchOptions{NumResults: nf(3), IncludeContent: true})
		noErr(t, err)
		fetched, err := ExtractWithKagi(bg(), "https://example.com/kagi", pageOpts(), SsrfConfig{})
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != "https://kagi.com/api/v1/search" || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer kagi-test-key" ||
			!reflect.DeepEqual(jsonBody(t, c), map[string]any{"query": "premium search", "limit": 3.0}) {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(search.Results, []SearchResult{{Title: "Kagi result", URL: "https://example.com/kagi", Snippet: "Kagi snippet"}}) {
			t.Fatalf("%+v", search.Results)
		}
		if fn.calls[1].URL != "https://kagi.com/api/v1/extract" || !reflect.DeepEqual(jsonBody(t, fn.calls[1]), map[string]any{"pages": []any{map[string]any{"url": "https://example.com/kagi"}}}) || fetched.Content != "Kagi markdown" {
			t.Fatalf("%v %+v", fn.calls[1], fetched)
		}
	})
}
