package pi_goal_x

import (
	"os"
	"strings"
	"testing"
)

// Extra tests for the parts of goal-service.ts and goal-scheduler-state.ts the upstream tests do not pin.

func TestSchedulerSummary(t *testing.T) {
	limit := func(v float64) *float64 { return &v }
	s := jo(t, `{"used":2,"phase":"interrupted","wait":{"id":"w1","reason":"CI","deadline":1000,"nextCheckAt":2000,"remainingChecks":3}}`)
	runs, instr := schedulerSummaryParts(s, limit(5), true)
	eq(t, runs, "Autonomous runs: 2/5.", "runs")
	eq(t, instr, "Waiting: CI; wait_id=w1; deadline=1970-01-01T00:00:01.000Z.\nNext check: 1970-01-01T00:00:02.000Z; 3 checks remaining.\nExecution requires dispatch admission or explicit /goal-resume after interruption.", "instructions")
	runs, _ = schedulerSummaryParts(nil, limit(0), true)
	eq(t, runs, "Autonomous runs: 0/0 (automatic continuation disabled).", "disabled runs")
	runs, _ = schedulerSummaryParts(s, limit(5), false)
	eq(t, runs, "", "hidden runs")
	runs, _ = schedulerSummaryParts(s, nil, true)
	eq(t, runs, "", "unlimited runs")
	eq(t, schedulerSummary(jo(t, `{"used":1,"phase":"claimed"}`), limit(3), true), "Autonomous runs: 1/3.\nExecution requires dispatch admission or explicit /goal-resume after interruption.", "joined")
	eq(t, schedulerSummary(jo(t, `{"used":1,"phase":"idle"}`), nil, true), "", "empty")
}

func TestGoalServiceExtra(t *testing.T) {
	t.Run("flush rejects a buffer whose disk revision moved and folds the usage delta", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		f.service.beginTurn(f.ctx, id)
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject {
			u := newObject()
			u.set("tokensUsed", 10.0)
			u.set("activeSeconds", 4.0)
			return goalWith(g, "usage", u)
		}}))
		disk := cloneGoal(parseGoalFile(f.activePath(0)))
		disk.set("revision", 7.0)
		gsMust(f.ctx.writeActiveGoalFile(disk))
		got := gsMust(f.service.flushTurn(f.ctx))
		if got != nil {
			t.Fatal("conflicting flush must write nothing")
		}
		eq(t, f.service.isTurnBuffered(), false, "turn closed")
		focused := f.ref.getFocused()
		eq(t, gnum(focused.obj("usage"), "tokensUsed"), 10.0, "usage delta folded onto disk goal")
		eq(t, goalRevision(focused), 7.0, "disk revision kept")
		eq(t, len(f.log.diagnostics), 1, "diagnostic")
		eq(t, f.log.diagnostics[0].source, "storage", "diagnostic source")
		eq(t, f.log.diagnostics[0].message, "Goal "+id+" changed in another process; buffered changes were rejected. Refresh and retry.", "message")
		eq(t, gsMust(f.service.flushForAudit(f.ctx)), f.log.diagnostics[0].message, "a closed turn keeps the last flush error")
	})

	t.Run("a flush bumps the revision once and appends the buffered ledger batch", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		f.service.beginTurn(f.ctx, id)
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("one"), ledger: []*jsObject{gsPausedEvent(id, "a", nowIso())}}))
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("two")}))
		f.service.appendEvents(f.ctx, []*jsObject{gsPausedEvent(id, "b", nowIso())})
		eq(t, len(f.ledgerEvents()), 0, "nothing appended before the flush")
		eq(t, goalRevision(parseGoalFile(f.activePath(0))), 0.0, "nothing written before the flush")
		f.service.endTurn(f.ctx)
		eq(t, goalRevision(parseGoalFile(f.activePath(0))), 1.0, "one revision bump")
		eq(t, len(f.ledgerEvents()), 2, "batched ledger")
		eq(t, gstr(parseGoalFile(f.activePath(0)), "objective"), "two", "last mutation wins")
	})

	t.Run("in-turn persist is buffered", func(t *testing.T) {
		f := gsNewFixture(t)
		f.service.beginTurn(f.ctx, gstr(f.written, "id"))
		got := gsMust(f.service.persist(f.ctx))
		if got == nil || !f.service.isTurnBuffered() {
			t.Fatal("persist must buffer")
		}
		eq(t, goalRevision(parseGoalFile(f.activePath(0))), 0.0, "disk untouched")
	})

	t.Run("updateTasks applies an ordered batch and rejects the whole batch on a failed step", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		tl := gsTaskList("t1", "One", false)
		two := newObject()
		two.set("id", "t2")
		two.set("title", "Two")
		two.set("status", "pending")
		tl.set("tasks", append(tl.vals["tasks"].([]any), two))
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", tl) }}))
		done := func(task *jsObject) (*jsObject, goalGate) {
			return goalWith(task, "status", "complete"), goalGate{ok: true}
		}
		bad := goalTaskUpdateSpec{taskId: "nope", update: done}
		res := gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{{taskId: "t1", update: done}, bad}))
		eq(t, res.ok, false, "batch fails")
		eq(t, res.message, `Task "nope" not found.`, "message")
		tasks := parseGoalFile(f.activePath(0)).obj("taskList").vals["tasks"].([]any)
		eq(t, gstr(tasks[0].(*jsObject), "status"), "pending", "nothing applied")
		cur := "t2"
		res = gsMust(f.service.updateTasks(f.ctx, []goalTaskUpdateSpec{{taskId: "t1", update: done}, {taskId: "t2", update: done, setCurrentTaskId: &cur}}))
		if !res.ok {
			t.Fatal(res.message)
		}
		eq(t, res.goalId, id, "goal id")
		eq(t, goalRevision(res.goal), 2.0, "revision")
		eq(t, res.goal.vals["currentTaskId"], "t2", "current task")
		tasks = parseGoalFile(f.activePath(0)).obj("taskList").vals["tasks"].([]any)
		eq(t, gstr(tasks[1].(*jsObject), "status"), "complete", "second task complete")
	})

	t.Run("completing the current task clears the execution focus", func(t *testing.T) {
		f := gsNewFixture(t)
		cur := "t1"
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject {
			return goalWith(g, "taskList", gsTaskList("t1", "One", false), "currentTaskId", "t1")
		}}))
		_ = cur
		res := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: func(task *jsObject) (*jsObject, goalGate) {
			return goalWith(task, "status", "complete"), goalGate{ok: true}
		}}))
		if !res.ok {
			t.Fatal(res.message)
		}
		if res.goal.has("currentTaskId") {
			t.Fatal("currentTaskId must be cleared")
		}
	})

	t.Run("in-turn updateTask and missing-task failures", func(t *testing.T) {
		f := gsNewFixture(t)
		id := gstr(f.written, "id")
		f.service.beginTurn(f.ctx, id)
		res := gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1"}))
		eq(t, res.message, "The goal has no task list.", "no task list")
		gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: func(g *jsObject) *jsObject { return goalWith(g, "taskList", gsTaskList("t1", "One", false)) }}))
		res = gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "zz"}))
		eq(t, res.message, `Task "zz" not found.`, "missing task")
		res = gsMust(f.service.updateTask(f.ctx, goalTaskUpdateSpec{taskId: "t1", update: func(task *jsObject) (*jsObject, goalGate) {
			return goalWith(task, "status", "complete"), goalGate{ok: true}
		}}))
		if !res.ok {
			t.Fatal(res.message)
		}
		eq(t, goalRevision(res.goal), 2.0, "buffered revision")
		eq(t, goalRevision(parseGoalFile(f.activePath(0))), 0.0, "disk untouched until flush")
	})

	t.Run("reconcile adopts a disk change and fires onReconciled", func(t *testing.T) {
		f := gsNewFixture(t)
		disk := cloneGoal(parseGoalFile(f.activePath(0)))
		disk.set("objective", "external")
		disk.set("revision", 3.0)
		gsMust(f.ctx.writeActiveGoalFile(disk))
		f.ctx.invalidateDir()
		eq(t, f.service.reconcileFocused(f.ctx, false), true, "reconciled")
		eq(t, gstr(f.ref.getFocused(), "objective"), "external", "adopted")
		eq(t, len(f.log.reconciled) > 0, true, "hook fired")
		eq(t, f.service.lastPersistedUsage.goalId, gstr(f.written, "id"), "baseline tracked")
	})

	t.Run("debug files are written and removed under .pi/goals/debug", func(t *testing.T) {
		f := gsNewFixture(t)
		if err := f.service.writeDebugFile(f.ctx, ".pi/goals/debug/x.md", "hello"); err != nil {
			t.Fatal(err)
		}
		p := f.cwd + "/.pi/goals/debug/x.md"
		if string(gsMust(os.ReadFile(p))) != "hello" {
			t.Fatal("debug file content")
		}
		f.service.removeDebugFile(f.ctx, ".pi/goals/debug/x.md")
		if _, err := os.Stat(p); err == nil {
			t.Fatal("debug file must be gone")
		}
		f.service.removeDebugFile(f.ctx, ".pi/goals/debug/x.md")
	})

	t.Run("apply refuses without a focused goal", func(t *testing.T) {
		f := gsNewFixture(t)
		f.ref.focusedId = ""
		res := gsMust(f.service.apply(f.ctx, goalMutationSpec{reconcile: new(false), mutate: gsSetObjective("x")}))
		eq(t, res.message, "No focused goal to mutate.", "message")
		eq(t, strings.Contains(res.message, "focused"), true, "typed failure")
	})
}
