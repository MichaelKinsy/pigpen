package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/input.test.ts and input-events.test.ts.

func (r *gfHandlerRig) input(t *testing.T, text string, ctx SessionContext) InputEventResult {
	t.Helper()
	out, err := r.handler.HandleInput(text, ctx)
	if err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	return out
}

func TestInputHandlerTwins(t *testing.T) {
	const f = "handlers/input"
	nameOf := func(text string) *string {
		n, ok := ExtractSkillNameFromInput(text)
		if !ok {
			return nil
		}
		return &n
	}
	state := func(s string) gfSurfaceCheck { return constCheck(PermissionCheckResult{State: s}) }
	handled, cont := InputEventResult{Action: "handled"}, InputEventResult{Action: "continue"}

	tw(t, f, "returns null for plain text", func(t *testing.T) { eq(t, nameOf("hello world"), (*string)(nil), "name") })
	tw(t, f, "returns null for empty string", func(t *testing.T) { eq(t, nameOf(""), (*string)(nil), "name") })
	tw(t, f, "returns null for bare /skill: with no name", func(t *testing.T) { eq(t, nameOf("/skill:"), (*string)(nil), "name") })
	tw(t, f, "extracts skill name from /skill:<name>", func(t *testing.T) { eq(t, nameOf("/skill:librarian"), sp("librarian"), "name") })
	tw(t, f, "extracts skill name stopping at whitespace", func(t *testing.T) { eq(t, nameOf("/skill:librarian some extra"), sp("librarian"), "name") })
	tw(t, f, "trims leading whitespace before the prefix", func(t *testing.T) { eq(t, nameOf("  /skill:my-skill"), sp("my-skill"), "name") })
	tw(t, f, "returns null when the skill name after trimming is empty", func(t *testing.T) { eq(t, nameOf("/skill: "), (*string)(nil), "name") })

	tw(t, f, "activates session with ctx", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t)
		rig.input(t, "hello", ctx)
		eq(t, len(rig.forwarding.started), 1, "starts")
	})
	tw(t, f, "returns continue for non-skill input", func(t *testing.T) {
		ctx, _ := makeCtx()
		eq(t, makeHandler(t).input(t, "just a message", ctx), cont, "result")
	})
	tw(t, f, "does not check permissions for non-skill input", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t)
		rig.input(t, "just a message", ctx)
		eq(t, len(rig.pm.checks), 0, "checks")
	})
	tw(t, f, "returns continue when skill is allowed", func(t *testing.T) {
		ctx, _ := makeCtx()
		eq(t, makeHandler(t).input(t, "/skill:librarian", ctx), cont, "result")
	})
	tw(t, f, "returns handled when skill is denied", func(t *testing.T) {
		ctx, _ := makeCtx()
		eq(t, makeHandler(t, gfHandlerOpts{checkPermission: state("deny")}).input(t, "/skill:librarian", ctx), handled, "result")
	})
	tw(t, f, "shows a warning notification when skill is denied and UI is available", func(t *testing.T) {
		ctx, n := makeCtx()
		makeHandler(t, gfHandlerOpts{checkPermission: state("deny")}).input(t, "/skill:librarian", ctx)
		eq(t, len(n.notified), 1, "notifications")
		eq(t, n.notified[0][1], "warning", "level")
		eq(t, strings.Contains(n.notified[0][0], "librarian"), true, "message")
	})
	tw(t, f, "does not show a warning notification when skill is denied and UI is absent", func(t *testing.T) {
		ctx, n := makeCtx(func(c *SessionContext) { c.HasUI = false })
		makeHandler(t, gfHandlerOpts{checkPermission: state("deny")}).input(t, "/skill:librarian", ctx)
		eq(t, len(n.notified), 0, "notifications")
	})
	tw(t, f, "returns handled when skill requires approval but no UI is available", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: state("ask"), prompter: gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })})
		eq(t, rig.input(t, "/skill:librarian", ctx), handled, "result")
	})
	tw(t, f, "prompts and returns continue when skill ask is approved", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: state("ask"), prompter: gfDecision(true, DecisionApproved, gfHuman)})
		eq(t, rig.input(t, "/skill:librarian", ctx), cont, "result")
		eq(t, len(rig.prompter.calls), 1, "escalations")
	})
	tw(t, f, "returns handled when skill ask is denied by user", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: state("ask"), prompter: gfDecision(false, DecisionDenied, gfHuman)})
		eq(t, rig.input(t, "/skill:librarian", ctx), handled, "result")
	})
	tw(t, f, "passes agentName in the prompt permission request", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: state("ask"), prompter: gfDecision(true, DecisionApproved, gfHuman),
			resolveAgent: func(ActiveAgentContext, string) (*string, error) { return sp("code-agent"), nil }})
		rig.input(t, "/skill:librarian", ctx)
		eq(t, len(rig.prompter.calls), 1, "escalations")
		eq(t, [2]any{rig.prompter.calls[0].AgentName, rig.prompter.calls[0].SkillName}, [2]any{sp("code-agent"), sp("librarian")}, "details")
	})
}

func TestInputEventsTwins(t *testing.T) {
	const f = "handlers/input-events"
	skillCheck := func(state string) gfSurfaceCheck {
		return constCheck(makeCheckResult(ckState(state, "*"), func(c *PermissionCheckResult) { c.ToolName, c.Source, c.Origin = "skill", "skill", "global" }))
	}
	one := func(t *testing.T, rig *gfHandlerRig, value, result, resolution string) {
		t.Helper()
		d := rig.events.decisions()
		eq(t, len(d), 1, "decisions")
		eq(t, [4]string{d[0].Value, d[0].Surface, d[0].Result, string(d[0].Resolution)}, [4]string{value, "skill", result, resolution}, "decision")
	}
	tw(t, f, "does not emit when input is not a skill invocation", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t)
		rig.input(t, "hello world", ctx)
		eq(t, len(rig.events.decisions()), 0, "decisions")
	})
	tw(t, f, "emits allow with policy_allow for an allowed skill", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("allow")})
		rig.input(t, "/skill:librarian", ctx)
		one(t, rig, "librarian", "allow", "policy_allow")
	})
	tw(t, f, "emits deny with policy_deny for a denied skill", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("deny")})
		rig.input(t, "/skill:restricted", ctx)
		one(t, rig, "restricted", "deny", "policy_deny")
	})
	tw(t, f, "emits allow with user_approved when state=ask and user approves", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("ask"), prompter: gfDecision(true, DecisionApproved, gfHuman)})
		rig.input(t, "/skill:explorer", ctx)
		one(t, rig, "explorer", "allow", "user_approved")
	})
	tw(t, f, "emits deny with user_denied when state=ask and user denies", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("ask"), prompter: gfDecision(false, DecisionDenied, gfHuman)})
		rig.input(t, "/skill:explorer", ctx)
		one(t, rig, "explorer", "deny", "user_denied")
	})
	tw(t, f, "emits deny with confirmation_unavailable when state=ask but no UI", func(t *testing.T) {
		ctx, _ := makeCtx(func(c *SessionContext) { c.HasUI = false })
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("ask"), prompter: gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })})
		rig.input(t, "/skill:explorer", ctx)
		one(t, rig, "explorer", "deny", "confirmation_unavailable")
	})
	tw(t, f, "emits allow with auto_approved when yolo decided the escalated ask", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: skillCheck("ask"), prompter: gfDecision(true, DecisionApproved, DecisionSource{Kind: "yolo", Pattern: sp("*")})})
		rig.input(t, "/skill:explorer", ctx)
		one(t, rig, "explorer", "allow", "auto_approved")
	})
}
