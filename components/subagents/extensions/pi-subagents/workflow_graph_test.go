package pi_subagents

import (
	"reflect"
	"strings"
	"testing"
)

// Twins of test/unit/workflow-graph.test.ts.
const graphFile = "workflow-graph"

func ok0() *WorkflowGraphResult { return &WorkflowGraphResult{ExitCode: 0} }

func TestWorkflowGraphTwins(t *testing.T) {
	tw(t, graphFile, "maps sequential chains with phase, label, output name, and stable flat indexes", func(t *testing.T) {
		g := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{
			RunID: "run-1",
			Steps: []*jsObject{
				jo("agent", "scout", "task", "Scan", "phase", "Research", "label", "Find context", "as", "context"),
				jo("agent", "writer", "task", "Use {outputs.context}", "phase", "Synthesis", "outputSchema", jo("type", "object")),
			},
			Results: []*WorkflowGraphResult{ok0(), {ExitCode: 1, Error: "bad output"}},
		})
		n0, n1 := g.Nodes[0], g.Nodes[1]
		if n0.ID != "step-0" || n0.Label != "Find context" || *n0.FlatIndex != 0 || n0.OutputName != "context" {
			t.Errorf("node 0 = %+v", n0)
		}
		if !n1.Structured || n1.Status != "failed" {
			t.Errorf("node 1 = %+v", n1)
		}
		want := []WorkflowGraphPhase{{"Research", []string{"step-0"}}, {"Synthesis", []string{"step-1"}}}
		if !reflect.DeepEqual(g.Phases, want) {
			t.Errorf("phases = %+v", g.Phases)
		}
	})

	tw(t, graphFile, "maps a long alternating worker and reviewer chain without losing indexes", func(t *testing.T) {
		var steps []*jsObject
		var results []*WorkflowGraphResult
		for i := 0; i < 40; i++ {
			agent, task := "worker", "{previous}"
			if i%2 != 0 {
				agent = "reviewer"
			}
			if i == 0 {
				task = "Start"
			}
			steps = append(steps, jo("agent", agent, "task", task))
			results = append(results, ok0())
		}
		g := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{RunID: "run-long", Steps: steps, Results: results, CurrentFlatIndex: new(37), CurrentStepIndex: new(37)})
		if len(g.Nodes) != 40 {
			t.Fatalf("nodes = %d", len(g.Nodes))
		}
		for _, c := range []struct {
			i     int
			agent string
		}{{0, "worker"}, {37, "reviewer"}, {39, "reviewer"}} {
			n := g.Nodes[c.i]
			if n.ID != "step-"+itoa(c.i) || n.Agent != c.agent || *n.FlatIndex != c.i {
				t.Errorf("node %d = %+v", c.i, n)
			}
		}
		if g.CurrentNodeID != "step-37" {
			t.Errorf("current = %q", g.CurrentNodeID)
		}
	})

	tw(t, graphFile, "maps parallel chain steps with stable group and child indexes", func(t *testing.T) {
		g := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{
			RunID: "run-2",
			Steps: []*jsObject{
				jo("agent", "setup", "task", "Setup"),
				jo("parallel", []any{
					jo("agent", "reviewer", "label", "Correctness", "phase", "Review", "as", "correctness"),
					jo("agent", "security", "label", "Security", "phase", "Review", "as", "security"),
				}),
			},
			CurrentFlatIndex: new(2),
		})
		group := g.Nodes[1]
		if group.Kind != "parallel-group" || group.Children[0].ID != "step-1-agent-0" || *group.Children[0].FlatIndex != 1 || *group.Children[1].FlatIndex != 2 || group.Children[1].Status != "running" {
			t.Errorf("group = %+v", group)
		}
		if g.CurrentNodeID != "step-1-agent-1" {
			t.Errorf("current = %q", g.CurrentNodeID)
		}
		if want := []WorkflowGraphPhase{{"Review", []string{"step-1-agent-0", "step-1-agent-1"}}}; !reflect.DeepEqual(g.Phases, want) {
			t.Errorf("phases = %+v", g.Phases)
		}
	})

	tw(t, graphFile, "marks partially completed parallel groups as running", func(t *testing.T) {
		g := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{RunID: "run-3", Steps: []*jsObject{jo("parallel", []any{jo("agent", "first"), jo("agent", "second")})}, Results: []*WorkflowGraphResult{ok0()}})
		if g.Nodes[0].Status != "running" || g.Nodes[0].Children[0].Status != "completed" || g.Nodes[0].Children[1].Status != "pending" {
			t.Errorf("group = %+v", g.Nodes[0])
		}
	})

	tw(t, graphFile, "summarizes mixed terminal parallel group statuses with explicit precedence", func(t *testing.T) {
		steps := []*jsObject{jo("parallel", []any{jo("agent", "first"), jo("agent", "second")})}
		a := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{RunID: "run-4", Steps: steps, Results: []*WorkflowGraphResult{{ExitCode: 1}, {ExitCode: 1, Interrupted: true}}})
		b := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{RunID: "run-5", Steps: steps, Results: []*WorkflowGraphResult{{ExitCode: 1, Interrupted: true}, {ExitCode: 1}}})
		if a.Nodes[0].Status != "failed" || b.Nodes[0].Status != "failed" {
			t.Errorf("statuses = %q %q", a.Nodes[0].Status, b.Nodes[0].Status)
		}
	})

	tw(t, graphFile, "uses dynamic group status overrides for empty or aggregate-failure fanout states", func(t *testing.T) {
		steps := []*jsObject{jo("expand", jo("from", jo("output", "targets", "path", "/items"), "maxItems", float64(4)), "parallel", jo("agent", "reviewer", "task", "Review {item}"), "collect", jo("as", "reviews"))}
		empty := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{RunID: "run-dynamic-empty", Steps: steps, DynamicGroupStatuses: map[int]WorkflowGraphGroupStatus{0: {Status: "completed"}}})
		if empty.Nodes[0].Kind != "dynamic-parallel-group" || empty.Nodes[0].Status != "completed" || empty.Nodes[0].Children == nil || len(empty.Nodes[0].Children) != 0 {
			t.Errorf("empty = %+v", empty.Nodes[0])
		}
		fail := BuildWorkflowGraphSnapshot(WorkflowGraphBuildInput{
			RunID: "run-dynamic-collect-fail", Steps: steps,
			DynamicChildren:      map[int][]WorkflowGraphDynamicChild{0: {{Agent: "reviewer", FlatIndex: 0, ItemKey: "a"}}},
			Results:              []*WorkflowGraphResult{ok0()},
			DynamicGroupStatuses: map[int]WorkflowGraphGroupStatus{0: {Status: "failed", Error: "Collected output validation failed"}},
		})
		if fail.Nodes[0].Status != "failed" || !strings.Contains(fail.Nodes[0].Error, "Collected output validation failed") || fail.Nodes[0].Children[0].Status != "completed" {
			t.Errorf("fail = %+v", fail.Nodes[0])
		}
	})
}
