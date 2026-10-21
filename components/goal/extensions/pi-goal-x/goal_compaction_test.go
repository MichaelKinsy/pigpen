package pi_goal_x

import (
	"strconv"
	"testing"
)

// Twins of tests/goal-compaction.test.ts.

func gpCompactionGoal(t *testing.T, id string, extra string) *jsObject {
	t.Helper()
	g := jo(t, `{"id":"g-test","objective":"=== Goal ===\nObjective: Build tests","status":"active","autoContinue":true,
		"usage":{"activeSeconds":120,"tokensUsed":5000},"sisyphus":false,"createdAt":"2024-01-01T00:00:00.000Z","updatedAt":"2024-01-01T00:00:00.000Z"}`)
	g.set("id", id)
	if extra != "" {
		for k, v := range jo(t, extra).vals {
			g.set(k, v)
		}
	}
	return g
}

func gpEvents(t *testing.T, events ...string) []*jsObject {
	t.Helper()
	out := make([]*jsObject, 0, len(events))
	for _, e := range events {
		out = append(out, jo(t, e))
	}
	return out
}

func TestGoalCompaction(t *testing.T) {
	const f = "goal-compaction"

	tw(t, f, "buildGoalCompactSummary includes status, objective, usage, and recent events", func(t *testing.T) {
		g := gpCompactionGoal(t, "g1", "")
		events := gpEvents(t,
			`{"type":"goal_created","goalId":"g1","objective":"o1","sisyphus":false,"autoContinue":true,"at":"2024-01-01T00:00:00.000Z"}`,
			`{"type":"goal_paused","goalId":"g1","reason":"missing tests","at":"2024-01-01T00:00:01.000Z"}`,
			`{"type":"goal_resumed","goalId":"g1","reason":"user","at":"2024-01-01T00:00:02.000Z"}`)
		summary := buildGoalCompactSummary(g, events, nil)
		assertMatch(t, summary, `g1`)
		assertMatch(t, summary, `running`)
		assertMatch(t, summary, `Build tests`)
		assertMatch(t, summary, `5K \(5,000\) tokens`)
		assertMatch(t, summary, `paused: missing tests`)
		assertMatch(t, summary, `resumed: user`)
	})

	tw(t, f, "buildGoalCompactSummary includes auditor rejection", func(t *testing.T) {
		g := gpCompactionGoal(t, "g1", "")
		events := gpEvents(t,
			`{"type":"completion_requested","goalId":"g1","summary":"done","at":"2024-01-01T00:00:00.000Z"}`,
			`{"type":"audit_result","goalId":"g1","verdict":"disapproved","report":"Tests are missing. Coverage is 0%.","at":"2024-01-01T00:00:01.000Z"}`)
		summary := buildGoalCompactSummary(g, events, nil)
		assertMatch(t, summary, `Auditor rejection`)
		assertMatch(t, summary, `Tests are missing`)
	})

	tw(t, f, "buildGoalCompactSummary does not include approved auditor result", func(t *testing.T) {
		g := gpCompactionGoal(t, "g1", "")
		events := gpEvents(t, `{"type":"audit_result","goalId":"g1","verdict":"approved","report":"All good","at":"2024-01-01T00:00:00.000Z"}`)
		assertNoMatch(t, buildGoalCompactSummary(g, events, nil), `Auditor rejection`)
	})

	tw(t, f, "buildCompactionSummary produces full session snapshot", func(t *testing.T) {
		g1 := gpCompactionGoal(t, "g1", `{"objective":"Goal A","status":"active"}`)
		g2 := gpCompactionGoal(t, "g2", `{"objective":"Goal B","status":"paused","pauseReason":"blocked"}`)
		goalsById := newPool()
		goalsById.set("g1", g1)
		goalsById.set("g2", g2)
		events := gpEvents(t,
			`{"type":"goal_created","goalId":"g1","objective":"Goal A","sisyphus":false,"autoContinue":true,"at":"2024-01-01T00:00:00.000Z"}`,
			`{"type":"goal_created","goalId":"g2","objective":"Goal B","sisyphus":false,"autoContinue":true,"at":"2024-01-01T00:00:01.000Z"}`,
			`{"type":"goal_created","goalId":"g3","objective":"Goal C","sisyphus":false,"autoContinue":true,"at":"2024-01-01T00:00:02.000Z"}`,
			`{"type":"goal_completed","goalId":"g3","at":"2024-01-01T00:00:03.000Z"}`)
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: goalsById, focusedGoalId: sptr("g1"), ledgerEvents: events})
		assertMatch(t, summary, `FOCUSED GOAL`)
		assertMatch(t, summary, `Goal A`)
		assertMatch(t, summary, `OTHER OPEN GOALS`)
		assertMatch(t, summary, `Goal B`)
		assertMatch(t, summary, `TERMINAL GOALS`)
		assertMatch(t, summary, `g3`)
		assertMatch(t, summary, `Continue from the focused goal`)
	})

	tw(t, f, "buildCompactionSummary handles no goals", func(t *testing.T) {
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: newPool(), ledgerEvents: []*jsObject{}})
		assertMatch(t, summary, `NO GOALS`)
	})

	tw(t, f, "buildCompactionSummary caps open goals and events", func(t *testing.T) {
		goalsById := newPool()
		var events []*jsObject
		for i := 0; i < 25; i++ {
			id := "g" + strconv.Itoa(i)
			g := gpCompactionGoal(t, id, `{"objective":"Goal `+strconv.Itoa(i)+`","status":"active"}`)
			goalsById.set(id, g)
			events = append(events, jo(t, `{"type":"goal_created","goalId":"`+id+`","objective":"Goal `+strconv.Itoa(i)+`","sisyphus":false,"autoContinue":true,"at":"2024-01-01T00:00:00.000Z"}`))
		}
		five, two := 5, 2
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: goalsById, focusedGoalId: sptr("g0"), ledgerEvents: events, capOpenGoals: &five, capEventsPerGoal: &two})
		// Should show focused goal + 5 other open + "and 19 more"
		assertMatch(t, summary, `and 19 more`)
	})

	tw(t, f, "buildCompactionSummary handles focused goal with pause reason", func(t *testing.T) {
		g := gpCompactionGoal(t, "g1", `{"status":"paused","pauseReason":"missing dependency","pauseSuggestedAction":"install libfoo"}`)
		events := gpEvents(t, `{"type":"goal_paused","goalId":"g1","reason":"missing dependency","suggestedAction":"install libfoo","at":"2024-01-01T00:00:00.000Z"}`)
		summary := buildGoalCompactSummary(g, events, nil)
		assertMatch(t, summary, `Pause reason: missing dependency`)
		assertMatch(t, summary, `Suggested action: install libfoo`)
	})
}
