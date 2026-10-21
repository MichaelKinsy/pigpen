package pi_goal_x

import (
	"regexp"
	"strings"
	"testing"
)

const gsFile = "goal-status"

func gsGoal(kv ...any) *jsObject {
	return dmGoal(dmCat([]any{"activePath", ".pi/goals/active_goal_g1.md"}, kv)...)
}

func gsWithTasks(tasks []any, kv ...any) *jsObject {
	return gsGoal(dmCat(kv, dmWithTasks(tasks))...)
}

func gsFiveTasks() []any {
	return dmTasks(
		dmTask("t1", "Review reports page and data source", "status", "complete", "evidence", "Reviewed source"),
		dmTask("t2", "Implement filtered CSV export", "status", "complete"),
		dmTask("t3", "Add the download button",
			"verificationContract", "The button downloads a CSV using the active filters.",
			"subtasks", dmTasks(
				dmTask("t3.1", "Add loading state", "status", "complete"),
				dmTask("t3.2", "Generate timestamped filename", "status", "complete"),
				dmTask("t3.3", "Add error handling"))),
		dmTask("t4", "Add documentation"),
		dmTask("t5", "Add and run tests", "status", "skipped", "skipReason", "Covered by t2"),
	)
}

func gsEvents() []*jsObject {
	return []*jsObject{
		dmObj("type", "goal_created", "goalId", "g1", "objective", "x", "sisyphus", false, "autoContinue", true, "at", "2026-01-01T09:00:00.000Z"),
		dmObj("type", "task_complete", "goalId", "g1", "taskId", "t2", "evidence", "Done", "at", "2026-01-01T09:05:00.000Z"),
		dmObj("type", "task_started", "goalId", "g1", "taskId", "t3.3", "at", "2026-01-01T09:06:00.000Z"),
		dmObj("type", "audit_result", "goalId", "g1", "verdict", "disapproved", "report", "Tests were not run after the final change.", "at", "2026-01-02T10:00:00.000Z"),
	}
}

func gsBase() goalStatusTextOptions {
	return goalStatusTextOptions{
		goal:           gsWithTasks(gsFiveTasks(), "currentTaskId", "t3", "verificationContract", "Run npm test with zero failures."),
		focused:        true,
		otherOpenGoals: 2,
		ledgerEvents:   gsEvents(),
	}
}

func gsMatch(t *testing.T, text, pattern string, msg ...string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("expected /%s/ to match:\n%s", pattern, text)
	}
}

func gsNoMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("expected /%s/ not to match:\n%s", pattern, text)
	}
}

func gsWidthSafe(t *testing.T, text string) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if visibleWidth(line) > goalStatusWidth {
			t.Errorf("line exceeds %d: %q", goalStatusWidth, line)
		}
	}
}

func TestGoalStatusTwins(t *testing.T) {
	tw(t, gsFile, "standard mode renders the compact dashboard from the shared model (§13.1)", func(t *testing.T) {
		text := buildGoalStatusText(gsBase())
		gsMatch(t, text, `╭─ pi-goal-x ─ Add CSV export to reports`)
		gsMatch(t, text, `goal: running \[12m47s 18\.2K\] \(\+2 open\)`)
		// The top-level task list is part of the compact dashboard now (step-11).
		gsMatch(t, text, `├─ Tasks `)
		gsMatch(t, text, `✓ t1  Review reports page and data source`)
		// The five-task list overflows the medium-status width (78 cols).
		gsMatch(t, text, `… \+1 more task`)
		gsMatch(t, text, `Tasks · ✓3 done · 2 open`)
		gsMatch(t, text, `Current  t3 · Add the download button`)
		gsMatch(t, text, `· Sub 2/3 \[.*\]`) // subtask bar sits beside the task bar in the compact header
		gsMatch(t, text, `Verify   Run npm test with zero failures`)
		gsMatch(t, text, `File     \.pi/goals/active_goal_g1\.md`)
	})

	tw(t, gsFile, "standard mode adds current-task details, activity, and the last audit", func(t *testing.T) {
		text := buildGoalStatusText(gsBase())
		// Current-task details block (§13.1 item 2).
		gsMatch(t, text, `├─ Current task `)
		gsMatch(t, text, `Contract: The button downloads a CSV using the active filters`)
		// Recent activity (§13.1 item 3) — ledger-derived, task titles preferred.
		gsMatch(t, text, `├─ Recent activity `)
		gsMatch(t, text, `Started “Add error handling”\.`)
		gsMatch(t, text, `Completed “Implement filtered CSV export”\. — Done`)
		// Last audit result (§13.1 item 4).
		gsMatch(t, text, `Last audit: CHANGES REQUIRED \(2026-01-02\)`)
	})

	tw(t, gsFile, "standard mode includes no effective settings noise by default", func(t *testing.T) {
		text := buildGoalStatusText(gsBase())
		gsNoMatch(t, text, `Effective settings`)
		gsNoMatch(t, text, `provider:`)
	})

	tw(t, gsFile, "standard mode shows the anchored task window — parity with the widget default (§9.6)", func(t *testing.T) {
		// 30 top-level tasks; t5 and t20 completed (t20 latest). The status
		// width (78 cols, medium) budgets 4 task rows, so the anchored window is
		// t17..t20 — the most recently completed task visible, earliest hidden,
		// exactly like the compact widget's default rendering.
		tasks := make([]*jsObject, 30)
		for i := range tasks {
			tasks[i] = dmTask("t"+jsNumber(float64(i+1)), "Task number "+jsNumber(float64(i+1)))
		}
		tasks[4].set("status", "complete")
		tasks[4].set("completedAt", "2026-01-01T10:00:00.000Z")
		tasks[19].set("status", "complete")
		tasks[19].set("completedAt", "2026-01-01T11:00:00.000Z")
		text := buildGoalStatusText(goalStatusTextOptions{goal: gsWithTasks(dmTasks(tasks...), "currentTaskId", "t21"), focused: true})
		gsMatch(t, text, `↑ 16 more tasks`)   // the anchored window hides the earliest tasks
		gsMatch(t, text, `Task number 20`)    // the latest completion is the last visible row
		gsMatch(t, text, `… \+10 more tasks`) // pending tasks after the anchor stay reachable below
		gsNoMatch(t, text, `[✓▸·~] t1\s`)     // the earliest task row is not rendered
		// Width-safe including the indicator rows.
		gsWidthSafe(t, text)
	})

	tw(t, gsFile, "verbose mode carries full diagnostic detail (§13.2)", func(t *testing.T) {
		o := gsBase()
		o.verbose = true
		o.settingsReport = []string{"provider: anthropic (from .pi/pi-goal-x-settings.json)", "disableTasks: false"}
		text := buildGoalStatusText(o)
		gsMatch(t, text, `Goal id: g1`)
		gsMatch(t, text, `Revision: \d+`)
		gsMatch(t, text, `Status: In progress \(focused\)`)
		gsMatch(t, text, `Objective:`)
		gsMatch(t, text, `Add CSV export to reports`)
		// Full task tree with full evidence and contracts.
		gsMatch(t, text, `✓ t1  Review reports page and data source — evidence: Reviewed source`)
		gsMatch(t, text, `▸ t3  Add the download button — contract: The button downloads a CSV`)
		gsMatch(t, text, `~ t5  Add and run tests`)
		// Current task detail.
		gsMatch(t, text, `Current task: t3 · Add the download button`)
		gsMatch(t, text, `subtasks: 2/3 \(67%\)`)
		// Recent ledger history.
		gsMatch(t, text, `Recent ledger:`)
		gsMatch(t, text, `task_started`)
		// Last audit report.
		gsMatch(t, text, `Last audit \(2026-01-02\): disapproved`)
		gsMatch(t, text, `Tests were not run after the final change`)
		// Effective settings with provenance.
		gsMatch(t, text, `Effective settings:`)
		gsMatch(t, text, `provider: anthropic \(from .pi/pi-goal-x-settings.json\)`)
	})

	tw(t, gsFile, "no rendered line of the boxed standard output exceeds the rendering width", func(t *testing.T) {
		gsWidthSafe(t, buildGoalStatusText(gsBase()))
	})

	tw(t, gsFile, "unfocused and no-goal states render correctly", func(t *testing.T) {
		unfocused := buildGoalStatusText(goalStatusTextOptions{otherOpenGoals: 3})
		gsMatch(t, unfocused, `3 open goals are available`)
		gsMatch(t, unfocused, `/goal-focus`)
		none := buildGoalStatusText(goalStatusTextOptions{})
		gsMatch(t, none, `No goal is set`)
	})

	tw(t, gsFile, "complete goal standard output shows the §4.7 message", func(t *testing.T) {
		tasks := gsFiveTasks()
		for _, raw := range tasks {
			raw.(*jsObject).set("status", "complete")
		}
		text := buildGoalStatusText(goalStatusTextOptions{
			goal:    gsWithTasks(tasks, "status", "complete", "activePath", undef{}, "archivedPath", ".pi/goals/archived/goal_g1.md"),
			focused: true,
		})
		gsMatch(t, text, `All required work is complete`)
	})

	tw(t, gsFile, "health mode reports storage, ledger, task, and budget warnings without claiming completion", func(t *testing.T) {
		list := dmObj("tasks", dmTasks(dmTask("t1", "Pending verification")), "blockCompletion", true, "proposedAt", "2026-01-01T00:00:00.000Z")
		absent := false
		text := buildGoalStatusText(goalStatusTextOptions{
			goal:              dmGoal("taskList", list, "tokenBudget", 20000.0, "usage", dmUsage(19500, 10)),
			focused:           true,
			ledgerMalformed:   2,
			health:            true,
			activeFilePresent: &absent,
		})
		gsMatch(t, text, `^Goal health: ERROR`)
		gsMatch(t, text, `ERROR Goal file: missing`)
		gsMatch(t, text, `WARN Ledger: 2 malformed entries`)
		gsMatch(t, text, `WARN Tasks: 0/1 terminal · 1 pending`)
		gsMatch(t, text, `WARN Budget: 19\.5K / 20K \(98%\)`)
		gsMatch(t, text, `not a completion verdict`)
	})

	tw(t, gsFile, "health mode can report an internally healthy goal", func(t *testing.T) {
		present := true
		text := buildGoalStatusText(goalStatusTextOptions{
			goal:              dmGoal("usage", dmUsage(100, 2)),
			focused:           true,
			health:            true,
			activeFilePresent: &present,
		})
		gsMatch(t, text, `^Goal health: OK`)
		gsMatch(t, text, `OK Goal file:`)
		gsMatch(t, text, `OK Ledger: valid`)
	})

	tw(t, gsFile, "health mode explains an unfocused session", func(t *testing.T) {
		text := buildGoalStatusText(goalStatusTextOptions{otherOpenGoals: 2, health: true})
		gsMatch(t, text, `^Goal health: WARN`)
		gsMatch(t, text, `no goal is focused`)
	})
}
