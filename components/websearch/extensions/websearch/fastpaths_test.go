package websearch

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// Fast paths of the shared helpers against the code they replace. The references below are the
// previous implementations, kept here verbatim.

func refNormalizeDomain(value string) string {
	input := strings.ToLower(jsTrim(value))
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "-") {
		input = jsTrim(input[1:])
	}
	if input == "" {
		return ""
	}
	candidate := input
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	if u, err := ParseURL(candidate, nil); err == nil {
		input = JSHostname(u)
	} else {
		input, _, _ = strings.Cut(input, "/")
		input, _, _ = strings.Cut(input, ":")
	}
	input = strings.Trim(input, ".")
	if domainShape.MatchString(input) {
		return input
	}
	return ""
}

func refIsNumericLastLabel(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if last == "" && len(labels) > 1 {
		last = labels[len(labels)-2]
	}
	if last == "" {
		return false
	}
	if _, ok := refParseIPv4Number(last); ok {
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

func refParseIPv4Number(s string) (uint64, bool) {
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

var domainEntries = []string{
	"example.com", "Example.COM", "  example.com  ", "-example.com", "- example.com", "--example.com", "docs.example.co.uk",
	"https://example.com/path", "-https://Docs.Example.com/x?y=1", "http://user:pw@example.com:8080/", "example.com:8080", "example.com/path",
	"example.com.", ".example.com", "..example.com", "ex..ample.com", "-a.com", "a-.com", "a-b.com", "a--b.com", "xn--nxasmq6b.com",
	"1.2.3.4", "1.2.3.4.com", "1.2.3", "0x1f.com", "0x7f.0.0.1", "127.1", "999.999.999.999", "0177.0.0.1", "4294967295", "[::1]", "[2001:db8::1]:80",
	"localhost", "a.b", "a.bc", "a.b1", "ab.c1", "a.1b", "1password.com", "3m.com", "x.y.z.museum", "UPPER.CASE.ORG", "mixed.Case.Net",
	"bücher.de", "İstanbul.com", "ſ.com", "K.com", "例え.jp", "exa mple.com", "ex_ample.com", "exam%70le.com", "example.com#frag", "example.com?q", "@", ":", "/", ".", "-", "", "  ",
}

func TestNormalizeDomainFastPathMatchesTheURLPath(t *testing.T) {
	check := func(entry string) {
		t.Helper()
		if got, want := NormalizeDomain(entry), refNormalizeDomain(entry); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, the URL path gives %q", entry, got, want)
		}
	}
	for _, entry := range domainEntries {
		check(entry)
	}
	// Every plain name must have been taken by the fast path for the speed-up to apply to filters.
	for _, entry := range []string{"example.com", "docs.example.co.uk", "-example.com", "a-b.org", "1password.com"} {
		in := strings.TrimPrefix(entry, "-")
		if !plainDNSName(in) {
			t.Errorf("%q is a plain DNS name but plainDNSName rejects it", in)
		}
	}
	r := rand.New(rand.NewPCG(7, 20261020))
	const alphabet = "abcxyzAZ019.-_:/@ %~[]#?"
	for range 60000 {
		b := make([]byte, r.IntN(16))
		for i := range b {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		check(string(b))
		// Names that look like hosts: labels of letters and digits with a letter-first top label.
		labels := make([]string, 1+r.IntN(4))
		for i := range labels {
			l := make([]byte, 1+r.IntN(6))
			for j := range l {
				l[j] = "abcz0189-"[r.IntN(9)]
			}
			labels[i] = string(l)
		}
		check(strings.Join(labels, "."))
		check("-" + strings.Join(labels, "."))
	}
}

func TestIsNumericLastLabelMatchesTheSplitImplementation(t *testing.T) {
	hosts := []string{"", ".", "..", "a", "a.", "a..", "a.b", "a.b.", "1", "1.", "1..", "0x", "0x1f", "a.0x1f", "a.0X1F.", "a.09", "a.0777", "a.123", "a.12a", "a.-1", "a.+1",
		"a.1_0", "a.0b1", "example.com", "example.com.", "1.2.3.4", "1.2.3.4.", "foo.1e3", "foo.0xg", "foo.0x", "foo.00", "foo.0"}
	r := rand.New(rand.NewPCG(11, 3))
	const alphabet = "abcxX0189._-+e"
	for range 40000 {
		b := make([]byte, r.IntN(12))
		for i := range b {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		hosts = append(hosts, string(b))
	}
	for _, h := range hosts {
		if got, want := isNumericLastLabel(h), refIsNumericLastLabel(h); got != want {
			t.Errorf("isNumericLastLabel(%q) = %v, the split implementation gives %v", h, got, want)
		}
	}
}
