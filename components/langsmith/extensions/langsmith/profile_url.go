package langsmith

// A WHATWG URL parser for the strings the profile code hands to `new URL(...)` and `fetch(...)`: scheme, credentials,
// host (domain, IPv4, IPv6), port, path with dot segments, query, fragment and the percent-encode sets. Non-ASCII
// hosts are lower-cased and punycoded without the UTS #46 mapping tables (named limitation, port/lane-profile.md).

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type whatwgURL struct {
	scheme             string
	username, password string
	host               string
	hasHost            bool
	port               int // -1: none
	path               string
	opaque             bool
	query              string // with the leading `?`
	hasQuery           bool
	frag               string // with the leading `#`
	hasFrag            bool
}

func (u *whatwgURL) special() bool { return isSpecialScheme(u.scheme) }

func isSpecialScheme(s string) bool {
	switch s {
	case "http", "https", "ws", "wss", "ftp", "file":
		return true
	}
	return false
}

func defaultPort(scheme string) int {
	switch scheme {
	case "http", "ws":
		return 80
	case "https", "wss":
		return 443
	case "ftp":
		return 21
	}
	return -1
}

// protocol is URL.protocol.
func (u *whatwgURL) protocol() string { return u.scheme + ":" }

// hostPort is URL.host.
func (u *whatwgURL) hostPort() string {
	if !u.hasHost {
		return ""
	}
	if u.port >= 0 {
		return u.host + ":" + strconv.Itoa(u.port)
	}
	return u.host
}

// origin is URL.origin.
func (u *whatwgURL) origin() string {
	switch u.scheme {
	case "http", "https", "ws", "wss", "ftp":
		return u.scheme + "://" + u.hostPort()
	}
	return "null"
}

// href is URL.href.
func (u *whatwgURL) href() string {
	var sb strings.Builder
	sb.WriteString(u.scheme)
	sb.WriteByte(':')
	if u.hasHost {
		sb.WriteString("//")
		if u.username != "" || u.password != "" {
			sb.WriteString(u.username)
			if u.password != "" {
				sb.WriteByte(':')
				sb.WriteString(u.password)
			}
			sb.WriteByte('@')
		}
		sb.WriteString(u.hostPort())
	} else if !u.opaque && strings.HasPrefix(u.path, "//") {
		sb.WriteString("/.")
	}
	sb.WriteString(u.path)
	if u.hasQuery {
		sb.WriteString(u.query)
	}
	if u.hasFrag {
		sb.WriteString(u.frag)
	}
	return sb.String()
}

func inC0Set(b byte) bool { return b <= 0x1F || b > 0x7E }

func inFragmentSet(b byte) bool {
	return inC0Set(b) || b == ' ' || b == '"' || b == '<' || b == '>' || b == '`'
}

func inQuerySet(b byte) bool {
	return inC0Set(b) || b == ' ' || b == '"' || b == '#' || b == '<' || b == '>'
}

func inSpecialQuerySet(b byte) bool { return inQuerySet(b) || b == '\'' }

func pfInPathSet(b byte) bool {
	return inQuerySet(b) || b == '?' || b == '^' || b == '`' || b == '{' || b == '}'
}

func inUserinfoSet(b byte) bool {
	return pfInPathSet(b) || b == '/' || b == ':' || b == ';' || b == '=' || b == '@' || (b >= '[' && b <= '^') || b == '|'
}

func pctEncodeInto(sb *strings.Builder, s string, in func(byte) bool) {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if in(c) {
			sb.WriteByte('%')
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&15])
		} else {
			sb.WriteByte(c)
		}
	}
}

func pctEncode(s string, in func(byte) bool) string {
	var sb strings.Builder
	pctEncodeInto(&sb, s, in)
	return sb.String()
}

// parseWhatwgURL is `new URL(input)` without a base: ok is false where it throws.
func parseWhatwgURL(input string) (*whatwgURL, bool) {
	s := jsValidUTF8(input)
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	if strings.ContainsAny(s, "\t\n\r") {
		s = strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, s)
	}
	i := 0
	if i >= len(s) || !pfIsAlpha(s[i]) {
		return nil, false
	}
	for i < len(s) && (pfIsAlpha(s[i]) || (s[i] >= '0' && s[i] <= '9') || s[i] == '+' || s[i] == '-' || s[i] == '.') {
		i++
	}
	if i >= len(s) || s[i] != ':' {
		return nil, false
	}
	u := &whatwgURL{scheme: strings.ToLower(s[:i]), port: -1}
	rest := s[i+1:]
	if j := strings.IndexByte(rest, '#'); j >= 0 {
		u.frag, u.hasFrag = "#"+pctEncode(rest[j+1:], inFragmentSet), true
		rest = rest[:j]
	}
	special := u.special()
	if j := strings.IndexByte(rest, '?'); j >= 0 {
		q := rest[j+1:]
		set := inQuerySet
		if special {
			set = inSpecialQuerySet
		}
		u.query, u.hasQuery = "?"+pctEncode(q, set), true
		rest = rest[:j]
	}
	isSlash := func(b byte) bool { return b == '/' || (special && b == '\\') }

	var authority, pathPart string
	hasAuthority := false
	switch {
	case u.scheme == "file":
		u.hasHost = true
		if len(rest) >= 2 && isSlash(rest[0]) && isSlash(rest[1]) {
			rest = rest[2:]
			end := 0
			for end < len(rest) && !isSlash(rest[end]) {
				end++
			}
			authority, pathPart, hasAuthority = rest[:end], rest[end:], true
		} else {
			pathPart = rest
		}
	case special:
		for len(rest) > 0 && isSlash(rest[0]) {
			rest = rest[1:]
		}
		end := 0
		for end < len(rest) && !isSlash(rest[end]) {
			end++
		}
		authority, pathPart, hasAuthority = rest[:end], rest[end:], true
	case strings.HasPrefix(rest, "//"):
		rest = rest[2:]
		end := strings.IndexByte(rest, '/')
		if end < 0 {
			end = len(rest)
		}
		authority, pathPart, hasAuthority = rest[:end], rest[end:], true
	case strings.HasPrefix(rest, "/"):
		pathPart = rest
	default:
		u.opaque = true
		u.path = pctEncode(rest, inC0Set)
		return u, true
	}

	if hasAuthority {
		hostport := authority
		// A file URL has no credentials: its `@` stays in the host, which the host parser then refuses.
		if at := strings.LastIndexByte(authority, '@'); at >= 0 && u.scheme != "file" {
			hostport = authority[at+1:]
			if hostport == "" {
				return nil, false
			}
			userinfo := authority[:at]
			user, pass, _ := strings.Cut(userinfo, ":")
			u.username = pctEncode(user, inUserinfoSet)
			u.password = pctEncode(pass, inUserinfoSet)
		}
		hostStr, portStr, hasPort := splitHostPort(hostport)
		if u.scheme == "file" && hasPort {
			return nil, false // a file URL has no port
		}
		if hostStr == "" {
			if special && u.scheme != "file" {
				return nil, false
			}
			if hasPort || u.username != "" || u.password != "" {
				return nil, false
			}
		}
		if hostStr != "" || u.scheme != "file" {
			h, ok := parseURLHost(hostStr, special)
			if !ok {
				return nil, false
			}
			u.host = h
			if u.scheme == "file" && h == "localhost" {
				u.host = ""
			}
		}
		u.hasHost = true
		if hasPort && portStr != "" {
			n := 0
			for k := range len(portStr) {
				c := portStr[k]
				if c < '0' || c > '9' {
					return nil, false
				}
				n = n*10 + int(c-'0')
				if n > 65535 {
					return nil, false
				}
			}
			if n != defaultPort(u.scheme) {
				u.port = n
			}
		}
	}

	u.path = serializeURLPath(pathPart, special, special)
	return u, true
}

func pfIsAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// splitHostPort splits at the first colon outside square brackets; a non-digit in the port is reported by the caller.
func splitHostPort(hp string) (host, port string, hasPort bool) {
	inBracket := false
	for i := 0; i < len(hp); i++ {
		switch hp[i] {
		case '[':
			inBracket = true
		case ']':
			inBracket = false
		case ':':
			if !inBracket {
				return hp[:i], hp[i+1:], true
			}
		}
	}
	return hp, "", false
}

func isDoubleDotSegment(seg string) bool {
	switch strings.ToLower(seg) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func isSingleDotSegment(seg string) bool {
	switch strings.ToLower(seg) {
	case ".", "%2e":
		return true
	}
	return false
}

// serializeURLPath runs the path state: the dot segments are resolved and every other segment percent-encoded. A path
// of a URL with a host (or a special one) always starts with `/`; the path of a hostless non-special URL may be empty.
func serializeURLPath(p string, special, forceSlash bool) string {
	if p == "" {
		if forceSlash {
			return "/"
		}
		return ""
	}
	if p[0] == '/' || (special && p[0] == '\\') {
		p = p[1:]
	}
	var segs []string
	start := 0
	for start <= len(p) {
		end := start
		for end < len(p) && !(p[end] == '/' || (special && p[end] == '\\')) {
			end++
		}
		seg := p[start:end]
		last := end >= len(p)
		switch {
		case isDoubleDotSegment(seg):
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
			if last {
				segs = append(segs, "")
			}
		case isSingleDotSegment(seg):
			if last {
				segs = append(segs, "")
			}
		default:
			segs = append(segs, pctEncode(seg, pfInPathSet))
		}
		start = end + 1
	}
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteByte('/')
		sb.WriteString(s)
	}
	return sb.String()
}

func pfForbiddenHost(b byte) bool {
	switch b {
	case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

// parseURLHost is the host parser; the result is the serialized host.
func parseURLHost(input string, special bool) (string, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", false
		}
		return parseIPv6Host(input[1 : len(input)-1])
	}
	if !special {
		for i := 0; i < len(input); i++ {
			if pfForbiddenHost(input[i]) {
				return "", false
			}
		}
		return pctEncode(input, inC0Set), true
	}
	decoded := decodeUTF8Replacing([]byte(pfPercentDecode(input)))
	ascii := pfDomainToASCII(decoded)
	if ascii == "" {
		return "", false
	}
	for i := 0; i < len(ascii); i++ {
		c := ascii[i]
		if pfForbiddenHost(c) || c <= 0x1F || c == '%' || c == 0x7F {
			return "", false
		}
	}
	if endsInANumber(ascii) {
		return parseIPv4Host(ascii)
	}
	return ascii, true
}

func pfPercentDecode(s string) string {
	if strings.IndexByte(s, '%') < 0 {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			h1, h2 := pfHexVal(s[i+1]), pfHexVal(s[i+2])
			if h1 >= 0 && h2 >= 0 {
				out = append(out, byte(h1<<4|h2))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

// domainToASCII is a reduced UTS #46 ToASCII: full-width dots and the soft hyphen are mapped, letters lower-cased,
// non-ASCII labels punycoded.
func pfDomainToASCII(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case 0x3002, 0xFF0E, 0xFF61:
			r = '.'
		case 0xAD, 0x200B, 0x2060, 0xFEFF:
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	labels := strings.Split(sb.String(), ".")
	for i, l := range labels {
		nonASCII := false
		for j := 0; j < len(l); j++ {
			if l[j] >= utf8.RuneSelf {
				nonASCII = true
				break
			}
		}
		if !nonASCII {
			continue
		}
		labels[i] = "xn--" + pfPunycode(l)
	}
	return strings.Join(labels, ".")
}

// pfPunycode is RFC 3492 section 6.3 (encoding).
func pfPunycode(s string) string {
	const (
		base        = 36
		tMin, tMax  = 1, 26
		skew, damp  = 38, 700
		initialBias = 72
		initialN    = 128
	)
	runes := []rune(s)
	var out []byte
	for _, r := range runes {
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
	adapt := func(delta, numPoints int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / numPoints
		k := 0
		for delta > ((base-tMin)*tMax)/2 {
			delta /= base - tMin
			k += base
		}
		return k + (base-tMin+1)*delta/(delta+skew)
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	for h < len(runes) {
		m := rune(0x7FFFFFFF)
		for _, r := range runes {
			if r >= n && r < m {
				m = r
			}
		}
		delta += int(m-n) * (h + 1)
		n = m
		for _, r := range runes {
			if r < n {
				delta++
			}
			if r == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tMin {
						t = tMin
					} else if t > tMax {
						t = tMax
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
	return string(out)
}

// parseIPv4Number is the IPv4 number parser; ok is false where it fails.
func pfIPv4Number(s string) (uint64, bool) {
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
	var n uint64
	for i := 0; i < len(s); i++ {
		d := pfHexVal(s[i])
		if d < 0 || d >= radix {
			return 0, false
		}
		n = n*uint64(radix) + uint64(d)
		if n > 1<<40 {
			n = 1 << 40 // saturate: any value this large is out of range
		}
	}
	return n, true
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
	if last != "" {
		allDigits := true
		for i := 0; i < len(last); i++ {
			if last[i] < '0' || last[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	_, ok := pfIPv4Number(last)
	return ok && (strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X"))
}

func parseIPv4Host(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := pfIPv4Number(p)
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
	limit := uint64(1) << (8 * (5 - len(nums)))
	if last >= limit {
		return "", false
	}
	ipv4 := last
	for i, n := range nums[:len(nums)-1] {
		ipv4 += n << (8 * (3 - i))
	}
	return strconv.Itoa(int(ipv4>>24&255)) + "." + strconv.Itoa(int(ipv4>>16&255)) + "." + strconv.Itoa(int(ipv4>>8&255)) + "." + strconv.Itoa(int(ipv4&255)), true
}

func parseIPv6Host(inner string) (string, bool) {
	if strings.ContainsAny(inner, "%") {
		return "", false
	}
	addr, err := netip.ParseAddr(inner)
	if err != nil || !addr.Is6() {
		return "", false
	}
	a := addr.As16()
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(a[2*i])<<8 | uint16(a[2*i+1])
	}
	// The longest run (length > 1) of zero pieces, the first on a tie, is compressed.
	bestStart, bestLen := -1, 0
	for i := 0; i < 8; {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && pieces[j] == 0 {
			j++
		}
		if j-i > bestLen {
			bestStart, bestLen = i, j-i
		}
		i = j
	}
	if bestLen < 2 {
		bestStart = -1
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < 8; i++ {
		if i == bestStart {
			if i == 0 {
				sb.WriteByte(':')
			}
			sb.WriteByte(':')
			i += bestLen - 1
			continue
		}
		sb.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i != 7 {
			sb.WriteByte(':')
		}
	}
	sb.WriteByte(']')
	return sb.String(), true
}

var errFetchURL = errors.New("fetch failed")

// fetchRequestURL is the URL `fetch(rawURL)` requests: the href without its fragment. fetch rejects a URL that does not
// parse, a scheme other than http(s) and a URL with credentials.
func fetchRequestURL(rawURL string) (string, error) {
	u, ok := parseWhatwgURL(rawURL)
	if !ok {
		return "", errFetchURL
	}
	if u.scheme != "http" && u.scheme != "https" {
		return "", errFetchURL
	}
	if u.username != "" || u.password != "" {
		return "", errFetchURL
	}
	u.hasFrag = false
	return u.href(), nil
}
