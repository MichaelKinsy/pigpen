package pi_goal_x_test

// Twins of tests/goal-task-tools.test.ts and tests/goal-task-lifecycle.test.ts that drive the tools through the whole extension.

import (
	"os"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gttFile = "goal-task-tools"
const gtlFile = "goal-task-lifecycle"

func nowZ() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// fixtureWithTasks is the original's fixtureWithTasks: tasks is the JSON array of task objects.
func fixtureWithTasks(t *testing.T, objective, tasks string, extra string) toolsFixture {
	t.Helper()
	overrides := `"taskList":{"tasks":` + tasks + `,"blockCompletion":false,"proposedAt":"` + nowZ() + `"}`
	if extra != "" {
		overrides += "," + extra
	}
	return newToolsFixture(t, objective, false, goalAt(2026, time.August, 5, 9, 0, 0), "{"+overrides+"}")
}

func startedRig(t *testing.T, f toolsFixture, opts rigOptions) *goalRig {
	t.Helper()
	r := newToolsRig(t, f, opts)
	r.Fire("session_start", map[string]any{"reason": "start"})
	r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "go", "systemPromptOptions": map[string]any{}})
	return r
}

func listTasks(g map[string]any) []any { return tasksOf(g) }

func TestGoalTaskToolsRig(t *testing.T) {
	tw(t, gttFile, "set_goal_tasks sets a structural task tree (headless auto-confirm)", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[]`, "")
		r := startedRig(t, f, rigOptions{})
		out := r.callTool("set_goal_tasks", map[string]any{
			"tasks":            []any{map[string]any{"id": "t1", "title": "Task one"}, map[string]any{"id": "t2", "title": "Task two", "parent_id": "t1"}},
			"block_completion": true,
		})
		if !out.terminated() {
			t.Error("structural change terminates the turn")
		}
		g := goal.ToolsActiveGoal(f.cwd)
		tasks := listTasks(g)
		equal(t, len(tasks), 1, "root task")
		equal(t, tasks[0].(map[string]any)["id"], "t1", "id")
		equal(t, tasks[0].(map[string]any)["subtasks"].([]any)[0].(map[string]any)["id"], "t2", "child")
		equal(t, g["taskList"].(map[string]any)["blockCompletion"], true, "blockCompletion")
		if !hasEvent(f.cwd, "task_list_set") {
			t.Error("task_list_set ledger event")
		}
	})

	tw(t, gttFile, "update_goal_task(complete) marks a task complete with evidence and ledger", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"t1","title":"Task one","status":"pending"}]`, "")
		r := startedRig(t, f, rigOptions{})
		out := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "verified"})
		if out.Terminate != nil {
			t.Error("task update does not terminate the turn")
		}
		task := listTasks(goal.ToolsActiveGoal(f.cwd))[0].(map[string]any)
		equal(t, task["status"], "complete", "status")
		equal(t, task["evidence"], "verified", "evidence")
		if !hasEvent(f.cwd, "task_complete") {
			t.Error("task_complete ledger event")
		}
	})

	tw(t, gttFile, "update_goal_task(complete) requires evidence for contracted tasks", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"t1","title":"Task one","status":"pending","verificationContract":"Run the check."}]`, "")
		r := startedRig(t, f, rigOptions{})
		out := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete"})
		textContains(t, out.Content, "verification contract", "evidence required for contracted task")
		equal(t, listTasks(goal.ToolsActiveGoal(f.cwd))[0].(map[string]any)["status"], "pending", "task unchanged")
	})

	tw(t, gttFile, "update_goal_task(skipped) requires a reason and cascades to subtasks", func(t *testing.T) {
		f := fixtureWithTasks(t, "Skip test", `[{"id":"t1","title":"Parent","status":"pending","subtasks":[{"id":"t2","title":"Child","status":"pending"}]}]`, "")
		r := startedRig(t, f, rigOptions{})
		noReason := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "skipped"})
		textContains(t, noReason.Content, "requires a non-empty reason", "reason required")
		ok := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "skipped", "reason": "user direction"})
		textContains(t, ok.Content, "skipped", "skipped")
		parent := listTasks(goal.ToolsActiveGoal(f.cwd))[0].(map[string]any)
		equal(t, parent["status"], "skipped", "parent skipped")
		equal(t, parent["subtasks"].([]any)[0].(map[string]any)["status"], "skipped", "subtask cascaded")
		if !hasEvent(f.cwd, "task_skipped") {
			t.Error("task_skipped event")
		}
	})

	tw(t, gttFile, "update_goal_task(pending) reopens a skipped task; completed tasks are immutable", func(t *testing.T) {
		f := fixtureWithTasks(t, "Reopen test", `[{"id":"sk","title":"Skipped","status":"skipped","skipReason":"later","skippedAt":"`+nowZ()+`"},`+
			`{"id":"done","title":"Done","status":"complete","evidence":"y","completedAt":"`+nowZ()+`"}]`, "")
		r := startedRig(t, f, rigOptions{})
		reopen := r.callTool("update_goal_task", map[string]any{"task_id": "sk", "status": "pending"})
		textContains(t, reopen.Content, "reopened", "reopened")
		immutable := r.callTool("update_goal_task", map[string]any{"task_id": "done", "status": "pending"})
		textContains(t, immutable.Content, "cannot be reopened", "immutable")
		tasks := listTasks(goal.ToolsActiveGoal(f.cwd))
		equal(t, taskByID(tasks, "sk")["status"], "pending", "skipped task reopened")
		equal(t, taskByID(tasks, "done")["status"], "complete", "completed task immutable")
	})

	tw(t, gttFile, "set_goal_tasks preserves an external disk edit made between confirmation and apply", func(t *testing.T) {
		// Session starts with t1/t2 pending. An external edit marks t2 complete on disk. set_goal_tasks then replaces the tree
		// (headless auto-confirm). The disk-fresh merge must preserve the external t2 progress.
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"t1","title":"T1","status":"pending"},{"id":"t2","title":"T2","status":"pending"}]`, "")
		r := startedRig(t, f, rigOptions{})
		// External edit: mark t2 complete directly on disk.
		disk := goal.ToolsActiveGoal(f.cwd)
		for _, x := range listTasks(disk) {
			if m := x.(map[string]any); m["id"] == "t2" {
				m["status"], m["evidence"], m["completedAt"] = "complete", "external", nowZ()
			}
		}
		goal.ToolsWriteGoal(f.cwd, jsonOf(disk))
		r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "T1"}, map[string]any{"id": "t2", "title": "T2"}}})
		tasks := listTasks(goal.ToolsActiveGoal(f.cwd))
		equal(t, taskByID(tasks, "t2")["status"], "complete", "external progress preserved by the disk-fresh merge")
		equal(t, taskByID(tasks, "t2")["evidence"], "external", "external evidence preserved")
		equal(t, taskByID(tasks, "t1")["status"], "pending", "unchanged task stays pending")
		events := eventsOfType(f.cwd, "task_list_set")
		if len(events) == 0 {
			t.Fatal("task_list_set recorded")
		}
		equal(t, events[0]["taskCount"], 2, "taskCount counts all nodes")
	})

	tw(t, gttFile, "update_goal_task updates only the requested path and preserves concurrent disk tasks", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"t1","title":"T1","status":"pending"}]`, "")
		r := startedRig(t, f, rigOptions{})
		// Concurrent external edit: add a brand-new task to the disk tree.
		disk := goal.ToolsActiveGoal(f.cwd)
		tl := disk["taskList"].(map[string]any)
		tl["tasks"] = append(tl["tasks"].([]any), map[string]any{"id": "ext", "title": "External task", "status": "pending"})
		goal.ToolsWriteGoal(f.cwd, jsonOf(disk))
		r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "verified"})
		tasks := listTasks(goal.ToolsActiveGoal(f.cwd))
		equal(t, taskByID(tasks, "t1")["status"], "complete", "requested task updated")
		equal(t, taskByID(tasks, "ext")["status"], "pending", "concurrent disk task preserved")
		equal(t, taskByID(tasks, "ext")["id"], "ext", "concurrent disk task still present")
	})

	tw(t, gttFile, "update_goal_task returns a typed failure for a task removed on disk", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"t1","title":"T1","status":"pending"}]`, "")
		r := startedRig(t, f, rigOptions{})
		// External edit removes t1 from disk (leaving another task so the list exists).
		disk := goal.ToolsActiveGoal(f.cwd)
		disk["taskList"].(map[string]any)["tasks"] = []any{map[string]any{"id": "other", "title": "Other", "status": "pending"}}
		goal.ToolsWriteGoal(f.cwd, jsonOf(disk))
		out := r.callTool("update_goal_task", map[string]any{"task_id": "t1", "status": "complete", "evidence": "x"})
		textContains(t, out.Content, "not found", "removed task must return a typed failure")
	})

	tw(t, gttFile, "set_goal_tasks never creates per-goal auditor bypass state", func(t *testing.T) {
		f := fixtureWithTasks(t, "Task tool goal", `[]`, "")
		r := startedRig(t, f, rigOptions{})
		r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "T1"}}})
		g := goal.ToolsActiveGoal(f.cwd)
		if _, has := g["skipAuditor"]; has {
			t.Error("task confirmation must not create skipAuditor bypass state")
		}
		files := goal.ToolsActiveGoalFiles(f.cwd)
		raw, _ := os.ReadFile(f.cwd + "/.pi/goals/" + files[0])
		textNotContains(t, string(raw), "skipAuditor", "skipAuditor must not be written by set_goal_tasks")
	})

	tw(t, gttFile, "update_goal_task(pending) writes a task_reopened ledger event", func(t *testing.T) {
		f := fixtureWithTasks(t, "Reopen event", `[{"id":"sk","title":"Skipped","status":"skipped","skipReason":"later","skippedAt":"`+nowZ()+`"}]`, "")
		r := startedRig(t, f, rigOptions{})
		r.callTool("update_goal_task", map[string]any{"task_id": "sk", "status": "pending"})
		reopened := eventsOfType(f.cwd, "task_reopened")
		if len(reopened) == 0 {
			t.Fatal("task_reopened ledger event must be written")
		}
		equal(t, reopened[0]["taskId"], "sk", "taskId")
	})

	tw(t, gttFile, "RPC cancellation preserves the persisted task list", func(t *testing.T) {
		t.Setenv("PI_GOAL_AUTO_CONFIRM", "")
		os.Unsetenv("PI_GOAL_AUTO_CONFIRM")
		f := fixtureWithTasks(t, "Task tool goal", `[{"id":"original","title":"Original task","status":"pending"}]`, "")
		r := startedRig(t, f, rigOptions{Mode: "rpc", HasUI: boolPtr(true), Select: func(title string, options []string) (string, bool) {
			textContains(t, title, "Replacement task", "the proposal is shown")
			return "", false
		}})
		out := r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "replacement", "title": "Replacement task"}}})
		if out.IsError {
			t.Error("cancellation is not an error")
		}
		equal(t, listTasks(goal.ToolsActiveGoal(f.cwd))[0].(map[string]any)["id"], "original", "persisted list")
		equal(t, hasEvent(f.cwd, "task_list_set"), false, "no task_list_set")
	})
}

// ---- goal-task-lifecycle ----

const pendingTasksJSON = `[{"id":"t1","title":"Task one"},{"id":"t2","title":"Task two"},{"id":"t3","title":"Parent","subtasks":[{"id":"t3.1","title":"Child"}]}]`

func lifecycleFixture(t *testing.T, tasks, extra string) toolsFixture {
	return fixtureWithTasks(t, "Lifecycle goal", tasks, extra)
}

func TestGoalTaskLifecycleRig(t *testing.T) {
	ut := func(r *goalRig, params map[string]any) toolOut { return r.callTool("update_goal_task", params) }

	tw(t, gtlFile, "start sets the persisted currentTaskId and appends task_started", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		out := ut(r, map[string]any{"task_id": "t2", "status": "start"})
		textContains(t, out.Content, "Started t2", "start message")
		if out.Terminate != nil {
			t.Error("start does not terminate the turn")
		}
		// Memory + disk both carry the focus.
		equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t2", "currentTaskId")
		found := false
		for _, e := range eventsOfType(f.cwd, "task_started") {
			found = found || e["taskId"] == "t2"
		}
		if !found {
			t.Error("task_started ledger event")
		}
	})

	tw(t, gtlFile, "start surfaces the task verification contract", func(t *testing.T) {
		f := lifecycleFixture(t, `[{"id":"t1","title":"Task one","verificationContract":"Run the check."}]`, "")
		r := startedRig(t, f, rigOptions{})
		out := ut(r, map[string]any{"task_id": "t1", "status": "start"})
		textContains(t, out.Content, "Contract: Run the check.", "contract surfaced")
	})

	tw(t, gtlFile, "starting another task replaces the current task", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t1", "status": "start"})
		ut(r, map[string]any{"task_id": "t3", "status": "start"})
		equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t3", "later start replaces earlier focus")
	})

	tw(t, gtlFile, "starting a subtask sets focus to the nested node", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t3.1", "status": "start"})
		equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t3.1", "nested focus")
	})

	tw(t, gtlFile, "completing the current task clears currentTaskId", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t2", "status": "start"})
		ut(r, map[string]any{"task_id": "t2", "status": "complete", "evidence": "done"})
		g := goal.ToolsActiveGoal(f.cwd)
		if _, has := g["currentTaskId"]; has {
			t.Error("completing the current task clears focus")
		}
		equal(t, taskByID(listTasks(g), "t2")["status"], "complete", "status")
		if !hasEvent(f.cwd, "task_complete") {
			t.Error("task_complete event")
		}
	})

	tw(t, gtlFile, "skipping the current task clears currentTaskId", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t1", "status": "start"})
		ut(r, map[string]any{"task_id": "t1", "status": "skipped", "reason": "user direction"})
		g := goal.ToolsActiveGoal(f.cwd)
		if _, has := g["currentTaskId"]; has {
			t.Error("skipping the current task clears focus")
		}
		equal(t, taskByID(listTasks(g), "t1")["status"], "skipped", "status")
		if !hasEvent(f.cwd, "task_skipped") {
			t.Error("task_skipped event")
		}
	})

	tw(t, gtlFile, "completing a non-current task leaves focus untouched", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t2", "status": "start"})
		ut(r, map[string]any{"task_id": "t1", "status": "complete", "evidence": "done"})
		equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t2", "unrelated completion keeps focus")
	})

	tw(t, gtlFile, "start validation rejects missing, terminal, and ineligible tasks", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		missing := ut(r, map[string]any{"task_id": "ghost", "status": "start"})
		textContains(t, missing.Content, "not found", "missing task")
		// Complete the task first, then starting it must fail.
		ut(r, map[string]any{"task_id": "t1", "status": "complete", "evidence": "done"})
		terminal := ut(r, map[string]any{"task_id": "t1", "status": "start"})
		textContains(t, terminal.Content, "only pending tasks can be started", "terminal task")
		if _, has := goal.ToolsActiveGoal(f.cwd)["currentTaskId"]; has {
			t.Error("failed start does not set focus")
		}
	})

	tw(t, gtlFile, "start validation rejects paused and taskless goals", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, `"status":"paused","stopReason":"user","pauseReason":"user pause"`)
		r := startedRig(t, f, rigOptions{})
		out := ut(r, map[string]any{"task_id": "t1", "status": "start"})
		textContains(t, out.Content, "only to an active goal", "paused goal")
		if _, has := goal.ToolsActiveGoal(f.cwd)["currentTaskId"]; has {
			t.Error("no focus")
		}
		noTasks := lifecycleFixture(t, `[]`, "")
		r2 := startedRig(t, noTasks, rigOptions{})
		out2 := ut(r2, map[string]any{"task_id": "t1", "status": "start"})
		textContains(t, out2.Content, "no task list", "taskless goal")
	})

	tw(t, gtlFile, "start honors disableTasks settings", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		writeSettings(t, f.cwd, `{"disableTasks":true}`)
		r := startedRig(t, f, rigOptions{})
		out := ut(r, map[string]any{"task_id": "t1", "status": "start"})
		textContains(t, out.Content, "disabled by settings", "disabled")
		if _, has := goal.ToolsActiveGoal(f.cwd)["currentTaskId"]; has {
			t.Error("no focus")
		}
	})

	tw(t, gtlFile, "set_goal_tasks preserves currentTaskId when the id remains pending", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t2", "status": "start"})
		// Restructure: keep t2 (with a new title), drop t3, add t4.
		out := r.callTool("set_goal_tasks", map[string]any{"tasks": []any{
			map[string]any{"id": "t1", "title": "Task one"}, map[string]any{"id": "t2", "title": "Task two renamed"}, map[string]any{"id": "t4", "title": "Task four"}}})
		if !out.terminated() {
			t.Error("terminates")
		}
		g := goal.ToolsActiveGoal(f.cwd)
		equal(t, g["currentTaskId"], "t2", "focus preserved when id stays pending")
		equal(t, taskByID(listTasks(g), "t2")["title"], "Task two renamed", "title")
	})

	tw(t, gtlFile, "set_goal_tasks clears currentTaskId when the id is removed or terminal", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		task := func(id, title string) any { return map[string]any{"id": id, "title": title} }
		ut(r, map[string]any{"task_id": "t3", "status": "start"})
		// Remove t3 entirely.
		r.callTool("set_goal_tasks", map[string]any{"tasks": []any{task("t1", "Task one"), task("t2", "Task two")}})
		if _, has := goal.ToolsActiveGoal(f.cwd)["currentTaskId"]; has {
			t.Error("removed id clears focus")
		}
		r.callTool("set_goal_tasks", map[string]any{"tasks": []any{task("t1", "Task one"), task("t2", "Task two"), task("t5", "Task five")}})
		ut(r, map[string]any{"task_id": "t5", "status": "start"})
		ut(r, map[string]any{"task_id": "t5", "status": "complete", "evidence": "done"})
		// Re-set the same list (t5 now complete in the existing tree): merge keeps the completion, so the previously-current id is no
		// longer pending.
		r.callTool("set_goal_tasks", map[string]any{"tasks": []any{task("t1", "Task one"), task("t2", "Task two"), task("t5", "Task five")}})
		if _, has := goal.ToolsActiveGoal(f.cwd)["currentTaskId"]; has {
			t.Error("terminal id clears focus after structural merge")
		}
	})

	tw(t, gtlFile, "focus survives restart (reload from disk) and session refocus", func(t *testing.T) {
		f := lifecycleFixture(t, pendingTasksJSON, "")
		r := startedRig(t, f, rigOptions{})
		ut(r, map[string]any{"task_id": "t3", "status": "start"})
		// Simulate a restart: a fresh harness with the same session entries.
		r2 := startedRig(t, f, rigOptions{})
		equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t3", "focus restored after restart")
		if !r2.Host.Registered("session_start") {
			t.Error("session_start handler")
		}
	})
}
