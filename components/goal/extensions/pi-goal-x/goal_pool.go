package pi_goal_x

// ported from goal-pool.ts (the part not in format.go / core.go: openGoalsFromPool, otherOpenGoalCount, resolveSessionFocus,
// goalSelectorLabel, buildGoalListText, buildUnfocusedOpenGoalsSummary live there)

// goalPoolFromGoals pools the goals that are not complete, as clones.
func goalPoolFromGoals(goals []*jsObject) *goalPool {
	pool := newPool()
	for _, g := range goals {
		if gstr(g, "status") != "complete" {
			pool.set(gstr(g, "id"), cloneGoal(g))
		}
	}
	return pool
}

// focusedGoalFromPool returns nil when nothing is focused or the id is not pooled.
func focusedGoalFromPool(pool *goalPool, focusedGoalId *string) *jsObject {
	if focusedGoalId == nil || *focusedGoalId == "" {
		return nil
	}
	return pool.get(*focusedGoalId)
}

// mergeFocusedGoalWithDisk takes the disk goal with the larger usage of either side.
func mergeFocusedGoalWithDisk(memoryGoal, diskGoal *jsObject) *jsObject {
	mu, du := memoryGoal.obj("usage"), diskGoal.obj("usage")
	tokens := maxNum(gnumOf(mu, "tokensUsed"), gnumOf(du, "tokensUsed"))
	seconds := maxNum(gnumOf(mu, "activeSeconds"), gnumOf(du, "activeSeconds"))
	out := diskGoal.clone()
	usage := newObject()
	usage.set("tokensUsed", tokens)
	usage.set("activeSeconds", seconds)
	out.set("usage", usage)
	return out
}

func gnumOf(o *jsObject, k string) float64 {
	if o == nil {
		return 0
	}
	return gnum(o, k)
}

func maxNum(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
