package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of the upstream tests of Baizhi (MCP over HTTP), Jina Search, SearXNG, SerpBase and
// Bocha. As in rest_providers_test.go, request/answer cases replay the oracle fixture.

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func textResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

const baizhiKey = "synthetic-baizhi-credential"

func baizhiCase(mode string) *fixtureCase {
	return &fixtureCase{Baizhi: mode, Env: map[string]string{"BAIZHI_API_KEY": baizhiKey}}
}

func TestUpstream_baizhi_provider(t *testing.T) {
	const F = "baizhi-provider"
	tw(t, F, "Baizhi uses full MCP initialization, session and schema arguments; preserves opaque output", func(t *testing.T) {
		fixtureCases(t, "baizhi-json", "baizhi-text", "baizhi-sse")
		d, res, err := runFixture(t, loadFixtures(t)["baizhi-json"])
		noErr(t, err)
		if len(res.Results) != 0 || !strings.Contains(res.Answer, `"unknown_service_field"`) {
			t.Fatal(res)
		}
		methods := []string{}
		for _, r := range d.requests {
			if m, ok := r.Body.(map[string]any); ok {
				methods = append(methods, fmt.Sprint(m["method"]))
			}
		}
		if !eqStrings(methods, []string{"initialize", "notifications/initialized", "tools/call"}) || d.requests[len(d.requests)-1].Method != "DELETE" {
			// the GET for the optional event stream runs beside tools/call; the DELETE is last
			t.Fatal(methods, d.requests[len(d.requests)-1].Method)
		}
	})
	tw(t, F, "Baizhi output and errors never disclose the key", func(t *testing.T) {
		fixtureCases(t, "baizhi-redact", "baizhi-tool-error", "baizhi-empty", "baizhi-http401")
		for _, id := range []string{"baizhi-redact", "baizhi-tool-error", "baizhi-empty", "baizhi-http401"} {
			d, res, err := runFixture(t, loadFixtures(t)[id])
			out := fmt.Sprint(res)
			if err != nil {
				out = err.Error()
				wantErr(t, err, `Baizhi`)
			}
			if strings.Contains(out, baizhiKey) {
				t.Fatalf("%s: %s", id, out)
			}
			if id == "baizhi-http401" {
				wantErr(t, err, `HTTP 401`)
				for _, r := range d.requests {
					if m, ok := r.Body.(map[string]any); ok && m["method"] == "tools/call" {
						t.Fatal("no tool call after a rejected key")
					}
				}
			}
		}
	})
	tw(t, F, "Baizhi rejects redirects without contacting a second origin", func(t *testing.T) {
		fixtureCases(t, "baizhi-redirect")
		d, _, err := runFixture(t, loadFixtures(t)["baizhi-redirect"])
		wantErr(t, err, `Baizhi network request failed`)
		if len(d.requests) != 1 {
			t.Fatal(d.requests)
		}
	})
	tw(t, F, "Baizhi rejects missing credentials, empty query, invalid filter and pre-aborted calls before network", func(t *testing.T) {
		for _, id := range []string{"baizhi-missing-key", "baizhi-empty-query", "baizhi-invalid-filter", "baizhi-pre-aborted"} {
			d, _, _ := runFixture(t, loadFixtures(t)[id])
			if len(d.requests) != 0 {
				t.Fatalf("%s reached the network: %v", id, d.requests)
			}
		}
		fixtureCases(t, "baizhi-missing-key", "baizhi-empty-query", "baizhi-invalid-filter", "baizhi-pre-aborted")
		isolate(t)
		if IsBaizhiAvailable() {
			t.Fatal("available without a credential")
		}
	})
	tw(t, F, "malformed credentials fail locally with a fixed message and never reach HTTP", func(t *testing.T) {
		fixtureCases(t, "baizhi-malformed-credential")
		d, _, err := runFixture(t, loadFixtures(t)["baizhi-malformed-credential"])
		if err == nil || err.Error() != "Baizhi credential resolution failed: invalid-header-value" || len(d.requests) != 0 {
			t.Fatal(err, d.requests)
		}
	})
	tw(t, F, "Baizhi supports shared credential source rules without credential-resolution side effects at availability time", func(t *testing.T) {
		fixtureCases(t, "baizhi-redact", "baizhi-env-empty-named", "baizhi-config-root")
		c := loadFixtures(t)["baizhi-redact"]
		d, _, err := runFixture(t, c)
		noErr(t, err)
		if !IsBaizhiAvailable() {
			t.Fatal("a named credential source is available")
		}
		for _, r := range d.requests {
			if r.Headers["authorization"] != "Bearer "+baizhiKey {
				t.Fatalf("wrong credential sent: %v", r.Headers)
			}
		}
		wantErr(t, errors.New(loadFixtures(t)["baizhi-env-empty-named"].Outcome.Error), `environment-empty`)
	})
	tw(t, F, "Baizhi is explicit-only, available to provider arrays, labels and the Curator", func(t *testing.T) {
		adapted(t, "the Curator page half has no Go counterpart (the curator is a later slice); selection, labels and calls run")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"webSearch":{"allowedProviders":["baizhi"]}}`)
		t.Setenv("BAIZHI_API_KEY", baizhiKey)
		d := &fixtureDoer{c: baizhiCase("json")}
		t.Cleanup(SetHTTP(d))
		for _, p := range []string{"baizhi"} {
			if sliceHas(AllSearchProviders, p) {
				t.Fatal("baizhi is in provider all")
			}
		}
		if _, err := restSearch(bg(), "q", Auto); err == nil {
			t.Fatal("auto must not run Baizhi")
		}
		if _, err := restSearch(bg(), "q", named("all")); err == nil {
			t.Fatal("all must not run Baizhi")
		}
		if len(d.requests) != 0 {
			t.Fatal(d.requests)
		}
		r, err := restSearch(bg(), "q", named("baizhi"))
		noErr(t, err)
		list, err := restSearch(bg(), "q", ProviderSelection{List: []string{"baizhi"}})
		noErr(t, err)
		if r.Provider != "baizhi" || list.Provider != "all" || ProviderLabel("baizhi") != "Baizhi" {
			t.Fatal(r.Provider, list.Provider)
		}
	})
	tw(t, F, "real Pi resource loader, extension binding and agent prompt dispatch Baizhi; includeContent does not guess sources", func(t *testing.T) {
		adapted(t, "the real Pi resource loader and agent session have no Go counterpart; the registered web_search tool is executed")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"webSearch":{"allowedProviders":["baizhi"]}}`)
		t.Setenv("BAIZHI_API_KEY", baizhiKey)
		d := &fixtureDoer{c: baizhiCase("json")}
		t.Cleanup(SetHTTP(d))
		ClearResults()
		t.Cleanup(ClearResults)
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		out, err := mustTool(t, r, "web_search").Execute(bg(), map[string]any{"query": "host 中文", "provider": "baizhi", "numResults": 2.0, "recencyFilter": "year",
			"domainFilter": []any{"example.com"}, "includeContent": true, "workflow": "none"}, nil)
		noErr(t, err)
		if out.IsError || !strings.Contains(out.Text(), "opaque search result") {
			t.Fatal(out.IsError, out.Text())
		}
		calls := 0
		for _, req := range d.requests {
			m, ok := req.Body.(map[string]any)
			if !ok || m["method"] != "tools/call" {
				continue
			}
			calls++
			args := m["params"].(map[string]any)["arguments"].(map[string]any)
			f := args["filter"].(map[string]any)
			if args["query"] != "host 中文" || args["count"] != 2.0 || args["need_summary"] != false || args["time_range"] != "year" || fmt.Sprint(f["domains"]) != "[example.com]" || len(f) != 1 {
				t.Fatal(args)
			}
		}
		if calls != 1 || d.requests[len(d.requests)-1].Method != "DELETE" || strings.Contains(out.Text(), baizhiKey) {
			t.Fatal(calls, d.requests[len(d.requests)-1].Method)
		}
	})
	tw(t, F, "routing keeps safe HTTP/network classifications and never falls back after caller cancellation", func(t *testing.T) {
		for _, failure := range []string{"http401", "http429", "http503", "network", "invalid-json", "abort"} {
			t.Run(failure, func(t *testing.T) {
				_, agentDir := isolate(t)
				writeConfig(t, agentDir, `{"searchRouting":{"providers":["baizhi","brave"],"fallbackOn":["network","transient","quota","invalid-response"]}}`)
				t.Setenv("BAIZHI_API_KEY", baizhiKey)
				t.Setenv("BRAVE_API_KEY", "synthetic-brave")
				ctx, cancel := context.WithCancel(bg())
				defer cancel()
				d := &fixtureDoer{c: baizhiCase(failure), cancel: cancel, brave: `{"web":{"results":[{"title":"Fallback","url":"https://example.com","description":"synthetic"}]}}`}
				t.Cleanup(SetHTTP(d))
				r, err := Search(ctx, "q", FullSearchOptions{Provider: Auto})
				if err != nil && strings.Contains(err.Error(), baizhiKey) {
					t.Fatal("key leaked: ", err)
				}
				brave := false
				for _, q := range d.requests {
					brave = brave || strings.Contains(q.URL, "api.search.brave.com")
				}
				switch failure {
				case "http401", "abort":
					kind := map[string]string{"http401": "auth", "abort": "aborted"}[failure]
					var spe *SearchProviderError
					if !errors.As(err, &spe) || spe.Kind != kind || brave {
						t.Fatal(err, brave)
					}
				default:
					noErr(t, err)
					if r.Provider != "brave" {
						t.Fatal(r.Provider)
					}
				}
			})
		}
	})
}

func jinaRouting(t *testing.T, fallbackOn string) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, fmt.Sprintf(`{"searchRouting":{"providers":["jina","tavily"],"fallbackOn":[%q]}}`, fallbackOn))
	t.Setenv("JINA_API_KEY", "synthetic-jina-test-key")
	t.Setenv("TAVILY_API_KEY", "synthetic-tavily-test-key")
}

const tavilyFallbackBody = `{"answer":"Tavily fallback","results":[]}`

func TestUpstream_jina_search(t *testing.T) {
	const F = "jina-search"
	tw(t, F, "Jina Search availability reads environment and config credentials", func(t *testing.T) {
		isolate(t)
		if IsJinaSearchAvailable() {
			t.Fatal("empty home is not available")
		}
		t.Setenv("JINA_API_KEY", "synthetic-jina-env-key")
		if !IsJinaSearchAvailable() {
			t.Fatal("env credential")
		}
		unsetenv(t, "JINA_API_KEY")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"jinaApiKey":"synthetic-jina-config-key"}`)
		if !IsJinaSearchAvailable() {
			t.Fatal("config credential")
		}
	})
	tw(t, F, "Jina Search resolves command credentials only when a request starts", func(t *testing.T) {
		command, marker := commandMarker(t, "synthetic-jina-command-key")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, fmt.Sprintf(`{"jinaApiKey":%q}`, command))
		n := useNet(t, func(netCall) netReply { return reply(200, `{"code":200,"status":20000,"data":[]}`) })
		availableBefore, ranBefore := IsJinaSearchAvailable(), fileExists(marker)
		_, err := SearchWithJina(bg(), "credential timing", SearchOptions{})
		noErr(t, err)
		if !availableBefore || ranBefore || !fileExists(marker) || n.calls[0].Header.Get("Authorization") != "Bearer synthetic-jina-command-key" {
			t.Fatal(availableBefore, ranBefore, fileExists(marker), n.calls[0].Header)
		}
	})
	tw(t, F, "explicit Jina search maps filters, full content, unique results, and truthful headers", func(t *testing.T) {
		fixtureCases(t, "jina-filters-content", "jina-query-encoding")
		isolate(t)
		t.Setenv("JINA_API_KEY", "synthetic-jina-test-key")
		useNet(t, func(netCall) netReply {
			return reply(200, `{"code":200,"status":20000,"data":[{"title":"Jina Docs","url":"https://docs.jina.ai/search","description":"  Search   docs. ","content":"# Full Jina docs"}]}`)
		})
		five := 2.0
		r, err := Search(bg(), "jina search docs", FullSearchOptions{Provider: named("jina"), SearchOptions: SearchOptions{IncludeContent: true, NumResults: &five}})
		noErr(t, err)
		if r.Provider != "jina" || len(r.InlineContent) != 1 || !strings.Contains(r.Answer, "Search docs.") || !strings.Contains(r.Answer, "https://docs.jina.ai/search") {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, "Jina Search skips page crawling without includeContent and accepts the direct array response", func(t *testing.T) {
		fixtureCases(t, "jina-array-response")
	})
	tw(t, F, "Jina Search errors redact credentials and retain an HTTP-classifiable status", func(t *testing.T) {
		fixtureCases(t, "jina-401-redacted", "jina-network-redacted")
		_, _, err := runFixture(t, loadFixtures(t)["jina-401-redacted"])
		wantErr(t, err, `Jina Search API error 401`)
		wantErr(t, err, `(?i)\[redacted\]`)
		if strings.Contains(err.Error(), "synthetic-jina-secret") || ClassifyProviderError("jina", err).Kind != "auth" {
			t.Fatal(err)
		}
	})
	tw(t, F, "Jina Search invalid JSON errors do not include response body credentials", func(t *testing.T) {
		fixtureCases(t, "jina-invalid-json")
		_, _, err := runFixture(t, loadFixtures(t)["jina-invalid-json"])
		if err == nil || err.Error() != "Jina Search API returned invalid JSON" {
			t.Fatal(err)
		}
	})
	tw(t, F, "configured routing treats Jina's own timeout as transient rather than caller cancellation", func(t *testing.T) {
		fixtureCases(t, "jina-timeout", "jina-caller-abort")
		jinaRouting(t, "transient")
		n := routedNet(t, map[string]func(netCall) netReply{
			"https://s.jina.ai/":            fixed(netReply{Err: context.DeadlineExceeded}),
			"https://api.tavily.com/search": fixed(reply(200, tavilyFallbackBody)),
		})
		r, err := restSearch(bg(), "timeout route", Auto)
		noErr(t, err)
		if r.Provider != "tavily" || len(n.urls()) != 2 {
			t.Fatal(r.Provider, n.urls())
		}
	})
	tw(t, F, "configured routing treats a Jina response-body timeout as transient", func(t *testing.T) {
		fixtureCases(t, "jina-body-timeout")
		jinaRouting(t, "transient")
		var urls []string
		t.Cleanup(SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) {
			urls = append(urls, r.URL.String())
			if strings.HasPrefix(r.URL.String(), "https://s.jina.ai/") {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: brokenBody{context.DeadlineExceeded}, Request: r}, nil
			}
			if r.URL.String() == "https://api.tavily.com/search" {
				return textResponse(r, 200, `{"answer":"Tavily body-timeout fallback","results":[]}`), nil
			}
			return nil, errors.New("Unexpected fetch " + r.URL.String())
		})))
		r, err := restSearch(bg(), "body timeout route", Auto)
		noErr(t, err)
		if r.Provider != "tavily" || len(urls) != 2 {
			t.Fatal(r.Provider, urls)
		}
	})
	tw(t, F, "Jina envelope failures are recorded as activity errors", func(t *testing.T) {
		adapted(t, "the activity monitor is the widget slice's; the envelope failure and its error text run")
		fixtureCases(t, "jina-envelope-429", "jina-envelope-no-data", "jina-scalar-response")
		_, _, err := runFixture(t, loadFixtures(t)["jina-envelope-429"])
		wantErr(t, err, `Jina Search API error 429`)
	})
	tw(t, F, "configured routing preserves Jina envelope status codes", func(t *testing.T) {
		jinaRouting(t, "quota")
		n := routedNet(t, map[string]func(netCall) netReply{
			"https://s.jina.ai/":            fixed(reply(200, `{"code":429,"status":42901,"data":[]}`)),
			"https://api.tavily.com/search": fixed(reply(200, `{"answer":"Tavily quota fallback","results":[]}`)),
		})
		r, err := restSearch(bg(), "quota route", Auto)
		noErr(t, err)
		if r.Provider != "tavily" || len(n.urls()) != 2 {
			t.Fatal(r.Provider, n.urls())
		}
	})
	tw(t, F, "configured searchRouting can select Jina Search", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"searchRouting":{"providers":["jina"],"fallbackOn":["network"]}}`)
		t.Setenv("JINA_API_KEY", "synthetic-jina-test-key")
		useNet(t, func(netCall) netReply {
			return reply(200, `{"code":200,"status":20000,"data":[{"title":"Routed","description":"Jina route","url":"https://example.com/routed"}]}`)
		})
		r, err := restSearch(bg(), "route", Auto)
		noErr(t, err)
		if r.Provider != "jina" || r.Results[0].Title != "Routed" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, F, `provider "all" includes configured Jina Search alongside zero-config Exa`, func(t *testing.T) {
		isolate(t)
		t.Setenv("JINA_API_KEY", "synthetic-jina-test-key")
		n := routedNet(t, map[string]func(netCall) netReply{
			"https://mcp.exa.ai/mcp": fixed(exaMCPText("Title: Exa result\nURL: https://example.com/exa\nText: Exa answer\n---")),
			"https://s.jina.ai/":     fixed(reply(200, `{"code":200,"status":20000,"data":[{"title":"Jina result","url":"https://example.com/jina","description":"Jina answer"}]}`)),
		})
		r, err := restSearch(bg(), "combined", named("all"))
		noErr(t, err)
		if callsWithPrefix(n, "https://s.jina.ai/") == 0 || !eqStrings(providerNames(r.ProviderResponses), []string{"exa", "jina"}) ||
			!eqStrings(urlsOf(r.Results), []string{"https://example.com/exa", "https://example.com/jina"}) || !strings.Contains(r.Answer, "## Jina") {
			t.Fatalf("%v %+v", n.urls(), r)
		}
	})
	tw(t, F, "README documents Jina Search credentials and routing", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
		if os.IsNotExist(err) {
			t.Skip("the Package README is not part of a copied extension tree (mutation runs)")
		}
		noErr(t, err)
		for _, want := range []string{"JINA_API_KEY", "jinaApiKey", "`jina`", "s.jina.ai"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("README lacks %q", want)
			}
		}
	})
}

func searxngConfig(extra string) string {
	return `{"searxngBaseUrl":"http://127.0.0.1:8443/",` + extra + `"ssrf":{"allowRanges":["127.0.0.1"]}}`
}

func TestUpstream_search_providers_searxng(t *testing.T) {
	const f = "search-providers"
	tw(t, f, "SearXNG search is SSRF-guarded and preferred first when configured", func(t *testing.T) {
		fixtureCases(t, "searxng-direct", "searxng-error-status", "searxng-invalid-json", "searxng-missing-base", "searxng-base-with-credentials", "searxng-env-base")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, searxngConfig(""))
		n := useNet(t, func(netCall) netReply {
			return reply(200, `{"answers":["SearXNG answer"],"results":[{"title":"Allowed","url":"https://docs.example.com/a","content":"allowed"},{"title":"Blocked","url":"https://private.example.net/b","content":"blocked"}]}`)
		})
		two := 2.0
		direct, err := SearchWithSearXNG(bg(), "self hosted", SearchOptions{DomainFilter: []string{"example.com", "-private.example.net"}, NumResults: &two})
		noErr(t, err)
		auto, err := restSearch(bg(), "local first", Auto)
		noErr(t, err)
		if len(n.calls) != 2 || n.calls[0].URL != "http://127.0.0.1:8443/search?q=self+hosted+site%3Aexample.com+-site%3Aprivate.example.net&format=json" ||
			len(direct.Results) != 1 || direct.Results[0].URL != "https://docs.example.com/a" || auto.Provider != "searxng" {
			t.Fatalf("%v %+v %s", n.urls(), direct.Results, auto.Provider)
		}
	})
	tw(t, f, "SearXNG search merges configured custom headers", func(t *testing.T) {
		fixtureCases(t, "searxng-headers", "searxng-accept-override")
	})
	tw(t, f, "SearXNG search strips configured headers on cross-origin redirects", func(t *testing.T) {
		fixtureCases(t, "searxng-redirect-strips-headers")
	})
	tw(t, f, "SearXNG redirect validation rejects an unapproved private target", func(t *testing.T) {
		fixtureCases(t, "searxng-redirect-private")
		_, _, err := runFixture(t, loadFixtures(t)["searxng-redirect-private"])
		wantErr(t, err, `Blocked internal address`)
	})
	tw(t, f, "malformed SearXNG SSRF config fails before hosted auto fallback", func(t *testing.T) {
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"searxngBaseUrl":"https://search.example.com","ssrf":{"allowRanges":["127.0.0.0/33"]}}`)
		t.Setenv("TAVILY_API_KEY", "tvly-hosted-fallback-must-not-run")
		n := useNet(t, func(netCall) netReply {
			return reply(200, `{"results":[{"title":"Hosted fallback","url":"https://example.com","content":"should not be used"}]}`)
		})
		_, err := restSearch(bg(), "config", Auto)
		wantErr(t, err, `Invalid CIDR notation in ssrf\.allowRanges`)
		if len(n.urls()) != 0 {
			t.Fatal(n.urls())
		}
	})
}

func TestUpstream_new_search_providers_rest(t *testing.T) {
	const F = "new-search-providers"
	tw(t, F, "SerpBase is explicit-only and maps Google organic results", func(t *testing.T) {
		fixtureCases(t, "serpbase-map", "serpbase-default-count-and-fields", "serpbase-error-envelope", "serpbase-error-envelope-no-status", "serpbase-bad-envelope", "serpbase-429", "serpbase-missing-key")
		_, agentDir := isolate(t)
		writeConfig(t, agentDir, `{"serpbaseApiKey":"serpbase-test-key"}`)
		n := routedNet(t, map[string]func(netCall) netReply{"https://api.serpbase.dev/google/search?": fixed(reply(200, `{"organic_results":[{"title":"SerpBase result","link":"https://example.com/result","snippet":"Google snippet"}]}`))})
		four := 4.0
		_, err := SearchWithSerpBase(bg(), "google query", SearchOptions{NumResults: &four, RecencyFilter: "week", DomainFilter: []string{"example.com"}})
		noErr(t, err)
		_, autoErr := restSearch(bg(), "auto must not spend", Auto)
		if callsWithPrefix(n, "https://api.serpbase.dev/google/search?") != 1 || autoErr == nil {
			t.Fatal(n.urls(), autoErr)
		}
		noMatchRE(t, `SerpBase`, autoErr.Error())
	})
	tw(t, F, "Bocha search filters domains and closes activity on API errors", func(t *testing.T) {
		adapted(t, "the activity monitor is the widget slice's; the filtering, the API error and its redaction run")
		fixtureCases(t, "bocha-filters", "bocha-api-error-redacted", "bocha-string-code-and-fields", "bocha-code-without-msg", "bocha-missing-array", "bocha-no-code", "bocha-401")
		_, _, err := runFixture(t, loadFixtures(t)["bocha-api-error-redacted"])
		wantErr(t, err, `code 500`)
		if strings.Contains(err.Error(), "bocha-test-key") {
			t.Fatal(err)
		}
	})
}
