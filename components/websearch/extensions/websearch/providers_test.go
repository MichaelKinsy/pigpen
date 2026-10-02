package websearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of test/search-providers, exa-search-transport, exa-source-label, tavily-key-pool and
// duckduckgo-provider. `fetch` is the fakeNet transport; the upstream child processes become
// isolated temp environments.

func jsonBody(t *testing.T, c netCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(c.Body), &m); err != nil {
		t.Fatalf("body %q: %v", c.Body, err)
	}
	return m
}

func jsonReply(v any) netReply {
	b, _ := json.Marshal(v)
	return reply(200, string(b))
}

func urlsOf(rs []SearchResult) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.URL)
	}
	return out
}

func TestUpstream_search_providers(t *testing.T) {
	const f = "search-providers"

	tw(t, f, "Brave search applies domain filters in the query and returned results", func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-test-key")
		fn := useNet(t, func(c netCall) netReply {
			return jsonReply(map[string]any{"web": map[string]any{"results": []map[string]any{
				{"title": "GitHub", "url": "https://github.com/nicobailon/pi-web-access", "description": "repo"},
				{"title": "Gist", "url": "https://gist.github.com/nicobailon/abc", "description": "gist"},
				{"title": "Example", "url": "https://example.com/nope", "description": "example"},
			}}})
		})
		res, err := SearchWithBrave(bg(), "sdk docs", SearchOptions{DomainFilter: []string{"github.com", "-gist.github.com"}, NumResults: nf(2)})
		noErr(t, err)
		c := fn.calls[0]
		q := query(c.URL, "q")
		if !regexp.MustCompile(`site:github\.com`).MatchString(q) || !regexp.MustCompile(`NOT site:gist\.github\.com`).MatchString(q) {
			t.Fatal(q)
		}
		if query(c.URL, "count") != "20" || c.Header.Get("X-Subscription-Token") != "brave-test-key" {
			t.Fatalf("%v", c)
		}
		if !eqStrings(urlsOf(res.Results), []string{"https://github.com/nicobailon/pi-web-access"}) {
			t.Fatalf("%v", res.Results)
		}
	})

	tw(t, f, "Perplexity normalizes invalid result counts", func(t *testing.T) {
		isolate(t)
		t.Setenv("PERPLEXITY_API_KEY", "pplx-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "answer"}}},
				"citations": []string{"https://example.com/a", "https://example.com/b", "https://example.com/c", "https://example.com/d", "https://example.com/e"}})
		})
		var counts []int
		for _, n := range []float64{-1, nan(), 3.8} {
			r, err := SearchWithPerplexity(bg(), "q", SearchOptions{NumResults: nf(n)})
			noErr(t, err)
			counts = append(counts, len(r.Results))
		}
		if !reflect.DeepEqual(counts, []int{1, 5, 3}) {
			t.Fatal(counts)
		}
	})

	tw(t, f, "Perplexity retains cited sources beyond numResults", func(t *testing.T) {
		isolate(t)
		t.Setenv("PERPLEXITY_API_KEY", "pplx-test-key")
		useNet(t, func(c netCall) netReply {
			var body struct{ Messages []struct{ Content string } }
			_ = json.Unmarshal([]byte(c.Body), &body)
			answer := "The answer has no citations."
			if body.Messages[0].Content == "cited" {
				answer = "The answer cites [13]."
			}
			var cites []string
			for i := 1; i <= 13; i++ {
				cites = append(cites, fmt.Sprintf("https://example.com/source-%d", i))
			}
			return jsonReply(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": answer}}}, "citations": cites})
		})
		cited, err := SearchWithPerplexity(bg(), "cited", SearchOptions{NumResults: nf(8)})
		noErr(t, err)
		uncited, err := SearchWithPerplexity(bg(), "uncited", SearchOptions{NumResults: nf(8)})
		noErr(t, err)
		if len(cited.Results) != 13 || cited.Results[12] != (SearchResult{Title: "Source 13", URL: "https://example.com/source-13", Snippet: ""}) || len(uncited.Results) != 8 {
			t.Fatalf("%d %v %d", len(cited.Results), cited.Results[len(cited.Results)-1], len(uncited.Results))
		}
	})

	tw(t, f, "Tavily search uses bearer auth and maps filters/content", func(t *testing.T) {
		isolate(t)
		t.Setenv("TAVILY_API_KEY", "tvly-test-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"answer": "Tavily answer", "results": []map[string]any{{"title": "Tavily Docs", "url": "https://docs.tavily.com/search",
				"content": "Search docs snippet", "raw_content": "# Tavily Docs\nFull content"}}})
		})
		res, err := SearchWithTavily(bg(), "tavily search docs", SearchOptions{DomainFilter: []string{"https://docs.tavily.com/search", "-reddit.com"},
			RecencyFilter: "week", NumResults: nf(4), IncludeContent: true})
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != "https://api.tavily.com/search" || c.Header.Get("Authorization") != "Bearer tvly-test-key" {
			t.Fatalf("%v", c)
		}
		want := map[string]any{"query": "tavily search docs", "search_depth": "basic", "max_results": 4.0, "include_answer": "basic", "include_raw_content": "markdown",
			"time_range": "week", "include_domains": []any{"docs.tavily.com"}, "exclude_domains": []any{"reddit.com"}}
		if !reflect.DeepEqual(jsonBody(t, c), want) {
			t.Fatalf("%v", jsonBody(t, c))
		}
		if res.Answer != "Tavily answer" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Tavily Docs", URL: "https://docs.tavily.com/search", Snippet: "Search docs snippet"}}) {
			t.Fatalf("%+v", res)
		}
		if !reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://docs.tavily.com/search", Title: "Tavily Docs", Content: "# Tavily Docs\nFull content"}}) {
			t.Fatalf("%+v", res.InlineContent)
		}
	})

	tw(t, f, "Brave, keyed Exa, and Tavily honor base URL overrides without leaking credentials across origins", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"braveApiKey":"brave-config-key","braveBaseUrl":"https://gateway.example.com/brave/res/v1/","exaApiKey":"exa-config-key","exaBaseUrl":"https://gateway.example.com/exa/","tavilyApiKey":"tavily-config-key","tavilyBaseUrl":"https://gateway.example.com/tavily/"}`)
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://gateway.example.com/"):
				status := 307
				if strings.Contains(c.URL, "/tavily/") {
					status = 302
				}
				return redirectTo(status, strings.Replace(c.URL, "gateway.example.com", "redirect.example.com", 1))
			case strings.Contains(c.URL, "/brave/res/v1/web/search?"):
				return reply(200, `{"web":{"results":[]}}`)
			case strings.HasSuffix(c.URL, "/exa/search"):
				return reply(200, `{"results":[]}`)
			case strings.HasSuffix(c.URL, "/tavily/search"):
				return reply(200, `{"answer":"answer","results":[]}`)
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		_, err := SearchWithBrave(bg(), "configured", SearchOptions{})
		noErr(t, err)
		_, err = SearchWithExa(bg(), "default search", SearchOptions{})
		noErr(t, err)
		_, err = SearchWithTavily(bg(), "configured", SearchOptions{})
		noErr(t, err)
		t.Setenv("BRAVE_BASE_URL", "https://env.example.com/brave/res/v1/")
		t.Setenv("EXA_BASE_URL", "https://env.example.com/exa/")
		t.Setenv("TAVILY_BASE_URL", "https://env.example.com/tavily/")
		_, err = SearchWithBrave(bg(), "environment", SearchOptions{})
		noErr(t, err)
		_, err = SearchWithExa(bg(), "environment", SearchOptions{})
		noErr(t, err)
		_, err = SearchWithTavily(bg(), "environment", SearchOptions{})
		noErr(t, err)
		t.Setenv("BRAVE_BASE_URL", "not-a-url")
		_, invalidErr := SearchWithBrave(bg(), "invalid", SearchOptions{})
		t.Setenv("BRAVE_BASE_URL", "http://gateway.example.com/brave/res/v1")
		_, plainErr := SearchWithBrave(bg(), "plaintext", SearchOptions{})

		wantURLs := []string{
			"https://gateway.example.com/brave/res/v1/web/search?q=configured&count=5",
			"https://redirect.example.com/brave/res/v1/web/search?q=configured&count=5",
			"https://gateway.example.com/exa/search",
			"https://redirect.example.com/exa/search",
			"https://gateway.example.com/tavily/search",
			"https://redirect.example.com/tavily/search",
			"https://env.example.com/brave/res/v1/web/search?q=environment&count=5",
			"https://env.example.com/exa/search",
			"https://env.example.com/tavily/search",
		}
		if !eqStrings(fn.urls(), wantURLs) {
			t.Fatalf("%v", fn.urls())
		}
		cred := func(c netCall) string {
			for _, h := range []string{"X-Subscription-Token", "X-Api-Key", "Authorization"} {
				if v := c.Header.Get(h); v != "" {
					return v
				}
			}
			return "<none>"
		}
		var got []string
		for _, c := range fn.calls {
			got = append(got, cred(c))
		}
		wantCred := []string{"brave-config-key", "<none>", "exa-config-key", "<none>", "Bearer tavily-config-key", "<none>", "brave-config-key", "exa-config-key", "Bearer tavily-config-key"}
		if !eqStrings(got, wantCred) {
			t.Fatalf("%v", got)
		}
		// tavily POST with a JSON body, then the 302 turns it into a body-less GET.
		a, b := fn.calls[4], fn.calls[5]
		if a.Method != "POST" || a.Body == "" || a.Header.Get("Content-Type") != "application/json" || b.Method != "GET" || b.Body != "" || b.Header.Get("Content-Type") != "" {
			t.Fatalf("%v %v", a, b)
		}
		wantErr(t, invalidErr, `^BRAVE_BASE_URL must be an absolute HTTP\(S\) URL$`)
		wantErr(t, plainErr, `^BRAVE_BASE_URL must be an absolute HTTPS URL$`)
		// upstream also asserts init.redirect === "manual"; the Go transport never follows redirects
		// on its own (CheckRedirect), asserted in TestAPITransportNeverFollowsRedirects.
	})

	tw(t, f, "provider base URLs allow HTTP only on exact loopback hosts", func(t *testing.T) {
		isolate(t)
		t.Setenv("BRAVE_API_KEY", "brave-loopback-key")
		fn := useNet(t, func(c netCall) netReply {
			if query(c.URL, "q") == "redirect" {
				return redirectTo(307, "https://remote.example.com/redirected")
			}
			return reply(200, `{"web":{"results":[]}}`)
		})
		t.Setenv("BRAVE_BASE_URL", "http://localhost:8080/api")
		_, _ = SearchWithBrave(bg(), "redirect", SearchOptions{})
		accepted := []string{"http://localhost:8080/api/", "http://LOCALHOST:8080/api", "http://localhost.:8080/api", "http://127.0.0.1:8080/api",
			"http://127.42.3.4:8080/api", "http://[::1]:8080/api", "http://[0:0:0:0:0:0:0:1]:8080/api", "https://gateway.example.com/api"}
		for i, base := range accepted {
			t.Setenv("BRAVE_BASE_URL", base)
			if _, err := SearchWithBrave(bg(), fmt.Sprintf("accepted-%d", i), SearchOptions{}); err != nil {
				t.Fatalf("%s: %v", base, err)
			}
		}
		rejected := []string{"http://example.com/api", "http://localhost.example/api", "http://foo.localhost/api", "http://10.0.0.1/api",
			"http://169.254.169.254/api", "http://0.0.0.0/api", "http://[::]/api", "http://[::ffff:127.0.0.1]/api"}
		for _, base := range rejected {
			t.Setenv("BRAVE_BASE_URL", base)
			_, err := SearchWithBrave(bg(), "rejected", SearchOptions{})
			if err == nil || err.Error() != "BRAVE_BASE_URL must be an absolute HTTPS URL" {
				t.Fatalf("%s: %v", base, err)
			}
		}
		invalid := map[string]string{
			"http://user:secret@localhost:8080/api": "BRAVE_BASE_URL must not include credentials",
			"http://localhost:8080/api?debug=true":  "BRAVE_BASE_URL must not include query parameters or fragments",
			"http://localhost:8080/api#fragment":    "BRAVE_BASE_URL must not include query parameters or fragments",
		}
		for base, want := range invalid {
			t.Setenv("BRAVE_BASE_URL", base)
			if _, err := SearchWithBrave(bg(), "invalid", SearchOptions{}); err == nil || err.Error() != want {
				t.Fatalf("%s: %v", base, err)
			}
		}
		if fn.count() != 10 {
			t.Fatalf("calls %v", fn.urls())
		}
		if fn.calls[0].URL != "http://localhost:8080/api/web/search?q=redirect&count=5" || fn.calls[0].Header.Get("X-Subscription-Token") != "brave-loopback-key" ||
			fn.calls[1].URL != "https://remote.example.com/redirected" || fn.calls[1].Header.Get("X-Subscription-Token") != "" {
			t.Fatalf("%v", fn.calls[:2])
		}
		for _, c := range fn.calls[2:] {
			if c.Header.Get("X-Subscription-Token") != "brave-loopback-key" {
				t.Fatal("credential must be sent to every accepted base URL")
			}
		}
	})

	tw(t, f, "auto provider falls through to Tavily after unavailable earlier providers", func(t *testing.T) {
		isolate(t)
		t.Setenv("TAVILY_API_KEY", "tvly-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp") {
				return reply(503, "Exa unavailable")
			}
			if c.URL == "https://api.tavily.com/search" {
				return jsonReply(map[string]any{"answer": "Auto Tavily answer", "results": []map[string]any{{"title": "Tavily Auto", "url": "https://docs.tavily.com/auto", "content": "auto snippet"}}})
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		res, err := Search(bg(), "auto tavily docs", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		sawExa, sawTavily := false, false
		for _, u := range fn.urls() {
			sawExa = sawExa || strings.HasPrefix(u, "https://mcp.exa.ai/mcp")
			sawTavily = sawTavily || u == "https://api.tavily.com/search"
		}
		if !sawExa || !sawTavily || res.Provider != "tavily" || res.Answer != "Auto Tavily answer" {
			t.Fatalf("%v %+v", fn.urls(), res)
		}
	})

	tw(t, f, "Exa direct API key ignores full legacy usage counter", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"exaApiKey":"exa-paid-key"}`)
		usage := filepath.Join(dir, "exa-usage.json")
		legacy := fmt.Sprintf(`{"month":%q,"count":1000}`, time.Now().UTC().Format("2006-01"))
		noErr(t, os.WriteFile(usage, []byte(legacy), 0o600))
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"results": []map[string]any{{"title": "Exa Docs", "url": "https://exa.ai/docs", "highlights": []string{"Paid Exa answer"}}}})
		})
		if !IsExaAvailable() {
			t.Fatal("Exa is always available (keyless MCP)")
		}
		res, err := SearchWithExa(bg(), "paid exa query", SearchOptions{})
		noErr(t, err)
		c := fn.calls[0]
		want := map[string]any{"query": "paid exa query", "type": "auto", "numResults": 5.0, "contents": map[string]any{"highlights": true}}
		if c.URL != "https://api.exa.ai/search" || !reflect.DeepEqual(jsonBody(t, c), want) || c.Header.Get("x-api-key") != "exa-paid-key" || c.Header.Get("x-exa-integration") != "pi-web-access" {
			t.Fatalf("%v", c)
		}
		if res.Answer != "Paid Exa answer\nSource: Exa Docs (https://exa.ai/docs)" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Exa Docs", URL: "https://exa.ai/docs"}}) {
			t.Fatalf("%+v", res)
		}
		after, _ := os.ReadFile(usage)
		if string(after) != legacy {
			t.Fatalf("legacy usage file changed: %s", after)
		}
	})

	tw(t, f, "Exa command source is lazy, overrides stale env, and rotates per request", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell script credential command")
		}
		_, dir := isolate(t)
		script := filepath.Join(dir, "read-key.sh")
		counter := filepath.Join(dir, "counter")
		noErr(t, os.WriteFile(script, []byte("#!/bin/sh\ncount=0\n[ ! -f \"$1\" ] || count=$(cat \"$1\")\ncount=$((count + 1))\nprintf '%s' \"$count\" >\"$1\"\nprintf 'synthetic-exa-%s\\n' \"$count\"\n"), 0o700))
		writeConfig(t, dir, fmt.Sprintf(`{"exaApiKey":%q}`, "!"+script+" "+counter))
		t.Setenv("EXA_API_KEY", "stale-exa-environment-value")
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"results":[]}`) })
		available := HasExaAPIKey()
		_, statErr := os.Stat(counter)
		lazy := os.IsNotExist(statErr)
		_, err := SearchWithExa(bg(), "first", SearchOptions{})
		noErr(t, err)
		_, err = SearchWithExa(bg(), "second", SearchOptions{})
		noErr(t, err)
		keys := []string{fn.calls[0].Header.Get("x-api-key"), fn.calls[1].Header.Get("x-api-key")}
		if !available || !lazy || !eqStrings(keys, []string{"synthetic-exa-1", "synthetic-exa-2"}) {
			t.Fatalf("%v %v %v", available, lazy, keys)
		}
	})

	tw(t, f, "failed Exa command source is redacted and blocks MCP or provider fallback", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell script credential command")
		}
		_, dir := isolate(t)
		script := filepath.Join(dir, "fail-key.sh")
		noErr(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf 'SYNTHETIC_SECRET_MUST_NOT_ESCAPE\\n' >&2\nexit 9\n"), 0o700))
		writeConfig(t, dir, fmt.Sprintf(`{"exaApiKey":%q}`, "!"+script))
		t.Setenv("EXA_API_KEY", "stale-exa-environment-value")
		t.Setenv("TAVILY_API_KEY", "stale-alternate-provider-value")
		fn := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("unexpected fetch")} })
		_, err := Search(bg(), "must fail closed", FullSearchOptions{Provider: Auto})
		if fn.count() != 0 {
			t.Fatalf("fetch calls: %v", fn.urls())
		}
		wantErr(t, err, `^Exa credential resolution failed: command-failed$`)
		if strings.Contains(err.Error(), "SYNTHETIC_SECRET_MUST_NOT_ESCAPE") || strings.Contains(err.Error(), script) {
			t.Fatal("secret or command path leaked")
		}
	})

	tw(t, f, "Exa provider errors redact the resolved credential", func(t *testing.T) {
		isolate(t)
		const secret = "SYNTHETIC_EXA_SECRET_MUST_NOT_ESCAPE"
		t.Setenv("EXA_API_KEY", secret)
		useNet(t, func(netCall) netReply { return reply(400, "provider echoed "+secret) })
		_, err := SearchWithExa(bg(), "redaction test", SearchOptions{})
		if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("%v", err)
		}
	})

	tw(t, f, "keyless Exa search sends filters to the advanced MCP tool as parameters", func(t *testing.T) {
		isolate(t)
		fn := useNet(t, func(netCall) netReply {
			inner, _ := json.Marshal(map[string]any{"results": []map[string]any{{"title": "Advanced result", "url": "https://docs.example.com/advanced", "text": "full page text", "highlights": []string{"relevant highlight"}}}})
			return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": string(inner)}}}})
		})
		res, err := SearchWithExa(bg(), "semantic query", SearchOptions{NumResults: nf(3), RecencyFilter: "week", DomainFilter: []string{"docs.example.com", "-spam.example.net"}, IncludeContent: true})
		noErr(t, err)
		c := fn.calls[0]
		body := jsonBody(t, c)
		params := body["params"].(map[string]any)
		args := params["arguments"].(map[string]any)
		if c.URL != "https://mcp.exa.ai/mcp?tools=web_search_advanced_exa" || params["name"] != "web_search_advanced_exa" || args["startPublishedDate"] == nil || args["startPublishedDate"] == "" {
			t.Fatalf("%v", c)
		}
		delete(args, "startPublishedDate")
		want := map[string]any{"query": "semantic query", "type": "auto", "numResults": 3.0, "includeDomains": []any{"docs.example.com"}, "excludeDomains": []any{"spam.example.net"},
			"enableHighlights": true, "textMaxCharacters": 50000.0}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("%v", args)
		}
		if !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Advanced result", URL: "https://docs.example.com/advanced"}}) || !strings.Contains(res.Answer, "relevant highlight") ||
			!reflect.DeepEqual(res.InlineContent, []ExtractedContent{{URL: "https://docs.example.com/advanced", Title: "Advanced result", Content: "full page text"}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "keyless Exa search falls back to the default MCP tool when the advanced tool is missing", func(t *testing.T) {
		isolate(t)
		var tools []string
		useNet(t, func(c netCall) netReply {
			var body struct{ Params struct{ Name string } }
			_ = json.Unmarshal([]byte(c.Body), &body)
			tools = append(tools, body.Params.Name)
			if strings.Contains(c.URL, "web_search_advanced_exa") {
				return reply(200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Tool web_search_advanced_exa not found"}}`)
			}
			return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": "Title: Basic result\nURL: https://example.com/basic\nText: basic text\n---"}}}})
		})
		res, err := SearchWithExa(bg(), "fallback query", SearchOptions{DomainFilter: []string{"example.com"}})
		noErr(t, err)
		if !eqStrings(tools, []string{"web_search_advanced_exa", "web_search_exa"}) || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Basic result", URL: "https://example.com/basic"}}) {
			t.Fatalf("%v %+v", tools, res)
		}
	})

}

func nan() float64 { var z float64; return z / z }

// The Go transport never follows a redirect by itself: providers handle them by hand so that
// credential headers can be dropped (the upstream asserts init.redirect === "manual").
func TestAPITransportNeverFollowsRedirects(t *testing.T) {
	c, ok := httpDoer.(*http.Client)
	if !ok || c.CheckRedirect == nil || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("the default API client must not follow redirects")
	}
}

func TestUpstream_exa_search_transport(t *testing.T) {
	const f = "exa-search-transport"
	runKeyed := func(t *testing.T, calls ...SearchOptions) (*fakeNet, []*SearchResponse) {
		isolate(t)
		t.Setenv("EXA_API_KEY", "exa-transport-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"results": []map[string]any{
				{"title": "Exa Docs", "url": "https://exa.ai/docs", "text": "full docs text", "highlights": []string{"docs highlight"}},
				{"url": "https://exa.ai/untitled", "highlights": []string{}},
			}})
		})
		var out []*SearchResponse
		for _, o := range calls {
			r, err := SearchWithExa(bg(), "q", o)
			noErr(t, err)
			out = append(out, r)
		}
		return fn, out
	}

	tw(t, f, "keyed Exa default and explicit five-result searches post to /search, never /answer", func(t *testing.T) {
		fn, results := runKeyed(t, SearchOptions{}, SearchOptions{NumResults: nf(5)})
		for _, c := range fn.calls {
			want := map[string]any{"query": "q", "type": "auto", "numResults": 5.0, "contents": map[string]any{"highlights": true}}
			if c.URL != "https://api.exa.ai/search" || c.Method != "POST" || c.Header.Get("x-api-key") != "exa-transport-key" || !reflect.DeepEqual(jsonBody(t, c), want) {
				t.Fatalf("%v", c)
			}
		}
		for _, r := range results {
			if r.Answer != "docs highlight\nSource: Exa Docs (https://exa.ai/docs)" || r.InlineContent != nil ||
				!reflect.DeepEqual(r.Results, []SearchResult{{Title: "Exa Docs", URL: "https://exa.ai/docs"}, {Title: "exa.ai", URL: "https://exa.ai/untitled"}}) {
				t.Fatalf("%+v", r)
			}
		}
	})

	tw(t, f, "keyed Exa filtered searches keep filters and inline content on /search", func(t *testing.T) {
		fn, results := runKeyed(t, SearchOptions{NumResults: nf(3), RecencyFilter: "week", DomainFilter: []string{"exa.ai", "-spam.example"}, IncludeContent: true})
		if len(fn.calls) != 1 || fn.calls[0].URL != "https://api.exa.ai/search" {
			t.Fatal(fn.urls())
		}
		body := jsonBody(t, fn.calls[0])
		start, _ := body["startPublishedDate"].(string)
		if _, err := time.Parse(time.RFC3339, start); err != nil {
			t.Fatalf("startPublishedDate %q", start)
		}
		delete(body, "startPublishedDate")
		want := map[string]any{"query": "q", "type": "auto", "numResults": 3.0, "includeDomains": []any{"exa.ai"}, "excludeDomains": []any{"spam.example"}, "contents": map[string]any{"text": true, "highlights": true}}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("%v", body)
		}
		if !reflect.DeepEqual(results[0].InlineContent, []ExtractedContent{{URL: "https://exa.ai/docs", Title: "Exa Docs", Content: "full docs text"}}) {
			t.Fatalf("%+v", results[0].InlineContent)
		}
	})
}

func TestUpstream_exa_source_label(t *testing.T) {
	const f = "exa-source-label"
	urls := []string{"https://cdn.jsdelivr.net/npm/pi-web-access@0.27.0/index.ts", "mailto:team@example.com", "file:///tmp/notes.txt", "not-a-url", "https://example.com/kept"}
	titles := []string{"cdn.jsdelivr.net", "Source 2", "Source 3", "Source 4", "Kept title"}
	assertLabels := func(t *testing.T, r *SearchResponse, includeContent bool) {
		t.Helper()
		var got, sources, inline []string
		for _, x := range r.Results {
			got = append(got, x.Title)
		}
		for _, line := range strings.Split(r.Answer, "\n") {
			if strings.HasPrefix(line, "Source: ") {
				sources = append(sources, line)
			}
		}
		var wantSources, wantInline []string
		for i, title := range titles {
			wantSources = append(wantSources, fmt.Sprintf("Source: %s (%s)", title, urls[i]))
			wantInline = append(wantInline, urls[i]+"|"+title)
		}
		for _, c := range r.InlineContent {
			inline = append(inline, c.URL+"|"+c.Title)
		}
		if !eqStrings(got, titles) || !eqStrings(sources, wantSources) {
			t.Fatalf("%v %v", got, sources)
		}
		if includeContent && !eqStrings(inline, wantInline) || !includeContent && r.InlineContent != nil {
			t.Fatalf("inline %v", inline)
		}
	}

	tw(t, f, "keyed Exa search labels untitled results by hostname, else Source N", func(t *testing.T) {
		isolate(t)
		t.Setenv("EXA_API_KEY", "exa-test-key")
		useNet(t, func(c netCall) netReply {
			if c.URL != "https://api.exa.ai/search" {
				t.Errorf("url %s", c.URL)
			}
			var rs []map[string]any
			for i, u := range urls {
				title := ""
				if i == 4 {
					title = "Kept title"
				}
				rs = append(rs, map[string]any{"title": title, "url": u, "highlights": []string{fmt.Sprintf("snippet %d", i+1)}, "text": fmt.Sprintf("page %d", i+1)})
			}
			return jsonReply(map[string]any{"results": rs})
		})
		r, err := SearchWithExa(bg(), "labels", SearchOptions{NumResults: nf(10)})
		noErr(t, err)
		assertLabels(t, r, false)
		r, err = SearchWithExa(bg(), "labels", SearchOptions{NumResults: nf(10), IncludeContent: true})
		noErr(t, err)
		assertLabels(t, r, true)
	})

	tw(t, f, "keyless Exa MCP search labels untitled results by hostname, else Source N", func(t *testing.T) {
		isolate(t)
		var blocks []string
		for i, u := range urls {
			title := ""
			if i == 4 {
				title = "Kept title"
			}
			blocks = append(blocks, fmt.Sprintf("Title: %s\nURL: %s\nText: snippet %d", title, u, i+1))
		}
		text := strings.Join(blocks, "\n\n")
		useNet(t, func(c netCall) netReply {
			if !strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp") {
				t.Errorf("url %s", c.URL)
			}
			return jsonReply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
		})
		r, err := SearchWithExa(bg(), "labels", SearchOptions{})
		noErr(t, err)
		assertLabels(t, r, false)
		r, err = SearchWithExa(bg(), "labels", SearchOptions{IncludeContent: true})
		noErr(t, err)
		assertLabels(t, r, true)
	})
}

func TestUpstream_tavily_key_pool(t *testing.T) {
	const f = "tavily-key-pool"
	type out struct {
		available bool
		keys      []string
		results   int
		err       string
	}
	run := func(t *testing.T, config string, env map[string]string, succeedWith string, failStatus int) out {
		_, dir := isolate(t)
		if config == "" {
			config = "{}"
		}
		writeConfig(t, dir, config)
		for k, v := range env {
			t.Setenv(k, v)
		}
		var keys []string
		useNet(t, func(c netCall) netReply {
			key := strings.TrimPrefix(c.Header.Get("Authorization"), "Bearer ")
			keys = append(keys, key)
			if key == succeedWith {
				return jsonReply(map[string]any{"results": []map[string]any{{"title": "Tavily", "url": "https://example.com/tavily", "content": "result"}}})
			}
			return reply(failStatus, "abort this quota-limited request")
		})
		o := out{available: IsTavilyAvailable()}
		r, err := SearchWithTavily(bg(), "tavily", SearchOptions{NumResults: nf(1)})
		if err != nil {
			o.err = err.Error()
		} else {
			o.results = len(r.Results)
		}
		o.keys = keys
		return o
	}

	tw(t, f, "a plain TAVILY_API_KEY works alone and out-of-range pool slots are ignored", func(t *testing.T) {
		o := run(t, "", map[string]string{"TAVILY_API_KEY": "solo", "TAVILY_API_KEY_25": "out-of-range"}, "solo", 429)
		if !o.available || !eqStrings(o.keys, []string{"solo"}) || o.results != 1 {
			t.Fatalf("%+v", o)
		}
	})
	tw(t, f, "pool keys alone make Tavily available and fail over on quota responses", func(t *testing.T) {
		o := run(t, "", map[string]string{"TAVILY_API_KEY_1": "key-1", "TAVILY_API_KEY_2": "key-2", "TAVILY_API_KEY_3": "key-3"}, "key-3", 429)
		if !o.available || !eqStrings(o.keys, []string{"key-1", "key-2", "key-3"}) || o.results != 1 {
			t.Fatalf("%+v", o)
		}
	})
	tw(t, f, "TAVILY_API_KEY_INDEX starts at the next configured slot and wraps", func(t *testing.T) {
		o := run(t, "", map[string]string{"TAVILY_API_KEY_1": "key-1", "TAVILY_API_KEY_5": "key-5", "TAVILY_API_KEY_INDEX": "2"}, "key-1", 429)
		if !eqStrings(o.keys, []string{"key-5", "key-1"}) {
			t.Fatalf("%+v", o)
		}
	})
	tw(t, f, "duplicate pool and standalone keys are tried once", func(t *testing.T) {
		o := run(t, "", map[string]string{"TAVILY_API_KEY": "dup", "TAVILY_API_KEY_1": "dup", "TAVILY_API_KEY_3": "dup", "TAVILY_API_KEY_5": "key-5", "TAVILY_API_KEY_INDEX": "3"}, "", 429)
		if !eqStrings(o.keys, []string{"dup", "key-5"}) {
			t.Fatalf("%+v", o)
		}
	})
	tskip(t, f, "the standalone environment key is tried after the pool and logs one activity entry",
		"the activity monitor widget is deferred; key order is covered by TestTavilyStandaloneKeyAfterPool")
	tw(t, f, "the configured key is tried after the pool", func(t *testing.T) {
		o := run(t, `{"tavilyApiKey":"config-key"}`, map[string]string{"TAVILY_API_KEY_1": "key-1"}, "config-key", 429)
		if !eqStrings(o.keys, []string{"key-1", "config-key"}) {
			t.Fatalf("%+v", o)
		}
	})
	tw(t, f, "an unresolvable explicit credential source fails closed after the pool", func(t *testing.T) {
		o := run(t, `{"tavilyApiKey":"$PI_WEB_ACCESS_TEST_MISSING_TAVILY_KEY"}`, map[string]string{"TAVILY_API_KEY": "solo", "TAVILY_API_KEY_1": "key-1"}, "", 429)
		if !eqStrings(o.keys, []string{"key-1"}) || o.err != "Tavily credential resolution failed: environment-empty" {
			t.Fatalf("%+v", o)
		}
	})
	tw(t, f, "non-quota failures do not consume more pool keys", func(t *testing.T) {
		o := run(t, "", map[string]string{"TAVILY_API_KEY_1": "key-1", "TAVILY_API_KEY_2": "key-2"}, "", 400)
		if !eqStrings(o.keys, []string{"key-1"}) || !strings.Contains(o.err, "Tavily API error 400") {
			t.Fatalf("%+v", o)
		}
	})
}

// Extra (not an upstream case): the key order of the skipped activity-entry twin.
func TestTavilyStandaloneKeyAfterPool(t *testing.T) {
	isolate(t)
	t.Setenv("TAVILY_API_KEY", "solo")
	t.Setenv("TAVILY_API_KEY_1", "key-1")
	t.Setenv("TAVILY_API_KEY_5", "key-5")
	t.Setenv("TAVILY_API_KEY_INDEX", "5")
	var keys []string
	useNet(t, func(c netCall) netReply {
		key := strings.TrimPrefix(c.Header.Get("Authorization"), "Bearer ")
		keys = append(keys, key)
		if key == "solo" {
			return reply(200, `{"results":[]}`)
		}
		return reply(429, "quota")
	})
	_, err := SearchWithTavily(bg(), "q", SearchOptions{})
	noErr(t, err)
	if !eqStrings(keys, []string{"key-5", "key-1", "solo"}) {
		t.Fatal(keys)
	}
}

const ddgResultHTML = `
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fdocs.example.com%2Fa"> Example Docs </a><a class="result__snippet">First snippet</a></div>
<div class="result"><a class="result__a" href="https://example.net/b">Example Net</a><div class="result__snippet">Second snippet</div></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=javascript%3Aalert(1)">Bad</a><div class="result__snippet">Bad snippet</div></div>`

func TestUpstream_duckduckgo_provider(t *testing.T) {
	const f = "duckduckgo-provider"

	tw(t, f, "DuckDuckGo uses the fixed HTML endpoint, decodes redirects, and caps results", func(t *testing.T) {
		isolate(t)
		fn := useNet(t, func(netCall) netReply { return reply(200, ddgResultHTML) })
		res, err := SearchWithDuckDuckGo(bg(), "example search", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		c := fn.calls[0]
		if !strings.HasPrefix(c.URL, "https://html.duckduckgo.com/html/?") || query(c.URL, "q") != "example search" || c.Header.Get("Accept") != "text/html" ||
			!strings.Contains(c.Header.Get("User-Agent"), "Mozilla") || !IsDuckDuckGoAvailable() {
			t.Fatalf("%v", c)
		}
		if !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Example Docs", URL: "https://docs.example.com/a", Snippet: "First snippet"}}) {
			t.Fatalf("%+v", res.Results)
		}
	})

	tw(t, f, "DuckDuckGo enforces local domain filters and accepts filtered empty results", func(t *testing.T) {
		isolate(t)
		useNet(t, func(netCall) netReply { return reply(200, ddgResultHTML) })
		allowed, err := SearchWithDuckDuckGo(bg(), "example", SearchOptions{DomainFilter: []string{"example.net"}, NumResults: nf(3)})
		noErr(t, err)
		filtered, err := SearchWithDuckDuckGo(bg(), "example", SearchOptions{DomainFilter: []string{"example.com", "-docs.example.com"}})
		noErr(t, err)
		if !reflect.DeepEqual(allowed.Results, []SearchResult{{Title: "Example Net", URL: "https://example.net/b", Snippet: "Second snippet"}}) || len(filtered.Results) != 0 {
			t.Fatalf("%+v %+v", allowed.Results, filtered.Results)
		}
	})

	tw(t, f, "DuckDuckGo invalid HTML fails directly and falls through configured invalid-response routing", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["duckduckgo","tavily"],"fallbackOn":["invalid-response"]}}`)
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://html.duckduckgo.com/html/") {
				return reply(200, "<html>blocked</html>")
			}
			if c.URL == "https://api.tavily.com/search" {
				return reply(200, `{"answer":"fallback","results":[]}`)
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		_, directErr := SearchWithDuckDuckGo(bg(), "blocked", SearchOptions{})
		routed, err := Search(bg(), "blocked", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		wantErr(t, directErr, `DuckDuckGo returned no parseable results`)
		if routed.Provider != "tavily" || !eqStrings(fn.urls(), []string{"https://html.duckduckgo.com/html/?q=blocked", "https://html.duckduckgo.com/html/?q=blocked", "https://api.tavily.com/search"}) {
			t.Fatalf("%s %v", routed.Provider, fn.urls())
		}
	})

	tw(t, f, "DuckDuckGo 503 falls through configured transient routing", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["duckduckgo","tavily"],"fallbackOn":["transient"]}}`)
		t.Setenv("TAVILY_API_KEY", "tavily-test-key")
		useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://html.duckduckgo.com/html/") {
				return reply(503, "unavailable")
			}
			return reply(200, `{"answer":"fallback","results":[]}`)
		})
		r, err := Search(bg(), "unavailable", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "tavily" {
			t.Fatal(r.Provider)
		}
	})
}
