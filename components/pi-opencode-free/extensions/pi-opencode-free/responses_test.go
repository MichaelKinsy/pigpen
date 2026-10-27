// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func responsesModel() map[string]any {
	return map[string]any{
		"id": "muse-spark-free", "name": "Muse Spark", "api": "openai-responses",
		"provider": ProviderID, "reasoning": true, "input": []any{"text"},
		"thinkingLevelMap": map[string]any{"high": "high"},
	}
}

func TestResponsesStreamTextReasoningAndTool(t *testing.T) {
	var body map[string]any
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body = decodeBody(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`,
			`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"think"}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}]}}`,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1"}}`,
			`data: {"type":"response.output_text.delta","output_index":1,"delta":"Hello"}`,
			`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Hello"}]}}`,
			`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read"}}`,
			`data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":\"README.md\"}"}`,
			`data: {"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"README.md\"}"}}`,
			`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
			``,
		}, "\n"))
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{
		Values: map[string]any{"maxTokens": float64(200), "reasoning": "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	want := []string{"start", "thinking_start", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_end", "done"}
	assertEqual(t, s.types(), want)
	if s.terminal()["reason"] != "toolUse" {
		t.Errorf("reason = %v", s.terminal()["reason"])
	}
	msg := s.terminal()["message"].(map[string]any)
	if msg["api"] != "openai-responses" || msg["provider"] != ProviderID {
		t.Errorf("identity = %#v", msg)
	}
	blocks := msg["content"].([]any)
	if blocks[0].(map[string]any)["thinking"] != "think" {
		t.Errorf("thinking = %#v", blocks[0])
	}
	if blocks[1].(map[string]any)["text"] != "Hello" {
		t.Errorf("text = %#v", blocks[1])
	}
	call := blocks[2].(map[string]any)
	if call["id"] != "call_1" || call["name"] != "read" {
		t.Errorf("call = %#v", call)
	}
	assertEqual(t, call["arguments"], map[string]any{"path": "README.md"})
	usage := msg["usage"].(map[string]any)
	if usage["input"] != 10 || usage["output"] != 4 || usage["totalTokens"] != 14 {
		t.Errorf("usage = %#v", usage)
	}
	if path != "/responses" {
		t.Errorf("path = %q", path)
	}
	if body["model"] != "muse-spark-free" || body["stream"] != true || body["store"] != false {
		t.Errorf("body = %#v", body)
	}
	if body["max_output_tokens"] != float64(200) {
		t.Errorf("max_output_tokens = %#v", body["max_output_tokens"])
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Errorf("reasoning = %#v", body["reasoning"])
	}
	input, _ := body["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
		t.Errorf("input = %#v", input)
	}
}

func TestResponsesRequestMapsTranscriptAndTools(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = decodeBody(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	transcript := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "Be brief.",
			"toolsAdded": []any{map[string]any{"name": "read", "description": "Read", "parameters": map[string]any{"type": "object"}}}},
		map[string]any{"role": "user", "content": "look"},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "checking"},
			map[string]any{"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "a.txt"}},
		}},
		map[string]any{"role": "toolResult", "toolCallId": "call_1", "toolName": "read",
			"content": []any{map[string]any{"type": "text", "text": "contents"}}},
	}}
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), transcript, sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, stream)

	input, _ := body["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input = %#v", input)
	}
	if input[0].(map[string]any)["role"] != "system" || input[0].(map[string]any)["content"] != "Be brief." {
		t.Errorf("system = %#v", input[0])
	}
	if input[2].(map[string]any)["type"] != "message" || input[2].(map[string]any)["id"] != "msg_pi_2" {
		t.Errorf("assistant message = %#v", input[2])
	}
	if input[3].(map[string]any)["type"] != "function_call" || input[3].(map[string]any)["call_id"] != "call_1" {
		t.Errorf("function call = %#v", input[3])
	}
	if input[4].(map[string]any)["type"] != "function_call_output" || input[4].(map[string]any)["output"] != "contents" {
		t.Errorf("tool output = %#v", input[4])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" || tools[0].(map[string]any)["name"] != "read" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestResponsesFunctionCallOnlyInDoneItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Join([]string{
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"}}`,
			`data: {"type":"response.completed","response":{"status":"completed"}}`,
			``,
		}, "\n"))
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	want := []string{"start", "toolcall_start", "toolcall_delta", "toolcall_end", "done"}
	assertEqual(t, s.types(), want)
	blocks := s.terminal()["message"].(map[string]any)["content"].([]any)
	assertEqual(t, blocks[0].(map[string]any)["arguments"], map[string]any{"path": "a.txt"})
}

func TestResponsesIncompleteMapsToLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	if s.terminal()["type"] != "done" || s.terminal()["reason"] != "length" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
}

func TestResponsesErrorEventBecomesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"error\",\"code\":\"invalid_request\",\"message\":\"bad request\"}\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	if s.terminal()["type"] != "error" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
	message := s.terminal()["error"].(map[string]any)["errorMessage"].(string)
	if !strings.Contains(message, "bad request") {
		t.Errorf("error = %q", message)
	}
}

func TestResponsesMissingTerminalIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n")
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := collect(t, stream)
	if s.terminal()["type"] != "error" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
}

func TestResponsesAbort(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{baseURL: server.URL, http: server.Client(), newID: func() string { return "call_test" }}
	stream, err := c.stream(responsesModel(), userTranscript("hi"), sdk.ProviderStreamOptions{Signal: ctx})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	s := collect(t, stream)
	if s.terminal()["type"] != "error" || s.terminal()["reason"] != "aborted" {
		t.Fatalf("terminal = %#v", s.terminal())
	}
}
