package tintinweb_subagents

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twin of test/isolation-param.test.ts. upstream: src/invocation-config.ts isolationParam. The parameter is the text in
// agent_text.json, added to the Agent schema by agentSchemaFor when worktrees are allowed.
func TestIsolationParamTwin(t *testing.T) {
	const f = "isolation-param"
	dc := descriptionContext{reg: defaultAgents(), agentDir: "/agents"}
	param := func(enabled bool) map[string]any {
		schema := agentSchemaFor(descriptionFlags{worktreeIsolation: enabled, mode: "full"}, dc, loadToolSpecs()["Agent"])
		props, _ := schema["properties"].(map[string]any)
		p, _ := props["isolation"].(map[string]any)
		return p
	}
	values := func() []any {
		var out []any
		for _, v := range param(true)["anyOf"].([]any) {
			out = append(out, v.(map[string]any)["const"])
		}
		return out
	}
	tw(t, f, "offers a value meaning 'no isolation', not just 'worktree'", func(t *testing.T) {
		v := values()
		eq(t, v[0] == "off" || v[1] == "off", true)
		eq(t, v[0] == "worktree" || v[1] == "worktree", true)
	})
	tw(t, f, "lists the inert value first", func(t *testing.T) { eq(t, values()[0], "off") })
	tw(t, f, "warns that a worktree cannot see uncommitted work", func(t *testing.T) {
		b, _ := json.Marshal(param(true))
		if !strings.Contains(string(b), "uncommitted or staged") {
			t.Fatal(string(b))
		}
	})
	tw(t, f, "omits the parameter entirely when the project disabled worktrees", func(t *testing.T) {
		eq(t, param(false) == nil, true)
	})
}
