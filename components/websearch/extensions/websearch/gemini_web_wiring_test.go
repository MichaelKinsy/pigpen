package websearch

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Cross-slice wiring: with browser cookie access on, the Gemini search of gemini-search.ts reaches
// Gemini Web through the default client, which reads the cookies of the signed-in profile
// (chrome-cookies.ts, through the host command runner) and calls the Gemini Web transport with
// them. The routing twins of the Gemini slice replace the client with a fake; this test keeps the
// default one.
func TestGeminiSearchUsesTheCookieClientByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the canned profile is the macOS and Linux layout; the Windows key store has its own twins")
	}
	home, agentDir := isolate(t)
	t.Setenv("PI_ALLOW_BROWSER_COOKIES", "1")
	writeConfig(t, agentDir, `{"webSearch":{"allowedProviders":["gemini"]}}`)
	db := filepath.Join(browserBase(home, "Chrome", runtime.GOOS), "Default", "Cookies")
	noErr(t, os.MkdirAll(filepath.Dir(db), 0o755))
	noErr(t, os.WriteFile(db, []byte("placeholder"), 0o600))
	rows := `[{"name":"__Secure-1PSID","value":"sid","host_key":".google.com","path":"/","expires_utc":0,"encrypted_value_hex":""},` +
		`{"name":"__Secure-1PSIDTS","value":"ts","host_key":".google.com","path":"/","expires_utc":0,"encrypted_value_hex":""}]`
	t.Cleanup(SetExecRunner(func(_ context.Context, command string, _ []string, _ time.Duration) (ExecResult, error) {
		switch command {
		case "sqlite3":
			return ExecResult{Stdout: rows}, nil
		case "security", "secret-tool":
			return ExecResult{Stdout: "peanuts"}, nil
		}
		return ExecResult{Code: 1}, nil
	}))
	calls := useGeminiWeb(t, func(r GeminiWebRequest) (*http.Response, error) {
		switch {
		case r.URL == "https://gemini.google.com/app":
			return webResp(200, `"SNlM0e":"token"`, nil), nil
		case strings.Contains(r.URL, "StreamGenerate"):
			body := `[null,null,null,null,[["rc",["answer from the web [Src](https://example.com/s)"]]]]`
			return webResp(200, ")]}'\n"+`[["wrb.fr",null,`+jsonString(body)+`]]`, nil), nil
		}
		t.Errorf("unexpected request %s", r.URL)
		return nil, http.ErrNotSupported
	})
	useNet(t, func(c netCall) netReply { t.Errorf("unexpected API request %s", c.URL); return reply(500, "x") })
	r, err := geminiSearch(bg(), "q")
	noErr(t, err)
	if !strings.Contains(r.Answer, "answer from the web") || len(r.Results) != 1 || r.Results[0].URL != "https://example.com/s" {
		t.Fatalf("%+v", r)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0].Cookie, "__Secure-1PSID=sid") || !strings.Contains((*calls)[1].Cookie, "__Secure-1PSIDTS=ts") {
		t.Fatalf("calls %+v", *calls)
	}
}
