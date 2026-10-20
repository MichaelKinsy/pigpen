package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/workflow-receipt.test.ts.

func rcChild(key string, overrides ...any) *jsObject {
	c := jo("key", key, "ok", true, "agent", "reviewer", "runId", "run-"+key, "output", strings.Repeat("x", 100000),
		"structuredOutput", jo("report", strings.Repeat("x", 100000)), "artifactPaths", []any{"/tmp/" + key + ".md"},
		"requestedContext", "fresh", "resolvedContext", "fresh", "outputReference", "/tmp/"+key+".md",
		"resumability", jo("state", "resumable"), "continuation", jo("runIds", []any{"run-" + key}))
	return joOf(c, overrides...)
}

func rcHostStep(overrides ...any) *jsObject {
	return joOf(jo("version", float64(1), "kind", "host-step", "monitorKind", "gate", "id", "gate-1", "label", "Review gate", "state", "done", "verdict", "pass", "updatedAt", float64(10)), overrides...)
}

func rcBuild(t *testing.T, in WorkflowReceiptInput) *jsObject {
	t.Helper()
	r, err := BuildWorkflowReceipt(in)
	if err != nil {
		t.Fatalf("BuildWorkflowReceipt: %v", err)
	}
	return r
}

func rcRead(t *testing.T, root, id string) *jsObject {
	t.Helper()
	r, err := ReadWorkflowReceipt(root, id)
	if err != nil {
		t.Fatalf("ReadWorkflowReceipt: %v", err)
	}
	return r
}

func rcWrite(t *testing.T, dir string, receipt *jsObject) {
	t.Helper()
	if _, err := WriteWorkflowReceipt(dir, receipt); err != nil {
		t.Fatalf("WriteWorkflowReceipt: %v", err)
	}
}

func rcDir(t *testing.T, root, id string) string {
	t.Helper()
	d := filepath.Join(root, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func rcWriteRaw(t *testing.T, dir string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "workflow-receipt.json"), string(b))
}

func rcReadRawObject(t *testing.T, dir string) *jsObject {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "workflow-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func rcPath(o *jsObject, keys ...string) any {
	var cur any = o
	for _, k := range keys {
		m, ok := cur.(*jsObject)
		if !ok {
			return nil
		}
		cur = m.vals[k]
	}
	return cur
}

func rcFails(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Errorf("error = %v, want /%s/", err, pattern)
	}
}

func rcSummary(t *testing.T, in WorkflowChildSummaryInput) WorkflowChildSummary {
	t.Helper()
	s, err := WorkflowChildSummaryOf(in)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	return s
}

func TestWorkflowReceiptTwins(t *testing.T) {
	const f = "workflow-receipt"
	tw(t, f, "reads old receipt-v1 files without external adapter metadata", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-old")
		rcWriteRaw(t, dir, map[string]any{"version": 1, "workflowRunId": "workflow-old", "state": "complete", "createdAt": 10,
			"entries": map[string]any{"advisor": map[string]any{"key": "advisor", "latestRunId": "run-old", "resumability": map[string]any{"state": "resumable"}, "continuation": map[string]any{"runIds": []string{"run-old"}}}}})
		if rcPath(rcRead(t, root, "workflow-old"), "entries", "advisor", "externalAdapter") != nil {
			t.Fatal("externalAdapter appeared")
		}
	})
	tw(t, f, "round-trips named workflow resource provenance", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-resource")
		resource := jo("kind", "workflow", "name", "review", "version", float64(1), "invocation", "named", "expansion", "resolved", "id", "resource-1")
		ca := 10.0
		rcWrite(t, dir, rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-resource", State: "complete", Resource: resource, CreatedAt: &ca}))
		jsonEq(t, rcRead(t, root, "workflow-resource").vals["resource"], marshalJSON(resource, ""))
	})
	tw(t, f, "round-trips the canonical workflow argument digest and rejects malformed digests", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-args")
		digest := strings.Repeat("a", 64)
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-args", State: "complete", ArgsDigest: digest})
		rcWrite(t, dir, receipt)
		eq(t, rcRead(t, root, "workflow-args").vals["argsDigest"], digest, "digest")
		_, err := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: "workflow-args", State: "complete", ArgsDigest: "invalid"})
		rcFails(t, err, "argsDigest")
		bad := receipt.clone()
		bad.set("argsDigest", "invalid")
		rcWrite(t, dir, bad)
		_, err = ReadWorkflowReceipt(root, "workflow-args")
		rcFails(t, err, "argsDigest is invalid")
	})
	tw(t, f, "round-trips bounded host CI/gate state in terminal receipts", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-host")
		ca := 10.0
		step := rcHostStep("monitorKind", "ci", "id", "ci-1", "label", "CI checks", "provider", "opaque-provider", "state", "done", "verdict", "inconclusive", "reasonCode", "stale-head",
			"freshness", jo("expectedRef", "old", "observedRef", "new", "stale", true), "reportPath", "/private/report.json")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-host", State: "complete", HostSteps: []any{step}, CreatedAt: &ca})
		rcWrite(t, dir, receipt)
		jsonEq(t, rcRead(t, root, "workflow-host").vals["hostSteps"], marshalJSON(receipt.vals["hostSteps"], ""))
		eq(t, strings.Contains(marshalJSON(receipt, ""), "/private/report.json"), true, "report path")
	})
	tw(t, f, "reads inconclusive host monitor receipt state without a verdict", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-host-inconclusive")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-host-inconclusive", State: "complete"})
		step := rcHostStep()
		delete(step.vals, "verdict")
		step.keys = []string{"version", "kind", "monitorKind", "id", "label", "state", "updatedAt"}
		receipt.set("hostSteps", []any{step})
		rcWrite(t, dir, receipt)
		steps := rcRead(t, root, "workflow-host-inconclusive").vals["hostSteps"].([]any)
		if _, has := steps[0].(*jsObject).vals["verdict"]; has {
			t.Fatal("a verdict appeared")
		}
	})
	tw(t, f, "reads legacy Grok receipt metadata after the active profile is removed", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-old-grok")
		reason := "The legacy prompt-file adapter has no durable external session identity."
		raw := `{"version":1,"workflowRunId":"workflow-old-grok","state":"complete","createdAt":10,"entries":{"grok":{"key":"grok","resumability":{"state":"not-resumable","reason":"` + reason + `"},"continuation":{"runIds":[]},"externalAdapter":{"adapter":{"id":"grok-build","version":1,"executionMode":"one-shot-prompt-file"},"capabilities":{"stop":true,"steer":false,"resume":false,"structuredOutput":false,"toolEvents":false,"supervisor":"unsupported","forkContext":false,"extensionBindings":false},"safety":{"access":"read-only","authentication":"xai-api-key-required","permissionMode":"plan","tools":"read_file,grep,list_dir","deniedTools":"run_terminal_cmd,search_replace,Agent,Bash,Edit,Write,MCPTool","sandbox":"read-only","webSearch":false,"subagents":false,"config":"temporary-home","updates":"disabled","sessionPersistence":false},"handoff":{"mode":"fresh"},"supervisor":{"mode":"unsupported","reason":"Legacy adapter has no supervisor transport."},"nonResumableReason":"` + reason + `"}}}}`
		writeFile(t, filepath.Join(dir, "workflow-receipt.json"), raw)
		eq(t, rcPath(rcRead(t, root, "workflow-old-grok"), "entries", "grok", "externalAdapter", "adapter", "id"), "grok-build", "adapter id")
	})
	tw(t, f, "builds one metadata-only entry per workflow child", func(t *testing.T) {
		var children []*jsObject
		for i := 0; i < 1000; i++ {
			children = append(children, rcChild("child-"+jsNumber(float64(i))))
		}
		ca := 10.0
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "complete", Children: children, CreatedAt: &ca})
		entries := receipt.obj("entries")
		eq(t, len(entries.keys), len(children), "entries")
		jsonEq(t, entries.vals["child-0"], `{"key":"child-0","agent":"reviewer","requestedContext":"fresh","resolvedContext":"fresh","latestRunId":"run-child-0","resumability":{"state":"resumable"},"outputReference":"/tmp/child-0.md","continuation":{"runIds":["run-child-0"]}}`)
		serialized := marshalJSON(receipt, "")
		if regexp.MustCompile(`structuredOutput|artifactPaths|"output"`).MatchString(serialized) {
			t.Fatal("payload data in the receipt")
		}
		if len(serialized) >= 400000 {
			t.Fatalf("receipt metadata unexpectedly large: %d", len(serialized))
		}
	})
	tw(t, f, "round-trips durable acceptance recovery metadata in terminal receipts", func(t *testing.T) {
		recovery := jo("status", "available-for-review", "reason", "acceptance-metadata-rejected", "reportPath", "/tmp/writer-report.md", "reportHash", strings.Repeat("b", 64))
		ca := 10.0
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-recovery", State: "complete", Children: []*jsObject{rcChild("writer", "ok", false, "recovery", recovery)}, CreatedAt: &ca})
		root := tmp(t)
		rcWrite(t, rcDir(t, root, "workflow-recovery"), receipt)
		jsonEq(t, rcPath(rcRead(t, root, "workflow-recovery"), "entries", "writer", "acceptanceRecovery"), marshalJSON(recovery, ""))
	})
	tw(t, f, "persists structured partial outcomes for workflows and children", func(t *testing.T) {
		root := tmp(t)
		outcome := jo("state", "partial", "reason", "budget_exhausted")
		ca := 10.0
		rcWrite(t, rcDir(t, root, "workflow-budget"), rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-budget", State: "failed",
			Children: []*jsObject{rcChild("first"), rcChild("second", "ok", false, "terminalOutcome", outcome)}, TerminalOutcome: outcome, CreatedAt: &ca}))
		receipt := rcRead(t, root, "workflow-budget")
		jsonEq(t, receipt.vals["terminalOutcome"], marshalJSON(outcome, ""))
		jsonEq(t, rcPath(receipt, "entries", "second", "terminalOutcome"), marshalJSON(outcome, ""))
		if rcPath(receipt, "entries", "first", "terminalOutcome") != nil {
			t.Fatal("first has an outcome")
		}
	})
	tw(t, f, "persists a bounded terminal workflow-child summary without payload data", func(t *testing.T) {
		var trace []WorkflowScriptTraceEntry
		for i := 0; i < 64; i++ {
			trace = append(trace, WorkflowScriptTraceEntry{Operation: "run", Key: "child-" + jsNumber(float64(i)), State: "started"})
		}
		in := WorkflowChildSummaryInput{ParentToolCallID: "tool-call", WorkflowRunID: "workflow-1", WorkflowState: "failed", InventoryComplete: true, Trace: trace}
		started := time.Now()
		summary := rcSummary(t, in)
		for i := 0; i < 999; i++ {
			summary = rcSummary(t, in)
		}
		elapsed := time.Since(started)
		eq(t, len(summary.Children), 64, "children")
		eq(t, summary.Children[0].ChildID, "child-0", "first")
		eq(t, summary.Children[0].State, "failed", "state")
		b, _ := json.Marshal(summary)
		if len(b) >= 8000 {
			t.Fatalf("max-fanout summary too large: %d", len(b))
		}
		if elapsed.Milliseconds() >= 500 {
			t.Fatalf("1,000 max-fanout projections took %v", elapsed)
		}
		if regexp.MustCompile(`task|prompt|output|artifact|transcript|secret`).Match(b) {
			t.Fatal("payload word in the summary")
		}
		ca := 10.0
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "failed", WorkflowChildren: &summary, CreatedAt: &ca})
		root := tmp(t)
		rcWrite(t, rcDir(t, root, "workflow-1"), receipt)
		jsonEq(t, rcRead(t, root, "workflow-1").vals["workflowChildren"], string(b))
	})
	tw(t, f, "round-trips long parent tool-call ids while rejecting ids over the summary bound", func(t *testing.T) {
		parent := "call_" + strings.Repeat("x", 500)
		summary := rcSummary(t, WorkflowChildSummaryInput{ParentToolCallID: parent, WorkflowRunID: "workflow-long-tool-call", WorkflowState: "completed", InventoryComplete: true})
		root := tmp(t)
		rcWrite(t, rcDir(t, root, "workflow-long-tool-call"), rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-long-tool-call", State: "complete", WorkflowChildren: &summary}))
		eq(t, rcPath(rcRead(t, root, "workflow-long-tool-call"), "workflowChildren", "parentToolCallId"), parent, "parent")
		over := "call_" + strings.Repeat("x", 4091)
		eq(t, rcSummary(t, WorkflowChildSummaryInput{ParentToolCallID: over, WorkflowRunID: "workflow-long-tool-call", WorkflowState: "completed", InventoryComplete: true}).ParentToolCallID, over, "at the bound")
		_, err := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: over + "x", WorkflowRunID: "workflow-long-tool-call", WorkflowState: "completed", InventoryComplete: true})
		rcFails(t, err, "at most 4096 UTF-8 bytes")
		o := summaryObject(t, summary)
		o.set("parentToolCallId", over+"x")
		_, err = ParseWorkflowChildSummary(o)
		rcFails(t, err, "at most 4096 UTF-8 bytes")
	})
	tw(t, f, "uses workflow keys when flattened child indexes collide", func(t *testing.T) {
		summary := rcSummary(t, WorkflowChildSummaryInput{ParentToolCallID: "tool-call", WorkflowRunID: "workflow-1", WorkflowState: "completed", InventoryComplete: true,
			Children: []*jsObject{
				rcChild("review", "results", []any{jo("index", float64(0), "model", "provider/reviewer", "thinking", "high")}),
				rcChild("tests", "results", []any{jo("index", float64(0), "model", "provider/tester", "thinking", "low")}),
			}})
		var got [][2]string
		for _, c := range summary.Children {
			got = append(got, [2]string{c.ChildID, c.Model})
		}
		eq(t, got, [][2]string{{"review", "provider/reviewer"}, {"tests", "provider/tester"}}, "rows")
	})
	tw(t, f, "rejects a summary bound to a different workflow", func(t *testing.T) {
		summary := rcSummary(t, WorkflowChildSummaryInput{ParentToolCallID: "tool", WorkflowRunID: "other", WorkflowState: "completed", InventoryComplete: true})
		_, err := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "complete", WorkflowChildren: &summary})
		rcFails(t, err, "does not match its receipt")
	})
	tw(t, f, "round-trips bounded lane metadata and rejects mismatched receipt identity", func(t *testing.T) {
		lane := jo("version", float64(1), "key", "advisor", "mode", "review", "sourceRef", "owner/repo#1621", "claims", []any{"src/shared/types.ts"}, "outputPaths", []any{"reports/advisor.md"})
		ca := 10.0
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-lane", State: "complete", Children: []*jsObject{rcChild("advisor", "lane", lane)}, CreatedAt: &ca})
		root := tmp(t)
		dir := rcDir(t, root, "workflow-lane")
		rcWrite(t, dir, receipt)
		jsonEq(t, rcPath(rcRead(t, root, "workflow-lane"), "entries", "advisor", "lane"), marshalJSON(lane, ""))
		_, err := BuildWorkflowReceipt(WorkflowReceiptInput{WorkflowRunID: "workflow-lane", State: "complete", Children: []*jsObject{rcChild("advisor", "lane", joOf(lane, "key", "other"))}})
		rcFails(t, err, "does not match workflow key")
		malformed := rcReadRawObject(t, dir)
		malformed.obj("entries").obj("advisor").obj("lane").set("key", "other")
		rcWrite(t, dir, malformed)
		_, err = ReadWorkflowReceipt(root, "workflow-lane")
		rcFails(t, err, "does not match workflow key")
	})
	tw(t, f, "rejects non-string workflow-child summary identifiers", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-1")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "complete"})
		receipt.set("workflowChildren", jo("version", float64(1), "parentToolCallId", float64(123), "workflowRunId", true, "inventoryComplete", true, "workflowState", "completed", "children", []any{}))
		rcWrite(t, dir, receipt)
		_, err := ReadWorkflowReceipt(root, "workflow-1")
		rcFails(t, err, `workflowChildren\.parentToolCallId is invalid`)
	})
	tw(t, f, "adds bounded external adapter metadata without raw output or handoff content", func(t *testing.T) {
		runner := ResolveExternalCliRunnerStatus("", "review-cli", nil, nil)
		machine := jo("provider", "herdr", "id", "machine-1", "label", "workmac", "target", "host.example", "cwd", "/srv/repo", "remoteGit", jo("head", "abc123", "branch", "main", "dirty", true))
		process := jo("startedAt", float64(1), "stdoutPath", "/tmp/stdout.log", "stderrPath", "/tmp/stderr.log", "machine", machine)
		adapter := ExternalCliReceiptMetadata(runner, process, "/tmp/final.md")
		reason := adapter.vals["nonResumableReason"]
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-external", State: "complete",
			Children: []*jsObject{rcChild("advisor", "resumability", jo("state", "not-resumable", "reason", reason), "externalAdapter", adapter)}})
		serialized := marshalJSON(receipt, "")
		ea := func(keys ...string) any {
			return rcPath(receipt, append([]string{"entries", "advisor", "externalAdapter"}, keys...)...)
		}
		eq(t, ea("adapter", "id"), "external-cli", "adapter")
		eq(t, ea("capabilities", "stop"), true, "stop")
		eq(t, ea("capabilities", "supervisor"), "unsupported", "supervisor")
		eq(t, ea("handoff", "mode"), "fresh", "handoff")
		jsonEq(t, ea("machine"), marshalJSON(machine, ""))
		rcFails(t, errString(rcPath(receipt, "entries", "advisor", "resumability", "reason")), "no durable external session identity")
		if regexp.MustCompile(`artifactPaths|rawOutput|handoffText|contact_supervisor`).MatchString(serialized) {
			t.Fatal("forbidden content in the receipt")
		}
		if len(serialized) >= 2000 {
			t.Fatalf("external receipt metadata unexpectedly large: %d", len(serialized))
		}
		root := tmp(t)
		dir := rcDir(t, root, "workflow-external")
		rcWrite(t, dir, receipt)
		jsonEq(t, rcPath(rcRead(t, root, "workflow-external"), "entries", "advisor", "externalAdapter", "machine"), marshalJSON(machine, ""))
		malformed := rcReadRawObject(t, dir)
		malformed.obj("entries").obj("advisor").obj("externalAdapter").obj("machine").obj("remoteGit").set("dirty", "true")
		rcWrite(t, dir, malformed)
		_, err := ReadWorkflowReceipt(root, "workflow-external")
		rcFails(t, err, `machine\.remoteGit is invalid`)
	})
	tw(t, f, "fails closed for malformed external adapter receipt metadata", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-external-bad")
		adapter := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("", "review-cli", nil, nil), nil, "")
		bad := adapter.clone()
		bad.set("nonResumableReason", "")
		rcWriteRaw(t, dir, map[string]any{"version": 1, "workflowRunId": "workflow-external-bad", "state": "complete", "createdAt": 10, "entries": map[string]any{"advisor": map[string]any{
			"key": "advisor", "latestRunId": "run-advisor", "resumability": map[string]any{"state": "not-resumable", "reason": adapter.vals["nonResumableReason"]},
			"continuation": map[string]any{"runIds": []string{"run-advisor"}}, "externalAdapter": bad}}})
		_, err := ReadWorkflowReceipt(root, "workflow-external-bad")
		rcFails(t, err, `externalAdapter\.nonResumableReason is missing`)
		raw := rcReadRawObject(t, dir)
		extra := adapter.clone()
		extra.set("rawOutput", "do not persist")
		raw.obj("entries").obj("advisor").set("externalAdapter", extra)
		rcWrite(t, dir, raw)
		_, err = ReadWorkflowReceipt(root, "workflow-external-bad")
		rcFails(t, err, `externalAdapter has unsupported fields: rawOutput`)
	})
	tw(t, f, "writes and resolves one exact terminal receipt", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-1")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "complete", Children: []*jsObject{rcChild("advisor")}})
		receipt.set("workflowResolution", "settled-awaiting-resume")
		receipt.set("recovery", []any{jo("key", "advisor", "call", "runs.run", "resume", jo("workflowRunId", "workflow-1", "key", "advisor", "latest", true), "taskRequired", true)})
		path, err := WriteWorkflowReceipt(dir, receipt)
		want, _ := WorkflowReceiptPath(root, "workflow-1")
		eq(t, err, nil, "write")
		eq(t, path, want, "path")
		jsonEq(t, rcRead(t, root, "workflow-1"), marshalJSON(receipt, ""))
		validations := 0
		runID, err := ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-1", Key: "advisor", Latest: true}, root, func(v string) error {
			validations++
			eq(t, v, "run-advisor", "validated run")
			return nil
		})
		eq(t, err, nil, "resolve")
		eq(t, runID, "run-advisor", "run id")
		eq(t, validations, 1, "validations")
		invalid := receipt.clone()
		rec := invalid.vals["recovery"].([]any)[0].(*jsObject).clone()
		rec.set("resume", jo("workflowRunId", "other", "key", "advisor", "latest", true))
		invalid.set("recovery", []any{rec})
		rcWriteRaw(t, dir, invalid)
		_, err = ReadWorkflowReceipt(root, "workflow-1")
		rcFails(t, err, `recovery\[0\] does not identify a resumable entry`)
	})
	tw(t, f, "explains a missing receipt when workflow status or events remain visible", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-active")
		writeFile(t, filepath.Join(dir, "status.json"), `{"runId":"workflow-active","state":"running"}`)
		writeFile(t, filepath.Join(dir, "events.jsonl"), `{"type":"subagent.workflow.child.completed","runId":"workflow-active","childRunId":"run-advisor"}`+"\n")
		_, err := ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-active", Key: "advisor", Latest: true}, root, nil)
		rcFails(t, err, `Workflow receipt 'workflow-active' is not available because the workflow may still be active or terminal receipt writing failed\. Use direct child run IDs from status/events for direct resume after the normal retained-child checks\.`)
	})
	tw(t, f, "fails closed for missing, non-resumable, and stale receipt references", func(t *testing.T) {
		root := tmp(t)
		_, err := ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "missing", Key: "advisor", Latest: true}, root, nil)
		rcFails(t, err, "not found")
		dir := rcDir(t, root, "workflow-1")
		rcWrite(t, dir, rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "failed", Children: []*jsObject{rcChild("advisor", "resumability", jo("state", "not-resumable", "reason", "session removed"))}}))
		_, err = ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-1", Key: "missing", Latest: true}, root, nil)
		rcFails(t, err, "no child key 'missing'")
		_, err = ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-1", Key: "advisor", Latest: true}, root, nil)
		rcFails(t, err, "not resumable: session removed")
		stale := rcReadRawObject(t, dir)
		stale.obj("entries").obj("advisor").set("latestRunId", "other-run")
		rcWrite(t, dir, stale)
		_, err = ReadWorkflowReceipt(root, "workflow-1")
		rcFails(t, err, "stale: latestRunId")
		_, err = ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-1", Key: "advisor", Latest: false}, root, nil)
		rcFails(t, err, "requires latest: true")
	})
	tw(t, f, "refuses keyed resume for a one-shot external CLI with its adapter reason", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-external")
		adapter := ExternalCliReceiptMetadata(ResolveExternalCliRunnerStatus("", "review-cli", nil, nil), nil, "")
		rcWrite(t, dir, rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-external", State: "complete",
			Children: []*jsObject{rcChild("advisor", "externalAdapter", adapter, "resumability", jo("state", "not-resumable", "reason", adapter.vals["nonResumableReason"]))}}))
		_, err := ResolveWorkflowReceiptResume(WorkflowReceiptResumeReference{WorkflowRunID: "workflow-external", Key: "advisor", Latest: true}, root, nil)
		rcFails(t, err, "not resumable: The one-shot stdin adapter has no durable external session identity")
	})
	tw(t, f, "persists a bounded child session name from async status", func(t *testing.T) {
		summary := rcSummary(t, WorkflowChildSummaryInput{ParentToolCallID: "tool-call", WorkflowRunID: "workflow-1", WorkflowState: "completed", InventoryComplete: true,
			Steps: []WorkflowStepStatus{{Agent: "reviewer", SessionName: "reviewer: Inspect the changed auth middleware", WorkflowKey: "review", Status: "complete"}}})
		eq(t, summary.Children[0].SessionName, "reviewer: Inspect the changed auth middleware", "session name")
	})
	tw(t, f, "rejects unbounded session names in persisted workflow summaries", func(t *testing.T) {
		root := tmp(t)
		dir := rcDir(t, root, "workflow-1")
		receipt := rcBuild(t, WorkflowReceiptInput{WorkflowRunID: "workflow-1", State: "complete"})
		receipt.set("workflowChildren", jo("version", float64(1), "parentToolCallId", "tool", "workflowRunId", "workflow-1", "inventoryComplete", true, "workflowState", "completed",
			"children", []any{jo("childId", "review", "state", "completed", "sessionName", strings.Repeat("x", 257))}))
		rcWrite(t, dir, receipt)
		_, err := ReadWorkflowReceipt(root, "workflow-1")
		rcFails(t, err, "sessionName is invalid")
	})
}

func errString(v any) error {
	s, _ := v.(string)
	return &stringError{s}
}

type stringError struct{ s string }

func (e *stringError) Error() string { return e.s }
