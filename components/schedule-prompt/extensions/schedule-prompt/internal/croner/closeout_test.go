package croner

import (
	"math"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

// Assertions that pin the mutation run's survivors. Expected values are croner 10.0.1's under Node 24.19 (`TZ=<zone> node`).

func iso(t *testing.T, ms float64) string {
	t.Helper()
	s, ok := jsdate.ToISOString(ms)
	if !ok {
		t.Fatalf("no date for %v", ms)
	}
	return s
}

func TestWeeklyNicknameIsSunday(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	jsdate.SetLocation(time.UTC)
	c, err := Parse("@weekly")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.NextRunAfter(jsdate.Parse("2026-10-21T12:00:00Z")) // a Wednesday
	if err != nil || iso(t, got) != "2026-10-25T00:00:00.000Z" {
		t.Fatalf("@weekly = %v %v", got, err)
	}
}

// A one-shot is not due after its own instant: nextRun(date) is null at the date and the date itself just before it.
func TestOnceIsOverAtItsInstant(t *testing.T) {
	c, err := Parse("2026-10-21T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	at := jsdate.Parse("2026-10-21T10:00:00Z")
	if got, err := c.NextRunAfter(at - 1); err != nil || got != at {
		t.Fatalf("1 ms before: %v %v", got, err)
	}
	if got, err := c.NextRunAfter(at); err != nil || !math.IsNaN(got) {
		t.Fatalf("at the instant: %v %v", got, err)
	}
}

func TestTimerDelay(t *testing.T) {
	for _, c := range []struct {
		ms   float64
		want time.Duration
	}{{-5, 0}, {0, 0}, {500, 500 * time.Millisecond}, {30000, 30 * time.Second}, {30001, 30 * time.Second}, {1e12, 30 * time.Second}} {
		if got := timerDelay(c.ms); got != c.want {
			t.Errorf("timerDelay(%v) = %v, want %v", c.ms, got, c.want)
		}
	}
}

// The timer loop sets the next timer after a run: an every-second pattern runs again.
func TestEverySecondRunsAgain(t *testing.T) {
	var n atomic.Int32
	c, err := New("* * * * * *", func() { n.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	deadline := time.Now().Add(4500 * time.Millisecond)
	for n.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d runs in 4.5 s", n.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A check that wakes before the target does not run: only a clock at or past the target does.
func TestACheckBeforeTheTargetDoesNotRun(t *testing.T) {
	var n atomic.Int32
	c, err := New("0 0 0 1 1 *", func() { n.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	c.checkTrigger(jsdate.Now() + 300)
	if n.Load() != 0 {
		t.Fatal("ran 300 ms early")
	}
	c.checkTrigger(jsdate.Now() - 1)
	if n.Load() != 1 {
		t.Fatalf("runs = %d after a target in the past", n.Load())
	}
}

// V8's stack holds a pattern that needs about 4 900 steps (a 31st of February or April, from 2026) and croner answers null; 7 300 steps overflow.
func TestDeepSearchIsAnsweredBelowTheStackLimit(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	jsdate.SetLocation(time.UTC)
	c, err := Parse("0 0 0 31 2,4 *")
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	DepthHook = func(n int) { depth = max(depth, n) }
	defer func() { DepthHook = nil }()
	got, err := c.NextRunAfter(jsdate.Parse("2026-01-01T00:00:00Z"))
	if err != nil || !math.IsNaN(got) {
		t.Fatalf("answer = %v, %v", got, err)
	}
	if depth < 4000 || depth > 6000 {
		t.Fatalf("depth %d: the pattern no longer probes the band 3000..6824", depth)
	}
}

// fromTZ in a gap (a wall-clock time that does not exist): the later of the two guesses when the first answer does not match, as helpers/timezone.ts.
func TestFromTZInAGapAnswersTheLaterGuess(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	for _, c := range []struct {
		zone    string
		y, m, d int64
		h, i    int64
		want    string
	}{
		{"Europe/Berlin", 2005, 3, 27, 2, 30, "2005-03-27T01:30:00.000Z"},
		{"Europe/London", 2005, 3, 27, 1, 30, "2005-03-27T01:30:00.000Z"},
		{"Africa/Cairo", 2005, 4, 29, 0, 30, "2005-04-28T22:30:00.000Z"},
		{"Pacific/Auckland", 2005, 10, 2, 2, 30, "2005-10-01T14:30:00.000Z"},
		{"Pacific/Apia", 2011, 12, 30, 10, 30, "2011-12-30T20:30:00.000Z"},
		{"Australia/Lord_Howe", 2005, 10, 30, 2, 15, "2005-10-29T15:45:00.000Z"},
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		jsdate.SetLocation(loc)
		got, err := fromTZ(timePoint{y: c.y, m: c.m, d: c.d, h: c.h, i: c.i})
		if err != nil {
			t.Fatal(err)
		}
		if s := iso(t, got); s != c.want {
			t.Errorf("%s %d-%02d-%02dT%02d:%02d: %s, croner says %s", c.zone, c.y, c.m, c.d, c.h, c.i, s, c.want)
		}
	}
}

// A step of 0 is refused with croner's text; a parser that took it would loop forever, so the call is bounded.
func TestZeroStepIsRefused(t *testing.T) {
	done := make(chan error, 1)
	go func() { _, err := Parse("*/0 * * * * *"); done <- err }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "CronPattern: Syntax error, illegal stepping: 0" {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Parse did not return")
	}
}
