package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func withRepo(t *testing.T, run func(repo string)) {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init")
	gitIn(t, repo, "config", "user.email", "test@example.com")
	gitIn(t, repo, "config", "user.name", "Test User")
	gitIn(t, repo, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	gitIn(t, repo, "add", "tracked.txt")
	gitIn(t, repo, "commit", "-m", "base")
	run(repo)
}

func TestTrackedMutationEvidence(t *testing.T) {
	tw(t, "mutation-evidence", "detects edits to a tracked file that was already dirty at child start", func(t *testing.T) {
		withRepo(t, func(repo string) {
			writeFile(t, filepath.Join(repo, "tracked.txt"), "dirty before child\n")
			snapshot := snapshotTrackedMutations(repo)
			writeFile(t, filepath.Join(repo, "tracked.txt"), "dirty after child\n")
			writeFile(t, filepath.Join(repo, "untracked.txt"), "ignored by evidence\n")
			ev := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, ev.AttemptedMutation, true, "attemptedMutation")
			equal(t, ev.ChangedFiles, []string{"tracked.txt"}, "changedFiles")
		})
	})
	tw(t, "mutation-evidence", "detects a child reverting a tracked file that was dirty at child start", func(t *testing.T) {
		withRepo(t, func(repo string) {
			writeFile(t, filepath.Join(repo, "tracked.txt"), "dirty before child\n")
			snapshot := snapshotTrackedMutations(repo)
			writeFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
			ev := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, ev.AttemptedMutation, true, "attemptedMutation")
			equal(t, ev.ChangedFiles, []string{"tracked.txt"}, "changedFiles")
		})
	})
	tw(t, "mutation-evidence", "detects staged edits to a tracked file that was already staged dirty at child start", func(t *testing.T) {
		withRepo(t, func(repo string) {
			writeFile(t, filepath.Join(repo, "tracked.txt"), "staged before child\n")
			gitIn(t, repo, "add", "tracked.txt")
			snapshot := snapshotTrackedMutations(repo)
			writeFile(t, filepath.Join(repo, "tracked.txt"), "staged after child\n")
			gitIn(t, repo, "add", "tracked.txt")
			ev := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, ev.AttemptedMutation, true, "attemptedMutation")
			equal(t, ev.ChangedFiles, []string{"tracked.txt"}, "changedFiles")
		})
	})
	tw(t, "mutation-evidence", "distinguishes unchanged and changed tracked diffs that exceed the fast buffer", func(t *testing.T) {
		withRepo(t, func(repo string) {
			writeFile(t, filepath.Join(repo, "tracked.txt"), strings.Repeat("before\n", 180000)+"before-tail\n")
			snapshot := snapshotTrackedMutations(repo)
			unchanged := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, unchanged.AttemptedMutation, false, "unchanged attemptedMutation")
			equal(t, unchanged.ChangedFiles, []string{}, "unchanged files")
			writeFile(t, filepath.Join(repo, "tracked.txt"), strings.Repeat("after\n", 180000)+"after-tail\n")
			ev := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, ev.AttemptedMutation, true, "attemptedMutation")
			equal(t, ev.ChangedFiles, []string{"tracked.txt"}, "changedFiles")
		})
	})
	tw(t, "mutation-evidence", "does not invoke repository fsmonitor while collecting mutation evidence", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the hook is a POSIX shell script")
		}
		withRepo(t, func(repo string) {
			marker := filepath.Join(repo, "fsmonitor-invoked")
			hook := filepath.Join(repo, "fsmonitor-hook.sh")
			writeFile(t, hook, "#!/bin/sh\necho invoked >> '"+marker+"'\n")
			if err := os.Chmod(hook, 0o755); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "config", "core.fsmonitor", hook)
			gitIn(t, repo, "status", "--short")
			if _, err := os.Stat(marker); err != nil {
				t.Skip("this git does not run the fsmonitor hook for status here")
			}
			os.Remove(marker)
			writeFile(t, filepath.Join(repo, "tracked.txt"), "dirty before child\n")
			snapshot := snapshotTrackedMutations(repo)
			writeFile(t, filepath.Join(repo, "tracked.txt"), "dirty after child\n")
			ev := collectTrackedMutationEvidence(snapshot, repo)
			equal(t, snapshot.Unavailable, "", "unavailable")
			equal(t, ev.AttemptedMutation, true, "attemptedMutation")
			if _, err := os.Stat(marker); err == nil {
				t.Error("fsmonitor was invoked")
			}
		})
	})
	tw(t, "mutation-evidence", "formats bounded timeout recovery data", func(t *testing.T) {
		s := buildTimeoutRecoverySummary(timeoutRecoveryInput{
			Termination: "timed-out",
			Evidence:    trackedMutationEvidence{ChangedFiles: []string{"tracked.txt"}, AttemptedMutation: true},
			CurrentTool: "edit", CurrentPath: "tracked.txt", SessionFile: "session.jsonl",
		})
		equal(t, s.ChangedFiles, []string{"tracked.txt"}, "changedFiles")
		polMatches(t, s.Message, `termination: timed-out`)
		polMatches(t, s.Message, `changed tracked files: tracked\.txt`)
		polMatches(t, s.Message, `active tool: edit`)
	})
	tw(t, "mutation-evidence", "classifies dirty timeouts with a missing requested report as recovery-needed", func(t *testing.T) {
		var files []string
		for i := 1; i <= 25; i++ {
			files = append(files, "src/file-"+pad2(i)+".ts")
		}
		missing := true
		s := buildTimeoutRecoverySummary(timeoutRecoveryInput{
			Termination: "timed-out", Evidence: trackedMutationEvidence{ChangedFiles: files, AttemptedMutation: true}, RequiredOutputMissing: &missing,
		})
		equal(t, len(s.ChangedFiles), 20, "kept files")
		equal(t, s.ChangedFiles[:2], []string{"src/file-01.ts", "src/file-02.ts"}, "first files")
		equal(t, s.Truncated, true, "truncated")
		polMatches(t, s.Message, `\.\.\. \(5 more\)`)
		polNotMatches(t, s.Message, `src/file-25\.ts`)
		equal(t, s.RecoveryNeeded, true, "recoveryNeeded")
		equal(t, s.Reason, "timed-out-with-dirty-worktree", "reason")
		equal(t, s.ReportStatus, "missing", "reportStatus")
		polMatches(t, s.Message, `Recovery needed`, "i")
		polMatches(t, s.Message, `requested report: missing`, "i")
		polMatches(t, s.Message, `review (?:the )?diff and artifacts before resuming`, "i")
		polMatches(t, s.Message, `dependent stages`, "i")
	})
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
