package powerline_footer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Latency and allocations of what /cd, /reply and the last-prompt line do on a representative input.

func benchDir(b *testing.B, n int) string {
	b.Helper()
	dir := b.TempDir()
	for i := range n {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("project-%03d", i)), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

func BenchmarkResolveCdTarget(b *testing.B) {
	dir := benchDir(b, 10)
	b.ReportAllocs()
	for b.Loop() {
		resolveCdTarget("project-003", dir, dir)
	}
}

// 200 directories: the completion keeps the first 50 that match.
func BenchmarkCdArgumentCompletions(b *testing.B) {
	dir := benchDir(b, 200)
	b.ReportAllocs()
	for b.Loop() {
		getCdArgumentCompletions("project-1", dir, dir)
	}
}

// a 2000-entry session of about 1 MB: the fork reads it, parses every line and writes it again.
func benchSession(b *testing.B, entries int) string {
	b.Helper()
	var sb strings.Builder
	sb.WriteString(`{"type":"session","version":3,"id":"0192f5a0-0000-7000-8000-000000000001","timestamp":"2026-10-20T10:00:00.000Z","cwd":"/x"}` + "\n")
	parent := "null"
	for i := range entries {
		id := fmt.Sprintf("%08x", i+1)
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		fmt.Fprintf(&sb, `{"type":"message","id":"%s","parentId":%s,"timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"%s","content":[{"type":"text","text":%q}],"timestamp":1792490401000}}`+"\n",
			id, parent, role, strings.Repeat("some message text ", 28))
		parent = `"` + id + `"`
	}
	path := filepath.Join(b.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

func BenchmarkForkSessionFrom(b *testing.B) {
	source := benchSession(b, 2000)
	root := b.TempDir()
	info, _ := os.Stat(source)
	b.SetBytes(info.Size())
	b.ReportAllocs()
	now := time.Now()
	for b.Loop() {
		path, err := forkSessionFrom(source, "/target/dir", root, now)
		if err != nil {
			b.Fatal(err)
		}
		_ = os.Remove(path)
	}
}

func benchBranch(n int) []map[string]any {
	var branch []map[string]any
	for i := range n {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		branch = append(branch, messageEntry(fmt.Sprintf("%08x", i+1), "2026-10-20T10:00:01.000Z", role, textBlocks(strings.Repeat("some message text ", 28))))
	}
	return branch
}

// the candidates of a 2000-entry branch (the newest 200 are kept) and the quote of the last one.
func BenchmarkReplyCandidatesAndQuote(b *testing.B) {
	ctx := &fakeQuoteCtx{branch: benchBranch(2000)}
	b.ReportAllocs()
	for b.Loop() {
		candidates, _ := collectCandidates(ctx)
		formatQuote(candidates[0])
	}
}

func BenchmarkFuzzyFilter200(b *testing.B) {
	ctx := &fakeQuoteCtx{branch: benchBranch(2000)}
	candidates, _ := collectCandidates(ctx)
	b.ReportAllocs()
	for b.Loop() {
		filterCandidates(candidates, "assistant 00000a")
	}
}

func BenchmarkPickerRender(b *testing.B) {
	ctx := &fakeQuoteCtx{branch: benchBranch(2000)}
	candidates, _ := collectCandidates(ctx)
	p := newQuotePicker(candidates, pickerPlainTheme{}, time.Now, nil, nil)
	b.ReportAllocs()
	for b.Loop() {
		p.Render(120)
	}
}

func BenchmarkLastPromptRender(b *testing.B) {
	s := newLastPromptState()
	s.prompt = strings.Repeat("fix the failing test in the parser ", 20)
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		s.render(120)
		for b.Loop() {
			s.render(120)
		}
	})
	b.Run("resized", func(b *testing.B) {
		b.ReportAllocs()
		w := 100
		for b.Loop() {
			w++
			s.render(w)
		}
	})
}
