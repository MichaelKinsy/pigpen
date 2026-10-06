// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scriptedHost answers the command's UI calls from a script and records the notifications. upstream: createMockCtx in
// index.test.ts, with ctx.ui.select/input/notify as vi.fn() doubles.
type scriptedHost struct {
	hasUIValue    bool
	selects       []selectOutcome
	inputs        []inputOutcome
	selectIdx     int
	inputIdx      int
	notified      []string
	levels        []string
	saved         []config
	saveFails     bool
	labelsSeen    []string
	selectOptions []string
}

func (h *scriptedHost) hasUI() bool { return h.hasUIValue }

func (h *scriptedHost) selectOne(title string, options []string) (string, bool) {
	h.labelsSeen = append(h.labelsSeen, title)
	h.selectOptions = options
	if h.selectIdx >= len(h.selects) {
		h.selectIdx++
		return "", false
	}
	out := h.selects[h.selectIdx]
	h.selectIdx++
	return out.Label, !out.Cancelled
}

func (h *scriptedHost) input(label, _ string) (string, bool) {
	h.labelsSeen = append(h.labelsSeen, label)
	if h.inputIdx >= len(h.inputs) {
		h.inputIdx++
		return "", false
	}
	out := h.inputs[h.inputIdx]
	h.inputIdx++
	return out.Value, !out.Cancelled
}

func (h *scriptedHost) notify(message, level string) {
	h.notified = append(h.notified, message)
	h.levels = append(h.levels, level)
}

func (h *scriptedHost) save(c config) bool {
	h.saved = append(h.saved, c)
	return !h.saveFails
}

func TestCommandFlow(t *testing.T) {
	setup := func() (*scriptedHost, config, string) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		return &scriptedHost{hasUIValue: true}, config{}, ConfigPath()
	}

	tw(t, fIndex, "!hasUI notifies error", func(t *testing.T) {
		host, cfg, path := setup()
		host.hasUIValue = false
		runConfigCommand(host, "", cfg, noEnv, path, host.save)
		eq(t, len(host.notified), 1, "one notification")
		eq(t, host.notified[0], "/web-tools requires interactive mode", "message")
		eq(t, host.levels[0], notifyError, "level")
	})
	tw(t, fIndex, "two-step: select provider then enter key", func(t *testing.T) {
		host, cfg, path := setup()
		host.selects = []selectOutcome{{Label: "Tavily"}}
		host.inputs = []inputOutcome{{Value: "tv-key"}}
		runConfigCommand(host, "", cfg, noEnv, path, host.save)
		eq(t, host.labelsSeen, []string{"Search provider", "Tavily API key"}, "the picker comes first, then the key prompt")
		eq(t, contains(host.selectOptions, "Tavily"), true, "the picker is offered the providers")
		eq(t, host.labelsSeen[1], "Tavily API key", "the key prompt names the provider")
		eq(t, len(host.saved), 1, "one save")
		eq(t, host.saved[0].Provider, "tavily", "the selected provider is stored")
		eq(t, host.saved[0].APIKeys["tavily"], "tv-key", "the key is stored")
	})
	tw(t, fIndex, "notifies error and skips 'Saved …' when the underlying write fails", func(t *testing.T) {
		host, cfg, path := setup()
		host.selects = []selectOutcome{{Label: "Brave"}}
		host.inputs = []inputOutcome{{Value: "k"}}
		host.saveFails = true
		runConfigCommand(host, "", cfg, noEnv, path, host.save)
		eq(t, len(host.notified), 1, "exactly one notification")
		eq(t, host.levels[0], notifyError, "it is an error")
		if strings.Contains(host.notified[0], "Saved") {
			t.Fatalf("a failed save must not claim success, got %q", host.notified[0])
		}
		if !strings.Contains(host.notified[0], "disk write failed") {
			t.Fatalf("the message must name the real cause, got %q", host.notified[0])
		}
	})
	tw(t, fIndex, "spills full body to temp file and appends truncation footer when truncated", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		body := strings.Repeat("a line of content\n", 400)
		kept, truncation, err := truncateBody(body, 200)
		if err != nil {
			t.Fatal(err)
		}
		if !truncation.Truncated {
			t.Fatal("a capped body must report truncation")
		}
		if len(kept) >= len(body) {
			t.Fatalf("the kept body must be smaller: %d vs %d", len(kept), len(body))
		}
		if truncation.TempFilePath == "" {
			t.Fatal("the full content must be spilled to a temp file")
		}
		full, err := os.ReadFile(truncation.TempFilePath)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, string(full), body, "the temp file holds the whole body")
		footer := formatTruncationFooter(truncation)
		for _, want := range []string{"[Content truncated: showing ", "of " + itoa(truncation.TotalLines) + " lines",
			"Full content saved to: " + truncation.TempFilePath} {
			if !strings.Contains(footer, want) {
				t.Fatalf("the footer is missing %q, got:\n%s", want, footer)
			}
		}
		if filepath.Base(truncation.TempFilePath) != fetchTempFileName {
			t.Fatalf("the spill file name changed, got %q", filepath.Base(truncation.TempFilePath))
		}
	})
	t.Run("a body within the budget is returned untouched", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		body := "short body"
		kept, truncation, err := truncateBody(body, 50000)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, kept, body, "body")
		eq(t, truncation.Truncated, false, "not truncated")
		eq(t, truncation.TempFilePath, "", "nothing spilled")
	})
}

func TestShowInterceptorLines(t *testing.T) {
	tw(t, fIndex, "--show lists 'github: disabled' with how-to-enable hint when interceptor is off", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		msg := strings.Join(showConfigLines(config{}, noEnv, interceptorsConfig{}, githubEnabledFrom(config{}), ""), "\n")
		if !strings.Contains(msg, "github: disabled") {
			t.Fatalf("the disabled line is missing, got:\n%s", msg)
		}
		for _, want := range []string{`"interceptors": { "github": true }`, `"interceptors": { "github": false }`} {
			if !strings.Contains(msg, want) {
				t.Fatalf("the how-to line %q is missing, got:\n%s", want, msg)
			}
		}
	})
	tw(t, fIndex, "--show lists 'github: enabled' with token + clonePath when opted in", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		write, _ := configHome(t)
		write(`{"interceptors":{"github":{"maxRepoSizeMB":500,"clonePath":"/tmp/gh"}}}`)
		cfg := ReadConfig()
		msg := strings.Join(showConfigLines(cfg, noEnv, *cfg.Interceptors, githubEnabledFrom(cfg), "ghp_tokenvalue"), "\n")
		if !strings.Contains(msg, "github: enabled") {
			t.Fatalf("the enabled line is missing, got:\n%s", msg)
		}
		for _, want := range []string{"GITHUB_TOKEN: ghp_...alue", "maxRepoSizeMB: 500", "clonePath: /tmp/gh"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("the enabled line omits %q, got:\n%s", want, msg)
			}
		}
	})
}

func TestInterceptorDispatchTitles(t *testing.T) {
	tw(t, fIndex, "default OFF: github.com URLs go straight to active provider (no interceptor registered)", func(t *testing.T) {
		registry := &interceptorRegistry{}
		chain := registry.build(userGitHubConfig{}, false)
		client := &fakeHTTP{status: 200, contentType: "text/plain", body: "provider"}
		res, err := fetchDispatch(client, chain, nil, "https://github.com/owner/repo/blob/main/a.go", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "provider", "the provider answered")
		eq(t, len(chain), 0, "no interceptor is registered by default")
	})
	tw(t, fIndex, "user config wins: { interceptors: { github: false } } overrides consumer opt-in", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":false}}`)
		registry := &interceptorRegistry{}
		chain := registry.build(readUserGitHubConfig(), true)
		eq(t, len(chain), 0, "the user disable beats the consumer opt-in")
	})
	tw(t, fIndex, "falls back to provider.fetch when parseGitHubUrl returns null (non-code github URL)", func(t *testing.T) {
		interceptor := &countingInterceptor{name_: "github", urls: map[string]string{}}
		providerCalls := 0
		res, err := fetchDispatch(&fakeHTTP{status: 200, body: "generic"},
			[]urlInterceptor{interceptor},
			func(target string, _ bool) (fetchResponse, error) {
				providerCalls++
				return fetchResponse{Text: "provider for " + target}, nil
			}, "https://github.com/owner/repo/issues/1", false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Text, "provider for") || providerCalls != 1 {
			t.Fatalf("the provider must answer, got %q after %d calls", res.Text, providerCalls)
		}
	})
	tw(t, fIndex, "does not intercept non-GitHub URLs — active provider handles them", func(t *testing.T) {
		interceptor := &countingInterceptor{name_: "github", urls: map[string]string{}}
		res, err := fetchDispatch(&fakeHTTP{status: 200, body: "generic"},
			[]urlInterceptor{interceptor},
			func(target string, _ bool) (fetchResponse, error) {
				return fetchResponse{Text: "provider for " + target}, nil
			}, "https://gitlab.com/a/b", false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Text, "provider for") {
			t.Fatalf("the interceptor must not answer, got %q", res.Text)
		}
	})
	tw(t, fIndex, "SSRF guard fires before github.com hostname check — refuses private/loopback addresses", func(t *testing.T) {
		// The guard runs on the raw URL before anything parses the host, so a loopback address never reaches the
		// interceptor or the provider.
		for _, raw := range []string{"http://127.0.0.1/repo", "http://localhost:8080/o/r", "http://169.254.169.254/"} {
			if _, err := parseAndAssertHTTPURL(raw); err == nil {
				t.Fatalf("%s must be refused before the host check", raw)
			} else if !strings.Contains(err.Error(), "Refusing to fetch private/loopback address") {
				t.Fatalf("%s: unexpected error %q", raw, err.Error())
			}
		}
	})
}

func TestFetchOverrideTitles(t *testing.T) {
	tw(t, fIndex, "override works with config-file key (no env var)", func(t *testing.T) {
		cfg := config{APIKeys: map[string]string{"tavily": "from-config"}}
		name, err := instantiateProvider(cfg, "tavily", true, noEnv)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "tavily", "the override wins")
		eq(t, resolveProviderAPIKey(name, cfg, noEnv), "from-config", "the key comes from the config file")
	})
	tw(t, fIndex, "override resolves baseUrl for self-hosted providers (searxng)", func(t *testing.T) {
		meta, _ := providerMetaByName("searxng")
		cfg := config{BaseURLs: map[string]string{"searxng": "http://config:8080"}}
		eq(t, resolveProviderBaseURL(meta, cfg, noEnv), "http://config:8080", "the base URL resolves for the override")
	})
	tw(t, fIndex, "override throws when the named provider has no key configured (no silent fallback)", func(t *testing.T) {
		cfg := config{Provider: "brave", APIKeys: map[string]string{"brave": "k"}}
		if _, err := instantiateProvider(cfg, "exa", true, noEnv); err != nil {
			t.Fatalf("naming an unconfigured provider is not a resolution error, got %v", err)
		}
		eq(t, resolveProviderAPIKey("exa", cfg, noEnv), "", "exa has no key, so the arm must throw at call time")
	})
	tw(t, fIndex, "WEB_SEARCH_PROVIDER beats config.provider (env-pinned tavily fetch)", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "tavily", tavilyAPIKeyEnvVar: "tv"})
		name, err := instantiateProvider(config{Provider: "brave"}, "", false, env)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "tavily", "the env pins the backend")
		eq(t, resolveProviderAPIKey(name, config{}, env), "tv", "the env-pinned provider's own key is used")
	})
	tw(t, fIndex, "unknown WEB_SEARCH_PROVIDER name throws (no silent fallback)", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "nope"})
		_, err := instantiateProvider(config{}, "", false, env)
		if err == nil {
			t.Fatal("a bogus env provider must throw")
		}
		if !strings.Contains(err.Error(), `Unknown web_search provider: "nope"`) {
			t.Fatalf("unexpected error %q", err.Error())
		}
	})
}

func TestYouComTitles(t *testing.T) {
	tw(t, fIndex, "wraps non-2xx as 'You.com Search API error (status)'", func(t *testing.T) {
		client := &fakeHTTP{status: 502, body: "bad gateway"}
		_, err := searchYouCom(client, "k", "q", 5)
		if err == nil {
			t.Fatal("a non-2xx must throw")
		}
		eq(t, err.Error(), "You.com Search API error (502): bad gateway", "error text")
	})
	tw(t, fIndex, "tolerates missing fields in results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{"results":{"web":[{"title":"only-title"}]}}`}
		res, err := searchYouCom(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{{Title: "only-title"}}, "row")
	})
	tw(t, fIndex, "returns no-results envelope on empty results", func(t *testing.T) {
		client := &fakeHTTP{status: 200, body: `{}`}
		res, err := searchYouCom(client, "k", "q", 5)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Results, []searchResult{}, "an empty list, never nil")
		text, details := emptyResultsEnvelope("q", "youcom")
		eq(t, text, `No results found for "q".`, "envelope text")
		eq(t, details, searchEnvelopeDetails{Query: "q", Backend: "youcom", ResultCount: 0}, "envelope details")
	})
}

func TestSelfHostedRemainingTitles(t *testing.T) {
	t.Run("searxng", func(t *testing.T) {
		tw(t, fIndex, "sends Bearer Authorization when API key is configured", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: "{}"}
			if _, err := searchSearxng(client, "http://localhost:8080", "k", "q", 5); err != nil {
				t.Fatal(err)
			}
			eq(t, client.last.Headers["Authorization"], "Bearer k", "bearer sent")
		})
		tw(t, fIndex, "omits Authorization when no API key is configured", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: "{}"}
			if _, err := searchSearxng(client, "http://localhost:8080", "", "q", 5); err != nil {
				t.Fatal(err)
			}
			if _, ok := client.last.Headers["Authorization"]; ok {
				t.Fatal("no key means no Authorization header")
			}
		})
		tw(t, fIndex, "falls back to config apiKeys.searxng when env is unset", func(t *testing.T) {
			cfg := config{APIKeys: map[string]string{"searxng": "from-config"}}
			eq(t, resolveProviderAPIKey("searxng", cfg, noEnv), "from-config", "config key")
		})
		tw(t, fIndex, "returns no-results envelope on empty results array", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{}`}
			res, err := searchSearxng(client, "http://localhost:8080", "", "q", 5)
			if err != nil {
				t.Fatal(err)
			}
			eq(t, res.Results, []searchResult{}, "an empty list, never nil")
		})
		tw(t, fIndex, "--show surfaces the resolved searxng URL and its source (env)", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			env := envMap(map[string]string{searxngURLEnvVar: "http://env:8080"})
			msg := strings.Join(showConfigLines(config{}, env, interceptorsConfig{}, false, ""), "\n")
			eq(t, strings.Contains(msg, "  searxng url: http://env:8080 (source: env)"), true, "env line")
		})
		tw(t, fIndex, "--show surfaces the resolved searxng URL and its source (config)", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			cfg := config{BaseURLs: map[string]string{"searxng": "http://config:8080"}}
			msg := strings.Join(showConfigLines(cfg, noEnv, interceptorsConfig{}, false, ""), "\n")
			if !strings.Contains(msg, "  searxng url: http://config:8080 (source: config)") {
				t.Fatalf("the config line is missing, got:\n%s", msg)
			}
		})
		tw(t, fIndex, "--show surfaces the resolved searxng URL and its source (default)", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			msg := strings.Join(showConfigLines(config{}, noEnv, interceptorsConfig{}, false, ""), "\n")
			if !strings.Contains(msg, "  searxng url: http://localhost:8080 (source: default)") {
				t.Fatalf("the default line is missing, got:\n%s", msg)
			}
		})
	})
	t.Run("ollama", func(t *testing.T) {
		tw(t, fIndex, "sends Bearer Authorization only when an API key is configured", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{"results":[]}`}
			if _, err := searchOllama(client, "http://localhost:11434", "k", "q", 5, true); err != nil {
				t.Fatal(err)
			}
			eq(t, client.last.Headers["Authorization"], "Bearer k", "bearer sent")
		})
		tw(t, fIndex, "omits Authorization when no API key is configured", func(t *testing.T) {
			client := &fakeHTTP{status: 200, body: `{"results":[]}`}
			if _, err := searchOllama(client, "http://localhost:11434", "", "q", 5, true); err != nil {
				t.Fatal(err)
			}
			if _, ok := client.last.Headers["Authorization"]; ok {
				t.Fatal("no key means no Authorization header")
			}
		})
		tw(t, fIndex, "URL cancel (undefined) leaves config untouched", func(t *testing.T) {
			ui := &scriptedUI{answers: []userInput{cancelled()}}
			before := config{BaseURLs: map[string]string{"ollama": "http://kept:11434"}, Provider: "ollama"}
			if _, ok := configureOllama(ui, providerConfigCurrent{BaseURL: before.BaseURLs["ollama"], HasBaseURL: true}); ok {
				t.Fatal("a cancel stops the flow")
			}
			eq(t, ui.asked, 1, "the key prompt is never reached")
		})
		tw(t, fIndex, "keeps existing URL and key when both inputs are empty", func(t *testing.T) {
			ui := &scriptedUI{answers: []userInput{typed(""), typed("")}}
			change, ok := configureOllama(ui, providerConfigCurrent{
				BaseURL: "http://kept:11434", HasBaseURL: true, APIKey: "kept", HasAPIKey: true,
			})
			if !ok {
				t.Fatal("the flow must complete")
			}
			eq(t, change.BaseURL, "http://kept:11434", "kept url")
			eq(t, change.APIKey, "kept", "kept key")
		})
	})
}
