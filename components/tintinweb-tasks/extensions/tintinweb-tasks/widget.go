package tintinweb_tasks

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Persistent widget showing the task list with status glyphs and progress. upstream: ui/task-widget.ts.
//
//	✔ completed (strikethrough, dim)   ◼ in_progress   ◻ pending   ✳/✽ actively executing (spinner, elapsed, tokens)
//
// Every glyph is a default that `glyphs` in tasks-config.json can replace. The original registers a component
// the host re-renders; a Go extension cannot, so the widget pushes pre-rendered rows at the width and theme of
// the host's context each time it changes and on the spinner's timer (the host shows at most ten rows).

// widgetTheme is what the widget needs of a theme (the host's UITheme satisfies it). upstream: task-widget.ts Theme.
type widgetTheme interface {
	Fg(color, text string) string
	Strikethrough(text string) string
}

// widgetUI is where the widget draws: the host. upstream: task-widget.ts UICtx.
type widgetUI interface {
	// setWidget shows lines above the editor under key; nil lines remove the widget.
	setWidget(key string, lines []string)
	theme() widgetTheme
	columns() int
	// alive reports whether the host still exists; the spinner timer stops when it does not.
	alive() bool
}

// widgetPlacement is where the widget is shown.
const widgetPlacement = "aboveEditor"

const (
	defaultMaxVisibleTasks = 10
	spinnerTick            = 150 * time.Millisecond
)

// tickerFn replaces the timer in tests; it is atomic because the extension's own goroutines read it.
var tickerFn atomic.Pointer[func(d time.Duration, fn func()) (stop func())]

// setTicker replaces the repeating timer (a test uses a fake clock) and returns the function that restores it.
func setTicker(f func(d time.Duration, fn func()) (stop func())) (restore func()) {
	prev := tickerFn.Swap(&f)
	return func() { tickerFn.Store(prev) }
}

func newTicker(d time.Duration, fn func()) (stop func()) {
	if f := tickerFn.Load(); f != nil {
		return (*f)(d, fn)
	}
	return realTicker(d, fn)
}

func realTicker(d time.Duration, fn func()) (stop func()) {
	t := time.NewTicker(d)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				fn()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { t.Stop(); close(done) }) }
}

// taskMetrics are the runtime metrics of a task being executed (elapsed time, token usage).
type taskMetrics struct {
	startedAt              int64
	inputTokens, outTokens int
}

type taskWidget struct {
	mu         sync.Mutex
	store      *taskStore
	config     tasksConfig
	ui         widgetUI
	frame      int
	stopTicker func()
	active     map[string]bool
	activeOrd  []string
	metrics    map[string]*taskMetrics
	registered bool
}

func newTaskWidget(store *taskStore, cfg tasksConfig) *taskWidget {
	if cfg == nil {
		cfg = tasksConfig{}
	}
	return &taskWidget{store: store, config: cfg, active: map[string]bool{}, metrics: map[string]*taskMetrics{}}
}

func (w *taskWidget) setStore(s *taskStore) { w.mu.Lock(); w.store = s; w.mu.Unlock() }
func (w *taskWidget) setUI(ui widgetUI)     { w.mu.Lock(); w.ui = ui; w.mu.Unlock() }

// formatDuration formats milliseconds as a human-readable duration ("2m 49s", "1h 3m"). upstream: task-widget.ts:62-70.
func formatDuration(ms int64) string {
	totalSec := ms / 1000
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	min, sec := totalSec/60, totalSec%60
	if min < 60 {
		if sec > 0 {
			return fmt.Sprintf("%dm %ds", min, sec)
		}
		return fmt.Sprintf("%dm", min)
	}
	hr, remMin := min/60, min%60
	if remMin > 0 {
		return fmt.Sprintf("%dh %dm", hr, remMin)
	}
	return fmt.Sprintf("%dh", hr)
}

// formatTokens formats a token count with a k suffix ("4.1k", "850"). upstream: task-widget.ts:73-76.
func formatTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	s := strconv.FormatFloat(float64(n)/1000, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + "k"
}

func (w *taskWidget) removeActive(id string) {
	delete(w.active, id)
	for i, o := range w.activeOrd {
		if o == id {
			w.activeOrd = append(w.activeOrd[:i:i], w.activeOrd[i+1:]...)
			break
		}
	}
}

// setActiveTask adds a task to or removes it from the active spinner set. upstream: task-widget.ts:92-103.
func (w *taskWidget) setActiveTask(id string, active bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id != "" && active {
		if !w.active[id] {
			w.active[id] = true
			w.activeOrd = append(w.activeOrd, id)
		}
		if w.metrics[id] == nil {
			w.metrics[id] = &taskMetrics{startedAt: nowMs()}
		}
		w.ensureTimer()
	} else if id != "" {
		w.removeActive(id)
	}
	w.updateLocked()
}

// addTokenUsage records token usage for every currently active task.
func (w *taskWidget) addTokenUsage(in, out int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.active {
		if m := w.metrics[id]; m != nil {
			m.inputTokens += in
			m.outTokens += out
		}
	}
}

// ensureTimer makes sure the update timer runs. The spinner advances there and nowhere else: update() also
// runs on every task mutation and tool execution, so incrementing there would tie the animation speed to how
// busy the agent is. The caller holds w.mu.
func (w *taskWidget) ensureTimer() {
	if w.stopTicker == nil {
		w.stopTicker = newTicker(spinnerTick, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.ui != nil && !w.ui.alive() {
				w.stopTimer()
				return
			}
			w.frame++
			w.updateLocked()
		})
	}
}

func (w *taskWidget) stopTimer() {
	if w.stopTicker != nil {
		w.stopTicker()
		w.stopTicker = nil
	}
}

// buildLines builds the widget lines from the current live state, never failing: a render error leaves the
// widget empty for one frame instead of reaching the host. upstream: task-widget.ts:128-136 (renderWidget).
func (w *taskWidget) buildLines(theme widgetTheme, columns int) (lines []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buildLinesLocked(theme, columns)
}

func (w *taskWidget) buildLinesLocked(theme widgetTheme, columns int) (lines []string) {
	defer func() {
		if recover() != nil {
			lines = []string{}
		}
	}()
	return w.buildWidgetLines(theme, columns)
}

func (w *taskWidget) buildWidgetLines(theme widgetTheme, columns int) []string {
	sortOrder := w.config["sortOrder"]
	// Resolved per render, not cached: the extension swaps this config's contents when the host moves to a
	// session in another workspace.
	glyphs := resolveTaskGlyphs(w.config["glyphs"])
	tasks := w.store.list(sortOrder)
	truncate := func(line string) string { return truncateToWidth(line, columns, glyphs.Truncation) }
	if len(tasks) == 0 {
		return []string{}
	}
	var completed, inProgress, pending int
	for _, t := range tasks {
		switch t.Status {
		case statusCompleted:
			completed++
		case statusInProgress:
			inProgress++
		case statusPending:
			pending++
		}
	}
	var parts []string
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("%d done", completed))
	}
	if inProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d in progress", inProgress))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d open", pending))
	}
	statusText := fmt.Sprintf("%d tasks (%s)", len(tasks), strings.Join(parts, ", "))
	spinnerFrame := glyphs.Spinner[w.frame%len(glyphs.Spinner)]
	lines := []string{truncate(theme.Fg("accent", glyphs.Header) + " " + theme.Fg("accent", statusText))}

	// Collapsing only decides what goes in the list; the visible-limit logic then runs over what remains.
	collapse := w.config.flag("collapseCompleted")
	listed := tasks
	if collapse {
		listed = nil
		for _, t := range tasks {
			if t.Status != statusCompleted {
				listed = append(listed, t)
			}
		}
	}
	showAll := w.config.flag("showAll")
	limit := defaultMaxVisibleTasks
	if v, ok := w.config["maxVisible"].(float64); ok {
		limit = int(v)
	} else if v, ok := w.config["maxVisible"].(int); ok {
		limit = v
	}
	// Narrowed rather than defaulted: config is hand-editable JSON.
	hiddenAt := "bottom"
	if w.config["hiddenAt"] == "top" {
		hiddenAt = "top"
	}
	visible := listed
	if !showAll {
		limit = max(limit, 0)
		if limit < len(listed) {
			if hiddenAt == "top" {
				visible = listed[len(listed)-limit:]
			} else {
				visible = listed[:limit]
			}
		}
	}
	hidden := len(listed) - len(visible)
	overflowLine := ""
	if hidden > 0 {
		overflowLine = truncate(theme.Fg("dim", fmt.Sprintf("    %s and %d more", glyphs.Overflow, hidden)))
	}
	if overflowLine != "" && hiddenAt == "top" {
		lines = append(lines, overflowLine)
	}
	for _, t := range visible {
		isActive := w.active[t.ID] && t.Status == statusInProgress
		var statusGlyph string
		switch {
		case isActive:
			statusGlyph = theme.Fg("accent", spinnerFrame)
		case t.Status == statusCompleted:
			statusGlyph = theme.Fg("success", glyphs.Completed)
		case t.Status == statusInProgress:
			statusGlyph = theme.Fg("accent", glyphs.InProgress)
		default:
			statusGlyph = glyphs.Pending
		}
		suffix := ""
		if t.Status == statusPending && len(t.BlockedBy) > 0 {
			var open []string
			for _, bid := range t.BlockedBy {
				if b := w.store.get(bid); b != nil && b.Status != statusCompleted {
					open = append(open, "#"+bid)
				}
			}
			if len(open) > 0 {
				suffix = theme.Fg("dim", fmt.Sprintf(" %s blocked by %s", glyphs.Blocked, strings.Join(open, ", ")))
			}
		}
		agentID, _ := t.Metadata["agentId"].(string)
		var text string
		switch {
		case isActive:
			form := t.ActiveForm
			if form == "" {
				form = t.Subject
			}
			agentLabel := ""
			if agentID != "" {
				agentLabel = fmt.Sprintf(" (agent %s)", firstUTF16Units(agentID, 5))
			}
			stats := ""
			if m := w.metrics[t.ID]; m != nil {
				elapsed := formatDuration(nowMs() - m.startedAt)
				var tokenParts []string
				if m.inputTokens > 0 {
					tokenParts = append(tokenParts, glyphs.InputTokens+" "+formatTokens(m.inputTokens))
				}
				if m.outTokens > 0 {
					tokenParts = append(tokenParts, glyphs.OutputTokens+" "+formatTokens(m.outTokens))
				}
				if len(tokenParts) > 0 {
					stats = " " + theme.Fg("dim", fmt.Sprintf("(%s %s %s)", elapsed, glyphs.StatsSeparator, strings.Join(tokenParts, " ")))
				} else {
					stats = " " + theme.Fg("dim", fmt.Sprintf("(%s)", elapsed))
				}
			}
			text = fmt.Sprintf("  %s %s %s%s", statusGlyph, theme.Fg("dim", "#"+t.ID), theme.Fg("accent", form+agentLabel+glyphs.TrailingEllipsis), stats)
		case t.Status == statusCompleted:
			text = fmt.Sprintf("  %s %s", statusGlyph, theme.Fg("dim", theme.Strikethrough("#"+t.ID+" "+t.Subject)))
		default:
			agentSuffix := ""
			if t.Status == statusInProgress && agentID != "" {
				agentSuffix = theme.Fg("dim", fmt.Sprintf(" (agent %s)", firstUTF16Units(agentID, 5)))
			}
			text = fmt.Sprintf("  %s %s %s%s", statusGlyph, theme.Fg("dim", "#"+t.ID), t.Subject, agentSuffix)
		}
		lines = append(lines, truncate(text+suffix))
	}
	if overflowLine != "" && hiddenAt != "top" {
		lines = append(lines, overflowLine)
	}
	if collapse && completed > 0 {
		lines = append(lines, truncate(fmt.Sprintf("  %s %s", theme.Fg("success", glyphs.CompletedSummary), theme.Fg("dim", fmt.Sprintf("%d completed", completed)))))
	}
	return lines
}

// firstUTF16Units is s.slice(0, n) as JavaScript counts: the first n UTF-16 code units (agent ids are ASCII).
func firstUTF16Units(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// update forces an immediate widget update: the widget is removed when the list is empty, stale active ids are
// pruned, the timer runs while a spinner shows, and the rows are pushed to the host. upstream: task-widget.ts:253-297.
func (w *taskWidget) update() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.updateLocked()
}

func (w *taskWidget) updateLocked() {
	if w.ui == nil {
		return
	}
	tasks := w.store.list(nil)
	// Transition: visible → hidden.
	if len(tasks) == 0 {
		if w.registered {
			w.ui.setWidget("tasks", nil)
			w.registered = false
		}
		w.stopTimer()
		return
	}
	// Prune stale active IDs (deleted or no longer in_progress).
	for _, id := range append([]string{}, w.activeOrd...) {
		if t := w.store.get(id); t == nil || t.Status != statusInProgress {
			w.removeActive(id)
			delete(w.metrics, id)
		}
	}
	hasSpinner := false
	for _, t := range tasks {
		if w.active[t.ID] && t.Status == statusInProgress {
			hasSpinner = true
		}
	}
	if hasSpinner {
		w.ensureTimer()
	} else {
		w.stopTimer()
	}
	w.registered = true
	w.ui.setWidget("tasks", w.buildLinesLocked(w.ui.theme(), w.ui.columns()))
}

// dispose stops the timer and removes the widget.
func (w *taskWidget) dispose() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopTimer()
	if w.ui != nil {
		w.ui.setWidget("tasks", nil)
	}
	w.registered = false
}
