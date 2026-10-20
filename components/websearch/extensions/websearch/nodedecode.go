package websearch

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Node's lenient decoders, which the GitHub extractors rely on: Buffer.from(text, "base64") and
// buffer.toString("utf-8") / readFileSync(path, "utf-8").

func base64Value(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+' || c == '-':
		return 62
	case c == '/' || c == '_':
		return 63
	}
	return -1
}

// nodeBase64Decode is Buffer.from(s, "base64"): both alphabets are accepted, characters outside
// them (the line breaks `gh api --jq .content` leaves in) are skipped, a "=" ends the data, and a
// final partial group yields the whole bytes it holds.
func nodeBase64Decode(s string) []byte {
	out := make([]byte, 0, len(s)*3/4)
	var acc uint
	var bits uint
	for i := 0; i < len(s); i++ {
		v := base64Value(s[i])
		if v < 0 {
			if s[i] == '=' {
				break
			}
			continue
		}
		acc = acc<<6 | uint(v)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// decodeUTF8Lossy is the WHATWG UTF-8 decoder Node uses for toString("utf-8"): every maximal invalid
// subsequence becomes one U+FFFD (Go's conversions would give one per byte), and a BOM is kept.
func decodeUTF8Lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b))
	var cp rune
	needed, seen := 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				sb.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				sb.WriteRune(utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			cp, needed, seen = 0, 0, 0
			lower, upper = 0x80, 0xBF
			i--
			sb.WriteRune(utf8.RuneError)
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			sb.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		sb.WriteRune(utf8.RuneError)
	}
	return sb.String()
}

// jsParseInt10 is parseInt(s, 10): optional whitespace and sign, then the leading digits; ok is
// false for NaN.
func jsParseInt10(s string) (float64, bool) {
	s = jsTrim(s)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

// toFixed1 is bytes.toFixed(1) for a byte count divided by a power of two: exact integer
// arithmetic, rounding half up like Number.prototype.toFixed.
func toFixed1(bytes, divisor int64) string {
	n := (bytes*20 + divisor) / (2 * divisor)
	return strconv.FormatInt(n/10, 10) + "." + strconv.FormatInt(n%10, 10)
}
