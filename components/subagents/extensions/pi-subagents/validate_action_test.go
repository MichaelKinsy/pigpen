package pi_subagents_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The validate action: a script checked without a child launching (the original's text and JSON, public-execution.ts and
// subagent-executor.ts:5266-5287).

func validateHost(t *testing.T) (*Host, string) {
	t.Helper()
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	return StartHost(t, sub.Extension(), HostOptions{Cwd: cwd}), cwd
}

func validateResult(t *testing.T, params map[string]any) (ok bool, text string, failure string) {
	t.Helper()
	h, _ := validateHost(t)
	params["action"] = "validate"
	raw, failure := h.Tool("subagent", params)
	if failure != "" {
		return false, failure, failure
	}
	return true, toolText(t, raw), ""
}

func TestValidateAction(t *testing.T) {
	_, text, failure := validateResult(t, map[string]any{"workflowScript": `const r = await runs.run("a", { agent: "helper", task: "x" }); return r.output;`})
	eq(t, failure, "", "a valid script")
	eq(t, text, `{"ok":true,"errors":[]}`, "a valid script's answer")

	_, text, failure = validateResult(t, map[string]any{"workflowScript": `await runs.run("a", { agent: "helpr", task: "x" });`})
	eq(t, text, failure, "an error result carries the JSON")
	var v struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	if v.OK || len(v.Errors) != 1 || v.Errors[0].Kind != "agent" || !strings.Contains(v.Errors[0].Message, "Unknown agent 'helpr'. Did you mean 'helper'?") {
		t.Errorf("unknown agent: %s", text)
	}

	_, _, failure = validateResult(t, map[string]any{"workflowScript": `async function f() {}`})
	if !strings.Contains(failure, "workflowScript does not support nested async functions") {
		t.Errorf("nested async: %s", failure)
	}
	_, _, failure = validateResult(t, map[string]any{"workflowScript": `await runs.run("a", {`})
	if !strings.Contains(failure, "workflowScript must be valid JavaScript") && !strings.Contains(failure, `"ok":false`) {
		t.Errorf("syntax error: %s", failure)
	}
	_, _, failure = validateResult(t, map[string]any{"workflowScript": `await runs.run("a", {agent:"helper",task:"x"}); await runs.run("b", {agent:"helper",task:"x"}); return 1;`, "maxSubagentSpawnsPerRun": 1})
	if !strings.Contains(failure, "statically requires child launches 'a', 'b'; minimum required: 2; configured: 1.") {
		t.Errorf("spawn budget: %s", failure)
	}
}

func TestValidateActionRefusesWhatItDoesNotTake(t *testing.T) {
	for params, want := range map[string]string{
		`{}`: "validate requires a non-empty workflowScript or workflowScriptPath.",
		`{"workflowScript":"return 1","workflowScriptPath":"x.js"}`: "workflowScript and workflowScriptPath are mutually exclusive.",
		`{"workflowScript":"return 1","agent":"helper"}`:            "validate requires workflowScript or workflowScriptPath and does not accept direct agent, task, or step execution fields.",
		`{"workflowScript":"return 1","globalConcurrencyLimit":2}`:  "Workflow capacity overrides are only supported on top-level workflowScript or workflowScriptPath calls; validate accepts maxSubagentSpawnsPerRun for static budget checks.",
		`{"workflowScript":"return 1","maxSubagentSpawnsPerRun":0}`: "maxSubagentSpawnsPerRun must be a positive safe integer.",
		`{"workflowScriptPath":"missing.js"}`:                       "Failed to read workflowScriptPath '",
	} {
		var p map[string]any
		_ = json.Unmarshal([]byte(params), &p)
		_, _, failure := validateResult(t, p)
		if !strings.Contains(failure, want) {
			t.Errorf("%s: %q, want %q", params, failure, want)
		}
	}
}

func TestValidateActionReadsAScriptFile(t *testing.T) {
	h, cwd := validateHost(t)
	writeFile(t, filepath.Join(cwd, "flow.js"), `return await runs.run("a", { agent: "helper", task: "x" });`)
	raw, failure := h.Tool("subagent", map[string]any{"action": "validate", "workflowScriptPath": "flow.js"})
	eq(t, failure, "", "script file")
	eq(t, toolText(t, raw), `{"ok":true,"errors":[]}`, "script file answer")
	writeFile(t, filepath.Join(cwd, "empty.js"), "  \n")
	_, failure = h.Tool("subagent", map[string]any{"action": "validate", "workflowScriptPath": "empty.js"})
	if !strings.Contains(failure, "empty.js' is empty.") {
		t.Errorf("empty file: %s", failure)
	}
}

func TestValidateActionAnswersWarningsAndUnreadableFiles(t *testing.T) {
	_, text, failure := validateResult(t, map[string]any{"workflowScript": "const keys = [\"a\",\"b\"];\nawait runs.all(keys.map((k) => ({ key: k, agent: \"helper\", task: \"x\" })));\nreturn 1;", "maxSubagentSpawnsPerRun": 5})
	eq(t, failure, "", "a valid script with a dynamic fan-out")
	eq(t, text, `{"ok":true,"errors":[],"warnings":[{"kind":"dynamic-spawn-count","message":"workflowScript contains dynamic child launches; static validation proved 0 launch(es), so runtime fan-out enforcement remains authoritative for the configured budget of 5."}]}`, "warnings answer")

	h, cwd := validateHost(t)
	_, failure = h.Tool("subagent", map[string]any{"action": "validate", "workflowScriptPath": "missing.js"})
	path := filepath.Join(cwd, "missing.js")
	eq(t, failure, "Failed to read workflowScriptPath '"+path+"': ENOENT: no such file or directory, open '"+path+"'", "a missing file")
}
