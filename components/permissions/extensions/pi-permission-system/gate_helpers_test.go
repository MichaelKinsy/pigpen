package pi_permission_system

import "testing"

// Twins of test/handlers/gates/helpers.test.ts.

func TestGateHelpersTwins(t *testing.T) {
	const f = "handlers/gates/helpers"
	cmd := func(s string) PermissionCheckResult { return PermissionCheckResult{Command: &s} }
	target := func(s string) PermissionCheckResult { return PermissionCheckResult{Target: &s} }

	tw(t, f, "returns command for bash", func(t *testing.T) {
		eq(t, DeriveDecisionValue("bash", cmd("git status"), ""), "git status", "value")
	})
	tw(t, f, "falls back to toolName when bash has no command", func(t *testing.T) {
		eq(t, DeriveDecisionValue("bash", PermissionCheckResult{}, ""), "bash", "value")
	})
	tw(t, f, "returns target for mcp", func(t *testing.T) {
		eq(t, DeriveDecisionValue("mcp", target("exa:search"), ""), "exa:search", "value")
	})
	tw(t, f, "falls back to toolName when mcp has no target", func(t *testing.T) {
		eq(t, DeriveDecisionValue("mcp", PermissionCheckResult{}, ""), "mcp", "value")
	})
	tw(t, f, "returns toolName for non-path-bearing tools", func(t *testing.T) {
		eq(t, DeriveDecisionValue("my_extension_tool", PermissionCheckResult{}, ""), "my_extension_tool", "value")
	})
	tw(t, f, "returns path for path-bearing tools when path is provided", func(t *testing.T) {
		eq(t, DeriveDecisionValue("read", PermissionCheckResult{}, "/project/src/main.ts"), "/project/src/main.ts", "read")
		eq(t, DeriveDecisionValue("write", PermissionCheckResult{}, "src/.env"), "src/.env", "write")
	})
	tw(t, f, "falls back to toolName for path-bearing tools when path is missing", func(t *testing.T) {
		eq(t, DeriveDecisionValue("read", PermissionCheckResult{}, ""), "read", "read")
		eq(t, DeriveDecisionValue("write", PermissionCheckResult{}, ""), "write", "write")
	})

	makeCheck := func(mut ...func(*PermissionCheckResult)) PermissionCheckResult {
		return makeCheckResult(append([]func(*PermissionCheckResult){ckPattern("*")}, mut...)...)
	}
	tw(t, f, "builds a decision event with all fields populated", func(t *testing.T) {
		c := makeCheck(func(c *PermissionCheckResult) { c.Origin = "global"; c.MatchedPattern = sp("read") })
		got := BuildDecisionEvent(GateDecision{Surface: "read", Value: "read"}, c, sp("test-agent"), "allow", "policy_allow")
		eq(t, got, DecisionEventFacts{Surface: "read", Value: "read", Result: "allow", Resolution: "policy_allow", Origin: sp("global"),
			AgentName: sp("test-agent"), MatchedPattern: sp("read")}, "event")
	})
	tw(t, f, "normalises undefined origin to null", func(t *testing.T) {
		got := BuildDecisionEvent(GateDecision{Surface: "bash", Value: "git status"}, makeCheck(func(c *PermissionCheckResult) { c.Origin = "" }), nil, "allow", "user_approved")
		eq(t, got.Origin, (*string)(nil), "origin")
	})
	tw(t, f, "normalises null agentName to null", func(t *testing.T) {
		got := BuildDecisionEvent(GateDecision{Surface: "read", Value: "read"}, makeCheck(), nil, "deny", "policy_deny")
		eq(t, got.AgentName, (*string)(nil), "agent")
	})
	tw(t, f, "normalises undefined matchedPattern to null", func(t *testing.T) {
		got := BuildDecisionEvent(GateDecision{Surface: "read", Value: "read"}, makeCheck(func(c *PermissionCheckResult) { c.MatchedPattern = nil }), nil, "deny", "policy_deny")
		eq(t, got.MatchedPattern, (*string)(nil), "pattern")
	})
	tw(t, f, "passes result and resolution through", func(t *testing.T) {
		got := BuildDecisionEvent(GateDecision{Surface: "bash", Value: "rm -rf /"}, makeCheck(), nil, "deny", "user_denied")
		eq(t, got.Result, "deny", "result")
		eq(t, got.Resolution, PermissionDecisionResolution("user_denied"), "resolution")
	})

	tw(t, f, "returns the check unchanged for a ruleset-granted yolo allow", func(t *testing.T) {
		c := makeCheckResult(func(c *PermissionCheckResult) { c.Origin = "yolo"; c.MatchedPattern = sp("*") })
		eq(t, *ResolveYoloGrant(c, false), c, "grant")
	})
	tw(t, f, "grants a residual ask under yolo, preserving the matched pattern", func(t *testing.T) {
		c := makeCheckResult(ckState("ask"), func(c *PermissionCheckResult) {
			c.Source, c.ToolName, c.MatchedPattern = "bash", "bash", sp("<indirection-bash-wrapper>")
		})
		want := c
		want.State, want.Origin = "allow", "yolo"
		eq(t, *ResolveYoloGrant(c, true), want, "grant")
	})
	tw(t, f, "returns null for a residual ask with yolo disabled", func(t *testing.T) {
		if got := ResolveYoloGrant(makeCheckResult(ckState("ask", "*")), false); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	tw(t, f, "returns null for an allow granted by an ordinary rule", func(t *testing.T) {
		c := makeCheckResult(func(c *PermissionCheckResult) { c.Origin = "global"; c.MatchedPattern = sp("*") })
		if got := ResolveYoloGrant(c, true); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	tw(t, f, "returns null for a deny, so an explicit deny survives yolo", func(t *testing.T) {
		if got := ResolveYoloGrant(makeCheckResult(ckState("deny", "rm *")), true); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	tw(t, f, "projects the AccessPath's match set and boundary as strings", func(t *testing.T) {
		path := AccessPathForPath("/outside/x.ts", AccessPathForPathOptions{Cwd: "/repo", Flavor: PosixPathFlavor})
		got := AccessFactsFromPath("external_directory", path)
		boundary := path.BoundaryValue()
		eq(t, got, ForwardedAccessFacts{Surface: "external_directory", MatchValues: path.MatchValues(), BoundaryValue: &boundary}, "facts")
	})
	tw(t, f, "collapses an empty boundary (literal-only path) to null", func(t *testing.T) {
		path := AccessPathForLiteral("relative-token")
		eq(t, path.BoundaryValue(), "", "boundary")
		eq(t, AccessFactsFromPath("path", path), ForwardedAccessFacts{Surface: "path", MatchValues: []string{"relative-token"}}, "facts")
	})
	tw(t, f, "wraps a single portable value with a null boundary", func(t *testing.T) {
		eq(t, AccessFactsFromValue("skill", "deep-research"), ForwardedAccessFacts{Surface: "skill", MatchValues: []string{"deep-research"}}, "facts")
	})
}
