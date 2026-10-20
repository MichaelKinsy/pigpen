package pi_goal_x

import (
	"strings"

	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Ported from widgets/goal-escape-dialog.ts.

// escapeDialogResult is EscapeDialogResult.
type escapeDialogResult = string

const (
	escapeCompleteWithoutAudit escapeDialogResult = "complete_without_audit"
	escapeContinueWorking      escapeDialogResult = "continue_working"
)

type escapeDialogOption struct {
	label       string
	value       escapeDialogResult
	description string
}

var escapeDialogOptions = []escapeDialogOption{
	{"Mark complete without audit", escapeCompleteWithoutAudit, "Bypass the auditor and mark the goal complete now."},
	{"Continue working", escapeContinueWorking, "Resume work on the goal. It stays active; the audit will not run this turn."},
}

// escapeDialogComponent is the component built by showEscapeDialog's factory.
type escapeDialogComponent struct {
	ctx                    goalDialogContext
	tui                    goalDialogTUI
	theme                  dialogTheme
	done                   func(any)
	objective              string
	wasHardwareCursorShown bool
	selectedIndex          int
	cancelled              bool
}

// showEscapeDialog shows a confirmation dialog when the user presses Escape during a completion audit and returns the choice.
// Headless contexts return "continue_working", the safe default.
func showEscapeDialog(ctx goalDialogContext, goalObjective string) escapeDialogResult {
	if !ctx.hasUI() {
		return escapeContinueWorking
	}
	result, err := ctx.ui().custom(func(t goalDialogTUI, theme dialogTheme, done func(any)) goalDialogComponent {
		c := &escapeDialogComponent{ctx: ctx, tui: t, theme: theme, done: done, objective: goalObjective, selectedIndex: 1}
		c.wasHardwareCursorShown = t.getShowHardwareCursor()
		t.setShowHardwareCursor(false)
		// Pause pi's working spinner for the dialog duration: its ~80ms re-renders snap a scrolled-up user back to the bottom.
		ctx.ui().setWorkingVisible(false)
		return c
	}, goalCustomOptions{overlay: true, anchor: "center", width: "70%", minWidth: 50, maxHeight: "50%"})
	if err != nil {
		return escapeContinueWorking
	}
	s, _ := result.(string)
	return s
}

func (c *escapeDialogComponent) SetFocused(bool) {}

func (c *escapeDialogComponent) dispose() {
	c.tui.setShowHardwareCursor(c.wasHardwareCursorShown)
	c.ctx.ui().setWorkingVisible(true)
}

func (c *escapeDialogComponent) Invalidate() {}

func (c *escapeDialogComponent) Render(width int) []string {
	termWidth := width
	if termWidth > 80 {
		termWidth = 80
	}
	innerWidth := dialogInnerWidth(termWidth, 64)
	accent := func(s string) string { return c.theme.fg("accent", s) }
	dim := func(s string) string { return c.theme.fg("dim", s) }
	warning := func(s string) string { return c.theme.fg("warning", s) }
	line := func(content string) string { return borderedLine(accent, innerWidth, content) }

	horizLine := horizontalRule(innerWidth)
	lines := []string{}
	p := "  "

	lines = append(lines, accent("┌"+horizLine+"┐"))
	headerContent := p + c.theme.bold("Audit interrupted by Escape") + dim("  (continue = default)")
	lines = append(lines, line(truncateToWidth(headerContent, innerWidth, "…", false)))
	truncatedObjective := truncateToWidth(c.objective, innerWidth-14, "…", false)
	lines = append(lines, line(p+dim("Goal: ")+dim(truncatedObjective)))
	lines = append(lines, accent("├"+horizLine+"┤"))

	for i, opt := range escapeDialogOptions {
		isSelected := i == c.selectedIndex && !c.cancelled
		marker := "  "
		if isSelected {
			marker = "▸ "
		}
		label := opt.label
		if isSelected {
			label = warning(opt.label)
		}
		truncLabel := truncateToWidth(label, innerWidth-6, "…", false)
		lines = append(lines, line(p+marker+truncLabel))
		if isSelected && opt.description != "" {
			desc := dim(opt.description)
			truncDesc := truncateToWidth(desc, innerWidth-10, "…", false)
			lines = append(lines, line(p+strings.Repeat(" ", 4)+truncDesc))
		}
	}

	lines = append(lines, accent("├"+horizLine+"┤"))
	footerText := dim("Enter to select  ·  ↑↓ to navigate  ·  Esc = continue working")
	truncFooter := truncateToWidth(footerText, innerWidth-2, "…", false)
	lines = append(lines, line(p+truncFooter))
	lines = append(lines, accent("└"+horizLine+"┘"))
	return lines
}

func (c *escapeDialogComponent) HandleInput(data string) {
	n := len(escapeDialogOptions)
	switch {
	case pitui.MatchesKey(data, "up"):
		c.selectedIndex = (c.selectedIndex - 1 + n) % n
		c.tui.requestRender()
	case pitui.MatchesKey(data, "down"):
		c.selectedIndex = (c.selectedIndex + 1) % n
		c.tui.requestRender()
	case pitui.MatchesKey(data, "enter"):
		c.cancelled = false
		c.done(escapeDialogOptions[c.selectedIndex].value)
	case pitui.MatchesKey(data, "escape"):
		c.cancelled = true
		c.done(escapeContinueWorking)
	}
}
