// Package netrange decides whether an IP address may be dialled. It is the address rule of the egress profile.
package netrange

import (
	"errors"
	"net/netip"
)

// ErrDenied is returned for an address that may not be dialled. It names no address.
var ErrDenied = errors.New("address denied")

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// hardDenied are never dialled, whatever the operator lists: link-local, and the cloud metadata services.
var hardDenied = mustPrefixes(
	"169.254.0.0/16",     // IPv4 link-local, including the AWS, GCP, Azure and ECS metadata addresses
	"fe80::/10",          // IPv6 link-local
	"fd00:ec2::254/128",  // AWS metadata, IPv6
	"100.100.100.200/32", // Alibaba Cloud metadata
	"168.63.129.16/32",   // Azure wire server
)

// defaultDenied are the non-public ranges. An operator range exempts them (but not hardDenied).
var defaultDenied = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	// Beyond the base list: addresses that carry or hide an IPv4 address that cannot be checked, or hold no global
	// address, are not public. ::/8 is reserved by the IETF; it covers the IPv4-compatible form (::/96), the SIIT
	// IPv4-translated form ::ffff:0:a.b.c.d (RFC 6145) and local-use NAT64 (64:ff9b:1::/48, RFC 8215). IPv4-mapped and
	// well-known NAT64 addresses inside it are unmapped before this list is consulted. Then Teredo (2001::/32),
	// deprecated site-local (fec0::/10) and discard-only (100::/64, RFC 6666).
	"::/8", "2001::/32", "fec0::/10", "100::/64",
)

var (
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
	to4   = netip.MustParsePrefix("2002::/16")
)

// Normalize returns the address that is actually reached: an IPv4-mapped IPv6 address becomes its IPv4 address, a
// NAT64 (64:ff9b::/96) or 6to4 (2002::/16) address becomes the IPv4 address embedded in it, and a zone is dropped.
func Normalize(a netip.Addr) netip.Addr {
	a = a.WithZone("").Unmap()
	if !a.Is6() {
		return a
	}
	b := a.As16()
	switch {
	case nat64.Contains(a):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	case to4.Contains(a):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
	}
	return a
}

func contains(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Check reports whether a may be dialled. allow is the operator's list of ranges (already normalised by
// NormalizePrefix). The order is: the hard-denied ranges, the operator ranges, the default-denied ranges, then
// anything that is not a global unicast address.
func Check(a netip.Addr, allow []netip.Prefix) error {
	if !a.IsValid() {
		return ErrDenied
	}
	n := Normalize(a)
	switch {
	case contains(hardDenied, n):
		return ErrDenied
	case contains(allow, n):
		return nil
	case contains(defaultDenied, n):
		return ErrDenied
	case !n.IsGlobalUnicast():
		return ErrDenied
	}
	return nil
}

// NormalizePrefix masks p and unmaps an IPv4-mapped IPv6 prefix. A prefix that covers every address is refused, as
// is a mapped prefix that is shorter than the mapped block.
func NormalizePrefix(p netip.Prefix) (netip.Prefix, error) {
	if !p.IsValid() {
		return netip.Prefix{}, ErrDenied
	}
	a := p.Addr().WithZone("")
	bits := p.Bits()
	if a.Is4In6() {
		if bits < 96 {
			return netip.Prefix{}, ErrDenied
		}
		a = a.Unmap()
		bits -= 96
	}
	if bits == 0 {
		return netip.Prefix{}, ErrDenied
	}
	return netip.PrefixFrom(a, bits).Masked(), nil
}
