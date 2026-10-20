package pi_permission_system

import (
	"strings"
	"unicode"
)

// icuASCIIOrder is the root collation order of printable ASCII that String.prototype.localeCompare gives (Node, ICU, en-US):
// whitespace, punctuation (non-ignorable), digits, then letters with the lowercase form ahead of the uppercase one.
const icuASCIIOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789aAbBcCdDeEfFgGhHiIjJkKlLmMnNoOpPqQrRsStTuUvVwWxXyYzZ"

// latinBase maps the accented Latin-1 letters to the base letter ICU gives them the primary weight of (their accent is a
// secondary difference).
var latinBase = map[rune]rune{}

func init() {
	for base, accented := range map[rune]string{'a': "àáâãäåā", 'c': "çćč", 'e': "èéêëēę", 'i': "ìíîïī", 'n': "ñń", 'o': "òóôõöøō", 'u': "ùúûüū", 'y': "ýÿ"} {
		for _, r := range accented {
			latinBase[r] = base
		}
		for _, r := range strings.ToUpper(accented) {
			latinBase[r] = unicode.ToUpper(base)
		}
	}
}

type collationElement struct{ primary, accent, upper int }

// icuElements is the collation elements of s. Printable ASCII follows the table above; an accented Latin letter takes its base
// letter's primary weight and an accent weight; any other symbol sorts just ahead of the digits, any other letter after the
// Latin ones by code point. This is an approximation of ICU's root collation beyond Latin-1.
func icuElements(s string) []collationElement {
	letters := strings.Index(icuASCIIOrder, "aA")
	digits := strings.IndexByte(icuASCIIOrder, '0')
	weight := func(r rune) (int, bool) {
		i := strings.IndexRune(icuASCIIOrder, r)
		switch {
		case i < 0:
			return 0, false
		case i >= letters:
			return (letters + (i-letters)/2) * 2, true
		}
		return i * 2, true
	}
	var out []collationElement
	for _, r := range s {
		e := collationElement{}
		if unicode.IsUpper(r) {
			e.upper = 1
		}
		if base, ok := latinBase[r]; ok {
			w, _ := weight(base)
			e.primary, e.accent = w, int(unicode.ToLower(r)) // the same accent in either case: the case is a tertiary difference
		} else if w, ok := weight(r); ok {
			e.primary = w
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			e.primary = 4000 + int(r)
		} else {
			e.primary = digits*2 - 1
			e.accent = int(r)
		}
		out = append(out, e)
	}
	return out
}

// icuCompare compares like localeCompare: primary weights over the whole string first, then accents, then the case at the first
// difference.
func icuCompare(a, b string) int {
	ea, eb := icuElements(a), icuElements(b)
	for _, level := range []func(collationElement) int{
		func(e collationElement) int { return e.primary },
		func(e collationElement) int { return e.accent },
		func(e collationElement) int { return e.upper },
	} {
		for i := 0; i < len(ea) && i < len(eb); i++ {
			if x, y := level(ea[i]), level(eb[i]); x != y {
				return cmpInt(x, y)
			}
		}
		if len(ea) != len(eb) {
			return cmpInt(len(ea), len(eb))
		}
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
