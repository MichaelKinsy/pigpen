package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The portable slash commands through the host boundary.

// slashHost starts a headless host (no UI dialogs) with a faked child runner.
func slashHost(t *testing.T, outputs map[string]string) (*Host, *childCalls, string) {
	t.Helper()
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	calls := fakeChildren(t, outputs)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	hasUI := false
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd, HasUI: &hasUI,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-slash"}, ""
			}
			return nil, ""
		},
	})
	return h, calls, cwd
}

// slashTexts are the messages a command sent to the thread, in order.
func slashTexts(h *Host, customType string) []string {
	var out []string
	for _, c := range h.CallsTo("sendMessage") {
		msg, _ := c.Args["message"].(map[string]any)
		if ct, _ := msg["customType"].(string); ct == customType {
			content, _ := msg["content"].(string)
			out = append(out, content)
		}
	}
	return out
}

func TestRunCommandRunsForegroundAndSendsTheResult(t *testing.T) {
	h, calls, _ := slashHost(t, map[string]string{"build": "built it"})
	eq(t, h.Command("run", "helper build"), "", "the command")
	waitFor(t, func() bool { return calls.len() == 1 }, "the child")
	eq(t, calls.at(0).Stdin, "Task: build", "the task reaches the child")
	texts := slashTexts(h, "subagent-slash-text")
	if len(texts) == 0 || !strings.Contains(texts[0], "built it") {
		t.Errorf("slash texts: %v", texts)
	}
	// --bg detaches and the started message lands in the thread.
	eq(t, h.Command("run", "helper build --bg"), "", "the async command")
	var started bool
	for _, text := range slashTexts(h, "subagent-slash-text") {
		if strings.Contains(text, "Async: helper [") {
			started = true
		}
	}
	if !started {
		t.Errorf("no started message: %v", slashTexts(h, "subagent-slash-text"))
	}
	waitFor(t, func() bool { return calls.len() == 2 }, "the async child")
	// An unknown agent notifies and runs nothing.
	before := calls.len()
	eq(t, h.Command("run", "nope fix"), "", "unknown agent")
	notes := h.CallsTo("ui.notify")
	last, _ := notes[len(notes)-1].Args["message"].(string)
	if !strings.HasPrefix(last, "Unknown agent 'nope'.") {
		t.Errorf("unknown agent note: %q", last)
	}
	eq(t, calls.len(), before, "no child for the unknown agent")
	// No arguments reports the usage line.
	eq(t, h.Command("run", "  "), "", "usage")
	notes = h.CallsTo("ui.notify")
	last, _ = notes[len(notes)-1].Args["message"].(string)
	eq(t, last, "Usage: /run <agent> [task] [--bg] [--fork]", "usage line")
}

func TestSubagentsAdminCommand(t *testing.T) {
	h, _, cwd := slashHost(t, nil)
	// Headless without arguments lists the agents.
	eq(t, h.Command("subagents", ""), "", "list")
	texts := slashTexts(h, "subagents-admin")
	if len(texts) == 0 || !strings.Contains(texts[0], "- helper (project)") {
		t.Errorf("admin list: %v", texts)
	}
	// Headless with an agent prints its metadata.
	eq(t, h.Command("subagents", "helper"), "", "metadata")
	texts = slashTexts(h, "subagents-admin")
	last := texts[len(texts)-1]
	for _, want := range []string{
		"Agent: helper (project)",
		"Path: " + filepath.Join(cwd, ".pi", "agents", "helper.md"),
		"Description: Helps",
		"Model: default / inherit",
		"System prompt mode: replace",
		"Inherit project context: false",
		"Inherit global context: false",
		"Inherit skills: false",
		"System Prompt:",
		"You help.",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("metadata missing %q:\n%s", want, last)
		}
	}
	// An unknown name reports the available list.
	eq(t, h.Command("subagents", "nope"), "", "unknown")
	texts = slashTexts(h, "subagents-admin")
	if !strings.Contains(texts[len(texts)-1], "Subagent 'nope' not found.") {
		t.Errorf("unknown: %v", texts)
	}
}

func TestSubagentsAdminInteractiveFlow(t *testing.T) {
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	fakeChildren(t, nil)
	cwd := tmp(t)
	home := withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "custom.md"), "---\nname: custom\ndescription: Custom.\nmodel: p/old\n---\nPrompt.\n")
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "user-agent.md"), agentMD("user-agent", "User.", "User."))
	// Scripted answers: pick the agent, the action, then the value.
	answers := []string{"custom (project)", "Change model", "p/new"}
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getSessionID":
				return map[string]any{"sessionId": "session-ui"}, ""
			case "ui.select":
				if len(answers) == 0 {
					return map[string]any{"ok": false}, ""
				}
				a := answers[0]
				answers = answers[1:]
				return map[string]any{"ok": true, "selected": a}, ""
			}
			return nil, ""
		},
	})
	eq(t, h.Command("subagents", ""), "", "interactive admin")
	// The chosen agent's model was written to its file.
	content, err := readFileString(filepath.Join(cwd, ".pi", "agents", "custom.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "model: p/new") {
		t.Errorf("model not saved:\n%s", content)
	}
	notes := h.CallsTo("ui.notify")
	last, _ := notes[len(notes)-1].Args["message"].(string)
	eq(t, last, "Updated 'custom' model to 'p/new' in "+filepath.Join(cwd, ".pi", "agents", "custom.md")+".", "the original's save message")

	// A builtin saves through the user settings override.
	answers = []string{"scout (builtin)", "Change thinking level", "high"}
	eq(t, h.Command("subagents", ""), "", "builtin admin")
	settings, err := readFileString(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(settings, `"thinking": "high"`) {
		t.Errorf("settings override:\n%s", settings)
	}
	notes = h.CallsTo("ui.notify")
	last, _ = notes[len(notes)-1].Args["message"].(string)
	if !strings.Contains(last, "Saved user settings override for 'scout' with thinking 'high' in ") {
		t.Errorf("builtin save message: %q", last)
	}
}

func TestStopGuideModelsAndFleetCommands(t *testing.T) {
	h, _, _ := slashHost(t, nil)
	// guide
	eq(t, h.Command("subagents-guide", "agents"), "", "guide")
	texts := slashTexts(h, "subagent-slash-text")
	if !strings.Contains(texts[len(texts)-1], "# Agents") {
		t.Errorf("guide: %v", texts)
	}
	eq(t, h.Command("subagents-guide", "two words"), "", "guide usage")
	notes := h.CallsTo("ui.notify")
	last, _ := notes[len(notes)-1].Args["message"].(string)
	eq(t, last, "Usage: /subagents-guide [topic]", "guide usage line")

	// models
	eq(t, h.Command("subagents-models", ""), "", "models")
	texts = slashTexts(h, "subagent-slash-text")
	if !strings.Contains(texts[len(texts)-1], "Subagent models") {
		t.Errorf("models: %v", texts)
	}
	eq(t, h.Command("subagents-models", "a b"), "", "models usage")
	notes = h.CallsTo("ui.notify")
	last, _ = notes[len(notes)-1].Args["message"].(string)
	eq(t, last, "Usage: /subagents-models [agent-name]", "models usage line")

	// fleet with nothing active keeps the original's empty text.
	eq(t, h.Command("subagents-fleet", ""), "", "fleet")
	texts = slashTexts(h, "subagent-slash-text")
	if !strings.Contains(texts[len(texts)-1], "No active subagent fleet.") {
		t.Errorf("fleet: %v", texts)
	}

	// stop with nothing active notifies the original's line.
	eq(t, h.Command("subagents-stop", ""), "", "stop")
	notes = h.CallsTo("ui.notify")
	last, _ = notes[len(notes)-1].Args["message"].(string)
	eq(t, last, "No active current-session async runs or scheduled subagent runs to stop.", "stop line")
	// stop with too many arguments reports the usage.
	eq(t, h.Command("subagents-stop", "a b c"), "", "stop usage")
	texts = slashTexts(h, "subagent-slash-text")
	if !strings.Contains(texts[len(texts)-1], "Usage: /subagents-stop [run-id] [child-id]") {
		t.Errorf("stop usage: %v", texts)
	}
}

func TestStopCommandStopsARun(t *testing.T) {
	h, _, _ := slashHost(t, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return sub.ChildResult{Stdout: "ok"}, nil
		case <-done:
			return sub.ChildResult{}, sub.ErrCancelled
		}
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t"})
	eq(t, failure, "", "start")
	runID := asyncIDRe.FindStringSubmatch(string(raw))[1]
	<-started
	// Headless stop with no id lists the targets with the original's shape.
	eq(t, h.Command("subagents-stop", ""), "", "fallback list")
	texts := slashTexts(h, "subagent-slash-text")
	list := texts[len(texts)-1]
	for _, want := range []string{"Subagent stop targets:", "- helper run " + runID, "Stop run: subagent({ action: \"stop\", id: \"" + runID + "\" })", "slash: /subagents-stop " + runID} {
		if !strings.Contains(list, want) {
			t.Errorf("fallback missing %q:\n%s", want, list)
		}
	}
	// Stopping by id seals the run with the original's message.
	eq(t, h.Command("subagents-stop", runID), "", "stop by id")
	waitFor(t, func() bool {
		status, failure := h.Tool("subagent", map[string]any{"action": "status", "id": runID})
		return failure == "" && strings.Contains(string(status), "State: stopped")
	}, "the stopped run")
	close(release)
}

// readFileString reads a file as text for assertions.
func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
