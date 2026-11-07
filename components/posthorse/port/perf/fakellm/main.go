// fakellm: the measurement model for port/perf. A scripted OpenAI-compatible chat-completions server on a loopback
// port, like `pigeq llm`, with two differences the perf workloads need: the reported usage follows the request
// (prompt_tokens = request body bytes / 4, so context usage grows with the transcript and Pi's compaction threshold,
// which Posthorse hooks for its rollover, fires from real growth), and a request past the end of the script is
// answered with a plain text turn instead of an error. GET /stats returns {"requests":N,"overrun":M}.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
)

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}
type turn struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []toolCall `json:"toolCalls,omitempty"`
}

func main() {
	script := flag.String("script", "", "turns.json")
	flag.Parse()
	var turns []turn
	if data, err := os.ReadFile(*script); err != nil || json.Unmarshal(data, &turns) != nil {
		fmt.Fprintln(os.Stderr, "fakellm: cannot read script", *script, err)
		os.Exit(2)
	}
	var requests, overrun atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"requests":%d,"overrun":%d}`, requests.Load(), overrun.Load())
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var size int64
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Body.Read(buf)
			size += int64(n)
			if err != nil {
				break
			}
		}
		i := int(requests.Add(1) - 1)
		t := turn{Text: "done"}
		if i < len(turns) {
			t = turns[i]
		} else {
			overrun.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta map[string]any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("pf-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "pf-1",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		send(map[string]any{"role": "assistant", "content": ""}, nil)
		if t.Text != "" {
			send(map[string]any{"content": t.Text}, nil)
		}
		finish := "stop"
		for j, c := range t.ToolCalls {
			args, _ := json.Marshal(c.Arguments)
			send(map[string]any{"tool_calls": []any{map[string]any{"index": j, "id": fmt.Sprintf("call_%d_%d", i, j), "type": "function",
				"function": map[string]any{"name": c.Name, "arguments": string(args)}}}}, nil)
			finish = "tool_calls"
		}
		send(map[string]any{}, finish)
		prompt, completion := size/4, int64(len(t.Text)/4+1)
		usage, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("pf-%d", i), "object": "chat.completion.chunk", "created": 1, "model": "pf-1", "choices": []any{},
			"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakellm:", err)
		os.Exit(2)
	}
	fmt.Printf("http://%s/v1\n", ln.Addr())
	os.Stdout.Sync()
	go func() { // lifetime = stdin, like pigeq llm
		buf := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				os.Exit(0)
			}
		}
	}()
	_ = http.Serve(ln, mux)
}
