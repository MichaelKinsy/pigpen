package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// The MCP surface's target derivation, twinned from the original's test/access-intent/mcp-targets.test.ts:
// every upstream title runs here unchanged (twin_test.go). The derivation table the original publishes in
// docs/configuration.md runs as plain subtests, because `pigeq twins list` does not record `test.each` titles.

func hasTarget(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// mcpTestAgentDir points the agent directory (and so mcp.json) at an empty temp dir, so a gate test decides
// against no configured servers rather than against the machine it runs on.
func mcpTestAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PIG_EXT_HOST_INFO", "")
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	return dir
}

func TestMcpTargetTwins(t *testing.T) {
	tw(t, "access-intent/mcp-targets", "returns server and tool for a valid qualified name", func(t *testing.T) {
		server, tool, ok := parseQualifiedMcpToolName("exa:search")
		eq(t, ok, true, "ok")
		eq(t, server, "exa", "server")
		eq(t, tool, "search", "tool")
	})
	tw(t, "access-intent/mcp-targets", "returns server and tool with surrounding whitespace trimmed", func(t *testing.T) {
		server, tool, ok := parseQualifiedMcpToolName("  exa : search  ")
		eq(t, ok, true, "ok")
		eq(t, server, "exa", "server")
		eq(t, tool, "search", "tool")
	})
	tw(t, "access-intent/mcp-targets", "returns null for empty string", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName("")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null for whitespace-only string", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName("   ")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null when colon is the first character", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName(":search")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null when colon is the last character", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName("exa:")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null for a plain tool name with no colon", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName("exa_search")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null when server part is empty after trimming", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName(" :search")
		eq(t, ok, false, "null")
	})
	tw(t, "access-intent/mcp-targets", "returns null when tool part is empty after trimming", func(t *testing.T) {
		_, _, ok := parseQualifiedMcpToolName("exa: ")
		eq(t, ok, false, "null")
	})

	tw(t, "access-intent/mcp-targets", "produces targets for a bare tool name with no configured servers", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "exa_search"}, nil)
		eq(t, hasTarget(targets, "exa_search"), true, "exa_search")
		eq(t, hasTarget(targets, "mcp_call"), true, "mcp_call")
	})
	tw(t, "access-intent/mcp-targets", "produces targets for a qualified tool name (server:tool)", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "exa:search"}, nil)
		eq(t, hasTarget(targets, "exa_search"), true, "exa_search")
		eq(t, hasTarget(targets, "exa:search"), true, "exa:search")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_call"), true, "mcp_call")
	})
	tw(t, "access-intent/mcp-targets", "produces targets for a tool call with explicit server field", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "search", "server": "exa"}, nil)
		eq(t, hasTarget(targets, "exa_search"), true, "exa_search")
		eq(t, hasTarget(targets, "exa:search"), true, "exa:search")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_call"), true, "mcp_call")
	})
	tw(t, "access-intent/mcp-targets", "derives server targets from configured server names when tool name ends with _<server>", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "search_code_exa"}, []string{"exa"})
		eq(t, hasTarget(targets, "exa_search_code_exa"), true, "exa_search_code_exa")
		eq(t, hasTarget(targets, "exa:search_code_exa"), true, "exa:search_code_exa")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "search_code_exa"), true, "search_code_exa")
	})
	tw(t, "access-intent/mcp-targets", "derives the bare server for a configured leading segment", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "github_search_code"}, []string{"github", "todoist"})
		eq(t, hasTarget(targets, "github"), true, "github")
	})
	tw(t, "access-intent/mcp-targets", "reports the tool name ahead of the derived server", func(t *testing.T) {
		// Order decides which name a winning rule is reported under, so the
		// specific tool name comes first and the prompt says what is running.
		targets := createMcpPermissionTargets(map[string]any{"tool": "github_search_code"}, []string{"github"})
		eq(t, len(targets) > 1, true, "two candidates")
		eq(t, targets[0], "github_search_code", "the tool name leads")
		eq(t, targets[1], "github", "then the bare server")
	})
	tw(t, "access-intent/mcp-targets", "derives nothing when the leading segment names no configured server", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "github_search_code"}, []string{"todoist"})
		eq(t, hasTarget(targets, "github"), false, "no github")
		eq(t, hasTarget(targets, "github_search_code"), true, "github_search_code")
	})
	tw(t, "access-intent/mcp-targets", "picks the longest matching server whatever order the list arrives in", func(t *testing.T) {
		// The production loader sorts longest-first, but the invariant belongs
		// to the derivation rather than to its caller: `foo_bar_baz` belongs to
		// `foo_bar`, never also to `foo`.
		targets := createMcpPermissionTargets(map[string]any{"tool": "foo_bar_baz"}, []string{"foo", "foo_bar"})
		eq(t, hasTarget(targets, "foo_bar"), true, "foo_bar")
		eq(t, hasTarget(targets, "foo"), false, "not foo")
	})
	tw(t, "access-intent/mcp-targets", "omits re-prefixed candidates when an explicit server repeats the prefix", func(t *testing.T) {
		// `github_github_search_code` names nothing a rule can usefully match,
		// and it used to lead the list -- so it also became the reported
		// target whenever no rule matched.
		targets := createMcpPermissionTargets(map[string]any{"tool": "github_search_code", "server": "github"}, nil)
		eq(t, hasTarget(targets, "github_github_search_code"), false, "no re-prefix")
		eq(t, hasTarget(targets, "github:github_search_code"), false, "no re-qualified")
		eq(t, targets[0], "github_search_code", "the tool name leads")
		eq(t, hasTarget(targets, "github"), true, "github")
	})
	tw(t, "access-intent/mcp-targets", "keeps the qualified candidates when the explicit server is not the prefix", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "search_code", "server": "github"}, nil)
		eq(t, hasTarget(targets, "github_search_code"), true, "github_search_code")
		eq(t, hasTarget(targets, "github:search_code"), true, "github:search_code")
		eq(t, hasTarget(targets, "github"), true, "github")
	})
	tw(t, "access-intent/mcp-targets", "suppresses a suffix coincidence once a prefix matches", func(t *testing.T) {
		// One naming convention per name: `foo_bar_baz_github` is a foo_bar
		// tool that happens to end in a configured server's name.
		targets := createMcpPermissionTargets(map[string]any{"tool": "foo_bar_baz_github"}, []string{"foo_bar", "github"})
		eq(t, hasTarget(targets, "foo_bar"), true, "foo_bar")
		eq(t, hasTarget(targets, "github"), false, "not github")
	})
	tw(t, "access-intent/mcp-targets", "does not include duplicate entries", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "exa:search"}, []string{"exa"})
		eq(t, len(targets), len(unique(targets)), "unique")
	})

	tw(t, "access-intent/mcp-targets", "produces targets for a connect operation", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"connect": "exa"}, nil)
		eq(t, hasTarget(targets, "mcp_connect_exa"), true, "mcp_connect_exa")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_connect"), true, "mcp_connect")
	})
	tw(t, "access-intent/mcp-targets", "does not include mcp_call for connect operations", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"connect": "exa"}, nil)
		eq(t, hasTarget(targets, "mcp_call"), false, "no mcp_call")
	})
	tw(t, "access-intent/mcp-targets", "produces targets for a describe operation on a qualified tool", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"describe": "exa:search"}, nil)
		eq(t, hasTarget(targets, "exa_search"), true, "exa_search")
		eq(t, hasTarget(targets, "exa:search"), true, "exa:search")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_describe"), true, "mcp_describe")
	})
	tw(t, "access-intent/mcp-targets", "produces mcp_search and the search string as targets", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"search": "weather"}, nil)
		eq(t, hasTarget(targets, "weather"), true, "weather")
		eq(t, hasTarget(targets, "mcp_search"), true, "mcp_search")
	})
	tw(t, "access-intent/mcp-targets", "includes server targets when server is provided alongside search", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"search": "weather", "server": "exa"}, nil)
		eq(t, hasTarget(targets, "mcp_server_exa"), true, "mcp_server_exa")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_search"), true, "mcp_search")
	})
	tw(t, "access-intent/mcp-targets", "produces mcp_list and server-specific targets", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"server": "exa"}, nil)
		eq(t, hasTarget(targets, "mcp_server_exa"), true, "mcp_server_exa")
		eq(t, hasTarget(targets, "exa"), true, "exa")
		eq(t, hasTarget(targets, "mcp_list"), true, "mcp_list")
	})
	tw(t, "access-intent/mcp-targets", "produces mcp_status for empty input", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{}, nil)
		eq(t, hasTarget(targets, "mcp_status"), true, "mcp_status")
	})
	tw(t, "access-intent/mcp-targets", "produces mcp_status for null input", func(t *testing.T) {
		targets := createMcpPermissionTargets(nil, nil)
		eq(t, hasTarget(targets, "mcp_status"), true, "mcp_status")
	})
	tw(t, "access-intent/mcp-targets", "produces mcp_status when no server/tool/connect/describe/search present", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"unrelated": "value"}, []string{"exa"})
		eq(t, hasTarget(targets, "mcp_status"), true, "mcp_status")
	})
	tw(t, "access-intent/mcp-targets", "tool targets appear before mcp_call", func(t *testing.T) {
		targets := createMcpPermissionTargets(map[string]any{"tool": "exa:search"}, nil)
		at := func(want string) int {
			for i, v := range targets {
				if v == want {
					return i
				}
			}
			return -1
		}
		eq(t, at("exa_search") >= 0, true, "exa_search is present")
		eq(t, at("mcp_call") > at("exa_search"), true, "mcp_call comes later")
	})

	tw(t, "access-intent/mcp-targets", "ignores null", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("")
		eq(t, list.toArray(), []string{}, "empty")
	})
	tw(t, "access-intent/mcp-targets", "ignores empty string", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("")
		eq(t, list.toArray(), []string{}, "empty")
	})
	tw(t, "access-intent/mcp-targets", "appends a new value", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("exa")
		eq(t, list.toArray(), []string{"exa"}, "one value")
	})
	tw(t, "access-intent/mcp-targets", "dedups repeated values", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("exa")
		list.add("exa")
		eq(t, list.toArray(), []string{"exa"}, "one value")
	})
	tw(t, "access-intent/mcp-targets", "preserves first-insertion order across a mix of values", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("exa_search")
		list.add("exa:search")
		list.add("exa")
		list.add("exa_search") // duplicate — must not change order
		list.add("mcp_call")
		eq(t, list.toArray(), []string{"exa_search", "exa:search", "exa", "mcp_call"}, "order")
	})
	tw(t, "access-intent/mcp-targets", "returns an independent copy that does not mutate the list", func(t *testing.T) {
		list := newMcpTargetList()
		list.add("exa")
		first := list.toArray()
		first[0] = "mutated"
		eq(t, list.toArray(), []string{"exa"}, "the list is unchanged")
	})

	tw(t, "access-intent/mcp-targets", "emits the configured and sanitized server spellings, most specific first", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__danger_srv__wipe", []string{"danger-srv"}), []string{
			"danger-srv_wipe", "danger-srv:wipe", "danger-srv",
			"danger_srv_wipe", "danger_srv:wipe", "danger_srv",
			"wipe", "mcp__danger_srv__wipe", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "emits one spelling when the configured name needs no sanitizing", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__github__search", []string{"github"}), []string{
			"github_search", "github:search", "github",
			"search", "mcp__github__search", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "splits an unconfigured server at the first separator", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__srv__get__x", nil), []string{
			"srv_get__x", "srv:get__x", "srv",
			"get__x", "mcp__srv__get__x", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "resolves the longest configured server whose sanitized name prefixes the tool", func(t *testing.T) {
		// Both `a` and `a--b` (sanitized `a__b`) prefix `mcp__a__b__x`; the
		// longer one owns it, whatever the configured order.
		expected := []string{
			"a--b_x", "a--b:x", "a--b",
			"a__b_x", "a__b:x", "a__b",
			"x", "mcp__a__b__x", "mcp_call",
		}
		eq(t, createPiMcpToolTargets("mcp__a__b__x", []string{"a", "a--b"}), expected, "a, a--b")
		eq(t, createPiMcpToolTargets("mcp__a__b__x", []string{"a--b", "a"}), expected, "a--b, a")
	})
	tw(t, "access-intent/mcp-targets", "resolves a configured server whose name holds a separator Pi rewrote", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__my_srv__x", []string{"my.srv"}), []string{
			"my.srv_x", "my.srv:x", "my.srv",
			"my_srv_x", "my_srv:x", "my_srv",
			"x", "mcp__my_srv__x", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "resolves a configured server whose sanitized name contains the separator", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__a__b__x", []string{"a--b"}), []string{
			"a--b_x", "a--b:x", "a--b",
			"a__b_x", "a__b:x", "a__b",
			"x", "mcp__a__b__x", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "still qualifies a tool whose own name starts with its server", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__github__github_x", []string{"github"}), []string{
			"github_github_x", "github:github_x", "github",
			"github_x", "mcp__github__github_x", "mcp_call",
		}, "targets")
	})
	tw(t, "access-intent/mcp-targets", "keeps every configured name that sanitizes to the same server", func(t *testing.T) {
		eq(t, createPiMcpToolTargets("mcp__a_b__x", []string{"a.b", "a_b"}), []string{
			"a.b_x", "a.b:x", "a.b",
			"a_b_x", "a_b:x", "a_b",
			"x", "mcp__a_b__x", "mcp_call",
		}, "targets")
	})
}

// The derivation table the original prints in docs/configuration.md § `mcp` Surface (a `test.each` the twins
// ledger does not record): asserting the full array keeps the doc honest.
func TestMcpDerivationTable(t *testing.T) {
	for _, c := range []struct {
		label    string
		input    map[string]any
		servers  []string
		expected []string
	}{
		{"a prefix-named tool", map[string]any{"tool": "github_search_code"}, []string{"github"},
			[]string{"github_search_code", "github", "mcp_call"}},
		{"a suffix-named tool", map[string]any{"tool": "search_code_github"}, []string{"github"},
			[]string{"github_search_code_github", "github:search_code_github", "github", "search_code_github", "mcp_call"}},
		{"a qualified tool name", map[string]any{"tool": "github:search_code"}, nil,
			[]string{"github_search_code", "github:search_code", "github", "search_code", "mcp_call"}},
		{"an explicit server argument", map[string]any{"tool": "search_code", "server": "github"}, nil,
			[]string{"github_search_code", "github:search_code", "github", "search_code", "mcp_call"}},
	} {
		t.Run(c.label, func(t *testing.T) {
			eq(t, createMcpPermissionTargets(c.input, c.servers), c.expected, c.label)
		})
	}
}

func TestDecideMcp(t *testing.T) {
	mcpTestAgentDir(t)
	cwd := t.TempDir()

	// With no rule of its own the universal default asks, on the mcp surface, showing the first candidate and
	// offering the exact tool name as the session option (suggestExactSessionPattern).
	p := policyOf(t, `{"permission":{"*":"ask"}}`)
	v := p.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd)
	eq(t, v.Ask != nil, true, "asks")
	eq(t, v.Ask.Surface, "mcp", "surface")
	eq(t, v.Ask.Value, "mcp_dap_start_session", "the first candidate is the decision value")
	eq(t, v.Ask.Tool, "mcp__mcp_dap__start_session", "the invoked tool")
	eq(t, v.Ask.Pattern, "", "the universal fallback matchedPattern is undefined")
	eq(t, v.Ask.Session.Surface, "mcp", "session surface")
	eq(t, v.Ask.Session.Pattern, "mcp__mcp_dap__start_session", "the session option is the exact tool name")
	eq(t, v.Ask.Session.Label, `Yes, allow mcp tool "mcp__mcp_dap__start_session" for this session`, "label")

	// The dialog's session grant allows the next call without asking (session/session-rules.ts).
	grant := v.Ask.sessionGrants()
	eq(t, len(grant), 1, "one grant, on the mcp surface")
	eq(t, grant[0], Rule{Surface: "mcp", Pattern: "mcp__mcp_dap__start_session", Action: "allow", Layer: "session", Origin: "session"}, "the grant")
	eq(t, p.DecideIn("mcp__mcp_dap__start_session", nil, grant, cwd), Verdict{}, "the session grant allows")

	// A top-level `mcp__…` key is relocated onto the mcp surface (RelocateMcpToolKeyRules) and names the call.
	r := policyOf(t, `{"permission":{"mcp__mcp_dap__*":"allow"}}`)
	eq(t, r.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd), Verdict{}, "a relocated allow allows")

	// A rule written on the mcp surface matches the derived candidates.
	s := policyOf(t, `{"permission":{"mcp":{"mcp_dap*":"allow"}}}`)
	eq(t, s.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd), Verdict{}, "an mcp-surface allow allows")
	eq(t, s.DecideIn("mcp__other__tool", nil, nil, cwd).Ask != nil, true, "another server still asks")

	// A deny denies, with the original's denial text on the mcp surface.
	d := policyOf(t, `{"permission":{"mcp":{"mcp_dap*":{"action":"deny","reason":"no dap"}}}}`)
	dv := d.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd)
	eq(t, dv.Block, true, "deny blocks")
	eq(t, dv.Reason, "[pi-permission-system] Denied by policy: 'mcp' (rule 'mcp_dap*'). Reason: no dap.", "reason")

	// yoloMode grants the ask, as it grants every ask.
	y := policyOf(t, `{"yoloMode":true,"permission":{"*":"ask"}}`)
	eq(t, y.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd), Verdict{}, "yolo grants the ask")

	// The mcp proxy: its input names the call, its candidates decide on the same surface, and its session
	// option is the pattern the target suggests (suggestMcpPattern).
	x := policyOf(t, `{"permission":{"mcp":{"mcp_status":"allow"}}}`)
	eq(t, x.DecideIn("mcp", nil, nil, cwd), Verdict{}, "the status probe allows")
	toolCall := x.DecideIn("mcp", map[string]any{"tool": "exa:search"}, nil, cwd)
	eq(t, toolCall.Ask != nil, true, "a tool call asks")
	eq(t, toolCall.Ask.Surface, "mcp", "surface")
	eq(t, toolCall.Ask.Value, "exa_search", "the first candidate")
	eq(t, toolCall.Ask.Session.Pattern, "exa_*", "the proxy suggests the server pattern")
	eq(t, toolCall.Ask.Tool, "mcp", "the proxy is its own surface")

	// An MCP call's input is the MCP arguments, never a path: no path gate applies to it.
	paths := policyOf(t, `{"permission":{"path":{"*":"deny"},"mcp":"allow","external_directory":"deny"}}`)
	eq(t, paths.DecideIn("mcp__srv__tool", map[string]any{"path": "/etc/passwd"}, nil, cwd), Verdict{}, "no path gate on an MCP call")
}

func TestConfiguredMcpServerNames(t *testing.T) {
	agent := mcpTestAgentDir(t)
	cwd := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(agent, "mcp.json"), `{"mcpServers":{"mcp-dap":{"command":"mcp-dap"},"zeta":{"command":"z"}}}`)

	p := policyOf(t, `{"permission":{"*":"ask"}}`)
	eq(t, p.mcpServerNames(""), []string{"mcp-dap", "zeta"}, "the agent's servers, longest name first")

	// The configured spelling is a candidate, so a rule written for the name in mcp.json fires.
	r := policyOf(t, `{"permission":{"mcp":{"mcp-dap*":"allow"}}}`)
	eq(t, r.DecideIn("mcp__mcp_dap__start_session", nil, nil, cwd), Verdict{}, "the configured spelling matches")

	// The project's mcp.json names its servers too (`.pig/mcp.json`, and `.pi/mcp.json` for compatibility);
	// `mcpServers` wins over the `mcp-servers` spelling of one file, as `root.mcpServers ?? root["mcp-servers"]` does.
	write(filepath.Join(cwd, ".pig", "mcp.json"), `{"mcpServers":{"proj":{"command":"p"}}}`)
	write(filepath.Join(cwd, ".pi", "mcp.json"), `{"mcpServers":{"piProj":{"command":"p"}},"mcp-servers":{"legacy":{"command":"l"}}}`)
	eq(t, p.mcpServerNames(cwd), []string{"mcp-dap", "piProj", "proj", "zeta"}, "the project's servers join, longest first")

	// A file that cannot be read or parsed names nothing.
	write(filepath.Join(agent, "mcp.json"), `{"mcpServers":`)
	eq(t, len(p.mcpServerNames("")), 0, "an unparsable file names nothing")
}
