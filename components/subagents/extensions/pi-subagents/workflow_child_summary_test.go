package pi_subagents

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Twins of test/unit/workflow-child-summary.test.ts.
const childSummaryFile = "workflow-child-summary"

func summaryProgress() WorkflowAgentProgress {
	n := func(v float64) *float64 { return &v }
	return WorkflowAgentProgress{
		WorkflowChildActivity: WorkflowChildActivity{CurrentTool: "read", CurrentToolStartedAt: n(100), LastActivityAt: n(120), DurationMs: n(20), ToolCount: n(1), TurnCount: n(2), Tokens: n(10), InputTokens: n(8), OutputTokens: n(2)},
		Agent:                 "worker", Model: "provider/model", Thinking: "high", Task: "private", CurrentToolArgs: "private", RecentOutput: []string{"private"},
	}
}

func summaryInput() WorkflowChildSummaryInput {
	var trace []WorkflowScriptTraceEntry
	for _, k := range []string{"a", "b"} {
		trace = append(trace, WorkflowScriptTraceEntry{Operation: "run", Key: k, State: "started"})
	}
	return WorkflowChildSummaryInput{ParentToolCallID: "tool", WorkflowRunID: "wf", WorkflowState: "running", InventoryComplete: false, Trace: trace}
}

func mustSummary(t *testing.T, in WorkflowChildSummaryInput) WorkflowChildSummary {
	t.Helper()
	s, err := WorkflowChildSummaryOf(in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func summaryObject(t *testing.T, s WorkflowChildSummary) *jsObject {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func TestWorkflowChildSummaryTwins(t *testing.T) {
	tw(t, childSummaryFile, "projects independent bounded snapshots by stable key and clears terminal activity", func(t *testing.T) {
		progress := summaryProgress()
		other := summaryProgress()
		other.CurrentTool = "bash"
		other.ToolCount = new(9.0)
		snapshots := map[string]WorkflowChildLiveProgress{"b": WorkflowChildProgress(progress), "a": WorkflowChildProgress(other)}
		in := summaryInput()
		in.Progress = snapshots
		summary := mustSummary(t, in)
		var rows []string
		for _, r := range summary.Children {
			rows = append(rows, r.ChildID+":"+r.Activity.CurrentTool+":"+jsNumber(*r.Activity.ToolCount))
		}
		if strings.Join(rows, ",") != "a:bash:9,b:read:1" {
			t.Fatalf("rows = %v", rows)
		}
		if summary.Children[0].Model != progress.Model || summary.Children[0].Thinking != "high" {
			t.Fatalf("row = %+v", summary.Children[0])
		}
		b, _ := json.Marshal(summary)
		if strings.Contains(string(b), "private") {
			t.Fatalf("a private field reached the summary: %s", b)
		}
		parsed, err := ParseWorkflowChildSummary(summaryObject(t, summary))
		if err != nil || !reflect.DeepEqual(*parsed, summary) {
			t.Fatalf("parse = %+v, %v", parsed, err)
		}
		cleared := summaryProgress()
		cleared.CurrentTool, cleared.CurrentToolStartedAt = "", nil
		snapshots["a"] = WorkflowChildProgress(cleared)
		in.Progress = snapshots
		if got := mustSummary(t, in).Children[0].Activity.CurrentTool; got != "" {
			t.Fatalf("currentTool = %q", got)
		}
		if summary.Children[0].Activity.CurrentTool != "bash" {
			t.Fatalf("published snapshots stay immutable")
		}
		in.InventoryComplete, in.WorkflowState = true, "failed"
		for _, r := range mustSummary(t, in).Children {
			if r.Activity != nil {
				t.Fatalf("terminal row %s keeps its activity", r.ChildID)
			}
		}
	})

	tw(t, childSummaryFile, "keeps launch receipts running when the workflow inventory closes successfully or with an error", func(t *testing.T) {
		for _, state := range []string{"completed", "failed", "stopped"} {
			in := summaryInput()
			in.WorkflowState, in.InventoryComplete = state, true
			in.Children = []*jsObject{jo("key", "a", "state", "running", "ok", false, "runId", "child-a", "output", "", "artifactPaths", []any{})}
			s := mustSummary(t, in)
			var a, b string
			for _, r := range s.Children {
				switch r.ChildID {
				case "a":
					a = r.State
				case "b":
					b = r.State
				}
			}
			want := "failed"
			if state == "stopped" {
				want = "stopped"
			}
			if a != "running" || b != want {
				t.Fatalf("%s: a=%s b=%s", state, a, b)
			}
		}
	})

	tw(t, childSummaryFile, "bounds UTF-8 and JSON bytes and rejects malformed activity strictly", func(t *testing.T) {
		if got := WorkflowChildActivityOf(WorkflowChildActivity{CurrentTool: strings.Repeat("界", 86)}).CurrentTool; got != "" {
			t.Fatalf("86 wide characters (258 bytes) must be refused, got %q", got)
		}
		if got := WorkflowChildActivityOf(WorkflowChildActivity{CurrentTool: strings.Repeat("界", 85)}).CurrentTool; len([]rune(got)) != 85 {
			t.Fatalf("85 wide characters kept? %q", got)
		}
		p := summaryProgress()
		p.CurrentTool = strings.Repeat("\x00", 256)
		p.Tokens = new(math.MaxFloat64)
		activity := WorkflowChildActivityOf(p.WorkflowChildActivity)
		if b, _ := json.Marshal(activity); len(b) >= 2048 {
			t.Fatalf("activity JSON is %d bytes", len(b))
		}
		bad := WorkflowChildActivityOf(WorkflowChildActivity{Tokens: new(math.NaN()), ToolCount: new(-1.0), DurationMs: new(math.Inf(1))})
		if bad != (WorkflowChildActivity{}) {
			t.Fatalf("bad = %+v", bad)
		}
		in := summaryInput()
		in.Progress = map[string]WorkflowChildLiveProgress{"a": WorkflowChildProgress(summaryProgress())}
		summary := mustSummary(t, in)
		withActivity := func(activity any) *jsObject {
			o := summaryObject(t, summary)
			row := o.vals["children"].([]any)[0].(*jsObject)
			row.set("activity", activity)
			return o
		}
		for _, invalid := range []any{nil, []any{}, jo("tokens", float64(-1)), jo("tokens", math.Inf(1)), jo("tokens", "1"), jo("currentTool", strings.Repeat("界", 86)), jo("recentOutput", []any{}), jo("currentToolArgs", "secret")} {
			if _, err := ParseWorkflowChildSummary(withActivity(invalid)); err == nil || !strings.Contains(err.Error(), "activity") {
				t.Fatalf("%v: err = %v", invalid, err)
			}
		}
		o := summaryObject(t, summary)
		o.vals["children"].([]any)[0].(*jsObject).set("state", "completed")
		if _, err := ParseWorkflowChildSummary(o); err == nil || !strings.Contains(err.Error(), "running state") {
			t.Fatalf("err = %v", err)
		}
	})
}
