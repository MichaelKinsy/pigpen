package herdr

import (
	"strings"
	"testing"
)

// The reporter runs on agent_start, agent_settled and prompt events. Each handler takes the lock, decides
// whether the state changed, and only a change queues a herdr call (a process exec, off the handler's path).
// These benchmarks keep the reporter released so no herdr process is started: they measure the handler logic.
//
//	go test -run xxx -bench . -benchmem
func benchReporter() *reporter {
	return &reporter{env: herdrEnv{bin: "/bin/true", paneID: "p1"}, rootSession: true, released: true}
}

func BenchmarkReporterStartSettled(b *testing.B) {
	r := benchReporter()
	path, id := "/home/u/.pig/agent/sessions/--work--/2026-01-01_abc.jsonl", "0199abcd-0000-7000-8000-000000000000"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.agentStarted(path, id)
		r.agentSettled()
	}
}

// The same state again (a second agent_start in one run, a repeated prompt event) is dropped without queueing.
func BenchmarkReporterUnchangedState(b *testing.B) {
	r := benchReporter()
	r.agentStarted("/s.jsonl", "id")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.agentStarted("/s.jsonl", "id")
	}
}

func BenchmarkReportArgs(b *testing.B) {
	r := benchReporter()
	r.sessionPath, r.sessionID = "/home/u/.pig/agent/sessions/--work--/2026-01-01_abc.jsonl", "0199abcd-0000-7000-8000-000000000000"
	rep := report{state: stateBlocked, message: "Allow this tool call?", seq: 7}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.mu.Lock()
		_ = r.reportArgsLocked(rep, true)
		r.mu.Unlock()
	}
}

func BenchmarkMessageFrom(b *testing.B) {
	title := strings.Repeat("Allow bash to run a long command?\n  with several lines\tand tabs ", 3)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = messageFrom(title)
	}
}
