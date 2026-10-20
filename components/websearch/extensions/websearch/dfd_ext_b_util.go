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
	return jsEncodeURI(jsDecodeURI(extAttr(el, "href")))
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
			hi, lo := jsURIHex(b[i+1]), jsURIHex(b[i+2])
			if hi >= 0 && lo >= 0 {
				out = append(out, byte(hi<<4|lo))
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
