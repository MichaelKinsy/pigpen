// fakellm: the measurement model for port/perf. A scripted OpenAI-compatible chat-completions server on a loopback
// port, like `pigeq llm`, with these differences the perf workloads need: the reported usage follows the request
// (prompt_tokens = request body bytes / 4), long texts are streamed in 16 KB deltas, a request past the end of the
// script is answered with a plain text turn instead of an error, and GET /stats reports when the first and the last
// request arrived (Unix nanoseconds) so the driver can time "prompt sent -> first model request".
// GET /stats returns {"requests":N,"overrun":M,"bytes":B,"firstRequestUnixNs":T0,"lastRequestUnixNs":T1}.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}
type turn struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []toolCall `json:"toolCalls,omitempty"`
}

const deltaBytes = 16 << 10

func main() {
	script := flag.String("script", "", "turns.json")
	flag.Parse()
	var turns []turn
	if data, err := os.ReadFile(*script); err != nil || json.Unmarshal(data, &turns) != nil {
		fmt.Fprintln(os.Stderr, "fakellm: cannot read script", *script, err)
		os.Exit(2)
	}
	var requests, overrun, total, first, last atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"requests":%d,"overrun":%d,"bytes":%d,"firstRequestUnixNs":%d,"lastRequestUnixNs":%d}`,
			requests.Load(), overrun.Load(), total.Load(), first.Load(), last.Load())
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixNano()
		first.CompareAndSwap(0, now)
		last.Store(now)
		var size int64
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Body.Read(buf)
			size += int64(n)
			if err != nil {
				break
			}
		}
		total.Add(size)
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
		for off := 0; off < len(t.Text); off += deltaBytes {
			end := off + deltaBytes
			if end > len(t.Text) {
				end = len(t.Text)
			}
			send(map[string]any{"content": t.Text[off:end]}, nil)
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
