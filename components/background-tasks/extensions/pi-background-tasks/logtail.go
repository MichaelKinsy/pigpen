package pi_background_tasks

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"sync"
)

// Asynchronous log tails owned by the task that consumes them. upstream: extensions/log-tail.ts.

// outputTail is a log excerpt and whether it omits earlier output.
type outputTail struct {
	text      string
	truncated bool
}

// readableFile is the part of an open log file the reader uses; a test replaces the opener to inject short reads and held closes.
type readableFile interface {
	Stat() (os.FileInfo, error)
	ReadAt(p []byte, off int64) (int, error)
	Close() error
}

type tailCache struct {
	logFile  string
	maxChars int
	size     int64
	mtimeNs  int64
	ctimeNs  int64
	ino      uint64
	tail     outputTail
}

type tailState struct {
	tail    *tailCache
	pending *future
}

// taskOutputReader reads the end of a task's log, at most probeConcurrency reads at once across every task, and keeps the
// last tail of each task until its log changes.
type taskOutputReader struct {
	report    func(logFile, err string)
	isCurrent func(*task) bool
	// inMemory is the task's in-memory output (guarded by the runtime's lock); a task that holds some is read from it.
	inMemory func(*task) string
	// logFile and the other task reads go through these so the reader never touches task fields unguarded.
	logFile func(*task) string

	// open opens a log file; os.Open unless a test replaces it.
	open func(path string) (readableFile, error)

	mu     sync.Mutex
	tails  map[*task]*tailState
	active int
	queued []chan struct{}
}

func newTaskOutputReader(report func(logFile, err string), isCurrent func(*task) bool, inMemory func(*task) string, logFile func(*task) string) *taskOutputReader {
	if isCurrent == nil {
		isCurrent = func(*task) bool { return true }
	}
	return &taskOutputReader{report: report, isCurrent: isCurrent, inMemory: inMemory, logFile: logFile, tails: map[*task]*tailState{},
		open: func(path string) (readableFile, error) { return os.Open(path) }}
}

// read is the text of readTail.
func (r *taskOutputReader) read(t *task, maxChars float64) string {
	return r.readTail(t, maxChars).text
}

// readTail keeps the disk omission information for consumers that format a bounded excerpt.
func (r *taskOutputReader) readTail(t *task, maxChars float64) outputTail {
	for {
		if out := r.inMemory(t); len(out) > 0 {
			return outputTail{text: out}
		}
		r.mu.Lock()
		state := r.tails[t]
		if state == nil {
			state = &tailState{}
			r.tails[t] = state
		}
		if state.pending != nil {
			pending := state.pending
			r.mu.Unlock()
			pending.wait()
			r.mu.Lock()
			stale := r.tails[t] != state
			r.mu.Unlock()
			if stale || !r.isCurrent(t) {
				return outputTail{}
			}
			// A concurrent caller can ask for a different tail limit.
			continue
		}
		owned := state
		done := newFuture()
		owned.pending = done
		r.mu.Unlock()
		out := r.load(t, owned, maxChars)
		r.mu.Lock()
		owned.pending = nil
		r.mu.Unlock()
		done.resolve()
		return out
	}
}

func (r *taskOutputReader) load(t *task, owned *tailState, maxChars float64) outputTail {
	lengthLimit := max(1, jsToInt(math.Floor(maxChars)))
	// Admission: wait while the shared limit is in use, and read only while the request is still current.
	for {
		current := r.isCurrent(t)
		r.mu.Lock()
		if r.tails[t] != owned || !current {
			r.mu.Unlock()
			return outputTail{}
		}
		if r.active < probeConcurrency {
			r.active++
			r.mu.Unlock()
			break
		}
		wake := make(chan struct{})
		r.queued = append(r.queued, wake)
		r.mu.Unlock()
		<-wake
	}
	logFile := r.logFile(t)
	var f readableFile
	defer func() {
		// The descriptor closes before the admission is released.
		if f != nil {
			_ = f.Close()
		}
		r.mu.Lock()
		r.active--
		var wake chan struct{}
		if len(r.queued) > 0 {
			wake = r.queued[0]
			r.queued = r.queued[1:]
		}
		r.mu.Unlock()
		if wake != nil {
			close(wake)
		}
	}()
	tail, err := r.readFile(&f, logFile, lengthLimit, owned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return outputTail{}
		}
		reason := nodeReadErrorMessage(err)
		r.report(logFile, reason)
		return outputTail{text: "[log unreadable: " + reason + "]"}
	}
	return tail
}

func (r *taskOutputReader) readFile(fp *readableFile, logFile string, lengthLimit int, owned *tailState) (outputTail, error) {
	f, err := r.open(logFile)
	if err != nil {
		return outputTail{}, err
	}
	*fp = f
	info, err := f.Stat()
	if err != nil {
		return outputTail{}, err
	}
	size := info.Size()
	ino, ctime := fileIdentity(info)
	mtime := info.ModTime().UnixNano()
	r.mu.Lock()
	cached := owned.tail
	r.mu.Unlock()
	if cached != nil && cached.logFile == logFile && cached.maxChars == lengthLimit && cached.size == size && cached.mtimeNs == mtime && cached.ctimeNs == ctime && cached.ino == ino {
		return cached.tail, nil
	}
	// UTF-8 needs at most three bytes per UTF-16 code unit, plus one when the suffix starts with the second half of a pair.
	length := int64(min(size, int64(3*lengthLimit+1)))
	buf := make([]byte, length)
	var read int64
	for read < length {
		n, err := f.ReadAt(buf[read:], size-length+read)
		read += int64(n)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return outputTail{}, err
		}
	}
	// Skip only a cut UTF-8 prefix, not a literal replacement character.
	start := int64(0)
	if size > length {
		for start < read && buf[start]&0xc0 == 0x80 {
			start++
		}
	}
	decoded := decodeUTF8(buf[start:read])
	text := jsTail(decoded, lengthLimit)
	tail := outputTail{text: text, truncated: size > read || jsLen(decoded) > lengthLimit}
	r.mu.Lock()
	owned.tail = &tailCache{logFile: logFile, maxChars: lengthLimit, size: size, mtimeNs: mtime, ctimeNs: ctime, ino: ino, tail: tail}
	r.mu.Unlock()
	return tail, nil
}

// forget drops a task's cached tail.
func (r *taskOutputReader) forget(t *task) {
	r.mu.Lock()
	delete(r.tails, t)
	r.mu.Unlock()
}

// clear drops every cached tail; reads waiting for admission wake and find themselves stale. Open descriptors keep their
// capacity until they close, even across sessions.
func (r *taskOutputReader) clear() {
	r.mu.Lock()
	r.tails = map[*task]*tailState{}
	queued := r.queued
	r.queued = nil
	r.mu.Unlock()
	for _, w := range queued {
		close(w)
	}
}

// nodeReadErrorMessage is the text Node gives a read failure.
func nodeReadErrorMessage(err error) string { return nodeErrorMessage(err) }
