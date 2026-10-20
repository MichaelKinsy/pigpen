package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// The host half of a workflow run: runWorkflowScript (scripted-workflow.ts:2077-2705). It owns the children, the trace, the
// emitted values and the console of a script, and answers the script's calls. The script half is the goja sandbox
// (workflow_sandbox*.go). Everything here runs on one goroutine, the one that called RunWorkflowScript, so the state needs no
// locks (the original does the same on the Node main thread).

// Failure kinds of a workflow script (types.ts:134).
const (
	WorkflowFailureValidation          = "validation"
	WorkflowFailureScript              = "script"
	WorkflowFailureChild               = "child"
	WorkflowFailureReturnSerialization = "return-serialization"
	WorkflowFailureTimeout             = "timeout"
	WorkflowFailureDetachedChild       = "detached-child"
	WorkflowFailureRuntime             = "runtime"
)

func isWorkflowFailureKind(k string) bool {
	switch k {
	case WorkflowFailureValidation, WorkflowFailureScript, WorkflowFailureChild, WorkflowFailureReturnSerialization, WorkflowFailureTimeout, WorkflowFailureDetachedChild, WorkflowFailureRuntime:
		return true
	}
	return false
}

const workflowAssemblyFlushTimeout = 5 * time.Second // WORKFLOW_ASSEMBLY_FLUSH_TIMEOUT_MS

// workflowLateEventsGrace bounds how long a finished run keeps delivering the callbacks of children that settle after it.
const workflowLateEventsGrace = 10 * time.Second

// WorkflowScriptTraceEntry is one step of the trace (scripted-workflow.ts:1126).
type WorkflowScriptTraceEntry struct {
	Operation        string `json:"operation"` // run, status, steer, host
	Key              string `json:"key"`
	State            string `json:"state"` // started, completed, failed, detached, stopped, reused, queued, delivered, missed
	Agent            string `json:"agent,omitempty"`
	RunID            string `json:"runId,omitempty"`
	DurationMs       *int64 `json:"durationMs,omitempty"`
	Phase            string `json:"phase,omitempty"`
	Label            string `json:"label,omitempty"`
	Error            string `json:"error,omitempty"`
	GeneratedLaneKey string `json:"generatedLaneKey,omitempty"`
	Lane             any    `json:"lane,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// WorkflowConsoleEntry is a console line the script wrote.
type WorkflowConsoleEntry struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// WorkflowScriptResult is a finished script (scripted-workflow.ts:1185).
type WorkflowScriptResult struct {
	Value    any
	Emits    []any
	Console  []WorkflowConsoleEntry
	Trace    []WorkflowScriptTraceEntry
	Children []*jsObject // in launch order
}

// WorkflowScriptError is a failed script with what it did before it failed (scripted-workflow.ts:1201).
type WorkflowScriptError struct {
	Message string
	Partial WorkflowScriptResult // Value is unset
	Kind    string               // a WorkflowFailure* constant, or "" for a stop or reload
	Cause   error
}

func (e *WorkflowScriptError) Error() string { return e.Message }
func (e *WorkflowScriptError) Unwrap() error { return e.Cause }

// workflowKindError is an error that carries a failure kind (taggedWorkflowError, scripted-workflow.ts:1197).
type workflowKindError struct {
	msg  string
	kind string
}

func (e *workflowKindError) Error() string { return e.msg }

func taggedWorkflowError(msg, kind string) error { return &workflowKindError{msg, kind} }

func workflowErrorKind(err error) string {
	var k *workflowKindError
	if errors.As(err, &k) {
		return k.kind
	}
	return ""
}

// WorkflowAdmission tells a launch whether the call was admitted as part of a runs.all batch.
type WorkflowAdmission struct {
	Admitted bool
	Batch    bool
}

// WorkflowCallSpec is one child of an admission request.
type WorkflowCallSpec struct {
	Key    string
	Params *jsObject
}

// WorkflowResumeRef is what a child's `resume` names: a retained run id, or a keyed receipt of an earlier workflow.
type WorkflowResumeRef struct {
	RunID         string // set for a retained run id
	WorkflowRunID string // set with Key for a keyed receipt reference ({workflowRunId, key, latest: true})
	Key           string
}

// WorkflowResolvedResume is the retained run a resume reference resolved to.
type WorkflowResolvedResume struct {
	RunID  string
	RunIDs []string // the lineage, when the host knows it
	Object bool     // the host answered with an object (and so a lineage), not just an id
}

// WorkflowSteerOptions mirrors the original's (scripted-workflow.ts:1159).
type WorkflowSteerOptions struct {
	Mode         string
	Index        *int
	AckTimeoutMs *int
}

// WorkflowSteerTarget is one steered child of a receipt.
type WorkflowSteerTarget struct {
	Index  int    `json:"index"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// WorkflowSteerResult mirrors the original's (scripted-workflow.ts:1165).
type WorkflowSteerResult struct {
	Key            string `json:"key"`
	State          string `json:"state"` // queued, delivered, missed, failed
	RequestID      string `json:"requestId,omitempty"`
	DeliveryStatus string `json:"deliveryStatus,omitempty"`
	Targets        []WorkflowSteerTarget
	Error          string `json:"error,omitempty"`
}

func (r WorkflowSteerResult) object() *jsObject {
	o := newObject()
	o.set("key", r.Key)
	o.set("state", r.State)
	if r.RequestID != "" {
		o.set("requestId", r.RequestID)
	}
	if r.DeliveryStatus != "" {
		o.set("deliveryStatus", r.DeliveryStatus)
	}
	if r.Targets != nil {
		ts := make([]any, len(r.Targets))
		for i, t := range r.Targets {
			to := newObject()
			to.set("index", float64(t.Index))
			to.set("state", t.State)
			if t.Reason != "" {
				to.set("reason", t.Reason)
			}
			ts[i] = to
		}
		o.set("targets", ts)
	}
	if r.Error != "" {
		o.set("error", r.Error)
	}
	return o
}

// WorkflowHostCommandParams is a validated runs.host call (host-command.ts:17).
type WorkflowHostCommandParams struct {
	Command   string
	TimeoutMs int
	Output    string
	Role      string
	Provider  string
}

// WorkflowHostCommandResult mirrors the original's (host-command.ts:27).
type WorkflowHostCommandResult struct {
	Key        string
	OK         bool
	State      string // passed, failed, timed-out, stopped
	ExitCode   *int
	Stdout     string
	Stderr     string
	OutputPath string
	DurationMs int64
	Error      string
}

func (r WorkflowHostCommandResult) object() *jsObject {
	o := newObject()
	o.set("key", r.Key)
	o.set("kind", "command")
	o.set("ok", r.OK)
	o.set("state", r.State)
	if r.ExitCode != nil {
		o.set("exitCode", float64(*r.ExitCode))
	} else {
		o.set("exitCode", nil)
	}
	o.set("stdout", r.Stdout)
	o.set("stderr", r.Stderr)
	o.set("outputPath", r.OutputPath)
	o.set("durationMs", float64(r.DurationMs))
	if r.Error != "" {
		o.set("error", r.Error)
	}
	return o
}

// WorkflowHostStepNode is the progress row of a host command (host-step-status.ts, HostStepNode).
type WorkflowHostStepNode struct {
	ID         string
	Label      string
	Role       string
	Provider   string
	State      string // running, done, error, cancelled
	UpdatedAt  int64
	DeadlineAt int64
	Verdict    string
	ReasonCode string
	Detail     string
	ReportPath string
	ExitCode   *int
}

// WorkflowChildSettledNotification mirrors the original's (scripted-workflow.ts:1215).
type WorkflowChildSettledNotification struct {
	WorkflowRunID   string
	ChildKey        string
	ChildRunID      string
	Outcome         string // completed, failed, paused, stopped
	OutputReference string
	Error           string
	WorkflowRunning bool
}

// WorkflowLanePlan is the bounded metadata of a runs.lanes graph (scripted-workflow.ts:1144); lanes arrive as ordered objects.

// WorkflowState is the mission state a script reads and writes.
type WorkflowState struct {
	Get func(key string) *WorkflowFuture[any]
	Set func(key string, value any) *WorkflowFuture[struct{}]
}

// RunWorkflowScriptOptions mirrors the original's options (scripted-workflow.ts:1225). The callbacks return futures so that the
// run keeps the order promises would give it; a callback that knows its answer returns WorkflowResolved. Callbacks that only
// report (OnTrace, OnLanePlan, OnHostStep, OnChildSettled) may panic: the run logs nothing and goes on.
type RunWorkflowScriptOptions struct {
	Script string
	// Args is the deeply frozen sandbox global `args`; nil exposes an empty object.
	Args *jsObject
	// ProcessCwd, when set, must be a directory the run can enter: otherwise the run fails with "Workflow process cwd is
	// unavailable". (The original also re-anchors a deleted process cwd with process.chdir; a Go extension never changes its
	// working directory, so there is nothing to re-anchor.)
	ProcessCwd string
	// WorkflowRunID labels child-settled notifications.
	WorkflowRunID string
	// OneUsePermit is the first-slice admission context; it is never sent into the sandbox.
	OneUsePermit interface{ Claim(key string) string }
	// TimeoutMs bounds the script; nil lets it run without a timeout.
	TimeoutMs *float64
	// Ctx is the abort signal: cancelling it (with a cause) stops the workflow.
	Ctx context.Context
	// ContinueAfterAbortWhenChildrenSettled lets an async workflow flush pure result assembly after reload once every child is terminal.
	ContinueAfterAbortWhenChildrenSettled func(abortErr error) bool
	// GlobalConcurrencyLimit caps children executing at once (default 20).
	GlobalConcurrencyLimit *float64

	Admit         func(ctx context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}]
	Launch        func(ctx context.Context, key string, params *jsObject, admission WorkflowAdmission) *WorkflowFuture[*jsObject]
	ResolveResume func(ctx context.Context, ref WorkflowResumeRef, index *int) *WorkflowFuture[WorkflowResolvedResume]
	Status        func(ctx context.Context, keyOrRunID string) *WorkflowFuture[*jsObject]
	Steer         func(ctx context.Context, key, message string, options WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult]
	Host          func(ctx context.Context, key string, params WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult]
	OnHostStep    func(step WorkflowHostStepNode)
	State         *WorkflowState
	// RegisterStopChild receives the function that stops one keyed child, and nil when the run ends.
	RegisterStopChild func(stop func(key, message string) bool)
	OnTrace           func(trace []WorkflowScriptTraceEntry)
	OnLanePlan        func(lanes []any)
	OnEmit            func(emits []any) error
	OnChildSettled    func(n WorkflowChildSettledNotification)
}

// WorkflowLauncher is what an executor offers a workflow run: launching children and their companions. Optional capabilities are
// separate interfaces (WorkflowAdmitter, WorkflowResumeResolver, WorkflowSteerer, WorkflowHostRunner, WorkflowStateStore): a
// launcher without one leaves the call unavailable, as the original does when an option is omitted.
type WorkflowLauncher interface {
	Launch(ctx context.Context, key string, params *jsObject, admission WorkflowAdmission) *WorkflowFuture[*jsObject]
	Status(ctx context.Context, keyOrRunID string) *WorkflowFuture[*jsObject]
}

type WorkflowAdmitter interface {
	Admit(ctx context.Context, calls []WorkflowCallSpec) *WorkflowFuture[struct{}]
}

type WorkflowResumeResolver interface {
	ResolveResume(ctx context.Context, ref WorkflowResumeRef, index *int) *WorkflowFuture[WorkflowResolvedResume]
}

type WorkflowSteerer interface {
	Steer(ctx context.Context, key, message string, options WorkflowSteerOptions) *WorkflowFuture[WorkflowSteerResult]
}

type WorkflowHostRunner interface {
	Host(ctx context.Context, key string, params WorkflowHostCommandParams) *WorkflowFuture[WorkflowHostCommandResult]
}

type WorkflowStateStore interface {
	GetState(key string) *WorkflowFuture[any]
	SetState(key string, value any) *WorkflowFuture[struct{}]
}

// UseLauncher fills the callbacks of o from a launcher and the optional interfaces it implements.
func (o *RunWorkflowScriptOptions) UseLauncher(l WorkflowLauncher) {
	o.Launch = l.Launch
	o.Status = l.Status
	if a, ok := l.(WorkflowAdmitter); ok {
		o.Admit = a.Admit
	}
	if a, ok := l.(WorkflowResumeResolver); ok {
		o.ResolveResume = a.ResolveResume
	}
	if a, ok := l.(WorkflowSteerer); ok {
		o.Steer = a.Steer
	}
	if a, ok := l.(WorkflowHostRunner); ok {
		o.Host = a.Host
	}
	if a, ok := l.(WorkflowStateStore); ok {
		o.State = &WorkflowState{Get: a.GetState, Set: a.SetState}
	}
}

// ---- Go-side JSON data helpers (assertWorkflowJsonValue, omitUndefinedWorkflowValues) ----

func goTypeof(v any) string {
	switch v.(type) {
	case nil:
		return "object"
	case undef:
		return "undefined"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number"
	}
	return "other"
}

func goFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

// assertWorkflowJSONValue is assertWorkflowJsonValue (scripted-workflow.ts:1451).
func assertWorkflowJSONValue(v any, path string, seen map[any]bool) error {
	switch x := v.(type) {
	case nil, string, bool:
		return nil
	case undef:
		return fmt.Errorf("%s must be a JSON value; received undefined.", path)
	case func():
		return fmt.Errorf("%s must be a JSON value; received function.", path)
	case []any:
		for i, e := range x {
			if err := assertWorkflowJSONValue(e, fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
				return err
			}
		}
		return nil
	case *jsObject:
		if seen[x] {
			return fmt.Errorf("%s must not contain cycles.", path)
		}
		seen[x] = true
		defer delete(seen, x)
		for _, k := range x.order() {
			if err := assertWorkflowJSONValue(x.vals[k], path+"."+k, seen); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			if err := assertWorkflowJSONValue(x[k], path+"."+k, seen); err != nil {
				return err
			}
		}
		return nil
	}
	if f, ok := goFloat(v); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%s must contain only finite JSON numbers.", path)
		}
		return nil
	}
	if goTypeof(v) == "number" {
		return nil
	}
	return fmt.Errorf("%s must contain only plain JSON objects.", path)
}

// omitUndefinedGo is omitUndefinedWorkflowValues on host data: undefined properties are dropped and undefined array entries become null.
func omitUndefinedGo(v any) any { return omitUndefinedSeen(v, map[*jsObject]bool{}) }

func omitUndefinedSeen(v any, seen map[*jsObject]bool) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			if _, u := e.(undef); u {
				out[i] = nil
			} else {
				out[i] = omitUndefinedSeen(e, seen)
			}
		}
		return out
	case *jsObject:
		if seen[x] {
			return x
		}
		seen[x] = true
		defer delete(seen, x)
		o := newObject()
		for _, k := range x.keys {
			e := x.vals[k]
			if _, u := e.(undef); u {
				continue
			}
			o.set(k, omitUndefinedSeen(e, seen))
		}
		return o
	case map[string]any:
		o := newObject()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			if _, u := x[k].(undef); u {
				continue
			}
			o.set(k, omitUndefinedSeen(x[k], seen))
		}
		return o
	}
	return v
}

// omitNonJSONChildMetadata is omitNonJsonWorkflowResultMetadata: a `results` field that is not JSON is dropped.
func omitNonJSONChildMetadata(v any) any {
	n := omitUndefinedGo(v)
	o, ok := n.(*jsObject)
	if !ok {
		return n
	}
	if _, has := o.vals["results"]; !has {
		return n
	}
	if err := assertWorkflowJSONValue(o.vals["results"], "runs.run result.results", map[any]bool{}); err != nil {
		c := newObject()
		for _, k := range o.keys {
			if k != "results" {
				c.set(k, o.vals[k])
			}
		}
		return c
	}
	return n
}

// ---- accessors on child results ----

func objStr(o *jsObject, k string) string {
	if o == nil {
		return ""
	}
	s, _ := o.vals[k].(string)
	return s
}

func objBool(o *jsObject, k string) bool {
	if o == nil {
		return false
	}
	b, _ := o.vals[k].(bool)
	return b
}

func objTruthy(o *jsObject, k string) bool {
	if o == nil {
		return false
	}
	switch x := o.vals[k].(type) {
	case nil, undef:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && x == x
	}
	return true
}

func objHasString(o *jsObject, k string) (string, bool) {
	if o == nil {
		return "", false
	}
	s, ok := o.vals[k].(string)
	return s, ok
}

func (r *wfHost) abortReasonText(ctx context.Context) string {
	cause := context.Cause(ctx)
	switch {
	case cause == nil || cause == context.Canceled:
		return "Workflow script aborted."
	}
	return cause.Error()
}

// wfOutcome is what finish() receives.
type wfOutcome struct {
	value    any
	hasValue bool
	err      error
}

type wfLaunch struct {
	fingerprint      string
	promise          *WorkflowFuture[*jsObject]
	observed         bool
	generatedLaneKey string
}

type wfSteerCall struct {
	key      string
	promise  *WorkflowFuture[*jsObject]
	observed bool
}

type wfHostCall struct {
	key      string
	promise  *WorkflowFuture[*jsObject]
	observed bool
}

type wfHost struct {
	opt  *RunWorkflowScriptOptions
	loop *workflowLoop
	sb   *wfSandbox
	sbQ  *workflowLoop

	launchSem *ParallelSemaphore

	emits    []any
	console  []WorkflowConsoleEntry
	trace    []WorkflowScriptTraceEntry
	children map[string]*jsObject
	order    []string

	launches                                                map[string]*wfLaunch
	launchOrder                                             []string
	steers                                                  map[int]*wfSteerCall
	steerOrder                                              []int
	hostCalls                                               map[int]*wfHostCall
	hostOrder                                               []int
	stoppedLaunches                                         map[string]bool
	stopControllers                                         map[string]context.CancelCauseFunc
	batchAdmissions                                         map[string]*WorkflowFuture[struct{}]
	observedRunCalls, observedSteerCalls, observedHostCalls map[int]bool

	childCtx    context.Context
	childCancel context.CancelCauseFunc

	recoveryBarrier                            *string
	settled, finishing, assemblyAbortRequested bool
	flushTimer                                 *time.Timer
	timer                                      *time.Timer
	abortError                                 error
	abortSeen                                  bool
	done                                       chan struct{}
	abortStop                                  func() bool
	result                                     *WorkflowScriptResult
	resultErr                                  *WorkflowScriptError
	startedAt                                  time.Time
}

// RunWorkflowScript runs a script to its end and returns what it returned, or a *WorkflowScriptError.
func RunWorkflowScript(options RunWorkflowScriptOptions) (WorkflowScriptResult, error) {
	empty := func() WorkflowScriptResult { return WorkflowScriptResult{} }
	if jsTrim(options.Script) == "" {
		return WorkflowScriptResult{}, &WorkflowScriptError{Message: "workflowScript must not be empty.", Partial: empty(), Kind: WorkflowFailureValidation}
	}
	if options.TimeoutMs != nil && (*options.TimeoutMs != math.Trunc(*options.TimeoutMs) || *options.TimeoutMs < 1 || math.IsInf(*options.TimeoutMs, 0)) {
		return WorkflowScriptResult{}, &WorkflowScriptError{Message: "workflow script timeout must be a positive integer.", Partial: empty(), Kind: WorkflowFailureRuntime}
	}
	limit := 20.0 // DEFAULT_GLOBAL_CONCURRENCY_LIMIT
	if options.GlobalConcurrencyLimit != nil {
		l := *options.GlobalConcurrencyLimit
		if l != math.Trunc(l) || l < 1 || l > 9007199254740991 {
			return WorkflowScriptResult{}, &WorkflowScriptError{Message: "workflow script global concurrency limit must be a positive integer.", Partial: empty(), Kind: WorkflowFailureRuntime}
		}
		limit = l
	}
	if options.ProcessCwd != "" {
		if err := workflowCheckProcessCwd(options.ProcessCwd); err != nil {
			return WorkflowScriptResult{}, &WorkflowScriptError{Message: fmt.Sprintf("Workflow process cwd is unavailable: %s: %s", options.ProcessCwd, err.Error()), Partial: empty(), Kind: WorkflowFailureRuntime, Cause: err}
		}
	}
	h := &wfHost{
		opt: &options, loop: newWorkflowLoop(), launchSem: NewParallelSemaphore(limit),
		children: map[string]*jsObject{}, launches: map[string]*wfLaunch{}, steers: map[int]*wfSteerCall{}, hostCalls: map[int]*wfHostCall{},
		stoppedLaunches: map[string]bool{}, stopControllers: map[string]context.CancelCauseFunc{}, batchAdmissions: map[string]*WorkflowFuture[struct{}]{},
		observedRunCalls: map[int]bool{}, observedSteerCalls: map[int]bool{}, observedHostCalls: map[int]bool{},
		startedAt: time.Now(),
	}
	h.childCtx, h.childCancel = context.WithCancelCause(context.Background())
	h.sbQ = newWorkflowLoop()
	h.sb = newWfSandbox(wfHostPort{h}, h.sbQ)
	go h.runSandbox()

	if options.RegisterStopChild != nil {
		options.RegisterStopChild(h.stopChild)
	}
	h.loop.postMacro(h.begin)
	h.done = make(chan struct{})
	go h.loop.run()
	<-h.done
	if h.resultErr != nil {
		return WorkflowScriptResult{}, h.resultErr
	}
	return *h.result, nil
}

// runSandbox is the worker thread: it runs the script's jobs one after another and checks, after each, whether the script is done.
func (h *wfHost) runSandbox() {
	defer func() {
		if r := recover(); r != nil {
			h.loop.postMacro(func() {
				h.finish(wfOutcome{err: taggedWorkflowError(fmt.Sprintf("Workflow worker failed: %v", r), WorkflowFailureRuntime)})
			})
		}
	}()
	for {
		job, ok := h.sbQ.next()
		if !ok {
			return
		}
		job()
		h.sb.afterJob()
	}
}

func (h *wfHost) terminate() {
	h.sbQ.close()
	h.sb.vm.Interrupt("workflow terminated")
}

// wfHostPort is the host's end of the worker's channel: every message is queued to the host goroutine.
type wfHostPort struct{ h *wfHost }

func (p wfHostPort) call(id int, method string, args *jsObject) {
	p.h.loop.postMacro(func() { p.h.onCall(id, method, args) })
}

func (p wfHostPort) observed(id int, key, op string) {
	p.h.loop.postMacro(func() { p.h.onObserved(id, key, op) })
}

func (p wfHostPort) emit(v any) { p.h.loop.postMacro(func() { p.h.onEmit(v) }) }

func (p wfHostPort) console(level, text string) {
	p.h.loop.postMacro(func() {
		if p.h.settled {
			return
		}
		p.h.console = append(p.h.console, WorkflowConsoleEntry{Level: level, Text: text})
	})
}

func (p wfHostPort) lanePlan(lanes any) {
	p.h.loop.postMacro(func() {
		if p.h.settled {
			return
		}
		if list, ok := lanes.([]any); ok {
			p.h.lanePlanChanged(list)
		}
	})
}

func (p wfHostPort) complete(v any) { p.h.loop.postMacro(func() { p.h.onComplete(v) }) }

func (p wfHostPort) fail(message, kind, phase string) {
	p.h.loop.postMacro(func() { p.h.onError(message, kind, phase) })
}

// ---- host-side callbacks that must not decide the outcome ----

func safely(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

func (h *wfHost) traceChanged() {
	if h.opt.OnTrace == nil {
		return
	}
	cp := append([]WorkflowScriptTraceEntry(nil), h.trace...)
	safely(func() { h.opt.OnTrace(cp) })
	h.pollAbort() // an abort the callback caused is seen at once, as an AbortSignal listener sees it
}

func (h *wfHost) lanePlanChanged(lanes []any) {
	if h.opt.OnLanePlan != nil {
		safely(func() { h.opt.OnLanePlan(lanes) })
		h.pollAbort()
	}
}

func (h *wfHost) hostStepChanged(step WorkflowHostStepNode) {
	if h.opt.OnHostStep != nil {
		safely(func() { h.opt.OnHostStep(step) })
		h.pollAbort()
	}
}

func (h *wfHost) partial() WorkflowScriptResult {
	var children []*jsObject
	for _, k := range h.order {
		if c, ok := h.children[k]; ok {
			children = append(children, c)
		}
	}
	return WorkflowScriptResult{Emits: h.emits, Console: h.console, Trace: h.trace, Children: children}
}

func stoppedChildResult(key, message string) *jsObject {
	o := newObject()
	o.set("key", key)
	o.set("ok", false)
	o.set("stopped", true)
	o.set("output", message)
	o.set("error", message)
	o.set("artifactPaths", []any{})
	return o
}

func responseBoundaryFailure(key string, err error) *jsObject {
	o := newObject()
	o.set("key", key)
	o.set("ok", false)
	o.set("output", err.Error())
	o.set("error", err.Error())
	o.set("artifactPaths", []any{})
	return o
}

func (h *wfHost) lastStarted(key string) *WorkflowScriptTraceEntry {
	for i := len(h.trace) - 1; i >= 0; i-- {
		e := h.trace[i]
		if e.Operation == "run" && e.Key == key && e.State == "started" {
			return &h.trace[i]
		}
	}
	return nil
}

func (h *wfHost) pushStopped(key, message string) {
	started := h.lastStarted(key)
	e := WorkflowScriptTraceEntry{Operation: "run", Key: key, State: "stopped", Error: message}
	if started != nil {
		e.Agent, e.Phase, e.Label, e.GeneratedLaneKey = started.Agent, started.Phase, started.Label, started.GeneratedLaneKey
	}
	h.trace = append(h.trace, e)
}

// stopChild stops one keyed child (scripted-workflow.ts:2188).
func (h *wfHost) stopChild(key, message string) bool {
	if message == "" {
		message = "Workflow child '" + key + "' stopped by user."
	}
	if _, launched := h.launches[key]; !launched {
		return false
	}
	if _, done := h.children[key]; done {
		return false
	}
	h.stoppedLaunches[key] = true
	h.children[key] = stoppedChildResult(key, message)
	if cancel := h.stopControllers[key]; cancel != nil {
		cancel(errors.New(message))
	}
	h.pushStopped(key, message)
	h.traceChanged()
	return true
}

func (h *wfHost) notifyChildSettled(key string, result *jsObject) {
	if objStr(result, "state") == "running" || h.opt.OnChildSettled == nil || h.opt.WorkflowRunID == "" {
		return
	}
	outcome := "failed"
	switch {
	case objBool(result, "ok"):
		outcome = "completed"
	case objTruthy(result, "stopped"):
		outcome = "stopped"
	case objTruthy(result, "detached"):
		outcome = "paused"
	}
	ref := objStr(result, "outputReference")
	if ref == "" {
		ref = objStr(result, "outputArtifactPath")
	}
	n := WorkflowChildSettledNotification{WorkflowRunID: h.opt.WorkflowRunID, ChildKey: key, ChildRunID: objStr(result, "runId"), Outcome: outcome, OutputReference: ref, WorkflowRunning: !h.settled && !h.finishing}
	if !objBool(result, "ok") {
		n.Error = objStr(result, "error")
	}
	safely(func() { h.opt.OnChildSettled(n) })
	h.pollAbort()
}

// ---- start, finish, abort ----

func (h *wfHost) begin() {
	if h.opt.TimeoutMs != nil {
		d := time.Duration(*h.opt.TimeoutMs) * time.Millisecond
		ms := *h.opt.TimeoutMs
		h.timer = time.AfterFunc(d, func() {
			h.loop.postMacro(func() {
				h.finish(wfOutcome{err: taggedWorkflowError(fmt.Sprintf("Workflow script timed out after %sms.", jsNumber(ms)), WorkflowFailureTimeout)})
			})
		})
	}
	if ctx := h.opt.Ctx; ctx != nil {
		if ctx.Err() != nil {
			h.onAbort()
			return
		}
		h.abortStop = context.AfterFunc(ctx, func() { h.loop.postMacro(h.onAbort) })
	}
	h.sbQ.post(func() {
		h.sb.start(wfStart{script: h.opt.Script, args: h.opt.Args, stateEnabled: h.opt.State != nil})
	})
}

func (h *wfHost) finish(outcome wfOutcome) {
	if h.settled || h.finishing {
		return
	}
	h.finishing = true
	if h.flushTimer != nil {
		h.flushTimer.Stop()
		h.flushTimer = nil
	}
	if outcome.err != nil {
		h.childCancel(outcome.err)
	} else {
		h.childCancel(errors.New("Workflow script completed."))
	}
	// Steering and host commands still in flight are awaited before the run ends (Promise.allSettled).
	var pending []func(func())
	for _, id := range h.steerOrder {
		f := h.steers[id].promise
		pending = append(pending, func(done func()) { onSettle(h.loop, f, func(*jsObject, error) { done() }) })
	}
	for _, id := range h.hostOrder {
		f := h.hostCalls[id].promise
		pending = append(pending, func(done func()) { onSettle(h.loop, f, func(*jsObject, error) { done() }) })
	}
	remaining := len(pending)
	complete := func() { h.complete(outcome) }
	if remaining == 0 {
		h.loop.post(complete)
		return
	}
	for _, p := range pending {
		p(func() {
			remaining--
			if remaining == 0 {
				complete()
			}
		})
	}
}

func (h *wfHost) complete(outcome wfOutcome) {
	if h.settled {
		return
	}
	h.settled = true
	if h.opt.RegisterStopChild != nil {
		h.opt.RegisterStopChild(nil)
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	if h.abortStop != nil {
		h.abortStop()
	}
	h.terminate()
	var unobserved []string
	if outcome.hasValue {
		for _, k := range h.launchOrder {
			if !h.launches[k].observed {
				unobserved = append(unobserved, k)
			}
		}
	}
	quote := func(keys []string) string {
		q := make([]string, len(keys))
		for i, k := range keys {
			q[i] = "'" + k + "'"
		}
		return strings.Join(q, ", ")
	}
	var completionError string
	switch {
	case len(unobserved) > 0:
		completionError = "workflowScript completed with unawaited runs.run launch(es): " + quote(unobserved) + ". For ordinary parallel fanout use await runs.all([{key, agent, task}, ...]); do not read .output from unawaited launches."
	case outcome.hasValue:
		var us []string
		for _, id := range h.steerOrder {
			if !h.steers[id].observed {
				us = append(us, h.steers[id].key)
			}
		}
		if len(us) > 0 {
			completionError = "workflowScript completed with unawaited runs.steer call(s): " + quote(us) + ". Await or return each call."
			break
		}
		var uh []string
		for _, id := range h.hostOrder {
			if !h.hostCalls[id].observed {
				uh = append(uh, h.hostCalls[id].key)
			}
		}
		if len(uh) > 0 {
			completionError = "workflowScript completed with unawaited runs.host call(s): " + quote(uh) + ". Await or return each call."
		}
	}
	switch {
	case outcome.err != nil:
		kind := workflowErrorKind(outcome.err)
		if !isWorkflowFailureKind(kind) {
			kind = ""
		}
		h.resultErr = &WorkflowScriptError{Message: outcome.err.Error(), Partial: h.partial(), Kind: kind, Cause: outcome.err}
	case completionError != "":
		h.resultErr = &WorkflowScriptError{Message: completionError, Partial: h.partial(), Kind: WorkflowFailureValidation}
	default:
		r := h.partial()
		r.Value = outcome.value
		h.result = &r
	}
	// What is still waiting (a child that settles after the end) gets its callbacks, as the original's event loop would deliver
	// them after the promise returned; a child that never settles does not keep the loop for more than the grace.
	h.loop.lingerThenClose(workflowLateEventsGrace)
	close(h.done)
}

func (h *wfHost) onAbort() {
	// the abort listener fires once ({ once: true } in the original)
	if h.abortSeen {
		return
	}
	h.abortSeen = true
	reason := context.Cause(h.opt.Ctx)
	var err error
	switch {
	case reason == nil || reason == context.Canceled:
		err = errors.New("This operation was aborted")
		if reason == nil {
			err = errors.New("Workflow script aborted.")
		}
	default:
		err = reason
	}
	if h.finishing {
		return
	}
	h.abortError = err
	allSettled := len(h.launches) > 0
	for k := range h.launches {
		if _, ok := h.children[k]; !ok {
			allSettled = false
		}
	}
	mayFlush := false
	if h.opt.ContinueAfterAbortWhenChildrenSettled != nil {
		var cbErr any
		func() {
			defer func() { cbErr = recover() }()
			mayFlush = h.opt.ContinueAfterAbortWhenChildrenSettled(err)
		}()
		if cbErr != nil {
			h.finish(wfOutcome{err: taggedWorkflowError(fmt.Sprintf("Workflow assembly flush eligibility failed: %v", cbErr), WorkflowFailureRuntime)})
			return
		}
	}
	if mayFlush && allSettled {
		h.assemblyAbortRequested = true
		h.childCancel(err)
		h.flushTimer = time.AfterFunc(workflowAssemblyFlushTimeout, func() { h.loop.postMacro(func() { h.finish(wfOutcome{err: err}) }) })
		return
	}
	for _, k := range h.launchOrder {
		if _, ok := h.children[k]; ok {
			continue
		}
		h.stoppedLaunches[k] = true
		h.pushStopped(k, err.Error())
	}
	h.traceChanged()
	h.finish(wfOutcome{err: err})
}

// ---- messages from the script ----

func (h *wfHost) onEmit(v any) {
	if h.settled {
		return
	}
	if err := assertWorkflowJSONValue(v, "emit", map[any]bool{}); err != nil {
		h.finish(wfOutcome{err: taggedWorkflowError("Workflow emit could not be persisted: "+err.Error(), WorkflowFailureRuntime)})
		return
	}
	h.emits = append(h.emits, v)
	if h.opt.OnEmit != nil {
		cp := append([]any(nil), h.emits...)
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%v", r)
				}
			}()
			err = h.opt.OnEmit(cp)
		}()
		if err != nil {
			h.emits = h.emits[:len(h.emits)-1]
			h.finish(wfOutcome{err: taggedWorkflowError("Workflow emit could not be persisted: "+err.Error(), WorkflowFailureRuntime)})
		}
	}
}

func (h *wfHost) onComplete(v any) {
	if h.settled {
		return
	}
	if err := assertWorkflowJSONValue(v, "return", map[any]bool{}); err != nil {
		h.finish(wfOutcome{err: taggedWorkflowError("Workflow return could not be persisted: "+err.Error(), WorkflowFailureReturnSerialization)})
		return
	}
	h.finish(wfOutcome{value: v, hasValue: true})
}

func (h *wfHost) onError(message, kind, phase string) {
	if h.settled {
		return
	}
	text := message
	if phase == "return-serialization" {
		text += workflowReturnRecoveryHint(h.partial().Children)
	}
	if isWorkflowFailureKind(kind) {
		h.finish(wfOutcome{err: taggedWorkflowError(text, kind)})
		return
	}
	h.finish(wfOutcome{err: errors.New(text)})
}

func (h *wfHost) onObserved(callID int, key, op string) {
	if h.settled {
		return
	}
	switch op {
	case "run":
		if l, ok := h.launches[key]; ok && key != "" {
			l.observed = true
		} else {
			h.observedRunCalls[callID] = true
		}
	case "steer":
		if s, ok := h.steers[callID]; ok {
			s.observed = true
		} else {
			h.observedSteerCalls[callID] = true
		}
	case "host":
		if c, ok := h.hostCalls[callID]; ok {
			c.observed = true
		} else {
			h.observedHostCalls[callID] = true
		}
	}
}

// workflowReturnRecoveryHint is workflowReturnRecoveryHint (scripted-workflow.ts:1486).
func workflowReturnRecoveryHint(children []*jsObject) string {
	if len(children) == 0 {
		return " Return only plain JSON data. For a child result, select fields such as { runId: child.runId, ok: child.ok, outputReference: child.outputReference }."
	}
	var refs []string
	for _, c := range children[:min(len(children), 10)] {
		var fields []string
		if id := objStr(c, "runId"); id != "" {
			fields = append(fields, "runId="+string(utf16Slice(id, 500)))
		}
		if ref := objStr(c, "outputReference"); ref != "" {
			fields = append(fields, "outputReference="+string(utf16Slice(ref, 500)))
		}
		if paths, ok := c.vals["artifactPaths"].([]any); ok && len(paths) > 0 {
			if p, ok := paths[0].(string); ok && p != "" {
				fields = append(fields, "artifact="+string(utf16Slice(p, 500)))
			}
		}
		s := "'" + objStr(c, "key") + "'"
		if len(fields) > 0 {
			s += " (" + strings.Join(fields, ", ") + ")"
		}
		refs = append(refs, s)
	}
	running := false
	for _, c := range children {
		if objStr(c, "state") == "running" {
			running = true
		}
	}
	head := "Child work completed before return serialization failed."
	if running {
		head = "Child calls returned before return serialization failed; launch receipts do not prove completion."
	}
	more := ""
	if len(children) > len(refs) {
		more = fmt.Sprintf(", and %d more", len(children)-len(refs))
	}
	return " " + head + " Recover outputs from: " + strings.Join(refs, ", ") + more + ". Return a plain projection such as { runId: child.runId, ok: child.ok, outputReference: child.outputReference }."
}

// workflowCheckProcessCwd is the validation of processCwd (scripted-workflow.ts:2086-2110): the real path must be a directory
// the process can enter.
func workflowCheckProcessCwd(dir string) error {
	target, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "access", Path: target, Err: syscall.ENOTDIR}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return &fs.PathError{Op: "access", Path: target, Err: fs.ErrPermission}
	}
	return nil
}
