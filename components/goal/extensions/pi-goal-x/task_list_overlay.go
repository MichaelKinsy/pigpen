package pi_goal_x

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Ported from widgets/task-list-overlay.ts.

// overlayEntry is the TS LineEntry union; kind is separator | goal-header | task-summary | task | empty-message.
type overlayEntry struct {
	kind   string
	icon   string // goal-header
	title  string // goal-header, task
	status string // goal-header: status label; task: pending | complete | skipped
	text   string // task-summary, empty-message
	goalID string // task
	taskID string // task
	prefix string // task
}

// overlayToggleResult is the result of onToggleTask.
type overlayToggleResult struct {
	ok      bool
	message string
}

// taskListOverlayOptions is the options argument of showTaskListOverlay.
type taskListOverlayOptions struct {
	onToggleTask func(goalID, taskID string) overlayToggleResult
}

// overlayClearDelay is the 2500 ms the toggle-failure message stays on screen.
var overlayClearDelay = 2500 * time.Millisecond

// showTaskListOverlay shows a scrollable modal overlay displaying the current goal's task list. 'a' toggles between the current goal
// and all open goals. focusedGoalID is nil when no goal is focused.
func showTaskListOverlay(ctx goalExtensionContext, goalsByID *goalPool, focusedGoalID *string, opts taskListOverlayOptions) {
	if !ctx.hasUI() {
		return
	}
	ctx.ui().custom(func(t goalTUI, theme goalTheme, done func(any)) goalComponent {
		o := &taskListOverlay{tui: t, theme: theme, done: done, goalsByID: goalsByID, focusedGoalID: focusedGoalID, opts: opts, cursorEntryIndex: -1, lastRenderWidth: 80}
		o.rebuildEntries()
		o.wasHardwareCursorShown = t.getShowHardwareCursor()
		t.setShowHardwareCursor(false)
		return o
	}, goalCustomOptions{overlay: true, anchor: "center", width: "80%", minWidth: 60, maxHeight: "80%"})
}

type taskListOverlay struct {
	mu    sync.Mutex
	tui   goalTUI
	theme goalTheme
	done  func(any)
	opts  taskListOverlayOptions

	goalsByID     *goalPool
	focusedGoalID *string

	showAllGoals     bool
	entries          []overlayEntry
	totalOpenGoals   int
	totalTaskCount   int
	cursorEntryIndex int
	cursorMessage    string

	scrollOffset      int
	lastRenderWidth   int
	renderedLineCount int

	wasHardwareCursorShown bool
}

func (o *taskListOverlay) accent(s string) string { return o.theme.fg("accent", s) }
func (o *taskListOverlay) dim(s string) string    { return o.theme.fg("dim", s) }
func (o *taskListOverlay) muted(s string) string  { return o.theme.fg("muted", s) }
func (o *taskListOverlay) bold(s string) string   { return o.theme.bold(s) }

func (o *taskListOverlay) rebuildEntries() {
	o.entries = nil
	o.totalOpenGoals = 0
	o.totalTaskCount = 0

	openGoals := openGoalsFromPool(o.goalsByID)
	var targetGoals []*jsObject
	if o.showAllGoals {
		targetGoals = openGoals
	} else {
		for _, g := range openGoals {
			if o.focusedGoalID != nil && gstr(g, "id") == *o.focusedGoalID {
				targetGoals = append(targetGoals, g)
			}
		}
	}

	if len(targetGoals) == 0 {
		o.entries = append(o.entries, overlayEntry{kind: "empty-message", text: "No open goals to display."})
		return
	}

	for gIdx, goal := range targetGoals {
		taskList := goal.obj("taskList")
		var tasks []any
		if taskList != nil {
			tasks, _ = taskList.vals["tasks"].([]any)
			o.totalTaskCount += countAllTasks(tasks)
		}
		if gIdx > 0 {
			o.entries = append(o.entries, overlayEntry{kind: "separator"})
		}
		icon := "●"
		if gstr(goal, "status") == "paused" {
			icon = "⏸"
		} else if gflag(goal, "sisyphus") {
			icon = "◆"
		}
		o.entries = append(o.entries, overlayEntry{kind: "goal-header", icon: icon, title: displayObjectiveTitle(gstr(goal, "objective")), status: statusLabel(goal)})

		if taskList != nil && len(tasks) > 0 {
			c := countTaskSubtree(tasks)
			summary := strconv.Itoa(c.complete) + "/" + strconv.Itoa(c.total) + " done"
			if c.skipped > 0 {
				summary += " (" + strconv.Itoa(c.skipped) + " skipped)"
			}
			o.entries = append(o.entries, overlayEntry{kind: "task-summary", text: summary})
			for i, t := range tasks {
				if task, _ := t.(*jsObject); task != nil {
					o.collectTaskEntries(task, 1, i == len(tasks)-1, gstr(goal, "id"))
				}
			}
		} else {
			o.entries = append(o.entries, overlayEntry{kind: "empty-message", text: "(no tasks)"})
		}
		o.totalOpenGoals++
	}

	// F3: reset the cursor to the first task row after a rebuild.
	o.cursorEntryIndex = -1
	for i, e := range o.entries {
		if e.kind == "task" {
			o.cursorEntryIndex = i
			break
		}
	}
	o.cursorMessage = ""
}

func (o *taskListOverlay) collectTaskEntries(task *jsObject, depth int, isLast bool, goalID string) {
	branch := "├─"
	if isLast {
		branch = "└─"
	}
	statusIcon := "◌"
	switch gstr(task, "status") {
	case "complete":
		statusIcon = "✓"
	case "skipped":
		statusIcon = "—"
	}
	indent := "   " + strings.Repeat("  ", depth-1)
	prefix := indent + " " + branch + " " + statusIcon
	o.entries = append(o.entries, overlayEntry{kind: "task", goalID: goalID, taskID: gstr(task, "id"), status: gstr(task, "status"), prefix: prefix, title: gstr(task, "title")})
	if subs, _ := task.vals["subtasks"].([]any); len(subs) > 0 {
		for i, s := range subs {
			if st, _ := s.(*jsObject); st != nil {
				o.collectTaskEntries(st, depth+1, i == len(subs)-1, goalID)
			}
		}
	}
}

func overlayFitLine(l string, innerWidth int) string {
	if visibleWidth(l) > innerWidth {
		return truncateToWidth(l, innerWidth, "…", false)
	}
	return l
}

// renderEntry renders a single line entry into styled wrapped lines.
func (o *taskListOverlay) renderEntry(entry overlayEntry, innerWidth int) []string {
	switch entry.kind {
	case "separator":
		return []string{o.dim("·")}
	case "goal-header":
		icon := o.accent("●")
		switch entry.icon {
		case "⏸":
			icon = o.muted("⏸")
		case "◆":
			icon = o.accent("◆")
		}
		status := o.dim(entry.status)
		_ = status
		rawPrefix := entry.icon + "  "
		prefixWidth := visibleWidth(rawPrefix)
		available := innerWidth - prefixWidth
		if available < 1 {
			available = 1
		}
		wrappedTitle := wrapTextWithAnsi(o.bold(entry.title), available)
		pad := strings.Repeat(" ", utf16Len(rawPrefix)-3)
		lines := []string{}
		for i, segment := range wrappedTitle {
			if i == 0 {
				lines = append(lines, icon+"  "+segment+"  "+o.dim(entry.status))
			} else {
				lines = append(lines, "   "+pad+segment)
			}
		}
		if visibleWidth(lines[0]) > innerWidth {
			// Status overflowed — put status on a separate dim line.
			lines[0] = icon + "  " + wrappedTitle[0]
			if len(wrappedTitle) == 1 {
				lines = append(lines, "   "+o.dim(entry.status))
			} else {
				lastLine := wrappedTitle[len(wrappedTitle)-1]
				lines[len(lines)-1] = "   " + pad + lastLine
				lines = append(lines, "   "+o.dim(entry.status))
			}
		}
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = overlayFitLine(l, innerWidth)
		}
		return out
	case "task-summary", "empty-message":
		return []string{overlayFitLine(o.dim(entry.text), innerWidth)}
	case "task":
		prefixWidth := visibleWidth(entry.prefix)
		available := innerWidth - prefixWidth
		if available < 1 {
			available = 1
		}
		wrappedTitle := wrapTextWithAnsi(entry.title, available)
		lines := make([]string, 0, len(wrappedTitle))
		for i, segment := range wrappedTitle {
			if i == 0 {
				lines = append(lines, o.dim(entry.prefix)+" "+segment)
			} else {
				lines = append(lines, strings.Repeat(" ", prefixWidth+1)+segment)
			}
		}
		for i, l := range lines {
			lines[i] = overlayFitLine(l, innerWidth)
		}
		return lines
	}
	return []string{}
}

func overlayVisibleHeight(innerWidth int) int {
	h := int(math.Floor(float64(innerWidth) / 2.8))
	if h < 8 {
		h = 8
	}
	return h
}

func (o *taskListOverlay) Dispose() { o.tui.setShowHardwareCursor(o.wasHardwareCursorShown) }

func (o *taskListOverlay) Invalidate() {}

func (o *taskListOverlay) Render(width int) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastRenderWidth = width

	termWidth := width
	if termWidth > 100 {
		termWidth = 100
	}
	innerWidth := dialogInnerWidth(termWidth, 90)
	p := "  "
	line := func(content string) string { return borderedLine(o.accent, innerWidth, content) }

	renderedLines := []string{}
	for _, entry := range o.entries {
		renderedLines = append(renderedLines, o.renderEntry(entry, innerWidth)...)
	}
	o.renderedLineCount = len(renderedLines)

	horiz := horizontalRule(innerWidth)
	out := []string{o.accent("┌" + horiz + "┐")}

	viewLabel := "current goal"
	if o.showAllGoals {
		word := "goals"
		if o.totalOpenGoals == 1 {
			word = "goal"
		}
		viewLabel = strconv.Itoa(o.totalOpenGoals) + " " + word
	}
	taskWord := "tasks"
	if o.totalTaskCount == 1 {
		taskWord = "task"
	}
	h := o.bold(" Tasks (" + viewLabel + ", " + strconv.Itoa(o.totalTaskCount) + " " + taskWord + ")")
	out = append(out, line(p+h))
	out = append(out, o.accent("├"+horiz+"┤"))

	visibleHeight := overlayVisibleHeight(innerWidth)
	maxOffset := o.renderedLineCount - visibleHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if o.scrollOffset > maxOffset {
		o.scrollOffset = maxOffset
	}
	canScrollUp := o.scrollOffset > 0
	canScrollDown := o.scrollOffset < maxOffset

	if canScrollUp {
		out = append(out, line(p+o.dim("▴  "+strconv.Itoa(o.scrollOffset)+"/"+strconv.Itoa(o.renderedLineCount)+" lines")))
	}
	end := o.scrollOffset + visibleHeight
	if o.renderedLineCount < end {
		end = o.renderedLineCount
	}
	for i := o.scrollOffset; i < end; i++ {
		out = append(out, line(p+overlayFitLine(renderedLines[i], innerWidth)))
	}
	if canScrollDown {
		out = append(out, line(p+o.dim("▾  "+strconv.Itoa(o.renderedLineCount-end)+" more lines")))
	}
	if o.renderedLineCount == 0 {
		out = append(out, line(p+o.dim("No tasks to display.")))
	}

	out = append(out, o.accent("├"+horiz+"┤"))
	toggleHint := "show all"
	if o.showAllGoals {
		toggleHint = "show current"
	}
	interactive := ""
	if o.opts.onToggleTask != nil {
		interactive = "  ·  Enter toggle task"
	}
	footer := o.dim("↑↓/jk scroll  ·  'a' to " + toggleHint + "  ·  Esc close" + interactive)
	out = append(out, line(p+footer))
	if o.cursorMessage != "" {
		out = append(out, line(p+o.theme.fg("warning", o.cursorMessage)))
	}
	out = append(out, o.accent("└"+horiz+"┘"))
	return out
}

func (o *taskListOverlay) HandleInput(data string) {
	o.mu.Lock()
	tw := o.lastRenderWidth
	if tw > 100 {
		tw = 100
	}
	innerW := tw
	if innerW > 90 {
		innerW = 90
	}
	innerW -= 2
	visibleH := overlayVisibleHeight(innerW)
	maxO := o.renderedLineCount - visibleH
	if maxO < 0 {
		maxO = 0
	}

	switch {
	case pitui.MatchesKey(data, "up") || pitui.MatchesKey(data, "k"):
		o.scrollOffset = max(0, o.scrollOffset-1)
		if o.opts.onToggleTask != nil {
			for i := o.cursorEntryIndex - 1; i >= 0; i-- {
				if o.entries[i].kind == "task" {
					o.cursorEntryIndex = i
					break
				}
			}
		}
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "down") || pitui.MatchesKey(data, "j"):
		o.scrollOffset = min(maxO, o.scrollOffset+1)
		if o.opts.onToggleTask != nil {
			for i := o.cursorEntryIndex + 1; i < len(o.entries); i++ {
				if o.entries[i].kind == "task" {
					o.cursorEntryIndex = i
					break
				}
			}
		}
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "pageUp"):
		o.scrollOffset = max(0, o.scrollOffset-visibleH)
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "pageDown"):
		o.scrollOffset = min(maxO, o.scrollOffset+visibleH)
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "home"):
		o.scrollOffset = 0
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "end"):
		o.scrollOffset = maxO
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "a"):
		o.showAllGoals = !o.showAllGoals
		o.rebuildEntries()
		o.scrollOffset = 0
		o.mu.Unlock()
		o.tui.requestRender()
	case pitui.MatchesKey(data, "enter") && o.opts.onToggleTask != nil:
		var cursor *overlayEntry
		if o.cursorEntryIndex >= 0 && o.cursorEntryIndex < len(o.entries) {
			e := o.entries[o.cursorEntryIndex]
			cursor = &e
		}
		o.mu.Unlock()
		if cursor != nil && cursor.kind == "task" {
			go o.toggleTask(cursor.goalID, cursor.taskID)
			return
		}
		o.done(nil)
	case pitui.MatchesKey(data, "escape") || pitui.MatchesKey(data, "enter"):
		o.mu.Unlock()
		o.done(nil)
	default:
		o.mu.Unlock()
	}
}

func (o *taskListOverlay) toggleTask(goalID, taskID string) {
	result := o.opts.onToggleTask(goalID, taskID)
	if result.ok {
		o.mu.Lock()
		o.rebuildEntries()
		o.mu.Unlock()
		o.tui.requestRender()
		return
	}
	msg := result.message
	if msg == "" {
		msg = "Toggle failed."
	}
	o.mu.Lock()
	o.cursorMessage = msg
	o.mu.Unlock()
	o.tui.requestRender()
	time.AfterFunc(overlayClearDelay, func() {
		o.mu.Lock()
		o.cursorMessage = ""
		o.mu.Unlock()
		o.tui.requestRender()
	})
}
