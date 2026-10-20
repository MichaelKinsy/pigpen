package pi_subagents

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// jsJSONParse is JSON.parse with V8's error messages (Node 24): the model sees them in validation errors such as
// "acceptance JSON string must encode a valid acceptance object: <message>". It returns the decoded value (objects as
// *jsObject) or the message.
func jsJSONParse(text string) (any, string) {
	p := &jsonScan{src: utf16.Encode([]rune(text))}
	p.skipWS()
	v, ok := p.value()
	if !ok {
		return nil, p.msg
	}
	p.skipWS()
	if p.pos < len(p.src) {
		p.unexpected(p.pos, "Unexpected non-whitespace character after JSON")
		return nil, p.msg
	}
	return v, ""
}

type jsonScan struct {
	src []uint16
	pos int
	msg string
}

func (p *jsonScan) skipWS() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonScan) at(pos int) string {
	line, col := 1, 1
	for i := 0; i < pos && i < len(p.src); i++ {
		if p.src[i] == '\n' {
			line++
			col = 1
		} else if p.src[i] == '\r' {
			if i+1 < len(p.src) && p.src[i+1] == '\n' {
				continue
			}
			line++
			col = 1
		} else {
			col++
		}
	}
	return fmt.Sprintf("at position %d (line %d column %d)", pos, line, col)
}

func (p *jsonScan) withPos(what string, pos int) {
	if strings.HasSuffix(what, "after JSON") {
		p.msg = what + " " + p.at(pos)
		return
	}
	p.msg = what + " in JSON " + p.at(pos)
}

// unexpected is V8's ReportUnexpectedToken: the token class picks the message; specific is the call site's message for any
// other token (empty: the generic "Unexpected token" text).
func (p *jsonScan) unexpected(pos int, specific string) {
	if specific != "" {
		p.withPos(specific, pos)
		return
	}
	if pos >= len(p.src) {
		p.msg = "Unexpected end of JSON input"
		return
	}
	c := p.src[pos]
	switch {
	case c == '"':
		p.withPos("Unexpected string", pos)
		return
	case c == '-' || (c >= '0' && c <= '9'):
		p.withPos("Unexpected number", pos)
		return
	}
	whole := string(utf16.Decode(p.src))
	if whole == "NaN" || whole == "Infinity" || whole == "undefined" {
		p.msg = strconv.Quote(whole) + " is not valid JSON"
		return
	}
	tok := string(utf16.Decode([]uint16{c}))
	const ctx = 10
	n := len(p.src)
	if n >= 2*ctx+1 {
		start, end := 0, n
		if pos > ctx {
			start = pos - ctx
		}
		if pos+ctx < n {
			end = pos + ctx
		}
		sub := string(utf16.Decode(p.src[start:end]))
		switch {
		case start == 0:
			p.msg = fmt.Sprintf("Unexpected token '%s', \"%s\"... is not valid JSON", tok, sub)
		case end == n:
			p.msg = fmt.Sprintf("Unexpected token '%s', ...\"%s\" is not valid JSON", tok, sub)
		default:
			p.msg = fmt.Sprintf("Unexpected token '%s', ...\"%s\"... is not valid JSON", tok, sub)
		}
		return
	}
	p.msg = fmt.Sprintf("Unexpected token '%s', \"%s\" is not valid JSON", tok, whole)
}

func (p *jsonScan) value() (any, bool) {
	if p.pos >= len(p.src) {
		p.unexpected(p.pos, "")
		return nil, false
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		s, ok := p.str()
		return s, ok
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'n':
		return p.literal("null", nil)
	}
	p.unexpected(p.pos, "")
	return nil, false
}

func (p *jsonScan) literal(word string, v any) (any, bool) {
	for i := 0; i < len(word); i++ {
		if p.pos+i >= len(p.src) || p.src[p.pos+i] != uint16(word[i]) {
			p.unexpected(p.pos+i, "")
			return nil, false
		}
	}
	p.pos += len(word)
	return v, true
}

func isDigit(c uint16) bool { return c >= '0' && c <= '9' }

func (p *jsonScan) number() (any, bool) {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
		if p.pos >= len(p.src) || !isDigit(p.src[p.pos]) {
			p.withPos("No number after minus sign", p.pos)
			return nil, false
		}
	}
	if p.src[p.pos] == '0' {
		p.pos++
		if p.pos < len(p.src) && isDigit(p.src[p.pos]) {
			p.unexpected(p.pos, "")
			return nil, false
		}
	} else {
		for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
			p.pos++
		}
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.src) || !isDigit(p.src[p.pos]) {
			p.withPos("Unterminated fractional number", p.pos)
			return nil, false
		}
		for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
			p.pos++
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.src) || !isDigit(p.src[p.pos]) {
			p.withPos("Exponent part is missing a number", p.pos)
			return nil, false
		}
		for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
			p.pos++
		}
	}
	f, _ := strconv.ParseFloat(string(utf16.Decode(p.src[start:p.pos])), 64)
	return f, true
}

func (p *jsonScan) str() (string, bool) {
	p.pos++ // opening quote
	var b []uint16
	for {
		if p.pos >= len(p.src) {
			p.withPos("Unterminated string", p.pos)
			return "", false
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return string(utf16.Decode(b)), true
		case c < 0x20:
			p.withPos("Bad control character in string literal", p.pos)
			return "", false
		case c == '\\':
			p.pos++
			if p.pos >= len(p.src) {
				p.withPos("Unterminated string", p.pos)
				return "", false
			}
			e := p.src[p.pos]
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				var v uint16
				for i := 1; i <= 4; i++ {
					if p.pos+i >= len(p.src) {
						p.withPos("Bad Unicode escape", p.pos+i)
						return "", false
					}
					h := p.src[p.pos+i]
					var d uint16
					switch {
					case h >= '0' && h <= '9':
						d = h - '0'
					case h >= 'a' && h <= 'f':
						d = h - 'a' + 10
					case h >= 'A' && h <= 'F':
						d = h - 'A' + 10
					default:
						p.withPos("Bad Unicode escape", p.pos+i)
						return "", false
					}
					v = v<<4 | d
				}
				p.pos += 4
				b = append(b, v)
			default:
				p.withPos("Bad escaped character", p.pos)
				return "", false
			}
			p.pos++
		default:
			b = append(b, c)
			p.pos++
		}
	}
}

func (p *jsonScan) array() (any, bool) {
	p.pos++
	out := []any{}
	p.skipWS()
	if p.pos < len(p.src) && p.src[p.pos] == ']' {
		p.pos++
		return out, true
	}
	for {
		p.skipWS()
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		out = append(out, v)
		p.skipWS()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.src) && p.src[p.pos] == ']' {
			p.pos++
			return out, true
		}
		p.unexpected(p.pos, "Expected ',' or ']' after array element")
		return nil, false
	}
}

func (p *jsonScan) object() (any, bool) {
	p.pos++
	o := newObject()
	p.skipWS()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		p.pos++
		return o, true
	}
	specific := "Expected property name or '}'"
	for {
		p.skipWS()
		if p.pos >= len(p.src) || p.src[p.pos] != '"' {
			p.unexpected(p.pos, specific)
			return nil, false
		}
		key, ok := p.str()
		if !ok {
			return nil, false
		}
		p.skipWS()
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			p.unexpected(p.pos, "Expected ':' after property name")
			return nil, false
		}
		p.pos++
		p.skipWS()
		v, ok := p.value()
		if !ok {
			return nil, false
		}
		o.set(key, v)
		p.skipWS()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
			specific = "Expected double-quoted property name"
			continue
		}
		if p.pos < len(p.src) && p.src[p.pos] == '}' {
			p.pos++
			return o, true
		}
		p.unexpected(p.pos, "Expected ',' or '}' after property value")
		return nil, false
	}
}
