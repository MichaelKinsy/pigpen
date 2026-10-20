package pi_goal_x

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	pageDownKey = "\x1b[6~"
	arrowDown   = "\x1b[B"
	tabKey      = "\t"
	enterKey    = "\r"
	escKey      = "\x1b"
)

func stripSGR(l string) string { return ansiSGRRe.ReplaceAllString(l, "") }

func stripAll(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripSGR(l)
	}
	return strings.Join(out, "\n")
}

func contains(t *testing.T, s, sub, msg string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: %q not in %q", msg, sub, s)
	}
}

func notContains(t *testing.T, s, sub, msg string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%s: %q unexpectedly in %q", msg, sub, s)
	}
}

func anyLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func mustMatch(t *testing.T, re, s, msg string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Errorf("%s: %q does not match %s", msg, s, re)
	}
}

func eqDeep(t *testing.T, got, want any, msg string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %#v\nwant %#v", msg, got, want)
	}
}

func lastOf(l []string) string { return l[len(l)-1] }

func taskObj(id, title, status string, subs ...*jsObject) *jsObject {
	o := newObject()
	o.set("id", id)
	o.set("title", title)
	o.set("status", status)
	if len(subs) > 0 {
		arr := make([]any, len(subs))
		for i, s := range subs {
			arr[i] = s
		}
		o.set("subtasks", arr)
	}
	return o
}

func mustRun(t *testing.T, ctx goalDialogContext, qs []goalQuestionnaireQuestion, toggle *goalAuditorToggleInit) goalQuestionnaireResult {
	t.Helper()
	r, err := runGoalQuestionnaire(ctx, qs, toggle)
	if err != nil {
		t.Fatalf("runGoalQuestionnaire: %v", err)
	}
	return r
}

const reproQuestionText = "via uv too?"

var reproOptionLabels = []string{
	"Dev toolchain only (recommended): pyproject.toml with [tool.uv] package=false, [dependency-groups] dev (pytest, pytest-benchmark), pytest config moved in; committed uv.lock; `uv sync` + `uv run pytest benchmarks/`; requirements-dev.txt and pytest.ini removed; runtime/zipapp/battery stay stdlib-only and uv-free",
	"Also pin a dev Python via uv (.python-version, e.g. 3.12) while the runtime floor stays >=3.9",
	"Also manage the built artifact with uv (uv tool install of the zipapp) — note: the zipapp is self-contained, uv adds nothing there",
	"Write your own answer...",
}

func renderGoalQuestionDialog(t *testing.T, rows, baseFrame, width int) []string {
	c := newCapturedCtx(t)
	f := c.open(func(ctx goalDialogContext) {
		runGoalQuestionnaire(ctx, []goalQuestionnaireQuestion{{id: "question", question: reproQuestionText, options: reproOptionLabels, recommended: questionnaireInt(0)}}, nil)
	})
	comp, _ := boundedDialog(f, rows, baseFrame, plainTestTheme{})
	return comp.Render(width)
}

const proposalTopic = "Goal draft is not presenting tasks"

var proposalObjective = strings.Join([]string{
	"=== Goal ===",
	"Objective: Fix the pi-goal-x bug where the goal draft is not presenting tasks.",
	"Success criteria: the tasks section is visible in the confirmation dialog.",
	"Boundaries: in scope: extensions; out of scope: pi-tui API changes.",
	"Constraints: the dialog frame must never exceed the terminal height.",
	"Verification contract: npm run check (0 errors); npm test (0 failures).",
	"If blocked: stop and ask the user.",
}, "\n")

var proposalConfirmOptions = []string{"Confirm — create this goal now", "Continue chatting — keep refining", "Cancel — discard this draft"}

func proposalTasks() []*jsObject {
	return []*jsObject{
		taskObj("task-1", "Add the failing render-level regression test", "pending"),
		taskObj("task-2", "Add the failing flow-level regression test", "pending"),
	}
}

func buildProposalConfirmationContext(tasks []*jsObject, auditorEnabled bool) string {
	base := buildDraftConfirmationText("goal", proposalTopic, proposalObjective, true)
	tasksText := "\n\nTasks proposed for confirmation:\n" + strings.Join(renderConfirmationTasks(tasks, 0), "\n")
	auditorLine := "\n\nAuditor for this goal: disabled (completion skips the audit)."
	if auditorEnabled {
		auditorLine = "\n\nAuditor for this goal: enabled (independent approval required before completion)."
	}
	return base + tasksText + auditorLine
}

func openProposalDialogComponent(t *testing.T, rows, baseFrame int, theme dialogTheme) goalDialogComponent {
	c := newCapturedCtx(t)
	f := c.open(func(ctx goalDialogContext) {
		no := false
		runGoalQuestionnaire(ctx, []goalQuestionnaireQuestion{{
			id: "confirm", question: "Confirm Goal Draft", context: buildProposalConfirmationContext(proposalTasks(), true),
			options: proposalConfirmOptions, recommended: questionnaireInt(0), allowCustom: &no,
		}}, &goalAuditorToggleInit{true})
	})
	comp, _ := boundedDialog(f, rows, baseFrame, theme)
	return comp
}

var proposalUnitLines = []string{
	"─",
	" Confirm Goal Draft",
	"● Goal draft ready for confirmation.",
	" Objective: Fix the goal draft.",
	" Success criteria: tasks visible.",
	"",
	"Tasks proposed for confirmation:",
	"[ ] task-1: Add the failing render-level regression test",
	"[ ] task-2: Add the failing flow-level regression test",
	"[ ] task-3: Add the fitDialogLines unit tests",
	"",
	" Auditor for this goal: enabled.",
	"",
	" 1. Confirm — create this goal now",
	" 2. Continue chatting — keep refining",
	" 3. Cancel — discard this draft",
	"",
	" ↑↓ navigate • Enter select • Esc cancel",
	"─",
}

var proposalUnitSegments = &proposalPresentationSegments{6, 9, 13}

func fit3(lines []string, maxLines, head int) []string {
	return fitDialogLines(lines, maxLines, head, nil, nil, nil)
}

func TestGoalQuestionnaire(t *testing.T) {
	const f = "goal-questionnaire"

	tw(t, f, "normalizeQuestionnaireQuestions trims ids, de-duplicates, filters options, and validates recommended", func(t *testing.T) {
		no := false
		yes := true
		got := normalizeQuestionnaireQuestions([]goalQuestionnaireQuestion{
			{id: " scope ", question: "Scope?", options: []string{" A ", "", "B"}, recommended: questionnaireInt(1)},
			{id: "scope", question: "Again?", options: []string{"X"}, recommended: questionnaireInt(2), allowCustom: &no},
			{id: "  ", question: "Empty id?", options: []string{}, recommended: questionnaireInt(0)},
		})
		want := []goalQuestionnaireQuestion{
			{id: "scope", question: "Scope?", options: []string{" A ", "B"}, recommended: questionnaireInt(1), allowCustom: &yes},
			{id: "scope-2", question: "Again?", options: []string{"X"}, recommended: nil, allowCustom: &no},
			{id: "q3", question: "Empty id?", options: []string{}, recommended: nil, allowCustom: &yes},
		}
		eqDeep(t, got, want, "normalized")
	})

	tw(t, f, "dialog line limit supports pi 0.83 frames and pi 0.84 docked/fullscreen frames", func(t *testing.T) {
		chk := func(rows, base, want int, ok bool) {
			t.Helper()
			g, gok := computeDialogLineLimit(rows, base)
			if g != want || gok != ok {
				t.Errorf("computeDialogLineLimit(%d,%d) = %d,%v want %d,%v", rows, base, g, gok, want, ok)
			}
		}
		chk(46, 38, 10, true)
		chk(46, 20, 27, true)
		chk(46, 0, 42, true)
		chk(8, 0, 4, true)
		chk(8, 20, 8, true)
		chk(3, 0, 3, true)
		chk(0, 0, 0, false)
	})

	tw(t, f, "formatQuestionnaireAnswers emits stable Q/A records with context and options", func(t *testing.T) {
		yes := true
		result := goalQuestionnaireResult{
			cancelled: false,
			questions: []goalQuestionnaireQuestion{
				{id: "scope", question: "Scope?", context: "Pick one", options: []string{"A", "B"}, allowCustom: &yes},
				{id: "notes", question: "Notes?", options: []string{}, allowCustom: &yes},
			},
			answers: []goalQuestionnaireAnswer{
				{id: "scope", question: "Scope?", answer: "A", wasCustom: false},
				{id: "notes", question: "Notes?", answer: "Custom", wasCustom: true},
			},
		}
		eqDeep(t, formatQuestionnaireAnswers(result), "**Q:** Scope?\nPick one\nOptions: A / B\n**A:** A\n\n---\n\n**Q:** Notes?\n**A:** Custom", "formatted")
	})

	tw(t, f, "headless question sufficiency blocks vague-topic default fabrication", func(t *testing.T) {
		eqDeep(t, isHeadlessQuestionSufficientForDraft("整理笔记", "你的笔记目前存放在哪里，是什么格式？输出为什么形式？"), false, "vague")
		eqDeep(t, isHeadlessQuestionSufficientForDraft("在 sandbox 当前目录创建 hello.txt，内容为 Hello, Goal!，不要修改其他文件。", "如果 hello.txt 已存在，应该覆盖还是停止？"), true, "concrete")
	})

	tw(t, f, "regression: agent question stays readable when the goal panel leaves little room (rows=24, baseFrame=19)", func(t *testing.T) {
		lines := renderGoalQuestionDialog(t, 24, 19, 100)
		if len(lines) > 10 {
			t.Errorf("dialog stays within the terminal-height bound: %d", len(lines))
		}
		mustMatch(t, `^─+$`, lines[0], "top border must be visible")
		if !anyLine(lines, reproQuestionText) {
			t.Error("question text must be visible")
		}
		if !anyLine(lines, "Dev toolchain only") {
			t.Error("recommended first option must be visible")
		}
		last := lines[len(lines)-1]
		if !regexp.MustCompile(`^─+$`).MatchString(last) && !strings.Contains(last, "more · PgUp/PgDn scroll") {
			t.Errorf("bottom edge is the border or the scroll indicator when clipped: %q", last)
		}
	})

	tw(t, f, "regression: the goal confirmation dialog presents the tasks and the auditor toggle (rows=24, baseFrame=19)", func(t *testing.T) {
		lines := openProposalDialogComponent(t, 24, 19, plainTestTheme{}).Render(100)
		if len(lines) > 10 {
			t.Errorf("dialog stays within the terminal-height bound: %d", len(lines))
		}
		mustMatch(t, `^─+$`, lines[0], "top border must be visible")
		for _, c := range []struct{ sub, msg string }{
			{"Confirm Goal Draft", "question must be visible"},
			{"Tasks proposed for confirmation:", "tasks header must be visible"},
			{"[ ] task-1: Add the failing render-level regression test", "at least one task line must be visible"},
			{"press 'a' to toggle", "the auditor toggle line must be visible in the bounded dialog"},
			{"Confirm — create this goal now", "confirm option must be visible"},
			{"Enter select", "footer hint must be visible"},
		} {
			if !anyLine(lines, c.sub) {
				t.Errorf("%s (%q)", c.msg, c.sub)
			}
		}
		mustMatch(t, `^─+$`, lastOf(lines), "bottom border must be visible")
	})

	tw(t, f, "fitDialogLines keeps the protected head and never exceeds the bound", func(t *testing.T) {
		lines := []string{"─", "Q", "", "ctx1", "ctx2", "", "1. A", "2. B", "", "footer", "─"}
		eqDeep(t, fit3(lines, 20, 3), lines, "under the limit")
		eqDeep(t, fit3([]string{"─", "Q", "", "1. A", "footer", "─"}, 2, 2), []string{"─", "Q"}, "tiny bound")
	})

	tw(t, f, "fitDialogLines tail-keeps context-heavy dialogs so options/footer/border stay", func(t *testing.T) {
		lines := []string{"─", "Confirm", "", "ctx1", "ctx2", "", "1. A", "2. B", "", "footer", "─"}
		eqDeep(t, fit3(lines, 6, 3), []string{"─", "Confirm", "", "", "footer", "─"}, "bound 6")
		eqDeep(t, fit3(lines, 8, 3), []string{"─", "Confirm", "", "1. A", "2. B", "", "footer", "─"}, "bound 8")
	})

	tw(t, f, "fitDialogLines viewport: full content stays reachable, nothing truncated or dropped", func(t *testing.T) {
		lines := []string{"─", "Q", "", "1. first", "2. second", "3. third", "", "footer", "─"}
		scroll := &dialogScrollState{optionRanges: [][2]int{}}
		eqDeep(t, fitDialogLines(lines, 6, 3, nil, scroll, nil), []string{"─", "Q", "", "1. first", "2. second", "… +3 more · PgUp/PgDn scroll"}, "top")
		scroll.scrollTop = 99
		eqDeep(t, fitDialogLines(lines, 6, 3, nil, scroll, nil), []string{"▲ 3 more", "2. second", "3. third", "", "footer", "─"}, "clamped to the end")
		seen := map[string]bool{}
		for s := 0; s <= len(lines); s++ {
			for _, l := range fitDialogLines(lines, 6, 3, nil, &dialogScrollState{scrollTop: s}, nil) {
				if !strings.HasPrefix(l, "… +") && !strings.HasPrefix(l, "▲") {
					seen[l] = true
				}
			}
		}
		for _, l := range lines {
			if !seen[l] {
				t.Errorf("content line reachable: %q", l)
			}
		}
	})

	tw(t, f, "fitDialogLines viewport: selection auto-follow nudges the window into view", func(t *testing.T) {
		lines := []string{"─", "Q", "", "1. first", "2. second", "3. third", "", "footer", "─"}
		scroll := &dialogScrollState{needsFollow: true, optionRanges: [][2]int{{3, 3}, {4, 4}, {5, 5}}, followIndex: 2}
		out := fitDialogLines(lines, 4, 3, nil, scroll, nil)
		if scroll.scrollTop != 3 {
			t.Errorf("window nudged to reveal the selected option: %d", scroll.scrollTop)
		}
		if !anyLine(out, "3. third") {
			t.Error("selected option visible")
		}
		if scroll.needsFollow {
			t.Error("follow flag consumed")
		}
		scroll2 := &dialogScrollState{needsFollow: true, optionRanges: [][2]int{{1, 1}, {3, 3}, {5, 5}}, followIndex: 0}
		fitDialogLines(lines, 4, 3, nil, scroll2, nil)
		if scroll2.scrollTop != 0 {
			t.Errorf("no nudge when the selection is already visible: %d", scroll2.scrollTop)
		}
	})

	tw(t, f, "fitDialogLines viewport: ▲ indicator and never-exceeds-bound across the scroll range", func(t *testing.T) {
		lines := []string{"─", "Q", "", "1. first", "2. second", "3. third", "", "footer", "─"}
		out := fitDialogLines(lines, 5, 2, nil, &dialogScrollState{scrollTop: 4}, nil)
		if !strings.HasPrefix(out[0], "▲ 4 more") {
			t.Errorf("▲ indicator at the top when scrolled down: %q", out[0])
		}
		if len(out) > 5 {
			t.Error("exceeds 5")
		}
		for _, bound := range []int{1, 2, 3, 4, 5, 8} {
			for s := 0; s <= len(lines)+2; s++ {
				if n := len(fitDialogLines(lines, bound, min(2, bound), nil, &dialogScrollState{scrollTop: s}, nil)); n > bound {
					t.Errorf("bound %d at scroll %d: %d", bound, s, n)
				}
			}
		}
		if n := len(fit3([]string{"─", "Q", "", "ctx", "", "1. A", "", "footer", "─"}, 4, 2)); n > 4 {
			t.Errorf("tail-keep bound: %d", n)
		}
	})

	tw(t, f, "fitDialogLines proposal mode: content that fits renders byte-identical", func(t *testing.T) {
		eqDeep(t, fitDialogLines(proposalUnitLines, 30, 2, proposalUnitSegments, nil, nil), proposalUnitLines, "fits")
	})

	tw(t, f, "fitDialogLines proposal mode: head + tasks + options/footer/border kept within budget", func(t *testing.T) {
		eqDeep(t, fitDialogLines(proposalUnitLines, 12, 2, proposalUnitSegments, nil, nil), []string{
			"─", " Confirm Goal Draft", "Tasks proposed for confirmation:",
			"[ ] task-1: Add the failing render-level regression test",
			"[ ] task-2: Add the failing flow-level regression test",
			"[ ] task-3: Add the fitDialogLines unit tests",
			" 1. Confirm — create this goal now", " 2. Continue chatting — keep refining", " 3. Cancel — discard this draft",
			"", " ↑↓ navigate • Enter select • Esc cancel", "─",
		}, "budget 12")
	})

	tw(t, f, "fitDialogLines proposal mode: strips blank spacing, then drops task lines from the end", func(t *testing.T) {
		fitted := fitDialogLines(proposalUnitLines, 10, 2, proposalUnitSegments, nil, nil)
		eqDeep(t, fitted, []string{
			"─", " Confirm Goal Draft", "Tasks proposed for confirmation:",
			"[ ] task-1: Add the failing render-level regression test",
			"[ ] task-2: Add the failing flow-level regression test",
			" 1. Confirm — create this goal now", " 2. Continue chatting — keep refining", " 3. Cancel — discard this draft",
			" ↑↓ navigate • Enter select • Esc cancel", "─",
		}, "budget 10")
		notContains(t, strings.Join(fitted, "\n"), "task-3", "task-3 drops only after blank spacing is exhausted")
	})

	tw(t, f, "fitDialogLines proposal mode: never exceeds the bound even at degenerate budgets", func(t *testing.T) {
		tight := fitDialogLines(proposalUnitLines, 6, 2, proposalUnitSegments, nil, nil)
		if len(tight) > 6 {
			t.Error("tight exceeds")
		}
		mustMatch(t, `^─+$`, lastOf(tight), "bottom border must be visible")
		if !anyLine(tight, "Enter select") {
			t.Error("footer hint must be visible")
		}
		tiny := fitDialogLines(proposalUnitLines, 3, 2, proposalUnitSegments, nil, nil)
		if len(tiny) != 3 {
			t.Errorf("tiny length %d", len(tiny))
		}
		mustMatch(t, `^─+$`, lastOf(tiny), "bottom border must be visible")
		if n := len(fitDialogLines(proposalUnitLines, 1, 2, proposalUnitSegments, nil, nil)); n != 1 {
			t.Errorf("budget 1: %d", n)
		}
	})

	tw(t, f, "findProposalPresentationSegments locates the tasks section and options tail", func(t *testing.T) {
		eqDeep(t, findProposalPresentationSegments(proposalUnitLines, 13), proposalUnitSegments, "segments")
		if findProposalPresentationSegments([]string{"─", "Q", "", "1. A", "footer", "─"}, 3) != nil {
			t.Error("plain agent question is not a proposal confirmation")
		}
		if findProposalPresentationSegments(proposalUnitLines, -1) != nil {
			t.Error("degenerate tail index is rejected")
		}
	})

	tw(t, f, "proposal confirmation helpers keep headless and cancel semantics stable", func(t *testing.T) {
		eqDeep(t, shouldAutoConfirmProposal(false, ""), true, "no UI")
		eqDeep(t, shouldAutoConfirmProposal(true, "1"), true, "env 1")
		eqDeep(t, shouldAutoConfirmProposal(true, "0"), false, "env 0")
		eqDeep(t, proposalDecisionFromQuestionnaireResult(true, "Confirm — create this goal now"), "continue", "cancelled")
		eqDeep(t, proposalDecisionFromQuestionnaireResult(false, "Confirm — create this goal now"), "confirm", "confirm")
		eqDeep(t, proposalDecisionFromQuestionnaireResult(false, "Continue chatting — keep refining"), "continue", "continue")
		eqDeep(t, proposalDecisionFromQuestionnaireResult(false, "Cancel — discard this draft"), "cancel", "cancel")
		msg := proposalDialogFailureMessage(errString("boom"))
		mustMatch(t, `NOT created`, msg, "failure")
		mustMatch(t, `drafting remains active`, msg, "failure")
	})

	tw(t, f, "findProposalPresentationSegments is ANSI-aware: styled task lines extend the tasks section", func(t *testing.T) {
		lines := []string{
			ansiStyle("─"), ansiStyle(" Confirm Goal Draft"), ansiStyle("● Goal draft ready for confirmation."), ansiStyle(" Objective: Fix the dialog."), "",
			ansiStyle("Tasks proposed for confirmation:"),
			ansiStyle("[ ]") + " task-1: First", ansiStyle("[ ]") + " task-2: Second", "",
			ansiStyle(" ● Auditor enabled") + ansiStyle("  (press 'a' to toggle)"), "",
			ansiStyle(" 1. Confirm — create this goal now"), ansiStyle(" 2. Continue chatting — keep refining"), ansiStyle(" 3. Cancel — discard this draft"), "",
			ansiStyle(" ↑↓ navigate • Enter select • Esc cancel"), ansiStyle("─"),
		}
		segs := findProposalPresentationSegments(lines, 12)
		if segs == nil {
			t.Fatal("proposal segments found")
		}
		eqDeep(t, *segs, proposalPresentationSegments{5, 7, 9}, "ANSI segments")
	})

	tw(t, f, "findProposalPresentationSegments keeps the box-drawn TASKS section of a tweak confirmation complete", func(t *testing.T) {
		lines := []string{
			ansiStyle("─"), ansiStyle(" Confirm Goal Draft"), ansiStyle("● Goal tweak ready for confirmation."), "",
			ansiStyle("┌─ TASKS ─────────────────────────────────────┐"),
			ansiStyle("[ ]") + " audit: Audit the tweak path", ansiStyle("[ ]") + " fix: Fix it",
			ansiStyle("└──────────────────────────────────────────────┘"), "",
			ansiStyle(" ● Auditor enabled") + ansiStyle("  (press 'a' to toggle)"), "",
			ansiStyle(" 1. Confirm — create this goal now"), ansiStyle("─"),
		}
		segs := findProposalPresentationSegments(lines, 10)
		if segs == nil {
			t.Fatal("proposal segments found")
		}
		eqDeep(t, *segs, proposalPresentationSegments{4, 7, 9}, "box segments")
	})

	tw(t, f, "fitDialogLines proposal mode: the auditor toggle line is never sacrificed at tight budgets", func(t *testing.T) {
		lines := []string{
			"─", " Confirm Goal Draft", "Tasks proposed for confirmation:", "[ ] task-1: First", "[ ] task-2: Second", "",
			" ● Auditor enabled  (press 'a' to toggle)", "",
			" 1. Confirm — create this goal now", " 2. Continue chatting — keep refining", " 3. Cancel — discard this draft", "",
			" ↑↓ navigate • Enter select • Esc cancel", "─",
		}
		segs := findProposalPresentationSegments(lines, 8)
		if segs == nil {
			t.Fatal("proposal segments found")
		}
		fitted := fitDialogLines(lines, 10, 2, segs, nil, nil)
		if len(fitted) > 10 {
			t.Error("never exceeds the bound")
		}
		for _, sub := range []string{"press 'a' to toggle", "[ ] task-1: First", "Confirm — create this goal now"} {
			if !anyLine(fitted, sub) {
				t.Errorf("%q stays in frame", sub)
			}
		}
		mustMatch(t, `^─+$`, lastOf(fitted), "bottom border stays")
	})

	tw(t, f, "regression: the bounded proposal dialog keeps task lines and the auditor toggle with real-TUI ANSI styling", func(t *testing.T) {
		plain := openProposalDialogComponent(t, 24, 19, ansiTestTheme{}).Render(100)
		if len(plain) > 10 {
			t.Error("dialog stays within the bound")
		}
		s := stripAll(plain)
		contains(t, s, "[ ] task-1:", "a task line is visible under ANSI styling")
		contains(t, s, "press 'a' to toggle", "auditor toggle line is visible under ANSI styling")
	})

	tw(t, f, "pressing 'a' toggles the auditor status with visible feedback in the bounded proposal dialog", func(t *testing.T) {
		c := openProposalDialogComponent(t, 24, 19, ansiTestTheme{})
		before := strings.Join(c.Render(100), "\n")
		contains(t, before, "● Auditor enabled", "defaults to enabled")
		c.HandleInput("a")
		after := strings.Join(c.Render(100), "\n")
		contains(t, after, "○ Auditor disabled", "toggle feedback is visible after 'a'")
		notContains(t, after, "● Auditor enabled", "status flipped off")
	})

	tw(t, f, "bounded agent question: every option is in the initial frame; question stays, scroll indicator gone", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{
			id: "scope", question: "Which definition of parity should the release goal adopt?",
			options: []string{
				"Preserve the no-execute product definition — SFIToolkit.stata and .error become the ONLY documented incompatibilities (exact vendor signatures + deterministic typed refusal + prominent docs)",
				"Full vendor parity — adopt execution semantics for SFIToolkit.stata and SFIToolkit.error even though they execute / terminate the process, matching the vendor module in full",
			},
			recommended: questionnaireInt(0),
		}})
		top := c.render()
		if len(top) > 10 {
			t.Error("dialog stays within the terminal-height bound")
		}
		s := stripAll(top)
		contains(t, s, "Which definition of parity should the release goal adopt?", "question visible")
		contains(t, s, "1. Preserve the no-execute product definition", "option 1 in the initial frame")
		contains(t, s, "2. Full vendor parity", "option 2 in the initial frame")
		notContains(t, s, "PgUp/PgDn", "no scroll affordance advertised")
		notContains(t, s, "more ·", "no clipped-content indicator")
	})

	tw(t, f, "multi-question tabs: every option of each tab is in the initial frame", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{
			{id: "scope", question: "First question with a long option list.", options: []string{
				"Scope option A — with a very long label that wraps across several lines of the bounded dialog window",
				"Scope option B — another long wrapped label so the options exceed the dialog height and force scrolling",
				"Scope option C — still more wrapped text below the fold",
			}, recommended: questionnaireInt(0)},
			{id: "span", question: "Second question, also long.", options: []string{"Span option 1", "Span option 2"}, recommended: questionnaireInt(1)},
		})
		q1 := stripAll(c.render())
		for _, s := range []string{"Scope option A", "Scope option B", "Scope option C", "Write your own answer"} {
			contains(t, q1, s, "question 1 in the initial frame")
		}
		notContains(t, q1, "PgUp/PgDn", "no scroll affordance on question 1")
		c.comp.HandleInput(tabKey)
		q2 := stripAll(c.render())
		contains(t, q2, "Second question, also long.", "question 2 opens from the top")
		contains(t, q2, "Span option 1", "question 2 option 1 in the initial frame")
		contains(t, q2, "Span option 2", "question 2 option 2 in the initial frame")
		notContains(t, q2, "▲ ", "no stale scroll position on the new tab")
	})

	tw(t, f, "overflow: question/context yields before options; dialog never exceeds the bound", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{
			id: "q", question: "This question is deliberately long and wraps across several lines to simulate a verbose prompt.",
			context: "Context one.\nContext two.\nContext three.\nContext four.\nContext five.",
			options: []string{"Alpha", "Beta", "Gamma"},
		}})
		view := stripAll(c.render())
		if len(c.render()) > 10 {
			t.Error("dialog never exceeds the bound")
		}
		for _, opt := range []string{"1. Alpha", "2. Beta", "3. Gamma", "Write your own answer"} {
			contains(t, view, opt, "stays in frame on overflow")
		}
		contains(t, view, "This question is deliberately long", "question kept while context yields")
		notContains(t, view, "Context five", "context gives way first")
		notContains(t, view, "PgUp/PgDn", "no scroll affordance for realistic overflow")
	})

	tw(t, f, "pathological: an option block that alone exceeds the bound falls back to the scroll viewport", func(t *testing.T) {
		opts := make([]string, 12)
		for i := range opts {
			opts[i] = "Option number " + itoaT(i+1)
		}
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{id: "q", question: "Many options.", options: opts}})
		top := c.render()
		if len(top) > 10 {
			t.Error("bound preserved under the viewport fallback")
		}
		contains(t, stripAll(top), "PgUp/PgDn", "fallback advertises the scroll affordance")
		c.comp.HandleInput(pageDownKey)
		mustMatch(t, `Option number 1[0-2]`, stripAll(c.render()), "later options reachable via the fallback viewport")
	})

	tw(t, f, "selection stays visible with all options in frame: ↓ moves the marker, nothing scrolls", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{
			id: "q", question: "Pick one — every option is immediately viewable.",
			options:     []string{"Option one — short", "Option two — short", "Option three — short", "Option four — short", "Option five — short", "Option six — short"},
			recommended: questionnaireInt(0),
		}})
		initial := stripAll(c.render())
		for _, l := range []string{"1. Option one", "2. Option two", "3. Option three", "4. Option four", "5. Option five", "6. Option six", "Write your own answer"} {
			contains(t, initial, l, "in the initial frame")
		}
		notContains(t, initial, "PgUp/PgDn", "no scroll affordance")
		for i := 0; i < 4; i++ {
			c.comp.HandleInput(arrowDown)
		}
		view := stripAll(c.render())
		contains(t, view, "> 5. Option five", "selection marker on the fifth option")
		contains(t, view, "Option six", "later option still in frame")
		if len(c.render()) > 10 {
			t.Error("bound preserved")
		}
	})

	tw(t, f, "input mode keeps the editor visible when bounded (tail-keep unchanged)", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{
			id: "q", question: "Type your answer below the option hints.",
			options: []string{
				"Option one — with a long wrapped label that pushes the option hints far up the dialog",
				"Option two — with an equally long wrapped label for the hint block",
			},
		}})
		c.comp.HandleInput(arrowDown)
		c.comp.HandleInput(arrowDown)
		c.comp.HandleInput(enterKey)
		view := stripAll(c.render())
		contains(t, view, "Your answer:", "editor prompt visible")
		contains(t, view, "Enter to submit", "editor submit hint visible")
		if len(c.render()) > 10 {
			t.Error("input-mode dialog stays within the bound")
		}
	})

	tw(t, f, "input mode: Editor emits CURSOR_MARKER and hardware cursor is on while typing", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{{id: "q", question: "Pick or write your answer", options: []string{"Alpha", "Beta", "Gamma"}}})
		c.comp.SetFocused(true)
		for _, v := range c.tui.setCalls {
			if v {
				t.Error("hardware cursor off in select mode")
			}
		}
		for i := 0; i < 3; i++ {
			c.comp.HandleInput(arrowDown)
		}
		c.comp.HandleInput(enterKey)
		if calls := c.tui.setCalls; !calls[len(calls)-1] {
			t.Error("hardware cursor enabled on entering input mode")
		}
		markers := 0
		for _, l := range c.render() {
			if strings.Contains(l, widthx.CursorMarker) {
				markers++
			}
		}
		if markers != 1 {
			t.Errorf("Editor emits CURSOR_MARKER at the cursor position when focused: %d", markers)
		}
		for _, ch := range "hello" {
			c.comp.HandleInput(string(ch))
		}
		contains(t, stripAll(c.render()), "hello", "typed text lands in the editor")
		for _, ch := range "我的答案" {
			c.comp.HandleInput(string(ch))
		}
		contains(t, stripAll(c.render()), "我的答案", "composed text lands in the editor")
		c.comp.HandleInput(escKey)
		if calls := c.tui.setCalls; calls[len(calls)-1] {
			t.Error("hardware cursor off after leaving input mode")
		}
	})

	tw(t, f, "custom answer flows to the summary as (wrote) and Enter submits it", func(t *testing.T) {
		c := openQuestionnaire(t, 24, 19, []goalQuestionnaireQuestion{
			{id: "q1", question: "First question", options: []string{"A1", "A2"}},
			{id: "q2", question: "Second question", options: []string{"B1", "B2"}},
		})
		c.comp.SetFocused(true)
		c.comp.HandleInput(arrowDown)
		c.comp.HandleInput(arrowDown)
		c.comp.HandleInput(enterKey)
		for _, ch := range "my custom answer" {
			c.comp.HandleInput(string(ch))
		}
		c.comp.HandleInput(enterKey)
		c.comp.HandleInput(tabKey)
		view := stripAll(c.render())
		contains(t, view, "Ready to submit", "submit summary shown")
		contains(t, view, "(wrote) my custom answer", "custom answer recorded as (wrote) in the summary")
	})

	tw(t, f, "runGoalQuestionnaire reports an unavailable dialog instead of dereferencing undefined", func(t *testing.T) {
		result := mustRun(t, nonTerminalUiContext(), []goalQuestionnaireQuestion{{id: "q", question: "Scope?", options: []string{"A", "B"}}}, nil)
		eqDeep(t, result, goalQuestionnaireResult{questions: []goalQuestionnaireQuestion{}, answers: []goalQuestionnaireAnswer{}, cancelled: true, unavailable: true}, "unavailable result")
	})

	tw(t, f, "drafting degrades onto select/input where only ui.custom is missing", func(t *testing.T) {
		ctx, prompts := browserLikeUiContext([]string{"2. B"}, nil)
		result := mustRun(t, ctx, []goalQuestionnaireQuestion{{id: "scope", question: "Scope?", context: "Pick one", options: []string{"A", "B"}}}, nil)
		if result.cancelled || result.unavailable {
			t.Errorf("result %#v", result)
		}
		eqDeep(t, result.answers, []goalQuestionnaireAnswer{{id: "scope", question: "Scope?", answer: "B", wasCustom: false}}, "answers")
		mustMatch(t, `(?s)Scope\?.*Pick one`, (*prompts)[0], "context is part of the question")
	})

	tw(t, f, "the proposal dialog is answerable through plain select on such a host", func(t *testing.T) {
		ctx, prompts := browserLikeUiContext([]string{"Enabled — require independent approval", "1. Confirm — create this goal now (Recommended)"}, nil)
		got, err := showProposalDialog(ctx, "OBJECTIVE\n\nTasks:\n1. one", "goal", questionnaireBool(true))
		if err != nil {
			t.Fatal(err)
		}
		eqDeep(t, got, proposalDialogResult{"confirm", true, false}, "decision")
		mustMatch(t, `(?s)Confirm Goal Draft.*OBJECTIVE.*1\. one`, (*prompts)[1], "proposal prompt")
	})

	tw(t, f, "a free-text question falls back to input, and a dismissed dialog cancels", func(t *testing.T) {
		ctx, _ := browserLikeUiContext(nil, new("my own words"))
		typed := mustRun(t, ctx, []goalQuestionnaireQuestion{{id: "notes", question: "Notes?", options: []string{}}}, nil)
		eqDeep(t, typed.answers, []goalQuestionnaireAnswer{{id: "notes", question: "Notes?", answer: "my own words", wasCustom: true}}, "typed")
		ctx2, _ := browserLikeUiContext([]string{""}, nil) // "" = dismissed via undefined pick
		ctx2.u.selectFn = func(string, []string) (string, bool, error) { return "", false, nil }
		dismissed := mustRun(t, ctx2, []goalQuestionnaireQuestion{{id: "scope", question: "Scope?", options: []string{"A"}}}, nil)
		eqDeep(t, dismissed.cancelled, true, "cancelled")
		eqDeep(t, dismissed.answers, []goalQuestionnaireAnswer{}, "partial answers are not presented as finished")
	})

	tw(t, f, "showProposalDialog survives a host whose ui.custom resolves to undefined", func(t *testing.T) {
		got, err := showProposalDialog(nonTerminalUiContext(), "objective", "goal", questionnaireBool(true))
		if err != nil {
			t.Fatal(err)
		}
		eqDeep(t, got, proposalDialogResult{"continue", true, true}, "decision")
	})

	tw(t, f, "a dialog the host never rendered is not reported as a user decision", func(t *testing.T) {
		eqDeep(t, proposalDecisionFromQuestionnaireResult(true, ""), "continue", "cancelled by user")
		result := mustRun(t, nonTerminalUiContext(), []goalQuestionnaireQuestion{{id: "q", question: "Scope?", options: []string{}}}, nil)
		eqDeep(t, result.unavailable, true, "unavailable")
		mustMatch(t, `PI_GOAL_AUTO_CONFIRM=1`, dialogUnavailableHint, "hint")
	})
}

type errString string

func (e errString) Error() string { return string(e) }

func itoaT(i int) string { return strconv.Itoa(i) }

func nonTerminalUiContext() *mockDialogCtx {
	u := &mockDialogUI{customFn: func(goalCustomFactory, goalCustomOptions) (any, error) { return nil, nil }}
	return &mockDialogCtx{uiFlag: true, u: u}
}

// browserLikeUiContext: a host with select/input but whose custom resolves to undefined. select pops picks (default: first option).
func browserLikeUiContext(picks []string, typed *string) (*mockDialogCtx, *[]string) {
	prompts := []string{}
	u := &mockDialogUI{customFn: func(goalCustomFactory, goalCustomOptions) (any, error) { return nil, nil }}
	u.selectFn = func(title string, options []string) (string, bool, error) {
		prompts = append(prompts, title)
		if len(picks) > 0 {
			p := picks[0]
			picks = picks[1:]
			return p, true, nil
		}
		return options[0], true, nil
	}
	u.inputFn = func(title, _ string) (string, bool, error) {
		prompts = append(prompts, title)
		if typed == nil {
			return "", false, nil
		}
		return *typed, true, nil
	}
	return &mockDialogCtx{uiFlag: true, u: u}, &prompts
}

// openedQuestionnaire is openQuestionnaireComponent of the original test.
type openedQuestionnaire struct {
	comp goalDialogComponent
	tui  *mockDialogTUI
}

func (o *openedQuestionnaire) render() []string { return o.comp.Render(100) }

func openQuestionnaire(t *testing.T, rows, baseFrame int, qs []goalQuestionnaireQuestion) *openedQuestionnaire {
	c := newCapturedCtx(t)
	f := c.open(func(ctx goalDialogContext) { runGoalQuestionnaire(ctx, qs, nil) })
	comp, m := boundedDialog(f, rows, baseFrame, ansiTestTheme{})
	return &openedQuestionnaire{comp, m}
}
