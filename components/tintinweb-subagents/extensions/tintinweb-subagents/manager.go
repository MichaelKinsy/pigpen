package tintinweb_subagents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The agent manager: records, the two concurrency pools and the queue, handles, abort, steer, consume, the record
// GC and the tombstones. upstream: src/agent-manager.ts.
//
// There are two independent pools, never one: background (`maxConcurrent`, default 10) bounds detached agents, and
// foreground (`maxConcurrentForeground`, default 0 = unlimited) bounds the agents a caller is blocking on inline
// (spawnAndWait). A foreground agent blocks the parent anyway, so charging it to the background pool would let a
// saturated pool starve the main session of work it could have done itself. Excess agents in either pool wait in one
// queue and start, first in first out within their pool, as slots free up. Nested children and a workflow's children
// take no slot in either (their parent is blocked awaiting them: queueing one behind its own parent would deadlock).

const (
	defaultMaxConcurrent           = 10 // background agents running at once. upstream: agent-manager.ts DEFAULT_MAX_CONCURRENT
	defaultMaxConcurrentForeground = 0  // 0: unlimited. upstream: DEFAULT_MAX_CONCURRENT_FOREGROUND
	recordRetention                = 10 * time.Minute
	maxTombstones                  = 100 // evicted agents that stay addressable by name. upstream: MAX_TOMBSTONES
)

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
	Background                  bool   // upstream isBackground: detached; its completion is announced to the model
	Blocking                    bool   // a caller awaits it inline (spawnAndWait): what maxConcurrentForeground bounds
	ParentAgentID, WorkflowID   string // an owned child reports through its owner, not to the session
	Depth, MaxSubagentDepth     int
	StartedAt, CompletedAt      time.Time
	ToolUses, Tokens, Turns     int
	WrappedUp                   bool
	Consumed                    bool   // the result was handed to the model, so its notification is not sent
	SessionFile                 string // the agent's pi session file when it was persisted: what is left to reopen once the record is gone
	JoinMode, GroupID           string // how the completion of a background agent is announced

	spec          childSpec
	child         child
	done          chan struct{} // closed once the record is final (settled, or stopped while queued); a resume replaces it
	startGate     chan struct{} // closed when the record leaves the queue (started, stopped or the manager disposed)
	cancel        context.CancelFunc // ends the context the child was started with: its process lives no longer
	stopped       bool               // stopped by the user or the parent (not a failure)
	started       chan struct{}
	settled       bool     // the run has settled: its status, result and slot are final (a resume waits for it)
	pool          pool     // the pool this run was charged to when it started
	pendingSteers []string // steers that arrived before the child existed
	onSpawned     func(id string)
	// resume is set while a resume of this agent is queued or running. foreground: the caller awaits the run and no
	// notification is sent. upstream: agent-manager.ts resume, startResume.
	resume *resumeRun
}

// topLevel: one of the session's own agents, not something another agent or a workflow owns. upstream: isTopLevelAgent.
func (r *agentRecord) topLevel() bool { return r.ParentAgentID == "" && r.WorkflowID == "" }

// closeGate, closeStarted and closeDone close a record's channels unless they are closed. The caller holds m.mu, which
// every close is under: a resume that lands once the record has settled replaces the channels.
func (r *agentRecord) closeGate()    { closeOnce(r.startGate) }
func (r *agentRecord) closeStarted() { closeOnce(r.started) }
func (r *agentRecord) closeDone() {
	closeOnce(r.started)
	closeOnce(r.done)
}

// closeOnce closes ch unless it is closed. The caller holds m.mu.
func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

type pool int

const (
	poolNone pool = iota
	poolBackground
	poolForeground
)

// occupiesPoolSlot: a detached top-level agent takes a background slot. upstream: occupiesPoolSlot.
func occupiesPoolSlot(r *agentRecord) bool { return r.Background && r.topLevel() }

// occupiesForegroundSlot: keyed on `Blocking` (a caller awaiting this record inline), not on !Background, because
// spawn is also the funnel for detached starts (the RPC, the registry) that may pass isBackground: false and are
// documented to run at once: they block nobody. upstream: occupiesForegroundSlot.
func occupiesForegroundSlot(r *agentRecord) bool { return r.Blocking && r.topLevel() }

func newAgentID() string {
	b := make([]byte, 9)
	rand.Read(b)
	return hex.EncodeToString(b)[:17]
}

// tombstone is what an evicted agent leaves so `@handle` can reopen its conversation. upstream: AgentTombstone.
type tombstone struct {
	Handle, Alias, ID, Type, Description, SessionFile string
	CompletedAt                                       time.Time
}

type queueEntry struct {
	r    *agentRecord
	pool pool
}

// spawnOptions are what a spawn is told besides the agent type and the child's spec. upstream: SpawnOptions.
type spawnOptions struct {
	Name             string
	Background       bool
	Blocking         bool
	BypassQueue      bool   // start at once even when the pool is full (it still counts its slot)
	ParentAgentID    string // set by the nested delegation tools only
	WorkflowID       string // set by the workflow runtime only
	Depth            int    // 0: 1
	MaxSubagentDepth int
	Signal           <-chan struct{} // the caller's: when it closes the agent is stopped, queued or running
	OnQueued         func(id string, ahead int)
	OnSpawned        func(id string) // the run was kicked off (also when it started from the queue)
	Reclaim          *reclaim        // reopen a conversation under the handle it had
	ResumeFile       string          // reopen this pi session file instead of starting a conversation
}

// resumeRun is one resume of an agent's conversation.
type resumeRun struct {
	prompt     string
	background bool
}

type reclaim struct{ Handle, Alias string }

type manager struct {
	mu                      sync.Mutex
	records                 map[string]*agentRecord
	order                   []string
	maxConcurrent           int
	maxConcurrentForeground int
	runningBackground       int
	runningForeground       int
	queue                   []queueEntry
	tombstones              []*tombstone // evicted agents that can still be reopened, in the order they were evicted
	start                   childStarter
	// disposed is set when the session ends: a child that settles after it is closed, not kept.
	disposed bool
	// onSettle runs once a record has settled (completed, stopped or failed).
	onSettle func(*agentRecord)
	now      func() time.Time
	stopGC   chan struct{}
	gcDone   chan struct{}
}

func newManager(start childStarter, onSettle func(*agentRecord)) *manager {
	return &manager{records: map[string]*agentRecord{}, maxConcurrent: defaultMaxConcurrent,
		maxConcurrentForeground: defaultMaxConcurrentForeground, start: start, onSettle: onSettle, now: time.Now}
}

// startGC sweeps old records every interval until dispose (once: a second call while a sweep runs keeps it). The
// session starts it. upstream: the 60 s setInterval of the constructor.
func (m *manager) startGC(interval time.Duration) {
	m.mu.Lock()
	if m.stopGC != nil {
		m.mu.Unlock()
		return
	}
	m.stopGC, m.gcDone = make(chan struct{}), make(chan struct{})
	stop, done := m.stopGC, m.gcDone
	m.mu.Unlock()
	goGuarded("agent cleanup timer", func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				guard("agent cleanup", m.cleanup)
			case <-stop:
				return
			}
		}
	})
}

// gcRunning reports whether the sweep runs.
func (m *manager) gcRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopGC != nil
}

func (m *manager) stopGCLoop() {
	m.mu.Lock()
	stop, done := m.stopGC, m.gcDone
	m.stopGC, m.gcDone = nil, nil
	m.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// setMaxConcurrent changes the background limit (at least 1) and starts queued agents the new limit allows.
func (m *manager) setMaxConcurrent(n int) {
	m.mu.Lock()
	m.maxConcurrent = max(1, n)
	after := m.drainLocked()
	m.mu.Unlock()
	runAll(after)
}

func (m *manager) getMaxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxConcurrent
}

// setMaxConcurrentForeground changes the foreground limit; 0 is unlimited, the default, and a negative value reads
// as that rather than as 1. Everything queued that the new limit allows starts now, all of it when the limit is
// cleared mid-run.
func (m *manager) setMaxConcurrentForeground(n int) {
	m.mu.Lock()
	m.maxConcurrentForeground = max(0, n)
	after := m.drainLocked()
	m.mu.Unlock()
	runAll(after)
}

func (m *manager) getMaxConcurrentForeground() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxConcurrentForeground
}

func runAll(fns []func()) {
	for _, fn := range fns {
		fn()
	}
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

// takenHandles are the handles in use. A tombstone holds its names too: an evicted `@explore` can still be reopened,
// so a later Explore becomes `explore-2` instead of shadowing a conversation the user can still reach. The caller
// holds m.mu.
func (m *manager) takenHandles() map[string]bool {
	taken := map[string]bool{}
	for _, r := range m.records {
		if r.Handle != "" {
			taken[r.Handle] = true
		}
		if r.Alias != "" {
			taken[r.Alias] = true
		}
	}
	for _, t := range m.tombstones {
		taken[t.Handle] = true
		if t.Alias != "" {
			taken[t.Alias] = true
		}
	}
	return taken
}

// assignHandle is base, else base-2, base-3, ... the first form no agent holds. The caller holds m.mu.
func (m *manager) assignHandle(base string) string {
	taken := m.takenHandles()
	candidate := base
	for n := 2; taken[candidate]; n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
}

// resolve finds a top-level agent by id, or by handle (its name's slug, else its type, explore-2...), the handle
// compared without case: one that can still be steered first, else the most recently started. A tombstone does not
// answer: it has no result to read and nothing to steer. upstream: index.ts resolveAgentRef over
// agent-manager.ts resolveMention.
func (m *manager) resolve(ref string) *agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[ref]; r != nil {
		return r
	}
	return m.resolveLiveLocked(ref)
}

func (m *manager) resolveLiveLocked(name string) *agentRecord {
	wanted := strings.ToLower(name)
	var fallback *agentRecord
	for _, id := range m.order {
		r := m.records[id]
		if r.ParentAgentID != "" {
			continue
		}
		if (r.Handle == "" || strings.ToLower(r.Handle) != wanted) && (r.Alias == "" || strings.ToLower(r.Alias) != wanted) {
			continue
		}
		if r.Status == statusRunning || r.Status == statusQueued {
			return r
		}
		if fallback == nil || r.StartedAt.After(fallback.StartedAt) {
			fallback = r
		}
	}
	return fallback
}

// resolveMention is resolve for `@name`: a live record, else a tombstone to reopen. upstream: resolveMention.
func (m *manager) resolveMention(name string) (live *agentRecord, tomb *tombstone) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.resolveLiveLocked(name); r != nil {
		return r, nil
	}
	if r := m.records[name]; r != nil && r.ParentAgentID == "" {
		return r, nil
	}
	wanted := strings.ToLower(name)
	for _, t := range m.tombstones { // in the order they were evicted: the first that matches answers
		if strings.ToLower(t.Handle) == wanted || strings.ToLower(t.Alias) == wanted || t.ID == name {
			c := *t
			return nil, &c
		}
	}
	return nil, nil
}

// dropTombstone forgets an evicted agent, by handle, freeing the name. upstream: dropTombstone.
func (m *manager) dropTombstone(handle string) {
	m.mu.Lock()
	m.dropTombstoneLocked(handle)
	m.mu.Unlock()
}

// listTombstones: the evicted agents whose conversation can still be reopened, newest first (equal ages in the order
// they were evicted).
func (m *manager) listTombstones() []tombstone {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tombstone, 0, len(m.tombstones))
	for _, t := range m.tombstones {
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CompletedAt.After(out[j].CompletedAt) })
	return out
}

// list is the records in the order they were spawned.
func (m *manager) list() []*agentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*agentRecord, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.records[id])
	}
	return out
}

// poolFor is the pool a spawn is charged to: none for a nested child or a detached spawn with no blocking caller,
// and none for a blocking one while the foreground limit is unset. The caller holds m.mu.
func (m *manager) poolFor(r *agentRecord) pool {
	if occupiesPoolSlot(r) {
		return poolBackground
	}
	if m.maxConcurrentForeground > 0 && occupiesForegroundSlot(r) {
		return poolForeground
	}
	return poolNone
}

func (m *manager) poolHasRoom(p pool) bool {
	if p == poolBackground {
		return m.runningBackground < m.maxConcurrent
	}
	return m.maxConcurrentForeground == 0 || m.runningForeground < m.maxConcurrentForeground
}

// spawn is spawnWith for a background or foreground agent named by `name`.
func (m *manager) spawn(typ, description, name string, spec childSpec, background bool) *agentRecord {
	return m.spawnWith(typ, description, spec, spawnOptions{Name: name, Background: background})
}

// spawnWith creates an agent: it starts at once, or waits in the queue when its pool is full. A foreground agent is
// not limited unless maxConcurrentForeground is set. upstream: agent-manager.ts spawn.
func (m *manager) spawnWith(typ, description string, spec childSpec, o spawnOptions) *agentRecord {
	if o.ResumeFile != "" {
		spec.ResumeFile = o.ResumeFile
	}
	r := &agentRecord{ID: newAgentID(), Type: typ, Description: description, Name: o.Name, Background: o.Background, Blocking: o.Blocking,
		ParentAgentID: o.ParentAgentID, WorkflowID: o.WorkflowID, Depth: max(1, o.Depth), MaxSubagentDepth: o.MaxSubagentDepth, spec: spec, onSpawned: o.OnSpawned,
		done: make(chan struct{}), started: make(chan struct{}), startGate: make(chan struct{}), StartedAt: m.now()}
	if spec.SessionName != "" { // upstream: session.setSessionName(`${name}#${agentId.slice(0, 8)}`)
		r.spec.SessionName = spec.SessionName + "#" + r.ID[:min(8, len(r.ID))]
	}
	r.Status = statusRunning
	if o.Background {
		r.Status = statusQueued
	}
	m.mu.Lock()
	m.records[r.ID] = r
	m.order = append(m.order, r.ID)
	// An owned child is filtered out of every top-level surface, so it has no handle: nothing can address it and it
	// must not take a name a top-level sibling could have.
	if r.topLevel() {
		if o.Reclaim != nil && o.Reclaim.Handle != "" {
			r.Handle, r.Alias = o.Reclaim.Handle, o.Reclaim.Alias // used as it is: it belongs to the conversation being reopened
		} else {
			r.Handle = m.assignHandle(handleBase(typ))
		}
		if r.Alias == "" && o.Name != "" {
			r.Alias = m.assignHandle(handleBase(o.Name))
		}
	}
	p := m.poolFor(r)
	if p != poolNone && !o.BypassQueue && !m.poolHasRoom(p) {
		r.Status = statusQueued
		if signalDone(o.Signal) {
			// Never enqueue a spawn whose signal is already aborted: nothing would ever release it.
			r.stopped, r.Status, r.CompletedAt, r.settled = true, statusStopped, m.now(), true
			r.closeGate()
			r.closeDone()
			m.mu.Unlock()
			return r
		}
		m.queue = append(m.queue, queueEntry{r: r, pool: p})
		ahead := 0
		for _, q := range m.queue {
			if q.pool == p {
				ahead++
			}
		}
		m.mu.Unlock()
		m.watchSignal(r, o.Signal)
		if o.OnQueued != nil {
			o.OnQueued(r.ID, ahead-1)
		}
		return r
	}
	m.claimLocked(r)
	m.mu.Unlock()
	if signalDone(o.Signal) {
		// An already-aborted signal never fires a listener: stop the agent now, so it stays within the reach of Esc.
		m.abort(r)
	} else {
		m.watchSignal(r, o.Signal)
	}
	m.launch(r)
	r.fireSpawned()
	return r
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

// settlePanicked settles an agent whose run panicked, releasing what the run held and starting what that frees.
func (m *manager) settlePanicked(r *agentRecord) {
	m.mu.Lock()
	if r.settled { // the run had settled: the panic was in what announces it
		m.mu.Unlock()
		return
	}
	r.Status, r.Error, r.CompletedAt, r.resume, r.settled = statusError, "internal error in the agent run", m.now(), nil, true
	m.releasePoolLocked(r)
	c, cancel := r.child, r.cancel
	r.child, r.cancel = nil, nil
	r.closeDone()
	after := m.drainLocked()
	m.mu.Unlock()
	if c != nil {
		c.close()
	}
	if cancel != nil {
		cancel()
	}
	runAll(after)
}

// releasePoolLocked gives back the slot of the pool the record was charged to when its run started. The caller holds
// m.mu. A released record has no pool: the release happens once, here and nowhere else.
func (m *manager) releasePoolLocked(r *agentRecord) {
	switch r.pool {
	case poolBackground:
		m.runningBackground--
	case poolForeground:
		m.runningForeground--
	}
	r.pool = poolNone
}

func signalDone(sig <-chan struct{}) bool {
	select {
	case <-sig:
		return sig != nil
	default:
		return false
	}
}

// watchSignal stops the agent when the caller's signal ends, whether it is queued or running.
func (m *manager) watchSignal(r *agentRecord, sig <-chan struct{}) {
	if sig == nil {
		return
	}
	go func() {
		select {
		case <-sig:
			m.abort(r)
		case <-r.done:
		}
	}()
}

// claimLocked takes the running state, and with it the slot of the pool the record is charged to. The pool is
// resolved once, here, and kept on the record for the release: the limit can change while the agent runs, so
// recomputing it at settle time would free a slot this run never took or keep one it did. The caller holds m.mu.
func (m *manager) claimLocked(r *agentRecord) {
	r.pool = m.poolFor(r)
	r.Status = statusRunning
	r.settled = false
	r.StartedAt = m.now()
	switch r.pool {
	case poolBackground:
		m.runningBackground++
	case poolForeground:
		m.runningForeground++
	}
	r.closeGate()
}

// fireSpawned tells the caller its run was kicked off. It reads the hook off the record, so a spawn that started from
// the queue still reaches the caller that queued it.
func (r *agentRecord) fireSpawned() {
	if fn := r.onSpawned; fn != nil {
		fn(r.ID)
	}
}

// drainLocked starts queued agents up to each pool's limit. It takes the earliest entry whose OWN pool has room, not
// the head: with one queue serving two limits, a saturated pool at the head would otherwise stall every agent of the
// other behind it. It returns what is to run once the lock is released. The caller holds m.mu.
func (m *manager) drainLocked() []func() {
	var after []func()
	for {
		i := -1
		for j, q := range m.queue {
			if m.poolHasRoom(q.pool) {
				i = j
				break
			}
		}
		if i == -1 {
			return after
		}
		next := m.queue[i].r
		m.queue = append(m.queue[:i], m.queue[i+1:]...)
		if next.Status != statusQueued { // stopped while queued: not started, but still released
			next.closeGate()
			continue
		}
		m.claimLocked(next)
		after = append(after, func() { m.launch(next); next.fireSpawned() })
	}
}

// run executes one agent to completion, then releases its slot and starts the next queued agent. The child is bound to
// a context that lives until the child is closed: a child that keeps its conversation in memory outlives the run, so
// that resume can continue it (the original keeps the AgentSession on the record until cleanup).
func (m *manager) run(r *agentRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	old, oldCancel := r.child, r.cancel // the child of an earlier run, which a resume may continue
	r.cancel = cancel                   // abort reads it under the lock, while the child starts
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
	var steers []string
	if err == nil {
		r.child = c
		if f := c.sessionFile(); f != "" {
			r.SessionFile = f
		}
		steers = r.pendingSteers
		r.pendingSteers = nil
	}
	stop := r.stopped
	done := r.done // this run's: a resume that lands once the record has settled replaces it
	r.closeStarted()
	m.mu.Unlock()
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
		for _, msg := range steers { // the steers that arrived before the child existed, in order
			_ = c.steer(msg)
		}
		if stop {
			c.abort()
		}
		res, err = c.wait()
	}
	alive := c != nil && err == nil && c.alive() // an in-memory conversation outlives its run
	m.mu.Lock()
	if r.CompletedAt.IsZero() {
		r.CompletedAt = m.now()
	}
	switch {
	case r.stopped: // abort already marked it stopped; the run settles on its own
		r.Status = statusStopped
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
	// A foreground agent's result is delivered inline, so nothing is left to announce.
	if !r.Background {
		r.Consumed = true
	}
	r.resume, r.settled = nil, true
	m.releasePoolLocked(r) // released here and nowhere else: abort only fires the child's stop, so freeing in both would double-free
	// The process ends with its run unless the agent still holds a conversation in it (and the session is going on).
	var closing child
	var closingCancel context.CancelFunc
	if c != nil && (!alive || m.disposed) {
		closingCancel, r.cancel = r.cancel, nil
		if alive {
			closing = c
		}
	}
	m.mu.Unlock()
	if closing != nil {
		closing.close()
	}
	if closingCancel != nil {
		closingCancel()
	}
	m.abortOwnedChildren(r.ID)
	if m.onSettle != nil && (rr == nil || rr.background) { // a foreground resume is not announced
		m.onSettle(r)
	}
	m.mu.Lock()
	after := m.drainLocked()
	closeOnce(done)
	m.mu.Unlock()
	runAll(after)
}

// abortOwnedChildren stops the nested children a settled parent owns: hidden from the UI, only their owner can read
// them, so a child outliving its parent would spend tokens unseen. Grandchildren follow through their own settle.
func (m *manager) abortOwnedChildren(parentID string) {
	for _, r := range m.list() {
		if r.ParentAgentID == parentID {
			m.abort(r)
		}
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

// spawnAndWait spawns a blocking agent and waits until it is final. It is charged to the foreground pool, never the
// background one. upstream: agent-manager.ts spawnAndWait.
func (m *manager) spawnAndWait(typ, description string, spec childSpec, o spawnOptions) *agentRecord {
	o.Background, o.Blocking = false, true
	r := m.spawnWith(typ, description, spec, o)
	m.wait(r)
	return r
}

// wait blocks until the record is final. A caller whose agent is aborted while queued gets the stopped record back, never
// an error: a failing tool call would reject pi's whole batch of tool calls.
func (m *manager) wait(r *agentRecord) {
	m.mu.Lock()
	queued, gate := r.Status == statusQueued, r.startGate
	m.mu.Unlock()
	if queued {
		<-gate
	}
	m.mu.Lock()
	disposed, done := r.Status == statusQueued, r.done // left the queue without starting: the manager was disposed
	m.mu.Unlock()
	if !disposed {
		<-done
	}
}

// abort stops a running or queued agent; it reports false for one that is not. A running one is marked stopped at
// once and settles when its child ends; a child that keeps its conversation in memory is only told to stop its run:
// the conversation stays resumable, as `session.abort()` leaves it in the original (agent-manager.ts abort).
func (m *manager) abort(r *agentRecord) bool {
	m.mu.Lock()
	switch r.Status {
	case statusQueued:
		// No slot was taken, so none is freed, and no completion is announced: the blocking caller learns of the
		// stop from its own tool result.
		m.dequeueLocked(func(q queueEntry) bool { return q.r == r })
		r.stopped, r.Status, r.CompletedAt, r.resume, r.settled = true, statusStopped, m.now(), nil, true
		r.closeDone() // under the lock: a resume that lands once the record is stopped replaces the channels
		m.mu.Unlock()
		return true
	case statusRunning:
		r.stopped, r.Status, r.CompletedAt = true, statusStopped, m.now()
		c, cancel := r.child, r.cancel
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

// dequeueLocked removes queued entries and wakes whoever waits on them: leaving the queue always releases the
// waiter, a missed release being an unbounded hang. The caller holds m.mu.
func (m *manager) dequeueLocked(pred func(queueEntry) bool) {
	kept := m.queue[:0]
	for _, q := range m.queue {
		if pred(q) {
			q.r.closeGate()
		} else {
			kept = append(kept, q)
		}
	}
	m.queue = kept
}

// steer sends a message into a running agent; one whose child does not exist yet gets it queued, delivered in order
// once the child starts. upstream: the steer_subagent tool, agent-manager.ts steer.
func (m *manager) steer(r *agentRecord, message string) (queued bool, err error) {
	m.mu.Lock()
	status, c := r.Status, r.child
	if status != statusRunning {
		m.mu.Unlock()
		return false, fmt.Errorf("Agent %s is not running (status: %s).", r.ID, status)
	}
	if c == nil {
		r.pendingSteers = append(r.pendingSteers, message)
		m.mu.Unlock()
		return true, nil
	}
	m.mu.Unlock()
	return false, c.steer(message)
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

// cleanup evicts the records that settled before the retention window, which also ends the processes that kept their
// conversations. A running or queued record is never evicted, however old its timestamp. upstream: agent-manager.ts
// cleanup (run every minute).
func (m *manager) cleanup() { m.cleanupAt(m.now()) }

// cleanupAt is cleanup for the clock reading `now`.
func (m *manager) cleanupAt(now time.Time) {
	cutoff := now.Add(-recordRetention)
	m.mu.Lock()
	var gone []string
	for _, id := range m.order {
		r := m.records[id]
		if r.Status == statusRunning || r.Status == statusQueued {
			continue
		}
		if !r.CompletedAt.IsZero() && !r.CompletedAt.Before(cutoff) {
			continue
		}
		gone = append(gone, id)
	}
	m.mu.Unlock()
	for _, id := range gone {
		m.removeRecord(id)
	}
}

// clearCompleted removes the settled records now; it runs on session boundaries so a prior session's records do not
// persist. skipUnconsumed keeps the ones the model has not read yet (the GC evicts them later), and the tombstones
// always go: a new session means new handles. upstream: agent-manager.ts clearCompleted.
func (m *manager) clearCompleted(skipUnconsumed bool) {
	m.mu.Lock()
	var gone []string
	for _, id := range m.order {
		r := m.records[id]
		if r.Status == statusRunning || r.Status == statusQueued {
			continue
		}
		if skipUnconsumed && !r.Consumed {
			continue
		}
		gone = append(gone, id)
	}
	m.mu.Unlock()
	for _, id := range gone {
		m.removeRecord(id)
	}
	m.mu.Lock()
	m.tombstones = nil
	m.mu.Unlock()
}

// removeRecord forgets an agent: its record, and the process that holds its conversation. What is left of it is a
// tombstone when it has a handle and a session file. The caller holds no lock. upstream: agent-manager.ts removeRecord.
func (m *manager) removeRecord(id string) {
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		m.mu.Unlock()
		return
	}
	m.tombstoneLocked(r)
	delete(m.records, id)
	for i, o := range m.order {
		if o == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	c, cancel := r.child, r.cancel
	r.child, r.cancel = nil, nil
	m.mu.Unlock()
	if c != nil {
		c.close()
	}
	if cancel != nil {
		cancel()
	}
}

// tombstoneLocked keeps enough of a departing record for `@handle` to reopen its conversation: it needs a handle to
// be addressed by and a session file to reopen (an in-memory session leaves no transcript). A handle that has a
// tombstone keeps its place in the order (a JS Map.set). The oldest go first past the bound; of equal ages the
// earliest kept. The caller holds m.mu.
func (m *manager) tombstoneLocked(r *agentRecord) {
	if r.Handle == "" || r.SessionFile == "" {
		return
	}
	completed := r.CompletedAt
	if completed.IsZero() {
		completed = m.now()
	}
	t := &tombstone{Handle: r.Handle, Alias: r.Alias, ID: r.ID, Type: r.Type, Description: r.Description, SessionFile: r.SessionFile, CompletedAt: completed}
	if i := m.tombstoneIndex(r.Handle); i >= 0 {
		m.tombstones[i] = t
	} else {
		m.tombstones = append(m.tombstones, t)
	}
	for len(m.tombstones) > maxTombstones {
		oldest := 0
		for i, o := range m.tombstones {
			if o.CompletedAt.Before(m.tombstones[oldest].CompletedAt) {
				oldest = i
			}
		}
		m.tombstones = slices.Delete(m.tombstones, oldest, oldest+1)
	}
}

func (m *manager) tombstoneIndex(handle string) int {
	return slices.IndexFunc(m.tombstones, func(t *tombstone) bool { return t.Handle == handle })
}

func (m *manager) dropTombstoneLocked(handle string) {
	if i := m.tombstoneIndex(handle); i >= 0 {
		m.tombstones = slices.Delete(m.tombstones, i, i+1)
	}
}

// hasRunning: whether any agent is running or queued.
func (m *manager) hasRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if r.Status == statusRunning || r.Status == statusQueued {
			return true
		}
	}
	return false
}

// abortAll stops every agent, queued first, and returns how many it stopped.
func (m *manager) abortAll() int {
	n := 0
	for _, r := range m.list() {
		if m.abort(r) {
			n++
		}
	}
	return n
}

// waitForAll waits for every running and queued agent, including the ones that start as others finish.
func (m *manager) waitForAll() {
	for {
		var pending []*agentRecord
		for _, r := range m.list() {
			m.mu.Lock()
			active := r.Status == statusRunning || r.Status == statusQueued
			m.mu.Unlock()
			if active {
				pending = append(pending, r)
			}
		}
		if len(pending) == 0 {
			return
		}
		for _, r := range pending {
			<-m.doneOf(r)
		}
	}
}

// open starts a session on the manager: a child that settles from now on is kept again, if it holds a conversation.
func (m *manager) open() {
	m.mu.Lock()
	m.disposed = false
	m.mu.Unlock()
}

// dispose ends the session: every agent is stopped, every process that holds a conversation is closed, everyone waiting
// in the queue is released (a blocked caller must not hang), the GC stops and the records are forgotten. upstream:
// agent-manager.ts abortAll, dispose (index.ts session_shutdown).
func (m *manager) dispose() {
	m.stopGCLoop()
	m.mu.Lock()
	m.disposed = true // a run that settles after this closes its child
	m.mu.Unlock()
	m.abortAll()
	type held struct {
		c      child
		cancel context.CancelFunc
	}
	var all []held
	m.mu.Lock()
	m.dequeueLocked(func(queueEntry) bool { return true })
	for _, r := range m.records {
		if r.child != nil || r.cancel != nil {
			all = append(all, held{r.child, r.cancel})
			r.child, r.cancel = nil, nil
		}
	}
	m.records, m.order, m.queue = map[string]*agentRecord{}, nil, nil
	m.mu.Unlock()
	for _, h := range all {
		if h.c != nil {
			h.c.close()
		}
		if h.cancel != nil {
			h.cancel()
		}
	}
}

// isConsumed: whether the result was handed to the model.
func (m *manager) isConsumed(r *agentRecord) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.Consumed
}
