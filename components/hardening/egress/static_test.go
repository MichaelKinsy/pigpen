package egress

import (
	"context"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func cfg(allow ...string) *profile.EgressPolicy { return &profile.EgressPolicy{Allow: allow} }

func mustNew(t *testing.T, c *profile.EgressPolicy) Policy {
	t.Helper()
	p, err := New("websearch", c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantDenied(t *testing.T, err error, code profile.Code) {
	t.Helper()
	if !profile.IsCode(err, code) {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if pe, ok := err.(*profile.Error); !ok || pe.Flag != profile.FlagEgressPolicy || pe.Package != "websearch" {
		t.Fatalf("flag/package not set: %#v", err)
	}
}

func TestAllowListMatching(t *testing.T) {
	p := mustNew(t, cfg("api.example.com", ".search.example", "xn--bcher-kva.example", "203.0.113.7"))
	allowed := []string{
		"api.example.com", "API.EXAMPLE.COM", "api.example.com.", "Api.Example.Com.", "a.search.example", "A.B.search.EXAMPLE.",
		"bücher.example", "BÜCHER.example.", "xn--bcher-kva.example",
	}
	for _, h := range allowed {
		if err := p.Check(h); err != nil {
			t.Errorf("%q denied: %v", h, err)
		}
	}
	denied := []string{
		"", ".", "example.com", "evil.example.com", "api.example.com.evil.net", "xapi.example.com", "evil-api.example.com",
		"search.example", "evil-search.example", "evilsearch.example", "search.example.evil.net", "a..search.example",
		"api.example.com..", ".api.example.com", "api.example.com\x00.evil.net", "api.example.com\n", "api.example.com ",
		"api.example.com:443", "api.example.com/", "user@api.example.com", "*.search.example", "a.search.example@evil.net",
		"203.0.113.70", "203.0.113.8", "bucher.example", "xn--zz.example", "localhost",
	}
	for _, h := range denied {
		wantDenied(t, p.Check(h), profile.EgressDenied)
	}
}

func TestCheckURL(t *testing.T) {
	p := mustNew(t, cfg("api.example.com"))
	parse := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for _, s := range []string{"https://api.example.com/x", "http://API.example.com:8080/x?y=1", "https://api.example.com./"} {
		if err := p.CheckURL(parse(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{
		"https://evil.example/", "https://api.example.com@evil.example/", "https://evil.example@api.example.com.evil.example/",
		"https://evil.example\\@api.example.com/", "https://evil.example#@api.example.com/", "https://evil.example?@api.example.com/",
		"ftp://api.example.com/", "file:///etc/passwd", "gopher://api.example.com/", "//api.example.com/x", "/relative", "https:///x",
		"mailto:a@api.example.com", "wss://api.example.com/",
	} {
		u, err := url.Parse(s)
		if err != nil {
			continue // a URL that does not parse cannot be requested
		}
		if err := p.CheckURL(u); err == nil && u.Hostname() != "api.example.com" {
			t.Errorf("%s allowed (host %q)", s, u.Hostname())
		} else if err == nil && (u.Scheme != "http" && u.Scheme != "https") {
			t.Errorf("%s allowed with scheme %q", s, u.Scheme)
		}
	}
	wantDenied(t, p.CheckURL(nil), profile.EgressDenied)
	wantDenied(t, p.CheckURL(parse("ftp://api.example.com/")), profile.EgressDenied)
	wantDenied(t, p.CheckURL(parse("/relative")), profile.EgressDenied)
}

func TestIPLiteralHostIsCheckedAgainstTheAddressRules(t *testing.T) {
	c := cfg("10.0.0.5", "8.8.8.8", "169.254.169.254", "10.0.0.9", "127.0.0.1", "fd00:ec2::254", "64:ff9b::a00:1")
	c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fd00::/8")}
	p := mustNew(t, c)
	if err := p.Check("10.0.0.5"); err != nil {
		t.Errorf("a listed private literal inside the operator range: %v", err)
	}
	if err := p.Check("::ffff:10.0.0.9"); err != nil {
		t.Errorf("a listed mapped literal inside the operator range: %v", err)
	}
	if err := p.Check("8.8.8.8"); err != nil {
		t.Errorf("public literal: %v", err)
	}
	wantDenied(t, p.Check("127.0.0.1"), profile.EgressAddressDenied)       // listed, private, outside the range
	wantDenied(t, p.Check("169.254.169.254"), profile.EgressAddressDenied) // listed, inside the range, metadata
	wantDenied(t, p.Check("fd00:ec2::254"), profile.EgressAddressDenied)
	if err := p.Check("64:ff9b::a00:1"); err != nil { // NAT64 of 10.0.0.1, inside the operator range
		t.Errorf("NAT64 literal of an address inside the range: %v", err)
	}
	wantDenied(t, p.Check("10.0.0.6"), profile.EgressDenied) // not listed
}

func TestControlHookChecksTheDialledAddress(t *testing.T) {
	c := cfg("a.example")
	c.AllowCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fd00::/8"), netip.MustParsePrefix("fe80::/10")}
	p := mustNew(t, c)
	ok := []string{"8.8.8.8:443", "[2606:4700:4700::1111]:443", "10.1.2.3:80", "127.0.0.1:9", "[::ffff:10.1.2.3]:80", "[::ffff:8.8.8.8]:80", "[64:ff9b::808:808]:443", "[2002:808:808::1]:443"}
	for _, a := range ok {
		if err := p.control(context.Background(), "tcp4", a, nil); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	denied := []string{
		"127.0.0.2:80", "0.0.0.0:80", "192.168.1.1:80", "172.16.0.1:80", "100.64.0.1:80", "198.18.0.1:80", "224.0.0.1:80", "240.0.0.1:80",
		"169.254.169.254:80", "169.254.170.2:80", "[fd00:ec2::254]:80", "[fe80::1]:80", "[fe80::1%eth0]:80", "[::1]:80", "[::]:80", "[fc00::1]:80", "[ff02::1]:80",
		"[::ffff:127.0.0.2]:80", "[::ffff:169.254.169.254]:80", "[64:ff9b::7f00:2]:80", "[64:ff9b::a9fe:a9fe]:80", "[2002:7f00:2::]:80", "[2002:a9fe:a9fe::1]:80",
		"[::10.0.0.1]:80", "100.100.100.200:80", "168.63.129.16:80",
		"", "garbage", "8.8.8.8", "example.com:80", "[8.8.8.8]:80", ":80", "8.8.8.8:http", "8.8.8.8:99999",
	}
	for _, a := range denied {
		if err := p.control(context.Background(), "tcp4", a, nil); !profile.IsCode(err, profile.EgressAddressDenied) {
			t.Errorf("%q: err = %v", a, err)
		}
	}
	for _, n := range []string{"udp", "unix", "ip4", "", "tcp5"} {
		wantDenied(t, p.control(context.Background(), n, "8.8.8.8:80", nil), profile.EgressAddressDenied)
	}
}

func TestNewRejectsWhatTheProfileWouldNot(t *testing.T) {
	bad := []*profile.EgressPolicy{
		nil, {}, {Allow: []string{}}, {Allow: []string{"Example.com"}}, {Allow: []string{"example.com."}}, {Allow: []string{"*.example.com"}},
		{Allow: []string{"a.example"}, AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("10.1.2.3/8")}},
		{Allow: []string{"a.example"}, AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
		{Allow: []string{"a.example"}, AllowCIDRs: []netip.Prefix{{}}},
		{Allow: []string{"a.example"}, NoProxy: []string{"*"}},
		{Allow: []string{"a.example", "p"}, Proxy: "ftp://p:21"},
		{Allow: []string{"a.example", "p"}, Proxy: "http://p"},
		{Allow: []string{"a.example", "p"}, Proxy: "http://u:p@p:80"},
		{Allow: []string{"a.example"}, Proxy: "://"},
		{Allow: []string{"a.example"}, Proxy: "http://proxy.example:3128"}, // the proxy must be listed
		{Allow: []string{"a.example"}, CABundle: "/does/not/exist.pem"},
		{Allow: []string{"a.example"}, CABundle: t.TempDir()},
	}
	for i, c := range bad {
		if _, err := New("websearch", c); !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	var zero Policy
	if err := zero.Check("a.example"); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Errorf("zero Policy Check: %v", err)
	}
	if _, err := zero.Transport(nil); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Errorf("zero Policy Transport: %v", err)
	}
	if _, err := zero.Client(0); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Errorf("zero Policy Client: %v", err)
	}
	if err := zero.CheckURL(&url.URL{Scheme: "https", Host: "a.example"}); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Errorf("zero Policy CheckURL: %v", err)
	}
}

func TestErrorsDoNotEchoTheDestination(t *testing.T) {
	p := mustNew(t, cfg("a.example"))
	for _, h := range []string{"secret-host.evil.net", "10.99.88.77"} {
		err := p.Check(h)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "10.99") || strings.Contains(err.Error(), "evil") {
			t.Errorf("%v", err)
		}
	}
	err := p.control(context.Background(), "tcp4", "10.99.88.77:80", nil)
	if err == nil || strings.Contains(err.Error(), "10.99") {
		t.Errorf("%v", err)
	}
}

func TestDenied(t *testing.T) {
	p := mustNew(t, cfg("a.example"))
	if !Denied(p.Check("evil.example")) || !Denied(p.control(context.Background(), "tcp4", "127.0.0.1:1", nil)) || Denied(nil) || Denied(context.Canceled) {
		t.Fatal("Denied")
	}
}
