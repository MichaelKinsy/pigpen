package schedule_prompt

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// JavaScript semantics the original relies on, kept in one place.

// truthy is JavaScript's ToBoolean.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return !jsobj.IsUndefined(v)
}

func isNullish(v any) bool { return v == nil || jsobj.IsUndefined(v) }

// jsNumber formats a float64 as JavaScript's Number::toString does for the values this extension handles.
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
	if math.Abs(f) < 1e21 && math.Abs(f) >= 1e-6 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[0]
	exp = strings.TrimLeft(exp[1:], "0")
	return mant + "e" + string(sign) + exp
}

// jsStr is the string a template literal makes of v.
func jsStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsNumber(t)
	case int:
		return strconv.Itoa(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			if isNullish(e) {
				parts[i] = ""
			} else {
				parts[i] = jsStr(e)
			}
		}
		return strings.Join(parts, ",")
	case *jsobj.Object:
		return "[object Object]"
	}
	if jsobj.IsUndefined(v) {
		return "undefined"
	}
	return "[object Object]"
}

// strictEqualString is `v === s`.
func strictEqualString(v any, s string) bool {
	str, ok := v.(string)
	return ok && str == s
}

func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// jsSubstring is s.substring(start, end) over UTF-16 units.
func jsSubstring(s string, start, end int) string {
	u := utf16.Encode([]rune(s))
	end = min(end, len(u))
	if start >= end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}

// jsPadEnd is s.padEnd(n) with spaces.
func jsPadEnd(s string, n int) string {
	if l := jsLen(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

// isoString is new Date(ms).toISOString(); ok is false for the RangeError an Invalid Date throws.
func isoString(ms float64) (string, bool) { return jsdate.ToISOString(ms) }

// isoNow is new Date().toISOString().
func isoNow() string {
	s, _ := jsdate.ToISOString(jsdate.Now())
	return s
}
