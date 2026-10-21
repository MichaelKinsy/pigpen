package pi_background_tasks

import (
	"sync"
	"time"
)

// coalescedCall is one trailing call per window. The first request arms a timer; requests that arrive before it fires join
// that one call. Later requests arm a new window, so a steady stream of requests runs fn once per delay, never once per
// request and never starved by the stream. upstream: extensions/coalesce.ts.
type coalescedCall struct {
	fn     func()
	delay  time.Duration
	timers timers
	mu     sync.Mutex
	timer  timer
	gen    int
}

func newCoalescedCall(fn func(), delay time.Duration, t timers) *coalescedCall {
	if t == nil {
		t = realTimers{}
	}
	return &coalescedCall{fn: fn, delay: delay, timers: t}
}

// request runs fn once, delay after the first request of this window.
func (c *coalescedCall) request() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		return
	}
	c.gen++
	gen := c.gen
	c.timer = c.timers.After(c.delay, func() {
		c.mu.Lock()
		if c.gen != gen || c.timer == nil {
			c.mu.Unlock()
			return
		}
		c.timer = nil
		c.mu.Unlock()
		c.fn()
	})
}

// cancel drops the pending call, for a caller that just ran the work itself.
func (c *coalescedCall) cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer == nil {
		return
	}
	c.timer.Stop()
	c.timer = nil
}
