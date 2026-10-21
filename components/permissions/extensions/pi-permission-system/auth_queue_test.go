package pi_permission_system

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Twins of test/authority/ask-dialog-queue.test.ts. A JavaScript promise is a blocking call in the port, so "settles" is a channel the
// test closes and "drains every pending microtask" is a short sleep that lets the queue's goroutines run.

func releasedValue(reason string) string { return "released: " + reason }

type queueAsk struct {
	label     string
	presented *queueLog
	result    chan queueResult
	invoked   int
	mu        sync.Mutex
}

type queueResult struct {
	value string
	err   error
}

type queueLog struct {
	mu    sync.Mutex
	items []string
}

func (l *queueLog) add(s string) { l.mu.Lock(); l.items = append(l.items, s); l.mu.Unlock() }
func (l *queueLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

// makeQueueAsk is one presentation the test drives by hand: it records when it was invoked and settles only when the test says so.
func makeQueueAsk(label string, log *queueLog) *queueAsk {
	return &queueAsk{label: label, presented: log, result: make(chan queueResult, 1)}
}

func (a *queueAsk) present() (string, error) {
	a.mu.Lock()
	a.invoked++
	a.mu.Unlock()
	a.presented.add(a.label)
	r := <-a.result
	return r.value, r.err
}

func (a *queueAsk) settle()           { a.result <- queueResult{value: a.label} }
func (a *queueAsk) fail(err error)    { a.result <- queueResult{err: err} }
func (a *queueAsk) timesInvoked() int { a.mu.Lock(); defer a.mu.Unlock(); return a.invoked }

func settleMicrotasks() { time.Sleep(40 * time.Millisecond) }

// waitAsk waits for an admitted ask to settle, failing the test instead of hanging.
func waitAsk(t *testing.T, a *AdmittedAsk[string]) (string, error) {
	t.Helper()
	type res struct {
		v string
		e error
	}
	c := make(chan res, 1)
	go func() { v, e := a.Wait(); c <- res{v, e} }()
	select {
	case r := <-c:
		return r.v, r.e
	case <-time.After(5 * time.Second):
		t.Fatal("ask never settled")
		return "", nil
	}
}

func TestAskDialogQueueTwins(t *testing.T) {
	f := "authority/ask-dialog-queue"
	setup := func() (*AskDialogQueue, *queueLog) { return NewAskDialogQueue(), &queueLog{} }

	tw(t, f, "presents an ask admitted to an idle queue", func(t *testing.T) {
		q, log := setup()
		ask := makeQueueAsk("a", log)
		decision := Admit(q, ask.present, releasedValue)
		ask.settle()
		v, err := waitAsk(t, decision)
		eq(t, err, nil, "err")
		eq(t, v, "a", "value")
		eq(t, log.snapshot(), []string{"a"}, "presentations")
	})
	tw(t, f, "does not present a second ask until the first settles", func(t *testing.T) {
		q, log := setup()
		first, second := makeQueueAsk("a", log), makeQueueAsk("b", log)
		firstDecision, secondDecision := Admit(q, first.present, releasedValue), Admit(q, second.present, releasedValue)
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a"}, "before the first settles")
		first.settle()
		v, _ := waitAsk(t, firstDecision)
		eq(t, v, "a", "first")
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a", "b"}, "after the first settles")
		second.settle()
		v, _ = waitAsk(t, secondDecision)
		eq(t, v, "b", "second")
	})
	tw(t, f, "presents admitted asks in admission order", func(t *testing.T) {
		q, log := setup()
		first, second, third := makeQueueAsk("a", log), makeQueueAsk("b", log), makeQueueAsk("c", log)
		decisions := []*AdmittedAsk[string]{Admit(q, first.present, releasedValue), Admit(q, second.present, releasedValue), Admit(q, third.present, releasedValue)}
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a"}, "step 1")
		first.settle()
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a", "b"}, "step 2")
		second.settle()
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a", "b", "c"}, "step 3")
		third.settle()
		var got []string
		for _, d := range decisions {
			v, _ := waitAsk(t, d)
			got = append(got, v)
		}
		eq(t, got, []string{"a", "b", "c"}, "values")
	})
	tw(t, f, "rejects only the failing ask and presents the next one", func(t *testing.T) {
		q, log := setup()
		failing, next := makeQueueAsk("a", log), makeQueueAsk("b", log)
		failingDecision, nextDecision := Admit(q, failing.present, releasedValue), Admit(q, next.present, releasedValue)
		settleMicrotasks()
		failing.fail(errors.New("dialog exploded"))
		_, err := waitAsk(t, failingDecision)
		if err == nil || err.Error() != "dialog exploded" {
			t.Errorf("err %v", err)
		}
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a", "b"}, "presentations")
		next.settle()
		v, _ := waitAsk(t, nextDecision)
		eq(t, v, "b", "next")
	})
	t.Run("a panicking presentation rejects only its ask", func(t *testing.T) {
		q, log := setup()
		next := makeQueueAsk("b", log)
		panicking := Admit(q, func() (string, error) { panic("dialog exploded") }, releasedValue)
		nextDecision := Admit(q, next.present, releasedValue)
		if _, err := waitAsk(t, panicking); err == nil {
			t.Error("no error")
		}
		next.settle()
		v, _ := waitAsk(t, nextDecision)
		eq(t, v, "b", "next")
	})

	tw(t, f, "settles a queued ask without ever presenting it", func(t *testing.T) {
		q, log := setup()
		head, queued := makeQueueAsk("a", log), makeQueueAsk("b", log)
		headDecision, queuedDecision := Admit(q, head.present, releasedValue), Admit(q, queued.present, releasedValue)
		settleMicrotasks()
		q.ReleaseAll("the session ended")
		settleMicrotasks()
		v, _ := waitAsk(t, queuedDecision)
		eq(t, v, "released: the session ended", "queued")
		v, _ = waitAsk(t, headDecision)
		eq(t, v, "released: the session ended", "head")
		eq(t, queued.timesInvoked(), 0, "queued invoked")
		eq(t, log.snapshot(), []string{"a"}, "presentations")
	})
	tw(t, f, "never presents a released ask whose turn arrives afterwards", func(t *testing.T) {
		q, log := setup()
		head, queued := makeQueueAsk("a", log), makeQueueAsk("b", log)
		headDecision, queuedDecision := Admit(q, head.present, releasedValue), Admit(q, queued.present, releasedValue)
		settleMicrotasks()
		q.ReleaseAll("the session ended")
		// The stale dialog is still on screen after a release, so answering it advances the queue to an ask that has already been
		// answered.
		head.settle()
		settleMicrotasks()
		eq(t, queued.timesInvoked(), 0, "queued invoked")
		eq(t, log.snapshot(), []string{"a"}, "presentations")
		v, _ := waitAsk(t, headDecision)
		eq(t, v, "released: the session ended", "head")
		v, _ = waitAsk(t, queuedDecision)
		eq(t, v, "released: the session ended", "queued")
	})
	tw(t, f, "discards a presentation that resolves after its ask was released", func(t *testing.T) {
		q, log := setup()
		head := makeQueueAsk("a", log)
		decision := Admit(q, head.present, releasedValue)
		settleMicrotasks()
		q.ReleaseAll("the session ended")
		head.settle()
		v, _ := waitAsk(t, decision)
		eq(t, v, "released: the session ended", "value")
	})
	tw(t, f, "presents an ask admitted after a release", func(t *testing.T) {
		q, log := setup()
		abandoned := makeQueueAsk("a", log)
		abandonedDecision := Admit(q, abandoned.present, releasedValue)
		settleMicrotasks()
		q.ReleaseAll("the session ended")
		later := makeQueueAsk("b", log)
		laterDecision := Admit(q, later.present, releasedValue)
		settleMicrotasks()
		eq(t, log.snapshot(), []string{"a", "b"}, "presentations")
		later.settle()
		v, _ := waitAsk(t, laterDecision)
		eq(t, v, "b", "later")
		v, _ = waitAsk(t, abandonedDecision)
		eq(t, v, "released: the session ended", "abandoned")
		abandoned.settle() // let the stale presentation goroutine finish
	})
}
