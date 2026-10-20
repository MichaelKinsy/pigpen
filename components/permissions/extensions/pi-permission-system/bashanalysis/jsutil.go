// Package bashanalysis analyses a parsed bash program the way the original extension does: it
// enumerates the command-pattern units, interprets wrappers, proves what a command's head word does
// to its path operands, and collects path-candidate tokens. Port of access-intent/bash/
// {command-enumeration,wrapper-analysis,command-effects,bash-arity,sed-invocation,awk-invocation,
// sync-commands,token-classification,token-collection}.ts of @gotgenes/pi-permission-system 39.0.3.
//
// It reads trees through package bashparse, whose node indices are UTF-16 code units; every offset
// in this package is therefore a UTF-16 index too, sliced through [slice16].
package bashanalysis

import (
	"strings"
	"unicode/utf8"
)

// isJSSpace reports whether r is in JavaScript's WhiteSpace or LineTerminator set, the characters
// `\s`, `trim()` and `trimEnd()` agree on (ECMA-262 12.2, 12.3). Copied from the module root's
// jsutil.go, which this package cannot import.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsTrimEnd is String.prototype.trimEnd.
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, isJSSpace) }

// isASCII reports whether s holds only ASCII, where byte and UTF-16 indices coincide.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// utf16Len is JavaScript's s.length.
func utf16Len(s string) int {
	if isASCII(s) {
		return len(s)
	}
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// slice16 is JavaScript's s.slice(start, end) with UTF-16 indices: negative indices count from the
// end, both are clamped, and an empty range is "". A cut inside a surrogate pair keeps the whole
// character on its start side only, which Go strings cannot represent half of; no caller cuts
// there (offsets are node boundaries).
func slice16(s string, start, end int) string {
	n := utf16Len(s)
	start, end = clampIndex(start, n), clampIndex(end, n)
	if start >= end {
		return ""
	}
	if n == len(s) {
		return s[start:end]
	}
	return s[byteOffset16(s, start):byteOffset16(s, end)]
}

// slice16From is JavaScript's s.slice(start).
func slice16From(s string, start int) string { return slice16(s, start, utf16Len(s)) }

func clampIndex(i, n int) int {
	if i < 0 {
		i += n
		if i < 0 {
			return 0
		}
		return i
	}
	if i > n {
		return n
	}
	return i
}

// byteOffset16 converts a UTF-16 index (0 <= index <= length) to a byte offset.
func byteOffset16(s string, index int) int {
	unit := 0
	for i, r := range s {
		if unit >= index {
			return i
		}
		if r >= 0x10000 {
			unit += 2
		} else {
			unit++
		}
	}
	return len(s)
}

// jsLower is String.prototype.toLowerCase. Go's simple mapping lowers U+0130 to "i"; JavaScript's
// full mapping gives "i" plus U+0307, which is not the ASCII letter a table key spells.
func jsLower(s string) string {
	if strings.ContainsRune(s, 0x130) {
		s = strings.ReplaceAll(s, "\u0130", "i\u0307")
	}
	return strings.ToLower(s)
}
