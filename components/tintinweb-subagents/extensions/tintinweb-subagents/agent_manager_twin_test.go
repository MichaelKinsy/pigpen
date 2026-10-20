package tintinweb_subagents

import (
	"testing"
	"time"
)

// Twins of the resume, background-resume and session-file cases of test/agent-manager.test.ts and
// test/background-resume-wiring.test.ts. The original mocks runAgent and resumeAgent; the Go form drives the manager
// with a fleet of scripted children.

func TestAgentManagerResumeTwins(t *testing.T) {
	const f = "agent-manager"
	// settled spawns a background agent, lets it finish and returns the manager, the fleet and the record.
	settled := func(t *testing.T, onSettle func(*agentRecord), limit int) (*manager, *fleet, *agentRecord, *fakeChild) {
		t.Helper()
		fl := &fleet{}
		m := newManager(fl.start, onSettle)
		if limit > 0 {
			m.maxConcurrent = limit
		}
		rec := m.spawn("general-purpose", "task", "", childSpec{Prompt: "task"}, true)
		c := fl.wait(t, 1)[0]
		c.finish("first")
		<-m.doneOf(rec)
		return m, fl, rec, c
	}
	status := func(m *manager, r *agentRecord) string {
		m.mu.Lock()
		defer m.mu.Unlock()
		return r.Status
	}
	waitFor := func(t *testing.T, cond func() bool) {
		t.Helper()
		for i := 0; i < 1000; i++ {
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("condition never held")
	}
	resumed := func(c *fakeChild) int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) }

	tw(t, f, "resume(): a failed final turn on the resumed prompt maps to error too", func(t *testing.T) {
		m, _, rec, c := settled(t, nil, 0)
		eq(t, status(m, rec), statusCompleted)
		eq(t, m.resume(rec, "more", false), true)
		waitFor(t, func() bool { return resumed(c) == 1 })
		// resumeAgent bounds its fallback to this invocation, so a failed empty resume yields text "": never the
		// prior turn's answer (#144).
		c.mu.Lock()
		c.res = &childResult{Text: "", Failure: "retries exhausted on resume"}
		c.mu.Unlock()
		c.releaseNow()
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusError)
		eq(t, rec.Error, "retries exhausted on resume")
		eq(t, rec.Result, "") // no stale prior answer
	})
	tw(t, f, "resume(): partial text produced before the failure is kept as result", func(t *testing.T) {
		m, _, rec, c := settled(t, nil, 0)
		eq(t, m.resume(rec, "more", false), true)
		waitFor(t, func() bool { return resumed(c) == 1 })
		c.mu.Lock()
		c.res = &childResult{Text: "new partial progress", Failure: "provider died mid-turn"}
		c.mu.Unlock()
		c.releaseNow()
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusError)
		eq(t, rec.Result, "new partial progress") // salvageable, this-run text
	})
	tw(t, f, "returns immediately with a running record + promise, then settles and fires onComplete", func(t *testing.T) {
		completions := make(chan string, 4)
		m, _, rec, c := settled(t, func(r *agentRecord) { completions <- r.ID }, 0)
		<-completions // the spawn's own completion
		eq(t, m.resume(rec, "keep going", true), true)
		// Returned at once: still running, no notification yet.
		eq(t, status(m, rec), statusRunning)
		select {
		case <-completions:
			t.Fatal("notified before the run ended")
		case <-time.After(50 * time.Millisecond):
		}
		waitFor(t, func() bool { return resumed(c) == 1 })
		c.finish("second")
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusCompleted)
		eq(t, rec.Result, "second")
		select {
		case <-completions:
		case <-time.After(5 * time.Second):
			t.Fatal("no completion for the resumed run")
		}
	})
	tw(t, f, "a failed final turn on a background resume maps to error and still notifies", func(t *testing.T) {
		completions := make(chan string, 4)
		m, _, rec, c := settled(t, func(r *agentRecord) { completions <- r.ID }, 0)
		<-completions
		eq(t, m.resume(rec, "again", true), true)
		waitFor(t, func() bool { return resumed(c) == 1 })
		c.mu.Lock()
		c.res = &childResult{Text: "partial", Failure: "provider exploded"}
		c.mu.Unlock()
		c.releaseNow()
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusError)
		eq(t, rec.Error, "provider exploded")
		eq(t, rec.Result, "partial") // #144: keep this-run text
		select {
		case <-completions:
		case <-time.After(5 * time.Second):
			t.Fatal("not notified")
		}
	})
	tskip(t, f, "forwards activity/usage callbacks to the resumed run", "the original forwards the session's tool-activity and usage callbacks into the record; usage reporting and per-run activity are a separate slice (usage.ts) and a child process reports only its totals")
	tw(t, f, "queues a background resume when the concurrency pool is full", func(t *testing.T) {
		m, fl, rec, c := settled(t, nil, 1)
		// Occupy the single slot with a spawn that never settles.
		blocker := m.spawn("general-purpose", "blocker", "", childSpec{Prompt: "blocker"}, true)
		fl.wait(t, 2)
		eq(t, status(m, blocker), statusRunning)
		eq(t, m.resume(rec, "later", true), true)
		eq(t, status(m, rec), statusQueued)
		eq(t, resumed(c), 0)
	})
	tw(t, f, "foreground resume is unchanged: awaits inline and does not fire onComplete", func(t *testing.T) {
		completions := make(chan string, 4)
		m, _, rec, c := settled(t, func(r *agentRecord) { completions <- r.ID }, 0)
		<-completions
		eq(t, m.resume(rec, "sync", false), true)
		waitFor(t, func() bool { return resumed(c) == 1 })
		c.finish("inline result")
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusCompleted)
		eq(t, rec.Result, "inline result")
		select {
		case <-completions:
			t.Fatal("a foreground resume must not announce itself")
		case <-time.After(100 * time.Millisecond):
		}
	})
	tw(t, f, "refuses to background-resume an agent whose run is still in flight", func(t *testing.T) {
		m, _, rec, c := settled(t, nil, 0)
		eq(t, m.resume(rec, "go", true), true)
		eq(t, status(m, rec), statusRunning)
		waitFor(t, func() bool { return resumed(c) == 1 })
		eq(t, m.resume(rec, "go again", true), false)
		eq(t, resumed(c), 1)                 // the second prompt never reached the child
		eq(t, status(m, rec), statusRunning) // the in-flight run is untouched
		eq(t, m.abort(rec), true)            // and still stoppable
		<-m.doneOf(rec)
		eq(t, status(m, rec), statusStopped)
	})
	tw(t, f, "refuses to background-resume an agent that is still queued", func(t *testing.T) {
		m, fl, rec, c := settled(t, nil, 1)
		m.spawn("general-purpose", "blocker", "", childSpec{Prompt: "blocker"}, true)
		fl.wait(t, 2)
		eq(t, m.resume(rec, "later", true), true)
		eq(t, status(m, rec), statusQueued)
		eq(t, m.resume(rec, "later again", true), false)
		eq(t, resumed(c), 0)
	})
	tskip(t, f, "fires onStarted when the run starts, not when a queued resume is registered", "onStarted is the hook the Agent tool hangs output-file streaming on; output transcripts are a separate slice and refused here (output_transcript)")
	tskip(t, f, "never fires onStarted for a queued resume that is stopped before it drains", "onStarted is the output-file streaming hook (see the previous case); the stop of a queued resume itself is TestAQueuedResumeCanBeStopped")
	tw(t, f, "captures the session file so the agent can be resumed after eviction", func(t *testing.T) {
		fl := &fleet{session: "/sessions/explore.jsonl"}
		m := newManager(fl.start, nil)
		rec := m.spawn("Explore", "go", "child", childSpec{Prompt: "go"}, true)
		fl.wait(t, 1)[0].finish("done")
		<-m.doneOf(rec)
		eq(t, rec.SessionFile, "/sessions/explore.jsonl")
	})
	tw(t, f, "records no session file for an in-memory session", func(t *testing.T) {
		fl := &fleet{}
		m := newManager(fl.start, nil)
		rec := m.spawn("Explore", "go", "child", childSpec{Prompt: "go"}, true)
		fl.wait(t, 1)[0].finish("done")
		<-m.doneOf(rec)
		eq(t, rec.SessionFile, "")
	})
	tskip(t, f, "tells the runner which spawns are nested, so only top-level ones persist", "a spawn is nested only through nested delegation (allowed_subagents), which this port refuses; the persistence decision it feeds is tested in the agent-runner twins (leaves a nested child in memory)")
	tskip(t, f, "aborts children spawned during a resumed turn", "children spawned by an agent are nested delegation, which this port refuses")
	tskip(t, f, "resume() also accumulates usage and increments compactions on the same record", "usage and compaction counters belong to usage reporting (usage.ts), a separate slice; the child reports session totals only")
}

func TestBackgroundResumeWiringTwins(t *testing.T) {
	const f = "background-resume-wiring"
	tw(t, f, "does not tie the detached run to the tool-call signal", func(t *testing.T) {
		r := startRig(t)
		c, id := finished(t, r, "general-purpose")
		r.must("Agent", obj{"prompt": "keep going", "description": "Keep going", "subagent_type": "general-purpose", "resume": id, "run_in_background": true})
		// The tool call has returned and its signal is gone; the detached run goes on, and a stop is the user's.
		time.Sleep(100 * time.Millisecond)
		c.mu.Lock()
		eq(t, c.aborted, false)
		c.mu.Unlock()
		waitStatus(t, r, id, statusRunning)
		c.finish("resumed answer")
		waitStatus(t, r, id, statusCompleted)
	})
	tskip(t, f, "appends to the agent's existing transcript instead of truncating it", "the .output transcript file is a separate slice (output-file.ts) and an agent file's output_transcript is refused here")
	tw(t, f, "reports the record's own type, not the caller's ignored subagent_type", func(t *testing.T) {
		r := sessionRig(t, "", "")
		write(t, r.app.cwd+"/.pi/agents/explorer.md", "---\ndescription: Explorer agent\n---\n\nExplore.")
		r.app.reload(r.app.cwd)
		_, id := finished(t, r, "general-purpose")
		created := make(chan map[string]any, 4)
		r.onBus("subagents:created", func(d any) { m, _ := d.(map[string]any); created <- m })
		text := r.must("Agent", obj{"prompt": "keep going", "description": "Different description", "subagent_type": "explorer", "resume": id, "run_in_background": true})
		select {
		case p := <-created:
			eq(t, p["type"], "general-purpose")
			eq(t, p["id"], id)
		case <-time.After(5 * time.Second):
			t.Fatal("no subagents:created")
		}
		eq(t, contains(text, "Type: general-purpose"), true)
	})
	tw(t, f, "refuses a second background resume while the first run is in flight", func(t *testing.T) {
		r := startRig(t)
		c, id := finished(t, r, "general-purpose")
		params := obj{"prompt": "keep going", "description": "Keep going", "subagent_type": "general-purpose", "resume": id, "run_in_background": true}
		r.must("Agent", params)
		second := r.must("Agent", params)
		eq(t, resumed(c), 1)
		eq(t, contains(second, "still running"), true)
		eq(t, contains(second, "steer_subagent"), true)
	})
	tw(t, f, "still resumes in the foreground when run_in_background is false", func(t *testing.T) {
		r := startRig(t)
		c, id := finished(t, r, "general-purpose")
		wait := foreground(r, "Agent", obj{"prompt": "keep going", "description": "Keep going", "subagent_type": "general-purpose", "resume": id, "run_in_background": false})
		for i := 0; i < 500 && resumed(c) == 0; i++ {
			time.Sleep(5 * time.Millisecond)
		}
		c.finish("inline answer")
		text, _ := wait()
		// Foreground resume returns the answer inline: no background handoff text.
		eq(t, contains(text, "inline answer"), true)
		eq(t, contains(text, "You will be notified"), false)
	})
}

func resumed(c *fakeChild) int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.resumed) }
