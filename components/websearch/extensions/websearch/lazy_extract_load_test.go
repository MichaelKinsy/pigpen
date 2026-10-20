package websearch

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Twin of lazy-extract-load.test.mjs. Upstream proves, with a module-load hook, that extract.ts is
// not loaded while the extension registers and loads on the first fetch. A Go extension compiles
// extraction in and loads nothing lazily, so that marker has no counterpart (PORT.md). What
// remains observable is checked: the five tools register in order, the command registers, and the
// first fetch extracts the page. The `websearch` command and the shortcuts belong to the curator
// (later slice); the session handlers are checked by TestExtensionRegistersEventsAndCommand.
func TestUpstream_lazy_extract_load(t *testing.T) {
	tw(t, "lazy-extract-load", "registers tool definitions and loads content extraction on first use", func(t *testing.T) {
		r, _ := newRuntime(t, `{"ssrf":{"allowRanges":["127.0.0.0/8"]}}`)
		if got, want := toolNames(r), []string{"web_search", "source_check", "fetch_content", "get_search_content", "web_enable"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("tools %v, want %v", got, want)
		}
		if !sliceHas(r.Commands(), "search") {
			t.Fatalf("commands %v", r.Commands())
		}

		article := strings.Repeat("Lazy loading keeps extension startup responsive while preserving first-use extraction. ", 20)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><html><head><title>Lazy extraction</title></head><body><article><h1>Lazy extraction works</h1><p>" + article + "</p></article></body></html>"))
		}))
		defer server.Close()

		result := run(t, mustTool(t, r, "fetch_content"), map[string]any{"url": server.URL + "/article"})
		if n, _ := dNum(result, "successful"); n != 1 {
			t.Fatalf("%v", result.Details)
		}
		last := result.Content[len(result.Content)-1].Text
		if !strings.Contains(last, "preserving first-use extraction") {
			t.Fatal(last)
		}
	})
}
