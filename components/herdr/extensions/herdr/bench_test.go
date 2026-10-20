package herdr

import "testing"

// Allocations and time of the reporter's own work, without a herdr: the drain goroutine is held off (sending is set), so
// these measure the state logic and the request encoding.
//
//	go test -run xxx -bench . -benchmem

func benchReporter() *reporter {
	return &reporter{env: herdrEnv{endpoint: "/nonexistent", paneID: "w1:p1"}, rootSession: true, sending: true}
}

func BenchmarkTurn(b *testing.B) {
	r := benchReporter()
	resume := []string{"pig", "--session", "/home/user/.pig/agent/sessions/one.jsonl"}
	b.ReportAllocs()
	for b.Loop() {
		r.agentStarted(resume)
		r.agentSettled()
	}
}

// The same state again is dropped without queueing.
func BenchmarkUnchangedState(b *testing.B) {
	r := benchReporter()
	resume := []string{"pig", "--session", "/s.jsonl"}
	r.agentStarted(resume)
	b.ReportAllocs()
	for b.Loop() {
		r.agentStarted(resume)
	}
}

func BenchmarkBlockUnblock(b *testing.B) {
	r := benchReporter()
	label := newJSValue("approve command")
	b.ReportAllocs()
	for b.Loop() {
		r.block(holderBus, label)
		r.unblock(holderBus)
	}
}

func BenchmarkEncodeStateRequest(b *testing.B) {
	payload := statePayload{PaneID: "w1:p1", Source: source, Agent: agent, State: stateWorking, Seq: 1792509903106737, ResumeArgv: []string{"pig", "--session", "/home/user/.pig/agent/sessions/one.jsonl"}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeLine(request{ID: requestID("state"), Method: "pane.report_agent", Params: payload}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResumeArgvFor(b *testing.B) {
	file, id := "/home/user/.pig/agent/sessions/one.jsonl", "0199abcd-0000-7000-8000-000000000000"
	b.ReportAllocs()
	for b.Loop() {
		_ = resumeArgvFor(&file, &id)
	}
}
