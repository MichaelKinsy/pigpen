package websearch

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JavaScript number parsing for the attribute values Defuddle reads.

// jsParseInt is parseInt(s) with no radix: NaN when there is no leading integer.
func jsParseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, jsSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	base := 10
	if len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base = 16
		s = s[2:]
	}
	end := 0
	for end < len(s) {
		c := s[end]
		ok := (c >= '0' && c <= '9') || (base == 16 && ((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')))
		if !ok {
			break
		}
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	v := 0.0
	if end <= 15 && base == 10 {
		n, _ := strconv.ParseUint(s[:end], 10, 64)
		v = float64(n)
	} else if base == 16 && end <= 12 {
		n, _ := strconv.ParseUint(s[:end], 16, 64)
		v = float64(n)
	} else {
		for i := 0; i < end; i++ {
			d := float64(s[i] - '0')
			if s[i] >= 'a' {
				d = float64(s[i]-'a') + 10
			} else if s[i] >= 'A' {
				d = float64(s[i]-'A') + 10
			}
			v = v*float64(base) + d
		}
	}
	if neg {
		return -v
	}
	return v
}

var reFloatPrefix = mustRE(`^[+-]?(?:Infinity|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)`)

// jsParseFloat is parseFloat(s): NaN when there is no leading decimal literal.
func jsParseFloat(s string) float64 {
	s = strings.TrimLeftFunc(s, jsSpace)
	m := reFloatPrefix.FindString(s)
	if m == "" {
		return math.NaN()
	}
	switch m {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	f, err := strconv.ParseFloat(m, 64)
	if err != nil && !strings.Contains(err.Error(), "range") {
		return math.NaN()
	}
	return f
}

// jsIsNaN reports NaN.
func jsIsNaN(f float64) bool { return f != f }

// jsOr0 is `parseInt(...) || 0`: NaN and 0 both give 0.
func jsOr0(f float64) float64 {
	if f != f || f == 0 {
		return 0
	}
	return f
}

func lastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }
