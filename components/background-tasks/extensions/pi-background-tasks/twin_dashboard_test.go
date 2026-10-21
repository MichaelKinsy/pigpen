package pi_background_tasks

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twin of dashboard: the dashboard renders before its log read and caches the command layout by content and width. The original
// runs its own layout and cache with the terminal's wrap counted; the port runs the whole dashboard with a plain theme and the
// same wrap, counting the calls for the command ("cache-me ...").

func TestTwinDashboard(t *testing.T) {
	tw(t, "dashboard", "the dashboard renders before its log read and caches command layout by content and width", func(t *testing.T) {
		wraps, renders := 0, 0
		task := fakeSnapshot(func(s *snapshot) {
			s.Status, s.Command, s.ID, s.StartedAt = statusCompleted, "cache-me "+strings.Repeat("a", 400), "bg-1", 2
		})
		selectedTask := fakeSnapshot(func(s *snapshot) {
			s.Status, s.Command, s.ID, s.StartedAt = statusCompleted, task.Command, "bg-2", 1
		})
		tasks := []*snapshot{&task, &selectedTask}
		// A read is a callback the test releases: the original's promise.
		reads := map[string][]func(string){}
		deps := dashboardDeps{
			sortedTasks: func() []*snapshot { return tasks },
			getTask: func(id string) *snapshot {
				for _, s := range tasks {
					if s.ID == id {
						return s
					}
				}
				return nil
			},
			getTaskOutput:      func(id string, done func(string)) { reads[id] = append(reads[id], done) },
			requestStop:        func(string) (bool, string) { t.Fatal("unexpected stop"); return false, "" },
			clearFinishedTasks: func() int { return 0 },
		}
		release := func(id, text string) {
			pending := reads[id]
			reads[id] = nil
			for _, done := range pending {
				done(text)
			}
		}
		wrap := func(text string, width int) []string {
			if strings.HasPrefix(text, "cache-me") {
				wraps++
			}
			return widthx.WrapTextWithAnsi(text, width)
		}
		d := newDashboard(deps, plainTheme{}, "", func() int { return 40 }, wrap)
		d.SetInvalidate(func() { renders++ })
		has := func(lines []string, text string) bool {
			for _, line := range lines {
				if strings.Contains(line, text) {
					return true
				}
			}
			return false
		}
		mustTrue(t, has(d.Render(120), "(no output yet)"), "render must not block on the log")
		eq(t, wraps, 1, "the command is wrapped once")
		d.Render(120)
		eq(t, wraps, 1, "unchanged command must use the cache")
		_, _ = d.HandleInput(keyDown)
		mustTrue(t, has(d.Render(120), "(no output yet)"), "selected task read must remain pending")
		release("bg-2", "recent output")
		eq(t, renders, 2, "the selection and the arrived output each ask for a render")
		mustTrue(t, has(d.Render(120), "recent output"), "the arrived output is drawn")
		beforeStale := renders
		release("bg-1", "stale prior output")
		lines := d.Render(120)
		mustTrue(t, has(lines, "recent output"), "late prior read must retain selected output")
		mustTrue(t, !has(lines, "stale prior output"), "late prior read must not replace selected output")
		eq(t, renders, beforeStale, "late prior read must not request a render")
		_, _ = d.HandleInput("x")
		d.Render(120)
		eq(t, wraps, 1, "expansion must reuse the complete layout")
		d.Render(80)
		eq(t, wraps, 2, "a new width wraps again")
		selectedTask.Command += "different"
		d.Render(80)
		eq(t, wraps, 3, "a new command wraps again")
		d.resetTheme()
		d.Render(80)
		eq(t, wraps, 4, "theme invalidation must clear styled lines")
		d.Dispose()
		atDispose := renders
		release("bg-2", "text that arrives after the dashboard closed")
		release("bg-1", "so does this")
		eq(t, renders, atDispose, "a pending read must not render a disposed modal")
	})
	tskip(t, "dashboard", "must-fail: bypassing the command cache fails the dashboard suite", "a must-fail source mutation of the original; the Go mutant dashboard-command-cache-bypassed (port/mutations.json) must fail TestTwinDashboard and TestTwinComponentBenchmark")
	tskip(t, "dashboard", "must-fail: dropping the selected-task guard lets a late read replace selected output", "a must-fail source mutation of the original; the Go mutant dashboard-selected-task-guard-dropped (port/mutations.json) must fail TestTwinDashboard")
}

// The terminal's bytes for the arrow keys (the original's fixture matches the key names themselves).
const (
	keyDown = "\x1b[B"
	keyUp   = "\x1b[A"
)
