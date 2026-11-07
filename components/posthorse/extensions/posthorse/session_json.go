package posthorse

// JSON.parse for session files (session-files.ts:190 `JSON.parse(line)`, 325 the header line) and the
// normalization of an entry line (`parseSessionLine`, session-files.ts:211-241), in one pass.
//
// The SDK decoder is not used here: it validates every line before decoding it, refuses nesting
// deeper than 10,000 (V8's JSON.parse has no such limit; this reader allows 2^20) and builds every
// key of an entry, including the ones the normalization then drops. The grammar is JSON.parse's:
// whitespace is space, tab, LF and CR; no leading zeros, trailing commas or control characters in
// strings; a number out of range reads as an infinity; \u escapes combine a surrogate pair and keep
// a lone surrogate as its three-byte WTF-8 form (the SDK's convention for Go strings).
//
// The strings of a parsed value are substrings of the text, which is copied once per line: a line is
// one allocation, not one per string.

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxJSONDepth bounds the recursion of the reader; deeper input is refused (V8 is limited by memory).
const maxJSONDepth = 1 << 20

type jsonReader struct {
	s string
	i int
}

func (r *jsonReader) skipSpace() {
	for r.i < len(r.s) {
		switch r.s[r.i] {
		case ' ', '\t', '\n', '\r':
			r.i++
		default:
			return
		}
	}
}

// jsonLoad64 is the eight bytes of s at i as a little-endian word (i+8 <= len(s)).
func jsonLoad64(s string, i int) uint64 {
	return uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
		uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
}

// jsonSpecialWord reports whether one of the eight bytes of x is a quote, a backslash or below
// 0x20 (the bytes that end the plain run of a string). Exact as a yes/no answer.
func jsonSpecialWord(x uint64) bool {
	const (
		ones = 0x0101010101010101
		high = 0x8080808080808080
	)
	control := (x - 0x2020202020202020) & ^x & high
	q := x ^ 0x2222222222222222
	quote := (q - ones) & ^q & high
	b := x ^ 0x5c5c5c5c5c5c5c5c
	slash := (b - ones) & ^b & high
	return control|quote|slash != 0
}

// scanString finds the end of the string whose first content byte is at i: the index of the closing
// quote, and whether a backslash occurs. ok is false for an unterminated string or a control
// character.
func scanString(s string, i int) (end int, escaped, ok bool) {
	n := len(s)
	for {
		for i+8 <= n && !jsonSpecialWord(jsonLoad64(s, i)) {
			i += 8
		}
		if i >= n {
			return 0, false, false
		}
		switch c := s[i]; {
		case c == '"':
			return i, escaped, true
		case c == '\\':
			escaped = true
			i += 2
		case c < 0x20:
			return 0, false, false
		default:
			i++
		}
	}
}

func jsonHex4(s string) (rune, bool) {
	if len(s) < 4 {
		return 0, false
	}
	var v rune
	for k := 0; k < 4; k++ {
		c := s[k]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// unescapeJSON decodes the escapes of the content of a string; false for an invalid escape.
func unescapeJSON(raw string) (string, bool) {
	b := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			j := i + 1
			for j < len(raw) && raw[j] != '\\' {
				j++
			}
			b = append(b, raw[i:j]...)
			i = j
			continue
		}
		i++
		if i >= len(raw) {
			return "", false
		}
		switch raw[i] {
		case '"', '\\', '/':
			b = append(b, raw[i])
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
			r, ok := jsonHex4(raw[i+1:])
			if !ok {
				return "", false
			}
			i += 4
			switch {
			case r >= 0xD800 && r < 0xDC00:
				// A high surrogate combines with a \u low surrogate right after it.
				if i+6 < len(raw)+0 && raw[i+1] == '\\' && raw[i+2] == 'u' {
					if low, ok := jsonHex4(raw[i+3:]); ok && low >= 0xDC00 && low < 0xE000 {
						b = utf8.AppendRune(b, 0x10000+(r-0xD800)<<10+(low-0xDC00))
						i += 6
						break
					}
				}
				sur := surrogateBytes(r)
				b = append(b, sur[:]...)
			case r >= 0xDC00 && r < 0xE000:
				sur := surrogateBytes(r)
				b = append(b, sur[:]...)
			default:
				b = utf8.AppendRune(b, r)
			}
		default:
			return "", false
		}
		i++
	}
	return string(b), true
}

// readString parses the string at r.i (which holds a quote).
func (r *jsonReader) readString() (string, bool) {
	start := r.i + 1
	end, escaped, ok := scanString(r.s, start)
	if !ok {
		return "", false
	}
	r.i = end + 1
	if !escaped {
		return r.s[start:end], true
	}
	return unescapeJSON(r.s[start:end])
}

// skipString validates and skips the string at r.i without keeping it.
func (r *jsonReader) skipString() bool {
	start := r.i + 1
	end, escaped, ok := scanString(r.s, start)
	if !ok {
		return false
	}
	r.i = end + 1
	if escaped {
		_, ok = unescapeJSON(r.s[start:end])
	}
	return ok
}

// scanNumber returns the end of the JSON number at r.i; false when the grammar fails.
func (r *jsonReader) scanNumber() (end int, integer bool, ok bool) {
	s, i := r.s, r.i
	n := len(s)
	if i < n && s[i] == '-' {
		i++
	}
	if i >= n {
		return 0, false, false
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return 0, false, false
	}
	integer = true
	if i < n && s[i] == '.' {
		integer = false
		i++
		d := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == d {
			return 0, false, false
		}
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		integer = false
		i++
		if i < n && (s[i] == '+' || s[i] == '-') {
			i++
		}
		d := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == d {
			return 0, false, false
		}
	}
	return i, integer, true
}

func (r *jsonReader) readNumber() (float64, bool) {
	end, integer, ok := r.scanNumber()
	if !ok {
		return 0, false
	}
	text := r.s[r.i:end]
	r.i = end
	if integer && len(text) <= 15 {
		// Up to 15 digits and a sign are exact in a float64.
		neg := text[0] == '-'
		var v int64
		for k := range len(text) {
			if c := text[k]; c != '-' {
				v = v*10 + int64(c-'0')
			}
		}
		if neg {
			if v == 0 {
				return negativeZero, true
			}
			return float64(-v), true
		}
		return float64(v), true
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil && !isRangeError(err) {
		return 0, false
	}
	return f, true // an out-of-range number is an infinity, as in JSON.parse
}

var negativeZero = func() float64 { z := 0.0; return -z }()

func isRangeError(err error) bool {
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// literal consumes word at r.i.
func (r *jsonReader) literal(word string) bool {
	if strings.HasPrefix(r.s[r.i:], word) {
		r.i += len(word)
		return true
	}
	return false
}

// value parses any JSON value.
func (r *jsonReader) value(depth int) (any, bool) {
	if r.i >= len(r.s) || depth > maxJSONDepth {
		return nil, false
	}
	switch c := r.s[r.i]; {
	case c == '"':
		s, ok := r.readString()
		return s, ok
	case c == '{':
		return r.object(depth)
	case c == '[':
		return r.array(depth)
	case c == '-' || c >= '0' && c <= '9':
		f, ok := r.readNumber()
		return f, ok
	case c == 't':
		return true, r.literal("true")
	case c == 'f':
		return false, r.literal("false")
	case c == 'n':
		return nil, r.literal("null")
	}
	return nil, false
}

func (r *jsonReader) object(depth int) (any, bool) {
	r.i++
	m := make(rec)
	r.skipSpace()
	if r.i < len(r.s) && r.s[r.i] == '}' {
		r.i++
		return m, true
	}
	for {
		r.skipSpace()
		key, ok := r.key()
		if !ok {
			return nil, false
		}
		v, ok := r.value(depth + 1)
		if !ok {
			return nil, false
		}
		m[key] = v
		switch r.separator('}') {
		case 1:
			return m, true
		case 0:
			return nil, false
		}
	}
}

// key parses `"name" :` and the space after it.
func (r *jsonReader) key() (string, bool) {
	if r.i >= len(r.s) || r.s[r.i] != '"' {
		return "", false
	}
	key, ok := r.readString()
	if !ok {
		return "", false
	}
	r.skipSpace()
	if r.i >= len(r.s) || r.s[r.i] != ':' {
		return "", false
	}
	r.i++
	r.skipSpace()
	return key, true
}

// separator consumes the space and the `,` or closer after a member: 1 for the closer, 2 for a
// comma (the next member follows), 0 for anything else.
func (r *jsonReader) separator(closer byte) int {
	r.skipSpace()
	if r.i >= len(r.s) {
		return 0
	}
	switch r.s[r.i] {
	case ',':
		r.i++
		return 2
	case closer:
		r.i++
		return 1
	}
	return 0
}

func (r *jsonReader) array(depth int) (any, bool) {
	r.i++
	items := make([]any, 0, 4)
	r.skipSpace()
	if r.i < len(r.s) && r.s[r.i] == ']' {
		r.i++
		return items, true
	}
	for {
		r.skipSpace()
		v, ok := r.value(depth + 1)
		if !ok {
			return nil, false
		}
		items = append(items, v)
		switch r.separator(']') {
		case 1:
			return items, true
		case 0:
			return nil, false
		}
	}
}

// skipValue validates and skips one value.
func (r *jsonReader) skipValue(depth int) bool {
	if r.i >= len(r.s) || depth > maxJSONDepth {
		return false
	}
	switch c := r.s[r.i]; {
	case c == '"':
		return r.skipString()
	case c == '{':
		r.i++
		r.skipSpace()
		if r.i < len(r.s) && r.s[r.i] == '}' {
			r.i++
			return true
		}
		for {
			r.skipSpace()
			if r.i >= len(r.s) || r.s[r.i] != '"' || !r.skipString() {
				return false
			}
			r.skipSpace()
			if r.i >= len(r.s) || r.s[r.i] != ':' {
				return false
			}
			r.i++
			r.skipSpace()
			if !r.skipValue(depth + 1) {
				return false
			}
			switch r.separator('}') {
			case 1:
				return true
			case 0:
				return false
			}
		}
	case c == '[':
		r.i++
		r.skipSpace()
		if r.i < len(r.s) && r.s[r.i] == ']' {
			r.i++
			return true
		}
		for {
			r.skipSpace()
			if !r.skipValue(depth + 1) {
				return false
			}
			switch r.separator(']') {
			case 1:
				return true
			case 0:
				return false
			}
		}
	case c == '-' || c >= '0' && c <= '9':
		end, _, ok := r.scanNumber()
		r.i = end
		return ok
	case c == 't':
		return r.literal("true")
	case c == 'f':
		return r.literal("false")
	case c == 'n':
		return r.literal("null")
	}
	return false
}

// jsonParse is `JSON.parse(text)`: false for text JSON.parse rejects. The strings of the result are
// substrings of text.
func jsonParse(text string) (any, bool) {
	r := jsonReader{s: text}
	r.skipSpace()
	v, ok := r.value(0)
	if !ok {
		return nil, false
	}
	r.skipSpace()
	if r.i != len(text) {
		return nil, false
	}
	return v, true
}

// stringMember handles a member whose normalized form is an optional string: a string is kept, any
// other value is validated and leaves the member absent (the last occurrence of a key decides).
func (r *jsonReader) stringMember(m rec, key string) bool {
	if r.s[r.i] == '"' {
		s, ok := r.readString()
		m[key] = s
		return ok
	}
	delete(m, key)
	return r.skipValue(0)
}

// boolMember is `value === true`: the member is always present.
func (r *jsonReader) boolMember(m rec, key string) bool {
	if r.s[r.i] == 't' {
		if !r.literal("true") {
			return false
		}
		m[key] = true
		return true
	}
	m[key] = false
	return r.skipValue(0)
}

// members iterates the members of the object at r.i, calling member with the key and the reader at
// the value; member must consume the value.
func (r *jsonReader) members(member func(key string) bool) bool {
	r.i++
	r.skipSpace()
	if r.i < len(r.s) && r.s[r.i] == '}' {
		r.i++
		return true
	}
	for {
		r.skipSpace()
		key, ok := r.key()
		if !ok || r.i >= len(r.s) || !member(key) {
			return false
		}
		switch r.separator('}') {
		case 1:
			return true
		case 0:
			return false
		}
	}
}

// messageMembers reads the object at r.i as `messageFrom` (session-files.ts:187-210) does.
func (r *jsonReader) message() (rec, bool) {
	m := make(rec, 8)
	ok := r.members(func(key string) bool {
		switch key {
		case "role", "stopReason", "errorMessage", "summary", "toolName", "namespace", "toolCallId", "command", "output":
			return r.stringMember(m, key)
		case "content", "details", "nestedCalls":
			v, ok := r.value(0)
			m[key] = v
			return ok
		case "isError", "excludeFromContext":
			return r.boolMember(m, key)
		}
		return r.skipValue(0)
	})
	if !ok {
		return nil, false
	}
	for _, key := range [2]string{"isError", "excludeFromContext"} {
		if _, present := m[key]; !present {
			m[key] = false
		}
	}
	return m, true
}

// replacement reads the `replacement` member: null stays null, an object keeps only its content.
func (r *jsonReader) replacement(entry rec) bool {
	switch r.s[r.i] {
	case 'n':
		if !r.literal("null") {
			return false
		}
		entry["replacement"] = nil
		return true
	case '{':
		out := rec{}
		ok := r.members(func(key string) bool {
			if key != "content" {
				return r.skipValue(0)
			}
			v, ok := r.value(0)
			out[key] = v
			return ok
		})
		entry["replacement"] = out
		return ok
	}
	delete(entry, "replacement")
	return r.skipValue(0)
}

// parseSessionLine is `parseSessionLine` (session-files.ts:211-241): one JSONL line as a
// normalized entry record, false for a malformed line (Pi skips those too). Keys the original
// does not carry are dropped and ill-typed string fields are absent, so the record has the shape
// of the TS EntryLike; `display` is always a boolean and a null `parentId` or `replacement` stays
// null. The record does not alias line (see parseSessionLineView).
func parseSessionLine(line string) (rec, bool) {
	return parseSessionLineView(strings.Clone(line))
}

// parseSessionLineView is parseSessionLine for a caller that keeps line unchanged while it uses the
// record: the strings of the record are substrings of line.
func parseSessionLineView(line string) (rec, bool) {
	r := jsonReader{s: line}
	r.skipSpace()
	if r.i >= len(r.s) || r.s[r.i] != '{' {
		return nil, false // not an object (or not JSON at all)
	}
	entry := make(rec, 8)
	ok := r.members(func(key string) bool {
		switch key {
		case "type", "id", "timestamp", "summary", "customType", "cwd", "parentSession", "handoff", "firstKeptEntryId", "targetId":
			return r.stringMember(entry, key)
		case "parentId":
			if r.s[r.i] == 'n' {
				if !r.literal("null") {
					return false
				}
				entry[key] = nil
				return true
			}
			return r.stringMember(entry, key)
		case "content", "details", "data":
			v, ok := r.value(0)
			entry[key] = v
			return ok
		case "display":
			return r.boolMember(entry, key)
		case "message":
			if r.s[r.i] == '{' {
				m, ok := r.message()
				entry[key] = m
				return ok
			}
			delete(entry, key)
			return r.skipValue(0)
		case "replacement":
			return r.replacement(entry)
		}
		return r.skipValue(0)
	})
	if !ok {
		return nil, false
	}
	r.skipSpace()
	if r.i != len(r.s) {
		return nil, false
	}
	if _, present := entry["display"]; !present {
		entry["display"] = false
	}
	return entry, true
}
