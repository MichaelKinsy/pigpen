package langsmith

// JavaScript string-to-bytes behaviour the wire helpers depend on: `TextEncoder.encode` (lone surrogates become
// U+FFFD) and `String.prototype.toLowerCase` (full Unicode lower-casing, including the context-sensitive Final_Sigma
// rule and U+0130). Go strings that carry lone surrogates do so as WTF-8 (ED A0..BF 80..BF).

import (
	"unicode"
	"unicode/utf8"
)

// jsValidUTF8 returns s with every lone surrogate (WTF-8 triple) and every other invalid byte replaced by U+FFFD,
// the way TextEncoder.encode treats a JavaScript string. It returns s itself, without allocating, when s is valid.
func jsValidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
				size = 3
			}
			out = utf8.AppendRune(out, utf8.RuneError)
			i += size
			continue
		}
		out = append(out, s[i:i+size]...)
		i += size
	}
	return string(out)
}

// jsUTF8Lower appends the UTF-8 encoding of s.toLowerCase() to dst.
func jsUTF8Lower(dst []byte, s string) []byte {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		for i := 0; i < len(s); i++ {
			c := s[i]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
		}
		return dst
	}
	s = jsValidUTF8(s)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x130: // SpecialCasing.txt: 0130; 0069 0307; ...
			dst = append(dst, 'i', 0xCC, 0x87)
		case r == 0x3A3: // Final_Sigma
			if isFinalSigma(s, i, size) {
				dst = utf8.AppendRune(dst, 0x3C2)
			} else {
				dst = utf8.AppendRune(dst, 0x3C3)
			}
		default:
			dst = utf8.AppendRune(dst, unicode.ToLower(r))
		}
		i += size
	}
	return dst
}

// isFinalSigma is the Unicode Final_Sigma condition for the sigma at s[i:i+size]: preceded by a cased letter
// (skipping case-ignorable characters) and not followed by one.
func isFinalSigma(s string, i, size int) bool {
	before := false
	for j := i; j > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:j])
		j -= n
		if isCaseIgnorable(r) {
			continue
		}
		before = isCased(r)
		break
	}
	if !before {
		return false
	}
	for j := i + size; j < len(s); {
		r, n := utf8.DecodeRuneInString(s[j:])
		j += n
		if isCaseIgnorable(r) {
			continue
		}
		return !isCased(r)
	}
	return true
}
