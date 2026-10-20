package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/unit/workflow-terminal-proof.test.ts.
const terminalProofFile = "workflow-terminal-proof"

func proofSummary(mod func(*WorkflowChildSummary)) WorkflowChildSummary {
	s := WorkflowChildSummary{Version: 1, ParentToolCallID: "parent-tool-call", WorkflowRunID: "workflow-run", InventoryComplete: true, WorkflowState: "completed",
		Children: []WorkflowChildSummaryRow{{ChildID: "main", RunID: "child-run", State: "completed"}}}
	if mod != nil {
		mod(&s)
	}
	return s
}

func asyncChildStep() []AsyncStep {
	yes := true
	return []AsyncStep{{Agent: "worker", WorkflowKey: "main", RunID: "child-run", Async: &yes, Status: "completed"}}
}

func observedChildProof() *ProcessTerminal {
	at := ms(1234)
	zero := 0
	return &ProcessTerminal{Version: 1, State: "observed", RunID: "child-run", RunnerProcessInstanceID: "runner-1", ObservedAt: &at,
		Instances: []ProcessInstanceExit{{Kind: "runner", ProcessInstanceID: "runner-1", CloseObservedAt: 1234, ExitCode: &zero}}}
}

// proofFixture is the original's withFixture: a workflow directory beside the child's.
func proofFixture(t *testing.T) (asyncDir string, writeChild func(status map[string]any, proof *ProcessTerminal)) {
	root := t.TempDir()
	asyncDir = filepath.Join(root, "workflow-run")
	if err := os.MkdirAll(asyncDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeChild = func(status map[string]any, proof *ProcessTerminal) {
		childDir := filepath.Join(root, "child-run")
		st := map[string]any{"runId": "child-run", "mode": "single", "startedAt": 1, "lastUpdate": 2}
		for k, v := range status {
			st[k] = v
		}
		b, _ := json.Marshal(st)
		writeFile(t, filepath.Join(childDir, "status.json"), string(b))
		if proof != nil {
			pb, _ := json.Marshal(proof)
			writeFile(t, filepath.Join(childDir, "process-terminal.json"), string(pb))
		}
	}
	return asyncDir, writeChild
}

var runnerIdentity = func(state string) *ProcessTerminal {
	return &ProcessTerminal{Version: 1, State: state, RunID: "child-run", RunnerProcessInstanceID: "runner-1"}
}

func TestWorkflowTerminalProofTwins(t *testing.T) {
	tw(t, terminalProofFile, "keeps an open inventory pending", func(t *testing.T) {
		asyncDir, _ := proofFixture(t)
		got := readWorkflowTerminalProof(asyncDir, asyncChildStep(), proofSummary(func(s *WorkflowChildSummary) { s.InventoryComplete = false; s.WorkflowState = "running" }), 0, 2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "pending", DispatchClosed: false, Reason: "Workflow dispatch is still open."}, "proof")
	})
	tw(t, terminalProofFile, "keeps closed dispatch pending until the child's exit is observed", func(t *testing.T) {
		asyncDir, writeChild := proofFixture(t)
		writeChild(map[string]any{"state": "complete", "processTerminal": runnerIdentity("pending")}, nil)
		got := readWorkflowTerminalProof(asyncDir, asyncChildStep(), proofSummary(nil), 0, 2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "pending", DispatchClosed: true, Reason: "async workflow child main process-terminal proof is missing"}, "proof")
	})
	tw(t, terminalProofFile, "does not accept a missing child roster as an empty workflow", func(t *testing.T) {
		asyncDir, _ := proofFixture(t)
		got := readWorkflowTerminalProof(asyncDir, nil, proofSummary(nil), 0, 2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "unknown", DispatchClosed: true, Reason: "workflow child roster is missing"}, "proof")
		eq(t, readWorkflowTerminalProof(asyncDir, []AsyncStep{}, proofSummary(func(s *WorkflowChildSummary) { s.Children = nil }), 0, 2000).State, "observed", "empty roster")
	})
	tw(t, terminalProofFile, "returns observed after every async child has writer-exit evidence", func(t *testing.T) {
		asyncDir, writeChild := proofFixture(t)
		proof := observedChildProof()
		writeChild(map[string]any{"state": "complete", "processTerminal": runnerIdentity("pending")}, proof)
		got := readWorkflowTerminalProof(asyncDir, asyncChildStep(), proofSummary(func(s *WorkflowChildSummary) { s.WorkflowState = "stopped" }), 0, 1000)
		at := ms(1234)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "observed", DispatchClosed: true, ObservedAt: &at, Children: []*ProcessTerminal{proof}}, "proof")
	})
	tw(t, terminalProofFile, "accepts a child whose runner failed before starting", func(t *testing.T) {
		asyncDir, writeChild := proofFixture(t)
		notStarted := runnerIdentity("not-started")
		writeChild(map[string]any{"state": "failed", "error": "runner failed to start", "processTerminal": notStarted}, nil)
		got := readWorkflowTerminalProof(asyncDir, asyncChildStep(), proofSummary(func(s *WorkflowChildSummary) { s.WorkflowState = "failed" }), 0, 2000)
		at := ms(2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "observed", DispatchClosed: true, ObservedAt: &at, Children: []*ProcessTerminal{notStarted}}, "proof")
	})
	tw(t, terminalProofFile, "needs no process evidence for synchronous children that run inside the host", func(t *testing.T) {
		asyncDir, _ := proofFixture(t)
		no := false
		steps := []AsyncStep{{Agent: "worker", WorkflowKey: "main", Async: &no, Status: "completed"}}
		got := readWorkflowTerminalProof(asyncDir, steps, proofSummary(func(s *WorkflowChildSummary) {
			s.Children = []WorkflowChildSummaryRow{{ChildID: "main", State: "completed"}}
		}), 0, 2000)
		at := ms(2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "observed", DispatchClosed: true, ObservedAt: &at, Children: []*ProcessTerminal{}}, "proof")
	})
	tw(t, terminalProofFile, "reports host commands as unknown", func(t *testing.T) {
		asyncDir, _ := proofFixture(t)
		got := readWorkflowTerminalProof(asyncDir, []AsyncStep{}, proofSummary(func(s *WorkflowChildSummary) { s.Children = nil }), 1, 2000)
		eq(t, got, WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: "workflow-run", State: "unknown", DispatchClosed: true, Reason: "Workflow host commands have no process-terminal proof."}, "proof")
	})
}

// An observed proof with no async children still says `children: []` (the original's JSON).
func TestWorkflowTerminalProofJSON(t *testing.T) {
	asyncDir, _ := proofFixture(t)
	b, err := json.Marshal(readWorkflowTerminalProof(asyncDir, []AsyncStep{}, proofSummary(func(s *WorkflowChildSummary) { s.Children = nil }), 0, 2000))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"version":1,"kind":"workflow","runId":"workflow-run","state":"observed","dispatchClosed":true,"observedAt":2000,"children":[]}` {
		t.Fatalf("json = %s", b)
	}
}
