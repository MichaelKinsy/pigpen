package stretchtext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The original is JavaScript: its string tests are defined by ECMAScript, not by Go's strings and unicode
// packages. These helpers keep the ECMAScript definitions where the two differ.

// isSpace is ECMAScript's /\s/: WhiteSpace and LineTerminator. Go's unicode.IsSpace differs (it
// accepts U+0085 and rejects U+FEFF).
func isSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// isAllSpace is /^\s+$/.test(s).
func isAllSpace(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isSpace(r) {
			return false
		}
	}
	return true
}

// hasNonSpace is /\S/.test(s).
func hasNonSpace(s string) bool {
	for _, r := range s {
		if !isSpace(r) {
			return true
		}
	}
	return false
}

// trim is String.prototype.trim.
func trim(s string) string { return strings.TrimFunc(s, isSpace) }

// isCased and isCaseIgnorable are the Unicode properties String.prototype.toLowerCase consults for
// the final sigma.
func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) || unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func isCaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case '\'', '.', ':', 0xb7, 0x387, 0x5f4, 0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55, 0xff07, 0xff0e, 0xff1a:
		return true
	}
	return false
}

// lower is String.prototype.toLowerCase: the full case mapping, so "İ" is "i̇" and a capital sigma
// ending a word is "ς". Go's strings.ToLower maps each rune alone and gets both wrong.
func lower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		switch {
		case r == 0x130:
			b.WriteString("i\u0307")
		case r == 0x3a3:
			if finalSigma(runes, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma is the Final_Sigma condition of SpecialCasing.txt: a cased letter before, none after,
// ignoring case-ignorable characters.
func finalSigma(runes []rune, at int) bool {
	before := false
	for i := at - 1; i >= 0; i-- {
		if isCaseIgnorable(runes[i]) {
			continue
		}
		before = isCased(runes[i])
		break
	}
	if !before {
		return false
	}
	for i := at + 1; i < len(runes); i++ {
		if isCaseIgnorable(runes[i]) {
			continue
		}
		return !isCased(runes[i])
	}
	return true
}
