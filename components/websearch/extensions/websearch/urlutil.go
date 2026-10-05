package websearch

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ParseURL parses a URL the way the WHATWG URL constructor (`new URL`) does for the
// http(s) cases this extension needs: hosts are lowercased, IPv4 hosts written as one
// number, hex or octal parts (2130706433, 0x7f.0.0.1, 0177.0.0.1, 127.1) become dotted
// decimal, the scheme's default port is dropped and an empty path becomes "/". SSRF checks
// depend on the canonical form, so a hostname that is really a number must not survive.
func ParseURL(raw string, base *url.URL) (*url.URL, error) {
	raw = strings.Trim(raw, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\v\f\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	var u *url.URL
	var err error
	if base != nil {
		u, err = base.Parse(raw)
	} else {
		u, err = url.Parse(raw)
		if err == nil && u.Scheme == "" {
			err = errors.New("missing scheme")
		}
	}
	if err != nil {
		return nil, errors.New("Invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return u, nil
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("Invalid URL")
	}
	host = strings.ToLower(host)
	if strings.Contains(u.Host, "[") {
		addr, err := netip.ParseAddr(host)
		if err != nil || addr.Zone() != "" {
			return nil, errors.New("Invalid URL")
		}
		host = "[" + addr.String() + "]"
	} else if isNumericLastLabel(host) {
		v4, ok := parseWhatwgIPv4(host)
		if !ok {
			return nil, errors.New("Invalid URL")
		}
		host = v4
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	if u.Path == "" && u.Opaque == "" {
		u.Path = "/"
	}
	return u, nil
}

// JSHostname is `URL.hostname`: no port, IPv6 addresses keep their brackets.
func JSHostname(u *url.URL) string {
	h := u.Hostname()
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

// Origin is `URL.origin` for http(s) URLs.
func Origin(u *url.URL) string { return u.Scheme + "://" + u.Host }

func isNumericLastLabel(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if last == "" && len(labels) > 1 {
		last = labels[len(labels)-2]
	}
	if last == "" {
		return false
	}
	if _, ok := parseIPv4Number(last); ok {
		return true
	}
	allDigits := true
	for _, r := range last {
		if r < '0' || r > '9' {
			allDigits = false
		}
	}
	return allDigits
}

func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
		if s == "" {
			return 0, true
		}
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}
	n, err := strconv.ParseUint(s, base, 64)
	return n, err == nil
}

func parseWhatwgIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
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

// normalizeHostname lowercases, drops IPv6 brackets and a trailing dot.
func normalizeHostname(h string) string {
	h = strings.ToLower(h)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return strings.TrimSuffix(h, ".")
}

// ipVersion is `net.isIP`: 4, 6 or 0.
func ipVersion(s string) int {
	if isStrictIPv4(s) {
		return 4
	}
	if strings.Contains(s, ":") {
		if a, err := netip.ParseAddr(s); err == nil && a.Zone() == "" && a.Is6() {
			return 6
		}
	}
	return 0
}

func isStrictIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 || (len(p) > 1 && p[0] == '0') {
			return false
		}
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
			n = n*10 + int(r-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}
