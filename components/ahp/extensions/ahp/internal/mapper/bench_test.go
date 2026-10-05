package mapper_test

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// One streamed text delta through the turn mapper: this runs for every token batch of a turn while a remote
// client is attached (the listener is off, and nothing here runs, unless it is configured).
//
//	go test -run xxx -bench . -benchmem
func BenchmarkMapTextDelta(b *testing.B) {
	m := mapper.NewTurnMapper("turn-1", 0)
	m.Handle(pi.agentStart())
	m.Handle(pi.assistantStart())
	delta := pi.text(0, "some streamed text from the model ")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Handle(delta)
	}
}

func BenchmarkMapToolExecution(b *testing.B) {
	m := mapper.NewTurnMapper("turn-1", 0)
	m.Handle(pi.agentStart())
	m.Handle(pi.assistantStart())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Handle(pi.toolStart(1, "call-1", "bash"))
		m.Handle(pi.toolDelta(1, `{"command":"go test ./..."}`))
		m.Handle(pi.toolEnd(1, "call-1", "bash", map[string]any{"command": "go test ./..."}))
		m.Handle(pi.execUpdate("call-1", "bash", "ok  \tpkg\t0.1s\n"))
		m.Handle(pi.execEnd("call-1", "bash", "ok  \tpkg\t0.1s\n", false))
	}
}
