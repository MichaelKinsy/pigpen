package pi_goal_x

import "testing"

const dmFile = "goal-dashboard-model"

func f64(v float64) *float64 { return &v }

func TestGoalDashboardModelTwins(t *testing.T) {
	tw(t, dmFile, "formatDashboardDuration renders compact h/m/s labels", func(t *testing.T) {
		eq(t, formatDashboardDuration(0), "0s", "0")
		eq(t, formatDashboardDuration(767), "12m47s", "767")
		eq(t, formatDashboardDuration(3661), "1h01m01s", "3661")
	})

	tw(t, dmFile, "formatCompactTokens renders compact token labels", func(t *testing.T) {
		eq(t, formatCompactTokens(0), "0", "0")
		eq(t, formatCompactTokens(999), "999", "999")
		eq(t, formatCompactTokens(1200), "1.2K", "1200")
		eq(t, formatCompactTokens(18200), "18.2K", "18200")
		eq(t, formatCompactTokens(2_500_000), "2.5M", "2.5M")
	})

	tw(t, dmFile, "formatBudget summarizes used/total/percentage", func(t *testing.T) {
		eq(t, formatBudget(18200, 50000), "18.2K / 50K · 36%", "budget")
		eq(t, formatBudget(0, 0), "0 / 0 · 0%", "zero budget")
	})

	tw(t, dmFile, "auditorEnabled derives from the goal's persisted skipAuditor (§auditor-toggle)", func(t *testing.T) {
		// Unset/absent → on (the global default); skipAuditor: true → off.
		eq(t, dmModel(t).auditorEnabled, true, "no per-goal setting keeps the auditor on")
		eq(t, dmModel(t, "skipAuditor", true).auditorEnabled, false, "skipAuditor: true turns the auditor off")
	})

	tw(t, dmFile, "status maps lifecycle states to explicit display codes", func(t *testing.T) {
		eq(t, deriveGoalStatus(dmGoal()), dashboardGoalStatus{code: "running", label: "In progress", footerLabel: "running"}, "running")
		eq(t, deriveGoalStatus(dmGoal("autoContinue", false)), dashboardGoalStatus{code: "idle", label: "Idle", footerLabel: "active"}, "idle")
		eq(t, deriveGoalStatus(dmGoal("status", "paused", "stopReason", "agent")), dashboardGoalStatus{code: "paused", label: "Paused (agent)", footerLabel: "paused (agent)"}, "paused agent")
		eq(t, deriveGoalStatus(dmGoal("status", "paused", "stopReason", "user")), dashboardGoalStatus{code: "paused", label: "Paused (user)", footerLabel: "paused"}, "paused user")
		eq(t, deriveGoalStatus(dmGoal("status", "blocked", "pauseReason", "Build fails", "pauseSuggestedAction", "Run npm test")),
			dashboardGoalStatus{code: "blocked", label: "Blocked", footerLabel: "blocked", reason: "Build fails", suggestedAction: "Run npm test"}, "blocked")
		eq(t, deriveGoalStatus(dmGoal("status", "budget_limited")), dashboardGoalStatus{code: "budget_limited", label: "Budget limited", footerLabel: "budget limited"}, "budget limited")
		eq(t, deriveGoalStatus(dmGoal("status", "complete")), dashboardGoalStatus{code: "complete", label: "Complete", footerLabel: "complete"}, "complete")
	})

	tw(t, dmFile, "active goal without tasks has no progress sections", func(t *testing.T) {
		m := dmModel(t)
		eq(t, m.title, "Add CSV export to reports", "title")
		eq(t, m.status.code, "running", "status code")
		eq(t, m.taskProgress, (*taskProgress)(nil), "taskProgress")
		eq(t, m.taskTree, []dashboardTaskNode{}, "taskTree")
		eq(t, m.currentTask, (*dashboardCurrentTask)(nil), "currentTask")
	})

	tw(t, dmFile, "partial tasks derive 3/5 · 60% with skipped counted as done", func(t *testing.T) {
		tasks := append(dmFiveTaskList(), dmTask("t6", "Legacy fallback", "status", "skipped"))
		m := dmModel(t, dmWithTasks(tasks)...)
		eq(t, m.taskProgress, &taskProgress{3, 6, 50}, "progress")
		// skipped is tracked separately in the tree but counts toward progress
		for _, n := range m.taskTree {
			if n.id == "t6" {
				eq(t, n.status, "skipped", "t6 status")
				return
			}
		}
		t.Fatal("t6 missing from the tree")
	})

	tw(t, dmFile, "all top-level tasks complete derive 100% and no current task", func(t *testing.T) {
		tasks := dmFiveTaskList()
		for _, raw := range tasks {
			task := raw.(*jsObject)
			task.set("status", "complete")
			if subs, ok := task.vals["subtasks"].([]any); ok {
				for _, s := range subs {
					s.(*jsObject).set("status", "complete")
				}
			}
		}
		m := dmModel(t, dmWithTasks(tasks)...)
		eq(t, m.taskProgress, &taskProgress{5, 5, 100}, "progress")
		eq(t, m.currentTask, (*dashboardCurrentTask)(nil), "currentTask")
	})

	tw(t, dmFile, "top-level progress counts only top-level tasks", func(t *testing.T) {
		tasks := dmTasks(dmTask("a", "A", "status", "complete"), dmTask("b", "B", "subtasks", dmTasks(dmTask("b.1", "B1", "status", "complete"))))
		eq(t, deriveTopLevelTaskProgress(dmGoal(dmWithTasks(tasks)...)), &taskProgress{1, 2, 50}, "progress")
	})

	tw(t, dmFile, "flattenTaskTree walks the tree recursively with depth and current marker", func(t *testing.T) {
		nodes := flattenTaskTree(dmFiveTaskList(), "t3.3")
		ids := []string{}
		for _, n := range nodes {
			ids = append(ids, n.id)
		}
		eq(t, ids, []string{"t1", "t2", "t3", "t3.1", "t3.2", "t3.3", "t4", "t5"}, "ids")
		find := func(id string) dashboardTaskNode {
			for _, n := range nodes {
				if n.id == id {
					return n
				}
			}
			t.Fatalf("no node %s", id)
			return dashboardTaskNode{}
		}
		eq(t, find("t3.3").depth, 1, "t3.3 depth")
		eq(t, find("t3").depth, 0, "t3 depth")
		eq(t, find("t3.3").isCurrent, true, "t3.3 current")
		current := 0
		for _, n := range nodes {
			if n.isCurrent {
				current++
			}
		}
		eq(t, current, 1, "current count")
	})

	tw(t, dmFile, "flattenTaskTree with no tasks returns an empty tree", func(t *testing.T) {
		eq(t, flattenTaskTree(nil, ""), []dashboardTaskNode{}, "nil")
		eq(t, flattenTaskTree([]any{}, ""), []dashboardTaskNode{}, "empty")
	})

	tw(t, dmFile, "tree nodes carry verification contracts and evidence", func(t *testing.T) {
		nodes := flattenTaskTree(dmFiveTaskList(), "")
		eq(t, nodes[0].evidence, "Reviewed source", "t1 evidence")
		eq(t, nodes[2].verificationContract, "The button downloads a CSV using the active filters.", "t3 contract")
	})

	tw(t, dmFile, "tree nodes carry direct-child subtask counts for the compact marker", func(t *testing.T) {
		nodes := flattenTaskTree(dmFiveTaskList(), "")
		byID := map[string]dashboardTaskNode{}
		for _, n := range nodes {
			byID[n.id] = n
		}
		// t3 has three direct children, two complete → 2/3; skipped counts as done.
		eq(t, byID["t3"].totalSubtasks, 3, "t3 total")
		eq(t, byID["t3"].completedSubtasks, 2, "t3 completed")
		// Leaves carry zero counts.
		for _, id := range []string{"t1", "t2", "t4", "t5"} {
			eq(t, byID[id].totalSubtasks, 0, id+" total")
			eq(t, byID[id].completedSubtasks, 0, id+" completed")
		}
		// Subtask nodes are leaves too.
		eq(t, byID["t3.1"].totalSubtasks, 0, "t3.1 total")
	})

	tw(t, dmFile, "tree node subtask counts count skipped children as done", func(t *testing.T) {
		tasks := dmTasks(dmTask("a", "A", "subtasks", dmTasks(
			dmTask("a.1", "A1", "status", "complete"),
			dmTask("a.2", "A2", "status", "skipped", "skipReason", "Covered"),
			dmTask("a.3", "A3"))))
		node := flattenTaskTree(tasks, "")[0]
		eq(t, node.totalSubtasks, 3, "total")
		eq(t, node.completedSubtasks, 2, "completed")
	})

	tw(t, dmFile, "latestCompletedNodeIndex picks the max completedAt and ties resolve to the last in plan order", func(t *testing.T) {
		nodes := dmNodes(map[string]string{"t1": "2026-01-01T10:00:00.000Z", "t2": "2026-01-01T11:00:00.000Z", "t5": "2026-01-01T09:00:00.000Z"})
		eq(t, latestCompletedNodeIndex(nodes), 1, "t2")
		tied := dmNodes(map[string]string{"t2": "2026-01-01T11:00:00.000Z", "t5": "2026-01-01T11:00:00.000Z"})
		eq(t, latestCompletedNodeIndex(tied), 4, "t5 wins the tie (last position)")
	})

	tw(t, dmFile, "latestCompletedNodeIndex returns -1 without completed/timestamped tasks", func(t *testing.T) {
		eq(t, latestCompletedNodeIndex(nil), -1, "empty")
		eq(t, latestCompletedNodeIndex(dmNodes(nil)), -1, "none completed")
		// status complete but no completedAt (legacy) does not qualify
		eq(t, latestCompletedNodeIndex([]dashboardTaskNode{{id: "t1", title: "A", status: "complete"}}), -1, "legacy")
		// skipped tasks never qualify
		eq(t, latestCompletedNodeIndex([]dashboardTaskNode{{id: "t1", title: "A", status: "skipped", completedAt: "2026-01-01T10:00:00.000Z"}}), -1, "skipped")
	})

	tw(t, dmFile, "anchoredScrollOffset bottom-anchors the latest completion as the last visible row", func(t *testing.T) {
		// t5 completed last (index 4); a 3-row window ends at t5 → offset 2
		nodes := dmNodes(map[string]string{"t1": "2026-01-01T10:00:00.000Z", "t2": "2026-01-01T11:00:00.000Z", "t5": "2026-01-01T12:00:00.000Z"})
		eq(t, anchoredScrollOffset(nodes, 3), 2, "rows t3..t5")
		eq(t, anchoredScrollOffset(nodes, 5), 0, "t5 already the bottom row of the initial window")
		// anchor near the end clamps to the tail
		eq(t, anchoredScrollOffset(dmNodes(map[string]string{"t6": "2026-01-01T12:00:00.000Z"}), 3), 3, "tail")
		// early completion with everything else pending stays at the top
		eq(t, anchoredScrollOffset(dmNodes(map[string]string{"t2": "2026-01-01T10:00:00.000Z"}), 5), 0, "early")
		// no completions → top
		eq(t, anchoredScrollOffset(dmNodes(nil), 3), 0, "none")
		// list fits entirely → top
		eq(t, anchoredScrollOffset(dmNodes(map[string]string{"t3": "2026-01-01T10:00:00.000Z"}), 7), 0, "fits")
		// long list: the most recent completion (t12) pulls the window to the tail,
		// hiding the earliest tasks — the core ask
		longList := make([]dashboardTaskNode, 12)
		for i := range longList {
			id := "t" + jsNumber(float64(i+1))
			longList[i] = dashboardTaskNode{id: id, title: "Task " + jsNumber(float64(i+1)), status: "pending"}
		}
		longList[1].status, longList[1].completedAt = "complete", "2026-01-01T10:00:00.000Z"
		longList[11].status, longList[11].completedAt = "complete", "2026-01-01T11:00:00.000Z"
		eq(t, anchoredScrollOffset(longList, 5), 7, "rows t8..t12")
	})

	tw(t, dmFile, "clampScrollOffset and maxScrollOffset bound the window", func(t *testing.T) {
		eq(t, maxScrollOffset(7, 5), 2, "max 7/5")
		eq(t, maxScrollOffset(3, 5), 0, "max 3/5")
		eq(t, clampScrollOffset(-3, 7, 5), 0, "clamp -3")
		eq(t, clampScrollOffset(99, 7, 5), 2, "clamp 99")
		eq(t, clampScrollOffset(1.9, 7, 5), 1, "clamp 1.9")
		eq(t, clampScrollOffset(2, 3, 5), 0, "clamp 2 of 3")
	})

	tw(t, dmFile, "compactTaskViewportRows matches the §5.5 width buckets", func(t *testing.T) {
		for _, c := range [][2]int{{140, 5}, {100, 5}, {99, 4}, {70, 4}, {69, 3}, {50, 3}, {49, 2}, {40, 2}} {
			eq(t, compactTaskViewportRows(c[0]), c[1], "width "+jsNumber(float64(c[0])))
		}
	})

	tw(t, dmFile, "taskViewportPageSize is one viewport of rows", func(t *testing.T) {
		eq(t, taskViewportPageSize(5), 5, "5")
		eq(t, taskViewportPageSize(1), 1, "1")
		eq(t, taskViewportPageSize(0), 1, "0")
	})

	tw(t, dmFile, "deriveTaskListViewport computes the window with hidden counts", func(t *testing.T) {
		eq(t, deriveTaskListViewport(9, 5, 2), taskListViewport{9, 5, 2, 4, 2, 2}, "9/5/2")
		eq(t, deriveTaskListViewport(9, 5, 99), taskListViewport{9, 5, 4, 4, 4, 0}, "9/5/99")
		eq(t, deriveTaskListViewport(4, 5, 3), taskListViewport{4, 5, 0, 0, 0, 0}, "4/5/3")
		eq(t, deriveTaskListViewport(0, 5, 0), taskListViewport{0, 5, 0, 0, 0, 0}, "0/5/0")
	})

	tw(t, dmFile, "persisted current top-level task is resolved without inference", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"currentTaskId", "t3"})...)
		eq(t, m.currentTask.id, "t3", "id")
		eq(t, m.currentTask.title, "Add the download button", "title")
		eq(t, m.currentTask.depth, 0, "depth")
		eq(t, m.currentTask.inferred, false, "inferred")
		for _, n := range m.taskTree {
			if n.id == "t3" {
				eq(t, n.isCurrent, true, "t3 current")
			}
		}
	})

	tw(t, dmFile, "current nested subtask is resolved with its depth", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"currentTaskId", "t3.3"})...)
		eq(t, m.currentTask.id, "t3.3", "id")
		eq(t, m.currentTask.depth, 1, "depth")
		for _, n := range m.taskTree {
			if n.id == "t3.3" {
				eq(t, n.isCurrent, true, "t3.3 current")
			}
		}
	})

	tw(t, dmFile, "current parent task shows direct-child subtask progress", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"currentTaskId", "t3"})...)
		eq(t, [3]int{m.currentTask.completedSubtasks, m.currentTask.totalSubtasks, m.currentTask.subtaskPercentage}, [3]int{2, 3, 67}, "progress")
	})

	tw(t, dmFile, "current leaf task omits subtask progress (all-zero totals)", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"currentTaskId", "t4"})...)
		eq(t, m.currentTask.totalSubtasks, 0, "total")
		eq(t, m.currentTask.subtaskPercentage, 0, "percentage")
	})

	tw(t, dmFile, "invalid currentTaskId falls back to the first pending task and is marked inferred", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"currentTaskId", "t999"})...)
		eq(t, m.currentTask.inferred, true, "inferred")
		eq(t, m.currentTask.id, "t3", "first pending in tree order")
		eq(t, m.currentTask.completedSubtasks, 2, "subtask progress still derives")
	})

	tw(t, dmFile, "removed current task falls back to first pending", func(t *testing.T) {
		tasks := dmTasks(dmTask("a", "A", "status", "complete"), dmTask("b", "B"))
		m := dmModel(t, dmCat(dmWithTasks(tasks), []any{"currentTaskId", "vanished"})...)
		eq(t, m.currentTask.id, "b", "id")
		eq(t, m.currentTask.inferred, true, "inferred")
	})

	tw(t, dmFile, "currentTaskId pointing at a completed task is not accepted", func(t *testing.T) {
		tasks := dmTasks(dmTask("a", "A", "status", "complete"), dmTask("b", "B"))
		m := dmModel(t, dmCat(dmWithTasks(tasks), []any{"currentTaskId", "a"})...)
		eq(t, m.currentTask.id, "b", "id")
	})

	tw(t, dmFile, "deriveCurrentTask returns undefined when nothing is pending", func(t *testing.T) {
		tasks := dmTasks(dmTask("a", "A", "status", "complete"))
		nodes := flattenTaskTree(tasks, "")
		eq(t, deriveCurrentTask(dmGoal(dmWithTasks(tasks)...), nodes), (*dashboardCurrentTask)(nil), "current")
	})

	tw(t, dmFile, "deriveCurrentTaskSubtaskProgress uses direct children of the parent", func(t *testing.T) {
		eq(t, deriveCurrentTaskSubtaskProgress("t3", dmFiveTaskList()), currentTaskSubtaskProgress{2, 3, 67}, "t3")
	})

	tw(t, dmFile, "deriveCurrentTaskSubtaskProgress omits the ratio for a leaf", func(t *testing.T) {
		eq(t, deriveCurrentTaskSubtaskProgress("t4", dmFiveTaskList()), currentTaskSubtaskProgress{0, 0, 0}, "t4")
	})

	tw(t, dmFile, "goal-level and task-level verification contracts are surfaced", func(t *testing.T) {
		m := dmModel(t, dmCat(dmWithTasks(dmFiveTaskList()), []any{"verificationContract", "Run npm test with zero failures.", "currentTaskId", "t3"})...)
		eq(t, m.goalVerificationContract, "Run npm test with zero failures.", "goal contract")
		eq(t, m.currentTask.verificationContract, "The button downloads a CSV using the active filters.", "task contract")
	})

	tw(t, dmFile, "other open goals and focus state are reflected", func(t *testing.T) {
		m := dmModelOpts(t, dmWithTasks(dmFiveTaskList()), goalDashboardModelOptions{focused: false, otherOpenGoals: 2})
		eq(t, m.focused, false, "focused")
		eq(t, m.otherOpenGoals, 2, "otherOpenGoals")
	})

	tw(t, dmFile, "filePath prefers the active path and falls back to the archive path", func(t *testing.T) {
		m := dmModel(t, "activePath", ".pi/goals/active_goal_g1.md")
		eq(t, m.filePath, ".pi/goals/active_goal_g1.md", "active")
		archived := dmModel(t, "status", "complete", "activePath", undef{}, "archivedPath", ".pi/goals/archived/goal_g1.md")
		eq(t, archived.filePath, ".pi/goals/archived/goal_g1.md", "archived")
	})

	tw(t, dmFile, "no goal record derives a null model", func(t *testing.T) {
		eq(t, deriveGoalDashboardModel(nil, goalDashboardModelOptions{}), (*goalDashboardModel)(nil), "nil model")
	})

	tw(t, dmFile, "token budget derives used/total/percentage/remaining", func(t *testing.T) {
		m := dmModel(t, "tokenBudget", 50000.0)
		eq(t, m.budget, &dashboardBudget{used: 18200, total: 50000, percentage: 36, remaining: 31800}, "budget")
	})

	tw(t, dmFile, "budget percentage clamps to 100 and remaining to zero", func(t *testing.T) {
		m := dmModel(t, "tokenBudget", 10000.0)
		eq(t, m.budget, &dashboardBudget{used: 18200, total: 10000, percentage: 100, remaining: 0}, "budget")
	})

	tw(t, dmFile, "no budget means no budget section", func(t *testing.T) {
		eq(t, dmModel(t).budget, (*dashboardBudget)(nil), "budget")
	})

	tw(t, dmFile, "usage derives footer-status bits (compact duration + compact tokens)", func(t *testing.T) {
		m := dmModel(t)
		eq(t, m.usage.activeSeconds, 767.0, "seconds")
		eq(t, m.usage.tokens, 18200.0, "tokens")
		eq(t, m.usage.footerBits, "12m47s 18.2K", "bits")
	})

	tw(t, dmFile, "recent activity is derived from the durable ledger", func(t *testing.T) {
		events := []*jsObject{
			dmEv("goal_created", "2026-01-01T09:00:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
			dmEv("task_complete", "2026-01-01T09:05:00.000Z", "taskId", "t2", "evidence", "Done"),
			dmEv("task_started", "2026-01-01T09:06:00.000Z", "taskId", "t3"),
		}
		m := dmModelOpts(t, dmWithTasks(dmFiveTaskList()), goalDashboardModelOptions{focused: true, ledgerEvents: events})
		texts := []string{}
		for _, a := range m.recentActivity {
			texts = append(texts, a.text)
		}
		eq(t, texts, []string{"Created and focused the goal.", "Completed “Implement filtered CSV export”. — Done", "Started “Add the download button”."}, "texts")
	})

	tw(t, dmFile, "activity respects the configured limit and excludes other goals", func(t *testing.T) {
		other := dmEv("goal_created", "2026-01-01T09:01:00.000Z", "objective", "other", "sisyphus", false, "autoContinue", true)
		other.set("goalId", "g2")
		events := []*jsObject{
			dmEv("goal_created", "2026-01-01T09:00:00.000Z", "objective", "x", "sisyphus", false, "autoContinue", true),
			dmEv("task_complete", "2026-01-01T09:05:00.000Z", "taskId", "t1"),
			dmEv("task_complete", "2026-01-01T09:06:00.000Z", "taskId", "t2"),
			other,
		}
		limit := 2
		m := dmModelOpts(t, dmWithTasks(dmFiveTaskList()), goalDashboardModelOptions{focused: true, ledgerEvents: events, activityLimit: &limit})
		texts := []string{}
		for _, a := range m.recentActivity {
			texts = append(texts, a.text)
		}
		eq(t, texts, []string{"Completed “Review reports page and data source”.", "Completed “Implement filtered CSV export”."}, "texts")
	})
}
