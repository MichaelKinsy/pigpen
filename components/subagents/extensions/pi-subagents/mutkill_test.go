package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

// The list action orders the agents of a section by name, not by the order of the files that define them.
func TestListOrdersAgentsByName(t *testing.T) {
	e := newMgmtEnv(t)
	writeFile(t, filepath.Join(e.dir, ".pi", "agents", "a-file.md"), "---\nname: zeta\ndescription: Last by name\n---\n\nZ.\n")
	writeFile(t, filepath.Join(e.dir, ".pi", "agents", "b-file.md"), "---\nname: alpha\ndescription: First by name\n---\n\nA.\n")
	text := handleManagementAction("list", map[string]any{"agentScope": "project"}, e.ctx()).text
	alpha, zeta := strings.Index(text, "- alpha "), strings.Index(text, "- zeta ")
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Errorf("alpha at %d, zeta at %d:\n%s", alpha, zeta, text)
	}
}
