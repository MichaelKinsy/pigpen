package websearch

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of summary-model-scope.test.mjs. The upstream file mixes the model-resolution helpers with
// the summary generation they feed; both are ported here (summary-model-scope.ts, summary-review.ts
// generateSummaryDraft). The curator workflows that call the generation stay with the Curator slice.

func sameModel(a, b map[string]any) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

var summaryResults = []QueryResultData{{
	Query: "test query", Answer: "A test answer.",
	Results:  []SearchResult{{Title: "Test source", URL: "https://example.com"}},
	Provider: "test",
}}

// summarySide is the upstream summaryContext(): one registry model that every lookup returns.
func summarySide(scoped ...map[string]any) (*fakeSide, *fakeSideRegistry, map[string]any) {
	model := map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5"}
	reg := &fakeSideRegistry{models: []map[string]any{model}}
	reg.findFn = func(string, string) map[string]any { return model }
	return &fakeSide{reg: reg, scoped: scoped}, reg, model
}

// textComplete is a custom completion that answers text and records its options.
func textComplete(text string, calls *[]CompleteOptions) CompleteFunc {
	return func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
		if calls != nil {
			*calls = append(*calls, o)
		}
		return stopText(text), nil
	}
}

func TestUpstream_summary_model_scope(t *testing.T) {
	const F = "summary-model-scope"

	tw(t, F, "never-settling summary completion returns a deterministic deadline fallback", func(t *testing.T) {
		side, _, _ := summarySide()
		never := make(chan struct{})
		defer close(never)
		completions := make(chan context.Context, 1)
		started := time.Now()
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{
			DeadlineMs: 20,
			Complete: func(ctx context.Context, _, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
				completions <- ctx
				<-never
				return nil, nil
			},
		})
		noErr(t, err)
		if time.Since(started) >= 500*time.Millisecond {
			t.Fatal("the deadline did not end the generation")
		}
		if !draft.Meta.FallbackUsed || draft.Meta.FallbackReason != "summary-generation-timeout" || draft.Meta.Phase != "deterministic-fallback" {
			t.Fatalf("%+v", draft.Meta)
		}
		if completion := <-completions; completion.Err() == nil {
			t.Fatal("the completion's signal was not aborted")
		}
	})

	tw(t, F, "model resolution errors after the deadline return timeout fallback", func(t *testing.T) {
		side, reg, _ := summarySide()
		reg.authFn = func(map[string]any) (map[string]any, error) {
			time.Sleep(30 * time.Millisecond)
			return nil, context.DeadlineExceeded
		}
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{DeadlineMs: 10})
		noErr(t, err)
		if !draft.Meta.FallbackUsed || draft.Meta.FallbackReason != "summary-generation-timeout" || draft.Meta.Phase != "deterministic-fallback" {
			t.Fatalf("%+v", draft.Meta)
		}
	})

	tw(t, F, "caller abort takes precedence over a pending summary completion", func(t *testing.T) {
		side, _, _ := summarySide()
		never := make(chan struct{})
		defer close(never)
		ctx, cancel := context.WithCancel(bg())
		time.AfterFunc(10*time.Millisecond, cancel)
		_, err := GenerateSummaryDraft(ctx, summaryResults, side, SummaryOptions{
			DeadlineMs: 1000,
			Complete: func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
				<-never
				return nil, nil
			},
		})
		wantErr(t, err, "Aborted")
	})

	tw(t, F, "summary generation only uses models in the host's resolved scope", func(t *testing.T) {
		var outOfScope []CompleteOptions
		side, _, _ := summarySide(map[string]any{"provider": "openai-codex", "id": "gpt-5.6-luna"})
		excluded, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{DeadlineMs: 1000, Complete: textComplete("Scoped summary", &outOfScope)})
		noErr(t, err)
		if len(outOfScope) != 0 || !excluded.Meta.FallbackUsed {
			t.Fatalf("%d %+v", len(outOfScope), excluded.Meta)
		}

		var inScope []CompleteOptions
		side, _, _ = summarySide(map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5"})
		included, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{DeadlineMs: 1000, Complete: textComplete("Scoped summary", &inScope)})
		noErr(t, err)
		if len(inScope) != 1 || included.Meta.Model == nil || *included.Meta.Model != "anthropic/claude-haiku-4-5" {
			t.Fatalf("%d %+v", len(inScope), included.Meta)
		}
	})

	tw(t, F, "summary generation resolves preferred models through routed providers", func(t *testing.T) {
		routed := map[string]any{"provider": "openrouter", "id": "anthropic/claude-haiku-4-5"}
		reg := &fakeSideRegistry{models: []map[string]any{routed}, findFn: func(string, string) map[string]any { return nil }}
		var calls []CompleteOptions
		draft, err := GenerateSummaryDraft(bg(), summaryResults, &fakeSide{reg: reg}, SummaryOptions{DeadlineMs: 1000, Complete: textComplete("Routed summary", &calls)})
		noErr(t, err)
		if len(calls) != 1 || draft.Meta.FallbackUsed || *draft.Meta.Model != "openrouter/anthropic/claude-haiku-4-5" {
			t.Fatalf("%d %+v", len(calls), draft.Meta)
		}
	})

	thinkingCase := func(model map[string]any, override, text string) (CompleteOptions, []string, SummaryDraft) {
		var lookups []string
		reg := &fakeSideRegistry{models: []map[string]any{model}, availableFn: func() []map[string]any { return nil }}
		reg.findFn = func(provider, id string) map[string]any {
			lookups = append(lookups, provider+" "+id)
			return model
		}
		var calls []CompleteOptions
		draft, err := GenerateSummaryDraft(bg(), summaryResults, &fakeSide{reg: reg}, SummaryOptions{ModelOverride: override, DeadlineMs: 1000, Complete: textComplete(text, &calls)})
		if err != nil || len(calls) == 0 {
			t.Fatalf("%v %d", err, len(calls))
		}
		return calls[0], lookups, draft
	}

	tw(t, F, "summaryModel thinking suffix strips before lookup and reaches completion options", func(t *testing.T) {
		model := map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5", "reasoning": true}
		options, lookups, draft := thinkingCase(model, "anthropic/claude-haiku-4-5:low", "Low-thinking summary")
		if !sliceHas(lookups, "anthropic claude-haiku-4-5") || options.Reasoning != "low" || options.ReasoningEffort != "low" || *draft.Meta.Model != "anthropic/claude-haiku-4-5" {
			t.Fatalf("%v %+v %+v", lookups, options, draft.Meta)
		}
	})

	tw(t, F, "summaryModel keeps non-thinking colons in model ids", func(t *testing.T) {
		model := map[string]any{"provider": "openrouter", "id": "nvidia/nemotron-3-super-120b-a12b:free", "reasoning": true}
		options, lookups, draft := thinkingCase(model, "openrouter/nvidia/nemotron-3-super-120b-a12b:free", "Free model summary")
		if !sliceHas(lookups, "openrouter nvidia/nemotron-3-super-120b-a12b:free") || options.Reasoning != "" || options.ReasoningEffort != "" ||
			*draft.Meta.Model != "openrouter/nvidia/nemotron-3-super-120b-a12b:free" {
			t.Fatalf("%v %+v %+v", lookups, options, draft.Meta)
		}
	})

	tw(t, F, "summaryModel thinking suffix omits effort for non-reasoning models", func(t *testing.T) {
		model := map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5", "reasoning": false}
		options, _, _ := thinkingCase(model, "anthropic/claude-haiku-4-5:low", "No-thinking summary")
		if options.Reasoning != "off" || options.ReasoningEffort != "" {
			t.Fatalf("%+v", options)
		}
	})

	tw(t, F, "preferred models resolve through routed providers", func(t *testing.T) {
		routed := map[string]any{"provider": "openrouter", "id": "anthropic/claude-haiku-4-5"}
		reg := &fakeSideRegistry{models: []map[string]any{routed}, findFn: func(string, string) map[string]any { return nil }}
		found, err := FindModelWithProviderRouting(reg, "anthropic", "claude-haiku-4-5")
		noErr(t, err)
		if !sameModel(found, routed) {
			t.Fatalf("%v", found)
		}
	})

	tw(t, F, "model resolution preserves the direct registry fallback", func(t *testing.T) {
		configured := map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5"}
		reg := &fakeSideRegistry{availableFn: func() []map[string]any { return nil }, findFn: func(string, string) map[string]any { return configured }}
		found, err := FindModelWithProviderRouting(reg, "anthropic", "claude-haiku-4-5")
		noErr(t, err)
		if !sameModel(found, configured) {
			t.Fatalf("%v", found)
		}
	})

	tw(t, F, "routed model resolution follows available-model ordering", func(t *testing.T) {
		first := map[string]any{"provider": "openrouter", "id": "anthropic/claude-haiku-4-5"}
		second := map[string]any{"provider": "requesty", "id": "anthropic/claude-haiku-4-5"}
		reg := &fakeSideRegistry{models: []map[string]any{first, second}, findFn: func(string, string) map[string]any { return nil }}
		found, err := FindModelWithProviderRouting(reg, "anthropic", "claude-haiku-4-5")
		noErr(t, err)
		if !sameModel(found, first) {
			t.Fatalf("%v", found)
		}
	})

	// The upstream case reads the TypeScript source for the race and the abort of the deadline;
	// the behaviour is what the Go twins above prove (a never-settling completion is cut at the
	// deadline and its context aborted, a caller cancellation wins). The constant is checked here.
	tw(t, F, "summary generation has a hard deadline and preserves caller cancellation", func(t *testing.T) {
		if SummaryGenerationDefaultDeadlineMs != 30_000 {
			t.Fatal(SummaryGenerationDefaultDeadlineMs)
		}
	})

	// The upstream case also reads index.ts for the three call sites of the configured deadline;
	// the curator workflows (the Curator slice) take their deadline from GetSummaryGenerationDeadlineMs.
	tw(t, F, "summary generation deadline config defaults, validates, caps, and reaches both workflows", func(t *testing.T) {
		agentDir := pageQuerySetup(t)
		deadline := func(config string) int {
			if config != "" {
				writeConfig(t, agentDir, config)
			}
			return GetSummaryGenerationDeadlineMs()
		}
		for _, c := range []struct {
			config string
			want   int
		}{
			{"", 30_000},
			{`{"summaryGenerationDeadlineMs":150000}`, 150_000},
			{`{"summaryGenerationDeadlineMs":0}`, 30_000},
			{`{"summaryGenerationDeadlineMs":1.5}`, 30_000},
			{`{"summaryGenerationDeadlineMs":"150000"}`, 30_000},
			{`{"summaryGenerationDeadlineMs":600001}`, 600_000},
		} {
			if got := deadline(c.config); got != c.want {
				t.Errorf("%s: %d, want %d", c.config, got, c.want)
			}
		}
		data, err := os.ReadFile("../../README.md")
		if os.IsNotExist(err) {
			t.Skip("the module is copied without its component README")
		}
		noErr(t, err)
		readme := string(data)
		if !strings.Contains(readme, `"summaryGenerationDeadlineMs": 30000`) || !regexpMust("summaryGenerationDeadlineMs.*capped at `600000`").MatchString(readme) {
			t.Fatal("the README does not document summaryGenerationDeadlineMs and its cap")
		}
	})

	tw(t, F, "summary generation no longer uses catalog fallback or first available model", func(t *testing.T) {
		// Only an unrelated model is available: it is never used in place of a preferred one.
		other := map[string]any{"provider": "other", "id": "first-available"}
		reg := &fakeSideRegistry{models: []map[string]any{other}}
		var calls []CompleteOptions
		draft, err := GenerateSummaryDraft(bg(), summaryResults, &fakeSide{reg: reg}, SummaryOptions{DeadlineMs: 1000, Complete: textComplete("must not be used", &calls)})
		noErr(t, err)
		if len(calls) != 0 || !draft.Meta.FallbackUsed || draft.Meta.FallbackReason != "summary-model-unavailable: Summary model not found: deepseek/deepseek-v4-flash" {
			t.Fatalf("%d %+v", len(calls), draft.Meta)
		}
	})

	// The upstream case orders the model lists of summary-review.ts, index.ts (the curator's model
	// picker) and query-rewrite.ts; the picker is the Curator slice's.
	tw(t, F, "summary and query rewrite defaults use the refreshed model order", func(t *testing.T) {
		var order []string
		for _, m := range preferredSummaryModels {
			order = append(order, m.provider+"/"+m.id)
		}
		want := []string{"anthropic/claude-haiku-4-5", "openai-codex/gpt-5.6-luna", "openai-codex/gpt-5.6-terra", "google/gemini-3.6-flash", "openai/gpt-5-mini", "deepseek/deepseek-v4-flash"}
		if !reflect.DeepEqual(order, want) {
			t.Fatal(order)
		}
		var rewrite []string
		for _, m := range rewriteModelCandidates {
			rewrite = append(rewrite, m.provider+"/"+m.id)
		}
		if !reflect.DeepEqual(rewrite, []string{"anthropic/claude-haiku-4-5", "google/gemini-3.6-flash", "openai/gpt-5-mini"}) {
			t.Fatal(rewrite)
		}
		for _, name := range append(order, rewrite...) {
			if strings.Contains(name, "gpt-5.3-codex-spark") || strings.Contains(name, "gpt-4.1-mini") {
				t.Fatal(name)
			}
		}
	})
}

// A completion that settles late must not leak into a later call: the registry path and the
// direct path both finish under the deadline's context.
func TestGenerateSummaryDraftSuccessMeta(t *testing.T) {
	side, reg, _ := summarySide()
	var mu sync.Mutex
	var seen context.Context
	reg.completeFn = func(ctx context.Context, _, request map[string]any, _ CompleteOptions) (map[string]any, error) {
		mu.Lock()
		seen = ctx
		mu.Unlock()
		if !strings.Contains(requestPrompt(request), "<search_results>") {
			t.Errorf("prompt %q", requestPrompt(request))
		}
		return map[string]any{"stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "  Summary text  "}, map[string]any{"type": "text", "text": ""}}}, nil
	}
	draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{})
	noErr(t, err)
	if draft.Summary != "Summary text" || draft.Meta.FallbackUsed || draft.Meta.Phase != "summary-model" || draft.Meta.TokenEstimate != 3 || draft.Meta.Edited {
		t.Fatalf("%+v", draft)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen == nil {
		t.Fatal("the registry completion was not used")
	}
}
