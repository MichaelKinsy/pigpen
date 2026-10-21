package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a running agent's tools say (the steer text, the live conversation), the usage wrapper on steer, and which
// spawns write a transcript.

func runningAgent(t *testing.T, r *rig) (string, *fakeChild) {
	t.Helper()
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	return id, r.fleet.wait(t, n+1)[n]
}

func TestSteerTextOfARunningAgent(t *testing.T) {
	// upstream: index.ts steer_subagent: tokens, cost under showCost, tool uses, context, compactions.
	t.Run("the cost is part of the state only under showCost", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, `{"showCost": true}`, ""))
		id, c := runningAgent(t, r)
		c.spend(lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0123})
		eq(t, strings.HasSuffix(r.must("steer_subagent", obj{"agent_id": id, "message": "m"}), "Current state: 1.2k token · ~$0.0123 · 0 tool uses"), true)

		r = startObsRig(t, obsWorkspace(t, "", ""))
		id, c = runningAgent(t, r)
		c.spend(lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0123})
		eq(t, strings.HasSuffix(r.must("steer_subagent", obj{"agent_id": id, "message": "m"}), "Current state: 1.2k token · 0 tool uses"), true)
	})
	t.Run("one tool use, the live context percent and one compaction are singular", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		id, c := runningAgent(t, r)
		pct := 7.5
		c.mu.Lock()
		c.livePercent = &pct
		c.mu.Unlock()
		c.spec.Hooks.Compaction("overflow", 9)
		rec := r.app.mgr.get(id)
		r.app.mgr.mu.Lock()
		rec.ToolUses = 1
		r.app.mgr.mu.Unlock()
		eq(t, strings.HasSuffix(r.must("steer_subagent", obj{"agent_id": id, "message": "m"}), "Current state: 1 tool use · context 8% full · 1 compaction"), true)
	})
	t.Run("the result of a steer carries the spend the parent has not been told about", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, `{"reportUsage": true}`, ""))
		id, c := runningAgent(t, r)
		c.spend(lifetimeUsage{Input: 100, Output: 50})
		res, failure := r.toolRaw("steer_subagent", obj{"agent_id": id, "message": "m"}, "call-steer")
		eq(t, failure, "")
		usage, _ := res["usage"].(map[string]any)
		eq(t, usage["totalTokens"], 150.0)
	})
}

func TestTheLiveConversationOfARunningAgent(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	id, c := runningAgent(t, r)
	c.mu.Lock()
	c.liveMessages = []map[string]any{user("go"), assistant("working")}
	c.mu.Unlock()
	out := r.must("get_subagent_result", obj{"agent_id": id, "verbose": true, "wait": false})
	eq(t, strings.HasSuffix(out, "--- Agent Conversation ---\n[User]: go\n\n[Assistant]: working"), true)
}

func TestWhichSpawnsWriteATranscript(t *testing.T) {
	t.Run("the transcript lands under the session directory", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		id, _ := runningAgent(t, r)
		rec := r.app.mgr.get(id)
		eq(t, filepath.Base(filepath.Dir(filepath.Dir(rec.OutputFile))), "session-1")
	})
	t.Run("a spawn over the cross-extension bus writes none, as in the original", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "do stuff"})
		eq(t, reply["success"], true)
		r.fleet.wait(t, 1)
		rec := r.app.mgr.get(reply["data"].(map[string]any)["id"].(string))
		eq(t, rec.OutputFile, "")
	})
}

func TestOnlyAnAssistantMessageSpends(t *testing.T) {
	useFakePig(t, "usage-nonassistant")
	var spent []lifetimeUsage
	c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Hooks: &childHooks{Usage: func(u lifetimeUsage) { spent = append(spent, u) }}})
	eq(t, err, nil)
	_, err = c.wait()
	eq(t, err, nil)
	eq(t, spent, []lifetimeUsage{{Input: 7, Output: 3}})
}

func TestContextPercentOfNullIsUnavailable(t *testing.T) {
	// A just-compacted session reports `percent: null`: unavailable, not 0%.
	eq(t, contextPercentOf(obj{"contextUsage": obj{"percent": nil}}) == nil, true)
	eq(t, contextPercentOf(obj{"contextUsage": obj{}}) == nil, true)
	eq(t, contextPercentOf(obj{}) == nil, true)
	eq(t, contextPercentOf(nil) == nil, true)
	p := contextPercentOf(obj{"contextUsage": obj{"percent": 0.0}})
	eq(t, p != nil && *p == 0, true)
}

func TestStructuredRetryNeedsNothingValidatedAndARunThatWasNotCutShort(t *testing.T) {
	// upstream: agent-runner.ts:1120.
	eq(t, structuredRetryWanted(true, false, false), true)
	eq(t, structuredRetryWanted(false, false, false), false) // something validated
	eq(t, structuredRetryWanted(true, true, false), false)   // aborted at the turn limit
	eq(t, structuredRetryWanted(true, false, true), false)   // stopped
}

func TestTheRefusalTextIsLiteral(t *testing.T) {
	// Without reading a file: a copy of this directory alone (a mutation run) has no package.json next to it.
	eq(t, originalSpec, "npm:@tintinweb/pi-subagents@0.19.0")
	eq(t, refusalText("x"), "@pi-in-go/pigpen-tintinweb-subagents (Go port) does not support x yet. Install the original extension, npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.")
}

func TestTheOutputFileIsEscapedInTheNotification(t *testing.T) {
	r := &agentRecord{ID: "a", Status: statusCompleted, OutputFile: "/tmp/a&b<c>.output"}
	got := formatTaskNotification(r, 500, false)
	eq(t, strings.Contains(got, "<output-file>/tmp/a&amp;b&lt;c&gt;.output</output-file>"), true)
}

func TestUsageOfAnAgentAddsUp(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	id, c := runningAgent(t, r)
	c.spend(lifetimeUsage{Input: 100, Output: 50, Cost: 0.01})
	c.spend(lifetimeUsage{Input: 200, Output: 80, CacheRead: 7, Cost: 0.02})
	rec := r.app.mgr.get(id)
	r.app.mgr.mu.Lock()
	got := rec.Usage
	r.app.mgr.mu.Unlock()
	eq(t, got.Input, 300)
	eq(t, got.Output, 130)
	eq(t, got.CacheRead, 7)
	eq(t, got.Cost > 0.0299 && got.Cost < 0.0301, true)
}

func TestAnAgentLeavingTheQueueByAbortLetsGoOfItsTranscript(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	_, _ = runningAgent(t, r)
	queued := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	rec := r.app.mgr.get(queued)
	r.app.mgr.mu.Lock()
	status, held := rec.Status, rec.transcript != nil
	r.app.mgr.mu.Unlock()
	eq(t, status, statusQueued)
	eq(t, held, true)
	eq(t, r.app.mgr.abort(rec), true)
	r.app.mgr.mu.Lock()
	held = rec.transcript != nil
	r.app.mgr.mu.Unlock()
	eq(t, held, false)
}

func TestTheBackgroundAgentResultNamesTheOutputFile(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	n := r.fleet.count()
	out := r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true})
	r.fleet.wait(t, n+1)
	rec := r.app.mgr.get(idFrom(t, out))
	eq(t, strings.Contains(out, "Output file: "+rec.OutputFile+"\n"), true)
}

func TestAResultConsumedOverTheBusIsNotAnnounced(t *testing.T) {
	// subagents:rpc:consume marks the result as handed over without stopping the held notification's timer: the
	// notification itself must see the mark and stay silent.
	r := startRig(t)
	reply := r.request("subagents:rpc:spawn", obj{"type": "general-purpose", "prompt": "p"})
	c := r.fleet.wait(t, 1)[0]
	c.finish("done")
	r.waitEmitted("subagents:completed")
	eq(t, r.request("subagents:rpc:consume", obj{"agentId": reply["data"].(map[string]any)["id"]}), obj{"success": true})
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.app.mu.Lock()
		pending := len(r.app.timers)
		r.app.mu.Unlock()
		if pending == 0 {
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	eq(t, len(r.messages()), 0)
}

func TestReadingAFinishedResultMarksItConsumed(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	id, c := runningAgent(t, r)
	c.finish("done")
	rec := r.app.mgr.get(id)
	<-rec.done
	r.app.mgr.mu.Lock()
	before := rec.Consumed
	r.app.mgr.mu.Unlock()
	eq(t, before, false)
	r.must("get_subagent_result", obj{"agent_id": id})
	r.app.mgr.mu.Lock()
	after := rec.Consumed
	r.app.mgr.mu.Unlock()
	eq(t, after, true)
}

func TestTheSettingsNoticeIsShownOncePerText(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, `{"scopeModels": true}`, ""))
	eq(t, len(notices(r)), 1)
	r.Fire("session_start", obj{"reason": "reload"})
	r.Fire("session_start", obj{"reason": "reload"})
	eq(t, len(notices(r)), 1)
}

func TestUsageIsPooledOnlyWhileReportingIsOn(t *testing.T) {
	t.Run("off: nothing is pooled and nothing is attached", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		id, c := runningAgent(t, r)
		c.spend(lifetimeUsage{Input: 100, Output: 50})
		eq(t, r.app.obs.pool.drain() == nil, true)
		r.app.obs.pool.add(lifetimeUsage{Input: 5}) // spend that predates the switch-off
		res, failure := r.toolRaw("steer_subagent", obj{"agent_id": id, "message": "m"}, "call-1")
		eq(t, failure, "")
		_, attached := res["usage"]
		eq(t, attached, false)
	})
	t.Run("switching it off drops what had accumulated", func(t *testing.T) {
		cwd := obsWorkspace(t, `{"reportUsage": true}`, "")
		r := startObsRig(t, cwd)
		_, c := runningAgent(t, r)
		c.spend(lifetimeUsage{Input: 100, Output: 50})
		must(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"reportUsage": false}`), 0o644))
		r.app.reloadObs(cwd)
		eq(t, r.app.obs.pool.drain() == nil, true)
	})
}
