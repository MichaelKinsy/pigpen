package tintinweb_subagents

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Twins of the original's test/schedule.test.ts. The original runs the fire path on vitest's fake timers and a mocked
// AgentManager; here the same is a schedClock and a mockManager (schedule_helpers_test.go). A mutation is a function that
// patches the job, since a JavaScript patch object cannot say "unset" the way a Go struct needs.
const fSchedule = "schedule"

func ptrOf[T any](v T) *T { return &v }

func TestSchedulerStaticParsers(t *testing.T) {
	tw(t, fSchedule, "parseRelativeTime accepts +Ns/Nm/Nh/Nd and rejects bare numbers", func(t *testing.T) {
		before := time.Now()
		iso, ok, _ := parseRelativeTime("+10s", before)
		eq(t, ok, true)
		ms, _ := parseJSDate(iso)
		diff := int64(ms) - before.UnixMilli()
		eq(t, diff >= 9_000 && diff <= 11_000, true)
		for _, s := range []string{"+5m", "+1h", "+2d"} {
			_, ok, _ := parseRelativeTime(s, before)
			eq(t, ok, true)
		}
		for _, s := range []string{"10s", "+5x", "hello"} { // bare digits, wrong unit, no plus
			_, ok, _ := parseRelativeTime(s, before)
			eq(t, ok, false)
		}
	})
	tw(t, fSchedule, "parseInterval converts unit-suffixed strings to milliseconds", func(t *testing.T) {
		for s, want := range map[string]float64{"10s": 10_000, "5m": 300_000, "1h": 3_600_000, "2d": 172_800_000} {
			got, ok := parseInterval(s)
			eq(t, ok, true)
			eq(t, got, want)
		}
		for _, s := range []string{"+5m", "5x", "five-minutes"} { // relative isn't an interval
			_, ok := parseInterval(s)
			eq(t, ok, false)
		}
	})
	tw(t, fSchedule, "validateCronExpression rejects non-6-field expressions", func(t *testing.T) {
		eq(t, validateCronExpression("* * * * *").Valid, false) // 5 fields
		eq(t, validateCronExpression("0 0 9 * * 1").Valid, true)
		eq(t, validateCronExpression("0 0 9 * * *").Valid, true)
		eq(t, validateCronExpression("not-a-cron").Valid, false)
		eq(t, validateCronExpression("* * * * *").Error, `Cron must have 6 fields (second minute hour dom month dow), got 5. Example: "0 0 9 * * 1" for 9am every Monday.`)
	})
	tw(t, fSchedule, "detectSchedule tags type and normalizes input", func(t *testing.T) {
		now := time.Now()
		get := func(s string) detected {
			d, err := detectSchedule(s, now)
			if err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			return d
		}
		eq(t, get("+10m").Type, "once")
		eq(t, get("5m").Type, "interval")
		eq(t, *get("5m").IntervalMs, float64(300_000))
		eq(t, get("0 0 9 * * 1").Type, "cron")

		const iso = "2099-01-01T00:00:00.000Z"
		r := get(iso)
		eq(t, r.Type, "once")
		eq(t, r.Normalized, iso)

		_, err := detectSchedule("garbage", now)
		eq(t, err != nil && strings.Contains(err.Error(), "Invalid schedule"), true)
	})
}

func TestSchedulerLifecycle(t *testing.T) {
	start := func(t *testing.T) *schedRig { return startSched(t, nil, newMockManager()) }
	tw(t, fSchedule, "isActive() reports start/stop state", func(t *testing.T) {
		r := start(t)
		eq(t, r.sched.isActive(), true)
		r.sched.stop()
		eq(t, r.sched.isActive(), false)
	})
	tw(t, fSchedule, "addJob persists, arms, and emits added event", func(t *testing.T) {
		r := start(t)
		job, err := r.sched.addJob(input("j1", "1h"))
		eq(t, err, nil)
		eq(t, job.ScheduleType, "interval")
		eq(t, len(r.sched.list()), 1)
		eq(t, r.events.types(), []string{"added"})
		eq(t, len(newScheduleStore(r.dir+"/s.json").list()), 1) // persisted
	})
	tw(t, fSchedule, "addJob rejects duplicate names", func(t *testing.T) {
		r := start(t)
		_, err := r.sched.addJob(input("j1", "1h"))
		eq(t, err, nil)
		_, err = r.sched.addJob(input("j1", "2h"))
		eq(t, err != nil && strings.Contains(err.Error(), "already exists"), true)
	})
	tw(t, fSchedule, "removeJob clears the job and emits removed", func(t *testing.T) {
		r := start(t)
		job, _ := r.sched.addJob(input("j1", "1h"))
		ok, _ := r.sched.removeJob(job.ID)
		eq(t, ok, true)
		eq(t, len(r.sched.list()), 0)
		eq(t, r.events.has(func(e scheduleChange) bool { return e.Type == "removed" && e.JobID == job.ID }), true)
	})
	tw(t, fSchedule, "updateJob({enabled: false}) unschedules but keeps the record", func(t *testing.T) {
		r := start(t)
		job, _ := r.sched.addJob(input("j1", "1h"))
		_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.Enabled = false })
		eq(t, r.sched.list()[0].Enabled, false)
		_, ok := r.sched.getNextRun(job.ID)
		eq(t, ok, false)
	})
	// Regression: getNextRun on a freshly-created interval used to return undefined (the lastRun-based branch needs
	// lastRun, which is undefined before first fire), surfacing as "Next run: (unknown)" in the agent's create-response.
	tw(t, fSchedule, "getNextRun returns an approximate future time for a fresh interval (no lastRun yet)", func(t *testing.T) {
		r := start(t)
		before := time.Now()
		job, _ := r.sched.addJob(newJobInput{Name: "fresh-interval", Description: "x", Schedule: "1h", SubagentType: "general-purpose", Prompt: "p"})
		next, ok := r.sched.getNextRun(job.ID)
		eq(t, ok, true)
		ms, _ := parseJSDate(next)
		diff := int64(ms) - before.UnixMilli() // ~now + 1h, with a small tolerance for the time spent in the call
		eq(t, diff >= 3_600_000-1_000 && diff <= 3_600_000+1_000, true)
	})
	// Once a fire happens and `lastRun` is set, getNextRun should pivot to it.
	tw(t, fSchedule, "getNextRun uses lastRun when present for interval jobs", func(t *testing.T) {
		r := start(t)
		job, _ := r.sched.addJob(input("ran-once", "1h"))
		lastRun := isoString(time.Now().Add(-30 * time.Minute)) // 30m ago
		_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.LastRun = lastRun })
		next, ok := r.sched.getNextRun(job.ID)
		eq(t, ok, true)
		ms, _ := parseJSDate(lastRun)
		eq(t, next, isoString(time.UnixMilli(int64(ms)+3_600_000)))
	})
	tw(t, fSchedule, "rejects past one-shot timestamps upfront — no record created", func(t *testing.T) {
		r := start(t)
		past := isoString(time.Now().Add(-time.Minute))
		_, err := r.sched.addJob(input("past", past))
		eq(t, err != nil && strings.Contains(err.Error(), "in the past"), true)
		eq(t, len(r.sched.list()), 0) // no dead-on-arrival record left behind
	})
	// The safety net in scheduleJob's past-branch only fires on store reload: a once-job persisted with a future ISO
	// whose time has now passed (process restart after the trigger window).
	tw(t, fSchedule, "disables a previously-enabled one-shot reloaded from disk past its time", func(t *testing.T) {
		r := start(t)
		past := isoString(time.Now().Add(-time.Minute))
		// A direct store insert bypasses addJob's upfront validation: a record valid when written, stale on reload.
		eq(t, r.store.add(scheduledSubagent{ID: "reload-test", Name: "reload", Description: "reload", Schedule: past, ScheduleType: "once",
			SubagentType: "general-purpose", Prompt: "x", Enabled: true, CreatedAt: past}), nil)
		// Re-arm: stop drops the timers, start re-reads the store and arms every enabled job.
		r.sched.stop()
		r.sched.start(r.manager, r.store, r.events.emit)

		reloaded := r.job(t, "reload-test")
		eq(t, reloaded.Enabled, false)
		eq(t, reloaded.LastStatus, "error")
		eq(t, r.events.has(func(e scheduleChange) bool {
			return e.Type == "error" && e.JobID == "reload-test" && strings.Contains(e.Error, "in the past")
		}), true)
	})
}

func TestSchedulerFirePath(t *testing.T) {
	// start is the original's beforeEach/afterEach: fake timers, a mock manager, and strict dispatch restored afterwards.
	start := func(t *testing.T, manager *mockManager) (*schedRig, *schedClock) {
		clock := newFakeClock()
		t.Cleanup(func() {
			setFallbackSubagent(nil)
			registerAgents(newRegistry())
		})
		return startSched(t, clock, manager), clock
	}
	tw(t, fSchedule, "interval jobs fire repeatedly via setInterval", func(t *testing.T) {
		r, clock := start(t, newMockManager())
		_, err := r.sched.addJob(input("every-10s", "10s"))
		eq(t, err, nil)
		eq(t, len(r.manager.spawned()), 0)
		clock.advance(10 * time.Second)
		eq(t, len(r.manager.spawned()), 1)
		clock.advance(20 * time.Second)
		eq(t, len(r.manager.spawned()), 3)
	})
	tw(t, fSchedule, "refuses at fire time when the job's agent type no longer resolves", func(t *testing.T) {
		// The registry is what production populates at activation; a job outliving its agent must not silently run
		// something else (#183).
		r, clock := start(t, newMockManager())
		registerAgents(newRegistry())
		setFallbackSubagent(ptrOf(noFallback))
		job, err := r.sched.addJob(newJobInput{Name: "gone", Description: "vanished agent", Schedule: "+1s", SubagentType: "deleted-since", Prompt: "run"})
		eq(t, err, nil)

		clock.advance(2 * time.Second)

		eq(t, len(r.manager.spawned()), 0)
		eq(t, r.job(t, job.ID).LastStatus, "error")
		// Pin WHY it failed: "didn't spawn" alone would be satisfied by any unrelated pre-spawn throw.
		eq(t, r.events.has(func(e scheduleChange) bool {
			return e.Type == "error" && strings.Contains(e.Error, "Unknown or disabled agent type")
		}), true)
	})
	tw(t, fSchedule, "one-shot fires once and auto-disables", func(t *testing.T) {
		r, clock := start(t, newMockManager())
		job, _ := r.sched.addJob(input("soon", "+1s"))

		clock.advance(2 * time.Second)
		eq(t, len(r.manager.spawned()), 1)

		// The auto-disable update happens synchronously inside the timer callback.
		eq(t, r.job(t, job.ID).Enabled, false)

		// Subsequent ticks shouldn't fire again.
		clock.advance(60 * time.Second)
		eq(t, len(r.manager.spawned()), 1)
	})
	tw(t, fSchedule, "fire passes bypassQueue: true to manager.spawn", func(t *testing.T) {
		r, clock := start(t, newMockManager())
		_, _ = r.sched.addJob(input("every-1s", "1s"))

		clock.advance(time.Second)
		calls := r.manager.spawned()
		eq(t, len(calls), 1)
		eq(t, calls[0].opts.BypassQueue, true)
		eq(t, calls[0].opts.IsBackground, true)
	})
	tw(t, fSchedule, "fire passes the job's configuration as the invocation snapshot", func(t *testing.T) {
		// A scheduled run has no tool call to build one, so without this the conversation viewer can say nothing about
		// how the job was configured. The model is left out on purpose: the manager fills in the effective one once the
		// session reports it.
		r, clock := start(t, newMockManager())
		in := input("every-1s", "1s")
		in.Thinking, in.MaxTurns, in.Isolated = ptrOf("high"), ptrOf(12), ptrOf(true)
		_, _ = r.sched.addJob(in)

		clock.advance(time.Second)
		eq(t, r.manager.spawned()[0].opts.Invocation, invocationSnapshot{Thinking: ptrOf("high"), MaxTurns: ptrOf(12), Isolated: ptrOf(true), RunInBackground: true})
	})
	tw(t, fSchedule, "fire normalizes an unlimited turn budget out of the snapshot", func(t *testing.T) {
		// 0 means unlimited; "max turns: 0" would read as a limit of none.
		r, clock := start(t, newMockManager())
		in := input("unlimited", "1s")
		in.MaxTurns = ptrOf(0)
		_, _ = r.sched.addJob(in)

		clock.advance(time.Second)
		eq(t, r.manager.spawned()[0].opts.Invocation.MaxTurns == nil, true)
	})
	tw(t, fSchedule, "disabled jobs do not fire", func(t *testing.T) {
		r, clock := start(t, newMockManager())
		job, _ := r.sched.addJob(input("off", "1s"))
		_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.Enabled = false })
		clock.advance(5 * time.Second)
		eq(t, len(r.manager.spawned()), 0)
	})
	tw(t, fSchedule, "emits fired event with agentId on successful spawn", func(t *testing.T) {
		r, clock := start(t, newMockManager())
		_, _ = r.sched.addJob(input("fire-once", "+1s"))
		clock.advance(2 * time.Second)
		eq(t, r.events.has(func(e scheduleChange) bool {
			return e.Type == "fired" && e.Name == "fire-once" && strings.HasPrefix(e.AgentID, "agent-")
		}), true)
	})
	tw(t, fSchedule, "records lastStatus error and emits when manager.spawn throws", func(t *testing.T) {
		m := newMockManager()
		m.spawnErr = []error{errors.New("no slots")}
		r, clock := start(t, m)
		job, _ := r.sched.addJob(input("boom", "+1s"))
		clock.advance(2 * time.Second)

		// The update is synchronous in the spawn-throw path.
		eq(t, r.job(t, job.ID).LastStatus, "error")
		eq(t, r.events.has(func(e scheduleChange) bool { return e.Type == "error" && e.JobID == job.ID && e.Error == "no slots" }), true)
	})
	tw(t, fSchedule, "records lastStatus error when the agent fails to start after spawn returns", func(t *testing.T) {
		// Under isolation: "worktree" the agent is not running when spawn() returns (the repo copy is awaited). A failure
		// there must be recorded as a failed run, not as the success the missing run promise would otherwise imply.
		m := newMockManager()
		m.startErr = []error{errors.New(`Cannot run with isolation: "worktree"`)}
		r, clock := start(t, m)
		in := input("no-worktree", "+1s")
		in.Isolation = "worktree"
		job, _ := r.sched.addJob(in)
		clock.advance(2 * time.Second)

		waitFor(t, 2*time.Second, "lastStatus error", func() bool { return r.job(t, job.ID).LastStatus == "error" })
	})

	// Status reflection from record.status (regression for bug #1): the real AgentManager's promise *always* resolves
	// (its .catch returns ""), so the schedule's success/error must be inferred from `record.status`, not from promise
	// resolution. These tests model that contract.
	t.Run("infers success vs error from record.status, not promise resolution", func(t *testing.T) {
		tw(t, fSchedule, "records lastStatus 'error' when the agent terminates with status='error'", func(t *testing.T) {
			r, clock := start(t, newFaithfulManager("running", false))
			job, _ := r.sched.addJob(input("fail-job", "+1s"))

			clock.advance(2 * time.Second)
			eq(t, len(r.manager.spawned()), 1)

			// The agent ran and ended in error: the same shape the real AgentManager produces.
			rec := r.manager.record(0)
			rec.setStatus("error")
			rec.resolve()

			waitFor(t, 2*time.Second, "lastStatus error", func() bool { return r.job(t, job.ID).LastStatus == "error" })
		})
		tw(t, fSchedule, "records lastStatus 'success' when the agent terminates with status='completed'", func(t *testing.T) {
			r, clock := start(t, newFaithfulManager("running", false))
			job, _ := r.sched.addJob(input("ok-job", "+1s"))

			clock.advance(2 * time.Second)
			rec := r.manager.record(0)
			rec.setStatus("completed")
			rec.resolve()

			waitFor(t, 2*time.Second, "lastStatus success", func() bool { return r.job(t, job.ID).LastStatus == "success" })
		})
		tw(t, fSchedule, "treats aborted and stopped as errors (terminal failure states)", func(t *testing.T) {
			r, clock := start(t, newFaithfulManager("running", false))
			a, _ := r.sched.addJob(input("abort-job", "+1s"))
			b, _ := r.sched.addJob(input("stop-job", "+2s"))

			clock.advance(3 * time.Second)
			r.manager.record(0).setStatus("aborted")
			r.manager.record(0).resolve()
			r.manager.record(1).setStatus("stopped")
			r.manager.record(1).resolve()

			waitFor(t, 2*time.Second, "both errors", func() bool {
				return r.job(t, a.ID).LastStatus == "error" && r.job(t, b.ID).LastStatus == "error"
			})
		})
	})
}

func TestSchedulerStoppedState(t *testing.T) {
	tw(t, fSchedule, "throws on mutation when not started", func(t *testing.T) {
		s := newSubagentScheduler(nil)
		_, err := s.addJob(input("x", "1h"))
		eq(t, err != nil && strings.Contains(err.Error(), "not started"), true)
	})
	tw(t, fSchedule, "list() returns empty array when not started", func(t *testing.T) {
		eq(t, newSubagentScheduler(nil).list(), []scheduledSubagent{})
	})
}
