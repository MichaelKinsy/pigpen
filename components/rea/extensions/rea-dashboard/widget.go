package readashboard

import (
	"fmt"
	"sort"
	"time"
)

// maxWidgetLines is the most rows the widget uses above the editor.
const maxWidgetLines = 5

// fmtDuration is a compact elapsed time.
func fmtDuration(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// tailFit keeps the end of s within w cells: a path's last segments say more than its first.
func tailFit(s string, w int) string {
	s = sanitize(s)
	if textWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return truncate(s, w)
	}
	runes := []rune(s)
	used := 0
	i := len(runes)
	for i > 0 && used+cellWidth(runes[i-1]) <= w-1 {
		i--
		used += cellWidth(runes[i])
	}
	return "…" + string(runes[i:])
}

// callStatusGlyph is the icon and style of a call; running calls turn with the clock.
func callStatusGlyph(c call, now time.Time) (string, string) {
	switch c.Status {
	case statusOK:
		return "✓", styGreen
	case statusError:
		return "✗", styRed
	}
	frames := []string{"◐", "◓", "◑", "◒"}
	return frames[max(int(now.Sub(c.Started)/time.Second), 0)%len(frames)], styCyan
}

// callElapsed is the running time of a live call or the duration of a finished one.
func callElapsed(c call, now time.Time) time.Duration {
	if c.Status == statusRunning {
		return now.Sub(c.Started)
	}
	return c.Duration
}

// callLine draws one call: status, tool, target, elapsed and, once finished, the evidence and limitation counts.
func callLine(c call, now time.Time, w int, withCounts bool) []seg {
	glyph, style := callStatusGlyph(c, now)
	prefix := " "
	if c.nested() {
		prefix = " ↳"
	}
	right := fmtDuration(callElapsed(c, now))
	if withCounts && c.Status != statusRunning && c.Counted {
		right += fmt.Sprintf(" · ev %d · lim %d", c.Evidence, c.Limitations)
	}
	left := prefix + glyph + " " + c.Tool
	segs := []seg{{prefix, styDim}, {glyph, style}, {" " + c.Tool, styBold}}
	space := w - textWidth(left) - textWidth(right) - 4
	if c.Target != "" && space >= 6 {
		segs = append(segs, seg{"  " + tailFit(c.Target, space), styDim})
	}
	return append(segs, seg{"  " + right, ""})
}

// widgetLines is the live widget: the running REA calls and the most recent ones, in start order, in at most
// maxWidgetLines rows. With no calls it is empty and the widget is hidden.
func widgetLines(m model, width int) []string {
	if len(m.Calls) == 0 {
		return nil
	}
	width = max(width, 1)
	slots := maxWidgetLines
	overflow := len(m.Calls) > slots
	if overflow {
		slots--
	}
	// Running calls first, then the newest finished ones, shown in start order.
	var picked []int
	for i := len(m.Calls) - 1; i >= 0 && len(picked) < slots; i-- {
		if m.Calls[i].Status == statusRunning {
			picked = append(picked, i)
		}
	}
	for i := len(m.Calls) - 1; i >= 0 && len(picked) < slots; i-- {
		if m.Calls[i].Status != statusRunning {
			picked = append(picked, i)
		}
	}
	sort.Ints(picked)
	lines := make([]string, 0, maxWidgetLines)
	for _, i := range picked {
		lines = append(lines, renderSegs(callLine(m.Calls[i], m.Now, width, true), width))
	}
	if overflow {
		hidden := len(m.Calls) - len(picked)
		lines = append(lines, renderSegs([]seg{{fmt.Sprintf(" … %d earlier REA calls · /rea or alt+r", hidden), styDim}}, width))
	}
	return lines
}
