package websearch

import (
	"context"
	"strings"
	"testing"
)

// The model side calls, with the model scripted: what the port itself costs around a call.
//
//	go test -run xxx -bench 'AnswerFromPage|GenerateSummaryDraft' -benchmem

func benchSide() (*fakeSide, *fakeSideRegistry) {
	model := scModel("test", "page-model")
	model["contextWindow"] = 200000.0
	reg := &fakeSideRegistry{models: []map[string]any{model}}
	reg.completeFn = func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
		return stopText("The answer."), nil
	}
	return &fakeSide{model: model, reg: reg, session: "s"}, reg
}

// A 2 MB page is cut to the model's input budget (360,000 characters for a 200,000-token window).
func BenchmarkAnswerFromPage(b *testing.B) {
	b.Setenv("PIG_CODING_AGENT_DIR", b.TempDir())
	ResetCaches()
	b.Cleanup(ResetCaches)
	side, _ := benchSide()
	page := strings.Repeat("The page text goes on and on. ", 70000)
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := AnswerFromPage(context.Background(), PageAnswerInput{Question: "Q", PageText: page, SourceURL: "https://example.com"}, side); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateSummaryDraft(b *testing.B) {
	side, _ := benchSide()
	side.reg.models = []map[string]any{{"provider": "anthropic", "id": "claude-haiku-4-5"}}
	results := make([]QueryResultData, 8)
	for i := range results {
		results[i] = QueryResultData{Query: "query", Answer: strings.Repeat("An answer sentence. ", 40), Provider: "tavily",
			Results: []SearchResult{{Title: "One", URL: "https://a.test/1"}, {Title: "Two", URL: "https://a.test/2"}}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateSummaryDraft(context.Background(), results, side, SummaryOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
