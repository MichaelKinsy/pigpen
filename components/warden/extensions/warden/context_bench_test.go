package warden

import (
	"fmt"
	"strings"
	"testing"
)

// Benchmarks of the context saver's hot paths on representative inputs: a 200 KB build log, a 100 KB compiler log,
// a 5,000-line report pasted again, and a 120-call span for the relevance compaction.

func benchLog(lines int) string {
	out := make([]string, lines)
	for i := range out {
		out[i] = fmt.Sprintf("progress %d complete: compiled module %d without warnings", i, i)
	}
	out[lines/2] = "ERROR: parser.test.ts keeps the header: expected 3 to equal 4"
	return strings.Join(out, "\n") + "\nsummary: 1 failed, 3999 passed\nexit code 1\n"
}

func benchTsc(lines int) string {
	out := make([]string, 0, lines+3)
	for i := 0; i < lines; i++ {
		if i%40 == 0 {
			out = append(out, fmt.Sprintf("src/f%d.ts(%d,5): error TS2322: Type 'string' is not assignable to type 'number'.", i, i%90+1))
		} else {
			out = append(out, fmt.Sprintf("progress %d complete: compiled module %d", i, i))
		}
	}
	return strings.Join(out, "\n") + fmt.Sprintf("\nFound %d errors in %d files.", lines/40, lines/40)
}

func BenchmarkCompressOutputGeneric200KB(b *testing.B) {
	text := benchLog(3500)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := CompressOutput(text, RetentionErrorsAndSummary, ""); !ok {
			b.Fatal("no excerpt")
		}
	}
}

func BenchmarkCompressOutputParserTsc100KB(b *testing.B) {
	text := benchTsc(2000)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := CompressOutput(text, RetentionErrorsAndSummary, FormatTsc); !ok {
			b.Fatal("no excerpt")
		}
	}
}

func BenchmarkOutputKey200KB(b *testing.B) {
	text := benchLog(3500)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		_ = OutputKey(text)
	}
}

func BenchmarkSeenTextAddFind5000Lines(b *testing.B) {
	report := dedupeReport(5000, "report")
	text := "Turn 4\n" + report + "\n" + dedupeFresh(4000)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		seen := NewSeenText()
		seen.Add(report, "an earlier bash result")
		if runs := seen.Find(text); len(runs) == 0 {
			b.Fatal("no run")
		}
	}
}

func BenchmarkFilterChunksAndBatches200KB(b *testing.B) {
	text := benchLog(3500)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		chunks := ChunkLines(text, 2000)
		_ = BuildFilterBatches(chunks, filterInput, FilterRequestBytes)
	}
}

func benchSpan() []SpanMessage {
	messages := []SpanMessage{userMsg(strings.Repeat("u", 3000))}
	for i := 0; i < 120; i++ {
		messages = append(messages,
			assistant(textBlock(fmt.Sprintf("step %d %s", i, strings.Repeat("t", 600))), toolCall(fmt.Sprintf("c%d", i), "bash", map[string]any{"command": fmt.Sprintf("run %d %s", i, strings.Repeat("a", 900))})),
			toolResult(fmt.Sprintf("c%d", i), "bash", strings.Repeat("o", 5000), false))
	}
	return messages
}

func BenchmarkRelevanceBuildRequests120Calls(b *testing.B) {
	messages := benchSpan()
	b.ReportAllocs()
	for b.Loop() {
		units, _ := BuildUnits(RelevanceInput{Messages: messages})
		if len(BuildRequests(units, RelevanceInput{Task: "t"}, MaxQuestions)) == 0 {
			b.Fatal("no request")
		}
	}
}

func BenchmarkRelevanceRenderSummary120Calls(b *testing.B) {
	units, files := BuildUnits(RelevanceInput{Messages: benchSpan()})
	keep := keepAllUnits(units)
	b.ReportAllocs()
	for b.Loop() {
		_ = RenderSummary(units, files, keep, nil)
	}
}
