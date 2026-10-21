package tintinweb_subagents

import (

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `/agents → Workflows` and the run inspector behind it: the submenu and the overlay it opens, on top of the data and
// actions of workflow_menu.go. Everything they need arrives as workflowMenuDeps rather than through a closure. The
// inspector is reached from two places, this menu and a `workflow` row in the fleet list, and both go through
// showWorkflowDialog, so the two entry points cannot drift apart on what the keys do. upstream: src/ui/workflow-menu.ts.

// workflowMenuDeps is everything the menu and the inspector need from the extension around them.
type workflowMenuDeps struct {
	// tasks are the live runs by id, read on every use rather than snapshotted: a run that settled and was swept
	// between render and keypress must be a no-op, not a crash.
	tasks *workflowTaskStore
	// getRecord is the record behind an agent id, nil once it has been swept.
	getRecord func(id string) *agentView
	// viewAgentConversation is the conversation overlay `c` opens on an agent row; it returns when the overlay has closed.
	viewAgentConversation func(ui menuUI, record *agentView)
	// getUI is the session's UI, for the fleet-list entry point (a keypress in a list that holds no ctx of its own);
	// nil between sessions, which is a no-op rather than an error.
	getUI func() menuUI
}

// snapshotWorkflowDialogSource is what the dialog reads of a run, re-read on every render: the run is in the
// background, so the dialog has to follow it rather than snapshot it at open time. The progress slice is append-only,
// so the copied header stays valid after the lock is released.
func snapshotWorkflowDialogSource(task *workflowTask) workflowDialogSource {
	task.mu.Lock()
	defer task.mu.Unlock()
	return workflowDialogSource{
		Progress: task.WorkflowProgress[:len(task.WorkflowProgress):len(task.WorkflowProgress)],
		Task: workflowCardTask{Status: task.Status, WorkflowName: task.WorkflowName, StartTime: task.StartTime, EndTime: task.EndTime,
			TotalPausedMs: task.TotalPausedMs},
		Meta: task.Meta, AgentCount: task.AgentCount, Version: task.ProgressVersion + 1,
	}
}

// showWorkflowDialog opens the inspector for a workflow run and returns when it has closed. All six controls are wired:
// onKill aborts the run's context, pause/resume and per-agent skip/retry go through the task's control, and onOpenAgent
// opens the child's conversation rather than changing the run. The dialog derives its key hints from the actions it is
// handed, so the footer advertises exactly what works.
func showWorkflowDialog(ui menuUI, task *workflowTask, deps workflowMenuDeps) {
	// This dialog's own overlay, so `c` can hide it while the conversation is up. Overlays stack, so the viewer would
	// render over it either way, but the two frames size themselves to different content and the taller one's edges
	// show around the shorter. Hidden, there is nothing to peek out, and un-hiding puts the focus back on the dialog.
	var overlay menuOverlayHandle
	setOverlay := func(h menuOverlayHandle) { overlay = h }
	hide := func(hidden bool) {
		if overlay != nil {
			guard(func() { _ = overlay.SetHidden(hidden) })
		}
	}
	notify := func(message, level string) { ui.Notify(message, level) }
	actions := workflowDialogActions{
		OnKill:       func() { stopWorkflowFromDialog(task, notify) },
		OnPause:      func() { pauseWorkflowFromDialog(task, notify) },
		OnResume:     func() { resumeWorkflowFromDialog(task, notify) },
		OnSkipAgent:  func(index int) { skipAgentFromDialog(task, index, notify) },
		OnRetryAgent: func(index int) { retryAgentFromDialog(task, index, notify) },
		OnOpenAgent: func(recordID string) {
			record := deps.getRecord(recordID)
			// A run's children are records like any other, so they can be swept: the row outlives the conversation it
			// points at, and saying why beats an overlay that opens empty.
			if record == nil {
				ui.Notify(noConversationNotice, "info")
				return
			}
			hide(true)
			// Off the dialog's input path: the viewer's overlay blocks until it closes. The un-hide runs whatever
			// happens, so a viewer that fails to open still brings the dialog back.
			safeGo("workflow conversation", func() {
				defer hide(false)
				deps.viewAgentConversation(ui, record)
			})
		},
	}
	ui.Custom(func(host viewerHost, th uiTheme, done func(any)) viewerComponent {
		return newWorkflowDialog(host, func() workflowDialogSource { return snapshotWorkflowDialogSource(task) }, th,
			func() { done(nil) }, actions, 0)
	}, &menuOverlay{overlayOptions: overlayOptions{Anchor: "center", WidthPercent: 90, HeightPercent: viewportHeightPct}, OnHandle: setOverlay})
}

// openWorkflowFromFleet opens a run from the fleet list. The list hands back an id rather than a task, so a run that
// settled and was swept between render and keypress is a no-op instead of a crash. Esc in the dialog closes it and
// control returns to the list, which puts the cursor back on the run.
func openWorkflowFromFleet(id string, deps workflowMenuDeps) {
	task := deps.tasks.get(id)
	var ui menuUI
	if deps.getUI != nil {
		ui = deps.getUI()
	}
	if task == nil || ui == nil {
		return
	}
	showWorkflowDialog(ui, task, deps)
}

// showWorkflowsMenu is `/agents → Workflows`: list this session's runs, open one.
func showWorkflowsMenu(ui menuUI, deps workflowMenuDeps) {
	plan := planWorkflowsMenu(deps.tasks.values())
	switch {
	case plan.Notice != "":
		ui.Notify(plan.Notice, "info")
	case plan.Open != nil:
		showWorkflowDialog(ui, plan.Open, deps)
	default:
		picked, ok, err := ui.Select("Workflows", plan.Labels)
		if task := plan.Pick(picked, ok && err == nil); task != nil {
			showWorkflowDialog(ui, task, deps)
		}
	}
}

// fleetWorkflows is the workflow runs as the fleet list wants them. Cached counters only, no derivation: the list calls
// this on a 200ms tick and reads the roster several times per update, so walking a run's progress log here would put
// O(log) work in the render loop. upstream: index.ts fleetWorkflows.
func (a *app) fleetWorkflows() []fleetWorkflow {
	tasks := a.workflows().tasks.values()
	out := make([]fleetWorkflow, 0, len(tasks))
	for _, task := range tasks {
		task.mu.Lock()
		name := task.ID
		switch {
		case task.Meta != nil:
			name = task.Meta.Name
		case task.WorkflowName != "":
			name = task.WorkflowName
		}
		w := fleetWorkflow{ID: task.ID, Name: name, Status: task.Status, DoneCount: task.DoneCount, TotalCount: task.AgentCount,
			StartedAt: task.StartTime, Tokens: task.TotalTokens}
		if task.EndTime != nil {
			w.CompletedAt = *task.EndTime
		}
		task.mu.Unlock()
		out = append(out, w)
	}
	return out
}

// workflowMenuDeps is the one object `/agents → Workflows` and the fleet list's workflow rows both open the inspector
// through, built once.
func (a *app) workflowMenuDeps() workflowMenuDeps {
	return workflowMenuDeps{
		tasks: a.workflows().tasks,
		getRecord: func(id string) *agentView {
			for _, v := range a.ui.listAgents() {
				if v.ID == id {
					return v
				}
			}
			return nil
		},
		viewAgentConversation: a.viewAgentConversation,
		// Read lazily: the context is rebound on every session event, and the fleet list may act between sessions,
		// when there is none.
		getUI: func() menuUI {
			if ctx, ok := a.context(); ok {
				return a.menuUIFor(ctx)
			}
			return nil
		},
	}
}

// wireWorkflowFleet hands the fleet list the runs and the way to open one. upstream: index.ts fleet.setWorkflowSource.
func (a *app) wireWorkflowFleet() {
	a.ui.fleet.setWorkflowSource(a.fleetWorkflows, func(id string, onClose func()) {
		// The list calls this from a key handler: the dialog blocks until closed, so it runs off the input path.
		safeGo("workflow dialog", func() {
			defer safeCall("workflow dialog closed", onClose)
			openWorkflowFromFleet(id, a.workflowMenuDeps())
		})
	})
}

// workflowCallLine is the SubagentWorkflow call line. upstream: index.ts renderCall of the SubagentWorkflow tool.
func workflowCallLine(args map[string]any, th uiTheme) string {
	script, _ := args["script"].(string)
	scriptPath, _ := args["scriptPath"].(string)
	name, _ := args["name"].(string)
	return th.Fg("toolTitle", "▸ ") + th.Bold(th.Fg("toolTitle", "SubagentWorkflow")) + "  " + th.Fg("muted", workflowCallName(script, scriptPath, name))
}

// renderWorkflowCall draws the call line.
func (a *app) renderWorkflowCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return protectLines("SubagentWorkflow call renderer", func() ([]string, error) {
		return renderText(workflowCallLine(args, sdkTheme{ctx.UITheme()}), width), nil
	})
}

// renderWorkflowResult draws the live card of the run the result names. The tool returns before the run finishes, so
// the card looks the task up on every render rather than freezing a snapshot into the result: that is what makes the
// inline card follow a background run. No task means the run predates this session (a reloaded transcript) or the call
// never started one: what `execute` said is shown instead.
func (a *app) renderWorkflowResult(ctx sdk.Context, result sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
	return protectLines("SubagentWorkflow result renderer", func() ([]string, error) {
		text := toolResultText(result)
		var task *workflowTask
		if d, ok := result.Details.(map[string]any); ok {
			if id, ok := d["taskId"].(string); ok {
				task = a.workflows().tasks.get(id)
			}
		}
		if render.IsError || task == nil {
			return renderText(text, width), nil
		}
		task.mu.Lock()
		in := workflowCardInput{
			Progress: task.WorkflowProgress[:len(task.WorkflowProgress):len(task.WorkflowProgress)],
			Task: workflowCardTask{Status: task.Status, WorkflowName: task.WorkflowName, StartTime: task.StartTime, EndTime: task.EndTime,
				TotalPausedMs: task.TotalPausedMs},
			Meta: task.Meta, AgentCount: task.AgentCount,
		}
		tokens := task.TotalTokens
		in.TotalTokens = &tokens
		task.mu.Unlock()
		return renderWorkflowCard(in, sdkTheme{ctx.UITheme()}, width), nil
	})
}

// renderWorkflowEntry draws the session entry a flag-launched run leaves behind, through the same layout the tool
// result uses. Custom entries with no registered renderer are silently dropped by the host, which is why this is
// registered at activation. upstream: index.ts registerEntryRenderer(WORKFLOW_ENTRY_TYPE).
func (a *app) renderWorkflowEntry(ctx sdk.Context, entry map[string]any, _ sdk.EntryRenderOptions, width int) ([]string, error) {
	return protectLines("workflow entry renderer", func() ([]string, error) {
		data := decodeDetails[workflowEntryData](entry["data"])
		if data == nil {
			return nil, nil
		}
		return renderWorkflowEntryCard(data, sdkTheme{ctx.UITheme()}, width), nil
	})
}

