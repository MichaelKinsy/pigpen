package pi_goal_x

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const goldenFixtureRel = ".pi/goals/active_goal_fixture.md"

func goldenFixtureDir(rel string) string {
	return filepath.Join("testdata", "fixtures", rel) // copies of port/oracle/tests/fixtures
}

func goldenTempCwd(t *testing.T) string {
	cwd := t.TempDir()
	_ = os.MkdirAll(filepath.Join(cwd, ".pi", "goals"), 0o755)
	return cwd
}

func goldenWriteFixture(t *testing.T, cwd string) {
	data, err := os.ReadFile(goldenFixtureDir("goals/active_goal_fixture.md"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cwd, goldenFixtureRel), data, 0o644)
}

func goldenWriteLedger(t *testing.T, cwd string) {
	data, err := os.ReadFile(goldenFixtureDir("ledger/goal_events_fixture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cwd, ".pi/goals/goal_events.jsonl"), data, 0o644)
}

const goldenExpectedRecordJSON = `{
 "id":"golden_fixture_goal","objective":"Golden fixture goal objective","status":"active","autoContinue":true,
 "usage":{"tokensUsed":123456,"activeSeconds":3600},"sisyphus":false,
 "createdAt":"2026-08-03T09:00:00.000Z","updatedAt":"2026-08-03T10:00:00.000Z",
 "activePath":".pi/goals/active_goal_2026080317000000_golden_fixture_goal.md","revision":0,
 "taskList":{"blockCompletion":true,"proposedAt":"2026-08-03T09:05:00.000Z","tasks":[
  {"id":"task-1","title":"Implement core","status":"complete","completedAt":"2026-08-03T09:30:00.000Z","evidence":"npm run check passes",
   "subtasks":[{"id":"task-1a","title":"Design","status":"pending","verificationContract":"Design doc reviewed"}]},
  {"id":"task-2","title":"Docs","status":"skipped","skippedAt":"2026-08-03T09:40:00.000Z","skipReason":"User cancelled docs work","lightweightSubtasks":true}]},
 "verificationContract":"Run npm test (0 failures) and confirm every requirement is addressed."}`

func TestGoalGolden(t *testing.T) {
	tw(t, "goal-golden", "golden: fixture v3 goal file parses to the pinned record", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		parsed := parseGoalFile(filepath.Join(cwd, goldenFixtureRel))
		eq(t, parsed != nil, true, "fixture goal file must parse")
		deepEqualJS(t, parsed, jo(t, goldenExpectedRecordJSON), "record")
	})
	tw(t, "goal-golden", "golden: prompt body is authoritative over the JSON header objective", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		file := filepath.Join(cwd, goldenFixtureRel)
		data, _ := os.ReadFile(file)
		edited := strings.Replace(string(data), "# Goal Prompt\n\nGolden fixture goal objective", "# Goal Prompt\n\nEdited objective from the prompt body", 1)
		_ = os.WriteFile(file, []byte(edited), 0o644)
		parsed := parseGoalFile(file)
		eq(t, parsed != nil, true, "parses")
		eq(t, gstr(parsed, "objective"), "Edited objective from the prompt body", "objective")
		eq(t, gstr(parsed, "id"), "golden_fixture_goal", "id")
		eq(t, gstr(parsed, "status"), "active", "status")
		eq(t, gstr(parsed, "verificationContract"), "Run npm test (0 failures) and confirm every requirement is addressed.", "contract")
	})
	tw(t, "goal-golden", "golden: serialize -> parse round-trips the fixture record", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		parsed := parseGoalFile(filepath.Join(cwd, goldenFixtureRel))
		written, err := storage{cwd: cwd}.writeActiveGoalFile(parsed)
		eq(t, err, nil, "write")
		reparsed := parseGoalFile(filepath.Join(cwd, ".pi", "goals", filepath.Base(gstr(written, "activePath"))))
		eq(t, reparsed != nil, true, "reparses")
		eq(t, gstr(reparsed, "id"), gstr(parsed, "id"), "id")
		eq(t, gstr(reparsed, "objective"), gstr(parsed, "objective"), "objective")
		eq(t, gstr(reparsed, "status"), gstr(parsed, "status"), "status")
		eq(t, gstr(reparsed, "verificationContract"), gstr(parsed, "verificationContract"), "contract")
		deepEqualJS(t, reparsed.obj("usage"), parsed.obj("usage"), "usage")
		deepEqualJS(t, reparsed.obj("taskList"), parsed.obj("taskList"), "taskList")
	})
	tw(t, "goal-golden", "golden: normalizeTaskList preserves the recursive tree from the fixture", func(t *testing.T) {
		expected := jo(t, goldenExpectedRecordJSON)
		n := normalizeTaskList(deepClone(expected.obj("taskList")))
		eq(t, n != nil, true, "normalized")
		eq(t, gflag(n, "blockCompletion"), true, "blockCompletion")
		tasks := n.vals["tasks"].([]any)
		t0, t1 := tasks[0].(*jsObject), tasks[1].(*jsObject)
		eq(t, gstr(t0, "id"), "task-1", "id")
		eq(t, gstr(t0.vals["subtasks"].([]any)[0].(*jsObject), "verificationContract"), "Design doc reviewed", "sub contract")
		eq(t, gstr(t1, "status"), "skipped", "status")
		eq(t, gstr(t1, "skipReason"), "User cancelled docs work", "skipReason")
		eq(t, gflag(t1, "lightweightSubtasks"), true, "lightweight")
	})
	tw(t, "goal-golden", "golden: malformed or duplicate task items are dropped by normalization", func(t *testing.T) {
		eq(t, normalizeTaskItem(jo(t, `{"id":"","title":"x"}`)) == nil, true, "empty id")
		eq(t, normalizeTaskItem(jo(t, `{"id":"a","title":""}`)) == nil, true, "empty title")
		eq(t, normalizeTaskList(jo(t, `{"tasks":[]}`)) == nil, true, "empty tasks")
		eq(t, normalizeTaskList(jo(t, `{}`)) == nil, true, "no tasks")
		eq(t, normalizeTaskList(nil) == nil, true, "null")
	})
	normalizeStatus := func(t *testing.T, status string, auto bool) *jsObject {
		raw := newObject()
		raw.set("id", "g1")
		raw.set("objective", "x")
		raw.set("status", status)
		raw.set("autoContinue", auto)
		return normalizeGoalRecord(raw)
	}
	tw(t, "goal-golden", "golden: paused + legacy autoContinue:true stays paused (status authoritative)", func(t *testing.T) {
		r := normalizeStatus(t, "paused", true)
		eq(t, gstr(r, "status"), "paused", "status")
		eq(t, gflag(r, "autoContinue"), true, "autoContinue normalizes independently")
	})
	tw(t, "goal-golden", "golden: paused + autoContinue:false stays paused", func(t *testing.T) {
		eq(t, gstr(normalizeStatus(t, "paused", false), "status"), "paused", "status")
	})
	tw(t, "goal-golden", "golden: all five statuses survive normalization for both continuation flag values", func(t *testing.T) {
		for _, status := range []string{"active", "paused", "blocked", "complete", "budget_limited"} {
			for _, auto := range []bool{true, false} {
				r := normalizeStatus(t, status, auto)
				eq(t, r != nil, true, status)
				if r != nil {
					eq(t, gstr(r, "status"), status, status+" must survive")
					eq(t, gflag(r, "autoContinue"), auto, "autoContinue must survive for "+status)
				}
			}
		}
	})
	tw(t, "goal-golden", "golden: legacy paused+autoContinue:true record stays paused through markdown parse", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		raw := newObject()
		raw.set("id", "legacy-paused")
		raw.set("objective", "Legacy paused goal")
		raw.set("status", "paused")
		raw.set("autoContinue", true)
		legacy := normalizeGoalRecord(raw)
		eq(t, legacy != nil, true, "normalizes")
		written, err := storage{cwd: cwd}.writeActiveGoalFile(legacy)
		eq(t, err, nil, "write")
		parsed := parseGoalFile(filepath.Join(cwd, gstr(written, "activePath")))
		eq(t, parsed != nil, true, "legacy file must parse")
		eq(t, gstr(parsed, "status"), "paused", "parsed legacy record must stay paused")
		eq(t, gflag(parsed, "autoContinue"), true, "autoContinue flag survives as data")
	})
	tw(t, "goal-golden", "golden: fixture ledger reads every event type and counts malformed lines", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteLedger(t, cwd)
		res := readGoalLedger(storage{cwd: cwd})
		eq(t, len(res.events), 17, "all current event types must read")
		eq(t, res.malformed, 1, "the non-JSON line must be counted as malformed")
		var types []string
		for _, e := range res.events {
			types = append(types, gstr(e, "type"))
		}
		sortStrings(types)
		eq(t, types, []string{
			"audit_result", "audit_skipped", "audit_started", "completion_requested",
			"goal_aborted", "goal_completed", "goal_created", "goal_focused",
			"goal_paused", "goal_resumed", "goal_tweaked", "goal_unfocused",
			"task_complete", "task_list_set", "task_reopened", "task_skipped",
			"task_skipped",
		}, "types")
	})
	tw(t, "goal-golden", "golden: ledger reconstruction yields stable terminal and focus state", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteLedger(t, cwd)
		st := reconstructGoalLedger(readGoalLedger(storage{cwd: cwd}).events)
		eq(t, st.focusedGoalID == nil, true, "focus is cleared after terminal transitions")
		eq(t, st.goals.size(), 0, "goals")
		eq(t, st.terminalGoals.size(), 2, "terminal")
		a, _ := st.terminalGoals.get("goal-a")
		eq(t, a.latestStatus, "complete", "a status")
		eq(t, *a.completedAt, "2026-08-03T09:06:00.000Z", "a completedAt")
		b, _ := st.terminalGoals.get("goal-b")
		eq(t, b.latestStatus, "aborted", "b status")
		eq(t, *b.abortedAt, "2026-08-03T09:07:00.000Z", "b abortedAt")
	})
	tw(t, "goal-golden", "golden: focus restores from a focus entry against the on-disk pool", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		pool := storage{cwd: cwd}.readActiveGoalPool()
		entry := func(id *string, reason string) *focusEntry { return &focusEntry{goalID: id, reason: reason} }
		fx, ghost := "golden_fixture_goal", "ghost-goal"
		eq(t, *resolveSessionFocus(pool, entry(&fx, "created"), nil, false), "golden_fixture_goal", "valid focus")
		eq(t, resolveSessionFocus(pool, entry(&ghost, "selected"), nil, false) == nil, true, "stale focus id")
		eq(t, resolveSessionFocus(pool, nil, nil, false) == nil, true, "no entry default")
		eq(t, *resolveSessionFocus(pool, nil, nil, true), "golden_fixture_goal", "auto select single")
	})
	tw(t, "goal-golden", "golden: compaction summary text for a focused active goal", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		pool := storage{cwd: cwd}.readActiveGoalPool()
		g := pool.get("golden_fixture_goal")
		eq(t, g != nil, true, "goal")
		id := gstr(g, "id")
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: pool, focusedGoalId: &id})
		eq(t, summary, strings.Join([]string{
			"[FOCUSED GOAL]",
			"Goal golden_fixture_goal — running",
			"  Objective: Golden fixture goal objective",
			"  Cumulative goal usage (not context occupancy): 123K (123,456) tokens",
			"  Time: 1h00m00s",
			"",
			"[INSTRUCTION]",
			"Continue from the focused goal above, or ask the user to run /goal-focus.",
			"Do not rely on chat memory for goal state; use the facts above.",
		}, "\n"), "summary")
	})
	tw(t, "goal-golden", "golden: compaction summary with ledger terminal goals", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		goldenWriteLedger(t, cwd)
		events := readGoalLedger(storage{cwd: cwd}).events
		pool := storage{cwd: cwd}.readActiveGoalPool()
		id := gstr(pool.get("golden_fixture_goal"), "id")
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: pool, focusedGoalId: &id, ledgerEvents: events})
		eq(t, regexp.MustCompile(`^\[FOCUSED GOAL\]\nGoal golden_fixture_goal — running\n`).MatchString(summary), true, "head")
		eq(t, regexp.MustCompile(`\[TERMINAL GOALS — 2 completed or aborted\]\n- goal-a — completed at 2026-08-03T09:06:00\.000Z\n- goal-b — aborted at 2026-08-03T09:07:00\.000Z`).MatchString(summary), true, "terminal")
	})
	tw(t, "goal-golden", "golden: compaction summary for an empty session", func(t *testing.T) {
		summary := buildCompactionSummary(compactionSummaryArgs{goalsById: newPool()})
		eq(t, summary, strings.Join([]string{
			"[NO GOALS]",
			"No open or terminal goals recorded in this session.",
			"[INSTRUCTION]",
			"Continue from the focused goal above, or ask the user to run /goal-focus.",
			"Do not rely on chat memory for goal state; use the facts above.",
		}, "\n"), "summary")
	})
	tw(t, "goal-golden", "golden: auditor verdict marker contract is final-line approval/disapproval", func(t *testing.T) {
		eq(t, parseAuditorDecision("Looks good\n<approved/>"), auditorDecision{true, false}, "approved")
		eq(t, parseAuditorDecision("Nope\n<disapproved/>"), auditorDecision{false, true}, "disapproved")
		eq(t, parseAuditorDecision("confused <approved/> <disapproved/>"), auditorDecision{false, false}, "confused")
		eq(t, parseAuditorDecision("no marker"), auditorDecision{false, false}, "no marker")
	})
	tw(t, "goal-golden", "golden: serializeGoalFile emits the same header/body structure the fixture uses", func(t *testing.T) {
		cwd := goldenTempCwd(t)
		goldenWriteFixture(t, cwd)
		parsed := parseGoalFile(filepath.Join(cwd, goldenFixtureRel))
		s := serializeGoalFile(parsed)
		for _, re := range []string{`^\{`, `"version": 3`, `# Goal Prompt`, `## Progress`, `- Status: running`, `## Tasks\n\n<!-- blockCompletion: true -->`,
			`- \[x\] task-1: Implement core — evidence: npm run check passes`, `- \[~\] task-2: Docs — skipped: User cancelled docs work`} {
			eq(t, regexp.MustCompile(re).MatchString(s), true, re)
		}
	})
	tw(t, "goal-golden", "golden: invalid persisted token budgets normalize to absent", func(t *testing.T) {
		for _, bad := range []float64{0, -5, 1.5, math.NaN(), math.Inf(1), 9007199254740992} {
			raw := newObject()
			raw.set("id", "g1")
			raw.set("objective", "x")
			raw.set("status", "active")
			raw.set("tokenBudget", bad)
			r := normalizeGoalRecord(raw)
			eq(t, r != nil && !r.has("tokenBudget"), true, "persisted tokenBudget must normalize to absent")
		}
		raw := newObject()
		raw.set("id", "g1")
		raw.set("objective", "x")
		raw.set("status", "active")
		raw.set("tokenBudget", float64(5000))
		eq(t, gnum(normalizeGoalRecord(raw), "tokenBudget"), float64(5000), "valid budget survives")
	})
	tw(t, "goal-golden", "golden: validateTokenBudgetInput rejects invalid live input with user-facing messages", func(t *testing.T) {
		_, msg := validateTokenBudgetInput(2.5)
		eq(t, msg != "", true, "fractional rejected with a message")
		for _, bad := range []float64{0, -3, 9007199254740992} {
			_, msg := validateTokenBudgetInput(bad)
			eq(t, msg != "", true, "rejected")
		}
		v, msg := validateTokenBudgetInput(float64(5000))
		eq(t, msg == "" && v == 5000, true, "good")
	})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
