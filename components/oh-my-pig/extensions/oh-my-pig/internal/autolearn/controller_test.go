package autolearn

import (
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

func saveAndRestoreAutolearnHooks(t *testing.T) {
	t.Helper()
	origEnabled := Enabled
	origSetting := Setting
	origIsSubagent := IsSubagent
	origExplicitTools := ExplicitTools
	origGoalActive := GoalActive
	origCaptureTurn := CaptureTurn
	t.Cleanup(func() {
		Enabled = origEnabled
		Setting = origSetting
		IsSubagent = origIsSubagent
		ExplicitTools = origExplicitTools
		GoalActive = origGoalActive
		CaptureTurn = origCaptureTurn
		gate.ResetForTests()
	})
}

func runSubstantiveTurn(h *autolearnHost, toolCalls int, endData map[string]any) {
	h.emitEvent(sdk.EventAgentStart, nil)
	for range toolCalls {
		h.emitEvent(sdk.EventToolResult, map[string]any{"toolName": "read"})
	}
	if endData == nil {
		endData = map[string]any{
			"messages": []any{
				map[string]any{"role": "assistant", "stopReason": "stop"},
			},
		}
	}
	h.emitEvent(sdk.EventAgentEnd, endData)
}

func TestBuildInstructions(t *testing.T) {
	saveAndRestoreAutolearnHooks(t)

	var zero sdk.Context

	// Default disabled -> empty string (zero prompt change).
	Enabled = func(_ sdk.Context, _ string, def bool) bool { return def }
	if got := BuildInstructions(zero); got != "" {
		t.Fatalf("expected empty instructions when disabled, got %q", got)
	}

	// Enabled -> non-empty standing guidance.
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingEnabled {
			return true
		}
		return def
	}
	got := BuildInstructions(zero)
	if !strings.Contains(got, "## Auto-Learn (experimental)") || !strings.Contains(got, "manage_skill") {
		t.Fatalf("unexpected standing guidance: %q", got)
	}

	// Subagent without explicit manage_skill -> empty string.
	IsSubagent = func(_ sdk.Context) bool { return true }
	ExplicitTools = nil
	if got := BuildInstructions(zero); got != "" {
		t.Fatalf("expected empty instructions for subagent without manage_skill, got %q", got)
	}
}

func TestAutoLearnControllerGates(t *testing.T) {
	saveAndRestoreAutolearnHooks(t)

	var (
		mu       sync.Mutex
		captures []string
		capCh    = make(chan string, 10)
	)
	CaptureTurn = func(_ sdk.Context, prompt string) error {
		mu.Lock()
		captures = append(captures, prompt)
		mu.Unlock()
		capCh <- prompt
		return nil
	}

	ext := sdk.New("autolearn-controller-test")
	Register(ext)
	h := startAutolearnHost(t, ext, "interactive", true, t.TempDir())
	t.Cleanup(func() { lifecycle.End(h.sessionID) })

	assertNoCapture := func(label string) {
		t.Helper()
		select {
		case got := <-capCh:
			t.Fatalf("%s: unexpected capture triggered: %q", label, got)
		case <-time.After(40 * time.Millisecond):
		}
	}

	// 1. Disabled (default) -> no-op.
	Enabled = func(_ sdk.Context, _ string, def bool) bool { return def }
	runSubstantiveTurn(h, 6, nil)
	assertNoCapture("disabled")

	// 2. autolearn.enabled=true, autoContinue=false (passive) -> no-op.
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingEnabled {
			return true
		}
		return def
	}
	runSubstantiveTurn(h, 6, nil)
	assertNoCapture("passive autoContinue=false")

	// Enable both autolearn.enabled and autolearn.autoContinue for remaining gate tests.
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == SettingEnabled || key == SettingAutoContinue {
			return true
		}
		return def
	}
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingMinToolCalls {
			return 5
		}
		return nil
	}

	// 3. Below minToolCalls -> skip (and counter resets, so 4 + 4 does NOT accumulate to 8).
	runSubstantiveTurn(h, 4, nil)
	assertNoCapture("below minToolCalls #1")
	runSubstantiveTurn(h, 4, nil)
	assertNoCapture("below minToolCalls #2 (must not accumulate)")

	// 4. Aborted turn -> skip.
	runSubstantiveTurn(h, 6, map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "stopReason": "aborted"},
		},
	})
	assertNoCapture("aborted turn")

	// 5. Plan mode active -> skip.
	gate.SetPlanActive(func(_ sdk.Context) bool { return true })
	runSubstantiveTurn(h, 6, nil)
	assertNoCapture("plan mode")
	gate.SetPlanActive(nil)

	// 6. Goal mode active at turn start (even if cleared before end) -> skip.
	inGoal := true
	GoalActive = func(_ sdk.Context) bool { return inGoal }
	h.emitEvent(sdk.EventAgentStart, nil)
	for range 6 {
		h.emitEvent(sdk.EventToolResult, nil)
	}
	inGoal = false // goal completed mid-turn
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	assertNoCapture("started in goal mode")

	// 7. Goal mode active at turn end -> skip.
	inGoal = false
	h.emitEvent(sdk.EventAgentStart, nil)
	for range 6 {
		h.emitEvent(sdk.EventToolResult, nil)
	}
	inGoal = true
	h.emitEvent(sdk.EventAgentEnd, map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	assertNoCapture("ended in goal mode")
	GoalActive = nil

	// 8. Subagent -> skip.
	IsSubagent = func(_ sdk.Context) bool { return true }
	runSubstantiveTurn(h, 6, nil)
	assertNoCapture("subagent")
	IsSubagent = nil

	// 9. Capture turn itself completing -> must NOT recursively re-trigger capture
	// (including a follow-up auto-retry agent_end without the custom message, until
	// EventAgentSettled clears captureActive).
	runSubstantiveTurn(h, 6, map[string]any{
		"messages": []any{
			map[string]any{"role": "custom", "customType": captureCustomType},
			map[string]any{"role": "assistant", "stopReason": "stop"},
		},
	})
	assertNoCapture("capture turn self-end")
	runSubstantiveTurn(h, 6, nil)
	assertNoCapture("retried capture turn before agent_settled")
	h.emitEvent(sdk.EventAgentSettled, map[string]any{})

	// 10. Eligible turn -> triggers capture synchronously with exact embedded nudge prompt.
	runSubstantiveTurn(h, 5, nil)
	select {
	case got := <-capCh:
		if !strings.Contains(got, "Automated capture turn") || !strings.Contains(got, "manage_skill") {
			t.Fatalf("unexpected capture prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auto-capture trigger")
	}
}

func TestAutoLearnControllerReentrancyAndSettledReset(t *testing.T) {
	saveAndRestoreAutolearnHooks(t)

	Enabled = func(_ sdk.Context, key string, def bool) bool {
		return key == SettingEnabled || key == SettingAutoContinue
	}
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingMinToolCalls {
			return 2
		}
		return nil
	}

	var (
		mu    sync.Mutex
		calls int
	)
	CaptureTurn = func(_ sdk.Context, _ string) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}

	ext := sdk.New("autolearn-reentrancy-test")
	Register(ext)
	h := startAutolearnHost(t, ext, "interactive", true, t.TempDir())
	t.Cleanup(func() { lifecycle.End(h.sessionID) })

	// Turn 1 triggers capture #1 synchronously and marks captureActive=true.
	runSubstantiveTurn(h, 2, nil)
	// Subsequent agent_end before EventAgentSettled (e.g. capture turn itself or
	// retry continuation without customType) must NOT trigger another capture.
	runSubstantiveTurn(h, 2, nil)
	runSubstantiveTurn(h, 2, nil)

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected exactly 1 capture before agent_settled, got %d", got)
	}

	// Once EventAgentSettled fires, the next primary turn can trigger capture again.
	h.emitEvent(sdk.EventAgentSettled, map[string]any{})
	runSubstantiveTurn(h, 2, nil)

	mu.Lock()
	got = calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("expected 2nd capture after agent_settled, got %d", got)
	}
}
