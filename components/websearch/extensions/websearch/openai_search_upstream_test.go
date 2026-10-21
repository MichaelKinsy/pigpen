package websearch

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// Twins of the OpenAI and auto-search cases of search-providers.test.mjs (lines 932-1420). The original runs each in a child
// process with a replaced fetch; here the transport and the extension context are fakes.

func codexModel() map[string]any {
	return map[string]any{"provider": "openai-codex", "id": "gpt-5.6-terra"}
}

func fullOpts(p ProviderSelection, n *float64, recency string) FullSearchOptions {
	o := FullSearchOptions{Provider: p}
	o.NumResults, o.RecencyFilter = n, recency
	return o
}

func codexCtx(model map[string]any) ExtensionContext {
	return &fakeExt{model: model, reg: &fakeRegistry{models: []map[string]any{codexModel()}, auth: func(map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true, "apiKey": "codex-token", "headers": map[string]any{}}, nil
	}}}
}

func exaSSEFree() netReply {
	return exaMCPText("Title: Exa Fallback\nURL: https://exa.example/fallback\nText: Exa after Codex failure")
}

func TestUpstream_search_providers_openai(t *testing.T) {
	const F = "search-providers"
	const codexURL = "https://chatgpt.com/backend-api/codex/responses"
	ok := func(text string) string { return hostedBody(text) }

	tw(t, F, "OpenAI search requires web_search and maps domain filters", func(t *testing.T) {
		routingConfig(t, `{}`, map[string]string{"OPENAI_API_KEY": "sk-test-key"})
		fn := useNet(t, func(netCall) netReply {
			return reply(200, `{"output":[{"type":"web_search_call","action":{"sources":[{"title":"OpenAI Blog","url":"https://openai.com/blog?utm_source=openai"}]}},`+
				`{"type":"message","content":[{"type":"output_text","text":"Answer from the web","annotations":[{"type":"url_citation","start_index":0,"end_index":6,"url":"https://openai.com/docs?utm_source=openai","title":"OpenAI Docs"}]}]}]}`)
		})
		n := 3.0
		r, err := SearchWithOpenAI(bg(), "latest docs", SearchOptions{DomainFilter: []string{"https://openai.com/docs", "-reddit.com"}, NumResults: &n})
		noErr(t, err)
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(fn.calls[0].Body), &body))
		tools, _ := body["tools"].([]any)
		filters, _ := tools[0].(map[string]any)["filters"].(map[string]any)
		inc, _ := json.Marshal(body["include"])
		flt, _ := json.Marshal(filters)
		if fn.calls[0].URL != "https://api.openai.com/v1/responses" || fn.calls[0].Header.Get("Authorization") != "Bearer sk-test-key" || body["tool_choice"] != "required" ||
			string(inc) != `["web_search_call.action.sources"]` || string(flt) != `{"allowed_domains":["openai.com"],"blocked_domains":["reddit.com"]}` ||
			r.Answer != "Answer from the web" || len(r.Results) != 2 || r.Results[0].URL != "https://openai.com/docs" || r.Results[1].URL != "https://openai.com/blog" {
			t.Fatalf("%s %s %s %+v", fn.calls[0].URL, inc, flt, r)
		}
	})

	tw(t, F, "OpenAI search uses configured Responses endpoint", func(t *testing.T) {
		routingConfig(t, `{"openaiApiKey":"sk-config-key","openaiResponsesUrl":"https://gateway.example.com/v1/responses"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, ok("gateway answer")) })
		one := 1.0
		r, err := SearchWithOpenAI(bg(), "gateway docs", SearchOptions{NumResults: &one})
		noErr(t, err)
		if fn.calls[0].URL != "https://gateway.example.com/v1/responses" || fn.calls[0].Header.Get("Authorization") != "Bearer sk-config-key" || r.Answer != "gateway answer" {
			t.Fatalf("%s %v %q", fn.calls[0].URL, fn.calls[0].Header, r.Answer)
		}
	})

	tw(t, F, "auto search prefers Codex-backed OpenAI search when the selected model is openai-codex", func(t *testing.T) {
		routingConfig(t, `{}`, nil)
		fn := useNet(t, func(c netCall) netReply {
			if c.URL != codexURL {
				return netReply{Err: errors.New("Expected Codex search first, got " + c.URL)}
			}
			return reply(200, ok("codex search answer"))
		})
		r, err := Search(withExt(codexCtx(codexModel())), "current model search", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "openai" || r.Answer != "codex search answer" || !eqStrings(fn.urls(), []string{codexURL}) {
			t.Fatalf("%+v %v", r, fn.urls())
		}
	})

	tw(t, F, "auto search keeps selected Codex-backed OpenAI for result counts and recency filters", func(t *testing.T) {
		routingConfig(t, `{}`, nil)
		useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == codexURL:
				return reply(200, ok("codex option answer"))
			case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
				return exaSSEFree()
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		twenty := 20.0
		r, err := Search(withExt(codexCtx(codexModel())), "current model search", fullOpts(Auto, &twenty, "week"))
		noErr(t, err)
		if r.Provider != "openai" || r.Answer != "codex option answer" {
			t.Fatalf("%+v", r)
		}
	})

	tw(t, F, "auto search falls through to Exa when selected Codex-backed OpenAI fails", func(t *testing.T) {
		routingConfig(t, `{}`, nil)
		fn := useNet(t, func(c netCall) netReply {
			switch {
			case c.URL == codexURL:
				return reply(503, "Codex unavailable")
			case strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp"):
				return exaSSEFree()
			}
			return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
		})
		twenty := 20.0
		r, err := Search(withExt(codexCtx(codexModel())), "current model search", fullOpts(Auto, &twenty, "week"))
		noErr(t, err)
		urls := fn.urls()
		if r.Provider != "exa" || !strings.Contains(r.Answer, "Exa after Codex failure") || urls[0] != codexURL || !strings.HasPrefix(urls[1], "https://mcp.exa.ai/mcp") {
			t.Fatalf("%+v %v", r, urls)
		}
	})

	tw(t, F, "auto search uses Exa before OpenAI when the selected model is not openai-codex", func(t *testing.T) {
		routingConfig(t, `{}`, nil)
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.openai.com/") || strings.HasPrefix(c.URL, "https://chatgpt.com/") {
				return netReply{Err: errors.New("OpenAI must not run before Exa for a non-Codex selected model")}
			}
			if !strings.HasPrefix(c.URL, "https://mcp.exa.ai/mcp") {
				return netReply{Err: errors.New("Unexpected fetch " + c.URL)}
			}
			return exaMCPText("Title: Exa Source\nURL: https://exa.example/source\nText: Exa selected first")
		})
		r, err := Search(withExt(codexCtx(map[string]any{"provider": "openai", "id": "gpt-5.6-terra"})), "current model search", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "exa" || !strings.Contains(r.Answer, "Exa selected first") || !strings.HasPrefix(fn.urls()[0], "https://mcp.exa.ai/mcp") {
			t.Fatalf("%+v %v", r, fn.urls())
		}
	})

	tw(t, F, "OpenAI search falls back to API key when model registry cannot enumerate", func(t *testing.T) {
		routingConfig(t, `{}`, map[string]string{"OPENAI_API_KEY": "sk-test-key"})
		fn := useNet(t, func(netCall) netReply { return reply(200, ok("fallback answer")) })
		ec := &fakeExt{reg: &fakeRegistry{getAll: errors.New("registry cannot enumerate")}}
		one := 1.0
		r, err := SearchWithOpenAI(withExt(ec), "fallback docs", SearchOptions{NumResults: &one})
		noErr(t, err)
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(fn.calls[0].Body), &body))
		if r.Answer != "fallback answer" || body["model"] != "gpt-5.6-terra" {
			t.Fatalf("%q %v", r.Answer, body["model"])
		}
	})

	tw(t, F, "OpenAI search uses configured model with selected registry auth", func(t *testing.T) {
		routingConfig(t, `{"openaiSearchModel":"gateway-search-model"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, ok("registry answer")) })
		reg := &fakeRegistry{models: []map[string]any{{"provider": "openai", "id": "gpt-5.9"}, {"provider": "openai", "id": "gpt-5.10-pro"}, {"provider": "openai", "id": "gpt-5.10"}},
			auth: func(map[string]any) (map[string]any, error) {
				return map[string]any{"ok": true, "apiKey": "registry-key", "headers": map[string]any{"X-Registry": "yes"}}, nil
			}}
		one := 1.0
		r, err := SearchWithOpenAI(withExt(&fakeExt{reg: reg}), "registry docs", SearchOptions{NumResults: &one})
		noErr(t, err)
		var body map[string]any
		noErr(t, json.Unmarshal([]byte(fn.calls[0].Body), &body))
		if r.Answer != "registry answer" || body["model"] != "gateway-search-model" || len(reg.selected) == 0 || reg.selected[len(reg.selected)-1] != "openai/gpt-5.10" {
			t.Fatalf("%q %v %v", r.Answer, body["model"], reg.selected)
		}
	})

	tw(t, F, "OpenAI search honors configured provider priority", func(t *testing.T) {
		routingConfig(t, `{"openaiSearchProviders":["unregistered-provider","openai-codex-work","openai-codex"]}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, ok("second account answer")) })
		reg := &fakeRegistry{models: []map[string]any{{"provider": "openai-codex", "id": "gpt-5.6-terra"}, {"provider": "openai-codex-work", "id": "gpt-5.6-terra"}},
			auth: func(m map[string]any) (map[string]any, error) {
				return map[string]any{"ok": true, "apiKey": jsString(m["provider"]) + "-key", "headers": map[string]any{}}, nil
			}}
		one := 1.0
		r, err := SearchWithOpenAI(withExt(&fakeExt{reg: reg}), "priority docs", SearchOptions{NumResults: &one})
		noErr(t, err)
		if r.Answer != "second account answer" || fn.calls[0].Header.Get("Authorization") != "Bearer openai-codex-work-key" {
			t.Fatalf("%q %v", r.Answer, fn.calls[0].Header)
		}
	})

	tw(t, F, "OpenAI search rejects invalid openaiSearchProviders", func(t *testing.T) {
		routingConfig(t, `{"openaiSearchProviders":["openai-codex",""]}`, map[string]string{"OPENAI_API_KEY": "sk-must-not-be-billed"})
		fn := useNet(t, func(netCall) netReply { return reply(200, "{}") })
		reg := &fakeRegistry{models: []map[string]any{codexModel()}, auth: func(map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true, "apiKey": "registry-key", "headers": map[string]any{}}, nil
		}}
		one := 1.0
		for label, ctx := range map[string]ExtensionContext{"withContext": &fakeExt{reg: reg}, "withoutContext": nil} {
			c := bg()
			if ctx != nil {
				c = withExt(ctx)
			}
			_, err := SearchWithOpenAI(c, "invalid config", SearchOptions{NumResults: &one})
			if err == nil || !regexp.MustCompile(`openaiSearchProviders`).MatchString(err.Error()) {
				t.Fatalf("%s: %v", label, err)
			}
		}
		if fn.count() != 0 {
			t.Fatalf("%d fetches", fn.count())
		}
	})
}
