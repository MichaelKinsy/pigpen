package websearch

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The wire proof for the model-backed providers. Their endpoints are fixed (api.x.ai,
// api.kimi.com, api.mistral.ai, api.openai.com, chatgpt.com), so a pigeq scenario server cannot
// receive their traffic under the unmodified original. port/record-model-requests.mjs runs the
// original modules with a recording `fetch` and checks in what they sent and returned; this test
// replays the same inputs against the port and requires the same requests (method, URL, headers,
// raw body) and the same result or error.

type wireCase struct {
	Name     string         `json:"name"`
	Provider string         `json:"provider"`
	Config   map[string]any `json:"config"`
	Query    string         `json:"query"`
	Options  struct {
		RecencyFilter string   `json:"recencyFilter"`
		DomainFilter  []string `json:"domainFilter"`
		NumResults    *float64 `json:"numResults"`
	} `json:"options"`
	SessionID    string         `json:"sessionId"`
	CurrentModel map[string]any `json:"currentModel"`
	Registry     *struct {
		Models []map[string]any          `json:"models"`
		Auth   map[string]map[string]any `json:"auth"`
	} `json:"registry"`
	Response struct {
		JSON   any    `json:"json"`
		Text   string `json:"text"`
		Status int    `json:"status"`
	} `json:"response"`
	Requests []struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"requests"`
	Result *SearchResponse `json:"result"`
	Error  *string         `json:"error"`
}

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func TestModelProviderWireFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../port/fixtures/model-provider-requests.json")
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("the Package files are not beside this module (a mutation run copies the module alone); the repository run checks them")
		}
		t.Fatal(err)
	}
	var fixture struct {
		Cases []wireCase `json:"cases"`
	}
	noErr(t, json.Unmarshal(raw, &fixture))
	if len(fixture.Cases) == 0 {
		t.Fatal("empty fixture")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			_, dir := isolate(t)
			writeConfig(t, dir, string(mustJSON(c.Config)))
			status := c.Response.Status
			if status == 0 {
				status = 200
			}
			body := c.Response.Text
			if c.Response.JSON != nil {
				body = string(mustJSON(c.Response.JSON))
			}
			fn := useNet(t, func(netCall) netReply { return reply(status, body) })

			ctx := bg()
			if c.Registry != nil {
				reg := &fakeRegistry{models: c.Registry.Models, auth: func(m map[string]any) (map[string]any, error) {
					if a, ok := c.Registry.Auth[jsString(m["provider"])]; ok {
						return a, nil
					}
					return map[string]any{"ok": false, "error": "no auth"}, nil
				}}
				current := c.CurrentModel
				if current == nil {
					current = c.Registry.Models[0]
				}
				ctx = withExt(&fakeExt{model: current, reg: reg, session: c.SessionID})
			}
			o := SearchOptions{RecencyFilter: c.Options.RecencyFilter, DomainFilter: c.Options.DomainFilter, NumResults: c.Options.NumResults}
			var got *SearchResponse
			var err error
			switch c.Provider {
			case "xai":
				got, err = SearchWithXai(ctx, c.Query, o)
			case "kimi":
				got, err = SearchWithKimi(ctx, c.Query, o)
			case "mistral":
				got, err = SearchWithMistral(ctx, c.Query, o)
			case "openai-current":
				got, err = SearchWithCurrentModelOpenAI(ctx, c.Query, o)
			default:
				got, err = SearchWithOpenAI(ctx, c.Query, o)
			}

			if len(fn.calls) != len(c.Requests) {
				t.Fatalf("%d requests, the original sent %d", len(fn.calls), len(c.Requests))
			}
			for i, want := range c.Requests {
				call := fn.calls[i]
				headers := map[string]string{}
				for k, v := range call.Header {
					headers[strings.ToLower(k)] = uuidRE.ReplaceAllString(strings.Join(v, ", "), "<uuid>")
				}
				if call.Method != want.Method || call.URL != want.URL || !reflect.DeepEqual(headers, want.Headers) ||
					uuidRE.ReplaceAllString(call.Body, "<uuid>") != want.Body {
					t.Fatalf("request %d\n got  %s %s %v\n      %s\n want %s %s %v\n      %s", i, call.Method, call.URL, headers, uuidRE.ReplaceAllString(call.Body, "<uuid>"), want.Method, want.URL, want.Headers, want.Body)
				}
			}
			if c.Error != nil {
				if err == nil || err.Error() != *c.Error {
					t.Fatalf("error %v, the original returned %q", err, *c.Error)
				}
				return
			}
			noErr(t, err)
			if !reflect.DeepEqual(got, c.Result) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(c.Result)
				t.Fatalf("result\n got  %s\n want %s", gotJSON, wantJSON)
			}
		})
	}
}
