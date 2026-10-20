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
	pkg := readJSONFile(t, "../../package.json")
	orig := readJSONFile(t, "../../port/oracle/package.json")
	eq(t, portName, pkg["name"])
	eq(t, originalSpec, "npm:"+orig["name"].(string)+"@"+orig["version"].(string))
	eq(t, refusalText("x"), "@pi-in-go/pigpen-tintinweb-subagents (Go port) does not support x yet. Install the original extension, npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.")
}

func TestUnportedAgentCallParametersAreRefused(t *testing.T) {
	cases := []struct {
		name    string
		params  obj
		feature string
	}{
		{"schedule", obj{"schedule": "5m"}, "scheduled agent runs (the schedule parameter)"},
		{"isolation worktree", obj{"isolation": "worktree"}, `worktree isolation (isolation: "worktree")`},
		{"inherit_context", obj{"inherit_context": true}, "inheriting the parent conversation (inherit_context)"},
		{"resume", obj{"resume": "explore"}, "resuming a finished agent's session (resume)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := startRig(t)
			params := obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}
			for k, v := range c.params {
				params[k] = v
			}
			text, failure := r.tool("Agent", params)
			eq(t, text, "")
			eq(t, strings.Contains(failure, refusalText(c.feature)), true)
			eq(t, r.fleet.count(), 0)
		})
	}
	t.Run("values that mean nothing are not refused", func(t *testing.T) {
		r := startRig(t)
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "isolation": "off", "inherit_context": false, "resume": ""})
		r.fleet.wait(t, 1)
	})
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

func TestUnportedAgentFileFieldsAreRefused(t *testing.T) {
	cases := []struct{ name, frontmatter, feature string }{
		{"inherit_context", "inherit_context: true", "inheriting the parent conversation (inherit_context in the agent file)"},
		{"isolation", "isolation: worktree", "worktree isolation (isolation: worktree in the agent file)"},
		{"memory", "memory: project", "persistent agent memory (memory: project)"},
		{"allowed_subagents", "allowed_subagents: all", "nested delegation (allowed_subagents)"},
		{"ext selectors", "tools: read, ext:web", "extension tool selectors (ext: in tools)"},
		{"disallowed_tools", "disallowed_tools: bash", "disallowed_tools"},
		{"extensions list", "extensions: foo, bar", "an extension allow-list (extensions)"},
		{"exclude_extensions", "exclude_extensions: foo", "exclude_extensions"},
		{"skills false", "skills: false", "skill selection (skills)"},
		{"skills list", "skills: a, b", "skill selection (skills)"},
		{"persist_session", "persist_session: true", "persisted agent sessions (persist_session)"},
		{"output_transcript", "output_transcript: true", "output transcripts (output_transcript)"},
		{"session_dir", "session_dir: /tmp/x", "a session directory (session_dir)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := agentFileRig(t, c.frontmatter)
			text, failure := r.tool("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
			eq(t, text, "")
			eq(t, strings.Contains(failure, refusalText(c.feature)), true)
			eq(t, r.fleet.count(), 0)
			// the spawn RPC reaches the same check
			reply := r.request("subagents:rpc:spawn", obj{"type": "custom", "prompt": "p", "options": obj{"description": "d"}})
			eq(t, reply["success"], false)
			eq(t, strings.Contains(reply["error"].(string), refusalText(c.feature)), true)
			eq(t, r.fleet.count(), 0)
		})
	}
	t.Run("supported fields run", func(t *testing.T) {
		r := agentFileRig(t, "extensions: false\nskills: true\npersist_session: false\noutput_transcript: false\nisolation: off\ninherit_context: false\nmemory: none\nmax_turns: 4")
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "custom"})
		c := r.fleet.wait(t, 1)[0]
		eq(t, c.spec.Isolated, true)
		eq(t, c.spec.MaxTurns, 4)
	})
}

func TestUnportedSpawnRPCOptionsAreRefused(t *testing.T) {
	cases := []struct {
		name    string
		options obj
		feature string
	}{
		{"isolated", obj{"isolated": true}, `the spawn RPC option "isolated"`},
		{"isolation", obj{"isolation": "worktree"}, `the spawn RPC option "isolation"`},
		{"inheritContext", obj{"inheritContext": true}, `the spawn RPC option "inheritContext"`},
		{"resumeSessionFile", obj{"resumeSessionFile": "/x"}, `the spawn RPC option "resumeSessionFile"`},
		{"reclaim", obj{"reclaim": obj{"handle": "a"}}, `the spawn RPC option "reclaim"`},
		{"bypassQueue", obj{"bypassQueue": true}, `the spawn RPC option "bypassQueue"`},
		{"blocking", obj{"blocking": true}, `the spawn RPC option "blocking"`},
		{"workflowId", obj{"workflowId": "w"}, `the spawn RPC option "workflowId"`},
		{"foreground", obj{"isBackground": false}, "foreground agents through the spawn RPC (isBackground: false)"},
	}
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
		eq(t, c.spec.Thinking, "low")
		eq(t, c.spec.MaxTurns, 3)
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
		{"maxConcurrentForeground", `{"maxConcurrentForeground": 2}`, "maxConcurrentForeground: 2"},
		{"defaultJoinMode", `{"defaultJoinMode": "group"}`, `defaultJoinMode: "group"`},
		{"backgroundByDefault", `{"backgroundByDefault": false}`, "backgroundByDefault: false"},
		{"schedulingEnabled", `{"schedulingEnabled": true}`, "schedulingEnabled: true"},
		{"scopeModels", `{"scopeModels": true}`, "scopeModels: true"},
		{"strictAgentFiles", `{"strictAgentFiles": true}`, "strictAgentFiles: true"},
		{"toolDescriptionMode", `{"toolDescriptionMode": "compact"}`, `toolDescriptionMode: "compact"`},
		{"agentMentions", `{"agentMentions": "direct"}`, `agentMentions: "direct"`},
		{"agentMentions legacy true", `{"agentMentions": true}`, "agentMentions: true"},
		{"rememberAgents", `{"rememberAgents": true}`, "rememberAgents: true"},
		{"outputTranscript", `{"outputTranscript": true}`, "outputTranscript: true"},
		{"worktreeIsolation", `{"worktreeIsolation": true}`, "worktreeIsolation: true"},
		{"workflowsEnabled", `{"workflowsEnabled": true}`, "workflowsEnabled: true"},
		{"maxSubagentDepth", `{"maxSubagentDepth": 2}`, "maxSubagentDepth: 2"},
		{"reportUsage", `{"reportUsage": true}`, "reportUsage: true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := settingsRig(t, c.json)
			eq(t, notices(r), []string{refusalText("these subagents.json settings (" + c.listed + ")")})
		})
	}
	t.Run("two settings are listed in one notice", func(t *testing.T) {
		r := settingsRig(t, `{"reportUsage": true, "scopeModels": true}`)
		eq(t, notices(r), []string{refusalText("these subagents.json settings (scopeModels: true, reportUsage: true)")})
	})
	t.Run("the global file counts and the project file overrides it", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "subagents.json"), []byte(`{"showCost": true, "reportUsage": true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cwd := t.TempDir()
		_ = os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
		_ = os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"showCost": false}`), 0o644)
		r := startRigIn(t, cwd)
		eq(t, notices(r), []string{refusalText("these subagents.json settings (reportUsage: true)")})
	})
}

func TestSettingsThePortMatchesAreNotRefused(t *testing.T) {
	r := settingsRig(t, `{"maxConcurrent": 3, "defaultMaxTurns": 5, "graceTurns": 2, "disableDefaultAgents": false, "fallbackSubagent": "none",
		"maxConcurrentForeground": 0, "defaultJoinMode": "async", "backgroundByDefault": true, "schedulingEnabled": false, "scopeModels": false,
		"strictAgentFiles": false, "toolDescriptionMode": "full", "fleetView": false, "agentMentions": "off", "rememberAgents": false,
		"widgetMode": "off", "outputTranscript": false, "worktreeIsolation": false, "workflowsEnabled": false, "maxSubagentDepth": 1,
		"reportUsage": false, "showCost": false, "showModel": false, "showCost2": "x", "fleetView2": 1, "agentMentions2": 1}`)
	eq(t, len(notices(r)), 0)
	r = settingsRig(t, `{"widgetMode": "sideways", "maxSubagentDepth": "deep", "scopeModels": "yes"}`) // wrong types and unknown values are ignored, as in the original
	eq(t, len(notices(r)), 0)
	r = settingsRig(t, "")
	eq(t, len(notices(r)), 0)
}
