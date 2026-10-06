// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

// The factory must reach every provider's real arm. A stub here would compile, pass every logic test and still make
// web_search fail at runtime, so each provider is driven through the factory with a canned response.

func TestFactoryReachesEveryArm(t *testing.T) {
	t.Run("each keyed provider's arm runs behind the factory", func(t *testing.T) {
		for _, name := range []string{"brave", "tavily", "serper", "exa", "youcom", "jina", "firecrawl", "perplexity"} {
			t.Run(name, func(t *testing.T) {
				client := &fakeHTTP{status: 200, body: vendorBodyFor(name)}
				provider, err := newSearchProvider(name, providerCredentials{APIKey: "k", HasAPIKey: true}, client)
				if err != nil {
					t.Fatal(err)
				}
				res, err := provider.Search("q", 5)
				if err != nil {
					t.Fatalf("%s: the arm must run, got %v", name, err)
				}
				if client.calls != 1 {
					t.Fatalf("%s: the arm must have made one request, got %d", name, client.calls)
				}
				if res.Query != "q" {
					t.Fatalf("%s: the query must echo back, got %q", name, res.Query)
				}
				if len(res.Results) == 0 {
					t.Fatalf("%s: the arm must normalize the fixture's row", name)
				}
			})
		}
	})
	t.Run("an unknown name is the factory's own error, before any client call", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "{}"}
		_, err := newSearchProvider("nope", providerCredentials{}, client)
		if err == nil {
			t.Fatal("an unknown provider must fail")
		}
		eq(t, err.Error(), `Unknown search provider: "nope"`, "error text")
		eq(t, client.calls, 0, "no request is made")
	})
	t.Run("a missing key throws before any client call", func(t *testing.T) {
		for _, name := range []string{"brave", "tavily", "serper", "exa", "youcom", "jina", "firecrawl", "perplexity"} {
			client := &fakeHTTP{status: 200, body: "{}"}
			provider, err := newSearchProvider(name, providerCredentials{}, client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Search("q", 5); err == nil {
				t.Fatalf("%s: a missing key must throw", name)
			}
			if client.calls != 0 {
				t.Fatalf("%s: no request may be made without a key", name)
			}
		}
	})
	t.Run("the identity half comes from the metadata table", func(t *testing.T) {
		meta, _ := providerMetaByName("youcom")
		provider, err := newSearchProvider("youcom", providerCredentials{APIKey: "k", HasAPIKey: true}, &fakeHTTP{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, provider.Name(), meta.Name, "name")
		eq(t, provider.Label(), meta.Label, "label")
		eq(t, provider.EnvVar(), meta.EnvVar, "env var")
	})
}

// vendorBodyFor is one result row in each vendor's own response shape, so the factory test proves the arm normalized
// the vendor's field names rather than the test's assumption.
func vendorBodyFor(name string) string {
	switch name {
	case "brave":
		return `{"web":{"results":[{"title":"A","url":"u","description":"d"}]}}`
	case "serper":
		return `{"organic":[{"title":"A","link":"u","snippet":"d"}]}`
	case "perplexity":
		return `{"results":[{"title":"A","url":"u","snippet":"d"}]}`
	case "tavily":
		return `{"results":[{"title":"A","url":"u","content":"d"}]}`
	case "exa":
		return `{"results":[{"title":"A","url":"u","text":"d"}]}`
	case "jina":
		return `{"data":[{"title":"A","url":"u","description":"d"}]}`
	case "youcom":
		return `{"results":{"web":[{"title":"A","url":"u","description":"d"}]}}`
	case "firecrawl":
		return `{"data":[{"title":"A","url":"u","description":"d"}]}`
	}
	return "{}"
}

// The registered tool must reach the same arms, so the wiring is proven end to end rather than per layer.
func TestRegisteredSearchReachesTheArm(t *testing.T) {
	write, _ := configHome(t)
	write(`{"provider":"brave","apiKeys":{"brave":"k"}}`)
	app := newApp()
	client := &fakeHTTP{status: 200, body: vendorBodyFor("brave")}
	app.client.doer = client

	name, err := instantiateProvider(ReadConfig(), "", false, envMap(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, name, "brave", "the configured provider is resolved")
	res, err := app.runSearch(name, ReadConfig(), "q", 3)
	if err != nil {
		t.Fatalf("the registered path must reach the arm, got %v", err)
	}
	eq(t, client.calls, 1, "one request")
	eq(t, strings.TrimSpace(res.Results[0].Title), "A", "the vendor's row is normalized")
}
