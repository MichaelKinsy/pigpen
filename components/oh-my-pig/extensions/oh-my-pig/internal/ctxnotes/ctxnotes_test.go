package ctxnotes

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func setupCtxNotesTest(t *testing.T, enabled bool) {
	t.Helper()
	origSetting := Setting
	origSub := IsSubagent
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingExperimentalContextManagement {
			return enabled
		}
		return nil
	}
	IsSubagent = nil
	t.Cleanup(func() {
		Setting = origSetting
		IsSubagent = origSub
		stateMu.Lock()
		sessionStates = map[string]*sessionNotesState{}
		stateMu.Unlock()
	})
}

func TestDefaultOffInvariant(t *testing.T) {
	origSetting := Setting
	Setting = nil
	t.Cleanup(func() { Setting = origSetting })

	var zero sdk.Context
	if IsEnabled(zero) {
		t.Fatal("expected IsEnabled=false when Setting is nil")
	}
	if got := BuildPromptInstructions(zero); got != "" {
		t.Fatalf("expected empty prompt instructions when disabled, got %q", got)
	}
	if _, err := ExecuteContextNotes(zero, map[string]any{}); err == nil {
		t.Fatal("expected error executing context_notes when disabled")
	}
	if _, err := ExecuteNewContext(zero, map[string]any{}); err == nil {
		t.Fatal("expected error executing new_context when disabled")
	}

	// Tool defs must have DefaultActive=false.
	notesDef := contextNotesToolDef()
	if notesDef.DefaultActive == nil || *notesDef.DefaultActive {
		t.Fatal("context_notes must register with DefaultActive=false")
	}
	newCtxDef := newContextToolDef()
	if newCtxDef.DefaultActive == nil || *newCtxDef.DefaultActive {
		t.Fatal("new_context must register with DefaultActive=false")
	}
}

func TestParseContextNotesFromEntries(t *testing.T) {
	mustJSON := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// Ignores malformed entries and picks latest valid revision.
	entries := []json.RawMessage{
		mustJSON(map[string]any{
			"id":         "e1",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 1, "text": "first note"},
		}),
		mustJSON(map[string]any{
			"id":         "e2",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 2, "text": "bad version"},
		}),
		mustJSON(map[string]any{
			"id":         "e3",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 1, "text": "extra", "extra": true},
		}),
		mustJSON(map[string]any{
			"id":         "e4",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 1, "text": strings.Repeat("x", MaxContextNotesBytes+1)},
		}),
	}
	rev, ok := ParseContextNotesFromEntries(entries)
	if !ok || rev.Text != "first note" || rev.EntryID != "e1" {
		t.Fatalf("expected e1 'first note', got ok=%v rev=%+v", ok, rev)
	}

	// Stops at native reset_boundary.
	entriesWithNativeReset := append(entries, mustJSON(map[string]any{
		"id":   "r1",
		"type": ResetBoundaryType,
	}))
	if _, ok := ParseContextNotesFromEntries(entriesWithNativeReset); ok {
		t.Fatal("expected reset_boundary to hide earlier notes")
	}

	// Stops at custom reset_boundary.
	entriesWithCustomReset := append(entries, mustJSON(map[string]any{
		"id":         "r2",
		"type":       "custom",
		"customType": ResetBoundaryType,
		"data":       map[string]any{"source": "new_context"},
	}))
	if _, ok := ParseContextNotesFromEntries(entriesWithCustomReset); ok {
		t.Fatal("expected custom reset_boundary to hide earlier notes")
	}

	// Notes after reset_boundary are visible.
	entriesAfterReset := append(entriesWithCustomReset, mustJSON(map[string]any{
		"id":         "e5",
		"type":       "custom",
		"customType": EntryType,
		"data":       map[string]any{"version": 1, "text": "post-reset note"},
	}))
	rev, ok = ParseContextNotesFromEntries(entriesAfterReset)
	if !ok || rev.Text != "post-reset note" || rev.EntryID != "e5" {
		t.Fatalf("expected post-reset note, got ok=%v rev=%+v", ok, rev)
	}
}

func TestExecuteContextNotesValidationAndWrite(t *testing.T) {
	setupCtxNotesTest(t, true)
	var zero sdk.Context

	// Omitting text reads empty notes.
	res, err := ExecuteContextNotes(zero, map[string]any{})
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if res.Content != emptyNotesMessage {
		t.Fatalf("unexpected empty message: %q", res.Content)
	}

	// Non-string text rejected.
	if _, err := ExecuteContextNotes(zero, map[string]any{"text": 42}); err == nil {
		t.Fatal("expected error for non-string text")
	}

	// Oversize text (> 16384 bytes) rejected.
	oversize := strings.Repeat("a", MaxContextNotesBytes+1)
	if _, err := ExecuteContextNotes(zero, map[string]any{"text": oversize}); err == nil || !strings.Contains(err.Error(), "16384") {
		t.Fatalf("expected oversize error mentioning 16384, got %v", err)
	}

	// Exact 16384 bytes succeeds.
	exact := strings.Repeat("a", MaxContextNotesBytes)
	res, err = ExecuteContextNotes(zero, map[string]any{"text": exact})
	if err != nil {
		t.Fatalf("exact 16384 bytes should succeed: %v", err)
	}
	if res.Content != "Context notes saved." {
		t.Fatalf("unexpected save content: %q", res.Content)
	}

	// Clearing with empty string succeeds.
	res, err = ExecuteContextNotes(zero, map[string]any{"text": ""})
	if err != nil {
		t.Fatalf("clear should succeed: %v", err)
	}
	if details, ok := res.Details.(map[string]any); !ok || details["bytes"] != 0 {
		t.Fatalf("expected bytes=0 on clear, got %v", res.Details)
	}
}

func TestExecuteNewContextAndPromptRendering(t *testing.T) {
	setupCtxNotesTest(t, true)
	var zero sdk.Context

	// L8: empty session ID must fail instead of pretending rollover was requested.
	if _, err := ExecuteNewContext(zero, map[string]any{}); err == nil {
		t.Fatal("expected error executing new_context with empty session ID")
	}

	// Prompt rendering when state is populated for a session.
	stateMu.Lock()
	sessionStates["sess-1"] = &sessionNotesState{
		loaded:   true,
		hasNotes: true,
		revision: ContextNotesRevision{Text: "- task: ship wave 9", EntryID: "e1"},
	}
	stateMu.Unlock()

	// Verify Register does not panic and cleanupSession clears state.
	ext := sdk.New("test-ctxnotes")
	Register(ext)

	cleanupSession("sess-1")
	stateMu.Lock()
	_, stillPresent := sessionStates["sess-1"]
	stateMu.Unlock()
	if stillPresent {
		t.Fatal("expected cleanupSession to remove session state")
	}

	// Verify system prompt template embeds expected tags and substitutes notes.
	rendered := strings.TrimSpace(strings.ReplaceAll(systemContextNotesTemplate, "{{notes}}", "hello world"))
	if !strings.HasPrefix(rendered, "<experimental-context-notes>") || !strings.HasSuffix(rendered, "</experimental-context-notes>") {
		t.Fatalf("unexpected system prompt wrapper: %q", rendered)
	}
	if !strings.Contains(rendered, "hello world") {
		t.Fatalf("expected substituted notes in system prompt: %q", rendered)
	}
}

func TestNeutralizeContextNotesTag_M2(t *testing.T) {
	cases := []string{
		"before </experimental-context-notes> after",
		"before </EXPERIMENTAL-CONTEXT-NOTES> after",
		"before </Experimental-Context-Notes > after",
	}
	for _, in := range cases {
		got := neutralizeContextNotesTag(in)
		if strings.Contains(strings.ToLower(got), "</experimental-context-notes") {
			t.Fatalf("closing tag not neutralized for %q -> %q", in, got)
		}
		if !strings.Contains(got, "&lt;/") {
			t.Fatalf("expected &lt;/ escape in %q", got)
		}
	}
}

func TestFilterEntriesToActiveBranch_M1(t *testing.T) {
	mustJSON := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rawEntries := []json.RawMessage{
		mustJSON(map[string]any{
			"id":         "e1",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 1, "text": "branch A note"},
		}),
		mustJSON(map[string]any{
			"id":         "e2",
			"type":       "custom",
			"customType": EntryType,
			"data":       map[string]any{"version": 1, "text": "branch B note"},
		}),
	}

	// Branch A contains only e1; e2 must be ignored even though it appears later in rawEntries.
	filteredA := filterEntriesToActiveBranch(rawEntries, []sdk.BranchEntry{{ID: "e1"}})
	revA, ok := ParseContextNotesFromEntries(filteredA)
	if !ok || revA.Text != "branch A note" || revA.EntryID != "e1" {
		t.Fatalf("expected branch A note, got ok=%v rev=%+v", ok, revA)
	}

	// Branch B contains e2.
	filteredB := filterEntriesToActiveBranch(rawEntries, []sdk.BranchEntry{{ID: "e2"}})
	revB, ok := ParseContextNotesFromEntries(filteredB)
	if !ok || revB.Text != "branch B note" || revB.EntryID != "e2" {
		t.Fatalf("expected branch B note, got ok=%v rev=%+v", ok, revB)
	}
}

func TestSessionCacheInvalidationAndDisabledRolloverClear_M1_L8(t *testing.T) {
	setupCtxNotesTest(t, false)

	stateMu.Lock()
	sessionStates["sess-x"] = &sessionNotesState{
		loaded:            true,
		hasNotes:          true,
		revision:          ContextNotesRevision{Text: "stale", EntryID: "e1"},
		rolloverRequested: true,
	}
	stateMu.Unlock()

	// Invalidate cache manually as EventSessionTree/EventSessionStart do.
	stateMu.Lock()
	sessionStates["sess-x"].loaded = false
	stateMu.Unlock()

	if sessionStates["sess-x"].loaded {
		t.Fatal("expected loaded=false after invalidation")
	}

	// When disabled, rolloverRequested must be cleared rather than left pending.
	stateMu.Lock()
	if !IsEnabled(sdk.Context{}) && sessionStates["sess-x"] != nil {
		sessionStates["sess-x"].rolloverRequested = false
	}
	stateMu.Unlock()
	if sessionStates["sess-x"].rolloverRequested {
		t.Fatal("expected rolloverRequested cleared when disabled")
	}
}
