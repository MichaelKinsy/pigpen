package tintinweb_subagents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The agent manager: records, the concurrency limit and the queue. upstream: src/agent-manager.ts (the background
// slots, spawn, abort, steer and consume).

const defaultMaxConcurrent = 10 // background agents running at once. upstream: agent-manager.ts:55

// agent statuses. upstream: src/types.ts AgentRecord.status.
const (
	statusQueued    = "queued"
	statusRunning   = "running"
	statusCompleted = "completed"
	statusSteered   = "steered" // wrapped up at the turn limit
	statusStopped   = "stopped"
	statusAborted   = "aborted"
	statusError     = "error"
)

type agentRecord struct {
	ID, Type, Description, Name string
	ToolCallID                  string // the Agent call that started a background agent (the notification's <tool-use-id>)
	Handle, Alias               string // how the agent is addressed besides its id: its type (explore, explore-2) and its name
	Status                      string
	Result, Error               string
	Background                  bool
	StartedAt, CompletedAt      time.Time
	ToolUses, Tokens, Turns     int
	WrappedUp                   bool
	Consumed                    bool
	slot                        bool // holds a background concurrency slot

	// PendingSteers are messages sent before the agent's process exists (queued, or still starting); they are delivered
	// once it does. upstream: AgentRecord.pendingSteers.
	PendingSteers []string

	SessionFile string // the agent's pi session file when it was persisted: what is left to reopen once the record is gone

	spec    childSpec
	child   child
	done    chan struct{}
	cancel  context.CancelFunc // ends the context the child was started with: its process lives no longer
	stopped bool               // stopped by the user or the parent (not a failure)
	started chan struct{}

	// resume is set while a resume of this agent is queued or running. foreground: the caller awaits the run and no
	// notification is sent. upstream: agent-manager.ts resume, startResume.
	resume *resumeRun
}

func newAgentID() string {
	b := make([]byte, 9)
	rand.Read(b)
	return hex.EncodeToString(b)[:17]
}

type manager struct {
	mu            sync.Mutex
	records       map[string]*agentRecord
	order         []string
	maxConcurrent int
	running       int
	queue         []*agentRecord
	tombstones    []*tombstone // evicted agents that can still be reopened, in the order they were evicted
	start         childStarter
	// disposed is set when the session ends: a child that settles after it is closed, not kept.
	disposed bool
	// onSettle runs once a record has settled (completed, stopped or failed).
	onSettle func(*agentRecord)
}

func newManager(start childStarter, onSettle func(*agentRecord)) *manager {
	return &manager{records: map[string]*agentRecord{}, maxConcurrent: defaultMaxConcurrent, start: start, onSettle: onSettle}
}

// assignHandle is base, else base-2, base-3, ... the first form no agent holds. The caller holds m.mu.
func (m *manager) assignHandle(base string) string { return assignHandleIn(base, m.takenHandles()) }

func (m *manager) get(id string) *agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[id]
}

// resolveID accepts an agent id, a unique prefix of one, or an agent's name.
func (m *manager) resolve(idOrName string) *agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[idOrName]; r != nil {
		return r
	}
	var found *agentRecord
	for _, id := range m.order {
		r := m.records[id]
		if idOrName != "" && (strings.HasPrefix(id, idOrName) || r.Handle == idOrName || r.Alias == idOrName || (r.Name != "" && r.Name == idOrName)) {
			if found != nil {
				return nil
			}
			found = r
		}
	}
	return found
}

func (m *manager) list() []*agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*agentRecord, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.records[id])
	}
	return out
}

// spawn creates an agent: it starts at once, or is queued behind the concurrency limit (background agents only;
// a foreground agent is not limited, as `maxConcurrentForeground` defaults to unlimited). upstream: agent-manager.ts spawn.
func (m *manager) spawn(typ, description, name string, spec childSpec, background bool) *agentRecord {
	return m.spawnWith(typ, description, name, spec, spawnOptions{Background: background})
}

// launch runs an agent in a goroutine of its own. A panic in the run settles the agent as failed rather than ending
// the extension.
func (m *manager) launch(r *agentRecord) {
	go func() {
		if guard("agent run", func() { m.run(r) }) {
			m.settlePanicked(r)
		}
	}()
}

// settlePanicked settles an agent whose run panicked, releasing what the run held.
func (m *manager) settlePanicked(r *agentRecord) {
	m.mu.Lock()
	if r.Status != statusRunning { // it had settled: the panic was in what announces it
		m.mu.Unlock()
		return
	}
	r.Status, r.Error, r.CompletedAt, r.resume = statusError, "internal error in the agent run", time.Now(), nil
	next := m.releaseSlotLocked(r)
	c, cancel := r.child, r.cancel
	r.child, r.cancel = nil, nil
	closeOnce(r.started)
	closeOnce(r.done)
	m.mu.Unlock()
	if c != nil {
		c.close()
	}
	if cancel != nil {
		cancel()
	}
	if next != nil {
		m.launch(next)
	}
}

// releaseSlotLocked gives back the concurrency slot of a settled agent and takes the next queued agent, if any is
// to run now. The caller holds m.mu and starts the agent returned.
func (m *manager) releaseSlotLocked(r *agentRecord) *agentRecord {
	if r.slot {
		m.running--
		r.slot = false
	}
	if len(m.queue) == 0 || m.running >= m.maxConcurrent {
		return nil
	}
	next := m.queue[0]
	m.queue = m.queue[1:]
	m.running++
	next.slot = true
	next.Status = statusRunning
	return next
}

// run executes one agent to completion, then releases its slot and starts the next queued agent. The child is bound to
// a context that lives until the child is closed: a child that keeps its conversation in memory outlives the run, so
// that resume can continue it (the original keeps the AgentSession on the record until cleanup).
func (m *manager) run(r *agentRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	old, oldCancel := r.child, r.cancel // the child of an earlier run, which a resume may continue
	r.cancel = cancel                   // abort reads it under the lock, while the child starts
	r.StartedAt = time.Now()
	rr := r.resume
	spec := r.spec
	m.mu.Unlock()
	var c child
	var err error
	if rr != nil {
		c, err = m.reopen(ctx, r, rr)
	} else {
		if spec.InheritContext != nil { // built when the run starts, so a queued agent sees the later conversation
			var pc string
			if pc, err = spec.InheritContext(); err == nil {
				spec.Prompt = pc + spec.Prompt
			}
		}
		if err == nil {
			c, err = m.start(ctx, spec)
		}
	}
	var replaced child
	var replacedCancel context.CancelFunc
	unused := cancel // the context of this run when the child it started is not the one the agent holds
	m.mu.Lock()
	switch {
	case err != nil:
		r.cancel = oldCancel
	case c == old: // the same process took the prompt
		r.cancel = oldCancel
	default:
		r.cancel, unused = cancel, nil
		if old != nil {
			replaced, replacedCancel = old, oldCancel
		}
	}
	if err == nil {
		r.child = c
		if f := c.sessionFile(); f != "" {
			r.SessionFile = f
		}
	}
	stop := r.stopped
	var pending []string
	if err == nil {
		pending, r.PendingSteers = r.PendingSteers, nil
	}
	closeOnce(r.started)
	m.mu.Unlock()
	for _, msg := range pending { // steers that arrived before the process was ready. upstream: agent-manager.ts:840
		_ = c.steer(msg)
	}
	if replaced != nil {
		replaced.close()
	}
	if replacedCancel != nil {
		replacedCancel()
	}
	if unused != nil {
		unused()
	}
	var res *childResult
	if err == nil {
		if stop {
			c.abort()
		}
		res, err = c.wait()
	}
	alive := c != nil && err == nil && c.alive() // an in-memory conversation outlives its run
	m.mu.Lock()
	r.CompletedAt = time.Now()
	switch {
	case r.stopped:
		r.Status = statusStopped
		if res != nil {
			r.Result = res.Text
		}
	case err != nil:
		r.Status = statusError
		r.Error = err.Error()
	// A hard abort keeps "aborted"; then a failed final turn is an "error"; then "steered" or "completed".
	// upstream: agent-manager.ts:850-862.
	case res.Aborted:
		r.Status = statusAborted
	case res.Failure != "":
		r.Status = statusError
		r.Error = res.Failure
	case res.WrappedUp:
		r.Status = statusSteered
	default:
		r.Status = statusCompleted
	}
	if res != nil {
		r.Result, r.ToolUses, r.Tokens, r.Turns, r.WrappedUp = res.Text, res.ToolUses+len(spec.Warnings), res.Tokens, res.Turns, res.WrappedUp
	}
	r.resume = nil
	// The process ends with its run unless the agent still holds a conversation in it (and the session is going on).
	var closing child
	var closingCancel context.CancelFunc
	if c != nil && (!alive || m.disposed) {
		closingCancel, r.cancel = r.cancel, nil
		if alive {
			closing = c
		}
	}
	next := m.releaseSlotLocked(r)
	closeOnce(r.done)
	m.mu.Unlock()
	if closing != nil {
		closing.close()
	}
	if closingCancel != nil {
		closingCancel()
	}
	if m.onSettle != nil && (rr == nil || rr.background) { // a foreground resume is not announced
		m.onSettle(r)
	}
	if next != nil {
		m.launch(next)
	}
}

// awaitStartup waits until the agent has been started, or fails. An agent that is still queued is not waited for: the
// original resolves at once for one that is already running, still queued or unknown (agent-manager.ts awaitStartup).
func (m *manager) awaitStartup(r *agentRecord, timeout time.Duration) error {
	m.mu.Lock()
	queued, started, done := r.Status == statusQueued, r.started, r.done
	m.mu.Unlock()
	if queued {
		return nil
	}
	select {
	case <-started:
		return nil
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("the agent did not start in time")
	}
}

// abort stops a running or queued agent; it reports false for one that is not running. A child that keeps its
// conversation in memory is only told to stop its run: the conversation stays resumable, as `session.abort()` leaves
// it in the original (agent-manager.ts abort); ending the process is up to close.
func (m *manager) abort(r *agentRecord) bool {
	m.mu.Lock()
	switch r.Status {
	case statusQueued:
		for i, q := range m.queue {
			if q == r {
				m.queue = append(m.queue[:i], m.queue[i+1:]...)
				break
			}
		}
		r.Status, r.stopped, r.CompletedAt, r.resume = statusStopped, true, time.Now(), nil
		// The channels are closed under the lock: a resume that lands once the record is stopped replaces them.
		closeOnce(r.started)
		closeOnce(r.done)
		m.mu.Unlock()
		if m.onSettle != nil {
			m.onSettle(r)
		}
		return true
	case statusRunning:
		r.stopped = true
		c := r.child
		cancel := r.cancel
		m.mu.Unlock()
		if c != nil {
			c.abort()
		} else if cancel != nil {
			cancel()
		}
		return true
	}
	m.mu.Unlock()
	return false
}

// steer sends a message into a running agent. upstream: the steer_subagent tool.
func (m *manager) steer(r *agentRecord, message string) error {
	m.mu.Lock()
	status, c := r.Status, r.child
	m.mu.Unlock()
	if status != statusRunning || c == nil {
		return fmt.Errorf("Agent %s is not running (status: %s).", r.ID, status)
	}
	return c.steer(message)
}

// consume marks a finished agent's result as handed to the model, so its notification is not sent. It reports
// false for an unknown or still-running agent. upstream: agent-manager.ts consumeResult.
func (m *manager) consume(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[id]
	if r == nil || r.Status == statusRunning || r.Status == statusQueued {
		return false
	}
	r.Consumed = true
	return true
}

// abortAll stops every running and queued agent.
func (m *manager) abortAll() {
	for _, r := range m.list() {
		m.abort(r)
	}
}

// closeOnce closes ch unless it is closed. The caller holds m.mu, which every close of a record's channels is under.
func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// steerRecord sends a message to a running or queued agent: at once when its process exists, else parked and flushed
// when it does. It reports false for an agent that is neither running nor queued. The process call can block on the
// child, so callers on an input path run it off that path. upstream: agent-manager.ts steer.
func (m *manager) steerRecord(r *agentRecord, message string) bool {
	m.mu.Lock()
	if r.Status != statusRunning && r.Status != statusQueued {
		m.mu.Unlock()
		return false
	}
	c := r.child
	if c == nil {
		r.PendingSteers = append(r.PendingSteers, message)
		m.mu.Unlock()
		return true
	}
	m.mu.Unlock()
	_ = c.steer(message)
	return true
}

// discard forgets an agent that never started (its process could not be launched), giving its handle back.
// upstream: agent-manager.ts launch (an immediate start that fails drops the record).
func (m *manager) discard(r *agentRecord) {
	m.mu.Lock()
	r.child = nil
	m.mu.Unlock()
	m.removeRecord(r.ID)
}
