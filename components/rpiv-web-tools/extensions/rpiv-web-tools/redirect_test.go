package rpiv_web_tools

import (
	"testing"
)

// The original's fetch (undici) follows 20 redirects and fails on the 21st with a network error that web_fetch
// reports as "fetch failed"; net/http's default client stops at the tenth. upstream: web-tools.ts fetchViaGenericHtml
// uses the global fetch with redirect "follow".
func TestRedirectLimit(t *testing.T) {
	hops := func(t *testing.T, redirects int) (string, error, int) {
		t.Helper()
		clearEnv(t)
		seen := 0
		useNet(t, func(netReq) netResp {
			seen++
			if seen <= redirects {
				return netResp{Status: 302, Header: map[string]string{"Location": "https://example.com/hop"}}
			}
			return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: "arrived"}
		})
		text, _, err := fetchURL(t, "https://example.com/start", false)
		return text, err, seen
	}
	for _, n := range []int{1, 10, 11, 20} {
		t.Run("follows "+itoa(n)+" redirects", func(t *testing.T) {
			text, err, seen := hops(t, n)
			eq(t, err, nil)
			eq(t, seen, n+1)
			contains(t, text, "arrived")
		})
	}
	t.Run("the 21st redirect fails as fetch failed", func(t *testing.T) {
		_, err, seen := hops(t, 21)
		eq(t, errText(err), "fetch failed")
		eq(t, seen, 21)
	})
}
