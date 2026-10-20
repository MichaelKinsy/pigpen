package websearch

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Twin of test/crawl4ai-extraction. The activity-log assertions of the upstream cases have no
// counterpart: this port keeps no activity log (the activity widget belongs to the curator slice).

func c4aOK(md string) netReply {
	return jsonReply(map[string]any{"success": true, "markdown": md})
}

func c4aEnv(t *testing.T, base string) {
	t.Helper()
	isolate(t)
	t.Setenv("CRAWL4AI_BASE_URL", base)
}

func TestUpstream_crawl4ai_extraction(t *testing.T) {
	const f = "crawl4ai-extraction"
	lookup := pageOpts()

	tw(t, f, "Crawl4AI extraction posts a fit markdown request with a bearer token and titles from the first heading", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com/")
		t.Setenv("CRAWL4AI_API_TOKEN", "c4a-test-token")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"url": "https://example.com/article", "filter": "fit", "markdown": "# Example Domain\nThis domain is for use in documentation examples.\n", "success": true})
		})
		res, err := ExtractWithCrawl4ai(bg(), "https://example.com/article", lookup, nil)
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != "https://crawl.example.com/md" || c.Header.Get("Authorization") != "Bearer c4a-test-token" || c.Header.Get("Content-Type") != "application/json" ||
			!reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/article", "f": "fit"}) {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/article", Title: "Example Domain", Content: "# Example Domain\nThis domain is for use in documentation examples."}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Crawl4AI extraction sends no Authorization header without a token and returns null for empty markdown", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		fn := useNet(t, func(netCall) netReply { return c4aOK("  \n") })
		res, err := ExtractWithCrawl4ai(bg(), "https://example.com/empty", lookup, nil)
		noErr(t, err)
		if _, has := fn.calls[0].Header["Authorization"]; has || res != nil {
			t.Fatalf("%v %+v", fn.calls[0].Header, res)
		}
	})

	tw(t, f, "Crawl4AI resolves the token from a config credential source", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"crawl4aiBaseUrl":"https://crawl.example.com","crawl4aiApiToken":"$CRAWL4AI_TEST_TOKEN"}`)
		t.Setenv("CRAWL4AI_TEST_TOKEN", "from-env-source")
		fn := useNet(t, func(netCall) netReply { return c4aOK("# Configured") })
		res, err := ExtractWithCrawl4ai(bg(), "https://example.com/configured", lookup, nil)
		noErr(t, err)
		if fn.calls[0].Header.Get("Authorization") != "Bearer from-env-source" || res.Title != "Configured" {
			t.Fatalf("%v %+v", fn.calls[0].Header, res)
		}
	})

	tw(t, f, "fetch_content falls back to configured Crawl4AI before hosted providers", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return c4aOK("# Rendered\nRendered body")
		})
		r := ExtractContent(bg(), "https://example.com/client-rendered", lookup)
		if !eqStrings(fn.urls(), []string{"https://example.com/client-rendered", "https://crawl.example.com/md"}) ||
			!reflect.DeepEqual(r, ExtractedContent{URL: "https://example.com/client-rendered", Title: "Rendered", Content: "# Rendered\nRendered body"}) {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
	})

	tw(t, f, "fetch_content tries Firecrawl before Crawl4AI when both are configured", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		t.Setenv("FIRECRAWL_BASE_URL", "https://firecrawl.example.com")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			switch n {
			case 1:
				return reply(403, "blocked")
			case 2:
				return reply(502, "gateway")
			}
			return c4aOK("# Second\nSecond body")
		})
		r := ExtractContent(bg(), "https://example.com/order", lookup)
		if !eqStrings(fn.urls(), []string{"https://example.com/order", "https://firecrawl.example.com/v2/scrape", "https://crawl.example.com/md"}) || r.Content != "# Second\nSecond body" {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
	})

	tw(t, f, "Crawl4AI extraction errors remain visible in fetch_content guidance", func(t *testing.T) {
		extractEnv(t, `{"crawl4aiBaseUrl":"https://crawl.example.com"}`)
		n := 0
		useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return reply(502, "gateway")
		})
		r := ExtractContent(bg(), "https://example.com/client-rendered", lookup)
		matchRE(t, `Crawl4AI fallback failed: Crawl4AI md error 502`, errOf(r))
		matchRE(t, `Set crawl4aiBaseUrl in`, errOf(r))
	})

	tw(t, f, "Crawl4AI unsuccessful envelopes mentioning abort remain visible in fetch_content guidance", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return jsonReply(map[string]any{"success": false, "error": "browser navigation aborted unexpectedly"})
		})
		r := ExtractContent(bg(), "https://example.com/client-rendered", lookup)
		if !eqStrings(fn.urls(), []string{"https://example.com/client-rendered", "https://crawl.example.com/md"}) || errOf(r) == "Aborted" {
			t.Fatalf("%v %v", fn.urls(), errOf(r))
		}
		matchRE(t, `Crawl4AI fallback failed: Crawl4AI md unsuccessful: browser navigation aborted unexpectedly`, errOf(r))
	})

	tw(t, f, "Crawl4AI malformed and unsuccessful envelopes throw visible errors", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		responses := []string{`[]`, `{"markdown":"# No success flag"}`, `{"success":true}`, `{"success":false,"error":"browser crashed"}`, `{"success":false,"detail":"Authentication required"}`}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, responses[i-1]) })
		var errs []string
		for range responses {
			_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
			if err == nil {
				t.Fatal("no error")
			}
			errs = append(errs, err.Error())
		}
		want := []string{"Crawl4AI md returned an unexpected response shape", "Crawl4AI md returned an unexpected response shape", "Crawl4AI md returned markdown in an unexpected shape",
			"Crawl4AI md unsuccessful: browser crashed", "Crawl4AI md unsuccessful: Authentication required"}
		if !eqStrings(errs, want) {
			t.Fatalf("%v", errs)
		}
	})

	tw(t, f, "Crawl4AI never leaks the configured token through a JSON parse failure", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		t.Setenv("CRAWL4AI_API_TOKEN", "c4a-secret")
		useNet(t, func(netCall) netReply { return reply(200, "c4a-secret") })
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		if err == nil || err.Error() != "Crawl4AI md returned invalid JSON" {
			t.Fatal(err)
		}
	})

	tw(t, f, "Crawl4AI redacts a token that crosses the HTTP error excerpt boundary", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		t.Setenv("CRAWL4AI_API_TOKEN", "boundary-secret-token")
		useNet(t, func(netCall) netReply { return reply(502, strings.Repeat("x", 295)+"boundary-secret-token"+"tail") })
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		wantErr(t, err, `^Crawl4AI md error 502: `)
		if strings.Contains(err.Error(), "bound") {
			t.Fatal(err)
		}
	})

	tw(t, f, "Crawl4AI rejects private targets without invoking the configured instance", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		fn := useNet(t, func(netCall) netReply { return reply(200, "{}") })
		errs := 0
		for _, target := range []string{"http://127.0.0.1:8080/admin", "http://localhost:8080/admin", "http://169.254.169.254/latest/meta-data"} {
			if _, err := ExtractWithCrawl4ai(bg(), target, ExtractOptions{}, nil); err != nil {
				errs++
			}
		}
		internal := func(context.Context, string) ([]LookupAddress, error) {
			return []LookupAddress{{Address: "10.0.0.5", Family: 4}}, nil
		}
		if _, err := ExtractWithCrawl4ai(bg(), "https://internal.example.com/", ExtractOptions{Lookup: internal}, nil); err != nil {
			errs++
		}
		if errs != 4 || fn.count() != 0 {
			t.Fatalf("%d errors, %d calls", errs, fn.count())
		}
	})

	tw(t, f, "Crawl4AI API base allows configured loopback without global SSRF allow ranges", func(t *testing.T) {
		for _, base := range []string{"http://localhost:11235", "http://127.0.0.1:11235"} {
			_, dir := isolate(t)
			writeConfig(t, dir, fmt.Sprintf(`{"crawl4aiBaseUrl":%q}`, base))
			fn := useNet(t, func(netCall) netReply { return c4aOK("# Local\nLocal body") })
			res, err := ExtractWithCrawl4ai(bg(), "https://example.com/local", lookup, nil)
			noErr(t, err)
			if !eqStrings(fn.urls(), []string{base + "/md"}) || res.Content != "# Local\nLocal body" {
				t.Fatalf("%v %+v", fn.urls(), res)
			}
		}
	})

	tw(t, f, "Crawl4AI rejects every cross-origin redirect before fetching the redirected origin", func(t *testing.T) {
		for _, status := range []int{301, 302, 303, 307, 308} {
			c4aEnv(t, "https://crawl.example.com")
			t.Setenv("CRAWL4AI_API_TOKEN", "c4a-secret")
			n := 0
			fn := useNet(t, func(netCall) netReply {
				n++
				if n == 1 {
					return netReply{Status: status, Header: http.Header{"Location": {"https://other.example.com/md"}}}
				}
				return c4aOK("# Redirected")
			})
			_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
			wantErr(t, err, `Crawl4AI refused cross-origin redirect to https://other\.example\.com`)
			c := fn.calls[0]
			if len(fn.calls) != 1 || c.URL != "https://crawl.example.com/md" || c.Method != "POST" || !reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/a", "f": "fit"}) || c.Header.Get("Authorization") != "Bearer c4a-secret" {
				t.Fatalf("%d: %v", status, fn.calls)
			}
		}
	})

	tw(t, f, "Crawl4AI replays same-origin 301 and 302 POSTs but rejects a later cross-origin redirect", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		t.Setenv("CRAWL4AI_API_TOKEN", "c4a-secret")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			switch n {
			case 1:
				return redirectTo(302, "https://crawl.example.com/api/md")
			case 2:
				return redirectTo(301, "https://other.example.com/md")
			}
			return c4aOK("# Redirected")
		})
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		wantErr(t, err, `Crawl4AI refused cross-origin redirect to https://other\.example\.com`)
		if len(fn.calls) != 2 {
			t.Fatalf("%v", fn.urls())
		}
		for i, u := range []string{"https://crawl.example.com/md", "https://crawl.example.com/api/md"} {
			c := fn.calls[i]
			if c.URL != u || c.Method != "POST" || !reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/a", "f": "fit"}) || c.Header.Get("Authorization") != "Bearer c4a-secret" {
				t.Fatalf("%d: %v", i, c)
			}
		}
	})

	tw(t, f, "Crawl4AI follows a 303 as a bodyless GET and keeps the POST on 307", func(t *testing.T) {
		second := func(status int) netCall {
			c4aEnv(t, "https://crawl.example.com")
			n := 0
			fn := useNet(t, func(netCall) netReply {
				n++
				if n == 1 {
					return redirectTo(status, "https://crawl.example.com/elsewhere")
				}
				return c4aOK("# Followed")
			})
			_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
			noErr(t, err)
			return fn.calls[1]
		}
		seeOther, temporary := second(303), second(307)
		if seeOther.URL != "https://crawl.example.com/elsewhere" || seeOther.Method != "GET" || seeOther.Body != "" ||
			temporary.URL != "https://crawl.example.com/elsewhere" || temporary.Method != "POST" || temporary.Body == "" {
			t.Fatalf("%+v %+v", seeOther, temporary)
		}
	})

	tw(t, f, "Crawl4AI does not resurrect the POST when a 303 is followed by a 301", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			switch n {
			case 1:
				return redirectTo(303, "https://crawl.example.com/step")
			case 2:
				return redirectTo(301, "https://crawl.example.com/final")
			}
			return c4aOK("# Followed")
		})
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		noErr(t, err)
		type hop struct {
			url, method string
			body        bool
		}
		var got []hop
		for _, c := range fn.calls {
			got = append(got, hop{c.URL, c.Method, c.Body != ""})
		}
		want := []hop{{"https://crawl.example.com/md", "POST", true}, {"https://crawl.example.com/step", "GET", false}, {"https://crawl.example.com/final", "GET", false}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v", got)
		}
	})

	tw(t, f, "Crawl4AI keeps the loopback exemption on the configured origin and blocks a pivot to another loopback service", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"crawl4aiBaseUrl":"http://127.0.0.1:11235"}`)
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			switch n {
			case 1:
				return redirectTo(302, "http://127.0.0.1:11235/api/md")
			case 2:
				return redirectTo(302, "http://127.0.0.2:9999/private")
			}
			return c4aOK("# Pivoted")
		})
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		wantErr(t, err, `Blocked internal address`)
		if !eqStrings(fn.urls(), []string{"http://127.0.0.1:11235/md", "http://127.0.0.1:11235/api/md"}) || fn.calls[1].Method != "POST" || fn.calls[1].Body != fn.calls[0].Body {
			t.Fatalf("%v", fn.calls)
		}
	})

	tw(t, f, "Crawl4AI blocks a loopback base redirecting to localhost", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"crawl4aiBaseUrl":"http://127.0.0.1:11235"}`)
		fn := useNet(t, func(netCall) netReply { return redirectTo(302, "http://localhost:11235/md") })
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		wantErr(t, err, `Blocked internal hostname`)
		if !eqStrings(fn.urls(), []string{"http://127.0.0.1:11235/md"}) {
			t.Fatal(fn.urls())
		}
	})

	tw(t, f, "Crawl4AI blocks configured base redirects to private targets", func(t *testing.T) {
		c4aEnv(t, "https://crawl.example.com")
		fn := useNet(t, func(netCall) netReply { return redirectTo(302, "http://127.0.0.1/admin") })
		_, err := ExtractWithCrawl4ai(bg(), "https://example.com/a", lookup, nil)
		wantErr(t, err, `Blocked internal address`)
		if !eqStrings(fn.urls(), []string{"https://crawl.example.com/md"}) {
			t.Fatal(fn.urls())
		}
	})
}
