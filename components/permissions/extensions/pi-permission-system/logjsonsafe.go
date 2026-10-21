package pi_permission_system

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Port of src/logging/json-safe-stringify.ts: JSON.stringify that survives Error instances, bigints and object cycles.
//
// JavaScript values reach this code as: nil (null), undef{} (undefined), bool, float64 and the Go integer kinds, string, *big.Int
// (bigint), *JSError (an Error instance), error (read as an Error with no stack), *jsObject or map[string]any (an object, the map
// with sorted keys), []any (an array) and any func (a function, which JSON leaves out). A string may carry a lone UTF-16 surrogate
// as its WTF-8 bytes (see utf16Prefix); the writer escapes it as JSON.stringify does.

// JSError is a JavaScript Error: what the replacer expands to its name, message and stack.
type JSError struct {
	Name, Message string
	// Stack is the stack text; nil is an Error whose stack is undefined, which JSON leaves out.
	Stack *string
}

// JSONValueTransform rewrites a value before the Error / bigint / cycle handling runs (JsonValueTransform).
type JSONValueTransform func(key string, value any) any

// JSONReplacer is a JSON.stringify replacer.
type JSONReplacer func(key string, value any) any

type seenKey struct {
	kind reflect.Kind
	ptr  uintptr
	n    int
}

// identityOf is the WeakSet identity of an object or array value; ok is false for a value with none (a primitive, an empty array).
func identityOf(v any) (seenKey, bool) {
	switch x := v.(type) {
	case *jsObject:
		if x != nil {
			return seenKey{reflect.Ptr, reflect.ValueOf(x).Pointer(), 0}, true
		}
	case map[string]any:
		if x != nil {
			return seenKey{reflect.Map, reflect.ValueOf(x).Pointer(), 0}, true
		}
	case []any:
		if len(x) > 0 {
			return seenKey{reflect.Slice, reflect.ValueOf(x).Pointer(), len(x)}, true
		}
	}
	return seenKey{}, false
}

func isNullish(v any) bool {
	if v == nil {
		return true
	}
	_, u := v.(undef)
	return u
}

// CreateJSONSafeReplacer builds a JSON.stringify replacer. Each call owns a fresh `seen` set, so a replacer must not be reused
// across stringify calls. As in the original, a repeated non-cyclic reference is also marked: seen entries are never released.
func CreateJSONSafeReplacer(transform JSONValueTransform) JSONReplacer {
	seen := map[seenKey]bool{}
	return func(key string, raw any) any {
		value := raw
		if transform != nil {
			value = transform(key, raw)
		}
		switch e := value.(type) {
		case *JSError:
			if e != nil {
				return errorRecord(e.Name, e.Message, e.Stack)
			}
		case error:
			return errorRecord("Error", e.Error(), nil)
		case *big.Int:
			if e != nil {
				return e.String()
			}
		}
		if id, ok := identityOf(value); ok {
			if seen[id] {
				return "[Circular]"
			}
			seen[id] = true
		}
		return value
	}
}

func errorRecord(name, message string, stack *string) *jsObject {
	o := newObject()
	o.set("name", name)
	o.set("message", message)
	if stack != nil {
		o.set("stack", *stack)
	} else {
		o.set("stack", undef{})
	}
	return o
}

// SafeJSONStringify serializes value to JSON, tolerating errors, bigints and cycles. ok is false where JSON.stringify returns
// undefined (a value JSON cannot represent).
func SafeJSONStringify(value any) (string, bool) {
	return jsonStringifyWith(value, CreateJSONSafeReplacer(nil))
}

// jsonStringifyWith is JSON.stringify(value, replacer) with no indent.
func jsonStringifyWith(value any, replacer JSONReplacer) (string, bool) {
	var b bytes.Buffer
	if !serializeProperty(&b, "", value, replacer) {
		return "", false
	}
	return b.String(), true
}

func isFunc(v any) bool { return v != nil && reflect.TypeOf(v).Kind() == reflect.Func }

// serializeProperty is SerializeJSONProperty: it writes the property's JSON, or nothing (false) where it is undefined.
func serializeProperty(b *bytes.Buffer, key string, value any, replacer JSONReplacer) bool {
	if replacer != nil {
		value = replacer(key, value)
	}
	switch x := value.(type) {
	case nil:
		b.WriteString("null")
	case undef:
		return false
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		writeWTFString(b, x)
	case float64:
		writeNumber(b, x)
	case float32:
		writeNumber(b, float64(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int8, int16, int32, int64:
		b.WriteString(strconv.FormatInt(reflect.ValueOf(x).Int(), 10))
	case uint, uint8, uint16, uint32, uint64:
		b.WriteString(strconv.FormatUint(reflect.ValueOf(x).Uint(), 10))
	case *big.Int, *JSError, error:
		// Only reachable with a replacer that leaves them be; JSON.stringify would throw on a bigint.
		b.WriteString("null")
	case *jsObject:
		if x == nil {
			b.WriteString("null")
			return true
		}
		writeObjectWith(b, x.orderView(), x.vals, replacer)
	case map[string]any:
		if x == nil {
			b.WriteString("null")
			return true
		}
		o := orderedRecord(x)
		writeObjectWith(b, o.order(), o.vals, replacer)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			key := ""
			if replacer != nil { // the index is the replacer's key: nothing else reads it
				key = strconv.Itoa(i)
			}
			if !serializeProperty(b, key, e, replacer) {
				b.WriteString("null")
			}
		}
		b.WriteByte(']')
	default:
		if isFunc(value) {
			return false
		}
		// An unknown Go value: read it through its JSON form.
		data, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Errorf("value of type %T is not JSON serializable: %w", value, err))
		}
		v, err := parseJSON(data)
		if err != nil {
			panic(err)
		}
		return serializeProperty(b, key, v, nil)
	}
	return true
}

func writeObjectWith(b *bytes.Buffer, keys []string, vals map[string]any, replacer JSONReplacer) {
	b.WriteByte('{')
	first := true
	for _, k := range keys {
		mark := b.Len()
		if !first {
			b.WriteByte(',')
		}
		writeWTFString(b, k)
		b.WriteByte(':')
		if !serializeProperty(b, k, vals[k], replacer) {
			b.Truncate(mark) // undefined: the property is left out
			continue
		}
		first = false
	}
	b.WriteByte('}')
}

func writeNumber(b *bytes.Buffer, f float64) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		b.WriteString("null")
		return
	}
	b.WriteString(jsNumber(f))
}

const hexDigits = "0123456789abcdef"

// wtfSurrogate reads s[i:] as the WTF-8 encoding of a lone surrogate, returning its code unit.
func wtfSurrogate(s string, i int) (unit rune, ok bool) {
	if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
		return 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), true
	}
	return 0, false
}

// writeWTFString quotes like JSON.stringify (well-formed): control characters and lone surrogates escape, nothing else does.
func writeWTFString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		if c := s[i]; c >= 0x20 && c < utf8.RuneSelf && c != '"' && c != '\\' { // a run of ASCII that needs no escape
			j := i + 1
			for j < len(s) && s[j] >= 0x20 && s[j] < utf8.RuneSelf && s[j] != '"' && s[j] != '\\' {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if u, ok := wtfSurrogate(s, i); ok {
			b.WriteString(`\u` + string(hexDigits[u>>12&15]) + string(hexDigits[u>>8&15]) + string(hexDigits[u>>4&15]) + string(hexDigits[u&15]))
			i += 3
			continue
		}
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
			b.WriteString(`\u00` + string(hexDigits[r>>4]) + string(hexDigits[r&15]))
		case r == utf8.RuneError && size == 1:
			b.WriteString(`\ufffd`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// utf16LenWTF is the JavaScript `.length` of s.
func utf16LenWTF(s string) int {
	i := 0
	for i < len(s) && s[i] < utf8.RuneSelf { // the ASCII prefix is one unit per byte
		i++
	}
	n := i
	for i < len(s) {
		if _, ok := wtfSurrogate(s, i); ok {
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// utf16Prefix is s.slice(0, n) for 0 <= n: when n falls inside an astral character the result ends in that character's high
// surrogate, held as WTF-8.
func utf16Prefix(s string, n int) string {
	units := 0
	for i := 0; i < len(s); {
		if units >= n {
			return s[:i]
		}
		if _, ok := wtfSurrogate(s, i); ok {
			units++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			if units+2 > n {
				hi := 0xD800 + (r-0x10000)>>10
				return s[:i] + string([]byte{0xED, byte(0x80 | (hi>>6)&0x3F), byte(0x80 | hi&0x3F)})
			}
			units += 2
		} else {
			units++
		}
		i += size
	}
	return s
}

// orderedRecord orders a Go map as an object: keys sorted.
func orderedRecord(m map[string]any) *jsObject {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	o := newObject()
	for _, k := range keys {
		o.set(k, m[k])
	}
	return o
}
