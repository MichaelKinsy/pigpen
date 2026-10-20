package tintinweb_tasks

// orderGate lets the calls of one parallel batch take the app lock in the order they started. A signature stub:
// the red commit's test fails on it.
type orderGate struct{}

// enter takes the next place in the order. Call it first in a tool handler.
func (g *orderGate) enter() uint64 { return 0 }

// wait blocks until every earlier place has passed.
func (g *orderGate) wait(place uint64) {}

// pass lets the next place go.
func (g *orderGate) pass(place uint64) {}
