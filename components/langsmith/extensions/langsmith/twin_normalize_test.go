package langsmith_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

// Twins of test/normalize.test.ts and test/usage.test.ts.

// sameJSON is `toEqual`: the JSON.stringify text of got equals the expected JSON (key order and undefined ignored).
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(langsmith.Stringify(got)), &g); err != nil {
		t.Fatalf("%v: %s", err, langsmith.Stringify(got))
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s\nwant %s", langsmith.Stringify(got), want)
	}
}

const (
	piImage = `{"type":"image","mimeType":"image/png","data":"iVBORw0KGgoBASE64"}`
	lcImage = `{"type":"image","mime_type":"image/png","base64":"iVBORw0KGgoBASE64"}`
)

func TestNormalizeTwins(t *testing.T) {
	first := func(t *testing.T, out []any, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(langsmith.Stringify(out[0])), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	asMap := func(t *testing.T, v any) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(langsmith.Stringify(v)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	tw(t, "normalize", "converts a pi image part to the LangChain v1 shape", func(t *testing.T) {
		sameJSON(t, langsmith.NormalizeContentPart(jsonOf(t, piImage)), lcImage)
	})
	tw(t, "normalize", "passes text parts through unchanged", func(t *testing.T) {
		text := jsonOf(t, `{"type":"text","text":"hello"}`)
		got := langsmith.NormalizeContentPart(text)
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(text).Pointer() {
			t.Fatal("toBe: the very same object must come back")
		}
	})
	tw(t, "normalize", "leaves malformed/partial image parts untouched", func(t *testing.T) {
		sameJSON(t, langsmith.NormalizeContentPart(jsonOf(t, `{"type":"image"}`)), `{"type":"image"}`)
		sameJSON(t, langsmith.NormalizeContentPart(jsonOf(t, `{"type":"image","mimeType":"image/png"}`)), `{"type":"image","mimeType":"image/png"}`)
		if langsmith.NormalizeContentPart("plain string") != "plain string" {
			t.Fatal("toBe on a string")
		}
	})
	tw(t, "normalize", "normalizes image parts in user message content", func(t *testing.T) {
		out, err := langsmith.ConvertMessages([]any{jsonOf(t, `{"role":"user","content":[{"type":"text","text":"look at this"},`+piImage+`]}`)})
		sameJSON(t, first(t, out, err)["content"], `[{"type":"text","text":"look at this"},`+lcImage+`]`)
	})
	tw(t, "normalize", "maps toolResult to role 'tool' and normalizes image content (the `read` tool path)", func(t *testing.T) {
		out, err := langsmith.ConvertMessages([]any{jsonOf(t, `{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"Read image file [image/png]"},`+piImage+`]}`)})
		m := first(t, out, err)
		if m["role"] != "tool" {
			t.Fatalf("role %v", m["role"])
		}
		sameJSON(t, m["content"], `[{"type":"text","text":"Read image file [image/png]"},`+lcImage+`]`)
	})
	tw(t, "normalize", "leaves string user content untouched", func(t *testing.T) {
		out, err := langsmith.ConvertMessages([]any{jsonOf(t, `{"role":"user","content":"just text"}`)})
		if first(t, out, err)["content"] != "just text" {
			t.Fatal("string content")
		}
	})
	tw(t, "normalize", "still converts assistant tool calls and never sees images there", func(t *testing.T) {
		out, err := langsmith.ConvertMessages([]any{jsonOf(t, `{"role":"assistant","content":[{"type":"toolCall","id":"1","name":"read","arguments":{"path":"a.png"}}]}`)})
		sameJSON(t, first(t, out, err)["content"], `[{"type":"tool_call","id":"1","name":"read","args":{"path":"a.png"}}]`)
	})
	tw(t, "normalize", "normalizes image parts in tool result content", func(t *testing.T) {
		out := langsmith.ConvertToolOutputs(jsonOf(t, `{"content":[{"type":"text","text":"Read image file"},`+piImage+`]}`))
		sameJSON(t, out, `{"output":{"role":"tool","content":[{"type":"text","text":"Read image file"},`+lcImage+`]}}`)
		if strings.Contains(langsmith.Stringify(asMap(t, out)), "mimeType") {
			t.Fatal("mimeType left")
		}
	})
	tw(t, "normalize", "passes through non-record results", func(t *testing.T) {
		sameJSON(t, langsmith.ConvertToolOutputs("ok"), `{"output":"ok"}`)
	})
}

func TestUsageTwins(t *testing.T) {
	tw(t, "usage", "omits cost fields when tokens > 0 but pi reports zero cost, so LangSmith prices it", func(t *testing.T) {
		out, err := langsmith.ExtractUsageMetadata(jsonOf(t, `{"role":"assistant","usage":{"input":2,"output":246,"cacheRead":2755,"cacheWrite":98,"totalTokens":3101,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, out, `{"input_tokens":2855,"output_tokens":246,"total_tokens":3101,"input_token_details":{"cache_read":2755,"cache_creation":98}}`)
		text := langsmith.Stringify(out)
		for _, k := range []string{"input_cost", "output_cost", "total_cost", "input_cost_details"} {
			if strings.Contains(text, k) {
				t.Fatalf("%s must be absent: %s", k, text)
			}
		}
	})
}
