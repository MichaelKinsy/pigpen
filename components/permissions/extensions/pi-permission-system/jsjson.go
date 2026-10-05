package pi_permission_system

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsObject is a decoded JSON object that keeps the order JavaScript iterates its keys in: array-index keys ascending, then the
// rest in insertion order. A repeated key keeps its first position and takes the last value, as JSON.parse does.
type jsObject struct {
	keys []string
	vals map[string]any
}

func newObject() *jsObject { return &jsObject{vals: map[string]any{}} }

func (o *jsObject) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsObject) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// has reports a present key whose value is not undefined.
func (o *jsObject) has(k string) bool {
	v, ok := o.vals[k]
	if !ok {
		return false
	}
	_, u := v.(undef)
	return !u
}

func (o *jsObject) str(k string) (string, bool) {
	s, ok := o.vals[k].(string)
	return s, ok
}

func (o *jsObject) num(k string) (float64, bool) {
	f, ok := o.vals[k].(float64)
	return f, ok
}

func (o *jsObject) flag(k string) (bool, bool) {
	b, ok := o.vals[k].(bool)
	return b, ok
}

func (o *jsObject) obj(k string) *jsObject {
	x, _ := o.vals[k].(*jsObject)
	return x
}

// setOpt sets k to v, or to undefined when v is nil (a JavaScript `value ?? undefined` property).
func (o *jsObject) setOpt(k string, v any) {
	if v == nil {
		v = undef{}
	}
	o.set(k, v)
}

func (o *jsObject) clone() *jsObject {
	c := newObject()
	for _, k := range o.keys {
		c.set(k, o.vals[k])
	}
	return c
}

// isArrayIndex reports whether k is a canonical array index (0, 1, ..., 2^32-2), which JavaScript orders before other keys.
func isArrayIndex(k string) bool {
	if k == "" || (len(k) > 1 && k[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < 1<<32-1 && strconv.FormatUint(n, 10) == k
}

// order returns the keys in Object.keys order.
func (o *jsObject) order() []string {
	var idx, rest []string
	for _, k := range o.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 64)
		b, _ := strconv.ParseUint(idx[j], 10, 64)
		return a < b
	})
	return append(idx, rest...)
}

// parseJSON decodes one JSON value: objects become *jsObject, arrays []any, numbers float64, null nil.
func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			obj := newObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				obj.set(kt.(string), v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		}
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	case json.Number:
		// JSON.parse reads a number beyond the double range as ±Infinity (or 0 when it underflows), not as an error.
		f, err := strconv.ParseFloat(string(t), 64)
		if errors.Is(err, strconv.ErrRange) {
			err = nil
		}
		return f, err
	default:
		return tok, nil // string, bool, nil
	}
}

// marshalJSON is JSON.stringify(v, null, indent): indent "" is compact, otherwise one entry per line. It encodes *jsObject in
// its original key order, maps with sorted keys, and leaves out nothing (undefined does not occur in decoded JSON).
func marshalJSON(v any, indent string) string {
	var b strings.Builder
	writeJSON(&b, v, indent, "")
	return b.String()
}

func writeJSON(b *strings.Builder, v any, indent, cur string) {
	nl := func(level string) {
		if indent != "" {
			b.WriteString("\n" + level)
		}
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case float64:
		b.WriteString(jsNumber(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case string:
		writeJSONString(b, x)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(cur + indent)
			writeJSON(b, e, indent, cur+indent)
		}
		nl(cur)
		b.WriteByte(']')
	case *jsObject:
		writeObject(b, x.order(), x.vals, indent, cur, nl)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeObject(b, keys, x, indent, cur, nl)
	default:
		b.WriteString("null")
	}
}

// undef is a JavaScript undefined held in an object: the key keeps its position, JSON.stringify leaves it out.
type undef struct{}

func writeObject(b *strings.Builder, keys []string, vals map[string]any, indent, cur string, nl func(string)) {
	shown := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, u := vals[k].(undef); !u {
			shown = append(shown, k)
		}
	}
	keys = shown
	if len(keys) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		nl(cur + indent)
		writeJSONString(b, k)
		b.WriteByte(':')
		if indent != "" {
			b.WriteByte(' ')
		}
		writeJSON(b, vals[k], indent, cur+indent)
	}
	nl(cur)
	b.WriteByte('}')
}

// writeJSONString quotes like JSON.stringify: control characters escape, nothing else does (no HTML escaping, U+2028 stays).
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20:
			b.WriteString(`\u00` + string("0123456789abcdef"[r>>4]) + string("0123456789abcdef"[r&15]))
		case r == utf8.RuneError && size == 1:
			b.WriteString(`\ufffd`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// MarshalJSON lets an ordered object travel through encoding/json (a session entry's data) with its keys in order.
func (o *jsObject) MarshalJSON() ([]byte, error) { return []byte(marshalJSON(o, "")), nil }
