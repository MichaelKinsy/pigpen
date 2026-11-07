package posthorse

// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
// This whole file, the `order` field and the three call sites in notes.go, and the two TEMPORARY-GATE tests in notes_order_test.go go together.

// Start order of the notes tool's file mutations. In the TS original every notes execution reaches
// withFileMutationQueue after the same single `await store.get(ctx)`, so the queue (FIFO) sees the
// calls of one parallel batch in the order they started (a `write` and an `append` of one note in
// one batch land in source order: notes.ts:151-177, official-host.ts:1457). In Go each execution
// runs on its own goroutine and makes a different number of host round trips before it asks the
// host's queue for its turn, so without this gate the host could grant the turns in either order and
// the append could run before the write it follows.
//
// A ticket is taken at the very start of an execution (the SDK starts a batch's handlers in arrival
// order) and passed when the execution has acquired its queue turn, failed before reaching the queue,
// or turned out not to queue at all. An execution asks the host for its turn only after the ticket
// taken before its own has passed, so the host receives the requests in start order.

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var noteOrder struct {
	mu   sync.Mutex
	tail chan struct{}
}

type noteTicket struct {
	previous <-chan struct{}
	passed   chan struct{}
	once     sync.Once
}

// takeNoteTicket reserves the next place in the start order.
func takeNoteTicket() *noteTicket {
	t := &noteTicket{passed: make(chan struct{})}
	noteOrder.mu.Lock()
	t.previous = noteOrder.tail
	noteOrder.tail = t.passed
	noteOrder.mu.Unlock()
	return t
}

// pass hands the place on; it is idempotent and safe on a nil ticket.
func (t *noteTicket) pass() {
	if t == nil {
		return
	}
	t.once.Do(func() { close(t.passed) })
}

// queuedInOrder is sdk.WithFileMutationQueue that asks the host for its turn in ticket order and passes the ticket once the turn is held.
func queuedInOrder[T any](t *noteTicket, done <-chan struct{}, path string, fn func() (T, error)) (T, error) {
	if t != nil && t.previous != nil {
		select {
		case <-t.previous:
		case <-done:
		}
	}
	return sdk.WithFileMutationQueue(path, func() (T, error) {
		t.pass()
		return fn()
	})
}
