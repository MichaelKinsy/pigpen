package pi_subagents

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// The mission modules (src/missions/*.ts) read JSON that the model, a file or a caller wrote and validate it field by field
// with the original's messages. They work on the decoded form of jsjson.go: *jsObject (key order kept), []any, string,
// float64, bool and nil.

// toDyn converts Go-decoded JSON (map[string]any and the like) into that form; a map becomes an object with sorted keys.
func toDyn(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := newObject()
		for _, k := range keys {
			o.set(k, toDyn(x[k]))
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = toDyn(e)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return v
}

// dynGet is `object[key]`: the value and whether the key is present (JSON null is present; undefined is not).
func dynGet(o *jsObject, k string) (any, bool) {
	v, ok := o.vals[k]
	if !ok {
		return nil, false
	}
	if _, u := v.(undef); u {
		return nil, false
	}
	return v, true
}

func isPlainObject(v any) (*jsObject, bool) {
	o, ok := v.(*jsObject)
	return o, ok
}

// missionAsObject is asObject.
func missionAsObject(v any, label string) (*jsObject, error) {
	o, ok := isPlainObject(v)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return o, nil
}

// missionRequiredString is requiredString.
func missionRequiredString(v any, label string) (string, error) {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", label)
	}
	return s, nil
}

// missionOptionalString is optionalString on object[key]: set reports a present, valid string.
func missionOptionalString(o *jsObject, key, label string) (s string, set bool, err error) {
	v, present := dynGet(o, key)
	if !present {
		return "", false, nil
	}
	s, err = missionRequiredString(v, label)
	return s, err == nil, err
}

// safeInteger is Number.isSafeInteger.
func safeInteger(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

// isoMillis is Date.prototype.toISOString.
func isoMillis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

var isoDatePattern = regexp.MustCompile(`^(?:[+-]\d{6}|\d{4})(?:-(\d\d)(?:-(\d\d))?)?(?:T(\d\d):(\d\d)(?::(\d\d)(?:\.\d+)?)?(?:Z|[+-]\d\d:\d\d)?)?$`)

var legacyDateLayouts = []string{
	time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC, time.UnixDate, time.RubyDate,
	"Jan 2 2006", "Jan 2, 2006", "January 2, 2006", "January 2 2006", "2 Jan 2006", "2006/01/02", "1/2/2006",
	"Jan 2 2006 15:04:05", "Jan 2, 2006 15:04:05", "2006/01/02 15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04",
	"Mon Jan 02 2006 15:04:05 GMT-0700", "Mon Jan 02 2006", "Mon, 02 Jan 2006",
}

// jsDateParses reports whether Date.parse(s) is not NaN: the ECMAScript date-time format (what the store writes) and the
// common forms V8's legacy parser accepts.
func jsDateParses(s string) bool {
	s = jsTrim(s)
	if m := isoDatePattern.FindStringSubmatch(s); m != nil {
		num := func(i int) int {
			n, _ := strconv.Atoi(m[i])
			return n
		}
		if m[1] != "" && (num(1) < 1 || num(1) > 12) {
			return false
		}
		if m[2] != "" && (num(2) < 1 || num(2) > 31) {
			return false
		}
		if m[3] != "" && num(3) > 24 {
			return false
		}
		if m[4] != "" && num(4) > 59 {
			return false
		}
		if m[5] != "" && num(5) > 59 {
			return false
		}
		return !(m[3] != "" && num(3) == 24 && (num(4) != 0 || m[5] != "" && num(5) != 0))
	}
	for _, layout := range legacyDateLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// missionTimestamp is timestamp.
func missionTimestamp(v any, label string) (string, error) {
	s, err := missionRequiredString(v, label)
	if err != nil {
		return "", err
	}
	if !jsDateParses(s) {
		return "", fmt.Errorf("%s must be an ISO timestamp", label)
	}
	return s, nil
}

// utf16Units is String.prototype.length.
func utf16Units(s string) int {
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

// utf16Prefix is s.slice(0, n) for text that does not split a surrogate pair at n.
func utf16Prefix(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if n >= len(units) {
		return s
	}
	return string(utf16.Decode(units[:n]))
}

// jsCollapseSpaces is value.replace(/\s+/g, " ").trim().
func jsCollapseSpaces(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if isJSSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if space {
		out += " "
	}
	return jsTrim(out)
}
