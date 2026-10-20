package tintinweb_subagents

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The persistent widget above the editor: a tree of agents with animated spinners, live stats and activity
// descriptions. upstream: src/ui/agent-widget.ts AgentWidget.

const (
	maxWidgetLines    = 12 // rendered lines before the overflow collapse kicks in
	errorLingerTurns  = 2  // extra turns that error/aborted agents linger (completed agents clear after 1)
	widgetKey         = "agents"
	widgetStatusKey   = "subagents"
	widgetTickMs      = 80
	aboveEditor       = "aboveEditor"
	belowEditor       = "belowEditor"
	widgetFinishedAge = 1
)

// widgetRender draws a widget at the terminal width (the original's `tui.terminal.columns`).
type widgetRender func(columns int, th uiTheme) []string

// widgetUI is the part of ctx.ui the widget needs. upstream: ui/agent-widget.ts UICtx.
type widgetUI interface {
	// SetStatus sets (text != nil) or clears (nil) a footer status.
	SetStatus(key string, text *string)
	// SetWidget registers a widget drawn by render, or clears it (render == nil).
	SetWidget(key string, render widgetRender, placement string)
	// RequestRender redraws the registered widgets.
	RequestRender()
}

// agentLister is the manager as the widget and the fleet list read it. upstream: AgentManager.listAgents.
type agentLister interface{ listAgents() []*agentView }

type agentWidget struct {
	mu        sync.Mutex
	manager   agentLister
	activity  activitySource
	mode      func() widgetMode
	showCost  func() bool
	showModel func() bool

	ui             widgetUI
	frame          int
	stopTimer      func()
	finishedAge    map[string]int
	registered     bool
	lastStatusText *string
}

// newAgentWidget: a nil mode reads "all", as the original's default; nil showCost/showModel read false.
func newAgentWidget(m agentLister, a activitySource, mode func() widgetMode, showCost, showModel func() bool) *agentWidget {
	if mode == nil {
		mode = func() widgetMode { return widgetModeAll }
	}
	if showCost == nil {
		showCost = func() bool { return false }
	}
	if showModel == nil {
		showModel = func() bool { return false }
	}
	if a == nil {
		a = activityMap{}
	}
	return &agentWidget{manager: m, activity: a, mode: mode, showCost: showCost, showModel: showModel, finishedAge: map[string]int{}}
}

// widgetAgents are the agents eligible for the widget, per the widget mode: none for off; for background everything
// but agents known to be foreground; every agent for all.
func (w *agentWidget) widgetAgents() []*agentView {
	var all []*agentView
	for _, a := range valueOf("agent list", w.manager.listAgents) {
		if isTopLevelAgent(a) {
			all = append(all, a)
		}
	}
	switch w.mode() {
	case widgetModeOff:
		return nil
	case widgetModeBackground:
		var out []*agentView
		for _, a := range all {
			if a.IsBackground == nil || *a.IsBackground {
				out = append(out, a)
			}
		}
		return out
	}
	return all
}

// setUI sets the UI context; a changed one has lost the widget registered on the old one.
func (w *agentWidget) setUI(ui widgetUI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ui != w.ui {
		w.ui = ui
		w.registered = false
		w.lastStatusText = nil
	}
}

// onTurnStart ages the finished agents and refreshes the widget, which drops the ones that lingered long enough.
func (w *agentWidget) onTurnStart() {
	w.mu.Lock()
	for id, age := range w.finishedAge {
		w.finishedAge[id] = age + 1
	}
	w.mu.Unlock()
	w.update()
}

// ensureTimer makes sure the widget update timer is running.
func (w *agentWidget) ensureTimer() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopTimer == nil {
		w.stopTimer = startTicker(widgetTickMs*time.Millisecond, w.update)
	}
}

func (w *agentWidget) shouldShowFinished(id, status string) bool {
	maxAge := widgetFinishedAge
	if errorStatuses[status] {
		maxAge = errorLingerTurns
	}
	return w.finishedAge[id] < maxAge
}

// markFinished records an agent as finished.
func (w *agentWidget) markFinished(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.finishedAge[id]; !ok {
		w.finishedAge[id] = 0
	}
}

// markRunning drops an agent's finished age (a background resume), which markFinished would otherwise keep from the
// previous run and hide the new run's completion line.
func (w *agentWidget) markRunning(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.finishedAge, id)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderFinishedLine is a finished agent's line.
func (w *agentWidget) renderFinishedLine(a *agentView, th uiTheme) string {
	modeLabel := getPromptModeLabel(a.Type)
	completed := a.CompletedAt
	if completed == 0 {
		completed = nowMs()
	}
	duration := formatMs(completed - a.StartedAt)

	var icon, statusText string
	switch a.Status {
	case "completed":
		icon = th.Fg("success", "✓")
	case "steered":
		icon, statusText = th.Fg("warning", "✓"), th.Fg("warning", " (turn limit)")
	case "stopped":
		icon, statusText = th.Fg("dim", "■"), th.Fg("dim", " stopped")
	case "error":
		icon = th.Fg("error", "✗")
		errMsg := ""
		if a.Error != "" {
			errMsg = ": " + jsstring.Slice(a.Error, 0, 60)
		}
		statusText = th.Fg("error", " error"+errMsg)
	default: // aborted
		icon, statusText = th.Fg("error", "✗"), th.Fg("warning", " aborted")
	}

	var parts []string
	if act := w.activity.activityOf(a.ID); act != nil {
		parts = append(parts, formatTurns(act.TurnCount, act.MaxTurns))
	}
	if a.ToolUses > 0 {
		parts = append(parts, fmt.Sprintf("%d tool %s", a.ToolUses, plural(a.ToolUses, "use", "uses")))
	}
	// From the record, not the activity tracker: that entry is deleted the moment an agent finishes.
	if w.showCost() {
		if c := formatCost(getLifetimeCost(a.Usage)); c != "" {
			parts = append(parts, c)
		}
	}
	parts = append(parts, duration)

	modeTag := ""
	if modeLabel != "" {
		modeTag = " " + th.Fg("dim", "("+modeLabel+")")
	}
	return icon + " " + renderAgentName(a.Type, th, agentNameStyle{FallbackColor: "dim"}) + modeTag + "  " + th.Fg("dim", a.Description) + " " + th.Fg("dim", "·") + " " + th.Fg("dim", strings.Join(parts, " · ")) + statusText
}

// renderWidget renders the widget content, reading live state each time. Called per frame.
func (w *agentWidget) renderWidget(columns int, th uiTheme) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	all := w.widgetAgents()
	var running, queued, finished []*agentView
	for _, a := range all {
		switch {
		case a.Status == "running":
			running = append(running, a)
		case a.Status == "queued":
			queued = append(queued, a)
		case a.CompletedAt != 0 && w.shouldShowFinished(a.ID, a.Status):
			finished = append(finished, a)
		}
	}
	hasActive := len(running) > 0 || len(queued) > 0
	if !hasActive && len(finished) == 0 {
		return nil
	}

	truncate := func(line string) string { return widthx.TruncateToWidth(line, columns, "...", false) }
	headingColor, headingIcon := "dim", "○"
	if hasActive {
		headingColor, headingIcon = "accent", "●"
	}
	frame := spinner[w.frame%len(spinner)]

	// Sections are built separately for the overflow-aware assembly: a running agent is 2 lines (header and
	// activity), a finished one 1, the queued summary 1.
	var finishedLines []string
	for _, a := range finished {
		finishedLines = append(finishedLines, truncate(th.Fg("dim", "├─")+" "+w.renderFinishedLine(a, th)))
	}

	var runningLines [][2]string
	for _, a := range running {
		modeLabel := getPromptModeLabel(a.Type)
		modeTag := ""
		if modeLabel != "" {
			modeTag = " " + th.Fg("dim", "("+modeLabel+")")
		}
		elapsed := formatMs(nowMs() - a.StartedAt)

		bg := w.activity.activityOf(a.ID)
		toolUses := a.ToolUses
		if bg != nil {
			toolUses = bg.ToolUses
		}
		// Spend comes from the record, never from the activity tracker.
		tokens := getLifetimeTotal(a.Usage)
		var contextPercent *float64
		if bg != nil {
			contextPercent = getSessionContextPercent(bg.Session)
		}
		tokenText := ""
		if tokens > 0 {
			tokenText = formatSessionTokens(tokens, contextPercent, th, a.CompactionCount)
		}
		costText := ""
		if w.showCost() {
			costText = formatCost(getLifetimeCost(a.Usage))
		}

		var parts []string
		if w.showModel() {
			// Leading, and paired: a thinking level means nothing without the model it applies to.
			tags := buildInvocationTags(a.Invocation)
			if tags.ModelName != "" {
				parts = append(parts, tags.ModelName)
			}
			for _, tag := range tags.Tags {
				if strings.HasPrefix(tag, "thinking: ") {
					parts = append(parts, tag)
					break
				}
			}
		}
		if bg != nil {
			parts = append(parts, formatTurns(bg.TurnCount, bg.MaxTurns))
		}
		if toolUses > 0 {
			parts = append(parts, fmt.Sprintf("%d tool %s", toolUses, plural(toolUses, "use", "uses")))
		}
		if tokenText != "" {
			parts = append(parts, tokenText)
		}
		if costText != "" {
			parts = append(parts, costText)
		}
		parts = append(parts, elapsed)
		statsText := strings.Join(parts, " · ")

		activity := "thinking…"
		if bg != nil {
			activity = describeActivity(bg.ActiveTools, bg.ResponseText)
		}
		runningLines = append(runningLines, [2]string{
			truncate(th.Fg("dim", "├─") + " " + th.Fg("accent", frame) + " " + renderAgentName(a.Type, th, agentNameStyle{Bold: true}) + modeTag + "  " + th.Fg("muted", a.Description) + " " + th.Fg("dim", "·") + " " + fgPreservingNestedStyles(th, "dim", statsText)),
			truncate(th.Fg("dim", "│  ") + th.Fg("dim", "  ⎿  "+activity)),
		})
	}

	queuedLine, hasQueued := "", len(queued) > 0
	if hasQueued {
		queuedLine = truncate(th.Fg("dim", "├─") + " " + th.Fg("muted", "◦") + " " + th.Fg("dim", fmt.Sprintf("%d queued", len(queued))))
	}

	// Assemble with the overflow cap (the heading takes 1 line).
	maxBody := maxWidgetLines - 1
	totalBody := len(finishedLines) + len(runningLines)*2
	if hasQueued {
		totalBody++
	}
	lines := []string{truncate(th.Fg(headingColor, headingIcon) + " " + th.Fg(headingColor, "Agents"))}

	if totalBody <= maxBody {
		lines = append(lines, finishedLines...)
		for _, pair := range runningLines {
			lines = append(lines, pair[0], pair[1])
		}
		if hasQueued {
			lines = append(lines, queuedLine)
		}
		// Fix the last connector: ├─ → └─, and │ → space on the last agent's activity line.
		if len(lines) > 1 {
			last := len(lines) - 1
			lines[last] = strings.Replace(lines[last], "├─", "└─", 1)
			if len(runningLines) > 0 && !hasQueued && last >= 2 {
				lines[last-1] = strings.Replace(lines[last-1], "├─", "└─", 1)
				lines[last] = strings.Replace(lines[last], "│  ", "   ", 1)
			}
		}
	} else {
		// Overflow: running > queued > finished. One row is reserved for the overflow indicator, and the queued
		// summary's row up front, so it cannot be dropped (costing at most one running agent, which IS counted).
		budget := maxBody - 1
		hiddenRunning, hiddenFinished := 0, 0
		queuedReserve := 0
		if hasQueued {
			queuedReserve = 1
		}
		budget -= queuedReserve
		for _, pair := range runningLines {
			if budget >= 2 {
				lines = append(lines, pair[0], pair[1])
				budget -= 2
			} else {
				hiddenRunning++
			}
		}
		if hasQueued {
			budget += queuedReserve
			lines = append(lines, queuedLine)
			budget--
		}
		for _, fl := range finishedLines {
			if budget >= 1 {
				lines = append(lines, fl)
				budget--
			} else {
				hiddenFinished++
			}
		}
		var overflow []string
		if hiddenRunning > 0 {
			overflow = append(overflow, fmt.Sprintf("%d running", hiddenRunning))
		}
		if hiddenFinished > 0 {
			overflow = append(overflow, fmt.Sprintf("%d finished", hiddenFinished))
		}
		lines = append(lines, truncate(th.Fg("dim", "└─")+" "+th.Fg("dim", fmt.Sprintf("+%d more (%s)", hiddenRunning+hiddenFinished, strings.Join(overflow, ", ")))))
	}
	return lines
}

// update forces an immediate widget update.
func (w *agentWidget) update() {
	w.mu.Lock()
	ui := w.ui
	if ui == nil {
		w.mu.Unlock()
		return
	}
	all := w.widgetAgents()
	// Lightweight existence checks: the full categorization happens in renderWidget().
	runningCount, queuedCount, hasFinished := 0, 0, false
	for _, a := range all {
		switch {
		case a.Status == "running":
			runningCount++
		case a.Status == "queued":
			queuedCount++
		case a.CompletedAt != 0 && w.shouldShowFinished(a.ID, a.Status):
			hasFinished = true
		}
	}
	hasActive := runningCount > 0 || queuedCount > 0

	if !hasActive && !hasFinished {
		clearWidget, clearStatus := w.registered, w.lastStatusText != nil
		w.registered = false
		w.lastStatusText = nil
		if w.stopTimer != nil {
			w.stopTimer()
			w.stopTimer = nil
		}
		// Clean up stale entries.
		present := map[string]bool{}
		for _, a := range all {
			present[a.ID] = true
		}
		for id := range w.finishedAge {
			if !present[id] {
				delete(w.finishedAge, id)
			}
		}
		w.mu.Unlock()
		if clearWidget {
			ui.SetWidget(widgetKey, nil, "")
		}
		if clearStatus {
			ui.SetStatus(widgetStatusKey, nil)
		}
		return
	}

	// Status bar: only call SetStatus when the text actually changes.
	var newStatus *string
	if hasActive {
		var parts []string
		if runningCount > 0 {
			parts = append(parts, fmt.Sprintf("%d running", runningCount))
		}
		if queuedCount > 0 {
			parts = append(parts, fmt.Sprintf("%d queued", queuedCount))
		}
		total := runningCount + queuedCount
		s := strings.Join(parts, ", ") + " agent" + plural(total, "", "s")
		newStatus = &s
	}
	setStatus := !sameStatus(newStatus, w.lastStatusText)
	if setStatus {
		w.lastStatusText = newStatus
	}
	w.frame++
	register := !w.registered
	w.registered = true
	w.mu.Unlock()

	if setStatus {
		ui.SetStatus(widgetStatusKey, newStatus)
	}
	// The widget is registered once; later updates only request a re-render (no layout thrash).
	if register {
		ui.SetWidget(widgetKey, w.renderWidget, aboveEditor)
	} else {
		ui.RequestRender()
	}
}

func sameStatus(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// dispose stops the timer and clears the widget and the status.
func (w *agentWidget) dispose() {
	w.mu.Lock()
	if w.stopTimer != nil {
		w.stopTimer()
		w.stopTimer = nil
	}
	ui := w.ui
	w.registered = false
	w.lastStatusText = nil
	w.mu.Unlock()
	if ui != nil {
		ui.SetWidget(widgetKey, nil, "")
		ui.SetStatus(widgetStatusKey, nil)
	}
}
