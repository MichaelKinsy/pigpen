package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Twins of provider-precedence, allowed-search-providers and the tool-level cases of
// source-check. The upstream fixtures for OpenAI, SerpBase, Serply and AnySearch become tskip
// (those providers are not ported) or run with a ported provider where the behaviour under test
// is provider-independent (adapted).

const kagiURL = "https://kagi.com/api/v1/search"
const kagiBody = `{"data":{"search":[{"title":"Kagi","url":"https://example.com/kagi","snippet":"answer"}]}}`

// providerEnv isolates the config and then sets the credentials of the case.
func providerEnv(t *testing.T, config string, env map[string]string) {
	t.Helper()
	extractEnv(t, config)
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func searchProviderNet(t *testing.T) *fakeNet {
	return useNet(t, func(c netCall) netReply {
		switch {
		case c.URL == "https://api.perplexity.ai/chat/completions":
			return reply(200, `{"choices":[{"message":{"content":"perplexity answer"}}],"citations":["https://perplexity.example/source"]}`)
		case c.URL == "https://api.tavily.com/search":
			return reply(200, tavilyBody("tavily answer", map[string]string{"title": "Tavily source", "url": "https://tavily.example/source", "content": "tavily result"}))
		case c.URL == kagiURL:
			return reply(200, kagiBody)
		}
		return netReply{Err: fmt.Errorf("Unexpected fetch: %s", c.URL)}
	})
}

func TestUpstream_provider_precedence(t *testing.T) {
	const F = "provider-precedence"
	tool := func(t *testing.T, config string, provider any) (*fakeNet, ToolOutput, error) {
		providerEnv(t, config, nil)
		ClearResults()
		t.Cleanup(ClearResults)
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		n := searchProviderNet(t)
		params := map[string]any{"query": "provider precedence", "workflow": "none"}
		if provider != nil {
			params["provider"] = provider
		}
		out, err := mustTool(t, r, "web_search").Execute(bg(), params, nil)
		return n, out, err
	}
	const base = `{"provider":"perplexity","perplexityApiKey":"perplexity-test-key","tavilyApiKey":"tavily-test-key"}`
	sorted := func(l []string) []string { c := append([]string(nil), l...); sortStrings(c); return c }
	both := []string{"https://api.perplexity.ai/chat/completions", "https://api.tavily.com/search"}

	tw(t, F, "configured provider is used when tool omits provider", func(t *testing.T) {
		n, _, err := tool(t, base, nil)
		noErr(t, err)
		wantCalls(t, n, "https://api.perplexity.ai/chat/completions")
	})
	tw(t, F, "configured provider array is used when the tool omits provider", func(t *testing.T) {
		n, _, err := tool(t, `{"provider":["tavily","perplexity"],"perplexityApiKey":"perplexity-test-key","tavilyApiKey":"tavily-test-key"}`, nil)
		noErr(t, err)
		if !reflect.DeepEqual(sorted(n.urls()), both) {
			t.Fatal(n.urls())
		}
	})
	tw(t, F, "explicit named provider overrides configured provider", func(t *testing.T) {
		n, _, err := tool(t, base, "tavily")
		noErr(t, err)
		wantCalls(t, n, "https://api.tavily.com/search")
	})
	tw(t, F, "explicit provider array overrides configured provider and runs only the selected providers", func(t *testing.T) {
		n, _, err := tool(t, base, []any{"tavily", "perplexity"})
		noErr(t, err)
		if !reflect.DeepEqual(sorted(n.urls()), both) {
			t.Fatal(n.urls())
		}
	})
	tw(t, F, "explicit auto uses configured provider", func(t *testing.T) {
		n, _, err := tool(t, base, "auto")
		noErr(t, err)
		wantCalls(t, n, "https://api.perplexity.ai/chat/completions")
	})
	tskip(t, F, "auto still uses provider fallback when no provider is configured",
		"the fixture's only credential is OPENAI_API_KEY and OpenAI hosted search is not ported (docs/PORT.md); TestAutoOrderIsFixed in routing_test.go covers the ported order")
	tskip(t, F, "configured explicit-only SerpBase fails instead of falling back",
		"SerpBase is not ported; TestUnportedProviderFailsWithoutFallback pins the equivalent behaviour for every unported provider")
	tw(t, F, "legacy Gemini Web profile config does not block unrelated configured providers", func(t *testing.T) {
		n, _, err := tool(t, `{"provider":"tavily","tavilyApiKey":"tavily-test-key","allowBrowserCookies":true,"chromeProfile":"Profile 1"}`, nil)
		noErr(t, err)
		wantCalls(t, n, "https://api.tavily.com/search")
	})
	tw(t, F, "malformed config root fails with an explicit object-shape error", func(t *testing.T) {
		n, _, err := tool(t, "null\n", "auto")
		wantErr(t, err, `Invalid config in .*web-search\.json: expected a JSON object`)
		wantCalls(t, n)
	})
	tw(t, F, "non-curated search stops after caller cancellation", func(t *testing.T) {
		adapted(t, "provider tavily instead of anysearch (not ported)")
		providerEnv(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		n := searchProviderNet(t)
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err = mustTool(t, r, "web_search").Execute(ctx, map[string]any{"queries": []any{"first", "second"}, "provider": "tavily", "workflow": "none"}, nil)
		wantErr(t, err, `(?i)abort`)
		if !errors.Is(err, context.Canceled) {
			t.Fatal("the cause is lost")
		}
		wantCalls(t, n)
	})
	tskip(t, F, "curated and non-curated branches both resolve the requested provider",
		"asserts on the upstream TypeScript source text (index.ts regexes); the Go tool has one resolution path, ResolveRequestedProvider, covered by the cases above")
}

func TestUnportedProviderFailsWithoutFallback(t *testing.T) {
	providerEnv(t, `{"provider":"serpbase"}`, map[string]string{"TAVILY_API_KEY": "k"})
	r, err := NewRuntime(&fakeHost{})
	noErr(t, err)
	n := searchProviderNet(t)
	out := run(t, mustTool(t, r, "web_search"), map[string]any{"query": "x", "workflow": "none"})
	matchRE(t, `SerpBase search is not available in this Go port yet`, out.Text())
	wantCalls(t, n)
}

func TestUpstream_allowed_search_providers(t *testing.T) {
	const F = "allowed-search-providers"
	kagiOnly := func(extra string) string {
		return `{"webSearch":{"allowedProviders":["kagi"]}` + extra + `}`
	}
	searchOnce := func(t *testing.T, provider any) (*AttributedSearchResponse, error) {
		sel := Auto
		if provider != nil {
			var err error
			sel, err = NormalizeSearchProviderSelection(provider, "provider")
			noErr(t, err)
		}
		return Search(bg(), "q", FullSearchOptions{Provider: sel})
	}

	tw(t, F, "Kagi-only policy constrains schema and generated description", func(t *testing.T) {
		r, _ := newRuntime(t, kagiOnly(""))
		search := mustTool(t, r, "web_search")
		matchRE(t, `Search the web with Kagi\.`, search.Description)
		noMatchRE(t, `Brave`, search.Description)
		for _, name := range []string{"web_search", "source_check"} {
			schema := prop(t, mustTool(t, r, name), "provider")
			anyOf := schema["anyOf"].([]any)
			if !reflect.DeepEqual(anyOf[0].(map[string]any)["enum"], []any{"auto", "all", "kagi"}) ||
				!reflect.DeepEqual(anyOf[1].(map[string]any)["items"].(map[string]any)["enum"], []any{"kagi"}) {
				t.Fatalf("%s: %v", name, schema)
			}
		}
	})
	tskip(t, F, "Kagi-only policy generates Curator choices without disabled providers",
		"the search curator UI is deferred (docs/PORT.md)")
	tw(t, F, "disabled scalar and array selections fail before provider requests", func(t *testing.T) {
		providerEnv(t, kagiOnly(`,"kagiApiKey":"k"`), map[string]string{"BRAVE_API_KEY": "b"})
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
		var errs []error
		for _, p := range []any{"brave", []any{"kagi", "brave"}} {
			_, err := searchOnce(t, p)
			errs = append(errs, err)
		}
		if n.count() != 0 || len(errs) != 2 {
			t.Fatalf("%d calls", n.count())
		}
		for _, err := range errs {
			wantErr(t, err, `disabled provider`)
		}
	})
	tw(t, F, "web_search and Curator reject disabled providers before availability or requests", func(t *testing.T) {
		adapted(t, "only workflow none runs; summary-review is the curator (deferred)")
		providerEnv(t, kagiOnly(""), map[string]string{"BRAVE_API_KEY": "b"})
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
		_, err = mustTool(t, r, "web_search").Execute(bg(), map[string]any{"query": "q", "provider": "brave", "workflow": "none"}, nil)
		wantErr(t, err, `disabled provider`)
		wantCalls(t, n)
	})
	tw(t, F, "auto and all filter to allowed providers", func(t *testing.T) {
		for _, provider := range []string{"auto", "all"} {
			providerEnv(t, kagiOnly(`,"kagiApiKey":"k"`), map[string]string{"BRAVE_API_KEY": "b", "EXA_API_KEY": "e"})
			n := searchProviderNet(t)
			out, err := searchOnce(t, provider)
			noErr(t, err)
			wantCalls(t, n, kagiURL)
			want := "kagi"
			if provider == "all" {
				want = "all"
			}
			if out.Provider != want {
				t.Fatalf("%s: %s", provider, out.Provider)
			}
		}
	})
	tw(t, F, "allowlisting an explicit-only provider does not opt it into auto or all", func(t *testing.T) {
		adapted(t, "duckduckgo instead of serply (not ported); both are explicit-only")
		for _, provider := range []string{"auto", "all"} {
			providerEnv(t, `{"webSearch":{"allowedProviders":["duckduckgo"]}}`, nil)
			n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
			_, err := searchOnce(t, provider)
			wantErr(t, err, `No search provider available|No configured search provider available`)
			wantCalls(t, n)
		}
	})
	tw(t, F, "an allowlisted explicit-only provider remains directly selectable with its credential", func(t *testing.T) {
		adapted(t, "duckduckgo (keyless) instead of serply (not ported); credential header check dropped")
		providerEnv(t, `{"webSearch":{"allowedProviders":["duckduckgo"]}}`, nil)
		n := useNet(t, func(netCall) netReply {
			return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: `<div class="result"><a class="result__a" href="https://example.com/ddg">DDG</a><a class="result__snippet">answer</a></div>`}
		})
		out, err := searchOnce(t, "duckduckgo")
		noErr(t, err)
		if out.Provider != "duckduckgo" || n.count() != 1 {
			t.Fatalf("%s %d", out.Provider, n.count())
		}
	})
	tw(t, F, "source_check cannot bypass policy", func(t *testing.T) {
		providerEnv(t, kagiOnly(""), map[string]string{"BRAVE_API_KEY": "b"})
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
		out := run(t, mustTool(t, r, "source_check"), map[string]any{"claim": "claim", "provider": "brave"})
		wantCalls(t, n)
		matchRE(t, `disabled provider`, jsonNoEscape(out.Details["artifact"]))
	})
	tw(t, F, "an absent config file preserves registration and search behavior", func(t *testing.T) {
		adapted(t, "commands are the ported ones and web_enable is registered (dynamic activation is the default here)")
		providerEnv(t, "", map[string]string{"KAGI_API_KEY": "k"})
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		searchProviderNet(t)
		providers := prop(t, mustTool(t, r, "web_search"), "provider")["anyOf"].([]any)[0].(map[string]any)["enum"].([]any)
		hasSerply := false
		for _, p := range providers {
			hasSerply = hasSerply || p == "serply"
		}
		out, err := searchOnce(t, "kagi")
		noErr(t, err)
		if !reflect.DeepEqual(toolNames(r), []string{"web_search", "source_check", "fetch_content", "get_search_content", "web_enable"}) ||
			!reflect.DeepEqual(r.Commands(), []string{"search"}) || !hasSerply || out.Provider != "kagi" {
			t.Fatal(toolNames(r), r.Commands(), hasSerply, out.Provider)
		}
	})
	tw(t, F, "invalid allowlists fail tool registration clearly", func(t *testing.T) {
		for _, c := range []struct{ list, pattern string }{
			{`[]`, `must be a non-empty array`},
			{`["kagi","KAGI"]`, `must not contain duplicates: kagi`},
			{`["not-a-provider"]`, `contains an invalid provider: not-a-provider`},
			{`"kagi"`, `must be a non-empty array`},
		} {
			extractEnv(t, `{"webSearch":{"allowedProviders":`+c.list+`}}`)
			_, err := NewRuntime(&fakeHost{})
			wantErr(t, err, c.pattern)
			wantErr(t, err, `webSearch\.allowedProviders`)
		}
	})
	tw(t, F, "a configured auto default retains automatic allowlist behavior", func(t *testing.T) {
		providerEnv(t, kagiOnly(`,"provider":"auto","kagiApiKey":"k"`), map[string]string{"BRAVE_API_KEY": "b", "SERPLY_API_KEY": "s"})
		n := searchProviderNet(t)
		out, err := searchOnce(t, nil)
		noErr(t, err)
		wantCalls(t, n, kagiURL)
		if out.Provider != "kagi" {
			t.Fatal(out.Provider)
		}
	})
	tw(t, F, "source_check follows an allowed routing fallback", func(t *testing.T) {
		providerEnv(t, `{"webSearch":{"allowedProviders":["brave","kagi"]},"searchRouting":{"providers":["brave","kagi"],"fallbackOn":["network"]},"kagiApiKey":"k"}`, map[string]string{"BRAVE_API_KEY": "b"})
		r, err := NewRuntime(&fakeHost{})
		noErr(t, err)
		n := useNet(t, func(c netCall) netReply {
			switch {
			case strings.HasPrefix(c.URL, "https://api.search.brave.com/"):
				return netReply{Err: errors.New("fetch failed")}
			case c.URL == kagiURL:
				return reply(200, kagiBody)
			}
			return netReply{Err: fmt.Errorf("disabled request %s", c.URL)}
		})
		out := run(t, mustTool(t, r, "source_check"), map[string]any{"claim": "q"})
		artifact := out.Details["artifact"].(ResearchArtifact)
		urls := n.urls()
		if artifact.Provider != "kagi" || len(artifact.Errors) != 0 || len(urls) != 2 || !strings.HasPrefix(urls[0], "https://api.search.brave.com/") || urls[1] != kagiURL {
			t.Fatalf("%+v %v", artifact, urls)
		}
	})
	tw(t, F, "configured defaults and routing cannot reference disabled providers", func(t *testing.T) {
		for _, config := range []string{
			`{"webSearch":{"allowedProviders":["kagi"]},"provider":"brave"}`,
			`{"webSearch":{"allowedProviders":["kagi"]},"searchRouting":{"providers":["brave"],"fallbackOn":["network"]}}`,
		} {
			providerEnv(t, config, nil)
			n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
			_, err := searchOnce(t, nil)
			wantErr(t, err, `disabled provider`)
			wantCalls(t, n)
		}
	})
	tw(t, F, "both configured provider aliases are validated while searchProvider keeps precedence", func(t *testing.T) {
		providerEnv(t, `{"webSearch":{"allowedProviders":["kagi"]},"searchProvider":"kagi","provider":"brave"}`, map[string]string{"BRAVE_API_KEY": "b", "KAGI_API_KEY": "k"})
		n := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not fetch")} })
		_, err := searchOnce(t, nil)
		wantErr(t, err, `provider in .* references disabled provider "brave"`)
		wantCalls(t, n)

		providerEnv(t, `{"webSearch":{"allowedProviders":["kagi","brave"]},"searchProvider":"kagi","provider":"brave","kagiApiKey":"k"}`, map[string]string{"BRAVE_API_KEY": "b"})
		n = searchProviderNet(t)
		out, err := searchOnce(t, nil)
		noErr(t, err)
		wantCalls(t, n, kagiURL)
		if out.Provider != "kagi" {
			t.Fatal(out.Provider)
		}
	})
}

func TestUpstream_source_check_tool(t *testing.T) {
	const F = "source-check"
	const note = "provider tavily instead of openai (not ported)"
	check := func(t *testing.T, params map[string]any, handler func(netCall) netReply) (ToolOutput, *fakeHost, error) {
		providerEnv(t, `{}`, map[string]string{"TAVILY_API_KEY": "source-check-test-key"})
		ClearResults()
		t.Cleanup(ClearResults)
		h := &fakeHost{}
		r, err := NewRuntime(h)
		noErr(t, err)
		useNet(t, handler)
		params["provider"] = "tavily"
		ctx := bg()
		if c, ok := params["__ctx"].(context.Context); ok {
			ctx = c
			delete(params, "__ctx")
		}
		out, err := mustTool(t, r, "source_check").Execute(ctx, params, nil)
		return out, h, err
	}
	docs := tavilyBody("The API supports streaming responses.", map[string]string{"title": "API docs", "url": "https://93.184.216.34/api", "content": ""})
	tw(t, F, "source_check executes a successful OpenAI provider response with runtime context", func(t *testing.T) {
		adapted(t, note)
		out, h, err := check(t, map[string]any{"claim": "API supports streaming responses"}, func(c netCall) netReply {
			if c.URL != "https://api.tavily.com/search" {
				return netReply{Err: fmt.Errorf("unexpected %s", c.URL)}
			}
			return reply(200, docs)
		})
		noErr(t, err)
		if dInt(t, out, "sourceCount") != 1 || dInt(t, out, "passageCount") != 0 || h.entries[0].Type != "web-search-results" {
			t.Fatalf("%+v %+v", out.Details, h.entries)
		}
	})
	tw(t, F, "source_check stops on cancellation instead of continuing queries", func(t *testing.T) {
		adapted(t, note)
		ctx, cancel := context.WithCancel(bg())
		calls := 0
		out, _, err := check(t, map[string]any{"claim": "cancel this", "queries": []any{"first", "second"}, "__ctx": ctx}, func(netCall) netReply {
			calls++
			cancel()
			return netReply{Err: errors.New("AbortError: canceled")}
		})
		noErr(t, err)
		if calls != 1 || dInt(t, out, "sourceCount") != 0 {
			t.Fatalf("%d %+v", calls, out.Details)
		}
	})
	tw(t, F, "source_check retains a rejected page fetch in the artifact", func(t *testing.T) {
		adapted(t, note+"; the page is an IP literal so no DNS is needed")
		out, _, err := check(t, map[string]any{"claim": "API docs", "fetchContent": true}, func(c netCall) netReply {
			if c.URL == "https://api.tavily.com/search" {
				return reply(200, tavilyBody("", map[string]string{"title": "API docs", "url": "https://93.184.216.34/api", "content": ""}))
			}
			return netReply{Err: errors.New("fetch rejected")}
		})
		noErr(t, err)
		artifact := out.Details["artifact"].(ResearchArtifact)
		if dInt(t, out, "sourceCount") != 1 || !strings.HasPrefix(artifact.Sources[0].FetchError, "fetch rejected") {
			t.Fatalf("%+v", artifact.Sources)
		}
	})
	tw(t, F, "registered source_check validates the claim at runtime", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		out := run(t, mustTool(t, r, "source_check"), map[string]any{"claim": "   "})
		if dStr(out, "error") != "Missing claim" {
			t.Fatalf("%+v", out.Details)
		}
	})
}

// ---- fetch-mode-config ------------------------------------------------------------------------

func TestUpstream_fetch_mode_config(t *testing.T) {
	const F = "fetch-mode-config"
	tw(t, F, "fetch mode config rejects a default outside the allowed modes", func(t *testing.T) {
		extractEnv(t, `{"fetch":{"defaultMode":"readable","allowedModes":["raw"]}}`)
		_, err := NewRuntime(&fakeHost{})
		wantErr(t, err, `fetch\.defaultMode.*must be one of fetch\.allowedModes`)
	})
	tw(t, F, "fetch mode config rejects duplicate allowed modes", func(t *testing.T) {
		extractEnv(t, `{"fetch":{"allowedModes":["readable","raw","raw"]}}`)
		_, err := NewRuntime(&fakeHost{})
		wantErr(t, err, `fetch\.allowedModes.*must not contain duplicates: "raw"`)
	})
	tw(t, F, "absent config exposes all modes and defaults execution to readable", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		calls := 0
		useNet(t, func(netCall) netReply {
			calls++
			return netReply{Status: 200, Body: "plain readable body", Header: hdr("content-type", "text/plain")}
		})
		tool := mustTool(t, r, "fetch_content")
		mode := prop(t, tool, "mode")
		if !reflect.DeepEqual(mode["enum"], []any{"readable", "raw", "answer"}) && !reflect.DeepEqual(mode["enum"], []string{"readable", "raw", "answer"}) {
			t.Fatalf("%v", mode["enum"])
		}
		matchRE(t, `readable \(default\)`, mode["description"].(string))
		if _, ok := props(t, tool)["answerModel"]; !ok {
			t.Fatal("answerModel missing")
		}
		out := run(t, tool, map[string]any{"url": "https://93.184.216.34/page"})
		if out.Text() != "plain readable body" || dStr(out, "mode") != "readable" || calls != 1 {
			t.Fatalf("%q %v %d", out.Text(), out.Details, calls)
		}
	})
	tw(t, F, "configured modes align schema and execution and reject disabled modes before work", func(t *testing.T) {
		r, _ := newRuntime(t, `{"fetch":{"defaultMode":"raw","allowedModes":["raw"]}}`)
		calls := 0
		useNet(t, func(netCall) netReply {
			calls++
			return netReply{Status: 404, Body: "<html>exact raw body</html>", Header: hdr("content-type", "text/html")}
		})
		tool := mustTool(t, r, "fetch_content")
		mode := prop(t, tool, "mode")
		if e, _ := json.Marshal(mode["enum"]); string(e) != `["raw"]` {
			t.Fatalf("%s", e)
		}
		if _, ok := props(t, tool)["answerModel"]; ok {
			t.Fatal("answerModel must be hidden")
		}
		matchRE(t, `raw \(default\): return the exact textual body using direct HTTP only`, tool.Description)
		for _, s := range []string{tool.Description, tool.PromptSnippet, mode["description"].(string)} {
			noMatchRE(t, `readable|answer`, s)
		}
		def := run(t, tool, map[string]any{"url": "https://93.184.216.34/page"})
		matchRE(t, `<html>exact raw body</html>`, def.Text())
		if dStr(def, "mode") != "raw" || dInt(t, def, "status") != 404 {
			t.Fatalf("%v", def.Details)
		}
		after, updates := calls, 0
		out, err := tool.Execute(bg(), map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "question", "auth": "missing"},
			func(ToolOutput) { updates++ })
		noErr(t, err)
		matchRE(t, `Fetch mode "answer" is disabled by fetch\.allowedModes`, out.Text())
		if calls != after || updates != 0 {
			t.Fatalf("work before rejecting: %d %d", calls-after, updates)
		}
	})
}

func TestUpstream_tool_registration_readme(t *testing.T) {
	tw(t, "tool-registration-config", "README documents registration gates and toolNames", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
		if os.IsNotExist(err) {
			t.Skip("the Package README is not part of a copied extension tree (mutation runs)")
		}
		noErr(t, err)
		readme := string(raw)
		matchRE(t, `"tools": \{`, readme)
		matchRE(t, `"commands": \{`, readme)
		matchRE(t, `Pi restart is required for tool and command registration changes`, readme)
		matchRE(t, "`toolNames` can opt into alternate public tool names", readme)
	})
}
