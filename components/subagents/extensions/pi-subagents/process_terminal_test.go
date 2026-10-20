package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of process-terminal (runs/background/process-terminal.ts and its candidate sidecar).

func ptWriter(id string, group int, exit int) ProcessInstanceExit {
	attempt := 0
	return ProcessInstanceExit{ProcessInstanceID: id, Kind: "pi-writer", Attempt: &attempt, CloseObservedAt: 10, ExitCode: &exit,
		ProcessTree: &ProcessTreeProof{State: "observed", Mechanism: "posix-process-group", ProcessGroupID: group, VerifiedAt: 11}}
}

func ptClose(runner string, at int64, exit int) RunnerClose {
	return RunnerClose{ProcessInstanceID: runner, CloseObservedAt: at, ExitCode: &exit}
}

func ptStatus(t *testing.T, dir, runID, state, stepStatus string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"runId": runID, "state": state, "lifecycleArtifactVersion": 3, "steps": []map[string]any{{"agent": "worker", "status": stepStatus}}})
	writeFile(t, filepath.Join(dir, "status.json"), string(b))
	writeFile(t, filepath.Join(dir, "events.jsonl"), "")
}

func ptStatusProof(t *testing.T, dir string) map[string]any {
	t.Helper()
	var st struct {
		ProcessTerminal map[string]any `json:"processTerminal"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "status.json"))
	_ = json.Unmarshal(b, &st)
	return st.ProcessTerminal
}

func TestProcessTerminalTwins(t *testing.T) {
	const f = "process-terminal"
	tw(t, f, "process-terminal proof requires the matching runner instance and writer close records", func(t *testing.T) {
		dir := tmp(t)
		if err := writeProcessTerminalCandidate(dir, &ProcessTerminalCandidate{Version: 1, RunID: "run-1", RunnerProcessInstanceID: "runner-1",
			ExpectedWriters: map[string]int{"0": 1}, Writers: map[string][]ProcessInstanceExit{"0": {ptWriter("writer-1", 123, 0)}}}); err != nil {
			t.Fatal(err)
		}
		ptStatus(t, dir, "run-1", "complete", "complete")
		mismatch := finalizeProcessTerminal(dir, "run-1", ptClose("runner-2", 20, 0))
		eq(t, mismatch.State, "unknown", "mismatch state")
		eq(t, mismatch.Reason, "runner-instance-mismatch", "mismatch reason")
		_ = os.Remove(processTerminalPath(dir))
		observed := finalizeProcessTerminal(dir, "run-1", ptClose("runner-1", 30, 0))
		eq(t, observed.State, "observed", "state")
		eq(t, len(observed.Instances), 2, "instances")
		eq(t, ptStatusProof(t, dir)["state"], "observed", "status.json overlay")
	})
	tw(t, f, "startup process-terminal candidate does not certify unobserved writers", func(t *testing.T) {
		dir := tmp(t)
		if err := initializeProcessTerminal(dir, "run-startup", "runner-startup"); err != nil {
			t.Fatal(err)
		}
		ptStatus(t, dir, "run-startup", "running", "running")
		proof := finalizeProcessTerminal(dir, "run-startup", ptClose("runner-startup", 20, 1))
		eq(t, proof.State, "unknown", "state")
		eq(t, proof.Reason, "writer-close-unverified", "reason")
	})
	tw(t, f, "process-terminal rejects missing writer close evidence", func(t *testing.T) {
		dir := tmp(t)
		_ = writeProcessTerminalCandidate(dir, &ProcessTerminalCandidate{Version: 1, RunID: "run-missing-writer", RunnerProcessInstanceID: "runner-missing-writer",
			ExpectedWriters: map[string]int{"0": 1}, Writers: map[string][]ProcessInstanceExit{"0": {}}})
		ptStatus(t, dir, "run-missing-writer", "complete", "complete")
		proof := finalizeProcessTerminal(dir, "run-missing-writer", ptClose("runner-missing-writer", 40, 1))
		eq(t, proof.State, "unknown", "state")
		eq(t, proof.Reason, "writer-close-unverified", "reason")
	})
	tw(t, f, "process-terminal fails closed when process-tree verification is unknown", func(t *testing.T) {
		root := tmp(t)
		dir := filepath.Join(root, "run-tree-unknown")
		sig, one := "SIGKILL", 1
		attempt := 0
		w := ProcessInstanceExit{ProcessInstanceID: "writer-tree-unknown", Kind: "pi-writer", Attempt: &attempt, CloseObservedAt: 10, ExitCode: &one, Signal: &sig,
			ProcessTree: &ProcessTreeProof{State: "unknown", Reason: "verification-failed", Diagnostic: "still live"}}
		_ = writeProcessTerminalCandidate(dir, &ProcessTerminalCandidate{Version: 1, RunID: "run-tree-unknown", RunnerProcessInstanceID: "runner-tree-unknown",
			ExpectedWriters: map[string]int{"0": 1}, Writers: map[string][]ProcessInstanceExit{"0": {w}}})
		ptStatus(t, dir, "run-tree-unknown", "failed", "failed")
		if err := updateActiveRunIndex(dir, "running", "", indexOptions{}); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(root, activeRunIndexDir, "run-tree-unknown")
		proof := finalizeProcessTerminal(dir, "run-tree-unknown", ptClose("runner-tree-unknown", 20, 1))
		eq(t, proof.State, "unknown", "state")
		eq(t, proof.Reason, "process-tree-unverified", "reason")
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("the active marker must stay: %v", err)
		}
		_ = os.Remove(processTerminalPath(dir))
		cand, _ := readProcessTerminalCandidate(dir)
		cand.Writers["0"][0].ProcessTree = &ProcessTreeProof{State: "observed", Mechanism: "posix-process-group", ProcessGroupID: 126, VerifiedAt: 21}
		_ = writeProcessTerminalCandidate(dir, cand)
		observed := finalizeProcessTerminal(dir, "run-tree-unknown", ptClose("runner-tree-unknown", 30, 1))
		eq(t, observed.State, "observed", "observed once the tree is verified")
		if _, err := os.Stat(marker); err == nil {
			t.Error("the active marker must be released")
		}
	})
	tw(t, f, "malformed process-terminal sidecars project unknown instead of throwing", func(t *testing.T) {
		dir := tmp(t)
		writeFile(t, processTerminalPath(dir), `{"version":1,"state":"bogus"}`)
		proof := readProcessTerminal(dir, "malformed-run", "malformed-runner")
		eq(t, proof.State, "unknown", "state")
		eq(t, proof.Reason, "proof-write-failed", "reason")
		if !strings.Contains(proof.Diagnostic, "Invalid process-terminal proof") {
			t.Errorf("diagnostic %q", proof.Diagnostic)
		}
		writeFile(t, filepath.Join(dir, "events.jsonl"), "")
		eq(t, finalizeProcessTerminal(dir, "malformed-run", ptClose("malformed-runner", 10, 1)).State, "unknown", "finalized")
	})
	tw(t, f, "process-terminal preserves stopped non-resumability and requires lease release acknowledgement", func(t *testing.T) {
		dir := tmp(t)
		_ = writeProcessTerminalCandidate(dir, &ProcessTerminalCandidate{Version: 1, RunID: "stopped-run", RunnerProcessInstanceID: "stopped-runner",
			ExpectedWriters: map[string]int{"0": 1}, Writers: map[string][]ProcessInstanceExit{"0": {ptWriter("stopped-writer", 124, 0)}},
			RevivalLeaseToken: "lease-token", RevivalLeaseReleaseAcknowledged: new(false)})
		ptStatus(t, dir, "stopped-run", "stopped", "stopped")
		unverified := finalizeProcessTerminal(dir, "stopped-run", ptClose("stopped-runner", 20, 0))
		eq(t, unverified.State, "unknown", "state")
		eq(t, unverified.Reason, "canonical-session-release-unverified", "reason")
		_ = os.Remove(processTerminalPath(dir))
		cand, _ := readProcessTerminalCandidate(dir)
		cand.RevivalLeaseReleaseAcknowledged = new(true)
		cand.RevivalLeaseToken = ""
		_ = writeProcessTerminalCandidate(dir, cand)
		observed := finalizeProcessTerminal(dir, "stopped-run", ptClose("stopped-runner", 30, 0))
		eq(t, observed.State, "observed", "state")
		eq(t, observed.ResumeDisposition, "non-resumable", "stopped stays non-resumable")
	})
	tw(t, f, "process-terminal rejects cross-run observed sidecars and inconsistent expected writer maps", func(t *testing.T) {
		dir := tmp(t)
		writeFile(t, processTerminalPath(dir), `{"version":1,"state":"observed","runId":"wrong-run","runnerProcessInstanceId":"runner-1","observedAt":10,"instances":[{"processInstanceId":"runner-1","kind":"runner","closeObservedAt":10,"exitCode":0,"signal":null}]}`)
		cross := finalizeProcessTerminal(dir, "actual-run", ptClose("runner-1", 20, 0))
		eq(t, cross.State, "unknown", "state")
		eq(t, cross.Reason, "proof-write-failed", "reason")
		if !strings.Contains(cross.Diagnostic, "wrong-run") && !strings.Contains(cross.Diagnostic, "expected") {
			t.Errorf("diagnostic %q", cross.Diagnostic)
		}
		_ = os.Remove(processTerminalPath(dir))
		_ = writeProcessTerminalCandidate(dir, &ProcessTerminalCandidate{Version: 1, RunID: "actual-run", RunnerProcessInstanceID: "runner-1",
			ExpectedWriters: map[string]int{"0": 0}, Writers: map[string][]ProcessInstanceExit{"0": {ptWriter("unexpected-writer", 125, 0)}}})
		ptStatus(t, dir, "actual-run", "complete", "complete")
		inconsistent := finalizeProcessTerminal(dir, "actual-run", ptClose("runner-1", 30, 0))
		eq(t, inconsistent.State, "unknown", "state")
		eq(t, inconsistent.Reason, "writer-close-unverified", "reason")
	})
	tw(t, f, "process-terminal sanitizes malformed status fallback proofs", func(t *testing.T) {
		eq(t, readProcessTerminal("/no/such/dir", "run-fallback", "runner-fallback") == nil, true, "a missing sidecar reads as nothing")
		sanitized := sanitizeProcessTerminal(&ProcessTerminal{Version: 1, State: "bogus", RunID: "run-fallback", RunnerProcessInstanceID: "runner-fallback"}, "run-fallback", "runner-fallback", "status.json")
		eq(t, sanitized.State, "unknown", "state")
		eq(t, sanitized.Reason, "proof-write-failed", "reason")
	})
	tw(t, f, "process-terminal reports unknown when the runner candidate is unavailable", func(t *testing.T) {
		dir := tmp(t)
		proof := finalizeProcessTerminal(dir, "run-2", ptClose("runner-2", 40, 1))
		b, _ := json.Marshal(proof)
		eq(t, string(b), `{"version":1,"state":"unknown","runId":"run-2","runnerProcessInstanceId":"runner-2","reason":"runner-candidate-missing","instances":[{"kind":"runner","processInstanceId":"runner-2","closeObservedAt":40,"exitCode":1,"signal":null}]}`, "proof")
	})
	tw(t, f, "process-terminal adds the observed runner exit to a sticky runner-published unknown proof", func(t *testing.T) {
		dir := tmp(t)
		sticky := `{"version":1,"state":"unknown","runId":"run-3","runnerProcessInstanceId":"runner-3","reason":"process-tree-unverified","diagnostic":"Worktree setup settlement unknown"}`
		writeFile(t, filepath.Join(dir, "process-terminal.json"), sticky)
		sig := "SIGKILL"
		exit := RunnerClose{ProcessInstanceID: "runner-3", CloseObservedAt: 50, Signal: &sig}
		want := `{"version":1,"state":"unknown","runId":"run-3","runnerProcessInstanceId":"runner-3","reason":"process-tree-unverified","diagnostic":"Worktree setup settlement unknown","instances":[{"kind":"runner","processInstanceId":"runner-3","closeObservedAt":50,"exitCode":null,"signal":"SIGKILL"}]}`
		got, _ := json.Marshal(finalizeProcessTerminal(dir, "run-3", exit))
		eq(t, string(got), want, "finalized")
		read, _ := json.Marshal(readProcessTerminal(dir, "run-3", "runner-3"))
		eq(t, string(read), want, "read back")
	})
}
