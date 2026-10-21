package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Everything of the original that this port does not do yet is refused with one message (refuse.go): a tool error,
// or a notification for a setting; never a silent fallback and never a partial run.

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) { // the mutation runner copies only the extension directory
		t.Skip("not in this copy of the port: " + path)
	}
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRefusalMessageNamesThisPackageAndThePinnedOriginal(t *testing.T) {
	eq(t, refusalText("x"), "@pi-in-go/pigpen-tintinweb-subagents (Go port) does not support x yet. Install the original extension, npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.")
	// Where the Package files are beside the module (not in a scratch copy of the module), the names come from them.
	if _, err := os.Stat("../../package.json"); err != nil {
		return
	}
	pkg := readJSONFile(t, "../../package.json")
	orig := readJSONFile(t, "../../port/oracle/package.json")
	eq(t, portName, pkg["name"])
	eq(t, originalSpec, "npm:"+orig["name"].(string)+"@"+orig["version"].(string))
}

// Every Agent call parameter is ported (schedule included): "values that mean nothing" run like any call.
func TestAgentCallParametersThatMeanNothingRun(t *testing.T) {
	r := startRig(t)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "isolation": "off", "inherit_context": false, "resume": ""})
	r.fleet.wait(t, 1)
}

// agentFileRig starts a session whose workspace holds one custom agent, "custom", with the given frontmatter lines.
func agentFileRig(t *testing.T, frontmatter string) *rig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".pi", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.md"), []byte("---\ndescription: a custom agent\n"+frontmatter+"\n---\nDo the work.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return startRigIn(t, cwd)
}

// Every field of an agent file is ported: none is refused.
func TestEveryAgentFileFieldRuns(t *testing.T) {
	r := agentFileRig(t, "extensions: false\nskills: true\npersist_session: false\noutput_transcript: false\nisolation: off\ninherit_context: false\nmemory: none\nallowed_subagents: all\nmax_turns: 4")
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.NoExtensions, true)
	eq(t, c.spec.MaxTurns, 4)
	// the spawn RPC takes the same file
	reply := r.request("subagents:rpc:spawn", obj{"type": "custom", "prompt": "p", "options": obj{"description": "d"}})
	eq(t, reply["success"], true)
	r.fleet.wait(t, 2)
}

func TestUnportedSpawnRPCOptionsAreRefused(t *testing.T) {
	cases := []struct {
		name    string
		options obj
		feature string
	}{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := startRig(t)
			options := obj{"description": "d"}
			for k, v := range c.options {
				options[k] = v
			}
			reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": options})
			eq(t, reply["success"], false)
			eq(t, reply["error"], refusalText(c.feature))
			eq(t, r.fleet.count(), 0)
		})
	}
	t.Run("honored options run", func(t *testing.T) {
		r := startRig(t)
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{"description": "d", "name": "scout", "thinkingLevel": "low", "maxTurns": 3.0, "isBackground": true, "model": nil}})
		eq(t, reply["success"], true)
		c := r.fleet.wait(t, 1)[0]
		eq(t, r.app.mgr.list()[0].Alias, "scout") // the name option is honored
		eq(t, c.spec.Thinking, "low")
		eq(t, c.spec.MaxTurns, 3)
		recs := r.app.mgr.list()
		eq(t, recs[0].Name, "scout") // the name is the agent's, and addresses it
		eq(t, recs[0].Alias, "scout")
		eq(t, r.app.mgr.resolve("scout") != nil, true)
		eq(t, r.app.mgr.get(reply["data"].(map[string]any)["id"].(string)).Name, "scout")
	})
}

func settingsRig(t *testing.T, project string) *rig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	if project != "" {
		if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return startRigIn(t, cwd)
}

func notices(r *rig) []string {
	var out []string
	for _, c := range r.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}

func TestUnportedSettingsAreRefusedWithANotification(t *testing.T) {
	cases := []struct{ name, json, listed string }{
		{"fleetView", `{"fleetView": true}`, "fleetView: true"},
		{"agentMentions", `{"agentMentions": "direct"}`, `agentMentions: "direct"`},
		{"agentMentions legacy true", `{"agentMentions": true}`, "agentMentions: true"},
		{"widgetMode", `{"widgetMode": "all"}`, `widgetMode: "all"`},
		{"workflowsEnabled", `{"workflowsEnabled": true}`, "workflowsEnabled: true"},
		{"showModel", `{"showModel": true}`, "showModel: true"},
		{"viewerMarkdown", `{"viewerMarkdown": "all"}`, `viewerMarkdown: "all"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := settingsRig(t, c.json)
			eq(t, notices(r), []string{refusalText("these subagents.json settings (" + c.listed + ")")})
		})
	}
	t.Run("the same notice is not repeated by a second session_start", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true}`)
		r.Fire("session_start", obj{"reason": "reload"})
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showCost: true (only its widget display; the cost in the notification, result and steer texts is shown))")})
	})
	t.Run("two settings are listed in one notice", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true, "showModel": true}`)
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showCost: true (only its widget display; the cost in the notification, result and steer texts is shown), showModel: true)")})
	})
	t.Run("the same settings are announced once", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true}`)
		r.Fire("session_start", obj{"reason": "new"})
		r.Fire("session_start", obj{"reason": "resume"})
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showCost: true (only its widget display; the cost in the notification, result and steer texts is shown))")})
	})
	t.Run("the global file counts and the project file overrides it", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "subagents.json"), []byte(`{"showCost": true, "showModel": true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cwd := t.TempDir()
		_ = os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
		_ = os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"showCost": false}`), 0o644)
		r := startRigIn(t, cwd)
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showModel: true)")})
	})
	t.Run("the same notice is not repeated when another session starts with the same settings", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true}`)
		r.Fire("session_start", obj{"reason": "new"})
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showCost: true (only its widget display; the cost in the notification, result and steer texts is shown))")})
	})
	t.Run("the same notice is not repeated when another session starts with the same settings", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true}`)
		r.Fire("session_start", obj{"reason": "new"})
		eq(t, notices(r), []string{refusalText("these subagents.json settings (showCost: true)")})
	})
}

func TestSettingsThePortMatchesAreNotRefused(t *testing.T) {
	r := settingsRig(t, `{"maxConcurrent": 3, "defaultMaxTurns": 5, "graceTurns": 2, "disableDefaultAgents": false, "fallbackSubagent": "none",
		"maxConcurrentForeground": 0, "defaultJoinMode": "async", "backgroundByDefault": true, "schedulingEnabled": false, "scopeModels": false,
		"strictAgentFiles": false, "toolDescriptionMode": "full", "fleetView": false, "agentMentions": "off", "rememberAgents": false,
		"widgetMode": "off", "outputTranscript": false, "worktreeIsolation": false, "workflowsEnabled": false, "maxSubagentDepth": 3,
		"reportUsage": false, "showCost": false, "showModel": false, "showCost2": "x", "fleetView2": 1, "agentMentions2": 1}`)
	eq(t, len(notices(r)), 0)
	r = settingsRig(t, `{"widgetMode": "sideways", "maxSubagentDepth": "deep", "scopeModels": "yes"}`) // wrong types and unknown values are ignored, as in the original
	eq(t, len(notices(r)), 0)
	r = settingsRig(t, "")
	eq(t, len(notices(r)), 0)
}
