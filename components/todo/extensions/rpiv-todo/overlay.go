package rpiv_todo

import (
	"fmt"
	"strings"
	"sync"
)

// widgetSink is what the overlay pushes rows to (the host's widget). A nil slice clears the widget.
type widgetSink interface {
	SetWidgetLines(key string, lines []string) error
}

// viewEnv is what one render or update needs from the host: where rows go, the live theme, the width and
// Pi's tool-output expansion state.
type viewEnv struct {
	sink          widgetSink
	theme         themer
	width         int
	toolsExpanded bool
}

// hostWidgetRows is how many rows PiG shows of a string[] widget (Pi interactive-mode.ts:2321-2336). The
// overlay of the original is a component widget, which the Go SDK cannot express (PORT.md, SDK gap G1), so
// the rows are pre-rendered and the production overlay keeps its budget inside this limit.
const hostWidgetRows = 10

// overlay is the lifecycle controller of the persistent todo widget: register once, refresh by pushing
// again, collapse instead of scroll, follow Pi's tool-output expansion mode, hide when empty.
// upstream: todo-overlay.ts. A subprocess has no TUI handle (requestRender), so every refresh pushes the
// rows again, laid out at the width the host reported.
type overlay struct {
	st     *store
	rowCap int // 0: no cap; else the most rows the host shows (the body budget shrinks to fit)

	mu              sync.Mutex
	bound           bool
	registered      bool
	collapsed       bool
	pendingHide     map[int]bool
	hidden          map[int]bool
	lastNextID      int
	lastNextIDKnown bool
}

func newOverlay(st *store, rowCap int) *overlay {
	return &overlay{st: st, rowCap: rowCap, pendingHide: map[int]bool{}, hidden: map[int]bool{}}
}

// bind attaches the overlay to a (new) UI: a rebind invalidates the registration so the next update
// registers again. upstream: todo-overlay.ts:38-46 (setUICtx).
func (o *overlay) bind() {
	o.mu.Lock()
	o.bound = true
	o.registered = false
	o.mu.Unlock()
}

// snapshot reads the foreground slot and keeps the completed-display tracking in step with it: a nextId
// that went back (clear, or a replay of an older branch) resets it, and ids no longer completed leave both
// sets. upstream: todo-overlay.ts:122-135 (getSnapshot). Callers hold o.mu.
func (o *overlay) snapshot() taskState {
	state := o.st.getRenderState()
	if o.lastNextIDKnown && state.NextID < o.lastNextID {
		o.resetLocked()
	}
	o.lastNextID, o.lastNextIDKnown = state.NextID, true
	completed := map[int]bool{}
	for _, t := range state.Tasks {
		if t.Status == statusCompleted {
			completed[t.ID] = true
		}
	}
	for id := range o.pendingHide {
		if !completed[id] {
			delete(o.pendingHide, id)
		}
	}
	for id := range o.hidden {
		if !completed[id] {
			delete(o.hidden, id)
		}
	}
	return taskState{Tasks: append([]task{}, state.Tasks...), NextID: state.NextID}
}

func (o *overlay) overlayTasks(s taskState) []task {
	return filterTasks(s.Tasks, func(t task) bool {
		return t.Status != statusDeleted && !(t.Status == statusCompleted && o.hidden[t.ID])
	})
}

// update refreshes the widget: clears it when nothing is visible, else pushes the rows. A no-op until bound.
func (o *overlay) update(env viewEnv) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.updateLocked(env)
}

func (o *overlay) updateLocked(env viewEnv) {
	if !o.bound {
		return
	}
	snap := o.snapshot()
	if len(o.overlayTasks(snap)) == 0 {
		if o.registered {
			_ = env.sink.SetWidgetLines(widgetKey, nil)
			o.registered = false
		}
		return
	}
	o.pushLocked(env)
	o.registered = true
}

// pushLocked renders and sends the rows; an empty render clears the widget (the original's widget stays
// registered but draws nothing).
func (o *overlay) pushLocked(env viewEnv) {
	lines := o.renderLocked(env)
	if len(lines) == 0 {
		lines = nil
	}
	_ = env.sink.SetWidgetLines(widgetKey, lines)
}

// render is the widget's row layout at env.width. upstream: todo-overlay.ts:146-205 (renderWidget).
func (o *overlay) render(env viewEnv) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.renderLocked(env)
}

func (o *overlay) renderLocked(env viewEnv) []string {
	snap := o.snapshot()
	tasks := o.overlayTasks(snap)
	if len(tasks) == 0 {
		return []string{}
	}
	theme := env.theme
	state := taskState{Tasks: tasks, NextID: snap.NextID}
	truncate := func(line string) string { return truncateToWidth(line, env.width, "…") }
	counts := selectTodoCounts(state)
	showIDs := selectShowTaskIDs(state)

	color, icon := "dim", "○"
	if selectHasActive(state) {
		color, icon = "accent", "●"
	}
	headingText := fmt.Sprintf("%s (%d/%d)", tr("overlay.heading", "Todos"), counts.Completed, counts.Total)
	heading := truncate(theme.Fg(color, icon) + " " + theme.Fg(color, headingText))

	if o.collapsed {
		hint := tr("overlay.collapsed", "collapsed")
		if key := resolveCollapseKey(); key != collapseKeyOff {
			hint = strings.Replace(tr("overlay.expandHint", "{key} to expand"), "{key}", key, 1)
		}
		return withTrailingSpacer([]string{heading, truncate(theme.Fg("dim", "└─") + " " + theme.Fg("dim", hint))})
	}

	lines := []string{heading}
	maxLines := getMaxWidgetLines()
	if o.rowCap > 0 && maxLines > o.rowCap {
		maxLines = o.rowCap
	}
	budget := maxLines - 1
	if env.toolsExpanded {
		budget = len(tasks)
		if o.rowCap > 0 && budget > o.rowCap-1 {
			budget = o.rowCap - 1
		}
	}
	layout := selectOverlayLayout(state, budget)
	for _, t := range layout.Visible {
		lines = append(lines, truncate(theme.Fg("dim", "├─")+" "+formatOverlayTaskLine(t, theme, showIDs)))
	}

	for _, t := range tasks {
		if t.Status == statusCompleted && !o.pendingHide[t.ID] && !o.hidden[t.ID] {
			o.pendingHide[t.ID] = true
		}
	}

	if layout.HiddenCompleted == 0 && layout.TruncatedTail == 0 {
		last := len(lines) - 1
		lines[last] = strings.Replace(lines[last], "├─", "└─", 1)
		return withTrailingSpacer(lines)
	}
	total := layout.HiddenCompleted + layout.TruncatedTail
	var parts []string
	if layout.HiddenCompleted > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", layout.HiddenCompleted, formatStatusLabel(statusCompleted)))
	}
	if layout.TruncatedTail > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", layout.TruncatedTail, formatStatusLabel(statusPending)))
	}
	more := tr("overlay.more", "more")
	summary := fmt.Sprintf("+%d %s (%s)", total, more, strings.Join(parts, ", "))
	lines = append(lines, truncate(theme.Fg("dim", "└─")+" "+theme.Fg("dim", summary)))
	return withTrailingSpacer(lines)
}

// withTrailingSpacer appends a blank row so the panel is not flush against the editor box.
func withTrailingSpacer(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	return append(lines, "")
}

func (o *overlay) resetLocked() {
	o.pendingHide = map[int]bool{}
	o.hidden = map[int]bool{}
	o.lastNextID, o.lastNextIDKnown = 0, false
}

// resetCompletedDisplayState lets replayed completed tasks be shown once again.
func (o *overlay) resetCompletedDisplayState() {
	o.mu.Lock()
	o.resetLocked()
	o.mu.Unlock()
}

// hideCompletedTasksFromPreviousTurn is the agent_start step: completed tasks shown during the previous
// turn leave the widget. A no-op when none is pending.
func (o *overlay) hideCompletedTasksFromPreviousTurn(env viewEnv) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pendingHide) == 0 {
		return
	}
	for id := range o.pendingHide {
		o.hidden[id] = true
	}
	o.pendingHide = map[int]bool{}
	if o.registered {
		o.pushLocked(env)
	}
}

// toggleCollapse flips the collapsed view and pushes the new shape.
func (o *overlay) toggleCollapse(env viewEnv) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.collapsed = !o.collapsed
	if o.registered {
		o.pushLocked(env)
	}
}

func (o *overlay) isRegistered() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.registered
}

// dispose clears the widget (even one that was never registered, as the original does) and forgets the UI.
// A failing clear is returned after the state is reset, like the original's try/finally.
func (o *overlay) dispose(sink widgetSink) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var err error
	if o.bound {
		err = sink.SetWidgetLines(widgetKey, nil)
	}
	o.registered, o.bound, o.collapsed = false, false, false
	o.resetLocked()
	return err
}
