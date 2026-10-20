package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of the provider-URL-reuse half of test/openai-alpha-search.test.mjs.

var (
	gatewayModel = func(baseURL string) map[string]any { return map[string]any{"baseUrl": baseURL} }

	providerPaths = [][2]string{
		{"https://gateway.example", "/v1/responses"},
		{"https://gateway.example/", "/v1/responses"},
		{"https://gateway.example/v1", "/v1/responses"},
		{"https://gateway.example/team/v1/", "/team/v1/responses"},
		{"https://gateway.example/custom", "/custom/responses"},
		{"https://gateway.example/v1/responses", "/v1/responses"},
		{"https://gateway.example/v1/responses/", "/v1/responses"},
	}
)

func payloadFor(alpha bool) any {
	if alpha {
		return alphaPayload
	}
	return legacyPayload
}

func suffixFor(alpha bool) string {
	if alpha {
		return "alpha/search"
	}
	return "responses"
}

// providerCandidates is the upstream fixture of two Pi providers with their own credentials.
func providerCandidates() oaScenario {
	return oaScenario{
		registry: true,
		models: []map[string]any{
			{"id": "gpt-5.6-luna", "provider": "first", "api": "openai-responses", "baseUrl": "https://first-model.example/v1"},
			{"id": "gpt-5.6-terra", "provider": "second", "api": "openai-responses", "baseUrl": "https://second-model.example/v1"},
		},
		authByProvider: map[string]map[string]any{
			"first":  {"ok": true, "apiKey": "first-provider-key", "headers": map[string]any{"X-Selected": "first"}},
			"second": {"ok": true, "apiKey": "second-provider-key", "baseUrl": "https://second-auth.example/team/v1", "headers": map[string]any{"X-Selected": "second"}},
		},
		config: map[string]any{"openaiSearchProviders": []string{"first", "second"}, "openaiUseProviderBaseUrl": true},
	}
}

func withConfig(sc oaScenario, extra map[string]any) oaScenario {
	merged := map[string]any{}
	for k, v := range sc.config {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	sc.config = merged
	return sc
}

func TestUpstream_openai_alpha_search_provider_url(t *testing.T) {
	const f = "openai-alpha-search"

	tw(t, f, "provider URL reuse completes ${baseUrl} (alpha: ${alpha})", func(t *testing.T) {
		for _, alpha := range []bool{false, true} {
			for _, p := range providerPaths {
				out := runOpenAI(t, oaScenario{registry: true, model: gatewayModel(p[0]), config: map[string]any{"openaiUseProviderBaseUrl": true, "openaiUseAlphaSearch": alpha}, payload: payloadFor(alpha)})
				out.mustOK(t)
				path := p[1]
				if alpha {
					path = strings.TrimSuffix(path, "/responses") + "/alpha/search"
				}
				if out.requests[0].URL != "https://gateway.example"+path || headerOf(out.requests[0], "Authorization") != "Bearer registry-test-key" {
					t.Fatalf("%s alpha %v: %s", p[0], alpha, out.requests[0].URL)
				}
			}
		}
	})

	tw(t, f, "provider URL reuse skips ${reason} and keeps the next candidate's credentials (alpha: ${alpha})", func(t *testing.T) {
		for _, alpha := range []bool{false, true} {
			for _, tc := range []struct {
				reason, modelBase, authBase string
				dropBase                    bool
			}{
				{"missing model URL", "", "", true},
				{"invalid model URL", "/v1", "", false},
				{"invalid auth URL", "https://first-model.example/v1", "invalid", false},
			} {
				sc := withConfig(providerCandidates(), map[string]any{"openaiUseAlphaSearch": alpha})
				first := map[string]any{"id": "gpt-5.6-luna", "provider": "first", "api": "openai-responses", "baseUrl": tc.modelBase}
				if tc.dropBase {
					delete(first, "baseUrl")
				}
				sc.models = []map[string]any{first, sc.models[1]}
				if tc.authBase != "" {
					sc.authByProvider["first"]["baseUrl"] = tc.authBase
				}
				sc.payload = payloadFor(alpha)
				out := runOpenAI(t, sc)
				out.mustOK(t)
				req := out.requests[0]
				if !eqStrings(out.authProviders, []string{"first", "second"}) || len(out.requests) != 1 ||
					req.URL != "https://second-auth.example/team/v1/"+suffixFor(alpha) ||
					headerOf(req, "Authorization") != "Bearer second-provider-key" || headerOf(req, "X-Selected") != "second" || req.Body["model"] != "gpt-5.6-terra" {
					t.Fatalf("%s alpha %v: %v %v %s", tc.reason, alpha, out.authProviders, out.urls(), req.Body)
				}
				sc.availability = true
				avail := runOpenAI(t, sc)
				avail.mustOK(t)
				if avail.result != true || !eqStrings(avail.authProviders, []string{"first", "second"}) {
					t.Fatalf("%s: availability %v %v", tc.reason, avail.result, avail.authProviders)
				}
				avail.wantNoRequests(t)
			}
		}
	})

	tw(t, f, "provider URL reuse checks all invalid candidates without standalone key fallback (alpha: ${alpha})", func(t *testing.T) {
		for _, alpha := range []bool{false, true} {
			sc := withConfig(providerCandidates(), map[string]any{"openaiUseAlphaSearch": alpha})
			sc.models = []map[string]any{{"id": "gpt-5.6-luna", "provider": "first", "api": "openai-responses"}, sc.models[1]}
			sc.authByProvider["second"]["baseUrl"] = "ftp://invalid.example/v1"
			out := runOpenAI(t, sc)
			out.wantNoRequests(t)
			if !eqStrings(out.authProviders, []string{"first", "second"}) {
				t.Fatal(out.authProviders)
			}
			wantErr(t, out.err, `openaiUseProviderBaseUrl requires an absolute http\(s\) provider baseUrl`)
			sc.availability = true
			avail := runOpenAI(t, sc)
			avail.mustOK(t)
			if avail.result != false || !eqStrings(avail.authProviders, []string{"first", "second"}) {
				t.Fatalf("availability %v %v", avail.result, avail.authProviders)
			}
			avail.wantNoRequests(t)
		}
	})

	tw(t, f, "provider candidates retain the existing origin guard with URL reuse ${flag}", func(t *testing.T) {
		for _, flag := range []any{absent{}, false} {
			out := runOpenAI(t, withConfig(providerCandidates(), map[string]any{"openaiUseProviderBaseUrl": flag}))
			if !eqStrings(out.authProviders, []string{"first"}) {
				t.Fatal(out.authProviders)
			}
			out.wantNoRequests(t)
			wantErr(t, out.err, `custom baseUrl`)
		}
	})

	tw(t, f, "explicit endpoint keeps the first credential candidate even with an invalid provider URL", func(t *testing.T) {
		sc := withConfig(providerCandidates(), map[string]any{"openaiResponsesUrl": "https://explicit.example/v1/responses"})
		sc.models = []map[string]any{{"id": "gpt-5.6-luna", "provider": "first", "api": "openai-responses", "baseUrl": "invalid"}, sc.models[1]}
		out := runOpenAI(t, sc)
		out.mustOK(t)
		if !eqStrings(out.authProviders, []string{"first"}) || len(out.requests) != 1 ||
			out.requests[0].URL != "https://explicit.example/v1/alpha/search" || headerOf(out.requests[0], "Authorization") != "Bearer first-provider-key" {
			t.Fatalf("%v %v", out.authProviders, out.urls())
		}
	})

	tw(t, f, "provider URL reuse does not retry a failed request with later credential candidates", func(t *testing.T) {
		sc := providerCandidates()
		sc.status = 503
		out := runOpenAI(t, sc)
		if !eqStrings(out.authProviders, []string{"first"}) || len(out.requests) != 1 ||
			out.requests[0].URL != "https://first-model.example/v1/alpha/search" || headerOf(out.requests[0], "Authorization") != "Bearer first-provider-key" {
			t.Fatalf("%v %v", out.authProviders, out.urls())
		}
		wantErr(t, out.err, `API error 503`)
	})

	tw(t, f, "provider URL reuse stays off for ${flag}", func(t *testing.T) {
		for _, flag := range []any{absent{}, false} {
			out := runOpenAI(t, oaScenario{registry: true, model: gatewayModel("https://gateway.example/v1"), config: map[string]any{"openaiUseProviderBaseUrl": flag}})
			out.wantNoRequests(t)
			wantErr(t, out.err, `custom baseUrl`)
		}
	})

	tw(t, f, "explicit endpoint ignores provider URL switch entirely (alpha: ${alpha})", func(t *testing.T) {
		for _, alpha := range []bool{false, true} {
			for _, flag := range []any{true, false, "ignored", nil} {
				out := runOpenAI(t, oaScenario{registry: true, model: gatewayModel("invalid provider URL"),
					config:  map[string]any{"openaiResponsesUrl": "https://explicit.example/prefix/responses", "openaiUseProviderBaseUrl": flag, "openaiUseAlphaSearch": alpha},
					payload: payloadFor(alpha)})
				out.mustOK(t)
				if out.requests[0].URL != "https://explicit.example/prefix/"+suffixFor(alpha) {
					t.Fatalf("alpha %v flag %v: %s", alpha, flag, out.requests[0].URL)
				}
			}
		}
	})

	tw(t, f, "provider URL reuse prefers auth-resolved origin and preserves port and query", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, model: gatewayModel("https://model.example/v1"), authBaseURL: "http://auth.example:8921/team/v1?version=1", config: map[string]any{"openaiUseProviderBaseUrl": true}})
		out.mustOK(t)
		if out.requests[0].URL != "http://auth.example:8921/team/v1/alpha/search?version=1" {
			t.Fatal(out.requests[0].URL)
		}
	})

	tw(t, f, "provider URL reuse follows updated model registry URLs without caching the endpoint", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, calls: 2, model: gatewayModel("https://first.example/v1"), nextBaseURL: "https://second.example/team", config: map[string]any{"openaiUseProviderBaseUrl": true}})
		out.mustOK(t)
		if !eqStrings(out.urls(), []string{"https://first.example/v1/alpha/search", "https://second.example/team/alpha/search"}) {
			t.Fatal(out.urls())
		}
	})

	tw(t, f, "provider URL reuse validates ${JSON.stringify(value)} when not overridden", func(t *testing.T) {
		for _, value := range []any{"true", 1, nil} {
			out := runOpenAI(t, oaScenario{config: map[string]any{"openaiUseProviderBaseUrl": value}})
			out.wantNoRequests(t)
			wantErr(t, out.err, `openaiUseProviderBaseUrl.*boolean`)
		}
	})

	tw(t, f, "provider URL reuse fails closed and reports unavailable: ${JSON.stringify(scenario)}", func(t *testing.T) {
		for _, sc := range []oaScenario{
			{},
			{registry: true, key: strp("")},
			{registry: true, missingBaseURL: true},
			{registry: true, model: gatewayModel("")},
			{registry: true, model: gatewayModel("/v1")},
			{registry: true, model: gatewayModel("ftp://gateway.example/v1")},
			{registry: true, authBaseURL: "invalid", model: gatewayModel("https://model.example/v1")},
		} {
			sc.config = map[string]any{"openaiUseProviderBaseUrl": true}
			out := runOpenAI(t, sc)
			out.wantNoRequests(t)
			wantErr(t, out.err, `(?i)configuration.*openaiUseProviderBaseUrl.*baseUrl`)
			sc.availability = true
			avail := runOpenAI(t, sc)
			avail.mustOK(t)
			if avail.result != false {
				t.Fatalf("%+v: available", sc)
			}
		}
	})

	tw(t, f, "provider URL reuse makes a configured gateway available", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, availability: true, model: gatewayModel("https://gateway.example/v1"), config: map[string]any{"openaiUseProviderBaseUrl": true}})
		out.mustOK(t)
		if out.result != true {
			t.Fatal(out.result)
		}
		out.wantNoRequests(t)
	})

	tw(t, f, "provider URL reuse preserves Codex metadata without overriding ${baseUrl} (alpha: ${alpha})", func(t *testing.T) {
		for _, alpha := range []bool{false, true} {
			for _, baseURL := range []string{"https://chatgpt.com/backend-api", "https://gateway.example/team/v1"} {
				out := runOpenAI(t, oaScenario{registry: true, key: strp(codexToken()),
					model:   map[string]any{"provider": "openai-codex", "api": "openai-codex-responses", "baseUrl": baseURL},
					config:  map[string]any{"openaiUseProviderBaseUrl": true, "openaiUseAlphaSearch": alpha},
					payload: payloadFor(alpha)})
				out.mustOK(t)
				want := baseURL
				if strings.Contains(baseURL, "chatgpt.com") {
					want += "/codex"
				}
				want += "/" + suffixFor(alpha)
				if out.requests[0].URL != want || headerOf(out.requests[0], "chatgpt-account-id") != "test-account" {
					t.Fatalf("%s alpha %v: %s %v", baseURL, alpha, out.requests[0].URL, out.requests[0].Header)
				}
			}
		}
	})

	tw(t, f, "explicit endpoint keeps existing Codex selection when provider URL switch is ignored", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, model: map[string]any{"provider": "openai-codex", "api": "openai-codex-responses", "baseUrl": "https://provider.example/v1"},
			config: map[string]any{"openaiResponsesUrl": "https://explicit.example/v1/responses", "openaiUseProviderBaseUrl": true}})
		out.mustOK(t)
		if out.requests[0].URL != "https://chatgpt.com/backend-api/codex/alpha/search" {
			t.Fatal(out.requests[0].URL)
		}
	})

	tw(t, f, "provider URL reuse does not broaden current-model eligibility", func(t *testing.T) {
		rejected := runOpenAI(t, oaScenario{current: true, model: gatewayModel("https://gateway.example/v1"), config: map[string]any{"openaiUseProviderBaseUrl": true}})
		rejected.wantNoRequests(t)
		wantErr(t, rejected.err, `not eligible`)
		official := runOpenAI(t, oaScenario{current: true, config: map[string]any{"openaiUseProviderBaseUrl": true}})
		official.mustOK(t)
		if official.requests[0].URL != "https://api.openai.com/v1/alpha/search" {
			t.Fatal(official.requests[0].URL)
		}
	})

	tw(t, f, "provider URL reuse cannot rescue invalid explicit endpoint ${JSON.stringify(endpoint)}", func(t *testing.T) {
		for _, endpoint := range []any{"", "not a URL", nil} {
			out := runOpenAI(t, oaScenario{registry: true, model: gatewayModel("https://gateway.example/v1"), config: map[string]any{"openaiResponsesUrl": endpoint, "openaiUseProviderBaseUrl": true}})
			out.wantNoRequests(t)
			wantErr(t, out.err, `openaiResponsesUrl.*absolute http`)
		}
	})

	tw(t, f, "provider URL reuse works through configured provider routing", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, route: true, model: map[string]any{"provider": "gateway", "baseUrl": "https://gateway.example"},
			config: map[string]any{"openaiSearchProviders": []string{"gateway"}, "openaiUseProviderBaseUrl": true, "searchRouting": json.RawMessage(searchRoute)}})
		out.mustOK(t)
		if out.result.(*AttributedSearchResponse).Provider != "openai" || len(out.requests) != 1 || out.requests[0].URL != "https://gateway.example/v1/alpha/search" {
			t.Fatalf("%+v %v", out.result, out.urls())
		}
	})

	tw(t, f, "provider URL reuse traverses registered web_search and content retrieval", func(t *testing.T) {
		out := runOpenAI(t, oaScenario{registry: true, tool: true, model: map[string]any{"provider": "gateway", "baseUrl": "https://gateway.example"},
			config: map[string]any{"openaiSearchProviders": []string{"gateway"}, "openaiUseProviderBaseUrl": true}})
		out.mustOK(t)
		if out.isError || !strings.Contains(out.retrieved, "Plain search output") || len(out.requests) != 2 {
			t.Fatalf("%v %q %d", out.isError, out.retrieved, len(out.requests))
		}
		for _, r := range out.requests {
			if r.URL != "https://gateway.example/v1/alpha/search" {
				t.Fatal(r.URL)
			}
		}
	})
}
