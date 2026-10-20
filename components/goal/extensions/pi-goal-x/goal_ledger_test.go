package pi_goal_x

import (
	"strings"
	"testing"
)

const ledgerFile0 = "goal-ledger"

func TestGoalLedger(t *testing.T) {
	tw(t, ledgerFile0, "goalLedgerPath resolves under .pi/goals", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		if !strings.Contains(goalLedgerPath(ctx), ".pi/goals/goal_events.jsonl") {
			t.Error(goalLedgerPath(ctx))
		}
	})
	tw(t, ledgerFile0, "appendGoalEvent creates ledger and appends event", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, createdEv("g1", "test", nowIso()))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		eq(t, evStr(r.events[0], "type"), "goal_created", "type")
		eq(t, evStr(r.events[0], "goalId"), "g1", "goalId")
		eq(t, r.malformed, 0, "malformed")
	})
	tw(t, ledgerFile0, "readGoalLedger tolerates missing file", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 0, "events")
		eq(t, r.malformed, 0, "malformed")
	})
	tw(t, ledgerFile0, "readGoalLedger skips malformed lines and counts them", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		writeLedgerFile(t, ctx, "not json\n{\"type\":\"goal_created\",\"goalId\":\"g2\",\"objective\":\"x\",\"sisyphus\":false,\"autoContinue\":true,\"at\":\"2024-01-01T00:00:00.000Z\"}\n")
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		eq(t, r.malformed, 1, "malformed")
	})
	tw(t, ledgerFile0, "readGoalLedger skips unknown event types", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		writeLedgerFile(t, ctx, "{\"type\":\"unknown_event\",\"at\":\"2024-01-01T00:00:00.000Z\"}\n")
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 0, "events")
		eq(t, r.malformed, 1, "malformed")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger tracks focus and status", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_focused", "g1", "2024-01-01T00:00:01.000Z", "reason", "created"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:02.000Z", "reason", "blocked"),
			ledgerEv("goal_resumed", "g1", "2024-01-01T00:00:03.000Z", "reason", "user"),
		})
		eq(t, derefStr(state.focusedGoalID), "g1", "focus")
		g1, _ := state.goals.get("g1")
		eq(t, g1.latestStatus, "active", "status")
		eq(t, g1.latestPauseReason == nil, true, "pause reason cleared")
		eq(t, state.terminalGoals.size(), 0, "terminal")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger marks terminal goals", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_completed", "g1", "2024-01-01T00:00:01.000Z"),
			createdEv("g2", "o2", "2024-01-01T00:00:02.000Z"),
			ledgerEv("goal_aborted", "g2", "2024-01-01T00:00:03.000Z", "reason", "obsolete"),
		})
		eq(t, state.terminalGoals.has("g1"), true, "g1 terminal")
		eq(t, state.terminalGoals.has("g2"), true, "g2 terminal")
		g1, _ := state.terminalGoals.get("g1")
		g2, _ := state.terminalGoals.get("g2")
		eq(t, g1.latestStatus, "complete", "g1 status")
		eq(t, derefStr(g1.completedAt), "2024-01-01T00:00:01.000Z", "completedAt")
		eq(t, g2.latestStatus, "aborted", "g2 status")
		eq(t, derefStr(g2.abortedAt), "2024-01-01T00:00:03.000Z", "abortedAt")
		eq(t, state.goals.has("g1"), false, "g1 left goals")
		eq(t, state.goals.has("g2"), false, "g2 left goals")
		eq(t, state.focusedGoalID == nil, true, "no focus")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger clears focus when focused goal is terminal", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_focused", "g1", "2024-01-01T00:00:01.000Z", "reason", "created"),
			ledgerEv("goal_completed", "g1", "2024-01-01T00:00:02.000Z"),
		})
		eq(t, state.focusedGoalID == nil, true, "no focus")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger captures auditor results", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("completion_requested", "g1", "2024-01-01T00:00:01.000Z", "summary", "done"),
			ledgerEv("audit_started", "g1", "2024-01-01T00:00:02.000Z", "provider", "fireworks", "model", "kimi"),
			ledgerEv("audit_result", "g1", "2024-01-01T00:00:03.000Z", "verdict", "disapproved", "report", "missing tests"),
		})
		g1, ok := state.goals.get("g1")
		eq(t, ok, true, "g1")
		eq(t, gstr(g1.latestAuditorResult, "verdict"), "disapproved", "verdict")
		eq(t, gstr(g1.latestAuditorResult, "report"), "missing tests", "report")
	})
	tw(t, ledgerFile0, "latestAuditorResultForGoal returns most recent result", func(t *testing.T) {
		r := latestAuditorResultForGoal([]*jsObject{
			ledgerEv("audit_result", "g1", "2024-01-01T00:00:00.000Z", "verdict", "disapproved", "report", "first"),
			ledgerEv("audit_result", "g1", "2024-01-01T00:00:01.000Z", "verdict", "approved", "report", "second"),
		}, "g1")
		eq(t, gstr(r, "verdict"), "approved", "verdict")
		eq(t, gstr(r, "report"), "second", "report")
	})
	tw(t, ledgerFile0, "latestEventsForGoal returns capped recent events", func(t *testing.T) {
		r := latestEventsForGoal([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:01.000Z", "reason", "a"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:02.000Z", "reason", "b"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:03.000Z", "reason", "c"),
		}, "g1", 2)
		eq(t, len(r), 2, "len")
		eq(t, evStr(r[0], "reason"), "b", "first")
		eq(t, evStr(r[1], "reason"), "c", "second")
	})
	tw(t, ledgerFile0, "latestGoalLifecycleEvent returns last event for goal", func(t *testing.T) {
		r := latestGoalLifecycleEvent([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:01.000Z", "reason", "a"),
			createdEv("g2", "o2", "2024-01-01T00:00:02.000Z"),
		}, "g1")
		eq(t, evStr(r, "type"), "goal_paused", "type")
	})
	tw(t, ledgerFile0, "appendGoalEvent handles multiple sequential appends", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		for i := range 5 {
			appendGoalEvent(ctx, createdEv("g"+jsNumber(float64(i)), "obj"+jsNumber(float64(i)), nowIso()))
		}
		eq(t, len(readGoalLedger(ctx).events), 5, "events")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger handles empty events", func(t *testing.T) {
		state := reconstructGoalLedger(nil)
		eq(t, state.focusedGoalID == nil, true, "no focus")
		eq(t, state.goals.size(), 0, "goals")
		eq(t, state.terminalGoals.size(), 0, "terminal")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger defaults missing status to paused in goal_paused", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:01.000Z", "reason", "user"),
		})
		g1, _ := state.goals.get("g1")
		eq(t, g1.latestStatus, "paused", "status")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger creates stub state for terminal events without prior goal_created", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			ledgerEv("goal_completed", "legacy-g1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_aborted", "legacy-g2", "2024-01-01T00:00:01.000Z", "reason", "obsolete"),
		})
		a, _ := state.terminalGoals.get("legacy-g1")
		b, _ := state.terminalGoals.get("legacy-g2")
		eq(t, a.latestStatus, "complete", "a status")
		eq(t, derefStr(a.completedAt), "2024-01-01T00:00:00.000Z", "completedAt")
		eq(t, b.latestStatus, "aborted", "b status")
		eq(t, derefStr(b.abortedAt), "2024-01-01T00:00:01.000Z", "abortedAt")
	})
	tw(t, ledgerFile0, "appendGoalEvent persists goal_paused with status field", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("goal_paused", "g1", "2024-01-01T00:00:00.000Z", "reason", "user", "status", "paused", "suggestedAction", "Check credentials"))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		eq(t, evStr(r.events[0], "type"), "goal_paused", "type")
		eq(t, evStr(r.events[0], "status"), "paused", "status")
		eq(t, evStr(r.events[0], "reason"), "user", "reason")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger handles repeated pause and resume events", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_focused", "g1", "2024-01-01T00:00:01.000Z", "reason", "created"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:02.000Z", "reason", "user", "status", "paused"),
			ledgerEv("goal_resumed", "g1", "2024-01-01T00:00:03.000Z", "reason", "user"),
			ledgerEv("goal_paused", "g1", "2024-01-01T00:00:04.000Z", "reason", "user"),
			ledgerEv("goal_resumed", "g1", "2024-01-01T00:00:05.000Z", "reason", "user"),
			ledgerEv("goal_completed", "g1", "2024-01-01T00:00:06.000Z"),
		})
		eq(t, state.focusedGoalID == nil, true, "no focus")
		eq(t, state.goals.size(), 0, "goals")
		eq(t, state.terminalGoals.size(), 1, "terminal")
		g1, ok := state.terminalGoals.get("g1")
		eq(t, ok, true, "g1")
		eq(t, g1.latestStatus, "complete", "status")
		eq(t, derefStr(g1.completedAt), "2024-01-01T00:00:06.000Z", "completedAt")
		// goal_resumed clears pauseReason, so latestPauseReason should be undefined
		eq(t, g1.latestPauseReason == nil, true, "pause reason")
	})
	tw(t, ledgerFile0, "appendGoalEvent persists audit_skipped with disabled reason and metadata", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("audit_skipped", "g1", "2024-01-01T00:00:00.000Z", "reason", "disabled", "provider", "fireworks", "model", "kimi", "thinkingLevel", "high"))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		e := r.events[0]
		eq(t, evStr(e, "type"), "audit_skipped", "type")
		eq(t, evStr(e, "reason"), "disabled", "reason")
		eq(t, evStr(e, "provider"), "fireworks", "provider")
		eq(t, evStr(e, "model"), "kimi", "model")
		eq(t, evStr(e, "thinkingLevel"), "high", "thinkingLevel")
		eq(t, r.malformed, 0, "malformed")
	})
	tw(t, ledgerFile0, "appendGoalEvent persists audit_skipped with user_aborted reason and minimal metadata", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("audit_skipped", "g1", "2024-01-01T00:00:00.000Z", "reason", "user_aborted"))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		e := r.events[0]
		eq(t, evStr(e, "type"), "audit_skipped", "type")
		eq(t, evStr(e, "reason"), "user_aborted", "reason")
		eq(t, e.has("provider"), false, "provider")
		eq(t, e.has("model"), false, "model")
		eq(t, r.malformed, 0, "malformed")
	})
	tw(t, ledgerFile0, "reconstructGoalLedger handles audit_skipped without changing goal status", func(t *testing.T) {
		state := reconstructGoalLedger([]*jsObject{
			createdEv("g1", "o1", "2024-01-01T00:00:00.000Z"),
			ledgerEv("goal_focused", "g1", "2024-01-01T00:00:01.000Z", "reason", "created"),
			ledgerEv("audit_skipped", "g1", "2024-01-01T00:00:02.000Z", "reason", "disabled", "provider", "fireworks", "model", "kimi"),
			ledgerEv("audit_skipped", "g1", "2024-01-01T00:00:03.000Z", "reason", "user_aborted"),
		})
		// Goal should remain active (not terminal)
		eq(t, state.goals.has("g1"), true, "active")
		eq(t, state.terminalGoals.has("g1"), false, "not terminal")
		g1, _ := state.goals.get("g1")
		eq(t, g1.latestStatus, "active", "status")
		// audit_skipped should NOT set latestAuditorResult
		eq(t, g1.latestAuditorResult == nil, true, "no auditor result")
		// Focus should remain
		eq(t, derefStr(state.focusedGoalID), "g1", "focus")
	})
	tw(t, ledgerFile0, "task_list_set event round-trips correctly", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("task_list_set", "g1", "2026-05-27T00:00:00.000Z", "taskCount", 3.0, "blockCompletion", true))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		e := r.events[0]
		eq(t, evStr(e, "type"), "task_list_set", "type")
		n, _ := e.num("taskCount")
		b, _ := e.flag("blockCompletion")
		eq(t, n, 3.0, "taskCount")
		eq(t, b, true, "blockCompletion")
		eq(t, evStr(e, "goalId"), "g1", "goalId")
	})
	tw(t, ledgerFile0, "task_complete event round-trips correctly", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("task_complete", "g1", "2026-05-27T00:00:00.000Z", "taskId", "t1", "evidence", "all tests pass"))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		e := r.events[0]
		eq(t, evStr(e, "type"), "task_complete", "type")
		eq(t, evStr(e, "taskId"), "t1", "taskId")
		eq(t, evStr(e, "evidence"), "all tests pass", "evidence")
		eq(t, evStr(e, "goalId"), "g1", "goalId")
	})
	tw(t, ledgerFile0, "task_skipped event round-trips correctly", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoalEvent(ctx, ledgerEv("task_skipped", "g1", "2026-05-27T00:00:00.000Z", "taskId", "t1", "reason", "No longer needed"))
		r := readGoalLedger(ctx)
		eq(t, len(r.events), 1, "events")
		e := r.events[0]
		eq(t, evStr(e, "type"), "task_skipped", "type")
		eq(t, evStr(e, "taskId"), "t1", "taskId")
		eq(t, evStr(e, "reason"), "No longer needed", "reason")
		eq(t, evStr(e, "goalId"), "g1", "goalId")
	})
}
