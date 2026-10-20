package websearch

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// The summary prompt and the deterministic summary (summary-review.ts:35-193) have no upstream
// test of their own; these pin their exact text against the original's code.

func TestBuildSummaryPrompt(t *testing.T) {
	const head = "You are writing the final web search summary for a coding assistant.\n" +
		"Write a concise, factual summary using only the provided search results.\n" +
		"Requirements:\n" +
		"- Keep it readable and skimmable.\n" +
		"- Include key findings and caveats.\n" +
		"- Do not invent sources or claims.\n" +
		"- If evidence is weak or conflicting, say so explicitly.\n" +
		"- End with a short \"Sources\" section listing the most relevant URLs.\n"
	const body = "\n<search_results>\n\n[Result 1]\nQuery: test query\nProvider: test\nAnswer: A test answer.\nSources:\n1. Test source — https://example.com\n\n</search_results>"
	if got := BuildSummaryPrompt(summaryResults, ""); got != head+body {
		t.Fatalf("%q", got)
	}
	want := head + "- Incorporate the user feedback provided below into the summary.\n" + body + "\n\n<user_feedback>\nBe brief\n</user_feedback>"
	if got := BuildSummaryPrompt(summaryResults, "Be brief"); got != want {
		t.Fatalf("%q", got)
	}
	got := BuildSummaryPrompt([]QueryResultData{
		{Query: "failing", Error: new("boom")},
		{Query: "bare"},
	}, "")
	wantResults := "\n<search_results>\n\n[Result 1]\nQuery: failing\nStatus: Error\nError: boom\n\n[Result 2]\nQuery: bare\nProvider: unknown\nAnswer: (no answer text returned)\nSources: none\n\n</search_results>"
	if got != head+wantResults {
		t.Fatalf("%q", got)
	}
}

func TestBuildDeterministicSummary(t *testing.T) {
	empty := BuildDeterministicSummary(nil)
	if empty.Summary != "No completed search results were available when the curator session finished.\n\nSources\n- None" ||
		empty.Meta != (SummaryMeta{TokenEstimate: 24, FallbackUsed: true, FallbackReason: "deterministic-submit-fallback", Phase: "deterministic-fallback"}) {
		t.Fatalf("%+v", empty)
	}

	draft := BuildDeterministicSummary([]QueryResultData{
		{Query: "q1", Answer: "Fast   answer.\nSources: a, b", Results: []SearchResult{{URL: "https://a.test/1"}, {URL: "https://a.test/2"}}},
		{Query: "q2", Error: new("boom")},
		{Query: "q3", Results: []SearchResult{{URL: "https://a.test/2"}}},
		{Query: "q4"},
	})
	want := "Summary based on the currently selected search results.\n\n- q1: Fast answer.\n- q2: failed (boom)\n- q3: returned 1 source without answer text.\n- q4: returned 0 sources without answer text.\n\n" +
		"Completed queries: 4\nSuccessful: 3\nFailed: 1\n\nSources\n- https://a.test/1\n- https://a.test/2"
	if draft.Summary != want || draft.Meta.TokenEstimate != 70 || draft.Meta.Model != nil || draft.Meta.DurationMs != 0 || draft.Meta.Edited {
		t.Fatalf("%q %+v", draft.Summary, draft.Meta)
	}

	// An answer that only opens with a source list has no preview; a long one is cut at 240 characters.
	draft = BuildDeterministicSummary([]QueryResultData{
		{Query: "only sources", Answer: "Sources: x", Results: []SearchResult{{URL: "https://a.test/s"}}},
		{Query: "long", Answer: strings.Repeat("x", 300)},
	})
	if !strings.Contains(draft.Summary, "- only sources: returned 1 source without answer text.\n") ||
		!strings.Contains(draft.Summary, "- long: "+strings.Repeat("x", 237)+"...\n") {
		t.Fatal(draft.Summary)
	}

	// At most twelve sources are listed.
	var many []SearchResult
	for i := 1; i <= 14; i++ {
		many = append(many, SearchResult{URL: "https://a.test/" + strconv.Itoa(i)})
	}
	draft = BuildDeterministicSummary([]QueryResultData{{Query: "many", Results: many}})
	if !strings.HasSuffix(draft.Summary, "- https://a.test/12\n- ... and 2 more") || strings.Contains(draft.Summary, "https://a.test/13\n") {
		t.Fatal(draft.Summary)
	}
}

// How a generation ends when no model is usable or the model fails (summary-review.ts:386-445).
func TestGenerateSummaryDraftFallbackReasons(t *testing.T) {
	side, reg, _ := summarySide()
	cases := []struct {
		name     string
		override string
		complete func(CompleteOptions) (map[string]any, error)
		reason   string
	}{
		{"invalid override", "no-slash", nil, "summary-model-settings-error: Invalid summary model: no-slash. Use provider/model-id."},
		{"empty answer", "", func(CompleteOptions) (map[string]any, error) {
			return map[string]any{"stopReason": "stop", "content": []any{map[string]any{"type": "thinking", "thinking": "hm"}, map[string]any{"text": "  "}}}, nil
		}, "summary-model-unavailable: Summary model returned empty response (content parts: thinking, unknown)"},
		{"no parts", "", func(CompleteOptions) (map[string]any, error) {
			return map[string]any{"stopReason": "stop", "content": []any{}}, nil
		}, "summary-model-unavailable: Summary model returned empty response (content parts: none)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var options SummaryOptions
			options.ModelOverride = c.override
			options.DeadlineMs = 1000
			if c.complete != nil {
				reg.completeFn = nil
				options.Complete = func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
					return c.complete(o)
				}
			}
			draft, err := GenerateSummaryDraft(bg(), summaryResults, side, options)
			noErr(t, err)
			if !draft.Meta.FallbackUsed || draft.Meta.FallbackReason != c.reason || draft.Meta.Phase != "deterministic-fallback" {
				t.Fatalf("%+v", draft.Meta)
			}
		})
	}

	// A refusal part counts as text.
	draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{DeadlineMs: 1000, Complete: func(_ context.Context, _, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
		return map[string]any{"stopReason": "stop", "content": []any{map[string]any{"refusal": "I cannot"}}}, nil
	}})
	noErr(t, err)
	if draft.Summary != "I cannot" || draft.Meta.FallbackUsed {
		t.Fatalf("%+v", draft)
	}

	// A completion that ends "aborted" is the caller's cancellation.
	_, err = GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{DeadlineMs: 1000, Complete: func(_ context.Context, _, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
		return map[string]any{"stopReason": "aborted"}, nil
	}})
	wantErr(t, err, "^Aborted$")
}

func TestClampThinkingLevel(t *testing.T) {
	plain := map[string]any{"reasoning": true}
	mapped := map[string]any{"reasoning": true, "thinkingLevelMap": map[string]any{"minimal": nil, "xhigh": "x-high", "max": nil}}
	off := map[string]any{"reasoning": false}
	for _, c := range []struct {
		model map[string]any
		level string
		want  string
	}{
		{plain, "low", "low"}, {plain, "high", "high"}, {plain, "off", "off"},
		{plain, "xhigh", "high"}, {plain, "max", "high"}, {plain, "bogus", "off"},
		{mapped, "minimal", "low"}, {mapped, "xhigh", "xhigh"}, {mapped, "max", "xhigh"},
		{off, "high", "off"}, {off, "off", "off"},
	} {
		if got := clampThinkingLevel(c.model, c.level); got != c.want {
			t.Errorf("%v %s: %s, want %s", c.model, c.level, got, c.want)
		}
	}
}
