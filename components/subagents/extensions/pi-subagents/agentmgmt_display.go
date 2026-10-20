package pi_subagents

import (
	"strings"
	"unicode/utf16"
)

// Display text helpers (src/shared/display-text.ts: sanitizeDisplayText, truncateDisplayText, previewDisplayText).

func isWhitespaceOrControl(cp rune) bool {
	if cp <= 0x20 || (cp >= 0x7f && cp <= 0x9f) {
		return true
	}
	if cp >= 0xd800 && cp <= 0xdfff {
		return true
	}
	return strings.TrimFunc(string(cp), isJSSpace) == ""
}

func consumeControlString(units []uint16, i int, osc bool) int {
	for i < len(units) {
		cp, w := codePointAtUnits(units, i)
		if osc && cp == 0x07 {
			return i + w
		}
		if cp == 0x9c {
			return i + w
		}
		if cp == 0x1b && i+1 < len(units) && units[i+1] == 0x5c {
			return i + 2
		}
		i += w
	}
	return len(units)
}

func consumeCsi(units []uint16, i int) int {
	for i < len(units) {
		cp, w := codePointAtUnits(units, i)
		if cp >= 0x40 && cp <= 0x7e {
			return i + w
		}
		i += w
	}
	return len(units)
}

// codePointAtUnits is String.prototype.codePointAt over UTF-16 units: a lone surrogate is its own code point.
func codePointAtUnits(units []uint16, i int) (rune, int) {
	u := units[i]
	if u >= 0xd800 && u <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
		return utf16.DecodeRune(rune(u), rune(units[i+1])), 2
	}
	return rune(u), 1
}

// sanitizeDisplayText is display-text.ts:sanitizeDisplayText: terminal escapes and control characters become single spaces.
func sanitizeDisplayText(value string) string {
	units := utf16.Encode([]rune(value))
	var out []string
	pending := false
	appendSpace := func() {
		if len(out) > 0 {
			pending = true
		}
	}
	appendText := func(s string) {
		if pending {
			out = append(out, " ")
		}
		out = append(out, s)
		pending = false
	}
	for i := 0; i < len(units); {
		cp, w := codePointAtUnits(units, i)
		if cp == 0x1b {
			var next uint16
			if i+1 < len(units) {
				next = units[i+1]
			}
			appendSpace()
			switch next {
			case 0x5b:
				i = consumeCsi(units, i+2)
				continue
			case 0x5d, 0x50, 0x58, 0x5e, 0x5f:
				i = consumeControlString(units, i+2, next == 0x5d)
				continue
			}
			if next != 0 {
				i += 2
			} else {
				i++
			}
			continue
		}
		if cp == 0x9b {
			appendSpace()
			i = consumeCsi(units, i+w)
			continue
		}
		if cp == 0x90 || cp == 0x98 || cp == 0x9d || cp == 0x9e || cp == 0x9f {
			appendSpace()
			i = consumeControlString(units, i+w, cp == 0x9d)
			continue
		}
		if isWhitespaceOrControl(cp) {
			appendSpace()
		} else {
			appendText(string(utf16.Decode(units[i : i+w])))
		}
		i += w
	}
	return strings.Join(out, "")
}

// truncateDisplayText cuts to maxLength UTF-16 units without splitting a code point (display-text.ts:81).
func truncateDisplayText(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	if utf16Length(value) <= maxLength {
		return value
	}
	var b strings.Builder
	n := 0
	for _, r := range value {
		if n+utf16Len(r) > maxLength {
			break
		}
		b.WriteRune(r)
		n += utf16Len(r)
	}
	return b.String()
}

// previewDisplayText is display-text.ts:95.
func previewDisplayText(value string, maxLength int) string {
	n := sanitizeDisplayText(value)
	if utf16Length(n) <= maxLength {
		return n
	}
	if maxLength <= 3 {
		return truncateDisplayText(n, maxLength)
	}
	return truncateDisplayText(n, maxLength-3) + "..."
}
