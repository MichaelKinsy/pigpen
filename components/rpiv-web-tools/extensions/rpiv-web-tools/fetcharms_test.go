// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

func TestFetchURLGuard(t *testing.T) {
	tw(t, fIndex, "throws on invalid URL", func(t *testing.T) {
		_, err := parseAndAssertHTTPURL("not a url")
		if err == nil {
			t.Fatal("an unparseable URL must throw")
		}
		eq(t, err.Error(), "Invalid URL: not a url", "error text")
	})
	tw(t, fIndex, "throws on non-http(s) protocol", func(t *testing.T) {
		_, err := parseAndAssertHTTPURL("file:///etc/passwd")
		if err == nil {
			t.Fatal("a file URL must throw")
		}
		eq(t, err.Error(), "Unsupported URL protocol: file:. Only http and https are supported.", "error text")
	})
	tw(t, fIndex, "refuses private/loopback host when active provider is searxng", func(t *testing.T) {
		// The guard is the orchestrator's, so it applies whichever provider is active; searxng is simply the case the
		// upstream case names, because a self-hosted backend is the one people point at a private host.
		for _, host := range []string{
			"http://localhost:8080/x", "http://127.0.0.1/x", "http://10.1.2.3/x", "http://192.168.1.1/x",
			"http://172.16.0.1/x", "http://169.254.169.254/latest/meta-data/", "http://[::1]/x", "http://foo.localhost/x",
		} {
			if _, err := parseAndAssertHTTPURL(host); err == nil {
				t.Fatalf("%s must be refused", host)
			} else if !strings.Contains(err.Error(), "Refusing to fetch private/loopback address") {
				t.Fatalf("%s: unexpected error %q", host, err.Error())
			}
		}
		// A public host still passes.
		if _, err := parseAndAssertHTTPURL("https://example.com/docs"); err != nil {
			t.Fatalf("a public host must pass, got %v", err)
		}
	})
}

func TestFetchGenericPath(t *testing.T) {
	tw(t, fIndex, "strips HTML and extracts title for text/html", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "text/html; charset=utf-8",
			body: `<html><head><title>My Page</title></head><body><p>Hello</p><p>World</p></body></html>`}
		res, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Title, "My Page", "title")
		// The title text stays inline: upstream strips only script/style/noscript, never <title>.
		eq(t, res.Text, "My Page Hello\n World", "text")
	})
	tw(t, fIndex, "throws on non-2xx with HTTP status in message", func(t *testing.T) {
		client := &fakeHTTP{status: 404, body: "missing"}
		_, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err == nil {
			t.Fatal("a 404 must throw")
		}
		eq(t, err.Error(), "HTTP 404 Not Found for https://example.com", "error text")
	})
	tw(t, fIndex, "wraps non-2xx as generic HTTP error from fetchViaGenericHtml", func(t *testing.T) {
		client := &fakeHTTP{status: 500, body: ""}
		_, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err == nil {
			t.Fatal("a 500 must throw")
		}
		eq(t, err.Error(), "HTTP 500 Internal Server Error for https://example.com", "error text")
	})
	tw(t, fIndex, "throws on binary content-type", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "image/png", body: "\x89PNG"}
		_, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err == nil {
			t.Fatal("a binary content type must throw")
		}
		eq(t, err.Error(), "Unsupported content type: image/png. web_fetch supports text pages only.", "error text")
	})
	tw(t, fIndex, "returns raw=true untouched", func(t *testing.T) {
		raw := "<html><head><title>T</title></head><body><p>keep me</p></body></html>"
		client := &fakeHTTP{status: 200, contentType: "text/html", body: raw}
		res, err := fetchViaProviderGeneric(client, "https://example.com", true)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, raw, "the body is untouched")
		eq(t, res.HasTitle, false, "no title is extracted in raw mode")
	})
	tw(t, fIndex, "sends UA + Accept headers + redirect:follow", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "text/plain", body: "ok"}
		if _, err := fetchViaProviderGeneric(client, "https://example.com", false); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.Method, "GET", "method")
		eq(t, client.last.Headers["User-Agent"], userAgent, "user agent")
		eq(t, client.last.Headers["Accept"], fetchAcceptHeader, "accept header")
	})
	tw(t, fIndex, "returns undefined contentType/contentLength when headers are absent", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "plain"}
		res, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.HasContentType, false, "content type absent")
		eq(t, res.ContentLength == nil, true, "content length absent")
	})
	tw(t, fIndex, "parses Number(contentLength) when the header is present", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "plain"}
		clientWithLength := client
		clientWithLength.contentLength = "1234"
		res, err := fetchViaGenericHTML(clientWithLength, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		if res.ContentLength == nil {
			t.Fatal("content length must be present")
		}
		eq(t, *res.ContentLength, 1234.0, "content length")
	})
	tw(t, fIndex, "decodes numeric HTML entities in text/html bodies", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "text/html", body: `<html><body><p>a&#38;b &#65; &amp;amp;</p></body></html>`}
		res, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "a&b A &amp;", "entities decoded in the original's order")
	})
	tw(t, fIndex, "does not throw on missing key (raw HTTP doesn't authenticate to the target)", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "text/plain", body: "ok"}
		res, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err != nil {
			t.Fatalf("the generic path never needs a key, got %v", err)
		}
		eq(t, res.Text, "ok", "body")
	})
}

func TestFetchNativeArms(t *testing.T) {
	tw(t, fIndex, "fetch throws when no key configured for ${provider}", func(t *testing.T) {
		for _, f := range []struct {
			meta string
			run  func(httpDoer, string) (fetchResponse, error)
		}{
			{"tavily", func(c httpDoer, k string) (fetchResponse, error) { return fetchTavily(c, k, "https://example.com") }},
			{"exa", func(c httpDoer, k string) (fetchResponse, error) { return fetchExa(c, k, "https://example.com") }},
			{"jina", func(c httpDoer, k string) (fetchResponse, error) { return fetchJina(c, k, "https://example.com") }},
			{"firecrawl", func(c httpDoer, k string) (fetchResponse, error) { return fetchFirecrawl(c, k, "https://example.com") }},
		} {
			t.Run(f.meta, func(t *testing.T) {
				client := &fakeHTTP{status: 200, body: "{}"}
				_, err := f.run(client, "")
				if err == nil {
					t.Fatalf("%s: a missing key must throw", f.meta)
				}
				meta := mustMeta(f.meta)
				eq(t, err.Error(), meta.EnvVar+" is not set. Run /web-tools to configure, or export the env var.", "error text")
				eq(t, client.calls, 0, "no request is made")
			})
		}
	})
	tw(t, fIndex, "fetch wraps non-2xx as '${label} Fetch API error (429)'", func(t *testing.T) {
		for _, f := range []struct {
			meta string
			run  func(httpDoer, string) (fetchResponse, error)
		}{
			{"tavily", func(c httpDoer, k string) (fetchResponse, error) { return fetchTavily(c, k, "https://example.com") }},
			{"exa", func(c httpDoer, k string) (fetchResponse, error) { return fetchExa(c, k, "https://example.com") }},
			{"jina", func(c httpDoer, k string) (fetchResponse, error) { return fetchJina(c, k, "https://example.com") }},
			{"firecrawl", func(c httpDoer, k string) (fetchResponse, error) { return fetchFirecrawl(c, k, "https://example.com") }},
		} {
			t.Run(f.meta, func(t *testing.T) {
				client := &fakeHTTP{status: 429, body: "rate limited"}
				_, err := f.run(client, "k")
				if err == nil {
					t.Fatalf("%s: a 429 must throw", f.meta)
				}
				eq(t, err.Error(), mustMeta(f.meta).Label+" Fetch API error (429): rate limited", "error text")
			})
		}
	})
	tw(t, fIndex, "brave fetch strips HTML and extracts title", func(t *testing.T) {
		client := &fakeHTTP{status: 200, contentType: "text/html",
			body: `<html><head><title>Doc</title></head><body><h1>Title</h1><p>Body</p></body></html>`}
		res, err := fetchViaProviderGeneric(client, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Title, "Doc", "title")
		if !strings.Contains(res.Text, "Body") {
			t.Fatalf("text carries the body, got %q", res.Text)
		}
	})
	tw(t, fIndex, "brave fetch returns raw HTML when raw=true", func(t *testing.T) {
		raw := "<html><body><p>x</p></body></html>"
		client := &fakeHTTP{status: 200, contentType: "text/html", body: raw}
		res, err := fetchViaProviderGeneric(client, "https://example.com", true)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, raw, "raw body")
	})
	tw(t, fIndex, "tavily fetch uses /extract endpoint", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"results":[{"raw_content":"body"}]}`}
		if _, err := fetchTavily(client, "k", "https://example.com"); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "https://api.tavily.com/extract", "endpoint")
	})
	tw(t, fIndex, "tavily fetch handles failed_results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"failed_results":[{"url":"https://bad","error":"blocked"}]}`}
		_, err := fetchTavily(client, "k", "https://example.com")
		if err == nil {
			t.Fatal("a failed extraction must throw")
		}
		eq(t, err.Error(), "Tavily Fetch API error: extraction failed for https://bad: blocked", "error text")
	})
	tw(t, fIndex, "exa fetch uses /contents endpoint", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"results":[{"text":"body","title":"T"}]}`}
		if _, err := fetchExa(client, "k", "https://example.com"); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "https://api.exa.ai/contents", "endpoint")
	})
	tw(t, fIndex, "exa fetch throws when no content returned", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"results":[{"title":"T"}]}`}
		_, err := fetchExa(client, "k", "https://example.com")
		if err == nil {
			t.Fatal("an empty content must throw")
		}
		eq(t, err.Error(), "Exa Fetch API error: no content returned for https://example.com", "error text")
	})
	tw(t, fIndex, "jina fetch throws when response body is empty", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "   "}
		_, err := fetchJina(client, "k", "https://example.com")
		if err == nil {
			t.Fatal("an empty body must throw")
		}
		eq(t, err.Error(), "Jina Fetch API error: no content returned for https://example.com", "error text")
	})
	tw(t, fIndex, "jina fetch uses r.jina.ai reader", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "# markdown"}
		res, err := fetchJina(client, "k", "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "https://r.jina.ai/https://example.com", "reader endpoint")
		eq(t, res.ContentType, "text/markdown", "reported content type")
	})
	tw(t, fIndex, "firecrawl fetch uses /v1/scrape endpoint", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"success":true,"data":{"markdown":"# md"}}`}
		if _, err := fetchFirecrawl(client, "k", "https://example.com"); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "https://api.firecrawl.dev/v1/scrape", "endpoint")
	})
	tw(t, fIndex, "firecrawl fetch throws on success=true with empty markdown", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"success":true,"data":{"markdown":""}}`}
		_, err := fetchFirecrawl(client, "k", "https://example.com")
		if err == nil {
			t.Fatal("empty markdown must throw")
		}
		eq(t, err.Error(), "Firecrawl Fetch API error: no content returned for https://example.com", "error text")
	})
	tw(t, fIndex, "firecrawl fetch handles success=false", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"success":false,"error":"blocked"}`}
		_, err := fetchFirecrawl(client, "k", "https://example.com")
		if err == nil {
			t.Fatal("success=false must throw")
		}
		eq(t, err.Error(), "Firecrawl Fetch API error: blocked", "error text")
	})
	tw(t, fIndex, "extraction providers (jina) ignore raw and never strip vendor body", func(t *testing.T) {
		markdown := "# Title\n\nSome <b>bold</b> text"
		client := &fakeHTTP{status: 200, body: markdown}
		res, err := fetchJina(client, "k", "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, markdown, "the vendor body is untouched")
	})
}
