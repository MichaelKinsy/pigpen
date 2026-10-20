package pi_goal_x_test

// Twins of tests/goal-refresh.test.ts (the pure refresh diff) and tests/goal-harness-shell.test.ts.

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const refreshSettings = `{"subtaskDepth":2,"provider":"anthropic"}`

// refreshDiff is diffGoalRefreshState(state(before), state(after)) with the original's defaults (g1, g2; 10 events; 0 malformed).
func refreshDiff(beforeIDs, afterIDs []string, afterEvents, afterMalformed int, afterSettings string) []string {
	if beforeIDs == nil {
		beforeIDs = []string{"g1", "g2"}
	}
	if afterIDs == nil {
		afterIDs = []string{"g1", "g2"}
	}
	return goal.DiffGoalRefreshState(beforeIDs, 10, 0, refreshSettings, afterIDs, afterEvents, afterMalformed, afterSettings)
}

func sameLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestGoalRefresh(t *testing.T) {
	const f = "goal-refresh"

	tw(t, f, "diffGoalRefreshState: unchanged state reports no changes", func(t *testing.T) {
		sameLines(t, refreshDiff(nil, nil, 10, 0, refreshSettings), nil)
	})

	tw(t, f, "diffGoalRefreshState: pool additions and removals are reported", func(t *testing.T) {
		sameLines(t, refreshDiff(nil, []string{"g1", "g2", "g3"}, 10, 0, refreshSettings), []string{"pool: 1 goal(s) added — g3"})
		sameLines(t, refreshDiff([]string{"g1", "g2"}, []string{"g2"}, 10, 0, refreshSettings), []string{"pool: 1 goal(s) removed — g1"})
		sameLines(t, refreshDiff([]string{"g1", "g2"}, []string{"g2", "g4"}, 10, 0, refreshSettings), []string{"pool: 1 goal(s) added — g4", "pool: 1 goal(s) removed — g1"})
	})

	tw(t, f, "diffGoalRefreshState: ledger growth and malformed-entry changes are reported", func(t *testing.T) {
		sameLines(t, refreshDiff(nil, nil, 13, 0, refreshSettings), []string{"ledger: 10 -> 13 events"})
		sameLines(t, refreshDiff(nil, nil, 10, 2, refreshSettings), []string{"ledger: malformed entries 0 -> 2"})
	})

	tw(t, f, "diffGoalRefreshState: settings fingerprint changes are reported", func(t *testing.T) {
		sameLines(t, refreshDiff(nil, nil, 10, 0, `{"subtaskDepth":3}`), []string{"settings: effective settings changed (external edit)"})
	})

	tw(t, f, "diffGoalRefreshState: combined changes are reported in order", func(t *testing.T) {
		sameLines(t, refreshDiff(nil, []string{"g1", "g3"}, 12, 1, `{"subtaskDepth":9}`), []string{
			"pool: 1 goal(s) added — g3",
			"pool: 1 goal(s) removed — g2",
			"ledger: 10 -> 12 events",
			"ledger: malformed entries 0 -> 1",
			"settings: effective settings changed (external edit)",
		})
	})
}

func TestGoalHarnessShell(t *testing.T) {
	tw(t, "goal-harness-shell", "experiment harness shell tests pass", func(t *testing.T) {
		// The harness parses SUPPORTED_CASES.json with jq (experiments/harness/lib.sh); the original's test needs it too.
		if err := exec.Command("jq", "--version").Run(); err != nil {
			t.Skip("jq is not installed or does not run: the harness shell script cannot resolve a case id without it")
		}
		script, err := filepath.Abs("../../port/oracle/tests/shell/harness.test.sh")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", script)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("harness shell tests failed: %v\n%s\n%s", err, out.String(), errOut.String())
		}
		if !strings.Contains(out.String(), "harness shell tests: all passed") {
			t.Errorf("stdout %q", out.String())
		}
	})
}
