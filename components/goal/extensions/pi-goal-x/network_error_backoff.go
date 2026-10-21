package pi_goal_x

import "math"

// ported from network-error-backoff.ts

// networkErrorBackoffDelaysMs is the escalating delay ladder (ms) that plateaus at its last entry.
var networkErrorBackoffDelaysMs = []float64{5_000, 10_000, 20_000, 40_000, 80_000}

// networkErrorRecoveryPolicy: maxAttempts 0 = unbounded (default); delays plateau at maxDelayMs.
type networkErrorRecoveryPolicy struct {
	maxAttempts float64
	maxDelayMs  float64
}

var defaultNetworkErrorRecoveryPolicy = networkErrorRecoveryPolicy{
	maxAttempts: 0,
	maxDelayMs:  networkErrorBackoffDelaysMs[len(networkErrorBackoffDelaysMs)-1],
}

type networkErrorBackoffPlanRecord struct {
	attempt float64
	// maxAttempts 0 = unbounded.
	maxAttempts float64
	delayMs     float64
}

func rawLadderDelay(attempt float64) float64 {
	last := float64(len(networkErrorBackoffDelaysMs) - 1)
	return networkErrorBackoffDelaysMs[int(math.Min(attempt-1, last))]
}

// networkErrorBackoffPlan returns the recovery plan for a one-based attempt under the policy (nil policy = default
// unbounded), or nil once a configured bounded cap is exhausted.
func networkErrorBackoffPlan(attempt float64, policy *networkErrorRecoveryPolicy) *networkErrorBackoffPlanRecord {
	p := defaultNetworkErrorRecoveryPolicy
	if policy != nil {
		p = *policy
	}
	if attempt != math.Trunc(attempt) || math.IsInf(attempt, 0) || math.IsNaN(attempt) || attempt < 1 {
		return nil
	}
	if p.maxAttempts > 0 && attempt > p.maxAttempts {
		return nil
	}
	return &networkErrorBackoffPlanRecord{attempt: attempt, maxAttempts: p.maxAttempts, delayMs: math.Min(rawLadderDelay(attempt), p.maxDelayMs)}
}
