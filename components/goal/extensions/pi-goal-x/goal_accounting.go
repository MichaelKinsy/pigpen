package pi_goal_x

import "math"

// ported from goal-accounting.ts

// accountingCharge is the tokens and whole seconds accumulated since the previous charge.
type accountingCharge struct {
	tokens  float64
	seconds float64
}

// accountingChargeOpts mirrors charge({now?, completedTurnTokens?}); a nil now means Date.now().
type accountingChargeOpts struct {
	now                 *int64
	completedTurnTokens float64
}

// goalAccounting is the serialized, idempotent token/time accounting (class GoalAccounting).
type goalAccounting struct {
	activeGoalID    *string
	lastAccountedAt *int64
}

// begin starts accounting for a goal id (or clears it when nil or empty).
func (a *goalAccounting) begin(goalID *string) {
	if goalID != nil && *goalID == "" {
		goalID = nil
	}
	a.activeGoalID = goalID
	if goalID != nil {
		now := clockMs()
		a.lastAccountedAt = &now
	} else {
		a.lastAccountedAt = nil
	}
}

func (a *goalAccounting) clear() {
	a.activeGoalID = nil
	a.lastAccountedAt = nil
}

func (a *goalAccounting) goalId() *string { return a.activeGoalID }

// isActiveFor is true when accounting is active for exactly this goal.
func (a *goalAccounting) isActiveFor(goalID string) bool {
	return a.activeGoalID != nil && *a.activeGoalID == goalID && a.lastAccountedAt != nil
}

// charge returns the tokens plus whole elapsed seconds since the previous charge, then advances the baseline to now.
func (a *goalAccounting) charge(opts accountingChargeOpts) accountingCharge {
	now := clockMs()
	if opts.now != nil {
		now = *opts.now
	}
	elapsed := 0.0
	if a.lastAccountedAt != nil {
		elapsed = math.Max(0, math.Floor(float64(now-*a.lastAccountedAt)/1000))
	}
	a.lastAccountedAt = &now
	tokens := math.Max(0, math.Trunc(opts.completedTurnTokens))
	return accountingCharge{tokens: tokens, seconds: elapsed}
}

// liveSeconds is the read-only elapsed whole seconds for display; it does not advance the baseline.
func (a *goalAccounting) liveSeconds(now int64) float64 {
	if a.lastAccountedAt == nil {
		return 0
	}
	return math.Max(0, math.Floor(float64(now-*a.lastAccountedAt)/1000))
}

// budgetRemaining is the remaining token budget; ok is false when no (positive, finite) budget is set.
func budgetRemaining(goal *jsObject) (float64, bool) {
	budget, ok := goal.num("tokenBudget")
	if !ok || math.IsNaN(budget) || math.IsInf(budget, 0) || budget <= 0 {
		return 0, false
	}
	used := math.Max(0, math.Floor(budgetUsed(goal)))
	return math.Max(0, math.Floor(budget-used)), true
}

func budgetUsed(goal *jsObject) float64 {
	if u := goal.obj("usage"); u != nil {
		return gnum(u, "tokensUsed")
	}
	return 0
}

// budgetLine is the token-budget description for display/steering text; ok is false without a budget.
// (budgetReached lives in core.go with the same semantics.)
func budgetLine(goal *jsObject) (string, bool) {
	remaining, ok := budgetRemaining(goal)
	if !ok {
		return "", false
	}
	used := math.Floor(budgetUsed(goal))
	return "token budget: " + jsNumber(used) + "/" + jsNumber(math.Floor(gnum(goal, "tokenBudget"))) + " used, " + jsNumber(remaining) + " remaining", true
}

// modelBudgetLine: agent-facing accounting must never read as current context occupancy.
func modelBudgetLine(goal *jsObject) (string, bool) {
	line, ok := budgetLine(goal)
	if !ok {
		return "", false
	}
	return "Lifetime " + line + " (spending cap, not context capacity)", true
}

// goalContextUsage: tokens is nil when unknown.
type goalContextUsage struct {
	tokens        *float64
	contextWindow float64
}

func finiteNumber(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// contextUsageLine is a point-in-time observation; older retained observations are historical.
func contextUsageLine(usage *goalContextUsage) string {
	if usage == nil || usage.tokens == nil || !finiteNumber(*usage.tokens) || *usage.tokens < 0 ||
		!finiteNumber(usage.contextWindow) || usage.contextWindow <= 0 {
		return "Context snapshot: unavailable"
	}
	return "Context snapshot: " + jsNumber(jsRound(*usage.tokens)) + "/" + jsNumber(jsRound(usage.contextWindow)) + " tokens (" +
		jsNumber(jsRound(100**usage.tokens/usage.contextWindow)) + "%)"
}
