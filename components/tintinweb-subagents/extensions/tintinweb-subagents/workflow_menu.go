package tintinweb_subagents

import (
	"fmt"
	"regexp"
	"sort"
)

// The data and the actions behind `/agents → Workflows` and the run inspector, without the drawing: what the menu
// offers and when, which run a pick opens, and what each dialog key does to a run and says about it. The dialog and the
// overlay themselves are the UI lane's (ui/workflow-menu.ts, ui/workflow-dialog.ts); they call these, so the keys cannot
// drift from the runtime. The inspector lives in the agents menu rather than as a top-level /workflows command: pi
// renames a duplicate command to /workflows:1 and /workflows:2, which breaks the bare name for both extensions.
// upstream: src/ui/workflow-menu.ts, and the `Workflows (N)` entry of index.ts showAgentsMenu.

var workflowsEntryPattern = regexp.MustCompile(`^Workflows \(\d+\)$`)

// workflowsMenuEntry is the agents-menu entry that opens the inspector, shown only when the feature is on so the menu
// never advertises a switched-off feature; "" when there is no entry. upstream: `if (isWorkflowsEnabled()) options.push(...)`.
func (a *app) workflowsMenuEntry() string {
	w := a.workflows()
	if !w.isEnabled() {
		return ""
	}
	return fmt.Sprintf("Workflows (%d)", len(w.tasks.keys()))
}

// workflowsMenuPlan is what showWorkflowsMenu does with a session's runs: a notice when there are none, the dialog of
// the only run, or a picker (newest first) over several. upstream: showWorkflowsMenu.
type workflowsMenuPlan struct {
	// Notice is said instead of opening anything.
	Notice string
	// Open is the run whose dialog opens at once (exactly one run).
	Open *workflowTask
	// Labels are the picker's options, newest first, each unique (the run id ends it); Tasks[i] is what Labels[i] opens.
	Labels []string
	Tasks  []*workflowTask
}

func planWorkflowsMenu(tasks []*workflowTask) workflowsMenuPlan {
	sorted := append([]*workflowTask(nil), tasks...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartTime > sorted[j].StartTime })
	switch len(sorted) {
	case 0:
		return workflowsMenuPlan{Notice: "No workflows in this session."}
	case 1:
		return workflowsMenuPlan{Open: sorted[0]}
	}
	// select deals in plain strings and hands back the string, so the label has to be unique or the second run of a
	// workflow maps onto the first: the run id makes it so.
	plan := workflowsMenuPlan{Tasks: sorted}
	for _, t := range sorted {
		t.mu.Lock()
		name := t.ID
		if t.Meta != nil {
			name = t.Meta.Name
		}
		plural := "s"
		if t.AgentCount == 1 {
			plural = ""
		}
		plan.Labels = append(plan.Labels, fmt.Sprintf("%s — %s, %d agent%s · %s", name, t.Status, t.AgentCount, plural, t.ID))
		t.mu.Unlock()
	}
	return plan
}

// Pick is the run a picked label opens, nil for a cancelled picker.
func (p workflowsMenuPlan) Pick(label string, picked bool) *workflowTask {
	if !picked {
		return nil
	}
	for i, l := range p.Labels {
		if l == label {
			return p.Tasks[i]
		}
	}
	return nil
}

// workflowDialogNotify is the notification a dialog action raises.
type workflowDialogNotify func(message, level string)

// stopWorkflowFromDialog aborts the run (not merely looks like it did) and says so once: a run that is already aborted
// announces nothing. upstream: onKill.
func stopWorkflowFromDialog(task *workflowTask, notify workflowDialogNotify) {
	if task.Ctx.Err() != nil {
		return
	}
	task.Abort()
	task.mu.Lock()
	name := task.ID
	if task.Meta != nil {
		name = task.Meta.Name
	}
	task.mu.Unlock()
	notify(`Stopped workflow "`+name+`".`, "info")
}

// pauseWorkflowFromDialog pauses the run. The notice says what it does rather than implying more: "paused" on a run
// whose agents are still finishing promises more than it delivers. upstream: onPause.
func pauseWorkflowFromDialog(task *workflowTask, notify workflowDialogNotify) {
	if pauseWorkflowTask(task, wfNowMs()) {
		notify("Paused — running agents finish, no new ones start.", "info")
	}
}

// resumeWorkflowFromDialog lets a paused run go again. upstream: onResume.
func resumeWorkflowFromDialog(task *workflowTask, notify workflowDialogNotify) {
	if resumeWorkflowTask(task, wfNowMs()) {
		notify("Resumed.", "info")
	}
}

func workflowControlOf(task *workflowTask) workflowControl {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.Control
}

// skipAgentFromDialog skips the agent at index. upstream: onSkipAgent.
func skipAgentFromDialog(task *workflowTask, index int, notify workflowDialogNotify) {
	if c := workflowControlOf(task); c == nil || !c.Skip(index) {
		notify("Nothing to skip — that agent has already finished.", "info")
	}
}

// retryAgentFromDialog starts the agent at index over; the window is exactly "while it is running". upstream: onRetryAgent.
func retryAgentFromDialog(task *workflowTask, index int, notify workflowDialogNotify) {
	if c := workflowControlOf(task); c == nil || !c.Retry(index) {
		notify("Only a running agent can be retried.", "info")
	}
}

// noConversationNotice is what opening an agent row says once its record has been swept. upstream: onOpenAgent.
const noConversationNotice = "No conversation left — agent records are dropped ten minutes after they finish."
