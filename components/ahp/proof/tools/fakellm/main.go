// Command fakellm is a scripted OpenAI-compatible chat-completions server for the differential
// proof: pi-ahp under Pi and the Go port under PiG talk to it through an ordinary custom
// provider, so the model side is identical in both lanes and no credentials are involved.
//
//	fakellm -script turns.json [-addr 127.0.0.1:0]
//
// The script is a JSON array of turns: {"text": "...", "chunks": ["a","b"], "delayMs": 5,
// "toolCalls": [{"name": "read", "arguments": {...}}]}. Turn N answers request N; running out
// of script answers HTTP 500 and is logged. It prints "LISTENING <base url>" on stdout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type turn struct {
	Text      string     `json:"text,omitempty"`
	Chunks    []string   `json:"chunks,omitempty"`
	DelayMs   int        `json:"delayMs,omitempty"`
	ToolCalls []toolCall `json:"toolCalls,omitempty"`
}

func main() {
	script := flag.String("script", "", "JSON file with the scripted turns")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	flag.Parse()
	raw, err := os.ReadFile(*script)
	if err != nil {
		log.Fatal(err)
	}
	var turns []turn
	if err := json.Unmarshal(raw, &turns); err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	next := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := next
		next++
		mu.Unlock()
		if i >= len(turns) {
			log.Printf("script exhausted: request %d but %d turns", i+1, len(turns))
			http.Error(w, "script exhausted", http.StatusInternalServerError)
			return
		}
		t := turns[i]
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(delta map[string]any, finish any) {
			chunk := map[string]any{"id": fmt.Sprintf("fake-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "fake-1",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		pause := func() {
			if t.DelayMs > 0 {
				time.Sleep(time.Duration(t.DelayMs) * time.Millisecond)
			}
		}
		send(map[string]any{"role": "assistant", "content": ""}, nil)
		chunks := t.Chunks
		if len(chunks) == 0 && t.Text != "" {
			chunks = []string{t.Text}
		}
		for _, c := range chunks {
			pause()
			send(map[string]any{"content": c}, nil)
		}
		finish := "stop"
		for j, c := range t.ToolCalls {
			pause()
			args, _ := json.Marshal(c.Arguments)
			send(map[string]any{"tool_calls": []any{map[string]any{"index": j, "id": fmt.Sprintf("call_%d_%d", i, j), "type": "function",
				"function": map[string]any{"name": c.Name, "arguments": string(args)}}}}, nil)
			finish = "tool_calls"
		}
		send(map[string]any{}, finish)
		usage, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("fake-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "fake-1", "choices": []any{},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
		if flusher != nil {
			flusher.Flush()
		}
	})
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("LISTENING http://%s/v1\n", ln.Addr())
	log.Fatal(http.Serve(ln, mux))
}
