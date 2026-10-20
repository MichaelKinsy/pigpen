package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of nested-events (runs/shared/nested-events.ts).

func nestedTemp(t *testing.T) {
	t.Helper()
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
}

func trackRoute(t *testing.T, rootRunID string) nestedRoute {
	t.Helper()
	r, err := createNestedRoute(rootRunID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func nestedChild(id, state string, ts int, parentRunID string) jm {
	stepStatus := "complete"
	if state == "running" {
		stepStatus = "running"
	}
	return jm{"id": id, "parentRunId": parentRunID, "parentStepIndex": 1, "depth": 1, "path": []any{jm{"runId": parentRunID, "stepIndex": 1}},
		"mode": "single", "state": state, "agent": "reviewer", "agents": []any{"reviewer"}, "startedAt": 10, "lastUpdate": ts,
		"steps": []any{jm{"agent": "leaf", "status": stepStatus}}}
}

func withJM(base jm, over jm) jm {
	out := copyJM(base)
	for k, v := range over {
		out[k] = v
	}
	return out
}

func nev(t *testing.T, r nestedRoute, typ string, ts float64, parent string, step *float64, child jm) {
	t.Helper()
	if err := writeNestedEvent(r, typ, ts, parent, step, child); err != nil {
		t.Fatal(err)
	}
}

func fl(n float64) *float64 { return &n }

func projected(t *testing.T, r nestedRoute) nestedRegistry {
	t.Helper()
	reg, err := projectNestedEvents(r)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func jmGet(m jm, path ...any) any {
	var cur any = m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			mm, ok := cur.(jm)
			if !ok {
				if m2, ok2 := cur.(map[string]any); ok2 {
					mm = m2
				} else {
					return nil
				}
			}
			cur = mm[k]
		case int:
			switch l := cur.(type) {
			case []jm:
				if k >= len(l) {
					return nil
				}
				cur = l[k]
			case []any:
				if k >= len(l) {
					return nil
				}
				cur = l[k]
			default:
				return nil
			}
		}
	}
	return cur
}

func TestNestedEventsTwins(t *testing.T) {
	const f = "nested-events"
	tw(t, f, "indexes routes by root run id in a single directory scan", func(t *testing.T) {
		nestedTemp(t)
		a, b := trackRoute(t, "index-root-a"), trackRoute(t, "index-root-b")
		idx := buildNestedRouteIndex()
		eq(t, idx["index-root-a"].CapabilityToken, a.CapabilityToken, "a")
		eq(t, idx["index-root-b"].CapabilityToken, b.CapabilityToken, "b")
		_, has := idx["missing-root"]
		eq(t, has, false, "missing")
	})
	tw(t, f, "keeps at most one route when a root run id has duplicate route dirs", func(t *testing.T) {
		nestedTemp(t)
		first, second := trackRoute(t, "dup-root"), trackRoute(t, "dup-root")
		got, ok := buildNestedRouteIndex()["dup-root"]
		if !ok {
			t.Fatal("expected one route for dup-root")
		}
		if got.CapabilityToken != first.CapabilityToken && got.CapabilityToken != second.CapabilityToken {
			t.Errorf("indexed route %q is neither created route", got.CapabilityToken)
		}
	})
	tw(t, f, "recovers a route from its index when the route file is missing", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "late-route-root")
		routeFile := filepath.Join(filepath.Dir(r.EventSink), "route.json")
		if err := os.Remove(routeFile); err != nil {
			t.Fatal(err)
		}
		eq(t, buildNestedRouteIndex()["late-route-root"].CapabilityToken, r.CapabilityToken, "recovered")
		eq(t, readJSONMap(t, routeFile)["capabilityToken"], r.CapabilityToken, "route.json rewritten")
	})
	tw(t, f, "resolves nested parent addresses from the inherited child runtime", func(t *testing.T) {
		got := inheritedNestedParentAddressOf(&childRuntimeNested{NestedParent: &nestedParent{ParentRunID: "nested-parent", ParentChildIndex: fl(2), Depth: 3, Path: []any{
			jm{"runId": "root-run", "stepIndex": 0, "agent": "root-agent"}, jm{"runId": "nested-parent", "stepIndex": 2, "agent": "nested-agent"}}}})
		eq(t, jsonOf(got), `{"ParentRunID":"nested-parent","ParentStepIndex":2,"Depth":3,"Path":[{"agent":"root-agent","runId":"root-run","stepIndex":0},{"agent":"nested-agent","runId":"nested-parent","stepIndex":2}]}`, "with an index")
		got = inheritedNestedParentAddressOf(&childRuntimeNested{NestedParent: &nestedParent{ParentRunID: "nested-parent", Depth: 1, Path: []any{}}})
		eq(t, jsonOf(got), `{"ParentRunID":"nested-parent","ParentStepIndex":null,"Depth":1,"Path":[{"runId":"nested-parent"}]}`, "without an index the path is the parent")
	})
	tw(t, f, "ignores unsafe nested parent ids and missing parents", func(t *testing.T) {
		eq(t, inheritedNestedParentAddressOf(&childRuntimeNested{NestedParent: &nestedParent{ParentRunID: "../unsafe", ParentChildIndex: fl(2), Depth: 1, Path: []any{}}}) == nil, true, "unsafe")
		eq(t, inheritedNestedParentAddressOf(&childRuntimeNested{}) == nil, true, "no parent")
		eq(t, inheritedNestedParentAddressOf(nil) == nil, true, "no runtime")
	})
	tw(t, f, "resolves only matching contained routes from the inherited child runtime", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		got := inheritedNestedRouteOf(&childRuntimeNested{NestedRoute: &r})
		eq(t, *got, r, "the route")
		eq(t, inheritedNestedRouteOf(&childRuntimeNested{}) == nil, true, "no route")
		var logged []string
		old := nestedRouteLog
		nestedRouteLog = func(msg string, err error) { logged = append(logged, err.Error()) }
		defer func() { nestedRouteLog = old }()
		wrong := r
		wrong.CapabilityToken = "wrong-token"
		eq(t, inheritedNestedRouteOf(&childRuntimeNested{NestedRoute: &wrong}) == nil, true, "wrong token")
		if len(logged) == 0 || !strings.Contains(logged[0], "capability token") {
			t.Errorf("logged %v", logged)
		}
	})
	tw(t, f, "projects started, updated, and completed records into async and foreground parent state", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		ack := jm{"version": 1, "source": "child-runtime", "ids": []any{"ext.ok", "bad/path", "ext.ok"}, "omitted": 1}
		started := withJM(nestedChild("nested-a", "running", 100, "root-run"), jm{"model": "provider/gpt-5.6-luna:medium", "thinking": "medium",
			"steps":    []any{jm{"agent": "leaf", "status": "running", "model": "provider/leaf", "thinking": "low"}},
			"children": []any{nestedChild("nested-grandchild", "running", 100, "nested-a")}})
		nev(t, r, "subagent.nested.started", 100, "root-run", fl(1), started)
		nev(t, r, "subagent.nested.updated", 200, "root-run", fl(1), withJM(nestedChild("nested-a", "running", 200, "root-run"), jm{"currentTool": "read", "runtimeAcknowledgedExtensions": ack}))
		nev(t, r, "subagent.nested.completed", 300, "root-run", fl(1), withJM(nestedChild("nested-a", "complete", 300, "root-run"), jm{"runtimeAcknowledgedExtensions": ack}))
		reg := projected(t, r)
		eq(t, len(reg.Children), 1, "children")
		c := reg.Children[0]
		eq(t, c["id"], "nested-a", "id")
		eq(t, c["state"], "complete", "state")
		eq(t, jmGet(c, "steps", 0, "agent"), "leaf", "step agent")
		eq(t, c["model"], "provider/gpt-5.6-luna:medium", "model")
		eq(t, c["thinking"], "medium", "thinking")
		eq(t, jmGet(c, "steps", 0, "model"), "provider/leaf", "step model")
		eq(t, jmGet(c, "steps", 0, "thinking"), "low", "step thinking")
		eq(t, jmGet(c, "children", 0, "id"), "nested-grandchild", "grandchild")
		eq(t, jsonOf(c["runtimeAcknowledgedExtensions"]), `{"ids":["ext.ok"],"omitted":1,"source":"child-runtime","version":1}`, "acknowledged extensions")

		job := &AsyncJobState{AsyncID: "root-run", AsyncDir: "/tmp/root-run", Status: "running", NestedRoute: &r, Steps: []AsyncRunStepSummary{
			{AsyncStep: AsyncStep{Agent: "owner-0", Status: "running"}, Index: 0}, {AsyncStep: AsyncStep{Agent: "owner-1", Status: "running"}, Index: 1}}}
		if err := updateAsyncJobNestedProjection(job); err != nil {
			t.Fatal(err)
		}
		eq(t, job.NestedChildren[0]["id"], "nested-a", "job children")
		eq(t, job.Steps[1].Children[0]["id"], "nested-a", "step 1 children")
		ctl := &ForegroundRunRecord{RunID: "root-run", NestedRoute: &r}
		if err := updateForegroundNestedProjection(ctl); err != nil {
			t.Fatal(err)
		}
		eq(t, ctl.NestedChildren[0]["id"], "nested-a", "foreground children")
	})
	tw(t, f, "attaches root children to visible step slices by original step index", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		nev(t, r, "subagent.nested.updated", 100, "root-run", fl(3), withJM(nestedChild("nested-visible", "running", 100, "root-run"), jm{"parentStepIndex": 3, "path": []any{jm{"runId": "root-run", "stepIndex": 3}}}))
		job := &AsyncJobState{AsyncID: "root-run", AsyncDir: "/tmp/root-run", Status: "running", NestedRoute: &r, Steps: []AsyncRunStepSummary{
			{AsyncStep: AsyncStep{Agent: "owner-2", Status: "running"}, Index: 2}, {AsyncStep: AsyncStep{Agent: "owner-3", Status: "running"}, Index: 3}}}
		if err := updateAsyncJobNestedProjection(job); err != nil {
			t.Fatal(err)
		}
		eq(t, len(job.Steps[0].Children), 0, "step 0")
		eq(t, job.Steps[1].Children[0]["id"], "nested-visible", "step 1")
	})
	tw(t, f, "ignores corrupt, partial, wrong-token, duplicate, and stale records while preserving terminal state", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		writeFile(t, filepath.Join(r.EventSink, "0000000000001-corrupt.json"), "{not json")
		rec := func(typ string, ts int, token string, child jm) string {
			return jsonOf(jm{"type": typ, "ts": ts, "rootRunId": r.RootRunID, "parentRunId": "root-run", "parentStepIndex": 1, "capabilityToken": token, "child": child}) + "\n"
		}
		writeFile(t, filepath.Join(r.EventSink, "0000000000002-partial.jsonl"), rec("subagent.nested.started", 50, r.CapabilityToken, nestedChild("partial-good", "running", 50, "root-run"))+`{"type":"subagent.nested.started"`)
		nev(t, r, "subagent.nested.completed", 300, "root-run", fl(1), nestedChild("nested-terminal", "complete", 300, "root-run"))
		writeFile(t, filepath.Join(r.EventSink, "0000000000400-stale.json"), rec("subagent.nested.updated", 400, r.CapabilityToken, nestedChild("nested-terminal", "running", 100, "root-run")))
		writeFile(t, filepath.Join(r.EventSink, "0000000000500-wrong-token.json"), rec("subagent.nested.started", 500, "wrong", nestedChild("wrong-token", "running", 500, "root-run")))
		reg := projected(t, r)
		state := func(id string) any {
			for _, c := range reg.Children {
				if c["id"] == id {
					return c["state"]
				}
			}
			return nil
		}
		eq(t, state("partial-good"), "running", "partial-good")
		eq(t, state("nested-terminal"), "complete", "terminal state is kept")
		eq(t, state("wrong-token"), nil, "wrong token")
		eq(t, hasLiveNestedDescendants(reg.Children), true, "live")
	})
	tw(t, f, "detects live descendants attached to terminal step children", func(t *testing.T) {
		parent := withJM(nestedChild("terminal-parent", "complete", 300, "root-run"), jm{"steps": []jm{{"agent": "owner-step", "status": "complete", "children": []jm{
			withJM(nestedChild("running-step-child", "running", 310, "terminal-parent"), jm{"parentStepIndex": 0, "path": []any{jm{"runId": "terminal-parent", "stepIndex": 0}}})}}}})
		eq(t, hasLiveNestedDescendants([]jm{parent}), true, "live")
	})
	tw(t, f, "preserves rejected nested states across the event and registry boundary", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "rejected-root")
		nev(t, r, "subagent.nested.completed", 300, r.RootRunID, fl(0), withJM(nestedChild("rejected-child", "rejected", 300, r.RootRunID), jm{"parentStepIndex": 0,
			"path": []any{jm{"runId": r.RootRunID, "stepIndex": 0}}, "steps": []any{jm{"agent": "leaf", "status": "rejected"}}}))
		reg := projected(t, r)
		eq(t, reg.Children[0]["state"], "rejected", "state")
		eq(t, jmGet(reg.Children[0], "steps", 0, "status"), "rejected", "step status")
		eq(t, hasLiveNestedDescendants(reg.Children), false, "not live")
	})
	tw(t, f, "accepts only complete numeric token usage at the nested event boundary", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		nev(t, r, "subagent.nested.updated", 100, "root-run", fl(1), withJM(nestedChild("nested-valid-tokens", "running", 100, "root-run"), jm{"totalTokens": jm{"input": 10, "output": 15, "total": 25, "window": 30, "windowPeak": 40}}))
		writeFile(t, filepath.Join(r.EventSink, "0000000000200-invalid-tokens.json"), jsonOf(jm{"type": "subagent.nested.updated", "ts": 200, "rootRunId": r.RootRunID, "parentRunId": "root-run",
			"parentStepIndex": 1, "capabilityToken": r.CapabilityToken, "child": withJM(nestedChild("nested-invalid-tokens", "running", 200, "root-run"), jm{"totalTokens": jm{"input": 1, "output": "bad", "total": 1}})})+"\n")
		reg := projected(t, r)
		find := func(id string) jm {
			for _, c := range reg.Children {
				if c["id"] == id {
					return c
				}
			}
			return nil
		}
		eq(t, jsonOf(find("nested-valid-tokens")["totalTokens"]), `{"input":10,"output":15,"total":25,"window":30,"windowPeak":40}`, "valid tokens")
		_, has := find("nested-invalid-tokens")["totalTokens"]
		eq(t, has, false, "invalid tokens are dropped")
	})
	tw(t, f, "parses only complete jsonl records", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		content := jsonOf(jm{"type": "subagent.nested.started", "ts": 100, "rootRunId": r.RootRunID, "parentRunId": "root-run", "parentStepIndex": 1, "capabilityToken": r.CapabilityToken,
			"child": nestedChild("jsonl-good", "running", 100, "root-run")}) + "\n" + `{"type":"subagent.nested.started"`
		recs := parseNestedEventRecords(content, r)
		eq(t, len(recs), 1, "records")
		eq(t, recs[0].Child["id"], "jsonl-good", "child")
	})
	tw(t, f, "sanitizes nested model bounds and thinking levels", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		nev(t, r, "subagent.nested.updated", 100, "root-run", fl(1), withJM(nestedChild("nested-model-bounds", "running", 100, "root-run"), jm{"model": strings.Repeat("m", 600), "thinking": "turbo",
			"steps": []any{jm{"agent": "leaf", "status": "running", "model": "worker", "thinking": "xhigh"}}}))
		s := projected(t, r).Children[0]
		eq(t, len(s["model"].(string)), 512, "model length")
		_, has := s["thinking"]
		eq(t, has, false, "unknown thinking is dropped")
		eq(t, jmGet(s, "steps", 0, "thinking"), "xhigh", "step thinking")
	})
	tw(t, f, "projects effective model and thinking from async status, including a single-step run", func(t *testing.T) {
		st := &AsyncStatus{RunID: "child-run", Mode: "single", State: "running", StartedAt: 1, Steps: []AsyncStep{{Agent: "worker", Status: "running", Model: "provider/worker", Thinking: "high"}}}
		s := nestedSummaryFromAsyncStatus(st, "/tmp/child-run", nestedSummaryFallback{ID: "child-run", ParentRunID: "parent-run", Depth: 1, Mode: "single", TS: 2})
		eq(t, s["model"], "provider/worker", "model")
		eq(t, s["thinking"], "high", "thinking")
		eq(t, jmGet(s, "steps", 0, "model"), "provider/worker", "step model")
		eq(t, jmGet(s, "steps", 0, "thinking"), "high", "step thinking")
	})
	tw(t, f, "sanitizes malformed process-terminal proofs in nested status summaries", func(t *testing.T) {
		st := &AsyncStatus{RunID: "child-run", Mode: "single", State: "complete", StartedAt: 1,
			ProcessTerminal: &ProcessTerminal{Version: 1, State: "bogus", RunID: "child-run", RunnerProcessInstanceID: "runner-1"},
			Steps:           []AsyncStep{{Agent: "worker", Status: "complete", ProcessTerminal: &ProcessTerminal{Version: 1, State: "observed", RunID: "wrong-run", RunnerProcessInstanceID: "runner-1"}}}}
		s := nestedSummaryFromAsyncStatus(st, "/tmp/child-run", nestedSummaryFallback{ID: "child-run", ParentRunID: "parent-run", Depth: 1, Mode: "single", TS: 2})
		root := s["processTerminal"].(*ProcessTerminal)
		eq(t, root.State, "unknown", "root state")
		eq(t, root.Reason, "proof-write-failed", "root reason")
		step := jmGet(s, "steps", 0, "processTerminal").(*ProcessTerminal)
		eq(t, step.State, "unknown", "step state")
		eq(t, step.Reason, "proof-write-failed", "step reason")
	})
	tw(t, f, "sanitizes runtime acknowledged extensions in nested status summaries", func(t *testing.T) {
		raw := func(ids ...string) map[string]json.RawMessage {
			return map[string]json.RawMessage{"runtimeAcknowledgedExtensions": json.RawMessage(jsonOf(jm{"version": 1, "source": "child-runtime", "ids": ids, "omitted": 0}))}
		}
		st := &AsyncStatus{RunID: "child-run", Mode: "single", State: "complete", StartedAt: 1,
			Extra: raw("/Users/alice/.secret-extension", "ok-ext", strings.Repeat("x", 5000)),
			Steps: []AsyncStep{{Agent: "worker", Status: "complete", Extra: raw("C:/Users/alice/secret")}}}
		s := nestedSummaryFromAsyncStatus(st, "/tmp/child-run", nestedSummaryFallback{ID: "child-run", ParentRunID: "parent-run", Depth: 1, Mode: "single", TS: 2})
		eq(t, jsonOf(jmGet(s, "runtimeAcknowledgedExtensions", "ids")), `["ok-ext"]`, "ids")
		eq(t, jmGet(s, "steps", 0, "runtimeAcknowledgedExtensions"), nil, "a step with only unsafe ids has none")
	})
	tw(t, f, "removes evicted status files without replaying completed children", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-run")
		for i := range 1000 {
			nev(t, r, "subagent.nested.updated", float64(i+1), "root-run", fl(1), nestedChild("nested-retained", "running", i+1, "root-run"))
		}
		nev(t, r, "subagent.nested.completed", 1001, "root-run", fl(1), nestedChild("nested-retained", "complete", 1001, "root-run"))
		count := func() int { e, _ := os.ReadDir(r.EventSink); return len(e) }
		eq(t, count(), 1001, "files before")
		first := projected(t, r)
		eq(t, len(first.ProcessedEvents), 1000, "processed")
		eq(t, count(), 1000, "files after")
		eq(t, first.Children[0]["state"], "complete", "state")
		second := projected(t, r)
		eq(t, jsonOf(second), jsonOf(first), "second projection")
		eq(t, second.Children[0]["state"], "complete", "second state")
	})
	tw(t, f, "keeps bounded root and step session names from async status", func(t *testing.T) {
		st := &AsyncStatus{RunID: "child-run", Mode: "single", State: "running", StartedAt: 1, Steps: []AsyncStep{{Agent: "reviewer", SessionName: "reviewer: Inspect the changed auth middleware", Status: "running"}}}
		s := nestedSummaryFromAsyncStatus(st, "/tmp/child-run", nestedSummaryFallback{ID: "child-run", ParentRunID: "root-run", Depth: 1, TS: 1})
		eq(t, s["sessionName"], "reviewer: Inspect the changed auth middleware", "name")
		eq(t, jmGet(s, "steps", 0, "sessionName"), "reviewer: Inspect the changed auth middleware", "step name")
	})
}
