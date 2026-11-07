package posthorse

// Benchmarks of the history lane: archive search (ripgrep prefilter and in-process scan),
// branch search, paging, and the line matcher. The archive is synthetic; scale it with
// POSTHORSE_BENCH_LINES (default 200000 entries, about 55 MB).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func benchLines() int {
	if v, err := strconv.Atoi(os.Getenv("POSTHORSE_BENCH_LINES")); err == nil && v > 0 {
		return v
	}
	return 200_000
}

// benchArchive writes one session archive of n entries in Pi's key order; every 1000th entry
// carries the rare marker, every entry the word "entry". It returns the directory and file size.
func benchArchive(b *testing.B, n int) (string, int64) {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.jsonl")
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	// Streamed, so the generator's own memory does not show in the peak RSS of the search.
	out := bufio.NewWriterSize(file, 1<<20)
	fmt.Fprintf(out, `{"type":"session","version":3,"id":"bench","timestamp":"2026-11-06T00:00:00Z","cwd":%q}`+"\n", dir)
	parent := "null"
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 6)
	for i := range n {
		id := fmt.Sprintf("e%07d", i)
		marker := ""
		if i%1000 == 0 {
			marker = " rare-needle-marker"
		}
		fmt.Fprintf(out, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-11-06T00:00:%02d.000Z","message":{"role":"user","content":"entry %d %s%s"}}`+"\n",
			id, parent, i%60, i, filler, marker)
		parent = strconv.Quote(id)
	}
	if err := out.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	return dir, info.Size()
}

func benchSource(dir string) historySource {
	return historySource{
		dir:        dir,
		cwd:        absPath(dir),
		branch:     func() ([]entryLike, error) { return nil, nil },
		projection: func() ([]projectedEntry, error) { return nil, nil },
	}
}

func benchFail(message string) error { return errors.New(message) }

// reportPeakRSS adds the process peak resident set (Linux VmHWM) as a metric.
func reportPeakRSS(b *testing.B) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			fields := strings.Fields(rest)
			if kb, err := strconv.ParseFloat(fields[0], 64); err == nil {
				b.ReportMetric(kb/1024, "peakRSS-MiB")
			}
		}
	}
}

func BenchmarkSearchArchive(b *testing.B) {
	dir, size := benchArchive(b, benchLines())
	source := benchSource(dir)
	for _, mode := range []string{"rg", "noRg"} {
		for _, query := range []string{"rare-needle-marker", "entry 1999"} {
			b.Run(mode+"/"+strings.ReplaceAll(query, " ", "_"), func(b *testing.B) {
				if mode == "noRg" {
					b.Setenv("PATH", "")
				}
				params := historyParams{op: "search", query: query, hasQuery: true, all: true, limit: hlInt(10)}
				b.SetBytes(size)
				b.ReportAllocs()
				for b.Loop() {
					matches, err := historyMatches(context.Background(), source, params, benchFail)
					if err != nil {
						b.Fatal(err)
					}
					if len(matches.hits) == 0 {
						b.Fatal("no hits")
					}
				}
				reportPeakRSS(b)
			})
		}
	}
}

// BenchmarkSearchArchiveEveryLine matches every entry: each line is parsed and ranked while
// only the newest limit+2 hits are kept.
func BenchmarkSearchArchiveEveryLine(b *testing.B) {
	dir, size := benchArchive(b, benchLines())
	source := benchSource(dir)
	b.Setenv("PATH", "")
	params := historyParams{op: "search", query: "lorem", hasQuery: true, all: true, limit: hlInt(10)}
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := historyMatches(context.Background(), source, params, benchFail); err != nil {
			b.Fatal(err)
		}
	}
	reportPeakRSS(b)
}

func BenchmarkFindEntry(b *testing.B) {
	dir, size := benchArchive(b, benchLines())
	source := benchSource(dir)
	b.Setenv("PATH", "")
	id := fmt.Sprintf("e%07d", benchLines()-1)
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := findEntry(context.Background(), source, id); err != nil {
			b.Fatal(err)
		}
	}
	reportPeakRSS(b)
}

func BenchmarkSearchBranch(b *testing.B) {
	const n = 20_000
	branch := make([]entryLike, n)
	parent := any(nil)
	for i := range branch {
		id := fmt.Sprintf("b%06d", i)
		branch[i] = rec{
			"type": "message", "id": id, "parentId": parent, "timestamp": "t",
			"message": rec{"role": "user", "content": fmt.Sprintf("Branch entry %d with some Mixed Case text and a needle %d", i, i%97)},
		}
		parent = id
	}
	source := historySource{
		branch:     func() ([]entryLike, error) { return branch, nil },
		projection: func() ([]projectedEntry, error) { return nil, nil },
	}
	params := historyParams{op: "search", query: "needle 3", hasQuery: true, limit: hlInt(10)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := historyMatches(context.Background(), source, params, benchFail); err != nil {
			b.Fatal(err)
		}
	}
}

// benchPages sizes every page at a fixed number of characters and keeps no reservations.
type benchPages struct{ chars int }

func (p benchPages) size(policyContext, pageRequest) (int, error) { return p.chars, nil }
func (p benchPages) result(text string, images []imageLike, display *posthorseDisplay) toolResult {
	return textResult(text, images, display)
}
func (p benchPages) error(_ policyContext, message string, _ string) error {
	return errors.New(message)
}
func (p benchPages) notes(policyContext, notesPage) (toolResult, error) {
	return toolResult{}, errors.New("unused")
}

// BenchmarkReadPage pages one 400k-character entry in 40k-character pages.
func BenchmarkReadPage(b *testing.B) {
	text := strings.Repeat("0123456789abcdef", 25_000)
	item := windowedEntry{
		entry:    rec{"id": "big", "timestamp": "t"},
		windowId: "initial", text: text,
	}
	pages := benchPages{chars: 40_000}
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		for offset := 0; offset < len(text); offset += 40_000 {
			off := float64(offset)
			if _, err := historyReadPage(item, historyParams{op: "read", id: "big", offset: &off}, nil, pages, ""); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkSearchPage renders a 50-hit page and its continuation cursors.
func BenchmarkSearchPage(b *testing.B) {
	hits := make([]historyHit, 50)
	for i := range hits {
		hits[i] = historyHit{id: fmt.Sprintf("hit%03d@AbCdEfGhIj", i), text: strings.Repeat("x", 300), headerLength: 40}
	}
	matches := historyMatchSet{hits: hits, key: "AbCdEfGhIj", limit: 50}
	params := historyParams{op: "search", query: "x", hasQuery: true, limit: hlInt(50)}
	pages := benchPages{chars: 40_000}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := searchPage(matches, params, nil, pages); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLineMatcher is the throughput of the raw-line prefilter over a 1 MiB window.
func BenchmarkLineMatcher(b *testing.B) {
	window := strings.Repeat(`{"type":"message","id":"e0000001","parentId":null,"message":{"role":"user","content":"lorem ipsum dolor sit amet"}}`, 9000)
	for _, query := range []string{"rare-needle-marker", "dolor sit", "sk"} {
		needles, _ := prefilterNeedles(query)
		matcher := needleLines(needles)
		b.Run(query, func(b *testing.B) {
			b.SetBytes(int64(len(window)))
			b.ReportAllocs()
			for b.Loop() {
				matcher.test(window)
			}
		})
	}
	needles, _ := prefilterNeedles("éclair 🐴")
	matcher := needleLines(needles)
	b.Run("unicode", func(b *testing.B) {
		b.SetBytes(int64(len(window)))
		b.ReportAllocs()
		for b.Loop() {
			matcher.test(window)
		}
	})
}
