package pi_permission_system

import (
	"sync"
	"time"
)

// Port of src/authority/forwarding-manager.ts: the forwarded-permission polling lifecycle.

// ForwardingController is the narrow interface for the forwarding lifecycle PermissionSession uses; ForwardingManager satisfies it.
type ForwardingController interface {
	Start(ctx ForwarderContext)
	Stop()
}

// InboxProcessor is what ForwardingManager needs from the server: one method that drains this session's forwarded-permission inbox.
// It returns when the drain is finished (the original's promise resolving); it may block for as long as a human takes to answer.
type InboxProcessor interface {
	ProcessInbox(ctx ForwarderContext)
}

// IntervalScheduler is the seam for setInterval, so a test drives the poll ticks by hand instead of sleeping.
type IntervalScheduler interface {
	// SetInterval calls fn every `every` until the returned stop function is called. Stop does not wait for a tick in flight.
	SetInterval(fn func(), every time.Duration) (stop func())
}

// tickerScheduler is the real clock: one goroutine per interval, ended by stop.
type tickerScheduler struct{}

func (tickerScheduler) SetInterval(fn func(), every time.Duration) func() {
	ticker := time.NewTicker(every)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// ForwardingManagerDeps are the constructor config of ForwardingManager.
type ForwardingManagerDeps struct {
	// Forwarder drains this session's forwarded-permission inbox on each tick.
	Forwarder InboxProcessor
	// Serving publishes that this session is draining its inbox, for forwarding children.
	Serving ServingAnnouncer
	Logger  DebugReviewLogger
	// Scheduler drives the poll; nil uses the real clock.
	Scheduler IntervalScheduler
}

// ForwardingManager encapsulates the forwarded-permission polling lifecycle: the timer, the current context and the processing lock.
// Call Start(ctx) on each session event that may activate forwarding; call Stop() on session shutdown.
//
// While polling it publishes the session id it polls to the ServingAnnouncer so a forwarding child can tell that someone is draining
// the inbox it wrote into, and the review log records that id (#719). Serving eligibility is HasUI and nothing else (#907): a
// spawner may export a parent-session marker from its own root process, which must not make the root withdraw serving.
//
// The original's single thread is replaced by a mutex; the drain itself runs on its own goroutine, one at a time, as the original's
// un-awaited promise does.
type ForwardingManager struct {
	deps      ForwardingManagerDeps
	scheduler IntervalScheduler

	mu               sync.Mutex
	stopTimer        func()
	context          *ForwarderContext
	processing       bool
	servingSessionID string
	serving          bool
	// drains tracks the goroutines running ProcessInbox, so a test can wait for them.
	drains sync.WaitGroup
}

// NewForwardingManager is `new ForwardingManager(deps)`.
func NewForwardingManager(deps ForwardingManagerDeps) *ForwardingManager {
	m := &ForwardingManager{deps: deps, scheduler: deps.Scheduler}
	if m.scheduler == nil {
		m.scheduler = tickerScheduler{}
	}
	return m
}

// Start starts polling if ctx has UI. It is a no-op (the timer stays running) if already polling, updating the stored context so the
// next tick uses the latest session. It stops any existing poll when the context does not qualify for forwarding.
func (m *ForwardingManager) Start(ctx ForwarderContext) {
	if !ctx.HasUI {
		m.Stop()
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.context = &ctx
	m.announceServing(GetSessionID(ctx))
	if m.stopTimer != nil {
		return
	}
	m.stopTimer = m.scheduler.SetInterval(m.tick, PermissionForwardingPollIntervalMs*time.Millisecond)
}

// Stop stops polling and clears all internal state.
func (m *ForwardingManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopTimer != nil {
		m.stopTimer()
		m.stopTimer = nil
	}
	m.withdrawServing()
	m.context = nil
	m.processing = false
}

// tick is one poll interval.
func (m *ForwardingManager) tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Ahead of the processing guard: a session whose human is deliberating at a forwarded dialog holds ProcessInbox open for as long
	// as they take, and it is serving throughout. Refreshing behind the guard would let its announcement decay exactly when it is
	// most demonstrably alive, and every other forwarding child would give up on it.
	m.refreshServing()
	if m.context == nil || m.processing {
		return
	}
	m.processing = true
	ctx := *m.context
	m.drains.Add(1)
	go func() {
		defer m.drains.Done()
		m.deps.Forwarder.ProcessInbox(ctx)
		m.mu.Lock()
		m.processing = false
		m.mu.Unlock()
	}()
}

// announceServing publishes sessionID as the served session, replacing any previous one. A no-op when the id is unchanged (start runs
// on every before_agent_start, input and tool_call, and the announcement must not cost a log line per turn) and for an unreachable id.
// Caller holds m.mu.
func (m *ForwardingManager) announceServing(sessionID string) {
	served, ok := NormalizePermissionForwardingSessionID(sessionID)
	if !ok || (m.serving && m.servingSessionID == served) {
		return
	}
	m.withdrawServing()
	m.servingSessionID, m.serving = served, true
	m.deps.Serving.MarkServing(served)
	d := newObject()
	d.set("sessionId", served)
	m.deps.Logger.Review("forwarded_permission.serving_started", d)
}

// refreshServing re-announces the served session, keeping a decayable channel current. The id is re-resolved from the live context
// because a session id can change in place without a turn event and ProcessInbox reads the live one on every tick (#907). A change is
// delegated to announceServing and logged; the unchanged case stays silent. Caller holds m.mu.
func (m *ForwardingManager) refreshServing() {
	if !m.serving || m.context == nil {
		return
	}
	live, ok := NormalizePermissionForwardingSessionID(GetSessionID(*m.context))
	if ok && live != m.servingSessionID {
		m.announceServing(live)
		return
	}
	m.deps.Serving.MarkServing(m.servingSessionID)
}

// withdrawServing withdraws the published session, if any. Caller holds m.mu.
func (m *ForwardingManager) withdrawServing() {
	if !m.serving {
		return
	}
	sessionID := m.servingSessionID
	m.servingSessionID, m.serving = "", false
	m.deps.Serving.ClearServing(sessionID)
	d := newObject()
	d.set("sessionId", sessionID)
	m.deps.Logger.Review("forwarded_permission.serving_stopped", d)
}
