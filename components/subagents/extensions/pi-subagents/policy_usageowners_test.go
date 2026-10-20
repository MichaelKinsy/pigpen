package pi_subagents

import "testing"

// Twins of test/unit/usage-reconciliation-owners.test.ts. Both cases drive the original's run owners (runSync and the detached
// runner's runChildSession) with a scripted ChildSessionFactory: an in-process Pi child session whose messages the owner reads
// back to reconcile terminal usage. The port's child is a pig process, so there is no in-process session factory to script. The
// reconciliation itself (live message usage against the persisted messages of the session) is twinned in
// usage-reconciliation (policy_budget_test.go), and the delegation update that drops usage when the counters are not all known is
// twinned in delegation-api ("omits usage from a structured delegation update when counters are absent or invalid").
func TestUsageReconciliationOwners(t *testing.T) {
	tskip(t, "usage-reconciliation-owners", "reconciles foreground runSync usage before its terminal progress update",
		"in-process child-session factory: runSync is given a scripted ChildSessionFactory (src/runs/shared/child-session.ts) and reads the session's persisted messages; the port runs a child pig process and has no such factory")
	tskip(t, "usage-reconciliation-owners", "reconciles background settlement after success and failure",
		"in-process child-session factory: runChildSession (src/runs/background/run-child-session.ts) settles a scripted ChildSessionFactory session; the detached runner of the port supervises a child pig process instead")
}
