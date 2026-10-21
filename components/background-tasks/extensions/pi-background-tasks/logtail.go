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

// admission is one read's place in the shared limit: a slot taken at once or a position in the queue. A queued read is handed
// the slot of the read that finishes (granted), or woken without one when the reader is cleared.
type admission struct {
	wake    chan struct{}
	granted bool
}

// tailRead is a read that has been started; wait is its result.
type tailRead struct {
	done chan struct{}
	out  outputTail
}

func (p *tailRead) wait() outputTail {
	<-p.done
	return p.out
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
	queued []*admission
	// joins counts the reads that found another read of their task in flight and waited for it.
	joins int
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
	return r.start(t, maxChars, r.inMemory(t)).wait()
}

// start begins a read and returns at once. Which read owns a task's tail and where a read stands in the admission queue are
// decided here, in call order, as the original's promises decide them in one tick: a caller that holds the runtime's lock
// starts its reads in the order the lock was taken, and a read that starts later cannot overtake one that started before it.
// inMemory is the task's in-memory output as the caller saw it; start never takes the runtime's lock, so the caller may hold it.
func (r *taskOutputReader) start(t *task, maxChars float64, inMemory string) *tailRead {
	op := &tailRead{done: make(chan struct{})}
	if len(inMemory) > 0 {
		op.out = outputTail{text: inMemory}
		close(op.done)
		return op
	}
	r.mu.Lock()
	state := r.tails[t]
	if state == nil {
		state = &tailState{}
		r.tails[t] = state
	}
	if state.pending != nil {
		pending := state.pending
		r.joins++
		r.mu.Unlock()
		go func() {
			pending.wait()
			r.mu.Lock()
			stale := r.tails[t] != state
			r.mu.Unlock()
			if stale || !r.isCurrent(t) {
				op.out = outputTail{}
			} else {
				// A concurrent caller can ask for a different tail limit.
				op.out = r.readTail(t, maxChars)
			}
			close(op.done)
		}()
		return op
	}
	owned := state
	done := newFuture()
	owned.pending = done
	slot := r.admitLocked()
	r.mu.Unlock()
	go func() {
		op.out = r.load(t, owned, maxChars, slot)
		r.mu.Lock()
		owned.pending = nil
		r.mu.Unlock()
		done.resolve()
		close(op.done)
	}()
	return op
}

// admitLocked takes a slot of the shared limit or a place in its queue.
func (r *taskOutputReader) admitLocked() *admission {
	slot := &admission{wake: make(chan struct{})}
	if r.active < probeConcurrency {
		r.active++
		slot.granted = true
		close(slot.wake)
	} else {
		r.queued = append(r.queued, slot)
	}
	return slot
}

// release gives the slot to the first queued read, or frees it.
func (r *taskOutputReader) release() {
	r.mu.Lock()
	var next *admission
	if len(r.queued) > 0 {
		next = r.queued[0]
		r.queued = r.queued[1:]
		next.granted = true
	} else {
		r.active--
	}
	r.mu.Unlock()
	if next != nil {
		close(next.wake)
	}
}

func (r *taskOutputReader) load(t *task, owned *tailState, maxChars float64, slot *admission) outputTail {
	lengthLimit := max(1, jsToInt(math.Floor(maxChars)))
	// Admission: wait for the slot, and read only while the request is still current.
	<-slot.wake
	r.mu.Lock()
	granted := slot.granted
	r.mu.Unlock()
	if !granted {
		// The reader was cleared while this read queued: it holds no slot.
		return outputTail{}
	}
	current := r.isCurrent(t)
	logFile := r.logFile(t)
	var f readableFile
	defer func() {
		// The descriptor closes before the admission is released.
		if f != nil {
			_ = f.Close()
		}
		r.release()
	}()
	r.mu.Lock()
	stale := r.tails[t] != owned || !current
	r.mu.Unlock()
	if stale {
		return outputTail{}
	}
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
		close(w.wake)
	}
}

// nodeReadErrorMessage is the text Node gives a read failure.
func nodeReadErrorMessage(err error) string { return nodeErrorMessage(err) }
