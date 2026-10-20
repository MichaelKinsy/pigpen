package pi_subagents

import (
	"context"

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

func isWorkflowFailureKind(k string) bool { panic("not implemented") }

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

func (e *WorkflowScriptError) Error() string { panic("not implemented") }
func (e *WorkflowScriptError) Unwrap() error { panic("not implemented") }

// workflowKindError is an error that carries a failure kind (taggedWorkflowError, scripted-workflow.ts:1197).
type workflowKindError struct {
	msg  string
	kind string
}

func (e *workflowKindError) Error() string { panic("not implemented") }

func taggedWorkflowError(msg, kind string) error { panic("not implemented") }

func workflowErrorKind(err error) string { panic("not implemented") }

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

func (r WorkflowSteerResult) object() *jsObject { panic("not implemented") }

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

func (r WorkflowHostCommandResult) object() *jsObject { panic("not implemented") }

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
func (o *RunWorkflowScriptOptions) UseLauncher(l WorkflowLauncher) { panic("not implemented") }

// ---- Go-side JSON data helpers (assertWorkflowJsonValue, omitUndefinedWorkflowValues) ----

func goTypeof(v any) string { panic("not implemented") }

func goFloat(v any) (float64, bool) { panic("not implemented") }

// assertWorkflowJSONValue is assertWorkflowJsonValue (scripted-workflow.ts:1451).
func assertWorkflowJSONValue(v any, path string, seen map[any]bool) error { panic("not implemented") }

// omitUndefinedGo is omitUndefinedWorkflowValues on host data: undefined properties are dropped and undefined array entries become null.
func omitUndefinedGo(v any) any { panic("not implemented") }

func omitUndefinedSeen(v any, seen map[*jsObject]bool) any { panic("not implemented") }

// omitNonJSONChildMetadata is omitNonJsonWorkflowResultMetadata: a `results` field that is not JSON is dropped.
func omitNonJSONChildMetadata(v any) any { panic("not implemented") }

// ---- accessors on child results ----

func objStr(o *jsObject, k string) string { panic("not implemented") }

func objBool(o *jsObject, k string) bool { panic("not implemented") }

func objTruthy(o *jsObject, k string) bool { panic("not implemented") }

func objHasString(o *jsObject, k string) (string, bool) { panic("not implemented") }

func (r *wfHost) abortReasonText(ctx context.Context) string { panic("not implemented") }

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
	panic("not implemented")
}

// DEFAULT_GLOBAL_CONCURRENCY_LIMIT

// runSandbox is the worker thread: it runs the script's jobs one after another and checks, after each, whether the script is done.
func (h *wfHost) runSandbox() { panic("not implemented") }

func (h *wfHost) terminate() { panic("not implemented") }

// wfHostPort is the host's end of the worker's channel: every message is queued to the host goroutine.
type wfHostPort struct{ h *wfHost }

func (p wfHostPort) call(id int, method string, args *jsObject) { panic("not implemented") }

func (p wfHostPort) observed(id int, key, op string) { panic("not implemented") }

func (p wfHostPort) emit(v any) { panic("not implemented") }

func (p wfHostPort) console(level, text string) { panic("not implemented") }

func (p wfHostPort) lanePlan(lanes any) { panic("not implemented") }

func (p wfHostPort) complete(v any) { panic("not implemented") }

func (p wfHostPort) fail(message, kind, phase string) { panic("not implemented") }

// ---- host-side callbacks that must not decide the outcome ----

func safely(fn func()) { panic("not implemented") }

func (h *wfHost) traceChanged() { panic("not implemented") }

// an abort the callback caused is seen at once, as an AbortSignal listener sees it

func (h *wfHost) lanePlanChanged(lanes []any) { panic("not implemented") }

func (h *wfHost) hostStepChanged(step WorkflowHostStepNode) { panic("not implemented") }

func (h *wfHost) partial() WorkflowScriptResult { panic("not implemented") }

func stoppedChildResult(key, message string) *jsObject { panic("not implemented") }

func responseBoundaryFailure(key string, err error) *jsObject { panic("not implemented") }

func (h *wfHost) lastStarted(key string) *WorkflowScriptTraceEntry { panic("not implemented") }

func (h *wfHost) pushStopped(key, message string) { panic("not implemented") }

// stopChild stops one keyed child (scripted-workflow.ts:2188).
func (h *wfHost) stopChild(key, message string) bool { panic("not implemented") }

func (h *wfHost) notifyChildSettled(key string, result *jsObject) { panic("not implemented") }

// ---- start, finish, abort ----

func (h *wfHost) begin() { panic("not implemented") }

func (h *wfHost) finish(outcome wfOutcome) { panic("not implemented") }

// Steering and host commands still in flight are awaited before the run ends (Promise.allSettled).

func (h *wfHost) complete(outcome wfOutcome) { panic("not implemented") }

// What is still waiting (a child that settles after the end) gets its callbacks, as the original's event loop would deliver
// them after the promise returned; a child that never settles does not keep the loop for more than the grace.

func (h *wfHost) onAbort() {
	panic(
		// the abort listener fires once ({ once: true } in the original)
		"not implemented")
}

// ---- messages from the script ----

func (h *wfHost) onEmit(v any) { panic("not implemented") }

func (h *wfHost) onComplete(v any) { panic("not implemented") }

func (h *wfHost) onError(message, kind, phase string) { panic("not implemented") }

func (h *wfHost) onObserved(callID int, key, op string) { panic("not implemented") }

// workflowReturnRecoveryHint is workflowReturnRecoveryHint (scripted-workflow.ts:1486).
func workflowReturnRecoveryHint(children []*jsObject) string { panic("not implemented") }

// workflowCheckProcessCwd is the validation of processCwd (scripted-workflow.ts:2086-2110): the real path must be a directory
// the process can enter.
func workflowCheckProcessCwd(dir string) error { panic("not implemented") }
