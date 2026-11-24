package readashboard

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Everything the dashboard draws that came from outside (tool arguments, REA results, doctor output, file
// names) passes through sanitize first, so an escape sequence in a target name cannot reach the terminal.

// SGR codes for the few styles the dashboard uses.
const (
	styBold   = "1"
	styDim    = "2"
	styRev    = "7"
	styGreen  = "32"
	styRed    = "31"
	styYellow = "33"
	styCyan   = "36"
)

// seg is a run of text with one style ("" for none).
type seg struct {
	text  string
	style string
}

func styled(text, style string) string {
	if style == "" || text == "" {
		return text
	}
	return "\x1b[" + style + "m" + text + "\x1b[0m"
}

// sanitize replaces control characters, including ESC and newlines, with spaces.
func sanitize(s string) string {
	clean := true
	for _, r := range s {
		if isControl(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 || r == 0x2028 || r == 0x2029
}

// cellWidth is the number of terminal cells r occupies.
func cellWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x300:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// textWidth is the width of s in terminal cells.
func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += cellWidth(r)
	}
	return n
}

// truncate cuts s to at most w cells, ending in an ellipsis when it cut anything.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	used := 0
	end := 0
	for i, r := range s {
		cw := cellWidth(r)
		if used+cw > w-1 {
			end = i
			break
		}
		used += cw
		end = i + utf8.RuneLen(r)
	}
	return s[:end] + "…"
}

// padTo pads s with spaces to w cells; a longer s is truncated.
func padTo(s string, w int) string {
	s = truncate(s, w)
	if gap := w - textWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// renderSegs lays segs out in at most w cells and styles each piece.
func renderSegs(segs []seg, w int) string {
	var b strings.Builder
	remaining := w
	for _, s := range segs {
		if remaining <= 0 {
			break
		}
		text := sanitize(s.text)
		if textWidth(text) > remaining {
			text = truncate(text, remaining)
			b.WriteString(styled(text, s.style))
			break
		}
		remaining -= textWidth(text)
		b.WriteString(styled(text, s.style))
	}
	return b.String()
}

// segsWidth is the width of segs laid out unconstrained.
func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += textWidth(sanitize(s.text))
	}
	return n
}

// wrapText hard-wraps s at w cells. Empty input gives one empty line.
func wrapText(s string, w int) []string {
	s = sanitize(s)
	if w <= 0 {
		return []string{""}
	}
	var out []string
	var line strings.Builder
	used := 0
	for _, r := range s {
		cw := cellWidth(r)
		if used+cw > w && used > 0 {
			out = append(out, line.String())
			line.Reset()
			used = 0
		}
		line.WriteRune(r)
		used += cw
	}
	return append(out, line.String())
}

// short cuts s to n runes for storage, marking the cut.
func short(s string, n int) string {
	s = sanitize(strings.TrimSpace(s))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}
