// Package hostname normalises host names for the egress allow-list: lower case, no trailing dot, IDNA ASCII.
package hostname

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// ErrInvalid is returned for a host or allow-list entry that is not acceptable. It carries no input.
var ErrInvalid = errors.New("invalid host")

// Normalize returns the canonical form of host: an IP literal in its canonical text (IPv4-mapped IPv6 unmapped, a
// zone refused), a name lower-cased, stripped of one trailing dot and converted to IDNA ASCII. It does no DNS.
func Normalize(host string) (string, error) {
	if host == "" || len(host) > 253+1 {
		return "", ErrInvalid
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return "", ErrInvalid
		}
		return a.Unmap().String(), nil
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", ErrInvalid
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", ErrInvalid
	}
	// The mapping turns the other full stops (U+3002, U+FF0E, U+FF61) into ".", so a trailing one only appears now.
	ascii = strings.TrimSuffix(ascii, ".")
	if ascii == "" || strings.HasPrefix(ascii, ".") || strings.HasSuffix(ascii, ".") || strings.Contains(ascii, "..") {
		return "", ErrInvalid
	}
	return strings.ToLower(ascii), nil
}

// ParseEntry validates and normalises an allow-list entry: a host (exact match) or ".suffix" (any name below it).
func ParseEntry(entry string) (string, error) {
	if strings.HasPrefix(entry, ".") {
		rest, err := Normalize(entry[1:])
		if err != nil {
			return "", err
		}
		if _, err := netip.ParseAddr(rest); err == nil {
			return "", ErrInvalid
		}
		return "." + rest, nil
	}
	return Normalize(entry)
}

// Match reports whether a normalised host matches a normalised entry. A suffix entry ".example.com" matches
// "a.example.com" and not "example.com" or "evil-example.com": the suffix includes the dot.
func Match(entry, host string) bool {
	if strings.HasPrefix(entry, ".") {
		return len(host) > len(entry) && strings.HasSuffix(host, entry)
	}
	return host == entry
}

// Origin is the scheme, host and port that a credential is bound to. Host is normalised; Port is never empty.
type Origin struct{ Scheme, Host, Port string }

// String returns "scheme://host:port", with brackets around an IPv6 host.
func (o Origin) String() string { return o.Scheme + "://" + net.JoinHostPort(o.Host, o.Port) }

// OriginOf returns the origin of a URL. Only http and https are origins. A URL with user information is refused.
func OriginOf(u *url.URL) (Origin, error) {
	if u == nil || u.User != nil {
		return Origin{}, ErrInvalid
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch scheme {
	case "http":
		if port == "" {
			port = "80"
		}
	case "https":
		if port == "" {
			port = "443"
		}
	default:
		return Origin{}, ErrInvalid
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return Origin{}, ErrInvalid
	}
	host, err := Normalize(u.Hostname())
	if err != nil {
		return Origin{}, err
	}
	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// ParseOrigin parses "scheme://host[:port]". A path, query or fragment is refused (a lone "/" is allowed).
func ParseOrigin(s string) (Origin, error) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return Origin{}, ErrInvalid
	}
	return OriginOf(u)
}

// IsLoopback reports whether a normalised host is "localhost" or a loopback address.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}
