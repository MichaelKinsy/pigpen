package tintinweb_subagents

import (
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeClock is Vitest's fake timers: timers fire when advance reaches them.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	fn      func()
	stopped bool
}

func (c *fakeClock) after(d time.Duration, fn func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now + d, fn: fn}
	c.timers = append(c.timers, t)
	return func() { c.mu.Lock(); t.stopped = true; c.mu.Unlock() }
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	target := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].at < c.timers[j].at })
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.stopped && t.at <= target {
				next = t
				break
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.stopped = true
		c.now = next.at
		c.mu.Unlock()
		next.fn()
	}
}

type delivery struct {
	ids     []string
	partial bool
}

func newJoin(timeout time.Duration) (*groupJoinManager, *fakeClock, *[]delivery) {
	clock := &fakeClock{}
	var got []delivery
	g := newGroupJoinManager(func(records []*agentRecord, partial bool) {
		d := delivery{partial: partial}
		for _, r := range records {
			d.ids = append(d.ids, r.ID)
		}
		got = append(got, d)
	}, timeout, clock.after)
	return g, clock, &got
}

func rec(id string) *agentRecord {
	return &agentRecord{ID: id, Type: "general-purpose", Description: "test", Status: statusCompleted}
}

func sorted(s []string) []string { out := append([]string{}, s...); sort.Strings(out); return out }

// Twins of test/group-join.test.ts.
func TestGroupJoinTwins(t *testing.T) {
	const f = "group-join"
	tw(t, f, "returns 'pass' for unregistered agents and never invokes the callback", func(t *testing.T) {
		g, _, got := newJoin(groupJoinTimeout)
		eq(t, g.onAgentComplete(rec("a")), "pass")
		eq(t, len(*got), 0)
		eq(t, g.isGrouped("a"), false)
	})
	tw(t, f, "holds the first completion and arms the join timeout", func(t *testing.T) {
		g, clock, got := newJoin(30 * time.Second)
		g.registerGroup("g", []string{"a", "b"})
		eq(t, g.isGrouped("a"), true)
		eq(t, g.onAgentComplete(rec("a")), "held")
		clock.advance(29_999 * time.Millisecond)
		eq(t, len(*got), 0)
	})
	tw(t, f, "delivers all records (partial=false) when the final completion arrives in time", func(t *testing.T) {
		g, _, got := newJoin(groupJoinTimeout)
		g.registerGroup("g", []string{"a", "b"})
		g.onAgentComplete(rec("a"))
		eq(t, g.onAgentComplete(rec("b")), "delivered")
		eq(t, len(*got), 1)
		eq(t, sorted((*got)[0].ids), []string{"a", "b"})
		eq(t, (*got)[0].partial, false)
		eq(t, g.isGrouped("a") || g.isGrouped("b"), false) // the group is cleaned up: no future delivery can fire from these ids
	})
	tw(t, f, "delivers partial=true on timeout and re-arms the group for stragglers", func(t *testing.T) {
		g, clock, got := newJoin(30 * time.Second)
		g.registerGroup("g", []string{"a", "b", "c"})
		g.onAgentComplete(rec("a"))
		clock.advance(30 * time.Second)
		eq(t, *got, []delivery{{[]string{"a"}, true}})
		eq(t, [3]bool{g.isGrouped("a"), g.isGrouped("b"), g.isGrouped("c")}, [3]bool{false, true, true}) // b and c remain as stragglers
	})
	tw(t, f, "uses the shorter straggler timeout (15s) regardless of the configured group timeout", func(t *testing.T) {
		g, clock, got := newJoin(30 * time.Second)
		g.registerGroup("g", []string{"a", "b", "c"})
		g.onAgentComplete(rec("a"))
		clock.advance(30 * time.Second)
		eq(t, len(*got), 1)
		g.onAgentComplete(rec("b")) // a straggler fires at 15 s, not 30 s
		clock.advance(14_999 * time.Millisecond)
		eq(t, len(*got), 1)
		clock.advance(time.Millisecond)
		eq(t, len(*got), 2)
		eq(t, (*got)[1], delivery{[]string{"b"}, true})
		eq(t, g.isGrouped("c"), true) // c is the remaining straggler now
	})
	tw(t, f, "delivers stragglers as a complete batch (partial=false) when all complete before their timeout", func(t *testing.T) {
		g, clock, got := newJoin(30 * time.Second)
		g.registerGroup("g", []string{"a", "b", "c"})
		g.onAgentComplete(rec("a"))
		clock.advance(30 * time.Second)
		eq(t, len(*got), 1)
		g.onAgentComplete(rec("b"))
		eq(t, g.onAgentComplete(rec("c")), "delivered")
		eq(t, len(*got), 2)
		eq(t, sorted((*got)[1].ids), []string{"b", "c"})
		eq(t, (*got)[1].partial, false)
	})
	tw(t, f, "returns 'pass' for late completions arriving after a group is already delivered", func(t *testing.T) {
		g, _, got := newJoin(groupJoinTimeout)
		g.registerGroup("g", []string{"a", "b"})
		g.onAgentComplete(rec("a"))
		g.onAgentComplete(rec("b")) // full delivery
		eq(t, len(*got), 1)
		eq(t, g.onAgentComplete(rec("a")), "pass") // a duplicate or late completion must not trigger a second delivery
		eq(t, len(*got), 1)
	})
	tw(t, f, "dispose() clears pending timers so a partial delivery never fires post-dispose", func(t *testing.T) {
		g, clock, got := newJoin(30 * time.Second)
		g.registerGroup("g", []string{"a", "b"})
		g.onAgentComplete(rec("a")) // arms the 30 s timeout
		g.dispose()
		clock.advance(60 * time.Second)
		eq(t, len(*got), 0)
		eq(t, g.isGrouped("a") || g.isGrouped("b"), false)
	})
}
