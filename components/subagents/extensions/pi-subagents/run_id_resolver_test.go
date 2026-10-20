package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Twins of run-id-resolver (runs/background/run-id-resolver.ts).

func foregroundState(id string) *SubagentState {
	s := newSubagentState()
	s.ForegroundControls[id] = &ForegroundRunRecord{RunID: id}
	return s
}

func nestedWithChild(t *testing.T, rootRunID, id string) nestedRoute {
	t.Helper()
	r := trackRoute(t, rootRunID)
	writeNestedChildAt(t, r, rootRunID, id, fl(0))
	return r
}

func writeNestedChildAt(t *testing.T, r nestedRoute, parentRunID, id string, step *float64) {
	t.Helper()
	child := jm{"id": id, "parentRunId": parentRunID, "depth": 1, "path": []any{jm{"runId": parentRunID}}, "state": "running", "agent": "worker"}
	if step != nil {
		child["parentStepIndex"] = *step
		child["path"] = []any{jm{"runId": parentRunID, "stepIndex": *step}}
	}
	nev(t, r, "subagent.nested.updated", 100, parentRunID, step, child)
}

func stateWithNestedRoute(r nestedRoute) *SubagentState {
	s := foregroundState("foreground-only")
	s.ForegroundControls[r.RootRunID] = &ForegroundRunRecord{RunID: r.RootRunID, NestedRoute: &r}
	return s
}

func resolveWith(id string, state *SubagentState, asyncRoot, results string, nested *nestedRunResolutionScope) (*ResolvedRunID, error) {
	return resolveSubagentRunID(id, state, asyncRoot, results, nested)
}

func TestRunIDResolverTwins(t *testing.T) {
	const f = "run-id-resolver"
	tw(t, f, "prefers exact foreground, then exact async, then exact nested before prefix matches", func(t *testing.T) {
		nestedTemp(t)
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "runs"), filepath.Join(root, "results")
		writeFile(t, filepath.Join(asyncRoot, "shared-id", "status.json"), "{}")
		nestedWithChild(t, "root-shared", "shared-id")
		nestedWithChild(t, "root-prefix", "shared-id-child")
		r, err := resolveWith("shared-id", foregroundState("shared-id"), asyncRoot, results, nil)
		eq(t, err, nil, "err")
		eq(t, r.Kind, "foreground", "foreground first")
		r, _ = resolveWith("shared-id", nil, asyncRoot, results, nil)
		eq(t, r.Kind, "async", "async second")
		_ = os.RemoveAll(filepath.Join(asyncRoot, "shared-id"))
		r, _ = resolveWith("shared-id", nil, asyncRoot, results, nil)
		eq(t, r.Kind, "nested", "nested third")
		eq(t, r.ID, "shared-id", "id")
	})
	tw(t, f, "does not let a mission-only directory shadow owned nested status by exact id or prefix", func(t *testing.T) {
		nestedTemp(t)
		withTempHome(t)
		root := tmp(t)
		r := nestedWithChild(t, "mission-root", "mission-child")
		state := stateWithNestedRoute(r)
		asyncRoot := filepath.Join(root, "runs")
		writeFile(t, filepath.Join(asyncRoot, "mission-child", "mission.json"), "{}")
		rt := &asyncRuntime{state: state, dirs: asyncDirs{Async: asyncRoot, Results: filepath.Join(root, "results")}}
		for _, id := range []string{"mission-child", "mission-chi"} {
			got, err := resolveWith(id, state, asyncRoot, filepath.Join(root, "results"), nil)
			eq(t, err, nil, id)
			eq(t, got.Kind, "nested", id)
			text, isErr := rt.inspectStatus("", map[string]any{"id": id})
			eq(t, isErr, false, "status of "+id+": "+text)
		}
	})
	tw(t, f, "retains session-owned nested lookup after coordinator removal and descendant completion", func(t *testing.T) {
		nestedTemp(t)
		withTempHome(t)
		r := nestedWithChild(t, "retired-root", "finished-coordinator")
		nev(t, r, "subagent.nested.started", 110, "finished-coordinator", nil, jm{"id": "retired-child", "parentRunId": "finished-coordinator", "depth": 2,
			"path": []any{jm{"runId": r.RootRunID}, jm{"runId": "finished-coordinator"}}, "state": "running", "agent": "worker"})
		nev(t, r, "subagent.nested.completed", 120, r.RootRunID, nil, jm{"id": "finished-coordinator", "parentRunId": r.RootRunID, "depth": 1,
			"path": []any{jm{"runId": r.RootRunID}}, "state": "complete", "agent": "coordinator"})
		state := stateWithNestedRoute(r)
		state.CurrentSessionID = "owner"
		state.ForegroundControls[r.RootRunID].SessionID = "owner"
		got, _ := resolveWith("retired-child", state, "", "", nil)
		eq(t, got.Kind, "nested", "before removal")
		eq(t, removeForegroundControlIfIdle(state, r.RootRunID, nil), true, "control removed")
		got, _ = resolveWith("retired-child", state, "", "", nil)
		eq(t, got.Kind, "nested", "after removal")
		nev(t, r, "subagent.nested.completed", 200, "finished-coordinator", nil, jm{"id": "retired-child", "parentRunId": "finished-coordinator", "depth": 2,
			"path": []any{jm{"runId": r.RootRunID}, jm{"runId": "finished-coordinator"}}, "state": "complete", "agent": "worker"})
		rt := &asyncRuntime{state: state, dirs: defaultAsyncDirs()}
		text, isErr := rt.inspectStatus("owner", map[string]any{"id": "retired-child"})
		eq(t, isErr, false, text)
		hasRe(t, text, `State: complete`)
		none, _ := resolveWith("retired-child", state, "", "", &nestedRunResolutionScope{HasRoutes: true})
		eq(t, none == nil, true, "child subtree restriction still wins")
		state.CurrentSessionID = "foreign"
		none, _ = resolveWith("retired-child", state, "", "", nil)
		eq(t, none == nil, true, "a foreign session does not see it")
	})
	tw(t, f, "reports one combined ambiguity for prefixes across namespaces", func(t *testing.T) {
		nestedTemp(t)
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "runs"), filepath.Join(root, "results")
		writeFile(t, filepath.Join(asyncRoot, "fanout-x-async", "status.json"), "{}")
		nestedWithChild(t, "root-fanout", "fanout-x-nested")
		_, err := resolveWith("fanout-x", nil, asyncRoot, results, nil)
		hasRe(t, err.Error(), `Ambiguous subagent run id prefix 'fanout-x' matched: async:fanout-x-async, nested:fanout-x-nested`)
	})
	tw(t, f, "limits nested lookup to active state routes when state is provided", func(t *testing.T) {
		nestedTemp(t)
		allowed := nestedWithChild(t, "root-allowed", "shared-nested")
		nestedWithChild(t, "root-outside", "shared-nested")
		_, err := resolveWith("shared-nested", nil, "", "", nil)
		hasRe(t, err.Error(), `(?i)ambiguous across authorized registries|ambiguous across registries`)
		none, _ := resolveWith("shared-nested", foregroundState("foreground-only"), "", "", nil)
		eq(t, none == nil, true, "a state without the route sees nothing")
		got, err := resolveWith("shared-nested", stateWithNestedRoute(allowed), "", "", nil)
		eq(t, err, nil, "err")
		eq(t, got.Kind, "nested", "kind")
		eq(t, got.Match.RootRunID, "root-allowed", "root")
	})
	tw(t, f, "limits nested lookup to descendants of a scoped child address", func(t *testing.T) {
		nestedTemp(t)
		r := trackRoute(t, "root-scoped")
		writeNestedChildAt(t, r, "root-scoped", "same-child-zero", fl(0))
		writeNestedChildAt(t, r, "root-scoped", "same-child-one", fl(1))
		_, err := resolveWith("same-child", nil, "", "", &nestedRunResolutionScope{Routes: []nestedRoute{r}, HasRoutes: true})
		hasRe(t, err.Error(), `Ambiguous subagent run id prefix 'same-child'`)
		got, err := resolveWith("same-child", nil, "", "", &nestedRunResolutionScope{Routes: []nestedRoute{r}, HasRoutes: true, DescendantOf: &nestedRunDescendantOf{ParentRunID: "root-scoped", ParentStepIndex: fl(0)}})
		eq(t, err, nil, "err")
		eq(t, got.Kind, "nested", "kind")
		eq(t, got.ID, "same-child-zero", "id")
		eq(t, got.Match.Run["parentStepIndex"], float64(0), "parent step index")
	})
	tw(t, f, "reports async prefix ambiguity without parsing resolver error text", func(t *testing.T) {
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "runs"), filepath.Join(root, "results")
		writeFile(t, filepath.Join(asyncRoot, "dupe-aaa-one", "status.json"), "{}")
		writeFile(t, filepath.Join(asyncRoot, "dupe-aaa-two", "status.json"), "{}")
		_, err := resolveWith("dupe-aaa", nil, asyncRoot, results, nil)
		hasRe(t, err.Error(), `Ambiguous subagent run id prefix 'dupe-aaa' matched: async:dupe-aaa-one, async:dupe-aaa-two`)
	})
	tw(t, f, "does not resolve unindexed result files by prefix", func(t *testing.T) {
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "runs"), filepath.Join(root, "results")
		writeFile(t, filepath.Join(results, "legacy-run.json"), `{"id":"legacy-run","sessionId":"session-a","success":true}`)
		exact, _ := resolveWith("legacy-run", nil, asyncRoot, results, nil)
		eq(t, exact.Kind, "async", "kind")
		eq(t, exact.ID, "legacy-run", "id")
		prefix, _ := resolveWith("legacy", nil, asyncRoot, results, nil)
		eq(t, prefix == nil, true, "prefix finds nothing")
	})
	tw(t, f, "resolves workflow tool-call ids through active and result indexes", func(t *testing.T) {
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "async"), filepath.Join(root, "results")
		active := filepath.Join(asyncRoot, "workflow-active")
		writeFile(t, filepath.Join(active, "status.json"), `{"runId":"workflow-active","toolCallId":"call-active","state":"running","mode":"workflow","startedAt":1,"lastUpdate":1,"steps":[]}`)
		if err := updateActiveRunIndex(active, "running", "call-active", indexOptions{}); err != nil {
			t.Fatal(err)
		}
		got, _ := resolveWith("call-active", nil, asyncRoot, results, nil)
		eq(t, got.Kind, "async", "kind")
		eq(t, got.ID, "workflow-active", "id")
		_ = releaseActiveRunIndex(active)
		gone, _ := resolveWith("call-active", nil, asyncRoot, results, nil)
		eq(t, gone == nil, true, "released")
		if _, err := writeAsyncResultFile(resultFilePath(results, "workflow-done"), map[string]any{"id": "workflow-done", "runId": "workflow-done", "toolCallId": "call-done", "sessionId": "session-a", "state": "complete", "success": true}); err != nil {
			t.Fatal(err)
		}
		done, _ := resolveWith("call-done", nil, asyncRoot, results, nil)
		eq(t, done.Kind, "async", "kind")
		eq(t, done.ID, "workflow-done", "id")
	})
	tw(t, f, "prefers a newer pending payload when resolving run and tool-call ids", func(t *testing.T) {
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "async"), filepath.Join(root, "results")
		resultPath := resultFilePath(results, "workflow-done")
		payload := func(success bool) map[string]any {
			return map[string]any{"id": "workflow-done", "runId": "workflow-done", "toolCallId": "call-done", "sessionId": "session-a", "success": success}
		}
		if _, err := writeAsyncResultFile(resultPath, payload(false)); err != nil {
			t.Fatal(err)
		}
		old := resultRename
		resultRename = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EEXIST} }
		t.Cleanup(func() { resultRename = old })
		if err := writePendingAsyncResultFile(resultPath, payload(true)); err != nil {
			t.Fatal(err)
		}
		pending := filepath.Join(results, "result-pending", "session-a", "workflow-done.json")
		byRun, _ := resolveWith("workflow-done", nil, asyncRoot, results, nil)
		eq(t, byRun.Kind, "async", "kind")
		eq(t, byRun.Location.ResultPath, pending, "run id picks the pending payload")
		byTool, _ := resolveWith("call-done", nil, asyncRoot, results, nil)
		eq(t, byTool.ID, "workflow-done", "tool call id")
		eq(t, byTool.Location.ResultPath, pending, "tool call id picks the pending payload")
		eq(t, readJSONMap(t, pending)["success"], true, "pending payload")
		eq(t, readJSONMap(t, resultPath)["success"], false, "public payload")
	})
	tw(t, f, "resolves oversized workflow tool-call ids through bounded indexes", func(t *testing.T) {
		root := tmp(t)
		asyncRoot, results := filepath.Join(root, "async"), filepath.Join(root, "results")
		toolCallID := "call_" + strings.Repeat("界", 100)
		active := filepath.Join(asyncRoot, "workflow-active-long")
		writeFile(t, filepath.Join(active, "status.json"), jsonOf(map[string]any{"runId": "workflow-active-long", "toolCallId": toolCallID, "state": "running", "mode": "workflow", "startedAt": 1, "lastUpdate": 1, "steps": []any{}}))
		if err := updateActiveRunIndex(active, "running", toolCallID, indexOptions{}); err != nil {
			t.Fatal(err)
		}
		got, _ := resolveWith(toolCallID, nil, asyncRoot, results, nil)
		eq(t, got.ID, "workflow-active-long", "active")
		_ = releaseActiveRunIndex(active)
		if _, err := writeAsyncResultFile(resultFilePath(results, "workflow-done-long"), map[string]any{"id": "workflow-done-long", "runId": "workflow-done-long", "toolCallId": toolCallID, "sessionId": "session-a", "state": "complete", "success": true}); err != nil {
			t.Fatal(err)
		}
		got, _ = resolveWith(toolCallID, nil, asyncRoot, results, nil)
		eq(t, got.ID, "workflow-done-long", "result")
	})
	tw(t, f, "rejects unsafe nested id tokens before lookup", func(t *testing.T) {
		for _, id := range []string{"../run", "a/b", ""} {
			_, err := resolveWith(id, nil, "", "", nil)
			hasRe(t, err.Error(), `safe id token`)
		}
	})
}
