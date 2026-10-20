package websearch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/provider-credential-source.test.mjs. The upstream case runs seven providers
// (Cloudflare through the Gemini API); the table below holds the original's seven and the
// Mistral row of this port.

// credentialSourceRedactionAttempts is `attempts` of the upstream redaction case: the provider,
// the secret its command source prints, and the call that must fail with that secret redacted.
var credentialSourceRedactionAttempts = []struct {
	name, secret string
	run          func() error
}{
	{"brave", "brave-redaction-secret", func() error { _, err := SearchWithBrave(bg(), "query", SearchOptions{}); return err }},
	{"openai", "openai-redaction-secret", func() error { _, err := SearchWithOpenAI(bg(), "query", SearchOptions{}); return err }},
	{"parallel", "parallel-redaction-secret", func() error { _, err := SearchWithParallel(bg(), "query", SearchOptions{}); return err }},
	{"tinyfish", "tinyfish-redaction-secret", func() error { _, err := SearchWithTinyFish(bg(), "query", SearchOptions{}); return err }},
	{"perplexity", "perplexity-redaction-secret", func() error { _, err := SearchWithPerplexity(bg(), "query", SearchOptions{}); return err }},
	{"tavily", "tavily-redaction-secret", func() error { _, err := SearchWithTavily(bg(), "query", SearchOptions{}); return err }},
	{"mistral", "mistral-redaction-secret", func() error { _, err := SearchWithMistral(bg(), "query", SearchOptions{}); return err }},
	{"cloudflare", "cf-redaction-secret", func() error {
		_, err := QueryGeminiAPIWithVideo(bg(), "prompt", "files/synthetic", GeminiAPIOptions{})
		return err
	}},
}

func TestUpstream_provider_credential_source(t *testing.T) {
	const f = "provider-credential-source"

	tw(t, f, "previously unsupported providers resolve explicit env and command sources lazily", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the command sources are POSIX shell commands")
		}
		dir := t.TempDir()
		openaiMarker, tavilyMarker := filepath.Join(dir, "openai-ran"), filepath.Join(dir, "tavily-ran")
		_, agent := isolate(t)
		writeConfig(t, agent, string(mustJSONBody(map[string]any{
			"braveApiKey":  "${BRAVE_SCOPED_KEY}",
			"openaiApiKey": "!touch " + openaiMarker + " && printf openai-command-key",
			"tavilyApiKey": "!touch " + tavilyMarker + " && printf tavily-command-key",
		})))
		t.Setenv("BRAVE_SCOPED_KEY", "brave-scoped-key")
		exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case strings.Contains(c.URL, "api.search.brave.com"):
				return braveOK("Brave", "https://example.com/brave", "result")
			case strings.Contains(c.URL, "api.openai.com"):
				return jsonReply(legacyPayload)
			}
			return jsonReply(map[string]any{"results": []any{map[string]any{"title": "Tavily", "url": "https://example.com/tavily", "content": "result"}}})
		})

		openaiAvailable, err := IsOpenAISearchAvailable(nil)
		noErr(t, err)
		if !IsBraveAvailable() || !openaiAvailable || !IsTavilyAvailable() || exists(openaiMarker) || exists(tavilyMarker) {
			t.Fatal("availability ran a command, or a source was not found")
		}
		_, err = SearchWithBrave(bg(), "brave", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		_, err = SearchWithOpenAI(bg(), "openai", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		_, err = SearchWithTavily(bg(), "tavily", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		if !exists(openaiMarker) || !exists(tavilyMarker) {
			t.Fatal("the command sources did not run at request time")
		}
		if fn.calls[0].Header.Get("X-Subscription-Token") != "brave-scoped-key" ||
			fn.calls[1].Header.Get("Authorization") != "Bearer openai-command-key" ||
			fn.calls[2].Header.Get("Authorization") != "Bearer tavily-command-key" {
			t.Fatalf("%v", fn.calls)
		}
	})

	tw(t, f, "provider requests can resolve credentials through an inherited 1Password service-account token", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake tool is launched through a POSIX shell")
		}
		exe, err := os.Executable()
		noErr(t, err)
		_, agent := isolate(t)
		writeConfig(t, agent, string(mustJSONBody(map[string]any{"braveApiKey": "!'" + exe + "' __fake-op read op://Synthetic/Search/credential"})))
		t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "synthetic-service-account-token")
		t.Setenv("UNRELATED_SECRET", "must-not-reach-command")
		fn := useNet(t, func(netCall) netReply { return jsonReply(map[string]any{"web": map[string]any{"results": []any{}}}) })
		_, err = SearchWithBrave(bg(), "query", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		c := fn.calls[0]
		if c.Header.Get("X-Subscription-Token") != "synthetic-provider-key" || strings.Contains(fmt.Sprint(c), "synthetic-service-account-token") {
			t.Fatalf("%v", c)
		}
	})

	tw(t, f, "provider API errors redact resolved credential-source values", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the command sources are POSIX shell commands")
		}
		_, agent := isolate(t)
		writeConfig(t, agent, string(mustJSONBody(map[string]any{
			"braveApiKey":      "${BRAVE_SCOPED_KEY}",
			"cloudflareApiKey": "${CLOUDFLARE_SCOPED_KEY}",
			"geminiBaseUrl":    geminiGatewayAI,
			"openaiApiKey":     "!printf openai-redaction-secret",
			"parallelApiKey":   "!printf parallel-redaction-secret",
			"tinyfishApiKey":   "!printf tinyfish-redaction-secret",
			"perplexityApiKey": "!printf perplexity-redaction-secret",
			"tavilyApiKey":     "!printf tavily-redaction-secret",
			"mistralApiKey":    "!printf mistral-redaction-secret",
		})))
		t.Setenv("BRAVE_SCOPED_KEY", "brave-redaction-secret")
		t.Setenv("CLOUDFLARE_SCOPED_KEY", "cf-redaction-secret")
		for _, a := range credentialSourceRedactionAttempts {
			secret := a.secret
			useNet(t, func(netCall) netReply { return reply(400, string(mustJSONBody(map[string]any{"error": secret}))) })
			err := a.run()
			if err == nil {
				t.Fatalf("%s: no error", a.name)
			}
			if !strings.Contains(err.Error(), "[redacted]") || strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: %v", a.name, err)
			}
		}
	})

	// Title 4 (the Cloudflare gateway response) is twinned in gemini_test.go.
}
