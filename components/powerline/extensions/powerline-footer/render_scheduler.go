package powerline_footer

import (
	"sync"
	"time"
)

// renderScheduler coalesces render requests: a request for a later moment than the pending one is dropped, an earlier one pulls
// the pending render forward. upstream: render-scheduler.ts (createRenderScheduler).
type renderScheduler struct {
	render       func()
	defaultDelay time.Duration

	// Injectable for tests: the clock and the timer (setTimeout/clearTimeout of the original).
	now       func() time.Time
	afterFunc func(d time.Duration, f func()) schedulerTimer

	mu      sync.Mutex
	pending *pendingRender
}

type schedulerTimer interface{ Stop() bool }

type pendingRender struct {
	timer    schedulerTimer
	deadline time.Time
}

func newRenderScheduler(render func(), defaultDelay time.Duration) *renderScheduler {
	return &renderScheduler{
		render: render, defaultDelay: defaultDelay, now: time.Now,
		afterFunc: func(d time.Duration, f func()) schedulerTimer { return time.AfterFunc(d, f) },
	}
}

// schedule renders after the default delay; scheduleAfter after delay.
func (s *renderScheduler) schedule() { s.scheduleAfter(s.defaultDelay) }

func (s *renderScheduler) scheduleAfter(delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := s.now().Add(delay)
	if s.pending != nil {
		if !deadline.Before(s.pending.deadline) {
			return
		}
		s.pending.timer.Stop()
	}
	p := &pendingRender{deadline: deadline}
	p.timer = s.afterFunc(delay, func() {
		s.mu.Lock()
		if s.pending == p {
			s.pending = nil
		}
		s.mu.Unlock()
		s.render() // outside the lock: render may schedule the next one
	})
	s.pending = p
}

// cancel drops the pending render.
func (s *renderScheduler) cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return
	}
	s.pending.timer.Stop()
	s.pending = nil
}
