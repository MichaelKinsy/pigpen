package pi_goal_x

import (
	"fmt"
	"regexp"
	"strings"
)

// ported from goal-policy.ts. buildTaskSummary and clearGoalCommandMessage live in format.go.
// Goal and task records are *jsObject; a nil goal is the TS null.

// policyValidation is PolicyValidation: ok, or a rejection message.
type policyValidation struct {
	ok      bool
	message string
}

var policyOK = policyValidation{ok: true}

func policyReject(msg string) policyValidation { return policyValidation{message: msg} }

func isRunnableStatus(status string) bool { return status == "active" }

// isCompletableStatus: a budget-limited goal may still be completed; a blocked one may not.
func isCompletableStatus(status string) bool {
	return status == "active" || status == "paused" || status == "budget_limited"
}

// validateGoalCompletion; runningGoalId "" means none.
func validateGoalCompletion(goal *jsObject, runningGoalId string) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	if runningGoalId != "" && gstr(goal, "id") != runningGoalId {
		return policyReject("The active goal changed during this run; not marking it complete.")
	}
	if !isCompletableStatus(gstr(goal, "status")) {
		return policyReject(fmt.Sprintf("Goal is %s; update_goal(complete) does not apply.", statusLabel(goal)))
	}
	return policyOK
}

// validateGoalBlock: update_goal(blocked) applies only to an active goal.
func validateGoalBlock(goal *jsObject) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	if gstr(goal, "status") != "active" {
		return policyReject(fmt.Sprintf("Goal is %s; update_goal(blocked) applies only to an active goal.", statusLabel(goal)))
	}
	return policyOK
}

// validateGoalAgentPause: update_goal(paused) applies only to an active goal.
func validateGoalAgentPause(goal *jsObject) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	if gstr(goal, "status") != "active" {
		return policyReject(fmt.Sprintf("Goal is %s; update_goal(paused) applies only to an active goal.", statusLabel(goal)))
	}
	return policyOK
}

func validateResumeGoal(goal *jsObject) policyValidation {
	if goal == nil {
		return policyReject("No goal is set. Use /goal to draft one, or /goal-direct <objective> to start immediately.")
	}
	status := gstr(goal, "status")
	if status == "complete" {
		return policyReject("Goal is complete. Use /goal to draft a new one, or /goal-direct <objective> to start immediately.")
	}
	if status == "active" && gflag(goal, "autoContinue") {
		return policyReject("Goal is already running.")
	}
	return policyOK
}

func taskListTasks(list *jsObject) []any {
	if list == nil {
		return nil
	}
	ts, _ := list.vals["tasks"].([]any)
	return ts
}

// taskCompletionBlockWarning returns "" (TS null) when there is no warning.
func taskCompletionBlockWarning(taskList *jsObject) string {
	if !gflag(taskList, "blockCompletion") {
		return ""
	}
	pending := countTaskSubtree(taskListTasks(taskList)).pending
	if pending == 0 {
		return ""
	}
	s := ""
	if pending > 1 {
		s = "s"
	}
	return fmt.Sprintf("%d task%s still pending with blockCompletion enabled. Complete or skip all pending tasks before finishing the goal.", pending, s)
}

func goalTaskList(goal *jsObject) *jsObject { return goal.obj("taskList") }

func validateTaskCompletion(goal *jsObject, taskId string) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	tl := goalTaskList(goal)
	if tl == nil {
		return policyReject("Goal has no task list.")
	}
	task := findTaskInTree(taskListTasks(tl), taskId)
	if task == nil {
		return policyReject(fmt.Sprintf("Task \"%s\" not found.", taskId))
	}
	switch gstr(task, "status") {
	case "complete":
		return policyReject(fmt.Sprintf("Task \"%s\" is already complete.", taskId))
	case "skipped":
		return policyReject(fmt.Sprintf("Task \"%s\" was already skipped.", taskId))
	}
	return policyOK
}

func validateTaskSkip(goal *jsObject, taskId, reason string) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	tl := goalTaskList(goal)
	if tl == nil {
		return policyReject("Goal has no task list.")
	}
	task := findTaskInTree(taskListTasks(tl), taskId)
	if task == nil {
		return policyReject(fmt.Sprintf("Task \"%s\" not found.", taskId))
	}
	switch gstr(task, "status") {
	case "complete":
		return policyReject(fmt.Sprintf("Task \"%s\" is already complete.", taskId))
	case "skipped":
		return policyOK // toggled by the executor
	}
	if jsTrim(reason) == "" {
		return policyReject("Skipping requires a non-empty reason.")
	}
	return policyOK
}

func taskSubtasks(task *jsObject) []any {
	subs, _ := task.vals["subtasks"].([]any)
	return subs
}

// measureSubtaskDepth is the maximum nesting depth of a task's subtasks (root = 0).
func measureSubtaskDepth(task *jsObject) int {
	subs := taskSubtasks(task)
	if len(subs) == 0 {
		return 0
	}
	maxChild := 0
	for _, c := range subs {
		if ct, _ := c.(*jsObject); ct != nil {
			if d := measureSubtaskDepth(ct); d > maxChild {
				maxChild = d
			}
		}
	}
	return maxChild + 1
}

// findSubtaskDepthViolation returns the first violation message, or "" when valid.
func findSubtaskDepthViolation(tasks []any, maxDepth int) string {
	for _, x := range tasks {
		task, _ := x.(*jsObject)
		if task == nil {
			continue
		}
		if depth := measureSubtaskDepth(task); depth > maxDepth {
			return fmt.Sprintf("Task \"%s\" has subtask nesting depth %d, exceeding the configured maximum of %d", gstr(task, "id"), depth, maxDepth)
		}
		if v := findSubtaskDepthViolation(taskSubtasks(task), maxDepth); v != "" {
			return v
		}
	}
	return ""
}

func checkDuplicateTaskIds(tasks []any, ids map[string]bool) string {
	for _, x := range tasks {
		t, _ := x.(*jsObject)
		if t == nil {
			continue
		}
		id := jsTrim(gstr(t, "id"))
		if id == "" {
			return "All tasks must have a non-empty id."
		}
		if ids[id] {
			return fmt.Sprintf("Duplicate task id: \"%s\".", id)
		}
		ids[id] = true
		if e := checkDuplicateTaskIds(taskSubtasks(t), ids); e != "" {
			return e
		}
	}
	return ""
}

// validateTaskListProposal; maxSubtaskDepth nil means the default 1.
func validateTaskListProposal(goal *jsObject, tasks []any, maxSubtaskDepth *int) policyValidation {
	if goal == nil {
		return policyReject("No goal is set.")
	}
	if len(tasks) > 50 {
		return policyReject("Task list cannot exceed 50 tasks.")
	}
	ids := map[string]bool{}
	for _, x := range tasks {
		t, _ := x.(*jsObject)
		if t == nil {
			continue
		}
		id := gstr(t, "id")
		if jsTrim(id) == "" {
			return policyReject("All tasks must have a non-empty id.")
		}
		if jsTrim(gstr(t, "title")) == "" {
			return policyReject(fmt.Sprintf("Task \"%s\" must have a non-empty title.", id))
		}
		if ids[id] {
			return policyReject(fmt.Sprintf("Duplicate task id: \"%s\".", id))
		}
		ids[id] = true
		if subs := taskSubtasks(t); len(subs) > 0 {
			if e := checkDuplicateTaskIds(subs, ids); e != "" {
				return policyReject(e)
			}
		}
	}
	maxDepth := 1
	if maxSubtaskDepth != nil {
		maxDepth = *maxSubtaskDepth
	}
	if v := findSubtaskDepthViolation(tasks, maxDepth); v != "" {
		return policyReject(v)
	}
	return policyOK
}

// findTaskInTree finds a task by id depth-first; nil when missing.
func findTaskInTree(tasks []any, taskId string) *jsObject {
	for _, x := range tasks {
		t, _ := x.(*jsObject)
		if t == nil {
			continue
		}
		if gstr(t, "id") == taskId {
			return t
		}
		if f := findTaskInTree(taskSubtasks(t), taskId); f != nil {
			return f
		}
	}
	return nil
}

// updateTaskInTree applies updater to the task with the id. Unchanged branches keep their identity; when nothing changed the
// input slice is returned itself.
func updateTaskInTree(tasks []any, taskId string, updater func(*jsObject) *jsObject) []any {
	var next []any
	for i, x := range tasks {
		task, _ := x.(*jsObject)
		if task == nil {
			continue
		}
		updated := task
		if gstr(task, "id") == taskId {
			updated = updater(task)
		} else if subs, ok := task.vals["subtasks"].([]any); ok {
			ns := updateTaskInTree(subs, taskId, updater)
			if !sameSlice(ns, subs) {
				updated = task.clone()
				updated.set("subtasks", ns)
			}
		}
		if updated != task {
			if next == nil {
				next = append([]any{}, tasks...)
			}
			next[i] = updated
		}
	}
	if next == nil {
		return tasks
	}
	return next
}

func sameSlice(a, b []any) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// checkSubtasksComplete returns "" when all subtasks are done (or lightweight), else the message.
func checkSubtasksComplete(task *jsObject) string {
	subs := taskSubtasks(task)
	if len(subs) == 0 || gflag(task, "lightweightSubtasks") {
		return ""
	}
	for _, x := range subs {
		child, _ := x.(*jsObject)
		if child == nil {
			continue
		}
		if gstr(child, "status") == "pending" {
			return fmt.Sprintf("Task \"%s\" has pending subtask \"%s\". Complete or skip all subtasks first.", gstr(task, "id"), gstr(child, "id"))
		}
		if m := checkSubtasksComplete(child); m != "" {
			return m
		}
	}
	return ""
}

// skipAllSubtasks recursively skips every non-complete subtask; returns the task itself when it has none.
func skipAllSubtasks(task *jsObject, now, reason string) *jsObject {
	subs := taskSubtasks(task)
	if len(subs) == 0 {
		return task
	}
	out := make([]any, len(subs))
	for i, x := range subs {
		child, _ := x.(*jsObject)
		if child == nil || gstr(child, "status") == "complete" {
			out[i] = x
			continue
		}
		sk := child.clone()
		sk.set("status", "skipped")
		sk.set("skippedAt", now)
		sk.set("skipReason", reason)
		out[i] = skipAllSubtasks(sk, now, reason)
	}
	res := task.clone()
	res.set("subtasks", out)
	return res
}

// completionReportArgs are the buildCompletionReport arguments; "" means absent/null.
type completionReportArgs struct {
	detailedSummary, auditorReport, auditSkippedReason, taskSummary string
}

func buildCompletionReport(a completionReportArgs) string {
	skipped := jsTrim(a.auditSkippedReason)
	report := jsTrim(a.auditorReport)
	var lines []string
	switch {
	case skipped != "":
		lines = []string{"Goal audit skipped.", "", "Reason: " + skipped, "", "Goal complete."}
	case report != "":
		lines = []string{"Goal audit approved.", "", "Auditor approval:", report, "", "Goal complete."}
	default:
		lines = []string{"Goal complete."}
	}
	if ts := jsTrim(a.taskSummary); ts != "" {
		lines = append(lines, "", "Task summary: "+ts)
	}
	lines = append(lines, "", a.detailedSummary)
	return strings.Join(lines, "\n")
}

// goalCreatedReportArgs mirror GoalCreatedReportArgs; pointers are the optional fields.
type goalCreatedReportArgs struct {
	objective            string
	detailedSummary      string
	confirmed            bool
	goalId, filePath     string
	taskCount            *int
	verificationContract string
	auditorEnabled       *bool
	tokenBudget          *float64
}

// formatGoalBudget; nil budget is "none".
func formatGoalBudget(budget *float64) string {
	if budget == nil {
		return "Budget: none"
	}
	return "Budget: " + jsNumber(*budget) + " tokens"
}

func buildGoalCreatedReport(a goalCreatedReportArgs) string {
	opening := []string{"Goal confirmed and created."}
	if a.confirmed {
		opening = []string{"✓ Goal created and focused.", "Continuing automatically with the confirmed plan."}
	}
	lines := append(append([]string{}, opening...), "", "Finalized goal:", "", jsTrim(a.objective))
	var details []string
	if a.goalId != "" {
		details = append(details, "Goal id: "+a.goalId)
	}
	if a.filePath != "" {
		details = append(details, "File: "+a.filePath)
	}
	if a.taskCount != nil {
		details = append(details, fmt.Sprintf("Tasks: %d", *a.taskCount))
	}
	if v := jsTrim(a.verificationContract); v != "" {
		details = append(details, "Verification: "+v)
	}
	if a.auditorEnabled != nil {
		state := "disabled"
		if *a.auditorEnabled {
			state = "enabled"
		}
		details = append(details, "Auditor: "+state)
	}
	details = append(details, formatGoalBudget(a.tokenBudget))
	if s := jsTrim(a.detailedSummary); s != "" {
		details = append(details, s)
	}
	lines = append(lines, "", "Goal details:")
	lines = append(lines, details...)
	return strings.Join(lines, "\n")
}

var (
	orderedNumberedRE = regexp.MustCompile(`(?m)^` + jsS + `*\d{1,2}` + jsS + `*[.):]`)
	orderedStepRE     = regexp.MustCompile(`(?mi)^` + jsS + `*step` + jsS + `+\d+`)
	lineBreakNorm     = strings.NewReplacer("\r", "\n", "\u2028", "\n", "\u2029", "\n")
)

// countOrderedSteps counts numbered items or "Step N" markers, whichever is more. JavaScript's ^ also matches after \r,
// U+2028 and U+2029; those (all white space) are normalized to \n, which counts the same.
func countOrderedSteps(objective string) int {
	norm := lineBreakNorm.Replace(objective)
	numbered := len(orderedNumberedRE.FindAllStringIndex(norm, -1))
	steps := len(orderedStepRE.FindAllStringIndex(jsFoldSafe(norm), -1))
	if steps > numbered {
		return steps
	}
	return numbered
}

// sisyphusStepProgress returns ok=false for non-sisyphus goals or objectives without ordered markers.
func sisyphusStepProgress(goal *jsObject) (current, total int, ok bool) {
	if !gflag(goal, "sisyphus") {
		return 0, 0, false
	}
	total = countOrderedSteps(gstr(goal, "objective"))
	if total == 0 {
		return 0, 0, false
	}
	current = 1
	if tasks := taskListTasks(goal.obj("taskList")); len(tasks) > 0 {
		done := 0
		for _, x := range tasks {
			if t, _ := x.(*jsObject); t != nil && gstr(t, "status") == "complete" {
				done++
			}
		}
		current = done + 1
		if total < current {
			current = total
		}
	}
	return current, total, true
}

func shouldQueueContinuation(goal *jsObject) bool {
	return goal != nil && gstr(goal, "status") == "active" && gflag(goal, "autoContinue")
}

func shouldArmPostCompactReminder(goal *jsObject) bool {
	return goal != nil && isRunnableStatus(gstr(goal, "status"))
}

func shouldInjectPostCompactReminder(pending bool, goal *jsObject) bool {
	return pending && goal != nil
}
