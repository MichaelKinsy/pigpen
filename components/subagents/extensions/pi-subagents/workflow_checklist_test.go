package pi_subagents

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/workflow-checklist.test.ts.

func ckGraph() *WorkflowGraphSnapshot {
	n := func(i int) *int { return &i }
	return &WorkflowGraphSnapshot{RunID: "workflow-checklist", Mode: "workflow",
		Phases: []WorkflowGraphPhase{{Title: "inventory", NodeIDs: []string{"inventory"}}, {Title: "writers", NodeIDs: []string{"writer-a", "writer-b"}}, {Title: "reviews", NodeIDs: []string{"review"}}, {Title: "gate", NodeIDs: []string{"gate"}}},
		Nodes: []*WorkflowGraphNode{
			{ID: "inventory", Kind: "step", Label: "inventory", Agent: "scout", Status: "completed", FlatIndex: n(0)},
			{ID: "writer-a", Kind: "agent", Label: "writer-a", Agent: "writer", Status: "completed", FlatIndex: n(1)},
			{ID: "writer-b", Kind: "agent", Label: "writer-b", Agent: "writer", Status: "running", FlatIndex: n(2)},
			{ID: "review", Kind: "step", Label: "review", Agent: "reviewer", Status: "pending", FlatIndex: n(3)},
			{ID: "gate", Kind: "step", Label: "gate", Agent: "reviewer", Status: "pending", AcceptanceStatus: "rejected", FlatIndex: n(4)},
		}}
}

func ckHost(mod func(*WorkflowHostStep)) *WorkflowHostStep {
	h := &WorkflowHostStep{MonitorKind: "ci", ID: "ci", Label: "CI", State: "done", UpdatedAt: 1000}
	mod(h)
	return h
}

func ckPhaseRows(p WorkflowChecklistProjection) [][]any {
	var out [][]any
	for _, ph := range p.Phases {
		out = append(out, []any{ph.Label, ph.State, ph.Total, ph.Done})
	}
	return out
}

func TestWorkflowChecklistTwins(t *testing.T) {
	const f = "workflow-checklist"
	f64 := func(v float64) *float64 { return &v }
	tw(t, f, "workflow checklist fuses graph phases with loaded child state without duplicate rows", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: ckGraph(), Now: f64(5000), Steps: []WorkflowChecklistStep{
			{WorkflowKey: "inventory", Agent: "scout", Status: "complete"},
			{WorkflowKey: "writer-a", Agent: "writer", Status: "complete"},
			{WorkflowKey: "writer-b", Agent: "writer", Status: "running", CurrentTool: "grep", ToolCount: f64(18), StartedAt: f64(1000)},
			{WorkflowKey: "review", Agent: "reviewer", Status: "pending", Context: "fork"},
		}})
		eq(t, ckPhaseRows(p), [][]any{{"inventory", "complete", 1, 1}, {"writers", "running", 2, 1}, {"reviews", "queued", 1, 0}, {"gate", "blocked", 1, 0}}, "phases")
		eq(t, []int{p.Total, p.Done, p.Running, p.Queued, p.Blocked}, []int{5, 2, 1, 1, 1}, "counts")
		rows := 0
		for _, ph := range p.Phases {
			rows += len(ph.Items)
		}
		eq(t, rows, 5, "rows")
		eq(t, *p.Phases[1].Items[1].DurationMs, 4000.0, "duration")
		eq(t, p.Phases[2].Items[0].Context, "fork", "context")
		eq(t, p.Bottleneck.Key, "gate", "bottleneck")
	})
	tw(t, f, "workflow checklist preserves explicit host monitor verdicts and trace lane identity", func(t *testing.T) {
		stale := true
		host := ckHost(func(h *WorkflowHostStep) {
			h.Provider, h.Verdict, h.Target = "buildkite", "inconclusive", "main"
			h.Freshness = &WorkflowHostStepFreshness{ExpectedRef: "main", Stale: &stale}
		})
		p := projectWorkflowChecklist(WorkflowChecklistInput{HostSteps: []*WorkflowHostStep{host},
			Preflight: &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "reviewers", Mode: "review"}}},
			Trace: []WorkflowChecklistTraceEntry{
				{Operation: "run", Key: "reviewers.a", GeneratedLaneKey: "reviewers", State: "started", Agent: "reviewer"},
				{Operation: "run", Key: "reviewers.a", GeneratedLaneKey: "reviewers", State: "completed", Agent: "reviewer"}}})
		var labels []string
		for _, ph := range p.Phases {
			labels = append(labels, ph.Label)
		}
		eq(t, labels, []string{"CI", "reviewers"}, "phases")
		eq(t, p.Phases[0].Items[0].State, "blocked", "state")
		eq(t, p.Phases[0].Items[0].MonitorKind, "ci", "monitor")
		eq(t, p.Phases[0].Items[0].Verdict, "inconclusive", "verdict")
		eq(t, p.Phases[1].Items[0].State, "complete", "lane state")
		eq(t, p.Phases[1].Items[0].Preflight.Mode, "review", "lane mode")
		mustMatch(t, "CI", formatWorkflowChecklistBottleneck(p.Bottleneck, true, true))
	})
	tw(t, f, "workflow checklist renders a failed item error only once when item details are included", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Steps: []WorkflowChecklistStep{{WorkflowKey: "review", Agent: "reviewer", Status: "failed", Error: "review failed with details"}}})
		expanded := strings.Join(formatWorkflowChecklistText(p, "", true), "\n")
		eq(t, strings.Count(expanded, "review failed with details"), 1, "expanded")
		if regexp.MustCompile(`bottleneck .*error:review failed with details`).MatchString(expanded) {
			t.Error("bottleneck repeats the error")
		}
		collapsed := strings.Join(formatWorkflowChecklistText(p, "", false), "\n")
		eq(t, strings.Count(collapsed, "review failed with details"), 1, "collapsed")
		mustMatch(t, `bottleneck .*error:review failed with details`, collapsed)
	})
	tw(t, f, "workflow checklist counts one host monitor when host status and trace share an id", func(t *testing.T) {
		host := ckHost(func(h *WorkflowHostStep) { h.Verdict = "pass" })
		p := projectWorkflowChecklist(WorkflowChecklistInput{HostSteps: []*WorkflowHostStep{host}, Trace: []WorkflowChecklistTraceEntry{{Operation: "host", Key: "ci", State: "completed", Label: "CI"}}})
		eq(t, p.Total, 1, "total")
		var rows [][3]string
		for _, ph := range p.Phases {
			for _, i := range ph.Items {
				rows = append(rows, [3]string{i.Key, i.Kind, i.State})
			}
		}
		eq(t, rows, [][3]string{{"ci", "host", "complete"}}, "rows")
	})
	tw(t, f, "workflow checklist keeps terminal child state newer than stale running trace", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: ckGraph(), Steps: []WorkflowChecklistStep{{WorkflowKey: "writer-b", Agent: "writer", Status: "complete"}},
			Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "writer-b", State: "started", Agent: "writer"}}})
		eq(t, p.Running, 0, "running")
		eq(t, p.Phases[1].Items[1].State, "complete", "state")
	})
	tw(t, f, "workflow checklist keeps terminal graph state newer than stale running child state", func(t *testing.T) {
		g := ckGraph()
		g.Nodes[2].Status, g.Nodes[2].Error = "failed", "writer failed"
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: g, Steps: []WorkflowChecklistStep{{WorkflowKey: "writer-b", Agent: "writer", Status: "running"}},
			Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "writer-b", State: "started", Agent: "writer"}}})
		eq(t, p.Running, 0, "running")
		eq(t, p.Failed, 1, "failed")
		eq(t, p.Phases[1].State, "failed", "phase")
		eq(t, p.Phases[1].Items[1].State, "failed", "item")
	})
	tw(t, f, "workflow checklist keeps terminal graph state newer than stale trace without loaded child state", func(t *testing.T) {
		g := ckGraph()
		g.Nodes[2].Status, g.Nodes[2].Error = "failed", "writer failed"
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: g, Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "writer-b", State: "started", Agent: "writer"}}})
		eq(t, p.Running, 0, "running")
		eq(t, p.Failed, 1, "failed")
		eq(t, p.Phases[1].Items[1].State, "failed", "item")
	})
	tw(t, f, "workflow checklist keeps graph acceptance blockers ahead of stale running child state", func(t *testing.T) {
		g := ckGraph()
		g.Nodes[2].AcceptanceStatus = "rejected"
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: g, Steps: []WorkflowChecklistStep{{WorkflowKey: "writer-b", Agent: "writer", Status: "running"}},
			Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "writer-b", State: "started", Agent: "writer"}}})
		eq(t, p.Running, 0, "running")
		eq(t, p.Blocked, 2, "blocked")
		eq(t, p.Phases[1].Items[1].State, "blocked", "item")
	})
	tw(t, f, "workflow checklist uses preflight only to annotate authoritative work", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: ckGraph(), Preflight: &WorkflowPreflight{Version: 1, Coverage: "partial",
			Lanes: []WorkflowPreflightLane{{Key: "writer-b", Mode: "mutation"}, {Key: "writers", Mode: "mutation"}, {Key: "deploy", Mode: "gate"}}}})
		eq(t, p.Total, 5, "total")
		var labels []string
		for _, ph := range p.Phases {
			labels = append(labels, ph.Label)
		}
		eq(t, labels, []string{"inventory", "writers", "reviews", "gate"}, "phases")
		eq(t, p.Queued, 1, "queued")
		eq(t, p.Phases[2].Items[0].Key, "review", "review")
	})
	tw(t, f, "workflow checklist does not turn unmatched preflight metadata into queued work", func(t *testing.T) {
		pre := &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "pr14", Mode: "review"}}}
		declarationOnly := projectWorkflowChecklist(WorkflowChecklistInput{Preflight: pre})
		mismatched := projectWorkflowChecklist(WorkflowChecklistInput{Preflight: pre, Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "pr14-quality", State: "started", Agent: "reviewer"}}})
		if !reflect.DeepEqual(declarationOnly, WorkflowChecklistProjection{Phases: []*WorkflowChecklistPhase{}}) {
			t.Errorf("declaration only = %+v", declarationOnly)
		}
		eq(t, mismatched.Total, 1, "total")
		eq(t, mismatched.Running, 1, "running")
		eq(t, mismatched.Queued, 0, "queued")
		var keys []string
		for _, ph := range mismatched.Phases {
			for _, i := range ph.Items {
				keys = append(keys, i.Key)
			}
		}
		eq(t, keys, []string{"pr14-quality"}, "keys")
	})
	tw(t, f, "workflow checklist prefers specific dotted preflight lanes over generated aliases", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Preflight: &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "writer", Mode: "mutation"}, {Key: "writer.quality", Mode: "review"}}},
			Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "writer.quality.deep", GeneratedLaneKey: "writer", State: "started", Agent: "reviewer"}}})
		eq(t, p.Total, 1, "total")
		eq(t, len(p.Phases), 1, "phases")
		eq(t, p.Phases[0].Label, "writer.quality", "label")
		eq(t, p.Phases[0].Items[0].Preflight.Mode, "review", "mode")
	})
	tw(t, f, "workflow checklist text exposes aggregate, phase, and bottleneck signals with bounded errors", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Trace: []WorkflowChecklistTraceEntry{{Operation: "run", Key: "review", State: "failed", Label: "reviewer", Agent: "reviewer", Error: "Output: secret details"}}})
		text := strings.Join(formatWorkflowChecklistText(p, "", true), "\n")
		mustMatch(t, `Workflow checklist: 0/1 done · 1 failed`, text)
		mustMatch(t, `reviewer`, text)
		mustMatch(t, `bottleneck`, text)
		if strings.Contains(text, "Output:") {
			t.Errorf("Output: leaked: %s", text)
		}
	})
	tw(t, f, "workflow checklist text can suppress item rows for status surfaces", func(t *testing.T) {
		p := projectWorkflowChecklist(WorkflowChecklistInput{Graph: ckGraph(), Steps: []WorkflowChecklistStep{{WorkflowKey: "writer-b", Agent: "writer", Status: "running"}}})
		text := strings.Join(formatWorkflowChecklistText(p, "", false), "\n")
		mustMatch(t, `Workflow checklist:`, text)
		mustMatch(t, `writers 1 done · 1 active`, text)
		if strings.Contains(text, "writer-b · writer · active") {
			t.Errorf("item row present: %s", text)
		}
	})
}
