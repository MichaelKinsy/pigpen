package websearch

import (
	"context"
	"strings"
	"testing"
)

// The call sites of the hosted and model providers that the recorded fixtures reach only on their
// success path: an HTTP 500 and a cancelled caller, with the entry each original leaves (the
// try/catch blocks of tinyfish.ts, search1api.ts, querit.ts, searchinfinity.ts, xai-search.ts,
// mistral-search.ts, ollama.ts, parallel.ts and parallel-mcp.ts).
func TestActivityEntriesOfHostedProviderFailures(t *testing.T) {
	type searchFn = func(context.Context, string, SearchOptions) (*SearchResponse, error)
	cases := []struct {
		name string
		env  map[string]string
		fn   searchFn
		// status: the original completes the entry with the HTTP status (ollama.ts:154, parallel.ts:359);
		// otherwise it records the error message.
		status bool
		// openOnCancel: the catch block asks the caller's signal (querit.ts:354) and every provider here
		// completes a cancelled call with status 0.
		errPrefix string
	}{
		{"tinyfish", map[string]string{"TINYFISH_API_KEY": "k"}, SearchWithTinyFish, false, "TinyFish"},
		{"search1api", map[string]string{"SEARCH1API_KEY": "k"}, SearchWithSearch1API, false, "Search1API"},
		{"querit", map[string]string{"QUERIT_API_KEY": "k"}, SearchWithQuerit, false, "Querit"},
		{"searchinfinity", map[string]string{"SEARCHINFINITY_API_KEY": "k"}, SearchWithSearchinfinity, false, "Searchinfinity"},
		{"xai", map[string]string{"XAI_API_KEY": "k"}, SearchWithXai, false, "xAI API error 500"},
		{"mistral", map[string]string{"MISTRAL_API_KEY": "k"}, SearchWithMistral, false, "Mistral API error 500"},
		{"parallel-mcp", nil, SearchWithParallelMCP, false, ""},
		{"ollama", map[string]string{"OLLAMA_API_KEY": "k"}, SearchWithOllama, true, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name+" HTTP 500", func(t *testing.T) {
			isolate(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			useNet(t, func(netCall) netReply { return reply(500, "boom") })
			Activity.Clear()
			if _, err := c.fn(bg(), "the query", SearchOptions{}); err == nil {
				t.Fatal("no error")
			}
			got := Activity.Entries()
			if len(got) != 1 || got[0].Type != "api" || got[0].Query != "the query" && !strings.HasPrefix(got[0].Query, "Parallel") {
				t.Fatalf("%+v", got)
			}
			if c.status {
				if got[0].Status == nil || *got[0].Status != 500 || got[0].Error != "" {
					t.Fatalf("%+v", got[0])
				}
			} else if got[0].Status != nil || !strings.Contains(got[0].Error, "500") || !strings.HasPrefix(got[0].Error, c.errPrefix) {
				t.Fatalf("%+v", got[0])
			}
		})
		t.Run(c.name+" cancelled caller", func(t *testing.T) {
			isolate(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			useNet(t, func(netCall) netReply { return reply(200, "{}") })
			ctx, cancel := context.WithCancel(bg())
			cancel()
			Activity.Clear()
			if _, err := c.fn(ctx, "the query", SearchOptions{}); err == nil {
				t.Fatal("no error")
			}
			got := Activity.Entries()
			if len(got) != 1 || got[0].Status == nil || *got[0].Status != 0 || got[0].Error != "" {
				t.Fatalf("%+v", got)
			}
		})
	}
}

// A successful hosted call completes its entry (tinyfish.ts:324, search1api.ts:222, parallel-mcp.ts:193 use the constant 200).
func TestActivityEntryOfAnExtraction(t *testing.T) {
	isolate(t)
	t.Setenv("OLLAMA_API_KEY", "k")
	useNet(t, func(netCall) netReply { return reply(200, `{"title":"T","content":" body ","links":[]}`) })
	Activity.Clear()
	got, err := ExtractWithOllama(bg(), "https://93.184.216.34/page", ExtractOptions{}, SsrfConfig{})
	noErr(t, err)
	if got == nil || got.Content != "body" {
		t.Fatalf("%+v", got)
	}
	e := Activity.Entries()
	if len(e) != 1 || e[0].Type != "api" || e[0].Query != "ollama fetch: https://93.184.216.34/page" || e[0].Status == nil || *e[0].Status != 200 {
		t.Fatalf("%+v", e)
	}
}

// youtube-extract.ts:111-136 and video-extract.ts:179-200: the entries of the media extractors.
func TestActivityEntriesOfTheYouTubeExtractor(t *testing.T) {
	isolate(t)
	useNet(t, func(c netCall) netReply { return reply(500, "x") })
	Activity.Clear()
	got, err := ExtractYouTube(bg(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "", "")
	noErr(t, err)
	if got == nil || got.Error == nil {
		t.Fatalf("%+v", got)
	}
	e := Activity.Entries()
	if len(e) != 1 || e[0].Type != "fetch" || e[0].URL != "youtube.com/dQw4w9WgXcQ" || e[0].Status != nil || e[0].Error != *got.Error {
		t.Fatalf("%+v", e)
	}
	ctx, cancel := context.WithCancel(bg())
	cancel()
	Activity.Clear()
	if got, _ = ExtractYouTube(ctx, "https://youtu.be/dQw4w9WgXcQ", "", ""); got != nil {
		t.Fatalf("%+v", got)
	}
	e = Activity.Entries()
	if len(e) != 1 || e[0].Status == nil || *e[0].Status != 0 {
		t.Fatalf("%+v", e)
	}
}
