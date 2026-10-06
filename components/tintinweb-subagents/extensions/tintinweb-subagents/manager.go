package tintinweb_subagents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
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

	spec    childSpec
	child   child
	done    chan struct{}
	cancel  context.CancelFunc
	stopped bool // stopped by the user or the parent (not a failure)
	started chan struct{}
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
	start         childStarter
	// onSettle runs once a record has settled (completed, stopped or failed).
	onSettle func(*agentRecord)
}

func newManager(start childStarter, onSettle func(*agentRecord)) *manager {
	return &manager{records: map[string]*agentRecord{}, maxConcurrent: defaultMaxConcurrent, start: start, onSettle: onSettle}
}

func (m *manager) get(id string) *agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[id]
}

var notHandle = regexp.MustCompile(`[^a-z0-9_-]+`)

// handleBase is the slug of a type or a name: lowercase, `[a-z0-9_-]` only. upstream: mention.ts handleBase.
func handleBase(s string) string {
	slug := notHandle.ReplaceAllString(strings.ToLower(s), "-")
	slug = strings.Trim(slug, "-")
	if r := []rune(slug); len(r) > 32 {
		slug = strings.TrimRight(string(r[:32]), "-")
	}
	if slug == "" {
		return "agent"
	}
	return slug
}

// assignHandle is base, else base-2, base-3, ... the first form no agent holds. The caller holds m.mu.
func (m *manager) assignHandle(base string) string {
	taken := map[string]bool{}
	for _, r := range m.records {
		taken[r.Handle], taken[r.Alias] = true, true
	}
	candidate := base
	for n := 2; taken[candidate]; n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
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
	r := &agentRecord{ID: newAgentID(), Type: typ, Description: description, Name: name, Background: background, spec: spec,
		done: make(chan struct{}), started: make(chan struct{}), Status: statusQueued, StartedAt: time.Now()}
	m.mu.Lock()
	m.records[r.ID] = r
	m.order = append(m.order, r.ID)
	r.Handle = m.assignHandle(handleBase(typ))
	if name != "" {
		r.Alias = m.assignHandle(handleBase(name))
	}
	if background && m.running >= m.maxConcurrent {
		m.queue = append(m.queue, r)
		m.mu.Unlock()
		return r
	}
	if background {
		m.running++
	}
	r.Status = statusRunning
	m.mu.Unlock()
	go m.run(r)
	return r
}

// run executes one agent to completion, then releases its slot and starts the next queued agent.
func (m *manager) run(r *agentRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	r.cancel = cancel // abort reads it under the lock
	r.StartedAt = time.Now()
	m.mu.Unlock()
	c, err := m.start(ctx, r.spec)
	close(r.started)
	var res *childResult
	if err == nil {
		m.mu.Lock()
		r.child = c
		stop := r.stopped
		m.mu.Unlock()
		if stop {
			c.abort()
		}
		res, err = c.wait()
	}
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
		r.Result, r.ToolUses, r.Tokens, r.Turns, r.WrappedUp = res.Text, res.ToolUses, res.Tokens, res.Turns, res.WrappedUp
	}
	if r.Background {
		m.running--
	}
	var next *agentRecord
	if len(m.queue) > 0 && m.running < m.maxConcurrent {
		next = m.queue[0]
		m.queue = m.queue[1:]
		m.running++
		next.Status = statusRunning
	}
	m.mu.Unlock()
	cancel()
	close(r.done)
	if m.onSettle != nil {
		m.onSettle(r)
	}
	if next != nil {
		go m.run(next)
	}
}

// awaitStartup waits until the agent has been started (it may be queued first), or fails.
func (m *manager) awaitStartup(r *agentRecord, timeout time.Duration) error {
	select {
	case <-r.started:
		return nil
	case <-r.done:
		return nil
	case <-time.After(timeout):
		return errors.New("the agent did not start in time")
	}
}

// abort stops a running or queued agent; it reports false for one that is not running.
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
		r.Status, r.stopped, r.CompletedAt = statusStopped, true, time.Now()
		m.mu.Unlock()
		close(r.started)
		close(r.done)
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

// abortAll stops every agent (the session ends).
func (m *manager) abortAll() {
	for _, r := range m.list() {
		m.abort(r)
	}
}
