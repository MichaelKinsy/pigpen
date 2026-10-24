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
		eq(t, len(prov.Upstreams), 1, "one upstream")
		eq(t, prov.Upstreams[0].Name, id.OriginalName, "provenance.json names the original")
	}
}

// Each refused option, in the exact words: the call is blocked and the reason is the refusal of that feature. The
// ask, the wrapper floors and the path-surface checks are no longer refusals: they are asks (ask.go) and path
// gates (paths.go), decided by the dialog or the config.
func TestEveryRefusalIsExact(t *testing.T) {
	const wide = `"path":"allow","external_directory":"allow"`
	for _, c := range []struct {
		name, config, tool string
		input              map[string]any
		feature            string
	}{
		{"unreadable bash against patterns", `{"permission":{` + wide + `,"bash":{"*":"deny","ls":"allow"}}}`, "bash", map[string]any{"command": "echo \"x\""},
			"matching this bash command against bash patterns (only simple commands joined by &&, ||, ;, | or newlines are supported)"},
		{"a path tool with patterns", `{"permission":{` + wide + `,"read":{"*":"deny","*.md":"allow"}}}`, "read", map[string]any{"path": "a.md"},
			"path rules with value patterns for the 'read' tool (path matching)"},
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

// A read of a listed skill's file while the skill surface asks is an ask, not a refusal: the dialog decides it,
// and the ask carries no session option (the original's skill-read gate holds no SessionApproval).
func TestSkillReadIsAnAsk(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"ask"}}`)
	entries := p.visibleSkillEntries("<available_skills><skill><name>demo</name><description>d</description><location>/s/demo/SKILL.md</location></skill></available_skills>", "/cwd")
	got := skillRead("read", map[string]any{"path": "/s/demo/SKILL.md"}, entries, "/cwd")
	eq(t, got.Block, false, "an ask does not block by itself")
	eq(t, got.Ask != nil && got.Ask.Surface == "skill" && got.Ask.Value == "demo" && got.Ask.Tool == "read", true, "the skill asks")
	eq(t, got.Ask.Session.Surface, "", "no session option")
}
