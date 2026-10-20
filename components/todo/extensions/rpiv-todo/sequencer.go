package rpiv_todo

import (
	"sync"
	"sync/atomic"
)

// sequencer keeps the calls of a parallel batch in Pi's order. Pi starts the calls of a batch in source order
// from one thread, and each reaches the synchronous reducer at once, so the commits happen in source order and
// the calls end in source order. Here the handler first asks the host for the session id, so without an order
// the commits could be reordered (an update before the create it follows), and a call that returned early would
// answer before the one ahead of it. upstream: agent-loop.ts:619-647.
//
// The order is the position of the call among the todo calls of the assistant message that is the session's
// leaf while the batch runs (predecessorIn): a call waits until the host reports the end of the call ahead of
// it. That does not depend on which handler the scheduler starts first, which the SDK cannot promise
// (tool_start_order.go: a handler thread preempted between its start and its first statement can be overtaken;
// the bad-input scenario saw it once in about 40 runs). When the leaf does not list the call (a host that has
// not appended the message yet, a sequential batch whose earlier results are already in the session), the call
// takes the arrival order of its handlers instead, an arrival ticket taken as the handler's first statement.
type sequencer struct {
	mu    sync.Mutex
	tail  chan struct{}     // the done channel of the latest arrival ticket
	ended map[string]*ended // tool call id -> its end, reported by the host
}

type ended struct {
	ch     chan struct{}
	closed bool
}

// ticket is a call's place in the arrival order. wait returns when every earlier ticket was released;
// release hands the place on; skip leaves the arrival order for the position order without waiting.
type ticket struct {
	prev, done chan struct{}
	once       sync.Once
	skipped    atomic.Bool
}

func (q *sequencer) ticket() *ticket {
	t := &ticket{done: make(chan struct{})}
	q.mu.Lock()
	t.prev = q.tail
	q.tail = t.done
	q.mu.Unlock()
	return t
}

func (t *ticket) wait() {
	if t.prev != nil {
		<-t.prev
	}
}

func (t *ticket) release() {
	if t.skipped.Load() {
		return
	}
	t.wait()
	t.once.Do(func() { close(t.done) })
}

// skip frees the place at once: the calls behind it in the arrival order do not wait for it, and its own order
// is the position order.
func (t *ticket) skip() {
	t.skipped.Store(true)
	t.once.Do(func() { close(t.done) })
}

func (q *sequencer) endLocked(id string) *ended {
	if q.ended == nil {
		q.ended = map[string]*ended{}
	}
	e := q.ended[id]
	if e == nil {
		e = &ended{ch: make(chan struct{})}
		q.ended[id] = e
	}
	return e
}

// endOf is closed once the host reported the end of call id.
func (q *sequencer) endOf(id string) <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.endLocked(id).ch
}

// end records the end of call id: the call ran, or Pi finished it without running it (arguments that fail the
// schema end at once).
func (q *sequencer) end(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e := q.endLocked(id); !e.closed {
		e.closed = true
		close(e.ch)
	}
}

// reset is the end of the agent run: every call has ended, whatever was reported.
func (q *sequencer) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.ended {
		if !e.closed {
			e.closed = true
			close(e.ch)
		}
	}
	q.ended = nil
}

// predecessorIn is the id of the todo call ahead of callID in the assistant message entry ("" for the first),
// and whether the entry lists the call at all.
func predecessorIn(entry map[string]any, callID string) (prev string, listed bool) {
	msg, _ := entry["message"].(map[string]any)
	if msg["role"] != "assistant" {
		return "", false
	}
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		m, _ := b.(map[string]any)
		if m["type"] != "toolCall" || m["name"] != toolName {
			continue
		}
		id, _ := m["id"].(string)
		if id == callID {
			return prev, true
		}
		prev = id
	}
	return "", false
}
