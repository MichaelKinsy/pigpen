// Package jsobj is a JavaScript object as `JSON.parse` makes it and `JSON.stringify(value, null, 2)` writes it: keys keep the order
// they were first set in, a key may hold `undefined` (kept in place, left out of the text), numbers are float64.
package jsobj

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Undefined is the JavaScript `undefined` value (an `undefined` property stays in the object and is not written).
type undefinedType struct{}

// Undefined is the value of a key that is set but undefined.
var Undefined = undefinedType{}

// IsUndefined reports an unset or undefined value.
func IsUndefined(v any) bool {
	_, ok := v.(undefinedType)
	return ok
}

// Object is an insertion-ordered JavaScript object.
type Object struct {
	keys []string
	vals map[string]any
}

// New returns an empty object.
func New() *Object { return &Object{vals: map[string]any{}} }

// Keys returns the keys in order (including those holding undefined).
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Has reports whether the key exists (even holding undefined).
func (o *Object) Has(k string) bool { _, ok := o.vals[k]; return ok }

// Get returns the value, or Undefined for a missing key.
func (o *Object) Get(k string) any {
	if v, ok := o.vals[k]; ok {
		return v
	}
	return Undefined
}

// Set assigns a property: a new key goes last, an existing one keeps its place.
func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// Clone is `{...o}`: a shallow copy with the same key order.
func (o *Object) Clone() *Object {
	c := &Object{keys: append([]string(nil), o.keys...), vals: make(map[string]any, len(o.vals))}
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}

// Assign is Object.assign(o, src): src's keys in order.
func (o *Object) Assign(src *Object) {
	for _, k := range src.keys {
		o.Set(k, src.vals[k])
	}
}

// Parse is JSON.parse: an Object for objects, []any for arrays, float64, string, bool, nil (null).
func Parse(text string) (any, error) {
	if !json.Valid([]byte(text)) {
		// JSON.parse's message differs by position; callers only log it
		var sink any
		err := json.Unmarshal([]byte(text), &sink)
		if err == nil {
			err = errors.New("invalid JSON")
		}
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	return decodeValue(dec)
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := New()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
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
		}
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !math.IsInf(f, 0) {
			return nil, err
		}
		return f, nil
	case string, bool, nil:
		return t, nil
	}
	return nil, io.ErrUnexpectedEOF
}

// Stringify is JSON.stringify(v, null, 2) (undefined members are left out of objects).
func Stringify(v any) (string, error) {
	var b bytes.Buffer
	if err := write(&b, v, 0, true); err != nil {
		return "", err
	}
	return b.String(), nil
}

func scalar(v any) (string, error) {
	out, err := sdkjson.MarshalStringify(v)
	return string(out), err
}

func write(b *bytes.Buffer, v any, depth int, top bool) error {
	indent := strings.Repeat("  ", depth+1)
	closeIndent := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case *Object:
		var parts []string
		for _, k := range t.keys {
			val := t.vals[k]
			if IsUndefined(val) {
				continue
			}
			var sub bytes.Buffer
			if err := write(&sub, val, depth+1, false); err != nil {
				return err
			}
			if sub.Len() == 0 { // not serializable (undefined inside)
				continue
			}
			ks, err := scalar(k)
			if err != nil {
				return err
			}
			parts = append(parts, indent+ks+": "+sub.String())
		}
		if len(parts) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n" + strings.Join(parts, ",\n") + "\n" + closeIndent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		parts := make([]string, len(t))
		for i, e := range t {
			var sub bytes.Buffer
			if err := write(&sub, e, depth+1, false); err != nil {
				return err
			}
			if sub.Len() == 0 || IsUndefined(e) {
				sub.Reset()
				sub.WriteString("null")
			}
			parts[i] = indent + sub.String()
		}
		b.WriteString("[\n" + strings.Join(parts, ",\n") + "\n" + closeIndent + "]")
	case undefinedType:
		if top {
			return nil
		}
		return nil
	case nil:
		b.WriteString("null")
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			b.WriteString("null")
			return nil
		}
		s, err := scalar(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case string, bool, int, int64:
		s, err := scalar(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
	default:
		s, err := scalar(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
	}
	return nil
}

// ToGo converts to plain Go values for a JSON wire (an Object becomes a map: key order is not kept; use Raw for ordered output).
func ToGo(v any) any {
	switch t := v.(type) {
	case *Object:
		m := map[string]any{}
		for _, k := range t.keys {
			if !IsUndefined(t.vals[k]) {
				m[k] = ToGo(t.vals[k])
			}
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			if IsUndefined(e) {
				out[i] = nil
			} else {
				out[i] = ToGo(e)
			}
		}
		return out
	}
	return v
}

// MarshalJSON writes the object compactly, in key order, without its undefined members (JSON.stringify(o)).
func (o *Object) MarshalJSON() ([]byte, error) {
	s, err := Stringify(o)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
