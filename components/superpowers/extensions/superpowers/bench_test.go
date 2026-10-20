package superpowers

import "testing"

// BenchmarkWithBootstrap is the cost of the context handler's work on a request that gets the bootstrap: scanning
// the messages for the marker and building the list. The bootstrap text itself is built once.
func BenchmarkWithBootstrap(b *testing.B) {
	messages := []any{
		map[string]any{"role": "compactionSummary", "summary": "Prior work summary", "tokensBefore": 123, "timestamp": 1},
		userMessage("Let us make a react todo list", 2),
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
		userMessage("Now add a delete button", 3),
	}
	bootstrapContent()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if withBootstrap(messages) == nil {
			b.Fatal("no bootstrap")
		}
	}
}

// BenchmarkAlreadyInjected is the request that already holds the bootstrap: the scan finds the marker and stops.
func BenchmarkAlreadyInjected(b *testing.B) {
	messages := []any{userMessage(bootstrapContent(), 1), userMessage("Let us make a react todo list", 2)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if withBootstrap(messages) != nil {
			b.Fatal("injected twice")
		}
	}
}
