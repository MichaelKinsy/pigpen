package websearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeBenchConfig(dir, json string, age time.Duration) error {
	path := filepath.Join(dir, "web-search.json")
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
		return err
	}
	if age == 0 {
		return nil
	}
	when := time.Now().Add(-age)
	return os.Chtimes(path, when, when)
}

// The model-backed providers on the inputs of the wire fixtures (port/fixtures): one search each,
// registry lookups included, against an in-process fake endpoint.

const (
	benchXaiBody       = `{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://x.ai/news","title":"xAI News"}]}},{"type":"message","content":[{"type":"output_text","text":"Grok Imagine v1.5 shipped with reference images.","annotations":[{"type":"url_citation","url":"https://x.ai/news/grok-imagine","title":"Grok Imagine","start_index":0,"end_index":12}]}]}],"citations":["https://x.ai/news/grok-imagine","https://x.ai/citation-only"]}`
	benchMistralBody   = `{"outputs":[{"type":"tool.execution","name":"web_search"},{"type":"message.output","content":[{"type":"text","text":"Mistral found the answer."},{"type":"tool_reference","tool":"web_search","title":"Mistral source","url":"https://example.com/source","description":"A useful source."},{"type":"text","text":"It returned references."}]}]}`
	benchKimiBody      = `{"search_results":[{"title":"Kimi result","url":"https://example.com/one","snippet":"First snippet"},{"title":"   ","url":"https://example.com/two"},{"title":"Blocked","url":"https://ads.example.com/x","snippet":"blocked"}]}`
	benchResponsesBody = `{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://example.com/raw","title":"Raw source"}]}},{"type":"message","content":[{"type":"output_text","text":"See [the docs](https://example.com/docs) for more.","annotations":[{"type":"url_citation","url":"https://example.com/docs?utm_source=openai&x=1","title":"Docs","start_index":4,"end_index":20}]}]}]}`
	benchAlphaBody     = `{"output":"Alpha plaintext","results":[{"type":"text_result","url":"https://example.com/alpha?utm_source=openai","title":"Alpha","snippet":"Evidence"},{"type":"image_result","url":"https://ignored.example"}]}`
)

func benchModelProvider(b *testing.B, envKey, envValue, body string, search func(context.Context, string, SearchOptions) (*SearchResponse, error), ctx context.Context) {
	b.Helper()
	benchEnv(b, envKey, envValue)
	b.Cleanup(SetHTTP(stubDoer{body}))
	o := SearchOptions{RecencyFilter: "week", NumResults: nf(3), DomainFilter: []string{"example.com", "-spam.example"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := search(ctx, "what shipped?", o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchXai(b *testing.B) {
	benchModelProvider(b, "XAI_API_KEY", "bench-key", benchXaiBody, SearchWithXai, context.Background())
}

func BenchmarkSearchMistral(b *testing.B) {
	benchModelProvider(b, "MISTRAL_API_KEY", "bench-key", benchMistralBody, SearchWithMistral, context.Background())
}

func BenchmarkSearchOpenAIResponses(b *testing.B) {
	benchModelProvider(b, "OPENAI_API_KEY", "bench-key", benchResponsesBody, SearchWithOpenAI, context.Background())
}

func BenchmarkSearchOpenAIRegistry(b *testing.B) {
	models := []map[string]any{
		{"provider": "openai", "id": "gpt-5.6-terra", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"},
		{"provider": "openai", "id": "gpt-5.7", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"},
		{"provider": "openai", "id": "gpt-5.5-pro", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"},
	}
	ec := &fakeExt{reg: &fakeRegistry{models: models, auth: staticAuth(authOK("sk-registry", map[string]any{"X-Org": "acme"}))}}
	benchModelProvider(b, "UNUSED_API_KEY", "x", benchResponsesBody, SearchWithOpenAI, withExt(ec))
}

// BenchmarkSearchOpenAIAlpha reads a config file that was edited a minute ago, like a real one: the
// config cache serves it after the first call. The original reads and parses it on every call.
func BenchmarkSearchOpenAIAlpha(b *testing.B) { benchAlpha(b, time.Minute) }

// BenchmarkSearchOpenAIAlphaFreshConfig is the same search with a config file written just now, which
// the cache does not trust for two seconds (a second write could keep the stat), so every call
// reads and parses it like the original does.
func BenchmarkSearchOpenAIAlphaFreshConfig(b *testing.B) { benchAlpha(b, 0) }

func benchAlpha(b *testing.B, age time.Duration) {
	benchEnv(b, "OPENAI_API_KEY", "bench-key")
	dir := b.TempDir()
	b.Setenv("PIG_CODING_AGENT_DIR", dir)
	ResetCaches()
	if err := writeBenchConfig(dir, `{"openaiUseAlphaSearch":true,"openaiSearchModel":"gpt-5.6-luna"}`, age); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(SetHTTP(stubDoer{benchAlphaBody}))
	o := SearchOptions{RecencyFilter: "day", DomainFilter: []string{"www.example.com"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := SearchWithOpenAI(context.Background(), "alpha", o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchKimi(b *testing.B) {
	models := []map[string]any{{"provider": "kimi-code", "id": "compat"}, {"provider": "kimi-coding", "id": "primary"}}
	ec := &fakeExt{reg: &fakeRegistry{models: models, auth: staticAuth(authOK("kimi-token", map[string]any{"X-Registry": "forwarded"}))}}
	benchModelProvider(b, "UNUSED_API_KEY", "x", benchKimiBody, SearchWithKimi, withExt(ec))
}
