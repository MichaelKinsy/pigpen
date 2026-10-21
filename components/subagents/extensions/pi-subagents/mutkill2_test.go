package pi_subagents

import "testing"

// A failed wait is a failed tool call (the host drops the details of a failed result); a wait that ended is a result.
func TestWaitToolResultOfAFailedWaitIsAToolError(t *testing.T) {
	res, err := waitToolResult(WaitResult{Text: "provider 'patty' disappeared", IsError: true})
	if err == nil || err.Error() != "provider 'patty' disappeared" || res != nil {
		t.Errorf("failed wait: %v, %v", res, err)
	}
	res, err = waitToolResult(WaitResult{Text: "done."})
	if err != nil || res == nil {
		t.Errorf("wait: %v, %v", res, err)
	}
}

// details.spawnBudget is the original's snapshot: null for an absent limit, the grant history with its previous limit.
func TestSpawnBudgetDetails(t *testing.T) {
	configured, limit, remaining, grantRemaining := 4, 5, 3, 3
	got := marshalJSON(spawnBudgetDetails(spawnBudgetSnapshot{Used: 2, ConfiguredLimit: &configured, Granted: 1, Limit: &limit, Remaining: &remaining, GrantRemaining: &grantRemaining,
		GrantHistory: []grantRecord{{SessionID: "s", Amount: 1, GrantedAt: 7, PreviousLimit: 4, Limit: 5}}}), "")
	eq(t, got, `{"used":2,"configuredLimit":4,"granted":1,"limit":5,"remaining":3,"grantRemaining":3,"grantHistory":[{"sessionId":"s","amount":1,"grantedAt":7,"previousLimit":4,"limit":5}]}`, "limited")
	got = marshalJSON(spawnBudgetDetails(spawnBudgetSnapshot{Used: 2, Granted: 0}), "")
	eq(t, got, `{"used":2,"configuredLimit":null,"granted":0,"limit":null,"remaining":null,"grantRemaining":null,"grantHistory":[]}`, "unlimited")
}
