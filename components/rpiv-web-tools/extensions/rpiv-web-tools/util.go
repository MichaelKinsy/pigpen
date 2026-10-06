package rpiv_web_tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

// kv and ordered build a JSON object whose keys keep their order, as a JavaScript object literal does.
type kv struct {
	K string
	V any
}
type ordered []kv

func (o ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := marshalJS(e.K)
		v, err := marshalJS(e.V)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalJS is JSON.stringify: compact, with no HTML escaping.
func marshalJS(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}

// encodeURIComponent is the JavaScript function: everything but A-Z a-z 0-9 - _ . ! ~ * ' ( ) is escaped.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// jsURL is the part of a WHATWG URL the extension reads. Hostname is the host as `new URL()` serializes it
// (IPv6 in brackets); Href is the URL with that host, which is what a request goes to.
type jsURL struct{ Scheme, Host, Hostname, Href string }

var schemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*):`)

// parseJSURL is `new URL(raw)` for the cases that matter: it needs a scheme, and an http(s) URL needs a host, which
// is parsed as the WHATWG URL Standard's host parser parses it (domain to ASCII, the IPv4 number forms such as
// `127.1`, `0x7f.1` and `2130706433`, IPv6 compression), so a check of the host sees the address the request goes to.
// Any other scheme parses (the callers then refuse it by name).
func parseJSURL(raw string) (*jsURL, error) {
	raw = jsTrim(raw)
	m := schemeRe.FindStringSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("Invalid URL")
	}
	scheme := strings.ToLower(m[1])
	if scheme != "http" && scheme != "https" {
		return &jsURL{Scheme: scheme}, nil
	}
	rest := strings.TrimLeft(raw[len(m[0]):], "/\\")
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("Invalid URL")
	}
	host, err := whatwgHost(u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("Invalid URL")
	}
	if port := u.Port(); port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	return &jsURL{Scheme: scheme, Host: u.Host, Hostname: host, Href: u.String()}, nil
}

// idnaProfile is UTS #46 processing as the URL Standard's "domain to ASCII" asks for it (non-transitional, no
// STD3 rules, no hyphen or length checks).
var idnaProfile = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.BidiRule(), idna.CheckJoiners(true),
	idna.CheckHyphens(false), idna.StrictDomainName(false), idna.VerifyDNSLength(false))

// whatwgHost parses the host of an http(s) URL (as Go's url.Parse gives it, without brackets) into its WHATWG
// serialization. upstream: Node's URL class; https://url.spec.whatwg.org/#host-parsing.
func whatwgHost(h string) (string, error) {
	if strings.Contains(h, ":") {
		a, err := netip.ParseAddr(h)
		if err != nil || !a.Is6() || a.Zone() != "" {
			return "", fmt.Errorf("invalid IPv6 address")
		}
		return "[" + serializeIPv6(a.As16()) + "]", nil
	}
	ascii := h
	for i := 0; i < len(h); i++ {
		if h[i] >= 0x80 {
			var err error
			if ascii, err = idnaProfile.ToASCII(h); err != nil {
				return "", err
			}
			break
		}
	}
	ascii = strings.ToLower(ascii)
	if ascii == "" || strings.ContainsAny(ascii, " #%/:<>?@[\\]^|\x00\t\n\r") {
		return "", fmt.Errorf("forbidden host code point")
	}
	if !endsInANumber(ascii) {
		return ascii, nil
	}
	v4, ok := parseIPv4(ascii)
	if !ok {
		return "", fmt.Errorf("invalid IPv4 address")
	}
	return fmt.Sprintf("%d.%d.%d.%d", v4>>24, v4>>16&0xff, v4>>8&0xff, v4&0xff), nil
}

// endsInANumber is the URL Standard's "ends in a number checker".
func endsInANumber(host string) bool {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number is the URL Standard's IPv4 number parser: 0x hexadecimal, a leading 0 octal, else decimal.
// A value too large for 32 bits reports math.MaxUint64, which every caller refuses.
func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	radix := uint64(10)
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, radix = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, radix = s[1:], 8
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		var d uint64
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, false
		}
		if d >= radix {
			return 0, false
		}
		if n > 1<<32 {
			n = math.MaxUint64 // already too large; keep scanning for an invalid digit
			continue
		}
		n = n*radix + d
	}
	return n, true
}

// parseIPv4 is the URL Standard's IPv4 parser.
func parseIPv4(host string) (uint32, bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return 0, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := parseIPv4Number(p)
		if !ok {
			return 0, false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return 0, false
		}
	}
	last := nums[len(nums)-1]
	if last >= uint64(1)<<(8*(5-len(nums))) {
		return 0, false
	}
	v := last
	for i, n := range nums[:len(nums)-1] {
		v += n << (8 * (3 - i))
	}
	return uint32(v), true
}

// serializeIPv6 is the URL Standard's IPv6 serializer: lowercase hexadecimal pieces, the first longest run of two
// or more zero pieces compressed to "::", no embedded IPv4 form.
func serializeIPv6(b [16]byte) string {
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	compress, best := -1, 1
	for i := 0; i < 8; {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && pieces[j] == 0 {
			j++
		}
		if j-i > best {
			compress, best = i, j-i
		}
		i = j
	}
	var out strings.Builder
	ignore0 := false
	for i, p := range pieces {
		if ignore0 && p == 0 {
			continue
		}
		ignore0 = false
		if compress == i {
			if i == 0 {
				out.WriteString("::")
			} else {
				out.WriteString(":")
			}
			ignore0 = true
			continue
		}
		fmt.Fprintf(&out, "%x", p)
		if i != 7 {
			out.WriteString(":")
		}
	}
	return out.String()
}
