package jsonrpc

import (
	"io"
	"testing"
)

// One session/update notification written through the connection's queue and writer goroutine.
//
//	go test -run xxx -bench . -benchmem
func BenchmarkNotify(b *testing.B) {
	pr, pw := io.Pipe()
	defer pw.Close()
	c := New(pr, io.Discard, func(*Request) (any, error) { return nil, nil })
	params := map[string]any{"sessionId": "s1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "some streamed text from the model"}}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Notify("session/update", params); err != nil {
			b.Fatal(err)
		}
	}
	c.Drain(0)
}
