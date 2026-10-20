package websearch

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Benchmarks of the browser cookie path with canned programs (no process is started), compared
// with the original's by port/bench-cookies.mjs, which patches execFile and fetch the same way.

const benchCookieRows = `[{"name":"sid","value":"synthetic","host_key":"app.example.com","path":"/private","expires_utc":0,"encrypted_value_hex":""}]`

func benchCookieEnv(b *testing.B) {
	b.Helper()
	home := b.TempDir()
	agent := filepath.Join(home, "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		b.Fatal(err)
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "PIG_HOME": home + "/pig", "PIG_CODING_AGENT_DIR": agent, "PI_ALLOW_BROWSER_COOKIES": "1"} {
		b.Setenv(k, v)
	}
	ResetCaches()
	b.Cleanup(ResetCaches)
	if err := os.WriteFile(filepath.Join(agent, "web-search.json"), []byte(`{"authFetch":{"work":["example.com"]}}`), 0o600); err != nil {
		b.Fatal(err)
	}
	db := filepath.Join(browserBase(home, "Chrome", runtime.GOOS), "Default", "Cookies")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("placeholder"), 0o600); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(SetExecRunner(func(_ context.Context, command string, _ []string, _ time.Duration) (ExecResult, error) {
		switch command {
		case "sqlite3":
			return ExecResult{Stdout: benchCookieRows}, nil
		case "security", "secret-tool":
			return ExecResult{Stdout: "peanuts"}, nil
		}
		return ExecResult{Code: 1}, nil
	}))
}

func BenchmarkCookieExtraction(b *testing.B) {
	benchCookieEnv(b)
	u, _ := url.Parse("https://app.example.com/private/page")
	for _, mode := range []string{"warm", "cold"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "cold" {
					resetCookieState()
				}
				res, err := GetBrowserCookiesForHosts(bg(), BrowserCookieOptions{Hosts: []string{"app.example.com"}, RequestURL: u})
				if err != nil || res == nil || res.CookieHeader != "sid=synthetic" {
					b.Fatalf("result %+v err %v", res, err)
				}
			}
		})
	}
}

func BenchmarkAuthenticatedFetch(b *testing.B) {
	benchCookieEnv(b)
	f := &fakeNet{fn: func(netCall) netReply {
		return netReply{Status: 200, Body: "private body", Header: http.Header{"Content-Type": {"text/plain"}}}
	}}
	b.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		req.Header = init.Header.Clone()
		return f.Do(req)
	}))
	p, err := ResolveAuthFetchProfile(AuthSpec{Name: "work"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res := ExtractContent(bg(), "https://app.example.com/private/page", ExtractOptions{Mode: "raw", AuthProfile: &p, Lookup: publicLookup})
		if res.Error != nil || res.Content != "private body" {
			b.Fatalf("result %+v", res)
		}
	}
}
