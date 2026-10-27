// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestChatStreamTextAndReasoning(t *testing.T) {
	var body map[string]any
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		headers = r.Header.Clone()
		body = decodeBody(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"reasoning_content":"think "}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"more"}}]}`,
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[{"finish_reason":"stop"}]}`,
			`data: {"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":2}}}`,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	model := freeModel("hy3-free")
	model["reasoning"] = true
	model["thinkingLevelMap"] = map[string]any{"high": "high"}
	stream, err := c.stream(model, userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"maxTokens": float64(1234), "reasoning": "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	want := []string{"start", "thinking_start", "thinking_delta", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_delta", "text_end", "done"}
	assertEqual(t, s.types(), want)
	if s.terminal()["reason"] != "stop" {
		t.Errorf("reason = %v", s.terminal()["reason"])
	}
	msg := s.terminal()["message"].(map[string]any)
	blocks := msg["content"].([]any)
	if blocks[0].(map[string]any)["thinking"] != "think more" || blocks[1].(map[string]any)["text"] != "Hello world" {
		t.Errorf("content = %#v", blocks)
	}
	usage := msg["usage"].(map[string]any)
	if usage["input"] != 18 || usage["output"] != 5 || usage["cacheRead"] != 2 || usage["totalTokens"] != 25 {
		t.Errorf("usage = %#v", usage)
	}
	if msg["api"] != "openai-completions" || msg["provider"] != ProviderID {
		t.Errorf("identity = %#v", msg)
	}

	if body["model"] != "hy3-free" || body["stream"] != true || body["max_tokens"] != float64(1234) {
		t.Errorf("request = %#v", body)
	}
	if body["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %#v", body["reasoning_effort"])
	}
	streamOptions, _ := body["stream_options"].(map[string]any)
	if streamOptions["include_usage"] != true {
		t.Errorf("stream_options = %#v", body["stream_options"])
	}
	if headers.Get("x-opencode-client") != "cli" || headers.Get("x-opencode-project") != "global" {
		t.Errorf("headers = %#v", headers)
	}
	if headers.Get("Authorization") != "" {
		t.Errorf("Authorization must not be sent: %q", headers.Get("Authorization"))
	}
	if headers.Get("User-Agent") != UserAgent {
		t.Errorf("User-Agent = %q", headers.Get("User-Agent"))
	}
}

func TestChatStreamToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"README.md\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
			`data: {"choices":[{"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("look"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	want := []string{"start", "toolcall_start", "toolcall_delta", "toolcall_delta",
		"toolcall_start", "toolcall_delta", "toolcall_end", "toolcall_end", "done"}
	assertEqual(t, s.types(), want)
	if s.terminal()["reason"] != "toolUse" {
		t.Errorf("reason = %v", s.terminal()["reason"])
	}
	blocks := s.terminal()["message"].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v", blocks)
	}
	first := blocks[0].(map[string]any)
	if first["id"] != "call_1" || first["name"] != "read" {
		t.Errorf("first = %#v", first)
	}
	assertEqual(t, first["arguments"], map[string]any{"path": "README.md"})
	second := blocks[1].(map[string]any)
	assertEqual(t, second["arguments"], map[string]any{"cmd": "ls"})
}

func TestChatRequestMapsTranscriptAndTools(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	transcript := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "Be brief.", "sections": map[string]any{"cwd": "<cwd>/work</cwd>"},
			"toolsAdded": []any{
				map[string]any{"name": "read", "description": "Read a file", "parameters": map[string]any{"type": "object"}},
				map[string]any{"name": "bash", "description": "Run", "parameters": map[string]any{"type": "object"}},
			}},
		map[string]any{"role": "user", "content": "look"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "checking"},
			map[string]any{"type": "thinking", "thinking": "hmm"},
			map[string]any{"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "a.txt"}},
		}},
		map[string]any{"role": "toolResult", "toolCallId": "call_1", "toolName": "read",
			"content": []any{map[string]any{"type": "text", "text": "contents"}}},
	}}
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), transcript, sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, stream)

	messages, _ := body["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	system := messages[0].(map[string]any)
	if system["role"] != "system" || !strings.Contains(system["content"].(string), "Be brief.") || !strings.Contains(system["content"].(string), "<cwd>/work</cwd>") {
		t.Errorf("system = %#v", system)
	}
	assistant := messages[2].(map[string]any)
	if assistant["content"] != "checking" || assistant["reasoning_content"] != "hmm" {
		t.Errorf("assistant = %#v", assistant)
	}
	calls := assistant["tool_calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["id"] != "call_1" {
		t.Errorf("tool_calls = %#v", calls)
	}
	tool := messages[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "contents" {
		t.Errorf("tool result = %#v", tool)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %#v", tools)
	}
	first := tools[0].(map[string]any)["function"].(map[string]any)
	if first["name"] != "read" || first["description"] != "Read a file" {
		t.Errorf("tool = %#v", first)
	}
}

func TestChatHTTPErrorBecomesErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"no free access"}}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	if s.terminal()["type"] != "error" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
	message := s.terminal()["error"].(map[string]any)["errorMessage"].(string)
	if !strings.Contains(message, "401") || !strings.Contains(message, "no free access") {
		t.Errorf("error = %q", message)
	}
}

func TestChatAbort(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{Signal: ctx})
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, cancel)
	s := collect(t, stream)
	if s.terminal()["type"] != "error" || s.terminal()["reason"] != "aborted" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
	blocks := s.terminal()["error"].(map[string]any)["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["text"] != "partial" {
		t.Errorf("partial = %#v", blocks)
	}
}

func TestChatTruncatedStreamIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	// The compat flag supportsFinishReason is false, so a clean EOF after
	// content infers a normal stop instead of failing.
	if s.terminal()["type"] != "done" || s.terminal()["reason"] != "stop" {
		t.Errorf("terminal = %#v", s.terminal())
	}
}

func TestChatPayloadHookCanReplaceBody(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{
		OnPayload: func(value any, _ map[string]any) (any, error) {
			payload := value.(map[string]any)
			payload["temperature"] = 0.25
			return payload, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, stream)
	if body["temperature"] != 0.25 {
		t.Fatalf("body = %#v", body)
	}
}

func TestChatForbiddenAuthorizationHeaderIsRemovable(t *testing.T) {
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(freeModel("hy3-free"), userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"headers": map[string]any{
			"Authorization":     nil,
			"x-opencode-client": "custom",
			"x-extra":           "1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, stream)
	if headers.Get("Authorization") != "" {
		t.Errorf("Authorization = %q", headers.Get("Authorization"))
	}
	if headers.Get("x-opencode-client") != "custom" || headers.Get("x-extra") != "1" {
		t.Errorf("headers = %#v", headers)
	}
}

func TestChatRequestUsesModelBaseURL(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := freeModel("hy3-free")
	model["baseUrl"] = server.URL + "/custom/v1"
	c := &client{baseURL: "http://unused.invalid", http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(model, userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, stream)
	if path != "/custom/v1/chat/completions" {
		t.Fatalf("path = %q", path)
	}
}

func TestChatDecodesNonObjectToolArguments(t *testing.T) {
	if _, err := parseToolArguments(`["not","object"]`); err == nil {
		t.Fatal("expected an error")
	}
	args, err := parseToolArguments(`{"a":1}`)
	if err != nil || args["a"] != float64(1) {
		t.Fatalf("args = %#v, %v", args, err)
	}
	if args, err := parseToolArguments(""); err != nil || len(args) != 0 {
		t.Fatalf("empty args = %#v, %v", args, err)
	}
}
