package rpiv_web_tools

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Node behaviours the GitHub interceptor depends on, which Go's standard library does not match: JavaScript
// strings count UTF-16 code units, Buffer decodes UTF-8 and base64 leniently, and path.extname, toFixed and
// the default sort have their own rules.

// asciiRun is the number of leading ASCII bytes of s that can be skipped eight at a time, at most limit.
func asciiRun(s string, limit int) int {
	i := 0
	for i+8 <= len(s) && i+8 <= limit {
		w := uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
			uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
		if w&0x8080808080808080 != 0 {
			break
		}
		i += 8
	}
	return i
}

// utf16Len is `s.length` of a valid UTF-8 string, counted over the bytes: an ASCII byte and a lead byte of a
// 2- or 3-byte rune is one code unit, the lead byte of a 4-byte rune two, a continuation byte none.
func utf16Len(s string) int {
	i := asciiRun(s, len(s))
	n := i
	for ; i < len(s); i++ {
		switch b := s[i]; {
		case b < 0x80, b >= 0xC0 && b < 0xF0:
			n++
		case b >= 0xF0:
			n += 2
		}
	}
	return n
}

// utf16Prefix is `s.slice(0, n)`: a cut inside a surrogate pair leaves a lone surrogate, which Node writes as U+FFFD.
func utf16Prefix(s string, n int) string {
	units := asciiRun(s, n)
	for i := units; i < len(s); i++ {
		b := s[i]
		w := 1
		switch {
		case b >= 0x80 && b < 0xC0:
			continue
		case b >= 0xF0:
			w = 2
		}
		if units+w > n {
			if w == 2 && units < n {
				return s[:i] + "\ufffd"
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// decodeUTF8Lossy is Buffer#toString("utf-8"): invalid input becomes U+FFFD by the WHATWG decoder's rules (one
// replacement per maximal invalid prefix).
func decodeUTF8Lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		var need int
		var cp rune
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need, cp = 1, rune(c&0x1F)
		case c >= 0xE0 && c <= 0xEF:
			need, cp = 2, rune(c&0x0F)
			if c == 0xE0 {
				lo = 0xA0
			}
			if c == 0xED {
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need, cp = 3, rune(c&0x07)
			if c == 0xF0 {
				lo = 0x90
			}
			if c == 0xF4 {
				hi = 0x8F
			}
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		i++
		ok := true
		for k := 0; k < need; k++ {
			if i >= len(b) || b[i] < lo || b[i] > hi {
				ok = false
				break
			}
			cp = cp<<6 | rune(b[i]&0x3F)
			lo, hi = 0x80, 0xBF
			i++
		}
		if !ok {
			sb.WriteRune(utf8.RuneError) // the offending byte is read again
			continue
		}
		sb.WriteRune(cp)
	}
	return sb.String()
}

// b64Value maps a byte to its sextet: -1 is skipped (whitespace and every other stray character), -2 is the padding
// that ends the data.
var b64Value = func() (t [256]int8) {
	for i := range t {
		t[i] = -1
	}
	for i, c := range []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") {
		t[c] = int8(i)
	}
	t['+'] = 62
	t['-'] = 62
	t['/'] = 63
	t['_'] = 63
	t['='] = -2
	return t
}()

// decodeBase64Lenient is Buffer.from(s, "base64"): both alphabets, whitespace and other stray characters are
// skipped, decoding stops at the first "=", and a missing padding is accepted.
func decodeBase64Lenient(s string) []byte {
	out := make([]byte, len(s)*3/4+1)
	n := 0
	var acc uint32
	bits := 0
	for i := range len(s) {
		v := b64Value[s[i]]
		if v < 0 {
			if v == -2 {
				break
			}
			continue
		}
		acc = acc<<6 | uint32(v)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out[n] = byte(acc >> uint(bits))
			n++
			acc &= 1<<uint(bits) - 1
		}
	}
	return out[:n]
}

// decodeURIComponent is the JavaScript function; ok is false where it throws a URIError.
func decodeURIComponent(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		h1, ok1 := unhex(s[i+1])
		h2, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", false
		}
		b = append(b, h1<<4|h2)
		i += 2
	}
	if !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// posixExtname is path.posix.extname.
func posixExtname(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	i := strings.LastIndex(p, ".")
	if i <= 0 || p == ".." {
		return ""
	}
	return p[i:]
}

// compareUTF16 orders strings as Array#sort does by default: by UTF-16 code unit.
func compareUTF16(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		ra, wa := utf8.DecodeRuneInString(a)
		rb, wb := utf8.DecodeRuneInString(b)
		if ra != rb {
			ua, ub := utf16Unit(ra), utf16Unit(rb)
			if ua == ub {
				ua, ub = ra, rb // the same high surrogate: the low surrogates order as the code points do
			}
			if ua < ub {
				return -1
			}
			return 1
		}
		a, b = a[wa:], b[wb:]
	}
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return -1
	}
	return 1
}

// utf16Unit is the first UTF-16 code unit of r.
func utf16Unit(r rune) rune {
	if r < 0x10000 {
		return r
	}
	return 0xD800 + (r-0x10000)>>10
}

// jsParseInt10 is parseInt(s, 10): the leading integer, or ok false for NaN.
func jsParseInt10(s string) (float64, bool) {
	s = strings.TrimLeft(s, " \t\n\v\f\r\u00a0\ufeff")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 {
		return 0, false
	}
	v := 0.0
	for _, c := range []byte(s[:n]) {
		v = v*10 + float64(c-'0')
	}
	if neg {
		v = -v
	}
	return v, true
}

// formatFileSize is the interceptor's: toFixed(1) rounds a tie up, which exact integer arithmetic reproduces
// (the quotient by a power of two is exact in binary).
func formatFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		t := (bytes*10 + 512) / 1024
		return fmt.Sprintf("%d.%d KB", t/10, t%10)
	}
	t := (bytes*10 + 524288) / 1048576
	return fmt.Sprintf("%d.%d MB", t/10, t%10)
}

// jsRound is Math.round: halves go up.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }
