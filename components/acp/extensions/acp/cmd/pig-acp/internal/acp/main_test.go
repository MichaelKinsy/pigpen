package acp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates every test from the real configuration: HOME, PIG_HOME and the agent
// directories point into a temporary directory, so nothing reads or writes ~/.pig.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "pig-acp-test-*")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME": filepath.Join(root, "home"), "PIG_HOME": filepath.Join(root, "pighome"),
		"PIG_CODING_AGENT_DIR": filepath.Join(root, "agent"), "PI_CODING_AGENT_DIR": filepath.Join(root, "piagent"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg"), "PIG_USE_PI_DIRS": "", "PI_ACP_PI_COMMAND": "", "PIG_ACP_PIG_COMMAND": "",
		"PI_ACP_ENABLE_EMBEDDED_CONTEXT": "", "PIG_ACP_ENABLE_EMBEDDED_CONTEXT": "",
		"PIG_CODING_AGENT_SESSION_DIR": "", "PI_CODING_AGENT_SESSION_DIR": "",
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}
