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

// Files under a .agents/skills directory are skills, never agent definitions: the inspection of an agents directory skips them
// (agents.ts inspectAgentDefinitionDirectory), so a directory that holds only skills is empty.
func TestInspectionSkipsLegacySkillFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".agents", "skills", "skill.md"), "---\nname: skill\ndescription: A skill\n---\n\nBody.\n")
	insp := inspectAgentDefinitionDirectory(dir, nil)
	if insp.state != "empty" || len(insp.files) != 0 {
		t.Errorf("state %q, files %v", insp.state, insp.files)
	}
	writeFile(t, filepath.Join(dir, "agent.md"), "---\nname: agent\ndescription: An agent\n---\n\nBody.\n")
	insp = inspectAgentDefinitionDirectory(dir, nil)
	if insp.state != "candidates" || len(insp.files) != 1 || filepath.Base(insp.files[0]) != "agent.md" {
		t.Errorf("state %q, files %v", insp.state, insp.files)
	}
}
