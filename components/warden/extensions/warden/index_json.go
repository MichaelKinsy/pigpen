package warden

import (
	"strconv"
	"unicode/utf8"
)

// JSON.stringify for the index's own shapes (index-cmd.ts writes with JSON.stringify(index, null, 2) and measures an
// entry with JSON.stringify(entry).length). Written by hand because the generic encoder costs several times what
// the fixed shape does, and an index of a few hundred entries is read and written on the session's path; the
// tests hold it to the generic encoder's output.

type jsonOut struct {
	b      []byte
	indent bool
}

const indentSpaces = "                                "

func (w *jsonOut) newline(level int) {
	if !w.indent {
		return
	}
	w.b = append(w.b, '\n')
	if 2*level <= len(indentSpaces) {
		w.b = append(w.b, indentSpaces[:2*level]...)
		return
	}
	for range level {
		w.b = append(w.b, ' ', ' ')
	}
}

func (w *jsonOut) key(name string, level int, first bool) {
	if !first {
		w.b = append(w.b, ',')
	}
	w.newline(level)
	w.str(name)
	w.b = append(w.b, ':')
	if w.indent {
		w.b = append(w.b, ' ')
	}
}

const hexDigits = "0123456789abcdef"

// str appends a JSON string the way JavaScript writes it: quotes, backslashes and control characters escaped,
// everything else (including <, >, & and U+2028) as is.
func (w *jsonOut) str(s string) {
	w.b = append(w.b, '"')
	start := 0
	// Bytes of 0x80 and over pass through as they are (a Go string holds well-formed UTF-8 or the bytes stay as they
	// were), so the only bytes that need a look are the ones firstSpecial finds.
	for i := firstSpecial(s, 0); i >= 0; i = firstSpecial(s, start) {
		w.b = append(w.b, s[start:i]...)
		switch c := s[i]; c {
		case '"', '\\':
			w.b = append(w.b, '\\', c)
		case '\b':
			w.b = append(w.b, '\\', 'b')
		case '\f':
			w.b = append(w.b, '\\', 'f')
		case '\n':
			w.b = append(w.b, '\\', 'n')
		case '\r':
			w.b = append(w.b, '\\', 'r')
		case '\t':
			w.b = append(w.b, '\\', 't')
		default:
			w.b = append(w.b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		start = i + 1
	}
	w.b = append(w.b, s[start:]...)
	w.b = append(w.b, '"')
}

func (w *jsonOut) strings(items []string, level int) {
	if len(items) == 0 {
		w.b = append(w.b, '[', ']')
		return
	}
	w.b = append(w.b, '[')
	for i, it := range items {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		w.newline(level + 1)
		w.str(it)
	}
	w.newline(level)
	w.b = append(w.b, ']')
}

// entry appends one index entry, its members at level+1.
func (w *jsonOut) entry(e *IndexEntry, level int) {
	w.b = append(w.b, '{')
	w.key("kind", level+1, true)
	w.str(e.Kind)
	w.key("name", level+1, false)
	w.str(e.Name)
	w.key("scope", level+1, false)
	w.str(e.Scope)
	w.key("sourceHash", level+1, false)
	w.str(e.SourceHash)
	w.key("role", level+1, false)
	w.str(e.Role)
	w.key("lead", level+1, false)
	w.str(e.Lead)
	w.key("useWhen", level+1, false)
	w.strings(e.UseWhen, level+1)
	w.key("notWhen", level+1, false)
	w.strings(e.NotWhen, level+1)
	w.key("inputs", level+1, false)
	w.str(e.Inputs)
	w.key("examples", level+1, false)
	w.strings(e.Examples, level+1)
	w.key("thin", level+1, false)
	w.b = strconv.AppendBool(w.b, e.Thin)
	w.key("sourceQuality", level+1, false)
	w.str(e.SourceQuality)
	if e.Improve != "" {
		w.key("improve", level+1, false)
		w.str(e.Improve)
	}
	if e.Location != "" {
		w.key("_location", level+1, false)
		w.str(e.Location)
	}
	if e.Truncated {
		w.key("truncated", level+1, false)
		w.b = append(w.b, "true"...)
	}
	w.newline(level)
	w.b = append(w.b, '}')
}

// entryLength is JSON.stringify(entry).length: UTF-16 units of the compact form.
func entryLength(e *IndexEntry) int {
	w := jsonOut{b: make([]byte, 0, 512)}
	w.entry(e, 0)
	return utf16LenBytes(w.b)
}

// utf16LenBytes is the length in UTF-16 units of UTF-8 text.
func utf16LenBytes(b []byte) int {
	n := 0
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < utf8.RuneSelf:
			n++
			i++
		case c >= 0xF0:
			n += 2
			i += 4
		case c >= 0xE0:
			n++
			i += 3
		default:
			n++
			i += 2
		}
		if i > len(b) {
			break
		}
	}
	return n
}

// indexFileJSON is JSON.stringify(index, null, 2).
func indexFileJSON(f *IndexFile) []byte {
	w := jsonOut{b: make([]byte, 0, 1024+len(f.Entries)*640), indent: true}
	w.b = append(w.b, '{')
	w.key("formatVersion", 1, true)
	w.b = strconv.AppendInt(w.b, int64(f.FormatVersion), 10)
	w.key("builtAt", 1, false)
	w.str(f.BuiltAt)
	w.key("model", 1, false)
	w.str(f.Model)
	w.key("entries", 1, false)
	if len(f.Entries) == 0 {
		w.b = append(w.b, '[', ']')
	} else {
		w.b = append(w.b, '[')
		for i := range f.Entries {
			if i > 0 {
				w.b = append(w.b, ',')
			}
			w.newline(2)
			w.entry(&f.Entries[i], 2)
		}
		w.newline(1)
		w.b = append(w.b, ']')
	}
	w.newline(0)
	w.b = append(w.b, '}')
	return w.b
}
