package websearch

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// Twin of test/firecrawl-extraction.

func fcOK(data any) netReply { return jsonReply(map[string]any{"success": true, "data": data}) }

func TestUpstream_firecrawl_extraction(t *testing.T) {
	const f = "firecrawl-extraction"
	pageLookup := pageOpts()

	tw(t, f, "Firecrawl extraction maps markdown, uses credentials, and defaults to lockdown", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com/")
		t.Setenv("FIRECRAWL_API_KEY", "fc-test-key")
		fn := useNet(t, func(netCall) netReply {
			return fcOK(map[string]any{"markdown": "# Body", "metadata": map[string]any{"title": "Title"}})
		})
		res, err := ExtractWithFirecrawl(bg(), "https://example.com/article", pageLookup, nil)
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != "https://crawl.example.com/v2/scrape" || c.Header.Get("Authorization") != "Bearer fc-test-key" ||
			!reflect.DeepEqual(jsonBody(t, c), map[string]any{"url": "https://example.com/article", "formats": []any{"markdown"}, "onlyMainContent": true, "lockdown": true}) {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(*res, ExtractedContent{URL: "https://example.com/article", Title: "Title", Content: "# Body"}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Firecrawl search maps shared options and inline Markdown", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com/")
		t.Setenv("FIRECRAWL_API_KEY", "fc-test-key")
		fn := useNet(t, func(netCall) netReply {
			return fcOK(map[string]any{"web": []map[string]any{
				{"title": "Docs", "description": "Firecrawl docs", "url": "https://docs.firecrawl.dev/search", "markdown": "# Firecrawl Search"},
				{"title": "Blocked", "description": "Blocked", "url": "https://spam.example/result", "markdown": "spam"},
			}})
		})
		res, err := Search(bg(), "firecrawl docs", FullSearchOptions{Provider: ProviderSelection{Name: "firecrawl"}, SearchOptions: SearchOptions{
			DomainFilter: []string{"https://docs.firecrawl.dev/search", "-spam.example"}, RecencyFilter: "week", NumResults: nf(4), IncludeContent: true, Lookup: pubLookup}})
		noErr(t, err)
		c := fn.calls[0]
		want := map[string]any{"query": "firecrawl docs", "limit": 4.0, "sources": []any{"web"}, "includeDomains": []any{"docs.firecrawl.dev"}, "tbs": "qdr:w",
			"scrapeOptions": map[string]any{"formats": []any{"markdown"}, "onlyMainContent": true, "lockdown": true}}
		if c.URL != "https://crawl.example.com/v2/search" || c.Header.Get("Authorization") != "Bearer fc-test-key" || !reflect.DeepEqual(jsonBody(t, c), want) {
			t.Fatalf("%v", c)
		}
		if res.Provider != "firecrawl" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Docs", URL: "https://docs.firecrawl.dev/search", Snippet: "Firecrawl docs"}}) ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://docs.firecrawl.dev/search", Title: "Docs", Content: "# Firecrawl Search"}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Firecrawl search accepts v1 flat result arrays", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com/")
		t.Setenv("FIRECRAWL_API_VERSION", "v1")
		fn := useNet(t, func(netCall) netReply {
			return fcOK([]map[string]any{{"title": "V1 result", "description": "V1 snippet", "url": "https://example.com/v1", "markdown": "# V1"}})
		})
		res, err := Search(bg(), "v1 firecrawl", FullSearchOptions{Provider: ProviderSelection{Name: "firecrawl"}, SearchOptions: SearchOptions{IncludeContent: true, Lookup: pubLookup}})
		noErr(t, err)
		if fn.calls[0].URL != "https://crawl.example.com/v1/search" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "V1 result", URL: "https://example.com/v1", Snippet: "V1 snippet"}}) ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://example.com/v1", Title: "V1 result", Content: "# V1"}}) {
			t.Fatalf("%v %+v", fn.calls[0].URL, res)
		}
	})

	tw(t, f, "Firecrawl API base allows configured loopback without global SSRF allow ranges", func(t *testing.T) {
		for _, base := range []string{"http://localhost:3002", "http://127.0.0.1:3002"} {
			_, dir := isolate(t)
			writeConfig(t, dir, fmt.Sprintf(`{"firecrawlBaseUrl":%q}`, base))
			fn := useNet(t, func(netCall) netReply {
				return fcOK(map[string]any{"web": []map[string]any{{"title": "Local", "url": "https://example.com/local", "description": "local"}}})
			})
			res, err := Search(bg(), "local firecrawl", FullSearchOptions{Provider: ProviderSelection{Name: "firecrawl"}})
			noErr(t, err)
			if !eqStrings(fn.urls(), []string{base + "/v2/search"}) || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Local", URL: "https://example.com/local", Snippet: "local"}}) {
				t.Fatalf("%v %+v", fn.urls(), res)
			}
		}
	})

	tw(t, f, "Firecrawl search honors configured SSRF allow ranges", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"firecrawlBaseUrl":"http://127.0.0.1:3002","ssrf":{"allowRanges":["127.0.0.0/8"]}}`)
		fn := useNet(t, func(netCall) netReply {
			return fcOK(map[string]any{"web": []map[string]any{{"title": "Local", "url": "https://example.com/local", "description": "local"}}})
		})
		res, err := Search(bg(), "local firecrawl", FullSearchOptions{Provider: ProviderSelection{Name: "firecrawl"}})
		noErr(t, err)
		if !eqStrings(fn.urls(), []string{"http://127.0.0.1:3002/v2/search"}) || len(res.Results) != 1 {
			t.Fatalf("%v %+v", fn.urls(), res)
		}
	})

	tw(t, f, "Firecrawl fresh scraping is explicit opt-in", func(t *testing.T) {
		for _, env := range []map[string]string{{}, {"FIRECRAWL_FRESH_SCRAPE": "1"}} {
			_, dir := isolate(t)
			writeConfig(t, dir, `{"firecrawlBaseUrl":"https://crawl.example.com","firecrawlFreshScrape":true}`)
			for k, v := range env {
				t.Setenv(k, v)
			}
			fn := useNet(t, func(netCall) netReply { return fcOK(map[string]any{"markdown": "Fresh body"}) })
			_, err := ExtractWithFirecrawl(bg(), "https://example.com/fresh", pageLookup, nil)
			noErr(t, err)
			if !reflect.DeepEqual(jsonBody(t, fn.calls[0]), map[string]any{"url": "https://example.com/fresh", "formats": []any{"markdown"}, "onlyMainContent": true}) {
				t.Fatalf("%v", fn.calls[0].Body)
			}
		}
	})

	tw(t, f, "private Firecrawl base URLs require an explicit narrow SSRF range", func(t *testing.T) {
		extractEnv(t, `{"firecrawlBaseUrl":"http://127.0.0.1:3002","ssrf":{"allowRanges":["127.0.0.0/8"]}}`)
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return fcOK(map[string]any{"markdown": "Local body", "metadata": map[string]any{"title": "Local"}})
		})
		r := ExtractContent(bg(), "https://example.com/article", pageLookup)
		urls := fn.urls()
		if len(urls) < 2 || !eqStrings(urls[:2], []string{"https://example.com/article", "http://127.0.0.1:3002/v2/scrape"}) || r.Content != "Local body" {
			t.Fatalf("%v %+v", urls, r)
		}
	})

	tw(t, f, "Firecrawl rejects private targets without invoking the configured instance", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
		fn := useNet(t, func(netCall) netReply { return reply(200, "{}") })
		errs := 0
		for _, target := range []string{"http://127.0.0.1:8080/admin", "http://localhost:8080/admin", "http://169.254.169.254/latest/meta-data"} {
			if _, err := ExtractWithFirecrawl(bg(), target, ExtractOptions{}, nil); err != nil {
				errs++
			}
		}
		internal := func(context.Context, string) ([]LookupAddress, error) {
			return []LookupAddress{{Address: "10.0.0.5", Family: 4}}, nil
		}
		if _, err := ExtractWithFirecrawl(bg(), "https://internal.example.com/", ExtractOptions{Lookup: internal}, nil); err != nil {
			errs++
		}
		if errs != 4 || fn.count() != 0 {
			t.Fatalf("%d errors, %d calls", errs, fn.count())
		}
	})

	tw(t, f, "Firecrawl validates configured base redirects and strips auth on public cross-origin redirects", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
		t.Setenv("FIRECRAWL_API_KEY", "fc-secret")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return netReply{Status: 307, Header: http.Header{"Location": {"https://other.example.com/v2/scrape"}}}
			}
			return fcOK(map[string]any{"markdown": "Redirect body"})
		})
		_, err := ExtractWithFirecrawl(bg(), "https://example.com/a", pageLookup, nil)
		noErr(t, err)
		if len(fn.calls) != 2 || fn.calls[0].URL != "https://crawl.example.com/v2/scrape" || fn.calls[0].Header.Get("Authorization") != "Bearer fc-secret" ||
			fn.calls[1].URL != "https://other.example.com/v2/scrape" || fn.calls[1].Header.Get("Authorization") != "" {
			t.Fatalf("%v", fn.calls)
		}
	})

	tw(t, f, "Firecrawl blocks configured base redirects to private targets", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
		fn := useNet(t, func(netCall) netReply { return redirectTo(302, "http://127.0.0.1/admin") })
		_, err := ExtractWithFirecrawl(bg(), "https://example.com/a", pageLookup, nil)
		wantErr(t, err, `Blocked internal address`)
		if !eqStrings(fn.urls(), []string{"https://crawl.example.com/v2/scrape"}) {
			t.Fatal(fn.urls())
		}
	})

	tw(t, f, "Firecrawl malformed success envelopes throw visible shape errors", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
		responses := []string{`{"success":true}`, `{"success":true,"data":null}`, `{"success":true,"data":{}}`}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, responses[i-1]) })
		var errs []string
		for range responses {
			_, err := ExtractWithFirecrawl(bg(), "https://example.com/a", pageLookup, nil)
			noErr2 := err != nil
			if !noErr2 {
				t.Fatal("no error")
			}
			errs = append(errs, err.Error())
		}
		want := []string{"Firecrawl scrape returned an unexpected data shape", "Firecrawl scrape returned an unexpected data shape", "Firecrawl scrape returned markdown in an unexpected shape"}
		if !eqStrings(errs, want) {
			t.Fatalf("%v", errs)
		}
	})

	tw(t, f, "fetch_content prefers configured Firecrawl before hosted fallbacks", func(t *testing.T) {
		isolate(t)
		t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
		n := 0
		fn := useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return fcOK(map[string]any{"markdown": "Rendered body", "metadata": map[string]any{"title": "Rendered"}})
		})
		r := ExtractContent(bg(), "https://example.com/client-rendered", pageLookup)
		if !eqStrings(fn.urls(), []string{"https://example.com/client-rendered", "https://crawl.example.com/v2/scrape"}) ||
			!reflect.DeepEqual(r, ExtractedContent{URL: "https://example.com/client-rendered", Title: "Rendered", Content: "Rendered body"}) {
			t.Fatalf("%v %+v", fn.urls(), r)
		}
	})

	tw(t, f, "Firecrawl extraction errors remain visible in fetch_content guidance", func(t *testing.T) {
		extractEnv(t, `{"firecrawlBaseUrl":"https://crawl.example.com"}`)
		n := 0
		useNet(t, func(netCall) netReply {
			n++
			if n == 1 {
				return reply(403, "blocked")
			}
			return reply(502, "gateway")
		})
		r := ExtractContent(bg(), "https://example.com/client-rendered", pageLookup)
		matchRE(t, `Firecrawl fallback failed: Firecrawl scrape error 502`, errOf(r))
	})
}
