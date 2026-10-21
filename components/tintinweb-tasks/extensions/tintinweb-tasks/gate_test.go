package tintinweb_tasks

import (
	"sync"
	"testing"
	"time"
)

// Pi runs the synchronous handlers of one model turn in call order; the SDK starts them in order but each runs on
// its own goroutine, so which one reaches the app lock first is the scheduler's choice. The gate gives the places
// out when a handler starts and lets them pass the lock in that order.
func TestOrderGateReleasesInPlaceOrder(t *testing.T) {
	var g orderGate
	const n = 8
	places := make([]uint64, n)
	for i := range places {
		places[i] = g.enter()
	}
	var mu sync.Mutex
	var got []int
	var wg sync.WaitGroup
	// Start the waiters in the opposite order, so the scheduler favours the wrong one.
	for i := n - 1; i >= 0; i-- {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.wait(places[i])
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			g.pass(places[i])
		}()
	}
	wg.Wait()
	for i, v := range got {
		if v != i {
			t.Fatalf("places passed in order %v, want ascending", got)
		}
	}
}

func TestOrderGatePassingAnEarlyPlaceTwiceDoesNotSkipOthers(t *testing.T) {
	var g orderGate
	a, b := g.enter(), g.enter()
	g.wait(a)
	g.pass(a)
	g.pass(a) // a handler that returns twice (a panic after pass) must not let b's successor jump the queue
	c := g.enter()
	done := make(chan struct{})
	go func() { g.wait(c); close(done) }()
	select {
	case <-done:
		t.Fatal("place c passed while b had not")
	case <-time.After(50 * time.Millisecond):
	}
	g.wait(b)
	g.pass(b)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("place c never passed")
	}
}
