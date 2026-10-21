package ui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

var (
	ansiSgrRe     = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	ansiOsc8Re    = regexp.MustCompile(`\x1b\]8;[^\x07\x1b]*(?:\x07|\x1b\\)`)
	fenceMarkerRe = regexp.MustCompile("^`{3}")
)

const (
	// BorderVerticalOverhead is the top + bottom border rows consumed by RenderBorderedBox.
	BorderVerticalOverhead = 2
	// BorderHorizontalOverhead is the left + right vertical bar columns consumed by RenderBorderedBox.
	BorderHorizontalOverhead = 2
	// BorderInnerPaddingHorizontal is the inner horizontal padding (1 col) between each border bar and content.
	BorderInnerPaddingHorizontal = 1
	// BoxMinContentWidth is the floor for the preview box's inner content width (CC parity).
	BoxMinContentWidth = 40
)

// StripFenceMarkers drops fenced-code-block marker lines from rendered markdown. upstream: stripFenceMarkers.
func StripFenceMarkers(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		clean := ansiOsc8Re.ReplaceAllString(ansiSgrRe.ReplaceAllString(line, ""), "")
		if !fenceMarkerRe.MatchString(clean) {
			out = append(out, line)
		}
	}
	return out
}

// RenderBorderedBox wraps lines in a 4-sided border with 1 col of inner padding; hidden > 0 replaces the bottom
// dash run with the " ✂ ── N lines hidden ── " indicator. upstream: renderBorderedBox.
func RenderBorderedBox(lines []string, width int, colorFn func(string) string, hidden int) []string {
	dashSpan := max(1, width-BorderHorizontalOverhead)
	contentInner := max(1, dashSpan-2*BorderInnerPaddingHorizontal)
	pad := strings.Repeat(" ", BorderInnerPaddingHorizontal)
	out := []string{colorFn("┌" + strings.Repeat("─", dashSpan) + "┐")}
	for _, line := range lines {
		padded := widthx.TruncateToWidth(line, contentInner, "", true)
		out = append(out, colorFn("│")+pad+padded+pad+colorFn("│"))
	}
	if hidden > 0 {
		indicator := " ✂ ── " + strconv.Itoa(hidden) + " lines hidden ── "
		indicatorLen := jsstring.Length(indicator)
		space := dashSpan - indicatorLen
		leftFill := strings.Repeat("─", max(0, space/2))
		rightFill := strings.Repeat("─", max(0, dashSpan-jsstring.Length(leftFill)-indicatorLen))
		out = append(out, colorFn("└"+leftFill+indicator+rightFill+"┘"))
	} else {
		out = append(out, colorFn("└"+strings.Repeat("─", dashSpan)+"┘"))
	}
	return out
}

// ComputeBoxDimensions computes box dimensions from content lines (CC parity: contentWidth = max(minWidth,
// widest rendered line), trailing whitespace stripped before measuring). upstream: computeBoxDimensions.
func ComputeBoxDimensions(contentLines []string, maxInnerWidth int) (innerWidth, boxWidth int) {
	widest := min(BoxMinContentWidth, maxInnerWidth)
	for _, line := range contentLines {
		if w := widthx.VisibleWidth(jsstring.TrimEnd(line)); w > widest {
			widest = w
		}
	}
	innerWidth = min(widest, maxInnerWidth)
	boxWidth = innerWidth + BorderHorizontalOverhead + 2*BorderInnerPaddingHorizontal
	return innerWidth, boxWidth
}
