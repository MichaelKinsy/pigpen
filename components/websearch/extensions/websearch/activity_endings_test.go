package websearch

import (
	"context"
	"errors"
	"testing"
)

// How the entry of one provider call ends, one case per ending of the original's try/catch (the
// logComplete and logError calls of crawl4ai.ts, firecrawl.ts, brightdata-unlocker.ts, kagi.ts and
// ollama.ts). A status ending completes the entry with the HTTP status, an error ending keeps the
// message and no status, and a cancelled caller completes it with status 0.

type ending struct {
	name   string
	reply  netReply
	cancel bool
	status int    // completed with this status (a cancelled caller: 0) ...
	errHas string // ... or closed with an error containing this
	anyErr bool   // ... or closed with some error
}

var errReadFailed = errors.New("read failed")

// runEndings runs call once per ending and requires exactly one entry of the given type.
func runEndings(t *testing.T, kind string, setup func(t *testing.T), call func(ctx context.Context), cases []ending) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			setup(t)
			useNet(t, func(netCall) netReply { return c.reply })
			Activity.Clear()
			ctx := bg()
			if c.cancel {
				var cancel context.CancelFunc
				ctx, cancel = contextCanceled()
				defer cancel()
			}
			call(ctx)
			var got []ActivityEntry
			for _, e := range Activity.Entries() {
				if e.Type == kind {
					got = append(got, e)
				}
			}
			if len(got) != 1 {
				t.Fatalf("%+v", Activity.Entries())
			}
			if c.anyErr {
				if got[0].EndTime == 0 || got[0].Status != nil || got[0].Error == "" {
					t.Fatalf("want an entry closed with an error: %+v", got[0])
				}
			} else if c.errHas != "" {
				wantLoggedError(t, got[0], c.errHas)
			} else {
				wantLoggedStatus(t, got[0], c.status)
			}
		})
	}
}

func TestActivityEndingsOfCrawl4AI(t *testing.T) {
	runEndings(t, "fetch", func(t *testing.T) { c4aEnv(t, "https://crawl.example.com") },
		func(ctx context.Context) { _, _ = ExtractWithCrawl4ai(ctx, "https://example.com/a", pageOpts(), nil) },
		[]ending{
			{name: "success", reply: c4aOK("# Title\nbody"), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), errHas: "Crawl4AI md error 500"},
			{name: "a body that cannot be read", reply: netReply{Status: 200, BodyErr: errReadFailed}, errHas: "read failed"},
			{name: "invalid JSON", reply: reply(200, "<<not json"), errHas: "invalid JSON"},
			{name: "an array", reply: reply(200, "[1]"), errHas: "unexpected response shape"},
			{name: "success false", reply: jsonReply(map[string]any{"success": false, "error": "no way"}), errHas: "unsuccessful: no way"},
			{name: "success of another type", reply: jsonReply(map[string]any{"success": "yes"}), errHas: "unexpected response shape"},
			{name: "no markdown", reply: jsonReply(map[string]any{"success": true}), errHas: "markdown in an unexpected shape"},
			{name: "a cancelled caller", reply: c4aOK("x"), cancel: true, status: 0},
		})
}

// keyed isolates the test and sets one credential.
func keyed(name, value string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()
		isolate(t)
		t.Setenv(name, value)
	}
}

func firecrawlEnv(t *testing.T) {
	t.Helper()
	isolate(t)
	t.Setenv("FIRECRAWL_BASE_URL", "https://crawl.example.com")
}

func firecrawlEndings() []ending {
	return []ending{
		{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
		{name: "HTTP 500", reply: reply(500, "boom"), errHas: "error 500"},
		{name: "a body that cannot be read", reply: netReply{Status: 200, BodyErr: errReadFailed}, errHas: "read failed"},
		{name: "invalid JSON", reply: reply(200, "<<not json"), errHas: "invalid JSON"},
		{name: "an array", reply: reply(200, "[1]"), errHas: "unexpected response shape"},
		{name: "success false", reply: jsonReply(map[string]any{"success": false, "error": "no way"}), errHas: "unsuccessful: no way"},
		{name: "success of another type", reply: jsonReply(map[string]any{"success": "yes"}), errHas: "unexpected response shape"},
		{name: "a cancelled caller", reply: reply(200, "{}"), cancel: true, status: 0},
	}
}

func TestActivityEndingsOfFirecrawlSearch(t *testing.T) {
	runEndings(t, "api", firecrawlEnv,
		func(ctx context.Context) {
			_, _ = SearchWithFirecrawl(ctx, "the query", SearchOptions{Lookup: pubLookup})
		},
		append(firecrawlEndings(), ending{name: "success", reply: jsonReply(map[string]any{"success": true, "data": []any{}}), status: 200}))
	Activity.Clear()
	firecrawlEnv(t)
	useNet(t, func(netCall) netReply { return jsonReply(map[string]any{"success": true, "data": []any{}}) })
	_, _ = SearchWithFirecrawl(bg(), "the query", SearchOptions{Lookup: pubLookup})
	if e := Activity.Entries(); len(e) != 1 || e[0].Type != "api" || e[0].Query != "the query" {
		t.Fatalf("%+v", e)
	}
}

func TestActivityEndingsOfFirecrawlExtract(t *testing.T) {
	runEndings(t, "fetch", firecrawlEnv,
		func(ctx context.Context) { _, _ = ExtractWithFirecrawl(ctx, "https://example.com/a", pageOpts(), nil) },
		append(firecrawlEndings(), ending{name: "success", reply: jsonReply(map[string]any{"success": true, "data": map[string]any{"markdown": "# T\nbody"}}), status: 200}))
}

func TestActivityEndingsOfTheBrightDataUnlocker(t *testing.T) {
	runEndings(t, "fetch", func(t *testing.T) { bdSetup(t, "", bdUnlocker) },
		func(ctx context.Context) {
			_, _ = ExtractWithBrightDataUnlocker(ctx, "https://example.com/article", pageOpts(), SsrfConfig{})
		},
		[]ending{
			{name: "success", reply: reply(200, "# Title\n\nbody"), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), errHas: "Web Unlocker error 500"},
			{name: "a body that cannot be read", reply: netReply{Status: 200, BodyErr: errReadFailed}, errHas: "read failed"},
			{name: "a cancelled caller", reply: reply(200, "x"), cancel: true, status: 0},
		})
}

func TestActivityEndingsOfKagiExtract(t *testing.T) {
	runEndings(t, "api", keyed("KAGI_API_KEY", "kagi-key"),
		func(ctx context.Context) {
			_, _ = ExtractWithKagi(ctx, "https://example.com/a", pageOpts(), SsrfConfig{})
		},
		[]ending{
			{name: "success", reply: jsonReply(map[string]any{"data": []any{map[string]any{"url": "https://example.com/a", "markdown": "body"}}}), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), status: 500},
			{name: "invalid JSON", reply: reply(200, "<<not json"), status: 200},
			{name: "a cancelled caller", reply: reply(200, "{}"), cancel: true, status: 0},
		})
}

func TestActivityEndingsOfOllamaExtract(t *testing.T) {
	runEndings(t, "api", keyed("OLLAMA_API_KEY", "ollama-key"),
		func(ctx context.Context) {
			_, _ = ExtractWithOllama(ctx, "https://example.com/a", pageOpts(), SsrfConfig{})
		},
		[]ending{
			{name: "success", reply: jsonReply(map[string]any{"title": "T", "content": "body", "links": []any{}}), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), status: 500},
			{name: "invalid JSON", reply: reply(200, "<<not json"), status: 200},
			{name: "a cancelled caller", reply: reply(200, "{}"), cancel: true, status: 0},
		})
}

const geminiOKBody = `{"candidates":[{"content":{"parts":[{"text":"answer text that is long enough to be kept as page content for the extraction path"}]}}]}`

func geminiKeyed(t *testing.T) {
	t.Helper()
	geminiAuto(t, "")
	t.Setenv("GEMINI_API_KEY", "gem-key")
}

func TestActivityEndingsOfGeminiSearch(t *testing.T) {
	runEndings(t, "api", geminiKeyed,
		func(ctx context.Context) { _, _ = searchWithGeminiAPI(ctx, "the query", SearchOptions{}) },
		[]ending{
			{name: "success", reply: reply(200, geminiOKBody), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), errHas: "500"},
			{name: "invalid JSON", reply: reply(200, "<<not json"), anyErr: true},
			{name: "a cancelled caller", reply: reply(200, geminiOKBody), cancel: true, status: 0},
		})
}

func TestActivityEndingsOfGeminiURLContext(t *testing.T) {
	runEndings(t, "api", geminiKeyed,
		func(ctx context.Context) { _, _ = ExtractWithURLContext(ctx, "https://example.com/a") },
		[]ending{
			{name: "success", reply: reply(200, geminiOKBody), status: 200},
			{name: "transport failure", reply: netReply{Err: errTransport}, errHas: "connect failed"},
			{name: "HTTP 500", reply: reply(500, "boom"), status: 500},
			{name: "invalid JSON", reply: reply(200, "<<not json"), anyErr: true},
		})
}

func TestActivityEndingsOfGeminiWeb(t *testing.T) {
	for _, c := range []struct {
		name string
		web  *fakeGeminiWeb
		want int
	}{
		{"an answer", &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, answer: "an answer long enough to count as the content of the page"}, 200},
		{"a failure", &fakeGeminiWeb{cookies: GeminiWebCookies{"c": "v"}, queryErr: errTransport}, -1},
	} {
		c := c
		for _, call := range []struct {
			name string
			kind string
			fn   func()
			want string
		}{
			{"search", "api", func() { _, _ = searchWithGeminiWeb(bg(), "the query", SearchOptions{}) }, "the query"},
			{"extract", "api", func() { _, _ = ExtractWithGeminiWeb(bg(), "https://example.com/a") }, "gemini_web: https://example.com/a"},
		} {
			call := call
			t.Run(call.name+" with "+c.name, func(t *testing.T) {
				geminiAuto(t, "")
				t.Cleanup(SetGeminiWebClient(c.web))
				Activity.Clear()
				call.fn()
				got := Activity.Entries()
				if len(got) != 1 || got[0].Query != call.want {
					t.Fatalf("%+v", got)
				}
				if c.want >= 0 {
					wantLoggedStatus(t, got[0], c.want)
				} else {
					wantLoggedError(t, got[0], "connect failed")
				}
			})
		}
	}
}
