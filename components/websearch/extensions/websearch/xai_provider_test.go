package websearch

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Twins of test/xai-provider.test.mjs. The upstream child processes replace globalThis.fetch;
// `useNet` is the same seam here, and the config is written into an isolated agent directory.

const xaiURL = "https://api.x.ai/v1/responses"

const xaiSuccessBody = `{"output":[
 {"type":"web_search_call","action":{"sources":[{"url":"https://x.ai/news","title":"xAI News"}]}},
 {"type":"message","content":[{"type":"output_text","text":"Grok Imagine v1.5 shipped with reference images.",
   "annotations":[{"type":"url_citation","url":"https://x.ai/news/grok-imagine","title":"Grok Imagine","start_index":0,"end_index":12}]}]}
],"citations":["https://x.ai/news/grok-imagine","https://x.ai/citation-only"]}`

const xaiXSearchBody = `{"output":[
 {"type":"x_search_call","action":{"sources":[
   {"url":"https://x.com/i/status/raw","title":"Raw X post"},
   {"url":"https://x.com/i/status/annotated","title":"Raw duplicate"},
   {"ignored":true}]},
  "sources":["https://x.com/i/status/source-string"]},
 {"type":"message","content":[{"type":"output_text","text":"People are discussing the release on X.","annotations":[
   {"type":"url_citation","url":"https://x.com/i/status/annotated","title":"Annotated X post","start_index":0,"end_index":6},
   {"type":"url_citation","url":"https://x.com/i/status/answer","title":"Answer citation","start_index":7,"end_index":15}]}]}
],"citations":["https://x.com/i/status/citation-only",{"url":"https://x.com/i/status/citation-object","title":"Citation object"},42]}`

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func xaiSetup(t *testing.T, config string) {
	t.Helper()
	_, dir := isolate(t)
	writeConfig(t, dir, config)
}

func TestUpstream_xai_provider(t *testing.T) {
	const f = "xai-provider"

	tw(t, f, "xAI posts the verified minimal Responses body and reads citation source shapes", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		result, err := SearchWithXai(bg(), "what shipped?", SearchOptions{})
		noErr(t, err)
		c := fn.calls[0]
		if c.URL != xaiURL || c.Method != "POST" || c.Header.Get("Authorization") != "Bearer xai-key" {
			t.Fatalf("%s %s %v", c.URL, c.Method, c.Header)
		}
		body := jsonBody(t, c)
		// Only these three fields are verified against a live account.
		if !reflect.DeepEqual(sortedKeys(body), []string{"input", "model", "tools"}) {
			t.Fatalf("body keys %v", sortedKeys(body))
		}
		if !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": "web_search"}}) {
			t.Fatalf("tools %v", body["tools"])
		}
		if !regexp.MustCompile(`what shipped\?$`).MatchString(body["input"].(string)) {
			t.Fatalf("input %q", body["input"])
		}
		if !strings.Contains(result.Answer, "Grok Imagine v1.5 shipped") {
			t.Fatal(result.Answer)
		}
		// Annotations carry titles, so they win the dedupe and come first.
		if !eqStrings(urlsOf(result.Results), []string{"https://x.ai/news/grok-imagine", "https://x.ai/news", "https://x.ai/citation-only"}) || result.Results[0].Title != "Grok Imagine" {
			t.Fatalf("%+v", result.Results)
		}
	})

	tw(t, f, "xAI opts into web and X tools and keeps annotation sources first", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key","xaiSearchTools":["web_search","x_search"]}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiXSearchBody) })
		result, err := SearchWithXai(bg(), "what are people saying?", SearchOptions{})
		noErr(t, err)
		body := jsonBody(t, fn.calls[0])
		if body["model"] != "grok-4.6" || !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": "web_search"}, map[string]any{"type": "x_search"}}) {
			t.Fatalf("%v", body)
		}
		if !strings.Contains(body["input"].(string), "Search the web and X") {
			t.Fatal(body["input"])
		}
		want := []string{"https://x.com/i/status/annotated", "https://x.com/i/status/answer", "https://x.com/i/status/raw", "https://x.com/i/status/source-string", "https://x.com/i/status/citation-only", "https://x.com/i/status/citation-object"}
		if !eqStrings(urlsOf(result.Results), want) || result.Results[0].Title != "Annotated X post" || result.Results[len(want)-1].Title != "Citation object" {
			t.Fatalf("%+v", result.Results)
		}
	})

	tw(t, f, "xAI folds recency and domain filters into the prompt rather than tool params", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(bg(), "filtered", SearchOptions{RecencyFilter: "week", DomainFilter: []string{"example.com", "-spam.example"}, NumResults: nf(4)})
		noErr(t, err)
		body := jsonBody(t, fn.calls[0])
		input := body["input"].(string)
		if !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": "web_search"}}) {
			t.Fatal(body["tools"])
		}
		for _, re := range []string{`past week`, `Only use sources from: example\.com\.`, `Do not use sources from: spam\.example\.`, `around 4 distinct sources`} {
			if !regexp.MustCompile(re).MatchString(input) {
				t.Fatalf("%q does not match %q", input, re)
			}
		}
	})

	tw(t, f, "xaiSearchModel pins the model id on the api-key path", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key","xaiSearchModel":"  grok-5  "}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(bg(), "pinned", SearchOptions{})
		noErr(t, err)
		if jsonBody(t, fn.calls[0])["model"] != "grok-5" {
			t.Fatal(fn.calls[0].Body)
		}
	})

	tw(t, f, "xAI defaults to the first candidate model when xaiSearchModel is unset", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key"}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		_, err := SearchWithXai(bg(), "default", SearchOptions{})
		noErr(t, err)
		if jsonBody(t, fn.calls[0])["model"] != "grok-4.5" {
			t.Fatal(fn.calls[0].Body)
		}
	})

	tw(t, f, "a non-string or empty xaiSearchModel is rejected rather than silently ignored", func(t *testing.T) {
		for _, bad := range []string{`42`, `"   "`} {
			xaiSetup(t, `{"xaiApiKey":"xai-key","xaiSearchModel":`+bad+`}`)
			n := noNet(t)
			_, err := SearchWithXai(bg(), "bad model", SearchOptions{})
			wantErr(t, err, `xaiSearchModel .* must be a non-empty string`)
			if n.count() != 0 {
				t.Fatal("reached the network")
			}
		}
	})

	tw(t, f, "xaiSearchTools is validated and may opt into X-only search", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key","xaiSearchTools":["x_search"]}`)
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiXSearchBody) })
		_, err := SearchWithXai(bg(), "x only", SearchOptions{})
		noErr(t, err)
		body := jsonBody(t, fn.calls[0])
		if !reflect.DeepEqual(body["tools"], []any{map[string]any{"type": "x_search"}}) || !strings.HasPrefix(body["input"].(string), "Search X and answer") {
			t.Fatalf("%v", body)
		}

		for _, bad := range []string{`null`, `"x_search"`, `[]`, `["x_search","x_search"]`, `["x_search","unknown"]`, `["web_search",42]`} {
			xaiSetup(t, `{"xaiApiKey":"xai-key","xaiSearchTools":`+bad+`}`)
			n := noNet(t)
			if IsXaiSearchAvailable(nil) {
				t.Fatalf("xaiSearchTools %s should only make xAI unavailable", bad)
			}
			_, err := SearchWithXai(bg(), "bad tools", SearchOptions{})
			wantErr(t, err, `xaiSearchTools .* (?:non-empty array|web_search or x_search|duplicates)`)
			if n.count() != 0 {
				t.Fatal("reached the network")
			}
		}
	})

	tw(t, f, "explicit xai provider routing works", func(t *testing.T) {
		xaiSetup(t, `{}`)
		t.Setenv("XAI_API_KEY", "xai-env-key")
		fn := useNet(t, func(netCall) netReply { return reply(200, xaiSuccessBody) })
		r, err := Search(bg(), "route", FullSearchOptions{Provider: ProviderSelection{Name: "xai"}})
		noErr(t, err)
		if r.Provider != "xai" || !eqStrings(fn.urls(), []string{xaiURL}) {
			t.Fatalf("%s %v", r.Provider, fn.urls())
		}
	})

	tw(t, f, "xAI is never part of auto fallback, even with a key configured", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key"}`)
		fn := useNet(t, func(netCall) netReply { return netReply{Err: errNoNetwork} })
		_, err := Search(bg(), "auto", FullSearchOptions{Provider: Auto})
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, u := range fn.urls() {
			if strings.HasPrefix(u, "https://api.x.ai/") {
				t.Fatalf("auto reached xAI: %s", u)
			}
		}
	})

	tw(t, f, `provider "all" does not fan out to xAI`, func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key","braveApiKey":"brave-key"}`)
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.x.ai/") {
				return netReply{Err: errNoNetwork}
			}
			return braveOK("Brave", "https://example.com/brave", "ok")
		})
		r, err := Search(bg(), "fan out", FullSearchOptions{Provider: ProviderSelection{Name: "all"}})
		noErr(t, err)
		if r.Provider != "all" {
			t.Fatal(r.Provider)
		}
		for _, u := range fn.urls() {
			if strings.HasPrefix(u, "https://api.x.ai/") {
				t.Fatalf("all reached xAI: %s", u)
			}
		}
	})

	tw(t, f, "xAI without any credential explains all three ways to supply one", func(t *testing.T) {
		xaiSetup(t, `{}`)
		noNet(t)
		if IsXaiSearchAvailable(nil) {
			t.Fatal("available without a credential")
		}
		_, err := SearchWithXai(bg(), "nope", SearchOptions{})
		for _, re := range []string{`SuperGrok or X Premium subscription`, `xaiApiKey`, `XAI_API_KEY`} {
			wantErr(t, err, re)
		}
	})

	tw(t, f, "xAI HTTP errors redact the credential", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-secret"}`)
		useNet(t, func(netCall) netReply { return reply(401, "denied for xai-secret") })
		_, err := SearchWithXai(bg(), "redact", SearchOptions{})
		if err == nil || strings.Contains(err.Error(), "xai-secret") || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("%v", err)
		}
	})

	tw(t, f, "xAI answers with no answer text and no sources fail loudly", func(t *testing.T) {
		xaiSetup(t, `{"xaiApiKey":"xai-key"}`)
		useNet(t, func(netCall) netReply { return reply(200, `{"output":[]}`) })
		_, err := SearchWithXai(bg(), "empty", SearchOptions{})
		wantErr(t, err, `returned no answer or sources`)
	})
}
