package pi_goal_x

// ported from storage/goal-lock.ts and the remainder of storage/goal-files.ts that storage.go does not hold.

// acquireGoalLock takes the exclusive per-goal lock; attempts and retryMs of 0 are the defaults (8 x 1 ms). The error is
// the TS throw after the bounded attempt window. The caller must release() the lock.
func acquireGoalLock(ctx storage, goalID string, attempts, retryMs int) (*goalLock, error) {
	return ctx.acquireLock(goalID, attempts, retryMs)
}

// readActiveGoalFiles is the active goals as a list: the cached pool when there is one, else an uncached scan that fills it.
func (s storage) readActiveGoalFiles() []*jsObject {
	if pool := poolCache[s.root()]; pool != nil {
		return pool.values()
	}
	goals := s.scanActiveGoalFiles()
	pool := newPool()
	for _, g := range goals {
		pool.set(gstr(g, "id"), g)
	}
	poolCache[s.root()] = pool
	return goals
}
