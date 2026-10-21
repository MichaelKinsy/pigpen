package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests of the `/agents` menu over a scripted UI. The original has no test of its menu closure (every wiring test mocks
// registerCommand); these cover its branches and wording.

func menuRig(t *testing.T, project string) (*agentsMenu, *fakeMenuUI, *rig) {
	t.Helper()
	r := settingsRig(t, project)
	ui := &fakeMenuUI{}
	return &agentsMenu{a: r.app, ui: ui, cwd: r.app.cwdValue()}, ui, r
}

func TestAgentsMenuOptions(t *testing.T) {
	m, ui, r := menuRig(t, `{"workflowsEnabled": true}`)
	r.must("Agent", obj{"prompt": "p", "description": "scan", "subagent_type": "Explore"})
	r.fleet.wait(t, 1)
	m.once()
	eq(t, ui.selects[0].Title, "Agents")
	eq(t, ui.selects[0].Options, []string{"Running agents (1) — 1 running, 0 done", "Agent types (3)", "Scheduled jobs (0)", "Workflows (0)", "Create new agent", "Settings"})
}

func TestAgentTypeFileToggles(t *testing.T) {
	m, ui, _ := menuRig(t, "")
	cwd := m.cwd
	answer := func(by map[string]string) func(string, []string) (string, bool) {
		return func(title string, options []string) (string, bool) {
			if v, ok := by[title]; ok {
				return v, true
			}
			return "", false
		}
	}
	// Disabling a built-in with no file writes the stub, after asking where.
	ui.pick = answer(map[string]string{"Plan": "Disable", "Choose location": "Project (.pi/agents/)"})
	m.showAgentDetail("Plan")
	stub := filepath.Join(cwd, ".pi", "agents", "Plan.md")
	data, err := os.ReadFile(stub)
	eq(t, err, nil)
	eq(t, string(data), "---\nenabled: false\n---\n")
	eq(t, getAgentConfig("Plan").Enabled, false)
	eq(t, ui.notified("Disabled Plan ("+stub+")", "info"), true)
	// A disabled default with a file offers Enable, and enabling a bare stub removes the file.
	ui.pick = answer(map[string]string{"Plan": "Enable"})
	m.showAgentDetail("Plan")
	eq(t, ui.selects[len(ui.selects)-1].Options, []string{"Enable", "Edit", "Delete", "Back"}) // a file override loads as a custom agent, as in the original
	_, err = os.Stat(stub)
	eq(t, os.IsNotExist(err), true)
	eq(t, getAgentConfig("Plan").Enabled, true)
	eq(t, ui.notified("Enabled Plan (removed "+stub+")", "info"), true)
	// Ejecting a default writes its full file, and asks before overwriting.
	ui.pick = answer(map[string]string{"Explore": "Eject (export as .md)", "Choose location": "Project (.pi/agents/)"})
	ui.confirm = func(string, string) bool { return false }
	m.showAgentDetail("Explore")
	ejected := filepath.Join(cwd, ".pi", "agents", "Explore.md")
	if _, err := os.Stat(ejected); err != nil {
		t.Fatal(err)
	}
	eq(t, ui.notified("Ejected Explore to "+ejected, "info"), true)
	m.ejectAgent("Explore", getAgentConfig("Explore"))
	eq(t, ui.confirms[len(ui.confirms)-1], confirmCall{"Overwrite", ejected + " already exists. Overwrite?"})
}

func TestManualCreateWizardWritesAnAgentFile(t *testing.T) {
	m, ui, _ := menuRig(t, "")
	ui.pick = func(title string, options []string) (string, bool) {
		switch title {
		case "Choose location":
			return "Project (.pi/agents/)", true
		case "Creation method":
			return "Manual configuration", true
		case "Tools":
			return "read-only (read, bash, grep, find, ls)", true
		case "Model":
			return "haiku", true
		case "Thinking level":
			return "high", true
		}
		return "", false
	}
	answers := map[string]string{"Agent name (filename, no spaces)": "scout", "Description (one line)": "Scout: find #things"}
	ui.input = func(title, _ string) (string, bool) { s, ok := answers[title]; return s, ok }
	ui.editor = func(string, string) (string, bool) { return "You scout.", true }
	m.showCreateWizard()
	path := filepath.Join(m.cwd, ".pi", "agents", "scout.md")
	data, err := os.ReadFile(path)
	eq(t, err, nil)
	eq(t, string(data), "---\ndescription: \"Scout: find #things\"\ntools: read, bash, grep, find, ls\nmodel: \"anthropic/claude-haiku-4-5\"\nthinking: high\nprompt_mode: replace\n---\n\nYou scout.\n")
	eq(t, getAgentConfig("scout") != nil, true)
	eq(t, ui.notified("Created "+path, "info"), true)
}

func readSettingsFileOf(t *testing.T, cwd string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cwd, ".pi", "subagents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSettingsMenuWritesTheWholeSnapshotAndSaysWhatItDid(t *testing.T) {
	m, ui, _ := menuRig(t, "")
	m.a.applySetting(ui, "maxConcurrent", "3")
	eq(t, ui.notified("Max concurrency set to 3", "info"), true)
	file := readSettingsFileOf(t, m.cwd)
	eq(t, file["maxConcurrent"], float64(3))
	eq(t, len(file), 22) // every key but the two that are written only when chosen
	for _, key := range []string{"maxConcurrent", "maxConcurrentForeground", "defaultMaxTurns", "graceTurns", "defaultJoinMode", "backgroundByDefault",
		"schedulingEnabled", "scopeModels", "strictAgentFiles", "disableDefaultAgents", "toolDescriptionMode", "fleetView", "agentMentions",
		"rememberAgents", "widgetMode", "outputTranscript", "worktreeIsolation", "maxSubagentDepth", "reportUsage", "showCost", "showModel", "viewerMarkdown"} {
		if _, ok := file[key]; !ok {
			t.Errorf("%s is not written back", key)
		}
	}
	// A setting that is switched on and has no feature behind it says so.
	m.a.applySetting(ui, "worktreeIsolation", "on")
	eq(t, ui.notified("Worktree isolation enabled. Tool parameter updates on next pi session.", "info"), true)
	eq(t, ui.notified(refusalText("this subagents.json setting (worktreeIsolation: true)"), "warning"), true)
	eq(t, readSettingsFileOf(t, m.cwd)["worktreeIsolation"], true)
	// The two keys written only when chosen.
	m.a.applySetting(ui, "fallbackSubagent", "none")
	m.a.applySetting(ui, "workflowsEnabled", "off")
	file = readSettingsFileOf(t, m.cwd)
	eq(t, file["fallbackSubagent"], "none")
	eq(t, file["workflowsEnabled"], false)
	eq(t, ui.notified("Unknown or disabled agent types will now be rejected", "info"), true)
	// A repeat is not a change.
	m.a.applySetting(ui, "workflowsEnabled", "off")
	eq(t, ui.notified("Workflows already disabled.", "info"), true)
}

func TestSettingsPersistFailureIsSessionOnly(t *testing.T) {
	m, ui, _ := menuRig(t, "")
	old := settingsWriter
	settingsWriter = func(subagentsSettings, string) bool { return false }
	t.Cleanup(func() { settingsWriter = old })
	m.a.applySetting(ui, "graceTurns", "7")
	eq(t, ui.notified("Grace turns set to 7 (session only; failed to persist)", "warning"), true)
}

func TestNumericSettingPromptRepromptsUntilAnInteger(t *testing.T) {
	m, ui, _ := menuRig(t, "")
	var seen []string
	replies := []string{"abc", " 12 "}
	ui.input = func(title, placeholder string) (string, bool) {
		seen = append(seen, title+"|"+placeholder)
		r := replies[0]
		replies = replies[1:]
		return r, true
	}
	m.numericPrompt("maxConcurrent")
	eq(t, seen, []string{"Max concurrency (1+)|10", "Max concurrency (1+)|abc"})
	eq(t, m.numericDone, true)
	eq(t, ui.notified("Max concurrency set to 12", "info"), true)
}

func TestWorkflowsMenuOpensTheInspectorAsAnOverlay(t *testing.T) {
	w := startWfRig(t, wfRigOptions{hasUI: true})
	w.start()
	text := w.run(obj{"script": inlineScript + "await new Promise(() => {});\n"})
	id := w.taskID(text)
	ui := &fakeMenuUI{}
	var opts *menuOverlay
	var rendered []string
	ui.custom = func(factory menuFactory, o *menuOverlay) (any, bool) {
		opts = o
		c := factory(&fakeDialogHost{}, tagTheme(), func(any) {})
		defer c.Dispose()
		rendered = c.Render(100)
		return nil, true
	}
	showWorkflowsMenu(ui, w.app.workflowMenuDeps())
	eq(t, opts != nil && opts.Anchor == "center" && opts.WidthPercent == 90 && opts.HeightPercent == viewportHeightPct, true)
	hasStr(t, strings.Join(rendered, "\n"), "from-inline")
	hasStr(t, strings.Join(rendered, "\n"), "x stop")
	// The Stop key aborts the run and says so.
	task := w.app.workflows().tasks.get(id)
	stopWorkflowFromDialog(task, ui.Notify)
	eq(t, task.Ctx.Err() != nil, true)
}

// Twins of test/clear-completed-wiring.test.ts (issue #108): a background agent that has COMPLETED but whose result the
// model has not read yet was wiped on session_start / session_before_switch, so the next get_subagent_result answered
// "Agent not found". Both handlers keep unread records; a read one goes.
const fClear = "clear-completed-wiring"

func completedUnread(t *testing.T) (*rig, string) {
	t.Helper()
	r := startRig(t)
	spawnBackground(r, "Review monero_en.rs in depth")
	c := r.fleet.wait(t, 1)[0]
	c.finish("THE-RESULT-PAYLOAD")
	r.waitEmitted("subagents:completed")
	return r, r.app.mgr.list()[0].ID
}

func TestClearCompletedWiring(t *testing.T) {
	tw(t, fClear, "session_before_switch (user switches sessions) does NOT wipe the unread result", func(t *testing.T) {
		r, id := completedUnread(t)
		r.FireAll("session_before_switch", obj{})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		lacksStr(t, out, "Agent not found")
		hasStr(t, out, "THE-RESULT-PAYLOAD")
	})
	tw(t, fClear, "session_start (/resume) does NOT wipe the unread result", func(t *testing.T) {
		r, id := completedUnread(t)
		r.FireAll("session_start", obj{})
		out := r.must("get_subagent_result", obj{"agent_id": id})
		lacksStr(t, out, "Agent not found")
		hasStr(t, out, "THE-RESULT-PAYLOAD")
	})
	tw(t, fClear, "once read, a session switch DOES evict it — the fix stays surgical, no leak", func(t *testing.T) {
		r, id := completedUnread(t)
		hasStr(t, r.must("get_subagent_result", obj{"agent_id": id}), "THE-RESULT-PAYLOAD")
		r.FireAll("session_before_switch", obj{})
		hasStr(t, r.must("get_subagent_result", obj{"agent_id": id}), "Agent not found")
	})
}

// The inspector's `c` key hides the dialog's own overlay while the conversation is up and brings it back after.
func TestWorkflowDialogOpenAgentHidesAndRestoresTheDialog(t *testing.T) {
	tw(t, "workflow-dialog-open-agent", "carries the child's record id onto the row and opens its conversation on c", func(t *testing.T) {
		task := createWorkflowTask(workflowTaskInit{ID: "wf_run", StartTime: dlgStart})
		updateWorkflowProgressBatch(task, []workflowEntry{newPhaseEntry(0, "Review"),
			&workflowAgentEntry{Type: "workflow_agent", Index: 0, Label: "audit", PhaseIndex: intp(0), State: entryDone, RecordID: "rec-1"}})
		var viewed []string
		handle := &fakeOverlayHandle{}
		deps := workflowMenuDeps{tasks: newWorkflowTaskStore(), getRecord: func(id string) *agentView { return &agentView{ID: id} },
			viewAgentConversation: func(_ menuUI, rec *agentView) { viewed = append(viewed, rec.ID) }}
		ui := &fakeMenuUI{}
		finished := make(chan struct{})
		ui.custom = func(factory menuFactory, o *menuOverlay) (any, bool) {
			o.OnHandle(handle)
			c := factory(&fakeDialogHost{}, tagTheme(), func(any) {})
			defer c.Dispose()
			c.HandleInput("c")
			handle.waitHidden(t, []bool{true, false})
			close(finished)
			return nil, true
		}
		showWorkflowDialog(ui, task, deps)
		<-finished
		eq(t, viewed, []string{"rec-1"})
	})
}

func TestDisableKeepsTheFilesLineEndings(t *testing.T) {
	content, outcome := disableInContent("---\r\ndescription: Scout\r\n---\r\n\r\nBody.\r\n")
	eq(t, outcome, outcomeDisabled)
	eq(t, content, "---\r\nenabled: false\r\ndescription: Scout\r\n---\r\n\r\nBody.\r\n")
}
