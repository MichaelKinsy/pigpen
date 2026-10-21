package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/skill-loader.test.ts. The user-level agent dir is PiG's (PIG_CODING_AGENT_DIR), the counterpart of
// getAgentDir() under divergence D2; the original sets PI_CODING_AGENT_DIR.
func TestPreloadSkillsTwins(t *testing.T) {
	const f = "skill-loader"
	setup := func(t *testing.T) (tmp string, project, global func() string) {
		tmp = t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, "user-agent-dir"))
		t.Setenv("HOME", filepath.Join(tmp, "home")) // ~/.agents/skills and ~/.pi/skills are the user's: keep them out
		return tmp, func() string { return filepath.Join(tmp, ".pi", "skills") }, func() string { return filepath.Join(agentDir(), "skills") }
	}
	mkdir := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(t *testing.T, p, content string) {
		t.Helper()
		mkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flat := func(t *testing.T, root, name, content, ext string) { write(t, filepath.Join(root, name+ext), content) }
	dir := func(t *testing.T, root, name, content string) {
		write(t, filepath.Join(root, name, "SKILL.md"), content)
	}
	one := func(t *testing.T, tmp, name string) string {
		t.Helper()
		got := preloadSkills([]string{name}, tmp)
		eq(t, len(got), 1)
		eq(t, got[0].Name, name)
		return got[0].Content
	}
	notFound := func(t *testing.T, got string) { eq(t, strings.Contains(got, "not found"), true) }
	traversal := func(t *testing.T, got string) { eq(t, strings.Contains(got, "path traversal"), true) }
	symlink := func(t *testing.T, target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skip(err)
		}
	}

	tw(t, f, "returns empty array for empty skill list", func(t *testing.T) {
		tmp, _, _ := setup(t)
		eq(t, len(preloadSkills(nil, tmp)), 0)
	})
	tw(t, f, "loads a top-level flat .md skill from project", func(t *testing.T) {
		tmp, project, _ := setup(t)
		flat(t, project(), "api-conventions", "# API Conventions", ".md")
		eq(t, strings.Contains(one(t, tmp, "api-conventions"), "API Conventions"), true)
	})
	tw(t, f, "ignores .txt files (only .md is supported)", func(t *testing.T) {
		tmp, project, _ := setup(t)
		flat(t, project(), "error-handling", "should not load", ".txt")
		notFound(t, one(t, tmp, "error-handling"))
	})
	tw(t, f, "ignores extensionless files (only .md is supported)", func(t *testing.T) {
		tmp, project, _ := setup(t)
		flat(t, project(), "bare-skill", "should not load", "")
		notFound(t, one(t, tmp, "bare-skill"))
	})
	tw(t, f, "loads a top-level <name>/SKILL.md from project", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, project(), "writing-go", "# Writing Go")
		eq(t, strings.Contains(one(t, tmp, "writing-go"), "Writing Go"), true)
	})
	tw(t, f, "loads a top-level <name>/SKILL.md from getAgentDir()/skills", func(t *testing.T) {
		tmp, _, global := setup(t)
		dir(t, global(), "writing-python", "# Writing Python")
		eq(t, strings.Contains(one(t, tmp, "writing-python"), "Writing Python"), true)
	})
	tw(t, f, "loads a flat .md from getAgentDir()/skills", func(t *testing.T) {
		tmp, _, global := setup(t)
		flat(t, global(), "shell-tips", "use rg", ".md")
		eq(t, one(t, tmp, "shell-tips"), "use rg")
	})
	tw(t, f, "finds nested <subdir>/<name>/SKILL.md in getAgentDir()/skills", func(t *testing.T) {
		tmp, _, global := setup(t)
		dir(t, filepath.Join(global(), "dev-tools"), "using-modern-cli", "# Modern CLI")
		eq(t, strings.Contains(one(t, tmp, "using-modern-cli"), "Modern CLI"), true)
	})
	tw(t, f, "loads <name>/SKILL.md from project .agents/skills (Agent Skills spec)", func(t *testing.T) {
		tmp, _, _ := setup(t)
		dir(t, filepath.Join(tmp, ".agents", "skills"), "writing-rust", "# Writing Rust")
		eq(t, strings.Contains(one(t, tmp, "writing-rust"), "Writing Rust"), true)
	})
	tw(t, f, "prefers .pi/skills over .agents/skills in the same project", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, project(), "shared", "from-pi")
		dir(t, filepath.Join(tmp, ".agents", "skills"), "shared", "from-agents")
		eq(t, one(t, tmp, "shared"), "from-pi")
	})
	tw(t, f, "finds nested <subdir>/<name>/SKILL.md", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, filepath.Join(project(), "dev-tools"), "using-modern-cli", "# Modern CLI")
		eq(t, strings.Contains(one(t, tmp, "using-modern-cli"), "Modern CLI"), true)
	})
	tw(t, f, "prefers project over global", func(t *testing.T) {
		tmp, project, global := setup(t)
		dir(t, project(), "shared", "from-project")
		dir(t, global(), "shared", "from-global")
		eq(t, one(t, tmp, "shared"), "from-project")
	})
	tw(t, f, "prefers shallower match (lex tie-break)", func(t *testing.T) {
		tmp, project, _ := setup(t)
		// Different depths: shallower wins.
		dir(t, filepath.Join(project(), "z-deep", "nested"), "collide", "deep")
		dir(t, filepath.Join(project(), "a-shallow"), "collide", "shallow")
		eq(t, one(t, tmp, "collide"), "shallow")
		// Same depth: alphabetical wins.
		dir(t, filepath.Join(project(), "b-sibling"), "tie", "b")
		dir(t, filepath.Join(project(), "a-sibling"), "tie", "a")
		eq(t, one(t, tmp, "tie"), "a")
	})
	tw(t, f, "descends past a same-named dir that lacks SKILL.md to find a deeper match", func(t *testing.T) {
		tmp, project, _ := setup(t)
		mkdir(t, filepath.Join(project(), "foo"))
		dir(t, filepath.Join(project(), "foo", "inner"), "foo", "deeper")
		eq(t, one(t, tmp, "foo"), "deeper")
	})
	tw(t, f, "does not descend into a sibling skill directory (skills don't nest)", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, project(), "outer", "outer-skill")
		dir(t, filepath.Join(project(), "outer"), "target", "hidden")
		notFound(t, one(t, tmp, "target"))
	})
	tw(t, f, "skips node_modules during recursion", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, filepath.Join(project(), "node_modules", "some-pkg"), "leaked", "should not load")
		notFound(t, one(t, tmp, "leaked"))
	})
	tw(t, f, "skips dotfile directories during recursion", func(t *testing.T) {
		tmp, project, _ := setup(t)
		dir(t, filepath.Join(project(), ".hidden-tree"), "buried", "should not load")
		notFound(t, one(t, tmp, "buried"))
	})
	tw(t, f, "returns fallback for missing skills", func(t *testing.T) {
		tmp, _, _ := setup(t)
		got := preloadSkills([]string{"nonexistent"}, tmp)
		eq(t, got[0].Name, "nonexistent")
		notFound(t, got[0].Content)
		eq(t, got[0].Content, `(Skill "nonexistent" not found in .pi/skills/, .agents/skills/, or global skill locations)`)
	})
	tw(t, f, "loads multiple skills", func(t *testing.T) {
		tmp, project, _ := setup(t)
		flat(t, project(), "a", "Content A", ".md")
		dir(t, project(), "b", "Content B")
		got := preloadSkills([]string{"a", "b"}, tmp)
		eq(t, got[0].Content, "Content A")
		eq(t, strings.Contains(got[1].Content, "Content B"), true)
	})
	unsafe := func(t *testing.T, name string) {
		tmp, _, _ := setup(t)
		got := one(t, tmp, name)
		traversal(t, got)
		eq(t, got, `(Skill "`+name+`" skipped: name contains path traversal characters)`)
	}
	tw(t, f, "skips skill names with path traversal (..)", func(t *testing.T) { unsafe(t, "../../etc/passwd") })
	tw(t, f, "skips skill names with forward slash", func(t *testing.T) { unsafe(t, "sub/dir") })
	tw(t, f, "skips skill names with backslash", func(t *testing.T) { unsafe(t, `sub\dir`) })
	tw(t, f, "skips skill names with spaces", func(t *testing.T) { unsafe(t, "my skill") })
	tw(t, f, "skips skill names starting with a dot", func(t *testing.T) { unsafe(t, ".hidden") })
	tw(t, f, "skips empty skill names", func(t *testing.T) { unsafe(t, "") })
	tw(t, f, "skips skill names exceeding 128 characters", func(t *testing.T) { unsafe(t, strings.Repeat("a", 129)) })
	tw(t, f, "loads valid skills alongside skipped unsafe ones", func(t *testing.T) {
		tmp, project, _ := setup(t)
		flat(t, project(), "legit", "Good content", ".md")
		got := preloadSkills([]string{"../evil", "legit"}, tmp)
		traversal(t, got[0].Content)
		eq(t, got[1].Content, "Good content")
	})
	tw(t, f, "rejects symlinked flat .md files", func(t *testing.T) {
		tmp, project, _ := setup(t)
		mkdir(t, project())
		secret := filepath.Join(tmp, "secret.md")
		write(t, secret, "TOP SECRET")
		symlink(t, secret, filepath.Join(project(), "evil.md"))
		got := one(t, tmp, "evil")
		notFound(t, got)
		eq(t, strings.Contains(got, "TOP SECRET"), false)
	})
	tw(t, f, "rejects symlinked skill directories", func(t *testing.T) {
		tmp, project, _ := setup(t)
		mkdir(t, project())
		real := filepath.Join(tmp, "real-skill")
		write(t, filepath.Join(real, "SKILL.md"), "TOP SECRET")
		symlink(t, real, filepath.Join(project(), "evil-dir"))
		got := one(t, tmp, "evil-dir")
		notFound(t, got)
		eq(t, strings.Contains(got, "TOP SECRET"), false)
	})
	tw(t, f, "rejects symlinked skill root", func(t *testing.T) {
		tmp, project, _ := setup(t)
		realRoot := filepath.Join(tmp, "elsewhere")
		write(t, filepath.Join(realRoot, "leaked-flat.md"), "TOP SECRET FLAT")
		write(t, filepath.Join(realRoot, "leaked-dir", "SKILL.md"), "TOP SECRET DIR")
		mkdir(t, filepath.Join(tmp, ".pi"))
		symlink(t, realRoot, project())
		for _, name := range []string{"leaked-flat", "leaked-dir"} {
			got := one(t, tmp, name)
			notFound(t, got)
			eq(t, strings.Contains(got, "TOP SECRET"), false)
		}
	})
	tw(t, f, "rejects symlinked SKILL.md inside a real skill directory", func(t *testing.T) {
		tmp, project, _ := setup(t)
		skillDir := filepath.Join(project(), "evil-inner")
		mkdir(t, skillDir)
		secret := filepath.Join(tmp, "secret.md")
		write(t, secret, "TOP SECRET")
		symlink(t, secret, filepath.Join(skillDir, "SKILL.md"))
		got := one(t, tmp, "evil-inner")
		notFound(t, got)
		eq(t, strings.Contains(got, "TOP SECRET"), false)
	})
}
