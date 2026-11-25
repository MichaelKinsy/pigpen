package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	id    string
	label string
	avail bool
	fn    func(context.Context, *http.Client, SearchParams) (SearchResponse, error)
}

func (f fakeProvider) ID() string        { return f.id }
func (f fakeProvider) Label() string     { return f.label }
func (f fakeProvider) IsAvailable() bool { return f.avail }
func (f fakeProvider) Search(ctx context.Context, client *http.Client, params SearchParams) (SearchResponse, error) {
	return f.fn(ctx, client, params)
}

// =============================================================================
// Query Pipeline (upstream: packages/coding-agent/test/web/search/query-pipeline.test.ts)
// =============================================================================

// upstream: packages/coding-agent/test/web/search/query-pipeline.test.ts: "returns an empty result, not off-site sources, when nothing matches a site: directive"
func TestQueryPipelineEmptyOnSiteScopeMiss(t *testing.T) {
	resetRateLimitCooldowns()
	sources := []SearchSource{
		{Title: "Docs page", URL: "https://docs.example.com/guide"},
		{Title: "Blog post", URL: "https://blog.other.com/post"},
	}
	prov := fakeProvider{
		id: "brave", label: "brave", avail: true,
		fn: func(context.Context, *http.Client, SearchParams) (SearchResponse, error) {
			return SearchResponse{Provider: "brave", Sources: sources}, nil
		},
	}

	text, details := executeSearchWithProviders(context.Background(), httpClient, []SearchProvider{prov}, SearchParams{
		Query: "guide site:nowhere.example",
	})
	if details.Error != "" {
		t.Fatalf("expected no error, got %q", details.Error)
	}
	if len(details.Response.Sources) != 0 {
		t.Fatalf("expected 0 sources, got %+v", details.Response.Sources)
	}
	if text != "No results from brave matched `site:nowhere.example`." {
		t.Fatalf("text = %q", text)
	}
}

// upstream: packages/coding-agent/test/web/search/query-pipeline.test.ts: "skips an engine that answered 429 on later calls instead of paying the rate limit again"
func TestQueryPipeline429CooldownRecordsSkippedNote(t *testing.T) {
	resetRateLimitCooldowns()
	defer resetRateLimitCooldowns()

	parallelCalls := 0
	p1 := fakeProvider{
		id: "parallel", label: "Parallel", avail: true,
		fn: func(context.Context, *http.Client, SearchParams) (SearchResponse, error) {
			parallelCalls++
			return SearchResponse{}, &SearchProviderError{
				Provider: "parallel",
				Message:  "parallel: MCP rate limit reached (429)",
				Status:   429,
			}
		},
	}
	p2 := fakeProvider{
		id: "exa", label: "Exa", avail: true,
		fn: func(context.Context, *http.Client, SearchParams) (SearchResponse, error) {
			return SearchResponse{
				Provider: "exa",
				Sources:  []SearchSource{{Title: "Docs page", URL: "https://docs.example.com/guide"}},
			}, nil
		},
	}

	_, _ = executeSearchWithProviders(context.Background(), httpClient, []SearchProvider{p1, p2}, SearchParams{Query: "first"})
	secondText, secondDetails := executeSearchWithProviders(context.Background(), httpClient, []SearchProvider{p1, p2}, SearchParams{Query: "second"})

	if parallelCalls != 1 {
		t.Fatalf("expected parallelCalls=1, got %d", parallelCalls)
	}
	if secondDetails.Response.Provider != "exa" {
		t.Fatalf("provider = %q, want exa", secondDetails.Response.Provider)
	}
	if !strings.Contains(secondText, "rate-limited (429)") {
		t.Fatalf("expected rate-limited (429) note in second call, got:\n%s", secondText)
	}
	if len(secondDetails.Failures) != 1 || !strings.Contains(secondDetails.Failures[0].Reason, "rate-limited (429)") {
		t.Fatalf("expected cooldown failure detail, got %+v", secondDetails.Failures)
	}
}

// upstream: packages/coding-agent/test/web/search/query-pipeline.test.ts: "routes a Hacker News-only query to the HN engine ahead of the role's engine"
// upstream: packages/coding-agent/test/web/search/query-pipeline.test.ts: "keeps the role's engine when another site shares the query"
func TestQueryPipelineHackerNewsRouting(t *testing.T) {
	resetRateLimitCooldowns()
	brave := fakeProvider{
		id: "brave", label: "Brave", avail: true,
		fn: func(context.Context, *http.Client, SearchParams) (SearchResponse, error) {
			return SearchResponse{
				Provider: "brave",
				Sources:  []SearchSource{{Title: "Docs", URL: "https://github.com/a"}},
			}, nil
		},
	}
	hn := fakeProvider{
		id: "hackernews", label: "Hacker News", avail: true,
		fn: func(context.Context, *http.Client, SearchParams) (SearchResponse, error) {
			return SearchResponse{
				Provider: "hackernews",
				Sources:  []SearchSource{{Title: "Thread", URL: "https://news.ycombinator.com/item?id=1"}},
			}, nil
		},
	}

	_, d1 := executeSearchWithProviders(context.Background(), httpClient, []SearchProvider{brave, hn}, SearchParams{
		Query: "tern site:news.ycombinator.com/item",
	})
	if d1.Response.Provider != "hackernews" {
		t.Fatalf("HN-only query routed to %q, want hackernews", d1.Response.Provider)
	}

	_, d2 := executeSearchWithProviders(context.Background(), httpClient, []SearchProvider{brave, hn}, SearchParams{
		Query: "tern site:news.ycombinator.com site:github.com",
	})
	if d2.Response.Provider != "brave" {
		t.Fatalf("multi-site query routed to %q, want brave", d2.Response.Provider)
	}

	// Subdomain must not be treated as exact news.ycombinator.com.
	if targetsHackerNews(ParseSearchQuery("site:foo.news.ycombinator.com")) {
		t.Fatalf("subdomain should not trigger HN-only routing")
	}
}

// =============================================================================
// Keenable (upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts)
// =============================================================================

func TestKeenableUpstreamCases(t *testing.T) {
	prevNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = prevNow }()

	samplePayload := map[string]any{
		"results": []map[string]any{
			{
				"title":        "Bun v1.4.3",
				"url":          "https://bun.com/blog/bun-v1.4.3",
				"snippet":      "Fixes 166 issues,\n  addressing 122 👍.",
				"published_at": "2026-10-30T10:00:00Z",
			},
			{"title": "", "url": "https://bun.com/docs", "snippet": ""},
			{"title": "dup", "url": "https://bun.com/blog/bun-v1.4.3", "snippet": "duplicate"},
			{"title": "bad", "url": "javascript:alert(1)", "snippet": "dropped"},
		},
	}

	type capturedReq struct {
		path    string
		headers http.Header
		body    map[string]any
	}

	runKeenable := func(t *testing.T, key string, params SearchParams, respPayload any) (SearchResponse, error, []capturedReq) {
		t.Helper()
		var captured []capturedReq
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			captured = append(captured, capturedReq{path: r.URL.Path, headers: r.Header.Clone(), body: b})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(respPayload)
		}))
		defer srv.Close()

		origKeyed, origPub := keenableSearchURL, keenablePublicSearchURL
		keenableSearchURL = srv.URL + "/v1/search"
		keenablePublicSearchURL = srv.URL + "/v1/search/public"
		defer func() {
			keenableSearchURL = origKeyed
			keenablePublicSearchURL = origPub
		}()
		t.Setenv("KEENABLE_API_KEY", key)

		resp, err := (keenableProvider{}).Search(context.Background(), srv.Client(), params)
		return resp, err, captured
	}

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "uses the keyed endpoint and X-API-Key when a key is stored"
	t.Run("uses the keyed endpoint and X-API-Key when a key is stored", func(t *testing.T) {
		resp, err, cap := runKeenable(t, "keen_test_key", SearchParams{Query: "bun runtime release notes"}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(cap) != 1 || cap[0].path != "/v1/search" || cap[0].headers.Get("X-API-Key") != "keen_test_key" || cap[0].headers.Get("X-Keenable-Title") != "" {
			t.Fatalf("unexpected request: %+v", cap)
		}
		if cap[0].body["query"] != "bun runtime release notes" || cap[0].body["max_results"] != float64(10) || resp.AuthMode != "api_key" {
			t.Fatalf("unexpected body=%v authMode=%q", cap[0].body, resp.AuthMode)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "falls back to the keyless public endpoint without a key"
	t.Run("falls back to the keyless public endpoint without a key", func(t *testing.T) {
		resp, err, cap := runKeenable(t, "", SearchParams{Query: "bun runtime release notes"}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cap[0].path != "/v1/search/public" || cap[0].headers.Get("X-Keenable-Title") != "oh-my-pi" || cap[0].headers.Get("X-API-Key") != "" || resp.AuthMode != "anonymous" {
			t.Fatalf("unexpected keyless request: %+v authMode=%q", cap[0], resp.AuthMode)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "maps results, dedupes URLs, and drops non-http URLs"
	t.Run("maps results, dedupes URLs, and drops non-http URLs", func(t *testing.T) {
		resp, err, _ := runKeenable(t, "", SearchParams{Query: "bun"}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(resp.Sources) != 2 {
			t.Fatalf("expected 2 sources, got %+v", resp.Sources)
		}
		if resp.Sources[0].Title != "Bun v1.4.3" || resp.Sources[0].Snippet != "Fixes 166 issues, addressing 122 👍." || resp.Sources[0].AgeSeconds == nil || *resp.Sources[0].AgeSeconds <= 0 {
			t.Fatalf("unexpected source[0]: %+v", resp.Sources[0])
		}
		if resp.Sources[1].Title != "https://bun.com/docs" || resp.Sources[1].Snippet != "" {
			t.Fatalf("unexpected source[1]: %+v", resp.Sources[1])
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "maps a single site:, date bounds, and phrases onto native fields"
	t.Run("maps a single site:, date bounds, and phrases onto native fields", func(t *testing.T) {
		_, err, cap := runKeenable(t, "", SearchParams{Query: `"error handling" rust site:github.com after:2025-01-01 before:2026-01-01`}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		want := map[string]any{
			"query":            `"error handling" rust`,
			"max_results":      float64(10),
			"site":             "github.com",
			"published_after":  "2025-01-01",
			"published_before": "2026-01-01",
		}
		if !reflect.DeepEqual(cap[0].body, want) {
			t.Fatalf("body = %#v, want %#v", cap[0].body, want)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "maps recency to a relative published_after and clamps the result count"
	t.Run("maps recency to a relative published_after and clamps the result count", func(t *testing.T) {
		_, err, cap := runKeenable(t, "", SearchParams{Query: "bun release", Recency: "week", NumSearchResults: 500}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cap[0].body["max_results"] != float64(50) || cap[0].body["published_after"] != "7d" {
			t.Fatalf("body = %#v", cap[0].body)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "never sends search operators as keywords"
	t.Run("never sends search operators as keywords", func(t *testing.T) {
		_, _, c1 := runKeenable(t, "", SearchParams{Query: "tokio site:github.com site:docs.rs"}, samplePayload)
		if c1[0].body["query"] != "tokio" || c1[0].body["site"] != nil {
			t.Fatalf("c1 = %#v", c1[0].body)
		}
		_, _, c2 := runKeenable(t, "", SearchParams{Query: "site:github.com site:docs.rs"}, samplePayload)
		if c2[0].body["query"] != "github.com docs.rs" {
			t.Fatalf("c2 = %#v", c2[0].body)
		}
		_, _, c3 := runKeenable(t, "", SearchParams{Query: "site:docs.rs/tokio"}, samplePayload)
		if c3[0].body["query"] != "docs.rs/tokio" || c3[0].body["site"] != "docs.rs" {
			t.Fatalf("c3 = %#v", c3[0].body)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "rejects a date-only query without calling the API"
	t.Run("rejects a date-only query without calling the API", func(t *testing.T) {
		_, err, cap := runKeenable(t, "", SearchParams{Query: "after:2025-01-01"}, samplePayload)
		var spe *SearchProviderError
		if !errors.As(err, &spe) || spe.Status != 400 || !strings.Contains(spe.Message, "Keenable needs search terms") || len(cap) != 0 {
			t.Fatalf("expected 400 without API call, got err=%v calls=%d", err, len(cap))
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-keenable.test.ts: "rejects an unexpected response shape"
	t.Run("rejects an unexpected response shape", func(t *testing.T) {
		_, err, _ := runKeenable(t, "", SearchParams{Query: "bun"}, map[string]any{"results": "nope"})
		var spe *SearchProviderError
		if !errors.As(err, &spe) || spe.Status != 500 || !strings.Contains(spe.Message, "unexpected response shape") {
			t.Fatalf("expected 500 unexpected shape, got %v", err)
		}
	})
}

// =============================================================================
// Sofya (upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts)
// =============================================================================

func TestSofyaUpstreamCases(t *testing.T) {
	prevNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = prevNow }()

	samplePayload := map[string]any{
		"results": []map[string]any{
			{
				"title":          "Bun v1.4.3",
				"url":            "https://bun.com/blog/bun-v1.4.3",
				"description":    "Fixes 166 issues,\n  addressing 122 👍.",
				"content":        "Full release notes follow.",
				"published_date": "2026-10-30",
			},
			{"title": "", "url": "https://bun.com/docs", "content": ""},
			{"title": "dup", "url": "https://bun.com/blog/bun-v1.4.3", "content": "duplicate"},
			{"title": "bad", "url": "javascript:alert(1)", "content": "dropped"},
		},
	}

	type capturedReq struct {
		headers http.Header
		body    map[string]any
	}

	runSofya := func(t *testing.T, key string, params SearchParams, respPayload any) (SearchResponse, error, []capturedReq) {
		t.Helper()
		var captured []capturedReq
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			captured = append(captured, capturedReq{headers: r.Header.Clone(), body: b})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(respPayload)
		}))
		defer srv.Close()

		orig := sofyaSearchURL
		sofyaSearchURL = srv.URL
		defer func() { sofyaSearchURL = orig }()
		t.Setenv("SOFYA_API_KEY", key)

		resp, err := (sofyaProvider{}).Search(context.Background(), srv.Client(), params)
		return resp, err, captured
	}

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "sends the key as a bearer token with a snippets-depth body"
	t.Run("sends the key as a bearer token with a snippets-depth body", func(t *testing.T) {
		resp, err, cap := runSofya(t, "sofya_key", SearchParams{Query: "bun runtime release notes"}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cap[0].headers.Get("Authorization") != "Bearer sofya_key" || cap[0].body["search_depth"] != "snippets" || resp.AuthMode != "api_key" {
			t.Fatalf("unexpected request: %+v", cap[0])
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "fails without a key and never calls the API"
	t.Run("fails without a key and never calls the API", func(t *testing.T) {
		_, err, cap := runSofya(t, "", SearchParams{Query: "bun"}, samplePayload)
		if err == nil || !strings.Contains(err.Error(), "Sofya credentials not found") || len(cap) != 0 {
			t.Fatalf("expected missing key error without call, got err=%v calls=%d", err, len(cap))
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "maps site:, -site:, an exclusive before: bound, phrases, and exclusions onto native fields"
	t.Run("maps site:, -site:, an exclusive before: bound, phrases, and exclusions onto native fields", func(t *testing.T) {
		_, err, cap := runSofya(t, "sofya_key", SearchParams{
			Query: `"error handling" rust -unsafe site:github.com -site:reddit.com after:2025-01-01 before:2026-01-01`,
		}, samplePayload)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cap[0].body["query"] != `"error handling" rust -unsafe` || cap[0].body["freshness"] != "2025-01-01:2025-12-31" {
			t.Fatalf("body = %#v", cap[0].body)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "closes an open date bound and prefers it over recency"
	t.Run("closes an open date bound and prefers it over recency", func(t *testing.T) {
		_, _, c1 := runSofya(t, "sofya_key", SearchParams{Query: "bun after:2025-01-01", Recency: "week"}, samplePayload)
		if c1[0].body["freshness"] != "2025-01-01:2026-11-24" {
			t.Fatalf("freshness1 = %v", c1[0].body["freshness"])
		}
		_, _, c2 := runSofya(t, "sofya_key", SearchParams{Query: "bun before:2025-01-01"}, samplePayload)
		if c2[0].body["freshness"] != "1970-01-01:2024-12-31" {
			t.Fatalf("freshness2 = %v", c2[0].body["freshness"])
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "reports a query Sofya relaxed or corrected"
	t.Run("reports a query Sofya relaxed or corrected", func(t *testing.T) {
		p1 := map[string]any{"results": samplePayload["results"], "relaxed_query": "bun", "altered_query": nil}
		r1, _, _ := runSofya(t, "sofya_key", SearchParams{Query: "bun site:bun.com"}, p1)
		if !reflect.DeepEqual(r1.SearchQueries, []string{"bun"}) {
			t.Fatalf("relaxed SearchQueries = %v", r1.SearchQueries)
		}
		p2 := map[string]any{"results": samplePayload["results"], "altered_query": "bun js"}
		r2, _, _ := runSofya(t, "sofya_key", SearchParams{Query: "bun"}, p2)
		if !reflect.DeepEqual(r2.SearchQueries, []string{"bun js"}) {
			t.Fatalf("altered SearchQueries = %v", r2.SearchQueries)
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "rejects a date-only query without calling the API"
	t.Run("rejects a date-only query without calling the API", func(t *testing.T) {
		_, err, cap := runSofya(t, "sofya_key", SearchParams{Query: "after:2025-01-01"}, samplePayload)
		var spe *SearchProviderError
		if !errors.As(err, &spe) || spe.Status != 400 || !strings.Contains(spe.Message, "Sofya needs search terms") || len(cap) != 0 {
			t.Fatalf("expected 400 without API call, got err=%v calls=%d", err, len(cap))
		}
	})

	// upstream: packages/coding-agent/test/tools/web-search-sofya.test.ts: "rejects an unexpected response shape"
	t.Run("rejects an unexpected response shape", func(t *testing.T) {
		_, err1, _ := runSofya(t, "sofya_key", SearchParams{Query: "bun"}, map[string]any{"detail": "nope"})
		_, err2, _ := runSofya(t, "sofya_key", SearchParams{Query: "bun"}, nil)
		for _, err := range []error{err1, err2} {
			var spe *SearchProviderError
			if !errors.As(err, &spe) || spe.Status != 500 || !strings.Contains(spe.Message, "unexpected response shape") {
				t.Fatalf("expected 500 unexpected shape, got %v", err)
			}
		}
	})
}

// =============================================================================
// Hacker News Search (upstream: packages/coding-agent/test/web/search/hackernews.test.ts)
// =============================================================================

func TestHackerNewsSearchUpstreamCases(t *testing.T) {
	captureHN := func(t *testing.T, storyHits, commentHits []map[string]any, status int) (*httptest.Server, *[]*url.URL) {
		t.Helper()
		var urls []*url.URL
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uCopy := *r.URL
			urls = append(urls, &uCopy)
			w.Header().Set("Content-Type", "application/json")
			if status != 200 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"Too many requests"}`))
				return
			}
			if r.URL.Query().Get("tags") == "comment" {
				_ = json.NewEncoder(w).Encode(map[string]any{"hits": commentHits})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": storyHits})
		}))
		t.Cleanup(srv.Close)
		orig := hnSearchURL
		hnSearchURL = srv.URL
		t.Cleanup(func() { hnSearchURL = orig })
		return srv, &urls
	}

	// upstream: packages/coding-agent/test/web/search/hackernews.test.ts: "maps phrases, exclusions, OR alternatives, field directives, and date bounds onto Algolia parameters"
	t.Run("maps phrases, exclusions, OR alternatives, field directives, and date bounds onto Algolia parameters", func(t *testing.T) {
		srv, urls := captureHN(t, nil, nil, 200)
		_, err := (hackerNewsProvider{}).Search(context.Background(), srv.Client(), SearchParams{
			Query: `"zig rewrite" (bun OR deno) -node intitle:compiler site:news.ycombinator.com after:2026-10-01 before:2026-10-08`,
			Limit: 6,
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(*urls) != 2 {
			t.Fatalf("expected 2 calls, got %d", len(*urls))
		}
		for i, tag := range []string{"story", "comment"} {
			q := (*urls)[i].Query()
			if q.Get("query") != `"zig rewrite" bun deno -node compiler` ||
				q.Get("advancedSyntax") != "true" ||
				q.Get("hitsPerPage") != "6" ||
				q.Get("optionalWords") != "bun deno" ||
				q.Get("numericFilters") != "created_at_i>=1790812800,created_at_i<1791417600" ||
				q.Get("tags") != tag {
				t.Fatalf("call %d params = %v", i, q)
			}
		}

		// Plain terms without OR must NOT populate optionalWords (regression for A5).
		*urls = nil
		_, _ = (hackerNewsProvider{}).Search(context.Background(), srv.Client(), SearchParams{
			Query: "rust async runtime site:news.ycombinator.com",
		})
		if got := (*urls)[0].Query().Get("optionalWords"); got != "" {
			t.Fatalf("plain query must not set optionalWords, got %q", got)
		}
	})

	// upstream: packages/coding-agent/test/web/search/hackernews.test.ts: "lists stories only, unfiltered by keyword or date, for a query that carries only a site directive"
	t.Run("lists stories only for site-only query", func(t *testing.T) {
		srv, urls := captureHN(t, []map[string]any{{"objectID": "s1", "title": "s1"}, {"objectID": "s2", "title": "s2"}}, nil, 200)
		resp, err := (hackerNewsProvider{}).Search(context.Background(), srv.Client(), SearchParams{Query: "site:news.ycombinator.com"})
		if err != nil || len(*urls) != 1 || (*urls)[0].Query().Get("tags") != "story" || (*urls)[0].Query().Get("query") != "" {
			t.Fatalf("unexpected site-only call: err=%v urls=%v", err, *urls)
		}
		if len(resp.Sources) != 2 || resp.Sources[0].Title != "s1" {
			t.Fatalf("unexpected sources: %+v", resp.Sources)
		}
	})

	// upstream: packages/coding-agent/test/web/search/hackernews.test.ts: "links HN item pages and renders story stats and comment HTML as text snippets"
	t.Run("links HN item pages and renders story stats and comment HTML as text snippets", func(t *testing.T) {
		prevNow := nowFunc
		nowFunc = func() time.Time { return time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC) }
		defer func() { nowFunc = prevNow }()

		srv, _ := captureHN(t,
			[]map[string]any{
				{"objectID": "101", "title": "Rewriting Bun in Rust", "url": "https://bun.sh/blog/rust", "author": "jarred", "points": 795, "num_comments": 534, "created_at": "2026-07-08T10:00:00Z"},
				{"objectID": "102", "title": "Ask HN: Why rewrite?", "author": "pg", "points": 12, "num_comments": 3, "story_text": "I&#x27;m curious.<p>Why <i>now</i>?"},
				{"title": "Hit without an id"},
			},
			[]map[string]any{
				{"objectID": "201", "author": "simonw", "story_title": "Cloudflare acquires Deno", "comment_text": `&gt; a full rewrite<p>Considering <a href="https:&#x2F;&#x2F;example.com&#x2F;x" rel="nofollow">this</a> was fast.`},
			},
			200,
		)
		resp, err := (hackerNewsProvider{}).Search(context.Background(), srv.Client(), SearchParams{Query: "bun rust rewrite"})
		if err != nil || len(resp.Sources) != 3 {
			t.Fatalf("err=%v sources=%+v", err, resp.Sources)
		}
		if resp.Sources[0].Snippet != "795 points, 534 comments, by jarred — https://bun.sh/blog/rust" || resp.Sources[0].AgeSeconds == nil || *resp.Sources[0].AgeSeconds != 3600 {
			t.Fatalf("source[0] = %+v", resp.Sources[0])
		}
		if resp.Sources[1].Snippet != "12 points, 3 comments, by pg — I'm curious. Why *now*?" {
			t.Fatalf("source[1] = %+v", resp.Sources[1])
		}
		if resp.Sources[2].Title != "Comment by simonw on: Cloudflare acquires Deno" || resp.Sources[2].Snippet != "> a full rewrite Considering [this](https://example.com/x) was fast." {
			t.Fatalf("source[2] = %+v", resp.Sources[2])
		}
	})

	// upstream: packages/coding-agent/test/web/search/hackernews.test.ts: "surfaces a non-2xx response as a provider error carrying the status"
	t.Run("surfaces a non-2xx response as a provider error carrying the status", func(t *testing.T) {
		srv, _ := captureHN(t, nil, nil, 429)
		_, err := (hackerNewsProvider{}).Search(context.Background(), srv.Client(), SearchParams{Query: "tern"})
		var spe *SearchProviderError
		if !errors.As(err, &spe) || spe.Provider != "hackernews" || spe.Status != 429 {
			t.Fatalf("expected SearchProviderError{hackernews, 429}, got %v", err)
		}
	})
}

// =============================================================================
// Keyless Providers: Parallel Public MCP, Firecrawl Keyless, DuckDuckGo HTML (A1)
// =============================================================================

func TestKeylessSearchProvidersWorkWithoutAPIKeys(t *testing.T) {
	t.Run("Parallel public MCP answers without PARALLEL_API_KEY (JSON and SSE)", func(t *testing.T) {
		clearProviderEnv(t)
		withSettings(t, map[string]any{"providers.webKeyless": true})
		for _, mode := range []string{"json", "sse"} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				payload := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"structuredContent": map[string]any{
							"search_id": "mcp-1",
							"results": []map[string]any{
								{"title": "Parallel MCP Result", "url": "https://example.com/mcp", "excerpts": []string{"MCP excerpt"}},
							},
						},
					},
				}
				b, _ := json.Marshal(payload)
				if mode == "sse" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: message\ndata: " + string(b) + "\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
			}))
			orig := parallelMCPURL
			parallelMCPURL = srv.URL
			prevClient := httpClient
			httpClient = srv.Client()

			text, details := ExecuteSearch(context.Background(), SearchParams{Query: "golang"})
			httpClient = prevClient
			parallelMCPURL = orig
			srv.Close()

			if details.Error != "" || details.Response.Provider != "parallel" || details.Response.AuthMode != "keyless" || !strings.Contains(text, "Parallel MCP Result") {
				t.Fatalf("mode=%s failed: details=%+v text=%q", mode, details, text)
			}
		}
	})

	t.Run("DuckDuckGo HTML provider parses results, unwraps uddg links, and skips ads", func(t *testing.T) {
		ddgHTML := `<html><body>
			<div class="result">
				<a class="result__a" href="//duckduckgo.com/y.js?ad=1">Ad</a>
			</div>
			<div class="result">
				<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F">The <b>Go</b> Docs</a>
				<a class="result__snippet">Official Go documentation &amp; tutorials.</a>
				<div class="result__extras__url"><span>&nbsp; 2026-07-30T20:19:00</span></div>
			</div>
		</body></html>`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(ddgHTML))
		}))
		defer srv.Close()
		orig := duckduckgoHTMLURL
		duckduckgoHTMLURL = srv.URL
		defer func() { duckduckgoHTMLURL = orig }()

		resp, err := (duckDuckGoProvider{}).Search(context.Background(), srv.Client(), SearchParams{Query: "go docs"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(resp.Sources) != 1 || resp.Sources[0].URL != "https://go.dev/doc/" || resp.Sources[0].Title != "The Go Docs" || resp.Sources[0].PublishedDate != "2026-07-30T20:19:00" {
			t.Fatalf("unexpected DDG sources: %+v", resp.Sources)
		}
	})
}

func TestFormatAgeYears(t *testing.T) {
	twoYears := 2 * 365 * 24 * 3600
	if got := formatAge(&twoYears); got != "2y ago" {
		t.Fatalf("formatAge(2y) = %q, want 2y ago", got)
	}
}

// =============================================================================
// Fetch v19.1.1: UA rotation, Bot Wall, Feed Fallback, Front-Matter Pruning (A2/A3)
// =============================================================================

func TestFetchUserAgentRotationAndBotWallAndAlternatesAndFrontMatter(t *testing.T) {
	article := `<!doctype html><html><head><title>Filing</title></head><body><main><h1>Quarterly report</h1><p>` +
		strings.Repeat("Revenue grew in the quarter while operating costs held steady across every segment. ", 4) +
		`</p></main></body></html>`

	// upstream: packages/coding-agent/test/tools/fetch-user-agent-retry.test.ts: "reads the page from the next user agent when a bare 403 page names no bot check"
	t.Run("rotates UA on bare 403", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("User-Agent"), "curl/") {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("<html><head><title>403 Forbidden</title></head><body><h1>403 Forbidden</h1></body></html>"))
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(article))
		}))
		defer srv.Close()

		out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/filing.htm", false, fetchMaxBytes)
		if err != nil || strings.Contains(out, "Method: failed") || !strings.Contains(out, "Revenue grew in the quarter") {
			t.Fatalf("bare 403 rotation failed: err=%v out:\n%s", err, out)
		}
	})

	// upstream: packages/coding-agent/test/tools/fetch-user-agent-retry.test.ts: "reads the page from the next user agent before waiting out a 429's Retry-After"
	t.Run("rotates UA before waiting on 429 Retry-After", func(t *testing.T) {
		slept := false
		prevSleep := fetchRetrySleep
		fetchRetrySleep = func(context.Context, time.Duration) error { slept = true; return nil }
		defer func() { fetchRetrySleep = prevSleep }()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("User-Agent"), "curl/") {
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("Too Many Requests"))
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(article))
		}))
		defer srv.Close()

		out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/filing.htm", false, fetchMaxBytes)
		if err != nil || slept || !strings.Contains(out, "Revenue grew in the quarter") {
			t.Fatalf("expected UA rotation without sleep: slept=%v err=%v out:\n%s", slept, err, out)
		}
	})

	// upstream: packages/coding-agent/test/tools/fetch-http-status.test.ts: "ends a 200 challenge read as $method when the remote reader gets $reader"
	t.Run("reports 200 js_challenge page as Method: blocked", func(t *testing.T) {
		challenge := `<!DOCTYPE html><html><head><title>Reddit</title></head><body><form><input type="hidden" name="js_challenge" value="1"/></form></body></html>`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(challenge))
		}))
		defer srv.Close()

		out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/post", false, fetchMaxBytes)
		if err != nil || !strings.Contains(out, "Method: blocked") || !strings.Contains(out, "JavaScript challenge, HTTP 200") {
			t.Fatalf("expected Method: blocked, got err=%v out:\n%s", err, out)
		}
		rawOut, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/post", true, fetchMaxBytes)
		if err != nil || rawOut != challenge {
			t.Fatalf("expected verbatim challenge on :raw, got %q", rawOut)
		}
	})

	// upstream: packages/coding-agent/test/tools/fetch-alternates.test.ts: "renders a post instead of substituting its page-specific feed"
	// upstream: packages/coding-agent/test/tools/fetch-alternates.test.ts: "falls back to a page-specific feed when the page renders to nothing"
	t.Run("uses feed alternate only as fallback when rendering fails", func(t *testing.T) {
		feedXML := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>APP_FEED</title><entry><title>Item</title><summary>` +
			strings.Repeat("Feed body text. ", 15) + `</summary></entry></feed>`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/posts/one":
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html><head><link rel="alternate" type="application/atom+xml" href="/posts/one/comments.xml"></head><body><article><p>POST_BODY ` + strings.Repeat("real article text ", 10) + `</p></article></body></html>`))
			case "/posts/one/comments.xml":
				w.Header().Set("Content-Type", "application/atom+xml")
				_, _ = w.Write([]byte(strings.ReplaceAll(feedXML, "APP_FEED", "POST_COMMENTS")))
			case "/app":
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<html><head><link rel="alternate" type="application/atom+xml" href="/app/index.xml"></head><body><div id="root"></div></body></html>`))
			case "/app/index.xml":
				w.Header().Set("Content-Type", "application/atom+xml")
				_, _ = w.Write([]byte(feedXML))
			}
		}))
		defer srv.Close()

		postOut, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/posts/one", false, fetchMaxBytes)
		if err != nil || !strings.Contains(postOut, "Method: native") || !strings.Contains(postOut, "POST_BODY") || strings.Contains(postOut, "POST_COMMENTS") {
			t.Fatalf("post must render natively, got:\n%s", postOut)
		}

		appOut, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/app", false, fetchMaxBytes)
		if err != nil || !strings.Contains(appOut, "URL: "+srv.URL+"/app/index.xml\n") || !strings.Contains(appOut, "Method: alternate-feed") || !strings.Contains(appOut, "APP_FEED") {
			t.Fatalf("empty app must fall back to feed alternate with alternate finalUrl, got:\n%s", appOut)
		}
	})

	t.Run("prunes non-reader meta tags from front matter", func(t *testing.T) {
		doc := `<!doctype html><html><head>` +
			`<title>Article</title>` +
			`<meta name="description" content="Keep me">` +
			`<meta property="og:image" content="https://example.com/img.png">` +
			`<meta name="viewport" content="width=device-width">` +
			`</head><body><p>` + strings.Repeat("Substantial body paragraph text for reader mode. ", 4) + `</p></body></html>`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(doc))
		}))
		defer srv.Close()

		out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/article", false, fetchMaxBytes)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out, "meta-description: Keep me") ||
			strings.Contains(out, "meta-og:image:") ||
			strings.Contains(out, "meta-viewport:") ||
			!strings.Contains(out, `meta_omitted: "og:image, viewport; read the URL with :raw for their values"`) {
			t.Fatalf("front matter pruning mismatch:\n%s", out)
		}
	})
}

// =============================================================================
// Site-Specific Scrapers Exact Format Parity (A4, B1)
// =============================================================================

func TestSiteScrapersExactFormatParity(t *testing.T) {
	prevNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = prevNow }()

	readmeB64 := base64.StdEncoding.EncodeToString([]byte("# Hello Repo\n\nBody text"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/owner/repo/main/README.md":
			_, _ = w.Write([]byte("raw file content\n"))
		case "/repos/owner/repo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"full_name": "owner/repo", "description": "A repo",
				"stargazers_count": 1200, "forks_count": 50, "open_issues_count": 7,
				"language": "Go", "license": map[string]any{"name": "MIT License"},
			})
		case "/repos/owner/repo/git/trees/HEAD":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tree": []map[string]any{
					{"path": "cmd", "type": "tree"},
					{"path": "go.mod", "type": "blob"},
				},
			})
		case "/repos/owner/repo/readme":
			_ = json.NewEncoder(w).Encode(map[string]any{"encoding": "base64", "content": readmeB64})
		case "/repos/owner/repo/issues/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "Fix crash", "number": 42, "state": "open",
				"user":       map[string]any{"login": "alice"},
				"created_at": "2026-07-01T00:00:00Z", "updated_at": "2026-07-02T00:00:00Z",
				"body": "Crash details", "labels": []map[string]any{{"name": "bug"}}, "comments": 1,
			})
		case "/repos/owner/repo/issues/42/comments":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"user": map[string]any{"login": "bob"}, "created_at": "2026-07-02T01:00:00Z", "body": "On it"},
			})
		case "/react/latest":
			// Deliberate reverse-alphabetical dependency keys to verify JSON insertion order is preserved (B1).
			_, _ = w.Write([]byte(`{"name":"react","version":"19.0.0","license":"MIT","dependencies":{"zeta":"1.0","alpha":"2.0"}}`))
		case "/downloads/point/last-week/react":
			_ = json.NewEncoder(w).Encode(map[string]any{"downloads": 25000000})
		case "/requests/json":
			_, _ = w.Write([]byte(`{"info":{"name":"requests","version":"2.32.0","project_urls":{"Source":"https://src","Docs":"https://docs"}}}`))
		case "/api/rest_v1/page/summary/Go_(programming_language)":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "Go (programming language)", "description": "Programming language", "extract": "Go is a statically typed language.",
			})
		case "/api/rest_v1/page/mobile-html/Go_(programming_language)":
			_, _ = w.Write([]byte(`<section><h2>History</h2><p>Go was designed at Google in 2007 to improve programming productivity.</p></section><section><h2>References</h2><p>Should be skipped because it is references.</p></section>`))
		case "/item/99.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 99, "title": "HN Story", "by": "pg", "score": 100,
				"time": nowFunc().Add(-2 * time.Hour).Unix(), "descendants": 2, "kids": []int{100},
			})
		case "/item/100.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 100, "by": "alice", "time": nowFunc().Add(-1 * time.Hour).Unix(),
				"text": "<p>Top comment</p>", "kids": []int{101},
			})
		case "/item/101.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 101, "by": "bob", "time": nowFunc().Add(-30 * time.Minute).Unix(),
				"text": "<p>Nested reply</p>",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origGHAPI, origGHRaw := githubAPIBase, githubRawBase
	origNpmReg, origNpmDL := npmRegistryBase, npmDownloadsBase
	origPyPI, origPyStats := pypiAPIBase, pypistatsAPIBase
	origWiki, origHN := wikiAPIBase, hnFirebaseBase
	githubAPIBase, githubRawBase = srv.URL, srv.URL
	npmRegistryBase, npmDownloadsBase = srv.URL, srv.URL
	pypiAPIBase, pypistatsAPIBase = srv.URL, srv.URL
	wikiAPIBase, hnFirebaseBase = srv.URL, srv.URL
	defer func() {
		githubAPIBase, githubRawBase = origGHAPI, origGHRaw
		npmRegistryBase, npmDownloadsBase = origNpmReg, origNpmDL
		pypiAPIBase, pypistatsAPIBase = origPyPI, origPyStats
		wikiAPIBase, hnFirebaseBase = origWiki, origHN
	}()

	// GitHub blob: raw URL, text/plain, Fetched raw: <url>
	blobOut, err := fetchWithClient(context.Background(), srv.Client(), "https://github.com/owner/repo/blob/main/README.md", false, fetchMaxBytes)
	wantBlobRaw := srv.URL + "/owner/repo/main/README.md"
	if err != nil || !strings.Contains(blobOut, "URL: "+wantBlobRaw+"\nContent-Type: text/plain\nMethod: github-raw\nNotes: Fetched raw: "+wantBlobRaw) {
		t.Fatalf("github blob mismatch:\n%s", blobOut)
	}

	// GitHub repo: omp exact format (Stars/Forks/Issues, ## Files tree, ## README)
	repoOut, err := fetchWithClient(context.Background(), srv.Client(), "https://github.com/owner/repo", false, fetchMaxBytes)
	for _, want := range []string{
		"Method: github-repo",
		"Stars: 1200 · Forks: 50 · Issues: 7",
		"## Files\n\n```\n[dir] cmd\n      go.mod\n```",
		"## README\n\n# Hello Repo",
	} {
		if err != nil || !strings.Contains(repoOut, want) {
			t.Fatalf("github repo missing %q:\n%s", want, repoOut)
		}
	}

	// GitHub issue: omp exact format with comments
	issueOut, err := fetchWithClient(context.Background(), srv.Client(), "https://github.com/owner/repo/issues/42", false, fetchMaxBytes)
	for _, want := range []string{
		"Method: github-issue",
		"# Fix crash\n\n**#42** · open · opened by @alice\nCreated: 2026-07-01T00:00:00Z · Updated: 2026-07-02T00:00:00Z\nLabels: bug",
		"## Comments (1)\n\n### @bob · 2026-07-02T01:00:00Z\n\nOn it",
	} {
		if err != nil || !strings.Contains(issueOut, want) {
			t.Fatalf("github issue missing %q:\n%s", want, issueOut)
		}
	}

	// npm ordered dependencies (B1)
	npmOut, err := fetchWithClient(context.Background(), srv.Client(), "https://www.npmjs.com/package/react", false, fetchMaxBytes)
	if err != nil || !strings.Contains(npmOut, "## Dependencies\n\n- zeta: 1.0\n- alpha: 2.0") {
		t.Fatalf("npm ordered deps mismatch:\n%s", npmOut)
	}

	// PyPI ordered project URLs (B1)
	pypiOut, err := fetchWithClient(context.Background(), srv.Client(), "https://pypi.org/project/requests", false, fetchMaxBytes)
	if err != nil || !strings.Contains(pypiOut, "**Project URLs:**\n- Source: https://src\n- Docs: https://docs") {
		t.Fatalf("pypi ordered URLs mismatch:\n%s", pypiOut)
	}

	// Wikipedia summary + mobile-html sections (skips References)
	wikiOut, err := fetchWithClient(context.Background(), srv.Client(), "https://en.wikipedia.org/wiki/Go_(programming_language)", false, fetchMaxBytes)
	if err != nil || !strings.Contains(wikiOut, "## History\n\nGo was designed at Google in 2007") || strings.Contains(wikiOut, "Should be skipped") {
		t.Fatalf("wikipedia mismatch:\n%s", wikiOut)
	}

	// Hacker News item: relative timestamps, nested depth-2 reply indented, exact note
	hnOut, err := fetchWithClient(context.Background(), srv.Client(), "https://news.ycombinator.com/item?id=99", false, fetchMaxBytes)
	for _, want := range []string{
		"Notes: Fetched HN item 99 with top-level comments (depth 2)",
		"**Posted by:** pg | **Score:** 100 | **Time:** 2h ago | **Comments:** 2",
		"**alice** (1h ago)\nTop comment",
		"  **bob** (30m ago)\n  Nested reply",
	} {
		if err != nil || !strings.Contains(hnOut, want) {
			t.Fatalf("hackernews item missing %q:\n%s", want, hnOut)
		}
	}
}

// Regression tests for pass-2 review items N1–N5.
func TestPass2Fixes(t *testing.T) {
	// N1: orderedStringMap skips non-string values without shifting subsequent keys.
	t.Run("N1 orderedStringMap skips non-string values cleanly", func(t *testing.T) {
		var m orderedStringMap
		if err := json.Unmarshal([]byte(`{"a":1,"b":"x","c":{"nested":true},"d":"y"}`), &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		want := orderedStringMap{{Key: "b", Value: "x"}, {Key: "d", Value: "y"}}
		if !reflect.DeepEqual(m, want) {
			t.Fatalf("orderedStringMap = %+v, want %+v", m, want)
		}
	})

	// N2: fetch only the first markdown alternate and apply exact /feed and /comment(s)/ skip patterns.
	t.Run("N2 first markdown alternate only and exact feed/comment skip patterns", func(t *testing.T) {
		var requested []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requested = append(requested, r.URL.Path)
			switch r.URL.Path {
			case "/doc":
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<!doctype html><html><head>` +
					`<link rel="alternate" type="text/markdown" href="/doc/first.md">` +
					`<link rel="alternate" type="text/markdown" href="/doc/second.md">` +
					`<link rel="alternate" type="application/rss+xml" href="/doc/feed">` +
					`<link rel="alternate" type="application/rss+xml" href="/doc/feed?fmt=rss">` +
					`<link rel="alternate" type="application/atom+xml" href="/doc/comments/all.xml">` +
					`<link rel="alternate" type="application/atom+xml" href="/doc/comment/one.xml">` +
					`</head><body><p>` + strings.Repeat("Native doc body text. ", 10) + `</p></body></html>`))
			case "/doc/first.md":
				w.WriteHeader(http.StatusNotFound)
			case "/doc/second.md":
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte("# SECOND\n\n" + strings.Repeat("Should not be fetched. ", 10)))
			}
		}))
		defer srv.Close()

		links := parseAlternateLinks(
			`<head><link rel="alternate" type="application/rss+xml" href="/doc/feed">`+
				`<link rel="alternate" type="application/rss+xml" href="/doc/feed?a=1">`+
				`<link rel="alternate" type="application/atom+xml" href="/doc/comments/1.xml">`+
				`<link rel="alternate" type="application/atom+xml" href="/doc/comment/1.xml"></head>`,
			srv.URL+"/doc",
		)
		if len(links) != 0 {
			t.Fatalf("expected all /feed and /comment(s)/ links skipped, got %+v", links)
		}

		out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/doc", false, fetchMaxBytes)
		if err != nil || !strings.Contains(out, "Method: native") {
			t.Fatalf("expected native fallback after first markdown alternate fails, err=%v out:\n%s", err, out)
		}
		for _, p := range requested {
			if p == "/doc/second.md" {
				t.Fatalf("must not fetch second markdown alternate; requests=%v", requested)
			}
		}
	})

	// N3: raw mode returns only the content (empty on 404/204, bare notice on binary) with no wrapper header.
	t.Run("N3 raw mode returns bare content on failed, 204, and binary results", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/missing":
				w.WriteHeader(http.StatusNotFound)
			case "/empty":
				w.WriteHeader(http.StatusNoContent)
			case "/bin":
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write([]byte{0x00, 0x01, 0x02, 0x03})
			}
		}))
		defer srv.Close()

		if out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/missing", true, fetchMaxBytes); err != nil || out != "" {
			t.Fatalf("raw 404 = %q, %v; want empty string", out, err)
		}
		if out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/empty", true, fetchMaxBytes); err != nil || out != "" {
			t.Fatalf("raw 204 = %q, %v; want empty string", out, err)
		}
		wantBin := "[Binary content: application/octet-stream, 4 B] " + srv.URL + "/bin"
		if out, err := fetchWithClient(context.Background(), srv.Client(), srv.URL+"/bin", true, fetchMaxBytes); err != nil || out != wantBin {
			t.Fatalf("raw binary = %q, %v; want %q", out, err, wantBin)
		}
	})

	// N4: Wikipedia nested sections preserve every subsection heading and trailing text; CJK uses rune count.
	t.Run("N4 Wikipedia nested sections and rune-count paragraph threshold", func(t *testing.T) {
		mobileHTML := `<section><h2>Design</h2>` +
			`<p>Parent intro paragraph with more than twenty characters.</p>` +
			`<section><h3>Sub One</h3><p>First subsection paragraph with more than twenty characters.</p></section>` +
			`<section><h3>Sub Two</h3><p>日本語の短い段落です。</p><p>Second subsection paragraph with more than twenty characters.</p></section>` +
			`<p>Trailing parent paragraph after subsections with enough length.</p>` +
			`</section>`
		var md strings.Builder
		renderWikiSections(parseHTMLTree(mobileHTML), &md)
		got := md.String()
		for _, want := range []string{
			"## Design\n\n",
			"Parent intro paragraph with more than twenty characters.",
			"### Sub One\n\n",
			"First subsection paragraph with more than twenty characters.",
			"### Sub Two\n\n",
			"Second subsection paragraph with more than twenty characters.",
			"Trailing parent paragraph after subsections with enough length.",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in:\n%s", want, got)
			}
		}
		if strings.Contains(got, "日本語の短い段落です。") {
			t.Fatalf("10-rune CJK paragraph (30 bytes) must be dropped under >20 rune rule, got:\n%s", got)
		}
	})

	// N5: Parallel MCP SSE returns immediately on the first matching message even when the stream stays open.
	t.Run("N5 Parallel MCP SSE returns on first matching message before EOF", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			payload, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"structuredContent": map[string]any{
						"results": []map[string]any{
							{"title": "Early SSE", "url": "https://example.com/sse", "excerpts": []string{"fast"}},
						},
					},
				},
			})
			_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
			// Hold the stream open until test teardown; client must not block waiting for EOF.
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()

		orig := parallelMCPURL
		parallelMCPURL = srv.URL
		defer func() { parallelMCPURL = orig }()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := searchParallelPublicMCP(ctx, srv.Client(), "fast", "fast", 5)
		if err != nil {
			t.Fatalf("SSE early return failed: %v", err)
		}
		if len(resp.Sources) != 1 || resp.Sources[0].Title != "Early SSE" {
			t.Fatalf("unexpected sources: %+v", resp.Sources)
		}
	})
}
