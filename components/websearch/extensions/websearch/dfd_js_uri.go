package websearch

import (
	"strings"
	"unicode/utf8"
)

// ECMAScript decodeURI and decodeURIComponent (ECMA-262 Decode, section 19.2.6.5). A malformed sequence is a URIError.

const jsURIReserved = ";/?:@&=+$,#"

func jsURIHex(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// jsURIDecode is Decode(string, reservedSet); ok is false where the original throws a URIError.
func jsURIDecode(s, reserved string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	n := len(s)
	for k := 0; k < n; k++ {
		c := s[k]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		start := k
		if k+2 >= n {
			return "", false
		}
		h1, h2 := jsURIHex(s[k+1]), jsURIHex(s[k+2])
		if h1 < 0 || h2 < 0 {
			return "", false
		}
		k += 2
		first := byte(h1<<4 | h2)
		if first&0x80 == 0 {
			if strings.IndexByte(reserved, first) < 0 {
				b.WriteByte(first)
			} else {
				b.WriteString(s[start : k+1])
			}
			continue
		}
		count := 0
		for m := first; m&0x80 != 0; m <<= 1 {
			count++
		}
		if count == 1 || count > 4 {
			return "", false
		}
		octets := make([]byte, count)
		octets[0] = first
		if k+3*(count-1) >= n {
			return "", false
		}
		for j := 1; j < count; j++ {
			k++
			if s[k] != '%' {
				return "", false
			}
			h1, h2 := jsURIHex(s[k+1]), jsURIHex(s[k+2])
			if h1 < 0 || h2 < 0 {
				return "", false
			}
			k += 2
			octets[j] = byte(h1<<4 | h2)
			if octets[j]&0xC0 != 0x80 {
				return "", false
			}
		}
		r, size := utf8.DecodeRune(octets)
		if size != count || (r == utf8.RuneError && size <= 1) {
			return "", false
		}
		b.Write(octets)
	}
	return b.String(), true
}

// jsDecodeURI is decodeURI(s); a malformed sequence throws the URIError like the original.
func jsDecodeURI(s string) string {
	out, ok := jsURIDecode(s, jsURIReserved)
	if !ok {
		panic(&jsThrow{name: "URIError", msg: "URI malformed"})
	}
	return out
}

// jsDecodeURIComponent is decodeURIComponent(s); ok is false where the original throws a URIError.
func jsDecodeURIComponent(s string) (string, bool) {
	return jsURIDecode(s, "")
}

// jsEncodeURI is encodeURI: everything but letters, digits and ;,/?:@&=+$-_.!~*'()# is percent-encoded as UTF-8.
func jsEncodeURI(s string) string {
	const keep = ";,/?:@&=+$-_.!~*'()#"
	clean := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || (c < 0x80 && strings.IndexByte(keep, c) >= 0)) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.IndexByte(keep, byte(r)) >= 0) {
			b.WriteByte(byte(r))
			continue
		}
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], r)
		for _, c := range buf[:n] {
			b.WriteByte('%')
			b.WriteByte(digits[c>>4])
			b.WriteByte(digits[c&15])
		}
	}
	return b.String()
}
