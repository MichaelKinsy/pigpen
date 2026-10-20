package pi_subagents

import (
	"math"
	"sort"
)

// The policy modules validate values that arrive as decoded JSON, as the original's validators do with `unknown`. A value is one
// of: nil or undef{} (a JavaScript undefined, and a JSON null counts as unset here as it does for the tool's parameters),
// bool, a number (float64, or an int kind), string, []any (or []string), *jsObject or map[string]any (an object). A
// map[string]any has no order, so its keys are visited in sorted order (JavaScript visits array-index keys, then insertion
// order); a caller that needs the model's order passes the *jsObject parseJSON returns.

// isUndefined is `value === undefined`.
func isUndefined(v any) bool {
	if v == nil {
		return true
	}
	_, ok := v.(undef)
	return ok
}

// asObject is `value && typeof value === "object" && !Array.isArray(value)`.
func asObject(v any) (*jsObject, bool) {
	switch o := v.(type) {
	case *jsObject:
		return o, o != nil
	case map[string]any:
		out := newObject()
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.set(k, o[k])
		}
		return out, true
	}
	return nil, false
}

// asArray is Array.isArray.
func asArray(v any) ([]any, bool) {
	switch a := v.(type) {
	case []any:
		return a, true
	case []string:
		out := make([]any, len(a))
		for i, s := range a {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// asNumber is `typeof value === "number"`.
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

// isInteger is Number.isInteger on a value.
func isInteger(v any) bool {
	f, ok := asNumber(v)
	return ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
}

// isSafeInteger is Number.isSafeInteger on a value.
func isSafeInteger(v any) bool {
	f, ok := asNumber(v)
	return ok && isInteger(v) && math.Abs(f) <= 1<<53-1
}

// hasOwn is Object.hasOwn(object, key) for a key that is not undefined.
func (o *jsObject) hasOwn(k string) bool {
	_, ok := o.vals[k]
	return ok
}

// stringsOf converts a decoded string list.
func stringsOf(v any) []string {
	a, _ := asArray(v)
	out := make([]string, 0, len(a))
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
