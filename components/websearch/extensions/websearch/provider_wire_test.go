package websearch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The providers below call fixed endpoints, so no scenario server can receive their traffic under
// the original. port/record-provider-requests.mjs runs the vendored original with a recording
// `fetch` and writes port/provider-wire.json; this test replays the same inputs against the port
// and compares every request (method, URL, headers, body) and the result.

type wireCase struct {
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Fn        string            `json:"fn"`
	Env       map[string]string `json:"env"`
	Args      []json.RawMessage `json:"args"`
	Responses []struct {
		Status int `json:"status"`
		Body   any `json:"body"`
	} `json:"responses"`
	Expect struct {
		Calls []struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    any               `json:"body"`
		} `json:"calls"`
		Result any     `json:"result"`
		Error  *string `json:"error"`
	} `json:"expect"`
}

type wireOptions struct {
	NumResults     *float64 `json:"numResults"`
	RecencyFilter  string   `json:"recencyFilter"`
	DomainFilter   []string `json:"domainFilter"`
	IncludeContent bool     `json:"includeContent"`
	Prompt         string   `json:"prompt"`
	TimeoutMs      *int64   `json:"timeoutMs"`
}

func wireProject(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if k == "error" && val == nil {
				continue
			}
			out[k] = wireProject(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = wireProject(x[i])
		}
		return out
	}
	return v
}

func wireJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	noErr(t, err)
	var out any
	noErr(t, json.Unmarshal(b, &out))
	return wireProject(out)
}

func wireSearchResult(r *SearchResponse) any {
	if r == nil {
		return nil
	}
	m := map[string]any{"answer": r.Answer, "results": []any{}}
	for _, s := range r.Results {
		m["results"] = append(m["results"].([]any), map[string]any{"title": s.Title, "url": s.URL, "snippet": s.Snippet})
	}
	if len(r.InlineContent) > 0 {
		var inline []any
		for _, c := range r.InlineContent {
			inline = append(inline, map[string]any{"url": c.URL, "title": c.Title, "content": c.Content})
		}
		m["inlineContent"] = inline
	}
	return m
}

func wireExtractResult(r *ExtractedContent) any {
	if r == nil {
		return nil
	}
	return map[string]any{"url": r.URL, "title": r.Title, "content": r.Content}
}

func TestProviderWireFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash("../../port/provider-wire.json"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("port/provider-wire.json is not beside this module (a mutation run copies the module alone); the repository run checks it")
		}
		t.Fatal(err)
	}
	var cases []wireCase
	noErr(t, json.Unmarshal(data, &cases))
	if len(cases) == 0 {
		t.Fatal("no recorded cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			isolate(t)
			for k, v := range c.Env {
				t.Setenv(k, v)
			}
			i := 0
			fn := useNet(t, func(netCall) netReply {
				r := c.Responses[min(i, len(c.Responses)-1)]
				i++
				if s, ok := r.Body.(string); ok {
					return reply(r.Status, s)
				}
				return jsonReply(r.Body).withStatus(r.Status)
			})
			var first string
			noErr(t, json.Unmarshal(c.Args[0], &first))
			var o wireOptions
			noErr(t, json.Unmarshal(c.Args[len(c.Args)-1], &o))
			so := SearchOptions{NumResults: o.NumResults, RecencyFilter: o.RecencyFilter, DomainFilter: o.DomainFilter, IncludeContent: o.IncludeContent}
			eo := ExtractOptions{Prompt: o.Prompt, TimeoutMs: o.TimeoutMs, Lookup: pubLookup}
			ctx := context.Background()
			var got any
			switch c.Fn {
			case "searchWithTinyFish":
				got, err = searchWire(SearchWithTinyFish(ctx, first, so))
			case "searchWithSearch1API":
				got, err = searchWire(SearchWithSearch1API(ctx, first, so))
			case "searchWithSearchinfinity":
				got, err = searchWire(SearchWithSearchinfinity(ctx, first, so))
			case "searchWithQuerit":
				got, err = searchWire(SearchWithQuerit(ctx, first, so))
			case "searchWithParallel":
				got, err = searchWire(SearchWithParallel(ctx, first, so))
			case "searchWithParallelMcp":
				got, err = searchWire(SearchWithParallelMCP(ctx, first, so))
			case "searchWithOllama":
				got, err = searchWire(SearchWithOllama(ctx, first, so))
			case "searchWithKagi":
				got, err = searchWire(SearchWithKagi(ctx, first, so))
			case "searchWithBrightData":
				got, err = searchWire(SearchWithBrightData(ctx, first, so))
			case "extractWithTinyFish":
				got, err = extractWire(ExtractWithTinyFish(ctx, first, eo))
			case "extractWithSearch1API":
				got, err = extractWire(ExtractWithSearch1API(ctx, first, eo))
			case "extractWithQuerit":
				got, err = extractWire(ExtractWithQuerit(ctx, first, eo))
			case "extractWithParallel":
				got, err = extractWire(ExtractWithParallel(ctx, first, eo))
			case "extractWithParallelMcp":
				got, err = extractWire(ExtractWithParallelMCP(ctx, first, eo))
			case "extractWithOllama":
				got, err = extractWire(ExtractWithOllama(ctx, first, eo, SsrfConfig{}))
			case "extractWithKagi":
				got, err = extractWire(ExtractWithKagi(ctx, first, eo, SsrfConfig{}))
			case "extractWithBrightDataUnlocker":
				got, err = extractWire(ExtractWithBrightDataUnlocker(ctx, first, eo, SsrfConfig{}))
			default:
				t.Fatalf("unknown function %s", c.Fn)
			}
			if c.Expect.Error != nil {
				if err == nil || err.Error() != *c.Expect.Error {
					t.Fatalf("error = %v, want %q", err, *c.Expect.Error)
				}
			} else {
				noErr(t, err)
			}
			if len(fn.calls) != len(c.Expect.Calls) {
				t.Fatalf("%d requests, want %d: %v", len(fn.calls), len(c.Expect.Calls), fn.urls())
			}
			for n, want := range c.Expect.Calls {
				got := fn.calls[n]
				if got.Method != want.Method || got.URL != want.URL {
					t.Fatalf("request %d: %s %s, want %s %s", n, got.Method, got.URL, want.Method, want.URL)
				}
				headers := map[string]string{}
				for k := range got.Header {
					headers[strings.ToLower(k)] = got.Header.Get(k)
				}
				if !reflect.DeepEqual(headers, want.Headers) {
					t.Fatalf("request %d headers: %v, want %v", n, headers, want.Headers)
				}
				var body any
				if got.Body != "" {
					noErr(t, json.Unmarshal([]byte(got.Body), &body))
				}
				if !reflect.DeepEqual(body, want.Body) {
					t.Fatalf("request %d body: %s, want %v", n, got.Body, want.Body)
				}
			}
			if c.Expect.Error == nil && !reflect.DeepEqual(wireJSON(t, got), wireProject(c.Expect.Result)) {
				t.Fatalf("result: %v\nwant:   %v", wireJSON(t, got), wireProject(c.Expect.Result))
			}
		})
	}
}

func searchWire(r *SearchResponse, err error) (any, error) { return wireSearchResult(r), err }

func extractWire(r *ExtractedContent, err error) (any, error) { return wireExtractResult(r), err }
