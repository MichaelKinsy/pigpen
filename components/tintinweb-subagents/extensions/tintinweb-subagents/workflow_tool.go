package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SubagentWorkflow tool, the --subagents-workflow-file flag and the workflowsEnabled master switch: the seams that
// bind the workflow engine to the rest of the extension. upstream: src/index.ts (the workflow tool, runWorkflowTask,
// notifyWorkflowFinished, resolveWorkflowCollisions, runWorkflowFlag).

// workflowFileFlag is the CLI flag that runs a workflow script at session start. upstream: WORKFLOW_FILE_FLAG.
const workflowFileFlag = "subagents-workflow-file"

// workflowFeature is the app's workflow state: the master switch, the runs of this session, and what has been handled once.
type workflowFeature struct {
	mu sync.Mutex
	// enabled is the master switch (default on). Off means the SubagentWorkflow tool is never registered: the model is
	// not told the feature exists. The menu entry and the flag are refused too, so there is no second door into the
	// same machinery.
	enabled bool
	// pinned records that the answer came from the user (a boolean in subagents.json, or the settings toggle) rather
	// than from this default: resolveWorkflowCollisions yields to another extension's tool only for a default.
	pinned            bool
	tasks             *workflowTaskStore
	ownDescription    string
	collisionsChecked bool
	flagHandled       bool
}

func (a *app) workflows() *workflowFeature {
	a.wfOnce.Do(func() { a.wf = &workflowFeature{enabled: true, tasks: newWorkflowTaskStore()} })
	return a.wf
}

func (w *workflowFeature) isEnabled() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.enabled }
func (w *workflowFeature) isPinned() bool  { w.mu.Lock(); defer w.mu.Unlock(); return w.pinned }

// setEnabled is the user's choice (the setting or the toggle): it pins the answer.
func (w *workflowFeature) setEnabled(b bool) {
	w.mu.Lock()
	w.enabled, w.pinned = b, true
	w.mu.Unlock()
}

// loadWorkflowSettings reads workflowsEnabled from subagents.json, the global file then the project's (the project's
// wins): a boolean pins the answer; any other value is ignored, as in the original. upstream: settings.ts sanitize.
func loadWorkflowSettings(cwd string) (enabled, pinned bool) {
	enabled = true
	for _, path := range []string{filepath.Join(agentDir(), "subagents.json"), filepath.Join(cwd, ".pi", "subagents.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if b, ok := raw["workflowsEnabled"].(bool); ok {
			enabled, pinned = b, true
		}
	}
	return enabled, pinned
}

var workflowToolParameters = sdk.Schema{
	"type": "object",
	"properties": map[string]any{
		"script": map[string]any{"type": "string", "maxLength": 524288,
			"description": "Inline workflow source. Must begin with `export const meta = { name, description }`."},
		"scriptPath": map[string]any{"type": "string",
			"description": "Path to a workflow script file, absolute or relative to the project. Takes precedence over `script` — this is how you re-run an edited workflow."},
		"name": map[string]any{"type": "string",
			"description": "Name of a saved workflow — `<name>.js` in .pi/workflows/, .agents/workflows/ or the user's agent dir. Lowest precedence: `scriptPath` and `script` both win over it."},
		"args": map[string]any{"description": "Exposed to the script as the global `args`, verbatim. Must be JSON-shaped."},
		"resumeFromRunId": map[string]any{"type": "string", "pattern": "^wf_[a-z0-9-]{6,}$",
			"description": "Run id of an earlier workflow in this session. Its unchanged leading agent() calls return their recorded results instantly; the first changed or failed call, and everything after it, runs live. Same script and args means nothing re-runs."},
		// Accepted and ignored, as in Claude Code. Models reach for them because the Workflow tool has them; rejecting
		// costs a whole turn to re-emit a script that was already correct. The meta block is the one place a workflow is named.
		"title":       map[string]any{"type": "string", "description": "Ignored — set the workflow title in the script's `meta` block."},
		"description": map[string]any{"type": "string", "description": "Ignored — set the workflow description in the script's `meta` block."},
	},
}

var workflowPromptGuidelines = []string{
	"Use SubagentWorkflow when the number of agents depends on something discovered at runtime, when work flows through stages, or when findings should be independently verified. Use Agent for one delegated task or a handful you can name up front.",
	"Prefer `pipeline` over `parallel` — a barrier costs wall-clock whenever the stages are unevenly sized.",
	"A workflow runs in the background and notifies you when it finishes — do not poll or sleep waiting for it.",
}

// registerWorkflow registers the SubagentWorkflow tool and its flag. The tool is defined unconditionally and registered
// only when the feature is on, so the switch changes exactly one thing: whether the model is ever told about it.
// upstream: index.ts workflowTool, `if (isWorkflowsEnabled()) pi.registerTool(workflowTool)`, registerFlag.
func (a *app) registerWorkflow(e *sdk.Extension) {
	w := a.workflows()
	if wd, err := os.Getwd(); err == nil {
		enabled, pinned := loadWorkflowSettings(wd)
		w.mu.Lock()
		w.enabled, w.pinned = enabled, pinned
		w.mu.Unlock()
	}
	// Registered at activation; READ from session_start: the host applies CLI values after every extension factory has run.
	e.Flag(workflowFileFlag, sdk.FlagOptions{Type: sdk.FlagString,
		Description: "Run a workflow script at startup: --" + workflowFileFlag + "=<path>. " +
			"Use the `=` form — the space form consumes the next argument, which would swallow a following prompt."})
	description := renderWorkflowToolDescription(currentRegistry())
	w.ownDescription = description
	if !w.isEnabled() {
		return
	}
	e.RegisterTool(sdk.ToolDefinition{
		Name: workflowToolName, Label: "SubagentWorkflow", Description: description,
		PromptSnippet:    "Run a deterministic script that orchestrates many subagents",
		PromptGuidelines: workflowPromptGuidelines,
		Parameters:       workflowToolParameters,
		Execute:          a.workflowTool,
	})
}

func paramString(p map[string]any, key string) string { s, _ := p[key].(string); return s }

// workflowTool is the tool's execute. upstream: workflowTool.execute.
func (a *app) workflowTool(ctx sdk.Context, p map[string]any) (any, error) {
	a.touch(ctx)
	w := a.workflows()
	resumeFromRunID := paramString(p, "resumeFromRunId")
	resumeFrom := resolveResumeTarget(resumeFromRunID, w.tasks)
	if resumeFrom != nil && !resumeFrom.OK {
		return textResult(resumeFrom.Message), nil
	}
	_, hasScript := p["script"]
	_, hasPath := p["scriptPath"]
	_, hasName := p["name"]
	params := workflowScriptParams{Script: paramString(p, "script"), ScriptPath: paramString(p, "scriptPath"), Name: paramString(p, "name")}
	// A resume with no source of its own re-runs what that run ran: "run that again, cheaply" should not require
	// repeating a path the run already knows.
	if !hasScript && !hasPath && !hasName && resumeFrom != nil {
		params = workflowScriptParams{ScriptPath: resumeFrom.ScriptPath}
	}
	resolved := resolveWorkflowScript(params, ctx.Cwd())
	if !resolved.OK {
		return textResult(resolved.Message), nil
	}
	// Parsed before anything is scheduled: a bad meta is an authoring error the model can fix immediately.
	meta, _, err := extractMeta(resolved.Script)
	if err != nil {
		return textResult(err.Error()), nil
	}

	runID := workflowRunID()
	// Every invocation lands on disk next to the agent transcripts, so iterating is edit-the-file-then-rerun with
	// scriptPath. The journal sits beside it under the same id, which is what makes a run id enough to resume from.
	var savedPath, journalPath string
	sessionID, _ := ctx.GetSessionID()
	if dir, err := workflowSessionTaskDir(ctx.Cwd(), sessionID); err == nil {
		path := filepath.Join(dir, runID+".workflow.js")
		if os.WriteFile(path, []byte(resolved.Script), 0o666) == nil {
			savedPath, journalPath = path, filepath.Join(dir, runID+".workflow.jsonl")
		}
	}

	init := workflowTaskInit{ID: runID, Script: resolved.Script, ScriptPath: resolved.ScriptPath, Meta: &meta, ToolCallID: ctx.ToolCallID(),
		JournalPath: journalPath}
	if init.ScriptPath == "" {
		init.ScriptPath = savedPath
	}
	init.Args, init.HasArgs = p["args"], hasKey(p, "args")
	if resumeFrom != nil {
		if replay := readWorkflowJournal(resumeFrom.JournalPath); len(replay) > 0 {
			init.Replay, init.ResumedFrom = replay, resumeFrom.RunID
		}
	}
	task := createWorkflowTask(init)
	w.tasks.set(task)

	// Background, like Claude Code: the id comes back now and the run keeps going without the tool call.
	go func() {
		a.runWorkflowTask(ctx, task)
		a.notifyWorkflowFinished(task)
	}()

	text := `Workflow "` + meta.Name + `" started in the background.` + "\nTask ID: " + runID + "\n"
	if task.ScriptPath != "" {
		text += "Script: " + task.ScriptPath + "\n"
	}
	switch {
	case task.ResumedFrom != "":
		text += "Resuming " + task.ResumedFrom + ": " + itoa(len(task.Replay)) + " recorded call(s) available to replay.\n"
	case resumeFromRunID != "":
		text += "Nothing to replay from " + resumeFromRunID + " — every agent runs live.\n"
	}
	text += "\nYou will be notified when it finishes — do NOT poll or sleep waiting for it.\n" +
		"To iterate, edit the script file and call SubagentWorkflow again with scriptPath."
	return sdk.ToolResult{Content: text, Details: map[string]any{"taskId": runID}}, nil
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func itoa(n int) string { return strconv.Itoa(n) }

// runWorkflowTask runs a task to completion and settles it; it never fails: a run that cannot start (bad meta,
// oversized source, non-JSON args) is a failed workflow, and both callers are detached. upstream: runWorkflowTask.
func (a *app) runWorkflowTask(toolCtx sdk.Context, task *workflowTask) {
	// The session's latest context, read at the time of use: the tool call's own context belongs to a call that has returned.
	live := func() sdk.Context {
		if c, ok := a.context(); ok {
			return c
		}
		return toolCtx
	}
	sessionID, _ := toolCtx.GetSessionID()
	host := newWorkflowHost(workflowHostDeps{
		Manager: managerWorkflowAdapter{a: a, ctx: toolCtx},
		Exec: func(ctx context.Context, command string, args []string, cwd string, timeout time.Duration) (workflowExecResult, error) {
			return wfExec(live())(ctx, command, args, cwd, timeout)
		},
		Cwd:      toolCtx.Cwd(),
		Notify:   func(message, level string) { live().Notify(message, level) },
		Registry: sdkModelRegistry{r: toolCtx.ModelRegistry()},
		ParentModel: func() *workflowModel {
			q := live().ModelQualified()
			if i := strings.Index(q, "/"); i > 0 {
				return &workflowModel{Provider: q[:i], ID: q[i+1:]}
			}
			return nil
		},
		Signal: task.Ctx, RootSessionID: sessionID, WorkflowID: task.ID,
	})
	opts := workflowRunOptions{Script: task.Script, Args: task.Args, HasArgs: task.HasArgs, Host: host, Ctx: task.Ctx,
		OnProgress: func(es []workflowEntry) { updateWorkflowProgressBatch(task, es) },
		// The dialog's pause / skip / retry keys run through this; it is dropped again when the task settles.
		OnControl: func(c workflowControl) { task.mu.Lock(); task.Control = c; task.mu.Unlock() }}
	opts.Journal = &workflowJournalOptions{Entries: task.Replay}
	if task.JournalPath != "" {
		path := task.JournalPath
		opts.Journal.Append = func(e workflowJournalEntry) { appendWorkflowJournal(path, e) }
	}
	res, err := runWorkflow(opts)
	if err != nil {
		failWorkflowTask(task, err.Error())
		return
	}
	completeWorkflowTask(task, res)
}

// notifyWorkflowFinished hands a finished run back to the model through the SAME channel a background agent uses: held
// briefly by scheduleNudge, delivered as a follow-up that triggers a turn. upstream: notifyWorkflowFinished.
func (a *app) notifyWorkflowFinished(task *workflowTask) {
	result := workflowResultText(task)
	task.mu.Lock()
	status := "error"
	switch task.Status {
	case runCompleted:
		status = "completed"
	case runKilled:
		status = "stopped"
	}
	name := task.WorkflowName
	if name == "" {
		name = task.ID
	}
	details := map[string]any{"id": task.ID, "description": "Workflow " + name, "status": status, "toolUses": task.TotalToolCalls,
		// A workflow has agents, not turns; rendering "↻0" would be noise.
		"turnCount": 0, "totalTokens": task.TotalTokens,
		"durationMs":    workflowElapsedMs(workflowClock{task.StartTime, task.EndTime, task.TotalPausedMs}, wfNowMs()),
		"resultPreview": result}
	if wfUTF16Len(result) > 500 {
		details["resultPreview"] = wfUTF16Prefix(result, 500) + "…"
	}
	if task.HasError {
		details["error"] = task.Error
	}
	task.mu.Unlock()
	a.scheduleNudge(task.ID, func() {
		ctx, ok := a.context()
		if !ok {
			return
		}
		turn := true
		_ = ctx.SendCustomMessage(sdk.CustomMessage{CustomType: "subagent-notification", Content: formatWorkflowNotification(task, wfNowMs()), Display: true, Details: details},
			sdk.SendMessageOptions{DeliverAs: "followUp", TriggerTurn: &turn})
	})
}

// workflowSessionStart is what session_start does for workflows: the collision check first (so the flag's door closes
// with the tool), then the flag. upstream: index.ts session_start `resolveWorkflowCollisions(ctx); runWorkflowFlag(ctx)`.
func (a *app) workflowSessionStart(ctx sdk.Context) {
	a.resolveWorkflowCollisions(ctx)
	a.runWorkflowFlag(ctx)
}

// resolveWorkflowCollisions acts on decideWorkflowCollision: read the registry, warn, and take our tool out of the
// active set. It can only happen at session_start: the collision is visible only after every extension has loaded,
// which is after ours registered, so the decision cannot gate the registration; it has to undo it. Best-effort: a
// diagnostic that took the session down would be worse than the collision it reports. upstream: resolveWorkflowCollisions.
func (a *app) resolveWorkflowCollisions(ctx sdk.Context) {
	w := a.workflows()
	w.mu.Lock()
	if w.collisionsChecked {
		w.mu.Unlock()
		return
	}
	w.collisionsChecked = true
	w.mu.Unlock()
	if !w.isEnabled() {
		return
	}
	tools, err := ctx.GetAllTools()
	if err != nil {
		return // GetAllTools is unavailable in some hosts (print mode, RPC)
	}
	infos := make([]registeredToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = registeredToolInfo{Name: t.Name, Description: t.Description, Source: t.SourceInfo.Source}
	}
	verdict := decideWorkflowCollision(infos, w.ownDescription, w.isPinned())
	switch verdict.Kind {
	case "none":
		return
	case "report":
		a.workflowWarn(ctx, verdict.Message)
		return
	}
	w.mu.Lock()
	w.enabled = false // not setEnabled: this is not the user pinning it
	w.mu.Unlock()
	a.workflowWarn(ctx, verdict.Message)
	if !verdict.Withdraw {
		return
	}
	active, err := ctx.GetActiveTools()
	if err != nil {
		return
	}
	for _, name := range active {
		if name == workflowToolName {
			rest := make([]string, 0, len(active))
			for _, n := range active {
				if n != workflowToolName {
					rest = append(rest, n)
				}
			}
			ctx.SetActiveTools(rest)
			return
		}
	}
}

// workflowWarn raises a warning: a notification with a UI, stderr without. upstream: the `warn`/`report` closures.
func (a *app) workflowWarn(ctx sdk.Context, message string) {
	if ctx.HasUI() {
		ctx.Notify(message, "warning")
		return
	}
	os.Stderr.WriteString("[pi-subagents] " + message + "\n")
}

// runWorkflowFlag is --subagents-workflow-file=<path>: run a script at startup, with no LLM round-trip deciding
// whether to call the tool. Read here rather than at activation because that is the only place the real value
// exists. upstream: runWorkflowFlag.
func (a *app) runWorkflowFlag(ctx sdk.Context) {
	w := a.workflows()
	w.mu.Lock()
	if w.flagHandled {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	flag, err := ctx.GetFlag(workflowFileFlag)
	if err != nil || flag == nil || flag == false {
		return
	}
	w.mu.Lock()
	w.flagHandled = true
	w.mu.Unlock()
	report := func(message, level string) {
		if ctx.HasUI() {
			ctx.Notify(message, level)
			return
		}
		os.Stderr.WriteString("[pi-subagents] " + message + "\n")
	}
	// The flag is the same machinery by another door, so the master switch has to close it too.
	if !w.isEnabled() {
		report("--"+workflowFileFlag+" ignored: workflows are off. Turn them on in /agents → Settings → Workflows, "+
			"or set `\"workflowsEnabled\": true` in .pi/subagents.json.", "warning")
		return
	}
	// A bare --subagents-workflow-file parses to boolean true: say what was missing rather than reading a file called "true".
	path, isString := flag.(string)
	if !isString || strings.TrimSpace(path) == "" {
		report("--"+workflowFileFlag+" needs a path: --"+workflowFileFlag+"=<path>", "warning")
		return
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.Cwd(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		report("Could not read "+path+": "+nodeFSError(err), "warning")
		return
	}
	script := string(data)
	meta, _, err := extractMeta(script)
	if err != nil {
		report(err.Error(), "warning")
		return
	}
	task := createWorkflowTask(workflowTaskInit{ID: workflowRunID(), Script: script, ScriptPath: path, Meta: &meta})
	w.tasks.set(task)
	report("Running workflow "+meta.Name+"…", "info")
	// Detached: session_start is awaited by the host, and a workflow can run for minutes.
	go func() {
		a.runWorkflowTask(ctx, task)
		// No tool call to attach a result card to, so the card becomes a session entry (same layout), and the outcome is
		// handed to the model as context for its next turn rather than forcing one.
		live := ctx
		if c, ok := a.context(); ok {
			live = c
		}
		_ = live.AppendEntry(workflowEntryType, snapshotWorkflowEntry(task))
		_ = live.SendCustomMessage(sdk.CustomMessage{CustomType: "workflow-result", Content: formatWorkflowNotification(task, wfNowMs()), Display: false},
			sdk.SendMessageOptions{DeliverAs: "nextTurn"})
	}()
}

// shutdownWorkflows aborts every run of the session: before abortAll, and not folded into it, because a workflow owns a
// worker as well as its children, and only its own context terminates that. upstream: session_shutdown.
func (a *app) shutdownWorkflows() {
	w := a.workflows()
	for _, task := range w.tasks.values() {
		task.Abort()
	}
	w.tasks.clear()
}

// settingValue is what the settings write-back stores for workflowsEnabled: the user's answer, not the effective one.
// A stand-down for another extension's workflow tool is scoped to the session it was detected in; writing it would let
// an unrelated settings change freeze it into an explicit false, which then survives uninstalling the extension it was
// deferring to. write is false for a default (undefined is dropped from the file). upstream: index.ts settings
// snapshot `workflowsEnabled: isWorkflowsPinned() ? isWorkflowsEnabled() : undefined`.
func (w *workflowFeature) settingValue() (value, write bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled, w.pinned
}
