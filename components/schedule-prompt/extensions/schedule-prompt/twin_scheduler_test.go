package schedule_prompt

import (
	"context"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Twins of the original's test/scheduler.test.ts (43 cases), with its inputs and expectations. The runSubagentOnce mock of the original
// is the scheduler's runner seam.

const (
	scheduler_ = "scheduler"
)

func contentText(t *testing.T, m customMessage) []string {
	t.Helper()
	blocks, ok := m.Content.([]any)
	if !ok {
		t.Fatalf("content = %#v", m.Content)
	}
	var out []string
	for _, b := range blocks {
		o := b.(*jsobj.Object)
		out = append(out, o.Get("text").(string))
	}
	return out
}

func detail(m customMessage, key string) any { return m.Details.(*jsobj.Object).Get(key) }

func TestSchedulerSubagentMarkers(t *testing.T) {
	tw(t, scheduler_, "posts a subagent_start marker with brief status text (no delivery options)", func(t *testing.T) {
		pi := &mockPi{}
		rc := &runnerCalls{}
		job := exampleJob(obj{"model": "haiku"})
		s := newSched(makeStorage(job), pi, makeCtx(), rc.runner(subagentResult{OK: true, Text: "result text"}))
		s.executeJobInSubagent(job)
		// The first call is the start marker, sent synchronously before the run starts.
		if pi.messageCount() < 1 {
			t.Fatal("no start marker")
		}
		m := pi.message(0)
		if detail(m.msg, "mode") != "subagent_start" || detail(m.msg, "model") != "haiku" {
			t.Fatalf("details = %v", m.msg.Details)
		}
		// Non-empty content prevents session poisoning.
		texts := contentText(t, m.msg)
		if len(texts) == 0 || len(texts[0]) == 0 {
			t.Fatal("empty content")
		}
		if m.opts != nil {
			t.Fatalf("opts = %+v", m.opts)
		}
	})

	done := func(t *testing.T, job *jsobj.Object, result subagentResult) (*mockPi, *memStorage, *scheduler) {
		pi := &mockPi{}
		st := makeStorage(job)
		s := newSched(st, pi, makeCtx(), (&runnerCalls{}).runner(result))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { return pi.messageCount() == 2 })
		return pi, st, s
	}

	tw(t, scheduler_, "posts a subagent_done marker with non-empty content when notify is unset (prevents session poisoning)", func(t *testing.T) {
		pi, _, _ := done(t, exampleJob(obj{"model": "haiku"}), subagentResult{OK: true, Text: "OK"})
		m := pi.message(1)
		if detail(m.msg, "mode") != "subagent_done" || detail(m.msg, "output") != "OK" {
			t.Fatalf("details = %v", m.msg.Details)
		}
		if texts := contentText(t, m.msg); len(texts) == 0 || texts[0] != "OK" {
			t.Fatalf("content = %v", texts)
		}
		if m.opts != nil {
			t.Fatalf("opts = %+v", m.opts)
		}
	})

	tw(t, scheduler_, "posts a wake-up subagent_done marker when notify is true (followUp + triggerTurn, snippet in content)", func(t *testing.T) {
		pi, _, _ := done(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{OK: true, Text: "OK"})
		m := pi.message(1)
		if texts := contentText(t, m.msg); len(texts) != 1 || texts[0] != "OK" {
			t.Fatalf("content = %v", texts)
		}
		if m.opts == nil || *m.opts != (deliverOptions{DeliverAs: "followUp", TriggerTurn: true}) {
			t.Fatalf("opts = %+v", m.opts)
		}
	})

	tw(t, scheduler_, "posts a subagent_error marker with notify-gated wake-up", func(t *testing.T) {
		pi, _, _ := done(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{Error: "model exploded"})
		m := pi.message(1)
		if detail(m.msg, "mode") != "subagent_error" || detail(m.msg, "error") != "model exploded" {
			t.Fatalf("details = %v", m.msg.Details)
		}
		if texts := contentText(t, m.msg); len(texts) != 1 || texts[0] != "model exploded" {
			t.Fatalf("content = %v", texts)
		}
		if m.opts == nil || *m.opts != (deliverOptions{DeliverAs: "followUp", TriggerTurn: true}) {
			t.Fatalf("opts = %+v", m.opts)
		}
	})

	tw(t, scheduler_, "truncates output longer than 500 chars with an ellipsis", func(t *testing.T) {
		pi, _, _ := done(t, exampleJob(obj{"model": "haiku"}), subagentResult{OK: true, Text: strings.Repeat("x", 600)})
		out := detail(pi.message(1).msg, "output").(string)
		if jsLen(out) != 501 || !strings.HasSuffix(out, "…") { // 500 + the ellipsis
			t.Fatalf("output length %d, ends %q", jsLen(out), out[len(out)-3:])
		}
	})

	tw(t, scheduler_, "truncates long error messages the same way as success snippets", func(t *testing.T) {
		pi, _, _ := done(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{Error: strings.Repeat("boom ", 200)})
		m := pi.message(1).msg
		// Both details.error (the renderer) and content (the model, since notify is true) see the truncated form.
		e := detail(m, "error").(string)
		if jsLen(e) != 501 || !strings.HasSuffix(e, "…") {
			t.Fatalf("error length %d", jsLen(e))
		}
		if texts := contentText(t, m); texts[0] != e {
			t.Fatal("content differs from details.error")
		}
	})

	tw(t, scheduler_, "updates lastStatus and increments runCount on success", func(t *testing.T) {
		job := exampleJob(obj{"model": "haiku", "runCount": float64(3)})
		st := makeStorage(job)
		s := newSched(st, &mockPi{}, makeCtx(), (&runnerCalls{}).runner(subagentResult{OK: true, Text: "done"}))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { j, _ := st.GetJob("job-1"); return j.Get("lastStatus") == "success" })
		j, _ := st.GetJob("job-1")
		if j.Get("runCount") != float64(4) || jsobj.IsUndefined(j.Get("lastRun")) {
			t.Fatalf("job = %v", j)
		}
	})

	tw(t, scheduler_, "updates lastStatus to error and does not advance runCount on failure", func(t *testing.T) {
		job := exampleJob(obj{"model": "haiku", "runCount": float64(7)})
		st := makeStorage(job)
		s := newSched(st, &mockPi{}, makeCtx(), (&runnerCalls{}).runner(subagentResult{Error: "boom"}))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { j, _ := st.GetJob("job-1"); return j.Get("lastStatus") == "error" })
		if j, _ := st.GetJob("job-1"); j.Get("runCount") != float64(7) {
			t.Fatalf("runCount = %v", j.Get("runCount"))
		}
	})

	placeholder := func(t *testing.T, job *jsobj.Object, result subagentResult, want string, detailKey string) {
		pi, _, _ := done(t, job, result)
		m := pi.message(1).msg
		texts := contentText(t, m)
		if len(texts) == 0 || texts[0] != want {
			t.Fatalf("content = %v, want %q", texts, want)
		}
		if detailKey != "" && detail(m, detailKey) != want {
			t.Fatalf("details.%s = %v", detailKey, detail(m, detailKey))
		}
	}

	tw(t, scheduler_, "uses placeholder text when subagent produces no output with notify=true (#10)", func(t *testing.T) {
		placeholder(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{OK: true, Text: ""}, "(subagent produced no text output)", "")
	})
	tw(t, scheduler_, "uses placeholder text when subagent produces no output with notify=false (#10 + #12)", func(t *testing.T) {
		// Even with notify=false the content must be non-empty to prevent session poisoning.
		placeholder(t, exampleJob(obj{"model": "haiku"}), subagentResult{OK: true, Text: ""}, "(subagent produced no text output)", "output")
	})
	tw(t, scheduler_, "uses placeholder text when subagent errors with empty error message (#10)", func(t *testing.T) {
		placeholder(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{Error: ""}, "(subagent failed with empty error)", "")
	})
	tw(t, scheduler_, "uses placeholder text when subagent errors with empty error message and notify=false (#10 + #12)", func(t *testing.T) {
		placeholder(t, exampleJob(obj{"model": "haiku"}), subagentResult{Error: ""}, "(subagent failed with empty error)", "error")
	})
	tw(t, scheduler_, "uses placeholder text when subagent produces whitespace-only output (edge case)", func(t *testing.T) {
		placeholder(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{OK: true, Text: "   "}, "(subagent produced no text output)", "")
	})
	tw(t, scheduler_, "uses placeholder text when subagent returns whitespace-only error (edge case)", func(t *testing.T) {
		placeholder(t, exampleJob(obj{"model": "haiku", "notify": true}), subagentResult{Error: "   "}, "(subagent failed with empty error)", "")
	})
}

func TestSchedulerStartRecovery(t *testing.T) {
	tw(t, scheduler_, "clears stale lastStatus=running on start (interrupted-by-shutdown recovery)", func(t *testing.T) {
		// Jobs use type=cron with a valid expression, so scheduleJob does not overwrite lastStatus during start().
		cron := "0 0 * * * *" // hourly
		stuck := exampleJob(obj{"id": "stuck", "type": "cron", "schedule": cron, "lastStatus": "running"})
		clean := exampleJob(obj{"id": "clean", "type": "cron", "schedule": cron, "lastStatus": "success"})
		errored := exampleJob(obj{"id": "errored", "type": "cron", "schedule": cron, "lastStatus": "error"})
		st := makeStorage(stuck, clean, errored)
		s := newSched(st, &mockPi{}, makeCtx(), nil)
		defer s.stop()
		s.start()
		if j, _ := st.GetJob("stuck"); !jsobj.IsUndefined(j.Get("lastStatus")) {
			t.Fatalf("stuck lastStatus = %v", j.Get("lastStatus"))
		}
		// Other lastStatus values are left alone: only the one that signals an interrupted run is swept.
		if j, _ := st.GetJob("clean"); j.Get("lastStatus") != "success" {
			t.Fatal("clean job touched")
		}
		if j, _ := st.GetJob("errored"); j.Get("lastStatus") != "error" {
			t.Fatal("errored job touched")
		}
	})
}

// blockingRunner is a runner that waits for its context to be cancelled (the mockImplementation of the abort cases).
type blockingRunner struct {
	mu      sync.Mutex
	signal  context.Context
	started chan struct{}
}

func newBlockingRunner() *blockingRunner { return &blockingRunner{started: make(chan struct{})} }

func (b *blockingRunner) run(_ string, _ string, abort context.Context, _ subagentOptions) subagentResult {
	b.mu.Lock()
	b.signal = abort
	b.mu.Unlock()
	close(b.started)
	<-abort.Done()
	return subagentResult{Error: "aborted"}
}

func TestSchedulerShutdownAbort(t *testing.T) {
	activeCount := func(s *scheduler) int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.activeSubagents) }

	tw(t, scheduler_, "aborts in-flight subagents when stop() is called", func(t *testing.T) {
		b := newBlockingRunner()
		job := exampleJob(obj{"model": "haiku"})
		s := newSched(makeStorage(job), &mockPi{}, makeCtx(), b.run)
		s.executeJobInSubagent(job)
		// Wait until the run has actually started and we have its signal.
		<-b.started
		if b.signal.Err() != nil {
			t.Fatal("aborted before stop()")
		}
		s.stop()
		if b.signal.Err() == nil {
			t.Fatal("stop() did not abort the run")
		}
	})

	tw(t, scheduler_, "does not post completion markers for runs aborted by stop()", func(t *testing.T) {
		b := newBlockingRunner()
		pi := &mockPi{}
		job := exampleJob(obj{"model": "haiku"})
		s := newSched(makeStorage(job), pi, makeCtx(), b.run)
		s.executeJobInSubagent(job)
		<-b.started
		if pi.messageCount() != 1 { // the start marker only
			t.Fatalf("messages = %d", pi.messageCount())
		}
		s.stop()
		// Wait for the run to clean itself up after the abort.
		waitFor(t, func() bool { return activeCount(s) == 0 })
		time.Sleep(100 * time.Millisecond)
		// No done or error marker is posted because the signal was aborted.
		if pi.messageCount() != 1 {
			t.Fatalf("messages = %d, want the start marker only", pi.messageCount())
		}
	})

	tw(t, scheduler_, "clears activeSubagents after a natural completion", func(t *testing.T) {
		job := exampleJob(obj{"model": "haiku"})
		s := newSched(makeStorage(job), &mockPi{}, makeCtx(), (&runnerCalls{}).runner(subagentResult{OK: true, Text: "done"}))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { return activeCount(s) == 0 })
	})

	tw(t, scheduler_, "survives a thrown sendMessage and still advances storage to a terminal status", func(t *testing.T) {
		pi := &mockPi{sendError: func(call int) error {
			if call == 1 {
				return nil // the start marker succeeds
			}
			return errSimulated
		}}
		logged := captureLog(t)
		job := exampleJob(obj{"model": "haiku"})
		st := makeStorage(job)
		s := newSched(st, pi, makeCtx(), (&runnerCalls{}).runner(subagentResult{OK: true, Text: "done"}))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { return len(logged()) > 0 })
		// The storage was advanced before the marker was posted, so the job is NOT stuck on "running" although sendMessage threw.
		j, _ := st.GetJob("job-1")
		if j.Get("lastStatus") != "success" || j.Get("runCount") != float64(1) {
			t.Fatalf("job = %v", j)
		}
		// The marker failure was logged by the inner handler (not the outer backstop).
		if msg := logged()[0]; !strings.Contains(msg, "Failed to post subagent_done marker for job "+jobID(job)) {
			t.Fatalf("logged %q", msg)
		}
	})
}

func TestSchedulerInlinePath(t *testing.T) {
	tw(t, scheduler_, "does not call runSubagentOnce when job has no model", func(t *testing.T) {
		pi := &mockPi{}
		rc := &runnerCalls{}
		job := exampleJob(nil) // no model
		s := newSched(makeStorage(job), pi, makeCtx(), rc.runner(subagentResult{}))
		s.executeJob(job)
		if rc.count() != 0 {
			t.Fatal("the subagent runner ran")
		}
		// The inline path: a marker and sendUserMessage.
		if pi.messageCount() != 1 || len(pi.users) != 1 || pi.users[0] != (sentUser{"do the thing", "followUp"}) {
			t.Fatalf("messages %d users %v", pi.messageCount(), pi.users)
		}
	})

	tw(t, scheduler_, "inline marker uses empty content and no delivery options (regression guard for double-injection)", func(t *testing.T) {
		pi := &mockPi{}
		job := exampleJob(nil)
		s := newSched(makeStorage(job), pi, makeCtx(), nil)
		s.executeJob(job)
		// The marker must NOT carry the prompt in `content`: that would put the prompt into the model's context a second time.
		m := pi.message(0)
		if c, ok := m.msg.Content.([]any); !ok || len(c) != 0 {
			t.Fatalf("content = %#v", m.msg.Content)
		}
		d := m.msg.Details.(*jsobj.Object)
		if got, _ := jsobj.Stringify(d); got != "{\n  \"jobId\": \"job-1\",\n  \"jobName\": \"demo\",\n  \"prompt\": \"do the thing\"\n}" {
			t.Fatalf("details = %s", got)
		}
		if m.opts != nil {
			t.Fatalf("opts = %+v", m.opts)
		}
	})
}

func TestSchedulerSessionBinding(t *testing.T) {
	tw(t, scheduler_, "isLoadedFor: unbound job is loaded for any session", func(t *testing.T) {
		if !isLoadedFor(exampleJob(nil), "any") || !isLoadedFor(exampleJob(nil), "") {
			t.Fatal("an unbound job must load everywhere")
		}
	})
	tw(t, scheduler_, "isLoadedFor: bound job loads only for the matching session", func(t *testing.T) {
		j := exampleJob(obj{"session": "A"})
		if !isLoadedFor(j, "A") || isLoadedFor(j, "B") || isLoadedFor(j, "") {
			t.Fatal("a bound job loads for its own session only")
		}
	})
	tw(t, scheduler_, "start() does not schedule foreign-session jobs", func(t *testing.T) {
		cron := "0 0 * * * *"
		mine := exampleJob(obj{"id": "mine", "type": "cron", "schedule": cron, "session": "session-A"})
		// The foreign job is lastStatus=running too, to assert that the stale-running sweep skips it.
		foreign := exampleJob(obj{"id": "foreign", "type": "cron", "schedule": cron, "session": "session-B", "lastStatus": "running"})
		unbound := exampleJob(obj{"id": "unbound", "type": "cron", "schedule": cron})
		st := makeStorage(mine, foreign, unbound)
		s := newSched(st, &mockPi{}, makeCtx("session-A"), nil)
		defer s.stop()
		s.start()
		s.mu.Lock()
		_, hasMine := s.jobs["mine"]
		_, hasUnbound := s.jobs["unbound"]
		_, hasForeign := s.jobs["foreign"]
		s.mu.Unlock()
		if !hasMine || !hasUnbound || hasForeign {
			t.Fatalf("scheduled: mine %v unbound %v foreign %v", hasMine, hasUnbound, hasForeign)
		}
		// The foreign session's stale-running flag is untouched: it is not ours.
		if j, _ := st.GetJob("foreign"); j.Get("lastStatus") != "running" {
			t.Fatal("the foreign job was swept")
		}
	})
	tw(t, scheduler_, "executeJob bails when storage now reports the job is bound to another session", func(t *testing.T) {
		pi := &mockPi{}
		job := exampleJob(obj{"id": "j", "session": "session-A"})
		s := newSched(makeStorage(job), pi, makeCtx("session-B"), nil)
		s.executeJob(job)
		// No marker, no user message: the defensive re-read caught the mismatch before the dispatch.
		if pi.messageCount() != 0 || len(pi.users) != 0 {
			t.Fatal("something was dispatched")
		}
	})
	tw(t, scheduler_, "executeJob bails when the job has been removed mid-tick", func(t *testing.T) {
		pi := &mockPi{}
		job := exampleJob(obj{"id": "j"})
		s := newSched(makeStorage(), pi, makeCtx("session-A"), nil) // the job is not in the storage any more
		s.executeJob(job)
		if pi.messageCount() != 0 || len(pi.users) != 0 {
			t.Fatal("something was dispatched")
		}
	})
	tw(t, scheduler_, "executeJob bails when the job was disabled mid-tick (e.g. hand-edited file)", func(t *testing.T) {
		pi := &mockPi{}
		job := exampleJob(obj{"id": "j", "enabled": false})
		s := newSched(makeStorage(job), pi, makeCtx("session-A"), nil)
		s.executeJob(job)
		if pi.messageCount() != 0 || len(pi.users) != 0 {
			t.Fatal("something was dispatched")
		}
	})
	tw(t, scheduler_, "executeJob bails before delegating to subagent when session no longer matches", func(t *testing.T) {
		pi := &mockPi{}
		rc := &runnerCalls{}
		job := exampleJob(obj{"id": "j", "model": "haiku", "session": "session-A"})
		s := newSched(makeStorage(job), pi, makeCtx("session-B"), rc.runner(subagentResult{}))
		s.executeJob(job)
		// The guard fires before the model branch: the subagent never runs, no markers.
		if rc.count() != 0 || pi.messageCount() != 0 {
			t.Fatal("the subagent path ran")
		}
	})
}

func TestSchedulerValidateSchedule(t *testing.T) {
	tw(t, scheduler_, "interval: accepts valid duration, returns intervalMs", func(t *testing.T) {
		r, err := validateSchedule("interval", "5m")
		if err != nil || !r.ok || r.schedule != "5m" || r.intervalMs == nil || *r.intervalMs != 5*60*1000 {
			t.Fatalf("%+v %v", r, err)
		}
	})
	tw(t, scheduler_, "interval: rejects garbage with a useful hint", func(t *testing.T) {
		r, _ := validateSchedule("interval", "five minutes")
		if r.ok || !strings.Contains(r.err, "'5m', '1h', '30s'") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, scheduler_, "once: accepts relative time and resolves to ISO", func(t *testing.T) {
		r, _ := validateSchedule("once", "+5m")
		if !r.ok {
			t.Fatal(r.err)
		}
		d := jsdate.Parse(r.schedule) - jsdate.Now()
		if d <= 4*60*1000 || d >= 6*60*1000 {
			t.Fatalf("delta %v", d)
		}
	})
	tw(t, scheduler_, "once: rejects past timestamps", func(t *testing.T) {
		r, _ := validateSchedule("once", "2000-01-01T00:00:00Z")
		if r.ok || !strings.Contains(r.err, "in the past") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, scheduler_, "once: rejects timestamps under 5s away with a relative-time suggestion", func(t *testing.T) {
		soon, _ := isoString(jsdate.Now() + 2000)
		r, _ := validateSchedule("once", soon)
		if r.ok || !strings.Contains(r.err, "too soon") || !regexp.MustCompile(`use relative time like '\+\d+s'`).MatchString(r.err) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, scheduler_, "once: rejects unparseable timestamps", func(t *testing.T) {
		r, _ := validateSchedule("once", "not-a-date")
		if r.ok || !strings.Contains(r.err, "Invalid timestamp") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, scheduler_, "cron: accepts valid 6-field expression", func(t *testing.T) {
		r, _ := validateSchedule("cron", "0 0 9 * * *")
		if !r.ok || r.schedule != "0 0 9 * * *" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, scheduler_, "cron: rejects 5-field expression with field-count error", func(t *testing.T) {
		r, _ := validateSchedule("cron", "0 9 * * *")
		if r.ok || !strings.Contains(r.err, "6 fields") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestSchedulerDescribeSchedule(t *testing.T) {
	tw(t, scheduler_, "cron: humanizes known patterns", func(t *testing.T) {
		if describeSchedule("cron", "0 0 0 * * *") != "daily" || describeSchedule("cron", "0 */5 * * * *") != "every 5 min" {
			t.Fatal("wrong wording")
		}
	})
	tw(t, scheduler_, "cron: returns raw expression untouched for unknown patterns (no guessing)", func(t *testing.T) {
		raw := "15 30 8 1,15 * 1-5"
		if describeSchedule("cron", raw) != raw {
			t.Fatal("guessed")
		}
	})
	tw(t, scheduler_, "interval: prefixes with 'every'", func(t *testing.T) {
		if describeSchedule("interval", "5m") != "every 5m" {
			t.Fatal("wrong wording")
		}
	})
	tw(t, scheduler_, "once: renders ISO as 'Mon DD HH:MM'", func(t *testing.T) {
		out := describeSchedule("once", "2026-02-13T15:30:00.000Z")
		// The format depends on the local time zone for the day and hour fields, so only the shape is asserted.
		if !regexp.MustCompile(`^[A-Z][a-z]{2} \d{1,2} \d{2}:\d{2}$`).MatchString(out) {
			t.Fatalf("out = %q", out)
		}
	})
}

func TestSchedulerExtensionsSkills(t *testing.T) {
	run := func(t *testing.T, job *jsobj.Object) runnerCall {
		pi := &mockPi{}
		rc := &runnerCalls{}
		s := newSched(makeStorage(job), pi, makeCtx(), rc.runner(subagentResult{OK: true, Text: "OK"}))
		s.executeJobInSubagent(job)
		waitFor(t, func() bool { return pi.messageCount() == 2 })
		return rc.calls[0]
	}
	tw(t, scheduler_, "passes extensions=true to runSubagentOnce when job has extensions=true", func(t *testing.T) {
		if o := run(t, exampleJob(obj{"model": "haiku", "extensions": true})).opts; o.Extensions != true || o.Skills != nil {
			t.Fatalf("options = %+v", o)
		}
	})
	tw(t, scheduler_, "passes extensions=false to runSubagentOnce when job has no extensions", func(t *testing.T) {
		if o := run(t, exampleJob(obj{"model": "haiku"})).opts; o.Extensions != nil || o.Skills != nil {
			t.Fatalf("options = %+v", o)
		}
	})
	tw(t, scheduler_, "passes extensions=false to runSubagentOnce when job has extensions=false", func(t *testing.T) {
		if o := run(t, exampleJob(obj{"model": "haiku", "extensions": false})).opts; o.Extensions != false || o.Skills != nil {
			t.Fatalf("options = %+v", o)
		}
	})
}

var _ = math.NaN
