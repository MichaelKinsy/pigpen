package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func mkNamed(name, local string) AgentConfig {
	return AgentConfig{Name: name, LocalName: local, Description: name + " agent", SystemPromptMode: "replace", SystemPrompt: "Inspect", Source: SourceProject, FilePath: filepath.Join(".pi", "agents", name+".md")}
}

func TestResolveAgentName(t *testing.T) {
	const f = "agent-name-resolution"
	tw(t, f, "prefers an exact canonical name over a packaged local name", func(t *testing.T) {
		plain, packaged := mkNamed("scout", ""), mkNamed("code-analysis.scout", "scout")
		agents := []AgentConfig{plain, packaged}
		a, _ := ResolveAgentName("scout", agents)
		eq(t, a, &agents[0], "plain")
		b, _ := ResolveAgentName("code-analysis.scout", agents)
		eq(t, b, &agents[1], "packaged")
	})
	tw(t, f, "uses a unique packaged local name when no canonical name exists", func(t *testing.T) {
		agents := []AgentConfig{mkNamed("code-analysis.scout", "scout")}
		a, _ := ResolveAgentName("scout", agents)
		eq(t, a, &agents[0], "packaged")
	})
	tw(t, f, "rejects a local name shared by multiple packaged agents", func(t *testing.T) {
		_, msg := ResolveAgentName("scout", []AgentConfig{mkNamed("code-analysis.scout", "scout"), mkNamed("repository.scout", "scout")})
		if !regexp.MustCompile(`Ambiguous local agent name 'scout': code-analysis\.scout, repository\.scout`).MatchString(msg) {
			t.Errorf("message %q", msg)
		}
	})
}

func TestFindConfiguredProjectRoot(t *testing.T) {
	tw(t, "agent-name-resolution", "does not reinterpret user config reached through a home alias as project config", func(t *testing.T) {
		isolated := tmp(t)
		home := filepath.Join(isolated, "home")
		alias := filepath.Join(isolated, "home-alias")
		if err := os.Mkdir(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(home, alias); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		nested := filepath.Join(alias, "agent-project-x")
		if err := os.Mkdir(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(home, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, FindConfiguredProjectRoot(nested), "", "no project")
		if err := os.Mkdir(filepath.Join(nested, ".pi"), 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, FindConfiguredProjectRoot(nested), nested, "project")
	})
}
