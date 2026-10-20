package websearch

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// unmarshalAny is json.Unmarshal into an `any`: the generic value (map[string]any, []any, string,
// float64, bool, nil) or the decoder's own error. Provider responses are decoded this way and read
// back with jsonPath and the type assertions of the callers, so the value and the error text must
// not change. encoding/json decodes into an interface through reflection and allocates a value per
// element, which dominated the cost of a provider call (a Gemini search spent 78 percent of its
// allocated bytes there). fastParseJSON is a direct parser for the well-formed, common case; any
// input it does not accept outright (a syntax error, a duplicate object key, an invalid UTF-8
// byte or a lone surrogate that the standard decoder repairs, a number out of range, nesting
// beyond fastJSONMaxDepth) goes through encoding/json, so the result for those is the standard
// one by construction. TestFastJSONMatchesStandard compares the two on the recorded provider
// fixtures, a table of edge cases and a seeded random corpus.
func unmarshalAny(text string) (any, error) {
	if v, ok := fastParseJSON(text); ok {
		return v, nil
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	return v, nil
}

const fastJSONMaxDepth = 200

type fastJSON struct {
	s string
	i int
}

// fastParseJSON parses one JSON text into the generic value encoding/json produces, reporting false
// for anything outside the fast path.
func fastParseJSON(s string) (any, bool) {
	p := fastJSON{s: s}
	p.skipSpace()
	v, ok := p.value(0)
	if !ok {
		return nil, false
	}
	p.skipSpace()
	return v, p.i == len(p.s)
}

func (p *fastJSON) skipSpace() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *fastJSON) value(depth int) (any, bool) {
	if p.i >= len(p.s) || depth > fastJSONMaxDepth {
		return nil, false
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, ok := p.str()
		return s, ok
	case c == 't':
		return true, p.literal("true")
	case c == 'f':
		return false, p.literal("false")
	case c == 'n':
		return nil, p.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		f, ok := p.number()
		return f, ok
	}
	return nil, false
}

func (p *fastJSON) literal(word string) bool {
	if strings.HasPrefix(p.s[p.i:], word) {
		p.i += len(word)
		return true
	}
	return false
}

func (p *fastJSON) object(depth int) (any, bool) {
	p.i++
	m := map[string]any{}
	p.skipSpace()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return m, true
	}
	for {
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, false
		}
		key, ok := p.strOpt(true)
		if !ok {
			return nil, false
		}
		p.skipSpace()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, false
		}
		p.i++
		p.skipSpace()
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		n := len(m)
		m[key] = v
		if len(m) == n {
			// A repeated key: the standard decoder's rule for it is its own.
			return nil, false
		}
		p.skipSpace()
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
}

func (p *fastJSON) array(depth int) (any, bool) {
	p.i++
	a := make([]any, 0, 4)
	p.skipSpace()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return a, true
	}
	for {
		p.skipSpace()
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		a = append(a, v)
		p.skipSpace()
		if p.i >= len(p.s) {
			return nil, false
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return a, true
		default:
			return nil, false
		}
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *fastJSON) number() (float64, bool) {
	s, j := p.s, p.i
	if s[j] == '-' {
		j++
	}
	if j >= len(s) {
		return 0, false
	}
	switch {
	case s[j] == '0':
		j++
	case s[j] >= '1' && s[j] <= '9':
		for j < len(s) && isDigit(s[j]) {
			j++
		}
	default:
		return 0, false
	}
	if j < len(s) && s[j] == '.' {
		j++
		start := j
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == start {
			return 0, false
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		j++
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		start := j
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == start {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s[p.i:j], 64)
	if err != nil {
		return 0, false
	}
	p.i = j
	return f, true
}

// internedJSONKeys are object keys that provider responses repeat in every element; a key found
// here is shared instead of copied.
var internedJSONKeys = func() map[string]string {
	m := map[string]string{}
	for _, k := range []string{
		"type", "text", "title", "url", "uri", "id", "name", "description", "snippet", "content", "role",
		"message", "error", "code", "status", "model", "data", "index", "results", "items", "sources", "source",
		"output", "outputs", "annotations", "start_index", "end_index", "citations", "action", "query", "usage",
		"candidates", "parts", "groundingMetadata", "groundingChunks", "web", "finishReason", "promptFeedback",
		"blockReason", "search_results", "tool", "object", "choices", "caption", "source_website_url",
	} {
		m[k] = k
	}
	return m
}()

// str reads the string whose opening quote is at p.i. The common string (printable ASCII, no
// escape) is one copy of its bytes; the rest takes strSlow.
func (p *fastJSON) str() (string, bool) { return p.strOpt(false) }

// strOpt is str; with intern set the copy is replaced by the shared one for a known object key.
func (p *fastJSON) strOpt(intern bool) (string, bool) {
	s := p.s
	start := p.i + 1
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			p.i = i + 1
			if intern {
				if k, ok := internedJSONKeys[s[start:i]]; ok {
					return k, true
				}
			}
			return strings.Clone(s[start:i]), true
		}
		if c == '\\' || c < 0x20 || c >= utf8.RuneSelf {
			return p.strSlow(start, i)
		}
	}
	return "", false
}

// strSlow continues a string from index at, after the plain bytes s[start:at].
func (p *fastJSON) strSlow(start, at int) (string, bool) {
	s := p.s
	buf := make([]byte, 0, at-start+32)
	buf = append(buf, s[start:at]...)
	for i := at; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			p.i = i + 1
			return string(buf), true
		case c < 0x20:
			return "", false
		case c == '\\':
			if i+1 >= len(s) {
				return "", false
			}
			switch e := s[i+1]; e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				r, ok := hex4(s, i+2)
				if !ok {
					return "", false
				}
				i += 4
				if utf16.IsSurrogate(r) {
					// Only a high surrogate followed by an escaped low surrogate is a character.
					if r >= 0xDC00 || i+7 > len(s) || s[i+2] != '\\' || s[i+3] != 'u' {
						return "", false
					}
					lo, ok := hex4(s, i+4)
					if !ok || lo < 0xDC00 || lo > 0xDFFF {
						return "", false
					}
					r = utf16.DecodeRune(r, lo)
					i += 6
				}
				buf = utf8.AppendRune(buf, r)
			default:
				return "", false
			}
			i += 2
		case c < utf8.RuneSelf:
			buf = append(buf, c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return "", false
			}
			buf = append(buf, s[i:i+size]...)
			i += size
		}
	}
	return "", false
}

func hex4(s string, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[i : i+4]) {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}
