package pi_goal_x_test

// Extra tests (not twins), lane B: the task tools and the completion flow (see mutants_b_test.go).

import (
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func mbTaskRig(t *testing.T, tasks, extra string) (*goalRig, toolsFixture) {
	t.Helper()
	f := fixtureWithTasks(t, "Task goal", tasks, extra)
	return startedRig(t, f, rigOptions{}), f
}

// omit-keys-skip, batch-bounds, batch-both, batch-summary, task-evidence-cap
func TestMutantsBUpdateGoalTaskBatch(t *testing.T) {
	tasks := `[{"id":"sk","title":"S","status":"skipped","skipReason":"later","skippedAt":"` + nowZ() + `"},{"id":"p","title":"P","status":"pending"}]`
	r, f := mbTaskRig(t, tasks, "")
	out := r.callTool("update_goal_task", map[string]any{"updates": []any{
		map[string]any{"task_id": "sk", "status": "pending"},
		map[string]any{"task_id": "p", "status": "start"},
	}})
	textContains(t, out.Content, "sk pending; p start. ", "the batch summary joins the updates with semicolons")
	task := taskByID(listTasks(goal.ToolsActiveGoal(f.cwd)), "sk")
	equal(t, task["status"], "pending", "the skipped task is reopened")
	for _, k := range []string{"skippedAt", "skipReason"} {
		if _, has := task[k]; has {
			t.Errorf("reopening drops %s: %v", k, task)
		}
	}

	many := make([]any, 101)
	for i := range many {
		many[i] = map[string]any{"task_id": "p", "status": "start"}
	}
	equal(t, r.callTool("update_goal_task", map[string]any{"updates": many}).Content, "updates must contain 1\u2013100 valid task updates.", "101 updates")
	equal(t, r.callTool("update_goal_task", map[string]any{"updates": []any{}}).Content, "updates must contain 1\u2013100 valid task updates.", "no updates")
	for _, k := range []string{"task_id", "status", "evidence", "reason"} {
		equal(t, r.callTool("update_goal_task", map[string]any{"updates": []any{map[string]any{"task_id": "p", "status": "start"}}, k: "x"}).Content,
			"Use either updates or single-task fields, never both.", "updates with "+k)
	}

	long := strings.Repeat("e", 300)
	r2, f2 := mbTaskRig(t, `[{"id":"a","title":"A","status":"pending"},{"id":"b","title":"B","status":"pending"}]`, "")
	r2.callTool("update_goal_task", map[string]any{"updates": []any{map[string]any{"task_id": "a", "status": "complete", "evidence": "  " + long + "  "}}})
	r2.callTool("update_goal_task", map[string]any{"task_id": "b", "status": "complete", "evidence": "  " + long + "  "})
	disk := listTasks(goal.ToolsActiveGoal(f2.cwd))
	equal(t, taskByID(disk, "a")["evidence"], strings.Repeat("e", 200), "batch evidence is trimmed and cut to 200 chars")
	equal(t, taskByID(disk, "b")["evidence"], strings.Repeat("e", 200), "single evidence is trimmed and cut to 200 chars")
}

// task-start-pending-only, task-skip-reason, task-reopen-skipped-only, task-no-list
func TestMutantsBUpdateGoalTaskSingle(t *testing.T) {
	tasks := `[{"id":"sk","title":"S","status":"skipped","skipReason":"later","skippedAt":"` + nowZ() + `"},{"id":"p","title":"P","status":"pending"},` +
		`{"id":"w","title":"W","status":"in_progress"}]`
	r, _ := mbTaskRig(t, tasks, "")
	equal(t, r.callTool("update_goal_task", map[string]any{"task_id": "sk", "status": "start"}).Content,
		`Task "sk" is skipped; only pending tasks can be started.`, "start a skipped task")
	equal(t, r.callTool("update_goal_task", map[string]any{"task_id": "p", "status": "skipped", "reason": " \n"}).Content,
		"update_goal_task(status=skipped) requires a non-empty reason.", "blank skip reason")
	equal(t, r.callTool("update_goal_task", map[string]any{"task_id": "w", "status": "pending"}).Content,
		`Task "w" is not skipped; only skipped tasks can be reopened with status=pending.`, "reopening a task that is not skipped")

	r.callTool("update_goal_task", map[string]any{"task_id": "p", "status": "start"})
	equal(t, r.Probe.Goal()["currentTaskId"], "p", "starting a task makes it current")

	r2, _ := mbRig(t, "No task list", "", rigOptions{})
	equal(t, r2.callTool("update_goal_task", map[string]any{"task_id": "x", "status": "start"}).Content, "The goal has no task list.", "no task list")
}

// set-tasks-status, set-tasks-block, set-tasks-current-pending, set-tasks-gate-label, set-tasks-plural
func TestMutantsBSetGoalTasks(t *testing.T) {
	r, f := mbTaskRig(t, `[]`, `"status":"paused","autoContinue":false`)
	out := r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "a", "title": "A"}}})
	equal(t, out.Content, "Task list set and confirmed. 1 task.", "a paused goal accepts a task list; block_completion defaults to false")
	equal(t, goal.ToolsActiveGoal(f.cwd)["taskList"].(map[string]any)["blockCompletion"], false, "blockCompletion defaults to false")

	r, _ = mbTaskRig(t, `[]`, `"status":"blocked"`)
	equal(t, r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "a", "title": "A"}}}).Content,
		"set_goal_tasks applies to an active or paused goal; this goal is blocked.", "a blocked goal")

	r, _ = mbTaskRig(t, `[]`, "")
	equal(t, r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "a", "title": "A"}}, "block_completion": true}).Content,
		"Task list set and confirmed. 1 task. (blockCompletion enabled)", "one task with the gate")
	equal(t, r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "a", "title": "A"}, map[string]any{"id": "b", "title": "B"}}, "block_completion": true}).Content,
		"Task list set and confirmed. 2 tasks. (blockCompletion enabled)", "two tasks with the gate")

	// The current task survives only while it stays pending.
	r, f = mbTaskRig(t, `[{"id":"t1","title":"T1","status":"complete","evidence":"e","completedAt":"`+nowZ()+`"},{"id":"t2","title":"T2","status":"pending"}]`, `"currentTaskId":"t1"`)
	r.Probe.SetGoalField("currentTaskId", "t1")
	r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "T1"}, map[string]any{"id": "t2", "title": "T2"}}})
	if id, has := r.Probe.Goal()["currentTaskId"]; has {
		t.Errorf("a completed task is no longer current, got %v", id)
	}
	_ = f
	r, f = mbTaskRig(t, `[{"id":"t1","title":"T1","status":"pending"}]`, `"currentTaskId":"t1"`)
	r.callTool("set_goal_tasks", map[string]any{"tasks": []any{map[string]any{"id": "t1", "title": "T1 renamed"}}})
	equal(t, goal.ToolsActiveGoal(f.cwd)["currentTaskId"], "t1", "a pending current task stays current")
}

func mbAuditRig(t *testing.T, objective, overrides string, opts rigOptions, audit func(goal.RigAuditCall) goal.RigAuditResult) (*goalRig, toolsFixture) {
	t.Helper()
	f := newToolsFixture(t, objective, false, mbAt, overrides)
	r := newToolsRig(t, f, opts)
	if audit != nil {
		r.Probe.SetCompletionAuditor(audit)
	}
	r.startToolsSession()
	return r, f
}

// auditContents are the audit messages the completion flow queued, once the run settles idle.
func (r *goalRig) auditContents() []string {
	r.Fire("agent_settled", map[string]any{})
	var out []string
	for _, s := range r.Sent() {
		if s.Message["customType"] == "pi-goal-audit-event" {
			out = append(out, str(s.Message["content"]))
		}
	}
	return out
}

// complete-task-gate
func TestMutantsBCompleteTaskGate(t *testing.T) {
	f := fixtureWithTasks(t, "Gated goal", `[{"id":"a","title":"A","status":"pending"},{"id":"b","title":"B","status":"pending"}]`, "")
	goal.ToolsWriteGoal(f.cwd, jsonOf(withBlock(goal.ToolsActiveGoal(f.cwd))))
	r := startedRig(t, f, rigOptions{})
	equal(t, r.callTool("update_goal", map[string]any{"status": "complete"}).Content,
		"2 tasks still pending with blockCompletion enabled. Complete or skip all pending tasks before finishing the goal.", "pending tasks gate the completion")
}

func withBlock(g map[string]any) map[string]any {
	g["taskList"].(map[string]any)["blockCompletion"] = true
	return g
}

// complete-label-default, complete-label-thinking, complete-skip-terminate, complete-approved-text, complete-approved-terminate,
// commit-stop-reason, complete-summary-trim
func TestMutantsBCompleteLabelsAndResults(t *testing.T) {
	var call goal.RigAuditCall
	approved := func(c goal.RigAuditCall) goal.RigAuditResult {
		call = c
		return goal.RigAuditResult{Approved: true, Output: "All good\n<approved/>", Model: "mock"}
	}
	r, f := mbAuditRig(t, "Label goal", "", rigOptions{}, approved)
	out := r.callTool("update_goal", map[string]any{"status": "complete", "completion_summary": "  my claim \n"})
	equal(t, call.CompletionSummary, "my claim", "the completion summary is trimmed for the auditor")
	if !out.terminated() {
		t.Error("an approved completion ends the turn")
	}
	textContains(t, out.Content, "Goal audit approved.\n\nAuditor approval:\nAll good\n<approved/>\n\nGoal complete.", "approved report")
	equal(t, goal.ToolsActiveGoal(f.cwd)["stopReason"], "agent", "the completed goal records the agent as the stop reason")
	msgs := r.auditContents()
	equal(t, len(msgs), 2, "two audit messages")
	equal(t, msgs[0], "Auditor: I am starting the independent completion audit.\nGoal id: "+mbGoalID(f)+"\nAuditor model: default", "started message")
	equal(t, msgs[1], "Auditor: I approve this completion claim.\nAuditor model: mock\n\nAll good\n<approved/>", "approved message")

	// A configured auditor model and thinking level name the label.
	r, f = mbAuditRig(t, "Label goal", "", rigOptions{Settings: `{"provider":"anthropic","model":"claude-x","thinkingLevel":"high"}`}, approved)
	r.callTool("update_goal", map[string]any{"status": "complete"})
	equal(t, r.auditContents()[0], "Auditor: I am starting the independent completion audit.\nGoal id: "+mbGoalID(f)+"\nAuditor model: anthropic/claude-x:high", "configured label")
	r, f = mbAuditRig(t, "Label goal", "", rigOptions{Settings: `{"model":"claude-x"}`}, approved)
	r.callTool("update_goal", map[string]any{"status": "complete"})
	equal(t, r.auditContents()[0], "Auditor: I am starting the independent completion audit.\nGoal id: "+mbGoalID(f)+"\nAuditor model: default/claude-x", "partial label")

	// A legacy per-goal skip completes without an audit and ends the turn.
	r, f = mbAuditRig(t, "Skip goal", `{"skipAuditor":true}`, rigOptions{}, nil)
	out = r.callTool("update_goal", map[string]any{"status": "complete"})
	if !out.terminated() {
		t.Error("a skipped audit ends the turn")
	}
	textContains(t, out.Content, "Goal audit skipped.\n\nReason: per-goal auditor disabled\n\nGoal complete.", "skip report")
	equal(t, goal.ToolsActiveGoal(f.cwd)["stopReason"], "agent", "stop reason")
}

// complete-report-default, complete-reject-error-line
func TestMutantsBCompleteRejections(t *testing.T) {
	r, f := mbAuditRig(t, "Reject goal", "", rigOptions{}, func(goal.RigAuditCall) goal.RigAuditResult {
		return goal.RigAuditResult{Output: "", Model: "mock", Err: "boom"}
	})
	out := r.callTool("update_goal", map[string]any{"status": "complete"})
	equal(t, out.Content, "Goal audit rejected.\n\nGoal completion rejected by independent auditor.\nAuditor model: mock\nAuditor error: boom\n\nAuditor produced no approval marker.", "rejection text")
	if out.terminated() {
		t.Error("a rejection does not end the turn")
	}
	result := eventsOfType(f.cwd, "audit_result")
	if len(result) != 1 {
		t.Fatalf("audit_result events: %v", result)
	}
	equal(t, result[0]["report"], "Auditor produced no output.", "the ledger report default")
	equal(t, result[0]["verdict"], "error", "an auditor error is verdict error")
}

// complete-aborted-reset
func TestMutantsBCompleteAbortedReset(t *testing.T) {
	r, f := mbAuditRig(t, "Abort goal", "", rigOptions{}, func(goal.RigAuditCall) goal.RigAuditResult {
		return goal.RigAuditResult{Err: "Auditor aborted."}
	})
	out := r.callTool("update_goal", map[string]any{"status": "complete"})
	equal(t, out.Content, "Audit aborted \u2014 the goal remains active and work continues.", "no UI: the work continues")
	if r.Probe.RigAuditAborted() {
		t.Error("the transient abort marker is consumed after the dialog")
	}
	equal(t, goal.ToolsActiveGoal(f.cwd)["status"], "active", "still active")
}

// complete-warm-tail, complete-warm-prev-verdict, complete-warm-evidence
func TestMutantsBCompleteWarmContext(t *testing.T) {
	seed := func(f toolsFixture, extra ...string) {
		id := mbGoalID(f)
		for i := 1; i <= 6; i++ {
			n := string(rune('0' + i))
			goal.ToolsAppendLedger(f.cwd, `{"type":"task_complete","goalId":"`+id+`","taskId":"t`+n+`","evidence":"ev `+n+`","at":"2026-08-04T09:0`+n+`:00.000Z"}`)
		}
		for _, e := range extra {
			goal.ToolsAppendLedger(f.cwd, strings.ReplaceAll(e, "GOAL", id))
		}
	}
	warm := func(extra ...string) string {
		f := newToolsFixture(t, "Warm goal", false, mbAt, "")
		seed(f, extra...)
		var got string
		r := newToolsRig(t, f, rigOptions{})
		r.Probe.SetCompletionAuditor(func(c goal.RigAuditCall) goal.RigAuditResult {
			got = c.WarmContext
			return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "mock"}
		})
		r.startToolsSession()
		r.callTool("update_goal", map[string]any{"status": "complete"})
		return got
	}
	plain := warm()
	lines := strings.Split(plain, "\n")
	equal(t, lines[0], "Recent goal events (from the shared ledger):", "heading")
	var events []string
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "- ") {
			events = append(events, l)
		}
	}
	equal(t, len(events), 8, "the warm start carries the latest eight events")
	textContains(t, plain, "- 2026-08-04T09:06:00.000Z task_complete (task t6) evidence: ev 6", "evidence of a task completion")
	textNotContains(t, plain, "Previous rejection", "no earlier audit")

	rejected := warm(`{"type":"audit_result","goalId":"GOAL","verdict":"disapproved","report":"` + strings.Repeat("m", 700) + `","at":"2026-08-04T09:07:00.000Z"}`)
	textContains(t, rejected, "\nPrevious rejection (verify whether resolved): "+strings.Repeat("m", 600), "a previous rejection is passed on, cut to 600 chars")
	textNotContains(t, rejected, strings.Repeat("m", 601), "the rejection is cut")
	errored := warm(`{"type":"audit_result","goalId":"GOAL","verdict":"error","report":"it broke","at":"2026-08-04T09:07:00.000Z"}`)
	textNotContains(t, errored, "Previous rejection", "an auditor error is not a rejection")
}

// animation-interval, animation-elapsed
func TestMutantsBCompleteAnimation(t *testing.T) {
	var elapsedEarly, elapsedLate float64
	var fc *goal.FakeClock
	var r *goalRig
	r, _ = mbAuditRig(t, "Timed goal", "", rigOptions{}, func(goal.RigAuditCall) goal.RigAuditResult {
		fc.Tick(79)
		elapsedEarly, _, _ = r.Probe.RigAuditProgress()
		fc.Tick(1)
		elapsedLate, _, _ = r.Probe.RigAuditProgress()
		return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "mock"}
	})
	fc = mbFakeClock(t, r.Probe, mbAt)
	r.callTool("update_goal", map[string]any{"status": "complete"})
	equal(t, elapsedEarly, 0, "the elapsed time stays until the first 80 ms tick")
	equal(t, elapsedLate, 80, "the spinner ticks every 80 ms and moves the elapsed time")
}

// commit-account: the progress up to the commit is charged even when the commit itself is rejected.
func TestMutantsBCompleteChargesProgress(t *testing.T) {
	f := newToolsFixture(t, "Charged goal", false, mbAt, "")
	r := newToolsRig(t, f, rigOptions{})
	r.Probe.SetCompletionAuditor(func(goal.RigAuditCall) goal.RigAuditResult {
		return goal.RigAuditResult{Approved: true, Output: "ok\n<approved/>", Model: "mock"}
	})
	fc := mbFakeClock(t, r.Probe, mbAt)
	r.startToolsSession()
	fc.Tick(30_000)
	restore := r.Probe.RejectMutations("")
	out := r.callTool("update_goal", map[string]any{"status": "complete"})
	restore()
	equal(t, out.Content, "Goal completion failed: the state mutation was rejected. The goal was not completed.", "rejected commit")
	equal(t, goal.ToolsActiveGoal(f.cwd)["usage"].(map[string]any)["activeSeconds"], 30, "the active time is charged before the commit")
}
