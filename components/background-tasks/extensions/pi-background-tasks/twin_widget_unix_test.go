//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Twin of widget-lifecycle: the original runs ten scenarios of its widget, expiry timer and stack in separate processes (the
// clock and setTimeout replaced, the registry read through its global). The port runs them on a Rig: the clock is pinned, the
// expiry timer is the rig's, the registry is the stack's entries, and what the host sees is the `ui.setWidget` calls.

const nativeTimerLimitMs = 2_147_483_647

var widgetLifecycleCases = []struct {
	name    string
	seconds float64
}{
	{"expiry-above", 15},
	{"expiry-below", 15},
	{"sibling", 15},
	{"hide", 15},
	{"shutdown", 15},
	{"switch", 15},
	{"boundary-before", 2_147_483.646},
	{"boundary-at", 2_147_483.647},
	{"thirty-days", 2_592_000},
	{"overflow", 1e308},
}

// armed is the timeouts the rig holds that have not fired or been cleared (the interval of the orphan watcher is not one).
func armed(r *rigged) []string {
	var out []string
	for _, timer := range r.rig.ActiveTimers() {
		if strings.HasPrefix(timer, "timeout:") {
			out = append(out, timer)
		}
	}
	return out
}

// pendingExpiry is the one pending expiry timer's delay: "exactly one pending expiry".
func pendingExpiry(t *testing.T, r *rigged) int {
	t.Helper()
	timers := armed(r)
	if len(timers) != 1 {
		t.Fatalf("exactly one pending expiry, got %v", timers)
	}
	ms, err := strconv.Atoi(strings.TrimPrefix(timers[0], "timeout:"))
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func removedWidget(r *rigged, key string) bool {
	r.rig.Settle()
	for _, c := range r.CallsTo("ui.setWidget") {
		if c.Args["key"] == key && c.Args["view"] == nil {
			return true
		}
	}
	return false
}

func hasEntry(r *rigged, key string) bool {
	for _, k := range r.rig.WidgetEntries() {
		if k == key {
			return true
		}
	}
	return false
}

func TestTwinWidgetLifecycle(t *testing.T) {
	tw(t, "widget-lifecycle", "widget lifecycle scenarios", func(t *testing.T) {
		for _, c := range widgetLifecycleCases {
			t.Run(c.name, func(t *testing.T) { widgetScenario(t, c.name, c.seconds) })
		}
	})
}

func widgetScenario(t *testing.T, scenario string, seconds float64) {
	const start = int64(1_700_000_000_000)
	var now atomic.Int64
	now.Store(start)
	bg.SetClock(t, now.Load)
	placement, stackKey := "aboveEditor", "kendex-mini-dashboard-stack-above"
	if scenario == "expiry-below" {
		placement, stackKey = "belowEditor", "kendex-mini-dashboard-stack-below"
	}
	const widgetKey = "kendex-background-tasks"
	completed := func() []map[string]any {
		return []map[string]any{{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bg_task", "details": map[string]any{"tasks": []any{map[string]any{
			"id": "bg-1", "title": "true", "command": "true", "cwd": "/tmp", "pid": 0, "status": "completed", "exitCode": 0,
			"startedAt": start, "updatedAt": start, "lastOutputAt": nil, "expiresAt": nil, "outputBytes": 0,
			"logFile": "/tmp/task.log", "notifyOnExit": false, "notifyOnOutput": false, "exitNotified": true,
		}}}}}}
	}
	config := map[string]any{"showWidget": true, "widgetFinishedRetentionSeconds": seconds, "widgetPlacement": placement}
	r := rigHost(t, rigOptions{config: config, branch: completed(), noStart: true})
	r.Fire("session_start", map[string]any{"reason": "startup"})
	r.rig.Settle()

	if !hasEntry(r, widgetKey) {
		t.Fatal("the widget is in the stack")
	}
	if got := strings.Join(r.rig.StackLines(placement, 120), "\n"); !strings.Contains(got, "bg-1") {
		t.Fatalf("the stack draws bg-1, got %q", got)
	}
	// The retention in ms, as the original computes it: Math.max(0, Math.floor(seconds * 1000)), bounded by the native timer limit.
	retentionMs := math.Floor(seconds * 1000)
	first := pendingExpiry(t, r)
	wantFirst := int64(nativeTimerLimitMs)
	if retentionMs+1 < nativeTimerLimitMs {
		wantFirst = int64(retentionMs) + 1
	}
	eq(t, int64(first), wantFirst, "the first expiry delay")
	events := len(r.rig.TimerEvents())
	r.FireAll("session_compact", nil)
	r.rig.Settle()
	// resync cancels the previous expiry and arms a new one
	got := r.rig.TimerEvents()[events:]
	eq(t, got, []string{fmt.Sprintf("clear timeout:%d", first), fmt.Sprintf("set timeout:%d", first)}, "resync cancels previous expiry")
	pendingExpiry(t, r)

	if scenario == "sibling" {
		r.rig.SetSibling(20, []string{"sibling"}, placement)
	}
	switch scenario {
	case "hide", "shutdown", "switch":
		if scenario == "hide" {
			if known, failure := r.shortcut("alt+h"); !known || failure != "" {
				t.Fatalf("the widget shortcut: known %v, %q", known, failure)
			}
		} else {
			r.Fire("session_shutdown", map[string]any{"reason": "quit"})
		}
		r.rig.Settle()
		eq(t, len(armed(r)), 0, "cleanup cancels expiry")
		eq(t, hasEntry(r, widgetKey), false, "the stack no longer holds the widget")
		eq(t, removedWidget(r, stackKey), true, "host widget unregistered")
		if scenario == "switch" {
			// Pi replaces the extension instance between shutdown and session_start.
			r = rigHost(t, rigOptions{config: config, noStart: true})
			r.Fire("session_start", map[string]any{"reason": "startup"})
			r.rig.Settle()
		}
		now.Add(60_000)
		eq(t, len(armed(r)), 0, "no timer after a minute")
		eq(t, hasEntry(r, widgetKey), false, "still no widget")
	case "overflow":
		now.Add(int64(first))
		r.rig.FireTimeout(t, first)
		r.rig.Settle()
		eq(t, hasEntry(r, widgetKey), true, "the retention is unbounded")
		eq(t, pendingExpiry(t, r), nativeTimerLimitMs, "bounded again")
	default:
		retention := int64(retentionMs)
		if retention+1 > nativeTimerLimitMs {
			now.Add(int64(first))
			r.rig.FireTimeout(t, first)
			r.rig.Settle()
			eq(t, hasEntry(r, widgetKey), true, "bounded recheck must preserve retention")
			eq(t, int64(pendingExpiry(t, r)), start+retention-now.Load()+1, "recompute remaining delay")
		}
		// Inclusive boundary: rendering at the deadline still includes the task.
		now.Store(start + retention)
		if got := strings.Join(r.rig.StackLines(placement, 120), "\n"); !strings.Contains(got, "bg-1") {
			t.Fatalf("the deadline still draws bg-1, got %q", got)
		}
		last := pendingExpiry(t, r)
		now.Add(1)
		r.rig.FireTimeout(t, last)
		r.rig.Settle()
		eq(t, hasEntry(r, widgetKey), false, "expiry removes the entry without a task event")
		eq(t, len(armed(r)), 0, "no timer left")
		if scenario == "sibling" {
			eq(t, hasEntry(r, "sibling"), true, "the sibling stays")
			eq(t, r.rig.StackLines(placement, 120), []string{"sibling"}, "the stack draws the sibling")
			eq(t, removedWidget(r, stackKey), false, "sibling keeps host stack registered")
			r.rig.SetSibling(20, nil, placement)
		} else {
			eq(t, removedWidget(r, stackKey), true, "expiry calls host unregister")
		}
	}
	r.Fire("session_shutdown", map[string]any{"reason": "quit"})
}
