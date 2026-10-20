package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of stale-run-reconciler (runs/background/stale-run-reconciler.ts).

func killESRCH(int, int) error { return errNoSuchProcess }
func killEPERM(int, int) error { return errNotPermitted }

func writeStatusJSON(t *testing.T, asyncDir string, status map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(asyncDir, "status.json"), string(b))
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func at(n int64) func() int64 { return func() int64 { return n } }

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

// canon is the JSON of v with object keys sorted: two values are equal as JSON when their canon is equal.
func canon(v any) string {
	var back any
	_ = json.Unmarshal([]byte(jsonOf(v)), &back)
	return jsonOf(back)
}

func str(v any) string { s, _ := v.(string); return s }

func TestStaleRunReconcilerTwins(t *testing.T) {
	const f = "stale-run-reconciler"
	runStatus := func(runID string, extra map[string]any) map[string]any {
		m := map[string]any{"runId": runID, "mode": "single", "state": "running", "pid": 12345, "startedAt": 1000, "lastUpdate": 1000,
			"steps": []any{map[string]any{"agent": "worker", "status": "running", "startedAt": 1000}}}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	tw(t, f, "classifies pid liveness without treating EPERM as dead", func(t *testing.T) {
		eq(t, checkPidLiveness(123, func(int, int) error { return nil }), PidLiveness("alive"), "alive")
		eq(t, checkPidLiveness(123, killESRCH), PidLiveness("dead"), "ESRCH")
		eq(t, checkPidLiveness(123, killEPERM), PidLiveness("unknown"), "EPERM")
		eq(t, checkPidLiveness(123, func(int, int) error { return os.ErrInvalid }), PidLiveness("unknown"), "other errors")
	})
	tw(t, f, "repairs a run whose PID belongs to another namespace only after status goes stale", func(t *testing.T) {
		dir := filepath.Join(tmp(t), "run-live")
		writeStatusJSON(t, dir, map[string]any{"runId": "run-live", "mode": "single", "state": "running", "pid": 1404, "pidNamespaceScope": "pid:[runner]",
			"startedAt": 1000, "lastUpdate": 1000, "steps": []any{map[string]any{"agent": "reviewer", "status": "running", "startedAt": 1000}}})
		r, err := reconcileAsyncRun(dir, ReconcileOptions{Kill: killESRCH, PidScope: func() string { return "pid:[observer]" }, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, false, "repaired")
		eq(t, r.Status.State, "running", "state")
		eq(t, readJSONMap(t, filepath.Join(dir, "status.json"))["state"], "running", "file")
		stale, err := reconcileAsyncRun(dir, ReconcileOptions{Kill: killESRCH, PidScope: func() string { return "pid:[observer]" }, StaleAlivePidMs: new(int64(5000)), Now: at(7000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, stale.Repaired, true, "repaired once stale")
		eq(t, stale.Status.State, "failed", "state")
		hasRe(t, stale.Message, `PID 1404 cannot be probed from this process; status has not updated for 6000ms`)
	})
	tw(t, f, "does not repair a recent namespaced run from an observer without a PID namespace", func(t *testing.T) {
		dir := filepath.Join(tmp(t), "run-dead")
		writeStatusJSON(t, dir, map[string]any{"runId": "run-dead", "mode": "single", "state": "running", "pid": 1404, "pidNamespaceScope": "pid:[runner]",
			"startedAt": 1000, "lastUpdate": 1000, "steps": []any{map[string]any{"agent": "reviewer", "status": "running", "startedAt": 1000}}})
		r, err := reconcileAsyncRun(dir, ReconcileOptions{Kill: killESRCH, PidScope: func() string { return "" }, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, false, "repaired")
		eq(t, r.Status.State, "running", "state")
	})
	tw(t, f, "marks a dead runner failed and writes exactly one completion result", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-dead"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, map[string]any{"lifecycleArtifactVersion": 3, "runId": "run-dead", "sessionId": "session-current", "completionOwnerId": "owner-current",
			"mode": "single", "state": "running", "pid": 12345, "pidNamespaceScope": "pid:[same]",
			"processTerminal": map[string]any{"version": 1, "state": "pending", "runId": "run-dead", "runnerProcessInstanceId": "runner-dead"},
			"startedAt":       1000, "lastUpdate": 1000, "currentStep": 0,
			"steps": []any{map[string]any{"agent": "scout", "status": "running", "startedAt": 1000, "contextOverflow": true}}})
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, PidScope: func() string { return "pid:[same]" }, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "failed", "state")
		hasRe(t, r.Message, `process 12345 exited or disappeared`)
		status := readJSONMap(t, filepath.Join(dir, "status.json"))
		eq(t, status["state"], "failed", "file state")
		eq(t, status["sessionId"], "session-current", "session")
		eq(t, status["completionOwnerId"], "owner-current", "owner")
		step := status["steps"].([]any)[0].(map[string]any)
		eq(t, step["status"], "failed", "step")
		hasRe(t, str(step["error"]), `process 12345 exited or disappeared`)
		res := readJSONMap(t, filepath.Join(results, "run-dead.json"))
		eq(t, res["success"], false, "success")
		eq(t, res["sessionId"], "session-current", "result session")
		eq(t, res["completionOwnerId"], "owner-current", "result owner")
		eq(t, res["state"], "failed", "result state")
		eq(t, res["exitCode"], float64(1), "exit code")
		eq(t, res["results"].([]any)[0].(map[string]any)["contextOverflow"], true, "overflow")
		hasRe(t, str(res["summary"]), `process 12345 exited or disappeared`)
		events, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		if !strings.Contains(string(events), "subagent.run.repaired_stale") {
			t.Errorf("events: %s", events)
		}
		before, _ := os.ReadFile(filepath.Join(results, "run-dead.json"))
		second, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(3000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, second.Repaired, false, "a second reconcile repairs nothing")
		after, _ := os.ReadFile(filepath.Join(results, "run-dead.json"))
		eq(t, string(after), string(before), "result untouched")
	})
	tw(t, f, "reports the observed runner exit code and signal when a killed runner left no result", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-killed"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, map[string]any{"lifecycleArtifactVersion": 3, "runId": "run-killed", "sessionId": "session-current", "mode": "single", "state": "running",
			"pid": 4242, "startedAt": 1000, "lastUpdate": 1000, "currentStep": 0, "steps": []any{map[string]any{"agent": "worker", "status": "running", "startedAt": 1000}}})
		if err := initializeProcessTerminal(dir, "run-killed", "runner-killed"); err != nil {
			t.Fatal(err)
		}
		sig := "SIGKILL"
		proof := finalizeProcessTerminal(dir, "run-killed", RunnerClose{ProcessInstanceID: "runner-killed", CloseObservedAt: 1500, Signal: &sig})
		eq(t, proof.State, "unknown", "proof")
		if _, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)}); err != nil {
			t.Fatal(err)
		}
		res := readJSONMap(t, filepath.Join(results, "run-killed.json"))
		eq(t, res["state"], "failed", "state")
		hasRe(t, str(res["summary"]), `Async runner process 4242 exited with code none \(signal SIGKILL\) before writing a result`)
	})
	tw(t, f, "repairs sessionless stale status without writing an unindexed result", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-sessionless"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, runStatus("run-sessionless", map[string]any{"currentStep": 0}))
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.ResultPath, "", "no result path")
		eq(t, readJSONMap(t, filepath.Join(dir, "status.json"))["state"], "failed", "state")
		if _, err := os.Stat(filepath.Join(results, "run-sessionless.json")); err == nil {
			t.Error("an unindexed result must not be written")
		}
	})
	tw(t, f, "includes runner stderr diagnostics when repairing a stale startup crash", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-dead-stderr"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, runStatus("run-dead-stderr", map[string]any{"sessionId": "session-current", "currentStep": 0}))
		writeFile(t, filepath.Join(dir, "runner.stderr.log"), "startup failed\nmissing peer package\n")
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		hasRe(t, r.Message, `Runner stderr tail:`)
		hasRe(t, r.Message, `missing peer package`)
		step := readJSONMap(t, filepath.Join(dir, "status.json"))["steps"].([]any)[0].(map[string]any)
		hasRe(t, str(step["error"]), `missing peer package`)
		hasRe(t, str(readJSONMap(t, filepath.Join(results, "run-dead-stderr.json"))["summary"]), `missing peer package`)
	})
	tw(t, f, "keeps stale repair successful when the event log cannot be appended", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-dead-events-dir"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, runStatus("run-dead-events-dir", map[string]any{"sessionId": "session-current", "currentStep": 0}))
		if err := os.MkdirAll(filepath.Join(dir, "events.jsonl"), 0o755); err != nil {
			t.Fatal(err)
		}
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "failed", "state")
		eq(t, readJSONMap(t, filepath.Join(dir, "status.json"))["state"], "failed", "file")
		eq(t, readJSONMap(t, filepath.Join(results, "run-dead-events-dir.json"))["success"], false, "result")
		st, _ := os.Stat(filepath.Join(dir, "events.jsonl"))
		eq(t, st.IsDir(), true, "events.jsonl is still the directory")
	})
	tw(t, f, "repairs stale status with per-child result outcomes", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-mixed"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, map[string]any{"runId": "run-mixed", "mode": "chain", "state": "running", "pid": 12345, "startedAt": 1000, "lastUpdate": 1000,
			"steps": []any{map[string]any{"agent": "scout", "status": "running", "startedAt": 1000, "model": "planned-scout"},
				map[string]any{"agent": "worker", "status": "running", "startedAt": 1100, "model": "planned-worker"}}})
		scout, worker := filepath.Join(root, "scout.jsonl"), filepath.Join(root, "worker.jsonl")
		writeFile(t, filepath.Join(results, "run-mixed.json"), canon(map[string]any{"id": "run-mixed", "success": false, "state": "failed", "results": []any{
			map[string]any{"agent": "scout", "success": true, "sessionFile": scout, "model": "fast"},
			map[string]any{"agent": "worker", "success": false, "error": "boom", "sessionFile": worker, "model": "careful", "contextOverflow": true}}}))
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "failed", "state")
		s0, s1 := r.Status.Steps[0], r.Status.Steps[1]
		eq(t, s0.Status, "complete", "step 0")
		eq(t, *s0.ExitCode, 0, "step 0 exit")
		eq(t, s0.Model, "fast", "step 0 model")
		eq(t, s0.SessionFile, scout, "step 0 session")
		eq(t, s1.Status, "failed", "step 1")
		eq(t, *s1.ExitCode, 1, "step 1 exit")
		eq(t, s1.Error, "boom", "step 1 error")
		eq(t, s1.Model, "careful", "step 1 model")
		eq(t, *s1.ContextOverflow, true, "step 1 overflow")
		eq(t, s1.SessionFile, worker, "step 1 session")
		eq(t, r.Status.Error, "boom", "root error")
	})
	tw(t, f, "retains a root failure when stale result repair writes after runner finalization", func(t *testing.T) {
		root := tmp(t)
		const runID, staleRunner, finalRunner = "run-result-race", "runner-stale-result-race", "runner-final-result-race"
		dir, results := filepath.Join(root, runID), filepath.Join(root, "results")
		if err := initializeProcessTerminal(dir, runID, staleRunner); err != nil {
			t.Fatal(err)
		}
		pending := func(id string, child any, state string) map[string]any {
			m := map[string]any{"version": 1, "state": state, "runId": runID, "runnerProcessInstanceId": id}
			if child != nil {
				m["childIndex"] = child
			}
			return m
		}
		staleSteps := func(t0, t1 any) []any {
			return []any{
				map[string]any{"agent": "scout", "status": "complete", "startedAt": 1000, "endedAt": 1200, "exitCode": 0, "processTerminal": t0},
				map[string]any{"agent": "worker", "status": "failed", "startedAt": 1000, "endedAt": 1400, "exitCode": 1, "error": "child diagnostic", "processTerminal": t1}}
		}
		stale := map[string]any{"lifecycleArtifactVersion": 3, "runId": runID, "mode": "chain", "state": "running", "pid": 12345, "startedAt": 1000, "lastUpdate": 1500,
			"processTerminal": pending(staleRunner, nil, "pending"),
			"steps":           staleSteps(pending("runner-stale-step-0", 0, "pending"), pending("runner-stale-step-1", 1, "not-started"))}
		writeStatusJSON(t, dir, stale)
		resultPath := filepath.Join(results, runID+".json")
		writeFile(t, resultPath, canon(map[string]any{"id": runID, "success": false, "state": "failed", "results": []any{
			map[string]any{"agent": "scout", "success": true}, map[string]any{"agent": "worker", "success": false, "error": "child diagnostic"}}}))
		runnerInst := func(id string, at int) any {
			return []any{map[string]any{"kind": "runner", "processInstanceId": id, "closeObservedAt": at, "exitCode": 0, "signal": nil}}
		}
		observed := map[string]any{"version": 1, "state": "observed", "runId": runID, "runnerProcessInstanceId": finalRunner, "observedAt": 1900, "instances": runnerInst(finalRunner, 1900)}
		observedStep := map[string]any{"version": 1, "state": "observed", "runId": runID, "childIndex": 0, "runnerProcessInstanceId": "runner-current-step-0", "observedAt": 1901,
			"resumeDisposition": "non-resumable", "instances": runnerInst("runner-current-step-0", 1901)}
		unknownStep := map[string]any{"version": 1, "state": "unknown", "runId": runID, "childIndex": 1, "runnerProcessInstanceId": "runner-current-step-1",
			"reason": "process-tree-unverified", "resumeDisposition": "non-resumable"}
		repaired, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Now: at(2000), Observe: func(*AsyncStatus) {
			writeFile(t, filepath.Join(dir, "process-terminal.json"), canon(observed))
			next := map[string]any{}
			for k, v := range stale {
				next[k] = v
			}
			next["state"], next["error"], next["processTerminal"], next["lastUpdate"], next["endedAt"] = "failed", "ROOT DIAGNOSTIC", observed, 1900, 1900
			next["steps"] = staleSteps(observedStep, unknownStep)
			writeStatusJSON(t, dir, next)
		}})
		if err != nil {
			t.Fatal(err)
		}
		// the stale status is repaired from the result file, the finalizing runner's root error and proofs win
		eq(t, repaired.Repaired, true, "repaired")
		eq(t, repaired.Status.State, "failed", "state")
		eq(t, repaired.Status.Error, "ROOT DIAGNOSTIC", "root error")
		eq(t, repaired.Status.Steps[0].Status, "complete", "step 0")
		eq(t, repaired.Status.Steps[1].Status, "failed", "step 1")
		eq(t, repaired.Status.Steps[1].Error, "child diagnostic", "step 1 error")
		eq(t, canon(repaired.Status.ProcessTerminal), canon(observed), "root proof")
		eq(t, canon(repaired.Status.Steps[0].ProcessTerminal), canon(observedStep), "step 0 proof")
		eq(t, canon(repaired.Status.Steps[1].ProcessTerminal), canon(unknownStep), "step 1 proof")
		persisted := readJSONMap(t, filepath.Join(dir, "status.json"))
		eq(t, persisted["error"], "ROOT DIAGNOSTIC", "persisted error")
		eq(t, canon(persisted["processTerminal"]), canon(observed), "persisted root proof")
		steps := persisted["steps"].([]any)
		eq(t, canon(steps[0].(map[string]any)["processTerminal"]), canon(observedStep), "persisted step 0 proof")
		eq(t, canon(steps[1].(map[string]any)["processTerminal"]), canon(unknownStep), "persisted step 1 proof")
		st, _ := readStatus(dir)
		public := statusToSummary(dir, st)
		eq(t, canon(public.ProcessTerminal), canon(observed), "public root proof")
		eq(t, canon(public.Steps[0].ProcessTerminal), canon(observedStep), "public step 0 proof")
		eq(t, canon(public.Steps[1].ProcessTerminal), canon(unknownStep), "public step 1 proof")
		eq(t, canon(readProcessTerminal(dir, runID, finalRunner)), canon(observed), "sidecar read with the final runner")
		staleLookup := readProcessTerminal(dir, runID, staleRunner)
		eq(t, staleLookup.State, "unknown", "stale lookup state")
		eq(t, staleLookup.Reason, "proof-write-failed", "stale lookup reason")
		proof := finalizeProcessTerminal(dir, runID, RunnerClose{ProcessInstanceID: finalRunner, CloseObservedAt: 2100, ExitCode: new(0)})
		final := readJSONMap(t, filepath.Join(dir, "status.json"))
		eq(t, canon(proof), canon(observed), "finalized proof")
		eq(t, canon(final["processTerminal"]), canon(proof), "final status proof")
		eq(t, canon(final["steps"].([]any)[0].(map[string]any)["processTerminal"]), canon(observedStep), "final step 0 proof")
		eq(t, canon(final["steps"].([]any)[1].(map[string]any)["processTerminal"]), canon(unknownStep), "final step 1 proof")
		eq(t, final["state"], "failed", "final state")
		eq(t, final["error"], "ROOT DIAGNOSTIC", "final error")
		eq(t, readJSONMap(t, resultPath)["results"].([]any)[1].(map[string]any)["error"], "child diagnostic", "result error")
	})
	tw(t, f, "preserves explicit root failure precedence during result repair", func(t *testing.T) {
		root := tmp(t)
		results := filepath.Join(root, "results")
		for i, fx := range []struct{ statusError, resultError, expected string }{
			{"status failure", "result failure", "status failure"},
			{"", "result failure", "result failure"},
		} {
			runID := "run-error-precedence-" + strings.Repeat("x", i)
			dir := filepath.Join(root, runID)
			extra := map[string]any{}
			if fx.statusError != "" {
				extra["error"] = fx.statusError
			}
			writeStatusJSON(t, dir, runStatus(runID, extra))
			writeFile(t, filepath.Join(results, runID+".json"), canon(map[string]any{"id": runID, "success": false, "state": "failed", "error": fx.resultError,
				"results": []any{map[string]any{"agent": "worker", "success": false, "error": "child failure"}}}))
			r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Now: at(2000)})
			if err != nil {
				t.Fatal(err)
			}
			eq(t, r.Status.Error, fx.expected, "error "+runID)
		}
	})
	tw(t, f, "repairs stale running status from an indexed pending result", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-pending-result"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, runStatus("run-pending-result", map[string]any{"sessionId": "session-current", "currentStep": 0}))
		public := filepath.Join(results, "run-pending-result.json")
		if err := os.MkdirAll(public, 0o755); err != nil {
			t.Fatal(err)
		}
		state, err := writeAsyncResultFile(public, map[string]any{"id": "run-pending-result", "runId": "run-pending-result", "sessionId": "session-current",
			"success": true, "state": "complete", "summary": "already done"})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, state, "pending", "the public path is blocked, the result stays pending")
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "complete", "state")
		eq(t, r.Status.Steps[0].Status, "complete", "step")
		eq(t, readJSONMap(t, filepath.Join(dir, "status.json"))["state"], "complete", "file")
		st, _ := os.Stat(public)
		eq(t, st.IsDir(), true, "the public path is still a directory")
		body, _ := os.ReadFile(r.ResultPath)
		if !strings.Contains(string(body), "already done") {
			t.Errorf("result %s", body)
		}
	})
	tw(t, f, "repairs stale rejected status from an indexed pending status snapshot", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-rejected"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, map[string]any{"runId": "run-rejected", "sessionId": "session-current", "mode": "chain", "state": "rejected", "pid": 12345, "startedAt": 1000, "lastUpdate": 1000,
			"currentStep": 0, "steps": []any{map[string]any{"agent": "worker", "status": "rejected", "startedAt": 1000, "error": "Run rejected."}}})
		public := filepath.Join(results, "run-rejected.json")
		if err := os.MkdirAll(public, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writePendingAsyncResultFile(public, map[string]any{"id": "run-rejected", "runId": "run-rejected", "sessionId": "session-current", "success": false, "state": "rejected",
			"summary": "Run rejected.", "results": []any{map[string]any{"agent": "worker", "success": false, "error": "Run rejected."}}}); err != nil {
			t.Fatal(err)
		}
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, false, "repaired")
		eq(t, r.Status.State, "rejected", "state")
		eq(t, r.Status.Steps[0].Status, "rejected", "step")
		body, _ := os.ReadFile(r.ResultPath)
		if !strings.Contains(string(body), "Run rejected") {
			t.Errorf("result %s", body)
		}
	})
	tw(t, f, "fails a stale run when a live pid has not updated beyond the stale threshold", func(t *testing.T) {
		root := tmp(t)
		dir := filepath.Join(root, "run-reused-pid")
		writeStatusJSON(t, dir, runStatus("run-reused-pid", nil))
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: filepath.Join(root, "results"), Kill: func(int, int) error { return nil }, Now: at(5000), StaleAlivePidMs: new(int64(1000))})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "failed", "state")
		hasRe(t, r.Message, `PID .* is still live; status has not updated`)
	})
	tw(t, f, "preserves an existing result instead of overwriting it with stale-run failure", func(t *testing.T) {
		root := tmp(t)
		dir, results := filepath.Join(root, "run-result"), filepath.Join(root, "results")
		writeStatusJSON(t, dir, runStatus("run-result", nil))
		resultPath := filepath.Join(results, "run-result.json")
		writeFile(t, resultPath, canon(map[string]any{"id": "run-result", "success": true, "state": "complete", "summary": "already done"}))
		r, err := reconcileAsyncRun(dir, ReconcileOptions{ResultsDir: results, Kill: killESRCH, Now: at(2000)})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Repaired, true, "repaired")
		eq(t, r.Status.State, "complete", "state")
		eq(t, readJSONMap(t, resultPath)["summary"], "already done", "result kept")
	})
}
