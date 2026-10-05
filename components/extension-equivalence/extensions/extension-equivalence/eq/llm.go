package eq

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// fakeLLM is a scripted OpenAI-compatible chat-completions server. Both Pi and
// PiG talk to it through an ordinary custom provider, so the model side of a
// scenario is identical in every lane and no credentials are involved.
type fakeLLM struct {
	srv    *http.Server
	ln     net.Listener
	turns  []Turn
	record func(ch string, data any)
	// system maps the (normalized) system text of a request to the value
	// recorded for it, or reports false to record nothing. See systemDelta.
	system func(text string) (any, bool)

	mu   sync.Mutex
	next int
}

func newFakeLLM(turns []Turn, record func(ch string, data any)) (*fakeLLM, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &fakeLLM{ln: ln, turns: turns, record: record}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", f.handle)
	f.srv = &http.Server{Handler: mux}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

func (f *fakeLLM) baseURL() string { return "http://" + f.ln.Addr().String() + "/v1" }
func (f *fakeLLM) close()          { _ = f.srv.Close() }

type chatRequest struct {
	Messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls []struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name        string          `json:"name"`
			Description *string         `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Record what the model would see: the messages, every tool definition
	// (name, description and parameter schema: an extension's tool is defined
	// by all three, and a port that changes any of them changes what the model
	// is told), and the extension's part of the system text. The system text is
	// mostly the host's own (Pi and PiG word it differently), so only its
	// difference from the host's baseline prompt is recorded (systemDelta).
	var msgs []map[string]any
	var system []string
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			system = append(system, contentText(m.Content))
			continue
		}
		entry := map[string]any{"role": m.Role, "text": contentText(m.Content)}
		var calls []any
		for _, c := range m.ToolCalls {
			calls = append(calls, map[string]any{"name": c.Function.Name, "arguments": c.Function.Arguments})
		}
		if calls != nil {
			entry["toolCalls"] = calls
		}
		msgs = append(msgs, entry)
	}
	tools := []any{}
	for _, t := range req.Tools {
		def := map[string]any{"name": t.Function.Name}
		if t.Function.Description != nil {
			def["description"] = *t.Function.Description
		}
		if len(t.Function.Parameters) > 0 {
			params, err := decodeAny(t.Function.Parameters)
			if err != nil {
				params = string(t.Function.Parameters)
			}
			def["parameters"] = params
		}
		tools = append(tools, def)
	}
	entry := map[string]any{"messages": msgs, "tools": tools}
	if f.system != nil {
		if v, ok := f.system(strings.Join(system, "\n")); ok {
			entry["system"] = v
		}
	}
	f.record(ChLLM, entry)

	f.mu.Lock()
	i := f.next
	f.next++
	f.mu.Unlock()
	if i >= len(f.turns) {
		f.record(ChError, map[string]any{"message": fmt.Sprintf("llm script exhausted: request %d but %d turns", i+1, len(f.turns))})
		http.Error(w, "llm script exhausted", http.StatusInternalServerError)
		return
	}
	turn := f.turns[i]

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(delta map[string]any, finish any) {
		chunk := map[string]any{
			"id": fmt.Sprintf("eq-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "eq-1",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	send(map[string]any{"role": "assistant", "content": ""}, nil)
	if turn.Text != "" {
		send(map[string]any{"content": turn.Text}, nil)
	}
	finish := "stop"
	for j, c := range turn.ToolCalls {
		args, _ := json.Marshal(c.Arguments)
		send(map[string]any{"tool_calls": []any{map[string]any{
			"index": j, "id": fmt.Sprintf("call_%d_%d", i, j), "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": string(args)},
		}}}, nil)
		finish = "tool_calls"
	}
	send(map[string]any{}, finish)
	usage, _ := json.Marshal(map[string]any{
		"id": fmt.Sprintf("eq-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "eq-1", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	if flusher != nil {
		flusher.Flush()
	}
}

// systemDelta describes how the system text of a request differs from the
// host's baseline system text (the same host and arguments, no extension under
// test): the longest common prefix and suffix are removed and the rest is
// reported. Both texts are normalized first. It reports false when the texts
// are equal, so an extension that does not touch the system prompt adds
// nothing to the trace. What an extension appends, inserts or replaces
// (before_agent_start systemPrompt, a tool's promptSnippet or guidelines) is
// compared exactly; the host's own wording never is, unless the extension
// removed it (then "removed" holds the host text it removed).
func systemDelta(base, got string) (any, bool) {
	if base == got {
		return nil, false
	}
	b, g := []rune(base), []rune(got)
	p := 0
	for p < len(b) && p < len(g) && b[p] == g[p] {
		p++
	}
	s := 0
	for s < len(b)-p && s < len(g)-p && b[len(b)-1-s] == g[len(g)-1-s] {
		s++
	}
	return map[string]any{"inserted": string(g[p : len(g)-s]), "removed": string(b[p : len(b)-s])}, true
}

// ServeLLM starts the scripted OpenAI-compatible model server standalone (for `pigeq llm`): the
// same server the scenarios use, so a port's own end-to-end test sees the same scripted turns.
// Every request is written to log as one JSON line {"ch","data"}. It returns the base URL (ends
// in /v1) and a stop function.
func ServeLLM(turns []Turn, log io.Writer) (string, func(), error) {
	var mu sync.Mutex
	f, err := newFakeLLM(turns, func(ch string, data any) {
		if log == nil {
			return
		}
		line, _ := json.Marshal(map[string]any{"ch": ch, "data": data})
		mu.Lock()
		defer mu.Unlock()
		_, _ = log.Write(append(line, '\n'))
	})
	if err != nil {
		return "", nil, err
	}
	return f.baseURL(), f.close, nil
}
