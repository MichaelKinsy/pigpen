package rpiv_web_tools

import (
	"context"
	"strings"
	"testing"
)

// The host of a URL is read as Node's URL class reads it (the WHATWG host parser), so the private-address guard
// of web_fetch sees the address the request goes to. Each expected value is `new URL(input).hostname` under
// Node 24.19.0 ("ERR": the constructor throws, which the original reports as "Invalid URL: <input>").
var whatwgHostCases = [][2]string{
	{"http://127.1/", "127.0.0.1"},
	{"http://2130706433/", "127.0.0.1"},
	{"http://0x7f000001/", "127.0.0.1"},
	{"http://0177.0.0.1/", "127.0.0.1"},
	{"http://0x7f.1/", "127.0.0.1"},
	{"http://127.0.1/", "127.0.0.1"},
	{"http://4294967295/", "255.255.255.255"},
	{"http://4294967296/", "ERR"},
	{"http://256.0.0.1/", "ERR"},
	{"http://1.2.3.4.5/", "ERR"},
	{"http://foo.123/", "ERR"},
	{"http://foo.0x/", "ERR"},
	{"http://example.com./", "example.com."},
	{"http://１２７．０．０．１/", "127.0.0.1"},
	{"http://①②⑦.0.0.1/", "127.0.0.1"},
	{"http://ｌｏｃａｌｈｏｓｔ/", "localhost"},
	{"http://bücher.example/", "xn--bcher-kva.example"},
	{"http://[0:0:0:0:0:0:0:1]/", "[::1]"},
	{"http://[0::1]/", "[::1]"},
	{"http://[::ffff:127.0.0.1]/", "[::ffff:7f00:1]"},
	{"http://[FE80:0::1]/", "[fe80::1]"},
	{"http://[00fd::1]/", "[fd::1]"},
	{"http://[1:0:0:2:0:0:0:3]/", "[1:0:0:2::3]"},
	{"http://[1:0:0:0:2:0:0:3]/", "[1::2:0:0:3]"},
	{"http://[fe80::1%25eth0]/", "ERR"},
	{"http://0.0.0.0/", "0.0.0.0"},
	{"http://0/", "0.0.0.0"},
	{"http://10.1/", "10.0.0.1"},
	{"http://169.254.169.254/", "169.254.169.254"},
	{"http://0xa9fea9fe/", "169.254.169.254"},
	{"http://172.16.0.1/", "172.16.0.1"},
	{"http://192.168.1.1/", "192.168.1.1"},
	{"http://1.2.3.09/", "ERR"},
	{"http://08.1.2.3/", "ERR"},
	{"http://1.2.3.4./", "1.2.3.4"},
	{"http://1.16777216/", "ERR"},
	{"http://1.16777215/", "1.255.255.255"},
	{"http://1.2.65536/", "ERR"},
	{"http://1.2.65535/", "1.2.255.255"},
	{"http://1.2.3.256/", "ERR"},
}

func TestHostIsParsedAsNodeParsesIt(t *testing.T) {
	for _, c := range whatwgHostCases {
		u, err := parseJSURL(c[0])
		got := "ERR"
		if err == nil {
			got = u.Hostname
		}
		if got != c[1] {
			t.Errorf("%s: hostname %q, want %q", c[0], got, c[1])
		}
	}
}

func TestTheRequestGoesToTheParsedURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://EXAMPLE.com:8443/a b?q=1#x": "https://example.com:8443/a%20b?q=1#x",
		"http://bücher.example/p":            "http://xn--bcher-kva.example/p",
		"http://[0:0:0:0:0:0:0:2]:81/":       "http://[::2]:81/",
		"http://134744072/":                  "http://8.8.8.8/",
	} {
		u, err := parseJSURL(in)
		eq(t, err, nil)
		eq(t, u.Href, want)
	}
}

func TestPrivateAddressesInEveryWrittenFormAreRefused(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "brave")
	for _, c := range whatwgHostCases {
		if c[1] == "ERR" || !isPrivateOrLoopbackHostname(c[1]) {
			continue
		}
		_, _, err := fetchTool(context.Background(), c[0], false, nil)
		if err == nil || err.Error() != "Refusing to fetch private/loopback address: "+c[1] {
			t.Errorf("%s: %v", c[0], err)
		}
	}
	_, _, err := fetchTool(context.Background(), "http://4294967296/", false, nil)
	eq(t, err.Error(), "Invalid URL: http://4294967296/")
	// The other providers' base URLs are read the same way.
	eq(t, isLocalHost("http://127.1:11434"), true)
	eq(t, isLocalHost("http://[0::1]:11434"), true)
	eq(t, strings.HasPrefix(func() string { _, err := newSearxng("", "http://1.2.3.4.5"); return err.Error() }(), "SEARXNG_URL is not a valid URL"), true)
}

func TestTheBuiltInFetchRequestsTheParsedURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("WEB_SEARCH_PROVIDER", "brave")
	n := useNet(t, func(r netReq) netResp {
		return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: "ok"}
	})
	text, _, err := fetchTool(context.Background(), "http://134744072:8080/a b", false, nil)
	eq(t, err, nil)
	eq(t, n.Requests[0].URL, "http://8.8.8.8:8080/a%20b")
	eq(t, strings.HasPrefix(text, "**Fetched:** http://134744072:8080/a b\n"), true) // the header names the URL as given
}
