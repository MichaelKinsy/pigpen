package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The scheduler beyond the original's own tests: croner's loop on a clock, the timers the original gets wrong, and the
// extension around it (session binding, events, the notification, the concurrency bypass, the tool text).

func newFakeClockAt(t time.Time) *fakeClock { return &fakeClock{t: t} }

func TestCronJobsFireOnTheirSchedule(t *testing.T) {
	withLocal(t, "UTC")
	clock := newFakeClockAt(time.Date(2026, 10, 20, 9, 0, 1, 500e6, time.UTC))
	r := startSched(t, clock, newMockManager())
	job, err := r.sched.addJob(input("every-5s", "*/5 * * * * *"))
	eq(t, err, nil)
	eq(t, job.ScheduleType, "cron")
	next, ok := r.sched.getNextRun(job.ID)
	eq(t, ok, true)
	eq(t, next, "2026-10-20T09:00:05.000Z")

	clock.advance(3 * time.Second) // 09:00:04.500: not yet
	eq(t, len(r.manager.spawned()), 0)
	clock.advance(time.Second) // 09:00:05.500: the 05 run
	eq(t, len(r.manager.spawned()), 1)
	clock.advance(5 * time.Second)
	eq(t, len(r.manager.spawned()), 2)
	next, _ = r.sched.getNextRun(job.ID)
	eq(t, next, "2026-10-20T09:00:15.000Z")

	_, _, _ = r.sched.updateJob(job.ID, func(j *scheduledSubagent) { j.Enabled = false })
	clock.advance(time.Minute)
	eq(t, len(r.manager.spawned()), 2)
}

func TestCronJobWaitsInPiecesAndFiresOnTime(t *testing.T) {
	withLocal(t, "UTC")
	clock := newFakeClockAt(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	r := startSched(t, clock, newMockManager())
	job, _ := r.sched.addJob(input("noon", "0 0 12 * * *"))
	clock.advance(3*time.Hour - time.Second)
	eq(t, len(r.manager.spawned()), 0)
	clock.advance(time.Second)
	eq(t, len(r.manager.spawned()), 1)
	next, _ := r.sched.getNextRun(job.ID)
	eq(t, next, "2026-10-21T12:00:00.000Z")
}

func TestLongTimersWaitTheirWholeDelay(t *testing.T) {
	// Node's setTimeout and setInterval turn a delay above 2^31-1 ms (24.8 days) into 1 ms, so a one-shot a month away
	// fires at once and a 30-day interval fires every millisecond. Here the delay is what was asked for (PORT.md).
	clock := newFakeClockAt(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	r := startSched(t, clock, newMockManager())
	once, _ := r.sched.addJob(input("in-a-month", "+30d"))
	every, _ := r.sched.addJob(input("monthly", "30d"))
	eq(t, once.ScheduleType, "once")
	clock.advance(29 * 24 * time.Hour)
	eq(t, len(r.manager.spawned()), 0)
	clock.advance(24*time.Hour + time.Second)
	eq(t, len(r.manager.spawned()), 2)
	eq(t, r.job(t, once.ID).Enabled, false)
	eq(t, r.job(t, every.ID).Enabled, true)
	clock.advance(30 * 24 * time.Hour)
	eq(t, len(r.manager.spawned()), 3) // the interval again, nothing else
}

func TestJobsAreArmedAgainFromTheStore(t *testing.T) {
	clock := newFakeClockAt(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	r := startSched(t, clock, newMockManager())
	_, _ = r.sched.addJob(input("hourly", "1h"))
	off, _ := r.sched.addJob(input("off", "1h"))
	_, _, _ = r.sched.updateJob(off.ID, func(j *scheduledSubagent) { j.Enabled = false })
	r.sched.stop()
	clock.advance(2 * time.Hour)
	eq(t, len(r.manager.spawned()), 0) // stopped: nothing fires

	r.sched.start(r.manager, newScheduleStore(r.dir+"/s.json"), r.events.emit) // /resume: the session's file is read again
	clock.advance(time.Hour)
	eq(t, len(r.manager.spawned()), 1) // the enabled job only
}

func TestAnInvalidCronExpressionOnReloadIsAnErrorEvent(t *testing.T) {
	r := startSched(t, nil, newMockManager())
	eq(t, r.store.add(makeJob(func(j *scheduledSubagent) {
		j.ID, j.Name, j.Schedule, j.ScheduleType, j.IntervalMs = "bad", "bad", "not a cron", "cron", nil
	})), nil)
	r.sched.stop()
	r.sched.start(r.manager, r.store, r.events.emit)
	eq(t, r.events.has(func(e scheduleChange) bool { return e.Type == "error" && e.JobID == "bad" && e.Error != "" }), true)
}

func TestInvalidSchedulesAreRefusedWithTheOriginalsMessage(t *testing.T) {
	now := time.Now()
	for _, s := range []string{"", "soon", "* * * * *", "0 0 25 * * *", "0 0 9 * * 1 1", "+5x", "2020-01-01T00:00:00Z junk"} {
		_, err := detectSchedule(s, now)
		eq(t, err != nil && err.Error() == `Invalid schedule "`+s+`". Use 6-field cron (e.g. "0 0 9 * * 1" — 9am every Monday), interval ("5m"/"1h"), or one-shot ("+10m" / ISO).`, true)
	}
	_, err := detectSchedule("2020-01-01T00:00:00Z", now)
	eq(t, err.Error(), "Scheduled time 2020-01-01T00:00:00.000Z is in the past.")
	_, _, err = parseRelativeTime("+99999999999999d", now)
	eq(t, err.Error(), "Invalid time value")
	// The schedule is trimmed, and an interval keeps the trimmed text.
	d, err := detectSchedule("  5m \n", now)
	eq(t, err, nil)
	eq(t, d.Normalized, "5m")
}

func TestStoreFileHasTheOriginalsShape(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.json")
	store := newScheduleStore(file)
	job := makeJob(func(j *scheduledSubagent) {
		j.Model, j.MaxTurns = ptrOf("anthropic/<x>&y"), ptrOf(4)
	})
	_ = store.add(job)
	_, _, _ = store.update(job.ID, func(j *scheduledSubagent) { j.LastStatus = "running" })
	_, _, _ = store.update(job.ID, func(j *scheduledSubagent) {
		j.LastRun, j.LastStatus, j.RunCount, j.NextRun = "2026-01-01T00:00:00.000Z", "success", 1, "2026-01-01T00:05:00.000Z"
	})
	raw, _ := os.ReadFile(file)
	want := "{\n  \"version\": 1,\n  \"jobs\": [\n    {\n      \"id\": \"" + job.ID + "\",\n      \"name\": \"test-job\",\n      \"description\": \"test\",\n      \"schedule\": \"5m\",\n" +
		"      \"scheduleType\": \"interval\",\n      \"intervalMs\": 300000,\n      \"subagent_type\": \"general-purpose\",\n      \"prompt\": \"hello\",\n" +
		"      \"model\": \"anthropic/<x>&y\",\n      \"max_turns\": 4,\n      \"enabled\": true,\n      \"createdAt\": \"" + job.CreatedAt + "\",\n      \"runCount\": 1,\n" +
		"      \"lastStatus\": \"success\",\n      \"lastRun\": \"2026-01-01T00:00:00.000Z\",\n      \"nextRun\": \"2026-01-01T00:05:00.000Z\"\n    }\n  ]\n}"
	eq(t, string(raw), want) // JSON.stringify(data, null, 2): two-space indent, no trailing newline, no HTML escaping
	empty := filepath.Join(t.TempDir(), "e.json")
	e := newScheduleStore(empty)
	_ = e.add(job)
	_, _ = e.remove(job.ID)
	raw, _ = os.ReadFile(empty)
	eq(t, string(raw), "{\n  \"version\": 1,\n  \"jobs\": []\n}")
}

func TestTheStoreSerializesMutationsFromManyGoroutines(t *testing.T) {
	store := newScheduleStore(filepath.Join(t.TempDir(), "s.json"))
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = store.add(makeJob(nil))
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	eq(t, len(store.list()), 8)
	eq(t, len(newScheduleStore(store.filePath).list()), 8)
}

func TestAJobIsRefusedWhenAnotherProcessHoldsTheLock(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the lock retries")
	}
	file := filepath.Join(t.TempDir(), "s.json")
	store := newScheduleStore(file)
	eq(t, os.WriteFile(file+".lock", []byte(itoa(os.Getpid())), 0o644), nil) // this process is alive: the lock is held
	start := time.Now()
	err := store.add(makeJob(nil))
	eq(t, err != nil && strings.Contains(err.Error(), "Failed to acquire schedule lock: "+file+".lock"), true)
	eq(t, time.Since(start) >= 4*time.Second, true) // 100 tries, 50 ms apart
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// ---- the extension around the scheduler ----

// emitted returns the payloads of the events the extension published on a channel.
func (r *rig) emittedOn(channel string) []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("events.emit") {
		if c.Args["channel"] == channel {
			m, _ := c.Args["json"].(map[string]any)
			out = append(out, m)
		}
	}
	return out
}

func writeProjectSettings(t *testing.T, cwd, body string) {
	t.Helper()
	eq(t, os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755), nil)
	eq(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(body), 0o644), nil)
}

func TestSessionStartBindsTheScheduler(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	store := newScheduleStore(resolveStorePath(cwd, rigSessionID))
	_ = store.add(makeJob(func(j *scheduledSubagent) { j.Name, j.Enabled = "a", false }))
	_ = store.add(makeJob(func(j *scheduledSubagent) { j.Name, j.Enabled = "b", false }))
	r := startRigIn(t, cwd)
	eq(t, r.app.scheduler().isActive(), true)
	ready := r.emittedOn("subagents:scheduler_ready")
	eq(t, ready, []map[string]any{{"sessionId": rigSessionID, "jobCount": float64(2)}})
	entry, ok := r.app.scheduleMenuEntry()
	eq(t, ok, true)
	eq(t, entry, "Scheduled jobs (2)")
}

func TestSchedulingDisabledMeansNoSchedulerNoParameterAndNoEvent(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	writeProjectSettings(t, cwd, `{"schedulingEnabled": false}`)
	r := startRigIn(t, cwd)
	eq(t, r.app.scheduler().isActive(), false)
	eq(t, len(r.emittedOn("subagents:scheduler_ready")), 0)
	_, ok := r.app.scheduleMenuEntry()
	eq(t, ok, false)
	schema := r.app.withScheduleParam(agentToolSchema(currentRegistry(), loadToolSpecs()["Agent"]))
	_, has := schema["properties"].(map[string]any)["schedule"]
	eq(t, has, false)
	eq(t, strings.Contains(r.app.withScheduleGuideline(agentToolDescription(currentRegistry())), "schedule"), false)
}

func TestTheAgentToolTextCarriesTheScheduleParameterAndGuideline(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRig(t)
	schema := r.app.withScheduleParam(agentToolSchema(currentRegistry(), loadToolSpecs()["Agent"]))
	props := schema["properties"].(map[string]any)
	eq(t, props["schedule"], map[string]any{"type": "string", "description": scheduleParamDescription})
	for _, required := range schema["required"].([]any) {
		eq(t, required != "schedule", true) // optional
	}
	desc := r.app.withScheduleGuideline(agentToolDescription(currentRegistry()))
	want := "- Use inherit_context if the agent needs the parent conversation history.\n- Use `schedule` only when the user explicitly asked for scheduled / recurring / delayed execution " +
		"(e.g. \"every Monday\", \"in an hour\"). Don't auto-schedule from vague intent like \"monitor X\" — run once now or ask.\n\n## Writing the prompt"
	eq(t, strings.Contains(desc, want), true)
	eq(t, r.app.withScheduleGuideline(desc), desc) // idempotent
}

// A scheduled run fires, runs as a background agent, and its completion reaches the parent as the notification every
// background agent gets; the job records the outcome.
func TestAScheduledRunFiresAndNotifies(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	r := startRigIn(t, cwd)
	reply := r.must("Agent", obj{"prompt": "sweep the repo", "description": "nightly sweep", "subagent_type": "Explore", "schedule": "+1s", "thinking": "low", "max_turns": float64(3)})
	eq(t, strings.HasPrefix(reply, `Scheduled "nightly sweep" (id: `), true)
	eq(t, strings.Contains(reply, ", type: once). Next run: "), true)
	eq(t, strings.HasSuffix(reply, ". Manage via /agents → Scheduled jobs."), true)
	eq(t, r.fleet.count(), 0) // nothing spawned now
	jobs := r.app.scheduler().list()
	eq(t, len(jobs), 1)

	c := r.fleet.wait(t, 1)[0] // fires about a second later
	eq(t, c.spec.Prompt, "sweep the repo")
	eq(t, c.spec.Thinking, "low")
	eq(t, c.spec.MaxTurns, 3)
	c.finish("swept")
	r.waitEmitted("subagents:completed")
	waitFor(t, 5*time.Second, "notification", func() bool { return len(r.messages()) == 1 })
	m := r.messages()[0]["message"].(map[string]any)
	eq(t, m["customType"], "subagent-notification")
	content := m["content"].(string)
	for _, want := range []string{"<status>Done</status>", `Agent "nightly sweep" completed`, "<result>swept</result>"} {
		eq(t, strings.Contains(content, want), true)
	}
	waitFor(t, 5*time.Second, "outcome recorded", func() bool { return r.app.scheduler().list()[0].LastStatus == "success" })
	done := r.app.scheduler().list()[0]
	eq(t, done.RunCount, 1)
	eq(t, done.Enabled, false)

	var types []string
	for _, ev := range r.emittedOn("subagents:scheduled") {
		types = append(types, ev["type"].(string))
	}
	eq(t, types, []string{"added", "fired", "updated"})
	fired := r.emittedOn("subagents:scheduled")[1]
	eq(t, fired["jobId"], jobs[0].ID)
	eq(t, fired["name"], "nightly sweep")
	eq(t, fired["agentId"], r.app.mgr.list()[0].ID)
}

func TestAScheduledFireBypassesTheConcurrencyQueueAndHoldsNoSlot(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	writeProjectSettings(t, cwd, `{"maxConcurrent": 1}`)
	r := startRigIn(t, cwd)
	r.must("Agent", obj{"prompt": "a", "description": "first", "subagent_type": "Plan"})
	first := r.fleet.wait(t, 1)[0]
	text := r.must("Agent", obj{"prompt": "b", "description": "second", "subagent_type": "Plan"})
	eq(t, strings.Contains(text, "Agent queued in background."), true)
	r.must("Agent", obj{"prompt": "c", "description": "scheduled", "subagent_type": "Plan", "schedule": "+1s"})

	scheduled := r.fleet.wait(t, 2)[1] // the fire does not wait behind the running agent
	eq(t, scheduled.spec.Prompt, "c")
	eq(t, r.fleet.count(), 2) // and the queued one still waits
	scheduled.finish("c done")
	r.waitEmitted("subagents:completed")
	time.Sleep(100 * time.Millisecond)
	eq(t, r.fleet.count(), 2) // the bypassing agent held no slot, so its end frees none
	first.finish("a done")
	r.fleet.wait(t, 3) // the queued one starts when the real slot frees
}

func TestJobsOfThePreviousRunStartWithTheSession(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	store := newScheduleStore(resolveStorePath(cwd, rigSessionID))
	every := 150.0
	_ = store.add(makeJob(func(j *scheduledSubagent) {
		j.Name, j.Schedule, j.IntervalMs, j.Prompt = "tick", "150ms", &every, "tick"
	}))
	r := startRigIn(t, cwd)
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.Prompt, "tick")
	c.finish("ok")
	waitFor(t, 5*time.Second, "recorded", func() bool { return r.app.scheduler().list()[0].RunCount >= 1 })
}

func TestASessionSwitchAndShutdownStopTheScheduler(t *testing.T) {
	for _, event := range []string{"session_before_switch", "session_shutdown"} {
		t.Run(event, func(t *testing.T) {
			t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
			cwd := t.TempDir()
			store := newScheduleStore(resolveStorePath(cwd, rigSessionID))
			every := 200.0
			_ = store.add(makeJob(func(j *scheduledSubagent) { j.Schedule, j.IntervalMs = "200ms", &every }))
			r := startRigIn(t, cwd)
			eq(t, r.app.scheduler().isActive(), true)
			r.FireAll(event, obj{})
			eq(t, r.app.scheduler().isActive(), false)
			time.Sleep(500 * time.Millisecond)
			eq(t, r.fleet.count(), 0) // stopped before the first tick
		})
	}
}

func TestAScheduleOfWhatThePortDoesNotDoIsRefusedNotScheduled(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	eq(t, os.MkdirAll(filepath.Join(cwd, ".pi", "agents"), 0o755), nil)
	eq(t, os.WriteFile(filepath.Join(cwd, ".pi", "agents", "memo.md"), []byte("---\ndescription: remembers\nmemory: project\n---\nRemember.\n"), 0o644), nil)
	r := startRigIn(t, cwd)
	for name, params := range map[string]obj{
		"worktree isolation":                 {"isolation": "worktree"},
		"an agent file that asks for memory": {"subagent_type": "memo"},
	} {
		call := obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "schedule": "1h"}
		for k, v := range params {
			call[k] = v
		}
		text, failure := r.tool("Agent", call)
		eq(t, text, "")
		eq(t, strings.Contains(failure, "does not support"), true)
		eq(t, len(r.app.scheduler().list()), 0)
		t.Log(name, failure)
	}
}

func TestScheduleRejectsABadScheduleAndADuplicateName(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, t.TempDir())
	call := func(schedule string) string {
		return r.must("Agent", obj{"prompt": "p", "description": "same name", "subagent_type": "Plan", "schedule": schedule})
	}
	eq(t, strings.HasPrefix(call("every now and then"), `Invalid schedule "every now and then". Use 6-field cron`), true)
	eq(t, strings.HasPrefix(call("1h"), `Scheduled "same name" (id: `), true)
	eq(t, call("2h"), `A scheduled job named "same name" already exists.`)
	eq(t, len(r.app.scheduler().list()), 1)
	// An empty `schedule` is not a schedule: the call runs now.
	r.must("Agent", obj{"prompt": "p", "description": "now", "subagent_type": "Plan", "schedule": ""})
	r.fleet.wait(t, 1)
}

func TestTheSchedulerIsNotActiveBeforeTheSessionStarted(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	// A session id the host cannot give: the scheduler stays off and a call says so.
	r := startRigIn(t, t.TempDir())
	r.app.scheduler().stop()
	text := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "schedule": "1h"})
	eq(t, text, "Scheduler is not active in this session yet. Try again after the session has fully started.")
}

func TestScheduleMenuData(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, t.TempDir())
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli()
	iso := func(d time.Duration) string { return isoString(time.UnixMilli(now).Add(d)) }
	for in, want := range map[string]string{
		"": "—", "garbage": "—", iso(30 * time.Second): "in <1m", iso(-30 * time.Second): "<1m ago", iso(4 * time.Minute): "in 4m", iso(-59 * time.Minute): "59m ago",
		iso(90 * time.Minute): "in 2h", iso(-23 * time.Hour): "23h ago", iso(36 * time.Hour): "in 2d", iso(-49 * time.Hour): "2d ago", iso(59*time.Minute + 31*time.Second): "in 1h",
	} {
		eq(t, scheduleRelTime(in, now), want)
	}
	job := makeJob(func(j *scheduledSubagent) {
		j.Name, j.Schedule, j.SubagentType, j.RunCount, j.LastRun, j.Prompt = "review the auth module A", "0 0 9 * * 1", "Plan", 3, iso(-2*time.Hour), strings.Repeat("é", 201)
		j.ScheduleType, j.IntervalMs, j.LastStatus = "cron", nil, "error"
	})
	eq(t, scheduleStatusIcon(job), "!")
	job.LastStatus = "running"
	eq(t, scheduleStatusIcon(job), "⋯")
	job.LastStatus = ""
	eq(t, scheduleStatusIcon(job), "✓")
	job.Enabled = false
	eq(t, scheduleStatusIcon(job), "✗")
	job.Enabled = true
	job.LastStatus = "success"
	row := r.app.scheduleFormatJob(job, now)
	eq(t, row, "✓  review the auth mo  0 0 9 * * 1     [Plan]  next —  last 2h ago  runs 3")
	details := r.app.scheduleFormatDetails(job)
	eq(t, strings.HasPrefix(details, "name:      review the auth module A\nschedule:  0 0 9 * * 1 (cron)\nagent:     Plan\nprompt:    "+strings.Repeat("é", 200)+"…\ncreated:   "), true)
	eq(t, strings.HasSuffix(details, "last run:  "+job.LastRun+" (success)\nnext run:  —\nruns:      3"), true)
}
