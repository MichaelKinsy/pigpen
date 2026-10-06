// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

// scriptedUI answers the prompts of a provider's configure() from a fixed script, so the twin asserts the labels,
// the placeholders and the outcome without a host. upstream: the ui double in the upstream twins.
type scriptedUI struct {
	answers []userInput
	labels  []string
	asked   int
}

func (s *scriptedUI) Input(label, _ string) userInput {
	s.labels = append(s.labels, label)
	if s.asked >= len(s.answers) {
		s.asked++
		return userInput{Set: false}
	}
	answer := s.answers[s.asked]
	s.asked++
	return answer
}

func typed(v string) userInput { return userInput{Value: v, Set: true} }
func cancelled() userInput     { return userInput{} }

func TestSelfHostedConfigure(t *testing.T) {
	tw(t, fIndex, "prompts URL first, then optional key, and persists both", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed("http://host:8080"), typed("k")}}
		change, ok := configureSearxng(ui, providerConfigCurrent{})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://host:8080", "base url")
		eq(t, change.HasBaseURL, true, "base url present")
		eq(t, change.APIKey, "k", "api key")
		eq(t, ui.labels[0], "SearXNG base URL", "URL is prompted first")
		eq(t, ui.labels[1], "SearXNG API key (optional — some instances sit behind a reverse proxy that requires it)", "key prompt")
	})
	tw(t, fIndex, "uses SEARXNG_DEFAULT_URL and null apiKey when both inputs are empty and no current values exist", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
		change, ok := configureSearxng(ui, providerConfigCurrent{})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://localhost:8080", "default url")
		eq(t, change.HasAPIKey, false, "the key stays unset")
	})
	tw(t, fIndex, "keeps current values when both inputs are empty", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
		change, ok := configureSearxng(ui, providerConfigCurrent{
			BaseURL: "http://kept:8080", HasBaseURL: true, APIKey: "kept-key", HasAPIKey: true,
		})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://kept:8080", "kept url")
		eq(t, change.APIKey, "kept-key", "kept key")
	})
	tw(t, fIndex, "uses fresh values when both inputs are non-empty", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed("http://fresh:8080"), typed("fresh")}}
		change, ok := configureSearxng(ui, providerConfigCurrent{
			BaseURL: "http://old:8080", HasBaseURL: true, APIKey: "old", HasAPIKey: true,
		})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://fresh:8080", "fresh url")
		eq(t, change.APIKey, "fresh", "fresh key")
	})
	tw(t, fIndex, "prompts URL first, then key, with placeholders that reflect current values", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
		if _, ok := configureSearxng(ui, providerConfigCurrent{
			BaseURL: "http://kept:8080", HasBaseURL: true, APIKey: "sk-live-abcdefghij", HasAPIKey: true,
		}); !ok {
			t.Fatal("the flow must complete")
		}
		// The key placeholder masks; the URL placeholder shows the current URL in the clear.
		eq(t, len(ui.labels), 2, "two prompts")
	})
	tw(t, fIndex, "returns null when the user cancels at the URL prompt", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{cancelled()}}
		if _, ok := configureSearxng(ui, providerConfigCurrent{}); ok {
			t.Fatal("a cancel at the URL prompt stops the flow")
		}
		eq(t, ui.asked, 1, "the key prompt is never reached")
	})
	tw(t, fIndex, "keeps existing URL and key when both inputs are empty", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed("  "), typed("  ")}}
		change, ok := configureOllama(ui, providerConfigCurrent{
			BaseURL: "http://kept:11434", HasBaseURL: true, APIKey: "kept", HasAPIKey: true,
		})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://kept:11434", "kept url")
		eq(t, change.APIKey, "kept", "kept key")
	})
	tw(t, fIndex, "empty URL input falls back to the default URL and leaves key unset", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
		change, ok := configureOllama(ui, providerConfigCurrent{})
		if !ok {
			t.Fatal("the flow must complete")
		}
		eq(t, change.BaseURL, "http://localhost:11434", "ollama default url")
		eq(t, change.HasAPIKey, false, "key unset")
	})
	tw(t, fIndex, "URL cancel (undefined) leaves config untouched", func(t *testing.T) {
		ui := &scriptedUI{answers: []userInput{cancelled()}}
		if _, ok := configureOllama(ui, providerConfigCurrent{BaseURL: "http://x", HasBaseURL: true}); ok {
			t.Fatal("a cancel must stop the flow")
		}
	})
}

func TestOllamaFetch(t *testing.T) {
	tw(t, fIndex, "ollama fetch uses /api/experimental/web_fetch endpoint", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"content":"body"}`}
		if _, err := fetchOllama(client, "http://localhost:11434", "", "https://example.com", true); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "http://localhost:11434/api/experimental/web_fetch", "local endpoint")
		client = &fakeHTTP{status: 200, body: `{"content":"body"}`}
		if _, err := fetchOllama(client, "https://ollama.com", "", "https://example.com", false); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.URL, "https://ollama.com/api/web_fetch", "cloud endpoint")
	})
	tw(t, fIndex, "ollama fetch throws when content is empty", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"title":"T"}`}
		_, err := fetchOllama(client, "http://localhost:11434", "", "https://example.com", true)
		if err == nil {
			t.Fatal("empty content must throw")
		}
		eq(t, err.Error(), "Ollama Fetch API error: no content returned for https://example.com", "error text")
	})
	tw(t, fIndex, "ollama fetch wraps non-2xx as 'Ollama Fetch API error (status)'", func(t *testing.T) {
		client := &fakeHTTP{status: 503, body: "down"}
		_, err := fetchOllama(client, "http://localhost:11434", "", "https://example.com", true)
		if err == nil {
			t.Fatal("a 503 must throw")
		}
		eq(t, err.Error(), "Ollama Fetch API error (503): down", "error text")
	})
	tw(t, fIndex, "401 attaches the 'ollama signin' hint", func(t *testing.T) {
		client := &fakeHTTP{status: 401, body: "no"}
		_, err := fetchOllama(client, "http://localhost:11434", "", "https://example.com", true)
		if err == nil {
			t.Fatal("a 401 must throw")
		}
		if !strings.Contains(err.Error(), "run `ollama signin` to authenticate") {
			t.Fatalf("the signin hint is missing, got %q", err.Error())
		}
	})
	tw(t, fIndex, "404 attaches the 'may not support web search' hint", func(t *testing.T) {
		eq(t, ollamaHintForStatus(404),
			" (the Ollama instance may not support web search; ensure you are running a recent version)", "404 hint")
		eq(t, ollamaHintForStatus(401), " (run `ollama signin` to authenticate)", "401 hint")
		eq(t, ollamaHintForStatus(500), "", "no hint elsewhere")
	})
	tw(t, fIndex, "surfaces connection-refused with actionable hint", func(t *testing.T) {
		eq(t, connectionRefusedError("http://localhost:11434").Error(),
			"Could not connect to Ollama at http://localhost:11434. Make sure Ollama is running (ollama serve).",
			"connection refused message")
	})
	t.Run("wraps a non-2xx search response with the same hint", func(t *testing.T) {
		eq(t, searchOllamaHintError(httpResponse{Status: 401, Body: "no"}).Error(),
			"Ollama Search API error (401) (run `ollama signin` to authenticate): no", "search hint")
	})
	t.Run("sends Bearer Authorization when a key is configured", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"content":"body"}`}
		if _, err := fetchOllama(client, "http://localhost:11434", "k", "https://example.com", true); err != nil {
			t.Fatal(err)
		}
		eq(t, client.last.Headers["Authorization"], "Bearer k", "bearer sent")
		client = &fakeHTTP{status: 200, body: `{"content":"body"}`}
		if _, err := fetchOllama(client, "http://localhost:11434", "", "https://example.com", true); err != nil {
			t.Fatal(err)
		}
		if _, ok := client.last.Headers["Authorization"]; ok {
			t.Fatal("no key means no Authorization header")
		}
	})
}
