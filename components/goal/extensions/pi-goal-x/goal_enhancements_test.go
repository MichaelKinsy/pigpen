package pi_goal_x

import (
	"strings"
	"testing"
)

// Twins of tests/goal-enhancements.test.ts (E1/E4/E6/E7).

func enhGoalRecord(objective string, sisyphus bool) *jsObject {
	return createGoal(objective, true, sisyphus, false, ms(2026, 9, 5, 0, 0, 0))
}

func enhLedgerEvents(goalID string) []*jsObject {
	return []*jsObject{
		wgObj("type", "goal_created", "goalId", goalID, "objective", "x", "sisyphus", false, "autoContinue", true, "at", "2026-08-05T09:00:00.000Z"),
		wgObj("type", "audit_result", "goalId", goalID, "verdict", "disapproved", "report", "missing evidence for criterion 3", "at", "2026-08-05T10:00:00.000Z"),
		wgObj("type", "task_complete", "goalId", goalID, "taskId", "t1", "evidence", "verified", "at", "2026-08-05T10:01:00.000Z"),
	}
}

func TestGoalEnhancements(t *testing.T) {
	f := "goal-enhancements"
	theme := createMockTheme()
	tw(t, f, "surfaces the last audit verdict and recent lifecycle events", func(t *testing.T) {
		goal := enhGoalRecord("History test", false)
		block := buildGoalHistoryBlock(goal, enhLedgerEvents(gstr(goal, "id")))
		reMatch(t, `Last audit: disapproved`, block)
		reMatch(t, `missing evidence for criterion 3`, block)
		reMatch(t, `Recent events:`, block)
		reMatch(t, `task_complete`, block)
	})
	tw(t, f, "counts ordered steps from numbered markers", func(t *testing.T) {
		eq(t, countOrderedSteps("1. do this\n2. do that\n3. done"), 3, "numbered")
		eq(t, countOrderedSteps("Step 1: a\nStep 2: b"), 2, "Step N")
		eq(t, countOrderedSteps("no markers here"), 0, "none")
	})
	tw(t, f, "derives the current step from completed top-level tasks", func(t *testing.T) {
		goal := enhGoalRecord("1. a\n2. b\n3. c", true)
		goal.set("taskList", wgTaskList(wgTask("t1", "a", "complete"), wgTask("t2", "b", "pending"), wgTask("t3", "c", "pending")))
		current, total, ok := sisyphusStepProgress(goal)
		eq(t, []any{current, total, ok}, []any{2, 3, true}, "steps")
	})
	tw(t, f, "returns null for non-sisyphus goals", func(t *testing.T) {
		_, _, ok := sisyphusStepProgress(enhGoalRecord("1. a", false))
		eq(t, ok, false, "null")
	})
	tw(t, f, "keeps the collapsed heading byte-identical and adds the detail when expanded", func(t *testing.T) {
		goal := enhGoalRecord("Pause test", false)
		detail := "Pause reason: waiting on the user\nSuggested action: run /goal-tweak"
		result := goalToolResult{
			content: []goalToolContent{{typ: "text", text: "Goal paused by the agent: waiting on the user. Stop now.", hasText: true}},
			details: goalDetails(goal, detail),
		}
		collapsed := strings.TrimSpace(strings.Join(renderGoalResult(result, false, theme).Render(200), "\n"))
		reMatch(t, `^Goal running - Pause test$`, collapsed)
		expanded := strings.Join(renderGoalResult(result, true, theme).Render(200), "\n")
		reMatch(t, `Pause reason: waiting on the user`, expanded)
		reMatch(t, `Suggested action: run /goal-tweak`, expanded)
	})
	tw(t, f, "renders used/total with remaining when a budget is set", func(t *testing.T) {
		goal := enhGoalRecord("Budget test", false)
		goal.set("tokenBudget", 100000.0)
		goal.set("usage", wgObj("tokensUsed", 45000.0, "activeSeconds", 0.0))
		lines := renderGoalWidgetLines(goal, theme, 100, goalWidgetLinesOptions{openGoalCount: 1})
		reMatch(t, `Budget 45K / 100K · 45%`, strings.Join(lines, "\n"))
	})
	tw(t, f, "omits the budget line when no budget is set", func(t *testing.T) {
		goal := enhGoalRecord("No budget", false)
		lines := renderGoalWidgetLines(goal, theme, 100, goalWidgetLinesOptions{openGoalCount: 1})
		if strings.Contains(strings.Join(lines, "\n"), "Budget") {
			t.Error("budget line shown without a budget")
		}
	})
}
