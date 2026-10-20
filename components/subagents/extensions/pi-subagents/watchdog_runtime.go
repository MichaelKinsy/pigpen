package pi_subagents

import (
	"sync"
)

// The main watchdog runtime (watchdog/runtime.ts): it collects the turn deltas of a session, decides when a review is due, runs
// the review with a deadline, and turns what the reviewer found into warnings (deduplicated, bounded, routed by importance).
//
// The original is single threaded: its state changes only between awaits. Here one mutex guards the state, it is released while
// the runtime waits (for the LSP pre-pass, for a review) and while it calls back into the host (callbacks run after the lock is
// released, in the order they were queued), so every interleaving is one the original's event loop could have produced.

const (
	wdMaxReviewInputChars   = 24_000
	wdReviewInputHeadChars  = 6_000
	wdReviewDeltaSeparator  = "\n\n---\n\n"
	wdActivityTailChars     = 6_000
	wdActivityEntryChars    = 3_000
	wdMaxChangedPathsListed = 200
	wdFailureErrorTextChars = 600
	wdClarificationQuestion = 1_000
	wdClarificationEvidence = 2_000
)

type watchdogClarification struct{ Question, Evidence string }

type watchdogReviewResult struct {
	Warnings []wdWarning
	// StopReason is "stop", "error", "aborted" or "length"; empty is "no stop reason".
	StopReason string
	// ErrorMessage is the provider error text of a failed review, surfaced in status `Last error`.
	ErrorMessage  string
	Clarification *watchdogClarification
}

type watchdogReviewRequest struct {
	Delta       string
	Epoch       int
	HasScope    bool
	ReviewID    int
	Config      *watchdogConfig
	EmitWarning func(w wdWarning) bool
	// AllowClarification offers the reviewer one bounded question.
	AllowClarification bool
	// Signal closes when the runtime abandons the review.
	Signal <-chan struct{}
}

// watchdogReviewFunc reviews one delta. A nil result is a review that said nothing (the original's void).
type watchdogReviewFunc func(req watchdogReviewRequest) (*watchdogReviewResult, error)

// wdSendOptions are the delivery options of a warning message: {deliverAs: "steer"} or {triggerTurn: false}.
type wdSendOptions struct {
	DeliverAs   string
	TriggerTurn *bool
}

type wdRunCtx struct {
	Cwd    string
	Signal <-chan struct{}
}

type watchdogRuntimeSnapshot struct {
	Status               string
	Enabled              bool
	Config               *watchdogConfig
	ConfigOK             bool
	Errors               []wdSettingsError
	Sources              []wdSettingsSource
	BufferedDeltas       int
	Epoch                int
	ActiveReviewID       int
	SessionOverride      *bool
	SessionModelOverride *wdSessionModel
	LastWarning          *wdWarning
	LastError            string
	FailedReviews        int
	StaleReviews         int
	ReviewConnected      bool
	ReviewDescription    string
	BoundaryRepeats      int
	Stalemate            bool
	ReviewTrigger        string
	ChangedPaths         []string
	Lsp                  wdLspSnapshot
}

// wdSessionModel is the session's model override: Model "" and an unset Thinking are not overridden.
type wdSessionModel struct {
	Model    string
	Thinking wdThinking
}

type wdSessionModelPatch struct {
	Model    wdPatch[string]
	Thinking wdPatch[wdThinking]
}

type watchdogRuntimeOptions struct {
	Cwd                  string
	ResolveConfig        func(cwd string, session *jsObject) watchdogSettingsResult
	Review               watchdogReviewFunc
	ReviewDescription    string
	DisplayWarning       func(w wdWarning, opts *wdSendOptions)
	DisplayUserWarning   func(w wdWarning)
	DisplayClarification func(content string)
	ReviewChangesOnly    bool
	LspDiagnostics       watchdogLspFunc
	RepoChangeSignature  func(cwd string) *watchdogRepoChangeSignature
}

type wdReviewOutcome struct {
	kind          string // "completed", "timeout", "stale" or "clarification"
	clarification *watchdogClarification
}

type wdWaiter struct{ ch chan bool }

type watchdogRuntime struct {
	mu      sync.Mutex
	effects []func()

	cwd                  string
	resolveConfig        func(cwd string, session *jsObject) watchdogSettingsResult
	review               watchdogReviewFunc
	reviewConnected      bool
	reviewDescription    string
	displayWarning       func(w wdWarning, opts *wdSendOptions)
	displayUserWarning   func(w wdWarning)
	displayClarification func(content string)
	reviewChangesOnly    bool
	lspDiagnostics       watchdogLspFunc
	repoChangeSignature  func(cwd string) *watchdogRepoChangeSignature
	lspLedger            *watchdogLspLedger
	scope                *watchdogScope

	configResult          watchdogSettingsResult
	sessionOverride       *bool
	sessionModelOverride  *wdSessionModel
	status                string
	pendingDeltas         []string
	pendingDeltaChars     int
	guard                 *watchdogEmissionGuard
	guardMaxWarnings      *int
	epoch                 int
	reviewIDCounter       int
	agentEndIDCounter     int
	activeAgentEndID      int
	activeAgentEndAbort   *wdAbort
	activeReviewID        int
	activeReviewWarning   *wdWarning
	reviewing             bool
	waitingAtAgentEnd     bool
	disposed              bool
	includePromptNext     bool
	userPrompt            string
	waiters               []*wdWaiter
	lastWarning           *wdWarning
	lastError             string
	lastReviewInputSig    string
	turnStartChangeSig    *watchdogRepoChangeSignature
	lastReviewedChangeSig string
	currentChangedPaths   []string
	lastLspSnapshot       *wdLspSnapshot
	observedRepoEdit      bool
	toolResultsThisRun    int
	midRunReviewing       bool
	lastBoundaryIdentity  string
	boundaryRepeats       int
	stalemate             bool
	ruleWarningsThisRun   map[string]bool
	midRunGeneration      int
	activeReviewAbort     *wdAbort
	failedReviews         int
	staleReviews          int
	askedThisPrompt       bool
	activityTail          string
	activityPending       bool
	activityReviewUsed    bool
}

func newWatchdogRuntime(o watchdogRuntimeOptions) *watchdogRuntime { panic("not implemented") }

func wdCwd() string { panic("not implemented") }

// boundWatchdogReviewText keeps the head and the tail of a text over the cap, with a marker for what is left out.
func boundWatchdogReviewText(text string, cap int) string { panic("not implemented") }

// wdBoundReview is boundWatchdogReviewText at the default cap.
func wdBoundReview(text string) string { panic("not implemented") }

func wdPromptFromBeforeAgentStart(event any) (string, bool) { panic("not implemented") }

// ── locking ──

func (r *watchdogRuntime) lock() { panic("not implemented") }

func (r *watchdogRuntime) unlock() { panic("not implemented") }

// unlocked runs fn without the lock (after the queued callbacks ran) and takes the lock back.
func (r *watchdogRuntime) unlocked(fn func()) { panic("not implemented") }

func (r *watchdogRuntime) later(fn func()) { panic("not implemented") }

// ── session ──

func (r *watchdogRuntime) bindSession(ctx wdRunCtx) { panic("not implemented") }

func (r *watchdogRuntime) sessionObject() *jsObject { panic("not implemented") }

func (r *watchdogRuntime) refreshConfig(cwd string) watchdogSettingsResult { panic("not implemented") }

func (r *watchdogRuntime) refreshConfigLocked(cwd string) watchdogSettingsResult {
	panic("not implemented")
}

func wdSameIntPtr(a, b *int) bool { panic("not implemented") }

func (r *watchdogRuntime) setSessionEnabled(enabled bool, cwd string) watchdogRuntimeSnapshot {
	panic("not implemented")
}

func (r *watchdogRuntime) setSessionModel(patch wdSessionModelPatch, cwd string) watchdogRuntimeSnapshot {
	panic("not implemented")
}

func (r *watchdogRuntime) clearSessionModel(cwd string) watchdogRuntimeSnapshot {
	panic("not implemented")
}

func (r *watchdogRuntime) clearSessionOverride(cwd string) watchdogRuntimeSnapshot {
	panic("not implemented")
}

type wdResetOptions struct {
	clearReviewInputSignature, resetChangeSignature, clearLspLedger, clearScope, clearActivity bool
}

func (r *watchdogRuntime) reset(opts wdResetOptions) { panic("not implemented") }

func (r *watchdogRuntime) resetLocked(opts wdResetOptions) { panic("not implemented") }

func (r *watchdogRuntime) dispose() { panic("not implemented") }

// ── events ──

func (r *watchdogRuntime) handleBeforeAgentStart(event any, ctx wdRunCtx) { panic("not implemented") }

func (r *watchdogRuntime) handleTurnEnd(event any, ctx wdRunCtx, structuredTerminal bool) {
	panic("not implemented")
}

func (r *watchdogRuntime) enqueueDelta(delta string) { panic("not implemented") }

func (r *watchdogRuntime) enqueueDeltaLocked(delta string) { panic("not implemented") }

func (r *watchdogRuntime) handleToolResult(ctx wdRunCtx) { panic("not implemented") }

// startMidRunReview is `void this.reviewMidRunDelta(delta)`: the claim is made now, the wait runs on its own goroutine.
func (r *watchdogRuntime) startMidRunReview(delta string) { panic("not implemented") }

func (r *watchdogRuntime) handleAgentEnd(event any, ctx wdRunCtx) { panic("not implemented") }

// A question consumes this evidence just like a completed review, not a pending exchange.

func (r *watchdogRuntime) displayRuleWarning(v watchdogRuleViolation) { panic("not implemented") }

func (r *watchdogRuntime) displayRecordedWarning(w wdWarning) { panic("not implemented") }

func (r *watchdogRuntime) getSnapshot(cwd string) watchdogRuntimeSnapshot { panic("not implemented") }

func (r *watchdogRuntime) snapshotLocked() watchdogRuntimeSnapshot { panic("not implemented") }

// handleUserInput: a real mid-stream user input can steer without emitting before_agent_start.
func (r *watchdogRuntime) handleUserInput() { panic("not implemented") }

func (r *watchdogRuntime) handleModelChange() { panic("not implemented") }

// waitForIdle waits until no review is running and no delta is pending, or the timeout passes.
func (r *watchdogRuntime) waitForIdle(timeoutMs int) bool { panic("not implemented") }

// ── internals (the lock is held) ──

func (r *watchdogRuntime) isEnabled() bool { panic("not implemented") }

func (r *watchdogRuntime) abortActiveAgentEnd() { panic("not implemented") }

func (r *watchdogRuntime) isAgentEndCurrent(epoch, id int) bool { panic("not implemented") }

func (r *watchdogRuntime) isCurrent(epoch, id int) bool { panic("not implemented") }

func (r *watchdogRuntime) warningMeetsThreshold(w wdWarning) bool { panic("not implemented") }

func (r *watchdogRuntime) routeWarning(details wdWarning, opts *wdSendOptions) {
	panic("not implemented")
}

func (r *watchdogRuntime) acceptWarning(epoch, id int, w wdWarning) bool { panic("not implemented") }

func (r *watchdogRuntime) displayBoundaryWarning(w wdWarning) bool { panic("not implemented") }

func (r *watchdogRuntime) invalidateActiveReview() { panic("not implemented") }

// cancelMidRunReview: the agent-end boundary review is authoritative; an in-flight cadence review is superseded.
func (r *watchdogRuntime) cancelMidRunReview() { panic("not implemented") }

type wdReviewDone struct {
	result *watchdogReviewResult
	err    error
}

// reviewDelta runs one review with a deadline. The lock is held on entry and on return, and released while the review runs.
func (r *watchdogRuntime) reviewDelta(delta string, timeoutMs int, allowClarificationOpt, correction bool) (outcome wdReviewOutcome) {
	panic("not implemented")
}

func (r *watchdogRuntime) displayAcceptedReviewWarning(correction bool) { panic("not implemented") }

// deliverBoundaryWarning: a displayed boundary warning continues the run; the same identity back from consecutive boundaries
// means no progress, so it is shown held instead of continuing again.
func (r *watchdogRuntime) deliverBoundaryWarning(details wdWarning) { panic("not implemented") }

// repeatableBoundaryIdentity: the previous boundary finding is a repeat to count, not a duplicate, until stalemate.
func (r *watchdogRuntime) repeatableBoundaryIdentity() string { panic("not implemented") }

func (r *watchdogRuntime) resetBoundaryRepeats() { panic("not implemented") }

func (r *watchdogRuntime) currentRepoChangeSignature(cwd string) *watchdogRepoChangeSignature {
	panic("not implemented")
}

func (r *watchdogRuntime) resetRepoChangeBaseline(cwd string, reviewed bool) {
	panic("not implemented")
}

func (r *watchdogRuntime) resolveReviewChangeSignature(current *watchdogRepoChangeSignature, cwd string) *watchdogRepoChangeSignature {
	panic("not implemented")
}

// collectLspDiagnostics runs the LSP pre-pass of a boundary review; the lock is released while the language server runs.
func (r *watchdogRuntime) collectLspDiagnostics(sig *watchdogRepoChangeSignature, epoch, agentEndID int, signal <-chan struct{}) string {
	panic("not implemented")
}

func (r *watchdogRuntime) lspSnapshot() wdLspSnapshot { panic("not implemented") }

func (r *watchdogRuntime) appendBoundedDelta(delta string) { panic("not implemented") }

func (r *watchdogRuntime) buildReviewInput(sig *watchdogRepoChangeSignature, lspBlock string) string {
	panic("not implemented")
}

func (r *watchdogRuntime) scopeBlock() string { panic("not implemented") }

func (r *watchdogRuntime) clearPendingDeltas() { panic("not implemented") }

func (r *watchdogRuntime) clearActivity() { panic("not implemented") }

func (r *watchdogRuntime) fail(message string) { panic("not implemented") }

func (r *watchdogRuntime) markLastWarningStale() { panic("not implemented") }

func (r *watchdogRuntime) isSettled() bool { panic("not implemented") }

func (r *watchdogRuntime) resolveWaiters(settled bool) { panic("not implemented") }
