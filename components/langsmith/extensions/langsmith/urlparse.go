package langsmith

// The part of the WHATWG URL parser `new URL(remote)` that metadata.ts:48-54 reads: `parsed.host` and `parsed.pathname`.
// Special schemes (http, https, ws, wss, ftp, file) and opaque-host schemes (ssh, git, ...) are handled; a failure is
// the TypeError `new URL` throws. Divergence (lane-ext.md): host names with non-ASCII letters are lowercased and
// punycode-encoded without the full UTS #46 mapping.

import (
	"net/netip"
	"strconv"
	"strings"
)

func isASCIIAlpha(c byte) bool { return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') }

func isASCIIDigit(c byte) bool { return '0' <= c && c <= '9' }

var specialPorts = map[string]int{"ftp": 21, "file": -1, "http": 80, "https": 443, "ws": 80, "wss": 443}

// parseURLHostPath returns URL.host and URL.pathname of input; ok is false when the URL parser fails.
func parseURLHostPath(input string) (host, pathname string, ok bool) {
	// Leading and trailing C0 controls and spaces are stripped, tabs and newlines removed.
	input = strings.TrimFunc(input, func(r rune) bool { return r <= 0x20 })
	if strings.ContainsAny(input, "\t\n\r") {
		input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)
	}
	// scheme
	i := 0
	if i >= len(input) || !isASCIIAlpha(input[0]) {
		return "", "", false
	}
	for i < len(input) && (isASCIIAlpha(input[i]) || isASCIIDigit(input[i]) || input[i] == '+' || input[i] == '-' || input[i] == '.') {
		i++
	}
	if i >= len(input) || input[i] != ':' {
		return "", "", false
	}
	scheme := strings.ToLower(input[:i])
	rest := input[i+1:]
	defPort, special := specialPorts[scheme]
	if !special {
		defPort = -1
	}
	isSlash := func(c byte) bool { return c == '/' || (special && c == '\\') }

	hasAuthority := false
	switch {
	case scheme == "file":
		if len(rest) >= 2 && isSlash(rest[0]) && isSlash(rest[1]) {
			rest = rest[2:]
			hasAuthority = true
		}
	case special:
		for len(rest) > 0 && isSlash(rest[0]) {
			rest = rest[1:]
		}
		hasAuthority = true
	default:
		if len(rest) >= 2 && rest[0] == '/' && rest[1] == '/' {
			rest = rest[2:]
			hasAuthority = true
		}
	}
	if !hasAuthority {
		// No host: `host` is "" (an opaque or host-less path), which the caller treats as failure.
		return "", "", true
	}
	end := len(rest)
	for j := 0; j < len(rest); j++ {
		if rest[j] == '/' || rest[j] == '?' || rest[j] == '#' || (special && rest[j] == '\\') {
			end = j
			break
		}
	}
	authority, tail := rest[:end], rest[end:]
	if scheme == "file" {
		// File host state: no userinfo and no port; a Windows drive letter is path, not host.
		switch {
		case authority == "":
			return "", parsePath(tail, true, true), true
		case isDriveLetter(authority, true):
			return "", parsePath("/"+authority+tail, true, true), true
		}
		h, good := parseHost(authority, false)
		if !good {
			return "", "", false
		}
		if h == "localhost" {
			h = ""
		}
		return h, parsePath(tail, true, true), true
	}
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
		if authority == "" {
			return "", "", false
		}
	}
	hostText, portText := authority, ""
	hasPort := false
	inBracket := false
	for j := 0; j < len(authority); j++ {
		switch authority[j] {
		case '[':
			inBracket = true
		case ']':
			inBracket = false
		case ':':
			if !inBracket {
				hostText, portText, hasPort = authority[:j], authority[j+1:], true
				j = len(authority)
			}
		}
	}
	if hostText == "" && special && scheme != "file" {
		return "", "", false
	}
	if hasPort && hostText == "" {
		return "", "", false
	}
	h, good := parseHost(hostText, !special)
	if !good {
		return "", "", false
	}
	port := -1
	if portText != "" {
		for k := 0; k < len(portText); k++ {
			if !isASCIIDigit(portText[k]) {
				return "", "", false
			}
		}
		digits := strings.TrimLeft(portText, "0")
		n := 0
		if digits != "" {
			var err error
			if n, err = strconv.Atoi(digits); err != nil || n > 65535 {
				return "", "", false
			}
		}
		port = n
		if port == defPort {
			port = -1
		}
	}
	host = h
	if port >= 0 {
		host += ":" + strconv.Itoa(port)
	}
	pathname = parsePath(tail, special, scheme == "file")
	return host, pathname, true
}

// parseHost is the host parser (opaque: non-special schemes).
func parseHost(s string, opaque bool) (string, bool) {
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return "", false
		}
		a, err := netip.ParseAddr(s[1 : len(s)-1])
		if err != nil || !a.Is6() || a.Zone() != "" {
			return "", false
		}
		return "[" + serializeIPv6(a.As16()) + "]", true
	}
	if opaque {
		for k := 0; k < len(s); k++ {
			if isForbiddenHostByte(s[k]) {
				return "", false
			}
		}
		return percentEncode(s, func(c byte) bool { return c <= 0x1F || c > 0x7E }), true
	}
	dec := percentDecode(s)
	ascii, good := domainToASCII(dec)
	if !good || ascii == "" {
		return "", false
	}
	for k := 0; k < len(ascii); k++ {
		if isForbiddenHostByte(ascii[k]) || ascii[k] == '%' || ascii[k] <= 0x1F || ascii[k] == 0x7F {
			return "", false
		}
	}
	if endsInNumber(ascii) {
		return parseIPv4(ascii)
	}
	return ascii, true
}

func isForbiddenHostByte(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	b := make([]byte, 0, len(s))
	for k := 0; k < len(s); k++ {
		if s[k] == '%' && k+2 < len(s)+0 && isHex(s[k+1]) && isHex(s[k+2]) {
			v, _ := strconv.ParseUint(s[k+1:k+3], 16, 8)
			b = append(b, byte(v))
			k += 2
			continue
		}
		b = append(b, s[k])
	}
	return string(b)
}

func isHex(c byte) bool {
	return isASCIIDigit(c) || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

const upperHex = "0123456789ABCDEF"

func percentEncode(s string, inSet func(byte) bool) string {
	var b strings.Builder
	for k := 0; k < len(s); k++ {
		if c := s[k]; inSet(c) {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&15])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// domainToASCII lowercases and punycode-encodes each label.
func domainToASCII(s string) (string, bool) {
	ascii := true
	for k := 0; k < len(s); k++ {
		if s[k] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s), true
	}
	labels := strings.Split(jsToLowerCase(s), ".")
	for i, l := range labels {
		na := false
		for _, r := range l {
			if r >= 0x80 {
				na = true
			}
		}
		if na {
			p, ok := punycodeEncode(l)
			if !ok {
				return "", false
			}
			labels[i] = "xn--" + p
		}
	}
	return strings.Join(labels, "."), true
}

// punycodeEncode is RFC 3492 encode.
func punycodeEncode(s string) (string, bool) {
	const base, tmin, tmax, skew, damp, initialBias, initialN = 36, 1, 26, 38, 700, 72, 128
	rs := []rune(s)
	var out []byte
	for _, r := range rs {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := rune(initialN), 0, initialBias
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
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
	for h < len(rs) {
		m := rune(0x7FFFFFFF)
		for _, r := range rs {
			if r >= n && r < m {
				m = r
			}
		}
		delta += int(m-n) * (h + 1)
		n = m
		for _, r := range rs {
			if r < n {
				delta++
			}
			if r == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tmin {
						t = tmin
					} else if t > tmax {
						t = tmax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out), true
}

// endsInNumber is the URL spec's "ends in a number checker".
func endsInNumber(s string) bool {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" {
		all := true
		for k := 0; k < len(last); k++ {
			if !isASCIIDigit(last[k]) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	_, ok := parseIPv4Number(last)
	return ok
}

func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, radix = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, radix = s[1:], 8
	}
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(s, radix, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 1 << 62, true
		}
		return 0, false
	}
	return n, true
}

func parseIPv4(s string) (string, bool) {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := parseIPv4Number(p)
		if !ok {
			return "", false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return "", false
		}
	}
	last := nums[len(nums)-1]
	limit := uint64(1) << (8 * uint(5-len(nums)))
	if last >= limit {
		return "", false
	}
	ipv4 := last
	for i, n := range nums[:len(nums)-1] {
		ipv4 += n << (8 * uint(3-i))
	}
	return strconv.Itoa(int(ipv4>>24&255)) + "." + strconv.Itoa(int(ipv4>>16&255)) + "." + strconv.Itoa(int(ipv4>>8&255)) + "." + strconv.Itoa(int(ipv4&255)), true
}

// serializeIPv6 is the URL spec's IPv6 serializer: the first longest run of two or more zero pieces becomes `::`.
func serializeIPv6(a [16]byte) string {
	var p [8]uint16
	for i := range p {
		p[i] = uint16(a[2*i])<<8 | uint16(a[2*i+1])
	}
	best, bestLen := -1, 1
	for i := 0; i < 8; {
		if p[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && p[j] == 0 {
			j++
		}
		if j-i > bestLen {
			best, bestLen = i, j-i
		}
		i = j
	}
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == best {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			i += bestLen - 1
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(p[i]), 16))
		if i != 7 {
			b.WriteByte(':')
		}
	}
	return b.String()
}

// parsePath is the path state: tail starts at the first `/` (or `\` for special schemes), `?` or `#`, or is empty.
func parsePath(tail string, special, file bool) string {
	end := len(tail)
	for k := 0; k < len(tail); k++ {
		if tail[k] == '?' || tail[k] == '#' {
			end = k
			break
		}
	}
	tail = tail[:end]
	if tail == "" {
		if special {
			return "/"
		}
		return ""
	}
	if tail[0] == '/' || (special && tail[0] == '\\') {
		tail = tail[1:]
	}
	var segs []string
	rem := tail
	for {
		cut := len(rem)
		for k := 0; k < len(rem); k++ {
			if rem[k] == '/' || (special && rem[k] == '\\') {
				cut = k
				break
			}
		}
		seg := percentEncode(rem[:cut], inPathSet)
		last := cut == len(rem)
		switch lower := strings.ToLower(seg); {
		case lower == ".." || lower == ".%2e" || lower == "%2e." || lower == "%2e%2e":
			if len(segs) > 0 && !(file && len(segs) == 1 && isDriveLetter(segs[0], false)) {
				segs = segs[:len(segs)-1]
			}
			if last {
				segs = append(segs, "")
			}
		case lower == "." || lower == "%2e":
			if last {
				segs = append(segs, "")
			}
		default:
			if file && len(segs) == 0 && isDriveLetter(seg, true) {
				seg = seg[:1] + ":"
			}
			segs = append(segs, seg)
		}
		if last {
			break
		}
		rem = rem[cut+1:]
	}
	return "/" + strings.Join(segs, "/")
}

// inPathSet is the path percent-encode set.
func inPathSet(c byte) bool {
	if c <= 0x1F || c > 0x7E {
		return true
	}
	switch c {
	case ' ', '"', '#', '<', '>', '?', '^', '`', '{', '}':
		return true
	}
	return false
}

// isDriveLetter is a Windows drive letter: an ASCII letter and `:` (or `|` when pipe allows it), nothing else.
func isDriveLetter(s string, pipe bool) bool {
	return len(s) == 2 && isASCIIAlpha(s[0]) && (s[1] == ':' || (pipe && s[1] == '|'))
}
