package ollamanative

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func run(t *testing.T, c *client, model, transcript map[string]any, opts sdk.ProviderStreamOptions) streamed {
	t.Helper()
	stream, err := c.stream(model, transcript, opts)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, stream)
}

func content(msg map[string]any) []any { return msg["content"].([]any) }

func TestStreamText(t *testing.T) {
	f := newFake(t)
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{})
	want := []string{"start", "text_start", "text_delta", "text_delta", "text_delta", "text_end", "done"}
	if !reflect.DeepEqual(s.types(), want) {
		t.Fatalf("events = %v", s.types())
	}
	done := s.terminal()
	msg := done["message"].(map[string]any)
	if done["reason"] != "stop" || msg["stopReason"] != "stop" {
		t.Errorf("reason = %v / %v", done["reason"], msg["stopReason"])
	}
	if blocks := content(msg); len(blocks) != 1 || blocks[0].(map[string]any)["text"] != "Hello, world." {
		t.Errorf("content = %v", msg["content"])
	}
	u := msg["usage"].(map[string]any)
	if u["input"] != 26 || u["output"] != 5 || u["totalTokens"] != 31 {
		t.Errorf("usage = %v", u)
	}
	if msg["api"] != "ollama-native" || msg["provider"] != "ollama-native" || msg["model"] != "granite4.1:3b" {
		t.Errorf("identity = %v", msg)
	}
	// Deltas carry the right slice of text.
	if s.events[2]["delta"] != "Hel" || s.events[4]["delta"] != ", world." {
		t.Errorf("deltas = %v %v", s.events[2], s.events[4])
	}
	// The request is the native chat shape: streaming, one user message, no tools.
	req := f.chats[0]
	if req["model"] != "granite4.1:3b" || req["stream"] != true || req["tools"] != nil {
		t.Errorf("request = %v", req)
	}
	if f.requests[0] != "POST /api/chat" {
		t.Errorf("path = %v", f.requests)
	}
}

func TestStreamThinkingThenText(t *testing.T) {
	f := newFake(t)
	f.chat = "chat_thinking.ndjson"
	s := run(t, newClient(f.URL), map[string]any{"id": "qwen3:8b", "reasoning": true}, userTranscript("hi"), sdk.ProviderStreamOptions{})
	want := []string{"start", "thinking_start", "thinking_delta", "thinking_delta", "thinking_end", "text_start", "text_delta", "text_end", "done"}
	if !reflect.DeepEqual(s.types(), want) {
		t.Fatalf("events = %v", s.types())
	}
	blocks := content(s.terminal()["message"].(map[string]any))
	if len(blocks) != 2 || blocks[0].(map[string]any)["thinking"] != "The user wants a greeting." || blocks[1].(map[string]any)["text"] != "Hi!" {
		t.Errorf("content = %v", blocks)
	}
}

func TestStreamToolCalls(t *testing.T) {
	f := newFake(t)
	f.chat = "chat_toolcalls.ndjson"
	s := run(t, newClient(f.URL), graniteModel, userTranscript("look around"), sdk.ProviderStreamOptions{})
	want := []string{"start", "text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_end",
		"toolcall_start", "toolcall_delta", "toolcall_end", "done"}
	if !reflect.DeepEqual(s.types(), want) {
		t.Fatalf("events = %v", s.types())
	}
	done := s.terminal()
	msg := done["message"].(map[string]any)
	if done["reason"] != "toolUse" || msg["stopReason"] != "toolUse" {
		t.Errorf("stop = %v / %v", done["reason"], msg["stopReason"])
	}
	blocks := content(msg)
	if len(blocks) != 3 {
		t.Fatalf("content = %v", blocks)
	}
	first, second := blocks[1].(map[string]any), blocks[2].(map[string]any)
	// Ollama sent no id for the first call: one is made up, and is unique.
	if first["type"] != "toolCall" || first["name"] != "read" || first["id"] != "call_t1" {
		t.Errorf("first = %v", first)
	}
	if !reflect.DeepEqual(first["arguments"], map[string]any{"path": "README.md"}) {
		t.Errorf("first args = %v", first["arguments"])
	}
	// A server-sent id is kept.
	if second["name"] != "bash" || second["id"] != "call_server7" || second["arguments"].(map[string]any)["timeout"] != float64(10) {
		t.Errorf("second = %v", second)
	}
	end := s.events[6]
	if end["contentIndex"] != 1 || end["toolCall"].(map[string]any)["name"] != "read" {
		t.Errorf("toolcall_end = %v", end)
	}
}

func TestStreamLengthStop(t *testing.T) {
	f := newFake(t)
	f.chat = "chat_length.ndjson"
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{})
	if s.terminal()["reason"] != "length" {
		t.Errorf("terminal = %v", s.terminal())
	}
}

func errorMessage(s streamed) string {
	e := s.terminal()
	if e["type"] != "error" {
		return "<not an error: " + e["type"].(string) + ">"
	}
	return e["error"].(map[string]any)["errorMessage"].(string)
}

func TestStreamMidStreamError(t *testing.T) {
	f := newFake(t)
	f.chat = "chat_midstream_error.ndjson"
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{})
	if msg := errorMessage(s); !strings.Contains(msg, "model runner has unexpectedly stopped") {
		t.Errorf("error = %q (events %v)", msg, s.types())
	}
	// What arrived before the failure is kept on the failed message.
	partial := content(s.terminal()["error"].(map[string]any))
	if len(partial) != 1 || partial[0].(map[string]any)["text"] != "Partial " {
		t.Errorf("partial = %v", partial)
	}
	if s.terminal()["reason"] != "error" {
		t.Errorf("reason = %v", s.terminal()["reason"])
	}
}

func TestStreamTruncatedBodyIsAnError(t *testing.T) {
	f := newFake(t)
	f.chat = "chat_truncated.ndjson"
	s := run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{})
	if msg := errorMessage(s); !strings.Contains(msg, "ended before") {
		t.Errorf("error = %q", msg)
	}
}

func TestStreamUnknownModelTellsYouToPull(t *testing.T) {
	f := newFake(t)
	f.chat, f.chatStatus = "chat_404.json", 404
	s := run(t, newClient(f.URL), map[string]any{"id": "nope:1b"}, userTranscript("hi"), sdk.ProviderStreamOptions{})
	msg := errorMessage(s)
	if !strings.Contains(msg, "nope:1b") || !strings.Contains(msg, "ollama pull nope:1b") {
		t.Errorf("error = %q", msg)
	}
}

func TestStreamOllamaNotRunning(t *testing.T) {
	f := newFake(t)
	url := f.URL
	f.Close()
	s := run(t, newClient(url), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{})
	msg := errorMessage(s)
	if !strings.Contains(msg, "Ollama is not reachable at "+url) || !strings.Contains(msg, "ollama serve") || !strings.Contains(msg, "OLLAMA_HOST") {
		t.Errorf("error = %q", msg)
	}
}

func TestStreamAbort(t *testing.T) {
	f := newFake(t)
	f.hold = make(chan struct{})
	defer close(f.hold)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := newClient(f.URL).stream(graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{Signal: ctx})
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	s := collect(t, stream)
	if s.terminal()["type"] != "error" || s.terminal()["reason"] != "aborted" {
		t.Errorf("terminal = %v", s.terminal())
	}
}

func TestChatRequestMapsTheTranscript(t *testing.T) {
	transcript := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "Be brief.", "timestamp": 0,
			"sections": map[string]any{"cwd": "<cwd>/work</cwd>", "docs": "<docs>old</docs>", "gone": "x"},
			"toolsAdded": []any{
				map[string]any{"name": "read", "description": "Read a file", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
				map[string]any{"name": "bash", "description": "Run", "parameters": map[string]any{"type": "object"}},
			}},
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "Later note."}}, "timestamp": 1,
			"sections":     map[string]any{"docs": "<docs>new</docs>", "gone": nil},
			"toolsRemoved": []any{map[string]any{"name": "bash"}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "what is this?"},
			map[string]any{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"}}},
		map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "thinking", "thinking": "hmm"},
			map[string]any{"type": "text", "text": "Reading."},
			map[string]any{"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "a.txt"}}}},
		map[string]any{"role": "toolResult", "toolCallId": "call_1", "toolName": "read", "isError": false,
			"content": []any{map[string]any{"type": "text", "text": "line one"}, map[string]any{"type": "text", "text": "line two"}}},
		// An aborted turn that produced nothing is not replayed.
		map[string]any{"role": "assistant", "stopReason": "aborted", "content": []any{}},
		map[string]any{"role": "user", "content": "thanks"},
	}}
	opts := sdk.ProviderStreamOptions{Values: map[string]any{"temperature": 0.2, "maxTokens": float64(512)}}
	req := chatRequest(map[string]any{"id": "granite4.1:3b"}, transcript, opts)

	if req["model"] != "granite4.1:3b" || req["stream"] != true {
		t.Fatalf("req = %v", req)
	}
	msgs := req["messages"].([]map[string]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m["role"].(string))
	}
	if want := []string{"system", "user", "assistant", "tool", "user"}; !reflect.DeepEqual(roles, want) {
		t.Fatalf("roles = %v", roles)
	}
	// One system message, as PiG itself renders it: the instructions, then the
	// sections with later values replacing earlier ones and null removing one.
	// PiG 0.4.0 hands a provider the sections as a map, so their authored order
	// is lost: they come in name order.
	if want := "Be brief.\n\nLater note.\n\n<cwd>/work</cwd>\n\n<docs>new</docs>"; msgs[0]["content"] != want {
		t.Errorf("system = %q, want %q", msgs[0]["content"], want)
	}
	if msgs[1]["content"] != "what is this?" || !reflect.DeepEqual(msgs[1]["images"], []string{"aGVsbG8="}) {
		t.Errorf("user = %v", msgs[1])
	}
	a := msgs[2]
	calls := a["tool_calls"].([]map[string]any)
	if a["content"] != "Reading." || a["thinking"] != nil || len(calls) != 1 {
		t.Fatalf("assistant = %v", a)
	}
	fn := calls[0]["function"].(map[string]any)
	if fn["name"] != "read" || !reflect.DeepEqual(fn["arguments"], map[string]any{"path": "a.txt"}) {
		t.Errorf("call = %v", calls[0])
	}
	if msgs[3]["tool_name"] != "read" || msgs[3]["content"] != "line one\nline two" {
		t.Errorf("tool result = %v", msgs[3])
	}

	// bash was removed by the second system message; only read is offered.
	tools := req["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["type"] != "function" || tools[0]["function"].(map[string]any)["name"] != "read" {
		t.Errorf("tools = %v", tools)
	}
	o := req["options"].(map[string]any)
	if o["temperature"] != 0.2 || o["num_predict"] != 512 {
		t.Errorf("options = %v", o)
	}
}

func TestChatRequestThinkingOnlyForModelsThatThink(t *testing.T) {
	on := sdk.ProviderStreamOptions{Values: map[string]any{"thinkingEnabled": true}}
	if got := chatRequest(map[string]any{"id": "qwen3:8b", "reasoning": true}, userTranscript("x"), on)["think"]; got != true {
		t.Errorf("think = %v", got)
	}
	// Ollama rejects `think` for a model that cannot think.
	if _, has := chatRequest(map[string]any{"id": "granite4.1:3b", "reasoning": false}, userTranscript("x"), on)["think"]; has {
		t.Error("think sent to a model without the thinking capability")
	}
	if _, has := chatRequest(map[string]any{"id": "qwen3:8b", "reasoning": true}, userTranscript("x"), sdk.ProviderStreamOptions{})["think"]; has {
		t.Error("think sent although no level was asked for")
	}
}

func TestAPIKeyOnlyWhenOneIsConfigured(t *testing.T) {
	f := newFake(t)
	var auth []string
	inner := f.Server.Config.Handler
	f.Server.Config.Handler = httpHandler(func(h map[string][]string) { auth = append(auth, strings.Join(h["Authorization"], "")) }, inner)
	run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{Values: map[string]any{"apiKey": "ollama"}})
	run(t, newClient(f.URL), graniteModel, userTranscript("hi"), sdk.ProviderStreamOptions{Values: map[string]any{"apiKey": "sk-cloud"}})
	if !reflect.DeepEqual(auth, []string{"", "Bearer sk-cloud"}) {
		t.Errorf("Authorization headers = %q", auth)
	}
}
