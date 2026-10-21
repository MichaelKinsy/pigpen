//go:build !windows

package pi_background_tasks_test

import (
	"strings"
	"testing"
)

// Twin of the registered rows of output-matcher.test.ts (spawn-extension.ts mode matcher).

func TestTwinMatcherRegistered(t *testing.T) {
	tw(t, "output-matcher", "registered output timeout completes, reports once, suppresses output and retains exit delivery", func(t *testing.T) {
		r := rigHost(t, rigOptions{})
		task := r.spawnFifo(t, map[string]any{"notifyOnExit": true, "notifyOnOutput": true, "notifyPattern": "/" + strings.Repeat("a", 1000) + "(a+)+$/"})
		total := 0
		completions := 0
		react := func(text string) {
			task.write(t, text)
			total += len(text)
			waitFor(t, "the output", func() bool {
				snap, _ := r.rig.Task("bg-1")
				return snap["outputBytes"] == float64(total) && r.rig.ActiveTimeouts(2000) == 1
			})
			r.rig.FireTimeout(t, 2000)
			waitFor(t, "the wake decision", func() bool {
				r.rig.Settle()
				snap, _ := r.rig.Task("bg-1")
				p, _ := snap["pendingWakes"].([]any)
				return len(p) == 0
			})
			completions++
			r.Tool("bg_task", map[string]any{"action": "list"})
		}
		react(strings.Repeat("a", 1_000_000-1) + "!")
		snap, _ := r.rig.Task("bg-1")
		// The original's fixture delivers the megabyte as one chunk; a pipe delivers it in pieces, and each piece before the last
		// reschedules the pending wake (a "output-wake-rescheduled" drop). The settled wake is the last, and it is the timeout.
		drops, _ := snap["wakeEvents"].([]any)
		for i, raw := range drops {
			drop, _ := raw.(map[string]any)
			want := "output-wake-rescheduled"
			if i == len(drops)-1 {
				want = "notify-pattern-timeout"
			}
			eq(t, []any{drop["droppedReason"], drop["deliveredAt"]}, []any{want, nil}, "drop")
		}
		for i := 0; i < 20; i++ {
			react("\nsubsequent output")
		}
		msgs := r.CallsTo("sendMessage")
		eq(t, len(msgs), 1, "matcher must report one notice without output wakes")
		m, _ := msgs[0].Args["message"].(map[string]any)
		d, _ := m["details"].(map[string]any)
		eq(t, []any{d["eventType"], d["reason"]}, []any{"output-matcher-timeout", "notify-pattern-timeout"}, "the notice")
		eq(t, d["error"] != nil && d["error"] != "", true, "the notice carries the error")
		pattern, _ := d["matchedPattern"].(string)
		eq(t, len([]rune(pattern)) <= 192, true, "bounded pattern")
		eq(t, len(jsonOf(msgs[0].Args)) < 4096, true, "matcher notice must remain bounded")
		opts, _ := msgs[0].Args["options"].(map[string]any)
		eq(t, []any{opts["triggerTurn"], opts["deliverAs"]}, []any{true, "steer"}, "delivery")
		snap, _ = r.rig.Task("bg-1")
		budget, _ := snap["outputWakeBudget"].(map[string]any)
		pending, _ := snap["pendingWakes"].([]any)
		eq(t, []any{budget["wakes"], len(pending), completions}, []any{float64(0), 0, 21}, "budget, pending wakes, callbacks")
		task.exit()
		waitFor(t, "the exit wake", func() bool { return len(r.CallsTo("sendMessage")) == 2 })
		msgs = r.CallsTo("sendMessage")
		m, _ = msgs[1].Args["message"].(map[string]any)
		d, _ = m["details"].(map[string]any)
		opts, _ = msgs[1].Args["options"].(map[string]any)
		eq(t, []any{d["eventType"], opts["deliverAs"]}, []any{"exit", "followUp"}, "the exit wake")
		waitFor(t, "the exit to be acknowledged", func() bool { snap, _ := r.rig.Task("bg-1"); return snap["exitNotified"] == true })
	})
	tskip(t, "output-matcher", "must-fail: rethrowing the matcher timeout rejects the registered output callback", "a must-fail source mutation of the original; the Go mutant matcher-timeout-rethrown (port/mutations.json) must fail TestTwinMatcherRegistered")
	tskip(t, "output-matcher", "must-fail: dropping the matcher notice leaves headless callers without a failure", "a must-fail source mutation of the original; the Go mutant matcher-notice-dropped (port/mutations.json) must fail TestTwinMatcherRegistered")
	tskip(t, "output-matcher", "must-fail: recording a timeout as an ordinary non-match loses its cause", "a must-fail source mutation of the original; the Go mutant matcher-timeout-recorded-as-no-match (port/mutations.json) must fail TestTwinMatcherRegistered")
	tskip(t, "output-matcher", "must-fail: removing the matcher deadline hangs the same pathological test", "a must-fail source mutation of the original; the Go mutant matcher-deadline-removed (port/mutations.json) must fail TestTwinMatcherRegistered")
}
