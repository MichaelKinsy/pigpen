package pi_goal_x

import (
	"sync"
	"time"
)

// ported from goal-runtime.ts: continuation scheduling, stale-checkpoint state, the turn-stop guard and the one-time
// steering reminders. Timers fire on their own goroutines, so the state is guarded by a mutex that is never held while a
// hook runs (hooks may call back into the runtime).

const continuationIdleRetryMs = 50

// goalRuntimeCtx is the slice of the extension context the runtime reads. An error models the TS ctx throwing.
type goalRuntimeCtx interface {
	isIdle() (bool, error)
	hasPendingMessages() (bool, error)
}

// goalRuntimeHooks binds the runtime to the extension's state. Optional hooks may be nil.
type goalRuntimeHooks struct {
	// authorize returns the dispatch authorization (generation, dispatchId) or nil to refuse.
	authorize func(ctx goalRuntimeCtx, goal *jsObject) *jsObject
	recover   func(ctx goalRuntimeCtx)
	// dispatchFailed runs when sendFollowUp fails.
	dispatchFailed func(ctx goalRuntimeCtx)
	// sendFollowUp dispatches a hidden follow-up checkpoint message (pi.sendMessage + triggerTurn).
	sendFollowUp func(content string, details *jsObject) error
	// getGoal is the current focused goal (state.goal), or nil.
	getGoal func() *jsObject
	// isActionable reports whether a checkpointed goal id is still actionable (active + autoContinue).
	isActionable func(goalID string) bool
}

type turnStop struct {
	goalID  string
	turnSeq int
}

type goalRuntime struct {
	hooks goalRuntimeHooks
	// afterFunc schedules a timer; tests may replace it with a deterministic scheduler.
	afterFunc func(d time.Duration, f func()) *time.Timer

	mu                       sync.Mutex
	continuationQueuedFor    *string
	continuationScheduledFor *string
	continuationTimer        *time.Timer
	networkErrorRetryGoalID  *string
	networkErrorRetryAttempt float64
	networkErrorRetryTimer   *time.Timer
	turnSeq                  int
	turnStoppedFor           *turnStop
	checkpointGoalID         *string
	// checkpointSeq is the monotonic per-session counter persisted on v2 checkpoint details (issue #30).
	checkpointSeq              float64
	postCompactReminderPending bool
	postBudgetReminderPending  bool
}

func newGoalRuntime(hooks goalRuntimeHooks) *goalRuntime {
	return &goalRuntime{hooks: hooks, afterFunc: time.AfterFunc}
}

func sameID(p *string, id string) bool { return p != nil && *p == id }

func (r *goalRuntime) clearContinuationState(resetNetworkErrorBackoff bool) {
	r.mu.Lock()
	r.clearContinuationTimerLocked()
	r.continuationQueuedFor = nil
	r.mu.Unlock()
	if resetNetworkErrorBackoff {
		r.clearNetworkErrorBackoff()
	}
}

// clearContinuationTimer clears the pending timer but keeps the queued marker (used at session shutdown).
func (r *goalRuntime) clearContinuationTimer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearContinuationTimerLocked()
}

func (r *goalRuntime) clearContinuationTimerLocked() {
	if r.continuationTimer != nil {
		r.continuationTimer.Stop()
		r.continuationTimer = nil
	}
	r.continuationScheduledFor = nil
}

// continuationPendingFor reports whether a continuation is queued or scheduled for this goal id.
func (r *goalRuntime) continuationPendingFor(goalID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingLocked(goalID)
}

func (r *goalRuntime) pendingLocked(goalID string) bool {
	return sameID(r.continuationQueuedFor, goalID) || sameID(r.continuationScheduledFor, goalID)
}

// queueContinuation schedules the next auto-continuation for the focused active goal. Only active + autoContinue goals
// queue; force bypasses the already-queued/scheduled dedup (used right after creation/resume).
func (r *goalRuntime) queueContinuation(ctx goalRuntimeCtx, goal *jsObject, force bool) {
	if gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") {
		return
	}
	goalID := gstr(goal, "id")
	if !force && r.continuationPendingFor(goalID) {
		return
	}
	r.clearContinuationTimer()
	delay := continuationIdleRetryMs
	idle, err := ctx.isIdle()
	if err != nil {
		return
	}
	pending, err := ctx.hasPendingMessages()
	if err != nil {
		return
	}
	if idle && !pending {
		delay = 0
	}
	r.mu.Lock()
	r.continuationScheduledFor = &goalID
	r.continuationTimer = r.afterFunc(time.Duration(delay)*time.Millisecond, func() { r.sendQueuedContinuation(ctx, goalID) })
	r.mu.Unlock()
}

// cancelContinuationFor cancels a pending continuation for a goal id (after update/clear/focus change).
func (r *goalRuntime) cancelContinuationFor(goalID string) {
	r.mu.Lock()
	if sameID(r.continuationQueuedFor, goalID) {
		r.continuationQueuedFor = nil
	}
	scheduled := sameID(r.continuationScheduledFor, goalID)
	retry := sameID(r.networkErrorRetryGoalID, goalID)
	r.mu.Unlock()
	if scheduled {
		r.clearContinuationState(true)
	}
	if retry {
		r.clearNetworkErrorBackoff()
	}
}

// scheduleNetworkErrorRetry schedules the next bounded recovery after Pi's built-in provider retries have failed. The
// counter stays in memory and is cleared on a successful turn or any user-owned cancellation path.
func (r *goalRuntime) scheduleNetworkErrorRetry(ctx goalRuntimeCtx, goal *jsObject, policy *networkErrorRecoveryPolicy) *networkErrorBackoffPlanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gstr(goal, "status") != "active" || !gflag(goal, "autoContinue") || r.networkErrorRetryTimer != nil {
		return nil
	}
	goalID := gstr(goal, "id")
	if !sameID(r.networkErrorRetryGoalID, goalID) {
		r.networkErrorRetryGoalID = &goalID
		r.networkErrorRetryAttempt = 0
	}
	plan := networkErrorBackoffPlan(r.networkErrorRetryAttempt+1, policy)
	if plan == nil {
		return nil
	}
	r.networkErrorRetryAttempt = plan.attempt
	r.networkErrorRetryTimer = r.afterFunc(time.Duration(plan.delayMs)*time.Millisecond, func() {
		r.mu.Lock()
		r.networkErrorRetryTimer = nil
		r.mu.Unlock()
		if !r.hooks.isActionable(goalID) {
			return
		}
		current := r.hooks.getGoal()
		if current == nil || gstr(current, "id") != goalID {
			return
		}
		if r.hooks.recover != nil {
			r.hooks.recover(ctx)
		}
	})
	return plan
}

// clearNetworkErrorBackoff cancels and forgets all goal-level network-error recovery state.
func (r *goalRuntime) clearNetworkErrorBackoff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.networkErrorRetryTimer != nil {
		r.networkErrorRetryTimer.Stop()
	}
	r.networkErrorRetryTimer = nil
	r.networkErrorRetryGoalID = nil
	r.networkErrorRetryAttempt = 0
}

func (r *goalRuntime) dropQueuedMarker(goalID string) {
	r.mu.Lock()
	if sameID(r.continuationQueuedFor, goalID) {
		r.continuationQueuedFor = nil
	}
	r.mu.Unlock()
}

// sendQueuedContinuation (issue #30): the delivered follow-up triggers the turn but carries no goal state. The persisted
// content is a tiny v2 marker and the details a bounded structured record; before_agent_start injects the authoritative
// full prompt once per turn.
func (r *goalRuntime) sendQueuedContinuation(ctx goalRuntimeCtx, scheduledGoalID string) {
	r.mu.Lock()
	r.continuationTimer = nil
	r.continuationScheduledFor = nil
	r.mu.Unlock()
	if !r.hooks.isActionable(scheduledGoalID) {
		r.dropQueuedMarker(scheduledGoalID)
		return
	}

	idle, err := ctx.isIdle()
	pending := false
	if err == nil {
		pending, err = ctx.hasPendingMessages()
	}
	if err != nil {
		r.dropQueuedMarker(scheduledGoalID)
		return
	}
	// TS evaluates !hasPendingMessages() && isIdle(): a throwing isIdle is not reached when messages are pending; both are
	// pure reads here, so the order is not observable.
	if pending || !idle {
		r.mu.Lock()
		r.continuationScheduledFor = &scheduledGoalID
		r.continuationTimer = r.afterFunc(continuationIdleRetryMs*time.Millisecond, func() { r.sendQueuedContinuation(ctx, scheduledGoalID) })
		r.mu.Unlock()
		return
	}
	goal := r.hooks.getGoal()
	if goal == nil || gstr(goal, "id") != scheduledGoalID {
		r.dropQueuedMarker(scheduledGoalID)
		return
	}
	var authorization *jsObject
	if r.hooks.authorize != nil {
		authorization = r.hooks.authorize(ctx, goal)
	}
	if authorization == nil {
		return
	}
	goalID := gstr(goal, "id")
	r.mu.Lock()
	r.checkpointSeq++
	r.continuationQueuedFor = &goalID
	seq := r.checkpointSeq
	r.mu.Unlock()
	generation, _ := authorization.get("generation")
	dispatchID, _ := authorization.get("dispatchId")
	details := newObject()
	details.set("version", 3.0)
	details.set("generation", runtimeString(generation))
	details.set("dispatchId", runtimeString(dispatchID))
	details.set("kind", "checkpoint")
	details.set("goalId", goalID)
	details.set("status", "active")
	details.set("revision", gnum(goal, "revision"))
	details.set("checkpointSeq", seq)
	details.set("timestamp", float64(clockMs()))
	if err := r.hooks.sendFollowUp(checkpointTriggerPrompt(goalID), details); err != nil && r.hooks.dispatchFailed != nil {
		r.hooks.dispatchFailed(ctx)
	}
}

// runtimeString is JavaScript String(value) for the JSON values an authorization can hold.
func runtimeString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return jsNumber(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	}
	return "undefined"
}

// ── turn-stop guard ──

func (r *goalRuntime) advanceTurn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnSeq++
	if r.turnStoppedFor != nil && r.turnStoppedFor.turnSeq != r.turnSeq {
		r.turnStoppedFor = nil
	}
}

// markTurnStopped marks the current turn stopped after a terminal/mutating goal tool.
func (r *goalRuntime) markTurnStopped(goalID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnStoppedFor = &turnStop{goalID: goalID, turnSeq: r.turnSeq}
}

// currentTurnStoppedGoalId is the goal id that stopped the current turn, or nil. Stale markers are dropped.
func (r *goalRuntime) currentTurnStoppedGoalId() *string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnStoppedFor == nil {
		return nil
	}
	if r.turnStoppedFor.turnSeq != r.turnSeq {
		r.turnStoppedFor = nil
		return nil
	}
	id := r.turnStoppedFor.goalID
	return &id
}

// ── stale checkpoint state ──

func (r *goalRuntime) setCheckpoint(goalID *string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkpointGoalID = goalID
}

func (r *goalRuntime) getCheckpointGoalId() *string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkpointGoalID
}

// isStaleCheckpointBlocked: tools blocked when a stale checkpoint triggered the current turn.
func (r *goalRuntime) isStaleCheckpointBlocked(toolName string) bool {
	for _, allowed := range postStopAllowedTools {
		if allowed == toolName {
			return false
		}
	}
	return true
}

// ── one-time steering reminders ──

func (r *goalRuntime) armPostCompactReminder() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postCompactReminderPending = true
}

// isPostCompactReminderPending is a read-only check.
func (r *goalRuntime) isPostCompactReminderPending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.postCompactReminderPending
}

func (r *goalRuntime) clearPostCompactReminder() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postCompactReminderPending = false
}

// consumePostCompactReminder is true once if a post-compaction reminder is pending; it clears it.
func (r *goalRuntime) consumePostCompactReminder() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	was := r.postCompactReminderPending
	r.postCompactReminderPending = false
	return was
}

func (r *goalRuntime) armPostBudgetReminder() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postBudgetReminderPending = true
}

// consumePostBudgetReminder is true once if a post-budget-limit reminder is pending; it clears it.
func (r *goalRuntime) consumePostBudgetReminder() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	was := r.postBudgetReminderPending
	r.postBudgetReminderPending = false
	return was
}
