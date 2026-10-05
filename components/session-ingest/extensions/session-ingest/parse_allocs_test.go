package sessioningest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each line of the file used to be copied twice before it was decoded (Scanner.Text, then []byte of that string).
func TestParseSessionFileDoesNotCopyEachLineTwice(t *testing.T) {
	var lines []string
	lines = append(lines, `{"type":"session","id":"s1","version":3,"cwd":"/w","timestamp":"2026-01-01T00:00:00Z"}`)
	pad := strings.Repeat("x", 4000)
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"message","id":"m","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"`+pad+`"}]}}`)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var bytes float64
	allocBytes := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if s, err := parseSessionFile(path, 100000); err != nil || len(s.Messages) != 50 {
				b.Fatal(err, len(s.Messages))
			}
		}
	})
	bytes = float64(allocBytes.AllocedBytesPerOp())
	// 50 lines of ~4.1 KB: the decoded text is kept (about 205 KB); a second copy of every line is not.
	if bytes > 205_000*2.6 {
		t.Fatalf("parsing allocated %.0f bytes for a 205 KB file; each line is being copied more than once", bytes)
	}
}
