package pi_goal_x

import "testing"

// Fixtures shared by the goal-dashboard-model and goal-status twins (tests/goal-dashboard-model.test.ts, goal-status.test.ts).

func dmObj(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func dmTask(id, title string, kv ...any) *jsObject {
	o := dmObj("id", id, "title", title, "status", "pending")
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func dmTasks(ts ...*jsObject) []any {
	out := make([]any, len(ts))
	for i, t := range ts {
		out[i] = t
	}
	return out
}

func dmUsage(tokens, seconds float64) *jsObject {
	return dmObj("tokensUsed", tokens, "activeSeconds", seconds)
}

func dmGoal(kv ...any) *jsObject {
	o := dmObj("id", "g1", "objective", "Add CSV export to reports", "status", "active", "autoContinue", true,
		"usage", dmUsage(18200, 767), "sisyphus", false,
		"createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z")
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func dmTaskList(tasks []any) *jsObject {
	return dmObj("tasks", tasks, "blockCompletion", false, "proposedAt", "2026-01-01T00:00:00.000Z")
}

// dmWithTasks is withTasks(): the override pairs that attach a task list.
func dmWithTasks(tasks []any) []any { return []any{"taskList", dmTaskList(tasks)} }

func dmCat(parts ...[]any) []any {
	var out []any
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func dmEv(typ, at string, kv ...any) *jsObject {
	o := dmObj("type", typ, "goalId", "g1", "at", at)
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func dmFiveTaskList() []any {
	return dmTasks(
		dmTask("t1", "Review reports page and data source", "status", "complete", "evidence", "Reviewed source"),
		dmTask("t2", "Implement filtered CSV export", "status", "complete"),
		dmTask("t3", "Add the download button",
			"verificationContract", "The button downloads a CSV using the active filters.",
			"subtasks", dmTasks(
				dmTask("t3.1", "Add loading state", "status", "complete", "evidence", "Loading state added"),
				dmTask("t3.2", "Generate timestamped filename", "status", "complete"),
				dmTask("t3.3", "Add error handling"))),
		dmTask("t4", "Add documentation"),
		dmTask("t5", "Add and run tests"),
	)
}

// dmModel is model(overrides) with the default options (focused, no other goals); dmModelOpts passes the options as given.
func dmModel(t *testing.T, overrides ...any) *goalDashboardModel {
	t.Helper()
	return dmModelOpts(t, overrides, goalDashboardModelOptions{focused: true})
}

func dmModelOpts(t *testing.T, overrides []any, opts goalDashboardModelOptions) *goalDashboardModel {
	t.Helper()
	m := deriveGoalDashboardModel(dmGoal(overrides...), opts)
	if m == nil {
		t.Fatal("deriveGoalDashboardModel returned null for a non-null goal")
	}
	return m
}

func dmNodes(completed map[string]string) []dashboardTaskNode {
	out := []dashboardTaskNode{}
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"} {
		n := dashboardTaskNode{id: id, title: "Task " + id, status: "pending"}
		if at, ok := completed[id]; ok {
			n.status, n.completedAt = "complete", at
		}
		out = append(out, n)
	}
	return out
}
