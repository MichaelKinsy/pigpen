package pi_goal_x_test

// Twins of tests/e2e/goal-prompt-cache-sdk.test.ts and tests/e2e/network-recovery-rpc.test.ts. Both need a runtime this test binary does
// not have; the lead runs the Go equivalents as pigeq scenarios on the remote host.

import "testing"

func TestGoalEventsE2E(t *testing.T) {
	tskip(t, "e2e/goal-prompt-cache-sdk", "real SDK: explicit and implicit caching preserve request prefixes",
		"runs pi's own provider serializers (ModelRuntime.streamSimple/convertToLlm of the Node SDK); the Go side of the prefix property is covered by the goal-prompt-cache and goal-live-retention twins and TestBeforeProviderRequestMovesTheCacheMarker")
	tskip(t, "e2e/network-recovery-rpc", "e2e: real pi recovers from sustained 503 outages with escalating unbounded backoff",
		"spawns a live pi --mode rpc process against a mock HTTP provider; run as a pigeq scenario against pig on the remote host")
	tskip(t, "e2e/network-recovery-rpc", "e2e: real pi recovers from sustained 429 rate-limit outages",
		"spawns a live pi --mode rpc process against a mock HTTP provider; run as a pigeq scenario against pig on the remote host")
}
