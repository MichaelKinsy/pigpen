package powerline_footer

import (
	"errors"
	"testing"
	"time"
)

// Twins of tests/editor-responsiveness.test.ts and tests/quit-cursor.test.ts.

// fakeTimers replaces setTimeout and clearTimeout: callbacks are kept, never run on their own.
type fakeTimers struct {
	callbacks []func()
	delays    []time.Duration
	cleared   map[*fakeTimer]bool
}

type fakeTimer struct {
	f *fakeTimers
}

func (t *fakeTimer) Stop() bool { t.f.cleared[t] = true; return true }

func newFakeScheduler(render func(), delay time.Duration) (*renderScheduler, *fakeTimers) {
	f := &fakeTimers{cleared: map[*fakeTimer]bool{}}
	s := newRenderScheduler(render, delay)
	s.afterFunc = func(d time.Duration, fn func()) schedulerTimer {
		f.callbacks = append(f.callbacks, fn)
		f.delays = append(f.delays, d)
		return &fakeTimer{f}
	}
	return s, f
}

func TestEditorResponsivenessTwins(t *testing.T) {
	const file = "editor-responsiveness"

	tw(t, file, "render scheduler coalesces pending status renders", func(t *testing.T) {
		renderCount := 0
		s, f := newFakeScheduler(func() { renderCount++ }, 33*time.Millisecond)

		s.schedule()
		s.scheduleAfter(150 * time.Millisecond)
		s.schedule()

		if len(f.callbacks) != 1 || f.delays[0] != 33*time.Millisecond {
			t.Fatalf("callbacks %d delays %v, want one at 33ms", len(f.callbacks), f.delays)
		}
		f.callbacks[0]()
		if renderCount != 1 {
			t.Fatalf("renderCount = %d", renderCount)
		}
		s.scheduleAfter(150 * time.Millisecond)
		if len(f.callbacks) != 2 || f.delays[1] != 150*time.Millisecond {
			t.Fatalf("callbacks %d delays %v, want a second at 150ms", len(f.callbacks), f.delays)
		}
	})

	tw(t, file, "render scheduler pulls pending work forward for an earlier deadline", func(t *testing.T) {
		renderCount := 0
		s, f := newFakeScheduler(func() { renderCount++ }, 33*time.Millisecond)

		s.scheduleAfter(250 * time.Millisecond)
		s.scheduleAfter(33 * time.Millisecond)

		if len(f.delays) != 2 || f.delays[0] != 250*time.Millisecond || f.delays[1] != 33*time.Millisecond {
			t.Fatalf("delays = %v", f.delays)
		}
		if len(f.cleared) != 1 {
			t.Fatalf("cleared %d timers, want 1", len(f.cleared))
		}
		f.callbacks[1]()
		if renderCount != 1 {
			t.Fatalf("renderCount = %d", renderCount)
		}
	})

	tw(t, file, "render scheduler allows callbacks to schedule follow-up renders", func(t *testing.T) {
		renderCount := 0
		var s *renderScheduler
		var f *fakeTimers
		s, f = newFakeScheduler(func() {
			renderCount++
			if renderCount == 1 {
				s.scheduleAfter(150 * time.Millisecond)
			}
		}, 33*time.Millisecond)

		s.schedule()
		f.callbacks[0]()

		if renderCount != 1 || len(f.callbacks) != 2 || f.delays[1] != 150*time.Millisecond {
			t.Fatalf("renderCount %d callbacks %d delays %v", renderCount, len(f.callbacks), f.delays)
		}
		f.callbacks[1]()
		if renderCount != 2 {
			t.Fatalf("renderCount = %d", renderCount)
		}
	})

	tw(t, file, "render scheduler cancels pending status renders", func(t *testing.T) {
		renderCount := 0
		s, f := newFakeScheduler(func() { renderCount++ }, 33*time.Millisecond)

		s.schedule()
		s.cancel()

		if len(f.cleared) != 1 || renderCount != 0 {
			t.Fatalf("cleared %d renders %d", len(f.cleared), renderCount)
		}
	})
}

func TestRenderSchedulerRunsOnTheRealClock(t *testing.T) {
	done := make(chan struct{}, 2)
	s := newRenderScheduler(func() { done <- struct{}{} }, 5*time.Millisecond)
	s.schedule()
	s.schedule()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the render never ran")
	}
	select {
	case <-done:
		t.Fatal("the two requests must coalesce into one render")
	case <-time.After(50 * time.Millisecond):
	}
	s.scheduleAfter(time.Hour)
	s.cancel()
	s.cancel()
}

func TestQuitCursorTwins(t *testing.T) {
	const file = "quit-cursor"

	tw(t, file, "startup welcome only shows on terminal startup when enabled", func(t *testing.T) {
		for _, c := range []struct {
			reason  string
			enabled bool
			mode    string
			want    bool
		}{
			{"startup", true, "tui", true},
			{"reload", true, "tui", false},
			{"startup", false, "tui", false},
			{"startup", true, "rpc", false},
			{"startup", true, "print", false},
			{"startup", true, "", false},
		} {
			if got := shouldShowStartupWelcome(c.reason, c.enabled, c.mode); got != c.want {
				t.Errorf("shouldShowStartupWelcome(%q, %v, %q) = %v, want %v", c.reason, c.enabled, c.mode, got, c.want)
			}
		}
	})

	tw(t, file, "stale extension context errors are recognized", func(t *testing.T) {
		if !isStaleExtensionContextError(errors.New("This extension instance is stale")) {
			t.Error("the instance message is stale")
		}
		if !isStaleExtensionContextError(errors.New("This extension ctx is stale")) {
			t.Error("the ctx message is stale")
		}
		if isStaleExtensionContextError(errors.New("other")) || isStaleExtensionContextError(nil) {
			t.Error("other errors are not stale")
		}
	})
}
