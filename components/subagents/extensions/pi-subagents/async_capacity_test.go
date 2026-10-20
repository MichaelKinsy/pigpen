package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Twins of active-async-capacity.test.ts.

func capWriteJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(b))
}

func observedProofJSON(runID, runnerID string) map[string]any {
	return map[string]any{"version": 1, "state": "observed", "runId": runID, "runnerProcessInstanceId": runnerID, "observedAt": 300,
		"instances": []any{map[string]any{"kind": "runner", "processInstanceId": runnerID, "closeObservedAt": 300, "exitCode": 0, "signal": nil}}}
}

func lim(n int) *int { return &n }

func mustAcquire(t *testing.T, session string, limit *int, runID, kind, dir string, o CapacityOptions) *ActiveAsyncCapacityHandle {
	t.Helper()
	h, err := acquireActiveAsyncCapacity(session, limit, runID, kind, dir, o)
	if err != nil {
		t.Fatalf("acquire %s: %v", runID, err)
	}
	if h == nil {
		t.Fatalf("acquire %s: no handle", runID)
	}
	return h
}

func TestActiveAsyncCapacity(t *testing.T) {
	const f = "active-async-capacity"
	tw(t, f, "admits exactly to the limit and rolls back only its own unstarted slot", func(t *testing.T) {
		root := t.TempDir()
		first := mustAcquire(t, "session-a", lim(1), "run-a", "runner", filepath.Join(root, "run-a"), CapacityOptions{RootDir: root, Token: func() string { return "token-a" }})
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "used")
		_, err := acquireActiveAsyncCapacity("session-a", lim(1), "run-b", "runner", filepath.Join(root, "run-b"), CapacityOptions{RootDir: root, Token: func() string { return "token-b" }})
		var ce *ActiveAsyncCapacityError
		eq(t, errors.As(err, &ce), true, "capacity error")
		eq(t, err.Error(), "Active async run capacity exhausted: 1/1 used.", "message")
		eq(t, first.Rollback(), true, "rollback")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "freed")
	})
	tw(t, f, "counts corrupt slot metadata as occupied", func(t *testing.T) {
		root := t.TempDir()
		h := mustAcquire(t, "session-corrupt", lim(1), "run-a", "runner", filepath.Join(root, "run-a"), CapacityOptions{RootDir: root})
		writeFile(t, filepath.Join(root, h.Owner.OwnerSessionKey, "slot-0", "owner.json"), "not json")
		eq(t, getActiveAsyncCapacitySnapshot("session-corrupt", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "still occupied")
		_, err := acquireActiveAsyncCapacity("session-corrupt", lim(1), "run-b", "runner", filepath.Join(root, "run-b"), CapacityOptions{RootDir: root})
		var ce *ActiveAsyncCapacityError
		eq(t, errors.As(err, &ce), true, "refused")
	})
	tw(t, f, "uses the injected clock for reservation and start timestamps", func(t *testing.T) {
		root := t.TempDir()
		now := int64(100)
		o := CapacityOptions{RootDir: root, Now: func() int64 { return now }}
		runner := mustAcquire(t, "session-clock", lim(2), "runner", "runner", filepath.Join(root, "runner"), o)
		eq(t, int64(runner.Owner.ReservedAt), int64(100), "reservedAt")
		now = 200
		eq(t, runner.MarkStarted("runner-process"), nil, "mark")
		eq(t, int64(*runner.Owner.RunnerStartedAt), int64(200), "runnerStartedAt")
		now = 300
		wf := mustAcquire(t, "session-clock", lim(2), "workflow", "workflow", filepath.Join(root, "workflow"), o)
		eq(t, wf.MarkWorkflowStarted(), nil, "workflow mark")
		eq(t, int64(*wf.Owner.RunnerStartedAt), int64(300), "workflow start")
	})
	tw(t, f, "does not roll back when runner start was recorded only in memory", func(t *testing.T) {
		root := t.TempDir()
		h := mustAcquire(t, "session-a", lim(1), "run-a", "runner", filepath.Join(root, "runs", "run-a"), CapacityOptions{RootDir: root})
		h.Owner.RunnerProcessInstanceID = "runner-a"
		h.Owner.RunnerStartedAt = msPtrOf(nowMs())
		eq(t, h.Rollback(), false, "rollback")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "still used")
	})
	tw(t, f, "explains runner release verdicts without mutating slots", func(t *testing.T) {
		root := t.TempDir()
		asyncDir := filepath.Join(root, "runs", "run-a")
		h := mustAcquire(t, "session-a", lim(2), "run-a", "runner", asyncDir, CapacityOptions{RootDir: root})
		eq(t, h.MarkStarted("runner-a"), nil, "mark")
		capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100,
			"processTerminal": map[string]any{"version": 1, "state": "unknown", "runId": "run-a", "runnerProcessInstanceId": "runner-a", "reason": "process-tree-unverified"}})
		retained := inspectActiveAsyncCapacityOwner("run-a", "session-a", asyncDir, CapacityOptions{RootDir: root})
		eq(t, retained.Relation, "current", "relation")
		eq(t, retained.Release.State, "retained", "state")
		mustMatch(t, `process-terminal proof is missing`, retained.Release.Reason)
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(2), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 2}, "unchanged")
		capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
		releasable := inspectActiveAsyncCapacityOwner("run-a", "session-a", asyncDir, CapacityOptions{RootDir: root})
		eq(t, releasable.Release.State, "releasable", "releasable")
		mustMatch(t, `observed process-terminal proof`, releasable.Release.Reason)
		_, err := os.Stat(releasable.SlotDir)
		eq(t, err, nil, "slot kept")
	})
	tw(t, f, "retains active and unknown runner proof, then releases matching observed proof", func(t *testing.T) {
		root := t.TempDir()
		asyncDir := filepath.Join(root, "runs", "run-a")
		h := mustAcquire(t, "session-a", lim(2), "run-a", "runner", asyncDir, CapacityOptions{RootDir: root})
		_ = h.MarkStarted("runner-a")
		o := CapacityOptions{RootDir: root}
		capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-a", "mode": "single", "state": "running", "startedAt": 100,
			"processTerminal": map[string]any{"version": 1, "state": "pending", "runId": "run-a", "runnerProcessInstanceId": "runner-a"}})
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(2), o), ActiveAsyncCapacitySnapshot{1, 2}, "running")
		unknown := map[string]any{"version": 1, "state": "unknown", "runId": "run-a", "runnerProcessInstanceId": "runner-a", "reason": "process-tree-unverified"}
		capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100, "processTerminal": unknown})
		capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), unknown)
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(2), o), ActiveAsyncCapacitySnapshot{1, 2}, "unknown proof")
		capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(2), o), ActiveAsyncCapacitySnapshot{0, 2}, "observed")
	})
	tw(t, f, "does not delete a slot recreated while a terminal owner is released", func(t *testing.T) {
		root := t.TempDir()
		asyncDir := filepath.Join(root, "runs", "run-a")
		h := mustAcquire(t, "session-a", lim(1), "run-a", "runner", asyncDir, CapacityOptions{RootDir: root, Token: func() string { return "token-a" }})
		_ = h.MarkStarted("runner-a")
		capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100})
		capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
		var replacement *ActiveAsyncCapacityHandle
		snapshot := getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root, AfterSlotRename: func(string) {
			replacement = mustAcquire(t, "session-a", lim(1), "run-b", "runner", filepath.Join(root, "runs", "run-b"), CapacityOptions{RootDir: root, Token: func() string { return "token-b" }})
		}})
		eq(t, snapshot, ActiveAsyncCapacitySnapshot{1, 1}, "snapshot")
		eq(t, replacement.Owner.RunID, "run-b", "replacement")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "kept")
	})
	tw(t, f, "does not delete a slot recreated during pre-start rollback", func(t *testing.T) {
		root := t.TempDir()
		first := mustAcquire(t, "session-a", lim(1), "run-a", "runner", filepath.Join(root, "runs", "run-a"), CapacityOptions{RootDir: root, Token: func() string { return "token-a" }, AfterSlotRename: func(string) {
			mustAcquire(t, "session-a", lim(1), "run-b", "runner", filepath.Join(root, "runs", "run-b"), CapacityOptions{RootDir: root, Token: func() string { return "token-b" }})
		}})
		eq(t, first.Rollback(), true, "rollback")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "replacement stays")
		eq(t, first.Rollback(), false, "second rollback")
	})
	tw(t, f, "restores a transferred source when revival fails before runner proceed", func(t *testing.T) {
		root := t.TempDir()
		sourceDir, failedDir := filepath.Join(root, "runs", "source"), filepath.Join(root, "runs", "failed-revival")
		source := mustAcquire(t, "session-a", lim(1), "source", "runner", sourceDir, CapacityOptions{RootDir: root})
		_ = source.MarkStarted("source-runner")
		capWriteJSON(t, filepath.Join(sourceDir, "status.json"), map[string]any{"runId": "source", "sessionId": "session-a", "mode": "single", "state": "paused", "startedAt": 100})
		tr, err := transferActiveAsyncCapacity("session-a", lim(1), "source", "failed-revival", failedDir, CapacityOptions{RootDir: root})
		eq(t, err, nil, "transfer")
		_ = tr.MarkStarted("failed-runner")
		eq(t, tr.RollbackBeforeRunnerProceed("failed-runner"), true, "rollback")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "usage")
		si := inspectActiveAsyncCapacityOwner("source", "session-a", sourceDir, CapacityOptions{RootDir: root})
		eq(t, si.Relation, "current", "relation")
		eq(t, si.Owner.RunID, "source", "owner")
		eq(t, inspectActiveAsyncCapacityOwner("failed-revival", "session-a", failedDir, CapacityOptions{RootDir: root}).Relation, "none", "failed revival")
	})
	tw(t, f, "releases capacity bound before runner proceed when startup is terminated", func(t *testing.T) {
		root := t.TempDir()
		o := CapacityOptions{RootDir: root}
		unbound := mustAcquire(t, "session-a", lim(1), "pre-bind", "runner", filepath.Join(root, "runs", "pre-bind"), o)
		eq(t, unbound.RollbackBeforeRunnerProceed("pre-bind-runner"), false, "unbound")
		eq(t, unbound.Rollback(), true, "unbound rollback")

		h := mustAcquire(t, "session-a", lim(1), "pre-proceed", "runner", filepath.Join(root, "runs", "pre-proceed"), o)
		_ = h.MarkStarted("pre-proceed-runner")
		eq(t, h.Rollback(), false, "started")
		eq(t, h.RollbackBeforeRunnerProceed("other-runner"), false, "other runner")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), o), ActiveAsyncCapacitySnapshot{1, 1}, "kept")
		eq(t, h.RollbackBeforeRunnerProceed("pre-proceed-runner"), true, "own runner")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), o), ActiveAsyncCapacitySnapshot{0, 1}, "released")

		bindFail := mustAcquire(t, "session-a", lim(1), "pre-proceed-bind-failure", "runner", filepath.Join(root, "runs", "pre-proceed-bind-failure"),
			CapacityOptions{RootDir: root, WriteOwner: func(string, *ActiveAsyncCapacityOwner) error { return fmt.Errorf("simulated durable bind failure") }})
		hasMatch(t, bindFail.MarkStarted("pre-proceed-bind-failure-runner"), "simulated durable bind failure")
		eq(t, bindFail.Rollback(), false, "bind failure rollback")
		eq(t, bindFail.RollbackBeforeRunnerProceed("pre-proceed-bind-failure-runner"), true, "bind failure pre-proceed")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), o), ActiveAsyncCapacitySnapshot{0, 1}, "released after bind failure")

		div := mustAcquire(t, "session-a", lim(1), "pre-proceed-divergent", "runner", filepath.Join(root, "runs", "pre-proceed-divergent"), o)
		unstarted := *div.Owner
		_ = div.MarkStarted("pre-proceed-divergent-runner")
		capWriteJSON(t, filepath.Join(root, activeAsyncCapacitySessionKey("session-a"), "slot-0", "owner.json"), unstarted)
		eq(t, div.Rollback(), false, "divergent rollback")
		eq(t, div.RollbackBeforeRunnerProceed("pre-proceed-divergent-runner"), true, "divergent pre-proceed")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), o), ActiveAsyncCapacitySnapshot{0, 1}, "released divergent")
	})
	tw(t, f, "does not restore old source proof after a transferred runner starts", func(t *testing.T) {
		root := t.TempDir()
		sourceDir, revivedDir := filepath.Join(root, "runs", "source"), filepath.Join(root, "runs", "revived")
		source := mustAcquire(t, "session-a", lim(1), "source", "runner", sourceDir, CapacityOptions{RootDir: root})
		_ = source.MarkStarted("source-runner")
		capWriteJSON(t, filepath.Join(sourceDir, "status.json"), map[string]any{"runId": "source", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100})
		capWriteJSON(t, filepath.Join(sourceDir, "process-terminal.json"), observedProofJSON("source", "source-runner"))
		tr, err := transferActiveAsyncCapacity("session-a", lim(1), "source", "revived", revivedDir, CapacityOptions{RootDir: root})
		eq(t, err, nil, "transfer")
		capWriteJSON(t, filepath.Join(revivedDir, "status.json"), map[string]any{"runId": "revived", "sessionId": "session-a", "mode": "single", "state": "failed", "startedAt": 200,
			"processTerminal": map[string]any{"version": 1, "state": "not-started", "runId": "revived", "runnerProcessInstanceId": "revived-runner"}})
		_ = tr.MarkStarted("revived-runner")
		eq(t, tr.Rollback(), false, "rollback")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "kept")
		capWriteJSON(t, filepath.Join(revivedDir, "process-terminal.json"), observedProofJSON("revived", "revived-runner"))
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "released")
	})
	tw(t, f, "reports transferred source runs as not owned", func(t *testing.T) {
		root := t.TempDir()
		sourceDir, nextDir := filepath.Join(root, "runs", "source"), filepath.Join(root, "runs", "next")
		source := mustAcquire(t, "session-a", lim(1), "source", "runner", sourceDir, CapacityOptions{RootDir: root})
		_ = source.MarkStarted("runner-source")
		capWriteJSON(t, filepath.Join(sourceDir, "status.json"), map[string]any{"runId": "source", "sessionId": "session-a", "mode": "single", "state": "paused", "startedAt": 100})
		_, err := transferActiveAsyncCapacity("session-a", lim(1), "source", "next", nextDir, CapacityOptions{RootDir: root})
		eq(t, err, nil, "transfer")
		i := inspectActiveAsyncCapacityOwner("source", "session-a", sourceDir, CapacityOptions{RootDir: root})
		eq(t, i.Relation, "source", "relation")
		eq(t, i.Owner.RunID, "next", "owner")
		eq(t, i.Release.State, "not-owned", "state")
		mustMatch(t, `transferred to next`, i.Release.Reason)
	})
	tw(t, f, "transfers a paused reservation without changing usage", func(t *testing.T) {
		root := t.TempDir()
		sourceDir := filepath.Join(root, "runs", "source")
		source := mustAcquire(t, "session-a", lim(1), "source", "runner", sourceDir, CapacityOptions{RootDir: root})
		_ = source.MarkStarted("runner-source")
		capWriteJSON(t, filepath.Join(sourceDir, "status.json"), map[string]any{"runId": "source", "sessionId": "session-a", "mode": "single", "state": "paused", "startedAt": 100})
		next, err := transferActiveAsyncCapacity("session-a", lim(1), "source", "next", filepath.Join(root, "runs", "next"), CapacityOptions{RootDir: root})
		eq(t, err, nil, "transfer")
		eq(t, next.Owner.SourceRunID, "source", "source")
		eq(t, next.Owner.Generation, 1, "generation")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "usage")
	})
	tw(t, f, "transfers an existing reservation after the cap is disabled", func(t *testing.T) {
		root := t.TempDir()
		sourceDir := filepath.Join(root, "runs", "source")
		source := mustAcquire(t, "session-a", lim(1), "source", "runner", sourceDir, CapacityOptions{RootDir: root})
		_ = source.MarkStarted("runner-source")
		capWriteJSON(t, filepath.Join(sourceDir, "status.json"), map[string]any{"runId": "source", "sessionId": "session-a", "mode": "single", "state": "paused", "startedAt": 100})
		next, err := transferActiveAsyncCapacity("session-a", nil, "source", "next", filepath.Join(root, "runs", "next"), CapacityOptions{RootDir: root})
		eq(t, err, nil, "transfer")
		eq(t, next.Owner.RunID, "next", "owner")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", nil, CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 0}, "usage")
	})
	tw(t, f, "releases terminal workflows despite stale step projections after the controller and async children stop", func(t *testing.T) {
		root := t.TempDir()
		asyncRoot := filepath.Join(root, "runs")
		workflowDir, childDir := filepath.Join(asyncRoot, "workflow"), filepath.Join(asyncRoot, "child")
		wf := mustAcquire(t, "session-a", lim(1), "workflow", "workflow", workflowDir, CapacityOptions{RootDir: root})
		_ = wf.MarkWorkflowStarted()
		capWriteJSON(t, filepath.Join(workflowDir, "status.json"), map[string]any{"runId": "workflow", "sessionId": "session-a", "mode": "workflow", "state": "complete", "startedAt": 100,
			"steps": []any{map[string]any{"agent": "worker", "workflowKey": "foreground", "async": false, "status": "running"}}})
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root, LiveWorkflowRunIDs: map[string]bool{"workflow": true}}), ActiveAsyncCapacitySnapshot{1, 1}, "live controller")
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "controller gone")

		second := mustAcquire(t, "session-a", lim(1), "workflow-2", "workflow", workflowDir, CapacityOptions{RootDir: root})
		_ = second.MarkWorkflowStarted()
		capWriteJSON(t, filepath.Join(workflowDir, "status.json"), map[string]any{"runId": "workflow-2", "sessionId": "session-a", "mode": "workflow", "state": "failed", "startedAt": 100,
			"steps": []any{map[string]any{"agent": "worker", "workflowKey": "async", "runId": "child", "async": true, "status": "running"}}})
		unknown := map[string]any{"version": 1, "state": "unknown", "runId": "child", "runnerProcessInstanceId": "runner-child", "reason": "process-tree-unverified"}
		capWriteJSON(t, filepath.Join(childDir, "status.json"), map[string]any{"runId": "child", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100, "processTerminal": unknown})
		capWriteJSON(t, filepath.Join(childDir, "process-terminal.json"), unknown)
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "unknown child proof")
		capWriteJSON(t, filepath.Join(childDir, "process-terminal.json"), observedProofJSON("child", "runner-child"))
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "observed child proof")
	})
	tw(t, f, "retains terminal workflows while a resumed async child lacks observed proof", func(t *testing.T) {
		root := t.TempDir()
		asyncRoot := filepath.Join(root, "runs")
		workflowDir, childDir := filepath.Join(asyncRoot, "workflow"), filepath.Join(asyncRoot, "revived-child")
		wf := mustAcquire(t, "session-a", lim(1), "workflow", "workflow", workflowDir, CapacityOptions{RootDir: root})
		_ = wf.MarkWorkflowStarted()
		capWriteJSON(t, filepath.Join(workflowDir, "status.json"), map[string]any{"runId": "workflow", "sessionId": "session-a", "mode": "workflow", "state": "failed", "startedAt": 100,
			"steps": []any{map[string]any{"agent": "worker", "workflowKey": "resume", "runId": "revived-child", "async": true, "status": "failed"}}})
		capWriteJSON(t, filepath.Join(childDir, "status.json"), map[string]any{"runId": "revived-child", "sessionId": "session-a", "mode": "single", "state": "failed", "startedAt": 100,
			"processTerminal": map[string]any{"version": 1, "state": "not-started", "runId": "revived-child", "runnerProcessInstanceId": "runner-child"}})
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{1, 1}, "retained")
		capWriteJSON(t, filepath.Join(childDir, "process-terminal.json"), observedProofJSON("revived-child", "runner-child"))
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "released")
	})
	tw(t, f, "releases terminal workflows when a child is marked failed before launch identity exists", func(t *testing.T) {
		root := t.TempDir()
		workflowDir := filepath.Join(root, "runs", "workflow")
		wf := mustAcquire(t, "session-a", lim(1), "workflow", "workflow", workflowDir, CapacityOptions{RootDir: root})
		_ = wf.MarkWorkflowStarted()
		capWriteJSON(t, filepath.Join(workflowDir, "status.json"), map[string]any{"runId": "workflow", "sessionId": "session-a", "mode": "workflow", "state": "failed", "startedAt": 100,
			"steps": []any{map[string]any{"agent": "worker", "workflowKey": "prep-failure", "status": "failed", "async": false, "error": "gate cannot be combined with acceptance"}}})
		eq(t, getActiveAsyncCapacitySnapshot("session-a", lim(1), CapacityOptions{RootDir: root}), ActiveAsyncCapacitySnapshot{0, 1}, "released")
	})
	tw(t, f, "reclaims only old failed runs with dead runners under the abandoned-timeout policy", func(t *testing.T) {
		cases := []struct {
			name      string
			state     string
			liveness  PidLiveness
			last      int64
			threshold any
			releases  bool
		}{
			{"dead old", "failed", pidDead, 0, int64(1000), true},
			{"dead recent", "failed", pidDead, 9500, int64(1000), false},
			{"alive old", "failed", pidAlive, 0, int64(1000), false},
			{"unknown old", "failed", pidUnknown, 0, int64(1000), false},
			{"strict mode", "failed", pidDead, 0, false, false},
			{"successful old", "complete", pidDead, 0, int64(1000), false},
		}
		for i, c := range cases {
			root := t.TempDir()
			runID := fmt.Sprintf("run-%d", i)
			asyncDir := filepath.Join(root, "runs", runID)
			h := mustAcquire(t, "session-policy", lim(1), runID, "runner", asyncDir, CapacityOptions{RootDir: root})
			_ = h.MarkStarted(fmt.Sprintf("runner-%d", i))
			capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": runID, "sessionId": "session-policy", "mode": "single", "state": c.state, "pid": 50000 + i, "startedAt": 0,
				"lastActivityAt": c.last, "processTerminal": map[string]any{"version": 1, "state": "unknown", "runId": runID, "runnerProcessInstanceId": fmt.Sprintf("runner-%d", i), "reason": "stale-repair"}})
			o := CapacityOptions{RootDir: root, Now: func() int64 { return 10_000 }, PidLiveness: func(int) PidLiveness { return c.liveness }, AbandonedSlotReleaseAfterMs: c.threshold}
			insp := inspectActiveAsyncCapacityOwner(runID, "session-policy", asyncDir, o)
			want := "retained"
			if c.releases {
				want = "releasable"
			}
			eq(t, insp.Release.State, want, c.name)
			if c.releases {
				mustMatch(t, `abandoned-timeout`, insp.Release.Reason)
				mustMatch(t, `process proof unknown`, insp.Release.Reason)
				eq(t, getActiveAsyncCapacitySnapshot("session-policy", lim(1), o), ActiveAsyncCapacitySnapshot{0, 1}, c.name+" released")
				b, _ := os.ReadFile(filepath.Join(asyncDir, "events.jsonl"))
				eq(t, regexp.MustCompile(`"releasedBy":"abandoned-timeout"`).MatchString(string(b)), true, c.name+" event")
			} else {
				eq(t, getActiveAsyncCapacitySnapshot("session-policy", lim(1), o), ActiveAsyncCapacitySnapshot{1, 1}, c.name+" kept")
			}
		}
	})
}
