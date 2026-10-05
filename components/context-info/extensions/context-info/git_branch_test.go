package contextinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentGitBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "topic")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if got := currentGitBranch(filepath.Join(dir, "subdir")); got != "" {
		t.Fatalf("nonexistent subdir branch = %q, want empty", got)
	}
	if got := currentGitBranch(dir); got != "topic" {
		t.Fatalf("branch = %q, want topic", got)
	}
}

func TestCurrentGitBranch_UsesShortCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "topic")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	gitCacheMu.Lock()
	gitCacheCWD = ""
	gitCacheBranch = ""
	gitCacheAt = time.Time{}
	gitCacheMu.Unlock()

	if got := currentGitBranch(dir); got != "topic" {
		t.Fatalf("initial branch = %q, want topic", got)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("remove .git: %v", err)
	}
	if got := currentGitBranch(dir); got != "topic" {
		t.Fatalf("cached branch = %q, want topic", got)
	}
}

func TestCurrentGitBranch_NonRepo(t *testing.T) {
	if got := currentGitBranch(t.TempDir()); got != "" {
		t.Fatalf("non-repo branch = %q, want empty", got)
	}
}
