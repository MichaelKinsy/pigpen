package websearch

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Error branches and security-relevant helpers found uncovered by a coverage run.

func TestKagiErrorBranches(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http error redacts the key", 401, `{"error":"bad key kagi-secret-key"}`, `Kagi API error 401: .*\[REDACTED\]|Kagi API error 401`},
		{"invalid JSON", 200, `not json`, `Kagi API returned invalid JSON`},
		{"non-object envelope", 200, `[1,2]`, `Kagi API returned invalid response: expected an object envelope`},
		{"envelope errors list messages", 200, `{"errors":[{"message":"quota"},{"code":"E2"},{"other":1},"plain"]}`, `invalid response: quota; E2; \{"other":1\}; plain`},
		{"envelope error key", 200, `{"error":[{"msg":"denied"}]}`, `invalid response: denied`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			t.Setenv("KAGI_API_KEY", "kagi-secret-key")
			useNet(t, func(netCall) netReply { return reply(c.status, c.body) })
			_, err := SearchWithKagi(bg(), "q", SearchOptions{})
			wantErr(t, err, c.want)
			if strings.Contains(err.Error(), "kagi-secret-key") {
				t.Fatal("credential leaked: " + err.Error())
			}
		})
	}
	t.Run("missing key names both sources", func(t *testing.T) {
		isolate(t)
		unsetenv(t, "KAGI_API_KEY")
		_, err := SearchWithKagi(bg(), "q", SearchOptions{})
		wantErr(t, err, `(?s)Kagi API key not found.*kagiApiKey.*KAGI_API_KEY`)
	})
	t.Run("results are capped at numResults and deduplicated content", func(t *testing.T) {
		isolate(t)
		t.Setenv("KAGI_API_KEY", "k")
		useNet(t, func(netCall) netReply {
			return reply(200, `{"data":[{"url":"https://a","title":"A","content":"ca"},{"href":"https://b"},{"link":"https://c","name":"C"}]}`)
		})
		res, err := SearchWithKagi(bg(), "q", SearchOptions{NumResults: nf(2), IncludeContent: true})
		noErr(t, err)
		if len(res.Results) != 2 || res.Results[1].Title != "https://b" || len(res.InlineContent) != 1 {
			t.Fatalf("%+v", res)
		}
	})
}

func TestBraveKeyMissingNamesBothSources(t *testing.T) {
	isolate(t)
	unsetenv(t, "BRAVE_API_KEY")
	_, err := SearchWithBrave(bg(), "q", SearchOptions{})
	wantErr(t, err, `(?s)Brave Search API key not found.*braveApiKey.*BRAVE_API_KEY`)
}

func TestProxyBypassRules(t *testing.T) {
	proxy, _ := url.Parse("http://proxy.example:8080")
	unsetenv(t, "NO_PROXY", "no_proxy")
	t.Setenv("NO_PROXY", "internal.example, .corp.test:8443,[::1],*.skipped")
	for target, wantProxy := range map[string]bool{
		"https://api.example.com/x":   true,
		"http://localhost:8080/":      false,
		"http://a.localhost/":         false,
		"http://127.0.0.1/":           false,
		"http://[::1]:9/":             false,
		"https://internal.example/":   false,
		"https://x.internal.example/": false,
		"https://x.corp.test/":        false,
		"https://corp.test/":          true, // ".corp.test" only matches subdomains
		"https://notinternal.example": true,
	} {
		req, _ := http.NewRequest("GET", target, nil)
		got, err := proxyOrBypass(req, proxy)
		noErr(t, err)
		if (got != nil) != wantProxy {
			t.Errorf("%s: proxied=%v, want %v", target, got != nil, wantProxy)
		}
	}
	t.Setenv("NO_PROXY", "*")
	req, _ := http.NewRequest("GET", "https://anything.example/", nil)
	if got, _ := proxyOrBypass(req, proxy); got != nil {
		t.Error("NO_PROXY=* must bypass every host")
	}
	if noProxyEntryMatches("a.example", "") {
		t.Error("an empty entry matches nothing")
	}
}

func TestAPIClientForScopesTheProxy(t *testing.T) {
	restore := SetHTTP(&http.Client{})
	defer restore()
	if c := apiClientFor(bg()); c == nil {
		t.Fatal("no client")
	} else if _, ok := c.(*http.Client); !ok {
		t.Fatalf("%T", c)
	}
	ctx := WithProxy(bg(), "http://proxy.example:8080")
	c := apiClientFor(ctx).(*http.Client)
	tr := c.Transport.(*http.Transport)
	req, _ := http.NewRequest("GET", "https://api.example.com/", nil)
	got, err := tr.Proxy(req)
	noErr(t, err)
	if got == nil || got.Host != "proxy.example:8080" {
		t.Fatalf("%v", got)
	}
	// A test double keeps precedence over the proxy client.
	fake := &fakeNet{fn: func(netCall) netReply { return reply(200, "") }}
	defer SetHTTP(fake)()
	if apiClientFor(ctx) != HTTPDoer(fake) {
		t.Fatal("an injected transport must not be replaced")
	}
}

func TestResizeImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 7, 255})
		}
	}
	var pngBuf, jpgBuf bytes.Buffer
	noErr(t, png.Encode(&pngBuf, img))
	noErr(t, jpeg.Encode(&jpgBuf, img, nil))

	small, err := resizeImage(pngBuf.Bytes(), "image/png", 800, 800)
	noErr(t, err)
	if small == nil || small.width != 400 || small.height != 200 || small.mime != "image/png" {
		t.Fatalf("%+v", small)
	}
	scaled, err := resizeImage(pngBuf.Bytes(), "image/png", 100, 100)
	noErr(t, err)
	if scaled == nil || scaled.width != 100 || scaled.height != 50 || scaled.mime != "image/png" {
		t.Fatalf("%+v", scaled)
	}
	scaledJPEG, err := resizeImage(jpgBuf.Bytes(), "image/jpeg", 100, 100)
	noErr(t, err)
	if scaledJPEG == nil || scaledJPEG.mime != "image/jpeg" || scaledJPEG.width != 100 {
		t.Fatalf("%+v", scaledJPEG)
	}
	if bad, err := resizeImage([]byte("not an image"), "image/png", 100, 100); bad != nil || err != nil {
		t.Fatalf("undecodable data is nil, nil: %v %v", bad, err)
	}
}
