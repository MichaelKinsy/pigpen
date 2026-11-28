// Package pause implements omp's process-wide pause gate (`AgentPauseGate`)
// and `/pause` slash command.
//
// Engaging the gate parks extension-owned tool calls (via `EventToolCall`) and
// any cooperative loops (subagents, advisors, eval) polling `WaitUntilResumed`
// until resumed, without aborting in-flight work. Cancelling one waiter's
// context releases only that waiter without resuming the gate for others.
//
// upstream: packages/agent/src/pause.ts:25-107,
//
//	packages/coding-agent/src/slash-commands/builtin-control.ts:24-40,112-120,
//	packages/tui/src/overlays/pause-screen.ts:412-440
package pause

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// WorkSummary describes outstanding active work reported by `/pause`.
// upstream: packages/tui/src/overlays/pause-screen.ts:28-31 (PauseWorkStatus)
type WorkSummary struct {
	UnsettledTurns int
	UnfinishedJobs int
	ActiveAgents   int
}

// Total returns the total count of active running jobs/agents/turns.
func (w WorkSummary) Total() int {
	n := w.UnfinishedJobs + w.ActiveAgents
	if w.UnsettledTurns > w.ActiveAgents {
		n = w.UnfinishedJobs + w.UnsettledTurns
	}
	if n < 0 {
		return 0
	}
	return n
}

// GateStatus is an immutable snapshot of a Gate's state.
type GateStatus struct {
	Paused      bool
	PausedSince time.Time
	Elapsed     time.Duration
}

// Gate is a process-wide pause gate mirroring omp's AgentPauseGate.
// Safe for concurrent use from any number of goroutines.
// upstream: packages/agent/src/pause.ts:25-104
type Gate struct {
	notifyMu     sync.Mutex // serializes Pause/Resume transitions + listener delivery; never held by IsPaused/OnChange/WaitUntilResumed
	mu           sync.Mutex
	waitCh       chan struct{} // non-nil and open while paused; closed & cleared on resume
	pausedAt     time.Time
	listeners    map[uint64]func(paused bool)
	nextListener uint64
}

// NewGate constructs an unpaused Gate.
func NewGate() *Gate {
	return &Gate{
		listeners: make(map[uint64]func(paused bool)),
	}
}

// DefaultGate is the process-wide singleton pause gate.
// upstream: packages/agent/src/pause.ts:107 (agentPauseGate)
var DefaultGate = NewGate()

// Pause engages the gate. Returns true if newly paused, or false (no-op) when
// already paused.
// upstream: packages/agent/src/pause.ts:42-48
func (g *Gate) Pause() bool {
	g.notifyMu.Lock()
	defer g.notifyMu.Unlock()

	g.mu.Lock()
	if g.waitCh != nil {
		g.mu.Unlock()
		return false
	}
	g.waitCh = make(chan struct{})
	g.pausedAt = time.Now()
	listeners := g.snapshotListenersLocked()
	g.mu.Unlock()

	notifyListeners(listeners, true)
	return true
}

// Resume releases the gate, closing the wait channel and waking every parked
// waiter atomically. Returns the elapsed pause duration and true if the gate
// was paused, or (0, false) when not paused. Safe to call repeatedly.
// upstream: packages/agent/src/pause.ts:54-61
func (g *Gate) Resume() (time.Duration, bool) {
	g.notifyMu.Lock()
	defer g.notifyMu.Unlock()

	g.mu.Lock()
	ch := g.waitCh
	if ch == nil {
		g.mu.Unlock()
		return 0, false
	}
	elapsed := time.Since(g.pausedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	g.waitCh = nil
	g.pausedAt = time.Time{}
	close(ch)
	listeners := g.snapshotListenersLocked()
	g.mu.Unlock()

	notifyListeners(listeners, false)
	return elapsed, true
}

// IsPaused reports whether the gate is currently engaged.
// upstream: packages/agent/src/pause.ts:32-34
func (g *Gate) IsPaused() bool {
	g.mu.Lock()
	paused := g.waitCh != nil
	g.mu.Unlock()
	return paused
}

// PausedSince returns the timestamp when the current pause began and true, or
// (zero, false) when not paused.
// upstream: packages/agent/src/pause.ts:37-39
func (g *Gate) PausedSince() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waitCh == nil {
		return time.Time{}, false
	}
	return g.pausedAt, true
}

// Status returns a point-in-time snapshot of the gate's pause state.
func (g *Gate) Status() GateStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waitCh == nil {
		return GateStatus{}
	}
	elapsed := time.Since(g.pausedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return GateStatus{
		Paused:      true,
		PausedSince: g.pausedAt,
		Elapsed:     elapsed,
	}
}

// OnChange subscribes fn to pause/resume transitions and returns an idempotent
// unsubscribe function. Listeners are always invoked outside g.mu and panics
// inside a listener are recovered so host UI subscribers never break the gate.
// upstream: packages/agent/src/pause.ts:64-67, 95-103
func (g *Gate) OnChange(fn func(paused bool)) func() {
	if fn == nil {
		return func() {}
	}
	g.mu.Lock()
	if g.listeners == nil {
		g.listeners = make(map[uint64]func(paused bool))
	}
	g.nextListener++
	id := g.nextListener
	g.listeners[id] = fn
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.listeners, id)
			g.mu.Unlock()
		})
	}
}

// WaitUntilResumed parks the caller until the gate is released. Returns nil
// immediately when not paused. If ctx is cancelled while parked, returns
// ctx.Err() without unpausing the gate for other waiters.
//
// Loops while paused so a re-engaged pause immediately after resume re-parks
// waking waiters instead of letting them slip through.
// upstream: packages/agent/src/pause.ts:74-93
func (g *Gate) WaitUntilResumed(ctx context.Context) error {
	for {
		g.mu.Lock()
		ch := g.waitCh
		g.mu.Unlock()
		if ch == nil {
			return nil
		}
		if ctx == nil {
			<-ch
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *Gate) snapshotListenersLocked() []func(paused bool) {
	if len(g.listeners) == 0 {
		return nil
	}
	out := make([]func(paused bool), 0, len(g.listeners))
	for _, fn := range g.listeners {
		out = append(out, fn)
	}
	return out
}

func notifyListeners(listeners []func(paused bool), paused bool) {
	for _, fn := range listeners {
		func() {
			defer func() { _ = recover() }()
			fn(paused)
		}()
	}
}

// Package-level convenience helpers forwarding to DefaultGate.

// Pause engages DefaultGate.
func Pause() bool { return DefaultGate.Pause() }

// Resume releases DefaultGate.
func Resume() (time.Duration, bool) { return DefaultGate.Resume() }

// IsPaused reports whether DefaultGate is engaged.
func IsPaused() bool { return DefaultGate.IsPaused() }

// PausedSince returns when DefaultGate was paused.
func PausedSince() (time.Time, bool) { return DefaultGate.PausedSince() }

// Status returns DefaultGate's current GateStatus.
func Status() GateStatus { return DefaultGate.Status() }

// WaitUntilResumed waits until DefaultGate is resumed or ctx is cancelled.
func WaitUntilResumed(ctx context.Context) error { return DefaultGate.WaitUntilResumed(ctx) }

// OnChange subscribes to DefaultGate state transitions.
func OnChange(fn func(paused bool)) func() { return DefaultGate.OnChange(fn) }

// WorkStatus is an optional hook wired by the integrator (wiring.go) to count
// running agents, jobs, and unsettled turns for `/pause` status messages.
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts:24-40
var WorkStatus func(ctx sdk.Context) WorkSummary

var (
	sessionTrackMu sync.Mutex
	activeSessions = map[string]int{}
	ownerSessions  = map[string]bool{}
)

// WaitBeforeToolCall is wired into approval.BeforeVeto so unpaused sessions pay
// zero extra EventToolCall IPC round trips while paused sessions park cleanly
// on DefaultGate until resumed or the tool call's context is cancelled.
func WaitBeforeToolCall(ctx sdk.Context) error {
	return waitBeforeToolCallWithGate(ctx, DefaultGate)
}

func waitBeforeToolCallWithGate(ctx sdk.Context, gate *Gate) error {
	if gate == nil || !gate.IsPaused() {
		return nil
	}
	waitCtx, cancel := contextFromSDK(ctx)
	defer cancel()
	if err := gate.WaitUntilResumed(waitCtx); err != nil {
		return fmt.Errorf("Tool call aborted while paused.")
	}
	return nil
}

// Register installs the `/pause` slash command and `lifecycle.OnEnd` cleanup on
// e. Tool-call parking is wired via `approval.BeforeVeto = pause.WaitBeforeToolCall`
// in wiring.go so unpaused sessions incur zero extra EventToolCall IPC overhead.
// Safe to call across multiple sessions.
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts:112-120
func Register(e *sdk.Extension) {
	if e == nil {
		return
	}
	registerWithGate(e, DefaultGate)
}

func registerWithGate(e *sdk.Extension, gate *Gate) {
	lifecycle.OnEnd("pause", func(sessionID string) {
		if sessionID == "" {
			return
		}
		sessionTrackMu.Lock()
		delete(activeSessions, sessionID)
		wasOwner := ownerSessions[sessionID]
		delete(ownerSessions, sessionID)
		remaining := len(activeSessions)
		sessionTrackMu.Unlock()
		// Never leak a process-wide pause when the owning session (UI or
		// headless) or the last tracked session shuts down.
		if (wasOwner || remaining == 0) && gate.IsPaused() {
			gate.Resume()
		}
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDOf(ctx); sid != "" {
			sessionTrackMu.Lock()
			activeSessions[sid]++
			sessionTrackMu.Unlock()
		}
		return nil, nil
	})

	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDOf(ctx); sid != "" {
			lifecycle.End(sid)
		} else if gate.IsPaused() {
			gate.Resume()
		}
		return nil, nil
	})

	e.RegisterCommand("pause", sdk.CommandOptions{
		Description: "Freeze all agents (main, subagents, advisor) until resumed",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			items := []sdk.AutocompleteItem{
				{Value: "status", Label: "status", Description: "Show current pause state and running work"},
				{Value: "resume", Label: "resume", Description: "Resume paused agents and tool calls"},
			}
			p := strings.ToLower(strings.TrimSpace(prefix))
			if p == "" {
				return items, nil
			}
			var out []sdk.AutocompleteItem
			for _, it := range items {
				if strings.HasPrefix(it.Value, p) {
					out = append(out, it)
				}
			}
			return out, nil
		},
		Handler: func(ctx sdk.Context, args string) error {
			return handlePauseCommand(ctx, args, gate)
		},
	})
}

func handlePauseCommand(ctx sdk.Context, rawArgs string, gate *Gate) error {
	arg := strings.ToLower(strings.TrimSpace(rawArgs))
	switch arg {
	case "status":
		st := gate.Status()
		ws := readWorkSummary(ctx)
		if st.Paused {
			ctx.Notify(fmt.Sprintf("Paused for %s (%s). Run /pause or /pause resume to continue.",
				formatCoarseDuration(st.Elapsed), formatWorkSummary(ws)), "info")
		} else {
			ctx.Notify(fmt.Sprintf("Running (%s).", formatWorkSummary(ws)), "info")
		}
		return nil

	case "resume", "off":
		elapsed, ok := gate.Resume()
		if !ok {
			ctx.Notify("Not paused.", "info")
			return nil
		}
		clearOwnerSessions()
		ctx.Notify(fmt.Sprintf("Resumed after %s.", formatCoarseDuration(elapsed)), "info")
		return nil

	case "":
		if gate.IsPaused() {
			elapsed, ok := gate.Resume()
			if ok {
				clearOwnerSessions()
				ctx.Notify(fmt.Sprintf("Resumed after %s.", formatCoarseDuration(elapsed)), "info")
			}
			return nil
		}
		gate.Pause()
		sid := sessionIDOf(ctx)
		if sid != "" {
			sessionTrackMu.Lock()
			ownerSessions[sid] = true
			sessionTrackMu.Unlock()
		}
		ws := readWorkSummary(ctx)
		if safeHasUI(ctx) {
			if sid != "" {
				defer func() {
					sessionTrackMu.Lock()
					delete(ownerSessions, sid)
					sessionTrackMu.Unlock()
				}()
			}
			defer func() {
				if elapsed, ok := gate.Resume(); ok {
					clearOwnerSessions()
					ctx.Notify(fmt.Sprintf("Resumed after %s — agents are running again.", formatCoarseDuration(elapsed)), "info")
				}
			}()
			title := fmt.Sprintf("PAUSED (%s) — Main agent, subagents, and advisor hold at their next step. Select Resume or press Esc to continue.",
				formatWorkSummary(ws))
			_, _, _ = ctx.Select(title, []string{"Resume"})
			return nil
		}
		ctx.Notify(fmt.Sprintf("Paused (%s). Run /pause or /pause resume to continue.", formatWorkSummary(ws)), "info")
		return nil
	default:
		ctx.Notify("Usage: /pause [resume|status]", "warning")
		return nil
	}
}

func clearOwnerSessions() {
	sessionTrackMu.Lock()
	clear(ownerSessions)
	sessionTrackMu.Unlock()
}

func readWorkSummary(ctx sdk.Context) WorkSummary {
	if WorkStatus == nil {
		return WorkSummary{}
	}
	return WorkStatus(ctx)
}

func formatWorkSummary(w WorkSummary) string {
	total := w.Total()
	if w.UnsettledTurns > 0 || w.UnfinishedJobs > 0 {
		return fmt.Sprintf("%d running jobs/agents (turns: %d, jobs: %d)", total, w.UnsettledTurns, w.UnfinishedJobs)
	}
	return fmt.Sprintf("%d running jobs/agents", total)
}

// formatCoarseDuration formats a pause duration cleanly matching omp's
// `formatCoarseDuration` (`packages/tui/src/chrome/format.ts`).
func formatCoarseDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	totalSec := int(d / time.Second)
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	mins := totalSec / 60
	secs := totalSec % 60
	if mins < 60 {
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, remMins)
}

func contextFromSDK(ctx sdk.Context) (context.Context, context.CancelFunc) {
	var zero sdk.Context
	if ctx == zero {
		return context.Background(), func() {}
	}
	done := ctx.Done()
	if done == nil {
		return context.Background(), func() {}
	}
	c, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-done:
			cancel()
		case <-c.Done():
		}
	}()
	return c, cancel
}

func safeHasUI(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	var has bool
	func() {
		defer func() { _ = recover() }()
		has = ctx.HasUI()
	}()
	return has
}

func sessionIDOf(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var id string
	func() {
		defer func() { _ = recover() }()
		if sid, err := ctx.GetSessionID(); err == nil {
			id = strings.TrimSpace(sid)
		}
	}()
	return id
}
