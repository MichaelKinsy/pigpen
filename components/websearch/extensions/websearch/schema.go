package websearch

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Ordered JSON for the tool parameter schemas. The original's TypeBox output has a fixed key
// order, and tool definitions are compared by hash, so the schemas are built in that order
// instead of as Go maps.

type kv struct {
	k string
	v any
}

// obj is a JSON object that keeps insertion order.
type obj []kv

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := marshalNoEscape(e.k)
		b.Write(key)
		b.WriteByte(':')
		val, err := marshalNoEscape(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalNoEscape is JSON.stringify's escaping: <, > and & stay as they are.
func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func withDescription(o obj, desc string) obj {
	if desc != "" {
		o = append(o, kv{"description", desc})
	}
	return o
}

func sString(desc string) obj { return withDescription(obj{{"type", "string"}}, desc) }
func sBool(desc string) obj   { return withDescription(obj{{"type", "boolean"}}, desc) }
func sStringArray(desc string) obj {
	return withDescription(obj{{"type", "array"}, {"items", sString("")}}, desc)
}
func sEnum(values []string, desc string) obj {
	return withDescription(obj{{"type", "string"}, {"enum", values}}, desc)
}
func sInteger(min, max *int, desc string) obj {
	o := obj{{"type", "integer"}}
	if min != nil {
		o = append(o, kv{"minimum", *min})
	}
	if max != nil {
		o = append(o, kv{"maximum", *max})
	}
	return withDescription(o, desc)
}
func sObject(required []string, props obj) obj {
	o := obj{{"type", "object"}}
	if len(required) > 0 {
		o = append(o, kv{"required", required})
	}
	return append(o, kv{"properties", props})
}

func intp(v int) *int { return &v }

// searchProviderSchema is the `provider` parameter: one provider (or auto/all), or a list.
func searchProviderSchema(desc string, allowed []string) obj {
	single := append([]string{"auto", "all"}, allowed...)
	return withDescription(obj{{"anyOf", []any{
		sEnum(single, ""),
		obj{{"type", "array"}, {"items", sEnum(allowed, "")}, {"minItems", 1}},
	}}}, desc)
}

// toPlain converts an ordered schema to plain maps and slices.
func toPlain(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}

func labels(providers []string) string {
	out := make([]string, len(providers))
	for i, p := range providers {
		out[i] = ProviderLabel(p)
	}
	return strings.Join(out, ", ")
}
