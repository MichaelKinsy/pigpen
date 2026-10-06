// Package strictjson decodes JSON that comes from a file an operator or a host wrote, and refuses what encoding/json
// accepts silently: a repeated object key, a key that names a struct field only case-insensitively, an unknown field,
// a second value after the first.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

const maxDepth = 32

// ErrInvalid is returned for any violation. It carries no input text.
var ErrInvalid = errors.New("invalid JSON")

// Decode decodes exactly one JSON value into v. Unknown fields are an error when strict is true.
//
// encoding/json matches an object key to a struct field case-insensitively (with Unicode folding), so
// {"Origin": ...} would fill the field tagged "origin", and {"origin": ..., "Origin": ...} would pass the
// repeated-key check and keep the last value. A key must therefore equal a field's name exactly; a key that matches
// one only by folding is an error in both modes.
func Decode(data []byte, v any, strict bool) error {
	if err := noDuplicates(data); err != nil {
		return err
	}
	if err := exactKeys(data, reflect.TypeOf(v), strict); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		return ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func noDuplicates(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walk(dec, 0); err != nil {
		return err
	}
	return nil
}

func walk(dec *json.Decoder, depth int) error {
	if depth > maxDepth {
		return ErrInvalid
	}
	tok, err := dec.Token()
	if err != nil {
		return ErrInvalid
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return ErrInvalid
			}
			key, _ := k.(string)
			if _, dup := seen[key]; dup {
				return ErrInvalid
			}
			seen[key] = struct{}{}
			if err := walk(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walk(dec, depth+1); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return ErrInvalid
	}
	return nil
}

// exactKeys walks data alongside the type it decodes into and refuses a key that names a struct field only
// case-insensitively, and, when strict, a key that names no field.
func exactKeys(data []byte, t reflect.Type, strict bool) error {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return ErrInvalid
	}
	return checkKeys(tree, t, strict)
}

func checkKeys(v any, t reflect.Type, strict bool) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil // a type mismatch is the decoder's error
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Name
			if tag, ok := f.Tag.Lookup("json"); ok {
				if tag == "-" {
					continue
				}
				if n, _, _ := strings.Cut(tag, ","); n != "" {
					name = n
				}
			}
			fields[name] = f.Type
		}
		for k, val := range obj {
			ft, ok := fields[k]
			if !ok {
				if strict {
					return ErrInvalid
				}
				for name := range fields {
					if strings.EqualFold(name, k) {
						return ErrInvalid
					}
				}
				continue
			}
			if err := checkKeys(val, ft, strict); err != nil {
				return err
			}
		}
	case reflect.Map:
		if obj, ok := v.(map[string]any); ok {
			for _, val := range obj {
				if err := checkKeys(val, t.Elem(), strict); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if arr, ok := v.([]any); ok {
			for _, val := range arr {
				if err := checkKeys(val, t.Elem(), strict); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
