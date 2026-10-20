package pi_subagents

import (
	"strings"
	"unicode/utf8"
)

// Port of src/shared/display-text.ts: text the model or the user sees on one terminal line, with control sequences removed.
// Strings are UTF-8; a lone surrogate (JavaScript allows them, Go strings do not) is read from its WTF-8 form (ED A0..BF xx).

// nextCodePoint decodes the code point at s[i:] and its width in bytes; WTF-8 surrogates decode to their value.
func nextCodePoint(s string, i int) (rune, int) {
	if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
		return rune(0xD000) | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	return r, w
}

func isWhitespaceOrControl(cp rune) bool {
	if cp <= 0x20 || (cp >= 0x7f && cp <= 0x9f) {
		return true
	}
	if cp >= 0xd800 && cp <= 0xdfff {
		return true
	}
	return isJSSpace(cp)
}

func consumeControlString(s string, i int, osc bool) int {
	for i < len(s) {
		cp, w := nextCodePoint(s, i)
		switch {
		case osc && cp == 0x07, cp == 0x9c:
			return i + w
		case cp == 0x1b && i+1 < len(s) && s[i+1] == 0x5c:
			return i + 2
		}
		i += w
	}
	return len(s)
}

func consumeCsi(s string, i int) int {
	for i < len(s) {
		cp, w := nextCodePoint(s, i)
		if cp >= 0x40 && cp <= 0x7e {
			return i + w
		}
		i += w
	}
	return len(s)
}

// sanitizeDisplayText collapses whitespace, control characters and terminal escape sequences (CSI, OSC and the other control
// strings) to single spaces and trims the ends.
func sanitizeDisplayText(value string) string {
	var out strings.Builder
	pendingSpace := false
	appendSpace := func() {
		if out.Len() > 0 {
			pendingSpace = true
		}
	}
	for i := 0; i < len(value); {
		cp, w := nextCodePoint(value, i)
		if cp == 0x1b {
			var next byte
			if i+1 < len(value) {
				next = value[i+1]
			}
			appendSpace()
			switch next {
			case 0x5b:
				i = consumeCsi(value, i+2)
			case 0x5d, 0x50, 0x58, 0x5e, 0x5f:
				i = consumeControlString(value, i+2, next == 0x5d)
			default:
				if next != 0 {
					i += 2
				} else {
					i++
				}
			}
			continue
		}
		switch cp {
		case 0x9b:
			appendSpace()
			i = consumeCsi(value, i+w)
			continue
		case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
			appendSpace()
			i = consumeControlString(value, i+w, cp == 0x9d)
			continue
		}
		if isWhitespaceOrControl(cp) {
			appendSpace()
		} else {
			if pendingSpace {
				out.WriteByte(' ')
			}
			out.WriteString(string(cp))
			pendingSpace = false
		}
		i += w
	}
	return out.String()
}

// utf16Len is the JavaScript length of s.
func displayLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		cp, w := nextCodePoint(s, i)
		if cp > 0xffff {
			n += 2
		} else {
			n++
		}
		i += w
	}
	return n
}

// truncateDisplayText keeps whole characters up to maxLength UTF-16 units.
func truncateDisplayText(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	if displayLen(value) <= maxLength {
		return value
	}
	var out strings.Builder
	n := 0
	for i := 0; i < len(value); {
		cp, w := nextCodePoint(value, i)
		cw := 1
		if cp > 0xffff {
			cw = 2
		}
		if n+cw > maxLength {
			break
		}
		out.WriteString(value[i : i+w])
		n += cw
		i += w
	}
	return out.String()
}

// previewDisplayText is the sanitized text cut to maxLength with "...".
func previewDisplayText(value string, maxLength int) string {
	normalized := sanitizeDisplayText(value)
	if displayLen(normalized) <= maxLength {
		return normalized
	}
	if maxLength <= 3 {
		return truncateDisplayText(normalized, maxLength)
	}
	return truncateDisplayText(normalized, maxLength-3) + "..."
}
