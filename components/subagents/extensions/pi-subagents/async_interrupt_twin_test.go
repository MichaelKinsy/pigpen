//go:build unix

package pi_subagents_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// Twins of the control cases of async-interrupt-action.test.ts: interrupt and stop of background runs found on disk, through
// the fake host and the real actions.

type killCall struct{ pid, signal int }

func TestAsyncInterruptActionTwins(t *testing.T) {
	const f = "async-interrupt-action"
	writeJ := func(t *testing.T, p string, v any) {
		t.Helper()
		b, _ := json.MarshalIndent(v, "", "  ")
		writeFile(t, p, string(b))
	}
	runningRun := func(t *testing.T, e asyncEnv, runID string, extra map[string]any) string {
		dir := filepath.Join(e.temp, "async-subagent-runs", runID)
		status := map[string]any{"runId": runID, "mode": "single", "state": "running", "sessionId": "/sessions/parent-a.jsonl", "pid": 12345, "cwd": os.TempDir(), "startedAt": 100,
			"lastUpdate": time_now(), "steps": []any{map[string]any{"agent": "worker", "status": "running", "startedAt": 100}}}
		for k, v := range extra {
			status[k] = v
		}
		writeJ(t, filepath.Join(dir, "status.json"), status)
		return dir
	}
	call := func(t *testing.T, h *Host, params map[string]any) (text, failure string) {
		raw, failure := h.Tool("subagent", params)
		if failure != "" {
			return failure, failure
		}
		return toolText(t, raw), ""
	}
	recordKills := func(t *testing.T) *[]killCall {
		var kills []killCall
		sub.SetAsyncKill(t, func(pid, signal int) error { kills = append(kills, killCall{pid, signal}); return nil })
		return &kills
	}
	match := func(t *testing.T, pattern, s string) {
		t.Helper()
		if !regexp.MustCompile(pattern).MatchString(s) {
			t.Errorf("%q does not match %s", s, pattern)
		}
	}
	for _, c := range []struct{ title, reason string }{
		{"routes debug.run to async lifecycle debug, not live foreground status", "the debug.run lifecycle report (inspectSubagentStatus, run-status.ts) of foreground-tracked runs belongs to the status-inspection and foreground-executor slices, not ported"},
		{"renders run lifecycle debug without transcript content", "the debug.run lifecycle report renders the workflow children and capacity of a run (run-status.ts debug view, workflow slice), not ported"},
		{"steers a live workflow-owned foreground child by child id", "steering (action steer) belongs to the steering and supervisor-channel slice, not ported; a steer is refused"},
		{"steers a foreground workflow child by stable key", "workflow-key steering of a foreground child (workflow-foreground-steering.ts) belongs to the workflow and steering slices, not ported"},
		{"retries a foreground workflow-key steer until the child session is running", "the retry of a workflow-key steer belongs to the workflow and steering slices, not ported"},
		{"routes a stable key to async steering without recovery", "async steering of a workflow child by stable key belongs to the workflow and steering slices, not ported"},
		{"rejects workflow-key steering for a one-shot external CLI child", "external CLI runners and workflow-key steering are separate slices, not ported"},
		{"returns missed when a terminal keyed child cannot accept a retained follow-up", "the retained follow-up of a terminal workflow child (steering slice) is not ported"},
		{"returns missed when a keyed child's raw running status reconciles terminal", "steering by workflow key against a reconciled status (steering slice) is not ported"},
		{"reports a failed steer when the child session rejects it", "the in-process child session a foreground steer is delivered to does not exist in the port (it runs a pig child process); steering is refused"},
		{"rejects a steer request when the child session is not steerable", "foreground child sessions and their steer support do not exist in the port; steering is refused"},
		{"queues follow-up steering through the child session", "follow-up steering through an in-process child session does not exist in the port; steering is refused"},
		{"steers the unique live foreground child by workflow id and directory", "workflow ids and foreground child controls belong to the workflow and steering slices, not ported"},
		{"exposes exact async child targets when owned workflow steering has no foreground route", "async workflow steering hints belong to the workflow and steering slices, not ported"},
		{"keeps async workflow hints projection-only, selective, and scoped to the active owner", "async workflow hints belong to the workflow and steering slices, not ported"},
		{"rejects ambiguous workflow steering without choosing a foreground child", "workflow steering resolution belongs to the workflow and steering slices, not ported"},
		{"rejects a terminal workflow instead of queuing to its outer inbox", "workflow steering inboxes belong to the workflow and steering slices, not ported"},
		{"queues steering for a running async child", "the steer control inbox (consumeSteerRequests) belongs to the steering slice, not ported; a steer of a background child is refused"},
		{"queues steering for a running async child by directory", "the steer control inbox addressed by run directory belongs to the steering slice, not ported"},
		{"queues steering for a pending indexed async child", "steering a pending indexed child belongs to the steering slice, not ported"},
		{"rejects steering async runs outside the active session", "steering (and its session check) belongs to the steering slice, not ported; every steer is refused"},
		{"rejects runs.steer for the one-shot external CLI with its adapter reason", "external CLI adapters and runs.steer belong to separate slices, not ported"},
		{"rejects runs.steer for an old persisted external CLI status shape", "persisted external CLI statuses and runs.steer belong to separate slices, not ported"},
	} {
		tskip(t, f, c.title, c.reason)
	}

	tw(t, f, "requests an interrupt without signaling a running async runner", func(t *testing.T) {
		e := newAsyncEnv(t)
		kills := recordKills(t)
		h := e.host(t)
		runID := "interrupt-disk-aaaa1111"
		dir := runningRun(t, e, runID, nil)
		text, failure := call(t, h, map[string]any{"action": "interrupt", "id": runID})
		eq(t, failure, "", "failure")
		match(t, "Interrupt requested for async run "+runID, text)
		if _, err := os.Stat(filepath.Join(dir, "control", "interrupt.json")); err != nil {
			t.Errorf("interrupt.json: %v", err)
		}
		eq(t, *kills, []killCall{{12345, 0}}, "kills")
	})
	tw(t, f, "rejects workflow interrupt instead of signaling a shared host pid", func(t *testing.T) {
		e := newAsyncEnv(t)
		kills := recordKills(t)
		h := e.host(t)
		runID := "interrupt-workflow-host-aaaa"
		dir := runningRun(t, e, runID, map[string]any{"mode": "workflow", "pid": os.Getpid()})
		text, failure := call(t, h, map[string]any{"action": "interrupt", "id": runID})
		match(t, fmt.Sprintf(`Interrupt is unsupported for async workflow %s; use stop instead\.`, runID), text+failure)
		if _, err := os.Stat(filepath.Join(dir, "control", "interrupt.json")); err == nil {
			t.Error("a pause request was written")
		}
		eq(t, *kills, []killCall{{os.Getpid(), 0}}, "kills")
	})
	tw(t, f, "rejects interrupt for a running external runner without writing a pause request", func(t *testing.T) {
		for _, runner := range []map[string]any{{"type": "external-cli"}, {"type": "external-job", "provider": "surf-oracle", "options": map[string]any{}}} {
			e := newAsyncEnv(t)
			sub.SetAsyncKill(t, func(pid, signal int) error { return nil })
			h := e.host(t)
			runID := "interrupt-external-" + runner["type"].(string) + "-aaaa"
			dir := runningRun(t, e, runID, map[string]any{"steps": []any{map[string]any{"agent": "worker", "status": "running", "startedAt": 100, "runner": runner}}})
			text, failure := call(t, h, map[string]any{"action": "interrupt", "id": runID})
			match(t, fmt.Sprintf(`Interrupt is unsupported for external async run %s; use stop instead\.`, runID), text+failure)
			if _, err := os.Stat(filepath.Join(dir, "control", "interrupt.json")); err == nil {
				t.Error("a pause request was written")
			}
			var st map[string]any
			b, _ := os.ReadFile(filepath.Join(dir, "status.json"))
			_ = json.Unmarshal(b, &st)
			eq(t, st["state"], "running", "state")
		}
	})
	tw(t, f, "stops a running async run resolved from disk", func(t *testing.T) {
		e := newAsyncEnv(t)
		kills := recordKills(t)
		h := e.host(t)
		runID := "stop-disk-aaaa1111"
		dir := runningRun(t, e, runID, nil)
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID})
		eq(t, failure, "", "failure")
		match(t, "Stop requested for async run "+runID, text)
		entries, err := os.ReadDir(filepath.Join(dir, "control", "stop-requests"))
		eq(t, err, nil, "stop-requests")
		eq(t, len(entries), 1, "stop requests")
		b, _ := os.ReadFile(filepath.Join(dir, "control", "stop-requests", entries[0].Name()))
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		eq(t, payload["type"], "stop", "payload type")
		eq(t, *kills, []killCall{{12345, 0}}, "kills")
	})
	tw(t, f, "rejects a stop after a running run closes its stop inbox", func(t *testing.T) {
		e := newAsyncEnv(t)
		recordKills(t)
		h := e.host(t)
		runID := "stop-closed-aaaa1111"
		dir := runningRun(t, e, runID, nil)
		writeJ(t, filepath.Join(dir, "control", "stop-inbox-closed.json"), map[string]any{"version": 1, "closedAt": 1})
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID})
		match(t, `Failed to stop async run.*Retry stop after runner shutdown is observed`, text+failure)
		if _, err := os.Stat(filepath.Join(dir, "control", "stop-requests")); err == nil {
			t.Error("a stop request was written")
		}
	})

	observed := func(runID, runner string) map[string]any {
		return map[string]any{"version": 1, "state": "observed", "runId": runID, "runnerProcessInstanceId": runner, "observedAt": 300,
			"instances": []any{map[string]any{"kind": "runner", "processInstanceId": runner, "closeObservedAt": 300, "exitCode": 0, "signal": nil}}}
	}
	pausedRun := func(t *testing.T, e asyncEnv, runID string) string {
		dir := runningRun(t, e, runID, map[string]any{"state": "paused", "pid": nil,
			"processTerminal": map[string]any{"version": 1, "state": "pending", "runId": runID, "runnerProcessInstanceId": "runner-a", "resumeDisposition": "resumable"},
			"steps": []any{
				map[string]any{"agent": "done", "status": "completed", "startedAt": 50, "endedAt": 90, "exitCode": 0},
				map[string]any{"agent": "paused", "status": "paused", "startedAt": 100, "endedAt": 200, "exitCode": 0, "processTerminal": map[string]any{"version": 1, "state": "not-started", "runId": runID, "childIndex": 1, "runnerProcessInstanceId": "runner-a", "resumeDisposition": "resumable"}}}})
		delete(statusOf(t, dir), "pid")
		st := statusOf(t, dir)
		delete(st, "pid")
		writeJ(t, filepath.Join(dir, "status.json"), st)
		if err := sub.WriteAsyncResultFile(sub.ResultFilePath(filepath.Join(e.temp, "async-subagent-results"), runID), map[string]any{"id": runID, "sessionId": "/sessions/parent-a.jsonl", "state": "paused", "success": false,
			"summary": "Paused after interrupt.", "exitCode": 0, "results": []any{
				map[string]any{"agent": "done", "output": "done", "success": true, "exitCode": 0},
				map[string]any{"agent": "paused", "output": "paused", "success": false, "exitCode": 0, "interrupted": true}}}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	readResult := func(t *testing.T, e asyncEnv, runID string) map[string]any {
		var m map[string]any
		b, err := os.ReadFile(filepath.Join(e.temp, "async-subagent-results", runID+".json"))
		eq(t, err, nil, "result file")
		_ = json.Unmarshal(b, &m)
		return m
	}
	tw(t, f, "seals a paused whole run only with exact observed runner proof", func(t *testing.T) {
		e := newAsyncEnv(t)
		recordKills(t)
		h := e.host(t)
		runID := "stop-paused-aaaa1111"
		dir := pausedRun(t, e, runID)
		writeJ(t, filepath.Join(dir, "process-terminal.json"), observed(runID, "runner-a"))
		proofBefore, _ := os.ReadFile(filepath.Join(dir, "process-terminal.json"))
		if _, err := sub.AcquireCapacity("/sessions/parent-a.jsonl", runID, dir, "runner-a"); err != nil {
			t.Fatal(err)
		}
		if _, err := sub.AcquireCapacity("/sessions/parent-a.jsonl", runID+"-blocked", dir+"-blocked", ""); err == nil {
			t.Fatal("a second slot was granted at limit 1")
		}
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID, "childId": "paused"})
		if failure == "" && !strings.Contains(text, "paused") {
			t.Logf("child stop: %s", text)
		}
		if _, err := os.Stat(filepath.Join(dir, "control", "stop-requests")); err == nil {
			t.Error("a paused child stop wrote a request")
		}
		writeJ(t, filepath.Join(dir, "control", "stop-inbox-closed.json"), map[string]any{"version": 1, "closedAt": 1})
		text, failure = call(t, h, map[string]any{"action": "stop", "id": runID})
		eq(t, failure, "", "failure")
		match(t, "Stopped paused async run", text)
		if _, err := os.Stat(filepath.Join(dir, "control", "stop-requests")); err == nil {
			t.Error("a stop request was written for a closed inbox")
		}
		st := statusOf(t, dir)
		res := readResult(t, e, runID)
		eq(t, st["state"], "stopped", "state")
		steps := st["steps"].([]any)
		eq(t, steps[0].(map[string]any)["status"], "completed", "first step")
		eq(t, steps[1].(map[string]any)["status"], "stopped", "second step")
		eq(t, res["state"], "stopped", "result state")
		results := res["results"].([]any)
		eq(t, results[0].(map[string]any)["output"], "done", "first result output")
		eq(t, results[1].(map[string]any)["stopped"], true, "second result stopped")
		proofAfter, _ := os.ReadFile(filepath.Join(dir, "process-terminal.json"))
		eq(t, string(proofAfter), string(proofBefore), "proof untouched")
		_, failure2 := call(t, h, map[string]any{"action": "stop", "id": runID})
		if failure2 == "" {
			text2, _ := call(t, h, map[string]any{"action": "stop", "id": runID})
			if strings.Contains(text2, "Stopped paused") {
				t.Error("a stopped run was stopped again")
			}
		}
		eq(t, statusOf(t, dir)["steps"].([]any)[0].(map[string]any)["status"], "completed", "first step after the repeat")
		next, err := sub.AcquireCapacity("/sessions/parent-a.jsonl", runID+"-next", dir+"-next", "")
		eq(t, err, nil, "the slot was released")
		if next != nil {
			eq(t, next(), true, "rollback")
		}
	})
	tskip(t, f, "seals stale-repaired paused status without losing current terminal authority", "the case injects a concurrent writer into the reconciler (reconcileAsyncRun's third argument, a test seam of the original) while it repairs a stale status; the Go reconciler takes no such callback and its repair is serialized with the supervisor, so the race it simulates cannot be staged")
	tskip(t, f, "keeps stopped publication discoverable when terminal indexing runs out of space", "the case monkey-patches fs.renameSync to fail the terminal index with ENOSPC; the Go port writes its terminal index through its own atomic writer (no replaceable fs binding), whose capacity-error retry is covered by the active-run-index twins")
	tw(t, f, "keeps a paused run resumable when proof is ${proofCase}", func(t *testing.T) {
		for _, proofCase := range []string{"missing", "pending", "unknown", "not-started", "malformed", "wrong-run", "wrong-runner"} {
			e := newAsyncEnv(t)
			recordKills(t)
			h := e.host(t)
			runID := "stop-paused-" + proofCase + "-aaaa"
			dir := pausedRun(t, e, runID)
			proof := filepath.Join(dir, "process-terminal.json")
			switch proofCase {
			case "pending":
				writeJ(t, proof, map[string]any{"version": 1, "state": "pending", "runId": runID, "runnerProcessInstanceId": "runner-a"})
			case "unknown":
				writeJ(t, proof, map[string]any{"version": 1, "state": "unknown", "runId": runID, "runnerProcessInstanceId": "runner-a", "reason": "process-tree-unverified"})
			case "not-started":
				writeJ(t, proof, map[string]any{"version": 1, "state": "not-started", "runId": runID, "runnerProcessInstanceId": "runner-a"})
			case "malformed":
				writeFile(t, proof, "not json")
			case "wrong-run":
				writeJ(t, proof, observed("other-run", "runner-a"))
			case "wrong-runner":
				writeJ(t, proof, observed(runID, "runner-b"))
			}
			text, failure := call(t, h, map[string]any{"action": "stop", "id": runID})
			match(t, `Stop request persisted.*Retry stop`, text+failure)
			entries, err := os.ReadDir(filepath.Join(dir, "control", "stop-requests"))
			if err != nil || len(entries) == 0 {
				t.Errorf("%s: no stop request persisted (%v)", proofCase, err)
			}
			eq(t, statusOf(t, dir)["state"], "paused", proofCase+" status")
			eq(t, readResult(t, e, runID)["state"], "paused", proofCase+" result")
		}
	})
	for _, c := range []struct{ title, reason string }{
		{"stops a reload-recovered workflow through the durable control channel", "workflow runs and their controllers belong to the workflow slice, not ported"},
	} {
		tskip(t, f, c.title, c.reason)
	}
	tw(t, f, "writes child-scoped stop requests for a running async run", func(t *testing.T) {
		e := newAsyncEnv(t)
		recordKills(t)
		h := e.host(t)
		runID := "stop-child-aaaa1111"
		dir := runningRun(t, e, runID, map[string]any{"steps": []any{
			map[string]any{"agent": "first", "status": "running", "runId": "child-a", "startedAt": 100},
			map[string]any{"agent": "second", "status": "running", "workflowKey": "review", "startedAt": 100}}})
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID, "childId": "review"})
		eq(t, failure, "", "failure")
		match(t, "Stop requested for child review", text)
		entries, err := os.ReadDir(filepath.Join(dir, "control", "stop-requests"))
		eq(t, err, nil, "stop-requests")
		eq(t, len(entries), 1, "requests")
		b, _ := os.ReadFile(filepath.Join(dir, "control", "stop-requests", entries[0].Name()))
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		eq(t, req["type"], "stop", "type")
		eq(t, req["source"], "stop-action", "source")
		eq(t, req["targetIndex"], 1.0, "targetIndex")
		eq(t, req["childId"], "review", "childId")
		if _, ok := req["ts"].(float64); !ok {
			t.Errorf("ts %v", req["ts"])
		}
	})
	for _, c := range []struct{ title, reason string }{
		{"/subagents-stop rejects a nested-only child without writing a parent target request", "the /subagents-stop slash command and nested child routes belong to the slash-commands and nested-run slices, not ported"},
		{"dismisses only a reload-recovered running workflow without terminating work", "dismiss (a Fleet view action on workflows) belongs to the terminal-views and workflow slices, not ported"},
		{"rejects transcript view for display-dismissed workflows from another session", "the transcript view of dismissed workflows belongs to the terminal-views slice, not ported"},
		{"shows terminal status when a result appears after display dismissal", "display dismissal of workflows belongs to the terminal-views slice, not ported"},
		{"rejects dismiss when a stale running workflow has a terminal result", "dismiss of workflows belongs to the terminal-views and workflow slices, not ported"},
		{"rejects dismiss from child-safe fanout mode", "dismiss and the child-safe fan-out mode belong to the terminal-views and workflow slices, not ported"},
		{"rejects dismiss for live-controller, non-workflow, terminal, and other-session runs", "dismiss of runs (live workflow controllers) belongs to the terminal-views and workflow slices, not ported"},
	} {
		tskip(t, f, c.title, c.reason)
	}
	tw(t, f, "does not stop a different async run when the requested id is missing", func(t *testing.T) {
		e := newAsyncEnv(t)
		recordKills(t)
		h := e.host(t)
		dir := runningRun(t, e, "stop-existing-aaaa1111", nil)
		text, failure := call(t, h, map[string]any{"action": "stop", "id": "missing-run"})
		match(t, `Run not found|No stoppable async run found in this session`, text+failure)
		if _, err := os.Stat(filepath.Join(dir, "control", "stop.json")); err == nil {
			t.Error("another run was stopped")
		}
		if _, err := os.Stat(filepath.Join(dir, "control", "stop-requests")); err == nil {
			t.Error("another run was stopped")
		}
	})
	tw(t, f, "stops a queued async run by writing the portable request", func(t *testing.T) {
		e := newAsyncEnv(t)
		kills := recordKills(t)
		h := e.host(t)
		runID := "stop-queued-aaaa1111"
		dir := runningRun(t, e, runID, map[string]any{"state": "queued", "steps": []any{map[string]any{"agent": "worker", "status": "pending"}}})
		st := statusOf(t, dir)
		delete(st, "pid")
		writeJ(t, filepath.Join(dir, "status.json"), st)
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID})
		eq(t, failure, "", "failure")
		match(t, "Stop requested for async run "+runID, text)
		entries, err := os.ReadDir(filepath.Join(dir, "control", "stop-requests"))
		eq(t, err, nil, "stop-requests")
		eq(t, len(entries), 1, "requests")
		eq(t, len(*kills), 0, "a queued stop signals no process")
	})
	tw(t, f, "rejects stop for async runs outside the active session", func(t *testing.T) {
		e := newAsyncEnv(t)
		recordKills(t)
		h := e.host(t)
		runID := "stop-other-session-aaaa1111"
		dir := runningRun(t, e, runID, map[string]any{"sessionId": "other-session"})
		text, failure := call(t, h, map[string]any{"action": "stop", "id": runID})
		match(t, `active session`, text+failure)
		if _, err := os.Stat(filepath.Join(dir, "control", "stop.json")); err == nil {
			t.Error("a foreign run was stopped")
		}
		if _, err := os.Stat(filepath.Join(dir, "control", "stop-requests")); err == nil {
			t.Error("a foreign run was stopped")
		}
	})
	tw(t, f, "does not report success for stale running status with a dead pid", func(t *testing.T) {
		e := newAsyncEnv(t)
		sub.SetAsyncKill(t, func(pid, signal int) error { return sub.ErrNoSuchProcess })
		h := e.host(t)
		runID := "interrupt-esrch-aaaa1111"
		dir := runningRun(t, e, runID, nil)
		text, failure := call(t, h, map[string]any{"action": "interrupt", "id": runID})
		match(t, `No running async run with an interrupt-capable pid`, text+failure)
		if _, err := os.Stat(filepath.Join(dir, "control", "interrupt.json")); err == nil {
			t.Error("an interrupt was written for a dead run")
		}
		eq(t, statusOf(t, dir)["state"], "failed", "state")
	})
}

func statusOf(t *testing.T, dir string) map[string]any {
	t.Helper()
	var m map[string]any
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &m)
	return m
}
