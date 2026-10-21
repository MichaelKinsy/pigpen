package pi_goal_x

import (
	"strings"
	"testing"
	"time"
)

// Twins of tests/session-checkpoint-growth.test.ts (issue #30: bounded continuation checkpoints).

func TestSessionCheckpointGrowth(t *testing.T) {
	const f = "session-checkpoint-growth"
	longObjective := strings.Repeat("Reproduce the repeated full checkpoint prompt growth defect. ", 20)
	newGoal := func() *jsObject {
		return createGoal(longObjective, true, false, false, ms(2026, time.August, 23, 9, 0, 0))
	}
	newRuntime := func(goal *jsObject, sent chan sentFollowUp) *goalRuntime {
		return newGoalRuntime(goalRuntimeHooks{
			authorize: func(goalRuntimeCtx, *jsObject) *jsObject {
				return jo(t, `{"generation":"test-generation","dispatchId":"test-dispatch"}`)
			},
			sendFollowUp: func(content string, details *jsObject) error {
				sent <- sentFollowUp{content, details}
				return nil
			},
			getGoal:      func() *jsObject { return goal },
			isActionable: func(string) bool { return true },
		})
	}
	// fireContinuation queues with an idle context (0 ms delay) and waits for the single delivered follow-up.
	fireContinuation := func(t *testing.T, goal *jsObject) sentFollowUp {
		sent := make(chan sentFollowUp, 4)
		runtime := newRuntime(goal, sent)
		runtime.queueContinuation(mockRuntimeCtx{}, goal, true)
		select {
		case m := <-sent:
			time.Sleep(30 * time.Millisecond)
			if len(sent) != 0 {
				t.Fatal("exactly one follow-up must be delivered")
			}
			return m
		case <-time.After(2 * time.Second):
			t.Fatal("exactly one follow-up must be delivered")
		}
		return sentFollowUp{}
	}

	tw(t, f, "persisted checkpoint content stays within the trigger bound", func(t *testing.T) {
		sent := fireContinuation(t, newGoal())
		assertTrue(t, utf16Len(sent.content) <= checkpointTriggerMaxChars, "checkpoint content within bound")
	})

	tw(t, f, "persisted checkpoint omits objective, task, contract, and policy text", func(t *testing.T) {
		sent := fireContinuation(t, newGoal())
		for _, forbidden := range []string{utf16Prefix(longObjective, 40), "TASK LIST", "VERIFICATION CONTRACT", "[OUTCOMES]", "lifecycle"} {
			assertTrue(t, !strings.Contains(sent.content, forbidden), "checkpoint content leaked "+forbidden)
			assertTrue(t, !strings.Contains(marshalJSON(sent.details, ""), forbidden), "checkpoint details leaked "+forbidden)
		}
	})

	tw(t, f, "persisted details carry generation-tagged v3 fields", func(t *testing.T) {
		goal := newGoal()
		sent := fireContinuation(t, goal)
		d := sent.details
		assertEq(t, gnum(d, "version"), 3.0, "version")
		assertEq(t, gstr(d, "generation"), "test-generation", "generation")
		assertEq(t, gstr(d, "dispatchId"), "test-dispatch", "dispatchId")
		assertEq(t, gstr(d, "kind"), "checkpoint", "kind")
		assertEq(t, gstr(d, "goalId"), gstr(goal, "id"), "goalId")
		assertEq(t, gstr(d, "status"), "active", "status")
		_, isNum := d.num("checkpointSeq")
		assertTrue(t, isNum, "checkpointSeq is a number")
	})

	tw(t, f, "sequential checkpoints increment checkpointSeq", func(t *testing.T) {
		goal := newGoal()
		sent := make(chan sentFollowUp, 4)
		runtime := newRuntime(goal, sent)
		var seqs []any
		next := func() {
			select {
			case m := <-sent:
				seqs = append(seqs, gnum(m.details, "checkpointSeq"))
			case <-time.After(2 * time.Second):
				t.Fatal("no follow-up delivered")
			}
		}
		runtime.queueContinuation(mockRuntimeCtx{}, goal, true)
		next()
		runtime.clearContinuationState(true)
		runtime.queueContinuation(mockRuntimeCtx{}, goal, true)
		next()
		jsonEq(t, seqs, []any{1.0, 2.0}, "seqs")
	})
}
