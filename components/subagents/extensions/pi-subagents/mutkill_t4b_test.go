package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests for the survivors of the mutations of the prompt-template delegation bridge, the validate action and the mission records.

func TestDelegationRequestBounds(t *testing.T) {
	parse := func(overrides *jsObject) *delegationParseFailure {
		req := structuredRequest(t, "")
		for _, k := range overrides.order() {
			req.set(k, overrides.vals[k])
		}
		_, failure := parseSubagentDelegationRequest(req)
		return failure
	}
	eq(t, parse(jo("requestId", strings.Repeat("a", 256))) == nil, true, "256 characters")
	if f := parse(jo("requestId", strings.Repeat("a", 257))); f == nil || !strings.HasPrefix(f.Error, "Delegation requestId must be") {
		t.Errorf("257 characters: %+v", f)
	}
	if f := parse(jo("bogus", float64(1))); f == nil || f.Error != "Unsupported delegation field: bogus." {
		t.Errorf("unsupported field: %+v", f)
	}
	eq(t, parse(jo("timeoutMs", float64(2147483647))) == nil, true, "the largest timeout")
	if f := parse(jo("timeoutMs", float64(2147483648))); f == nil || f.Error != "timeoutMs must be <= 2147483647." {
		t.Errorf("timeout over the maximum: %+v", f)
	}
	big := jo("kind", "structured", "schema", jo("type", "object", "description", strings.Repeat("x", 70*1024)))
	if f := parse(jo("result", big)); f == nil || f.Error != "result.schema exceeds 64 KiB when encoded." {
		t.Errorf("schema over 64 KiB: %+v", f)
	}
	small := jo("kind", "structured", "schema", jo("type", "object", "description", strings.Repeat("x", 60*1024)))
	eq(t, parse(jo("result", small)) == nil, true, "a schema under 64 KiB")
}

func TestDelegationDurationAloneIsAHeartbeat(t *testing.T) {
	a := jo("currentTool", "read", "durationMs", float64(1))
	eq(t, sameDelegationProgress(a, jo("currentTool", "read", "durationMs", float64(2))), true, "same progress, later duration")
	eq(t, sameDelegationProgress(a, jo("currentTool", "write", "durationMs", float64(1))), false, "a different tool")
}

func TestDelegationBridgeTerminalOnceAndDuplicateNodes(t *testing.T) {
	events := &fakeDelegationEvents{}
	release := make(chan struct{})
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return &delegationContext{Cwd: "/repo"} },
		func(string, *jsObject, <-chan struct{}, any, func(*jsObject)) (*jsObject, error) {
			<-release
			return js(t, `{"details":{"results":[{"agent":"worker","finalOutput":"ok","exitCode":0}]}}`).(*jsObject), nil
		})
	defer bridge.dispose()
	var statuses []string
	events.On(delegationResponseEvent, func(data any) {
		o, _ := data.(*jsObject)
		s, _ := o.str("status")
		statuses = append(statuses, s)
	})
	bridge.emitTerminal("k", jo("status", "first"))
	bridge.emitTerminal("k", jo("status", "second"))
	eq(t, statuses, []string{"first"}, "one terminal response per attempt")

	started := events.once(delegationStartedEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, ""))
	recv(t, started)
	duplicate := events.once(delegationResponseEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"r2"}`))
	r := recv(t, duplicate)
	eq(t, r.vals["status"], any("duplicate_node"), "a second request for the running node")
	close(release)
}

func TestValidateActionRefusals(t *testing.T) {
	_, err := validateAction(sdk.Context{}, map[string]any{"workflowScript": "return 1;", "workflowScriptPath": "flow.js", "agent": "x"})
	eq(t, err.Error(), "workflowScript and workflowScriptPath are mutually exclusive.", "both script sources")
	_, err = validateAction(sdk.Context{}, map[string]any{"workflowScript": "return 1;", "globalConcurrencyLimit": float64(3), "agent": "x"})
	eq(t, err.Error(), "Workflow capacity overrides are only supported on top-level workflowScript or workflowScriptPath calls; validate accepts maxSubagentSpawnsPerRun for static budget checks.", "capacity override")
}

func TestMissionReceiptUrlAndObjectiveText(t *testing.T) {
	receipt := func(url string) *jsObject {
		return jo("kind", "ci", "status", "ready", "url", url, "title", "T", "createdAt", "2026-01-01T00:00:00.000Z")
	}
	_, err := parseMissionReceipt(receipt("https://example.com/run/1"), "r")
	eq(t, err, nil, "an absolute URL")
	_, err = parseMissionReceipt(receipt("not a url"), "r")
	matchesRe(t, err, `r\.url must be an absolute URL`)
	_, err = parseMissionReceipt(receipt("https://"), "r")
	matchesRe(t, err, `r\.url must be an absolute URL`)

	rec := jo("schemaVersion", float64(1), "id", "m1", "title", "T", "objective", "  O  ", "status", "planned", "createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z",
		"runs", []any{}, "decisions", []any{}, "artifacts", []any{})
	r, err := parseMissionRecord(rec, "r")
	eq(t, err, nil, "record")
	eq(t, r.Objective, "O", "the objective is trimmed")
}

func TestMissionDecisionsMoveAnActiveMissionToNeedsDecision(t *testing.T) {
	f := newMissionFixture(t)
	m := mkMission(t, f.loc, MissionCreateInput{Title: "T", Objective: "O", Status: "active"})
	u := updMission(t, f.loc, m.ID, MissionUpdateInput{AddDecisions: []MissionDecisionInput{{Title: "Pick", Prompt: "Which?", Options: []string{"a", "b"}}}})
	eq(t, u.Status, "needs_decision", "an open decision on an active mission")
	u = updMission(t, f.loc, m.ID, MissionUpdateInput{ResolveDecision: &MissionResolveDecision{ID: u.Decisions[0].ID, Resolution: "  a  "}})
	eq(t, u.Status, "active", "the last open decision resolved")
	eq(t, u.Decisions[0].Resolution, "a", "the resolution is trimmed")
}

func TestPromptChainSplitsOnSpacedArrowsOnly(t *testing.T) {
	eq(t, splitPromptChain("a ->b -> c"), []string{"a ->b", "c"}, "an arrow without a following space stays in the segment")
	eq(t, splitPromptChain(" a ->  -> b "), []string{"a", "b"}, "blank segments are dropped")
}

func TestLaunchBindingTaskDigest(t *testing.T) {
	task := "do the work"
	eq(t, projectLaunchBinding(launchBindingInput{Task: &task})["taskDigest"], any(stableJSONDigest(task)), "the task is carried as a digest")
	_, has := projectLaunchBinding(launchBindingInput{})["taskDigest"]
	eq(t, has, false, "no task, no digest")
}

func TestBindingPropertiesAreBounded(t *testing.T) {
	build := func(n int) map[string]any {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%03d", i)] = float64(i)
		}
		return m
	}
	props := 0
	_, err := canonicalizeBindingJSON(build(maxExtensionBindingsProperties), "b", 0, map[uintptr]bool{}, &props)
	eq(t, err, nil, "exactly the limit")
	props = 0
	_, err = canonicalizeBindingJSON(build(maxExtensionBindingsProperties+1), "b", 0, map[uintptr]bool{}, &props)
	hasMatch(t, err, `extensionBindings exceeds 256 total properties.`)
}

func TestRefinementFileBaseSourceAndBlankCurrent(t *testing.T) {
	file := func(source, current string) *refinementFile {
		f := newRefinementFile(&AgentConfig{Name: "worker", Source: AgentSource(source), FilePath: "/x/worker.md", SystemPrompt: "p"}, 1, "2026-01-01T00:00:00.000Z")
		f.Current = current
		return f
	}
	_, err := parseRefinementFile(file("nowhere", "guidance").serialize(), "L")
	hasMatch(t, err, `L base source is invalid.`)
	_, err = parseRefinementFile(file("project", "guidance").serialize(), "L")
	eq(t, err, nil, "a project base")

	withTempHome(t)
	dir := tmp(t)
	path, err := getAgentRefinementPath(dir, "worker")
	eq(t, err, nil, "path")
	eq(t, writeRefinementFile(path, file("project", "   ")), nil, "write")
	eq(t, appendAgentRefinementOverlay("Base.", dir, "worker"), "Base.", "a blank refinement adds no overlay")
	eq(t, writeRefinementFile(path, file("project", "  Be brief.  ")), nil, "write")
	if got := appendAgentRefinementOverlay("Base.", dir, "worker"); !strings.Contains(got, "\nBe brief.\n") {
		t.Errorf("overlay: %q", got)
	}
}

func TestStatusBudgetReconcilesTheSessionAndNamesUnlimitedCapacity(t *testing.T) {
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	asyncDir := filepath.Join(tmp(t), "run-a")
	h := mustAcquire(t, "session-bs", lim(3), "run-a", "runner", asyncDir, CapacityOptions{})
	eq(t, h.MarkStarted("runner-a"), nil, "mark")
	lines, _, _ := statusBudget("session-bs")
	hasRe(t, lines, `Active async capacity: 1/unlimited used`)
	capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-bs", "mode": "single", "state": "complete", "startedAt": 100})
	capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
	lines, _, _ = statusBudget("session-bs")
	hasRe(t, lines, `Active async capacity: 0/unlimited used`)
}

func TestValidateResultKeyOrderAndMissingScriptText(t *testing.T) {
	out := validationObject(WorkflowScriptValidationResult{OK: true, Warnings: []WorkflowScriptValidationWarning{{Kind: "dynamic-spawn-count", Message: "m"}}}, nil, "")
	eq(t, marshalJSON(out, ""), `{"ok":true,"errors":[],"warnings":[{"kind":"dynamic-spawn-count","message":"m"}]}`, "warning keys")
	dir := tmp(t)
	_, text := loadWorkflowScriptPath(dir, map[string]any{"workflowScriptPath": "missing.js"})
	want := filepath.Join(dir, "missing.js")
	eq(t, text, "Failed to read workflowScriptPath '"+want+"': ENOENT: no such file or directory, open '"+want+"'", "a missing script")
}

func newTestFanout(t *testing.T) *runFanout {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	f, err := claimRunFanout("run-x")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunFanoutAnnotatesResultsAndKeepsCancellationAndLostBudgets(t *testing.T) {
	f := newTestFanout(t)
	res, err := f.annotate(sdk.ToolResult{Content: "body", Details: map[string]any{}}, nil)
	eq(t, err, nil, "annotate")
	tr := res.(sdk.ToolResult)
	if !strings.HasPrefix(tr.Content, "Run fan-out: 1/") || !strings.HasSuffix(tr.Content, "\nbody") {
		t.Errorf("content: %q", tr.Content)
	}
	eq(t, tr.Details.(map[string]any)["runFanoutBudget"].(*jsObject).vals["used"], any(float64(1)), "details")

	_, err = f.annotate(nil, errCancelled)
	eq(t, err == errCancelled, true, "a cancelled run keeps its own error")

	eq(t, os.RemoveAll(f.desc.Directory), nil, "the budget is gone")
	_, err = f.annotate(nil, errors.New("boom"))
	eq(t, err.Error(), "boom", "without a readable budget the error stays as it is")
}
