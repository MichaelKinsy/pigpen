package pi_permission_system

import (
	"path/filepath"
	"testing"
)

// The agent directory the original reads from Pi (getAgentDir()) and the global config path under it: the default, a relative and an
// absolute override, and the extension's own directory. (Mutants agent-dir-default and config-path.)
func TestAgentDirAndGlobalConfigPath(t *testing.T) {
	old := homeDir
	homeDir = func() string { return filepath.FromSlash("/home/u") }
	t.Cleanup(func() { homeDir = old })
	abs := filepath.FromSlash

	t.Setenv("PI_CODING_AGENT_DIR", "")
	eq(t, agentDir(), abs("/home/u/.pi/agent"), "the default is ~/.pi/agent")
	eq(t, globalConfigPath(), abs("/home/u/.pi/agent/extensions/pi-permission-system/config.json"), "the config under the extension's directory")

	t.Setenv("PI_CODING_AGENT_DIR", "  \t ")
	eq(t, agentDir(), abs("/home/u/.pi/agent"), "a blank override is no override")

	t.Setenv("PI_CODING_AGENT_DIR", "custom/agent")
	eq(t, agentDir(), abs("/home/u/custom/agent"), "a relative override is under the home directory")

	t.Setenv("PI_CODING_AGENT_DIR", abs("/srv/agent/../agent2"))
	eq(t, agentDir(), abs("/srv/agent2"), "an absolute override is cleaned")
	eq(t, globalConfigPath(), abs("/srv/agent2/extensions/pi-permission-system/config.json"), "the config follows the override")
}
