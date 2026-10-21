package websearch

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Twins of the upstream tests that look at the curator's provider choices, the /websearch
// command's proxy scope, and the activity widget.

func TestUpstream_curator_providers(t *testing.T) {
	tw(t, "allowed-search-providers", "Curator represents unavailable auto honestly while keeping explicit Serply immediately selectable", func(t *testing.T) {
		adapted(t, "the Serply search itself is not part of this slice: the curator page and its provider state are what the case checks")
		e := newCuratorEnv(t, `{"webSearch":{"allowedProviders":["serply"]},"serplyApiKey":"s","autoOpenBrowser":false,"curatorTimeoutSeconds":5}`)
		curatorHTML := func(callID, provider string) string {
			t.Helper()
			run := e.runWebSearch(callID, map[string]any{"query": "q", "provider": provider, "workflow": "summary-review"})
			html := newCuratorClient(t, run.curatorURL()).page()
			run.cancel()
			run.result()
			return html
		}
		auto := curatorHTML("call-auto", "auto")
		explicit := curatorHTML("call-serply", "serply")
		for label, ok := range map[string]bool{
			"autoDefault":           strings.Contains(auto, `"defaultProvider":"auto"`),
			"autoSearch":            strings.Contains(auto, `"searchProvider":"auto"`),
			"autoSerplyIdle":        regexp.MustCompile(`class="provider-btn idle" data-provider="serply"`).MatchString(auto),
			"explicitDefault":       strings.Contains(explicit, `"defaultProvider":"serply"`),
			"explicitSearch":        strings.Contains(explicit, `"searchProvider":"serply"`),
			"explicitSerplyLoading": regexp.MustCompile(`class="provider-btn loading is-default" data-provider="serply"`).MatchString(explicit),
		} {
			if !ok {
				t.Errorf("%s is false", label)
			}
		}
	})
	tw(t, "search-providers", "curator auto default follows the active model provider", func(t *testing.T) {
		isolate(t)
		available := NewProviderAvailability("all", true, "openai", true, "brave", false, "parallel", false, "parallel-mcp", false, "tinyfish", false, "search1api", false, "searchinfinity", false,
			"querit", false, "tavily", false, "firecrawl", false, "jina", false, "serpdive", false, "searxng", false, "duckduckgo", false, "perplexity", false, "exa", true, "gemini", false,
			"kagi", false, "bocha", false, "ollama", false, "anysearch", false, "xai", false, "brightdata", false, "serpbase", false, "serper", false, "valyu", false)
		codex := map[string]any{"provider": "openai-codex"}
		openai := map[string]any{"provider": "openai"}
		if got := ResolveCuratorDefaultProvider(Auto, available, codex, nil, ""); got != "openai" {
			t.Errorf("codex: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(Auto, available, openai, nil, ""); got != "exa" {
			t.Errorf("openai model: %s", got)
		}
		noExa := available.Clone()
		noExa.Set("exa", false)
		if got := ResolveCuratorDefaultProvider(Auto, noExa, openai, nil, ""); got != "openai" {
			t.Errorf("openai model without exa: %s", got)
		}
		bocha := available.Clone()
		bocha.Set("openai", false)
		bocha.Set("exa", false)
		bocha.Set("bocha", true)
		bocha.Set("ollama", true)
		if got := ResolveCuratorDefaultProvider(Auto, bocha, nil, nil, ""); got != "bocha" {
			t.Errorf("bocha before ollama: %s", got)
		}
		// Codex does not win a search that asks for another result count or a recency filter.
		five, three := 5.0, 3.0
		if got := ResolveCuratorDefaultProvider(Auto, available, codex, &five, ""); got != "openai" {
			t.Errorf("five results: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(Auto, available, codex, &three, ""); got != "exa" {
			t.Errorf("three results: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(Auto, available, codex, nil, "week"); got != "exa" {
			t.Errorf("week: %s", got)
		}
		// Lists, "all", and explicit unavailable providers.
		if got := ResolveCuratorDefaultProvider(ProviderSelection{List: []string{"brave"}}, available, nil, nil, ""); got != "all" {
			t.Errorf("list: %s", got)
		}
		allOff := available.Clone()
		allOff.Set("all", false)
		if got := ResolveCuratorDefaultProvider(ProviderSelection{Name: "all"}, allOff, nil, nil, ""); got != "exa" {
			t.Errorf("all unavailable: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(ProviderSelection{Name: "brave"}, available, nil, nil, ""); got != "exa" {
			t.Errorf("brave unavailable falls to exa: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(ProviderSelection{Name: "serply"}, available, nil, nil, ""); got != "serply" {
			t.Errorf("explicit-only provider: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(ProviderSelection{Name: "openai"}, noExa, codex, nil, ""); got != "openai" {
			t.Errorf("openai: %s", got)
		}
	})
	tw(t, "search-routing", "Curator auto default follows the same current-model Hosted Search eligibility", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["openai","tavily"],"useCurrentModel":true,"fallbackOn":["unsupported","network"]}}`)
		available := NewProviderAvailability("all", true, "openai", true, "tavily", true)
		official := map[string]any{"provider": "openai", "api": "openai-responses", "id": "gpt-5.6", "baseUrl": "https://api.openai.com/v1"}
		proxy := map[string]any{"provider": "openai", "api": "openai-responses", "id": "gpt-5.6-sol", "baseUrl": "https://ai.feei.cn/v1"}
		if got := ResolveCuratorDefaultProvider(Auto, available, official, nil, ""); got != "openai" {
			t.Errorf("official: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(Auto, available, proxy, nil, ""); got != "tavily" {
			t.Errorf("proxy: %s", got)
		}
		// A Codex login on the ChatGPT backend is eligible too; a model that is not a gpt model, or has no model, is not.
		codexModel := map[string]any{"provider": "openai-codex", "api": "openai-codex-responses", "id": "gpt-5.6", "baseUrl": "https://chatgpt.com/backend-api"}
		if got := ResolveCuratorDefaultProvider(Auto, available, codexModel, nil, ""); got != "openai" {
			t.Errorf("codex: %s", got)
		}
		other := map[string]any{"provider": "openai", "api": "openai-responses", "id": "o3", "baseUrl": "https://api.openai.com/v1"}
		if got := ResolveCuratorDefaultProvider(Auto, available, other, nil, ""); got != "tavily" {
			t.Errorf("not a gpt model: %s", got)
		}
		if got := ResolveCuratorDefaultProvider(Auto, available, nil, nil, ""); got != "tavily" {
			t.Errorf("no model: %s", got)
		}
		// Nothing available: the first eligible candidate.
		none := NewProviderAvailability("all", false)
		if got := ResolveCuratorDefaultProvider(Auto, none, proxy, nil, ""); got != "tavily" {
			t.Errorf("unavailable proxy: %s", got)
		}
	})
}

// fakeWidgetUI records setWidget calls.
type fakeWidgetUI struct {
	mu    sync.Mutex
	calls []widgetCall
}

type widgetCall struct {
	key   string
	lines []string
}

func (u *fakeWidgetUI) SetWidget(key string, lines []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, widgetCall{key, lines})
}
func (u *fakeWidgetUI) Theme() WidgetTheme { return plainTheme{} }
func (u *fakeWidgetUI) last() widgetCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[len(u.calls)-1]
}
func (u *fakeWidgetUI) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func TestUpstream_websearch_command_proxy_scope(t *testing.T) {
	tw(t, "proxy-transport", "websearch command scopes searches but not model callbacks to configured proxy", func(t *testing.T) {
		e := newCuratorEnv(t, `{"provider":"xcrawl","xcrawlApiKey":"xc-test-key","proxy":"http://configured-proxy.example:8080","autoOpenBrowser":false,"curatorTimeoutSeconds":5}`)
		curl := useCurl(t, map[string]curlRoute{"https://run.xcrawl.com/v1/serp": {Status: 200, StatusText: "OK",
			Body: `{"search_metadata":{"status":"completed"},"organic_results":[{"title":"Result","link":"https://example.com/result","snippet":"Answer"}]}`}})
		var mu sync.Mutex
		var modelProxies []string
		e.reg.completeFn = func(ctx context.Context, _, request map[string]any, _ CompleteOptions) (map[string]any, error) {
			mu.Lock()
			modelProxies = append(modelProxies, activeProxy(ctx))
			mu.Unlock()
			if strings.Contains(requestPrompt(request), "Rewrite this") {
				return stopText("rewritten query"), nil
			}
			return stopText("summarized results"), nil
		}
		noErr(t, e.rt.WebsearchCommand(e.side, "initial command query"))
		note := e.host.waitNote(t, "Open manually: ")
		c := newCuratorClient(t, regexp.MustCompile(`http://\S+`).FindString(note.message))
		c.waitDone()
		if status, body := c.post("/search", map[string]any{"query": "added command query"}); status != 200 || body["error"] != nil {
			t.Fatalf("add-search: %d %v", status, body)
		}
		if status, body := c.post("/rewrite", map[string]any{"query": "rewrite this query"}); status != 200 || body["query"] != "rewritten query" {
			t.Fatalf("rewrite: %d %v", status, body)
		}
		if status, body := c.post("/summarize", map[string]any{"selected": []int{0}}); status != 200 || body["summary"] != "summarized results" {
			t.Fatalf("summarize: %d %v", status, body)
		}
		if status, _ := c.post("/submit", map[string]any{"selected": []int{0}, "summary": "finished"}); status != 200 {
			t.Fatal(status)
		}
		mu.Lock()
		defer mu.Unlock()
		calls := curl.snapshot()
		if len(calls) != 2 {
			t.Fatalf("%d curl calls: %+v", len(calls), calls)
		}
		for _, c := range calls {
			if got := valueAfter(c.Args, "-x"); got != "http://configured-proxy.example:8080" && got != "http://configured-proxy.example:8080/" {
				t.Errorf("a search ran under %q", got)
			}
			if c.Args[len(c.Args)-1] != "https://run.xcrawl.com/v1/serp" {
				t.Errorf("curl asked for %q", c.Args[len(c.Args)-1])
			}
		}
		if len(modelProxies) != 2 || modelProxies[0] != "" || modelProxies[1] != "" {
			t.Errorf("model callbacks ran under %q", modelProxies)
		}
	})
}

func TestWebsearchCommandFlow(t *testing.T) {
	e := newCuratorEnv(t, `{"provider":"brave","curatorTimeoutSeconds":5}`)
	useFakeProvider(t, "brave", func(_ context.Context, q string, _ SearchOptions) (*SearchResponse, error) {
		return &SearchResponse{Answer: "answer " + q, Results: []SearchResult{source(q, "https://example.com/"+strings.ReplaceAll(q, " ", "-"))}}, nil
	})
	noErr(t, e.rt.WebsearchCommand(e.side, " first query ,, second query "))
	pageURL := recvURL(t, e.opened)
	notes := e.host.notifications()
	if len(notes) == 0 || notes[0] != (hostNote{"Opening web search curator...", "info"}) {
		t.Fatalf("%+v", notes)
	}
	c := newCuratorClient(t, pageURL)
	state := c.waitDone()
	if n := len(state["events"].([]any)); n != 2 {
		t.Fatalf("%d events", n)
	}
	matchRE(t, `"queries":\["first query","second query"\]`, c.page())
	// Keep the first query as is, without a summary: the submission arrives as a follow-up message.
	if status, _ := c.post("/submit", map[string]any{"selected": []int{0}, "rawResults": true}); status != 200 {
		t.Fatal(status)
	}
	m := e.host.waitMessage(t, 1)
	if m.customType != "web-search-results" || !m.triggerTurn || m.deliverAs != "followUp" || m.details["curated"] != true || m.details["curatedFrom"] != 2 {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(m.text, "answer first query") || strings.Contains(m.text, "second query") {
		t.Fatalf("%q", m.text)
	}
	// The call state of the command is gone.
	e.rt.cur.mu.Lock()
	left := len(e.rt.cur.active)
	e.rt.cur.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d curators left", left)
	}
}

func TestWebsearchCommandTimesOutWithTheDeterministicSummary(t *testing.T) {
	e := newCuratorEnv(t, `{"provider":"brave","curatorTimeoutSeconds":1,"autoOpenBrowser":false}`)
	useFakeProvider(t, "brave", answerWith("answer", source("S", "https://example.com/s")))
	noErr(t, e.rt.WebsearchCommand(e.side, "q"))
	m := e.host.waitMessage(t, 1)
	summary, _ := m.details["summary"].(map[string]any)
	if !strings.HasPrefix(m.text, "Summary based on the currently selected search results.") || summary["workflow"] != "summary-review" || summary["phase"] != "deterministic-fallback" {
		t.Fatalf("%+v", m)
	}
}

func TestWebsearchCommandReportsAConfigItCannotLoad(t *testing.T) {
	e := newCuratorEnv(t, ``)
	writeConfig(t, ConfigDir(), `{oops`)
	noErr(t, e.rt.WebsearchCommand(e.side, "q"))
	notes := e.host.notifications()
	if len(notes) != 1 || notes[0].level != "error" || !strings.HasPrefix(notes[0].message, "Failed to load web search config: ") {
		t.Fatalf("%+v", notes)
	}
	noURL(t, e.opened)
}

func TestWebsearchCommandWithoutQueriesIsDoneAtOnce(t *testing.T) {
	e := newCuratorEnv(t, `{"provider":"brave"}`)
	useFakeProvider(t, "brave", answerWith("a"))
	noErr(t, e.rt.WebsearchCommand(e.side, "  "))
	c := newCuratorClient(t, recvURL(t, e.opened))
	if s := c.waitDone(); len(s["events"].([]any)) != 0 {
		t.Fatalf("%v", s)
	}
	// The browser is the user's to drive: an added search works.
	if status, body := c.post("/search", map[string]any{"query": "added"}); status != 200 || body["answer"] != "a" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestSaveConfigKeepsTheFileLayout(t *testing.T) {
	extractEnv(t, ``)
	path := ConfigDir() + "/web-search.json"
	// A new file gets the key alone.
	noErr(t, SaveConfigValue("provider", "exa"))
	if got := readFile(t, path); got != "{\n  \"provider\": \"exa\"\n}\n" {
		t.Fatalf("%q", got)
	}
	// An existing key is replaced where it stands.
	writeConfig(t, ConfigDir(), `{"a":1,"provider":"brave","b":{"c":[]}}`)
	noErr(t, SaveConfigValue("provider", "exa"))
	if got := readFile(t, path); got != "{\n  \"a\": 1,\n  \"provider\": \"exa\",\n  \"b\": {\n    \"c\": []\n  }\n}\n" {
		t.Fatalf("%q", got)
	}
	// An invalid file is reported and left alone.
	writeConfig(t, ConfigDir(), `{oops`)
	wantErr(t, SaveConfigValue("provider", "exa"), `^Failed to parse .*web-search\.json: `)
	if readFile(t, path) != `{oops` {
		t.Fatal("the file was rewritten")
	}
}

func TestCuratorNetworkConfig(t *testing.T) {
	old := hostnameFunc
	hostnameFunc = func() (string, error) { return "the-host", nil }
	t.Cleanup(func() { hostnameFunc = old })
	for _, c := range []struct {
		config string
		want   CuratorNetwork
	}{
		{``, CuratorNetwork{false, "localhost", "127.0.0.1"}},
		{`{"curatorRemote":false}`, CuratorNetwork{false, "localhost", "127.0.0.1"}},
		{`{"curatorRemote":true}`, CuratorNetwork{true, "the-host", "0.0.0.0"}},
		{`{"curatorRemote":{}}`, CuratorNetwork{true, "the-host", "0.0.0.0"}},
		{`{"curatorRemote":{"host":" h.example ","bind":"10.0.0.2"}}`, CuratorNetwork{true, "h.example", "10.0.0.2"}},
		{`{"curatorRemote":{"host":3,"bind":"  "}}`, CuratorNetwork{true, "the-host", "0.0.0.0"}},
		{`{"curatorRemote":[true]}`, CuratorNetwork{false, "localhost", "127.0.0.1"}},
		{`not json`, CuratorNetwork{false, "localhost", "127.0.0.1"}},
		{`[]`, CuratorNetwork{false, "localhost", "127.0.0.1"}},
	} {
		extractEnv(t, c.config)
		if got := ResolveCuratorNetworkConfig(); got != c.want {
			t.Errorf("%q: %+v, want %+v", c.config, got, c.want)
		}
	}
}

func TestCuratorTimeoutAndAutoOpenDefaults(t *testing.T) {
	for _, c := range []struct {
		config   string
		timeout  int
		autoOpen bool
	}{
		{``, 20, true},
		{`{"curatorTimeoutSeconds":45.9}`, 45, true},
		{`{"curatorTimeoutSeconds":0.5}`, 20, true},
		{`{"curatorTimeoutSeconds":9999}`, 600, true},
		{`{"curatorTimeoutSeconds":"7"}`, 20, true},
		{`{"curatorRemote":true}`, 60, false},
		{`{"curatorRemote":true,"autoOpenBrowser":true}`, 60, true},
		{`{"autoOpenBrowser":false}`, 20, false},
	} {
		extractEnv(t, c.config)
		got, err := CuratorTimeoutSeconds()
		noErr(t, err)
		if got != c.timeout || ShouldAutoOpenCuratorBrowser(currentRoot()) != c.autoOpen {
			t.Errorf("%q: %d %v, want %d %v", c.config, got, ShouldAutoOpenCuratorBrowser(currentRoot()), c.timeout, c.autoOpen)
		}
	}
}
