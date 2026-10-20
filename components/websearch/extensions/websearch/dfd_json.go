package websearch

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// jsObj is a parsed JSON object with JavaScript's key order: array-index keys ascending first, then the others
// in the order they first appear. A repeated key keeps its first position and its last value.
type jsObj struct {
	keys []string
	m    map[string]any
}

func (o *jsObj) get(k string) (any, bool) {
	v, ok := o.m[k]
	return v, ok
}

func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }

// jsonParse is JSON.parse: values are nil, bool, float64, string, []any and *jsObj.
func jsonParse(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	v, err := dfdJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("Unexpected non-whitespace character after JSON")
	}
	return v, nil
}

func dfdJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := dfdJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		case '{':
			obj := &jsObj{m: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key := kt.(string)
				v, err := dfdJSONValue(dec)
				if err != nil {
					return nil, err
				}
				if _, seen := obj.m[key]; !seen {
					obj.keys = append(obj.keys, key)
				}
				obj.m[key] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			obj.reorder()
			return obj, nil
		}
		return nil, errors.New("unexpected delimiter")
	case string, float64, bool, nil:
		return t, nil
	}
	return nil, errors.New("unexpected token")
}

func (o *jsObj) reorder() {
	hasInt := false
	for _, k := range o.keys {
		if _, ok := ldIntKey(k); ok {
			hasInt = true
			break
		}
	}
	if !hasInt {
		return
	}
	var ints, rest []string
	for _, k := range o.keys {
		if _, ok := ldIntKey(k); ok {
			ints = append(ints, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.SliceStable(ints, func(i, j int) bool {
		a, _ := ldIntKey(ints[i])
		b, _ := ldIntKey(ints[j])
		return a < b
	})
	o.keys = append(ints, rest...)
}

