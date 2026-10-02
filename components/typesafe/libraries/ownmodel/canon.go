package ownmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// The Python oracle serializes with pydantic_core.to_json: compact, no ASCII escaping, key
// order kept. These helpers reproduce it so that prompts match the oracle byte for byte.

// writeJSONString writes s like serde_json: only the quote, the backslash and the
// control characters are escaped.
func writeJSONString(b *strings.Builder, s string) {
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
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// canonicalJSON re-encodes JSON text the way pydantic_core.to_json encodes the parsed
// value: compact, key order kept, strings escaped like serde_json. Number literals are
// kept as written.
func canonicalJSON(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := canonValue(dec, &b); err != nil {
		return "", err
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", fmt.Errorf("unexpected data after the JSON value")
	}
	return b.String(), nil
}

func canonValue(dec *json.Decoder, b *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			b.WriteByte('{')
			first := true
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				if !first {
					b.WriteByte(',')
				}
				first = false
				writeJSONString(b, kt.(string))
				b.WriteByte(':')
				if err := canonValue(dec, b); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			b.WriteByte('}')
		case '[':
			b.WriteByte('[')
			first := true
			for dec.More() {
				if !first {
					b.WriteByte(',')
				}
				first = false
				if err := canonValue(dec, b); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			b.WriteByte(']')
		}
	case string:
		writeJSONString(b, v)
	case json.Number:
		b.WriteString(v.String())
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	}
	return nil
}

// kv and obj are an ordered JSON object, so schemas keep pydantic's key order.
type kv struct {
	k string
	v any
}

type obj []kv

func writeAny(b *strings.Builder, v any) {
	switch x := v.(type) {
	case obj:
		b.WriteByte('{')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, e.k)
			b.WriteByte(':')
			writeAny(b, e.v)
		}
		b.WriteByte('}')
	case []string:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, e)
		}
		b.WriteByte(']')
	case string:
		writeJSONString(b, x)
	case bool:
		b.WriteString(strconv.FormatBool(x))
	default:
		panic(fmt.Sprintf("writeAny: unsupported %T", v))
	}
}

// toMap converts an ordered object to the generic form handed to a Model.
func toMap(v any) any {
	switch x := v.(type) {
	case obj:
		m := make(map[string]any, len(x))
		for _, e := range x {
			m[e.k] = toMap(e.v)
		}
		return m
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validUTF8 replaces invalid bytes so that a prompt is always valid UTF-8 (Python str cannot hold them).
func validUTF8(s string) string { return strings.ToValidUTF8(s, "\uFFFD") }
