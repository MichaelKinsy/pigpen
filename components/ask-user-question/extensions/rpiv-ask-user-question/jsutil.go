package ask_user_question

import (
	"strings"
)

// isJSSpace reports whether r is trimmed by JavaScript's String.prototype.trim: ECMAScript WhiteSpace and
// LineTerminator. Go's unicode.IsSpace differs (it includes U+0085 and lacks U+FEFF).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }
