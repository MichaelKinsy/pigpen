package warden

import (
	"strings"
	"unicode/utf16"
)

// JavaScript string helpers. pi-warden measures and cuts strings in UTF-16 code units
// (`String.length`, `slice`); the port keeps that unit wherever the number reaches the model
// (truncation limits) so a cut lands where the original's does.

func utf16Len(s string) int {
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

// utf16Slice is s.slice(from, to) for UTF-16 code unit offsets; to < 0 means the end.
func utf16Slice(s string, from, to int) string {
	units := utf16.Encode([]rune(s))
	if to < 0 || to > len(units) {
		to = len(units)
	}
	if from > to {
		from = to
	}
	return string(utf16.Decode(units[from:to]))
}

// truncate is guard.ts `truncate`: the text, or its first `limit` code units and a count of the rest.
func truncate(text string, limit int) string {
	n := utf16Len(text)
	if n <= limit {
		return text
	}
	return utf16Slice(text, 0, limit) + "… [" + itoaInt(n-limit) + " more chars]"
}

// sample is guard.ts `sample`: head, a slice from the middle, and the tail.
func sample(text string, limit int) string {
	n := utf16Len(text)
	if n <= limit {
		return text
	}
	head := limit * 6 / 10
	mid := limit * 2 / 10
	tail := limit - head - mid
	middleStart := (n - mid) / 2
	if (n-mid)%2 != 0 {
		// Math.floor(n/2 - mid/2)
		middleStart = (n - mid) / 2
	}
	return utf16Slice(text, 0, head) + "\n… [" + itoaInt(middleStart-head) + " chars] …\n" + utf16Slice(text, middleStart, middleStart+mid) +
		"\n… [" + itoaInt(n-tail-(middleStart+mid)) + " chars] …\n" + utf16Slice(text, n-tail, -1)
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

var reWhitespaceRun = lazyRE(`\s+`)

// jsTrim is String.prototype.trim: it strips Unicode white space and line terminators, which is
// wider than Go's ASCII-only `\s` and than strings.TrimSpace on some code points (U+FEFF).
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsSplitWhitespace is s.split(/\s+/) with JS's `\s` set.
func jsSplitWhitespace(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	first := true
	for _, r := range s {
		if isJSSpace(r) {
			if !in {
				out = append(out, cur.String())
				cur.Reset()
				in = true
			}
			first = false
			continue
		}
		in = false
		first = false
		cur.WriteRune(r)
	}
	_ = first
	out = append(out, cur.String())
	return out
}
