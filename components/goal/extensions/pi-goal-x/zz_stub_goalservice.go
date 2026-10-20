package pi_goal_x

// Temporary stubs for symbols owned by other slices (deleted at integration).
// Every stub is a minimal working placeholder (not a panic) so that this slice's tests run; the real owners replace them.

// owned by GoalLedger (storage/goal-lock.ts, goal-ledger.ts)
type goalAppendResult struct {
	ok  bool
	err error
}

func acquireGoalLock(ctx storage, goalID string, attempts, retryMs int) (*goalLock, error) {
	return ctx.acquireLock(goalID, attempts, retryMs)
}

func appendGoalEvents(ctx storage, events []*jsObject) goalAppendResult {
	err := ctx.appendGoalEvents(events)
	return goalAppendResult{ok: err == nil, err: err}
}

func appendGoalEvent(ctx storage, event *jsObject) goalAppendResult {
	return appendGoalEvents(ctx, []*jsObject{event})
}

func goalLedgerPath(ctx storage) string { return ctx.mustPath(ledgerFile) }

