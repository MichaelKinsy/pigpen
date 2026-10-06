package netrange

import (
	"net/netip"
	"testing"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func prefixes(ss ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func TestDefaultDeniedRanges(t *testing.T) {
	denied := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "100.64.0.1", "100.127.255.255", "127.0.0.1", "127.9.9.9",
		"169.254.0.1", "169.254.169.254", "172.16.0.1", "172.31.255.255", "192.0.0.1", "192.168.1.1", "198.18.0.1", "198.19.255.255",
		"224.0.0.1", "239.255.255.255", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fd00::1", "fd00:ec2::254", "fe80::1", "febf::1", "ff02::1", "ff00::",
		"::10.0.0.1", "::8.8.8.8", "64:ff9b:1::1", "2001::1", "fec0::1",
		"192.0.0.200", "192.168.200.1", "192.168.255.255", "250.1.1.1", "255.255.255.254", "172.20.0.1", "10.200.0.1", "100.100.0.1", "198.19.0.1", "::ffff:ffff", "64:ff9b:1:ffff::1", "2001:0:ffff::1", "fef0::1", "fdff::1", "0.200.0.1",
		// SIIT IPv4-translated (::ffff:0:0/96) hides 10.0.0.1, 127.0.0.1 and 169.254.169.254; the rest of ::/8; discard-only
		"::ffff:0:a00:1", "::ffff:0:7f00:1", "::ffff:0:a9fe:a9fe", "::1:0:0:1", "ff::1", "100::1", "100::ffff:ffff:ffff:ffff",
	}
	for _, s := range denied {
		if err := Check(addr(s), nil); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
}

func TestPublicAddressesAreAllowed(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "192.0.1.1", "223.255.255.255", "100:0:0:1::1", "200::1"} {
		if err := Check(addr(s), nil); err != nil {
			t.Errorf("%s was denied: %v", s, err)
		}
	}
}

func TestEmbeddedIPv4IsCheckedOnTheEmbeddedAddress(t *testing.T) {
	denied := []string{
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:192.168.0.1",
		"64:ff9b::a00:1",     // NAT64 10.0.0.1
		"64:ff9b::7f00:1",    // NAT64 127.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64 169.254.169.254
		"2002:a00:1::1",      // 6to4 10.0.0.1
		"2002:7f00:1::",      // 6to4 127.0.0.1
		"2002:a9fe:a9fe::1",  // 6to4 169.254.169.254
	}
	for _, s := range denied {
		if err := Check(addr(s), nil); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	allowed := []string{"::ffff:8.8.8.8", "64:ff9b::808:808", "2002:808:808::1"}
	for _, s := range allowed {
		if err := Check(addr(s), nil); err != nil {
			t.Errorf("%s was denied: %v", s, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"::ffff:10.0.0.1":  "10.0.0.1",
		"64:ff9b::a00:1":   "10.0.0.1",
		"2002:a00:1::1":    "10.0.0.1",
		"2606:4700::1111":  "2606:4700::1111",
		"8.8.8.8":          "8.8.8.8",
		"fe80::1%eth0":     "fe80::1",
		"64:ff9b:1::a00:1": "64:ff9b:1::a00:1",
	}
	for in, want := range cases {
		if got := Normalize(addr(in)).String(); got != want {
			t.Errorf("Normalize(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestOperatorRangesExemptPrivateAddresses(t *testing.T) {
	allow := prefixes("10.0.0.0/8", "127.0.0.1/32", "fd12::/16", "100.64.0.0/10")
	for _, s := range []string{"10.1.2.3", "127.0.0.1", "fd12::5", "100.64.1.1", "::ffff:10.1.2.3", "64:ff9b::a01:203", "2002:a01:203::1"} {
		if err := Check(addr(s), allow); err != nil {
			t.Errorf("%s inside the operator range was denied: %v", s, err)
		}
	}
	for _, s := range []string{"127.0.0.2", "192.168.1.1", "172.16.0.1", "fd13::1", "::1"} {
		if err := Check(addr(s), allow); err == nil {
			t.Errorf("%s outside the operator range was allowed", s)
		}
	}
}

func TestMetadataAndLinkLocalStayDeniedInsideOperatorRanges(t *testing.T) {
	allow := prefixes("0.0.0.0/1", "128.0.0.0/1", "169.254.0.0/16", "fd00:ec2::/32", "fe80::/10", "::/1", "8000::/1", "100.64.0.0/10", "168.63.129.16/32")
	for _, s := range []string{
		"169.254.169.254", "169.254.170.2", "169.254.0.1", "fd00:ec2::254", "fe80::1", "fe80::1%eth0",
		"::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1", "100.100.100.200", "168.63.129.16",
	} {
		if err := Check(addr(s), allow); err == nil {
			t.Errorf("%s was allowed inside an operator range", s)
		}
	}
}

func TestOperatorRangeCoveringTheWholeInternetIsNotARange(t *testing.T) {
	for _, s := range []string{"0.0.0.0/0", "::/0"} {
		if _, err := NormalizePrefix(netip.MustParsePrefix(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	got, err := NormalizePrefix(netip.MustParsePrefix("::ffff:10.1.2.3/120"))
	if err != nil || got.String() != "10.1.2.0/24" {
		t.Fatalf("got %v, %v", got, err)
	}
	got, err = NormalizePrefix(netip.MustParsePrefix("10.1.2.3/8"))
	if err != nil || got.String() != "10.0.0.0/8" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := NormalizePrefix(netip.MustParsePrefix("::ffff:0:0/64")); err == nil {
		t.Fatal("a mapped prefix shorter than /96 was accepted")
	}
}
