package pi_goal_x

import (
	"strings"
	"testing"
)

const gtF = "goal-tasks"

// tk builds a task; kids are subtasks.
func tk(id, title, status string, kids ...*jsObject) *jsObject {
	o := newObject()
	o.set("id", id)
	o.set("title", title)
	o.set("status", status)
	if len(kids) > 0 {
		s := make([]any, len(kids))
		for i, k := range kids {
			s[i] = k
		}
		o.set("subtasks", s)
	}
	return o
}

func tl(blockCompletion bool, tasks ...*jsObject) *jsObject {
	l := newObject()
	s := make([]any, len(tasks))
	for i, k := range tasks {
		s[i] = k
	}
	l.set("tasks", s)
	l.set("blockCompletion", blockCompletion)
	l.set("proposedAt", "2026-05-27T00:00:00.000Z")
	return l
}

func anyTasks(tasks ...*jsObject) []any {
	s := make([]any, len(tasks))
	for i, k := range tasks {
		s[i] = k
	}
	return s
}

// gtGoal is makeGoal from the original: tasks default to pending "task-N" / "Task N".
func gtGoal(tasks ...*jsObject) *jsObject {
	g := newObject()
	g.set("id", "goal-1")
	g.set("objective", "Test goal")
	g.set("status", "active")
	g.set("autoContinue", true)
	u := newObject()
	u.set("tokensUsed", 0.0)
	u.set("activeSeconds", 0.0)
	g.set("usage", u)
	g.set("sisyphus", false)
	g.set("taskList", tl(false, tasks...))
	return g
}

func hasMsg(t *testing.T, v policyValidation, ok bool, re string) {
	t.Helper()
	if v.ok != ok {
		t.Fatalf("ok = %v, want %v (%q)", v.ok, ok, v.message)
	}
	if re != "" && !strings.Contains(v.message, re) {
		t.Fatalf("message %q lacks %q", v.message, re)
	}
}

func TestGoalTasksTwins(t *testing.T) {
	ptr := func(n int) *int { return &n }
	tw(t, gtF, "buildTaskSummary: no tasks", func(t *testing.T) {
		eq(t, buildTaskSummary(tl(false)), "No tasks", "summary")
	})
	tw(t, gtF, "buildTaskSummary: all pending", func(t *testing.T) {
		eq(t, buildTaskSummary(tl(false, tk("t1", "Task 1", "pending"), tk("t2", "Task 2", "pending"))), "0/2 tasks complete", "summary")
	})
	tw(t, gtF, "buildTaskSummary: some complete", func(t *testing.T) {
		eq(t, buildTaskSummary(tl(false, tk("t1", "Task 1", "complete"), tk("t2", "Task 2", "pending"), tk("t3", "Task 3", "pending"))), "1/3 tasks complete", "summary")
	})
	tw(t, gtF, "buildTaskSummary: some skipped", func(t *testing.T) {
		eq(t, buildTaskSummary(tl(false, tk("t1", "Task 1", "complete"), tk("t2", "Task 2", "skipped"), tk("t3", "Task 3", "pending"))), "1/3 tasks complete (1 skipped)", "summary")
	})
	tw(t, gtF, "buildTaskSummary: all complete", func(t *testing.T) {
		eq(t, buildTaskSummary(tl(false, tk("t1", "Task 1", "complete"), tk("t2", "Task 2", "complete"))), "2/2 tasks complete", "summary")
	})
	tw(t, gtF, "taskCompletionBlockWarning: no blockCompletion returns null", func(t *testing.T) {
		eq(t, taskCompletionBlockWarning(tl(false, tk("t1", "Task 1", "pending"))), "", "warning")
	})
	tw(t, gtF, "taskCompletionBlockWarning: blockCompletion but no pending returns null", func(t *testing.T) {
		eq(t, taskCompletionBlockWarning(tl(true, tk("t1", "Task 1", "complete"))), "", "warning")
	})
	tw(t, gtF, "taskCompletionBlockWarning: blockCompletion with pending returns warning", func(t *testing.T) {
		r := taskCompletionBlockWarning(tl(true, tk("t1", "Task 1", "complete"), tk("t2", "Task 2", "pending")))
		if r == "" || !strings.Contains(r, "pending") || !strings.Contains(r, "blockCompletion") {
			t.Fatalf("warning %q", r)
		}
	})
	tw(t, gtF, "taskCompletionBlockWarning: plural tasks", func(t *testing.T) {
		r := taskCompletionBlockWarning(tl(true, tk("t1", "Task 1", "pending"), tk("t2", "Task 2", "pending")))
		if !strings.Contains(r, "2 tasks") {
			t.Fatalf("warning %q", r)
		}
	})
	tw(t, gtF, "validateTaskCompletion: no goal", func(t *testing.T) {
		hasMsg(t, validateTaskCompletion(nil, "t1"), false, "No goal")
	})
	tw(t, gtF, "validateTaskCompletion: no task list", func(t *testing.T) {
		g := gtGoal()
		g.set("taskList", undef{})
		hasMsg(t, validateTaskCompletion(g, "t1"), false, "no task list")
	})
	tw(t, gtF, "validateTaskCompletion: unknown task", func(t *testing.T) {
		hasMsg(t, validateTaskCompletion(gtGoal(tk("t1", "T1", "pending")), "t2"), false, "not found")
	})
	tw(t, gtF, "validateTaskCompletion: already complete", func(t *testing.T) {
		hasMsg(t, validateTaskCompletion(gtGoal(tk("t1", "T1", "complete")), "t1"), false, "already complete")
	})
	tw(t, gtF, "validateTaskCompletion: already skipped", func(t *testing.T) {
		hasMsg(t, validateTaskCompletion(gtGoal(tk("t1", "T1", "skipped")), "t1"), false, "already skipped")
	})
	tw(t, gtF, "validateTaskCompletion: valid pending task", func(t *testing.T) {
		hasMsg(t, validateTaskCompletion(gtGoal(tk("t1", "T1", "pending")), "t1"), true, "")
	})
	tw(t, gtF, "validateTaskSkip: no goal", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(nil, "t1", "irrelevant"), false, "No goal")
	})
	tw(t, gtF, "validateTaskSkip: no task list", func(t *testing.T) {
		g := gtGoal()
		g.set("taskList", undef{})
		hasMsg(t, validateTaskSkip(g, "t1", "irrelevant"), false, "no task list")
	})
	tw(t, gtF, "validateTaskSkip: unknown task", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(gtGoal(tk("t1", "T1", "pending")), "t2", "irrelevant"), false, "not found")
	})
	tw(t, gtF, "validateTaskSkip: already complete", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(gtGoal(tk("t1", "T1", "complete")), "t1", "irrelevant"), false, "already complete")
	})
	tw(t, gtF, "validateTaskSkip: already skipped passes validation (toggle handled by executor)", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(gtGoal(tk("t1", "T1", "skipped")), "t1", "irrelevant"), true, "")
	})
	tw(t, gtF, "validateTaskSkip: empty reason", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(gtGoal(tk("t1", "T1", "pending")), "t1", ""), false, "non-empty reason")
	})
	tw(t, gtF, "validateTaskSkip: valid skip", func(t *testing.T) {
		hasMsg(t, validateTaskSkip(gtGoal(tk("t1", "T1", "pending")), "t1", "No longer needed"), true, "")
	})
	tw(t, gtF, "validateTaskListProposal: no goal", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(nil, anyTasks(tk("t1", "T1", "pending")), nil), false, "No goal")
	})
	tw(t, gtF, "validateTaskListProposal: valid proposal", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tk("t1", "Task 1", "pending"), tk("t2", "Task 2", "pending")), nil), true, "")
	})
	tw(t, gtF, "validateTaskListProposal: duplicate ids", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tk("t1", "Task 1", "pending"), tk("t1", "Task 1 again", "pending")), nil), false, "Duplicate")
	})
	tw(t, gtF, "validateTaskListProposal: empty id", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tk("", "Task", "pending")), nil), false, "non-empty id")
	})
	tw(t, gtF, "validateTaskListProposal: empty title", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tk("t1", "", "pending")), nil), false, "non-empty title")
	})
	tw(t, gtF, "validateTaskListProposal: over 50 tasks", func(t *testing.T) {
		var tasks []*jsObject
		for i := 0; i < 51; i++ {
			tasks = append(tasks, tk("t"+jsNumber(float64(i)), "Task "+jsNumber(float64(i)), "pending"))
		}
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tasks...), nil), false, "cannot exceed 50")
	})
	tw(t, gtF, "validateTaskListProposal: duplicate ids across nested subtasks", func(t *testing.T) {
		tasks := anyTasks(tk("t1", "Task 1", "pending", tk("t2", "Subtask with same id", "pending")), tk("t2", "Task 2", "pending"))
		hasMsg(t, validateTaskListProposal(gtGoal(), tasks, nil), false, `Duplicate task id: "t2"`)
	})
	tw(t, gtF, "validateTaskListProposal: duplicate ids within same subtask tree", func(t *testing.T) {
		tasks := anyTasks(tk("t1", "Task 1", "pending", tk("sub-a", "Sub A", "pending"), tk("sub-a", "Sub A dup", "pending")))
		hasMsg(t, validateTaskListProposal(gtGoal(), tasks, nil), false, `Duplicate task id: "sub-a"`)
	})
	tw(t, gtF, "findSubtaskDepthViolation: no violation at default depth", func(t *testing.T) {
		eq(t, findSubtaskDepthViolation(anyTasks(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "pending"))), 1), "", "violation")
	})
	deep2 := func() []any {
		return anyTasks(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "pending", tk("t1ai", "Sub-sub", "pending"))))
	}
	tw(t, gtF, "findSubtaskDepthViolation: catches sub-sub-tasks at depth=1", func(t *testing.T) {
		v := findSubtaskDepthViolation(deep2(), 1)
		if !strings.Contains(v, "subtask nesting depth") || !strings.Contains(v, "maximum of 1") {
			t.Fatalf("violation %q", v)
		}
		eq(t, v, `Task "t1" has subtask nesting depth 2, exceeding the configured maximum of 1`, "text")
	})
	tw(t, gtF, "findSubtaskDepthViolation: sub-sub-tasks allowed at depth=2", func(t *testing.T) {
		eq(t, findSubtaskDepthViolation(deep2(), 2), "", "violation")
	})
	tw(t, gtF, "findSubtaskDepthViolation: catches 3-levels at depth=2", func(t *testing.T) {
		tasks := anyTasks(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "pending", tk("t1ai", "Sub-sub", "pending", tk("t1aia", "Sub-sub-sub", "pending")))))
		if findSubtaskDepthViolation(tasks, 2) == "" {
			t.Fatal("expected violation")
		}
	})
	tw(t, gtF, "findSubtaskDepthViolation: no violation for tasks without subtasks", func(t *testing.T) {
		eq(t, findSubtaskDepthViolation(anyTasks(tk("t1", "Task 1", "pending"), tk("t2", "Task 2", "pending")), 1), "", "violation")
	})
	tw(t, gtF, "validateTaskListProposal: passes with valid subtasks", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), anyTasks(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "pending"))), ptr(1)), true, "")
	})
	tw(t, gtF, "validateTaskListProposal: rejects with subtasks exceeding depth", func(t *testing.T) {
		hasMsg(t, validateTaskListProposal(gtGoal(), deep2(), ptr(1)), false, "")
	})
	tw(t, gtF, "checkSubtasksComplete: returns undefined when no subtasks", func(t *testing.T) {
		eq(t, checkSubtasksComplete(tk("t1", "Task 1", "pending")), "", "result")
	})
	tw(t, gtF, "checkSubtasksComplete: returns undefined when lightweight", func(t *testing.T) {
		task := tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "pending"))
		task.set("lightweightSubtasks", true)
		eq(t, checkSubtasksComplete(task), "", "result")
	})
	tw(t, gtF, "checkSubtasksComplete: returns message when subtask is pending", func(t *testing.T) {
		r := checkSubtasksComplete(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "complete"), tk("t1b", "Sub B", "pending")))
		if !strings.Contains(r, "pending subtask") {
			t.Fatalf("result %q", r)
		}
		eq(t, r, `Task "t1" has pending subtask "t1b". Complete or skip all subtasks first.`, "text")
	})
	tw(t, gtF, "checkSubtasksComplete: returns undefined when all subtasks complete/skipped", func(t *testing.T) {
		eq(t, checkSubtasksComplete(tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "complete"), tk("t1b", "Sub B", "skipped"))), "", "result")
	})
	tw(t, gtF, "checkSubtasksComplete: checks nested subtasks", func(t *testing.T) {
		task := tk("t1", "Task 1", "pending", tk("t1a", "Sub A", "complete", tk("t1ai", "Sub-sub A", "complete"), tk("t1aii", "Sub-sub B", "pending")))
		if checkSubtasksComplete(task) == "" {
			t.Fatal("expected message")
		}
	})
	subOf := func(o *jsObject, path ...int) *jsObject {
		for _, i := range path {
			o = o.vals["subtasks"].([]any)[i].(*jsObject)
		}
		return o
	}
	tw(t, gtF, "skipAllSubtasks: skips direct subtasks", func(t *testing.T) {
		r := skipAllSubtasks(tk("t1", "Task 1", "skipped", tk("t1a", "Sub A", "pending"), tk("t1b", "Sub B", "pending")), "2026-01-01", "Test skip")
		eq(t, gstr(subOf(r, 0), "status"), "skipped", "a")
		eq(t, gstr(subOf(r, 0), "skipReason"), "Test skip", "reason")
		eq(t, gstr(subOf(r, 0), "skippedAt"), "2026-01-01", "at")
		eq(t, gstr(subOf(r, 1), "status"), "skipped", "b")
	})
	tw(t, gtF, "skipAllSubtasks: does not modify completed subtasks", func(t *testing.T) {
		done := tk("t1a", "Sub A", "complete")
		done.set("completedAt", "2026-01-01")
		r := skipAllSubtasks(tk("t1", "Task 1", "skipped", done, tk("t1b", "Sub B", "pending")), "2026-06-01", "Cascade")
		eq(t, gstr(subOf(r, 0), "status"), "complete", "a")
		eq(t, gstr(subOf(r, 1), "status"), "skipped", "b")
	})
	tw(t, gtF, "skipAllSubtasks: skips nested subtasks", func(t *testing.T) {
		r := skipAllSubtasks(tk("t1", "Task 1", "skipped", tk("t1a", "Sub A", "pending", tk("t1ai", "Sub-sub", "pending"))), "2026-01-01", "Nested")
		eq(t, gstr(subOf(r, 0, 0), "status"), "skipped", "nested")
	})
	tw(t, gtF, "findTaskInTree: finds top-level task", func(t *testing.T) {
		eq(t, gstr(findTaskInTree(anyTasks(tk("t1", "T1", "pending"), tk("t2", "T2", "pending")), "t2"), "id"), "t2", "id")
	})
	tw(t, gtF, "findTaskInTree: finds nested task", func(t *testing.T) {
		eq(t, gstr(findTaskInTree(anyTasks(tk("t1", "T1", "pending", tk("t1a", "Sub A", "pending"))), "t1a"), "id"), "t1a", "id")
	})
	tw(t, gtF, "findTaskInTree: returns undefined for missing task", func(t *testing.T) {
		if findTaskInTree(anyTasks(tk("t1", "T1", "pending")), "missing") != nil {
			t.Fatal("expected nil")
		}
	})
	setStatus := func(s string) func(*jsObject) *jsObject {
		return func(o *jsObject) *jsObject { c := o.clone(); c.set("status", s); return c }
	}
	tw(t, gtF, "updateTaskInTree: updates top-level task", func(t *testing.T) {
		u := updateTaskInTree(anyTasks(tk("t1", "T1", "pending"), tk("t2", "T2", "pending")), "t1", setStatus("complete"))
		eq(t, gstr(u[0].(*jsObject), "status"), "complete", "t1")
		eq(t, gstr(u[1].(*jsObject), "status"), "pending", "t2")
	})
	tw(t, gtF, "updateTaskInTree: updates nested task", func(t *testing.T) {
		u := updateTaskInTree(anyTasks(tk("t1", "T1", "pending", tk("t1a", "Sub A", "pending"))), "t1a", setStatus("complete"))
		eq(t, gstr(subOf(u[0].(*jsObject), 0), "status"), "complete", "t1a")
	})
	tw(t, gtF, "updateTaskInTree: leaves unrelated tasks unchanged", func(t *testing.T) {
		in := anyTasks(tk("t1", "T1", "pending"), tk("t2", "T2", "pending"))
		u := updateTaskInTree(in, "t2", setStatus("skipped"))
		eq(t, gstr(u[0].(*jsObject), "status"), "pending", "t1")
		eq(t, gstr(u[1].(*jsObject), "status"), "skipped", "t2")
		if u[0] != in[0] {
			t.Fatal("unrelated task lost identity")
		}
		same := updateTaskInTree(in, "missing", setStatus("skipped"))
		if !sameSlice(same, in) {
			t.Fatal("unchanged tree must keep its slice")
		}
	})
	tw(t, gtF, "buildTaskSummary: counts subtasks recursively", func(t *testing.T) {
		r := buildTaskSummary(tl(false, tk("t1", "T1", "complete", tk("t1a", "Sub A", "pending"))))
		if !strings.Contains(r, "1/2") {
			t.Fatalf("summary %q", r)
		}
	})
	tw(t, gtF, "taskCompletionBlockWarning: counts pending subtasks", func(t *testing.T) {
		r := taskCompletionBlockWarning(tl(true, tk("t1", "T1", "complete", tk("t1a", "Sub A", "pending"))))
		if !strings.Contains(r, "1 task") {
			t.Fatalf("warning %q", r)
		}
	})
}
