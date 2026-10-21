package pi_goal_x

import "testing"

// Twins of tests/network-error-backoff.test.ts.

func TestNetworkErrorBackoff(t *testing.T) {
	const f = "network-error-backoff"
	plan := func(p *networkErrorBackoffPlanRecord) any {
		if p == nil {
			return nil
		}
		return []float64{p.attempt, p.maxAttempts, p.delayMs}
	}
	want := func(attempt, maxAttempts, delayMs float64) any { return []float64{attempt, maxAttempts, delayMs} }

	tw(t, f, "network-error backoff escalates and plateaus; default policy is unbounded", func(t *testing.T) {
		eq(t, networkErrorBackoffDelaysMs, []float64{5_000, 10_000, 20_000, 40_000, 80_000}, "delays")
		eq(t, plan(networkErrorBackoffPlan(1, nil)), want(1, 0, 5_000), "attempt 1")
		eq(t, plan(networkErrorBackoffPlan(5, nil)), want(5, 0, 80_000), "attempt 5")
		eq(t, plan(networkErrorBackoffPlan(0, nil)), nil, "attempt 0")
		// Unbounded default: the ladder plateaus at the max delay forever.
		eq(t, plan(networkErrorBackoffPlan(6, nil)), want(6, 0, 80_000), "attempt 6")
		eq(t, plan(networkErrorBackoffPlan(50, nil)), want(50, 0, 80_000), "attempt 50")
	})

	tw(t, f, "network-error backoff honors a configured bounded cap and custom plateau", func(t *testing.T) {
		bounded := &networkErrorRecoveryPolicy{maxAttempts: 3, maxDelayMs: 15_000}
		eq(t, plan(networkErrorBackoffPlan(1, bounded)), want(1, 3, 5_000), "bounded 1")
		eq(t, plan(networkErrorBackoffPlan(2, bounded)), want(2, 3, 10_000), "bounded 2")
		eq(t, plan(networkErrorBackoffPlan(3, bounded)), want(3, 3, 15_000), "ladder capped by maxDelayMs")
		eq(t, plan(networkErrorBackoffPlan(4, bounded)), nil, "bounded cap exhausts")
		eq(t, plan(networkErrorBackoffPlan(9, &networkErrorRecoveryPolicy{maxAttempts: 0, maxDelayMs: 1_000})), want(9, 0, 1_000), "custom plateau applies indefinitely when unbounded")
	})

	tw(t, f, "network-error detection accepts provider error text and raw finish reason only", func(t *testing.T) {
		msg := func(s string) any { return jo(t, s) }
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason: network_error"}`)), true, "network_error text")
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"error","rawStopReason":"network-error"}`)), true, "raw stop reason")
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason: content_filter"}`)), false, "content_filter")
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"stop","errorMessage":"network_error"}`)), false, "non-error stop")
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason: error"}`)), true, "finish_reason: error")
		eq(t, isNetworkErrorAssistantMessage(msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason:  error"}`)), true, "double space")
		eq(t, hasNetworkErrorAssistantMessage([]any{
			msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason: error"}`),
			msg(`{"role":"assistant","stopReason":"error","errorMessage":"Provider finish_reason: content_filter"}`),
		}), true, "any network error")
	})
}
