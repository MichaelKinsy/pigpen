package pi_subagents

import "encoding/json"

// Port of src/runs/background/workflow-terminal-proof.ts: when a persistent workflow host is terminal. The process evidence
// of the async children is the async slice's (readWorkflowChildProcessEvidence), so a status proof and a capacity release
// cannot disagree about the same workflow.

// WorkflowTerminalProof is the proof that a workflow host ended (types.ts WorkflowTerminalProof).
type WorkflowTerminalProof struct {
	Version        int                `json:"version"`
	Kind           string             `json:"kind"`
	RunID          string             `json:"runId"`
	State          string             `json:"state"` // pending, observed, unknown
	DispatchClosed bool               `json:"dispatchClosed"`
	Reason         string             `json:"reason,omitempty"`
	ObservedAt     *ms                `json:"observedAt,omitempty"`
	Children       []*ProcessTerminal `json:"children,omitempty"`
}

// MarshalJSON keeps `children: []` of an observed proof (the original emits it), and omits it from an unresolved one.
func (p WorkflowTerminalProof) MarshalJSON() ([]byte, error) {
	type plain WorkflowTerminalProof
	if p.State != "observed" {
		return json.Marshal(plain(p))
	}
	children := p.Children
	if children == nil {
		children = []*ProcessTerminal{}
	}
	return json.Marshal(struct {
		plain
		Children []*ProcessTerminal `json:"children"`
	}{plain(p), children})
}

func unresolvedWorkflowProof(runID, state string, dispatchClosed bool, reason string) WorkflowTerminalProof {
	return WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: runID, State: state, DispatchClosed: dispatchClosed, Reason: reason}
}

// readWorkflowTerminalProof: a persistent workflow host is terminal only after dispatch closes and every async child has
// process evidence. steps nil is a missing child roster.
func readWorkflowTerminalProof(asyncDir string, steps []AsyncStep, summary WorkflowChildSummary, hostCommandCount int, closedAt int64) WorkflowTerminalProof {
	runID := summary.WorkflowRunID
	terminal := summary.WorkflowState == "completed" || summary.WorkflowState == "failed" || summary.WorkflowState == "stopped"
	if !summary.InventoryComplete || !terminal {
		return unresolvedWorkflowProof(runID, "pending", false, "Workflow dispatch is still open.")
	}
	if hostCommandCount > 0 {
		return unresolvedWorkflowProof(runID, "unknown", true, "Workflow host commands have no process-terminal proof.")
	}
	evidence := readWorkflowChildProcessEvidence(asyncDir, steps)
	if evidence.State != "observed" {
		return unresolvedWorkflowProof(runID, evidence.State, true, evidence.Reason)
	}
	observedAt := closedAt
	children := evidence.Children
	if children == nil {
		children = []*ProcessTerminal{}
	}
	for _, c := range children {
		if c.State == "observed" && c.ObservedAt != nil && int64(*c.ObservedAt) > observedAt {
			observedAt = int64(*c.ObservedAt)
		}
	}
	at := ms(observedAt)
	return WorkflowTerminalProof{Version: 1, Kind: "workflow", RunID: runID, State: "observed", DispatchClosed: true, ObservedAt: &at, Children: children}
}
