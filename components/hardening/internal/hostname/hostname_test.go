package hostname

import "testing"

func TestNormalize(t *testing.T) {
	good := map[string]string{
		"example.com":           "example.com",
		"Example.COM":           "example.com",
		"example.com.":          "example.com",
		"EXAMPLE.com.":          "example.com",
		"bücher.example":        "xn--bcher-kva.example",
		"BÜCHER.example":        "xn--bcher-kva.example",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"a.b.c.example.org":     "a.b.c.example.org",
		"10.0.0.1":              "10.0.0.1",
		"::ffff:10.0.0.1":       "10.0.0.1",
		"2606:4700::1111":       "2606:4700::1111",
		"localhost":             "localhost",
		// the other full stops map to "." in IDNA: a trailing one is still the trailing dot
		"example.com\u3002":      "example.com",
		"example\uff0ecom\uff0e": "example.com",
		"\uff45xample.com":       "example.com", // full-width e
	}
	for in, want := range good {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", ".", "..", "example.com..", ".example.com", "a..b", "exa mple.com", "example.com:443", "exa/mple.com",
		"example.com\x00", "example.com\n", "*.example.com", "fe80::1%eth0", "-bad.example", "a_b.example",
		"xn--zz.example", "example.com/x", "user@example.com", "example.com\u3002\u3002", "example.com.\u3002", "\u3002example.com",
	} {
		if got, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", bad, got)
		}
	}
}

func TestParseEntryAndMatch(t *testing.T) {
	type c struct {
		entry, host string
		match       bool
	}
	cases := []c{
		{"example.com", "example.com", true},
		{"example.com", "a.example.com", false},
		{".example.com", "a.example.com", true},
		{".example.com", "a.b.example.com", true},
		{".example.com", "example.com", false},
		{".example.com", "evil-example.com", false},
		{".example.com", "evilexample.com", false},
		{".example.com", "example.com.evil.net", false},
		{"example.com", "evil-example.com", false},
		{"10.0.0.5", "10.0.0.5", true},
		{"10.0.0.5", "10.0.0.50", false},
	}
	for _, k := range cases {
		entry, err := ParseEntry(k.entry)
		if err != nil {
			t.Fatalf("ParseEntry(%q): %v", k.entry, err)
		}
		host, err := Normalize(k.host)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", k.host, err)
		}
		if got := Match(entry, host); got != k.match {
			t.Errorf("Match(%q, %q) = %v, want %v", k.entry, k.host, got, k.match)
		}
	}
	for _, bad := range []string{"", ".", "*.example.com", "..example.com", ".10.0.0.1", "example.com:443", ". example.com", "a b"} {
		if e, err := ParseEntry(bad); err == nil {
			t.Errorf("ParseEntry(%q) = %q, want an error", bad, e)
		}
	}
}

func TestSuffixEntryIsNormalised(t *testing.T) {
	e, err := ParseEntry(".Bücher.Example")
	if err != nil || e != ".xn--bcher-kva.example" {
		t.Fatalf("got %q, %v", e, err)
	}
	h, _ := Normalize("shop.bücher.example.")
	if !Match(e, h) {
		t.Fatal("no match")
	}
}

func TestParseOrigin(t *testing.T) {
	good := map[string]string{
		"https://api.example.com":      "https://api.example.com:443",
		"https://API.Example.com/":     "https://api.example.com:443",
		"https://api.example.com:8443": "https://api.example.com:8443",
		"http://127.0.0.1:8080":        "http://127.0.0.1:8080",
		"http://[::1]:9":               "http://[::1]:9",
		"HTTPS://api.example.com.":     "https://api.example.com:443",
	}
	for in, want := range good {
		o, err := ParseOrigin(in)
		if err != nil || o.String() != want {
			t.Errorf("ParseOrigin(%q) = %v, %v; want %s", in, o, err, want)
		}
	}
	for _, bad := range []string{
		"", "api.example.com", "ftp://x.example", "https://", "https://u:p@api.example.com", "https://api.example.com/path",
		"https://api.example.com?x=1", "https://api.example.com#f", "https://api.example.com:0", "https://api.example.com:99999",
		"https://api.example.com:08", "mailto:x@example.com", "https://api.example.com:abc",
	} {
		if o, err := ParseOrigin(bad); err == nil {
			t.Errorf("ParseOrigin(%q) = %v, want an error", bad, o)
		}
	}
	a, _ := ParseOrigin("https://example.com")
	b, _ := ParseOrigin("https://example.com:443")
	if a != b {
		t.Fatal("the default port is not applied")
	}
}

func TestIsLoopback(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "127.8.8.8", "::1"} {
		if !IsLoopback(h) {
			t.Errorf("%s", h)
		}
	}
	for _, h := range []string{"example.com", "10.0.0.1", "localhost.example.com", "::ffff:8.8.8.8"} {
		if IsLoopback(h) {
			t.Errorf("%s", h)
		}
	}
}
