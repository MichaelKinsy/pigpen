package pi_goal_x

import (
	"fmt"
	"strings"
)

// Shared bordered-dialog scaffold; ported from widgets/dialog-scaffold.ts.

// dialogInnerWidth is the inner content width between the ││ borders for a dialog at termWidth (maxInner defaults to 64 in the TS).
func dialogInnerWidth(termWidth, maxInner int) int {
	if termWidth > maxInner {
		termWidth = maxInner
	}
	return termWidth - 2
}

// borderedLine is one bordered line: exactly innerWidth visible chars between ││.
func borderedLine(accent func(string) string, innerWidth int, content string) string {
	fill := innerWidth - visibleWidth(content)
	pad := ""
	if fill > 0 {
		pad = strings.Repeat(" ", fill)
	}
	return accent("│") + content + pad + accent("│")
}

// horizontalRule is the ── run used by ┌/├/└ borders.
func horizontalRule(innerWidth int) string {
	if innerWidth < 0 {
		panic(fmt.Sprintf("Invalid count value: %d", innerWidth)) // "─".repeat(negative) throws a RangeError in JS
	}
	return strings.Repeat("─", innerWidth)
}
