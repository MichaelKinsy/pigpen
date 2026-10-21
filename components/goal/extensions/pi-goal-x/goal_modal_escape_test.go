package pi_goal_x_test

// Twins of tests/goal-modal-escape.test.ts: while any goal-owned modal is open the terminal-input handler must not intercept keys
// (pi's onTerminalInput runs BEFORE the focused overlay, so without the guard Escape would pause the goal before the dialog could
// process it). The guard is a depth counter (enterGoalModal/exitGoalModal) so nested goal modals stay guarded.

import (
	"os"
	"sync"
	"testing"
	"time"
)

const (
	escapeKey  = "\x1b"
	ctrlShiftT = "\x1b[116;6u" // kitty protocol: 't' with ctrl+shift modifiers
)

// modalEscapeRig is createHarness + startSession of the original: a TUI session with the golden fixture goal active.
func modalEscapeRig(t *testing.T, custom func(map[string]any) map[string]any) *goalRig {
	t.Helper()
	fixture, err := os.ReadFile("testdata/fixtures/goals/active_goal_fixture.md")
	if err != nil {
		t.Fatal(err)
	}
	stateGoal := map[string]any{
		"id": "golden_fixture_goal", "objective": "x", "autoContinue": true,
		"activePath": ".pi/goals/active_goal_fixture.md", "usage": map[string]any{"tokensUsed": 0.0, "activeSeconds": 0.0},
	}
	r := newGoalRig(t, rigOptions{
		Cwd: archivedCwd(t), HasUI: boolPtr(true), SessionID: "test-session",
		Seed: map[string]string{".pi/goals/active_goal_fixture.md": string(fixture)},
		Branch: []any{
			map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1.0, "focusedGoalId": "golden_fixture_goal", "reason": "created"}},
			map[string]any{"type": "custom", "customType": "pi-goal-state", "data": map[string]any{"version": 3.0, "goal": stateGoal}},
		},
		Idle:    func() bool { return false },
		Pending: func() bool { return true },
		Custom:  custom,
	})
	r.Fire("session_start", map[string]any{"reason": "start"})
	return r
}

func TestGoalModalEscape(t *testing.T) {
	const f = "goal-modal-escape"

	tw(t, f, "Escape while a goal modal is open never pauses the goal; Escape after it closes does", func(t *testing.T) {
		var mu sync.Mutex
		shown := false
		release := make(chan struct{})
		r := modalEscapeRig(t, func(map[string]any) map[string]any {
			mu.Lock()
			shown = true
			mu.Unlock()
			<-release
			return map[string]any{"ok": true, "result": map[string]any{"decision": "confirm"}}
		})
		overlayShown := func() bool { mu.Lock(); defer mu.Unlock(); return shown }

		// Ctrl+Shift+T now toggles the unified dashboard expansion (the separate task-list overlay registration is removed; §10/§19.5).
		// It must be consumed and must NOT open an overlay or enter a goal modal.
		if !r.terminalInput(ctrlShiftT) {
			t.Error("ctrl+shift+t must be consumed by the widget")
		}
		if overlayShown() {
			t.Error("ctrl+shift+t must not open the task-list overlay anymore")
		}
		// Toggle back to compact so the final Escape exercises the pause path rather than collapsing the expanded dashboard.
		r.terminalInput(ctrlShiftT)

		// Open a real goal modal through the real wiring: set_goal_tasks shows the task-confirmation dialog (ui.custom), entering the
		// goal modal (depth counter) before showing it.
		if !r.Host.tools["set_goal_tasks"] {
			t.Fatal("set_goal_tasks tool registered")
		}
		call := goAsync(func() {
			r.Tool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "Task one"}}})
		})
		deadline := time.Now().Add(5 * time.Second)
		for !overlayShown() && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if !overlayShown() {
			t.Fatal("task confirmation modal must be shown")
		}

		// Escape while the modal is open: the handler must yield to the dialog (not consume) and never pause the goal.
		if r.terminalInput(escapeKey) {
			t.Error("handler must not consume Escape while a goal modal is open")
		}
		if contains(r.Notices(), "info: Goal paused.") {
			t.Error("goal must not be paused while a goal modal is open")
		}

		// Close the modal via its done callback -> the modal exits (finally).
		close(release)
		call.wait(t)
		time.Sleep(10 * time.Millisecond)

		// Escape with no goal modal open: the normal pause path must work.
		r.terminalInput(escapeKey)
		if !contains(r.Notices(), "info: Goal paused.") {
			t.Errorf("Escape without a goal modal pauses the goal: %v", r.Notices())
		}
	})

	tw(t, f, "Escape with no goal modal open pauses the goal (regression guard)", func(t *testing.T) {
		r := modalEscapeRig(t, nil)
		r.terminalInput(escapeKey)
		if !contains(r.Notices(), "info: Goal paused.") {
			t.Errorf("Escape must pause the goal when no modal is open: %v", r.Notices())
		}
	})

	tw(t, f, "Escape on a live goal pauses AND passes the key back to pi (stops the working)", func(t *testing.T) {
		r := modalEscapeRig(t, nil)
		// The fixture goal is active + autoContinue. Escape must pause it AND not consume the key so pi also receives it and aborts the
		// running tool execution / current turn — pausing without stopping the "working" is the reported bug.
		if r.terminalInput(escapeKey) {
			t.Error("Escape on a live goal must pass back to pi so the current turn stops")
		}
		if !contains(r.Notices(), "info: Goal paused.") {
			t.Errorf("Escape on a live goal must still pause the goal: %v", r.Notices())
		}
	})

	tw(t, f, "Escape while the goal is paused passes through to pi without any goal state change", func(t *testing.T) {
		r := modalEscapeRig(t, nil)
		count := func() int {
			n := 0
			for _, m := range r.Notices() {
				if m == "info: Goal paused." {
					n++
				}
			}
			return n
		}
		// First Escape pauses the live goal (one notification).
		r.terminalInput(escapeKey)
		if count() != 1 {
			t.Errorf("first Escape pauses the live goal: %d", count())
		}
		// Second Escape: the goal is now paused, so the handler must not pause again (no state change) and must pass the key back to
		// pi, which stops the current turn.
		if r.terminalInput(escapeKey) {
			t.Error("Escape while paused must pass back to pi (stop the current turn)")
		}
		if count() != 1 {
			t.Errorf("Escape while paused must not re-pause or otherwise change goal state: %d", count())
		}
	})
}
