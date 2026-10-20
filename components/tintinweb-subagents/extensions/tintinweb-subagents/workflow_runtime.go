package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// The host half of a workflow run: the worker's lifecycle, the message bridge, the concurrency semaphore, the per-run
// caps and the progress log. The script's only route to an agent is a `call` message landing here, which is what
// makes the caps and the abort story enforceable: a script cannot go around them because it has nothing to go around
// them with. Spawning is injected (workflowHost), so the runtime knows nothing about the extension's manager.
//
// The original's host half is single-threaded JavaScript: handlers run to their first `await` and are interleaved only
// there. This port keeps that discipline with a baton (runState.mu): a handler runs as a goroutine that holds it
// and gives it up only inside await (coroutine.await), so the state needs no other locking and call order is the
// order the script made the calls in. upstream: src/workflow/runtime.ts.

const (
	workflowAgentCap      = 1000 // agents one run may schedule, in total. upstream: WORKFLOW_AGENT_CAP
	workflowItemCap       = 4096 // items one parallel() or pipeline() call may take. upstream: WORKFLOW_ITEM_CAP
	workflowNestedCap     = 256  // nested workflow() invocations allowed per run. upstream: WORKFLOW_NESTED_CAP
	workflowPreviewLength = 200  // how much of a prompt or result is kept for the UI
)

// workflowRuntimeError is a run that could not start: a script over the limits, args that cannot cross.
type workflowRuntimeError struct{ msg string }

func (e *workflowRuntimeError) Error() string { return e.msg }

// wfCPUCount is os.cpus().length: every logical CPU the machine has, which on Linux is /proc/stat's cpuN lines, not
// the affinity mask runtime.NumCPU honours (a pinned container would otherwise read as a small machine).
func wfCPUCount() int {
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		n := 0
		for _, line := range strings.Split(string(b), "\n") {
			if rest, ok := strings.CutPrefix(line, "cpu"); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
				n++
			}
		}
		if n > 0 {
			return n
		}
	}
	return runtime.NumCPU()
}

// workflowConcurrency is the concurrent agents allowed, leaving two cores for the host and the TUI. The max(1, …) is
// not decoration: min(16, cpus - 2) is 0 on a one- or two-core machine, and a semaphore with zero permits hangs
// before the first agent. upstream: workflowConcurrency.
func workflowConcurrency(cpuCount int) int { return max(1, min(16, cpuCount-2)) }

// workflowResolvedInfo is the child's effective configuration, reported once its session exists (#168, #182).
type workflowResolvedInfo struct {
	RecordID          string // the host's own id for the child; reported as soon as there is one
	ModelName         string
	ModelID           string
	Thinking          string
	RequestedThinking string
	RequestedModel    string
}

// workflowSpawnRequest is one agent the script asked for. upstream: WorkflowSpawnRequest.
type workflowSpawnRequest struct {
	// Ctx is done when the run settles: the child must stop.
	Ctx context.Context
	// AgentID is the handle for workflowHost.AbortAgent.
	AgentID string
	// Index is the position in the run and the progress entry's stable identity.
	Index  int
	Prompt string
	Label  string
	// AgentType is never empty.
	AgentType string
	Model     string // "" when the script named none
	// Effort is a thinking level, already checked by the worker; "" leaves it to the agent definition.
	Effort    string
	Isolation string // "" or "worktree"
	// OnResolved is called by the host once the child's effective configuration is known; it is never nil.
	OnResolved func(workflowResolvedInfo)
	// Schema is compiled from the script's agent({ schema }): the host gives the child a StructuredOutput tool built
	// from it and returns the validated payload as JSON text.
	Schema     *workflowCompiledSchema
	PhaseIndex *int
	PhaseTitle string
	// Gate is the command this agent runs under; a host that can run it inside the child's worktree does and hands
	// back workflowSpawnResult.Gate.
	Gate string
}

// workflowSpawnResult is what the host reports for an agent. upstream: WorkflowSpawnResult.
type workflowSpawnResult struct {
	OK bool
	// Text is the agent's answer, present when OK.
	Text string
	// Error is why it failed, present when not OK.
	Error string
	// Skipped: the user dismissed it rather than it failing; renders as skipped.
	Skipped bool
	Tokens  *int
	// OutputTokens are output tokens only, for the script's budget.spent().
	OutputTokens      int
	StructuredRetried bool
	ToolCalls         *int
	// Cwd is where the child actually ran (a worktree); only meaningful for isolation.
	Cwd string
	// Gate is the outcome of this agent's gate when the host already ran it.
	Gate *workflowGateResult
}

// workflowGateResult is the outcome of a gate command; Output is what the user is shown when it fails.
type workflowGateResult struct {
	OK     bool
	Output string
}

// workflowScriptSource is a nested workflow() reference resolved to source.
type workflowScriptSource struct {
	OK      bool
	Script  string
	Path    string
	Message string
}

// workflowHost is the one seam between a workflow and the rest of the extension. A host may also implement
// workflowResumer, workflowGateRunner and workflowLoader: a capability it lacks fails a script that asks for it
// rather than quietly doing something else.
type workflowHost interface {
	// SpawnAgent runs one child to completion. An error is that agent's failure, not the run's.
	SpawnAgent(req *workflowSpawnRequest) (workflowSpawnResult, error)
	// AbortAgent is called for every in-flight agent when the run aborts.
	AbortAgent(agentID string)
}

// workflowResumer continues a child that already ran in this run, keeping its context. The child keeps the agent
// type, model and tool contract it started with, so only the follow-up prompt crosses.
type workflowResumer interface {
	ResumeAgent(ctx context.Context, agentID, prompt string, onResolved func(workflowResolvedInfo)) (workflowSpawnResult, error)
}

// workflowGateRunner runs a gate command and reports whether it passed. cwd is the child's worktree when it had one.
type workflowGateRunner interface {
	RunGate(ctx context.Context, command, agentID, cwd string) (workflowGateResult, error)
}

// workflowLoader resolves a nested workflow() reference to source.
type workflowLoader interface {
	LoadWorkflow(ref workflowRef) (workflowScriptSource, error)
}

// workflowControl is what a run can be told to do while it is going, from the workflows dialog. Every method is
// best-effort and idempotent: false means there was nothing to do that to. upstream: WorkflowControl.
type workflowControl interface {
	// Pause stops starting agents; ones already running are left to finish.
	Pause()
	Resume()
	IsPaused() bool
	// Skip gives up on the agent at index: its agent() call returns null and the row renders skipped.
	Skip(index int) bool
	// Retry starts the agent at index over, only while it is running.
	Retry(index int) bool
}

type workflowJournalOptions struct {
	// Entries are a previous run's settled calls, in position order. Empty replays nothing.
	Entries []workflowJournalEntry
	// Append is called as each call of this run settles, so it can be resumed in turn.
	Append func(workflowJournalEntry)
}

// workflowRunOptions are the options of runWorkflow. upstream: RunWorkflowOptions.
type workflowRunOptions struct {
	// Script is the full source, starting with `export const meta = { … }`.
	Script  string
	Args    any
	HasArgs bool
	Host    workflowHost
	// Ctx aborts the run: its children are aborted and the worker is terminated.
	Ctx context.Context
	// OnProgress is fired per batch, not per entry; it runs with the run's state locked and must not call the control.
	OnProgress func([]workflowEntry)
	// Concurrency, AgentCap, ItemCap and NestedCap are the run's limits; zero means the default.
	Concurrency int
	AgentCap    int
	ItemCap     int
	NestedCap   int
	// OnControl hands the caller the run's control surface, once per run, before the first agent starts.
	OnControl func(workflowControl)
	Journal   *workflowJournalOptions
}

// workflowRunResult is the outcome of a run. upstream: WorkflowRunResult.
type workflowRunResult struct {
	Status string // completed, failed or killed
	Meta   workflowMeta
	// Value is the script's return value decoded from ValueJSON (nil for undefined and for null); ValueJSON is
	// the JSON text the script produced, nil for undefined.
	Value     any
	ValueJSON json.RawMessage
	Error     string
	HasError  bool
	// Progress is the append-only log, in emission order.
	Progress []workflowEntry
	// AgentCount is the agents scheduled, including those that failed.
	AgentCount int
	// ReplayedCount is how many of those came back from the journal instead of being spawned.
	ReplayedCount int
}

/* ------------------------------------------------------------------------- *
 * JSON boundary — host side
 * ------------------------------------------------------------------------- */

func workflowBoundaryError(what, path string) error {
	return &workflowRuntimeError{fmt.Sprintf("Cannot pass %s across the workflow VM boundary (at %s).", what, path)}
}

func walkWorkflowBoundary(v reflect.Value, path string, seen map[uintptr]bool) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return walkWorkflowBoundary(v.Elem(), path, seen)
	}
	switch v.Kind() {
	case reflect.String, reflect.Bool:
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
			return workflowBoundaryError("a non-finite number", path)
		}
		return nil
	case reflect.Func:
		return workflowBoundaryError("a function", path)
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return workflowBoundaryError("a non-plain object", path)
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		if p := v.Pointer(); seen[p] {
			return workflowBoundaryError("a circular structure", path)
		} else {
			seen[p] = true
			defer delete(seen, p)
		}
		for i := 0; i < v.Len(); i++ {
			if err := walkWorkflowBoundary(v.Index(i), fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return workflowBoundaryError("a non-plain object", path)
		}
		if v.IsNil() {
			return nil
		}
		if p := v.Pointer(); seen[p] {
			return workflowBoundaryError("a circular structure", path)
		} else {
			seen[p] = true
			defer delete(seen, p)
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			if err := walkWorkflowBoundary(v.MapIndex(k), path+"."+k.String(), seen); err != nil {
				return err
			}
		}
		return nil
	}
	return workflowBoundaryError("a non-plain object", path)
}

// assertBoundarySafe rejects anything that cannot survive the round trip to the worker and into a resume journal:
// the check is stricter than JSON (a Date, a struct, a cycle, NaN). upstream: assertBoundarySafe.
func assertBoundarySafe(value any, path string) error {
	return walkWorkflowBoundary(reflect.ValueOf(value), path, map[uintptr]bool{})
}

/* ------------------------------------------------------------------------- *
 * The run
 * ------------------------------------------------------------------------- */

// Everything below 0x20 except tab, newline and carriage return, plus DEL.
var workflowControlCharacters = regexp.MustCompile("[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]")

func wfPreviewText(text string) string {
	if wfUTF16Len(text) <= workflowPreviewLength {
		return text
	}
	return wfUTF16Prefix(text, workflowPreviewLength-1) + "…"
}

// wfDerivedLabel is the first line of the prompt, trimmed: the fallback display name for an agent.
func wfDerivedLabel(prompt string) string {
	line := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if wfUTF16Len(line) <= 60 {
		if line == "" {
			return "agent"
		}
		return line
	}
	return wfUTF16Prefix(line, 59) + "…"
}

// validateScript is everything a script must satisfy before it is compiled, so a nested workflow() is held to the
// same standard as a top-level run. upstream: validateScript.
func validateScript(script string) (workflowMeta, string, error) {
	if n := wfUTF16Len(script); n > maxWorkflowScriptLength {
		return workflowMeta{}, "", &workflowRuntimeError{fmt.Sprintf("Workflow script is %d characters, over the limit of %d.", n, maxWorkflowScriptLength)}
	}
	if workflowControlCharacters.MatchString(script) {
		return workflowMeta{}, "", &workflowRuntimeError{"Workflow script contains control characters. Only tab, carriage return and newline are allowed."}
	}
	return extractMeta(script)
}

// workflowCompletedChild is a child resume can revive, remembered under its label with the spawn options it started with.
type workflowCompletedChild struct {
	agentID, label, agentType string
	model                     *string
	isolation                 *string
}

// workflowAgentCall is the payload of an `agent` call message.
type workflowAgentCall struct {
	prompt                                                   string
	label, model, agentType, isolation, gate, resume, effort *string
	phaseIndex                                               *int
	phaseTitle                                               *string
	schemaJSON                                               *string
}

func wfOptString(m map[string]any, key string) *string {
	if s, ok := m[key].(string); ok {
		return &s
	}
	return nil
}

func parseWorkflowAgentCall(m map[string]any) workflowAgentCall {
	c := workflowAgentCall{label: wfOptString(m, "label"), model: wfOptString(m, "model"), agentType: wfOptString(m, "agentType"),
		isolation: wfOptString(m, "isolation"), gate: wfOptString(m, "gate"), resume: wfOptString(m, "resume"),
		effort: wfOptString(m, "effort"), phaseTitle: wfOptString(m, "phaseTitle"), schemaJSON: wfOptString(m, "schemaJson")}
	c.prompt, _ = m["prompt"].(string)
	if n, ok := wfToInt(m["phaseIndex"]); ok {
		c.phaseIndex = &n
	}
	return c
}

func (c workflowAgentCall) keyInput() workflowJournalKeyInput {
	return workflowJournalKeyInput{Prompt: c.prompt, Label: c.label, Model: c.model, AgentType: c.agentType, Effort: c.effort,
		Isolation: c.isolation, Gate: c.gate, Resume: c.resume, Schema: c.schemaJSON}
}

// workflowLiveAgent is an agent that still has an unanswered agent() call, by index: the window in which skip and retry mean anything.
type workflowLiveAgent struct {
	agentID string
	started bool
	intent  string // "", "skip" or "retry"
	// wake releases it from a pause hold, so a skip does not wait for a resume.
	wake func()
}

type wfSemaphore struct {
	active, limit int
	waiters       []chan struct{}
}

type workflowInboxItem struct {
	msg    *workerMessage
	exited bool // the worker goroutine ended without reporting
}

type workflowRunState struct {
	// mu is the baton: whoever holds it is "the host's thread".
	mu       sync.Mutex
	opts     workflowRunOptions
	host     workflowHost
	meta     workflowMeta
	agentCap int

	runCtx    context.Context
	runCancel context.CancelFunc
	worker    *workflowWorker
	sem       wfSemaphore
	result    chan workflowRunResult

	progress []workflowEntry
	inflight map[string]bool
	// completedByLabel: label → the child that ran under it, last one wins (the resume handle), in first-seen order.
	completedByLabel map[string]*workflowCompletedChild
	labelOrder       []string
	// openLaunches are launches the host has accepted and not yet answered, in call order: anything left when
	// `complete` arrives is a result nobody is waiting for.
	openLaunches []int
	openLabels   map[int]string

	agentCount, replayedCount int
	aborted, settled          bool

	journalEntries []workflowJournalEntry
	prefixIntact   bool

	liveAgents   map[int]*workflowLiveAgent
	spentOutput  int
	paused       bool
	pauseWaiters map[*func()]bool
	inbox        []workflowInboxItem
	inboxCond    *sync.Cond
	inboxMu      sync.Mutex
	inboxClosed  bool
	stopWatcher  chan struct{}
}

// wfCoroutine is a handler that runs as a goroutine holding the baton and gives it up only inside await.
type wfCoroutine struct {
	rt      *workflowRunState
	yielded chan struct{}
	once    sync.Once
}

func (c *wfCoroutine) yield() { c.once.Do(func() { close(c.yielded) }) }

// await runs fn (a blocking wait) without the baton, and takes it again after.
func (c *wfCoroutine) await(fn func()) {
	c.yield()
	c.rt.mu.Unlock()
	fn()
	c.rt.mu.Lock()
}

// call is await for a call into the host (which is foreign code): a panic in it is that call's error, and the baton
// is taken again before anything else looks at the state.
func (c *wfCoroutine) call(fn func() error) (err error) {
	c.await(func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		err = fn()
	})
	return err
}

// spawn starts fn as a coroutine and returns once it reaches its first await or ends, so the part of a handler before
// its first await runs in message order. The caller holds the baton; the coroutine gives it back.
func (rt *workflowRunState) spawn(fn func(c *wfCoroutine)) {
	c := &wfCoroutine{rt: rt, yielded: make(chan struct{})}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				rt.finish(workflowRunResult{Status: runFailed, Error: fmt.Sprint(r), HasError: true})
			}
			c.yield()
			rt.mu.Unlock()
		}()
		fn(c)
	}()
	<-c.yielded
}

func (s *wfSemaphore) acquire(c *wfCoroutine) {
	if s.active < s.limit {
		s.active++
		return
	}
	ch := make(chan struct{})
	s.waiters = append(s.waiters, ch)
	c.await(func() { <-ch })
}

func (s *wfSemaphore) release() {
	if len(s.waiters) > 0 {
		next := s.waiters[0]
		s.waiters = s.waiters[1:]
		// The permit is handed straight over rather than decremented and re-acquired; otherwise a burst of releases
		// can let more than limit through.
		close(next)
		return
	}
	s.active--
}

// drain wakes everyone so aborted callers can observe the abort and bail.
func (s *wfSemaphore) drain() {
	for _, ch := range s.waiters {
		close(ch)
	}
	s.waiters = nil
}

func (rt *workflowRunState) emit(entries ...workflowEntry) {
	if len(entries) == 0 {
		return
	}
	rt.progress = append(rt.progress, entries...)
	if rt.opts.OnProgress != nil {
		rt.opts.OnProgress(entries)
	}
}

func (rt *workflowRunState) respond(callID int, ok bool, value any, errText string, fatal bool) {
	// Cleared before the settled check: a launch answered by a run that is already finishing is not an unawaited one.
	rt.closeLaunch(callID)
	if rt.settled {
		return
	}
	// spent rides on every response, so the worker's budget.spent() mirrors this number rather than a second tally.
	rt.worker.sendResponse(workerResponse{CallID: callID, OK: ok, Value: value, Error: errText, Fatal: fatal, Spent: rt.spentOutput})
}

func (rt *workflowRunState) closeLaunch(callID int) {
	if _, ok := rt.openLabels[callID]; !ok {
		return
	}
	delete(rt.openLabels, callID)
	for i, id := range rt.openLaunches {
		if id == callID {
			rt.openLaunches = append(rt.openLaunches[:i], rt.openLaunches[i+1:]...)
			break
		}
	}
}

func (rt *workflowRunState) releasePause() {
	for wake := range rt.pauseWaiters {
		(*wake)()
	}
	rt.pauseWaiters = map[*func()]bool{}
}

// pauseGate parks while the run is paused, so no new agent is started.
func (rt *workflowRunState) pauseGate(c *wfCoroutine, live *workflowLiveAgent) {
	if !rt.paused || rt.aborted || rt.settled {
		return
	}
	ch := make(chan struct{})
	var wake func()
	wake = func() {
		if rt.pauseWaiters[&wake] {
			delete(rt.pauseWaiters, &wake)
			live.wake = nil
			close(ch)
		}
	}
	live.wake = wake
	rt.pauseWaiters[&wake] = true
	c.await(func() { <-ch })
}

// finish settles the run once; the caller holds the baton. upstream: finish.
func (rt *workflowRunState) finish(result workflowRunResult) {
	if rt.settled {
		return
	}
	rt.settled = true
	close(rt.stopWatcher)
	// Symmetric with sem.drain(): everything parked is woken so it observes the settle and unwinds.
	rt.releasePause()
	ids := make([]string, 0, len(rt.inflight))
	for id := range rt.inflight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rt.host.AbortAgent(id)
	}
	rt.inflight = map[string]bool{}
	rt.sem.drain()
	rt.runCancel()
	// Resolved only once the worker is actually down, so a caller that waits for runWorkflow() is guaranteed not to be
	// leaking one. In its own goroutine: the worker may be waiting for the baton this caller holds.
	go func() {
		rt.worker.terminate()
		rt.mu.Lock()
		result.Meta, result.AgentCount, result.ReplayedCount = rt.meta, rt.agentCount, rt.replayedCount
		result.Progress = append([]workflowEntry(nil), rt.progress...)
		rt.mu.Unlock()
		rt.result <- result
	}()
}

func (rt *workflowRunState) onAbort() {
	rt.aborted = true
	// terminate() is why this runs on its own goroutine at all: it stops a script that is spinning or wedged mid-await.
	rt.finish(workflowRunResult{Status: runKilled, Error: "Workflow aborted.", HasError: true})
}

// post queues a worker message (or exit) for the dispatcher; it never blocks.
func (rt *workflowRunState) post(item workflowInboxItem) {
	rt.inboxMu.Lock()
	if !rt.inboxClosed {
		rt.inbox = append(rt.inbox, item)
		rt.inboxCond.Signal()
	}
	rt.inboxMu.Unlock()
}

// dispatch handles the worker's messages one at a time, in the order they were posted.
func (rt *workflowRunState) dispatch() {
	for {
		rt.inboxMu.Lock()
		for len(rt.inbox) == 0 && !rt.inboxClosed {
			rt.inboxCond.Wait()
		}
		if rt.inboxClosed {
			rt.inboxMu.Unlock()
			return
		}
		item := rt.inbox[0]
		rt.inbox = rt.inbox[1:]
		rt.inboxMu.Unlock()
		rt.mu.Lock()
		if rt.settled {
			rt.mu.Unlock()
			continue
		}
		if item.exited {
			// Only reachable when the worker dies without reporting: a promise nothing will ever settle.
			rt.finish(workflowRunResult{Status: runFailed, Error: "Workflow worker exited before completing.", HasError: true})
			rt.mu.Unlock()
			continue
		}
		rt.handleMessage(item.msg) // returns with the baton released
	}
}

// handleMessage runs with the baton held and returns with it released.
func (rt *workflowRunState) handleMessage(m *workerMessage) {
	switch m.Type {
	case "progress":
		rt.emit(m.Entries...)
		rt.mu.Unlock()
	case "call":
		switch m.Method {
		case "workflow":
			ref := workflowRef{}
			if s, ok := m.Payload["name"].(string); ok {
				ref.Name = s
			}
			if s, ok := m.Payload["scriptPath"].(string); ok {
				ref.ScriptPath = s
			}
			rt.spawn(func(c *wfCoroutine) { rt.handleLoadWorkflow(c, m.CallID, ref) })
		case "agent":
			call := parseWorkflowAgentCall(m.Payload)
			rt.spawn(func(c *wfCoroutine) { rt.handleAgent(c, m.CallID, call) })
		default:
			rt.respond(m.CallID, false, nil, fmt.Sprintf(`Unknown workflow host method "%s".`, m.Method), true)
			rt.mu.Unlock()
		}
	case "complete":
		// The script is done, so every launch it made should have been answered by now: a response is sent before the
		// worker can post this, so anything still open was never awaited. finish() aborts those children on the way out.
		if len(rt.openLaunches) > 0 {
			labels := make([]string, len(rt.openLaunches))
			for i, id := range rt.openLaunches {
				labels[i] = "'" + rt.openLabels[id] + "'"
			}
			// Nico's wording, kept verbatim: the one borrowed check whose message a user is likely to search for.
			rt.finish(workflowRunResult{Status: runFailed, HasError: true,
				Error: "workflow script completed with unawaited agent launch(es): " + strings.Join(labels, ", ") + ". Await or return each launch."})
		} else {
			res := workflowRunResult{Status: runCompleted}
			if m.ResultJSON != nil {
				res.ValueJSON = json.RawMessage(*m.ResultJSON)
				_ = json.Unmarshal(res.ValueJSON, &res.Value)
			}
			rt.finish(res)
		}
		rt.mu.Unlock()
	case "error":
		rt.finish(workflowRunResult{Status: runFailed, Error: m.Message, HasError: true})
		rt.mu.Unlock()
	default:
		rt.mu.Unlock()
	}
}

// replayAt is the journal entry to reuse at index, or nil to run it live. Once a position misses (a different key, a
// journaled failure, nothing recorded there) every later call runs live, however well it matches.
func (rt *workflowRunState) replayAt(index int, key string) *workflowJournalEntry {
	if !rt.prefixIntact {
		return nil
	}
	if index >= len(rt.journalEntries) {
		rt.prefixIntact = false
		return nil
	}
	e := &rt.journalEntries[index]
	if e.Index != index || e.Key != key || !e.OK {
		rt.prefixIntact = false
		return nil
	}
	return e
}

func (rt *workflowRunState) recordJournal(e workflowJournalEntry) {
	if rt.opts.Journal != nil && rt.opts.Journal.Append != nil {
		rt.opts.Journal.Append(e)
	}
}

// applyWorkflowSchema holds a schema'd result to its schema, host-side: the one place that can promise the script the shape it
// asked for (a host that ignores schema, a replayed entry from before the schema changed, a torn journal line).
func applyWorkflowSchema(result workflowSpawnResult, compiled *workflowCompiledSchema) workflowSpawnResult {
	var parsed any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		result.OK, result.Error = false, "The agent did not return structured output: its answer was not JSON."
		return result
	}
	if verdict := compiled.Check(parsed); verdict != "" {
		result.OK, result.Error = false, "The agent's answer did not match the requested schema: "+verdict
	}
	return result
}

// applyGate turns a failing gate into a failing agent: no new state, no new entry type. It is the single place that
// decides whether a gate passed; the host may already have run the command (inside a worktree that no longer exists),
// but only ever one of the two runs it.
func (rt *workflowRunState) applyGate(c *wfCoroutine, result workflowSpawnResult, command, agentID string, runGate workflowGateRunner) workflowSpawnResult {
	var outcome workflowGateResult
	if result.Gate != nil {
		outcome = *result.Gate
	} else {
		err := c.call(func() (e error) { outcome, e = runGate.RunGate(rt.runCtx, command, agentID, result.Cwd); return })
		if err != nil {
			panic(err) // the caller turns it into a failed agent
		}
	}
	result.Gate = nil
	if outcome.OK {
		return result
	}
	result.Text = ""
	output := strings.TrimSpace(outcome.Output)
	result.OK = false
	if output == "" {
		result.Error = "Gate command failed: " + command
	} else {
		result.Error = output
	}
	return result
}

func (rt *workflowRunState) handleAgent(c *wfCoroutine, callID int, payload workflowAgentCall) {
	// Checked once, up front: a capability the host lacks fails before an agent is spawned rather than after.
	runGate, _ := rt.host.(workflowGateRunner)
	resumer, _ := rt.host.(workflowResumer)
	if payload.gate != nil && runGate == nil {
		rt.respond(callID, false, nil, "This workflow host cannot run gate commands.", true)
		return
	}
	if payload.resume != nil && resumer == nil {
		rt.respond(callID, false, nil, "This workflow host cannot resume agents.", true)
		return
	}

	var resumed *workflowCompletedChild
	if payload.resume != nil {
		resumed = rt.completedByLabel[*payload.resume]
		if resumed == nil {
			// Fatal: a typo'd label is a script bug. Unless agents were replayed, in which case it is not a script bug
			// at all: the label's child came back from the journal and has no conversation here to continue.
			if rt.replayedCount > 0 {
				rt.respond(callID, false, nil, fmt.Sprintf(`agent() opts.resume: "%s" was replayed from the resume journal, not run, so there is `+
					"no conversation in this run to continue. Re-run without resumeFromRunId.", *payload.resume), true)
			} else {
				known := "No agent has completed yet."
				if len(rt.labelOrder) > 0 {
					quoted := make([]string, len(rt.labelOrder))
					for i, l := range rt.labelOrder {
						quoted[i] = `"` + l + `"`
					}
					known = "Known labels: " + strings.Join(quoted, ", ") + "."
				}
				rt.respond(callID, false, nil, fmt.Sprintf(`agent() opts.resume: no agent has completed under the label "%s" in this run. %s`, *payload.resume, known), true)
			}
			return
		}
	}

	// Compiled before anything is scheduled: a schema the runtime cannot use is a script bug, so it is fatal.
	var compiledSchema *workflowCompiledSchema
	if payload.schemaJSON != nil {
		var raw any
		if err := json.Unmarshal([]byte(*payload.schemaJSON), &raw); err != nil {
			rt.respond(callID, false, nil, "agent() opts.schema must be JSON-serializable.", true)
			return
		}
		compilation := compileWorkflowSchema(raw)
		if !compilation.OK {
			rt.respond(callID, false, nil, compilation.Message, true)
			return
		}
		compiledSchema = compilation.Compiled
		compiledSchema.Raw = *payload.schemaJSON
	}

	if rt.agentCount >= rt.agentCap {
		// Fatal, so parallel()/pipeline() rethrow instead of folding it into a null.
		rt.respond(callID, false, nil, fmt.Sprintf("Workflow exceeded its cap of %d agents.", rt.agentCap), true)
		return
	}
	index := rt.agentCount
	rt.agentCount++
	// A resumed call is the same child again: it keeps the agent id, so an abort still reaches it, and its spawn contract.
	agentID := fmt.Sprintf("wf-agent-%d", index)
	if resumed != nil {
		agentID = resumed.agentID
	}
	label := wfDerivedLabel(payload.prompt)
	if payload.label != nil {
		label = *payload.label
	} else if resumed != nil {
		label = resumed.label
	}
	agentType := "general-purpose"
	if resumed != nil {
		agentType = resumed.agentType
	} else if payload.agentType != nil {
		agentType = *payload.agentType
	}
	model, isolation := payload.model, payload.isolation
	if resumed != nil {
		model, isolation = resumed.model, resumed.isolation
	}
	rt.openLaunches = append(rt.openLaunches, callID)
	rt.openLabels[callID] = label

	base := &workflowAgentEntry{Type: "workflow_agent", Index: index, Label: label, State: entryStart, AgentID: agentID,
		AgentType: agentType, PromptPreview: wfPreviewText(payload.prompt)}
	if model != nil {
		base.Model = *model
	}
	if isolation != nil {
		base.Isolation = *isolation
	}
	base.PhaseIndex = payload.phaseIndex
	if payload.phaseTitle != nil {
		base.PhaseTitle = *payload.phaseTitle
	}
	// with copies the row: later mutations of base must not reach entries already emitted.
	with := func(mut func(*workflowAgentEntry)) *workflowAgentEntry {
		e := *base
		mut(&e)
		return &e
	}

	queuedAt := wfNowMs()
	rt.emit(with(func(e *workflowAgentEntry) { e.QueuedAt = &queuedAt }))

	// Replay before the semaphore, not after: a cached answer is not model running, so it must not hold a slot.
	key := workflowJournalKey(payload.keyInput())
	replayed := rt.replayAt(index, key)
	// A replayed answer still has to satisfy the schema: the key covers a schema that changed, not a hand-edited
	// journal, nor the empty text a torn entry leaves behind.
	if replayed != nil && compiledSchema != nil {
		text := ""
		if replayed.Text != nil {
			text = *replayed.Text
		}
		if recheck := applyWorkflowSchema(workflowSpawnResult{OK: true, Text: text}, compiledSchema); !recheck.OK {
			rt.prefixIntact = false
			replayed = nil
		}
	}
	if replayed != nil {
		rt.replayedCount++
		replayedText := ""
		if replayed.Text != nil {
			replayedText = *replayed.Text
		}
		at := wfNowMs()
		rt.emit(with(func(e *workflowAgentEntry) {
			e.QueuedAt, e.StartedAt, e.LastProgressAt, e.DurationMs = &queuedAt, &at, &at, new(int64(0))
			e.State = entryDone
			// The row reads as done, because it is: cached tells the dialog to annotate it "from resume journal".
			e.Cached = true
			e.ResultPreview = wfPreviewText(replayedText)
		}))
		rt.closeLaunch(callID)
		// Re-recorded so this run's journal is complete on its own terms: a resume of a resume must not have to walk
		// back through a chain of earlier files to find the prefix.
		rt.recordJournal(workflowJournalEntry{Index: index, Key: replayed.Key, OK: true, Text: &replayedText})
		rt.respond(callID, true, replayedText, "", false)
		return
	}

	resumeMark := payload.resume != nil

	// settleSkipped is a skip the user asked for, before the child ever started.
	settleSkipped := func() {
		rt.recordJournal(workflowJournalEntry{Index: index, Key: key, OK: false, Resumed: resumeMark})
		rt.emit(with(func(e *workflowAgentEntry) {
			e.QueuedAt = &queuedAt
			e.State = entryError
			e.Skipped = true
			e.Error = "Skipped by user."
		}))
		// null, exactly as a terminal failure gives: a skipped agent is one the script's .filter(Boolean) survives.
		rt.respond(callID, true, nil, "", false)
	}

	// Registered for exactly as long as the call is unanswered, which is the window in which skip and retry mean anything.
	live := &workflowLiveAgent{agentID: agentID}
	rt.liveAgents[index] = live
	defer delete(rt.liveAgents, index)
	attempt := 1
	for {
		// Held before the slot, not after: a paused run must not sit on concurrency it is not using.
		rt.pauseGate(c, live)
		if live.intent == "skip" {
			settleSkipped()
			return
		}

		// A resumed agent waits its turn like any other: it is the same amount of model running at once.
		rt.sem.acquire(c)
		if rt.aborted || rt.settled {
			rt.sem.release()
			rt.respond(callID, false, nil, "Workflow aborted.", true)
			return
		}
		// Paused while parked behind the limit: hand the permit back and go wait at the gate like everything else, or a
		// pause would leak exactly as many agents as were queued.
		if rt.paused && !rt.aborted && !rt.settled {
			rt.sem.release()
			continue
		}
		// Skipped while parked behind the limit: the permit arrived, and the only thing left is to give it back.
		if live.intent == "skip" {
			rt.sem.release()
			settleSkipped()
			return
		}

		// Carried on every emit from here on, so a retried row keeps saying why it is on its second attempt.
		attemptMark := func(e *workflowAgentEntry) {
			if attempt > 1 {
				e.Attempt, e.LastAttemptReason = attempt, "user-retry"
			}
		}

		startedAt := wfNowMs()
		rt.emit(with(func(e *workflowAgentEntry) { e.QueuedAt, e.StartedAt = &queuedAt, &startedAt; attemptMark(e) }))

		// Mutates base rather than emitting a standalone patch: every later emit spreads it, so the settle path carries
		// the effective values without knowing they were ever corrected. Re-emitting under the same index is what the
		// append-only, last-write-wins log is for.
		onResolved := func(info workflowResolvedInfo) {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			if info.RecordID != "" {
				base.RecordID = info.RecordID
			}
			if info.ModelName != "" {
				base.Model = info.ModelName
			}
			if info.ModelID != "" {
				base.ModelID = info.ModelID
			}
			if info.Thinking != "" {
				base.Thinking = info.Thinking
			}
			if info.RequestedThinking != "" {
				base.RequestedThinking = info.RequestedThinking
			}
			if info.RequestedModel != "" {
				base.RequestedModel = info.RequestedModel
			}
			// base.State is still "start", so emitting after the row reached a terminal state would revert it to
			// running under last-write-wins.
			if !rt.inflight[agentID] {
				return
			}
			now := wfNowMs()
			rt.emit(with(func(e *workflowAgentEntry) {
				e.QueuedAt, e.StartedAt, e.LastProgressAt = &queuedAt, &startedAt, &now
				attemptMark(e)
			}))
		}
		live.started = true
		rt.inflight[agentID] = true

		var result workflowSpawnResult
		var refusal error
		func() {
			defer func() {
				if r := recover(); r != nil {
					result = workflowSpawnResult{Error: fmt.Sprint(r)}
				}
				delete(rt.inflight, agentID)
				live.started = false
				rt.sem.release()
			}()
			var err error
			if resumed != nil && resumer != nil {
				err = c.call(func() (e error) {
					result, e = resumer.ResumeAgent(rt.runCtx, resumed.agentID, payload.prompt, onResolved)
					return
				})
			} else {
				req := &workflowSpawnRequest{Ctx: rt.runCtx, AgentID: agentID, Index: index, Prompt: payload.prompt, Label: label,
					AgentType: agentType, OnResolved: onResolved, Schema: compiledSchema, PhaseIndex: payload.phaseIndex}
				if model != nil {
					req.Model = *model
				}
				if payload.effort != nil {
					req.Effort = *payload.effort
				}
				if isolation != nil {
					req.Isolation = *isolation
				}
				if payload.phaseTitle != nil {
					req.PhaseTitle = *payload.phaseTitle
				}
				// Offered, not delegated: a host that can run it inside the child's worktree does, and hands back result.Gate.
				if payload.gate != nil {
					req.Gate = *payload.gate
				}
				err = c.call(func() (e error) { result, e = rt.host.SpawnAgent(req); return })
			}
			if err != nil {
				result = workflowSpawnResult{Error: err.Error()}
				if wfIsRefusal(err) {
					refusal = err
				}
				return
			}
			if result.OK {
				// Recorded before the gate runs: the child itself finished, so it is resumable even when its gate rejects.
				if _, seen := rt.completedByLabel[label]; !seen {
					rt.labelOrder = append(rt.labelOrder, label)
				}
				rt.completedByLabel[label] = &workflowCompletedChild{agentID: agentID, label: label, agentType: agentType, model: model, isolation: isolation}
				// Re-checked here, not just in the child's tool: before the gate, because a gate verifies work and there
				// is no work to verify if the shape is wrong.
				if compiledSchema != nil && result.OK {
					result = applyWorkflowSchema(result, compiledSchema)
				}
				if result.OK && payload.gate != nil && runGate != nil {
					result = rt.applyGate(c, result, *payload.gate, agentID, runGate)
				}
			}
		}()

		if rt.settled {
			return
		}

		// The stop that produced this result was ours, so run the same call again rather than reporting it.
		if live.intent == "retry" && !rt.aborted {
			live.intent = ""
			attempt++
			rt.emit(with(func(e *workflowAgentEntry) {
				e.QueuedAt = &queuedAt
				e.Attempt, e.LastAttemptReason = attempt, "user-retry"
			}))
			continue
		}

		// A feature this port does not do (workflow_host.go workflowRefusalError) fails the run, not just this agent.
		if refusal != nil {
			rt.recordJournal(workflowJournalEntry{Index: index, Key: key, OK: false, Resumed: resumeMark})
			rt.emit(with(func(e *workflowAgentEntry) {
				e.QueuedAt, e.StartedAt = &queuedAt, &startedAt
				e.State, e.Error = entryError, refusal.Error()
			}))
			// Failed directly, not answered with a fatal call error: a script's try/catch would swallow that, and the
			// run would go on as if the feature had run.
			rt.finish(workflowRunResult{Status: runFailed, Error: refusal.Error(), HasError: true})
			return
		}

		// Counted before the response is sent, so the very call that spent them already sees them in budget.spent().
		// Failed and skipped agents count too: they burned the tokens either way.
		rt.spentOutput += result.OutputTokens

		finishedAt := wfNowMs()
		common := func(e *workflowAgentEntry) {
			e.QueuedAt, e.StartedAt = &queuedAt, &startedAt
			attemptMark(e)
			e.LastProgressAt, e.DurationMs = &finishedAt, new(finishedAt-startedAt)
			if result.Tokens != nil {
				e.Tokens = result.Tokens
			}
			if result.ToolCalls != nil {
				e.ToolCalls = result.ToolCalls
			}
		}

		if result.OK {
			text := result.Text
			rt.emit(with(func(e *workflowAgentEntry) { common(e); e.State = entryDone; e.ResultPreview = wfPreviewText(text) }))
			rt.recordJournal(workflowJournalEntry{Index: index, Key: key, OK: true, Text: &text, Resumed: resumeMark})
			rt.respond(callID, true, text, "", false)
			return
		}
		// Recorded as a failure rather than left out: a gap would be read as an unchanged prefix on the next resume,
		// silently skipping the retry this whole mechanism exists to make cheap.
		rt.recordJournal(workflowJournalEntry{Index: index, Key: key, OK: false, Resumed: resumeMark})
		// A dead agent is a null in the script, not a thrown error.
		rt.emit(with(func(e *workflowAgentEntry) {
			common(e)
			e.State = entryError
			e.Error = result.Error
			if result.Error == "" {
				e.Error = "Agent failed."
			}
			e.Skipped = result.Skipped
		}))
		rt.respond(callID, true, nil, "", false)
		return
	}
}

// handleLoadWorkflow resolves one workflow(ref) and hands the child's source back. Resolution failures are non-fatal:
// workflow() throws on an unknown name so a script can catch it. A host with no loader at all is fatal.
func (rt *workflowRunState) handleLoadWorkflow(c *wfCoroutine, callID int, ref workflowRef) {
	loader, _ := rt.host.(workflowLoader)
	if loader == nil {
		rt.respond(callID, false, nil, "This workflow host cannot run nested workflows.", true)
		return
	}
	var source workflowScriptSource
	err := c.call(func() (e error) { source, e = loader.LoadWorkflow(ref); return })
	if err != nil {
		rt.respond(callID, false, nil, err.Error(), false)
		return
	}
	if !source.OK {
		rt.respond(callID, false, nil, source.Message, false)
		return
	}
	meta, body, verr := validateScript(source.Script)
	if verr != nil {
		rt.respond(callID, false, nil, verr.Error(), false)
		return
	}
	metaJSON, _ := json.Marshal(meta)
	rt.respond(callID, true, map[string]any{"name": meta.Name, "metaJson": string(metaJSON), "body": body}, "", false)
}

// workflowRunControl is the control surface of a run.
type workflowRunControl struct{ rt *workflowRunState }

func (c workflowRunControl) Pause() { c.rt.mu.Lock(); c.rt.paused = true; c.rt.mu.Unlock() }
func (c workflowRunControl) Resume() {
	c.rt.mu.Lock()
	c.rt.paused = false
	c.rt.releasePause()
	c.rt.mu.Unlock()
}
func (c workflowRunControl) IsPaused() bool {
	c.rt.mu.Lock()
	defer c.rt.mu.Unlock()
	return c.rt.paused
}
func (c workflowRunControl) Skip(index int) bool {
	rt := c.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	live := rt.liveAgents[index]
	if live == nil || live.intent != "" {
		return false
	}
	live.intent = "skip"
	// A running child is stopped, which comes back as a skipped result; a held one is woken so it can bail at its gate.
	if live.started {
		rt.host.AbortAgent(live.agentID)
	} else if live.wake != nil {
		live.wake()
	}
	return true
}
func (c workflowRunControl) Retry(index int) bool {
	rt := c.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	live := rt.liveAgents[index]
	if live == nil || !live.started || live.intent != "" {
		return false
	}
	live.intent = "retry"
	rt.host.AbortAgent(live.agentID)
	return true
}

// runWorkflow runs one workflow script to completion. It fails before starting for a script that cannot run at all
// (bad meta, over the size limit, control characters, args that cannot cross). Everything after the worker is live
// resolves instead, carrying the failure in Status: by then there is a progress log worth handing back. upstream: runWorkflow.
func runWorkflow(opts workflowRunOptions) (*workflowRunResult, error) {
	if opts.HasArgs {
		if err := assertBoundarySafe(opts.Args, "args"); err != nil {
			return nil, err
		}
	}
	meta, body, err := validateScript(opts.Script)
	if err != nil {
		return nil, err
	}
	rt := &workflowRunState{opts: opts, host: opts.Host, meta: meta, agentCap: opts.AgentCap, result: make(chan workflowRunResult, 1),
		inflight: map[string]bool{}, completedByLabel: map[string]*workflowCompletedChild{}, openLabels: map[int]string{},
		liveAgents: map[int]*workflowLiveAgent{}, pauseWaiters: map[*func()]bool{}, stopWatcher: make(chan struct{})}
	if rt.agentCap == 0 {
		rt.agentCap = workflowAgentCap
	}
	itemCap, nestedCap := opts.ItemCap, opts.NestedCap
	if itemCap == 0 {
		itemCap = workflowItemCap
	}
	if nestedCap == 0 {
		nestedCap = workflowNestedCap
	}
	rt.sem.limit = opts.Concurrency
	if rt.sem.limit == 0 {
		rt.sem.limit = workflowConcurrency(wfCPUCount())
	}
	rt.inboxCond = sync.NewCond(&rt.inboxMu)
	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	rt.runCtx, rt.runCancel = context.WithCancel(context.Background())
	defer rt.runCancel()

	if rt.opts.Journal != nil {
		rt.journalEntries = rt.opts.Journal.Entries
	}
	// A journal from a run that used agent({ resume }) is declined whole: a replayed agent leaves no conversation for a
	// later resume to continue. Declining up front beats stranding the first resume call partway through.
	journalResumes := false
	for _, e := range rt.journalEntries {
		if e.Resumed {
			journalResumes = true
		}
	}
	rt.prefixIntact = len(rt.journalEntries) > 0 && !journalResumes

	if opts.OnControl != nil {
		opts.OnControl(workflowRunControl{rt})
	}

	if ctx.Err() != nil {
		return &workflowRunResult{Status: runKilled, Meta: meta, Error: "Workflow aborted.", HasError: true, Progress: []workflowEntry{}}, nil
	}

	metaJSON, _ := json.Marshal(meta)
	var argsJSON *string
	if opts.HasArgs {
		b, err := wfJSONNoEscape(opts.Args)
		if err != nil {
			return nil, &workflowRuntimeError{err.Error()}
		}
		argsJSON = new(string(b))
	}
	go rt.dispatch()
	rt.mu.Lock()
	rt.worker = startWorkflowWorker(body, string(metaJSON), argsJSON, itemCap, nestedCap,
		func(m workerMessage) { rt.post(workflowInboxItem{msg: &m}) },
		func(unreported bool) {
			if unreported {
				rt.post(workflowInboxItem{exited: true})
			}
		})
	rt.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			rt.mu.Lock()
			if !rt.settled {
				rt.onAbort()
			}
			rt.mu.Unlock()
		case <-rt.stopWatcher:
		}
	}()
	res := <-rt.result
	rt.inboxMu.Lock()
	rt.inboxClosed = true
	rt.inboxCond.Broadcast()
	rt.inboxMu.Unlock()
	if res.Progress == nil {
		res.Progress = []workflowEntry{}
	}
	return &res, nil
}
