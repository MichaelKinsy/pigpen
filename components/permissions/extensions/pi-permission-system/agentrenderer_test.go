package pi_permission_system

import (
	"strings"
	"testing"
)

// arPayload is payload() of the agent-renderer test: a payload of the given kind with request facts and evidence overridden.
func arPayload(kind string, mod func(*PromptRequestFacts), evidence ...PromptEvidence) PromptPayload {
	return ppPayload(kind, func(p *PromptPayload) {
		if mod != nil {
			mod(&p.Request)
		}
		if evidence != nil {
			p.Evidence = evidence
		}
	})
}

// arBash is bashPayload(): a bash ask, the shape whose value the renderer must never echo.
func arBash(mod func(*PromptRequestFacts)) PromptPayload {
	return arPayload("bash", func(r *PromptRequestFacts) {
		r.Surface, r.ToolName, r.Value, r.MatchedPattern = "bash", sp("bash"), "rm -rf build", sp("rm *")
		if mod != nil {
			mod(r)
		}
	})
}

func arFacts(surface string, tool *string, value string, matched *string) func(*PromptRequestFacts) {
	return func(r *PromptRequestFacts) {
		r.Surface, r.ToolName, r.Value, r.MatchedPattern = surface, tool, value, matched
	}
}

func TestAgentRendererTwins(t *testing.T) {
	const f = "presentation/agent-renderer"
	const tag = "[pi-permission-system] "
	budget := DefaultAgentRenderBudget
	policy := func(p PromptPayload, reason *string) string { return RenderPolicyDenial(p, reason, budget) }
	user := func(p PromptPayload, reason *string) string { return RenderUserDenial(p, reason, budget) }
	unavailable := func(p PromptPayload, reason *string) string { return RenderUnavailableDenial(p, reason, budget) }
	authorizer := func(p PromptPayload, name string, reason *string) string {
		return RenderAuthorizerDenial(p, name, reason, budget)
	}
	escalated := func(p PromptPayload, rule EscalatedRule, reason *string) string {
		return RenderEscalatedPolicyDenial(p, rule, reason, budget)
	}
	gateError := func(p PromptPayload, failure GateFailure) string { return RenderGateErrorDenial(p, failure, budget) }
	refusal := func(p PromptPayload, by DecisionSource, reason *string) string {
		return RenderRefusal(p, by, reason, budget)
	}
	workingDir := evid("working directory", "/repo", nil)
	bigCommand := strings.Repeat("x", 70000)

	tw(t, f, "attributes every block reason to this extension", func(t *testing.T) {
		eq(t, ExtensionTag, "[pi-permission-system]", "tag")
	})

	// renderPolicyDenial
	tw(t, f, "names the surface and the matched rule for a bash deny", func(t *testing.T) {
		eq(t, policy(arBash(nil), nil), tag+"Denied by policy: 'bash' (rule 'rm *').", "text")
	})
	tw(t, f, "never echoes the command, however large", func(t *testing.T) {
		command := "cat <<'EOF' > gen.py\n" + bigCommand + "\nEOF"
		rendered := policy(arBash(func(r *PromptRequestFacts) { r.Value, r.MatchedPattern = command, sp("*") }), nil)
		eq(t, rendered, tag+"Denied by policy: 'bash' (rule '*').", "text")
		eq(t, strings.Contains(rendered, "xxx"), false, "echo")
	})
	tw(t, f, "names the tool when it differs from the surface", func(t *testing.T) {
		p := arPayload("path", arFacts("path", sp("read"), "/etc/passwd", sp("/etc/*")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'path' for tool 'read' for path '/etc/passwd' (rule '/etc/*').", "text")
	})
	tw(t, f, "names the invoked tool when a shell alias re-exposed bash", func(t *testing.T) {
		p := arBash(func(r *PromptRequestFacts) { r.InvokedToolName = sp("exec_command") })
		eq(t, policy(p, nil), tag+"Denied by policy: 'bash' (invoked as 'exec_command') (rule 'rm *').", "text")
	})
	tw(t, f, "names the requesting agent when the ask carries one", func(t *testing.T) {
		p := arBash(func(r *PromptRequestFacts) { r.Requester = PromptRequester{AgentName: sp("scout")} })
		eq(t, policy(p, nil), tag+"Denied by policy: 'bash' for agent 'scout' (rule 'rm *').", "text")
	})
	tw(t, f, "carries the operator's deny-with-reason text", func(t *testing.T) {
		eq(t, policy(arBash(nil), sp("destructive by policy")), tag+"Denied by policy: 'bash' (rule 'rm *'). Reason: destructive by policy.", "text")
	})
	tw(t, f, "names the nested context a bash unit ran in", func(t *testing.T) {
		p := arBash(func(r *PromptRequestFacts) { r.CommandContext = sp("command_substitution") })
		eq(t, policy(p, nil), tag+"Denied by policy: 'bash' (rule 'rm *', inside command substitution).", "text")
	})
	for _, sentinel := range []string{"<indirection-bash-wrapper>", "<opaque-bash-wrapper>", "<unparseable-bash-command>", "<unparsed-bash-subtree>"} {
		// it.each: the parametrized title is not in the upstream ledger.
		t.Run("surfaces the "+sentinel+" sentinel as the matched rule", func(t *testing.T) {
			p := arBash(func(r *PromptRequestFacts) { r.MatchedPattern = sp(sentinel) })
			eq(t, policy(p, nil), tag+"Denied by policy: 'bash' (rule '"+sentinel+"').", "text")
		})
	}
	tw(t, f, "omits the rule clause when no pattern matched", func(t *testing.T) {
		eq(t, policy(arBash(func(r *PromptRequestFacts) { r.MatchedPattern = nil }), nil), tag+"Denied by policy: 'bash'.", "text")
	})
	tw(t, f, "names the escaped boundary for a tool external-directory deny", func(t *testing.T) {
		p := arPayload("external_directory", arFacts("external_directory", sp("write"), "/etc/hosts", sp("*")), workingDir)
		eq(t, policy(p, nil), tag+"Denied by policy: 'external_directory' for tool 'write' for path '/etc/hosts' (rule '*'): outside working directory '/repo'.", "text")
	})
	tw(t, f, "names the canonical target when a path resolves elsewhere", func(t *testing.T) {
		p := arPayload("external_directory", arFacts("external_directory", sp("read"), "link", sp("*")), evid("resolves to", "/etc/shadow", nil), workingDir)
		eq(t, policy(p, nil), tag+"Denied by policy: 'external_directory' for tool 'read' for path 'link' (resolves to '/etc/shadow') (rule '*'): outside working directory '/repo'.", "text")
	})
	tw(t, f, "names every escaping path but not the command for a bash external-directory deny", func(t *testing.T) {
		p := arPayload("bash_external_directory", arFacts("external_directory", sp("bash"), "diff /etc/hosts ~/.ssh/config", sp("*")),
			workingDir, evid("external path", "/etc/hosts", nil), evid("external path", "~/.ssh/config", sp("/home/me/.ssh/config")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'external_directory' for tool 'bash' for paths '/etc/hosts', '~/.ssh/config' (resolves to '/home/me/.ssh/config') (rule '*'): outside working directory '/repo'.", "text")
	})
	tw(t, f, "names the MCP target", func(t *testing.T) {
		p := arPayload("mcp", arFacts("mcp", sp("mcp"), "github:create_issue", sp("github:*")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'mcp' for target 'github:create_issue' (rule 'github:*').", "text")
	})
	tw(t, f, "names the skill", func(t *testing.T) {
		p := arPayload("skill", arFacts("skill", nil, "deploy", sp("deploy")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'skill' for skill 'deploy' (rule 'deploy').", "text")
	})
	tw(t, f, "names the skill a read reached, and the path it reached it through", func(t *testing.T) {
		p := arPayload("skill_read", arFacts("skill", nil, "deploy", sp("deploy")), evid("read path", ".pi/skills/deploy/SKILL.md", nil))
		eq(t, policy(p, nil), tag+"Denied by policy: 'skill' for skill 'deploy' (rule 'deploy'), reached via '.pi/skills/deploy/SKILL.md'.", "text")
	})
	tw(t, f, "states the tool once for a generic tool ask, whose value is its own name", func(t *testing.T) {
		p := arPayload("tool", arFacts("webfetch", sp("webfetch"), "webfetch", sp("web*")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'webfetch' (rule 'web*').", "text")
	})
	tw(t, f, "names nothing beyond the surface for a payload-less forwarded relay", func(t *testing.T) {
		p := arPayload("forwarded", arFacts("bash", sp("bash"), "rm -rf /", sp("*")))
		eq(t, policy(p, nil), tag+"Denied by policy: 'bash' (rule '*').", "text")
	})

	// renderUserDenial
	tw(t, f, "attributes the refusal to the user", func(t *testing.T) {
		eq(t, user(arBash(nil), nil), tag+"The user denied this 'bash' call (rule 'rm *').", "text")
	})
	tw(t, f, "carries the human's typed reason", func(t *testing.T) {
		eq(t, user(arBash(nil), sp("not with sudo")), tag+"The user denied this 'bash' call (rule 'rm *'). Reason: not with sudo.", "text")
	})
	bigBash := func() PromptPayload {
		return arBash(func(r *PromptRequestFacts) { r.Value, r.MatchedPattern = bigCommand, sp("*") })
	}
	tw(t, f, "never echoes the command", func(t *testing.T) {
		eq(t, user(bigBash(), sp("too big")), tag+"The user denied this 'bash' call (rule '*'). Reason: too big.", "user")
	})
	tw(t, f, "names the flagged path for a tool ask", func(t *testing.T) {
		p := arPayload("path", arFacts("path", sp("read"), "/etc/passwd", sp("/etc/*")))
		eq(t, user(p, sp("not that file")), tag+"The user denied this 'path' call for tool 'read' for path '/etc/passwd' (rule '/etc/*'). Reason: not that file.", "user")
	})

	// renderUnavailableDenial
	tw(t, f, "states that approval was required and unreachable", func(t *testing.T) {
		eq(t, unavailable(arBash(nil), nil), tag+"This 'bash' call (rule 'rm *') requires approval, but no interactive UI is available.", "text")
	})
	tw(t, f, "carries an abandoning authority's reason", func(t *testing.T) {
		got := unavailable(arBash(nil), sp("Session 'parent-1' is not serving forwarded requests"))
		eq(t, got, tag+"This 'bash' call (rule 'rm *') requires approval, but no interactive UI is available. Reason: Session 'parent-1' is not serving forwarded requests.", "text")
	})
	tw(t, f, "names the flagged path for a tool ask", func(t *testing.T) {
		p := arPayload("path", arFacts("path", sp("read"), "/etc/passwd", sp("/etc/*")))
		eq(t, unavailable(p, nil), tag+"This 'path' call for tool 'read' for path '/etc/passwd' (rule '/etc/*') requires approval, but no interactive UI is available.", "unavailable")
	})
	tw(t, f, "omits the escaped boundary, which no retry shape would change", func(t *testing.T) {
		p := arPayload("external_directory", arFacts("external_directory", sp("write"), "/etc/hosts", sp("*")), workingDir)
		eq(t, unavailable(p, nil), tag+"This 'external_directory' call for tool 'write' for path '/etc/hosts' (rule '*') requires approval, but no interactive UI is available.", "unavailable")
	})

	// renderAuthorizerDenial
	tw(t, f, "attributes the refusal to the named link, not the user", func(t *testing.T) {
		eq(t, authorizer(arBash(nil), "model-judge", nil), tag+"The 'model-judge' authorizer denied this 'bash' call (rule 'rm *').", "text")
	})
	tw(t, f, "carries the link's corrective reason", func(t *testing.T) {
		p := arPayload("external_directory", arFacts("external_directory", sp("read"), "/elsewhere/service.test.ts", sp("*")), workingDir)
		eq(t, authorizer(p, "model-judge", sp("Doubled package segment detected")),
			tag+"The 'model-judge' authorizer denied this 'external_directory' call for tool 'read' for path '/elsewhere/service.test.ts' (rule '*'): outside working directory '/repo'. Reason: Doubled package segment detected.", "text")
	})

	tw(t, f, "never echoes the command", func(t *testing.T) {
		eq(t, authorizer(bigBash(), "model-judge", sp("too big")), tag+"The 'model-judge' authorizer denied this 'bash' call (rule '*'). Reason: too big.", "authorizer")
	})

	// renderEscalatedPolicyDenial
	serving := EscalatedRule{Pattern: sp("git push --force*"), DecidedElsewhere: true}
	tw(t, f, "names the rule that decided, not the rule that raised the ask", func(t *testing.T) {
		// The payload's own pattern is 'rm *'; the deciding node's is not.
		eq(t, escalated(arBash(nil), serving, nil), tag+"A policy rule in the session serving this request denied this 'bash' call (rule 'git push --force*').", "text")
	})
	tw(t, f, "carries the deciding rule's deny-with-reason text", func(t *testing.T) {
		eq(t, escalated(arBash(nil), serving, sp("force pushes are blocked")),
			tag+"A policy rule in the session serving this request denied this 'bash' call (rule 'git push --force*'). Reason: force pushes are blocked.", "text")
	})
	tw(t, f, "omits the rule clause when the deciding node recorded no pattern", func(t *testing.T) {
		eq(t, escalated(arBash(nil), EscalatedRule{DecidedElsewhere: true}, nil), tag+"A policy rule in the session serving this request denied this 'bash' call.", "text")
	})
	tw(t, f, "claims no other session when the rule decided here", func(t *testing.T) {
		eq(t, escalated(arBash(nil), EscalatedRule{Pattern: sp("git push --force*")}, nil), tag+"A policy rule denied this 'bash' call (rule 'git push --force*').", "text")
	})
	tw(t, f, "keeps the nested context the substituted pattern fired in", func(t *testing.T) {
		p := arBash(func(r *PromptRequestFacts) { r.CommandContext = sp("command_substitution") })
		eq(t, escalated(p, serving, nil),
			tag+"A policy rule in the session serving this request denied this 'bash' call (rule 'git push --force*', inside command substitution).", "text")
	})
	tw(t, f, "never echoes the command", func(t *testing.T) {
		eq(t, escalated(arBash(func(r *PromptRequestFacts) { r.Value = bigCommand }), EscalatedRule{Pattern: sp("*"), DecidedElsewhere: true}, sp("too big")),
			tag+"A policy rule in the session serving this request denied this 'bash' call (rule '*'). Reason: too big.", "escalated")
	})
	tw(t, f, "names the escaped boundary for a path ask", func(t *testing.T) {
		p := arPayload("external_directory", arFacts("external_directory", sp("read"), "/elsewhere/service.ts", sp("*")), workingDir)
		eq(t, escalated(p, EscalatedRule{Pattern: sp("/elsewhere/*"), DecidedElsewhere: true}, nil),
			tag+"A policy rule in the session serving this request denied this 'external_directory' call for tool 'read' for path '/elsewhere/service.ts' (rule '/elsewhere/*'): outside working directory '/repo'.", "text")
	})

	// renderGateErrorDenial
	tw(t, f, "states that the authority failed and the call was blocked fail-closed", func(t *testing.T) {
		eq(t, gateError(arBash(nil), GateFailure{Reason: "Cannot read properties of undefined", DecidedElsewhere: true}),
			tag+"The permission authority in the session serving this request failed to answer this 'bash' call (rule 'rm *'), so it was blocked (fail-closed). Reason: Cannot read properties of undefined.", "text")
	})
	tw(t, f, "claims no other session when the failure happened here", func(t *testing.T) {
		eq(t, gateError(arBash(nil), GateFailure{Reason: "boom"}), tag+"The permission authority failed to answer this 'bash' call (rule 'rm *'), so it was blocked (fail-closed). Reason: boom.", "text")
	})
	tw(t, f, "omits the escaped boundary, which no retry shape would change", func(t *testing.T) {
		q := arPayload("external_directory", arFacts("external_directory", sp("read"), "/elsewhere/service.ts", sp("*")), workingDir)
		eq(t, gateError(q, GateFailure{Reason: "boom", DecidedElsewhere: true}),
			tag+"The permission authority in the session serving this request failed to answer this 'external_directory' call for tool 'read' for path '/elsewhere/service.ts' (rule '*'), so it was blocked (fail-closed). Reason: boom.", "gate error")
	})
	tw(t, f, "never echoes the command", func(t *testing.T) {
		eq(t, gateError(bigBash(), GateFailure{Reason: "boom", DecidedElsewhere: true}),
			tag+"The permission authority in the session serving this request failed to answer this 'bash' call (rule '*'), so it was blocked (fail-closed). Reason: boom.", "gate error")
	})

	// renderRefusal
	link := DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("reads outside the project")}
	human := DecisionSource{Kind: "user", Via: "dialog"}
	absent := DecisionSource{Kind: "unavailable", Reason: sp("no serving session")}
	tw(t, f, "names the link when a chain link refused", func(t *testing.T) {
		eq(t, refusal(arBash(nil), link, sp("reads outside")), tag+"The 'model-judge' authorizer denied this 'bash' call (rule 'rm *'). Reason: reads outside.", "text")
	})
	tw(t, f, "names the user when a human refused", func(t *testing.T) {
		eq(t, refusal(arBash(nil), human, sp("not with sudo")), tag+"The user denied this 'bash' call (rule 'rm *'). Reason: not with sudo.", "text")
	})
	tw(t, f, "states that approval was unreachable when nobody could rule", func(t *testing.T) {
		eq(t, refusal(arBash(nil), absent, sp("no serving session")),
			tag+"This 'bash' call (rule 'rm *') requires approval, but no interactive UI is available. Reason: no serving session.", "text")
	})
	tw(t, f, "names the link that refused inside a serving session", func(t *testing.T) {
		// The hop says where; the agent is told what refused.
		by := DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &link}
		eq(t, refusal(arBash(nil), by, sp("reads outside")), tag+"The 'model-judge' authorizer denied this 'bash' call (rule 'rm *'). Reason: reads outside.", "text")
	})
	tw(t, f, "names the serving session's rule when its policy refused", func(t *testing.T) {
		rule := DecisionSource{Kind: "rule", Surface: "bash", Pattern: sp("git push"), Origin: sp("global")}
		by := DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &rule}
		eq(t, refusal(arBash(nil), by, nil), tag+"A policy rule in the session serving this request denied this 'bash' call (rule 'git push').", "text")
	})
	tw(t, f, "reports an escalation that threw in the serving session as a failure", func(t *testing.T) {
		// The detail rides decidedBy.reason; this path carries no denialReason at all, so a render reading it would say nothing.
		inner := DecisionSource{Kind: "gate_error", Reason: sp("boom")}
		by := DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &inner}
		eq(t, refusal(arBash(nil), by, nil),
			tag+"The permission authority in the session serving this request failed to answer this 'bash' call (rule 'rm *'), so it was blocked (fail-closed). Reason: boom.", "text")
	})
	tw(t, f, "claims no other session for a rule that decided here", func(t *testing.T) {
		rule := DecisionSource{Kind: "rule", Surface: "bash", Pattern: sp("git push"), Origin: sp("global")}
		eq(t, refusal(arBash(nil), rule, nil), tag+"A policy rule denied this 'bash' call (rule 'git push').", "text")
	})

	// the flagged-element field cap
	tw(t, f, "shortens an oversized path and marks it", func(t *testing.T) {
		long := "/etc/" + strings.Repeat("a", 500)
		p := arPayload("path", arFacts("path", sp("read"), long, sp("*")))
		got := RenderPolicyDenial(p, nil, AgentRenderBudget{FieldMaxWidth: 20})
		eq(t, got, tag+"Denied by policy: 'path' for tool 'read' for path '/etc/aaaaaaaaaaaaaaa\u2026' (rule '*').", "text")
	})
	tw(t, f, "leaves a path within the budget untouched", func(t *testing.T) {
		p := arPayload("path", arFacts("path", sp("read"), "/etc/hosts", sp("*")))
		got := RenderPolicyDenial(p, nil, AgentRenderBudget{FieldMaxWidth: 400})
		eq(t, got, tag+"Denied by policy: 'path' for tool 'read' for path '/etc/hosts' (rule '*').", "text")
	})
}
