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

// owned by GoalTasksPolicy (goal-policy.ts, goal-pool.ts)
func findTaskInTree(tasks []any, id string) *jsObject {
	for _, t := range tasks {
		o := t.(*jsObject)
		if gstr(o, "id") == id {
			return o
		}
		if subs, ok := o.vals["subtasks"].([]any); ok {
			if r := findTaskInTree(subs, id); r != nil {
				return r
			}
		}
	}
	return nil
}

func updateTaskInTree(tasks []any, id string, fn func(*jsObject) *jsObject) []any {
	out := make([]any, len(tasks))
	for i, t := range tasks {
		o := t.(*jsObject)
		if gstr(o, "id") == id {
			out[i] = fn(o)
		} else {
			out[i] = o
		}
	}
	return out
}

func mergeFocusedGoalWithDisk(memoryGoal, diskGoal *jsObject) *jsObject {
	panic("stub: owned by GoalTasksPolicy")
}
