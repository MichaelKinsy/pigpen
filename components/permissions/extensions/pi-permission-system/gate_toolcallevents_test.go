package pi_permission_system

import "testing"

// Twins of test/handlers/tool-call-events.test.ts.

func TestToolCallEventsTwins(t *testing.T) {
	const f = "handlers/tool-call-events"
	one := func(t *testing.T, rig *gfHandlerRig) PermissionDecisionEvent {
		t.Helper()
		d := rig.events.decisions()
		eq(t, len(d), 1, "decisions")
		return d[0]
	}
	run := func(t *testing.T, opts gfHandlerOpts, event any, mut ...func(*SessionContext)) *gfHandlerRig {
		t.Helper()
		ctx, _ := makeCtx(mut...)
		rig := makeHandler(t, opts)
		rig.toolCall(t, event, ctx)
		return rig
	}
	ask := constCheck(makeCheckResult(ckState("ask")))
	pair := func(e PermissionDecisionEvent) [2]string { return [2]string{e.Result, string(e.Resolution)} }

	tw(t, f, "emits allow with policy_allow when checkPermission returns allow", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: constCheck(makeCheckResult(ckState("allow", "*"), func(c *PermissionCheckResult) { c.Origin = "global" }))}, makeToolCallEvent("read"))
		d := one(t, rig)
		eq(t, [5]any{d.Surface, d.Result, d.Resolution, d.Origin, d.MatchedPattern}, [5]any{"read", "allow", PermissionDecisionResolution("policy_allow"), sp("global"), sp("*")}, "decision")
	})
	tw(t, f, "emits deny with policy_deny when checkPermission returns deny", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: constCheck(makeCheckResult(ckState("deny", "read"), func(c *PermissionCheckResult) { c.Origin = "project" }))}, makeToolCallEvent("read"))
		d := one(t, rig)
		eq(t, [3]string{d.Surface, d.Result, string(d.Resolution)}, [3]string{"read", "deny", "policy_deny"}, "decision")
	})
	tw(t, f, "emits allow with session_approved when checkPermission returns source:session", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: constCheck(makeCheckResult(ckState("allow", "git *"), func(c *PermissionCheckResult) { c.Source = "session" }))},
			makeToolCallEvent("bash", "input", obj("command", "git status")))
		d := one(t, rig)
		eq(t, [3]string{d.Surface, d.Result, string(d.Resolution)}, [3]string{"bash", "allow", "session_approved"}, "decision")
	})
	tw(t, f, "emits allow with user_approved when state=ask and user approves once", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: ask, prompter: gfDecision(true, DecisionApproved, gfHuman)}, makeToolCallEvent("read"))
		eq(t, pair(one(t, rig)), [2]string{"allow", "user_approved"}, "decision")
	})
	tw(t, f, "emits allow with user_approved_for_session when user approves for session", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: ask, prompter: gfDecision(true, DecisionApprovedForSession, gfHuman)}, makeToolCallEvent("read"))
		eq(t, pair(one(t, rig)), [2]string{"allow", "user_approved_for_session"}, "decision")
	})
	tw(t, f, "emits deny with user_denied when state=ask and user denies", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: ask, prompter: gfDecision(false, DecisionDenied, gfHuman)}, makeToolCallEvent("read"))
		eq(t, pair(one(t, rig)), [2]string{"deny", "user_denied"}, "decision")
	})
	tw(t, f, "emits deny with confirmation_unavailable when state=ask but no UI", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: ask, prompter: gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })},
			makeToolCallEvent("read"), func(c *SessionContext) { c.HasUI = false })
		eq(t, pair(one(t, rig)), [2]string{"deny", "confirmation_unavailable"}, "decision")
	})
	tw(t, f, "emits allow with infrastructure_auto_allowed for Pi infra reads", func(t *testing.T) {
		const infraDir = "/test/agent"
		rig := run(t, gfHandlerOpts{checkPermission: constCheck(makeCheckResult()), infraScope: func() InfrastructureReadScope {
			return InfrastructureReadScope{Dirs: []string{infraDir}, ExcludedDirs: []string{}}
		}}, makeToolCallEvent("read", "input", obj("path", infraDir+"/some-file.json")))
		n := 0
		for _, d := range rig.events.decisions() {
			if d.Resolution == "infrastructure_auto_allowed" {
				n++
				eq(t, d.Result, "allow", "result")
			}
		}
		eq(t, n, 1, "infrastructure events")
	})
	tw(t, f, "emits allow with auto_approved when yolo decided the escalated ask", func(t *testing.T) {
		rig := run(t, gfHandlerOpts{checkPermission: ask, prompter: gfDecision(true, DecisionApproved, DecisionSource{Kind: "yolo", Pattern: sp("*")})}, makeToolCallEvent("read"))
		eq(t, pair(one(t, rig)), [2]string{"allow", "auto_approved"}, "decision")
	})
}
