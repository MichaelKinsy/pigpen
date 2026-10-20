package warden

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// The git-state cases run real git against a real repository, like the original's (git-state.test.ts).

var gitRepo struct {
	once          sync.Once
	repo, notRepo string
	err           error
}

func gitIn(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func gitFixture(t *testing.T) (repo, notRepo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	gitRepo.once.Do(func() {
		base, err := os.MkdirTemp("", "pi-warden-git-state-")
		if err != nil {
			gitRepo.err = err
			return
		}
		gitRepo.repo, gitRepo.notRepo = filepath.Join(base, "repo"), filepath.Join(base, "no-git")
		for _, d := range []string{gitRepo.repo, gitRepo.notRepo} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				gitRepo.err = err
				return
			}
		}
		if err := os.WriteFile(filepath.Join(gitRepo.repo, "a.txt"), []byte("one\n"), 0o600); err != nil {
			gitRepo.err = err
			return
		}
		for _, args := range [][]string{
			{"init", "-q", "-b", "feature"}, {"config", "push.default", "simple"}, {"add", "a.txt"}, {"commit", "-q", "-m", "one"},
			// A remote whose HEAD is `trunk`, set up from local refs only: no network.
			{"remote", "add", "origin", "https://example.invalid/repo.git"},
			{"update-ref", "refs/remotes/origin/trunk", "HEAD"}, {"update-ref", "refs/remotes/origin/feature", "HEAD"},
			{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"},
		} {
			if err := gitIn(gitRepo.repo, args...); err != nil {
				gitRepo.err = err
				return
			}
		}
	})
	if gitRepo.err != nil {
		t.Fatal(gitRepo.err)
	}
	return gitRepo.repo, gitRepo.notRepo
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	if err := gitIn(repo, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func severityIn(command, dir, id string) Severity {
	hit, _ := hitByID(MatchPatterns("bash", map[string]any{"command": command}, dir, nil), id)
	return hit.Severity
}

// twin: tests/git-state.test.ts:33 "git reset --hard warns on a clean tree and holds with uncommitted changes"
func TestGitResetHardWarnsOnACleanTree(t *testing.T) {
	repo, _ := gitFixture(t)
	if !cleanHardReset("git reset --hard HEAD~1", repo, runGit) || severityIn("git reset --hard HEAD~1", repo, "git-reset-hard") != SeverityRisky {
		t.Fatal("clean tree")
	}
	a := filepath.Join(repo, "a.txt")
	os.WriteFile(a, []byte("changed\n"), 0o600)
	if severityIn("git reset --hard HEAD~1", repo, "git-reset-hard") != SeverityDestructive {
		t.Fatal("uncommitted changes")
	}
	os.WriteFile(a, []byte("one\n"), 0o600)
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("untracked\n"), 0o600)
	if severityIn("git reset --hard", repo, "git-reset-hard") != SeverityDestructive {
		t.Fatal("an untracked file counts as a change")
	}
	os.Remove(filepath.Join(repo, "new.txt"))
	if severityIn("git reset --hard", repo, "git-reset-hard") != SeverityRisky {
		t.Fatal("clean again")
	}
}

// twin: tests/git-state.test.ts:45 "git reset --hard holds when the status check fails or the command is not plain"
func TestGitResetHardHoldsWhenTheCheckFailsOrTheCommandIsNotPlain(t *testing.T) {
	repo, notRepo := gitFixture(t)
	if severityIn("git reset --hard", notRepo, "git-reset-hard") != SeverityDestructive {
		t.Fatal("git status fails outside a repository")
	}
	if severityIn("git reset --hard", filepath.Join(repo, "missing"), "git-reset-hard") != SeverityDestructive {
		t.Fatal("git status fails in a missing directory")
	}
	if severityIn("git reset --hard", "", "git-reset-hard") != SeverityDestructive {
		t.Fatal("no cwd")
	}
	for _, command := range []string{"cd other && git reset --hard", "git reset --hard $REF", "git reset --hard; rm x"} {
		if severityIn(command, repo, "git-reset-hard") != SeverityDestructive {
			t.Errorf("%s", command)
		}
	}
}

// twin: tests/git-state.test.ts:54 "git reset --hard with -C, --git-dir, or --work-tree holds, even on a clean tree"
func TestGitResetHardWithRepositoryOptionsHolds(t *testing.T) {
	repo, _ := gitFixture(t)
	if severityIn("git reset --hard", repo, "git-reset-hard") != SeverityRisky {
		t.Fatal("the tree is clean")
	}
	for _, command := range []string{
		"git -C " + repo + " reset --hard", "git -C . reset --hard HEAD~1", `git -C "` + repo + `" reset --hard`, "git --git-dir=.git reset --hard",
		"git --git-dir .git reset --hard", "git --work-tree=. reset --hard", "git --work-tree . --git-dir=.git reset --hard",
		"git -c core.quotepath=off reset --hard", "git --no-pager -C . reset --hard",
	} {
		if severityIn(command, repo, "git-reset-hard") != SeverityDestructive {
			t.Errorf("%s", command)
		}
	}
	for _, command := range []string{"git -C . reset --soft HEAD~1", "git -C . log --oneline", `grep -rn "git -C x reset --hard" docs/`} {
		if s := severityIn(command, repo, "git-reset-hard"); s != "" {
			t.Errorf("%s: %s", command, s)
		}
	}
}

// twin: tests/git-state.test.ts:67 "git push --force and --force-with-lease with -C, --git-dir, or --work-tree hold"
func TestGitPushForceWithRepositoryOptionsHolds(t *testing.T) {
	repo, _ := gitFixture(t)
	if severityIn("git push --force-with-lease origin feature", repo, "git-force-with-lease") != SeverityRisky {
		t.Fatal("the plain form warns")
	}
	for _, options := range []string{"-C " + repo, "-C .", `-C "` + repo + `"`, "--git-dir=.git", "--git-dir .git", "--work-tree=.", "-c push.default=simple", "--no-pager -C ."} {
		lease := "git " + options + " push --force-with-lease origin feature"
		if severityIn(lease, repo, "git-force-with-lease") != SeverityDestructive {
			t.Errorf("%s", lease)
		}
		for _, force := range []string{"--force", "-f"} {
			command := "git " + options + " push " + force + " origin feature"
			if severityIn(command, repo, "git-force-push") != SeverityDestructive {
				t.Errorf("%s", command)
			}
		}
	}
	for _, command := range []string{"git -C . push origin feature", "git -C . log --force-with-lease", `grep -rn "git -C x push --force" docs/`} {
		ids := hitIDs(MatchPatterns("bash", map[string]any{"command": command}, repo, nil))
		if contains(ids, "git-force-push") || contains(ids, "git-force-with-lease") {
			t.Errorf("%s: %v", command, ids)
		}
	}
}

// twin: tests/git-state.test.ts:83 "git push --force-with-lease warns only for a named or current branch that is not the default"
func TestGitLeasePushWarnsOnlyForANonDefaultBranch(t *testing.T) {
	repo, _ := gitFixture(t)
	for _, command := range []string{"git push --force-with-lease", "git push --force-with-lease origin feature", "git push --force-with-lease origin HEAD", "git push -u --force-with-lease origin feature", "git push --force-with-lease=feature origin HEAD:refs/heads/feature"} {
		if !safeLeasePush(command, repo, runGit) || severityIn(command, repo, "git-force-with-lease") != SeverityRisky {
			t.Errorf("%s", command)
		}
	}
}

// twin: tests/git-state.test.ts:90 "git push --force-with-lease holds for the default branch, a plain force, and a failed check"
func TestGitLeasePushHoldsForTheDefaultBranch(t *testing.T) {
	repo, notRepo := gitFixture(t)
	for _, command := range []string{
		"git push --force-with-lease origin main", "git push --force-with-lease origin master", "git push --force-with-lease origin trunk",
		"git push --force-with-lease origin feature:main", "git push --force-with-lease origin refs/heads/main",
		"git push --force --force-with-lease origin feature", "git push -f --force-with-lease origin feature", "git push -uf --force-with-lease origin feature",
		"git push --force-with-lease origin +feature", "git push --force-with-lease origin :feature", "git push --force-with-lease --all origin",
		"git push --force-with-lease --mirror origin", "git push --force-with-lease origin feature main", "git push --force-with-lease git@host:o/r.git feature",
		"git push --force-with-lease origin refs/tags/v1", "cd x && git push --force-with-lease origin feature",
	} {
		if severityIn(command, repo, "git-force-with-lease") != SeverityDestructive {
			t.Errorf("%s", command)
		}
	}
	if severityIn("git push --force-with-lease origin feature", notRepo, "git-force-with-lease") != SeverityDestructive {
		t.Error("the remote check fails outside a repository")
	}
	if severityIn("git push --force-with-lease origin feature", "", "git-force-with-lease") != SeverityDestructive {
		t.Error("no cwd")
	}
}

// twin: tests/git-state.test.ts:104 "a bare lease push holds when the current branch tracks the default branch or push.default is matching"
func TestABareLeasePushHoldsWhenTheBranchTracksTheDefault(t *testing.T) {
	repo, _ := gitFixture(t)
	git(t, repo, "config", "push.default", "matching")
	if safeLeasePush("git push --force-with-lease", repo, runGit) {
		t.Fatal("push.default matching")
	}
	git(t, repo, "config", "push.default", "upstream")
	git(t, repo, "config", "branch.feature.remote", "origin")
	git(t, repo, "config", "branch.feature.merge", "refs/heads/trunk")
	if safeLeasePush("git push --force-with-lease", repo, runGit) {
		t.Fatal("the upstream is the remote HEAD branch")
	}
	git(t, repo, "config", "branch.feature.merge", "refs/heads/feature")
	if !safeLeasePush("git push --force-with-lease", repo, runGit) {
		t.Fatal("a feature upstream")
	}
	git(t, repo, "config", "push.default", "simple")
	git(t, repo, "checkout", "-q", "--detach")
	defer git(t, repo, "checkout", "-q", "feature")
	if safeLeasePush("git push --force-with-lease", repo, runGit) || safeLeasePush("git push --force-with-lease origin HEAD", repo, runGit) {
		t.Fatal("a detached HEAD has no current branch")
	}
}
