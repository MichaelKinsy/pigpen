package pi_subagents_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A launch that fails before a child starts (the working directory is not usable) is a failed tool call carrying the run's
// own lines: the fan-out budget, the background run's name, the mission. Twin of the pigeq scenario run-launch-failure
// (Pi shows these results with details `{}`; the port's tool error matches).
func TestLaunchFailuresAreFailedToolCalls(t *testing.T) {
	calls := fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "plain.md"), agentMD("plain", "Plain", "You help."))
	writeFile(t, filepath.Join(cwd, "flow.js"), "return 1;")
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})
	missing := "Subagent launch aborted: cwd does not exist: " + filepath.Join(cwd, "nope") + "\n(resolved from \"nope\")"
	file := "Subagent launch aborted: cwd is not a directory: " + filepath.Join(cwd, "flow.js") + "\n(resolved from \"flow.js\")"
	run := func(extra map[string]any) string {
		t.Helper()
		args := map[string]any{"agent": "plain", "task": "do the work", "async": false}
		for k, v := range extra {
			args[k] = v
		}
		raw, failure := h.Tool("subagent", args)
		if failure == "" {
			t.Fatalf("not a failed call: %s", raw)
		}
		return failure
	}
	// (a run in a git checkout is also tracked in a mission, whose line closes the text; the scenario has no repository)
	for _, c := range []struct{ name, cwd, want string }{{"missing directory", "nope", missing}, {"a file", "flow.js", file}} {
		if got := run(map[string]any{"cwd": c.cwd}); !strings.HasPrefix(got, "Run fan-out: 1/64 used, 63 remaining\n"+c.want) {
			t.Errorf("foreground, %s: %q", c.name, got)
		}
	}
	got := run(map[string]any{"cwd": "nope", "mission": map[string]any{"title": "Goal"}})
	if !strings.HasPrefix(got, "Run fan-out: 1/64 used, 63 remaining\n"+missing+"\nMission: ") || !strings.HasSuffix(got, " (failed)") {
		t.Errorf("mission: %q", got)
	}
	got = run(map[string]any{"cwd": "nope", "async": true})
	if !strings.HasPrefix(got, "Run fan-out: 1/64 used, 63 remaining\nFailed to start async run '") || !strings.HasSuffix(got, "': "+missing) {
		t.Errorf("background: %q", got)
	}
	eq(t, len(*calls), 0, "no child started")
}
