package ratchet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// DirectionEntry is one key/direction pair inside OrderedDirections.
type DirectionEntry struct {
	Key       string
	Direction Direction
}

// OrderedDirections stores metric -> Direction overrides in insertion order so
// `_state.json` persistence and `StageDigest(..., StagePlan)` match JavaScript's
// `JSON.stringify` key order byte-for-byte.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:33, 398, 411
type OrderedDirections struct {
	entries []DirectionEntry
}

// NewOrderedDirections constructs an OrderedDirections from entries, keeping
// first-seen key position if a key repeats (matching JS object assignment).
func NewOrderedDirections(entries ...DirectionEntry) OrderedDirections {
	var od OrderedDirections
	for _, e := range entries {
		od.Set(e.Key, e.Direction)
	}
	return od
}

// Set sets key to dir, updating in place if present or appending otherwise.
func (o *OrderedDirections) Set(key string, dir Direction) {
	for i := range o.entries {
		if o.entries[i].Key == key {
			o.entries[i].Direction = dir
			return
		}
	}
	o.entries = append(o.entries, DirectionEntry{Key: key, Direction: dir})
}

// Get looks up key's direction.
func (o OrderedDirections) Get(key string) (Direction, bool) {
	for _, e := range o.entries {
		if e.Key == key {
			return e.Direction, true
		}
	}
	return "", false
}

// Len returns the number of entries.
func (o OrderedDirections) Len() int { return len(o.entries) }

// Entries returns a copy of the ordered entries.
func (o OrderedDirections) Entries() []DirectionEntry {
	out := make([]DirectionEntry, len(o.entries))
	copy(out, o.entries)
	return out
}

// Clone returns an independent copy preserving entry order.
func (o OrderedDirections) Clone() OrderedDirections {
	return OrderedDirections{entries: o.Entries()}
}

// MarshalJSON encodes as a JSON object preserving insertion order and
// ECMA-262 string escaping (no HTML or U+2028/U+2029 escaping).
func (o OrderedDirections) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range o.entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(jsStringifyString(e.Key))
		buf.WriteByte(':')
		buf.WriteString(jsStringifyString(string(e.Direction)))
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object preserving key order.
func (o *OrderedDirections) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		o.entries = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return errors.New("directions must be a JSON object")
	}
	o.entries = nil
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("direction key must be a string")
		}
		var val string
		if err := dec.Decode(&val); err != nil {
			return err
		}
		o.Set(key, Direction(val))
	}
	endTok, err := dec.Token()
	if err != nil {
		return err
	}
	if endDelim, ok := endTok.(json.Delim); !ok || endDelim != '}' {
		return errors.New("expected closing brace for directions")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing tokens after directions")
	}
	return nil
}

// jsStringifyString encodes s identically to ECMAScript's JSON.stringify for
// valid UTF-8 strings: escapes ", \, \b, \f, \n, \r, \t and control chars
// < 0x20 as \u00xx, while leaving <, >, &, U+2028, U+2029 unescaped.
func jsStringifyString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r >= 0 && r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// unescapeJSLineSeparators converts Go's json.Encoder `\u2028`/`\u2029` escapes
// back to literal UTF-8 U+2028/U+2029 bytes only when the backslash is unescaped
// (matching ECMAScript JSON.stringify).
func unescapeJSLineSeparators(raw []byte) []byte {
	if !bytes.Contains(raw, []byte(`\u2028`)) && !bytes.Contains(raw, []byte(`\u2029`)) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			out = append(out, c)
			continue
		}
		if c == '"' {
			inString = false
			out = append(out, c)
			continue
		}
		if c == '\\' && i+1 < len(raw) {
			if i+6 <= len(raw) && raw[i+1] == 'u' {
				seq := raw[i+2 : i+6]
				if bytes.Equal(seq, []byte("2028")) {
					out = append(out, "\u2028"...)
					i += 5
					continue
				}
				if bytes.Equal(seq, []byte("2029")) {
					out = append(out, "\u2029"...)
					i += 5
					continue
				}
			}
			// Copy escaped pair verbatim (e.g. `\\`, `\"`, `\n`).
			out = append(out, c, raw[i+1])
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}

// marshalJSCompact encodes v as compact JSON matching ECMAScript JSON.stringify
// (no HTML escaping, no U+2028/U+2029 escaping).
func marshalJSCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return unescapeJSLineSeparators(raw), nil
}

// jsToFixed formats v with exact ECMAScript Number.prototype.toFixed(digits)
// rounding semantics (half away from zero on the exact IEEE-754 binary64 value,
// preserving "-0.000" only for negative non-zero values that round to zero, and
// "0.000" for +0 and -0).
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:705-706, 838, 844
func jsToFixed(v float64, digits int) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "Infinity"
	}
	if math.IsInf(v, -1) {
		return "-Infinity"
	}
	if digits < 0 {
		digits = 0
	}
	// Per ECMA-262 21.1.3.3, toFixed(-0) produces "0.000" (negative sign only when x < 0).
	neg := v < 0
	absVal := v
	if neg {
		absVal = -absVal
	}
	// For very large numbers (|v| >= 1e21), fall back to strconv formatting.
	if absVal >= 1e21 {
		return strconv.FormatFloat(v, 'g', -1, 64)
	}

	// Exact rational value of the IEEE-754 float64.
	r := new(big.Rat).SetFloat64(absVal)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))

	num := scaled.Num()
	den := scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// Round half away from zero: if 2*rem >= den, increment q.
	twiceRem := new(big.Int).Lsh(rem, 1)
	if twiceRem.Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}

	s := q.String()
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if digits == 0 {
		b.WriteString(s)
		return b.String()
	}
	if len(s) <= digits {
		b.WriteString("0.")
		for range digits - len(s) {
			b.WriteByte('0')
		}
		b.WriteString(s)
		return b.String()
	}
	split := len(s) - digits
	b.WriteString(s[:split])
	b.WriteByte('.')
	b.WriteString(s[split:])
	return b.String()
}

// jsRound matches JavaScript's Math.round for non-negative finite numbers:
// half toward +Infinity, while returning 0 for 0.49999999999999994 (where
// naive `x + 0.5` rounds up to 1.0 in float64).
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:338
func jsRound(x float64) int {
	if x <= 0 {
		return 0
	}
	floor := math.Floor(x)
	if x-floor >= 0.5 {
		return int(floor) + 1
	}
	return int(floor)
}

// compareUTF16 compares two strings in UTF-16 code-unit order, matching
// JavaScript's `<` / `>` and `Array.prototype.sort()` default comparator
// (so astral characters U+10000..U+10FFFF encoded as surrogate pairs 0xD800..0xDFFF
// sort BEFORE U+E000..U+FFFF).
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:325, 345-346, 360, 368, 408
func compareUTF16(a, b string) int {
	// Fast path: identical prefix / ASCII.
	i := 0
	minLen := min(len(a), len(b))
	for i < minLen && a[i] < 0x80 && b[i] < 0x80 {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
		i++
	}
	ua := utf16.Encode([]rune(a[i:]))
	ub := utf16.Encode([]rune(b[i:]))
	n := min(len(ua), len(ub))
	for k := range n {
		if ua[k] != ub[k] {
			if ua[k] < ub[k] {
				return -1
			}
			return 1
		}
	}
	if len(ua) < len(ub) {
		return -1
	}
	if len(ua) > len(ub) {
		return 1
	}
	return 0
}

func sortStringsUTF16(s []string) {
	sort.Slice(s, func(i, j int) bool {
		return compareUTF16(s[i], s[j]) < 0
	})
}
