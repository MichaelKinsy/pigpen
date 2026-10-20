package pi_goal_x

import (
	"strconv"
	"strings"
)

// Ported from widgets/goal-widget.ts. The TS file's unused private helpers (fit, heading, branchLine,
// progressBar, spinnerFrame) are not ported.

// goalWidgetKey is GOAL_WIDGET_KEY.
const goalWidgetKey = "goal"

// widgetHeightReserve is WIDGET_HEIGHT_RESERVE: rows reserved below/around the widget (status 1 + editor 3 + footer 1 + a chat row 1).
const widgetHeightReserve = 6

// ── Host interfaces (what the original gets from pi-tui's TUI and pi's ctx) ──

// goalTUI is the part of pi-tui's TUI the widgets use.
type goalTUI interface {
	getShowHardwareCursor() bool
	setShowHardwareCursor(enabled bool)
	requestRender()
}

// goalTerminalTUI is implemented by a TUI that exposes `terminal.rows` (the terminal-height bound reads it at render time).
type goalTerminalTUI interface{ terminalRows() (int, bool) }

// goalDockTUI is implemented by a TUI that exposes its top-level `children` (the dock measurement reads them).
type goalDockTUI interface{ dockChildren() []any }

// goalDockContainer is a dock child that holds children (a Container); goalDockRenderer one that renders (a Component).
type goalDockContainer interface{ dockChildren() []any }
type goalDockRenderer interface{ render(width int) []string }

// goalInputComponent is a component that takes keyboard input (Component.handleInput).
type goalInputComponent interface{ HandleInput(data string) }

// goalDisposable is a component with dispose().
type goalDisposable interface{ Dispose() }

// goalCustomOptions is the options argument of ctx.ui.custom for the overlays.
type goalCustomOptions struct {
	overlay   bool
	anchor    string
	width     string
	minWidth  int
	maxHeight string
}

// goalCustomFactory is the factory passed to ctx.ui.custom: (tui, theme, keybindings, done) => Component.
type goalCustomFactory func(t goalTUI, theme goalTheme, done func(result any)) goalComponent

// goalComponent is a pi-tui Component (the Go interface is tui.Component).
type goalComponent interface {
	Render(width int) []string
	Invalidate()
}

// goalInputResult is `{ consume: true }`, returned by a terminal input listener (nil = undefined).
type goalInputResult struct{ consume bool }

// goalUI is the part of ctx.ui the widgets use.
type goalUI interface {
	// custom blocks until the component calls done and returns its result (nil for done()).
	custom(factory goalCustomFactory, opts goalCustomOptions) any
	setWorkingVisible(visible bool)
	onTerminalInput(cb func(data string) *goalInputResult) (unsubscribe func())
	notify(message, level string)
	input(title, placeholder string) (value string, ok bool)
}

// goalExtensionContext is the part of ExtensionContext the widgets use.
type goalExtensionContext interface {
	hasUI() bool
	cwd() string
	ui() goalUI
}

// ── Records and options ──

// auditorWidgetProgress is AuditorWidgetProgress. Optional strings are "" when undefined; the optional numbers are pointers.
type auditorWidgetProgress struct {
	currentTool          string
	currentToolArgs      string
	currentToolStartedAt *float64
	recentOutput         []string
	phase                string // running | tool_executing | producing_report | thinking | done
	elapsedMs            float64
	label                string
	percentage           *float64
	auditorLabel         string
}

// auditResultView is AuditResultView.
type auditResultView struct {
	verdict string // approved | disapproved | error
	report  string
}

type goalWidgetOptions struct {
	theme              goalTheme
	tui                goalTUI
	getGoal            func() *jsObject
	getOpenGoalCount   func() int
	getAuditorProgress func() *auditorWidgetProgress
	getSettings        func() goalSettings
	getDebugMode       func() bool
	getStalled         func() bool
	getExpanded        func() bool
	getLedgerEvents    func() []*jsObject
	getAuditResult     func() *auditResultView
}

func intPtr(n int) *int { return &n }

// dashboardKeybindingsOf is `settings.keybindings?.dashboard`: nil (the renderers' defaults) when no keybindings are set.
func dashboardKeybindingsOf(s goalSettings) *goalDashboardKeybindings {
	if s.keybindings.dashboard == (goalDashboardKeybindings{}) {
		return nil
	}
	return &s.keybindings.dashboard
}

// liveDisplayGoal is the live display record for the widget/status line: the accounted usage view when accounting is active for the goal.
func liveDisplayGoal(goal *jsObject, accounting interface {
	isActiveFor(goalID string) bool
	liveSeconds() float64
}) *jsObject {
	if goal == nil || gstr(goal, "status") != "active" || !accounting.isActiveFor(gstr(goal, "id")) {
		return goal
	}
	live := accounting.liveSeconds()
	if live == 0 {
		return goal
	}
	out := goal.clone()
	usage := newObject()
	if u := goal.obj("usage"); u != nil {
		usage = u.clone()
	}
	usage.set("activeSeconds", gnum(goal.obj("usage"), "activeSeconds")+live)
	out.set("usage", usage)
	return out
}

// makeGoalWidgetFactory builds the factory the host UI calls at render time.
func makeGoalWidgetFactory(opts goalWidgetFactoryOptions) func(t goalTUI, theme goalTheme) *goalWidgetComponent {
	return func(t goalTUI, theme goalTheme) *goalWidgetComponent {
		component := newGoalWidgetComponent(goalWidgetOptions{
			tui:                t,
			theme:              theme,
			getGoal:            opts.getGoal,
			getOpenGoalCount:   opts.getOpenGoalCount,
			getAuditorProgress: opts.getAuditorProgress,
			getSettings:        opts.getSettings,
			getDebugMode:       opts.getDebugMode,
			getStalled:         opts.getStalled,
			getExpanded:        opts.getExpanded,
			getLedgerEvents:    opts.getLedgerEvents,
			getAuditResult:     opts.getAuditResult,
		})
		if opts.componentRef != nil {
			opts.componentRef.current = component
		}
		return component
	}
}

type goalWidgetFactoryOptions struct {
	getGoal            func() *jsObject
	getOpenGoalCount   func() int
	getAuditorProgress func() *auditorWidgetProgress
	getSettings        func() goalSettings
	getDebugMode       func() bool
	getStalled         func() bool
	getExpanded        func() bool
	getLedgerEvents    func() []*jsObject
	getAuditResult     func() *auditResultView
	// componentRef receives the live widget so terminal shortcuts can scroll its viewports.
	componentRef *goalWidgetComponentRef
}

type goalWidgetComponentRef struct{ current *goalWidgetComponent }

// ── terminal-height bound ──

// boundWidgetRenderLines caps rendered lines to the terminal height minus the reserved chrome (head slice). A nil, zero or negative terminalRows is unbounded.
func boundWidgetRenderLines(lines []string, terminalRows *int) []string {
	if terminalRows == nil || *terminalRows <= 0 {
		return lines
	}
	cap := *terminalRows - widgetHeightReserve
	if cap < 1 {
		cap = 1
	}
	if len(lines) <= cap {
		return lines
	}
	return lines[:cap]
}

// stableHeightState is the latch state of applyStableHeightBound (nil = undefined).
type stableHeightState struct {
	stickyCap          *int
	stickyRegime       *string
	stickyTerminalRows *int
	stickyReserve      *int
}

// applyStableHeightBound keeps the rendered height constant per regime (spec 2026-08-11-stable-widget-height).
func applyStableHeightBound(lines []string, terminalRows *int, state *stableHeightState, regime string, reserve int) []string {
	if terminalRows == nil || *terminalRows <= 0 {
		return lines
	}
	cap := *terminalRows - reserve
	if cap < 1 {
		cap = 1
	}
	if state.stickyTerminalRows == nil || *state.stickyTerminalRows != *terminalRows {
		state.stickyCap = nil
		state.stickyTerminalRows = intPtr(*terminalRows)
	}
	if state.stickyReserve == nil || *state.stickyReserve != reserve {
		state.stickyCap = nil
		state.stickyReserve = intPtr(reserve)
	}
	if state.stickyRegime == nil || *state.stickyRegime != regime {
		state.stickyCap = nil
		r := regime
		state.stickyRegime = &r
	}
	if state.stickyCap == nil {
		n := len(lines)
		if cap < n {
			n = cap
		}
		state.stickyCap = intPtr(n)
	}
	sticky := *state.stickyCap
	if len(lines) > sticky {
		return lines[:sticky]
	}
	if len(lines) < sticky {
		padded := append([]string{}, lines...)
		for len(padded) < sticky {
			padded = append(padded, "")
		}
		return padded
	}
	return lines
}

// ── audit views ──

func (p *auditorWidgetProgress) asAuditorProgress() auditorProgress {
	return auditorProgress{
		currentTool:          p.currentTool,
		currentToolArgs:      p.currentToolArgs,
		currentToolStartedAt: p.currentToolStartedAt,
		recentOutput:         p.recentOutput,
		phase:                p.phase,
		elapsedMs:            p.elapsedMs,
		label:                p.label,
		percentage:           p.percentage,
	}
}

// renderAuditorWidgetLines is the structured audit dashboard (§15).
func renderAuditorWidgetLines(progress *auditorWidgetProgress, theme goalTheme, width int, showToolDetails bool, verdict string) []string {
	model := deriveAuditorDashboardModel(progress.asAuditorProgress(), deriveAuditorDashboardOptions{auditorLabel: progress.auditorLabel, verdict: verdict})
	return renderAuditorDashboard(model, theme, width, showToolDetails)
}

// renderAuditResultCardView is the approval / rejection result card for a finished audit (§15.4).
func renderAuditResultCardView(view *auditResultView, theme goalTheme, width int) []string {
	return renderAuditResultCard(deriveAuditResultCard(view.verdict, view.report), theme, width)
}

// goalWidgetLinesOptions is the options argument of renderGoalWidgetLines.
type goalWidgetLinesOptions struct {
	openGoalCount        int
	auditorProgress      *auditorWidgetProgress
	disableTasks         bool
	stalled              bool
	ledgerEvents         []*jsObject
	expanded             bool
	debug                bool
	model                *goalDashboardModel
	compactScrollOffset  *int
	expandedScrollOffset *int
	expandedTaskRows     *int
	keybindings          *goalDashboardKeybindings
	// terminalRows bounds the height (see boundWidgetRenderLines); nil = unbounded.
	terminalRows *int
}

func renderGoalWidgetLines(goal *jsObject, theme goalTheme, width int, options goalWidgetLinesOptions) []string {
	// When auditor progress is active, show the structured audit dashboard instead of the normal goal widget (§15.3).
	if options.auditorProgress != nil {
		return boundWidgetRenderLines(renderAuditorWidgetLines(options.auditorProgress, theme, width, options.expanded || options.debug, ""), options.terminalRows)
	}
	openGoalCount := options.openGoalCount
	if goal == nil {
		// Unfocused panel when open goals exist; nothing when the pool is empty.
		var lines []string
		if openGoalCount > 0 {
			lines = renderUnfocusedDashboard(openGoalCount, theme, width)
		} else {
			lines = []string{}
		}
		return boundWidgetRenderLines(lines, options.terminalRows)
	}
	safeWidth := width
	if safeWidth < 1 {
		safeWidth = 1
	}
	otherCount := openGoalCount - 1
	if otherCount < 0 {
		otherCount = 0
	}
	model := options.model
	if model == nil {
		model = deriveGoalDashboardModel(goal, goalDashboardModelOptions{
			focused:        true,
			otherOpenGoals: otherCount,
			ledgerEvents:   options.ledgerEvents,
			tasksDisabled:  options.disableTasks,
		})
	}
	if model == nil {
		return []string{}
	}
	var lines []string
	if options.expanded {
		lines = renderExpandedDashboard(model, theme, safeWidth, goalExpandedOptions{scrollOffset: options.expandedScrollOffset, rows: options.expandedTaskRows, keybindings: options.keybindings})
	} else {
		lines = renderCompactDashboard(model, theme, safeWidth, goalCompactOptions{scrollOffset: options.compactScrollOffset, keybindings: options.keybindings})
	}
	return boundWidgetRenderLines(clampLinesToWidth(lines, width), options.terminalRows)
}

// ── The component ──

type goalWidgetComponent struct {
	theme goalTheme
	tui   goalTUI

	getGoal            func() *jsObject
	getOpenGoalCount   func() int
	getAuditorProgress func() *auditorWidgetProgress
	getSettings        func() goalSettings
	getDebugMode       func() bool
	getStalled         func() bool
	getExpanded        func() bool
	getLedgerEvents    func() []*jsObject
	getAuditResult     func() *auditResultView

	// §9.6 scroll state.
	compactScrollOffset  int
	expandedScrollOffset int
	lastRenderWidth      int
	lastSeenGoalID       *string
	lastSeenLatestAt     string

	stableHeightState stableHeightState
}

func newGoalWidgetComponent(o goalWidgetOptions) *goalWidgetComponent {
	c := &goalWidgetComponent{
		theme: o.theme, tui: o.tui, getGoal: o.getGoal,
		getOpenGoalCount: o.getOpenGoalCount, getAuditorProgress: o.getAuditorProgress,
		getSettings: o.getSettings, getDebugMode: o.getDebugMode, getStalled: o.getStalled,
		getExpanded: o.getExpanded, getLedgerEvents: o.getLedgerEvents, getAuditResult: o.getAuditResult,
		lastRenderWidth: 100,
	}
	if c.getOpenGoalCount == nil {
		c.getOpenGoalCount = func() int {
			if c.getGoal() != nil {
				return 1
			}
			return 0
		}
	}
	if c.getAuditorProgress == nil {
		c.getAuditorProgress = func() *auditorWidgetProgress { return nil }
	}
	if c.getSettings == nil {
		c.getSettings = func() goalSettings { return goalSettings{} }
	}
	if c.getDebugMode == nil {
		c.getDebugMode = func() bool { return false }
	}
	if c.getStalled == nil {
		c.getStalled = func() bool { return false }
	}
	if c.getExpanded == nil {
		c.getExpanded = func() bool { return false }
	}
	if c.getLedgerEvents == nil {
		c.getLedgerEvents = func() []*jsObject { return nil }
	}
	if c.getAuditResult == nil {
		c.getAuditResult = func() *auditResultView { return nil }
	}
	return c
}

// isExpanded reports whether the dashboard is currently in expanded mode (§10).
func (c *goalWidgetComponent) isExpanded() bool { return c.getExpanded() }

func (c *goalWidgetComponent) update() { c.tui.requestRender() }

func (c *goalWidgetComponent) Invalidate() { c.tui.requestRender() }

// dockChildren is not a dock container; it exists so the widget is a distinct node (identity compare only).

func jsBool(b bool) string { return strconv.FormatBool(b) }

// renderDebugPanel renders the debug info panel when debug mode is active.
func (c *goalWidgetComponent) renderDebugPanel(width int) []string {
	t := c.theme
	lines := []string{}
	safeWidth := width
	if safeWidth < 20 {
		safeWidth = 20
	}
	dim := func(s string) { lines = append(lines, t.fg("dim", s)) }

	lines = append(lines, t.fg("dim", strings.Repeat("─", safeWidth)))
	lines = append(lines, t.fg("warning", "⊙ [DEBUG MODE]"))
	lines = append(lines, "")

	goal := c.getGoal()
	if goal != nil {
		usage := goal.obj("usage")
		dim("  id: " + gstr(goal, "id"))
		dim("  status: " + gstr(goal, "status"))
		dim("  objective: " + truncateText(gstr(goal, "objective"), 80))
		dim("  sisyphus: " + jsBool(gflag(goal, "sisyphus")))
		dim("  autoContinue: " + jsBool(gflag(goal, "autoContinue")))
		dim("  tokens: " + jsNumber(gnum(usage, "tokensUsed")))
		dim("  activeSeconds: " + jsNumber(gnum(usage, "activeSeconds")))
		dim("  createdAt: " + gstr(goal, "createdAt"))
		dim("  updatedAt: " + gstr(goal, "updatedAt"))
		if s := gstr(goal, "pauseReason"); s != "" {
			dim("  pauseReason: " + s)
		}
		if s := gstr(goal, "pauseSuggestedAction"); s != "" {
			dim("  pauseSuggestedAction: " + s)
		}
		if s := gstr(goal, "stopReason"); s != "" {
			dim("  stopReason: " + s)
		}
		if s := gstr(goal, "activePath"); s != "" {
			dim("  activePath: " + s)
		}
		if s := gstr(goal, "archivedPath"); s != "" {
			dim("  archivedPath: " + s)
		}
		if s := gstr(goal, "verificationContract"); s != "" {
			dim("  vContract: " + truncateText(s, 60))
		}
		// Task tree summary (from the shared dashboard model).
		model := deriveGoalDashboardModel(goal, goalDashboardModelOptions{focused: true, otherOpenGoals: 0})
		if model != nil && model.taskProgress != nil {
			dim("  tasks: " + strconv.Itoa(model.taskProgress.completed) + "/" + strconv.Itoa(model.taskProgress.total))
		}
		if model != nil && model.currentTask != nil {
			dim("  next: " + model.currentTask.id + " (" + truncateText(model.currentTask.title, 40) + ")")
		}
	} else {
		dim("  (no goal)")
	}

	lines = append(lines, "")
	dim("── Debug keybindings ──")
	dim("  Ctrl+Shift+X  Toggle debug mode")
	dim("  Ctrl+Shift+N  Create test goal")
	dim("  Ctrl+Shift+T  Inject sample tasks")
	dim("  Ctrl+Shift+R  Mock audit animation")
	dim("  Ctrl+Shift+O  Open proposal dialog")
	return lines
}

func (c *goalWidgetComponent) Render(width int) []string {
	c.lastRenderWidth = width
	if c.lastRenderWidth < 1 {
		c.lastRenderWidth = 1
	}
	// Terminal-height bound (spec 2026-08-10): read pi's terminal rows at render time; a TUI without a terminal renders unbounded.
	var terminalRows *int
	if tt, ok := c.tui.(goalTerminalTUI); ok {
		if rows, ok := tt.terminalRows(); ok {
			terminalRows = intPtr(rows)
		}
	}
	// Render the current branch unbounded (natural), then apply the stable-height bound (spec 2026-08-11).
	natural := c.renderNatural(width)
	regime := c.stableHeightRegime()
	reserve := widgetHeightReserve
	if r, ok := c.measureDockReserve(width); ok {
		reserve = r
	}
	return applyStableHeightBound(natural, terminalRows, &c.stableHeightState, regime, reserve)
}

// measureDockReserve measures the rendered height of the dock chrome around the widget (pending + status + editor + widgetBelow + footer)
// at the current width; ok is false (static reserve) when the TUI exposes no measurable dock.
func (c *goalWidgetComponent) measureDockReserve(width int) (int, bool) {
	dt, isDock := c.tui.(goalDockTUI)
	if !isDock {
		return 0, false
	}
	top := dt.dockChildren()
	if len(top) < 2 {
		return 0, false
	}
	// regular mode — top-level [document, pending, status, widgetAbove, editor, widgetBelow, footer] with the widget directly inside
	// widgetContainerAbove; fullscreen mode — top-level [transcript, dock] with the widget inside widgetContainerAbove inside the dock VStack.
	var dockList []any
	var widgetContainer any
	childrenOf := func(n any) ([]any, bool) {
		if cc, ok := n.(goalDockContainer); ok {
			return cc.dockChildren(), true
		}
		return nil, false
	}
	var self any = c
	found := false
	for _, ch := range top {
		kids, has := childrenOf(ch)
		if has {
			direct := false
			for _, k := range kids {
				if k == self {
					direct = true
					break
				}
			}
			if direct {
				dockList, widgetContainer, found = top, ch, true
				break
			}
		}
		for _, cc := range kids {
			nested, hasNested := childrenOf(cc)
			if !hasNested {
				continue
			}
			inside := false
			for _, k := range nested {
				if k == self {
					inside = true
					break
				}
			}
			if inside {
				dockList, widgetContainer, found = kids, cc, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return 0, false
	}
	total := 0
	for i, ch := range dockList {
		// Skip the document container / transcript and the widget's own container.
		if i == 0 && sameDockList(dockList, top) {
			continue
		}
		if ch == widgetContainer {
			continue
		}
		r, ok := ch.(goalDockRenderer)
		if !ok {
			continue
		}
		total += len(r.render(width))
	}
	// +1 slack so the strictest safety condition holds even at the boundary.
	return total + 1, true
}

func sameDockList(a, b []any) bool {
	return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0]
}

// renderNatural renders the current widget branch unbounded (no terminal-height bound).
func (c *goalWidgetComponent) renderNatural(width int) []string {
	settings := c.getSettings()
	safeWidth := width
	if safeWidth < 1 {
		safeWidth = 1
	}
	// §15.4: a finished audit shows its result card until cleared.
	if auditResult := c.getAuditResult(); auditResult != nil {
		return clampLinesToWidth(renderAuditResultCardView(auditResult, c.theme, width), width)
	}
	goal := c.getGoal()
	model := c.deriveModel(goal, settings)
	c.maybeReanchor(model)
	lines := renderGoalWidgetLines(c.getGoal(), c.theme, safeWidth, goalWidgetLinesOptions{
		openGoalCount:        c.getOpenGoalCount(),
		auditorProgress:      c.getAuditorProgress(),
		disableTasks:         settings.disableTasks,
		stalled:              c.getStalled(),
		expanded:             c.getExpanded(),
		ledgerEvents:         c.getLedgerEvents(),
		debug:                c.getDebugMode(),
		model:                model,
		compactScrollOffset:  intPtr(c.compactScrollOffset),
		expandedScrollOffset: intPtr(c.expandedScrollOffset),
		expandedTaskRows:     intPtr(expandedTaskViewportRows(c.lastRenderWidth)),
		keybindings:          dashboardKeybindingsOf(settings),
	})
	if c.getDebugMode() {
		lines = append(lines, c.renderDebugPanel(width)...)
	}
	return clampLinesToWidth(lines, width)
}

func (c *goalWidgetComponent) deriveModel(goal *jsObject, settings goalSettings) *goalDashboardModel {
	if goal == nil {
		return nil
	}
	other := c.getOpenGoalCount() - 1
	if other < 0 {
		other = 0
	}
	return deriveGoalDashboardModel(goal, goalDashboardModelOptions{
		focused:           true,
		otherOpenGoals:    other,
		ledgerEvents:      c.getLedgerEvents(),
		tasksDisabled:     settings.disableTasks,
		maxAutonomousRuns: settings.maxAutonomousRuns,
	})
}

// stableHeightRegime is the stable-height regime key: goal, status, widget state kind, expansion, debug, task setting and task presence.
func (c *goalWidgetComponent) stableHeightRegime() string {
	settings := c.getSettings()
	goal := c.getGoal()
	var stateKind string
	switch {
	case c.getAuditResult() != nil:
		stateKind = "result"
	case c.getAuditorProgress() != nil:
		stateKind = "audit"
	case goal != nil:
		stateKind = "focused"
	case c.getOpenGoalCount() > 0:
		stateKind = "unfocused"
	default:
		stateKind = "none"
	}
	id, status := "∅", "∅"
	hasTasks := false
	if goal != nil {
		id, status = gstr(goal, "id"), gstr(goal, "status")
		if tl := goal.obj("taskList"); tl != nil {
			if tasks, ok := tl.vals["tasks"].([]any); ok && len(tasks) > 0 {
				hasTasks = true
			}
		}
	}
	return strings.Join([]string{id, status, stateKind, jsBool(c.getExpanded()), jsBool(c.getDebugMode()), jsBool(settings.disableTasks), jsBool(hasTasks)}, "|")
}

// maybeReanchor is the §9.6 re-anchor rule: when the goal changes or a new task completes, reset both viewports to the anchored defaults.
func (c *goalWidgetComponent) maybeReanchor(model *goalDashboardModel) {
	latestAt := ""
	var tree []dashboardTaskNode
	var goalID *string
	if model != nil {
		tree = model.taskTree
		id := model.goalId
		goalID = &id
		if i := latestCompletedNodeIndex(tree); i >= 0 {
			latestAt = tree[i].completedAt
		}
	}
	same := (goalID == nil && c.lastSeenGoalID == nil) || (goalID != nil && c.lastSeenGoalID != nil && *goalID == *c.lastSeenGoalID)
	if same && latestAt == c.lastSeenLatestAt {
		return
	}
	c.lastSeenGoalID = goalID
	c.lastSeenLatestAt = latestAt
	if model == nil || len(tree) == 0 {
		c.compactScrollOffset = 0
		c.expandedScrollOffset = 0
		return
	}
	var topLevel []dashboardTaskNode
	for _, n := range tree {
		if n.depth == 0 {
			topLevel = append(topLevel, n)
		}
	}
	c.compactScrollOffset = anchoredScrollOffset(topLevel, compactTaskViewportRows(c.lastRenderWidth))
	c.expandedScrollOffset = anchoredScrollOffset(tree, expandedTaskViewportRows(c.lastRenderWidth))
}

// scrollTarget applies a navigation key to a viewport offset (shared by both handlers).
func widgetScrollOffset(key string, offset, rows, maxO int) int {
	switch key {
	case "up":
		offset--
	case "down":
		offset++
	case "pageUp":
		offset -= taskViewportPageSize(float64(rows))
	case "pageDown":
		offset += taskViewportPageSize(float64(rows))
	case "home":
		offset = 0
	case "end":
		offset = maxO
	}
	return offset
}

// handleCompactScrollKey scrolls the compact task list with a Ctrl+Shift chord (§9.6); true when the key was consumed.
func (c *goalWidgetComponent) handleCompactScrollKey(key string) bool {
	if c.getExpanded() {
		return false
	}
	settings := c.getSettings()
	model := c.deriveModel(c.getGoal(), settings)
	c.maybeReanchor(model)
	count := 0
	if model != nil {
		for _, n := range model.taskTree {
			if n.depth == 0 {
				count++
			}
		}
	}
	rows := compactTaskViewportRows(c.lastRenderWidth)
	if count <= rows {
		return false
	}
	offset := clampScrollOffset(float64(c.compactScrollOffset), count, rows)
	offset = widgetScrollOffset(key, offset, rows, maxScrollOffset(count, rows))
	offset = clampScrollOffset(float64(offset), count, rows)
	c.compactScrollOffset = offset
	c.tui.requestRender()
	return true
}

// handleNavigationKey handles a navigation key for the expanded dashboard, which is modal while open; true when the key was consumed.
func (c *goalWidgetComponent) handleNavigationKey(key string) bool {
	if !c.getExpanded() {
		return false
	}
	settings := c.getSettings()
	model := c.deriveModel(c.getGoal(), settings)
	c.maybeReanchor(model)
	count := 0
	if model != nil {
		count = len(model.taskTree)
	}
	if count == 0 {
		return false
	}
	rows := expandedTaskViewportRows(c.lastRenderWidth)
	maxO := maxScrollOffset(count, rows)
	if maxO <= 0 {
		return true
	}
	offset := clampScrollOffset(float64(c.expandedScrollOffset), count, rows)
	offset = widgetScrollOffset(key, offset, rows, maxO)
	offset = clampScrollOffset(float64(offset), count, rows)
	c.expandedScrollOffset = offset
	c.tui.requestRender()
	return true
}
