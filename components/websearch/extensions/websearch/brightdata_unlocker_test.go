package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twin of test/brightdata-unlocker. `redirect: "manual"` has no counterpart (the Go transport
// never follows a redirect, so the manual walk below is the only one); transport errors carry no
// JavaScript error name; an own timeout is a "timed out" error rather than an AbortError.

var bdUnlocker = map[string]string{"BRIGHTDATA_API_KEY": "bd-test-key", "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}

func bdUnlock(t *testing.T, env map[string]string, handler func(netCall) netReply) *fakeNet {
	t.Helper()
	bdSetup(t, "", env)
	return useNet(t, handler)
}

func TestUpstream_brightdata_unlocker(t *testing.T) {
	const f = "brightdata-unlocker"
	lookup := pageOpts()

	tw(t, f, "Bright Data Web Unlocker maps markdown and sends the credential and Unlocker zone", func(t *testing.T) {
		fn := bdUnlock(t, bdUnlocker, func(netCall) netReply { return reply(200, "# Unlocked Title\n\nUnlocked body.\n") })
		res, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/article", lookup, SsrfConfig{})
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != "https://api.brightdata.com/request" || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer bd-test-key" || c.Header.Get("Content-Type") != "application/json" ||
			!reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/article", "zone": "pi_unlocker", "format": "raw", "data_format": "markdown"}) {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/article", Title: "Unlocked Title", Content: "# Unlocked Title\n\nUnlocked body."}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Bright Data Web Unlocker rejects private targets without invoking the paid API", func(t *testing.T) {
		fn := bdUnlock(t, bdUnlocker, func(netCall) netReply { return reply(200, "# Should never happen") })
		var errs []string
		try := func(target string, o ExtractOptions) {
			if _, err := ExtractWithBrightDataUnlocker(bg(), target, o, SsrfConfig{}); err != nil {
				errs = append(errs, err.Error())
			}
		}
		for _, target := range []string{"http://127.0.0.1:8080/admin", "http://localhost:8080/admin", "http://169.254.169.254/latest/meta-data"} {
			try(target, ExtractOptions{})
		}
		try("https://internal.example.com/", ExtractOptions{Lookup: func(context.Context, string) ([]LookupAddress, error) {
			return []LookupAddress{{Address: "10.0.0.5", Family: 4}}, nil
		}})
		try("https://rebind.example.com/", ExtractOptions{Lookup: func(context.Context, string) ([]LookupAddress, error) {
			return []LookupAddress{{Address: "93.184.216.34", Family: 4}, {Address: "192.168.1.10", Family: 4}}, nil
		}})
		if fn.count() != 0 || len(errs) != 5 {
			t.Fatalf("%d calls %v", fn.count(), errs)
		}
		for i, re := range []string{`Blocked internal address`, `Blocked internal hostname`, `Blocked internal address`, `Blocked internal address`, `Blocked internal address`} {
			matchRE(t, re, errs[i])
		}
	})

	tw(t, f, "Bright Data Web Unlocker is not reached for a blocked target through fetch_content", func(t *testing.T) {
		extractEnv(t, `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"pi_unlocker"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, "# Should never happen") })
		r := ExtractContent(bg(), "http://169.254.169.254/latest/meta-data", ExtractOptions{})
		if fn.count() != 0 || r.Content != "" {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
		matchRE(t, `Blocked internal address`, errOf(r))
	})

	tw(t, f, "Bright Data Web Unlocker validates its own fixed API endpoint, not just the target", func(t *testing.T) {
		fn := bdUnlock(t, bdUnlocker, func(netCall) netReply { return reply(200, "# Should never happen") })
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", ExtractOptions{Lookup: func(_ context.Context, host string) ([]LookupAddress, error) {
			if host == "api.brightdata.com" {
				return []LookupAddress{{Address: "10.0.0.5", Family: 4}}, nil
			}
			return []LookupAddress{{Address: "93.184.216.34", Family: 4}}, nil
		}}, SsrfConfig{})
		wantErr(t, err, `Blocked internal address`)
		if fn.count() != 0 {
			t.Fatal("the endpoint was fetched")
		}
	})

	tw(t, f, "Bright Data Web Unlocker credentials are stripped on public cross-origin API redirects", func(t *testing.T) {
		n := 0
		fn := bdUnlock(t, map[string]string{"BRIGHTDATA_API_KEY": "bd-secret", "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, func(netCall) netReply {
			n++
			if n == 1 {
				return redirectTo(307, "https://mirror.example.com/request")
			}
			return reply(200, "# Redirect body")
		})
		res, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		noErr(t, err)
		if len(fn.calls) != 2 || fn.calls[0].URL != "https://api.brightdata.com/request" || fn.calls[0].Header.Get("Authorization") != "Bearer bd-secret" ||
			fn.calls[1].URL != "https://mirror.example.com/request" || fn.calls[1].Header.Get("Authorization") != "" {
			t.Fatalf("%v", fn.calls)
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/a", Title: "Redirect body", Content: "# Redirect body"}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Bright Data Web Unlocker blocks API redirects to private targets", func(t *testing.T) {
		fn := bdUnlock(t, bdUnlocker, func(netCall) netReply { return redirectTo(302, "http://127.0.0.1/admin") })
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		wantErr(t, err, `Blocked internal address`)
		if !eqStrings(fn.urls(), []string{"https://api.brightdata.com/request"}) {
			t.Fatal(fn.urls())
		}
	})

	tw(t, f, "Bright Data Web Unlocker stops after five API redirects with an explicit message", func(t *testing.T) {
		n := 0
		fn := bdUnlock(t, map[string]string{"BRIGHTDATA_API_KEY": "bd-secret", "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, func(netCall) netReply {
			n++
			return redirectTo(302, fmt.Sprintf("https://hop%d.example.com/request", n))
		})
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		want := []string{"https://api.brightdata.com/request"}
		for i := 1; i <= 5; i++ {
			want = append(want, fmt.Sprintf("https://hop%d.example.com/request", i))
		}
		if !eqStrings(fn.urls(), want) || err == nil || err.Error() != "Too many redirects fetching https://hop5.example.com/request" || strings.Contains(err.Error(), "bd-secret") {
			t.Fatalf("%v %v", fn.urls(), err)
		}
		for i, c := range fn.calls {
			if (i == 0) != (c.Header.Get("Authorization") == "Bearer bd-secret") || (i > 0 && c.Header.Get("Authorization") != "") {
				t.Fatalf("hop %d: %v", i, c.Header)
			}
		}
	})

	tw(t, f, "BRIGHTDATA_UNLOCKER_ZONE takes precedence over brightdataUnlockerZone in the config file", func(t *testing.T) {
		for _, c := range []struct {
			env      map[string]string
			expected string
		}{{nil, "config_zone1"}, {map[string]string{"BRIGHTDATA_UNLOCKER_ZONE": "env_zone1"}, "env_zone1"}} {
			bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"config_zone1"}`, c.env)
			fn := useNet(t, func(netCall) netReply { return reply(200, "# Zone body") })
			_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
			noErr(t, err)
			if z := jsonBody(t, fn.calls[0])["zone"]; z != c.expected {
				t.Fatalf("%v: %v", c.env, z)
			}
		}
	})

	tw(t, f, "Bright Data Web Unlocker requires its own zone and never borrows the SERP zone", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"serp_zone1"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, "# Should never happen") })
		available := IsBrightDataUnlockerAvailable()
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		if available || fn.count() != 0 {
			t.Fatalf("%v %d", available, fn.count())
		}
		for _, re := range []string{`Bright Data Web Unlocker zone not configured`, `brightdataUnlockerZone`, `BRIGHTDATA_UNLOCKER_ZONE`} {
			wantErr(t, err, re)
		}
	})

	tw(t, f, "Bright Data Web Unlocker availability needs both a zone and a credential source", func(t *testing.T) {
		for _, c := range []struct {
			env      map[string]string
			expected bool
		}{
			{nil, false},
			{map[string]string{"BRIGHTDATA_API_KEY": "bd-test-key"}, false},
			{map[string]string{"BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, false},
			{bdUnlocker, true},
			{map[string]string{"BRIGHTDATA_API_KEY": "bd-test-key", "BRIGHTDATA_UNLOCKER_ZONE": "https://zone.example.com"}, false},
		} {
			bdSetup(t, "", c.env)
			if got := IsBrightDataUnlockerAvailable(); got != c.expected {
				t.Fatalf("%v: %v", c.env, got)
			}
		}
	})

	tw(t, f, "Bright Data Web Unlocker resolves the credential at request time, after the SSRF guard", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell credential command")
		}
		_, dir := isolate(t)
		marker := filepath.Join(dir, "resolver-ran")
		writeConfig(t, dir, fmt.Sprintf(`{"brightdataApiKey":%q,"brightdataUnlockerZone":"pi_unlocker"}`, "!touch "+marker+" && printf bd-command-key"))
		fn := useNet(t, func(netCall) netReply { return reply(200, "# Command body") })
		availableBefore := IsBrightDataUnlockerAvailable()
		_, ranBefore := os.Stat(marker)
		_, blocked := ExtractWithBrightDataUnlocker(bg(), "http://127.0.0.1:8080/admin", ExtractOptions{}, SsrfConfig{})
		_, ranAfterBlocked := os.Stat(marker)
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		noErr(t, err)
		_, ranAfter := os.Stat(marker)
		wantErr(t, blocked, `Blocked internal address`)
		if !availableBefore || !os.IsNotExist(ranBefore) || !os.IsNotExist(ranAfterBlocked) || ranAfter != nil || fn.calls[0].Header.Get("Authorization") != "Bearer bd-command-key" {
			t.Fatalf("%v %v %v %v", availableBefore, ranBefore, ranAfterBlocked, ranAfter)
		}
	})

	tw(t, f, "Bright Data Web Unlocker HTTP failures surface the status and redact the credential", func(t *testing.T) {
		bdUnlock(t, map[string]string{"BRIGHTDATA_API_KEY": "bd-secret", "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, func(netCall) netReply { return reply(402, "payment required for token bd-secret") })
		res, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		if res != nil {
			t.Fatal("a result")
		}
		for _, re := range []string{`Bright Data Web Unlocker error 402`, `payment required`, `\[redacted\]`} {
			wantErr(t, err, re)
		}
		if strings.Contains(err.Error(), "bd-secret") {
			t.Fatal(err)
		}
	})

	tw(t, f, "Bright Data Web Unlocker redacts the credential before truncating a long error body", func(t *testing.T) {
		const key = "bd-boundary-secret-0123456789"
		bdUnlock(t, map[string]string{"BRIGHTDATA_API_KEY": key, "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, func(netCall) netReply {
			return reply(500, strings.Repeat("e", 280)+key+" trailing detail")
		})
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		wantErr(t, err, `Bright Data Web Unlocker error 500`)
		wantErr(t, err, `\[redacted\]`)
		if strings.Contains(err.Error(), "bd-bound") {
			t.Fatal(err)
		}
	})

	tw(t, f, "Bright Data Web Unlocker redacts transport errors and preserves the error name", func(t *testing.T) {
		adapted(t, "a Go error has no JavaScript name to preserve; the redaction and the error itself are asserted")
		bdUnlock(t, map[string]string{"BRIGHTDATA_API_KEY": "bd-secret", "BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker"}, func(netCall) netReply {
			return netReply{Err: errors.New("socket hang up while sending token bd-secret")}
		})
		res, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		if res != nil {
			t.Fatal("a result")
		}
		wantErr(t, err, `socket hang up`)
		wantErr(t, err, `\[redacted\]`)
		if strings.Contains(err.Error(), "bd-secret") {
			t.Fatal(err)
		}
	})

	tw(t, f, "Bright Data Web Unlocker returns null only for genuinely empty content", func(t *testing.T) {
		bodies := []string{"", "   \n\t  ", "Paywalled: subscribe to continue reading."}
		i := 0
		bdUnlock(t, bdUnlocker, func(netCall) netReply { i++; return reply(200, bodies[i-1]) })
		var results []*ExtractedContent
		for range bodies {
			r, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
			noErr(t, err)
			results = append(results, r)
		}
		if results[0] != nil || results[1] != nil || !reflect.DeepEqual(*results[2], ExtractedContent{URL: "https://example.com/a", Content: "Paywalled: subscribe to continue reading."}) {
			t.Fatalf("%+v", results)
		}
	})

	tw(t, f, "Bright Data Web Unlocker propagates cancellation instead of returning null", func(t *testing.T) {
		bdUnlock(t, bdUnlocker, func(netCall) netReply { return reply(200, "# Never returned") })
		ctx, cancel := context.WithCancel(bg())
		cancel()
		res, err := ExtractWithBrightDataUnlocker(ctx, "https://example.com/a", lookup, SsrfConfig{})
		if res != nil || err == nil || !isAbortError(err) {
			t.Fatalf("%+v %v", res, err)
		}
	})

	tw(t, f, "Bright Data Web Unlocker uses fetch_content's timeoutMs and its own 60s default otherwise", func(t *testing.T) {
		fn := bdUnlock(t, bdUnlocker, func(netCall) netReply { return reply(200, "# Body") })
		remaining := func(c netCall) time.Duration {
			dl, ok := c.Ctx.Deadline()
			if !ok {
				t.Fatal("no deadline")
			}
			return time.Until(dl)
		}
		ms := int64(12345)
		o := lookup
		o.TimeoutMs = &ms
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", o, SsrfConfig{})
		noErr(t, err)
		_, err = ExtractWithBrightDataUnlocker(bg(), "https://example.com/b", lookup, SsrfConfig{})
		noErr(t, err)
		if r := remaining(fn.calls[0]); r < 10*time.Second || r > 13*time.Second {
			t.Fatalf("explicit: %v", r)
		}
		if r := remaining(fn.calls[1]); r < 55*time.Second || r > 61*time.Second {
			t.Fatalf("default: %v", r)
		}
		useNet(t, func(netCall) netReply { return netReply{Hang: true} })
		tiny := int64(25)
		o.TimeoutMs = &tiny
		res, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/c", o, SsrfConfig{})
		if res != nil || err == nil {
			t.Fatalf("%+v %v", res, err)
		}
	})

	tw(t, f, "fetch_content tries Bright Data after the direct fetch and Jina Reader, and returns before Gemini", func(t *testing.T) {
		extractEnv(t, `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"pi_unlocker","fetchRouting":{"allowRemoteHostedProviders":true}}`)
		n := 0
		fn := useNet(t, func(c netCall) netReply {
			n++
			switch {
			case n == 1:
				return reply(403, "blocked")
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(500, "nope")
			}
			return reply(200, "# Unblocked\n\nUnblocked body.")
		})
		r := ExtractContent(bg(), "https://example.com/protected", lookup)
		if !eqStrings(fn.urls(), []string{"https://example.com/protected", "https://r.jina.ai/https://example.com/protected", "https://api.brightdata.com/request"}) ||
			!reflect.DeepEqual(r, ExtractedContent{URL: "https://example.com/protected", Title: "Unblocked", Content: "# Unblocked\n\nUnblocked body."}) {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
	})

	tw(t, f, "Bright Data Web Unlocker failures stay visible in fetch_content guidance", func(t *testing.T) {
		extractEnv(t, `{"brightdataApiKey":"bd-secret","brightdataUnlockerZone":"pi_unlocker","fetchRouting":{"allowRemoteHostedProviders":true}}`)
		n := 0
		useNet(t, func(c netCall) netReply {
			n++
			switch {
			case n == 1:
				return reply(403, "blocked")
			case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
				return reply(500, "nope")
			}
			return reply(402, "payment required for token bd-secret")
		})
		r := ExtractContent(bg(), "https://example.com/paywalled", lookup)
		matchRE(t, `Bright Data fallback failed: Bright Data Web Unlocker error 402`, errOf(r))
		matchRE(t, `Set brightdataApiKey and brightdataUnlockerZone in .*web-search\.json or BRIGHTDATA_API_KEY and BRIGHTDATA_UNLOCKER_ZONE`, errOf(r))
		if strings.Contains(errOf(r), "bd-secret") {
			t.Fatal(errOf(r))
		}
	})

	tw(t, f, "Bright Data Web Unlocker rejects a malformed zone and a malformed config root", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"https://zone.example.com"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, "# Should never happen") })
		available := IsBrightDataUnlockerAvailable()
		_, err := ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		if available || fn.count() != 0 {
			t.Fatalf("%v %d", available, fn.count())
		}
		wantErr(t, err, `Invalid Bright Data Unlocker zone: brightdataUnlockerZone in .*web-search\.json`)

		bdSetup(t, "[]\n", nil)
		_, err = ExtractWithBrightDataUnlocker(bg(), "https://example.com/a", lookup, SsrfConfig{})
		wantErr(t, err, `Invalid config in .*web-search\.json: expected a JSON object`)
	})

	tw(t, f, "a private Bright Data endpoint mirror requires an explicit narrow SSRF range", func(t *testing.T) {
		for _, allow := range []bool{false, true} {
			cfg := `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"pi_unlocker","fetchRouting":{"allowRemoteHostedProviders":true}`
			if allow {
				cfg += `,"ssrf":{"allowRanges":["127.0.0.0/8"]}`
			}
			extractEnv(t, cfg+"}")
			n := 0
			fn := useNet(t, func(c netCall) netReply {
				n++
				switch {
				case n == 1:
					return reply(403, "blocked")
				case strings.HasPrefix(c.URL, "https://r.jina.ai/"):
					return reply(500, "nope")
				case c.URL == "https://api.brightdata.com/request":
					return netReply{Status: 307, Header: http.Header{"Location": {"http://127.0.0.1:3002/request"}}}
				}
				return reply(200, "# Local mirror")
			})
			r := ExtractContent(bg(), "https://example.com/protected", lookup)
			urls := fn.urls()
			if allow {
				if len(urls) < 4 || !eqStrings(urls[2:], []string{"https://api.brightdata.com/request", "http://127.0.0.1:3002/request"}) || r.Content != "# Local mirror" {
					t.Fatalf("%v %+v", urls, r)
				}
			} else {
				if len(urls) < 3 || !eqStrings(urls[2:], []string{"https://api.brightdata.com/request"}) {
					t.Fatalf("%v", urls)
				}
				matchRE(t, `Bright Data fallback failed: Blocked internal address`, errOf(r))
			}
		}
	})
}
