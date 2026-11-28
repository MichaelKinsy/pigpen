package modes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestWave5ForceCommandArmsWireToolChoiceAndClearsOnSessionEnd(t *testing.T) {
	origSet := SetForcedToolChoice
	origClear := ClearForcedToolChoice
	t.Cleanup(func() {
		SetForcedToolChoice = origSet
		ClearForcedToolChoice = origClear
	})

	var mu sync.Mutex
	var armed []string
	var cleared []string
	SetForcedToolChoice = func(sessionID, toolName string) {
		mu.Lock()
		armed = append(armed, sessionID+":"+toolName)
		mu.Unlock()
	}
	ClearForcedToolChoice = func(sessionID string) {
		mu.Lock()
		cleared = append(cleared, sessionID)
		mu.Unlock()
	}

	_, h := newTestExtension(t, hostOptions{
		SessionID:   "sess-force-wire",
		ActiveTools: []string{"read", "bash", "edit"},
	})

	// 1. `/force bash` invokes SetForcedToolChoice and keeps before_agent_start fallback.
	h.callCommand("force", "bash")
	mu.Lock()
	gotArmed := append([]string(nil), armed...)
	mu.Unlock()
	if len(gotArmed) != 1 || gotArmed[0] != "sess-force-wire:bash" {
		t.Fatalf("expected SetForcedToolChoice(sess-force-wire, bash), got %v", gotArmed)
	}
	res := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPrompt":        "",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	if !strings.Contains(string(res), "You MUST invoke the `bash` tool on this turn.") {
		t.Fatalf("expected forced-tool-notice fallback, got %s", string(res))
	}

	// 2. `/force:edit do it` colon syntax also arms SetForcedToolChoice.
	h.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "/force:edit fix the bug",
		"source": "interactive",
	})
	mu.Lock()
	gotArmed = append([]string(nil), armed...)
	mu.Unlock()
	if len(gotArmed) != 2 || gotArmed[1] != "sess-force-wire:edit" {
		t.Fatalf("expected second SetForcedToolChoice(sess-force-wire, edit), got %v", gotArmed)
	}

	// 3. Inactive tool is rejected and does NOT arm SetForcedToolChoice.
	h.callCommand("force", "nonexistent")
	mu.Lock()
	if len(armed) != 2 {
		mu.Unlock()
		t.Fatalf("inactive tool should not arm SetForcedToolChoice, got %v", armed)
	}
	mu.Unlock()

	// 4. Session shutdown clears forced tool choice via lifecycle.
	h.fireEvent(sdk.EventSessionShutdown, map[string]any{})
	mu.Lock()
	gotCleared := append([]string(nil), cleared...)
	mu.Unlock()
	if len(gotCleared) != 1 || gotCleared[0] != "sess-force-wire" {
		t.Fatalf("expected ClearForcedToolChoice(sess-force-wire), got %v", gotCleared)
	}
}

func TestWave5SwitchCommandRoleSelectors(t *testing.T) {
	origResolve := ResolveRole
	t.Cleanup(func() { ResolveRole = origResolve })

	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		switch selector {
		case "@smol":
			return "anthropic", "claude-haiku-4-5", "", true
		case "@slow:high":
			return "openai", "gpt-5.6-sol", "high", true
		case "pi/plan":
			return "anthropic", "claude-opus-5", "max", true
		case "*":
			return "anthropic", "claude-sonnet-4-5", "", true
		default:
			return "", "", "", false
		}
	}

	var switchedModels []string
	_, h := newTestExtension(t, hostOptions{
		SessionID:     "sess-switch-roles",
		ThinkingLevel: "medium",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "resolveModel" {
				spec, _ := args["spec"].(string)
				switchedModels = append(switchedModels, spec)
				parts := strings.SplitN(spec, "/", 2)
				prov, id := "", spec
				if len(parts) == 2 {
					prov, id = parts[0], parts[1]
				}
				return map[string]any{"model": map[string]any{"provider": prov, "id": id}}, ""
			}
			return nil, ""
		},
	})

	// 1. `/switch @smol`
	h.callCommand("switch", "@smol")
	if len(switchedModels) != 1 || switchedModels[0] != "anthropic/claude-haiku-4-5" {
		t.Fatalf("expected switch to anthropic/claude-haiku-4-5, got %v (notify=%q)", switchedModels, h.lastNotify())
	}
	if got := h.lastNotify(); got != "Model switched to anthropic/claude-haiku-4-5." {
		t.Fatalf("unexpected notify: %q", got)
	}

	// 2. `/switch @slow:high` applies explicit thinking suffix.
	h.callCommand("switch", "@slow:high")
	if len(switchedModels) != 2 || switchedModels[1] != "openai/gpt-5.6-sol" {
		t.Fatalf("expected switch to openai/gpt-5.6-sol, got %v", switchedModels)
	}
	h.mu.Lock()
	lvl := h.thinkingLevel
	h.mu.Unlock()
	if lvl != "high" {
		t.Fatalf("expected thinking level high, got %q", lvl)
	}

	// 3. `/switch pi/plan` applies role-derived thinking level ("max").
	h.callCommand("switch", "pi/plan")
	if len(switchedModels) != 3 || switchedModels[2] != "anthropic/claude-opus-5" {
		t.Fatalf("expected switch to anthropic/claude-opus-5, got %v", switchedModels)
	}
	h.mu.Lock()
	lvl = h.thinkingLevel
	h.mu.Unlock()
	if lvl != "max" {
		t.Fatalf("expected thinking level max, got %q", lvl)
	}

	// 4. `/switch *`
	h.callCommand("switch", "*")
	if len(switchedModels) != 4 || switchedModels[3] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("expected switch to anthropic/claude-sonnet-4-5, got %v", switchedModels)
	}

	// 5. Unknown role fails cleanly.
	h.callCommand("switch", "@nonexistent")
	if got := h.lastNotify(); got != "Unknown model: @nonexistent" {
		t.Fatalf("expected Unknown model error, got %q", got)
	}
}

func TestWave5PlanModeRoleSwitchAndRestore(t *testing.T) {
	origSetting := Setting
	origResolve := ResolveRole
	t.Cleanup(func() {
		Setting = origSetting
		ResolveRole = origResolve
	})

	// 1. Default session (modelRoles.plan unset) MUST NOT switch models on /plan.
	Setting = func(_ sdk.Context, key string) any {
		if key == "modelRoles" {
			return map[string]any{"smol": "anthropic/claude-haiku-4-5"}
		}
		return nil
	}
	resolveCalled := false
	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		resolveCalled = true
		return "anthropic", "claude-opus-5", "max", true
	}

	var defaultSwitched []string
	_, hDefault := newTestExtension(t, hostOptions{
		SessionID:     "sess-plan-default",
		ModelProvider: "anthropic",
		Model:         "claude-sonnet-4-5",
		ThinkingLevel: "medium",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "resolveModel" {
				spec, _ := args["spec"].(string)
				defaultSwitched = append(defaultSwitched, spec)
				return map[string]any{"model": map[string]any{"id": spec, "provider": "anthropic"}}, ""
			}
			return nil, ""
		},
	})
	hDefault.callCommand("plan", "")
	if resolveCalled || len(defaultSwitched) != 0 {
		t.Fatalf("default session without modelRoles.plan must not switch models: resolveCalled=%v switched=%v", resolveCalled, defaultSwitched)
	}

	// 2. Configured modelRoles.plan switches on enter and restores on exit.
	Setting = func(_ sdk.Context, key string) any {
		if key == "modelRoles" {
			return map[string]any{"plan": "anthropic/claude-opus-5:xhigh"}
		}
		return nil
	}
	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		if selector == "@plan" {
			return "anthropic", "claude-opus-5", "xhigh", true
		}
		return "", "", "", false
	}

	var switched []string
	m, h := newTestExtension(t, hostOptions{
		SessionID:     "sess-plan-role",
		ModelProvider: "anthropic",
		Model:         "claude-sonnet-4-5",
		ThinkingLevel: "medium",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "resolveModel" {
				spec, _ := args["spec"].(string)
				switched = append(switched, spec)
				parts := strings.SplitN(spec, "/", 2)
				prov, id := "anthropic", spec
				if len(parts) == 2 {
					prov, id = parts[0], parts[1]
				}
				return map[string]any{"model": map[string]any{"provider": prov, "id": id}}, ""
			}
			return nil, ""
		},
	})

	h.callCommand("plan", "")
	if len(switched) != 1 || switched[0] != "anthropic/claude-opus-5" {
		t.Fatalf("expected switch to anthropic/claude-opus-5 on enterPlanMode, got %v", switched)
	}
	h.mu.Lock()
	lvl := h.thinkingLevel
	h.mu.Unlock()
	if lvl != "xhigh" {
		t.Fatalf("expected thinking level xhigh in plan mode, got %q", lvl)
	}
	m.mu.Lock()
	st := m.stateLocked("sess-plan-role")
	if st.planPrevModel != "anthropic/claude-sonnet-4-5" || st.planPrevThinking != "medium" {
		m.mu.Unlock()
		t.Fatalf("unexpected saved pre-plan state: model=%q thinking=%q", st.planPrevModel, st.planPrevThinking)
	}
	m.mu.Unlock()

	// Exit plan mode -> restores previous model & thinking level.
	h.callCommand("plan", "")
	if len(switched) != 2 || switched[1] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("expected restore to anthropic/claude-sonnet-4-5 on exitPlanMode, got %v", switched)
	}
	h.mu.Lock()
	lvl = h.thinkingLevel
	h.mu.Unlock()
	if lvl != "medium" {
		t.Fatalf("expected thinking level restored to medium, got %q", lvl)
	}
	m.mu.Lock()
	st = m.stateLocked("sess-plan-role")
	if st.planPrevModel != "" || st.planPrevThinking != "" {
		m.mu.Unlock()
		t.Fatalf("expected pre-plan state cleared on exit, got model=%q thinking=%q", st.planPrevModel, st.planPrevThinking)
	}
	m.mu.Unlock()

	// 3. Re-enter and approve via fresh session ("Approve and execute") restores model in newCtx.
	hasUI := true
	cwd := t.TempDir()
	var freshSwitched []string
	_, hFresh := newTestExtension(t, hostOptions{
		SessionID:     "sess-plan-fresh",
		Cwd:           cwd,
		HasUI:         &hasUI,
		ModelProvider: "openai",
		Model:         "gpt-5.5",
		ThinkingLevel: "low",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "resolveModel":
				spec, _ := args["spec"].(string)
				freshSwitched = append(freshSwitched, spec)
				parts := strings.SplitN(spec, "/", 2)
				prov, id := "openai", spec
				if len(parts) == 2 {
					prov, id = parts[0], parts[1]
				}
				return map[string]any{"model": map[string]any{"provider": prov, "id": id}}, ""
			case "ui.select":
				return map[string]any{"ok": true, "selected": "Approve and execute"}, ""
			}
			return nil, ""
		},
	})
	hFresh.callCommand("plan", "")
	if len(freshSwitched) != 1 || freshSwitched[0] != "anthropic/claude-opus-5" {
		t.Fatalf("expected switch to plan model before fresh approval, got %v", freshSwitched)
	}
	// Create a local plan file so /plan-review finds it.
	planRoot := filepath.Join(os.TempDir(), "omp-local", "sess-plan-fresh")
	if err := os.MkdirAll(planRoot, 0o755); err != nil {
		t.Fatalf("mkdir planRoot: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(planRoot) })
	if err := os.WriteFile(filepath.Join(planRoot, "demo-plan.md"), []byte("# Demo Plan\n\nStep 1\n"), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
	hFresh.callCommand("plan-review", "")
	if len(freshSwitched) != 2 || freshSwitched[1] != "openai/gpt-5.5" {
		t.Fatalf("expected restore to openai/gpt-5.5 in fresh session, got %v (notify=%q)", freshSwitched, hFresh.lastNotify())
	}
	hFresh.mu.Lock()
	restoredLvl := hFresh.thinkingLevel
	hFresh.mu.Unlock()
	if restoredLvl != "low" {
		t.Fatalf("expected thinking level restored to low in fresh session, got %q", restoredLvl)
	}
}

func TestWave5RenameCommandAutoTitle(t *testing.T) {
	origResolve := ResolveRole
	origComplete := completeRename
	t.Cleanup(func() {
		ResolveRole = origResolve
		completeRename = origComplete
	})

	// 1. Explicit title bypasses completion completely.
	completeCalled := false
	completeRename = func(_ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		completeCalled = true
		return nil, nil
	}
	_, hExplicit := newTestExtension(t, hostOptions{SessionID: "sess-rename-explicit"})
	hExplicit.callCommand("rename", "My Custom Session")
	if completeCalled {
		t.Fatalf("explicit /rename <title> must not call completeRename")
	}
	hExplicit.mu.Lock()
	name := hExplicit.sessionName
	hExplicit.mu.Unlock()
	if name != "My Custom Session" {
		t.Fatalf("expected sessionName 'My Custom Session', got %q", name)
	}
	if got := hExplicit.lastNotify(); got != "Session renamed to My Custom Session." {
		t.Fatalf("unexpected notify: %q", got)
	}

	// 2. Empty branch -> fallback notice without calling completion.
	_, hEmpty := newTestExtension(t, hostOptions{SessionID: "sess-rename-empty"})
	hEmpty.callCommand("rename", "")
	if completeCalled {
		t.Fatalf("empty branch should not call completeRename")
	}
	if got := hEmpty.lastNotify(); got != "Could not generate a session title. Use /rename <title> to set one." {
		t.Fatalf("unexpected empty-branch notify: %q", got)
	}

	// 3. Auto-title success via @smol role + cleaning (<title>, quotes, trailing punct).
	ResolveRole = func(_ sdk.Context, selector string) (string, string, string, bool) {
		if selector == "@smol" {
			return "anthropic", "claude-haiku-4-5", "", true
		}
		return "", "", "", false
	}
	var gotModel map[string]any
	var gotReq map[string]any
	completeRename = func(_ sdk.Context, modelObj, req, _ map[string]any) (json.RawMessage, error) {
		gotModel = modelObj
		gotReq = req
		return json.RawMessage(`{"content":[{"type":"text","text":"<title>\"Fix retry queue dropping jobs.\"</title>\nextra line"}]}`), nil
	}
	_, hAuto := newTestExtension(t, hostOptions{
		SessionID: "sess-rename-auto",
		Branch: []map[string]any{
			{
				"id":   "u1",
				"type": "message",
				"message": map[string]any{
					"role":    "user",
					"content": "the retry queue drops jobs when redis restarts, please fix",
				},
			},
		},
	})
	hAuto.callCommand("rename", "")
	if gotModel["id"] != "claude-haiku-4-5" || gotModel["provider"] != "anthropic" {
		t.Fatalf("expected @smol model anthropic/claude-haiku-4-5, got %#v", gotModel)
	}
	if gotReq["maxTokens"] != 1024 {
		t.Fatalf("expected maxTokens=1024, got %#v", gotReq["maxTokens"])
	}
	hAuto.mu.Lock()
	name = hAuto.sessionName
	hAuto.mu.Unlock()
	if name != "Fix retry queue dropping jobs" {
		t.Fatalf("expected cleaned title 'Fix retry queue dropping jobs', got %q", name)
	}
	if got := hAuto.lastNotify(); got != "Session renamed to Fix retry queue dropping jobs." {
		t.Fatalf("unexpected notify: %q", got)
	}

	// 4. Completion error -> fallback notice.
	completeRename = func(_ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return nil, errors.New("provider timeout")
	}
	_, hErr := newTestExtension(t, hostOptions{
		SessionID: "sess-rename-err",
		Branch: []map[string]any{
			{
				"id":   "u1",
				"type": "message",
				"message": map[string]any{
					"role":    "user",
					"content": "add jitter to backoff policy",
				},
			},
		},
	})
	hErr.callCommand("rename", "")
	if got := hErr.lastNotify(); got != "Could not generate a session title. Use /rename <title> to set one." {
		t.Fatalf("unexpected error fallback notify: %q", got)
	}
	hErr.mu.Lock()
	if hErr.sessionName != "" {
		hErr.mu.Unlock()
		t.Fatalf("session name should remain unset on error, got %q", hErr.sessionName)
	}
	hErr.mu.Unlock()

	// 5. Sentinel `<title/>` / `none` -> fallback notice.
	completeRename = func(_ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"content":"<title/>"}`), nil
	}
	hErr.callCommand("rename", "")
	if got := hErr.lastNotify(); got != "Could not generate a session title. Use /rename <title> to set one." {
		t.Fatalf("unexpected sentinel fallback notify: %q", got)
	}

	// 6. Long title truncates safely to <=80 chars.
	longTitle := strings.Repeat("a", 120)
	if cleaned := cleanGeneratedTitle("<title>" + longTitle + "</title>"); len([]rune(cleaned)) != renameTitleMaxChars {
		t.Fatalf("expected truncated length %d, got %d", renameTitleMaxChars, len([]rune(cleaned)))
	}
}
