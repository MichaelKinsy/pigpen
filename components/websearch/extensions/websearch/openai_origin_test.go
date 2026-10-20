package websearch

import (
	"regexp"
	"strings"
	"testing"
)

// Twins of test/openai-origin.test.mjs and test/openai-search-opencode-headers.test.mjs.

var configPathRE = regexp.MustCompile(`in \S+web-search\.json`)

func TestUpstream_openai_origin(t *testing.T) {
	const f = "openai-origin"
	const official = "https://api.openai.com/v1/responses"
	const gateway = "http://127.0.0.1:8921/v1"

	type originCase struct {
		name                                        string
		provider, baseURL, authBaseURL, endpoint    string
		noBaseURL, standalone, configError, blocked bool
		expectedURL                                 string
	}
	cases := []originCase{
		{name: "invalid explicit endpoint remains an availability error", endpoint: "not a URL", configError: true},
		{name: "custom Pi base URL refuses before fetch", baseURL: gateway, blocked: true},
		{name: "auth-resolved custom URL overrides official model URL", baseURL: "https://api.openai.com/v1", authBaseURL: gateway, blocked: true},
		{name: "invalid Pi base URL refuses before fetch", baseURL: "not a URL", blocked: true},
		{name: "lookalike official host refuses before fetch", baseURL: "https://api.openai.com.example/v1", blocked: true},
		{name: "HTTP official host refuses before fetch", baseURL: "http://api.openai.com/v1", blocked: true},
		{name: "official Pi base URL keeps default", baseURL: "https://api.openai.com/v1", expectedURL: official},
		{name: "official trailing slash keeps default", baseURL: "https://api.openai.com/v1/", expectedURL: official},
		{name: "absent Pi base URL keeps default", noBaseURL: true, expectedURL: official},
		{name: "auth-resolved official URL overrides custom model URL", baseURL: gateway, authBaseURL: "https://api.openai.com/v1", expectedURL: official},
		{name: "explicit gateway full endpoint takes precedence", baseURL: gateway, endpoint: "http://127.0.0.1:8921/custom/responses?version=1", expectedURL: "http://127.0.0.1:8921/custom/responses?version=1"},
		{name: "explicit official endpoint opts in", baseURL: gateway, endpoint: official, expectedURL: official},
		{name: "standalone API key keeps default", standalone: true, expectedURL: official},
		{name: "Codex keeps its existing endpoint", provider: "openai-codex", baseURL: "https://chatgpt.com/backend-api", expectedURL: "https://chatgpt.com/backend-api/codex/responses"},
	}

	tw(t, f, "OpenAI origin: ${scenario.name}", func(t *testing.T) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				provider := c.provider
				if provider == "" {
					provider = "openai"
				}
				config := map[string]any{"openaiApiKey": "standalone-test-key"}
				if c.endpoint != "" {
					config["openaiResponsesUrl"] = c.endpoint
				}
				sc := oaScenario{
					baseConfig: config, registry: true, standalone: c.standalone, authBaseURL: c.authBaseURL,
					key:     strp("pi-gateway-test-key"),
					headers: map[string]any{"X-Gateway": "test"},
					model:   map[string]any{"id": "gpt-5.6-terra", "provider": provider, "api": "openai-responses", "baseUrl": c.baseURL},
					payload: map[string]any{"output": []any{
						map[string]any{"type": "web_search_call"},
						map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Test answer"}}},
					}},
					missingBaseURL: c.noBaseURL,
				}
				search := runOpenAI(t, sc)
				sc.availability = true
				avail := runOpenAI(t, sc)
				switch {
				case c.configError:
					search.wantNoRequests(t)
					wantErr(t, search.err, `openaiResponsesUrl.*absolute http\(s\) URL`)
					// Each run has its own agent directory: compare the messages without the path.
					if avail.err == nil || configPathRE.ReplaceAllString(avail.err.Error(), "<config>") != configPathRE.ReplaceAllString(search.err.Error(), "<config>") {
						t.Fatalf("availability error %v, search error %v", avail.err, search.err)
					}
				case c.blocked:
					search.wantNoRequests(t)
					wantErr(t, search.err, `custom baseUrl.*openaiResponsesUrl`)
					if strings.Contains(search.err.Error(), "pi-gateway-test-key") || avail.err != nil || avail.result != false {
						t.Fatalf("%v %v %v", search.err, avail.err, avail.result)
					}
				default:
					search.mustOK(t)
					wantKey := "pi-gateway-test-key"
					if c.standalone {
						wantKey = "standalone-test-key"
					}
					if avail.err != nil || avail.result != true || len(search.requests) != 1 || search.requests[0].URL != c.expectedURL ||
						oaHeaderOf(search.requests[0], "Authorization") != "Bearer "+wantKey {
						t.Fatalf("%v %v %v", avail.result, search.urls(), search.requests[0].Header)
					}
					if !c.standalone && oaHeaderOf(search.requests[0], "X-Gateway") != "test" {
						t.Fatal("the registry header was not forwarded")
					}
				}
			})
		}
	})
}

func TestUpstream_openai_search_opencode_headers(t *testing.T) {
	const f = "openai-search-opencode-headers"
	const openCodeURL = "https://opencode.ai/zen/v1/responses"
	openCodeGoModels := func() []map[string]any {
		go1 := func(id, api string) map[string]any {
			return map[string]any{"provider": "opencode-go", "id": id, "api": api, "baseUrl": "https://opencode.ai/zen/go/v1"}
		}
		return []map[string]any{
			go1("deepseek-v4-pro", "openai-completions"), go1("gpt-5.6-luna", "openai-responses"), go1("gpt-6-luna", "openai-responses"),
			go1("gpt-oss-120b", "openai-completions"), go1("gpt-realtime-2.1", "openai-responses"), go1("space-bunny-free", "openai-completions"),
		}
	}
	openAIModel := map[string]any{"provider": "openai", "id": "gpt-5.6-terra", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"}
	answer := map[string]any{"output": []any{
		map[string]any{"type": "web_search_call"},
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Search answer"}}},
	}}
	// search is runSearch of the upstream test: a Pi registry holding models, one resolved auth.
	search := func(t *testing.T, config map[string]any, models []map[string]any, mutate func(*oaScenario)) oaOut {
		sc := oaScenario{
			baseConfig: config, registry: true, models: models, key: strp("test-key"), payload: answer,
			sessionID: "session-1", headers: map[string]any{"x-existing": "kept"}, options: SearchOptions{NumResults: nf(1)},
		}
		if mutate != nil {
			mutate(&sc)
		}
		return runOpenAI(t, sc)
	}
	goConfig := func() map[string]any {
		return map[string]any{"openaiSearchProviders": []string{"opencode-go"}, "openaiUseProviderBaseUrl": true, "openaiSearchModel": "gpt-6-luna"}
	}

	tw(t, f, "OpenAI search through opencode-go keeps provider headers and picks the newest versioned GPT model", func(t *testing.T) {
		cfg := goConfig()
		delete(cfg, "openaiSearchModel")
		out := search(t, cfg, openCodeGoModels(), nil)
		out.mustOK(t)
		req := out.requests[0]
		if out.searchResponse(t).Answer != "Search answer" || req.URL != "https://opencode.ai/zen/go/v1/responses" || req.Body["model"] != "gpt-6-luna" ||
			oaHeaderOf(req, "x-existing") != "kept" || oaHeaderOf(req, "x-opencode-session") != "session-1" || oaHeaderOf(req, "x-opencode-client") != "pi" {
			t.Fatalf("%s %v %v", req.URL, req.Body["model"], req.Header)
		}
	})

	tw(t, f, "OpenAI search sends OpenCode attribution to ${name}", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			config map[string]any
			models []map[string]any
		}{
			{"an explicit OpenCode URL with OpenCode credentials", map[string]any{"openaiSearchProviders": []string{"opencode-go"}, "openaiResponsesUrl": openCodeURL, "openaiSearchModel": "gpt-6-luna"}, openCodeGoModels()},
			{"an explicit OpenCode URL with standalone credentials", map[string]any{"openaiApiKey": "standalone-key", "openaiResponsesUrl": openCodeURL}, []map[string]any{}},
			{"an explicit OpenCode URL with a non-OpenCode Pi model", map[string]any{"openaiSearchProviders": []string{"openai"}, "openaiResponsesUrl": openCodeURL}, []map[string]any{openAIModel}},
		} {
			out := search(t, c.config, c.models, nil)
			out.mustOK(t)
			if out.requests[0].URL != openCodeURL || oaHeaderOf(out.requests[0], "x-opencode-session") != "session-1" || oaHeaderOf(out.requests[0], "x-opencode-client") != "pi" {
				t.Fatalf("%s: %s %v", c.name, out.requests[0].URL, out.requests[0].Header)
			}
		}
	})

	tw(t, f, "OpenAI search omits OpenCode attribution ${name}", func(t *testing.T) {
		for _, c := range []struct {
			name, url string
			config    map[string]any
			models    []map[string]any
			mutate    func(*oaScenario)
		}{
			{"without a session id", "https://opencode.ai/zen/go/v1/responses", goConfig(), openCodeGoModels(), func(sc *oaScenario) { sc.sessionID = "" }},
			{"without an extension context", openCodeURL, map[string]any{"openaiApiKey": "standalone-key", "openaiResponsesUrl": openCodeURL}, []map[string]any{}, func(sc *oaScenario) { sc.standalone = true }},
			{"when resolved auth overrides the provider base URL", "https://resolved-gateway.example/v1/responses", goConfig(), openCodeGoModels(), func(sc *oaScenario) { sc.authBaseURL = "https://resolved-gateway.example/v1" }},
			{"to another explicit gateway, even when the registry supplies OpenCode headers", "https://other-gateway.example/v1/responses",
				map[string]any{"openaiSearchProviders": []string{"opencode-go"}, "openaiResponsesUrl": "https://other-gateway.example/v1/responses", "openaiSearchModel": "gpt-6-luna"}, openCodeGoModels(),
				func(sc *oaScenario) {
					sc.headers = map[string]any{"X-OpenCode-Session": "registry-session", "x-opencode-client": "registry-client"}
				}},
			{"after Codex credentials switch to the ChatGPT endpoint", "https://chatgpt.com/backend-api/codex/responses",
				map[string]any{"openaiSearchProviders": []string{"openai-codex"}, "openaiResponsesUrl": openCodeURL},
				[]map[string]any{{"provider": "openai-codex", "id": "gpt-5.6-terra", "api": "openai-codex-responses", "baseUrl": "https://chatgpt.com/backend-api"}}, nil},
			{"through official providers", "https://api.openai.com/v1/responses", map[string]any{}, []map[string]any{openAIModel}, nil},
		} {
			out := search(t, c.config, c.models, c.mutate)
			out.mustOK(t)
			req := out.requests[0]
			if req.URL != c.url || hasHeader(req, "x-opencode-session") || hasHeader(req, "x-opencode-client") {
				t.Fatalf("%s: %s %v", c.name, req.URL, req.Header)
			}
		}
	})

	tw(t, f, "OpenAI search strips attribution and provider auth across a cross-origin redirect", func(t *testing.T) {
		out := search(t, map[string]any{"openaiSearchProviders": []string{"opencode-go"}, "openaiResponsesUrl": openCodeURL}, openCodeGoModels(), func(sc *oaScenario) {
			sc.headers = map[string]any{"x-provider-secret": "registry-secret"}
			sc.redirectURL, sc.redirectStatus = "https://redirect-target.example/v1/responses", 302
		})
		out.mustOK(t)
		if out.searchResponse(t).Answer != "Search answer" || oaHeaderOf(out.requests[0], "x-provider-secret") != "registry-secret" || oaHeaderOf(out.requests[0], "x-opencode-session") != "session-1" {
			t.Fatalf("%v", out.requests[0].Header)
		}
		redirected := out.requests[1]
		if redirected.URL != "https://redirect-target.example/v1/responses" {
			t.Fatal(redirected.URL)
		}
		for _, h := range []string{"authorization", "x-opencode-session", "x-opencode-client", "x-provider-secret"} {
			if hasHeader(redirected, h) {
				t.Fatalf("%s was forwarded", h)
			}
		}
	})

	tw(t, f, "OpenAI search rejects an insecure explicit OpenCode URL before sending a request", func(t *testing.T) {
		out := search(t, map[string]any{"openaiApiKey": "standalone-key", "openaiResponsesUrl": "http://opencode.ai/zen/v1/responses"}, []map[string]any{}, nil)
		wantErr(t, out.err, `must use HTTPS for opencode\.ai`)
		out.wantNoRequests(t)
	})
}
