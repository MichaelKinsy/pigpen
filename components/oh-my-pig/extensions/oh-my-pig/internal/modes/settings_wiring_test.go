package modes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestModesSettingsWiring_MagicKeywords(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	// 1. magicKeywords.enabled = false suppresses all magic keyword notices & chillax effort drop.
	Setting = func(_ sdk.Context, key string) any {
		if key == "magicKeywords.enabled" {
			return false
		}
		return nil
	}
	_, h := newTestExtension(t, hostOptions{
		SessionID:        "sess-mk-disabled",
		ThinkingLevel:    "high",
		PerMessageEffort: true,
		ActiveTools:      []string{"read", "task", "eval"},
	})
	h.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "ultrathink chillax orchestrate workflowz jevify",
		"source": "interactive",
	})
	if len(h.callsTo("sendMessage")) != 0 {
		t.Fatalf("expected no magic keyword notices when magicKeywords.enabled=false, got %d", len(h.callsTo("sendMessage")))
	}
	h.mu.Lock()
	lvl := h.thinkingLevel
	h.mu.Unlock()
	if lvl != "high" {
		t.Fatalf("expected thinking level unchanged when magicKeywords.enabled=false, got %q", lvl)
	}

	// 2. Per-keyword toggle: disable ultrathink & workflow, keep jevify & orchestrate.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "magicKeywords.enabled":
			return true
		case "magicKeywords.ultrathink", "magicKeywords.workflow":
			return false
		}
		return nil
	}
	_, h2 := newTestExtension(t, hostOptions{
		SessionID:   "sess-mk-partial",
		ActiveTools: []string{"read", "task", "eval"},
	})
	h2.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "please ultrathink and workflowz and orchestrate and jevify",
		"source": "interactive",
	})
	seenTypes := map[string]bool{}
	for _, c := range h2.callsTo("sendMessage") {
		if msg, ok := c.Args["message"].(map[string]any); ok {
			if ct, ok := msg["customType"].(string); ok {
				seenTypes[ct] = true
			}
		}
	}
	if seenTypes["ultrathink-notice"] || seenTypes["workflow-notice"] {
		t.Fatalf("disabled keywords should not fire, got %#v", seenTypes)
	}
	if !seenTypes["orchestrate-notice"] || !seenTypes["jevify-notice"] {
		t.Fatalf("enabled keywords should fire, got %#v", seenTypes)
	}
}

func TestModesSettingsWiring_PlanAndGoalEnabled(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	// 1. plan.enabled = false blocks entering plan mode via /plan.
	Setting = func(_ sdk.Context, key string) any {
		if key == "plan.enabled" {
			return false
		}
		return nil
	}
	m, h := newTestExtension(t, hostOptions{SessionID: "sess-plan-disabled"})
	h.callCommand("plan", "")
	if got := h.lastNotify(); !strings.Contains(got, "Plan mode is disabled") {
		t.Fatalf("expected plan disabled warning, got %q", got)
	}
	m.mu.Lock()
	planOn := m.stateLocked("sess-plan-disabled").planEnabled
	m.mu.Unlock()
	if planOn {
		t.Fatalf("plan mode should remain disabled when plan.enabled=false")
	}

	// 2. goal.enabled = false blocks /goal, /guided-goal, and goal tool create.
	Setting = func(_ sdk.Context, key string) any {
		if key == "goal.enabled" {
			return false
		}
		return nil
	}
	_, h2 := newTestExtension(t, hostOptions{SessionID: "sess-goal-disabled"})
	h2.callCommand("goal", "ship feature X")
	if got := h2.lastNotify(); !strings.Contains(got, "Goal mode is disabled") {
		t.Fatalf("expected goal disabled warning, got %q", got)
	}
	h2.callCommand("guided-goal", "ship feature X")
	if got := h2.lastNotify(); !strings.Contains(got, "Goal mode is disabled") {
		t.Fatalf("expected guided-goal disabled warning, got %q", got)
	}
	_, errMsg := h2.callTool("goal", map[string]any{"op": "create", "objective": "ship X"})
	if errMsg == "" || !strings.Contains(errMsg, "Goal mode is disabled") {
		t.Fatalf("expected goal tool create error when goal.enabled=false, got %q", errMsg)
	}
}

func TestModesSettingsWiring_PlanAutosaveAndDir(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	cwd := t.TempDir()

	// 1. Default (Setting == nil): plan.autosave=false -> writes nothing.
	Setting = nil
	saved, err := AutosaveApprovedPlan(sdk.Context{}, cwd, "My Feature", "# My Feature\n\nSteps...\n")
	if err != nil || saved != "" {
		t.Fatalf("expected no autosave by default, got saved=%q err=%v", saved, err)
	}

	// 2. plan.autosave=true with empty plan.autosaveDir -> <cwd>/.omp/plans/MY_FEATURE_PLAN.md
	Setting = func(_ sdk.Context, key string) any {
		if key == "plan.autosave" {
			return true
		}
		return nil
	}
	saved1, err := AutosaveApprovedPlan(sdk.Context{}, cwd, "My Feature", "# My Feature\n\nV1\n")
	if err != nil {
		t.Fatalf("autosave 1 failed: %v", err)
	}
	want1 := filepath.Join(cwd, ".omp", "plans", "MY_FEATURE_PLAN.md")
	if saved1 != want1 {
		t.Fatalf("saved1 = %q, want %q", saved1, want1)
	}
	// Second autosave with same title claims MY_FEATURE_PLAN-1.md via O_EXCL.
	saved2, err := AutosaveApprovedPlan(sdk.Context{}, cwd, "My Feature", "# My Feature\n\nV2\n")
	if err != nil {
		t.Fatalf("autosave 2 failed: %v", err)
	}
	want2 := filepath.Join(cwd, ".omp", "plans", "MY_FEATURE_PLAN-1.md")
	if saved2 != want2 {
		t.Fatalf("saved2 = %q, want %q", saved2, want2)
	}
	gotContent, err := os.ReadFile(saved2)
	if err != nil || !strings.Contains(string(gotContent), "V2") {
		t.Fatalf("unexpected saved2 content: %q (err=%v)", string(gotContent), err)
	}

	// 3. plan.autosaveDir relative to cwd.
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "plan.autosave":
			return true
		case "plan.autosaveDir":
			return "custom-plans"
		}
		return nil
	}
	saved3, err := AutosaveApprovedPlan(sdk.Context{}, cwd, "Ship It", "# Ship It\n")
	if err != nil {
		t.Fatalf("autosave 3 failed: %v", err)
	}
	want3 := filepath.Join(cwd, "custom-plans", "SHIP_IT_PLAN.md")
	if saved3 != want3 {
		t.Fatalf("saved3 = %q, want %q", saved3, want3)
	}
}

func TestModesSettingsWiring_LoopConditionTimeoutMs(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	// Default when Setting == nil -> 30000ms.
	Setting = nil
	if got := loopConditionTimeoutMs(sdk.Context{}); got != 30_000 {
		t.Fatalf("default loopConditionTimeoutMs = %d, want 30000", got)
	}

	// Custom positive value.
	Setting = func(_ sdk.Context, key string) any {
		if key == "loop.conditionTimeoutMs" {
			return float64(5000)
		}
		return nil
	}
	if got := loopConditionTimeoutMs(sdk.Context{}); got != 5000 {
		t.Fatalf("configured loopConditionTimeoutMs = %d, want 5000", got)
	}

	// 0 disables timeout (wait indefinitely, matching schema.json).
	Setting = func(_ sdk.Context, key string) any {
		if key == "loop.conditionTimeoutMs" {
			return float64(0)
		}
		return nil
	}
	if got := loopConditionTimeoutMs(sdk.Context{}); got != 0 {
		t.Fatalf("zero loopConditionTimeoutMs = %d, want 0", got)
	}
}
