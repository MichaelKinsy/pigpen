package tintinweb_subagents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The scheduler: timer-driven dispatch of scheduled subagents. Two kinds of timer, as in the original (a cron per
// cron job, a setInterval/setTimeout per interval and one-shot job); a fire spawns the agent through the manager,
// bypassing the concurrency queue, and records the outcome in the store; the completion notification is the manager's
// own background path. upstream: src/schedule.ts SubagentScheduler.

// scheduleChange is an event of the `subagents:scheduled` channel. upstream: src/schedule.ts ScheduleChangeEvent.
type scheduleChange struct {
	Type    string // "added", "removed", "updated", "fired" or "error"
	Job     *scheduledSubagent
	JobID   string
	AgentID string
	Name    string
	Error   string
}

// MarshalJSON writes the keys each kind of event has in the original (`{type, job}`, `{type, jobId}`, `{type, jobId,
// agentId, name}`, `{type, jobId, error}`), in that order, even where a value is the empty string.
func (e scheduleChange) MarshalJSON() ([]byte, error) {
	type field struct {
		key string
		val any
	}
	var fields []field
	switch e.Type {
	case "added", "updated":
		fields = []field{{"type", e.Type}, {"job", e.Job}}
	case "removed":
		fields = []field{{"type", e.Type}, {"jobId", e.JobID}}
	case "fired":
		fields = []field{{"type", e.Type}, {"jobId", e.JobID}, {"agentId", e.AgentID}, {"name", e.Name}}
	default:
		fields = []field{{"type", e.Type}, {"jobId", e.JobID}, {"error", e.Error}}
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(f.key)
		v, err := json.Marshal(f.val)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// newJobInput is what a job is created from; the id, the timestamps and the state are derived. upstream: schedule.ts NewJobInput.
type newJobInput struct {
	Name, Description, Schedule, SubagentType, Prompt string
	Model, Thinking                                   *string
	MaxTurns                                          *int
	Isolated                                          *bool
	Isolation                                         string
}

// invocationSnapshot is how a scheduled run was configured, handed to the manager so the conversation viewer can show
// it (a scheduled run has no tool call to build one). The model is left out on purpose: the manager fills in the
// effective one when the session reports it. upstream: schedule.ts executeJob `invocation`, types.ts AgentInvocation.
type invocationSnapshot struct {
	Thinking        *string
	MaxTurns        *int // normalized: 0 means unlimited and is not shown as "max turns: 0"
	Isolated        *bool
	RunInBackground bool
	Isolation       string
}

// spawnJobOptions are the options a fire passes to manager.spawn. upstream: schedule.ts executeJob.
type spawnJobOptions struct {
	Description  string
	IsBackground bool
	BypassQueue  bool
	Model        *string
	MaxTurns     *int
	Isolated     *bool
	Thinking     *string
	Isolation    string
	Invocation   invocationSnapshot
}

// scheduleRun is the part of an agent record a fire reads: its run (done closes when it settles) and its status.
type scheduleRun struct {
	Done   <-chan struct{}
	Status func() string
}

// scheduleManager is what the scheduler uses of the agent manager. upstream: the AgentManager methods schedule.ts calls.
type scheduleManager interface {
	spawn(typ, prompt string, opts spawnJobOptions) (agentID string, err error)
	awaitStartup(agentID string) error
	getRecord(agentID string) *scheduleRun
}

// scheduleClock is time and timers, replaced by a fake in tests (the original's tests use fake timers).
type scheduleClock interface {
	now() time.Time
	afterFunc(d time.Duration, f func()) interface{ stop() }
}

type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

// afterFunc arms a timer whose callback cannot take the process down: a panic in a fire is dropped here, where the
// callback has no caller to return it to (the scheduler turns a panic inside a fire into an error event before that).
func (realClock) afterFunc(d time.Duration, f func()) interface{ stop() } {
	return realTimer{time.AfterFunc(d, func() {
		defer func() { _ = recover() }()
		f()
	})}
}

type realTimer struct{ t *time.Timer }

func (r realTimer) stop() { r.t.Stop() }

// timerChunk bounds one wait: a longer delay is waited in pieces, so no timer is armed for more than a day.
const timerChunk = 24 * time.Hour

// cronCheckInterval is the longest wait croner arms in one go: it re-arms every 30 s. upstream: croner.ts maxDelay.
const cronCheckInterval = 30 * time.Second

// rearmable is a timer that survives being re-armed: stop() cancels whichever piece is pending.
type rearmable struct {
	mu      sync.Mutex
	stopped bool
	cur     interface{ stop() }
}

func (c *rearmable) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.cur != nil {
		c.cur.stop()
	}
}

// arm runs next after wait (nil-safe against stop), unless stopped. It reports false when stopped.
func (c *rearmable) arm(clock scheduleClock, wait time.Duration, next func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return false
	}
	c.cur = clock.afterFunc(wait, next)
	return true
}

// at runs fire once, at deadline (setTimeout). A wait too long for a Duration is made in pieces.
func (c *rearmable) at(clock scheduleClock, deadline time.Time, fire func()) {
	var step func()
	step = func() {
		remaining := deadline.Sub(clock.now())
		if remaining <= 0 {
			fire()
			return
		}
		c.arm(clock, min(remaining, timerChunk), step)
	}
	step()
}

// nodeTimerMax is the longest delay Node's setTimeout and setInterval keep: 2^31-1 ms (24.8 days).
const nodeTimerMax = 2147483647

// nodeDelay is the delay Node's timers really use: one that is not at least 1 ms and at most 2^31-1 ms (NaN included)
// becomes 1 ms, with a TimeoutOverflowWarning. So a one-shot more than 24.8 days away fires at once, and an interval
// longer than that fires every millisecond, in the original (PORT.md).
func nodeDelay(ms float64) float64 {
	if !(ms >= 1 && ms <= nodeTimerMax) {
		return 1
	}
	return ms
}

// after runs fire once, delayMs from now (setTimeout).
func (c *rearmable) after(clock scheduleClock, delayMs float64, fire func()) {
	c.at(clock, clock.now().Add(msDuration(nodeDelay(delayMs))), fire)
}

// every runs fire every intervalMs (setInterval) until stopped. The next tick is due one interval after this one
// started, whether or not the fire panics or takes long.
func (c *rearmable) every(clock scheduleClock, intervalMs float64, fire func()) {
	var tick func(from time.Time)
	tick = func(from time.Time) {
		c.at(clock, from.Add(msDuration(nodeDelay(intervalMs))), func() {
			started := clock.now()
			defer tick(started)
			fire()
		})
	}
	tick(clock.now())
}

// msDuration is a millisecond count as a Duration, capped at the longest one (about 292 years).
func msDuration(ms float64) time.Duration {
	if ms >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// cronRunner is croner's scheduling loop for one cron job: wait for the next run (at most 30 s at a time), fire when
// it is due, plan the next. upstream: croner.ts Cron.schedule, _checkTrigger.
type cronRunner struct {
	cron  *cron
	clock scheduleClock
	fire  func()
	timer rearmable
}

func (r *cronRunner) schedule() {
	now := r.clock.now()
	target, ok := r.cron.next(now)
	if !ok {
		return
	}
	wait := min(target.Sub(now), cronCheckInterval)
	r.timer.arm(r.clock, wait, func() { r.check(target) })
}

func (r *cronRunner) check(target time.Time) {
	if !r.clock.now().Before(target) {
		r.fire()
	}
	r.schedule()
}

func (r *cronRunner) stop() { r.timer.stop() }

// subagentScheduler is the scheduler. The zero value is not started. upstream: schedule.ts SubagentScheduler.
type subagentScheduler struct {
	clock scheduleClock

	mu        sync.Mutex
	crons     map[string]*cronRunner
	intervals map[string]*rearmable
	store     *scheduleStore
	manager   scheduleManager
	emitFn    func(scheduleChange)
	settling  sync.WaitGroup // the runs whose outcome is still to be recorded
}

func newSubagentScheduler(clock scheduleClock) *subagentScheduler {
	if clock == nil {
		clock = realClock{}
	}
	return &subagentScheduler{clock: clock, crons: map[string]*cronRunner{}, intervals: map[string]*rearmable{}}
}

// start binds the scheduler to a session's store and arms the enabled jobs. emit publishes on `subagents:scheduled`.
func (s *subagentScheduler) start(manager scheduleManager, store *scheduleStore, emit func(scheduleChange)) {
	s.mu.Lock()
	s.manager, s.store, s.emitFn = manager, store, emit
	s.mu.Unlock()
	for _, job := range store.list() {
		if job.Enabled {
			s.scheduleJob(job)
		}
	}
}

// stop cancels every timer and drops the references. Safe to call repeatedly.
func (s *subagentScheduler) stop() {
	s.mu.Lock()
	crons, intervals := s.crons, s.intervals
	s.crons, s.intervals = map[string]*cronRunner{}, map[string]*rearmable{}
	s.store, s.manager, s.emitFn = nil, nil, nil
	s.mu.Unlock()
	for _, c := range crons {
		c.stop()
	}
	for _, t := range intervals {
		t.stop()
	}
}

// isActive: start() has bound a store.
func (s *subagentScheduler) isActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store != nil
}

func (s *subagentScheduler) list() []scheduledSubagent {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store == nil {
		return []scheduledSubagent{}
	}
	return store.list()
}

func (s *subagentScheduler) requireStore() (*scheduleStore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil, errors.New("Scheduler not started — no active session.")
	}
	return s.store, nil
}

func (s *subagentScheduler) emit(ev scheduleChange) {
	s.mu.Lock()
	fn := s.emitFn
	s.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// buildJob validates the schedule and builds a job from input. upstream: schedule.ts buildJob.
func (s *subagentScheduler) buildJob(in newJobInput) (scheduledSubagent, error) {
	now := s.clock.now()
	d, err := detectSchedule(in.Schedule, now)
	if err != nil {
		return scheduledSubagent{}, err
	}
	return scheduledSubagent{
		ID: nanoid(), Name: in.Name, Description: in.Description, Schedule: d.Normalized, ScheduleType: d.Type, IntervalMs: d.IntervalMs,
		SubagentType: in.SubagentType, Prompt: in.Prompt, Model: in.Model, Thinking: in.Thinking, MaxTurns: in.MaxTurns,
		Isolated: in.Isolated, Isolation: in.Isolation, Enabled: true, CreatedAt: isoString(now),
	}, nil
}

// addJob adds a job, persists it and arms it. upstream: schedule.ts addJob.
func (s *subagentScheduler) addJob(in newJobInput) (scheduledSubagent, error) {
	store, err := s.requireStore()
	if err != nil {
		return scheduledSubagent{}, err
	}
	if store.hasName(in.Name, "") {
		return scheduledSubagent{}, fmt.Errorf("A scheduled job named %q already exists.", in.Name)
	}
	job, err := s.buildJob(in)
	if err != nil {
		return scheduledSubagent{}, err
	}
	if err := store.add(job); err != nil {
		return scheduledSubagent{}, err
	}
	if job.Enabled {
		s.scheduleJob(job)
	}
	s.emit(scheduleChange{Type: "added", Job: &job})
	return job, nil
}

// removeJob unschedules and deletes a job. upstream: schedule.ts removeJob.
func (s *subagentScheduler) removeJob(id string) (bool, error) {
	store, err := s.requireStore()
	if err != nil {
		return false, err
	}
	if _, ok := store.get(id); !ok {
		return false, nil
	}
	s.unscheduleJob(id)
	ok, err := store.remove(id)
	if ok {
		s.emit(scheduleChange{Type: "removed", JobID: id})
	}
	return ok, err
}

// updateJob mutates a job and re-arms it from its new `enabled` state. upstream: schedule.ts updateJob.
func (s *subagentScheduler) updateJob(id string, patch func(*scheduledSubagent)) (scheduledSubagent, bool, error) {
	store, err := s.requireStore()
	if err != nil {
		return scheduledSubagent{}, false, err
	}
	updated, ok, err := store.update(id, patch)
	if !ok {
		return scheduledSubagent{}, false, err
	}
	s.unscheduleJob(id)
	if updated.Enabled {
		s.scheduleJob(updated)
	}
	s.emit(scheduleChange{Type: "updated", Job: &updated})
	return updated, true, nil
}

// getNextRun is the next run as an ISO timestamp, or false when the job is not armed. upstream: schedule.ts getNextRun.
func (s *subagentScheduler) getNextRun(jobID string) (string, bool) {
	s.mu.Lock()
	runner, store := s.crons[jobID], s.store
	s.mu.Unlock()
	if runner != nil {
		if next, ok := runner.cron.next(s.clock.now()); ok {
			return isoString(next), true
		}
		return "", false
	}
	if store == nil {
		return "", false
	}
	job, ok := store.get(jobID)
	if !ok || !job.Enabled {
		return "", false
	}
	if job.ScheduleType == "once" {
		return job.Schedule, true
	}
	if job.ScheduleType == "interval" && job.IntervalMs != nil && *job.IntervalMs != 0 {
		// Before the first fire there is no lastRun, so "now": accurate at create time (the interval was just armed)
		// and within one interval of right in any view before the first fire.
		base := float64(s.clock.now().UnixMilli())
		if job.LastRun != "" {
			if ms, ok := parseJSDate(job.LastRun); ok {
				base = ms
			} else {
				return "", false
			}
		}
		if t := base + *job.IntervalMs; math.Abs(t) <= maxDateMs {
			return isoString(time.UnixMilli(int64(t))), true
		}
	}
	return "", false
}

// scheduleJob arms a job's timer. upstream: schedule.ts scheduleJob.
func (s *subagentScheduler) scheduleJob(job scheduledSubagent) {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store == nil {
		return
	}
	s.unscheduleJob(job.ID) // never two timers for one job
	switch {
	case job.ScheduleType == "interval" && job.IntervalMs != nil && *job.IntervalMs != 0:
		t := &rearmable{}
		s.mu.Lock()
		s.intervals[job.ID] = t
		s.mu.Unlock()
		t.every(s.clock, *job.IntervalMs, func() { s.executeJob(job.ID) })
	case job.ScheduleType == "once":
		target, ok := parseJSDate(job.Schedule)
		delay := target - float64(s.clock.now().UnixMilli())
		if ok && delay > 0 {
			t := &rearmable{}
			s.mu.Lock()
			s.intervals[job.ID] = t
			s.mu.Unlock()
			t.after(s.clock, delay, func() {
				s.executeJob(job.ID)
				// Auto-disable a one-shot once it has fired.
				s.record(store, job.ID, func(j *scheduledSubagent) { j.Enabled = false })
				if cur, ok := store.get(job.ID); ok {
					s.emit(scheduleChange{Type: "updated", Job: &cur})
				}
			})
		} else {
			// A past timestamp: disable, mark it an error, never fire.
			if _, _, err := store.update(job.ID, func(j *scheduledSubagent) { j.Enabled, j.LastStatus = false, "error" }); err != nil {
				s.emit(scheduleChange{Type: "error", JobID: job.ID, Error: err.Error()})
				return
			}
			s.emit(scheduleChange{Type: "error", JobID: job.ID, Error: fmt.Sprintf("Scheduled time %s is in the past", job.Schedule)})
		}
	default:
		c, err := newCronAt(job.Schedule, s.clock.now())
		if err != nil {
			s.emit(scheduleChange{Type: "error", JobID: job.ID, Error: err.Error()})
			return
		}
		runner := &cronRunner{cron: c, clock: s.clock, fire: func() { s.executeJob(job.ID) }}
		s.mu.Lock()
		s.crons[job.ID] = runner
		s.mu.Unlock()
		runner.schedule()
	}
}

func (s *subagentScheduler) unscheduleJob(id string) {
	s.mu.Lock()
	runner, t := s.crons[id], s.intervals[id]
	delete(s.crons, id)
	delete(s.intervals, id)
	s.mu.Unlock()
	if runner != nil {
		runner.stop()
	}
	if t != nil {
		t.stop()
	}
}

// normalizeMaxTurns: nothing or 0 is unlimited, otherwise at least 1. upstream: agent-runner.ts normalizeMaxTurns.
func normalizeMaxTurns(n *int) *int {
	if n == nil || *n == 0 {
		return nil
	}
	v := max(1, *n)
	return &v
}

// executeJob fires a job: records that it is running, spawns the agent (bypassing the concurrency queue) and records
// the outcome when the agent settles. Fire and forget: the timer tick returns at once so other jobs keep firing.
// upstream: schedule.ts executeJob.
func (s *subagentScheduler) executeJob(id string) {
	s.mu.Lock()
	store, manager := s.store, s.manager
	s.mu.Unlock()
	if store == nil || manager == nil {
		return
	}
	job, ok := store.get(id)
	if !ok || !job.Enabled {
		return
	}
	s.record(store, id, func(j *scheduledSubagent) { j.LastStatus = "running" })

	var agentID string
	err := func() (err error) {
		// A panic in the manager is a thrown error in the original: it becomes this fire's error.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		// Re-resolved at fire time against the registry as it stands: a job that outlives its agent must not silently
		// run something else (#183).
		dispatch := resolveSpawnType(job.SubagentType)
		if !dispatch.OK {
			return errors.New(dispatch.Message)
		}
		agentID, err = manager.spawn(dispatch.Type, job.Prompt, spawnJobOptions{
			Description: job.Description, IsBackground: true, BypassQueue: true, Model: job.Model, MaxTurns: job.MaxTurns,
			Isolated: job.Isolated, Thinking: job.Thinking, Isolation: job.Isolation,
			Invocation: invocationSnapshot{Thinking: job.Thinking, MaxTurns: normalizeMaxTurns(job.MaxTurns), Isolated: job.Isolated,
				RunInBackground: true, Isolation: job.Isolation},
		})
		return err
	}()
	if err != nil {
		now := isoString(s.clock.now())
		s.record(store, id, func(j *scheduledSubagent) { j.LastRun, j.LastStatus = now, "error" })
		s.emit(scheduleChange{Type: "error", JobID: id, Error: err.Error()})
		return
	}
	s.emit(scheduleChange{Type: "fired", JobID: id, AgentID: agentID, Name: job.Name})

	finalize := func(status string) {
		next, hasNext := s.getNextRun(id)
		current, _ := store.get(id)
		now := isoString(s.clock.now())
		s.record(store, id, func(j *scheduledSubagent) {
			j.LastRun, j.LastStatus, j.RunCount = now, status, current.RunCount+1
			j.NextRun = ""
			if hasNext {
				j.NextRun = next
			}
		})
	}
	// A run settles either way, so success or failure is read off the record's status, not off the run ending:
	// completed and steered are successes; error, aborted and stopped are errors. awaitStartup first: a run that
	// only exists once a preparation step has finished (a failed one rejects here). A panic in either is a failure
	// of the run, not of the process.
	s.settling.Add(1)
	go func() {
		defer s.settling.Done()
		status := func() (status string) {
			defer func() {
				if recover() != nil {
					status = "error"
				}
			}()
			if err := manager.awaitStartup(agentID); err != nil {
				return "error"
			}
			if rec := manager.getRecord(agentID); rec != nil {
				<-rec.Done
				switch rec.Status() {
				case statusError, statusAborted, statusStopped:
					return "error"
				}
			}
			return "success"
		}()
		defer func() { _ = recover() }()
		finalize(status)
	}()
}

// record applies a patch from a timer or a settle callback. A store failure there has no caller to return to: it is
// reported as an error event.
func (s *subagentScheduler) record(store *scheduleStore, id string, patch func(*scheduledSubagent)) {
	if _, _, err := store.update(id, patch); err != nil {
		s.emit(scheduleChange{Type: "error", JobID: id, Error: err.Error()})
	}
}

// ---- Format detection and parsers (pure). upstream: schedule.ts statics ----

const maxDateMs = 8.64e15 // a JavaScript Date's range, +-100,000,000 days from the epoch

var (
	relativeTimeRe = regexp.MustCompile(`^\+(\d+)(s|m|h|d)$`)
	intervalRe     = regexp.MustCompile(`^(\d+)(s|m|h|d)$`)
	isoStartRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)
	unitMs         = map[string]float64{"s": 1000, "m": 60_000, "h": 3_600_000, "d": 86_400_000}
)

// isoString is Date.prototype.toISOString: UTC, milliseconds, and a signed six-digit year outside 0-9999.
func isoString(t time.Time) string {
	u := t.UTC()
	year := fmt.Sprintf("%04d", u.Year())
	if y := u.Year(); y < 0 || y > 9999 {
		sign := "+"
		if y < 0 {
			sign, y = "-", -y
		}
		year = fmt.Sprintf("%s%06d", sign, y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(u.Month()), u.Day(), u.Hour(), u.Minute(), u.Second(), u.Nanosecond()/1e6)
}

var isoDateRe = regexp.MustCompile(`^([+-]\d{6}|\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|z|[+-]\d{2}:?\d{2})?$`)

// parseJSDate is Date.parse for the ECMAScript date-time format (`YYYY-MM-DDTHH:mm[:ss[.sss]][Z|±HH:mm]`, which V8
// also reads with `±HHmm`): milliseconds since the epoch, or false for what Date.parse turns into NaN. A date-time
// with no offset is local time. Other shapes V8's legacy parser reads are not accepted: the callers only pass strings
// that start with a date-time, where V8 accepts nothing more. upstream: JavaScript `new Date(string)`.
func parseJSDate(s string) (float64, bool) {
	m := isoDateRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	atoi := func(v string) int { n, _ := strconv.Atoi(v); return n }
	year := atoi(m[1])
	if m[1][0] == '-' && year == 0 {
		return 0, false // -000000 is not a year
	}
	month, day, hour, minute := atoi(m[2]), atoi(m[3]), atoi(m[4]), atoi(m[5])
	second, ms := 0, 0
	if m[6] != "" {
		second = atoi(m[6])
	}
	if m[7] != "" {
		frac := (m[7] + "00")[:3]
		ms = atoi(frac)
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 24 || minute > 59 || second > 59 {
		return 0, false
	}
	if hour == 24 && (minute != 0 || second != 0 || ms != 0) {
		return 0, false
	}
	var t time.Time
	switch off := m[8]; {
	case off == "":
		t = jsLocalTime(year, month-1, day, hour, minute, second, ms)
	default:
		offMin := 0
		if off != "Z" && off != "z" {
			digits := strings.ReplaceAll(off[1:], ":", "")
			h, mi := atoi(digits[:2]), atoi(digits[2:])
			if h > 23 || mi > 59 {
				return 0, false
			}
			offMin = h*60 + mi
			if off[0] == '-' {
				offMin = -offMin
			}
		}
		t = utcDate(year, month-1, day, hour, minute, second, ms).Add(-time.Duration(offMin) * time.Minute)
	}
	v := float64(t.UnixMilli())
	if math.Abs(v) > maxDateMs {
		return 0, false
	}
	return v, true
}

// detected is a schedule string's type and normal form.
type detected struct {
	Type       string // "cron", "once" or "interval"
	IntervalMs *float64
	Normalized string
}

// detectSchedule sniffs a schedule string and tags its type. Relative ("+10m") and interval ("5m") both read as digit
// and unit; the relative one needs its leading "+". upstream: schedule.ts detectSchedule.
func detectSchedule(s string, now time.Time) (detected, error) {
	trimmed := jsTrim(s)
	if rel, ok, err := parseRelativeTime(trimmed, now); err != nil {
		return detected{}, err
	} else if ok {
		return detected{Type: "once", Normalized: rel}, nil
	}
	if ivl, ok := parseInterval(trimmed); ok {
		return detected{Type: "interval", IntervalMs: &ivl, Normalized: trimmed}, nil
	}
	// An ISO timestamp is a one-shot. A past one is refused up front, so a record is never dead on arrival.
	if isoStartRe.MatchString(trimmed) {
		if ms, ok := parseJSDate(trimmed); ok {
			t := time.UnixMilli(int64(ms))
			if ms <= float64(now.UnixMilli()) {
				return detected{}, fmt.Errorf("Scheduled time %s is in the past.", isoString(t))
			}
			return detected{Type: "once", Normalized: isoString(t)}, nil
		}
	}
	if validateCronExpressionAt(trimmed, now).Valid {
		return detected{Type: "cron", Normalized: trimmed}, nil
	}
	return detected{}, fmt.Errorf(`Invalid schedule "%s". Use 6-field cron (e.g. "0 0 9 * * 1" — 9am every Monday), interval ("5m"/"1h"), or one-shot ("+10m" / ISO).`, s)
}

// cronValidation is validateCronExpression's result.
type cronValidation struct {
	Valid bool
	Error string
}

// validateCronExpression: 6-field cron, "second minute hour dom month dow", judged at this moment.
// upstream: schedule.ts validateCronExpression.
func validateCronExpression(expr string) cronValidation { return validateCronExpressionAt(expr, time.Now()) }

// validateCronExpressionAt is validateCronExpression at the instant now: constructing a Cron searches for its first
// run from then, which is what a pattern that never runs can fail on (cronStackLimit).
func validateCronExpressionAt(expr string, now time.Time) cronValidation {
	fields := 1 // "".split(/\s+/) is [""]
	if t := jsTrim(expr); t != "" {
		fields = len(jsFields(t))
	}
	if fields != 6 {
		return cronValidation{Error: fmt.Sprintf(`Cron must have 6 fields (second minute hour dom month dow), got %d. Example: "0 0 9 * * 1" for 9am every Monday.`, fields)}
	}
	if _, err := newCronAt(expr, now); err != nil {
		return cronValidation{Error: err.Error()}
	}
	return cronValidation{Valid: true}
}

// parseRelativeTime: "+10s"/"+5m"/"+1h"/"+2d" as an ISO timestamp from now; false when s is not that shape; an error
// when the time is out of range. upstream: schedule.ts parseRelativeTime.
func parseRelativeTime(s string, now time.Time) (string, bool, error) {
	m := relativeTimeRe.FindStringSubmatch(s)
	if m == nil {
		return "", false, nil
	}
	n, _ := jsParseInt(m[1])
	at := float64(now.UnixMilli()) + n*unitMs[m[2]]
	if math.Abs(at) > maxDateMs {
		return "", false, errors.New("Invalid time value")
	}
	return isoString(time.UnixMilli(int64(at))), true, nil
}

// parseInterval: "10s"/"5m"/"1h"/"2d" as milliseconds. upstream: schedule.ts parseInterval.
func parseInterval(s string) (float64, bool) {
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ := jsParseInt(m[1])
	return n * unitMs[m[2]], true
}
