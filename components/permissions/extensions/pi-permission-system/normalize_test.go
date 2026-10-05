package pi_permission_system

import "testing"

func TestNormalizeFlatConfig(t *testing.T) {
	const f = "policy/normalize"
	br := func(surface, pattern, action string) Rule { return rule(surface, pattern, action, "", "builtin") }
	nf := func(t *testing.T, s string) Ruleset { return NormalizeFlatConfig(jo(t, s)) }
	none := func(t *testing.T, got Ruleset) {
		t.Helper()
		if len(got) != 0 {
			t.Errorf("want no rules, got %+v", got)
		}
	}
	tw(t, f, "string value produces a single catch-all rule for the surface", func(t *testing.T) {
		eq(t, nf(t, `{"read":"allow"}`), Ruleset{br("read", "*", "allow")}, "rules")
	})
	tw(t, f, "string shorthand works for multiple surfaces", func(t *testing.T) {
		eq(t, nf(t, `{"read":"allow","write":"deny"}`), Ruleset{br("read", "*", "allow"), br("write", "*", "deny")}, "rules")
	})
	tw(t, f, "universal fallback '*' becomes a catch-all rule with surface '*'", func(t *testing.T) {
		eq(t, nf(t, `{"*":"ask"}`), Ruleset{br("*", "*", "ask")}, "rules")
	})
	tw(t, f, "external_directory string shorthand maps directly to its surface", func(t *testing.T) {
		eq(t, nf(t, `{"external_directory":"ask"}`), Ruleset{br("external_directory", "*", "ask")}, "rules")
	})
	tw(t, f, "invalid string values (non-PermissionState) are ignored", func(t *testing.T) {
		eq(t, nf(t, `{"read":"allow","write":"invalid"}`), Ruleset{br("read", "*", "allow")}, "rules")
	})
	tw(t, f, "object value produces one rule per pattern", func(t *testing.T) {
		eq(t, nf(t, `{"bash":{"*":"ask","git *":"allow"}}`), Ruleset{br("bash", "*", "ask"), br("bash", "git *", "allow")}, "rules")
	})
	tw(t, f, "mcp object map produces rules with surface 'mcp'", func(t *testing.T) {
		eq(t, nf(t, `{"mcp":{"*":"ask","mcp_status":"allow"}}`), Ruleset{br("mcp", "*", "ask"), br("mcp", "mcp_status", "allow")}, "rules")
	})
	tw(t, f, "skill object map produces rules with surface 'skill'", func(t *testing.T) {
		eq(t, nf(t, `{"skill":{"*":"ask","librarian":"allow"}}`), Ruleset{br("skill", "*", "ask"), br("skill", "librarian", "allow")}, "rules")
	})
	tw(t, f, "invalid action values in object map are ignored", func(t *testing.T) {
		eq(t, nf(t, `{"bash":{"git *":"allow","rm -rf *":"bad"}}`), Ruleset{br("bash", "git *", "allow")}, "rules")
	})
	tw(t, f, "full mixed config produces rules in insertion order", func(t *testing.T) {
		got := nf(t, `{"*":"ask","read":"allow","write":"deny","bash":{"*":"ask","git *":"allow"},"mcp":{"mcp_status":"allow"},"skill":{"*":"ask"},"external_directory":"ask"}`)
		eq(t, got, Ruleset{br("*", "*", "ask"), br("read", "*", "allow"), br("write", "*", "deny"), br("bash", "*", "ask"), br("bash", "git *", "allow"),
			br("mcp", "mcp_status", "allow"), br("skill", "*", "ask"), br("external_directory", "*", "ask")}, "rules")
	})
	tw(t, f, "empty permission object produces empty ruleset", func(t *testing.T) { none(t, nf(t, `{}`)) })
	tw(t, f, "non-object values (null, array) nested in map are skipped", func(t *testing.T) {
		eq(t, nf(t, `{"bash":null,"read":"allow"}`), Ruleset{br("read", "*", "allow")}, "rules")
	})
	tw(t, f, "{ action: 'deny', reason } produces a deny rule carrying the reason", func(t *testing.T) {
		r := br("bash", "npm *", "deny")
		r.Reason = sp("Use pnpm instead")
		eq(t, nf(t, `{"bash":{"npm *":{"action":"deny","reason":"Use pnpm instead"}}}`), Ruleset{r}, "rules")
	})
	tw(t, f, "{ action: 'deny' } without a reason produces a deny rule without reason", func(t *testing.T) {
		eq(t, nf(t, `{"bash":{"rm -rf *":{"action":"deny"}}}`), Ruleset{br("bash", "rm -rf *", "deny")}, "rules")
	})
	tw(t, f, "deny-with-reason and plain strings coexist in the same surface", func(t *testing.T) {
		r := br("bash", "npm *", "deny")
		r.Reason = sp("Use pnpm")
		eq(t, nf(t, `{"bash":{"git *":"allow","npm *":{"action":"deny","reason":"Use pnpm"},"*":"ask"}}`),
			Ruleset{br("bash", "git *", "allow"), r, br("bash", "*", "ask")}, "rules")
	})
	tw(t, f, "top-level deny-with-reason object is treated as a pattern map", func(t *testing.T) {
		eq(t, nf(t, `{"bash":{"action":"deny","reason":"Not allowed"}}`), Ruleset{br("bash", "action", "deny")}, "rules")
	})
	tw(t, f, "non-string reason is rejected (malformed config)", func(t *testing.T) {
		none(t, nf(t, `{"bash":{"npm *":{"action":"deny","reason":42}}}`))
	})
}

func TestExpandDirectionalSugar(t *testing.T) {
	const f = "policy/normalize"
	ex := func(t *testing.T, s string) string { return marshalJSON(ExpandDirectionalSugar(jo(t, s)), "") }
	tw(t, f, "expands a bare path key into both directional keys", func(t *testing.T) {
		eq(t, ex(t, `{"path":{"*":"ask","~/.ssh/*":"deny"}}`),
			`{"path_read":{"*":"ask","~/.ssh/*":"deny"},"path_write":{"*":"ask","~/.ssh/*":"deny"}}`, "expanded")
	})
	tw(t, f, "expands a bare external_directory key the same way", func(t *testing.T) {
		eq(t, ex(t, `{"external_directory":"ask"}`), `{"external_directory_read":"ask","external_directory_write":"ask"}`, "expanded")
	})
	tw(t, f, "leaves the bare surfaces empty — no rule survives on them", func(t *testing.T) {
		eq(t, ExpandDirectionalSugar(jo(t, `{"path":"deny"}`)).has("path"), false, "path")
		_, present := ExpandDirectionalSugar(jo(t, `{"path":"deny"}`)).get("path")
		eq(t, present, false, "present")
	})
	tw(t, f, "passes a non-family surface through untouched", func(t *testing.T) {
		eq(t, ex(t, `{"read":"allow","bash":{"git *":"ask"}}`), `{"read":"allow","bash":{"git *":"ask"}}`, "expanded")
	})
	tw(t, f, "drops a family key set to an explicit undefined, expanding nothing", func(t *testing.T) {
		in := newObject()
		in.set("path", undef{})
		in.set("bash", "allow")
		eq(t, marshalJSON(ExpandDirectionalSugar(in), ""), `{"bash":"allow"}`, "expanded")
	})
	tw(t, f, "passes an explicit directional key through when no sugar key is present", func(t *testing.T) {
		eq(t, ex(t, `{"path_read":{"~/dev/*":"allow"}}`), `{"path_read":{"~/dev/*":"allow"}}`, "expanded")
	})
	tw(t, f, "appends explicit directional entries after the sugar-derived ones", func(t *testing.T) {
		eq(t, ex(t, `{"path":{"*":"ask","~/.ssh/*":"deny"},"path_read":{"~/dev/*":"allow"}}`),
			`{"path_read":{"*":"ask","~/.ssh/*":"deny","~/dev/*":"allow"},"path_write":{"*":"ask","~/.ssh/*":"deny"}}`, "expanded")
	})
	tw(t, f, "means the same thing when the two keys are written in the other order", func(t *testing.T) {
		a := jo(t, `{"external_directory":{"*":"ask"},"external_directory_read":{"~/dev/*":"allow"}}`)
		b := jo(t, `{"external_directory_read":{"~/dev/*":"allow"},"external_directory":{"*":"ask"}}`)
		sugarFirst, directionalFirst := ExpandDirectionalSugar(a), ExpandDirectionalSugar(b)
		eq(t, sugarFirst.order(), directionalFirst.order(), "key order")
		eq(t, marshalJSON(sugarFirst, ""), marshalJSON(directionalFirst, ""), "same")
		eq(t, marshalJSON(sugarFirst, ""), `{"external_directory_read":{"*":"ask","~/dev/*":"allow"},"external_directory_write":{"*":"ask"}}`, "expanded")
	})
	tw(t, f, "moves a pattern the explicit entry redefines to the end", func(t *testing.T) {
		got := ExpandDirectionalSugar(jo(t, `{"path":{"*":"ask","~/.ssh/*":"deny"},"path_read":{"*":"allow"}}`))
		eq(t, marshalJSON(got, ""), `{"path_read":{"~/.ssh/*":"deny","*":"allow"},"path_write":{"*":"ask","~/.ssh/*":"deny"}}`, "expanded")
		eq(t, got.obj("path_read").order(), []string{"~/.ssh/*", "*"}, "order")
	})
	tw(t, f, "normalizes a string sugar value to a catch-all when merging", func(t *testing.T) {
		eq(t, ex(t, `{"path":"ask","path_write":{"~/scratch/*":"allow"}}`),
			`{"path_read":"ask","path_write":{"*":"ask","~/scratch/*":"allow"}}`, "expanded")
	})
	tw(t, f, "does not share a pattern map between the two directional keys", func(t *testing.T) {
		got := ExpandDirectionalSugar(jo(t, `{"path":{"*":"ask"}}`))
		got.obj("path_read").set("injected", "allow")
		eq(t, marshalJSON(got.obj("path_write"), ""), `{"*":"ask"}`, "write")
	})
}

func TestRelocateMcpToolKeyRules(t *testing.T) {
	const f = "policy/normalize"
	r := func(surface, pattern, action string) Rule { return rule(surface, pattern, action, "config", "global") }
	tw(t, f, "copies a Pi MCP tool key onto the mcp surface, after every other rule", func(t *testing.T) {
		legacy := r("mcp__danger_srv__wipe", "*", "deny")
		legacy.Origin, legacy.Reason = "project", sp("no wiping")
		moved := r("mcp", "mcp__danger_srv__wipe", "deny")
		moved.Origin, moved.Reason = "project", sp("no wiping")
		got := RelocateMcpToolKeyRules(Ruleset{legacy, r("mcp", "*", "allow"), r("bash", "*", "ask")})
		eq(t, got, McpRelocation{Rules: Ruleset{legacy, r("mcp", "*", "allow"), r("bash", "*", "ask"), moved}, RelocatedKeys: []string{"mcp__danger_srv__wipe"}}, "result")
	})
	tw(t, f, "copies wildcard keys too, keeping their relative order", func(t *testing.T) {
		got := RelocateMcpToolKeyRules(Ruleset{r("mcp__a__*", "*", "deny"), r("read", "*", "allow"), r("mcp__*", "*", "allow")})
		eq(t, got.Rules, Ruleset{r("mcp__a__*", "*", "deny"), r("read", "*", "allow"), r("mcp__*", "*", "allow"), r("mcp", "mcp__a__*", "deny"), r("mcp", "mcp__*", "allow")}, "rules")
		eq(t, got.RelocatedKeys, []string{"mcp__a__*", "mcp__*"}, "keys")
	})
	tw(t, f, "leaves a key that can name no Pi MCP tool on its own surface only", func(t *testing.T) {
		in := Ruleset{r("mcp__foo", "*", "deny"), r("mcp__a__", "*", "ask")}
		eq(t, RelocateMcpToolKeyRules(in), McpRelocation{Rules: in, RelocatedKeys: nil}, "result")
	})
	tw(t, f, "leaves a non-catch-all pattern on an mcp__ key in place, inert as before", func(t *testing.T) {
		in := Ruleset{r("mcp__a__x", "foo", "deny")}
		eq(t, RelocateMcpToolKeyRules(in), McpRelocation{Rules: in, RelocatedKeys: nil}, "result")
	})
	tw(t, f, "leaves the mcp surface and other surfaces untouched", func(t *testing.T) {
		in := Ruleset{r("mcp", "mcp__a__x", "deny"), r("mcp_server", "*", "deny"), r("bash", "*", "ask")}
		eq(t, RelocateMcpToolKeyRules(in), McpRelocation{Rules: in, RelocatedKeys: nil}, "result")
	})
}
