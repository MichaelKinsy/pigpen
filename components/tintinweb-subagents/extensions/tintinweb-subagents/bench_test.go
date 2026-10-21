package tintinweb_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkWorktreeRoundTrip is the latency of an isolated agent's worktree with real git: create, make a change,
// commit it to a branch and remove the copy (what an isolated agent costs beyond the agent itself). The original's
// timings are not comparable: it only runs under Node, with the same git calls through pi.exec.
func BenchmarkWorktreeRoundTrip(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git is not installed")
	}
	repo := b.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if r := osGit(args, repo, 10000); r.Code != 0 {
			b.Fatal(r.Stderr)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# r\n"), 0o666); err != nil {
		b.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-q", "-m", "initial"}} {
		if r := osGit(args, repo, 10000); r.Code != 0 {
			b.Fatal(r.Stderr)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := "bench" + strconv.Itoa(i)
		wt := createWorktree(osGit, repo, id)
		if wt == nil {
			b.Fatal("no worktree")
		}
		if err := os.WriteFile(filepath.Join(wt.Path, "f.txt"), []byte("x"), 0o666); err != nil {
			b.Fatal(err)
		}
		if res := cleanupWorktree(osGit, repo, wt, "bench"); !res.HasChanges {
			b.Fatal("changes lost")
		}
	}
}

// BenchmarkBuildMemoryBlock is the cost of the memory section of a system prompt (a 200-line MEMORY.md).
func BenchmarkBuildMemoryBlock(b *testing.B) {
	cwd := b.TempDir()
	dir := filepath.Join(cwd, ".pi", "agent-memory", "auditor")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(strings.Repeat("- a memory line\n", 300)), 0o666); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildMemoryBlock("auditor", "project", cwd); err != nil {
			b.Fatal(err)
		}
	}
}
