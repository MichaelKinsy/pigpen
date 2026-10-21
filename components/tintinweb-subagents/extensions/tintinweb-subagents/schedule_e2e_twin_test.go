package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Twins of the original's test/schedule-e2e.test.ts: the real-timer firing path, a faithful manager (a run settles with
// the right status) and the store on disk. Very short timings (100-300 ms) keep it fast.
const fScheduleE2E = "schedule-e2e"

func TestSchedulerEndToEnd(t *testing.T) {
	start := func(t *testing.T, status string) (*schedRig, string) {
		r := startSched(t, nil, newFaithfulManager(status, true))
		return r, filepath.Join(r.dir, "s.json")
	}
	tw(t, fScheduleE2E, "one-shot job: real setTimeout fires, agent runs, store reflects success", func(t *testing.T) {
		r, _ := start(t, "completed")
		// Fire ~100ms in the future. detectSchedule normalizes "+100ms", but the parser only accepts s/m/h/d, so use a
		// near-future ISO timestamp instead.
		future := isoString(time.Now().Add(100 * time.Millisecond))
		job, err := r.sched.addJob(input("e2e-once", future))
		eq(t, err, nil)
		eq(t, job.ScheduleType, "once")

		// Wait for the spawn (the real timer fires) and for the finalize chain to settle: polling, no fake clock.
		waitFor(t, 1500*time.Millisecond, "spawn", func() bool { return len(r.manager.spawned()) == 1 })
		waitFor(t, 1500*time.Millisecond, "success", func() bool { return r.job(t, job.ID).LastStatus == "success" })

		final := r.job(t, job.ID)
		eq(t, final.LastStatus, "success")
		eq(t, final.RunCount, 1)
		eq(t, final.Enabled, false) // one-shot auto-disabled
		eq(t, final.LastRun != "", true)
	})
	tw(t, fScheduleE2E, "one-shot job that errors: store records lastStatus error (regression — bug #1)", func(t *testing.T) {
		r, _ := start(t, "error") // the agent terminates with error status
		future := isoString(time.Now().Add(100 * time.Millisecond))
		job, _ := r.sched.addJob(input("e2e-fail", future))

		waitFor(t, 1500*time.Millisecond, "spawn", func() bool { return len(r.manager.spawned()) == 1 })
		waitFor(t, 1500*time.Millisecond, "finished", func() bool { return r.job(t, job.ID).LastStatus != "running" })

		eq(t, r.job(t, job.ID).LastStatus, "error")
		eq(t, r.job(t, job.ID).RunCount, 1)
	})
	tw(t, fScheduleE2E, "interval job: fires repeatedly, runCount grows", func(t *testing.T) {
		r, _ := start(t, "completed")
		// A 100 ms interval: wait for ~3 fires.
		job, _ := r.sched.addJob(input("e2e-interval", "100s")) // will be too long; override below
		// Replace with a literal 100ms interval, easier than a parseable shorthand for ms (parseInterval doesn't accept
		// "ms"): the persisted job is patched and re-armed.
		_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.IntervalMs, j.Schedule = ptrOf(100.0), "100ms" })

		waitFor(t, 2*time.Second, "three spawns", func() bool { return len(r.manager.spawned()) >= 3 })
		// The original's microtask order settles a fast agent before the next tick; a goroutine needs a moment.
		waitFor(t, 2*time.Second, "three runs recorded", func() bool { return r.job(t, job.ID).RunCount >= 3 })

		final := r.job(t, job.ID)
		eq(t, final.RunCount >= 3, true)
		eq(t, final.LastStatus, "success")
		eq(t, final.Enabled, true) // intervals don't auto-disable

		ok, _ := r.sched.removeJob(job.ID)
		eq(t, ok, true)
	})
	tw(t, fScheduleE2E, "persistence: schedules survive re-instantiating the store on the same file", func(t *testing.T) {
		r, file := start(t, "completed")
		future := isoString(time.Now().Add(60 * time.Second)) // far enough not to fire
		job, _ := r.sched.addJob(input("persistent", future))

		// Tear down the live scheduler; re-load from disk.
		r.sched.stop()
		reloaded := newScheduleStore(file)
		eq(t, len(reloaded.list()), 1)
		eq(t, reloaded.list()[0].ID, job.ID)
		eq(t, reloaded.list()[0].Name, "persistent")
	})
	tw(t, fScheduleE2E, "on-disk file shape: version=1 plus jobs array", func(t *testing.T) {
		r, file := start(t, "completed")
		_, _ = r.sched.addJob(input("shape-test", "1h"))

		raw, err := os.ReadFile(file)
		eq(t, err, nil)
		var onDisk struct {
			Version int              `json:"version"`
			Jobs    []map[string]any `json:"jobs"`
		}
		eq(t, json.Unmarshal(raw, &onDisk), nil)
		eq(t, onDisk.Version, 1)
		eq(t, len(onDisk.Jobs), 1)
		for k, want := range map[string]any{"name": "shape-test", "schedule": "1h", "scheduleType": "interval", "enabled": true, "runCount": float64(0)} {
			eq(t, onDisk.Jobs[0][k], want)
		}
	})
	tw(t, fScheduleE2E, "subagents:scheduled events fire across the lifecycle", func(t *testing.T) {
		r, _ := start(t, "completed")
		future := isoString(time.Now().Add(100 * time.Millisecond))
		job, _ := r.sched.addJob(input("events", future))

		waitFor(t, 1500*time.Millisecond, "spawn", func() bool { return len(r.manager.spawned()) == 1 })

		types := r.events.types()
		eq(t, hasString(types, "added"), true)
		eq(t, hasString(types, "fired"), true)

		_, _ = r.sched.removeJob(job.ID)
		eq(t, hasString(r.events.types(), "removed"), true)
	})
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
