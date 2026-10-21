package pi_background_tasks

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the original's log writer, log tail, coalescing, matcher, acknowledgement and activity tests.

type logStep struct {
	op    string // append, fire, settle, stall, drain, flush
	file  string
	text  string
	idx   int
	fails bool
}

type logObserved struct {
	Writes      [][2]string
	Diagnostics [][2]string
	TimerArmed  bool
	StallsArmed int
	Held        int
	Drained     string
}

func TestTwinLogWriter(t *testing.T) {
	writeNow := repeat("w", logWriteNowBytes)
	atCap := repeat("c", logMaxPendingByte)
	writeFailed := func(n int) string { return failedLogMarker(n, "fixture append failure") }
	failedDiag := [2]string{"task log append failed", "a.log"}
	stalledDiag := [2]string{"task log write stalled", "a.log"}
	ap := func(file, text string) logStep { return logStep{op: "append", file: file, text: text} }
	fire := logStep{op: "fire"}
	settle := func(i int) logStep { return logStep{op: "settle", idx: i} }
	settleFail := func(i int) logStep { return logStep{op: "settle", idx: i, fails: true} }
	stall := func(i int) logStep { return logStep{op: "stall", idx: i} }
	drain := logStep{op: "drain"}
	flush := func(f string) logStep { return logStep{op: "flush", file: f} }
	w := func(file, text string) [2]string { return [2]string{file, text} }

	rows := []struct {
		name  string
		steps []logStep
		want  logObserved
	}{
		{"appends inside one window become one write per file", []logStep{ap("a.log", "one\n"), ap("b.log", "x\n"), ap("a.log", "two\n"), fire},
			logObserved{Writes: [][2]string{w("a.log", "one\ntwo\n"), w("b.log", "x\n")}, StallsArmed: 2}},
		{"nothing is written before the window ends", []logStep{ap("a.log", "one\n")}, logObserved{TimerArmed: true}},
		{"text appended during a write waits for it and for the next window", []logStep{ap("a.log", "one\n"), fire, ap("a.log", "two\n"), fire, settle(0), fire},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", "two\n")}, StallsArmed: 1}},
		{"pending text at the write-now size is written without the window", []logStep{ap("a.log", "one\n"), ap("a.log", writeNow)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"+writeNow)}, TimerArmed: true, StallsArmed: 1}},
		{"pending text at the write-now size waits for the write in flight", []logStep{ap("a.log", "one\n"), fire, ap("a.log", writeNow)},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, TimerArmed: true, StallsArmed: 1}},
		{"pending text at the cap behind a write in flight holds the producer and keeps every byte", []logStep{ap("a.log", "one\n"), fire, ap("a.log", atCap), ap("a.log", "more\n")},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, TimerArmed: true, StallsArmed: 1, Held: 2}},
		{"the end of the write in flight writes held text at once and releases the producer", []logStep{ap("a.log", "one\n"), fire, ap("a.log", atCap), ap("a.log", "more\n"), settle(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", atCap+"more\n")}, TimerArmed: true, StallsArmed: 1}},
		{"flush writes one file's pending text without the window and waits for it", []logStep{ap("a.log", "one\n"), ap("b.log", "x\n"), flush("a.log"), settle(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, TimerArmed: true, Drained: "done"}},
		{"flush waits for the write in flight", []logStep{ap("a.log", "one\n"), fire, flush("a.log")},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, StallsArmed: 1, Drained: "waiting"}},
		{"flush of a file with nothing pending or in flight returns null", []logStep{ap("a.log", "one\n"), fire, settle(0), flush("a.log")},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, Drained: "nothing"}},
		{"a stalled write frees a waiting flush and the producer's hold, and reports the file once", []logStep{ap("a.log", "one\n"), fire, ap("a.log", atCap), flush("a.log"), stall(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, Diagnostics: [][2]string{stalledDiag}, TimerArmed: true, Drained: "done"}},
		{"during a stall text past the cap is counted, and its marker follows the kept text once the write settles", []logStep{ap("a.log", "one\n"), fire, ap("a.log", atCap), stall(0), ap("a.log", "lost\n"), settle(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", atCap+stalledLogMarker(5))}, Diagnostics: [][2]string{stalledDiag}, TimerArmed: true, StallsArmed: 1}},
		{"a write chained behind a stalled write starts unstalled, so text at the cap holds the producer and is kept", []logStep{
			ap("a.log", "one\n"), flush("a.log"), ap("a.log", "two\n"), flush("a.log"), stall(0), settle(0), ap("a.log", atCap), ap("a.log", "more\n"), settle(1)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", "two\n"), w("a.log", atCap+"more\n")}, Diagnostics: [][2]string{stalledDiag}, TimerArmed: true, StallsArmed: 1, Drained: "done"}},
		{"a failed batch that carries a stall marker counts the counted bytes in its failure marker", []logStep{
			ap("a.log", "one\n"), fire, ap("a.log", atCap), stall(0), ap("a.log", "lost\n"), settle(0), settleFail(1), ap("a.log", "two\n"), fire},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", atCap+stalledLogMarker(5)), w("a.log", writeFailed(len(atCap)+5)+"two\n")},
				Diagnostics: [][2]string{stalledDiag, failedDiag}, StallsArmed: 1}},
		{"flush of a stalled file takes no batch and resolves at once, so text past the cap stays counted", []logStep{
			ap("a.log", "one\n"), fire, ap("a.log", atCap), stall(0), flush("a.log"), ap("a.log", "lost\n"), settle(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", atCap+stalledLogMarker(5))}, Diagnostics: [][2]string{stalledDiag}, TimerArmed: true, StallsArmed: 1, Drained: "done"}},
		{"drain does not wait for a stalled write", []logStep{ap("a.log", "one\n"), ap("b.log", "x\n"), drain, settle(1), stall(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("b.log", "x\n")}, Diagnostics: [][2]string{stalledDiag}, Drained: "done"}},
		{"a failed write is reported and its marker leads the next batch", []logStep{ap("a.log", "one\n"), fire, settleFail(0), ap("a.log", "two\n"), fire},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", writeFailed(4)+"two\n")}, Diagnostics: [][2]string{failedDiag}, StallsArmed: 1}},
		{"a failure marker alone arms no retry", []logStep{ap("a.log", "one\n"), fire, settleFail(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, Diagnostics: [][2]string{failedDiag}}},
		{"a failure marker alone is written by drain", []logStep{ap("a.log", "one\n"), fire, settleFail(0), drain, settle(1)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", writeFailed(4))}, Diagnostics: [][2]string{failedDiag}, Drained: "done"}},
		{"drain writes pending text without the window and waits for every write", []logStep{ap("a.log", "one\n"), fire, ap("a.log", "two\n"), drain, settle(0), settle(1)},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", "two\n")}, Drained: "done"}},
		{"a drain batch waits for the write in flight", []logStep{ap("a.log", "one\n"), fire, ap("a.log", "two\n"), drain},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, StallsArmed: 1, Drained: "waiting"}},
		{"text appended while a drain batch writes waits for that write", []logStep{ap("a.log", "one\n"), fire, ap("a.log", "two\n"), drain, settle(0), ap("a.log", "three\n"), fire},
			logObserved{Writes: [][2]string{w("a.log", "one\n"), w("a.log", "two\n")}, StallsArmed: 1, Drained: "waiting"}},
		{"drain waits only for text pending at the call", []logStep{ap("a.log", "one\n"), drain, ap("a.log", "two\n"), settle(0)},
			logObserved{Writes: [][2]string{w("a.log", "one\n")}, TimerArmed: true, Drained: "done"}},
		{"drain with nothing pending resolves at once", []logStep{drain}, logObserved{Drained: "done"}},
	}

	tw(t, "log-writer", "task log writer batches, bounds and drains appends", func(t *testing.T) {
		for _, row := range rows {
			var writes [][2]string
			var outcomes []func(error)
			var diagnostics [][2]string
			ft := &fakeTimers{}
			drained := ""
			held := 0
			lw := newLogWriter(logWriterDeps{
				append: func(file, text string, done func(error)) {
					writes = append(writes, [2]string{file, text})
					outcomes = append(outcomes, done)
				},
				logDiagnostic: func(m string, d logDiagnosticDetails) { diagnostics = append(diagnostics, [2]string{m, d.file}) },
				timers:        ft,
			})
			// Handles are told apart by delay: the flush window or a write's stall deadline.
			findFlush := func() int {
				for i, h := range ft.handles {
					if h.d == logFlushDelay && !h.stopped && !h.fired {
						return i
					}
				}
				return -1
			}
			var stalls []int
			track := func(f *future) {
				if f == nil {
					drained = "nothing"
					return
				}
				drained = "waiting"
				f.then(func() { drained = "done" })
			}
			for _, s := range row.steps {
				switch s.op {
				case "append":
					if hold := lw.append(s.file, s.text); hold != nil {
						held++
						hold.then(func() { held-- })
					}
				case "flush":
					track(lw.flush(s.file))
				case "fire":
					i := findFlush()
					if i < 0 {
						t.Fatalf("%s: no pending timer", row.name)
					}
					ft.fire(i)
				case "settle":
					if s.idx >= len(outcomes) {
						t.Fatalf("%s: write %d missing", row.name, s.idx)
					}
					if s.fails {
						outcomes[s.idx](errors.New("fixture append failure"))
					} else {
						outcomes[s.idx](nil)
					}
				case "stall":
					stalls = stalls[:0]
					for i, h := range ft.handles {
						if h.d == logWriteStall {
							stalls = append(stalls, i)
						}
					}
					if s.idx >= len(stalls) || !ft.activeHandle(stalls[s.idx]) {
						t.Fatalf("%s: no pending stall timer %d", row.name, s.idx)
					}
					ft.fire(stalls[s.idx])
				case "drain":
					track(lw.drain())
				}
			}
			stallsArmed := 0
			for i, h := range ft.handles {
				if h.d == logWriteStall && ft.activeHandle(i) {
					stallsArmed++
				}
			}
			got := logObserved{Writes: writes, Diagnostics: diagnostics, TimerArmed: findFlush() >= 0, StallsArmed: stallsArmed, Held: held, Drained: drained}
			want := row.want
			if want.Writes == nil {
				want.Writes = nil
			}
			eq(t, got, want, row.name)
		}
	})

	tw(t, "log-writer", "output past the pending cap inside one window reaches the log whole through the producer's holds", func(t *testing.T) {
		// A producer that waits on each hold, as a task's paused output streams do, against a disk that answers at once and a
		// window that never fires.
		chunk := repeat("y\n", 32*1024)
		chunks := (5*logMaxPendingByte + len(chunk) - 1) / len(chunk)
		var mu sync.Mutex
		var all strings.Builder
		var dones []func(error)
		largest := 0
		holds := 0
		lw := newLogWriter(logWriterDeps{
			append: func(_, text string, done func(error)) {
				mu.Lock()
				all.WriteString(text)
				largest = max(largest, len(text))
				dones = append(dones, done)
				mu.Unlock()
			},
			timers: noWindowTimers{},
		})
		// The disk answers when the producer is held (or at the end): a write is in flight while the producer keeps appending.
		settleAll := func() {
			mu.Lock()
			pending := dones
			dones = nil
			mu.Unlock()
			for _, d := range pending {
				d(nil)
			}
		}
		for range chunks {
			if hold := lw.append("a.log", chunk); hold != nil {
				holds++
				settleAll()
				hold.wait()
			}
		}
		drained := lw.drain()
		for !drained.isDone() {
			settleAll()
			time.Sleep(time.Millisecond)
		}
		eq(t, [4]any{all.Len(), all.String() == repeat(chunk, chunks), holds > 0, largest <= logMaxPendingByte+len(chunk)}, [4]any{len(chunk) * chunks, true, true, true}, "the log is whole")
	})
}

// noWindowTimers never fires: the flush window and the stall deadline stay armed.
type noWindowTimers struct{}

func (noWindowTimers) After(time.Duration, func()) timer { return nopTimer{} }
func (noWindowTimers) Every(time.Duration, func()) timer { return nopTimer{} }

// shortFile reads at most three bytes per call, as the original's fixture does, and counts reads and bytes.
type shortFile struct {
	*os.File
	c *tailCounters
}

type tailCounters struct {
	mu                          sync.Mutex
	reads, bytes, closes, opens int
	failRead                    bool
	holdClose                   bool
	heldCloses                  []chan struct{}
}

func (f shortFile) ReadAt(p []byte, off int64) (int, error) {
	f.c.mu.Lock()
	f.c.reads++
	fail := f.c.failRead
	f.c.mu.Unlock()
	if fail {
		return 0, errors.New("injected disk error")
	}
	n, err := f.File.ReadAt(p[:min(len(p), 3)], off)
	f.c.mu.Lock()
	f.c.bytes += n
	f.c.mu.Unlock()
	if err == io.EOF && n > 0 && n == min(len(p), 3) {
		err = nil
	}
	return n, err
}

func (f shortFile) Close() error {
	f.c.mu.Lock()
	hold := f.c.holdClose
	var ch chan struct{}
	if hold {
		ch = make(chan struct{})
		f.c.heldCloses = append(f.c.heldCloses, ch)
	}
	f.c.mu.Unlock()
	if hold {
		<-ch
	}
	f.c.mu.Lock()
	f.c.closes++
	f.c.mu.Unlock()
	return f.File.Close()
}

func (c *tailCounters) opener() func(string) (readableFile, error) {
	return func(path string) (readableFile, error) {
		c.mu.Lock()
		c.opens++
		c.mu.Unlock()
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		return shortFile{f, c}, nil
	}
}

func (c *tailCounters) held() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.heldCloses) }

func (c *tailCounters) releaseHeld() {
	c.mu.Lock()
	held := c.heldCloses
	c.heldCloses = nil
	c.mu.Unlock()
	for _, ch := range held {
		close(ch)
	}
}

func TestTwinLogTail(t *testing.T) {
	tw(t, "log-tail", "asynchronous per-task log tails preserve character limits, bound reads, invalidate metadata, and report errors", func(t *testing.T) {
		dir := t.TempDir()
		c := &tailCounters{}
		var errs []string
		var invalidMu sync.Mutex
		invalid := map[*task]bool{}
		reader := newTaskOutputReader(func(_ string, e string) { errs = append(errs, e) },
			func(x *task) bool { invalidMu.Lock(); defer invalidMu.Unlock(); return !invalid[x] },
			func(x *task) string { return x.output }, func(x *task) string { return x.LogFile })
		reader.open = c.opener()
		first := &task{snapshot: snapshot{LogFile: filepath.Join(dir, "first.log")}}
		second := &task{snapshot: snapshot{LogFile: filepath.Join(dir, "second.log")}}
		write := func(path, text string) {
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Commands can emit all of these UTF-8 characters, including U+FFFD itself. A cut inside a surrogate pair is a lone
		// surrogate in JavaScript; Go strings cannot hold one, so the half is dropped (the adaptation of two expectations).
		charRows := []struct {
			name, output string
			maxChars     int
			expected     string
			truncated    bool
		}{
			{"bmp", repeat("€", 10_001), 10_000, repeat("€", 10_000), true},
			{"surrogate suffix", repeat("x", 100) + "😀" + repeat("€", 9), 10, repeat("€", 9), true},
			{"single surrogate", "😀", 1, "", true},
			{"decoded clipping", repeat("😀", 10), 10, repeat("😀", 5), true},
			{"partial prefix", repeat("€", 30), 10, repeat("€", 10), true},
			{"replacement character", "\uFFFDok", 4, "\uFFFDok", false},
			{"short unicode", "€ok", 4, "€ok", false},
			{"exact unicode", repeat("€", 10), 10, repeat("€", 10), false},
		}
		for _, row := range charRows {
			write(first.LogFile, row.output)
			before := c.bytes
			tail := reader.readTail(first, float64(row.maxChars))
			eq(t, [2]any{tail.text, tail.truncated}, [2]any{row.expected, row.truncated}, "character tail: "+row.name)
			eq(t, c.bytes-before, min(len(row.output), 3*row.maxChars+1), "UTF-8 byte bound: "+row.name)
		}
		write(first.LogFile, repeat("x", 1024)+"0123456789")
		write(second.LogFile, "second tail")
		beforeASCII := c.bytes
		eq(t, reader.read(first, 10), "0123456789", "ascii tail")
		eq(t, c.bytes-beforeASCII, 31, "ascii bytes")
		eq(t, reader.read(second, 10), "econd tail", "second tail")
		readCount := c.reads
		for _, x := range []*task{first, second, first} {
			reader.read(x, 10)
		}
		eq(t, c.reads, readCount, "unchanged tasks must keep independent caches")
		write(first.LogFile, repeat("x", 1024)+"abcdefghij")
		if err := os.Chtimes(first.LogFile, time.UnixMilli(2000), time.UnixMilli(2000)); err != nil {
			t.Fatal(err)
		}
		eq(t, reader.read(first, 10), "abcdefghij", "same-size mtime change must invalidate")
		f, _ := os.OpenFile(first.LogFile, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("NEXT")
		f.Close()
		eq(t, reader.read(first, 10), "efghijNEXT", "append")
		if err := os.Truncate(first.LogFile, 2); err != nil {
			t.Fatal(err)
		}
		eq(t, reader.read(first, 10), "xx", "truncate")
		eq(t, reader.read(second, 4), "tail", "limit changes must invalidate")
		reader.clear()
		before := c.reads
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); reader.read(second, 4) }()
		}
		wg.Wait()
		eq(t, c.reads-before, 4, "concurrent reads must share the byte read, including short reads")
		write(first.LogFile, "")
		eq(t, reader.read(first, 10), "", "empty log")
		if err := os.Remove(first.LogFile); err != nil {
			t.Fatal(err)
		}
		eq(t, reader.read(first, 10), "", "missing log")
		first.output = "not flushed"
		eq(t, reader.read(first, 10), "not flushed", "in-memory output")
		first.output = ""
		write(first.LogFile, "failed")
		c.failRead = true
		mustTrue(t, strings.HasPrefix(reader.read(first, 10), "[log unreadable:"), "a failed read is reported in the text")
		eq(t, len(errs), 1, "one report")
		mustTrue(t, c.closes > 0, "closes happened")
		c.failRead = false
		c.holdClose = true
		// Exits, dashboard and tools ask the same owner for different tasks and limits.
		for _, action := range []string{"invalid", "clear"} {
			n := probeConcurrency*2 + 1
			tasks := make([]*task, n)
			for i := range tasks {
				cp := *second
				tasks[i] = &cp
			}
			beforeOpens := c.opens
			results := make([]string, n)
			var all sync.WaitGroup
			for i, x := range tasks {
				all.Add(1)
				go func() {
					defer all.Done()
					if i%2 == 1 {
						results[i] = reader.read(x, 4)
					} else {
						results[i] = reader.readTail(x, 4).text
					}
				}()
			}
			var dup string
			var dupDone sync.WaitGroup
			dupDone.Add(1)
			go func() { defer dupDone.Done(); dup = reader.read(tasks[n-1], 4) }()
			// Real file reads reach close; the fixture holds that close.
			waitUntil(t, func() bool { return c.held() >= probeConcurrency })
			eq(t, c.opens-beforeOpens, probeConcurrency, action+": shared admission must hold through close")
			var replacement string
			var replDone sync.WaitGroup
			if action == "invalid" {
				invalidMu.Lock()
				invalid[tasks[n-1]] = true
				invalidMu.Unlock()
			} else {
				reader.clear()
				cp := *second
				replDone.Add(1)
				go func() { defer replDone.Done(); replacement = reader.read(&cp, 4) }()
			}
			time.Sleep(20 * time.Millisecond)
			eq(t, c.opens-beforeOpens, probeConcurrency, action+": clear must not release active descriptors")
			c.releaseHeld()
			remaining := 1
			if action == "invalid" {
				remaining = probeConcurrency
			}
			waitUntil(t, func() bool { return c.held() >= remaining })
			eq(t, c.opens-beforeOpens, probeConcurrency+remaining, action+": stale queued reads must not open")
			// Every read that opens after a release holds its own close; keep releasing until every reader has returned.
			finished := make(chan struct{})
			go func() { all.Wait(); dupDone.Wait(); replDone.Wait(); close(finished) }()
			for open := true; open; {
				c.releaseHeld()
				select {
				case <-finished:
					open = false
				case <-time.After(2 * time.Millisecond):
				}
			}
			for i := range results {
				want := ""
				if i < probeConcurrency || (action == "invalid" && i < n-1) {
					want = "tail"
				}
				eq(t, results[i], want, action+": result "+itoa(i))
			}
			eq(t, dup, "", action+": stale pending deduplication must not restart acquisition")
			if action == "clear" {
				eq(t, replacement, "tail", "new session waits for old close")
			}
		}
	})

	tskip(t, "log-tail", "must-fail: character, cache and shared acquisition defects fail the log reader suite",
		"the seven source patches are the mutations logtail-character-bound, logtail-byte-bound, logtail-task-cache, logtail-shared-admission, logtail-queued-validity, logtail-close-ownership and logtail-clear-ownership of port/mutations.json, each killed by the twin above")
}

func TestTwinCoalesce(t *testing.T) {
	tw(t, "coalesce", "coalesced call runs once per window", func(t *testing.T) {
		type obs struct {
			runs, armed, cleared int
			pending              bool
		}
		rows := []struct {
			name  string
			steps []string
			want  obs
		}{
			{"requests inside one window share one run", []string{"request", "request", "request", "fire"}, obs{1, 1, 0, false}},
			{"a request after the run arms a new window", []string{"request", "fire", "request", "fire"}, obs{2, 2, 0, false}},
			{"cancel drops the pending run", []string{"request", "cancel"}, obs{0, 1, 1, false}},
			{"cancel with nothing pending clears nothing", []string{"cancel"}, obs{0, 0, 0, false}},
			{"a request without its window elapsing stays pending", []string{"request"}, obs{0, 1, 0, true}},
		}
		for _, row := range rows {
			runs := 0
			ft := &fakeTimers{}
			call := newCoalescedCall(func() { runs++ }, 100*time.Millisecond, ft)
			for _, step := range row.steps {
				switch step {
				case "request":
					call.request()
				case "cancel":
					call.cancel()
				default:
					fired := false
					for i := range ft.handles {
						if ft.activeHandle(i) {
							ft.fire(i)
							fired = true
							break
						}
					}
					if !fired {
						t.Fatalf("%s: no pending timer", row.name)
					}
				}
			}
			pending := false
			for i := range ft.handles {
				if ft.activeHandle(i) {
					pending = true
				}
			}
			eq(t, obs{runs, len(ft.handles), len(ft.clearedIdx()), pending}, row.want, row.name)
			for _, h := range ft.handles {
				eq(t, h.d, 100*time.Millisecond, row.name+": delay")
			}
		}
	})
}
