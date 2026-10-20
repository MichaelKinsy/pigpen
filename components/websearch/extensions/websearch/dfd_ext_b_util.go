package websearch

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Property reads on parsed JSON with JavaScript's semantics, for the extractors that walk page data with
// optional chaining. A parsed value is nil, bool, float64, string, []any or *jsObj; a missing property is nil.

// extGet is v?.key: nil for a missing key and for a value that has no such property.
func extGet(v any, key string) any {
	switch t := v.(type) {
	case *jsObj:
		r, _ := t.get(key)
		return r
	case []any:
		if key == "length" {
			return float64(len(t))
		}
		if i, ok := ldIntKey(key); ok && int(i) < len(t) {
			return t[i]
		}
	case string:
		if key == "length" {
			return float64(jsLen(t))
		}
	}
	return nil
}

// extGetNN is v.key: a TypeError when v is null or undefined.
func extGetNN(v any, key string) any {
	if v == nil {
		ldThrowType("Cannot read properties of null (reading '" + key + "')")
	}
	return extGet(v, key)
}

// extIdx is v?.[i].
func extIdx(v any, i int) any { return extGet(v, strconv.Itoa(i)) }

// extStrCall is the receiver of a String method (replace, startsWith, ...): a TypeError when v is not a string.
func extStrCall(v any, method string) string {
	s, ok := v.(string)
	if !ok {
		ldThrowType(jsType(v) + "." + method + " is not a function")
	}
	return s
}

// extArray is the array a `for...of` or .map walks: nil for a missing value; a string yields its characters, which
// no caller reads a property of, so it is an empty walk; anything else that is not an array is not iterable.
func extArray(v any, what string) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case string:
		return nil
	}
	ldThrowType(what + " is not iterable")
	return nil
}

// extHasLen is the truth of v?.length.
func extHasLen(v any) bool { return jsTruthy(extGet(v, "length")) }

// extAnchorHref is HTMLAnchorElement.href of linkedom: encodeURI(decodeURI(getAttribute('href') || ”)). A malformed
// percent escape throws the URIError of decodeURI.
func extAnchorHref(el *ldNode) string {
	return extEncodeURI(extDecodeURI(extAttr(el, "href")))
}

const extURIReserved = ";/?:@&=+$,#"

func extURIError() { panic(&jsThrow{name: "URIError", msg: "URI malformed"}) }

// extDecodeURI is decodeURI: percent escapes of UTF-8 are decoded, except those of the reserved characters.
func extDecodeURI(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	// escape reads the byte of the %XX at i.
	escape := func(i int) byte {
		if i+2 >= len(s) || s[i] != '%' {
			extURIError()
		}
		hi, ok1 := extHexVal(s[i+1])
		lo, ok2 := extHexVal(s[i+2])
		if !ok1 || !ok2 {
			extURIError()
		}
		return hi<<4 | lo
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		start := i
		first := escape(i)
		i += 3
		if first < 0x80 {
			if strings.IndexByte(extURIReserved, first) >= 0 {
				b.WriteString(s[start:i])
			} else {
				b.WriteByte(first)
			}
			continue
		}
		n := 0
		for first&(0x80>>n) != 0 {
			n++
		}
		if n == 1 || n > 4 {
			extURIError()
		}
		seq := []byte{first}
		for k := 1; k < n; k++ {
			cb := escape(i)
			if cb&0xC0 != 0x80 {
				extURIError()
			}
			seq = append(seq, cb)
			i += 3
		}
		r, size := utf8.DecodeRune(seq)
		if r == utf8.RuneError || size != len(seq) {
			extURIError()
		}
		b.WriteRune(r)
	}
	return b.String()
}

func extHexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// extEncodeURI is encodeURI: everything but letters, digits and ;,/?:@&=+$-_.!~*'()# is percent-encoded as UTF-8.
func extEncodeURI(s string) string {
	const keep = ";,/?:@&=+$-_.!~*'()#"
	clean := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || (c < 0x80 && strings.IndexByte(keep, c) >= 0)) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.IndexByte(keep, byte(r)) >= 0) {
			b.WriteByte(byte(r))
			continue
		}
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], r)
		for _, c := range buf[:n] {
			b.WriteByte('%')
			b.WriteByte(digits[c>>4])
			b.WriteByte(digits[c&15])
		}
	}
	return b.String()
}

// extKeys is Object.keys of a parsed object or array.
func extKeys(v any) []string {
	switch t := v.(type) {
	case *jsObj:
		return t.keys
	case []any:
		keys := make([]string, len(t))
		for i := range t {
			keys[i] = strconv.Itoa(i)
		}
		return keys
	}
	return nil
}

// extQueryParam is new URL(...).searchParams.get(name) for a URL's search ("?a=b&c=d"): the first pair of that name,
// '+' read as a space and percent escapes decoded as UTF-8 without throwing. ok is false when there is no such pair.
func extQueryParam(search, name string) (value string, ok bool) {
	search = strings.TrimPrefix(search, "?")
	if search == "" {
		return "", false
	}
	for _, pair := range strings.Split(search, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if extFormDecode(k) == name {
			return extFormDecode(v), true
		}
	}
	return "", false
}

func extFormDecode(s string) string {
	b := []byte(strings.ReplaceAll(s, "+", " "))
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) {
			hi, ok1 := extHexVal(b[i+1])
			lo, ok2 := extHexVal(b[i+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, b[i])
	}
	var res strings.Builder
	for len(out) > 0 {
		r, size := utf8.DecodeRune(out)
		res.WriteRune(r)
		out = out[size:]
	}
	return res.String()
}

// extPath is v?.a?.b?.c for the keys given.
func extPath(v any, keys ...string) any {
	for _, k := range keys {
		v = extGet(v, k)
	}
	return v
}
