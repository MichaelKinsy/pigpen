package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReadsPathResolution(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	chainDir := filepath.Join(t.TempDir(), "chain")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	tw(t, "reads-resolution", "expands bare ~ to the home directory", func(t *testing.T) { equal(t, expandHomePath("~"), home, "path") })
	tw(t, "reads-resolution", "expands ~/ to the home directory", func(t *testing.T) { equal(t, expandHomePath("~/"), home, "path") })
	tw(t, "reads-resolution", "expands ~/file to <home>/file", func(t *testing.T) { equal(t, expandHomePath("~/.zprofile"), filepath.Join(home, ".zprofile"), "path") })
	tw(t, "reads-resolution", "leaves absolute paths unchanged", func(t *testing.T) { equal(t, expandHomePath("/etc/hosts"), "/etc/hosts", "path") })
	tw(t, "reads-resolution", "leaves repository-relative paths unchanged", func(t *testing.T) { equal(t, expandHomePath("docs/x.md"), "docs/x.md", "path") })
	tw(t, "reads-resolution", "expands ~ before resolving", func(t *testing.T) {
		equal(t, resolveChainPath("~/.zprofile", chainDir), filepath.Join(home, ".zprofile"), "path")
	})
	tw(t, "reads-resolution", "passes absolute paths through", func(t *testing.T) { equal(t, resolveChainPath("/etc/hosts", chainDir), "/etc/hosts", "path") })
	tw(t, "reads-resolution", "prepends chainDir to repository-relative paths", func(t *testing.T) {
		equal(t, resolveChainPath("docs/x.md", chainDir), filepath.Join(chainDir, "docs/x.md"), "path")
	})
}

func TestPathHandling(t *testing.T) {
	win := runtimeIsWindows()
	tw(t, "path-handling", "startsWith('/') misses Windows absolute paths", func(t *testing.T) {
		for _, p := range []string{`C:\dev\pi-subagents\output.md`, "C:/dev/pi-subagents/output.md"} {
			equal(t, strings.HasPrefix(p, "/"), false, "prefix check on "+p)
			equal(t, filepath.IsAbs(p), win, "IsAbs on "+p)
		}
		equal(t, strings.HasPrefix("/home/user/output.md", "/"), true, "posix prefix")
		equal(t, filepath.IsAbs("/home/user/output.md"), !win, "posix IsAbs")
	})
	tw(t, "path-handling", "path.isAbsolute is the correct cross-platform check", func(t *testing.T) {
		equal(t, filepath.IsAbs("output.md"), false, "relative")
		equal(t, filepath.IsAbs("subdir/output.md"), false, "relative dir")
		equal(t, strings.HasPrefix("output.md", "/"), false, "prefix")
		if win {
			for _, p := range []string{`C:\output.md`, "C:/output.md", `\\server\share`} {
				equal(t, filepath.IsAbs(p), true, p)
			}
		}
	})
	tw(t, "path-handling", "template concatenation produces forward slashes regardless of platform", func(t *testing.T) {
		chainDir := `C:\Users\marc\temp\chain-abc`
		equal(t, chainDir+"/progress.md", `C:\Users\marc\temp\chain-abc/progress.md`, "template")
		if win {
			equal(t, filepath.Join(chainDir, "progress.md"), `C:\Users\marc\temp\chain-abc\progress.md`, "join")
		}
	})
	tw(t, "path-handling", "resolveChainPath pattern should use path.join for relative paths", func(t *testing.T) {
		chainDir := `C:\temp\chain-runs\abc123`
		current, fixed := chainDir+"/synthesis.md", filepath.Join(chainDir, "synthesis.md")
		if win {
			if current == fixed || !strings.ContainsRune(fixed, filepath.Separator) {
				t.Errorf("current %q fixed %q", current, fixed)
			}
		}
	})
	tw(t, "path-handling", "parallel subdir naming should use path.join", func(t *testing.T) {
		sub := filepath.Join("parallel-0", "0-_code-reviewer")
		equal(t, filepath.Join(sub, "review.md"), filepath.Join("parallel-0", "0-_code-reviewer", "review.md"), "join")
	})
}

func runtimeIsWindows() bool { return windowsHost() }

func TestPathResolutionForAgents(t *testing.T) {
	home := withTempHome(t)
	cwd := filepath.Join(t.TempDir(), "cwd")
	os.MkdirAll(cwd, 0o755)
	userAgents := filepath.Join(home, ".agents")
	os.MkdirAll(filepath.Join(userAgents, "skills"), 0o755)
	canary := filepath.Join(userAgents, ".data-loss-canary")
	writeFile(t, canary, "preserve me")
	t.Cleanup(func() {
		b, _ := os.ReadFile(canary)
		equal(t, string(b), "preserve me", "the canary survives")
	})
	tskip(t, "path-resolution", "should resolve skills in .agents/skills", "tests resolveSkillPath of the original's skills discovery (agents/skills.ts), which the profiles slice ports")
	tskip(t, "path-resolution", "should resolve skills in ~/.agents/skills", "tests resolveSkillPath of the original's skills discovery (agents/skills.ts), which the profiles slice ports")
	tw(t, "path-resolution", "should resolve project agents from both .agents and .pi/agents", func(t *testing.T) {
		legacy := filepath.Join(cwd, ".agents")
		agents := filepath.Join(cwd, ".pi", "agents")
		os.MkdirAll(filepath.Join(legacy, "skills"), 0o755)
		os.MkdirAll(agents, 0o755)
		writeFile(t, filepath.Join(legacy, "test-agent-legacy.md"), "---\nname: test-agent-legacy\ndescription: Legacy agent\n---\nLegacy content")
		writeFile(t, filepath.Join(agents, "test-agent-1.md"), "---\nname: test-agent-1\ndescription: Test agent\n---\nAgent content")
		d := DiscoverAgents(cwd, ScopeProject)
		legacyAgent, agent := findAgent(d.Agents, "test-agent-legacy"), findAgent(d.Agents, "test-agent-1")
		if legacyAgent == nil || agent == nil {
			t.Fatalf("agents: %+v", d.Agents)
		}
		equal(t, legacyAgent.FilePath, filepath.Join(legacy, "test-agent-legacy.md"), "legacy path")
		equal(t, agent.FilePath, filepath.Join(agents, "test-agent-1.md"), "agent path")
	})
	tw(t, "path-resolution", "should resolve agents in ~/.agents", func(t *testing.T) {
		writeFile(t, filepath.Join(userAgents, "test-agent-2.md"), "---\nname: test-agent-2\ndescription: Test agent\n---\nAgent content")
		d := DiscoverAgents(cwd, ScopeUser)
		a := findAgent(d.Agents, "test-agent-2")
		if a == nil {
			t.Fatalf("agents: %+v", d.Agents)
		}
		equal(t, a.FilePath, filepath.Join(userAgents, "test-agent-2.md"), "path")
	})
}

func TestBoundedIndexSegments(t *testing.T) {
	hashed := regexp.MustCompile(`^~sha256-[a-f0-9]{64}$`)
	enc := func(s string) string { return encodeIndexSegment(s, maxIndexSegmentBytes) }
	tw(t, "index-segment", "preserves legacy encoding for short values and hashes oversized opaque ids", func(t *testing.T) {
		got, _ := jsEncodeURIComponent("call/a+b=")
		equal(t, enc("call/a+b="), got, "short")
		equal(t, got, "call%2Fa%2Bb%3D", "encodeURIComponent")
		equal(t, enc(strings.Repeat("x", 255)), strings.Repeat("x", 255), "255 chars")
		equal(t, hashed.MatchString(enc(strings.Repeat("x", 256))), true, "256 chars")
		for _, unsafe := range []string{"", ".", "..", "CON", "nul.txt", "trailing."} {
			equal(t, hashed.MatchString(enc(unsafe)), true, "unsafe "+unsafe)
		}
		long := "call_" + strings.Repeat("opaque/+=", 80)
		first := enc(long)
		equal(t, first, enc(long), "deterministic")
		equal(t, hashed.MatchString(first), true, "long id")
		equal(t, len(first) <= maxIndexSegmentBytes, true, "bounded")
		equal(t, indexSegmentAliases(long, maxIndexSegmentBytes), []string{first}, "aliases")
		equal(t, first != enc(long+"other"), true, "distinct")
	})
	tw(t, "index-segment", "hashes Windows session file paths that stay under the byte limit", func(t *testing.T) {
		id := `C:\Users\theap\.pi\agent\sessions\--C--Users-theap--\2026-08-17T07-29-51-353Z_01a00ea0-72f9-754f-82ca-2b74ded94746.jsonl`
		e, _ := jsEncodeURIComponent(id)
		equal(t, len(e) <= maxIndexSegmentBytes, true, "fits")
		equal(t, hashed.MatchString(enc(id)), true, "hashed")
		equal(t, enc(id), enc(id), "deterministic")
		equal(t, hashed.MatchString(enc("session.jsonl")), true, "session.jsonl")
		equal(t, indexSegmentAliases(id, maxIndexSegmentBytes), []string{enc(id), e}, "aliases")
		equal(t, indexSegmentAliases("session-a", maxIndexSegmentBytes), []string{"session-a"}, "short aliases")
	})
	tskip(t, "index-segment", "indexes and releases active runs with oversized provider tool-call ids", "tests updateActiveRunIndex, readActiveRunToolCallIndex and releaseActiveRunIndex of the original's background active-run index (active-run-index.ts), which the async lane ports")
	tskip(t, "index-segment", "throws non-missing tool-call index read failures", "mocks node:fs readdirSync (t.mock.method) around the original's active-run index, which the async lane ports")
	tskip(t, "index-segment", "keeps the authoritative marker when optional tool-call indexing fails", "tests the original's background active-run index (active-run-index.ts), which the async lane ports")
	tskip(t, "index-segment", "readStatus safely handles oversized directory paths without throwing ENAMETOOLONG", "tests readStatus of the original's async status files (utils.ts), which the async lane ports")
	tskip(t, "index-segment", "surfaces unrelated ENAMETOOLONG errors from status inspection", "mocks node:fs statSync around readStatus of the original's async status files, which the async lane ports")
	tskip(t, "index-segment", "surfaces ENAMETOOLONG errors from status reads", "mocks node:fs readFileSync around readStatus of the original's async status files, which the async lane ports")
}
