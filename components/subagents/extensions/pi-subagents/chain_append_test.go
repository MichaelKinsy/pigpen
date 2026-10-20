package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/unit/chain-append.test.ts.

func caWriteStatus(t *testing.T, asyncDir string, status string) {
	t.Helper()
	writeFile(t, filepath.Join(asyncDir, "status.json"), status)
}

func caRunnerStep(t *testing.T, agent string) *jsObject {
	return jobj(t, `{"agent":"`+agent+`","task":"Use {previous}","inheritProjectContext":false,"inheritSkills":false}`)
}

func caReadStatus(t *testing.T, asyncDir string) *jsObject {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(asyncDir, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func TestChainAppendTwins(t *testing.T) {
	const f = "chain-append"
	n64 := func(v int64) *int64 { return &v }
	tw(t, f, "persists append requests for running async chains and records pending status", func(t *testing.T) {
		asyncDir := filepath.Join(tmp(t), "run-a")
		caWriteStatus(t, asyncDir, `{"runId":"run-a","mode":"chain","state":"running","startedAt":100,"lastUpdate":100,"chainStepCount":1,"steps":[{"agent":"scout","status":"running"}]}`)
		result, err := EnqueueChainAppendRequest(asyncDir, "run-a", []*jsObject{caRunnerStep(t, "worker")}, n64(200), nil)
		eq(t, err, nil, "enqueue")
		eq(t, result.PendingCount, 1, "pendingCount")
		n, _ := CountPendingChainAppendRequests(asyncDir)
		eq(t, n, 1, "pending files")
		status := caReadStatus(t, asyncDir)
		eq(t, status.vals["pendingAppends"], float64(1), "pendingAppends")
		eq(t, status.vals["lastUpdate"], float64(200), "lastUpdate")
		log, _ := os.ReadFile(filepath.Join(asyncDir, "events.jsonl"))
		mustMatch(t, `subagent\.chain\.append\.requested`, string(log))
		consumed, err := ConsumeChainAppendRequests(asyncDir)
		eq(t, err, nil, "consume")
		eq(t, len(consumed), 1, "consumed")
		eq(t, consumed[0].ID, result.Request.ID, "id")
		n, _ = CountPendingChainAppendRequests(asyncDir)
		eq(t, n, 0, "pending after consume")
	})
	tw(t, f, "persists the request inside the admission callback", func(t *testing.T) {
		asyncDir := filepath.Join(tmp(t), "run-admission")
		caWriteStatus(t, asyncDir, `{"runId":"run-admission","mode":"chain","state":"running","startedAt":100,"steps":[{"agent":"scout","status":"running"}]}`)
		admitted := false
		result, err := EnqueueChainAppendRequest(asyncDir, "run-admission", []*jsObject{caRunnerStep(t, "worker")}, n64(200), func(persist func() error) error {
			n, _ := CountPendingChainAppendRequests(asyncDir)
			eq(t, n, 0, "nothing persisted before admission")
			if err := persist(); err != nil {
				return err
			}
			admitted = true
			return nil
		})
		eq(t, err, nil, "enqueue")
		eq(t, admitted, true, "admitted")
		eq(t, result.PendingCount, 1, "pendingCount")
	})
	tw(t, f, "reads pending append requests without consuming them and reports reserved output names", func(t *testing.T) {
		asyncDir := filepath.Join(tmp(t), "run-pending")
		caWriteStatus(t, asyncDir, `{"runId":"run-pending","mode":"chain","state":"running","startedAt":100,"steps":[{"agent":"scout","status":"running"}]}`)
		group := jo("parallel", []any{joOf(caRunnerStep(t, "worker"), "outputName", "draft"), joOf(caRunnerStep(t, "reviewer"), "outputName", "review")})
		_, err := EnqueueChainAppendRequest(asyncDir, "run-pending", []*jsObject{group}, n64(200), nil)
		eq(t, err, nil, "enqueue")
		pending, err := ReadPendingChainAppendRequests(asyncDir)
		eq(t, err, nil, "read")
		eq(t, len(pending), 1, "pending")
		eq(t, RunnerStepOutputNames(pending[0].Steps), []string{"draft", "review"}, "output names")
		n, _ := CountPendingChainAppendRequests(asyncDir)
		eq(t, n, 1, "still queued")
	})
	tw(t, f, "rejects terminal and non-chain async runs", func(t *testing.T) {
		root := tmp(t)
		complete := filepath.Join(root, "complete")
		caWriteStatus(t, complete, `{"runId":"complete","mode":"chain","state":"complete","startedAt":100,"steps":[{"agent":"scout","status":"complete"}]}`)
		_, err := EnqueueChainAppendRequest(complete, "complete", []*jsObject{caRunnerStep(t, "worker")}, nil, nil)
		rcFails(t, err, "only running chain runs")
		parallel := filepath.Join(root, "parallel")
		caWriteStatus(t, parallel, `{"runId":"parallel","mode":"parallel","state":"running","startedAt":100,"steps":[{"agent":"scout","status":"running"}]}`)
		_, err = EnqueueChainAppendRequest(parallel, "parallel", []*jsObject{caRunnerStep(t, "worker")}, nil, nil)
		rcFails(t, err, "only active chain runs")
		drained := filepath.Join(root, "drained")
		caWriteStatus(t, drained, `{"runId":"drained","mode":"chain","state":"running","startedAt":100,"steps":[{"agent":"scout","status":"complete"}]}`)
		_, err = EnqueueChainAppendRequest(drained, "drained", []*jsObject{caRunnerStep(t, "worker")}, nil, nil)
		rcFails(t, err, "no running or pending chain steps left")
	})
	tw(t, f, "extends async chain status, parallel groups, and workflow graph", func(t *testing.T) {
		status := jobj(t, `{"runId":"run-graph","mode":"chain","state":"running","startedAt":100,"currentStep":0,"chainStepCount":1,"steps":[{"agent":"scout","status":"complete"}],"parallelGroups":[],
			"workflowGraph":{"runId":"run-graph","mode":"chain","phases":[],"nodes":[{"id":"step-0","kind":"step","agent":"scout","label":"scout","status":"completed","flatIndex":0,"stepIndex":0}]}}`)
		appended := []*jsObject{caRunnerStep(t, "worker"), joOf(jo("parallel", []any{caRunnerStep(t, "reviewer"), caRunnerStep(t, "auditor")}), "concurrency", float64(2))}
		chain, flat := AppendRunnerStepsToStatus(status, appended, n64(300), 0)
		eq(t, [2]int{chain, flat}, [2]int{2, 3}, "added")
		eq(t, status.vals["chainStepCount"], float64(3), "chainStepCount")
		eq(t, status.vals["pendingAppends"], float64(0), "pendingAppends")
		eq(t, status.vals["lastUpdate"], float64(300), "lastUpdate")
		var rows, descriptions []string
		for i, s := range status.vals["steps"].([]any) {
			so := s.(*jsObject)
			rows = append(rows, so.vals["agent"].(string)+":"+so.vals["status"].(string))
			if i > 0 {
				descriptions = append(descriptions, so.vals["description"].(string))
			}
		}
		eq(t, rows, []string{"scout:complete", "worker:pending", "reviewer:pending", "auditor:pending"}, "steps")
		eq(t, descriptions, []string{promptRedacted, promptRedacted, promptRedacted}, "no raw task text in durable status")
		jsonEq(t, status.vals["parallelGroups"], `[{"start":2,"count":2,"stepIndex":2}]`)
		nodes := status.obj("workflowGraph").vals["nodes"].([]any)
		eq(t, nodes[1].(*jsObject).vals["id"], "step-1", "node id")
		eq(t, nodes[2].(*jsObject).vals["kind"], "parallel-group", "node kind")
		var flats []any
		for _, c := range nodes[2].(*jsObject).vals["children"].([]any) {
			flats = append(flats, c.(*jsObject).vals["flatIndex"])
		}
		eq(t, flats, []any{2.0, 3.0}, "child flat indexes")
	})
	tskip(t, f, "reports accepted appends and keeps admission claims when bookkeeping fails after persistence",
		"the case claims a run fan-out budget (claimRunFanoutBatchWithCommit, createRunFanoutBudget, getRunFanoutBudgetSnapshot), which is the run-policy slice's run-fanout-budget port; the bookkeeping behavior it checks is TestChainAppendBookkeepingFailure here, and the admission seam it uses is the 'persists the request inside the admission callback' twin")
}

// A request persisted by the admission callback stays accepted when the status and events writes fail afterwards.
func TestChainAppendBookkeepingFailure(t *testing.T) {
	asyncDir := filepath.Join(tmp(t), "run-bookkeeping")
	caWriteStatus(t, asyncDir, `{"runId":"run-bookkeeping","mode":"chain","state":"running","startedAt":100,"steps":[{"agent":"scout","status":"running"}]}`)
	statusPath := filepath.Join(asyncDir, "status.json")
	if err := os.MkdirAll(filepath.Join(asyncDir, "events.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := int64(200)
	committed := false
	result, err := EnqueueChainAppendRequest(asyncDir, "run-bookkeeping", []*jsObject{caRunnerStep(t, "worker")}, &now, func(persist func() error) error {
		if err := persist(); err != nil {
			return err
		}
		committed = true
		if err := os.Remove(statusPath); err != nil {
			return err
		}
		return os.MkdirAll(statusPath, 0o755)
	})
	eq(t, err, nil, "enqueue")
	eq(t, committed, true, "commit ran")
	eq(t, result.PendingCount, 1, "pendingCount")
	if !strings.Contains(result.BookkeepingError, "status update failed") || !strings.Contains(result.BookkeepingError, "event append failed") {
		t.Errorf("bookkeepingError = %q", result.BookkeepingError)
	}
	pending, _ := ReadPendingChainAppendRequests(asyncDir)
	eq(t, len(pending), 1, "the request is still queued")
}
