package pi_background_tasks

import (
	"strings"
	"testing"
	"time"
)

// Twins of the original's activity, acknowledgement and output-matcher tests (extensions/__tests__/activity*.test.ts,
// tests/background-ack.test.ts, tests/output-matcher.test.ts).

func TestTwinActivity(t *testing.T) {
	tw(t, "activity", "activity event construction", func(t *testing.T) {
		type row struct {
			name     string
			status   string
			exitCode int
			command  string
			kind     string
			sequence int
			typ      string
			severity string
			imp      string
			shown    string
		}
		rows := []row{
			{"completed", statusCompleted, 0, "printf ready", "exit", 4, "bg_task.completed", "success", "normal", "printf ready"},
			{"failed", statusFailed, 1, "printf ready", "exit", 4, "bg_task.failed", "error", "important", "printf ready"},
			{"timed out", statusTimedOut, 1, "printf ready", "exit", 4, "bg_task.timed_out", "error", "important", "printf ready"},
			{"stopped", statusStopped, 1, "printf ready", "exit", 4, "bg_task.stopped", "warning", "important", "printf ready"},
			{"long command", statusRunning, -1, repeat("x", 260), "start", 0, "bg_task.started", "info", "noisy", repeat("x", 200)},
		}
		for _, r := range rows {
			tk := fakeTask(func(x *task) {
				x.Status, x.Command = r.status, r.command
				if r.exitCode >= 0 {
					x.ExitCode = exitCode(r.exitCode)
				}
			})
			seq := r.sequence
			event := buildBackgroundTaskActivity(r.kind, tk, activityOptions{sequence: &seq})
			var wantExit any
			if r.exitCode >= 0 {
				wantExit = r.exitCode
			}
			eq(t, [7]any{event.Type, event.Severity, event.Importance, event.Details["command"], event.Details["status"], event.Details["exit_code"], event.Details["sequence"]},
				[7]any{r.typ, r.severity, r.imp, r.shown, r.status, wantExit, r.sequence}, r.name)
		}
	})

	tw(t, "activity-publication", "activity publication retains the ordered task trace", func(t *testing.T) {
		var events []activityEvent
		prev := activityBroker
		activityBroker = func(e activityEvent) { events = append(events, e) }
		defer func() { activityBroker = prev }()
		running := fakeTask(func(x *task) {
			x.ID, x.Command, x.output, x.OutputBytes, x.UpdatedAt = "bg-7", "printf ready", "ready\n", 6, 1_700_000_001_000
		})
		publishBackgroundTaskStarted(running)
		two, three := 2, 3
		publishBackgroundTaskActivity("output", running, activityOptions{matchedPattern: "ready", newOutputTail: "ready\n", sequence: &two})
		done := *running
		done.Status, done.ExitCode, done.UpdatedAt = statusCompleted, exitCode(0), 1_700_000_002_000
		publishBackgroundTaskActivity("exit", &done, activityOptions{sequence: &three})
		type seen struct {
			Type, Importance, Severity, Source, Task string
			Pattern, Tail                            any
			Sequence                                 any
		}
		var got []seen
		for _, e := range events {
			got = append(got, seen{e.Type, e.Importance, e.Severity, e.Source, e.Refs["bg_task_id"].(string), e.Details["matched_pattern"], e.Details["new_output_tail"], e.Details["sequence"]})
		}
		eq(t, got, []seen{
			{"bg_task.started", "noisy", "info", "pi-bg-task", "bg-7", nil, nil, 0},
			{"bg_task.output_matched", "noisy", "info", "pi-bg-task", "bg-7", "ready", "ready\n", 2},
			{"bg_task.completed", "normal", "success", "pi-bg-task", "bg-7", nil, nil, 3},
		}, "events")
	})
}

func TestTwinBackgroundAck(t *testing.T) {
	tw(t, "background-ack", "background acknowledgement rows", func(t *testing.T) {
		limit := wakeManifestFieldMaxChars
		rows := []struct {
			name string
			huge bool
		}{
			{"huge acknowledgement fields retain bounded prefixes", true},
			{"small acknowledgement fields are unchanged", false},
		}
		pick := func(huge bool, big, small string) string {
			if huge {
				return big
			}
			return small
		}
		for _, row := range rows {
			s := fakeSnapshot(func(s *snapshot) {
				s.ID, s.Pid = "bg-log-1", 4242
				s.Command = pick(row.huge, repeat("C", 200_000), "echo log")
				s.Cwd = pick(row.huge, "/path/"+repeat("P", 5_000), "/path/work")
				s.LogFile = pick(row.huge, "/tmp/"+repeat("L", 5_000), "/tmp/log")
				s.NotifyOnOutput = true
				s.NotifyPattern = pick(row.huge, repeat("R", 5_000), "ready")
				s.DedupeKey = pick(row.huge, repeat("D", 5_000), "monitor")
			})
			text := bashBackgroundAckText(&s, bashBackgroundDecision{notifyOnExit: true, notifyOnOut: true, reason: "test", title: "test"})
			expected := "Started bg-log-1 (pid 4242) in the background.\nReason: test.\n" +
				"Command: " + pick(row.huge, repeat("C", limit-1)+"…", "echo log") + "\n" +
				"Cwd: " + pick(row.huge, "/path/"+repeat("P", limit-7)+"…", "/path/work") + "\n" +
				"Log: " + pick(row.huge, "/tmp/"+repeat("L", limit-6)+"…", "/tmp/log") + "\n" +
				"Wakeups: exit=yes, output=" + pick(row.huge, repeat("R", limit-1)+"…", "ready") + ", mode=always, dedupeKey=" + pick(row.huge, repeat("D", limit-1)+"…", "monitor") + "\n" +
				"Continue the turn without waiting. Use bg_task list/log/stop to inspect or terminate this task."
			var excluded []string
			for _, c := range []string{"C", "P", "L", "R", "D"} {
				if strings.Contains(text, repeat(c, limit+1)) {
					excluded = append(excluded, c)
				}
			}
			eq(t, [3]any{text, len(text) < 4096, len(excluded)}, [3]any{expected, true, 0}, row.name)
		}
	})
}

func TestTwinOutputMatcher(t *testing.T) {
	tw(t, "output-matcher", "a pathological notify regex on 1 MB reaches its deadline and reports only once", func(t *testing.T) {
		rows := []struct {
			pattern, text string
			want          *bool
		}{
			{"", "output", nil},
			{" ", "output", nil},
			{"READY", "ready", new(true)},
			{"/ready/i", "READY", new(true)},
			{"/ready/g", "ready", new(true)},
			{"/^ready/m", "waiting\nready", new(true)},
			{"/[/", "literal /[/", new(true)},
			{"/ready/", "waiting", new(false)},
		}
		for _, row := range rows {
			m := parseOutputMatcher(row.pattern)
			for range 2 {
				var got *bool
				if m != nil {
					v, err := m(row.text)
					if err != nil {
						t.Fatalf("%q: %v", row.pattern, err)
					}
					got = &v
				}
				eq(t, got, row.want, "pattern "+row.pattern)
			}
		}
		m := parseOutputMatcher("/(a+)+$/")
		mustTrue(t, m != nil, "the pathological pattern compiles")
		text := repeat("a", 1_000_000-1) + "!"
		start := time.Now()
		reports := 0
		_, err := m(text)
		if err == errMatcherBudget {
			reports++
		} else {
			t.Fatalf("want the matcher budget error, got %v", err)
		}
		elapsed := time.Since(start)
		// The real wait verifies the engine's own deadline, with room for CI scheduling.
		mustTrue(t, elapsed < 250*time.Millisecond, "matcher elapsed "+elapsed.String())
		for range 20 {
			v, err := m(text)
			if v || err != nil {
				t.Fatalf("a disabled matcher answers false without an error, got %v, %v", v, err)
			}
		}
		eq(t, [3]any{len(text), reports, 20}, [3]any{1_000_000, 1, 20}, "evidence")
	})
}
