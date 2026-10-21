package pi_goal_x

import "math"

// validateTokenBudgetInput is goal-record.ts validateTokenBudgetInput: ok value, or a user-facing message.
func validateTokenBudgetInput(value any) (float64, string) {
	f, ok := value.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, "token_budget must be a number."
	}
	if !isSafeInteger(f) {
		return 0, "token_budget must be a whole safe integer (fractional values are not accepted)."
	}
	if f < 1 {
		return 0, "token_budget must be at least 1."
	}
	return f, ""
}
