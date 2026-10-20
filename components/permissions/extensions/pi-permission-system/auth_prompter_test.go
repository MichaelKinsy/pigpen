package pi_permission_system

import (
	"errors"
	"testing"
)

// Twins of test/authority/permission-prompter.test.ts.

// prompterDetails is this file's semantic defaults over the shared structural fixture.
func prompterDetails(mut ...func(*PromptPermissionDetails)) *PromptPermissionDetails {
	return authDetails(append([]func(*PromptPermissionDetails){func(d *PromptPermissionDetails) {
		d.RequestID, d.AgentName, d.ToolName = "req-123", sp("test-agent"), sp("read")
	}}, mut...)...)
}

func TestPermissionPrompterTwins(t *testing.T) {
	f := "authority/permission-prompter"
	run := func(t *testing.T, decision PermissionPromptDecision, details *PromptPermissionDetails) (*fwLogger, *terminalFake, PermissionPromptDecision) {
		t.Helper()
		logger, authorizer := &fwLogger{}, newTerminal(decision)
		got, err := NewPermissionPrompter(PermissionPrompterDeps{Logger: logger}).Prompt(authorizer, details)
		if err != nil {
			t.Fatal(err)
		}
		return logger, authorizer, got
	}
	human := func(state PermissionDecisionState, approved bool, mut ...func(*PermissionPromptDecision)) PermissionPromptDecision {
		d := PermissionPromptDecision{Approved: approved, State: state, DecidedBy: decidedByHuman}
		for _, m := range mut {
			m(&d)
		}
		return d
	}
	approved := human(DecisionApproved, true)

	tw(t, f, "logs permission_request.waiting before the outcome", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		eq(t, reviewEvents(logger), []string{"permission_request.waiting", "permission_request.approved"}, "events")
	})
	tw(t, f, "calls authorizer.authorize with the details", func(t *testing.T) {
		details := prompterDetails()
		_, authorizer, _ := run(t, approved, details)
		if len(authorizer.calls) != 1 || authorizer.calls[0] != details {
			t.Error("not called with the details")
		}
	})
	tw(t, f, "logs permission_request.approved when the authorizer approves", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		if !logger.reviewMatching("permission_request.approved", fwObj("requestId", "req-123", "resolution", "approved")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "names the width a session grant was recorded at", func(t *testing.T) {
		logger, _, _ := run(t, human(DecisionApprovedForSession, true, func(d *PermissionPromptDecision) { d.SessionGrantWidth = "family" }), prompterDetails())
		if !logger.reviewMatching("permission_request.approved", fwObj("resolution", "approved_for_session", "sessionGrantWidth", "family")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "names the proven width when a session grant chose no width", func(t *testing.T) {
		logger, _, _ := run(t, human(DecisionApprovedForSession, true), prompterDetails())
		if !logger.reviewMatching("permission_request.approved", fwObj("sessionGrantWidth", "proven")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "names no width for an approval that granted nothing for the session", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		reviews := logger.reviews()
		if reviews[len(reviews)-1].details.has("sessionGrantWidth") {
			t.Error("a width was named")
		}
	})
	tw(t, f, "logs permission_request.denied when the authorizer denies", func(t *testing.T) {
		logger, _, _ := run(t, human(DecisionDenied, false), prompterDetails())
		if !logger.reviewMatching("permission_request.denied", fwObj("requestId", "req-123", "resolution", "denied")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "logs confirmation_unavailable resolution when the decision carries the marker", func(t *testing.T) {
		logger, _, _ := run(t, PermissionPromptDecision{State: DecisionDenied, ConfirmationUnavailable: true, DecidedBy: decidedByAbsentAuthority}, prompterDetails())
		if !logger.reviewMatching("permission_request.denied", fwObj("resolution", "confirmation_unavailable")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "logs permission_request.denied with denialReason when present", func(t *testing.T) {
		logger, _, _ := run(t, human(DecisionDeniedWithReason, false, func(d *PermissionPromptDecision) { d.DenialReason = sp("too sensitive") }), prompterDetails())
		if !logger.reviewMatching("permission_request.denied", fwObj("denialReason", "too sensitive")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "records who decided on the outcome entry", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		if !logger.reviewMatching("permission_request.approved", fwObj("decidedBy", fwObj("kind", "user", "via", "dialog"))) {
			t.Error("not logged")
		}
	})
	tw(t, f, "records the decider on a denial too", func(t *testing.T) {
		d := PermissionPromptDecision{State: DecisionDenied, ConfirmationUnavailable: true, DecidedBy: DecisionSource{Kind: "unavailable", Reason: sp("nobody was home")}}
		logger, _, _ := run(t, d, prompterDetails())
		if !logger.reviewMatching("permission_request.denied", fwObj("decidedBy", fwObj("kind", "unavailable", "reason", "nobody was home"))) {
			t.Error("not logged")
		}
	})
	tw(t, f, "leaves the waiting entry unattributed — nothing has decided yet", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		if logger.reviews()[0].details.has("decidedBy") {
			t.Error("the waiting entry names a decider")
		}
	})
	tw(t, f, "returns the decision from the authorizer", func(t *testing.T) {
		decision := human(DecisionDeniedWithReason, false, func(d *PermissionPromptDecision) { d.DenialReason = sp("sensitive") })
		_, _, got := run(t, decision, prompterDetails())
		eq(t, got, decision, "decision")
	})

	tw(t, f, "includes all standard fields in the waiting log entry", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails(func(d *PromptPermissionDetails) {
			d.ToolCallID, d.SkillName, d.Path, d.Command, d.Target, d.ToolInputPreview =
				sp("tc-1"), sp("librarian"), sp("/src/foo.ts"), sp("git status"), sp("server:tool"), sp("{ path: '...' }")
		}))
		if !logger.reviewMatching("permission_request.waiting", fwObj("requestId", "req-123", "source", "tool_call", "agentName", "test-agent",
			"toolCallId", "tc-1", "toolName", "read", "skillName", "librarian", "path", "/src/foo.ts", "command", "git status",
			"target", "server:tool", "toolInputPreview", "{ path: '...' }")) {
			t.Errorf("not logged: %s", marshalJSON(logger.reviews()[0].details, ""))
		}
	})
	tw(t, f, "uses null for optional fields not present in details", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		want := fwObj("toolCallId", nil, "skillName", nil, "path", nil, "command", nil, "target", nil, "toolInputPreview", nil)
		if !logger.reviewMatching("permission_request.waiting", want) {
			t.Errorf("not logged: %s", marshalJSON(logger.reviews()[0].details, ""))
		}
	})
	tw(t, f, "records the payload's request facts rather than its prompt wording", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails(func(d *PromptPermissionDetails) {
			d.Payload = authPayload(func(p *PromptPayload) {
				p.Kind = "bash"
				p.Request.Surface, p.Request.ToolName, p.Request.Value, p.Request.MatchedPattern = "bash", sp("bash"), "rm -rf build", sp("rm *")
			})
		}))
		if !logger.reviewMatching("permission_request.waiting", fwObj("surface", "bash", "matchedPattern", "rm *")) {
			t.Error("not logged")
		}
	})
	tw(t, f, "persists neither the payload nor the prompt sentence", func(t *testing.T) {
		logger, _, _ := run(t, approved, prompterDetails())
		entry := logger.reviews()[0].details
		for _, k := range []string{"payload", "message", "evidence"} {
			if entry.has(k) {
				t.Errorf("entry has %q", k)
			}
		}
	})

	// Not in the original: a rejecting authorizer writes no closing entry and returns its error, never a decision.
	t.Run("an authorizer error is returned and writes no closing entry", func(t *testing.T) {
		logger, authorizer := &fwLogger{}, newTerminal()
		authorizer.err = errors.New("dialog exploded")
		_, err := NewPermissionPrompter(PermissionPrompterDeps{Logger: logger}).Prompt(authorizer, prompterDetails())
		if err == nil || err.Error() != "dialog exploded" {
			t.Errorf("err %v", err)
		}
		eq(t, reviewEvents(logger), []string{"permission_request.waiting"}, "events")
	})
}
