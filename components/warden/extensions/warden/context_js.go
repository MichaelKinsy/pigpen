package warden

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The context modules (saver, dedupe, filter, excerpt, relevance, output compression) run upstream's regular
// expressions over arbitrary tool output. JavaScript and RE2 differ on three atoms that reach such text: `\s` is
// the Unicode white-space set in JavaScript and ASCII only in RE2, and `.` stops at \r, U+2028 and U+2029 in
// JavaScript but only at \n in RE2. jsRE translates a JavaScript source pattern to the RE2 pattern with the same
// meaning on those atoms, so a CRLF log or a NBSP is read the way the original reads it. Everything else in the
// patterns written here is common to both syntaxes (no lookahead, no backreference).
const jsSpaceRanges = ` \t\n\v\f\r\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsPattern translates the atoms `\s`, `\S` and `.` (outside a class) and `\s` inside a class. `\S` inside a class
// is not supported: no pattern here needs it, and a negated set cannot be spliced into a class.
func jsPattern(src string) string {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\' && i+1 < len(src):
			next := src[i+1]
			i++
			switch {
			case next == 's' && inClass:
				out.WriteString(jsSpaceRanges)
			case next == 's':
				out.WriteString("[" + jsSpaceRanges + "]")
			case next == 'S' && !inClass:
				out.WriteString("[^" + jsSpaceRanges + "]")
			case next == 'S':
				panic("jsPattern: \\S inside a class: " + src)
			default:
				out.WriteByte('\\')
				out.WriteByte(next)
			}
		case c == '[' && !inClass:
			inClass = true
			out.WriteByte(c)
		case c == ']' && inClass:
			inClass = false
			out.WriteByte(c)
		case c == '.' && !inClass:
			out.WriteString(`[^\n\r\x{2028}\x{2029}]`)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// jsRE compiles a JavaScript source pattern lazily (see lazyre.go). Flags are RE2 flag groups written in the
// pattern, `(?i)` for JavaScript's `i`, `(?m)` for `m`.
func jsRE(src string) *lazyRegexp { return lazyRE(jsPattern(src)) }

// jsTrimEnd is String.prototype.trimEnd.
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, isJSSpace) }

// jsCollapseSpace is text.replace(/\s+/g, " ").
func jsCollapseSpace(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if isJSSpace(r) {
			if !in {
				b.WriteByte(' ')
			}
			in = true
			continue
		}
		in = false
		b.WriteRune(r)
	}
	return b.String()
}

// jsStringify is JSON.stringify for a value json can marshal: no HTML escaping, and U+2028/U+2029 literal.
func jsStringify(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	out = strings.ReplaceAll(out, `\u2028`, "\u2028")
	return strings.ReplaceAll(out, `\u2029`, "\u2029")
}
