package eq

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func loadSC(t *testing.T, body string) (*Scenario, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadScenario(p)
}

func TestFeatureFieldsValidate(t *testing.T) {
	step := `"steps":[{"rpc":{"type":"prompt","message":"x"}}]`
	good := map[string]string{
		"model":     `{"name":"a","llm":[{"text":"hi"}],"model":{"contextWindow":2000,"maxTokens":100,"reasoning":true,"input":["text","image"]},` + step + `}`,
		"models":    `{"name":"a","llm":[{"text":"hi"}],"model":{"id":"m1"},"models":[{"id":"m2","contextWindow":50}],` + step + `}`,
		"usage":     `{"name":"a","llm":[{"text":"hi","usage":{"input":10,"output":5,"cacheRead":3,"cacheWrite":2,"totalTokens":20},"finishReason":"length"}],` + step + `}`,
		"error":     `{"name":"a","llm":[{"error":{"message":"prompt is too long: 300000 tokens > 100000 maximum"}}],` + step + `}`,
		"errorbody": `{"name":"a","llm":[{"error":{"status":529,"body":"{\"type\":\"error\"}","headers":{"retry-after":"1"}}}],` + step + `}`,
		"settings":  `{"name":"a","settings":{"compaction":{"enabled":true,"reserveTokens":16384,"keepRecentTokens":1}},` + step + `}`,
		"session":   `{"name":"a","session":{"files":{"seed.jsonl":"{}"}},"steps":[{"rpc":{"type":"switch_session","sessionPath":"{{sessions}}/seed.jsonl"}}]}`,
		"helper":    `{"name":"a","extensions":[{"name":"dump.ts","source":"export default function(pi){}","position":"before"}],` + step + `}`,
		"capture":   `{"name":"a","captureFiles":[".pi/notes/**","sessions:*.jsonl",{"root":"agent","glob":"x/*","modes":true,"exclude":["x/y"]}],` + step + `}`,
		"project":   `{"name":"a","project":{"messageFields":["errorMessage"],"images":true,"full":["turn_end"]},` + step + `}`,
		"save and trigger": `{"name":"a","llm":[{"text":"a","hold":"h"}],"steps":[
			{"rpc":{"type":"get_entries"},"save":{"first":"data.entries.0.id","user":"data.entries.[type=message]!.id"}},
			{"rpc":{"type":"prompt","message":"go"},"triggers":[{"when":{"event":"agent_start"},"rpc":{"type":"steer","message":"{{var:first}}"},"release":["h"]}]}]}`,
		"llm trigger": `{"name":"a","llm":[{"text":"a","hold":"h"}],"steps":[{"rpc":{"type":"prompt","message":"go"},"triggers":[{"when":{"llmRequest":1},"rpc":{"type":"abort"}}]}]}`,
	}
	for name, body := range good {
		if _, err := loadSC(t, body); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
	bad := map[string]string{
		"model without llm":       `{"name":"a","model":{"contextWindow":5},` + step + `}`,
		"models without model":    `{"name":"a","llm":[{"text":"x"}],"models":[{"id":"m2"}],` + step + `}`,
		"models without id":       `{"name":"a","llm":[{"text":"x"}],"model":{},"models":[{}],` + step + `}`,
		"duplicate model":         `{"name":"a","llm":[{"text":"x"}],"model":{},"models":[{"id":"eq-1"}],` + step + `}`,
		"bad input kind":          `{"name":"a","llm":[{"text":"x"}],"model":{"input":["audio"]},` + step + `}`,
		"negative window":         `{"name":"a","llm":[{"text":"x"}],"model":{"contextWindow":-1},` + step + `}`,
		"total mismatch":          `{"name":"a","llm":[{"text":"x","usage":{"input":1,"output":1,"totalTokens":5}}],` + step + `}`,
		"negative usage":          `{"name":"a","llm":[{"text":"x","usage":{"input":-1}}],` + step + `}`,
		"finish reason":           `{"name":"a","llm":[{"text":"x","finishReason":"boom"}],` + step + `}`,
		"error with text":         `{"name":"a","llm":[{"text":"x","error":{"message":"m"}}],` + step + `}`,
		"error status":            `{"name":"a","llm":[{"error":{"status":200,"message":"m"}}],` + step + `}`,
		"error needs one":         `{"name":"a","llm":[{"error":{"status":400}}],` + step + `}`,
		"error both":              `{"name":"a","llm":[{"error":{"message":"m","body":"b"}}],` + step + `}`,
		"negative delay":          `{"name":"a","llm":[{"text":"x","delayMs":-1}],` + step + `}`,
		"settings twice":          `{"name":"a","settings":{},"agentFiles":{"settings.json":"{}"},` + step + `}`,
		"session file escapes":    `{"name":"a","session":{"files":{"../x":"y"}},` + step + `}`,
		"helper name":             `{"name":"a","extensions":[{"name":"a/b.ts","source":"x"}],` + step + `}`,
		"helper twice":            `{"name":"a","extensions":[{"name":"a.ts","source":"x"},{"name":"a.ts","source":"y"}],` + step + `}`,
		"helper position":         `{"name":"a","extensions":[{"name":"a.ts","source":"x","position":"middle"}],` + step + `}`,
		"helper empty":            `{"name":"a","extensions":[{"name":"a.ts","source":" "}],` + step + `}`,
		"capture root":            `{"name":"a","captureFiles":["nowhere:x"],"steps":[{"rpc":{"type":"x"}}]}`,
		"capture dotdot":          `{"name":"a","captureFiles":["../x"],` + step + `}`,
		"capture bad pattern":     `{"name":"a","captureFiles":["a[b"],` + step + `}`,
		"capture unknown field":   `{"name":"a","captureFiles":[{"glob":"x","bogus":1}],` + step + `}`,
		"var never saved":         `{"name":"a","steps":[{"rpc":{"type":"prompt","message":"{{var:nope}}"}}]}`,
		"var saved later":         `{"name":"a","steps":[{"rpc":{"type":"prompt","message":"{{var:v}}"}},{"rpc":{"type":"get_state"},"save":{"v":"data.sessionId"}}]}`,
		"sessions without dir":    `{"name":"a","steps":[{"rpc":{"type":"switch_session","sessionPath":"{{sessions}}/x"}}]}`,
		"save builtin name":       `{"name":"a","steps":[{"rpc":{"type":"get_state"},"save":{"cwd":"data.x"}}]}`,
		"save bad name":           `{"name":"a","steps":[{"rpc":{"type":"get_state"},"save":{"1x":"data.x"}}]}`,
		"save bad path":           `{"name":"a","steps":[{"rpc":{"type":"get_state"},"save":{"x":"data..x"}}]}`,
		"trigger neither":         `{"name":"a","llm":[{"text":"x"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{},"rpc":{"type":"abort"}}]}]}`,
		"trigger both":            `{"name":"a","llm":[{"text":"x"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"event":"e","llmRequest":1},"rpc":{"type":"abort"}}]}]}`,
		"trigger llm range":       `{"name":"a","llm":[{"text":"x"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"llmRequest":2},"rpc":{"type":"abort"}}]}]}`,
		"trigger llm with match":  `{"name":"a","llm":[{"text":"x"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"llmRequest":1,"nth":2},"rpc":{"type":"abort"}}]}]}`,
		"trigger does nothing":    `{"name":"a","steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"event":"agent_start"}}]}]}`,
		"trigger rpc id":          `{"name":"a","steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"event":"agent_start"},"rpc":{"type":"abort","id":"1"}}]}]}`,
		"trigger release unknown": `{"name":"a","steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"event":"agent_start"},"release":["h"]}]}]}`,
		"project empty key":       `{"name":"a","project":{"messageFields":[""]},` + step + `}`,
	}
	for name, body := range bad {
		if _, err := loadSC(t, body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFileCaptureJSONForms(t *testing.T) {
	var caps []FileCapture
	if err := json.Unmarshal([]byte(`["a/**","agent:b.json","a:b/c",{"root":"home","glob":"c","modes":true}]`), &caps); err != nil {
		t.Fatal(err)
	}
	want := []string{"workspace:a/**", "agent:b.json", "a:b/c", "home:c"}
	for i, c := range caps {
		if c.label() != want[i] {
			t.Errorf("capture %d = %s, want %s", i, c.label(), want[i])
		}
	}
	if !caps[3].Modes {
		t.Error("modes lost")
	}
}

func TestResponsePaths(t *testing.T) {
	v, err := decodeAny([]byte(`{"data":{"sessionFile":"/s","n":3,"entries":[{"id":"a","type":"message","message":{"role":"user"}},{"id":"b","type":"compaction"},{"id":"c","type":"message","message":{"role":"assistant"}},{"id":"d","type":"message","message":{"role":"user"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"data.sessionFile":                          "/s",
		"data.n":                                    "3",
		"data.entries.0.id":                         "a",
		"data.entries.-1.id":                        "d",
		"data.entries.[type=compaction].id":         "b",
		"data.entries.[message.role=user].id":       "a",
		"data.entries.[message.role=user]!.id":      "d",
		"data.entries.[type=message]!.message.role": "user",
	}
	for p, want := range cases {
		segs, err := splitPath(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		got, ok := pathGet(v, segs)
		if !ok || scalarText(got) != want {
			t.Errorf("%s = %v %v, want %s", p, got, ok, want)
		}
	}
	for _, p := range []string{"data.entries.9.id", "data.entries.[type=nope].id", "data.missing", "data.sessionFile.x"} {
		segs, _ := splitPath(p)
		if got, ok := pathGet(v, segs); ok {
			t.Errorf("%s found %v", p, got)
		}
	}
	for _, p := range []string{"", "a..b", "a.", "a.[x", "a.[x]"} {
		if _, err := splitPath(p); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func TestLaneVarsExpandACopy(t *testing.T) {
	lv := &laneVars{builtin: map[string]string{"sessions": "/s", "cwd": "/w", "agent": "/a"}}
	lv.set("id", "abc")
	in := map[string]any{"type": "x", "p": "{{sessions}}/{{var:id}}", "list": []any{"{{cwd}}", 3.0}}
	out, err := lv.expand(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"list":["/w",3],"p":"/s/abc","type":"x"}` {
		t.Errorf("expanded %s", b)
	}
	if in["p"] != "{{sessions}}/{{var:id}}" {
		t.Error("the scenario's own value was changed")
	}
	if _, err := lv.expand("{{var:missing}}"); err == nil {
		t.Error("an unsaved variable expanded")
	}
}

func chunkPost(t *testing.T, base, body string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Post(base+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestFakeLLMScriptedOutcomes(t *testing.T) {
	turns := []Turn{
		{Text: "answer", Thinking: "hmm", Usage: &Usage{Input: 100, Output: 7, CacheRead: 30, CacheWrite: 5}, FinishReason: "length"},
		{Error: &TurnError{Message: "prompt is too long: 300000 tokens > 100000 maximum"}},
		{Error: &TurnError{Status: 529, Body: `{"type":"error","error":{"type":"overloaded_error"}}`, Headers: map[string]string{"retry-after": "3"}}},
		{Text: "plain"},
	}
	var recorded []string
	llm, err := newFakeLLM(turns, func(ch string, data any) { b, _ := json.Marshal(data); recorded = append(recorded, ch+" "+string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	llm.recordModel = true
	req := `{"model":"eq-2","messages":[{"role":"user","content":"q"}]}`

	code, out, _ := chunkPost(t, llm.baseURL(), req)
	if code != 200 || !strings.Contains(out, `"reasoning_content":"hmm"`) || !strings.Contains(out, `"finish_reason":"length"`) ||
		!strings.Contains(out, `"usage":{"completion_tokens":7,"prompt_tokens":135,"prompt_tokens_details":{"cache_write_tokens":5,"cached_tokens":30},"total_tokens":142}`) {
		t.Errorf("turn 1: %d %s", code, out)
	}
	code, out, h := chunkPost(t, llm.baseURL(), req)
	if code != 400 || h.Get("Content-Type") != "application/json" || out != `{"error":{"code":null,"message":"prompt is too long: 300000 tokens > 100000 maximum","param":null,"type":"invalid_request_error"}}` {
		t.Errorf("overflow: %d %s %v", code, out, h)
	}
	code, out, h = chunkPost(t, llm.baseURL(), req)
	if code != 529 || h.Get("retry-after") != "3" || out != `{"type":"error","error":{"type":"overloaded_error"}}` {
		t.Errorf("body error: %d %s %v", code, out, h)
	}
	code, out, _ = chunkPost(t, llm.baseURL(), req)
	if code != 200 || !strings.Contains(out, `"usage":{"completion_tokens":1,"prompt_tokens":1,"total_tokens":2}`) {
		t.Errorf("default usage: %d %s", code, out)
	}
	if !strings.Contains(recorded[0], `"model":"eq-2"`) {
		t.Errorf("model not recorded: %s", recorded[0])
	}
}

func TestFakeLLMHoldReleaseAndCancel(t *testing.T) {
	llm, err := newFakeLLM([]Turn{{Text: "late", Hold: "h"}, {Text: "never", Hold: "stuck"}, {Text: "d", DelayMs: 20}}, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	var hooks []int
	var mu sync.Mutex
	llm.setOnRequest(func(n int) { mu.Lock(); hooks = append(hooks, n); mu.Unlock() })
	done := make(chan string, 1)
	go func() {
		_, out, _ := chunkPost(t, llm.baseURL(), `{"messages":[]}`)
		done <- out
	}()
	select {
	case <-done:
		t.Fatal("a held turn answered before its release")
	case <-time.After(150 * time.Millisecond):
	}
	llm.release("h")
	llm.release("h") // idempotent
	if out := <-done; !strings.Contains(out, `"content":"late"`) {
		t.Errorf("released turn: %s", out)
	}
	// A held turn the host cancels ends without an answer.
	ctxDone := make(chan struct{})
	go func() {
		defer close(ctxDone)
		req, _ := http.NewRequest("POST", llm.baseURL()+"/chat/completions", strings.NewReader(`{"messages":[]}`))
		client := &http.Client{Timeout: 100 * time.Millisecond}
		if _, err := client.Do(req); err == nil {
			t.Error("a held turn answered")
		}
	}()
	<-ctxDone
	start := time.Now()
	_, out, _ := chunkPost(t, llm.baseURL(), `{"messages":[]}`)
	if !strings.Contains(out, `"content":"d"`) || time.Since(start) < 20*time.Millisecond {
		t.Errorf("delayed turn: %s after %s", out, time.Since(start))
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(hooks) != "[1 2 3]" {
		t.Errorf("request hooks %v", hooks)
	}
}

func TestFakeLLMRecordsImagesOnlyWhenPresent(t *testing.T) {
	var recorded []string
	llm, err := newFakeLLM([]Turn{{Text: "a"}, {Text: "b"}}, func(ch string, data any) { b, _ := json.Marshal(data); recorded = append(recorded, string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	png := base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	chunkPost(t, llm.baseURL(), `{"messages":[{"role":"user","content":[{"type":"text","text":"see"},{"type":"image_url","image_url":{"url":"data:image/png;base64,`+png+`"}}]}]}`)
	chunkPost(t, llm.baseURL(), `{"messages":[{"role":"user","content":[{"type":"text","text":"plain"}]}]}`)
	if !strings.Contains(recorded[0], `"images":[{"bytes":7,"mimeType":"image/png","sha256":"`) || !strings.Contains(recorded[0], `"text":"see"`) {
		t.Errorf("image request: %s", recorded[0])
	}
	if strings.Contains(recorded[1], "images") || strings.Contains(recorded[1], `"model"`) {
		t.Errorf("a request without images or a model list recorded extras: %s", recorded[1])
	}
}

func TestScenarioModelsFile(t *testing.T) {
	def := &Scenario{LLM: []Turn{{Text: "x"}}}
	b, _ := json.Marshal(scenarioModels(def, "http://x/v1"))
	// Byte for byte what every scenario got before ModelSpec existed.
	want := `{"providers":{"eq-llm":{"api":"openai-completions","apiKey":"eq-key","baseUrl":"http://x/v1","models":[{"contextWindow":100000,"cost":{"cacheRead":0,"cacheWrite":0,"input":0,"output":0},"id":"eq-1","input":["text"],"maxTokens":4096,"name":"eq-1","reasoning":false}]}}}`
	if string(b) != want {
		t.Errorf("default models.json\n got %s\nwant %s", b, want)
	}
	sc := &Scenario{LLM: []Turn{{Text: "x"}}, Model: &ModelSpec{ContextWindow: 2000, Reasoning: true, Input: []string{"text", "image"}}, Models: []ModelSpec{{ID: "small", ContextWindow: 500, MaxTokens: 50}}}
	b, _ = json.Marshal(scenarioModels(sc, "http://x/v1"))
	for _, frag := range []string{`"contextWindow":2000`, `"reasoning":true`, `"input":["text","image"]`, `"id":"small"`, `"contextWindow":500`, `"maxTokens":50`} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("models.json lacks %s: %s", frag, b)
		}
	}
	if !sc.recordsModel() || def.recordsModel() {
		t.Error("recordsModel")
	}
}

func TestProjectionIsOptInAndBackwardsCompatible(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	rec := map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "stopReason": "error", "errorMessage": "boom", "usage": map[string]any{"input": 1},
		"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("abc"))}},
	}}
	_, plain, _ := n.ProjectHost(rec, DefaultCapture)
	if string(plain) != `{"message":{"content":[{"type":"image"}],"role":"assistant","stopReason":"error"},"type":"message_end"}` {
		t.Errorf("original projection changed: %s", plain)
	}
	n.SetProjection(&Projection{MessageFields: []string{"errorMessage", "usage"}, Images: true})
	_, wide, _ := n.ProjectHost(rec, DefaultCapture)
	if !strings.Contains(string(wide), `"errorMessage":"boom"`) || !strings.Contains(string(wide), `"usage":{"input":1}`) ||
		!strings.Contains(string(wide), `"bytes":3,"mimeType":"image/png","sha256":"ba7816bf8f01cfea","type":"image"`) {
		t.Errorf("widened projection: %s", wide)
	}
}

func TestWholeEventsRelabelEntryIDs(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	project := func(rec string, capture ...string) string {
		v, err := decodeAny([]byte(rec))
		if err != nil {
			t.Fatal(err)
		}
		_, data, ok := n.ProjectHost(v.(map[string]any), capture)
		if !ok {
			t.Fatalf("%s not captured", rec)
		}
		return string(data)
	}
	got := project(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":true,"result":{"summary":"S","firstKeptEntryId":"a1b2c3d4","tokensBefore":900,"timestamp":5}}`, "compaction_end")
	if got != `{"aborted":false,"reason":"threshold","result":{"firstKeptEntryId":"<id:1>","summary":"S","tokensBefore":900},"type":"compaction_end","willRetry":true}` {
		t.Errorf("compaction_end: %s", got)
	}
	got = project(`{"type":"entry_appended","entry":{"id":"ffffffff","parentId":"a1b2c3d4","type":"custom","timestamp":"2026-11-06T10:00:00.000Z"}}`, "entry_appended")
	if got != `{"entry":{"id":"<id:2>","parentId":"<id:1>","type":"custom"},"type":"entry_appended"}` {
		t.Errorf("entry_appended: %s", got)
	}
	// get_entries ids are relabeled in a response; other responses are not touched.
	got = project(`{"type":"response","command":"get_entries","success":true,"data":{"leafId":"ffffffff","entries":[{"id":"a1b2c3d4","parentId":null}]}}`, "response")
	if got != `{"command":"get_entries","data":{"entries":[{"id":"<id:1>","parentId":null}],"leafId":"<id:2>"},"success":true}` {
		t.Errorf("get_entries: %s", got)
	}
	got = project(`{"type":"response","command":"get_state","success":true,"data":{"id":"a1b2c3d4"}}`, "response")
	if !strings.Contains(got, `"id":"a1b2c3d4"`) {
		t.Errorf("get_state was relabeled: %s", got)
	}
	// A type that is not in the default whole set and not listed stays a lifecycle marker; listing it records it whole.
	if got = project(`{"type":"turn_end","message":{"role":"assistant"},"toolResults":[]}`, "turn_end"); got != `{"type":"turn_end"}` {
		t.Errorf("turn_end default: %s", got)
	}
	n.SetProjection(&Projection{Full: []string{"turn_end"}})
	if got = project(`{"type":"turn_end","timestamp":9,"message":{"role":"assistant"},"toolResults":[]}`, "turn_end"); got != `{"message":{"role":"assistant"},"toolResults":[],"type":"turn_end"}` {
		t.Errorf("turn_end whole: %s", got)
	}
	// Not captured: not recorded.
	v, _ := decodeAny([]byte(`{"type":"queue_update","steering":[],"followUp":[]}`))
	if _, _, ok := n.ProjectHost(v.(map[string]any), DefaultCapture); ok {
		t.Error("queue_update recorded without being captured")
	}
	if got = project(`{"type":"queue_update","steering":["a"],"followUp":[]}`, "queue_update"); got != `{"followUp":[],"steering":["a"],"type":"queue_update"}` {
		t.Errorf("queue_update: %s", got)
	}
}

func TestRelabelIDsCoversAnchorLists(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	v, err := decodeAny([]byte(`{"id":"a1b2c3d4","data":{"seeds":[{"anchors":["ffffffff","a1b2c3d4","not-an-id"],"path":"x.md"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	// keys are visited in sorted order: data.seeds.anchors before id
	if got, want := string(canonical(n.relabelIDs(v))), `{"data":{"seeds":[{"anchors":["<id:1>","<id:2>","not-an-id"],"path":"x.md"}]},"id":"<id:2>"}`; got != want {
		t.Errorf("anchors relabel\n got %s\nwant %s", got, want)
	}
}

func TestProjectionSkipSystemLeavesOutSystemMessageEvents(t *testing.T) {
	system := map[string]any{"type": "message_end", "message": map[string]any{"role": "system", "content": ""}}
	user := map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": "hi"}}
	plain := NewNormalizer("/w", "/t", "/h")
	if _, _, ok := plain.ProjectHost(system, []string{"message_end"}); !ok {
		t.Fatal("without skipSystem a system message is recorded as before")
	}
	skip := NewNormalizer("/w", "/t", "/h")
	skip.SetProjection(&Projection{SkipSystem: true})
	if _, _, ok := skip.ProjectHost(system, []string{"message_end"}); ok {
		t.Error("skipSystem must leave out a system message_end")
	}
	if _, _, ok := skip.ProjectHost(user, []string{"message_end"}); !ok {
		t.Error("skipSystem must keep every other message")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{".pi/notes/**", ".pi/notes/a.md", true},
		{".pi/notes/**", ".pi/notes/x/y/a.md", true},
		{".pi/notes/**", ".pi/other/a.md", false},
		{"**/*.jsonl", "a.jsonl", true},
		{"**/*.jsonl", "a/b/c.jsonl", true},
		{"**/*.jsonl", "a/b/c.json", false},
		{"*.jsonl", "a/b.jsonl", false},
		{"a/**/z", "a/z", true},
		{"a/**/z", "a/b/c/z", true},
		{"a/**/z", "a/b/c/y", false},
		{"a/?.md", "a/b.md", true},
		{"a/[bc].md", "a/d.md", false},
		{"**", "x/y", true},
	}
	for _, c := range cases {
		if got := globMatch(strings.Split(c.glob, "/"), strings.Split(c.path, "/")); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v", c.glob, c.path, got)
		}
	}
}

func TestCaptureFilesNormalizesAndSorts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and modes")
	}
	dir := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".pi/notes/b.md", "at "+dir+"/x on 2026-11-06T10:00:00.000Z\n", 0o600)
	write(".pi/notes/a.md", "plain\n", 0o644)
	write(".pi/notes/sub/c.md", "nested\n", 0o644)
	write(".pi/notes/skip.tmp", "tmp", 0o644)
	write(".pi/notes/bin", "\xff\xfe\x00", 0o644)
	if err := os.Symlink("a.md", filepath.Join(dir, ".pi/notes/link.md")); err != nil {
		t.Fatal(err)
	}
	write(".git/config", "never", 0o644)
	write("2026-11-06T10-20-30-123Z_0d0c7e0e-1c3e-4d8a-9b57-8a1e5d9d9c11.jsonl",
		`{"type":"session","version":3,"id":"0d0c7e0e-1c3e-4d8a-9b57-8a1e5d9d9c11","timestamp":"2026-11-06T10:20:30.123Z","cwd":"`+dir+`"}`+"\n"+
			`{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2026-11-06T10:20:31.000Z","message":{"role":"user","content":"hi","timestamp":1793000000000}}`+"\n"+
			`{"type":"message","id":"e5f6a7b8","parentId":"a1b2c3d4","message":{"role":"assistant","content":"yo","timestamp":1793000000001}}`+"\n", 0o600)
	n := NewNormalizer(dir, dir, dir)
	got, err := n.captureFiles(dir, FileCapture{Glob: ".pi/notes/**", Exclude: []string{"**/*.tmp"}, Modes: true})
	if err != nil {
		t.Fatal(err)
	}
	b := canonical(got)
	want := `{"capture":"workspace:.pi/notes/**","files":[` +
		`{"mode":"0644","path":".pi/notes/a.md","text":"plain\n"},` +
		`{"mode":"0600","path":".pi/notes/b.md","text":"at <cwd>/x on <time>\n"},` +
		`{"mode":"0644","path":".pi/notes/bin","sha256":"` + shaHex("\xff\xfe\x00") + `","size":3},` +
		`{"path":".pi/notes/link.md","symlink":"a.md"},` +
		`{"mode":"0644","path":".pi/notes/sub/c.md","text":"nested\n"}]}`
	// the cwd root is the first Replace rule, so the placeholder is <cwd>
	if string(b) != want {
		t.Errorf("notes capture\n got %s\nwant %s", b, want)
	}
	got, err = n.captureFiles(dir, FileCapture{Glob: "*.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	b = canonical(got)
	want = `{"capture":"workspace:*.jsonl","files":[{"jsonl":[` +
		`{"cwd":"<cwd>","id":"<uuid>","timestamp":"<time>","type":"session","version":3},` +
		`{"id":"<id:1>","message":{"content":"hi","role":"user"},"parentId":null,"timestamp":"<time>","type":"message"},` +
		`{"id":"<id:2>","message":{"content":"yo","role":"assistant"},"parentId":"<id:1>","type":"message"}],` +
		`"path":"<time>_<uuid>.jsonl"}]}`
	if string(b) != want {
		t.Errorf("session capture\n got %s\nwant %s", b, want)
	}
	if got, err = n.captureFiles(dir, FileCapture{Glob: "nowhere/**"}); err != nil || len(got["files"].([]any)) != 0 {
		t.Errorf("missing directory: %v %v", got, err)
	}
	if got, err = n.captureFiles(dir, FileCapture{Glob: ".git/**"}); err != nil || len(got["files"].([]any)) != 1 {
		t.Errorf("an explicit .git glob must see .git: %v %v", got, err)
	}
	if got, err = n.captureFiles(dir, FileCapture{Glob: "**"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range got["files"].([]any) {
		if strings.HasPrefix(f.(map[string]any)["path"].(string), ".git/") {
			t.Error("a ** capture descended into .git")
		}
	}
}

func TestSystemHunksReportEachChangeWithoutTheHostTextBetween(t *testing.T) {
	base := "intro\n<tools>\n- read: r\n</tools>\nHOST DOCS /pi/docs\nrules\nDate: today"
	got := "intro\n<tools>\n- read: r\n- notes: n\n- history: h\n</tools>\nHOST DOCS /pig/docs\nrules\nGuidance one\nGuidance two\nDate: today"
	span, _ := systemDelta(base, got)
	if s := span.(map[string]any)["inserted"].(string); !strings.Contains(s, "HOST DOCS") {
		t.Fatalf("the span delta was expected to contain the host line between the changes: %q", s)
	}
	v, ok := systemHunks(base, got)
	if !ok {
		t.Fatal("no delta")
	}
	b := canonical(v)
	want := `{"hunks":[{"inserted":"- notes: n\n- history: h","removed":""},{"inserted":"HOST DOCS /pig/docs","removed":"HOST DOCS /pi/docs"},{"inserted":"Guidance one\nGuidance two","removed":""}]}`
	if string(b) != want {
		t.Errorf("hunks\n got %s\nwant %s", b, want)
	}
	// With the host's docs line equal on both sides the host text never shows.
	v, _ = systemHunks(base, strings.Replace(got, "/pig/docs", "/pi/docs", 1))
	if string(canonical(v)) != `{"hunks":[{"inserted":"- notes: n\n- history: h","removed":""},{"inserted":"Guidance one\nGuidance two","removed":""}]}` {
		t.Errorf("hunks %s", canonical(v))
	}
	if _, ok := systemHunks(base, base); ok {
		t.Error("equal texts reported a delta")
	}
	if v, _ := systemDeltaMode("", base, got); v == nil || strings.Contains(string(canonical(v)), "hunks") {
		t.Error("the default mode must stay the span delta")
	}
}

func TestJSONLDropPathsAndClocks(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	line := `{"type":"compaction","id":"a1b2c3d4","systemMessage":{"role":"system","sections":{"docs":"H","preamble":"P","posthorse":"G"}},` +
		`"message":{"role":"toolResult","durationMs":43,"timestamp":5,"sections":{"docs":"H2"},"details":{"docs":"keep"}}}`
	got, ok := n.jsonlLines([]byte(line+"\n"), splitDrop([]string{"**.sections.docs", "systemMessage.sections.preamble"}))
	if !ok {
		t.Fatal("not jsonl")
	}
	want := `[{"id":"<id:1>","message":{"details":{"docs":"keep"},"role":"toolResult","sections":{}},"systemMessage":{"role":"system","sections":{"posthorse":"G"}},"type":"compaction"}]`
	if string(canonical(got)) != want {
		t.Errorf("got  %s\nwant %s", canonical(got), want)
	}
	for pat, path := range map[string]string{"a.b": "a.b", "*.b": "x.b", "**.c": "a.b.c", "**.b.c": "z.b.c", "a.*": "a.q"} {
		if !pathMatches(strings.Split(pat, "."), strings.Split(path, ".")) {
			t.Errorf("%s did not match %s", pat, path)
		}
	}
	for pat, path := range map[string]string{"a.b": "a.b.c", "a.b.c": "a.b", "*.b": "b", "**.b": "a.c"} {
		if pathMatches(strings.Split(pat, "."), strings.Split(path, ".")) {
			t.Errorf("%s matched %s", pat, path)
		}
	}
	if _, err := loadSC(t, `{"name":"a","captureFiles":[{"glob":"x","drop":["a..b"]}],"steps":[{"rpc":{"type":"x"}}]}`); err == nil {
		t.Error("an empty drop segment was accepted")
	}
	if _, err := loadSC(t, `{"name":"a","captureFiles":[{"glob":"x","drop":["a.**"]}],"steps":[{"rpc":{"type":"x"}}]}`); err == nil {
		t.Error("a trailing ** was accepted")
	}
	if _, err := loadSC(t, `{"name":"a","systemDiff":"lines","steps":[{"rpc":{"type":"x"}}]}`); err == nil {
		t.Error("an unknown systemDiff was accepted")
	}
}

func TestFakeLLMExpandsSavedVariablesWhenAturnsAreServed(t *testing.T) {
	var recorded []string
	llm, err := newFakeLLM([]Turn{
		{ToolCalls: []ToolCall{{Name: "history", Arguments: map[string]any{"op": "read", "id": "{{var:entry}}", "n": 2.0}}}, Text: "see {{var:entry}}"},
		{Text: "{{var:late}}"},
	}, func(ch string, data any) { b, _ := json.Marshal(data); recorded = append(recorded, ch+" "+string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	lv := &laneVars{}
	llm.setVars(lv)
	lv.set("entry", "a1b2c3d4")
	_, out, _ := chunkPost(t, llm.baseURL(), `{"messages":[]}`)
	if !strings.Contains(out, `\"id\":\"a1b2c3d4\"`) || !strings.Contains(out, `"content":"see a1b2c3d4"`) {
		t.Errorf("expanded turn: %s", out)
	}
	if llm.turns[0].Text != "see {{var:entry}}" {
		t.Error("the scripted turn was changed")
	}
	code, _, _ := chunkPost(t, llm.baseURL(), `{"messages":[]}`)
	if code != 500 || !strings.Contains(recorded[len(recorded)-1], `variable \"late\" has no value`) {
		t.Errorf("unsaved variable: %d %q", code, recorded)
	}
	if _, err := loadSC(t, `{"name":"a","llm":[{"text":"{{var:nope}}"}],"steps":[{"rpc":{"type":"get_state"}}]}`); err == nil {
		t.Error("a variable no step saves was accepted in an llm turn")
	}
	if _, err := loadSC(t, `{"name":"a","llm":[{"toolCalls":[{"name":"h","arguments":{"id":"{{var:e}}"}}]}],"steps":[{"rpc":{"type":"get_entries"},"save":{"e":"data.leafId"}}]}`); err != nil {
		t.Errorf("a saved variable was rejected in an llm turn: %v", err)
	}
}

func TestProjectionDropRemovesHostComputedValuesFromWholeEvents(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	n.SetProjection(&Projection{Drop: []string{"**.estimatedTokensAfter"}})
	v, _ := decodeAny([]byte(`{"type":"compaction_end","reason":"threshold","result":{"summary":"S","estimatedTokensAfter":2677,"firstKeptEntryId":"a1b2c3d4"}}`))
	_, data, ok := n.ProjectHost(v.(map[string]any), []string{"compaction_end"})
	if !ok || string(data) != `{"reason":"threshold","result":{"firstKeptEntryId":"<id:1>","summary":"S"},"type":"compaction_end"}` {
		t.Errorf("%s", data)
	}
	if _, err := loadSC(t, `{"name":"a","project":{"drop":["a..b"]},"steps":[{"rpc":{"type":"x"}}]}`); err == nil {
		t.Error("an empty project.drop segment was accepted")
	}
}
