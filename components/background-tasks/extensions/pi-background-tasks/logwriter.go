package pi_background_tasks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Task log appends, batched and written off the extension's request path.
//
// A chunk of task output joins its log file's pending text; one timer per window writes every file's pending text with one
// asynchronous append per file, and a file has at most one write in flight. A file whose pending text reaches
// logWriteNowBytes with no write in flight is written at once, without the window. A file whose pending text reaches
// logMaxPendingBytes while a write is in flight holds its producer: append returns a future that resolves when the file's
// writes settle, and the task pauses its output until then.
//
// A write in flight past logWriteStall stalls its file, and that one deadline frees every waiter: the producer's hold, flush
// and drain. Until the stalled write settles, text past the cap is counted and not kept, and a marker naming the count
// follows the text kept before it. A failed write's bytes are counted too, and a marker naming them and the error leads the
// next batch. A slow disk costs no output; a stalled or failing one costs counted bytes and never holds a task's output or
// its close.
//
// upstream: extensions/log-writer.ts.

const (
	logFlushDelay     = 250 * time.Millisecond
	logWriteNowBytes  = 1024 * 1024
	logMaxPendingByte = 4 * 1024 * 1024
	logWriteStall     = 2 * time.Second
)

func failedLogMarker(bytes int, errText string) string {
	return fmt.Sprintf("\n[log dropped %d bytes: log write failed: %s]\n", bytes, errText)
}

func stalledLogMarker(bytes int) string {
	return fmt.Sprintf("\n[log dropped %d bytes: log write stalled]\n", bytes)
}

// logDiagnosticDetails is the details a log writer diagnostic carries.
type logDiagnosticDetails struct {
	file    string
	err     string
	stallMs int
}

type logFailure struct {
	bytes int
	err   string
}

type writeChain struct {
	tail    *future
	stalled bool
}

type fileQueue struct {
	pending      []string
	pendingBytes int
	failed       *logFailure
	gapBytes     int
	writing      *writeChain
	waiters      []*future
}

type logBatch struct {
	text        string
	outputBytes int
}

// logWriterDeps are the seams of a log writer: the asynchronous append, the diagnostics sink and the timers.
type logWriterDeps struct {
	// append writes text to file and calls done with its outcome, from any goroutine.
	append        func(file, text string, done func(err error))
	logDiagnostic func(message string, d logDiagnosticDetails)
	timers        timers
}

type logWriter struct {
	deps logWriterDeps
	mu   sync.Mutex
	// queues holds the files in insertion order, as the original's Map does: a flush window and a drain visit them in that
	// order, and a file removed and added again goes last.
	queues      map[string]*fileQueue
	order       []string
	flushWindow *coalescedCall
}

func newLogWriter(deps logWriterDeps) *logWriter {
	if deps.append == nil {
		deps.append = appendFileAsync
	}
	if deps.logDiagnostic == nil {
		deps.logDiagnostic = func(message string, d logDiagnosticDetails) {
			details := map[string]any{"file": d.file}
			if d.err != "" {
				details["error"] = d.err
			}
			if d.stallMs != 0 {
				details["stallMs"] = d.stallMs
			}
			logBackgroundDiagnostic(message, details)
		}
	}
	if deps.timers == nil {
		deps.timers = realTimers{}
	}
	w := &logWriter{deps: deps, queues: map[string]*fileQueue{}}
	w.flushWindow = newCoalescedCall(func() {
		w.mu.Lock()
		var after []func()
		for _, file := range slices.Clone(w.order) {
			after = append(after, w.pumpLocked(file, w.queues[file])...)
		}
		w.mu.Unlock()
		runAll(after)
	}, logFlushDelay, deps.timers)
	return w
}

// dropQueueLocked removes a file's queue, keeping the order of the rest.
func (w *logWriter) dropQueueLocked(file string) {
	delete(w.queues, file)
	if i := slices.Index(w.order, file); i >= 0 {
		w.order = slices.Delete(w.order, i, i+1)
	}
}

func runAll(fns []func()) {
	for _, f := range fns {
		f()
	}
}

// appendFileAsync is fs/promises appendFile: the file is created when missing.
func appendFileAsync(file, text string, done func(err error)) {
	go func() {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o666)
		if err == nil {
			_, err = f.WriteString(text)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		done(err)
	}()
}

// settledOrStalledLocked is a future that resolves once the file's writes settle or one stalls.
func (w *logWriter) settledOrStalledLocked(q *fileQueue) *future {
	if q.writing == nil || q.writing.stalled {
		return resolvedFuture()
	}
	f := newFuture()
	q.waiters = append(q.waiters, f)
	return f
}

func (w *logWriter) takeBatchLocked(q *fileQueue) *logBatch {
	failed, gap, pendingBytes := q.failed, q.gapBytes, q.pendingBytes
	if len(q.pending) == 0 && failed == nil {
		return nil
	}
	var b strings.Builder
	if failed != nil {
		b.WriteString(failedLogMarker(failed.bytes, failed.err))
	}
	for _, p := range q.pending {
		b.WriteString(p)
	}
	if gap > 0 {
		b.WriteString(stalledLogMarker(gap))
	}
	q.pending = nil
	q.pendingBytes = 0
	q.failed = nil
	q.gapBytes = 0
	out := pendingBytes + gap
	if failed != nil {
		out += failed.bytes
	}
	return &logBatch{text: b.String(), outputBytes: out}
}

// startWriteLocked chains a write behind the queue's write in flight, so a file never has two. It returns the actions to run
// once the lock is released.
func (w *logWriter) startWriteLocked(file string, q *fileQueue, batch *logBatch) []func() {
	prev := q.writing
	chain := &writeChain{tail: newFuture()}
	q.writing = chain
	begin := func() { w.beginWrite(file, q, chain, batch) }
	if prev == nil {
		return []func(){begin}
	}
	// The write waits for the previous write's tail, which settles after its continuation ran.
	return []func(){func() { prev.tail.then(begin) }}
}

func (w *logWriter) beginWrite(file string, q *fileQueue, chain *writeChain, batch *logBatch) {
	var stallTimer timer
	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			w.mu.Lock()
			if stallTimer != nil {
				stallTimer.Stop()
			}
			if err != nil {
				msg := nodeErrorMessage(err)
				prior := 0
				if q.failed != nil {
					prior = q.failed.bytes
				}
				q.failed = &logFailure{bytes: prior + batch.outputBytes, err: msg}
			}
			if q.writing != nil {
				q.writing.stalled = false
			}
			// The continuation: only the newest write in the chain releases the queue.
			var after []func()
			if q.writing == chain {
				q.writing = nil
				waiters := q.waiters
				q.waiters = nil
				for _, f := range waiters {
					after = append(after, f.resolve)
				}
				switch {
				case q.pendingBytes >= logWriteNowBytes:
					after = append(after, w.pumpLocked(file, q)...)
				case len(q.pending) > 0:
					after = append(after, w.flushWindow.request)
				case q.failed == nil && w.queues[file] == q:
					w.dropQueueLocked(file)
				}
			}
			chainTail := chain.tail
			w.mu.Unlock()
			if err != nil {
				w.deps.logDiagnostic("task log append failed", logDiagnosticDetails{file: file, err: nodeErrorMessage(err)})
			}
			runAll(after)
			chainTail.resolve()
		})
	}
	w.mu.Lock()
	stallTimer = w.deps.timers.After(logWriteStall, func() {
		w.mu.Lock()
		var after []func()
		if q.writing != nil {
			q.writing.stalled = true
			waiters := q.waiters
			q.waiters = nil
			for _, f := range waiters {
				after = append(after, f.resolve)
			}
		}
		w.mu.Unlock()
		w.deps.logDiagnostic("task log write stalled", logDiagnosticDetails{file: file, stallMs: int(logWriteStall / time.Millisecond)})
		runAll(after)
	})
	w.mu.Unlock()
	w.deps.append(file, batch.text, finish)
}

// pumpLocked starts a write of the file's pending text unless one is in flight.
func (w *logWriter) pumpLocked(file string, q *fileQueue) []func() {
	if q.writing != nil {
		return nil
	}
	if len(q.pending) == 0 {
		if q.failed == nil && w.queues[file] == q {
			w.dropQueueLocked(file)
		}
		return nil
	}
	if batch := w.takeBatchLocked(q); batch != nil {
		return w.startWriteLocked(file, q, batch)
	}
	return nil
}

// writeNowLocked takes the queue's batch, failure marker included, unless its write in flight is stalled, and returns what a
// flush or drain waits for.
func (w *logWriter) writeNowLocked(file string, q *fileQueue) (*future, []func()) {
	var after []func()
	if q.writing == nil || !q.writing.stalled {
		if batch := w.takeBatchLocked(q); batch != nil {
			after = w.startWriteLocked(file, q, batch)
		}
	}
	if q.writing != nil {
		return w.settledOrStalledLocked(q), after
	}
	return nil, after
}

// append queues text for file. It returns nil while the file can take more, or a future that resolves once its writes settle
// or one stalls; the producer stops sending until then.
func (w *logWriter) append(file, text string) *future {
	if text == "" {
		return nil
	}
	w.mu.Lock()
	q := w.queues[file]
	if q == nil {
		q = &fileQueue{}
		w.queues[file] = q
		w.order = append(w.order, file)
	}
	n := len(text)
	if q.writing != nil && q.writing.stalled && q.pendingBytes >= logMaxPendingByte {
		q.gapBytes += n
		w.mu.Unlock()
		return nil
	}
	q.pending = append(q.pending, text)
	q.pendingBytes += n
	if q.writing == nil && q.pendingBytes >= logWriteNowBytes {
		after := w.pumpLocked(file, q)
		w.mu.Unlock()
		runAll(after)
		return nil
	}
	w.mu.Unlock()
	w.flushWindow.request()
	w.mu.Lock()
	defer w.mu.Unlock()
	if q.pendingBytes < logMaxPendingByte || (q.writing != nil && q.writing.stalled) {
		return nil
	}
	return w.settledOrStalledLocked(q)
}

// flush writes the file's pending text at once. It returns nil when the file has no pending text and no write in flight, so
// it already holds every appended chunk; otherwise a future that resolves once its writes settle or one stalls.
func (w *logWriter) flush(file string) *future {
	w.mu.Lock()
	q := w.queues[file]
	if q == nil {
		w.mu.Unlock()
		return nil
	}
	f, after := w.writeNowLocked(file, q)
	w.mu.Unlock()
	runAll(after)
	return f
}

// drain writes the text pending at the call and resolves once those writes settle or stall.
func (w *logWriter) drain() *future {
	w.flushWindow.cancel()
	w.mu.Lock()
	var futures []*future
	var after []func()
	for _, file := range slices.Clone(w.order) {
		q := w.queues[file]
		f, a := w.writeNowLocked(file, q)
		after = append(after, a...)
		if f != nil {
			futures = append(futures, f)
		}
	}
	w.mu.Unlock()
	runAll(after)
	all := newFuture()
	remaining := len(futures)
	if remaining == 0 {
		all.resolve()
		return all
	}
	var mu sync.Mutex
	for _, f := range futures {
		f.then(func() {
			mu.Lock()
			remaining--
			last := remaining == 0
			mu.Unlock()
			if last {
				all.resolve()
			}
		})
	}
	return all
}

// settled reports whether the file has no pending text, no write in flight (a stalled one included) and no failed write whose
// loss is not yet marked in it. False after a flush settles means its last write failed or is still stalled.
func (w *logWriter) settled(file string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, has := w.queues[file]
	return !has
}

// nodeErrorMessage is the text Node gives an fs error: `CODE: description, syscall 'path'`, which the log markers quote.
func nodeErrorMessage(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		var errno syscall.Errno
		if errors.As(pe.Err, &errno) {
			if code, desc := errnoCodeAndText(errno); code != "" {
				return fmt.Sprintf("%s: %s, %s '%s'", code, desc, pe.Op, pe.Path)
			}
		}
	}
	return err.Error()
}
