package langsmith

import (
	"encoding/json"
	"strings"
	"testing"
)

// convertVector is one case of testdata/ext_convert.json: the JSON text of the input (parsed here with its key order),
// and what the real index.js returned (JSON.stringify), threw, or returned as undefined.
type convertVector struct {
	Fn        string  `json:"fn"`
	Input     string  `json:"input"`
	Output    *string `json:"output"`
	Undefined bool    `json:"undefined"`
	Error     string  `json:"error"`
}

func runConvert(fn string, in any) (any, error) {
	switch fn {
	case "convertProviderPayload":
		return convertProviderPayload(in)
	case "convertMessages":
		arr, _ := in.([]any)
		out, err := convertMessages(arr)
		return out, err
	case "convertToolOutputs":
		return convertToolOutputs(recGet(in, "result")), nil
	case "extractUsageMetadata":
		return extractUsageMetadata(in)
	}
	panic("unknown fn " + fn)
}

func TestConvertVectors(t *testing.T) {
	vectors := readVectors[convertVector](t, "ext_convert.json")
	if len(vectors) < 250 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	counts := map[string]int{}
	for i, v := range vectors {
		in, ok := parseJSONOrdered(v.Input)
		if !ok {
			t.Fatalf("vector %d: input does not parse", i)
		}
		got, err := runConvert(v.Fn, in)
		counts[v.Fn]++
		switch {
		case v.Error != "":
			if err == nil || err.Error() != v.Error {
				t.Errorf("vector %d %s: want error %q, got %v", i, v.Fn, v.Error, err)
			}
		case v.Fn == "convertProviderPayload" && v.Undefined:
			// The generator's wrapper turned a throw into undefined: the port must fail there too.
			if err == nil {
				t.Errorf("vector %d: want an error", i)
			}
		case err != nil:
			t.Errorf("vector %d %s: %v\ninput %s", i, v.Fn, err, v.Input)
		case v.Undefined:
			if !isUndef(got) {
				t.Errorf("vector %d %s: want undefined, got %s\ninput %s", i, v.Fn, Stringify(got), v.Input)
			}
		default:
			if s := Stringify(got); s != *v.Output {
				t.Errorf("vector %d %s:\n got %s\nwant %s\ninput %s", i, v.Fn, s, *v.Output, v.Input)
			}
		}
	}
	for _, fn := range []string{"convertProviderPayload", "convertMessages", "convertToolOutputs", "extractUsageMetadata"} {
		if counts[fn] < 10 {
			t.Errorf("%s: only %d vectors", fn, counts[fn])
		}
	}
}

func parse(t testing.TB, text string) any {
	t.Helper()
	v, ok := parseJSONOrdered(text)
	if !ok {
		t.Fatalf("bad JSON %s", text)
	}
	return v
}

func TestNormalizeContentPartIdentity(t *testing.T) {
	text := map[string]any{"type": "text", "text": "hello"}
	if got := normalizeContentPart(text); Stringify(got) != `{"text":"hello","type":"text"}` {
		t.Fatalf("%s", Stringify(got))
	}
	if normalizeContentPart("plain string") != "plain string" || normalizeContentPart(nil) != nil {
		t.Fatal("non-records pass through")
	}
}

func TestConvertMessagesRejectsNonObject(t *testing.T) {
	for _, bad := range []any{nil, "s", 1.0, []any{}, undef} {
		if _, err := convertMessages([]any{bad}); err == nil {
			t.Errorf("message %#v must fail", bad)
		}
	}
}

func TestConvertProviderPayloadKeyOrderFromHostMaps(t *testing.T) {
	// Host objects are map[string]any: keys come out in JavaScript property order (finding F1).
	got, err := convertProviderPayload(map[string]any{"model": "m", "stream": true, "system": "s", "messages": []any{map[string]any{"role": "user", "content": "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"messages":[{"role":"system","content":"s","_raw":"s"},{"content":"x","role":"user"}],"model":"m","stream":true}`
	if s := Stringify(got); s != want {
		t.Fatalf("got %s", s)
	}
}

func TestSafeAdd(t *testing.T) {
	if !isUndef(safeAdd()) || !isUndef(safeAdd(nil, undef)) {
		t.Fatal("no value is undefined")
	}
	if safeAdd(nil, 2.0, undef, 3.0) != 5.0 || safeAdd(0.0) != 0.0 {
		t.Fatal("sums skip null and undefined")
	}
	if safeAdd(1, 2.0) != 3.0 {
		t.Fatal("ints count")
	}
}

func TestExtractUsageMetadataOmitsCostOnlyWhenTokensWithoutCost(t *testing.T) {
	for _, c := range []struct {
		in   string
		cost bool
	}{
		{`{"role":"assistant","usage":{"totalTokens":5,"cost":{"total":0}}}`, false},
		{`{"role":"assistant","usage":{"totalTokens":5}}`, false},
		{`{"role":"assistant","usage":{"totalTokens":5,"cost":{"total":0.1}}}`, true},
		{`{"role":"assistant","usage":{"totalTokens":0,"cost":{"total":0}}}`, true},
		{`{"role":"assistant"}`, true},
		{`{"role":"assistant","usage":{"totalTokens":-1,"cost":{"total":0}}}`, true},
	} {
		got, err := extractUsageMetadata(parse(t, c.in))
		if err != nil {
			t.Fatal(err)
		}
		m := got.(*omap)
		if m.has("input_cost") != c.cost || m.has("total_cost") != c.cost || m.has("input_cost_details") != c.cost {
			t.Errorf("%s: cost keys present = %v, want %v", c.in, m.has("input_cost"), c.cost)
		}
	}
	if got, _ := extractUsageMetadata(parse(t, `{"role":"user"}`)); !isUndef(got) {
		t.Fatal("only assistant messages have usage metadata")
	}
}

func TestGeminiRolePrototypeMembers(t *testing.T) {
	if !isUndef(geminiRole("constructor")) || Stringify(geminiRole("__proto__")) != "{}" || geminiRole("user") != "user" || geminiRole("model") != "assistant" || geminiRole("tool") != "tool" {
		t.Fatal("role mapping")
	}
}

func TestSystemPromptNullMessageThrows(t *testing.T) {
	_, err := convertProviderPayload(parse(t, `{"system":"s","messages":[null,{"role":"system"}]}`))
	if err == nil || !strings.Contains(err.Error(), "reading 'role'") {
		t.Fatalf("got %v", err)
	}
	// A system-role message before the null stops the scan (Array.prototype.some).
	if _, err := convertProviderPayload(parse(t, `{"system":"s","messages":[{"role":"system"},null]}`)); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkConvertMessages(b *testing.B) {
	msgs := parse(b, `[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","mimeType":"image/png","data":"AAAABBBBCCCC"}],"timestamp":1},
{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"toolCall","id":"1","name":"read","arguments":{"path":"a.png"}}],"usage":{"input":1,"output":2,"totalTokens":3,"cost":{"total":0}},"stopReason":"toolUse"},
{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"Read image"}],"isError":false}]`).([]any)
	b.ReportAllocs()
	for range b.N {
		if _, err := convertMessages(msgs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertProviderPayload(b *testing.B) {
	for name, text := range map[string]string{
		"anthropic": `{"model":"c","stream":true,"system":[{"type":"text","text":"coding assistant"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"bash","input":{"command":"ls"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"files"}]}],"max_tokens":1000}`,
		"responses": `{"model":"g","stream":true,"input":[{"role":"developer","content":"sys"},{"role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"function_call_output","call_id":"c","output":"o"}]}`,
		"gemini":    `{"model":"g","contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"functionCall":{"name":"bash","args":{"command":"ls"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"bash","response":{"output":"x"}}}]}]}`,
	} {
		payload := parse(b, text)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := convertProviderPayload(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConvertProviderPayloadHostMap converts what the host delivers (map[string]any, keys sorted on every read).
func BenchmarkConvertProviderPayloadHostMap(b *testing.B) {
	var payload any
	text := `{"model":"c","stream":true,"system":[{"type":"text","text":"coding assistant"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"bash","input":{"command":"ls"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"files"}]}],"max_tokens":1000}`
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for range b.N {
		if _, err := convertProviderPayload(payload); err != nil {
			b.Fatal(err)
		}
	}
}
