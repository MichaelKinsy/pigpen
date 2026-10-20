package tintinweb_tasks

import "sync"

// orderGate lets the calls of one parallel batch take the app lock in the order they started. Pi runs the
// synchronous handlers of a model turn one after another in call order, so the first TaskCreate of a turn is #1
// and the tool results come back in call order. The SDK starts the handlers in request order (each on its own
// goroutine); without the gate the scheduler decides which of them reaches the lock first.
type orderGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	next    uint64 // the next place to give out
	serving uint64 // the place that may pass now
}

// enter takes the next place in the order. Call it first in a tool handler.
func (g *orderGate) enter() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	place := g.next
	g.next++
	return place
}

// wait blocks until every earlier place has passed.
func (g *orderGate) wait(place uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	for g.serving != place {
		g.cond.Wait()
	}
}

// pass lets the next place go. Passing a place that already passed does nothing.
func (g *orderGate) pass(place uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if place != g.serving {
		return
	}
	g.serving++
	if g.cond != nil {
		g.cond.Broadcast()
	}
}
