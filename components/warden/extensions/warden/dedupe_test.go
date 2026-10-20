package warden

import (
	"fmt"
	"strings"
	"testing"
)

// Twins of tests/dedupe.test.ts.

func dedupeReport(lines int, tag string) string {
	out := make([]string, lines)
	for i := range out {
		out[i] = fmt.Sprintf("%s line %d: the build of module %d passed with no warnings at all", tag, i, i)
	}
	return strings.Join(out, "\n")
}

func dedupeFresh(chars int) string {
	n := (chars + 49) / 50
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("new status line %d %s", i, strings.Repeat("n", 28))
	}
	return strings.Join(out, "\n")
}

// twin: tests/dedupe.test.ts:8
func TestReportPastedAgainIsFoundOnceWithWhereTheEarlierCopyIs(t *testing.T) {
	seen := NewSeenText()
	seen.Add(dedupeReport(40, "report"), "an earlier subagent message")
	text := "Turn 3 report\n" + dedupeReport(40, "report") + "\n" + dedupeFresh(2500)
	runs := seen.Find(text)
	if len(runs) != 1 || runs[0].Start != 1 || runs[0].Lines != 40 || runs[0].Where != "an earlier subagent message" {
		t.Fatalf("%+v", runs)
	}
	collapsed := CollapseRuns(text, runs, "/tmp/full.txt")
	mustMatch(t, collapsed, `^Turn 3 report\n\[pi-warden: the next 40 lines repeat an earlier subagent message — omitted; full text: /tmp/full\.txt\]\nnew status line 0`, "pointer")
	if !strings.HasSuffix(collapsed, dedupeFresh(2500)) {
		t.Error("the new part is kept")
	}
}

// twin: tests/dedupe.test.ts:20
func TestTrailingSpacesDoNotBreakAMatchAChangedLineDoes(t *testing.T) {
	seen := NewSeenText()
	seen.Add(dedupeReport(40, "report"), "an earlier bash result")
	lines := strings.Split(dedupeReport(40, "report"), "\n")
	spaced := make([]string, len(lines))
	for i, l := range lines {
		spaced[i] = l + "   "
	}
	if got := seen.Find(strings.Join(spaced, "\n") + "\n" + dedupeFresh(2500)); len(got) != 1 {
		t.Fatalf("%d runs", len(got))
	}
	// One changed line in the middle leaves two halves of 20 and 19 lines: each is under 1500 characters, so nothing is cut.
	lines[20] = strings.Replace(lines[20], "passed", "FAILED", 1)
	if got := seen.Find(strings.Join(lines, "\n") + "\n" + dedupeFresh(2500)); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// twin: tests/dedupe.test.ts:31
func TestRunUnder20LinesOr1500CharactersIsKept(t *testing.T) {
	seen := NewSeenText()
	short := make([]string, 30)
	for i := range short {
		short[i] = fmt.Sprintf("ok %d", i)
	}
	shortText := strings.Join(short, "\n")
	seen.Add(shortText, "an earlier bash result")
	seen.Add(dedupeReport(19, "report"), "an earlier read result")
	if got := seen.Find(shortText + "\n" + dedupeFresh(2500)); len(got) != 0 {
		t.Errorf("30 lines but few characters: %+v", got)
	}
	if got := seen.Find(dedupeReport(19, "report") + "\n" + dedupeFresh(2500)); len(got) != 0 {
		t.Errorf("many characters but 19 lines: %+v", got)
	}
}

// twin: tests/dedupe.test.ts:40
func TestLast2000CharactersAreNeverCut(t *testing.T) {
	seen := NewSeenText()
	seen.Add(dedupeReport(100, "report"), "an earlier user message")
	text := dedupeReport(100, "report")
	runs := seen.Find(text)
	if len(runs) != 1 || runs[0].Start != 0 || runs[0].Lines >= 100 {
		t.Fatalf("the run stops before the tail: %+v", runs)
	}
	collapsed := CollapseRuns(text, runs, "/tmp/full.txt")
	if !strings.HasSuffix(collapsed, utf16Slice(text, utf16Len(text)-TailKeepChars, -1)) {
		t.Error("tail kept")
	}
	// A text whose repeat sits wholly in its tail is unchanged.
	if got := seen.Find(dedupeReport(30, "report")); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

// twin: tests/dedupe.test.ts:54
func TestTwoSeparateRunsFromDifferentSourcesEachGetTheirOwnPointer(t *testing.T) {
	seen := NewSeenText()
	seen.Add(dedupeReport(30, "alpha"), "an earlier bash result")
	seen.Add(dedupeReport(30, "beta"), "an earlier user message")
	text := dedupeReport(30, "beta") + "\nbetween\n" + dedupeReport(30, "alpha") + "\n" + dedupeFresh(2500)
	runs := seen.Find(text)
	want := []RepeatedRun{{Start: 0, Lines: 30, Where: "an earlier user message"}, {Start: 31, Lines: 30, Where: "an earlier bash result"}}
	if len(runs) != 2 {
		t.Fatalf("%+v", runs)
	}
	for i, w := range want {
		if runs[i].Start != w.Start || runs[i].Lines != w.Lines || runs[i].Where != w.Where {
			t.Errorf("run %d: %+v", i, runs[i])
		}
	}
	collapsed := CollapseRuns(text, runs, "/tmp/f")
	got := strings.Join(strings.Split(collapsed, "\n")[:3], "|")
	if got != "[pi-warden: the next 30 lines repeat an earlier user message — omitted; full text: /tmp/f]|between|[pi-warden: the next 30 lines repeat an earlier bash result — omitted; full text: /tmp/f]" {
		t.Errorf("%s", got)
	}
}

// twin: tests/dedupe.test.ts:65
func TestSyncAddsNewEntriesOnceAndRebuildsWhenAnEntryLeavesTheContext(t *testing.T) {
	seen := NewSeenText()
	first := SeenEntry{ID: "a", Type: "message", Message: &SeenMessage{Role: "toolResult", ToolName: "bash", Content: []any{map[string]any{"type": "text", "text": dedupeReport(40, "report")}}}}
	second := SeenEntry{ID: "b", Type: "custom_message", CustomType: "subagent-report", Content: "short"}
	a, _ := SeenItemOf(first)
	b, _ := SeenItemOf(second)
	seen.Sync([]SeenItem{a, b})
	text := dedupeReport(40, "report") + "\n" + dedupeFresh(2500)
	if runs := seen.Find(text); len(runs) == 0 || runs[0].Where != "an earlier bash result" {
		t.Fatalf("%+v", runs)
	}
	// A compaction drops the first entry: its text is no longer in context and nothing may point to it.
	seen.Sync([]SeenItem{b})
	if got := seen.Find(text); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// twin: tests/dedupe.test.ts:77
func TestSeenItemReadsUserAssistantToolAndCustomTextsNeverImages(t *testing.T) {
	item, ok := SeenItemOf(SeenEntry{ID: "u", Type: "message", Message: &SeenMessage{Role: "user", Content: "hi"}})
	if !ok || item.Key != "u" || item.Where != "an earlier user message" || len(item.Texts) != 1 || item.Texts[0] != "hi" {
		t.Errorf("%+v", item)
	}
	item, ok = SeenItemOf(SeenEntry{ID: "c", Type: "custom_message", CustomType: "relay", Content: []any{
		map[string]any{"type": "text", "text": "x"}, map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}}})
	if !ok || item.Key != "c" || item.Where != "an earlier relay message" || len(item.Texts) != 1 || item.Texts[0] != "x" {
		t.Errorf("%+v", item)
	}
	if _, ok := SeenItemOf(SeenEntry{ID: "s", Type: "compaction"}); ok {
		t.Error("a compaction summary is not indexed")
	}
}
