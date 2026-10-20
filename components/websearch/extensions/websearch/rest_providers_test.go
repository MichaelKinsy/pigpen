package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the upstream tests of the keyed REST providers: serpapi, serply, serper, valyu, you,
// xcrawl and anysearch. A case that sends a request and maps an answer is replayed from the
// oracle fixture (provider_fixtures_test.go), so the original's own requests and results are the
// reference; routing, availability and cancellation cases run the Search flow with a fake
// transport, like the upstream tests that replace globalThis.fetch.

const (
	braveSearchPrefix = "https://api.search.brave.com/res/v1/web/search"
	restExaMCPAnswer  = "Title: Exa\nURL: https://example.net\nText: result\n---"
)

// routedNet answers the hosts of one test by prefix and fails every other request.
func routedNet(t *testing.T, routes map[string]func(netCall) netReply) *fakeNet {
	return useNet(t, func(c netCall) netReply {
		for prefix, h := range routes {
			if strings.HasPrefix(c.URL, prefix) {
				return h(c)
			}
		}
		return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
	})
}

func fixed(r netReply) func(netCall) netReply { return func(netCall) netReply { return r } }

func exaFallback() map[string]func(netCall) netReply {
	return map[string]func(netCall) netReply{"https://mcp.exa.ai/mcp": fixed(exaMCPText(restExaMCPAnswer))}
}

func restSearch(ctx context.Context, query string, provider ProviderSelection) (*AttributedSearchResponse, error) {
	return Search(ctx, query, FullSearchOptions{Provider: provider})
}

func named(name string) ProviderSelection { return ProviderSelection{Name: name} }

func callsWithPrefix(n *fakeNet, prefix string) int {
	c := 0
	for _, u := range n.urls() {
		if strings.HasPrefix(u, prefix) {
			c++
		}
	}
	return c
}

// shortDeadline makes every REST provider deadline d for the test (the upstream tests patch
// AbortSignal.timeout the same way).
func shortDeadline(t *testing.T, d time.Duration) {
	prev := restTimeout
	restTimeout = func(time.Duration) time.Duration { return d }
	t.Cleanup(func() { restTimeout = prev })
}

// delayedBody serves headers at once and a body that never completes: it ends when the request's
// context does, with that context's error, like a response whose body is cut by a signal.
type delayedBody struct {
	ctx    context.Context
	onDone func(error)
}

func (b *delayedBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	err := b.ctx.Err()
	if b.onDone != nil {
		b.onDone(err)
	}
	return 0, err
}
func (b *delayedBody) Close() error { return nil }

type delayedNet struct {
	mu       sync.Mutex
	host     string
	status   int
	hosts    []string
	headers  bool
	onHeader func()
	onDone   func(error)
	brave    string
}

func (d *delayedNet) Do(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.hosts = append(d.hosts, r.URL.Hostname())
	d.mu.Unlock()
	switch r.URL.Hostname() {
	case d.host:
		d.mu.Lock()
		d.headers = true
		d.mu.Unlock()
		if d.onHeader != nil {
			d.onHeader()
		}
		h := http.Header{"Content-Type": {"application/json"}}
		return &http.Response{StatusCode: d.status, Header: h, Body: &delayedBody{ctx: r.Context(), onDone: d.onDone}, Request: r}, nil
	case "api.search.brave.com":
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(d.brave)), Request: r}, nil
	}
	return nil, errors.New("Unexpected fetch " + r.URL.Hostname())
}

// delayedBodyRouting is the shared body of "<Provider> delayed ${status} body honors ${mode}
// cancellation routing": the provider answers headers and then stalls; the deadline falls through
// to Brave, a cancellation by the caller (alone, or together with the deadline) does not.
func delayedBodyRouting(t *testing.T, provider, host string, status int, mode string) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, fmt.Sprintf(`{%q:"test-key","braveApiKey":"brave-test-key","searchRouting":{"providers":[%q,"brave"],"fallbackOn":["network"]}}`, providerKeyName(provider), provider))
	d := &delayedNet{host: host, status: status, brave: `{"web":{"results":[{"title":"Brave","url":"https://example.com","description":"fallback"}]}}`}
	t.Cleanup(SetHTTP(d))
	shortDeadline(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mode == "caller" {
		d.onHeader = func() { time.AfterFunc(20*time.Millisecond, cancel) }
	}
	if mode == "both" {
		d.onDone = func(err error) {
			if errors.Is(err, context.DeadlineExceeded) {
				cancel()
			}
		}
	}
	r, err := Search(ctx, "delayed body", FullSearchOptions{Provider: Auto})
	if !d.headers {
		t.Fatal("must reach response-body consumption before cancellation")
	}
	if mode == "deadline" {
		noErr(t, err)
		if r.Provider != "brave" || !eqStrings(d.hosts, []string{host, "api.search.brave.com"}) {
			t.Fatalf("%s %v", r.Provider, d.hosts)
		}
		return
	}
	wantErr(t, err, `(?i)`+provider+` search failed \(aborted\)`)
	if !eqStrings(d.hosts, []string{host}) {
		t.Fatal(d.hosts)
	}
}

func providerKeyName(provider string) string { return provider + "ApiKey" }

func providerTimeoutFallsThrough(t *testing.T, provider, urlPrefix string) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, fmt.Sprintf(`{%q:"test-key","braveApiKey":"brave-test-key","searchRouting":{"providers":[%q,"brave"],"fallbackOn":["network"]}}`, providerKeyName(provider), provider))
	n := routedNet(t, map[string]func(netCall) netReply{
		urlPrefix:         fixed(netReply{Err: context.DeadlineExceeded}),
		braveSearchPrefix: fixed(braveOK("Brave", "https://example.com/brave", "fallback")),
	})
	r, err := restSearch(bg(), "timeout route", Auto)
	noErr(t, err)
	urls := n.urls()
	if r.Provider != "brave" || len(urls) != 2 || !strings.HasPrefix(urls[0], urlPrefix) || !strings.HasPrefix(urls[1], braveSearchPrefix) {
		t.Fatal(r.Provider, urls)
	}
}

// explicitOnly is "supports explicit selection but is excluded from auto and provider all": the
// credential is configured, auto and all still choose Exa.
func explicitOnly(t *testing.T, provider, urlPrefix, body string) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, fmt.Sprintf(`{%q:"test-key"}`, providerKeyName(provider)))
	routes := exaFallback()
	routes[urlPrefix] = fixed(reply(200, body))
	n := routedNet(t, routes)
	explicit, err := restSearch(bg(), "explicit", named(provider))
	noErr(t, err)
	auto, err := restSearch(bg(), "auto", Auto)
	noErr(t, err)
	all, err := restSearch(bg(), "all", named("all"))
	noErr(t, err)
	if explicit.Provider != provider || auto.Provider != "exa" || !eqStrings(providerNames(all.ProviderResponses), []string{"exa"}) || callsWithPrefix(n, urlPrefix) != 1 {
		t.Fatal(explicit.Provider, auto.Provider, providerNames(all.ProviderResponses), n.urls())
	}
}

// TestRestProviderAvailabilityNames pins the credential names of each provider's availability
// check (the config key and the environment variable of the upstream module).
func TestRestProviderAvailabilityNames(t *testing.T) {
	for _, c := range []struct {
		provider, config, env string
		available             func() bool
	}{
		{"serper", "serperApiKey", "SERPER_API_KEY", IsSerperAvailable},
		{"serpapi", "serpapiApiKey", "SERPAPI_KEY", IsSerpAPIAvailable},
		{"serpbase", "serpbaseApiKey", "SERPBASE_API_KEY", IsSerpBaseAvailable},
		{"serply", "serplyApiKey", "SERPLY_API_KEY", IsSerplyAvailable},
		{"valyu", "valyuApiKey", "VALYU_API_KEY", IsValyuAvailable},
		{"bocha", "bochaApiKey", "BOCHA_API_KEY", IsBochaAvailable},
		{"baizhi", "baizhiApiKey", "BAIZHI_API_KEY", IsBaizhiAvailable},
		{"you", "youApiKey", "YDC_API_KEY", IsYouAvailable},
		{"xcrawl", "xcrawlApiKey", "XCRAWL_API_KEY", IsXcrawlAvailable},
		{"jina", "jinaApiKey", "JINA_API_KEY", IsJinaSearchAvailable},
	} {
		t.Run(c.provider, func(t *testing.T) {
			_, agentDir := isolate(t)
			unsetenv(t, "SERPAPI_KEY", "YDC_API_KEY")
			if c.available() || isProviderAvailable(c.provider) {
				t.Fatal("available without a credential")
			}
			t.Setenv(c.env, "k")
			if !c.available() || !isProviderAvailable(c.provider) {
				t.Fatalf("%s does not enable %s", c.env, c.provider)
			}
			unsetenv(t, c.env)
			writeConfig(t, agentDir, fmt.Sprintf(`{%q:"k"}`, c.config))
			if !c.available() || !isProviderAvailable(c.provider) {
				t.Fatalf("%s does not enable %s", c.config, c.provider)
			}
		})
	}
	t.Run("anysearch is available without a key", func(t *testing.T) {
		isolate(t)
		if !IsAnySearchAvailable() || !isProviderAvailable("anysearch") {
			t.Fatal("anysearch needs no key")
		}
	})
	t.Run("searxng needs a valid base URL", func(t *testing.T) {
		_, agentDir := isolate(t)
		if ok, err := IsSearXNGAvailable(); ok || err != nil {
			t.Fatal(ok, err)
		}
		for _, bad := range []string{"", "ftp://x.example", "https://user:pw@x.example", "not a url"} {
			writeConfig(t, agentDir, fmt.Sprintf(`{"searxngBaseUrl":%q}`, bad))
			if ok, _ := IsSearXNGAvailable(); ok {
				t.Fatalf("%q must not be a base URL", bad)
			}
		}
		writeConfig(t, agentDir, `{"searxngBaseUrl":"https://search.example.com/"}`)
		if ok, err := IsSearXNGAvailable(); !ok || err != nil {
			t.Fatal(ok, err)
		}
		t.Setenv("SEARXNG_BASE_URL", "")
		if ok, _ := IsSearXNGAvailable(); ok {
			t.Fatal("an empty environment variable wins over the config and is invalid")
		}
	})
}

func TestUpstream_serpapi_provider(t *testing.T) {
	const F = "serpapi-provider"
	tw(t, F, "SerpApi maps Google organic results, filters, recency, and environment credentials", func(t *testing.T) {
		fixtureCases(t, "serpapi-env-filters")
	})
	tw(t, F, "SerpApi supports explicit routing but is excluded from provider all", func(t *testing.T) {
		explicitOnly(t, "serpapi", "https://serpapi.com/search.json?", `{"organic_results":[{"title":"SerpApi","link":"https://example.com","snippet":"result"}]}`)
	})
	tw(t, F, "SerpApi provider timeouts can fall through configured routing", func(t *testing.T) {
		providerTimeoutFallsThrough(t, "serpapi", "https://serpapi.com/search.json?")
		fixtureCases(t, "serpapi-timeout")
	})
	tw(t, F, "SerpApi delayed ${status} body honors ${mode} cancellation routing", func(t *testing.T) {
		for _, status := range []int{200, 503} {
			for _, mode := range []string{"deadline", "caller", "both"} {
				t.Run(fmt.Sprintf("%d/%s", status, mode), func(t *testing.T) { delayedBodyRouting(t, "serpapi", "serpapi.com", status, mode) })
			}
		}
	})
	tw(t, F, "SerpApi redacts the resolved credential in JSON error envelopes", func(t *testing.T) {
		fixtureCases(t, "serpapi-error-envelope-redacted")
	})
	tw(t, F, "SerpApi redacts API errors and appears in the Curator", func(t *testing.T) {
		adapted(t, "the Curator page half has no Go counterpart (the curator is a later slice); the redaction half runs")
		fixtureCases(t, "serpapi-401-redacted")
	})
}

func TestUpstream_serply_provider(t *testing.T) {
	const F = "serply-provider"
	tw(t, F, "Serply maps Google results, filters, recency, and environment credentials", func(t *testing.T) {
		fixtureCases(t, "serply-env-filters")
		// the key travels in the header, never the query string
		c := loadFixtures(t)["serply-env-filters"]
		if strings.Contains(c.Requests[0].URL, "api_key") {
			t.Fatal(c.Requests[0].URL)
		}
	})
	tw(t, F, "Serply retains credentials for same-origin redirects and strips them cross-origin", func(t *testing.T) {
		fixtureCases(t, "serply-redirects")
	})
	tw(t, F, "Serply publishes only absolute HTTP(S) result URLs", func(t *testing.T) {
		fixtureCases(t, "serply-links")
	})
	tw(t, F, "Serply supports explicit selection but is excluded from auto and provider all", func(t *testing.T) {
		explicitOnly(t, "serply", "https://api.serply.io/v1/search?", `{"results":[{"title":"Serply","link":"https://example.com","description":"result"}]}`)
	})
	tw(t, F, "Serply provider timeouts can fall through configured routing", func(t *testing.T) {
		providerTimeoutFallsThrough(t, "serply", "https://api.serply.io/v1/search?")
		fixtureCases(t, "serply-timeout")
	})
	tw(t, F, "Serply delayed ${status} body honors ${mode} cancellation routing", func(t *testing.T) {
		for _, status := range []int{200, 503} {
			for _, mode := range []string{"deadline", "caller"} {
				t.Run(fmt.Sprintf("%d/%s", status, mode), func(t *testing.T) { delayedBodyRouting(t, "serply", "api.serply.io", status, mode) })
			}
		}
	})
	tw(t, F, "Serply redacts the resolved credential in JSON error envelopes", func(t *testing.T) {
		fixtureCases(t, "serply-detail-redacted")
	})
	tw(t, F, "Serply redacts API errors and appears in the Curator", func(t *testing.T) {
		adapted(t, "the Curator page half has no Go counterpart (the curator is a later slice); the redaction half runs")
		fixtureCases(t, "serply-401-redacted")
	})
}

func TestUpstream_valyu_serper_provider(t *testing.T) {
	const F = "valyu-serper-provider"
	tw(t, F, "Valyu sends documented credentials, bounds returned content, and supports explicit routing", func(t *testing.T) {
		fixtureCases(t, "valyu-content-bounds")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"valyuApiKey":"valyu-test-key"}`)
		routedNet(t, map[string]func(netCall) netReply{"https://api.valyu.ai/v1/search": fixed(reply(200, `{"success":true,"results":[{"title":"Valyu result","url":"https://example.com/valyu","description":"description"}]}`))})
		r, err := restSearch(bg(), "route", named("valyu"))
		noErr(t, err)
		if r.Provider != "valyu" {
			t.Fatal(r.Provider)
		}
	})
	tw(t, F, "Serper maps organic results and reads its environment credential", func(t *testing.T) {
		fixtureCases(t, "serper-env-credential")
	})
	tw(t, F, "Valyu maps domain and recency filters to documented request fields", func(t *testing.T) {
		fixtureCases(t, "valyu-filters-recency")
	})
	tw(t, F, "Serper maps filters to Google parameters and reapplies domain filtering", func(t *testing.T) {
		fixtureCases(t, "serper-filters", "serper-multi-include")
	})
	tw(t, F, "Valyu and Serper can be configured for routing but provider all never calls them", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"searchRouting":{"providers":["valyu","serper"],"fallbackOn":["quota"]}}`)
		t.Setenv("VALYU_API_KEY", "valyu-key")
		t.Setenv("SERPER_API_KEY", "serper-key")
		n := routedNet(t, map[string]func(netCall) netReply{
			"https://api.valyu.ai/v1/search":   fixed(reply(429, "quota")),
			"https://google.serper.dev/search": fixed(reply(200, `{"organic":[{"title":"Serper","link":"https://example.com","snippet":"fallback"}]}`)),
		})
		routed, err := restSearch(bg(), "route", Auto)
		noErr(t, err)
		_, allErr := restSearch(bg(), "all", named("all"))
		calls := n.urls()
		if !eqStrings(calls[:2], []string{"https://api.valyu.ai/v1/search", "https://google.serper.dev/search"}) {
			t.Fatal(calls, routed.Provider)
		}
		for _, u := range calls[2:] {
			if u == "https://api.valyu.ai/v1/search" || u == "https://google.serper.dev/search" {
				t.Fatal("all must not call them", calls)
			}
		}
		if routed.Provider != "serper" || allErr == nil {
			t.Fatal(routed.Provider, allErr)
		}
		noMatchRE(t, `Valyu|Serper`, allErr.Error())
	})
	tw(t, F, "${name} HTTP errors redact its credential", func(t *testing.T) {
		fixtureCases(t, "valyu-401-redacted", "serper-401-redacted")
	})
}

func TestUpstream_you_provider(t *testing.T) {
	const F = "you-provider"
	tw(t, F, "You.com sends the documented search request and maps web results", func(t *testing.T) {
		fixtureCases(t, "you-map")
	})
	tw(t, F, "You.com is explicit-only and routing skips it when no key is configured", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"youApiKey":"you-test-key"}`)
		routes := exaFallback()
		routes["https://ydc-index.io/v1/search"] = fixed(reply(200, `{"results":{"web":[{"title":"You","url":"https://example.com"}]}}`))
		n := routedNet(t, routes)
		explicit, err := restSearch(bg(), "explicit", named("you"))
		noErr(t, err)
		auto, err := restSearch(bg(), "auto", Auto)
		noErr(t, err)
		all, err := restSearch(bg(), "all", named("all"))
		noErr(t, err)
		if explicit.Provider != "you" || auto.Provider != "exa" || !eqStrings(providerNames(all.ProviderResponses), []string{"exa"}) || callsWithPrefix(n, "https://ydc-index.io/v1/search") != 1 {
			t.Fatal(explicit.Provider, auto.Provider, providerNames(all.ProviderResponses), n.urls())
		}
		_, agentDir = isolate(t)
		writeConfig(t, agentDir, `{"braveApiKey":"brave-test-key","searchRouting":{"providers":["you","brave"],"fallbackOn":["network"]}}`)
		routedNet(t, map[string]func(netCall) netReply{braveSearchPrefix: fixed(braveOK("Brave", "https://example.com/brave", "routed"))})
		routed, err := restSearch(bg(), "routed", Auto)
		noErr(t, err)
		if routed.Provider != "brave" {
			t.Fatal(routed.Provider)
		}
	})
	tw(t, F, "You.com redacts API errors and appears in the Curator", func(t *testing.T) {
		adapted(t, "the Curator page half has no Go counterpart (the curator is a later slice); the redaction half runs")
		fixtureCases(t, "you-401-redacted")
	})
}

func TestUpstream_xcrawl_provider(t *testing.T) {
	const F = "xcrawl-provider"
	tw(t, F, "XCrawl sends Bearer credentials to the SERP endpoint, normalizes null titles, and supports explicit routing", func(t *testing.T) {
		fixtureCases(t, "xcrawl-map")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"xcrawlApiKey":"xc-test-key"}`)
		routedNet(t, map[string]func(netCall) netReply{"https://run.xcrawl.com/v1/serp": fixed(jsonReply(map[string]any{
			"search_metadata": map[string]any{"status": "completed"}, "organic_results": []any{map[string]any{"title": "T", "link": "https://www.example.com/first", "snippet": "s"}}}))})
		r, err := restSearch(bg(), "route", named("xcrawl"))
		noErr(t, err)
		if r.Provider != "xcrawl" {
			t.Fatal(r.Provider)
		}
	})
	tw(t, F, "XCrawl resolves API-origin-relative result links to absolute URLs", func(t *testing.T) {
		fixtureCases(t, "xcrawl-relative-links")
	})
	tw(t, F, "XCrawl rejects whitespace-only result links", func(t *testing.T) {
		fixtureCases(t, "xcrawl-blank-link")
	})
	tw(t, F, "XCrawl applies the shared domain filter client-side", func(t *testing.T) {
		fixtureCases(t, "xcrawl-include-filter", "xcrawl-exclude-filter", "xcrawl-fuzzy-filter")
	})
	tw(t, F, "XCrawl preserves explicit www. labels in domain filters", func(t *testing.T) {
		fixtureCases(t, "xcrawl-www-include", "xcrawl-www-exclude")
	})
	tw(t, F, "XCrawl caps results to the requested numResults", func(t *testing.T) {
		fixtureCases(t, "xcrawl-cap")
	})
	tw(t, F, "XCrawl surfaces documented API errors without leaking the key", func(t *testing.T) {
		fixtureCases(t, "xcrawl-401-message", "xcrawl-401-error-field", "xcrawl-500-text")
		d, _, err := runFixture(t, loadFixtures(t)["xcrawl-401-message"])
		if err == nil || strings.Contains(err.Error(), "xc-secret-key") || d.requests[0].Headers["authorization"] != "Bearer xc-secret-key" {
			t.Fatal(err, d.requests)
		}
	})
	tw(t, F, "XCrawl rejects unexpected envelope shapes instead of returning empty answers silently", func(t *testing.T) {
		fixtureCases(t, "xcrawl-bad-status", "xcrawl-missing-results", "xcrawl-bad-entry", "xcrawl-bad-title", "xcrawl-bad-snippet", "xcrawl-bad-metadata", "xcrawl-invalid-json")
	})
	tw(t, F, "configured routing falls back when XCrawl hits its provider timeout", func(t *testing.T) {
		providerTimeoutFallsThrough(t, "xcrawl", "https://run.xcrawl.com/v1/serp")
	})
	tw(t, F, "XCrawl is never part of auto fallback, even with credentials", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"xcrawlApiKey":"xc-test-key"}`)
		n := useNet(t, func(c netCall) netReply { return netReply{Err: errors.New("unexpected auto provider")} })
		_, err := restSearch(bg(), "auto", Auto)
		if err == nil || callsWithPrefix(n, "https://run.xcrawl.com/") != 0 {
			t.Fatal(err, n.urls())
		}
	})
	tw(t, F, `provider "all" does not fan out to XCrawl`, func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"xcrawlApiKey":"xc-test-key","braveApiKey":"brave-test-key"}`)
		n := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://run.xcrawl.com/") {
				return netReply{Err: errors.New("XCrawl must not be part of all")}
			}
			return braveOK("Brave", "https://example.com/brave", "ok")
		})
		r, err := restSearch(bg(), "fan out", named("all"))
		noErr(t, err)
		if r.Provider != "all" || callsWithPrefix(n, "https://run.xcrawl.com/") != 0 {
			t.Fatal(r.Provider, n.urls())
		}
	})
	tw(t, F, "XCrawl provider timeout is a retriable failure, not caller cancellation", func(t *testing.T) {
		fixtureCases(t, "xcrawl-timeout")
		_, _, err := runFixture(t, loadFixtures(t)["xcrawl-timeout"])
		wantErr(t, err, `timed out after 60s`)
		noMatchRE(t, `(?i)abort`, err.Error())
	})
	tw(t, F, "XCrawl preserves caller cancellation when a timeout rejection races with abort", func(t *testing.T) {
		fixtureCases(t, "xcrawl-caller-abort")
		_, _, err := runFixture(t, loadFixtures(t)["xcrawl-caller-abort"])
		wantErr(t, err, `(?i)abort`)
		noMatchRE(t, `(?i)timed out after 60s`, err.Error())
	})
	tw(t, F, "XCrawl is unavailable without credentials and never part of auto fallback", func(t *testing.T) {
		isolate(t)
		n := useNet(t, func(c netCall) netReply { return netReply{Err: errors.New("unexpected auto provider")} })
		_, err := restSearch(bg(), "auto", Auto)
		if IsXcrawlAvailable() || callsWithPrefix(n, "https://run.xcrawl.com/") != 0 {
			t.Fatal(IsXcrawlAvailable(), n.urls())
		}
		if err != nil {
			noMatchRE(t, `XCrawl`, err.Error())
		}
	})
}

func anysearchReplyBody(content string) string {
	return fmt.Sprintf(`{"code":0,"data":{"results":[{"title":"AnySearch result","url":"https://example.com/result","snippet":"A useful snippet","content":%q}],"metadata":{}}}`, content)
}

// commandMarker returns a credential command that records that it ran and prints key, and the
// path of the record.
func commandMarker(t *testing.T, key string) (command, marker string) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command credential")
	}
	marker = filepath.Join(t.TempDir(), "ran")
	return fmt.Sprintf("!touch %s && printf %s", marker, key), marker
}

func TestUpstream_anysearch_provider(t *testing.T) {
	const F = "anysearch-provider"
	tw(t, F, "AnySearch supports anonymous success with a minimal request and gated content", func(t *testing.T) {
		fixtureCases(t, "anysearch-anonymous", "anysearch-include-content")
	})
	tw(t, F, "AnySearch accepts missing or null result content", func(t *testing.T) {
		fixtureCases(t, "anysearch-null-content", "anysearch-invalid-content")
	})
	tw(t, F, "AnySearch sends keyed auth and resolves command credentials lazily", func(t *testing.T) {
		command, marker := commandMarker(t, "anysearch-command-key")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, fmt.Sprintf(`{"anysearchApiKey":%q}`, command))
		n := useNet(t, func(netCall) netReply { return reply(200, anysearchReplyBody("full page")) })
		before := fileExists(marker)
		_, err := SearchWithAnySearch(bg(), "keyed", SearchOptions{})
		noErr(t, err)
		h := n.calls[0].Header
		if before || !fileExists(marker) || h.Get("Authorization") != "Bearer anysearch-command-key" || h.Get("Content-Type") != "application/json" || len(h) != 2 {
			t.Fatal(before, fileExists(marker), h)
		}
		fixtureCases(t, "anysearch-keyed")
	})
	tw(t, F, "explicit AnySearch provider routing works and is available anonymously", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{}`)
		n := useNet(t, func(netCall) netReply { return reply(200, anysearchReplyBody("route content")) })
		r, err := restSearch(bg(), "route", named("anysearch"))
		noErr(t, err)
		if r.Provider != "anysearch" || !eqStrings(n.urls(), []string{"https://api.anysearch.com/v1/search"}) {
			t.Fatal(r.Provider, n.urls())
		}
	})
	tw(t, F, "searchRouting accepts anonymous AnySearch availability and follows quota/transient fallback", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"searchRouting":{"providers":["anysearch","brave"],"fallbackOn":["quota","transient"]}}`)
		t.Setenv("BRAVE_API_KEY", "brave-key")
		n := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.anysearch.com/v1/search" {
				return reply(402, "quota")
			}
			return braveOK("Brave", "https://example.com/brave", "fallback")
		})
		r, err := restSearch(bg(), "route", Auto)
		noErr(t, err)
		if r.Provider != "brave" || !eqStrings(n.urls(), []string{"https://api.anysearch.com/v1/search", "https://api.search.brave.com/res/v1/web/search?q=route&count=5"}) {
			t.Fatal(r.Provider, n.urls())
		}
	})
	tw(t, F, "AnySearch HTTP ${status} is classified as ${kind}", func(t *testing.T) {
		for _, c := range []struct {
			status int
			kind   string
		}{{400, "invalid-request"}, {401, "auth"}, {403, "auth"}, {402, "quota"}, {429, "quota"}, {500, "transient"}, {503, "transient"}, {504, "transient"}} {
			t.Run(fmt.Sprintf("%d_%s", c.status, c.kind), func(t *testing.T) {
				_, agentDir := isolate(t)
				fallback := c.kind
				if c.kind == "auth" || c.kind == "invalid-request" {
					fallback = "quota"
				}
				writeConfig(t, agentDir, fmt.Sprintf(`{"searchRouting":{"providers":["anysearch","brave"],"fallbackOn":[%q]}}`, fallback))
				t.Setenv("BRAVE_API_KEY", "brave-key")
				n := useNet(t, func(c2 netCall) netReply {
					if c2.URL == "https://api.anysearch.com/v1/search" {
						return reply(c.status, "provider failure")
					}
					return braveOK("fallback", "https://example.com/fallback", "ok")
				})
				r, err := restSearch(bg(), "classified", Auto)
				if c.kind == "auth" || c.kind == "invalid-request" {
					wantErr(t, err, fmt.Sprintf(`anysearch search failed \(%s\)`, c.kind))
					if !eqStrings(n.urls(), []string{"https://api.anysearch.com/v1/search"}) {
						t.Fatal(n.urls())
					}
					return
				}
				noErr(t, err)
				if r.Provider != "brave" || len(n.urls()) != 2 {
					t.Fatal(r.Provider, n.urls())
				}
			})
		}
	})
	tw(t, F, "AnySearch malformed envelopes fail loudly", func(t *testing.T) {
		fixtureCases(t, "anysearch-no-metadata", "anysearch-bad-code", "anysearch-empty-url", "anysearch-bad-fields", "anysearch-402")
	})
	tw(t, F, "AnySearch malformed config root fails explicitly", func(t *testing.T) {
		fixtureCases(t, "anysearch-config-root")
	})
	tw(t, F, "AnySearch aborts without falling through", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{}`)
		useNet(t, func(netCall) netReply { return netReply{Err: errors.New("The operation was aborted")} })
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := SearchWithAnySearch(ctx, "cancel", SearchOptions{})
		if err == nil || !isAbortError(err) {
			t.Fatal(err)
		}
		_, err = restSearch(ctx, "cancel", named("anysearch"))
		wantErr(t, err, `(?i)abort`)
	})
	tw(t, F, "AnySearch is never part of auto fallback without explicit routing", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{}`)
		n := useNet(t, func(c netCall) netReply { return netReply{Err: errors.New("unexpected auto provider")} })
		_, err := restSearch(bg(), "auto", Auto)
		if err == nil || callsWithPrefix(n, "https://api.anysearch.com/") != 0 {
			t.Fatal(err, n.urls())
		}
		noMatchRE(t, `AnySearch`, err.Error())
	})
	tw(t, F, "AnySearch HTTP errors redact keyed credentials", func(t *testing.T) {
		fixtureCases(t, "anysearch-401-redacted")
	})
}

// TestRestProviderTransportFailure: a connection that fails, drops or is refused reads as Node's
// "fetch failed" (a network failure the routing falls through on), as upstream; a body cut short is
// "terminated". Go's own texts (EOF, connection refused) never reach the model or the classifier.
func TestRestProviderTransportFailure(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for name, call := range map[string]func() error{
		"serper":  func() error { _, err := SearchWithSerper(bg(), "q", SearchOptions{}); return err },
		"serpapi": func() error { _, err := SearchWithSerpAPI(bg(), "q", SearchOptions{}); return err },
		"jina":    func() error { _, err := SearchWithJina(bg(), "q", SearchOptions{}); return err },
		"xcrawl":  func() error { _, err := SearchWithXCrawl(bg(), "q", SearchOptions{}); return err },
		"valyu":   func() error { _, err := SearchWithValyu(bg(), "q", SearchOptions{}); return err },
	} {
		for failure, transport := range map[string]error{"EOF": io.EOF, "refused": refused} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				_, agentDir := isolate(t)
				writeConfig(t, agentDir, `{"serperApiKey":"k","serpapiApiKey":"k","jinaApiKey":"k","xcrawlApiKey":"k","valyuApiKey":"k"}`)
				useNet(t, func(netCall) netReply { return netReply{Err: transport} })
				err := call()
				if err == nil || err.Error() != "fetch failed" || ClassifyProviderError(name, err).Kind != "network" {
					t.Fatalf("%v", err)
				}
			})
		}
	}
	t.Run("a body cut short", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"serperApiKey":"k"}`)
		t.Cleanup(SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: brokenBody{io.ErrUnexpectedEOF}, Request: r}, nil
		})))
		_, err := SearchWithSerper(bg(), "q", SearchOptions{})
		if err == nil || err.Error() != "terminated" {
			t.Fatal(err)
		}
	})
}

// BenchmarkRestProviders measures one search of the keyed REST providers over a canned
// transport (no network): the cost of the request build, the response parse and the mapping.
func BenchmarkRestProviders(b *testing.B) {
	home := b.TempDir()
	b.Setenv("PIG_CODING_AGENT_DIR", home)
	if err := os.WriteFile(home+"/web-search.json", []byte(`{"serperApiKey":"k","serpapiApiKey":"k","valyuApiKey":"k","youApiKey":"k","xcrawlApiKey":"k","jinaApiKey":"k"}`), 0o600); err != nil {
		b.Fatal(err)
	}
	organic := `{"organic":[{"title":"A","link":"https://example.com/a","snippet":"s"},{"title":"B","link":"https://example.com/b","snippet":"s"}]}`
	cases := map[string]struct {
		body string
		call func() error
	}{
		"serper":  {organic, func() error { _, err := SearchWithSerper(bg(), "q", SearchOptions{}); return err }},
		"serpapi": {`{"organic_results":[{"title":"A","link":"https://example.com/a","snippet":"s"}]}`, func() error { _, err := SearchWithSerpAPI(bg(), "q", SearchOptions{}); return err }},
		"jina":    {`{"code":200,"data":[{"title":"A","url":"https://example.com/a","description":"d"}]}`, func() error { _, err := SearchWithJina(bg(), "q", SearchOptions{}); return err }},
	}
	for name, c := range cases {
		b.Run(name, func(b *testing.B) {
			body := c.body
			defer SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) { return textResponse(r, 200, body), nil }))()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := c.call(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
