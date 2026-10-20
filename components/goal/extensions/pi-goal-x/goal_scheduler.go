package pi_goal_x

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"time"
)

// ported from goal-scheduler.ts. Scheduling intent is durable; timers only arrange an opportunity to claim it.
//
// A TS `throw` inside a mutation callback is a panic of goalThrow, caught where the original catches (tryGoal): the service's
// mutate callbacks cannot return an error. The scheduler works on the core's host (core.h) where the original takes a `ctx`
// argument: a host that changes its answers (busy, pending messages) changes them for the scheduler too.

// goalThrow is a thrown Error: the message is Error.message.
type goalThrow string

func throwGoal(msg string) { panic(goalThrow(msg)) }

// tryGoal runs fn and returns the goalThrow it threw as an error (other panics propagate).
func tryGoal(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if t, ok := r.(goalThrow); ok {
				err = errors.New(string(t))
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}

// jsErrorString is String(error) for an Error.
func jsErrorString(err error) string { return "Error: " + err.Error() }

// goalAgentResult is the AgentToolResult the scheduler and the core hand to the tool layer: one text block, the details object
// (nil = undefined) and the terminate flag.
type goalAgentResult struct {
	text      string
	details   *jsObject
	terminate bool
}

// goalPolling is GoalContinuation's polling: the tool layer decodes the numbers (a non-number is NaN).
type goalPolling struct{ intervalSeconds, maxChecks float64 }

// goalContinuation is GoalContinuation: kind "ready", or kind "wait" with a reason (nil: not a string), an ISO deadline, an
// optional wait_id ("" = undefined) and optional polling.
type goalContinuation struct {
	kind     string
	reason   *string
	deadline string
	waitID   string
	polling  *goalPolling
}

var waitDeadlineZone = regexp.MustCompile(`T.*(?:Z|[+-]\d\d:\d\d)$`)

// jsDateParse is Date.parse for the ISO forms (YYYY-MM-DD[THH:mm[:ss[.sss]]][Z|±HH:mm]): NaN when it cannot be read. A date-time
// without an offset is local time, a date alone is UTC.
var jsDateISO = regexp.MustCompile(`^([+-]\d{6}|\d{4})(?:-(\d\d)(?:-(\d\d))?)?(?:T(\d\d):(\d\d)(?::(\d\d)(?:[.,](\d+))?)?)?(Z|[+-]\d\d:\d\d)?$`)

func jsDateParse(s string) float64 {
	m := jsDateISO.FindStringSubmatch(s)
	if m == nil {
		return math.NaN()
	}
	atoi := func(v string, def int) int {
		if v == "" {
			return def
		}
		n, _ := strconv.Atoi(v)
		return n
	}
	year := atoi(m[1], 0)
	if m[1][0] == '-' && year == 0 {
		return math.NaN() // -000000 is invalid
	}
	month, day := atoi(m[2], 1), atoi(m[3], 1)
	hour, min, sec := atoi(m[4], 0), atoi(m[5], 0), atoi(m[6], 0)
	ms := 0
	if m[7] != "" {
		frac := (m[7] + "000")[:3]
		ms, _ = strconv.Atoi(frac)
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 24 || min > 59 || sec > 59 || (hour == 24 && (min != 0 || sec != 0 || ms != 0)) {
		return math.NaN()
	}
	loc := time.UTC
	switch tz := m[8]; {
	case tz == "Z":
	case tz != "":
		sign := 1
		if tz[0] == '-' {
			sign = -1
		}
		oh, _ := strconv.Atoi(tz[1:3])
		om, _ := strconv.Atoi(tz[4:6])
		if oh > 23 || om > 59 {
			return math.NaN()
		}
		loc = time.FixedZone("", sign*(oh*3600+om*60))
	case m[4] != "":
		loc = localZone
	}
	midnightNext := hour == 24 // T24:00:00 is the start of the next day
	if midnightNext {
		hour = 0
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, ms*1e6, loc)
	if t.Day() != day { // 02-30 style overflow: JS rejects days past the month's end
		return math.NaN()
	}
	if midnightNext {
		t = t.AddDate(0, 0, 1)
	}
	return float64(t.UnixMilli())
}

// goalScheduler is class GoalScheduler.
type goalScheduler struct {
	c               *core
	timer           *time.Timer
	inRun           bool
	armedGeneration string // "" is undefined
	declared        bool
	runGoalID       string // "" is undefined
	denied          bool
	unsubscribe     func()
	attached        bool
	// dispatch hands an armed goal to the runtime's continuation queue (runtime.queueContinuation(ctx, goal, true)).
	dispatch func(goal *jsObject)
}

func newGoalSchedulerFor(c *core) *goalScheduler {
	s := &goalScheduler{c: c}
	s.dispatch = func(goal *jsObject) { c.runtime.queueContinuation(hostRuntimeCtx{c.h}, goal, true) }
	return s
}

func (s *goalScheduler) owner() string {
	if id := s.c.h.SessionID(); id != "" {
		return id
	}
	return "unknown-session"
}

func (s *goalScheduler) limit() *float64 { return loadGoalSettings(s.c.h.Cwd()).maxAutonomousRuns }

func (s *goalScheduler) strict(sch *jsObject) bool {
	return loadGoalSettings(s.c.h.Cwd()).strictExecutionContract || sch.has("wait")
}

func (s *goalScheduler) implicitReady(sch *jsObject) {
	sch.set("phase", "ready")
	sch.set("dispatch", undef{})
	sch.set("generation", newUUID())
	d := newObject()
	d.set("kind", "ready")
	d.set("purpose", "ready")
	sch.set("decision", d)
}

func (s *goalScheduler) available(sch *jsObject) bool {
	limit := s.limit()
	used, _ := sch.num("used")
	return limit == nil || used < *limit
}

// state is the goal's scheduler (a fresh one for a goal without), thrown when it belongs to another session or was interrupted.
func (s *goalScheduler) state(goal *jsObject) *jsObject {
	sch := goal.obj("scheduler")
	if sch == nil || !goal.has("scheduler") {
		sch = newGoalScheduler(s.owner())
	}
	owner, _ := sch.str("owner")
	phase, _ := sch.str("phase")
	if owner != s.owner() || phase == "interrupted" {
		throwGoal("Goal scheduling belongs to another session or was interrupted. Use /goal-resume to take ownership.")
	}
	return sch
}

// write applies change to the focused goal through the service, after the pending turn buffer reached disk.
func (s *goalScheduler) write(change func(g *jsObject) *jsObject) *jsObject {
	c := s.c
	flushErr, err := c.goalService.flushForAudit(c.st)
	if err != nil {
		throwGoal(err.Error())
	}
	if flushErr != "" || c.goalService.isTurnBuffered() {
		if flushErr == "" {
			flushErr = "Goal changes have not reached disk."
		}
		throwGoal(flushErr)
	}
	focused := c.state()
	if focused == nil {
		throwGoal("No focused goal.")
	}
	id := gstr(focused, "id")
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		expectedGoalId: &id,
		mutate:         change,
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			if gstr(written, "status") != "paused" {
				return nil, nil
			}
			reason := "Scheduling paused"
			if v, ok := written.vals["pauseReason"]; ok && v != nil {
				if _, u := v.(undef); !u {
					reason = runtimeString(v)
				}
			}
			return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(written, "id"), "reason", reason, "source", "agent", "at", nowIso())}, nil
		},
	})
	if err != nil {
		throwGoal(err.Error())
	}
	if !result.ok {
		throwGoal(result.message)
	}
	c.updateUI()
	return result.goal
}

func (s *goalScheduler) update(change func(sch, g *jsObject)) *jsObject {
	return s.write(func(g *jsObject) *jsObject {
		sch := s.state(g)
		change(sch, g)
		return goalWith(g, "scheduler", sch)
	})
}

// safe runs fn, stopping the goal's scheduling with a warning when it throws.
func (s *goalScheduler) safe(fn func()) {
	if err := tryGoal(fn); err != nil {
		s.cancelTimer()
		s.c.runtime.clearContinuationState(true)
		s.c.h.Notify("Goal scheduling stopped: "+err.Error(), "warning")
	}
}

func (s *goalScheduler) cancelTimer() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = nil
	s.armedGeneration = ""
}

func (s *goalScheduler) later(delay float64, fn func()) {
	s.cancelTimer()
	d := math.Min(2147483647, math.Max(0, delay))
	s.timer = s.c.after(time.Duration(d)*time.Millisecond, func() {
		s.timer = nil
		s.safe(fn)
	})
}

func (s *goalScheduler) pause(reason string) {
	s.cancelTimer()
	s.c.runtime.clearContinuationState(true)
	s.write(func(g *jsObject) *jsObject {
		if sch := g.obj("scheduler"); sch != nil && g.has("scheduler") {
			if owner, _ := sch.str("owner"); owner != s.owner() {
				throwGoal("Scheduling ownership changed; no pause was applied.")
			}
		}
		n := goalWith(g, "status", "paused", "autoContinue", false, "stopReason", "agent", "pauseReason", reason)
		if sch := g.obj("scheduler"); sch != nil && g.has("scheduler") {
			ns := sch.clone()
			ns.set("generation", newUUID())
			ns.set("phase", "idle")
			ns.set("decision", undef{})
			ns.set("dispatch", undef{})
			ns.set("wait", undef{})
			n.set("scheduler", ns)
		} else {
			n.set("scheduler", undef{})
		}
		return n
	})
	s.c.clearActiveAccounting()
	s.c.h.Notify(reason, "warning")
}

func (s *goalScheduler) allowanceReason() string {
	if l := s.limit(); l != nil && *l == 0 {
		return "Automatic continuation disabled by maxAutonomousRuns=0. Change the setting in /goal-settings, then use /goal-resume."
	}
	return "Autonomous-run allowance exhausted. Increase or remove maxAutonomousRuns, or use /goal-resume to renew."
}

// attach binds once per live session. The wake event is deliberately not a triggerTurn message.
func (s *goalScheduler) attach() {
	s.attached = true
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	s.unsubscribe = s.c.h.OnEvent("pi-goal:wake", func(event any) {
		switch event.(type) {
		case *jsObject, map[string]any:
		default:
			return
		}
		goalID, _ := sessionMessageField(event, "goalId")
		token, _ := sessionMessageField(event, "waitToken")
		s.signal(goalID, token)
	})
}

func (s *goalScheduler) shutdown() {
	s.cancelTimer()
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
	s.unsubscribe = nil
	s.attached = false
}

func (s *goalScheduler) restore() {
	s.attach()
	s.inRun, s.declared, s.runGoalID = false, false, ""
	s.cancelTimer()
	s.safe(func() {
		s.c.reconcileFocusedGoalFromDisk(false)
		goal := s.c.state()
		if goal == nil || gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") || !goal.has("scheduler") {
			return
		}
		sch := goal.obj("scheduler")
		if owner, _ := sch.str("owner"); owner != s.owner() {
			s.c.h.Notify("Goal owned by another session. Use /goal-resume to take ownership.", "info")
			return
		}
		if phase, _ := sch.str("phase"); inList(phase, "claimed", "running", "interrupted") {
			s.pause("Previous autonomous execution was interrupted. Use /goal-resume; its dispatch will not be replayed.")
			return
		}
		s.schedule()
	})
}

// resume is the explicit user resume: only it resets spent allowance, including while already active or waiting.
func (s *goalScheduler) resume() bool {
	invalidateGoalSettingsCache()
	if l := s.limit(); l != nil && *l == 0 {
		s.c.h.Notify(s.allowanceReason(), "warning")
		return false
	}
	err := tryGoal(func() {
		s.cancelTimer()
		s.c.runtime.clearContinuationState(true)
		s.write(func(g *jsObject) *jsObject {
			if gstr(g, "status") == "complete" || budgetReached(g) {
				throwGoal("A completed or token-budget-limited goal cannot resume.")
			}
			sch := newGoalScheduler(s.owner())
			sch.set("phase", "ready")
			d := newObject()
			d.set("kind", "ready")
			d.set("purpose", "kickoff")
			sch.set("decision", d)
			return goalWith(g, "status", "active", "autoContinue", true, "stopReason", undef{}, "pauseReason", undef{}, "pauseSuggestedAction", undef{}, "scheduler", sch)
		})
		// A resume during host work waits for settlement; another model turn still invalidates it through turn(), just like a
		// declaration.
		s.declared = s.inRun
		s.schedule()
	})
	if err != nil {
		s.c.h.Notify(jsErrorString(err), "warning")
		return false
	}
	return true
}

func (s *goalScheduler) kickoff() {
	if s.inRun {
		return // creating a goal within a user run needs no extra run
	}
	s.safe(func() {
		goal := s.c.state()
		if goal == nil || gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") {
			return
		}
		if l := s.limit(); l != nil && *l == 0 {
			s.update(func(*jsObject, *jsObject) {})
			s.c.h.Notify(s.allowanceReason(), "info")
			return
		}
		s.update(func(sch, _ *jsObject) {
			sch.set("phase", "ready")
			d := newObject()
			d.set("kind", "ready")
			d.set("purpose", "kickoff")
			sch.set("decision", d)
		})
		s.schedule()
	})
}

func schedulerFloat(o *jsObject, k string) (float64, bool) {
	if o == nil {
		return 0, false
	}
	if !o.has(k) {
		return 0, false
	}
	f, ok := o.num(k)
	return f, ok
}

func safeInt(f float64) bool { return isSafeInteger(f) }

// declare is the agent's continuation declaration (the update_goal continuation).
func (s *goalScheduler) declare(input goalContinuation) goalAgentResult {
	var result goalAgentResult
	err := tryGoal(func() {
		c := s.c
		g := c.state()
		if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") || s.denied {
			throwGoal("Scheduling requires an active auto-continue goal in this session.")
		}
		invalidateGoalSettingsCache() // an agent may have just configured the allowance
		goal := s.update(func(sch, _ *jsObject) {
			if !s.available(sch) {
				throwGoal(s.allowanceReason())
			}
			switch input.kind {
			case "ready":
				sch.set("phase", "ready")
				d := newObject()
				d.set("kind", "ready")
				d.set("purpose", "ready")
				sch.set("decision", d)
				sch.set("wait", undef{})
			case "wait":
				if !s.strict(sch) {
					throwGoal("New waits require strictExecutionContract=true in /goal-settings. This is a user preference; continue pursuing the goal without a wait unless the user opts in.")
				}
				if input.reason == nil || jsTrim(*input.reason) == "" || utf16Len(*input.reason) > 2000 {
					throwGoal("wait requires a nonempty reason (at most 2000 characters).")
				}
				deadline := jsDateParse(input.deadline)
				if !waitDeadlineZone.MatchString(input.deadline) || !safeInt(deadline) || deadline <= float64(clockMs()) {
					throwGoal("wait requires a future ISO deadline with a timezone.")
				}
				if w := sch.obj("wait"); w != nil && sch.has("wait") {
					wid, _ := w.str("id")
					wdl, _ := w.num("deadline")
					if input.waitID != wid || deadline != wdl {
						throwGoal("Re-declare the current wait_id with its original deadline; checks cannot be reset.")
					}
					if p := input.polling; p != nil {
						iv, ivOK := schedulerFloat(w, "intervalMs")
						rc, rcOK := schedulerFloat(w, "remainingChecks")
						if !ivOK || !rcOK || p.intervalSeconds*1000 != iv || p.maxChecks != rc {
							throwGoal("Existing wait polling cannot be reset; omit polling when re-declaring wait_id.")
						}
					}
					w.set("token", newUUID())
					w.set("signalled", false)
					if iv, ok := schedulerFloat(w, "intervalMs"); ok && iv != 0 {
						w.set("nextCheckAt", float64(clockMs())+iv)
					}
				} else {
					if input.waitID != "" {
						throwGoal("Unknown wait_id.")
					}
					p := input.polling
					if p != nil && (!safeInt(p.intervalSeconds) || p.intervalSeconds < 1 || p.intervalSeconds > 2147483 || !safeInt(p.maxChecks) || p.maxChecks < 1) {
						throwGoal("Polling requires positive whole interval_seconds (at most 2147483) and max_checks.")
					}
					w := newObject()
					w.set("id", newUUID())
					w.set("token", newUUID())
					w.set("reason", jsTrim(*input.reason))
					w.set("deadline", deadline)
					if p != nil {
						w.set("intervalMs", p.intervalSeconds*1000)
						w.set("remainingChecks", p.maxChecks)
						w.set("nextCheckAt", float64(clockMs())+p.intervalSeconds*1000)
					}
					sch.set("wait", w)
				}
				sch.set("phase", "waiting")
				d := newObject()
				d.set("kind", "wait")
				sch.set("decision", d)
			default:
				throwGoal("Unknown continuation kind.")
			}
			sch.set("dispatch", undef{})
			sch.set("generation", newUUID())
		})
		s.declared = true
		c.runtime.markTurnStopped(gstr(goal, "id"))
		sch := goal.obj("scheduler")
		var wait *jsObject
		if sch != nil {
			wait = sch.obj("wait")
			if !sch.has("wait") {
				wait = nil
			}
		}
		settings := loadGoalSettings(c.h.Cwd())
		text := "Scheduling decision saved. Stop this execution.\n" + schedulerSummary(sch, s.limit(), settings.showAutonomousRuns)
		details := newObject()
		details.set("goal", goal)
		if wait != nil {
			token, _ := wait.str("token")
			wid, _ := wait.str("id")
			text += "\nWake token: " + token + ". Register this token with the producer before completion; adapters must retain early results."
			details.set("wait_id", wid)
			details.set("waitToken", token)
		}
		result = goalAgentResult{text: text, details: details, terminate: true}
	})
	if err != nil {
		details := newObject()
		details.set("error", true)
		return goalAgentResult{text: "Scheduling decision NOT saved: " + err.Error(), details: details}
	}
	return result
}

// begin starts a logical run; one run can contain multiple agent_start events during native retry and compaction.
func (s *goalScheduler) begin() {
	c := s.c
	if s.inRun {
		c.setRunningGoalFromState()
		return
	}
	s.inRun, s.declared, s.denied = true, false, false
	s.runGoalID = ""
	if g := c.state(); g != nil {
		s.runGoalID = gstr(g, "id")
	}
	s.cancelTimer()
	c.runtime.clearContinuationTimer()
	c.setRunningGoalFromState()
}

func (s *goalScheduler) turn() {
	if !s.declared {
		return
	}
	s.declared = false
	s.safe(func() {
		s.update(func(sch, _ *jsObject) {
			sch.set("decision", undef{})
			sch.set("phase", "idle")
			sch.set("dispatch", undef{})
		})
	})
}

// message handles a message_start: a rejected checkpoint aborts the turn; genuine host work supersedes an old claim.
func (s *goalScheduler) message(raw any) {
	role, ok := sessionMessageField(raw, "role")
	if !ok || (role != any("user") && role != any("custom")) {
		return
	}
	custom, _ := sessionMessageField(raw, "customType")
	if custom == any(goalAuditEntry) {
		return
	}
	if custom == any(goalEventEntry) {
		details, _ := sessionMessageField(raw, "details")
		kind, _ := sessionMessageField(details, "kind")
		if kind != any("checkpoint") {
			return
		}
		err := tryGoal(func() {
			s.update(func(sch, g *jsObject) {
				version, _ := sessionMessageField(details, "version")
				goalID, _ := sessionMessageField(details, "goalId")
				generation, _ := sessionMessageField(details, "generation")
				dispatchID, _ := sessionMessageField(details, "dispatchId")
				phase, _ := sch.str("phase")
				disp := sch.obj("dispatch")
				var dispID any
				if disp != nil && sch.has("dispatch") {
					dispID = disp.vals["id"]
				}
				if version != any(3.0) || phase != "claimed" || any(gstr(g, "id")) != goalID || sch.vals["generation"] != generation || dispID != dispatchID || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
					throwGoal("Stale or unclaimed goal checkpoint.")
				}
				// Retain the admitted action as context; phase prevents reusing its authorization.
				sch.set("phase", "running")
			})
		})
		if err != nil {
			s.denied = true
			goalID, ok := sessionMessageField(details, "goalId")
			id := "invalid"
			if ok && goalID != nil {
				id = runtimeString(goalID)
			}
			s.c.runtime.setCheckpoint(&id)
			s.c.h.Abort()
			s.c.h.Notify("Goal checkpoint rejected: "+jsErrorString(err), "warning")
		}
		return
	}
	// Genuine host work supersedes an old scheduling claim, but never resets used.
	s.takeover()
}

func (s *goalScheduler) takeover() {
	s.cancelTimer()
	s.c.runtime.clearContinuationState(true)
	s.declared = false
	g := s.c.state()
	if g == nil {
		return
	}
	sch := g.obj("scheduler")
	if sch == nil || !g.has("scheduler") {
		return
	}
	if owner, _ := sch.str("owner"); owner != s.owner() {
		return
	}
	s.safe(func() {
		s.update(func(state, _ *jsObject) {
			state.set("generation", newUUID())
			state.set("phase", "idle")
			state.set("decision", undef{})
			state.set("dispatch", undef{})
			state.set("wait", undef{})
			state.set("repairUsed", false)
		})
	})
}

// settled ends a logical run; successful is false after an agent error.
func (s *goalScheduler) settled(successful bool) {
	s.inRun = false
	c := s.c
	if s.denied {
		return
	}
	if s.runGoalID != "" {
		if g := c.state(); g == nil || gstr(g, "id") != s.runGoalID {
			return
		}
	}
	invalidateGoalSettingsCache()
	s.safe(func() {
		goal := c.state()
		if goal == nil || gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") || !successful {
			return
		}
		if sch := goal.obj("scheduler"); s.declared && sch != nil && goal.has("scheduler") && sch.has("decision") {
			s.schedule()
			return
		}
		sch := s.state(goal)
		if !s.strict(sch) {
			s.update(func(state, _ *jsObject) { s.implicitReady(state) })
			s.schedule()
			return
		}
		if rep, _ := sch.flag("repairUsed"); rep || !s.available(sch) {
			if !s.available(sch) {
				s.pause(s.allowanceReason())
			} else {
				s.pause("No execution disposition after the contract-repair prompt. Use /goal-resume to continue.")
			}
			return
		}
		s.update(func(state, _ *jsObject) {
			state.set("phase", "ready")
			state.set("dispatch", undef{})
			d := newObject()
			d.set("kind", "ready")
			d.set("purpose", "repair")
			state.set("decision", d)
		})
		s.schedule()
	})
}

// recover schedules a recovery run after a network error.
func (s *goalScheduler) recover() {
	s.safe(func() {
		s.update(func(sch, _ *jsObject) {
			sch.set("phase", "ready")
			sch.set("dispatch", undef{})
			d := newObject()
			d.set("kind", "ready")
			d.set("purpose", "recovery")
			sch.set("decision", d)
		})
		s.schedule()
	})
}

// signal wakes a waiting goal whose wait token matches.
func (s *goalScheduler) signal(goalID, token any) {
	s.safe(func() {
		c := s.c
		c.reconcileFocusedGoalFromDisk(false)
		g := c.state()
		if g == nil || any(gstr(g, "id")) != goalID || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
			return
		}
		sch := g.obj("scheduler")
		if sch == nil || !g.has("scheduler") {
			return
		}
		if owner, _ := sch.str("owner"); owner != s.owner() {
			return
		}
		if phase, _ := sch.str("phase"); phase != "waiting" {
			return
		}
		w := sch.obj("wait")
		if w == nil || !sch.has("wait") || w.vals["token"] != token {
			return
		}
		if sg, _ := w.flag("signalled"); sg {
			return
		}
		s.update(func(current, _ *jsObject) {
			cw := current.obj("wait")
			phase, _ := current.str("phase")
			if cw == nil || !current.has("wait") || cw.vals["token"] != token || phase != "waiting" {
				throwGoal("Wait changed.")
			}
			cw.set("signalled", true)
		})
		if !s.inRun {
			s.schedule()
		}
	})
}

// schedule arms the next dispatch of a ready or waiting goal, or stops the goal when its allowance, budget or wait ran out.
func (s *goalScheduler) schedule() {
	if s.inRun {
		return
	}
	invalidateGoalSettingsCache()
	s.safe(func() {
		c := s.c
		c.reconcileFocusedGoalFromDisk(false)
		g := c.state()
		if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") || !g.has("scheduler") {
			return
		}
		sch := s.state(g)
		if phase, _ := sch.str("phase"); phase == "ready" && repairPending(sch) && !s.strict(sch) {
			sch = s.update(func(state, _ *jsObject) { s.implicitReady(state) }).obj("scheduler")
		}
		phase, _ := sch.str("phase")
		if phase != "ready" && phase != "waiting" {
			return
		}
		if budgetReached(g) {
			s.pause("Goal token budget exhausted.")
			return
		}
		if !s.available(sch) {
			s.pause(s.allowanceReason())
			return
		}
		// Recovery and repair retain the wait while changing phase to ready.
		w := sch.obj("wait")
		if !sch.has("wait") {
			w = nil
		}
		now := float64(clockMs())
		if w != nil {
			if dl, _ := w.num("deadline"); now >= dl {
				s.pause("Wait deadline reached.")
				return
			}
		}
		if phase == "waiting" && w != nil {
			c.clearActiveAccounting()
			signalled, _ := w.flag("signalled")
			rc, hasRC := schedulerFloat(w, "remainingChecks")
			nextAt, hasNext := schedulerFloat(w, "nextCheckAt")
			if !signalled && hasRC && rc == 0 {
				s.pause("Wait check allowance exhausted.")
				return
			}
			if !signalled && (!hasNext || nextAt > now) {
				dl, _ := w.num("deadline")
				due := dl
				if hasNext && nextAt < due {
					due = nextAt
				}
				s.later(due-now, func() { s.schedule() })
				return
			}
		}
		s.cancelTimer()
		// Runtime readiness polling cannot authorize: claim() is called again at delivery.
		s.armedGeneration, _ = sch.str("generation")
		s.dispatch(g)
	})
}

func repairPending(s *jsObject) bool {
	d := s.obj("decision")
	if d == nil {
		return false
	}
	kind, _ := d.str("kind")
	purpose, _ := d.str("purpose")
	return kind == "ready" && purpose == "repair"
}

// claim authorizes one dispatch: it spends the allowance and returns {generation, dispatchId}, or nil to refuse.
func (s *goalScheduler) claim() *jsObject {
	generation := s.armedGeneration
	if generation == "" {
		return nil
	}
	s.armedGeneration = ""
	invalidateGoalSettingsCache()
	var result *jsObject
	err := tryGoal(func() {
		s.update(func(sch, g *jsObject) {
			if gen, _ := sch.str("generation"); gen != generation {
				throwGoal("Scheduling generation changed.")
			}
			if gstr(g, "status") != "active" || !gflag(g, "autoContinue") || budgetReached(g) || !s.available(sch) {
				throwGoal(s.allowanceReason())
			}
			w := sch.obj("wait")
			if !sch.has("wait") {
				w = nil
			}
			if w != nil {
				if dl, _ := w.num("deadline"); float64(clockMs()) >= dl {
					throwGoal("Wait deadline reached.")
				}
			}
			if phase, _ := sch.str("phase"); phase == "ready" && repairPending(sch) && !s.strict(sch) {
				s.implicitReady(sch)
			}
			kind := ""
			phase, _ := sch.str("phase")
			if d := sch.obj("decision"); phase == "ready" && d != nil && sch.has("decision") {
				if k, _ := d.str("kind"); k == "ready" {
					kind, _ = d.str("purpose")
				}
			}
			if phase == "waiting" && w != nil {
				nextAt, hasNext := schedulerFloat(w, "nextCheckAt")
				rc, _ := schedulerFloat(w, "remainingChecks")
				if sg, _ := w.flag("signalled"); sg {
					kind = "wake"
				} else if hasNext && nextAt <= float64(clockMs()) && rc > 0 {
					kind = "check"
					w.set("remainingChecks", rc-1)
				}
			}
			if kind == "" {
				throwGoal("No scheduling decision authorizes this checkpoint.")
			}
			used, _ := sch.num("used")
			sch.set("used", used+1)
			sch.set("phase", "claimed")
			disp := newObject()
			disp.set("id", newUUID())
			disp.set("kind", kind)
			disp.set("claimedAt", float64(clockMs()))
			sch.set("dispatch", disp)
			if kind == "repair" {
				sch.set("repairUsed", true)
			} else if kind != "recovery" {
				sch.set("repairUsed", false)
			}
			if w != nil {
				w.set("signalled", false)
				w.set("token", newUUID())
			}
			result = newObject()
			result.set("generation", sch.vals["generation"])
			result.set("dispatchId", disp.vals["id"])
		})
	})
	if err != nil {
		var current *jsObject
		if g := s.c.state(); g != nil {
			current = g.obj("scheduler")
		}
		if current != nil {
			owner, _ := current.str("owner")
			gen, _ := current.str("generation")
			if owner != s.owner() || gen != generation {
				return nil
			}
		}
		msg := err.Error()
		s.safe(func() { s.pause("Goal wake stopped: " + msg) })
		return nil
	}
	return result
}

func (s *goalScheduler) failedDispatch() {
	s.safe(func() {
		s.pause("Goal checkpoint delivery failed after claiming allowance. Use /goal-resume; it will not be replayed.")
	})
}

func (s *goalScheduler) isWaiting() bool {
	if s.inRun {
		return false
	}
	g := s.c.state()
	if g == nil {
		return false
	}
	sch := g.obj("scheduler")
	if sch == nil || !g.has("scheduler") {
		return false
	}
	phase, _ := sch.str("phase")
	return phase == "waiting"
}

func (s *goalScheduler) isDenied() bool { return s.denied }
