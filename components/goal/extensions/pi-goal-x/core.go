package pi_goal_x

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// The kernel: goal-state.ts createGoalCore. All state lives in core; the commands (goal_commands.go), tools (goal_tools.go) and
// events (goal_events.go) operate on it. core.state() mirrors the original's `state.goal`: reading returns the focused goal,
// setStateGoal replaces it in the pool and updates the focus. The host is bound at every entry (core.h); handlers run under
// core.mu, and so do timer callbacks (core.after), as the original runs everything on one thread.

const (
	focusEntryType = "pi-goal-focus"
	stateEntryType = "pi-goal-state"
	widgetKey      = "goal"
)

// goalDeps is the original's `dependencies`: the injectable completion auditor and the nested-session factory the auditor runs on.
type goalDeps struct {
	runCompletionAuditor func(args runGoalCompletionAuditorArgs) goalAuditorResult
	createSession        auditorSessionFactory
}

// goalAuditResultCard is core.auditResult: the finished audit card shown briefly before the dashboard returns (§15.4).
type goalAuditResultCard struct{ verdict, report, at string }

// goalCreationConfig is GoalCreationConfig.
type goalCreationConfig struct {
	objective    string
	autoContinue bool
	sisyphus     bool
	skipAuditor  bool
	taskList     *jsObject
}

type core struct {
	h host
	// pi is the extension the registration seams add to (nil under the tests that drive the core through a host only).
	pi   *goalPi
	deps goalDeps
	st   storage
	mu   sync.Mutex
	// afterBase starts a timer; tests replace it with a deterministic clock.
	afterBase func(d time.Duration, f func()) *time.Timer

	goals    *goalPool
	focused  *string
	focusRev int
	// explicit is hasExplicitSessionFocus.
	explicit bool

	runningGoalID              *string
	auditProgress              *auditorWidgetProgress
	auditAnimationStop         func()
	auditResult                *goalAuditResultCard
	auditResultTimer           *time.Timer
	auditAbortController       context.CancelFunc
	auditAborted               bool
	goalModalDepth             int
	goalWorkToolCalledThisTurn bool
	tasksEnabled               bool
	debugMode                  bool
	dashboardExpanded          bool
	terminalInputUnsubscribe   func()
	widgetRef                  goalWidgetComponentRef

	goalService   *goalService
	runtime       *goalRuntime
	scheduler     *goalScheduler
	auditMessages *goalAuditMessages
	accounting    *goalAccounting

	// draft is the active guided draft (goal-drafting.ts activeDrafts.get(core)).
	draft *activeGoalDraft

	profileInitialized bool
	draftingProfile    bool

	focusStorageRoot  string
	externalFocusRoot bool

	lastGoalActivityAt int64
	stallNotified      bool
	budgetWarnings     map[string]bool

	uiScheduled      bool
	lastUI           bool
	widgetRegistered bool
	widgetLines      []string
	widgetWidthOff   func()
	// widgetPushTap observes the widget lines the SDK host forwards (tests).
	widgetPushTap func(key string, lines []string)
}

func newCore(h host) *core { return newGoalCore(h, goalDeps{}) }

// newGoalCore is createGoalCore. h may be nil until the first entry binds a host.
func newGoalCore(h host, deps goalDeps) *core {
	c := &core{h: h, deps: deps, goals: newPool(), tasksEnabled: true, budgetWarnings: map[string]bool{}}
	if h != nil {
		c.st = storage{cwd: h.Cwd()}
	}
	c.afterBase = time.AfterFunc
	c.lastGoalActivityAt = clockMs()
	// Sole mutation boundary for goal records: all goal-file writes, ledger appends and ordered write -> ledger -> memory commits
	// route through this service; handlers keep validation and runtime/UI effects.
	c.goalService = newGoalService(coreServiceRef{c})
	c.runtime = newGoalRuntime(goalRuntimeHooks{
		authorize:      func(goalRuntimeCtx, *jsObject) *jsObject { return c.scheduler.claim() },
		recover:        func(goalRuntimeCtx) { c.scheduler.recover() },
		dispatchFailed: func(goalRuntimeCtx) { c.scheduler.failedDispatch() },
		sendFollowUp: func(content string, details *jsObject) error {
			return c.h.SendMessage(goalEventEntry, content, false, details, true, "followUp")
		},
		getGoal:      func() *jsObject { return c.state() },
		isActionable: func(goalID string) bool { return c.isActionableContinuationGoal(&goalID) },
	})
	c.runtime.afterFunc = c.after
	c.accounting = &goalAccounting{}
	c.auditMessages = &goalAuditMessages{}
	c.scheduler = newGoalSchedulerFor(c)
	return c
}

// after starts a timer whose callback runs under the core's lock, then the queued UI update (the original's one thread).
func (c *core) after(d time.Duration, f func()) *time.Timer {
	return c.afterBase(d, func() { c.locked(f) })
}

// locked runs fn as one unit of work: under the lock, followed by the UI update the original queues as a microtask.
func (c *core) locked(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
	c.flush()
}

func (c *core) state() *jsObject { return focusedGoalFromPool(c.goals, c.focused) }

func sameFocus(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func (c *core) assignFocused(id *string) {
	if !sameFocus(c.focused, id) {
		c.focusRev++
	}
	c.focused = id
	if c.profileInitialized && !c.draftingProfile {
		c.installGoalToolProfile(c.tasksEnabled)
	}
}

// setRunningGoalFromState is `runningGoalId = state.goal?.id ?? null`.
func (c *core) setRunningGoalFromState() {
	if g := c.state(); g != nil {
		c.runningGoalID = new(gstr(g, "id"))
	} else {
		c.runningGoalID = nil
	}
}

func (c *core) focusedOperationToken(goalID string) goalFocusToken {
	return goalFocusToken{goalId: goalID, revision: c.focusRev}
}

func (c *core) isFocusedOperationCurrent(token goalFocusToken) bool {
	return c.focused != nil && *c.focused == token.goalId && c.focusRev == token.revision
}

// focusedOperationCancelledResult is the terminating tool result of an operation whose focus went away.
func (c *core) focusedOperationCancelledResult(action string, token goalFocusToken) goalAgentResult {
	return goalAgentResult{
		text:      fmt.Sprintf("%s cancelled because goal %s is no longer focused in this session. The shared goal was not modified.", action, token.goalId),
		details:   goalDetails(c.state(), ""),
		terminate: true,
	}
}

// setStateGoal is the `state.goal = next` setter.
func (c *core) setStateGoal(next *jsObject) {
	if next != nil {
		c.goals.set(gstr(next, "id"), next)
		c.assignFocused(new(gstr(next, "id")))
		return
	}
	if c.focused != nil {
		c.goals.delete(*c.focused)
	}
	c.assignFocused(nil)
}

// coreServiceRef is the GoalService's view of the core (GoalServiceRef).
type coreServiceRef struct{ c *core }

func (r coreServiceRef) getFocused() *jsObject   { return r.c.state() }
func (r coreServiceRef) setFocused(g *jsObject)  { r.c.setStateGoal(g) }
func (r coreServiceRef) getPool() *goalPool      { return r.c.goals }
func (r coreServiceRef) replacePool(p *goalPool) { r.c.goals = p }
func (r coreServiceRef) getFocusedGoalId() string {
	if r.c.focused == nil {
		return ""
	}
	return *r.c.focused
}
func (r coreServiceRef) assignFocusedGoalId(id string) {
	if id == "" {
		r.c.assignFocused(nil)
		return
	}
	r.c.assignFocused(&id)
}
func (r coreServiceRef) focusToken(id string) goalFocusToken { return r.c.focusedOperationToken(id) }
func (r coreServiceRef) isTokenCurrent(t goalFocusToken) bool {
	return r.c.isFocusedOperationCurrent(t)
}
func (r coreServiceRef) appendFocusEntry(id string, reason string) {
	if id == "" {
		r.c.appendFocusEntry(nil, reason)
		return
	}
	r.c.appendFocusEntry(&id, reason)
}
func (r coreServiceRef) onFocusedGoalLost(string, goalServiceContext) {
	r.c.clearStoppedRuntimeState()
	r.c.updateUI()
}
func (r coreServiceRef) onReconciled(goal *jsObject) {
	if gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") {
		r.c.clearContinuationState(true)
	}
	if gstr(goal, "status") != "active" {
		r.c.clearActiveAccounting()
	}
}
func (r coreServiceRef) onFocusChanged(string, string) {
	r.c.clearContinuationState(true)
	r.c.clearActiveAccounting()
}

// onDiagnostic is the non-fatal failure sink: ledger appends never roll back the authoritative state write, but they stay observable.
func (r coreServiceRef) onDiagnostic(d goalDiagnostic) {
	fmt.Fprintf(os.Stderr, "[pi-goal] %s diagnostic: %s\n", d.source, d.message)
}

// ---- tool profile ----

// installGoalToolProfile installs the fixed three/five goal-tool profile. Called only after session initialization and after a
// settings change that toggles disableTasks. It never mutates the host's ordinary work-tool selection.
func (c *core) installGoalToolProfile(tasksEnabled bool) {
	c.profileInitialized = true
	c.draftingProfile = false
	c.installTools(applicableGoalTools(c.state(), tasksEnabled))
	c.tasksEnabled = tasksEnabled
}

// installDraftingToolProfile is the transient drafting profile, entered only by an explicit user drafting command.
func (c *core) installDraftingToolProfile() {
	c.draftingProfile = true
	c.installTools(draftingGoalTools)
}

// installTools replaces every known goal tool in the active set by add. Idempotent: a profile that is already installed is neither
// rebuilt nor re-reported. A host that cannot list its tools is left alone.
func (c *core) installTools(add []string) {
	active, err := c.h.GetActiveTools()
	if err != nil {
		return
	}
	var current []string
	seen := map[string]bool{}
	push := func(name string) {
		if !seen[name] {
			seen[name] = true
			current = append(current, name)
		}
	}
	for _, name := range active {
		push(name)
	}
	for _, known := range allRegisteredGoalTools {
		if seen[known] {
			seen[known] = false
			kept := current[:0]
			for _, n := range current {
				if n != known {
					kept = append(kept, n)
				}
			}
			current = kept
		}
	}
	for _, name := range add {
		push(name)
	}
	next := append([]string{}, current...)
	sort.Strings(next)
	before := append([]string{}, active...)
	sort.Strings(before)
	same := len(next) == len(before)
	for i := 0; same && i < len(next); i++ {
		same = next[i] == before[i]
	}
	if !same {
		c.h.SetActiveTools(current)
	}
}

// ---- audit state ----

func (c *core) setAuditResult(verdict, report string) {
	c.auditResult = &goalAuditResultCard{verdict: verdict, report: report, at: nowIso()}
	if c.auditResultTimer != nil {
		c.auditResultTimer.Stop()
	}
	// Short-lived foreground display (§2.5): the card is visible while the user reads it, then the normal dashboard returns.
	c.auditResultTimer = c.after(6*time.Second, func() {
		c.auditResult = nil
		c.auditResultTimer = nil
		c.invalidateWidget()
	})
	c.invalidateWidget()
}

func (c *core) clearAuditResult() {
	if c.auditResultTimer != nil {
		c.auditResultTimer.Stop()
	}
	c.auditResultTimer = nil
	c.auditResult = nil
}

func (c *core) stopAuditAnimation() {
	if c.auditAnimationStop != nil {
		c.auditAnimationStop()
		c.auditAnimationStop = nil
	}
}

func (c *core) abortAudit() {
	if c.auditAbortController == nil || c.auditProgress == nil {
		return
	}
	c.auditAbortController()
	c.auditAbortController = nil
	c.stopAuditAnimation()
	c.auditProgress = nil
	c.invalidateWidget()
	// Record the abort as transient runtime state only; the completion flow decides the single canonical ledger outcome after the
	// dialog choice.
	c.auditAborted = true
}

func (c *core) enterGoalModal() { c.goalModalDepth++ }
func (c *core) exitGoalModal() {
	if c.goalModalDepth > 0 {
		c.goalModalDepth--
	}
}

// toggleDashboardExpanded flips the unified dashboard between compact and expanded task views (§10).
func (c *core) toggleDashboardExpanded() {
	c.dashboardExpanded = !c.dashboardExpanded
	c.invalidateWidget()
}

func (c *core) isDashboardExpanded() bool { return c.dashboardExpanded }

// ---- runtime glue ----

func (c *core) clearContinuationTimer() { c.runtime.clearContinuationTimer() }

func (c *core) clearContinuationState(resetNetworkErrorBackoff bool) {
	c.scheduler.cancelTimer()
	c.runtime.clearContinuationState(resetNetworkErrorBackoff)
}

func (c *core) clearActiveAccounting() { c.accounting.clear() }

func (c *core) advanceTurnSeq() { c.runtime.advanceTurn() }

func (c *core) currentTurnStoppedGoalId() *string { return c.runtime.currentTurnStoppedGoalId() }

func (c *core) isActionableContinuationGoal(goalID *string) bool {
	g := c.state()
	return goalID != nil && *goalID != "" && g != nil && gstr(g, "id") == *goalID && gstr(g, "status") == "active" && gflag(g, "autoContinue")
}

func (c *core) isStaleCheckpointBlockedToolCall(toolName string) bool {
	return c.runtime.isStaleCheckpointBlocked(toolName)
}

func (c *core) clearStoppedRuntimeState() {
	c.clearContinuationState(true)
	c.clearActiveAccounting()
}

func (c *core) openGoals() []*jsObject { return openGoalsFromPool(c.goals) }

func (c *core) reconcileFocusedGoalFromDisk(preserveMemoryUsage bool) bool {
	return c.goalService.reconcileFocused(c.st, preserveMemoryUsage)
}

// ---- focus ----

func (c *core) appendFocusEntry(id *string, reason string) {
	c.explicit = true
	d := goalFocusDetails(id, reason)
	if c.externalFocusRoot {
		d.set("storageRoot", c.focusStorageRoot)
	}
	c.h.AppendEntry(focusEntryType, d)
}

func (c *core) setFocusedGoalId(id *string, reason string, recordLedger bool) {
	prev := c.focused
	changed := !sameFocus(prev, id)
	if changed {
		c.scheduler.takeover()
		_, _ = c.goalService.flushTurn(c.st) // P1-3: persist the old buffer before switching focus
	}
	if id != nil && c.goals.has(*id) {
		c.assignFocused(id)
	} else {
		c.assignFocused(nil)
	}
	if !sameFocus(prev, c.focused) {
		c.clearContinuationState(true)
		c.clearActiveAccounting()
	}
	c.appendFocusEntry(c.focused, reason)
	// Ledger append failure must not crash a focus change.
	if recordLedger && c.focused != nil {
		c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_focused", "goalId", *c.focused, "reason", reason, "at", nowIso())})
	} else if recordLedger && prev != nil {
		c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_unfocused", "reason", reason, "at", nowIso())})
	}
	c.updateUI()
}

func (c *core) updateFocusedGoal(next *jsObject, shouldPersist bool) {
	c.goals.set(gstr(next, "id"), next)
	c.assignFocused(new(gstr(next, "id")))
	if shouldPersist {
		c.persist()
	}
	c.updateUI()
}

func (c *core) armFocusedContinuation() {
	c.beginAccounting()
	if g := c.state(); g != nil && gstr(g, "status") == "active" && gflag(g, "autoContinue") {
		c.scheduler.schedule()
	}
}

func (c *core) removeFocusedGoal(reason string) {
	_, _ = c.goalService.flushTurn(c.st) // P1-3: persist pending mutations before the goal leaves the session
	if c.focused != nil {
		c.goals.delete(*c.focused)
	}
	c.assignFocused(nil)
	c.clearStoppedRuntimeState()
	c.appendFocusEntry(nil, reason)
	c.updateUI()
}

// ---- accounting ----

func (c *core) beginAccounting() {
	g := c.state()
	if c.scheduler.isWaiting() || g == nil || gstr(g, "status") != "active" {
		c.clearActiveAccounting()
		return
	}
	c.accounting.begin(new(gstr(g, "id")))
}

// accountingLive adapts goalAccounting to liveDisplayGoal's view.
type accountingLive struct{ a *goalAccounting }

func (l accountingLive) isActiveFor(id string) bool { return l.a.isActiveFor(id) }
func (l accountingLive) liveSeconds() float64       { return l.a.liveSeconds(clockMs()) }

func (c *core) goalForDisplay() *jsObject {
	return liveDisplayGoal(c.state(), accountingLive{c.accounting})
}

// accountProgress charges the interval since the last charge to the focused goal (completedTurnTokens: the finished turn's tokens).
func (c *core) accountProgress(completedTurnTokens float64) {
	g := c.state()
	// Skip disk reconciliation for complete goals: they are pending archival at turn_end.
	if g != nil && g.has("activePath") && truthy(g.vals["activePath"]) && gstr(g, "status") != "complete" && !c.reconcileFocusedGoalFromDisk(true) {
		return
	}
	g = c.state()
	if g == nil || gstr(g, "status") != "active" || !c.accounting.isActiveFor(gstr(g, "id")) {
		c.beginAccounting()
		return
	}
	// Serialized idempotent charge: never double-charges the same interval.
	ch := c.accounting.charge(accountingChargeOpts{completedTurnTokens: completedTurnTokens})
	if ch.tokens == 0 && ch.seconds == 0 {
		return
	}
	next := cloneGoal(g)
	usage := goalUsageCopy(next)
	usage.set("tokensUsed", gnum(usage, "tokensUsed")+ch.tokens)
	usage.set("activeSeconds", gnum(usage, "activeSeconds")+ch.seconds)
	next.set("usage", usage)
	next.set("updatedAt", nowIso())
	c.setStateGoal(next)
	c.persist()

	// One warning per accounting update, even when several thresholds are crossed.
	budgetGoal := c.state()
	if budgetGoal != nil && gstr(budgetGoal, "status") == "active" && budgetGoal.has("tokenBudget") {
		budget, isNum := budgetGoal.num("tokenBudget")
		used := gnum(budgetGoal.obj("usage"), "tokensUsed")
		if isNum && budget > 0 && used > 0 {
			pct := used / budget
			var crossed []float64
			for _, threshold := range []float64{0.5, 0.75, 0.9} {
				key := fmt.Sprintf("%s:%s:%s", gstr(budgetGoal, "id"), jsNumber(budget), jsNumber(threshold))
				if pct >= threshold && !c.budgetWarnings[key] {
					crossed = append(crossed, threshold)
				}
			}
			for _, threshold := range crossed {
				c.budgetWarnings[fmt.Sprintf("%s:%s:%s", gstr(budgetGoal, "id"), jsNumber(budget), jsNumber(threshold))] = true
			}
			if len(crossed) > 0 {
				rounded := jsRound(pct * 100)
				c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_budget_warning", "goalId", gstr(budgetGoal, "id"), "budget", budget, "tokensUsed", used, "pct", rounded, "at", nowIso())})
				c.h.Notify(fmt.Sprintf("Token budget %s%% used (%s/%s tokens). Use /goal-tweak to change or remove the budget.", jsNumber(rounded), jsNumber(used), jsNumber(budget)), "warning")
			}
		}
	}

	// Token-budget transition: when accounted usage reaches the budget, mark the goal budget_limited exactly once (status no longer
	// active, so accounting stops and the transition cannot re-fire), emit the ledger event, arm the one-time wrap-up steering, and
	// cancel pending continuations.
	if budgetGoal != nil && gstr(budgetGoal, "status") == "active" {
		if budget, ok := budgetGoal.num("tokenBudget"); ok && budgetGoal.has("tokenBudget") && budgetReached(budgetGoal) {
			transition, err := c.goalService.apply(c.st, goalMutationSpec{
				reconcile: new(false),
				mutate: func(g *jsObject) *jsObject {
					return goalWith(g, "status", "budget_limited", "updatedAt", nowIso())
				},
				ledgerFn: func(written *jsObject) ([]*jsObject, error) {
					return []*jsObject{ledgerEvent("goal_budget_limited", "goalId", gstr(written, "id"), "budget", budget,
						"tokensUsed", gnum(written.obj("usage"), "tokensUsed"), "at", gstr(written, "updatedAt"))}, nil
				},
			})
			if err == nil && transition.ok {
				c.runtime.armPostBudgetReminder()
				c.runtime.clearContinuationState(true)
				c.accounting.clear()
				c.updateUI()
			}
		}
	}
}

// budgetReached: the token budget is spent (budgetRemaining() === 0).
func budgetReached(g *jsObject) bool {
	remaining, ok := budgetRemaining(g)
	return ok && remaining == 0
}

// syncGoalPromptFromDisk merges the objective from the goal file; true when it changed.
func (c *core) syncGoalPromptFromDisk() bool {
	g := c.state()
	if g == nil || gstr(g, "status") == "complete" {
		return false
	}
	previous := gstr(g, "objective")
	merged := c.st.mergeGoalPromptFromDisk(g)
	c.setStateGoal(merged)
	return gstr(merged, "objective") != previous
}

// persist writes the focused goal back through the service (a new updatedAt, a revision bump when nobody else changed it).
func (c *core) persist() {
	if _, err := c.goalService.persist(c.st); err != nil {
		fmt.Fprintf(os.Stderr, "[pi-goal] persist failed: %v\n", err)
	}
	c.updateUI()
}

func (c *core) refreshGoalDisplayFromDisk() {
	g := c.state()
	if g == nil || gstr(g, "status") == "complete" {
		return
	}
	if c.syncGoalPromptFromDisk() {
		c.setStateGoal(goalWith(c.state(), "updatedAt", nowIso()))
	}
	c.updateUI()
}

// ---- stall detection (F5) ----

// touchGoalActivity bumps the last-activity timestamp (called on real work events).
func (c *core) touchGoalActivity() {
	c.lastGoalActivityAt = clockMs()
	c.stallNotified = false
}

// checkStall detects a stalled active auto-continue goal; it returns the [GOAL STALLED] steering note ("" when not stalled).
func (c *core) checkStall() string {
	g := c.state()
	if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
		return ""
	}
	timeoutMinutes := loadGoalSettings(c.h.Cwd()).stallTimeoutMinutes
	if timeoutMinutes <= 0 || c.stallNotified {
		return ""
	}
	if float64(clockMs()-c.lastGoalActivityAt) < timeoutMinutes*60000 {
		return ""
	}
	c.stallNotified = true
	goalID := gstr(g, "id")
	plural := "s"
	if timeoutMinutes == 1 {
		plural = ""
	}
	minutes := fmt.Sprintf("%s minute%s", jsNumber(timeoutMinutes), plural)
	// Stall detection must never crash the turn.
	c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_stalled", "goalId", goalID, "reason", "No continuation or tool activity for "+minutes+".", "at", nowIso())})
	c.h.Notify("Goal stalled: no activity for "+minutes+".", "warning")
	return "\n\n[GOAL STALLED goalId=" + goalID + "]\nNo continuation or tool activity for " + minutes + ". Report your progress or ask the user to pause or resume the goal."
}

// ---- UI ----

// updateUI queues a coalesced widget update (P1-9): several calls in one synchronous block collapse into one render at flush().
func (c *core) updateUI() {
	if !c.h.HasUI() {
		return
	}
	c.lastUI = true
	c.uiScheduled = true
}

// flush runs the UI update the original queues as a microtask: once, after the handler's synchronous work.
func (c *core) flush() {
	if !c.uiScheduled {
		return
	}
	c.uiScheduled = false
	if c.lastUI {
		c.renderUI()
	}
}

func (c *core) clearGoalWidget() {
	c.lastUI = false
	if c.h.HasUI() {
		c.h.SetStatus(widgetKey, "")
		c.h.SetWidget(goalWidgetKey, nil)
	}
	c.widgetRegistered = false
	c.widgetRef.current = nil
	if c.widgetWidthOff != nil {
		c.widgetWidthOff()
		c.widgetWidthOff = nil
	}
}

func (c *core) renderUI() {
	totalOpen := otherOpenGoalCount(c.goals, nil)
	g := c.state()
	if g == nil && totalOpen == 0 {
		c.clearGoalWidget()
		return
	}
	if g == nil {
		// PR #29: layered hideUnfocusedBanner suppresses BOTH the unfocused widget and the status hint. Focused dashboards and audit UI
		// are unaffected; the model-facing [PI GOAL UNFOCUSED] prompt is unchanged.
		if loadGoalSettings(c.h.Cwd()).hideUnfocusedBanner {
			c.clearGoalWidget()
			return
		}
		c.h.SetStatus(widgetKey, fmt.Sprintf("goal: unfocused [%d open] - /goal-focus", totalOpen))
		c.ensureWidget()
		return
	}
	// The goal widget is the single home for goal status: the status line lives in the compact/expanded dashboard, so the
	// focused-goal footer segment is cleared; the unfocused branch above sets its own hint.
	c.h.SetStatus(widgetKey, "")
	c.ensureWidget()
}

// ensureWidget registers the dashboard once and refreshes it afterwards.
func (c *core) ensureWidget() {
	if c.widgetRegistered {
		if c.widgetRef.current != nil {
			c.widgetRef.current.update()
		}
		return
	}
	cwd := c.h.Cwd()
	st := c.st
	factory := makeGoalWidgetFactory(goalWidgetFactoryOptions{
		getGoal: func() *jsObject {
			if g := c.goalForDisplay(); g != nil {
				return g
			}
			return c.state()
		},
		getOpenGoalCount:   func() int { return otherOpenGoalCount(c.goals, nil) },
		getAuditorProgress: func() *auditorWidgetProgress { return c.auditProgress },
		getSettings:        func() goalSettings { return loadGoalSettings(cwd) },
		getDebugMode:       func() bool { return c.debugMode },
		getStalled:         func() bool { return c.stallNotified },
		getExpanded:        func() bool { return c.dashboardExpanded },
		getLedgerEvents: func() []*jsObject {
			if g := c.state(); g != nil {
				return goalActivityEvents(st, gstr(g, "id"))
			}
			return nil
		},
		getAuditResult: func() *auditResultView {
			if c.auditResult == nil {
				return nil
			}
			return &auditResultView{verdict: c.auditResult.verdict, report: c.auditResult.report}
		},
		componentRef: &c.widgetRef,
	})
	c.widgetRegistered = true
	factory(&coreWidgetTUI{c: c}, c.h.Theme())
	c.pushWidget()
	if c.widgetWidthOff == nil {
		c.widgetWidthOff = c.h.OnWidthChange(func(int) {
			c.locked(func() { c.pushWidget() })
		})
	}
}

// pushWidget lays the dashboard out at the host's width and sends the lines.
func (c *core) pushWidget() {
	comp := c.widgetRef.current
	if comp == nil {
		return
	}
	lines := comp.Render(c.h.Width())
	if lines == nil {
		lines = []string{}
	}
	c.widgetLines = lines
	c.h.SetWidget(goalWidgetKey, lines)
}

// invalidateWidget is goalWidgetComponentRef.current?.invalidate().
func (c *core) invalidateWidget() {
	if c.widgetRef.current != nil {
		c.widgetRef.current.Invalidate()
	}
}

// coreWidgetTUI is the TUI object the dashboard factory gets: a render request lays the lines out again and pushes them.
type coreWidgetTUI struct{ c *core }

func (t *coreWidgetTUI) getShowHardwareCursor() bool { return false }
func (t *coreWidgetTUI) setShowHardwareCursor(bool)  {}
func (t *coreWidgetTUI) requestRender()              { t.c.pushWidget() }
func (t *coreWidgetTUI) terminalRows() (int, bool)   { return t.c.h.Height() }
func (t *coreWidgetTUI) previousLinesLen() (int, bool) {
	return len(t.c.widgetLines), t.c.widgetLines != nil
}

func footerStatus(g *jsObject) string {
	tokens, seconds := usageOf(g)
	var bits []string
	if seconds > 0 {
		bits = append(bits, formatDuration(seconds))
	}
	if tokens > 0 {
		bits = append(bits, firstWord(formatTokenValue(tokens)))
	}
	usage := ""
	if len(bits) > 0 {
		usage = " [" + strings.Join(bits, " ") + "]"
	}
	prefix := "goal"
	if gflag(g, "sisyphus") {
		prefix = "goal✊"
	}
	return prefix + ": " + statusLabel(g) + usage + " - " + truncateText(gstr(g, "objective"), 60)
}

// ---- session load ----

func resolveSessionFocus(pool *goalPool, entry *focusEntry, legacy *jsObject, autoSelect bool) *string {
	var focusedID *string
	if entry != nil {
		focusedID = entry.goalID
	}
	if focusedID != nil {
		if g := pool.get(*focusedID); g != nil && gstr(g, "status") != "complete" {
			return focusedID
		}
	}
	if entry != nil {
		return nil
	}
	if legacy != nil && gstr(legacy, "status") != "complete" {
		id := gstr(legacy, "id")
		if !pool.has(id) {
			pool.set(id, cloneGoal(legacy))
		}
		return &id
	}
	if autoSelect {
		if open := openGoalsFromPool(pool); len(open) == 1 {
			return new(gstr(open[0], "id"))
		}
	}
	return nil
}

// loadState reads the pool and resolves the session's focus from the branch's focus entry (or the legacy state entry).
func (c *core) loadState() {
	c.clearGoalWidget()
	root := c.st.root()
	defaultRoot := defaultGoalRoot(c.h.Cwd())
	c.focusStorageRoot = root
	c.externalFocusRoot = root != defaultRoot
	c.goals = c.st.readActiveGoalPool()
	invalidateGoalSettingsCache()
	c.tasksEnabled = !loadGoalSettings(c.h.Cwd()).disableTasks
	c.focusRev++ // Session reload/tree navigation invalidates pending async focus operations.
	c.assignFocused(nil)
	c.explicit = false
	var entry *focusEntry
	var legacy *jsObject
	legacySeen := false
	branch := c.h.Branch()
	for i := len(branch) - 1; i >= 0; i-- {
		e, _ := branch[i].(*jsObject)
		if e == nil {
			continue
		}
		if t, _ := e.str("type"); t != "custom" {
			continue
		}
		ct, _ := e.str("customType")
		if entry == nil && ct == focusEntryType {
			entry = normalizeGoalFocusEntry(e.vals["data"])
		}
		if !legacySeen && ct == stateEntryType {
			if d := e.obj("data"); d != nil {
				legacy = normalizeGoalRecord(d.vals["goal"])
			}
			legacySeen = true
		}
		if entry != nil && legacySeen {
			break
		}
	}
	if legacy != nil && gstr(legacy, "status") != "complete" {
		legacy = sanitizeGoalPaths(c.st, c.st.mergeGoalPromptFromDisk(legacy))
	}
	settings := loadGoalSettings(c.h.Cwd())
	if entry != nil {
		entryRoot := defaultRoot
		if entry.hasStorage {
			entryRoot = entry.root
		}
		if entryRoot != c.focusStorageRoot {
			entry = &focusEntry{goalID: nil, reason: entry.reason}
			legacy = nil
		}
	}
	if c.focusStorageRoot != defaultRoot {
		legacy = nil
	}
	c.explicit = entry != nil
	c.assignFocused(resolveSessionFocus(c.goals, entry, legacy, settings.autoSelectSingleGoal))
	if entry == nil && c.focused != nil {
		reason := "selected"
		if legacy != nil && gstr(legacy, "id") == *c.focused {
			reason = "migrated"
		}
		c.appendFocusEntry(c.focused, reason)
	}
	for _, g := range c.goals.values() {
		if gstr(g, "status") == "complete" {
			c.goals.delete(gstr(g, "id"))
		}
	}
	c.clearStoppedRuntimeState()
	c.runningGoalID = nil
	c.updateUI()
}

// ---- state operations ----

// setGoal is setGoal(next, ctx, shouldPersist, focusReason): next becomes the focused goal (nil removes the focused one).
func (c *core) setGoal(next *jsObject, shouldPersist bool, focusReason string) {
	var previous *string
	if g := c.state(); g != nil {
		previous = new(gstr(g, "id"))
	}
	c.setStateGoal(next)
	focusChanged := !sameFocus(previous, c.focused)
	if focusChanged {
		c.clearContinuationState(true)
		c.clearActiveAccounting()
	}
	if focusReason != "" && focusChanged {
		c.appendFocusEntry(c.focused, focusReason)
	}
	g := c.state()
	if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
		c.clearContinuationState(true)
	}
	if g == nil || gstr(g, "status") == "paused" || gstr(g, "status") == "complete" {
		c.clearActiveAccounting()
	}
	if shouldPersist {
		c.persist()
	}
	c.updateUI()
}

// archiveCurrentGoal archives the focused goal (paused unless complete) with stopReason reason ("" is undefined) and leaves the focus
// to the caller; nil when it could not be archived.
func (c *core) archiveCurrentGoal(reason string) *jsObject {
	if c.state() == nil {
		return nil
	}
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       new(false),
		refreshFromDisk: true,
		archive:         true,
		commitFocused:   new(false),
		mutate: func(g *jsObject) *jsObject {
			status := "paused"
			if gstr(g, "status") == "complete" {
				status = "complete"
			}
			return goalWith(g, "status", status, "stopReason", optReason(reason))
		},
	})
	if err != nil || !result.ok {
		return nil
	}
	return result.goal
}

func optReason(reason string) any {
	if reason == "" {
		return undef{}
	}
	return reason
}

// stopActiveGoal stops the focused goal with the given status (paused, complete, budget_limited, ...) and stopReason ("" = undefined).
func (c *core) stopActiveGoal(status, reason string) {
	if c.state() == nil {
		return
	}
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       new(false),
		refreshFromDisk: true,
		mutate: func(g *jsObject) *jsObject {
			return goalWith(g, "status", status, "stopReason", optReason(reason), "updatedAt", nowIso())
		},
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			if status != "paused" {
				return nil, nil
			}
			why := reason
			if why == "" {
				why = "unknown"
			}
			return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(written, "id"), "reason", why,
				"suggestedAction", pickOpt(written, "pauseSuggestedAction"), "status", status, "at", gstr(written, "updatedAt"))}, nil
		},
	})
	if err != nil || !result.ok {
		return
	}
	// setGoal() glue: a stopped goal can no longer queue continuations or accrue time, and the UI must reflect the new status.
	c.clearContinuationState(true)
	c.clearActiveAccounting()
	_, _ = c.goalService.flushTurn(c.st) // P1-3: user-visible status change persists now, not at turn end
	c.updateUI()
}

func pickOpt(g *jsObject, k string) any {
	if g.has(k) {
		return g.vals[k]
	}
	return undef{}
}

// pauseActiveGoal is the user-initiated pause (Esc / aborted turn): it clears any stale agent pause reason.
func (c *core) pauseActiveGoal() {
	g := c.state()
	if g == nil || gstr(g, "status") != "active" {
		return
	}
	c.setStateGoal(goalWith(g, "autoContinue", false, "pauseReason", undef{}, "pauseSuggestedAction", undef{}))
	c.stopActiveGoal("paused", "user")
	c.h.Notify("Goal paused.", "info")
}

// toggleGoalAuditor flips the focused goal's independent-auditor setting (§auditor-toggle), persisted per goal.
func (c *core) toggleGoalAuditor() {
	g := c.state()
	if g == nil {
		c.h.Notify("No focused goal to toggle the auditor for.", "info")
		return
	}
	if gstr(g, "status") == "complete" {
		c.h.Notify("This goal is complete; the auditor no longer applies.", "info")
		return
	}
	nextEnabled := false
	if v, ok := g.flag("skipAuditor"); ok && v {
		nextEnabled = true
	}
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       new(false),
		refreshFromDisk: true,
		mutate: func(g *jsObject) *jsObject {
			var skip any = true
			if v, ok := g.flag("skipAuditor"); ok && v {
				skip = undef{}
			}
			return goalWith(g, "skipAuditor", skip, "updatedAt", nowIso())
		},
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			return []*jsObject{ledgerEvent("auditor_toggled", "goalId", gstr(written, "id"), "enabled", nextEnabled, "at", gstr(written, "updatedAt"))}, nil
		},
	})
	if err != nil {
		c.h.Notify("Could not toggle the auditor: "+err.Error(), "error")
		return
	}
	if !result.ok {
		c.h.Notify("Could not toggle the auditor: "+result.message, "error")
		return
	}
	_, _ = c.goalService.flushTurn(c.st) // P1-3: user-visible setting change persists now, not at turn end
	c.updateUI()
	if nextEnabled {
		c.h.Notify("Auditor enabled for this goal.", "info")
	} else {
		c.h.Notify("Auditor disabled for this goal.", "info")
	}
}

func (c *core) flushGoalTransaction() { _, _ = c.goalService.flushTurn(c.st) }

func (c *core) queueContinuation() { c.scheduler.schedule() }

// replaceGoal creates a new goal from config, focuses it and (startNow) starts it. tokenBudget < 0 means none.
func (c *core) replaceGoal(config goalCreationConfig, startNow bool, verificationContract string, tokenBudget float64) {
	goal := createGoal(config.objective, config.autoContinue, config.sisyphus, config.skipAuditor, clockMs())
	goal.set("scheduler", newGoalScheduler(c.h.SessionID()))
	if verificationContract != "" {
		goal.set("verificationContract", verificationContract)
	}
	if config.taskList != nil {
		goal.set("taskList", config.taskList)
	}
	if tokenBudget > 0 {
		goal.set("tokenBudget", float64(int64(tokenBudget)))
	}
	ledger := []*jsObject{ledgerEvent("goal_created", "goalId", gstr(goal, "id"), "objective", gstr(goal, "objective"),
		"sisyphus", gflag(goal, "sisyphus"), "autoContinue", gflag(goal, "autoContinue"), "at", gstr(goal, "createdAt"))}
	if tl := goal.obj("taskList"); tl != nil {
		tasks, _ := tl.vals["tasks"].([]any)
		ledger = append(ledger, ledgerEvent("task_list_set", "goalId", gstr(goal, "id"), "taskCount", float64(len(tasks)),
			"blockCompletion", gflag(tl, "blockCompletion"), "at", gstr(goal, "createdAt")))
	}
	result, err := c.goalService.create(c.st, goal, ledger)
	if err != nil {
		c.h.Notify("Could not create the goal: "+err.Error(), "error")
		return
	}
	if result.focusChanged {
		c.appendFocusEntry(new(result.goalId), "created")
	}
	c.beginAccounting()
	budget := "none"
	if goal.has("tokenBudget") {
		budget = jsNumber(gnum(goal, "tokenBudget")) + " tokens"
	}
	c.h.Notify(buildGoalRunningNotification(config.objective, config.sisyphus, config.autoContinue)+"\nBudget: "+budget, "info")
	if g := c.state(); startNow && g != nil && gflag(g, "autoContinue") {
		c.scheduler.kickoff()
	}
}

// shutdown is the session_shutdown handler's work: persist, then clear the goal UI.
func (c *core) shutdown() {
	if c.state() != nil {
		c.persist()
	}
	c.clearGoalWidget()
}

// sessionStart is the session_start handler's work: load the goals, let the queued UI update run (the original awaits the load),
// then start accounting and restore the scheduler.
func (c *core) sessionStart() {
	c.loadState()
	c.flush()
	c.beginAccounting()
	c.scheduler.restore()
}
