package rpiv_web_tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// Cases of the original's index.test.ts, run against the tools' functions with a fake network. The original
// mocks global fetch; this port replaces the HTTP transport.

func search(t *testing.T, query string, maxResults *float64, override *string) (string, any, error) {
	t.Helper()
	return searchTool(context.Background(), query, maxResults, override, nil)
}

func fetchURL(t *testing.T, url string, raw bool) (string, any, error) {
	t.Helper()
	return fetchTool(context.Background(), url, raw, nil)
}

func detailsMap(t *testing.T, d any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(d)
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// searchBodies is the raw search answer of each provider carrying two results.
var searchBodies = map[string]any{
	"brave":      obj{"web": obj{"results": arr{obj{"title": "T1", "url": "https://one", "description": "S1"}, obj{"title": "T2", "url": "https://two", "description": "S2"}}}},
	"tavily":     obj{"results": arr{obj{"title": "T1", "url": "https://one", "content": "S1"}, obj{"title": "T2", "url": "https://two", "content": "S2"}}},
	"serper":     obj{"organic": arr{obj{"title": "T1", "link": "https://one", "snippet": "S1"}, obj{"title": "T2", "link": "https://two", "snippet": "S2"}}},
	"exa":        obj{"results": arr{obj{"title": "T1", "url": "https://one", "text": "S1"}, obj{"title": "T2", "url": "https://two", "text": "S2"}}},
	"youcom":     obj{"results": obj{"web": arr{obj{"title": "T1", "url": "https://one", "description": "S1"}, obj{"title": "T2", "url": "https://two", "snippets": arr{"S2"}}}}},
	"jina":       obj{"data": arr{obj{"title": "T1", "url": "https://one", "description": "S1"}, obj{"title": "T2", "url": "https://two", "description": "S2"}}},
	"firecrawl":  obj{"data": arr{obj{"title": "T1", "url": "https://one", "description": "S1"}, obj{"title": "T2", "url": "https://two", "description": "S2"}}},
	"perplexity": obj{"results": arr{obj{"title": "T1", "url": "https://one", "snippet": "S1"}, obj{"title": "T2", "url": "https://two", "snippet": "S2"}}},
	"searxng":    obj{"results": arr{obj{"title": "T1", "url": "https://one", "content": "S1"}, obj{"title": "T2", "url": "https://two", "content": "S2"}}},
	"ollama":     obj{"results": arr{obj{"title": "T1", "url": "https://one", "content": "S1"}, obj{"title": "T2", "url": "https://two", "content": "S2"}}},
}

// requestShape is what each provider must send: method, URL (without a query), the headers that carry the key,
// and the JSON body (nil for a GET).
var requestShape = map[string]struct {
	method, urlPrefix string
	headers           map[string]string
	body              string
}{
	"brave":      {"GET", "https://api.search.brave.com/res/v1/web/search?count=5&q=go+news", map[string]string{"X-Subscription-Token": "KEY", "Accept": "application/json", "Accept-Encoding": "gzip"}, ""},
	"tavily":     {"POST", "https://api.tavily.com/search", map[string]string{"Content-Type": "application/json"}, `{"api_key":"KEY","query":"go news","max_results":5}`},
	"serper":     {"POST", "https://google.serper.dev/search", map[string]string{"X-Api-Key": "KEY", "Content-Type": "application/json"}, `{"q":"go news","num":5}`},
	"exa":        {"POST", "https://api.exa.ai/search", map[string]string{"X-Api-Key": "KEY"}, `{"query":"go news","numResults":5,"contents":{"text":{"maxCharacters":300}}}`},
	"youcom":     {"POST", "https://ydc-index.io/v1/search", map[string]string{"X-Api-Key": "KEY"}, `{"query":"go news","count":5}`},
	"jina":       {"GET", "https://s.jina.ai/go%20news?num=5", map[string]string{"Authorization": "Bearer KEY", "Accept": "application/json"}, ""},
	"firecrawl":  {"POST", "https://api.firecrawl.dev/v1/search", map[string]string{"Authorization": "Bearer KEY"}, `{"query":"go news","limit":5}`},
	"perplexity": {"POST", "https://api.perplexity.ai/search", map[string]string{"Authorization": "Bearer KEY"}, `{"query":"go news","max_results":5}`},
}

func TestSearchProviders(t *testing.T) {
	const f = "index"
	tw(t, f, "uses env key for ${provider}", func(t *testing.T) {
		for _, meta := range providers {
			if meta.BaseURLEnvVar != "" {
				continue
			}
			t.Run(meta.Name, func(t *testing.T) {
				clearEnv(t)
				t.Setenv(meta.EnvVar, "KEY")
				t.Setenv("WEB_SEARCH_PROVIDER", meta.Name)
				useNet(t, func(netReq) netResp { return jsonResp(searchBodies[meta.Name]) })
				text, details, err := search(t, "go news", nil, nil)
				eq(t, err, nil)
				eq(t, strings.Contains(text, "1. **T1**\n   https://one\n   S1"), true)
				eq(t, detailsMap(t, details)["backend"], meta.Name)
			})
		}
	})
	tw(t, f, "falls back to config key for ${provider}", func(t *testing.T) {
		for name, shape := range requestShape {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name, "apiKeys": obj{name: "KEY"}})
				n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies[name]) })
				text, details, err := search(t, "go news", nil, nil)
				eq(t, err, nil)
				eq(t, strings.Contains(text, "2. **T2**\n   https://two\n   S2"), true)
				eq(t, detailsMap(t, details)["backend"], name)
				eq(t, len(n.Requests), 1)
				r := n.Requests[0]
				eq(t, r.Method, shape.method)
				eq(t, r.URL, shape.urlPrefix)
				for h, v := range shape.headers {
					eq(t, r.Header.Get(h), v)
				}
				eq(t, r.Body, shape.body)
			})
		}
	})
	tw(t, f, "throws when no key configured for ${provider}", func(t *testing.T) {
		for _, meta := range providers {
			if meta.BaseURLEnvVar != "" {
				continue
			}
			t.Run(meta.Name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": meta.Name})
				useNet(t, func(netReq) netResp { t.Fatal("no request without a key"); return netResp{} })
				_, _, err := search(t, "q", nil, nil)
				eq(t, errText(err), meta.EnvVar+" is not set. Run /web-tools to configure, or export the env var.")
			})
		}
	})
	tw(t, f, "returns no-results envelope for ${provider}", func(t *testing.T) {
		for name := range requestShape {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name, "apiKeys": obj{name: "KEY"}})
				useNet(t, func(netReq) netResp { return jsonResp(obj{}) })
				text, details, err := search(t, "nothing", nil, nil)
				eq(t, err, nil)
				eq(t, text, `No results found for "nothing".`)
				eq(t, detailsMap(t, details), obj{"query": "nothing", "backend": name, "resultCount": float64(0)})
			})
		}
	})
	tw(t, f, "wraps non-2xx as '${provider} Search API error (status)'", func(t *testing.T) {
		labels := map[string]string{"brave": "Brave", "tavily": "Tavily", "serper": "Serper", "exa": "Exa", "youcom": "You.com", "jina": "Jina", "firecrawl": "Firecrawl", "perplexity": "Perplexity"}
		for name, label := range labels {
			t.Run(name, func(t *testing.T) {
				clearEnv(t)
				writeConfigFile(t, obj{"provider": name, "apiKeys": obj{name: "KEY"}})
				useNet(t, func(netReq) netResp { return netResp{Status: 429, Body: "slow down"} })
				_, _, err := search(t, "q", nil, nil)
				eq(t, errText(err), label+" Search API error (429): slow down")
			})
		}
	})
	tw(t, f, "clamps max_results to [1,10]", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "KEY")
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		for _, c := range []struct{ in, want string }{{"0", "count=1"}, {"99", "count=10"}, {"7", "count=7"}} {
			var v float64
			json.Unmarshal([]byte(c.in), &v)
			search(t, "q", &v, nil)
			eq(t, strings.Contains(n.Requests[len(n.Requests)-1].URL, c.want), true)
		}
		search(t, "q", nil, nil)
		eq(t, strings.Contains(n.Requests[len(n.Requests)-1].URL, "count=5"), true)
	})
	tw(t, f, "defaults to brave when no provider configured", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "KEY")
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		search(t, "q", nil, nil)
		eq(t, strings.HasPrefix(n.Requests[0].URL, "https://api.search.brave.com/"), true)
	})
	tw(t, f, "treats empty-string env key as unset", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "   ")
		writeConfigFile(t, obj{"apiKeys": obj{"brave": "from-config"}})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		search(t, "q", nil, nil)
		eq(t, n.Requests[0].Header.Get("X-Subscription-Token"), "from-config")
	})
	tw(t, f, "treats empty-string legacy brave apiKey as unset", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"apiKey": "  "})
		useNet(t, func(netReq) netResp { t.Fatal("no request"); return netResp{} })
		_, _, err := search(t, "q", nil, nil)
		eq(t, errText(err), braveKeyEnv+" is not set. Run /web-tools to configure, or export the env var.")
	})
	tw(t, f, "uses legacy apiKey fallback for brave", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"apiKey": "legacy-key"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["brave"]) })
		search(t, "q", nil, nil)
		eq(t, n.Requests[0].Header.Get("X-Subscription-Token"), "legacy-key")
	})
	tw(t, f, "brave search tolerates missing fields in organic results", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(braveKeyEnv, "KEY")
		useNet(t, func(netReq) netResp { return jsonResp(obj{"web": obj{"results": arr{obj{}}}}) })
		text, _, _ := search(t, "q", nil, nil)
		eq(t, text, "**Search results for \"q\":**\n\n1. ****")
	})
	tw(t, f, "serper search tolerates missing fields in organic results", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "serper", "apiKeys": obj{"serper": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"organic": arr{obj{}}}) })
		text, details, err := search(t, "q", nil, nil)
		eq(t, err, nil)
		eq(t, strings.HasPrefix(text, "**Search results for \"q\":**"), true)
		eq(t, detailsMap(t, details)["resultCount"], float64(1))
	})
}

func TestFetchTool(t *testing.T) {
	const f = "index"
	html := `<html><head><title>Hello &amp; Welcome</title><style>p{}</style></head><body><script>alert(1)</script><p>One &lt;two&gt;</p><p>Caf&#233;</p></body></html>`
	page := func(t *testing.T) *fakeNet {
		return useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/html; charset=utf-8", "Content-Length": "123"}, Body: html}
		})
	}
	tw(t, f, "throws on invalid URL", func(t *testing.T) {
		clearEnv(t)
		_, _, err := fetchURL(t, "not a url", false)
		eq(t, errText(err), "Invalid URL: not a url")
	})
	tw(t, f, "throws on non-http(s) protocol", func(t *testing.T) {
		clearEnv(t)
		_, _, err := fetchURL(t, "ftp://example.com/x", false)
		eq(t, errText(err), "Unsupported URL protocol: ftp:. Only http and https are supported.")
	})
	tw(t, f, "refuses private/loopback host when active provider is searxng", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "searxng"})
		for _, h := range []string{"http://localhost/x", "http://127.0.0.1/", "http://10.1.2.3/", "http://169.254.169.254/latest", "http://192.168.1.5/", "http://172.20.0.1/", "http://[::1]/", "http://foo.localhost/"} {
			_, _, err := fetchURL(t, h, false)
			eq(t, strings.Contains(errText(err), "Refusing to fetch private/loopback address"), true)
		}
	})
	tw(t, f, "strips HTML and extracts title for text/html", func(t *testing.T) {
		clearEnv(t)
		page(t)
		text, details, err := fetchURL(t, "https://example.com/p", false)
		eq(t, err, nil)
		eq(t, text, "**Fetched:** https://example.com/p\n**Title:** Hello &amp; Welcome\n**Content-Type:** text/html; charset=utf-8\n\nHello & Welcome One <two>\n Café")
		eq(t, detailsMap(t, details)["title"], "Hello &amp; Welcome")
	})
	tw(t, f, "throws on non-2xx with HTTP status in message", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp { return netResp{Status: 404, Body: "no"} })
		_, _, err := fetchURL(t, "https://example.com/missing", false)
		eq(t, errText(err), "HTTP 404 Not Found for https://example.com/missing")
	})
	tw(t, f, "throws on binary content-type", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "image/png"}, Body: "x"}
		})
		_, _, err := fetchURL(t, "https://example.com/a.png", false)
		eq(t, errText(err), "Unsupported content type: image/png. web_fetch supports text pages only.")
	})
	tw(t, f, "returns raw=true untouched", func(t *testing.T) {
		clearEnv(t)
		page(t)
		text, _, _ := fetchURL(t, "https://example.com/p", true)
		eq(t, strings.HasSuffix(text, html), true)
	})
	tw(t, f, "sends UA + Accept headers + redirect:follow", func(t *testing.T) {
		clearEnv(t)
		n := page(t)
		fetchURL(t, "https://example.com/p", false)
		eq(t, n.Requests[0].Header.Get("User-Agent"), "Mozilla/5.0 (compatible; rpiv-pi/1.0)")
		eq(t, n.Requests[0].Header.Get("Accept"), fetchAcceptHeader)
		eq(t, httpClient.CheckRedirect == nil, true) // the default policy follows up to ten redirects
	})
	tw(t, f, "coerces content-length to numeric details.contentLength", func(t *testing.T) {
		clearEnv(t)
		page(t)
		_, details, _ := fetchURL(t, "https://example.com/p", false)
		eq(t, detailsMap(t, details)["contentLength"], float64(123))
	})
	tw(t, f, "returns undefined contentType/contentLength when headers are absent", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp { return netResp{Body: "plain"} })
		text, details, err := fetchURL(t, "https://example.com/p", false)
		eq(t, err, nil)
		eq(t, text, "**Fetched:** https://example.com/p\n\nplain")
		m := detailsMap(t, details)
		_, hasCT := m["contentType"]
		_, hasCL := m["contentLength"]
		eq(t, hasCT || hasCL, false)
	})
	tw(t, f, "falls back to defaults when config file is malformed JSON", func(t *testing.T) {
		clearEnv(t)
		writeRaw(t, "{ nope")
		page(t)
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, err, nil)
	})
	tw(t, f, "decodes numeric HTML entities in text/html bodies", func(t *testing.T) {
		clearEnv(t)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/html"}, Body: "<p>&#65;&#8364;&#x41;</p>"}
		})
		text, _, _ := fetchURL(t, "https://example.com/p", false)
		eq(t, strings.HasSuffix(text, "A€&#x41;"), true)
	})
	tw(t, f, "spills full body to temp file and appends truncation footer when truncated", func(t *testing.T) {
		clearEnv(t)
		long := strings.Repeat("line\n", 2500)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: long}
		})
		text, details, err := fetchURL(t, "https://example.com/big", false)
		eq(t, err, nil)
		m := detailsMap(t, details)
		path, _ := m["fullOutputPath"].(string)
		saved, rerr := os.ReadFile(path)
		eq(t, rerr, nil)
		eq(t, string(saved), long)
		eq(t, strings.Contains(text, "[Content truncated: showing 2000 of 2500 lines (9.8KB of 12.2KB). 500 lines (2.4KB) omitted. Full content saved to: "+path+"]"), true)
		eq(t, m["truncation"].(map[string]any)["truncated"], true)
	})
	for _, c := range []struct{ name, endpoint, label string }{{"tavily", "https://api.tavily.com/extract", "Tavily"}, {"exa", "https://api.exa.ai/contents", "Exa"}, {"youcom", "https://ydc-index.io/v1/contents", "You.com"}, {"firecrawl", "https://api.firecrawl.dev/v1/scrape", "Firecrawl"}, {"jina", "https://r.jina.ai/https://example.com/p", "Jina"}} {
		c := c
		t.Run("fetch via "+c.name, func(t *testing.T) {
			clearEnv(t)
			writeConfigFile(t, obj{"provider": c.name, "apiKeys": obj{c.name: "KEY"}})
			n := useNet(t, func(r netReq) netResp {
				switch c.name {
				case "tavily":
					return jsonResp(obj{"results": arr{obj{"raw_content": "BODY"}}})
				case "exa":
					return jsonResp(obj{"results": arr{obj{"text": "BODY", "title": "TTL"}}})
				case "youcom":
					return jsonResp(arr{obj{"markdown": "BODY", "title": "TTL"}})
				case "firecrawl":
					return jsonResp(obj{"success": true, "data": obj{"markdown": "BODY", "metadata": obj{"title": "TTL"}}})
				}
				return netResp{Body: "BODY"}
			})
			text, _, err := fetchURL(t, "https://example.com/p", false)
			eq(t, err, nil)
			eq(t, strings.HasSuffix(text, "BODY"), true)
			eq(t, n.Requests[0].URL, c.endpoint)
		})
	}
	tw(t, f, "tavily fetch handles failed_results", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "tavily", "apiKeys": obj{"tavily": "K"}})
		useNet(t, func(netReq) netResp {
			return jsonResp(obj{"failed_results": arr{obj{"url": "https://example.com/p", "error": "blocked"}}})
		})
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Tavily Fetch API error: extraction failed for https://example.com/p: blocked")
	})
	tw(t, f, "exa fetch throws when no content returned", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "exa", "apiKeys": obj{"exa": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"results": arr{obj{}}}) })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Exa Fetch API error: no content returned for https://example.com/p")
	})
	tw(t, f, "jina fetch throws when response body is empty", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "jina", "apiKeys": obj{"jina": "K"}})
		useNet(t, func(netReq) netResp { return netResp{Body: "  \n"} })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Jina Fetch API error: no content returned for https://example.com/p")
	})
	tw(t, f, "firecrawl fetch handles success=false", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "firecrawl", "apiKeys": obj{"firecrawl": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"success": false, "error": "quota"}) })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Firecrawl Fetch API error: quota")
	})
	tw(t, f, "firecrawl fetch throws on success=true with empty markdown", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "firecrawl", "apiKeys": obj{"firecrawl": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(obj{"success": true, "data": obj{"markdown": ""}}) })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Firecrawl Fetch API error: no content returned for https://example.com/p")
	})
	tw(t, f, "fetch wraps non-2xx as '${label} Fetch API error (429)'", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "tavily", "apiKeys": obj{"tavily": "K"}})
		useNet(t, func(netReq) netResp { return netResp{Status: 429, Body: "limit"} })
		_, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, errText(err), "Tavily Fetch API error (429): limit")
	})
	tw(t, f, "does not throw on missing key (raw HTTP doesn't authenticate to the target)", func(t *testing.T) {
		clearEnv(t)
		page(t)
		_, _, err := fetchURL(t, "https://example.com/p", false) // brave has no fetch of its own
		eq(t, err, nil)
	})
}

func TestSelfHostedProviders(t *testing.T) {
	t.Run("searxng sends the instance's search URL, bearer key and trims results", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(searxngURLEnv, "http://searx.internal:8080//")
		t.Setenv(searxngKeyEnv, "tok")
		writeConfigFile(t, obj{"provider": "searxng"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["searxng"]) })
		two := 1.0
		text, _, err := search(t, "q x", &two, nil)
		eq(t, err, nil)
		eq(t, n.Requests[0].URL, "http://searx.internal:8080/search?format=json&q=q+x&safesearch=0")
		eq(t, n.Requests[0].Header.Get("Authorization"), "Bearer tok")
		eq(t, strings.Contains(text, "T2"), false)
	})
	t.Run("searxng explains 401 and 403", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(searxngURLEnv, "http://s")
		writeConfigFile(t, obj{"provider": "searxng"})
		useNet(t, func(netReq) netResp { return netResp{Status: 403, Body: "no"} })
		_, _, err := search(t, "q", nil, nil)
		eq(t, errText(err), "SearXNG Search API error (403) (the SearXNG instance may have JSON output disabled; enable 'json' under 'search.formats' in its settings.yml): no")
	})
	t.Run("searxng refuses a URL that is not http(s)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(searxngURLEnv, "ftp://host")
		writeConfigFile(t, obj{"provider": "searxng"})
		_, _, err := search(t, "q", nil, nil)
		eq(t, errText(err), "SEARXNG_URL must use http:// or https:// (got: ftp://)")
		t.Setenv(searxngURLEnv, "localhost:8080")
		_, _, err = search(t, "q", nil, nil)
		eq(t, errText(err), "SEARXNG_URL must use http:// or https:// (got: localhost://)")
		t.Setenv(searxngURLEnv, "not a url")
		_, _, err = search(t, "q", nil, nil)
		eq(t, errText(err), "SEARXNG_URL is not a valid URL (got: not a url)")
	})
	t.Run("ollama uses the experimental endpoints locally and the cloud ones elsewhere", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "ollama"})
		n := useNet(t, func(netReq) netResp { return jsonResp(searchBodies["ollama"]) })
		search(t, "q", nil, nil) // the default host is local
		eq(t, n.Requests[0].URL, "http://localhost:11434/api/experimental/web_search")
		eq(t, n.Requests[0].Body, `{"query":"q","max_results":5}`)
		t.Setenv(ollamaHostEnv, "https://ollama.com")
		t.Setenv(ollamaKeyEnv, "cloudkey")
		search(t, "q", nil, nil)
		eq(t, n.Requests[1].URL, "https://ollama.com/api/web_search")
		eq(t, n.Requests[1].Header.Get("Authorization"), "Bearer cloudkey")
	})
	t.Run("ollama explains a refused connection, 401 and 404", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "ollama"})
		useNet(t, func(netReq) netResp { return netResp{Failure: &netError{syscall.ECONNREFUSED}} })
		_, _, err := search(t, "q", nil, nil)
		eq(t, errText(err), "Could not connect to Ollama at http://localhost:11434. Make sure Ollama is running (ollama serve).")
		useNet(t, func(netReq) netResp { return netResp{Status: 401, Body: "who"} })
		_, _, err = search(t, "q", nil, nil)
		eq(t, errText(err), "Ollama Search API error (401) (run `ollama signin` to authenticate): who")
		useNet(t, func(netReq) netResp { return netResp{Status: 404, Body: "gone"} })
		_, _, err = search(t, "q", nil, nil)
		eq(t, strings.Contains(errText(err), "(the Ollama instance may not support web search; ensure you are running a recent version)"), true)
	})
	t.Run("ollama fetch posts the URL and returns the content", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "ollama"})
		n := useNet(t, func(netReq) netResp { return jsonResp(obj{"title": "T", "content": "BODY"}) })
		text, _, err := fetchURL(t, "https://example.com/p", false)
		eq(t, err, nil)
		eq(t, strings.HasSuffix(text, "BODY"), true)
		eq(t, n.Requests[0].URL, "http://localhost:11434/api/experimental/web_fetch")
		eq(t, n.Requests[0].Body, `{"url":"https://example.com/p"}`)
	})
}

type netError struct{ err error }

func (e *netError) Error() string { return "dial tcp: " + e.err.Error() }
func (e *netError) Unwrap() error { return e.err }

var _ = errors.New

func TestProviderPrecedence(t *testing.T) {
	t.Run("a per-call provider wins over a bogus WEB_SEARCH_PROVIDER", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "nope")
		writeConfigFile(t, obj{"apiKeys": obj{"tavily": "K"}})
		useNet(t, func(netReq) netResp { return jsonResp(searchBodies["tavily"]) })
		tav := "tavily"
		_, details, err := search(t, "q", nil, &tav)
		eq(t, err, nil)
		eq(t, detailsMap(t, details)["backend"], "tavily")
	})
	t.Run("a bogus WEB_SEARCH_PROVIDER throws on search and on fetch", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("WEB_SEARCH_PROVIDER", "nope")
		want := `Unknown web_search provider: "nope". Valid providers: brave, tavily, serper, exa, youcom, jina, firecrawl, perplexity, searxng, ollama.`
		_, _, err := search(t, "q", nil, nil)
		eq(t, errText(err), want)
		_, _, err = fetchURL(t, "https://example.com/", false)
		eq(t, errText(err), want)
	})
	t.Run("an unknown per-call provider throws", func(t *testing.T) {
		clearEnv(t)
		bad := "bing"
		_, _, err := search(t, "q", nil, &bad)
		eq(t, strings.HasPrefix(errText(err), `Unknown web_search provider: "bing".`), true)
	})
	t.Run("the env provider wins over config, config over the default", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "serper"})
		n, s := activeProvider(readConfig())
		eq(t, [2]string{n, s}, [2]string{"serper", "config"})
		t.Setenv("WEB_SEARCH_PROVIDER", "exa")
		n, s = activeProvider(readConfig())
		eq(t, [2]string{n, s}, [2]string{"exa", "env"})
		os.Unsetenv("WEB_SEARCH_PROVIDER")
		os.Remove(configPath())
		n, s = activeProvider(readConfig())
		eq(t, [2]string{n, s}, [2]string{"brave", "default"})
	})
}
