package pi_goal_x

// Twins of tests/no-status-refresh-timer.test.ts and tests/session-state-growth.test.ts. The originals read the source of
// goal-state.ts; the Go kernel is core.go, so the same constructs are looked for there under their Go spellings.

import (
	"os"
	"regexp"
	"testing"
)

func kernelSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("core.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func srcMatches(t *testing.T, source, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(source) {
		t.Errorf("core.go does not match /%s/", re)
	}
}

func srcNotMatches(t *testing.T, source, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(source) {
		t.Errorf("core.go matches /%s/", re)
	}
}

func TestNoStatusRefreshTimer(t *testing.T) {
	f := "no-status-refresh-timer"
	tw(t, f, "no periodic status-refresh timer is scheduled while goals are active", func(t *testing.T) {
		// PR #5 removed the 1s status refresh interval that forced TUI redraws and pulled users out of terminal scrollback.
		source := kernelSource(t)
		for _, name := range []string{"STATUS_REFRESH_MS", "statusRefreshTimer", "syncStatusRefresh", "stopStatusRefresh", "statusRefreshMs", "StatusRefresh"} {
			srcNotMatches(t, source, name)
		}
	})
	tw(t, f, "the widget and footer status still update on state changes", func(t *testing.T) {
		source := kernelSource(t)
		srcMatches(t, source, `widgetRef\.current\.update\(\)`)
		srcMatches(t, source, `c\.h\.SetStatus\(widgetKey`)
	})
	tw(t, f, "the focused-goal footer line is removed; the widget is the single home", func(t *testing.T) {
		source := kernelSource(t)
		srcNotMatches(t, source, `footerStatus\(displayGoal\)`)
		srcMatches(t, source, `c\.h\.SetStatus\(widgetKey, ""\)`)
		srcMatches(t, source, `goal: unfocused`)
	})
}

func TestSessionStateGrowth(t *testing.T) {
	tw(t, "session-state-growth", "goal updates do not append full state snapshots to the session", func(t *testing.T) {
		source := kernelSource(t)
		srcNotMatches(t, source, `AppendEntry\(stateEntryType`)
		srcMatches(t, source, `ct == stateEntryType`)
	})
}
