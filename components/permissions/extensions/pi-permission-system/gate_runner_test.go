package pi_permission_system

import (
	"regexp"
	"strings"
	"testing"
)

// Twins of test/handlers/gates/runner.test.ts.

var permIDRe = regexp.MustCompile(`^perm-`)

func gfDecision(approved bool, state PermissionDecisionState, by DecisionSource, mut ...func(*PermissionPromptDecision)) *gfEscalator {
	d := PermissionPromptDecision{Approved: approved, State: state, DecidedBy: by}
	for _, m := range mut {
		m(&d)
	}
	return gfAnswer(d)
}

func lastDecision(t *testing.T, d *gfRunnerDeps) PermissionDecisionEvent {
	t.Helper()
	if len(d.reporter.decisions) == 0 {
		t.Fatal("no decision event emitted")
	}
	return d.reporter.decisions[len(d.reporter.decisions)-1]
}

func mustRun(t *testing.T, r *GateRunner, gate GateResult, agent *string) GateOutcome {
	t.Helper()
	out, err := r.Run(gate, agent)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

var gfAllow = GateOutcome{Action: "allow"}

func TestGateRunnerTwins(t *testing.T) {
	const f = "handlers/gates/runner"
	askCheck := gfCheck(makeCheckResult(ckState("ask", "*")))
	denyCheck := gfCheck(makeCheckResult(ckState("deny", "*")))

	tw(t, f, "returns allow and emits policy_allow when policy is allow", func(t *testing.T) {
		r, d := makeGateRunner()
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		e := lastDecision(t, d)
		eq(t, [4]string{e.Surface, e.Value, e.Result, string(e.Resolution)}, [4]string{"read", "read", "allow", "policy_allow"}, "event")
	})
	tw(t, f, "returns block and emits policy_deny when policy is deny", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
		if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		e := lastDecision(t, d)
		eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"deny", "policy_deny"}, "event")
		hasFields(t, d.reporter.log(t, "permission_request.blocked"), "resolution", "policy_denied",
			"decidedBy", obj("kind", "rule", "surface", "read", "pattern", "*", "origin", "builtin"))
	})
	tw(t, f, "records which rule denied a blocked request", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("deny", "rm *")))})
		mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
			g.Surface = "bash"
			g.Payload = makePromptPayload(func(p *PromptPayload) {
				p.Kind = "bash"
				p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "bash", sp("bash"), "rm -rf build", sp("rm *")
			})
		}), nil)
		hasFields(t, d.reporter.log(t, "permission_request.blocked"), "surface", "bash", "matchedPattern", "rm *")
	})
	tw(t, f, "returns allow and emits session_approved on session hit", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(func(c *PermissionCheckResult) { c.Source = "session"; c.MatchedPattern = sp("git *") }))})
		out := mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
			g.Surface, g.Input, g.Decision = "bash", obj("command", "git status"), GateDecision{Surface: "bash", Value: "git status"}
		}), nil)
		eq(t, out, gfAllow, "outcome")
		hasFields(t, d.reporter.log(t, "permission_request.session_approved"), "resolution", "session_approved", "sessionApprovalPattern", "git *",
			"decidedBy", obj("kind", "session_approval", "surface", "bash", "pattern", "git *"))
		e := lastDecision(t, d)
		eq(t, [2]any{e.Resolution, e.MatchedPattern}, [2]any{PermissionDecisionResolution("session_approved"), sp("git *")}, "event")
	})
	tw(t, f, "returns allow and emits auto_approved on a yolo-origin allow without prompting", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(func(c *PermissionCheckResult) { c.Origin = "yolo"; c.MatchedPattern = sp("*") }))})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		eq(t, len(d.escalate.calls), 0, "escalations")
		hasFields(t, d.reporter.log(t, "permission_request.auto_approved"), "resolution", "auto_approved", "decidedBy", obj("kind", "yolo", "pattern", "*"))
		e := lastDecision(t, d)
		eq(t, [3]any{e.Result, e.Resolution, e.Origin}, [3]any{"allow", PermissionDecisionResolution("auto_approved"), sp("yolo")}, "event")
	})
	tw(t, f, "preserves the synthetic sentinel that raised a yolo-granted ask", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("ask", "<opaque-bash-wrapper>"))), isYoloEnabled: func() bool { return true }})
		mustRun(t, r, makeDescriptor(), nil)
		hasFields(t, d.reporter.log(t, "permission_request.auto_approved"), "decidedBy", obj("kind", "yolo", "pattern", "<opaque-bash-wrapper>"))
	})
	tw(t, f, "auto-approves a residual synthetic ask under yolo without prompting", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{yolo: true, resolveResult: gfCheck(makeCheckResult(ckState("ask", "<indirection-bash-wrapper>"), func(c *PermissionCheckResult) { c.Source, c.ToolName = "bash", "bash" }))})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		eq(t, len(d.escalate.calls), 0, "escalations")
		hasFields(t, d.reporter.log(t, "permission_request.auto_approved"), "resolution", "auto_approved")
		e := lastDecision(t, d)
		eq(t, [4]any{e.Result, e.Resolution, e.Origin, e.MatchedPattern}, [4]any{"allow", PermissionDecisionResolution("auto_approved"), sp("yolo"), sp("<indirection-bash-wrapper>")}, "event")
	})
	tw(t, f, "auto-approves an unparsed-subtree ask under yolo without prompting", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{yolo: true, resolveResult: gfCheck(makeCheckResult(ckState("ask", "<unparsed-bash-subtree>"), func(c *PermissionCheckResult) { c.Source, c.ToolName = "bash", "bash" }))})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		eq(t, len(d.escalate.calls), 0, "escalations")
		e := lastDecision(t, d)
		eq(t, [4]any{e.Result, e.Resolution, e.Origin, e.MatchedPattern}, [4]any{"allow", PermissionDecisionResolution("auto_approved"), sp("yolo"), sp("<unparsed-bash-subtree>")}, "event")
	})
	tw(t, f, "honours a session grant that survived the unparsed-subtree floor", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("ask", "<unparsed-bash-subtree>"), func(c *PermissionCheckResult) { c.Source, c.ToolName = "session", "bash" }))})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		eq(t, len(d.escalate.calls), 0, "escalations")
		hasFields(t, d.reporter.log(t, "permission_request.session_approved"), "resolution", "session_approved")
	})
	tw(t, f, "blocks an explicit deny under yolo without prompting", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{yolo: true, resolveResult: gfCheck(makeCheckResult(ckState("deny", "rm *")))})
		if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		eq(t, len(d.escalate.calls), 0, "escalations")
	})
	tw(t, f, "reads the yolo setting per run, so a mid-session toggle takes effect", func(t *testing.T) {
		yolo := false
		r, d := makeGateRunner(gfRunnerOpts{isYoloEnabled: func() bool { return yolo }, resolveResult: gfCheck(makeCheckResult(ckState("ask", "<unparseable-bash-command>")))})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, len(d.escalate.calls), 1, "escalations")
		yolo = true
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, len(d.escalate.calls), 1, "escalations")
	})
	tw(t, f, "returns allow and emits user_approved when ask + user approves", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, gfHuman)})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		e := lastDecision(t, d)
		eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"allow", "user_approved"}, "event")
	})
	tw(t, f, "returns allow, emits user_approved_for_session, and records session rule on approved_for_session", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApprovedForSession, gfHuman)})
		desc := makeDescriptor(func(g *GateDescriptor) { g.SessionApproval = SessionApprovalSingle("read", "*") })
		eq(t, mustRun(t, r, desc, nil), gfAllow, "outcome")
		eq(t, lastDecision(t, d).Resolution, PermissionDecisionResolution("user_approved_for_session"), "resolution")
		eq(t, d.recorder.recorded, []*SessionApproval{SessionApprovalSingle("read", "*")}, "recorded")
	})
	tw(t, f, "records the grants folded to their families when the decision names the family width", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApprovedForSession, gfHuman, func(p *PermissionPromptDecision) { p.SessionGrantWidth = "family" })})
		mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
			g.SessionApproval = SessionApprovalSingle("external_directory_write", "/outside/a/*")
		}), nil)
		eq(t, d.recorder.recorded, []*SessionApproval{SessionApprovalSingle("external_directory", "/outside/a/*")}, "recorded")
	})
	tw(t, f, "records the grants on their proven surfaces when the decision names no width", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApprovedForSession, gfHuman)})
		mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
			g.SessionApproval = SessionApprovalSingle("external_directory_write", "/outside/a/*")
		}), nil)
		eq(t, d.recorder.recorded, []*SessionApproval{SessionApprovalSingle("external_directory_write", "/outside/a/*")}, "recorded")
	})
	tw(t, f, "calls recordSessionApproval once with the full SessionApproval when sessionApproval has multiple patterns", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApprovedForSession, gfHuman)})
		approval := SessionApprovalForGrants([]ApprovalGrant{{Surface: "external_directory", Pattern: "/outside/a/*"}, {Surface: "external_directory", Pattern: "/outside/b/*"}})
		eq(t, mustRun(t, r, makeDescriptor(func(g *GateDescriptor) { g.SessionApproval = approval }), nil), gfAllow, "outcome")
		eq(t, d.recorder.recorded, []*SessionApproval{approval}, "recorded")
	})
	tw(t, f, "returns block and emits user_denied when ask + user denies", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, gfHuman)})
		if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		e := lastDecision(t, d)
		eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"deny", "user_denied"}, "event")
	})
	tw(t, f, "returns block and emits confirmation_unavailable when ask + no UI", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })})
		if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		e := lastDecision(t, d)
		eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"deny", "confirmation_unavailable"}, "event")
	})
	tw(t, f, "emits auto_approved resolution when yolo decided the escalated ask", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, DecisionSource{Kind: "yolo", Pattern: sp("*")})})
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
		eq(t, lastDecision(t, d).Resolution, PermissionDecisionResolution("auto_approved"), "resolution")
	})

	t.Run("attributes the ask to what decided it", func(t *testing.T) {
		tw(t, f, "emits authorizer_denied when a chain link refused the ask", func(t *testing.T) {
			r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDeniedWithReason, gfAuthorizer, func(p *PermissionPromptDecision) { p.DenialReason = sp("reads outside the project") })})
			if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
				t.Fatalf("got %v, want block", out)
			}
			e := lastDecision(t, d)
			eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"deny", "authorizer_denied"}, "event")
		})
		tw(t, f, "emits authorizer_allowed when a chain link granted the ask", func(t *testing.T) {
			r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "allow"})})
			eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
			e := lastDecision(t, d)
			eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"allow", "authorizer_allowed"}, "event")
		})
		tw(t, f, "emits policy_allow when a rule in the serving session answered the forwarded ask", func(t *testing.T) {
			rule := DecisionSource{Kind: "rule", Surface: "external_directory", Pattern: sp("/tmp/*"), Origin: sp("global")}
			r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &rule})})
			eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
			e := lastDecision(t, d)
			eq(t, [2]string{e.Result, string(e.Resolution)}, [2]string{"allow", "policy_allow"}, "event")
		})
		tw(t, f, "still emits user_approved when a human in the serving session answered", func(t *testing.T) {
			human := gfHuman
			r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &human})})
			mustRun(t, r, makeDescriptor(), nil)
			eq(t, lastDecision(t, d).Resolution, PermissionDecisionResolution("user_approved"), "resolution")
		})
	})

	tw(t, f, "uses preResolved.state instead of calling resolve", func(t *testing.T) {
		r, d := makeGateRunner()
		if out := mustRun(t, r, makeDescriptor(func(g *GateDescriptor) { g.PreResolved = &GatePreResolved{State: "deny"} }), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		eq(t, len(d.resolver.calls), 0, "resolves")
		eq(t, lastDecision(t, d).Resolution, PermissionDecisionResolution("policy_deny"), "resolution")
	})
	tw(t, f, "uses preResolved.state allow without calling resolve", func(t *testing.T) {
		r, d := makeGateRunner()
		eq(t, mustRun(t, r, makeDescriptor(func(g *GateDescriptor) { g.PreResolved = &GatePreResolved{State: "allow"} }), nil), gfAllow, "outcome")
		eq(t, len(d.resolver.calls), 0, "resolves")
		eq(t, lastDecision(t, d).Resolution, PermissionDecisionResolution("policy_allow"), "resolution")
	})
	tw(t, f, "passes agentName to resolve and decision event", func(t *testing.T) {
		r, d := makeGateRunner()
		eq(t, mustRun(t, r, makeDescriptor(), sp("test-agent")), gfAllow, "outcome")
		eq(t, d.resolver.calls, []AccessIntent{ToolAccessIntent{Surface: "read", Input: newObject(), AgentName: sp("test-agent")}}, "resolve calls")
		eq(t, lastDecision(t, d).AgentName, sp("test-agent"), "agent")
	})
	tw(t, f, "escalates a minted request id, not the tool call id", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck})
		mustRun(t, r, makeDescriptor(), nil)
		if len(d.escalate.calls) != 1 || !permIDRe.MatchString(d.escalate.calls[0].RequestID) {
			t.Fatalf("calls %+v", d.escalate.calls)
		}
		// The host's id keeps flowing as the join back to the Pi transcript.
		eq(t, d.escalate.calls[0].ToolCallID, sp("tc-1"), "toolCallId")
	})
	tw(t, f, "forwards the descriptor's sessionApproval suggestion on escalate", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck})
		mustRun(t, r, makeDescriptor(func(g *GateDescriptor) { g.SessionApproval = SessionApprovalSingle("bash", "git *") }), nil)
		eq(t, d.escalate.calls[0].SessionApproval, &ForwardedSessionApproval{Grants: []ApprovalGrant{{Surface: "bash", Pattern: "git *"}}}, "sessionApproval")
	})
	tw(t, f, "omits sessionApproval from escalate details when the descriptor has none", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.escalate.calls[0].SessionApproval, (*ForwardedSessionApproval)(nil), "sessionApproval")
	})
	tw(t, f, "does not call recordSessionApproval when user approves once (no sessionApproval)", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApproved, gfHuman)})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, len(d.recorder.recorded), 0, "recorded")
	})
	tw(t, f, "uses preCheck result directly instead of calling resolve", func(t *testing.T) {
		r, d := makeGateRunner()
		desc := makeDescriptor(func(g *GateDescriptor) {
			g.PreCheck = gfCheck(makeCheckResult(ckState("deny", "rm *"), func(c *PermissionCheckResult) { c.Origin = "global" }))
		})
		if out := mustRun(t, r, desc, nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
		eq(t, len(d.resolver.calls), 0, "resolves")
		e := lastDecision(t, d)
		eq(t, [3]any{e.Resolution, e.Origin, e.MatchedPattern}, [3]any{PermissionDecisionResolution("policy_deny"), sp("global"), sp("rm *")}, "event")
	})
	tw(t, f, "does not call recordSessionApproval when user approves for session but no sessionApproval on descriptor", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(true, DecisionApprovedForSession, gfHuman)})
		// No sessionApproval on descriptor
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, len(d.recorder.recorded), 0, "recorded")
	})

	t.Run("agent-facing denial rendering", func(t *testing.T) {
		reason := func(t *testing.T, o GateOutcome) string {
			t.Helper()
			if o.Action != "block" {
				t.Fatalf("got %v, want block", o)
			}
			return o.Reason
		}
		tw(t, f, "renders the deny reason from the descriptor's payload", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
			out := mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
				g.Payload = makePromptPayload(func(p *PromptPayload) { p.Request.MatchedPattern = sp("*") })
			}), sp("test-agent"))
			eq(t, reason(t, out), ExtensionTag+" Denied by policy: 'read' (rule '*').", "reason")
		})
		tw(t, f, "carries an operator's deny-with-reason text on a non-tool surface", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("deny", "/etc/*"), func(c *PermissionCheckResult) {
				c.ToolName, c.Reason = "path", sp("system files are off limits")
			}))})
			out := mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
				g.Surface = "path"
				g.Payload = makePromptPayload(func(p *PromptPayload) {
					p.Kind = "path"
					p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "path", sp("read"), "/etc/passwd", sp("/etc/*")
				})
			}), nil)
			eq(t, reason(t, out), ExtensionTag+" Denied by policy: 'path' for tool 'read' for path '/etc/passwd' (rule '/etc/*'). Reason: system files are off limits.", "reason")
		})
		tw(t, f, "renders the unavailable reason with the extension tag", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, gfAbsent, func(p *PermissionPromptDecision) { p.ConfirmationUnavailable = true })})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			if !strings.Contains(got, ExtensionTag) || !strings.Contains(got, "no interactive UI") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "carries an unavailable decision's denial reason into the block message", func(t *testing.T) {
			msg := "Session 'parent-1' is not serving forwarded requests"
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, DecisionSource{Kind: "unavailable", Reason: &msg}, func(p *PermissionPromptDecision) {
				p.ConfirmationUnavailable, p.DenialReason = true, &msg
			})})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			if !strings.Contains(got, "Reason: Session 'parent-1' is not serving forwarded requests.") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "names the authorizer link that refused, not the user", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDeniedWithReason, gfAuthorizer, func(p *PermissionPromptDecision) { p.DenialReason = sp("reads outside the project") })})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			if !strings.Contains(got, "The 'model-judge' authorizer denied") || strings.Contains(got, "The user denied") || !strings.Contains(got, "Reason: reads outside the project.") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "names the serving session's rule that refused, not the user", func(t *testing.T) {
			rule := DecisionSource{Kind: "rule", Surface: "bash", Pattern: sp("git push --force*"), Origin: sp("project")}
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDeniedWithReason, DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &rule},
				func(p *PermissionPromptDecision) { p.DenialReason = sp("force pushes are blocked") })})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			// The deciding rule replaces the ask's own, so the child's pattern must be absent.
			if !strings.Contains(got, "A policy rule in the session serving this request denied") || strings.Contains(got, "The user denied") ||
				!strings.Contains(got, "rule 'git push --force*'") || strings.Contains(got, "rule '*'") || !strings.Contains(got, "Reason: force pushes are blocked.") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "reports a serving session's failed escalation as an authority failure", func(t *testing.T) {
			inner := DecisionSource{Kind: "gate_error", Reason: sp("boom")}
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, DecisionSource{Kind: "forwarded", ResponderSessionID: sp("parent-1"), Decision: &inner})})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			if !strings.Contains(got, "The permission authority in the session serving this request failed to answer") || strings.Contains(got, "The user denied") || !strings.Contains(got, "Reason: boom.") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "renders the user's denial reason with the extension tag", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, escalate: gfDecision(false, DecisionDenied, gfHuman, func(p *PermissionPromptDecision) { p.DenialReason = sp("too risky") })})
			got := reason(t, mustRun(t, r, makeDescriptor(), nil))
			if !strings.Contains(got, ExtensionTag) || !strings.Contains(got, "too risky") {
				t.Errorf("reason %q", got)
			}
		})
		tw(t, f, "never echoes the command into a bash denial", func(t *testing.T) {
			r, _ := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("deny", "rm *"), func(c *PermissionCheckResult) { c.ToolName = "bash" }))})
			command := "cat <<'EOF'\n" + strings.Repeat("x", 5000) + "\nEOF"
			out := mustRun(t, r, makeDescriptor(func(g *GateDescriptor) {
				g.Surface = "bash"
				g.Payload = makePromptPayload(func(p *PromptPayload) {
					p.Kind = "bash"
					p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "bash", sp("bash"), command, sp("rm *")
				})
			}), nil)
			eq(t, reason(t, out), ExtensionTag+" Denied by policy: 'bash' (rule 'rm *').", "reason")
		})
	})
}

func TestGateRunnerDispatchTwins(t *testing.T) {
	const f = "handlers/gates/runner"
	infra := DecisionSource{Kind: "infrastructure_read"}

	tw(t, f, "returns allow for a null gate", func(t *testing.T) {
		r, d := makeGateRunner()
		eq(t, mustRun(t, r, nil, nil), gfAllow, "outcome")
		eq(t, len(d.reporter.logs)+len(d.reporter.decisions), 0, "writes")
	})
	tw(t, f, "returns allow for a bypass with no log or decision", func(t *testing.T) {
		r, d := makeGateRunner()
		eq(t, mustRun(t, r, &GateBypass{DecidedBy: infra}, nil), gfAllow, "outcome")
		eq(t, len(d.reporter.logs)+len(d.reporter.decisions), 0, "writes")
	})
	tw(t, f, "fires writeReviewLog for a bypass with a log entry", func(t *testing.T) {
		r, d := makeGateRunner()
		mustRun(t, r, &GateBypass{DecidedBy: infra, Log: &GateBypassLog{Event: "infra.bypass", Details: obj("path", "/x")}}, nil)
		eq(t, len(d.reporter.logs), 1, "logs")
		got := d.reporter.logs[0]
		eq(t, got.event, "infra.bypass", "event")
		eq(t, got.details.order(), []string{"path", "requestId", "decidedBy"}, "keys")
		hasFields(t, got.details, "path", "/x", "decidedBy", obj("kind", "infrastructure_read"))
		if id, _ := got.details.str("requestId"); !permIDRe.MatchString(id) {
			t.Errorf("requestId %q", id)
		}
		eq(t, len(d.reporter.decisions), 0, "decisions")
	})
	tw(t, f, "fires emitDecision for a bypass with a decision", func(t *testing.T) {
		r, d := makeGateRunner()
		decision := DecisionEventFacts{Surface: "path", Value: "/x", Result: "allow", Resolution: "policy_allow"}
		mustRun(t, r, &GateBypass{DecidedBy: infra, Decision: &decision}, nil)
		eq(t, len(d.reporter.decisions), 1, "decisions")
		got := d.reporter.decisions[0]
		if !permIDRe.MatchString(got.RequestID) {
			t.Errorf("requestId %q", got.RequestID)
		}
		got.RequestID = ""
		eq(t, got, PermissionDecisionEvent{Surface: "path", Value: "/x", Result: "allow", Resolution: "policy_allow"}, "event")
		eq(t, len(d.reporter.logs), 0, "logs")
	})
	tw(t, f, "routes a descriptor to the gate check logic and returns allow", func(t *testing.T) {
		r, _ := makeGateRunner()
		eq(t, mustRun(t, r, makeDescriptor(), nil), gfAllow, "outcome")
	})
	tw(t, f, "routes a descriptor to the gate check logic and returns block", func(t *testing.T) {
		r, _ := makeGateRunner(gfRunnerOpts{resolveResult: gfCheck(makeCheckResult(ckState("deny", "*")))})
		if out := mustRun(t, r, makeDescriptor(), nil); !gfIsBlock(out) {
			t.Fatalf("got %v, want block", out)
		}
	})
}

func TestGateRunnerRequestIdentityTwins(t *testing.T) {
	const f = "handlers/gates/runner"
	infra := DecisionSource{Kind: "infrastructure_read"}
	requestID := func(t *testing.T, o *jsObject) string {
		t.Helper()
		id, _ := o.str("requestId")
		if !permIDRe.MatchString(id) {
			t.Fatalf("requestId %q", id)
		}
		return id
	}
	bypassWithLog := func() *GateBypass {
		return &GateBypass{DecidedBy: infra, Log: &GateBypassLog{Event: "permission_request.infrastructure_auto_allowed", Details: obj("path", "/x")}}
	}
	sessionCheck := gfCheck(makeCheckResult(func(c *PermissionCheckResult) { c.Source = "session"; c.MatchedPattern = sp("git *") }))
	askCheck := gfCheck(makeCheckResult(ckState("ask", "*")))
	denyCheck := gfCheck(makeCheckResult(ckState("deny", "*")))

	tw(t, f, "carries the minted id on the session-approved review entry", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: sessionCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.logEvents(), []string{"permission_request.session_approved"}, "events")
		requestID(t, d.reporter.logs[0].details)
	})
	tw(t, f, "carries the minted id on the auto-approved review entry", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, yolo: true})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.logEvents(), []string{"permission_request.auto_approved"}, "events")
		requestID(t, d.reporter.logs[0].details)
	})
	tw(t, f, "carries the minted id on the policy-denied review entry", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.logEvents(), []string{"permission_request.blocked"}, "events")
		requestID(t, d.reporter.logs[0].details)
	})
	tw(t, f, "carries the minted id on a bypass review entry", func(t *testing.T) {
		r, d := makeGateRunner()
		mustRun(t, r, bypassWithLog(), nil)
		eq(t, len(d.reporter.logs), 1, "logs")
		requestID(t, d.reporter.logs[0].details)
	})
	tw(t, f, "stamps the bypass's own decider onto its review entry", func(t *testing.T) {
		r, d := makeGateRunner()
		mustRun(t, r, bypassWithLog(), nil)
		// The gate that short-circuits is the decider; the runner relays what it states rather than inferring one from the event name.
		hasFields(t, d.reporter.logs[0].details, "decidedBy", obj("kind", "infrastructure_read"))
	})
	tw(t, f, "keeps the tool call id alongside the minted id on the review entry", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.logs[0].details.vals["toolCallId"], any("tc-1"), "toolCallId")
		if requestID(t, d.reporter.logs[0].details) == "tc-1" {
			t.Error("requestId is the tool call id")
		}
	})
	tw(t, f, "mints a distinct id for each run, so one tool call's gates stay separable", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
		mustRun(t, r, makeDescriptor(), nil)
		mustRun(t, r, makeDescriptor(), nil)
		if requestID(t, d.reporter.logs[0].details) == requestID(t, d.reporter.logs[1].details) {
			t.Error("two runs share one id")
		}
	})
	tw(t, f, "stamps the session-approved entry and its decision event with one id", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: sessionCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.decisions[0].RequestID, requestID(t, d.reporter.logs[0].details), "id")
	})
	tw(t, f, "stamps the auto-approved entry and its decision event with one id", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: askCheck, yolo: true})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.decisions[0].RequestID, requestID(t, d.reporter.logs[0].details), "id")
	})
	tw(t, f, "stamps the policy-denied entry and its decision event with one id", func(t *testing.T) {
		r, d := makeGateRunner(gfRunnerOpts{resolveResult: denyCheck})
		mustRun(t, r, makeDescriptor(), nil)
		eq(t, d.reporter.decisions[0].RequestID, requestID(t, d.reporter.logs[0].details), "id")
	})
	tw(t, f, "stamps a bypass's log entry and decision event with one id", func(t *testing.T) {
		r, d := makeGateRunner()
		b := bypassWithLog()
		b.Decision = &DecisionEventFacts{Surface: "read", Value: "/x", Result: "allow", Resolution: "infrastructure_auto_allowed"}
		mustRun(t, r, b, nil)
		if !permIDRe.MatchString(d.reporter.decisions[0].RequestID) {
			t.Errorf("id %q", d.reporter.decisions[0].RequestID)
		}
		eq(t, d.reporter.decisions[0].RequestID, requestID(t, d.reporter.logs[0].details), "id")
	})
	tw(t, f, "stamps an allow decision event even when nothing is written to the log", func(t *testing.T) {
		r, d := makeGateRunner()
		mustRun(t, r, makeDescriptor(), nil)
		if !permIDRe.MatchString(d.reporter.decisions[0].RequestID) {
			t.Errorf("id %q", d.reporter.decisions[0].RequestID)
		}
	})
}
