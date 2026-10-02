package acp

import (
	"os"
	"path/filepath"
	"strings"
)

func sharedPiDirs() bool { return os.Getenv("PIG_USE_PI_DIRS") == "1" }

func userHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// expandTilde expands a leading "~" or "~/" as pig does for its directory variables
// (internal/codingagent ExpandTildePath). An editor passes env values unexpanded.
func expandTilde(p string) string {
	if p == "~" {
		return userHome()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(userHome(), rest)
	}
	return p
}

// pigHome is PiG's config root: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig. PiG keeps its own
// state here even in shared mode (PIG_USE_PI_DIRS=1), which shares only the agent and project
// directories with Pi.
func pigHome() string {
	if h := os.Getenv("PIG_HOME"); h != "" {
		return expandTilde(h)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(expandTilde(x), "pig")
	}
	return filepath.Join(userHome(), ".pig")
}

// AgentDir is PiG's agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME>/agent; with
// PIG_USE_PI_DIRS=1 it is Pi's (PI_CODING_AGENT_DIR, else ~/.pi/agent).
func AgentDir() string {
	if sharedPiDirs() {
		if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
			return absPath(expandTilde(d))
		}
		return filepath.Join(userHome(), ".pi", "agent")
	}
	if d := os.Getenv("PIG_CODING_AGENT_DIR"); d != "" {
		return absPath(expandTilde(d))
	}
	return filepath.Join(pigHome(), "agent")
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// ProjectDirName is ".pig", or ".pi" when PIG_USE_PI_DIRS=1.
func ProjectDirName() string {
	if sharedPiDirs() {
		return ".pi"
	}
	return ".pig"
}

// AcpDir is where the adapter keeps its own state, apart from pig's agent directory.
func AcpDir() string { return filepath.Join(pigHome(), "pig-acp") }

// SessionMapPath is the file the default store uses.
func SessionMapPath() string { return filepath.Join(AcpDir(), "session-map.json") }
