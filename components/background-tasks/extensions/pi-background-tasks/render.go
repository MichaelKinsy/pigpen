package pi_background_tasks

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Terminal rendering shared by the widget, the dashboard and the tool and message renderers. upstream: extensions/render.ts.

const (
	dashboardWidth                 = 96
	dashboardPaddingX              = 2
	dashboardPaddingY              = 1
	dashboardMinFrameRows          = 14
	dashboardFrameVerticalOverhead = 2 + dashboardPaddingY*2
	taskPaneMinWidth               = 30
	taskPaneMaxWidth               = 42
	widgetPaddingX                 = 1
	toolPreviewLines               = 12
	widgetCompactTasks             = 3
)

// uiTheme is what the renderers need of the host theme (sdk.UITheme satisfies it).
type uiTheme interface {
	Fg(token, text string) string
	Bg(token, text string) string
	Bold(text string) string
	Inverse(text string) string
}

func ansiGreen(text string) string  { return "\x1b[32m" + text + "\x1b[39m" }
func ansiYellow(text string) string { return "\x1b[33m" + text + "\x1b[39m" }

func clampIdx(v, lo, hi int) int { return max(lo, min(hi, v)) }

func vwidth(s string) int { return widthx.VisibleWidth(s) }

func truncW(text string, width int) string { return widthx.TruncateToWidth(text, width, "", false) }

// padAnsi truncates text to width and pads it with spaces.
func padAnsi(text string, width int) string {
	truncated := truncW(text, width)
	return truncated + strings.Repeat(" ", max(0, width-vwidth(truncated)))
}

// splitOutputLines is the dashboard's view of a log: its tail, at most dashboardOutputMaxLines lines.
func splitOutputLines(output string) []string {
	text := jsTrimEnd(tailText(output, settingNumber("logTailMaxChars", defaultLogTailMaxChars, ""), false))
	if len(text) == 0 {
		return []string{"(no output yet)"}
	}
	lines := splitLines(text)
	maxLines := int(math.Max(20, math.Floor(settingNumber("dashboardOutputMaxLines", 800, ""))))
	if len(lines) <= maxLines {
		return lines
	}
	omitted := fmt.Sprintf("%s %d older line(s) omitted from dashboard; use bg_task log or the Log file for full output", glyphsFor("").ellipsis, len(lines)-maxLines)
	return append([]string{omitted}, lines[len(lines)-maxLines:]...)
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func dashboardContentWidth(width int) int { return max(1, width-2-dashboardPaddingX*2) }

type frameOptions struct {
	paddingTop, paddingBottom int
}

func frameDashboard(lines []string, width int, theme uiTheme, title, right string, o frameOptions) []string {
	if width < 8 {
		return truncateAll(lines, width)
	}
	border := func(text string) string { return theme.Fg("borderAccent", text) }
	frame := glyphsFor("").frame
	contentWidth := dashboardContentWidth(width)
	blank := border(frame.v) + strings.Repeat(" ", width-2) + border(frame.v)
	var top string
	if title == "" {
		top = border(frame.tl) + border(strings.Repeat(frame.h, width-2)) + border(frame.tr)
	} else {
		rightPlain := ""
		if right != "" {
			rightPlain = " " + right + " "
		}
		titleBudget := max(1, width-2-vwidth(rightPlain)-1)
		titlePlain := " " + widthx.TruncateToWidth(title, max(1, titleBudget-2), glyphsFor("").ellipsis, false) + " "
		fill := max(1, width-2-vwidth(titlePlain)-vwidth(rightPlain))
		rightText := ""
		if right != "" {
			rightText = theme.Fg("dim", rightPlain)
		}
		top = border(frame.tl) + ansiGreen(titlePlain) + border(strings.Repeat(frame.h, fill)) + rightText + border(frame.tr)
	}
	framed := []string{top}
	for i := 0; i < o.paddingTop; i++ {
		framed = append(framed, blank)
	}
	for _, line := range lines {
		content := padAnsi(line, contentWidth)
		framed = append(framed, border(frame.v)+strings.Repeat(" ", dashboardPaddingX)+content+strings.Repeat(" ", dashboardPaddingX)+border(frame.v))
	}
	for i := 0; i < o.paddingBottom; i++ {
		framed = append(framed, blank)
	}
	framed = append(framed, border(frame.bl)+border(strings.Repeat(frame.h, width-2))+border(frame.br))
	return truncateAll(framed, width)
}

func truncateAll(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = truncW(l, width)
	}
	return out
}

func frameWidget(lines []string, width int, theme uiTheme) []string {
	safe := max(1, width)
	if safe < 8 {
		return truncateAll(lines, safe)
	}
	border := func(text string) string { return theme.Fg("borderAccent", text) }
	frame := glyphsFor("").frame
	contentWidth := max(1, safe-2-widgetPaddingX*2)
	out := []string{border(frame.tl) + border(strings.Repeat(frame.h, safe-2)) + border(frame.tr)}
	for _, line := range lines {
		out = append(out, border(frame.v)+strings.Repeat(" ", widgetPaddingX)+padAnsi(line, contentWidth)+strings.Repeat(" ", widgetPaddingX)+border(frame.v))
	}
	out = append(out, border(frame.bl)+border(strings.Repeat(frame.h, safe-2))+border(frame.br))
	return truncateAll(out, safe)
}

// wrapAnsiLines wraps every line of text to width.
func wrapAnsiLines(text string, width int) []string {
	target := max(1, width)
	var out []string
	for _, line := range splitLines(text) {
		wrapped := widthx.WrapTextWithAnsi(line, target)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		out = append(out, wrapped...)
	}
	return out
}

func bgTree(theme uiTheme, branch, cwd string) string {
	return theme.Fg("muted", treeGlyph(branch, cwd))
}

func bgToolLabel(theme uiTheme, label string) string { return theme.Fg("text", theme.Bold(label)) }

func bgStatusColor(status string) string {
	switch status {
	case statusRunning:
		return "warning"
	case statusCompleted:
		return "success"
	case statusFailed, statusTimedOut:
		return "error"
	}
	return "muted"
}

func bgStatusIcon(status string, theme uiTheme) string {
	g := glyphsFor("")
	ascii := glyphStyle("") == "ascii"
	switch status {
	case statusRunning:
		return theme.Fg("warning", strings.TrimSpace(g.bullet))
	case statusCompleted:
		if ascii {
			return theme.Fg("success", g.ok)
		}
		return theme.Fg("success", iconCheck)
	case statusFailed, statusTimedOut:
		if ascii {
			return theme.Fg("error", g.fail)
		}
		return theme.Fg("error", iconTimes)
	}
	if ascii {
		return theme.Fg("muted", "-")
	}
	return theme.Fg("muted", "■")
}

func bgStatusText(s *snapshot, theme uiTheme) string {
	return theme.Fg(bgStatusColor(s.Status), summarizeTaskStatus(s.Status, s.ExitCode, s.TerminationReason))
}

func wakeupsLine(s *snapshot) string {
	output := "no"
	if s.NotifyOnOutput {
		output = "yes"
		if s.NotifyPattern != "" {
			output = s.NotifyPattern
		}
	}
	exit := "no"
	if s.NotifyOnExit {
		exit = "yes"
	}
	mode := s.NotifyMode
	if mode == "" {
		mode = notifyAlways
	}
	dedupe := ""
	if s.DedupeKey != "" {
		dedupe = ", dedupeKey=" + s.DedupeKey
	}
	return fmt.Sprintf("exit=%s, output=%s, mode=%s%s", exit, output, mode, dedupe)
}

func renderTaskDetails(task *snapshot, theme uiTheme, cwd string) []string {
	current := latestSnapshot(task)
	if current == nil {
		current = task
	}
	mid, last := bgTree(theme, "├", cwd), bgTree(theme, "└", cwd)
	lines := []string{
		mid + theme.Fg("muted", "Status") + ": " + bgStatusText(current, theme) + " " + theme.Fg("dim", fmt.Sprintf("· pid %d", current.Pid)),
		mid + theme.Fg("muted", "Title") + ": " + taskDisplayName(current.Title, current.Command),
		mid + theme.Fg("muted", "Command") + ": " + current.Command,
		mid + theme.Fg("muted", "Cwd") + ": " + current.Cwd,
		mid + theme.Fg("muted", "Log") + ": " + current.LogFile,
	}
	if current.Status == statusRunning && current.ExpiresAt != nil {
		lines = append(lines, mid+theme.Fg("muted", "Timeout")+": "+formatRelativeTime(*current.ExpiresAt, float64(clockNow())))
	}
	return append(lines, last+theme.Fg("muted", "Wakeups")+": "+wakeupsLine(current))
}

func toolRenderMode(cwd string) string {
	return settingEnum("toolRenderMode", []string{"compact", "stacked"}, "stacked", cwd)
}

func outputLineLimit(cwd string) float64 {
	return math.Max(1, math.Floor(settingNumber("toolExpandedLogLines", 80, cwd)))
}

// backgroundRule is a muted rule: the first theme token that styles differently from plain text, else a grey escape.
func backgroundRule(theme uiTheme, width int) string {
	rule := strings.Repeat(glyphsFor("").line, max(1, width))
	plain := theme.Fg("text", rule)
	for _, token := range []string{"borderMuted", "muted", "dim"} {
		if styled := theme.Fg(token, rule); styled != rule && styled != plain {
			return styled
		}
	}
	return "\x1b[90m" + rule + "\x1b[39m"
}

func renderRuledBackgroundMessage(text string, theme uiTheme, width int) []string {
	rule := backgroundRule(theme, width)
	return append(append([]string{rule}, wrapAnsiLines(text, width)...), rule)
}

func snapshotOf(v any) *snapshot {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var s snapshot
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// renderTaskEventMessage draws a wake message of the `kendex-background-tasks:event` type.
func renderTaskEventMessage(message map[string]any, expanded bool, theme uiTheme, width int) []string {
	details, _ := message["details"].(map[string]any)
	eventType, _ := details["eventType"].(string)
	tail, hasTail := details["outputTail"].(string)
	taskRaw := snapshotOf(details["task"])
	if (eventType != "output" && eventType != "exit") || taskRaw == nil || !hasTail {
		content := "Background task update"
		if c, ok := message["content"]; ok && c != nil {
			content = fmt.Sprint(c)
		}
		return renderRuledBackgroundMessage(content, theme, width)
	}
	task := latestSnapshot(taskRaw)
	if task == nil {
		task = taskRaw
	}
	name := taskDisplayName(task.Title, task.Command)
	if !expanded {
		prefix := theme.Fg("accent", "●")
		label := theme.Fg("toolTitle", theme.Bold("Background task output"))
		if eventType == "exit" {
			prefix = theme.Fg("success", "●")
			label = theme.Fg("toolTitle", theme.Bold("Background task ")) + theme.Fg("success", "finished")
		}
		return renderRuledBackgroundMessage(prefix+" "+label+" "+theme.Fg("accent", task.ID)+theme.Fg("dim", " · "+compactText(name, 64)+" · ctrl+o to expand"), theme, width)
	}
	headingLabel := bgToolLabel(theme, "Background task output ")
	headingIcon := theme.Fg("accent", "●")
	if eventType == "exit" {
		headingLabel = theme.Fg("toolTitle", theme.Bold("Background task ")) + theme.Fg("success", "finished ")
		headingIcon = theme.Fg("success", "●")
	}
	cwd := ""
	lines := []string{headingIcon + " " + headingLabel + theme.Fg("accent", task.ID) + theme.Fg("dim", " · "+compactText(name, 72))}
	lines = append(lines, renderTaskDetails(task, theme, cwd)...)
	if pattern, _ := details["matchedPattern"].(string); pattern != "" {
		lines = append(lines, bgTree(theme, "└", cwd)+theme.Fg("muted", "Pattern")+": "+pattern)
	}
	limit := outputLineLimit("")
	out, hidden, total := takeTailLines(tail, limit)
	lines = append(lines, "", theme.Fg("accent", theme.Bold("Recent output")))
	if hidden > 0 {
		lines = append(lines, bgTree(theme, "│", cwd)+theme.Fg("muted", fmt.Sprintf("… %d older line(s); full log: %s", hidden, task.LogFile)))
	}
	if len(out) == 0 {
		out = []string{"(no output yet)"}
	}
	for _, line := range out {
		lines = append(lines, bgTree(theme, "│", cwd)+theme.Fg("dim", line))
	}
	if float64(total) >= limit {
		lines = append(lines, bgTree(theme, "└", cwd)+theme.Fg("muted", "Full background log: "+task.LogFile))
	}
	return renderRuledBackgroundMessage(strings.Join(lines, "\n"), theme, width)
}

func bgToolAction(args map[string]any, details map[string]any) string {
	if a, ok := details["action"].(string); ok {
		return a
	}
	if a, ok := args["action"].(string); ok {
		return a
	}
	return "status"
}

func renderBgToolPartial(args map[string]any, theme uiTheme, width int) []string {
	action := bgToolAction(args, nil)
	target := "tasks"
	if v, ok := args["id"]; ok && v != nil {
		target = fmt.Sprint(v)
	} else if v, ok := args["pid"]; ok && v != nil {
		target = fmt.Sprint(v)
	}
	verb := "checking"
	switch action {
	case "spawn":
		verb = "starting"
	case "log":
		verb = "tailing"
	case "stop":
		verb = "stopping"
	case "clear":
		verb = "clearing"
	}
	title := target
	if action == "spawn" {
		t, _ := args["title"].(string)
		if t == "" {
			t, _ = args["command"].(string)
		}
		if t == "" {
			t = "background task"
		}
		title = compactText(t, 72)
	}
	return wrapAnsiLines(theme.Fg("warning", "● ")+bgToolLabel(theme, "Background task "+verb)+" "+theme.Fg("accent", title)+theme.Fg("dim", "…"), width)
}

func renderBgLogResult(task *snapshot, output string, theme uiTheme, expanded bool, cwd string) string {
	if task != nil {
		if l := latestSnapshot(task); l != nil {
			task = l
		}
	}
	taskLabel := theme.Fg("accent", "log")
	if task != nil {
		taskLabel = theme.Fg("accent", task.ID) + " " + bgStatusText(task, theme)
	}
	limit := float64(toolPreviewLines)
	if expanded {
		limit = outputLineLimit(cwd)
	}
	outLines, hidden, _ := takeTailLines(output, limit)
	count := lineCount(output)
	plural := "s"
	if count == 1 {
		plural = ""
	}
	hint := " · ctrl+o to expand"
	if expanded {
		hint = ""
	}
	text := theme.Fg("accent", "● ") + bgToolLabel(theme, "Background log ") + taskLabel + theme.Fg("dim", fmt.Sprintf(" · %d line%s%s", count, plural, hint))
	if expanded && task != nil {
		text += "\n" + strings.Join(renderTaskDetails(task, theme, cwd), "\n")
	}
	switch {
	case expanded && output != "":
		if hidden > 0 {
			where := "available in details"
			if task != nil {
				where = task.LogFile
			}
			text += "\n" + bgTree(theme, "│", cwd) + theme.Fg("muted", fmt.Sprintf("… %d older line(s); full log: %s", hidden, where))
		}
		var rows []string
		for _, line := range outLines {
			rows = append(rows, bgTree(theme, "│", cwd)+theme.Fg("dim", line))
		}
		text += "\n" + strings.Join(rows, "\n")
		if task != nil {
			text += "\n" + bgTree(theme, "└", cwd) + theme.Fg("muted", "Full background log: "+task.LogFile)
		}
	case !expanded && output != "" && toolRenderMode(cwd) == "stacked":
		if len(outLines) > 0 {
			text += "\n" + bgTree(theme, "└", cwd) + theme.Fg("muted", compactText(outLines[len(outLines)-1], 120))
		}
	}
	return text
}

// renderBgToolResult draws a bg_task or bg_status result; nil lines draw nothing (the list action).
func renderBgToolResult(raw string, details map[string]any, isError, isPartial, expanded bool, args map[string]any, cwd string, theme uiTheme, width int) []string {
	if isPartial {
		return renderBgToolPartial(args, theme, width)
	}
	action := bgToolAction(args, details)
	if isError {
		first := splitLines(raw)[0]
		if first == "" {
			first = "background task failed"
		}
		return wrapAnsiLines(theme.Fg("error", iconTimes+" ")+bgToolLabel(theme, "Background task")+" "+theme.Fg("error", first), width)
	}
	task := snapshotOf(details["task"])
	switch action {
	case "list":
		return nil
	case "spawn":
		if task == nil {
			return wrapAnsiLines(theme.Fg("warning", "● ")+bgToolLabel(theme, "Background task started"), width)
		}
		hint := " · ctrl+o to expand"
		if expanded {
			hint = ""
		}
		text := theme.Fg("warning", "● ") + bgToolLabel(theme, "Background task started ") + theme.Fg("accent", task.ID) +
			theme.Fg("dim", fmt.Sprintf(" · pid %d · %s%s", task.Pid, compactText(taskDisplayName(task.Title, task.Command), 72), hint))
		if expanded {
			text += "\n" + strings.Join(renderTaskDetails(task, theme, cwd), "\n")
		}
		return wrapAnsiLines(text, width)
	case "log":
		return wrapAnsiLines(renderBgLogResult(task, raw, theme, expanded, cwd), width)
	case "stop":
		if task != nil {
			if l := latestSnapshot(task); l != nil {
				task = l
			}
		}
		label := theme.Fg("muted", compactText(raw, 80))
		if task != nil {
			label = theme.Fg("accent", task.ID) + " " + bgStatusText(task, theme)
		}
		text := theme.Fg("warning", "● ") + bgToolLabel(theme, "Background task stop ") + label
		if expanded && task != nil {
			text += "\n" + strings.Join(renderTaskDetails(task, theme, cwd), "\n")
		}
		return wrapAnsiLines(text, width)
	case "clear":
		removed, _ := details["removed"].(float64)
		return wrapAnsiLines(theme.Fg("success", "● ")+bgToolLabel(theme, "Background tasks cleared")+theme.Fg("dim", fmt.Sprintf(" · removed %d finished", int(removed))), width)
	}
	if raw != "" {
		return wrapAnsiLines(raw, width)
	}
	return nil
}

func activePill(theme uiTheme, label string) string {
	return theme.Fg("accent", theme.Inverse(theme.Bold(label)))
}

func inactivePill(theme uiTheme, label string) string {
	return theme.Bg("selectedBg", theme.Fg("accent", label))
}

// ── the host's renderers ───────────────────────────────────────────────────────────────────────────────────────────────

// renderToolCall is the original's renderCall: an empty component, so the card shows only the result.
func renderToolCall(sdk.Context, map[string]any, sdk.ToolRenderContext, int) ([]string, error) {
	return []string{}, nil
}

// renderToolResult is renderBgToolResult for the host: the text of the result, its details and the card's state.
func renderToolResult(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) ([]string, error) {
	raw := ""
	for _, part := range result.Content {
		if part["type"] == "text" {
			raw, _ = part["text"].(string)
			break
		}
	}
	details, _ := result.Details.(map[string]any)
	lines := renderBgToolResult(raw, details, rc.IsError, options.IsPartial, options.Expanded, rc.Args, rc.Cwd, ctx.UITheme(), width)
	if lines == nil {
		lines = []string{}
	}
	return lines, nil
}

// renderEventMessage is the renderer of the `kendex-background-tasks:event` message.
func renderEventMessage(ctx sdk.Context, message map[string]any, options sdk.MessageRenderOptions, width int) ([]string, error) {
	return renderTaskEventMessage(message, options.Expanded, ctx.UITheme(), width), nil
}
