package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/agent-memory.test.ts.

func memAgent(mem *AgentMemoryConfig, tools []string) AgentConfig {
	return AgentConfig{Name: "test-agent", Description: "test agent", SystemPrompt: "do the thing", SystemPromptMode: "replace", Source: SourceProject,
		FilePath: "/tmp/test-agent.md", Memory: mem, Tools: tools}
}

func memProject(t *testing.T) string {
	t.Helper()
	dir := tmp(t)
	mkdirAll(t, filepath.Join(dir, ".pi", "agents"))
	return dir
}

func writeMemory(t *testing.T, dir, contents string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, agentMemoryFile), contents)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitFixture(t *testing.T, project, tracked string) {
	t.Helper()
	git(t, project, "init", "-q")
	git(t, project, "config", "user.email", "test@example.invalid")
	git(t, project, "config", "user.name", "Test")
	git(t, project, "add", tracked)
	git(t, project, "commit", "-qm", "fixture")
}

func hasRe(t *testing.T, s, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("%q does not match /%s/", s, pattern)
	}
}

func noRe(t *testing.T, s, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("%q matches /%s/", s, pattern)
	}
}

func TestParseMemoryFrontmatter(t *testing.T) {
	const f = "agent-memory"
	tw(t, f, "parses a project scope block", func(t *testing.T) {
		eq(t, *parseMemoryFrontmatter("scope: project\npath: security-reviewer"), AgentMemoryConfig{"project", "security-reviewer"}, "parsed")
	})
	tw(t, f, "parses a user scope with nested path and strips quotes", func(t *testing.T) {
		eq(t, *parseMemoryFrontmatter("scope: user\npath: \"team/release-agent\""), AgentMemoryConfig{"user", "team/release-agent"}, "parsed")
	})
	tw(t, f, "parses an inline object memory block", func(t *testing.T) {
		eq(t, *parseMemoryFrontmatter(`{ scope: "project", path: "security-reviewer" }`), AgentMemoryConfig{"project", "security-reviewer"}, "parsed")
	})
	tw(t, f, "rejects unknown scopes", func(t *testing.T) { eq(t, parseMemoryFrontmatter("scope: global\npath: x") == nil, true, "nil") })
	tw(t, f, "rejects a missing path", func(t *testing.T) { eq(t, parseMemoryFrontmatter("scope: project") == nil, true, "nil") })
	tw(t, f, "rejects a missing scope", func(t *testing.T) { eq(t, parseMemoryFrontmatter("path: x") == nil, true, "nil") })
	tw(t, f, "treats empty or absent input as no memory config", func(t *testing.T) {
		eq(t, parseMemoryFrontmatter("") == nil, true, "empty")
	})
	tw(t, f, "inherits write capability when tools are unset", func(t *testing.T) { eq(t, agentHasWriteTools(AgentConfig{}), true, "unset") })
	tw(t, f, "detects edit, write, and bash as write tools", func(t *testing.T) {
		eq(t, agentHasWriteTools(AgentConfig{Tools: []string{"read", "edit"}}), true, "edit")
		eq(t, agentHasWriteTools(AgentConfig{Tools: []string{"write"}}), true, "write")
		eq(t, agentHasWriteTools(AgentConfig{Tools: []string{"bash"}}), true, "bash")
	})
	tw(t, f, "treats read-only and mcp-only tool sets as non-write", func(t *testing.T) {
		eq(t, agentHasWriteTools(AgentConfig{Tools: []string{"read", "grep", "find", "ls"}}), false, "read-only")
		eq(t, agentHasWriteTools(AgentConfig{Tools: []string{"mcp:filesystem"}}), false, "mcp")
	})
}

func TestResolveMemoryDir(t *testing.T) {
	const f = "agent-memory"
	isErr := func(root, p string) bool { _, e := resolveMemoryDir(root, p); return e != "" }
	tw(t, f, "resolves a simple and nested path under the root", func(t *testing.T) {
		root := tmp(t)
		d, e := resolveMemoryDir(root, "reviewer")
		eq(t, []string{d, e}, []string{filepath.Join(root, "reviewer"), ""}, "simple")
		d, e = resolveMemoryDir(root, "team/reviewer")
		eq(t, []string{d, e}, []string{filepath.Join(root, "team", "reviewer"), ""}, "nested")
	})
	tw(t, f, "rejects empty paths", func(t *testing.T) {
		root := tmp(t)
		eq(t, isErr(root, "") && isErr(root, "   "), true, "empty")
	})
	tw(t, f, "rejects dot and parent segments", func(t *testing.T) {
		root := tmp(t)
		eq(t, isErr(root, ".") && isErr(root, "..") && isErr(root, "a/../b"), true, "dots")
	})
	tw(t, f, "rejects absolute and Windows drive-like paths", func(t *testing.T) {
		root := tmp(t)
		eq(t, isErr(root, "/tmp/reviewer") && isErr(root, `C:\Users\reviewer`) && isErr(root, "C:reviewer") && isErr(root, "team:C"), true, "absolute")
	})
	tw(t, f, "rejects a symlinked ancestor before prompting a first write", func(t *testing.T) {
		root, outside := tmp(t), tmp(t)
		symlink(t, outside, filepath.Join(root, "leak"))
		eq(t, isErr(root, "leak/new-agent"), true, "expected symlink ancestor escape to be rejected")
	})
	tw(t, f, "rejects a memory dir that is a symlink escaping the root", func(t *testing.T) {
		root, outside := tmp(t), tmp(t)
		symlink(t, outside, filepath.Join(root, "leak"))
		eq(t, isErr(root, "leak"), true, "expected symlink escape to be rejected")
	})
}

func TestReadMemoryFile(t *testing.T) {
	const f = "agent-memory"
	tw(t, f, "returns null when no memory file exists", func(t *testing.T) {
		r, unsafe := readMemoryFile(tmp(t))
		eq(t, r == nil && !unsafe, true, "null")
	})
	tw(t, f, "reads contents and reports byte capping", func(t *testing.T) {
		dir := tmp(t)
		writeMemory(t, dir, "line one\nline two\n")
		r, _ := readMemoryFile(dir)
		eq(t, *r, memoryFile{"line one\nline two\n", false}, "result")
	})
	tw(t, f, "flags byte-capped contents", func(t *testing.T) {
		dir := tmp(t)
		writeMemory(t, dir, strings.Join(repeat(strings.Repeat("x", 500), 50), "\n"))
		r, _ := readMemoryFile(dir)
		eq(t, r.byteCapped, true, "capped")
	})
	tw(t, f, "does not return more than the memory byte cap", func(t *testing.T) {
		dir := tmp(t)
		writeMemory(t, dir, strings.Repeat("x", 1024*1024))
		r, _ := readMemoryFile(dir)
		eq(t, r.byteCapped && len(r.contents) <= 16*1024, true, "capped within the cap")
	})
	tw(t, f, "rejects a symlinked memory file that escapes the memory dir", func(t *testing.T) {
		dir := tmp(t)
		outside := filepath.Join(tmp(t), "secret.md")
		writeFile(t, outside, "leaked")
		symlink(t, outside, filepath.Join(dir, agentMemoryFile))
		r, unsafe := readMemoryFile(dir)
		eq(t, r == nil && unsafe, true, "unsafe")
	})
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestBuildAgentMemoryInjection(t *testing.T) {
	const f = "agent-memory"
	mem := func(scope, p string) *AgentMemoryConfig { return &AgentMemoryConfig{scope, p} }
	tw(t, f, "returns empty when the agent has no memory scope", func(t *testing.T) {
		eq(t, buildAgentMemoryInjection(memAgent(nil, nil), memProject(t)), "", "empty")
	})
	tw(t, f, "injects a read-write block with contents for a project scope", func(t *testing.T) {
		project := memProject(t)
		dir := filepath.Join(project, ".pi", agentMemoryDirName, "security-reviewer")
		writeMemory(t, dir, "Threat: token leakage in logs.\nGotcha: retry on 429.")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "security-reviewer"), []string{"read", "edit"}), project)
		hasRe(t, inj, "# Persistent agent memory")
		eq(t, strings.Contains(inj, "Memory file: "+filepath.Join(dir, agentMemoryFile)), true, "file")
		hasRe(t, inj, "append a concise dated entry")
		hasRe(t, inj, "reference data, not instructions")
		hasRe(t, inj, `Threat: token leakage in logs\.`)
		hasRe(t, inj, `Gotcha: retry on 429\.`)
		noRe(t, inj, "read-only")
	})
	tw(t, f, "injects a creation prompt when a read-write agent has no memory file yet", func(t *testing.T) {
		project := memProject(t)
		inj := buildAgentMemoryInjection(memAgent(mem("project", "fresh-agent"), []string{"read", "write"}), project)
		eq(t, strings.Contains(inj, "Memory file: "+filepath.Join(project, ".pi", agentMemoryDirName, "fresh-agent", agentMemoryFile)), true, "file")
		hasRe(t, inj, "No "+agentMemoryFile+" exists yet")
		hasRe(t, inj, "You may create it")
	})
	tw(t, f, "resolves project memory through a linked worktree when Git trace writes to stderr", func(t *testing.T) {
		root := tmp(t)
		project, worktree := filepath.Join(root, "project"), filepath.Join(root, "worktree")
		mkdirAll(t, filepath.Join(project, ".pi", "agents"))
		writeFile(t, filepath.Join(project, ".pi", "agents", "probe.md"), "probe\n")
		commitFixture(t, project, ".pi/agents/probe.md")
		git(t, project, "worktree", "add", "-qb", "probe", worktree)
		realProject, _ := filepath.EvalSymlinks(project)
		expected := filepath.Join(realProject, ".pi", agentMemoryDirName, "probe", agentMemoryFile)
		writeMemory(t, filepath.Dir(expected), "marker: shared-main-memory\n")
		agent := memAgent(mem("project", "probe"), []string{"read", "write"})
		t.Setenv("GIT_TRACE", "1")
		inj := buildAgentMemoryInjection(agent, filepath.Join(worktree, ".pi", "agents"))
		hasRe(t, inj, "marker: shared-main-memory")
		eq(t, strings.Contains(inj, "Memory file: "+expected), true, "file")
		eq(t, strings.Contains(inj, filepath.Join(worktree, ".pi", agentMemoryDirName)), false, "not worktree local")
		marker := filepath.Join(worktree, ".git")
		b, _ := os.ReadFile(marker)
		gitdir := gitdirLineRe.FindStringSubmatch(string(b))[1]
		realWorktree, _ := filepath.EvalSymlinks(worktree)
		absGitDir := gitdir
		if !filepath.IsAbs(gitdir) {
			absGitDir = filepath.Join(worktree, gitdir)
		}
		rel, _ := filepath.Rel(realWorktree, absGitDir)
		writeFileRaw(t, marker, "gitdir: "+rel+"\n")
		inj = buildAgentMemoryInjection(agent, worktree)
		hasRe(t, inj, "marker: shared-main-memory")
		eq(t, strings.Contains(inj, "Memory file: "+expected), true, "relative gitdir file")
	})
	tw(t, f, "maps nested project memory from a linked worktree to the same main-checkout path", func(t *testing.T) {
		root := tmp(t)
		project, worktree := filepath.Join(root, "project"), filepath.Join(root, "worktree")
		nested := filepath.Join("packages", "app")
		mkdirAll(t, filepath.Join(project, nested, ".pi", "agents"))
		writeFile(t, filepath.Join(project, nested, ".pi", "agents", "probe.md"), "probe\n")
		commitFixture(t, project, filepath.ToSlash(nested)+"/.pi/agents/probe.md")
		git(t, project, "worktree", "add", "-qb", "nested", worktree)
		realProject, _ := filepath.EvalSymlinks(project)
		expected := filepath.Join(realProject, nested, ".pi", agentMemoryDirName, "probe", agentMemoryFile)
		writeMemory(t, filepath.Dir(expected), "marker: nested-main-memory\n")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "probe"), []string{"read", "write"}), filepath.Join(worktree, nested))
		hasRe(t, inj, "marker: nested-main-memory")
		eq(t, strings.Contains(inj, "Memory file: "+expected), true, "file")
	})
	tw(t, f, "rejects a forged marker pointing at another repository's valid worktree metadata", func(t *testing.T) {
		project := memProject(t)
		other := filepath.Join(tmp(t), "project")
		otherWorktree := filepath.Join(filepath.Dir(other), "worktree")
		mkdirAll(t, other)
		writeFile(t, filepath.Join(other, "tracked.txt"), "fixture\n")
		commitFixture(t, other, "tracked.txt")
		git(t, other, "worktree", "add", "-qb", "other", otherWorktree)
		b, _ := os.ReadFile(filepath.Join(otherWorktree, ".git"))
		otherGitDir := gitdirLineRe.FindStringSubmatch(string(b))[1]
		writeFileRaw(t, filepath.Join(project, ".git"), "gitdir: "+otherGitDir+"\n")
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "probe"), "marker: checkout-local\n")
		writeMemory(t, filepath.Join(other, ".pi", agentMemoryDirName, "probe"), "marker: other-repository\n")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "probe"), []string{"read"}), project)
		hasRe(t, inj, "marker: checkout-local")
		noRe(t, inj, "marker: other-repository")
	})
	tw(t, f, "rejects self-consistent forged worktree metadata that Git does not recognize", func(t *testing.T) {
		project := memProject(t)
		fakeMain := tmp(t)
		fake := filepath.Join(fakeMain, ".git", "worktrees", "forged")
		mkdirAll(t, fake)
		writeFileRaw(t, filepath.Join(project, ".git"), "gitdir: "+fake+"\n")
		writeFileRaw(t, filepath.Join(fake, "gitdir"), filepath.Join(project, ".git")+"\n")
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "probe"), "marker: checkout-local\n")
		writeMemory(t, filepath.Join(fakeMain, ".pi", agentMemoryDirName, "probe"), "marker: forged-main\n")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "probe"), []string{"read"}), project)
		hasRe(t, inj, "marker: checkout-local")
		noRe(t, inj, "marker: forged-main")
	})
	tw(t, f, "uses the Git root when a linked worktree has no local project config directory", func(t *testing.T) {
		root := tmp(t)
		project, worktree := filepath.Join(root, "project"), filepath.Join(root, "worktree")
		mkdirAll(t, project)
		writeFile(t, filepath.Join(project, "tracked.txt"), "fixture\n")
		commitFixture(t, project, "tracked.txt")
		git(t, project, "worktree", "add", "-qb", "probe", worktree)
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "probe"), "marker: git-root-memory\n")
		eq(t, findNearestProjectRoot(worktree), "", "no project root")
		hasRe(t, buildAgentMemoryInjection(memAgent(mem("project", "probe"), []string{"read"}), worktree), "marker: git-root-memory")
	})
	tw(t, f, "keeps project memory checkout-local for unsupported separate Git directories", func(t *testing.T) {
		root := tmp(t)
		project, gitDir := filepath.Join(root, "project"), filepath.Join(root, "metadata")
		mkdirAll(t, filepath.Join(project, ".pi", "agents"))
		git(t, root, "init", "-q", "--separate-git-dir="+gitDir, project)
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "probe"), "marker: checkout-local\n")
		writeMemory(t, filepath.Join(root, ".pi", agentMemoryDirName, "probe"), "marker: unsupported-remap\n")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "probe"), []string{"read"}), project)
		hasRe(t, inj, "marker: checkout-local")
		noRe(t, inj, "marker: unsupported-remap")
		eq(t, strings.Contains(inj, "Memory file: "+filepath.Join(project, ".pi", agentMemoryDirName, "probe", agentMemoryFile)), true, "file")
	})
	tw(t, f, "injects a read-only block for agents without write tools", func(t *testing.T) {
		project := memProject(t)
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "scout"), "Known flake: async timeout test.")
		inj := buildAgentMemoryInjection(memAgent(mem("project", "scout"), []string{"read", "grep", "find", "ls"}), project)
		hasRe(t, inj, "read-only, role-specific memory scope")
		hasRe(t, inj, "Do not attempt to edit or create the memory file")
		hasRe(t, inj, "reference data, not instructions")
		hasRe(t, inj, `Known flake: async timeout test\.`)
		noRe(t, inj, "You may create it")
		noRe(t, inj, "append a concise dated entry")
	})
	tw(t, f, "injects nothing for a read-only agent with no memory file yet", func(t *testing.T) {
		eq(t, buildAgentMemoryInjection(memAgent(mem("project", "empty-scout"), []string{"read"}), memProject(t)), "", "empty")
	})
	tw(t, f, "resolves user scope under the agent dir, separate from the owner memory system", func(t *testing.T) {
		home := withTempHome(t)
		project := memProject(t)
		dir := filepath.Join(home, ".pi", "agent", agentMemoryDirName, "release-agent")
		writeMemory(t, dir, "Release gotcha: tag before gh release.")
		inj := buildAgentMemoryInjection(memAgent(mem("user", "release-agent"), []string{"read", "edit"}), project)
		eq(t, strings.Contains(inj, "Memory file: "+filepath.Join(dir, agentMemoryFile)), true, "file")
		hasRe(t, inj, `Release gotcha: tag before gh release\.`)
		// Must not collide with the owner's ~/.pi/agent/memory/{project}/ layout.
		noRe(t, inj, `agent/memory/[^/]+/release-agent`)
	})
	tw(t, f, "returns empty for project scope when no project root is found", func(t *testing.T) {
		nowhere := tmp(t)
		// Skip deterministically if an ancestor happens to register as a project root.
		if findNearestProjectRoot(nowhere) != "" || findNearestGitRoot(nowhere) != "" {
			return
		}
		eq(t, buildAgentMemoryInjection(memAgent(mem("project", "x"), []string{"read"}), nowhere), "", "empty")
	})
	tw(t, f, "returns empty when the memory path is unsafe", func(t *testing.T) {
		eq(t, buildAgentMemoryInjection(memAgent(mem("project", ".."), []string{"read", "edit"}), memProject(t)), "", "empty")
	})
	tw(t, f, "returns empty when the memory file is an escaping symlink", func(t *testing.T) {
		project := memProject(t)
		dir := filepath.Join(project, ".pi", agentMemoryDirName, "leak")
		mkdirAll(t, dir)
		outside := filepath.Join(tmp(t), "secret.md")
		writeFile(t, outside, "leaked")
		symlink(t, outside, filepath.Join(dir, agentMemoryFile))
		eq(t, buildAgentMemoryInjection(memAgent(mem("project", "leak"), []string{"read", "edit"}), project), "", "empty")
	})
	tw(t, f, "caps memory contents to the first N lines without claiming a byte cap", func(t *testing.T) {
		project := memProject(t)
		var lines []string
		for i := 1; i <= 210; i++ {
			lines = append(lines, "l-"+strconvItoa(i))
		}
		writeMemory(t, filepath.Join(project, ".pi", agentMemoryDirName, "capped"), strings.Join(lines, "\n"))
		inj := buildAgentMemoryInjection(memAgent(mem("project", "capped"), []string{"read", "edit"}), project)
		hasRe(t, inj, `l-200\b`)
		noRe(t, inj, `l-201\b`)
		hasRe(t, inj, `first 200 lines\)`)
		noRe(t, inj, "byte-capped")
	})
}

func TestAgentMemoryRoundTrip(t *testing.T) {
	const f = "agent-memory"
	tw(t, f, "parses memory frontmatter during discovery and keeps it out of extraFields", func(t *testing.T) {
		e := newAmEnv(t)
		project := memProject(t)
		_ = e
		writeFile(t, filepath.Join(project, ".pi", "agents", "security-reviewer.md"), "---\nname: security-reviewer\ndescription: Recurring security reviewer\ntools: read, grep, bash, edit\nmemory: { scope: project, path: security-reviewer }\n---\n\nReview for threats.\n")
		a := findAgent(dAgents(t, project, ScopeProject, "").Agents, "security-reviewer")
		if a == nil {
			t.Fatal("agent should be discovered")
		}
		eq(t, *a.Memory, AgentMemoryConfig{"project", "security-reviewer"}, "memory")
		for _, kv := range a.ExtraFields {
			eq(t, kv.Key == "memory", false, "memory must not leak into extraFields")
		}
	})
	tw(t, f, "serializes memory back into frontmatter", func(t *testing.T) {
		s := serializeAgent(memAgent(&AgentMemoryConfig{"user", "release-agent"}, nil), SerializeAgentOptions{})
		hasRe(t, s, `(?m)^memory:$`)
		hasRe(t, s, `(?m)^  scope: user$`)
		hasRe(t, s, `(?m)^  path: release-agent$`)
	})
	tw(t, f, "ignores a malformed memory block without dropping the agent", func(t *testing.T) {
		newAmEnv(t)
		project := memProject(t)
		writeFile(t, filepath.Join(project, ".pi", "agents", "bad-memory.md"), "---\nname: bad-memory\ndescription: Bad memory config\nmemory:\n  scope: galaxy\n  path: whatever\n---\n\nStill loads.\n")
		a := findAgent(dAgents(t, project, ScopeProject, "").Agents, "bad-memory")
		if a == nil {
			t.Fatal("agent should still be discovered")
		}
		eq(t, a.Memory == nil, true, "memory")
	})
}

func writeFileRaw(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
