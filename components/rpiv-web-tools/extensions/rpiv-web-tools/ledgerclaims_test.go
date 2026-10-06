// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// The per-provider titles the ledger repeats: SearXNG and Ollama share a wording, so each occurrence needs its own
// twin, and the provider under test is what tells them apart.

// TestSelfHostedSearchTitles claims the search titles the two self-hosted providers each carry. upstream:
// providers/searxng.ts and providers/ollama.ts, through the index ledger's parameterized cases.
func TestSelfHostedSearchTitles(t *testing.T) {
	t.Run("searxng", func(t *testing.T) {
		tw(t, fIndex, "wraps non-2xx as 'SearXNG Search API error (status)'", func(t *testing.T) {
			client := &fakeHTTP{status: 500, body: "boom"}
			_, err := searchSearxng(client, "http://localhost:8080", "", "q", 5)
			if err == nil {
				t.Fatal("a non-2xx must throw")
			}
			eq(t, err.Error(), "SearXNG Search API error (500): boom", "error text")
		})
		tw(t, fIndex, "403 attaches the 'JSON output may be disabled' hint", func(t *testing.T) {
			client := &fakeHTTP{status: 403, body: "no"}
			_, err := searchSearxng(client, "http://localhost:8080", "", "q", 5)
			if err == nil {
				t.Fatal("a 403 must throw")
			}
			if !strings.Contains(err.Error(), "JSON output may be disabled") {
				t.Fatalf("the hint is missing, got %q", err.Error())
			}
		})
		tw(t, fIndex, "401 attaches the 'reverse-proxy rejected the Bearer token' hint", func(t *testing.T) {
			client := &fakeHTTP{status: 401, body: "no"}
			_, err := searchSearxng(client, "http://localhost:8080", "", "q", 5)
			if err == nil {
				t.Fatal("a 401 must throw")
			}
			if !strings.Contains(err.Error(), "reverse proxy rejected the Bearer token") {
				t.Fatalf("the hint is missing, got %q", err.Error())
			}
		})
		tw(t, fIndex, "throws 'SEARXNG_URL is not set' when constructed with an empty baseUrl", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: "{}"}
			_, err := searchSearxng(client, "", "", "q", 5)
			if err == nil {
				t.Fatal("an empty base URL must throw")
			}
			eq(t, err.Error(),
				"SEARXNG_URL is not set. Run /web-tools to configure, or export the env var.", "error text")
			eq(t, client.calls, 0, "no request is made")
		})
		t.Run("base URL validation", func(t *testing.T) {
			tw(t, fIndex, "accepts http baseUrl", func(t *testing.T) {
				if err := assertHTTPURL("http://localhost:8080"); err != nil {
					t.Fatalf("http must be accepted, got %v", err)
				}
			})
			tw(t, fIndex, "accepts https baseUrl", func(t *testing.T) {
				if err := assertHTTPURL("https://searx.example.com"); err != nil {
					t.Fatalf("https must be accepted, got %v", err)
				}
			})
			tw(t, fIndex, "accepts an empty baseUrl (deferred-config state — search() then throws)", func(t *testing.T) {
				// The constructor stores an empty base URL without complaint; only search() refuses it, with the
				// URL-not-set error rather than an invalid-URL one.
				client := &fakeHTTP{status: 200, body: "{}"}
				_, err := searchSearxng(client, "", "", "q", 5)
				if err == nil {
					t.Fatal("search must refuse an empty base URL")
				}
				eq(t, err.Error(),
					"SEARXNG_URL is not set. Run /web-tools to configure, or export the env var.", "error text")
			})
			tw(t, fIndex, "rejects file:// scheme", func(t *testing.T) {
				if err := assertHTTPURL("file:///etc/passwd"); err == nil {
					t.Fatal("a file URL must be refused")
				}
			})
			tw(t, fIndex, "rejects javascript: scheme", func(t *testing.T) {
				if err := assertHTTPURL("javascript:alert(1)"); err == nil {
					t.Fatal("a javascript URL must be refused")
				}
			})
			tw(t, fIndex, "rejects an unparseable URL", func(t *testing.T) {
				if err := assertHTTPURL("http://[::1"); err == nil {
					t.Fatal("an unparseable URL must be refused")
				}
			})
		})
		t.Run("metadata", func(t *testing.T) {
			tw(t, fIndex, "declares envVar as SEARXNG_API_KEY (optional Bearer key)", func(t *testing.T) {
				meta, _ := providerMetaByName("searxng")
				eq(t, meta.EnvVar, "SEARXNG_API_KEY", "env var")
			})
			tw(t, fIndex, "declares baseUrlEnvVar as SEARXNG_URL (the URL that actually activates it)", func(t *testing.T) {
				meta, _ := providerMetaByName("searxng")
				eq(t, meta.BaseURLEnvVar, "SEARXNG_URL", "url env var")
			})
		})
	})

	t.Run("ollama", func(t *testing.T) {
		meta, _ := providerMetaByName("ollama")
		tw(t, fIndex, "uses env URL (wins over config and default)", func(t *testing.T) {
			env := envMap(map[string]string{ollamaHostEnvVar: "http://env:11434"})
			eq(t, resolveProviderBaseURL(meta, config{BaseURLs: map[string]string{"ollama": "http://config:11434"}}, env),
				"http://env:11434", "env url")
		})
		tw(t, fIndex, "falls back to config URL when env is unset", func(t *testing.T) {
			eq(t, resolveProviderBaseURL(meta, config{BaseURLs: map[string]string{"ollama": "http://config:11434"}}, noEnv),
				"http://config:11434", "config url")
		})
		tw(t, fIndex, "falls back to default URL (http://localhost:11434) when neither env nor config is set", func(t *testing.T) {
			eq(t, resolveProviderBaseURL(meta, config{}, noEnv), "http://localhost:11434", "default url")
		})
		tw(t, fIndex, "trailing slash on baseUrl does not produce a double-slash", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{"results":[]}`}
			if _, err := searchOllama(client, "http://localhost:11434/", "", "q", 5, true); err != nil {
				t.Fatal(err)
			}
			eq(t, strings.HasPrefix(client.last.URL, "http://localhost:11434/api/experimental/web_search"), true, "no double slash")
		})
		tw(t, fIndex, "returns no-results envelope on empty results array", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{}`}
			res, err := searchOllama(client, "http://localhost:11434", "", "q", 5, true)
			if err != nil {
				t.Fatal(err)
			}
			eq(t, res.Results, []searchResult{}, "an empty list, never nil")
		})
		tw(t, fIndex, "wraps non-2xx as 'Ollama Search API error (status)'", func(t *testing.T) {
			client := &fakeHTTP{status: 500, body: "boom"}
			_, err := searchOllama(client, "http://localhost:11434", "", "q", 5, true)
			if err == nil {
				t.Fatal("a non-2xx must throw")
			}
			eq(t, err.Error(), "Ollama Search API error (500): boom", "error text")
		})
		tw(t, fIndex, "normalizes missing fields on result rows to empty strings", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{"results":[{},{"url":"only-url"}]}`}
			res, err := searchOllama(client, "http://localhost:11434", "", "q", 5, true)
			if err != nil {
				t.Fatal(err)
			}
			eq(t, res.Results, []searchResult{{}, {URL: "only-url"}}, "rows")
		})
	})
}

// TestPerProviderFieldTolerance claims the two per-provider titles the keyed arms carry. upstream: providers/brave.ts
// and providers/serper.ts normalisation.
func TestPerProviderFieldTolerance(t *testing.T) {
	tw(t, fIndex, "brave search tolerates missing fields in organic results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"web":{"results":[{"title":"only-title"}]}}`}
		res, err := searchBrave(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{{Title: "only-title"}}, "row")
	})
	tw(t, fIndex, "serper search tolerates missing fields in organic results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"organic":[{"title":"only-title"}]}`}
		res, err := searchSerper(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{{Title: "only-title"}}, "row")
	})
	tw(t, fIndex, "coerces content-length to numeric details.contentLength", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: "plain", contentLength: "4096"}
		res, err := fetchViaGenericHTML(client, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		if res.ContentLength == nil {
			t.Fatal("content length must be present")
		}
		eq(t, *res.ContentLength, 4096.0, "content length")
	})
}

// TestSelfHostedConfigureTitles claims the setup titles each self-hosted provider carries. upstream:
// providers/ollama.ts configureOllama and providers/searxng.ts configureSearxng.
func TestSelfHostedConfigureTitles(t *testing.T) {
	t.Run("ollama", func(t *testing.T) {
		tw(t, fIndex, "prompts URL first, then optional key, and persists both", func(t *testing.T) {
			ui := &scriptedUI{answers: []userInput{typed("http://host:11434"), typed("k")}}
			change, ok := configureOllama(ui, providerConfigCurrent{})
			if !ok {
				t.Fatal("the flow must complete")
			}
			eq(t, change.BaseURL, "http://host:11434", "base url")
			eq(t, change.APIKey, "k", "api key")
			eq(t, ui.labels[0], "Ollama base URL", "URL is prompted first")
		})
		tw(t, fIndex, "empty URL input falls back to the default URL and leaves key unset", func(t *testing.T) {
			ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
			change, ok := configureOllama(ui, providerConfigCurrent{})
			if !ok {
				t.Fatal("the flow must complete")
			}
			eq(t, change.BaseURL, "http://localhost:11434", "default url")
			eq(t, change.HasAPIKey, false, "key unset")
		})
		tw(t, fIndex, "uses env key", func(t *testing.T) {
			env := envMap(map[string]string{ollamaAPIKeyEnvVar: "env-key"})
			eq(t, resolveProviderAPIKey("ollama", config{}, env), "env-key", "env key")
		})
		tw(t, fIndex, "falls back to config key", func(t *testing.T) {
			eq(t, resolveProviderAPIKey("ollama", config{APIKeys: map[string]string{"ollama": "cfg"}}, noEnv), "cfg", "config key")
		})
		tw(t, fIndex, "throws when no key configured", func(t *testing.T) {
			eq(t, resolveProviderAPIKey("ollama", config{}, noEnv), "", "no key")
		})
	})
	t.Run("searxng", func(t *testing.T) {
		tw(t, fIndex, "returns null when the user cancels at the API-key prompt", func(t *testing.T) {
			ui := &scriptedUI{answers: []userInput{typed("http://host:8080"), cancelled()}}
			if _, ok := configureSearxng(ui, providerConfigCurrent{}); ok {
				t.Fatal("a cancel at the key prompt must stop the flow")
			}
		})
		tw(t, fIndex, "trailing slash on baseUrl does not produce a double-slash", func(t *testing.T) {
			eq(t, stripTrailingSlashes("http://localhost:8080/"), "http://localhost:8080", "one slash")
			eq(t, stripTrailingSlashes("http://localhost:8080////"), "http://localhost:8080", "every slash")
		})
		tw(t, fIndex, "multiple trailing slashes on baseUrl are all stripped", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{}`}
			if _, err := searchSearxng(client, "http://localhost:8080///", "", "q", 5); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(client.last.URL, "8080///") {
				t.Fatalf("trailing slashes survive in %q", client.last.URL)
			}
			eq(t, filepath.Base(strings.Split(client.last.URL, "?")[0]), "search", "search path")
		})
	})
}
