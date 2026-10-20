package pi_subagents

import (
	"os"
	"strings"
	"sync"
	"time"
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

func newWatchdogRuntime(o watchdogRuntimeOptions) *watchdogRuntime {
	r := &watchdogRuntime{
		cwd: o.Cwd, resolveConfig: o.ResolveConfig, review: o.Review, reviewConnected: o.Review != nil, reviewDescription: o.ReviewDescription,
		displayWarning: o.DisplayWarning, displayUserWarning: o.DisplayUserWarning, displayClarification: o.DisplayClarification,
		reviewChangesOnly: o.ReviewChangesOnly, lspDiagnostics: o.LspDiagnostics, repoChangeSignature: o.RepoChangeSignature,
		lspLedger: newWatchdogLspLedger(), scope: &watchdogScope{}, status: "idle", ruleWarningsThisRun: map[string]bool{},
	}
	if r.cwd == "" {
		r.cwd = wdCwd()
	}
	if r.resolveConfig == nil {
		r.resolveConfig = resolveWatchdogConfig
	}
	if r.review == nil {
		r.review = func(watchdogReviewRequest) (*watchdogReviewResult, error) { return &watchdogReviewResult{}, nil }
	}
	if r.reviewDescription == "" {
		if o.Review != nil {
			r.reviewDescription = "injected seam"
		} else {
			r.reviewDescription = "not wired"
		}
	}
	if r.lspDiagnostics == nil {
		r.lspDiagnostics = collectWatchdogLspDiagnostics
	}
	if r.repoChangeSignature == nil {
		r.repoChangeSignature = computeWatchdogRepoChangeSignature
	}
	r.configResult = r.resolveConfig(r.cwd, nil)
	r.guardMaxWarnings = r.configResult.Config.MaxWarnings
	r.guard = newWatchdogEmissionGuard(r.guardMaxWarnings)
	r.turnStartChangeSig = r.currentRepoChangeSignature(r.cwd)
	if r.turnStartChangeSig != nil {
		r.lastReviewedChangeSig = r.turnStartChangeSig.Key
	}
	return r
}

func wdCwd() string {
	d, _ := os.Getwd()
	return d
}

// boundWatchdogReviewText keeps the head and the tail of a text over the cap, with a marker for what is left out.
func boundWatchdogReviewText(text string, cap int) string {
	n := wdLen(text)
	if n <= cap {
		return text
	}
	head := wdReviewInputHeadChars
	if cap/4 < head {
		head = cap / 4
	}
	marker := "\n\n[... about " + itoa(n-cap) + " characters omitted ...]\n\n"
	return wdSlice(text, 0, head) + marker + wdSliceFrom(text, n-(cap-head-wdLen(marker)))
}

// wdBoundReview is boundWatchdogReviewText at the default cap.
func wdBoundReview(text string) string { return boundWatchdogReviewText(text, wdMaxReviewInputChars) }

func wdPromptFromBeforeAgentStart(event any) (string, bool) {
	if s, ok := wdPropV(event, "prompt").(string); ok {
		return s, true
	}
	if s, ok := wdPropV(event, "systemPrompt").(string); ok {
		return s, true
	}
	return "", false
}

// ── locking ──

func (r *watchdogRuntime) lock() { r.mu.Lock() }

func (r *watchdogRuntime) unlock() {
	effects := r.effects
	r.effects = nil
	r.mu.Unlock()
	for _, e := range effects {
		e()
	}
}

// unlocked runs fn without the lock (after the queued callbacks ran) and takes the lock back.
func (r *watchdogRuntime) unlocked(fn func()) {
	r.unlock()
	fn()
	r.mu.Lock()
}

func (r *watchdogRuntime) later(fn func()) { r.effects = append(r.effects, fn) }

// ── session ──

func (r *watchdogRuntime) bindSession(ctx wdRunCtx) {
	r.lock()
	defer r.unlock()
	r.cwd = ctx.Cwd
	r.sessionOverride = nil
	r.sessionModelOverride = nil
	r.refreshConfigLocked(ctx.Cwd)
	r.resetLocked(wdResetOptions{clearReviewInputSignature: true, resetChangeSignature: true, clearLspLedger: true, clearScope: true})
	r.resetBoundaryRepeats()
}

func (r *watchdogRuntime) sessionObject() *jsObject {
	if r.sessionOverride == nil && r.sessionModelOverride == nil {
		return nil
	}
	main := newObject()
	session := newObject()
	if r.sessionOverride != nil {
		session.set("enabled", *r.sessionOverride)
		main.set("enabled", *r.sessionOverride)
	}
	if m := r.sessionModelOverride; m != nil {
		if m.Model != "" {
			main.set("model", m.Model)
		}
		if m.Thinking.Off {
			main.set("thinking", false)
		} else if m.Thinking.Level != "" {
			main.set("thinking", m.Thinking.Level)
		}
	}
	session.set("main", main)
	return session
}

func (r *watchdogRuntime) refreshConfig(cwd string) watchdogSettingsResult {
	r.lock()
	defer r.unlock()
	return r.refreshConfigLocked(cwd)
}

func (r *watchdogRuntime) refreshConfigLocked(cwd string) watchdogSettingsResult {
	if cwd != "" {
		r.cwd = cwd
	}
	wasEnabled := r.isEnabled()
	hadClarification := r.configResult.Config.Clarification
	previousMain := r.configResult.Config.Main
	r.configResult = r.resolveConfig(r.cwd, r.sessionObject())
	config := r.configResult.Config
	if !wdSameIntPtr(config.MaxWarnings, r.guardMaxWarnings) {
		r.guardMaxWarnings = config.MaxWarnings
		r.guard = newWatchdogEmissionGuard(r.guardMaxWarnings)
	}
	if wasEnabled && !r.isEnabled() {
		r.invalidateActiveReview()
	}
	if !r.isEnabled() || !config.Clarification {
		r.clearActivity()
	}
	if r.displayClarification != nil && (hadClarification || config.Clarification) && (r.reviewing || r.waitingAtAgentEnd) {
		if !config.Clarification || previousMain.Model != config.Main.Model || previousMain.Thinking != config.Main.Thinking {
			r.invalidateActiveReview()
		}
	}
	return r.configResult
}

func wdSameIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (r *watchdogRuntime) setSessionEnabled(enabled bool, cwd string) watchdogRuntimeSnapshot {
	r.lock()
	defer r.unlock()
	if cwd == "" {
		cwd = r.cwd
	}
	r.sessionOverride = &enabled
	r.resetLocked(wdResetOptions{})
	r.refreshConfigLocked(cwd)
	return r.snapshotLocked()
}

func (r *watchdogRuntime) setSessionModel(patch wdSessionModelPatch, cwd string) watchdogRuntimeSnapshot {
	r.lock()
	defer r.unlock()
	if cwd == "" {
		cwd = r.cwd
	}
	next := wdSessionModel{}
	if r.sessionModelOverride != nil {
		next = *r.sessionModelOverride
	}
	switch patch.Model.mode {
	case 2:
		next.Model = ""
	case 1:
		next.Model = patch.Model.Value
	}
	switch patch.Thinking.mode {
	case 2:
		next.Thinking = wdThinking{}
	case 1:
		next.Thinking = patch.Thinking.Value
	}
	if next.Model == "" && !next.Thinking.set() {
		r.sessionModelOverride = nil
	} else {
		r.sessionModelOverride = &next
	}
	r.resetLocked(wdResetOptions{})
	r.refreshConfigLocked(cwd)
	return r.snapshotLocked()
}

func (r *watchdogRuntime) clearSessionModel(cwd string) watchdogRuntimeSnapshot {
	r.lock()
	defer r.unlock()
	if cwd == "" {
		cwd = r.cwd
	}
	r.sessionModelOverride = nil
	r.resetLocked(wdResetOptions{})
	r.refreshConfigLocked(cwd)
	return r.snapshotLocked()
}

func (r *watchdogRuntime) clearSessionOverride(cwd string) watchdogRuntimeSnapshot {
	r.lock()
	defer r.unlock()
	if cwd == "" {
		cwd = r.cwd
	}
	r.sessionOverride = nil
	r.sessionModelOverride = nil
	r.resetLocked(wdResetOptions{})
	r.refreshConfigLocked(cwd)
	return r.snapshotLocked()
}

type wdResetOptions struct {
	clearReviewInputSignature, resetChangeSignature, clearLspLedger, clearScope, clearActivity bool
}

func (r *watchdogRuntime) reset(opts wdResetOptions) {
	r.lock()
	defer r.unlock()
	r.resetLocked(opts)
}

func (r *watchdogRuntime) resetLocked(opts wdResetOptions) {
	if r.activeReviewAbort != nil {
		r.activeReviewAbort.abort()
	}
	r.abortActiveAgentEnd()
	r.epoch++
	r.status = "idle"
	r.clearPendingDeltas()
	r.reviewing = false
	r.waitingAtAgentEnd = false
	r.activeReviewID = 0
	r.activeReviewWarning = nil
	r.includePromptNext = false
	r.userPrompt = ""
	r.lastError = ""
	r.currentChangedPaths = nil
	r.observedRepoEdit = false
	r.toolResultsThisRun = 0
	r.midRunReviewing = false
	r.ruleWarningsThisRun = map[string]bool{}
	if opts.clearLspLedger {
		r.lspLedger.reset()
		r.lastLspSnapshot = nil
	}
	if opts.clearScope {
		r.scope.reset()
	}
	if opts.clearScope || opts.clearActivity {
		r.clearActivity()
	}
	if opts.clearReviewInputSignature {
		r.lastReviewInputSig = ""
	}
	if opts.resetChangeSignature {
		r.resetRepoChangeBaseline("", true)
	}
	r.guard.reset()
	r.resolveWaiters(true)
}

func (r *watchdogRuntime) dispose() {
	r.lock()
	defer r.unlock()
	r.clearActivity()
	if r.activeReviewAbort != nil {
		r.activeReviewAbort.abort()
	}
	r.disposed = true
	r.abortActiveAgentEnd()
	r.epoch++
	r.status = "idle"
	r.clearPendingDeltas()
	r.reviewing = false
	r.waitingAtAgentEnd = false
	r.activeReviewID = 0
	r.activeReviewWarning = nil
	r.lastReviewInputSig = ""
	r.currentChangedPaths = nil
	r.lastLspSnapshot = nil
	r.lspLedger.reset()
	r.scope.reset()
	r.observedRepoEdit = false
	r.toolResultsThisRun = 0
	r.midRunReviewing = false
	r.resolveWaiters(false)
}

// ── events ──

func (r *watchdogRuntime) handleBeforeAgentStart(event any, ctx wdRunCtx) {
	r.lock()
	defer r.unlock()
	if r.disposed {
		return
	}
	incoming, _ := wdPromptFromBeforeAgentStart(event)
	r.resetLocked(wdResetOptions{})
	r.askedThisPrompt = false
	r.activityReviewUsed = false
	r.resetBoundaryRepeats()
	r.refreshConfigLocked(ctx.Cwd)
	r.userPrompt = incoming
	if jsTrim(r.userPrompt) != "" {
		r.includePromptNext = true
		r.scope.addPrompt(r.userPrompt, "")
	} else {
		r.includePromptNext = false
	}
	r.resetRepoChangeBaseline("", false)
}

func (r *watchdogRuntime) handleTurnEnd(event any, ctx wdRunCtx, structuredTerminal bool) {
	r.lock()
	defer r.unlock()
	if r.disposed {
		return
	}
	r.refreshConfigLocked(ctx.Cwd)
	if !r.isEnabled() {
		return
	}
	if r.displayClarification != nil && r.configResult.Config.Clarification && r.boundaryRepeats == 0 {
		if activity := formatWatchdogOrchestrationActivity(event); activity != "" {
			tail := activity
			if wdLen(tail) > 0 {
				tail = boundWatchdogReviewText(activity, wdActivityEntryChars)
			}
			joined := tail
			if r.activityTail != "" {
				joined = r.activityTail + wdReviewDeltaSeparator + tail
			}
			if n := wdLen(joined); n > wdActivityTailChars {
				joined = wdSliceFrom(joined, n-wdActivityTailChars)
			}
			r.activityTail = joined
			r.activityPending = true
		}
	}
	r.observedRepoEdit = r.observedRepoEdit || eventIndicatesRepoEdit(event)
	delta := formatWatchdogTurnDelta(watchdogTurnDeltaInput{IncludeUserPrompt: r.includePromptNext, UserPrompt: r.userPrompt, Events: []any{event}, StructuredTerminal: structuredTerminal})
	r.includePromptNext = false
	r.enqueueDeltaLocked(delta)
}

func (r *watchdogRuntime) enqueueDelta(delta string) {
	r.lock()
	defer r.unlock()
	r.enqueueDeltaLocked(delta)
}

func (r *watchdogRuntime) enqueueDeltaLocked(delta string) {
	if r.disposed || jsTrim(delta) == "" || !r.isEnabled() {
		return
	}
	r.appendBoundedDelta(delta)
	if !r.reviewing && !r.waitingAtAgentEnd && !r.midRunReviewing {
		r.status = "queued"
	}
}

func (r *watchdogRuntime) handleToolResult(ctx wdRunCtx) {
	r.lock()
	defer r.unlock()
	if r.disposed {
		return
	}
	r.refreshConfigLocked(ctx.Cwd)
	if !r.isEnabled() {
		return
	}
	every := r.configResult.Config.Cadence.EveryNTools
	if every == nil {
		return
	}
	r.toolResultsThisRun++
	if r.toolResultsThisRun%*every != 0 {
		return
	}
	if r.reviewing || r.waitingAtAgentEnd || r.midRunReviewing {
		return
	}
	delta := r.buildReviewInput(nil, "")
	if jsTrim(delta) == "" {
		return
	}
	r.startMidRunReview(delta)
}

// startMidRunReview is `void this.reviewMidRunDelta(delta)`: the claim is made now, the wait runs on its own goroutine.
func (r *watchdogRuntime) startMidRunReview(delta string) {
	if r.midRunReviewing || r.reviewing || r.waitingAtAgentEnd || r.disposed {
		return
	}
	r.midRunReviewing = true
	generation := r.midRunGeneration
	go func() {
		r.lock()
		defer r.unlock()
		outcome := r.reviewDelta(delta, r.configResult.Config.AgentEndTimeoutMs, false, true)
		if generation != r.midRunGeneration {
			return
		}
		if outcome.kind == "timeout" {
			r.staleReviews++
			r.status = "stale"
			r.markLastWarningStale()
		}
		r.midRunReviewing = false
		if r.status == "reviewing" {
			if len(r.pendingDeltas) > 0 {
				r.status = "queued"
			} else {
				r.status = "idle"
			}
		}
		r.resolveWaiters(r.isSettled())
	}()
}

func (r *watchdogRuntime) handleAgentEnd(event any, ctx wdRunCtx) {
	r.lock()
	defer r.unlock()
	if r.disposed {
		return
	}
	r.refreshConfigLocked(ctx.Cwd)
	if !r.isEnabled() || wdClosed(ctx.Signal) {
		return
	}
	changeSignature := r.resolveReviewChangeSignature(r.currentRepoChangeSignature(ctx.Cwd), ctx.Cwd)
	config := r.configResult.Config
	activityReview := r.displayClarification != nil && config.Clarification && !r.activityReviewUsed && r.boundaryRepeats == 0 && r.activityPending
	knownEvidence := changeSignature != nil && changeSignature.Key == r.lastReviewedChangeSig
	if !activityReview && (knownEvidence || (r.reviewChangesOnly && changeSignature == nil)) {
		r.clearPendingDeltas()
		if knownEvidence || r.status == "queued" {
			r.status = "idle"
		}
		r.resolveWaiters(true)
		return
	}
	r.cancelMidRunReview()
	if activityReview && (changeSignature == nil || knownEvidence) {
		r.activityReviewUsed = true
	}
	r.activityPending = false
	r.waitingAtAgentEnd = true
	agentEndEpoch := r.epoch
	r.agentEndIDCounter++
	agentEndID := r.agentEndIDCounter
	lspAbort := newWdAbort()
	r.activeAgentEndID = agentEndID
	r.activeAgentEndAbort = lspAbort
	defer func() {
		if r.activeAgentEndAbort == lspAbort {
			r.activeAgentEndAbort = nil
		}
		if r.activeAgentEndID == agentEndID {
			r.activeAgentEndID = 0
		}
	}()
	r.guard.startModelUpdate()
	lspBlock := r.collectLspDiagnostics(changeSignature, agentEndEpoch, agentEndID, lspAbort.signal())
	if r.activeAgentEndAbort == lspAbort {
		r.activeAgentEndAbort = nil
	}
	if !r.isAgentEndCurrent(agentEndEpoch, agentEndID) {
		return
	}
	delta := r.buildReviewInput(changeSignature, lspBlock)
	r.clearPendingDeltas()
	if jsTrim(delta) == "" {
		r.waitingAtAgentEnd = false
		if r.status == "queued" {
			r.status = "idle"
		}
		r.resolveWaiters(true)
		return
	}
	signature := wdSHA256(delta)
	if !r.reviewChangesOnly && signature == r.lastReviewInputSig {
		r.waitingAtAgentEnd = false
		r.status = "idle"
		r.resolveWaiters(true)
		return
	}
	outcome := r.reviewDelta(delta, config.AgentEndTimeoutMs, true, false)
	if !r.isAgentEndCurrent(agentEndEpoch, agentEndID) {
		return
	}
	r.waitingAtAgentEnd = false
	if outcome.kind == "timeout" {
		r.staleReviews++
		r.invalidateActiveReview()
		r.status = "stale"
		r.markLastWarningStale()
		r.resolveWaiters(true)
		return
	}
	if (outcome.kind == "completed" || outcome.kind == "clarification") && r.status != "failed" && r.status != "stale" {
		// A question consumes this evidence just like a completed review, not a pending exchange.
		r.lastReviewInputSig = signature
		if changeSignature != nil {
			r.lastReviewedChangeSig = changeSignature.Key
		}
		if changeSignature != nil {
			r.currentChangedPaths = changeSignature.ChangedPaths
		} else {
			r.currentChangedPaths = nil
		}
		r.status = "idle"
		if outcome.kind == "clarification" && !wdClosed(ctx.Signal) && r.displayClarification != nil {
			display := r.displayClarification
			content := strings.Join([]string{
				"Main watchdog clarification:", outcome.clarification.Question, "Evidence: " + outcome.clarification.Evidence,
				"Consider this missing context as you continue the task. This is not approval, permission, or a warning. The reviewer has yielded; no reply or follow-up review is required.",
			}, "\n")
			r.later(func() { display(content) })
		}
	}
	r.resolveWaiters(true)
}

func (r *watchdogRuntime) displayRuleWarning(v watchdogRuleViolation) {
	r.lock()
	defer r.unlock()
	if r.disposed || r.ruleWarningsThisRun[v.Summary] {
		return
	}
	r.ruleWarningsThisRun[v.Summary] = true
	details := normalizeWatchdogWarningDetails(ruleViolationWarning(v), wdWarning{State: "displayed", DisplayedAt: isoNow()})
	r.lastWarning = &details
	r.routeWarning(details, &wdSendOptions{DeliverAs: "steer"})
}

func (r *watchdogRuntime) displayRecordedWarning(w wdWarning) {
	r.lock()
	defer r.unlock()
	source := w.Source
	if source == "" {
		source = "main"
	}
	details := normalizeWatchdogWarningDetails(w, wdWarning{State: "displayed", Source: source})
	r.lastWarning = &details
	r.routeWarning(details, nil)
}

func (r *watchdogRuntime) getSnapshot(cwd string) watchdogRuntimeSnapshot {
	r.lock()
	defer r.unlock()
	if cwd != "" {
		r.refreshConfigLocked(cwd)
	}
	return r.snapshotLocked()
}

func (r *watchdogRuntime) snapshotLocked() watchdogRuntimeSnapshot {
	s := watchdogRuntimeSnapshot{
		Status: r.status, Enabled: r.isEnabled(), Config: r.configResult.Config, ConfigOK: r.configResult.OK,
		Errors: append([]wdSettingsError{}, r.configResult.Errors...), Sources: append([]wdSettingsSource{}, r.configResult.Sources...),
		BufferedDeltas: len(r.pendingDeltas), Epoch: r.epoch, ActiveReviewID: r.activeReviewID, LastError: r.lastError,
		FailedReviews: r.failedReviews, StaleReviews: r.staleReviews, ReviewConnected: r.reviewConnected, ReviewDescription: r.reviewDescription,
		BoundaryRepeats: r.boundaryRepeats, Stalemate: r.stalemate, ReviewTrigger: "turn-delta", Lsp: r.lspSnapshot(),
	}
	if r.reviewChangesOnly {
		s.ReviewTrigger = "repo-edits"
	}
	if r.sessionOverride != nil {
		v := *r.sessionOverride
		s.SessionOverride = &v
	}
	if r.sessionModelOverride != nil {
		v := *r.sessionModelOverride
		s.SessionModelOverride = &v
	}
	if r.lastWarning != nil {
		v := *r.lastWarning
		s.LastWarning = &v
	}
	if len(r.currentChangedPaths) > 0 {
		s.ChangedPaths = append([]string{}, r.currentChangedPaths...)
	}
	return s
}

// handleUserInput: a real mid-stream user input can steer without emitting before_agent_start.
func (r *watchdogRuntime) handleUserInput() {
	r.lock()
	defer r.unlock()
	if r.displayClarification != nil && r.configResult.Config.Clarification && (r.reviewing || r.waitingAtAgentEnd) {
		r.resetLocked(wdResetOptions{})
	}
}

func (r *watchdogRuntime) handleModelChange() { r.handleUserInput() }

// waitForIdle waits until no review is running and no delta is pending, or the timeout passes.
func (r *watchdogRuntime) waitForIdle(timeoutMs int) bool {
	r.lock()
	if r.isSettled() {
		r.unlock()
		return true
	}
	w := &wdWaiter{ch: make(chan bool, 1)}
	r.waiters = append(r.waiters, w)
	r.unlock()
	select {
	case settled := <-w.ch:
		return settled
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		r.lock()
		defer r.unlock()
		for i, x := range r.waiters {
			if x == w {
				r.waiters = append(r.waiters[:i:i], r.waiters[i+1:]...)
				break
			}
		}
		return false
	}
}

// ── internals (the lock is held) ──

func (r *watchdogRuntime) isEnabled() bool {
	return r.configResult.OK && r.configResult.Config.Main.Enabled
}

func (r *watchdogRuntime) abortActiveAgentEnd() {
	if r.activeAgentEndAbort != nil {
		r.activeAgentEndAbort.abort()
	}
	r.activeAgentEndAbort = nil
	r.activeAgentEndID = 0
}

func (r *watchdogRuntime) isAgentEndCurrent(epoch, id int) bool {
	return !r.disposed && r.epoch == epoch && r.activeAgentEndID == id && r.waitingAtAgentEnd && r.isEnabled()
}

func (r *watchdogRuntime) isCurrent(epoch, id int) bool {
	return !r.disposed && r.epoch == epoch && r.activeReviewID == id
}

func (r *watchdogRuntime) warningMeetsThreshold(w wdWarning) bool {
	return wdContains(watchdogWarningImportance, w.Importance) && (r.configResult.Config.SeverityThreshold == "concern" || w.Severity == "blocker")
}

func (r *watchdogRuntime) routeWarning(details wdWarning, opts *wdSendOptions) {
	if details.Importance == "high" {
		if r.displayWarning != nil {
			display := r.displayWarning
			r.later(func() { display(details, opts) })
		}
	} else if r.displayUserWarning != nil {
		display := r.displayUserWarning
		r.later(func() { display(details) })
	}
}

func (r *watchdogRuntime) acceptWarning(epoch, id int, w wdWarning) bool {
	if !r.isCurrent(epoch, id) || !r.isEnabled() || !r.warningMeetsThreshold(w) {
		return false
	}
	decision := r.guard.evaluate(w, r.repeatableBoundaryIdentity())
	if !decision.Accepted {
		return false
	}
	source := w.Source
	if source == "" {
		source = "main"
	}
	details := normalizeWatchdogWarningDetails(w, wdWarning{State: "candidate", Source: source, Identity: decision.Identity})
	r.lastWarning = &details
	r.activeReviewWarning = &details
	return true
}

func (r *watchdogRuntime) displayBoundaryWarning(w wdWarning) bool {
	if !r.isEnabled() || !r.warningMeetsThreshold(w) {
		return false
	}
	decision := r.guard.evaluate(w, r.repeatableBoundaryIdentity())
	if !decision.Accepted {
		return false
	}
	source := w.Source
	if source == "" {
		source = "main"
	}
	details := normalizeWatchdogWarningDetails(w, wdWarning{State: "displayed", Source: source, Identity: decision.Identity, DisplayedAt: isoNow()})
	r.deliverBoundaryWarning(details)
	return true
}

func (r *watchdogRuntime) invalidateActiveReview() {
	if r.activeReviewAbort != nil {
		r.activeReviewAbort.abort()
	}
	r.abortActiveAgentEnd()
	r.epoch++
	r.status = "idle"
	r.clearPendingDeltas()
	r.reviewing = false
	r.waitingAtAgentEnd = false
	r.activeReviewID = 0
	r.activeReviewWarning = nil
}

// cancelMidRunReview: the agent-end boundary review is authoritative; an in-flight cadence review is superseded.
func (r *watchdogRuntime) cancelMidRunReview() {
	if !r.midRunReviewing {
		return
	}
	r.midRunGeneration++
	if r.activeReviewAbort != nil {
		r.activeReviewAbort.abort()
	}
	r.activeReviewAbort = nil
	r.staleReviews++
	r.reviewing = false
	r.midRunReviewing = false
	r.activeReviewID = 0
	r.activeReviewWarning = nil
}

type wdReviewDone struct {
	result *watchdogReviewResult
	err    error
}

// reviewDelta runs one review with a deadline. The lock is held on entry and on return, and released while the review runs.
func (r *watchdogRuntime) reviewDelta(delta string, timeoutMs int, allowClarificationOpt, correction bool) (outcome wdReviewOutcome) {
	if r.reviewing || r.disposed {
		return wdReviewOutcome{kind: "stale"}
	}
	r.reviewing = true
	reviewEpoch := r.epoch
	r.reviewIDCounter++
	reviewID := r.reviewIDCounter
	r.activeReviewID = reviewID
	r.activeReviewWarning = nil
	r.status = "reviewing"
	abort := newWdAbort()
	r.activeReviewAbort = abort
	config := r.configResult.Config
	allowClarification := allowClarificationOpt && r.displayClarification != nil && config.Clarification && !r.askedThisPrompt && !r.stalemate
	req := watchdogReviewRequest{
		Delta: delta, Epoch: reviewEpoch, ReviewID: reviewID, Config: config, HasScope: jsTrim(r.scopeBlock()) != "", Signal: abort.signal(),
		AllowClarification: allowClarification,
		EmitWarning: func(w wdWarning) bool {
			r.lock()
			defer r.unlock()
			return r.acceptWarning(reviewEpoch, reviewID, w)
		},
	}
	done := make(chan wdReviewDone, 1)
	review := r.review
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- wdReviewDone{err: wdPanicError(p)}
			}
		}()
		res, err := review(req)
		done <- wdReviewDone{result: res, err: err}
	}()
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer func() {
		timer.Stop()
		if r.activeReviewAbort == abort {
			r.activeReviewAbort = nil
		}
		if r.epoch == reviewEpoch && r.activeReviewID == reviewID {
			r.reviewing = false
			r.activeReviewID = 0
			r.activeReviewWarning = nil
		}
		r.resolveWaiters(r.isSettled())
	}()
	var rd wdReviewDone
	timedOut := false
	r.unlocked(func() {
		select {
		case rd = <-done:
		case <-timer.C:
			timedOut = true
		}
	})
	if timedOut {
		abort.abort()
		return wdReviewOutcome{kind: "timeout"}
	}
	if rd.err != nil {
		if r.isCurrent(reviewEpoch, reviewID) {
			r.fail("Watchdog review failed: " + rd.err.Error())
			return wdReviewOutcome{kind: "completed"}
		}
		return wdReviewOutcome{kind: "stale"}
	}
	if !r.isCurrent(reviewEpoch, reviewID) || rd.result == nil {
		return wdReviewOutcome{kind: "stale"}
	}
	result := rd.result
	if allowClarification && config.Clarification && result.Clarification != nil && (result.StopReason == "" || result.StopReason == "stop") &&
		r.activeReviewWarning == nil && len(result.Warnings) == 0 && !wdClosed(abort.signal()) {
		question, evidence := jsTrim(result.Clarification.Question), jsTrim(result.Clarification.Evidence)
		if question != "" && evidence != "" {
			r.askedThisPrompt = true
			return wdReviewOutcome{kind: "clarification", clarification: &watchdogClarification{
				Question: boundWatchdogReviewText(question, wdClarificationQuestion), Evidence: boundWatchdogReviewText(evidence, wdClarificationEvidence)}}
		}
	}
	for _, w := range result.Warnings {
		r.acceptWarning(reviewEpoch, reviewID, w)
	}
	if result.StopReason != "" && result.StopReason != "stop" {
		detail := ""
		if t := jsTrim(result.ErrorMessage); t != "" {
			detail = " " + boundWatchdogReviewText(t, wdFailureErrorTextChars)
		}
		r.fail("Watchdog review ended with stop reason '" + result.StopReason + "'." + detail)
		return wdReviewOutcome{kind: "completed"}
	}
	r.displayAcceptedReviewWarning(correction)
	return wdReviewOutcome{kind: "completed"}
}

func (r *watchdogRuntime) displayAcceptedReviewWarning(correction bool) {
	if r.activeReviewWarning == nil {
		return
	}
	details := *r.activeReviewWarning
	details.State = "displayed"
	details.DisplayedAt = isoNow()
	if correction {
		r.lastWarning = &details
		r.routeWarning(details, &wdSendOptions{DeliverAs: "steer"})
		return
	}
	r.deliverBoundaryWarning(details)
}

// deliverBoundaryWarning: a displayed boundary warning continues the run; the same identity back from consecutive boundaries
// means no progress, so it is shown held instead of continuing again.
func (r *watchdogRuntime) deliverBoundaryWarning(details wdWarning) {
	identity := details.Identity
	if identity == "" {
		identity = wdSHA256(strings.Join([]string{details.Severity, details.Summary, details.Evidence}, "\n"))
	}
	if r.lastBoundaryIdentity == identity {
		r.boundaryRepeats++
	} else {
		r.lastBoundaryIdentity = identity
		r.boundaryRepeats = 1
	}
	stalemate := r.boundaryRepeats >= r.configResult.Config.StalemateRepeats
	delivered := details
	var opts *wdSendOptions
	if stalemate {
		repeats := r.boundaryRepeats
		delivered.State = "stalemate"
		delivered.StalemateRepeats = &repeats
		hold := false
		opts = &wdSendOptions{TriggerTurn: &hold}
	}
	r.stalemate = stalemate
	r.lastWarning = &delivered
	r.routeWarning(delivered, opts)
}

// repeatableBoundaryIdentity: the previous boundary finding is a repeat to count, not a duplicate, until stalemate.
func (r *watchdogRuntime) repeatableBoundaryIdentity() string {
	if r.waitingAtAgentEnd && !r.stalemate {
		return r.lastBoundaryIdentity
	}
	return ""
}

func (r *watchdogRuntime) resetBoundaryRepeats() {
	r.lastBoundaryIdentity = ""
	r.boundaryRepeats = 0
	r.stalemate = false
}

func (r *watchdogRuntime) currentRepoChangeSignature(cwd string) *watchdogRepoChangeSignature {
	if r.reviewChangesOnly && r.isEnabled() {
		return r.repoChangeSignature(cwd)
	}
	return nil
}

func (r *watchdogRuntime) resetRepoChangeBaseline(cwd string, reviewed bool) {
	if cwd == "" {
		cwd = r.cwd
	}
	r.turnStartChangeSig = r.currentRepoChangeSignature(cwd)
	if reviewed {
		r.lastReviewedChangeSig = ""
		if r.turnStartChangeSig != nil {
			r.lastReviewedChangeSig = r.turnStartChangeSig.Key
		}
	} else if r.lastReviewedChangeSig == "" && r.turnStartChangeSig != nil {
		r.lastReviewedChangeSig = r.turnStartChangeSig.Key
	}
	r.currentChangedPaths = nil
	if r.turnStartChangeSig != nil {
		r.currentChangedPaths = r.turnStartChangeSig.ChangedPaths
	}
	r.observedRepoEdit = false
}

func (r *watchdogRuntime) resolveReviewChangeSignature(current *watchdogRepoChangeSignature, cwd string) *watchdogRepoChangeSignature {
	if !r.reviewChangesOnly {
		return nil
	}
	if current != nil {
		r.currentChangedPaths = current.ChangedPaths
		if r.turnStartChangeSig != nil && current.Key == r.turnStartChangeSig.Key {
			return nil
		}
		if len(current.ChangedPaths) == 0 {
			return nil
		}
		return current
	}
	if r.observedRepoEdit {
		return &watchdogRepoChangeSignature{Root: cwd, Key: "observed-edit:" + itoa(r.epoch) + ":" + itoa(r.reviewIDCounter) + ":" + itoa(r.pendingDeltaChars), ChangedPaths: []string{}}
	}
	return nil
}

// collectLspDiagnostics runs the LSP pre-pass of a boundary review; the lock is released while the language server runs.
func (r *watchdogRuntime) collectLspDiagnostics(sig *watchdogRepoChangeSignature, epoch, agentEndID int, signal <-chan struct{}) string {
	config := r.configResult.Config.Lsp
	if !config.Enabled || sig == nil || len(sig.ChangedPaths) == 0 {
		status := "disabled"
		if config.Enabled {
			status = "skipped"
		}
		r.lastLspSnapshot = &wdLspSnapshot{wdLspResult: wdLspResult{Status: status, CheckedPaths: []string{}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}},
			Enabled: config.Enabled, UpdatedAt: isoNow()}
		return ""
	}
	var raw wdLspResult
	var failure error
	lsp := r.lspDiagnostics
	cwd := r.cwd
	r.unlocked(func() {
		defer func() {
			if p := recover(); p != nil {
				failure = wdPanicError(p)
			}
		}()
		raw = lsp(watchdogLspRequest{Cwd: cwd, Root: sig.Root, ChangedPaths: sig.ChangedPaths, Config: config, Signal: signal})
	})
	if !r.isAgentEndCurrent(epoch, agentEndID) {
		return ""
	}
	if failure != nil {
		r.lastLspSnapshot = &wdLspSnapshot{wdLspResult: wdLspResult{Status: "failed", CheckedPaths: []string{}, SkippedPaths: sig.ChangedPaths, Diagnostics: []wdLspDiagnostic{},
			Message: "LSP diagnostics failed: " + failure.Error()}, Enabled: true, UpdatedAt: isoNow()}
		return ""
	}
	diagnosticCount := len(raw.Diagnostics)
	fresh := r.lspLedger.reduce(raw)
	r.lastLspSnapshot = &wdLspSnapshot{wdLspResult: fresh, Enabled: true, DiagnosticCount: diagnosticCount, FreshDiagnosticCount: len(fresh.Diagnostics), UpdatedAt: isoNow()}
	if w := watchdogWarningFromLspDiagnostics(fresh); w != nil {
		r.displayBoundaryWarning(*w)
	}
	return formatWatchdogLspDiagnosticsBlock(fresh)
}

func (r *watchdogRuntime) lspSnapshot() wdLspSnapshot {
	if r.lastLspSnapshot != nil {
		s := *r.lastLspSnapshot
		s.CheckedPaths = append([]string{}, s.CheckedPaths...)
		s.SkippedPaths = append([]string{}, s.SkippedPaths...)
		s.Diagnostics = append([]wdLspDiagnostic{}, s.Diagnostics...)
		return s
	}
	enabled := r.isEnabled() && r.configResult.Config.Lsp.Enabled
	status := "disabled"
	if enabled {
		status = "skipped"
	}
	return wdLspSnapshot{wdLspResult: wdLspResult{Status: status, CheckedPaths: []string{}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}}, Enabled: enabled}
}

func (r *watchdogRuntime) appendBoundedDelta(delta string) {
	entry := jsTrim(delta)
	if entry == "" {
		return
	}
	if wdLen(entry) > wdMaxReviewInputChars {
		entry = wdBoundReview(entry)
	}
	r.pendingDeltas = append(r.pendingDeltas, entry)
	r.pendingDeltaChars += wdLen(entry)
	for len(r.pendingDeltas) > 1 && r.pendingDeltaChars+(len(r.pendingDeltas)-1)*len(wdReviewDeltaSeparator) > wdMaxReviewInputChars {
		r.pendingDeltaChars -= wdLen(r.pendingDeltas[0])
		r.pendingDeltas = r.pendingDeltas[1:]
	}
}

func (r *watchdogRuntime) buildReviewInput(sig *watchdogRepoChangeSignature, lspBlock string) string {
	input := strings.Join(r.pendingDeltas, wdReviewDeltaSeparator)
	scopeBlock := r.scopeBlock()
	changes := ""
	if sig != nil && len(sig.ChangedPaths) > 0 {
		lines := []string{"Changed repo paths:"}
		paths := sig.ChangedPaths
		if len(paths) > wdMaxChangedPathsListed {
			paths = paths[:wdMaxChangedPathsListed]
		}
		for _, f := range paths {
			lines = append(lines, "- "+f)
		}
		changes = strings.Join(lines, "\n")
	}
	activity := ""
	if r.activityTail != "" {
		activity = "Recent delivered orchestration activity (oldest first; bounded observations, not a task board; waits/holds are not evidence of neglect):\n" + r.activityTail
	}
	var pieces []string
	for _, p := range []string{scopeBlock, changes, lspBlock, activity} {
		if p != "" {
			pieces = append(pieces, p)
		}
	}
	if len(pieces) == 0 {
		return wdBoundReview(input)
	}
	maxContext := wdMaxReviewInputChars / 2
	maxPiece := maxContext / len(pieces)
	if maxPiece < 1_000 {
		maxPiece = 1_000
	}
	bounded := make([]string, len(pieces))
	for i, piece := range pieces {
		switch {
		case wdLen(piece) <= maxPiece:
			bounded[i] = piece
		case piece == activity:
			bounded[i] = boundWatchdogReviewText(piece, maxPiece)
		default:
			bounded[i] = wdSlice(piece, 0, maxPiece-6) + "\n- ..."
		}
	}
	boundedContext := strings.Join(bounded, wdReviewDeltaSeparator)
	separatorLength := 0
	if input != "" {
		separatorLength = len(wdReviewDeltaSeparator)
	}
	budget := wdMaxReviewInputChars - wdLen(boundedContext) - separatorLength
	boundedInput := ""
	if budget > 0 {
		boundedInput = boundWatchdogReviewText(input, budget)
	}
	var parts []string
	for _, p := range []string{boundedContext, boundedInput} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, wdReviewDeltaSeparator)
}

func (r *watchdogRuntime) scopeBlock() string {
	if r.configResult.Config.ScopeEnabled {
		return r.scope.render()
	}
	return ""
}

func (r *watchdogRuntime) clearPendingDeltas() {
	r.pendingDeltas = nil
	r.pendingDeltaChars = 0
}

func (r *watchdogRuntime) clearActivity() {
	if r.activityTail == "" {
		return
	}
	r.activityTail = ""
	r.activityPending = false
}

func (r *watchdogRuntime) fail(message string) {
	r.failedReviews++
	r.lastError = message
	r.status = "failed"
	r.clearPendingDeltas()
	r.resolveWaiters(true)
}

func (r *watchdogRuntime) markLastWarningStale() {
	if r.lastWarning == nil || r.lastWarning.State == "displayed" {
		return
	}
	w := *r.lastWarning
	stale := true
	w.Stale = &stale
	w.State = "stale"
	r.lastWarning = &w
}

func (r *watchdogRuntime) isSettled() bool { return !r.reviewing && len(r.pendingDeltas) == 0 }

func (r *watchdogRuntime) resolveWaiters(settled bool) {
	if !settled && !r.disposed {
		return
	}
	waiters := r.waiters
	r.waiters = nil
	for _, w := range waiters {
		w.ch <- settled
	}
}
