package websearch

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/mistral-provider.test.mjs.

const mistralURL = "https://api.mistral.ai/v1/conversations"

const mistralConversationBody = `{"outputs":[
 {"type":"tool.execution","name":"web_search","ignored":true},
 {"type":"message.output","content":[
  {"type":"text","text":"Mistral found the answer."},
  {"type":"tool_reference","tool":"web_search","title":"Mistral source","url":"https://example.com/source","description":"A useful source."},
  {"type":"text","text":"It returned references."}]}]}`

// doerFunc is an HTTPDoer that sees the request itself (its context).
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func mistralSetup(t *testing.T, config string) {
	t.Helper()
	_, dir := isolate(t)
	writeConfig(t, dir, config)
}

func TestUpstream_mistral_provider(t *testing.T) {
	const f = "mistral-provider"

	tw(t, f, "Mistral sends the Conversations request and maps message text and references", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-test-key"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, mistralConversationBody) })
		result, err := SearchWithMistral(bg(), "what shipped?", SearchOptions{})
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != mistralURL || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer mistral-test-key" || c.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%s %s %v", c.URL, c.Method, c.Header)
		}
		if c.Body != `{"inputs":[{"role":"user","content":"what shipped?"}],"stream":false,"model":"mistral-small-latest","tools":[{"type":"web_search"}]}` {
			t.Fatal(c.Body)
		}
		if result.Answer != "Mistral found the answer.\nIt returned references." ||
			!reflect.DeepEqual(result.Results, []SearchResult{{Title: "Mistral source", URL: "https://example.com/source", Snippet: "A useful source."}}) {
			t.Fatalf("%+v", result)
		}
	})

	tw(t, f, "Mistral trims model config, opts into premium explicitly, and folds filters into the prompt", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-test-key","mistralSearchModel":"  custom-mistral-model  ","mistralSearchTool":"web_search_premium"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, mistralConversationBody) })
		_, err := SearchWithMistral(bg(), "filtered query", SearchOptions{NumResults: nf(3), RecencyFilter: "week", DomainFilter: []string{" docs.example.com ", "-ads.example.com"}})
		noErr(t, err)
		body := jsonBody(t, fn.calls[0])
		content := body["inputs"].([]any)[0].(map[string]any)["content"].(string)
		if body["model"] != "custom-mistral-model" || !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": "web_search_premium"}}) {
			t.Fatalf("%v", body)
		}
		for _, re := range []string{`past week`, `Prefer up to 3 distinct sources`, `Only use sources from: docs\.example\.com\.`, `Do not use sources from: ads\.example\.com\.`, `filtered query$`} {
			if !regexp.MustCompile(re).MatchString(content) {
				t.Fatalf("%q does not match %q", content, re)
			}
		}
	})

	tw(t, f, "Mistral applies domain filtering, URL deduplication, null-URL skipping, and result limits locally", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-test-key"}`)
		useNet(t, func(netCall) netReply {
			return reply(200, `{"outputs":[{"type":"message.output","content":[
 {"type":"tool_reference","tool":"web_search","title":"First","url":"https://docs.example.com/first","description":"first"},
 {"type":"tool_reference","tool":"web_search","title":"Duplicate","url":"https://docs.example.com/first","description":"duplicate"},
 {"type":"tool_reference","tool":"web_search","title":"Null URL","url":null,"description":"ignored"},
 {"type":"tool_reference","tool":"web_search","title":"Blocked","url":"https://ads.example.com/blocked","description":"ignored"},
 {"type":"tool_reference","tool":"web_search","title":"Outside","url":"https://other.test/outside","description":"ignored"},
 {"type":"tool_reference","tool":"web_search","title":"Second","url":"https://example.com/second","description":"second"},
 {"type":"tool_reference","tool":"web_search","title":"Third","url":"https://example.com/third","description":"not kept"}]}]}`)
		})
		result, err := SearchWithMistral(bg(), "local filters", SearchOptions{NumResults: nf(2), DomainFilter: []string{"example.com", "-ads.example.com"}})
		noErr(t, err)
		want := []SearchResult{
			{Title: "First", URL: "https://docs.example.com/first", Snippet: "first"},
			{Title: "Second", URL: "https://example.com/second", Snippet: "second"},
		}
		if !reflect.DeepEqual(result.Results, want) {
			t.Fatalf("%+v", result.Results)
		}
	})

	tw(t, f, "Mistral accepts config, environment, and trusted command credential sources", func(t *testing.T) {
		for _, tc := range []struct{ config, env, expected string }{
			{`{"mistralApiKey":"config-key"}`, "", "config-key"},
			{`{}`, "environment-key", "environment-key"},
			{`{"mistralApiKey":"!printf command-key"}`, "", "command-key"},
		} {
			mistralSetup(t, tc.config)
			if tc.env != "" {
				t.Setenv("MISTRAL_API_KEY", tc.env)
			}
			fn := useNet(t, func(netCall) netReply { return reply(200, mistralConversationBody) })
			if !IsMistralAvailable() {
				t.Fatalf("%s: unavailable", tc.config)
			}
			_, err := SearchWithMistral(bg(), "credential source", SearchOptions{})
			noErr(t, err)
			if got := fn.calls[0].Header.Get("Authorization"); got != "Bearer "+tc.expected {
				t.Fatalf("%s: %s", tc.config, got)
			}
		}
	})

	tw(t, f, "Mistral rejects invalid model and tool configuration before making a request", func(t *testing.T) {
		for _, extra := range []string{`"mistralSearchModel":42`, `"mistralSearchModel":"   "`, `"mistralSearchTool":null`, `"mistralSearchTool":"unknown"`} {
			mistralSetup(t, `{"mistralApiKey":"mistral-key",`+extra+`}`)
			n := noNet(t)
			_, err := SearchWithMistral(bg(), "invalid config", SearchOptions{})
			wantErr(t, err, `mistralSearch(?:Model|Tool)`)
			if IsMistralAvailable() || n.count() != 0 {
				t.Fatalf("%s: available or reached the network", extra)
			}
		}
	})

	tw(t, f, "Mistral redacts credentials in HTTP and fetch errors", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-secret"}`)
		useNet(t, func(netCall) netReply { return reply(401, "denied for mistral-secret") })
		_, httpErr := SearchWithMistral(bg(), "http error", SearchOptions{})
		useNet(t, func(netCall) netReply { return netReply{Err: errors.New("fetch failed with mistral-secret")} })
		_, fetchErr := SearchWithMistral(bg(), "fetch error", SearchOptions{})
		for _, err := range []error{httpErr, fetchErr} {
			if err == nil || strings.Contains(err.Error(), "mistral-secret") || !strings.Contains(err.Error(), "[redacted]") {
				t.Fatalf("%v", err)
			}
		}
	})

	tw(t, f, "Mistral rejects malformed and empty Conversations responses", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-key"}`)
		for _, body := range []string{"{", `{}`, `{"outputs":"not-an-array"}`, `{"outputs":[]}`, `{"outputs":[{"type":"message.output","content":[{"type":"unknown"}]}]}`} {
			useNet(t, func(netCall) netReply { return reply(200, body) })
			_, err := SearchWithMistral(bg(), "malformed", SearchOptions{})
			wantErr(t, err, `(?i)invalid JSON|invalid response|no answer or sources`)
		}
	})

	tw(t, f, "Mistral propagates caller abort and passes the combined signal to fetch", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-key"}`)
		ctx, cancel := context.WithCancel(bg())
		defer cancel()
		var signalAborted bool
		t.Cleanup(SetHTTP(doerFunc(func(r *http.Request) (*http.Response, error) {
			cancel()
			signalAborted = r.Context().Err() != nil
			return nil, context.Canceled
		})))
		_, err := SearchWithMistral(ctx, "cancel", SearchOptions{})
		if !signalAborted {
			t.Fatal("the request context was not cancelled with the caller's")
		}
		wantErr(t, err, `(?i)cancel|abort`)
		if !isAbortError(err) {
			t.Fatalf("not an abort: %v", err)
		}
	})

	tw(t, f, "Mistral works through explicit provider and configured searchRouting", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-key","searchRouting":{"providers":["mistral"],"fallbackOn":["network"]}}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, mistralConversationBody) })
		r, err := Search(bg(), "routed", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if r.Provider != "mistral" || !eqStrings(fn.urls(), []string{mistralURL}) {
			t.Fatalf("%s %v", r.Provider, fn.urls())
		}

		mistralSetup(t, `{}`)
		t.Setenv("MISTRAL_API_KEY", "environment-key")
		useNet(t, func(c netCall) netReply {
			if c.URL != mistralURL {
				return netReply{Err: errors.New("unexpected provider")}
			}
			return reply(200, mistralConversationBody)
		})
		r, err = Search(bg(), "explicit", FullSearchOptions{Provider: ProviderSelection{Name: "mistral"}})
		noErr(t, err)
		if r.Provider != "mistral" {
			t.Fatal(r.Provider)
		}
	})

	tw(t, f, "Mistral is excluded from zero-config auto and provider all", func(t *testing.T) {
		mistralSetup(t, `{"mistralApiKey":"mistral-key","braveApiKey":"brave-key"}`)
		fn := useNet(t, func(c netCall) netReply {
			switch c.URL {
			case "https://api.search.brave.com/res/v1/web/search?q=all&count=5":
				return braveOK("Brave", "https://example.com/brave", "ok")
			case mistralURL:
				return netReply{Err: errors.New("Mistral must not run automatically")}
			}
			return netReply{Err: errors.New("expected eligible provider failure: " + c.URL)}
		})
		_, autoErr := Search(bg(), "auto", FullSearchOptions{Provider: Auto})
		all, err := Search(bg(), "all", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		noErr(t, err)
		if all.Provider != "all" {
			t.Fatal(all.Provider)
		}
		for _, u := range fn.urls() {
			if u == mistralURL {
				t.Fatal("Mistral ran")
			}
		}
		if autoErr == nil || strings.Contains(autoErr.Error(), "Mistral") {
			t.Fatalf("%v", autoErr)
		}
	})

	tskip(t, f, "Curator page exposes Mistral as an available manual provider", "the search curator page is the websearch-curator slice (it renders the provider buttons of curator-page.ts); the Mistral availability it displays is IsMistralAvailable, covered above")
}
