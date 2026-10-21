package warden

import "strings"

// The reader of an index file in the shape the port and the original write: one pass over the text, strings
// as substrings of it, no reflection. It accepts only what encoding/json would decode into the same values with
// nothing to report: exact key names, each key once, strings and arrays of strings where the entry has them, no null,
// no escape of a surrogate, no control character in a string, nothing after the value. Anything else is refused
// (ok false) and the caller decodes with the generic decoder, which has the final say (readIndex). The tests hold the
// two to each other over mutated files.

type indexScan struct {
	s string
	i int
}

func (d *indexScan) ws() {
	for d.i < len(d.s) {
		switch d.s[d.i] {
		case ' ', '\t', '\n', '\r':
			d.i++
		default:
			return
		}
	}
}

// eat skips white space and takes the byte c.
func (d *indexScan) eat(c byte) bool {
	d.ws()
	if d.i < len(d.s) && d.s[d.i] == c {
		d.i++
		return true
	}
	return false
}

// firstSpecial is the index of the first quote, backslash or control character of s at or after from, or -1.
func firstSpecial(s string, from int) int {
	const ones = 0x0101010101010101
	const high = 0x8080808080808080
	i := from
	for ; i+8 <= len(s); i += 8 {
		w := le64(s[i:])
		quote, slash := w^(ones*'"'), w^(ones*'\\')
		if ((quote-ones)&^quote|(slash-ones)&^slash|(w-ones*0x20)&^w)&high != 0 {
			break
		}
	}
	for ; i < len(s); i++ {
		if c := s[i]; c == '"' || c == '\\' || c < 0x20 {
			return i
		}
	}
	return -1
}

// str reads a string value.
func (d *indexScan) str() (string, bool) {
	d.ws()
	if d.i >= len(d.s) || d.s[d.i] != '"' {
		return "", false
	}
	start := d.i + 1
	k := firstSpecial(d.s, start)
	if k < 0 {
		return "", false
	}
	if d.s[k] == '"' {
		d.i = k + 1
		return d.s[start:k], true
	}
	var b []byte
	for from := start; ; {
		k = firstSpecial(d.s, from)
		if k < 0 {
			return "", false
		}
		b = append(b, d.s[from:k]...)
		switch d.s[k] {
		case '"':
			d.i = k + 1
			return string(b), true
		case '\\':
			if k+1 >= len(d.s) {
				return "", false
			}
			switch c := d.s[k+1]; c {
			case '"', '\\', '/':
				b = append(b, c)
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
				if k+6 > len(d.s) {
					return "", false
				}
				r := rune(0)
				for _, h := range []byte(d.s[k+2 : k+6]) {
					switch {
					case h >= '0' && h <= '9':
						r = r<<4 | rune(h-'0')
					case h >= 'a' && h <= 'f':
						r = r<<4 | rune(h-'a'+10)
					case h >= 'A' && h <= 'F':
						r = r<<4 | rune(h-'A'+10)
					default:
						return "", false
					}
				}
				if r >= 0xD800 && r <= 0xDFFF {
					return "", false
				}
				b = append(b, string(r)...)
				from = k + 6
				continue
			default:
				return "", false
			}
			from = k + 2
		default:
			return "", false
		}
	}
}

// strList reads an array of strings (never null); an empty array is an empty, non-nil slice.
func (d *indexScan) strList() ([]string, bool) {
	if !d.eat('[') {
		return nil, false
	}
	out := []string{}
	if d.eat(']') {
		return out, true
	}
	for {
		s, ok := d.str()
		if !ok {
			return nil, false
		}
		out = append(out, s)
		if d.eat(']') {
			return out, true
		}
		if !d.eat(',') {
			return nil, false
		}
	}
}

func (d *indexScan) boolean() (bool, bool) {
	d.ws()
	switch {
	case strings.HasPrefix(d.s[d.i:], "true"):
		d.i += 4
		return true, true
	case strings.HasPrefix(d.s[d.i:], "false"):
		d.i += 5
		return false, true
	}
	return false, false
}

// entry reads one entry object.
func (d *indexScan) entry(e *IndexEntry) bool {
	if !d.eat('{') {
		return false
	}
	var seen uint32
	if d.eat('}') {
		return true
	}
	for {
		key, ok := d.str()
		if !ok || !d.eat(':') {
			return false
		}
		var bit uint32
		var target *string
		var list *[]string
		var flag *bool
		switch key {
		case "kind":
			bit, target = 1<<0, &e.Kind
		case "name":
			bit, target = 1<<1, &e.Name
		case "scope":
			bit, target = 1<<2, &e.Scope
		case "sourceHash":
			bit, target = 1<<3, &e.SourceHash
		case "role":
			bit, target = 1<<4, &e.Role
		case "lead":
			bit, target = 1<<5, &e.Lead
		case "useWhen":
			bit, list = 1<<6, &e.UseWhen
		case "notWhen":
			bit, list = 1<<7, &e.NotWhen
		case "inputs":
			bit, target = 1<<8, &e.Inputs
		case "examples":
			bit, list = 1<<9, &e.Examples
		case "thin":
			bit, flag = 1<<10, &e.Thin
		case "sourceQuality":
			bit, target = 1<<11, &e.SourceQuality
		case "improve":
			bit, target = 1<<12, &e.Improve
		case "_location":
			bit, target = 1<<13, &e.Location
		case "truncated":
			bit, flag = 1<<14, &e.Truncated
		default:
			return false
		}
		if seen&bit != 0 {
			return false
		}
		seen |= bit
		switch {
		case target != nil:
			if *target, ok = d.str(); !ok {
				return false
			}
		case list != nil:
			if *list, ok = d.strList(); !ok {
				return false
			}
		default:
			if *flag, ok = d.boolean(); !ok {
				return false
			}
		}
		if d.eat('}') {
			return true
		}
		if !d.eat(',') {
			return false
		}
	}
}

// decodeIndexFast reads the text of an index file; ok is false when the text is anything but the plain shape, a file
// that is not an index file included (the caller's generic route tells those apart).
func decodeIndexFast(s string) (*IndexFile, bool) {
	d := indexScan{s: s}
	if !d.eat('{') {
		return nil, false
	}
	f := &IndexFile{FormatVersion: 1}
	var seen uint8
	for {
		key, ok := d.str()
		if !ok || !d.eat(':') {
			return nil, false
		}
		var bit uint8
		switch key {
		case "formatVersion":
			bit = 1
			d.ws()
			// Only the plain 1: the other spellings of a number are the generic decoder's.
			if !strings.HasPrefix(d.s[d.i:], "1") || d.i+1 < len(d.s) && strings.IndexByte(" \t\r\n,}", d.s[d.i+1]) < 0 {
				return nil, false
			}
			d.i++
		case "builtAt":
			bit = 2
			f.BuiltAt, ok = d.str()
		case "model":
			bit = 4
			f.Model, ok = d.str()
		case "entries":
			bit = 8
			ok = d.entries(f)
		default:
			return nil, false
		}
		if !ok || seen&bit != 0 {
			return nil, false
		}
		seen |= bit
		if d.eat('}') {
			break
		}
		if !d.eat(',') {
			return nil, false
		}
	}
	d.ws()
	if d.i != len(d.s) || seen != 15 {
		return nil, false
	}
	return f, true
}

func (d *indexScan) entries(f *IndexFile) bool {
	if !d.eat('[') {
		return false
	}
	f.Entries = make([]IndexEntry, 0, min(strings.Count(d.s, `"kind"`), len(d.s)/20))
	if d.eat(']') {
		return true
	}
	for {
		f.Entries = append(f.Entries, IndexEntry{})
		if !d.entry(&f.Entries[len(f.Entries)-1]) {
			return false
		}
		if d.eat(']') {
			return true
		}
		if !d.eat(',') {
			return false
		}
	}
}
