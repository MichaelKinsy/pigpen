package pi_subagents

// The bg_wait tool: block the current turn until outstanding async runs, provider work or a remembered detached foreground
// run finishes (or needs attention), the window elapses or the turn is aborted. Ports subagent-wait.ts and auto-drain.ts.

import ()

const (
	defaultWaitTimeoutMs      = 30 * 60 * 1000
	minPollIntervalMs         = 250
	defaultPollIntervalMs     = 1000
	defaultAutoDrainTimeoutMs = 30 * 60 * 1000
)

// WaitParams are the arguments of the bg_wait tool.
type WaitParams struct {
	ID              string
	NonBlocking     bool
	All             bool
	TimeoutMs       *int64
	StopOnAttention *bool
}

// WaitDetails is the machine-readable reason a wait returned without all its work finished.
type WaitDetails struct {
	Reason              string       `json:"reason"` // window_elapsed, supervisor_request
	TimedOut            bool         `json:"timedOut"`
	ActiveRunIDs        []string     `json:"activeRunIds"`
	ActiveProviderItems []ProviderID `json:"activeProviderItems"`
}

// ProviderID names one provider work item.
type ProviderID struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// WaitResult is the outcome of a wait: its text, whether it is an error, and the details a caller can read.
type WaitResult struct {
	Text        string
	IsError     bool
	Completions []WaitCompletion
	Wait        *WaitDetails
	Usage       *Usage
}

// WaitEventBus is the event surface a wait subscribes to (pi.events).
type WaitEventBus interface {
	On(channel string, handler func(any)) func()
}

// WaitSubscribe arms a durable wake subscription for one exact run.
type WaitSubscribe func(targetKind, runID, requestedID string, timeoutMs int64) (token string, expiresAt int64, err error)

// BackgroundWorkDeps is the provider protocol seam of a wait (tests inject it).
type BackgroundWorkDeps struct {
	Snapshot     func(sessionID string, nowMs int64) (BackgroundWorkSnapshot, error)
	WakeChannels func() []string
}

// WaitDeps are the seams and settings of a wait.
type WaitDeps struct {
	State                       *SubagentState
	OnUpdate                    func(WaitResult)
	AsyncDirRoot                string
	ResultsDir                  string
	NestedRootRunID             string
	Kill                        killFunc
	Now                         func() int64
	PollIntervalMs              int64
	Enabled                     *bool
	DefaultTimeoutMs            *int64
	Sleep                       func(ms int64, done <-chan struct{})
	StopOnAttention             *bool
	FailOnFailedRuns            bool
	FailOnAttention             bool
	HasPendingSupervisorRequest func() bool
	Subscribe                   WaitSubscribe
	BackgroundWork              *BackgroundWorkDeps
	Events                      WaitEventBus
}

// nestedRunScope is nested-events.ts nestedRunScope: where the runs of a nested root live.
func nestedRunScope(rootRunID string) (asyncDirRoot, resultsDir string) { panic("not ported yet") }

var activeWaitStates = []string{"queued", "running"}

func defaultSleep(ms int64, done <-chan struct{}) { panic("not ported yet") }

func wakeChannelsOf(deps WaitDeps) []string { panic("not ported yet") }

var waitWakeChannels = []string{intercomDetachRequestEvent, subagentAsyncCompleteEvent, subagentForegroundCompleteE, subagentControlEvent, subagentControlIntercomEvt, subagentResultIntercomEvt}

// waitForWake sleeps up to ms, but wakes early when a subagent event fires on the bus or the turn aborts.
func waitForWake(ms int64, signal <-chan struct{}, deps WaitDeps) error { panic("not ported yet") }

func listBackgroundWorkWakeChannelsOrErr(deps WaitDeps) ([]string, error) { panic("not ported yet") }

func matchesID(run AsyncRunSummary, id string) bool { panic("not ported yet") }

func needsAttention(run AsyncRunSummary) bool { panic("not ported yet") }

func hasSupervisorTool(run AsyncRunSummary) bool { panic("not ported yet") }

func backgroundWorkIdentity(item RegisteredBackgroundWorkItem) string { panic("not ported yet") }

func backgroundWorkForSession(deps WaitDeps, nowMs int64) (BackgroundWorkSnapshot, error) {
	panic("not ported yet")
}

type waitScope struct{ AsyncDirRoot, ResultsDir string }

// waitRunScopes: the ordinary scope, then the nested root's scope when the wait runs for a child.
func waitRunScopes(asyncDirRoot, resultsDir, nestedRootRunID string) []waitScope {
	panic("not ported yet")
}

func scopesOf(deps WaitDeps) []waitScope { panic("not ported yet") }

func collectScopedCompletions(terminal []AsyncRunSummary, deps WaitDeps, references *[]string) ([]WaitCompletion, error) {
	panic("not ported yet")
}

func listOpts(deps WaitDeps, scope waitScope) AsyncRunListOptions { panic("not ported yet") }

func runsForSession(p WaitParams, deps WaitDeps, activeOnly bool) ([]AsyncRunSummary, error) {
	panic("not ported yet")
}

func attentionRuns(p WaitParams, deps WaitDeps, initial map[string]bool) ([]AsyncRunSummary, error) {
	panic("not ported yet")
}

func runsForIDs(ids []string, deps WaitDeps) ([]AsyncRunSummary, error) { panic("not ported yet") }

func summarizeTerminalRuns(runs []AsyncRunSummary, providerFinished int) string {
	panic("not ported yet")
}

func completionUsage(cs []WaitCompletion) *Usage { panic("not ported yet") }

func waitResultOf(text string, isError bool, completions []WaitCompletion) WaitResult {
	panic("not ported yet")
}

func formatCompletionRecovery(cs []WaitCompletion) string { panic("not ported yet") }

func timeoutRecoveryMap(p *TimeoutRecoveryProjection) map[string]any { panic("not ported yet") }

func windowElapsedResult(text string, activeRunIDs []string, providers []RegisteredBackgroundWorkItem) WaitResult {
	panic("not ported yet")
}

func supervisorYieldResult(activeRunIDs []string, providers []RegisteredBackgroundWorkItem) WaitResult {
	panic("not ported yet")
}

type initialWaitScope struct {
	asyncRunIDs map[string]bool
	foreground  []initialForeground
	providerIDs map[string]bool
}

type initialForeground struct {
	runID, sessionID string
	detached         map[int]bool
}

func sortedBoolKeys(m map[string]bool) []string { panic("not ported yet") }

func supervisorYieldForScope(scope initialWaitScope, p WaitParams, deps WaitDeps, now int64) WaitResult {
	panic("not ported yet")
}

func asyncWaitUpdate(runs []AsyncRunSummary, providerCount int, elapsedMs int64) WaitResult {
	panic("not ported yet")
}

func activeDetachedForegroundRuns(p WaitParams, deps WaitDeps) []ForegroundRunRecord {
	panic("not ported yet")
}

func summarizeForegroundChildren(run ForegroundRunRecord, indices map[int]bool) string {
	panic("not ported yet")
}

func foregroundNeedingAttention(run ForegroundRunRecord, indices map[int]bool) []ForegroundChildRecord {
	panic("not ported yet")
}

func detachedIndices(run ForegroundRunRecord) map[int]bool { panic("not ported yet") }

func waitForDetachedForegroundRun(run ForegroundRunRecord, signal <-chan struct{}, deps WaitDeps, startedAt int64, now func() int64, pollMs, timeoutMs int64, supervisorYield func() WaitResult) WaitResult {
	panic("not ported yet")
}

func waitForSessionDetachedForegroundRuns(runs []ForegroundRunRecord, signal <-chan struct{}, deps WaitDeps, startedAt int64, now func() int64, pollMs, timeoutMs int64, supervisorYield func() WaitResult) WaitResult {
	panic("not ported yet")
}

func dedupe(in []string) []string { panic("not ported yet") }

// waitForSubagents is the bg_wait tool.
func waitForSubagents(p WaitParams, signal <-chan struct{}, deps WaitDeps) WaitResult {
	panic("not ported yet")
}

// ── auto-drain.ts ──

// AutoDrainDeps are the seams of the headless drain.
type AutoDrainDeps struct {
	State                       *SubagentState
	Events                      WaitEventBus
	AsyncDirRoot, ResultsDir    string
	NestedRootRunID             string
	TimeoutMs                   *int64
	Now                         func() int64
	Wait                        func(p WaitParams, signal <-chan struct{}, deps WaitDeps) WaitResult
	HasWork                     func(sessionID string, nowMs int64) (bool, error)
	HasPendingSupervisorRequest func() bool
}

func hasOutstandingWork(deps AutoDrainDeps, sessionID string, nowMs int64) (bool, error) {
	panic("not ported yet")
}

// drainOutstandingWork drains all work owned by the current headless session, including work added while draining.
func drainOutstandingWork(deps AutoDrainDeps) error { panic("not ported yet") }

// ── resume-guidance.ts (the parts that need no recovery descriptor reader are here; see formatAsyncReviveCommand) ──

const intercomDetachError = "detached for intercom coordination"

func isIntercomDetached(run AsyncRunSummary) bool { panic("not ported yet") }

func formatIntercomDetachGuidance(run AsyncRunSummary) string { panic("not ported yet") }

// formatAsyncReviveCommand is the resume call for a failed run with a persisted child session and a matching recovery descriptor.
func formatAsyncReviveCommand(run AsyncRunSummary) string { panic("not ported yet") }

func formatResumeFirstFailedRunsNote(runs []AsyncRunSummary) string { panic("not ported yet") }
