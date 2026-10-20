package pi_goal_x

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ported from goal-task-tools.ts. The task-tool support: flat parent-linked `set_goal_tasks` input -> recursive task tree (with the
// validation the recursive path enforced) and id-stable merging that preserves progress for matching ids. Tasks are []any of
// *jsObject, like deriveTasksFromObjective.

// maxGoalTasks is MAX_TASKS.
const maxGoalTasks = 50

// flatTaskParent is the trimmed parent_id of a flat item, "" when it has none.
func flatTaskParent(item any) string {
	p, _ := toolItemString(item, "parent_id")
	return jsTrim(p)
}

// flatTaskID is the trimmed id of a flat item ("" when it is not a string).
func flatTaskID(item any) string {
	id, _ := toolItemString(item, "id")
	return jsTrim(id)
}

// convertFlatTasks converts a flat parent-linked task list into the recursive task tree, validating non-empty unique ids and
// titles, existing parents, acyclic relationships, at most 50 tasks, the subtask depth within maxSubtaskDepth, and
// lightweight_subtasks only on tasks that have children. errMsg is "" on success.
func convertFlatTasks(flat []any, maxSubtaskDepth int) (tasks []any, errMsg string) {
	if len(flat) > maxGoalTasks {
		return nil, fmt.Sprintf("Task list cannot exceed %d tasks.", maxGoalTasks)
	}
	ids := map[string]bool{}
	for _, item := range flat {
		id := flatTaskID(item)
		if id == "" {
			return nil, "All tasks must have a non-empty id."
		}
		if ids[id] {
			return nil, fmt.Sprintf("Duplicate task id: \"%s\".", id)
		}
		ids[id] = true
		title, _ := toolItemString(item, "title")
		if jsTrim(title) == "" {
			return nil, fmt.Sprintf("Task \"%s\" must have a non-empty title.", id)
		}
	}

	// Parent must exist and relationships must be acyclic.
	byID := map[string]any{}
	for _, item := range flat {
		byID[flatTaskID(item)] = item
	}
	for _, item := range flat {
		parentID := flatTaskParent(item)
		if parentID != "" {
			if _, ok := byID[parentID]; !ok {
				return nil, fmt.Sprintf("Task \"%s\" references missing parent \"%s\".", flatTaskID(item), parentID)
			}
			// Walk up; if we return to the node itself, there is a cycle.
			cursor := byID[parentID]
			seen := map[string]bool{flatTaskID(item): true}
			for cursor != nil {
				if seen[flatTaskID(cursor)] {
					return nil, fmt.Sprintf("Cyclic parent relationship involving task \"%s\".", flatTaskID(cursor))
				}
				seen[flatTaskID(cursor)] = true
				if up := flatTaskParent(cursor); up != "" {
					cursor = byID[up]
				} else {
					cursor = nil
				}
			}
		}
	}

	// Build the tree.
	childrenOf := map[string][]any{}
	var roots []any
	for _, item := range flat {
		if parentID := flatTaskParent(item); parentID != "" {
			childrenOf[parentID] = append(childrenOf[parentID], item)
		} else {
			roots = append(roots, item)
		}
	}
	var buildNode func(item any) *jsObject
	buildNode = func(item any) *jsObject {
		node := newObject()
		node.set("id", flatTaskID(item))
		title, _ := toolItemString(item, "title")
		node.set("title", jsTrim(title))
		node.set("status", "pending")
		if vc, _ := toolItemString(item, "verification_contract"); jsTrim(vc) != "" {
			node.set("verificationContract", jsTrim(vc))
		} else {
			node.set("verificationContract", undef{})
		}
		if lw, _ := toolItemField(item, "lightweight_subtasks"); lw == true {
			node.set("lightweightSubtasks", true)
		} else {
			node.set("lightweightSubtasks", undef{})
		}
		if children := childrenOf[gstr(node, "id")]; len(children) > 0 {
			subs := make([]any, len(children))
			for i, child := range children {
				subs[i] = buildNode(child)
			}
			node.set("subtasks", subs)
		}
		return node
	}
	tasks = make([]any, len(roots))
	for i, r := range roots {
		tasks[i] = buildNode(r)
	}

	// Lightweight placement: lightweight_subtasks must be on a task with children.
	for _, item := range flat {
		if lw, _ := toolItemField(item, "lightweight_subtasks"); lw == true {
			if len(childrenOf[flatTaskID(item)]) == 0 {
				return nil, fmt.Sprintf("Task \"%s\" sets lightweight_subtasks but has no subtasks.", flatTaskID(item))
			}
		}
	}

	if v := findSubtaskDepthViolation(tasks, maxSubtaskDepth); v != "" {
		return nil, v
	}
	return tasks, ""
}

// mergeTasksWithExisting merges converted tasks into an existing tree. Matching ids preserve runtime progress ONLY (status, evidence,
// completion/skip timestamps, skip reason); incoming structural fields are authoritative and omission clears them.
func mergeTasksWithExisting(existing, incoming []any) []any {
	existingByID := map[string]*jsObject{}
	var index func(tasks []any)
	index = func(tasks []any) {
		for _, t := range tasks {
			o, ok := t.(*jsObject)
			if !ok {
				continue
			}
			existingByID[gstr(o, "id")] = o
			if subs, ok := o.vals["subtasks"].([]any); ok {
				index(subs)
			}
		}
	}
	index(existing)

	prop := func(o *jsObject, k string) any {
		if o != nil && o.has(k) {
			return o.vals[k]
		}
		return undef{}
	}
	var mergeTask func(input *jsObject) *jsObject
	mergeTask = func(input *jsObject) *jsObject {
		prior := existingByID[gstr(input, "id")]
		base := newObject()
		base.set("id", gstr(input, "id"))
		base.set("title", gstr(input, "title"))
		// Structural fields are authoritative; undefined (omitted) clears.
		base.set("verificationContract", prop(input, "verificationContract"))
		base.set("lightweightSubtasks", prop(input, "lightweightSubtasks"))
		if prior != nil {
			base.set("status", prop(prior, "status"))
			base.set("evidence", prop(prior, "evidence"))
			base.set("completedAt", prop(prior, "completedAt"))
			base.set("skippedAt", prop(prior, "skippedAt"))
			base.set("skipReason", prop(prior, "skipReason"))
		} else {
			base.set("status", "pending")
		}
		if subs, ok := input.vals["subtasks"].([]any); ok && len(subs) > 0 {
			out := make([]any, len(subs))
			for i, child := range subs {
				out[i] = mergeTask(child.(*jsObject))
			}
			base.set("subtasks", out)
		}
		return base
	}
	out := make([]any, len(incoming))
	for i, t := range incoming {
		out[i] = mergeTask(t.(*jsObject))
	}
	return out
}

// countTasks counts every node in a task tree (roots + all descendants).
func countTasks(tasks []any) int {
	total := 0
	for _, t := range tasks {
		total++
		if o, ok := t.(*jsObject); ok {
			if subs, ok := o.vals["subtasks"].([]any); ok {
				total += countTasks(subs)
			}
		}
	}
	return total
}

// omitKeys is `{ ...o }` without the keys.
func omitKeys(o *jsObject, keys ...string) *jsObject {
	out := newObject()
	for _, k := range o.keys {
		skip := false
		for _, d := range keys {
			skip = skip || d == k
		}
		if !skip {
			out.set(k, o.vals[k])
		}
	}
	return out
}

// taskProgressInput is TaskProgressInput; evidence and reason are "" with has* false when undefined.
type taskProgressInput struct {
	taskID, status     string
	evidence, reason   string
	hasEvidence, hasRe bool
}

func taskProgressOf(item any) taskProgressInput {
	in := taskProgressInput{}
	in.taskID, _ = toolItemString(item, "task_id")
	in.status, _ = toolItemString(item, "status")
	in.evidence, in.hasEvidence = toolItemString(item, "evidence")
	in.reason, in.hasRe = toolItemString(item, "reason")
	return in
}

// progressSpec is the batch form's per-update spec.
func progressSpec(input taskProgressInput, c *core) goalTaskUpdateSpec {
	settings := loadGoalSettings(c.h.Cwd())
	now := nowIso()
	evidence := ""
	if input.hasEvidence {
		evidence = utf16Prefix(jsTrim(input.evidence), 200)
	}
	reason := ""
	if input.hasRe {
		reason = jsTrim(input.reason)
	}
	token := c.focusedOperationToken(gstr(c.state(), "id"))
	spec := goalTaskUpdateSpec{
		taskId:     input.taskID,
		focusToken: &token,
		validate: func(task *jsObject) goalGate {
			taskStatus := gstr(task, "status")
			if input.status == "start" && taskStatus != "pending" {
				return goalGate{message: fmt.Sprintf("Task \"%s\" is %s; only pending tasks can be started.", gstr(task, "id"), taskStatus)}
			}
			if input.status != "start" && taskStatus == "complete" {
				return goalGate{message: fmt.Sprintf("Task \"%s\" is already complete and cannot be reopened.", gstr(task, "id"))}
			}
			if input.status == "complete" {
				if taskStatus == "skipped" {
					return goalGate{message: fmt.Sprintf("Task \"%s\" was already skipped.", gstr(task, "id"))}
				}
				if !settings.disableContracts && gstr(task, "verificationContract") != "" && evidence == "" {
					return goalGate{message: fmt.Sprintf("Task \"%s\" has a verification contract; provide evidence to complete it.", gstr(task, "id"))}
				}
				if gate := checkSubtasksComplete(task); gate != "" {
					return goalGate{message: gate}
				}
			}
			if input.status == "skipped" && reason == "" {
				return goalGate{message: "status=skipped requires a non-empty reason."}
			}
			if input.status == "pending" && taskStatus != "skipped" {
				return goalGate{message: "Only skipped tasks can be reopened with status=pending."}
			}
			return goalGate{ok: true}
		},
		update: func(task *jsObject) (*jsObject, goalGate) {
			switch input.status {
			case "start":
				return task, goalGate{ok: true}
			case "complete":
				return goalWith(task, "status", "complete", "completedAt", now, "evidence", optionalString(evidence)), goalGate{ok: true}
			case "skipped":
				next := goalWith(task, "status", "skipped", "skippedAt", now, "skipReason", reason)
				if subs, _ := task.vals["subtasks"].([]any); len(subs) > 0 && !gflag(task, "lightweightSubtasks") {
					return skipAllSubtasks(next, now, reason), goalGate{ok: true}
				}
				return next, goalGate{ok: true}
			}
			return goalWith(omitKeys(task, "skippedAt", "skipReason"), "status", "pending"), goalGate{ok: true}
		},
		ledger: func(written, _ *jsObject) ([]*jsObject, error) {
			ev := newObject()
			ev.set("goalId", gstr(written, "id"))
			ev.set("taskId", input.taskID)
			ev.set("at", gstr(written, "updatedAt"))
			switch input.status {
			case "start":
				ev.set("type", "task_started")
			case "complete":
				ev.set("type", "task_complete")
				ev.set("evidence", optionalString(evidence))
			case "skipped":
				ev.set("type", "task_skipped")
				ev.set("reason", reason)
			default:
				ev.set("type", "task_reopened")
			}
			return []*jsObject{ev}, nil
		},
	}
	if input.status == "start" {
		id := input.taskID
		spec.setCurrentTaskId = &id
	}
	return spec
}

// ---- tool registration ----

func registerTaskTools(c *core) {
	p := c.pi
	e := p.e
	e.RegisterTool(sdk.ToolDefinition{
		Name:             setGoalTasksToolName,
		Label:            "Set Goal Tasks",
		Description:      "Set or restructure the task tree with user confirmation. Matching IDs retain progress; removed IDs are deleted. Confirmation stops the turn; continuation follows.",
		PromptSnippet:    "Set the goal task tree with confirmation.",
		PromptGuidelines: []string{"Use tasks only for useful milestones. Restructure an existing tree only when the user asks or requirements structurally change. Input is a flat parent-linked tree, at most 50 tasks, within configured depth. lightweight_subtasks is valid only on parents."},
		Parameters: schemaObject(schemaMap{
			"tasks": schemaMap{"type": "array", "description": "Flat parent-linked task list", "items": sdk.Schema{"type": "object", "properties": schemaMap{
				"id":                    schemaString("Short stable slug e.g. 'task-1'"),
				"title":                 schemaString("Human-readable task title"),
				"parent_id":             schemaString("Parent id; omit for roots."),
				"verification_contract": schemaString("Required completion evidence."),
				"lightweight_subtasks":  schemaBool("Children do not gate parent completion."),
			}, "required": []string{"id", "title"}}},
			"block_completion": schemaBool("Require all tasks resolved; default false."),
			"change_summary":   schemaString("Optional summary of the task list change"),
		}, []string{"tasks"}),
		ExecutionMode: "sequential",
		Execute:       p.execute(executeSetGoalTasks),
		RenderCall: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			summary := ""
			if cs, _ := args["change_summary"].(string); cs != "" {
				summary = truncateText(cs, 80)
			} else {
				n := 0
				if tasks, ok := args["tasks"].([]any); ok {
					n = len(tasks)
				}
				summary = fmt.Sprintf("%d tasks", n)
			}
			return toolRenderCallLines(sdkTheme{ctx.UITheme()}, "set_goal_tasks ", summary, args, width), nil
		},
		RenderResult: toolRenderResult(),
	})

	statuses := schemaEnum("start", "complete", "skipped", "pending")
	e.RegisterTool(sdk.ToolDefinition{
		Name:             updateGoalTaskToolName,
		Label:            "Update Goal Task",
		Description:      "Update task progress without stopping the turn. Use ordered updates for an atomic batch, or task_id/status for one task. An invalid update rejects the whole batch.",
		PromptSnippet:    "Start, complete, skip, or reopen tasks; batch related progress.",
		PromptGuidelines: []string{"start requires pending and sets current task. complete requires evidence for contracted tasks and completed/skipped non-lightweight children. skipped requires a reason and explicit user direction or a hard contradiction; never skip to avoid work. pending reopens skipped tasks only; completed tasks are immutable. Completing/skipping the current task clears focus."},
		Parameters: schemaObject(schemaMap{
			"task_id": schemaString("Single-task form; omit with updates."),
			"status":  statuses,
			"updates": schemaMap{"type": "array", "minItems": 1, "maxItems": 100, "description": "Ordered atomic batch; omit all single-task fields.", "items": schemaObject(schemaMap{
				"task_id":  schemaMap{"type": "string"},
				"status":   statuses,
				"evidence": schemaMap{"type": "string"},
				"reason":   schemaMap{"type": "string"},
			}, []string{"task_id", "status"})},
			"evidence": schemaString("Completion evidence; max 200 chars."),
			"reason":   schemaString("Required for skipped."),
		}, nil),
		ExecutionMode: "sequential",
		Execute:       p.execute(executeUpdateGoalTask),
		RenderCall: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			id, _ := args["task_id"].(string)
			status, _ := args["status"].(string)
			return toolRenderCallLines(sdkTheme{ctx.UITheme()}, "update_goal_task ", id+" "+status, args, width), nil
		},
		RenderResult: toolRenderResult(),
	})
}

// renderTaskProposalLines renders the proposed STRUCTURAL tree for the confirmation dialog.
func renderTaskProposalLines(tasks []any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	var lines []string
	for _, t := range tasks {
		o := t.(*jsObject)
		marker := "[ ]"
		switch gstr(o, "status") {
		case "complete":
			marker = "[x]"
		case "skipped":
			marker = "[~]"
		}
		lw := ""
		if gflag(o, "lightweightSubtasks") {
			lw = " (lightweight)"
		}
		lines = append(lines, fmt.Sprintf("%s%s %s: %s%s", prefix, marker, gstr(o, "id"), gstr(o, "title"), lw))
		if subs, _ := o.vals["subtasks"].([]any); len(subs) > 0 {
			lines = append(lines, renderTaskProposalLines(subs, indent+1)...)
		}
	}
	return lines
}

func executeSetGoalTasks(c *core, params map[string]any) (goalAgentResult, error) {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil {
		return c.textResult("No goal is set; set_goal_tasks requires a focused active or paused goal."), nil
	}
	settings := loadGoalSettings(c.h.Cwd())
	if settings.disableTasks {
		return c.textResult("set_goal_tasks is disabled by settings (disableTasks: true)."), nil
	}
	if st := gstr(c.state(), "status"); st != "active" && st != "paused" {
		return c.textResult(fmt.Sprintf("set_goal_tasks applies to an active or paused goal; this goal is %s.", statusLabel(c.state()))), nil
	}
	flat, isArray := params["tasks"].([]any)
	if !isArray {
		return c.textResult("tasks must be an array."), nil
	}
	tasks, message := convertFlatTasks(flat, int(settings.subtaskDepth))
	if message != "" {
		return c.textResult(message), nil
	}
	blockCompletion := params["block_completion"] == true
	now := nowIso()

	taskLines := renderTaskProposalLines(tasks, 0)
	gateLabel := ""
	if blockCompletion {
		gateLabel = " (blockCompletion enabled)"
	}
	proposalText := strings.Join(append([]string{"Proposed task list" + gateLabel + ":", ""}, taskLines...), "\n")
	taskListFocus := c.focusedOperationToken(gstr(c.state(), "id"))
	// Task-only confirmation: the complete result is the user's decision. No auditor toggle and no goal-state mutation happen here.
	c.enterGoalModal()
	var dialog taskConfirmationResult
	var dialogErr error
	c.awaiting(func() { dialog, dialogErr = showTaskConfirmation(hostDialogCtx{c.h}, proposalText) })
	c.exitGoalModal()
	if dialogErr != nil {
		return goalAgentResult{}, dialogErr
	}
	if !c.isFocusedOperationCurrent(taskListFocus) {
		return c.focusedOperationCancelledResult("Task list proposal", taskListFocus), nil
	}
	if dialog.decision != "confirm" {
		return c.textResult("Task list kept unchanged."), nil
	}
	applied, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       falsePtr(),
		focusToken:      &taskListFocus,
		refreshFromDisk: true,
		// Merge the confirmed structural input against the disk-refreshed clone so a concurrent external edit is preserved unless the
		// requested operation changes the same task.
		mutate: func(g *jsObject) *jsObject {
			var existing []any
			if tl := g.obj("taskList"); tl != nil {
				existing, _ = tl.vals["tasks"].([]any)
			}
			merged := mergeTasksWithExisting(existing, tasks)
			// §7.5: preserve currentTaskId only when the same id remains pending; otherwise clear it.
			var currentTaskID any = undef{}
			if id := gstr(g, "currentTaskId"); id != "" && currentTaskIdIsPending(merged, id) {
				currentTaskID = id
			}
			list := newObject()
			list.set("tasks", merged)
			list.set("blockCompletion", blockCompletion)
			list.set("proposedAt", now)
			return goalWith(g, "currentTaskId", currentTaskID, "taskList", list, "updatedAt", now)
		},
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			var written2 []any
			if tl := written.obj("taskList"); tl != nil {
				written2, _ = tl.vals["tasks"].([]any)
			}
			return []*jsObject{ledgerEvent("task_list_set", "goalId", gstr(written, "id"), "taskCount", float64(countTasks(written2)),
				"blockCompletion", blockCompletion, "at", gstr(written, "updatedAt"))}, nil
		},
	})
	if err != nil {
		return goalAgentResult{}, err
	}
	if !applied.ok {
		// A stale writer receives a typed conflict carrying the current revision; whole-tree replacement is authoritative and must not
		// silently merge unknown new structure (follow-up Stage 4).
		return c.textResult(fmt.Sprintf("Task list not applied: %s. Review the current goal and re-propose the task list.", mutationMessage(applied))), nil
	}
	c.runtime.markTurnStopped(gstr(c.state(), "id"))
	c.updateUI()
	var confirmed []any
	if tl := c.state().obj("taskList"); tl != nil {
		confirmed, _ = tl.vals["tasks"].([]any)
	}
	n := countTasks(confirmed)
	plural := "s"
	if n == 1 {
		plural = ""
	}
	r := c.textResult(fmt.Sprintf("Task list set and confirmed. %d task%s.%s", n, plural, gateLabel))
	r.terminate = true
	return r, nil
}

func taskSummaryOf(c *core) string {
	return buildTaskSummary(c.state().obj("taskList"))
}

func executeUpdateGoalTask(c *core, raw map[string]any) (goalAgentResult, error) {
	if toolParamPresent(raw, "updates") {
		fail := c.textResult
		for _, k := range []string{"task_id", "status", "evidence", "reason"} {
			if toolParamPresent(raw, k) {
				return fail("Use either updates or single-task fields, never both."), nil
			}
		}
		updates, ok := raw["updates"].([]any)
		valid := ok && len(updates) >= 1 && len(updates) <= 100
		for _, u := range updates {
			if !valid {
				break
			}
			id, idOK := toolItemString(u, "task_id")
			status, _ := toolItemString(u, "status")
			okStatus := status == "start" || status == "complete" || status == "skipped" || status == "pending"
			valid = idOK && jsTrim(id) != "" && okStatus
			if ev, has := toolItemField(u, "evidence"); has {
				if _, isStr := ev.(string); !isStr {
					valid = false
				}
			}
			if rs, has := toolItemField(u, "reason"); has {
				if _, isStr := rs.(string); !isStr {
					valid = false
				}
			}
			if _, isObj := u.(map[string]any); !isObj {
				valid = false
			}
		}
		if !valid {
			return fail("updates must contain 1–100 valid task updates."), nil
		}
		c.reconcileFocusedGoalFromDisk(false)
		if loadGoalSettings(c.h.Cwd()).disableTasks {
			return fail("update_goal_task is disabled by settings (disableTasks: true)."), nil
		}
		if c.state() == nil {
			return fail("No goal is focused."), nil
		}
		if st := gstr(c.state(), "status"); st != "active" {
			return fail(fmt.Sprintf("update_goal_task applies only to an active goal (current status: %s).", st)), nil
		}
		specs := make([]goalTaskUpdateSpec, len(updates))
		for i, u := range updates {
			specs[i] = progressSpec(taskProgressOf(u), c)
		}
		result, err := c.goalService.updateTasks(c.st, specs)
		if err != nil {
			return goalAgentResult{}, err
		}
		if !result.ok {
			return fail(result.message), nil
		}
		c.updateUI()
		parts := make([]string, len(updates))
		for i, u := range updates {
			in := taskProgressOf(u)
			parts[i] = in.taskID + " " + in.status
		}
		return fail(fmt.Sprintf("%s. %s.", strings.Join(parts, "; "), buildTaskSummary(result.goal.obj("taskList")))), nil
	}
	taskID, _ := toolParamString(raw, "task_id")
	status, _ := toolParamString(raw, "status")
	if taskID == "" || status == "" {
		return c.textResult("Provide task_id and status, or an updates batch."), nil
	}
	params := taskProgressOf(raw)
	c.reconcileFocusedGoalFromDisk(false)
	if loadGoalSettings(c.h.Cwd()).disableTasks {
		return c.textResult("update_goal_task is disabled by settings (disableTasks: true)."), nil
	}
	// update_goal_task applies only to an active goal with an existing task list; invalid lifecycle calls return a state-aware failure.
	if c.state() == nil {
		return c.textResult("No goal is focused."), nil
	}
	if st := gstr(c.state(), "status"); st != "active" {
		return c.textResult(fmt.Sprintf("update_goal_task applies only to an active goal (current status: %s).", st)), nil
	}
	if c.state().obj("taskList") == nil {
		return c.textResult("The goal has no task list."), nil
	}
	settings := loadGoalSettings(c.h.Cwd())
	now := nowIso()
	taskFocus := c.focusedOperationToken(gstr(c.state(), "id"))
	ok := goalGate{ok: true}
	event := func(typ string, written *jsObject, kv ...any) []*jsObject {
		return []*jsObject{ledgerEvent(typ, append([]any{"goalId", gstr(written, "id"), "taskId", params.taskID}, append(kv, "at", gstr(written, "updatedAt"))...)...)}
	}
	finish := func(result goalTaskUpdateOutcome, err error, text func() string) (goalAgentResult, error) {
		if err != nil {
			return goalAgentResult{}, err
		}
		if !result.ok {
			return c.textResult(result.message), nil
		}
		c.updateUI()
		return c.textResult(text()), nil
	}

	switch params.status {
	case "start":
		id := params.taskID
		result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
			focusToken: &taskFocus,
			taskId:     params.taskID,
			validate: func(task *jsObject) goalGate {
				if gstr(task, "status") != "pending" {
					return goalGate{message: fmt.Sprintf("Task \"%s\" is %s; only pending tasks can be started.", params.taskID, gstr(task, "status"))}
				}
				return ok
			},
			update: func(task *jsObject) (*jsObject, goalGate) { return task, ok },
			// §8.1: set explicit execution focus; a later start replaces it, and completing/skipping this task clears it.
			setCurrentTaskId: &id,
			ledger:           func(written, _ *jsObject) ([]*jsObject, error) { return event("task_started", written), nil },
		})
		return finish(result, err, func() string {
			// §8.1: surface the task contract so the next continuation prompt and the dashboard can show what starting this task requires.
			var tasks []any
			if tl := c.state().obj("taskList"); tl != nil {
				tasks, _ = tl.vals["tasks"].([]any)
			}
			contract := ""
			if started := findTaskInTree(tasks, params.taskID); started != nil && gstr(started, "verificationContract") != "" {
				contract = " Contract: " + gstr(started, "verificationContract")
			}
			return fmt.Sprintf("Started %s%s. %s.", params.taskID, contract, taskSummaryOf(c))
		})
	case "complete":
		evidence := ""
		if params.hasEvidence {
			evidence = utf16Prefix(jsTrim(params.evidence), 200)
		}
		result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
			focusToken: &taskFocus,
			taskId:     params.taskID,
			validate: func(task *jsObject) goalGate {
				switch gstr(task, "status") {
				case "complete":
					return goalGate{message: fmt.Sprintf("Task \"%s\" is already complete.", params.taskID)}
				case "skipped":
					return goalGate{message: fmt.Sprintf("Task \"%s\" was already skipped.", params.taskID)}
				}
				if !settings.disableContracts && gstr(task, "verificationContract") != "" && evidence == "" {
					return goalGate{message: fmt.Sprintf("Task \"%s\" has a verification contract; provide evidence to complete it.", params.taskID)}
				}
				if gate := checkSubtasksComplete(task); gate != "" {
					return goalGate{message: gate}
				}
				return ok
			},
			update: func(task *jsObject) (*jsObject, goalGate) {
				return goalWith(task, "status", "complete", "completedAt", now, "evidence", optionalString(evidence)), ok
			},
			ledger: func(written, _ *jsObject) ([]*jsObject, error) {
				return event("task_complete", written, "evidence", optionalString(evidence)), nil
			},
		})
		return finish(result, err, func() string { return fmt.Sprintf("%s complete. %s.", params.taskID, taskSummaryOf(c)) })
	case "skipped":
		reason := jsTrim(params.reason)
		if !params.hasRe || reason == "" {
			return c.textResult("update_goal_task(status=skipped) requires a non-empty reason."), nil
		}
		result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
			focusToken: &taskFocus,
			taskId:     params.taskID,
			validate: func(task *jsObject) goalGate {
				if gstr(task, "status") == "complete" {
					return goalGate{message: fmt.Sprintf("Task \"%s\" is already complete.", params.taskID)}
				}
				return ok
			},
			update: func(task *jsObject) (*jsObject, goalGate) {
				base := goalWith(task, "status", "skipped", "skippedAt", now, "skipReason", reason)
				if subs, _ := task.vals["subtasks"].([]any); len(subs) > 0 && !gflag(task, "lightweightSubtasks") {
					return skipAllSubtasks(base, now, reason), ok
				}
				return base, ok
			},
			ledger: func(written, _ *jsObject) ([]*jsObject, error) {
				return event("task_skipped", written, "reason", reason), nil
			},
		})
		return finish(result, err, func() string { return fmt.Sprintf("%s skipped. %s.", params.taskID, taskSummaryOf(c)) })
	}

	// status === "pending": reopen a skipped task; completed tasks are immutable.
	result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
		focusToken: &taskFocus,
		taskId:     params.taskID,
		validate: func(task *jsObject) goalGate {
			if gstr(task, "status") == "complete" {
				return goalGate{message: fmt.Sprintf("Task \"%s\" is complete and cannot be reopened through update_goal_task.", params.taskID)}
			}
			if gstr(task, "status") != "skipped" {
				return goalGate{message: fmt.Sprintf("Task \"%s\" is not skipped; only skipped tasks can be reopened with status=pending.", params.taskID)}
			}
			return ok
		},
		update: func(task *jsObject) (*jsObject, goalGate) {
			return goalWith(omitKeys(task, "skippedAt", "skipReason"), "status", "pending"), ok
		},
		ledger: func(written, _ *jsObject) ([]*jsObject, error) { return event("task_reopened", written), nil },
	})
	return finish(result, err, func() string { return fmt.Sprintf("%s reopened. %s.", params.taskID, taskSummaryOf(c)) })
}
