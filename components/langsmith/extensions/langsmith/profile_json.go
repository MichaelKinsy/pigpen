package langsmith

// JSON, UTF-8 and string helpers the profile lane needs with exact JavaScript semantics: `JSON.parse` that keeps
// key order (into an *omap), `JSON.stringify(v, null, 2)`, the WHATWG UTF-8 decoder `fs.readFileSync(p, "utf-8")` and
// `response.json()` use, `String.prototype.trim`, `ToString` and the `URLSearchParams` serializer.

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// profileJSONMaxDepth bounds nesting so a hostile document cannot exhaust the stack (V8 parses iteratively and
// would only fail in JSON.stringify; a document this deep is not a configuration file).
const profileJSONMaxDepth = 5000

// decodeUTF8Replacing is the WHATWG "UTF-8 decode without BOM" (TextDecoder / Buffer.toString("utf8")): every maximal
// invalid subpart becomes one U+FFFD. It returns string(b) without a pass when b is valid.
func decodeUTF8Replacing(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]byte, 0, len(b)+8)
	var cp rune
	needed, seen := 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				out = append(out, c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0xF)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x7)
			default:
				out = append(out, "\uFFFD"...)
			}
			i++
			continue
		}
		if c < lower || c > upper {
			cp, needed, seen = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out = append(out, "\uFFFD"...)
			continue // the byte is processed again as a start byte
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		i++
		if seen == needed {
			out = utf8.AppendRune(out, cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		out = append(out, "\uFFFD"...)
	}
	return string(out)
}

// pfIsSpace is the ECMAScript WhiteSpace and LineTerminator set `String.prototype.trim` strips.
func pfIsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// pfTrim is `s.trim()`.
func pfTrim(s string) string { return strings.TrimFunc(s, pfIsSpace) }

// pfTruthy is ToBoolean for the decoded-JSON value kinds.
func pfTruthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedT:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// pfToString is `String(v)` for the decoded-JSON value kinds.
func pfToString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undefinedT:
		return "undefined"
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	case float64:
		b, _ := json.AppendStringify(nil, x) // Number::toString for a finite value
		if string(b) == "null" {
			if math.IsNaN(x) {
				return "NaN"
			}
			if x > 0 {
				return "Infinity"
			}
			return "-Infinity"
		}
		return string(b)
	case *omap:
		return "[object Object]"
	case []any:
		var sb strings.Builder
		for i, e := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			if e != nil && !isUndef(e) {
				sb.WriteString(pfToString(e))
			}
		}
		return sb.String()
	}
	return ""
}

// formURLEncode is the application/x-www-form-urlencoded serializer URLSearchParams uses: unreserved `*-._` and
// ASCII alphanumerics stay, a space is `+`, every other byte (of the USV string) is %XX.
func formURLEncode(dst []byte, s string) []byte {
	const hex = "0123456789ABCDEF"
	s = jsValidUTF8(s)
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			dst = append(dst, c)
		case c == ' ':
			dst = append(dst, '+')
		default:
			dst = append(dst, '%', hex[c>>4], hex[c&15])
		}
	}
	return dst
}

// parseJSONOrdered is `JSON.parse(text)`: objects become *omap (insertion order, duplicate keys keep the first
// position and the last value), arrays []any, numbers float64, strings Go strings with lone surrogates as WTF-8.
// ok is false where JSON.parse throws.
func parseJSONOrdered(text string) (v any, ok bool) {
	p := jsonParser{s: text}
	p.skipWS()
	v, ok = p.value(0)
	if !ok {
		return nil, false
	}
	p.skipWS()
	if p.i != len(p.s) {
		return nil, false
	}
	return v, true
}

type jsonParser struct {
	s string
	i int
}

func (p *jsonParser) skipWS() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jsonParser) lit(word string) bool {
	if strings.HasPrefix(p.s[p.i:], word) {
		p.i += len(word)
		return true
	}
	return false
}

func (p *jsonParser) value(depth int) (any, bool) {
	if p.i >= len(p.s) || depth > profileJSONMaxDepth {
		return nil, false
	}
	switch c := p.s[p.i]; {
	case c == '{':
		p.i++
		m := newOmap()
		p.skipWS()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return m, true
		}
		for {
			p.skipWS()
			if p.i >= len(p.s) || p.s[p.i] != '"' {
				return nil, false
			}
			k, ok := p.str()
			if !ok {
				return nil, false
			}
			p.skipWS()
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, false
			}
			p.i++
			p.skipWS()
			val, ok := p.value(depth + 1)
			if !ok {
				return nil, false
			}
			m.set(k, val)
			p.skipWS()
			if p.i >= len(p.s) {
				return nil, false
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case '}':
				p.i++
				return m, true
			default:
				return nil, false
			}
		}
	case c == '[':
		p.i++
		arr := []any{}
		p.skipWS()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return arr, true
		}
		for {
			p.skipWS()
			val, ok := p.value(depth + 1)
			if !ok {
				return nil, false
			}
			arr = append(arr, val)
			p.skipWS()
			if p.i >= len(p.s) {
				return nil, false
			}
			switch p.s[p.i] {
			case ',':
				p.i++
			case ']':
				p.i++
				return arr, true
			default:
				return nil, false
			}
		}
	case c == '"':
		s, ok := p.str()
		return s, ok
	case c == 't':
		return true, p.lit("true")
	case c == 'f':
		return false, p.lit("false")
	case c == 'n':
		return nil, p.lit("null")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.num()
	}
	return nil, false
}

func (p *jsonParser) num() (any, bool) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) {
		return nil, false
	}
	switch {
	case p.s[p.i] == '0':
		p.i++
	case p.s[p.i] >= '1' && p.s[p.i] <= '9':
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	default:
		return nil, false
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		d := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		if p.i == d {
			return nil, false
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		d := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		if p.i == d {
			return nil, false
		}
	}
	f, _ := strconv.ParseFloat(p.s[start:p.i], 64) // out of range gives ±Inf like JS
	return f, true
}

func pfHexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func (p *jsonParser) hex4(at int) (rune, bool) {
	if at+4 > len(p.s) {
		return 0, false
	}
	var r rune
	for k := range 4 {
		h := pfHexVal(p.s[at+k])
		if h < 0 {
			return 0, false
		}
		r = r<<4 | rune(h)
	}
	return r, true
}

// appendWTF8 appends the UTF-8 form of r, writing a lone surrogate as its three-byte WTF-8 form.
func appendWTF8(dst []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
	return utf8.AppendRune(dst, r)
}

// str parses a string literal at p.i (which is `"`).
func (p *jsonParser) str() (string, bool) {
	p.i++
	start := p.i
	// Fast path: no escape.
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			s := p.s[start:p.i]
			p.i++
			return s, true
		}
		if c < 0x20 {
			return "", false
		}
		if c == '\\' {
			break
		}
		p.i++
	}
	if p.i >= len(p.s) {
		return "", false
	}
	buf := append(make([]byte, 0, p.i-start+16), p.s[start:p.i]...)
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return string(buf), true
		case c < 0x20:
			return "", false
		case c != '\\':
			buf = append(buf, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.s) {
			return "", false
		}
		switch e := p.s[p.i]; e {
		case '"', '\\', '/':
			buf = append(buf, e)
			p.i++
		case 'b':
			buf = append(buf, '\b')
			p.i++
		case 'f':
			buf = append(buf, '\f')
			p.i++
		case 'n':
			buf = append(buf, '\n')
			p.i++
		case 'r':
			buf = append(buf, '\r')
			p.i++
		case 't':
			buf = append(buf, '\t')
			p.i++
		case 'u':
			r, ok := p.hex4(p.i + 1)
			if !ok {
				return "", false
			}
			p.i += 5
			if r >= 0xD800 && r <= 0xDBFF && p.i+1 < len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
				if lo, ok := p.hex4(p.i + 2); ok && lo >= 0xDC00 && lo <= 0xDFFF {
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					p.i += 6
				}
			}
			buf = appendWTF8(buf, r)
		default:
			return "", false
		}
	}
	return "", false
}

// stringifyIndent2 is `JSON.stringify(v, null, 2)` for the values parseJSONOrdered produces and the profile code
// stores into them (nil, bool, float64, string, []any, *omap).
func stringifyIndent2(dst []byte, v any) ([]byte, error) {
	return appendIndented(dst, v, 0)
}

func appendIndent(dst []byte, depth int) []byte {
	dst = append(dst, '\n')
	for range depth {
		dst = append(dst, ' ', ' ')
	}
	return dst
}

func appendIndented(dst []byte, v any, depth int) ([]byte, error) {
	switch x := v.(type) {
	case *omap:
		if x == nil {
			return append(dst, "null"...), nil
		}
		wrote := false
		dst = append(dst, '{')
		for _, k := range x.ownKeys() {
			val := x.vals[k]
			if isUndef(val) {
				continue
			}
			if wrote {
				dst = append(dst, ',')
			}
			wrote = true
			dst = appendIndent(dst, depth+1)
			dst = json.AppendStringifyString(dst, k)
			dst = append(dst, ':', ' ')
			var err error
			if dst, err = appendIndented(dst, val, depth+1); err != nil {
				return dst, err
			}
		}
		if wrote {
			dst = appendIndent(dst, depth)
		}
		return append(dst, '}'), nil
	case []any:
		if len(x) == 0 {
			return append(dst, '[', ']'), nil
		}
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendIndent(dst, depth+1)
			var err error
			if dst, err = appendIndented(dst, e, depth+1); err != nil {
				return dst, err
			}
		}
		dst = appendIndent(dst, depth)
		return append(dst, ']'), nil
	}
	return appendValue(dst, v)
}
