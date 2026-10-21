package pi_permission_system

import (
	"math"
	"strings"
	"unicode/utf16"
)

// JavaScript string helpers over UTF-16 code units, for the places the original compares `.length` or slices by index.

// jsLen is String.prototype.length.
func jsLen(s string) int {
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

// jsSlice is String.prototype.slice(start, end) over UTF-16 units; pass math.MaxInt for no end.
func jsSlice(s string, start, end int) string {
	if isASCII(s) { // one unit per byte: slice the string itself
		from, to := jsSliceBounds(len(s), start, end)
		if from >= to {
			return ""
		}
		return s[from:to]
	}
	u := utf16.Encode([]rune(s))
	from, to := jsSliceBounds(len(u), start, end)
	if from >= to {
		return ""
	}
	return string(utf16.Decode(u[from:to]))
}

// jsSliceBounds is slice's index clamping for a string of n units: negative indexes count from the end, both clamp to [0, n].
func jsSliceBounds(n, start, end int) (from, to int) {
	clamp := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		} else if i > n {
			i = n
		}
		return i
	}
	return clamp(start), clamp(end)
}

// isASCII reports whether every byte of s is below 0x80.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

const jsEnd = math.MaxInt

// jsIndexOf is String.prototype.indexOf(sub) in UTF-16 units, -1 when absent.
func jsIndexOf(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return jsLen(s[:i])
}

// uniqueStrings is [...new Set(values)]: first occurrence wins.
func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// toWellFormed is String.prototype.toWellFormed over a WTF-8 string: every lone surrogate (what slicing at an astral character
// leaves) becomes U+FFFD, the form a terminal or a UTF-8 encoder writes for it. The renderers apply it to what they hand back, so one
// lone surrogate reads as one replacement character rather than as three invalid bytes.
func toWellFormed(s string) string {
	if !strings.Contains(s, "\xed") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if _, ok := wtfSurrogate(s, i); ok {
			b.WriteString("\uFFFD")
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
