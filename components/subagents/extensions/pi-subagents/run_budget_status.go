package pi_subagents

import "fmt"

// spawnBudgetDetails is details.spawnBudget: the original's SpawnBudgetSnapshot (an absent limit is null).
func spawnBudgetDetails(snap spawnBudgetSnapshot) *jsObject {
	budget := obj("used", float64(snap.Used))
	setLimit := func(k string, v *int) {
		if v == nil {
			budget.set(k, nil)
		} else {
			budget.set(k, float64(*v))
		}
	}
	setLimit("configuredLimit", snap.ConfiguredLimit)
	budget.set("granted", float64(snap.Granted))
	setLimit("limit", snap.Limit)
	setLimit("remaining", snap.Remaining)
	setLimit("grantRemaining", snap.GrantRemaining)
	history := []any{}
	for _, g := range snap.GrantHistory {
		history = append(history, obj("sessionId", g.SessionID, "amount", float64(g.Amount), "grantedAt", float64(g.GrantedAt), "previousLimit", float64(g.PreviousLimit), "limit", float64(g.Limit)))
	}
	budget.set("grantHistory", history)
	return budget
}

// statusBudget is withSpawnBudgetStatus (subagent-executor.ts): the two budget lines a status answer starts with and the
// spawnBudget and activeAsyncCapacity details it carries.
func statusBudget(sessionID string) (lines string, spawnBudget, capacity *jsObject) {
	cfg := loadAsyncConfig()
	runPolicyMu.Lock()
	snap := getSpawnBudgetSnapshot(runState, loadExtensionConfig(), sessionID, osEnv)
	runPolicyMu.Unlock()
	used := capacitySnapshotFor(sessionID, cfg.MaxActiveAsync, CapacityOptions{}.root())
	if sessionID != "" {
		used = reconcileActiveAsyncCapacity(sessionID, cfg.MaxActiveAsync, CapacityOptions{AbandonedSlotReleaseAfterMs: cfg.AbandonedSlotRelease})
	}
	limit := "unlimited"
	if used.Limit > 0 {
		limit = fmt.Sprint(used.Limit)
	}
	return formatSpawnBudget(snap) + fmt.Sprintf("\nActive async capacity: %d/%s used", used.Used, limit),
		spawnBudgetDetails(snap), obj("used", float64(used.Used), "limit", float64(used.Limit))
}
