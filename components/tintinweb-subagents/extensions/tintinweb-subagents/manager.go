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

	// Nested delegation. ParentID is the agent that owns this one ("" for a top-level agent); Depth is 1 for a top-level
	// agent's child process, MaxDepth the cap of its branch. upstream: types.ts AgentRecord depth, parentAgentId,
	// maxSubagentDepth.
	ParentID        string
	Depth, MaxDepth int
	pendingSteers   []string // steers sent before the child was up, sent once it is. upstream: record.pendingSteers

	spec    childSpec
	child   child
	done    chan struct{}
	cancel  context.CancelFunc
	stopped bool // stopped by the user or the parent (not a failure)
	started chan struct{}
	// slotReleased: the record's pool slot (if it held one) has been given back.
	slotReleased bool
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
	// account runs for an owned child before it is reported settled: its spend is folded into its owner's.
	account func(*agentRecord)
}

// owner is the agent that owns a nested child, and the branch the child belongs to. upstream: agent-manager.ts
// SpawnOptions parentAgentId, depth, maxSubagentDepth, configCwd.
type owner struct {
	ParentID  string
	Depth     int // the child's depth
	MaxDepth  int
	ConfigCwd string
}

// isTopLevelAgent says whether a record is one of the session's own agents, rather than owned by another agent.
// upstream: agent-manager.ts isTopLevelAgent.
func isTopLevelAgent(r *agentRecord) bool { return r.ParentID == "" }

// occupiesPoolSlot says whether a record counts against the background concurrency limit. A nested child does not:
// its parent holds a slot already, and queueing a child behind its own parent would deadlock. upstream: occupiesPoolSlot.
func occupiesPoolSlot(r *agentRecord) bool { return r.Background && isTopLevelAgent(r) }

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
	if r := m.records[idOrName]; r != nil && isTopLevelAgent(r) {
		return r
	}
	var found *agentRecord
	for _, id := range m.order {
		r := m.records[id]
		if !isTopLevelAgent(r) {
			continue
		}
		if idOrName != "" && (strings.HasPrefix(id, idOrName) || r.Handle == idOrName || r.Alias == idOrName || (r.Name != "" && r.Name == idOrName)) {
			if found != nil {
				return nil
			}
			found = r
		}
	}
	return found
}

// listTopLevel is the session's own agents, the ones every surface lists. upstream: listAgents().filter(isTopLevelAgent).
func (m *manager) listTopLevel() []*agentRecord {
	var out []*agentRecord
	for _, r := range m.list() {
		if isTopLevelAgent(r) {
			out = append(out, r)
		}
	}
	return out
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
	return m.spawnOwned(typ, description, name, spec, background, nil)
}

// spawnOwned is spawn for an agent that may be owned by another (a nested child; o is nil for a top-level agent).
// An owned child takes no concurrency slot and no handle: nothing but its owner can address it.
// upstream: agent-manager.ts spawn.
func (m *manager) spawnOwned(typ, description, name string, spec childSpec, background bool, o *owner) *agentRecord {
	r := &agentRecord{ID: newAgentID(), Type: typ, Description: description, Name: name, Background: background, spec: spec,
		done: make(chan struct{}), started: make(chan struct{}), Status: statusQueued, StartedAt: time.Now(), Depth: 1, MaxDepth: spec.MaxDepth}
	if o != nil {
		r.ParentID, r.Depth, r.MaxDepth = o.ParentID, o.Depth, o.MaxDepth
		if o.ConfigCwd != "" {
			r.spec.ConfigCwd = o.ConfigCwd
		}
	}
	r.spec.Depth, r.spec.MaxDepth = r.Depth, r.MaxDepth
	m.mu.Lock()
	m.records[r.ID] = r
	m.order = append(m.order, r.ID)
	if isTopLevelAgent(r) {
		r.Handle = m.assignHandle(handleBase(typ))
		if name != "" {
			r.Alias = m.assignHandle(handleBase(name))
		}
	}
	if occupiesPoolSlot(r) && m.running >= m.maxConcurrent {
		m.queue = append(m.queue, r)
		m.mu.Unlock()
		return r
	}
	if occupiesPoolSlot(r) {
		m.running++
	}
	r.Status = statusRunning
	m.mu.Unlock()
	go m.safeRun(r)
	return r
}

// run executes one agent to completion, then releases its slot and starts the next queued agent.
func (m *manager) run(r *agentRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	r.cancel = cancel // abort reads it under the lock
	r.StartedAt = time.Now()
	m.mu.Unlock()
	spec := r.spec
	spec.AgentID = r.ID
	c, err := m.start(ctx, spec)
	if err != nil {
		// A waiter released by `started` must find the failure: a child that did not start is not "running".
		m.mu.Lock()
		if !r.stopped {
			r.Status, r.Error = statusError, err.Error()
		}
		m.mu.Unlock()
	}
	close(r.started)
	var res *childResult
	if err == nil {
		m.mu.Lock()
		r.child = c
		stop := r.stopped
		pending := r.pendingSteers
		r.pendingSteers = nil
		m.mu.Unlock()
		for _, message := range pending {
			_ = c.steer(message)
		}
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
	next := m.releaseSlotLocked(r)
	m.mu.Unlock()
	cancel()
	// The next queued agent starts before this one's bookkeeping runs: nothing below may keep it waiting.
	if next != nil {
		go m.safeRun(next)
	}
	if m.account != nil && !isTopLevelAgent(r) {
		m.account(r)
	}
	// A settled parent stops the children it owns: they are hidden, only it can read them, and one that outlived it
	// would run on unseen. Grandchildren follow through each child's own settle. upstream: agent-manager.ts abortOwnedChildren.
	m.abortOwnedChildren(r.ID)
	close(r.done)
	if m.onSettle != nil {
		m.onSettle(r)
	}
}

// releaseSlotLocked gives back the pool slot a settled record held and takes the next queued record (marked running)
// off the queue; the caller holds m.mu and starts the returned record. Safe to call twice for one record.
func (m *manager) releaseSlotLocked(r *agentRecord) *agentRecord {
	if r.slotReleased {
		return nil
	}
	r.slotReleased = true
	if occupiesPoolSlot(r) {
		m.running--
	}
	if len(m.queue) > 0 && m.running < m.maxConcurrent {
		next := m.queue[0]
		m.queue = m.queue[1:]
		m.running++
		next.Status = statusRunning
		return next
	}
	return nil
}

// safeRun runs an agent in its own goroutine: a panic in it must never take the extension process down, nor leave
// its owner waiting on an agent that never settles.
func (m *manager) safeRun(r *agentRecord) {
	defer func() {
		if v := recover(); v != nil {
			m.crash(r, v)
		}
	}()
	m.run(r)
}

// crash settles a record whose run panicked: an error, its slot returned, its child stopped, its waiters released.
func (m *manager) crash(r *agentRecord, v any) {
	m.mu.Lock()
	if r.CompletedAt.IsZero() {
		r.CompletedAt = time.Now()
	}
	if r.Status == statusRunning || r.Status == statusQueued {
		r.Status, r.Error = statusError, fmt.Sprintf("internal error: %v", v)
	}
	next := m.releaseSlotLocked(r)
	c := r.child
	m.mu.Unlock()
	if next != nil {
		go m.safeRun(next)
	}
	if c != nil {
		go func() {
			defer func() { _ = recover() }()
			c.abort()
		}()
	}
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	select {
	case <-r.done:
	default:
		close(r.done)
	}
}

// abortOwnedChildren stops every agent the given one owns.
func (m *manager) abortOwnedChildren(parentID string) {
	for _, r := range m.list() {
		if r.ParentID == parentID {
			m.abort(r)
		}
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
