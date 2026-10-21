package pi_permission_system

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// Twins of test/authority/authorizer.test.ts (selectAuthorizer) and authorizer-selection.test.ts (AuthorizerSelection).

// detectorFake is a SubagentDetector answering a fixed verdict.
type detectorFake struct {
	isSubagent bool
	err        error
}

func (d detectorFake) IsSubagent(SubagentDetectionContext) (bool, error) { return d.isSubagent, d.err }

// servingFake answers a fixed verdict for any target: answer nil is "no channel can say" (the original's null).
type servingFake struct {
	mu     sync.Mutex
	answer *bool
	asked  []PermissionForwardingTarget
}

func serving(answer bool) *servingFake { return &servingFake{answer: &answer} }

func (s *servingFake) IsServing(target PermissionForwardingTarget) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, target)
	if s.answer == nil {
		return false, false
	}
	return *s.answer, true
}

func (s *servingFake) Describe(PermissionForwardingTarget) ServingObservation {
	return ServingObservation{Channel: "none"}
}

func (s *servingFake) setAnswer(v bool) { s.mu.Lock(); s.answer = &v; s.mu.Unlock() }

// prompterFake records the call and resolves to a default approval; invoking runs the authorizer, as the real prompter does.
type prompterFake struct {
	mu        sync.Mutex
	invoking  bool
	decision  PermissionPromptDecision
	calls     []prompterCall
	lastError error
}

type prompterCall struct {
	authorizer TerminalAuthorizer
	details    *PromptPermissionDetails
}

func newPrompter() *prompterFake {
	return &prompterFake{decision: PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}}
}

func (p *prompterFake) Prompt(a TerminalAuthorizer, d *PromptPermissionDetails) (PermissionPromptDecision, error) {
	p.mu.Lock()
	p.calls = append(p.calls, prompterCall{a, d})
	invoking, decision := p.invoking, p.decision
	p.mu.Unlock()
	if invoking {
		return a.Authorize(d)
	}
	return decision, nil
}

func (p *prompterFake) callList() []prompterCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]prompterCall(nil), p.calls...)
}

// auditFake is a recording UnregisteredLinkAuditor.
type auditFake struct {
	mu    sync.Mutex
	links []UnregisteredLink
}

func (a *auditFake) AuditUnregisteredLink(l UnregisteredLink) {
	a.mu.Lock()
	a.links = append(a.links, l)
	a.mu.Unlock()
}

// selEnv is the AuthorizerSelection constructor bag with the parts a test inspects (makeAuthorizerSelectionDeps).
type selEnv struct {
	deps     AuthorizerSelectionConstructorDeps
	prompter *prompterFake
	logger   *fwLogger
	audit    *auditFake
	registry *AuthorizerRegistry
	chain    []string
}

func newSelEnv(t *testing.T, mut ...func(*selEnv)) *selEnv {
	t.Helper()
	clearHintEnv(t)
	e := &selEnv{prompter: newPrompter(), logger: &fwLogger{}, audit: &auditFake{}, registry: &AuthorizerRegistry{}}
	e.deps = AuthorizerSelectionConstructorDeps{
		AuthorizerSelectionDeps: AuthorizerSelectionDeps{
			Detection: detectorFake{},
			Events:    &busFake{},
			Dialogs:   NewAskDialogQueue(),
			GetPromptPreferences: func() PromptPreferences {
				return PromptPreferences{DoublePressToConfirm: true, Budget: DefaultRenderBudget, DialogKeys: defaultDialogKeys()}
			},
			RequestPermissionDecision: func(PermissionPromptView, string, PromptPayload, *RequestPermissionOptions) (PermissionPromptDecision, error) {
				return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}, nil
			},
			ForwardingDir: "/tmp/forwarding",
			Serving: NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{
				Registry:   NewServingSessionRegistry(),
				Heartbeats: absentHeartbeats{},
			}),
			GetForwardingTimeoutMs: func() float64 { return 1000 },
		},
		GetPermissionQuery: func() PermissionQuery { return &queryFake{} },
		GetAuthorizerChain: func() []string { return nil },
	}
	for _, m := range mut {
		m(e)
	}
	e.deps.Logger = e.logger
	e.deps.Prompter = e.prompter
	e.deps.AuthorizerRegistry = e.registry
	e.deps.ChainAudit = e.audit
	if e.chain != nil {
		chain := e.chain
		e.deps.GetAuthorizerChain = func() []string { return chain }
	}
	return e
}

type absentHeartbeats struct{}

func (absentHeartbeats) Read(string) HeartbeatState { return HeartbeatAbsent }
func (absentHeartbeats) ServingIDs() []string       { return nil }

func (e *selEnv) selection() *AuthorizerSelection { return NewAuthorizerSelection(e.deps) }

func withDetector(isSubagent bool) func(*selEnv) {
	return func(e *selEnv) { e.deps.Detection = detectorFake{isSubagent: isSubagent} }
}

func withServing(s TargetServingLookup) func(*selEnv) { return func(e *selEnv) { e.deps.Serving = s } }

func withChain(names ...string) func(*selEnv) { return func(e *selEnv) { e.chain = names } }

// selCtx is makeCtx: a context with an unnamed session whose id reads null.
func selCtx(hasUI bool, mut ...func(*AuthorityContext)) AuthorityContext {
	ctx := AuthorityContext{
		ForwarderContext: ForwarderContext{
			HasUI: hasUI, UI: fwUI{}, Cwd: "/test/project",
			SessionManager: &fwSessionManager{sessionID: fwID(""), sessionDir: fwID("/sessions/test")},
		},
		Mode:        "tui",
		PromptUI:    &promptUIFake{},
		SessionName: func() *string { return nil },
	}
	for _, m := range mut {
		m(&ctx)
	}
	return ctx
}

func mustActivate(t *testing.T, s *AuthorizerSelection, ctx AuthorityContext) {
	t.Helper()
	if err := s.Activate(ctx); err != nil {
		t.Fatal(err)
	}
}

func selectOf(t *testing.T, ctx AuthorityContext, deps AuthorizerSelectionDeps) SelectedAuthority {
	t.Helper()
	a, err := SelectAuthorizer(ctx, deps)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func registerFixedLink(t *testing.T, r *AuthorizerRegistry, name string, verdict AuthorizerVerdict) {
	t.Helper()
	if _, err := r.Register(name, func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
		return verdict, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// detailsOn is makeDetailsOn: details whose gate-computed surface drives the delegation envelope.
func detailsOn(surface string) *PromptPermissionDetails {
	return authDetails(func(d *PromptPermissionDetails) {
		d.AccessIntent = &ForwardedAccessFacts{Surface: surface, MatchValues: []string{"/v"}}
	})
}

func TestSelectAuthorizerTwins(t *testing.T) {
	f := "authority/authorizer"
	deps := func(t *testing.T, mut ...func(*selEnv)) AuthorizerSelectionDeps {
		return newSelEnv(t, mut...).deps.AuthorizerSelectionDeps
	}

	tw(t, f, "selects LocalUserAuthorizer when the context has UI", func(t *testing.T) {
		if _, ok := selectOf(t, selCtx(true), deps(t)).Terminal.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
	})
	tw(t, f, "selects LocalUserAuthorizer for a subagent with UI that declares no parent", func(t *testing.T) {
		if _, ok := selectOf(t, selCtx(true), deps(t, withDetector(true))).Terminal.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
	})
	tw(t, f, "selects ParentAuthorizer when there is no UI but the context is a subagent", func(t *testing.T) {
		if _, ok := selectOf(t, selCtx(false), deps(t, withDetector(true))).Terminal.(*ParentAuthorizer); !ok {
			t.Error("not a ParentAuthorizer")
		}
	})
	tw(t, f, "selects DenyingAuthorizer when there is no UI and no subagent", func(t *testing.T) {
		if _, ok := selectOf(t, selCtx(false), deps(t, withDetector(false))).Terminal.(DenyingAuthorizer); !ok {
			t.Error("not a DenyingAuthorizer")
		}
	})
	tw(t, f, "adjudicates locally when the terminal is the human", func(t *testing.T) {
		eq(t, selectOf(t, selCtx(true), deps(t)).AdjudicatesLocally, true, "role")
	})
	tw(t, f, "adjudicates locally when a subagent with its own UI declares no parent", func(t *testing.T) {
		eq(t, selectOf(t, selCtx(true), deps(t, withDetector(true))).AdjudicatesLocally, true, "role")
	})
	tw(t, f, "relays instead of adjudicating when the terminal forwards to a serving node", func(t *testing.T) {
		eq(t, selectOf(t, selCtx(false), deps(t, withDetector(true))).AdjudicatesLocally, false, "role")
	})
	tw(t, f, "adjudicates locally when the terminal denies for want of authority", func(t *testing.T) {
		eq(t, selectOf(t, selCtx(false), deps(t, withDetector(false))).AdjudicatesLocally, true, "role")
	})

	tw(t, f, "relays to the session an env marker names while that session serves", func(t *testing.T) {
		d := deps(t, withDetector(true), withServing(serving(true)))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		authority := selectOf(t, selCtx(true), d)
		if _, ok := authority.Terminal.(*ParentAuthorizer); !ok {
			t.Error("not a ParentAuthorizer")
		}
		eq(t, authority.AdjudicatesLocally, false, "role")
		eq(t, authority.RelayTarget, &PermissionForwardingTarget{SessionID: "lead-session", Source: "env"}, "relay target")
	})
	tw(t, f, "relays to a registry-resolved parent while that parent serves", func(t *testing.T) {
		d := deps(t, withDetector(true), withServing(serving(true)), func(e *selEnv) {
			e.deps.Registry = fwMakeSubagentRegistry("unknown", &SubagentSessionInfo{ParentSessionID: sp("lead-session")})
		})
		authority := selectOf(t, selCtx(true), d)
		if _, ok := authority.Terminal.(*ParentAuthorizer); !ok {
			t.Error("not a ParentAuthorizer")
		}
		eq(t, authority.RelayTarget, &PermissionForwardingTarget{SessionID: "lead-session", Source: "registry"}, "relay target")
	})
	tw(t, f, "asks the serving channel about the target it resolved", func(t *testing.T) {
		s := serving(true)
		d := deps(t, withDetector(true), withServing(s))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		selectOf(t, selCtx(true), d)
		eq(t, s.asked, []PermissionForwardingTarget{{SessionID: "lead-session", Source: "env"}}, "asked")
	})
	tw(t, f, "keeps the local dialog when the declared parent is not serving", func(t *testing.T) {
		d := deps(t, withDetector(true), withServing(serving(false)))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		authority := selectOf(t, selCtx(true), d)
		if _, ok := authority.Terminal.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
		eq(t, authority.AdjudicatesLocally, true, "role")
		if authority.RelayTarget != nil {
			t.Error("relay target set")
		}
	})
	tw(t, f, "keeps the local dialog when no channel can answer for the parent", func(t *testing.T) {
		d := deps(t, withDetector(true), withServing(&servingFake{}))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		if _, ok := selectOf(t, selCtx(true), d).Terminal.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
	})
	tw(t, f, "keeps the local dialog when the marker names the reading session", func(t *testing.T) {
		s := serving(true)
		d := deps(t, withDetector(true), withServing(s))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "unknown")
		if _, ok := selectOf(t, selCtx(true), d).Terminal.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
		eq(t, len(s.asked), 0, "asked")
	})
	tw(t, f, "relays without consulting the serving channel", func(t *testing.T) {
		s := serving(false)
		d := deps(t, withDetector(true), withServing(s))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		if _, ok := selectOf(t, selCtx(false), d).Terminal.(*ParentAuthorizer); !ok {
			t.Error("not a ParentAuthorizer")
		}
		eq(t, len(s.asked), 0, "asked")
	})

	// Not in the original (a throw there): a failing detection is an error, never a quiet local decision.
	t.Run("a failing subagent detection is an error", func(t *testing.T) {
		d := deps(t)
		d.Detection = detectorFake{err: errors.New("session dir unreadable")}
		for _, hasUI := range []bool{true, false} {
			if _, err := SelectAuthorizer(selCtx(hasUI), d); err == nil {
				t.Errorf("hasUI %v: no error", hasUI)
			}
		}
	})
}

func TestAuthorizerSelectionTwins(t *testing.T) {
	f := "authority/authorizer-selection"
	notActivated := "escalate called before the session was activated"
	approvedByHuman := PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}
	linkDenied := func(name, reason string) PermissionPromptDecision {
		return PermissionPromptDecision{State: DecisionDeniedWithReason, DenialReason: sp(reason),
			DecidedBy: DecisionSource{Kind: "authorizer", Name: name, Verdict: "deny", Reason: sp(reason)}}
	}
	expectNotActivated := func(t *testing.T, s *AuthorizerSelection) {
		t.Helper()
		if _, err := s.Escalate(authDetails()); err == nil || err.Error() != notActivated {
			t.Errorf("err %v", err)
		}
	}
	invoking := func(e *selEnv) { e.prompter.invoking = true }

	tw(t, f, "rejects before activate", func(t *testing.T) { expectNotActivated(t, newSelEnv(t).selection()) })
	tw(t, f, "delegates to deps.prompter.prompt with the selected authorizer", func(t *testing.T) {
		e := newSelEnv(t)
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		details := authDetails()
		result, err := s.Escalate(details)
		eq(t, err, nil, "err")
		calls := e.prompter.callList()
		if len(calls) != 1 || calls[0].details != details {
			t.Fatalf("calls %+v", calls)
		}
		if _, ok := calls[0].authorizer.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
		eq(t, result, approvedByHuman, "result")
	})
	tw(t, f, "uses the most recently selected authorizer", func(t *testing.T) {
		e := newSelEnv(t)
		s := e.selection()
		mustActivate(t, s, selCtx(false))
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(authDetails()))
		if _, ok := e.prompter.callList()[0].authorizer.(*LocalUserAuthorizer); !ok {
			t.Error("not a LocalUserAuthorizer")
		}
	})
	tw(t, f, "rejects after deactivate", func(t *testing.T) {
		s := newSelEnv(t).selection()
		mustActivate(t, s, selCtx(true))
		s.Deactivate()
		expectNotActivated(t, s)
	})
	tw(t, f, "returns the prompter decision", func(t *testing.T) {
		e := newSelEnv(t)
		decision := PermissionPromptDecision{State: DecisionDenied, DecidedBy: decidedByHuman, DenialReason: sp("user declined")}
		e.prompter.decision = decision
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		eq(t, mustDecision(t)(s.Escalate(authDetails())), decision, "decision")
	})
	tw(t, f, "activate then deactivate rejects a subsequent escalate", func(t *testing.T) {
		s := newSelEnv(t).selection()
		mustActivate(t, s, selCtx(true))
		s.Deactivate()
		expectNotActivated(t, s)
	})
	tw(t, f, "multiple activate calls escalate against the most recent context", func(t *testing.T) {
		e := newSelEnv(t)
		s := e.selection()
		mustActivate(t, s, selCtx(true, func(c *AuthorityContext) { c.Cwd = "/old" }))
		mustActivate(t, s, selCtx(true, func(c *AuthorityContext) { c.Cwd = "/new" }))
		mustDecision(t)(s.Escalate(authDetails()))
		eq(t, len(e.prompter.callList()), 1, "prompt calls")
	})

	tw(t, f, "consults a configured link before the terminal", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "deny", Reason: sp("typo path")})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		// The link decided; the LocalUserAuthorizer terminal was never reached (it would have approved by default).
		eq(t, mustDecision(t)(s.Escalate(detailsOn("bash"))), linkDenied("judge", "typo path"), "decision")
	})
	tw(t, f, "injects the session review-log seam into each link (ADR 0007 §3)", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("judge"))
		link := newLink(AuthorizerVerdict{Kind: "defer"})
		if _, err := e.registry.Register("judge", link.authorize); err != nil {
			t.Fatal(err)
		}
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if len(link.calls) != 1 || link.calls[0].log != AuthorizerLog(e.logger) {
			t.Errorf("link calls %+v", link.calls)
		}
	})
	tw(t, f, "resolves links in config order (first non-defer wins)", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("a", "b"))
		registerFixedLink(t, e.registry, "a", AuthorizerVerdict{Kind: "deny", Reason: sp("a-wins")})
		registerFixedLink(t, e.registry, "b", AuthorizerVerdict{Kind: "deny", Reason: sp("b-wins")})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		eq(t, mustDecision(t)(s.Escalate(detailsOn("bash"))), linkDenied("a", "a-wins"), "decision")
	})
	tw(t, f, "skips an unregistered configured name with a warning", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("missing", "present"))
		registerFixedLink(t, e.registry, "present", AuthorizerVerdict{Kind: "deny", Reason: sp("present-decided")})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		// The unregistered "missing" link is skipped fail-safe; "present" decides, and is the name credited.
		eq(t, mustDecision(t)(s.Escalate(detailsOn("bash"))), linkDenied("present", "present-decided"), "decision")
		eq(t, e.audit.links, []UnregisteredLink{{RequestID: "req-1", Name: "missing"}}, "audited")
	})
	tw(t, f, "hands the audit every skip, so its record stays per ask", func(t *testing.T) {
		e := newSelEnv(t, withChain("missing"))
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(authDetails(func(d *PromptPermissionDetails) { d.RequestID = "req-a" })))
		mustDecision(t)(s.Escalate(authDetails(func(d *PromptPermissionDetails) { d.RequestID = "req-b" })))
		eq(t, e.audit.links, []UnregisteredLink{{RequestID: "req-a", Name: "missing"}, {RequestID: "req-b", Name: "missing"}}, "audited")
	})
	tw(t, f, "records the resolved link names on the ask", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "defer"})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if !e.logger.reviewMatching("authorizer_chain_resolved", fwObj("requestId", "req-1", "links", []string{"judge"})) {
			t.Errorf("not recorded: %v", reviewEvents(e.logger))
		}
	})
	tw(t, f, "records only the names it could resolve", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("missing", "present"))
		registerFixedLink(t, e.registry, "present", AuthorizerVerdict{Kind: "defer"})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if !e.logger.reviewMatching("authorizer_chain_resolved", fwObj("requestId", "req-1", "links", []string{"present"})) {
			t.Errorf("not recorded: %v", reviewEvents(e.logger))
		}
	})
	tw(t, f, "records no consultation when no configured name resolved", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("missing"))
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if e.logger.reviewMatching("authorizer_chain_resolved", nil) {
			t.Error("a consultation was recorded")
		}
	})
	tw(t, f, "caps a link's allow on an excluded surface, falling through to the terminal", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "allow"})
		s := e.selection()
		// No UI, not a subagent: the terminal is DenyingAuthorizer.
		mustActivate(t, s, selCtx(false))
		decision := mustDecision(t)(s.Escalate(detailsOn("external_directory")))
		// The envelope downgraded the link's allow to defer, so the terminal (denying) owns the decision.
		eq(t, decision.Approved, false, "approved")
	})
	tw(t, f, "lets a link's allow through on a non-excluded surface", func(t *testing.T) {
		e := newSelEnv(t, invoking, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "allow"})
		s := e.selection()
		mustActivate(t, s, selCtx(false))
		eq(t, mustDecision(t)(s.Escalate(detailsOn("bash"))), PermissionPromptDecision{Approved: true, State: DecisionApproved,
			DecidedBy: DecisionSource{Kind: "authorizer", Name: "judge", Verdict: "allow"}}, "decision")
	})
	tw(t, f, "a registered but un-named link grants no authority (terminal identity)", func(t *testing.T) {
		e := newSelEnv(t, withChain())
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "allow"})
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		mustDecision(t)(s.Escalate(authDetails()))
		// An empty chain: the selected value is the terminal instance itself.
		if _, ok := e.prompter.callList()[0].authorizer.(*LocalUserAuthorizer); !ok {
			t.Error("not the terminal")
		}
	})

	tw(t, f, "satisfies the AdjudicationRole seam", func(t *testing.T) {
		var role AdjudicationRole = newSelEnv(t).selection()
		if role == nil {
			t.Error("nil")
		}
	})
	roleOf := func(t *testing.T, hasUI bool, mut ...func(*selEnv)) bool {
		s := newSelEnv(t, mut...).selection()
		mustActivate(t, s, selCtx(hasUI))
		return s.AdjudicatesLocally()
	}
	tw(t, f, "reports true for a node with its own UI", func(t *testing.T) { eq(t, roleOf(t, true), true, "role") })
	tw(t, f, "reports false for a relaying subagent node", func(t *testing.T) { eq(t, roleOf(t, false, withDetector(true)), false, "role") })
	tw(t, f, "reports true for a subagent node that has its own UI and no parent", func(t *testing.T) {
		// A node with a UI relays only while a declared parent is serving, so the role cannot be re-derived from subagent detection alone.
		eq(t, roleOf(t, true, withDetector(true)), true, "role")
	})
	tw(t, f, "reports true for a headless non-subagent node", func(t *testing.T) { eq(t, roleOf(t, false), true, "role") })
	tw(t, f, "reports true before activation and after deactivation", func(t *testing.T) {
		s := newSelEnv(t, withDetector(true)).selection()
		eq(t, s.AdjudicatesLocally(), true, "before")
		mustActivate(t, s, selCtx(false))
		s.Deactivate()
		eq(t, s.AdjudicatesLocally(), true, "after")
	})

	relaying := func(t *testing.T, mut ...func(*selEnv)) (*selEnv, *AuthorizerSelection) {
		e := newSelEnv(t, append([]func(*selEnv){withDetector(true)}, mut...)...)
		s := e.selection()
		mustActivate(t, s, selCtx(false))
		return e, s
	}
	tw(t, f, "composes no links, so the ask reaches the relaying terminal unchanged", func(t *testing.T) {
		e, s := relaying(t, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "deny", Reason: sp("judged locally")})
		details := detailsOn("bash")
		mustDecision(t)(s.Escalate(details))
		// Zero links: the composed chain is the terminal instance, so the registered link never ran.
		calls := e.prompter.callList()
		if _, ok := calls[0].authorizer.(*ParentAuthorizer); !ok || calls[0].details != details {
			t.Errorf("calls %+v", calls)
		}
	})
	tw(t, f, "records the delegated chain instead of the resolved one", func(t *testing.T) {
		e, s := relaying(t, withChain("judge"))
		registerFixedLink(t, e.registry, "judge", AuthorizerVerdict{Kind: "deny", Reason: sp("judged locally")})
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if !e.logger.reviewMatching("authorizer_chain_delegated", fwObj("requestId", "req-1", "links", []string{"judge"})) {
			t.Errorf("not recorded: %v", reviewEvents(e.logger))
		}
		if e.logger.reviewMatching("authorizer_chain_resolved", nil) {
			t.Error("a resolution was recorded")
		}
	})
	tw(t, f, "does not report an unregistrable link as an unregistered one", func(t *testing.T) {
		e, s := relaying(t, withChain("model-judge"))
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		// A child cannot host a link at all (#699), so its absence is the design, not the misconfiguration.
		eq(t, len(e.audit.links), 0, "audited")
	})
	tw(t, f, "records nothing when no chain is configured", func(t *testing.T) {
		e, s := relaying(t)
		mustDecision(t)(s.Escalate(detailsOn("bash")))
		if e.logger.reviewMatching("authorizer_chain_delegated", nil) {
			t.Error("a delegation was recorded")
		}
	})

	uiSelection := func(t *testing.T, servingNow bool) (*selEnv, *AuthorizerSelection, *servingFake) {
		s := serving(servingNow)
		e := newSelEnv(t, withDetector(true), withServing(s))
		return e, e.selection(), s
	}
	tw(t, f, "records the target and its channel when relaying starts", func(t *testing.T) {
		e, s, _ := uiSelection(t, true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		mustActivate(t, s, selCtx(true))
		if !e.logger.reviewMatching("forwarded_permission.relay_started", fwObj("targetSessionId", "lead-session", "channel", "env")) {
			t.Errorf("not recorded: %v", reviewEvents(e.logger))
		}
	})
	tw(t, f, "records nothing further while the same target keeps serving", func(t *testing.T) {
		e, s, _ := uiSelection(t, true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		for i := 0; i < 3; i++ {
			mustActivate(t, s, selCtx(true))
		}
		eq(t, len(e.logger.reviews()), 1, "reviews")
	})
	tw(t, f, "records the stop when the declared parent stops serving", func(t *testing.T) {
		e, s, sv := uiSelection(t, true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		mustActivate(t, s, selCtx(true))
		sv.setAnswer(false)
		mustActivate(t, s, selCtx(true))
		if !e.logger.reviewMatching("forwarded_permission.relay_stopped", fwObj("targetSessionId", "lead-session")) {
			t.Errorf("not recorded: %v", reviewEvents(e.logger))
		}
	})
	tw(t, f, "records a stop and a start when the target changes", func(t *testing.T) {
		e, s, _ := uiSelection(t, true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		mustActivate(t, s, selCtx(true))
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "other-lead")
		mustActivate(t, s, selCtx(true))
		reviews := e.logger.reviews()
		eq(t, len(reviews), 3, "reviews")
		eq(t, reviews[1].event, "forwarded_permission.relay_stopped", "second")
		eq(t, marshalJSON(reviews[1].details, ""), `{"targetSessionId":"lead-session"}`, "second details")
		eq(t, reviews[2].event, "forwarded_permission.relay_started", "third")
		eq(t, marshalJSON(reviews[2].details, ""), `{"targetSessionId":"other-lead","channel":"env"}`, "third details")
	})
	tw(t, f, "records the stop when a relaying node is deactivated", func(t *testing.T) {
		e, s, _ := uiSelection(t, true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "lead-session")
		mustActivate(t, s, selCtx(true))
		s.Deactivate()
		reviews := e.logger.reviews()
		eq(t, len(reviews), 2, "reviews")
		eq(t, reviews[1].event, "forwarded_permission.relay_stopped", "second")
		eq(t, marshalJSON(reviews[1].details, ""), `{"targetSessionId":"lead-session"}`, "details")
	})
	tw(t, f, "records nothing for a node that never relays", func(t *testing.T) {
		e, s, _ := uiSelection(t, false)
		mustActivate(t, s, selCtx(true))
		s.Deactivate()
		eq(t, len(e.logger.reviews()), 0, "reviews")
	})

	// Not in the original: a failing detection is an error from Activate (the original throws out of activate) and stores no selection.
	t.Run("a failing activation is an error and activates nothing", func(t *testing.T) {
		e := newSelEnv(t)
		s := e.selection()
		mustActivate(t, s, selCtx(true))
		e.deps.Detection = detectorFake{err: errors.New("boom")}
		failing := NewAuthorizerSelection(e.deps)
		if err := failing.Activate(selCtx(false)); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("err %v", err)
		}
		expectNotActivated(t, failing)
	})
}
