package tintinweb_tasks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func textResult(msg string) sdk.ToolResult { return sdk.ToolResult{Content: msg} }

// Schemas: the TypeBox parameters of the original, as the JSON Schema they compile to. upstream: index.ts tool parameters.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func objectSchema(props map[string]any, required ...string) sdk.Schema {
	s := sdk.Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}

func recordProp(desc string) map[string]any {
	return map[string]any{"type": "object", "patternProperties": map[string]any{"^.*$": map[string]any{}}, "description": desc}
}

func stringList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// guarded runs a tool body under the app lock and turns a failure to lock or write the store (a panic with an
// error, as the original throws) into the tool's error result.
func (a *app) guarded(fn func(ctx sdk.Context, p map[string]any) (string, error)) sdk.ToolFunc {
	return func(ctx sdk.Context, p map[string]any) (res any, err error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					res, err = nil, e
					return
				}
				panic(r)
			}
		}()
		text, err := fn(ctx, p)
		if err != nil {
			return nil, err
		}
		return textResult(text), nil
	}
}

func strParam(p map[string]any, key string) (string, bool) {
	s, ok := p[key].(string)
	return s, ok && p[key] != nil
}

func listParam(p map[string]any, key string) []string {
	out := []string{}
	if l, ok := p[key].([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (a *app) openBlockers(t *task, missingCounts bool) []string {
	var open []string
	for _, bid := range t.BlockedBy {
		b := a.store.get(bid)
		if missingCounts {
			if b == nil || b.Status != statusCompleted {
				open = append(open, "#"+bid)
			}
		} else if b != nil && b.Status != statusCompleted {
			open = append(open, "#"+bid)
		}
	}
	return open
}

func (a *app) registerTools(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskCreate", Label: "TaskCreate", Description: taskCreateDescription, PromptGuidelines: taskCreateGuidelines,
		Parameters: objectSchema(map[string]any{
			"subject":     strProp("A brief title for the task"),
			"description": strProp("A detailed description of what needs to be done"),
			"activeForm":  strProp("Present continuous form shown in spinner when in_progress (e.g., 'Running tests')"),
			"agentType":   strProp("Agent type for subagent execution (e.g., 'general-purpose', 'Explore'). Tasks with agentType can be started via TaskExecute."),
			"metadata":    recordProp("Arbitrary metadata to attach to the task"),
		}, "subject", "description"),
		Execute: a.guarded(a.taskCreate),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskList", Label: "TaskList", Description: taskListDescription,
		Parameters: objectSchema(map[string]any{}),
		Execute:    a.guarded(a.taskList),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskGet", Label: "TaskGet", Description: taskGetDescription,
		Parameters: objectSchema(map[string]any{"taskId": strProp("The ID of the task to retrieve")}, "taskId"),
		Execute:    a.guarded(a.taskGet),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskUpdate", Label: "TaskUpdate", Description: taskUpdateDescription,
		Parameters: objectSchema(map[string]any{
			"taskId":       strProp("The ID of the task to update"),
			"status":       map[string]any{"type": "string", "enum": []any{"pending", "in_progress", "completed", "deleted"}, "description": "New status for the task"},
			"subject":      strProp("New subject for the task"),
			"description":  strProp("New description for the task"),
			"activeForm":   strProp("Present continuous form shown in spinner when in_progress"),
			"owner":        strProp("New owner for the task"),
			"metadata":     recordProp("Metadata keys to merge into the task. Set a key to null to delete it."),
			"addBlocks":    stringList("Task IDs that this task blocks"),
			"addBlockedBy": stringList("Task IDs that block this task"),
		}, "taskId"),
		Execute: a.guarded(a.taskUpdate),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskOutput", Label: "TaskOutput", Description: taskOutputDescription,
		Parameters: objectSchema(map[string]any{
			"task_id": strProp("The task ID to get output from"),
			"block":   map[string]any{"type": "boolean", "description": "Whether to wait for completion", "default": true},
			"timeout": map[string]any{"type": "number", "description": "Max wait time in ms", "default": 30000, "minimum": 0, "maximum": 600000},
		}, "task_id", "block", "timeout"),
		Execute: a.guarded(a.taskOutput),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskStop", Label: "TaskStop", Description: taskStopDescription,
		Parameters: objectSchema(map[string]any{
			"task_id":  strProp("The ID of the background task to stop"),
			"shell_id": strProp("Deprecated: use task_id instead"),
		}),
		Execute: a.guarded(a.taskStop),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "TaskExecute", Label: "TaskExecute", Description: taskExecuteDescription, PromptGuidelines: taskExecuteGuidelines,
		Parameters: objectSchema(map[string]any{
			"task_ids":           stringList("Task IDs to execute as subagents"),
			"additional_context": strProp("Extra context for agent prompts"),
			"model":              strProp("Model override for agents"),
			"max_turns":          map[string]any{"type": "number", "description": "Max turns per agent", "minimum": 1},
		}, "task_ids"),
		Execute: a.guarded(a.taskExecute),
	})
}

// taskCreate: a finished list must not collect the batch that follows it, so a new batch retires it first.
// upstream: index.ts TaskCreate.
func (a *app) taskCreate(_ sdk.Context, p map[string]any) (string, error) {
	a.autoClear.startNewBatch()
	meta, _ := p["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if at, _ := strParam(p, "agentType"); at != "" {
		meta["agentType"] = at
	}
	subject, _ := strParam(p, "subject")
	description, _ := strParam(p, "description")
	activeForm, _ := strParam(p, "activeForm")
	var m map[string]any
	if len(meta) > 0 {
		m = meta
	}
	t := a.store.create(subject, description, activeForm, m)
	a.widget.update()
	return fmt.Sprintf("Task #%s created successfully: %s", t.ID, t.Subject), nil
}

// taskList lists pending first, then in-progress, then completed (each group by id). upstream: index.ts TaskList.
func (a *app) taskList(_ sdk.Context, _ map[string]any) (string, error) {
	tasks := a.store.list(nil)
	if len(tasks) == 0 {
		return "No tasks found", nil
	}
	order := map[string]int{statusPending: 0, statusInProgress: 1, statusCompleted: 2}
	sorted := slices.Clone(tasks)
	slices.SortStableFunc(sorted, func(x, y *task) int {
		if d := order[x.Status] - order[y.Status]; d != 0 {
			return d
		}
		a, b := jsNumber(x.ID), jsNumber(y.ID)
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	})
	lines := make([]string, 0, len(sorted))
	for _, t := range sorted {
		line := fmt.Sprintf("#%s [%s] %s", t.ID, t.Status, t.Subject)
		if t.Owner != "" {
			line += " (" + t.Owner + ")"
		}
		// Only non-completed blockers are shown.
		if open := a.openBlockers(t, false); len(open) > 0 {
			line += " [blocked by " + strings.Join(open, ", ") + "]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (a *app) taskGet(_ sdk.Context, p map[string]any) (string, error) {
	id, _ := strParam(p, "taskId")
	t := a.store.get(id)
	if t == nil {
		return "Task not found", nil
	}
	// Unescape literal \n sequences the LLM may have double-escaped in JSON.
	desc := strings.ReplaceAll(t.Description, `\n`, "\n")
	lines := []string{fmt.Sprintf("Task #%s: %s", t.ID, t.Subject), "Status: " + t.Status}
	if t.Owner != "" {
		lines = append(lines, "Owner: "+t.Owner)
	}
	lines = append(lines, "Description: "+desc)
	if len(t.BlockedBy) > 0 {
		if open := a.openBlockers(t, false); len(open) > 0 {
			lines = append(lines, "Blocked by: "+strings.Join(open, ", "))
		}
	}
	if len(t.Blocks) > 0 {
		ids := make([]string, len(t.Blocks))
		for i, b := range t.Blocks {
			ids[i] = "#" + b
		}
		lines = append(lines, "Blocks: "+strings.Join(ids, ", "))
	}
	if len(t.Metadata) > 0 {
		lines = append(lines, "Metadata: "+jsonString(t.Metadata))
	}
	return strings.Join(lines, "\n"), nil
}

func (a *app) taskUpdate(_ sdk.Context, p map[string]any) (string, error) {
	taskID, _ := strParam(p, "taskId")
	var f updateFields
	opt := func(key string) *string {
		if s, ok := strParam(p, key); ok {
			return &s
		}
		return nil
	}
	f.Status, f.Subject, f.Description, f.ActiveForm, f.Owner = opt("status"), opt("subject"), opt("description"), opt("activeForm"), opt("owner")
	if m, ok := p["metadata"].(map[string]any); ok {
		f.Metadata = m
	}
	f.AddBlocks, f.AddBlockedBy = listParam(p, "addBlocks"), listParam(p, "addBlockedBy")
	res := a.store.update(taskID, f)
	if len(res.ChangedFields) == 0 && res.Task == nil {
		return fmt.Sprintf("Task #%s not found", taskID), nil
	}
	// Active-task tracking for the widget.
	status := ""
	if f.Status != nil {
		status = *f.Status
	}
	switch status {
	case statusInProgress:
		a.widget.setActiveTask(taskID, true)
		a.autoClear.resetBatchCountdown()
	case statusPending:
		a.autoClear.resetBatchCountdown()
	case statusCompleted, statusDeleted:
		a.widget.setActiveTask(taskID, false)
		if status == statusCompleted {
			a.autoClear.trackCompletion(taskID, a.cadence.CurrentTurn)
		}
	}
	a.widget.update()
	msg := fmt.Sprintf("Updated task #%s %s", taskID, strings.Join(res.ChangedFields, ", "))
	if len(res.Warnings) > 0 {
		msg += " (warning: " + strings.Join(res.Warnings, "; ") + ")"
	}
	return msg, nil
}

// taskOutput retrieves the output of a task: a tracked process's, or a subagent task's stored result, waiting
// for the agent's lifecycle event when asked to block. upstream: index.ts TaskOutput.
func (a *app) taskOutput(ctx sdk.Context, p map[string]any) (string, error) {
	taskID, _ := strParam(p, "task_id")
	block := true
	if b, ok := p["block"].(bool); ok {
		block = b
	}
	timeout := 30000.0
	if v, ok := p["timeout"].(float64); ok {
		timeout = v
	}
	// Reject an empty id up front: every agent id starts with "", so the prefix match would resolve it to
	// whichever agent the map yields first.
	if taskID == "" {
		return "", errors.New("task_id is required")
	}
	if out := a.tracker.getOutput(taskID); out != nil {
		return a.processOutputText(ctx, taskID, out, block, timeout)
	}
	// No shell process: check for a subagent task; both task ids and agent ids (or prefixes) are accepted.
	resolved := taskID
	if a.store.get(resolved) == nil {
		if tid, ok := a.agents.resolve(taskID); ok {
			resolved = tid
		}
	}
	t := a.store.get(resolved)
	if t == nil {
		return "", fmt.Errorf("No task found with ID %s", taskID)
	}
	agentID, _ := t.Metadata["agentId"].(string)
	if agentID == "" {
		return "", fmt.Errorf("No background process for task %s", taskID)
	}
	// A subagent task: wait for completion if blocking.
	if block && t.Status == statusInProgress {
		a.waitForAgent(ctx, resolved, agentID, time.Duration(timeout*float64(time.Millisecond)))
	}
	// Re-read by resolved ID: the task predates the wait, and a file-backed store deserializes a fresh
	// object on every load.
	updated := a.store.get(resolved)
	if updated == nil {
		updated = t
	}
	// Consume only what is actually handed over: the agent has reported back (it left the map) and the task
	// carries its outcome. Short of both, the model is getting a status, and the notification pi-subagents is
	// holding is the only thing that will announce the result.
	if !a.agents.has(agentID) && updated.Status != statusInProgress {
		a.consumeSubagentResult(ctx, agentID)
	}
	output := ""
	switch r := updated.Metadata["result"].(type) {
	case string:
		output = r
	case nil:
		if le, ok := updated.Metadata["lastError"].(string); ok && le != "" {
			output = "Error: " + le
		}
	default:
		output = jsonString(r)
	}
	suffix := ""
	if output != "" {
		suffix = "\n\n" + output
	}
	return fmt.Sprintf("Task #%s [%s] — subagent %s%s", resolved, updated.Status, agentID, suffix), nil
}

// waitForAgent waits (without the app lock) for the agent's completion or failure event, the timeout, or the
// call's cancellation. upstream: index.ts TaskOutput (the blocking branch).
func (a *app) waitForAgent(ctx sdk.Context, resolvedID, agentID string, timeout time.Duration) {
	done := make(chan struct{}, 1)
	signal := func(_ sdk.Context, raw any) error {
		if strField(raw, "id") == agentID {
			select {
			case done <- struct{}{}:
			default:
			}
		}
		return nil
	}
	a.unlocked(func() {
		unsubOK, err1 := ctx.Events().On("subagents:completed", signal)
		unsubFail, err2 := ctx.Events().On("subagents:failed", signal)
		defer func() {
			if err1 == nil {
				unsubOK()
			}
			if err2 == nil {
				unsubFail()
			}
		}()
		if err1 != nil || err2 != nil {
			return
		}
		// Re-read before committing to the wait: this only differs on a shared file-backed list, where get()
		// reloads and another session may have finished the task.
		a.mu.Lock()
		current := a.store.get(resolvedID)
		a.mu.Unlock()
		if current != nil && current.Status != statusInProgress {
			return
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
		}
	})
}

func (a *app) processOutputText(ctx sdk.Context, taskID string, out *processOutput, block bool, timeout float64) (string, error) {
	format := func(o *processOutput) string {
		code := ""
		if o.ExitCode != nil {
			code = fmt.Sprintf(" exit code: %d", *o.ExitCode)
		}
		return fmt.Sprintf("Task #%s (%s)%s\n\n%s", taskID, o.Status, code, o.Output)
	}
	if block && out.Status == "running" {
		var res *processOutput
		stop := context.Background()
		a.unlocked(func() {
			c, cancel := context.WithCancel(stop)
			defer cancel()
			go func() { <-ctx.Done(); cancel() }()
			res = a.tracker.waitForCompletion(c, taskID, time.Duration(timeout*float64(time.Millisecond)))
		})
		if res != nil {
			return format(res), nil
		}
	}
	return format(out), nil
}

func (a *app) taskStop(ctx sdk.Context, p map[string]any) (string, error) {
	taskID, ok := strParam(p, "task_id")
	if !ok {
		taskID, ok = strParam(p, "shell_id")
	}
	if !ok || taskID == "" {
		return "", errors.New("task_id is required")
	}
	var stopped bool
	a.unlocked(func() { stopped = a.tracker.stop(taskID) })
	if !stopped {
		// No shell process: check for a subagent task, by task id or agent id (or a prefix of one).
		resolved := taskID
		if a.store.get(resolved) == nil {
			if tid, ok := a.agents.resolve(taskID); ok {
				resolved = tid
			}
		}
		t := a.store.get(resolved)
		if t != nil && t.Status == statusInProgress {
			if agentID, _ := t.Metadata["agentId"].(string); agentID != "" {
				a.store.update(resolved, updateFields{Status: strPtr(statusCompleted)})
				a.autoClear.trackCompletion(resolved, a.cadence.CurrentTurn)
				a.stopSubagent(ctx, agentID)
				a.widget.setActiveTask(resolved, false)
				a.widget.update()
				return fmt.Sprintf("Task #%s stopped successfully", resolved), nil
			}
		}
		return "", fmt.Errorf("No running background process for task %s", taskID)
	}
	a.store.update(taskID, updateFields{Status: strPtr(statusCompleted)})
	a.autoClear.trackCompletion(taskID, a.cadence.CurrentTurn)
	a.widget.setActiveTask(taskID, false)
	a.widget.update()
	return fmt.Sprintf("Task #%s stopped successfully", taskID), nil
}

// taskExecute launches tasks as background subagents through pi-subagents, and saves the cascade
// configuration for the completion listener. upstream: index.ts TaskExecute.
func (a *app) taskExecute(ctx sdk.Context, p map[string]any) (string, error) {
	if !a.subagentsAvailable {
		// The original pings once at load; ping again here, so a pi-subagents that appeared since is found.
		a.unlocked(func() { a.ping(ctx.Events()) })
	}
	if !a.subagentsAvailable {
		return "Subagent execution is currently unavailable (@tintinweb/pi-subagents not loaded " +
			"or version mismatch). You can run these as plain Agent-tool spawns, but pi-tasks " +
			"won't track them — status stays pending, cascade won't fire, TaskOutput stays empty.", nil
	}
	var results, launched []string
	additional, _ := strParam(p, "additional_context")
	model, _ := strParam(p, "model")
	maxTurns := p["max_turns"]
	for _, taskID := range listParam(p, "task_ids") {
		t := a.store.get(taskID)
		if t == nil {
			results = append(results, fmt.Sprintf("#%s: not found", taskID))
			continue
		}
		if t.Status != statusPending {
			results = append(results, fmt.Sprintf("#%s: not pending (status: %s)", taskID, t.Status))
			continue
		}
		agentType, _ := t.Metadata["agentType"].(string)
		if agentType == "" {
			results = append(results, fmt.Sprintf("#%s: no agentType set — create with agentType parameter or update metadata", taskID))
			continue
		}
		// Check all blockers are completed.
		if open := a.openBlockers(t, true); len(open) > 0 {
			results = append(results, fmt.Sprintf("#%s: blocked by %s", taskID, strings.Join(open, ", ")))
			continue
		}
		// Mark in_progress and spawn the agent through the bus.
		a.store.update(taskID, updateFields{Status: strPtr(statusInProgress)})
		prompt := a.buildTaskPrompt(t, additional)
		options := map[string]any{"description": t.Subject, "isBackground": true}
		if maxTurns != nil {
			options["maxTurns"] = maxTurns
		}
		if model != "" {
			options["model"] = model
		}
		agentID, err := a.spawnSubagent(ctx, agentType, prompt, options)
		if err != nil {
			debug("spawn:error task=#"+taskID, err)
			a.store.update(taskID, updateFields{Status: strPtr(statusPending)})
			results = append(results, fmt.Sprintf("#%s: spawn failed — %s", taskID, err.Error()))
			continue
		}
		a.agents.set(agentID, taskID)
		a.store.update(taskID, updateFields{Owner: strPtr(agentID), Metadata: withMeta(t.Metadata, "agentId", agentID)})
		a.widget.setActiveTask(taskID, true)
		launched = append(launched, fmt.Sprintf("#%s → agent %s", taskID, agentID))
	}
	// Save the cascade configuration for the completion listener.
	a.cascade = &cascadeConfig{additionalContext: additional, model: model, maxTurns: maxTurns}
	a.widget.update()
	var lines []string
	if len(launched) > 0 {
		lines = append(lines, fmt.Sprintf("Launched %d agent(s):\n%s\nUse TaskOutput to check progress. Do not spawn additional agents for these tasks.", len(launched), strings.Join(launched, "\n")))
	}
	if len(results) > 0 {
		lines = append(lines, "Skipped:\n"+strings.Join(results, "\n"))
	}
	if len(lines) == 0 {
		lines = append(lines, "No tasks to execute.")
	}
	return strings.Join(lines, "\n\n"), nil
}

func strPtr(s string) *string { return &s }
