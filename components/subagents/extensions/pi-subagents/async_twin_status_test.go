package pi_subagents

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Twins of async-status-snapshot.test.ts and async-status-projection.test.ts.

// js parses a JSON literal into the JSON-shaped value the projection reads.
func js(t *testing.T, s string) jm {
	t.Helper()
	var m jm
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return m
}

func jsList(t *testing.T, s string) []jm {
	t.Helper()
	var m []jm
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return m
}

// generic is any value as plain JSON data.
func generic(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func f64(v float64) *float64 { return &v }

func snapAt(jobs []jm, generatedAt int64) *AsyncStatusSnapshot {
	return projectAsyncStatusSnapshot(jobs, SnapshotOptions{GeneratedAt: &generatedAt}, nowMs)
}

func snapOpts(jobs []jm, o SnapshotOptions) *AsyncStatusSnapshot {
	return projectAsyncStatusSnapshot(jobs, o, nowMs)
}

func TestAsyncStatusSnapshotTwins(t *testing.T) {
	const f = "async-status-snapshot"
	const needle = "PRIVATE_LEAK_NEEDLE"
	body := func(s *AsyncStatusSnapshot) string { b, _ := json.Marshal(s); return string(b) }
	tw(t, f, "projects current async jobs with a versioned safe shape", func(t *testing.T) {
		snap := snapAt([]jm{js(t, `{"asyncId":"run-1","asyncDir":"/tmp/`+needle+`/run","cwd":"/repo/`+needle+`","sessionRoot":"/sessions/`+needle+`","sessionDir":"/session-dir/`+needle+`",
			"outputFile":"/output/`+needle+`.log","sessionFile":"/session-file/`+needle+`.jsonl","sessionId":"session-a","status":"running","mode":"parallel","agents":["worker"],
			"description":"task `+needle+`","startedAt":100,"updatedAt":150,"currentTool":"bash\n\u001b]8;;bad\u0007","currentPath":"/current/`+needle+`",
			"steps":[{"index":0,"agent":"worker","status":"running","startedAt":110,"currentTool":"read","currentToolArgs":"args `+needle+`","recentOutput":["output `+needle+`"],
			"error":"error `+needle+`","transcriptPath":"/transcript/`+needle+`.jsonl"}]}`)}, 200)
		eq(t, snap.Kind, asyncStatusSnapshotKind, "kind")
		eq(t, snap.Version, asyncStatusSnapshotVersion, "version")
		eq(t, snap.GeneratedAt, int64(200), "generatedAt")
		eq(t, len(snap.Runs), 1, "runs")
		want := js(t, `{"id":"run-1","kind":"subagent","label":"worker","state":"running","startedAt":100,"updatedAt":150,"activity":{"currentTool":"bash"},
			"children":[{"id":"step:0","kind":"step","label":"worker","state":"running","startedAt":110,"updatedAt":110,"activity":{"currentTool":"read"}}]}`)
		if got := generic(t, snap.Runs[0]); !reflect.DeepEqual(got, any(want)) {
			t.Errorf("run %v\nwant %v", got, want)
		}
		for _, banned := range []string{needle, "currentPath", "currentToolArgs", "recentOutput", "transcriptPath"} {
			if strings.Contains(body(snap), banned) {
				t.Errorf("snapshot leaks %s", banned)
			}
		}
	})
	tw(t, f, "normalizes pending child steps to queued", func(t *testing.T) {
		snap := snapAt([]jm{js(t, `{"asyncId":"run","asyncDir":"/tmp/run","status":"queued","agents":["worker"],"steps":[{"agent":"worker","status":"pending"}]}`)}, 1)
		eq(t, snap.Runs[0].Children[0].State, "queued", "state")
	})
	tw(t, f, "projects loaded host CI/gate nodes with bounded provider-neutral metadata", func(t *testing.T) {
		snap := snapAt([]jm{js(t, `{"asyncId":"workflow-1","asyncDir":"/tmp/workflow-1","status":"running","mode":"workflow","agents":[],"hostSteps":[{"version":1,"kind":"host-step","monitorKind":"gate",
			"id":"gate-1","label":"Gate","provider":"opaque-provider","state":"done","verdict":"inconclusive","reasonCode":"stale-head",
			"freshness":{"expectedRef":"old","observedRef":"new","stale":true},"reportPath":"/private/reports/gate.json","updatedAt":100}]}`)}, 200)
		want := []any{js(t, `{"id":"gate-1","kind":"host-step","label":"Gate","state":"partial","updatedAt":100,"endedAt":100,
			"hostStep":{"kind":"gate","provider":"opaque-provider","state":"done","verdict":"inconclusive","reasonCode":"stale-head","stale":true,"report":"gate.json"}}`)}
		if got := generic(t, snap.Runs[0].Children); !reflect.DeepEqual(got, any(want)) {
			t.Errorf("children %v\nwant %v", got, want)
		}
	})
	tw(t, f, "applies run, child, depth, string, and byte caps", func(t *testing.T) {
		var jobs []jm
		for r := 0; r < 5; r++ {
			steps := []any{}
			for s := 0; s < 5; s++ {
				steps = append(steps, jm{"agent": "child-" + string(rune('0'+s)) + "-" + strings.Repeat("y", 50), "status": "running", "children": []any{
					jm{"id": "nested-" + string(rune('0'+s)), "parentRunId": "run-" + string(rune('0'+r)), "depth": 1.0, "path": []any{}, "state": "running", "agent": "nested"}}})
			}
			jobs = append(jobs, jm{"asyncId": "run-" + string(rune('0'+r)), "asyncDir": "/tmp/run-" + string(rune('0'+r)), "status": "running", "mode": "workflow",
				"agents": []any{"agent-" + string(rune('0'+r)) + "-" + strings.Repeat("x", 50)}, "startedAt": float64(r), "updatedAt": float64(r), "steps": steps})
		}
		snap := snapOpts(jobs, SnapshotOptions{GeneratedAt: i64(10), MaxRuns: f64(2), MaxChildrenPerNode: f64(2), MaxDepth: f64(1), MaxStringLength: f64(16), MaxSerializedBytes: f64(1200)})
		eq(t, len(snap.Runs), 2, "runs")
		eq(t, snap.Omitted.Runs, 3, "omitted runs")
		eq(t, len(snap.Runs[0].Children), 2, "children")
		if snap.Omitted.Children < 6 {
			t.Errorf("omitted children %d", snap.Omitted.Children)
		}
		if n := len(jsUTF16(snap.Runs[0].Label)); n > 16 {
			t.Errorf("label length %d", n)
		}
		if n := len(body(snap)); n > 1200 {
			t.Errorf("snapshot is %d bytes", n)
		}
		capped := snapOpts(jobs, SnapshotOptions{MaxSerializedBytes: f64(512)})
		eq(t, capped.Omitted.ByteLimitExceeded, true, "byteLimitExceeded")
		if n := len(body(capped)); n > 512 {
			t.Errorf("capped snapshot is %d bytes", n)
		}
	})
	tw(t, f, "uses current-session state and retained fleet jobs without rebuilding history", func(t *testing.T) {
		state := newSubagentState()
		state.CurrentSessionID = "session-a"
		state.AsyncJobs["active"] = &AsyncJobState{AsyncID: "active", AsyncDir: "/tmp/active", SessionID: "session-a", Status: "running", Agents: []string{"worker"}}
		state.FleetJobs["terminal"] = &AsyncJobState{AsyncID: "terminal", AsyncDir: "/tmp/terminal", SessionID: "session-a", Status: "complete", Agents: []string{"reviewer"}, UpdatedAt: 300, OutputFile: "/tmp/" + needle + ".log"}
		state.FleetJobs["foreign"] = &AsyncJobState{AsyncID: "foreign", AsyncDir: "/tmp/foreign", SessionID: "other", Status: "running", Agents: []string{"hidden"}}
		build := func(session string, at *int64) *AsyncStatusSnapshot {
			return snapOpts(projectionJobs(asyncStatusSnapshotJobsForState(state, session)), SnapshotOptions{GeneratedAt: at})
		}
		snap := build("session-a", i64(1))
		var ids []string
		var endedAt *int64
		for _, r := range snap.Runs {
			ids = append(ids, r.ID)
			if r.ID == "terminal" {
				endedAt = r.EndedAt
			}
		}
		if len(ids) == 2 && ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
		eq(t, ids, []string{"active", "terminal"}, "run ids")
		if endedAt == nil || *endedAt != 300 {
			t.Errorf("endedAt %v", endedAt)
		}
		if strings.Contains(body(snap), needle) {
			t.Error("snapshot leaks the output file")
		}
		eq(t, len(build("other", nil).Runs), 0, "another session's runs")
	})
	tw(t, f, "encodes RPC widget payloads as a string-array snapshot", func(t *testing.T) {
		lines := encodeAsyncStatusSnapshotWidget([]*AsyncJobState{{AsyncID: "run", AsyncDir: "/tmp/run", Status: "queued", Agents: []string{"planner"}}}, SnapshotOptions{GeneratedAt: i64(5)})
		eq(t, len(lines), 1, "lines")
		if !strings.HasPrefix(lines[0], asyncStatusSnapshotWidgetPrefix) {
			t.Fatalf("prefix: %s", lines[0])
		}
		var snap map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[0], asyncStatusSnapshotWidgetPrefix)), &snap); err != nil {
			t.Fatal(err)
		}
		eq(t, snap["kind"], asyncStatusSnapshotKind, "kind")
		eq(t, snap["version"], 1.0, "version")
		eq(t, snap["runs"].([]any)[0].(map[string]any)["id"], "run", "run id")
	})
}

func TestAsyncStatusProjectionTwins(t *testing.T) {
	const f = "async-status-projection"
	job := func(t *testing.T, s string) jm { return js(t, s) }
	merge := func(base jm, over jm) jm {
		out := jm{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	hostStep := func(over jm) jm {
		return merge(jm{"version": 1.0, "kind": "host-step", "monitorKind": "ci", "id": "ci-check", "label": "CI checks", "provider": "github-ci", "state": "done", "verdict": "pass", "updatedAt": 20.0}, over)
	}
	type idKindState struct{ ID, Kind, State string }
	summary := func(nodes []*SnapshotNode) []idKindState {
		var out []idKindState
		for _, n := range nodes {
			out = append(out, idKindState{n.ID, n.Kind, n.State})
		}
		return out
	}
	ids := func(nodes []*SnapshotNode) []string {
		out := []string{}
		for _, n := range nodes {
			out = append(out, n.ID)
		}
		return out
	}
	omitted := func(runs, children int) SnapshotOmitted { return SnapshotOmitted{Runs: runs, Children: children} }
	// the fixtures of the test file
	materializedWorkflow := func(t *testing.T) jm {
		return job(t, `{"asyncId":"wf-1","asyncDir":"/tmp/wf-1","status":"running","mode":"workflow","agents":["reviewer"],"startedAt":1000,"updatedAt":5000,"steps":[
			{"workflowKey":"rev-core","label":"rev-core","agent":"reviewer","status":"completed","runId":"child-core","startedAt":1500,"durationMs":2500},
			{"workflowKey":"rev-r","label":"rev-r","agent":"reviewer","status":"running","runId":"child-r","startedAt":1500},
			{"workflowKey":"rev-parity","label":"rev-parity","agent":"reviewer","status":"pending"}]}`)
	}
	runningChild := func(t *testing.T) jm {
		return job(t, `{"asyncId":"child-r","asyncDir":"/tmp/child-r","status":"running","mode":"single","parentWorkflowRunId":"wf-1","workflowKey":"rev-r","agents":["reviewer"],"startedAt":1700,"updatedAt":4900,
			"currentTool":"read","toolCount":3,"turnCount":1,"steps":[{"agent":"reviewer","status":"running"}]}`)
	}
	unmatchedChild := func(t *testing.T) jm {
		return job(t, `{"asyncId":"child-extra","asyncDir":"/tmp/child-extra","status":"running","mode":"single","parentWorkflowRunId":"wf-1","agents":["helper"],"startedAt":2000,"steps":[{"agent":"helper","status":"running"}]}`)
	}
	orphanedChild := func(t *testing.T) jm {
		return job(t, `{"asyncId":"child-orphan","asyncDir":"/tmp/child-orphan","status":"running","mode":"single","parentWorkflowRunId":"wf-gone","workflowKey":"lane","agents":["scout"],"startedAt":3000,"steps":[{"agent":"scout","status":"running"}]}`)
	}

	tw(t, f, "projects already-loaded jobs in deterministic newest-first order", func(t *testing.T) {
		snap := snapAt([]jm{
			job(t, `{"asyncId":"older","asyncDir":"/tmp/older","status":"complete","agents":["reviewer"],"updatedAt":10}`),
			job(t, `{"asyncId":"newer","asyncDir":"/tmp/newer","status":"running","mode":"workflow","agents":["worker"],"updatedAt":20,"steps":[{"agent":"worker","status":"pending"}]}`),
		}, 30)
		eq(t, snap.Kind, asyncStatusSnapshotKind, "kind")
		eq(t, snap.Version, asyncStatusSnapshotVersion, "version")
		eq(t, snap.GeneratedAt, int64(30), "generatedAt")
		eq(t, summary(snap.Runs), []idKindState{{"newer", "workflow", "running"}, {"older", "subagent", "complete"}}, "runs")
		eq(t, snap.Runs[0].Children[0].State, "queued", "child state")
	})
	tw(t, f, "preserves partial needs-attention status while excluding private evidence", func(t *testing.T) {
		snap := snapAt([]jm{job(t, `{"asyncId":"partial-run","asyncDir":"/tmp/partial-run","status":"partial","agents":["writer"],"activityState":"needs_attention","steps":[{"agent":"writer","status":"partial",
			"activityState":"needs_attention","error":"Required file-only output was not produced: /private/report.md","effects":{"fileMutation":true}}]}`)}, 1)
		eq(t, snap.Runs[0].State, "partial", "run state")
		eq(t, *snap.Runs[0].Activity.State, "needs_attention", "run activity")
		eq(t, snap.Runs[0].Children[0].State, "partial", "child state")
		eq(t, *snap.Runs[0].Children[0].Activity.State, "needs_attention", "child activity")
		b, _ := json.Marshal(snap)
		for _, banned := range []string{"private/report", "fileMutation", "Required file-only output"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("leaks %s", banned)
			}
		}
	})
	tw(t, f, "maps malformed persisted states to partial instead of widening the public state union", func(t *testing.T) {
		snap := snapOpts([]jm{job(t, `{"asyncId":"bad-state","asyncDir":"/tmp/bad-state","status":"mystery","steps":[{"agent":"worker","status":"also-mystery"}]}`)}, SnapshotOptions{})
		eq(t, snap.Runs[0].State, "partial", "run state")
		eq(t, snap.Runs[0].Children[0].State, "partial", "child state")
	})
	tskip(t, f, "projects compact Fleet workflow rows without applying UI bounds", "projectAsyncWorkflowRows builds the rows of the Fleet terminal view (tui/fleet.ts), which belongs to the terminal-views slice and is not ported; the snapshot projection of the same facts is twinned")
	tskip(t, f, "projects typed CI and gate host rows without treating them as child agents", "projectAsyncWorkflowRows builds Fleet view rows (terminal-views slice, not ported); the host-step node contract it reads is twinned through the snapshot cases")
	tw(t, f, "projects done host steps without verdicts as partial", func(t *testing.T) {
		step := hostStep(nil)
		delete(step, "verdict")
		snap := snapOpts([]jm{{"asyncId": "inconclusive-gate", "asyncDir": "/tmp/g", "status": "running", "hostSteps": []any{step}}}, SnapshotOptions{})
		eq(t, len(snap.Runs[0].Children), 1, "children")
		eq(t, snap.Runs[0].Children[0].State, "partial", "state")
	})
	tw(t, f, "reserves bounded snapshot capacity for host steps", func(t *testing.T) {
		snap := snapOpts([]jm{{"asyncId": "bounded-gate", "asyncDir": "/tmp/b", "status": "running",
			"steps":     []any{jm{"agent": "first", "status": "running"}, jm{"agent": "second", "status": "running"}},
			"hostSteps": []any{hostStep(nil)}}}, SnapshotOptions{MaxChildrenPerNode: f64(2)})
		type kindID struct{ Kind, ID string }
		var got []kindID
		for _, c := range snap.Runs[0].Children {
			got = append(got, kindID{c.Kind, c.ID})
		}
		eq(t, got, []kindID{{"step", "step:0"}, {"host-step", "ci-check"}}, "children")
		eq(t, snap.Omitted.Children, 1, "omitted children")
	})
	tskip(t, f, "omits malformed host nodes instead of rendering them as agents", "projectAsyncWorkflowRows (Fleet view rows, terminal-views slice, not ported); malformed host nodes are dropped by validHostStepNodes, covered by the snapshot's malformed host step case")
	tskip(t, f, "annotates authoritative children without projecting unmatched preflight lanes", "workflow preflight lanes (workflow-preflight.ts) annotate Fleet rows; the workflow slice and the Fleet view are not ported")
	tskip(t, f, "prefers a specific preflight lane over an earlier broad phase alias", "workflow preflight lane matching (workflow-preflight.ts) belongs to the workflow slice, not ported")
	tskip(t, f, "projects known runs.lanes stages from the workflow graph, including pending stages", "projectAsyncWorkflowRows with a preflight (Fleet rows and workflow preflight, not ported); the snapshot's own use of the graph's stages is twinned by the materialized-child cases")
	tskip(t, f, "preserves duplicate loaded rows when a declared lane key is reused", "projectAsyncWorkflowRows with preflight lanes (Fleet rows and workflow preflight, not ported); lane reuse in the snapshot is twinned by the exact run id precedence case")
	tw(t, f, "projects a materialized workflow child once, as its lane, with the child's live facts", func(t *testing.T) {
		snap := snapAt([]jm{materializedWorkflow(t), runningChild(t)}, 5000)
		eq(t, ids(snap.Runs), []string{"wf-1"}, "runs")
		type ikls struct{ ID, Kind, Label, State string }
		var got []ikls
		for _, c := range snap.Runs[0].Children {
			got = append(got, ikls{c.ID, c.Kind, c.Label, c.State})
		}
		eq(t, got, []ikls{{"rev-core", "step", "rev-core", "complete"}, {"rev-r", "step", "rev-r", "running"}, {"rev-parity", "step", "rev-parity", "queued"}}, "children")
		running := snap.Runs[0].Children[1]
		eq(t, *running.StartedAt, int64(1700), "startedAt")
		eq(t, generic(t, running.Activity), any(js(t, `{"currentTool":"read","toolCount":3,"turnCount":1}`)), "activity")
		eq(t, len(running.Children), 0, "children of the running lane")
		eq(t, snap.Omitted, omitted(0, 0), "omitted")
	})
	tw(t, f, "keeps children matching no lane under their parent and orphaned children at the root", func(t *testing.T) {
		snap := snapAt([]jm{materializedWorkflow(t), runningChild(t), unmatchedChild(t), orphanedChild(t)}, 5000)
		eq(t, ids(snap.Runs), []string{"wf-1", "child-orphan"}, "runs")
		type ikl struct{ ID, Kind, Label string }
		var got []ikl
		for _, c := range snap.Runs[0].Children {
			got = append(got, ikl{c.ID, c.Kind, c.Label})
		}
		eq(t, got, []ikl{{"rev-core", "step", "rev-core"}, {"rev-r", "step", "rev-r"}, {"rev-parity", "step", "rev-parity"}, {"child-extra", "subagent", "helper"}}, "children")
		type kl struct{ Kind, Label string }
		var orphan []kl
		for _, c := range snap.Runs[1].Children {
			orphan = append(orphan, kl{c.Kind, c.Label})
		}
		eq(t, orphan, []kl{{"step", "scout"}}, "orphan children")
	})
	tw(t, f, "counts a lane and its materialized child once when the depth cap hides them", func(t *testing.T) {
		snap := snapOpts([]jm{materializedWorkflow(t), runningChild(t), unmatchedChild(t)}, SnapshotOptions{GeneratedAt: i64(5000), MaxDepth: f64(0)})
		eq(t, len(snap.Runs[0].Children), 0, "children")
		eq(t, snap.Omitted.Children, 4, "omitted children")
	})
	tw(t, f, "assigns a materialized child once and gives its exact run id precedence over a reused lane key", func(t *testing.T) {
		parent := jm{"asyncId": "duplicate-keys", "asyncDir": "/tmp/p", "status": "running", "mode": "workflow", "steps": []any{
			jm{"agent": "worker", "workflowKey": "same", "runId": "old", "label": "first", "status": "completed"},
			jm{"agent": "worker", "workflowKey": "same", "runId": "live", "label": "second", "status": "running"}}}
		child := jm{"asyncId": "live", "asyncDir": "/tmp/live", "parentWorkflowRunId": "duplicate-keys", "workflowKey": "same", "status": "running", "updatedAt": 30.0}
		snap := snapOpts([]jm{parent, child}, SnapshotOptions{})
		type lsu struct {
			Label, State string
			Updated      *int64
		}
		var got []lsu
		for _, c := range snap.Runs[0].Children {
			got = append(got, lsu{c.Label, c.State, c.UpdatedAt})
		}
		eq(t, got, []lsu{{"first", "complete", nil}, {"second", "running", i64(30)}}, "children")
		eq(t, snapOpts([]jm{parent, child}, SnapshotOptions{MaxDepth: f64(0)}).Omitted.Children, 2, "omitted at depth 0")
	})
	tw(t, f, "keeps live children visible as roots when their workflow parent is not running", func(t *testing.T) {
		mk := func(parentStatus, childStatus string) []jm {
			return []jm{
				{"asyncId": "paused-parent", "asyncDir": "/tmp/pp", "status": parentStatus, "mode": "workflow", "updatedAt": 200.0,
					"steps":          []any{jm{"agent": "worker", "workflowKey": "lane", "runId": "live-child", "status": "paused"}},
					"nestedChildren": []any{jm{"id": "live-child", "state": "running", "agent": "worker"}}},
				{"asyncId": "live-child", "asyncDir": "/tmp/lc", "parentWorkflowRunId": "paused-parent", "workflowKey": "lane", "status": childStatus, "updatedAt": 100.0,
					"steps": []any{jm{"agent": "worker", "status": "running"}}},
			}
		}
		snap := snapOpts(mk("paused", "running"), SnapshotOptions{})
		type is struct{ ID, State string }
		var got []is
		for _, r := range snap.Runs {
			got = append(got, is{r.ID, r.State})
		}
		eq(t, got, []is{{"live-child", "running"}, {"paused-parent", "paused"}}, "runs")
		eq(t, ids(snap.Runs[0].Children), []string{"step:0"}, "child steps")
		eq(t, len(snap.Runs[1].Children), 0, "parent children")
		eq(t, snap.Omitted, omitted(0, 0), "omitted")
		eq(t, snapOpts(mk("paused", "running"), SnapshotOptions{MaxDepth: f64(0)}).Omitted, omitted(0, 1), "omitted at depth 0")
		capped := snapOpts(mk("paused", "running"), SnapshotOptions{MaxRuns: f64(1)})
		eq(t, ids(capped.Runs), []string{"live-child"}, "capped runs")
		eq(t, capped.Omitted, omitted(1, 0), "capped omitted")
		queued := snapOpts(mk("complete", "queued"), SnapshotOptions{})
		eq(t, ids(queued.Runs), []string{"live-child", "paused-parent"}, "queued runs")
		eq(t, len(queued.Runs[1].Children), 0, "queued parent children")
		eq(t, queued.Omitted, omitted(0, 0), "queued omitted")
	})
	tw(t, f, "keeps cyclic workflow parent links visible as roots", func(t *testing.T) {
		snap := snapOpts([]jm{
			{"asyncId": "cycle-a", "asyncDir": "/tmp/a", "status": "complete", "mode": "workflow", "parentWorkflowRunId": "cycle-b"},
			{"asyncId": "cycle-b", "asyncDir": "/tmp/b", "status": "complete", "mode": "workflow", "parentWorkflowRunId": "cycle-a"}}, SnapshotOptions{})
		eq(t, ids(snap.Runs), []string{"cycle-a", "cycle-b"}, "runs")
		eq(t, snap.Omitted, omitted(0, 0), "omitted")
	})
	tw(t, f, "orders unmatched materialized children deterministically before applying child caps", func(t *testing.T) {
		parent := jm{"asyncId": "ordered-parent", "asyncDir": "/tmp/op", "status": "running", "mode": "workflow"}
		first := jm{"asyncId": "child-a", "asyncDir": "/tmp/ca", "parentWorkflowRunId": "ordered-parent", "status": "complete", "updatedAt": 10.0}
		second := jm{"asyncId": "child-b", "asyncDir": "/tmp/cb", "parentWorkflowRunId": "ordered-parent", "status": "complete", "updatedAt": 20.0}
		o := SnapshotOptions{MaxChildrenPerNode: f64(1)}
		forward := snapOpts([]jm{parent, first, second}, o)
		reversed := snapOpts([]jm{parent, second, first}, o)
		eq(t, ids(forward.Runs[0].Children), []string{"child-b"}, "forward")
		eq(t, ids(reversed.Runs[0].Children), []string{"child-b"}, "reversed")
		eq(t, forward.Omitted.Children, 1, "omitted children")
	})
	tw(t, f, "derives safe end times only for terminal steps", func(t *testing.T) {
		const maxSafe = 9007199254740991.0
		snap := snapAt([]jm{{"asyncId": "step-times", "asyncDir": "/tmp/st", "status": "running", "steps": []any{
			jm{"agent": "finished", "status": "completed", "startedAt": 1500.0, "durationMs": 2500.0},
			jm{"agent": "active", "status": "running", "startedAt": 1500.0, "durationMs": 2500.0},
			jm{"agent": "overflow", "status": "completed", "startedAt": maxSafe, "durationMs": maxSafe}}}}, 9000)
		c := snap.Runs[0].Children
		type eu struct{ Ended, Updated *int64 }
		eq(t, eu{c[0].EndedAt, c[0].UpdatedAt}, eu{i64(4000), i64(4000)}, "finished")
		eq(t, eu{c[1].EndedAt, c[1].UpdatedAt}, eu{nil, i64(1500)}, "active")
		eq(t, eu{c[2].EndedAt, c[2].UpdatedAt}, eu{nil, i64(int64(maxSafe))}, "overflow")
	})
}

func TestRetainedFleetJobsAreLimitedToTheMostRecentTerminalOnes(t *testing.T) {
	state := newSubagentState()
	for i := 0; i < 25; i++ {
		state.rememberFleetJob(&AsyncJobState{AsyncID: fmt.Sprintf("done-%02d", i), Status: "complete", UpdatedAt: int64(i)})
	}
	state.rememberFleetJob(&AsyncJobState{AsyncID: "live", Status: "running", UpdatedAt: 0})
	eq(t, len(state.FleetJobs), maxRecentFleetJobs+1, "retained jobs: the 20 newest terminal ones and the live one")
	if _, ok := state.FleetJobs["done-04"]; ok {
		t.Error("an old terminal job was kept")
	}
	if _, ok := state.FleetJobs["done-05"]; !ok {
		t.Error("the 20th newest terminal job was dropped")
	}
	if _, ok := state.FleetJobs["live"]; !ok {
		t.Error("a live job was dropped")
	}
}
