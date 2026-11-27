package modes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/commit"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-commit.ts:1-42
// upstream: packages/coding-agent/src/prompts/commit-request.md
func TestRenderCommitRequest(t *testing.T) {
	t.Run("with instructions and recent subjects", func(t *testing.T) {
		got := RenderCommitRequest(
			"Selected:\n  a.go (+2 -0)",
			[]string{"feat: add foo", "fix: bar"},
			"  split tests separately  ",
		)
		if !strings.Contains(got, "The user ran `/commit` with: split tests separately") {
			t.Fatalf("missing instructions line:\n%s", got)
		}
		if !strings.Contains(got, "```\nSelected:\n  a.go (+2 -0)\n```") {
			t.Fatalf("missing draft summary:\n%s", got)
		}
		if !strings.Contains(got, "### Recent subjects\n\n- feat: add foo\n- fix: bar") {
			t.Fatalf("missing recent subjects:\n%s", got)
		}
		if !strings.Contains(got, "NEVER use `git add`, `git commit`, `git stash`, `git reset`, `git checkout`, or `git restore` for this commit.") {
			t.Fatalf("missing critical block:\n%s", got)
		}
	})

	t.Run("without instructions or recent subjects", func(t *testing.T) {
		got := RenderCommitRequest("Nothing selected.", nil, "")
		if !strings.Contains(got, "The user ran `/commit`\n") {
			t.Fatalf("expected bare `/commit` line:\n%s", got)
		}
		if strings.Contains(got, " with: ") {
			t.Fatalf("unexpected instructions clause:\n%s", got)
		}
		if strings.Contains(got, "### Recent subjects") {
			t.Fatalf("unexpected recent subjects section:\n%s", got)
		}
	})

	t.Run("does not re-expand template syntax inside summary or instructions", func(t *testing.T) {
		got := RenderCommitRequest("{{instructions}}", []string{"{{summary}}"}, "{{recent}}")
		if !strings.Contains(got, "The user ran `/commit` with: {{recent}}") {
			t.Fatalf("instructions were re-expanded:\n%s", got)
		}
		if !strings.Contains(got, "```\n{{instructions}}\n```") {
			t.Fatalf("summary was re-expanded:\n%s", got)
		}
		if !strings.Contains(got, "- {{summary}}") {
			t.Fatalf("recent subject was re-expanded:\n%s", got)
		}
	})
}

func TestCommitSlashCommand(t *testing.T) {
	// Ensure commit prelude is registered.
	commit.Register(sdk.New("commit-test"))
	origSetting := commit.Setting
	t.Cleanup(func() { commit.Setting = origSetting })
	t.Run("warns when eval is not active", func(t *testing.T) {
		commit.Setting = func(_ sdk.Context, key string) any {
			if key == commit.SettingCommitEnabled {
				return true
			}
			return nil
		}
		_, h := newTestExtension(t, hostOptions{
			Cwd:         t.TempDir(),
			ActiveTools: []string{"read", "bash"},
		})
		if _, errMsg := h.callCommand("commit", ""); errMsg != "" {
			t.Fatalf("callCommand: %s", errMsg)
		}
		if calls := h.callsTo("sendUserMessage"); len(calls) != 0 {
			t.Fatalf("expected no prompt sent when eval inactive, got %d", len(calls))
		}
		found := false
		for _, n := range h.notifications() {
			if strings.Contains(n, "/commit needs the eval tool") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected usage warning, got %v", h.notifications())
		}
	})

	t.Run("warns when commit.enabled is false", func(t *testing.T) {
		commit.Setting = func(_ sdk.Context, key string) any {
			if key == commit.SettingCommitEnabled {
				return false
			}
			return nil
		}
		_, h := newTestExtension(t, hostOptions{
			Cwd:         t.TempDir(),
			ActiveTools: []string{"read", "eval"},
		})
		if _, errMsg := h.callCommand("commit", ""); errMsg != "" {
			t.Fatalf("callCommand: %s", errMsg)
		}
		if calls := h.callsTo("sendUserMessage"); len(calls) != 0 {
			t.Fatalf("expected no prompt sent when commit.enabled=false, got %d", len(calls))
		}
		found := false
		for _, n := range h.notifications() {
			if strings.Contains(n, "/commit needs the eval tool") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected usage warning, got %v", h.notifications())
		}
	})

	t.Run("drafts changes, includes recent subjects, and sends commit request prompt", func(t *testing.T) {
		commit.Setting = func(_ sdk.Context, key string) any {
			if key == commit.SettingCommitEnabled {
				return true
			}
			return nil
		}
		repo := t.TempDir()
		runGit := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		runGit("init", "-b", "main")
		runGit("config", "user.email", "test@example.com")
		runGit("config", "user.name", "Test User")
		if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", "hello.txt")
		runGit("commit", "-m", "feat: initial hello")
		if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, h := newTestExtension(t, hostOptions{
			Cwd:         repo,
			ActiveTools: []string{"read", "eval"},
		})
		if _, errMsg := h.callCommand("commit", "keep it short"); errMsg != "" {
			t.Fatalf("callCommand: %s", errMsg)
		}
		calls := h.callsTo("sendUserMessage")
		if len(calls) != 1 {
			t.Fatalf("expected 1 sent user prompt, got %d (notifies: %v)", len(calls), h.notifications())
		}
		msg, _ := calls[0].Args["content"].(string)
		if !strings.Contains(msg, "The user ran `/commit` with: keep it short") {
			t.Fatalf("missing instructions in prompt:\n%s", msg)
		}
		if !strings.Contains(msg, "- feat: initial hello") {
			t.Fatalf("missing recent subject in prompt:\n%s", msg)
		}
		if !strings.Contains(msg, "hello.txt") {
			t.Fatalf("missing changed file in draft summary:\n%s", msg)
		}
	})
}
