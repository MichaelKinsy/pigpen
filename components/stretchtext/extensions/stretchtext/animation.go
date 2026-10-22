package stretchtext

import (
	"sync"
	"time"
)

const frameInterval = 30 * time.Millisecond

// Clock is the time the animator reads and the timer it waits on.
type Clock interface {
	Now() time.Time
	// Every calls f each d until the returned stop is called. f never runs concurrently with itself.
	Every(d time.Duration, f func()) (stop func())
}

// Animation is one growth or shrinking of a stretch.
type Animation struct {
	From, To float64
	Seconds  float64
	// Frame is called on each frame with the progress, from From to To.
	Frame func(progress float64)
	// Done is called once, when the animation ends or another takes its place.
	Done func()
}

// Animator runs one animation at a time: starting another lands the running one where it stands. A
// port of src/animation.ts of pi-stretchtext.
type Animator struct {
	clock Clock
	mu    sync.Mutex
	run   *running
}

type running struct {
	mu       sync.Mutex
	finished bool
	stopTick func()
	done     func()
	animator *Animator
}

// NewAnimator returns an animator on clock.
func NewAnimator(clock Clock) *Animator { return &Animator{clock: clock} }

// Run starts animation, landing the one that runs.
func (a *Animator) Run(animation Animation) {
	a.Stop()
	start := a.clock.Now()
	r := &running{done: animation.Done, animator: a}
	a.mu.Lock()
	a.run = r
	a.mu.Unlock()
	r.stopTick = a.clock.Every(frameInterval, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.finished {
			return
		}
		t := a.clock.Now().Sub(start).Seconds() / animation.Seconds
		if t >= 1 {
			r.finishLocked()
		} else {
			animation.Frame(animation.From + (animation.To-animation.From)*t)
		}
	})
	animation.Frame(animation.From)
}

// Stop lands the running animation where it stands.
func (a *Animator) Stop() {
	a.mu.Lock()
	r := a.run
	a.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishLocked()
}

func (r *running) finishLocked() {
	if r.finished {
		return
	}
	r.finished = true
	if r.stopTick != nil {
		r.stopTick()
	}
	r.animator.mu.Lock()
	if r.animator.run == r {
		r.animator.run = nil
	}
	r.animator.mu.Unlock()
	r.done()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Every(d time.Duration, f func()) func() {
	ticker := time.NewTicker(d)
	quit := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				f()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			ticker.Stop()
			close(quit)
		})
	}
}

// RealClock is the wall clock.
func RealClock() Clock { return realClock{} }
