package langsmith

// JavaScript value semantics the client reads: String.prototype.trim, parseFloat, parseInt, truthiness,
// template-literal conversion and Number.prototype.toString for the values a run payload holds.

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// isJSSpace is the WhiteSpace and LineTerminator set String.prototype.trim removes (ECMA-262 §12.2, §12.3).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsTrim is s.trim().
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// stripQuotePair is `.replace(/^<q>(.*)<q>$/, "$1")`: `.` does not match a line terminator.
func stripQuotePair(s string, q byte) string {
	if len(s) < 2 || s[0] != q || s[len(s)-1] != q {
		return s
	}
	inner := s[1 : len(s)-1]
	for _, r := range inner {
		if isJSLineTerminator(r) {
			return s
		}
	}
	return inner
}

// trimQuotes is client.js:102-110 (undefined stays undefined: the caller passes nil).
func trimQuotes(s string) string {
	s = jsTrim(s)
	s = stripQuotePair(s, '"')
	return stripQuotePair(s, '\'')
}

func trimLeftJS(s string) string { return strings.TrimLeftFunc(s, isJSSpace) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// jsParseFloat is parseFloat(s): the longest StrDecimalLiteral prefix after leading white space, NaN if none.
func jsParseFloat(s string) float64 {
	s = trimLeftJS(s)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if strings.HasPrefix(s[i:], "Infinity") {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	intDigits := i - start
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		fracDigits = j - i - 1
		if intDigits > 0 || fracDigits > 0 {
			i = j
		}
	}
	if intDigits == 0 && fracDigits == 0 {
		return math.NaN()
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		if k > j {
			i = k
		}
	}
	f, _ := strconv.ParseFloat(s[:i], 64) // an out-of-range value is ±Infinity, as in JavaScript
	return f
}

// jsParseInt10 is parseInt(s, 10): ok is false for NaN.
func jsParseInt10(s string) (float64, bool) {
	s = trimLeftJS(s)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == start {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s[:i], 64)
	return f, true
}

// jsNumberString is Number.prototype.toString for a float64.
func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	b, err := json.AppendStringify(nil, f)
	if err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return string(b)
}

// jsTruthy is the JavaScript ToBoolean of a payload value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedT:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case float32:
		return x != 0 && !math.IsNaN(float64(x))
	case int:
		return x != 0
	case int64:
		return x != 0
	case int32:
		return x != 0
	case uint64:
		return x != 0
	}
	return true
}

// jsString is the template-literal conversion `${v}` of a scalar payload value.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undefinedT:
		return "undefined"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return jsNumberString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any, []string, []*omap:
		return "[object Array]"
	}
	return "[object Object]"
}

// jsUTF8Len is Buffer.byteLength(s, "utf8"): lone surrogates (WTF-8 triples) count as the 3 bytes of U+FFFD,
// which is what len(s) is for them.
func jsUTF8Len(s string) int {
	if utf8.ValidString(s) {
		return len(s)
	}
	return len(jsValidUTF8(s))
}
