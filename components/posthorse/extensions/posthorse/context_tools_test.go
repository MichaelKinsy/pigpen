package posthorse

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func TestContextReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy fakePolicy
		want   string
	}{
		{"unknown usage", fakePolicy{Settings: compactionPolicy{enabled: true, reserveTokens: 16_384}},
			"Context usage is not known until the next model response."},
		{"unknown tokens", func() fakePolicy {
			p := budgetPolicy(100_000, 0, 16_384, true)
			p.Usage.tokens = nil
			return p
		}(), "Context usage is not known until the next model response."},
		{"enabled", func() fakePolicy {
			p := budgetPolicy(272_000, 205_000, 64_000, true)
			p.Usage.percent = new(75.4)
			return p
		}(), "≈3,001 tokens until automatic rollover (line at 208,001); ≈67,000 tokens until the configured context limit (205,000/272,000 used, 75%). Best available native estimate."},
		{"enabled, past the line", func() fakePolicy {
			p := budgetPolicy(100_000, 90_000, 16_384, true)
			p.Usage.percent = new(90.0)
			return p
		}(), "≈0 tokens until automatic rollover (line at 83,617); ≈10,000 tokens until the configured context limit (90,000/100,000 used, 90%). Best available native estimate."},
		{"disabled", func() fakePolicy {
			p := budgetPolicy(100_000, 99_000, 16_384, false)
			p.Usage.percent = new(99.0)
			return p
		}(), "Automatic rollover is disabled in the available settings (Pi compaction.enabled=false). ≈1,000 tokens until the configured context limit (99,000/100,000 used, 99%). Best available native estimate."},
		{"unsupported", func() fakePolicy {
			p := budgetPolicy(4096, 3596, 16_384, true)
			p.Usage.percent = new(87.7900390625)
			return p
		}(), unsupportedMessage(&budget{contextWindow: 4096, reserveTokens: 16_384, usable: -12_288}) + " ≈500 tokens until the configured context limit (3,596/4,096 used, 88%). Best available native estimate."},
		{"missing percent counts as 0", budgetPolicy(100_000, 36_000, 64_000, true),
			"≈1 tokens until automatic rollover (line at 36,001); ≈64,000 tokens until the configured context limit (36,000/100,000 used, 0%). Best available native estimate."},
		{"rounding goes up at .5", func() fakePolicy {
			p := budgetPolicy(100_000, 36_000, 64_000, true)
			p.Usage.percent = new(35.5)
			return p
		}(), "≈1 tokens until automatic rollover (line at 36,001); ≈64,000 tokens until the configured context limit (36,000/100,000 used, 36%). Best available native estimate."},
	} {
		got, err := contextReport(tc.policy)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.Content != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got.Content, tc.want)
		}
		if display, _ := got.Details.(*posthorseDisplay); display == nil || display.kind != "context" {
			t.Errorf("%s: details = %+v", tc.name, got.Details)
		}
	}
}

func TestContextToolSchemasEqualTypeBox(t *testing.T) {
	// The JSON `JSON.stringify(Type.Object(...))` writes under Pi's TypeBox (verified with Pi 1.5.0's typebox).
	handoff := sdk.Type.Object(sdk.NewOrderedObject("handoff", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
		"description", "What the fresh window needs to continue, as readable prose",
		"maxLength", maxHandoffChars,
	)))))
	got, err := sdkjson.MarshalStringify(handoff)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"object","properties":{"handoff":{"type":"string","description":"What the fresh window needs to continue, as readable prose","maxLength":20000}}}`; string(got) != want {
		t.Errorf("new_context schema:\n got %s\nwant %s", got, want)
	}
	empty, _ := sdkjson.MarshalStringify(sdk.Type.Object(sdk.NewOrderedObject()))
	if string(empty) != `{"type":"object","properties":{}}` {
		t.Errorf("get_context_remaining schema: %s", empty)
	}
}

func TestToolRegistrationOrder(t *testing.T) {
	f := newFixture(t)
	names := f.ToolNames()
	want := []string{"new_context", "get_context_remaining", "notes", "history"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tool order = %v, want %v", names, want)
	}
}

func TestHandlerRegistrationOrder(t *testing.T) {
	f := newFixture(t)
	events := f.RegisteredEvents()
	// index.ts:55-63 and rollover.ts:393-456: the reset-request hooks, turn/session resets, guidance,
	// the reminder filter, automatic rollover, turn_end; then the notes store's session_start and
	// the note guards. notes and history register tools only.
	want := []string{
		"tool_execution_start", "tool_execution_end", "turn_start", "session_start", "session_tree",
		"before_agent_start", "agent_settled", "context_with_system", "context", "session_before_compact", "turn_end",
		"session_start", "tool_call", "tool_result", "tool_execution_end",
	}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Errorf("registered events = %v\n                 want %v", events, want)
	}
	if f.HandlerCount("session_start") < 2 {
		t.Errorf("rollover and the notes store each hold a session_start handler, got %d", f.HandlerCount("session_start"))
	}
}

func TestNewContextTool(t *testing.T) {
	f := newFixture(t)
	result := f.Run("new_context", rec{"handoff": "  carry on  "})
	if result.Text() != "Requested a fresh Pi context after the current foreground tools succeed. Background work continues across the reset. Earlier conversation stays in session history." {
		t.Errorf("text = %q", result.Text())
	}
	if kind, _ := result.Details["kind"].(string); kind != "new-context" {
		t.Errorf("details = %v", result.Details)
	}
	handoff := f.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"})
	entries, _ := handoff["entries"].([]any)
	if len(entries) == 0 || entries[0].(map[string]any)["summary"] != "carry on" {
		t.Errorf("the trimmed handoff reaches the boundary: %v", handoff)
	}
}

func TestNewContextEmptyHandoffUsesTheDefault(t *testing.T) {
	f := newFixture(t)
	f.Run("new_context", rec{"handoff": "   "})
	handoff := f.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"})
	entries, _ := handoff["entries"].([]any)
	if len(entries) == 0 || entries[0].(map[string]any)["summary"] != emptyHandoff {
		t.Errorf("an empty handoff becomes the default: %v", handoff)
	}
}

func TestNewContextRejectsATooLargeHandoff(t *testing.T) {
	f := newFixture(t)
	ctx := coreUsage(f, 8_192, 100, 16_384, false) // the configured limit bounds the handoff well below 20,000
	_, failure := ctx.RunErr("new_context", rec{"handoff": strings.Repeat("x", 19_000)})
	if !strings.HasPrefix(failure, "Handoff is too large for the active model (19,000 characters; limit ") || !strings.HasSuffix(failure, "). Save fuller state in notes, then retry with a shorter handoff or no handoff.") {
		t.Errorf("failure = %q", failure)
	}
}

func BenchmarkContextReport(b *testing.B) {
	p := budgetPolicy(272_000, 205_000, 64_000, true)
	b.ReportAllocs()
	for range b.N {
		_, _ = contextReport(p)
	}
}
