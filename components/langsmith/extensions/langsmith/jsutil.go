package langsmith

// JavaScript string and value semantics the extension relies on (config.ts parseBoolean, index.ts template
// literals, `ctx.model?.name?.toLocaleLowerCase()`). jsTrim, jsTruthy, jsNumberString live in client_jsutil.go and the
// full lower-casing in jstext.go.

import (
	"strconv"
	"strings"
	"unicode"
)

// utf16Len is String.prototype.length.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// jsToString is the template-literal conversion `${v}` of a decoded JSON value (arrays join their elements with
// commas, null and undefined elements are empty).
func jsToString(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if !isNullish(e) {
				parts[i] = jsToString(e)
			}
		}
		return strings.Join(parts, ",")
	case int:
		return strconv.Itoa(x)
	}
	return jsString(v)
}

// jsLength is `v.length` for an array or a string (UTF-16 units); ok is false for any other value.
func jsLength(v any) (int, bool) {
	switch x := v.(type) {
	case []any:
		return len(x), true
	case string:
		return utf16Len(x), true
	}
	return 0, false
}

// jsToLowerCase is String.prototype.toLowerCase. toLocaleLowerCase differs only in a tr, az or lt default locale,
// which the port does not model (lane-ext.md).
func jsToLowerCase(s string) string { return string(jsUTF8Lower(nil, s)) }

// isCased is the Unicode Cased property: Lu, Ll, Lt, Other_Lowercase, Other_Uppercase.
func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// isCaseIgnorable is the Unicode Case_Ignorable property: Mn, Me, Cf, Lm, Sk, or Word_Break MidLetter,
// MidNumLet or Single_Quote.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
