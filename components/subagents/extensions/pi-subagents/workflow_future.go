package pi_subagents

import (
	"sync"
	"sync/atomic"
	"time"
)

// A workflow run is a small single-threaded event loop (the original runs its host half on the Node main thread and the
// script in a worker thread, scripted-workflow.ts:2118). Callbacks the host hands to a workflow (launch, status, steer,
// ...) return a *WorkflowFuture instead of blocking: the loop then runs what follows a settled future in the order the
// futures were registered or settled, which is the order promise reactions run in. A callback that already knows its answer
// returns WorkflowResolved(v); one that waits runs in WorkflowAsync.

// workflowLoop runs queued functions one at a time on the goroutine that called run. It has Node's two queues: post queues a
// microtask (the continuation of a settled future), postMacro a macrotask (a message from the script, a timer, an abort). A
// macrotask starts only when no microtask is waiting, so a call from the script runs its promise chain to the end before the
// next call is looked at, as in the original (the worker's messages arrive as separate events).
type workflowLoop struct {
	mu      sync.Mutex
	cond    *sync.Cond
	micro   []func()
	macro   []func()
	closed  bool
	linger  bool         // closes by itself once nothing is waiting
	pending atomic.Int64 // futures with a continuation registered and not yet run
}

func newWorkflowLoop() *workflowLoop {
	l := &workflowLoop{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// post queues a microtask; it is safe from any goroutine and never blocks. After close it drops fn.
func (l *workflowLoop) post(fn func()) {
	l.mu.Lock()
	if !l.closed {
		l.micro = append(l.micro, fn)
		l.cond.Signal()
	}
	l.mu.Unlock()
}

// postMacro queues a macrotask.
func (l *workflowLoop) postMacro(fn func()) {
	l.mu.Lock()
	if !l.closed {
		l.macro = append(l.macro, fn)
		l.cond.Signal()
	}
	l.mu.Unlock()
}

func (l *workflowLoop) close() {
	l.mu.Lock()
	l.closed = true
	l.micro, l.macro = nil, nil
	l.cond.Broadcast()
	l.mu.Unlock()
}

// next blocks for the next function; false once the loop is closed.
func (l *workflowLoop) next() (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.micro) == 0 && len(l.macro) == 0 && !l.closed {
		if l.linger && l.pending.Load() == 0 {
			l.closed = true
			break
		}
		l.cond.Wait()
	}
	if l.closed {
		return nil, false
	}
	if len(l.micro) > 0 {
		fn := l.micro[0]
		l.micro[0] = nil
		l.micro = l.micro[1:]
		return fn, true
	}
	fn := l.macro[0]
	l.macro[0] = nil
	l.macro = l.macro[1:]
	return fn, true
}

// lingerThenClose lets the loop go on with what is already waiting (a late continuation of a settled workflow) and closes it
// once that is done, or after grace.
func (l *workflowLoop) lingerThenClose(grace time.Duration) {
	l.mu.Lock()
	l.linger = true
	l.cond.Broadcast()
	l.mu.Unlock()
	time.AfterFunc(grace, l.close)
}

// run executes posted functions until close.
func (l *workflowLoop) run() {
	for {
		fn, ok := l.next()
		if !ok {
			return
		}
		fn()
	}
}

// WorkflowFuture is the eventual result of a host callback.
type WorkflowFuture[T any] struct {
	mu      sync.Mutex
	done    bool
	val     T
	err     error
	waiters []func()
}

// NewWorkflowFuture returns a pending future; settle it with Resolve or Reject (the first call wins).
func NewWorkflowFuture[T any]() *WorkflowFuture[T] { return &WorkflowFuture[T]{} }

// WorkflowResolved is a future that already holds v.
func WorkflowResolved[T any](v T) *WorkflowFuture[T] { return &WorkflowFuture[T]{done: true, val: v} }

// WorkflowRejected is a future that already failed with err.
func WorkflowRejected[T any](err error) *WorkflowFuture[T] {
	return &WorkflowFuture[T]{done: true, err: err}
}

// WorkflowAsync runs fn on its own goroutine and settles the returned future with its result.
func WorkflowAsync[T any](fn func() (T, error)) *WorkflowFuture[T] {
	f := NewWorkflowFuture[T]()
	go func() {
		v, err := fn()
		f.settle(v, err)
	}()
	return f
}

// WorkflowAfter resolves after d.
func WorkflowAfter[T any](d time.Duration, v T) *WorkflowFuture[T] {
	f := NewWorkflowFuture[T]()
	time.AfterFunc(d, func() { f.settle(v, nil) })
	return f
}

func (f *WorkflowFuture[T]) Resolve(v T)      { f.settle(v, nil) }
func (f *WorkflowFuture[T]) Reject(err error) { var zero T; f.settle(zero, err) }

func (f *WorkflowFuture[T]) settle(v T, err error) {
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return
	}
	f.done, f.val, f.err = true, v, err
	waiters := f.waiters
	f.waiters = nil
	f.mu.Unlock()
	for _, w := range waiters {
		w()
	}
}

// Settled reports whether the future holds a result.
func (f *WorkflowFuture[T]) Settled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}

// result returns the settled value; ok is false while pending.
func (f *WorkflowFuture[T]) result() (v T, err error, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.val, f.err, f.done
}

// onSettle runs cb on the loop once f is settled: at once (queued) when it already is.
func onSettle[T any](l *workflowLoop, f *WorkflowFuture[T], cb func(T, error)) {
	l.pending.Add(1)
	inner := cb
	cb = func(v T, err error) {
		defer func() {
			l.pending.Add(-1)
			l.mu.Lock()
			l.cond.Broadcast()
			l.mu.Unlock()
		}()
		inner(v, err)
	}
	f.mu.Lock()
	if f.done {
		v, err := f.val, f.err
		f.mu.Unlock()
		l.post(func() { cb(v, err) })
		return
	}
	f.waiters = append(f.waiters, func() {
		f.mu.Lock()
		v, err := f.val, f.err
		f.mu.Unlock()
		l.post(func() { cb(v, err) })
	})
	f.mu.Unlock()
}

// workflowThen is promise.then(onFulfilled, onRejected): fn sees the value or the error and its result settles the returned future.
func workflowThen[T, U any](l *workflowLoop, f *WorkflowFuture[T], fn func(T, error) (U, error)) *WorkflowFuture[U] {
	out := NewWorkflowFuture[U]()
	onSettle(l, f, func(v T, err error) {
		u, uerr := fn(v, err)
		out.settle(u, uerr)
	})
	return out
}

// workflowThenF is workflowThen for a step that itself waits: fn returns the future the returned one follows.
func workflowThenF[T, U any](l *workflowLoop, f *WorkflowFuture[T], fn func(T, error) *WorkflowFuture[U]) *WorkflowFuture[U] {
	out := NewWorkflowFuture[U]()
	onSettle(l, f, func(v T, err error) {
		next := fn(v, err)
		onSettle(l, next, func(u U, uerr error) { out.settle(u, uerr) })
	})
	return out
}

// ParallelSemaphore is a FIFO semaphore limiting concurrent work (parallel-utils.ts:153-180).
type ParallelSemaphore struct {
	mu        sync.Mutex
	available int
	queue     []*WorkflowFuture[struct{}]
}

// NewParallelSemaphore is `new Semaphore(limit)`: the limit is floored and at least 1 (parallel-utils.ts:158).
func NewParallelSemaphore(limit float64) *ParallelSemaphore {
	n := int(limit)
	if limit != limit || n < 1 {
		n = 1
	}
	return &ParallelSemaphore{available: n}
}

// Acquire returns a future that settles when a slot is free.
func (s *ParallelSemaphore) Acquire() *WorkflowFuture[struct{}] {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.available > 0 {
		s.available--
		return WorkflowResolved(struct{}{})
	}
	f := NewWorkflowFuture[struct{}]()
	s.queue = append(s.queue, f)
	return f
}

// Release frees a slot, handing it to the longest waiter first.
func (s *ParallelSemaphore) Release() {
	s.mu.Lock()
	if len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		next.Resolve(struct{}{})
		return
	}
	s.available++
	s.mu.Unlock()
}
