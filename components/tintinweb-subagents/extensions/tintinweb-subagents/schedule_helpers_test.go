package tintinweb_subagents

import (
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

// ---- a fake clock: the original's schedule tests run on vitest's fake timers (vi.useFakeTimers) ----

type schedTimer struct {
	at      time.Time
	f       func()
	clock   *schedClock
	stopped bool
	seq     int
}

func (t *schedTimer) stop() {
	t.clock.mu.Lock()
	t.stopped = true
	t.clock.mu.Unlock()
}

// schedClock is time that moves only when advance says so; timers fire in order as it passes them.
type schedClock struct {
	mu     sync.Mutex
	t      time.Time
	timers []*schedTimer
	seq    int
}

func newFakeClock() *schedClock { return &schedClock{t: time.Now().Truncate(time.Millisecond)} }

func (c *schedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *schedClock) afterFunc(d time.Duration, f func()) interface{ stop() } {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &schedTimer{at: c.t.Add(d), f: f, clock: c, seq: c.seq}
	c.timers = append(c.timers, t)
	return t
}

// advance moves the clock by d, firing every timer that falls due (including the ones the callbacks arm).
func (c *schedClock) advance(d time.Duration) {
	c.mu.Lock()
	target := c.t.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		sort.SliceStable(c.timers, func(i, j int) bool {
			if !c.timers[i].at.Equal(c.timers[j].at) {
				return c.timers[i].at.Before(c.timers[j].at)
			}
			return c.timers[i].seq < c.timers[j].seq
		})
		var next *schedTimer
		for len(c.timers) > 0 {
			head := c.timers[0]
			if head.stopped {
				c.timers = c.timers[1:]
				continue
			}
			if !head.at.After(target) {
				next = head
				c.timers = c.timers[1:]
			}
			break
		}
		if next == nil {
			c.t = target
			c.mu.Unlock()
			return
		}
		if next.at.After(c.t) {
			c.t = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

// ---- a mock agent manager and an event log ----

type spawnCall struct {
	typ, prompt string
	opts        spawnJobOptions
}

type mockRecord struct {
	mu     sync.Mutex
	status string
	done   chan struct{}
}

func (r *mockRecord) setStatus(s string) { r.mu.Lock(); r.status = s; r.mu.Unlock() }
func (r *mockRecord) get() string        { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *mockRecord) resolve()           { close(r.done) }

// mockManager is the scheduler's view of the manager, scripted by the test. With faithful records (newFaithfulManager)
// a spawn creates a record whose run the test settles; the default mock has none, as the original's first mock has a
// record that is already settled with no status.
type mockManager struct {
	mu       sync.Mutex
	calls    []spawnCall
	spawnErr []error // consumed one per spawn
	startErr []error // consumed one per awaitStartup
	panics   string  // "spawn" or "startup": that method panics instead of returning
	records  map[string]*mockRecord
	order    []*mockRecord
	faithful string // "": no records. Otherwise the status a new record has
	autoDone bool   // settle a new record's run at once (a fast-finishing agent)
	next     int
}

func newMockManager() *mockManager { return &mockManager{records: map[string]*mockRecord{}} }

func newFaithfulManager(status string, autoDone bool) *mockManager {
	m := newMockManager()
	m.faithful, m.autoDone = status, autoDone
	return m
}

func (m *mockManager) spawn(typ, prompt string, o spawnJobOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, spawnCall{typ, prompt, o})
	if m.panics == "spawn" {
		panic("spawn went wrong")
	}
	if len(m.spawnErr) > 0 {
		err := m.spawnErr[0]
		m.spawnErr = m.spawnErr[1:]
		if err != nil {
			return "", err
		}
	}
	m.next++
	id := "agent-" + strconv.Itoa(m.next)
	rec := &mockRecord{status: m.faithful, done: make(chan struct{})}
	if m.faithful == "" {
		rec.status = ""
		close(rec.done)
	}
	m.records[id] = rec
	m.order = append(m.order, rec)
	if m.autoDone && m.faithful != "" {
		go rec.resolve()
	}
	return id, nil
}

func (m *mockManager) awaitStartup(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.panics == "startup" {
		panic("startup went wrong")
	}
	if len(m.startErr) > 0 {
		err := m.startErr[0]
		m.startErr = m.startErr[1:]
		return err
	}
	return nil
}

func (m *mockManager) getRecord(id string) *scheduleRun {
	m.mu.Lock()
	rec := m.records[id]
	m.mu.Unlock()
	if rec == nil {
		return nil
	}
	return &scheduleRun{Done: rec.done, Status: rec.get}
}

func (m *mockManager) spawned() []spawnCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]spawnCall{}, m.calls...)
}

func (m *mockManager) record(i int) *mockRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order[i]
}

// eventLog records what the scheduler publishes on `subagents:scheduled`.
type eventLog struct {
	mu     sync.Mutex
	events []scheduleChange
}

// settle waits, at most d, for the runs started by fires to have their outcome recorded (a run outlives stop(): its
// outcome is still written to the store it was started with).
func (s *subagentScheduler) settle(d time.Duration) {
	done := make(chan struct{})
	go func() { s.settling.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (l *eventLog) emit(ev scheduleChange) {
	l.mu.Lock()
	l.events = append(l.events, ev)
	l.mu.Unlock()
}
func (l *eventLog) all() []scheduleChange {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]scheduleChange{}, l.events...)
}
func (l *eventLog) types() []string {
	var out []string
	for _, e := range l.all() {
		out = append(out, e.Type)
	}
	return out
}
func (l *eventLog) has(match func(scheduleChange) bool) bool {
	for _, e := range l.all() {
		if match(e) {
			return true
		}
	}
	return false
}

// schedRig is a started scheduler over a temp store.
type schedRig struct {
	dir     string
	store   *scheduleStore
	sched   *subagentScheduler
	manager *mockManager
	events  *eventLog
}

func startSched(t *testing.T, clock scheduleClock, manager *mockManager) *schedRig {
	t.Helper()
	dir := t.TempDir()
	r := &schedRig{dir: dir, store: newScheduleStore(dir + "/s.json"), sched: newSubagentScheduler(clock), manager: manager, events: &eventLog{}}
	r.sched.start(manager, r.store, r.events.emit)
	t.Cleanup(func() {
		r.sched.stop()
		r.sched.settle(2 * time.Second) // a fire's outcome is written by a goroutine: let it finish before the directory goes
	})
	return r
}

func (r *schedRig) job(t *testing.T, id string) scheduledSubagent {
	t.Helper()
	for _, j := range r.sched.list() {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %s", id)
	return scheduledSubagent{}
}

func input(name, schedule string) newJobInput {
	return newJobInput{Name: name, Description: "x", Schedule: schedule, SubagentType: "general-purpose", Prompt: "p"}
}

// waitFor polls a condition for up to timeout (the original's tests poll at 5 ms as well).
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waitFor %s timed out after %s", what, timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func makeJob(mod func(*scheduledSubagent)) scheduledSubagent {
	every := float64(5 * 60_000)
	j := scheduledSubagent{ID: "job-" + nanoid(), Name: "test-job", Description: "test", Schedule: "5m", ScheduleType: "interval", IntervalMs: &every,
		SubagentType: "general-purpose", Prompt: "hello", Enabled: true, CreatedAt: isoString(time.Now())}
	if mod != nil {
		mod(&j)
	}
	return j
}
