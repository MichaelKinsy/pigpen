package commit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/test/commit-selective-journal.test.ts

const testJournalFile = "/repo/src/a.ts"

func recordTestWrite(j *ChangeJournal, before, after *string) {
	j.RecordChange(ChangeInput{
		Agent:  "Main",
		Tool:   "edit",
		Path:   testJournalFile,
		Before: before,
		After:  after,
	})
}

func replayTestWrites(t *testing.T, j *ChangeJournal) ([][2]*string, bool) {
	t.Helper()
	recs, err := j.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	var changes []JournalRecord
	for _, r := range recs {
		if r.Type == "change" {
			changes = append(changes, r)
		}
	}
	writes, ok, err := j.Snapshots(changes)
	if err != nil {
		t.Fatalf("Snapshots failed: %v", err)
	}
	if !ok {
		return nil, false
	}
	out := make([][2]*string, len(writes))
	for i, w := range writes {
		out[i] = [2]*string{w.Before, w.After}
	}
	return out, true
}

func bytesUnderDir(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return total
}

func assertWritePairsEqual(t *testing.T, got, want [][2]*string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d writes, want %d", len(got), len(want))
	}
	for i := range want {
		if !equalStringPtr(got[i][0], want[i][0]) || !equalStringPtr(got[i][1], want[i][1]) {
			t.Fatalf("write[%d] mismatch:\ngot  before=%v after=%v\nwant before=%v after=%v", i, got[i][0], got[i][1], want[i][0], want[i][1])
		}
	}
}

// upstream: commit-selective-journal.test.ts: "replays every write with the content around it, through changes nobody recorded"
func TestJournalReplaysThroughGaps(t *testing.T) {
	j := NewChangeJournal(t.TempDir())
	oldLines := make([]string, 3000)
	newLines := make([]string, 3000)
	for i := range 3000 {
		oldLines[i] = fmt.Sprintf("old %d", i)
		newLines[i] = fmt.Sprintf("new %d", i)
	}
	old := strings.Join(oldLines, "\n") + "\n"
	rewritten := strings.Join(newLines, "\n") + "\n"
	rewritten2 := strings.Replace(strings.Replace(rewritten, "new 1500\n", "new 1500 edited\n", 1), "new 7\n", "", 1)

	writes := [][2]*string{
		{nil, strPtr("a\nb\nc\n")},
		{strPtr("a\nb\nc\n"), strPtr("a\nB\nc\n")},
		{strPtr("a\nB\nc\nextra"), strPtr("a\nB\nextra")},
		{strPtr(old), strPtr(rewritten)},
		{strPtr(rewritten), strPtr(rewritten2)},
		{strPtr("\uFEFFx\r\ny\r\n"), strPtr("\uFEFFx\r\nz\r\n")},
		{strPtr("\uFEFFx\r\nz\r\n"), nil},
		{nil, strPtr("")},
	}
	for _, w := range writes {
		recordTestWrite(j, w[0], w[1])
	}
	got, ok := replayTestWrites(t, j)
	if !ok {
		t.Fatalf("expected replayable writes")
	}
	assertWritePairsEqual(t, got, writes)
}

// upstream: commit-selective-journal.test.ts: "stores a later write as its changes, not as another copy of the file"
func TestJournalStoresDeltasCompactly(t *testing.T) {
	dir := t.TempDir()
	j := NewChangeJournal(dir)
	var seed uint32 = 1
	lines := make([]string, 4000)
	for i := range 4000 {
		seed = seed*1103515245 + 12345
		lines[i] = fmt.Sprintf("%s %s %x", strconv.FormatUint(uint64(seed), 36), hashBytesBase36([]byte(strconv.FormatUint(uint64(seed), 10))), seed)
	}
	versions := []string{strings.Join(lines, "\n") + "\n"}
	for edit := range 30 {
		lines[edit*100] = fmt.Sprintf("edited %d", edit)
		versions = append(versions, strings.Join(lines, "\n")+"\n")
		recordTestWrite(j, strPtr(versions[edit]), strPtr(versions[edit+1]))
	}
	got, ok := replayTestWrites(t, j)
	if !ok || len(got) == 0 {
		t.Fatalf("expected replayable writes")
	}
	last := got[len(got)-1]
	if !equalStringPtr(last[0], strPtr(versions[29])) || !equalStringPtr(last[1], strPtr(versions[30])) {
		t.Fatalf("last replayed write did not match expected versions[29..30]")
	}
	if size := bytesUnderDir(t, dir); size >= int64(len(versions[0])) {
		t.Fatalf("journal dir size %d >= single file size %d", size, len(versions[0]))
	}
}

// upstream: commit-selective-journal.test.ts: "continues a path after a restart and drops history written against content it cannot see"
func TestJournalRestartAndStaleWriter(t *testing.T) {
	dir := t.TempDir()
	first := NewChangeJournal(dir)
	recordTestWrite(first, nil, strPtr("one\n"))
	first.Flush()

	second := NewChangeJournal(dir)
	recordTestWrite(second, strPtr("one\nuser\n"), strPtr("one\nuser\ntwo\n"))
	got, ok := replayTestWrites(t, second)
	if !ok {
		t.Fatalf("expected replay after restart")
	}
	assertWritePairsEqual(t, got, [][2]*string{
		{nil, strPtr("one\n")},
		{strPtr("one\nuser\n"), strPtr("one\nuser\ntwo\n")},
	})

	recordTestWrite(first, strPtr("one\nuser\ntwo\n"), strPtr("one\nuser\ntwo\nthree\n"))
	recordTestWrite(first, strPtr("one\nuser\ntwo\nthree\n"), strPtr("one\nuser\ntwo\nthree\nfour\n"))
	first.Flush()
	if _, ok := replayTestWrites(t, second); ok {
		t.Fatalf("expected unreplayable history after stale writer")
	}

	third := NewChangeJournal(dir)
	recordTestWrite(third, strPtr("one\nuser\ntwo\nthree\nfour\n"), strPtr("five\n"))
	got3, ok3 := replayTestWrites(t, third)
	if !ok3 {
		t.Fatalf("expected replay starting from new base")
	}
	assertWritePairsEqual(t, got3, [][2]*string{
		{strPtr("one\nuser\ntwo\nthree\nfour\n"), strPtr("five\n")},
	})
}
