package pi_goal_x

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Twins of tests/goal-optimization.test.ts. The cases that drive the whole extension through the original's harness
// (createHarness/startHarness and the get_goal, update_goal_task and update_goal tool executors) are in goal_optimization_rig_test.go.

const optTasksJSON = `[{"id":"parent","title":"Parent","status":"pending","subtasks":[{"id":"child","title":"Child","status":"pending","verificationContract":"Prove the child"}]},{"id":"next","title":"Next","status":"pending"}]`

type optFixture struct {
	cwd  string
	goal *jsObject
	ctx  storage
}

// optFocusedFixture is focusedFixture() of experiments/bench/bench-common.mjs: a temp cwd holding one active goal file.
func optFocusedFixture(t *testing.T) optFixture {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := storage{cwd: cwd}
	goal, err := ctx.writeActiveGoalFile(createGoal("Benchmark goal 1 — implement the thing end to end.", true, false, false, ms(2026, time.September, 1, 0, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	return optFixture{cwd: cwd, goal: goal, ctx: ctx}
}

func optTaskList(t *testing.T, proposedAt string) *jsObject {
	t.Helper()
	return jo(t, `{"tasks":`+optTasksJSON+`,"blockCompletion":true,"proposedAt":"`+proposedAt+`"}`)
}

func optTextEvents(t *testing.T, events ...string) []*jsObject { return gpEvents(t, events...) }

func TestGoalOptimization(t *testing.T) {
	const f = "goal-optimization"

	tw(t, f, "detail pagination is lossless, Unicode-safe, bounded, and rejects changed-source cursors", func(t *testing.T) {
		goal := createGoal(strings.Repeat("a", 3999)+"🧪"+strings.Repeat("b", 6200), true, false, false, clockMs())
		goal.set("verificationContract", strings.Repeat("C", 4500))
		cursor, text, firstCursor := "", "", ""
		for {
			page := goalDetailPage(goal, goalDetailQuery{section: "objective", cursor: cursor}, nil, nil)
			assertTrue(t, page.ok, "page ok")
			assertTrue(t, utf16Len(page.content) <= 4000, "page bounded")
			assertTrue(t, utf8.ValidString(page.content) && !strings.HasSuffix(page.content, "\ufffd"), "no split surrogate pair")
			text += page.content
			cursor = page.nextCursor
			if firstCursor == "" {
				firstCursor = cursor
			}
			if !page.hasNext {
				break
			}
		}
		assertEq(t, text, gstr(goal, "objective")+"\n\nVerification contract:\n"+gstr(goal, "verificationContract"), "lossless")
		changed := cloneGoal(goal)
		changed.set("objective", gstr(goal, "objective")+"changed")
		assertEq(t, goalDetailPage(changed, goalDetailQuery{section: "objective", cursor: firstCursor}, nil, nil).ok, false, "changed objective")
		other := cloneGoal(goal)
		other.set("id", "another-goal")
		assertEq(t, goalDetailPage(other, goalDetailQuery{section: "objective", cursor: firstCursor}, nil, nil).ok, false, "another goal")
		assertEq(t, goalDetailPage(goal, goalDetailQuery{section: "tasks", cursor: firstCursor}, nil, nil).ok, false, "another section")
		assertEq(t, goalDetailPage(goal, goalDetailQuery{section: "objective", cursor: "bad"}, nil, nil).ok, false, "bad cursor")
	})

	tw(t, f, "indexed activity matches append-order deduplication with out-of-order and equal timestamps", func(t *testing.T) {
		fx := optFocusedFixture(t)
		id := gstr(fx.goal, "id")
		events := optTextEvents(t,
			`{"type":"goal_resumed","goalId":"`+id+`","reason":"resume","at":"2026-09-03"}`,
			`{"type":"goal_paused","goalId":"`+id+`","reason":"wait","at":"2026-09-05"}`,
			`{"type":"goal_resumed","goalId":"`+id+`","reason":"resume","at":"2026-09-02"}`,
			`{"type":"goal_resumed","goalId":"`+id+`","reason":"resume","at":"2026-09-06"}`,
			`{"type":"task_complete","goalId":"`+id+`","taskId":"a","at":"2026-09-05"}`)
		appendGoalEvents(fx.ctx, events)
		eq(t, deriveGoalActivity(goalActivityEvents(fx.ctx, id), id, deriveGoalActivityOptions{}), deriveGoalActivity(events, id, deriveGoalActivityOptions{}), "indexed activity")
		invalidateGoalLedgerCache()
		eq(t, deriveGoalActivity(goalActivityEvents(fx.ctx, id), id, deriveGoalActivityOptions{}), deriveGoalActivity(events, id, deriveGoalActivityOptions{}), "indexed activity after cache invalidation")
	})

	tw(t, f, "long ledger projections stay bounded, preserve older Oracle state, and recover corrupt checkpoints", func(t *testing.T) {
		fx := optFocusedFixture(t)
		id := gstr(fx.goal, "id")
		events := optTextEvents(t, `{"type":"oracle_failed","goalId":"`+id+`","fingerprint":"same","attempt":1,"errorCode":"provider","message":"offline","at":"2026-09-01"}`)
		for i := 0; i < 1000; i++ {
			day := strconv.Itoa(i%28 + 1)
			if len(day) < 2 {
				day = "0" + day
			}
			events = append(events, jo(t, `{"type":"task_complete","goalId":"`+id+`","taskId":"t`+strconv.Itoa(i)+`","at":"2026-09-`+day+`"}`))
		}
		appendGoalEvents(fx.ctx, events)
		assertTrue(t, len(goalActivityEvents(fx.ctx, id)) <= 64, "activity projection bounded")
		assertEq(t, gnum(goalOracleState(fx.ctx, id, "same"), "failedAttempts"), 1.0, "older Oracle state preserved")
		expected := deriveGoalActivity(events, id, deriveGoalActivityOptions{})
		eq(t, deriveGoalActivity(goalActivityEvents(fx.ctx, id), id, deriveGoalActivityOptions{}), expected, "activity")
		if err := os.WriteFile(filepath.Join(fx.cwd, ".pi/goals", ledgerCheckpointFile), []byte(`{"version":2,"runtimeIndex":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
		invalidateGoalLedgerCache()
		eq(t, deriveGoalActivity(goalActivityEvents(fx.ctx, id), id, deriveGoalActivityOptions{}), expected, "activity after corrupt checkpoint")
		assertEq(t, len(readGoalLedger(fx.ctx).events), len(events), "full ledger")
	})

	tw(t, f, "Unicode ledger offsets and first warm append are counted exactly once", func(t *testing.T) {
		fx := optFocusedFixture(t)
		id := gstr(fx.goal, "id")
		readGoalLedger(fx.ctx)
		appendGoalEvent(fx.ctx, jo(t, `{"type":"goal_created","goalId":"`+id+`","objective":"日本語 🧪","sisyphus":false,"autoContinue":true,"at":"2026-09-01"}`))
		raw, err := os.ReadFile(filepath.Join(fx.cwd, ".pi/goals", ledgerCheckpointFile))
		if err != nil {
			t.Fatal(err)
		}
		cp := jo(t, string(raw))
		assertEq(t, gnum(cp, "coveredEvents"), 1.0, "coveredEvents")
		info, err := os.Stat(filepath.Join(fx.cwd, ".pi/goals/goal_events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		assertEq(t, gnum(cp, "coveredBytes"), float64(info.Size()), "coveredBytes")
		appendGoalEvent(fx.ctx, jo(t, `{"type":"goal_paused","goalId":"`+id+`","reason":"é","at":"2026-09-02"}`))
		invalidateGoalLedgerCache()
		state := loadLedgerState(fx.ctx)
		assertEq(t, state.coveredEvents, 2, "coveredEvents after second append")
		g, _ := state.state.goals.get(id)
		assertTrue(t, g != nil && g.latestPauseReason != nil && *g.latestPauseReason == "é", "latestPauseReason")
	})

	tw(t, f, "compact context preserves critical rules and exposes omitted task descendants", func(t *testing.T) {
		goal := createGoal("Objective "+strings.Repeat("x", 20000), true, true, false, clockMs())
		goal.set("taskList", optTaskList(t, "2026-09-01"))
		goal.set("currentTaskId", "parent")
		goal.set("verificationContract", "contract "+strings.Repeat("y", 20000))
		prompt := goalPrompt(goal, nil)
		assertTrue(t, utf16Len(prompt) < maxPromptFragmentChars, "prompt under the cap")
		for _, rule := range []string{`three consecutive goal turns`, `status: "paused"`, `independent completion auditor`, `TASK GATE`, `get_goal\(section="objective"\)`, `Follow the user's ordered plan faithfully`} {
			assertMatch(t, prompt, rule)
		}
		assertMatch(t, taskListBlock(goal, nil), `child`)
		assertEq(t, strings.Count(taskListBlock(goal, nil), "Current: parent"), 1, "current task listed once")
		goal.set("objective", "Updated objective with same id/revision/time")
		assertMatch(t, goalPrompt(goal, nil), `Updated objective`)
	})

	tw(t, f, "task presentation reuses usage-only work but invalidates in-place content edits", func(t *testing.T) {
		goal := createGoal("Test", true, false, false, clockMs())
		goal.set("taskList", optTaskList(t, "2026-09-01"))
		options := goalDashboardModelOptions{focused: true, otherOpenGoals: 0}
		first := deriveGoalDashboardModel(goal, options)
		goal.obj("usage").set("tokensUsed", 100.0)
		second := deriveGoalDashboardModel(goal, options)
		assertTrue(t, &first.taskTree[0] == &second.taskTree[0], "task tree reused for a usage-only change")
		assertEq(t, second.usage.tokens, 100.0, "usage tokens")
		tasks := goal.obj("taskList").vals["tasks"].([]any)
		old := taskIndex(tasks)
		tasks[0].(*jsObject).set("title", "Changed")
		assertTrue(t, taskIndex(tasks) != old, "in-place edit yields a new index")
		assertEq(t, old.byId["parent"].vals["title"].(string), "Parent", "old index keeps its snapshot")
		assertEq(t, deriveGoalDashboardModel(goal, options).taskTree[0].title, "Changed", "dashboard sees the edit")
	})
}
