package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// upstream: packages/coding-agent/test/workspace-tree.test.ts

func writeFileWithMtime(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func touchDirWithMtime(t *testing.T, p string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func wsLineIndex(rendered, needle string) int {
	for i, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func TestBuildWorkspaceTree(t *testing.T) {
	t.Run("sorts files and directories together by modification time", func(t *testing.T) {
		cwd := t.TempDir()
		base := time.Now().Add(-60 * time.Second).Truncate(time.Second)
		recentDir := filepath.Join(cwd, "recent-dir")
		staleDir := filepath.Join(cwd, "stale-dir")
		touchDirWithMtime(t, recentDir, base.Add(3*time.Second))
		touchDirWithMtime(t, staleDir, base)
		writeFileWithMtime(t, filepath.Join(cwd, "newest.txt"), "newest", base.Add(4*time.Second))
		writeFileWithMtime(t, filepath.Join(cwd, "old.txt"), "old", base.Add(2*time.Second))
		touchDirWithMtime(t, recentDir, base.Add(3*time.Second))
		touchDirWithMtime(t, staleDir, base)

		tree := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		if wsLineIndex(tree.Rendered, "newest.txt") < 0 {
			t.Fatalf("missing newest.txt:\n%s", tree.Rendered)
		}
		if wsLineIndex(tree.Rendered, "recent-dir/") <= wsLineIndex(tree.Rendered, "newest.txt") {
			t.Fatalf("expected recent-dir/ after newest.txt:\n%s", tree.Rendered)
		}
		if wsLineIndex(tree.Rendered, "old.txt") <= wsLineIndex(tree.Rendered, "recent-dir/") {
			t.Fatalf("expected old.txt after recent-dir/:\n%s", tree.Rendered)
		}
		if wsLineIndex(tree.Rendered, "stale-dir/") <= wsLineIndex(tree.Rendered, "old.txt") {
			t.Fatalf("expected stale-dir/ after old.txt:\n%s", tree.Rendered)
		}
	})

	t.Run("keeps newest entries, truncation marker, and oldest entry per directory", func(t *testing.T) {
		cwd := t.TempDir()
		base := time.Now().Add(-60 * time.Second).Truncate(time.Second)
		for i := range 13 {
			name := fmt.Sprintf("file-%02d.txt", i)
			writeFileWithMtime(t, filepath.Join(cwd, name), name, base.Add(time.Duration(13-i)*time.Second))
		}

		tree := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		if !tree.Truncated {
			t.Fatalf("expected Truncated=true")
		}
		for _, want := range []string{"… 1 more", "file-00.txt", "file-10.txt", "file-12.txt"} {
			if !strings.Contains(tree.Rendered, want) {
				t.Fatalf("missing %q:\n%s", want, tree.Rendered)
			}
		}
		if strings.Contains(tree.Rendered, "file-11.txt") {
			t.Fatalf("file-11.txt should have been elided:\n%s", tree.Rendered)
		}
		if wsLineIndex(tree.Rendered, "… 1 more") >= wsLineIndex(tree.Rendered, "file-12.txt") {
			t.Fatalf("marker should precede oldest entry:\n%s", tree.Rendered)
		}
	})

	t.Run("enforces depth cap and skips hidden, excluded, and gitignored paths including char classes and ancestor gitignore", func(t *testing.T) {
		parent := t.TempDir()
		// Parent is a git repo root with an ancestor .gitignore and .git/info/exclude.
		if err := os.MkdirAll(filepath.Join(parent, ".git", "info"), 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(-time.Second)
		writeFileWithMtime(t, filepath.Join(parent, ".gitignore"), "ancestor-ignored.txt\n*.py[cod]\n", now)
		writeFileWithMtime(t, filepath.Join(parent, ".git", "info", "exclude"), "excluded-by-info.txt\n", now)

		cwd := filepath.Join(parent, "subproj")
		writeFileWithMtime(t, filepath.Join(cwd, ".gitignore"), "ignored.txt\nignored-dir/\na/b/ignored-nested.txt\n", now)
		writeFileWithMtime(t, filepath.Join(cwd, ".ignore"), "!ignored.txt\n", now) // .ignore beats .gitignore
		writeFileWithMtime(t, filepath.Join(cwd, "kept.txt"), "kept", now)
		writeFileWithMtime(t, filepath.Join(cwd, "ignored.txt"), "unignored-by-dot-ignore", now)
		writeFileWithMtime(t, filepath.Join(cwd, "ancestor-ignored.txt"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "excluded-by-info.txt"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "mod.pyc"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "mod.py"), "kept-py", now)
		writeFileWithMtime(t, filepath.Join(cwd, "ignored-dir", "child.txt"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "node_modules", "pkg.js"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "dist", "out.js"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd, "a", "b", "c", "d", "deep.txt"), "deep", now)
		writeFileWithMtime(t, filepath.Join(cwd, "a", "b", "ignored-nested.txt"), "ignored", now)

		tree := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		for _, want := range []string{"kept.txt", "ignored.txt", "mod.py", "c/"} {
			if !strings.Contains(tree.Rendered, want) {
				t.Fatalf("missing %q:\n%s", want, tree.Rendered)
			}
		}
		for _, absent := range []string{"ancestor-ignored.txt", "excluded-by-info.txt", "mod.pyc", "ignored-dir", "ignored-nested.txt", "node_modules", "dist", "d/", "deep.txt"} {
			if strings.Contains(tree.Rendered, absent) {
				t.Fatalf("unexpected %q in workspace tree:\n%s", absent, tree.Rendered)
			}
		}
	})

	t.Run("caps rendered tree at hard 120 line limit", func(t *testing.T) {
		cwd := t.TempDir()
		base := time.Now().Add(-60 * time.Second).Truncate(time.Second)
		for d := range 12 {
			dirPath := filepath.Join(cwd, fmt.Sprintf("dir-%02d", d))
			for f := range 12 {
				writeFileWithMtime(t, filepath.Join(dirPath, fmt.Sprintf("file-%02d.txt", f)), "x", base.Add(time.Duration(f)*time.Millisecond))
			}
			touchDirWithMtime(t, dirPath, base.Add(time.Duration(d)*time.Millisecond))
		}

		tree := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		lines := strings.Split(tree.Rendered, "\n")
		if !tree.Truncated || tree.TotalLines > 120 || len(lines) > 120 || !strings.Contains(tree.Rendered, "ln elided…]") {
			t.Fatalf("expected capped tree (<=120 lines), got truncated=%v total=%d len=%d", tree.Truncated, tree.TotalLines, len(lines))
		}
	})

	t.Run("returns AGENTS.md at directory depths 1 through 4 and surfaces gitignored AGENTS.md", func(t *testing.T) {
		cwd := t.TempDir()
		now := time.Now().Add(-time.Second)
		writeFileWithMtime(t, filepath.Join(cwd, "AGENTS.md"), "root", now)
		writeFileWithMtime(t, filepath.Join(cwd, "one", "AGENTS.md"), "d1", now)
		writeFileWithMtime(t, filepath.Join(cwd, "one", "two", "AGENTS.md"), "d2", now)
		writeFileWithMtime(t, filepath.Join(cwd, "one", "two", "three", "AGENTS.md"), "d3", now)
		writeFileWithMtime(t, filepath.Join(cwd, "one", "two", "three", "four", "AGENTS.md"), "d4", now)
		writeFileWithMtime(t, filepath.Join(cwd, "one", "two", "three", "four", "five", "AGENTS.md"), "too deep", now)

		tree := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		want := []string{
			"one/AGENTS.md",
			"one/two/AGENTS.md",
			"one/two/three/AGENTS.md",
			"one/two/three/four/AGENTS.md",
		}
		if strings.Join(tree.AgentsMdFiles, ",") != strings.Join(want, ",") {
			t.Fatalf("AgentsMdFiles = %v, want %v", tree.AgentsMdFiles, want)
		}

		// Gitignored file-level AGENTS.md still surfaced, ignored dir pruned.
		cwd2 := t.TempDir()
		writeFileWithMtime(t, filepath.Join(cwd2, ".gitignore"), "src/AGENTS.md\nignored-dir/\n", now)
		writeFileWithMtime(t, filepath.Join(cwd2, "src", "AGENTS.md"), "src rules", now)
		writeFileWithMtime(t, filepath.Join(cwd2, "src", "main.go"), "package main", now)
		writeFileWithMtime(t, filepath.Join(cwd2, "ignored-dir", "AGENTS.md"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd2, "node_modules", "pkg", "AGENTS.md"), "ignored", now)
		writeFileWithMtime(t, filepath.Join(cwd2, ".hidden", "AGENTS.md"), "ignored", now)

		tree2 := BuildWorkspaceTree(cwd2, BuildWorkspaceTreeOptions{})
		if strings.Join(tree2.AgentsMdFiles, ",") != "src/AGENTS.md" {
			t.Fatalf("AgentsMdFiles = %v, want [src/AGENTS.md]", tree2.AgentsMdFiles)
		}
	})

	t.Run("byte-exact fixture matching omp v19.1.1 locale tie-break, half-up bytes, UTF-16 alignment, and symlink 0B", func(t *testing.T) {
		cwd := t.TempDir()
		fixed := time.Date(2025, 1, 2, 3, 4, 0, 0, time.UTC)

		// Equal mtime across all files: tests localeCompare order, JS toFixed(1) half-up rounding
		// (1280 -> 1.3KB, 2304 -> 2.3KB), and UTF-16 label alignment for multibyte `café.txt`.
		writeFileWithMtime(t, filepath.Join(cwd, "_under"), "u", fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "alpha.txt"), strings.Repeat("a", 1280), fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "café.txt"), strings.Repeat("c", 2304), fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "go.mod"), "module x\n", fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "Makefile"), "all:\n", fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "README.md"), "# hi\n", fixed)
		writeFileWithMtime(t, filepath.Join(cwd, "Zeta.txt"), "z", fixed)

		if runtime.GOOS != "windows" {
			// Symlink reports 0B rather than target-path length.
			linkPath := filepath.Join(cwd, "link.txt")
			if err := os.Symlink(filepath.Join(cwd, "README.md"), linkPath); err != nil {
				t.Fatal(err)
			}
			// Assemble directly for deterministic symlink mtime + full byte-exact check below,
			// AND verify scanWorkspace reports Size == 0 for the symlink on disk.
			scan, err := scanWorkspace(t.Context(), scanConfig{
				root:              cwd,
				maxDepth:          workspaceDefaultMaxDepth,
				walkMaxDepth:      agentsMdMaxDepth,
				includeHidden:     false,
				useGitignore:      true,
				pruneExcludedDirs: true,
				collectAgentsMd:   true,
			})
			if err != nil {
				t.Fatalf("scanWorkspace: %v", err)
			}
			foundLink := false
			for _, e := range scan.entries {
				if e.Path == "link.txt" {
					foundLink = true
					if e.Size != 0 {
						t.Fatalf("symlink size = %d, want 0", e.Size)
					}
				}
			}
			if !foundLink {
				t.Fatalf("symlink link.txt missing from scan")
			}
			_ = os.Remove(linkPath)
		}

		// Verify exact omp v19.1.1 byte-for-byte output:
		// maxLabel = utf16Len("  - alpha.txt") = 13 -> padEnd(15)
		// "  - café.txt" has UTF-16 length 12 (13 UTF-8 bytes) -> 3 spaces to col 15 (not 2!).
		// Sizes: 1280B -> 1.3KB, 2304B -> 2.3KB (half-up, not Go's %.1f 1.2KB/2.2KB).
		got := BuildWorkspaceTree(cwd, BuildWorkspaceTreeOptions{})
		want := strings.Join([]string{
			".",
			"  - _under     1B        2025-01-02 03:04",
			"  - alpha.txt  1.3KB     2025-01-02 03:04",
			"  - café.txt   2.3KB     2025-01-02 03:04",
			"  - go.mod     9B        2025-01-02 03:04",
			"  - Makefile   5B        2025-01-02 03:04",
			"  - README.md  5B        2025-01-02 03:04",
			"  - Zeta.txt   1B        2025-01-02 03:04",
		}, "\n")
		if got.Rendered != want {
			t.Fatalf("workspace tree byte-exact mismatch:\ngot:\n%s\n\nwant:\n%s", got.Rendered, want)
		}
	})
}

func TestProjectPromptWorkspaceTreeAndDirContext(t *testing.T) {
	ws := WorkspaceTree{
		DirectoryTree: DirectoryTree{
			RootPath:   "/repo",
			Rendered:   ".\n  - main.go  10B       2025-01-02 03:04",
			Truncated:  true,
			TotalLines: 2,
		},
		AgentsMdFiles: []string{"sub/AGENTS.md"},
	}

	// Default IncludeWorkspaceTree=false omits <workspace-tree>.
	off := RenderTemplate(systemPromptTemplate, BuildOptions{
		ToolNames:            []string{"read", "glob"},
		IncludeWorkspaceTree: false,
	})
	if strings.Contains(off, "<workspace-tree>") || strings.Contains(off, "<dir-context>") {
		t.Fatalf("default prompt should omit workspace-tree and dir-context")
	}

	// Enabled IncludeWorkspaceTree=true renders both <workspace-tree> and <dir-context>.
	on := RenderTemplate(systemPromptTemplate, BuildOptions{
		ToolNames:            []string{"read", "glob"},
		IncludeWorkspaceTree: true,
		WorkspaceTree:        &ws,
	})
	for _, want := range []string{
		"<dir-context>",
		"- sub/AGENTS.md",
		"</dir-context>",
		"Context files above auto-loaded.",
		"<workspace-tree>",
		"Working-directory layout: newest mtime first; depth ≤ 3.",
		"Some entries elided to shorten tree — use `glob`/`read` to drill in.",
		"</workspace-tree>",
	} {
		if !strings.Contains(on, want) {
			t.Fatalf("missing %q in rendered prompt:\n%s", want, on)
		}
	}
}
