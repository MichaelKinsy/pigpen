package pi_permission_system

import (
	"slices"
	"testing"
)

// Twins of test/access-intent/mcp-targets.test.ts.

func TestMcpTargetsTwins(t *testing.T) {
	const f = "access-intent/mcp-targets"
	parse := func(s string) (QualifiedMcpToolName, bool) { return ParseQualifiedMcpToolName(s) }
	tw(t, f, "returns server and tool for a valid qualified name", func(t *testing.T) {
		q, ok := parse("exa:search")
		eqv(t, ok, true)
		eqv(t, q, QualifiedMcpToolName{"exa", "search"})
	})
	tw(t, f, "returns server and tool with surrounding whitespace trimmed", func(t *testing.T) {
		q, ok := parse("  exa : search  ")
		eqv(t, ok, true)
		eqv(t, q, QualifiedMcpToolName{"exa", "search"})
	})
	tw(t, f, "returns null for empty string", func(t *testing.T) { _, ok := parse(""); eqv(t, ok, false) })
	tw(t, f, "returns null for whitespace-only string", func(t *testing.T) { _, ok := parse("   "); eqv(t, ok, false) })
	tw(t, f, "returns null when colon is the first character", func(t *testing.T) { _, ok := parse(":search"); eqv(t, ok, false) })
	tw(t, f, "returns null when colon is the last character", func(t *testing.T) { _, ok := parse("exa:"); eqv(t, ok, false) })
	tw(t, f, "returns null for a plain tool name with no colon", func(t *testing.T) { _, ok := parse("exa_search"); eqv(t, ok, false) })
	tw(t, f, "returns null when server part is empty after trimming", func(t *testing.T) { _, ok := parse(" :search"); eqv(t, ok, false) })
	tw(t, f, "returns null when tool part is empty after trimming", func(t *testing.T) { _, ok := parse("exa: "); eqv(t, ok, false) })

	in := func(t *testing.T, js string) any {
		if js == "null" {
			return nil
		}
		return jo(t, js)
	}
	targets := func(t *testing.T, js string, servers ...string) []string {
		return CreateMcpPermissionTargets(in(t, js), servers)
	}
	has := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%q missing from %q", w, got)
			}
		}
	}
	hasNot := func(t *testing.T, got []string, bad ...string) {
		t.Helper()
		for _, w := range bad {
			if slices.Contains(got, w) {
				t.Errorf("%q must not be in %q", w, got)
			}
		}
	}

	t.Run("tool call (input.tool)", func(t *testing.T) {
		tw(t, f, "produces targets for a bare tool name with no configured servers", func(t *testing.T) {
			has(t, targets(t, `{"tool":"exa_search"}`), "exa_search", "mcp_call")
		})
		tw(t, f, "produces targets for a qualified tool name (server:tool)", func(t *testing.T) {
			has(t, targets(t, `{"tool":"exa:search"}`), "exa_search", "exa:search", "exa", "mcp_call")
		})
		tw(t, f, "produces targets for a tool call with explicit server field", func(t *testing.T) {
			has(t, targets(t, `{"tool":"search","server":"exa"}`), "exa_search", "exa:search", "exa", "mcp_call")
		})
		tw(t, f, "derives server targets from configured server names when tool name ends with _<server>", func(t *testing.T) {
			has(t, targets(t, `{"tool":"search_code_exa"}`, "exa"), "exa_search_code_exa", "exa:search_code_exa", "exa", "search_code_exa")
		})
		t.Run("prefix-named tools (<server>_<tool>)", func(t *testing.T) {
			tw(t, f, "derives the bare server for a configured leading segment", func(t *testing.T) {
				has(t, targets(t, `{"tool":"github_search_code"}`, "github", "todoist"), "github")
			})
			tw(t, f, "reports the tool name ahead of the derived server", func(t *testing.T) {
				got := targets(t, `{"tool":"github_search_code"}`, "github")
				eqv(t, got[0], "github_search_code")
				eqv(t, got[1], "github")
			})
			tw(t, f, "derives nothing when the leading segment names no configured server", func(t *testing.T) {
				got := targets(t, `{"tool":"github_search_code"}`, "todoist")
				hasNot(t, got, "github")
				has(t, got, "github_search_code")
			})
			tw(t, f, "picks the longest matching server whatever order the list arrives in", func(t *testing.T) {
				got := targets(t, `{"tool":"foo_bar_baz"}`, "foo", "foo_bar")
				has(t, got, "foo_bar")
				hasNot(t, got, "foo")
			})
			tw(t, f, "omits re-prefixed candidates when an explicit server repeats the prefix", func(t *testing.T) {
				got := targets(t, `{"tool":"github_search_code","server":"github"}`)
				hasNot(t, got, "github_github_search_code", "github:github_search_code")
				eqv(t, got[0], "github_search_code")
				has(t, got, "github")
			})
			tw(t, f, "keeps the qualified candidates when the explicit server is not the prefix", func(t *testing.T) {
				has(t, targets(t, `{"tool":"search_code","server":"github"}`), "github_search_code", "github:search_code", "github")
			})
			tw(t, f, "suppresses a suffix coincidence once a prefix matches", func(t *testing.T) {
				got := targets(t, `{"tool":"foo_bar_baz_github"}`, "foo_bar", "github")
				has(t, got, "foo_bar")
				hasNot(t, got, "github")
			})
		})
		tw(t, f, "does not include duplicate entries", func(t *testing.T) {
			got := targets(t, `{"tool":"exa:search"}`, "exa")
			eqs(t, got, uniqueStrings(got)...)
		})
	})
	t.Run("connect call (input.connect)", func(t *testing.T) {
		tw(t, f, "produces targets for a connect operation", func(t *testing.T) {
			has(t, targets(t, `{"connect":"exa"}`), "mcp_connect_exa", "exa", "mcp_connect")
		})
		tw(t, f, "does not include mcp_call for connect operations", func(t *testing.T) { hasNot(t, targets(t, `{"connect":"exa"}`), "mcp_call") })
	})
	t.Run("describe operation (input.describe)", func(t *testing.T) {
		tw(t, f, "produces targets for a describe operation on a qualified tool", func(t *testing.T) {
			has(t, targets(t, `{"describe":"exa:search"}`), "exa_search", "exa:search", "exa", "mcp_describe")
		})
	})
	t.Run("search operation (input.search)", func(t *testing.T) {
		tw(t, f, "produces mcp_search and the search string as targets", func(t *testing.T) {
			has(t, targets(t, `{"search":"weather"}`), "weather", "mcp_search")
		})
		tw(t, f, "includes server targets when server is provided alongside search", func(t *testing.T) {
			has(t, targets(t, `{"search":"weather","server":"exa"}`), "mcp_server_exa", "exa", "mcp_search")
		})
	})
	t.Run("server listing (input.server only)", func(t *testing.T) {
		tw(t, f, "produces mcp_list and server-specific targets", func(t *testing.T) {
			has(t, targets(t, `{"server":"exa"}`), "mcp_server_exa", "exa", "mcp_list")
		})
	})
	t.Run("status (no meaningful input)", func(t *testing.T) {
		tw(t, f, "produces mcp_status for empty input", func(t *testing.T) { has(t, targets(t, `{}`), "mcp_status") })
		tw(t, f, "produces mcp_status for null input", func(t *testing.T) { has(t, targets(t, `null`), "mcp_status") })
		tw(t, f, "produces mcp_status when no server/tool/connect/describe/search present", func(t *testing.T) {
			has(t, targets(t, `{"unrelated":"value"}`, "exa"), "mcp_status")
		})
	})
	t.Run("the derivation table published in docs/configuration.md", func(t *testing.T) {
		for _, c := range []struct {
			input   string
			servers []string
			want    []string
		}{
			{`{"tool":"github_search_code"}`, []string{"github"}, []string{"github_search_code", "github", "mcp_call"}},
			{`{"tool":"search_code_github"}`, []string{"github"}, []string{"github_search_code_github", "github:search_code_github", "github", "search_code_github", "mcp_call"}},
			{`{"tool":"github:search_code"}`, nil, []string{"github_search_code", "github:search_code", "github", "search_code", "mcp_call"}},
			{`{"tool":"search_code","server":"github"}`, nil, []string{"github_search_code", "github:search_code", "github", "search_code", "mcp_call"}},
		} {
			eqs(t, CreateMcpPermissionTargets(jo(t, c.input), c.servers), c.want...)
		}
	})
	t.Run("priority ordering", func(t *testing.T) {
		tw(t, f, "tool targets appear before mcp_call", func(t *testing.T) {
			got := targets(t, `{"tool":"exa:search"}`)
			idx := slices.Index(got, "exa_search")
			eqv(t, idx >= 0, true)
			eqv(t, slices.Index(got, "mcp_call") > idx, true)
		})
	})
	t.Run("McpTargetList", func(t *testing.T) {
		t.Run("add", func(t *testing.T) {
			tskip(t, f, "ignores null", "Go strings have no null: Add takes a string, and the empty-string twin below covers the same guard")
			tw(t, f, "ignores empty string", func(t *testing.T) {
				l := &McpTargetList{}
				l.Add("")
				eqs(t, l.ToArray())
			})
			tw(t, f, "appends a new value", func(t *testing.T) {
				l := &McpTargetList{}
				l.Add("exa")
				eqs(t, l.ToArray(), "exa")
			})
			tw(t, f, "dedups repeated values", func(t *testing.T) {
				l := &McpTargetList{}
				l.Add("exa")
				l.Add("exa")
				eqs(t, l.ToArray(), "exa")
			})
			tw(t, f, "preserves first-insertion order across a mix of values", func(t *testing.T) {
				l := &McpTargetList{}
				for _, v := range []string{"exa_search", "exa:search", "exa", "exa_search", "mcp_call"} {
					l.Add(v)
				}
				eqs(t, l.ToArray(), "exa_search", "exa:search", "exa", "mcp_call")
			})
		})
		t.Run("toArray", func(t *testing.T) {
			tw(t, f, "returns an independent copy that does not mutate the list", func(t *testing.T) {
				l := &McpTargetList{}
				l.Add("exa")
				first := l.ToArray()
				first = append(first, "mutated")
				first[0] = "changed"
				eqs(t, l.ToArray(), "exa")
			})
		})
	})
	t.Run("createPiMcpToolTargets", func(t *testing.T) {
		pi := CreatePiMcpToolTargets
		tw(t, f, "emits the configured and sanitized server spellings, most specific first", func(t *testing.T) {
			eqs(t, pi("mcp__danger_srv__wipe", []string{"danger-srv"}), "danger-srv_wipe", "danger-srv:wipe", "danger-srv",
				"danger_srv_wipe", "danger_srv:wipe", "danger_srv", "wipe", "mcp__danger_srv__wipe", "mcp_call")
		})
		tw(t, f, "emits one spelling when the configured name needs no sanitizing", func(t *testing.T) {
			eqs(t, pi("mcp__github__search", []string{"github"}), "github_search", "github:search", "github", "search", "mcp__github__search", "mcp_call")
		})
		tw(t, f, "splits an unconfigured server at the first separator", func(t *testing.T) {
			eqs(t, pi("mcp__srv__get__x", nil), "srv_get__x", "srv:get__x", "srv", "get__x", "mcp__srv__get__x", "mcp_call")
		})
		tw(t, f, "resolves the longest configured server whose sanitized name prefixes the tool", func(t *testing.T) {
			want := []string{"a--b_x", "a--b:x", "a--b", "a__b_x", "a__b:x", "a__b", "x", "mcp__a__b__x", "mcp_call"}
			eqs(t, pi("mcp__a__b__x", []string{"a", "a--b"}), want...)
			eqs(t, pi("mcp__a__b__x", []string{"a--b", "a"}), want...)
		})
		tw(t, f, "resolves a configured server whose name holds a separator Pi rewrote", func(t *testing.T) {
			eqs(t, pi("mcp__my_srv__x", []string{"my.srv"}), "my.srv_x", "my.srv:x", "my.srv", "my_srv_x", "my_srv:x", "my_srv", "x", "mcp__my_srv__x", "mcp_call")
		})
		tw(t, f, "resolves a configured server whose sanitized name contains the separator", func(t *testing.T) {
			eqs(t, pi("mcp__a__b__x", []string{"a--b"}), "a--b_x", "a--b:x", "a--b", "a__b_x", "a__b:x", "a__b", "x", "mcp__a__b__x", "mcp_call")
		})
		tw(t, f, "still qualifies a tool whose own name starts with its server", func(t *testing.T) {
			eqs(t, pi("mcp__github__github_x", []string{"github"}), "github_github_x", "github:github_x", "github", "github_x", "mcp__github__github_x", "mcp_call")
		})
		tw(t, f, "keeps every configured name that sanitizes to the same server", func(t *testing.T) {
			eqs(t, pi("mcp__a_b__x", []string{"a.b", "a_b"}), "a.b_x", "a.b:x", "a.b", "a_b_x", "a_b:x", "a_b", "x", "mcp__a_b__x", "mcp_call")
		})
	})
}
