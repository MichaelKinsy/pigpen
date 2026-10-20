package pi_subagents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The mission actions, the launch parameters and /prompt-workflow through the real tool and command boundary (the port's own
// tests: the original's twins call the modules directly).

func resultText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content any `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	switch c := r.Content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				b.WriteString(fmt.Sprint(m["text"]))
			}
		}
		return b.String()
	}
	return string(raw)
}

func missionHost(t *testing.T) (*Host, string) {
	t.Helper()
	fakeChildren(t, map[string]string{"build": "built it"})
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	return StartHost(t, Extension(), HostOptions{Cwd: cwd}), cwd
}

func TestMissionActionsThroughTheTool(t *testing.T) {
	h, _ := missionHost(t)
	raw, failure := h.Tool("subagent", map[string]any{"action": "mission.create", "mission": map[string]any{"title": "Ship it", "objective": "Do it"}})
	eq(t, failure, "", "create")
	text := resultText(t, raw)
	m := regexp.MustCompile(`^Created mission ([0-9a-f-]{36}): Ship it$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("create text: %q", text)
	}
	id := m[1]
	raw, failure = h.Tool("subagent", map[string]any{"action": "mission.list"})
	eq(t, failure, "", "list")
	if !strings.Contains(resultText(t, raw), id+"  planned  Ship it  ") {
		t.Errorf("list: %s", raw)
	}
	raw, failure = h.Tool("subagent", map[string]any{"action": "mission.close", "missionId": id, "missionStatus": "cancelled", "summary": "Not needed"})
	eq(t, failure, "", "close")
	eq(t, resultText(t, raw), "Closed mission "+id+" as cancelled.", "close text")

	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"action": "mission.create"}, "mission must be an object"},
		{map[string]any{"action": "mission.show"}, "missionId must be a non-empty string"},
		{map[string]any{"action": "mission.show", "missionId": "../x"}, "missionId must contain only letters, numbers, '.', '_', or '-' and cannot contain '..'"},
		{map[string]any{"action": "mission.list", "missionScope": "everywhere"}, `missionScope must be "project" or "global"`},
		{map[string]any{"action": "mission.update", "missionId": id, "missionUpdate": map[string]any{}}, "missionUpdate must include at least one supported field"},
		{map[string]any{"action": "mission.update", "missionId": id, "missionUpdate": map[string]any{"bogus": 1}}, "missionUpdate.bogus is unknown"},
		{map[string]any{"action": "mission.close", "missionId": id, "missionStatus": "active"}, "mission.close missionStatus must be completed, failed, or cancelled"},
		{map[string]any{"action": "mission.attach-run", "missionId": id}, "mission.attach-run requires runId or id"},
		{map[string]any{"action": "mission.resolve-decision", "missionId": id, "id": "d1"}, "mission.resolve-decision requires a non-empty summary"},
	} {
		_, failure := h.Tool("subagent", c.params)
		eq(t, failure, c.want, fmt.Sprint(c.params))
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "mission.show", "missionId": "missing"})
	if !strings.HasPrefix(failure, "Mission 'missing' was not found in mission directory ") {
		t.Errorf("not found: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "mission.show", "missionId": id, "agent": "helper"})
	eq(t, failure, refusalHead+"the `agent` option"+refusalTail, "an option the action does not read is refused")
}

func TestSingleRunRecordsItsMission(t *testing.T) {
	h, cwd := missionHost(t)
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false})
	eq(t, failure, "", "run")
	if !regexp.MustCompile(`^built it\nMission: [0-9a-f-]{36} \(completed\)$`).MatchString(resultText(t, raw)) {
		t.Errorf("text: %q", resultText(t, raw))
	}
	raw, failure = h.Tool("subagent", map[string]any{"action": "mission.list"})
	eq(t, failure, "", "list")
	if !strings.Contains(resultText(t, raw), "  completed  [prompt redacted]  ") {
		t.Errorf("auto mission: %s", raw)
	}
	raw, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false, "mission": false})
	eq(t, failure, "", "opt-out")
	eq(t, resultText(t, raw), "built it", "no mission line")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false, "missionId": "nope"})
	if !strings.HasPrefix(failure, "Mission 'nope' was not found in mission directory ") {
		t.Errorf("unknown mission: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false, "mission": true})
	eq(t, failure, "mission must be an object", "invalid mission")
	_ = cwd
}

func TestMissionConfigDisablesAutomaticMissions(t *testing.T) {
	h, _ := missionHost(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"), `{"missions":{"enabled":false}}`)
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false})
	eq(t, failure, "", "run")
	eq(t, resultText(t, raw), "built it", "no mission")
}

func TestPromptWorkflowCommand(t *testing.T) {
	h, cwd := missionHost(t)
	if !h.cmds["prompt-workflow"] {
		t.Fatal("/prompt-workflow is not registered")
	}
	if h.cmds["chain-prompts"] {
		t.Error("/chain-prompts is registered")
	}
	writeFile(t, filepath.Join(cwd, ".pi", "prompts", "demo.md"), "---\ndescription: Demo prompt\nsubagent: helper\n---\nDo $1\n")
	h.Command("prompt-workflow", "list")
	sent := h.CallsTo("sendMessage")
	if len(sent) == 0 || !strings.Contains(fmt.Sprint(sent[len(sent)-1].Args["message"]), "- demo: Demo prompt (") {
		t.Errorf("list: %v", sent)
	}
	h.Command("prompt-workflow", "nosuch")
	h.Command("prompt-workflow", "demo thing")
	notes := h.CallsTo("ui.notify")
	if len(notes) < 2 {
		t.Fatalf("notes: %v", notes)
	}
	eq(t, notes[len(notes)-2].Args["message"], "Unknown prompt workflow: nosuch", "unknown")
	eq(t, notes[len(notes)-1].Args["message"], refusalHead+"workflow runs (`workflowScript`)"+refusalTail, "a workflow run is refused until the runtime is wired")
}
