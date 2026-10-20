package tintinweb_subagents

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Twins of test/env.test.ts: detectEnv against real git, through the same seam the extension uses (a git runner).
func TestDetectEnvTwins(t *testing.T) {
	const f = "env"
	realGit := func(args []string, cwd string, _ int) (string, int, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return "", ee.ExitCode(), nil
			}
			return "", 1, nil
		}
		return string(out), 0, nil
	}
	tw(t, f, "detects git repo in current project", func(t *testing.T) {
		wd, _ := os.Getwd()
		env := detectEnvWith(realGit, wd) // the pigpen checkout is a git repository, as the original's own is
		eq(t, env.IsGitRepo, true)
		eq(t, env.Platform, platformName(goosForTest()))
	})
	tw(t, f, "returns branch name when on a branch", func(t *testing.T) {
		dir := t.TempDir()
		for _, args := range [][]string{{"init"}, {"config", "user.email", "test@test.com"}, {"config", "user.name", "Test"}, {"checkout", "-b", "test-branch"}, {"commit", "--allow-empty", "-m", "init"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
			}
		}
		env := detectEnvWith(realGit, dir)
		eq(t, env.IsGitRepo, true)
		eq(t, env.Branch, "test-branch")
	})
	tw(t, f, "detects non-git directory", func(t *testing.T) {
		env := detectEnvWith(realGit, t.TempDir())
		eq(t, env.IsGitRepo, false)
		eq(t, env.Branch, "")
		eq(t, env.Platform, platformName(goosForTest()))
	})
}
