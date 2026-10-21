package tintinweb_subagents

import (
	"strings"
	"testing"
)

// Twins of subagent-error-status-e2e.test.ts (a real child process through the fake pig), agent-startup-error.test.ts
// and status-note-wiring.test.ts.

const fatalProviderError = "invalid request: provider rejected the prompt"

func TestErrorStatusTwins(t *testing.T) {
	run := func(t *testing.T, mode string) string {
		t.Helper()
		useFakePig(t, mode)
		r := startObsRig(t, obsWorkspace(t, "", ""), startRPCChild)
		return r.must("Agent", obj{"prompt": "Do work.", "description": "doomed", "subagent_type": "general-purpose", "run_in_background": false})
	}
	tw(t, "subagent-error-status-e2e", "a run whose ONLY turn errors with no output is a failure, not an empty success", func(t *testing.T) {
		out := run(t, "fatal-only")
		eq(t, strings.Contains(out, fatalProviderError), true)
		eq(t, strings.Contains(out, "No output."), false)
	})
	tw(t, "subagent-error-status-e2e", "an earlier turn's text must not mask a failed final turn as a fresh success", func(t *testing.T) {
		out := run(t, "fatal-masked")
		eq(t, strings.Contains(out, fatalProviderError), true)
		eq(t, strings.Contains(out, "Partial output before the failure:"), true)
		eq(t, strings.Contains(out, "EARLIER-PARTIAL-TEXT"), true)
		eq(t, strings.Index(out, fatalProviderError) < strings.Index(out, "EARLIER-PARTIAL-TEXT"), true) // the headline comes first
	})
	tw(t, "subagent-error-status-e2e", "a pure empty-error run shows no 'partial output' section", func(t *testing.T) {
		out := run(t, "fatal-only")
		eq(t, strings.Contains(out, fatalProviderError), true)
		eq(t, strings.Contains(out, "Partial output before the failure:"), false)
	})
}

// The original's throwing case is a worktree that cannot be created; here the spawn that never starts is one the
// port refuses before any agent exists. Either way the tool call fails instead of returning a diagnostic.
func TestStartupErrorTwin(t *testing.T) {
	tw(t, "agent-startup-error", "rejects instead of returning the diagnostic (run_in_background: ${background})", func(t *testing.T) {
		for _, background := range []bool{false, true} {
			r := startObsRig(t, obsWorkspace(t, "", ""))
			text, failure := r.tool("Agent", obj{"prompt": "do it", "description": "worktree probe", "subagent_type": "general-purpose", "isolation": "worktree", "run_in_background": background})
			eq(t, text, "")
			eq(t, strings.Contains(failure, "worktree isolation"), true)
			eq(t, r.fleet.count(), 0)
		}
	})
}

func fgAgent(t *testing.T, r *rig, run func(c *fakeChild)) string {
	t.Helper()
	n := r.fleet.count()
	got := make(chan string, 1)
	go func() {
		text, _ := r.tool("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": false})
		got <- text
	}()
	run(r.fleet.wait(t, n+1)[n])
	return <-got
}

func TestStatusNoteWiringObservabilityTwins(t *testing.T) {
	tw(t, "status-note-wiring", "foreground turn-limit abort → the Agent result flags an incomplete outcome", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		out := fgAgent(t, r, func(c *fakeChild) { c.finishWith(&childResult{Text: "partial work so far", Aborted: true}) })
		eq(t, strings.Contains(out, "aborted at the turn limit"), true)
		eq(t, strings.Contains(out, "partial work so far"), true)
		eq(t, strings.Contains(out, "STOPPED BY THE USER"), false)
		eq(t, strings.Contains(out, "everything the agent produced is above"), true)
		eq(t, strings.Contains(out, "the task is unfinished"), true)
		eq(t, strings.Contains(out, "re-spawn") || strings.Contains(out, "get_subagent_result"), false)
	})
	tw(t, "status-note-wiring", "foreground user-stop → tells the parent NOT to restart it unasked", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		out := fgAgent(t, r, func(c *fakeChild) {
			for _, rec := range r.app.mgr.list() {
				r.app.mgr.abort(rec) // the user stops it, the path the stop key and the stop RPC take
			}
		})
		eq(t, strings.Contains(out, "STOPPED BY THE USER"), true)
		eq(t, strings.Contains(out, "everything the agent produced is above"), true)
		eq(t, strings.Contains(out, "the task is unfinished"), true)
		eq(t, strings.Contains(out, "re-spawn") || strings.Contains(out, "ask before"), false)
	})
	tw(t, "status-note-wiring", "background user-stop → get_subagent_result flags STOPPED BY THE USER (not completed)", func(t *testing.T) {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
		r.fleet.wait(t, 1)
		r.request("subagents:rpc:stop", obj{"agentId": id})
		<-r.app.mgr.get(id).done
		out := r.must("get_subagent_result", obj{"agent_id": id})
		eq(t, strings.Contains(out, "STOPPED BY THE USER"), true)
		eq(t, strings.Contains(out, "the task was NOT finished"), true)
		eq(t, strings.Contains(out, "Done"), false)
		eq(t, strings.Contains(out, "everything the agent produced is above"), false)
	})
	compacting := func(t *testing.T, compactions ...[2]any) []obj {
		r := startObsRig(t, obsWorkspace(t, "", ""))
		compacted := r.collect("subagents:compacted")
		out := fgAgent(t, r, func(c *fakeChild) {
			for _, k := range compactions {
				c.spec.Hooks.Compaction(k[0].(string), k[1].(int))
			}
			c.finishWith(&childResult{Text: "done"})
		})
		_ = out
		return compacted.wait(t, len(compactions))
	}
	tw(t, "status-note-wiring", "emits the documented payload when a top-level agent's session compacts", func(t *testing.T) {
		got := compacting(t, [2]any{"threshold", 12345})[0]
		eq(t, got["type"], "general-purpose")
		eq(t, got["description"], "d")
		eq(t, got["reason"], "threshold")
		eq(t, got["tokensBefore"], 12345.0)
		eq(t, got["compactionCount"], 1.0)
		eq(t, got["id"] != nil && got["id"] != "", true)
	})
	tw(t, "status-note-wiring", "counts repeated compactions on the same agent", func(t *testing.T) {
		var counts []any
		for _, e := range compacting(t, [2]any{"overflow", 1}, [2]any{"overflow", 2}) {
			counts = append(counts, e["compactionCount"])
		}
		// (the test host delivers each emit on a goroutine of its own, so the arrival order is not the emit order)
		if counts[0].(float64) > counts[1].(float64) {
			counts[0], counts[1] = counts[1], counts[0]
		}
		eq(t, counts, []any{1.0, 2.0})
	})
	tskip(t, "status-note-wiring", "stays silent for a nested child, like every other lifecycle event", "nested delegation is refused by this port, so no nested child exists")
}

// subagents:started: once per agent that starts running, immediately or when it leaves the queue.
func TestStartedEvent(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, `{"maxConcurrent": 1}`, ""))
	started := r.collect("subagents:started")
	a := idFrom(t, r.must("Agent", obj{"prompt": "a", "description": "first", "subagent_type": "general-purpose", "run_in_background": true}))
	b := idFrom(t, r.must("Agent", obj{"prompt": "b", "description": "second", "subagent_type": "Plan", "run_in_background": true}))
	got := started.wait(t, 1)
	eq(t, got[0], obj{"id": a, "type": "general-purpose", "description": "first"})
	eq(t, len(started.all()), 1) // the second is queued: it has not started
	r.fleet.wait(t, 1)[0].finishWith(&childResult{Text: "x"})
	got = started.wait(t, 2)
	eq(t, got[1], obj{"id": b, "type": "Plan", "description": "second"})
}
