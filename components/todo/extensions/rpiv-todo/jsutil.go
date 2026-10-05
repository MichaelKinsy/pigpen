package rpiv_todo

import (
	"math"
	"strconv"
	"strings"
)

// isJSSpace reports whether r is trimmed by JavaScript's String.prototype.trim: ECMAScript WhiteSpace and
// LineTerminator. Go's unicode.IsSpace differs (it includes U+0085 and lacks U+FEFF).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsNumber formats a number as JavaScript's `${n}` does (Number::toString): the shortest digits that
// round-trip, in fixed notation from 1e-6 up to 1e21 and otherwise as an exponent without zero padding
// ("1e-7", "1e+21"); -0 reads "0". A task id comes from the model, so any number can reach the text.
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	if a := math.Abs(f); a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
}

// number reads a JSON number a tool argument carries (float64 once decoded; the integer kinds for tests
// and in-process callers).
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// numberList reads a JSON array of numbers; ok is false when v is not one.
func numberList(v any) ([]float64, bool) {
	switch l := v.(type) {
	case []float64:
		return l, true
	case []any:
		out := make([]float64, 0, len(l))
		for _, e := range l {
			n, ok := number(e)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	return nil, false
}

// text reads a string argument; ok is false when the key is absent or not a string.
func text(p params, key string) (string, bool) {
	s, ok := p[key].(string)
	return s, ok
}
