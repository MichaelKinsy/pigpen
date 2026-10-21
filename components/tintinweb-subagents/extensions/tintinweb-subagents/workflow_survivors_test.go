package tintinweb_subagents

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Coverage for the behaviors the second mutation run found untested: the worker's phase and log handling, the run
// controls, the tool's resume and flag paths, and the manager-level rules for an agent a workflow owns.

func lastRowByIndex(progress []workflowEntry) map[int]*workflowAgentEntry {
	out := map[int]*workflowAgentEntry{}
	for _, e := range wfAgentRows(progress) {
		out[e.Index] = e
	}
	return out
}

func TestWorkflowWorkerScopes(t *testing.T) {
	stub := func(o *workflowRunOptions) { o.Host = wfStubHost(nil) }
	t.Run("an agent's own phase option does not move the ambient phase", func(t *testing.T) {
		res := wfRun(t, "phase(\"A\");\nawait agent(\"x\", { phase: \"B\" });\nawait agent(\"y\");\nreturn 1;", stub)
		rows := lastRowByIndex(res.Progress)
		eq(t, rows[0].PhaseTitle, "B")
		eq(t, rows[1].PhaseTitle, "A")
	})
	t.Run("the phase that named an agent reaches the host ahead of its row", func(t *testing.T) {
		res := wfRun(t, "phase(\"A\");\nawait agent(\"x\");\nreturn 1;", stub)
		phaseAt, agentAt := -1, -1
		for i, e := range res.Progress {
			switch e.(type) {
			case *workflowPhaseEntry:
				if phaseAt == -1 {
					phaseAt = i
				}
			case *workflowAgentEntry:
				if agentAt == -1 {
					agentAt = i
				}
			}
		}
		eq(t, phaseAt >= 0 && phaseAt < agentAt, true)
	})
	t.Run("a nested workflow past the cap is fatal, not a null in parallel()", func(t *testing.T) {
		kid := wfHead + "return 1;\n"
		res := wfRun(t, "await workflow(\"k\");\nreturn await parallel([async () => workflow(\"k\")]);", func(o *workflowRunOptions) {
			o.Host = newNestingHost(map[string]string{"k": kid}, nil)
			o.NestedCap = 1
		})
		eq(t, res.Status, runFailed)
	})
	t.Run("a nested workflow's log lines carry its name", func(t *testing.T) {
		kid := "export const meta = { name: \"kid\", description: \"d\" };\nlog(\"hi\");\nreturn 1;\n"
		res := wfRun(t, "await workflow(\"k\");\nreturn 1;", func(o *workflowRunOptions) { o.Host = newNestingHost(map[string]string{"k": kid}, nil) })
		found := ""
		for _, e := range res.Progress {
			if l, ok := e.(*workflowLogEntry); ok && strings.HasSuffix(l.Message, "hi") {
				found = l.Message
			}
		}
		eq(t, found != "" && found != "hi" && strings.HasSuffix(found, ": hi"), true)
	})
	t.Run("a schema keeps its key order in the journal key", func(t *testing.T) {
		keyOf := func(schema string) string {
			rec := &wfRecorder{}
			wfRun(t, "await agent(\"p\", { schema: "+schema+" });\nreturn 1;", func(o *workflowRunOptions) { o.Host = wfStubHost(nil); o.Journal = rec.journal() })
			rec.mu.Lock()
			defer rec.mu.Unlock()
			return rec.entries[0].Key
		}
		typeFirst := keyOf(`{ type: "object", properties: { a: { type: "string" } } }`)
		typeLast := keyOf(`{ properties: { a: { type: "string" } }, type: "object" }`)
		eq(t, typeFirst != typeLast, true)
	})
}

func TestWorkflowRunBudgetAndControl(t *testing.T) {
	t.Run("a failed agent's output tokens are spent too", func(t *testing.T) {
		host := wfStubHost(func(*workflowSpawnRequest) workflowSpawnResult {
			return workflowSpawnResult{OK: false, Error: "died", OutputTokens: 5}
		})
		res := wfRun(t, "await agent(\"x\");\nreturn budget.spent();", func(o *workflowRunOptions) { o.Host = host })
		eq(t, res.Value, float64(5))
	})
	t.Run("an agent still waiting for the pause to lift cannot be retried", func(t *testing.T) {
		stub := newControllableHost()
		ctl := make(chan workflowControl, 1)
		done := make(chan *workflowRunResult, 1)
		go func() {
			done <- wfRun(t, "return await agent('one');", func(o *workflowRunOptions) {
				o.Host = stub
				o.OnControl = func(c workflowControl) { c.Pause(); ctl <- c }
			})
		}()
		control := <-ctl
		for i := 0; i < 60; i++ {
			eq(t, control.Retry(0), false)
			time.Sleep(5 * time.Millisecond)
		}
		control.Resume()
		wfUntil(t, func() bool { return len(stub.Started()) == 1 }, "the agent to start")
		stub.Finish("wf-agent-0", "ok")
		eq(t, (<-done).Value, "ok")
	})
	t.Run("the boundary refuses NaN", func(t *testing.T) {
		eq(t, walkWorkflowBoundary(reflect.ValueOf(math.NaN()), "args", map[uintptr]bool{}) != nil, true)
		eq(t, walkWorkflowBoundary(reflect.ValueOf(1.5), "args", map[uintptr]bool{}), nil)
	})
}

func TestWorkflowSmallPieces(t *testing.T) {
	t.Run("workflowId is ignored by the spawn RPC, not refused", func(t *testing.T) {
		eq(t, unportedSpawnOption(map[string]any{"workflowId": "x"}), "")
	})
	t.Run("a run's tokens are summed once", func(t *testing.T) {
		task := createWorkflowTask(workflowTaskInit{ID: "wf_a", StartTime: 1})
		hundred := 100
		updateWorkflowProgressBatch(task, wfEntries(wfAgentRow(0, func(e *workflowAgentEntry) { e.Tokens = &hundred })))
		eq(t, task.TotalTokens, 100)
	})
	t.Run("a skip that worked says nothing", func(t *testing.T) {
		task, _ := runningTask()
		var said []string
		skipAgentFromDialog(task, 0, func(message, level string) { said = append(said, message) })
		eq(t, len(said), 0)
	})
	t.Run("the tool's schema offers title and description", func(t *testing.T) {
		props := workflowToolParameters["properties"].(map[string]any)
		_, hasTitle := props["title"]
		eq(t, hasTitle, true)
	})
}

func TestWorkflowSettingsNoticeOnce(t *testing.T) {
	r := settingsRig(t, `{"schedulingEnabled": true}`)
	r.Fire("session_start", obj{"reason": "reload"})
	eq(t, len(notices(r)), 1)
}

func TestWorkflowToolPaths(t *testing.T) {
	t.Run("collisions are checked once per session", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		w.start()
		w.start()
		eq(t, len(w.CallsTo("getAllTools")), 1)
	})
	t.Run("the flag runs once and its result is not displayed", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{hasUI: true})
		path := w.dir + "/flow.js"
		writeFile(t, path, fileScript)
		w.flag = path
		w.start()
		w.start()
		wfUntil(t, func() bool {
			for _, m := range w.messages() {
				if msg, _ := m["message"].(map[string]any); msg != nil && msg["customType"] == "workflow-result" {
					return true
				}
			}
			return false
		}, "the flag run's result")
		eq(t, len(w.app.workflows().tasks.keys()), 1)
		for _, m := range w.messages() {
			if msg, _ := m["message"].(map[string]any); msg != nil && msg["customType"] == "workflow-result" {
				eq(t, msg["display"] == true, false)
			}
		}
	})
	t.Run("args present but null still count as given", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		id := w.taskID(w.run(obj{"script": inlineScript, "args": nil}))
		eq(t, w.app.workflows().tasks.get(id).HasArgs, true)
	})
	t.Run("resuming reuses the earlier run's script and says how much can replay", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		id := w.taskID(w.run(obj{"script": inlineScript + "return 1;\n"}))
		w.notification(id)
		text := w.run(obj{"resumeFromRunId": id})
		eq(t, strings.Contains(text, "Resuming "+id+": 0 recorded call(s) available to replay."), true)
	})
}

func TestWorkflowOwnedAgents(t *testing.T) {
	t.Run("an owned agent is not addressable by name or prefix", func(t *testing.T) {
		r := startRig(t)
		owned := r.app.mgr.spawnOwned("run-1", "general-purpose", "d", "worker", childSpec{Prompt: "p"}, false)
		eq(t, r.app.mgr.resolve("worker") == nil, true)
		eq(t, r.app.mgr.resolve(owned.ID[:6]) == nil, true)
	})
	t.Run("abortAll reaches an owned agent", func(t *testing.T) {
		r := startRig(t)
		r.app.mgr.spawnOwned("run-1", "general-purpose", "d", "", childSpec{Prompt: "p"}, false)
		child := r.fleet.wait(t, 1)[0]
		r.app.mgr.abortAll()
		wfUntil(t, func() bool { child.mu.Lock(); defer child.mu.Unlock(); return child.aborted }, "the owned child to be aborted")
	})
	t.Run("an owned agent is not announced on the bus, a top-level one is", func(t *testing.T) {
		r := startRig(t)
		owned := r.app.mgr.spawnOwned("run-1", "general-purpose", "d", "", childSpec{Prompt: "p"}, false)
		r.fleet.wait(t, 1)[0].finish("x")
		<-owned.done
		time.Sleep(100 * time.Millisecond)
		eq(t, r.emitted("subagents:completed"), false)
		top := r.app.mgr.spawn("general-purpose", "d", "", childSpec{Prompt: "p"}, false)
		r.fleet.wait(t, 2)[1].finish("y")
		<-top.done
		r.waitEmitted("subagents:completed")
	})
	t.Run("the RPC spawn keeps a name", func(t *testing.T) {
		r := startRig(t)
		r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "x", "options": obj{"name": "bob"}})
		eq(t, r.app.mgr.resolve("bob") != nil, true)
	})
}

func TestWorkflowAdapterOverTheManager(t *testing.T) {
	adapterOf := func(t *testing.T, r *rig) managerWorkflowAdapter {
		ctx, ok := r.app.context()
		if !ok {
			t.Fatal("no session context")
		}
		return managerWorkflowAdapter{a: r.app, ctx: ctx}
	}
	t.Run("the thinking level reaches the child, and the output tokens come back", func(t *testing.T) {
		r := startRig(t)
		adapter := adapterOf(t, r)
		got := make(chan workflowRecord, 1)
		go func() {
			rec, _ := adapter.SpawnAndWait("general-purpose", "p", workflowSpawnOptions{ThinkingLevel: "high"}, nil)
			got <- rec
		}()
		child := r.fleet.wait(t, 1)[0]
		eq(t, child.spec.Thinking, "high")
		child.res = &childResult{Text: "x", Tokens: 100, OutputTokens: 7}
		close(child.release)
		rec := <-got
		eq(t, [2]int{rec.LifetimeTokens, rec.OutputTokens}, [2]int{100, 7})
	})
	t.Run("cancelling the run aborts the child and returns", func(t *testing.T) {
		r := startRig(t)
		adapter := adapterOf(t, r)
		ctx, cancel := context.WithCancel(context.Background())
		returned := make(chan struct{})
		go func() {
			adapter.SpawnAndWait("general-purpose", "p", workflowSpawnOptions{Ctx: ctx}, nil)
			close(returned)
		}()
		child := r.fleet.wait(t, 1)[0]
		cancel()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatal("SpawnAndWait did not return after the run was cancelled")
		}
		child.mu.Lock()
		defer child.mu.Unlock()
		eq(t, child.aborted, true)
	})
}
