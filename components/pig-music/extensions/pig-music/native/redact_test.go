package native

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactRemovesURLsAndAddresses(t *testing.T) {
	cases := map[string]string{
		`Get "https://rr3---sn-abc.googlevideo.com/videoplayback?expire=1&sig=SECRET&ip=203.0.113.9": dial tcp 203.0.113.9:443: i/o timeout`: "",
		`read tcp 192.168.1.5:51234->142.250.1.1:443: connection reset`:                                                                      "",
		`dial tcp [2001:db8::1]:443: connect: refused`:                                                                                       "",
		`http://127.0.0.1:8080/x?a=b`: "",
	}
	for in := range cases {
		got := Redact(in)
		for _, bad := range []string{"googlevideo", "SECRET", "203.0.113.9", "192.168.1.5", "142.250.1.1", "2001:db8", "127.0.0.1", "expire="} {
			if strings.Contains(got, bad) {
				t.Errorf("Redact(%q) = %q still contains %q", in, got, bad)
			}
		}
	}
}

func TestRedactKeepsOrdinaryText(t *testing.T) {
	in := "the track needs a proof-of-origin token (status 403)"
	if got := Redact(in); got != in {
		t.Fatalf("changed ordinary text: %q", got)
	}
	// A version number is not an address.
	if got := Redact("yt-dlp 2026.08.19 and Go 1.27.1"); got != "yt-dlp 2026.08.19 and Go 1.27.1" {
		t.Fatalf("changed versions: %q", got)
	}
}

func TestRedactErrorKeepsChain(t *testing.T) {
	base := errors.New("dial tcp 203.0.113.9:443: refused")
	err := RedactError(base)
	if strings.Contains(err.Error(), "203.0.113.9") {
		t.Fatalf("not redacted: %v", err)
	}
	if !errors.Is(err, base) {
		t.Fatalf("lost the chain")
	}
	if RedactError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}

// An IPv6 address in its compressed form (with ::) and without brackets is
// removed whole, not up to the ::.
func TestRedactRemovesCompressedIPv6(t *testing.T) {
	for in, want := range map[string]string{
		"dial tcp 2a00:1450:4001:82b::200e: connection refused": "dial tcp <ip>: connection refused",
		"source fe80::1c2b:3cff:fe4d:5e6f is gone":              "source <ip> is gone",
		"bound to 2001:db8::7 now":                              "bound to <ip> now",
		"at 15:04:05, std::vector stays":                        "at 15:04:05, std::vector stays",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
