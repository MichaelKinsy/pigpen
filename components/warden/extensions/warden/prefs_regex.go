package warden

import (
	"strings"
	"unicode"
)

// JavaScript regular-expression semantics on top of Go's RE2, for the standing-preference detectors (prefs.ts).
// RE2's `\s` is ASCII only and its `.` accepts \r, U+2028 and U+2029; JavaScript's `\s` is the Unicode white-space set
// and its `.` stops at every line terminator. prefsRE rewrites those two tokens so a pattern keeps the meaning it has
// in the original. Lookahead, lookbehind and backreferences do not exist in RE2: each pattern that used one in
// prefs.ts is rewritten next to its use, and the rewrite is checked against the original by the differential test
// (prefs_differential_test.go).

// prefsWS is the body of a character class holding JavaScript's `\s` set.
const prefsWS = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// prefsDot is JavaScript's `.` without the s flag.
const prefsDot = `[^\n\r\x{2028}\x{2029}]`

// prefsPattern rewrites `\s` and `.` of a JavaScript pattern into RE2 syntax with the same meaning. `\S` is not
// accepted inside a class (no pattern needs it).
func prefsPattern(expr string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case c == '\\' && i+1 < len(expr):
			next := expr[i+1]
			i++
			switch next {
			case 's':
				if inClass {
					b.WriteString(prefsWS)
				} else {
					b.WriteString("[" + prefsWS + "]")
				}
			case 'S':
				if inClass {
					panic("prefsPattern: \\S inside a class")
				}
				b.WriteString("[^" + prefsWS + "]")
			default:
				b.WriteByte('\\')
				b.WriteByte(next)
			}
		case c == '[' && !inClass:
			inClass = true
			b.WriteByte(c)
		case c == ']' && inClass:
			inClass = false
			b.WriteByte(c)
		case c == '.' && !inClass:
			b.WriteString(prefsDot)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// prefsRE is a lazily compiled JavaScript-flavoured pattern; flags is "" or "i" (or "m", "im"); multiline
// recognises \n only, where JavaScript also recognises \r, U+2028 and U+2029 (PORT.md).
func prefsRE(flags, expr string) *lazyRegexp {
	prefix := ""
	if flags != "" {
		prefix = "(?" + flags + ")"
	}
	return lazyRE(prefix + prefsPattern(expr))
}

// jsLower is String.prototype.toLowerCase for the characters the detectors can see: Go's simple mapping, except
// U+0130 which JavaScript lowers to "i" and a combining dot.
func jsLower(s string) string {
	s = strings.ReplaceAll(s, "\u0130", "i\u0307")
	return strings.ToLower(s)
}

// jsUpperFirst is `s.charAt(0).toUpperCase() + s.slice(1)`: the first UTF-16 code unit is upper-cased with
// JavaScript's full mapping; a lone surrogate (the first half of an astral character) is left as it is.
func jsUpperFirst(s string) string {
	if s == "" {
		return s
	}
	for i, r := range s {
		rest := s[i+len(string(r)):]
		if r >= 0x10000 {
			return s
		}
		if full, ok := jsUpperFull[r]; ok {
			return full + rest
		}
		return string(unicode.ToUpper(r)) + rest
	}
	return s
}

// jsTrimEnd is String.prototype.trimEnd.
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, isJSSpace) }

// jsCollapseSpace is s.replace(/\s+/g, " ").
func jsCollapseSpace(s string) string {
	var b strings.Builder
	inRun := false
	for _, r := range s {
		if isJSSpace(r) {
			if !inRun {
				b.WriteByte(' ')
			}
			inRun = true
			continue
		}
		inRun = false
		b.WriteRune(r)
	}
	return b.String()
}
