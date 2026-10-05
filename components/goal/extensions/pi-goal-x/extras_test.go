package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests the port adds: behavior the original's tests do not pin and the replayed sessions cannot reach.

func TestSafeIdPart(t *testing.T) {
	eq(t, safeIdPart(strings.Repeat("x", 100)), strings.Repeat("x", 80), "cut at 80")
	eq(t, safeIdPart("a🚀b"), "a__b", "an astral character is two UTF-16 units")
	eq(t, safeIdPart(""), "goal", "empty")
	eq(t, safeIdPart("a/b c"), "a_b_c", "unsafe characters")
}

func TestNormalizeEdges(t *testing.T) {
	n := normalizeGoalRecord(jo(t, `{"id":"a","objective":"x"}`))
	eq(t, gflag(n, "autoContinue"), true, "autoContinue defaults to true")
	eq(t, gstr(n, "status"), "active", "status defaults to active")
	eq(t, normalizeGoalRecord(jo(t, `{"id":"a","objective":"   "}`)) == nil, true, "blank objective")
	eq(t, normalizeGoalRecord(jo(t, `{"objective":"x","tokenBudget":0}`)).has("tokenBudget"), false, "budget 0")
	eq(t, normalizeGoalRecord(jo(t, `{"objective":"x","tokenBudget":1.5}`)).has("tokenBudget"), false, "fractional budget")
	eq(t, gnum(normalizeGoalRecord(jo(t, `{"objective":"x","tokenBudget":7}`)), "tokenBudget"), 7.0, "budget 7")
	eq(t, gstr(normalizeGoalRecord(jo(t, `{"objective":"x","stopReason":"user"}`)), "stopReason"), "user", "stopReason user")
	eq(t, normalizeGoalRecord(jo(t, `{"objective":"x","stopReason":"other"}`)).has("stopReason"), false, "stopReason other")
	eq(t, normalizeGoalRecord(jo(t, `{"objective":"x","status":"blocked"}`)).vals["status"], "blocked", "blocked")
	task := func(s string) *jsObject {
		return normalizeGoalRecord(jo(t, `{"objective":"x","taskList":{"tasks":[`+s+`]}}`))
	}
	eq(t, task(`{"id":"t","title":"  "}`).has("taskList"), false, "a task needs a title")
	eq(t, task(`{"id":"","title":"x"}`).has("taskList"), false, "a task needs an id")
	eq(t, task(`{"id":"t","title":"x","status":"skipped"}`).obj("taskList").vals["tasks"].([]any)[0].(*jsObject).vals["status"], "skipped", "skipped")
	cur := func(id string) bool {
		g := normalizeGoalRecord(jo(t, `{"objective":"x","currentTaskId":"`+id+`","taskList":{"tasks":[{"id":"a","title":"A","status":"complete"},{"id":"b","title":"B","status":"pending","subtasks":[{"id":"c","title":"C","status":"pending"}]}]}}`))
		return g.has("currentTaskId")
	}
	eq(t, cur("a"), false, "complete task")
	eq(t, cur("b"), true, "pending task")
	eq(t, cur("c"), true, "pending subtask")
	eq(t, cur("zz"), false, "unknown task")
}

func TestSchedulerNormalization(t *testing.T) {
	valid := func(s string) bool {
		g := normalizeGoalRecord(jo(t, `{"objective":"x","scheduler":`+s+`}`))
		sc := g.obj("scheduler")
		return sc != nil && gstr(sc, "owner") != "invalid"
	}
	base := func(rest string) string {
		return `{"version":1,"owner":"o","generation":"g","used":0,"phase":"idle","repairUsed":false` + rest + `}`
	}
	eq(t, valid(base("")), true, "base")
	eq(t, valid(strings.Replace(base(""), `"idle"`, `"interrupted"`, 1)), true, "interrupted is a phase")
	eq(t, valid(strings.Replace(base(""), `"idle"`, `"bogus"`, 1)), false, "unknown phase")
	eq(t, valid(strings.Replace(base(""), `"version":1`, `"version":2`, 1)), false, "version 2")
	eq(t, valid(strings.Replace(base(""), `"version":1`, `"version":0`, 1)), false, "version 0")
	eq(t, valid(strings.Replace(base(""), `"owner":"o"`, `"owner":"`+strings.Repeat("x", 2000)+`"`, 1)), true, "owner of 2000 units")
	eq(t, valid(strings.Replace(base(""), `"owner":"o"`, `"owner":"`+strings.Repeat("x", 2001)+`"`, 1)), false, "owner of 2001 units")
	eq(t, valid(strings.Replace(base(""), `"used":0`, `"used":-1`, 1)), false, "negative used")
	eq(t, valid(strings.Replace(base(""), `"repairUsed":false`, `"repairUsed":0`, 1)), false, "repairUsed type")
	eq(t, valid(strings.Replace(base(`,"decision":{"kind":"ready","purpose":"recovery"}`), `"idle"`, `"ready"`, 1)), true, "ready with recovery")
	eq(t, valid(strings.Replace(base(`,"decision":{"kind":"wait"}`), `"idle"`, `"ready"`, 1)), false, "ready needs a ready decision")
	eq(t, valid(strings.Replace(base(`,"decision":{"kind":"ready","purpose":"bogus"}`), `"idle"`, `"idle"`, 1)), false, "unknown purpose")
	eq(t, valid(strings.Replace(base(`,"decision":{"kind":"wait"}`), `"idle"`, `"waiting"`, 1)), false, "waiting needs a wait")
	wait := `,"wait":{"id":"w","token":"t","reason":"r","deadline":5}`
	eq(t, valid(strings.Replace(base(wait+`,"decision":{"kind":"wait"}`), `"idle"`, `"waiting"`, 1)), true, "waiting with wait")
	eq(t, valid(strings.Replace(base(wait), `"idle"`, `"waiting"`, 1)), false, "waiting needs a wait decision")
	eq(t, valid(base(`,"wait":{"id":"w","token":"t","reason":"r","deadline":5,"intervalMs":999,"remainingChecks":1,"nextCheckAt":1}`)), false, "interval under 1000")
	eq(t, valid(base(`,"wait":{"id":"w","token":"t","reason":"r","deadline":5,"intervalMs":1000,"remainingChecks":1,"nextCheckAt":1}`)), true, "interval 1000")
	eq(t, valid(base(`,"wait":{"id":"w","token":"t","reason":"r","deadline":5,"remainingChecks":1}`)), false, "checks without an interval")
	eq(t, valid(base(`,"wait":{"id":"w","token":"t","reason":"r","deadline":5,"signalled":1}`)), false, "signalled type")
	eq(t, valid(strings.Replace(base(`,"dispatch":{"id":"d","kind":"recovery","claimedAt":1}`), `"idle"`, `"running"`, 1)), true, "running with a dispatch")
	eq(t, valid(strings.Replace(base(""), `"idle"`, `"running"`, 1)), false, "running needs a dispatch")
	eq(t, valid(strings.Replace(base(""), `"idle"`, `"claimed"`, 1)), false, "claimed needs a dispatch")
	eq(t, valid(base(`,"dispatch":{"id":"d","kind":"bogus","claimedAt":1}`)), false, "dispatch kind")
	eq(t, valid(base(`,"dispatch":{"id":"d","kind":"recovery","claimedAt":1}`)), true, "dispatch kind recovery")
	g := normalizeGoalRecord(jo(t, `{"objective":"x","scheduler":`+base(`,"decision":{"kind":"ready","purpose":"ready","nextAction":"x"}`)+`}`))
	eq(t, strings.Contains(show(g), "nextAction"), false, "nextAction is dropped")
	eq(t, normalizeGoalRecord(jo(t, `{"objective":"x","scheduler":5}`)).obj("scheduler").vals["phase"], "interrupted", "a scalar scheduler is invalid")
	eq(t, gnum(normalizeGoalRecord(jo(t, `{"objective":"x","scheduler":5}`)).obj("scheduler"), "used"), 9007199254740991.0, "the allowance is spent")
	eq(t, gstr(newGoalScheduler("me"), "owner"), "me", "owner")
}

func TestFormatEdges(t *testing.T) {
	eq(t, truncateText("abcdefgh", 8), "abcdefgh", "exactly max is not cut")
	eq(t, displayObjectiveTitle("Success criteria: x\nthe real title"), "the real title", "section headers are skipped")
	eq(t, displayObjectiveTitle("=== Goal ===\nSteps: 1. x"), "=== Goal === Steps: 1. x", "only headers: the whole text")
	eq(t, formatTokenValue(1000), "1K (1,000) tokens", "1000")
	eq(t, formatTokenValue(1e9), "1B (1,000,000,000) tokens", "a billion")
	eq(t, formatTokenValue(12_500_000), "13M (12,500,000) tokens", "12.5M rounds")
	eq(t, formatTokenValue(-5), "0 tokens", "negative")
	eq(t, formatTokenValue(10000), "10K (10,000) tokens", "10000")
	g := jo(t, `{"objective":"x","status":"active","autoContinue":false,"usage":{"tokensUsed":500,"activeSeconds":0},"sisyphus":false}`)
	eq(t, statusLabel(g), "active", "active without auto-continue")
	eq(t, oneLineSummary(g), "active [500] - x", "tokens in the summary")
	eq(t, oneLineSummary(jo(t, `{"objective":"x","status":"active","autoContinue":true,"usage":{"tokensUsed":0,"activeSeconds":0}}`)), "running - x", "no tokens")
	s := jo(t, `{"objective":"x","status":"budget_limited","autoContinue":true,"sisyphus":true,"usage":{}}`)
	eq(t, statusLabel(s), "sisyphus budget limited", "budget limited")
	eq(t, footerStatus(s), "goal✊: sisyphus budget limited - x", "sisyphus footer")
	eq(t, buildTaskSummary(jo(t, `{"tasks":[]}`)), "No tasks", "empty list")
	eq(t, buildTaskSummary(jo(t, `{"tasks":[{"status":"skipped"},{"status":"complete"},{"status":"pending"}]}`)), "1/3 tasks complete (1 skipped)", "counts")
	eq(t, buildTaskSummary(jo(t, `{"tasks":[{"status":"complete"}]}`)), "1/1 tasks complete", "no skipped")
	_, c := extractVerificationContract("a\nVerification contract: first\nVerification contract: second")
	eq(t, c, "second", "the last contract line wins")
	_, c = extractVerificationContract("a\nVerification contract:   spaced out   ")
	eq(t, c, "spaced out", "trimmed")
	long := jo(t, `{"id":"g","objective":"`+strings.Repeat("w", 100)+`","status":"active","autoContinue":true,"sisyphus":false}`)
	eq(t, utf16Len(goalSelectorLabel(long, nil)), len("  g | running | goal | ")+72, "the title is cut at 72")
	list := buildGoalListText(poolOf(jo(t, `{"id":"g","objective":"x","status":"active","autoContinue":true,"createdAt":"a","usage":{"tokensUsed":5,"activeSeconds":0}}`)), nil)
	eq(t, strings.Contains(list, "· 0s · 5"), true, "usage with tokens only")
	eq(t, buildUnfocusedOpenGoalsSummary(1), "No goal is focused in this session. 1 open goal exist in the goal pool. Use /goal-focus to choose the session focus before doing goal work.", "singular")
}

func TestStoragePathsAndParsing(t *testing.T) {
	useTimeZone(t)
	s := storage{cwd: t.TempDir()}
	_, err := s.path(".pi/goals/../x")
	eq(t, err != nil, true, "dot-dot is refused")
	_, err = s.path(".pi/other")
	eq(t, err != nil, true, "outside the goals directory")
	eq(t, isSafeActivePath(s, ".pi/goals/sub/active_goal_x.md"), false, "a subdirectory is not the active root")
	eq(t, isSafeActivePath(s, ".pi/goals/active_goal_x.txt"), false, "suffix")
	eq(t, isSafeActivePath(s, ".pi/goals/active_goal_x.md"), true, "plain")
	eq(t, isSafeArchivedPath(s, ".pi/goals/archived/goal_x.txt"), false, "archived suffix")
	eq(t, isSafeArchivedPath(s, ".pi/goals/archived/goal_x.md"), true, "archived plain")
	g := jo(t, `{"id":"g","createdAt":"2026-01-02T03:04:05.000Z","updatedAt":"2026-02-03T04:05:06.000Z"}`)
	eq(t, makeActiveGoalPath(g), ".pi/goals/active_goal_2026010203040500_g.md", "active path uses createdAt")
	eq(t, makeArchivedGoalPath(g), ".pi/goals/archived/goal_2026020304050600_g.md", "archived path uses updatedAt")
	eq(t, timestampForFile("2026-01-02T03:04:05.250Z"), "2026010203040525", "hundredths")
	eq(t, timestampForFile("2026-01-02T03:04:05.250Z")[4:6], "01", "month")

	// A header with quotes, escapes and braces inside strings, and a body edited with extra white space.
	rec := baseGoal(`He said "} go \ now {x} } done`, false, ms(2026, 1, 2, 3, 4, 5))
	text := serializeGoalFile(rec)
	end := findJSONObjectEnd(text)
	eq(t, text[end], byte('}'), "the header's closing brace")
	p := parseGoalContent(text)
	eq(t, gstr(p, "objective"), `He said "} go \ now {x} } done`, "objective round-trips")
	edited := text[:end+1] + "\n\n# Goal Prompt\n\n   \n  padded  \n\n## Progress\n\n- x\n"
	eq(t, gstr(parseGoalContent(edited), "objective"), "padded", "body is trimmed")
	noProgress := strings.Split(text, "## Progress")[0] + "tail text"
	eq(t, strings.Contains(gstr(parseGoalContent(noProgress), "objective"), "tail text"), true, "without a Progress heading the body runs to the end")

	// The pool: a complete goal and a goal whose metadata names another path.
	dir := s.root()
	os.MkdirAll(dir, 0o755)
	done := baseGoal("done", false, ms(2026, 1, 1, 0, 0, 0))
	done.set("id", "done")
	done.set("status", "complete")
	os.WriteFile(filepath.Join(dir, "active_goal_done.md"), []byte(serializeGoalFile(done)), 0o644)
	live := baseGoal("live", false, ms(2026, 1, 2, 0, 0, 0))
	live.set("id", "live")
	live.set("activePath", ".pi/goals/active_goal_elsewhere.md")
	os.WriteFile(filepath.Join(dir, "active_goal_live.md"), []byte(serializeGoalFile(live)), 0o644)
	goals := s.scanActiveGoalFiles()
	eq(t, ids(goals), []string{"live"}, "complete goals are not scanned")
	eq(t, strOrEmpty(goals[0], "activePath"), ".pi/goals/active_goal_live.md", "the path is the file's own")
	// Snapshot: a complete goal is not served, a legacy snapshot is.
	invalidateGoalPoolCache()
	snap := `{"version":1,"dirMtimeMs":1,"goals":[{"id":"live","status":"active","activePath":".pi/goals/active_goal_live.md","objective":"snap"},{"id":"done","status":"complete","activePath":".pi/goals/active_goal_done.md","objective":"d"}]}`
	os.WriteFile(filepath.Join(dir, snapshotLegacy), []byte(snap), 0o644)
	pool := s.readActiveGoalPool()
	eq(t, pool.ids, []string{"live"}, "the snapshot's complete goal is left out")
	eq(t, gstr(pool.get("live"), "objective"), "snap", "served from the legacy snapshot")
	// Unlinking invalidates the cached pool.
	invalidateGoalPoolCache()
	os.Remove(filepath.Join(dir, snapshotLegacy))
	os.Remove(s.snapshotPath())
	first := s.readActiveGoalPoolView()
	eq(t, first.has("live"), true, "cached")
	must(0, s.safeUnlink(goalsDir, ".pi/goals/active_goal_live.md"))
	os.Remove(s.snapshotPath())
	eq(t, s.readActiveGoalPoolView().has("live"), false, "an unlink drops the cache")
}

func TestSerializePauseLinesAndTasks(t *testing.T) {
	g := baseGoal("x", false, ms(2026, 1, 2, 3, 4, 5))
	g.set("pauseReason", "needs a key")
	g.set("pauseSuggestedAction", "ask the user")
	g.set("verificationContract", "  all green  ")
	g.set("taskList", jo(t, `{"tasks":[{"id":"a","title":"A","status":"complete","verificationContract":"hidden"},{"id":"b","title":"B","status":"pending","verificationContract":"shown"}],"blockCompletion":false}`))
	out := serializeGoalFile(g)
	for _, want := range []string{"- Agent pause reason: needs a key", "- Agent suggests: ask the user", "- Verification contract: all green\n", "[ ] b: B — contract: shown", "[x] a: A\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	eq(t, strings.Contains(out, "[x] a: A — contract"), false, "a complete task shows no contract")
	eq(t, strings.Contains(serializeGoalFile(withSkipped(t)), "[~] s: S — skipped: why\n"), true, "a skipped task shows its reason, not its contract")
	eq(t, strings.Contains(out, "blockCompletion: false"), true, "block flag")
}

func TestJSObjectOrder(t *testing.T) {
	o := newObject()
	for _, k := range []string{"b", "1", "a", "0"} {
		o.set(k, 1.0)
	}
	eq(t, o.order(), []string{"0", "1", "b", "a"}, "array-index keys first")
	o.set("b", undef{})
	eq(t, marshalJSON(o, ""), `{"0":1,"1":1,"a":1}`, "undefined is left out and keeps its place")
}

func TestSettingsLayers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	num := func(text string) *float64 { return readLayer(write("s.json", text)).maxRuns }
	if v := num(`{"maxAutonomousRuns":-1}`); v != nil {
		t.Error("a negative allowance is ignored")
	}
	if v := num(`{"maxAutonomousRuns":" 3 "}`); v == nil || *v != 3 {
		t.Error("a numeric string is trimmed")
	}
	if v := num(`{"maxAutonomousRuns":1.5}`); v != nil {
		t.Error("a fraction is ignored")
	}
	if v := num(`[1]`); v != nil {
		t.Error("an array is not a settings object")
	}
	l := readLayer(write("b.json", `{"disableContracts":"true","autoSelectSingleGoal":false}`))
	eq(t, l.disableContracts != nil && *l.disableContracts, true, "string true")
	eq(t, l.autoSelect != nil && !*l.autoSelect, true, "false")
	t.Setenv("PI_GOAL_DISABLE_CONTRACTS", "1")
	eq(t, loadGoalSettings(dir).disableContracts, false, "an environment value other than true/false is ignored")
}

func TestKickoffWithoutAScheduler(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
	os.WriteFile(filepath.Join(cwd, ".pi/pi-goal-x-settings.json"), []byte(`{"maxAutonomousRuns":1}`), 0o644)
	invalidateGoalPoolCache()
	h := &fakeHost{cwd: cwd}
	co := newCore(h)
	co.loadState()
	co.flush()
	h.events = nil
	must(0, co.direct("Do the thing", false))
	co.flush()
	var warned bool
	for _, e := range h.events {
		if e[0] == "notify" && strings.Contains(e[2].(string), "(Go port): automatic continuation is not ported") {
			warned = true
		}
	}
	eq(t, warned, true, "the port says it does not continue by itself")
	file := ""
	for k := range readTree(t, cwd) {
		if strings.Contains(k, "active_goal_") {
			file = k
		}
	}
	text, _ := os.ReadFile(filepath.Join(cwd, file))
	for _, want := range []string{`"phase": "ready"`, `"kind": "ready"`, `"purpose": "kickoff"`} {
		if !strings.Contains(string(text), want) {
			t.Errorf("goal file lacks %s", want)
		}
	}
}

func TestHeadlessAndBusy(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
	os.WriteFile(filepath.Join(cwd, ".pi/pi-goal-x-settings.json"), []byte(`{"maxAutonomousRuns":0}`), 0o644)
	invalidateGoalPoolCache()
	h := &fakeHost{cwd: cwd, noUI: true}
	co := newCore(h)
	co.loadState()
	must(0, co.direct("Headless goal", false))
	h.events = nil
	co.clear()
	eq(t, h.events[0][1], "warning", "level")
	if !strings.HasPrefix(h.events[0][2].(string), "Run /goal-clear in an interactive session to confirm clearing: running - Headless goal") {
		t.Errorf("headless clear: %v", h.events[0])
	}
	// Two open goals and no focus: a headless pause explains instead of asking.
	co.setFocusedGoalID(nil, "unfocused", false)
	second := baseGoal("Second", false, clockMs()+1000)
	must(co.st.writeActiveGoalFile(second))
	invalidateGoalPoolCache()
	co.goals = co.st.readActiveGoalPool()
	h.events = nil
	co.pause()
	eq(t, strings.HasPrefix(h.events[0][2].(string), "No goal is focused in this session. 2 open goals"), true, "headless pause")
	// A busy session aborts when the focus is dropped.
	h.noUI, h.busy = false, true
	co.setFocusedGoalID(strp(gstr(second, "id")), "selected", true)
	co.unfocus()
	eq(t, h.aborted, 1, "abort")
	h.busy, h.aborted = false, 0
	co.setFocusedGoalID(strp(gstr(second, "id")), "selected", true)
	co.unfocus()
	eq(t, h.aborted, 0, "idle sessions are not aborted")
}

func withSkipped(t *testing.T) *jsObject {
	g := baseGoal("x", false, ms(2026, 1, 2, 3, 4, 5))
	g.set("taskList", jo(t, `{"tasks":[{"id":"s","title":"S","status":"skipped","skipReason":"why","verificationContract":"hidden too"}],"blockCompletion":false}`))
	return g
}

// The confirmation of /goal-clear can outlast a change of focus: nothing is cleared then.
func TestClearWhenFocusMovesAndBackDuringConfirm(t *testing.T) { testClearFocusMoves(t) }
func TestClearWhenFocusMovesDuringConfirm(t *testing.T)        { testClearFocusMoves(t) }

func testClearFocusMoves(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
	os.WriteFile(filepath.Join(cwd, ".pi/pi-goal-x-settings.json"), []byte(`{"maxAutonomousRuns":0}`), 0o644)
	invalidateGoalPoolCache()
	h := &fakeHost{cwd: cwd}
	co := newCore(h)
	co.loadState()
	must(0, co.direct("First", false))
	first := *co.focused
	second := baseGoal("Second", false, clockMs()+1000)
	second.set("id", "other")
	must(co.st.writeActiveGoalFile(second))
	invalidateGoalPoolCache()
	co.goals = co.st.readActiveGoalPool()
	h.onConfirm = func() { co.assignFocused(strp("other")) }
	if strings.Contains(t.Name(), "AndBack") {
		h.onConfirm = func() { co.assignFocused(strp("other")); co.assignFocused(strp(first)) }
	}
	h.confirm = []bool{true}
	h.events = nil
	co.clear()
	last := h.events[len(h.events)-1]
	eq(t, last[1:], []any{"warning", "Goal changed while confirming; nothing was cleared."}, "notice")
	eq(t, co.goals.has(first), true, "the first goal is still there")
	_, err := os.Stat(filepath.Join(cwd, ".pi/goals/archived"))
	eq(t, err != nil, true, "nothing archived")
}
