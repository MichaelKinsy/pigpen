package schedule_prompt

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/croner"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// The scheduling engine (upstream: scheduler.ts CronScheduler).

type customMessage struct {
	CustomType string
	Content    any
	Display    bool
	Details    any
}

// deliverOptions are pi.sendMessage's options; a nil pointer is `undefined`.
type deliverOptions struct {
	DeliverAs   string
	TriggerTurn bool
}

// piAPI is the part of ExtensionAPI the scheduler uses.
type piAPI interface {
	SendMessage(msg customMessage, opts *deliverOptions) error
	SendUserMessage(text, deliverAs string) error
	Emit(channel string, data any)
}

// sessionInfo is the part of the extension context the scheduler and the tool use.
type sessionInfo interface {
	SessionID() string
}

type subagentOptions struct {
	Extensions any // bool, []any of names or nil
	Skills     any
}

type subagentResult struct {
	OK    bool
	Text  string
	Error string
}

// subagentRunner is runSubagentOnce(ctx, prompt, model, signal, options) with the context bound.
type subagentRunner func(prompt, model string, abort context.Context, opts subagentOptions) subagentResult

const subagentOutputSnippetLength = 500

// snippet truncates text to 500 UTF-16 units, appending an ellipsis if cut.
func snippet(text string) string {
	u := utf16.Encode([]rune(text))
	if len(u) > subagentOutputSnippetLength {
		return string(utf16.Decode(u[:subagentOutputSnippetLength])) + "…"
	}
	return text
}

type stopper interface{ stop() }

type timerStop struct{ t *time.Timer }

func (s timerStop) stop() { s.t.Stop() }

type tickerStop struct{ done chan struct{} }

func (s tickerStop) stop() { close(s.done) }

// abortCtl is the AbortController of one subagent run: stop() aborts it.
type abortCtl struct {
	ctx     context.Context
	cancel  context.CancelFunc
	aborted atomic.Bool
}

func (c *abortCtl) abort() {
	c.aborted.Store(true)
	c.cancel()
}

type scheduler struct {
	mu              sync.Mutex
	storage         jobStore
	pi              piAPI
	sess            sessionInfo
	jobs            map[string]*croner.Cron
	intervals       map[string]stopper
	activeSubagents map[*abortCtl]struct{}
	runSubagent     subagentRunner
	listeners       []func(*jsobj.Object)
}

func newScheduler(storage jobStore, pi piAPI, sess sessionInfo, run subagentRunner) *scheduler {
	return &scheduler{
		storage: storage, pi: pi, sess: sess, runSubagent: run,
		jobs: map[string]*croner.Cron{}, intervals: map[string]stopper{}, activeSubagents: map[*abortCtl]struct{}{},
	}
}

// onChange subscribes to the change events (the widget does; other extensions use the `cron:change` channel).
func (s *scheduler) onChange(fn func(*jsobj.Object)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

// isLoadedFor: unbound jobs (no `session` field) load for everyone.
func isLoadedFor(job *jsobj.Object, sessionID string) bool {
	session := job.Get("session")
	return !truthy(session) || strictEqualString(session, sessionID)
}

func jobID(job *jsobj.Object) string { return jsStr(job.Get("id")) }

// start schedules every enabled job loaded for this session; foreign-session jobs are skipped so two pis in one directory do not
// double-fire. It also clears a stale `lastStatus: "running"` left by an interrupted run of this session.
func (s *scheduler) start() {
	mySessionID := s.sess.SessionID()
	jobs, err := s.storage.GetAllJobs()
	if err != nil {
		logf("Failed to start the scheduler: %v", err)
		return
	}
	for _, job := range jobs {
		if !isLoadedFor(job, mySessionID) {
			continue
		}
		if strictEqualString(job.Get("lastStatus"), "running") {
			partial := jsobj.New()
			partial.Set("lastStatus", jsobj.Undefined)
			_, _ = s.storage.UpdateJob(jobID(job), partial)
		}
		if truthy(job.Get("enabled")) {
			s.scheduleJob(job)
		}
	}
}

// stop stops every timer and aborts the subagent runs in flight, so they do not keep going or post markers after the session is gone.
func (s *scheduler) stop() {
	s.mu.Lock()
	crons := s.jobs
	timers := s.intervals
	active := s.activeSubagents
	s.jobs = map[string]*croner.Cron{}
	s.intervals = map[string]stopper{}
	s.activeSubagents = map[*abortCtl]struct{}{}
	s.mu.Unlock()
	for _, c := range crons {
		c.Stop()
	}
	for _, t := range timers {
		t.stop()
	}
	for c := range active {
		c.abort()
	}
}

func (s *scheduler) addJob(job *jsobj.Object) {
	if truthy(job.Get("enabled")) {
		s.scheduleJob(job)
	}
	ev := jsobj.New()
	ev.Set("type", "add")
	ev.Set("job", job)
	s.emitChange(ev)
}

func (s *scheduler) removeJob(id string) {
	s.unscheduleJob(id)
	ev := jsobj.New()
	ev.Set("type", "remove")
	ev.Set("jobId", id)
	s.emitChange(ev)
}

func (s *scheduler) updateJob(id string, updated *jsobj.Object) {
	s.unscheduleJob(id)
	if truthy(updated.Get("enabled")) {
		s.scheduleJob(updated)
	}
	ev := jsobj.New()
	ev.Set("type", "update")
	ev.Set("job", updated)
	s.emitChange(ev)
}

// getNextRun is the next run time of a scheduled cron job (NaN when there is none or the job is not a cron job).
func (s *scheduler) getNextRun(id string) float64 {
	s.mu.Lock()
	c := s.jobs[id]
	s.mu.Unlock()
	if c == nil {
		return math.NaN()
	}
	return c.NextRun()
}

// toNumber is the Number() of the values a stored job can hold.
func toNumber(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	case nil:
		return 0
	}
	return math.NaN()
}

// toDate is `new Date(v)` for a stored schedule.
func toDate(v any) float64 {
	switch t := v.(type) {
	case string:
		return jsdate.Parse(t)
	case float64:
		return jsdate.TimeClip(t)
	}
	return math.NaN()
}

func errorEvent(jobID, text string) *jsobj.Object {
	ev := jsobj.New()
	ev.Set("type", "error")
	ev.Set("jobId", jobID)
	ev.Set("error", text)
	return ev
}

// schedulerNow is Date.now() for the one-shot's delay; a seam for the test of a delay of exactly zero.
var schedulerNow = jsdate.Now

func (s *scheduler) scheduleJob(job *jsobj.Object) {
	id := jobID(job)
	switch {
	case strictEqualString(job.Get("type"), "interval") && truthy(job.Get("intervalMs")):
		ms := toNumber(job.Get("intervalMs"))
		if !(ms >= 1) {
			ms = 1 // a delay below one millisecond is one, as in Node
		}
		d := time.Duration(math.Min(math.Floor(ms), float64(math.MaxInt64/int64(time.Millisecond)))) * time.Millisecond
		done := make(chan struct{})
		go func() {
			t := time.NewTicker(d)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					s.executeJob(job)
				case <-done:
					return
				}
			}
		}()
		s.mu.Lock()
		s.intervals[id] = tickerStop{done}
		s.mu.Unlock()
	case strictEqualString(job.Get("type"), "once"):
		target := toDate(job.Get("schedule"))
		delay := target - schedulerNow()
		if delay > 0 {
			// PORT.md D1: Node caps a timer at 2^31-1 ms and fires a longer one after 1 ms; a Go timer takes any delay.
			d := time.Duration(math.Min(delay, float64(math.MaxInt64/int64(time.Millisecond)))) * time.Millisecond
			t := time.AfterFunc(d, func() {
				s.executeJob(job)
				off := jsobj.New()
				off.Set("enabled", false)
				_, _ = s.storage.UpdateJob(id, off)
				ev := jsobj.New()
				ev.Set("type", "update")
				j := job.Clone()
				j.Set("enabled", false)
				ev.Set("job", j)
				s.emitChange(ev)
			})
			s.mu.Lock()
			s.intervals[id] = timerStop{t}
			s.mu.Unlock()
		} else {
			logf("Job %s (%s) scheduled for past time: %s", id, jsStr(job.Get("name")), jsStr(job.Get("schedule")))
			partial := jsobj.New()
			partial.Set("enabled", false)
			partial.Set("lastStatus", "error")
			_, _ = s.storage.UpdateJob(id, partial)
			s.emitChange(errorEvent(id, "Scheduled time "+jsStr(job.Get("schedule"))+" is in the past"))
		}
	default:
		c, err := croner.New(jsStr(job.Get("schedule")), func() { s.executeJob(job) })
		if err != nil {
			logf("Failed to schedule job %s: %v", id, err)
			s.emitChange(errorEvent(id, err.Error()))
			return
		}
		s.mu.Lock()
		s.jobs[id] = c
		s.mu.Unlock()
	}
}

func (s *scheduler) unscheduleJob(id string) {
	s.mu.Lock()
	c := s.jobs[id]
	delete(s.jobs, id)
	t := s.intervals[id]
	delete(s.intervals, id)
	s.mu.Unlock()
	if c != nil {
		c.Stop()
	}
	if t != nil {
		t.stop()
	}
}

// storageFailure logs a storage error where the original would have let the exception escape its timer callback.
func storageFailure(what string, err error) { logf("%s: %v", what, err) }

func (s *scheduler) updateStats(id string, partial *jsobj.Object) {
	if _, err := s.storage.UpdateJob(id, partial); err != nil {
		storageFailure("Failed to update job "+id, err)
	}
}

// executeJob runs a job's prompt.
func (s *scheduler) executeJob(job *jsobj.Object) {
	id := jobID(job)
	// Re-read before firing: the closure's `job` is stale if the storage was edited mid-tick (removed, disabled, or `session`
	// rebound by hand).
	fresh, err := s.storage.GetJob(id)
	if err != nil {
		storageFailure("Failed to read job "+id, err)
		return
	}
	if fresh == nil || !truthy(fresh.Get("enabled")) {
		return
	}
	if !isLoadedFor(fresh, s.sess.SessionID()) {
		return
	}
	if truthy(job.Get("model")) {
		s.executeJobInSubagent(job)
		return
	}

	fire := func() error {
		running := jsobj.New()
		running.Set("lastStatus", "running")
		if _, err := s.storage.UpdateJob(id, running); err != nil {
			return err
		}
		ev := jsobj.New()
		ev.Set("type", "fire")
		ev.Set("job", job)
		s.emitChange(ev)

		// A visible-only marker: the renderer reads `details`, so `content` is empty. Putting the prompt in `content` would put it into
		// the model's context a second time next to the user message below.
		details := jsobj.New()
		details.Set("jobId", job.Get("id"))
		details.Set("jobName", job.Get("name"))
		details.Set("prompt", job.Get("prompt"))
		if err := s.pi.SendMessage(customMessage{CustomType: "scheduled_prompt", Content: []any{}, Display: true, Details: details}, nil); err != nil {
			return err
		}
		// The single model-visible delivery.
		if err := s.pi.SendUserMessage(jsStr(job.Get("prompt")), "followUp"); err != nil {
			return err
		}

		// `job` is frozen at the value it had when the timer was made: read the latest count from the storage.
		nextRun := s.getNextRun(id)
		latest, err := s.storage.GetJob(id)
		if err != nil {
			return err
		}
		var current any = job.Get("runCount")
		if latest != nil && !isNullish(latest.Get("runCount")) {
			current = latest.Get("runCount")
		}
		stats := jsobj.New()
		stats.Set("lastRun", isoNow())
		stats.Set("lastStatus", "success")
		stats.Set("runCount", toNumber(current)+1)
		stats.Set("nextRun", nextRunISO(nextRun))
		if _, err := s.storage.UpdateJob(id, stats); err != nil {
			return err
		}
		ev2 := jsobj.New()
		ev2.Set("type", "fire")
		ev2.Set("job", job)
		s.emitChange(ev2)
		return nil
	}
	if err := fire(); err != nil {
		logf("Failed to execute job %s: %v", id, err)
		stats := jsobj.New()
		stats.Set("lastRun", isoNow())
		stats.Set("lastStatus", "error")
		s.updateStats(id, stats)
		s.emitChange(errorEvent(id, err.Error()))
	}
}

// nextRunISO is `nextRun?.toISOString()`.
func nextRunISO(ms float64) any {
	if math.IsNaN(ms) {
		return jsobj.Undefined
	}
	if s, ok := isoString(ms); ok {
		return s
	}
	return jsobj.Undefined
}

func (s *scheduler) abortActive(c *abortCtl) {
	s.mu.Lock()
	delete(s.activeSubagents, c)
	s.mu.Unlock()
}

func textBlock(text string) []any {
	b := jsobj.New()
	b.Set("type", "text")
	b.Set("text", text)
	return []any{b}
}

func markerDetails(job *jsobj.Object, mode, model string) *jsobj.Object {
	d := jsobj.New()
	d.Set("jobId", job.Get("id"))
	d.Set("jobName", job.Get("name"))
	d.Set("prompt", job.Get("prompt"))
	d.Set("mode", mode)
	d.Set("model", model)
	return d
}

// executeJobInSubagent runs a job's prompt in a fresh agent session with the chosen model. Fire and forget: the tick returns at once so
// other jobs keep firing.
func (s *scheduler) executeJobInSubagent(job *jsobj.Object) {
	id := jobID(job)
	model := jsStr(job.Get("model"))
	notify := job.Get("notify") == true
	running := jsobj.New()
	running.Set("lastStatus", "running")
	s.updateStats(id, running)
	ev := jsobj.New()
	ev.Set("type", "fire")
	ev.Set("job", job)
	s.emitChange(ev)

	// The start marker needs non-empty content: an empty user message is filtered out by the provider, which can leave the conversation
	// ending on the assistant.
	_ = s.pi.SendMessage(customMessage{
		CustomType: "scheduled_prompt",
		Content:    textBlock("🕐 Scheduled (subagent: " + model + "): " + jsStr(job.Get("name"))),
		Display:    true,
		Details:    markerDetails(job, "subagent_start", model),
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ctl := &abortCtl{ctx: ctx, cancel: cancel}
	s.mu.Lock()
	s.activeSubagents[ctl] = struct{}{}
	s.mu.Unlock()

	var wake *deliverOptions
	if notify {
		wake = &deliverOptions{DeliverAs: "followUp", TriggerTurn: true}
	}
	go func() {
		result := s.runSubagent(jsStr(job.Get("prompt")), model, ctx, subagentOptions{Extensions: undefinedToNil(job.Get("extensions")), Skills: undefinedToNil(job.Get("skills"))})
		s.abortActive(ctl)
		defer cancel()
		// The scheduler was stopped (session shutdown, switch or fork) while the run was going: touch neither the storage nor the chat.
		if ctl.aborted.Load() {
			return
		}
		nextRun := s.getNextRun(id)
		// The storage reaches a terminal status BEFORE the marker is posted: the marker is best effort (the session may be going away) and
		// must never leave the job stuck on "running".
		if result.OK {
			output := snippet(jsTrim(result.Text))
			if output == "" {
				output = "(subagent produced no text output)"
			}
			latest, _ := s.storage.GetJob(id)
			var current any = job.Get("runCount")
			if latest != nil && !isNullish(latest.Get("runCount")) {
				current = latest.Get("runCount")
			}
			stats := jsobj.New()
			stats.Set("lastRun", isoNow())
			stats.Set("lastStatus", "success")
			stats.Set("runCount", toNumber(current)+1)
			stats.Set("nextRun", nextRunISO(nextRun))
			s.updateStats(id, stats)
			ev := jsobj.New()
			ev.Set("type", "fire")
			ev.Set("job", job)
			s.emitChange(ev)
			details := markerDetails(job, "subagent_done", model)
			details.Set("output", output)
			if err := s.pi.SendMessage(customMessage{CustomType: "scheduled_prompt", Content: textBlock(output), Display: true, Details: details}, wake); err != nil {
				logf("Failed to post subagent_done marker for job %s: %v", id, err)
			}
			return
		}
		// The error is cut like the success snippet: verbose API errors would overflow the chat row.
		errText := snippet(jsTrim(result.Error))
		if errText == "" {
			errText = "(subagent failed with empty error)"
		}
		stats := jsobj.New()
		stats.Set("lastRun", isoNow())
		stats.Set("lastStatus", "error")
		stats.Set("nextRun", nextRunISO(nextRun))
		s.updateStats(id, stats)
		s.emitChange(errorEvent(id, errText))
		details := markerDetails(job, "subagent_error", model)
		details.Set("error", errText)
		if err := s.pi.SendMessage(customMessage{CustomType: "scheduled_prompt", Content: textBlock(errText), Display: true, Details: details}, wake); err != nil {
			logf("Failed to post subagent_error marker for job %s: %v", id, err)
		}
	}()
}

func undefinedToNil(v any) any {
	if jsobj.IsUndefined(v) {
		return nil
	}
	return v
}

func jsTrim(s string) string { return croner.JSTrim(s) }

func (s *scheduler) emitChange(event *jsobj.Object) {
	s.mu.Lock()
	listeners := append([]func(*jsobj.Object){}, s.listeners...)
	s.mu.Unlock()
	s.pi.Emit("cron:change", event)
	for _, fn := range listeners {
		fn(event)
	}
}
