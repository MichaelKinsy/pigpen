package pi_background_tasks

import (
	"fmt"
	"testing"
	"time"
)

// Twins of widget-visibility, widget-expiry and active-context: the widget's decisions, its expiry timer and the adoption of a
// context.

func TestTwinWidgetVisibility(t *testing.T) {
	tw(t, "widget-visibility", "widget toggles preserve the last visible mode", func(t *testing.T) {
		for _, row := range []struct {
			name, initial string
			toggles       int
			mode, last    string
			renders       bool
		}{
			{"hide expanded", "expanded", 1, "hidden", "expanded", false},
			{"restore expanded", "expanded", 2, "expanded", "expanded", true},
			{"hide compact", "compact", 1, "hidden", "compact", false},
			{"restore compact", "compact", 2, "compact", "compact", true},
			{"show initially hidden", "hidden", 1, "compact", "compact", true},
		} {
			state := newWidgetVisibility(row.initial)
			for range row.toggles {
				state.toggle()
			}
			renders := shouldRenderWidget(true, 1, 1, true, state.mode)
			eq(t, []any{state.mode, state.lastVisibleMode, renders}, []any{row.mode, row.last, row.renders}, row.name)
		}
	})
	tw(t, "widget-visibility", "widget rendering requires UI, enabled visibility and visible tasks", func(t *testing.T) {
		for _, row := range []struct {
			name              string
			hasUI, showWidget bool
			tracked, visible  int
			mode              string
			expected          bool
		}{
			{"visible", true, true, 1, 1, "expanded", true},
			{"headless", false, true, 1, 1, "expanded", false},
			{"disabled", true, false, 1, 1, "expanded", false},
			{"untracked", true, true, 0, 0, "expanded", false},
			{"expired", true, true, 1, 0, "expanded", false},
			{"manually hidden", true, true, 1, 1, "hidden", false},
		} {
			eq(t, shouldRenderWidget(row.hasUI, row.tracked, row.visible, row.showWidget, row.mode), row.expected, row.name)
		}
	})
}

// strictTimers is the original's injected setTimeout and clearTimeout: one pending timer at most, and only the pending one can be
// cleared.
type strictTimers struct {
	t       *testing.T
	delays  []int64
	clears  int
	pending *strictTimer
}

type strictTimer struct {
	owner *strictTimers
	f     func()
}

func (s *strictTimers) After(d time.Duration, f func()) timer {
	if s.pending != nil {
		s.t.Fatal("previous timer was not cleared")
	}
	s.delays = append(s.delays, int64(d/time.Millisecond))
	s.pending = &strictTimer{owner: s, f: f}
	return s.pending
}

func (s *strictTimers) Every(time.Duration, func()) timer {
	s.t.Fatal("no interval expected")
	return nil
}

func (st *strictTimer) Stop() {
	if st.owner.pending != st {
		st.owner.t.Fatal("cleared timer is not pending")
	}
	st.owner.pending = nil
	st.owner.clears++
}

func TestTwinWidgetExpiry(t *testing.T) {
	tasks := []expiryTask{{statusRunning, 500}, {statusCompleted, 1_000}, {statusFailed, 2_000}}
	tw(t, "widget-expiry", "widget expiry selects the earliest unexpired finished task", func(t *testing.T) {
		for _, row := range []struct {
			name     string
			now      int64
			expected int64
			ok       bool
		}{
			{"first finished task", 10_000, 6_001, true},
			{"inclusive deadline", 16_000, 1, true},
			{"next finished task", 16_001, 1_000, true},
			{"all finished tasks expired", 17_001, 0, false},
		} {
			delay, ok := nextWidgetExpiryDelay(tasks, 15_000, row.now)
			eq(t, []any{delay, ok}, []any{row.expected, row.ok}, row.name)
		}
	})
	tw(t, "widget-expiry", "widget expiry owns one timer until it fires or is cleared", func(t *testing.T) {
		for _, row := range []struct {
			name           string
			retention, now int64
			steps          []string
			delays         []int64
			refreshes      int
			clears         int
			pending        bool
		}{
			{"refresh then cancel", 15_000, 10_000, []string{"schedule", "fire", "schedule", "clear"}, []int64{6_001, 6_001}, 1, 1, false},
			{"replace pending timer", 15_000, 10_000, []string{"schedule", "schedule", "clear"}, []int64{6_001, 6_001}, 0, 2, false},
			{"fired timer is no longer owned", 15_000, 10_000, []string{"schedule", "fire", "clear"}, []int64{6_001}, 1, 0, false},
			{"expired tasks need no timer", 15_000, 17_001, []string{"schedule", "clear"}, []int64{}, 0, 0, false},
			{"native timer limit", 2_592_000_000, 10_000, []string{"schedule"}, []int64{2_147_483_647}, 0, 0, true},
		} {
			timers := &strictTimers{t: t}
			refreshes := 0
			expiry := newWidgetExpiry(func() { refreshes++ }, timers)
			for _, step := range row.steps {
				switch step {
				case "schedule":
					expiry.schedule(tasks, row.retention, row.now)
				case "clear":
					expiry.clear()
				case "fire":
					timer := timers.pending
					if timer == nil {
						t.Fatal("no timer to fire")
					}
					timers.pending = nil
					timer.f()
				}
			}
			delays := timers.delays
			if delays == nil {
				delays = []int64{}
			}
			eq(t, fmt.Sprint(delays, refreshes, timers.clears, timers.pending != nil),
				fmt.Sprint(row.delays, row.refreshes, row.clears, row.pending), row.name)
		}
	})
}

func TestTwinActiveContext(t *testing.T) {
	// shouldAdoptActiveContext(current, incoming): current is null/undefined or {hasUI}; incoming.hasUI missing reads as false.
	tw(t, "active-context", "context adoption retains UI while accepting first and headless contexts", func(t *testing.T) {
		for _, row := range []struct {
			name                      string
			haveCurrent, currentHasUI bool
			incomingHasUI, expected   bool
		}{
			{"first UI context", false, false, true, true},
			{"first headless context", false, false, false, true},
			{"undefined retained context", false, false, false, true},
			{"UI replaces headless context", true, false, true, true},
			{"UI replaces UI context", true, true, true, true},
			{"headless context preserves UI", true, true, false, false},
			{"missing UI flag preserves UI", true, true, false, false},
			{"headless context replaces headless context", true, false, false, true},
		} {
			eq(t, shouldAdoptActiveContext(row.haveCurrent, row.currentHasUI, row.incomingHasUI), row.expected, row.name)
		}
	})
	tw(t, "active-context", "retained UI context keeps the composed widget decision visible after headless input", func(t *testing.T) {
		retainedHasUI := true
		if shouldAdoptActiveContext(true, true, false) {
			retainedHasUI = false
		}
		eq(t, shouldRenderWidget(retainedHasUI, 1, 1, true, "compact"), true, "the retained UI context renders the widget")
	})
}
