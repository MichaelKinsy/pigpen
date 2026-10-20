package pi_goal_x

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Ported from goal-widget.ts (the terminal-input keybindings and the debug helpers). toggleTaskViaService, the third export of that
// file, needs the goal service and GoalCore and is ported with them.

const debugGoalsDir = ".pi/goals/debug"

var kittyRepeatOrRelease = regexp.MustCompile(`^[0-9:;]+:[23][u~ABCDHF]$`)

func isKittyRepeatOrRelease(data string) bool {
	return strings.HasPrefix(data, "\x1b[") && kittyRepeatOrRelease.MatchString(data[2:])
}

// isDebugEnabled: debug helpers are inert unless PI_GOAL_DEBUG is set (P1-13).
func isDebugEnabled() bool {
	v := os.Getenv("PI_GOAL_DEBUG")
	return v == "true" || v == "1"
}

// goalWidgetHandle is the live widget as the keybinding handler sees it (goalWidgetComponentRef.current).
type goalWidgetHandle interface {
	Invalidate()
	handleNavigationKey(key string) bool
	handleCompactScrollKey(key string) bool
}

// goalDebugCore is the part of GoalCore the debug helpers use.
type goalDebugCore interface {
	removeDebugFile(ctx goalExtensionContext, path string)
	writeDebugFile(ctx goalExtensionContext, path, content string)
	setGoal(ctx goalExtensionContext, goal *jsObject, persist bool, reason string)
	currentGoal() *jsObject
	clearStateGoal()
	focusedGoalID() *string
	deleteGoalByID(id string)
	assignFocusedGoalID(id *string)
	clearStoppedRuntimeState()
	updateUI(ctx goalExtensionContext)
	setAuditProgress(p *auditorWidgetProgress)
	auditProgressNow() *auditorWidgetProgress
}

// goalWidgetCore is the part of GoalCore syncTerminalInputPause reads.
type goalWidgetCore interface {
	goalDebugCore
	goalModalDepth() int
	auditProgress() *auditorWidgetProgress
	pauseActiveGoal(ctx goalExtensionContext)
	abortAudit(ctx goalExtensionContext)
	isDashboardExpanded() bool
	toggleDashboardExpanded()
	toggleGoalAuditor(ctx goalExtensionContext)
	widgetComponent() goalWidgetHandle
	terminalInputUnsubscribe() func()
	setTerminalInputUnsubscribe(f func())
	debugMode() bool
	setDebugMode(on bool)
}

// goalDashboardKeybindingsFor resolves the dashboard keybindings for a working directory (loadGoalSettings(cwd).keybindings.dashboard).
var goalDashboardKeybindingsFor = func(cwd string) goalDashboardKeybindings {
	if kb := loadGoalSettings(cwd).keybindings.dashboard; kb != (goalDashboardKeybindings{}) {
		return kb
	}
	return defaultGoalKeybindings.dashboard
}

var navigationKeys = []string{"up", "down", "pageUp", "pageDown", "home", "end"}

func anyKeyMatches(data string, keys ...string) bool {
	for _, k := range keys {
		if pitui.MatchesKey(data, k) {
			return true
		}
	}
	return false
}

// syncTerminalInputPause (re)registers the terminal input listener: Escape pause/abort-audit, the dashboard toggle and scroll chords,
// Ctrl+Shift+A and the hidden debug bindings. The handler reads live state through the core at event time.
func syncTerminalInputPause(core goalWidgetCore, ctx goalExtensionContext) {
	if !ctx.hasUI() {
		return
	}
	if u := core.terminalInputUnsubscribe(); u != nil {
		u()
	}
	goalCwd := ctx.cwd()
	if goalCwd == "" {
		if wd, err := os.Getwd(); err == nil {
			goalCwd = wd
		}
	}
	consume := &goalInputResult{consume: true}
	core.setTerminalInputUnsubscribe(ctx.ui().onTerminalInput(func(data string) *goalInputResult {
		// Any goal-owned modal owns every key while it is open: never intercept.
		if core.goalModalDepth() > 0 {
			return nil
		}
		keybindings := goalDashboardKeybindingsFor(goalCwd)
		if isKittyRepeatOrRelease(data) {
			goalShortcut := pitui.MatchesKey(data, keybindings.toggleExpand) || pitui.MatchesKey(data, "ctrl+shift+a") ||
				(pitui.MatchesKey(data, "escape") && (core.isDashboardExpanded() || core.currentGoal() != nil)) ||
				(core.isDashboardExpanded() && anyKeyMatches(data, navigationKeys...)) ||
				(!core.isDashboardExpanded() && core.widgetComponent() != nil &&
					anyKeyMatches(data, keybindings.scrollUp, keybindings.scrollDown, "ctrl+shift+pageUp", "ctrl+shift+pageDown", "ctrl+shift+home", "ctrl+shift+end"))
			if goalShortcut {
				return consume
			}
			return nil
		}
		// If an audit is running, Escape aborts the audit instead of pausing.
		if pitui.MatchesKey(data, "escape") && core.auditProgress() != nil {
			core.abortAudit(ctx)
			return consume
		}
		// §10: Escape collapses the expanded dashboard before it can pause the goal.
		if pitui.MatchesKey(data, "escape") && core.isDashboardExpanded() {
			core.toggleDashboardExpanded()
			return consume
		}
		// Escape on a live goal: pause it, then pass the key back to pi (nil, not consumed) so pi aborts the running turn too.
		if g := core.currentGoal(); pitui.MatchesKey(data, "escape") && g != nil && gstr(g, "status") == "active" && gflag(g, "autoContinue") {
			core.pauseActiveGoal(ctx)
			return nil
		}
		// Ctrl+Shift+T — toggle the unified dashboard between compact and expanded task views (§10, §19.5).
		if pitui.MatchesKey(data, keybindings.toggleExpand) {
			core.toggleDashboardExpanded()
			return consume
		}
		// Ctrl+Shift+A — toggle the focused goal's independent auditor.
		if pitui.MatchesKey(data, "ctrl+shift+a") {
			core.toggleGoalAuditor(ctx)
			if w := core.widgetComponent(); w != nil {
				w.Invalidate()
			}
			return consume
		}
		// Navigation keys: plain arrows scroll the expanded dashboard (modal while open).
		if anyKeyMatches(data, navigationKeys...) {
			key := "end"
			for _, k := range navigationKeys {
				if pitui.MatchesKey(data, k) {
					key = k
					break
				}
			}
			if w := core.widgetComponent(); w != nil && w.handleNavigationKey(key) {
				return consume
			}
		}
		// Ctrl+Shift chords scroll the compact task list, consumed only when it overflows (§9.6).
		scrollKeys := []string{keybindings.scrollUp, keybindings.scrollDown, "ctrl+shift+pageUp", "ctrl+shift+pageDown", "ctrl+shift+home", "ctrl+shift+end"}
		scrollNames := []string{"up", "down", "pageUp", "pageDown", "home", "end"}
		for i, k := range scrollKeys {
			if pitui.MatchesKey(data, k) {
				if w := core.widgetComponent(); w != nil && w.handleCompactScrollKey(scrollNames[i]) {
					return consume
				}
				break
			}
		}

		// Debug keybindings are inert unless PI_GOAL_DEBUG is set (P1-13).
		if !isDebugEnabled() {
			return nil
		}
		if pitui.MatchesKey(data, "ctrl+shift+x") {
			core.setDebugMode(!core.debugMode())
			if core.debugMode() {
				ctx.ui().notify("Debug mode ON", "info")
			} else {
				ctx.ui().notify("Debug mode OFF", "info")
			}
			if w := core.widgetComponent(); w != nil {
				w.Invalidate()
			}
			return consume
		}
		if !core.debugMode() {
			return nil
		}
		switch {
		case pitui.MatchesKey(data, "ctrl+shift+n"):
			createDebugGoal(core, ctx)
			return consume
		case pitui.MatchesKey(data, "ctrl+shift+t"):
			injectDebugTasks(core, ctx)
			return consume
		case pitui.MatchesKey(data, "ctrl+shift+r"):
			startMockAudit(core)
			return consume
		case pitui.MatchesKey(data, "ctrl+shift+o"):
			openDebugProposal(ctx)
			return consume
		}
		return nil
	}))
}

var debugGoalCounter int
var debugMockAuditStop chan struct{}

func debugTask(id, title, status string, completedAt string, subtasks []any) *jsObject {
	t := newObject()
	t.set("id", id)
	t.set("title", title)
	t.set("status", status)
	if completedAt != "" {
		t.set("completedAt", completedAt)
	}
	if subtasks != nil {
		t.set("subtasks", subtasks)
	}
	return t
}

func debugSampleTasks(now string) []any {
	return []any{
		debugTask("t1", "Set up project structure", "complete", now, []any{
			debugTask("t1a", "Initialize repo", "complete", now, nil),
			debugTask("t1b", "Add build config", "pending", "", nil),
		}),
		debugTask("t2", "Implement core feature", "pending", "", nil),
		debugTask("t3", "Write tests", "pending", "", nil),
	}
}

func debugGoalID() string {
	return "debug-" + strings.NewReplacer(":", "-", ".", "-").Replace(nowIso()) + "-" + strconv.Itoa(debugGoalCounter)
}

func debugTaskList(tasks []any, now string) *jsObject {
	tl := newObject()
	tl.set("tasks", tasks)
	tl.set("blockCompletion", false)
	tl.set("proposedAt", now)
	return tl
}

// createDebugGoal toggles a test goal: create (first press) or remove (second press).
func createDebugGoal(core goalDebugCore, ctx goalExtensionContext) {
	if !isDebugEnabled() {
		return
	}
	prev := core.currentGoal()
	if prev != nil && strings.HasPrefix(gstr(prev, "id"), "debug-") {
		core.removeDebugFile(ctx, debugGoalsDir+"/debug_goal.md")
		prevID := gstr(prev, "id")
		core.clearStateGoal()
		if f := core.focusedGoalID(); f != nil && *f == prevID {
			core.deleteGoalByID(prevID)
			core.assignFocusedGoalID(nil)
		}
		core.clearStoppedRuntimeState()
		core.updateUI(ctx)
		ctx.ui().notify("Debug goal removed", "info")
		return
	}
	debugGoalCounter++
	goal := createGoal("=== Goal ===\nObjective: Debug test goal", true, false, false, clockMs())
	goal.set("id", debugGoalID())
	goal.set("createdAt", nowIso())
	goal.set("updatedAt", nowIso())
	path := debugGoalsDir + "/debug_goal.md"
	goal.set("activePath", path)
	core.writeDebugFile(ctx, path, serializeGoalFile(goal))
	core.setGoal(ctx, goal, false, "created")
	ctx.ui().notify("Debug goal created: "+gstr(goal, "id"), "info")
}

// injectDebugTasks injects 3 sample tasks into the current goal.
func injectDebugTasks(core goalDebugCore, ctx goalExtensionContext) {
	if !isDebugEnabled() {
		return
	}
	cur := core.currentGoal()
	if cur == nil {
		ctx.ui().notify("No goal to inject tasks into; create one first (Ctrl+Shift+N)", "warning")
		return
	}
	now := nowIso()
	next := cloneGoal(cur)
	next.set("taskList", debugTaskList(debugSampleTasks(now), now))
	next.set("updatedAt", now)
	core.setGoal(ctx, next, true, "")
	ctx.ui().notify("Sample tasks injected (3 tasks, 1 completed)", "info")
}

func stopMockAuditTimer() {
	if debugMockAuditStop != nil {
		close(debugMockAuditStop)
		debugMockAuditStop = nil
	}
}

type mockAuditPhase struct {
	phase      string
	atMs       int64
	label      string
	percentage float64
}

// startMockAudit starts a mock completion audit that transitions through phases.
func startMockAudit(core goalWidgetCore) {
	if !isDebugEnabled() {
		return
	}
	stopMockAuditTimer()
	startedAt := time.Now().UnixMilli()
	phases := []mockAuditPhase{
		{"tool_executing", 0, "Checking test results...", 10},
		{"tool_executing", 800, "Verifying requirements...", 30},
		{"thinking", 1800, "Evaluating completion criteria...", 60},
		{"producing_report", 3200, "Writing audit report...", 85},
		{"done", 4800, "Audit complete", 100},
	}
	core.setAuditProgress(&auditorWidgetProgress{recentOutput: []string{}, phase: "running"})
	invalidate := func() {
		if w := core.widgetComponent(); w != nil {
			w.Invalidate()
		}
	}
	invalidate()
	stop := make(chan struct{})
	debugMockAuditStop = stop
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			elapsed := time.Now().UnixMilli() - startedAt
			currentPhase, currentLabel, currentPct := "done", "Audit complete", 100.0
			for i := len(phases) - 1; i >= 0; i-- {
				if elapsed >= phases[i].atMs {
					currentPhase, currentLabel, currentPct = phases[i].phase, phases[i].label, phases[i].percentage
					break
				}
			}
			recent := []string{}
			if p := core.auditProgressNow(); p != nil {
				recent = p.recentOutput
			}
			progress := &auditorWidgetProgress{phase: currentPhase, label: currentLabel, percentage: &currentPct, elapsedMs: float64(elapsed), recentOutput: recent}
			core.setAuditProgress(progress)
			if currentPhase == "done" {
				progress.recentOutput = []string{"✓ All requirements verified", "✓ Tests pass: 310/310", "✓ No truncation cap remaining"}
				time.AfterFunc(3*time.Second, func() {
					core.setAuditProgress(nil)
					invalidate()
				})
				invalidate()
				return
			}
			invalidate()
		}
	}()
}

const debugProposalObjective = `=== Goal ===
Objective: Add collapsible task sections to the goal widget so large task lists are navigable

Success criteria:
- Tasks are grouped into sections by status (pending, active, complete) with visible section headers
- Each section header is toggleable — clicking it expands or collapses that section
- When collapsed, the section shows a header line only with a task count badge
- When expanded, tasks render with normal indentation and per-line styling
- Default state: pending section expanded, active and complete sections collapsed
- Section state is tracked per-render (no persistence needed)
- All 310 existing tests still pass

Boundaries:
- In scope: GoalWidgetComponent.render() grouping logic, section header toggling, expand/collapse state per render cycle
- Out of scope: task reordering, drag-and-drop, keyboard navigation for sections, persistence of section state across pi restarts
- Out of scope: modifying GoalTask or GoalRecord types

Constraints:
- Render width must respect the existing width parameter — no hardcoded widths
- Section collapse state is a render-only map, not stored in goal record
- Collapse toggle must be keyboard-accessible via existing widget interaction model
- Do not change the GoalWidgetComponent public API (constructor options, render signature)
- Section headers must use theme.fg("accent", ...) consistent with existing render patterns

Verification contract:
- Run npm test and confirm 310/310 pass (0 failures)
- Read render method and confirm task grouping logic exists
- Read expand/collapse toggle handler and confirm it inverts section state
- Confirm collapsed sections only render the header line with task count
- Confirm expanded sections render tasks with correct indentation and styling`

// openDebugProposal builds a fresh debug goal with a sample task list in memory and notifies (the legacy proposal dialog is gone).
func openDebugProposal(ctx goalExtensionContext) {
	if !isDebugEnabled() {
		return
	}
	debugGoalCounter++
	goal := createGoal(debugProposalObjective, true, false, false, clockMs())
	goal.set("id", debugGoalID())
	goal.set("createdAt", nowIso())
	goal.set("updatedAt", nowIso())
	now := nowIso()
	tasks := []any{
		debugTask("t1", "Set up project structure", "complete", now, []any{
			debugTask("t1a", "Initialize repo", "complete", now, nil),
			debugTask("t1b", "Add build config", "pending", "", nil),
		}),
		debugTask("t2", "Implement core feature", "pending", "", []any{
			debugTask("t2a", "Status grouping logic", "pending", "", nil),
			debugTask("t2b", "Section header component", "pending", "", nil),
			debugTask("t2c", "Expand/collapse state", "pending", "", nil),
			debugTask("t2d", "Task count badge", "pending", "", nil),
		}),
		debugTask("t3", "Update tests", "pending", "", nil),
		debugTask("t4", "Manual TUI verification", "pending", "", nil),
	}
	goal.set("taskList", debugTaskList(tasks, now))
	ctx.ui().notify("Injected sample task list into debug goal "+gstr(goal, "id")+"; inspect the goal file or use the debug task overlay.", "info")
}
