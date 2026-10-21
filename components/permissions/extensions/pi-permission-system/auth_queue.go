package pi_permission_system

import (
	"fmt"
	"sync"
)

// Port of src/authority/ask-dialog-queue.ts: serializes the human-facing presentations one session owns. Pi's inline ctx.ui.custom
// slot holds one component: a second presentation mounts over the first, whose promise then never settles. The extension raises
// asks from two independent tasks (the gate's local ask and the forwarded ask a poll tick drains), so without a queue whichever
// arrives second silently strands the first (#965).
//
// A JavaScript promise is a blocking call here: Admit queues the ask (admission order is the call order, synchronously) and Wait
// blocks for its value.

// AskDialogRelease is the shutdown-facing slice of AskDialogQueue: settle everything this session still owes an answer for.
type AskDialogRelease interface {
	ReleaseAll(reason string)
}

// AskDialogAdmission is the admission-facing slice of AskDialogQueue for the decision-typed asks the terminal presents: present
// this ask once every earlier one has settled. released is the caller's own fallback; the queue never constructs a decision,
// because a decision is stamped at the site that decides.
type AskDialogAdmission interface {
	RunDecision(present func() (PermissionPromptDecision, error), released func(reason string) PermissionPromptDecision) (PermissionPromptDecision, error)
}

// AskDialogQueue serializes presentations; it never lets two of its own overlap. A presentation another extension mounts is outside
// its evidence (upstream declined to arbitrate globally, earendil-works/pi#7007).
type AskDialogQueue struct {
	mu sync.Mutex
	// tail is closed once every ask admitted so far has been presented or released.
	tail <-chan struct{}
	// outstanding holds the asks that still owe their caller a value, queued and in-flight alike, in admission order.
	outstanding []*releasable
}

// releasable is the release capability of one admitted ask, typed so the set need not name a value type it never reads.
type releasable struct{ release func(reason string) }

var closedTail = func() <-chan struct{} { c := make(chan struct{}); close(c); return c }()

// NewAskDialogQueue is `new AskDialogQueue()`.
func NewAskDialogQueue() *AskDialogQueue {
	return &AskDialogQueue{tail: closedTail}
}

// AdmittedAsk is one admitted ask and the value its caller is waiting for. A release while a presentation is in flight leaves the
// host's own call pending, and it may return later; that late answer is discarded because the caller's value has already settled.
type AdmittedAsk[T any] struct {
	released func(reason string) T
	mu       sync.Mutex
	settled  bool
	done     chan struct{}
	value    T
	err      error
}

func (a *AdmittedAsk[T]) isSettled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settled
}

// settle takes the first outcome only: a promise ignores a later resolve or reject.
func (a *AdmittedAsk[T]) settle(value T, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settled {
		return
	}
	a.settled = true
	a.value, a.err = value, err
	close(a.done)
}

// Wait blocks until the ask settles: its presentation's value, its presentation's error, or the released fallback.
func (a *AdmittedAsk[T]) Wait() (T, error) {
	<-a.done
	return a.value, a.err
}

// Admit admits an ask, presenting it only once every earlier one has settled. present may be called on another goroutine.
func Admit[T any](q *AskDialogQueue, present func() (T, error), released func(reason string) T) *AdmittedAsk[T] {
	ask := &AdmittedAsk[T]{released: released, done: make(chan struct{})}
	entry := &releasable{release: func(reason string) { ask.settle(released(reason), nil) }}
	myDone := make(chan struct{})

	q.mu.Lock()
	q.outstanding = append(q.outstanding, entry)
	prev := q.tail
	q.tail = myDone
	q.mu.Unlock()

	go func() {
		defer close(myDone)
		<-prev
		// A release while this ask waited its turn means nothing may render: presenting it now would put a dialog on screen for a
		// decision that has already been answered.
		if ask.isSettled() {
			return
		}
		value, err := callPresent(present)
		ask.settle(value, err)
		q.mu.Lock()
		for i, e := range q.outstanding {
			if e == entry {
				q.outstanding = append(q.outstanding[:i], q.outstanding[i+1:]...)
				break
			}
		}
		q.mu.Unlock()
	}()
	return ask
}

// callPresent runs one presentation, turning a panic into the error a rejected promise would carry, so one failing ask cannot take
// the process down or strand the queue.
func callPresent[T any](present func() (T, error)) (value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero T
			value, err = zero, fmt.Errorf("presentation panicked: %v", r)
		}
	}()
	return present()
}

// Run is Admit followed by Wait.
func Run[T any](q *AskDialogQueue, present func() (T, error), released func(reason string) T) (T, error) {
	return Admit(q, present, released).Wait()
}

// RunDecision implements AskDialogAdmission.
func (q *AskDialogQueue) RunDecision(present func() (PermissionPromptDecision, error), released func(reason string) PermissionPromptDecision) (PermissionPromptDecision, error) {
	return Run(q, present, released)
}

// ReleaseAll settles every outstanding ask with its own released value. The tail is reset rather than chained on, because the
// in-flight ask's presentation is still pending in the host and nothing would ever settle it: a session that keeps running must
// not queue behind a dialog nobody can answer.
func (q *AskDialogQueue) ReleaseAll(reason string) {
	q.mu.Lock()
	releasing := q.outstanding
	q.outstanding = nil
	q.tail = closedTail
	q.mu.Unlock()
	for _, entry := range releasing {
		entry.release(reason)
	}
}
