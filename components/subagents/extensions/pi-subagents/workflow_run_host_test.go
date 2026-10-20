package pi_subagents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A workflow script runs real children: the Launcher wired to the single-run executor (a fake child runner and a real child
// process) and to the background engine, foreground and async.

func workflowEnv(t *testing.T) (cwd string, h *Host) {
	t.Helper()
	cwd = tmp(t)
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h = StartHost(t, Extension(), HostOptions{Cwd: cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		switch method {
		case "getSessionFile":
			return map[string]any{"sessionFile": "/sessions/parent-a.jsonl"}, ""
		case "getSessionID":
			return map[string]any{"sessionId": "parent-a"}, ""
		}
		return map[string]any{}, ""
	}})
	return cwd, h
}

func toolDetails(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var r struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return r.Details
}

const twoChildScript = `const [a, b] = await runs.all([
  { key: "a", agent: "helper", task: "alpha" },
  { key: "b", agent: "helper", task: "beta" },
]);
return { a: a.output, b: b.output, ok: [a.ok, b.ok] };`

func TestForegroundWorkflowRunsTwoParallelChildrenOverTheFakeRunner(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"alpha": "ALPHA DONE", "beta": "BETA DONE"})
	_, h := workflowEnv(t)
	raw, failure := h.Tool("subagent", map[string]any{"workflowScript": twoChildScript, "async": false})
	eq(t, failure, "", "failure")
	text := toolText(t, raw)
	if !strings.HasPrefix(text, "Workflow completed.\n\nReturn:\n") || !strings.Contains(text, `"a": "ALPHA DONE"`) || !strings.Contains(text, `"b": "BETA DONE"`) {
		t.Errorf("text: %s", text)
	}
	if !strings.Contains(text, "- run a: completed") || !strings.Contains(text, "- run b: completed") {
		t.Errorf("trace missing: %s", text)
	}
	eq(t, len(*calls), 2, "two children ran")
	stdins := []string{(*calls)[0].Stdin, (*calls)[1].Stdin}
	if !(contains(stdins, "Task: alpha") && contains(stdins, "Task: beta")) {
		t.Errorf("child tasks: %q", stdins)
	}
	d := toolDetails(t, raw)
	eq(t, d["mode"], "workflow", "mode")
	wf := d["workflow"].(map[string]any)
	eq(t, wf["value"].(map[string]any)["ok"], []any{true, true}, "value")
	if _, ok := wf["receipt"].(map[string]any); !ok {
		t.Errorf("no receipt: %v", wf)
	}
	results := d["results"].([]any)
	eq(t, len(results), 2, "child results")
	wc := d["workflowChildren"].(map[string]any)
	eq(t, wc["inventoryComplete"], true, "inventory")
	eq(t, wc["workflowState"], "completed", "state")
	// the tool streamed its progress before the end
	if len(h.ToolUpdates()) == 0 {
		t.Errorf("no progress update streamed")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestForegroundWorkflowRunsTwoParallelChildrenAsRealProcesses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SUBAGENT_PIG_BINARY", exe)
	t.Setenv("PISUB_HELPER", "1")
	t.Setenv("PISUB_ECHO_STDIN", "1")
	_, h := workflowEnv(t)
	raw, failure := h.Tool("subagent", map[string]any{"workflowScript": twoChildScript, "async": false})
	eq(t, failure, "", "failure")
	text := toolText(t, raw)
	if !strings.Contains(text, `"a": "stdin:Task: alpha"`) || !strings.Contains(text, `"b": "stdin:Task: beta"`) {
		t.Errorf("each real child answered its own task: %s", text)
	}
}

func TestForegroundWorkflowFailureReportsTheChildAndKeepsTheTrace(t *testing.T) {
	fakeChildren(t, nil)
	_, h := workflowEnv(t)
	raw, failure := h.Tool("subagent", map[string]any{"async": false, "workflowScript": `const agent = "ghost"; return await runs.run("scout", { agent, task: "scan", phase: "Check" });`})
	if failure != "" {
		t.Fatalf("a failed workflow is a result, got failure %q", failure)
	}
	text := toolText(t, raw)
	if !strings.HasPrefix(text, "Workflow failed: Error: Run 'scout' failed: Unknown agent 'ghost'.") || !strings.Contains(text, "- run scout: failed") {
		t.Errorf("text: %s", text)
	}
	d := toolDetails(t, raw)
	eq(t, d["workflow"].(map[string]any)["failureKind"], "child", "failure kind")
}

func TestWorkflowOptionsTheChildrenCannotHonorAreRefused(t *testing.T) {
	fakeChildren(t, nil)
	_, h := workflowEnv(t)
	raw, failure := h.Tool("subagent", map[string]any{"async": false, "workflowScript": `return await runs.run("a", { agent: "helper", task: "t", skill: "x" });`})
	eq(t, failure, "", "failure")
	if text := toolText(t, raw); !strings.Contains(text, "Run 'a' failed: "+refusalHead+"the `skill` option"+refusalTail) {
		t.Errorf("text: %s", text)
	}
	_, failure = h.Tool("subagent", map[string]any{"workflowScript": "return 1", "toolBudget": map[string]any{}})
	eq(t, failure, refusalHead+"the `toolBudget` option"+refusalTail, "a top-level option the port does not run")
}

func TestLegacyOrchestrationInputsAreTheOriginalsErrors(t *testing.T) {
	fakeChildren(t, nil)
	_, h := workflowEnv(t)
	for _, p := range []map[string]any{
		{"tasks": []any{map[string]any{"agent": "helper", "task": "t"}}},
		{"chain": []any{map[string]any{"agent": "helper"}}},
		{"parallel": []any{map[string]any{"agent": "helper"}}},
		{"concurrency": 2},
		{"chainDir": "/x"},
	} {
		_, failure := h.Tool("subagent", p)
		eq(t, failure, "Legacy top-level chain and parallel inputs were removed; use workflowScript.", fmt.Sprint(p))
	}
	_, failure := h.Tool("subagent", map[string]any{"chainName": "x"})
	eq(t, failure, "Durable chain management was removed; use workflowScript or /prompt-workflow for repeatable workflows.", "chainName")
	_, failure = h.Tool("subagent", map[string]any{"action": "list", "chain": []any{}})
	eq(t, failure, "Legacy top-level chain and parallel inputs were removed; use workflowScript.", "with an action")
}

func TestValidationFailureNeverStartsAChild(t *testing.T) {
	calls := fakeChildren(t, nil)
	_, h := workflowEnv(t)
	_, failure := h.Tool("subagent", map[string]any{"async": false, "workflowScript": `return await runs.run("a", { agent: "ghost-literal", task: "t" });`})
	if !strings.Contains(failure, `"ok":false`) || !strings.Contains(failure, "Unknown agent 'ghost-literal'") {
		t.Errorf("failure: %s", failure)
	}
	eq(t, len(*calls), 0, "no child")
}

var asyncWorkflowPattern = regexp.MustCompile(`Async workflow \[([0-9a-f-]{36})\]`)

func TestAsyncWorkflowRunsInTheBackgroundAndPublishesItsEnd(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"alpha": "ALPHA DONE", "beta": "BETA DONE"})
	cwd, h := workflowEnv(t)
	raw, failure := h.Tool("subagent", map[string]any{"workflowScript": twoChildScript})
	eq(t, failure, "", "failure")
	m := asyncWorkflowPattern.FindStringSubmatch(toolText(t, raw))
	if m == nil {
		t.Fatalf("not an async workflow launch: %s", toolText(t, raw))
	}
	id := m[1]
	d := toolDetails(t, raw)
	eq(t, d["asyncId"], id, "asyncId")
	dir := d["asyncDir"].(string)
	eventually(t, "the workflow to complete", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "status.json"))
		var st struct {
			State string `json:"state"`
		}
		return err == nil && json.Unmarshal(b, &st) == nil && st.State == "complete"
	})
	b, _ := os.ReadFile(filepath.Join(dir, "status.json"))
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	eq(t, st["mode"], "workflow", "mode")
	if _, has := st["deadlineAt"]; has {
		t.Errorf("an async workflow has no deadline: %v", st["deadlineAt"])
	}
	steps := st["steps"].([]any)
	eq(t, len(steps), 2, "two steps")
	for _, s := range steps {
		eq(t, s.(map[string]any)["status"], "completed", "step status")
	}
	wf := st["workflow"].(map[string]any)
	eq(t, wf["value"].(map[string]any)["a"], "ALPHA DONE", "value")
	receipt := filepath.Join(dir, "workflow-receipt.json")
	if _, err := os.Stat(receipt); err != nil {
		t.Errorf("no receipt: %v", err)
	}
	if st["workflowReceiptPath"] != receipt {
		t.Errorf("receipt path: %v", st["workflowReceiptPath"])
	}
	var completion string
	eventually(t, "the completion notice", func() bool {
		for _, n := range sentNotices(h) {
			if c, _ := n["content"].(string); strings.HasPrefix(c, "Background task completed: **workflow**") {
				completion = c
			}
		}
		return completion != ""
	})
	if !strings.Contains(completion, "Workflow completed with 2 child run(s). Return:") || !strings.Contains(completion, "Workflow receipt: "+receipt) {
		t.Errorf("completion notice: %q (all: %v)", completion, sentNotices(h))
	}
	eq(t, len(*calls), 2, "two children ran")
	events, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	for _, want := range []string{`"subagent.workflow.started"`, `"subagent.workflow.trace"`, `"subagent.workflow.completed"`} {
		if !strings.Contains(string(events), want) {
			t.Errorf("events.jsonl lacks %s", want)
		}
	}
	// a mission tracked the run
	raw, failure = h.Tool("subagent", map[string]any{"action": "mission.list"})
	eq(t, failure, "", "mission list")
	if !strings.Contains(toolText(t, raw), "completed") {
		t.Errorf("the mission of the workflow did not complete: %s", toolText(t, raw))
	}
	_ = cwd
	_ = time.Second
}

func TestAnExplicitlyAsyncChildReturnsItsLaunchReceipt(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	raw, failure := h.Tool("subagent", map[string]any{"async": false, "workflowScript": `const r = await runs.run("bg", { agent: "helper", task: "build", async: true }); return { ok: r.ok, state: r.state, hasId: typeof r.runId === "string", dir: r.asyncDir };`})
	eq(t, failure, "", "failure")
	text := toolText(t, raw)
	if !strings.Contains(text, `"ok": false`) || !strings.Contains(text, `"state": "running"`) || !strings.Contains(text, `"hasId": true`) {
		t.Errorf("receipt: %s", text)
	}
	if !strings.HasPrefix(text, "Workflow dispatch completed; 1 child run remains running or uncollected: bg=") {
		t.Errorf("head: %s", text)
	}
}
