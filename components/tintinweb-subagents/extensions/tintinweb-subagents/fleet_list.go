package tintinweb_subagents

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The Claude Code-style "FleetView": `main` and each running/queued subagent as a navigable list below the editor.
// ↓ (or ←) at an empty prompt activates the list; ↑/↓ move the selection, Enter opens the selected agent's live
// conversation overlay, Esc returns to the prompt. The list is a belowEditor widget (render only) and ALL key
// handling goes through OnTerminalInput, which fires before the focused editor and can consume keys, gated on an
// empty editor so normal typing is untouched. upstream: ui/fleet-list.ts.

const (
	fleetKey         = "fleet"
	maxAgentRows     = 5    // agent rows shown at once; extras collapse into a "↓ N more" row
	fleetTickMs      = 200  // re-render cadence so elapsed/token stats tick while agents run
	finishedLingerMs = 4000 // how long a finished agent lingers in the list
)

// inputResult is the verdict on one terminal input chunk (nil: let it through).
type inputResult struct{ Consume bool }

// overlayOptions are the layout of the conversation overlay. upstream: ctx.ui.custom overlayOptions.
type overlayOptions struct {
	Anchor        string
	WidthPercent  int
	HeightPercent int
}

// viewerComponent is a component the overlay runs.
type viewerComponent interface {
	Render(width int) []string
	HandleInput(data string)
	Dispose()
}

// viewerFactory builds the overlay's component. upstream: the ctx.ui.custom factory (tui, theme, keybindings, done).
type viewerFactory func(host viewerHost, th uiTheme, kb viewerKeybindings, done func()) viewerComponent

// fleetUI is the part of ctx.ui the fleet list needs. upstream: ui/fleet-list.ts FleetUICtx.
type fleetUI interface {
	SetWidget(key string, render widgetRender, placement string)
	// OnTerminalInput subscribes to raw terminal input; the returned func unsubscribes.
	OnTerminalInput(handler func(data string) *inputResult) (unsubscribe func())
	GetEditorText() string
	Notify(message, level string)
	// Custom opens the overlay and calls onClose once it has closed.
	Custom(factory viewerFactory, opts overlayOptions, onClose func())
	// RequestRender redraws the registered widgets.
	RequestRender()
}

// fleetManager is the manager as the fleet list uses it.
type fleetManager interface {
	agentLister
	abort(id string) bool
	steer(id, message string) error
	// sessionOf is the transcript of an agent, for the overlay opened on it.
	sessionOf(id string) viewerSession
}

// fleetWorkflow is a workflow run as the fleet list needs to see it. upstream: ui/fleet-list.ts FleetWorkflow.
type fleetWorkflow struct {
	ID, Name string
	// Status is running, completed, failed, killed or paused.
	Status                string
	DoneCount, TotalCount int
	StartedAt             int64
	CompletedAt           int64 // set once the run settles, which freezes its clock
	Tokens                int
}

type fleetEntry struct {
	kind     string // "main", "workflow" or "agent"
	record   *agentView
	workflow *fleetWorkflow
}

// formatFleetElapsed is integer seconds, no decimal and no suffix (Claude Code's form, unlike formatMs).
func formatFleetElapsed(ms int64) string {
	s := jsRound(float64(ms) / 1000)
	if s < 0 {
		s = 0
	}
	return strconv.FormatFloat(s, 'f', -1, 64) + "s"
}

// formatFleetTokens is `↓ 13.1k tokens`: a down-arrow prefix, a compact magnitude and a plural "tokens".
func formatFleetTokens(count int) string {
	var compact string
	switch {
	case count >= 1_000_000:
		compact = jsstring.ToFixed(float64(count)/1_000_000, 1) + "M"
	case count >= 1_000:
		compact = jsstring.ToFixed(float64(count)/1_000, 1) + "k"
	default:
		compact = strconv.Itoa(count)
	}
	return "↓ " + compact + " tokens"
}

// rightAlign places right flush to width, truncating left first so the stats survive. The final clamp guarantees the
// line never exceeds width (which would wrap and desync the line diff).
func rightAlign(left, right string, width int) string {
	rightW := widthx.VisibleWidth(right)
	maxLeft := max(0, width-rightW-1)
	leftClamped := widthx.TruncateToWidth(left, maxLeft, "...", false)
	gap := max(1, width-widthx.VisibleWidth(leftClamped)-rightW)
	return widthx.TruncateToWidth(leftClamped+strings.Repeat(" ", gap)+right, width, "...", false)
}

type fleetList struct {
	mu       sync.Mutex
	manager  fleetManager
	activity activitySource
	showCost func() bool
	// viewerMarkdown is the user's setting for an overlay opened from here, read live (the viewer's `m` key changes
	// it while the overlay is up); onViewerMarkdown persists a mode chosen with `m`.
	viewerMarkdown   func() viewerMarkdownMode
	onViewerMarkdown func(viewerMarkdownMode)

	ui         fleetUI
	inputUnsub func()
	registered bool
	stopTimer  func()

	enabled bool
	// active: arrow keys currently navigate the list (rather than flowing to the editor).
	active bool
	// selectedIndex: 0 = main, 1..N = the rows.
	selectedIndex int
	// viewerClose is set while a conversation overlay is open; calling it closes the overlay.
	viewerClose     func()
	viewingAgentID  string
	workflowSource  func() []fleetWorkflow
	openWorkflow    func(id string, onClose func())
	viewingWorkflow string

	// editorHasFocus reports whether the prompt editor owns the keyboard. Unknowable focus counts as the editor, so
	// activation keeps working. upstream: FleetList.editorHasFocus (peeks at the TUI's focused component, which the
	// Go SDK does not expose; the adapter wires what it can).
	editorHasFocus func() bool
}

func newFleetList(m fleetManager, a activitySource, showCost func() bool, viewerMarkdown func() viewerMarkdownMode, onViewerMarkdown func(viewerMarkdownMode)) *fleetList {
	if a == nil {
		a = activityMap{}
	}
	if showCost == nil {
		showCost = func() bool { return false }
	}
	return &fleetList{manager: m, activity: a, showCost: showCost, viewerMarkdown: viewerMarkdown, onViewerMarkdown: onViewerMarkdown, enabled: true}
}

// ---- Lifecycle ----

func (f *fleetList) setEnabled(enabled bool) {
	f.mu.Lock()
	if enabled == f.enabled {
		f.mu.Unlock()
		return
	}
	f.enabled = enabled
	if !enabled {
		f.active = false
	}
	f.mu.Unlock()
	f.update()
}

// setUI captures the UI context and (re)registers the global input handler.
func (f *fleetList) setUI(ui fleetUI) {
	f.mu.Lock()
	if ui == f.ui {
		f.mu.Unlock()
		return
	}
	unsub := f.inputUnsub
	f.ui = ui
	f.registered = false
	f.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	u := ui.OnTerminalInput(f.handleKey)
	f.mu.Lock()
	f.inputUnsub = u
	f.mu.Unlock()
}

// ensureTimer makes sure the re-render timer is running (called when an agent spawns).
func (f *fleetList) ensureTimer() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopTimer == nil {
		f.stopTimer = startTicker(fleetTickMs*time.Millisecond, f.update)
	}
}

// onAgentFinished: the viewer (if open on it) stays open so the final output remains readable, and the row lingers.
func (f *fleetList) onAgentFinished(string) { f.update() }

func (f *fleetList) dispose() {
	f.mu.Lock()
	if f.stopTimer != nil {
		f.stopTimer()
		f.stopTimer = nil
	}
	unsub := f.inputUnsub
	f.inputUnsub = nil
	closeFn := f.viewerClose
	f.viewerClose = nil
	f.viewingAgentID = ""
	// No handle to close the workflow inspector with, but the list is going away: leaving the id set would keep it
	// swallowing input forever.
	f.viewingWorkflow = ""
	ui, registered := f.ui, f.registered
	f.registered = false
	f.active = false
	// Nil last so a close callback above cannot re-register the widget.
	f.ui = nil
	f.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if closeFn != nil {
		closeFn()
	}
	if ui != nil && registered {
		ui.SetWidget(fleetKey, nil, "")
	}
}

// update re-registers/refreshes the below-editor widget; it clears it when nothing remains.
func (f *fleetList) update() {
	f.mu.Lock()
	ui := f.ui
	if ui == nil {
		f.mu.Unlock()
		return
	}
	// A run with no agents of its own left in the list is still worth a row. Read off the roster, as activation does.
	hasRows := f.enabled && len(f.rosterLocked()) > 1
	if !hasRows {
		clear := f.registered
		f.registered = false
		if f.stopTimer != nil {
			f.stopTimer()
			f.stopTimer = nil
		}
		f.active = false
		f.selectedIndex = 0
		f.mu.Unlock()
		if clear {
			ui.SetWidget(fleetKey, nil, "")
		}
		return
	}
	f.clampSelection()
	if f.stopTimer == nil { // keep stats ticking whenever the list is shown (e.g. after a re-enable)
		f.stopTimer = startTicker(fleetTickMs*time.Millisecond, f.update)
	}
	register := !f.registered
	f.registered = true
	f.mu.Unlock()
	if register {
		ui.SetWidget(fleetKey, f.renderBar, belowEditor)
	} else {
		ui.RequestRender()
	}
}

// ---- Roster ----

// agentRecords are the agents shown in the list, earliest-launched first: running/queued ones, the one being viewed
// and recently finished ones. Pending agents with no session yet are hidden until they start.
func (f *fleetList) agentRecords() []*agentView {
	now := nowMs()
	var out []*agentView
	for _, a := range valueOf("agent list", f.manager.listAgents) {
		if isTopLevelAgent(a) && a.HasSession && (a.Status == "running" || a.Status == "queued" ||
			a.ID == f.viewingAgentID || (a.CompletedAt != 0 && now-a.CompletedAt < finishedLingerMs)) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// setWorkflowSource wires workflow runs into the list. Injected because the fleet list predates workflows and must
// keep working without them: a session with the feature off never calls this.
func (f *fleetList) setWorkflowSource(source func() []fleetWorkflow, open func(id string, onClose func())) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflowSource, f.openWorkflow = source, open
}

// workflows are the live runs plus recently settled ones.
func (f *fleetList) workflows() []fleetWorkflow {
	if f.workflowSource == nil {
		return nil
	}
	now := nowMs()
	var out []fleetWorkflow
	for _, run := range valueOf("workflow list", f.workflowSource) {
		if run.Status == "running" || run.Status == "paused" || (run.CompletedAt != 0 && now-run.CompletedAt < finishedLingerMs) {
			out = append(out, run)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// rosterLocked: runs sit above the agents, so the list reads as a hierarchy rather than a shuffle.
func (f *fleetList) rosterLocked() []fleetEntry {
	out := []fleetEntry{{kind: "main"}}
	for _, w := range f.workflows() {
		w := w
		out = append(out, fleetEntry{kind: "workflow", workflow: &w})
	}
	for _, r := range f.agentRecords() {
		out = append(out, fleetEntry{kind: "agent", record: r})
	}
	return out
}

func (f *fleetList) clampSelection() {
	last := len(f.rosterLocked()) - 1
	if f.selectedIndex > last {
		f.selectedIndex = maxInt(0, last)
	}
	if f.selectedIndex < 0 {
		f.selectedIndex = 0
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- Key handling ----

// handleKey returns a consume verdict to swallow a key, or nil to let it through.
func (f *fleetList) handleKey(data string) *inputResult {
	f.mu.Lock()
	if !f.enabled || f.ui == nil {
		f.mu.Unlock()
		return nil
	}
	// Input listeners receive BOTH key-press and key-release: act on press only, or every tap would fire twice.
	if tui.IsKeyRelease(data) {
		f.mu.Unlock()
		return nil
	}
	// While an overlay is open, let it own all input. Checked before the focus test below, which would otherwise read
	// the dialog holding the keyboard as "the user left the list" and reset the selection out from under it.
	if f.viewerClose != nil || f.viewingWorkflow != "" {
		f.mu.Unlock()
		return nil
	}
	// Input listeners fire BEFORE the focused component, and dialogs swap the prompt editor out while the editor text
	// still reads empty. So when anything but the editor owns the keyboard, stay out of its keys (#123).
	if !f.editorHasFocusLocked() {
		wasActive := f.active
		f.mu.Unlock()
		if wasActive {
			f.deactivate()
		}
		return nil
	}

	if !f.active {
		// Activate: ↓ or ← at an empty prompt moves focus into the list. Gated on the roster, not the agents: a
		// session whose only row is a workflow run still has somewhere to go.
		activator := tui.MatchesKey(data, "down") || tui.MatchesKey(data, "left")
		ui := f.ui
		rows := len(f.rosterLocked())
		f.mu.Unlock()
		if activator && rows > 1 && ui.GetEditorText() == "" {
			f.mu.Lock()
			f.active = true
			f.selectedIndex = 0
			f.mu.Unlock()
			f.update()
			return &inputResult{Consume: true}
		}
		return nil
	}

	// Active: arrows navigate, Enter opens, Esc / Up-past-top exits.
	switch {
	case tui.MatchesKey(data, "down"):
		f.selectedIndex = min(len(f.rosterLocked())-1, f.selectedIndex+1)
		f.mu.Unlock()
		f.update()
		return &inputResult{Consume: true}
	case tui.MatchesKey(data, "up"):
		if f.selectedIndex == 0 {
			f.mu.Unlock()
			f.deactivate()
			return &inputResult{Consume: true}
		}
		f.selectedIndex--
		f.mu.Unlock()
		f.update()
		return &inputResult{Consume: true}
	case tui.MatchesKey(data, "escape"):
		f.mu.Unlock()
		f.deactivate()
		return &inputResult{Consume: true}
	case tui.MatchesKey(data, "enter"):
		f.mu.Unlock()
		f.openSelected()
		return &inputResult{Consume: true}
	}
	// Any other key cancels navigation and flows to the editor.
	f.mu.Unlock()
	f.deactivate()
	return nil
}

func (f *fleetList) editorHasFocusLocked() bool {
	return f.editorHasFocus == nil || f.editorHasFocus()
}

func (f *fleetList) deactivate() {
	f.mu.Lock()
	f.active = false
	f.selectedIndex = 0
	f.mu.Unlock()
	f.update()
}

func (f *fleetList) openSelected() {
	f.mu.Lock()
	roster := f.rosterLocked()
	var entry *fleetEntry
	if f.selectedIndex < len(roster) {
		entry = &roster[f.selectedIndex]
	}
	if entry == nil || entry.kind == "main" {
		// `main` = return to the prompt; the native transcript is already shown.
		f.mu.Unlock()
		f.deactivate()
		return
	}
	if entry.kind == "workflow" {
		// The extension owns this overlay and closes it, so there is no viewerClose to hold: but the list still has to
		// know one is up, and put the cursor back on the run when it comes down.
		f.viewingWorkflow = entry.workflow.ID
		open := f.openWorkflow
		id := entry.workflow.ID
		f.mu.Unlock()
		if open != nil {
			open(id, f.clearViewer)
		} else {
			f.clearViewer()
		}
		return
	}
	record := entry.record
	ui := f.ui
	if ui == nil {
		f.mu.Unlock()
		return
	}
	if !record.HasSession {
		f.mu.Unlock()
		ui.Notify(fmt.Sprintf("Agent is %s — no session available.", record.Status), "info")
		return
	}
	f.viewingAgentID = record.ID
	id, description := record.ID, record.Description
	showCost := f.showCost()
	f.mu.Unlock()

	ui.Custom(func(host viewerHost, th uiTheme, kb viewerKeybindings, done func()) viewerComponent {
		f.mu.Lock()
		f.viewerClose = done
		f.mu.Unlock()
		return newConversationViewer(host, f.manager.sessionOf(id), f.recordOf(id), f.activityOfFn(id), th, done, viewerOptions{
			// The abort is a round trip to the child: it runs off the viewer's input path.
			OnStop: func() {
				safeGo("viewer stop", func() {
					if f.manager.abort(id) {
						ui.Notify("Stopped \""+description+"\".", "info")
					}
				})
			},
			Keybindings:    kb,
			OnSteer:        func(message string) { _ = f.manager.steer(id, message) },
			ShowCost:       showCost,
			ViewerMarkdown: f.viewerMarkdown,
			OnMarkdownMode: f.onViewerMarkdown,
		})
	}, overlayOptions{Anchor: "center", WidthPercent: 90, HeightPercent: viewportHeightPct}, f.clearViewer)
}

// recordOf and activityOfFn read an agent live, by id, for the overlay opened on it.
func (f *fleetList) recordOf(id string) func() agentView {
	return func() agentView {
		for _, a := range f.manager.listAgents() {
			if a.ID == id {
				return *a
			}
		}
		return agentView{ID: id}
	}
}

// activityOfFn is the activity the overlay holds: the one the agent has when the overlay opens (none for an agent that has
// already settled), kept live while it runs and frozen at its last state once it settles. upstream: the viewer is
// constructed with `agentActivity.get(id)`, an object the map's deletion does not take from the viewer.
func (f *fleetList) activityOfFn(id string) func() *agentActivity {
	var mu sync.Mutex
	held := f.activity.activityOf(id)
	return func() *agentActivity {
		mu.Lock()
		defer mu.Unlock()
		if held == nil {
			return nil
		}
		if cur := f.activity.activityOf(id); cur != nil {
			held = cur
		}
		return held
	}
}

// clearViewer resets the overlay state and returns to the list (on close, auto-close or error). The cursor stays on
// the agent that was viewed, re-resolved by id so it still feels natural if the list reordered while the overlay was up.
func (f *fleetList) clearViewer() {
	f.mu.Lock()
	viewed := f.viewingAgentID
	if viewed == "" {
		viewed = f.viewingWorkflow
	}
	if viewed != "" {
		for i, e := range f.rosterLocked() {
			if (e.kind == "agent" && e.record.ID == viewed) || (e.kind == "workflow" && e.workflow.ID == viewed) {
				f.selectedIndex = i
				break
			}
		}
	}
	f.viewerClose = nil
	f.viewingAgentID = ""
	f.viewingWorkflow = ""
	f.mu.Unlock()
	f.update()
}

// ---- Rendering ----

func (f *fleetList) renderBar(width int, th uiTheme) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	roster := f.rosterLocked()
	rows := roster[1:]
	if len(rows) == 0 {
		return nil
	}
	// Clamp locally so a render between a roster shrink and the next update() never loses the selection marker.
	sel := min(f.selectedIndex, len(rows))

	hint := "esc to interrupt · ← for agents · ↓ to manage"
	if f.active {
		hint = "↑↓ select · enter view · esc back"
	}
	lines := []string{
		widthx.TruncateToWidth("  "+th.Fg("dim", hint), width, "...", false),
		"",
		widthx.TruncateToWidth("  "+f.bullet(0, sel, th)+" main", width, "...", false),
	}

	// Window the rows so the selected one stays visible.
	visible := min(maxAgentRows, len(rows))
	selRow := maxInt(0, sel-1)
	start := 0
	if selRow >= visible {
		start = selRow - visible + 1
	}
	hiddenBelow := len(rows) - (start + visible)
	if start > 0 {
		lines = append(lines, rightAlign("", th.Fg("dim", fmt.Sprintf("↑ %d more", start)), width))
	}
	for a := start; a < start+visible; a++ {
		row := rows[a]
		if row.kind == "workflow" {
			lines = append(lines, f.renderWorkflowRow(a+1, sel, *row.workflow, width, th))
		} else {
			lines = append(lines, f.renderAgentRow(a+1, sel, row.record, width, th))
		}
	}
	if hiddenBelow > 0 {
		lines = append(lines, rightAlign("", th.Fg("dim", fmt.Sprintf("↓ %d more", hiddenBelow)), width))
	}
	return lines
}

func (f *fleetList) bullet(rosterIndex, sel int, th uiTheme) string {
	if rosterIndex == sel {
		return th.Fg("accent", "●")
	}
	return th.Fg("dim", "○")
}

// renderWorkflowRow is a run's row: shaped like an agent's (bullet, kind, name, stats flush right).
func (f *fleetList) renderWorkflowRow(rosterIndex, sel int, w fleetWorkflow, width int, th uiTheme) string {
	selected := rosterIndex == sel
	kindColor := "muted"
	if selected {
		kindColor = "text"
	}
	kind := th.Fg(kindColor, "workflow")
	name := w.Name
	if selected {
		name = th.Fg("text", w.Name)
	}
	left := "  " + f.bullet(rosterIndex, sel, th) + " " + kind + "  " + name
	// Frozen once the run settles, exactly as an agent's clock is.
	end := w.CompletedAt
	if end == 0 {
		end = nowMs()
	}
	agents := fmt.Sprintf("%d/%d agent%s", w.DoneCount, w.TotalCount, plural(w.TotalCount, "", "s"))
	stats := agents + " · " + formatFleetElapsed(end-w.StartedAt) + " · " + formatFleetTokens(w.Tokens)
	if selected {
		return rightAlign(left, th.Fg("text", stats), width)
	}
	return rightAlign(left, th.Fg("dim", stats), width)
}

func (f *fleetList) renderAgentRow(rosterIndex, sel int, r *agentView, width int, th uiTheme) string {
	// The selected row renders in the theme's primary text color so it reads as one selection (#230). A configured
	// badge survives (bolded), which also keeps the row's width fixed as the selection moves.
	selected := rosterIndex == sel
	style := agentNameStyle{FallbackColor: "muted"}
	if selected {
		style = agentNameStyle{FallbackColor: "text", Bold: hasAgentBadge(r.Type)}
	}
	name := renderAgentName(r.Type, th, style)
	description := r.Description
	if selected {
		description = th.Fg("text", r.Description)
	}
	left := "  " + f.bullet(rosterIndex, sel, th) + " " + name + "  " + description
	// The record, not the activity tracker: only the record carries a nested child's spend and outlives the agent.
	tokens := getLifetimeTotal(r.Usage)
	end := r.CompletedAt // freezes once finished
	if end == 0 {
		end = nowMs()
	}
	stats := formatFleetElapsed(end-r.StartedAt) + " · " + formatFleetTokens(tokens)
	if f.showCost() {
		if cost := formatCost(getLifetimeCost(r.Usage)); cost != "" {
			stats += " · " + cost
		}
	}
	if selected {
		return rightAlign(left, th.Fg("text", stats), width)
	}
	return rightAlign(left, th.Fg("dim", stats), width)
}
