package pi_goal_x

import (
	"regexp"
	"strings"
	"testing"
)

// Twins of tests/goal-dashboard-golden.test.ts (plan §19.4): compact and expanded dashboards at 40..140 columns.

var goldenWidths = []int{40, 50, 60, 80, 100, 140}

func goldenTask(id, title string, kv ...any) *jsObject { return wgTask(id, title, "pending", kv...) }

func goldenRecord(kv ...any) *jsObject {
	g := wgObj(
		"id", "g1",
		"objective", "Add CSV export to reports",
		"status", "active",
		"autoContinue", true,
		"usage", wgObj("tokensUsed", 18200.0, "activeSeconds", 767.0),
		"sisyphus", false,
		"createdAt", "2026-01-01T00:00:00.000Z",
		"updatedAt", "2026-01-01T00:00:00.000Z",
		"activePath", ".pi/goals/active_goal_g1.md",
	)
	return with(g, kv...)
}

func goldenWithTasks(tasks []*jsObject, kv ...any) *jsObject {
	list := make([]any, len(tasks))
	for i, x := range tasks {
		list[i] = x
	}
	return goldenRecord(append(append([]any{}, kv...), "taskList", wgTaskList(list...))...)
}

func goldenFiveTaskTree() []*jsObject {
	return []*jsObject{
		goldenTask("t1", "Review reports page and data source", "status", "complete", "evidence", "Reviewed source"),
		goldenTask("t2", "Implement filtered CSV export", "status", "complete"),
		goldenTask("t3", "Add the download button",
			"verificationContract", "The button downloads a CSV using the active filters.",
			"subtasks", []any{
				goldenTask("t3.1", "Add loading state", "status", "complete", "evidence", "Loading state added"),
				goldenTask("t3.2", "Generate timestamped filename", "status", "complete"),
				goldenTask("t3.3", "Add error handling"),
			}),
		goldenTask("t4", "Add documentation"),
		goldenTask("t5", "Add and run tests", "status", "skipped", "skipReason", "Covered by t2"),
	}
}

// goldenManyTaskTree: 30 top-level tasks; t5 and t20 completed (t20 latest).
func goldenManyTaskTree() []*jsObject {
	tasks := make([]*jsObject, 30)
	for i := range tasks {
		tasks[i] = goldenTask("t"+itoa(i+1), "Task number "+itoa(i+1))
	}
	tasks[4] = with(tasks[4], "status", "complete", "completedAt", "2026-01-01T10:00:00.000Z")
	tasks[19] = with(tasks[19], "status", "complete", "completedAt", "2026-01-01T11:00:00.000Z")
	return tasks
}

type goldenModelOpts struct {
	focused        *bool
	otherOpenGoals *int
	ledgerEvents   []*jsObject
}

func goldenModelFor(goal *jsObject, o goldenModelOpts) *goalDashboardModel {
	focused := true
	if o.focused != nil {
		focused = *o.focused
	}
	other := 2
	if o.otherOpenGoals != nil {
		other = *o.otherOpenGoals
	}
	return deriveGoalDashboardModel(goal, goalDashboardModelOptions{focused: focused, otherOpenGoals: other, ledgerEvents: o.ledgerEvents})
}

func goldenLedgerEventsFor(goalID string) []*jsObject {
	return []*jsObject{
		wgObj("type", "goal_created", "goalId", goalID, "objective", "Add CSV export to reports", "sisyphus", false, "autoContinue", true, "at", "2026-01-01T09:00:00.000Z"),
		wgObj("type", "task_complete", "goalId", goalID, "taskId", "t2", "evidence", "Done", "at", "2026-01-01T09:05:00.000Z"),
		wgObj("type", "task_started", "goalId", goalID, "taskId", "t3.3", "at", "2026-01-01T09:06:00.000Z"),
	}
}

func goldenAuditorProgress() *auditorWidgetProgress {
	return &auditorWidgetProgress{phase: "tool_executing", elapsedMs: 138000, currentTool: "read", currentToolArgs: `{"path":"src/parser.ts"}`,
		recentOutput: []string{"checking file exists...", "confirming test coverage..."}, percentage: fp(72), label: "Workspace inspection"}
}

func assertWidthSafe(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, l := range lines {
		if visibleWidth(l) > width {
			t.Errorf("line %d at width %d has visible width %d: %q", i, width, visibleWidth(l), l)
		}
	}
}

func goldenNotNil(t *testing.T, m *goalDashboardModel) *goalDashboardModel {
	t.Helper()
	if m == nil {
		t.Fatal("model is null")
	}
	return m
}

func TestGoalDashboardGolden(t *testing.T) {
	f := "goal-dashboard-golden"
	theme := wgTestTheme
	compact := func(m *goalDashboardModel, th goalTheme, w int, o goalCompactOptions) string {
		return strings.Join(renderCompactDashboard(m, th, w, o), "\n")
	}
	expanded := func(m *goalDashboardModel, th goalTheme, w int, o goalExpandedOptions) string {
		return strings.Join(renderExpandedDashboard(m, th, w, o), "\n")
	}

	tw(t, f, "width safety at ${width} cols: compact and expanded over all dashboard states", func(t *testing.T) {
		for _, width := range goldenWidths {
			completeTasks := []*jsObject{}
			for _, tk := range goldenFiveTaskTree() {
				c := with(tk, "status", "complete")
				if subs, ok := tk.vals["subtasks"].([]any); ok {
					ns := []any{}
					for _, s := range subs {
						ns = append(ns, with(s.(*jsObject), "status", "complete", "skipReason", undef{}))
					}
					c.set("subtasks", ns)
				} else {
					c.set("subtasks", undef{})
				}
				completeTasks = append(completeTasks, c)
			}
			states := []*jsObject{
				goldenRecord(),
				goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3", "verificationContract", "Run npm test with zero failures."),
				goldenRecord("status", "paused", "stopReason", "user", "pauseReason", "User is away", "pauseSuggestedAction", "Run /goal-resume when back."),
				goldenRecord("status", "blocked", "pauseReason", "Missing API key", "pauseSuggestedAction", "Set the key and run /goal-resume."),
				goldenRecord("status", "budget_limited", "tokenBudget", 20000.0),
				goldenWithTasks(completeTasks, "status", "complete", "activePath", undef{}, "archivedPath", ".pi/goals/archived/goal_g1.md"),
				goldenRecord("objective", strings.Repeat("x", 600)),
				goldenRecord("activePath", ".pi/goals/"+strings.Repeat("a", 200)+".md"),
				goldenRecord("verificationContract", strings.Repeat("Run ", 60)),
				goldenWithTasks([]*jsObject{
					goldenTask("u1", "中文任务：处理報告数据", "status", "complete"),
					goldenTask("u2", "日本語のタスクと📊チャート"),
					goldenTask("u3", "Combiné avec des caractères accentués"),
				}, "currentTaskId", "u2"),
			}
			for _, goal := range states {
				model := goldenModelFor(goal, goldenModelOpts{ledgerEvents: goldenLedgerEventsFor(gstr(goal, "id"))})
				if model == nil {
					continue
				}
				assertWidthSafe(t, renderCompactDashboard(model, theme, width, goalCompactOptions{}), width)
				assertWidthSafe(t, renderExpandedDashboard(model, theme, width, goalExpandedOptions{}), width)
			}
			scrolled := goldenWithTasks(goldenManyTaskTree(), "currentTaskId", "t21")
			if sm := goldenModelFor(scrolled, goldenModelOpts{}); sm != nil {
				assertWidthSafe(t, renderCompactDashboard(sm, theme, width, goalCompactOptions{}), width)
				assertWidthSafe(t, renderExpandedDashboard(sm, theme, width, goalExpandedOptions{rows: intPtr(10)}), width)
				for _, offset := range []int{0, 3, 7, 99} {
					assertWidthSafe(t, renderCompactDashboard(sm, theme, width, goalCompactOptions{scrollOffset: intPtr(offset)}), width)
					assertWidthSafe(t, renderExpandedDashboard(sm, theme, width, goalExpandedOptions{rows: intPtr(10), scrollOffset: intPtr(offset)}), width)
				}
			}
		}
	})
	tw(t, f, "unfocused panel is width-safe at every width", func(t *testing.T) {
		for _, width := range goldenWidths {
			assertWidthSafe(t, renderUnfocusedDashboard(3, theme, width), width)
		}
	})
	tw(t, f, "palette: neutral-gray chrome, colour-coded content (§5)", func(t *testing.T) {
		captured := [][2]string{}
		colorTheme := wgTheme{seen: &captured}
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3"), goldenModelOpts{}))
		renderCompactDashboard(model, colorTheme, 100, goalCompactOptions{})
		renderExpandedDashboard(model, colorTheme, 100, goalExpandedOptions{})
		byColor := func(c string) []string {
			out := []string{}
			for _, kv := range captured {
				if kv[0] == c {
					out = append(out, kv[1])
				}
			}
			return out
		}
		some := func(list []string, pred func(string) bool) bool {
			for _, v := range list {
				if pred(v) {
					return true
				}
			}
			return false
		}
		reTest := func(re string) func(string) bool {
			r := regexp.MustCompile(re)
			return r.MatchString
		}
		hasStr := func(list []string, s string) bool { return contains(list, s) }
		muted := byColor("muted")
		check := func(ok bool, msg string) {
			t.Helper()
			if !ok {
				t.Error(msg)
			}
		}
		check(len(byColor("mdLink")) == 0, "no blue-tinged mdLink anywhere — chrome is neutral gray")
		check(len(byColor("borderMuted")) == 0, "the dark borderMuted token is not used")
		check(some(muted, reTest(`^─+$`)), "top/bottom frame dashes are neutral gray")
		check(hasStr(muted, "│"), "vertical edges are neutral gray")
		for _, glyph := range []string{"╭", "╮", "├", "┤", "╰", "╯"} {
			check(hasStr(muted, glyph), glyph+" corner is neutral gray")
		}
		check(some(muted, func(v string) bool { return strings.HasPrefix(v, "─ Tasks") }), "section-rule label is part of the frame tone")
		check(hasStr(muted, "─"), "header leading dash is frame-colored (LHS)")
		check(some(muted, func(v string) bool { return strings.Contains(v, "Ctrl+Shift+T: expand tasks") }), "footer hint is part of the frame tone")
		check(some(muted, func(v string) bool { return strings.Contains(v, "12m47s") }), "header usage meta is part of the frame tone")
		check(some(muted, func(v string) bool { return strings.Contains(v, "Add CSV export to reports") }), "header title is part of the frame tone")
		check(hasStr(byColor("mdHeading"), "·"), "pending marker is pastel amber")
		check(hasStr(byColor("success"), "✓"), "complete marker is muted green")
		check(hasStr(muted, "~"), "skipped marker is gray")
		check(some(byColor("success"), func(v string) bool { return strings.Contains(v, "t1") || strings.Contains(v, "t2") }), "completed ids are muted green")
		check(some(byColor("mdHeading"), func(v string) bool { return strings.Contains(v, "Review reports page") }), "task titles are pastel amber")
		check(some(byColor("accent"), func(v string) bool { return strings.Contains(v, "t3") }), "current task id stays accent (teal)")
		check(some(byColor("accent"), func(v string) bool { return strings.Contains(v, "Add the download button") }), "current task title is accent too")
		check(some(byColor("accent"), func(v string) bool { return strings.Contains(v, "pi-goal-x") }), "brand is accent")
		check(some(muted, reTest(`^█+$`)), "progress bar fill is neutral gray")
		check(some(byColor("dim"), reTest(`^░+$`)), "progress bar empty cells are dim")
		check(some(muted, reTest(`✓\d+ done · \d+ open`)), "task counts live in the muted header row")
		check(some(muted, reTest(`^\d+/\d+$`)), "subtask fraction is neutral gray")
		check(some(muted, reTest(` ▸ \d+/\d+$`)) && some(muted, reTest(`^ ▸ \d+/\d+$`)), "per-row subtask marker is neutral gray")
		check(!some(byColor("accent"), reTest(`^\d+/\d+`)), "no accent progress fractions remain")
		check(len(byColor("warning")) == 0, "the bright warning color is not used in the dashboard")
	})
	tw(t, f, "audit-running widget is width-safe at every width", func(t *testing.T) {
		for _, width := range goldenWidths {
			assertWidthSafe(t, renderAuditorWidgetLines(goldenAuditorProgress(), theme, width, false, ""), width)
		}
	})

	// ── Compact layout goldens ──

	tw(t, f, "compact: top-level task list is shown by default with '+N more' overflow", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3"), goldenModelOpts{}))
		medium := compact(model, theme, 80, goalCompactOptions{})
		reMatch(t, `├─ Tasks `, medium)
		reMatch(t, `✓ t1  Review reports page and data source`, medium)
		reMatch(t, `… \+1 more task`, medium)
		wide := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `~ t5  Add and run tests`, wide)
		reNoMatch(t, `\+[0-9]+ more task`, wide)
		minimal := compact(model, theme, 40, goalCompactOptions{})
		reMatch(t, `\+3 more tasks`, minimal)
		reMatch(t, `✓ t1  `, minimal)
		emptyModel := goldenNotNil(t, goldenModelFor(goldenRecord(), goldenModelOpts{}))
		if strings.Contains(compact(emptyModel, theme, 100, goalCompactOptions{}), "├─ Tasks") {
			t.Error("no tasks → no task section at all")
		}
	})
	tw(t, f, "compact: the auditor indicator is a bottom-right border dot, green on / gray off (§auditor-toggle)", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3"), goldenModelOpts{}))
		wide := compact(model, theme, 100, goalCompactOptions{})
		reNoMatch(t, `Auditor  on`, wide)
		reNoMatch(t, `│ Auditor`, wide)
		reMatch(t, `toggle auditor ● ─╯`, wide)
		reMatch(t, `Ctrl\+Shift\+A: toggle auditor`, wide)
		reMatch(t, `Ctrl\+Shift\+A: toggle auditor ●`, wide)
		reMatch(t, `expand tasks─+ Ctrl\+Shift\+A: toggle auditor`, wide)
		reNoMatch(t, `· Ctrl\+Shift\+A`, wide)
		reMatch(t, `Ctrl\+Shift\+A: toggle auditor ●`, compact(model, theme, 80, goalCompactOptions{}))
		for _, width := range []int{60, 40} {
			out := compact(model, theme, width, goalCompactOptions{})
			reNoMatch(t, `Ctrl\+Shift\+A`, out)
			reNoMatch(t, `toggle auditor`, out)
			reMatch(t, `─ ● ─╯`, out)
		}
		seen := [][2]string{}
		tracking := wgTheme{seen: &seen}
		has := func(color, v string) bool {
			for _, kv := range seen {
				if kv[0] == color && kv[1] == v {
					return true
				}
			}
			return false
		}
		renderCompactDashboard(model, tracking, 100, goalCompactOptions{})
		if !has("success", "●") {
			t.Error("auditor on renders a green dot")
		}
		offModel := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3", "skipAuditor", true), goldenModelOpts{}))
		seen = seen[:0]
		renderCompactDashboard(offModel, tracking, 100, goalCompactOptions{})
		if !has("muted", "●") {
			t.Error("auditor off renders a muted gray dot")
		}
		if has("success", "●") {
			t.Error("off state never renders the green dot")
		}
		reNoMatch(t, `Auditor`, expanded(model, theme, 100, goalExpandedOptions{}))
		for _, width := range goldenWidths {
			assertWidthSafe(t, renderCompactDashboard(model, theme, width, goalCompactOptions{}), width)
			assertWidthSafe(t, renderCompactDashboard(offModel, theme, width, goalCompactOptions{}), width)
		}
	})
	tw(t, f, "compact: the default viewport anchors to the most recently completed tasks (§9.6)", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenManyTaskTree(), "currentTaskId", "t21"), goldenModelOpts{}))
		wide := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `↑ 15 more tasks`, wide)
		reMatch(t, `Task number 20`, wide)
		reNoMatch(t, `[✓▸·~] t1\s`, wide)
		reMatch(t, `… \+10 more tasks`, wide)
		minimal := compact(model, theme, 40, goalCompactOptions{})
		reMatch(t, `↑ 18 more tasks`, minimal)
		reMatch(t, `Task number 20`, minimal)
	})
	tw(t, f, "compact: an explicit scrollOffset windows the list and clamps at both ends", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenManyTaskTree(), "currentTaskId", "t21"), goldenModelOpts{}))
		top := compact(model, theme, 100, goalCompactOptions{scrollOffset: intPtr(0)})
		reNoMatch(t, `↑ \d+ more tasks`, top)
		reMatch(t, `Task number 1`, top)
		reMatch(t, `… \+25 more tasks`, top)
		tail := compact(model, theme, 100, goalCompactOptions{scrollOffset: intPtr(99)})
		reMatch(t, `↑ 25 more tasks`, tail)
		reNoMatch(t, `… \+\d+ more tasks`, tail)
	})
	tw(t, f, "expanded: rows + scrollOffset window the tree with indicators; full tree by default", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenManyTaskTree()), goldenModelOpts{}))
		mid := expanded(model, theme, 100, goalExpandedOptions{rows: intPtr(10), scrollOffset: intPtr(5)})
		reMatch(t, `↑ 5 more tasks`, mid)
		reMatch(t, `Task number 6`, mid)
		reMatch(t, `… \+15 more tasks`, mid)
		full := expanded(model, theme, 100, goalExpandedOptions{})
		reNoMatch(t, `↑ \d+ more tasks`, full)
		reMatch(t, `Task number 30`, full)
	})
	tw(t, f, "compact: running with tasks at 100 shows status, header counts + bar, current, contract, file", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3", "verificationContract", "Run npm test with zero failures."), goldenModelOpts{}))
		lines := renderCompactDashboard(model, theme, 100, goalCompactOptions{})
		text := strings.Join(lines, "\n")
		reMatch(t, `^╭─ pi-goal-x ─ Add CSV export to reports`, lines[0])
		reNoMatch(t, `12m47s`, lines[0])
		reMatch(t, `goal: running \[12m47s 18\.2K\] \(\+2 open\)`, lines[1])
		reMatch(t, `├─ Tasks · ✓3 done · 2 open ─+ \[.*\] · Sub 2/3 \[.*\] ┤`, text)
		reNoMatch(t, `Tasks  \[.*\] \d/\d · \d+%`, text)
		reNoMatch(t, `Subtasks \[.*\] 2/3 · 67%`, text)
		reMatch(t, `✓ t1  Review reports page and data source`, text)
		reMatch(t, `▸ t3  Add the download button ☑ ▸ 2/3`, text)
		reMatch(t, `· t4  Add documentation`, text)
		reMatch(t, `~ t5  Add and run tests`, text)
		reNoMatch(t, `· t4  Add documentation ▸`, text)
		reNoMatch(t, `\+[0-9]+ more task`, text)
		reMatch(t, `Current  t3 · Add the download button`, text)
		reMatch(t, `· Sub 2/3 \[.*\]`, text)
		reMatch(t, `Verify   Run npm test with zero failures`, text)
		reMatch(t, `File     \.pi/goals/active_goal_g1\.md`, text)
		reMatch(t, `Ctrl\+Shift\+T: expand tasks`, lastLine(lines))
	})
	tw(t, f, "compact: minimal mode at 40 keeps the essential summary", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3", "verificationContract", "Run npm test with zero failures."), goldenModelOpts{}))
		text := compact(model, theme, 40, goalCompactOptions{})
		reMatch(t, `goal: running`, text)
		reMatch(t, `Tasks · ✓3 done · 2 open`, text)
		reMatch(t, `▸ Add the download button`, text)
		reNoMatch(t, `Other goals`, text)
		reNoMatch(t, `File`, text)
	})
	tw(t, f, "compact: blocked state surfaces blocker and suggested action", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenRecord("status", "blocked", "pauseReason", "Build fails", "pauseSuggestedAction", "Run npm test"), goldenModelOpts{}))
		text := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `goal: blocked`, text)
		reMatch(t, `Blocker  Build fails`, text)
		reMatch(t, `Action   Run npm test`, text)
	})
	tw(t, f, "compact: paused state shows who paused and the reason", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenRecord("status", "paused", "stopReason", "agent", "pauseReason", "Waiting for input"), goldenModelOpts{}))
		text := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `goal: paused \(agent\)`, text)
		reMatch(t, `Reason   Waiting for input`, text)
	})
	tw(t, f, "compact: budget-limited state shows the budget summary", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenRecord("status", "budget_limited", "tokenBudget", 20000.0), goldenModelOpts{}))
		text := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `⛽ Budget 18\.2K / 20K · 91%`, text)
		reMatch(t, `goal: budget limited`, text)
	})
	tw(t, f, "compact: complete state shows the §4.7 message", func(t *testing.T) {
		tasks := []*jsObject{}
		for _, tk := range goldenFiveTaskTree() {
			c := with(tk, "status", "complete")
			if subs, ok := tk.vals["subtasks"].([]any); ok {
				ns := []any{}
				for _, s := range subs {
					ns = append(ns, with(s.(*jsObject), "status", "complete"))
				}
				c.set("subtasks", ns)
			} else {
				c.set("subtasks", undef{})
			}
			tasks = append(tasks, c)
		}
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(tasks, "status", "complete", "activePath", undef{}, "archivedPath", ".pi/goals/archived/goal_g1.md"), goldenModelOpts{}))
		text := compact(model, theme, 100, goalCompactOptions{})
		reMatch(t, `All required work is complete`, text)
		reMatch(t, `✓5 done · 0 open`, text)
	})

	// ── Expanded layout goldens ──

	tw(t, f, "expanded: full dashboard at 100 shows sections and complete task tree", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3", "verificationContract", "Run npm test with zero failures."), goldenModelOpts{ledgerEvents: goldenLedgerEventsFor("g1")}))
		lines := renderExpandedDashboard(model, theme, 100, goalExpandedOptions{})
		text := strings.Join(lines, "\n")
		reMatch(t, `goal: running \[12m47s 18\.2K\] \(\+2 open\)`, lines[1])
		reMatch(t, `├─ Progress `, text)
		reMatch(t, `3/5 tasks · 60%`, text)
		reMatch(t, `├─ Tasks `, text)
		reMatch(t, `✓ t1  Review reports page and data source`, text)
		reMatch(t, `▸ t3  Add the download button`, text)
		reMatch(t, `✓ t3\.1  Add loading state`, text)
		reMatch(t, `· t4  Add documentation`, text)
		reMatch(t, `├─ Current task `, text)
		reMatch(t, `Subtasks \[.*\] 2/3 · 67%`, text)
		reMatch(t, `Contract: The button downloads a CSV using the active filters`, text)
		reMatch(t, `├─ Verification `, text)
		reMatch(t, `Run npm test with zero failures`, text)
		reMatch(t, `├─ Recent activity `, text)
		reMatch(t, `Started “Add error handling”`, text)
		reMatch(t, `Esc/Ctrl\+Shift\+T: collapse`, lastLine(lines))
	})
	tw(t, f, "expanded: inferred current task is labelled", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree()), goldenModelOpts{}))
		reMatch(t, `Inferred from the first pending task`, expanded(model, theme, 100, goalExpandedOptions{}))
	})
	tw(t, f, "expanded: current nested subtask shows its depth in the tree", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3.3"), goldenModelOpts{}))
		text := expanded(model, theme, 100, goalExpandedOptions{})
		reMatch(t, `▸ t3\.3  Add error handling`, text)
		reMatch(t, `t3\.3 · Add error handling`, text)
	})
	tw(t, f, "expanded: no task sections when tasks are disabled (§9.5)", func(t *testing.T) {
		goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "verificationContract", "Run the suite."), goldenModelOpts{}))
		disabled := goldenNotNil(t, deriveGoalDashboardModel(goldenWithTasks(goldenFiveTaskTree(), "verificationContract", "Run the suite."), goalDashboardModelOptions{
			focused: true, otherOpenGoals: 1, tasksDisabled: true,
		}))
		text := expanded(disabled, theme, 100, goalExpandedOptions{})
		reNoMatch(t, `Tasks`, text)
		reNoMatch(t, `Current task`, text)
		reMatch(t, `Verification`, text)
	})
	tw(t, f, "compact: custom dashboard keybindings render in the footer hints", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3"), goldenModelOpts{}))
		custom := &goalDashboardKeybindings{toggleExpand: "ctrl+alt+t", scrollUp: "ctrl+alt+u", scrollDown: "ctrl+alt+d"}
		wide := compact(model, theme, 100, goalCompactOptions{keybindings: custom})
		reMatch(t, `Ctrl\+Alt\+T: expand tasks`, wide)
		reNoMatch(t, `Ctrl\+Shift\+T`, wide)
		sixTasks := append(goldenFiveTaskTree(), goldenTask("t6", "Extra row for overflow"))
		six := goldenNotNil(t, goldenModelFor(goldenWithTasks(sixTasks, "currentTaskId", "t3"), goldenModelOpts{}))
		wideOverflow := compact(six, theme, 100, goalCompactOptions{keybindings: custom})
		reMatch(t, `Ctrl\+Alt\+T: expand · Ctrl\+Alt\+U/Ctrl\+Alt\+D: scroll`, wideOverflow)
		reMatch(t, `\+1 more task`, wideOverflow)
		defaults := compact(six, theme, 100, goalCompactOptions{})
		reMatch(t, `Ctrl\+Shift\+T: expand · Ctrl\+Shift\+↑↓: scroll`, defaults)
		longKeys := &goalDashboardKeybindings{toggleExpand: "ctrl+alt+shift+super+t", scrollUp: "ctrl+alt+pageUp", scrollDown: "ctrl+alt+pageDown"}
		for _, line := range renderCompactDashboard(model, theme, 80, goalCompactOptions{keybindings: longKeys}) {
			if visibleWidth(line) > 80 {
				t.Errorf("custom long-key footer stays within width: %q", line)
			}
		}
	})
	tw(t, f, "expanded: the collapse footer shows the configured toggle key", func(t *testing.T) {
		model := goldenNotNil(t, goldenModelFor(goldenWithTasks(goldenFiveTaskTree(), "currentTaskId", "t3"), goldenModelOpts{}))
		custom := &goalDashboardKeybindings{toggleExpand: "ctrl+alt+t", scrollUp: "ctrl+alt+up", scrollDown: "ctrl+alt+down"}
		reMatch(t, `Esc/Ctrl\+Alt\+T: collapse`, expanded(model, theme, 100, goalExpandedOptions{keybindings: custom}))
		reMatch(t, `Esc/Ctrl\+Shift\+T: collapse`, expanded(model, theme, 100, goalExpandedOptions{}))
	})
}
