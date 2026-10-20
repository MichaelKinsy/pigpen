package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of async-resume.test.ts.

func writeJ(t *testing.T, p string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(b))
}

func TestAsyncResumeTwins(t *testing.T) {
	const f = "async-resume"
	type roots struct {
		root, asyncRoot, resultsDir string
		dirs                        asyncDirs
	}
	mk := func(t *testing.T) roots {
		root := tmp(t)
		r := roots{root: root, asyncRoot: filepath.Join(root, "runs"), resultsDir: filepath.Join(root, "results")}
		r.dirs = asyncDirs{Async: r.asyncRoot, Results: r.resultsDir}
		return r
	}
	resolve := func(r roots, p resumeParams, o resumeOptions) (*resumeTarget, error) {
		return resolveAsyncResumeTarget(p, r.dirs, o)
	}
	id := func(s string) resumeParams { return resumeParams{ID: s} }
	idx := func(s string, i float64) resumeParams { return resumeParams{ID: s, Index: &i} }
	session := func(t *testing.T, root, name string) string {
		p := filepath.Join(root, name)
		writeFile(t, p, "")
		return p
	}
	resolveOK := func(t *testing.T, r roots, p resumeParams, o resumeOptions) *resumeTarget {
		t.Helper()
		target, err := resolve(r, p, o)
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	wantErr := func(t *testing.T, err error, pattern string) {
		t.Helper()
		if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
			t.Errorf("error %v does not match %s", err, pattern)
		}
	}
	f64p := func(v float64) *float64 { return &v }

	tw(t, f, "resolves a completed single-child run from persisted status", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "session.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "run-abcde", "status.json"), jm{"runId": "run-abcde", "mode": "single", "state": "complete", "startedAt": 100, "endedAt": 200, "lastUpdate": 200, "cwd": r.root, "sessionFile": sf, "steps": []any{jm{"agent": "worker", "status": "complete"}}})
		target := resolveOK(t, r, id("run-abcd"), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		eq(t, target.runID, "run-abcde", "runId")
		eq(t, target.agent, "worker", "agent")
		eq(t, target.sessionFile, sf, "sessionFile")
		eq(t, target.cwd, r.root, "cwd")
	})
	tw(t, f, "requires a recovery descriptor for a legacy workflow child session", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "child-session.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "workflow-1", "status.json"), jm{"runId": "workflow-1", "mode": "workflow", "state": "failed", "startedAt": 100, "endedAt": 200, "lastUpdate": 200, "cwd": r.root,
			"steps": []any{jm{"agent": "worker", "status": "failed", "sessionFile": sf, "runId": "child-1"}}})
		target := resolveOK(t, r, id("workflow-1"), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		eq(t, target.mode, "workflow", "mode")
		eq(t, target.agent, "worker", "agent")
		eq(t, target.sessionFile, sf, "sessionFile")
		if target.descriptor != nil {
			t.Error("descriptor")
		}
		eq(t, asyncReviveRequiresRecoveryDescriptor(target), true, "requires a descriptor")
		eq(t, asyncReviveRequiresRecoveryDescriptor(&resumeTarget{}), true, "empty target requires a descriptor")
	})
	tskip(t, f, "uses a current workflow's persisted parent admission authority", "admissionCapabilityCeiling (the workflow's parent authority) belongs to the capability-ceiling slice, not ported: the port never writes it, and a run that carries one is refused on resume instead of being merged")
	tskip(t, f, "resolves a retained managed worktree cwd and fails closed when it is missing", "retained managed worktree handoffs (handoff.json, resolveRetainedWorktreeCwd of parallel-handoff.ts) belong to the worktree-isolation slice, not ported: a port run never creates a worktree")
	tskip(t, f, "resolves a retained managed worktree cwd in the original repository subdirectory", "managed worktree handoff resolution for a repository subdirectory (parallel-handoff.ts) belongs to the worktree-isolation slice, not ported")
	tw(t, f, "resumes a normal child when a sibling has a managed worktree handoff", func(t *testing.T) {
		r := mk(t)
		asyncDir := filepath.Join(r.asyncRoot, "run-mixed-cwd")
		worktree := filepath.Join(r.root, "managed-worktree")
		_ = os.MkdirAll(worktree, 0o755)
		sf := session(t, r.root, "session.jsonl")
		writeJ(t, filepath.Join(asyncDir, "status.json"), jm{"runId": "run-mixed-cwd", "mode": "parallel", "state": "complete", "startedAt": 100, "endedAt": 200, "lastUpdate": 200, "cwd": r.root,
			"steps": []any{jm{"agent": "worktree-worker", "status": "complete"}, jm{"agent": "normal-worker", "status": "complete", "sessionFile": sf}}})
		writeJ(t, filepath.Join(asyncDir, "handoff.json"), jm{"version": 1, "runId": "run-mixed-cwd", "mode": "parallel", "source": "async", "cwd": r.root, "createdAt": 100, "updatedAt": 200,
			"groups": []any{jm{"stepIndex": 0, "baseCommit": "deadbeef", "repoRoot": r.root,
				"children": []any{jm{"index": 0, "taskIndex": 0, "agent": "worktree-worker", "status": "completed", "summary": "done"}},
				"cleanup":  jm{"state": "partial", "pruned": true, "tasks": []any{jm{"index": 0, "path": worktree, "branch": "branch", "worktreeRemoved": false, "branchRemoved": false, "preserved": true}}}}}})
		target := resolveOK(t, r, idx("run-mixed-cwd", 1), resumeOptions{})
		eq(t, target.agent, "normal-worker", "agent")
		eq(t, target.cwd, r.root, "cwd")
	})
	tskip(t, f, "rejects a retained managed worktree cwd that resolves outside the preserved worktree", "the symlink-escape check of a retained managed worktree cwd (parallel-handoff.ts) belongs to the worktree-isolation slice, not ported")
	tskip(t, f, "preserves and validates persisted capability ceilings for resume", "capability ceilings (parseSubagentCapabilityCeiling and their intersection) belong to the capability-ceiling slice, not ported; the port refuses to resume a run that carries one (covered by the unsupported-field refusal tests)")
	tskip(t, f, "rejects unknown, cross-run, and wrong-agent recovery descriptors", "the case also checks capability ceilings, extension bindings, required child extensions, nested-subagent allowlists and applySteeringRecoveryAgentConfig, which belong to slices not ported; the unknown-field, source-run and agent checks it shares are in readAsyncRecoveryDescriptor and resolveAsyncResumeTarget and are covered by the cross-run and wrong-agent assertions of the other twins")
	writeDescriptor := func(t *testing.T, r roots, runID string, extra jm) {
		d := jm{"version": 1, "runFanoutBudget": createRunFanoutBudget(t, runID, 64), "sourceRunId": runID, "agent": "worker", "cwd": r.root, "systemPromptMode": "replace",
			"inheritGlobalContext": false, "inheritProjectContext": false, "inheritSkills": false, "outputMode": "inline", "maxSubagentDepth": 2, "share": false}
		for k, v := range extra {
			d[k] = v
		}
		writeJ(t, filepath.Join(r.asyncRoot, runID, "recovery-descriptor.json"), d)
	}
	pausedRun := func(t *testing.T, r roots, runID string) {
		sf := session(t, r.root, "session.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, runID, "status.json"), jm{"runId": runID, "mode": "single", "state": "paused", "startedAt": 100, "lastUpdate": 200, "cwd": r.root, "steps": []any{jm{"agent": "worker", "status": "paused", "sessionFile": sf}}})
	}
	tw(t, f, "ignores removed turn budgets in persisted recovery descriptors", func(t *testing.T) {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
		r := mk(t)
		pausedRun(t, r, "run-turn-budget")
		writeDescriptor(t, r, "run-turn-budget", jm{"initialTurnBudget": jm{"maxTurns": 8, "graceTurns": 2, "outcome": "within-budget", "turnCount": 0, "wrapUpRequestedAtTurn": 8, "terminationDeferredAtTurn": 10, "exceededAtTurn": 11}})
		target := resolveOK(t, r, id("run-turn-budget"), resumeOptions{})
		if _, has := target.descriptor.Carried["initialTurnBudget"]; has {
			t.Error("initialTurnBudget carried")
		}
		b, _ := json.Marshal(target.descriptor)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if _, has := m["initialTurnBudget"]; has {
			t.Error("initialTurnBudget in the descriptor")
		}
		writeDescriptor(t, r, "run-turn-budget", jm{"initialTurnBudget": jm{"maxTurns": 8, "graceTurns": 2, "unrelated": true}})
		legacy := resolveOK(t, r, id("run-turn-budget"), resumeOptions{})
		if _, has := legacy.descriptor.Carried["initialTurnBudget"]; has {
			t.Error("malformed initialTurnBudget carried")
		}
	})
	tw(t, f, "accepts current acceptance metadata in recovery descriptors", func(t *testing.T) {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
		r := mk(t)
		pausedRun(t, r, "run-acceptance")
		acceptance := jm{"level": "attested", "criteria": []any{jm{"id": "criterion-1", "must": "Return evidence", "evidence": []any{"manual-notes"}, "severity": "required"}},
			"evidence": []any{"manual-notes", "residual-risks"}, "verify": []any{}, "stopRules": []any{}}
		writeDescriptor(t, r, "run-acceptance", jm{"acceptance": acceptance})
		target := resolveOK(t, r, id("run-acceptance"), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		var got, want any
		_ = json.Unmarshal(target.descriptor.Carried["acceptance"], &got)
		wb, _ := json.Marshal(acceptance)
		_ = json.Unmarshal(wb, &want)
		eq(t, reflect.DeepEqual(got, want), true, "acceptance metadata")
		eq(t, target.unsupported, []string{"acceptance"}, "a resume refuses what it cannot enforce")
	})
	tskip(t, f, "rejects reviewed acceptance metadata in recovery descriptors", "acceptance descriptors are validated by validateAcceptanceInput (acceptance.ts, 1,664 lines of the acceptance-gates slice, not ported); the port refuses to resume any run that carries acceptance")
	tskip(t, f, "rejects legacy acceptance metadata in recovery descriptors", "the explicit/inferredReason acceptance fields are rejected by validateAcceptanceInput (acceptance-gates slice, not ported); the port refuses to resume any run that carries acceptance")

	tw(t, f, "rejects a stopped child selected from a terminal aggregate", func(t *testing.T) {
		for _, stopped := range []jm{{"status": "stopped"}, {"status": "failed", "stopped": true}} {
			r := mk(t)
			stoppedSession := session(t, r.root, "stopped.jsonl")
			completedSession := session(t, r.root, "completed.jsonl")
			first := jm{"agent": "stopped-child", "sessionFile": stoppedSession}
			for k, v := range stopped {
				first[k] = v
			}
			writeJ(t, filepath.Join(r.asyncRoot, "run-aggregate", "status.json"), jm{"runId": "run-aggregate", "mode": "parallel", "state": "failed", "startedAt": 100, "endedAt": 200, "lastUpdate": 200, "cwd": r.root,
				"steps": []any{first, jm{"agent": "completed-child", "status": "complete", "sessionFile": completedSession}}})
			_, err := resolve(r, idx("run-aggregate", 0), resumeOptions{})
			wantErr(t, err, `child 0 was stopped and cannot be resumed`)
			eq(t, resolveOK(t, r, idx("run-aggregate", 1), resumeOptions{}).agent, "completed-child", "agent")
		}
	})
	tw(t, f, "rejects stopped runs instead of reviving them", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "session.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "run-stopped", "status.json"), jm{"runId": "run-stopped", "mode": "single", "state": "stopped", "stopped": true, "startedAt": 100, "endedAt": 200, "lastUpdate": 200, "cwd": r.root,
			"sessionFile": sf, "steps": []any{jm{"agent": "worker", "status": "stopped", "stopped": true, "sessionFile": sf}}})
		_, err := resolve(r, id("run-stopped"), resumeOptions{})
		wantErr(t, err, `stopped and cannot be resumed`)
	})
	tw(t, f, "rejects ambiguous run id prefixes", func(t *testing.T) {
		r := mk(t)
		writeJ(t, filepath.Join(r.asyncRoot, "run-aaaa-1", "status.json"), jm{"runId": "run-aaaa-1", "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "scout", "status": "running"}}})
		writeJ(t, filepath.Join(r.asyncRoot, "run-aaaa-2", "status.json"), jm{"runId": "run-aaaa-2", "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "running"}}})
		_, err := resolve(r, id("run-aaaa"), resumeOptions{})
		wantErr(t, err, `Ambiguous async run id prefix 'run-aaaa' matched: run-aaaa-1, run-aaaa-2`)
		_, err = resolve(r, id("run-a"), resumeOptions{})
		wantErr(t, err, `at least 8 characters`)
	})
	tw(t, f, "rejects path-like ids and directories outside the async root", func(t *testing.T) {
		r := mk(t)
		_, err := resolve(r, id("../run"), resumeOptions{})
		wantErr(t, err, `id must be an async run id or prefix, not a path`)
		_, err = resolve(r, resumeParams{Dir: filepath.Join(r.root, "outside")}, resumeOptions{})
		wantErr(t, err, `Async run directory must be inside`)
	})
	tw(t, f, "rejects non-jsonl session files before reviving", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "session.txt")
		writeJ(t, filepath.Join(r.asyncRoot, "run-session", "status.json"), jm{"runId": "run-session", "mode": "single", "state": "complete", "startedAt": 100, "lastUpdate": 200, "sessionFile": sf, "steps": []any{jm{"agent": "worker", "status": "complete"}}})
		_, err := resolve(r, id("run-session"), resumeOptions{})
		wantErr(t, err, `session file must be a \.jsonl file`)
	})
	tw(t, f, "rejects malformed result metadata before using session fields", func(t *testing.T) {
		r := mk(t)
		writeJ(t, filepath.Join(r.resultsDir, "run-result.json"), jm{"id": "run-result", "agent": "worker", "success": true, "state": "complete", "results": []any{jm{"agent": "worker", "sessionFile": jm{"path": "session.jsonl"}}}})
		_, err := resolve(r, id("run-result"), resumeOptions{})
		wantErr(t, err, `results\[0\].sessionFile must be a string`)
	})
	tw(t, f, "rejects malformed result model metadata", func(t *testing.T) {
		r := mk(t)
		no := false
		writeJ(t, filepath.Join(r.resultsDir, "run-result-model.json"), jm{"id": "run-result-model", "agent": "worker", "success": true, "state": "complete", "results": []any{jm{"agent": "worker", "model": jm{"id": "bad"}}}})
		_, err := resolve(r, id("run-result-model"), resumeOptions{RequireSessionFile: &no})
		wantErr(t, err, `results\[0\].model must be a string`)
		writeJ(t, filepath.Join(r.resultsDir, "run-result-model.json"), jm{"id": "run-result-model", "agent": "worker", "success": true, "state": "complete", "results": []any{jm{"agent": "worker", "thinking": jm{"level": "high"}}}})
		_, err = resolve(r, id("run-result-model"), resumeOptions{RequireSessionFile: &no})
		wantErr(t, err, `results\[0\].thinking must be a string`)
	})
	tw(t, f, "rejects malformed status session ids", func(t *testing.T) {
		r := mk(t)
		writeJ(t, filepath.Join(r.asyncRoot, "run-session-id", "status.json"), jm{"runId": "run-session-id", "sessionId": jm{"value": "session"}, "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "running"}}})
		_, err := resolve(r, id("run-session-id"), resumeOptions{})
		wantErr(t, err, `sessionId must be a string`)
	})
	tw(t, f, "rejects malformed status model metadata", func(t *testing.T) {
		r := mk(t)
		p := filepath.Join(r.asyncRoot, "run-status-model", "status.json")
		writeJ(t, p, jm{"runId": "run-status-model", "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "running", "model": jm{"id": "bad"}}}})
		_, err := resolve(r, id("run-status-model"), resumeOptions{})
		wantErr(t, err, `steps\[0\].model must be a string`)
		writeJ(t, p, jm{"runId": "run-status-model", "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "running", "thinking": jm{"level": "high"}}}})
		_, err = resolve(r, id("run-status-model"), resumeOptions{})
		wantErr(t, err, `steps\[0\].thinking must be a string`)
	})
	tw(t, f, "returns live child metadata for a running child", func(t *testing.T) {
		r := mk(t)
		writeJ(t, filepath.Join(r.asyncRoot, "run-live", "status.json"), jm{"runId": "run-live", "mode": "single", "state": "running", "startedAt": 100, "lastUpdate": 100,
			"steps": []any{jm{"agent": "scout", "status": "running", "model": "openai/gpt-4.1", "thinking": "off"}}})
		target := resolveOK(t, r, id("run-live"), resumeOptions{})
		eq(t, target.kind, "live", "kind")
		eq(t, target.model, "openai/gpt-4.1", "model")
		eq(t, target.thinking, "off", "thinking")
	})
	tw(t, f, "enforces current-session ownership for status and result-only runs", func(t *testing.T) {
		r := mk(t)
		no := false
		running := func(session any) jm {
			m := jm{"runId": "", "mode": "single", "state": "running", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "running"}}}
			if session != nil {
				m["sessionId"] = session
			}
			return m
		}
		owned := running("session-a")
		owned["runId"] = "owned"
		writeJ(t, filepath.Join(r.asyncRoot, "owned", "status.json"), owned)
		sessionless := running(nil)
		sessionless["runId"] = "sessionless"
		writeJ(t, filepath.Join(r.asyncRoot, "sessionless", "status.json"), sessionless)
		writeJ(t, filepath.Join(r.resultsDir, "result-only.json"), jm{"id": "result-only", "sessionId": "session-a", "agent": "worker", "state": "complete", "success": true})
		complete := func(runID string, session any) jm {
			m := jm{"runId": runID, "mode": "single", "state": "complete", "startedAt": 100, "steps": []any{jm{"agent": "worker", "status": "complete"}}}
			if session != nil {
				m["sessionId"] = session
			}
			return m
		}
		writeJ(t, filepath.Join(r.asyncRoot, "mixed", "status.json"), complete("mixed", "session-a"))
		writeJ(t, filepath.Join(r.resultsDir, "mixed.json"), jm{"id": "mixed", "agent": "worker", "state": "complete", "success": true})
		writeJ(t, filepath.Join(r.asyncRoot, "mixed-reverse", "status.json"), complete("mixed-reverse", nil))
		writeJ(t, filepath.Join(r.resultsDir, "mixed-reverse.json"), jm{"id": "mixed-reverse", "sessionId": "session-a", "agent": "worker", "state": "complete", "success": true})

		eq(t, resolveOK(t, r, id("owned"), resumeOptions{SessionID: "session-a"}).kind, "live", "owned")
		_, err := resolve(r, id("owned"), resumeOptions{SessionID: "session-b"})
		wantErr(t, err, `not found in the active session`)
		_, err = resolve(r, id("sessionless"), resumeOptions{SessionID: "session-a"})
		wantErr(t, err, `not found in the active session`)
		eq(t, resolveOK(t, r, id("result-only"), resumeOptions{RequireSessionFile: &no, SessionID: "session-a"}).kind, "revive", "result-only")
		_, err = resolve(r, id("result-only"), resumeOptions{RequireSessionFile: &no, SessionID: "session-b"})
		wantErr(t, err, `not found in the active session`)
		_, err = resolve(r, id("mixed"), resumeOptions{RequireSessionFile: &no, SessionID: "session-a"})
		wantErr(t, err, `not found in the active session`)
		_, err = resolve(r, id("mixed-reverse"), resumeOptions{RequireSessionFile: &no, SessionID: "session-a"})
		wantErr(t, err, `not found in the active session`)
	})
	tw(t, f, "revives a completed child by index while a sibling async child is still running", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "done.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "run-partial", "status.json"), jm{"runId": "run-partial", "mode": "parallel", "state": "running", "startedAt": 100, "lastUpdate": 200,
			"steps": []any{jm{"agent": "done", "status": "complete", "sessionFile": sf}, jm{"agent": "active", "status": "running"}}})
		target := resolveOK(t, r, idx("run-partial", 0), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		eq(t, target.agent, "done", "agent")
		eq(t, target.sessionFile, sf, "sessionFile")
	})
	tw(t, f, "rejects pending indexed children in still-running async runs", func(t *testing.T) {
		r := mk(t)
		sf := session(t, r.root, "pending.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "run-pending", "status.json"), jm{"runId": "run-pending", "mode": "chain", "state": "running", "startedAt": 100, "lastUpdate": 200,
			"steps": []any{jm{"agent": "active", "status": "running"}, jm{"agent": "later", "status": "pending", "sessionFile": sf}}})
		_, err := resolve(r, idx("run-pending", 1), resumeOptions{})
		wantErr(t, err, `pending and has not started yet`)
	})
	tw(t, f, "resolves a completed multi-child run when an index and per-child session file are available", func(t *testing.T) {
		r := mk(t)
		first, second := session(t, r.root, "a.jsonl"), session(t, r.root, "b.jsonl")
		writeJ(t, filepath.Join(r.asyncRoot, "run-multi", "status.json"), jm{"runId": "run-multi", "mode": "chain", "state": "complete", "startedAt": 100, "lastUpdate": 200,
			"steps": []any{jm{"agent": "a", "status": "complete", "sessionFile": first}, jm{"agent": "b", "status": "complete", "sessionFile": second, "model": "anthropic/claude-sonnet-4", "thinking": "high"}}})
		_, err := resolve(r, id("run-multi"), resumeOptions{})
		wantErr(t, err, `Provide index to choose one`)
		target := resolveOK(t, r, idx("run-multi", 1), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		eq(t, target.agent, "b", "agent")
		eq(t, target.index, 1, "index")
		eq(t, target.sessionFile, second, "sessionFile")
		eq(t, target.model, "anthropic/claude-sonnet-4", "model")
		eq(t, target.thinking, "high", "thinking")
		_, err = resolve(r, resumeParams{ID: "run-multi", Index: f64p(1.5)}, resumeOptions{})
		wantErr(t, err, `index must be an integer`)
	})
	tw(t, f, "resolves a completed result-only child with per-child model metadata", func(t *testing.T) {
		r := mk(t)
		first, second := session(t, r.root, "a.jsonl"), session(t, r.root, "b.jsonl")
		writeJ(t, filepath.Join(r.resultsDir, "run-result-only.json"), jm{"id": "run-result-only", "mode": "parallel", "success": true, "state": "complete", "cwd": r.root,
			"results": []any{jm{"agent": "a", "success": true, "sessionFile": first, "model": "openai/gpt-4.1"}, jm{"agent": "b", "success": true, "sessionFile": second, "model": "anthropic/claude-sonnet-4", "thinking": "high"}}})
		_, err := resolve(r, id("run-result-only"), resumeOptions{})
		wantErr(t, err, `Provide index to choose one`)
		target := resolveOK(t, r, idx("run-result-only", 1), resumeOptions{})
		eq(t, target.kind, "revive", "kind")
		eq(t, target.agent, "b", "agent")
		eq(t, target.index, 1, "index")
		eq(t, target.cwd, r.root, "cwd")
		eq(t, target.sessionFile, second, "sessionFile")
		eq(t, target.model, "anthropic/claude-sonnet-4", "model")
		eq(t, target.thinking, "high", "thinking")
	})
	tw(t, f, "frames the revived follow-up with original run context", func(t *testing.T) {
		task := buildRevivedAsyncTask("run-old", "worker", "/tmp/session.jsonl", "What changed?")
		for _, want := range []string{"Original run: run-old", "Original agent: worker", "Original session file: /tmp/session.jsonl", "Follow-up:\nWhat changed?"} {
			if !strContains(task, want) {
				t.Errorf("task lacks %q: %s", want, task)
			}
		}
		if strContains(task, "async subagent conversation") {
			t.Error("task names an async subagent conversation")
		}
	})
}

func strContains(s, sub string) bool { return strings.Contains(s, sub) }

func TestResumeRefusesWhatItCannotHonor(t *testing.T) {
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	root := tmp(t)
	dirs := asyncDirs{Async: filepath.Join(root, "runs"), Results: filepath.Join(root, "results")}
	sf := filepath.Join(root, "session.jsonl")
	writeFile(t, sf, "")
	status := func(extra jm) {
		m := jm{"runId": "run-ceiling", "mode": "single", "state": "paused", "startedAt": 100, "lastUpdate": 200, "cwd": root, "steps": []any{jm{"agent": "worker", "status": "paused", "sessionFile": sf}}}
		for k, v := range extra {
			m[k] = v
		}
		writeJ(t, filepath.Join(dirs.Async, "run-ceiling", "status.json"), m)
	}
	descriptor := func(extra jm) {
		d := jm{"version": 1, "runFanoutBudget": createRunFanoutBudget(t, "run-ceiling", 64), "sourceRunId": "run-ceiling", "agent": "worker", "cwd": root, "systemPromptMode": "replace",
			"inheritGlobalContext": false, "inheritProjectContext": false, "inheritSkills": false, "outputMode": "inline", "maxSubagentDepth": 2, "share": false}
		for k, v := range extra {
			d[k] = v
		}
		writeJ(t, filepath.Join(dirs.Async, "run-ceiling", "recovery-descriptor.json"), d)
	}
	status(jm{"capabilityCeiling": jm{"version": 1, "allowedTools": []any{"read"}, "denyExtensions": true, "sources": []any{"run"}}})
	target, err := resolveAsyncResumeTarget(resumeParams{ID: "run-ceiling"}, dirs, resumeOptions{})
	eq(t, err, nil, "error")
	eq(t, target.unsupported, []string{"capabilityCeiling"}, "a ceiling cannot be dropped")
	status(nil)
	descriptor(jm{"sourceRunId": "another-run"})
	_, err = resolveAsyncResumeTarget(resumeParams{ID: "run-ceiling"}, dirs, resumeOptions{})
	hasMatch(t, err, "different source run")
	descriptor(jm{"agent": "another-agent"})
	_, err = resolveAsyncResumeTarget(resumeParams{ID: "run-ceiling"}, dirs, resumeOptions{})
	hasMatch(t, err, "not 'worker'")
	descriptor(jm{"token": "must-not-be-accepted"})
	_, err = resolveAsyncResumeTarget(resumeParams{ID: "run-ceiling"}, dirs, resumeOptions{})
	hasMatch(t, err, "unknown field 'token'")
}
