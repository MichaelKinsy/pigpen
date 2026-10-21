package pi_background_tasks

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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

// jsParseNumber is Number(string) for a string: the StringNumericLiteral grammar (decimal with an optional sign
// and exponent, "Infinity", and the 0x, 0o and 0b integer forms without a sign), an empty or all-space string being 0, and
// NaN for anything else. ok is false for NaN.
func jsParseNumber(s string) (float64, bool) {
	s = jsTrim(s)
	if s == "" {
		return 0, true
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := s[2:]
			var value float64
			for _, c := range digits {
				d := digitValue(c)
				if d < 0 || d >= base {
					return 0, false
				}
				value = value*float64(base) + float64(d)
			}
			return value, true
		}
	}
	body := s
	sign := 1.0
	switch body[0] {
	case '+':
		body = body[1:]
	case '-':
		sign = -1
		body = body[1:]
	}
	if body == "Infinity" {
		return sign * math.Inf(1), true
	}
	if !validDecimalLiteral(body) {
		return 0, false
	}
	f, err := strconv.ParseFloat(body, 64)
	if err != nil {
		// A range error still carries the clamped value (±Inf), as JavaScript's does.
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			return 0, false
		}
	}
	return sign * f, true
}

func digitValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// validDecimalLiteral reports whether s is StrUnsignedDecimalLiteral without Infinity: digits with an optional
// fraction, or a fraction alone, and an optional exponent.
func validDecimalLiteral(s string) bool {
	i := 0
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			exp++
		}
		if exp == 0 {
			return false
		}
	}
	return i == len(s)
}

// jsNumberString formats a number as JavaScript's `${n}` does (Number::toString): the shortest digits that
// round-trip, in fixed notation from 1e-6 up to 1e21 and otherwise as an exponent without zero padding; -0 reads "0".
func jsNumberString(f float64) string {
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

// jsLen is String.prototype.length: UTF-16 code units. A string read from a JSON document or a log is valid UTF-8,
// so a code point above U+FFFF counts two.
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

// jsTail is s.slice(-n) for n >= 0: the last n UTF-16 code units. A cut that falls inside a surrogate pair keeps the
// whole code point's second half in JavaScript (a lone low surrogate); Go strings cannot hold one, so the half is
// dropped. The only observable difference is that one unpaired unit.
func jsTail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	total := jsLen(s)
	if total <= n {
		return s
	}
	skip := total - n
	for i, r := range s {
		if skip <= 0 {
			return s[i:]
		}
		if r >= 0x10000 {
			if skip == 1 {
				return s[i+utf8.RuneLen(r):]
			}
			skip -= 2
		} else {
			skip--
		}
	}
	return ""
}

// jsHead is s.slice(0, n): the first n UTF-16 code units, the cut inside a surrogate pair dropping the half (see jsTail).
func jsHead(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if count+w > n {
			return s[:i]
		}
		count += w
	}
	return s
}

// jsSlice is s.slice(from) for from >= 0.
func jsSliceFrom(s string, from int) string {
	if from <= 0 {
		return s
	}
	count := 0
	for i, r := range s {
		if count >= from {
			return s[i:]
		}
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		count += w
		if count > from {
			// The cut is inside a pair: its second half is a lone surrogate.
			return s[i+utf8.RuneLen(r):]
		}
	}
	return ""
}

// utf16Units returns the UTF-16 code units of s.
func utf16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

// decodeUTF8 is Buffer.prototype.toString("utf8") and TextDecoder: each maximal invalid subpart of the byte
// sequence becomes one U+FFFD (WHATWG Encoding "utf-8 decoder"). strings.ToValidUTF8 would merge a run of
// invalid bytes into one.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			out.WriteByte(c)
			i++
			continue
		}
		need, lower, upper := 0, byte(0x80), byte(0xbf)
		switch {
		case c >= 0xc2 && c <= 0xdf:
			need = 1
		case c >= 0xe0 && c <= 0xef:
			need = 2
			if c == 0xe0 {
				lower = 0xa0
			}
			if c == 0xed {
				upper = 0x9f
			}
		case c >= 0xf0 && c <= 0xf4:
			need = 3
			if c == 0xf0 {
				lower = 0x90
			}
			if c == 0xf4 {
				upper = 0x8f
			}
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xbf
			j++
		}
		if ok {
			out.Write(b[i:j])
		} else {
			out.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return out.String()
}

// jsSplitLines is text.split(/\r?\n/): the pieces between line breaks, an empty string for an empty text.
func jsSplitLines(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			end := i
			if end > start && text[end-1] == '\r' {
				end--
			}
			out = append(out, text[start:end])
			start = i + 1
		}
	}
	return append(out, text[start:])
}
