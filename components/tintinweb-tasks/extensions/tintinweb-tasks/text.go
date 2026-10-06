package tintinweb_tasks

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Terminal text measurement, a stand-in for pi-tui's utils (the Go SDK has none): cells are counted per
// rune rather than per grapheme cluster, with zero-width marks and the wide East Asian and emoji blocks
// special-cased. ANSI escape sequences have no width.

// ansiAt returns the length of the escape sequence starting at s[i] (0 when none): a CSI sequence ending
// in a final byte, or an OSC sequence ending in BEL or ST.
func ansiAt(s string, i int) int {
	if s[i] != 0x1b || i+1 >= len(s) {
		return 0
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j - i + 1
			}
		}
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j - i + 2
			}
		}
	}
	return 0
}

func runeWidth(r rune) int {
	switch {
	case r == 0x200d || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0xfe00 && r <= 0xfe0f):
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe6f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// visibleWidth is the terminal cell width of s, ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if n := ansiAt(s, i); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += runeWidth(r)
		i += size
	}
	return w
}

// truncateToWidth cuts s to maxWidth cells, ending with ellipsis; text that fits is returned unchanged.
// Pending escape sequences are kept only when a visible rune follows them, and a cut ends with a reset
// before and after the ellipsis. upstream: pi-tui utils.js:1018-1139 (truncateToWidth).
func truncateToWidth(s string, maxWidth int, ellipsis string) string {
	if maxWidth <= 0 {
		return ""
	}
	if s == "" {
		return ""
	}
	ellipsisWidth := visibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		if visibleWidth(s) <= maxWidth {
			return s
		}
		return clipFragment(ellipsis, maxWidth)
	}
	target := maxWidth - ellipsisWidth
	var result, pending strings.Builder
	kept, seen := 0, 0
	contiguous, overflowed := true, false
	i := 0
	for i < len(s) {
		if n := ansiAt(s, i); n > 0 {
			pending.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := runeWidth(r)
		if contiguous && kept+w <= target {
			result.WriteString(pending.String())
			pending.Reset()
			result.WriteString(s[i : i+size])
			kept += w
		} else {
			contiguous = false
			pending.Reset()
		}
		seen += w
		i += size
		if seen > maxWidth {
			overflowed = true
			break
		}
	}
	if !overflowed {
		return s
	}
	const reset = "\x1b[0m"
	return result.String() + reset + ellipsis + reset
}

// clipFragment is the first maxWidth cells of s (used when the ellipsis alone does not fit).
func clipFragment(s string, maxWidth int) string {
	var b strings.Builder
	w := 0
	for _, r := range s {
		if w+runeWidth(r) > maxWidth {
			break
		}
		b.WriteRune(r)
		w += runeWidth(r)
	}
	return b.String()
}
