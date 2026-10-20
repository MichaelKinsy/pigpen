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
	u := utf16.Encode([]rune(s))
	n := len(u)
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
	from, to := clamp(start), clamp(end)
	if from >= to {
		return ""
	}
	return string(utf16.Decode(u[from:to]))
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
