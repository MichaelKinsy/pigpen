package websearch

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

var charsetRE = regexp.MustCompile(`(?i)charset\s*=\s*["']?([^;"'\s]+)`)

// decodeText is readTextResponseWithLimit of extract.ts: new TextDecoder(charset || "utf-8").decode(buffer), and
// UTF-8 when the label is not one TextDecoder knows. UTF-8 and UTF-16 follow the Encoding Standard's decoders
// (a leading BOM is dropped, each maximal invalid subpart is one U+FFFD); the other labels go through
// golang.org/x/text/encoding/htmlindex, whose legacy decoders replace invalid input differently (port/PORT.md).
func decodeText(data []byte, contentType string) string {
	if m := charsetRE.FindStringSubmatch(contentType); m != nil {
		label := strings.ToLower(m[1])
		if enc, err := htmlindex.Get(label); err == nil && enc != encoding.Replacement {
			name, _ := htmlindex.Name(enc)
			switch name {
			case "utf-8", "replacement":
			case "utf-16le":
				return decodeUTF16(data, false)
			case "utf-16be":
				return decodeUTF16(data, true)
			default:
				if strings.HasPrefix(name, "iso-8859-") || strings.HasPrefix(name, "windows-12") {
					return decodeSingleByte(enc, data)
				}
				if out, err := enc.NewDecoder().Bytes(data); err == nil {
					return string(out)
				}
			}
		}
	}
	return decodeUTF8(data)
}

// decodeUTF8 is the Encoding Standard's UTF-8 decoder with BOM removal.
func decodeUTF8(data []byte) string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); {
		c := data[i]
		if c < 0x80 {
			b.WriteByte(c)
			i++
			continue
		}
		need, lo, hi := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lo = 0xA0
			}
			if c == 0xED {
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lo = 0x90
			}
			if c == 0xF4 {
				hi = 0x8F
			}
		default:
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(data) || data[j] < lo || data[j] > hi {
				ok = false
				break
			}
			lo, hi = 0x80, 0xBF
			j++
		}
		if ok {
			b.Write(data[i:j])
		} else {
			b.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return b.String()
}

// decodeUTF16 is the Encoding Standard's UTF-16LE and UTF-16BE decoders: a BOM of the matching order is dropped,
// a lone surrogate or an odd last byte is U+FFFD.
func decodeUTF16(data []byte, be bool) string {
	unit := func(i int) rune {
		if be {
			return rune(data[i])<<8 | rune(data[i+1])
		}
		return rune(data[i+1])<<8 | rune(data[i])
	}
	i := 0
	if len(data) >= 2 && unit(0) == 0xFEFF {
		i = 2
	}
	var b strings.Builder
	for ; i+1 < len(data); i += 2 {
		u := unit(i)
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if i+3 < len(data) {
				if l := unit(i + 2); l >= 0xDC00 && l <= 0xDFFF {
					b.WriteRune(0x10000 + (u-0xD800)<<10 + (l - 0xDC00))
					i += 2
					continue
				}
			}
			b.WriteRune(utf8.RuneError)
		case u >= 0xDC00 && u <= 0xDFFF:
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(u)
		}
	}
	if len(data)%2 == 1 {
		b.WriteRune(utf8.RuneError)
	}
	return b.String()
}

// decodeSingleByte decodes a single-byte legacy encoding the way the Encoding Standard's index does: a byte of
// 0x80 to 0x9F that the x/text table leaves undefined is the C1 control of the same value.
func decodeSingleByte(enc encoding.Encoding, data []byte) string {
	var table [256]rune
	dec := enc.NewDecoder()
	for i := range table {
		out, err := dec.Bytes([]byte{byte(i)})
		r, _ := utf8.DecodeRune(out)
		if err != nil || r == utf8.RuneError && i >= 0x80 && i <= 0x9F {
			r = rune(i)
			if i < 0x80 || i > 0x9F {
				r = utf8.RuneError
			}
		}
		table[i] = r
	}
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		b.WriteRune(table[c])
	}
	return b.String()
}
