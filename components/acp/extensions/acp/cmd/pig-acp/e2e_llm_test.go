package main

// A scripted OpenAI-compatible chat-completions server, so the end-to-end tests drive a real
// `pig --mode rpc` without credentials or a network (the same idea as the equivalence harness).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type llmCall struct {
	Name string
	Args map[string]any
}

// llmTurn is one scripted model reply.
type llmTurn struct {
	Text  string
	Calls []llmCall
	// Chunks streams Text in this many pieces (default 1).
	Chunks int
	// Hold blocks the reply after its first chunk until the channel closes or the client disconnects.
	Hold chan struct{}
	// Started is closed when the first chunk of this turn has been sent.
	Started chan struct{}
}

type fakeLLM struct {
	t     *testing.T
	ln    net.Listener
	srv   *http.Server
	mu    sync.Mutex
	turns []llmTurn
	next  int
	// requests records the last user text of each request.
	requests []string
}

func startFakeLLM(t *testing.T, turns ...llmTurn) *fakeLLM {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeLLM{t: t, ln: ln, turns: turns}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", f.handle)
	f.srv = &http.Server{Handler: mux}
	go func() { _ = f.srv.Serve(ln) }()
	t.Cleanup(func() { _ = f.srv.Close() })
	return f
}

func (f *fakeLLM) baseURL() string { return "http://" + f.ln.Addr().String() + "/v1" }

func (f *fakeLLM) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	last := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			var s string
			if json.Unmarshal(m.Content, &s) == nil {
				last = s
			} else {
				var parts []struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(m.Content, &parts)
				if len(parts) > 0 {
					last = parts[0].Text
				}
			}
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, last)
	i := f.next
	f.next++
	f.mu.Unlock()
	if i >= len(f.turns) {
		http.Error(w, "llm script exhausted", http.StatusInternalServerError)
		return
	}
	turn := f.turns[i]
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("acp-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "acp-1",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	send(map[string]any{"role": "assistant", "content": ""}, nil)
	chunks := turn.Chunks
	if chunks < 1 {
		chunks = 1
	}
	if turn.Text != "" {
		pieces := splitText(turn.Text, chunks)
		for n, p := range pieces {
			send(map[string]any{"content": p}, nil)
			if n == 0 && turn.Started != nil {
				close(turn.Started)
			}
			if n == 0 && turn.Hold != nil {
				select {
				case <-turn.Hold:
				case <-r.Context().Done():
					return
				}
			}
		}
	} else if turn.Started != nil {
		close(turn.Started)
	}
	finish := "stop"
	for j, c := range turn.Calls {
		args, _ := json.Marshal(c.Args)
		send(map[string]any{"tool_calls": []any{map[string]any{"index": j, "id": fmt.Sprintf("call_%d_%d", i, j), "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": string(args)}}}}, nil)
		finish = "tool_calls"
	}
	send(map[string]any{}, finish)
	usage, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("acp-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "acp-1", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": 1234, "completion_tokens": 5, "total_tokens": 1239}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	flusher.Flush()
}

func splitText(s string, n int) []string {
	if n <= 1 || len(s) < n {
		return []string{s}
	}
	words := strings.SplitAfter(s, " ")
	if len(words) < n {
		return []string{s}
	}
	per := (len(words) + n - 1) / n
	var out []string
	for i := 0; i < len(words); i += per {
		end := i + per
		if end > len(words) {
			end = len(words)
		}
		out = append(out, strings.Join(words[i:end], ""))
	}
	return out
}

var _ = time.Second
