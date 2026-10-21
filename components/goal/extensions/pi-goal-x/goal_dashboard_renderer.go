package pi_goal_x

import (
	"strconv"
	"strings"
)

// ported from widgets/goal-dashboard-renderer.ts: the unified dashboard renderer (compact, expanded, unfocused,
// audit). Pure presentation; never emits a line wider than the requested width.

// goalTheme is the small slice of the pi Theme the dashboard uses. color is a ThemeColor name
// (accent, warning, success, error, dim, muted, text, mdHeading).
type goalTheme interface {
	fg(color, text string) string
	bold(text string) string
}

const (
	dashH = "─"
	dashV = "│"
)

func dashFrame(theme goalTheme, value string) string { return theme.fg("muted", value) }

func dashFit(value string, width int) string {
	if visibleWidth(value) > width {
		return truncateToWidth(value, width, "…", false)
	}
	return value
}

func dashMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func dashMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func dashRepeat(s string, n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(s, n)
}

func boxHeader(theme goalTheme, width int, left string, right ...string) string {
	r0 := ""
	if len(right) > 0 {
		r0 = right[0]
	}
	inner := dashMax(4, width-2)
	l := dashFrame(theme, dashH) + " " + left
	r := ""
	if r0 != "" {
		r = " " + r0 + " " + dashFrame(theme, dashH)
	}
	fixed := visibleWidth(l) + visibleWidth(r)
	if fixed > inner-2 {
		budget := dashMax(4, inner-visibleWidth(r)-4)
		l2 := dashFrame(theme, dashH) + " " + dashFit(left, budget)
		fill := dashMax(1, inner-visibleWidth(l2)-visibleWidth(r))
		return dashFrame(theme, "╭") + l2 + dashFrame(theme, dashRepeat(dashH, fill)) + r + dashFrame(theme, "╮")
	}
	fill := dashMax(1, inner-fixed)
	return dashFrame(theme, "╭") + l + dashFrame(theme, dashRepeat(dashH, fill)) + r + dashFrame(theme, "╮")
}

func boxLine(theme goalTheme, width int, content string) string {
	inner := dashMax(2, width-2)
	contentFit := dashFit(content, inner-1)
	pad := dashMax(0, inner-1-visibleWidth(contentFit))
	return dashFrame(theme, dashV) + " " + contentFit + strings.Repeat(" ", pad) + dashFrame(theme, dashV)
}

func boxSectionRule(theme goalTheme, width int, label string, right ...string) string {
	r0 := ""
	if len(right) > 0 {
		r0 = right[0]
	}
	inner := dashMax(4, width-2)
	left := dashFrame(theme, dashH+" "+label+" ")
	r := ""
	if r0 != "" {
		r = " " + r0 + " "
	}
	l := left
	if visibleWidth(left)+visibleWidth(r) > inner {
		budget := dashMax(4, inner-visibleWidth(r)-3)
		l = dashFrame(theme, dashH+" "+dashFit(label, budget)+" ")
	}
	fill := dashMax(0, inner-visibleWidth(l)-visibleWidth(r))
	return dashFrame(theme, "├") + l + dashFrame(theme, dashRepeat(dashH, fill)) + r + dashFrame(theme, "┤")
}

func boxFooter(theme goalTheme, width int, content string, right ...string) string {
	r0 := ""
	if len(right) > 0 {
		r0 = right[0]
	}
	inner := dashMax(4, width-2)
	r := ""
	if r0 != "" {
		r = " " + r0 + " " + dashFrame(theme, dashH)
	}
	if content == "" && r == "" {
		return dashFrame(theme, "╰"+dashRepeat(dashH, inner)+"╯")
	}
	l := ""
	if content != "" {
		l = dashFrame(theme, dashH+" "+dashFit(content, dashMax(0, inner-4-visibleWidth(r))))
	}
	fill := dashMax(1, inner-visibleWidth(l)-visibleWidth(r))
	return dashFrame(theme, "╰") + l + dashFrame(theme, dashRepeat(dashH, fill)) + r + dashFrame(theme, "╯")
}

var dashStatusColor = map[string]string{
	dashRunning: "accent", dashIdle: "muted", dashPaused: "muted", dashBlocked: "error", dashBudgetLimited: "mdHeading", dashComplete: "success",
}

// statusLine: widget status line, footer-status parity: `goal: <label> [<usage>] (+N open)`.
func dashStatusLine(theme goalTheme, model *goalDashboardModel) string {
	color := dashStatusColor[model.status.code]
	usage := ""
	if model.usage.footerBits != "" {
		usage = " [" + model.usage.footerBits + "]"
	}
	open := ""
	if model.otherOpenGoals > 0 {
		open = " (+" + strconv.Itoa(model.otherOpenGoals) + " open)"
	}
	return theme.fg(color, "goal: "+model.status.footerLabel) + dashMuted(theme, usage+open)
}

func dashProgressBar(theme goalTheme, pct float64, barWidth int) string {
	safeBar := dashMax(2, barWidth)
	filled := dashMin(safeBar, dashMax(0, int(jsRound(float64(pct/100*float64(safeBar))))))
	return "[" + theme.fg("muted", strings.Repeat("█", filled)) + theme.fg("dim", strings.Repeat("░", safeBar-filled)) + "]"
}

type dashLayoutSpec struct {
	barWidth, headerBarWidth, subtaskBarWidth int
	showPath, showPauseAction                 bool
	footerHint                                string
}

func dashLayoutMode(width int) string {
	switch {
	case width >= 100:
		return "wide"
	case width >= 70:
		return "medium"
	case width >= 50:
		return "narrow"
	}
	return "minimal"
}

func dashSpecFor(mode string) dashLayoutSpec {
	switch mode {
	case "wide":
		return dashLayoutSpec{26, 8, 4, true, true, "Ctrl+Shift+T: expand tasks"}
	case "medium":
		return dashLayoutSpec{18, 6, 3, true, true, "Ctrl+Shift+T: expand tasks"}
	case "narrow":
		return dashLayoutSpec{12, 5, 2, false, false, "Ctrl+Shift+T: expand"}
	}
	return dashLayoutSpec{8, 4, 2, false, false, "Ctrl+Shift+T: expand"}
}

func dashMuted(theme goalTheme, v string) string   { return theme.fg("muted", v) }
func dashDim(theme goalTheme, v string) string     { return theme.fg("dim", v) }
func dashAccent(theme goalTheme, v string) string  { return theme.fg("accent", v) }
func dashSuccess(theme goalTheme, v string) string { return theme.fg("success", v) }
func dashAmber(theme goalTheme, v string) string   { return theme.fg("mdHeading", v) }

func dashTaskMarker(node dashboardTaskNode) (symbol, color string) {
	switch {
	case node.isCurrent:
		return "▸", "accent"
	case node.status == "complete":
		return "✓", "success"
	case node.status == "skipped":
		return "~", "muted"
	}
	return "·", "mdHeading"
}

func dashPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dashRenderTaskRow(theme goalTheme, node dashboardTaskNode, indent, available int) string {
	symbol, color := dashTaskMarker(node)
	indentText := strings.Repeat("  ", indent)
	prefix := indentText + symbol + " " + node.id + "  "
	contractMark := ""
	if node.verificationContract != "" {
		contractMark = dashDim(theme, " ☑")
	}
	titleBudget := dashMax(4, available-visibleWidth(prefix)-visibleWidth(contractMark))
	title := dashFit(node.title, titleBudget)
	markerText := theme.fg(color, symbol)
	idText := theme.fg(color, node.id)
	titleText := dashAmber(theme, title)
	if node.isCurrent {
		idText = dashAccent(theme, node.id)
		titleText = dashAccent(theme, title)
	}
	return indentText + markerText + " " + idText + "  " + titleText + contractMark
}

func dashRenderCompactTaskRows(theme goalTheme, nodes []dashboardTaskNode, viewport taskListViewport, available int) []string {
	rows := []string{}
	if viewport.hiddenAbove > 0 {
		rows = append(rows, dashMuted(theme, "↑ "+strconv.Itoa(viewport.hiddenAbove)+" more task"+dashPlural(viewport.hiddenAbove)))
	}
	end := dashMin(viewport.offset+viewport.rows, len(nodes))
	shown := nodes[dashMin(viewport.offset, end):end]
	idWidth := 0
	for _, n := range shown {
		if l := utf16Len(n.id); l > idWidth {
			idWidth = l
		}
	}
	idWidth = dashMin(10, idWidth)
	for _, node := range shown {
		symbol, color := dashTaskMarker(node)
		contractMark := ""
		if node.verificationContract != "" {
			contractMark = dashDim(theme, " ☑")
		}
		subtaskMark := ""
		if node.totalSubtasks > 0 {
			subtaskMark = dashMuted(theme, " ▸ "+strconv.Itoa(node.completedSubtasks)+"/"+strconv.Itoa(node.totalSubtasks))
		}
		id := jsPadEnd(node.id, idWidth)
		prefix := symbol + " " + id + "  "
		titleBudget := dashMax(4, available-visibleWidth(prefix)-visibleWidth(contractMark)-visibleWidth(subtaskMark))
		title := dashFit(node.title, titleBudget)
		markerText := theme.fg(color, symbol)
		idText := theme.fg(color, id)
		body := dashAmber(theme, title)
		if node.isCurrent {
			idText = dashAccent(theme, id)
			body = dashAccent(theme, title)
		}
		rows = append(rows, markerText+" "+idText+"  "+body+contractMark+subtaskMark)
	}
	if viewport.hiddenBelow > 0 {
		rows = append(rows, dashMuted(theme, "… +"+strconv.Itoa(viewport.hiddenBelow)+" more task"+dashPlural(viewport.hiddenBelow)))
	}
	return rows
}

func dashActivityMarker(item goalActivityItem) (symbol, color string) {
	switch item.marker {
	case "done":
		return "✓", "success"
	case "current":
		return "▸", "accent"
	case "skipped":
		return "~", "mdHeading"
	}
	return "·", "muted"
}

// goalCompactOptions: footerHint nil = default; scrollOffset nil = anchored; keybindings nil = defaults.
type goalCompactOptions struct {
	footerHint   *string
	scrollOffset *int
	keybindings  *goalDashboardKeybindings
}

type goalExpandedOptions struct {
	scrollOffset *int
	rows         *int
	keybindings  *goalDashboardKeybindings
}

func dashCollapseWS(s string) string { return wsRun.ReplaceAllString(s, " ") }

// renderCompactDashboard: the persistent summary above the editor while a goal is focused.
func renderCompactDashboard(model *goalDashboardModel, theme goalTheme, width int, opts goalCompactOptions) []string {
	safeWidth := dashMax(10, width)
	mode := dashLayoutMode(safeWidth)
	spec := dashSpecFor(mode)
	inner := safeWidth - 2
	lines := []string{}

	lines = append(lines, boxHeader(theme, safeWidth, dashAccent(theme, "pi-goal-x")+" "+dashFrame(theme, "─ "+model.title)))

	if model.status.code == "complete" {
		lines = append(lines, boxLine(theme, safeWidth, dashSuccess(theme, "✓")+" "+dashSuccess(theme, "All required work is complete.")))
	} else {
		lines = append(lines, boxLine(theme, safeWidth, dashStatusLine(theme, model)))
	}

	for _, row := range model.scheduling {
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, dashFit(row, inner))))
	}

	if model.budget != nil {
		fuel := "mdHeading"
		if model.budget.used >= model.budget.total {
			fuel = "error"
		}
		lines = append(lines, boxLine(theme, safeWidth, theme.fg(fuel, "⛽")+" "+dashMuted(theme, "Budget")+" "+theme.fg(fuel, formatBudget(model.budget.used, model.budget.total))))
	}

	topLevel := []dashboardTaskNode{}
	for _, n := range model.taskTree {
		if n.depth == 0 {
			topLevel = append(topLevel, n)
		}
	}
	compactRows := compactTaskViewportRows(safeWidth)
	listOverflows := len(topLevel) > compactRows
	if len(topLevel) > 0 {
		offset := anchoredScrollOffset(topLevel, compactRows)
		if opts.scrollOffset != nil {
			offset = *opts.scrollOffset
		}
		viewport := deriveTaskListViewport(len(topLevel), compactRows, float64(offset))
		headerLabel := "Tasks"
		headerRight := ""
		if model.taskProgress != nil {
			headerLabel = "Tasks · ✓" + strconv.Itoa(model.taskProgress.completed) + " done · " + strconv.Itoa(model.taskProgress.total-model.taskProgress.completed) + " open"
			headerRight = dashProgressBar(theme, float64(model.taskProgress.percentage), spec.headerBarWidth)
		}
		if model.currentTask != nil && model.currentTask.totalSubtasks > 0 && mode != "minimal" {
			subBar := dashProgressBar(theme, float64(model.currentTask.subtaskPercentage), spec.subtaskBarWidth)
			subWord := "Sub "
			if mode == "narrow" {
				subWord = ""
			}
			headerRight += dashMuted(theme, " · "+subWord) + dashMuted(theme, strconv.Itoa(model.currentTask.completedSubtasks)+"/"+strconv.Itoa(model.currentTask.totalSubtasks)) + " " + subBar
		}
		lines = append(lines, boxSectionRule(theme, safeWidth, headerLabel, headerRight))
		for _, row := range dashRenderCompactTaskRows(theme, topLevel, viewport, inner) {
			lines = append(lines, boxLine(theme, safeWidth, row))
		}
	}

	if model.currentTask == nil && model.taskProgress != nil && model.taskProgress.completed == model.taskProgress.total && model.status.code != "complete" {
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Current")+"  "+dashAccent(theme, "All tasks complete")))
	}

	if model.currentTask != nil {
		if mode == "minimal" {
			lines = append(lines, boxLine(theme, safeWidth, dashAccent(theme, "▸")+" "+dashAccent(theme, dashFit(model.currentTask.title, inner-3))))
		} else {
			titleBudget := inner - visibleWidth("Current  "+model.currentTask.id+" · ")
			lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Current")+"  "+dashAccent(theme, model.currentTask.id)+" · "+dashAccent(theme, dashFit(model.currentTask.title, titleBudget))))
		}
	}

	if model.goalVerificationContract != "" && mode != "minimal" {
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Verify")+"   "+dashFit(dashCollapseWS(model.goalVerificationContract), inner-9)))
	}

	if model.status.code == "blocked" {
		if model.status.reason != "" {
			lines = append(lines, boxLine(theme, safeWidth, theme.fg("error", "Blocker")+"  "+dashMuted(theme, dashFit(model.status.reason, inner-10))))
		}
		if spec.showPauseAction && model.status.suggestedAction != "" {
			lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Action")+"   "+dashFit(model.status.suggestedAction, inner-9)))
		}
	}

	if model.status.code == "paused" && model.status.reason != "" {
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Reason")+"   "+dashFit(model.status.reason, inner-9)))
	}

	if spec.showPath && model.filePath != "" {
		lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, "File     "+model.filePath)))
	}

	kb := defaultGoalKeybindings.dashboard
	if opts.keybindings != nil {
		kb = *opts.keybindings
	}
	toggleShortcut := formatGoalKeybinding(kb.toggleExpand)
	scrollShortcut := formatGoalKeybinding(kb.scrollUp) + "/" + formatGoalKeybinding(kb.scrollDown)
	usesDefaultScrollKeys := kb.scrollUp == defaultGoalKeybindings.dashboard.scrollUp && kb.scrollDown == defaultGoalKeybindings.dashboard.scrollDown
	scrollHint := scrollShortcut
	if usesDefaultScrollKeys {
		if mode == "minimal" {
			scrollHint = "↑↓"
		} else {
			scrollHint = "Ctrl+Shift+↑↓"
		}
	}
	wideish := mode == "wide" || mode == "medium"
	expandHint := toggleShortcut + ": expand"
	if wideish {
		expandHint = toggleShortcut + ": expand tasks"
	}
	var footerHint string
	switch {
	case opts.footerHint != nil:
		footerHint = *opts.footerHint
	case listOverflows && wideish:
		footerHint = toggleShortcut + ": expand · " + scrollHint + ": scroll"
	case listOverflows:
		footerHint = scrollHint + ": scroll"
	default:
		footerHint = expandHint
	}
	auditorDot := dashMuted(theme, "●")
	if model.auditorEnabled {
		auditorDot = dashSuccess(theme, "●")
	}
	right := auditorDot
	if wideish {
		right = dashFrame(theme, "Ctrl+Shift+A: toggle auditor") + " " + auditorDot
	}
	lines = append(lines, boxFooter(theme, safeWidth, footerHint, right))
	return lines
}

// renderExpandedDashboard: the full dashboard; rows/scrollOffset bound the task-tree window (default: whole tree).
func renderExpandedDashboard(model *goalDashboardModel, theme goalTheme, width int, opts goalExpandedOptions) []string {
	safeWidth := dashMax(10, width)
	mode := dashLayoutMode(safeWidth)
	spec := dashSpecFor(mode)
	inner := safeWidth - 2
	lines := []string{}

	lines = append(lines, boxHeader(theme, safeWidth, dashAccent(theme, "pi-goal-x")+" "+dashFrame(theme, "─ "+model.title)))
	lines = append(lines, boxLine(theme, safeWidth, dashStatusLine(theme, model)))
	for _, row := range model.scheduling {
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, dashFit(row, inner))))
	}

	if spec.showPath && model.filePath != "" {
		lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, "File: "+model.filePath)))
	}
	if model.budget != nil {
		lines = append(lines, boxLine(theme, safeWidth, theme.fg("mdHeading", "⛽")+" "+dashMuted(theme, "Budget "+formatBudget(model.budget.used, model.budget.total))))
	}

	if model.taskProgress != nil {
		lines = append(lines, boxSectionRule(theme, safeWidth, "Progress"))
		bar := dashProgressBar(theme, float64(model.taskProgress.percentage), dashMax(10, spec.barWidth+8))
		lines = append(lines, boxLine(theme, safeWidth, bar+" "+dashMuted(theme, strconv.Itoa(model.taskProgress.completed)+"/"+strconv.Itoa(model.taskProgress.total)+" tasks · "+strconv.Itoa(model.taskProgress.percentage)+"%")))
	}

	if len(model.taskTree) > 0 {
		lines = append(lines, boxSectionRule(theme, safeWidth, "Tasks"))
		totalRows := len(model.taskTree)
		rows := totalRows
		if opts.rows != nil {
			rows = *opts.rows
		}
		offset := anchoredScrollOffset(model.taskTree, rows)
		if opts.scrollOffset != nil {
			offset = *opts.scrollOffset
		}
		viewport := deriveTaskListViewport(totalRows, rows, float64(offset))
		if viewport.hiddenAbove > 0 {
			lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, "↑ "+strconv.Itoa(viewport.hiddenAbove)+" more task"+dashPlural(viewport.hiddenAbove))))
		}
		end := dashMin(viewport.offset+viewport.rows, totalRows)
		for _, node := range model.taskTree[dashMin(viewport.offset, end):end] {
			lines = append(lines, boxLine(theme, safeWidth, dashRenderTaskRow(theme, node, dashMin(node.depth, 6), inner)))
		}
		if viewport.hiddenBelow > 0 {
			lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, "… +"+strconv.Itoa(viewport.hiddenBelow)+" more task"+dashPlural(viewport.hiddenBelow))))
		}
	}

	if model.currentTask != nil {
		lines = append(lines, renderCurrentTaskBlock(model, theme, safeWidth, spec.barWidth)...)
	}

	if model.goalVerificationContract != "" {
		lines = append(lines, boxSectionRule(theme, safeWidth, "Verification"))
		lines = append(lines, dashWrappedBlock(theme, safeWidth, "", model.goalVerificationContract)...)
	}

	if len(model.recentActivity) > 0 {
		lines = append(lines, renderActivityBlock(model.recentActivity, theme, safeWidth)...)
	}

	kb := defaultGoalKeybindings.dashboard
	if opts.keybindings != nil {
		kb = *opts.keybindings
	}
	lines = append(lines, boxFooter(theme, safeWidth, "Esc/"+formatGoalKeybinding(kb.toggleExpand)+": collapse"))
	return lines
}

// renderCurrentTaskBlock: title, subtask progress, contract, evidence and the inferred note. barWidth default is 16.
func renderCurrentTaskBlock(model *goalDashboardModel, theme goalTheme, width int, barWidth ...int) []string {
	bw := 16
	if len(barWidth) > 0 {
		bw = barWidth[0]
	}
	safeWidth := dashMax(10, width)
	inner := safeWidth - 2
	ct := model.currentTask
	lines := []string{boxSectionRule(theme, safeWidth, "Current task")}
	lines = append(lines, boxLine(theme, safeWidth, dashAccent(theme, ct.id)+" · "+dashFit(ct.title, inner-visibleWidth(ct.id+" · "))))
	if ct.totalSubtasks > 0 {
		bar := dashProgressBar(theme, float64(ct.subtaskPercentage), dashMax(8, bw))
		lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Subtasks")+" "+bar+" "+strconv.Itoa(ct.completedSubtasks)+"/"+strconv.Itoa(ct.totalSubtasks)+" · "+strconv.Itoa(ct.subtaskPercentage)+"%"))
	}
	if ct.verificationContract != "" {
		lines = append(lines, dashWrappedBlock(theme, safeWidth, "Contract", ct.verificationContract)...)
	}
	if ct.evidence != "" {
		lines = append(lines, dashWrappedBlock(theme, safeWidth, "Evidence", ct.evidence)...)
	}
	if ct.inferred {
		lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, "Inferred from the first pending task — no persisted current task.")))
	}
	return lines
}

// renderActivityBlock: the recent-activity block, shared by the expanded dashboard and /goal-status.
func renderActivityBlock(items []goalActivityItem, theme goalTheme, width int) []string {
	safeWidth := dashMax(10, width)
	inner := safeWidth - 2
	lines := []string{boxSectionRule(theme, safeWidth, "Recent activity")}
	for _, item := range items {
		symbol, color := dashActivityMarker(item)
		lines = append(lines, boxLine(theme, safeWidth, theme.fg(color, symbol)+" "+dashFit(item.text, inner-4)))
	}
	return lines
}

// dashWrappedBlock wraps long contract/evidence text safely at the inner width.
func dashWrappedBlock(theme goalTheme, width int, label, text string) []string {
	inner := dashMax(4, width-2)
	prefix := ""
	if label != "" {
		prefix = label + ": "
	}
	indent := strings.Repeat(" ", visibleWidth(prefix))
	wrapped := wrapTextWithAnsi(jsTrim(dashCollapseWS(text)), dashMax(8, inner-visibleWidth(prefix)))
	lines := []string{}
	for i, segment := range wrapped {
		content := indent + segment
		if i == 0 {
			content = prefix + segment
		}
		lines = append(lines, boxLine(theme, width, dashMuted(theme, content)))
	}
	return lines
}

// renderUnfocusedDashboard: shown when open goals exist but none is focused.
func renderUnfocusedDashboard(openGoalCount int, theme goalTheme, width int) []string {
	safeWidth := dashMax(10, width)
	lines := []string{boxHeader(theme, safeWidth, dashFrame(theme, "pi-goal-x ─ Goal focus required"))}
	goals := strconv.Itoa(openGoalCount) + " open goals are available."
	if openGoalCount == 1 {
		goals = "1 open goal is available."
	}
	lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, goals)))
	lines = append(lines, boxLine(theme, safeWidth, dashMuted(theme, "Run /goal-focus to choose the goal for this session.")))
	lines = append(lines, boxFooter(theme, safeWidth, ""))
	return lines
}

// clampLinesToWidth clamps every rendered line to the terminal width.
func clampLinesToWidth(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		if visibleWidth(line) > width {
			out[i] = truncateToWidth(line, width, "…", false)
		} else {
			out[i] = line
		}
	}
	return out
}

var (
	dashCheckSymbol = map[string]string{"passed": "✓", "running": "◌", "pending": "·", "failed": "✗"}
	dashCheckColor  = map[string]string{"passed": "success", "running": "accent", "pending": "muted", "failed": "error"}
)

// renderAuditorDashboard: five check stages, a progress bar and the elapsed duration. Raw tools and recent output appear
// only with showToolDetails or when the audit finished with a failure.
func renderAuditorDashboard(model *auditorDashboardModel, theme goalTheme, width int, showToolDetails bool) []string {
	safeWidth := dashMax(10, width)
	inner := safeWidth - 2
	lines := []string{}
	duration := formatAuditElapsed(model.elapsedMs)
	lines = append(lines, boxHeader(theme, safeWidth, dashFrame(theme, "Independent completion audit ─ "+model.auditorLabel), dashFrame(theme, duration)))

	for _, check := range model.checks {
		lines = append(lines, boxLine(theme, safeWidth, theme.fg(dashCheckColor[check.state], dashCheckSymbol[check.state])+" "+check.label))
	}

	if model.percentage != nil {
		barWidth := dashMax(8, dashMin(inner-12, 30))
		bar := dashProgressBar(theme, float64(*model.percentage), barWidth)
		lines = append(lines, boxLine(theme, safeWidth, bar+" "+theme.fg("muted", strconv.Itoa(*model.percentage)+"%")))
	}

	if showToolDetails || model.verdict == "disapproved" || model.verdict == "error" {
		if model.currentTool != "" {
			args := ""
			if model.currentToolArgs != "" {
				args = " " + dashDim(theme, truncateText(model.currentToolArgs, dashMax(10, inner-20)))
			}
			lines = append(lines, boxLine(theme, safeWidth, dashAccent(theme, "tool")+" "+model.currentTool+args))
		}
		if len(model.recentOutput) > 0 {
			lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, dashRepeat("─", dashMax(4, inner-8)))))
			for i, output := range model.recentOutput {
				if i >= 3 {
					break
				}
				lines = append(lines, boxLine(theme, safeWidth, dashDim(theme, truncateText(output, dashMax(8, inner-4)))))
			}
		}
	}

	hint := ""
	if model.active {
		hint = "Esc: stop audit"
	}
	lines = append(lines, boxFooter(theme, safeWidth, hint))
	return lines
}

// renderAuditResultCard: APPROVED or CHANGES REQUIRED / ERROR.
func renderAuditResultCard(card auditResultCard, theme goalTheme, width int) []string {
	safeWidth := dashMax(10, width)
	inner := safeWidth - 2
	success := card.verdict == "approved"
	accentColor, symbol := "error", "✗"
	if success {
		accentColor, symbol = "success", "✓"
	}
	lines := []string{boxHeader(theme, safeWidth, dashFrame(theme, "Audit result ─ "+card.label))}
	for _, line := range card.lines {
		lines = append(lines, boxLine(theme, safeWidth, theme.fg(accentColor, symbol)+" "+dashFit(line, inner-4)))
	}
	lines = append(lines, boxFooter(theme, safeWidth, ""))
	return lines
}
