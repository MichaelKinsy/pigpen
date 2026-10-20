package pi_goal_x

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of tests/goal-widget.test.ts.

var wgTestTheme goalTheme = wgTheme{}

func wgGoal(kv ...any) *jsObject {
	g := wgObj(
		"id", "test-goal-001",
		"createdAt", "2026-01-01T00:00:00.000Z",
		"updatedAt", "2026-01-01T00:00:00.000Z",
		"objective", "=== Goal ===\nObjective: Componentize the goal widget\nSuccess criteria: tests pass",
		"status", "active",
		"autoContinue", true,
		"usage", wgObj("activeSeconds", 65.0, "tokensUsed", 2500.0),
		"sisyphus", true,
		"activePath", ".pi/goals/active_goal.md",
	)
	return with(g, kv...)
}

func wgAuditorProgress(mod func(p *auditorWidgetProgress)) *auditorWidgetProgress {
	started := float64(time.Now().UnixMilli() - 5000)
	p := &auditorWidgetProgress{
		currentTool:          "read",
		currentToolArgs:      `{"path":"test.txt"}`,
		currentToolStartedAt: &started,
		recentOutput:         []string{"checking file exists...", "confirming test coverage..."},
		phase:                "tool_executing",
		elapsedMs:            5000,
	}
	if mod != nil {
		mod(p)
	}
	return p
}

func fp(f float64) *float64 { return &f }

func reMatch(t *testing.T, re, s string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Errorf("expected match /%s/ in %q", re, s)
	}
}

func reNoMatch(t *testing.T, re, s string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(s) {
		t.Errorf("expected no match /%s/ in %q", re, s)
	}
}

func lastLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func newWidget(tui goalTUI, goal func() *jsObject, mod func(o *goalWidgetOptions)) *goalWidgetComponent {
	o := goalWidgetOptions{tui: tui, theme: createMockTheme(), getGoal: goal, getOpenGoalCount: func() int { return 1 }}
	if mod != nil {
		mod(&o)
	}
	return newGoalWidgetComponent(o)
}

// ── keybinding harness ──

const (
	ctrlShiftT = "\x1b[27;6;84~"
	ctrlShiftA = "\x1b[27;6;65~"
)

type wgSubscriptions struct {
	cb   func(string) *goalInputResult
	live map[int]bool
	next int
}

type wgKeyUI struct{ subs *wgSubscriptions }

func (u *wgKeyUI) custom(goalCustomFactory, goalCustomOptions) any { return nil }
func (u *wgKeyUI) setWorkingVisible(bool)                          {}
func (u *wgKeyUI) notify(string, string)                           {}
func (u *wgKeyUI) input(string, string) (string, bool)             { return "", false }
func (u *wgKeyUI) onTerminalInput(cb func(string) *goalInputResult) func() {
	u.subs.cb = cb
	id := u.subs.next
	u.subs.next++
	u.subs.live[id] = true
	return func() { delete(u.subs.live, id) }
}

type wgKeyCtx struct{ u *wgKeyUI }

func (c *wgKeyCtx) hasUI() bool { return true }
func (c *wgKeyCtx) cwd() string { return "" }
func (c *wgKeyCtx) ui() goalUI  { return c.u }

type wgCore struct {
	modalDepth int
	audit      *auditorWidgetProgress
	goal       *jsObject
	consumed   *[]string
	expanded   *bool
	handle     goalWidgetHandle
	unsub      func()
	debug      bool
}

func (c *wgCore) goalModalDepth() int                   { return c.modalDepth }
func (c *wgCore) auditProgress() *auditorWidgetProgress { return c.audit }
func (c *wgCore) currentGoal() *jsObject                { return c.goal }
func (c *wgCore) pauseActiveGoal(goalExtensionContext)  { *c.consumed = append(*c.consumed, "pause") }
func (c *wgCore) abortAudit(goalExtensionContext)       { *c.consumed = append(*c.consumed, "abort-audit") }
func (c *wgCore) isDashboardExpanded() bool             { return *c.expanded }
func (c *wgCore) toggleDashboardExpanded() {
	*c.expanded = !*c.expanded
	*c.consumed = append(*c.consumed, "toggle")
}
func (c *wgCore) toggleGoalAuditor(goalExtensionContext) {
	*c.consumed = append(*c.consumed, "toggle-auditor")
}
func (c *wgCore) widgetComponent() goalWidgetHandle                     { return c.handle }
func (c *wgCore) terminalInputUnsubscribe() func()                      { return c.unsub }
func (c *wgCore) setTerminalInputUnsubscribe(f func())                  { c.unsub = f }
func (c *wgCore) debugMode() bool                                       { return c.debug }
func (c *wgCore) setDebugMode(on bool)                                  { c.debug = on }
func (c *wgCore) removeDebugFile(goalExtensionContext, string)          {}
func (c *wgCore) writeDebugFile(goalExtensionContext, string, string)   {}
func (c *wgCore) setGoal(goalExtensionContext, *jsObject, bool, string) {}
func (c *wgCore) clearStateGoal()                                       {}
func (c *wgCore) focusedGoalID() *string                                { return nil }
func (c *wgCore) deleteGoalByID(string)                                 {}
func (c *wgCore) assignFocusedGoalID(*string)                           {}
func (c *wgCore) clearStoppedRuntimeState()                             {}
func (c *wgCore) updateUI(goalExtensionContext)                         {}
func (c *wgCore) setAuditProgress(p *auditorWidgetProgress)             { c.audit = p }
func (c *wgCore) auditProgressNow() *auditorWidgetProgress              { return c.audit }

type wgInvalidateOnly struct{ consumed *[]string }

func (h wgInvalidateOnly) Invalidate()                        { *h.consumed = append(*h.consumed, "invalidate") }
func (h wgInvalidateOnly) handleNavigationKey(string) bool    { return false }
func (h wgInvalidateOnly) handleCompactScrollKey(string) bool { return false }

type keyHarness struct {
	core     *wgCore
	ctx      *wgKeyCtx
	subs     *wgSubscriptions
	expanded *bool
	consumed *[]string
	rawFire  func(string) *goalInputResult
}

func (h *keyHarness) fire(data string) *goalInputResult {
	if h.subs.cb == nil {
		return nil
	}
	return h.rawFire(data)
}

func newKeyHarness(initialExpanded bool, handle func(consumed *[]string) goalWidgetHandle) *keyHarness {
	expanded := initialExpanded
	consumed := []string{}
	subs := &wgSubscriptions{live: map[int]bool{}}
	ctx := &wgKeyCtx{u: &wgKeyUI{subs: subs}}
	core := &wgCore{consumed: &consumed, expanded: &expanded}
	if handle != nil {
		core.handle = handle(&consumed)
	}
	syncTerminalInputPause(core, ctx)
	h := &keyHarness{core: core, ctx: ctx, subs: subs, expanded: &expanded, consumed: &consumed}
	h.rawFire = func(data string) *goalInputResult { return subs.cb(data) }
	return h
}

func keybindingHarness(initialExpanded bool) *keyHarness {
	return newKeyHarness(initialExpanded, func(consumed *[]string) goalWidgetHandle { return wgInvalidateOnly{consumed} })
}

func activeAutoGoal() *jsObject { return wgObj("status", "active", "autoContinue", true) }

func consumeOK(t *testing.T, got *goalInputResult, what string) {
	t.Helper()
	if got == nil || !got.consume {
		t.Errorf("%s: expected {consume:true}, got %v", what, got)
	}
}

// ── scroll harness ──

func manyTasksGoal() *jsObject {
	tasks := make([]any, 30)
	for i := range tasks {
		tasks[i] = wgTask("t"+itoa(i+1), "Task number "+itoa(i+1), "pending")
	}
	tasks[4] = with(tasks[4].(*jsObject), "status", "complete", "completedAt", "2026-01-01T10:00:00.000Z")
	tasks[19] = with(tasks[19].(*jsObject), "status", "complete", "completedAt", "2026-01-01T11:00:00.000Z")
	return wgGoal("taskList", wgTaskList(tasks...))
}

type scrollHarnessT struct {
	*keyHarness
	component *goalWidgetComponent
}

func scrollHarness(initialExpanded bool, g *jsObject) *scrollHarnessT {
	if g == nil {
		g = manyTasksGoal()
	}
	expanded := initialExpanded
	tui, _ := createMockTUI()
	component := newWidget(tui, func() *jsObject { return g }, func(o *goalWidgetOptions) {
		o.getExpanded = func() bool { return expanded }
	})
	consumed := []string{}
	subs := &wgSubscriptions{live: map[int]bool{}}
	ctx := &wgKeyCtx{u: &wgKeyUI{subs: subs}}
	core := &wgCore{consumed: &consumed, expanded: &expanded, handle: component}
	syncTerminalInputPause(core, ctx)
	h := &keyHarness{core: core, ctx: ctx, subs: subs, expanded: &expanded, consumed: &consumed}
	h.rawFire = func(data string) *goalInputResult {
		result := subs.cb(data)
		if result != nil && result.consume {
			consumed = append(consumed, "c:"+data)
		}
		return result
	}
	// consumed is re-sliced by rawFire; keep the pointer in sync.
	h.consumed = &consumed
	return &scrollHarnessT{keyHarness: h, component: component}
}

func countPrefix(list []string, p string) int {
	n := 0
	for _, s := range list {
		if strings.HasPrefix(s, p) {
			n++
		}
	}
	return n
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func freshStableState() *stableHeightState { return &stableHeightState{} }

func nLines(n int, f func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func rowsPtr(n int) *int { return &n }

// dock fakes for the measured-chrome twin.
type wgDockNode struct{ r func(w int) []string }

func (n wgDockNode) render(w int) []string { return n.r(w) }

type wgDockContainer struct{ kids []any }

func (c *wgDockContainer) dockChildren() []any { return c.kids }

type wgDockTUI struct {
	rows     int
	children []any
}

func (d *wgDockTUI) getShowHardwareCursor() bool { return false }
func (d *wgDockTUI) setShowHardwareCursor(bool)  {}
func (d *wgDockTUI) requestRender()              {}
func (d *wgDockTUI) terminalRows() (int, bool)   { return d.rows, true }
func (d *wgDockTUI) dockChildren() []any         { return d.children }

func TestGoalWidget(t *testing.T) {
	f := "goal-widget"
	theme := wgTestTheme

	tw(t, f, "renderGoalWidgetLines renders the unified compact dashboard", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal(), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `^╭─ pi-goal-x ─ Componentize the goal widget`, lines[0])
		reNoMatch(t, `1m05s`, lines[0])
		reMatch(t, `goal: sisyphus running \[1m05s 2\.5K\]`, lines[1])
		reMatch(t, `^╰─ .*Ctrl\+Shift\+T: expand`, lastLine(lines))
	})
	tw(t, f, "renderGoalWidgetLines shows the complete state", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("status", "complete", "autoContinue", false, "sisyphus", false, "archivedPath", ".pi/goals/archived/goal.md"), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `All required work is complete`, strings.Join(lines, "\n"))
	})
	tw(t, f, "renderGoalWidgetLines highlights blocked state with reason and action", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("status", "blocked", "autoContinue", false, "pauseReason", "Missing API token", "pauseSuggestedAction", "Set TOKEN and run /goal-resume"), theme, 100, goalWidgetLinesOptions{})
		j := strings.Join(lines, "\n")
		reMatch(t, `goal: sisyphus blocked`, j)
		reMatch(t, `Blocker  Missing API token`, j)
		reMatch(t, `Action   Set TOKEN and run /goal-resume`, j)
	})
	tw(t, f, "renderGoalWidgetLines shows paused reason and who paused", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("status", "paused", "autoContinue", false, "stopReason", "agent", "pauseReason", "waiting on the user"), theme, 100, goalWidgetLinesOptions{})
		j := strings.Join(lines, "\n")
		reMatch(t, `goal: sisyphus paused \(agent\)`, j)
		reMatch(t, `Reason   waiting on the user`, j)
	})
	tw(t, f, "renderGoalWidgetLines shows other open goals and unfocused multi-goal guidance", func(t *testing.T) {
		focused := renderGoalWidgetLines(wgGoal(), theme, 100, goalWidgetLinesOptions{openGoalCount: 3})
		reMatch(t, `goal: sisyphus running \[1m05s 2\.5K\] \(\+2 open\)`, focused[1])
		unfocused := renderGoalWidgetLines(nil, theme, 100, goalWidgetLinesOptions{openGoalCount: 2})
		reMatch(t, `^╭─ pi-goal-x ─ Goal focus required`, unfocused[0])
		j := strings.Join(unfocused, "\n")
		reMatch(t, `2 open goals are available`, j)
		reMatch(t, `/goal-focus`, j)
	})
	tw(t, f, "renderAuditorWidgetLines shows the structured audit dashboard (§15.3)", func(t *testing.T) {
		progress := wgAuditorProgress(func(p *auditorWidgetProgress) { p.auditorLabel = "anthropic/claude" })
		lines := renderAuditorWidgetLines(progress, theme, 100, false, "")
		reMatch(t, `Independent completion audit ─ anthropic/claude`, lines[0])
		text := strings.Join(lines, "\n")
		idx := func(s string) int { return strings.Index(text, s) }
		if !(idx("Objective and success criteria") < idx("Verification contracts") &&
			idx("Verification contracts") < idx("Tasks and recorded evidence") &&
			idx("Tasks and recorded evidence") < idx("Workspace inspection") &&
			idx("Workspace inspection") < idx("Final decision")) {
			t.Error("five check stages out of order")
		}
		reMatch(t, `Esc: stop audit`, lastLine(lines))
		reNoMatch(t, `tool read`, text)
		reNoMatch(t, `checking file exists`, text)
	})
	tw(t, f, "renderAuditorWidgetLines shows tool and output details in expanded/debug mode", func(t *testing.T) {
		text := strings.Join(renderAuditorWidgetLines(wgAuditorProgress(nil), theme, 100, true, ""), "\n")
		reMatch(t, `tool read`, text)
		reMatch(t, `test\.txt`, text)
		reMatch(t, `checking file exists`, text)
	})
	tw(t, f, "renderAuditorWidgetLines drives check states from percentage bands (§15.2)", func(t *testing.T) {
		progress := wgAuditorProgress(func(p *auditorWidgetProgress) {
			p.phase = "running"
			p.percentage = fp(72)
			p.recentOutput = []string{}
		})
		text := strings.Join(renderAuditorWidgetLines(progress, theme, 100, false, ""), "\n")
		for _, re := range []string{`✓ Objective and success criteria`, `✓ Verification contracts`, `✓ Tasks and recorded evidence`, `◌ Workspace inspection`, `· Final decision`, `72%`} {
			reMatch(t, re, text)
		}
	})
	tw(t, f, "renderAuditorWidgetLines shows the progress bar at 0% and 100%", func(t *testing.T) {
		zero := strings.Join(renderAuditorWidgetLines(wgAuditorProgress(func(p *auditorWidgetProgress) { p.phase = "running"; p.percentage = fp(0); p.recentOutput = []string{} }), theme, 100, false, ""), "\n")
		reMatch(t, `0%`, zero)
		hundred := strings.Join(renderAuditorWidgetLines(wgAuditorProgress(func(p *auditorWidgetProgress) {
			p.phase = "running"
			p.percentage = fp(100)
			p.recentOutput = []string{}
		}), theme, 100, false, ""), "\n")
		reMatch(t, `100%`, hundred)
	})
	tw(t, f, "renderAuditorWidgetLines shows no percentage when undefined", func(t *testing.T) {
		progress := wgAuditorProgress(func(p *auditorWidgetProgress) { p.phase = "running"; p.label = "Working..." })
		j := strings.Join(renderAuditorWidgetLines(progress, theme, 100, false, ""), "\n")
		reMatch(t, `Working`, j)
		reNoMatch(t, `\d+%`, j)
	})
	tw(t, f, "renderAuditorWidgetLines shows the done phase without the stop hint", func(t *testing.T) {
		progress := wgAuditorProgress(func(p *auditorWidgetProgress) {
			p.phase = "done"
			p.currentTool = ""
			p.currentToolArgs = ""
			p.recentOutput = []string{}
		})
		reNoMatch(t, `Esc: stop audit`, strings.Join(renderAuditorWidgetLines(progress, theme, 100, false, ""), "\n"))
	})
	tw(t, f, "audit progress overrides normal goal display when provided", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal(), theme, 100, goalWidgetLinesOptions{auditorProgress: wgAuditorProgress(nil)})
		reMatch(t, `Independent completion audit`, lines[0])
		reNoMatch(t, `pi-goal-x ─`, lines[0])
	})
	tw(t, f, "finished audit shows the result card view (§15.4)", func(t *testing.T) {
		approved := renderAuditResultCardView(&auditResultView{"approved", "ok"}, theme, 100)
		reMatch(t, `Audit result ─ APPROVED`, approved[0])
		at := strings.Join(approved, "\n")
		reMatch(t, `✓ Objective satisfied\.`, at)
		reMatch(t, `✓ Verification requirements satisfied\.`, at)
		reMatch(t, `✓ Required tasks and evidence accepted\.`, at)
		rejected := renderAuditResultCardView(&auditResultView{"disapproved", "- Tests were not run after the final change.\n- Task \"docs\" has no evidence."}, theme, 100)
		rt := strings.Join(rejected, "\n")
		reMatch(t, `Audit result ─ CHANGES REQUIRED`, rt)
		reMatch(t, `✗ Tests were not run after the final change\.`, rt)
		reMatch(t, `✗ Task "docs" has no evidence\.`, rt)
	})
	tw(t, f, "renderGoalWidgetLines shows top-level task progress (§9.1, skipped counts as done)", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(wgTask("t1", "Task 1", "complete"), wgTask("t2", "Task 2", "pending"), wgTask("t3", "Task 3", "skipped"))), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `Tasks · ✓2 done · 1 open`, strings.Join(lines, "\n"))
	})
	tw(t, f, "renderGoalWidgetLines shows the current task (first pending, inferred)", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(wgTask("t1", "Task 1", "complete"), wgTask("t2", "Task 2", "pending"), wgTask("t3", "Task 3", "pending"))), theme, 100, goalWidgetLinesOptions{})
		body := strings.Join(lines, "\n")
		reMatch(t, `Tasks · ✓1 done · 2 open`, body)
		reMatch(t, `Current  t2 · Task 2`, body)
	})
	tw(t, f, "renderGoalWidgetLines shows 'All tasks complete' when all done (§9.4)", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(wgTask("t1", "Task 1", "complete"), wgTask("t2", "Task 2", "skipped"))), theme, 100, goalWidgetLinesOptions{})
		j := strings.Join(lines, "\n")
		reMatch(t, `Tasks · ✓2 done · 0 open`, j)
		reMatch(t, `Current  All tasks complete`, j)
	})
	tw(t, f, "renderGoalWidgetLines omits task rows when no taskList", func(t *testing.T) {
		body := strings.Join(renderGoalWidgetLines(wgGoal(), theme, 100, goalWidgetLinesOptions{}), "\n")
		if strings.Contains(body, "Tasks") || strings.Contains(body, "Current") {
			t.Error("task rows shown without a taskList")
		}
	})
	tw(t, f, "renderGoalWidgetLines shows subtask progress for the current parent task (§9.3)", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("currentTaskId", "t1", "taskList", wgTaskList(
			wgTask("t1", "Parent", "pending", "subtasks", []any{wgTask("t1a", "Child", "complete"), wgTask("t1b", "Child2", "complete")}))), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `· Sub 2/2 \[.*\]`, strings.Join(lines, "\n"))
	})
	tw(t, f, "renderGoalWidgetLines infers a pending subtask as current at any depth", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(
			wgTask("t1", "Parent", "complete", "subtasks", []any{wgTask("t1a", "Child", "pending")}))), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `Current  t1a · Child`, strings.Join(lines, "\n"))
	})
	tw(t, f, "renderGoalWidgetLines shows all complete when subtasks are done", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(
			wgTask("t1", "Parent", "complete", "subtasks", []any{wgTask("t1a", "Child", "complete")}))), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `Current  All tasks complete`, strings.Join(lines, "\n"))
	})
	tw(t, f, "renderGoalWidgetLines suppresses task info when disableTasks is true with subtasks (§9.5)", func(t *testing.T) {
		lines := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(
			wgTask("t1", "Parent", "pending", "subtasks", []any{wgTask("t1a", "Child", "pending")}))), theme, 100, goalWidgetLinesOptions{disableTasks: true})
		body := strings.Join(lines, "\n")
		for _, s := range []string{"Tasks", "t1a", "Current"} {
			if strings.Contains(body, s) {
				t.Errorf("%q shown with disableTasks", s)
			}
		}
	})

	// ── TUI rendering path: GoalWidgetComponent ──

	tw(t, f, "GoalWidgetComponent renders through mock TUI path", func(t *testing.T) {
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
		lines := c.Render(100)
		if len(lines) == 0 {
			t.Fatal("Component renders lines")
		}
		reMatch(t, `^╭─ pi-goal-x ─ Componentize the goal widget`, lines[0])
		reMatch(t, `goal: sisyphus running \[1m05s 2\.5K\]`, lines[1])
	})
	tw(t, f, "GoalWidgetComponent shows open goal count when > 1", func(t *testing.T) {
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) { o.getOpenGoalCount = func() int { return 3 } })
		reMatch(t, `goal: sisyphus running \[1m05s 2\.5K\] \(\+2 open\)`, strings.Join(c.Render(100), "\n"))
	})
	tw(t, f, "GoalWidgetComponent update triggers requestRender", func(t *testing.T) {
		tui, state := createMockTUI()
		c := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
		before := state.requestRenderCalls
		c.update()
		if state.requestRenderCalls <= before {
			t.Error("update() triggers requestRender")
		}
	})
	tw(t, f, "GoalWidgetComponent invalidate triggers requestRender", func(t *testing.T) {
		tui, state := createMockTUI()
		c := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
		before := state.requestRenderCalls
		c.Invalidate()
		if state.requestRenderCalls <= before {
			t.Error("invalidate() triggers requestRender")
		}
	})
	tw(t, f, "GoalWidgetComponent renders the audit dashboard when audit progress is present", func(t *testing.T) {
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) {
			o.getAuditorProgress = func() *auditorWidgetProgress {
				started := float64(time.Now().UnixMilli() - 5000)
				return &auditorWidgetProgress{currentTool: "read", currentToolArgs: `{"path":"test.txt"}`, currentToolStartedAt: &started, recentOutput: []string{"checking..."}, phase: "tool_executing", elapsedMs: 5000}
			}
		})
		text := strings.Join(c.Render(100), "\n")
		reMatch(t, `Independent completion audit`, text)
		reMatch(t, `Objective and success criteria`, text)
		reNoMatch(t, `tool read`, text)
	})
	tw(t, f, "GoalWidgetComponent renders with disableTasks setting", func(t *testing.T) {
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject {
			return wgGoal("taskList", wgTaskList(wgTask("t1", "Task 1", "pending")))
		}, func(o *goalWidgetOptions) {
			o.getSettings = func() goalSettings { return goalSettings{disableTasks: true} }
		})
		text := strings.Join(c.Render(100), "\n")
		if strings.Contains(text, "Tasks") {
			t.Error("Tasks hidden when disableTasks is true")
		}
		if strings.Contains(text, "Current") {
			t.Error("Current hidden when disableTasks is true")
		}
	})
	tw(t, f, "GoalWidgetComponent shows completed goal status", func(t *testing.T) {
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject {
			return wgGoal("status", "complete", "archivedPath", ".pi/goals/archived/g.md", "sisyphus", false)
		}, nil)
		reMatch(t, `All required work is complete`, strings.Join(c.Render(100), "\n"))
	})
	tw(t, f, "GoalWidgetComponent safety net at width ${width} with long content", func(t *testing.T) {
		for _, width := range []int{50, 70, 100, 109, 120} {
			tui, _ := createMockTUI()
			c := newWidget(tui, func() *jsObject {
				return wgGoal("objective", strings.Repeat("x", 500),
					"activePath", strings.Repeat("/very/long/path/that/should/definitely/be/truncated/because/it/exceeds/the/available/width/by/a/lot/and/would/cause/a/crash/if/not/truncated", 3))
			}, func(o *goalWidgetOptions) { o.getOpenGoalCount = func() int { return 8 } })
			for i, l := range c.Render(width) {
				if visibleWidth(l) > width {
					t.Errorf("width %d: line %d has visible width %d > %d", width, i, visibleWidth(l), width)
				}
			}
		}
	})
	tw(t, f, "GoalWidgetComponent with auditor progress at width 109 (crash regression)", func(t *testing.T) {
		tui, _ := createMockTUI()
		width := 109
		c := newWidget(tui, func() *jsObject {
			return wgGoal("objective", strings.Repeat("Achieve full end-to-end test suite pass on Linux x86_64 with 100% vendor parity — all e2e pass (no skips). The constraints should be exactly those as per the design document and the previous goals. We need to dissassemble the vendor's implementation live, stepping through, to ensure we implement this in full.", 2),
				"activePath", strings.Repeat("/Users/tom/projects/some-very-long-project-path-that-exceeds-terminal-width/when-combined-with-prefix-characters/and-wrapping-scenarios/src/extremely/nested/deeply/nested/module/that/makes/this/really/long/really/long/really/long.ts", 2))
		}, func(o *goalWidgetOptions) {
			o.getAuditorProgress = func() *auditorWidgetProgress {
				return &auditorWidgetProgress{phase: "thinking", label: "Very long auditor label that should not cause an overflow even when rendered at narrow terminal width with all the prefixes and padding", percentage: fp(45), recentOutput: []string{}, elapsedMs: 5000}
			}
		})
		for i, l := range c.Render(width) {
			if l != "" && visibleWidth(l) > width {
				t.Errorf("line %d has visible width %d > %d", i, visibleWidth(l), width)
			}
		}
	})
	tw(t, f, "GoalWidgetComponent unfocused with 38 open goals at width 109", func(t *testing.T) {
		tui, _ := createMockTUI()
		width := 109
		c := newWidget(tui, func() *jsObject { return nil }, func(o *goalWidgetOptions) { o.getOpenGoalCount = func() int { return 38 } })
		for i, l := range c.Render(width) {
			if l != "" && visibleWidth(l) > width {
				t.Errorf("line %d has visible width %d > %d", i, visibleWidth(l), width)
			}
		}
	})

	// ── §19.5 dashboard interaction tests ──

	tw(t, f, "re-registering terminal input replaces the old listener", func(t *testing.T) {
		h := keybindingHarness(false)
		eq(t, len(h.subs.live), 1, "listener count")
		syncTerminalInputPause(h.core, h.ctx)
		eq(t, len(h.subs.live), 1, "listener count after resync")
		h.fire(ctrlShiftT)
		eq(t, *h.consumed, []string{"toggle"}, "consumed")
	})
	tw(t, f, "compact mode is the default and the task shortcut expands and collapses", func(t *testing.T) {
		h := keybindingHarness(false)
		compact := renderGoalWidgetLines(wgGoal("taskList", wgTaskList(wgTask("t1", "T1", "pending"))), theme, 100, goalWidgetLinesOptions{})
		reMatch(t, `Ctrl\+Shift\+T: expand tasks`, strings.Join(compact, "\n"))
		h.fire(ctrlShiftT)
		eq(t, *h.expanded, true, "ctrl+shift+t expands the dashboard")
		h.fire(ctrlShiftT)
		eq(t, *h.expanded, false, "the same shortcut collapses it")
		eq(t, *h.consumed, []string{"toggle", "toggle"}, "consumed")
	})
	tw(t, f, "one Kitty key press toggles the dashboard once despite repeat and release events", func(t *testing.T) {
		h := keybindingHarness(false)
		consumeOK(t, h.fire("\x1b[116;6:1u"), "press")
		eq(t, *h.expanded, true, "expanded")
		h.fire("\x1b[116;6:2u")
		h.fire("\x1b[116;6:3u")
		eq(t, *h.expanded, true, "still expanded")
		eq(t, *h.consumed, []string{"toggle"}, "consumed")
	})
	tw(t, f, "one Kitty scroll key press advances one task despite repeat and release events", func(t *testing.T) {
		h := scrollHarness(false, nil)
		h.component.Render(100)
		h.fire("\x1b[1;6:1B")
		h.fire("\x1b[1;6:2B")
		h.fire("\x1b[1;6:3B")
		reMatch(t, `↑ 16 more tasks`, strings.Join(h.component.Render(100), "\n"))
	})
	tw(t, f, "escape collapses the expanded dashboard instead of pausing", func(t *testing.T) {
		h := keybindingHarness(true)
		h.fire("\x1b")
		eq(t, *h.expanded, false, "escape collapses the expanded dashboard")
		eq(t, *h.consumed, []string{"toggle"}, "consumed")
		if contains(*h.consumed, "pause") {
			t.Error("escape while expanded must not pause the goal")
		}
	})
	tw(t, f, "repeated Escape after collapsing does not pause the goal", func(t *testing.T) {
		h := keybindingHarness(true)
		h.core.goal = activeAutoGoal()
		h.fire("\x1b[27;1:1u")
		h.fire("\x1b[27;1:2u")
		h.fire("\x1b[27;1:3u")
		eq(t, *h.consumed, []string{"toggle"}, "consumed")
	})
	tw(t, f, "escape still pauses a running goal when the dashboard is compact", func(t *testing.T) {
		h := keybindingHarness(false)
		h.core.goal = activeAutoGoal()
		h.fire("\x1b")
		eq(t, *h.consumed, []string{"pause"}, "consumed")
	})
	tw(t, f, "ctrl+shift+a toggles the focused goal's auditor and refreshes the widget", func(t *testing.T) {
		h := keybindingHarness(false)
		h.core.goal = activeAutoGoal()
		h.fire(ctrlShiftA)
		eq(t, *h.consumed, []string{"toggle-auditor", "invalidate"}, "auditor toggle + widget refresh")
		eq(t, *h.expanded, false, "auditor toggle must not expand the dashboard")
	})
	tw(t, f, "ctrl+shift+a is inert while a goal modal is open", func(t *testing.T) {
		h := keybindingHarness(false)
		h.core.modalDepth = 1
		h.core.goal = activeAutoGoal()
		h.fire(ctrlShiftA)
		eq(t, *h.consumed, []string{}, "modal depth guard swallows the chord")
	})
	tw(t, f, "expanded mode renders the full dashboard without mutating editor state", func(t *testing.T) {
		tui, _ := createMockTUI()
		g := wgGoal("taskList", wgTaskList(wgTask("t1", "T1", "pending")))
		expanded := false
		c := newWidget(tui, func() *jsObject { return g }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return expanded } })
		compactLines := c.Render(100)
		expanded = true
		expandedLines := c.Render(100)
		text := strings.Join(expandedLines, "\n")
		reMatch(t, `├─ Tasks `, text)
		reMatch(t, `Esc/Ctrl\+Shift\+T: collapse`, text)
		eq(t, c.Render(100), expandedLines, "re-rendering returns identical lines")
		expanded = false
		eq(t, c.Render(100), compactLines, "collapsing returns the compact view")
	})
	tw(t, f, "goal state updates are visible in both modes", func(t *testing.T) {
		tui, _ := createMockTUI()
		current := wgGoal("objective", "=== Goal ===\nObjective: First objective")
		expanded := false
		c := newWidget(tui, func() *jsObject { return current }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return expanded } })
		reMatch(t, `First objective`, strings.Join(c.Render(100), "\n"))
		current = wgGoal("objective", "=== Goal ===\nObjective: Second objective")
		reMatch(t, `Second objective`, strings.Join(c.Render(100), "\n"))
		expanded = true
		reMatch(t, `Second objective`, strings.Join(c.Render(100), "\n"))
	})
	tw(t, f, "audit mode temporarily replaces the normal view and returns after", func(t *testing.T) {
		tui, _ := createMockTUI()
		audit := wgAuditorProgress(nil)
		c := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) {
			o.getAuditorProgress = func() *auditorWidgetProgress { return audit }
		})
		during := strings.Join(c.Render(100), "\n")
		reMatch(t, `Independent completion audit`, during)
		reNoMatch(t, `pi-goal-x ─`, during)
		audit = nil
		after := strings.Join(c.Render(100), "\n")
		reMatch(t, `pi-goal-x ─`, after)
		reNoMatch(t, `audit complete|auditing`, after)
	})
	tw(t, f, "ledger events flow into the dashboard recent-activity feed", func(t *testing.T) {
		tui, _ := createMockTUI()
		events := []*jsObject{
			wgObj("type", "goal_created", "goalId", "g1", "objective", "x", "sisyphus", false, "autoContinue", true, "at", "2026-01-01T09:00:00.000Z"),
			wgObj("type", "task_started", "goalId", "g1", "taskId", "t1", "at", "2026-01-01T09:01:00.000Z"),
		}
		c := newWidget(tui, func() *jsObject {
			return wgGoal("id", "g1", "taskList", wgTaskList(wgTask("t1", "T1", "pending")))
		}, func(o *goalWidgetOptions) {
			o.getExpanded = func() bool { return true }
			o.getLedgerEvents = func() []*jsObject { return events }
		})
		reMatch(t, `Started “T1”\.`, strings.Join(c.Render(100), "\n"))
	})

	// ── §9.6 task-list scrolling ──

	const (
		up     = "\x1b[A"
		down   = "\x1b[B"
		pgup   = "\x1b[5~"
		pgdn   = "\x1b[6~"
		home   = "\x1b[H"
		end    = "\x1b[F"
		csUp   = "\x1b[1;6A"
		csDown = "\x1b[1;6B"
		csHome = "\x1b[1;6H"
		csEnd  = "\x1b[1;6F"
		csPgUp = "\x1b[5;6~"
		csPgDn = "\x1b[6;6~"
	)
	render := func(h *scrollHarnessT) string { return strings.Join(h.component.Render(100), "\n") }

	tw(t, f, "compact default viewport is anchored to the most recently completed tasks", func(t *testing.T) {
		text := strings.Join(renderGoalWidgetLines(manyTasksGoal(), theme, 100, goalWidgetLinesOptions{}), "\n")
		reMatch(t, `↑ 15 more tasks`, text)
		reMatch(t, `Task number 20`, text)
		reMatch(t, `Task number 16`, text)
		reNoMatch(t, `[✓▸·~] t1\s`, text)
		reMatch(t, `… \+10 more tasks`, text)
	})
	tw(t, f, "plain arrows are never consumed in compact mode; the editor keeps them", func(t *testing.T) {
		h := scrollHarness(false, nil)
		h.component.Render(100)
		h.fire(up)
		h.fire(down)
		h.fire(pgup)
		h.fire(pgdn)
		eq(t, *h.consumed, []string{}, "plain arrow/page keys belong to the editor in compact mode")
	})
	tw(t, f, "Ctrl+Shift+↑/↓ scroll the compact list one row; Esc is untouched", func(t *testing.T) {
		h := scrollHarness(false, nil)
		h.component.Render(100)
		reMatch(t, `↑ 15 more tasks`, render(h))
		h.fire(csUp)
		if !contains(*h.consumed, "c:"+csUp) {
			t.Error("Ctrl+Shift+↑ is consumed")
		}
		reMatch(t, `↑ 14 more tasks`, render(h))
		h.fire(csDown)
		reMatch(t, `↑ 15 more tasks`, render(h))
		h.fire(up)
		h.fire("\x1b")
		eq(t, countPrefix(*h.consumed, "c:"+up), 0, "plain ↑ is never consumed")
		if contains(*h.consumed, "c:\x1b") {
			t.Error("Esc is not consumed when not expanded")
		}
	})
	tw(t, f, "Ctrl+Shift+Home/End/PgUp/PgDn jump and page in the compact list", func(t *testing.T) {
		h := scrollHarness(false, nil)
		h.component.Render(100)
		h.fire(csHome)
		reNoMatch(t, `↑ \d+ more tasks`, render(h))
		reMatch(t, `[✓▸·~] t1\s`, render(h))
		h.fire(csEnd)
		reMatch(t, `↑ 25 more tasks`, render(h))
		reNoMatch(t, `… \+\d+ more tasks`, render(h))
		h.fire(csPgDn)
		reMatch(t, `↑ 25 more tasks`, render(h))
		h.fire(csPgUp)
		reMatch(t, `↑ 20 more tasks`, render(h))
	})
	shortGoal := func() *jsObject {
		return wgGoal("taskList", wgTaskList(wgTask("t1", "One", "pending"), wgTask("t2", "Two", "pending"), wgTask("t3", "Three", "pending")))
	}
	tw(t, f, "compact overflow shows the scroll hint in the footer; a short list does not", func(t *testing.T) {
		overflowing := scrollHarness(false, nil)
		reMatch(t, `Ctrl\+Shift\+T: expand · Ctrl\+Shift\+↑↓: scroll`, render(overflowing))
		narrow := scrollHarness(false, nil)
		reMatch(t, `↑↓: scroll`, strings.Join(narrow.component.Render(40), "\n"))
		short := scrollHarness(false, shortGoal())
		reMatch(t, `Ctrl\+Shift\+T: expand tasks`, render(short))
	})
	tw(t, f, "Ctrl+Shift chords are not consumed when the compact list fits", func(t *testing.T) {
		h := scrollHarness(false, shortGoal())
		h.component.Render(100)
		h.fire(csUp)
		h.fire(csDown)
		eq(t, *h.consumed, []string{}, "nothing to scroll → chords pass through")
	})
	tw(t, f, "expanded mode scrolls the task tree with arrows, Home, End, and page keys", func(t *testing.T) {
		h := scrollHarness(true, nil)
		h.component.Render(100)
		reNoMatch(t, `↑ \d+ more tasks`, render(h))
		reMatch(t, `Task number 20`, render(h))
		h.fire(down)
		reMatch(t, `↑ 1 more task`, render(h))
		h.fire(home)
		reNoMatch(t, `↑ \d+ more tasks`, render(h))
		h.fire(up)
		h.fire(end)
		reMatch(t, `↑ 10 more tasks`, render(h))
		h.fire(pgdn)
		reMatch(t, `↑ 10 more tasks`, render(h))
		h.fire(pgup)
		reNoMatch(t, `↑ \d+ more tasks`, render(h))
		if countPrefix(*h.consumed, "c:") < 6 {
			t.Error("every navigation key is consumed while expanded")
		}
	})
	tw(t, f, "Ctrl+Shift+T toggles expansion; plain arrows scroll only while expanded", func(t *testing.T) {
		h := scrollHarness(false, nil)
		h.component.Render(100)
		h.fire(csUp)
		if !contains(*h.consumed, "c:"+csUp) {
			t.Error("compact scroll — chord")
		}
		h.fire(ctrlShiftT)
		eq(t, *h.expanded, true, "expanded")
		h.fire(up)
		if !contains(*h.consumed, "c:"+up) {
			t.Error("expanded is modal → plain arrow scrolls")
		}
		h.fire("\x1b")
		eq(t, *h.expanded, false, "collapsed")
		h.fire(up)
		eq(t, countPrefix(*h.consumed, "c:"), 4, "chord + Ctrl+Shift+T + expanded ↑ + Esc consumed; plain ↑ reaches the editor after collapse")
	})
	tw(t, f, "scrolling before the first render keeps the requested viewport", func(t *testing.T) {
		compact := scrollHarness(false, nil)
		consumeOK(t, compact.fire(csDown), "compact")
		reMatch(t, `↑ 16 more tasks`, render(compact))
		expanded := scrollHarness(true, nil)
		consumeOK(t, expanded.fire(down), "expanded")
		reMatch(t, `↑ 1 more task`, render(expanded))
	})
	tw(t, f, "expanded short task lists keep plain arrows out of the editor", func(t *testing.T) {
		g := wgGoal("taskList", wgTaskList(wgTask("t1", "One", "pending"), wgTask("t2", "Two", "pending")))
		h := scrollHarness(true, g)
		h.component.Render(100)
		before := h.component.Render(100)
		consumeOK(t, h.fire(down), "down")
		consumeOK(t, h.fire(up), "up")
		eq(t, h.component.Render(100), before, "render unchanged")
	})
	tw(t, f, "compact scroll chords do not change the hidden list while expanded", func(t *testing.T) {
		h := scrollHarness(false, nil)
		reMatch(t, `↑ 15 more tasks`, render(h))
		h.fire(ctrlShiftT)
		eq(t, *h.expanded, true, "expanded")
		if got := h.fire(csHome); got != nil {
			t.Errorf("chord while expanded = %v, want undefined", got)
		}
		h.fire("\x1b")
		reMatch(t, `↑ 15 more tasks`, render(h))
	})
	tw(t, f, "a new completion re-anchors the viewport to the latest completed task", func(t *testing.T) {
		current := manyTasksGoal()
		tui, _ := createMockTUI()
		c := newWidget(tui, func() *jsObject { return current }, nil)
		reMatch(t, `↑ 15 more tasks`, strings.Join(c.Render(100), "\n"))
		eq(t, c.handleCompactScrollKey("home"), true, "compact list overflows → chord consumed")
		reNoMatch(t, `↑ \d+ more tasks`, strings.Join(c.Render(100), "\n"))
		reMatch(t, `[✓▸·~] t1\s`, strings.Join(c.Render(100), "\n"))
		eq(t, c.handleCompactScrollKey("down"), true, "still overflows → consumed")
		tasks := []any{}
		for _, x := range current.obj("taskList").vals["tasks"].([]any) {
			tasks = append(tasks, x.(*jsObject).clone())
		}
		tasks[8] = with(tasks[8].(*jsObject), "status", "complete", "completedAt", "2026-01-01T12:00:00.000Z")
		current = with(current, "taskList", wgTaskList(tasks...))
		text := strings.Join(c.Render(100), "\n")
		reMatch(t, `↑ 4 more tasks`, text)
		reMatch(t, `Task number 9`, text)
	})

	// ── terminal-height bound ──

	tw(t, f, "boundWidgetRenderLines is a no-op when terminal rows are unknown", func(t *testing.T) {
		lines := []string{"a", "b", "c", "d", "e"}
		eq(t, boundWidgetRenderLines(lines, nil), lines, "undefined")
		eq(t, boundWidgetRenderLines(lines, rowsPtr(0)), lines, "0")
		eq(t, boundWidgetRenderLines(lines, rowsPtr(-3)), lines, "-3")
	})
	tw(t, f, "boundWidgetRenderLines leaves content that fits unchanged", func(t *testing.T) {
		lines := nLines(8, func(i int) string { return fmt.Sprintf("line %d", i) })
		eq(t, boundWidgetRenderLines(lines, rowsPtr(30)), lines, "30")
		eq(t, boundWidgetRenderLines(lines, rowsPtr(14)), lines, "14")
	})
	tw(t, f, "boundWidgetRenderLines head-slices content over the cap deterministically", func(t *testing.T) {
		lines := nLines(13, func(i int) string { return fmt.Sprintf("line %d", i) })
		rows := 13
		cap := max(1, rows-widgetHeightReserve)
		out := boundWidgetRenderLines(lines, rowsPtr(rows))
		eq(t, len(out), cap, "length")
		eq(t, out, lines[:cap], "keeps the head (identity/status/tasks)")
		eq(t, boundWidgetRenderLines(lines, rowsPtr(rows)), out, "deterministic")
	})
	tw(t, f, "boundWidgetRenderLines floors at 1 line for tiny terminals", func(t *testing.T) {
		lines := []string{"a", "b", "c", "d", "e"}
		eq(t, len(boundWidgetRenderLines(lines, rowsPtr(3))), 1, "length")
		eq(t, boundWidgetRenderLines(lines, rowsPtr(3)), []string{"a"}, "head")
	})
	tw(t, f, "renderGoalWidgetLines accepts an optional terminalRows bound (pure path)", func(t *testing.T) {
		g := wgGoal()
		unbounded := renderGoalWidgetLines(g, theme, 100, goalWidgetLinesOptions{openGoalCount: 1})
		rows := max(1, len(unbounded)-1)
		bounded := renderGoalWidgetLines(g, theme, 100, goalWidgetLinesOptions{openGoalCount: 1, terminalRows: rowsPtr(rows)})
		cap := max(1, rows-widgetHeightReserve)
		if len(bounded) > cap {
			t.Errorf("bounded to %d, got %d", cap, len(bounded))
		}
		reMatch(t, `^╭─ pi-goal-x`, bounded[0])
	})
	tw(t, f, "GoalWidgetComponent with terminalRows caps the compact dashboard at equal terminal height", func(t *testing.T) {
		tui, _ := createMockTUI(13)
		c := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
		lines := c.Render(100)
		cap := max(1, 13-widgetHeightReserve)
		if len(lines) > cap {
			t.Errorf("rendered %d lines, cap %d", len(lines), cap)
		}
		reMatch(t, `^╭─ pi-goal-x`, lines[0])
		reMatch(t, `goal: sisyphus running`, lines[1])
	})
	tw(t, f, "GoalWidgetComponent caps the expanded dashboard at equal terminal height", func(t *testing.T) {
		tui, _ := createMockTUI(24)
		c := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
		lines := c.Render(100)
		cap := max(1, 24-widgetHeightReserve)
		if len(lines) > cap {
			t.Errorf("rendered %d lines, cap %d", len(lines), cap)
		}
		reMatch(t, `^╭─ pi-goal-x`, lines[0])
	})
	tw(t, f, "GoalWidgetComponent leaves the expanded dashboard unchanged when it fits", func(t *testing.T) {
		tui, _ := createMockTUI(30)
		c := newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
		lines := c.Render(100)
		tui2, _ := createMockTUI()
		natural := newWidget(tui2, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } }).Render(100)
		eq(t, len(lines), len(natural), "fits -> byte-identical to the unbounded render")
	})
	tw(t, f, "GoalWidgetComponent bounds the audit dashboard, result card, debug panel, and unfocused panel", func(t *testing.T) {
		mk := func(rows int, extra func(o *goalWidgetOptions)) *goalWidgetComponent {
			tui, _ := createMockTUI(rows)
			return newWidget(tui, func() *jsObject { return wgGoal() }, func(o *goalWidgetOptions) {
				if extra != nil {
					extra(o)
				}
			})
		}
		audit := mk(8, func(o *goalWidgetOptions) {
			o.getAuditorProgress = func() *auditorWidgetProgress { return wgAuditorProgress(nil) }
		}).Render(100)
		if len(audit) > max(1, 8-widgetHeightReserve) {
			t.Errorf("audit rendered %d", len(audit))
		}
		reMatch(t, `^╭─ Independent completion audit`, audit[0])

		card := mk(6, func(o *goalWidgetOptions) {
			o.getAuditResult = func() *auditResultView { return &auditResultView{"approved", "Everything checks out."} }
		}).Render(100)
		if len(card) > max(1, 6-widgetHeightReserve) {
			t.Errorf("card rendered %d", len(card))
		}
		reMatch(t, `^╭─ Audit result`, card[0])

		debug := mk(36, func(o *goalWidgetOptions) { o.getDebugMode = func() bool { return true } }).Render(100)
		if len(debug) > max(1, 36-widgetHeightReserve) {
			t.Errorf("debug rendered %d", len(debug))
		}
		reMatch(t, `^╭─ pi-goal-x`, debug[0])

		tui, _ := createMockTUI(4)
		unfocused := newWidget(tui, func() *jsObject { return nil }, func(o *goalWidgetOptions) { o.getOpenGoalCount = func() int { return 2 } }).Render(100)
		if len(unfocused) > max(1, 4-widgetHeightReserve) {
			t.Errorf("unfocused rendered %d", len(unfocused))
		}
	})
	tw(t, f, "GoalWidgetComponent render height is deterministic across repeated renders", func(t *testing.T) {
		tui, _ := createMockTUI(13)
		c := newWidget(tui, func() *jsObject { return wgGoal() }, nil)
		first, second, third := len(c.Render(100)), len(c.Render(100)), len(c.Render(100))
		eq(t, first, second, "first/second")
		eq(t, second, third, "second/third")
	})

	// ── stable height per regime ──

	tw(t, f, "applyStableHeightBound: adaptive reserve caps the widget so the block + chrome fits the terminal", func(t *testing.T) {
		state := freshStableState()
		n14 := nLines(14, func(i int) string { return fmt.Sprintf("n%d", i) })
		n20 := nLines(20, func(i int) string { return fmt.Sprintf("n%d", i) })
		out := applyStableHeightBound(n14, rowsPtr(15), state, "regime", 7)
		eq(t, len(out), 8, "cap = terminalRows - measured reserve")
		eq(t, *state.stickyCap, 8, "stickyCap")
		eq(t, len(applyStableHeightBound(n20, rowsPtr(15), state, "regime", 7)), 8, "latch holds")
	})
	tw(t, f, "applyStableHeightBound: chrome change (editor grew) re-evaluates the latch", func(t *testing.T) {
		state := freshStableState()
		n14 := nLines(14, func(i int) string { return fmt.Sprintf("n%d", i) })
		first := applyStableHeightBound(n14, rowsPtr(15), state, "regime", 4)
		eq(t, len(first), 11, "first")
		eq(t, *state.stickyCap, 11, "stickyCap")
		second := applyStableHeightBound(n14, rowsPtr(15), state, "regime", 8)
		eq(t, len(second), 7, "reserve change clears the latch and re-evaluates")
		eq(t, *state.stickyCap, 7, "stickyCap")
		eq(t, *state.stickyReserve, 8, "stickyReserve")
		third := applyStableHeightBound(n14, rowsPtr(15), state, "regime", 4)
		eq(t, len(third), 11, "third")
	})
	tw(t, f, "applyStableHeightBound: default reserve keeps the 6-row contract unchanged", func(t *testing.T) {
		state := freshStableState()
		n20 := nLines(20, func(i int) string { return fmt.Sprintf("n%d", i) })
		out := applyStableHeightBound(n20, rowsPtr(24), state, "regime", widgetHeightReserve)
		eq(t, len(out), 24-widgetHeightReserve, "default reserve = WIDGET_HEIGHT_RESERVE")
		eq(t, *state.stickyReserve, widgetHeightReserve, "stickyReserve")
	})
	tw(t, f, "applyStableHeightBound: latches at the first render and holds (fits or capped)", func(t *testing.T) {
		state := freshStableState()
		cap := max(1, 24-widgetHeightReserve)
		mk := func(n int) []string { return nLines(n, func(i int) string { return fmt.Sprintf("n%d", i) }) }
		n10, n25, n30 := mk(10), mk(25), mk(30)
		out := applyStableHeightBound(n25, rowsPtr(24), state, "regime", widgetHeightReserve)
		eq(t, len(out), cap, "length")
		eq(t, out, n25[:cap], "head slice")
		eq(t, *state.stickyCap, cap, "stickyCap")
		eq(t, len(applyStableHeightBound(n30, rowsPtr(24), state, "regime", widgetHeightReserve)), cap, "still above")
		eq(t, len(applyStableHeightBound(n25, rowsPtr(24), state, "regime", widgetHeightReserve)), cap, "still above 25")
		eq(t, applyStableHeightBound(n10, rowsPtr(24), freshStableState(), "regime", widgetHeightReserve), n10, "first render fits")
	})
	tw(t, f, "applyStableHeightBound: fits case latches at the first-render natural and stays constant", func(t *testing.T) {
		state := freshStableState()
		mk := func(n int) []string { return nLines(n, func(i int) string { return fmt.Sprintf("line %d", i) }) }
		n13, n16, n10 := mk(13), mk(16), mk(10)
		eq(t, applyStableHeightBound(n13, rowsPtr(30), state, "regime", widgetHeightReserve), n13, "first render")
		eq(t, *state.stickyCap, 13, "fits case latches at the natural height")
		grown := applyStableHeightBound(n16, rowsPtr(30), state, "regime", widgetHeightReserve)
		eq(t, len(grown), 13, "growth is head-sliced")
		eq(t, grown, n16[:13], "growth head")
		shrunk := applyStableHeightBound(n10, rowsPtr(30), state, "regime", widgetHeightReserve)
		eq(t, len(shrunk), 13, "shrink is padded")
		eq(t, shrunk[:10], n10, "content")
		eq(t, shrunk[10:], []string{"", "", ""}, "padding")
		eq(t, applyStableHeightBound(n13, rowsPtr(30), state, "regime", widgetHeightReserve), n13, "back to committed")
	})
	tw(t, f, "applyStableHeightBound: pads deterministically when natural dips below the cap (cap crossing down)", func(t *testing.T) {
		state := freshStableState()
		cap := max(1, 24-widgetHeightReserve)
		applyStableHeightBound(make([]string, 25), rowsPtr(24), state, "regime", widgetHeightReserve)
		n12 := nLines(12, func(i int) string { return fmt.Sprintf("n%d", i) })
		padded := applyStableHeightBound(n12, rowsPtr(24), state, "regime", widgetHeightReserve)
		eq(t, len(padded), cap, "height stays at the committed cap")
		eq(t, padded[:12], n12, "content preserved")
		eq(t, padded[12:], make([]string, cap-12), "blank filler rows")
		n20 := nLines(20, func(i int) string { return fmt.Sprintf("n%d", i) })
		eq(t, len(applyStableHeightBound(n20, rowsPtr(24), state, "regime", widgetHeightReserve)), cap, "above")
		eq(t, len(applyStableHeightBound(make([]string, cap), rowsPtr(24), state, "regime", widgetHeightReserve)), cap, "exactly at cap")
	})
	tw(t, f, "applyStableHeightBound: terminal resize clears the latch and adapts to the new height", func(t *testing.T) {
		state := freshStableState()
		n22 := nLines(22, func(i int) string { return fmt.Sprintf("n%d", i) })
		n25 := nLines(25, func(i int) string { return fmt.Sprintf("n%d", i) })
		cap24 := max(1, 24-widgetHeightReserve)
		applyStableHeightBound(n25, rowsPtr(24), state, "regime", widgetHeightReserve)
		eq(t, *state.stickyCap, cap24, "latched")
		cap30 := max(1, 30-widgetHeightReserve)
		grown := applyStableHeightBound(n22, rowsPtr(30), state, "regime", widgetHeightReserve)
		eq(t, len(grown), min(len(n22), cap30), "grow reveals more of the widget")
		eq(t, *state.stickyCap, min(len(n22), cap30), "latch re-engages")
		shrunk := applyStableHeightBound(n25, rowsPtr(24), state, "regime", widgetHeightReserve)
		eq(t, len(shrunk), cap24, "re-latches at the 24-row cap")
	})
	tw(t, f, "applyStableHeightBound: regime change clears the latch so the new mode starts from its own height", func(t *testing.T) {
		state := freshStableState()
		big := nLines(25, func(i int) string { return fmt.Sprintf("n%d", i) })
		small := nLines(10, func(i int) string { return fmt.Sprintf("n%d", i) })
		cap := max(1, 24-widgetHeightReserve)
		applyStableHeightBound(big, rowsPtr(24), state, "expanded", widgetHeightReserve)
		eq(t, *state.stickyCap, cap, "latched")
		out := applyStableHeightBound(small, rowsPtr(24), state, "compact", widgetHeightReserve)
		eq(t, out, small, "compact starts from its own natural height")
		eq(t, *state.stickyCap, len(small), "latch re-engages at the first render of the new regime")
		out2 := applyStableHeightBound(big, rowsPtr(24), state, "compact", widgetHeightReserve)
		eq(t, len(out2), len(small), "compact holds its committed height")
	})
	tw(t, f, "applyStableHeightBound: unbounded without a terminal (mock/harness/status)", func(t *testing.T) {
		state := freshStableState()
		lines := nLines(40, func(i int) string { return fmt.Sprintf("n%d", i) })
		eq(t, applyStableHeightBound(lines, nil, state, "regime", widgetHeightReserve), lines, "no terminal -> unbounded")
		if state.stickyCap != nil || state.stickyTerminalRows != nil {
			t.Error("state touched without a terminal")
		}
	})

	manyTaskList := func(n, doneBelow int) *jsObject {
		tasks := make([]any, n)
		for i := range tasks {
			status := "pending"
			kv := []any{"createdAt", "2026-08-10T00:00:00Z", "updatedAt", "2026-08-10T00:00:00Z"}
			if i < doneBelow {
				status = "complete"
				kv = append(kv, "completedAt", "2026-08-10T00:01:00Z")
			}
			tasks[i] = wgTask("t"+itoa(i), "Task t"+itoa(i)+" with a reasonably long title for wrapping tests", status, kv...)
		}
		return wgObj("tasks", tasks, "blockCompletion", false, "proposedAt", "2026-08-10T00:00:00Z")
	}
	tw(t, f, "GoalWidgetComponent: rendered height is constant across goal-state changes (fits and capped)", func(t *testing.T) {
		stask := func(id, status string) *jsObject {
			kv := []any{"createdAt", "2026-08-10T00:00:00Z", "updatedAt", "2026-08-10T00:00:00Z"}
			if status == "complete" {
				kv = append(kv, "completedAt", "2026-08-10T00:01:00Z")
			}
			return wgTask(id, "Task "+id+" with a reasonably long title for wrapping tests", status, kv...)
		}
		tl := func(tasks ...any) *jsObject {
			return wgObj("tasks", tasks, "blockCompletion", false, "proposedAt", "2026-08-10T00:00:00Z")
		}
		current := with(wgGoal(), "taskList", tl(stask("t1", "pending")))
		var events []*jsObject
		tui, _ := createMockTUI(24)
		c := newWidget(tui, func() *jsObject { return current }, func(o *goalWidgetOptions) {
			o.getExpanded = func() bool { return true }
			o.getLedgerEvents = func() []*jsObject { return events }
		})
		cap := max(1, 24-widgetHeightReserve)
		heights := []int{}
		steps := []func(){
			func() {
				current = with(current, "taskList", tl(stask("t1", "complete"), stask("t2", "pending")))
				events = []*jsObject{wgObj("type", "task_complete", "at", "2026-08-10T00:01:00Z", "goalId", "g1", "taskId", "t1")}
			},
			func() {
				ts := make([]any, 12)
				for i := range ts {
					s := "pending"
					if i < 5 {
						s = "complete"
					}
					ts[i] = stask("t"+itoa(i), s)
				}
				current = with(current, "taskList", tl(ts...))
			},
			func() {
				current = with(current, "verificationContract", "Run npm test (0 failures) and re-read every requirement")
			},
			func() { current = with(current, "tokenBudget", 200000.0) },
			func() { current = with(current, "usage", wgObj("activeSeconds", 5000.0, "tokensUsed", 9000.0)) },
		}
		for _, step := range steps {
			step()
			heights = append(heights, len(c.Render(100)))
		}
		seen := map[int]bool{}
		for _, h := range heights {
			seen[h] = true
			if h > cap {
				t.Errorf("bounded by the cap: %v", heights)
			}
		}
		if len(seen) != 1 {
			t.Errorf("constant across all goal-state changes: %v", heights)
		}
	})
	tw(t, f, "GoalWidgetComponent: the first task appearing re-latches (task presence is part of the regime)", func(t *testing.T) {
		current := with(wgGoal(), "taskList", wgObj("tasks", []any{}, "blockCompletion", false, "proposedAt", "2026-08-10T00:00:00Z"))
		tui, _ := createMockTUI(40)
		c := newWidget(tui, func() *jsObject { return current }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
		h0 := len(c.Render(100))
		current = with(current, "taskList", wgObj("tasks", []any{wgTask("t1", "Task one", "pending")}, "blockCompletion", false, "proposedAt", "2026-08-10T00:00:00Z"))
		h1 := len(c.Render(100))
		h2 := len(c.Render(100))
		if h1 <= h0 {
			t.Errorf("first task grows the dashboard (%d -> %d)", h0, h1)
		}
		eq(t, h2, h1, "stable within the task-present regime")
	})
	tw(t, f, "GoalWidgetComponent: expanding the terminal reveals more of the widget, collapsing re-latches", func(t *testing.T) {
		current := with(wgGoal(), "taskList", manyTaskList(12, 5))
		tui, _ := createMockTUI(24)
		c := newWidget(tui, func() *jsObject { return current }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
		cap24 := max(1, 24-widgetHeightReserve)
		eq(t, len(c.Render(100)), cap24, "latched at 18 on a 24-row terminal")
		tui30, _ := createMockTUI(30)
		grown := newWidget(tui30, func() *jsObject { return current }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } }).Render(100)
		cap30 := max(1, 30-widgetHeightReserve)
		if len(grown) <= cap24 {
			t.Errorf("growing the terminal reveals more (%d > %d)", len(grown), cap24)
		}
		if len(grown) > cap30 {
			t.Errorf("still bounded by the new cap (%d <= %d)", len(grown), cap30)
		}
	})
	tw(t, f, "GoalWidgetComponent: measured dock chrome shrinks the widget so the block + chrome fits the terminal", func(t *testing.T) {
		current := with(wgGoal(), "taskList", manyTaskList(12, 5))
		editorLines := []string{""}
		container := &wgDockContainer{}
		tui := &wgDockTUI{rows: 15, children: []any{
			wgDockNode{func(int) []string { return []string{"chat 0", "chat 1", "chat 2"} }},
			wgDockNode{func(int) []string { return []string{"status"} }},
			container,
			wgDockNode{func(int) []string { return append([]string{"❯"}, editorLines...) }},
			wgDockNode{func(int) []string { return []string{"footer a", "footer b"} }},
		}}
		c := newWidget(tui, func() *jsObject { return current }, func(o *goalWidgetOptions) { o.getExpanded = func() bool { return true } })
		// Register the component inside its container so the measurement finds it.
		container.kids = append(container.kids, c)

		small := c.Render(100)
		eq(t, len(small), 9, "with a small editor the widget renders its natural height (9 < cap 10)")
		editorLines = []string{"l1", "l2", "l3"}
		tall := c.Render(100)
		eq(t, len(tall), 7, "with a tall editor the widget shrinks to 15 - (7 + 1) = 7 lines")
		if len(tall) >= len(small) {
			t.Error("taller chrome shrinks the widget")
		}
		editorLines = []string{""}
		back := c.Render(100)
		eq(t, len(back), 9, "chrome shrinking re-evaluates the latch up")
	})
}

var _ = reflect.DeepEqual
