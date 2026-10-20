package pi_goal_x

// What the GoalEvents twins (goal_events_rig_test.go) need from the package's internals, beyond export_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// SaveGoalSettingsForRig is saveGoalSettingsFileConfig: it replaces the project settings file and drops the read cache.
func SaveGoalSettingsForRig(cwd, content string) {
	p := filepath.Join(cwd, ".pi", "pi-goal-x-settings.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(content), 0o644)
	invalidateGoalSettingsCache()
}

// WriteGoalForRig is writeActiveGoalFile({cwd}, createGoal(...)) with a token budget and usage preset; it returns the written goal.
func WriteGoalForRig(cwd, objective string, atMs int64, tokenBudget, tokensUsed float64) map[string]any {
	g := createGoal(objective, true, false, false, atMs)
	if tokenBudget > 0 {
		g.set("tokenBudget", tokenBudget)
	}
	if tokensUsed > 0 {
		usage := g.obj("usage").clone()
		usage.set("tokensUsed", tokensUsed)
		g.set("usage", usage)
	}
	invalidateGoalPoolCache()
	written, err := (storage{cwd: cwd}).writeActiveGoalFile(g)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(marshalJSON(written, "")), &out)
	return out
}

// SchedulerDeclareReady is core.scheduler.declare(ctx, {kind: "ready"}).terminate.
func (p *Probe) SchedulerDeclareReady() (terminate bool) {
	p.c.locked(func() { terminate = p.c.scheduler.declare(goalContinuation{kind: "ready"}).terminate })
	return terminate
}

// AccountProgress is core.accountProgress(ctx, {completedTurnTokens}).
func (p *Probe) AccountProgress(tokens float64) {
	p.c.locked(func() { p.c.accountProgress(tokens) })
}

// SetTokenBudget is core.goalService.apply(ctx, {mutate: g => ({...g, tokenBudget})}).
func (p *Probe) SetTokenBudget(budget float64) {
	p.c.locked(func() {
		_, _ = p.c.goalService.apply(p.c.st, goalMutationSpec{mutate: func(g *jsObject) *jsObject { return goalWith(g, "tokenBudget", budget) }})
	})
}

// Teardown is `core.scheduler.shutdown(); core.clearContinuationState()`.
func (p *Probe) Teardown() {
	p.c.locked(func() {
		p.c.scheduler.shutdown()
		p.c.clearContinuationState(true)
	})
}

// WritePatchedGoalForRig is WriteGoalForRig with arbitrary top-level fields (decoded JSON values) set on the goal.
func WritePatchedGoalForRig(cwd, objective string, atMs int64, patch map[string]any) map[string]any {
	g := createGoal(objective, true, false, false, atMs)
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g.set(k, eventJS(patch[k]))
	}
	invalidateGoalPoolCache()
	written, err := (storage{cwd: cwd}).writeActiveGoalFile(g)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(marshalJSON(written, "")), &out)
	return out
}

// ArmOracleAdviceForRig arms follow-up tracking for the goal, as a delivered Oracle advice does.
func ArmOracleAdviceForRig(goalID, fingerprint, adviceID string) {
	armedByGoalID[goalID] = armedAdvice{goalID: goalID, fingerprint: fingerprint, adviceID: adviceID}
}

// CompleteFocused marks the focused goal complete in memory only: the state update_goal leaves for the deferred archival at turn_end.
func (p *Probe) CompleteFocused() {
	p.c.locked(func() { p.c.setGoal(goalWith(p.c.state(), "status", "complete"), false, "") })
}

// InvalidateLedgerForRig drops the ledger read cache (a file the test wrote by hand).
func InvalidateLedgerForRig() { invalidateGoalLedgerCache() }
