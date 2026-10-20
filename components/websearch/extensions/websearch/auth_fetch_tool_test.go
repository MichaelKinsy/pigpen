package websearch

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

// fetch_content with `auth` (index.ts:2596-2680): profile errors, the authenticated request, and
// the session cache that a profile with cache "off" never fills.

func TestFetchContentAuth(t *testing.T) {
	text := func(o ToolOutput) string { return o.Text() }

	t.Run("a profile error is a tool error before any network", func(t *testing.T) {
		r, h := newRuntime(t, `{"authFetch":{"work":["example.com"],"home":["example.org"]}}`)
		n := useNet(t, func(netCall) netReply { return reply(200, "x") })
		for _, c := range []struct {
			auth any
			want string
		}{
			{"nope", "Error: Unknown authFetch profile: nope"},
			{true, "Error: auth: true requires exactly one authFetch profile; use a profile name instead"},
		} {
			out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page", "auth": c.auth})
			noErr(t, err)
			if text(out) != c.want || out.Details["error"] != strings.TrimPrefix(c.want, "Error: ") {
				t.Fatalf("output = %q %v", text(out), out.Details)
			}
		}
		if len(n.calls) != 0 || h.entryCount() != 0 {
			t.Fatal("a refused profile reached the network or the session")
		}
	})

	t.Run("no authFetch profile at all", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page", "auth": true})
		noErr(t, err)
		wantText(t, text(out), `^Error: auth requires at least one authFetch profile in .*web-search\.json$`)
	})

	t.Run("auth cannot be combined with mode answer", func(t *testing.T) {
		r, _ := newRuntime(t, `{"authFetch":{"work":["example.com"]}}`)
		out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://example.com/p", "auth": "work", "mode": "answer", "prompt": "q"})
		noErr(t, err)
		if text(out) != "Error: auth cannot be combined with mode answer." {
			t.Fatalf("output = %q", text(out))
		}
	})

	t.Run("a session profile stores the result and a cache-off profile does not", func(t *testing.T) {
		for _, cache := range []string{"session", "off"} {
			t.Run(cache, func(t *testing.T) {
				r, h := newRuntime(t, `{"authFetch":{"work":{"hosts":["93.184.216.34"],"cache":"`+cache+`"}}}`)
				s := installShims(t, runtime.GOOS)
				s.password(passwordCommand(runtime.GOOS), "peanuts")
				createAuthFixture(t, os.Getenv("HOME"), [][]any{{"sid", "session", "93.184.216.34", "/", nil, 0, ""}})
				f := useNet(t, func(netCall) netReply {
					return netReply{Status: 200, Body: "authenticated body", Header: http.Header{"Content-Type": {"text/plain"}}}
				})
				out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page", "auth": "work"})
				noErr(t, err)
				if text(out) != "authenticated body" {
					t.Fatalf("output = %q", text(out))
				}
				if got := f.calls[0].Header.Get("Cookie"); got != "sid=session" {
					t.Fatalf("cookie = %q", got)
				}
				_, hasID := out.Details["responseId"]
				if hasID != (cache == "session") || (h.entryCount() == 1) != (cache == "session") {
					t.Fatalf("cache %s: responseId %v, entries %d", cache, hasID, h.entryCount())
				}
			})
		}
	})
}
