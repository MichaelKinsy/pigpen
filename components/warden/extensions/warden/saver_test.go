package warden

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Twins of tests/saver.test.ts.

func wantSnapshot(t *testing.T, got, want ContextLedgerSnapshot) {
	t.Helper()
	if want.Filter.Fallbacks == nil {
		want.Filter.Fallbacks = map[string]int{}
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Fatalf("snapshot\n got %s\nwant %s", g, w)
	}
}

// twin: tests/saver.test.ts:7
func TestLedgerCountsCandidatesCompressionsTokenTurnsAndFirstRecallsOnly(t *testing.T) {
	l := NewContextLedger()
	mustMatch(t, FormatLedger(l.Snapshot()), `no tool output large enough`, "empty")
	l.Candidate()
	l.Candidate()
	l.Record("/tmp/pi-warden-output-a/output.txt", 40_000, &StoredSource{Tool: "bash", Bytes: 50_000})
	l.TurnEnd()
	l.TurnEnd()
	l.Record("/tmp/pi-warden-output-b/output.txt", 8_000, &StoredSource{Tool: "bash", Bytes: 50_000})
	l.TurnEnd()
	if p, _ := l.NoteAccess(`{"path":"/tmp/pi-warden-output-a/output.txt"}`, RecallFull); p != "/tmp/pi-warden-output-a/output.txt" {
		t.Fatalf("first access %q", p)
	}
	if p, _ := l.NoteAccess("cat /tmp/pi-warden-output-a/output.txt | tail", RecallScoped); p != "/tmp/pi-warden-output-a/output.txt" {
		t.Fatalf("a second access is reported but not counted twice: %q", p)
	}
	if p, ok := l.NoteAccess(`{"path":"/tmp/unrelated.txt"}`, RecallFull); ok {
		t.Fatalf("unrelated %q", p)
	}
	s := l.Snapshot()
	wantSnapshot(t, s, ContextLedgerSnapshot{Large: 2, Compressed: 2, BytesSaved: 48_000, Turns: 3, TokenTurnsSaved: 10_000 + 10_000 + 12_000, Recalls: 1, RecallsFull: 1})
	mustMatch(t, FormatLedger(s), `2 large outputs, 2 compressed, 0 duplicates dropped, 46\.9 KB removed \(~12000 tokens\), ~32000 token-turns spared over 3 turns, 1 recall of the full output \(50%; 1 whole-file, 0 scoped\)`, "line")
	l.Reset()
	wantSnapshot(t, l.Snapshot(), ContextLedgerSnapshot{})
}

// twin: tests/saver.test.ts:27
func TestTokenTurnsAreRemovedTokensTimesTurnsInEffect(t *testing.T) {
	l := NewContextLedger()
	l.Record("/tmp/pi-warden-output-e/output.txt", 4_000, nil)
	l.TurnEnd()
	l.TurnEnd()
	l.TurnEnd()
	if got := l.Snapshot().TokenTurnsSaved; got != 3_000 {
		t.Fatalf("%d", got)
	}
}

// twin: tests/saver.test.ts:36
func TestWholeFileRecallStopsCountingScopedRecallDoesNot(t *testing.T) {
	for _, c := range []struct {
		kind RecallKind
		want int
	}{{RecallFull, 1_000}, {RecallScoped, 3_000}} {
		l := NewContextLedger()
		l.Record("/tmp/pi-warden-output-f/output.txt", 4_000, nil)
		l.TurnEnd()
		l.NoteAccess("cat /tmp/pi-warden-output-f/output.txt", c.kind)
		l.TurnEnd()
		l.TurnEnd()
		if got := l.Snapshot().TokenTurnsSaved; got != c.want {
			t.Errorf("%s: %d want %d", c.kind, got, c.want)
		}
	}
}

// twin: tests/saver.test.ts:48
func TestDuplicatesAreRememberedByKeyKeepFirstStoredCopyAndCountApart(t *testing.T) {
	l := NewContextLedger()
	if _, _, ok := l.DuplicateOf("k1"); ok {
		t.Fatal("unknown key")
	}
	l.Remember("k1", "bash", "")
	if tool, path, ok := l.DuplicateOf("k1"); !ok || tool != "bash" || path != "" {
		t.Fatalf("%q %q %v", tool, path, ok)
	}
	l.Remember("k1", "bash", "/tmp/pi-warden-output-c/output.txt")
	l.Remember("k1", "read", "")
	if tool, path, _ := l.DuplicateOf("k1"); tool != "bash" || path != "/tmp/pi-warden-output-c/output.txt" {
		t.Fatalf("a later copy without a file does not discard the stored path: %q %q", tool, path)
	}
	l.Duplicate(30_000)
	if p, _ := l.StoredPathIn("rg -n 'x' /tmp/pi-warden-output-c/output.txt"); p != "/tmp/pi-warden-output-c/output.txt" {
		t.Fatalf("stored path %q", p)
	}
	if p, ok := l.NoteAccess("rg -n 'x' /tmp/pi-warden-output-c/output.txt", RecallScoped); ok {
		t.Fatalf("a duplicate-only copy is not a compression recall: %q", p)
	}
	l.NoteAccess("anything", RecallScoped)
	s := l.Snapshot()
	if s.Duplicates != 1 || s.Compressed != 0 || s.BytesSaved != 30_000 {
		t.Fatalf("%+v", s)
	}
	mustMatch(t, FormatLedger(s), `0 large outputs, 0 compressed, 1 duplicate dropped, 29\.3 KB removed`, "line")
	win := `C:\Temp\pi-warden-output-d\output.txt`
	l.Record(win, 1_000, &StoredSource{Tool: "bash", Bytes: 50_000})
	if p, _ := l.StoredPathIn(`{"path":"C:\\Temp\\pi-warden-output-d\\output.txt"}`); p != win {
		t.Fatalf("a Windows path matches in its JSON form: %q", p)
	}
	l.NoteAccess(`{"command":"findstr /n /c:\"x\" C:\\Temp\\pi-warden-output-d\\output.txt"}`, RecallScoped)
	if s := l.Snapshot(); s.Recalls != 1 || s.RecallsFull != 0 {
		t.Fatalf("%d %d", s.Recalls, s.RecallsFull)
	}
	mustMatch(t, FormatLedger(l.Snapshot()), `1 recall of the full output \(100%; 0 whole-file, 1 scoped\)`, "line")
}

// twin: tests/saver.test.ts:72
func TestRepeatedRunsCountEarnTokenTurnsAndReadingTheStoredCopyIsARecall(t *testing.T) {
	l := NewContextLedger()
	l.Repeat("/tmp/pi-warden-output-r/output.txt", 8_000, "subagent-report message", 10_000)
	l.TurnEnd()
	l.TurnEnd()
	s := l.Snapshot()
	if s.Repeats != 1 || s.BytesSaved != 8_000 || s.TokenTurnsSaved != 4_000 {
		t.Fatalf("%+v", s)
	}
	mustMatch(t, FormatLedger(s), `0 large outputs, 0 compressed, 0 duplicates dropped, 1 repeat cut, 7\.8 KB removed`, "line")
	if p, _ := l.NoteAccess(`{"path":"/tmp/pi-warden-output-r/output.txt"}`, RecallFull); p != "/tmp/pi-warden-output-r/output.txt" {
		t.Fatalf("%q", p)
	}
	l.TurnEnd()
	if got := l.Snapshot().TokenTurnsSaved; got != 4_000 {
		t.Fatalf("a whole-file recall puts the text back, so it stops counting: %d", got)
	}
	mustMatch(t, FormatLedger(l.Snapshot()), `1 recall of the full output \(100%; 1 whole-file, 0 scoped\)`, "line")
}

// twin: tests/saver.test.ts:86
func TestFilteredAndExcerptOutputsAreCountedApart(t *testing.T) {
	l := NewContextLedger()
	l.Record("/tmp/pi-warden-output-f/output.txt", 9_000, &StoredSource{Tool: "bash", Bytes: 16_000, Kind: KindFiltered, KeptChars: 5_200})
	l.FilterSpent(1, 850, "")
	l.Record("/tmp/pi-warden-output-x/output.txt", 10_000, &StoredSource{Tool: "bash", Bytes: 16_000, Kind: KindExcerpt})
	l.FilterSpent(2, 4_000, "timeout")
	l.Record("/tmp/pi-warden-output-y/output.txt", 10_000, &StoredSource{Tool: "bash", Bytes: 16_000, Kind: KindExcerpt})
	l.FilterSpent(0, 0, "no_consent")
	l.Record("/tmp/pi-warden-output-p/output.txt", 10_000, &StoredSource{Tool: "bash", Bytes: 16_000, Kind: KindParser})
	l.NoteAccess("rg error /tmp/pi-warden-output-f/output.txt", RecallScoped)
	l.NoteAccess(`{"path":"/tmp/pi-warden-output-x/output.txt"}`, RecallFull)
	l.NoteAccess(`{"path":"/tmp/pi-warden-output-p/output.txt"}`, RecallFull)
	s := l.Snapshot()
	if s.Compressed != 4 {
		t.Errorf("every kind still counts as compressed: %d", s.Compressed)
	}
	wantFilter := FilterCounts{CompressionCounts: CompressionCounts{Count: 1, Recalls: 1}, KeptChars: 5_200, Requests: 3, Ms: 4_850, Fallbacks: map[string]int{"timeout": 1, "no_consent": 1}, Order: []string{"timeout", "no_consent"}}
	if !reflect.DeepEqual(s.Filter, wantFilter) {
		t.Errorf("filter %+v", s.Filter)
	}
	if want := (CompressionCounts{Count: 2, Recalls: 1, RecallsFull: 1}); s.Excerpt != want {
		t.Errorf("a parser excerpt is neither kind: %+v", s.Excerpt)
	}
	want := "Context filter (beta): 1 filtered (5200 characters kept), 1 recalled (100%; 0 whole-file, 1 scoped); 2 excerpts, 1 recalled (50%; 1 whole-file, 0 scoped); 3 requests, 4850 ms; fallbacks: timeout 1, no_consent 1."
	if got := FormatFilterLedger(s); got != want {
		t.Errorf("filter line\n got %s\nwant %s", got, want)
	}
	l.Reset()
	if len(l.Snapshot().Filter.Fallbacks) != 0 {
		t.Error("reset")
	}
	if strings.Contains(FormatLedger(ContextLedgerSnapshot{Large: 1}), "NaN") {
		t.Error("nan")
	}
}
