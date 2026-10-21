package pi_subagents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func parseDelegation(t *testing.T, overrides string) (*jsObject, *delegationParseFailure) {
	t.Helper()
	return parseSubagentDelegationRequest(structuredRequest(t, overrides))
}

func TestDelegationRequestLimits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	_, failure := parseDelegation(t, `{"ownerRunId":"`+long(256)+`"}`)
	eq(t, failure == nil, true, "a 256 character identity is valid")
	_, failure = parseDelegation(t, `{"ownerRunId":"`+long(257)+`"}`)
	if failure == nil || !strings.HasPrefix(failure.Error, "Delegation ownerRunId must be a non-empty string of at most 256 characters") {
		t.Errorf("a 257 character identity: %+v", failure)
	}
	_, failure = parseDelegation(t, `{"bogus":1}`)
	if failure == nil {
		t.Fatal("an unknown field was accepted")
	}
	eq(t, failure.Error, "Unsupported delegation field: bogus.", "unsupported field")
	_, failure = parseDelegation(t, `{"timeoutMs":2147483647}`)
	eq(t, failure == nil, true, "the largest timeout")
	_, failure = parseDelegation(t, `{"timeoutMs":2147483648}`)
	if failure == nil {
		t.Fatal("a timeout over the maximum was accepted")
	}
	eq(t, failure.Error, "timeoutMs must be <= 2147483647.", "timeout maximum")
	big := `{"result":{"kind":"structured","schema":{"description":"` + long(70000) + `"}}}`
	_, failure = parseDelegation(t, big)
	if failure == nil {
		t.Fatal("a schema over 64 KiB was accepted")
	}
	eq(t, failure.Error, "result.schema exceeds 64 KiB when encoded.", "schema size")
	_, failure = parseDelegation(t, `{"result":{"kind":"structured","schema":{"type":"object"}}}`)
	eq(t, failure == nil, true, "a small schema")
}

func TestDelegationRefusesASecondRunOfTheSameNode(t *testing.T) {
	events := &fakeDelegationEvents{}
	release := make(chan struct{})
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return &delegationContext{Cwd: "/repo"} }, func(_ string, _ *jsObject, done <-chan struct{}, _ any, _ func(*jsObject)) (*jsObject, error) {
		select {
		case <-done:
		case <-release:
		}
		return js(t, `{"details":{"results":[{"finalOutput":"ok","exitCode":0}]}}`).(*jsObject), nil
	})
	defer bridge.dispose()
	defer close(release)
	started := events.once(delegationStartedEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"first"}`))
	recv(t, started)
	response := events.once(delegationResponseEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, `{"requestId":"second"}`))
	r := recv(t, response)
	eq(t, r.vals["requestId"], any("second"), "the second request is the one refused")
	eq(t, r.vals["status"], any("duplicate_node"), "status")
}

func TestDelegationUpdatesSkipAHeartbeat(t *testing.T) {
	events := &fakeDelegationEvents{}
	bridge := registerPromptTemplateDelegationBridge(events, func() any { return &delegationContext{Cwd: "/repo"} }, func(_ string, _ *jsObject, _ <-chan struct{}, _ any, onUpdate func(*jsObject)) (*jsObject, error) {
		progress := func(tools, ms int) *jsObject {
			return obj("details", obj("progress", []any{obj("index", float64(0), "agent", "worker", "toolCount", float64(tools), "durationMs", float64(ms))}))
		}
		onUpdate(progress(1, 10))
		onUpdate(progress(1, 20)) // only the duration moved: a heartbeat
		onUpdate(progress(2, 30))
		return js(t, `{"details":{"results":[{"finalOutput":"ok","exitCode":0}]}}`).(*jsObject), nil
	})
	defer bridge.dispose()
	var tools []any
	events.On(delegationUpdateEvent, func(data any) { tools = append(tools, data.(*jsObject).vals["toolCount"]) })
	response := events.once(delegationResponseEvent)
	events.Emit(delegationRequestEvent, structuredRequest(t, ""))
	recv(t, response)
	bridge.wg.Wait()
	eq(t, tools, []any{float64(1), float64(2)}, "a duration alone is not an update")
}

func TestRunFanoutAnnotation(t *testing.T) {
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	fan, err := claimRunFanout("run-annotate")
	if err != nil {
		t.Fatal(err)
	}
	res, err := fan.annotate(sdk.ToolResult{Content: "body", IsError: true, Details: obj("mode", "single")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.(sdk.ToolResult)
	eq(t, tr.Content, "Run fan-out: 1/64 used, 63 remaining\nbody", "a failed result keeps its text behind the budget line")
	eq(t, marshalJSON(tr.Details, ""), `{"mode":"single","runFanoutBudget":{"used":1,"limit":64,"remaining":63}}`, "the budget is in the details of a failed result")
	_, err = fan.annotate(nil, errCancelled)
	eq(t, errors.Is(err, errCancelled) && err.Error() == errCancelled.Error(), true, "a cancelled run is not annotated")
	_, err = fan.annotate(nil, errors.New("boom"))
	eq(t, err.Error(), "Run fan-out: 1/64 used, 63 remaining\nboom", "a failure carries the line")
	var broken runFanout
	same, err := broken.annotate(explicitOK{Content: "c"}, nil)
	eq(t, same, any(explicitOK{Content: "c"}), "a budget that cannot be read leaves the result as it is")
	eq(t, err == nil, true, "and no error")
}

func TestRunFanoutClaimFailureIsReportedAsIs(t *testing.T) {
	withTempHome(t)
	temp := tmp(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", temp)
	if err := os.WriteFile(filepath.Join(temp, "run-fanout-budgets"), []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := claimRunFanout("run-claim"); err == nil {
		t.Fatal("a budget directory that cannot be created is a failed claim")
	}
}

func TestStatusBudgetReconcilesAndNamesAnUnlimitedCapacity(t *testing.T) {
	withTempHome(t)
	temp := tmp(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", temp)
	root := filepath.Join(temp, "session-active-async-capacity")
	workflowDir := filepath.Join(temp, "runs", "workflow")
	wf := mustAcquire(t, "session-a", lim(1), "workflow", "workflow", workflowDir, CapacityOptions{RootDir: root})
	_ = wf.MarkWorkflowStarted()
	capWriteJSON(t, filepath.Join(workflowDir, "status.json"), map[string]any{"runId": "workflow", "sessionId": "session-a", "mode": "workflow", "state": "failed", "startedAt": 100,
		"steps": []any{map[string]any{"agent": "worker", "workflowKey": "prep-failure", "status": "failed", "async": false, "error": "gate cannot be combined with acceptance"}}})
	lines, _, capacity := statusBudget("session-a")
	if !strings.HasSuffix(lines, "\nActive async capacity: 0/unlimited used") {
		t.Errorf("lines: %q", lines)
	}
	eq(t, capacity.vals["used"], any(float64(0)), "the slot of the finished workflow was released by the status")
	eq(t, capacity.vals["limit"], any(float64(0)), "no limit")
}

func TestAsyncStartedGuidanceOfAnInteractiveAndAHeadlessSession(t *testing.T) {
	polMatches(t, formatAsyncStartedMessage("H", true), `You are in an interactive session`)
	headless := formatAsyncStartedMessage("H", false)
	polMatches(t, headless, `This is a non-interactive run: Pi auto-drains`)
	polNotMatches(t, headless, `You are in an interactive session`)
}

func TestLaunchBindingCarriesTheTaskAsADigest(t *testing.T) {
	task := "do the work"
	got := projectLaunchBinding(launchBindingInput{Task: &task})
	eq(t, got["taskDigest"], any(stableJSONDigest(task)), "the digest of the task")
	_, has := projectLaunchBinding(launchBindingInput{})["taskDigest"]
	eq(t, has, false, "no task, no digest")
}

func TestExtensionBindingsCountEveryProperty(t *testing.T) {
	object := func(n int) map[string]any {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[strings.Repeat("k", 1)+string(rune('A'+i/26))+string(rune('a'+i%26))] = float64(i)
		}
		return m
	}
	n := 0
	if _, err := canonicalizeBindingJSON(object(maxExtensionBindingsProperties), "x", 0, map[uintptr]bool{}, &n); err != nil {
		t.Errorf("exactly the maximum: %v", err)
	}
	n = 0
	_, err := canonicalizeBindingJSON(object(maxExtensionBindingsProperties+1), "x", 0, map[uintptr]bool{}, &n)
	if err == nil || err.Error() != "extensionBindings exceeds 256 total properties." {
		t.Errorf("one over: %v", err)
	}
}

func TestRefinementFileMetadataAndOverlay(t *testing.T) {
	withTempHome(t)
	dir := tmp(t)
	file := func(source, current string) *refinementFile {
		return &refinementFile{Agent: "worker", Revision: 1, UpdatedAt: "2026-01-01T00:00:00.000Z", BaseSource: source, BaseFile: "/x/worker.md", BaseSHA: "abc",
			MaxItems: 8, MaxAgeDays: 30, ItemBytes: 1000, TotalBytes: 8000, Current: current}
	}
	path, err := getAgentRefinementPath(dir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRefinementFile(path, file("nowhere", "guidance")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	_, err = parseRefinementFile(string(b), "refinement")
	hasMatch(t, err, `refinement base source is invalid`)
	eq(t, appendAgentRefinementOverlay("Base", dir, "worker"), "Base", "an invalid file adds nothing")
	if err := writeRefinementFile(path, file("project", "  \n ")); err != nil {
		t.Fatal(err)
	}
	eq(t, appendAgentRefinementOverlay("Base", dir, "worker"), "Base", "a blank current block adds nothing")
	if err := writeRefinementFile(path, file("project", "guidance")); err != nil {
		t.Fatal(err)
	}
	polMatches(t, appendAgentRefinementOverlay("Base", dir, "worker"), `guidance`)
}
