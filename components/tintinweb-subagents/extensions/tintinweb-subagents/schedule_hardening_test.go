package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// What the scheduler does that the original gets from its runtime: a thrown error does not stop the process or the
// other jobs, a timer never outlives its job, an event has the keys the original's objects have, and the store reads a
// damaged file the way JSON.parse and a Map would.

func TestScheduleEventsHaveTheOriginalsKeys(t *testing.T) {
	job := makeJob(nil)
	for _, c := range []struct {
		ev   scheduleChange
		want string
	}{
		{scheduleChange{Type: "removed", JobID: "j"}, `{"type":"removed","jobId":"j"}`},
		{scheduleChange{Type: "fired", JobID: "j", AgentID: "a", Name: ""}, `{"type":"fired","jobId":"j","agentId":"a","name":""}`},
		{scheduleChange{Type: "fired", JobID: "j", AgentID: "a", Name: "n"}, `{"type":"fired","jobId":"j","agentId":"a","name":"n"}`},
		{scheduleChange{Type: "error", JobID: "j", Error: ""}, `{"type":"error","jobId":"j","error":""}`},
		{scheduleChange{Type: "error", JobID: "j", Error: "boom"}, `{"type":"error","jobId":"j","error":"boom"}`},
	} {
		raw, err := json.Marshal(c.ev)
		eq(t, err, nil)
		eq(t, string(raw), c.want)
	}
	for _, kind := range []string{"added", "updated"} {
		raw, err := json.Marshal(scheduleChange{Type: kind, Job: &job})
		eq(t, err, nil)
		eq(t, strings.HasPrefix(string(raw), `{"type":"`+kind+`","job":{"id":"`+job.ID+`","name":"test-job"`), true)
		eq(t, strings.HasSuffix(string(raw), `}}`), true)
	}
}

func TestAPanicInTheManagerIsAFireErrorNotACrash(t *testing.T) {
	clock := newFakeClock()
	m := newMockManager()
	m.panics = "spawn"
	r := startSched(t, clock, m)
	job, err := r.sched.addJob(input("boom", "1m"))
	eq(t, err, nil)
	clock.advance(time.Minute)
	got := r.job(t, job.ID)
	eq(t, got.LastStatus, "error")
	eq(t, got.LastRun != "", true)
	eq(t, r.events.has(func(e scheduleChange) bool { return e.Type == "error" && e.JobID == job.ID && e.Error == "spawn went wrong" }), true)
	clock.advance(time.Minute) // the interval is still armed
	eq(t, len(m.spawned()), 2)
}

func TestAPanicWhileWaitingForTheRunIsARunError(t *testing.T) {
	clock := newFakeClock()
	m := newFaithfulManager("completed", true)
	m.panics = "startup"
	r := startSched(t, clock, m)
	job, _ := r.sched.addJob(input("boom", "1m"))
	clock.advance(time.Minute)
	waitFor(t, 5*time.Second, "outcome", func() bool { return r.job(t, job.ID).RunCount == 1 })
	eq(t, r.job(t, job.ID).LastStatus, "error")
}

func TestARealTimerSurvivesAPanickingCallback(t *testing.T) {
	// A panic that escaped the timer's goroutine would end this test binary.
	done := make(chan struct{})
	realClock{}.afterFunc(time.Millisecond, func() { panic("a fire went wrong") })
	realClock{}.afterFunc(30*time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the second timer never fired")
	}
}

func TestAnIntervalKeepsTickingAfterAPanickingFire(t *testing.T) {
	clock := newFakeClock()
	var timer rearmable
	fires := 0
	timer.every(clock, 1000, func() {
		fires++
		if fires == 1 {
			panic("a fire went wrong")
		}
	})
	func() {
		defer func() { _ = recover() }() // the fake clock runs the callback in the caller; a real timer drops the panic
		clock.advance(time.Second)
	}()
	clock.advance(time.Second)
	eq(t, fires, 2)
	timer.stop()
	clock.advance(time.Hour)
	eq(t, fires, 2)
}

func TestStartingTwiceDoesNotDoubleTheTimers(t *testing.T) {
	clock := newFakeClock()
	r := startSched(t, clock, newMockManager())
	_, _ = r.sched.addJob(input("hourly", "1h"))
	_, _ = r.sched.addJob(input("cron", "0 0 * * * *"))
	r.sched.start(r.manager, r.store, r.events.emit)
	clock.advance(25 * time.Hour)
	// The interval fired 25 times and the cron job 24 or 25, each once per due time (never twice).
	var interval, cron int
	for _, e := range r.events.all() {
		if e.Type == "fired" {
			if e.Name == "hourly" {
				interval++
			} else {
				cron++
			}
		}
	}
	eq(t, interval, 25)
	eq(t, cron >= 24 && cron <= 25, true)
}

func TestTheSchedulerHoldsNoTimerForARemovedOrStoppedJob(t *testing.T) {
	clock := newFakeClock()
	r := startSched(t, clock, newMockManager())
	a, _ := r.sched.addJob(input("a", "1m"))
	b, _ := r.sched.addJob(input("b", "+10m"))
	c, _ := r.sched.addJob(input("c", "0 0 * * * *"))
	pending := func() int {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		n := 0
		for _, t := range clock.timers {
			if !t.stopped {
				n++
			}
		}
		return n
	}
	eq(t, pending(), 3)
	for _, id := range []string{a.ID, b.ID, c.ID} {
		ok, err := r.sched.removeJob(id)
		eq(t, ok, true)
		eq(t, err, nil)
	}
	eq(t, pending(), 0)
	_, _ = r.sched.addJob(input("d", "1m"))
	r.sched.stop()
	eq(t, pending(), 0)
}

func TestTheSchedulerIsSafeUnderConcurrentUse(t *testing.T) {
	// Real timers, many goroutines; the race detector is the assertion.
	r := startSched(t, nil, newFaithfulManager("completed", true))
	var wg sync.WaitGroup
	for g := range 6 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 6 {
				name := "job-" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
				job, err := r.sched.addJob(input(name, "1h"))
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = r.sched.getNextRun(job.ID)
				_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.Enabled = i%2 == 0 })
				_ = r.sched.list()
				if i%3 == 0 {
					_, _ = r.sched.removeJob(job.ID)
				}
			}
		}(g)
	}
	wg.Wait()
	eq(t, len(r.sched.list()), 6*4)
	eq(t, len(newScheduleStore(r.store.filePath).list()), 6*4)
}

func TestTheStoreReadsADamagedFileAsTheOriginalDoes(t *testing.T) {
	good := `{"version":1,"jobs":[{"id":"keep","name":"keep","enabled":true}]}`
	for _, c := range []struct {
		name, body string
		ids        []string
	}{
		{"not JSON: the cache stays", `{`, []string{"keep"}},
		{"null: the cache stays", `null`, []string{"keep"}},
		{"a list: no jobs", `[]`, nil},
		{"a number: no jobs", `5`, nil},
		{"no jobs key", `{"version":1}`, nil},
		{"jobs is not a list", `{"jobs":5}`, nil},
		{"jobs is null", `{"jobs":null}`, nil},
		{"a job with a field of the wrong type is skipped", `{"jobs":[{"id":"a","name":"a","enabled":"yes"},{"id":"b","name":"b","enabled":true}]}`, []string{"b"}},
		{"a record that is not an object ends the list", `{"jobs":[{"id":"a"},null,{"id":"c"}]}`, []string{"a"}},
		{"the last record of an id wins, in the first one's place", `{"jobs":[{"id":"a","name":"x"},{"id":"b"},{"id":"a","name":"y"}]}`, []string{"a", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "s.json")
			eq(t, os.WriteFile(file, []byte(good), 0o644), nil)
			store := newScheduleStore(file)
			eq(t, len(store.list()), 1)
			eq(t, os.WriteFile(file, []byte(c.body), 0o644), nil)
			store.mu.Lock()
			store.load()
			store.mu.Unlock()
			var ids []string
			for _, j := range store.list() {
				ids = append(ids, j.ID)
			}
			eq(t, ids, c.ids)
		})
	}
	// The last record of an id replaces the first in place.
	file := filepath.Join(t.TempDir(), "dup.json")
	eq(t, os.WriteFile(file, []byte(`{"jobs":[{"id":"a","name":"x"},{"id":"b"},{"id":"a","name":"y"}]}`), 0o644), nil)
	got, _ := newScheduleStore(file).get("a")
	eq(t, got.Name, "y")
}

func TestAgentsListsTheScheduledJobs(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, t.TempDir())
	now := time.Now().UnixMilli()
	eq(t, r.app.scheduleListing(now), []string{"", "Scheduled jobs (0):", "  (none)"})
	r.must("Agent", obj{"prompt": "p", "description": "nightly sweep", "subagent_type": "Plan", "schedule": "1h"})
	lines := r.app.scheduleListing(now)
	eq(t, len(lines), 4)
	eq(t, lines[:2], []string{"", "Scheduled jobs (1):"})
	eq(t, strings.HasPrefix(lines[2], "  ✓  nightly sweep       1h              [Plan]  next in "), true)
	eq(t, lines[3], "  "+refusalText("cancelling a scheduled job (the /agents Scheduled jobs menu)"))

	r.Command("agents", "")
	var msg string
	for _, c := range r.CallsTo("ui.notify") {
		msg, _ = c.Args["message"].(string)
	}
	eq(t, strings.Contains(msg, "\n\nScheduled jobs (1):\n  ✓  nightly sweep"), true)

	r.app.scheduler().stop()
	eq(t, r.app.scheduleListing(now) == nil, true) // not active: no entry, as the original's menu
}

func TestTwoStoresOnOneFileDoNotLoseEachOthersJobs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.json")
	a, b := newScheduleStore(file), newScheduleStore(file) // two processes: each has its own cache
	first, second := makeJob(func(j *scheduledSubagent) { j.Name = "first" }), makeJob(func(j *scheduledSubagent) { j.Name = "second" })
	eq(t, a.add(first), nil)
	eq(t, b.add(second), nil) // b's cache is stale: the mutation reloads the file under the lock before it applies
	got := newScheduleStore(file).list()
	eq(t, len(got), 2)
	eq(t, got[0].Name, "first")
	eq(t, got[1].Name, "second")
	_, ok, err := a.update(second.ID, func(j *scheduledSubagent) { j.RunCount = 7 }) // a never saw "second"
	eq(t, err, nil)
	eq(t, ok, false) // an id the cache does not know changes nothing, as in the original
}

func TestAScheduledCallResolvesLikeAnImmediateOne(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	eq(t, os.MkdirAll(filepath.Join(cwd, ".pi", "agents"), 0o755), nil)
	eq(t, os.WriteFile(filepath.Join(cwd, ".pi", "agents", "pinned.md"),
		[]byte("---\ndescription: pinned\nthinking: high\nmax_turns: 7\nisolated: true\n---\nWork.\n"), 0o644), nil)
	writeProjectSettings(t, cwd, `{"defaultMaxTurns": 25}`)
	r := startRigIn(t, cwd)
	// The agent file outranks the call.
	r.must("Agent", obj{"prompt": "p", "description": "pinned job", "subagent_type": "pinned", "schedule": "1h", "thinking": "low", "max_turns": float64(2), "isolated": false, "model": "m1"})
	// Nothing else names a turn limit: the configured default applies. An explicit 0 means unlimited and is not stored.
	r.must("Agent", obj{"prompt": "p", "description": "plain job", "subagent_type": "Plan", "schedule": "1h"})
	r.must("Agent", obj{"prompt": "p", "description": "unlimited job", "subagent_type": "Plan", "schedule": "1h", "max_turns": float64(0)})
	jobs := r.app.scheduler().list()
	eq(t, len(jobs), 3)
	eq(t, *jobs[0].Thinking, "high")
	eq(t, *jobs[0].MaxTurns, 7)
	eq(t, *jobs[0].Isolated, true)
	eq(t, *jobs[0].Model, "m1")
	eq(t, jobs[0].SubagentType, "pinned")
	eq(t, jobs[1].Thinking == nil, true)
	eq(t, *jobs[1].MaxTurns, 25)
	eq(t, *jobs[1].Isolated, false)
	eq(t, jobs[1].Model == nil, true)
	eq(t, jobs[2].MaxTurns == nil, true)
}

func TestTheSchedulingSettingIsReadFromTheGlobalAndProjectFiles(t *testing.T) {
	agentDirectory := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDirectory)
	write := func(path, body string) {
		eq(t, os.MkdirAll(filepath.Dir(path), 0o755), nil)
		eq(t, os.WriteFile(path, []byte(body), 0o644), nil)
	}
	cwd := t.TempDir()
	for _, c := range []struct {
		name, global, project string
		want                  bool
	}{
		{"neither file says", `{}`, `{}`, true},
		{"global off", `{"schedulingEnabled": false}`, `{}`, false},
		{"project off", `{}`, `{"schedulingEnabled": false}`, false},
		{"the project file wins over the global one (on)", `{"schedulingEnabled": false}`, `{"schedulingEnabled": true}`, true},
		{"the project file wins over the global one (off)", `{"schedulingEnabled": true}`, `{"schedulingEnabled": false}`, false},
		{"a value that is not a boolean is dropped", `{"schedulingEnabled": false}`, `{"schedulingEnabled": "yes"}`, false},
		{"a file that is not JSON is ignored", `{"schedulingEnabled": false}`, `{`, false},
	} {
		write(filepath.Join(agentDirectory, "subagents.json"), c.global)
		write(filepath.Join(cwd, ".pi", "subagents.json"), c.project)
		eq(t, scheduleSettingEnabled(cwd), c.want)
	}
}

func TestTheSchedulerStaysOffUntilTheHostHasASessionId(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigWith(t, t.TempDir(), true)
	eq(t, r.app.scheduler().isActive(), false)
	eq(t, len(r.emittedOn("subagents:scheduler_ready")), 0)
}

func TestAnIdleSchedulerHoldsNoTimer(t *testing.T) {
	s := newSubagentScheduler(nil)
	s.start(newMockManager(), newScheduleStore(filepath.Join(t.TempDir(), "s.json")), nil)
	eq(t, len(s.crons)+len(s.intervals), 0) // no jobs: nothing armed, no goroutine started
	s.stop()
	// With jobs, one timer each, and stop() drops them all.
	file := seedStore(t, 6)
	s.start(newMockManager(), newScheduleStore(file), nil)
	eq(t, len(s.crons)+len(s.intervals), 6)
	s.stop()
	eq(t, len(s.crons)+len(s.intervals), 0)
}

// seedStore writes n jobs, a third each of cron, interval and one-shot, to a store file.
func seedStore(tb testing.TB, n int) string {
	tb.Helper()
	file := filepath.Join(tb.TempDir(), "s.json")
	store := newScheduleStore(file)
	every := 300_000.0
	for i := range n {
		job := makeJob(func(j *scheduledSubagent) {
			j.Name = "job-" + strconv.Itoa(i)
			switch i % 3 {
			case 0:
				j.Schedule, j.ScheduleType, j.IntervalMs = "0 30 8 * * 1-5", "cron", nil
			case 1:
				j.Schedule, j.ScheduleType, j.IntervalMs = "5m", "interval", &every
			default:
				j.Schedule, j.ScheduleType, j.IntervalMs = isoString(time.Now().Add(48*time.Hour)), "once", nil
			}
		})
		if err := store.add(job); err != nil {
			tb.Fatal(err)
		}
	}
	return file
}

// The cost of a session that starts with schedules on disk: read the file and arm every enabled job.
func BenchmarkStartWithSchedulesLoaded(b *testing.B) {
	for _, n := range []int{0, 10, 100} {
		b.Run(strconv.Itoa(n)+" jobs", func(b *testing.B) {
			file := seedStore(b, n)
			for b.Loop() {
				s := newSubagentScheduler(nil)
				s.start(newMockManager(), newScheduleStore(file), nil)
				s.stop()
			}
		})
	}
}

// croner's search for the next run: a common pattern, one that needs a calendar walk, and one that never matches (the
// search runs to the year 3000).
func BenchmarkCronNext(b *testing.B) {
	for _, pattern := range []string{"0 30 8 * * 1-5", "0 0 12 * * 5#2", "0 0 0 31 2 *"} {
		b.Run(pattern, func(b *testing.B) {
			c, err := newCron(pattern)
			if err != nil {
				b.Fatal(err)
			}
			from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			for b.Loop() {
				c.next(from)
			}
		})
	}
}

func TestAJobStoredWithAWorktreeIsRefusedWhenItFiresNotRunWithoutOne(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	every := 100.0
	store := newScheduleStore(resolveStorePath(cwd, rigSessionID)) // a session of the original, resumed here
	_ = store.add(makeJob(func(j *scheduledSubagent) { j.Schedule, j.IntervalMs, j.Isolation = "100ms", &every, "worktree" }))
	r := startRigIn(t, cwd)
	waitFor(t, 5*time.Second, "error event", func() bool {
		for _, ev := range r.emittedOn("subagents:scheduled") {
			if ev["type"] == "error" && ev["error"] == refusalText(`worktree isolation (isolation: "worktree")`) {
				return true
			}
		}
		return false
	})
	eq(t, r.fleet.count(), 0)
	eq(t, r.app.scheduler().list()[0].LastStatus, "error")
}
