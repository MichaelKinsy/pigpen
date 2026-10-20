package websearch

import (
	"io"
	"net/http"
	"testing"
)

// Benchmarks of the curator page and the tool renderers (the Performance section of the PR body).

func BenchmarkGenerateCuratorPage(b *testing.B) {
	avail := NewProviderAvailability("all", true, "exa", true, "brave", true, "tavily", true)
	b.ReportAllocs()
	for range b.N {
		_ = GenerateCuratorPage([]string{"first query", "second query"}, "00000000-0000-4000-8000-000000000000", 20, avail, "exa", "auto", nil, nil)
	}
}

func BenchmarkCuratorPageServe(b *testing.B) {
	cbs := newCuratorCallbacks()
	h, err := StartCuratorServer(curatorBaseOptions(20), cbs.cb)
	if err != nil {
		b.Fatal(err)
	}
	defer h.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		resp, err := http.Get(h.URL)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func BenchmarkRenderWebSearchResult(b *testing.B) {
	extractEnvB(b)
	r, err := NewRuntime(&fakeHost{})
	if err != nil {
		b.Fatal(err)
	}
	content := []map[string]any{{"type": "text", "text": "**Provider:** brave\n\nAnswer.\n\n---\n\n**Sources:**\n1. One\n   https://example.com/1"}}
	details := map[string]any{"queryCount": 1.0, "successfulQueries": 1.0, "totalResults": 1.0,
		"curatedQueries": []any{map[string]any{"query": "q", "provider": "brave", "answer": "ans", "sources": []any{map[string]any{"title": "T", "url": "https://example.com/p"}}, "error": nil}}}
	b.ReportAllocs()
	for range b.N {
		if _, err := r.RenderResult("web_search", content, details, false, false, markTheme{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderCallFetch(b *testing.B) {
	extractEnvB(b)
	r, err := NewRuntime(&fakeHost{})
	if err != nil {
		b.Fatal(err)
	}
	args := map[string]any{"urls": []any{"https://example.com/a", "https://example.com/b"}, "mode": "raw", "prompt": "what"}
	b.ReportAllocs()
	for range b.N {
		if _, err := r.callText("fetch_content", args, markTheme{}); err != nil {
			b.Fatal(err)
		}
	}
}

func extractEnvB(b *testing.B) {
	b.Helper()
	b.Setenv("PIG_CODING_AGENT_DIR", b.TempDir())
	b.Setenv("PI_CODING_AGENT_DIR", "")
	ResetCaches()
}
