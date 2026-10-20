package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/watchdog-diff-tool.test.ts.

func wdDiffRepo(t *testing.T) string {
	t.Helper()
	repo := tmp(t)
	wdGit(t, repo, "init", "-q")
	wdGit(t, repo, "config", "user.email", "test@example.com")
	wdGit(t, repo, "config", "user.name", "Test User")
	wdGit(t, repo, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(repo, "src", "a.ts"), "export const a = 1;\n")
	writeFile(t, filepath.Join(repo, "README.md"), "readme\n")
	wdGit(t, repo, "add", ".")
	wdGit(t, repo, "commit", "-q", "-m", "base")
	return repo
}

func wdRunDiff(t *testing.T, tool *watchdogDiffTool, params watchdogDiffParams) string {
	t.Helper()
	text, err := tool.execute(params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return text
}

func TestWatchdogDiffToolTwins(t *testing.T) {
	const f = "watchdog-diff-tool"
	tw(t, f, "captures the session baseline and reports no changes when clean", func(t *testing.T) {
		repo := wdDiffRepo(t)
		baseline := captureWatchdogDiffBaseline(filepath.Join(repo, "src"))
		if baseline == nil {
			t.Fatal("no baseline")
		}
		wdEq(t, wdComparablePath(baseline.Root), wdComparablePath(repo), "root")
		wdEq(t, baseline.Ref, wdGit(t, repo, "rev-parse", "HEAD"), "ref")
		wdMatch(t, wdRunDiff(t, createWatchdogDiffTool(baseline, false), watchdogDiffParams{}), `^No changes since baseline`)
		if captureWatchdogDiffBaseline(os.TempDir()) != nil {
			t.Error("a baseline outside any repository")
		}
	})
	tw(t, f, "shows tracked changes since the baseline, including later commits, and lists untracked paths", func(t *testing.T) {
		repo := wdDiffRepo(t)
		baseline := captureWatchdogDiffBaseline(repo)
		writeFile(t, filepath.Join(repo, "src", "a.ts"), "export const a = 2;\n")
		wdGit(t, repo, "commit", "-q", "-am", "child commit")
		writeFile(t, filepath.Join(repo, "src", "b.ts"), "export const b = 1;\n")
		tool := createWatchdogDiffTool(baseline, false)
		full := wdRunDiff(t, tool, watchdogDiffParams{})
		wdMatch(t, full, `-export const a = 1;`)
		wdMatch(t, full, `\+export const a = 2;`)
		wdMatch(t, full, `Untracked files \(use read to inspect\):\n src/b\.ts`)
		wdNoMatch(t, full, `export const b = 1;`)
		stat := wdRunDiff(t, tool, watchdogDiffParams{Stat: true})
		wdMatch(t, stat, `src/a\.ts \|`)
		wdMatch(t, stat, `Untracked files \(use read to inspect\):\n src/b\.ts`)
		narrowed := wdRunDiff(t, tool, watchdogDiffParams{Path: "README.md"})
		wdNoMatch(t, narrowed, `a\.ts|b\.ts`)
	})
	tw(t, f, "shows staged and unstaged launch-HEAD changes while honestly excluding earlier commits", func(t *testing.T) {
		repo := wdDiffRepo(t)
		writeFile(t, filepath.Join(repo, "README.md"), "committed before reviewer launch\n")
		wdGit(t, repo, "commit", "-q", "-am", "review target already committed")
		baseline := captureWatchdogDiffBaseline(repo)
		tool := createWatchdogDiffTool(baseline, true)
		clean := wdRunDiff(t, tool, watchdogDiffParams{})
		wdMatch(t, clean, `^No working-tree changes against reviewer-launch HEAD`)
		wdMatch(t, clean, `Committed changes are not included\.`)
		wdNoMatch(t, clean, `committed before reviewer launch`)
		wdMatch(t, tool.Description, `Committed ranges are not included`)
		writeFile(t, filepath.Join(repo, "src", "a.ts"), "export const a = 2;\n")
		wdGit(t, repo, "add", "src/a.ts")
		writeFile(t, filepath.Join(repo, "README.md"), "unstaged after reviewer launch\n")
		writeFile(t, filepath.Join(repo, "untracked.txt"), "inspect with read\n")
		changed := wdRunDiff(t, tool, watchdogDiffParams{})
		wdMatch(t, changed, `\+export const a = 2;`)
		wdMatch(t, changed, `\+unstaged after reviewer launch`)
		wdMatch(t, changed, `Untracked files \(use read to inspect\):\n untracked\.txt`)
		wdEq(t, wdGit(t, repo, "rev-parse", "HEAD"), baseline.Ref, "inspection does not move HEAD")
		status := wdGit(t, repo, "status", "--short")
		wdEq(t, strings.Split(status, "\n"), []string{" M README.md", "M  src/a.ts", "?? untracked.txt"}, "inspection preserves staged, unstaged, and untracked state")
	})
	tw(t, f, "fails closed when HEAD advances after reviewer launch", func(t *testing.T) {
		repo := wdDiffRepo(t)
		baseline := captureWatchdogDiffBaseline(repo)
		tool := createWatchdogDiffTool(baseline, true)
		writeFile(t, filepath.Join(repo, "README.md"), "committed after reviewer launch\n")
		wdGit(t, repo, "commit", "-q", "-am", "advance HEAD")
		result := wdRunDiff(t, tool, watchdogDiffParams{})
		wdMatch(t, result, `^Reviewer-launch HEAD changed or is unavailable\.`)
		wdMatch(t, result, `Committed changes are unsupported; relaunch the reviewer or supply a diff artifact\.`)
		wdNoMatch(t, result, `committed after reviewer launch`)
	})
	tw(t, f, "rejects traversal and option-like paths and bounds large diffs", func(t *testing.T) {
		repo := wdDiffRepo(t)
		baseline := captureWatchdogDiffBaseline(repo)
		tool := createWatchdogDiffTool(baseline, false)
		for _, c := range []struct{ path, want string }{{"../etc", `must not contain '\.\.'`}, {"-R", `must not start with '-'`}, {"/etc/hosts", `relative`}} {
			_, err := tool.execute(watchdogDiffParams{Path: c.path})
			if err == nil {
				t.Errorf("path %q accepted", c.path)
				continue
			}
			wdMatch(t, err.Error(), c.want)
		}
		writeFile(t, filepath.Join(repo, "src", "a.ts"), strings.Repeat("export const line = 1;\n", 3000))
		bounded := wdRunDiff(t, tool, watchdogDiffParams{})
		wdTrue(t, wdLen(bounded) <= watchdogDiffMaxChars, "bounded output")
		wdMatch(t, bounded, `characters omitted; call again with a narrower path`)
		for i := 0; i < 52; i++ {
			writeFile(t, filepath.Join(repo, "untracked-"+string(rune('0'+i/10))+string(rune('0'+i%10))+".txt"), "x")
		}
		inventory := wdRunDiff(t, tool, watchdogDiffParams{Stat: true})
		wdMatch(t, inventory, `\.\.\. 2 more untracked files`)
		wdEq(t, len(regexp.MustCompile(`(?m)^ untracked-`).FindAllString(inventory, -1)), 50, "listed untracked files")
	})
}
