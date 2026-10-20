package pi_subagents

import (
	"regexp"
	"strings"
	"unicode"
)

// Text measurement for terminal cards: the visible width of a string (ANSI escapes take none, wide characters two, combining
// marks none), truncation and word wrapping to a width. The original uses pi-tui's visibleWidth, truncateToWidth and
// wrapTextWithAnsi.

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func runeWidth(r rune) int {
	switch {
	case r == 0 || r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf && r != 0x303f, r >= 0xac00 && r <= 0xd7a3, r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f, r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f,
		r >= 0x1f900 && r <= 0x1f9ff, r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// uiVisibleWidth is the number of terminal columns s occupies.
func uiVisibleWidth(s string) int {
	n := 0
	for _, r := range ansiSequence.ReplaceAllString(s, "") {
		n += runeWidth(r)
	}
	return n
}

// uiTruncateToWidth cuts s to at most width columns (ANSI sequences are kept whole and cost nothing).
func uiTruncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if uiVisibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for i := 0; i < len(s); {
		if loc := ansiSequence.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := rune(s[i]), 1
		if r >= 0x80 {
			r, size = decodeRuneAt(s, i)
		}
		w := runeWidth(r)
		if used+w > width {
			break
		}
		b.WriteString(s[i : i+size])
		used += w
		i += size
	}
	return b.String()
}

func decodeRuneAt(s string, i int) (rune, int) {
	for _, r := range s[i:] {
		return r, len(string(r))
	}
	return 0, 1
}

// uiWrapText wraps text to width columns at spaces; a word longer than a row is cut. Newlines start new rows.
func uiWrapText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		if raw == "" {
			out = append(out, "")
			continue
		}
		line := ""
		flush := func() {
			out = append(out, line)
			line = ""
		}
		for _, word := range strings.Split(raw, " ") {
			for uiVisibleWidth(word) > width {
				if line != "" {
					flush()
				}
				cut := uiTruncateToWidth(word, width)
				if cut == "" { // a character wider than the row: it still takes a row of its own
					_, size := decodeRuneAt(word, 0)
					cut = word[:size]
				}
				out = append(out, cut)
				word = word[len(cut):]
			}
			switch {
			case line == "":
				line = word
			case uiVisibleWidth(line)+1+uiVisibleWidth(word) <= width:
				line += " " + word
			default:
				flush()
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
