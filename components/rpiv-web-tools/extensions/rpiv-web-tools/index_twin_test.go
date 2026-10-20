package rpiv_web_tools

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// Twins of the remaining cases of the original's index.test.ts that need no host: the tools, the providers
// and the SearXNG helpers, run against the tools' functions with a fake network. The /web-tools cases are in
// index_command_twin_test.go (they need a host). Where a title is used by two describe blocks upstream
// (SearXNG and Ollama), one twin runs both as subtests.

func parseURLString(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func bodyJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return m
}

func resultRows(t *testing.T, details any) []any {
	t.Helper()
	rows, _ := detailsMap(t, details)["results"].([]any)
	return rows
}

func matches(t *testing.T, pattern, s string) bool {
	t.Helper()
	ok, err := regexp.MatchString(pattern, s)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func htmlPage(body string) func(netReq) netResp {
	return func(netReq) netResp {
		return netResp{Header: map[string]string{"Content-Type": "text/html"}, Body: body}
	}
}

// emptyRow is the shape of a result row whose vendor fields are all missing.
var emptyRow = obj{"title": "", "url": "", "snippet": ""}

func TestIndexRegistrationSchema(t *testing.T) {
	const f = "index"
	tw(t, f, "web_search schema declares min:1, max:10, default:5", func(t *testing.T) {
		props := searchSchema()["properties"].(map[string]any)
		eq(t, detailsMap(t, props["max_results"])["minimum"], float64(1))
		eq(t, detailsMap(t, props["max_results"])["maximum"], float64(10))
		eq(t, detailsMap(t, props["max_results"])["default"], float64(5))
	})
	tw(t, f, "schema declares the provider enum with all known names", func(t *testing.T) {
		props := searchSchema()["properties"].(map[string]any)
		var literals []string
		for _, e := range detailsMap(t, props["provider"])["anyOf"].([]any) {
			literals = append(literals, e.(map[string]any)["const"].(string))
		}
		eq(t, literals, []string{"brave", "tavily", "serper", "exa", "youcom", "jina", "firecrawl", "perplexity", "searxng", "ollama"})
	})
}

func TestIndexFetchProviders(t *testing.T) {
	const f = "index"
	const page = "<html><head><title>My Page</title></head><body><p>Hello</p></body></html>"
	tw(t, f, "fetch throws when no key configured for ${provider}", func(t *testing.T) {
		for _, name := range []string{"tavily", "exa", "jina", "firecrawl", "youcom"} {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name})
				useNet(t, func(netReq) netResp { t.Fatal("no request without a key"); return netResp{} })
				_, _, err := fetchURL(t, "https://example.com", false)
				eq(t, errText(err), metaOf(name).EnvVar+" is not set. Run /web-tools to configure, or export the env var.")
			})
		}
	})
	tw(t, f, "wraps non-2xx as generic HTTP error from fetchViaGenericHtml", func(t *testing.T) {
		for _, name := range []string{"brave", "serper", "searxng", "perplexity"} {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				t.Setenv(metaOf(name).EnvVar, "k")
				writeConfigFile(t, obj{"provider": name})
				useNet(t, func(netReq) netResp { return netResp{Status: 429, Body: "rate limit"} })
				_, _, err := fetchURL(t, "https://example.com", false)
				contains(t, errText(err), "HTTP 429")
			})
		}
	})
	tw(t, f, "brave fetch strips HTML and extracts title", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, htmlPage(page))
		text, _, err := fetchURL(t, "https://example.com", false)
		eq(t, err, nil)
		contains(t, text, "My Page")
		contains(t, text, "Hello")
	})
	tw(t, f, "brave fetch returns raw HTML when raw=true", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, htmlPage("<p>raw</p>"))
		text, _, err := fetchURL(t, "https://x.com", true)
		eq(t, err, nil)
		contains(t, text, "<p>raw</p>")
	})
	tw(t, f, "tavily fetch uses /extract endpoint", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(tavilyKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "tavily"})
		n := useNet(t, func(netReq) netResp {
			return jsonResp(obj{"results": arr{obj{"url": "https://x.com", "raw_content": "extracted text"}}})
		})
		text, _, err := fetchURL(t, "https://x.com", false)
		eq(t, err, nil)
		contains(t, text, "extracted text")
		contains(t, n.Requests[0].URL, "api.tavily.com/extract")
	})
	tw(t, f, "exa fetch uses /contents endpoint", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(exaKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "exa"})
		n := useNet(t, func(netReq) netResp {
			return jsonResp(obj{"results": arr{obj{"title": "Page", "url": "https://x.com", "text": "extracted content"}}})
		})
		text, _, err := fetchURL(t, "https://x.com", false)
		eq(t, err, nil)
		contains(t, text, "extracted content")
		contains(t, text, "Page")
		contains(t, n.Requests[0].URL, "api.exa.ai/contents")
	})
	tw(t, f, "jina fetch uses r.jina.ai reader", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(jinaKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "jina"})
		n := useNet(t, func(netReq) netResp { return netResp{Body: "extracted markdown content"} })
		text, _, err := fetchURL(t, "https://x.com", false)
		eq(t, err, nil)
		contains(t, text, "extracted markdown content")
		contains(t, n.Requests[0].URL, "r.jina.ai")
	})
	tw(t, f, "firecrawl fetch uses /v1/scrape endpoint", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(firecrawlKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "firecrawl"})
		n := useNet(t, func(netReq) netResp {
			return jsonResp(obj{"success": true, "data": obj{"markdown": "# Title\nPage content", "metadata": obj{"title": "Scraped Page"}}})
		})
		text, _, err := fetchURL(t, "https://x.com", false)
		eq(t, err, nil)
		contains(t, text, "Page content")
		contains(t, text, "Scraped Page")
		contains(t, n.Requests[0].URL, "api.firecrawl.dev/v1/scrape")
	})
	tw(t, f, "extraction providers (jina) ignore raw and never strip vendor body", func(t *testing.T) {
		// Jina returns what its reader gave; raw=true must not start the HTML-to-text pipeline that Brave and
		// Serper run, so the literal tag survives.
		clearEnv(t)
		t.Setenv(jinaKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "jina"})
		useNet(t, func(netReq) netResp { return netResp{Body: "# heading\n<p>vendor markdown</p>"} })
		text, _, err := fetchURL(t, "https://x.com", true)
		eq(t, err, nil)
		contains(t, text, "<p>vendor markdown</p>")
	})
	tw(t, f, "parses Number(contentLength) when the header is present", func(t *testing.T) {
		for _, name := range []string{"brave", "serper"} {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				t.Setenv(metaOf(name).EnvVar, "k")
				writeConfigFile(t, obj{"provider": name})
				useNet(t, func(netReq) netResp {
					return netResp{Header: map[string]string{"Content-Type": "text/plain", "Content-Length": "10"}, Body: "plain body"}
				})
				_, details, err := fetchURL(t, "https://example.com", true)
				eq(t, err, nil)
				m := detailsMap(t, details)
				eq(t, m["contentType"], "text/plain")
				eq(t, m["contentLength"], float64(10))
			})
		}
	})
}

func TestIndexSearxng(t *testing.T) {
	const f = "index"
	use := func(t *testing.T, cfg obj) *fakeNet {
		t.Helper()
		clearEnv(t)
		writeConfigFile(t, cfg)
		return useNet(t, func(netReq) netResp { return jsonResp(searchBodies["searxng"]) })
	}
	searxng := obj{"provider": "searxng"}
	one := func(t *testing.T, n *fakeNet) netReq {
		t.Helper()
		eq(t, len(n.Requests), 1)
		return n.Requests[0]
	}
	tw(t, f, "uses env URL (wins over config and default)", func(t *testing.T) {
		t.Run("searxng", func(t *testing.T) {
			clearEnv(t)
			t.Setenv(searxngURLEnv, "http://env-host:9000")
			n := use(t, obj{"provider": "searxng", "baseUrls": obj{"searxng": "http://config-host:7000"}})
			t.Setenv(searxngURLEnv, "http://env-host:9000")
			_, _, err := search(t, "hello", nil, nil)
			eq(t, err, nil)
			u := parseURLString(t, one(t, n).URL)
			eq(t, u.Scheme+"://"+u.Host, "http://env-host:9000")
			eq(t, u.Path, "/search")
			eq(t, u.Query().Get("q"), "hello")
			eq(t, u.Query().Get("format"), "json")
			eq(t, u.Query().Get("safesearch"), "0")
			eq(t, u.Query().Has("count"), false)
		})
		t.Run("ollama", func(t *testing.T) {
			n := use(t, obj{"provider": "ollama", "baseUrls": obj{"ollama": "http://config-host:7000"}})
			t.Setenv(ollamaHostEnv, "http://env-host:9000")
			_, _, err := search(t, "hello", nil, nil)
			eq(t, err, nil)
			r := one(t, n)
			u := parseURLString(t, r.URL)
			eq(t, u.Scheme+"://"+u.Host, "http://env-host:9000")
			eq(t, u.Path, "/api/web_search")
			body := bodyJSON(t, r.Body)
			eq(t, body["query"], "hello")
			_, has := body["max_results"]
			eq(t, has, true)
		})
	})
	tw(t, f, "falls back to config URL when env is unset", func(t *testing.T) {
		for _, c := range []struct{ name, baseKey string }{{"searxng", "searxng"}, {"ollama", "ollama"}} {
			t.Run(c.name, func(t *testing.T) {
				n := use(t, obj{"provider": c.name, "baseUrls": obj{c.baseKey: "http://config-host:7000"}})
				_, _, err := search(t, "x", nil, nil)
				eq(t, err, nil)
				eq(t, parseURLString(t, one(t, n).URL).Host, "config-host:7000")
			})
		}
	})
	tw(t, f, "falls back to default URL (http://localhost:8080) when neither env nor config is set", func(t *testing.T) {
		n := use(t, searxng)
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, parseURLString(t, one(t, n).URL).Host, "localhost:8080")
	})
	tw(t, f, "trailing slash on baseUrl does not produce a double-slash", func(t *testing.T) {
		t.Run("searxng", func(t *testing.T) {
			n := use(t, searxng)
			t.Setenv(searxngURLEnv, "http://host:8080/")
			_, _, err := search(t, "x", nil, nil)
			eq(t, err, nil)
			r := one(t, n)
			eq(t, strings.Contains(r.URL, "//search"), false)
			eq(t, parseURLString(t, r.URL).Path, "/search")
		})
		t.Run("ollama", func(t *testing.T) {
			n := use(t, obj{"provider": "ollama"})
			t.Setenv(ollamaHostEnv, "http://host:11434/")
			_, _, err := search(t, "x", nil, nil)
			eq(t, err, nil)
			eq(t, strings.Contains(one(t, n).URL, "//api"), false)
		})
	})
	tw(t, f, "multiple trailing slashes on baseUrl are all stripped", func(t *testing.T) {
		n := use(t, searxng)
		t.Setenv(searxngURLEnv, "http://host:8080///")
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		r := one(t, n)
		eq(t, strings.Contains(r.URL, "//search"), false)
		eq(t, parseURLString(t, r.URL).Path, "/search")
	})
	tw(t, f, "sends Bearer Authorization only when an API key is configured", func(t *testing.T) {
		n := use(t, searxng)
		t.Setenv(searxngKeyEnv, "env-bearer")
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, one(t, n).Header.Get("Authorization"), "Bearer env-bearer")
	})
	tw(t, f, "omits Authorization when no API key is configured", func(t *testing.T) {
		for _, name := range []string{"searxng", "ollama"} {
			t.Run(name, func(t *testing.T) {
				n := use(t, obj{"provider": name})
				_, _, err := search(t, "x", nil, nil)
				eq(t, err, nil)
				_, has := one(t, n).Header["Authorization"]
				eq(t, has, false)
			})
		}
	})
	tw(t, f, "falls back to config apiKeys.searxng when env is unset", func(t *testing.T) {
		n := use(t, obj{"provider": "searxng", "apiKeys": obj{"searxng": "config-bearer"}})
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, one(t, n).Header.Get("Authorization"), "Bearer config-bearer")
	})
	tw(t, f, "slices results to max_results", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, searxng)
		var rows arr
		for _, i := range []string{"0", "1", "2", "3", "4", "5", "6", "7"} {
			rows = append(rows, obj{"title": "T" + i, "url": "https://r/" + i, "content": "snip " + i})
		}
		useNet(t, func(netReq) netResp { return jsonResp(obj{"results": rows}) })
		_, details, err := search(t, "x", new(float64(3)), nil)
		eq(t, err, nil)
		eq(t, len(resultRows(t, details)), 3)
	})
	tw(t, f, "returns no-results envelope on empty results array", func(t *testing.T) {
		for _, name := range []string{"searxng", "ollama"} {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name})
				useNet(t, func(netReq) netResp { return jsonResp(obj{"results": arr{}}) })
				text, _, err := search(t, "x", nil, nil)
				eq(t, err, nil)
				contains(t, text, "No results found")
			})
		}
	})
	tw(t, f, "wraps non-2xx as 'SearXNG Search API error (status)'", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, searxng)
		useNet(t, func(netReq) netResp { return netResp{Status: 500, Body: "oops"} })
		_, _, err := search(t, "x", nil, nil)
		contains(t, errText(err), "SearXNG Search API error (500)")
	})
	tw(t, f, "403 attaches the 'JSON output may be disabled' hint", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, searxng)
		useNet(t, func(netReq) netResp { return netResp{Status: 403, Body: "forbidden"} })
		_, _, err := search(t, "x", nil, nil)
		eq(t, matches(t, `JSON output disabled`, errText(err)), true)
	})
	tw(t, f, "401 attaches the 'reverse-proxy rejected the Bearer token' hint", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "searxng", "apiKeys": obj{"searxng": "bad-bearer"}})
		useNet(t, func(netReq) netResp { return netResp{Status: 401, Body: "unauthorized"} })
		_, _, err := search(t, "x", nil, nil)
		eq(t, matches(t, `rejected the Bearer token.*SEARXNG_API_KEY`, errText(err)), true)
	})
	tw(t, f, "normalizes missing fields on result rows to empty strings", func(t *testing.T) {
		for _, name := range []string{"searxng", "ollama"} {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name})
				useNet(t, func(netReq) netResp { return jsonResp(obj{"results": arr{obj{}}}) })
				_, details, err := search(t, "x", nil, nil)
				eq(t, err, nil)
				eq(t, resultRows(t, details), arr{emptyRow})
			})
		}
	})
}

func TestIndexSearxngProvider(t *testing.T) {
	const f = "index"
	tw(t, f, "declares envVar as SEARXNG_API_KEY (optional Bearer key)", func(t *testing.T) {
		eq(t, metaOf("searxng").EnvVar, "SEARXNG_API_KEY")
	})
	tw(t, f, "declares baseUrlEnvVar as SEARXNG_URL (the URL that actually activates it)", func(t *testing.T) {
		eq(t, metaOf("searxng").BaseURLEnvVar, "SEARXNG_URL")
	})
	tw(t, f, "accepts http baseUrl", func(t *testing.T) {
		_, err := newSearxng("", "http://localhost:8080")
		eq(t, err, nil)
	})
	tw(t, f, "accepts https baseUrl", func(t *testing.T) {
		_, err := newSearxng("", "https://searx.example/")
		eq(t, err, nil)
	})
	tw(t, f, "accepts an empty baseUrl (deferred-config state — search() then throws)", func(t *testing.T) {
		_, err := newSearxng("", "")
		eq(t, err, nil)
	})
	tw(t, f, "rejects file:// scheme", func(t *testing.T) {
		_, err := newSearxng("", "file:///etc/passwd")
		eq(t, matches(t, `must use http`, errText(err)), true)
	})
	tw(t, f, "rejects javascript: scheme", func(t *testing.T) {
		_, err := newSearxng("", "javascript:alert(1)")
		eq(t, matches(t, `must use http`, errText(err)), true)
	})
	tw(t, f, "rejects an unparseable URL", func(t *testing.T) {
		_, err := newSearxng("", "not a url")
		eq(t, matches(t, `is not a valid URL`, errText(err)), true)
	})
	tw(t, f, "throws 'SEARXNG_URL is not set' when constructed with an empty baseUrl", func(t *testing.T) {
		p, err := newSearxng("", "")
		eq(t, err, nil)
		_, err = p.search(t.Context(), "q", 5)
		eq(t, matches(t, `SEARXNG_URL is not set`, errText(err)), true)
	})
}

// scriptedUI answers configureSearxng's prompts from a list; a nil answer cancels the dialog.
type scriptedUI struct {
	answers []*string
	calls   [][2]string
}

func (u *scriptedUI) input(label, placeholder string) (string, bool, error) {
	u.calls = append(u.calls, [2]string{label, placeholder})
	a := u.answers[0]
	u.answers = u.answers[1:]
	if a == nil {
		return "", false, nil
	}
	return *a, true, nil
}

func TestIndexConfigureSearxng(t *testing.T) {
	const f = "index"
	tw(t, f, "returns null when the user cancels at the URL prompt", func(t *testing.T) {
		got, err := configureSearxng(&scriptedUI{answers: []*string{nil}}, providerConfigCurrent{})
		eq(t, err, nil)
		eq(t, got == nil, true)
	})
	tw(t, f, "returns null when the user cancels at the API-key prompt", func(t *testing.T) {
		got, err := configureSearxng(&scriptedUI{answers: []*string{new("http://h:8080"), nil}}, providerConfigCurrent{})
		eq(t, err, nil)
		eq(t, got == nil, true)
	})
	tw(t, f, "uses SEARXNG_DEFAULT_URL and null apiKey when both inputs are empty and no current values exist", func(t *testing.T) {
		got, err := configureSearxng(&scriptedUI{answers: []*string{new(""), new("")}}, providerConfigCurrent{})
		eq(t, err, nil)
		eq(t, got.BaseURL, searxngDefault)
		eq(t, got.APIKey == nil, true)
	})
	tw(t, f, "keeps current values when both inputs are empty", func(t *testing.T) {
		got, err := configureSearxng(&scriptedUI{answers: []*string{new(""), new("")}},
			providerConfigCurrent{BaseURL: "http://kept:8080", APIKey: "kept-key", HasBaseURL: true, HasAPIKey: true})
		eq(t, err, nil)
		eq(t, got.BaseURL, "http://kept:8080")
		eq(t, *got.APIKey, "kept-key")
	})
	tw(t, f, "uses fresh values when both inputs are non-empty", func(t *testing.T) {
		got, err := configureSearxng(&scriptedUI{answers: []*string{new("  http://new:8080  "), new("  new-key  ")}},
			providerConfigCurrent{BaseURL: "http://kept:8080", APIKey: "kept-key", HasBaseURL: true, HasAPIKey: true})
		eq(t, err, nil)
		eq(t, got.BaseURL, "http://new:8080")
		eq(t, *got.APIKey, "new-key")
	})
	tw(t, f, "prompts URL first, then key, with placeholders that reflect current values", func(t *testing.T) {
		ui := &scriptedUI{answers: []*string{new(""), new("")}}
		_, err := configureSearxng(ui, providerConfigCurrent{BaseURL: "http://existing:8080", APIKey: "existing-key", HasBaseURL: true, HasAPIKey: true})
		eq(t, err, nil)
		eq(t, len(ui.calls), 2)
		eq(t, matches(t, `(?i)URL`, ui.calls[0][0]), true)
		contains(t, ui.calls[0][1], "http://existing:8080")
		eq(t, matches(t, `(?i)key`, ui.calls[1][0]), true)
		// The mask hides the middle and shows the first and last four characters.
		contains(t, ui.calls[1][1], "exis...-key")
	})
}

func TestIndexOllama(t *testing.T) {
	const f = "index"
	ollama := func(t *testing.T, handler func(netReq) netResp) *fakeNet {
		t.Helper()
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "ollama"})
		return useNet(t, handler)
	}
	tw(t, f, "falls back to default URL (http://localhost:11434) when neither env nor config is set", func(t *testing.T) {
		n := ollama(t, func(netReq) netResp { return jsonResp(searchBodies["ollama"]) })
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, parseURLString(t, n.Requests[0].URL).Host, "localhost:11434")
	})
	tw(t, f, "sends Bearer Authorization when API key is configured", func(t *testing.T) {
		n := ollama(t, func(netReq) netResp { return jsonResp(searchBodies["ollama"]) })
		t.Setenv(ollamaKeyEnv, "test-key")
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, n.Requests[0].Header.Get("Authorization"), "Bearer test-key")
	})
	tw(t, f, "wraps non-2xx as 'Ollama Search API error (status)'", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return netResp{Status: 500, Body: "oops"} })
		_, _, err := search(t, "x", nil, nil)
		contains(t, errText(err), "Ollama Search API error (500)")
	})
	tw(t, f, "401 attaches the 'ollama signin' hint", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return netResp{Status: 401, Body: "unauthorized"} })
		_, _, err := search(t, "x", nil, nil)
		contains(t, errText(err), "ollama signin")
	})
	tw(t, f, "404 attaches the 'may not support web search' hint", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return netResp{Status: 404, Body: "not found"} })
		_, _, err := search(t, "x", nil, nil)
		contains(t, errText(err), "may not support web search")
	})
	tw(t, f, "ollama fetch uses /api/experimental/web_fetch endpoint", func(t *testing.T) {
		n := ollama(t, func(netReq) netResp {
			return jsonResp(obj{"title": "Test Page", "content": "extracted text", "links": arr{"https://example.com"}})
		})
		text, _, err := fetchURL(t, "https://example.com", false)
		eq(t, err, nil)
		contains(t, text, "extracted text")
		contains(t, text, "Test Page")
		contains(t, n.Requests[0].URL, "/api/experimental/web_fetch")
	})
	tw(t, f, "ollama fetch throws when content is empty", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return jsonResp(obj{"title": "Empty", "content": "", "links": arr{}}) })
		_, _, err := fetchURL(t, "https://example.com", false)
		contains(t, errText(err), "no content returned")
	})
	tw(t, f, "ollama fetch wraps non-2xx as 'Ollama Fetch API error (status)'", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return netResp{Status: 502, Body: "bad"} })
		_, _, err := fetchURL(t, "https://example.com", false)
		contains(t, errText(err), "Ollama Fetch API error (502)")
	})
	tw(t, f, "surfaces connection-refused with actionable hint", func(t *testing.T) {
		ollama(t, func(netReq) netResp { return netResp{Failure: &netError{syscall.ECONNREFUSED}} })
		_, _, err := search(t, "x", nil, nil)
		eq(t, matches(t, `Could not connect to Ollama.*Make sure Ollama is running`, errText(err)), true)
	})
}

func TestIndexYouCom(t *testing.T) {
	const f = "index"
	web := obj{"results": obj{"web": arr{obj{"title": "T", "url": "https://x", "description": "snip", "snippets": arr{"snip"}}}}}
	tw(t, f, "uses env key", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(youcomKeyEnv, "env-key")
		writeConfigFile(t, obj{"provider": "youcom"})
		n := useNet(t, func(netReq) netResp { return jsonResp(web) })
		_, _, err := search(t, "hello", new(float64(3)), nil)
		eq(t, err, nil)
		contains(t, n.Requests[0].URL, "ydc-index.io/v1/search")
		eq(t, n.Requests[0].Header.Get("X-API-Key"), "env-key")
	})
	tw(t, f, "falls back to config key", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "youcom", "apiKeys": obj{"youcom": "config-key"}})
		n := useNet(t, func(netReq) netResp { return jsonResp(web) })
		_, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, n.Requests[0].Header.Get("X-API-Key"), "config-key")
	})
	tw(t, f, "throws when no key configured", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "youcom"})
		_, _, err := search(t, "x", nil, nil)
		eq(t, matches(t, `YOUCOM_API_KEY is not set`, errText(err)), true)
	})
	tw(t, f, "returns no-results envelope on empty results", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(youcomKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "youcom"})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"results": obj{}}) })
		text, _, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		contains(t, text, "No results found")
	})
	tw(t, f, "wraps non-2xx as 'You.com Search API error (status)'", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(youcomKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "youcom"})
		useNet(t, func(netReq) netResp { return netResp{Status: 429, Body: "rate limit"} })
		_, _, err := search(t, "x", nil, nil)
		eq(t, matches(t, `You\.com Search API error \(429\)`, errText(err)), true)
	})
	tw(t, f, "tolerates missing fields in results", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(youcomKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "youcom"})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"results": obj{"web": arr{obj{}}}}) })
		_, details, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, resultRows(t, details), arr{emptyRow})
	})
}

func TestIndexGitHubIntercept(t *testing.T) {
	const f = "index"
	const githubURL = "https://github.com/owner/repo/blob/main/file.ts"
	fetchWith := func(t *testing.T, cfg obj, body, target string) (string, error) {
		t.Helper()
		clearEnv(t)
		t.Setenv(braveKeyEnv, "k")
		writeConfigFile(t, cfg)
		useNet(t, htmlPage("<html><body>"+body+"</body></html>"))
		text, _, err := fetchURL(t, target, false)
		return text, err
	}
	tw(t, f, "default OFF: github.com URLs go straight to active provider (no interceptor registered)", func(t *testing.T) {
		// No opt-in and no interceptor: the chain is empty and the URL is fetched like any other.
		text, err := fetchWith(t, obj{"provider": "brave"}, "plain github page", githubURL)
		eq(t, err, nil)
		contains(t, text, "plain github page")
	})
	tw(t, f, "user config wins: { interceptors: { github: false } } overrides consumer opt-in", func(t *testing.T) {
		// The original's consumer opt-in (registerWebTools(pi, { interceptors: { github: true } })) has no
		// Go counterpart: this extension has no embedding consumer. The user's `false` is what the opt-in loses to.
		text, err := fetchWith(t, obj{"provider": "brave", "interceptors": obj{"github": false}}, "plain github page", githubURL)
		eq(t, err, nil)
		contains(t, text, "plain github page")
	})
	tw(t, f, "falls back to provider.fetch when parseGitHubUrl returns null (non-code github URL)", func(t *testing.T) {
		text, err := fetchWith(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, "GitHub Issues page", "https://github.com/owner/repo/issues")
		eq(t, err, nil)
		contains(t, text, "GitHub Issues page")
	})
	tw(t, f, "does not intercept non-GitHub URLs — active provider handles them", func(t *testing.T) {
		text, err := fetchWith(t, obj{"provider": "brave", "interceptors": obj{"github": true}}, "Not GitHub", "https://example.com")
		eq(t, err, nil)
		contains(t, text, "Not GitHub")
	})
	tw(t, f, "SSRF guard fires before github.com hostname check — refuses private/loopback addresses", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"interceptors": obj{"github": true}})
		useNet(t, func(netReq) netResp { t.Fatal("no request to a private address"); return netResp{} })
		_, _, err := fetchURL(t, "http://192.168.1.1/owner/repo/blob/main/file.ts", false)
		eq(t, matches(t, `(?i)private|loopback`, errText(err)), true)
	})
}

func TestIndexProviderOverride(t *testing.T) {
	const f = "index"
	tw(t, f, "routes to the override provider when its key is configured", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "brave-key")
		t.Setenv(tavilyKeyEnv, "tavily-key")
		writeConfigFile(t, obj{"provider": "brave"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["tavily"]) })
		_, details, err := search(t, "hello", nil, new("tavily"))
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "tavily")
		contains(t, n.Requests[0].URL, "api.tavily.com")
		eq(t, bodyJSON(t, n.Requests[0].Body)["api_key"], "tavily-key")
	})
	tw(t, f, "override works with config-file key (no env var)", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "brave", "apiKeys": obj{"brave": "brave-key", "exa": "exa-config-key"}})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["exa"]) })
		_, details, err := search(t, "x", nil, new("exa"))
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "exa")
		eq(t, n.Requests[0].Header.Get("x-api-key"), "exa-config-key")
	})
	tw(t, f, "override resolves baseUrl for self-hosted providers (searxng)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(searxngURLEnv, "http://override-host:9090")
		t.Setenv(braveKeyEnv, "brave-key")
		writeConfigFile(t, obj{"provider": "brave"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["searxng"]) })
		_, details, err := search(t, "x", nil, new("searxng"))
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "searxng")
		eq(t, parseURLString(t, n.Requests[0].URL).Host, "override-host:9090")
	})
	tw(t, f, "override throws when the named provider has no key configured (no silent fallback)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "brave-key")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp { t.Fatal("no request without the override's key"); return netResp{} })
		_, _, err := search(t, "x", nil, new("exa"))
		eq(t, matches(t, `EXA_API_KEY is not set`, errText(err)), true)
	})
	tw(t, f, "override with an unknown provider name throws a clear error", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "brave-key")
		writeConfigFile(t, obj{"provider": "brave"})
		_, _, err := search(t, "x", nil, new("nonexistent"))
		contains(t, errText(err), `Unknown web_search provider: "nonexistent"`)
	})
	tw(t, f, "override omitted falls back to config.provider (default path unchanged)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "brave-key")
		writeConfigFile(t, obj{"provider": "brave"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		_, details, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "brave")
		contains(t, n.Requests[0].URL, "api.search.brave.com")
	})
}

func TestIndexProviderEnvPrecedence(t *testing.T) {
	const f = "index"
	tw(t, f, "WEB_SEARCH_PROVIDER beats config.provider", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
		t.Setenv(tavilyKeyEnv, "tavily-key")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["tavily"]) })
		_, details, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "tavily")
	})
	tw(t, f, "per-call provider override beats WEB_SEARCH_PROVIDER", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
		t.Setenv(tavilyKeyEnv, "tavily-key")
		t.Setenv(exaKeyEnv, "exa-key")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["exa"]) })
		_, details, err := search(t, "x", nil, new("exa"))
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "exa")
	})
	tw(t, f, "valid per-call override succeeds even when WEB_SEARCH_PROVIDER is bogus", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "bogus")
		t.Setenv(exaKeyEnv, "exa-key")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["exa"]) })
		_, details, err := search(t, "x", nil, new("exa"))
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "exa")
	})
	tw(t, f, "whitespace-only WEB_SEARCH_PROVIDER is treated as unset (config wins)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "   ")
		t.Setenv(braveKeyEnv, "brave-key")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		_, details, err := search(t, "x", nil, nil)
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "brave")
	})
	tw(t, f, "unknown WEB_SEARCH_PROVIDER name throws (no silent fallback)", func(t *testing.T) {
		// Upstream runs it for web_search and, in a second describe block under the same title, for web_fetch.
		for _, tool := range []string{"web_search", "web_fetch"} {
			t.Run(tool, func(t *testing.T) {
				clearEnv(t)
				t.Setenv("WEB_SEARCH_PROVIDER", "bogus")
				t.Setenv(braveKeyEnv, "brave-key")
				writeConfigFile(t, obj{"provider": "brave"})
				var err error
				if tool == "web_search" {
					_, _, err = search(t, "x", nil, nil)
				} else {
					_, _, err = fetchURL(t, "https://x.com", false)
				}
				contains(t, errText(err), `Unknown web_search provider: "bogus"`)
			})
		}
	})
	tw(t, f, "WEB_SEARCH_PROVIDER beats config.provider (env-pinned tavily fetch)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
		t.Setenv(tavilyKeyEnv, "k")
		writeConfigFile(t, obj{"provider": "brave"})
		useNet(t, func(netReq) netResp {
			return jsonResp(obj{"results": arr{obj{"url": "https://x.com", "raw_content": "extracted text"}}})
		})
		text, _, err := fetchURL(t, "https://x.com", false)
		eq(t, err, nil)
		contains(t, text, "extracted text")
	})
}
