package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Twins of test/memory.test.ts and test/memory-legacy-fallback.test.ts. Adaptations: the original mocks Node's
// homedir() and sets PI_CODING_AGENT_DIR; here HOME / USERPROFILE and PIG_CODING_AGENT_DIR (agentDir()) are set
// for the test. upstream: src/memory.ts.

// memHome points the home directory at an empty directory with no legacy agent-memory and returns the test's tmp dir.
func memHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "home")
	if err := os.MkdirAll(fakeHome, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PIG_HOME", "")
	return tmp
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o777); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func mustDir(t *testing.T) func(name, scope, cwd string) string {
	return func(name, scope, cwd string) string {
		t.Helper()
		d, err := resolveMemoryDir(name, scope, cwd)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
}

func wantUnsafe(t *testing.T, name string) {
	t.Helper()
	_, err := resolveMemoryDir(name, "project", "/workspace")
	if err == nil || !strings.Contains(err.Error(), "Unsafe agent name") {
		t.Fatalf("name %q: err = %v, want Unsafe agent name", name, err)
	}
}

func TestMemoryTwin(t *testing.T) {
	const f = "memory"
	t.Run("resolveMemoryDir", func(t *testing.T) {
		tmp := memHome(t)
		dir := mustDir(t)
		tw(t, f, "resolves project scope to .pi/agent-memory/<name>", func(t *testing.T) {
			eq(t, dir("auditor", "project", "/workspace"), filepath.Join("/workspace", ".pi", "agent-memory", "auditor"))
		})
		tw(t, f, "resolves local scope to .pi/agent-memory-local/<name>", func(t *testing.T) {
			eq(t, dir("auditor", "local", "/workspace"), filepath.Join("/workspace", ".pi", "agent-memory-local", "auditor"))
		})
		tw(t, f, "resolves user scope under the agent dir (honors PI_CODING_AGENT_DIR)", func(t *testing.T) {
			t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, "custom-agent-dir"))
			got := dir("auditor", "user", "/workspace")
			eq(t, got, filepath.Join(tmp, "custom-agent-dir", "agent-memory", "auditor"))
			if strings.Contains(got, "/workspace") {
				t.Fatalf("user scope must not use the workspace: %s", got)
			}
		})
		tw(t, f, "throws on names with path traversal (..)", func(t *testing.T) { wantUnsafe(t, "../../etc/evil") })
		tw(t, f, "throws on names with forward slash", func(t *testing.T) { wantUnsafe(t, "foo/bar") })
		tw(t, f, "throws on names with backslash", func(t *testing.T) { wantUnsafe(t, "foo\\bar") })
		tw(t, f, "throws on names with null byte", func(t *testing.T) { wantUnsafe(t, "foo\x00bar") })
		tw(t, f, "throws on empty name", func(t *testing.T) { wantUnsafe(t, "") })
		tw(t, f, "throws on names starting with dot", func(t *testing.T) { wantUnsafe(t, ".hidden") })
		tw(t, f, "throws on names with spaces", func(t *testing.T) { wantUnsafe(t, "foo bar") })
		tw(t, f, "allows hyphens, underscores, and dots in names", func(t *testing.T) {
			if _, err := resolveMemoryDir("my-agent_v2.1", "project", "/workspace"); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("isUnsafeName (whitelist validation)", func(t *testing.T) {
		tw(t, f, "rejects empty string", func(t *testing.T) { eq(t, isUnsafeName(""), true) })
		tw(t, f, "rejects names longer than 128 chars", func(t *testing.T) {
			eq(t, isUnsafeName(strings.Repeat("a", 129)), true)
			eq(t, isUnsafeName(strings.Repeat("a", 128)), false)
		})
		tw(t, f, "rejects path traversal", func(t *testing.T) { eq(t, isUnsafeName("../../etc"), true) })
		tw(t, f, "rejects names starting with dot", func(t *testing.T) { eq(t, isUnsafeName(".hidden"), true) })
		tw(t, f, "rejects names with spaces", func(t *testing.T) { eq(t, isUnsafeName("foo bar"), true) })
		tw(t, f, "rejects names with special characters", func(t *testing.T) {
			for _, n := range []string{"foo;bar", "foo|bar", "foo`bar"} {
				eq(t, isUnsafeName(n), true)
			}
		})
		tw(t, f, "allows valid names", func(t *testing.T) {
			for _, n := range []string{"my-agent", "agent_v2", "Agent123", "my-agent.v2"} {
				eq(t, isUnsafeName(n), false)
			}
		})
	})

	t.Run("ensureMemoryDir", func(t *testing.T) {
		tw(t, f, "creates directory if it doesn't exist", func(t *testing.T) {
			dir := filepath.Join(memHome(t), "agent-memory", "test")
			eq(t, exists(dir), false)
			if err := ensureMemoryDir(dir); err != nil {
				t.Fatal(err)
			}
			eq(t, exists(dir), true)
		})
		tw(t, f, "no-ops if directory already exists", func(t *testing.T) {
			dir := filepath.Join(memHome(t), "agent-memory", "test")
			mustMkdir(t, dir)
			if err := ensureMemoryDir(dir); err != nil {
				t.Fatal(err)
			}
			eq(t, exists(dir), true)
		})
		tw(t, f, "throws on symlinked directory", func(t *testing.T) {
			tmp := memHome(t)
			realDir, linkDir := filepath.Join(tmp, "real-dir"), filepath.Join(tmp, "symlink-dir")
			mustMkdir(t, realDir)
			mustSymlink(t, realDir, linkDir)
			err := ensureMemoryDir(linkDir)
			if err == nil || !strings.Contains(err.Error(), "symlinked memory directory") {
				t.Fatalf("err = %v", err)
			}
			eq(t, err.Error(), "Refusing to use symlinked memory directory: "+linkDir)
		})
	})

	t.Run("isSymlink", func(t *testing.T) {
		tw(t, f, "returns false for regular file", func(t *testing.T) {
			file := filepath.Join(memHome(t), "regular.txt")
			mustWrite(t, file, "content")
			eq(t, isSymlink(file), false)
		})
		tw(t, f, "returns true for symlink", func(t *testing.T) {
			tmp := memHome(t)
			file, link := filepath.Join(tmp, "real.txt"), filepath.Join(tmp, "link.txt")
			mustWrite(t, file, "content")
			mustSymlink(t, file, link)
			eq(t, isSymlink(link), true)
		})
		tw(t, f, "returns false for nonexistent path", func(t *testing.T) {
			eq(t, isSymlink(filepath.Join(memHome(t), "nope")), false)
		})
	})

	t.Run("safeReadFile", func(t *testing.T) {
		tw(t, f, "reads regular files", func(t *testing.T) {
			file := filepath.Join(memHome(t), "regular.txt")
			mustWrite(t, file, "hello")
			got, ok := safeReadFile(file)
			eq(t, [2]any{got, ok}, [2]any{"hello", true})
		})
		tw(t, f, "rejects symlinked files", func(t *testing.T) {
			tmp := memHome(t)
			file, link := filepath.Join(tmp, "real.txt"), filepath.Join(tmp, "link.txt")
			mustWrite(t, file, "secret")
			mustSymlink(t, file, link)
			_, ok := safeReadFile(link)
			eq(t, ok, false)
		})
		tw(t, f, "returns undefined for nonexistent files", func(t *testing.T) {
			_, ok := safeReadFile(filepath.Join(memHome(t), "nope.txt"))
			eq(t, ok, false)
		})
	})

	t.Run("readMemoryIndex", func(t *testing.T) {
		tw(t, f, "returns undefined when MEMORY.md doesn't exist", func(t *testing.T) {
			_, ok := readMemoryIndex(memHome(t))
			eq(t, ok, false)
		})
		tw(t, f, "reads MEMORY.md content", func(t *testing.T) {
			tmp := memHome(t)
			mustWrite(t, filepath.Join(tmp, "MEMORY.md"), "# Memories\n- Item 1\n- Item 2")
			got, ok := readMemoryIndex(tmp)
			eq(t, [2]any{got, ok}, [2]any{"# Memories\n- Item 1\n- Item 2", true})
		})
		tw(t, f, "rejects symlinked memory directory", func(t *testing.T) {
			tmp := memHome(t)
			realDir, linkDir := filepath.Join(tmp, "real-mem"), filepath.Join(tmp, "link-mem")
			mustMkdir(t, realDir)
			mustWrite(t, filepath.Join(realDir, "MEMORY.md"), "# Secret")
			mustSymlink(t, realDir, linkDir)
			_, ok := readMemoryIndex(linkDir)
			eq(t, ok, false)
		})
		tw(t, f, "rejects symlinked MEMORY.md file", func(t *testing.T) {
			tmp := memHome(t)
			realFile := filepath.Join(tmp, "secret.md")
			mustWrite(t, realFile, "# Secret")
			memDir := filepath.Join(tmp, "mem-dir")
			mustMkdir(t, memDir)
			mustSymlink(t, realFile, filepath.Join(memDir, "MEMORY.md"))
			_, ok := readMemoryIndex(memDir)
			eq(t, ok, false)
		})
		tw(t, f, "truncates content beyond 200 lines", func(t *testing.T) {
			tmp := memHome(t)
			var lines []string
			for i := 1; i <= 250; i++ {
				lines = append(lines, "Line "+strconv.Itoa(i))
			}
			mustWrite(t, filepath.Join(tmp, "MEMORY.md"), strings.Join(lines, "\n"))
			got, _ := readMemoryIndex(tmp)
			if !strings.Contains(got, "Line 200") || strings.Contains(got, "Line 201") || !strings.Contains(got, "truncated at 200 lines") {
				t.Fatalf("got %q", got)
			}
			eq(t, strings.HasSuffix(got, "Line 200\n... (truncated at 200 lines)"), true)
			// exactly 200 lines are returned as they are
			mustWrite(t, filepath.Join(tmp, "MEMORY.md"), strings.Join(lines[:200], "\n"))
			got, _ = readMemoryIndex(tmp)
			eq(t, got, strings.Join(lines[:200], "\n"))
		})
	})

	t.Run("buildMemoryBlock", func(t *testing.T) {
		block := func(t *testing.T, name, scope, cwd string) string {
			t.Helper()
			b, err := buildMemoryBlock(name, scope, cwd)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		tw(t, f, "builds memory block with no existing MEMORY.md", func(t *testing.T) {
			b := block(t, "test-agent", "project", memHome(t))
			for _, want := range []string{"Agent Memory", "agent-memory/test-agent", "No MEMORY.md exists yet", "Memory Instructions"} {
				if !strings.Contains(b, want) {
					t.Fatalf("missing %q in %s", want, b)
				}
			}
		})
		tw(t, f, "builds memory block with existing MEMORY.md", func(t *testing.T) {
			tmp := memHome(t)
			memDir := filepath.Join(tmp, ".pi", "agent-memory", "test-agent")
			mustMkdir(t, memDir)
			mustWrite(t, filepath.Join(memDir, "MEMORY.md"), "# Existing\n- recall this")
			b := block(t, "test-agent", "project", tmp)
			if !strings.Contains(b, "Existing") || !strings.Contains(b, "recall this") || strings.Contains(b, "No MEMORY.md exists yet") {
				t.Fatalf("got %s", b)
			}
		})
		tw(t, f, "creates memory directory if it doesn't exist", func(t *testing.T) {
			tmp := memHome(t)
			memDir := filepath.Join(tmp, ".pi", "agent-memory", "new-agent")
			eq(t, exists(memDir), false)
			block(t, "new-agent", "project", tmp)
			eq(t, exists(memDir), true)
		})
		tw(t, f, "includes Read/Write/Edit instructions", func(t *testing.T) {
			if b := block(t, "test-agent", "project", memHome(t)); !strings.Contains(b, "Read, Write, and Edit tools") {
				t.Fatal(b)
			}
		})
		tw(t, f, "uses correct directory for local scope", func(t *testing.T) {
			if b := block(t, "test-agent", "local", memHome(t)); !strings.Contains(b, "agent-memory-local/test-agent") {
				t.Fatal(b)
			}
		})
		tw(t, f, "uses correct directory for user scope", func(t *testing.T) {
			tmp := memHome(t)
			t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, "agent-dir"))
			b := block(t, "test-agent", "user", filepath.Join(tmp, "workspace"))
			if !strings.Contains(b, filepath.Join(tmp, "agent-dir", "agent-memory", "test-agent")) || strings.Contains(b, filepath.Join(tmp, "workspace")) {
				t.Fatal(b)
			}
		})
		tw(t, f, "includes scope label in header", func(t *testing.T) {
			tmp := memHome(t)
			for _, s := range []string{"project", "local", "user"} {
				if b := block(t, "a", s, tmp); !strings.Contains(b, "Memory scope: "+s) {
					t.Fatalf("%s: %s", s, b)
				}
			}
		})
		t.Run("builds the whole block as the original words it", func(t *testing.T) {
			tmp := memHome(t)
			b := block(t, "test-agent", "project", tmp)
			dir := filepath.Join(tmp, ".pi", "agent-memory", "test-agent")
			want := "# Agent Memory\n\nYou have a persistent memory directory at: " + dir + "/\nMemory scope: project\n\n" +
				"This memory persists across sessions. Use it to build up knowledge over time.\n\n" +
				"No MEMORY.md exists yet. Create one at " + filepath.Join(dir, "MEMORY.md") + " to start building persistent memory.\n\n" +
				"## Memory Instructions\n" +
				"- MEMORY.md is an index file — keep it concise (under 200 lines). Lines after 200 are truncated.\n" +
				"- Store detailed memories in separate files within " + dir + "/ and link to them from MEMORY.md.\n" +
				"- Each memory file should use this frontmatter format:\n  ```markdown\n  ---\n  name: <memory name>\n  description: <one-line description>\n  type: <user|feedback|project|reference>\n  ---\n  <memory content>\n  ```\n" +
				"- Update or remove memories that become outdated. Check for existing memories before creating duplicates.\n" +
				"- You have Read, Write, and Edit tools available for managing memory files."
			eq(t, b, want)
		})
	})

	t.Run("buildReadOnlyMemoryBlock", func(t *testing.T) {
		block := func(t *testing.T, name, scope, cwd string) string {
			t.Helper()
			b, err := buildReadOnlyMemoryBlock(name, scope, cwd)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		tw(t, f, "returns read-only instructions without write/edit mention", func(t *testing.T) {
			b := block(t, "test-agent", "project", memHome(t))
			if !strings.Contains(b, "read-only") || strings.Contains(b, "Write") || strings.Contains(b, "Edit") || strings.Contains(b, "Memory Instructions") {
				t.Fatal(b)
			}
		})
		tw(t, f, "does NOT create the memory directory", func(t *testing.T) {
			tmp := memHome(t)
			memDir := filepath.Join(tmp, ".pi", "agent-memory", "ro-agent")
			eq(t, exists(memDir), false)
			block(t, "ro-agent", "project", tmp)
			eq(t, exists(memDir), false)
		})
		tw(t, f, "includes existing MEMORY.md content", func(t *testing.T) {
			tmp := memHome(t)
			memDir := filepath.Join(tmp, ".pi", "agent-memory", "test-agent")
			mustMkdir(t, memDir)
			mustWrite(t, filepath.Join(memDir, "MEMORY.md"), "# Existing\n- recall this")
			b := block(t, "test-agent", "project", tmp)
			if !strings.Contains(b, "Existing") || !strings.Contains(b, "recall this") {
				t.Fatal(b)
			}
		})
		tw(t, f, "returns 'no memory available' when no MEMORY.md exists", func(t *testing.T) {
			b := block(t, "test-agent", "project", memHome(t))
			if !strings.Contains(b, "No memory is available yet") || strings.Contains(b, "Create one") {
				t.Fatal(b)
			}
		})
		tw(t, f, "includes scope label in header", func(t *testing.T) {
			tmp := memHome(t)
			for _, s := range []string{"project", "local", "user"} {
				if b := block(t, "a", s, tmp); !strings.Contains(b, "Memory scope: "+s) {
					t.Fatalf("%s: %s", s, b)
				}
			}
		})
		tw(t, f, "does not mention memory directory path for write access", func(t *testing.T) {
			b := block(t, "test-agent", "project", memHome(t))
			if strings.Contains(b, "persistent memory directory at:") || strings.Contains(b, "Create one at") {
				t.Fatal(b)
			}
		})
		tw(t, f, "rejects symlinked memory directory in read-only mode", func(t *testing.T) {
			tmp := memHome(t)
			realDir := filepath.Join(tmp, ".pi", "agent-memory", "test-agent")
			mustMkdir(t, realDir)
			mustWrite(t, filepath.Join(realDir, "MEMORY.md"), "# Secret")
			mustSymlink(t, realDir, filepath.Join(tmp, ".pi", "agent-memory", "linked-agent"))
			b := block(t, "linked-agent", "project", tmp)
			if !strings.Contains(b, "No memory is available yet") {
				t.Fatal(b)
			}
		})
		t.Run("builds the whole read-only block as the original words it", func(t *testing.T) {
			tmp := memHome(t)
			eq(t, block(t, "a", "local", tmp), "# Agent Memory (read-only)\n\nMemory scope: local\nYou have read-only access to memory. You can reference existing memories but cannot create or modify them.\n\n"+
				"No memory is available yet. Other agents or sessions with write access can create memories for you to consume.")
			memDir := filepath.Join(tmp, ".pi", "agent-memory-local", "a")
			mustMkdir(t, memDir)
			mustWrite(t, filepath.Join(memDir, "MEMORY.md"), "x\ny")
			eq(t, block(t, "a", "local", tmp), "# Agent Memory (read-only)\n\nMemory scope: local\nYou have read-only access to memory. You can reference existing memories but cannot create or modify them.\n\n## Current MEMORY.md\nx\ny")
		})
	})
}

func TestMemoryLegacyFallbackTwin(t *testing.T) {
	const f = "memory-legacy-fallback"
	setup := func(t *testing.T) (tmp, fakeHome, agentDirPath string) {
		tmp = memHome(t)
		fakeHome = filepath.Join(tmp, "home")
		agentDirPath = filepath.Join(tmp, "agent-dir")
		t.Setenv("PIG_CODING_AGENT_DIR", agentDirPath)
		return
	}
	tw(t, f, "uses the agent dir location when no legacy memory exists", func(t *testing.T) {
		_, _, ad := setup(t)
		eq(t, mustDir(t)("chronicler", "user", "/workspace"), filepath.Join(ad, "agent-memory", "chronicler"))
	})
	tw(t, f, "falls back to legacy ~/.pi/agent-memory when it exists and the new location doesn't", func(t *testing.T) {
		_, home, _ := setup(t)
		legacy := filepath.Join(home, ".pi", "agent-memory", "chronicler")
		mustMkdir(t, legacy)
		eq(t, mustDir(t)("chronicler", "user", "/workspace"), legacy)
	})
	tw(t, f, "prefers the new location once it exists, even if legacy also exists", func(t *testing.T) {
		_, home, ad := setup(t)
		legacy := filepath.Join(home, ".pi", "agent-memory", "chronicler")
		current := filepath.Join(ad, "agent-memory", "chronicler")
		mustMkdir(t, legacy)
		mustMkdir(t, current)
		eq(t, mustDir(t)("chronicler", "user", "/workspace"), current)
	})
	tw(t, f, "ignores a symlinked legacy directory", func(t *testing.T) {
		tmp, home, ad := setup(t)
		target := filepath.Join(tmp, "elsewhere")
		mustMkdir(t, target)
		mustMkdir(t, filepath.Join(home, ".pi", "agent-memory"))
		mustSymlink(t, target, filepath.Join(home, ".pi", "agent-memory", "chronicler"))
		eq(t, mustDir(t)("chronicler", "user", "/workspace"), filepath.Join(ad, "agent-memory", "chronicler"))
	})
	tw(t, f, "scopes the fallback per agent name", func(t *testing.T) {
		_, home, ad := setup(t)
		legacyOther := filepath.Join(home, ".pi", "agent-memory", "other-agent")
		mustMkdir(t, legacyOther)
		dir := mustDir(t)
		eq(t, dir("other-agent", "user", "/workspace"), legacyOther)
		eq(t, dir("chronicler", "user", "/workspace"), filepath.Join(ad, "agent-memory", "chronicler"))
	})
}
