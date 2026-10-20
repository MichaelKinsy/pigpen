package pi_permission_system

import (
	"regexp"
	"strings"
	"testing"
)

// Twins of test/handlers/tool-call.test.ts.

func constCheck(c PermissionCheckResult) gfSurfaceCheck {
	return func(string, any, *string, Ruleset) PermissionCheckResult { return c }
}

func TestToolCallHandlerTwins(t *testing.T) {
	const f = "handlers/tool-call"
	blocked := func(t *testing.T, o GateOutcome) {
		t.Helper()
		if o.Action != "block" {
			t.Fatalf("got %+v, want block", o)
		}
	}
	denyAll := constCheck(makeCheckResult(ckState("deny")))

	tw(t, f, "returns the input field when present", func(t *testing.T) {
		eq(t, jsonOf(t, GetEventInput(obj("input", obj("path", "/foo")))), `{"path":"/foo"}`, "input")
	})
	tw(t, f, "returns the arguments field when input is absent", func(t *testing.T) {
		eq(t, jsonOf(t, GetEventInput(obj("arguments", obj("command", "ls")))), `{"command":"ls"}`, "input")
	})
	tw(t, f, "returns empty object when neither field is present", func(t *testing.T) {
		eq(t, jsonOf(t, GetEventInput(obj("type", "tool_call"))), `{}`, "input")
	})
	tw(t, f, "prefers input over arguments when both are present", func(t *testing.T) {
		eq(t, jsonOf(t, GetEventInput(obj("input", obj("a", 1.0), "arguments", obj("b", 2.0)))), `{"a":1}`, "input")
	})

	tw(t, f, "activates session with ctx", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t)
		rig.toolCall(t, makeToolCallEvent("read"), ctx)
		// session.activate(ctx) calls forwarding.start(ctx) on the real session
		eq(t, len(rig.forwarding.started), 1, "starts")
		eq(t, rig.forwarding.started[0].Cwd, ctx.Cwd, "cwd")
	})
	tw(t, f, "blocks when tool name cannot be resolved", func(t *testing.T) {
		ctx, _ := makeCtx()
		out := makeHandler(t).toolCall(t, obj("type", "tool_call"), ctx)
		if out.Action != "block" || !strings.Contains(out.Reason, "tool") {
			t.Errorf("outcome %+v", out)
		}
	})
	tw(t, f, "blocks when tool is not registered", func(t *testing.T) {
		ctx, _ := makeCtx()
		blocked(t, makeHandler(t, gfHandlerOpts{tools: []string{"read"}}).toolCall(t, makeToolCallEvent("unknown-tool"), ctx))
	})
	tw(t, f, "returns empty object when tool is allowed", func(t *testing.T) {
		ctx, _ := makeCtx()
		eq(t, makeHandler(t).toolCall(t, makeToolCallEvent("read"), ctx), gfAllow, "outcome")
	})
	tw(t, f, "blocks when tool is denied by policy", func(t *testing.T) {
		ctx, _ := makeCtx()
		blocked(t, makeHandler(t, gfHandlerOpts{checkPermission: denyAll}).toolCall(t, makeToolCallEvent("read"), ctx))
	})

	skillEntry := SkillPromptEntry{Name: "librarian", Description: "Research skills", Location: "/skills/librarian/SKILL.md", State: "deny",
		NormalizedLocation: "/skills/librarian/SKILL.md", NormalizedBaseDir: "/skills/librarian"}
	skillRig := func(t *testing.T) *gfHandlerRig {
		return makeHandler(t, gfHandlerOpts{skillEntries: func() []SkillPromptEntry { return []SkillPromptEntry{skillEntry} }, toolRegistryAll: []any{obj("toolName", "read")}})
	}
	tw(t, f, "blocks a read of a denied skill path", func(t *testing.T) {
		ctx, _ := makeCtx()
		event := obj("type", "tool_call", "toolCallId", "tc-skill", "toolName", "read", "input", obj("path", "/skills/librarian/SKILL.md"))
		blocked(t, skillRig(t).toolCall(t, event, ctx))
	})
	tw(t, f, "allows a read of a non-skill path even when skill entries are present", func(t *testing.T) {
		ctx, _ := makeCtx()
		event := obj("type", "tool_call", "toolCallId", "tc-ok", "toolName", "read", "input", obj("path", "/test/project/src/index.ts"))
		eq(t, skillRig(t).toolCall(t, event, ctx), gfAllow, "outcome")
	})

	tw(t, f, "blocks a read of a path outside cwd when policy is deny", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: denyAll, tools: []string{"read"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("read", "input", obj("path", "/outside/project/file.ts")), ctx))
	})
	tw(t, f, "blocks a bash command referencing an external path when policy is deny", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: denyAll, tools: []string{"bash"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat /outside/project/file.ts")), ctx))
	})

	tw(t, f, "blocks a read of .env when path surface denies *.env", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{"path": sc("deny", ckPattern("*.env"))}), tools: []string{"read"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("read", "input", obj("path", ".env")), ctx))
	})
	tw(t, f, "allows a read when path surface allows", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{tools: []string{"read"}})
		eq(t, rig.toolCall(t, makeToolCallEvent("read", "input", obj("path", "src/index.ts")), ctx), gfAllow, "outcome")
	})
	tw(t, f, "blocks a bash command accessing .env when path surface denies", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{"path": sc("deny", ckPattern("*.env"))}), tools: []string{"bash"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat .env")), ctx))
	})

	tw(t, f, "blocks a chain when a later sub-command is denied (#301)", func(t *testing.T) {
		ctx, _ := makeCtx()
		re := regexp.MustCompile(`^npm\b`)
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeBashCommandCheck(re.MatchString, "npm *", "echo *"), tools: []string{"bash"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "echo start && npm install compromised-package")), ctx))
	})
	tw(t, f, "blocks a command nested inside command substitution (#306)", func(t *testing.T) {
		ctx, _ := makeCtx()
		re := regexp.MustCompile(`^rm\b`)
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeBashCommandCheck(re.MatchString, "rm *", "echo *"), tools: []string{"bash"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "echo $(rm -rf foo)")), ctx))
	})
	tw(t, f, "allows a single non-chained bash command", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{tools: []string{"bash"}})
		eq(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "echo hi")), ctx), gfAllow, "outcome")
	})

	// The reported policy: `find / *` is denied on the `bash` surface while every path outside the working directory asks (#899).
	denyFindAskOutside := func(surface string, input any, _ *string, _ Ruleset) PermissionCheckResult {
		if surface == "bash" {
			command, _ := ToRecord(input).str("command")
			if strings.HasPrefix(command, "find /") {
				return makeCheckResult(ckState("deny"), func(c *PermissionCheckResult) {
					c.Source, c.Command, c.MatchedPattern = "bash", sp(command), sp("find / *")
				})
			}
			return makeCheckResult(ckState("allow"), func(c *PermissionCheckResult) { c.Source, c.Command, c.MatchedPattern = "bash", sp(command), sp("*") })
		}
		if strings.HasPrefix(surface, "external_directory") {
			return makeCheckResult(ckState("ask", "*"))
		}
		return makeCheckResult(ckState("allow"))
	}
	tw(t, f, "blocks a denied command without asking the user to approve it", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: denyFindAskOutside, tools: []string{"bash"}})
		out := rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "ls /tmp/mermaid-check*.svg 2>&1; find / -maxdepth 2 -iname 'mermaid-check*'")), ctx)
		eq(t, len(rig.prompter.calls), 0, "escalations")
		blocked(t, out)
		if !strings.Contains(out.Reason, "find / *") {
			t.Errorf("reason %q", out.Reason)
		}
	})
	tw(t, f, "still asks when the outside-path ask is the most restrictive answer", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: denyFindAskOutside, tools: []string{"bash"}})
		out := rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat /etc/hosts")), ctx)
		eq(t, len(rig.prompter.calls) > 0, true, "escalated")
		eq(t, out, gfAllow, "outcome")
	})

	tw(t, f, "allows bash command with only internal paths when external_directory is denied", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{tools: []string{"bash"}})
		eq(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat src/index.ts")), ctx), gfAllow, "outcome")
	})
	tw(t, f, "blocks bash command with external path when external_directory is ask and no UI", func(t *testing.T) {
		ctx, _ := makeCtx(func(c *SessionContext) { c.HasUI = false })
		rig := makeHandler(t, gfHandlerOpts{
			checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{"external_directory": sc("ask", func(c *PermissionCheckResult) { c.Source = "special" })}),
			tools:           []string{"bash"},
			prompter:        gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true }),
		})
		out := rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat /etc/hosts")), ctx)
		blocked(t, out)
		eq(t, regexp.MustCompile(`(?i)no interactive UI`).MatchString(out.Reason), true, "reason")
	})
	tw(t, f, "allows bash command with external path when external_directory is allow", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{"external_directory": sc("allow", func(c *PermissionCheckResult) { c.Source = "special" })}), tools: []string{"bash"}})
		eq(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat /etc/hosts")), ctx), gfAllow, "outcome")
	})
	tw(t, f, "applies bash pattern deny after external_directory allow", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{
			"external_directory": sc("allow", func(c *PermissionCheckResult) { c.Source = "special" }),
			"bash":               sc("deny", func(c *PermissionCheckResult) { c.Source = "bash" }),
		}, sc("allow")), tools: []string{"bash"}})
		blocked(t, rig.toolCall(t, makeToolCallEvent("bash", "input", obj("command", "cat /etc/hosts")), ctx))
	})

	tw(t, f, "ask prompt includes serialized tool input for informed approval", func(t *testing.T) {
		ctx, _ := makeCtx()
		rig := makeHandler(t, gfHandlerOpts{
			checkPermission: makeSurfaceCheck(map[string]PermissionCheckResult{"weather_lookup": sc("ask")}),
			tools:           []string{"weather_lookup"},
			prompter:        gfDecision(false, DecisionDenied, gfHuman),
		})
		rig.toolCall(t, makeToolCallEvent("weather_lookup", "input", obj("city", "Chicago", "units", "metric")), ctx)
		eq(t, len(rig.prompter.calls), 1, "escalations")
		var text string
		for _, e := range rig.prompter.calls[0].Payload.Evidence {
			if e.Label == "input" {
				text = e.Text
				break
			}
		}
		eq(t, regexp.MustCompile(`\{"city":"Chicago","units":"metric"\}`).MatchString(text), true, "evidence "+text)
	})
}
