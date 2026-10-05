package svc

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// watchEvents is the test-side journal of one watch subscription: one reader per subscription, a
// retained event journal, assertions that observe it (port of test/support/watch-events.ts).

type batchSource interface {
	// Next blocks for the next batch; it returns errStopped once stop closes.
	Next(stop <-chan struct{}) ([]ahptypes.ResourceChange, error)
}

var errStopped = errors.New("watch reader stopped")

type watchEvents struct {
	src      batchSource
	clock    Clock
	describe func() any

	mu      sync.Mutex
	batches [][]ahptypes.ResourceChange
	err     error
	closed  bool
	changed chan struct{}

	stop    chan struct{}
	reading chan struct{}
}

func newWatchEvents(src batchSource, clock Clock, describe func() any) *watchEvents {
	if clock == nil {
		clock = realClock{}
	}
	if describe == nil {
		describe = func() any { return map[string]any{} }
	}
	e := &watchEvents{src: src, clock: clock, describe: describe, changed: make(chan struct{}), stop: make(chan struct{}), reading: make(chan struct{})}
	go e.read()
	return e
}

func (e *watchEvents) notifyLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *watchEvents) read() {
	defer close(e.reading)
	for {
		batch, err := e.src.Next(e.stop)
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return
		}
		if err != nil {
			e.err = err
			e.notifyLocked()
			e.mu.Unlock()
			return
		}
		e.batches = append(e.batches, batch)
		e.notifyLocked()
		e.mu.Unlock()
	}
}

func (e *watchEvents) changesLocked() []ahptypes.ResourceChange {
	var all []ahptypes.ResourceChange
	for _, b := range e.batches {
		all = append(all, b...)
	}
	return all
}

func (e *watchEvents) Changes() []ahptypes.ResourceChange {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.changesLocked()
}

func (e *watchEvents) Batches() [][]ahptypes.ResourceChange {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]ahptypes.ResourceChange(nil), e.batches...)
}

func (e *watchEvents) Mark() int { return len(e.Changes()) }

// WaitFor blocks until complete accepts the changes since a checkpoint. A timeout stops only this
// assertion; it never starts or strands another read.
func (e *watchEvents) WaitFor(label string, complete func([]ahptypes.ResourceChange) bool, since int, timeout time.Duration) ([]ahptypes.ResourceChange, error) {
	timedOut := make(chan struct{})
	timer := e.clock.AfterFunc(timeout, func() { close(timedOut) })
	defer timer.Stop()
	fail := func(cause error) error {
		e.mu.Lock()
		batches, _ := json.Marshal(e.batches)
		e.mu.Unlock()
		state, _ := json.Marshal(e.describe())
		return fmt.Errorf("Expected %s; watch=%s; batches=%s: %w", label, state, batches, cause)
	}
	for {
		e.mu.Lock()
		if e.err != nil || e.closed {
			err := e.err
			if err == nil {
				err = errors.New("Collector closed")
			}
			e.mu.Unlock()
			return nil, fail(err)
		}
		changes := e.changesLocked()
		if since > len(changes) {
			since = len(changes)
		}
		changes = changes[since:]
		if complete(changes) {
			e.mu.Unlock()
			return changes, nil
		}
		changed := e.changed
		e.mu.Unlock()
		select {
		case <-changed:
		case <-timedOut:
			return nil, fail(errors.New("Timed out waiting for filesystem events"))
		}
	}
}

// Close ends the reader, settles pending assertions and reports a reader failure.
func (e *watchEvents) Close() error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.stop)
		e.notifyLocked()
	}
	e.mu.Unlock()
	<-e.reading
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

// clientSource reads resourceWatch/changed batches for one channel from a testkit client.
type clientSource struct {
	client  *testkit.Client
	channel string
}

func (s clientSource) Next(stop <-chan struct{}) ([]ahptypes.ResourceChange, error) {
	for {
		n, ok := s.client.Await(func(n testkit.Notification) bool {
			if n.Method != "action" {
				return false
			}
			var p struct {
				Channel string `json:"channel"`
				Action  struct {
					Type string `json:"type"`
				} `json:"action"`
			}
			return json.Unmarshal(n.Params, &p) == nil && p.Channel == s.channel && p.Action.Type == string(ahptypes.ActionTypeResourceWatchChanged)
		}, 20*time.Millisecond)
		if ok {
			var p struct {
				Action struct {
					Changes struct {
						Items []ahptypes.ResourceChange `json:"items"`
					} `json:"changes"`
				} `json:"action"`
			}
			if err := json.Unmarshal(n.Params, &p); err != nil {
				return nil, err
			}
			return p.Action.Changes.Items, nil
		}
		select {
		case <-stop:
			return nil, errStopped
		default:
		}
	}
}

// ── a hand-driven clock ─────────────────────────────────────────────────

type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, at: c.now + d, f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

// Tick advances the clock and runs every timer that came due, in order.
func (c *fakeClock) Tick(d time.Duration) {
	c.mu.Lock()
	target := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.stopped && !t.fired && t.at <= target && (next == nil || t.at < next.at) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.fired = true
		if next.at > c.now {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

// ── a scripted source ───────────────────────────────────────────────────

type scriptedSource struct {
	mu      sync.Mutex
	reads   int
	pending chan scriptedResult
	t       *testing.T
}

type scriptedResult struct {
	batch []ahptypes.ResourceChange
	err   error
}

func (s *scriptedSource) Next(stop <-chan struct{}) ([]ahptypes.ResourceChange, error) {
	s.mu.Lock()
	if s.pending != nil {
		s.mu.Unlock()
		s.t.Error("only one Next may be outstanding")
		return nil, errStopped
	}
	s.reads++
	ch := make(chan scriptedResult, 1)
	s.pending = ch
	s.mu.Unlock()
	select {
	case r := <-ch:
		return r.batch, r.err
	case <-stop:
		s.mu.Lock()
		if s.pending == ch {
			s.pending = nil // detaching does not settle an outstanding read, like the SDK
		}
		s.mu.Unlock()
		return nil, errStopped
	}
}

func (s *scriptedSource) Reads() int { s.mu.Lock(); defer s.mu.Unlock(); return s.reads }

func (s *scriptedSource) resolve(r scriptedResult) {
	// wait for the reader to park
	for i := 0; i < 500; i++ {
		s.mu.Lock()
		ch := s.pending
		s.pending = nil
		s.mu.Unlock()
		if ch != nil {
			ch <- r
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.t.Error("no read is outstanding")
}

func (s *scriptedSource) Push(items ...ahptypes.ResourceChange) {
	s.resolve(scriptedResult{batch: items})
}
func (s *scriptedSource) Fail(err error) { s.resolve(scriptedResult{err: err}) }

func addedChange(name string) ahptypes.ResourceChange {
	return ahptypes.ResourceChange{Uri: "file:///" + name, Type: ahptypes.ResourceChangeTypeAdded}
}

func hasURI(uri string) func([]ahptypes.ResourceChange) bool {
	return func(items []ahptypes.ResourceChange) bool {
		for _, i := range items {
			if i.Uri == uri {
				return true
			}
		}
		return false
	}
}

// Twins of upstream test/watch-events.test.ts: regression tests for the journal itself.

func TestWatchEventsJournal(t *testing.T) {
	twin.Run(t, "watch-events", "watch assertions share a reader and retain the entire batch, even after a match", func(t *testing.T) {
		input := &scriptedSource{t: t}
		events := newWatchEvents(input, nil, nil)
		defer events.Close()
		type result struct {
			changes []ahptypes.ResourceChange
			err     error
		}
		first, second := make(chan result, 1), make(chan result, 1)
		go func() { c, err := events.WaitFor("a", hasURI("file:///a"), 0, 4*time.Second); first <- result{c, err} }()
		go func() { c, err := events.WaitFor("b", hasURI("file:///b"), 0, 4*time.Second); second <- result{c, err} }()
		testkit.Eventually(t, "the single reader parks", func() bool { return input.Reads() == 1 })
		input.Push(addedChange("a"), addedChange("b"))
		want := []ahptypes.ResourceChange{addedChange("a"), addedChange("b")}
		for _, ch := range []chan result{first, second} {
			r := <-ch
			if r.err != nil || fmt.Sprint(r.changes) != fmt.Sprint(want) {
				t.Fatalf("%+v", r)
			}
		}
		if got := events.Batches(); len(got) != 1 || fmt.Sprint(got[0]) != fmt.Sprint(want) {
			t.Fatalf("batches %v", got)
		}
		again, err := events.WaitFor("already received b", hasURI("file:///b"), 0, 4*time.Second)
		if err != nil || fmt.Sprint(again) != fmt.Sprint(events.Changes()) {
			t.Fatalf("%v %v", again, err)
		}
	})

	twin.Run(t, "watch-events", "a timed-out assertion neither steals the next batch nor accepts events before its checkpoint", func(t *testing.T) {
		clock := &fakeClock{}
		input := &scriptedSource{t: t}
		events := newWatchEvents(input, clock, func() any { return map[string]any{"exists": true} })
		defer events.Close()
		testkit.Eventually(t, "reader parks", func() bool { return input.Reads() == 1 })
		input.Push(addedChange("a"))
		if _, err := events.WaitFor("first a", func(items []ahptypes.ResourceChange) bool { return len(items) == 1 }, 0, time.Minute); err != nil {
			t.Fatal(err)
		}
		since := events.Mark()
		rejected := make(chan error, 1)
		go func() {
			_, err := events.WaitFor("second a", func(items []ahptypes.ResourceChange) bool { return len(items) > 0 }, since, 10*time.Millisecond)
			rejected <- err
		}()
		// the assertion must have armed its timer before the clock moves
		testkit.Eventually(t, "the assertion armed its timer", func() bool { clock.mu.Lock(); defer clock.mu.Unlock(); return len(clock.timers) >= 2 })
		clock.Tick(10 * time.Millisecond)
		err := <-rejected
		if err == nil {
			t.Fatal("the assertion must time out")
		}
		for _, want := range []string{"second a", `"exists":true`, "file:///a"} {
			if !contains(err.Error(), want) {
				t.Fatalf("error %q lacks %q", err, want)
			}
		}
		if input.Reads() != 2 {
			t.Fatalf("timeout must not start another reader, reads=%d", input.Reads())
		}
		next := make(chan []ahptypes.ResourceChange, 1)
		go func() {
			c, _ := events.WaitFor("second a", func(items []ahptypes.ResourceChange) bool { return len(items) > 0 }, since, time.Minute)
			next <- c
		}()
		input.Push(addedChange("a"))
		if got := <-next; fmt.Sprint(got) != fmt.Sprint([]ahptypes.ResourceChange{addedChange("a")}) {
			t.Fatalf("%v", got)
		}
		if n := len(events.Batches()); n != 2 {
			t.Fatalf("%d batches", n)
		}
	})

	twin.Run(t, "watch-events", "reports a reader failure during cleanup even without a waiting assertion", func(t *testing.T) {
		input := &scriptedSource{t: t}
		events := newWatchEvents(input, nil, nil)
		failure := errors.New("subscription read failed")
		testkit.Eventually(t, "reader parks", func() bool { return input.Reads() == 1 })
		input.Fail(failure)
		time.Sleep(20 * time.Millisecond)
		if err := events.Close(); err != failure {
			t.Fatalf("got %v", err)
		}
	})

	twin.Run(t, "watch-events", "closing the journal settles pending assertions and the sole subscription reader", func(t *testing.T) {
		input := &scriptedSource{t: t}
		events := newWatchEvents(input, nil, nil)
		rejected := make(chan error, 1)
		go func() {
			_, err := events.WaitFor("never arrives", func(items []ahptypes.ResourceChange) bool { return len(items) > 0 }, 0, 4*time.Second)
			rejected <- err
		}()
		testkit.Eventually(t, "reader parks", func() bool { return input.Reads() == 1 })
		time.Sleep(10 * time.Millisecond)
		if err := events.Close(); err != nil && !errors.Is(err, errStopped) {
			t.Fatal(err)
		}
		if err := <-rejected; err == nil {
			t.Fatal("a pending assertion must be rejected when the journal closes")
		}
		if input.Reads() != 1 {
			t.Fatalf("reads=%d", input.Reads())
		}
	})
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
