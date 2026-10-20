package pi_permission_system

import (
	"errors"
	"strings"
	"testing"
)

// Twins of test/authority/bracketed-paste.test.ts, denying-authorizer.test.ts, authorizer-chain.test.ts,
// authorizer-chain-audit.test.ts and authorizer-registry.test.ts.

func pasteChunk(content string) string { return "\x1b[200~" + content + "\x1b[201~" }

func TestBracketedPasteTwins(t *testing.T) {
	f := "authority/bracketed-paste"
	tw(t, f, "collapses a line break into a single space", func(t *testing.T) {
		eq(t, CollapsePastedNewlines(pasteChunk("one\ntwo")), pasteChunk("one two"), "collapsed")
	})
	tw(t, f, "collapses a CRLF line break into a single space", func(t *testing.T) {
		eq(t, CollapsePastedNewlines(pasteChunk("one\r\ntwo")), pasteChunk("one two"), "collapsed")
	})
	tw(t, f, "collapses a run of blank lines into a single space", func(t *testing.T) {
		eq(t, CollapsePastedNewlines(pasteChunk("one\n\n\ntwo")), pasteChunk("one two"), "collapsed")
	})
	tw(t, f, "leaves a single-line paste byte-identical", func(t *testing.T) {
		eq(t, CollapsePastedNewlines(pasteChunk("no line breaks here")), pasteChunk("no line breaks here"), "collapsed")
	})
	tw(t, f, "leaves the paste markers in place so the editor still sees a paste", func(t *testing.T) {
		collapsed := CollapsePastedNewlines(pasteChunk("a\nb"))
		if !strings.HasPrefix(collapsed, "\x1b[200~") || !strings.HasSuffix(collapsed, "\x1b[201~") {
			t.Errorf("markers lost: %q", collapsed)
		}
	})
	tw(t, f, "returns ordinary keystroke data unchanged", func(t *testing.T) {
		eq(t, CollapsePastedNewlines("\r"), "\r", "enter")
		eq(t, CollapsePastedNewlines("a"), "a", "letter")
	})
	tw(t, f, "leaves a chunk missing its end marker unchanged", func(t *testing.T) {
		eq(t, CollapsePastedNewlines("\x1b[200~one\ntwo"), "\x1b[200~one\ntwo", "chunk")
	})
	tw(t, f, "collapses only inside the markers, not text typed after the paste", func(t *testing.T) {
		eq(t, CollapsePastedNewlines(pasteChunk("a\nb")+"c\nd"), pasteChunk("a b")+"c\nd", "chunk")
	})
}

func TestDenyingAuthorizerTwins(t *testing.T) {
	f := "authority/denying-authorizer"
	want := PermissionPromptDecision{State: DecisionDenied, ConfirmationUnavailable: true, DecidedBy: decidedByAbsentAuthority}
	var authorizer TerminalAuthorizer = DenyingAuthorizer{}
	tw(t, f, "denies with the confirmation-unavailable marker, regardless of details", func(t *testing.T) {
		d, err := authorizer.Authorize(authDetails(func(d *PromptPermissionDetails) { d.AgentName = sp("test-agent") }))
		eq(t, err, nil, "error")
		eq(t, d, want, "decision")
	})
	tw(t, f, "denies the same way for a skill-sourced request", func(t *testing.T) {
		d, err := authorizer.Authorize(authDetails(func(d *PromptPermissionDetails) {
			d.RequestID, d.Source, d.SkillName = "req-2", "skill_input", sp("deploy-helper")
		}))
		eq(t, err, nil, "error")
		eq(t, d, want, "decision")
	})
}

func TestAuthorizerChainTwins(t *testing.T) {
	f := "authority/authorizer-chain"
	log := &fwLogger{}
	humanApproved := PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}
	humanDenied := PermissionPromptDecision{State: DecisionDenied, DecidedBy: decidedByHuman}

	tw(t, f, "returns the terminal instance itself when there are no links", func(t *testing.T) {
		terminal := newTerminal()
		composed := ComposeAuthorizerChain(nil, terminal, &queryFake{}, log)
		// Identity is a behavioural invariant: escalate hands the real terminal to the prompter.
		if composed != TerminalAuthorizer(terminal) {
			t.Error("not the terminal instance")
		}
	})
	tw(t, f, "maps an allow verdict to an approved decision and injects the query", func(t *testing.T) {
		terminal, link, query, details := newTerminal(humanDenied), newLink(AuthorizerVerdict{Kind: "allow"}, "model-judge"), &queryFake{}, authDetails()
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, terminal, query, log).Authorize(details))
		eq(t, decision, PermissionPromptDecision{Approved: true, State: DecisionApproved,
			DecidedBy: DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "allow"}}, "decision")
		// The chain injects the session-scoped query and the review-log seam into each link (ADR 0007 section 3).
		if len(link.calls) != 1 || link.calls[0].details != details || link.calls[0].query != PermissionQuery(query) || link.calls[0].log != AuthorizerLog(log) {
			t.Errorf("link call: %+v", link.calls)
		}
		eq(t, terminal.callCount(), 0, "terminal calls")
	})
	tw(t, f, "maps a deny verdict with a reason to a denied_with_reason decision", func(t *testing.T) {
		terminal := newTerminal()
		link := newLink(AuthorizerVerdict{Kind: "deny", Reason: sp("wrong path; use pi-packages")}, "model-judge")
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, terminal, &queryFake{}, log).Authorize(authDetails()))
		eq(t, decision, PermissionPromptDecision{State: DecisionDeniedWithReason, DenialReason: sp("wrong path; use pi-packages"),
			DecidedBy: DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("wrong path; use pi-packages")}}, "decision")
		eq(t, terminal.callCount(), 0, "terminal calls")
	})
	tw(t, f, "maps a deny verdict without a reason to a plain denied decision", func(t *testing.T) {
		link := newLink(AuthorizerVerdict{Kind: "deny"}, "guard")
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, newTerminal(), &queryFake{}, log).Authorize(authDetails()))
		eq(t, decision, PermissionPromptDecision{State: DecisionDenied, DecidedBy: DecisionSource{Kind: "authorizer", Name: "guard", Verdict: "deny"}}, "decision")
	})
	tw(t, f, "falls through a defer verdict to the terminal", func(t *testing.T) {
		terminalDecision := PermissionPromptDecision{State: DecisionDenied, DecidedBy: decidedByAbsentAuthority, ConfirmationUnavailable: true}
		terminal, link, query, details := newTerminal(terminalDecision), newLink(AuthorizerVerdict{Kind: "defer"}), &queryFake{}, authDetails()
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, terminal, query, log).Authorize(details))
		eq(t, decision, terminalDecision, "decision")
		if len(link.calls) != 1 || link.calls[0].details != details || len(terminal.calls) != 1 || terminal.calls[0] != details {
			t.Error("calls")
		}
	})
	tw(t, f, "tries links in order and the first non-defer verdict wins", func(t *testing.T) {
		terminal := newTerminal()
		first, second, third := newLink(AuthorizerVerdict{Kind: "defer"}, "first"), newLink(AuthorizerVerdict{Kind: "deny", Reason: sp("no")}, "second"), newLink(AuthorizerVerdict{Kind: "allow"}, "third")
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{first.named(), second.named(), third.named()}, terminal, &queryFake{}, log).Authorize(authDetails()))
		// The deciding link is named, not merely the consulted set: a deferring link ahead of it decided nothing.
		eq(t, decision, PermissionPromptDecision{State: DecisionDeniedWithReason, DenialReason: sp("no"),
			DecidedBy: DecisionSource{Kind: "authorizer", Name: "second", Verdict: "deny", Reason: sp("no")}}, "decision")
		eq(t, len(third.calls), 0, "third calls")
		eq(t, terminal.callCount(), 0, "terminal calls")
	})
	tw(t, f, "reaches the terminal when every link defers", func(t *testing.T) {
		terminal := newTerminal(humanApproved)
		first, second := newLink(AuthorizerVerdict{Kind: "defer"}, "first"), newLink(AuthorizerVerdict{Kind: "defer"}, "second")
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{first.named(), second.named()}, terminal, &queryFake{}, log).Authorize(authDetails()))
		// The terminal's own decision passes through unchanged: a link that deferred is not the decider.
		eq(t, decision, humanApproved, "decision")
		eq(t, terminal.callCount(), 1, "terminal calls")
	})

	// Not in the original: a link's rejection decides nothing and never turns into an allow or a fall-through.
	t.Run("a failing link rejects the ask: no allow, no terminal", func(t *testing.T) {
		terminal := newTerminal(humanApproved)
		link := newLink(AuthorizerVerdict{Kind: "allow"}, "broken")
		link.err = errors.New("judge exploded")
		_, err := ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, terminal, &queryFake{}, log).Authorize(authDetails())
		if err == nil || err.Error() != "judge exploded" {
			t.Errorf("err %v", err)
		}
		eq(t, terminal.callCount(), 0, "terminal calls")
	})
	t.Run("an unknown verdict kind defers", func(t *testing.T) {
		terminal := newTerminal(humanApproved)
		link := newLink(AuthorizerVerdict{Kind: "maybe"}, "odd")
		decision := mustDecision(t)(ComposeAuthorizerChain([]NamedAuthorizer{link.named()}, terminal, &queryFake{}, log).Authorize(authDetails()))
		eq(t, decision, humanApproved, "decision")
	})
}

type auditLog struct {
	reviews []fwLogCall
	warns   []string
}

func (l *auditLog) Review(e string, d *jsObject) { l.reviews = append(l.reviews, fwLogCall{e, d}) }
func (l *auditLog) Warn(m string)                { l.warns = append(l.warns, m) }

func TestAuthorizerChainAuditTwins(t *testing.T) {
	f := "authority/authorizer-chain-audit"
	skip := func(a *AuthorizerChainAudit, name, id string) { a.AuditUnregisteredLink(UnregisteredLink{Name: name, RequestID: id}) }
	tw(t, f, "records the skipped name against the ask that skipped it", func(t *testing.T) {
		log := &auditLog{}
		skip(NewAuthorizerChainAudit(log), "model-judge", "req-1")
		eq(t, len(log.reviews), 1, "reviews")
		eq(t, log.reviews[0].event, "authorizer_chain_unregistered_link", "event")
		eq(t, marshalJSON(log.reviews[0].details, ""), `{"requestId":"req-1","name":"model-judge"}`, "details")
	})
	tw(t, f, "records every skip, including a repeat of the same name", func(t *testing.T) {
		log := &auditLog{}
		audit := NewAuthorizerChainAudit(log)
		skip(audit, "model-judge", "req-1")
		skip(audit, "model-judge", "req-2")
		skip(audit, "model-judge", "req-3")
		// The review stream is the auditor's record and must stay complete: the latch bounds the warning, never this.
		eq(t, len(log.reviews), 3, "reviews")
		eq(t, marshalJSON(log.reviews[2].details, ""), `{"requestId":"req-3","name":"model-judge"}`, "last details")
	})
	tw(t, f, "warns with the message naming the skipped link", func(t *testing.T) {
		log := &auditLog{}
		skip(NewAuthorizerChainAudit(log), "model-judge", "req-1")
		eq(t, log.warns, []string{UnregisteredLinkMessage("model-judge")}, "warns")
	})
	tw(t, f, "warns once per name, however many asks skip it", func(t *testing.T) {
		log := &auditLog{}
		audit := NewAuthorizerChainAudit(log)
		skip(audit, "model-judge", "req-1")
		skip(audit, "model-judge", "req-2")
		skip(audit, "model-judge", "req-3")
		eq(t, len(log.warns), 1, "warns")
	})
	tw(t, f, "warns again for a different name", func(t *testing.T) {
		log := &auditLog{}
		audit := NewAuthorizerChainAudit(log)
		skip(audit, "model-judge", "req-1")
		skip(audit, "typo-judge", "req-2")
		// Each configured name is its own misconfiguration, and the count is bounded by authorizerChain.length.
		eq(t, len(log.warns), 2, "warns")
		eq(t, log.warns[1], UnregisteredLinkMessage("typo-judge"), "last warn")
	})
	tw(t, f, "names the link, the config field, and the review event", func(t *testing.T) {
		m := UnregisteredLinkMessage("model-judge")
		for _, s := range []string{`"model-judge"`, "authorizerChain", "authorizer_chain_unregistered_link"} {
			if !strings.Contains(m, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
	tw(t, f, "admits all three causes rather than asserting one", func(t *testing.T) {
		m := UnregisteredLinkMessage("model-judge")
		for _, s := range []string{"excludedExtensionPackages", "failed to load", "no configuration of its own"} {
			if !strings.Contains(m, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
	tw(t, f, "claims only that this ask was decided without the link", func(t *testing.T) {
		if !strings.Contains(UnregisteredLinkMessage("model-judge"), "this ask is being decided without it") {
			t.Error("claim missing")
		}
	})
}

func TestAuthorizerRegistryTwins(t *testing.T) {
	f := "authority/authorizer-registry"
	mustRegister := func(t *testing.T, r AuthorizerRegistrar, name string, fn AuthorizeFunc) func() {
		t.Helper()
		dispose, err := r.Register(name, fn)
		if err != nil {
			t.Fatal(err)
		}
		return dispose
	}
	tw(t, f, "stores a link so get() returns it", func(t *testing.T) {
		r := &AuthorizerRegistry{}
		mustRegister(t, r, "model-judge", deferLink)
		if !sameFn(r.Get("model-judge"), deferLink) {
			t.Error("not the registered link")
		}
	})
	tw(t, f, "returns a disposer that removes the link", func(t *testing.T) {
		r := &AuthorizerRegistry{}
		mustRegister(t, r, "model-judge", deferLink)()
		if r.Get("model-judge") != nil {
			t.Error("still registered")
		}
	})
	tw(t, f, "throws when a link is already registered for the same name", func(t *testing.T) {
		r := &AuthorizerRegistry{}
		mustRegister(t, r, "model-judge", deferLink)
		_, err := r.Register("model-judge", allowLink)
		if err == nil || !strings.Contains(err.Error(), "model-judge") || err.Error() != "An authorizer is already registered for 'model-judge'." {
			t.Errorf("err %v", err)
		}
	})
	tw(t, f, "allows registering different names independently", func(t *testing.T) {
		r := &AuthorizerRegistry{}
		mustRegister(t, r, "judge-a", allowLink)
		mustRegister(t, r, "judge-b", denyLink)
		if !sameFn(r.Get("judge-a"), allowLink) || !sameFn(r.Get("judge-b"), denyLink) {
			t.Error("wrong links")
		}
	})
	tw(t, f, "stale disposer does not evict a later registration", func(t *testing.T) {
		r := &AuthorizerRegistry{}
		disposeFirst := mustRegister(t, r, "model-judge", deferLink)
		disposeFirst() // removes first
		mustRegister(t, r, "model-judge", allowLink)
		disposeFirst() // a stale disposer again must not remove the second
		if !sameFn(r.Get("model-judge"), allowLink) {
			t.Error("the later registration was evicted")
		}
	})
	tw(t, f, "returns undefined for an unregistered name", func(t *testing.T) {
		if (&AuthorizerRegistry{}).Get("unknown") != nil {
			t.Error("not nil")
		}
	})

	observed := func(adjudicatesLocally *bool) (*ObservedAuthorizerRegistrar, *AuthorizerRegistry, *fwLogger) {
		registry, logger := &AuthorizerRegistry{}, &fwLogger{}
		return NewObservedAuthorizerRegistrar(registry, roleFunc(func() bool { return *adjudicatesLocally }), logger), registry, logger
	}
	yes, no := true, false
	tw(t, f, "accepts a link on a relaying node, where no chain will consult it", func(t *testing.T) {
		registrar, registry, _ := observed(&no)
		mustRegister(t, registrar, "model-judge", deferLink)
		if !sameFn(registry.Get("model-judge"), deferLink) {
			t.Error("not registered")
		}
	})
	tw(t, f, "records the vacancy so a link registered where no chain runs is never silent", func(t *testing.T) {
		registrar, _, logger := observed(&no)
		mustRegister(t, registrar, "model-judge", deferLink)
		eq(t, len(logger.reviews()), 1, "reviews")
		eq(t, logger.reviews()[0].event, "authorizer_link_vacant", "event")
		eq(t, marshalJSON(logger.reviews()[0].details, ""), `{"name":"model-judge"}`, "details")
	})
	tw(t, f, "records nothing on a node that adjudicates its own asks", func(t *testing.T) {
		registrar, registry, logger := observed(&yes)
		mustRegister(t, registrar, "model-judge", deferLink)
		if !sameFn(registry.Get("model-judge"), deferLink) || len(logger.reviews()) != 0 {
			t.Error("recorded or not registered")
		}
	})
	tw(t, f, "returns the underlying disposer", func(t *testing.T) {
		registrar, registry, _ := observed(&no)
		mustRegister(t, registrar, "model-judge", deferLink)()
		if registry.Get("model-judge") != nil {
			t.Error("still registered")
		}
	})
	tw(t, f, "lets a duplicate registration throw, and records no vacancy for it", func(t *testing.T) {
		registrar, _, logger := observed(&no)
		mustRegister(t, registrar, "model-judge", deferLink)
		logger.clearReview()
		if _, err := registrar.Register("model-judge", deferLink); err == nil || !strings.Contains(err.Error(), "model-judge") {
			t.Errorf("err %v", err)
		}
		eq(t, len(logger.reviews()), 0, "reviews")
	})
	tw(t, f, "reads the node's role per registration, not once at construction", func(t *testing.T) {
		local := true
		registrar, _, logger := observed(&local)
		mustRegister(t, registrar, "first", deferLink)
		local = false
		mustRegister(t, registrar, "second", deferLink)
		eq(t, len(logger.reviews()), 1, "reviews")
		eq(t, marshalJSON(logger.reviews()[0].details, ""), `{"name":"second"}`, "details")
	})
}

type roleFunc func() bool

func (r roleFunc) AdjudicatesLocally() bool { return r() }
