package posthorse

// Twins of test/archive.test.ts (the ledger file "archive").
//
// Case 0 ("beyond V8's string limit"): the original proves that a 513 MiB record, which cannot be
// one JavaScript string, is never decoded when it does not match. A Go string has no such limit,
// so the observable intent is kept: the search and the later reads return the same results, and
// the live heap stays far below the record's size (heapSampler), which fails if a line is ever
// materialized whole. The prefilter and the entry-head shortcut are what keep it out of memory;
// see port/lane-HistoryLane.md.
//
// Case 1 aborts with an already cancelled context passed to historyMatches (the fake host cannot
// stage a cancel frame before the handler starts); the reason must come back unchanged.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// heapSampler records the peak live heap while a test runs.
type heapSampler struct {
	stop chan struct{}
	done sync.WaitGroup
	peak uint64
}

func startHeapSampler() *heapSampler {
	s := &heapSampler{stop: make(chan struct{})}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		var stats runtime.MemStats
		for {
			runtime.ReadMemStats(&stats)
			s.peak = max(s.peak, stats.HeapInuse)
			select {
			case <-s.stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return s
}

func (s *heapSampler) finish() uint64 {
	close(s.stop)
	s.done.Wait()
	return s.peak
}

func archiveHeader(dir string) string {
	return fxLine(rec{"type": "session", "version": float64(3), "id": "synthetic", "timestamp": "2026-10-24T00:00:00Z", "cwd": dir}) + "\n"
}

func archiveMessage(id string, content any, parent any) string {
	return fxLine(rec{"type": "message", "id": id, "parentId": parent, "message": rec{"role": "user", "content": content}})
}

func archiveFixture(t *testing.T, dir string) *fixture {
	t.Helper()
	return newFixture(t, withCwd(dir), withSessionDir(dir), withSessionFile(filepath.Join(dir, "synthetic.jsonl")))
}

func TestTwinArchive(t *testing.T) {
	tw(t, "archive", "archive search and later reads pass a nonmatching image beyond V8's string limit", func(t *testing.T) {
		dir := t.TempDir()
		file, err := os.OpenFile(filepath.Join(dir, "synthetic.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		write := func(data string) {
			if _, err := file.WriteString(data); err != nil {
				t.Fatal(err)
			}
		}
		write(archiveHeader(dir))
		write(fxLine(rec{"type": "context_window", "id": "window", "parentId": nil, "handoff": "new window"}) + "\n")
		write(`{"type":"message","id":"large001","parentId":"window","message":{"role":"toolResult","toolCallId":"fake","toolName":"read","content":[{"type":"image","mimeType":"image/png","data":"`)
		chunk := strings.Repeat("A", 1024*1024)
		for range 513 {
			write(chunk)
		}
		write(`"}]}}` + "\n")
		write(archiveMessage("older", "small marker older", "large001") + "\n")
		write(archiveMessage("newer", []any{fxText("small marker newer"), fxImage("AA==", "image/png")}, "older") + "\n")
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		f := archiveFixture(t, dir)
		sampler := startHeapSampler()
		first := f.Run("history", rec{"op": "search", "all": true, "query": "small marker", "limit": 1}).Text()
		hlMustMatch(t, `\[window window\] \[newer@[^\]]+\] \[user\] small marker newer`, first)
		cursor := regexp.MustCompile(`continue with cursor "([^"]+)"`).FindStringSubmatch(first)
		if cursor == nil {
			t.Fatalf("no cursor in\n%s", first)
		}
		next := f.Run("history", rec{"op": "search", "all": true, "query": "small marker", "limit": 1, "cursor": cursor[1]}).Text()
		hlMustMatch(t, `\[window window\] \[older@[^\]]+\] \[user\] small marker older`, next)
		hlMustNotMatch(t, `continue with cursor`, next)
		// A nonfinal record also exercises early archive iterator cleanup before the next read.
		olderID := regexp.MustCompile(`\[(older@[^\]]+)\]`).FindStringSubmatch(next)
		if olderID == nil {
			t.Fatal("no older id")
		}
		hlMustMatch(t, `\[window window\].*\[user\] small marker older`, f.Run("history", rec{"op": "read", "id": olderID[1]}).Text())
		newerID := regexp.MustCompile(`\[(newer@[^\]]+)\]`).FindStringSubmatch(first)
		if newerID == nil {
			t.Fatal("no newer id")
		}
		read := f.Run("history", rec{"op": "read", "id": newerID[1]})
		hlMustMatch(t, `\[user\] small marker newer`, read.Text())
		hlEqual(t, read.Images(), []rec{fxImage("AA==", "image/png")}, "newer images")
		if peak := sampler.finish(); peak > 256<<20 {
			t.Errorf("peak live heap %d MiB: the 513 MiB record must never be held in memory", peak>>20)
		}
	})

	tw(t, "archive", "archive prefilter keeps boundary-spanning text and Unicode folds", func(t *testing.T) {
		dir := t.TempDir()
		file, err := os.OpenFile(filepath.Join(dir, "synthetic.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		position := 0
		write := func(data string) {
			n, err := file.WriteString(data)
			if err != nil {
				t.Fatal(err)
			}
			position += n
		}
		cases := [][3]string{
			{"ascii", "small marker", "SMALL MARKER"},
			{"unicode", "éclair 🐴", "ÉCLAIR 🐴"},
			{"astral", "🐴 beacon", "🐴 BEACON"},
			{"kelvin", "kelvin", "\u212aELVIN"},
			{"longs", "\u017fail", "\u017fAIL"},
			{"dotted", "i\u0307stanbul", "\u0130STANBUL"},
			{"escaped", `say "needle"`, `say "needle"`},
		}
		write(archiveHeader(dir) + "malformed line\n")
		const mib = 1024 * 1024
		for _, c := range cases {
			id, value := c[0], c[2]
			write(`{"type":"message","id":"` + id + `","parentId":null,"message":{"role":"user","content":"`)
			// The value starts one byte before a 1 MiB read boundary.
			write(strings.Repeat(" ", (mib-1-position%mib+mib)%mib))
			encoded := fxStringify(value)
			tail := encoded[1:len(encoded)-1] + `"}}`
			if id != "escaped" {
				tail += "\n"
			}
			write(tail)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		f := archiveFixture(t, dir)
		for _, c := range cases {
			// Each query reopens the same generated archive; avoid concurrent scans of this fixture.
			output := f.Run("history", rec{"op": "search", "all": true, "query": c[1]}).Text()
			hlMustMatch(t, `\[`+c[0]+`@[^\]]+\]`, output)
		}
		reason := errors.New("cancel archive scan")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(reason)
		source := hlSource(t, dir, filepath.Join(dir, "synthetic.jsonl"))
		source.cwd = absPath(dir)
		_, err = historyMatches(ctx, source,
			historyParams{op: "search", query: "small marker", hasQuery: true, all: true},
			func(message string) error { return errors.New(message) })
		if !errors.Is(err, reason) {
			t.Errorf("aborted archive scan returned %v, want the abort reason", err)
		}
	})
}
