package pi_permission_system

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Twins of test/authority/forwarding-manager.test.ts. The original drives vitest fake timers; here a hand-advanced scheduler stands
// in for setInterval, and `advance` settles the drain goroutine after each tick the way advanceTimersByTimeAsync flushes microtasks.

type fwScheduler struct {
	mu      sync.Mutex
	fn      func()
	active  bool
	created int
}

func (s *fwScheduler) SetInterval(fn func(), every time.Duration) func() {
	if every != PermissionForwardingPollIntervalMs*time.Millisecond {
		panic("unexpected poll interval")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fn, s.active = fn, true
	s.created++
	return func() { s.mu.Lock(); s.active = false; s.mu.Unlock() }
}

// fwFakeForwarder is `vi.fn(): Promise<void>`: it records its contexts and either returns at once or blocks until released.
type fwFakeForwarder struct {
	mu      sync.Mutex
	calls   []ForwarderContext
	block   chan struct{}
	blocked int
}

func (f *fwFakeForwarder) ProcessInbox(ctx ForwarderContext) {
	f.mu.Lock()
	f.calls = append(f.calls, ctx)
	block := f.block
	if block != nil {
		f.blocked++
	}
	f.mu.Unlock()
	if block != nil {
		<-block
		f.mu.Lock()
		f.blocked--
		f.mu.Unlock()
	}
}

func (f *fwFakeForwarder) callCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *fwFakeForwarder) blockedNow() int { f.mu.Lock(); defer f.mu.Unlock(); return f.blocked }
func (f *fwFakeForwarder) lastCall() ForwarderContext {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}
func (f *fwFakeForwarder) clear() { f.mu.Lock(); f.calls = nil; f.mu.Unlock() }

// fwAnnouncer is a ServingAnnouncer whose calls can be counted.
type fwAnnouncer struct {
	mu     sync.Mutex
	marks  []string
	clears []string
}

func (a *fwAnnouncer) MarkServing(id string) {
	a.mu.Lock()
	a.marks = append(a.marks, id)
	a.mu.Unlock()
}
func (a *fwAnnouncer) ClearServing(id string) {
	a.mu.Lock()
	a.clears = append(a.clears, id)
	a.mu.Unlock()
}
func (a *fwAnnouncer) markList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.marks...)
}
func (a *fwAnnouncer) clearList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.clears...)
}
func (a *fwAnnouncer) resetMarks() { a.mu.Lock(); a.marks = nil; a.mu.Unlock() }

type fwManagerEnv struct {
	t         *testing.T
	manager   *ForwardingManager
	scheduler *fwScheduler
	forwarder *fwFakeForwarder
	logger    *fwLogger
}

func fwNewManager(t *testing.T, serving ServingAnnouncer) *fwManagerEnv {
	t.Helper()
	fwClearHintEnv(t)
	if serving == nil {
		serving = NewServingSessionRegistry()
	}
	env := &fwManagerEnv{t: t, scheduler: &fwScheduler{}, forwarder: &fwFakeForwarder{}, logger: &fwLogger{}}
	env.manager = NewForwardingManager(ForwardingManagerDeps{Forwarder: env.forwarder, Serving: serving, Logger: env.logger, Scheduler: env.scheduler})
	t.Cleanup(func() {
		env.manager.Stop()
		env.release()
		env.manager.drains.Wait()
	})
	return env
}

// release lets every blocked drain return.
func (e *fwManagerEnv) release() {
	e.forwarder.mu.Lock()
	if e.forwarder.block != nil {
		close(e.forwarder.block)
		e.forwarder.block = nil
	}
	e.forwarder.mu.Unlock()
}

func (e *fwManagerEnv) hang() {
	e.forwarder.mu.Lock()
	e.forwarder.block = make(chan struct{})
	e.forwarder.mu.Unlock()
}

// advance is advanceTimersByTimeAsync: fire each poll tick in turn, letting the drain it started run to its next await.
func (e *fwManagerEnv) advance(ms int) {
	e.t.Helper()
	for i := 0; i < ms/PermissionForwardingPollIntervalMs; i++ {
		e.scheduler.mu.Lock()
		fn, active := e.scheduler.fn, e.scheduler.active
		e.scheduler.mu.Unlock()
		if !active {
			return
		}
		fn()
		fwPoll(e.t, "the drain to settle", func() bool {
			e.manager.mu.Lock()
			processing := e.manager.processing
			e.manager.mu.Unlock()
			return processing == (e.forwarder.blockedNow() > 0)
		})
	}
}

func fwMgrCtx(hasUI bool, sessionID string) ForwarderContext {
	return ForwarderContext{HasUI: hasUI, Cwd: "/project", SessionManager: &fwSessionManager{sessionID: fwID(sessionID)}}
}

// fwChurningCtx is a ctx whose session id can change after start, as an in-place session fork does.
func fwChurningCtx(sessionID string) (ForwarderContext, *fwSessionManager) {
	sm := &fwSessionManager{sessionID: fwID(sessionID)}
	return ForwarderContext{HasUI: true, Cwd: "/project", SessionManager: sm}, sm
}

func TestForwardingManagerStop(t *testing.T) {
	const f = "authority/forwarding-manager"
	tw(t, f, "is a no-op when not started", func(t *testing.T) {
		fwNewManager(t, nil).manager.Stop()
	})
	tw(t, f, "clears the timer and processing state after start()", func(t *testing.T) {
		env := fwNewManager(t, nil)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Stop()
		// After stop, the timer fires no more callbacks.
		env.forwarder.clear()
		env.advance(500)
		if env.forwarder.callCount() != 0 {
			t.Fatal("processInbox called after stop")
		}
	})
}

func TestForwardingManagerStart(t *testing.T) {
	const f = "authority/forwarding-manager"
	tw(t, f, "does not start polling when hasUI is false", func(t *testing.T) {
		env := fwNewManager(t, nil)
		env.manager.Start(fwMgrCtx(false, "sess-1"))
		env.advance(500)
		if env.forwarder.callCount() != 0 || env.scheduler.created != 0 {
			t.Fatal("polled without UI")
		}
	})
	tw(t, f, "stops any existing poll and does not start a new one when hasUI is false", func(t *testing.T) {
		env := fwNewManager(t, nil)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		// Now stop the polling by calling start() with a no-UI ctx.
		env.manager.Start(fwMgrCtx(false, "sess-1"))
		env.forwarder.clear()
		env.advance(500)
		if env.forwarder.callCount() != 0 {
			t.Fatal("polled after a no-UI start")
		}
	})
	// it.each(SUBAGENT_ENV_HINT_KEYS): outside the ledger.
	for _, key := range SubagentEnvHintKeys {
		t.Run("keeps polling a UI context when "+key+" is set", func(t *testing.T) {
			// Serving eligibility is hasUI alone. A spawner may export a marker from its own root process so its children inherit it,
			// and that root must keep draining the inbox those children write into (#907).
			env := fwNewManager(t, nil)
			t.Setenv(key, "sess-1")
			env.manager.Start(fwMgrCtx(true, "sess-1"))
			env.advance(500)
			if env.forwarder.callCount() == 0 {
				t.Fatal("polling stopped")
			}
		})
	}
	tw(t, f, "starts polling and calls processInbox on tick", func(t *testing.T) {
		env := fwNewManager(t, nil)
		ctx := fwMgrCtx(true, "sess-1")
		env.manager.Start(ctx)
		env.advance(250)
		if env.forwarder.callCount() != 1 || env.forwarder.lastCall() != ctx {
			t.Fatalf("calls=%d", env.forwarder.callCount())
		}
	})
	tw(t, f, "is idempotent — calling start() twice does not create a second timer", func(t *testing.T) {
		env := fwNewManager(t, nil)
		ctx := fwMgrCtx(true, "sess-1")
		env.manager.Start(ctx)
		env.manager.Start(ctx)
		env.advance(250)
		// Only one tick should fire per interval, not two.
		if env.forwarder.callCount() != 1 || env.scheduler.created != 1 {
			t.Fatalf("calls=%d timers=%d", env.forwarder.callCount(), env.scheduler.created)
		}
	})
	tw(t, f, "updates the context when called again while already running", func(t *testing.T) {
		env := fwNewManager(t, nil)
		ctx2 := fwMgrCtx(true, "sess-2")
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Start(ctx2)
		env.advance(250)
		// The process call should use the newer context.
		if env.forwarder.lastCall() != ctx2 {
			t.Fatal("stale context")
		}
	})
	tw(t, f, "skips a tick while processing is in progress", func(t *testing.T) {
		env := fwNewManager(t, nil)
		// Make processInbox hang so processing=true persists.
		env.hang()
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		// First tick starts processing.
		env.advance(250)
		if env.forwarder.callCount() != 1 {
			t.Fatalf("calls=%d", env.forwarder.callCount())
		}
		// Second tick is skipped because the processing flag is still true.
		env.advance(250)
		if env.forwarder.callCount() != 1 {
			t.Fatalf("calls=%d", env.forwarder.callCount())
		}
		// Resolve, and a third tick should fire.
		env.release()
		env.manager.drains.Wait()
		env.advance(250)
		if env.forwarder.callCount() != 2 {
			t.Fatalf("calls=%d", env.forwarder.callCount())
		}
	})
}

func TestForwardingManagerServingAnnouncement(t *testing.T) {
	const f = "authority/forwarding-manager"
	tw(t, f, "marks the polled session as serving", func(t *testing.T) {
		serving := NewServingSessionRegistry()
		fwNewManager(t, serving).manager.Start(fwMgrCtx(true, "sess-1"))
		if !reflect.DeepEqual(serving.ServingIDs(), []string{"sess-1"}) {
			t.Fatal(serving.ServingIDs())
		}
	})
	tw(t, f, "logs the polled session id once per session", func(t *testing.T) {
		env := fwNewManager(t, nil)
		ctx := fwMgrCtx(true, "sess-1")
		env.manager.Start(ctx)
		env.manager.Start(ctx)
		r := env.logger.reviews()
		if len(r) != 1 || r[0].event != "forwarded_permission.serving_started" || !fwEqual(r[0].details, fwObj("sessionId", "sess-1")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "clears the mark on stop()", func(t *testing.T) {
		serving := NewServingSessionRegistry()
		env := fwNewManager(t, serving)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Stop()
		if len(serving.ServingIDs()) != 0 {
			t.Fatal(serving.ServingIDs())
		}
	})
	tw(t, f, "logs serving_stopped only when it was serving", func(t *testing.T) {
		env := fwNewManager(t, nil)
		env.manager.Stop()
		if len(env.logger.reviews()) != 0 {
			t.Fatal("logged without serving")
		}
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.logger.clearReview()
		env.manager.Stop()
		r := env.logger.reviews()
		if len(r) != 1 || r[0].event != "forwarded_permission.serving_stopped" || !fwEqual(r[0].details, fwObj("sessionId", "sess-1")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "moves the mark when the session id changes", func(t *testing.T) {
		serving := NewServingSessionRegistry()
		env := fwNewManager(t, serving)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Start(fwMgrCtx(true, "sess-2"))
		if !reflect.DeepEqual(serving.ServingIDs(), []string{"sess-2"}) {
			t.Fatal(serving.ServingIDs())
		}
	})
	tw(t, f, "clears the mark when a later context no longer qualifies", func(t *testing.T) {
		serving := NewServingSessionRegistry()
		env := fwNewManager(t, serving)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Start(fwMgrCtx(false, "sess-1"))
		if len(serving.ServingIDs()) != 0 {
			t.Fatal(serving.ServingIDs())
		}
	})
	tw(t, f, "never marks a session it does not poll", func(t *testing.T) {
		serving := NewServingSessionRegistry()
		fwNewManager(t, serving).manager.Start(fwMgrCtx(false, "sess-1"))
		if len(serving.ServingIDs()) != 0 {
			t.Fatal(serving.ServingIDs())
		}
	})
	tw(t, f, "marks nothing when the session id is unreachable", func(t *testing.T) {
		// A serving record under the `unknown` sentinel names a session no child can target, so publishing one only litters the
		// heartbeat directory.
		serving := NewServingSessionRegistry()
		env := fwNewManager(t, serving)
		ctx, sm := fwChurningCtx("sess-1")
		sm.setSessionID(func() (string, error) { return "", errors.New("session id unavailable") })
		env.manager.Start(ctx)
		if len(serving.ServingIDs()) != 0 || len(env.logger.reviews()) != 0 {
			t.Fatalf("ids=%v review=%+v", serving.ServingIDs(), env.logger.reviews())
		}
	})
}

func TestForwardingManagerServingRefresh(t *testing.T) {
	const f = "authority/forwarding-manager"
	tw(t, f, "re-announces on every poll tick, so the announcement cannot decay", func(t *testing.T) {
		serving := &fwAnnouncer{}
		env := fwNewManager(t, serving)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		serving.resetMarks()
		env.advance(750)
		if !reflect.DeepEqual(serving.markList(), []string{"sess-1", "sess-1", "sess-1"}) {
			t.Fatal(serving.markList())
		}
	})
	tw(t, f, "re-announces while a drain is still in flight", func(t *testing.T) {
		// A human deliberating at a forwarded dialog holds processInbox open for as long as they take. That session is serving, and
		// must not read as gone to another child while it waits, so the refresh cannot sit behind the processing guard.
		serving := &fwAnnouncer{}
		env := fwNewManager(t, serving)
		env.hang()
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.advance(250)
		if env.forwarder.callCount() != 1 {
			t.Fatalf("calls=%d", env.forwarder.callCount())
		}
		serving.resetMarks()
		env.advance(750)
		if len(serving.markList()) != 3 {
			t.Fatal(serving.markList())
		}
	})
	tw(t, f, "adds no review entry per refresh", func(t *testing.T) {
		env := fwNewManager(t, &fwAnnouncer{})
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.logger.clearReview()
		env.advance(1000)
		if len(env.logger.reviews()) != 0 {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
	tw(t, f, "republishes under the new id when the session id changes mid-session", func(t *testing.T) {
		// processInbox reads the live id on every tick, so an announcement pinned to the id captured at start drifts away from the
		// inbox being drained: a child holding the old id waits out the full timeout while a child holding the new one finds no
		// heartbeat at all (#907).
		serving := &fwAnnouncer{}
		env := fwNewManager(t, serving)
		ctx, sm := fwChurningCtx("sess-1")
		env.manager.Start(ctx)
		serving.resetMarks()
		sm.setSessionID(fwID("sess-2"))
		env.advance(250)
		if !reflect.DeepEqual(serving.clearList(), []string{"sess-1"}) || !reflect.DeepEqual(serving.markList(), []string{"sess-2"}) {
			t.Fatalf("clears=%v marks=%v", serving.clearList(), serving.markList())
		}
		serving.resetMarks()
		env.advance(500)
		if !reflect.DeepEqual(serving.markList(), []string{"sess-2", "sess-2"}) {
			t.Fatal(serving.markList())
		}
	})
	tw(t, f, "logs the migration once, not once per tick", func(t *testing.T) {
		env := fwNewManager(t, &fwAnnouncer{})
		ctx, sm := fwChurningCtx("sess-1")
		env.manager.Start(ctx)
		env.logger.clearReview()
		sm.setSessionID(fwID("sess-2"))
		env.advance(1000)
		r := env.logger.reviews()
		if len(r) != 2 || r[0].event != "forwarded_permission.serving_stopped" || !fwEqual(r[0].details, fwObj("sessionId", "sess-1")) ||
			r[1].event != "forwarded_permission.serving_started" || !fwEqual(r[1].details, fwObj("sessionId", "sess-2")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "keeps serving the last reachable id when the live id is unreachable", func(t *testing.T) {
		serving := &fwAnnouncer{}
		env := fwNewManager(t, serving)
		ctx, sm := fwChurningCtx("sess-1")
		env.manager.Start(ctx)
		serving.resetMarks()
		env.logger.clearReview()
		sm.setSessionID(func() (string, error) { return "", errors.New("session id unavailable") })
		env.advance(750)
		if !reflect.DeepEqual(serving.markList(), []string{"sess-1", "sess-1", "sess-1"}) || len(serving.clearList()) != 0 || len(env.logger.reviews()) != 0 {
			t.Fatalf("marks=%v clears=%v review=%+v", serving.markList(), serving.clearList(), env.logger.reviews())
		}
	})
	tw(t, f, "stops re-announcing once stopped", func(t *testing.T) {
		serving := &fwAnnouncer{}
		env := fwNewManager(t, serving)
		env.manager.Start(fwMgrCtx(true, "sess-1"))
		env.manager.Stop()
		serving.resetMarks()
		env.advance(750)
		if len(serving.markList()) != 0 {
			t.Fatal(serving.markList())
		}
	})
}

// The real clock path of the scheduler: one real tick reaches the drain, and Stop ends the goroutine for good.
func TestForwardingManagerRealTicker(t *testing.T) {
	fwClearHintEnv(t)
	forwarder := &fwFakeForwarder{}
	m := NewForwardingManager(ForwardingManagerDeps{Forwarder: forwarder, Serving: NewServingSessionRegistry(), Logger: &fwLogger{}})
	m.Start(fwMgrCtx(true, "sess-1"))
	fwPoll(t, "a real tick", func() bool { return forwarder.callCount() > 0 })
	m.Stop()
	m.drains.Wait()
	seen := forwarder.callCount()
	time.Sleep(3 * PermissionForwardingPollIntervalMs * time.Millisecond)
	if forwarder.callCount() != seen {
		t.Fatal("ticks continued after Stop")
	}
}

// Go-specific, as the original behaves: Stop clears the processing flag, so a drain abandoned mid-flight by a stop does not block the
// next start's first tick.
func TestForwardingManagerStopClearsTheProcessingFlag(t *testing.T) {
	env := fwNewManager(t, nil)
	env.hang()
	env.manager.Start(fwMgrCtx(true, "sess-1"))
	env.advance(250)
	if env.forwarder.callCount() != 1 {
		t.Fatal("the first drain did not start")
	}
	env.manager.Stop()
	env.manager.Start(fwMgrCtx(true, "sess-1"))
	// The scheduler is the fake: fire the new timer's first tick by hand. The first drain is still blocked, and a second one starts.
	env.scheduler.mu.Lock()
	fn := env.scheduler.fn
	env.scheduler.mu.Unlock()
	fn()
	fwPoll(t, "the second drain", func() bool { return env.forwarder.callCount() == 2 })
}
