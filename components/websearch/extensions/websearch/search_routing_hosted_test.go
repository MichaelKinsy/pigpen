package websearch

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// Twins of the xAI 403 and current-model Hosted Search cases of search-routing.test.mjs. The original runs each in a
// child process with a replaced fetch; here the transport is replaced and the extension context is a fake.

func routingConfig(t *testing.T, cfg string, env map[string]string) {
	t.Helper()
	_, dir := isolate(t)
	writeConfig(t, dir, cfg)
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func hostedModel(provider, api, id, base string) map[string]any {
	return map[string]any{"provider": provider, "api": api, "id": id, "baseUrl": base}
}

func hostedCtx(model map[string]any) ExtensionContext {
	return &fakeExt{model: model, reg: &fakeRegistry{auth: func(m map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true, "apiKey": jsString(m["id"]) + "-key", "headers": map[string]any{"X-Current-Model": "yes"}}, nil
	}}}
}

func fixedAuthCtx(model map[string]any, key string) ExtensionContext {
	return &fakeExt{model: model, reg: &fakeRegistry{auth: func(map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true, "apiKey": key}, nil
	}}}
}

const hostedAnswer = `{"output":[{"type":"web_search_call","action":{"sources":[]}},{"type":"message","content":[{"type":"output_text","text":"%s"}]}]}`

func hostedBody(answer string) string { return strings.Replace(hostedAnswer, "%s", answer, 1) }

func TestUpstream_search_routing_hosted(t *testing.T) {
	const F = "search-routing"
	const openaiURL = "https://api.openai.com/v1/responses"
	const tavilyURL = "https://api.tavily.com/search"
	hostedCfg := func(fallback string) string {
		return `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":true,"fallbackOn":` + fallback + `}}`
	}

	tw(t, F, "configured routing falls back from an xAI 403 quota-exhausted response", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["xai","tavily"],"fallbackOn":["quota"]}}`, map[string]string{"XAI_API_KEY": "xai-test-key", "TAVILY_API_KEY": "tavily-test-key"})
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case "https://api.x.ai/v1/responses":
				return reply(403, "quota exhausted")
			case tavilyURL:
				return jsonReply(map[string]any{"answer": "Tavily fallback answer", "results": []any{}})
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		r, err := Search(bg(), "xai quota route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "tavily" || r.Answer != "Tavily fallback answer" || !eqStrings(fn.urls(), []string{"https://api.x.ai/v1/responses", tavilyURL}) {
			t.Fatalf("%s %q %v", r.Provider, r.Answer, fn.urls())
		}
	})

	tw(t, F, "configured routing keeps an ordinary xAI 403 response classified as auth", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["xai","tavily"],"fallbackOn":["quota"]}}`, map[string]string{"XAI_API_KEY": "xai-test-key", "TAVILY_API_KEY": "tavily-test-key"})
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == "https://api.x.ai/v1/responses" {
				return reply(403, "invalid API key")
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		_, err := Search(bg(), "xai auth route", FullSearchOptions{Provider: Auto})
		if err == nil || !regexp.MustCompile(`(?i)xai search failed \(auth\)`).MatchString(err.Error()) || !eqStrings(fn.urls(), []string{"https://api.x.ai/v1/responses"}) {
			t.Fatalf("%v %v", err, fn.urls())
		}
	})

	tw(t, F, "current-model routing uses the official OpenAI model, auth, headers, and endpoint", func(t *testing.T) {
		routingConfig(t, hostedCfg(`["unsupported","transient","quota","network","invalid-response"]`), map[string]string{"TAVILY_API_KEY": "tavily-must-not-run"})
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == openaiURL {
				return reply(200, `{"output":[{"type":"web_search_call","action":{"sources":[{"title":"OpenAI source","url":"https://example.com/source?utm_source=openai"}]}},`+
					`{"type":"message","content":[{"type":"output_text","text":"Hosted answer","annotations":[{"type":"url_citation","url":"https://example.com/source?utm_source=openai","title":"OpenAI source","start_index":0,"end_index":6}]}]}]}`)
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		ctx := withExt(hostedCtx(hostedModel("openai", "openai-responses", "gpt-5.6-terra", "https://api.openai.com/v1")))
		r, err := Search(ctx, "official hosted search", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(fn.calls[0].Body), &body))
		if r.Provider != "openai" || r.Answer != "Hosted answer" || len(r.Results) != 1 || r.Results[0].URL != "https://example.com/source" ||
			!eqStrings(fn.urls(), []string{openaiURL}) || body["model"] != "gpt-5.6-terra" ||
			fn.calls[0].Header.Get("Authorization") != "Bearer gpt-5.6-terra-key" || fn.calls[0].Header.Get("X-Current-Model") != "yes" {
			t.Fatalf("%+v %v %v", r, fn.urls(), fn.calls[0].Header)
		}
	})

	tw(t, F, "non-official and non-GPT current models skip automatic Hosted Search", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":true,"fallbackOn":["unsupported","network"]}}`, map[string]string{"TAVILY_API_KEY": "tavily-test-key"})
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == tavilyURL {
				return jsonReply(map[string]any{"answer": "Tavily answer", "results": []any{}})
			}
			return netReply{Err: errors.New("Hosted OpenAI must not run: " + c.URL)}
		})
		for _, m := range []map[string]any{
			hostedModel("openai", "openai-responses", "gpt-5.6-sol", "https://ai.feei.cn/v1"),
			hostedModel("openai", "openai-responses", "grok-4.6", "https://api.openai.com/v1"),
		} {
			r, err := Search(withExt(fixedAuthCtx(m, "must-not-be-used")), jsString(m["id"]), FullSearchOptions{Provider: Auto})
			noErr(t, err)
			if r.Provider != "tavily" {
				t.Fatalf("%s", r.Provider)
			}
		}
		if !eqStrings(fn.urls(), []string{tavilyURL, tavilyURL}) {
			t.Fatalf("%v", fn.urls())
		}
	})

	tw(t, F, "Codex current models use the official Codex Responses search endpoint", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":true,"fallbackOn":["unsupported","network"]}}`, map[string]string{"TAVILY_API_KEY": "tavily-must-not-run"})
		const codexURL = "https://chatgpt.com/backend-api/codex/responses"
		fn := useNet(t, func(c netCall) netReply {
			if c.URL != codexURL {
				return netReply{Err: errors.New("Tavily must not run: " + c.URL)}
			}
			return reply(200, `{"output":[{"type":"web_search_call","action":{"sources":[{"title":"Codex source","url":"https://example.com/codex"}]}},{"type":"message","content":[{"type":"output_text","text":"Codex hosted answer"}]}]}`)
		})
		ctx := withExt(hostedCtx(hostedModel("openai-codex", "openai-codex-responses", "gpt-5.6-sol", "https://chatgpt.com/backend-api")))
		r, err := Search(ctx, "official Codex hosted search", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(fn.calls[0].Body), &body))
		if r.Provider != "openai" || r.Answer != "Codex hosted answer" || !eqStrings(fn.urls(), []string{codexURL}) || body["model"] != "gpt-5.6-sol" ||
			fn.calls[0].Header.Get("Authorization") != "Bearer gpt-5.6-sol-key" || fn.calls[0].Header.Get("X-Current-Model") != "yes" {
			t.Fatalf("%+v %v", r, fn.calls[0].Header)
		}
	})

	tw(t, F, "explicit OpenAI provider bypasses current-model restrictions", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":true,"fallbackOn":["unsupported","network"]}}`, map[string]string{"OPENAI_API_KEY": "explicit-openai-key"})
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == openaiURL {
				return reply(200, hostedBody("Explicit OpenAI answer"))
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		ec := &fakeExt{model: hostedModel("openai", "openai-responses", "gpt-5.6-sol", "https://ai.feei.cn/v1"), reg: &fakeRegistry{}}
		r, err := Search(withExt(ec), "explicit provider", FullSearchOptions{Provider: named("openai")})
		noErr(t, err)
		if r.Provider != "openai" || r.Answer != "Explicit OpenAI answer" || !eqStrings(fn.urls(), []string{openaiURL}) {
			t.Fatalf("%+v %v", r, fn.urls())
		}
	})

	officialCtx := func() ExtensionContext {
		return fixedAuthCtx(hostedModel("openai", "openai-responses", "gpt-5.6", "https://api.openai.com/v1"), "hosted-key")
	}

	tw(t, F, "unsupported Hosted Search errors fall back to Tavily", func(t *testing.T) {
		routingConfig(t, hostedCfg(`["unsupported"]`), map[string]string{"TAVILY_API_KEY": "tavily-test-key"})
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case openaiURL:
				return reply(400, "This model does not support web_search")
			case tavilyURL:
				return jsonReply(map[string]any{"answer": "Fallback answer", "results": []any{}})
			}
			return netReply{Err: errors.New("Unexpected fetch: " + c.URL)}
		})
		r, err := Search(withExt(officialCtx()), "unsupported hosted search", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "tavily" || r.Answer != "Fallback answer" || !eqStrings(fn.urls(), []string{openaiURL, tavilyURL}) {
			t.Fatalf("%+v %v", r, fn.urls())
		}
	})

	tw(t, F, "Hosted authentication errors do not silently fall back", func(t *testing.T) {
		routingConfig(t, hostedCfg(`["unsupported","transient","quota","network","invalid-response"]`), map[string]string{"TAVILY_API_KEY": "tavily-must-not-run"})
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == openaiURL {
				return reply(401, "invalid API key")
			}
			return netReply{Err: errors.New("Tavily must not run")}
		})
		_, err := Search(withExt(officialCtx()), "auth failure", FullSearchOptions{Provider: Auto})
		if err == nil || !regexp.MustCompile(`(?i)openai search failed \(auth\)`).MatchString(err.Error()) || !eqStrings(fn.urls(), []string{openaiURL}) {
			t.Fatalf("%v %v", err, fn.urls())
		}
	})

	tw(t, F, "a Hosted response without web_search_call is invalid and can fall back", func(t *testing.T) {
		routingConfig(t, hostedCfg(`["invalid-response"]`), map[string]string{"TAVILY_API_KEY": "tavily-test-key"})
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case openaiURL:
				return reply(200, `{"output":[{"type":"message","content":[{"type":"output_text","text":"hallucinated answer"}]}]}`)
			case tavilyURL:
				return jsonReply(map[string]any{"answer": "Validated fallback", "results": []any{}})
			}
			return netReply{Err: errors.New("Unexpected fetch: " + c.URL)}
		})
		r, err := Search(withExt(officialCtx()), "missing search call", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "tavily" || r.Answer != "Validated fallback" || !eqStrings(fn.urls(), []string{openaiURL, tavilyURL}) {
			t.Fatalf("%+v %v", r, fn.urls())
		}
	})

	tw(t, F, "useCurrentModel is strictly validated", func(t *testing.T) {
		routingConfig(t, `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":"yes","fallbackOn":["network"]}}`, nil)
		_, err := Search(bg(), "invalid current model config", FullSearchOptions{Provider: Auto})
		if err == nil || !regexp.MustCompile(`searchRouting\.useCurrentModel .*boolean`).MatchString(err.Error()) {
			t.Fatalf("%v", err)
		}
	})
}
