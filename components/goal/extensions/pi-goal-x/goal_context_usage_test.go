package pi_goal_x

import (
	"math"
	"testing"
)

// Twins of tests/goal-context-usage.test.ts.

func TestGoalContextUsage(t *testing.T) {
	const f = "goal-context-usage"

	tw(t, f, "context occupancy never falls back to cumulative usage or invented zero", func(t *testing.T) {
		nan := math.NaN()
		for _, usage := range []*goalContextUsage{nil, {tokens: nil, contextWindow: 200000}, {tokens: &nan, contextWindow: 200000}, {tokens: fptr(10), contextWindow: 0}} {
			assertEq(t, contextUsageLine(usage), "Context snapshot: unavailable", "unavailable")
		}
		assertEq(t, contextUsageLine(&goalContextUsage{tokens: fptr(100000), contextWindow: 250000}), "Context snapshot: 100000/250000 tokens (40%)", "40%")
		assertEq(t, contextUsageLine(&goalContextUsage{tokens: fptr(0), contextWindow: 250000}), "Context snapshot: 0/250000 tokens (0%)", "0%")
	})

	tw(t, f, "changing and removing budget or run limits clears historical policy", func(t *testing.T) {
		goal := createGoal("Continue work", true, false, false, clockMs())
		goal.set("tokenBudget", 100.0)
		goal.obj("usage").set("tokensUsed", 20.0)
		retention := newLiveTailRetention()
		base := []any{jo(t, `{"role":"user","content":"start"}`)}
		first := goalPromptParts(goal, gpSettings(fptr(10)), nil)
		retention.apply("s", base, &freshLiveTails{state: first.state, counters: &first.counters})
		objDelete(goal, "tokenBudget")
		parts := goalPromptParts(goal, &goalSettings{showAutonomousRuns: true}, &goalContextUsage{tokens: fptr(20), contextWindow: 1000})
		out := retention.apply("s", append(append([]any{}, base...), jo(t, `{"role":"assistant","content":"work"}`)), &freshLiveTails{state: parts.state, counters: &parts.counters})
		assertEq(t, len(out.transientContents), 2, "two transient contents")
		assertMatch(t, out.transientContents[0], `Limits: lifetime tokens=none; runs=unlimited`)
		joined := out.transientContents[0] + "\n" + out.transientContents[1]
		assertNoMatch(t, joined, `tokens=100|runs=10`)
		line, _ := modelBudgetLine(jo(t, `{"tokenBudget":1,"usage":{"tokensUsed":2}}`))
		assertMatch(t, line, `spending cap, not context capacity`)
	})
}
