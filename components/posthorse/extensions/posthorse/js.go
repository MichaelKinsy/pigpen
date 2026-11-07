package posthorse

// JavaScript string and number semantics the original relies on.
//
// Representation: a Go string is WTF-8, the convention of the SDK (extensions/sdk/json): well-formed
// text is UTF-8, and a lone UTF-16 surrogate unit is its three-byte form ED A0..BF 80..BF. Every
// JS-length quantity (budgets, excerpts, paging offsets) counts UTF-16 code units: a BMP character
// and a lone surrogate are one unit, an astral character two. A cut through an astral character
// leaves a lone surrogate, as `String.prototype.slice` does. Bytes that are not UTF-8 count as one
// unit each (U+FFFD); text read from a file goes through jsFromBytes first, which replaces them as
// `Buffer.toString("utf8")` does.
//
// A high and a low surrogate written next to each other as two three-byte forms (the result of
// concatenating two cut halves) count as the same two units as the canonical four-byte character;
// only byte-level equality and hashing differ, and jsUTF8Bytes canonicalises for hashing and files.

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// isASCII reports whether s holds only single-unit one-byte characters.
func isASCII(s string) bool {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		if s[i]|s[i+1]|s[i+2]|s[i+3]|s[i+4]|s[i+5]|s[i+6]|s[i+7] >= utf8.RuneSelf {
			return false
		}
	}
	for ; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isWTF8Surrogate reports whether s[i:] starts with a lone surrogate in its three-byte form.
func isWTF8Surrogate(s string, i int) bool {
	return i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80
}

// decodeUnit returns the byte size and the UTF-16 unit count of the character at s[i] (i < len(s)).
func decodeUnit(s string, i int) (size, units int) {
	c := s[i]
	if c < utf8.RuneSelf {
		return 1, 1
	}
	if isWTF8Surrogate(s, i) {
		return 3, 1
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	if r >= 0x10000 {
		return size, 2
	}
	return size, 1
}

// u16len is `s.length`.
func u16len(s string) int {
	if isASCII(s) {
		return len(s)
	}
	n := 0
	for i := 0; i < len(s); {
		size, units := decodeUnit(s, i)
		i += size
		n += units
	}
	return n
}

// byteOffset maps the UTF-16 index unit to a byte offset of s. units is the index the offset
// stands for: unit itself, or the length of s when unit is past the end. mid is true when unit
// falls between the two halves of an astral character; off is then the start of that character
// and units its index.
func byteOffset(s string, unit int) (off, units int, mid bool) {
	i, u := 0, 0
	for i < len(s) && u < unit {
		size, n := decodeUnit(s, i)
		if n == 2 && u+1 == unit {
			return i, u, true
		}
		i += size
		u += n
	}
	return i, u, false
}

// surrogateBytes is the three-byte form of one surrogate code unit.
func surrogateBytes(unit rune) [3]byte {
	return [3]byte{0xE0 | byte(unit>>12), 0x80 | byte(unit>>6)&0x3F, 0x80 | byte(unit)&0x3F}
}

// clampRelative is the relative-index rule of `slice`: negative counts from the end.
func clampRelative(i, length int) int {
	if i < 0 {
		i += length
		if i < 0 {
			return 0
		}
		return i
	}
	if i > length {
		return length
	}
	return i
}

// u16end stands for an omitted `end` argument of u16slice.
const u16end = math.MaxInt

// u16slice is `s.slice(start, end)` for integer arguments; pass u16end for `s.slice(start)`.
func u16slice(s string, start, end int) string {
	if isASCII(s) {
		from, to := clampRelative(start, len(s)), clampRelative(end, len(s))
		if from >= to {
			return ""
		}
		return s[from:to]
	}
	length := u16len(s)
	from, to := clampRelative(start, length), clampRelative(end, length)
	if from >= to {
		return ""
	}
	return u16range(s, from, to)
}

// u16sliceF is `s.slice(start, end)` for number arguments: NaN is 0, fractions truncate, infinities clamp.
func u16sliceF(s string, start, end float64) string {
	return u16slice(s, jsToInt(start), jsToInt(end))
}

// jsToInt is ToIntegerOrInfinity clamped to the int range: NaN is 0, fractions truncate toward zero.
func jsToInt(x float64) int {
	switch {
	case math.IsNaN(x):
		return 0
	case x >= math.MaxInt:
		return math.MaxInt
	case x <= math.MinInt:
		return math.MinInt
	}
	return int(x)
}

// u16range returns the units [from, to) of s, 0 <= from < to <= u16len(s).
func u16range(s string, from, to int) string {
	a, _, amid := byteOffset(s, from)
	b, _, bmid := byteOffset(s, to)
	if !amid && !bmid {
		return s[a:b]
	}
	out := make([]byte, 0, b-a+6)
	if amid {
		// from is the low half of the astral character at a.
		r, size := utf8.DecodeRuneInString(s[a:])
		sur := surrogateBytes(0xDC00 + (r-0x10000)&0x3FF)
		out = append(out, sur[:]...)
		a += size
	}
	out = append(out, s[a:b]...)
	if bmid {
		// to is between the halves of the astral character at b: the high half stays.
		r, _ := utf8.DecodeRuneInString(s[b:])
		sur := surrogateBytes(0xD800 + (r-0x10000)>>10)
		out = append(out, sur[:]...)
	}
	return string(out)
}

// u16substring is `s.substring(start, end)`: arguments clamp to [0, length] and swap when start > end.
func u16substring(s string, start, end int) string {
	length := math.MaxInt
	if isASCII(s) {
		length = len(s)
	} else {
		length = u16len(s)
	}
	from, to := min(max(start, 0), length), min(max(end, 0), length)
	if from > to {
		from, to = to, from
	}
	if from == to {
		return ""
	}
	if length == len(s) && isASCII(s) {
		return s[from:to]
	}
	return u16range(s, from, to)
}

// u16at is `s.charAt(i)`: the one-unit string at i, "" when i is out of range. A lone half of an
// astral character is returned as a lone surrogate.
func u16at(s string, i int) string {
	if i < 0 {
		return ""
	}
	if isASCII(s) {
		if i >= len(s) {
			return ""
		}
		return s[i : i+1]
	}
	off, _, mid := byteOffset(s, i)
	if off >= len(s) && !mid {
		return ""
	}
	return u16range(s, i, i+1)
}

// u16code is `s.charCodeAt(i)`; ok is false when i is out of range (NaN in JavaScript).
func u16code(s string, i int) (unit uint16, ok bool) {
	if i < 0 {
		return 0, false
	}
	off, _, mid := byteOffset(s, i)
	if off >= len(s) && !mid {
		return 0, false
	}
	if mid {
		r, _ := utf8.DecodeRuneInString(s[off:])
		return uint16(0xDC00 + (r-0x10000)&0x3FF), true
	}
	if isWTF8Surrogate(s, off) {
		return uint16(s[off]&0x0F)<<12 | uint16(s[off+1]&0x3F)<<6 | uint16(s[off+2]&0x3F), true
	}
	r, _ := utf8.DecodeRuneInString(s[off:])
	if r >= 0x10000 {
		return uint16(0xD800 + (r-0x10000)>>10), true
	}
	return uint16(r), true
}

// u16units is the UTF-16 code units of s.
func u16units(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		if isWTF8Surrogate(s, i) {
			units = append(units, uint16(s[i]&0x0F)<<12|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xD800+r>>10), uint16(0xDC00+r&0x3FF))
		} else {
			units = append(units, uint16(r))
		}
		i += size
	}
	return units
}

// u16string builds a string from UTF-16 code units: a high and a low surrogate in order are one
// astral character, an unpaired one keeps its three-byte form.
func u16string(units []uint16) string {
	var out strings.Builder
	out.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		switch {
		case u < 0xD800 || u > 0xDFFF:
			out.WriteRune(u)
		case u < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF:
			out.WriteRune(0x10000 + (u-0xD800)<<10 + rune(units[i+1]) - 0xDC00)
			i++
		default:
			sur := surrogateBytes(u)
			out.Write(sur[:])
		}
	}
	return out.String()
}

// hasSurrogateForm reports whether s holds a lone surrogate in its three-byte form.
func hasSurrogateForm(s string) bool {
	for i := range len(s) {
		if s[i] == 0xED && isWTF8Surrogate(s, i) {
			return true
		}
	}
	return false
}

// u16index is `s.indexOf(sub, from)`: the UTF-16 index of the first match at or after from, or -1.
// from clamps to [0, length].
func u16index(s, sub string, from int) int {
	from = max(from, 0)
	if isASCII(s) {
		from = min(from, len(s))
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return -1
		}
		return from + i
	}
	if sub == "" {
		// An empty needle matches at the clamped index, even between the halves of a pair.
		return min(from, u16len(s))
	}
	if hasSurrogateForm(sub) || hasSurrogateForm(s) {
		return unitIndex(u16units(s), u16units(sub), from)
	}
	off, unit, mid := byteOffset(s, from)
	if mid {
		// A well-formed needle cannot start between the halves of a character.
		_, size := utf8.DecodeRuneInString(s[off:])
		off += size
		unit += 2
	}
	i := strings.Index(s[off:], sub)
	if i < 0 {
		return -1
	}
	return unit + u16len(s[off:off+i])
}

// unitIndex is indexOf over code units.
func unitIndex(s, sub []uint16, from int) int {
	from = min(from, len(s))
	for i := from; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// u16lastIndex is `s.lastIndexOf(sub, from)` for from >= 0; pass u16end for no limit.
func u16lastIndex(s, sub string, from int) int {
	from = max(from, 0)
	if isASCII(s) {
		end := len(s)
		if from < len(s) {
			end = min(len(s), from+len(sub))
		}
		return strings.LastIndex(s[:end], sub)
	}
	units, needle := u16units(s), u16units(sub)
	for i := min(from, len(units)-len(needle)); i >= 0; i-- {
		match := true
		for j := range needle {
			if units[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// jsToLocaleInt is `n.toLocaleString("en-US")` for an integer: comma grouping.
func jsToLocaleInt(n int) string {
	if n >= 0 && n < 1000 {
		return strconv.Itoa(n)
	}
	digits := strconv.FormatInt(int64(n), 10)
	neg := digits[0] == '-'
	if neg {
		digits = digits[1:]
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3 + 1)
	if neg {
		out.WriteByte('-')
	}
	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	out.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		out.WriteByte(',')
		out.WriteString(digits[i : i+3])
	}
	return out.String()
}

// jsRound is `Math.round`: ties round toward +Infinity (-2.5 -> -2), and -0.4 is -0.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1<<52 {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && (x < 0 || math.Signbit(x)) {
		return math.Copysign(0, -1)
	}
	return r
}

// jsUndefined is the value JSON.stringify returns "undefined" for.
type jsUndefined struct{}

// jsUndef is the JavaScript `undefined`, for callers that must tell it from null (Go nil).
var jsUndef = jsUndefined{}

// jsStringify is `JSON.stringify(v)` for a value built from rec, []any, *sdk.OrderedObject, strings,
// float64/int numbers and bools: no HTML escaping, lone surrogates as \uXXXX, ordered objects in
// insertion order (a plain map is written in the SDK's deterministic key order: use an ordered
// object wherever the original's key order is observable).
func jsStringify(v any) (string, error) {
	b, err := json.MarshalStringify(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsWhitespace reports the characters `String.prototype.trim` removes (WhiteSpace and LineTerminator of ECMAScript).
func jsWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsTrim is `s.trim()`.
func jsTrim(s string) string { return strings.TrimFunc(s, jsWhitespace) }

// jsTrimStart is `s.trimStart()`.
func jsTrimStart(s string) string { return strings.TrimLeftFunc(s, jsWhitespace) }

// jsTrimEnd is `s.trimEnd()`.
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, jsWhitespace) }

// jsFromBytes is `buffer.toString("utf8")`: each maximal ill-formed subpart becomes one U+FFFD (WHATWG decoder).
func jsFromBytes(p []byte) string {
	if utf8.Valid(p) {
		return string(p)
	}
	return string(appendJSFromBytes(make([]byte, 0, len(p)+8), p))
}

// appendJSFromBytes appends jsFromBytes(p) to out.
func appendJSFromBytes(out, p []byte) []byte {
	if utf8.Valid(p) {
		return append(out, p...)
	}
	for i := 0; i < len(p); {
		b := p[i]
		if b < utf8.RuneSelf {
			out = append(out, b)
			i++
			continue
		}
		lo, hi, need := byte(0x80), byte(0xBF), 0
		switch {
		case b >= 0xC2 && b <= 0xDF:
			need = 1
		case b == 0xE0:
			need, lo = 2, 0xA0
		case b >= 0xE1 && b <= 0xEC, b == 0xEE, b == 0xEF:
			need = 2
		case b == 0xED:
			need, hi = 2, 0x9F
		case b == 0xF0:
			need, lo = 3, 0x90
		case b >= 0xF1 && b <= 0xF3:
			need = 3
		case b == 0xF4:
			need, hi = 3, 0x8F
		default:
			out = append(out, 0xEF, 0xBF, 0xBD)
			i++
			continue
		}
		j := i + 1
		ok := true
		for range need {
			if j >= len(p) || p[j] < lo || p[j] > hi {
				ok = false
				break
			}
			lo, hi = 0x80, 0xBF
			j++
		}
		if ok {
			out = append(out, p[i:j]...)
		} else {
			out = append(out, 0xEF, 0xBF, 0xBD)
		}
		i = j
	}
	return out
}

// jsUTF8Bytes is the bytes Node writes or hashes for a JS string: a lone surrogate is U+FFFD, and a
// high surrogate directly followed by a low one (two three-byte forms) is the one astral character.
func jsUTF8Bytes(s string) []byte {
	if !hasSurrogateForm(s) {
		return []byte(s)
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if !isWTF8Surrogate(s, i) {
			out = append(out, s[i])
			i++
			continue
		}
		hi := rune(s[i]&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
		if hi < 0xDC00 && isWTF8Surrogate(s, i+3) {
			lo := rune(s[i+3]&0x0F)<<12 | rune(s[i+4]&0x3F)<<6 | rune(s[i+5]&0x3F)
			if lo >= 0xDC00 {
				out = utf8.AppendRune(out, 0x10000+(hi-0xD800)<<10+lo-0xDC00)
				i += 6
				continue
			}
		}
		out = append(out, 0xEF, 0xBF, 0xBD)
		i += 3
	}
	return out
}

// jsCased reports the Unicode Cased property.
func jsCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// jsCaseIgnorable reports the Unicode Case_Ignorable property: Mn, Me, Cf, Lm, Sk and the
// Word_Break MidLetter/MidNumLet/Single_Quote characters.
func jsCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027, 0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// hasSpecialLowerCase reports whether s holds a character whose JavaScript lowercase differs
// from Go's simple mapping: U+0130, U+03A3 (Final_Sigma) or a lone surrogate (WTF-8 lead 0xED).
func hasSpecialLowerCase(s string) bool {
	for i := range len(s) {
		switch s[i] {
		case 0xED:
			return true
		case 0xC4:
			if i+1 < len(s) && s[i+1] == 0xB0 {
				return true
			}
		case 0xCE:
			if i+1 < len(s) && s[i+1] == 0xA3 {
				return true
			}
		}
	}
	return false
}

// jsToLowerCase is `s.toLowerCase()`: the full Unicode lowercase mapping, so U+0130 becomes
// "i" + U+0307, and a capital sigma at the end of a word becomes the final sigma. Lone surrogates
// pass through. Text without those characters takes the standard library's simple mapping.
func jsToLowerCase(s string) string {
	if isASCII(s) || !hasSpecialLowerCase(s) {
		// Outside U+0130, the capital sigma and lone surrogates the full mapping is the simple one.
		return strings.ToLower(s)
	}
	// A lone surrogate is held as its own code point (never cased, never ignorable).
	text := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		if isWTF8Surrogate(s, i) {
			text = append(text, rune(s[i]&0x0F)<<12|rune(s[i+1]&0x3F)<<6|rune(s[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		text = append(text, r)
		i += size
	}
	var out strings.Builder
	out.Grow(len(s) + 4)
	for i, r := range text {
		switch {
		case r >= 0xD800 && r <= 0xDFFF:
			sur := surrogateBytes(r)
			out.Write(sur[:])
		case r == 0x130:
			out.WriteString("i\u0307")
		case r == 0x3A3 && jsFinalSigma(text, i):
			out.WriteRune(0x3C2)
		default:
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

// jsFinalSigma is the Final_Sigma condition for the capital sigma at text[at]: a cased letter
// before it (skipping case-ignorable characters) and no cased letter after it.
func jsFinalSigma(text []rune, at int) bool {
	before := at - 1
	for before >= 0 && jsCaseIgnorable(text[before]) {
		before--
	}
	if before < 0 || !jsCased(text[before]) {
		return false
	}
	after := at + 1
	for after < len(text) && jsCaseIgnorable(text[after]) {
		after++
	}
	return after >= len(text) || !jsCased(text[after])
}
