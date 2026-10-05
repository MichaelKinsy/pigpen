package pitypesafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The library moves JSON as a tree of nil, bool, float64, string, []any and
// *Object. Object remembers key order the way a JavaScript object does, because
// the order of questions, criteria and answers is observable: it decides how a
// large request is chunked, the order the model reads Choice options in, and the
// order answers are printed. Go maps cannot carry it.

// Object is a JSON object that keeps insertion order.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject returns an empty Object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Set stores a value. Setting an existing key keeps its position, as JavaScript does.
func (o *Object) Set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

// Get returns the value for key and whether it is present.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Has reports whether key is present.
func (o *Object) Has(key string) bool { _, ok := o.vals[key]; return ok }

// Delete removes a key.
func (o *Object) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the keys in insertion order. The slice is a copy.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Clone copies the object and every value below it.
func (o *Object) Clone() *Object {
	c := NewObject()
	for _, k := range o.keys {
		c.Set(k, cloneValue(o.vals[k]))
	}
	return c
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case *Object:
		return t.Clone()
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

// ParseJSON decodes JSON text into a tree that keeps object key order. Numbers are float64, like JavaScript's.
func ParseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := NewObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Set(key, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %q", t)
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !math.IsInf(f, 0) {
			return nil, err
		}
		return f, nil
	default:
		return tok, nil // string, bool, nil
	}
}

// FromGo converts any Go value to a tree: a tree passes through, everything else takes the
// encoding/json round trip. A value JSON cannot carry (a cycle, NaN, a function) is an error.
func FromGo(v any) (any, error) {
	if isTree(v) {
		return v, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return ParseJSON(data)
}

func isTree(v any) bool {
	switch t := v.(type) {
	case nil, bool, string:
		return true
	case float64:
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	case *Object:
		for _, k := range t.keys {
			if !isTree(t.vals[k]) {
				return false
			}
		}
		return t != nil
	case []any:
		for _, e := range t {
			if !isTree(e) {
				return false
			}
		}
		return true
	}
	return false
}

// EncodeJSON serializes a tree the way JSON.stringify does: keys in insertion order, no
// insignificant whitespace, U+2028 and U+2029 unescaped, numbers in JavaScript's format. The
// byte length of this text is what the input limit measures.
func EncodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case string:
		writeJSString(b, t)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("cannot encode %v as JSON", t)
		}
		b.WriteString(jsNumber(t))
	case *Object:
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSString(b, k)
			b.WriteByte(':')
			if err := encodeValue(b, t.vals[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encodeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		tree, err := FromGo(v)
		if err != nil {
			return err
		}
		if isTree(tree) {
			return encodeValue(b, tree)
		}
		return fmt.Errorf("cannot encode %T as JSON", v)
	}
	return nil
}

func writeJSString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range strings.ToValidUTF8(s, "\uFFFD") {
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
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				var buf [utf8.UTFMax]byte
				n := utf8.EncodeRune(buf[:], r)
				b.Write(buf[:n])
			}
		}
	}
	b.WriteByte('"')
}

// jsNumber formats like JavaScript's Number.prototype.toString for the JSON range.
func jsNumber(f float64) string {
	if f == 0 {
		return "0" // also -0
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64) // 1e+21, 1.5e-07
		mant, exp, _ := strings.Cut(s, "e")
		sign := exp[0]
		digits := strings.TrimLeft(exp[1:], "0")
		return mant + "e" + string(sign) + digits
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
