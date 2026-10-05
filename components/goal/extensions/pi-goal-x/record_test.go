package pi_goal_x

import (
	"regexp"
	"testing"
)

func TestGoalRecord(t *testing.T) {
	at := ms(2026, 1, 2, 3, 4, 5)
	objective := "=== Goal ===\nObjective: ship the refactor"
	tw(t, "goal-record", "createGoal builds stable goal records with fresh usage and requested mode", func(t *testing.T) {
		g := createGoal(objective, true, false, false, at)
		eq(t, gstr(g, "objective"), objective, "objective")
		eq(t, gstr(g, "status"), "active", "status")
		eq(t, gflag(g, "autoContinue"), true, "autoContinue")
		eq(t, gflag(g, "sisyphus"), false, "sisyphus")
		eq(t, show(g.obj("usage")), `{"tokensUsed":0,"activeSeconds":0}`, "usage")
		eq(t, gstr(g, "createdAt"), "2026-01-02T03:04:05.000Z", "createdAt")
		eq(t, gstr(g, "updatedAt"), "2026-01-02T03:04:05.000Z", "updatedAt")
		if !regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`).MatchString(gstr(g, "id")) {
			t.Errorf("id %q", gstr(g, "id"))
		}
	})
	tw(t, "goal-record", "normalizeGoalRecord preserves known fields while sanitizing unsafe or missing values", func(t *testing.T) {
		n := normalizeGoalRecord(jo(t, `{"id":"goal-123","objective":"  Keep behavior  ","status":"paused","stopReason":"agent","pauseReason":"blocked",
			"pauseSuggestedAction":"ask user","autoContinue":false,"usage":{"tokensUsed":12.9,"activeSeconds":7.2},"sisyphus":true,
			"activePath":".pi/goals/active.md","archivedPath":".pi/goals/archived/old.md","createdAt":"2026-02-03T04:05:06.000Z","updatedAt":"2026-02-03T04:06:06.000Z"}`))
		if n == nil {
			t.Fatal("nil")
		}
		eq(t, gstr(n, "id"), "goal-123", "id")
		eq(t, gstr(n, "objective"), "Keep behavior", "objective")
		eq(t, gstr(n, "status"), "paused", "status")
		eq(t, gstr(n, "stopReason"), "agent", "stopReason")
		eq(t, gstr(n, "pauseReason"), "blocked", "pauseReason")
		eq(t, gstr(n, "pauseSuggestedAction"), "ask user", "pauseSuggestedAction")
		eq(t, gflag(n, "autoContinue"), false, "autoContinue")
		eq(t, show(n.obj("usage")), `{"tokensUsed":12,"activeSeconds":7}`, "usage")
		eq(t, gflag(n, "sisyphus"), true, "sisyphus")
		eq(t, gstr(n, "activePath"), ".pi/goals/active.md", "activePath")
		eq(t, gstr(n, "archivedPath"), ".pi/goals/archived/old.md", "archivedPath")
		eq(t, gstr(n, "createdAt"), "2026-02-03T04:05:06.000Z", "createdAt")
		eq(t, gstr(n, "updatedAt"), "2026-02-03T04:06:06.000Z", "updatedAt")
	})
	tw(t, "goal-record", "cloneGoal returns a detached usage object", func(t *testing.T) {
		g := createGoal(objective, true, false, false, at)
		c := cloneGoal(g)
		c.obj("usage").set("tokensUsed", 500.0)
		eq(t, gnum(g.obj("usage"), "tokensUsed"), 0.0, "original")
		eq(t, gnum(c.obj("usage"), "tokensUsed"), 500.0, "clone")
	})
	tw(t, "goal-record", "normalizeGoalRecord with taskList present round-trips tasks", func(t *testing.T) {
		n := normalizeGoalRecord(jo(t, `{"id":"goal-task","objective":"Do stuff","status":"active","autoContinue":true,"usage":{"tokensUsed":0,"activeSeconds":0},"sisyphus":false,
			"taskList":{"tasks":[{"id":"t1","title":"Task 1","status":"complete"},{"id":"t2","title":"Task 2","status":"pending"},
			{"id":"t3","title":"Task 3","status":"skipped","skipReason":"N/A"}],"blockCompletion":true,"proposedAt":"2026-05-27T00:00:00.000Z"}}`))
		if n == nil || n.obj("taskList") == nil {
			t.Fatal("no task list")
		}
		tasks := n.obj("taskList").vals["tasks"].([]any)
		eq(t, len(tasks), 3, "tasks")
		eq(t, gstr(tasks[0].(*jsObject), "id"), "t1", "id")
		eq(t, gstr(tasks[0].(*jsObject), "status"), "complete", "status 0")
		eq(t, gstr(tasks[1].(*jsObject), "status"), "pending", "status 1")
		eq(t, gstr(tasks[2].(*jsObject), "status"), "skipped", "status 2")
		eq(t, gstr(tasks[2].(*jsObject), "skipReason"), "N/A", "skipReason")
		eq(t, gflag(n.obj("taskList"), "blockCompletion"), true, "blockCompletion")
	})
	tw(t, "goal-record", "normalizeGoalRecord with malformed taskList returns taskList undefined", func(t *testing.T) {
		n := normalizeGoalRecord(jo(t, `{"id":"goal-mal","objective":"Test","status":"active","autoContinue":true,"usage":{"tokensUsed":0,"activeSeconds":0},"sisyphus":false,"taskList":"not-an-object"}`))
		eq(t, n != nil && n.obj("taskList") == nil, true, "malformed")
		n2 := normalizeGoalRecord(jo(t, `{"id":"goal-empty","objective":"Test","status":"active","autoContinue":true,"usage":{"tokensUsed":0,"activeSeconds":0},"sisyphus":false,
			"taskList":{"tasks":[],"blockCompletion":false,"proposedAt":"2026-05-27T00:00:00.000Z"}}`))
		eq(t, n2 != nil && n2.obj("taskList") == nil, true, "empty")
	})
	tw(t, "goal-record", "cloneGoal deep-clones tasks array", func(t *testing.T) {
		g := createGoal("Test", true, false, false, at)
		g.set("taskList", jo(t, `{"tasks":[{"id":"t1","title":"Task 1","status":"pending"},{"id":"t2","title":"Task 2","status":"complete","completedAt":"2026-05-27T00:00:00.000Z"}],"blockCompletion":false,"proposedAt":"2026-05-27T00:00:00.000Z"}`))
		c := cloneGoal(g)
		eq(t, len(c.obj("taskList").vals["tasks"].([]any)), 2, "clone length")
		c.obj("taskList").vals["tasks"].([]any)[0].(*jsObject).set("status", "complete")
		c.obj("taskList").set("blockCompletion", true)
		eq(t, gstr(g.obj("taskList").vals["tasks"].([]any)[0].(*jsObject), "status"), "pending", "original task")
		eq(t, gflag(g.obj("taskList"), "blockCompletion"), false, "original flag")
	})
	tw(t, "goal-record", "goal focus entries persist only session focus metadata", func(t *testing.T) {
		eq(t, show(goalFocusDetails(strp("goal/123"), "created")), `{"version":1,"focusedGoalId":"goal_123","reason":"created"}`, "created")
		eq(t, show(goalFocusDetails(nil, "cleared")), `{"version":1,"focusedGoalId":null,"reason":"cleared"}`, "cleared")
		eq(t, show(goalFocusDetails(nil, "unfocused")), `{"version":1,"focusedGoalId":null,"reason":"unfocused"}`, "unfocused")
		e := normalizeGoalFocusEntry(jo(t, `{"version":1,"focusedGoalId":"abc/def","reason":"resumed"}`))
		eq(t, *e.goalID, "abc_def", "id")
		eq(t, e.reason, "resumed", "reason")
		e = normalizeGoalFocusEntry(jo(t, `{"version":1,"focusedGoalId":null,"reason":"unfocused"}`))
		eq(t, e.goalID == nil && e.reason == "unfocused", true, "null focus")
		e = normalizeGoalFocusEntry(jo(t, `{"version":1,"focusedGoalId":"","reason":"unknown"}`))
		eq(t, e.goalID == nil && e.reason == "selected", true, "unknown reason")
		eq(t, normalizeGoalFocusEntry(jo(t, `{"version":3,"focusedGoalId":"abc"}`)) == nil, true, "version 3")
	})
}
