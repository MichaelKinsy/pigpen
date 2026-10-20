package pi_goal_x_test

// Twins of tests/goal-turn-transaction.test.ts (P1-3, the per-turn transaction buffer): through the real SDK. They need the GoalEvents
// (before_agent_start, turn_start, turn_end, message_end) and GoalTools (set_goal_tasks, update_goal_task, update_goal) slices.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gttF = "goal-turn-transaction"

type gttFixture struct {
	t   *testing.T
	rig *goalRig
	rel string
}

func newGttFixture(t *testing.T, deps goal.RigDeps) *gttFixture {
	t.Helper()
	rel, text := seedGoal("turn-txn-goal", "=== Goal ===\\nObjective: Turn transaction test")
	r := newGoalRig(t, rigOptions{Settings: `{"maxAutonomousRuns": 0}`, Seed: map[string]string{rel: text}, Branch: []any{focusEntry("turn-txn-goal")}, Deps: deps, SessionID: "s"})
	return &gttFixture{t: t, rig: r, rel: rel}
}

func (f *gttFixture) goalFile() string {
	b, err := os.ReadFile(filepath.Join(f.rig.Cwd, filepath.FromSlash(f.rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func (f *gttFixture) ledger() string {
	b, _ := os.ReadFile(filepath.Join(f.rig.Cwd, ".pi", "goals", "goal_events.jsonl"))
	return string(b)
}

func (f *gttFixture) start() {
	f.rig.Fire("session_start", map[string]any{"reason": "start"})
	f.rig.Fire("before_agent_start", map[string]any{"systemPrompt": "p", "prompt": "p", "systemPromptOptions": map[string]any{}})
}

func gttStop(r *goalRig) {
	r.Fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": 0, "output": 0}}})
}

func TestGoalTurnTransactionTwins(t *testing.T) {
	tw(t, gttF, "buffers in-turn task mutations and flushes once at turn_end", func(t *testing.T) {
		f := newGttFixture(t, goal.RigDeps{})
		f.start()
		f.rig.ToolResult("set_goal_tasks", map[string]any{"tasks": []any{
			map[string]any{"id": "t1", "title": "T1"}, map[string]any{"id": "t2", "title": "T2"}, map[string]any{"id": "t3", "title": "T3"}}, "block_completion": false})
		// set_goal_tasks stops the turn: close it, then open the buffered turn.
		gttStop(f.rig)
		f.rig.Fire("turn_start", map[string]any{})
		for _, id := range []string{"t1", "t2", "t3"} {
			f.rig.ToolResult("update_goal_task", map[string]any{"task_id": id, "status": "complete", "evidence": "done " + id})
		}

		// Mid-turn: in-memory state is current, disk is NOT yet written.
		before := f.goalFile()
		reMatch(t, before, `\[ \] t1: T1`, "disk still shows pending mid-turn")
		reMatch(t, before, `\[ \] t2: T2`, "disk still shows pending mid-turn")
		same(t, len(regexp.MustCompile(`task_complete`).FindAllString(f.ledger(), -1)), 0, "no task_complete ledger events mid-turn")

		// turn_end: one flush writes the goal + batches the ledger.
		gttStop(f.rig)
		after := f.goalFile()
		reMatch(t, after, `\[x\] t1: T1`, "disk catches up after flush")
		reMatch(t, after, `\[x\] t3: T3`, "disk catches up after flush")
		same(t, len(regexp.MustCompile(`task_complete`).FindAllString(f.ledger(), -1)), 3, "three task_complete events after flush")
	})

	tw(t, gttF, "flushes the buffer before update_goal(complete) dispatches the auditor", func(t *testing.T) {
		var f *gttFixture
		dispatched := false
		f = newGttFixture(t, goal.RigDeps{Auditor: func(_ context.Context, _ map[string]any, _, _ string) goal.RigAuditResult {
			dispatched = true
			// The auditor (a separate session) reads the goal FILE: it must already contain the completed task.
			if !strings.Contains(f.goalFile(), "[x] t1: T1") {
				t.Errorf("the auditor does not see the flushed task state on disk:\n%s", f.goalFile())
			}
			return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "fixture"}
		}})
		f.start()
		f.rig.ToolResult("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "T1"}}, "block_completion": false})
		gttStop(f.rig)
		f.rig.Fire("turn_start", map[string]any{})
		f.rig.ToolResult("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "done t1"})
		f.rig.ToolResult("update_goal", map[string]any{"status": "complete"})
		same(t, dispatched, true, "auditor was dispatched")
	})

	tw(t, gttF, "keeps a paused goal persisted immediately (user Esc mid-turn)", func(t *testing.T) {
		f := newGttFixture(t, goal.RigDeps{})
		f.start()
		f.rig.Fire("turn_start", map[string]any{})
		// User Esc: the session abort signal fires, then message_end (aborted) arrives with the signal set and pauses the goal.
		f.rig.RunSignal(true, true)
		f.rig.Fire("message_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": 0, "output": 0}}})
		reMatch(t, f.goalFile(), `Status: paused`, "pause persists immediately, not at turn end")
	})
}
