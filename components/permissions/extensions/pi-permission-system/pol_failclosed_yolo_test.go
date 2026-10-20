package pi_permission_system

import "testing"

// Twins of test/policy/permission-manager-fail-closed.test.ts and permission-manager-yolo.test.ts.

func TestPermissionManagerFailClosedTwins(t *testing.T) {
	const f = "policy/permission-manager-fail-closed"
	// Fail-closed clamp (#646): when a non-global config scope is invalid, the composed policy is floored so nothing resolves more
	// permissively than `ask` (`allow` becomes `ask`, tagged origin "fail-closed"). An invalid global scope never triggers the clamp;
	// a hard deny survives.
	bash := func(t *testing.T, m *PermissionManager, agent string) PermissionCheckResult {
		return checkToolAs(t, m, "bash", `{"command":"echo hi"}`, agent, nil)
	}
	invalid := &ScopeConfig{Invalid: true}

	tw(t, f, "floors a lower-scope allow to ask when a project scope is invalid", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: invalid})
		r := bash(t, m, "")
		eq(t, []string{r.State, r.Origin}, []string{"ask", "fail-closed"}, "result")
	})
	tw(t, f, "preserves a lower-scope deny when a project scope is invalid", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"deny"}`), project: invalid})
		r := bash(t, m, "")
		eq(t, r.State, "deny", "state")
		eq(t, r.Origin != "fail-closed", true, "origin")
	})
	tw(t, f, "floors when an agent scope is invalid", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), agent: map[string]*ScopeConfig{"coder": invalid}})
		r := bash(t, m, "coder")
		eq(t, []string{r.State, r.Origin}, []string{"ask", "fail-closed"}, "result")
	})
	tw(t, f, "does not floor when the invalid flag is on the global scope", func(t *testing.T) {
		// Global is the lowest precedence: nothing more permissive is inherited, so its invalid flag is ignored by the clamp.
		m := newInMemoryManager(polScopes{global: &ScopeConfig{Permission: jo(t, `{"bash":"allow"}`), Invalid: true}})
		r := bash(t, m, "")
		eq(t, r.State, "allow", "state")
		eq(t, r.Origin != "fail-closed", true, "origin")
	})
	tw(t, f, "does not floor when all scopes are valid", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: permScope(t, `{"read":"allow"}`)})
		r := bash(t, m, "")
		eq(t, r.State, "allow", "state")
		eq(t, r.Origin != "fail-closed", true, "origin")
	})
	tw(t, f, "floors getToolPermission too (display parity)", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: invalid})
		eq(t, m.GetToolPermission("bash", nil), "ask", "state")
	})
	tw(t, f, "keeps a floored tool exposed — the clamp makes allows ask, never deny", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: invalid})
		eq(t, m.IsToolFullyDenied("bash", nil), false, "denied")
	})
	tw(t, f, "appends a fail-closed notice to config issues naming the invalid scope", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: invalid})
		eq(t, m.GetConfigIssues(nil), []string{"Invalid project configuration detected \u2014 failing closed: 'allow' rules " +
			"are clamped to 'ask' for this session until the configuration is corrected."}, "issues")
	})
	tw(t, f, "emits no fail-closed notice when all scopes are valid", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: permScope(t, `{"read":"allow"}`)})
		eq(t, m.GetConfigIssues(nil), []string{}, "issues")
	})
}

func TestPermissionManagerYoloTwins(t *testing.T) {
	const f = "policy/permission-manager-yolo"
	// yolo mode as recorded authority (#526): when the injected reader reports true, check() rewrites every matched `ask` to `allow`
	// tagged origin "yolo". Display surfaces (getComposedConfigRules, getToolPermission) stay yolo-free.
	makeManager := func(t *testing.T, global string, yolo func() bool, project *ScopeConfig) *PermissionManager {
		return NewPermissionManager(PermissionManagerOptions{
			PolicyLoader:  &memLoader{scopes: polScopes{global: permScope(t, global), project: project}, issues: []string{}},
			IsYoloEnabled: yolo,
		})
	}
	on, off := func() bool { return true }, func() bool { return false }

	tw(t, f, "rewrites a would-be-ask tool check to allow with origin 'yolo'", func(t *testing.T) {
		r := checkTool(t, makeManager(t, `{"bash":"ask"}`, on, nil), "bash", `{"command":"rm -rf /tmp/x"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "yolo"}, "result")
	})
	tw(t, f, "rewrites the synthesized universal-default ask to allow with origin 'yolo'", func(t *testing.T) {
		// No rule for the surface; falls through to the universal "*" default (ask).
		r := checkTool(t, makeManager(t, `{}`, on, nil), "someExtensionTool", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "yolo"}, "result")
	})
	tw(t, f, "preserves explicit deny under yolo (hard denies survive)", func(t *testing.T) {
		r := checkTool(t, makeManager(t, `{"bash":"deny"}`, on, nil), "bash", `{"command":"rm -rf /"}`)
		eq(t, r.State, "deny", "state")
		eq(t, r.Origin != "yolo", true, "origin")
	})
	tw(t, f, "passes an explicit allow through unchanged (not tagged yolo)", func(t *testing.T) {
		r := checkTool(t, makeManager(t, `{"read":"allow"}`, on, nil), "read", `{"path":"/tmp/x"}`)
		eq(t, r.State, "allow", "state")
		eq(t, r.Origin != "yolo", true, "origin")
	})
	tw(t, f, "does not rewrite when yolo is disabled (state stays ask)", func(t *testing.T) {
		r := checkTool(t, makeManager(t, `{"bash":"ask"}`, off, nil), "bash", `{"command":"rm -rf /tmp/x"}`)
		eq(t, r.State, "ask", "state")
		eq(t, r.Origin != "yolo", true, "origin")
	})
	tw(t, f, "leaves display surfaces yolo-free even when yolo is enabled", func(t *testing.T) {
		m := makeManager(t, `{"bash":"ask"}`, on, nil)
		// getComposedConfigRules shows the configured action, not the rewrite.
		var bashRule *Rule
		for _, r := range m.GetComposedConfigRules(nil) {
			if r.Surface == "bash" {
				r := r
				bashRule = &r
				break
			}
		}
		eq(t, bashRule != nil, true, "bash rule")
		eq(t, bashRule.Action, "ask", "action")
		eq(t, bashRule.Origin != "yolo", true, "origin")
		// getToolPermission reports the configured surface state.
		eq(t, m.GetToolPermission("bash", nil), "ask", "tool permission")
	})
	tw(t, f, "does not re-expose a denied tool — the rewrite touches asks, never denies", func(t *testing.T) {
		eq(t, makeManager(t, `{"bash":"deny"}`, on, nil).IsToolFullyDenied("bash", nil), true, "denied")
	})
	tw(t, f, "yolo re-permits a fail-closed floored allow (allow→ask→allow)", func(t *testing.T) {
		// Invalid project scope floors global's `allow` to `ask` at composition; yolo then rewrites the `ask` back to `allow`. yolo is
		// an explicit full-permissive opt-in, so yolo users are unaffected by the clamp.
		r := checkTool(t, makeManager(t, `{"bash":"allow"}`, on, &ScopeConfig{Invalid: true}), "bash", `{"command":"echo hi"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "yolo"}, "result")
	})
	tw(t, f, "preserves a hard deny under yolo even with an invalid scope", func(t *testing.T) {
		r := checkTool(t, makeManager(t, `{"bash":"deny"}`, on, &ScopeConfig{Invalid: true}), "bash", `{"command":"rm -rf /"}`)
		eq(t, r.State, "deny", "state")
		eq(t, r.Origin != "yolo", true, "origin")
	})
}
