package pi_goal_x

// Mutation-survivor tests, lane D (dashboard model/renderer, auditor dashboard, audit session mapper, auditor, Oracle, selector,
// widget, escape dialog, task-list overlay, terminal keys). Each test asserts the original's documented behavior; the expected values
// come from the TypeScript original (components/goal/port/oracle/extensions) or its stated rules, not from the Go code.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── dashboard model ──

func mdSched(phase string) *jsObject {
	return dmObj("phase", phase, "wait", dmObj("id", "w1", "reason", "rest", "deadline", 1.7e12))
}

func TestMutantsDDashboardModel(t *testing.T) {
	// A waiting scheduler on an active goal reads as idle/Waiting with the footer label "waiting".
	eq(t, dmModel(t, "scheduler", mdSched("waiting")).status,
		dashboardGoalStatus{code: "idle", label: "Waiting", footerLabel: "waiting"}, "waiting status")
	// ...but only for an active goal.
	paused := dmModel(t, "status", "paused", "stopReason", "user", "scheduler", mdSched("waiting")).status
	eq(t, paused.code, "paused", "paused stays paused")
	eq(t, paused.label, "Paused (user)", "paused label")

	// percentageOf: a zero total is 0%, never infinite.
	eq(t, percentageOf(5, 0), 0, "5/0")
	eq(t, percentageOfF(0, 0), 0, "0/0")
	eq(t, dmModel(t, "tokenBudget", 0.0).budget.percentage, 0, "zero token budget")

	// A parent with exactly one child reports 1 child.
	tasks := dmTasks(dmTask("a", "A", "subtasks", dmTasks(dmTask("a.1", "A1", "status", "complete"))))
	eq(t, deriveCurrentTaskSubtaskProgress("a", tasks), currentTaskSubtaskProgress{1, 1, 100}, "one child")

	eq(t, maxScrollOffset(6, 5), 1, "max 6/5")
	eq(t, maxScrollOffset(5, 5), 0, "max 5/5")

	// A non-positive row budget anchors to the top whatever the completion.
	nodes := dmNodes(map[string]string{"t3": "2026-01-01T10:00:00.000Z"})
	eq(t, anchoredScrollOffset(nodes, 0), 0, "anchor with zero rows")
	eq(t, anchoredScrollOffset(nodes, -2), 0, "anchor with negative rows")

	for _, c := range [][2]int{{140, 20}, {100, 20}, {99, 16}, {70, 16}, {69, 12}, {50, 12}, {49, 8}, {10, 8}} {
		eq(t, expandedTaskViewportRows(c[0]), c[1], "expanded rows at "+strconv.Itoa(c[0]))
	}

	eq(t, formatCompactTokens(1_000_000), "1M", "exactly one million")
	eq(t, formatCompactTokens(999_999), "1000K", "just under a million")
	eq(t, formatBudget(18200, 10000), "18.2K / 10K · 100%", "over-budget percentage clamps to 100")
	eq(t, formatBudget(5, 0), "5 / 0 · 0%", "zero total")

	eq(t, dmModel(t, "usage", dmUsage(0, 1)).usage.footerBits, "1s", "one active second")
	eq(t, dmModel(t, "usage", dmUsage(1, 0)).usage.footerBits, "1", "one token")
	eq(t, dmModel(t, "usage", dmUsage(0, 0)).usage.footerBits, "", "unused")

	eq(t, dmModel(t).scheduling, []string{}, "no scheduling rows")
}

// ── dashboard renderer ──

func mdModelBase() *goalDashboardModel {
	return &goalDashboardModel{title: "T", status: dashboardGoalStatus{code: dashRunning, label: "In progress", footerLabel: "running"}, auditorEnabled: true}
}

func TestMutantsDDashboardRenderer(t *testing.T) {
	theme := wgTestTheme

	eq(t, dashLayoutMode(70), "medium", "width 70")
	eq(t, dashLayoutMode(69), "narrow", "width 69")

	// "↑ 1 more task" is singular for one hidden task.
	rows := dashRenderCompactTaskRows(theme, []dashboardTaskNode{{id: "a", title: "A", status: "pending"}, {id: "b", title: "B", status: "pending"}},
		taskListViewport{totalRows: 2, rows: 1, offset: 1, maxOffset: 1, hiddenAbove: 1, hiddenBelow: 0}, 60)
	eq(t, rows[0], "↑ 1 more task", "singular hidden above")

	// Narrow mode drops the "Sub " word from the compact subtask ratio; wider modes keep it.
	goal := goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3")
	model := goldenNotNil(t, goldenModelFor(goal, goldenModelOpts{}))
	narrow := strings.Join(renderCompactDashboard(model, theme, 60, goalCompactOptions{}), "\n")
	if !strings.Contains(narrow, " · 2/3 ") || strings.Contains(narrow, "Sub ") {
		t.Errorf("narrow header keeps the Sub word:\n%s", narrow)
	}
	wide := strings.Join(renderCompactDashboard(model, theme, 80, goalCompactOptions{}), "\n")
	if !strings.Contains(wide, " · Sub 2/3 ") {
		t.Errorf("medium header lacks the Sub word:\n%s", wide)
	}

	// The fuel gauge turns to the error colour exactly when the budget is used up.
	for _, c := range []struct {
		used  float64
		color string
	}{{20000, "error"}, {19999, "mdHeading"}} {
		m := deriveGoalDashboardModel(dmGoal("tokenBudget", 20000.0, "usage", dmUsage(c.used, 10)), goalDashboardModelOptions{focused: true})
		var seen [][2]string
		renderCompactDashboard(m, wgTheme{seen: &seen}, 100, goalCompactOptions{})
		found := false
		for _, kv := range seen {
			if kv[1] == "⛽" {
				found = kv[0] == c.color
			}
		}
		if !found {
			t.Errorf("used %v: fuel colour is not %s: %v", c.used, c.color, seen)
		}
	}

	// "All tasks complete" needs every top-level task done.
	open := mdModelBase()
	open.taskProgress = &taskProgress{1, 3, 33}
	if strings.Contains(strings.Join(renderCompactDashboard(open, theme, 100, goalCompactOptions{}), "\n"), "All tasks complete") {
		t.Error("announced completion with open tasks")
	}
	done := mdModelBase()
	done.taskProgress = &taskProgress{3, 3, 100}
	if !strings.Contains(strings.Join(renderCompactDashboard(done, theme, 100, goalCompactOptions{}), "\n"), "All tasks complete") {
		t.Error("no completion line when every task is done")
	}

	// Expanded rows indent by depth, capped at 6 levels.
	deep := mdModelBase()
	deep.taskTree = []dashboardTaskNode{{id: "x", title: "deepnode", status: "pending", depth: 9}}
	for _, l := range renderExpandedDashboard(deep, theme, 100, goalExpandedOptions{}) {
		if strings.Contains(l, "deepnode") && !strings.HasPrefix(l, "│ "+strings.Repeat(" ", 12)+"· x") {
			t.Errorf("indent is not capped at 6 levels: %q", l)
		}
	}

	// The compact id column is as wide as the longest id, capped at 10.
	wideID := mdModelBase()
	wideID.taskTree = []dashboardTaskNode{{id: "a", title: "first", status: "pending"}, {id: "abcdefghijkl", title: "second", status: "pending"}}
	hit := false
	for _, l := range renderCompactDashboard(wideID, theme, 100, goalCompactOptions{}) {
		if strings.Contains(l, "first") {
			hit = strings.Contains(l, "· a"+strings.Repeat(" ", 9)+"  first")
		}
	}
	if !hit {
		t.Error("id column is not padded to 10")
	}

	// A line wider than the terminal by one column is clamped.
	got := clampLinesToWidth([]string{strings.Repeat("a", 11), "short"}, 10)
	eq(t, visibleWidth(got[0]), 10, "clamped width")
	eq(t, strings.Contains(got[0], "…"), true, "clamped with an ellipsis")
	eq(t, got[1], "short", "short line kept")

	// The status line names the other open goals, even a single one.
	one := mdModelBase()
	one.otherOpenGoals = 1
	eq(t, strings.Contains(dashStatusLine(theme, one), " (+1 open)"), true, "one other open goal")
	one.otherOpenGoals = 0
	eq(t, strings.Contains(dashStatusLine(theme, one), "open"), false, "no other open goals")
}

// ── auditor dashboard model ──

func TestMutantsDAuditorDashboardModel(t *testing.T) {
	states := func(pct float64) map[string]string {
		m := deriveAuditorDashboardModel(auditorProgress{percentage: &pct, phase: "running"}, deriveAuditorDashboardOptions{})
		out := map[string]string{}
		for _, c := range m.checks {
			out[c.id] = c.state
		}
		return out
	}
	s65 := states(65)
	eq(t, s65["workspace"], "running", "workspace at 65%")
	eq(t, s65["tasks"], "passed", "tasks at 65%")
	eq(t, s65["decision"], "pending", "decision at 65%")
	eq(t, states(85)["decision"], "running", "decision at 85%")
	eq(t, states(79)["decision"], "pending", "decision at 79%")

	label := deriveAuditorDashboardModel(auditorProgress{}, deriveAuditorDashboardOptions{}).auditorLabel
	eq(t, label, "Independent completion audit", "default label")

	// Findings: at most three, each cut to 200 UTF-16 units (197 + "...").
	card := deriveAuditResultCard("disapproved", "- a\n- b\n- c\n- d")
	eq(t, card.lines, []string{"a", "b", "c"}, "three findings")
	long200 := strings.Repeat("x", 200)
	eq(t, deriveAuditResultCard("disapproved", long200).lines, []string{long200}, "200 units kept")
	eq(t, deriveAuditResultCard("disapproved", long200+"y").lines, []string{strings.Repeat("x", 197) + "..."}, "201 units cut")
}

// ── text cache ──

func TestMutantsDTextCache(t *testing.T) {
	reset := func() { textCache.entries = map[string]textCacheEntry{}; textCache.order = nil; textCache.retained = 0 }
	reset()
	defer reset()
	for i := range 1024 {
		truncateToWidth("t"+strconv.Itoa(i), 5, "…", false)
	}
	eq(t, len(textCache.entries), 1024, "1024 entries fit")
	truncateToWidth("one more", 5, "…", false)
	eq(t, len(textCache.entries), 1024, "the oldest entry is evicted at 1024")
	_, first := textCache.entries["t:5:false:1:…t0"]
	eq(t, first, false, "oldest evicted")

	reset()
	a := wrapTextWithAnsi("aaa bbb ccc", 3)
	a[0] = "X"
	b := wrapTextWithAnsi("aaa bbb ccc", 3)
	eq(t, b[0], "aaa", "a miss does not hand out the cached rows")
	b[0] = "Y"
	c := wrapTextWithAnsi("aaa bbb ccc", 3)
	eq(t, c[0], "aaa", "a hit hands out a private copy")
}

// ── dialog scaffold ──

func TestMutantsDScaffold(t *testing.T) {
	id := func(s string) string { return s }
	eq(t, borderedLine(id, 5, "abcd"), "│abcd │", "one cell of padding")
	eq(t, borderedLine(id, 5, "abcde"), "│abcde│", "exact fit")
	eq(t, borderedLine(id, 4, "abcdef"), "│abcdef│", "overflow is not padded")
}

// ── auditor model selector ──

func TestMutantsDSelector(t *testing.T) {
	labels := func(cs []auditorChoice) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.label)
		}
		return out
	}
	choices := buildAuditorModelChoices([]auditorModelSummary{{"zeta", "m1", ""}, {"alpha", "m2", ""}, {"mid", "m3", ""}}, "", "")
	eq(t, labels(choices)[1:4], []string{"  alpha/m2", "  mid/m3", "  zeta/m1"}, "models are listed alphabetically")

	// A model whose name equals its id carries no name suffix; a different name does.
	eq(t, auditorModelLabel(auditorModelSummary{"p", "m", "m"}), "p/m", "name equals id")
	eq(t, auditorModelLabel(auditorModelSummary{"p", "m", "Model M"}), "p/m — Model M", "distinct name")
	eq(t, auditorModelLabel(auditorModelSummary{"p", "m", ""}), "p/m", "no name")

	// The default entry is ticked only when nothing is configured.
	withConfig := buildAuditorModelChoices([]auditorModelSummary{{"p", "m", ""}}, "p/m", "s/x")
	eq(t, withConfig[0].label, "  Current session / default (s/x)", "configured model unticks the default")
	eq(t, withConfig[1].label, "✓ p/m", "configured model ticked")
	noConfig := buildAuditorModelChoices(nil, "", "s/x")
	eq(t, noConfig[0].label, "✓ Current session / default (s/x)", "default ticked")

	// The filter ignores case on both sides.
	cs := buildAuditorModelChoices([]auditorModelSummary{{"OpenAI", "GPT-X", ""}, {"other", "m", ""}}, "", "")
	got := labels(filterAuditorModelChoices(cs, "  OPENAI "))
	eq(t, len(got), 3, "default + the match + manual")
	eq(t, got[1], "  OpenAI/GPT-X", "mixed-case label matches a lower-case filter")
}

// ── blocker Oracle ──

func TestMutantsDOracle(t *testing.T) {
	g := oracleGoal()
	base := buildBlockerFingerprint(g, "r")

	other := oracleGoal()
	other.set("id", gstr(g, "id")) // goal ids are random; isolate the objective
	other.set("objective", "A different objective.")
	eq(t, buildBlockerFingerprint(other, "r") != base, true, "objective changes the fingerprint")

	cur1, cur2 := oracleGoal(), oracleGoal()
	cur1.set("id", gstr(g, "id"))
	cur2.set("id", gstr(g, "id"))
	cur1.set("currentTaskId", "t1")
	cur2.set("currentTaskId", "t2")
	eq(t, buildBlockerFingerprint(cur1, "r") != buildBlockerFingerprint(cur2, "r"), true, "current task changes the fingerprint")
	eq(t, buildBlockerFingerprint(cur1, "r") != base, true, "having a current task changes the fingerprint")

	// Normalisation: NFKC, lower-case, whitespace runs collapsed, trimmed.
	eq(t, normalizeBlockerReason("ＡＢＣ  Ｘ"), "abc x", "NFKC folds full-width forms")
	eq(t, normalizeBlockerReason("a   b\n\n c\t"), "a b c", "whitespace collapses to one space")

	// Task depth is part of the task-state fingerprint.
	nested := oracleGoal()
	nested.set("taskList", jo(t, `{"tasks":[{"id":"a","title":"A","status":"pending","subtasks":[{"id":"b","title":"B","status":"pending"}]}],"blockCompletion":false,"proposedAt":""}`))
	flat := oracleGoal()
	flat.set("taskList", jo(t, `{"tasks":[{"id":"a","title":"A","status":"pending"},{"id":"b","title":"B","status":"pending"}],"blockCompletion":false,"proposedAt":""}`))
	eq(t, taskStateFingerprint(nested) != taskStateFingerprint(flat), true, "nesting changes the task-state fingerprint")

	// State reconstruction reads only this goal's events for this fingerprint.
	events := oracleEvents(t,
		`{"type":"oracle_failed","goalId":"g1","fingerprint":"f","errorCode":"provider","message":"m1"}`,
		`{"type":"oracle_failed","goalId":"g2","fingerprint":"f","errorCode":"provider","message":"other goal"}`,
		`{"type":"oracle_result","goalId":"g2","fingerprint":"f","adviceId":"x","disposition":"actionable","summary":"s"}`,
		`{"type":"oracle_followup_attempted","goalId":"g2","fingerprint":"f"}`)
	st := oracleStateForFingerprint(events, "g1", "f")
	eq(t, st.failedAttempts, 1, "one failure for g1")
	eq(t, st.lastFailure, &oracleConsultFailure{"provider", "m1"}, "last failure")
	eq(t, st.result == nil, true, "no result of another goal")
	eq(t, st.followupAttempted, false, "no follow-up of another goal")

	// The prompt cuts recent evidence at 2000 units.
	p := buildBlockerOraclePrompt(g, "stuck", nil, strings.Repeat("Z", 2500))
	eq(t, strings.Count(p, "Z"), 2000, "evidence cap")

	// Advice: 1-4 alternatives and the three dispositions.
	alt := `{"title":"T","rationale":"R","steps":["s"],"expectedEvidence":[]}`
	advice := func(n int, disp string) any {
		alts := make([]string, n)
		for i := range alts {
			alts[i] = alt
		}
		return jo(t, `{"diagnosis":"d","alternatives":[`+strings.Join(alts, ",")+`],"recommendedIndex":0,"unresolvedQuestions":[],"disposition":"`+disp+`"}`)
	}
	_, msg := validateAdvice(advice(4, "actionable"))
	eq(t, msg, "", "four alternatives are valid")
	_, msg = validateAdvice(advice(5, "actionable"))
	eq(t, msg, "1-4 alternatives required", "five alternatives are refused")
	a, msg := validateAdvice(advice(1, "insufficient_context"))
	eq(t, msg, "", "insufficient_context is a disposition")
	eq(t, a.disposition, "insufficient_context", "disposition kept")
	_, msg = validateAdvice(advice(1, "maybe"))
	eq(t, msg, "disposition must be actionable|needs_human|insufficient_context", "unknown disposition")

	// At most eight attempted actions reach the prompt.
	acts := []string{}
	for i := range 10 {
		acts = append(acts, "act"+strconv.Itoa(i))
	}
	var prompt string
	sess := &fakeAuditorSession{onPrompt: func(text string) error { prompt = text; return nil }}
	runBlockerOracle(runBlockerOracleArgs{ctx: oracleFakeCtx(), goal: g, reason: "stuck", attemptedActions: acts,
		settings: oracleSettings("anthropic", "claude-strong"), createSession: auditorSessionOf(sess)})
	eq(t, strings.Contains(prompt, "- act7\n") || strings.HasSuffix(strings.Split(prompt, "</attempted_actions>")[0], "- act7"), true, "act7 is kept")
	eq(t, strings.Contains(prompt, "act8"), false, "act8 is dropped")
}

// ── completion auditor ──

func TestMutantsDAuditor(t *testing.T) {
	// The verdict marker is the last non-empty line after trimming each line.
	eq(t, parseAuditorDecision("ok\n<approved/>\n   \n"), auditorDecision{approved: true}, "trailing blank line")
	eq(t, parseAuditorDecision("ok\n  <disapproved/>  "), auditorDecision{disapproved: true}, "padded marker")

	// Prompt: a blank claim becomes the placeholder; the claim is trimmed.
	g := auditorGoal(t, "Finish X")
	p := buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g, completionSummary: " \n "})
	eq(t, strings.Contains(p, "<executor_claim>\n(no claim provided)\n</executor_claim>"), true, "blank claim")
	p = buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g, completionSummary: "  did it  "})
	eq(t, strings.Contains(p, "<executor_claim>\ndid it\n</executor_claim>"), true, "trimmed claim")

	// No contract and no task list: neither section appears.
	p = buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g})
	for _, absent := range []string{"<verification_contract>", "3. Verify that the executor", "<task_state>"} {
		eq(t, strings.Contains(p, absent), false, absent+" absent")
	}
	// Disabled settings drop them even when the goal carries them.
	g2 := auditorGoal(t, "Finish X")
	g2.set("verificationContract", "run it")
	g2.set("taskList", wgTaskList(wgTask("t1", "T", "pending")))
	p = buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g2})
	for _, present := range []string{"<verification_contract>", "3. Verify that the executor", "<task_state>"} {
		eq(t, strings.Contains(p, present), true, present+" present")
	}
	p = buildGoalAuditorPrompt(goalAuditorPromptArgs{goal: g2, settings: &goalSettings{disableContracts: true, disableTasks: true}})
	for _, absent := range []string{"<verification_contract>", "<task_state>"} {
		eq(t, strings.Contains(p, absent), false, absent+" absent when disabled")
	}

	// The task gate needs blockCompletion; skipped tasks count as pending in the gate (pending = total - complete).
	gate := " | TASK GATE: pending tasks block completion"
	open := wgTaskList(wgTask("t1", "T", "pending"))
	eq(t, strings.Contains(taskSummaryBlock(open), "TASK GATE"), false, "no gate without blockCompletion")
	open.set("blockCompletion", true)
	eq(t, strings.HasPrefix(taskSummaryBlock(open), "Tasks: 0/1 complete"+gate), true, "gate with a pending task")
	skipOnly := wgTaskList(wgTask("t1", "T", "complete"), wgTask("t2", "U", "skipped"))
	skipOnly.set("blockCompletion", true)
	eq(t, strings.HasPrefix(taskSummaryBlock(skipOnly), "Tasks: 1/2 complete, 1 skipped"+gate), true, "a skipped task still counts as pending for the gate")

	// An ambiguous model name is refused, not resolved to the first match.
	reg := &fakeAuditorRegistry{available: []*auditorModel{{"a", "x", "claude"}, {"b", "y", "claude"}}}
	m, errMsg := resolveAuditorModel(&fakeAuditorCtx{dir: t.TempDir(), reg: reg}, goalSettings{model: "claude"})
	eq(t, m == nil, true, "ambiguous model")
	eq(t, errMsg, "Configured auditor model is ambiguous or unavailable: claude", "ambiguous message")
	reg.available = reg.available[:1]
	m, errMsg = resolveAuditorModel(&fakeAuditorCtx{dir: t.TempDir(), reg: reg}, goalSettings{model: "claude"})
	eq(t, errMsg, "", "a unique name resolves")
	eq(t, m != nil && m.id == "x", true, "unique model")

	// An AbortError from the session reads "Auditor aborted." even without a fired signal.
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: g, detailedSummary: "d",
		createSession: auditorSessionFactoryFunc(func(auditorSessionOptions) (auditorSession, error) { return nil, &auditorAbortError{"boom"} })})
	eq(t, res.err, "Auditor aborted.", "abort error")
	eq(t, res.disapproved, true, "abort disapproves")

	// Two assistant messages join with a blank line; the progress tail keeps the last eight lines.
	var sess *fakeAuditorSession
	var seen []auditorProgress
	sess = &fakeAuditorSession{onPrompt: func(string) error {
		text := func(s string) auditorSessionEvent {
			return auditorSessionEvent{typ: "message_end", message: &auditorMessage{role: "assistant", content: []auditorContentPart{{typ: "text", text: &s}}}}
		}
		sess.listener(text("first"))
		sess.listener(text("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n<disapproved/>"))
		return nil
	}}
	res = runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: g, detailedSummary: "d",
		createSession: auditorSessionOf(sess), onProgress: func(p auditorProgress) { seen = append(seen, p) }})
	eq(t, res.output, "first\n\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n<disapproved/>", "outputs joined by a blank line")
	var tail []string
	for _, p := range seen {
		if p.label == "Producing report..." {
			tail = p.recentOutput
		}
	}
	eq(t, tail, []string{"l4", "l5", "l6", "l7", "l8", "l9", "l10", "<disapproved/>"}, "eight-line tail")

	// A short report is shown whole.
	var short *fakeAuditorSession
	seen = nil
	short = &fakeAuditorSession{onPrompt: func(string) error {
		s := "a\nb\nc\nd\ne"
		short.listener(auditorSessionEvent{typ: "message_end", message: &auditorMessage{role: "assistant", content: []auditorContentPart{{typ: "text", text: &s}}}})
		return nil
	}}
	runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: g, detailedSummary: "d",
		createSession: auditorSessionOf(short), onProgress: func(p auditorProgress) { seen = append(seen, p) }})
	tail = nil
	for _, p := range seen {
		if p.label == "Producing report..." {
			tail = p.recentOutput
		}
	}
	eq(t, tail, []string{"a", "b", "c", "d", "e"}, "five-line tail")

	// Tool arguments are shown cut to 120 units.
	eq(t, auditorArgsText(strings.Repeat("a", 130)), strings.Repeat("a", 120), "string args")
	eq(t, utf16Len(auditorArgsText(jo(t, `{"k":"`+strings.Repeat("b", 200)+`"}`))), 120, "object args")
}

// ── terminal keys ──

func TestMutantsDKeys(t *testing.T) {
	// Escape on a goal that is not active does not pause it.
	h := keybindingHarness(false)
	h.core.goal = wgObj("status", "blocked", "autoContinue", true)
	eq(t, h.fire("\x1b"), (*goalInputResult)(nil), "blocked goal: escape passes through")
	eq(t, *h.consumed, []string{}, "blocked goal is not paused")
	h.core.goal = wgObj("status", "paused", "autoContinue", true)
	h.fire("\x1b")
	eq(t, *h.consumed, []string{}, "paused goal is not paused again")

	// No UI: no listener is registered (the UI is never touched).
	syncTerminalInputPause(&wgCore{consumed: &[]string{}, expanded: new(bool)}, headlessCtx{})

	// Only "true" and "1" enable the debug helpers.
	for v, want := range map[string]bool{"true": true, "1": true, "yes": false, "0": false, "": false, "false": false} {
		t.Setenv("PI_GOAL_DEBUG", v)
		eq(t, isDebugEnabled(), want, "PI_GOAL_DEBUG="+v)
	}

	// Kitty repeat/release events: consumed only for the shortcuts that own them.
	esc := keybindingHarness(false)
	esc.core.goal = activeAutoGoal()
	consumeOK(t, esc.fire("\x1b[27;1:2u"), "repeat of escape with a live goal")
	noGoal := keybindingHarness(false)
	eq(t, noGoal.fire("\x1b[27;1:2u"), (*goalInputResult)(nil), "repeat of escape without goal or expansion")

	expanded := keybindingHarness(true)
	eq(t, expanded.fire("\x1b[1;6:2B"), (*goalInputResult)(nil), "ctrl+shift+down repeat while expanded")
	consumeOK(t, expanded.fire("\x1b[1;1:2B"), "plain down repeat while expanded")
	compact := keybindingHarness(false)
	eq(t, compact.fire("\x1b[1;1:2B"), (*goalInputResult)(nil), "plain down repeat while compact")
	consumeOK(t, compact.fire("\x1b[1;6:2B"), "ctrl+shift+down repeat while compact")
}

// ── toggling a task through the service ──

type mdToggleHost struct {
	featHost
	onInput func() (string, bool)
}

func (h *mdToggleHost) Input(string, string) (string, bool) { return h.onInput() }

func mdToggleCore(t *testing.T, tasks string, onInput func(co *core) (string, bool), kv ...any) (*core, string) {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(cwd+"/.pi/goals/archived", 0o755); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	invalidateGoalSettingsCache()
	t.Cleanup(func() { invalidateGoalPoolCache(); invalidateGoalSettingsCache() })
	h := &mdToggleHost{featHost: featHost{fakeHost{cwd: cwd}}}
	co := newCore(h)
	h.onInput = func() (string, bool) { return onInput(co) }
	goal := mustWrite(t, storage{cwd: cwd}, featGoal("Toggle test", false, 0))
	co.loadState()
	id := gstr(goal, "id")
	co.setFocusedGoalId(new(id), "selected", false)
	co.updateFocusedGoal(goalWith(co.state(), append([]any{"taskList", featTaskList(t, tasks)}, kv...)...), true)
	return co, id
}

func mdTask(co *core, i int) *jsObject {
	return asRecord(co.state().obj("taskList").vals["tasks"].([]any)[i])
}

func TestMutantsDToggleTask(t *testing.T) {
	noInput := func(*core) (string, bool) { t.Error("the evidence dialog opened"); return "", false }
	contract := `[{"id":"t1","title":"T1","status":"pending","verificationContract":"run it"}]`

	// Only an active goal takes task updates.
	co, id := mdToggleCore(t, `[{"id":"t1","title":"T1","status":"pending"}]`, noInput, "status", "paused")
	ok, msg := co.toggleTaskViaService(id, "t1")
	eq(t, ok, false, "paused goal")
	eq(t, msg, "Task updates apply only to an active goal (current status: paused).", "paused message")
	eq(t, gstr(mdTask(co, 0), "status"), "pending", "task untouched")

	// A blank evidence answer is refused; the contract stays unmet.
	co, id = mdToggleCore(t, contract, func(*core) (string, bool) { return "  \n ", true })
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, false, "blank evidence")
	eq(t, msg, `Task "t1" has a verification contract; provide evidence to complete it.`, "blank evidence message")
	eq(t, gstr(mdTask(co, 0), "status"), "pending", "task still pending")

	// Evidence is trimmed and cut to 200 units.
	co, id = mdToggleCore(t, contract, func(*core) (string, bool) { return "  " + strings.Repeat("e", 300) + "  ", true })
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, true, "evidence given: "+msg)
	eq(t, gstr(mdTask(co, 0), "evidence"), strings.Repeat("e", 200), "evidence cut")

	// Disabled contracts need no evidence.
	t.Setenv("PI_GOAL_DISABLE_CONTRACTS", "true")
	co, id = mdToggleCore(t, contract, noInput)
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, true, "contracts disabled: "+msg)
	eq(t, gstr(mdTask(co, 0), "status"), "complete", "completed without evidence")
	t.Setenv("PI_GOAL_DISABLE_CONTRACTS", "")
	invalidateGoalSettingsCache()

	// Pending subtasks block completion.
	co, id = mdToggleCore(t, `[{"id":"t1","title":"T1","status":"pending","subtasks":[{"id":"t1.1","title":"S","status":"pending"}]}]`, noInput)
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, false, "pending subtask")
	eq(t, msg, `Task "t1" has pending subtask "t1.1". Complete or skip all subtasks first.`, "subtask gate")
	eq(t, gstr(mdTask(co, 0), "status"), "pending", "parent still pending")

	// Reopening clears the completion stamp and the evidence.
	co, id = mdToggleCore(t, `[{"id":"t1","title":"T1","status":"complete","completedAt":"2026-01-01T00:00:00.000Z","evidence":"done"}]`, noInput)
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, true, "reopen: "+msg)
	task := mdTask(co, 0)
	eq(t, gstr(task, "status"), "pending", "reopened")
	eq(t, strings.Contains(show(task), "completedAt") || strings.Contains(show(task), "evidence"), false, "completion stamp and evidence cleared: "+show(task))

	// A task completed behind the dialog's back is refused by the service's own gate.
	completed := `[{"id":"t1","title":"T1","status":"complete","verificationContract":"run it"}]`
	co, id = mdToggleCore(t, contract, func(co *core) (string, bool) {
		co.updateFocusedGoal(goalWith(co.state(), "taskList", featTaskList(t, completed)), true)
		return "evidence", true
	})
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, false, "completed during the dialog")
	eq(t, msg, `Task "t1" is already complete.`, "already complete")

	// A skipped task cannot be toggled.
	co, id = mdToggleCore(t, `[{"id":"t1","title":"T1","status":"skipped"}]`, noInput)
	ok, msg = co.toggleTaskViaService(id, "t1")
	eq(t, ok, false, "skipped task")
	eq(t, msg, `Task "t1" is skipped; change it with /goal-tweak or update_goal_task.`, "skipped message")
}

// ── widget ──

type mdRenderContainer struct {
	kids  []any
	lines int
}

func (c *mdRenderContainer) dockChildren() []any { return c.kids }
func (c *mdRenderContainer) render(int) []string { return make([]string, c.lines) }

func TestMutantsDWidget(t *testing.T) {
	theme := wgTestTheme

	// The terminal-height bound reserves 6 rows.
	twenty := nLines(20, func(i int) string { return "l" + strconv.Itoa(i) })
	eq(t, len(boundWidgetRenderLines(twenty, rowsPtr(10))), 4, "10 rows minus 6 reserved")
	eq(t, len(boundWidgetRenderLines(twenty, rowsPtr(0))), 20, "zero rows is unbounded")
	eq(t, len(applyStableHeightBound(twenty, rowsPtr(0), freshStableState(), "r", 6)), 20, "stable bound: zero rows is unbounded")
	eq(t, len(applyStableHeightBound(twenty, rowsPtr(-3), freshStableState(), "r", 6)), 20, "stable bound: negative rows is unbounded")

	// Once latched, the height holds: a list one line over the latch is cut back.
	st := freshStableState()
	applyStableHeightBound(nLines(5, func(int) string { return "x" }), rowsPtr(50), st, "r", 6)
	eq(t, len(applyStableHeightBound(nLines(6, func(int) string { return "x" }), rowsPtr(50), st, "r", 6)), 5, "one line over the latch")
	eq(t, len(applyStableHeightBound(nLines(3, func(int) string { return "x" }), rowsPtr(50), st, "r", 6)), 5, "padded up to the latch")

	// Zero elapsed live seconds hand the very same goal back.
	goal := wgGoal()
	acc := mdAccounting{active: true, live: 0}
	if got := liveDisplayGoal(goal, acc); got != goal {
		t.Error("live 0 must return the goal itself")
	}
	acc.live = 4
	live := liveDisplayGoal(goal, acc)
	eq(t, gnum(live.obj("usage"), "activeSeconds"), 69.0, "live seconds added")

	// Audit tool details show with expanded or with debug.
	progress := wgAuditorProgress(func(p *auditorWidgetProgress) { p.currentToolStartedAt = nil })
	render := func(o goalWidgetLinesOptions) string {
		o.auditorProgress = progress
		return strings.Join(renderGoalWidgetLines(nil, theme, 100, o), "\n")
	}
	plain, exp, dbg := render(goalWidgetLinesOptions{}), render(goalWidgetLinesOptions{expanded: true}), render(goalWidgetLinesOptions{debug: true})
	eq(t, exp != plain, true, "expanded shows tool details")
	eq(t, dbg, exp, "debug shows the same tool details as expanded")

	// One open goal and none focused: the unfocused panel.
	eq(t, strings.Contains(strings.Join(renderGoalWidgetLines(nil, theme, 100, goalWidgetLinesOptions{openGoalCount: 1}), "\n"), "1 open goal is available."), true, "unfocused panel for one goal")
	eq(t, len(renderGoalWidgetLines(nil, theme, 100, goalWidgetLinesOptions{openGoalCount: 0})), 0, "nothing without open goals")

	// Without a count callback a focused goal counts as the only open goal.
	tui, _ := createMockTUI()
	solo := newGoalWidgetComponent(goalWidgetOptions{tui: tui, theme: theme, getGoal: func() *jsObject { return wgGoal() }})
	eq(t, strings.Contains(strings.Join(solo.Render(100), "\n"), "(+"), false, "no other open goals")
	eq(t, solo.getOpenGoalCount(), 1, "default count with a goal")
	none := newGoalWidgetComponent(goalWidgetOptions{tui: tui, theme: theme, getGoal: func() *jsObject { return nil }})
	eq(t, none.getOpenGoalCount(), 0, "default count without a goal")

	// A finished audit's result card wins over live audit progress.
	card := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) {
		o.getAuditResult = func() *auditResultView { return &auditResultView{verdict: "approved"} }
		o.getAuditorProgress = func() *auditorWidgetProgress { return wgAuditorProgress(nil) }
	})
	eq(t, strings.Contains(strings.Join(card.Render(100), "\n"), "APPROVED"), true, "result card shown")

	// Regime key: goal, status, state kind, expanded, debug, tasks disabled, has tasks.
	expanded, tasks := false, false
	rc := newWidget(tui, func() *jsObject {
		if tasks {
			return wgGoal("taskList", wgTaskList(wgTask("t1", "T", "pending")))
		}
		return wgGoal()
	}, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return expanded } })
	eq(t, rc.stableHeightRegime(), "test-goal-001|active|focused|false|false|false|false", "compact regime")
	expanded = true
	eq(t, rc.stableHeightRegime(), "test-goal-001|active|focused|true|false|false|false", "expanded regime")
	tasks = true
	eq(t, rc.stableHeightRegime(), "test-goal-001|active|focused|true|false|false|true", "regime with tasks")
	unfocused := newWidget(tui, func() *jsObject { return nil }, nil)
	eq(t, unfocused.stableHeightRegime(), "∅|∅|unfocused|false|false|false|false", "one open goal, none focused")
	empty := newWidget(tui, func() *jsObject { return nil }, func(o *goalWidgetOptions) { o.getOpenGoalCount = func() int { return 0 } })
	eq(t, empty.stableHeightRegime(), "∅|∅|none|false|false|false|false", "no open goals")

	// Width is at least 1.
	w := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
	w.Render(0)
	eq(t, w.lastRenderWidth, 1, "render width floor")
	w.Render(-4)
	eq(t, w.lastRenderWidth, 1, "negative render width floor")

	// A compact list that exactly fits its window does not scroll.
	five := newWidget(tui, func() *jsObject {
		ts := make([]any, 5)
		for i := range ts {
			ts[i] = wgTask("t"+strconv.Itoa(i+1), "Task", "pending")
		}
		return wgGoal("taskList", wgTaskList(ts...))
	}, nil)
	five.Render(100)
	eq(t, five.handleCompactScrollKey("down"), false, "5 rows in a 5-row window")
	// An expanded dashboard without tasks consumes nothing.
	emptyExp := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
	emptyExp.Render(100)
	eq(t, emptyExp.handleNavigationKey("down"), false, "no tasks to navigate")

	// One page is one viewport of rows.
	eq(t, widgetScrollOffset("pageDown", 0, 5, 20), 5, "page down")
	eq(t, widgetScrollOffset("pageUp", 9, 5, 20), 4, "page up")
}

type mdAccounting struct {
	active bool
	live   float64
}

func (a mdAccounting) isActiveFor(string) bool { return a.active }
func (a mdAccounting) liveSeconds() float64    { return a.live }

func TestMutantsDWidgetDock(t *testing.T) {
	tui := &wgDockTUI{rows: 50}
	c := newGoalWidgetComponent(goalWidgetOptions{tui: tui, theme: wgTestTheme, getGoal: func() *jsObject { return wgGoal() }})
	node := func(n int) wgDockNode { return wgDockNode{r: func(int) []string { return make([]string, n) }} }
	cont := &mdRenderContainer{kids: []any{c}, lines: 7}

	// The document (first top-level child) and the widget's own container do not count; the rest does, plus one row of slack.
	tui.children = []any{node(100), node(2), cont, node(3)}
	got, ok := c.measureDockReserve(80)
	eq(t, ok, true, "measurable dock")
	eq(t, got, 6, "chrome height")

	// A dock with a single top-level child is not measurable.
	tui.children = []any{cont}
	_, ok = c.measureDockReserve(80)
	eq(t, ok, false, "single top-level child")
}

func TestMutantsDWidgetDebugPanel(t *testing.T) {
	tui, _ := createMockTUI()
	long := strings.Repeat("o", 100)
	goal := wgGoal("objective", long, "verificationContract", strings.Repeat("c", 100),
		"taskList", wgTaskList(wgTask("t1", strings.Repeat("n", 60), "pending")))
	c := newWidget(tui, func() *jsObject { return goal }, nil)
	lines := c.renderDebugPanel(80)
	find := func(prefix string) string {
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		t.Fatalf("no line %q in %q", prefix, lines)
		return ""
	}
	eq(t, utf16Len(find("  objective: "))-len("  objective: "), 80, "objective cut at 80")
	eq(t, utf16Len(find("  vContract: "))-len("  vContract: "), 60, "contract cut at 60")
	eq(t, find("  next: t1 ("), "  next: t1 ("+strings.Repeat("n", 37)+"...)", "next title cut at 40")

	// The rule is at least 20 wide.
	eq(t, c.renderDebugPanel(5)[0], strings.Repeat("─", 20), "minimum rule width")
	eq(t, c.renderDebugPanel(20)[0], strings.Repeat("─", 20), "20-wide rule")
	eq(t, c.renderDebugPanel(33)[0], strings.Repeat("─", 33), "33-wide rule")
}

// ── escape dialog ──

var mdAnsi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestMutantsDEscapeDialog(t *testing.T) {
	line := func(lines []string, sub string) string {
		for _, l := range lines {
			if strings.Contains(l, sub) {
				return mdAnsi.ReplaceAllString(l, "")
			}
		}
		t.Fatalf("no line with %q in %q", sub, lines)
		return ""
	}
	// Inner width is min(width, 64) - 2 = 62 at width 80.
	ctx := escapeFixture(t, strings.Repeat("A", 200))
	tui, state := createMockTUI()
	state.getShowHardwareCursor = true
	comp := escapeComponent(t, ctx, tui, func(any) {})
	lines := comp.Render(80)
	eq(t, line(lines, "Goal: "), "│  Goal: "+strings.Repeat("A", 47)+"…"+strings.Repeat(" ", 62-2-6-48)+"│", "objective cut at inner-14")

	// The description is cut at inner-10 (52 columns).
	desc := line(lines, "Resume work")
	body := strings.TrimSuffix(strings.TrimPrefix(desc, "│  "+strings.Repeat(" ", 4)), "│")
	body = strings.TrimRight(body, " ")
	eq(t, visibleWidth(body), 52, "description width")
	eq(t, strings.HasSuffix(body, "…"), true, "description ellipsis")

	// The selected label is cut at inner-6 on a narrow dialog (width 30 => inner 28 => 22 columns).
	comp.HandleInput("\x1b[A") // move up to the first option
	narrow := comp.Render(30)
	l := line(narrow, "Mark complete")
	eq(t, strings.Contains(l, "▸ Mark complete without…"), true, "label cut to 22 columns: "+l)

	// Dispose restores the cursor to what it was when the dialog opened.
	comp.dispose()
	eq(t, state.setShowHardwareCursorCalls, []bool{false, true}, "cursor hidden then restored to its prior state")
}

// ── task list overlay ──

func mdOverlay(pool *goalPool, focused *string, opts taskListOverlayOptions) (*taskListOverlay, *mockTUIState) {
	tui, state := createMockTUI()
	o := &taskListOverlay{tui: tui, theme: wgTheme{}, done: func(any) {}, goalsByID: pool, focusedGoalID: focused, opts: opts, cursorEntryIndex: -1, lastRenderWidth: 80}
	o.rebuildEntries()
	return o, state
}

func mdOverlayManyTasks(n int) *goalPool {
	ts := make([]any, n)
	for i := range ts {
		ts[i] = wgTask("t"+strconv.Itoa(i+1), "Task "+strconv.Itoa(i+1), "pending")
	}
	return poolOf(overlayMakeGoal("g1", "Big goal", "taskList", wgTaskList(ts...)))
}

func TestMutantsDTaskListOverlay(t *testing.T) {
	// Geometry: the visible height is inner/2.8 with a floor of 8.
	eq(t, overlayVisibleHeight(88), 31, "88 wide")
	eq(t, overlayVisibleHeight(30), 10, "30 wide")
	eq(t, overlayVisibleHeight(10), 8, "10 wide")
	eq(t, overlayVisibleHeight(22), 8, "22 wide")

	// The frame is as wide as the dialog allows, 90 inner columns at most (minus the 2 border cells = 88 at width 100).
	o, _ := mdOverlay(mdOverlayManyTasks(1), sp("g1"), taskListOverlayOptions{})
	eq(t, o.Render(100)[0], "┌"+strings.Repeat("─", 88)+"┐", "frame at width 100")
	eq(t, o.Render(140)[0], "┌"+strings.Repeat("─", 88)+"┐", "frame at width 140")
	eq(t, o.Render(60)[0], "┌"+strings.Repeat("─", 58)+"┐", "frame at width 60")

	// Goal icons: paused wins over sisyphus, sisyphus over the plain dot.
	icon := func(kv ...any) string {
		o, _ := mdOverlay(poolOf(overlayMakeGoal("g1", "x", kv...)), sp("g1"), taskListOverlayOptions{})
		return o.entries[0].icon
	}
	eq(t, icon(), "●", "active")
	eq(t, icon("sisyphus", true), "◆", "sisyphus")
	eq(t, icon("status", "paused"), "⏸", "paused")
	eq(t, icon("status", "paused", "sisyphus", true), "⏸", "paused sisyphus")

	// The summary line names the skipped tasks; the branches and icons by status and position; nesting indents.
	mixed, _ := mdOverlay(poolOf(overlayGoalWithMixedTasks("g3", "G")), sp("g3"), taskListOverlayOptions{})
	eq(t, mixed.entries[1], overlayEntry{kind: "task-summary", text: "1/3 done (1 skipped)"}, "summary")
	prefixes := []string{}
	for _, e := range mixed.entries {
		if e.kind == "task" {
			prefixes = append(prefixes, e.prefix)
		}
	}
	eq(t, prefixes, []string{"    ├─ ✓", "    ├─ ◌", "    └─ —"}, "task prefixes")
	nested, _ := mdOverlay(poolOf(overlayMakeGoal("g4", "G", "taskList", wgTaskList(
		wgTask("a", "A", "pending", "subtasks", []any{wgTask("a.1", "A1", "complete"), wgTask("a.2", "A2", "pending")})))), sp("g4"), taskListOverlayOptions{})
	prefixes = nil
	for _, e := range nested.entries {
		if e.kind == "task" {
			prefixes = append(prefixes, e.prefix)
		}
	}
	eq(t, prefixes, []string{"    └─ ◌", "      ├─ ✓", "      └─ ◌"}, "nested prefixes")
	one, _ := mdOverlay(poolOf(overlayMakeGoal("g5", "G", "taskList", wgTaskList(wgTask("a", "A", "complete", "subtasks", nil)))), sp("g5"), taskListOverlayOptions{})
	eq(t, one.entries[1].text, "1/1 done", "no skipped suffix")

	// With no focused goal the current-goal view is empty.
	none, _ := mdOverlay(poolOf(overlayGoalWithTasks("g1", "G", "T")), nil, taskListOverlayOptions{})
	eq(t, none.entries, []overlayEntry{{kind: "empty-message", text: "No open goals to display."}}, "no focus")

	// All-goals view: a separator between goals, singular/plural counts.
	pool := poolOf(overlayGoalWithTasks("g1", "One", "T"), overlayGoalWithTasks("g2", "Two", "T"))
	all, _ := mdOverlay(pool, sp("g1"), taskListOverlayOptions{})
	all.HandleInput("a")
	kinds := []string{}
	for _, e := range all.entries {
		kinds = append(kinds, e.kind)
	}
	eq(t, kinds, []string{"goal-header", "task-summary", "task", "separator", "goal-header", "task-summary", "task"}, "separator between goals")
	header := func(o *taskListOverlay) string { return o.Render(80)[1] }
	single, _ := mdOverlay(poolOf(overlayGoalWithTasks("g1", "One", "T")), sp("g1"), taskListOverlayOptions{})
	single.HandleInput("a")
	eq(t, strings.Contains(header(single), "Tasks (1 goal, 1 task)"), true, "singular: "+header(single))
	eq(t, strings.Contains(header(all), "Tasks (2 goals, 2 tasks)"), true, "plural: "+header(all))
	noTasks, _ := mdOverlay(poolOf(overlayMakeGoal("g1", "One")), sp("g1"), taskListOverlayOptions{})
	noTasks.HandleInput("a")
	eq(t, strings.Contains(header(noTasks), "Tasks (1 goal, 0 tasks)"), true, "zero tasks: "+header(noTasks))
	empty, _ := mdOverlay(poolOf(), sp("g1"), taskListOverlayOptions{})
	empty.HandleInput("a")
	eq(t, strings.Contains(header(empty), "Tasks (0 goals, 0 tasks)"), true, "zero goals: "+header(empty))

	// Footer hints.
	footer := func(o *taskListOverlay) string {
		l := o.Render(120)
		return l[len(l)-2]
	}
	eq(t, strings.Contains(footer(single), "'a' to show current"), true, "all view offers show current")
	back, _ := mdOverlay(poolOf(overlayGoalWithTasks("g1", "One", "T")), sp("g1"), taskListOverlayOptions{})
	eq(t, strings.Contains(footer(back), "'a' to show all"), true, "current view offers show all")
	eq(t, strings.Contains(footer(back), "Enter toggle task"), false, "no toggle hint without a callback")
	inter, _ := mdOverlay(poolOf(overlayGoalWithTasks("g1", "One", "T")), sp("g1"), taskListOverlayOptions{onToggleTask: func(string, string) overlayToggleResult { return overlayToggleResult{ok: true} }})
	eq(t, strings.Contains(footer(inter), "Enter toggle task"), true, "toggle hint with a callback")
}

func TestMutantsDTaskListOverlayScroll(t *testing.T) {
	// 40 tasks + header + summary = 42 rendered lines; at width 80 the window is 27 rows, so the last offset is 15.
	o, _ := mdOverlay(mdOverlayManyTasks(40), sp("g1"), taskListOverlayOptions{})
	text := func() string { return strings.Join(o.Render(80), "\n") }
	text()
	eq(t, o.renderedLineCount, 42, "rendered lines")
	maxO := 15

	o.scrollOffset = 1
	eq(t, strings.Contains(text(), "▴  1/42 lines"), true, "scroll-up hint at offset 1")
	o.scrollOffset = 0
	eq(t, strings.Contains(text(), "▴"), false, "no scroll-up hint at the top")
	o.scrollOffset = maxO - 1
	eq(t, strings.Contains(text(), "▾  1 more lines"), true, "scroll-down hint one before the end")
	o.scrollOffset = maxO
	eq(t, strings.Contains(text(), "▾"), false, "no scroll-down hint at the end")

	// Keys clamp to [0, max].
	o.scrollOffset = maxO
	o.HandleInput("\x1b[B")
	eq(t, o.scrollOffset, maxO, "down at the end")
	o.scrollOffset = 0
	o.HandleInput("\x1b[A")
	eq(t, o.scrollOffset, 0, "up at the top")
	o.scrollOffset = 3
	o.HandleInput("\x1b[A")
	eq(t, o.scrollOffset, 2, "up")
	o.scrollOffset = maxO
	o.HandleInput("\x1b[5~") // page up: one window (27 rows), clamped at the top
	eq(t, o.scrollOffset, 0, "page up")
	o.HandleInput("\x1b[6~") // page down
	eq(t, o.scrollOffset, maxO, "page down clamps at the end")
	o.scrollOffset = 0
	o.HandleInput("\x1b[F") // end
	eq(t, o.scrollOffset, maxO, "end")
	o.HandleInput("\x1b[H") // home
	eq(t, o.scrollOffset, 0, "home")

	// Toggling the goal set returns to the top.
	o.scrollOffset = 5
	o.HandleInput("a")
	eq(t, o.scrollOffset, 0, "'a' resets the scroll offset")

	// A page is a whole window: 100 rendered lines scroll 27 at a time.
	big, _ := mdOverlay(mdOverlayManyTasks(98), sp("g1"), taskListOverlayOptions{})
	big.Render(80)
	big.scrollOffset = 60
	big.HandleInput("\x1b[5~")
	eq(t, big.scrollOffset, 33, "page up scrolls one window")
}

func TestMutantsDTaskListOverlayToggleFailure(t *testing.T) {
	eq(t, overlayClearDelay, 2500*time.Millisecond, "failure message lifetime")
	o, _ := mdOverlay(overlayGoalPoolForToggle(), sp("g1"), taskListOverlayOptions{onToggleTask: func(string, string) overlayToggleResult {
		return overlayToggleResult{ok: false, message: "nope"}
	}})
	o.toggleTask("g1", "g1-task-0")
	time.Sleep(200 * time.Millisecond)
	o.mu.Lock()
	msg := o.cursorMessage
	o.mu.Unlock()
	eq(t, msg, "nope", "the failure message is still shown after 200ms")
}

func overlayGoalPoolForToggle() *goalPool {
	return poolOf(overlayGoalWithTasks("g1", "G", "T"))
}

// ── audit session (child pig) ──

func TestMutantsDSessionPure(t *testing.T) {
	eq(t, (&pigAuditorFactory{}).graceOrDefault(), 5*time.Second, "default grace")
	eq(t, (&pigAuditorFactory{grace: 300 * time.Millisecond}).graceOrDefault(), 300*time.Millisecond, "configured grace")

	// Each project-resource switch maps to its own flag.
	base := []string{"--mode", "json", "--no-session", "--no-mcp", "--tools", "read"}
	for _, c := range []struct {
		opts auditorResourceLoaderOptions
		flag string
	}{
		{auditorResourceLoaderOptions{noExtensions: true}, "--no-extensions"},
		{auditorResourceLoaderOptions{noSkills: true}, "--no-skills"},
		{auditorResourceLoaderOptions{noPromptTemplates: true}, "--no-prompt-templates"},
		{auditorResourceLoaderOptions{noThemes: true}, "--no-themes"},
		{auditorResourceLoaderOptions{noContextFiles: true}, "--no-context-files"},
	} {
		o := c.opts
		got := buildPigAuditorArgs(auditorSessionOptions{tools: []string{"read"}, resourceLoaderOptions: &o})
		eq(t, got, append(append([]string{}, base...), c.flag), c.flag)
	}

	// The session runs where the auditor was started.
	s, _ := (&pigAuditorFactory{}).createSession(auditorSessionOptions{cwd: "/some/dir"})
	eq(t, s.(*pigAuditorSession).cwd, "/some/dir", "session cwd")

	// The custom-tool prompt protocol.
	eq(t, pigCustomToolSuffix([]*goalOracleTool{{name: "t", description: "does it"}}),
		"\n\nThe function `t` is not callable in this environment (does it). To use it, end your final reply with exactly one block and nothing after it:\n<t>\n{JSON arguments}\n</t>\n", "suffix")
	eq(t, strings.Contains(pigCustomToolSuffix([]*goalOracleTool{{name: "t", parameters: jo(t, `{"type":"object"}`)}}),
		"The JSON arguments must match this JSON Schema:\n{\"type\":\"object\"}\n"), true, "suffix with a schema")
	eq(t, pigCustomToolSuffix(nil), "", "no tools, no suffix")

	// A submitted block may be wrapped in Unicode whitespace.
	var got []*jsObject
	tool := &goalOracleTool{name: "t", execute: func(_ string, p *jsObject) oracleToolResult { got = append(got, p); return oracleToolResult{} }}
	pigRunCustomToolBlocks([]*goalOracleTool{tool}, "<t>\u00a0{\"a\":1}\u00a0</t>")
	eq(t, len(got), 1, "block wrapped in no-break spaces")

	// The line writer drops an over-long line even when its newline arrives in the same chunk.
	var lines []string
	w := &pigLineWriter{onLine: func(l []byte) { lines = append(lines, string(l)) }, maxLine: 8}
	w.Write([]byte("0123456789\n"))
	eq(t, len(lines), 0, "9+ byte line dropped")
	w.Write([]byte("12345678\n"))
	eq(t, lines, []string{"12345678"}, "8 byte line kept")
}

func TestMutantsDEventMapper(t *testing.T) {
	update := func(m *pigEventMapper, body string) auditorSessionEvent {
		ev, ok, _ := m.mapLine([]byte(`{"type":"message_update","assistantMessageEvent":` + body + `}`))
		if !ok {
			t.Fatalf("not forwarded: %s", body)
		}
		return ev
	}
	texts := func(e auditorSessionEvent) []string {
		out := []string{}
		for _, p := range e.message.content {
			if p.text != nil {
				out = append(out, *p.text)
			} else {
				out = append(out, "<"+p.typ+">")
			}
		}
		return out
	}

	// text_start empties the block.
	m := &pigEventMapper{}
	update(m, `{"type":"text_start","contentIndex":0}`)
	update(m, `{"type":"text_delta","contentIndex":0,"delta":"ab"}`)
	eq(t, texts(update(m, `{"type":"text_start","contentIndex":0}`)), []string{""}, "text_start resets")

	// No contentIndex means block 0.
	m = &pigEventMapper{}
	eq(t, texts(update(m, `{"type":"text_delta","delta":"z"}`)), []string{"z"}, "default index")

	// Indexes up to 1023 are tracked, 1024 and beyond are not.
	m = &pigEventMapper{}
	eq(t, len(update(m, `{"type":"text_delta","contentIndex":1023,"delta":"z"}`).message.content), 1024, "index 1023")
	m = &pigEventMapper{}
	eq(t, len(update(m, `{"type":"text_delta","contentIndex":1024,"delta":"z"}`).message.content), 0, "index 1024")

	// message_end joins only the text parts, and an assistant message_end starts the next partial afresh.
	m = &pigEventMapper{}
	_, _, txt := m.mapLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","text":"zz"},{"type":"text","text":"a"}]}}`))
	eq(t, txt, "a", "only text parts")
	m = &pigEventMapper{}
	update(m, `{"type":"text_delta","contentIndex":0,"delta":"ab"}`)
	m.mapLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[]}}`))
	eq(t, texts(update(m, `{"type":"text_delta","contentIndex":0,"delta":"z"}`)), []string{"z"}, "partial reset by message_end")
}
