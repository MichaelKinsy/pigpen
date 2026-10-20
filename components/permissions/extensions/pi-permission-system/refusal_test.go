package pi_permission_system

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The refusal text and one test per refused option (refusal.go): every call the port cannot decide yet is blocked with the exact
// text, never allowed.

func TestRefusalText(t *testing.T) {
	eq(t, refusal("the approval dialog"), "@pi-in-go/pigpen-permissions (Go port) does not support the approval dialog yet. "+
		"Install the original extension, npm:@gotgenes/pi-permission-system@39.0.3, which runs unchanged in PiG.", "text")
}

// identity is testdata/identity.json: the Package name and the pinned original, copied from the files that declare them. The
// mutation harness runs a copy of this module alone, so the constants are checked against the copy, and against the declaring
// files whenever the test runs inside the repository.
type identity struct {
	Package         string `json:"package"`
	OriginalName    string `json:"originalName"`
	OriginalVersion string `json:"originalVersion"`
}

func readJSONFile(t *testing.T, path string, into any) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(path, err)
	}
	return true
}

// The text names the Package and the pinned original: they must be the ones the Package declares.
func TestRefusalNamesThePackageAndTheOriginal(t *testing.T) {
	var id identity
	if !readJSONFile(t, "testdata/identity.json", &id) {
		t.Fatal("testdata/identity.json is missing")
	}
	eq(t, packageName, id.Package, "the Package name")
	eq(t, originalSpec, "npm:"+id.OriginalName+"@"+id.OriginalVersion, "the pinned original")

	var pkg struct{ Name string }
	if readJSONFile(t, "../../package.json", &pkg) {
		eq(t, pkg.Name, id.Package, "package.json name")
	}
	var orig struct{ Name, Version string }
	if readJSONFile(t, "../../port/oracle/packages/pi-permission-system/package.json", &orig) {
		eq(t, orig.Name, id.OriginalName, "the vendored original's name")
		eq(t, orig.Version, id.OriginalVersion, "the vendored original's version")
	}
	var prov struct{ Upstreams []struct{ Name string } }
	if readJSONFile(t, "../../provenance.json", &prov) {
		// The original first; the tree-sitter runtime and grammar the bash parser is ported from follow it.
		if len(prov.Upstreams) == 0 {
			t.Fatal("provenance.json lists no upstream")
		}
		eq(t, prov.Upstreams[0].Name, id.OriginalName, "provenance.json names the original")
	}
}

// Each refused option, in the exact words: the call is blocked and the reason is the refusal of that feature.
func TestEveryRefusalIsExact(t *testing.T) {
	const wide = `"path":"allow","external_directory":"allow"`
	for _, c := range []struct {
		name, config, tool string
		input              map[string]any
		feature            string
	}{
		{"ask", `{"permission":{` + wide + `,"bash":{"*":"ask"}}}`, "bash", map[string]any{"command": "ls"},
			"the approval dialog (the 'bash' call needs approval)"},
		{"ask on an extension tool", `{"permission":{"foo":"ask"}}`, "foo", nil, "the approval dialog (the 'foo' call needs approval)"},
		{"a wrapper's floored allow", `{"permission":{` + wide + `,"bash":"allow"}}`, "bash", map[string]any{"command": "sudo ls"},
			"the approval dialog (the 'bash' call needs approval)"},
		{"unreadable bash, catch-all allows", `{"permission":{` + wide + `,"bash":"allow"}}`, "bash", map[string]any{"command": "echo $(ls)"},
			"analysing this bash command (only simple commands joined by &&, ||, ;, | or newlines can be checked for a wrapper the original asks about)"},
		{"unreadable bash, patterns", `{"permission":{` + wide + `,"bash":{"*":"deny","ls":"allow"}}}`, "bash", map[string]any{"command": "echo \"x\""},
			"matching this bash command against bash patterns (only simple commands joined by &&, ||, ;, | or newlines are supported)"},
		{"a command that runs another", `{"permission":{` + wide + `,"bash":"allow"}}`, "bash", map[string]any{"command": "ssh host ls"},
			"checking the command that 'ssh' runs"},
		{"bash and the path surfaces", `{"permission":{"bash":"allow"}}`, "bash", map[string]any{"command": "ls"},
			"path checks for the 'bash' tool (a 'path_read' surface that does not allow every path)"},
		{"path patterns", `{"permission":{"path":{"*":"allow","*.env":"deny"},"external_directory":"allow","bash":"allow"}}`, "bash", map[string]any{"command": "ls"},
			"path checks for the 'bash' tool (path rules with value patterns)"},
		{"a path tool with patterns", `{"permission":{` + wide + `,"read":{"*":"deny","*.md":"allow"}}}`, "read", map[string]any{"path": "a.md"},
			"path rules with value patterns for the 'read' tool (path matching)"},
		{"a path tool and the path surfaces", `{"permission":{"read":"allow"}}`, "edit", map[string]any{"path": "a"},
			"path checks for the 'edit' tool (a 'path_read' surface that does not allow every path)"},
		{"shellTools", `{"permission":{` + wide + `,"exec_command":"allow"},"shellTools":{"exec_command":{"commandArgument":"cmd"}}}`, "exec_command", map[string]any{"cmd": "ls"},
			"the 'shellTools' option (the 'exec_command' tool routed through the bash surface)"},
		{"the MCP proxy", `{"permission":{"mcp":"allow"}}`, "mcp", nil, "permissions for the 'mcp' tool (the MCP and skill surfaces)"},
		{"the skill tool", `{"permission":{"skill":"allow"}}`, "skill", nil, "permissions for the 'skill' tool (the MCP and skill surfaces)"},
		{"a built-in MCP tool", `{"permission":{"mcp":"allow"}}`, "mcp__srv__tool", nil, "permissions for the 'mcp__srv__tool' tool (the MCP and skill surfaces)"},
	} {
		cfg := parseConfig([]byte(c.config), "config.json")
		eq(t, cfg.Issue, "", c.name+": the config is valid")
		v := NewPolicy(cfg)
		got := v.Decide(c.tool, c.input)
		eq(t, got, Verdict{Block: true, Reason: refusal(c.feature)}, c.name)
		if !strings.HasSuffix(got.Reason, "which runs unchanged in PiG.") {
			t.Errorf("%s: the refusal names no working alternative: %q", c.name, got.Reason)
		}
	}
}

func TestSkillReadRefusalIsExact(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"ask"}}`)
	entries := p.visibleSkillEntries("<available_skills><skill><name>demo</name><description>d</description><location>/s/demo/SKILL.md</location></skill></available_skills>", "/cwd")
	eq(t, skillRead("read", map[string]any{"path": "/s/demo/SKILL.md"}, entries, "/cwd"),
		Verdict{Block: true, Reason: refusal("the approval dialog (reading a file of the skill 'demo' needs approval)")}, "skill read")
}
