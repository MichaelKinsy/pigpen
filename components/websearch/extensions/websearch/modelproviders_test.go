package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Behaviour of the model-backed providers that the upstream titles do not pin one by one.

func TestCompareNumericLocale(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"gpt-5.10", "gpt-5.9", 1},
		{"gpt-5.9", "gpt-5.10", -1},
		{"gpt-6-luna", "gpt-5.6-luna", 1},
		{"gpt-5.6-terra", "gpt-5.6-luna", 1},
		{"gpt-5", "gpt-5", 0},
		{"gpt-5", "gpt-5-mini", -1},
		{"gpt-007", "gpt-7", 0},
		{"gpt-a", "gpt-1", 1},
		{"gpt-1", "gpt--", 1},
		{"gpt-5.6", "gpt-5-6", 1},
		{"a", "B", -1},
		{"b", "A", 1},
		{"a", "A", -1},
		{"A", "a", 1},
	} {
		got := compareNumericLocale(tc.a, tc.b)
		if (got < 0) != (tc.want < 0) || (got > 0) != (tc.want > 0) {
			t.Errorf("compareNumericLocale(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPickSearchModel(t *testing.T) {
	ids := func(ids ...string) []map[string]any {
		var out []map[string]any
		for _, id := range ids {
			out = append(out, map[string]any{"id": id})
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		models []map[string]any
		want   string
	}{
		{"terra wins over a newer mainline id", ids("gpt-6", "gpt-5.6-terra", "gpt-5.5-terra"), "gpt-5.6-terra"},
		{"the newest bare mainline id", ids("gpt-5.9", "gpt-5.10", "gpt-5.6-luna"), "gpt-5.10"},
		{"a versioned id when there is no mainline one", ids("gpt-5.6-luna", "gpt-6-luna", "gpt-oss-120b"), "gpt-6-luna"},
		{"pro and ultra tiers are excluded", ids("gpt-6-pro", "gpt-6-ultra", "gpt-5.5"), "gpt-5.5"},
		{"anything else, the newest first", ids("alpha", "zeta"), "zeta"},
		{"nothing eligible", ids("gpt-6-pro"), ""},
		{"no models", nil, ""},
	} {
		got := pickSearchModel(tc.models)
		id := ""
		if got != nil {
			id = got["id"].(string)
		}
		if id != tc.want {
			t.Errorf("%s: picked %q, want %q", tc.name, id, tc.want)
		}
	}
}

func TestCodexTokens(t *testing.T) {
	if !isCodexJWT(codexToken()) || extractAccountID(codexToken()) != "test-account" {
		t.Fatal("the Codex token was not recognised")
	}
	for _, token := range []string{"", "plain-key", "a.b", "a.%%%.c", "a.e30.c", "a.bnVsbA.c"} {
		if isCodexJWT(token) || extractAccountID(token) != "" {
			t.Errorf("%q was taken for a Codex token", token)
		}
	}
}

func TestOpenAIResponsesBodyAndPrompt(t *testing.T) {
	body := buildResponsesBody("q", SearchOptions{RecencyFilter: "week", NumResults: nf(50), DomainFilter: []string{"example.com", "-spam.example"}}, "gpt-x")
	raw, err := encodeJSONBody(body)
	noErr(t, err)
	want := `{"model":"gpt-x","instructions":"Search the web and return a concise answer grounded only in the web results. Include clickable source citations in the response text when possible. Prefer sources from the past week. Prefer around 20 distinct sources. Only use sources from: example.com. Do not use sources from: spam.example.","input":[{"role":"user","content":[{"type":"input_text","text":"q"}]}],"tools":[{"type":"web_search","filters":{"allowed_domains":["example.com"],"blocked_domains":["spam.example"]}}],"include":["web_search_call.action.sources"],"store":false,"stream":true,"tool_choice":"required","parallel_tool_calls":true}`
	if string(raw) != want {
		t.Fatalf("%s", raw)
	}
	plain, _ := encodeJSONBody(buildResponsesBody("a & b", SearchOptions{}, "m"))
	if !strings.Contains(string(plain), `"text":"a & b"`) || !strings.Contains(string(plain), `"tools":[{"type":"web_search"}]`) {
		t.Fatalf("%s", plain)
	}
}

func TestOpenAIStreamedResponse(t *testing.T) {
	done := func(item string) string { return `data: {"type":"response.output_item.done","item":` + item + `}` }
	text := "ignored: event\n\n" +
		`data: {"type":"response.web_search_call.completed"}` + "\n" +
		done(`{"type":"message","content":[{"type":"output_text","text":"See [the docs](https://example.com/docs) for more.","annotations":[{"type":"url_citation","url":"https://example.com/docs?utm_source=openai&x=1","title":"Docs","start_index":4,"end_index":20}]}]}`) + "\n" +
		"data: not json\n" +
		"data: [DONE]\n"
	parsed, err := parseOpenAIResponse(text)
	noErr(t, err)
	if !parsed.webSearchCallSeen || len(parsed.output) != 1 {
		t.Fatalf("%+v", parsed)
	}
	results := extractOpenAISearchResults(parsed.output, nil)
	want := []SearchResult{{Title: "Docs", URL: "https://example.com/docs?x=1", Snippet: "See the docs for more."}}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("%+v", results)
	}
	if extractOpenAIAnswer(parsed.output) != "See [the docs](https://example.com/docs) for more." {
		t.Fatal(extractOpenAIAnswer(parsed.output))
	}

	// A completed response with output wins over the streamed items; one without borrows them.
	completed := `data: {"type":"response.completed","response":{"output":[{"type":"web_search_call"}]}}` + "\n"
	p, err := parseOpenAIResponse(completed)
	noErr(t, err)
	if len(p.output) != 1 || !p.webSearchCallSeen {
		t.Fatalf("%+v", p)
	}
	empty := done(`{"type":"web_search_call"}`) + "\n" + `data: {"type":"response.done","response":{"output":[]}}` + "\n"
	p, err = parseOpenAIResponse(empty)
	noErr(t, err)
	if len(p.output) != 1 || !p.webSearchCallSeen {
		t.Fatalf("%+v", p)
	}
	if _, err := parseOpenAIResponse("event: ping\n"); err == nil || !strings.Contains(err.Error(), "no parseable response output") {
		t.Fatalf("%v", err)
	}
	if _, err := parseOpenAIResponse(`{"output": [`); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("%v", err)
	}
}

func TestOpenAISnippetsAndSourceURLs(t *testing.T) {
	long := strings.Repeat("x", 400)
	if got := extractSnippetAround(long, 0.0, 400.0); len(got) != 300 || !strings.HasSuffix(got, "...") {
		t.Fatalf("%d %q", len(got), got)
	}
	if got := extractSnippetAround("short text", 0.0, 5.0); got != "short text" {
		t.Fatal(got)
	}
	if got := extractSnippetAround(long, 150.0, 160.0); len(got) != 210 {
		t.Fatalf("a citation at 150..160 keeps 100 characters either side: %d", len(got))
	}
	// Offsets count UTF-16 units: the emoji is two of them.
	if got := extractSnippetAround("😀 cited", 3.0, 8.0); got != "😀 cited" {
		t.Fatal(got)
	}
	for _, tc := range []struct{ in, want string }{
		{"https://example.com/a?utm_source=openai", "https://example.com/a"},
		{"https://example.com/a?utm_source=other", "https://example.com/a?utm_source=other"},
		{"https://example.com/a?b=1&utm_source=openai&c=2", "https://example.com/a?b=1&c=2"},
		{"https://example.com/a?utm_source=openai#frag", "https://example.com/a#frag"},
		{"not a URL?utm_source=openai", "not a URL"},
		{"relative&utm_source=openai", "relative"},
	} {
		if got := cleanSourceURL(tc.in); got != tc.want {
			t.Errorf("cleanSourceURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Without a limit every source is kept; the count is capped at 20.
	var many []any
	for i := 0; i < 30; i++ {
		many = append(many, map[string]any{"type": "web_search_call", "sources": []any{map[string]any{"url": "https://example.com/" + string(rune('a'+i%26)) + string(rune('a'+i/26))}}})
	}
	if n := len(extractOpenAISearchResults(many, nil)); n != 30 {
		t.Fatal(n)
	}
	if n := len(extractOpenAISearchResults(many, nf(25))); n != 20 {
		t.Fatal(n)
	}
	if n := len(extractOpenAISearchResults(many, nf(3.9))); n != 3 {
		t.Fatal(n)
	}
}

func TestOpenAIAutoOrderFollowsTheSelectedModel(t *testing.T) {
	const openaiURL, exaURL, braveURL = "https://api.openai.com/v1/responses", "https://api.exa.ai/search", "https://api.search.brave.com/"
	cases := []struct {
		name  string
		model map[string]any
		ext   bool
		want  []string
	}{
		{"no session context: OpenAI before Exa", nil, false, []string{openaiURL, exaURL, braveURL}},
		{"a Codex model: OpenAI before Exa", map[string]any{"provider": "openai-codex", "id": "gpt-5"}, true, []string{openaiURL, exaURL, braveURL}},
		{"another model: Exa, then OpenAI, then the rest", map[string]any{"provider": "anthropic", "id": "claude"}, true, []string{exaURL, openaiURL, braveURL}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := isolate(t)
			writeConfig(t, dir, `{"openaiApiKey":"openai-key","exaApiKey":"exa-key","braveApiKey":"brave-key"}`)
			fn := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("fetch failed")} })
			ctx := bg()
			if tc.ext {
				ctx = withExt(&fakeExt{model: tc.model, reg: &fakeRegistry{auth: staticAuth(map[string]any{"ok": false})}})
			}
			if _, err := Search(ctx, "order", FullSearchOptions{Provider: Auto}); err == nil {
				t.Fatal("expected failures")
			}
			got := fn.urls()
			if len(got) != 3 {
				t.Fatalf("calls %v", got)
			}
			for i, want := range tc.want {
				if !strings.HasPrefix(got[i], want) {
					t.Fatalf("calls %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestOpenAIAlphaNotImplementedFallsBackOnlyAsUnsupported(t *testing.T) {
	// 501 is "unsupported" for alpha/search: routing that falls back on unsupported alone continues,
	// and a status that were merely transient would stop it.
	for _, status := range []int{404, 405, 501} {
		cfg := map[string]any{"tavilyApiKey": "tavily-test-key", "searchRouting": json.RawMessage(`{"providers":["openai","tavily"],"fallbackOn":["unsupported"]}`)}
		out := runOpenAI(t, oaScenario{route: true, status: status, raw: strp("no"), config: cfg})
		out.mustOK(t)
		if out.result.(*AttributedSearchResponse).Provider != "tavily" {
			t.Fatalf("%d: %+v", status, out.result)
		}
	}
	cfg := map[string]any{"tavilyApiKey": "tavily-test-key", "searchRouting": json.RawMessage(`{"providers":["openai","tavily"],"fallbackOn":["unsupported"]}`)}
	out := runOpenAI(t, oaScenario{route: true, status: 503, raw: strp("no"), config: cfg})
	if out.kind != "transient" {
		t.Fatalf("503 on alpha is transient, not unsupported: %q %v", out.kind, out.err)
	}
}

func TestOpenAIConfigurationErrorsSurfaceWhereTheOriginalAwaitsThem(t *testing.T) {
	bad := `{"openaiApiKey":"k","openaiResponsesUrl":"not a URL","braveApiKey":"b","searchRouting":{"providers":["openai","brave"],"fallbackOn":["network"]}}`
	setup := func(t *testing.T, cfg string) {
		_, dir := isolate(t)
		writeConfig(t, dir, cfg)
		noNet(t)
	}
	t.Run("configured routing", func(t *testing.T) {
		setup(t, bad)
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `openaiResponsesUrl.*absolute http\(s\) URL`)
	})
	t.Run("provider all", func(t *testing.T) {
		setup(t, bad)
		_, err := Search(bg(), "q", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		wantErr(t, err, `openaiResponsesUrl.*absolute http\(s\) URL`)
	})
	t.Run("auto records it and goes on", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"openaiApiKey":"k","openaiResponsesUrl":"not a URL","braveApiKey":"b"}`)
		useNet(t, func(netCall) netReply { return netReply{Err: errors.New("fetch failed")} })
		_, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `(?s)Auto provider search failed:.*OpenAI: openaiResponsesUrl.*Brave:`)
	})
	t.Run("provider all leaves an unallowed provider alone", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"openaiApiKey":"k","openaiResponsesUrl":"not a URL","braveApiKey":"b","webSearch":{"allowedProviders":["brave"]}}`)
		useNet(t, func(netCall) netReply { return braveOK("Brave", "https://example.com/b", "ok") })
		r, err := Search(bg(), "q", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		noErr(t, err)
		if r.Provider != "all" {
			t.Fatal(r.Provider)
		}
	})
}

func TestOpenAICurrentModelRouting(t *testing.T) {
	official := &fakeExt{model: map[string]any{"provider": "openai", "id": "gpt-5.6", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"},
		reg: &fakeRegistry{auth: staticAuth(authOK("current-key", map[string]any{}))}}
	for _, tc := range []struct {
		name  string
		model map[string]any
		want  bool
	}{
		{"official OpenAI", official.model, true},
		{"a non-gpt id", map[string]any{"provider": "openai", "id": "o3", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"}, false},
		{"uppercase id prefix", map[string]any{"provider": "openai", "id": "GPT-5", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"}, true},
		{"plain http", map[string]any{"provider": "openai", "id": "gpt-5", "api": "openai-responses", "baseUrl": "http://api.openai.com/v1"}, false},
		{"another host", map[string]any{"provider": "openai", "id": "gpt-5", "api": "openai-responses", "baseUrl": "https://gateway.example/v1"}, false},
		{"another api", map[string]any{"provider": "openai", "id": "gpt-5", "api": "openai-completions", "baseUrl": "https://api.openai.com/v1"}, false},
		{"Codex", map[string]any{"provider": "openai-codex", "id": "gpt-5", "api": "openai-codex-responses", "baseUrl": "https://chatgpt.com/backend-api/"}, true},
		{"Codex on another path", map[string]any{"provider": "openai-codex", "id": "gpt-5", "api": "openai-codex-responses", "baseUrl": "https://chatgpt.com/other"}, false},
	} {
		ec := &fakeExt{model: tc.model, reg: official.reg}
		if got := IsCurrentModelHostedSearchEligible(ec); got != tc.want {
			t.Errorf("%s: eligible = %v, want %v", tc.name, got, tc.want)
		}
	}
	if IsCurrentModelHostedSearchEligible(nil) || IsCurrentModelHostedSearchEligible(&fakeExt{reg: official.reg}) {
		t.Fatal("a missing context or model is not eligible")
	}

	_, dir := isolate(t)
	writeConfig(t, dir, `{}`)
	_, err := SearchWithCurrentModelOpenAI(bg(), "q", SearchOptions{})
	wantErr(t, err, `requires an extension context`)
	denied := &fakeExt{model: official.model, reg: &fakeRegistry{auth: staticAuth(map[string]any{"ok": false, "error": "login expired"})}}
	_, err = SearchWithCurrentModelOpenAI(withExt(denied), "q", SearchOptions{})
	wantErr(t, err, `OpenAI current model authentication failed: login expired`)
	keyless := &fakeExt{model: official.model, reg: &fakeRegistry{auth: staticAuth(map[string]any{"ok": true})}}
	_, err = SearchWithCurrentModelOpenAI(withExt(keyless), "q", SearchOptions{})
	wantErr(t, err, `API key unavailable`)
	rejected := &fakeExt{model: official.model, reg: &fakeRegistry{auth: func(map[string]any) (map[string]any, error) { return nil, errors.New("keychain locked") }}}
	_, err = SearchWithCurrentModelOpenAI(withExt(rejected), "q", SearchOptions{})
	wantErr(t, err, `keychain locked`)
}

func TestXaiRegistryAuth(t *testing.T) {
	newExt := func(models []map[string]any, auth func(map[string]any) (map[string]any, error)) *fakeExt {
		return &fakeExt{reg: &fakeRegistry{models: models, auth: auth}}
	}
	grok := func(id string) map[string]any { return model("xai", id) }
	subscription := staticAuth(authOK("grok-key", map[string]any{"X-Grok": "1"}))

	t.Run("the first known candidate wins, and xaiSearchModel renames only the request", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"xaiSearchModel":"grok-5"}`)
		ec := newExt([]map[string]any{grok("grok-4.3"), grok("grok-4.5")}, subscription)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(withExt(ec), "q", SearchOptions{})
		noErr(t, err)
		c := fn.calls[0]
		if jsonBody(t, c)["model"] != "grok-5" || c.Header.Get("Authorization") != "Bearer grok-key" || c.Header.Get("X-Grok") != "1" || !eqStrings(ec.reg.selected, []string{"xai/grok-4.5"}) {
			t.Fatalf("%s %v %v", c.Body, c.Header, ec.reg.selected)
		}
	})

	t.Run("a model the registry lacks, or whose auth fails, is skipped", func(t *testing.T) {
		isolate(t)
		ec := newExt([]map[string]any{grok("grok-4.5"), grok("grok-4.3")}, func(m map[string]any) (map[string]any, error) {
			if m["id"] == "grok-4.5" {
				return nil, errors.New("refresh failed")
			}
			return authOK("grok-later", nil), nil
		})
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(withExt(ec), "q", SearchOptions{})
		noErr(t, err)
		if jsonBody(t, fn.calls[0])["model"] != "grok-4.3" || !eqStrings(ec.reg.selected, []string{"xai/grok-4.5", "xai/grok-4.3"}) {
			t.Fatalf("%s %v", fn.calls[0].Body, ec.reg.selected)
		}
		if !IsXaiSearchAvailable(ec) {
			t.Fatal("unavailable")
		}
	})

	t.Run("x_search prefers grok-4.6", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"xaiSearchTools":["x_search"]}`)
		ec := newExt([]map[string]any{grok("grok-4.5"), grok("grok-4.6")}, subscription)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiXSearchBody) })
		_, err := SearchWithXai(withExt(ec), "q", SearchOptions{})
		noErr(t, err)
		if jsonBody(t, fn.calls[0])["model"] != "grok-4.6" {
			t.Fatal(fn.calls[0].Body)
		}
	})

	t.Run("without a usable login the API key is used", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"xaiApiKey":"configured-key"}`)
		ec := newExt([]map[string]any{grok("grok-4.5")}, staticAuth(map[string]any{"ok": false}))
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(withExt(ec), "q", SearchOptions{})
		noErr(t, err)
		if fn.calls[0].Header.Get("Authorization") != "Bearer configured-key" {
			t.Fatal(fn.calls[0].Header)
		}
	})

	t.Run("a null citation list, a null body and the redirect credential", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"xaiApiKey":"k"}`)
		useNet(t, func(netCall) netReply { return reply(200, "null") })
		_, err := SearchWithXai(bg(), "q", SearchOptions{})
		wantErr(t, err, `Cannot read properties of null`)
		useNet(t, func(netCall) netReply { return reply(200, "[]") })
		_, err = SearchWithXai(bg(), "q", SearchOptions{})
		wantErr(t, err, `returned no answer or sources`)
		fn := useNet(t, func(c netCall) netReply {
			if c.URL == xaiURL {
				return redirectTo(307, "https://elsewhere.example/v1/responses")
			}
			return reply(200, xaiSuccessBody)
		})
		_, err = SearchWithXai(bg(), "q", SearchOptions{})
		noErr(t, err)
		if fn.calls[1].Header.Get("Authorization") != "" || fn.calls[1].Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%v", fn.calls[1].Header)
		}
	})
}

func TestXaiPrompt(t *testing.T) {
	got := buildXaiInput("q", SearchOptions{}, []string{"web_search"})
	if got != "Search the web and answer using only what the web results say. Cite your sources inline.\n\nq" {
		t.Fatalf("%q", got)
	}
	if !strings.HasPrefix(buildXaiInput("q", SearchOptions{}, []string{"x_search"}), "Search X and answer using only what the X results say.") {
		t.Fatal("x only")
	}
	if got := buildXaiInput("q", SearchOptions{RecencyFilter: "day", NumResults: nf(0.5)}, []string{"web_search"}); !strings.Contains(got, "Prefer sources from the past 24 hours. Prefer around 0 distinct sources.") {
		t.Fatalf("%q", got)
	}
}

func TestKimiBearerTokenAndModelOrder(t *testing.T) {
	auth := func(h map[string]any) *fakeExt {
		return registryContext([]map[string]any{model("kimi-coding", "m")}, staticAuth(map[string]any{"ok": true, "headers": h}))
	}
	for _, tc := range []struct {
		name    string
		headers map[string]any
		want    bool
	}{
		{"a bearer header", map[string]any{"authorization": "Bearer t"}, true},
		{"a lower-case scheme and extra space", map[string]any{"AUTHORIZATION": "  bearer   t  "}, true},
		{"a basic header", map[string]any{"Authorization": "Basic t"}, false},
		{"an empty token", map[string]any{"Authorization": "Bearer "}, false},
		{"a null header", map[string]any{"Authorization": nil}, false},
		{"no header", map[string]any{}, false},
	} {
		if got := IsKimiSearchAvailable(auth(tc.headers)); got != tc.want {
			t.Errorf("%s: available = %v, want %v", tc.name, got, tc.want)
		}
	}
	if IsKimiSearchAvailable(nil) {
		t.Fatal("no context")
	}
	failing := registryContext([]map[string]any{model("kimi-coding", "m")}, nil)
	failing.reg.getAll = errors.New("registry down")
	if IsKimiSearchAvailable(failing) {
		t.Fatal("a failing registry is unavailable")
	}
	// The primary provider is tried before the compatibility one, whatever the model order.
	both := registryContext([]map[string]any{model("kimi-code", "compat"), model("kimi-coding", "primary")}, staticAuth(map[string]any{"ok": false}))
	IsKimiSearchAvailable(both)
	if !eqStrings(both.reg.selected, []string{"kimi-coding/primary", "kimi-code/compat"}) {
		t.Fatal(both.reg.selected)
	}
	// A later credential failure does not hide an earlier "not ok".
	_, dir := isolate(t)
	writeConfig(t, dir, `{}`)
	_, err := SearchWithKimi(withExt(both), "q", SearchOptions{})
	wantErr(t, err, `/login kimi-coding`)
}

func TestKimiTimeoutAndRequestFailure(t *testing.T) {
	isolate(t)
	ec := registryContext(nil, nil)
	useNet(t, func(netCall) netReply { return netReply{Err: context.DeadlineExceeded} })
	_, err := SearchWithKimi(withExt(ec), "q", SearchOptions{})
	if err == nil || err.Error() != "Kimi Code search API timed out" {
		t.Fatalf("%v", err)
	}
	useNet(t, func(netCall) netReply { return netReply{Err: errors.New("fetch failed")} })
	_, err = SearchWithKimi(withExt(ec), "q", SearchOptions{})
	wantErr(t, err, `^fetch failed$`)
	useNet(t, func(netCall) netReply { return reply(200, `{"search_results":"nope"}`) })
	_, err = SearchWithKimi(withExt(ec), "q", SearchOptions{})
	wantErr(t, err, `invalid response: search_results must be an array`)
	useNet(t, func(netCall) netReply { return reply(200, `[]`) })
	_, err = SearchWithKimi(withExt(ec), "q", SearchOptions{})
	wantErr(t, err, `invalid response: search_results must be an array`)
}

func TestMistralPromptAndContent(t *testing.T) {
	if got := buildMistralPrompt("q", SearchOptions{}, 5); got != "q" {
		t.Fatalf("%q", got)
	}
	if got := buildMistralPrompt("q", SearchOptions{RecencyFilter: "month", NumResults: nf(7)}, 7); got != "Prefer sources from the past month. Prefer up to 7 distinct sources.\n\nq" {
		t.Fatalf("%q", got)
	}
	if got := buildMistralPrompt("q", SearchOptions{RecencyFilter: "decade"}, 5); !strings.Contains(got, "the decade.") {
		t.Fatalf("%q", got)
	}
	// String content, text chunks and references mix; a reference after the limit is dropped.
	parsed, err := parseMistralConversation(map[string]any{"outputs": []any{
		map[string]any{"type": "message.output", "content": "  plain  "},
		map[string]any{"type": "message.output", "content": []any{
			map[string]any{"type": "text", "text": " chunk "},
			map[string]any{"type": "tool_reference", "url": " https://a.example/x ", "title": " T ", "description": 5},
			map[string]any{"type": "tool_reference", "url": "https://b.example/y"},
		}},
		map[string]any{"type": "other", "content": "ignored"},
	}}, SearchOptions{}, 1)
	noErr(t, err)
	want := &SearchResponse{Answer: "plain\nchunk", Results: []SearchResult{{Title: "T", URL: "https://a.example/x", Snippet: ""}}}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("%+v", parsed)
	}
	for _, n := range []*float64{nil, nf(0), nf(-3), nf(99)} {
		if c := normalizeMistralCount(n); c < 1 || c > 20 {
			t.Fatalf("count %d", c)
		}
	}
}

func TestExtensionContextCarriesToTheProviderCall(t *testing.T) {
	if ExtensionContextFrom(bg()) != nil {
		t.Fatal("a bare context has no extension context")
	}
	if ctx := WithExtensionContext(bg(), nil); ExtensionContextFrom(ctx) != nil {
		t.Fatal("a nil extension context is not attached")
	}
	ec := &fakeExt{session: "s"}
	if ExtensionContextFrom(withExt(ec)) != ExtensionContext(ec) {
		t.Fatal("the attached context was lost")
	}
}

func TestCurrentModelOf(t *testing.T) {
	reg := &fakeRegistry{models: []map[string]any{model("openai", "gpt-5")}}
	if got := currentModelOf("gpt-5", "openai", reg); got == nil || got["id"] != "gpt-5" {
		t.Fatalf("%v", got)
	}
	for _, c := range [][2]string{{"", "openai"}, {"gpt-5", ""}, {"gpt-6", "openai"}} {
		if got := currentModelOf(c[0], c[1], reg); got != nil {
			t.Errorf("%v: %v", c, got)
		}
	}
}

func TestOpenAICodexTokenOnAnotherProviderUsesTheCodexEndpoint(t *testing.T) {
	// A Codex login under another Pi provider id is recognised by its token: it passes the origin
	// guard (the token, not the baseUrl, says where it belongs) and goes to the Codex endpoint.
	out := runOpenAI(t, oaScenario{registry: true, key: strp(codexToken()),
		model: map[string]any{"provider": "openai", "baseUrl": "https://gateway.example/v1"}, config: map[string]any{"openaiUseAlphaSearch": false}, payload: legacyPayload})
	out.mustOK(t)
	req := out.requests[0]
	if req.URL != codexResponsesURL || headerOf(req, "chatgpt-account-id") != "test-account" || headerOf(req, "originator") != "pi" {
		t.Fatalf("%s %v", req.URL, req.Header)
	}
}

func TestOpenAIRequestHeadersKeepTheRegistrysDuplicateNamesAsOneHeader(t *testing.T) {
	// Headers joins keys that differ only in case: the registry's stale lower-case authorization and
	// the search's own Authorization travel as one value, as in the original (alpha overwrites it).
	out := runOpenAI(t, oaScenario{registry: true, headers: map[string]any{"authorization": "stale"}, payload: legacyPayload, config: map[string]any{"openaiUseAlphaSearch": false}})
	out.mustOK(t)
	if got := headerOf(out.requests[0], "Authorization"); got != "stale, Bearer registry-test-key" {
		t.Fatal(got)
	}
}

func TestOpenAICurrentModelRoutingIgnoresTheConfiguredEndpointAndModel(t *testing.T) {
	out := runOpenAI(t, oaScenario{route: true, current: true, key: strp("current-key"), config: map[string]any{
		"openaiResponsesUrl": "https://gateway.example/v1/responses", "openaiSearchModel": "not-current", "searchRouting": routeRaw(`,"useCurrentModel":true`)}})
	out.mustOK(t)
	req := out.requests[0]
	if req.URL != "https://api.openai.com/v1/alpha/search" || req.Body["model"] != "gpt-5.6-luna" || headerOf(req, "Authorization") != "Bearer current-key" {
		t.Fatalf("%s %v %v", req.URL, req.Body, req.Header)
	}
	// Without the switch the configured endpoint and model apply.
	out = runOpenAI(t, oaScenario{route: true, registry: true, key: strp("current-key"), config: map[string]any{
		"openaiResponsesUrl": "https://gateway.example/v1/responses", "openaiSearchModel": "pinned", "searchRouting": routeRaw("")}})
	out.mustOK(t)
	if out.requests[0].URL != "https://gateway.example/v1/alpha/search" || out.requests[0].Body["model"] != "pinned" {
		t.Fatalf("%s %v", out.requests[0].URL, out.requests[0].Body)
	}
}

func TestProviderDeadlines(t *testing.T) {
	// SEARCH_TIMEOUT_MS of each original: OpenAI, xAI and Mistral 60 s, Kimi 30 s.
	for _, tc := range []struct {
		name string
		want time.Duration
		run  func(ctx context.Context) error
		cfg  string
	}{
		{"openai", 60 * time.Second, func(ctx context.Context) error { _, err := SearchWithOpenAI(ctx, "q", SearchOptions{}); return err }, `{"openaiApiKey":"k"}`},
		{"xai", 60 * time.Second, func(ctx context.Context) error { _, err := SearchWithXai(ctx, "q", SearchOptions{}); return err }, `{"xaiApiKey":"k"}`},
		{"mistral", 60 * time.Second, func(ctx context.Context) error { _, err := SearchWithMistral(ctx, "q", SearchOptions{}); return err }, `{"mistralApiKey":"k"}`},
		{"kimi", 30 * time.Second, func(ctx context.Context) error {
			_, err := SearchWithKimi(withExt(registryContext(nil, nil)), "q", SearchOptions{})
			return err
		}, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := isolate(t)
			writeConfig(t, dir, tc.cfg)
			var left time.Duration
			t.Cleanup(SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) {
				if d, ok := r.Context().Deadline(); ok {
					left = time.Until(d)
				}
				return nil, errors.New("stop")
			})))
			_ = tc.run(bg())
			if left > tc.want || left < tc.want-5*time.Second {
				t.Fatalf("deadline in %v, want about %v", left, tc.want)
			}
		})
	}
}

func TestErrorBodiesKeepThreeHundredCharacters(t *testing.T) {
	long := strings.Repeat("e", 400)
	for _, tc := range []struct {
		name, cfg string
		run       func() error
	}{
		{"openai", `{"openaiApiKey":"k"}`, func() error { _, err := SearchWithOpenAI(bg(), "q", SearchOptions{}); return err }},
		{"xai", `{"xaiApiKey":"k"}`, func() error { _, err := SearchWithXai(bg(), "q", SearchOptions{}); return err }},
		{"mistral", `{"mistralApiKey":"k"}`, func() error { _, err := SearchWithMistral(bg(), "q", SearchOptions{}); return err }},
		{"kimi", `{}`, func() error {
			_, err := SearchWithKimi(withExt(registryContext(nil, nil)), "q", SearchOptions{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := isolate(t)
			writeConfig(t, dir, tc.cfg)
			useNet(t, func(netCall) netReply { return reply(500, long) })
			err := tc.run()
			if err == nil || !strings.HasSuffix(err.Error(), ": "+long[:300]) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestOpenAIPrefersTheCodexLoginOverTheOpenAIOne(t *testing.T) {
	codex := map[string]any{"id": "gpt-5.6-terra", "provider": "openai-codex", "api": "openai-codex-responses", "baseUrl": "https://chatgpt.com/backend-api"}
	official := map[string]any{"id": "gpt-5.6-terra", "provider": "openai", "api": "openai-responses", "baseUrl": "https://api.openai.com/v1"}
	out := runOpenAI(t, oaScenario{registry: true, models: []map[string]any{official, codex}, payload: legacyPayload,
		authByProvider: map[string]map[string]any{
			"openai-codex": {"ok": true, "apiKey": "codex-login", "headers": map[string]any{}},
			"openai":       {"ok": true, "apiKey": "openai-login", "headers": map[string]any{}},
		}, config: map[string]any{"openaiUseAlphaSearch": false}})
	out.mustOK(t)
	if out.requests[0].URL != codexResponsesURL || headerOf(out.requests[0], "Authorization") != "Bearer codex-login" || !eqStrings(out.authProviders, []string{"openai-codex"}) {
		t.Fatalf("%s %v %v", out.requests[0].URL, out.requests[0].Header, out.authProviders)
	}
}
