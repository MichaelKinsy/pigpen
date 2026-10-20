package websearch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Twins of gemini-web-cookie-opt-in.test.mjs and gemini-web-header-overflow.test.mjs. The upstream
// tests inject a transport with setGeminiFetchOverrideForTests; SetGeminiWebFetch is that hook.

type geminiWebCall struct {
	URL, Cookie, Redirect string
}

// useGeminiWeb installs a Gemini Web transport that records every request.
func useGeminiWeb(t *testing.T, fn func(r GeminiWebRequest) (*http.Response, error)) *[]geminiWebCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]geminiWebCall{}
	t.Cleanup(SetGeminiWebFetch(func(_ context.Context, r GeminiWebRequest) (*http.Response, error) {
		mu.Lock()
		*calls = append(*calls, geminiWebCall{URL: r.URL, Cookie: r.Header.Get("Cookie"), Redirect: r.Redirect})
		mu.Unlock()
		return fn(r)
	}))
	return calls
}

func webResp(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestGeminiWebCookieOptIn(t *testing.T) {
	const F = "gemini-web-cookie-opt-in"
	cookie := CookieMap{"__Secure-1PSID": "cookie"}

	tw(t, F, "Gemini Web never forwards browser cookies across origins", func(t *testing.T) {
		isolate(t)
		calls := useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
			if strings.HasPrefix(r.URL, "https://gemini.google.com/") || strings.HasPrefix(r.URL, "https://accounts.google.com/") {
				return webResp(302, "", http.Header{"Location": {"https://attacker.example/collect"}}), nil
			}
			t.Errorf("unexpected cross-origin request: %s", r.URL)
			return nil, io.ErrUnexpectedEOF
		})
		if email := ActiveGoogleEmail(bg(), CookieMap{"__Secure-1PSID": "sensitive-cookie"}); email != "" {
			t.Fatalf("email = %q", email)
		}
		for _, c := range *calls {
			if strings.HasPrefix(c.URL, "https://attacker.example/") {
				t.Fatalf("a request reached %s", c.URL)
			}
			if c.Redirect != "manual" {
				t.Fatalf("redirect = %q for %s", c.Redirect, c.URL)
			}
		}
		if len(*calls) == 0 {
			t.Fatal("no request was made")
		}
	})

	tw(t, F, "Gemini Web generation rejects automatic redirects", func(t *testing.T) {
		isolate(t)
		useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
			switch {
			case r.URL == "https://gemini.google.com/app":
				return webResp(200, `"SNlM0e":"test-token"`, nil), nil
			case strings.Contains(r.URL, "BardFrontendService/StreamGenerate"):
				if r.Redirect != "error" {
					t.Errorf("redirect = %q", r.Redirect)
				}
				return nil, io.ErrClosedPipe
			}
			t.Errorf("unexpected request: %s", r.URL)
			return nil, io.ErrUnexpectedEOF
		})
		_, err := QueryGeminiWebWithCookies(bg(), "search", cookie, GeminiWebOptions{Model: "gemini-3.1-pro"})
		wantErr(t, err, `closed pipe`)
	})

	tw(t, F, "Gemini Web rejects unsupported models instead of falling back to 2.5 Flash", func(t *testing.T) {
		isolate(t)
		useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
			t.Errorf("transport should not be reached: %s", r.URL)
			return nil, io.ErrUnexpectedEOF
		})
		_, err := QueryGeminiWebWithCookies(bg(), "search", cookie, GeminiWebOptions{Model: "gemini-3.6-flash"})
		wantErr(t, err, `Gemini Web does not support model gemini-3\.6-flash`)
	})

	tw(t, F, "Gemini Web file uploads read the file and reject automatic redirects", func(t *testing.T) {
		isolate(t)
		path := filepath.Join(t.TempDir(), "sample.txt")
		noErr(t, os.WriteFile(path, []byte("sample"), 0o600))
		var body string
		useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
			switch r.URL {
			case "https://gemini.google.com/app":
				return webResp(200, `"SNlM0e":"test-token"`, nil), nil
			case "https://content-push.googleapis.com/upload":
				if r.Redirect != "error" {
					t.Errorf("redirect = %q", r.Redirect)
				}
				body = string(r.Body)
				return nil, io.ErrClosedPipe
			}
			t.Errorf("unexpected request: %s", r.URL)
			return nil, io.ErrUnexpectedEOF
		})
		_, err := QueryGeminiWebWithCookies(bg(), "inspect file", cookie, GeminiWebOptions{Files: []string{path}, Model: "gemini-3.1-pro"})
		wantErr(t, err, `closed pipe`)
		if !strings.Contains(body, `filename="sample.txt"`) || !strings.Contains(body, "sample") {
			t.Fatalf("upload body = %q", body)
		}
	})

	tw(t, F, "browser cookie access is disabled unless explicitly allowed", func(t *testing.T) {
		_, agentDir := isolate(t)
		unsetenv(t, "PI_ALLOW_BROWSER_COOKIES", "FEYNMAN_ALLOW_BROWSER_COOKIES")
		allowed := func() bool {
			ok, err := IsBrowserCookieAccessAllowed()
			noErr(t, err)
			return ok
		}
		if allowed() {
			t.Fatal("allowed with no configuration")
		}
		writeConfig(t, agentDir, `{"allowBrowserCookies": true}`+"\n")
		if !allowed() {
			t.Fatal("not allowed although the configuration allows it")
		}
		writeConfig(t, agentDir, `{}`+"\n")
		t.Setenv("PI_ALLOW_BROWSER_COOKIES", "1")
		if !allowed() {
			t.Fatal("not allowed although the environment allows it")
		}
		t.Setenv("PI_ALLOW_BROWSER_COOKIES", "")
		t.Setenv("FEYNMAN_ALLOW_BROWSER_COOKIES", "1")
		if !allowed() {
			t.Fatal("not allowed although FEYNMAN_ALLOW_BROWSER_COOKIES allows it")
		}
	})

	tw(t, F, "disabled browser cookie access does not validate legacy profile selection", func(t *testing.T) {
		_, agentDir := isolate(t)
		unsetenv(t, "PI_ALLOW_BROWSER_COOKIES", "FEYNMAN_ALLOW_BROWSER_COOKIES")
		writeConfig(t, agentDir, `{"chromeProfile": "Profile 1"}`+"\n")
		ok, err := IsBrowserCookieAccessAllowed()
		noErr(t, err)
		if ok {
			t.Fatal("allowed")
		}
	})

	tw(t, F, "browser cookie config validates presets and rejects arbitrary profile paths", func(t *testing.T) {
		_, agentDir := isolate(t)
		selection := func(config string) (BrowserCookieSelection, error) {
			writeConfig(t, agentDir, config+"\n")
			resetGeminiWebConfig()
			return BrowserCookieSelectionFromConfig()
		}
		got, err := selection(`{"browserCookies": {"browser": "Helium", "profile": "Profile 1"}}`)
		noErr(t, err)
		if want := (BrowserCookieSelection{Browser: "helium", Profile: "Profile 1"}); got != want {
			t.Fatalf("selection = %+v, want %+v", got, want)
		}
		_, err = selection(`{"chromeProfile": "Profile 1", "allowBrowserCookies": true}`)
		wantErr(t, err, `chromeProfile.*no longer supported.*browserCookies\.profile`)
		_, err = selection(`{"browserCookies": {"browser": "safari", "profile": "Profile 1"}}`)
		wantErr(t, err, `Unsupported browserCookies\.browser`)
		_, err = selection(`{"browserCookies": {"browser": "helium", "profile": 1}}`)
		wantErr(t, err, `browserCookies\.profile.*must be a profile directory name`)
		_, err = selection(`{"browserCookies": {"browser": "helium", "profilePath": "/tmp/Profile 1"}}`)
		wantErr(t, err, `profilePath is not supported`)
	})
}

func TestGeminiWebHeaderOverflow(t *testing.T) {
	const F = "gemini-web-header-overflow"

	tw(t, F, "createGeminiFetch wires a dedicated agent with a >16 KiB header budget", func(t *testing.T) {
		isolate(t)
		tr, err := newGeminiWebTransport()
		noErr(t, err)
		if tr.MaxResponseHeaderBytes != GeminiMaxHeaderSize {
			t.Fatalf("MaxResponseHeaderBytes = %d, want %d", tr.MaxResponseHeaderBytes, GeminiMaxHeaderSize)
		}
		if tr.MaxResponseHeaderBytes <= 16*1024 {
			t.Fatal("the header budget must exceed the 16 KiB default")
		}
		if tr.ForceAttemptHTTP2 || tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
			t.Fatal("HTTP/2 must stay off (allowH2: false)")
		}
		if tr.Proxy == nil {
			t.Fatal("the proxy environment must be honoured (EnvHttpProxyAgent)")
		}
	})

	tw(t, F, "dedicated agent fetches oversized-header responses the pi-style global dispatcher rejects", func(t *testing.T) {
		isolate(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i < 40; i++ {
				w.Header().Set("X-Large-"+strconv.Itoa(i), strings.Repeat("x", 2048))
			}
			_, _ = w.Write([]byte("payload-ok"))
		}))
		defer srv.Close()
		// Control: a 16 KiB header budget (the host's default) chokes on ~85 KiB of headers.
		small := &http.Transport{MaxResponseHeaderBytes: 16 * 1024}
		if resp, err := (&http.Client{Transport: small}).Get(srv.URL); err == nil {
			resp.Body.Close()
			t.Fatal("the 16 KiB control accepted oversized headers")
		}
		tr, err := newGeminiWebTransport()
		noErr(t, err)
		resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
		noErr(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || string(body) != "payload-ok" {
			t.Fatalf("status %d body %q", resp.StatusCode, body)
		}
	})

	tskip(t, F, "resolveGeminiFetch falls back to the ambient global fetch when undici is not resolvable", "the dedicated transport is net/http from the standard library, always resolvable; there is no optional module whose absence needs an ambient fallback")

	tw(t, F, "resolveGeminiFetch surfaces dedicated agent construction errors", func(t *testing.T) {
		isolate(t)
		t.Setenv("HTTPS_PROXY", "not a url")
		_, err := resolveGeminiWebClient()
		wantErr(t, err, `Invalid URL`)
	})
}
