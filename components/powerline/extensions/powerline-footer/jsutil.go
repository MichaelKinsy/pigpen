package powerline_footer

import (
	"math"
	"math/big"
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

// jsToFixed is Number.prototype.toFixed: the decimal expansion of the double itself rounded to d places, ties going to the
// larger value (Go's FormatFloat rounds ties to even, so 2.5 would read "2").
func jsToFixed(x float64, d int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return jsNumber(x)
	}
	neg := x < 0
	if neg {
		x = -x
	}
	exact := new(big.Float).SetPrec(1100).SetFloat64(x).Text('f', 1100)
	intPart, frac, _ := strings.Cut(exact, ".")
	digits := []byte(intPart + frac[:d])
	if len(frac) > d && frac[d] >= '5' {
		for i := len(digits) - 1; ; i-- {
			if i < 0 {
				digits = append([]byte{'1'}, digits...)
				break
			}
			if digits[i] == '9' {
				digits[i] = '0'
				continue
			}
			digits[i]++
			break
		}
	}
	n := len(digits) - d
	out := string(digits[:n])
	if d > 0 {
		out += "." + string(digits[n:])
	}
	if neg {
		out = "-" + out
	}
	return out
}

// jsRound is Math.round: halves go toward positive infinity.
func jsRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}
