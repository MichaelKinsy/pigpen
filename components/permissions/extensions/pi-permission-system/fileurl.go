package pi_permission_system

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Port of Node's `fileURLToPath(url, { windows })` (lib/internal/url.js) for the strings the original feeds it: the file URL a
// built-in tool's `path` argument spells (native-tool-target.ts) and the module URL node-modules-discovery.ts walks up from. It
// includes the part of the WHATWG URL parser that decides the pathname of a `file:` URL (the file, file slash, file host, path
// start and path states, the Windows drive letter quirks and the path percent-encode set), then Node's own decoding.
//
// A host that is not a plain ASCII domain is only approximated (no IDNA mapping, no IPv6 compression): on win32 it ends up in a
// UNC prefix either way, so the path still reads as external; on POSIX any host fails, as in Node.

var errInvalidFileURL = errors.New("invalid file URL")

func isC0OrSpace(r rune) bool { return r <= 0x20 }

// inPathEncodeSet is the WHATWG path percent-encode set.
func inPathEncodeSet(r rune) bool {
	if r <= 0x1f || r >= 0x7f {
		return true
	}
	switch r {
	case ' ', '"', '#', '<', '>', '?', '^', '`', '{', '}':
		return true
	}
	return false
}

func percentEncodeRune(b *strings.Builder, r rune) {
	if !inPathEncodeSet(r) {
		b.WriteRune(r)
		return
	}
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	for _, c := range buf[:n] {
		b.WriteByte('%')
		b.WriteByte("0123456789ABCDEF"[c>>4])
		b.WriteByte("0123456789ABCDEF"[c&15])
	}
}

func isASCIIAlpha(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// isWindowsDriveLetter reports two code points: an ASCII letter then ':' or '|'.
func isWHATWGDriveLetter(s string) bool {
	return len(s) == 2 && isASCIIAlpha(rune(s[0])) && (s[1] == ':' || s[1] == '|')
}

func isNormalizedDriveLetter(s string) bool {
	return len(s) == 2 && isASCIIAlpha(rune(s[0])) && s[1] == ':'
}

func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func isSingleDot(s string) bool {
	l := strings.ToLower(s)
	return l == "." || l == "%2e"
}

// parseFileURL returns the host and pathname of a file URL, or an error where `new URL` throws or the scheme is not file.
func parseFileURL(input string) (host, pathname string, err error) {
	input = strings.TrimFunc(input, isC0OrSpace)
	input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)
	colon := strings.IndexByte(input, ':')
	if colon <= 0 || !isASCIIAlpha(rune(input[0])) {
		return "", "", errInvalidFileURL
	}
	for _, c := range input[:colon] {
		if !isASCIIAlpha(c) && !(c >= '0' && c <= '9') && c != '+' && c != '-' && c != '.' {
			return "", "", errInvalidFileURL
		}
	}
	if strings.ToLower(input[:colon]) != "file" {
		return "", "", errInvalidFileURL
	}
	rest := []rune(input[colon+1:])
	isSlash := func(r rune) bool { return r == '/' || r == '\\' }

	var path []string
	p := 0
	// file state
	pathStart := 0 // index where the path state begins
	if p < len(rest) && isSlash(rest[p]) {
		p++
		if p < len(rest) && isSlash(rest[p]) {
			p++
			// file host state
			end := p
			for end < len(rest) && !isSlash(rest[end]) && rest[end] != '?' && rest[end] != '#' {
				end++
			}
			buf := string(rest[p:end])
			if isWHATWGDriveLetter(buf) {
				// the buffer is the first path segment, the host stays empty
				pathStart = p
			} else if buf == "" {
				pathStart = end
			} else {
				h, herr := parseFileHost(buf)
				if herr != nil {
					return "", "", herr
				}
				if h == "localhost" {
					h = ""
				}
				host = h
				pathStart = end
			}
		} else {
			pathStart = p
		}
	} else {
		pathStart = 0
	}
	// path start state: one leading slash of the path is consumed
	p = pathStart
	if p < len(rest) && isSlash(rest[p]) {
		p++
	}
	// path state
	buf := strings.Builder{}
	raw := strings.Builder{}
	for i := p; ; i++ {
		eof := i >= len(rest)
		var c rune
		if !eof {
			c = rest[i]
		}
		if eof || isSlash(c) || c == '?' || c == '#' {
			seg := raw.String()
			slash := !eof && isSlash(c)
			switch {
			case isDoubleDot(seg):
				if !(len(path) == 1 && isNormalizedDriveLetter(path[0])) && len(path) > 0 {
					path = path[:len(path)-1]
				}
				if !slash {
					path = append(path, "")
				}
			case isSingleDot(seg):
				if !slash {
					path = append(path, "")
				}
			default:
				enc := buf.String()
				if len(path) == 0 && isWHATWGDriveLetter(seg) {
					enc = seg[:1] + ":"
				}
				path = append(path, enc)
			}
			buf.Reset()
			raw.Reset()
			if eof || c == '?' || c == '#' {
				break
			}
			continue
		}
		raw.WriteRune(c)
		percentEncodeRune(&buf, c)
	}
	var out strings.Builder
	for _, s := range path {
		out.WriteByte('/')
		out.WriteString(s)
	}
	return host, out.String(), nil
}

// parseFileHost is the WHATWG host parser for a special scheme (host parsing, domain to ASCII per UTS #46 as far as the host's
// text goes, the IPv4 and IPv6 parsers), returning the host as `url.hostname` reads it and as Node's domainToUnicode shows it.
func parseFileHost(s string) (string, error) {
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return "", errInvalidFileURL
		}
		addr, ok := parseIPv6(s[1 : len(s)-1])
		if !ok {
			return "", errInvalidFileURL
		}
		return "[" + serializeIPv6(addr) + "]", nil
	}
	d, err := percentDecodeBytes(s)
	if err != nil || !utf8.Valid(d) {
		return "", errInvalidFileURL
	}
	domain, ok := domainToUnicodeHost(string(d))
	if !ok || domain == "" {
		return "", errInvalidFileURL
	}
	for _, r := range domain {
		if r < 0x80 && (r <= 0x20 || r == 0x7f) {
			return "", errInvalidFileURL
		}
		switch r {
		case '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|', '%':
			return "", errInvalidFileURL
		}
	}
	if endsInANumber(domain) {
		v4, ok := parseIPv4(domain)
		if !ok {
			return "", errInvalidFileURL
		}
		return v4, nil
	}
	return domain, nil
}

// uts46Ignored are the code points UTS #46 maps to nothing.
func uts46Ignored(r rune) bool {
	switch r {
	case 0xad, 0x34f, 0x180b, 0x180c, 0x180d, 0x180f, 0x200b, 0x2060, 0x2064, 0xfeff:
		return true
	}
	return (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xe0100 && r <= 0xe01ef)
}

// domainToUnicodeHost maps a host like UTS #46 (ignored characters removed, NFKC, lower case, ideographic full stops as dots)
// and reads each `xn--` label back as Unicode, which is what url.hostname through domainToUnicode shows. ok false is an invalid
// label (a malformed punycode label). The mapping is the part of UTS #46 that decides what the host reads as; the validity tables
// are not ported.
func domainToUnicodeHost(domain string) (string, bool) {
	var b strings.Builder
	for _, r := range domain {
		if uts46Ignored(r) {
			continue
		}
		b.WriteRune(r)
	}
	mapped := jsToLowerCase(norm.NFKC.String(b.String()))
	mapped = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".").Replace(mapped)
	labels := strings.Split(mapped, ".")
	for i, l := range labels {
		if strings.HasPrefix(l, "xn--") {
			dec, ok := punycodeDecode(l[4:])
			if !ok {
				return "", false
			}
			labels[i] = dec
		}
	}
	return strings.Join(labels, "."), true
}

// punycodeDecode is RFC 3492 decoding.
func punycodeDecode(in string) (string, bool) {
	const base, tmin, tmax, skew, damp, initialBias, initialN = 36, 1, 26, 38, 700, 72, 128
	var out []rune
	b := strings.LastIndexByte(in, '-')
	if b > 0 {
		for _, c := range in[:b] {
			if c >= 0x80 {
				return "", false
			}
			out = append(out, c)
		}
		in = in[b+1:]
	} else if b == 0 {
		in = in[1:]
	}
	n, i, bias := initialN, 0, initialBias
	adapt := func(delta, numPoints int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / numPoints
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	for p := 0; p < len(in); {
		oldi, w := i, 1
		for k := base; ; k += base {
			if p >= len(in) {
				return "", false
			}
			c := in[p]
			p++
			var digit int
			switch {
			case c >= '0' && c <= '9':
				digit = int(c-'0') + 26
			case c >= 'a' && c <= 'z':
				digit = int(c - 'a')
			case c >= 'A' && c <= 'Z':
				digit = int(c - 'A')
			default:
				return "", false
			}
			i += digit * w
			t := k - bias
			if t < tmin {
				t = tmin
			} else if t > tmax {
				t = tmax
			}
			if digit < t {
				break
			}
			w *= base - t
			if i > 1<<30 || w > 1<<30 {
				return "", false
			}
		}
		bias = adapt(i-oldi, len(out)+1, oldi == 0)
		n += i / (len(out) + 1)
		i %= len(out) + 1
		if n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff) {
			return "", false
		}
		out = append(out[:i], append([]rune{rune(n)}, out[i:]...)...)
		i++
	}
	return string(out), true
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseIPv4Number is the IPv4 number parser: a 0x prefix is hex, a leading 0 octal, else decimal.
func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := 10
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s, radix = s[2:], 16
	} else if len(s) >= 2 && s[0] == '0' {
		s, radix = s[1:], 8
	}
	if s == "" {
		return 0, true
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		d := fromHex(s[i])
		if d < 0 || d >= radix {
			return 0, false
		}
		v = v*uint64(radix) + uint64(d)
		if v > 1<<40 {
			v = 1 << 40
		}
	}
	return v, true
}

func endsInANumber(host string) bool {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if isASCIIDigits(last) {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok && (strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X"))
}

func parseIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) > 1 {
			parts = parts[:len(parts)-1]
		}
	}
	if len(parts) > 4 {
		return "", false
	}
	var nums []uint64
	for _, p := range parts {
		v, ok := parseIPv4Number(p)
		if !ok {
			return "", false
		}
		nums = append(nums, v)
	}
	for _, v := range nums[:len(nums)-1] {
		if v > 255 {
			return "", false
		}
	}
	last := nums[len(nums)-1]
	limit := uint64(1)
	for i := 0; i < 5-len(nums); i++ {
		limit *= 256
	}
	if last >= limit {
		return "", false
	}
	ipv4 := last
	for i, v := range nums[:len(nums)-1] {
		m := uint64(1)
		for j := 0; j < 3-i; j++ {
			m *= 256
		}
		ipv4 += v * m
	}
	return fmt.Sprintf("%d.%d.%d.%d", ipv4>>24&255, ipv4>>16&255, ipv4>>8&255, ipv4&255), true
}

// parseIPv6 is the WHATWG IPv6 parser.
func parseIPv6(in string) (addr [8]uint16, ok bool) {
	piece, compress := 0, -1
	p := 0
	at := func(i int) int {
		if i < len(in) {
			return int(in[i])
		}
		return -1
	}
	if at(p) == ':' {
		if at(p+1) != ':' {
			return addr, false
		}
		p += 2
		piece++
		compress = piece
	}
	for at(p) != -1 {
		if piece == 8 {
			return addr, false
		}
		if at(p) == ':' {
			if compress != -1 {
				return addr, false
			}
			p++
			piece++
			compress = piece
			continue
		}
		value, length := 0, 0
		for length < 4 && at(p) != -1 && fromHex(byte(at(p))) >= 0 {
			value = value*16 + fromHex(byte(at(p)))
			p++
			length++
		}
		if at(p) == '.' {
			if length == 0 {
				return addr, false
			}
			p -= length
			if piece > 6 {
				return addr, false
			}
			seen := 0
			for at(p) != -1 {
				v4 := -1
				if seen > 0 {
					if at(p) == '.' && seen < 4 {
						p++
					} else {
						return addr, false
					}
				}
				if at(p) == -1 || at(p) < '0' || at(p) > '9' {
					return addr, false
				}
				for at(p) >= '0' && at(p) <= '9' {
					n := at(p) - '0'
					if v4 == -1 {
						v4 = n
					} else if v4 == 0 {
						return addr, false
					} else {
						v4 = v4*10 + n
					}
					if v4 > 255 {
						return addr, false
					}
					p++
				}
				addr[piece] = addr[piece]*0x100 + uint16(v4)
				seen++
				if seen == 2 || seen == 4 {
					piece++
				}
			}
			if seen != 4 {
				return addr, false
			}
			break
		} else if at(p) == ':' {
			p++
			if at(p) == -1 {
				return addr, false
			}
		} else if at(p) != -1 {
			return addr, false
		}
		addr[piece] = uint16(value)
		piece++
	}
	if compress != -1 {
		swaps := piece - compress
		piece = 7
		for piece != 0 && swaps > 0 {
			addr[piece], addr[compress+swaps-1] = addr[compress+swaps-1], addr[piece]
			piece--
			swaps--
		}
	} else if piece != 8 {
		return addr, false
	}
	return addr, true
}

// serializeIPv6 is the WHATWG IPv6 serializer: the first longest run of two or more zero pieces compresses to `::`.
func serializeIPv6(a [8]uint16) string {
	best, bestLen := -1, 0
	for i := 0; i < 8; {
		if a[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && a[j] == 0 {
			j++
		}
		if j-i > bestLen && j-i > 1 {
			best, bestLen = i, j-i
		}
		i = j
	}
	var b strings.Builder
	ignore0 := false
	for i := 0; i < 8; i++ {
		if ignore0 && a[i] == 0 {
			continue
		}
		ignore0 = false
		if best == i {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			ignore0 = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(a[i]), 16))
		if i != 7 {
			b.WriteString(":")
		}
	}
	return b.String()
}

func fromHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// percentDecodeBytes is the WHATWG percent-decode: a `%` not followed by two hex digits stays.
func percentDecodeBytes(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			h, l := fromHex(s[i+1]), fromHex(s[i+2])
			if h >= 0 && l >= 0 {
				out = append(out, byte(h<<4|l))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return out, nil
}

// decodeURIComponent is the JavaScript global: every %XX run must be valid UTF-8, a malformed sequence is an error.
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", errInvalidFileURL
		}
		h, l := fromHex(s[i+1]), fromHex(s[i+2])
		if h < 0 || l < 0 {
			return "", errInvalidFileURL
		}
		b := byte(h<<4 | l)
		if b < 0x80 {
			out = append(out, b)
			i += 2
			continue
		}
		var n int
		switch {
		case b&0xe0 == 0xc0:
			n = 2
		case b&0xf0 == 0xe0:
			n = 3
		case b&0xf8 == 0xf0:
			n = 4
		default:
			return "", errInvalidFileURL
		}
		seq := []byte{b}
		j := i + 3
		for k := 1; k < n; k++ {
			if j+2 >= len(s) || s[j] != '%' {
				return "", errInvalidFileURL
			}
			h2, l2 := fromHex(s[j+1]), fromHex(s[j+2])
			if h2 < 0 || l2 < 0 {
				return "", errInvalidFileURL
			}
			cb := byte(h2<<4 | l2)
			if cb&0xc0 != 0x80 {
				return "", errInvalidFileURL
			}
			seq = append(seq, cb)
			j += 3
		}
		r, size := utf8.DecodeRune(seq)
		if r == utf8.RuneError || size != n {
			return "", errInvalidFileURL
		}
		out = append(out, seq...)
		i = j - 1
	}
	return string(out), nil
}

// fileURLToPath is Node's url.fileURLToPath(url, { windows }).
func fileURLToPath(url string, windows bool) (string, error) {
	host, pathname, err := parseFileURL(url)
	if err != nil {
		return "", err
	}
	if windows {
		return pathFromURLWin32(host, pathname)
	}
	return pathFromURLPosix(host, pathname)
}

func pathFromURLPosix(host, pathname string) (string, error) {
	if host != "" {
		return "", errInvalidFileURL
	}
	for n := 0; n < len(pathname); n++ {
		if pathname[n] == '%' && n+1 < len(pathname) && pathname[n+1] == '2' && n+2 < len(pathname) && pathname[n+2]|0x20 == 'f' {
			return "", errInvalidFileURL
		}
	}
	return decodeURIComponent(pathname)
}

func pathFromURLWin32(host, pathname string) (string, error) {
	for n := 0; n < len(pathname); n++ {
		if pathname[n] != '%' || n+1 >= len(pathname) {
			continue
		}
		third := byte(0x20)
		if n+2 < len(pathname) {
			third = pathname[n+2] | 0x20
		}
		if (pathname[n+1] == '2' && third == 'f') || (pathname[n+1] == '5' && third == 'c') {
			return "", errInvalidFileURL
		}
	}
	pathname = strings.ReplaceAll(pathname, "/", `\`)
	pathname, err := decodeURIComponent(pathname)
	if err != nil {
		return "", err
	}
	if host != "" {
		return `\\` + host + pathname, nil
	}
	if len(pathname) < 3 {
		return "", errInvalidFileURL
	}
	letter := pathname[1] | 0x20
	if letter < 'a' || letter > 'z' || pathname[2] != ':' {
		return "", errInvalidFileURL
	}
	return pathname[1:], nil
}
