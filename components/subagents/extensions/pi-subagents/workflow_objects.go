package pi_subagents

import (
	"bytes"
	"encoding/json"
)

// jo builds an ordered object from key, value pairs (an object literal).
func jo(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// joOf is jo with a base: fields of the overrides replace the base's.
func joOf(base *jsObject, kv ...any) *jsObject {
	o := base.clone()
	for i := 0; i < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// jsonMarshalNoEscape is JSON.stringify for a typed value: compact, and without Go's escaping of <, > and &.
func jsonMarshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
