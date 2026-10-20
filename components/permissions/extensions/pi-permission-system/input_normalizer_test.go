package pi_permission_system

import (
	"slices"
	"testing"
)

// Twins of test/access-intent/input-normalizer.test.ts.

func TestInputNormalizerTwins(t *testing.T) {
	const f = "access-intent/input-normalizer"
	setup := func(t *testing.T) { mockHome(t, "/mock/home"); mockRealpath(t, identityRealpath) }
	ni := func(t *testing.T, tool, js string, servers ...string) NormalizedInput {
		return NormalizeInput(tool, jo(t, js), servers)
	}
	extras := func(t *testing.T, got NormalizedInput, js string) {
		t.Helper()
		eqv(t, marshalJSON(got.ResultExtras, ""), marshalJSON(jo(t, js), ""))
	}
	t.Run("normalizeInput — non-MCP surfaces", func(t *testing.T) {
		t.Run("path-bearing and special surfaces collapse to '*'", func(t *testing.T) {
			tw(t, f, "path surface ignores input.path and returns ['*']", func(t *testing.T) {
				r := ni(t, "path", `{"path":".env"}`)
				eqv(t, r.Surface, "path")
				eqs(t, r.Values, "*")
				extras(t, r, `{}`)
			})
			tw(t, f, "external_directory surface ignores input.path and returns ['*']", func(t *testing.T) {
				r := ni(t, "external_directory", `{"path":"/other/project"}`)
				eqv(t, r.Surface, "external_directory")
				eqs(t, r.Values, "*")
				extras(t, r, `{}`)
			})
			tw(t, f, "read surface ignores input.path and returns ['*']", func(t *testing.T) {
				r := ni(t, "read", `{"path":".env"}`)
				eqv(t, r.Surface, "read")
				eqs(t, r.Values, "*")
				extras(t, r, `{}`)
			})
			tw(t, f, "missing path also returns ['*'] (unchanged fallback)", func(t *testing.T) {
				for _, s := range []string{"path", "external_directory", "read", "write", "edit", "grep", "find", "ls"} {
					eqs(t, ni(t, s, `{}`).Values, "*")
				}
			})
		})
		t.Run("skill", func(t *testing.T) {
			tw(t, f, "uses skill name from input.name", func(t *testing.T) {
				r := ni(t, "skill", `{"name":"librarian"}`)
				eqv(t, r.Surface, "skill")
				eqs(t, r.Values, "librarian")
				extras(t, r, `{}`)
			})
			tw(t, f, "falls back to '*' when name is missing", func(t *testing.T) { eqs(t, ni(t, "skill", `{}`).Values, "*") })
			tw(t, f, "falls back to '*' when name is not a string", func(t *testing.T) { eqs(t, ni(t, "skill", `{"name":99}`).Values, "*") })
		})
		t.Run("bash", func(t *testing.T) {
			tw(t, f, "uses command from input.command", func(t *testing.T) {
				r := ni(t, "bash", `{"command":"git status"}`)
				eqv(t, r.Surface, "bash")
				eqs(t, r.Values, "git status")
				extras(t, r, `{"command":"git status"}`)
			})
			tw(t, f, "uses empty string when command is missing", func(t *testing.T) {
				r := ni(t, "bash", `{}`)
				eqs(t, r.Values, "")
				extras(t, r, `{"command":""}`)
			})
			tw(t, f, "uses empty string when command is not a string", func(t *testing.T) {
				r := ni(t, "bash", `{"command":42}`)
				eqs(t, r.Values, "")
				extras(t, r, `{"command":""}`)
			})
			tw(t, f, "strips leading comment lines from values but keeps original in resultExtras", func(t *testing.T) {
				cmd := "# Check debug logs\nfind /home -path '*debug*' -type f"
				r := NormalizeInput("bash", func() *jsObject { o := newObject(); o.set("command", cmd); return o }(), nil)
				eqs(t, r.Values, "find /home -path '*debug*' -type f")
				eqv(t, r.ResultExtras.vals["command"], any(cmd))
			})
			tw(t, f, "strips multiple comment lines", func(t *testing.T) {
				eqs(t, ni(t, "bash", `{"command":"# Step 1\n# Step 2\ngit status --short"}`).Values, "git status --short")
			})
			tw(t, f, "preserves command when no comment lines present", func(t *testing.T) {
				eqs(t, ni(t, "bash", `{"command":"grep -rn foo src/"}`).Values, "grep -rn foo src/")
			})
			tw(t, f, "falls back to original when all lines are comments", func(t *testing.T) {
				eqs(t, ni(t, "bash", `{"command":"# just a comment"}`).Values, "# just a comment")
			})
		})
		t.Run("normalizeBashCommand: a command's spellings", func(t *testing.T) {
			tw(t, f, "matches the typed command and each spelling as aliases, keeping the typed one as the command", func(t *testing.T) {
				r := NormalizeBashCommand("~/bin/x --y", []string{"/h/bin/x --y"})
				eqv(t, r.Surface, "bash")
				eqs(t, r.Values, "~/bin/x --y", "/h/bin/x --y")
				extras(t, r, `{"command":"~/bin/x --y"}`)
			})
			tw(t, f, "drops a spelling identical to the typed command", func(t *testing.T) {
				eqs(t, NormalizeBashCommand("/h/x", []string{"/h/x"}).Values, "/h/x")
			})
			tw(t, f, "strips comment lines from the typed command but not from a spelling", func(t *testing.T) {
				cmd := "# why\n~/bin/x"
				r := NormalizeBashCommand(cmd, []string{"# kept\n/h/bin/x"})
				eqs(t, r.Values, "~/bin/x", "# kept\n/h/bin/x")
				eqv(t, r.ResultExtras.vals["command"], any(cmd))
			})
			tw(t, f, "is what the bash tool input normalizes through, with no spellings", func(t *testing.T) {
				eq(t, ni(t, "bash", `{"command":"ls"}`), NormalizeBashCommand("ls", nil), "normalized")
			})
		})
		t.Run("extension tools (non-path-bearing)", func(t *testing.T) {
			tw(t, f, "uses '*' as the lookup value for extension tools", func(t *testing.T) {
				r := ni(t, "my_extension_tool", `{"some":"input"}`)
				eqv(t, r.Surface, "my_extension_tool")
				eqs(t, r.Values, "*")
				extras(t, r, `{}`)
			})
			tw(t, f, "uses '*' even when extension tool has a path field", func(t *testing.T) {
				eqs(t, ni(t, "my_extension_tool", `{"path":"/some/path"}`).Values, "*")
			})
		})
		t.Run("candidate count per surface", func(t *testing.T) {
			for _, c := range [][3]string{{"skill", "skill", `{"name":"my-skill"}`}, {"bash", "bash", `{"command":"ls -la"}`},
				{"path-bearing tool", "read", `{"path":".env"}`}, {"extension tool", "my_extension_tool", `{"some":"input"}`}} {
				eqv(t, len(ni(t, c[1], c[2], "exa").Values), 1)
			}
			tw(t, f, "an mcp call produces more than one candidate", func(t *testing.T) {
				eqv(t, len(ni(t, "mcp", `{"tool":"exa_search"}`, "exa").Values) > 1, true)
			})
		})
	})
	t.Run("normalizeInput — MCP surface", func(t *testing.T) {
		last := func(v []string) string { return v[len(v)-1] }
		tw(t, f, "surface is 'mcp'", func(t *testing.T) { eqv(t, ni(t, "mcp", `{"tool":"exa:search"}`).Surface, "mcp") })
		tw(t, f, "values end with the catch-all 'mcp' target", func(t *testing.T) { eqv(t, last(ni(t, "mcp", `{"tool":"exa:search"}`).Values), "mcp") })
		tw(t, f, "values include specific targets before the catch-all for a qualified tool call", func(t *testing.T) {
			v := ni(t, "mcp", `{"tool":"exa:search"}`).Values
			for _, w := range []string{"exa_search", "exa:search", "exa", "mcp_call"} {
				eqv(t, slices.Contains(v, w), true)
			}
			eqv(t, last(v), "mcp")
		})
		tw(t, f, "matches createMcpPermissionTargets output + 'mcp' appended", func(t *testing.T) {
			raw := CreateMcpPermissionTargets(jo(t, `{"tool":"exa:search"}`), []string{"exa"})
			eqs(t, ni(t, "mcp", `{"tool":"exa:search"}`, "exa").Values, append(raw, "mcp")...)
		})
		tw(t, f, "resultExtras.target is the first specific target (most-specific)", func(t *testing.T) {
			r := ni(t, "mcp", `{"tool":"exa:search"}`)
			eqv(t, r.ResultExtras.vals["target"], any(r.Values[0]))
		})
		tw(t, f, "resultExtras.target is 'mcp' when no specific targets are derived", func(t *testing.T) {
			eqv(t, ni(t, "mcp", `{}`).ResultExtras.vals["target"], any("mcp_status"))
		})
		tw(t, f, "values contain no duplicates", func(t *testing.T) {
			v := ni(t, "mcp", `{"tool":"exa:search"}`, "exa").Values
			eqs(t, v, uniqueStrings(v)...)
		})
		tw(t, f, "produces mcp_status + mcp for status input", func(t *testing.T) { eqs(t, ni(t, "mcp", `{}`).Values, "mcp_status", "mcp") })
		tw(t, f, "produces connect targets + mcp for connect input", func(t *testing.T) {
			v := ni(t, "mcp", `{"connect":"exa"}`).Values
			eqv(t, slices.Contains(v, "mcp_connect_exa") && slices.Contains(v, "mcp_connect"), true)
			eqv(t, last(v), "mcp")
		})
	})
	t.Run("normalizeInput — a Pi MCP tool (mcp__<server>__<tool>)", func(t *testing.T) {
		tw(t, f, "resolves on the mcp surface under the Pi-name candidates, then 'mcp'", func(t *testing.T) {
			r := ni(t, "mcp__danger_srv__wipe", `{"target":"prod"}`, "danger-srv")
			eqv(t, r.Surface, "mcp")
			eqs(t, r.Values, "danger-srv_wipe", "danger-srv:wipe", "danger-srv", "danger_srv_wipe", "danger_srv:wipe", "danger_srv", "wipe", "mcp__danger_srv__wipe", "mcp_call", "mcp")
			extras(t, r, `{"target":"danger-srv_wipe"}`)
		})
		tw(t, f, "ignores the tool's input, which is the MCP arguments", func(t *testing.T) {
			eqs(t, ni(t, "mcp__srv__x", `{"tool":"other:thing","server":"other"}`).Values, "srv_x", "srv:x", "srv", "x", "mcp__srv__x", "mcp_call", "mcp")
		})
	})

	t.Run("buildAccessIntentForSurface", func(t *testing.T) {
		setup(t)
		normalizer := func() *PathNormalizer { return NewPathNormalizer(PosixPathFlavor, "/test/project") }
		accessPath := func(t *testing.T, q QueryIntent) AccessPathAccessIntent {
			t.Helper()
			ap, ok := q.(AccessPathAccessIntent)
			if !ok {
				t.Fatalf("not an access-path intent: %#v", q)
			}
			return ap
		}
		tw(t, f, "emits an access-path intent carrying the canonical alias for the path surface", func(t *testing.T) {
			setup(t)
			mockRealpath(t, func(p string) (string, error) {
				if p == "/test/project/link" {
					return "/test/project/real", nil
				}
				return p, nil
			})
			ap := accessPath(t, BuildAccessIntentForSurface("path", ptr("link"), normalizer(), nil))
			eqv(t, ap.Surface, "path")
			eqv(t, slices.Contains(ap.Path.MatchValues(), "/test/project/real"), true)
			eqv(t, ap.Path.Value(), "/test/project/link")
		})
		tw(t, f, "emits an access-path intent for the external_directory surface", func(t *testing.T) {
			setup(t)
			ap := accessPath(t, BuildAccessIntentForSurface("external_directory", ptr("/outside/dir"), normalizer(), nil))
			eqv(t, ap.Surface, "external_directory")
			eqv(t, ap.Path.Value(), "/outside/dir")
		})
		tw(t, f, "emits an access-path intent for a path-bearing tool surface (read)", func(t *testing.T) {
			setup(t)
			ap := accessPath(t, BuildAccessIntentForSurface("read", ptr("/test/project/.env"), normalizer(), nil))
			eqv(t, ap.Surface, "read")
			eqv(t, ap.Path.Value(), "/test/project/.env")
		})
		tw(t, f, "resolves a built-in tool surface's value to the file the tool opens", func(t *testing.T) {
			setup(t)
			eqv(t, accessPath(t, BuildAccessIntentForSurface("read", ptr("file:///test/project/.env"), normalizer(), nil)).Path.Value(), "/test/project/.env")
		})
		tw(t, f, "keeps the path surface's value as typed", func(t *testing.T) {
			setup(t)
			eqv(t, accessPath(t, BuildAccessIntentForSurface("path", ptr("file:///test/project/.env"), normalizer(), nil)).Path.Value(), "/test/project/file:/test/project/.env")
		})
		tw(t, f, "emits a tool intent for a non-path surface (bash)", func(t *testing.T) {
			setup(t)
			got := BuildAccessIntentForSurface("bash", ptr("echo hi"), normalizer(), ptr("my-agent"))
			ti, ok := got.(ToolAccessIntent)
			eqv(t, ok, true)
			eqv(t, ti.Surface, "bash")
			eqv(t, marshalJSON(ti.Input, ""), `{"command":"echo hi"}`)
			eqv(t, *ti.AgentName, "my-agent")
		})
		tw(t, f, "passes agentName through on the access-path branch", func(t *testing.T) {
			setup(t)
			eqv(t, *accessPath(t, BuildAccessIntentForSurface("path", ptr("/some/file"), normalizer(), ptr("Explore"))).AgentName, "Explore")
		})
		tw(t, f, "falls back to a tool intent for a value-less path surface", func(t *testing.T) {
			setup(t)
			ti, ok := BuildAccessIntentForSurface("path", nil, normalizer(), nil).(ToolAccessIntent)
			eqv(t, ok, true)
			eqv(t, ti.Surface, "path")
		})
		tw(t, f, "falls back to a tool intent for a whitespace-only path value", func(t *testing.T) {
			setup(t)
			_, ok := BuildAccessIntentForSurface("external_directory", ptr("   "), normalizer(), nil).(ToolAccessIntent)
			eqv(t, ok, true)
		})
	})

	t.Run("buildResolvedIntentFromMatchValues", func(t *testing.T) {
		pv := func(t *testing.T, r ResolvedAccessIntent) PathValuesAccessIntent {
			t.Helper()
			p, ok := r.(PathValuesAccessIntent)
			if !ok {
				t.Fatalf("not path-values: %#v", r)
			}
			return p
		}
		tool := func(t *testing.T, r ResolvedAccessIntent, surface, input, agent string) {
			t.Helper()
			ti, ok := r.(ToolAccessIntent)
			if !ok {
				t.Fatalf("not tool: %#v", r)
			}
			eqv(t, ti.Surface, surface)
			eqv(t, marshalJSON(ti.Input, ""), input)
			eqv(t, *ti.AgentName, agent)
		}
		tw(t, f, "emits a path-values intent carrying the given match values as-is for the path surface", func(t *testing.T) {
			p := pv(t, BuildResolvedIntentFromMatchValues("path", []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts", "/main/src/foo.ts"}, "Explore"))
			eqv(t, p.Surface, "path")
			eqs(t, p.Values, "/worktree/issue-42/src/foo.ts", "src/foo.ts", "/main/src/foo.ts")
			eqv(t, *p.AgentName, "Explore")
		})
		tw(t, f, "emits a path-values intent for the external_directory surface", func(t *testing.T) {
			p := pv(t, BuildResolvedIntentFromMatchValues("external_directory", []string{"/tmp/x", "/real/tmp/x"}, "Explore"))
			eqv(t, p.Surface, "external_directory")
			eqs(t, p.Values, "/tmp/x", "/real/tmp/x")
		})
		tw(t, f, "emits a tool intent from the single portable value for a non-path surface (bash)", func(t *testing.T) {
			tool(t, BuildResolvedIntentFromMatchValues("bash", []string{"git status"}, "Explore"), "bash", `{"command":"git status"}`, "Explore")
		})
		tw(t, f, "emits a tool intent from the single portable value for a skill surface", func(t *testing.T) {
			tool(t, BuildResolvedIntentFromMatchValues("skill", []string{"librarian"}, "Explore"), "skill", `{"name":"librarian"}`, "Explore")
		})
		tw(t, f, "threads an empty agentName through for agent-neutral resolution", func(t *testing.T) {
			tool(t, BuildResolvedIntentFromMatchValues("bash", []string{"ls"}, ""), "bash", `{"command":"ls"}`, "")
		})
	})

	t.Run("a value-bearing mcp query evaluates its value as-is", func(t *testing.T) {
		setup(t)
		tw(t, f, "serves a forwarded mcp request from its child-fixed values", func(t *testing.T) {
			p, ok := BuildResolvedIntentFromMatchValues("mcp", []string{"danger_wipe"}, "Explore").(PathValuesAccessIntent)
			eqv(t, ok, true)
			eqv(t, p.Surface, "mcp")
			eqs(t, p.Values, "danger_wipe")
			eqv(t, *p.AgentName, "Explore")
		})
		tw(t, f, "answers a service mcp query from the value it names", func(t *testing.T) {
			p, ok := BuildAccessIntentForSurface("mcp", ptr("danger"), NewPathNormalizer(PosixPathFlavor, "/test/project"), ptr("Explore")).(PathValuesAccessIntent)
			eqv(t, ok, true)
			eqs(t, p.Values, "danger")
			eqv(t, *p.AgentName, "Explore")
		})
		tw(t, f, "keeps a value-less mcp query on the tool intent", func(t *testing.T) {
			ti, ok := BuildAccessIntentForSurface("mcp", nil, NewPathNormalizer(PosixPathFlavor, "/test/project"), ptr("Explore")).(ToolAccessIntent)
			eqv(t, ok, true)
			eqv(t, ti.Surface, "mcp")
			eqv(t, marshalJSON(ti.Input, ""), `{}`)
		})
	})
}
