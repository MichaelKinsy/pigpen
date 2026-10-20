package pi_goal_x

// Extra tests (not twins) that pin behavior of core.go, goal_scheduler.go and the runtime/accounting/prompt modules where the
// mutation run found no assertion. Every expectation is read from the original's TypeScript (goal-scheduler.ts, goal-state.ts,
// goal-accounting.ts, goal-runtime.ts, prompts/goal-prompts.ts, goal-compaction.ts, goal-live-retention.ts, goal-prompt-cache.ts).

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func mxaS(s string) *string { return &s }

// mxaPatch applies f to the focused goal through the service (the disk file and the revision move with it).
func mxaPatch(t *testing.T, h *schedFx, f func(g *jsObject)) {
	t.Helper()
	res, err := h.core.goalService.apply(h.core.st, goalMutationSpec{mutate: func(g *jsObject) *jsObject { f(g); return g }})
	if err != nil || !res.ok {
		t.Fatalf("patch: %v %s", err, res.message)
	}
}

// mxaSch patches the goal's scheduler (created for the fixture's session when the goal has none).
func mxaSch(t *testing.T, h *schedFx, f func(sch *jsObject)) {
	t.Helper()
	mxaPatch(t, h, func(g *jsObject) {
		sch := g.obj("scheduler")
		if sch == nil || !g.has("scheduler") {
			sch = newGoalScheduler(h.h.sessionID)
			g.set("scheduler", sch)
		}
		f(sch)
	})
}

func mxaHasNotice(h *schedFx, want string) bool {
	for _, n := range h.h.notices {
		if n == want {
			return true
		}
	}
	return false
}

func mxaLedger(h *schedFx) []*jsObject {
	invalidateGoalLedgerCache()
	return readGoalLedger(storage{cwd: h.cwd}).events
}

func mxaLedgerType(h *schedFx, typ string) []*jsObject {
	var out []*jsObject
	for _, e := range mxaLedger(h) {
		if gstr(e, "type") == typ {
			out = append(out, e)
		}
	}
	return out
}

// claimed runs the fixture to a claimed dispatch: ready declared, settled, the dispatch timer fired.
func mxaClaimed(t *testing.T, h *schedFx) kernelSent {
	t.Helper()
	h.mock()
	h.begin()
	h.ready()
	h.settled()
	h.tick(1)
	if len(h.h.sent) != 1 || h.phase() != "claimed" {
		t.Fatalf("not claimed: sent=%d phase=%s", len(h.h.sent), h.phase())
	}
	return h.h.sent[0]
}

func mxaCheckpoint(s kernelSent, edit func(d *jsObject)) map[string]any {
	d := s.details.clone()
	if edit != nil {
		edit(d)
	}
	return map[string]any{"customType": s.customType, "content": s.content, "display": s.display, "details": d, "role": "custom"}
}

func mxaGoalID(h *schedFx) string { return gstr(h.goal(), "id") }

func TestMutantsASchedulerOwnerAndState(t *testing.T) {
	t.Run("an empty session id owns as unknown-session", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.h.sessionID = ""
		eq(t, h.core.scheduler.owner(), "unknown-session", "owner")
		eq(t, h.core.scheduler.resume(), true, "resume")
		owner, _ := h.sch().str("owner")
		eq(t, owner, "unknown-session", "the scheduler is stored with the fallback owner")
	})

	t.Run("an interrupted scheduler refuses every update, even for its owner", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.begin()
		eq(t, h.ready().terminate, true, "ready")
		mxaSch(t, h, func(sch *jsObject) { sch.set("phase", "interrupted") })
		r := h.ready()
		eq(t, r.terminate, false, "not terminal")
		eq(t, r.text, "Scheduling decision NOT saved: Goal scheduling belongs to another session or was interrupted. Use /goal-resume to take ownership.", "text")
	})

	t.Run("a rejected buffered turn flush stops the scheduler write with the flush error", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.begin()
		eq(t, h.ready().terminate, true, "ready")
		id := mxaGoalID(h)
		h.core.goalService.beginTurn(h.core.st, id)
		res, err := h.core.goalService.apply(h.core.st, goalMutationSpec{mutate: func(g *jsObject) *jsObject { return goalWith(g, "objective", "buffered edit") }})
		if err != nil || !res.ok {
			t.Fatalf("buffered apply: %v %s", err, res.message)
		}
		eq(t, h.core.goalService.isTurnBuffered(), true, "buffered")
		// Another process moves the goal's revision: the buffered transaction is rejected at flush.
		disk := h.fileGoal()
		if _, err := h.core.st.writeActiveGoalFile(goalWith(disk, "revision", goalRevision(disk)+5)); err != nil {
			t.Fatal(err)
		}
		r := h.ready()
		eq(t, r.terminate, false, "not terminal")
		eq(t, r.text, "Scheduling decision NOT saved: Goal "+id+" changed in another process; buffered changes were rejected. Refresh and retry.", "text")
	})

	t.Run("a paused write without a pause reason ledgers 'Scheduling paused'; a reason is kept", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.scheduler.write(func(g *jsObject) *jsObject { return goalWith(g, "status", "paused") })
		ev := mxaLedgerType(h, "goal_paused")
		eq(t, len(ev), 1, "goal_paused events")
		eq(t, gstr(ev[0], "reason"), "Scheduling paused", "default reason")
		eq(t, gstr(ev[0], "source"), "agent", "source")
		h2 := newSchedFx(t, fxOpts{})
		h2.core.scheduler.pause("Because I say so")
		ev = mxaLedgerType(h2, "goal_paused")
		eq(t, len(ev), 1, "goal_paused events")
		eq(t, gstr(ev[0], "reason"), "Because I say so", "kept reason")
	})
}

func TestMutantsASchedulerTimers(t *testing.T) {
	t.Run("a scheduler error cancels the timer and the armed generation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		called := false
		h.core.scheduler.later(100, func() { called = true })
		h.core.scheduler.armedGeneration = "g"
		h.core.scheduler.safe(func() { throwGoal("boom") })
		eq(t, h.core.scheduler.armedGeneration, "", "armed generation")
		eq(t, h.core.scheduler.timer == nil, true, "timer")
		h.tick(1000)
		eq(t, called, false, "the timer never fires")
		eq(t, h.h.notices, []string{"warning: Goal scheduling stopped: boom"}, "notice")
	})

	t.Run("cancelTimer forgets the armed generation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.scheduler.armedGeneration = "g"
		h.core.scheduler.cancelTimer()
		eq(t, h.core.scheduler.armedGeneration, "", "armed generation")
	})

	t.Run("a distant deadline arms a timer clamped to 2^31-1 ms", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		eq(t, h.core.scheduler.declare(goalContinuation{kind: "wait", reason: mxaS("far"), deadline: "9999-12-31T00:00:00Z"}).terminate, true, "wait")
		h.settled()
		var dues []int64
		for _, tm := range h.clock.timers {
			dues = append(dues, tm.due)
		}
		eq(t, dues, []int64{2147483647}, "timer due times")
	})
}

func TestMutantsASchedulerPause(t *testing.T) {
	t.Run("a pause for a goal another session owns is refused", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.begin()
		h.ready()
		mxaSch(t, h, func(sch *jsObject) { sch.set("owner", "someone-else") })
		err := tryGoal(func() { h.core.scheduler.pause("stop") })
		if err == nil || err.Error() != "Scheduling ownership changed; no pause was applied." {
			t.Fatalf("err = %v", err)
		}
		eq(t, h.status(), "active", "status")
	})

	t.Run("a pause ends the active accounting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaGoalID(h)
		h.core.accounting.begin(&id)
		eq(t, h.core.accounting.isActiveFor(id), true, "accounting runs")
		h.core.scheduler.pause("stop")
		eq(t, h.core.accounting.isActiveFor(id), false, "accounting ended")
		eq(t, h.status(), "paused", "status")
	})

	t.Run("failedDispatch pauses with the delivery-failure reason", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.scheduler.failedDispatch()
		eq(t, h.pauseReason(), "Goal checkpoint delivery failed after claiming allowance. Use /goal-resume; it will not be replayed.", "reason")
	})
}

func TestMutantsASchedulerRestoreResumeKickoff(t *testing.T) {
	t.Run("restore binds the wake listener and resets the run state", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.scheduler.shutdown()
		eq(t, h.core.scheduler.attached, false, "detached")
		s := h.core.scheduler
		s.inRun, s.declared, s.runGoalID = true, true, "x"
		s.restore()
		eq(t, s.attached, true, "attached")
		eq(t, s.inRun, false, "inRun")
		eq(t, s.declared, false, "declared")
		eq(t, s.runGoalID, "", "runGoalID")
	})

	t.Run("resume at zero allowance warns and refuses", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(0)})
		eq(t, h.core.scheduler.resume(), false, "resume")
		eq(t, h.h.notices, []string{"warning: Automatic continuation disabled by maxAutonomousRuns=0. Change the setting in /goal-settings, then use /goal-resume."}, "notices")
	})

	t.Run("resume refuses a goal whose token budget is spent", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaPatch(t, h, func(g *jsObject) {
			g.set("tokenBudget", 100.0)
			u := goalUsageCopy(g)
			u.set("tokensUsed", 100.0)
			g.set("usage", u)
		})
		eq(t, h.core.scheduler.resume(), false, "resume")
		eq(t, h.h.notices, []string{"warning: Error: A completed or token-budget-limited goal cannot resume."}, "notices")
		eq(t, h.status(), "active", "status")
	})

	t.Run("kickoff does nothing inside a run", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		h.core.scheduler.kickoff()
		h.tick(100)
		eq(t, h.phase(), "", "no scheduler state was written")
		eq(t, len(h.h.sent), 0, "sent")
	})

	t.Run("kickoff with one allowed run still dispatches a kickoff", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(1)})
		h.mock()
		h.core.scheduler.kickoff()
		h.tick(1)
		eq(t, len(h.h.sent), 1, "sent")
		eq(t, h.dispatchKind(), "kickoff", "dispatch kind")
		eq(t, h.used(), 1.0, "used")
		eq(t, len(h.h.notices), 0, "notices")
	})
}

func TestMutantsASchedulerDeclare(t *testing.T) {
	const reasonErr = "Scheduling decision NOT saved: wait requires a nonempty reason (at most 2000 characters)."
	const futureErr = "Scheduling decision NOT saved: wait requires a future ISO deadline with a timezone."
	const pollErr = "Scheduling decision NOT saved: Polling requires positive whole interval_seconds (at most 2147483) and max_checks."

	declareWait := func(h *schedFx, reason string, deadline string, waitID string, polling *goalPolling) goalAgentResult {
		return h.core.scheduler.declare(goalContinuation{kind: "wait", reason: &reason, deadline: deadline, waitID: waitID, polling: polling})
	}

	t.Run("a denied run cannot declare", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.scheduler.denied = true
		r := h.ready()
		eq(t, r.terminate, false, "terminal")
		eq(t, r.text, "Scheduling decision NOT saved: Scheduling requires an active auto-continue goal in this session.", "text")
	})

	t.Run("the wait reason is bounded by 2000 UTF-16 units and must not be blank", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			reason string
			ok     bool
		}{
			{"2000 units", strings.Repeat("x", 2000), true},
			{"2001 units", strings.Repeat("x", 2001), false},
			{"1000 astral characters", strings.Repeat("😀", 1000), true},
			{"1001 astral characters", strings.Repeat("😀", 1001), false},
			{"blank", "   \t ", false},
			{"empty", "", false},
		} {
			h := newSchedFx(t, fxOpts{})
			h.mock()
			h.begin()
			r := declareWait(h, c.reason, isoFromMs(h.now()+10000), "", nil)
			eq(t, r.terminate, c.ok, c.name)
			if !c.ok {
				eq(t, r.text, reasonErr, c.name)
			}
		}
	})

	t.Run("a nil reason is refused", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		r := h.core.scheduler.declare(goalContinuation{kind: "wait", deadline: isoFromMs(h.now() + 10000)})
		eq(t, r.text, reasonErr, "text")
	})

	t.Run("the deadline must lie strictly in the future and carry a zone", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.tick(5000)
		h.begin()
		eq(t, declareWait(h, "r", isoFromMs(5000), "", nil).text, futureErr, "deadline equal to now")
		eq(t, declareWait(h, "r", isoFromMs(4999), "", nil).text, futureErr, "past")
		eq(t, declareWait(h, "r", "2099-01-01T00:00:00", "", nil).text, futureErr, "no zone")
		eq(t, declareWait(h, "r", "2099-01-01", "", nil).text, futureErr, "date only")
		eq(t, declareWait(h, "r", "nonsense", "", nil).text, futureErr, "not a date")
		eq(t, declareWait(h, "r", isoFromMs(5001), "", nil).terminate, true, "one millisecond ahead")
	})

	t.Run("the reason is stored trimmed", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		eq(t, declareWait(h, "  hello  ", isoFromMs(h.now()+10000), "", nil).terminate, true, "wait")
		eq(t, gstr(h.wait0(), "reason"), "hello", "stored reason")
	})

	t.Run("polling must be whole, within 1..2147483 seconds, with at least one check", func(t *testing.T) {
		for _, c := range []struct {
			name string
			p    goalPolling
			ok   bool
		}{
			{"1/1", goalPolling{1, 1}, true},
			{"2147483/1", goalPolling{2147483, 1}, true},
			{"2147484/1", goalPolling{2147484, 1}, false},
			{"0/1", goalPolling{0, 1}, false},
			{"-1/1", goalPolling{-1, 1}, false},
			{"1.5/1", goalPolling{1.5, 1}, false},
			{"1/0", goalPolling{1, 0}, false},
			{"1/-1", goalPolling{1, -1}, false},
			{"1/1.5", goalPolling{1, 1.5}, false},
			{"NaN/1", goalPolling{math.NaN(), 1}, false},
		} {
			h := newSchedFx(t, fxOpts{})
			h.mock()
			h.begin()
			p := c.p
			r := declareWait(h, "r", isoFromMs(h.now()+10000), "", &p)
			eq(t, r.terminate, c.ok, c.name)
			if !c.ok {
				eq(t, r.text, pollErr, c.name)
			}
		}
	})

	t.Run("an unknown wait_id is refused when there is no wait", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		r := declareWait(h, "r", isoFromMs(h.now()+10000), "nope", nil)
		eq(t, r.terminate, false, "terminal")
		eq(t, r.text, "Scheduling decision NOT saved: Unknown wait_id.", "text")
	})

	t.Run("re-declaring a wait keeps its identity and deadline, rotates the token and restarts the check clock", func(t *testing.T) {
		const redeclareErr = "Scheduling decision NOT saved: Re-declare the current wait_id with its original deadline; checks cannot be reset."
		const pollingErr = "Scheduling decision NOT saved: Existing wait polling cannot be reset; omit polling when re-declaring wait_id."
		h := newSchedFx(t, fxOpts{limit: fxLimit(9)})
		h.mock()
		h.begin()
		eq(t, declareWait(h, "Await job", isoFromMs(h.now()+10000), "", &goalPolling{1, 2}).terminate, true, "wait")
		w := deepClone(h.wait0()).(*jsObject)
		id, dl := gstr(w, "id"), isoFromMs(int64(gnum(w, "deadline")))
		eq(t, declareWait(h, "Await job", dl, "other-id", nil).text, redeclareErr, "other id, same deadline")
		eq(t, declareWait(h, "Await job", dl, "", nil).text, redeclareErr, "no id")
		eq(t, declareWait(h, "Await job", isoFromMs(int64(gnum(w, "deadline"))+1000), id, nil).text, redeclareErr, "same id, other deadline")
		eq(t, declareWait(h, "Await job", dl, id, &goalPolling{2, 2}).text, pollingErr, "other interval")
		eq(t, declareWait(h, "Await job", dl, id, &goalPolling{1, 3}).text, pollingErr, "other check count")
		h.tick(300)
		eq(t, declareWait(h, "Await job", dl, id, &goalPolling{1, 2}).terminate, true, "same polling is accepted")
		now := h.wait0()
		eq(t, gstr(now, "id"), id, "identity kept")
		eq(t, gstr(now, "token") != gstr(w, "token"), true, "token rotated")
		next, _ := now.num("nextCheckAt")
		eq(t, next, 1300.0, "the check clock restarts from now")
		eq(t, declareWait(h, "Await job", dl, id, nil).terminate, true, "no polling: nothing to compare")
	})

	t.Run("a ready declaration drops the wait and takes a fresh generation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		eq(t, h.wait().terminate, true, "wait")
		eq(t, h.wait0() != nil, true, "waiting")
		gen1, _ := h.sch().str("generation")
		eq(t, h.ready().terminate, true, "ready")
		eq(t, h.wait0() == nil, true, "the wait is cleared")
		eq(t, h.phase(), "ready", "phase")
		gen2, _ := h.sch().str("generation")
		eq(t, gen2 != gen1 && gen2 != "", true, "generation rotated")
	})

	t.Run("a declaration stops the turn for its goal", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.begin()
		eq(t, h.core.runtime.currentTurnStoppedGoalId() == nil, true, "before")
		h.ready()
		got := h.core.runtime.currentTurnStoppedGoalId()
		eq(t, got != nil && *got == mxaGoalID(h), true, "turn stopped for the goal")
	})

	t.Run("the result text names the allowance and the wake token", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		r := h.ready()
		eq(t, r.text, "Scheduling decision saved. Stop this execution.\nAutonomous runs: 0/5.", "ready text")
		r = h.wait()
		w := h.wait0()
		token := gstr(w, "token")
		eq(t, r.text, "Scheduling decision saved. Stop this execution.\n"+schedulerSummary(h.sch(), fxLimit(5), true)+
			"\nWake token: "+token+". Register this token with the producer before completion; adapters must retain early results.", "wait text")
		eq(t, gstr(r.details, "wait_id"), gstr(w, "id"), "wait_id")
		eq(t, gstr(r.details, "waitToken"), token, "waitToken")
	})
}

func TestMutantsASchedulerBeginTurnMessage(t *testing.T) {
	t.Run("a nested begin keeps the declaration of the run", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.begin()
		h.core.scheduler.declared = true
		h.core.scheduler.denied = true
		h.begin()
		eq(t, h.core.scheduler.declared, true, "declared")
		eq(t, h.core.scheduler.denied, true, "denied")
		eq(t, h.core.runningGoalID != nil && *h.core.runningGoalID == mxaGoalID(h), true, "running goal")
	})

	t.Run("a new run clears a previous denial and the armed generation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		s := h.core.scheduler
		s.denied = true
		s.armedGeneration = "g"
		h.begin()
		eq(t, s.denied, false, "denied")
		eq(t, s.armedGeneration, "", "armed generation")
		eq(t, s.inRun, true, "inRun")
		eq(t, s.runGoalID, mxaGoalID(h), "runGoalID")
	})

	t.Run("audit entries do not take over a claim", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		mxaClaimed(t, h)
		h.begin()
		h.core.scheduler.message(map[string]any{"role": "custom", "customType": goalAuditEntry, "content": "audit"})
		eq(t, h.phase(), "claimed", "phase")
	})

	t.Run("a goal event that is not a checkpoint is ignored", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		sent := mxaClaimed(t, h)
		h.begin()
		h.core.scheduler.message(mxaCheckpoint(sent, func(d *jsObject) { d.set("kind", "note") }))
		eq(t, h.phase(), "claimed", "phase")
		eq(t, h.h.aborts, 0, "aborts")
		eq(t, h.core.scheduler.denied, false, "denied")
	})

	t.Run("a checkpoint is admitted only with version 3, the claimed phase, its generation and its dispatch id", func(t *testing.T) {
		const rejected = "warning: Goal checkpoint rejected: Error: Stale or unclaimed goal checkpoint."
		for _, c := range []struct {
			name string
			edit func(d *jsObject)
		}{
			{"version", func(d *jsObject) { d.set("version", 2.0) }},
			{"generation", func(d *jsObject) { d.set("generation", "other-generation") }},
			{"dispatch id", func(d *jsObject) { d.set("dispatchId", "other-dispatch") }},
		} {
			h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
			sent := mxaClaimed(t, h)
			h.begin()
			h.core.scheduler.message(mxaCheckpoint(sent, c.edit))
			eq(t, h.h.aborts, 1, c.name+": aborts")
			eq(t, h.core.scheduler.denied, true, c.name+": denied")
			eq(t, h.phase(), "claimed", c.name+": phase")
			eq(t, h.h.notices[len(h.h.notices)-1], rejected, c.name+": notice")
		}
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		sent := mxaClaimed(t, h)
		h.begin()
		h.core.scheduler.message(mxaCheckpoint(sent, nil))
		eq(t, h.h.aborts, 0, "the first delivery is admitted")
		eq(t, h.phase(), "running", "phase")
		h.begin()
		h.core.scheduler.message(mxaCheckpoint(sent, nil))
		eq(t, h.h.aborts, 1, "a second delivery of the same checkpoint is no longer claimed")
		eq(t, h.core.scheduler.denied, true, "denied")
		eq(t, h.h.notices[len(h.h.notices)-1], rejected, "notice")
	})

	t.Run("a rejected checkpoint records its goal id, or 'invalid' without one", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		sent := mxaClaimed(t, h)
		h.begin()
		h.core.scheduler.message(mxaCheckpoint(sent, func(d *jsObject) { d.set("version", 2.0) }))
		got := h.core.runtime.getCheckpointGoalId()
		eq(t, got != nil && *got == mxaGoalID(h), true, "checkpoint goal id")
		h.begin()
		h.core.scheduler.message(mxaCheckpoint(sent, func(d *jsObject) { d.set("version", 2.0); objDelete(d, "goalId") }))
		got = h.core.runtime.getCheckpointGoalId()
		eq(t, got != nil && *got == "invalid", true, "no goal id")
	})

	t.Run("a user message supersedes a claim and clears the decision", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		mxaClaimed(t, h)
		h.begin()
		h.core.scheduler.message(map[string]any{"role": "user", "content": "hello"})
		eq(t, h.phase(), "idle", "phase")
		eq(t, h.dispatchKind(), "", "dispatch")
		eq(t, h.decision() == nil, true, "decision")
	})

	t.Run("takeover forgets the declaration and the wait and renews the repair", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		h.wait()
		mxaSch(t, h, func(sch *jsObject) { sch.set("repairUsed", true) })
		eq(t, h.core.scheduler.declared, true, "declared")
		gen, _ := h.sch().str("generation")
		h.core.scheduler.takeover()
		eq(t, h.core.scheduler.declared, false, "declared")
		eq(t, h.wait0() == nil, true, "wait")
		eq(t, h.phase(), "idle", "phase")
		rep, _ := h.sch().flag("repairUsed")
		eq(t, rep, false, "repairUsed")
		gen2, _ := h.sch().str("generation")
		eq(t, gen2 != gen, true, "generation")
	})
}

func TestMutantsASchedulerSettled(t *testing.T) {
	t.Run("a denied run schedules nothing at settlement", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{lax: true})
		h.mock()
		h.begin()
		h.core.scheduler.denied = true
		h.settled()
		h.tick(100)
		eq(t, len(h.h.sent), 0, "sent")
		eq(t, h.phase(), "", "no scheduler state")
	})

	t.Run("a run of another goal schedules nothing for the focused one", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{lax: true})
		h.mock()
		h.begin()
		h.core.scheduler.runGoalID = "another-goal"
		h.settled()
		h.tick(100)
		eq(t, len(h.h.sent), 0, "sent")
		eq(t, h.phase(), "", "no scheduler state")
	})

	t.Run("without strict mode a spent repair does not pause: the missing declaration is implicit ready", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{lax: true})
		h.mock()
		h.begin()
		h.ready()
		h.core.scheduler.turn() // a later turn invalidates the decision
		mxaSch(t, h, func(sch *jsObject) { sch.set("repairUsed", true) })
		h.settled()
		h.tick(1)
		eq(t, h.status(), "active", "status")
		eq(t, h.dispatchKind(), "ready", "dispatch")
		eq(t, len(h.h.sent), 1, "sent")
	})

	t.Run("recover schedules a recovery dispatch", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.core.scheduler.recover()
		h.tick(1)
		eq(t, h.dispatchKind(), "recovery", "dispatch kind")
		eq(t, h.used(), 1.0, "used")
	})
}

func TestMutantsASchedulerSignal(t *testing.T) {
	waiting := func(t *testing.T) (*schedFx, string, string) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		h.wait()
		return h, mxaGoalID(h), gstr(h.wait0(), "token")
	}
	signalled := func(h *schedFx) bool { v, _ := h.wait0().flag("signalled"); return v }

	t.Run("a wake token of another session's goal is ignored without stopping the schedule", func(t *testing.T) {
		h, id, token := waiting(t)
		mxaSch(t, h, func(sch *jsObject) { sch.set("owner", "another-session") })
		h.core.scheduler.signal(id, token)
		eq(t, h.h.notices, []string(nil), "notices")
		eq(t, signalled(h), false, "signalled")
	})

	t.Run("a wrong token is ignored without stopping the schedule", func(t *testing.T) {
		h, id, _ := waiting(t)
		h.core.scheduler.signal(id, "wrong")
		eq(t, h.h.notices, []string(nil), "notices")
		eq(t, signalled(h), false, "signalled")
	})

	t.Run("a wake for another goal id is ignored", func(t *testing.T) {
		h, _, token := waiting(t)
		h.core.scheduler.signal("another-goal", token)
		eq(t, h.h.notices, []string(nil), "notices")
		eq(t, signalled(h), false, "signalled")
	})

	t.Run("a second wake with the same token writes nothing", func(t *testing.T) {
		h, id, token := waiting(t)
		h.core.scheduler.signal(id, token)
		eq(t, signalled(h), true, "signalled")
		rev := goalRevision(h.goal())
		h.core.scheduler.signal(id, token)
		eq(t, goalRevision(h.goal()), rev, "revision")
		eq(t, h.h.notices, []string(nil), "notices")
	})

	t.Run("scheduling a waiting goal ends its active accounting", func(t *testing.T) {
		h, id, _ := waiting(t)
		h.settled()
		h.core.accounting.begin(&id)
		h.core.scheduler.schedule()
		eq(t, h.core.accounting.isActiveFor(id), false, "accounting")
	})

	t.Run("isWaiting is false inside a run and only true in the waiting phase", func(t *testing.T) {
		h, _, _ := waiting(t)
		eq(t, h.core.scheduler.isWaiting(), false, "inside a run")
		h.settled()
		eq(t, h.core.scheduler.isWaiting(), true, "waiting")
		mxaSch(t, h, func(sch *jsObject) { sch.set("phase", "ready") })
		eq(t, h.core.scheduler.isWaiting(), false, "ready is not waiting")
	})
}

func TestMutantsASchedulerClaim(t *testing.T) {
	armed := func(t *testing.T) *schedFx {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		h.ready()
		h.settled()
		if h.core.scheduler.armedGeneration == "" {
			t.Fatal("not armed")
		}
		return h
	}

	t.Run("without an armed generation claim refuses silently", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		eq(t, h.core.scheduler.claim() == nil, true, "claim")
		eq(t, h.status(), "active", "status")
		eq(t, h.h.notices, []string(nil), "notices")
		eq(t, h.goal().has("scheduler"), false, "no scheduler state was written")
	})

	t.Run("a stale generation refuses silently and spends nothing", func(t *testing.T) {
		h := armed(t)
		h.core.scheduler.armedGeneration = "stale"
		eq(t, h.core.scheduler.claim() == nil, true, "claim")
		eq(t, h.status(), "active", "status")
		eq(t, h.used(), 0.0, "used")
		eq(t, h.h.notices, []string(nil), "notices")
		eq(t, h.core.scheduler.armedGeneration, "", "armed generation")
	})

	t.Run("a spent token budget stops the claim and pauses the goal", func(t *testing.T) {
		h := armed(t)
		mxaPatch(t, h, func(g *jsObject) {
			g.set("tokenBudget", 10.0)
			u := goalUsageCopy(g)
			u.set("tokensUsed", 10.0)
			g.set("usage", u)
		})
		eq(t, h.core.scheduler.claim() == nil, true, "claim")
		eq(t, h.status(), "paused", "status")
		eq(t, h.used(), 0.0, "used")
		eq(t, h.pauseReason(), "Goal wake stopped: Autonomous-run allowance exhausted. Increase or remove maxAutonomousRuns, or use /goal-resume to renew.", "reason")
	})

	t.Run("a missing decision stops the claim and pauses the goal", func(t *testing.T) {
		h := armed(t)
		mxaSch(t, h, func(sch *jsObject) { sch.set("phase", "idle"); sch.set("decision", undef{}) })
		eq(t, h.core.scheduler.claim() == nil, true, "claim")
		eq(t, h.status(), "paused", "status")
		eq(t, h.used(), 0.0, "used")
		eq(t, h.pauseReason(), "Goal wake stopped: No scheduling decision authorizes this checkpoint.", "reason")
	})

	t.Run("a recovery claim leaves the repair flag alone; other kinds clear it", func(t *testing.T) {
		for _, c := range []struct {
			purpose string
			want    bool
		}{{"recovery", true}, {"ready", false}, {"kickoff", false}, {"repair", true}} {
			h := armed(t)
			mxaSch(t, h, func(sch *jsObject) {
				d := newObject()
				d.set("kind", "ready")
				d.set("purpose", c.purpose)
				sch.set("decision", d)
				sch.set("repairUsed", true)
			})
			eq(t, h.core.scheduler.claim() != nil, true, c.purpose+": claim")
			rep, _ := h.sch().flag("repairUsed")
			eq(t, rep, c.want, c.purpose+": repairUsed")
		}
	})

	signalledWait := func(t *testing.T) (*schedFx, string) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		h.wait()
		id, token := mxaGoalID(h), gstr(h.wait0(), "token")
		h.settled()
		h.core.scheduler.signal(id, token)
		if h.core.scheduler.armedGeneration == "" {
			t.Fatal("not armed after the wake")
		}
		return h, token
	}

	t.Run("a wake claim spends the signal and rotates the wait token", func(t *testing.T) {
		h, token := signalledWait(t)
		eq(t, h.core.scheduler.claim() != nil, true, "claim")
		eq(t, h.dispatchKind(), "wake", "dispatch")
		eq(t, gstr(h.wait0(), "token") != token, true, "token rotated")
		sg, _ := h.wait0().flag("signalled")
		eq(t, sg, false, "signalled")
	})

	t.Run("a claim exactly at the wait deadline is refused", func(t *testing.T) {
		h, _ := signalledWait(t)
		dl, _ := h.wait0().num("deadline")
		h.clock.now = int64(dl)
		eq(t, h.core.scheduler.claim() == nil, true, "claim")
		eq(t, h.status(), "paused", "status")
		eq(t, h.pauseReason(), "Goal wake stopped: Wait deadline reached.", "reason")
		eq(t, h.used(), 0.0, "used")
	})
}

func TestMutantsASchedulerDates(t *testing.T) {
	t.Run("a date-time without an offset is local time", func(t *testing.T) {
		old := localZone
		localZone = time.FixedZone("x", 5*3600)
		defer func() { localZone = old }()
		want := jsDateParse("2030-01-01T05:00:00Z")
		eq(t, jsDateParse("2030-01-01T10:00:00"), want, "local")
		eq(t, jsDateParse("2030-01-01"), jsDateParse("2030-01-01T00:00:00Z"), "a date alone is UTC")
	})

	t.Run("days past the month's end and hours past 24 are not dates", func(t *testing.T) {
		for _, s := range []string{"2030-02-30", "2030-02-30T10:00:00Z", "2030-04-31", "2030-01-01T25:00:00Z", "2030-13-01", "2030-01-01T10:60:00Z", "2030-01-01T24:00:01Z"} {
			eq(t, math.IsNaN(jsDateParse(s)), true, s)
		}
		eq(t, jsDateParse("2024-02-29T00:00:00Z"), 1709164800000.0, "leap day")
	})

	t.Run("T24:00:00 is midnight of the next day", func(t *testing.T) {
		eq(t, jsDateParse("2030-01-01T24:00:00Z"), jsDateParse("2030-01-02T00:00:00Z"), "24:00")
		eq(t, jsDateParse("2030-01-31T24:00:00.000+02:00"), jsDateParse("2030-02-01T00:00:00+02:00"), "24:00 with an offset")
	})
}

// ---- core.go ----

// mxaSecondGoal adds a second active goal "id" to the project and reloads the pool; the focus entry keeps the first focused.
func mxaSecondGoal(t *testing.T, h *schedFx, id string) {
	t.Helper()
	g := createGoal("Second goal", true, false, false, clockMs())
	g.set("id", id)
	if _, err := h.core.st.writeActiveGoalFile(g); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	if err := h.core.loadState(); err != nil {
		t.Fatal(err)
	}
}

func mxaDiskOf(h *schedFx, id string) *jsObject {
	g := h.core.goals.get(id)
	p, _ := g.str("activePath")
	return parseGoalFile(h.cwd + "/" + p)
}

// mxaExternal rewrites the focused goal's file the way another process would (same revision unless bump).
func mxaExternal(t *testing.T, h *schedFx, bump float64, kv ...any) {
	t.Helper()
	disk := h.fileGoal()
	next := goalWith(disk, kv...)
	next.set("revision", goalRevision(disk)+bump)
	if _, err := h.core.st.writeActiveGoalFile(next); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
}

// mxaPending makes a continuation pending in the runtime for the focused goal (the clock is mocked: nothing fires).
func mxaPending(t *testing.T, h *schedFx) string {
	t.Helper()
	h.mock()
	id := mxaGoalID(h)
	h.core.runtime.queueContinuation(hostRuntimeCtx{h.h}, h.goal(), true)
	if !h.core.runtime.continuationPendingFor(id) {
		t.Fatal("no continuation pending")
	}
	return id
}

func TestMutantsACoreFocusToken(t *testing.T) {
	h := newSchedFx(t, fxOpts{})
	id := mxaGoalID(h)
	c := h.core
	eq(t, c.isFocusedOperationCurrent(c.focusedOperationToken(id)), true, "current token")
	eq(t, c.isFocusedOperationCurrent(goalFocusToken{goalId: "another", revision: c.focusRev}), false, "another goal, same revision")
	eq(t, c.isFocusedOperationCurrent(goalFocusToken{goalId: id, revision: c.focusRev + 1}), false, "same goal, later revision")
	r := c.focusedOperationCancelledResult("Update", goalFocusToken{goalId: "g-7", revision: 0})
	eq(t, r.text, "Update cancelled because goal g-7 is no longer focused in this session. The shared goal was not modified.", "text")
	eq(t, r.terminate, true, "terminate")
	eq(t, r.details != nil, true, "details")
}

func TestMutantsACoreServiceRef(t *testing.T) {
	t.Run("a reconciled goal that is no longer active auto-continue drops its continuation", func(t *testing.T) {
		for _, c := range []struct {
			name      string
			status    string
			auto      bool
			keepsCont bool
			keepsAcct bool
		}{
			{"paused", "paused", true, false, false},
			{"auto off", "active", false, false, true},
			{"live", "active", true, true, true},
		} {
			h := newSchedFx(t, fxOpts{})
			id := mxaPending(t, h)
			h.core.accounting.begin(&id)
			g := goalWith(h.goal(), "status", c.status, "autoContinue", c.auto)
			coreServiceRef{h.core}.onReconciled(g)
			eq(t, h.core.runtime.continuationPendingFor(id), c.keepsCont, c.name+": continuation")
			eq(t, h.core.accounting.isActiveFor(id), c.keepsAcct, c.name+": accounting")
		}
	})

	t.Run("a focus change drops the continuation and the accounting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaPending(t, h)
		h.core.accounting.begin(&id)
		coreServiceRef{h.core}.onFocusChanged("a", "b")
		eq(t, h.core.runtime.continuationPendingFor(id), false, "continuation")
		eq(t, h.core.accounting.isActiveFor(id), false, "accounting")
	})
}

type mxaToolsErrHost struct{ *kernelHost }

func (mxaToolsErrHost) GetActiveTools() ([]string, error) { return nil, os.ErrClosed }

func TestMutantsACoreInstallToolsHostError(t *testing.T) {
	h := newSchedFx(t, fxOpts{})
	kh := h.h
	h.core.h = mxaToolsErrHost{kh}
	h.core.installTools([]string{"get_goal"})
	eq(t, kh.tools, []string{"read", "write", "bash"}, "a host that cannot list its tools is left alone")
}

func TestMutantsACoreAuditState(t *testing.T) {
	t.Run("the audit result card disappears after six seconds", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.core.setAuditResult("approved", "report")
		eq(t, h.core.auditResult != nil, true, "shown")
		h.tick(5999)
		eq(t, h.core.auditResult != nil, true, "still shown at 5999 ms")
		h.tick(1)
		eq(t, h.core.auditResult == nil, true, "gone at 6000 ms")
		eq(t, h.core.auditResultTimer == nil, true, "timer cleared")
	})

	t.Run("abortAudit needs both a controller and a progress record", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		c := h.core
		c.abortAudit() // neither: nothing happens
		eq(t, c.auditAborted, false, "aborted without audit")
		calls := 0
		c.auditAbortController = func() { calls++ }
		c.abortAudit() // no progress
		eq(t, calls, 0, "controller not called without progress")
		eq(t, c.auditAborted, false, "aborted flag")
		eq(t, c.auditAbortController != nil, true, "controller kept")
		c.auditAbortController = nil
		c.auditProgress = &auditorWidgetProgress{}
		c.abortAudit() // no controller
		eq(t, c.auditAborted, false, "aborted flag")
		eq(t, c.auditProgress != nil, true, "progress kept")
	})

	t.Run("abortAudit cancels the audit, drops the progress and records the abort", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		c := h.core
		calls, stops := 0, 0
		c.auditAbortController = func() { calls++ }
		c.auditAnimationStop = func() { stops++ }
		c.auditProgress = &auditorWidgetProgress{}
		c.abortAudit()
		eq(t, calls, 1, "controller")
		eq(t, stops, 1, "animation stop")
		eq(t, c.auditProgress == nil, true, "progress")
		eq(t, c.auditAbortController == nil, true, "controller cleared")
		eq(t, c.auditAborted, true, "aborted")
	})

	t.Run("leaving a modal never takes the depth below zero", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.exitGoalModal()
		eq(t, h.core.goalModalDepth, 0, "depth")
		h.core.enterGoalModal()
		h.core.exitGoalModal()
		h.core.exitGoalModal()
		eq(t, h.core.goalModalDepth, 0, "depth")
	})

	t.Run("only an active auto-continue goal is actionable for a continuation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaGoalID(h)
		eq(t, h.core.isActionableContinuationGoal(&id), true, "active auto-continue")
		eq(t, h.core.isActionableContinuationGoal(mxaS("other")), false, "another goal")
		eq(t, h.core.isActionableContinuationGoal(mxaS("")), false, "empty id")
		eq(t, h.core.isActionableContinuationGoal(nil), false, "nil")
		mxaPatch(t, h, func(g *jsObject) { g.set("status", "paused") })
		eq(t, h.core.isActionableContinuationGoal(&id), false, "paused")
		mxaPatch(t, h, func(g *jsObject) { g.set("status", "active"); g.set("autoContinue", false) })
		eq(t, h.core.isActionableContinuationGoal(&id), false, "auto-continue off")
	})
}

func TestMutantsACoreFocus(t *testing.T) {
	t.Run("a focus entry marks the focus explicit and names an external root", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		c := h.core
		id := mxaGoalID(h)
		c.explicit = false
		n := len(h.h.entries)
		c.appendFocusEntry(&id, "selected")
		eq(t, c.explicit, true, "explicit")
		last := h.h.entries[n]
		eq(t, last.has("storageRoot"), false, "default root is not recorded")
		c.externalFocusRoot, c.focusStorageRoot = true, "/somewhere/else"
		c.appendFocusEntry(&id, "selected")
		last = h.h.entries[n+1]
		eq(t, gstr(last, "storageRoot"), "/somewhere/else", "external root")
	})

	t.Run("switching focus persists the previous goal's buffered turn first", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaSecondGoal(t, h, "goal-b")
		a := mxaGoalID(h)
		eq(t, a, "scheduler-fixture", "focus")
		h.core.goalService.beginTurn(h.core.st, a)
		if res, err := h.core.goalService.apply(h.core.st, goalMutationSpec{mutate: func(g *jsObject) *jsObject { return goalWith(g, "objective", "buffered edit") }}); err != nil || !res.ok {
			t.Fatalf("apply: %v %s", err, res.message)
		}
		eq(t, h.core.goalService.isTurnBuffered(), true, "buffered")
		h.core.setFocusedGoalId(mxaS("goal-b"), "focus", false)
		eq(t, gstr(mxaDiskOf(h, a), "objective"), "buffered edit", "the buffered edit reached the disk")
		eq(t, *h.core.focused, "goal-b", "focus")
	})

	t.Run("an unknown goal id leaves nothing focused", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.setFocusedGoalId(mxaS("no-such-goal"), "focus", false)
		eq(t, h.core.focused == nil, true, "focused")
	})

	t.Run("a focus change drops the continuation and the accounting; an unchanged focus keeps them", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaSecondGoal(t, h, "goal-b")
		a := mxaPending(t, h)
		h.core.accounting.begin(&a)
		h.core.setFocusedGoalId(&a, "same", false)
		eq(t, h.core.accounting.isActiveFor(a), true, "accounting kept for an unchanged focus")
		h.core.setFocusedGoalId(mxaS("goal-b"), "switch", false)
		eq(t, h.core.accounting.isActiveFor(a), false, "accounting")
		eq(t, h.core.runtime.continuationPendingFor(a), false, "continuation")
	})

	t.Run("clearing the focus ledgers goal_unfocused with the reason", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.setFocusedGoalId(nil, "cleared by test", true)
		ev := mxaLedgerType(h, "goal_unfocused")
		eq(t, len(ev), 1, "events")
		eq(t, gstr(ev[0], "reason"), "cleared by test", "reason")
		eq(t, len(mxaLedgerType(h, "goal_focused")), 0, "no goal_focused event")
	})

	t.Run("arming a goal without auto-continue does not reconcile it from disk", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaPatch(t, h, func(g *jsObject) { g.set("autoContinue", false) })
		mxaExternal(t, h, 1, "objective", "changed by another process")
		h.core.armFocusedContinuation()
		eq(t, gstr(h.goal(), "objective"), "Test explicit scheduling", "memory untouched")
	})

	t.Run("removing the focused goal persists its buffered turn and drops it from the pool", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		a := mxaGoalID(h)
		p, _ := h.goal().str("activePath")
		h.core.goalService.beginTurn(h.core.st, a)
		if res, err := h.core.goalService.apply(h.core.st, goalMutationSpec{mutate: func(g *jsObject) *jsObject { return goalWith(g, "objective", "buffered edit") }}); err != nil || !res.ok {
			t.Fatalf("apply: %v %s", err, res.message)
		}
		h.core.removeFocusedGoal("removed")
		eq(t, gstr(parseGoalFile(h.cwd+"/"+p), "objective"), "buffered edit", "the buffered edit reached the disk")
		eq(t, h.core.goals.has(a), false, "pool")
		eq(t, h.core.focused == nil, true, "focused")
	})
}

func TestMutantsACoreAccounting(t *testing.T) {
	t.Run("accounting does not start for a waiting or inactive goal", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaGoalID(h)
		h.core.beginAccounting()
		eq(t, h.core.accounting.isActiveFor(id), true, "active goal")
		h.begin()
		h.wait()
		h.settled()
		eq(t, h.core.scheduler.isWaiting(), true, "waiting")
		h.core.beginAccounting()
		eq(t, h.core.accounting.isActiveFor(id), false, "waiting goal")
		h2 := newSchedFx(t, fxOpts{})
		id2 := mxaGoalID(h2)
		mxaPatch(t, h2, func(g *jsObject) { g.set("status", "paused") })
		h2.core.beginAccounting()
		eq(t, h2.core.accounting.isActiveFor(id2), false, "paused goal")
	})

	t.Run("a first call only starts accounting and charges nothing", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaGoalID(h)
		h.core.accounting.clear()
		h.core.accountProgress(500)
		eq(t, gnum(h.goal().obj("usage"), "tokensUsed"), 0.0, "tokens")
		eq(t, h.core.accounting.isActiveFor(id), true, "accounting started")
		h.core.accountProgress(500)
		eq(t, gnum(h.goal().obj("usage"), "tokensUsed"), 500.0, "the second call charges")
	})

	t.Run("a charge adds the tokens and the elapsed whole seconds to the usage", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.core.beginAccounting()
		h.tick(5900)
		h.core.accountProgress(7)
		u := h.goal().obj("usage")
		eq(t, gnum(u, "tokensUsed"), 7.0, "tokens")
		eq(t, gnum(u, "activeSeconds"), 5.0, "seconds")
		eq(t, gstr(h.goal(), "updatedAt"), isoFromMs(5900), "updatedAt")
	})

	t.Run("reaching the token budget limits the goal and drops the continuation and the network backoff", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaPatch(t, h, func(g *jsObject) { g.set("tokenBudget", 100.0) })
		id := mxaPending(t, h)
		h.core.beginAccounting()
		eq(t, h.core.runtime.scheduleNetworkErrorRetry(hostRuntimeCtx{h.h}, h.goal(), nil) != nil, true, "backoff scheduled")
		h.core.accountProgress(100)
		eq(t, h.status(), "budget_limited", "status")
		eq(t, h.core.runtime.continuationPendingFor(id), false, "continuation")
		eq(t, h.core.runtime.networkErrorRetryGoalID == nil, true, "network backoff")
		eq(t, h.core.accounting.isActiveFor(id), false, "accounting")
		eq(t, h.core.runtime.consumePostBudgetReminder(), true, "wrap-up reminder armed")
		eq(t, len(mxaLedgerType(h, "goal_budget_limited")), 1, "ledger")
	})

	t.Run("a budget is reached at exactly zero remaining", func(t *testing.T) {
		mk := func(used float64) *jsObject {
			g := newObject()
			g.set("tokenBudget", 100.0)
			u := newObject()
			u.set("tokensUsed", used)
			g.set("usage", u)
			return g
		}
		eq(t, budgetReached(mk(98)), false, "2 remaining")
		eq(t, budgetReached(mk(99)), false, "1 remaining")
		eq(t, budgetReached(mk(100)), true, "0 remaining")
		eq(t, budgetReached(mk(150)), true, "overspent")
		eq(t, budgetReached(newObject()), false, "no budget")
	})
}

func TestMutantsACoreSync(t *testing.T) {
	t.Run("a complete goal is not synced from disk", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaPatch(t, h, func(g *jsObject) { g.set("status", "complete") })
		mxaExternal(t, h, 0, "objective", "changed on disk")
		eq(t, h.core.syncGoalPromptFromDisk(), false, "sync")
		eq(t, gstr(h.goal(), "objective"), "Test explicit scheduling", "objective")
	})

	t.Run("sync reports whether the objective changed", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		eq(t, h.core.syncGoalPromptFromDisk(), false, "unchanged")
		mxaExternal(t, h, 0, "objective", "changed on disk")
		eq(t, h.core.syncGoalPromptFromDisk(), true, "changed")
		eq(t, gstr(h.goal(), "objective"), "changed on disk", "objective")
		eq(t, h.core.syncGoalPromptFromDisk(), false, "unchanged again")
	})

	t.Run("a refresh that picked up a new objective stamps updatedAt", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.tick(12345)
		before := gstr(h.goal(), "updatedAt")
		h.core.refreshGoalDisplayFromDisk()
		eq(t, gstr(h.goal(), "updatedAt"), before, "no change, no stamp")
		mxaExternal(t, h, 0, "objective", "changed on disk")
		h.core.refreshGoalDisplayFromDisk()
		eq(t, gstr(h.goal(), "objective"), "changed on disk", "objective")
		eq(t, gstr(h.goal(), "updatedAt"), isoFromMs(12345), "updatedAt")
	})
}

func TestMutantsACoreStall(t *testing.T) {
	stalled := func(t *testing.T, minutes float64) *schedFx {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.save("stallTimeoutMinutes", minutes)
		invalidateGoalSettingsCache()
		h.core.lastGoalActivityAt = 0
		return h
	}

	t.Run("the stall fires exactly at the timeout, with the plural form, a ledger event and a notice", func(t *testing.T) {
		h := stalled(t, 2)
		h.tick(119999)
		eq(t, h.core.checkStall(), "", "one millisecond early")
		h.tick(1)
		id := mxaGoalID(h)
		eq(t, h.core.checkStall(), "\n\n[GOAL STALLED goalId="+id+"]\nNo continuation or tool activity for 2 minutes. Report your progress or ask the user to pause or resume the goal.", "note")
		eq(t, h.h.notices, []string{"warning: Goal stalled: no activity for 2 minutes."}, "notices")
		ev := mxaLedgerType(h, "goal_stalled")
		eq(t, len(ev), 1, "events")
		eq(t, gstr(ev[0], "reason"), "No continuation or tool activity for 2 minutes.", "reason")
		eq(t, gstr(ev[0], "goalId"), id, "goal")
		eq(t, h.core.checkStall(), "", "only once")
	})

	t.Run("one minute is singular", func(t *testing.T) {
		h := stalled(t, 1)
		h.tick(60000)
		fxMatch(t, h.core.checkStall(), `for 1 minute\. Report`, "note")
		eq(t, h.h.notices, []string{"warning: Goal stalled: no activity for 1 minute."}, "notices")
	})

	t.Run("only an active auto-continue goal stalls", func(t *testing.T) {
		h := stalled(t, 1)
		h.tick(60000)
		mxaPatch(t, h, func(g *jsObject) { g.set("status", "paused") })
		eq(t, h.core.checkStall(), "", "paused")
		mxaPatch(t, h, func(g *jsObject) { g.set("status", "active"); g.set("autoContinue", false) })
		eq(t, h.core.checkStall(), "", "auto-continue off")
		eq(t, h.core.stallNotified, false, "not notified")
	})

	t.Run("activity re-arms the stall notice", func(t *testing.T) {
		h := stalled(t, 1)
		h.core.stallNotified = true
		h.core.touchGoalActivity()
		eq(t, h.core.stallNotified, false, "stallNotified")
		eq(t, h.core.lastGoalActivityAt, int64(0), "last activity")
		h.tick(7)
		h.core.touchGoalActivity()
		eq(t, h.core.lastGoalActivityAt, int64(7), "last activity")
	})
}

func TestMutantsACoreRender(t *testing.T) {
	t.Run("hideUnfocusedBanner suppresses the unfocused hint", func(t *testing.T) {
		for _, hide := range []bool{false, true} {
			h := newSchedFx(t, fxOpts{})
			h.h.noUI = false
			h.save("hideUnfocusedBanner", hide)
			invalidateGoalSettingsCache()
			h.core.assignFocused(nil)
			h.core.renderUI()
			var hint []string
			for _, s := range h.h.statuses {
				if strings.Contains(s[1], "unfocused") {
					hint = append(hint, s[1])
				}
			}
			if hide {
				eq(t, hint, []string(nil), "hidden")
			} else {
				eq(t, hint, []string{"goal: unfocused [1 open] - /goal-focus"}, "shown")
			}
		}
	})

	t.Run("the dashboard registers a width listener once and lays out at the host width", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.h.noUI = false
		h.h.width = 40
		h.core.renderUI()
		eq(t, h.core.widgetWidthOff != nil, true, "width listener")
		pushes := h.h.pushes()
		if len(pushes) == 0 {
			t.Fatal("no widget push")
		}
		widest := 0
		for _, l := range pushes[len(pushes)-1].lines {
			if w := visibleWidth(l); w > widest {
				widest = w
			}
		}
		eq(t, widest > 0 && widest <= 40, true, "widest line fits the 40 columns of the host")
	})

	t.Run("the dashboard is bounded by the host's terminal height", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.h.noUI = false
		var tasks []any
		for i := 0; i < 30; i++ {
			task := newObject()
			task.set("id", "t"+jsNumber(float64(i)))
			task.set("title", "Task "+jsNumber(float64(i)))
			task.set("status", "pending")
			tasks = append(tasks, task)
		}
		list := newObject()
		list.set("proposedAt", nowIso())
		list.set("tasks", tasks)
		list.set("blockCompletion", false)
		mxaPatch(t, h, func(g *jsObject) { g.set("taskList", list) })
		h.core.dashboardExpanded = true
		h.h.rows = 14
		h.core.renderUI()
		pushes := h.h.pushes()
		last := pushes[len(pushes)-1].lines
		eq(t, len(last) <= 14-widgetHeightReserve, true, "bounded lines: "+jsNumber(float64(len(last))))
		h.h.rows = 0
		h.core.pushWidget()
		pushes = h.h.pushes()
		eq(t, len(pushes[len(pushes)-1].lines) > 14, true, "unbounded without rows")
	})
}

func TestMutantsACoreResolveSessionFocus(t *testing.T) {
	mk := func(id, status string) *jsObject {
		g := createGoal("Goal "+id, true, false, false, 1)
		g.set("id", id)
		g.set("status", status)
		return g
	}
	pool := newPool()
	pool.set("done", mk("done", "complete"))
	pool.set("live", mk("live", "active"))
	entry := func(id string) *focusEntry { return &focusEntry{goalID: &id} }
	eq(t, resolveSessionFocus(pool, entry("live"), nil, false), mxaS("live"), "a live goal named by the entry")
	eq(t, resolveSessionFocus(pool, entry("done"), nil, true), (*string)(nil), "a complete goal named by the entry is not focused")
	eq(t, resolveSessionFocus(pool, &focusEntry{}, mk("legacy", "active"), true), (*string)(nil), "an entry without a goal beats the legacy goal and auto-select")

	two := newPool()
	two.set("a", mk("a", "active"))
	two.set("b", mk("b", "active"))
	eq(t, resolveSessionFocus(two, nil, nil, true), (*string)(nil), "two open goals are never auto-selected")
	one := newPool()
	one.set("a", mk("a", "active"))
	eq(t, resolveSessionFocus(one, nil, nil, true), mxaS("a"), "one open goal is auto-selected")
	eq(t, resolveSessionFocus(one, nil, nil, false), (*string)(nil), "unless auto-select is off")
}

func TestMutantsACoreLoadState(t *testing.T) {
	t.Run("complete goals leave the pool", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		g := createGoal("Finished", true, false, false, clockMs())
		g.set("id", "finished")
		g.set("status", "complete")
		if _, err := h.core.st.writeActiveGoalFile(g); err != nil {
			t.Fatal(err)
		}
		invalidateGoalPoolCache()
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.goals.has("finished"), false, "complete goal")
		eq(t, h.core.goals.has("scheduler-fixture"), true, "open goal")
	})

	t.Run("tasks follow the disableTasks setting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		eq(t, h.core.tasksEnabled, true, "default")
		h.save("disableTasks", true)
		invalidateGoalSettingsCache()
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.tasksEnabled, false, "disabled")
	})

	t.Run("a reload invalidates pending focus tokens even when nothing was focused", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.setFocusedGoalId(nil, "clear", false)
		h.h.branch = nil
		h.save("autoSelectSingleGoal", false)
		invalidateGoalSettingsCache()
		before := h.core.focusRev
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.focused == nil, true, "still nothing focused")
		eq(t, h.core.focusRev, before+1, "revision")
	})

	t.Run("a reload ends the accounting and the continuation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		id := mxaPending(t, h)
		h.core.accounting.begin(&id)
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.accounting.isActiveFor(id), false, "accounting")
		eq(t, h.core.runtime.continuationPendingFor(id), false, "continuation")
		eq(t, h.core.runningGoalID == nil, true, "running goal")
	})

	legacyEntry := func(id, objective, path string) *jsObject {
		g := createGoal(objective, true, false, false, 1)
		g.set("id", id)
		g.set("activePath", path)
		data := newObject()
		data.set("goal", g)
		e := newObject()
		e.set("type", "custom")
		e.set("customType", stateEntryType)
		e.set("data", data)
		return e
	}

	t.Run("a legacy state entry takes the objective from the goal file", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		path, _ := h.goal().str("activePath")
		h.h.branch = []any{legacyEntry("legacy-goal", "stale objective", path)}
		h.save("autoSelectSingleGoal", false)
		invalidateGoalSettingsCache()
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.focused != nil && *h.core.focused == "legacy-goal", true, "focused")
		eq(t, gstr(h.goal(), "objective"), "Test explicit scheduling", "objective from the file")
	})

	t.Run("an external pool root ignores the legacy state entry", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		path, _ := h.goal().str("activePath")
		root := t.TempDir()
		t.Setenv("PI_GOAL_ROOT", root)
		invalidateGoalSettingsCache()
		h.h.sessionID = "external-root-session" // the pool root is pinned per session: a new session resolves it again
		h.h.branch = []any{legacyEntry("legacy-goal", "stale objective", path)}
		if err := h.core.loadState(); err != nil {
			t.Fatal(err)
		}
		eq(t, h.core.externalFocusRoot, true, "external root")
		eq(t, h.core.focused == nil, true, "focused")
	})
}

func TestMutantsACoreSetGoal(t *testing.T) {
	t.Run("focusing another goal ends the accounting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		a := mxaGoalID(h)
		h.core.accounting.begin(&a)
		b := createGoal("Other", true, false, false, clockMs())
		b.set("id", "other-goal")
		h.core.setGoal(b, false, "")
		eq(t, h.core.accounting.isActiveFor(a), false, "accounting")
		eq(t, h.core.accounting.goalId() == nil, true, "accounting goal")
	})

	t.Run("a focus entry is appended only when the focus changed", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		n := len(h.h.entries)
		h.core.setGoal(goalWith(h.goal(), "objective", "same goal"), false, "selected")
		eq(t, len(h.h.entries), n, "entries for an unchanged focus")
		b := createGoal("Other", true, false, false, clockMs())
		b.set("id", "other-goal")
		h.core.setGoal(b, false, "selected")
		eq(t, len(h.h.entries), n+1, "entries for a changed focus")
		eq(t, gstr(h.h.entries[n], "reason"), "selected", "reason")
	})

	t.Run("a paused goal ends the accounting and the continuation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		a := mxaPending(t, h)
		h.core.accounting.begin(&a)
		h.core.setGoal(goalWith(h.goal(), "status", "paused"), false, "")
		eq(t, h.core.accounting.isActiveFor(a), false, "accounting")
		eq(t, h.core.runtime.continuationPendingFor(a), false, "continuation")
	})

	t.Run("a goal that is not auto-continue active drops the continuation but keeps the accounting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		a := mxaPending(t, h)
		h.core.accounting.begin(&a)
		h.core.setGoal(goalWith(h.goal(), "autoContinue", false), false, "")
		eq(t, h.core.runtime.continuationPendingFor(a), false, "continuation")
		eq(t, h.core.accounting.isActiveFor(a), true, "accounting")
	})

	t.Run("persisting writes the goal with a new revision", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		before := goalRevision(h.fileGoal())
		h.core.setGoal(h.goal(), false, "")
		eq(t, goalRevision(h.fileGoal()), before, "not persisted")
		h.core.setGoal(h.goal(), true, "")
		eq(t, goalRevision(h.fileGoal()), before+1, "persisted")
	})
}

func TestMutantsACoreArchiveStop(t *testing.T) {
	t.Run("archiving keeps a complete goal complete and pauses any other", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		g := h.core.archiveCurrentGoal("user")
		eq(t, g != nil && gstr(g, "status") == "paused", true, "active goal is archived paused")
		h2 := newSchedFx(t, fxOpts{})
		h2.goal().set("status", "complete")
		g = h2.core.archiveCurrentGoal("")
		eq(t, g != nil && gstr(g, "status") == "complete", true, "complete goal stays complete")
	})

	t.Run("archiving takes the objective from the file and leaves the focused goal as it was", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		mxaExternal(t, h, 0, "objective", "changed on disk")
		g := h.core.archiveCurrentGoal("user")
		eq(t, g != nil && gstr(g, "objective") == "changed on disk", true, "archived objective")
		eq(t, gstr(h.goal(), "status"), "active", "the focused goal is not committed")
		eq(t, gstr(h.goal(), "objective"), "Test explicit scheduling", "memory objective")
	})

	t.Run("stopping ledgers a goal_paused event only for a pause, with 'unknown' for no reason", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.stopActiveGoal("blocked", "because")
		eq(t, h.status(), "blocked", "status")
		eq(t, len(mxaLedgerType(h, "goal_paused")), 0, "no goal_paused for a block")
		eq(t, readGoalLedger(storage{cwd: h.cwd}).malformed, 0, "no ledger line was written for a block (one would be malformed: status is not paused)")
		h2 := newSchedFx(t, fxOpts{})
		h2.core.stopActiveGoal("paused", "")
		ev := mxaLedgerType(h2, "goal_paused")
		eq(t, len(ev), 1, "events")
		eq(t, gstr(ev[0], "reason"), "unknown", "reason")
		eq(t, gstr(ev[0], "status"), "paused", "status")
		h3 := newSchedFx(t, fxOpts{})
		h3.core.stopActiveGoal("paused", "agent")
		eq(t, gstr(mxaLedgerType(h3, "goal_paused")[0], "reason"), "agent", "given reason")
	})

	t.Run("stopping ends the accounting and the continuation", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		a := mxaPending(t, h)
		h.core.accounting.begin(&a)
		h.core.scheduler.armedGeneration = "g"
		h.core.stopActiveGoal("paused", "user")
		eq(t, h.core.accounting.isActiveFor(a), false, "accounting")
		eq(t, h.core.runtime.continuationPendingFor(a), false, "continuation")
		eq(t, h.core.scheduler.armedGeneration, "", "armed generation")
	})
}

func TestMutantsACoreReplaceGoal(t *testing.T) {
	taskList := func() *jsObject {
		v, err := parseJSON([]byte(`{"proposedAt":"2026-01-01T00:00:00.000Z","blockCompletion":true,"tasks":[{"id":"t1","title":"One","status":"pending"},{"id":"t2","title":"Two","status":"pending"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return v.(*jsObject)
	}

	t.Run("a goal created with a task list ledgers task_list_set", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.replaceGoal(goalCreationConfig{objective: "With tasks", autoContinue: false, taskList: taskList()}, false, "", -1)
		ev := mxaLedgerType(h, "task_list_set")
		eq(t, len(ev), 1, "events")
		eq(t, gnum(ev[0], "taskCount"), 2.0, "taskCount")
		eq(t, gflag(ev[0], "blockCompletion"), true, "blockCompletion")
		eq(t, gstr(ev[0], "goalId"), mxaGoalID(h), "goal")
		eq(t, len(mxaLedgerType(h, "goal_created")), 1, "goal_created")
	})

	t.Run("a new goal starts accounting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.core.replaceGoal(goalCreationConfig{objective: "Fresh", autoContinue: false}, false, "", -1)
		eq(t, h.core.accounting.isActiveFor(mxaGoalID(h)), true, "accounting")
	})

	t.Run("a new goal kicks off only when asked to start now", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.core.replaceGoal(goalCreationConfig{objective: "Not yet", autoContinue: true}, false, "", -1)
		h.tick(100)
		eq(t, len(h.h.sent), 0, "sent")
		eq(t, h.phase(), "idle", "phase")
		h.core.replaceGoal(goalCreationConfig{objective: "Now", autoContinue: true}, true, "", -1)
		h.tick(1)
		eq(t, len(h.h.sent), 1, "sent")
		eq(t, h.dispatchKind(), "kickoff", "dispatch kind")
	})
}

func TestMutantsACoreCheckpointDelivery(t *testing.T) {
	h := newSchedFx(t, fxOpts{limit: fxLimit(3)})
	sent := mxaClaimed(t, h)
	eq(t, sent.display, false, "the checkpoint is hidden")
	eq(t, sent.trigger, true, "trigger")
	eq(t, sent.deliverAs, "followUp", "deliverAs")
	eq(t, sent.customType, goalEventEntry, "type")
}

// ---- goal_accounting.go ----

func TestMutantsAAccounting(t *testing.T) {
	at := func(ms int64) *int64 { return &ms }

	t.Run("elapsed time is charged in whole seconds, rounded down", func(t *testing.T) {
		a := &goalAccounting{lastAccountedAt: at(1000)}
		c := a.charge(accountingChargeOpts{now: at(2500)})
		eq(t, c.seconds, 1.0, "1500 ms")
		eq(t, *a.lastAccountedAt, int64(2500), "baseline advanced")
		c = a.charge(accountingChargeOpts{now: at(2900)})
		eq(t, c.seconds, 0.0, "400 ms")
		eq(t, (&goalAccounting{}).charge(accountingChargeOpts{now: at(9000)}).seconds, 0.0, "no baseline")
		eq(t, (&goalAccounting{lastAccountedAt: at(9000)}).charge(accountingChargeOpts{now: at(1000)}).seconds, 0.0, "clock going backwards")
		eq(t, (&goalAccounting{lastAccountedAt: at(1000)}).liveSeconds(2999), 1.0, "live seconds")
	})

	t.Run("completed-turn tokens are truncated to whole numbers and never negative", func(t *testing.T) {
		a := &goalAccounting{}
		eq(t, a.charge(accountingChargeOpts{completedTurnTokens: 2.9}).tokens, 2.0, "2.9")
		eq(t, a.charge(accountingChargeOpts{completedTurnTokens: -3}).tokens, 0.0, "-3")
		eq(t, a.charge(accountingChargeOpts{completedTurnTokens: -0.5}).tokens, 0.0, "-0.5")
		eq(t, a.charge(accountingChargeOpts{completedTurnTokens: 7}).tokens, 7.0, "7")
	})

	t.Run("the budget reads the remaining tokens and states them", func(t *testing.T) {
		g := newObject()
		g.set("tokenBudget", 100.0)
		u := newObject()
		u.set("tokensUsed", 40.9)
		g.set("usage", u)
		rem, ok := budgetRemaining(g)
		eq(t, ok, true, "has a budget")
		eq(t, rem, 60.0, "remaining")
		line, _ := budgetLine(g)
		eq(t, line, "token budget: 40/100 used, 60 remaining", "line")
		model, _ := modelBudgetLine(g)
		eq(t, model, "Lifetime token budget: 40/100 used, 60 remaining (spending cap, not context capacity)", "model line")
		u.set("tokensUsed", 130.0)
		line, _ = budgetLine(g)
		eq(t, line, "token budget: 130/100 used, 0 remaining", "overspent")
	})

	t.Run("a negative token reading is unavailable", func(t *testing.T) {
		neg := -0.5
		eq(t, contextUsageLine(&goalContextUsage{tokens: &neg, contextWindow: 1000}), "Context snapshot: unavailable", "-0.5")
		zero := 0.0
		eq(t, contextUsageLine(&goalContextUsage{tokens: &zero, contextWindow: 1000}), "Context snapshot: 0/1000 tokens (0%)", "0")
		eq(t, contextUsageLine(&goalContextUsage{contextWindow: 1000}), "Context snapshot: unavailable", "unknown tokens")
	})
}

// ---- goal_runtime.go ----

func TestMutantsARuntime(t *testing.T) {
	t.Run("clearing the continuation state forgets the queued marker", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(3)})
		mxaClaimed(t, h)
		id := mxaGoalID(h)
		eq(t, h.core.runtime.continuationQueuedFor != nil, true, "queued marker after the dispatch")
		eq(t, h.core.runtime.continuationPendingFor(id), true, "pending")
		h.core.runtime.clearContinuationState(false)
		eq(t, h.core.runtime.continuationQueuedFor == nil, true, "queued marker")
		eq(t, h.core.runtime.continuationPendingFor(id), false, "pending")
	})

	t.Run("the continuation timer waits 50 ms unless the host is idle with nothing pending", func(t *testing.T) {
		for _, c := range []struct {
			name          string
			idle, pending bool
			due           int64
		}{{"idle", true, false, 0}, {"busy", false, false, 50}, {"pending", true, true, 50}, {"busy and pending", false, true, 50}} {
			h := newSchedFx(t, fxOpts{})
			h.mock()
			h.h.idle = func() bool { return c.idle }
			h.h.pending = func() bool { return c.pending }
			h.core.runtime.queueContinuation(hostRuntimeCtx{h.h}, h.goal(), true)
			var dues []int64
			for _, tm := range h.clock.timers {
				dues = append(dues, tm.due)
			}
			eq(t, dues, []int64{c.due}, c.name)
		}
	})

	t.Run("stale turn-stop markers are dropped by the turn advance and by the reader", func(t *testing.T) {
		r := newGoalRuntime(goalRuntimeHooks{})
		r.markTurnStopped("g1")
		eq(t, r.turnStoppedFor != nil, true, "marked")
		r.advanceTurn()
		eq(t, r.turnStoppedFor == nil, true, "the advance drops the marker")
		r.markTurnStopped("g2")
		eq(t, *r.currentTurnStoppedGoalId(), "g2", "current")
		r.turnSeq += 3 // a marker of an earlier turn reaching the reader
		eq(t, r.currentTurnStoppedGoalId() == nil, true, "the reader drops a stale marker")
		eq(t, r.turnStoppedFor == nil, true, "and forgets it")
	})
}

// ---- prompts/goal-prompts.ts ----

func mxaTaskGoal(t *testing.T, id string, tasksJSON string, extra ...any) *jsObject {
	t.Helper()
	g := createGoal("Objective of "+id, true, false, false, 1)
	g.set("id", id)
	v, err := parseJSON([]byte(`{"proposedAt":"2026-01-01T00:00:00.000Z","blockCompletion":false,"tasks":` + tasksJSON + `}`))
	if err != nil {
		t.Fatal(err)
	}
	g.set("taskList", v)
	for i := 0; i+1 < len(extra); i += 2 {
		g.set(extra[i].(string), extra[i+1])
	}
	return g
}

func TestMutantsAPrompts(t *testing.T) {
	t.Run("the checkpoint trigger is bounded at 160 UTF-16 units", func(t *testing.T) {
		base := utf16Len(checkpointTriggerPrompt(""))
		ok := strings.Repeat("a", 160-base)
		eq(t, utf16Len(checkpointTriggerPrompt(ok)), 160, "length at the bound")
		var msg any
		func() {
			defer func() { msg = recover() }()
			checkpointTriggerPrompt(ok + "a")
		}()
		eq(t, msg, any("checkpoint trigger content is 161 chars; bound is 160"), "panic")
	})

	t.Run("the goal id is XML-escaped in the trigger", func(t *testing.T) {
		eq(t, checkpointTriggerPrompt(`a&b"c<d>e`), `<pi_goal_continuation goal_id="a&amp;b&quot;c&lt;d&gt;e" kind="checkpoint" v="2"/>`, "trigger")
		eq(t, escapeXmlAttribute("&lt;"), "&amp;lt;", "ampersand first")
	})

	t.Run("at most three pending tasks are rendered, the rest counted", func(t *testing.T) {
		g := mxaTaskGoal(t, "cap", `[{"id":"t1","title":"Task 1","status":"pending"},{"id":"t2","title":"Task 2","status":"pending"},{"id":"t3","title":"Task 3","status":"pending"},{"id":"t4","title":"Task 4","status":"pending"},{"id":"t5","title":"Task 5","status":"pending"}]`)
		eq(t, taskListBlock(g, nil), strings.Join([]string{
			"[TASK LIST — 0/5 tasks complete]",
			"[ ] t1: Task 1", "[ ] t2: Task 2", "[ ] t3: Task 3",
			`  2 additional pending tasks omitted; retrieve with get_goal(section="tasks").`,
		}, "\n"), "block")
	})

	t.Run("the header names skipped tasks only when there are some", func(t *testing.T) {
		g := mxaTaskGoal(t, "skip0", `[{"id":"t1","title":"One","status":"pending"},{"id":"t2","title":"Two","status":"complete"}]`)
		eq(t, strings.Split(taskListBlock(g, nil), "\n")[0], "[TASK LIST — 1/2 tasks complete]", "no skipped")
		g = mxaTaskGoal(t, "skip1", `[{"id":"t1","title":"One","status":"pending"},{"id":"t2","title":"Two","status":"skipped","skipReason":"no"}]`)
		eq(t, strings.Split(taskListBlock(g, nil), "\n")[0], "[TASK LIST — 0/2 tasks complete (1 skipped)]", "one skipped")
	})

	t.Run("a task title is cut at 180 characters with a pointer to get_goal", func(t *testing.T) {
		g := mxaTaskGoal(t, "title", `[{"id":"t1","title":"`+strings.Repeat("x", 200)+`","status":"pending"}]`)
		eq(t, strings.Split(taskListBlock(g, nil), "\n")[1], "[ ] t1: "+strings.Repeat("x", 180)+`… [more: get_goal(section="tasks")]`, "line")
	})

	t.Run("the current task is not counted as hidden", func(t *testing.T) {
		g := mxaTaskGoal(t, "cur1", `[{"id":"t1","title":"Only","status":"pending"}]`, "currentTaskId", "t1")
		eq(t, taskListBlock(g, nil), "[TASK LIST — 0/1 tasks complete]\n  Current: t1 · Only", "single current task")
		g = mxaTaskGoal(t, "cur2", `[{"id":"t1","title":"One","status":"pending"},{"id":"t2","title":"Two","status":"pending"}]`, "currentTaskId", "t1")
		eq(t, taskListBlock(g, nil), "[TASK LIST — 0/2 tasks complete]\n  Current: t1 · One\n[ ] t2: Two", "current and one more")
	})

	t.Run("a spent run allowance drops the continuation policy", func(t *testing.T) {
		g := createGoal("Obj", true, false, false, 1)
		g.set("id", "policy-goal")
		zero := 0.0
		off := buildGoalPrompt(g, &goalSettings{maxAutonomousRuns: &zero})
		eq(t, strings.Contains(off, "Continue pursuing the goal automatically"), false, "no continue line at zero")
		eq(t, strings.Contains(off, "End execution with update_goal"), false, "no declaration line at zero")
		on := buildGoalPrompt(g, &goalSettings{})
		eq(t, strings.Contains(on, "Continue pursuing the goal automatically"), true, "continue line")
		strict := buildGoalPrompt(g, &goalSettings{strictExecutionContract: true})
		eq(t, strings.Contains(strict, "End execution with update_goal"), true, "declaration line")
	})

	t.Run("the memoized prompt follows the run allowance", func(t *testing.T) {
		g := createGoal("Obj", true, false, false, 1)
		g.set("id", "memo-goal-runs")
		zero, five := 0.0, 5.0
		first := goalPrompt(g, &goalSettings{maxAutonomousRuns: &five, showAutonomousRuns: true})
		eq(t, strings.Contains(first, "Continue pursuing the goal automatically"), true, "allowance 5")
		second := goalPrompt(g, &goalSettings{maxAutonomousRuns: &zero, showAutonomousRuns: true})
		eq(t, strings.Contains(second, "Continue pursuing the goal automatically"), false, "allowance 0 is not served from the memo")
		eq(t, strings.Contains(second, "runs=0."), true, "limits line")
	})

	t.Run("the state and counters are joined with newlines", func(t *testing.T) {
		g := createGoal("Obj", true, false, false, 1)
		g.set("id", "join-goal")
		parts := goalPromptParts(g, &goalSettings{}, nil)
		eq(t, strings.HasSuffix(parts.state, "\nLimits: lifetime tokens=none; runs=unlimited.\nUsage spans goal turns, not context. Latest snapshot supersedes earlier snapshots."), true, "state tail")
		eq(t, strings.Count(parts.state, "\n") > 10, true, "state is multi-line")
		eq(t, parts.counters, "Goal snapshot: none\nContext snapshot: unavailable\n", "counters")
	})

	t.Run("an edited-objective prompt is truncated at 10000 UTF-16 units", func(t *testing.T) {
		g := createGoal("Obj", true, false, false, 1)
		g.set("id", strings.Repeat("i", 12000))
		p := objectiveEditedPrompt(g)
		const suffix = "\n…[prompt truncated]"
		eq(t, strings.HasSuffix(p, suffix), true, "suffix")
		eq(t, utf16Len(p), 10000+utf16Len(suffix), "length")
		short := createGoal("Obj", true, false, false, 1)
		short.set("id", "short-id")
		eq(t, strings.Contains(objectiveEditedPrompt(short), "prompt truncated"), false, "a short prompt is whole")
	})

	t.Run("the stale-checkpoint prompt says when no goal is current", func(t *testing.T) {
		eq(t, strings.Contains(staleContinuationPrompt("old", nil), "\nCurrent goal: none\n"), true, "none")
		g := createGoal("Current objective", true, false, false, 1)
		g.set("id", "cur")
		eq(t, strings.Contains(staleContinuationPrompt("old", g), "\nCurrent goal: cur (running) - Current objective\n"), true, "current")
	})

	t.Run("usage reports minutes and the remaining seconds", func(t *testing.T) {
		g := createGoal("Obj", true, false, false, 1)
		u := newObject()
		u.set("tokensUsed", 10.0)
		u.set("activeSeconds", 125.0)
		g.set("usage", u)
		eq(t, formatUsage(g), "2m5s · 10 tokens", "usage")
		u.set("activeSeconds", 60.0)
		eq(t, formatUsage(g), "1m0s · 10 tokens", "whole minute")
	})
}

// ---- goal-compaction.ts ----

func TestMutantsACompaction(t *testing.T) {
	events := func(typ string, id string, n int) []*jsObject {
		var out []*jsObject
		for i := 0; i < n; i++ {
			out = append(out, ledgerEvent(typ, "goalId", id, "reason", "r"+jsNumber(float64(i)), "at", "2026-01-01T00:00:0"+jsNumber(float64(i))+".000Z"))
		}
		return out
	}
	mkGoal := func(id, status string) *jsObject {
		g := createGoal("Goal "+id, true, false, false, 1)
		g.set("id", id)
		g.set("status", status)
		return g
	}

	t.Run("a goal summary lists the five latest events", func(t *testing.T) {
		g := mkGoal("g1", "active")
		out := buildGoalCompactSummary(g, events("goal_paused", "g1", 7), nil)
		lines := strings.Split(out, "\n")
		var recent []string
		for _, l := range lines {
			if strings.HasPrefix(l, "    - paused:") {
				recent = append(recent, l)
			}
		}
		eq(t, recent, []string{"    - paused: r2", "    - paused: r3", "    - paused: r4", "    - paused: r5", "    - paused: r6"}, "events")
	})

	t.Run("other open goals are capped at 20 by default", func(t *testing.T) {
		pool := newPool()
		for i := 0; i < 25; i++ {
			id := "goal-" + jsNumber(float64(100+i))
			pool.set(id, mkGoal(id, "active"))
		}
		out := buildCompactionSummary(compactionSummaryArgs{goalsById: pool})
		eq(t, strings.Contains(out, "[OTHER OPEN GOALS — 25 total]"), true, "header")
		eq(t, strings.Count(out, "\n- goal-"), 20, "listed")
		eq(t, strings.Contains(out, "\n... and 5 more\n"), true, "remainder")
	})

	t.Run("complete goals are not open, and terminal goals suppress the no-goals notice", func(t *testing.T) {
		pool := newPool()
		pool.set("done", mkGoal("done", "complete"))
		out := buildCompactionSummary(compactionSummaryArgs{goalsById: pool})
		eq(t, strings.Contains(out, "[NO GOALS]\nNo open or terminal goals recorded in this session."), true, "only a complete goal")
		eq(t, strings.Contains(out, "[OTHER OPEN GOALS"), false, "no open goals")
		pool.set("live", mkGoal("live", "active"))
		out = buildCompactionSummary(compactionSummaryArgs{goalsById: pool})
		eq(t, strings.Contains(out, "[OTHER OPEN GOALS — 1 total]\n- live — "), true, "one open goal")
		eq(t, strings.Contains(out, "- done"), false, "the complete goal is not listed")

		term := []*jsObject{ledgerEvent("goal_completed", "goalId", "gone", "at", "2026-01-01T00:00:00.000Z")}
		out = buildCompactionSummary(compactionSummaryArgs{goalsById: newPool(), ledgerEvents: term})
		eq(t, strings.Contains(out, "[TERMINAL GOALS — 1 completed or aborted]\n- gone — completed at 2026-01-01T00:00:00.000Z"), true, "terminal")
		eq(t, strings.Contains(out, "[NO GOALS]"), false, "a terminal goal is a goal")
	})

	t.Run("the post-compaction delta cuts the auditor finding at 300 units", func(t *testing.T) {
		g := mkGoal("g1", "active")
		ev := []*jsObject{ledgerEvent("audit_result", "goalId", "g1", "verdict", "disapproved", "report", strings.Repeat("r", 400), "at", "2026-01-01T00:00:00.000Z")}
		out := buildPostCompactionGoalDelta(postCompactionDeltaArgs{goal: g, ledgerEvents: ev, otherOpenCount: 2})
		eq(t, strings.Contains(out, "\nLatest unresolved auditor finding: "+strings.Repeat("r", 300)+"\nOther open goals: 2\n"), true, "finding")
	})

	t.Run("the post-compaction delta lists five events with their time of day", func(t *testing.T) {
		g := mkGoal("g1", "active")
		out := buildPostCompactionGoalDelta(postCompactionDeltaArgs{goal: g, ledgerEvents: events("goal_paused", "g1", 7)})
		eq(t, strings.Count(out, "\n  00:00:0"), 5, "events")
		eq(t, strings.Contains(out, "\n  00:00:02 goal_paused\n"), true, "oldest listed")
		eq(t, strings.Contains(out, "\n  00:00:01 goal_paused"), false, "older dropped")
	})
}

// ---- goal-live-retention.ts ----

func mxaMsg(role, text string) any {
	m := newObject()
	m.set("role", role)
	m.set("content", text)
	return m
}

func mxaLiveContents(r *liveTailRetention) []string { c, _ := r.contents("k"); return c }

func TestMutantsALiveRetention(t *testing.T) {
	fresh := func(state, counters string) *freshLiveTails {
		return &freshLiveTails{state: state, counters: &counters}
	}

	t.Run("at most 32 tails are retained: the 33rd resets the session", func(t *testing.T) {
		r := newLiveTailRetention()
		base := []any{mxaMsg("user", "history")}
		for i := 0; i < 40; i++ {
			r.apply("k", base, fresh("state", "c"+jsNumber(float64(i))))
			want := i%31 + 2
			if i >= 31 {
				want = (i-31)%31 + 2
			}
			eq(t, len(mxaLiveContents(r)), want, "tails after request "+jsNumber(float64(i)))
		}
	})

	t.Run("a shorter history resets the retention", func(t *testing.T) {
		r := newLiveTailRetention()
		r.apply("k", []any{mxaMsg("user", "one"), mxaMsg("user", "two")}, fresh("state", "c1"))
		res := r.apply("k", []any{mxaMsg("user", "one")}, fresh("state", "c1"))
		eq(t, len(res.messages), 3, "messages")
		eq(t, mxaLiveContents(r), []string{"state", "c1"}, "tails")
		m, _ := res.messages[0].(*jsObject)
		eq(t, gstr(m, "content"), "one", "history first")
		eq(t, gstr(res.messages[1].(*jsObject), "content"), "state", "tails after the history")
	})

	t.Run("the state block does not count against the counter budget", func(t *testing.T) {
		r := newLiveTailRetention()
		base := []any{mxaMsg("user", "history")}
		state := strings.Repeat("s", 5000)
		r.apply("k", base, fresh(state, "c1"))
		r.apply("k", base, fresh(state, "c2"))
		eq(t, mxaLiveContents(r), []string{state, "c1", "c2"}, "tails")
	})

	t.Run("an unchanged request never overflows the counter budget", func(t *testing.T) {
		r := newLiveTailRetention()
		counters := strings.Repeat("c", 3000)
		r.apply("k", []any{mxaMsg("user", "one")}, fresh("state", counters))
		res := r.apply("k", []any{mxaMsg("user", "one"), mxaMsg("user", "two")}, fresh("state", counters))
		var order []string
		for _, m := range res.messages {
			order = append(order, gstr(m.(*jsObject), "content"))
		}
		eq(t, order, []string{"one", "state", counters, "two"}, "the retained tails stay at their anchor")
	})

	t.Run("live messages are hidden from the display", func(t *testing.T) {
		m := makeLiveMessage("text")
		v, _ := m.flag("display")
		eq(t, v, false, "display")
		eq(t, gstr(m, "customType"), liveContextType, "type")
		eq(t, gstr(m, "role"), "custom", "role")
	})
}

// ---- goal-prompt-cache.ts ----

func mxaPayload(t *testing.T, raw string) any {
	t.Helper()
	v, err := parseJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMutantsAPromptCache(t *testing.T) {
	t.Run("an empty live block is no live block", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":"hello"},{"role":"user","content":[{"type":"text","text":"","cache_control":{"type":"ephemeral"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{""}) == nil, true, "result")
	})

	t.Run("only a user message's tail is relocated", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":"history"},{"role":"assistant","content":[{"type":"text","text":"LIVE","cache_control":{"type":"ephemeral"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{"LIVE"}) == nil, true, "result")
		eq(t, strings.Contains(marshalJSON(p, ""), `"history","cache_control"`), false, "history untouched")
	})

	t.Run("a tail that is not live text is left alone", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":"history"},{"role":"user","content":[{"type":"text","text":"someone else's","cache_control":{"type":"ephemeral"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{"LIVE"}) == nil, true, "result")
	})

	t.Run("a live tail's breakpoint moves onto history", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":"history"},{"role":"user","content":[{"type":"text","text":"LIVE","cache_control":{"type":"ephemeral"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{"LIVE"}) != nil, true, "result")
		eq(t, marshalJSON(p, ""), `{"messages":[{"role":"user","content":[{"type":"text","text":"history","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"LIVE"}]}]}`, "payload")
	})

	t.Run("a bedrock cache point already behind the history block is not duplicated", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":[{"text":"history"},{"cachePoint":{"type":"default"}}]},{"role":"user","content":[{"text":"LIVE"},{"cachePoint":{"type":"default"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{"LIVE"}) != nil, true, "result")
		eq(t, marshalJSON(p, ""), `{"messages":[{"role":"user","content":[{"text":"history"},{"cachePoint":{"type":"default"}}]},{"role":"user","content":[{"text":"LIVE"}]}]}`, "payload")
	})

	t.Run("a bedrock cache point moves behind the history block", func(t *testing.T) {
		p := mxaPayload(t, `{"messages":[{"role":"user","content":[{"text":"history"}]},{"role":"user","content":[{"text":"LIVE"},{"cachePoint":{"type":"default"}}]}]}`)
		eq(t, cacheGoalHistory(p, []string{"LIVE"}) != nil, true, "result")
		eq(t, marshalJSON(p, ""), `{"messages":[{"role":"user","content":[{"text":"history"},{"cachePoint":{"type":"default"}}]},{"role":"user","content":[{"text":"LIVE"}]}]}`, "payload")
	})
}

// ---- goal.go ----

func TestMutantsAGoalRenderers(t *testing.T) {
	t.Run("the extension registers both message renderers, the event one first", func(t *testing.T) {
		reg := registrationOf(t, newGoalExtension(goalDeps{}))
		eq(t, reg.Renderers, []string{goalEventEntry, goalAuditEntry}, "renderers")
	})

	t.Run("the audit renderer labels the phase of the message's details", func(t *testing.T) {
		for _, phase := range []string{"approved", "rejected", "skipped"} {
			lines := renderGoalAuditEventMessage(map[string]any{"details": map[string]any{"phase": phase}}, false, 80, wgTestTheme)
			joined := strings.Join(lines, "\n")
			eq(t, strings.Contains(joined, "Goal audit "+phase), true, phase)
			eq(t, strings.Contains(joined, "Goal audit "+phase+"."), true, phase+" body")
		}
		lines := renderGoalAuditEventMessage(map[string]any{"details": map[string]any{"phase": "weird"}, "content": "custom body"}, false, 80, wgTestTheme)
		joined := strings.Join(lines, "\n")
		eq(t, strings.Contains(joined, "Goal audit started"), true, "unknown phase")
		eq(t, strings.Contains(joined, "custom body"), true, "string content wins")
	})
}
