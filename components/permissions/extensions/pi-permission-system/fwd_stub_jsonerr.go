// LANE-LOCAL COPY for the forwarding lane: the config lane (PermConfig) owns jsonerr.go, copied verbatim below so the forwarding
// readers log the message V8 gives a malformed file ("Expected property name or '}' in JSON at position 1"). Delete this file at
// integration.

package pi_permission_system

import (
	"strconv"
	"unicode/utf16"
)

// The message JSON.parse throws for malformed text, as V8 (Node 24) words it: config-loader.ts reports `error.message` of a failed
// JSON.parse in "Failed to read config at '<path>': <message>". Mirrors v8/src/json/json-parser.cc: ReportUnexpectedToken,
// LookUpErrorMessageForJsonToken and the explicit messages of ExpectNext/Expect. Positions count UTF-16 code units.

const (
	jsonMaxContext      = 10
	jsonMinContextLen   = jsonMaxContext*2 + 1
	jsonUnexpectedEOS   = "Unexpected end of JSON input"
	jsonEOSChar         = -1
	jsonTokenWhitespace = " \t\n\r"
)

type v8JSON struct {
	src []uint16
	pos int
	err string
}

// v8JSONError returns the JSON.parse error message for text, "" when text is valid JSON.
func v8JSONError(text string) string {
	p := &v8JSON{src: utf16.Encode([]rune(text))}
	p.skipWS()
	p.value()
	if p.err != "" {
		return p.err
	}
	p.skipWS()
	if p.pos < len(p.src) {
		p.atRaw("Unexpected non-whitespace character after JSON", p.pos)
	}
	return p.err
}

func (p *v8JSON) cur() int {
	if p.pos >= len(p.src) {
		return jsonEOSChar
	}
	return int(p.src[p.pos])
}

func (p *v8JSON) skipWS() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// at fails with "<what> in JSON at position N (line L column C)" for the cursor position.
func (p *v8JSON) at(what string) { p.atRaw(what+" in JSON", p.pos) }

// atRaw fails with "<what> at position N (line L column C)".
func (p *v8JSON) atRaw(what string, pos int) {
	if p.err != "" {
		return
	}
	line, lastBreak := 1, 0
	for i := 0; i < pos && i < len(p.src); i++ {
		c := p.src[i]
		if c == '\r' && i+1 < len(p.src) && p.src[i+1] == '\n' {
			i++
			line++
			lastBreak = i + 1
		} else if c == '\r' || c == '\n' {
			line++
			lastBreak = i + 1
		}
	}
	p.err = what + " at position " + strconv.Itoa(pos) + " (line " + strconv.Itoa(line) + " column " + strconv.Itoa(pos-lastBreak+1) + ")"
}

// unexpected is ReportUnexpectedToken without an explicit message.
func (p *v8JSON) unexpected() {
	if p.err != "" {
		return
	}
	c := p.cur()
	if c == jsonEOSChar {
		p.err = jsonUnexpectedEOS
		return
	}
	whole := string(utf16.Decode(p.src))
	switch whole {
	case "NaN", "Infinity", "undefined", "[object Object]":
		p.err = `"` + whole + `" is not valid JSON`
		return
	}
	tok := string(utf16.Decode([]uint16{uint16(c)}))
	n, pos := len(p.src), p.pos
	sub := func(from, to int) string { return string(utf16.Decode(p.src[from:to])) }
	head := "Unexpected token '" + tok + "', "
	switch {
	case n < jsonMinContextLen:
		p.err = head + `"` + whole + `" is not valid JSON`
	case pos < jsonMaxContext:
		p.err = head + `"` + sub(0, pos+jsonMaxContext) + `"... is not valid JSON`
	case pos < n-jsonMaxContext:
		p.err = head + `..."` + sub(pos-jsonMaxContext, pos+jsonMaxContext) + `"... is not valid JSON`
	default:
		p.err = head + `..."` + sub(pos-jsonMaxContext, n) + `" is not valid JSON`
	}
}

func (p *v8JSON) value() {
	if p.err != "" {
		return
	}
	switch c := p.cur(); {
	case c == '"':
		p.str()
	case c == '-' || (c >= '0' && c <= '9'):
		p.number()
	case c == '{':
		p.object()
	case c == '[':
		p.array()
	case c == 't':
		p.literal("true")
	case c == 'f':
		p.literal("false")
	case c == 'n':
		p.literal("null")
	default:
		p.unexpected()
	}
}

func (p *v8JSON) literal(word string) {
	for i := 0; i < len(word); i++ {
		if p.pos+i >= len(p.src) || p.src[p.pos+i] != uint16(word[i]) {
			p.pos += i
			p.unexpected()
			return
		}
	}
	p.pos += len(word)
}

func (p *v8JSON) digits() int {
	n := 0
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
		n++
	}
	return n
}

func (p *v8JSON) number() {
	if p.cur() == '-' {
		p.pos++
		if c := p.cur(); c < '0' || c > '9' {
			p.at("No number after minus sign")
			return
		}
	}
	if p.cur() == '0' {
		p.pos++
		if c := p.cur(); c >= '0' && c <= '9' {
			p.at("Unexpected number")
			return
		}
	} else {
		p.digits()
	}
	if p.cur() == '.' {
		p.pos++
		if p.digits() == 0 {
			p.at("Unterminated fractional number")
			return
		}
	}
	if c := p.cur(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.cur(); c == '+' || c == '-' {
			p.pos++
		}
		if p.digits() == 0 {
			p.at("Exponent part is missing a number")
		}
	}
}

func (p *v8JSON) str() {
	p.pos++ // opening quote
	for {
		c := p.cur()
		switch {
		case c == jsonEOSChar:
			p.at("Unterminated string")
			return
		case c == '"':
			p.pos++
			return
		case c < 0x20:
			p.at("Bad control character in string literal")
			return
		case c == '\\':
			p.pos++
			e := p.cur()
			switch e {
			case jsonEOSChar:
				p.err = jsonUnexpectedEOS
				return
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p.pos++
			case 'u':
				p.pos++
				for i := 0; i < 4; i++ {
					h := p.cur()
					if !(h >= '0' && h <= '9' || h >= 'a' && h <= 'f' || h >= 'A' && h <= 'F') {
						p.at("Bad Unicode escape")
						return
					}
					p.pos++
				}
			default:
				p.at("Bad escaped character")
				return
			}
		default:
			p.pos++
		}
	}
}

func (p *v8JSON) object() {
	p.pos++
	p.skipWS()
	if p.cur() == '}' {
		p.pos++
		return
	}
	first := true
	for {
		if p.cur() != '"' {
			if first {
				p.at("Expected property name or '}'")
			} else {
				p.at("Expected double-quoted property name")
			}
			return
		}
		first = false
		p.str()
		if p.err != "" {
			return
		}
		p.skipWS()
		if p.cur() != ':' {
			p.at("Expected ':' after property name")
			return
		}
		p.pos++
		p.skipWS()
		p.value()
		if p.err != "" {
			return
		}
		p.skipWS()
		switch p.cur() {
		case ',':
			p.pos++
			p.skipWS()
		case '}':
			p.pos++
			return
		default:
			p.at("Expected ',' or '}' after property value")
			return
		}
	}
}

func (p *v8JSON) array() {
	p.pos++
	p.skipWS()
	if p.cur() == ']' {
		p.pos++
		return
	}
	for {
		p.value()
		if p.err != "" {
			return
		}
		p.skipWS()
		switch p.cur() {
		case ',':
			p.pos++
			p.skipWS()
		case ']':
			p.pos++
			return
		default:
			p.at("Expected ',' or ']' after array element")
			return
		}
	}
}
