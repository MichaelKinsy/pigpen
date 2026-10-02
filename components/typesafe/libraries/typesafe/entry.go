package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Entry is text, a JSON object, a JSON array, or null: the value of a state, of a
// question's instructions, and of every criterion description (the TypeScript
// EntryType). The zero Entry is OMITTED: the field is left out of the request body,
// which is different from an explicit null that is sent as null.
//
// Build an Entry with [Text], [Value] or [Null], or let a builder convert a plain Go
// value with [EntryOf]. A JSON number or boolean is not an Entry; marshalling one
// fails with a *TypeSafeError.
type Entry struct {
	present bool
	value   any
}

// Null is the explicit JSON null.
var Null = Entry{present: true}

// Text returns a text Entry.
func Text(s string) Entry { return Entry{present: true, value: s} }

// Value returns an Entry holding v: a map, slice, struct or json.RawMessage that
// marshals to a JSON object or array, a string, or nil (which is null). To keep key
// order, pass a json.RawMessage; Go maps marshal with sorted keys.
func Value(v any) Entry { return Entry{present: true, value: v} }

// EntryOf converts what a builder accepts: nil is [Null], an Entry is returned as is,
// anything else is [Value].
func EntryOf(v any) Entry {
	switch x := v.(type) {
	case nil:
		return Null
	case Entry:
		return x
	}
	return Value(v)
}

// IsOmitted reports whether the Entry is the zero value (left out of the request).
func (e Entry) IsOmitted() bool { return !e.present }

// IsNull reports whether the Entry is an explicit null.
func (e Entry) IsNull() bool { return e.present && e.value == nil }

// Data returns the Go value of the Entry: string, map[string]any, []any, or nil for
// null and omitted.
func (e Entry) Data() any {
	if !e.present || e.value == nil {
		return nil
	}
	if s, ok := e.value.(string); ok {
		return s
	}
	raw, err := e.MarshalJSON()
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// marshalPlain is json.Marshal without HTML escaping: the bodies then carry <, > and & as
// written, like the official SDK's JSON.stringify. U+2028 and U+2029 are always escaped
// (\u2028, \u2029; the receiving JSON parser decodes them the same): encoding/json escapes
// them in a string, but Go 1.26 copies them through raw from a Marshaler or json.RawMessage
// when HTML escaping is off, and Go 1.27 escapes those too. The raw bytes can only occur
// inside a JSON string, so replacing them keeps the body (and its Content-Length) the same
// on every toolchain.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return escapeLineSeparators(bytes.TrimSuffix(b.Bytes(), []byte("\n"))), nil
}

// escapeLineSeparators replaces raw U+2028 and U+2029 in encoded JSON with \u2028 and \u2029.
func escapeLineSeparators(out []byte) []byte {
	out = bytes.ReplaceAll(out, []byte("\u2028"), []byte(`\u2028`))
	return bytes.ReplaceAll(out, []byte("\u2029"), []byte(`\u2029`))
}

// MarshalJSON encodes the Entry; an omitted Entry encodes as null (parents skip
// omitted fields themselves).
func (e Entry) MarshalJSON() ([]byte, error) {
	if !e.present || e.value == nil {
		return []byte("null"), nil
	}
	raw, err := marshalPlain(e.value)
	if err != nil {
		return nil, &TypeSafeError{Message: fmt.Sprintf("Cannot encode a description or instructions value: %v", err), Cause: err}
	}
	switch raw[0] {
	case '"', '{', '[', 'n':
		return raw, nil
	}
	return nil, &TypeSafeError{Message: fmt.Sprintf("A description, instructions or state must be text, a JSON object, a JSON array or null, got %s.", raw)}
}

// UnmarshalJSON decodes any JSON value; a decoded null is [Null], not omitted.
func (e *Entry) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return &TypeSafeError{Message: "Empty JSON value."}
	}
	switch data[0] {
	case 'n':
		*e = Null
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*e = Text(s)
	case '{', '[':
		*e = Value(json.RawMessage(append([]byte(nil), data...)))
	default:
		return &TypeSafeError{Message: fmt.Sprintf("A description, instructions or state must be text, a JSON object, a JSON array or null, got %s.", data)}
	}
	return nil
}

var _ json.Marshaler = Entry{}
