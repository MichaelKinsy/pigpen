package websearch

import (
	"strings"
	"unicode/utf16"
)

// The original measures and slices strings in UTF-16 code units (String.length, slice). Limits,
// offsets and cut points below use the same unit so a response splits where the original's did.

// jsLen is `s.length`.
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

// jsSlice is `s.slice(start, end)`; end < 0 means the end of the string. A cut inside a
// surrogate pair yields U+FFFD, as Go strings cannot hold lone surrogates.
func jsSlice(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	if end < 0 || end > len(u) {
		end = len(u)
	}
	if start < 0 {
		start = 0
	}
	if start >= end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}

// jsIndexOf is `haystack.indexOf(needle, from)` in UTF-16 units (-1 when absent).
func jsIndexOf(haystack, needle string, from int) int {
	u := utf16.Encode([]rune(haystack))
	n := utf16.Encode([]rune(needle))
	if from < 0 {
		from = 0
	}
	for i := from; i+len(n) <= len(u); i++ {
		match := true
		for j := range n {
			if u[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// jsLastIndexOf is `s.lastIndexOf(needle, from)` in UTF-16 units.
func jsLastIndexOf(s, needle string, from int) int {
	u := utf16.Encode([]rune(s))
	n := utf16.Encode([]rune(needle))
	start := from
	if start > len(u)-len(n) {
		start = len(u) - len(n)
	}
	for i := start; i >= 0; i-- {
		match := true
		for j := range n {
			if u[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// jsTruncate cuts to at most n UTF-16 units and appends suffix when it cut.
func jsTruncate(s string, n int, suffix string) string {
	if jsLen(s) <= n {
		return s
	}
	return jsSlice(s, 0, n) + suffix
}

func byteLen(s string) int { return len(s) }

func splitLines(s string) []string { return strings.Split(s, "\n") }
