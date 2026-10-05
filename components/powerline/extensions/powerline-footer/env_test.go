package powerline_footer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIcons(t *testing.T) {
	tw(t, "icons", "hasNerdFonts uses TERM only when TERM_PROGRAM is unset and preserves overrides", func(t *testing.T) {
		for _, k := range []string{"TERM", "TERM_PROGRAM", "POWERLINE_NERD_FONTS", "GHOSTTY_RESOURCES_DIR"} {
			setenv(t, k, nil)
		}
		setenv(t, "TERM", s("xterm-kitty"))
		eq(t, hasNerdFonts(), true, "kitty TERM=xterm-kitty should be detected")
		setenv(t, "TERM", s("xterm-256color"))
		eq(t, hasNerdFonts(), false, "plain xterm should not be detected")
		setenv(t, "TERM", s("xterm-kitty"))
		setenv(t, "TERM_PROGRAM", s("vscode"))
		eq(t, hasNerdFonts(), false, "TERM must not override a present TERM_PROGRAM")
		setenv(t, "TERM_PROGRAM", s(""))
		eq(t, hasNerdFonts(), false, "empty TERM_PROGRAM must not fall back to TERM")
		setenv(t, "TERM", s("xterm-256color"))
		setenv(t, "TERM_PROGRAM", s("WezTerm"))
		eq(t, hasNerdFonts(), true, "recognized TERM_PROGRAM remains case-insensitive")
		setenv(t, "TERM_PROGRAM", s("Kaku"))
		eq(t, hasNerdFonts(), true, "Kaku TERM_PROGRAM should be detected")
		setenv(t, "TERM_PROGRAM", s("vscode"))
		setenv(t, "POWERLINE_NERD_FONTS", s("1"))
		eq(t, hasNerdFonts(), true, "explicit enable overrides the terminal heuristic")
		setenv(t, "POWERLINE_NERD_FONTS", nil)
		setenv(t, "GHOSTTY_RESOURCES_DIR", s("/ghostty"))
		eq(t, hasNerdFonts(), true, "Ghostty marker overrides the terminal heuristic")
		setenv(t, "POWERLINE_NERD_FONTS", s("0"))
		eq(t, hasNerdFonts(), false, "explicit disable overrides Ghostty")
	})
}

// pathEnv gives a test a temporary HOME, no USERPROFILE and a custom agent dir path.
func pathEnv(t *testing.T) (home, agentDir string) {
	root := t.TempDir()
	home = filepath.Join(root, "home")
	agentDir = filepath.Join(root, "custom-agent")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	setenv(t, "HOME", &home)
	setenv(t, "USERPROFILE", nil)
	setenv(t, "PI_CODING_AGENT_DIR", nil)
	return home, agentDir
}

func TestPaths(t *testing.T) {
	tw(t, "paths", "agent dir uses non-empty PI_CODING_AGENT_DIR", func(t *testing.T) {
		_, agentDir := pathEnv(t)
		setenv(t, "PI_CODING_AGENT_DIR", &agentDir)
		eq(t, getAgentDir(), agentDir, "agent dir")
		eq(t, getAgentPath("settings.json"), filepath.Join(agentDir, "settings.json"), "agent path")
	})
	tw(t, "paths", "agent dir falls back to HOME .pi/agent for empty env values", func(t *testing.T) {
		home, _ := pathEnv(t)
		setenv(t, "PI_CODING_AGENT_DIR", s("   "))
		eq(t, getAgentDir(), filepath.Join(home, ".pi", "agent"), "agent dir")
		eq(t, getAgentPath("sessions"), filepath.Join(home, ".pi", "agent", "sessions"), "agent path")
	})
	tw(t, "paths", "agent dir normalizes tilde and file URL env values like Pi", func(t *testing.T) {
		home, agentDir := pathEnv(t)
		setenv(t, "PI_CODING_AGENT_DIR", s("~/custom-agent"))
		eq(t, getAgentDir(), filepath.Join(home, "custom-agent"), "tilde")
		eq(t, normalizeAgentDirPath("~"), home, "bare tilde")
		setenv(t, "PI_CODING_AGENT_DIR", s("file://"+agentDir))
		eq(t, getAgentDir(), agentDir, "file URL")
	})
	tw(t, "paths", "agent sessions include legacy ~/.pi/sessions only when it exists", func(t *testing.T) {
		home, agentDir := pathEnv(t)
		setenv(t, "PI_CODING_AGENT_DIR", &agentDir)
		eq(t, getAgentSessionDirs(), []string{filepath.Join(agentDir, "sessions")}, "without legacy")
		if err := os.MkdirAll(filepath.Join(home, ".pi", "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, getAgentSessionDirs(), []string{filepath.Join(agentDir, "sessions"), filepath.Join(home, ".pi", "sessions")}, "with legacy")
	})
}

func TestThinkingLevel(t *testing.T) {
	tw(t, "thinking-level", "thinking-level selection prefers the live event over stale context", func(t *testing.T) {
		eq(t, resolveThinkingLevelSelection("medium", "off"), "medium", "medium")
		eq(t, resolveThinkingLevelSelection("high", "low"), "high", "high")
	})
	tw(t, "thinking-level", "thinking-level selection falls back to context when the event has no level", func(t *testing.T) {
		eq(t, resolveThinkingLevelSelection(nil, "low"), "low", "context")
		eq(t, resolveThinkingLevelSelection(nil, nil), nil, "neither")
	})
}
