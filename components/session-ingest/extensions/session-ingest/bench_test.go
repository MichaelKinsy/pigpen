package sessioningest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// read_session parses the whole session file on every call (then slices, queries or tables it). A long session
// is tens of MB of JSONL, mostly tool output.
//
//	go test -run xxx -bench . -benchmem
func benchSession(b *testing.B, messages int) string {
	b.Helper()
	var s strings.Builder
	s.WriteString(`{"type":"session","id":"0199abcd","version":3,"cwd":"/work/repo","timestamp":"2026-01-01T00:00:00Z"}` + "\n")
	for i := 0; i < messages; i++ {
		fmt.Fprintf(&s, `{"type":"message","id":"u%d","timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"user","content":[{"type":"text","text":"please fix the failing test number %d"}]}}`+"\n", i, i%60, i)
		fmt.Fprintf(&s, `{"type":"message","id":"a%d","timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"assistant","content":[{"type":"text","text":"Looking at it now."},{"type":"toolCall","id":"c%d","name":"bash","arguments":{"command":"go test ./..."}}],"usage":{"input":1200,"output":80,"cacheRead":900,"cacheWrite":0,"totalTokens":2180,"cost":{"total":0.0123}}}}`+"\n", i, i%60, i)
		fmt.Fprintf(&s, `{"type":"message","id":"t%d","timestamp":"2026-01-01T00:00:%02dZ","message":{"role":"toolResult","toolCallId":"c%d","toolName":"bash","content":[{"type":"text","text":%q}]}}`+"\n", i, i%60, i, strings.Repeat("ok  \tgithub.com/example/pkg\t0.123s\n", 20))
	}
	path := filepath.Join(b.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(s.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

func BenchmarkParseSessionFile(b *testing.B) {
	path := benchSession(b, 2000)
	info, _ := os.Stat(path)
	b.SetBytes(info.Size())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := parseSessionFile(path, 2000); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildQueryReport(b *testing.B) {
	session, err := parseSessionFile(benchSession(b, 2000), 2000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildQueryReport(session, "failing test", false, false, 0, 10, 300); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildTOCReport(b *testing.B) {
	session, err := parseSessionFile(benchSession(b, 2000), 2000)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buildTOCReport(session, 0, 50)
	}
}
