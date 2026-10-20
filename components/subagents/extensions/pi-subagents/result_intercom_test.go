package pi_subagents

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Twins of result-intercom.test.ts.

func TestResultIntercomTwins(t *testing.T) {
	const f = "result-intercom"
	i0, i1 := 0, 1
	tw(t, f, "does not promote running children to grouped completion", func(t *testing.T) {
		equal(t, resolveSubagentResultStatus(map[string]any{"state": "running"}), "running", "running")
		equal(t, resolveSubagentResultStatus(map[string]any{"state": "running", "success": true}), "running", "running with success")
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "parent", RunID: "workflow", Mode: "workflow", Source: "async", Children: []resultIntercomChild{
			{Agent: "finished", Status: "completed", Summary: "done", OutputState: "present"}, {Agent: "launched", Status: "running", Summary: "", OutputState: "absent"}}})
		equal(t, [2]string{p.Status, p.Summary}, [2]string{"running", "1 running, 1 completed"}, "payload")
	})
	tw(t, f, "builds one grouped intercom payload with status counts and child sections", func(t *testing.T) {
		steps := 4
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "subagent-chat-main", RunID: "run-123", Mode: "chain", Source: "foreground", ChainSteps: &steps, Children: []resultIntercomChild{
			{Agent: "reviewer-a", Status: "completed", Summary: "Completed checks", ArtifactPath: "/tmp/a.md", SessionPath: "/tmp/a-session.jsonl", IntercomTarget: "subagent-reviewer-a-run-123-1"},
			{Agent: "reviewer-b", Status: "failed", Summary: "Failed checks", ArtifactPath: "/tmp/b.md"}}})
		equal(t, [3]any{p.Status, p.Summary, len(p.Children)}, [3]any{"failed", "1 completed, 1 failed", 2}, "payload")
		for _, re := range []string{"(?m)^subagent results", "Run: run-123", "Mode: chain", "Process status: failed", "Children: 1 completed, 1 failed", `Outputs: 2 unknown \(semantic adequacy unassessed\)`,
			"Chain steps: 4", "Intercom targets below identify child sessions used while they were running", "1\\. reviewer-a — process completed · output unknown",
			"Run intercom target: subagent-reviewer-a-run-123-1", "2\\. reviewer-b — process failed · output unknown", `Output artifact: /tmp/a\.md`, `Session: /tmp/a-session\.jsonl`} {
			polMatches(t, p.Message, re)
		}
	})
	tw(t, f, "advertises async revive only for single-child results with an existing session", func(t *testing.T) {
		session := filepath.Join(t.TempDir(), "session.jsonl")
		os.WriteFile(session, nil, 0o644)
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-single", Mode: "single", Source: "async", AsyncID: "run-single",
			Children: []resultIntercomChild{{Agent: "worker", Status: "completed", Summary: "done", SessionPath: session}}})
		polMatches(t, p.Message, `Revive: subagent\(\{ action: "resume", id: "run-single", message: "\.\.\." \}\)`)
		polNotMatches(t, p.Message, "unsupported for multi-child")
	})
	tw(t, f, "advertises indexed revive for multi-child async results with existing child sessions", func(t *testing.T) {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")
		os.WriteFile(a, nil, 0o644)
		os.WriteFile(b, nil, 0o644)
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-multi", Mode: "parallel", Source: "async", AsyncID: "run-multi",
			Children: []resultIntercomChild{{Agent: "a", Status: "completed", Summary: "done", SessionPath: a}, {Agent: "b", Status: "completed", Summary: "done", SessionPath: b}}})
		polMatches(t, p.Message, `Revive child: subagent\(\{ action: "resume", id: "run-multi", index: 0, message: "\.\.\." \}\)`)
		polNotMatches(t, p.Message, "unsupported for multi-child")
	})
	tw(t, f, "does not advertise async revive for missing child session files", func(t *testing.T) {
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-missing-session", Mode: "single", Source: "async", AsyncID: "run-missing-session",
			Children: []resultIntercomChild{{Agent: "worker", Status: "failed", Summary: "failed", SessionPath: filepath.Join(os.TempDir(), "missing-pi-session.jsonl")}}})
		polMatches(t, p.Message, "Resume: unavailable; no child session file was persisted")
		polNotMatches(t, p.Message, "Revive:")
	})
	tw(t, f, "attaches compact nested children under their parent result child without route secrets", func(t *testing.T) {
		step1 := float64(1)
		nested := &nestedRunSummary{ID: "nested-a", ParentRunID: "root-run", ParentStepIndex: &step1, Depth: 1, Path: []nestedPathEntry{{RunID: "root-run", StepIndex: &i1}}, State: "complete", Agent: "reviewer",
			Model: "provider/gpt-5.6-luna:medium", Thinking: "medium", SessionFile: filepath.Join(os.TempDir(), "nested-a.jsonl"),
			Steps:        []nestedStepSummary{{Agent: "leaf", Status: "complete", Model: "provider/leaf", Thinking: "low"}},
			ControlInbox: "/tmp/should-not-leak", CapabilityToken: "secret-token",
			Children: []*nestedRunSummary{{ID: "nested-grandchild", ParentRunID: "nested-a", Depth: 2, Path: []nestedPathEntry{{RunID: "root-run", StepIndex: &i1}, {RunID: "nested-a"}}, State: "complete", Agent: "auditor",
				ControlInbox: "/tmp/grandchild-should-not-leak", CapabilityToken: "grandchild-secret"}}}
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "root-run", Mode: "parallel", Source: "foreground",
			Children: attachNestedChildrenToResultChildren("root-run", []resultIntercomChild{{Agent: "owner-a", Status: "completed", Summary: "done", Index: &i0}, {Agent: "owner-b", Status: "completed", Summary: "done", Index: &i1}}, []*nestedRunSummary{nested})})
		equal(t, p.Children[0].Children == nil, true, "first child has none")
		n := p.Children[1].Children[0]
		g := n.Children[0]
		equal(t, [4]string{n.ID, n.Model, n.Thinking, n.Steps[0].Model}, [4]string{"nested-a", "provider/gpt-5.6-luna:medium", "medium", "provider/leaf"}, "nested")
		equal(t, n.Steps[0].Thinking, "low", "step thinking")
		equal(t, [4]string{n.ControlInbox, n.CapabilityToken, g.ControlInbox, g.CapabilityToken}, [4]string{"", "", "", ""}, "secrets")
		equal(t, g.ID, "nested-grandchild", "grandchild")
		polMatches(t, p.Message, "Nested subagents:")
		polMatches(t, p.Message, `↳ reviewer — complete \[nested-a\]`)
	})
	tw(t, f, "separates process failure from output presence and gives salvage guidance", func(t *testing.T) {
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-salvage", Mode: "parallel", Source: "foreground", Children: []resultIntercomChild{
			{Agent: "a", Status: "completed", OutputState: "present", Summary: "answer a"}, {Agent: "b", Status: "failed", OutputState: "present", Summary: "runner crashed after answer b"},
			{Agent: "c", Status: "failed", OutputState: "absent", Summary: "startup failed"}}})
		equal(t, [2]string{p.Status, p.Children[1].OutputState}, [2]string{"failed", "present"}, "payload")
		polMatches(t, p.Message, "Process status: failed")
		polMatches(t, p.Message, `Outputs: 2 present, 1 absent \(semantic adequacy unassessed\)`)
		polMatches(t, p.Message, "Inspect that output before retrying")
		polMatches(t, p.Message, "2\\. b — process failed · output present")
	})
	tw(t, f, "keeps full child summaries inside grouped payloads", func(t *testing.T) {
		long := strings.Repeat("x", 2000) + "\n" + strings.Repeat("y", 2000)
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-bound", Mode: "single", Source: "foreground", Children: []resultIntercomChild{{Agent: "worker", Status: "completed", Summary: long}}})
		equal(t, p.Children[0].Summary, long, "summary")
		equal(t, strings.Contains(p.Message, long), true, "message")
	})
	tw(t, f, "formats compact grouped receipts with artifacts and sessions", func(t *testing.T) {
		p := buildSubagentResultIntercomPayload(groupedResultInput{To: "chat", RunID: "run-abc", Mode: "parallel", Source: "foreground", Children: []resultIntercomChild{
			{Agent: "a", Status: "completed", Summary: "done", ArtifactPath: "/tmp/a.md", IntercomTarget: "subagent-a-run-abc-1"}, {Agent: "b", Status: "failed", Summary: "failed", SessionPath: "/tmp/b.jsonl"}}})
		receipt := formatSubagentResultReceipt("parallel", "run-abc", p)
		for _, re := range []string{`Delivered parallel subagent results via intercom\.`, "Children: 1 completed, 1 failed", "Artifacts:\n- a \\[completed\\]: /tmp/a\\.md",
			"Run intercom targets \\(may be inactive after completion\\):\n- a \\[completed\\]: subagent-a-run-abc-1", "Sessions:\n- b \\[failed\\]: /tmp/b\\.jsonl", `Full grouped output was sent over intercom\.`} {
			polMatches(t, receipt, re)
		}
	})
	tw(t, f, "strips heavy output fields from receipt details", func(t *testing.T) {
		got := stripDetailsOutputsForIntercomReceipt(map[string]any{"mode": "single", "results": []any{map[string]any{"agent": "worker", "task": "Task", "exitCode": 0.0,
			"messages": []any{"m"}, "finalOutput": "full output", "truncation": map[string]any{"truncated": true}}}})
		r := got["results"].([]any)[0].(map[string]any)
		_, hasMessages := r["messages"]
		_, hasFinal := r["finalOutput"]
		_, hasTrunc := r["truncation"]
		equal(t, [3]bool{hasMessages, hasFinal, hasTrunc}, [3]bool{false, false, false}, "stripped")
		equal(t, r["agent"], "worker", "kept")
	})
	tw(t, f, "resolves paused, detached, and signal-terminated statuses", func(t *testing.T) {
		for _, c := range []struct {
			in   map[string]any
			want string
		}{
			{map[string]any{"interrupted": true}, "paused"}, {map[string]any{"detached": true}, "detached"},
			{map[string]any{"processSignal": "SIGTERM", "exitCode": 1.0}, "stopped"}, {map[string]any{"processSignal": "SIGTERM", "exitCode": 1.0, "timedOut": true}, "failed"},
			{map[string]any{"processSignal": "SIGTERM", "exitCode": 0.0, "success": true}, "completed"}, {map[string]any{"success": true}, "completed"}, {map[string]any{"exitCode": 1.0}, "failed"},
		} {
			equal(t, resolveSubagentResultStatus(c.in), c.want, strings.TrimSpace(strings.Join([]string{c.want}, "")))
		}
	})
}

// Twins of supervisor-ui.test.ts.

func TestSupervisorUITwins(t *testing.T) {
	const f = "supervisor-ui"
	req := func(details map[string]any, content string, width int, expanded bool) []string {
		lines, ok := renderSupervisorRequest(map[string]any{"content": content, "details": details}, expanded, width)
		if !ok {
			t.Fatal("not rendered")
		}
		return lines
	}
	tw(t, f, "renders request metadata, interview shape, and an exact reply hint", func(t *testing.T) {
		id := "request-123456"
		hint := `subagent_supervisor({ action: "reply", replyTo: "` + id + `", message: "..." })`
		text := strings.Join(req(map[string]any{"id": id, "reason": "interview_request", "expectsReply": true, "runId": "run-1845", "agent": "worker", "childIndex": 2.0, "childTarget": "child-worker",
			"replyHint": hint, "interview": map[string]any{"approved": "boolean", "rationale": "string"}, "requestBody": "Should this change be applied?"},
			strings.Join([]string{"Subagent requests a structured supervisor interview.", "Run: run-1845", "Agent: worker", "Child index: 2", "Should this change be applied?", "Reply with: " + hint}, "\n"), 100, true), "\n")
		for _, re := range []string{"Supervisor interview request", "Reason: interview_request", "Run: run-1845", "Agent: worker", "Child index: 2", "Child target: child-worker", "Request ID: " + id,
			`replyTo: "` + id + `"`, "Interview shape:", "approved", "Should this change be applied"} {
			polMatches(t, text, re)
		}
		equal(t, [4]int{strings.Count(text, "Run: run-1845"), strings.Count(text, "Agent: worker"), strings.Count(text, "Child index: 2"), strings.Count(text, "Reply with:")}, [4]int{1, 1, 1, 1}, "counts")
	})
	tw(t, f, "omits reply hints for progress updates and renders durable reply metadata", func(t *testing.T) {
		out := strings.Join(req(map[string]any{"id": "progress-1", "reason": "progress_update", "expectsReply": false, "runId": "run-progress", "agent": "reviewer", "childIndex": 0.0}, "Progress is continuing.", 100, true), "\n")
		equal(t, strings.Contains(out, "Reply with:"), false, "reply hint")
		lines, ok := renderSupervisorReply(map[string]any{"data": map[string]any{"requestId": "request-1", "reason": "need_decision", "runId": "run-1", "agent": "worker", "childIndex": 1.0,
			"childTarget": "child-worker", "message": "Approved; continue.", "createdAt": 123.0}}, true, 100)
		equal(t, ok, true, "rendered")
		text := strings.Join(lines, "\n")
		for _, re := range []string{"Supervisor reply to child", "Reply to: request-1", `Approved; continue\.`, "child-worker"} {
			polMatches(t, text, re)
		}
	})
	tw(t, f, "ignores malformed request and reply metadata", func(t *testing.T) {
		_, ok := renderSupervisorRequest(map[string]any{"content": "request", "details": map[string]any{"id": "request-1", "expectsReply": true, "replyHint": []any{"bad"}}}, true, 100)
		equal(t, ok, false, "request")
		_, ok = renderSupervisorReply(map[string]any{"data": map[string]any{"requestId": "request-1", "runId": "run-1", "agent": "worker", "childIndex": 0.0, "message": []any{"bad"}, "createdAt": 123.0}}, true, 100)
		equal(t, ok, false, "reply")
	})
	tw(t, f, "sanitizes and bounds collapsed untrusted content at narrow widths", func(t *testing.T) {
		out := req(map[string]any{"id": "request-long", "reason": "need_decision", "expectsReply": true, "runId": "run-long", "agent": "worker", "childIndex": 0.0}, "\u0001"+strings.Repeat("untrusted ", 400), 24, false)
		if len(out) > 39 {
			t.Errorf("expected bounded rows, got %d", len(out))
		}
		for _, line := range out {
			if uiVisibleWidth(line) > 24 {
				t.Errorf("line exceeds width: %q", line)
			}
		}
		text := strings.Join(out, "\n")
		polMatches(t, text, "truncated")
		equal(t, strings.Contains(text, "\u0001"), false, "control character")
		polMatches(t, text, `\[U\+0001\]`)
	})
	tw(t, f, "keeps long blocking requests visible when expanded", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 45; i++ {
			b.WriteString("decision line " + itoaS(i) + "\n")
		}
		content := b.String() + strings.Repeat("body-detail ", 800) + "END_BODY"
		var qs []any
		for i := 1; i <= 12; i++ {
			end := itoaS(i)
			if i == 12 {
				end = "END_INTERVIEW"
			}
			qs = append(qs, map[string]any{"id": "q" + itoaS(i), "question": strings.Repeat("Question detail ", 40) + end})
		}
		out := req(map[string]any{"id": "request-expanded", "reason": "need_decision", "expectsReply": true, "runId": "run-expanded", "agent": "worker", "childIndex": 0.0, "interview": map[string]any{"questions": qs}}, content, 120, true)
		text := strings.Join(out, "\n")
		if len(out) <= 39 {
			t.Errorf("expected expanded rows, got %d", len(out))
		}
		polMatches(t, text, "decision line 45")
		polMatches(t, text, "END_BODY")
		polMatches(t, text, "END_INTERVIEW")
		polNotMatches(t, text, `\[truncated\]`)
	})
}

func itoaS(i int) string { return strconv.Itoa(i) }
