package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
)

// Twins of test/unit/chain-root-attachment.test.ts.

func craRoot(dir string, runID string, index int) ImportedAsyncRoot {
	return ImportedAsyncRoot{RunID: runID, Index: index, AsyncDir: filepath.Join(dir, runID), ResultPath: filepath.Join(dir, "results", runID+".json")}
}

func craWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(b))
}

func craWait(t *testing.T, root ImportedAsyncRoot, opts ImportedAsyncRootOptions) *jsObject {
	t.Helper()
	r, err := WaitForImportedAsyncRoot(root, opts)
	if err != nil {
		t.Fatalf("WaitForImportedAsyncRoot: %v", err)
	}
	return r
}

func i64p(v int64) *int64 { return &v }

type jmap = map[string]any

func TestChainRootAttachmentTwins(t *testing.T) {
	const f = "chain-root-attachment"
	tw(t, f, "imports an already completed async child result", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		sessionFile := filepath.Join(dir, "child.jsonl")
		writeFile(t, sessionFile, "")
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "complete", "startedAt": 1, "steps": []any{jmap{"agent": "worker", "status": "complete", "sessionFile": sessionFile}}})
		usage := jmap{"input": 100, "output": 50, "cacheRead": 0, "cacheWrite": 0, "cost": 0.001, "turns": 1}
		craWriteJSON(t, root.ResultPath, jmap{"sessionId": "publication-session", "toolCallId": "publication-tool-call", "state": "complete", "success": true,
			"results": []any{jmap{"agent": "worker", "output": "root output", "success": true, "sessionFile": sessionFile, "usage": usage}}})
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1)})
		writeFile(t, filepath.Join(root.AsyncDir, "status.json"), "{malformed")
		jsonEq(t, jo("agent", result.vals["agent"], "importedPublication", result.vals["importedPublication"], "output", result.vals["output"], "exitCode", result.vals["exitCode"], "sessionFile", result.vals["sessionFile"], "usage", result.vals["usage"]),
			`{"agent":"worker","importedPublication":{"sessionId":"publication-session","toolCallId":"publication-tool-call"},"output":"root output","exitCode":0,"sessionFile":`+marshalJSON(sessionFile, "")+`,"usage":{"input":100,"output":50,"cacheRead":0,"cacheWrite":0,"cost":0.001,"turns":1}}`)
	})
	tw(t, f, "imports a session-indexed pending result before terminal status fallback", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		root.ResultPath = filepath.Join(dir, "root-run", "workflow-result.json")
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "complete", "sessionId": "session-a", "startedAt": 1, "steps": []any{jmap{"agent": "worker", "status": "complete"}}})
		err := writePendingAsyncResultFile(root.ResultPath, map[string]any{"id": root.RunID, "runId": root.RunID, "sessionId": "session-a", "state": "complete", "success": true,
			"results": []any{map[string]any{"agent": "worker", "output": "pending root output", "success": true}}})
		eq(t, err, nil, "write pending result")
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1), TerminalResultGraceMs: i64p(0)})
		eq(t, result.vals["output"], "pending root output", "output")
		eq(t, result.vals["exitCode"], 0.0, "exitCode")
	})
	tw(t, f, "waits for a running async child to write its terminal result", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "running", "startedAt": 1, "steps": []any{jmap{"agent": "worker", "status": "running"}}})
		var once sync.Once
		polls := 0
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(5), Sleep: func(ms int64) {
			// the child publishes its result while the root is still running, after a few polls
			if polls++; polls == 3 {
				once.Do(func() {
					craWriteJSON(t, root.ResultPath, jmap{"state": "complete", "success": true, "results": []any{jmap{"agent": "worker", "output": "late root output", "success": true}}})
				})
			}
		}})
		eq(t, result.vals["output"], "late root output", "output")
		eq(t, result.vals["exitCode"], 0.0, "exitCode")
	})
	for _, proofState := range []string{"", "pending"} {
		name := "absent"
		if proofState != "" {
			name = proofState
		}
		tw(t, f, "waits past the terminal grace for workflow-owned single publication with ${proofState ?? \"absent\"} root proof", func(t *testing.T) {
			_ = name
			dir := tmp(t)
			root := craRoot(dir, "root-run", 0)
			root.ResultPath = filepath.Join(dir, "root-run", "workflow-result.json")
			status := jmap{"runId": root.RunID, "mode": "single", "parentWorkflowRunId": "workflow-parent", "state": "running", "pid": os.Getpid(), "startedAt": 1, "steps": []any{jmap{"agent": "worker", "status": "complete"}}}
			if proofState != "" {
				status["processTerminal"] = jmap{"version": 1, "runId": root.RunID, "runnerProcessInstanceId": "runner-instance", "state": proofState}
			}
			craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), status)
			// the waiter's clock is a fake one the test advances: the live publisher (this process) is left as it is
			var clock int64
			polls := 0
			result := craWait(t, root, ImportedAsyncRootOptions{Now: func() int64 { return clock }, Sleep: func(ms int64) {
				clock += ms
				if polls++; polls == 3 {
					eq(t, clock, int64(1500), "clock after three polls")
					if _, err := os.Stat(root.ResultPath); err == nil {
						t.Error("the result exists before publication")
					}
					craWriteJSON(t, root.ResultPath, jmap{"state": "complete", "success": true, "results": []any{jmap{"agent": "worker", "output": "published after step completion", "success": true}}})
				}
			}})
			eq(t, result.vals["success"], true, "success "+jsString(result.vals["error"]))
			eq(t, result.vals["output"], "published after step completion", "output")
		})
	}
	tw(t, f, "imports a failed root as a failed first chain step", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "failed", "startedAt": 1, "error": "root failed", "steps": []any{jmap{"agent": "worker", "status": "failed", "error": "root failed"}}})
		craWriteJSON(t, root.ResultPath, jmap{"state": "failed", "success": false, "summary": "root failed", "results": []any{jmap{"agent": "worker", "output": "root failed", "error": "root failed", "success": false}}})
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1)})
		eq(t, result.vals["exitCode"], 1.0, "exitCode")
		eq(t, result.vals["error"], "root failed", "error")
		eq(t, result.vals["output"], "root failed", "output")
		jsonEq(t, result.vals["importedPublication"], `{}`)
	})
	tw(t, f, "imports a partial root without collapsing it to failed", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		msg := "Required file-only output was not produced: report.md"
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "partial", "activityState": "needs_attention", "startedAt": 1, "error": msg,
			"steps": []any{jmap{"agent": "worker", "status": "failed", "activityState": "needs_attention", "error": msg}}})
		effects := jmap{"fileMutation": jmap{"status": "observed", "attempted": true, "evidence": jmap{"source": "tracked-files", "trackedOnly": true, "cwd": dir, "changedFiles": []any{"input.md"}, "attemptedMutation": true}}}
		craWriteJSON(t, root.ResultPath, jmap{"state": "partial", "success": false, "summary": msg, "results": []any{jmap{"agent": "worker", "output": msg, "error": msg, "success": false, "effects": effects}}})
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1)})
		eq(t, result.vals["exitCode"], 1.0, "exitCode")
		eq(t, result.vals["error"], msg, "error")
		eq(t, result.obj("execution").vals["status"], "partial", "execution status")
		jsonEq(t, result.obj("effects").obj("fileMutation").obj("evidence").vals["changedFiles"], `["input.md"]`)
	})
	tw(t, f, "fails a partial root that never produced a result file", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		msg := "Required file-only output was not produced: report.md"
		effects := jmap{"fileMutation": jmap{"status": "observed", "attempted": true, "evidence": jmap{"source": "tracked-files", "trackedOnly": true, "cwd": dir, "changedFiles": []any{"input.md"}, "attemptedMutation": true}}}
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "partial", "startedAt": 1, "error": msg,
			"steps": []any{jmap{"agent": "worker", "status": "failed", "activityState": "needs_attention", "error": msg, "effects": effects}}})
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1), TerminalResultGraceMs: i64p(0)})
		eq(t, result.vals["exitCode"], 1.0, "exitCode")
		eq(t, result.vals["error"], msg, "error")
		eq(t, result.obj("execution").vals["status"], "partial", "execution status")
		jsonEq(t, result.obj("effects").obj("fileMutation").obj("evidence").vals["changedFiles"], `["input.md"]`)
	})
	for _, fixture := range []struct {
		name   string
		index  int
		status jmap
	}{
		{"terminal root", 0, jmap{"mode": "single", "state": "complete"}},
		{"terminal workflow-owned single", 0, jmap{"mode": "single", "state": "complete", "parentWorkflowRunId": "workflow-parent"}},
		{"running workflow-owned single with unavailable root proof", 0, jmap{"mode": "single", "state": "running", "parentWorkflowRunId": "workflow-parent",
			"processTerminal": jmap{"version": 1, "runId": "root-run", "runnerProcessInstanceId": "runner-instance", "state": "unknown", "reason": "runner-candidate-missing"}}},
		{"selected child of running workflow-owned parallel root", 1, jmap{"mode": "parallel", "state": "running", "parentWorkflowRunId": "workflow-parent"}},
	} {
		tw(t, f, "fails closed without a result for ${fixture.name}", func(t *testing.T) {
			dir := tmp(t)
			root := craRoot(dir, "root-run", fixture.index)
			status := jmap{"runId": root.RunID, "startedAt": 1}
			for k, v := range fixture.status {
				status[k] = v
			}
			steps := []any{}
			if fixture.index != 0 {
				steps = append(steps, jmap{"agent": "first", "status": "running"})
			}
			status["steps"] = append(steps, jmap{"agent": "worker", "status": "complete"})
			craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), status)
			result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1), TerminalResultGraceMs: i64p(0)})
			eq(t, result.vals["agent"], "worker", fixture.name+" agent")
			eq(t, result.vals["exitCode"], 1.0, fixture.name+" exitCode")
			if !regexp.MustCompile(`ended without a result file`).MatchString(jsString(result.vals["error"])) {
				t.Errorf("%s: error = %v", fixture.name, result.vals["error"])
			}
			if _, has := result.vals["importedPublication"]; has {
				t.Errorf("%s: importedPublication is set", fixture.name)
			}
		})
	}
	tw(t, f, "stops waiting when the parent timeout aborts an attached root", func(t *testing.T) {
		dir := tmp(t)
		root := craRoot(dir, "root-run", 0)
		craWriteJSON(t, filepath.Join(root.AsyncDir, "status.json"), jmap{"runId": root.RunID, "mode": "single", "state": "running", "startedAt": 1, "steps": []any{jmap{"agent": "worker", "status": "running"}}})
		timedOut := false
		polls := 0
		result := craWait(t, root, ImportedAsyncRootOptions{PollIntervalMs: i64p(1), ShouldAbort: func() bool { return timedOut }, TimeoutMessage: "parent timed out", Sleep: func(ms int64) {
			if polls++; polls == 3 {
				timedOut = true
			}
		}})
		eq(t, result.vals["exitCode"], 1.0, "exitCode")
		eq(t, result.vals["timedOut"], true, "timedOut")
		eq(t, result.vals["error"], "parent timed out", "error")
		eq(t, result.vals["output"], "parent timed out", "output")
		if _, has := result.vals["importedPublication"]; has {
			t.Error("importedPublication is set")
		}
	})
}

func jsString(v any) string { s, _ := v.(string); return s }
