package langsmith

// JavaScript object semantics the original relies on: insertion-ordered keys, array-index keys first,
// `undefined` values that JSON.stringify drops from an object and writes as null in an array, and the
// object spread `{...a, ...b}`. Every run payload, metadata block and tool result the extension builds is an
// omap so the bytes written equal what JSON.stringify writes for the original's object.
//
// Event data arrives from the PiG host as map[string]any, which has no key order. Such a map is written with
// its keys sorted (in JavaScript's order: array indices ascending first, then the rest by code unit).
// The original's order is that of the JSON text Pi sent; the Go SDK does not expose it (port/PORT.md, finding F1).

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strconv"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// undefinedT is JavaScript's `undefined`. The zero value of the interface, nil, is JSON null.
type undefinedT struct{}

// undef is the value `undefined`.
var undef any = undefinedT{}

func isUndef(v any) bool { _, ok := v.(undefinedT); return ok }

// omap is a JavaScript object: keys in insertion order, a re-set key keeps its place.
type omap struct {
	keys []string
	vals map[string]any
}

func newOmap() *omap { return &omap{vals: map[string]any{}} }

// obj builds an omap from alternating keys and values.
func obj(kv ...any) *omap {
	m := &omap{keys: make([]string, 0, len(kv)/2), vals: make(map[string]any, len(kv)/2)}
	for i := 0; i+1 < len(kv); i += 2 {
		m.set(kv[i].(string), kv[i+1])
	}
	return m
}

func (m *omap) set(k string, v any) *omap {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
	return m
}

// get returns the value of k and whether the key exists (a key holding undefined exists).
func (m *omap) get(k string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.vals[k]
	return v, ok
}

// val is `m[k]`: undef when the key is missing.
func (m *omap) val(k string) any {
	if v, ok := m.get(k); ok {
		return v
	}
	return undef
}

func (m *omap) has(k string) bool { _, ok := m.get(k); return ok }

func (m *omap) del(k string) {
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, key := range m.keys {
		if key == k {
			m.keys = append(m.keys[:i:i], m.keys[i+1:]...)
			break
		}
	}
}

// size is Object.keys(m).length.
func (m *omap) size() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

func (m *omap) clone() *omap {
	c := &omap{keys: append([]string(nil), m.keys...), vals: make(map[string]any, len(m.vals))}
	for k, v := range m.vals {
		c.vals[k] = v
	}
	return c
}

// spread is `{...m, ...src}` in place: src's keys overwrite or append. src is an *omap, a map[string]any
// (sorted keys) or null/undefined (no-op).
func (m *omap) spread(src any) *omap {
	switch s := src.(type) {
	case *omap:
		if s == nil {
			return m
		}
		for _, k := range s.ownKeys() {
			m.set(k, s.vals[k])
		}
	case map[string]any:
		for _, k := range sortedKeys(s) {
			m.set(k, s[k])
		}
	}
	return m
}

// ownKeys is Object.keys: array-index keys ascending, then the others in insertion order.
func (m *omap) ownKeys() []string {
	if m == nil {
		return nil
	}
	hasIndex := false
	for _, k := range m.keys {
		if isArrayIndex(k) {
			hasIndex = true
			break
		}
	}
	if !hasIndex {
		return m.keys
	}
	idx := make([]string, 0, len(m.keys))
	rest := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 32)
		b, _ := strconv.ParseUint(idx[j], 10, 32)
		return a < b
	})
	return append(idx, rest...)
}

// compact drops the keys whose value is undefined, as `Object.fromEntries(entries.filter(v !== undefined))` does.
func (m *omap) compact() *omap {
	out := &omap{keys: make([]string, 0, len(m.keys)), vals: make(map[string]any, len(m.vals))}
	for _, k := range m.ownKeys() {
		if v := m.vals[k]; !isUndef(v) {
			out.keys = append(out.keys, k)
			out.vals[k] = v
		}
	}
	return out
}

// omit is `{...m}` without the listed keys.
func (m *omap) omit(keys ...string) *omap {
	c := m.clone()
	for _, k := range keys {
		c.del(k)
	}
	return c
}

// toOmap converts a decoded JSON object (map[string]any) to an omap with sorted keys; an *omap is returned as is.
func toOmap(v any) (*omap, bool) {
	switch r := v.(type) {
	case *omap:
		return r, r != nil
	case map[string]any:
		m := &omap{keys: make([]string, 0, len(r)), vals: make(map[string]any, len(r))}
		for _, k := range sortedKeys(r) {
			m.keys = append(m.keys, k)
			m.vals[k] = r[k]
		}
		return m, true
	}
	return nil, false
}

// isRecord is types.ts isRecord: a non-null, non-array object.
func isRecord(v any) bool {
	switch r := v.(type) {
	case map[string]any:
		return r != nil
	case *omap:
		return r != nil
	}
	return false
}

// recGet reads `v[k]` of a record: undef when v is not a record or the key is missing.
func recGet(v any, k string) any {
	switch r := v.(type) {
	case map[string]any:
		if x, ok := r[k]; ok {
			return x
		}
	case *omap:
		return r.val(k)
	}
	return undef
}

func isArrayIndex(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < math.MaxUint32
}

// sortedKeys returns the keys of a decoded JSON object in JavaScript property order for a map without
// insertion order: array indices ascending, the rest by UTF-16 code unit.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, cmpJSKey)
	return keys
}

// cmpJSKey orders property keys as JavaScript does: array-index keys first, ascending by value, then the others by
// UTF-16 code unit.
func cmpJSKey(a, b string) int {
	ai, bi := isArrayIndex(a), isArrayIndex(b)
	switch {
	case ai && bi:
		x, _ := strconv.ParseUint(a, 10, 32)
		y, _ := strconv.ParseUint(b, 10, 32)
		return cmp.Compare(x, y)
	case ai:
		return -1
	case bi:
		return 1
	}
	return cmpUTF16(a, b)
}

// cmpUTF16 compares two strings by UTF-16 code unit. UTF-8 byte order equals code point order, which equals UTF-16
// order except between U+E000..U+FFFF and the supplementary planes (and lone surrogates, which are WTF-8 here); the
// byte comparison is exact whenever the first differing byte is below 0xED, which is every ASCII and most real keys.
func cmpUTF16(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] < 0xED && b[i] < 0xED {
			return cmp.Compare(a[i], b[i])
		}
		return cmpUTF16Slow(a, b)
	}
	return cmp.Compare(len(a), len(b))
}

func cmpUTF16Slow(a, b string) int {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return cmp.Compare(ua[i], ub[i])
		}
	}
	return cmp.Compare(len(ua), len(ub))
}

// MarshalJSON lets the SDK codec nest an omap inside a Go value.
func (m *omap) MarshalJSON() ([]byte, error) { return m.AppendStringify(nil) }

// AppendStringify implements json.StringifyAppender.
func (m *omap) AppendStringify(dst []byte) ([]byte, error) {
	if m == nil {
		return append(dst, "null"...), nil
	}
	return appendObject(dst, m)
}

var _ json.StringifyAppender = (*omap)(nil)

// stringify is JSON.stringify(v) for the values the extension builds: nil (null), bool, numbers, string,
// []any, []map..., map[string]any (sorted keys), *omap, undef. An undefined top-level value yields (nil, false).
func stringify(v any) ([]byte, bool, error) {
	if isUndef(v) {
		return nil, false, nil
	}
	out, err := appendValue(make([]byte, 0, 256), v)
	return out, true, err
}

func appendValue(dst []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case undefinedT:
		return append(dst, "null"...), nil // in an array
	case bool:
		if x {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case string:
		return json.AppendStringifyString(dst, x), nil
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendValue(dst, e); err != nil {
				return dst, err
			}
		}
		return append(dst, ']'), nil
	case []string:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = json.AppendStringifyString(dst, e)
		}
		return append(dst, ']'), nil
	case []*omap:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendValue(dst, e); err != nil {
				return dst, err
			}
		}
		return append(dst, ']'), nil
	case *omap:
		if x == nil {
			return append(dst, "null"...), nil
		}
		return appendObject(dst, x)
	case map[string]any:
		if x == nil {
			return append(dst, "null"...), nil
		}
		return appendMap(dst, x)
	}
	return json.AppendStringify(dst, v) // float64, json.Number and anything the SDK codec writes as JSON.stringify does
}

// appendMap writes a decoded JSON object with its keys in JavaScript order, without building an omap.
func appendMap(dst []byte, m map[string]any) ([]byte, error) {
	dst = append(dst, '{')
	first := true
	for _, k := range sortedKeys(m) {
		val := m[k]
		if isUndef(val) {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = json.AppendStringifyString(dst, k)
		dst = append(dst, ':')
		var err error
		if dst, err = appendValue(dst, val); err != nil {
			return dst, err
		}
	}
	return append(dst, '}'), nil
}

func appendObject(dst []byte, m *omap) ([]byte, error) {
	dst = append(dst, '{')
	first := true
	for _, k := range m.ownKeys() {
		val := m.vals[k]
		if isUndef(val) {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = json.AppendStringifyString(dst, k)
		dst = append(dst, ':')
		var err error
		if dst, err = appendValue(dst, val); err != nil {
			return dst, err
		}
	}
	return append(dst, '}'), nil
}
