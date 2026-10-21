package tintinweb_subagents

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDecodeUTF8Lossy(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain é 😀", "plain é 😀"},
		{"a\xffb", "a\uFFFDb"},
		{"a\xff\xffb", "a\uFFFD\uFFFDb"},
		// a truncated 3-byte sequence is one maximal subpart
		{"\xe2\x82A", "\uFFFDA"},
		// a surrogate is three
		{"\xed\xa0\x80", "\uFFFD\uFFFD\uFFFD"},
		{"\xf0\x9f\x98", "\uFFFD"},
		{"\xc0\xaf", "\uFFFD\uFFFD"},
	} {
		if got := decodeUTF8Lossy([]byte(c.in)); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

// The original gives general-purpose no prompt of its own (default-agents.ts: systemPrompt ""); it once carried
// Explore's read-only text.
func TestGeneralPurposeHasNoPromptOfItsOwn(t *testing.T) {
	eq(t, defaultAgents().get("general-purpose").SystemPrompt, "")
}

// In an appended prompt the worktree notice follows the environment block and comes before the agent's instructions.
func TestWorktreeNoticeSitsBetweenTheEnvironmentAndTheInstructions(t *testing.T) {
	cfg := &agentConfig{Name: "w", PromptMode: "append", SystemPrompt: "MY INSTRUCTIONS"}
	got := buildAgentPrompt(cfg, "/copy", envInfo{Platform: "linux"}, "PARENT", promptExtras{WorktreeBase: "/main"})
	env, notice, instr := strings.Index(got, "# Environment"), strings.Index(got, "<worktree_isolation>"), strings.Index(got, "<agent_instructions>")
	eq(t, env >= 0 && env < notice && notice < instr, true)
}

func fakeGit(res execResult) gitFn {
	return func([]string, string, int) execResult { return res }
}

func TestGitFailureMessages(t *testing.T) {
	_, err := git(fakeGit(execResult{Stderr: " boom \n", Code: 1}), "/", []string{"a"}, 1)
	eq(t, err.Error(), "boom")
	_, err = git(fakeGit(execResult{Code: 3}), "/", []string{"a", "b"}, 1)
	eq(t, err.Error(), "git a b failed (exit 3)")
	_, err = git(fakeGit(execResult{Killed: true}), "/", []string{"a"}, 1)
	eq(t, err != nil, true)
	out, err := git(fakeGit(execResult{Stdout: " sha \n"}), "/", []string{"a"}, 1)
	eq(t, out, "sha")
	eq(t, err, nil)
}

func TestOsGitReportsAKilledCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs git and a POSIX shell for the alias")
	}
	start := time.Now()
	res := osGit([]string{"-c", "alias.nap=!sleep 5", "nap"}, t.TempDir(), 200)
	eq(t, res.Killed, true)
	eq(t, time.Since(start) < 4*time.Second, true) // the shell's child does not hold the call up
}

func TestCleanupOfAGoneWorktreeAsksGitNothing(t *testing.T) {
	calls := 0
	run := func([]string, string, int) execResult { calls++; return execResult{} }
	res := cleanupWorktree(run, "/repo", &worktreeInfo{Path: filepath.Join(t.TempDir(), "gone")}, "d")
	eq(t, res, worktreeCleanupResult{})
	eq(t, calls, 0)
}

func TestAFailedCleanupStillRemovesTheWorktree(t *testing.T) {
	var mu sync.Mutex
	var removed bool
	run := func(args []string, cwd string, _ int) execResult {
		mu.Lock()
		defer mu.Unlock()
		if len(args) > 1 && args[0] == "worktree" && args[1] == "remove" {
			removed = true
			return execResult{}
		}
		return execResult{Code: 1, Stderr: "no"}
	}
	res := cleanupWorktree(run, "/repo", &worktreeInfo{Path: t.TempDir()}, "d")
	eq(t, res, worktreeCleanupResult{})
	eq(t, removed, true)
}

func TestEnvOfAWorktreeWhoseBranchIsUnknown(t *testing.T) {
	run := func(args []string, _ string, _ int) execResult {
		if args[0] == "rev-parse" {
			return execResult{Stdout: "true\n"}
		}
		return execResult{Code: 1}
	}
	env := detectEnvVia(run, "/w")
	eq(t, env.IsGitRepo, true)
	eq(t, env.Branch, "unknown")
}

func TestAnUnknownMemoryScopeIsAnError(t *testing.T) {
	_, err := resolveMemoryDir("a", "bogus", "/w")
	eq(t, err != nil, true)
}
