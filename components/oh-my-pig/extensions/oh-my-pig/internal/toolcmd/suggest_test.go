package toolcmd

import (
	"encoding/json"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/agent/test/agent-loop.test.ts: "suggests the intended tool when a miss shares its trailing segment"
// upstream: packages/agent/test/agent-loop.test.ts: "suggests a mounted tool command the advertised set does not contain"
// upstream: packages/agent/test/agent-loop.test.ts: "ranks the distinctive tail ahead of tools sharing only the generic one"
func TestSuggestToolNameMatchesOmp(t *testing.T) {
	// 1. Trailing `_` and `__` segments against advertised tools.
	adv := []string{"read", "grep", "mcp__context_resolve_library_id"}
	if got := formatToolNotFoundMessage("mcp__abc123__xyz789_read", adv, nil); got != "Tool mcp__abc123__xyz789_read not found. Did you mean read?" {
		t.Fatalf("got %q", got)
	}
	if got := formatToolNotFoundMessage("mcp__context7__resolve_library_id", adv, nil); got != "Tool mcp__context7__resolve_library_id not found. Did you mean mcp__context_resolve_library_id?" {
		t.Fatalf("got %q", got)
	}
	if got := formatToolNotFoundMessage("my__grep", adv, nil); got != "Tool my__grep not found. Did you mean grep?" {
		t.Fatalf("got %q", got)
	}
	if got := formatToolNotFoundMessage("fake_read", adv, nil); got != "Tool fake_read not found. Did you mean read?" {
		t.Fatalf("got %q", got)
	}
	if got := formatToolNotFoundMessage("totally_unrelated", adv, nil); got != "Tool totally_unrelated not found" {
		t.Fatalf("got %q", got)
	}

	// 2. Segments shorter than 3 chars (`id`, `to`) are ignored as noise.
	if got := formatToolNotFoundMessage("foo_id", []string{"id", "other_id"}, nil); got != "Tool foo_id not found" {
		t.Fatalf("short segment should not suggest, got %q", got)
	}

	// 3. Mounted tool command (not in advertised) suggested via fallback list.
	if got := formatToolNotFoundMessage("mcp__nsnsnsnsnsns__ididididid_github", []string{"read"}, []string{"github", "lsp"}); got != "Tool mcp__nsnsnsnsnsns__ididididid_github not found. Did you mean github?" {
		t.Fatalf("mounted suggestion failed, got %q", got)
	}

	// 4. Distinctive `__` tail ranks ahead of generic `_` tail and truncates to 3.
	rankTools := []string{"alpha_get", "beta_get", "gamma_get", "mcp__context_resolve_library_get"}
	wantRanked := "Tool bad__resolve_library_get not found. Closest available: mcp__context_resolve_library_get, alpha_get, beta_get"
	if got := formatToolNotFoundMessage("bad__resolve_library_get", rankTools, nil); got != wantRanked {
		t.Fatalf("ranked suggestions =\n got: %q\nwant: %q", got, wantRanked)
	}

	// 5. Advertised tools rank ahead of mounted tools when both share a segment.
	if got := formatToolNotFoundMessage("x__edit", []string{"edit"}, []string{"ast_edit"}); got != "Tool x__edit not found. Closest available: edit, ast_edit" {
		t.Fatalf("advertised must rank ahead of mounted, got %q", got)
	}
}

// TestEventContextRewritesUnknownToolResultsInPlace verifies Allowlist Audit
// finding 7 end to end through the SDK `EventContext` wire handler:
//   - PiG's bare `Tool my__grep not found` and `Tool fake_read not found` error
//     toolResult messages are rewritten in place to omp's `Did you mean ...?`
//     text before reaching the model, while keeping `_pigContextUnchanged: true`
//     so PiG preserves message-list identity and positional system messages.
//   - Mounted commands (e.g. `lsp`) are included after advertised tools.
//   - Non-error results, unrelated error messages, and unknown names with no
//     matching segment are left untouched.
func TestEventContextRewritesUnknownToolResultsInPlace(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	Mountable("lsp", func(sdk.Context) bool { return true })
	Mountable("ast_edit", func(sdk.Context) bool { return true })

	ext := sdk.New("test-suggest-context")
	Register(ext)
	var directSuggest string
	ext.OnEvent("probe_suggest", func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["name"].(string)
		directSuggest = SuggestToolName(ctx, name)
		return nil, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "suggest-ctx-1",
		allTools: []sdk.ToolInfo{
			{Name: "read", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "bash", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "edit", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "grep", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "lsp", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "ast_edit", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		activeTools: []string{"read", "bash", "edit", "grep"},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	// Verify exported SuggestToolName directly against live session state.
	if _, errMsg := host.emitEvent("probe_suggest", map[string]any{"name": "mcp__ns__x_lsp"}); errMsg != "" {
		t.Fatalf("probe_suggest: %s", errMsg)
	}
	if directSuggest != "Tool mcp__ns__x_lsp not found. Did you mean lsp?" {
		t.Fatalf("SuggestToolName(mcp__ns__x_lsp) = %q", directSuggest)
	}

	// Emit `context` with PiG's host-generated toolResult messages.
	messages := []any{
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "c1",
			"toolName":   "my__grep",
			"isError":    true,
			"content":    []any{map[string]any{"type": "text", "text": "Tool my__grep not found"}},
		},
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "c2",
			"toolName":   "fake_read",
			"isError":    true,
			"content":    []any{map[string]any{"type": "text", "text": "Tool fake_read not found"}},
		},
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "c3",
			"toolName":   "bad__lsp",
			"isError":    true,
			"content":    []any{map[string]any{"type": "text", "text": "Tool bad__lsp not found"}},
		},
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "c4",
			"toolName":   "totally_unrelated",
			"isError":    true,
			"content":    []any{map[string]any{"type": "text", "text": "Tool totally_unrelated not found"}},
		},
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "c5",
			"toolName":   "my__grep",
			"isError":    false,
			"content":    []any{map[string]any{"type": "text", "text": "Tool my__grep not found"}},
		},
	}

	raw, errMsg := host.emitEvent(sdk.EventContext, map[string]any{"messages": messages})
	if errMsg != "" {
		t.Fatalf("context event failed: %s", errMsg)
	}
	var reply map[string]any
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("unmarshal context reply: %v (%s)", err, raw)
	}
	if reply["_pigContextUnchanged"] != true {
		t.Fatalf("expected _pigContextUnchanged=true for in-place edit, got %s", raw)
	}
	outMsgs, ok := reply["messages"].([]any)
	if !ok || len(outMsgs) != len(messages) {
		t.Fatalf("expected %d messages in reply, got %s", len(messages), raw)
	}

	textOf := func(idx int) string {
		m := outMsgs[idx].(map[string]any)
		blocks := m["content"].([]any)
		return blocks[0].(map[string]any)["text"].(string)
	}
	wantTexts := []string{
		"Tool my__grep not found. Did you mean grep?",
		"Tool fake_read not found. Did you mean read?",
		"Tool bad__lsp not found. Did you mean lsp?",
		"Tool totally_unrelated not found",
		"Tool my__grep not found", // non-error untouched
	}
	for i, want := range wantTexts {
		if got := textOf(i); got != want {
			t.Errorf("msg[%d] text = %q, want %q", i, got, want)
		}
	}
}
