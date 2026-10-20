package pi_permission_system

import "github.com/MichaelKinsy/pigpen/components/pitui/widthx"

// Port of src/presentation/line-fitting.ts.

// FitLinesToWidth fits rendered lines to a terminal width, so each returned entry is a single visual row no wider than width.
//
// Long lines are wrapped rather than clipped so no content is lost; the final truncation guards the edge cases wrapping cannot
// split (a lone wide grapheme). A width of zero or less yields no rows. pi-tui's truncateToWidth default ellipsis is "...".
func FitLinesToWidth(lines []string, width int) []string {
	out := []string{}
	if width <= 0 {
		return out
	}
	for _, line := range lines {
		for _, wrapped := range widthx.WrapTextWithAnsi(line, width) {
			out = append(out, widthx.TruncateToWidth(wrapped, width, "...", false))
		}
	}
	return out
}
