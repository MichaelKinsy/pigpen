package websearch

import (
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// wurl is a URL as the WHATWG URL Standard parses and serializes it, which `new URL(input, base).href` is in
// Node. Defuddle resolves every href, src and srcset entry of a page through it, and reads hostname, origin and
// pathname, so the parse is the standard's basic URL parser (percent-encode sets, dot segments, IPv4 and IPv6
// hosts, IDNA, opaque paths) and not net/url's.
type wurl struct {
	scheme   string
	username string
	password string
	host     *string // nil: no host
	port     int     // -1: none
	path     []string
	opaque   *string // opaque path (cannot-be-a-base URL)
	query    *string
	fragment *string
}

func (u *wurl) isSpecial() bool { return wSpecialPort(u.scheme) != -2 }

// wSpecialPort is the default port of a special scheme, -1 for file, -2 when the scheme is not special.
func wSpecialPort(scheme string) int {
	switch scheme {
	case "http", "ws":
		return 80
	case "https", "wss":
		return 443
	case "ftp":
		return 21
	case "file":
		return -1
	}
	return -2
}

// percent-encode sets
func wInC0(c byte) bool       { return c <= 0x1f || c > 0x7e }
func wInFragment(c byte) bool { return wInC0(c) || c == ' ' || c == '"' || c == '<' || c == '>' || c == '`' }
func wInQuery(c byte) bool {
	return wInC0(c) || c == ' ' || c == '"' || c == '#' || c == '<' || c == '>'
}
func wInSpecialQuery(c byte) bool { return wInQuery(c) || c == '\'' }
func wInPath(c byte) bool {
	return wInQuery(c) || c == '?' || c == '^' || c == '`' || c == '{' || c == '}'
}
func wInUserinfo(c byte) bool {
	return wInPath(c) || c == '/' || c == ':' || c == ';' || c == '=' || c == '@' || (c >= '[' && c <= ']') || c == '|'
}

const wHex = "0123456789ABCDEF"

func wEncode(s string, in func(byte) bool) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if in(s[i]) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if in(c) {
			b.WriteByte('%')
			b.WriteByte(wHex[c>>4])
			b.WriteByte(wHex[c&15])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func wIsHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func wAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// wParse is the basic URL parser with no state override. base may be nil.
func wParse(input string, base *wurl) (*wurl, bool) {
	// Strip leading and trailing C0 control or space, and remove tab and newline.
	input = strings.TrimFunc(input, func(r rune) bool { return r <= 0x20 })
	if strings.ContainsAny(input, "\t\n\r") {
		input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)
	}
	u := &wurl{port: -1}
	i := 0
	n := len(input)
	at := func(k int) int {
		if k >= 0 && k < n {
			return int(input[k])
		}
		return -1
	}
	// scheme
	hasScheme := false
	if n > 0 && wAlpha(input[0]) {
		k := 1
		for k < n && (wAlpha(input[k]) || (input[k] >= '0' && input[k] <= '9') || input[k] == '+' || input[k] == '-' || input[k] == '.') {
			k++
		}
		if k < n && input[k] == ':' {
			u.scheme = strings.ToLower(input[:k])
			i = k + 1
			hasScheme = true
		}
	}
	if !hasScheme {
		if base == nil || (base.opaque != nil && at(0) != '#') {
			return nil, false
		}
		if base.opaque != nil {
			// only a fragment can be set
			c := *base
			c.fragment = nil
			rest := wEncode(input[1:], wInFragment)
			c.fragment = &rest
			return &c, true
		}
		return wParseRelative(u, base, input, 0)
	}
	if u.scheme == "file" {
		return wParseFile(u, base, input, i)
	}
	if u.isSpecial() {
		if base != nil && base.scheme == u.scheme {
			return wParseRelative(u, base, input, i)
		}
		return wParseAuthority(u, input, i)
	}
	// non-special
	if at(i) == '/' {
		if at(i+1) == '/' {
			return wParseAuthority(u, input, i+2)
		}
		return wParsePathStart(u, input, i)
	}
	// opaque path
	end := len(input)
	hashAt := strings.IndexByte(input[i:], '#')
	queryAt := strings.IndexByte(input[i:], '?')
	pathEnd := end
	if queryAt >= 0 {
		pathEnd = i + queryAt
	} else if hashAt >= 0 {
		pathEnd = i + hashAt
	}
	if hashAt >= 0 && queryAt > hashAt {
		pathEnd = i + hashAt
		queryAt = -1
	}
	op := wEncode(input[i:pathEnd], wInC0)
	u.opaque = &op
	return wParseQueryFragment(u, input, pathEnd)
}

// wParseRelative handles a relative reference against base (the "no scheme" and "special relative" states).
func wParseRelative(u *wurl, base *wurl, input string, i int) (*wurl, bool) {
	u.scheme = base.scheme
	at := func(k int) int {
		if k < len(input) {
			return int(input[k])
		}
		return -1
	}
	special := u.isSpecial()
	slash := func(c int) bool { return c == '/' || (special && c == '\\') }
	if base.scheme == "file" {
		return wParseFile(u, base, input, i)
	}
	if slash(at(i)) {
		if slash(at(i + 1)) {
			// authority
			return wParseAuthority(u, input, i+2)
		}
		// path absolute: keep base's authority
		u.username, u.password, u.host, u.port = base.username, base.password, base.host, base.port
		return wParsePathStart(u, input, i)
	}
	u.username, u.password, u.host, u.port = base.username, base.password, base.host, base.port
	u.path = append([]string(nil), base.path...)
	u.query = base.query
	c := at(i)
	switch {
	case c == -1:
		return u, true
	case c == '?':
		u.query = nil
		return wParseQueryFragment(u, input, i)
	case c == '#':
		return wParseQueryFragment(u, input, i)
	}
	u.query = nil
	if len(u.path) > 0 {
		u.path = u.path[:len(u.path)-1]
	}
	return wParsePathSegments(u, input, i)
}

func wParseAuthority(u *wurl, input string, i int) (*wurl, bool) {
	special := u.isSpecial()
	// skip extra slashes for special schemes
	for special && i < len(input) && (input[i] == '/' || input[i] == '\\') {
		i++
	}
	end := len(input)
	for k := i; k < len(input); k++ {
		c := input[k]
		if c == '/' || c == '?' || c == '#' || (special && c == '\\') {
			end = k
			break
		}
	}
	auth := input[i:end]
	hostport := auth
	if at := strings.LastIndexByte(auth, '@'); at >= 0 {
		userinfo := auth[:at]
		hostport = auth[at+1:]
		user, pass, hasPass := strings.Cut(userinfo, ":")
		u.username = wEncode(user, wInUserinfo)
		if hasPass {
			u.password = wEncode(pass, wInUserinfo)
		}
		if hostport == "" {
			return nil, false
		}
	}
	// split host and port
	host := hostport
	portStr := ""
	hasPort := false
	if strings.HasPrefix(hostport, "[") {
		if close := strings.IndexByte(hostport, ']'); close >= 0 {
			host = hostport[:close+1]
			rest := hostport[close+1:]
			if strings.HasPrefix(rest, ":") {
				portStr, hasPort = rest[1:], true
			} else if rest != "" {
				return nil, false
			}
		}
	} else if c := strings.LastIndexByte(hostport, ':'); c >= 0 {
		host, portStr, hasPort = hostport[:c], hostport[c+1:], true
	}
	if host == "" {
		if special {
			return nil, false
		}
		if u.username != "" || u.password != "" || hasPort {
			return nil, false
		}
	}
	h, ok := wParseHost(host, !special)
	if !ok {
		return nil, false
	}
	u.host = &h
	if hasPort && portStr != "" {
		for k := 0; k < len(portStr); k++ {
			if portStr[k] < '0' || portStr[k] > '9' {
				return nil, false
			}
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p > 65535 {
			return nil, false
		}
		if p == wSpecialPort(u.scheme) {
			p = -1
		}
		u.port = p
	}
	return wParsePathStart(u, input, end)
}

// wParsePathStart parses what follows the authority: the path (for special schemes, an empty one is "/"),
// the query and the fragment.
func wParsePathStart(u *wurl, input string, i int) (*wurl, bool) {
	special := u.isSpecial()
	c := -1
	if i < len(input) {
		c = int(input[i])
	}
	if special {
		if c == '/' || c == '\\' {
			i++
		}
		return wParsePathSegments(u, input, i)
	}
	if c == '/' {
		return wParsePathSegments(u, input, i+1)
	}
	if c == '?' || c == '#' || c == -1 {
		return wParseQueryFragment(u, input, i)
	}
	return wParsePathSegments(u, input, i)
}

func wParseFile(u *wurl, base *wurl, input string, i int) (*wurl, bool) {
	u.scheme = "file"
	empty := ""
	u.host = &empty
	at := func(k int) int {
		if k < len(input) {
			return int(input[k])
		}
		return -1
	}
	slash := func(c int) bool { return c == '/' || c == '\\' }
	if slash(at(i)) {
		if slash(at(i + 1)) {
			// file host
			j := i + 2
			end := len(input)
			for k := j; k < len(input); k++ {
				if c := input[k]; c == '/' || c == '\\' || c == '?' || c == '#' {
					end = k
					break
				}
			}
			host := input[j:end]
			if host != "" {
				h, ok := wParseHost(host, false)
				if !ok {
					return nil, false
				}
				if h == "localhost" {
					h = ""
				}
				u.host = &h
			}
			return wParsePathStart(u, input, end)
		}
		if base != nil && base.scheme == "file" {
			u.host = base.host
		}
		return wParsePathSegments(u, input, i+1)
	}
	if base != nil && base.scheme == "file" {
		u.host = base.host
		u.path = append([]string(nil), base.path...)
		u.query = base.query
		c := at(i)
		switch {
		case c == -1:
			return u, true
		case c == '?':
			u.query = nil
			return wParseQueryFragment(u, input, i)
		case c == '#':
			return wParseQueryFragment(u, input, i)
		}
		u.query = nil
		if len(u.path) > 0 {
			u.path = u.path[:len(u.path)-1]
		}
		return wParsePathSegments(u, input, i)
	}
	return wParsePathSegments(u, input, i)
}

func wIsSingleDot(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

func wIsDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

// wParsePathSegments parses path segments from input[i:] up to a query or fragment into u.path.
func wParsePathSegments(u *wurl, input string, i int) (*wurl, bool) {
	special := u.isSpecial()
	end := len(input)
	for k := i; k < len(input); k++ {
		if input[k] == '?' || input[k] == '#' {
			end = k
			break
		}
	}
	rest := input[i:end]
	var segs []string
	cur := 0
	for k := 0; k <= len(rest); k++ {
		if k == len(rest) || rest[k] == '/' || (special && rest[k] == '\\') {
			segs = append(segs, rest[cur:k])
			cur = k + 1
		}
	}
	for idx, seg := range segs {
		last := idx == len(segs)-1
		switch {
		case wIsDoubleDot(seg):
			if len(u.path) > 0 && !(u.scheme == "file" && len(u.path) == 1 && wIsDriveLetter(u.path[0], true)) {
				u.path = u.path[:len(u.path)-1]
			}
			if last {
				u.path = append(u.path, "")
			}
		case wIsSingleDot(seg):
			if last {
				u.path = append(u.path, "")
			}
		default:
			if u.scheme == "file" && len(u.path) == 0 && wIsDriveLetter(seg, false) {
				seg = seg[:1] + ":"
			}
			u.path = append(u.path, wEncode(seg, wInPath))
		}
	}
	return wParseQueryFragment(u, input, end)
}

func wIsDriveLetter(s string, normalizedOnly bool) bool {
	if len(s) != 2 || !wAlpha(s[0]) {
		return false
	}
	return s[1] == ':' || (!normalizedOnly && s[1] == '|')
}

func wParseQueryFragment(u *wurl, input string, i int) (*wurl, bool) {
	if i < len(input) && input[i] == '?' {
		end := len(input)
		if h := strings.IndexByte(input[i:], '#'); h >= 0 {
			end = i + h
		}
		in := wInQuery
		if u.isSpecial() {
			in = wInSpecialQuery
		}
		q := wEncode(input[i+1:end], in)
		u.query = &q
		i = end
	}
	if i < len(input) && input[i] == '#' {
		f := wEncode(input[i+1:], wInFragment)
		u.fragment = &f
	}
	return u, true
}

// wParseHost is the host parser.
func wParseHost(input string, opaque bool) (string, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", false
		}
		addr, err := netip.ParseAddr(input[1 : len(input)-1])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", false
		}
		return "[" + wIPv6String(addr) + "]", true
	}
	if opaque {
		for i := 0; i < len(input); i++ {
			switch input[i] {
			case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
				return "", false
			}
		}
		return wEncode(input, wInC0), true
	}
	domain := wPercentDecode(input)
	if !utf8.ValidString(domain) {
		return "", false
	}
	ascii, err := idna.New(idna.MapForLookup(), idna.BidiRule(), idna.StrictDomainName(false), idna.VerifyDNSLength(false), idna.CheckHyphens(false)).ToASCII(domain)
	if err != nil || ascii == "" {
		return "", false
	}
	for i := 0; i < len(ascii); i++ {
		switch ascii[i] {
		case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|', '%', 0x7f:
			return "", false
		}
		if ascii[i] <= 0x1f {
			return "", false
		}
	}
	if isNumericLastLabel(ascii) {
		v4, ok := parseWhatwgIPv4(ascii)
		if !ok {
			return "", false
		}
		return v4, true
	}
	return ascii, true
}

func wPercentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && wIsHex(s[i+1]) && wIsHex(s[i+2]) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b = append(b, byte(v))
			i += 2
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

// wIPv6String is the standard's IPv6 serializer: lower-case hex, the longest run of zero pieces (two or more)
// compressed, and no IPv4 tail.
func wIPv6String(a netip.Addr) string {
	b := a.As16()
	var pieces [8]uint16
	for i := range 8 {
		pieces[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
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
	ignore0 := false
	for i := 0; i < 8; i++ {
		if ignore0 && pieces[i] == 0 {
			continue
		}
		ignore0 = false
		if bestStart == i {
			if i == 0 {
				sb.WriteString("::")
			} else {
				sb.WriteString(":")
			}
			ignore0 = true
			continue
		}
		sb.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i != 7 {
			sb.WriteByte(':')
		}
	}
	return sb.String()
}

// ---- serialization ---------------------------------------------------------------------------------------------

func (u *wurl) pathname() string {
	if u.opaque != nil {
		return *u.opaque
	}
	if len(u.path) == 0 {
		return ""
	}
	return "/" + strings.Join(u.path, "/")
}

func (u *wurl) hostname() string {
	if u.host == nil {
		return ""
	}
	return *u.host
}

func (u *wurl) hostPort() string {
	if u.host == nil {
		return ""
	}
	if u.port >= 0 {
		return *u.host + ":" + strconv.Itoa(u.port)
	}
	return *u.host
}

func (u *wurl) search() string {
	if u.query == nil || *u.query == "" {
		return ""
	}
	return "?" + *u.query
}

func (u *wurl) hash() string {
	if u.fragment == nil || *u.fragment == "" {
		return ""
	}
	return "#" + *u.fragment
}

func (u *wurl) href() string {
	var b strings.Builder
	b.WriteString(u.scheme)
	b.WriteByte(':')
	if u.host != nil {
		b.WriteString("//")
		if u.username != "" || u.password != "" {
			b.WriteString(u.username)
			if u.password != "" {
				b.WriteByte(':')
				b.WriteString(u.password)
			}
			b.WriteByte('@')
		}
		b.WriteString(u.hostPort())
	} else if u.opaque == nil && len(u.path) > 1 && u.path[0] == "" {
		b.WriteString("/.")
	}
	b.WriteString(u.pathname())
	if u.query != nil {
		b.WriteByte('?')
		b.WriteString(*u.query)
	}
	if u.fragment != nil {
		b.WriteByte('#')
		b.WriteString(*u.fragment)
	}
	return b.String()
}

// origin is URL.origin: "null" for schemes that have none.
func (u *wurl) origin() string {
	switch u.scheme {
	case "http", "https", "ws", "wss", "ftp":
		return u.scheme + "://" + u.hostPort()
	}
	return "null"
}

// newURL is `new URL(input, base)`; ok is false where it throws.
func newURL(input string, base string) (*wurl, bool) {
	if base == "" {
		return wParse(input, nil)
	}
	b, ok := wParse(base, nil)
	if !ok {
		return nil, false
	}
	return wParse(input, b)
}
