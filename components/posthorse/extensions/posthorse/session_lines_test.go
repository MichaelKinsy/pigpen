package posthorse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSessionFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(fsTemp(t), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsMatcher(needle, encoding string, overlap int) *lineMatcher {
	return &lineMatcher{test: func(text string) bool { return strings.Contains(text, needle) }, encoding: encoding, overlap: overlap}
}

type scanned struct {
	head, text string
	matches    bool
}

func scan(t *testing.T, path string, matcher *lineMatcher) []scanned {
	t.Helper()
	var out []scanned
	for line, err := range sessionLines(nil, path, matcher) {
		if err != nil {
			t.Fatal(err)
		}
		text, err := line.text()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, scanned{head: strings.Clone(line.head), text: strings.Clone(text), matches: line.matches})
	}
	return out
}

func TestSessionLinesWithoutMatcher(t *testing.T) {
	path := writeSessionFile(t, `{"a":1}`, "", "ünï", "last")
	got := scan(t, path, nil)
	want := []string{`{"a":1}`, "", "ünï", "last"}
	if len(got) != len(want) {
		t.Fatalf("lines = %+v", got)
	}
	for i, line := range got {
		if line.text != want[i] || line.head != want[i] || !line.matches {
			t.Errorf("line %d = %+v, want %q matching", i, line, want[i])
		}
	}
}

func TestSessionLinesHeadIsThe512BytePrefix(t *testing.T) {
	long := strings.Repeat("é", 400) // 800 bytes
	got := scan(t, writeSessionFile(t, long), nil)
	// 512 bytes of two-byte characters: 256 whole characters.
	if len(got) != 1 || got[0].head != strings.Repeat("é", 256) || got[0].text != long {
		t.Errorf("head %d bytes, text %d bytes", len(got[0].head), len(got[0].text))
	}
	// A cut through a character decodes as U+FFFD.
	odd := "x" + strings.Repeat("é", 300)
	got = scan(t, writeSessionFile(t, odd), nil)
	if !strings.HasSuffix(got[0].head, "\uFFFD") {
		t.Errorf("a split character at the 512th byte decodes with a replacement: %q", got[0].head[len(got[0].head)-4:])
	}
}

func TestSessionLinesMatchersAcrossChunkBoundaries(t *testing.T) {
	const chunk = 1024 * 1024
	needle := "NEEDLE-123"
	first := "short " + needle
	// The needle starts four bytes before the end of the first 1 MiB read of the file.
	line := strings.Repeat("x", chunk-4-len(first)-1) + needle + strings.Repeat("y", 100)
	other := strings.Repeat("z", chunk+50)
	path := writeSessionFile(t, first, line, other)
	for _, encoding := range []string{"latin1", "utf8"} {
		got := scan(t, path, containsMatcher(needle, encoding, len(needle)-1))
		if len(got) != 3 || !got[0].matches || !got[1].matches || got[2].matches {
			t.Errorf("%s: matches = %v %v %v", encoding, got[0].matches, got[1].matches, got[2].matches)
		}
		if got[1].text != line || got[2].text != other {
			t.Errorf("%s: texts must read back by position", encoding)
		}
	}
	// Without the overlap the straddling needle is missed (the overlap is the matcher's contract).
	got := scan(t, path, containsMatcher(needle, "utf8", 1))
	if got[1].matches {
		t.Error("a needle longer than the overlap can be missed at a chunk boundary")
	}
}

func TestSessionLinesUTF8HoldsBackIncompleteSequences(t *testing.T) {
	const chunk = 1024 * 1024
	// The 4-byte character is split by the 1 MiB read: its first two bytes end chunk 1.
	line := strings.Repeat("a", chunk-2) + "😀tail"
	got := scan(t, writeSessionFile(t, line), containsMatcher("😀tail", "utf8", 8))
	if len(got) != 1 || !got[0].matches {
		t.Errorf("a split character is decoded whole in the next window: %+v", got)
	}
	// latin1 sees raw bytes: the UTF-8 bytes of the needle are found by their byte string.
	got = scan(t, writeSessionFile(t, line), containsMatcher("\xf0\x9f\x98\x80tail", "latin1", 8))
	if !got[0].matches {
		t.Error("latin1 windows are raw bytes")
	}
}

func TestSessionLinesZeroOverlapKeepsTheWholeWindow(t *testing.T) {
	const chunk = 1024 * 1024
	needle := "ZZ"
	line := strings.Repeat("x", chunk-1) + needle
	// `window.slice(-0)` is the whole window in JavaScript, so nothing is lost at the boundary.
	got := scan(t, writeSessionFile(t, line), containsMatcher(needle, "utf8", 0))
	if !got[0].matches {
		t.Error("overlap 0 keeps the whole window as the tail")
	}
}

func TestUTF8TailCountsUTF16Units(t *testing.T) {
	window := []byte("ab😀cd")
	for _, tc := range []struct {
		overlap int
		want    string
	}{
		{0, "ab😀cd"}, {1, "d"}, {2, "cd"}, {3, u16string([]uint16{0xDE00}) + "cd"}, {4, "😀cd"}, {99, "ab😀cd"},
	} {
		if got := string(utf8Tail(nil, window, tc.overlap)); got != tc.want {
			t.Errorf("utf8Tail(overlap %d) = %q, want %q", tc.overlap, got, tc.want)
		}
	}
}

func TestUTF8DecoderStreams(t *testing.T) {
	var d utf8Decoder
	out := d.write(nil, []byte("a\xe2"))
	if string(out) != "a" {
		t.Errorf("held back: %q", out)
	}
	out = d.write(out[:0], []byte("\x82\xac!"))
	if string(out) != "€!" {
		t.Errorf("completed: %q", out)
	}
	out = d.write(out[:0], []byte("\xf0\x9f"))
	out = d.end(out)
	if string(out) != "\uFFFD" {
		t.Errorf("a truncated sequence flushes as one replacement: %q", out)
	}
	out = d.write(out[:0], []byte("\xe2\x28"))
	if string(out) != "\uFFFD(" {
		t.Errorf("an invalid continuation is replaced without eating the next byte: %q", out)
	}
}

func TestSessionLinesErrors(t *testing.T) {
	missing := filepath.Join(fsTemp(t), "none.jsonl")
	n := 0
	for _, err := range sessionLines(nil, missing, nil) {
		n++
		if err == nil || !strings.Contains(err.Error(), "ENOENT") {
			t.Errorf("open error = %v", err)
		}
	}
	if n != 1 {
		t.Errorf("one error expected, got %d", n)
	}
	path := writeSessionFile(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range sessionLines(ctx, path, nil) {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("aborted scan: %v", err)
		}
	}
}

func TestSessionLinesTextReadsByPositionWhenTheFileChanges(t *testing.T) {
	const chunk = 1024 * 1024
	path := writeSessionFile(t, strings.Repeat("q", chunk+10))
	for line, err := range sessionLines(nil, path, containsMatcher("nomatch", "utf8", 3)) {
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, 100); err != nil {
			t.Fatal(err)
		}
		_, err = line.text()
		if err == nil || err.Error() != "Session file changed while reading a history entry." {
			t.Errorf("a shrunk file: %v", err)
		}
	}
}

func benchSession(b *testing.B) (string, int) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "session.jsonl")
	line := `{"type":"message","id":"abc","message":{"role":"user","content":"` + strings.Repeat("lorem ipsum ", 60) + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 40_000)), 0o644); err != nil {
		b.Fatal(err)
	}
	return path, len(line) * 40_000
}

func BenchmarkSessionLinesScan(b *testing.B) {
	path, size := benchSession(b)
	matcher := containsMatcher("needle-that-is-absent", "utf8", 20)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for range b.N {
		for _, err := range sessionLines(nil, path, matcher) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkSessionLinesScanLatin1(b *testing.B) {
	path, size := benchSession(b)
	matcher := containsMatcher("needle-that-is-absent", "latin1", 20)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for range b.N {
		for _, err := range sessionLines(nil, path, matcher) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
