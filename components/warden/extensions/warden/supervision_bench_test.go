package warden

import (
	"strconv"
	"strings"
	"testing"
)

// The per-result cost of the supervision module: the call-waste window runs on every tool result, the stuck
// evidence builds only when a judge request goes out, and the report triage only when the agent settles.
//
//	go test -run xxx -bench 'Waste|Evidence|Triage' -benchmem
func BenchmarkWasteRecord(b *testing.B) {
	calls := []WasteCall{
		wbash("go test ./... -count=1", "ok  \tpkg\t0.2s\n"),
		wcall("read", map[string]any{"path": "src/a.go", "offset": float64(1), "limit": float64(40)}, wlines(40)),
		wbash("grep -n foo src/a.go", "12:foo\n"),
		wbash("git status --short", " M a.go\n"),
		wbash("sleep 1; echo running", "running\n"),
		wcall("edit", map[string]any{"path": "src/a.go", "edits": []any{}}, "ok"),
	}
	t := NewWasteTracker()
	c := wcfg()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		t.Record(calls[i%len(calls)], c)
	}
}

func BenchmarkBuildStuckEvidence(b *testing.B) {
	var attempts []Attempt
	for i := range 12 {
		attempts = append(attempts, evBash("go test ./...", "--- FAIL: TestX"+strconv.Itoa(i)+" (0.00s)\n    x_test.go:"+strconv.Itoa(40+i)+": got 1, want 2\nFAIL\nFAIL\texample.com/x\t0.01s\n", true))
		if i%3 == 0 {
			attempts = append(attempts, evEdit("x.go", "a\nb\nc", "a\nB\nc"))
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		BuildStuckEvidence(attempts, EvidenceLimit)
	}
}

func BenchmarkParseFailureTAP(b *testing.B) {
	out := evSamples[0].output + strings.Repeat("ok 2 - fine\n", 200)
	b.ReportAllocs()
	for b.Loop() {
		ParseFailure(out)
	}
}

func BenchmarkTriageReportOffline(b *testing.B) {
	r := subReport(subCompletion, false, "subagent-notify")
	o := TriageOptions{Config: DefaultConfig().Subagent}
	b.ReportAllocs()
	for b.Loop() {
		TriageReport(newContext(), r, o)
	}
}
