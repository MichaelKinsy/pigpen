package pi_goal_x

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const gschF = "goal-scheduler"

// schedFx is fixture() of tests/goal-scheduler.test.ts: a goal "scheduler-fixture" in a project, a core on a scripted host owned by
// `owner`, the scheduler attached to the wake channel. mock() is `t.mock.timers.enable({apis: ["setTimeout", "Date"]})`: time starts at
// 0 and moves only with tick.
type schedFx struct {
	t     *testing.T
	cwd   string
	h     *kernelHost
	core  *core
	clock *FakeClock
	// shared is the test's clock, shared by every fixture of the test.
	shared **FakeClock
}

type fxOpts struct {
	limit    *float64
	owner    string
	existing string
	lax      bool // strictExecutionContract false (the fixture's `strict = false`)
	shared   **FakeClock
}

func newSchedFx(t *testing.T, o fxOpts) *schedFx {
	t.Helper()
	cwd := o.existing
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_GOAL_GLOBAL_SETTINGS_FILE", filepath.Join(t.TempDir(), "absent-global.json"))
	if o.owner == "" {
		o.owner = "owner"
	}
	if cwd == "" {
		cwd = t.TempDir()
		settings := fmt.Sprintf(`{"strictExecutionContract": %v`, !o.lax)
		if o.limit != nil {
			settings += fmt.Sprintf(`, "maxAutonomousRuns": %s`, jsNumber(*o.limit))
		}
		fxWriteFile(t, filepath.Join(cwd, ".pi", "pi-goal-x-settings.json"), settings+"}")
	}
	invalidateGoalSettingsCache()
	goalID := "scheduler-fixture"
	if o.existing == "" {
		goal := createGoal("Test explicit scheduling", true, false, false, clockMs())
		goal.set("id", goalID)
		if _, err := (storage{cwd: cwd}).writeActiveGoalFile(goal); err != nil {
			t.Fatal(err)
		}
	}
	h := newKernelHost(cwd, o.owner)
	h.noUI = true
	h.branch = []any{focusEntryFor(goalID)}
	c := newCore(h)
	c.loadState()
	c.scheduler.attach()
	fx := &schedFx{t: t, cwd: cwd, h: h, core: c, shared: o.shared}
	if o.shared != nil && *o.shared != nil {
		(*o.shared).Attach(&Probe{c: c})
		fx.clock = *o.shared
	}
	t.Cleanup(func() {
		c.scheduler.shutdown()
		c.runtime.clearContinuationState(true)
	})
	return fx
}

func fxWriteFile(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mock enables the fake timers and clock for every fixture of the test (time 0).
func (f *schedFx) mock() {
	f.t.Helper()
	clock := (&Probe{c: f.core}).FakeClock(0)
	f.t.Cleanup(clock.Restore)
	f.clock = clock
	if f.shared != nil {
		*f.shared = clock
	}
}

func (f *schedFx) tick(ms int64) { f.t.Helper(); f.clock.Tick(ms) }

func (f *schedFx) goal() *jsObject { return f.core.state() }

func (f *schedFx) sch() *jsObject {
	if g := f.goal(); g != nil {
		if s := g.obj("scheduler"); s != nil {
			return s
		}
	}
	return newObject() // a goal without scheduler state reads as no fields
}

func (f *schedFx) used() float64  { u, _ := f.sch().num("used"); return u }
func (f *schedFx) phase() string  { p, _ := f.sch().str("phase"); return p }
func (f *schedFx) status() string { return gstr(f.goal(), "status") }
func (f *schedFx) pauseReason() string {
	r, _ := f.goal().str("pauseReason")
	return r
}

func (f *schedFx) dispatchKind() string {
	d := f.sch().obj("dispatch")
	if d == nil || !f.sch().has("dispatch") {
		return ""
	}
	k, _ := d.str("kind")
	return k
}

func (f *schedFx) decision() *jsObject {
	if !f.sch().has("decision") {
		return nil
	}
	return f.sch().obj("decision")
}

func (f *schedFx) wait0() *jsObject {
	if !f.sch().has("wait") {
		return nil
	}
	return f.sch().obj("wait")
}

func (f *schedFx) begin() { f.core.scheduler.begin() }

// admit is the delivery of the last sent checkpoint as the host's message_start.
func (f *schedFx) admit() {
	f.t.Helper()
	f.begin()
	if len(f.h.sent) == 0 {
		f.t.Fatal("nothing was sent to admit")
	}
	last := f.h.sent[len(f.h.sent)-1]
	f.core.scheduler.message(map[string]any{"customType": last.customType, "content": last.content, "display": last.display, "details": last.details, "role": "custom"})
}

func (f *schedFx) ready() goalAgentResult {
	return f.core.scheduler.declare(goalContinuation{kind: "ready"})
}

func (f *schedFx) now() int64 { return clockMs() }

func (f *schedFx) wait() goalAgentResult {
	return f.waitFor("Await fixture job", isoFromMs(f.now()+10000), "", &goalPolling{1, 2})
}

func (f *schedFx) waitFor(reason, deadline, waitID string, polling *goalPolling) goalAgentResult {
	return f.core.scheduler.declare(goalContinuation{kind: "wait", reason: &reason, deadline: deadline, waitID: waitID, polling: polling})
}

func (f *schedFx) settled()            { f.core.scheduler.settled(true) }
func (f *schedFx) settledWith(ok bool) { f.core.scheduler.settled(ok) }

// save is saveGoalSettingsFileConfig(cwd, {...}).
func (f *schedFx) save(kv ...any) {
	f.t.Helper()
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	if _, err := saveGoalSettingsFileConfig(f.cwd, o); err != nil {
		f.t.Fatal(err)
	}
}

func (f *schedFx) fileGoal() *jsObject {
	p, _ := f.goal().str("activePath")
	return parseGoalFile(filepath.Join(f.cwd, p))
}

func fxMatch(t *testing.T, got, re, what string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(got) {
		t.Errorf("%s: %q does not match /%s/", what, got, re)
	}
}

func fxNoMatch(t *testing.T, got, re, what string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(got) {
		t.Errorf("%s: %q matches /%s/", what, got, re)
	}
}

func fxLimit(n float64) *float64 { return &n }

func fxJSON(o *jsObject) string { return marshalJSON(o, "") }

func TestGoalSchedulerTwins(t *testing.T) {
	tw(t, gschF, "explicit zero means no automatic model runs, even for missing disposition", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(0)})
		h.mock()
		h.begin()
		eq(t, h.ready().terminate, false, "ready is refused")
		h.settled()
		h.tick(10000)
		eq(t, len(h.h.sent), 0, "nothing sent")
		eq(t, h.status(), "paused", "status")
		fxMatch(t, h.pauseReason(), `disabled by maxAutonomousRuns=0`, "pause reason")
	})

	tw(t, gschF, "strict ${start} starts automatically and still allows only one repair", func(t *testing.T) {
		for _, start := range []string{"creation", "resume"} {
			t.Run(start, func(t *testing.T) {
				h := newSchedFx(t, fxOpts{})
				h.mock()
				if start == "creation" {
					h.core.replaceGoal(goalCreationConfig{objective: "Work automatically", autoContinue: true}, true, "", -1)
				} else {
					eq(t, h.core.scheduler.resume(), true, "resume")
				}
				h.tick(1)
				eq(t, len(h.h.sent), 1, "one run started")
				h.admit()
				h.settled()
				h.tick(1)
				eq(t, h.dispatchKind(), "repair", "the repair")
				h.admit()
				h.settled()
				h.tick(10000)
				eq(t, len(h.h.sent), 2, "no third run")
				eq(t, h.status(), "paused", "status")
				fxMatch(t, h.pauseReason(), `No execution disposition`, "pause reason")
			})
		}
	})

	tw(t, gschF, "uncapped ready runs keep counting when limits are added and removed", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		for used := 1.0; used <= 5; used++ {
			eq(t, h.ready().terminate, true, "ready")
			h.settled()
			h.tick(1)
			h.admit()
			eq(t, h.used(), used, "used")
		}
		// Uncapped runs still count. The summary reports nothing while the allowance is unlimited, because there is no limit to
		// report.
		eq(t, h.used(), 5.0, "used")
		eq(t, schedulerSummary(h.sch(), nil, true), "", "no limit, no line")
		fxMatch(t, schedulerSummary(h.sch(), fxLimit(5), true), `5/5`, "summary with a limit")
		h.save("maxAutonomousRuns", 5.0)
		eq(t, h.ready().terminate, false, "adding a cap includes previously uncapped dispatches")
		h.save()
		eq(t, h.ready().terminate, true, "removing the cap re-enables continuation")
		h.settled()
		h.tick(1)
		h.admit()
		eq(t, h.used(), 6.0, "used")
		h.save("maxAutonomousRuns", 6.0)
		h.settled()
		h.tick(1)
		eq(t, len(h.h.sent), 6, "sent")
		eq(t, h.status(), "paused", "status")
		eq(t, h.used(), 6.0, "used")
	})

	tw(t, gschF, "declarations persist before termination, claim once, and settings changes never reset spent runs", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(2)})
		h.mock()
		h.begin()
		eq(t, h.ready().terminate, true, "ready")
		disk := h.fileGoal()
		p, _ := disk.obj("scheduler").str("phase")
		eq(t, p, "ready", "the declaration reached the disk before termination")
		eq(t, len(h.h.sent), 0, "sent")
		h.settled()
		h.tick(1)
		eq(t, h.used(), 1.0, "used")
		h.admit()
		eq(t, h.ready().terminate, true, "ready")
		h.settled()
		h.tick(1)
		eq(t, h.used(), 2.0, "used")
		h.admit()
		eq(t, h.ready().terminate, false, "the allowance is spent")
		h.save("maxAutonomousRuns", 3.0)
		eq(t, h.ready().terminate, true, "ready after raising the cap")
		h.settled()
		h.tick(1)
		eq(t, h.used(), 3.0, "used")
		eq(t, len(h.h.sent), 3, "sent")
		first := h.h.sent[0]
		h.core.scheduler.message(map[string]any{"customType": first.customType, "content": first.content, "display": first.display, "details": first.details, "role": "custom"})
		eq(t, h.h.aborts, 1, "duplicate or stale generation is rejected")
	})

	tw(t, gschF, "early wake and check timer race produce one claimed run", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		eq(t, h.wait().terminate, true, "wait")
		w := h.wait0()
		token, _ := w.str("token")
		wake := map[string]any{"goalId": gstr(h.goal(), "id"), "waitToken": token}
		h.h.emit("pi-goal:wake", wake)
		eq(t, len(h.h.sent), 0, "signal before settlement must not start a run")
		h.settled()
		h.tick(1)
		h.h.emit("pi-goal:wake", wake)
		h.tick(1000)
		eq(t, len(h.h.sent), 1, "one run")
		eq(t, h.dispatchKind(), "wake", "dispatch kind")
		rc, _ := h.wait0().num("remainingChecks")
		eq(t, rc, 2.0, "remaining checks")
	})

	tw(t, gschF, "checks retain identity, deadline and finite count across redeclaration", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		h.wait()
		first := deepClone(h.wait0()).(*jsObject)
		h.settled()
		h.tick(999)
		eq(t, len(h.h.sent), 0, "no check before the interval")
		h.tick(2)
		eq(t, len(h.h.sent), 1, "the check")
		h.admit()
		eq(t, h.wait().terminate, false, "new wait cannot reset an existing check")
		firstDeadline, _ := first.num("deadline")
		redeclare := func() goalAgentResult {
			reason := gstr(first, "reason")
			return h.core.scheduler.declare(goalContinuation{kind: "wait", waitID: gstr(first, "id"), reason: &reason, deadline: isoFromMs(int64(firstDeadline))})
		}
		eq(t, redeclare().terminate, true, "redeclare")
		rc, _ := h.wait0().num("remainingChecks")
		eq(t, rc, 1.0, "remaining checks")
		h.settled()
		h.tick(1001)
		h.admit()
		eq(t, redeclare().terminate, true, "redeclare")
		h.settled()
		eq(t, h.status(), "paused", "status")
		fxMatch(t, h.pauseReason(), `check allowance exhausted`, "pause reason")
		eq(t, len(h.h.sent), 2, "sent")
	})

	tw(t, gschF, "waiting restores with no catch-up; claimed dispatch never replays", func(t *testing.T) {
		var clock *FakeClock
		h := newSchedFx(t, fxOpts{limit: fxLimit(5), shared: &clock})
		h.mock()
		h.begin()
		h.wait()
		h.settled()
		h.core.scheduler.shutdown()
		h.tick(4000)
		resumed := newSchedFx(t, fxOpts{owner: "owner", existing: h.cwd, shared: &clock})
		resumed.core.scheduler.restore()
		resumed.tick(1)
		eq(t, len(resumed.h.sent), 1, "missed checks coalesce into one")
		resumed.core.scheduler.shutdown()
		interrupted := newSchedFx(t, fxOpts{owner: "owner", existing: h.cwd, shared: &clock})
		interrupted.core.scheduler.restore()
		interrupted.tick(10000)
		eq(t, len(interrupted.h.sent), 0, "a claimed dispatch never replays")
		eq(t, interrupted.status(), "paused", "status")
	})

	tw(t, gschF, "deadline expiration and another session cannot wake a sleeping goal", func(t *testing.T) {
		var clock *FakeClock
		h := newSchedFx(t, fxOpts{limit: fxLimit(5), shared: &clock})
		h.mock()
		h.begin()
		h.waitFor("External event", isoFromMs(h.now()+2000), "", nil)
		h.settled()
		other := newSchedFx(t, fxOpts{owner: "other-owner", existing: h.cwd, shared: &clock})
		other.core.scheduler.restore()
		eq(t, other.ready().terminate, false, "another session cannot declare")
		h.tick(2000)
		eq(t, len(h.h.sent)+len(other.h.sent), 0, "nothing was sent")
		fxMatch(t, h.pauseReason(), `deadline`, "pause reason")
	})

	tw(t, gschF, "a later model turn invalidates a decision and user takeover cancels delivery", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		h.begin()
		h.ready()
		h.core.scheduler.turn()
		eq(t, h.decision() == nil, true, "a later turn invalidates the decision")
		h.ready()
		h.settled()
		h.core.scheduler.takeover()
		h.tick(10000)
		eq(t, len(h.h.sent), 0, "takeover cancels delivery")
		eq(t, h.used(), 0.0, "used")
	})

	tw(t, gschF, "maxAutonomousRuns is strictly parsed, layered, and removable", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		for _, n := range []string{"-1", "1.1", `"1x"`, "9007199254740992"} {
			layer := mustParse(t, `{"maxAutonomousRuns": `+n+`}`)
			eq(t, layer.has("maxAutonomousRuns"), false, "maxAutonomousRuns "+n+" is rejected")
		}
		for n, want := range map[string]float64{"0": 0, `"0"`: 0, "1": 1, `"25"`: 25, "9007199254740991": 9007199254740991} {
			got, ok := mustParse(t, `{"maxAutonomousRuns": `+n+`}`).num("maxAutonomousRuns")
			eq(t, ok && got == want, true, "maxAutonomousRuns "+n)
		}
		h.save("maxAutonomousRuns", 4.0)
		l := loadGoalSettings(h.cwd).maxAutonomousRuns
		eq(t, l != nil && *l == 4, true, "saved 4")
		h.save()
		eq(t, loadGoalSettings(h.cwd).maxAutonomousRuns == nil, true, "removed")
	})

	tw(t, gschF, "project zero disables an inherited allowance without renewing consumption", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		fxWriteFile(t, os.Getenv("PI_GOAL_GLOBAL_SETTINGS_FILE"), `{"maxAutonomousRuns": 20}`)
		invalidateGoalSettingsCache()
		h.mock()
		h.begin()
		h.ready()
		h.settled()
		h.tick(1)
		h.admit()
		eq(t, h.used(), 1.0, "used")
		h.ready()
		h.settled()
		h.save("maxAutonomousRuns", 0.0)
		invalidateGoalSettingsCache()
		l := loadGoalSettings(h.cwd).maxAutonomousRuns
		eq(t, l != nil && *l == 0, true, "zero survives persistence and overrides global 20")
		fxMatch(t, schedulerSummary(h.sch(), fxLimit(0), true), `1/0 \(automatic continuation disabled\)`, "summary")
		h.tick(1)
		eq(t, len(h.h.sent), 1, "pending delivery is cancelled")
		eq(t, h.status(), "paused", "status")
		eq(t, h.core.scheduler.resume(), false, "resume is refused at zero")
		h.save()
		l = loadGoalSettings(h.cwd).maxAutonomousRuns
		eq(t, l != nil && *l == 20, true, "unsetting restores inheritance")
		eq(t, h.used(), 1.0, "used")
		eq(t, len(h.h.sent), 1, "sent")
	})

	tw(t, gschF, "${kind} respects an outstanding wait deadline expiring ${expiry}", func(t *testing.T) {
		for _, kind := range []string{"recovery", "repair"} {
			for _, expiry := range []string{"before scheduling", "before delivery"} {
				t.Run(kind+" "+expiry, func(t *testing.T) {
					h := newSchedFx(t, fxOpts{limit: fxLimit(10)})
					h.mock()
					h.begin()
					eq(t, h.waitFor("Await job", isoFromMs(h.now()+3000), "", &goalPolling{1, 2}).terminate, true, "wait")
					h.settled()
					h.tick(1001)
					h.admit()
					busy := true
					h.h.idle = func() bool { return !busy }
					if expiry == "before scheduling" {
						h.tick(2000)
					}
					h.settledWith(kind == "repair")
					if kind == "recovery" {
						h.core.scheduler.recover()
					}
					if expiry == "before delivery" {
						eq(t, h.phase(), "ready", "phase")
						k, _ := h.decision().str("kind")
						eq(t, k, "ready", "decision kind")
						h.tick(2000)
					}
					busy = false
					h.tick(50)
					eq(t, len(h.h.sent), 1, "no dispatch after the polling check")
					eq(t, h.used(), 1.0, "denied dispatch spends no allowance")
					eq(t, h.status(), "paused", "status")
					fxMatch(t, h.pauseReason(), `Wait deadline reached`, "pause reason")
				})
			}
		}
	})

	tw(t, gschF, "network backoff crossing a wait deadline cannot dispatch recovery", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		h.mock()
		h.begin()
		h.waitFor("Await job", isoFromMs(h.now()+3000), "", &goalPolling{1, 2})
		h.settled()
		h.tick(1001)
		h.admit()
		h.settledWith(false)
		plan := h.core.runtime.scheduleNetworkErrorRetry(hostRuntimeCtx{h.h}, h.goal(), nil)
		eq(t, plan != nil && plan.delayMs == 5000, true, "the first backoff is 5s")
		h.tick(5001)
		eq(t, len(h.h.sent), 1, "sent")
		eq(t, h.used(), 1.0, "used")
		eq(t, h.status(), "paused", "status")
		fxMatch(t, h.pauseReason(), `Wait deadline reached`, "pause reason")
	})

	tw(t, gschF, "lowering allowance before delivery stops the wake without resetting usage", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(2)})
		h.mock()
		h.begin()
		h.ready()
		h.settled()
		h.save("maxAutonomousRuns", 0.0)
		h.tick(1)
		eq(t, len(h.h.sent), 0, "sent")
		eq(t, h.used(), 0.0, "used")
		eq(t, h.status(), "paused", "status")
	})

	tw(t, gschF, "a previous owner's callback cannot dispatch or pause the new owner's goal", func(t *testing.T) {
		var clock *FakeClock
		h := newSchedFx(t, fxOpts{limit: fxLimit(5), shared: &clock})
		h.mock()
		h.begin()
		h.ready()
		h.settled()
		other := newSchedFx(t, fxOpts{owner: "new-owner", existing: h.cwd, shared: &clock})
		eq(t, other.core.scheduler.resume(), true, "the new owner resumes")
		h.tick(1)
		eq(t, len(h.h.sent), 0, "the previous owner sends nothing")
		eq(t, len(other.h.sent), 1, "the new owner sends one run")
		eq(t, other.status(), "active", "the new owner's goal stays active")
		eq(t, gstr(other.fileGoal(), "status"), "active", "so does the file")
	})

	tw(t, gschF, "persistence rejection cannot report a saved decision or dispatch", func(t *testing.T) {
		// The original swaps goalService.apply for a stub that fails; here the storage refuses for real: another writer holds the
		// goal's lock, so the mutation cannot be written.
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.begin()
		lock, err := acquireGoalLock(storage{cwd: h.cwd}, "scheduler-fixture", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		result := h.ready()
		lock.release()
		eq(t, result.terminate, false, "a rejected write is not terminal")
		eq(t, h.decision() == nil, true, "no decision is reported saved")
		eq(t, len(h.h.sent), 0, "nothing is dispatched")
	})

	tw(t, gschF, "readiness polling, failure after claim, and active-time waiting", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
		h.mock()
		busy := true
		h.h.pending = func() bool { return busy }
		h.begin()
		h.ready()
		h.settled()
		h.tick(200)
		eq(t, len(h.h.sent), 0, "no dispatch while messages are pending")
		eq(t, h.used(), 0.0, "used")
		busy = false
		h.tick(50)
		eq(t, len(h.h.sent), 1, "dispatched once ready")
		eq(t, h.used(), 1.0, "used")
		h.core.scheduler.failedDispatch()
		eq(t, h.status(), "paused", "status")
		eq(t, h.used(), 1.0, "used")
		h.core.scheduler.resume()
		h.tick(1)
		h.admit()
		h.wait()
		h.settled()
		before := gnum(h.goal().obj("usage"), "activeSeconds")
		h.tick(500)
		eq(t, gnum(h.core.goalForDisplay().obj("usage"), "activeSeconds"), before, "a waiting goal accrues no active time")
	})

	tw(t, gschF, "explicit resume during an execution waits for settlement and dispatches kickoff, not repair", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(2)})
		h.mock()
		h.begin()
		eq(t, h.core.scheduler.resume(), true, "resume")
		h.tick(100)
		eq(t, len(h.h.sent), 0, "nothing starts during the execution")
		h.settled()
		h.tick(1)
		eq(t, h.dispatchKind(), "kickoff", "dispatch kind")
		eq(t, h.used(), 1.0, "used")
	})

	tw(t, gschF, "default rejects new waits without terminating or mutating; optional ready succeeds", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(4), lax: true})
		h.begin()
		before := fxJSON(h.goal())
		result := h.wait()
		eq(t, result.terminate, false, "the wait is rejected without terminating")
		fxMatch(t, result.text, `strictExecutionContract=true`, "text")
		eq(t, fxJSON(h.goal()), before, "the goal is not mutated")
		eq(t, h.ready().terminate, true, "ready succeeds")
	})

	tw(t, gschF, "disabling strict mode converts queued repair at ${stage} without renewing allowance", func(t *testing.T) {
		for _, stage := range []string{"schedule", "claim"} {
			t.Run(stage, func(t *testing.T) {
				h := newSchedFx(t, fxOpts{limit: fxLimit(5)})
				h.mock()
				h.begin()
				h.settled()
				k, _ := h.decision().str("kind")
				eq(t, k, "ready", "decision kind")
				h.save("maxAutonomousRuns", 5.0, "strictExecutionContract", false)
				if stage == "schedule" {
					h.core.scheduler.schedule()
				}
				h.tick(1)
				eq(t, h.dispatchKind(), "ready", "dispatch kind")
				eq(t, h.used(), 1.0, "used")
				fxNoMatch(t, marshalJSON(h.sch().vals["decision"], ""), `repair prompt`, "decision")
				h.admit()
				h.settled()
				h.tick(1)
				eq(t, h.used(), 2.0, "used")
			})
		}
	})

	tw(t, gschF, "existing waits keep identity, deadline and repair bounds after opt-out", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(8)})
		h.mock()
		h.begin()
		h.wait()
		wait := deepClone(h.wait0()).(*jsObject)
		h.save("strictExecutionContract", false, "maxAutonomousRuns", 8.0)
		h.settled()
		h.tick(1001)
		h.admit()
		reason := gstr(wait, "reason")
		deadline, _ := wait.num("deadline")
		redeclare := h.core.scheduler.declare(goalContinuation{kind: "wait", waitID: gstr(wait, "id"), reason: &reason, deadline: isoFromMs(int64(deadline))})
		eq(t, redeclare.terminate, true, "redeclare")
		rc, _ := h.wait0().num("remainingChecks")
		eq(t, rc, 1.0, "remaining checks")
		gotDeadline, _ := h.wait0().num("deadline")
		eq(t, gotDeadline, deadline, "deadline")
		h.settled()
		h.tick(1001)
		h.admit()
		h.settled()
		h.tick(1)
		eq(t, h.dispatchKind(), "repair", "dispatch kind")
		h.admit()
		h.settled()
		eq(t, h.status(), "paused", "status")
		fxMatch(t, h.pauseReason(), `No execution disposition`, "pause reason")
	})

	tw(t, gschF, "enabling strict mode at settlement requires a disposition without resetting usage", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{limit: fxLimit(5), lax: true})
		h.mock()
		h.begin()
		h.settled()
		h.tick(1)
		h.admit()
		h.save("strictExecutionContract", true, "maxAutonomousRuns", 5.0)
		h.settled()
		h.tick(1)
		eq(t, h.dispatchKind(), "repair", "dispatch kind")
		eq(t, h.used(), 2.0, "used")
		h.admit()
		h.settled()
		eq(t, h.status(), "paused", "status")
		h.save("strictExecutionContract", false)
		h.core.scheduler.restore()
		h.tick(1000)
		eq(t, h.status(), "paused", "status")
		eq(t, len(h.h.sent), 2, "sent")
	})

	tw(t, gschF, "default settings inherit strict mode with explicit false overriding global true", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{lax: true})
		h.save()
		eq(t, loadGoalSettings(h.cwd).strictExecutionContract, false, "default")
		fxWriteFile(t, os.Getenv("PI_GOAL_GLOBAL_SETTINGS_FILE"), `{"strictExecutionContract": true}`)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(h.cwd).strictExecutionContract, true, "global true is inherited")
		h.save("strictExecutionContract", false)
		eq(t, loadGoalSettings(h.cwd).strictExecutionContract, false, "explicit false wins")
		h.save()
		eq(t, loadGoalSettings(h.cwd).strictExecutionContract, true, "unset inherits again")
		got, _ := mustParse(t, `{"strictExecutionContract": false}`).flag("strictExecutionContract")
		eq(t, got, false, "parse false")
		eq(t, mustParse(t, `{"strictExecutionContract": "invalid"}`).has("strictExecutionContract"), false, "parse invalid")
	})

	tw(t, gschF, "showAutonomousRuns is a layered boolean that defaults to on", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		eq(t, loadGoalSettings(h.cwd).showAutonomousRuns, true, "default on")
		h.save("showAutonomousRuns", false)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(h.cwd).showAutonomousRuns, false, "project false")
		fxWriteFile(t, os.Getenv("PI_GOAL_GLOBAL_SETTINGS_FILE"), `{"showAutonomousRuns": true}`)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(h.cwd).showAutonomousRuns, false, "project false beats global true")
		h.save("showAutonomousRuns", true)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(h.cwd).showAutonomousRuns, true, "project true beats global true")
		h.save()
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(h.cwd).showAutonomousRuns, true, "global value applies when the project is unset")
		for _, bad := range []string{`"yes"`, "1", "null", "{}"} {
			eq(t, mustParse(t, `{"showAutonomousRuns": `+bad+`}`).has("showAutonomousRuns"), false, "showAutonomousRuns "+bad)
		}
		got, _ := mustParse(t, `{"showAutonomousRuns": false}`).flag("showAutonomousRuns")
		eq(t, got, false, "parse false")
	})

	tw(t, gschF, "the runs line is omitted when unlimited and controlled by the setting when finite", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		s := normalizeGoalScheduler(jo(t, `{"version": 1, "owner": "owner", "generation": "gen", "used": 4, "phase": "idle", "repairUsed": false}`)).(*jsObject)

		// Unlimited: no line at all, because there is no limit to report.
		eq(t, schedulerSummary(s, nil, true), "", "unlimited")
		eq(t, schedulerSummary(s, nil, true), "", "the setting cannot force a line when there is no limit")

		// Finite: reported by default.
		fxMatch(t, schedulerSummary(s, fxLimit(20), true), `Autonomous runs: 4/20\.`, "finite")
		fxMatch(t, schedulerSummary(h.sch(), fxLimit(20), true), `Autonomous runs`, "the fixture's scheduler")

		// Finite with the setting off.
		eq(t, schedulerSummary(s, fxLimit(20), false), "", "setting off")

		// Zero is a finite allowance, so the disabled suffix still reports.
		fxMatch(t, schedulerSummary(s, fxLimit(0), true), `4/0 \(automatic continuation disabled\)`, "zero")
		eq(t, schedulerSummary(s, fxLimit(0), false), "", "zero with the setting off")

		fxMatch(t, schedulerSummary(nil, fxLimit(20), true), `Autonomous runs: 0/20\.`, "no scheduler")
	})

	tw(t, gschF, "a ready disposition needs no next action and the schema no longer accepts one", func(t *testing.T) {
		h := newSchedFx(t, fxOpts{})
		ext, _ := buildGoalExtension(goalDeps{})
		if schema, ok := registrationOf(t, ext).Schemas["update_goal"]; !ok {
			t.Error("update_goal is not registered")
		} else {
			fxNoMatch(t, schema, `next_action`, "update_goal schema must not expose next_action")
		}
		result := h.core.scheduler.declare(goalContinuation{kind: "ready"})
		eq(t, result.terminate, true, "ready with no next action succeeds")
		fxMatch(t, result.text, `Scheduling decision saved`, "declared text")
		eq(t, fxJSON(h.decision()), `{"kind":"ready","purpose":"ready"}`, "purpose is kept and no nextAction is stored")
		fxNoMatch(t, result.text, `Next action`, "the declaration result renders no next-action line")
		fxNoMatch(t, schedulerSummary(h.sch(), fxLimit(20), true), `Next action`, "summary")
	})

	tw(t, gschF, "a goal saved with decision.nextAction loads and strips the field", func(t *testing.T) {
		legacy := func(decision string) string {
			return `{"version": 1, "owner": "legacy-owner", "generation": "legacy-gen", "used": 3, "phase": "ready", "decision": ` + decision + `, "repairUsed": false}`
		}
		normalized, ok := normalizeGoalScheduler(jo(t, legacy(`{"kind": "ready", "nextAction": "Continue pursuing the goal, then verify and complete it when satisfied.", "purpose": "repair"}`))).(*jsObject)
		eq(t, ok, true, "a legacy scheduler state must still normalize")
		p, _ := normalized.str("phase")
		eq(t, p, "ready", "phase")
		used, _ := normalized.num("used")
		eq(t, used, 3.0, "spent allowance is preserved")
		eq(t, fxJSON(normalized.obj("decision")), `{"kind":"ready","purpose":"repair"}`, "purpose is preserved and nextAction is stripped")
		eq(t, strings.Contains(fxJSON(normalized), "nextAction"), false, "the stripped field is never written back")
		for _, purpose := range []string{"ready", "repair", "kickoff", "recovery"} {
			s := normalizeGoalScheduler(jo(t, legacy(`{"kind": "ready", "nextAction": "x", "purpose": "`+purpose+`"}`))).(*jsObject)
			got, _ := s.obj("decision").str("purpose")
			eq(t, got, purpose, "purpose")
		}
		invalid := normalizeGoalScheduler(jo(t, legacy(`{"kind": "ready", "purpose": "bogus"}`))).(*jsObject)
		p, _ = invalid.str("phase")
		eq(t, p, "interrupted", "an invalid decision is still rejected")
	})

}
