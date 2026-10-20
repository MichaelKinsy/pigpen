package warden

import (
	"fmt"
	"strings"
	"testing"
)

// Benchmarks of the security lane on representative inputs; port/bench/security-bench.ts runs the same cases against
// the original (port/bench/run.sh interleaves the two and prints medians).

func benchOutput() string {
	var b strings.Builder
	for i := 0; b.Len() < 64*1024; i++ {
		if i == 600 {
			b.WriteString("export API_KEY=k7Rm2Qx9Lp4Ns8Vt1Wd6Yc3Zb5Hf7Jg0Pk2Mn5Qz\n")
		}
		fmt.Fprintf(&b, "build step %d: compiled module_%d.o in %d ms, token count %d\n", i, i%97, (i*37)%1000, i*13)
	}
	return b.String()
}

func benchReplyParagraphs() string {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Paragraph %d explains one distinct part of the merge process in its own words.", i)
	}
	return b.String()
}

var benchSink any

func BenchmarkMaskSecrets64KB(b *testing.B) {
	text := benchOutput()
	b.ReportAllocs()
	for b.Loop() {
		out, n := MaskSecrets(text)
		benchSink = [2]any{out, n}
	}
}

func BenchmarkFindSecrets64KB(b *testing.B) {
	text := benchOutput()
	b.ReportAllocs()
	for b.Loop() {
		benchSink = FindSecrets(text)
	}
}

func BenchmarkRunawayStream(b *testing.B) {
	text := []rune(benchReplyParagraphs())
	cfg := DefaultConfig().Runaway
	b.ReportAllocs()
	for b.Loop() {
		m := NewRunawayMonitor()
		m.Feed(StreamEvent{Type: "text_start"})
		for i := 0; i < len(text); i += 7 {
			d := string(text[i:min(i+7, len(text))])
			if kind := m.Feed(StreamEvent{Type: "text_delta", Delta: &d}); kind != "" {
				benchSink = m.Check(kind, cfg)
			}
		}
	}
}

func BenchmarkRestatedShare(b *testing.B) {
	reply := "CON-375 is complete: the draft pull request 2688 is pushed together with code, tests and screenshots, and Linear sits In Review. The worktree millia-con375 now awaits review. " + strings.Repeat("Every check passed and nothing else changed in the worktree today. ", 3)
	finals := []string{proseDone, proseFresh, proseDone, proseFresh}
	b.ReportAllocs()
	for b.Loop() {
		benchSink = RestatedShare(reply, finals)
	}
}

func BenchmarkBuildProseRequest(b *testing.B) {
	reply := strings.Repeat("Great question! Let me walk you through it. ", 40)
	b.ReportAllocs()
	for b.Loop() {
		benchSink = BuildProseRequest("explain the bug", reply, "technical")
	}
}
