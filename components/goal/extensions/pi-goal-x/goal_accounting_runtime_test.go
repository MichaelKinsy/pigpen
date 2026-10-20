package pi_goal_x

import (
	"testing"
	"time"
)

// Twins of tests/goal-accounting-runtime.test.ts.

func gpMakeRuntime(isActionable func(string) bool, getGoal func() *jsObject) (*goalRuntime, *[]sentFollowUp) {
	sent := &[]sentFollowUp{}
	runtime := newGoalRuntime(goalRuntimeHooks{
		sendFollowUp: func(content string, details *jsObject) error {
			*sent = append(*sent, sentFollowUp{content, details})
			return nil
		},
		getGoal: func() *jsObject {
			if getGoal == nil {
				return nil
			}
			return getGoal()
		},
		isActionable: func(id string) bool { return isActionable != nil && isActionable(id) },
	})
	return runtime, sent
}

func gpRuntimeGoal(autoContinue bool) *jsObject {
	return createGoal("Runtime test", autoContinue, false, false, ms(2026, time.August, 2, 9, 0, 0))
}

func TestGoalAccountingRuntime(t *testing.T) {
	const f = "goal-accounting-runtime"
	i64 := func(n int64) *int64 { return &n }

	// ── GoalAccounting ──
	tw(t, f, "charges elapsed seconds and advances the baseline (idempotent)", func(t *testing.T) {
		acct := &goalAccounting{}
		acct.begin(sptr("g1"))
		// 10 seconds pass, 5 tokens.
		first := acct.charge(accountingChargeOpts{now: i64(clockMs() + 10_000), completedTurnTokens: 5})
		assertEq(t, first.seconds, 10.0, "seconds")
		assertEq(t, first.tokens, 5.0, "tokens")
		// Immediately charging again yields 0 seconds — no double-charge.
		second := acct.charge(accountingChargeOpts{now: i64(clockMs() + 10_000), completedTurnTokens: 3})
		assertEq(t, second.seconds, 0.0, "no double charge")
		assertEq(t, second.tokens, 3.0, "tokens")
	})

	tw(t, f, "never charges negative elapsed time", func(t *testing.T) {
		acct := &goalAccounting{}
		acct.begin(sptr("g1"))
		charge := acct.charge(accountingChargeOpts{now: i64(clockMs() - 5000)})
		assertEq(t, charge.seconds, 0.0, "seconds")
	})

	tw(t, f, "isActiveFor requires the exact goal id", func(t *testing.T) {
		acct := &goalAccounting{}
		acct.begin(sptr("g1"))
		assertTrue(t, acct.isActiveFor("g1"), "active for g1")
		assertTrue(t, !acct.isActiveFor("g2"), "not active for g2")
		acct.clear()
		assertTrue(t, !acct.isActiveFor("g1"), "cleared")
		assertTrue(t, acct.goalId() == nil, "goalId null")
	})

	tw(t, f, "liveSeconds is read-only display (does not advance the baseline)", func(t *testing.T) {
		acct := &goalAccounting{}
		acct.begin(sptr("g1"))
		now := clockMs()
		assertEq(t, acct.liveSeconds(now+7000), 7.0, "live seconds")
		// Baseline untouched: a subsequent charge still counts the full interval.
		charge := acct.charge(accountingChargeOpts{now: i64(now + 7000)})
		assertEq(t, charge.seconds, 7.0, "charge seconds")
	})

	// ── Budget helpers ──
	base := func(t *testing.T, extra string) *jsObject {
		return jo(t, `{`+extra+`"usage":{"tokensUsed":0}}`)
	}
	withBudget := func(t *testing.T, budget, used string) *jsObject {
		return jo(t, `{"tokenBudget":`+budget+`,"usage":{"tokensUsed":`+used+`}}`)
	}

	tw(t, f, "returns null when no budget is set", func(t *testing.T) {
		b := base(t, "")
		_, ok := budgetRemaining(b)
		assertTrue(t, !ok, "remaining null")
		assertTrue(t, !budgetReached(b), "not reached")
		_, ok = budgetLine(b)
		assertTrue(t, !ok, "line null")
	})

	tw(t, f, "computes remaining and reached", func(t *testing.T) {
		goal := withBudget(t, "100", "80")
		remaining, _ := budgetRemaining(goal)
		assertEq(t, remaining, 20.0, "remaining")
		assertTrue(t, !budgetReached(goal), "not reached")
		line, ok := budgetLine(goal)
		assertTrue(t, ok && containsStr(line, "80/100"), "line mentions 80/100")
		crossed := withBudget(t, "100", "100")
		remaining, _ = budgetRemaining(crossed)
		assertEq(t, remaining, 0.0, "crossed remaining")
		assertTrue(t, budgetReached(crossed), "reached")
	})

	tw(t, f, "ignores non-positive budgets", func(t *testing.T) {
		_, ok := budgetRemaining(base(t, `"tokenBudget":0,`))
		assertTrue(t, !ok, "zero budget ignored")
		_, ok = budgetRemaining(base(t, `"tokenBudget":-5,`))
		assertTrue(t, !ok, "negative budget ignored")
		assertTrue(t, !budgetReached(base(t, `"tokenBudget":0,`)), "zero budget never reached")
	})

	// ── GoalRuntime ──
	tw(t, f, "does not queue for non-active or non-autoContinue goals", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		paused := gpRuntimeGoal(true)
		paused.set("status", "paused")
		runtime.queueContinuation(mockRuntimeCtx{}, paused, true)
		manual := gpRuntimeGoal(true)
		manual.set("autoContinue", false)
		runtime.queueContinuation(mockRuntimeCtx{}, manual, true)
		assertTrue(t, !runtime.continuationPendingFor(gstr(paused, "id")), "nothing pending")
	})

	tw(t, f, "queues for an actionable goal and clears on cancel", func(t *testing.T) {
		goal := gpRuntimeGoal(true)
		runtime, _ := gpMakeRuntime(func(string) bool { return true }, func() *jsObject { return goal })
		// Hold the timer so it cannot fire before the assertions below.
		runtime.afterFunc = func(d time.Duration, fn func()) *time.Timer { return time.NewTimer(time.Hour) }
		runtime.queueContinuation(mockRuntimeCtx{}, goal, true)
		assertTrue(t, runtime.continuationPendingFor(gstr(goal, "id")), "scheduled")
		runtime.clearContinuationState(true)
		assertTrue(t, !runtime.continuationPendingFor(gstr(goal, "id")), "cleared")
	})

	// ── turn-stop guard ──
	tw(t, f, "markTurnStopped scopes to the current turn; advanceTurn drops stale markers", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		assertTrue(t, runtime.currentTurnStoppedGoalId() == nil, "none initially")
		runtime.markTurnStopped("g1")
		got := runtime.currentTurnStoppedGoalId()
		assertTrue(t, got != nil && *got == "g1", "g1 stopped")
		runtime.advanceTurn()
		assertTrue(t, runtime.currentTurnStoppedGoalId() == nil, "stale marker dropped")
	})

	tw(t, f, "isStaleCheckpointBlocked blocks work tools but allows get_goal", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		assertTrue(t, runtime.isStaleCheckpointBlocked("write"), "write blocked")
		assertTrue(t, runtime.isStaleCheckpointBlocked("bash"), "bash blocked")
		assertTrue(t, !runtime.isStaleCheckpointBlocked("get_goal"), "get_goal allowed")
	})

	// ── reminders ──
	tw(t, f, "post-compaction reminder is one-shot", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		assertTrue(t, !runtime.isPostCompactReminderPending(), "not pending")
		runtime.armPostCompactReminder()
		assertTrue(t, runtime.isPostCompactReminderPending(), "pending")
		runtime.clearPostCompactReminder()
		assertTrue(t, !runtime.isPostCompactReminderPending(), "cleared")
		runtime.armPostCompactReminder()
		assertTrue(t, runtime.consumePostCompactReminder(), "consumed once")
		assertTrue(t, !runtime.consumePostCompactReminder(), "second consume empty")
	})

	tw(t, f, "post-budget reminder is one-shot", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		assertTrue(t, !runtime.consumePostBudgetReminder(), "nothing to consume")
		runtime.armPostBudgetReminder()
		assertTrue(t, runtime.consumePostBudgetReminder(), "consumed once")
		assertTrue(t, !runtime.consumePostBudgetReminder(), "second consume empty")
	})

	// ── stale checkpoint state ──
	tw(t, f, "setCheckpoint/getCheckpointGoalId round-trip", func(t *testing.T) {
		runtime, _ := gpMakeRuntime(nil, nil)
		assertTrue(t, runtime.getCheckpointGoalId() == nil, "initially null")
		runtime.setCheckpoint(sptr("g9"))
		got := runtime.getCheckpointGoalId()
		assertTrue(t, got != nil && *got == "g9", "g9")
		runtime.setCheckpoint(nil)
		assertTrue(t, runtime.getCheckpointGoalId() == nil, "null again")
	})
}
