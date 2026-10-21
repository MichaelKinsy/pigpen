package websearch

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// The original measures and slices strings in UTF-16 code units (String.length, slice). Limits,
// offsets and cut points below use the same unit so a response splits where the original's did.

// jsLen is `s.length`: the UTF-16 length of the string, a rune above U+FFFF counting for two.
// A run of ASCII is counted a block at a time, which is nearly all of a page; any other rune goes
// through the decoder, which treats an invalid byte as U+FFFD (one unit) as a range loop does.
func jsLen(s string) int {
	n, i := 0, 0
	for i < len(s) {
		run := asciiRun(s[i:], len(s))
		n += run
		i += run
		if i == len(s) {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// asciiBlock is the number of bytes asciiRun tests at once.
const asciiBlock = 64

// asciiRun is the length of the leading run of ASCII bytes of s, counted in whole blocks of
// asciiBlock bytes and at most limit: it may stop short of the end of the run (the caller then
// decodes one rune and calls again), never past a byte of 0x80 or more. The block is read as
// eight 64-bit words through a read-only view of the string.
func asciiRun(s string, limit int) int {
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	i := 0
	for i+asciiBlock <= len(b) && i+asciiBlock <= limit {
		w := binary.LittleEndian.Uint64(b[i:]) | binary.LittleEndian.Uint64(b[i+8:]) |
			binary.LittleEndian.Uint64(b[i+16:]) | binary.LittleEndian.Uint64(b[i+24:]) |
			binary.LittleEndian.Uint64(b[i+32:]) | binary.LittleEndian.Uint64(b[i+40:]) |
			binary.LittleEndian.Uint64(b[i+48:]) | binary.LittleEndian.Uint64(b[i+56:])
		if w&0x8080808080808080 != 0 {
			break
		}
		i += asciiBlock
	}
	return i
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
