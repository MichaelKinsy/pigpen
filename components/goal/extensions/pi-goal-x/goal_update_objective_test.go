package pi_goal_x

import "testing"

const guoF = "goal-update-objective"

func TestGoalUpdateObjectiveTwins(t *testing.T) {
	mk := func(objective string, kv ...any) *jsObject {
		return gtpMakeGoal(ms(2026, 6, 2, 10, 0, 0), objective, kv...)
	}
	tskip(t, guoF, "update_goal schema has additionalProperties: false and no updatedObjective", "asserts the TypeScript source text of goal-core-tools.ts; the Go tool registration (goal-core-tools, wave 2) does not exist in the package yet")
	tskip(t, guoF, "update_goal routes complete directly to the shared completion flow", "asserts the TypeScript source text of goal-core-tools.ts; the Go tool registration (goal-core-tools, wave 2) does not exist in the package yet")
	tw(t, guoF, "update_goal(complete) with status=complete still works (completion flow unchanged)", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("Original objective: build feature X"))
		eq(t, gstr(active, "status"), "active", "status")
		completed := mustWrite(t, s, with(active, "status", "complete", "stopReason", "agent", "updatedAt", nowIso()))
		eq(t, gstr(completed, "status"), "complete", "status")
		eq(t, gstr(completed, "objective"), gstr(active, "objective"), "objective")
		tpContains(t, readRel(t, s, gstr(completed, "activePath")), `"status": "complete"`, "disk")
		archived := mustArchive(t, s, completed)
		eq(t, hasAnyKey(archived, "activePath"), false, "activePath")
		matches(t, gstr(archived, "archivedPath"), `^\.pi/goals/archived/goal_`, "archivedPath")
	})
	tw(t, guoF, "buildCompletionReport handles updated objective display", func(t *testing.T) {
		r := buildCompletionReport(completionReportArgs{detailedSummary: "Goal: Build feature X\nUpdated objective: Build feature Y\nStatus: active", auditorReport: "Inspected and verified.\n\n<approved/>"})
		tpContains(t, r, "Goal complete.", "report")
		tpContains(t, r, "<approved/>", "report")
	})
	tw(t, guoF, "tweak persist path: writeActiveGoalFile with new objective (simulated handler execution)", func(t *testing.T) {
		s := tempStore(t)
		goal := mk("Original objective")
		active := mustWrite(t, s, goal)
		eq(t, gstr(active, "objective"), "Original objective", "objective")
		newObj := "Tweaked objective after /goal-tweak interview"
		tweaked := mustWrite(t, s, with(active, "objective", newObj, "updatedAt", nowIso()))
		eq(t, gstr(tweaked, "objective"), newObj, "objective must be updated")
		eq(t, gstr(tweaked, "status"), "active", "status")
		eq(t, gstr(tweaked, "activePath"), gstr(active, "activePath"), "active path")
		disk := readRel(t, s, gstr(tweaked, "activePath"))
		tpContains(t, disk, newObj, "disk objective")
		tpContains(t, disk, `"status": "active"`, "disk status")
		if !s.readActiveGoalPool().has(gstr(goal, "id")) {
			t.Error("tweaked goal must still be in active pool")
		}
	})
	tw(t, guoF, "tweak persist path: taskList persisted when tasks parameter provided", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("Goal with inherited tasks"))
		t2 := tk("t2", "Task two", "pending")
		t2.set("verificationContract", "Must verify")
		list := tl(false, tk("t1", "Task one", "pending"), t2)
		list.set("proposedAt", nowIso())
		tweaked := mustWrite(t, s, with(active, "objective", "Updated objective with new tasks", "updatedAt", nowIso(), "taskList", list))
		l := tweaked.obj("taskList")
		if l == nil {
			t.Fatal("taskList must be set on tweaked goal")
		}
		tasks := l.vals["tasks"].([]any)
		eq(t, len(tasks), 2, "task count")
		eq(t, gstr(tasks[0].(*jsObject), "id"), "t1", "first id")
		eq(t, gstr(tasks[1].(*jsObject), "verificationContract"), "Must verify", "contract")
		eq(t, gstr(tweaked, "objective"), "Updated objective with new tasks", "objective")
		disk := readRel(t, s, gstr(tweaked, "activePath"))
		tpContains(t, disk, "Task one", "disk title")
		tpContains(t, disk, "Must verify", "disk contract")
	})
	tw(t, guoF, "tweak persist path: original taskList inherited when tasks omitted", func(t *testing.T) {
		s := tempStore(t)
		list := tl(false, tk("orig1", "Original task", "pending"))
		list.set("proposedAt", nowIso())
		active := mustWrite(t, s, mk("Goal with original task list", "taskList", list))
		if active.obj("taskList") == nil {
			t.Fatal("original must have taskList")
		}
		upd := "Objective tweaked, tasks inherited unchanged"
		w := mustWrite(t, s, with(active, "objective", upd, "updatedAt", nowIso(), "taskList", active.obj("taskList")))
		l := w.obj("taskList")
		if l == nil {
			t.Fatal("inherited taskList must be present")
		}
		tasks := l.vals["tasks"].([]any)
		eq(t, len(tasks), 1, "count")
		eq(t, gstr(tasks[0].(*jsObject), "id"), "orig1", "id")
		eq(t, gstr(tasks[0].(*jsObject), "title"), "Original task", "title")
		eq(t, gstr(w, "objective"), upd, "objective")
		disk := readRel(t, s, gstr(w, "activePath"))
		tpContains(t, disk, upd, "disk objective")
		tpContains(t, disk, "Original task", "disk task")
	})
	tw(t, guoF, "tweak persist path: taskList cleared when tasks omitted and goal has no taskList", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("Goal without task list"))
		eq(t, active.obj("taskList") == nil, true, "original has no taskList")
		tweaked := mustWrite(t, s, with(active, "objective", "Still no tasks", "updatedAt", nowIso(), "taskList", undef{}))
		eq(t, tweaked.obj("taskList") == nil, true, "still no taskList")
	})
	tw(t, guoF, "tweak persist path: task validation rejects deep subtasks", func(t *testing.T) {
		s := tempStore(t)
		active := mustWrite(t, s, mk("Test validation"))
		deep := anyTasks(tk("t1", "Parent", "pending", tk("t1a", "Child", "pending", tk("t1ai", "Grandchild", "pending"))))
		v := findSubtaskDepthViolation(deep, 1)
		if v == "" {
			t.Fatal("must reject tasks with subtask depth > 1")
		}
		tpContains(t, v, "subtask nesting depth", "violation")
		rec := with(active, "status", "active", "autoContinue", true)
		r := validateTaskListProposal(rec, deep, nil)
		hasMsg(t, r, false, "depth")
	})
	tw(t, guoF, "goal evolution instruction mentions /goal-tweak instead of updatedObjective", func(t *testing.T) {
		text := goalPrompt(mk("Original objective: build feature X"), nil)
		tpNotContains(t, text, "updatedObjective", "goalPrompt")
		tpContains(t, text, "immutable", "goalPrompt")
		tpContains(t, text, "/goal-tweak", "goalPrompt")
	})
}
