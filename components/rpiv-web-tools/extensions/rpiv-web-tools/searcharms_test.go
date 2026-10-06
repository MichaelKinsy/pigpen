// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"strings"
	"testing"
)

// fakeHTTP answers each arm with a canned response and records what was asked, so a twin asserts the request shape and
// the error text without a network. upstream: the vi.fn() fetch doubles in index.test.ts.
type fakeHTTP struct {
	status        int
	body          string
	contentType   string
	contentLength string
	last          httpRequest
	calls         int
}

func (f *fakeHTTP) Do(req httpRequest) (httpResponse, error) {
	f.last = req
	f.calls++
	return httpResponse{
		Status:           f.status,
		Body:             f.body,
		ContentType:      f.contentType,
		HasContentType:   f.contentType != "",
		ContentLength:    f.contentLength,
		HasContentLength: f.contentLength != "",
	}, nil
}

// arm is one provider's search arm behind a uniform signature, so a parameterized twin can sweep the table the way the
// upstream loop does.
type arm struct {
	meta providerMeta
	run  func(client httpDoer, creds providerCredentials, query string, maxResults int) (searchResponse, error)
}

// keyedArms are the eight providers with an API key and a fixed endpoint. upstream: brave, serper, perplexity, tavily,
// exa, jina, youcom, firecrawl.
func keyedArms() []arm {
	return []arm{
		{meta: mustMeta("brave"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchBrave(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("tavily"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchTavily(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("serper"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchSerper(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("exa"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchExa(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("youcom"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchYouCom(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("jina"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchJina(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("firecrawl"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchFirecrawl(c, creds.APIKey, q, n)
		}},
		{meta: mustMeta("perplexity"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchPerplexity(c, creds.APIKey, q, n)
		}},
	}
}

// hostedArms are the two self-hosted providers, which need a base URL instead of a key. upstream: searxng, ollama.
func hostedArms() []arm {
	return []arm{
		{meta: mustMeta("searxng"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchSearxng(c, creds.BaseURL, creds.APIKey, q, n)
		}},
		{meta: mustMeta("ollama"), run: func(c httpDoer, creds providerCredentials, q string, n int) (searchResponse, error) {
			return searchOllama(c, creds.BaseURL, creds.APIKey, q, n, true)
		}},
	}
}

func allArms() []arm { return append(keyedArms(), hostedArms()...) }

func mustMeta(name string) providerMeta {
	meta, ok := providerMetaByName(name)
	if !ok {
		panic("unknown provider in the arm table: " + name)
	}
	return meta
}

// credsFor gives an arm whatever it needs to get past its guard: a key for the keyed ones, a URL for the hosted ones.
func credsFor(meta providerMeta) providerCredentials {
	if meta.BaseURLEnvVar != "" {
		return providerCredentials{BaseURL: meta.DefaultBaseURL, HasBaseURL: true, APIKey: "k", HasAPIKey: true}
	}
	return providerCredentials{APIKey: "k", HasAPIKey: true}
}

func TestSearchNoResultsEnvelope(t *testing.T) {
	tw(t, fIndex, "returns no-results envelope for ${provider}", func(t *testing.T) {
		for _, a := range allArms() {
			t.Run(a.meta.Name, func(t *testing.T) {
				client := &fakeHTTP{status: 200, body: "{}"}
				res, err := a.run(client, credsFor(a.meta), "query", 5)
				if err != nil {
					t.Fatalf("%s: %v", a.meta.Name, err)
				}
				eq(t, res.Query, "query", "query echoed")
				eq(t, res.Results, []searchResult{}, "an empty list, never nil")
				eq(t, client.calls, 1, "one request")
			})
		}
	})
}

func TestSearchAPIErrorWrap(t *testing.T) {
	tw(t, fIndex, "wraps non-2xx as '${provider} Search API error (status)'", func(t *testing.T) {
		for _, a := range allArms() {
			t.Run(a.meta.Name, func(t *testing.T) {
				client := &fakeHTTP{status: 500, body: "boom"}
				_, err := a.run(client, credsFor(a.meta), "query", 5)
				if err == nil {
					t.Fatalf("%s: a non-2xx must throw", a.meta.Name)
				}
				// SearXNG adds its per-status hint between the status and the body; the other nine do not.
				want := fmt.Sprintf("%s Search API error (500): boom", a.meta.Label)
				if a.meta.Name == "searxng" {
					want = "SearXNG Search API error (500): boom"
				}
				eq(t, err.Error(), want, "error text")
			})
		}
	})
}

func TestSearchMissingCredentials(t *testing.T) {
	t.Run("the eight keyed providers refuse before any request", func(t *testing.T) {
		for _, a := range keyedArms() {
			t.Run(a.meta.Name, func(t *testing.T) {
				client := &fakeHTTP{status: 200, body: "{}"}
				_, err := a.run(client, providerCredentials{}, "query", 5)
				if err == nil {
					t.Fatalf("%s: a missing key must throw", a.meta.Name)
				}
				eq(t, err.Error(),
					a.meta.EnvVar+" is not set. Run /web-tools to configure, or export the env var.", "error text")
				eq(t, client.calls, 0, "no request is made")
			})
		}
	})
	t.Run("the two hosted providers refuse an unset URL", func(t *testing.T) {
		for _, a := range hostedArms() {
			t.Run(a.meta.Name, func(t *testing.T) {
				client := &fakeHTTP{status: 200, body: "{}"}
				_, err := a.run(client, providerCredentials{}, "query", 5)
				if err == nil {
					t.Fatalf("%s: an unset base URL must throw", a.meta.Name)
				}
				eq(t, client.calls, 0, "no request is made")
			})
		}
	})
	t.Run("slices results to max_results", func(t *testing.T) {
		rows := make([]string, 0, 4)
		for i := 0; i < 4; i++ {
			rows = append(rows, fmt.Sprintf(`{"title":"t%d","url":"u%d","content":"c%d"}`, i, i, i))
		}
		client := &fakeHTTP{status: 200, body: `{"results":[` + strings.Join(rows, ",") + `]}`}
		res, err := searchSearxng(client, "http://localhost:8080", "", "q", 2)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, len(res.Results), 2, "sliced to max_results")
	})
	t.Run("normalizes missing fields on result rows to empty strings", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"web":{"results":[{},{"url":"only-url"}]}}`}
		res, err := searchBrave(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{{Title: "", URL: "", Snippet: ""}, {Title: "", URL: "only-url", Snippet: ""}}, "rows")
	})
	t.Run("tolerates missing fields in organic results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"organic":[{"title":"only-title"}]}`}
		res, err := searchSerper(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{{Title: "only-title"}}, "row")
	})
	t.Run("attaches the SearXNG 403 and 401 hints", func(t *testing.T) {
		eq(t, searxngHintForStatus(403), " — JSON output may be disabled on this instance; set format=json in its settings", "403 hint")
		eq(t, searxngHintForStatus(401), " — the reverse proxy rejected the Bearer token", "401 hint")
		eq(t, searxngHintForStatus(500), "", "no hint for other statuses")
	})
	t.Run("sends the Authorization header only when a key is configured", func(t *testing.T) {
		withKey := &fakeHTTP{status: 200, body: "{}"}
		if _, err := searchSearxng(withKey, "http://localhost:8080", "k", "q", 5); err != nil {
			t.Fatal(err)
		}
		eq(t, withKey.last.Headers["Authorization"], "Bearer k", "bearer sent")
		withoutKey := &fakeHTTP{status: 200, body: "{}"}
		if _, err := searchSearxng(withoutKey, "http://localhost:8080", "", "q", 5); err != nil {
			t.Fatal(err)
		}
		if _, ok := withoutKey.last.Headers["Authorization"]; ok {
			t.Fatal("no key means no Authorization header")
		}
	})
	t.Run("strips every trailing slash from a base URL", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "{}"}
		if _, err := searchSearxng(client, "http://localhost:8080///", "", "q", 5); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(client.last.URL, "8080///") {
			t.Fatalf("trailing slashes survive in %q", client.last.URL)
		}
		eq(t, strings.HasPrefix(client.last.URL, "http://localhost:8080/search?"), true, "search path")
	})
	t.Run("caps the Exa snippet at 300 characters", func(t *testing.T) {
		long := strings.Repeat("x", 400)
		client := &fakeHTTP{status: 200, body: `{"results":[{"title":"t","url":"u","snippet":"` + long + `"}]}`}
		res, err := searchExa(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, len([]rune(res.Results[0].Snippet)), 300, "snippet length")
	})
}
