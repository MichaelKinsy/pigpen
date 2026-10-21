package croner

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

// maxDelay: croner never waits more than 30 seconds between two checks.
const maxDelay = 30 * 1000

// Cron is croner's Cron: a pattern (or one date, for a string with a ':'), and a timer loop that calls fn at each match.
type Cron struct {
	mu         sync.Mutex
	pattern    *Pattern
	once       *CronDate
	fn         func()
	kill       bool
	timer      *time.Timer
	currentRun *CronDate
}

// Validate is `new Cron(pattern, () => {})` without its timer: the error croner throws, or nil.
func Validate(pattern string) error {
	c, err := parse(pattern)
	if err != nil {
		return err
	}
	// the constructor schedules: its first msToNext() runs the search, which can throw
	_, err = c.next(nil)
	return err
}

func parse(pattern string) (*Cron, error) {
	c := &Cron{}
	// `pattern.indexOf(":") > 0`: a string with a colon after its first character is a date, not a pattern.
	if i := strings.IndexByte(pattern, ':'); pattern != "" && i > 0 {
		once, err := fromString(pattern)
		if err != nil {
			return nil, err
		}
		c.once = once
		return c, nil
	}
	p, err := NewPattern(pattern)
	if err != nil {
		return nil, err
	}
	c.pattern = p
	return c, nil
}

// New is `new Cron(pattern, fn)`: it parses the pattern and, with a function, starts the timer loop.
func New(pattern string, fn func()) (*Cron, error) {
	c, err := parse(pattern)
	if err != nil {
		return nil, err
	}
	if fn != nil {
		c.fn = fn
		if err := c.schedule(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// next is _next: the next run after prev (nil: now), or nil.
func (c *Cron) next(prev *CronDate) (*CronDate, error) {
	var previousRun *CronDate
	if prev == nil {
		previousRun = NowDate()
	} else {
		previousRun = prev.clone()
	}
	var nextRun *CronDate
	if c.once != nil {
		nextRun = c.once
	} else {
		var err error
		nextRun, err = previousRun.clone().Increment(c.pattern)
		if err != nil {
			return nil, err
		}
	}
	if c.once != nil && c.once.Date() <= previousRun.Date() {
		return nil, nil
	}
	if nextRun == nil || c.kill {
		return nil, nil
	}
	return nextRun, nil
}

// NextRun is nextRun(): the next run after now as a Date (NaN: null).
func (c *Cron) NextRun() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, _ := c.nextRunLocked(nil)
	return t
}

func (c *Cron) nextRunLocked(prev *CronDate) (float64, error) {
	n, err := c.next(prev)
	if err != nil || n == nil {
		return math.NaN(), err
	}
	return n.Date(), nil
}

func (c *Cron) msToNext() (float64, bool, error) {
	n, err := c.next(nil)
	if err != nil || n == nil {
		return 0, false, err
	}
	return n.Date() - NowDate().Date(), true, nil
}

func (c *Cron) schedule() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scheduleLocked()
}

func (c *Cron) scheduleLocked() error {
	waitMs, ok, err := c.msToNext()
	if err != nil {
		return err
	}
	target, err := c.nextRunLocked(c.currentRun)
	if err != nil {
		return err
	}
	if !ok || math.IsNaN(waitMs) || math.IsNaN(target) {
		return nil
	}
	if waitMs > maxDelay {
		waitMs = maxDelay
	}
	wait := time.Duration(waitMs) * time.Millisecond
	if waitMs < 0 {
		wait = 0
	}
	c.timer = time.AfterFunc(wait, func() { c.checkTrigger(target) })
	return nil
}

func (c *Cron) checkTrigger(target float64) {
	c.mu.Lock()
	if c.kill {
		c.mu.Unlock()
		return
	}
	shouldRun := jsdate.Now() >= target
	if shouldRun {
		c.currentRun = NowDate()
	}
	fn := c.fn
	c.mu.Unlock()
	if shouldRun {
		fn()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kill {
		return
	}
	_ = c.scheduleLocked() // croner's timer callback would throw an uncaught RangeError here: no further timer is set
}

// Stop is stop(): no further run, the waiting timer is cleared.
func (c *Cron) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

// Parse is `new Cron(pattern)` without a function: no timer.
func Parse(pattern string) (*Cron, error) { return parse(pattern) }

// NextRunAfter is nextRun(new Date(t)): the next run after the instant t (NaN: null).
func (c *Cron) NextRunAfter(t float64) (float64, error) {
	d, err := fromMs(t)
	if err != nil {
		return math.NaN(), err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextRunLocked(d)
}
