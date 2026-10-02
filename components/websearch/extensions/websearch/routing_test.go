package websearch

import (
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of test/search-routing and test/all-provider (the cases whose providers are in the
// first slice) and the routing behaviour of gemini-search.ts.

func braveOK(title, u, desc string) netReply {
	return jsonReply(map[string]any{"web": map[string]any{"results": []map[string]any{{"title": title, "url": u, "description": desc}}}})
}

func TestUpstream_search_routing(t *testing.T) {
	const f = "search-routing"
	const braveURL = "https://api.search.brave.com/res/v1/web/search?"

	tw(t, f, "configured routing follows order after a selected network failure and returns the successful provider", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["brave","tavily"],"fallbackOn":["network"]}}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.search.brave.com/") {
				return netReply{Err: errors.New("fetch failed")}
			}
			if c.URL == "https://api.tavily.com/search" {
				return jsonReply(map[string]any{"answer": "Tavily route answer", "results": []any{}})
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "ordered route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "tavily" || r.Answer != "Tavily route answer" ||
			!eqStrings(fn.urls(), []string{"https://api.search.brave.com/res/v1/web/search?q=ordered+route&count=5", "https://api.tavily.com/search"}) {
			t.Fatalf("%s %s %v", r.Provider, r.Answer, fn.urls())
		}
	})

	tw(t, f, "configured routing fails closed on quota errors not selected by fallbackOn", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["brave","tavily"],"fallbackOn":["network"]}}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.search.brave.com/") {
				return reply(429, "quota")
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		_, err := Search(bg(), "quota route", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `brave search failed \(quota\)`)
		if !eqStrings(fn.urls(), []string{"https://api.search.brave.com/res/v1/web/search?q=quota+route&count=5"}) {
			t.Fatal(fn.urls())
		}
	})

	tw(t, f, "configured routing falls back from Tavily monthly plan exhaustion", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["tavily","brave"],"fallbackOn":["quota"]}}`)
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.tavily.com/search" {
				return reply(432, "Monthly plan usage limit exceeded")
			}
			if strings.HasPrefix(c.URL, braveURL) {
				return braveOK("Brave fallback", "https://example.com/brave", "Fallback answer")
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "tavily quota route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "brave" || r.Answer != "Fallback answer\nSource: Brave fallback (https://example.com/brave)" ||
			!eqStrings(fn.urls(), []string{"https://api.tavily.com/search", "https://api.search.brave.com/res/v1/web/search?q=tavily+quota+route&count=5"}) {
			t.Fatalf("%s %q %v", r.Provider, r.Answer, fn.urls())
		}
	})

	tw(t, f, "auth status fails closed even when the response text looks like quota", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["brave","tavily"],"fallbackOn":["quota"]}}`)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.search.brave.com/") {
				return reply(403, "quota exceeded")
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		_, err := Search(bg(), "auth route", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `brave search failed \(auth\)`)
		if !eqStrings(fn.urls(), []string{"https://api.search.brave.com/res/v1/web/search?q=auth+route&count=5"}) {
			t.Fatal(fn.urls())
		}
	})

	tw(t, f, "legacy single-provider config takes precedence over searchRouting", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"perplexity","searchRouting":{"providers":["tavily","brave"],"fallbackOn":["network"]}}`)
		t.Setenv("PERPLEXITY_API_KEY", "perplexity-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.perplexity.ai/chat/completions" {
				return reply(200, `{"choices":[{"message":{"content":"Perplexity answer"}}],"citations":[]}`)
			}
			return netReply{Err: errors.New("Routing provider must not run")}
		})
		r, err := Search(bg(), "precedence", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "perplexity" || !eqStrings(fn.urls(), []string{"https://api.perplexity.ai/chat/completions"}) {
			t.Fatalf("%s %v", r.Provider, fn.urls())
		}
	})

	tw(t, f, "configured routing accepts SERPdive and detects its availability", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["serpdive"],"fallbackOn":["network"]}}`)
		t.Setenv("SERPDIVE_API_KEY", "serpdive-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.serpdive.com/v1/search" {
				return reply(200, `{"results":[{"url":"https://serpdive.example/source","title":"SERPdive source","content":"SERPdive content"}]}`)
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "serpdive route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "serpdive" || !strings.Contains(r.Answer, "SERPdive content") || !eqStrings(fn.urls(), []string{"https://api.serpdive.com/v1/search"}) {
			t.Fatalf("%s %q %v", r.Provider, r.Answer, fn.urls())
		}
	})

	tw(t, f, "configured SERPdive provider remains strict instead of falling back to auto", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"serpdive"}`)
		t.Setenv("SERPDIVE_API_KEY", "serpdive-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.serpdive.com/v1/search" {
				return reply(200, `{"results":[{"url":"https://serpdive.example/source","title":"SERPdive source","content":"configured content"}]}`)
			}
			return netReply{Err: errors.New("Auto fallback must not run: " + c.URL)}
		})
		r, err := Search(bg(), "configured serpdive", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "serpdive" || !strings.Contains(r.Answer, "configured content") || !eqStrings(fn.urls(), []string{"https://api.serpdive.com/v1/search"}) {
			t.Fatalf("%s %q %v", r.Provider, r.Answer, fn.urls())
		}
	})

	tw(t, f, "invalid searchRouting configuration fails loudly", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["auto"],"fallbackOn":["network"]}}`)
		_, err := Search(bg(), "invalid route", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `searchRouting\.providers .*invalid provider: auto`)
	})

}

// gate lets N concurrent requests proceed only once all of them started (the upstream tests
// use the same trick to prove the providers run together).
type gate struct {
	mu      sync.Mutex
	started []string
	want    map[string]bool
	open    chan struct{}
	once    sync.Once
}

func newGate(names ...string) *gate {
	g := &gate{want: map[string]bool{}, open: make(chan struct{})}
	for _, n := range names {
		g.want[n] = true
	}
	return g
}

func (g *gate) wait(t *testing.T, name string) {
	g.mu.Lock()
	g.started = append(g.started, name)
	all := true
	for n := range g.want {
		found := false
		for _, s := range g.started {
			found = found || s == n
		}
		all = all && found
	}
	g.mu.Unlock()
	if all {
		g.once.Do(func() { close(g.open) })
	}
	select {
	case <-g.open:
	case <-time.After(10 * time.Second):
		t.Errorf("providers did not start together: %v", g.started)
	}
}

func (g *gate) sorted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string{}, g.started...)
	sort.Strings(out)
	return out
}

func exaMCPText(text string) netReply {
	return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
}

func providerNames(rs []ProviderSearchResponse) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Provider)
	}
	return out
}

func TestUpstream_all_provider(t *testing.T) {
	const f = "all-provider"

	tskip(t, f, `provider "all" starts every eligible provider together, excludes AnySearch, and deduplicates sources`,
		"needs the TinyFish and Search1API providers (slice 2); the same behaviour is covered by TestAllProviderStartsEligibleProvidersTogether with Exa, Brave and Tavily")

	tw(t, f, `provider array searches only the selected providers concurrently and preserves their order`, func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TINYFISH_API_KEY", "tinyfish-test-key")
		g := newGate("exa", "brave")
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
				g.wait(t, "exa")
				return exaMCPText("Title: Exa result\nURL: https://example.com/exa\nText: Exa answer\n---")
			case strings.HasPrefix(c.URL, "https://api.search.brave.com/"):
				g.wait(t, "brave")
				return braveOK("Brave result", "https://example.com/brave", "Brave answer")
			case strings.HasPrefix(c.URL, "https://api.search.tinyfish.ai"):
				return netReply{Err: errors.New("Unselected TinyFish provider must not run")}
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "selected", FullSearchOptions{Provider: ProviderSelection{List: []string{"brave", "exa"}}})
		noErr(t, err)
		if !eqStrings(g.sorted(), []string{"brave", "exa"}) || !eqStrings(providerNames(r.ProviderResponses), []string{"brave", "exa"}) ||
			!eqStrings(urlsOf(r.Results), []string{"https://example.com/brave", "https://example.com/exa"}) {
			t.Fatalf("%v %v %v", g.sorted(), providerNames(r.ProviderResponses), r.Results)
		}
	})

	tw(t, f, `provider array reports an unavailable selection without discarding successful providers`, func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.search.brave.com/") {
				return braveOK("Brave result", "https://example.com/brave", "Brave answer")
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "selected unavailable", FullSearchOptions{Provider: ProviderSelection{List: []string{"brave", "tavily"}}})
		noErr(t, err)
		if !eqStrings(providerNames(r.ProviderResponses), []string{"brave"}) || len(r.ProviderErrors) != 1 || r.ProviderErrors[0].Provider != "tavily" ||
			!regexp.MustCompile(`Tavily API key not found`).MatchString(r.ProviderErrors[0].Error) || !strings.Contains(r.Answer, "## Provider errors") {
			t.Fatalf("%+v", r)
		}
	})

	tskip(t, f, `provider "all" uses Gemini API without falling back to Gemini Web`, "Gemini is slice 2 (and Gemini Web needs browser cookies, off by default); named gap")

	tw(t, f, `provider "all" keeps successful providers when another available provider fails`, func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
				return exaMCPText("Title: Exa result\nURL: https://example.com/exa\nText: Exa answer\n---")
			case strings.HasPrefix(c.URL, "https://api.search.brave.com/"):
				return reply(500, "brave down")
			case c.URL == "https://api.tavily.com/search":
				return reply(200, `{"answer":"Tavily answer","results":[{"title":"T","url":"https://example.com/t","content":"c"}]}`)
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "partial", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		noErr(t, err)
		if r.Provider != "all" || !eqStrings(providerNames(r.ProviderResponses), []string{"exa", "tavily"}) || len(r.ProviderErrors) != 1 || r.ProviderErrors[0].Provider != "brave" ||
			!strings.Contains(r.Answer, "## Provider errors") || !strings.Contains(r.Answer, "- **Brave:** Brave Search API error 500") {
			t.Fatalf("%+v", r)
		}
	})

	tw(t, f, `provider arrays reject empty, duplicate, and aggregate entries`, func(t *testing.T) {
		got, err := NormalizeSearchProviderSelection([]any{" Brave ", "EXA"}, "provider")
		noErr(t, err)
		if !reflect.DeepEqual(got, ProviderSelection{List: []string{"brave", "exa"}}) {
			t.Fatalf("%+v", got)
		}
		_, err = NormalizeSearchProviderSelection([]any{}, "provider")
		wantErr(t, err, `must be a non-empty array`)
		_, err = NormalizeSearchProviderSelection([]any{"exa", "exa"}, "provider")
		wantErr(t, err, `must not contain duplicates: exa`)
		_, err = NormalizeSearchProviderSelection([]any{"all"}, "provider")
		wantErr(t, err, `contains an invalid provider: all`)
	})

	tskip(t, f, `"all" is a Curator provider but remains invalid inside sequential searchRouting`,
		"needs the Curator page generator (deferred UI); the searchRouting half is covered by TestAllInvalidInsideSearchRouting")
}

func TestAllProviderStartsEligibleProvidersTogether(t *testing.T) {
	isolate(t)
	t.Setenv("BRAVE_API_KEY", "brave-test-key")
	t.Setenv("TAVILY_API_KEY", "tavily-test-key")
	t.Setenv("ANYSEARCH_API_KEY", "must-not-run")
	g := newGate("exa", "brave", "tavily")
	useNet(t, func(c netCall) netReply {
		switch {
		case strings.HasPrefix(c.URL, "https://api.anysearch.com/"):
			return netReply{Err: errors.New("AnySearch must not run for provider all")}
		case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
			g.wait(t, "exa")
			return exaMCPText("Title: Shared result\nURL: https://example.com/shared\nText: Exa answer\n---")
		case strings.HasPrefix(c.URL, "https://api.search.brave.com/"):
			g.wait(t, "brave")
			return braveOK("Shared result", "https://example.com/shared", "Brave answer")
		case c.URL == "https://api.tavily.com/search":
			g.wait(t, "tavily")
			return reply(200, `{"answer":"Tavily answer","results":[{"title":"Tavily result","url":"https://example.com/tavily","content":"x"}]}`)
		}
		return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
	})
	r, err := Search(bg(), "combined", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
	noErr(t, err)
	if r.Provider != "all" || !eqStrings(g.sorted(), []string{"brave", "exa", "tavily"}) || !eqStrings(providerNames(r.ProviderResponses), []string{"exa", "brave", "tavily"}) ||
		!eqStrings(urlsOf(r.Results), []string{"https://example.com/shared", "https://example.com/tavily"}) {
		t.Fatalf("%+v", r)
	}
	for _, h := range []string{"## Exa", "## Brave", "## Tavily"} {
		if !strings.Contains(r.Answer, h) {
			t.Fatalf("missing %s in %q", h, r.Answer)
		}
	}
	if strings.Contains(r.Answer, "AnySearch") {
		t.Fatal("AnySearch must be excluded")
	}
}

func TestAllInvalidInsideSearchRouting(t *testing.T) {
	_, dir := isolate(t)
	writeConfig(t, dir, `{"searchRouting":{"providers":["all"],"fallbackOn":["network"]}}`)
	_, err := Search(bg(), "x", FullSearchOptions{Provider: Auto})
	wantErr(t, err, `searchRouting\.providers .*invalid provider: all`)
}

// Provider error classification (classifyProviderError in gemini-search.ts).
func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		provider, msg string
		want          string
	}{
		{"brave", "Brave Search API error 429: slow down", "quota"},
		{"tavily", "Tavily API error 432: plan", "quota"},
		{"brave", "Brave Search API error 402: pay", "quota"},
		{"brave", "Brave Search API error 401: no", "auth"},
		{"brave", "Brave Search API error 403: quota exceeded", "auth"},
		{"brave", "Brave Search API error 400: bad", "invalid-request"},
		{"brave", "Brave Search API error 503: down", "transient"},
		{"brave", "Brave Search API error 408: slow", "transient"},
		{"duckduckgo", "DuckDuckGo returned no parseable results (invalid response)", "invalid-response"},
		{"exa", "Provider returned empty response", "invalid-response"},
		{"exa", "Exa MCP returned an empty response", "unknown"},
		{"brave", "fetch failed", "network"},
		{"brave", "connect ECONNREFUSED", "network"},
		{"brave", "Too many requests", "quota"},
		{"brave", "rate limit hit", "quota"},
		{"brave", "Service unavailable right now", "transient"},
		{"brave", "BRAVE_BASE_URL must be an absolute HTTPS URL", "config"},
		{"brave", "something entirely different", "unknown"},
		{"brave", "the operation was aborted", "aborted"},
	}
	for _, c := range cases {
		got := ClassifyProviderError(c.provider, errors.New(c.msg))
		if got.Kind != c.want {
			t.Errorf("%q => %s, want %s", c.msg, got.Kind, c.want)
		}
	}
	if k := ClassifyProviderError("exa", &CredentialResolutionError{Provider: "Exa", Category: "command-failed"}).Kind; k != "credential" {
		t.Errorf("credential => %s", k)
	}
	if e := ClassifyProviderError("brave", errors.New("Brave Search API error 429: x")); e.Error() != "brave search failed (quota): Brave Search API error 429: x" || e.Status != 429 {
		t.Errorf("%v", e)
	}
}
