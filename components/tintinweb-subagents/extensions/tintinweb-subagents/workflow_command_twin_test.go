package tintinweb_subagents

import (
	"strings"
	"testing"
	"time"
)

// Twins of test/workflow-command.test.ts (`/agents → Workflows`) and test/e2e/workflow.e2e.test.ts. The menu and the
// dialog are drawn by the UI lane; what is twinned here is the data layer they call (workflow_menu.go), through the
// real extension over the fake host. Frames, overlays and key handling are the UI lane's twins.

func TestWorkflowCommand(t *testing.T) {
	const f = "workflow-command"
	script := func(name string) string {
		return "export const meta = { name: \"" + name + "\", description: \"d\" };\nreturn 1;\n"
	}
	// withRuns boots with workflows on and starts count workflows, so the session has tasks to inspect. They never
	// settle, so every one is a live run with a control surface.
	withRuns := func(t *testing.T, count int) *wfRig {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		for i := 0; i < count; i++ {
			w.run(obj{"script": "export const meta = { name: \"wf-" + string(rune('0'+i)) + "\", description: \"d\" };\nawait new Promise(() => {});\n"})
			time.Sleep(3 * time.Millisecond) // distinct start times: newest first is by start time
		}
		for _, task := range w.app.workflows().tasks.values() {
			task := task
			wfUntil(t, func() bool { return workflowControlOf(task) != nil }, "the run's control surface")
		}
		return w
	}
	_ = script
	notes := func() (*[]string, workflowDialogNotify) {
		var got []string
		return &got, func(m, _ string) { got = append(got, m) }
	}

	tw(t, f, "registers no top-level /workflows command", func(t *testing.T) {
		// It lives under /agents instead, deliberately: pi renames a duplicate command to /workflows:1 and /workflows:2.
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		eq(t, w.cmds["workflows"], false)
		eq(t, w.cmds["agents"], true)
	})
	tw(t, f, "offers the entry in the agents menu", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		eq(t, workflowsEntryPattern.MatchString(w.app.workflowsMenuEntry()), true)
	})
	tw(t, f, "hides the entry when workflows are off", func(t *testing.T) {
		// The menu never advertises a switched-off feature; Settings is where it gets turned on.
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": false}`})
		eq(t, w.app.workflowsMenuEntry(), "")
	})
	tw(t, f, "says so when the session has no workflows", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		plan := planWorkflowsMenu(w.app.workflows().tasks.values())
		eq(t, has(t, strings.ToLower(plan.Notice), `no workflows`), true)
		// Nothing to inspect, so no dialog should be opened.
		eq(t, plan.Open == nil, true)
	})
	tw(t, f, "does not prompt for a choice when there is nothing to choose", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		eq(t, len(planWorkflowsMenu(w.app.workflows().tasks.values()).Labels), 0)
	})
	// with runs in the session
	tw(t, f, "opens the dialog directly when exactly one run exists", func(t *testing.T) {
		w := withRuns(t, 1)
		plan := planWorkflowsMenu(w.app.workflows().tasks.values())
		// Straight to the dialog — asking which of one is noise.
		eq(t, len(plan.Labels), 0)
		eq(t, plan.Open != nil, true)
	})
	tw(t, f, "asks which run when several exist, newest first", func(t *testing.T) {
		w := withRuns(t, 3)
		plan := planWorkflowsMenu(w.app.workflows().tasks.values())
		eq(t, len(plan.Labels), 3)
		// Most recent at the top: that is nearly always the one being asked about.
		eq(t, strings.Contains(plan.Labels[0], "wf-2"), true)
		eq(t, strings.Contains(plan.Labels[2], "wf-0"), true)
		// Cancelling the picker must not open anything.
		eq(t, plan.Pick("", false) == nil, true)
	})
	tw(t, f, "actually aborts the run when stopped from the dialog", func(t *testing.T) {
		// The consequential action: it must abort, not merely look like it did.
		w := withRuns(t, 1)
		task := planWorkflowsMenu(w.app.workflows().tasks.values()).Open
		got, notify := notes()
		stopWorkflowFromDialog(task, notify)
		eq(t, has(t, strings.Join(*got, "\n"), `Stopped workflow "wf-0"`), true)
		eq(t, task.Ctx.Err() != nil, true)
		wfUntil(t, func() bool { return workflowRunStatus(task) == "killed" }, "the run to stop")
	})
	tw(t, f, "does not re-announce a stop for an already-aborted run", func(t *testing.T) {
		w := withRuns(t, 1)
		task := planWorkflowsMenu(w.app.workflows().tasks.values()).Open
		got, notify := notes()
		stopWorkflowFromDialog(task, notify)
		stopWorkflowFromDialog(task, notify)
		n := 0
		for _, m := range *got {
			if strings.Contains(m, "Stopped workflow") {
				n++
			}
		}
		eq(t, n, 1)
	})
	tw(t, f, "keeps the labels distinct so identical runs do not collide", func(t *testing.T) {
		// Two runs of the same script share a name, a status and an agent count, and select hands back the string: without
		// the run id the picker would open the first whichever the user chose.
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		for i := 0; i < 2; i++ {
			w.run(obj{"script": "export const meta = { name: \"same\", description: \"d\" };\nawait new Promise(() => {});\n"})
		}
		plan := planWorkflowsMenu(w.app.workflows().tasks.values())
		seen := map[string]bool{}
		for _, l := range plan.Labels {
			seen[l] = true
			eq(t, has(t, l, `wf_[0-9a-f]+$`), true)
		}
		eq(t, len(seen), len(plan.Labels))
	})
	tw(t, f, "opens the run the user picked", func(t *testing.T) {
		w := withRuns(t, 2)
		plan := planWorkflowsMenu(w.app.workflows().tasks.values())
		picked := plan.Pick(plan.Labels[1], true)
		eq(t, picked != nil, true)
		eq(t, picked, plan.Tasks[1])
	})
	tw(t, f, "pauses and resumes the run from the dialog", func(t *testing.T) {
		// The whole path: the key reaches the task, the task reaches the run's control surface, and the run's new state comes out.
		w := withRuns(t, 1)
		task := planWorkflowsMenu(w.app.workflows().tasks.values()).Open
		got, notify := notes()
		pauseWorkflowFromDialog(task, notify)
		// Said plainly: "paused" on a run whose agents are still finishing promises more than it delivers.
		eq(t, has(t, (*got)[len(*got)-1], `Paused — running agents finish`), true)
		eq(t, workflowRunStatus(task), "paused")
		eq(t, workflowControlOf(task).IsPaused(), true)
		resumeWorkflowFromDialog(task, notify)
		eq(t, has(t, (*got)[len(*got)-1], `Resumed`), true)
		eq(t, workflowRunStatus(task), "running")
		eq(t, workflowControlOf(task).IsPaused(), false)
	})
	tskip(t, f, "opens as a centered overlay, like the conversation viewer beside it", "asserts the options passed to ui.custom (overlay: true, anchor: center), which belong to the dialog's drawing in the UI lane's ui/workflow-menu.ts")
}

func workflowRunStatus(task *workflowTask) string {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.Status
}

// Twins of test/e2e/workflow.e2e.test.ts. The original drives print mode against a faux model; the Go rig's children
// are scripted fakes (fakeChild), which reach the same links: the script is parsed and compiled, calls back over the
// bridge, reaches the manager and spawns children with the script's prompts, and the results cross the JSON boundary.
func TestWorkflowE2E(t *testing.T) {
	const f = "e2e/workflow.e2e"
	tw(t, f, "runs a real script whose agents reach real sessions with the script's prompts", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		script := strings.Join([]string{
			`export const meta = { name: "e2e-fanout", description: "spawn two agents", phases: [{ title: "Work" }] };`,
			`phase("Work");`, "const results = await parallel([",
			`  () => agent("FIRST-TASK-MARKER", { label: "one" }),`, `  () => agent("SECOND-TASK-MARKER", { label: "two" }),`, "]);",
			`log("collected " + results.length);`, "return results;"}, "\n")
		text := w.run(obj{"script": script})
		eq(t, strings.Contains(text, "Task ID"), true)
		// The detached run is still going when the parent turn ends, so wait for the children to actually run.
		children := w.fleet.wait(t, 2)
		prompts := []string{children[0].spec.Prompt, children[1].spec.Prompt}
		eq(t, strings.Contains(strings.Join(prompts, "|"), "FIRST-TASK-MARKER") && strings.Contains(strings.Join(prompts, "|"), "SECOND-TASK-MARKER"), true)
		// A workflow child's text is the value agent() resolves to, not a report a person reads, and its prompt has to say so.
		for _, c := range children {
			eq(t, strings.Contains(c.spec.SystemPrompt, "<workflow_child>"), true)
		}
		eq(t, strings.Contains(children[0].spec.SystemPrompt, "Your final message IS the return value"), true)
		for _, c := range children {
			c.finish("SUBAGENT-DONE")
		}
		taskID := w.taskID(text)
		task := w.app.workflows().tasks.get(taskID)
		wfUntil(t, func() bool { return workflowRunStatus(task) == "completed" }, "the run to complete")
		// The journal is what makes resumeFromRunId cheap, and it is only real if a real run writes it.
		journal := readWorkflowJournal(task.JournalPath)
		eq(t, len(journal), 2)
		for _, e := range journal {
			eq(t, e.OK && len(e.Key) > 0, true)
		}
		eq(t, []int{journal[0].Index, journal[1].Index}, []int{0, 1})
		// The children are the workflow's: not in the session's own listing.
		eq(t, len(w.app.mgr.list()), 0)
	})
	tskip(t, f, "runs a saved workflow by name, with schema and a nested child, end to end", "its nested child's agent answers through the injected StructuredOutput tool and the journal holds the validated payload; structured output is the observability lane's (G11, structured-output.ts), so agent({ schema }) fails the run with the refusal text here (workflow_manager.go); the saved-name and nested-workflow() links are twinned in workflow-tool and workflow-runtime")
	tw(t, f, "surfaces a script that fails to parse instead of launching it", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{settings: `{"workflowsEnabled": true}`})
		text := w.run(obj{"script": "export const meta = { name: computeName(), description: \"x\" };\n"})
		// The author-facing rejection reaches the model, not a stack trace.
		eq(t, strings.Contains(text, "PURE LITERAL"), true)
		// And nothing was spawned for a script that never compiled.
		time.Sleep(50 * time.Millisecond)
		eq(t, w.fleet.count(), 0)
	})
	tskip(t, f, "a real model runs a saved workflow by name, and its agent reaches a real session", "opt-in live-LLM smoke (PI_E2E_LIVE) that needs a real provider and key; pigeq's harness scripts the model, and the saved-name dispatch is twinned in workflow-tool")
	tskip(t, f, "a real model dispatches an inline script, and its parallel agents both run", "opt-in live-LLM smoke (PI_E2E_LIVE) that needs a real provider and key; the inline dispatch and the parallel fan-out are twinned in workflow-tool and e2e 'runs a real script'")
	tskip(t, f, "a schema-bearing agent answers through StructuredOutput against a real provider", "opt-in live-LLM smoke (PI_E2E_LIVE) of constrained sampling against a real provider, and structured output is the observability lane's (G11)")
}
