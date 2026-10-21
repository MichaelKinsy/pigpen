package pi_permission_system

// Port of src/authority/permission-prompter.ts: the ask details, and the prompter that brackets the ask path with review-log
// entries around the selected terminal authorizer.

// PermissionReviewSource is where an ask was raised: "tool_call", "skill_input" or "skill_read".
type PermissionReviewSource = string

// ForwardedAskProvenance is who forwarded an ask: who is really asking, one hop below. Present on PromptPermissionDetails only when
// the ask was forwarded from a subagent.
type ForwardedAskProvenance struct{ RequesterAgentName, RequesterSessionID *string }

// PromptPermissionDetails are the details passed when prompting for a permission decision. A nil pointer is undefined (or, for
// AgentName and the forwarding fields, null: no reader tells the two apart). Surface and Value are the explicit display overrides
// of a forwarded ask: nil is "derive from the request".
type PromptPermissionDetails struct {
	RequestID string
	Source    PermissionReviewSource
	AgentName *string
	// Payload is the complete structured description of this ask (ADR 0011 section 2); every ask carries one.
	Payload          PromptPayload
	ToolCallID       *string
	ToolName         *string
	SkillName        *string
	Path             *string
	Command          *string
	Target           *string
	ToolInputPreview *string
	// SessionLabel overrides the "for this session" dialog option label.
	SessionLabel *string
	Surface      *string
	Value        *string
	// Forwarding is present iff this ask was forwarded from a subagent.
	Forwarding *ForwardedAskProvenance
	// SessionApproval is the session-approval suggestion for this ask.
	SessionApproval *ForwardedSessionApproval
	// AccessIntent is the child-fixed access facts the raising gate computed.
	AccessIntent *ForwardedAccessFacts
}

// PermissionPrompterApi is the narrow seam onto PermissionPrompter.
type PermissionPrompterApi interface {
	Prompt(authorizer TerminalAuthorizer, details *PromptPermissionDetails) (PermissionPromptDecision, error)
}

// PermissionPrompterDeps are the dependencies of a PermissionPrompter.
type PermissionPrompterDeps struct {
	// Logger writes structured entries to the permission review log.
	Logger ReviewLogger
}

// PermissionPrompter brackets the ask-path flow with review-log entries and delegates the live decision to the selected
// TerminalAuthorizer: a "waiting" entry, the authorizer, then an "approved" or "denied" entry. Yolo-mode auto-approval happens
// upstream, so an ask under yolo never reaches this type.
type PermissionPrompter struct{ deps PermissionPrompterDeps }

// NewPermissionPrompter is `new PermissionPrompter(deps)`.
func NewPermissionPrompter(deps PermissionPrompterDeps) *PermissionPrompter {
	return &PermissionPrompter{deps: deps}
}

// recordedGrantWidth is the width a decision's session grant was recorded at, or "" when it granted nothing for the session. A
// session-granting entry states its width explicitly (absent means "proven"), because the review log is read rather than consumed.
func recordedGrantWidth(d PermissionPromptDecision) SessionGrantWidth {
	if d.State != DecisionApprovedForSession && d.State != DecisionApprovedForServingSess {
		return ""
	}
	if d.SessionGrantWidth == "" {
		return "proven"
	}
	return d.SessionGrantWidth
}

// Prompt implements PermissionPrompterApi. An authorizer error is the original's rejection: no closing entry is written.
func (p *PermissionPrompter) Prompt(authorizer TerminalAuthorizer, details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	p.writeReviewEntry("permission_request.waiting", details, nil)

	decision, err := authorizer.Authorize(details)
	if err != nil {
		return PermissionPromptDecision{}, err
	}

	event := "permission_request.denied"
	if decision.Approved {
		event = "permission_request.approved"
	}
	resolution := string(decision.State)
	if decision.ConfirmationUnavailable {
		resolution = "confirmation_unavailable"
	}
	p.writeReviewEntry(event, details, &reviewOutcome{resolution: resolution, denialReason: decision.DenialReason, decidedBy: decision.DecidedBy, width: recordedGrantWidth(decision)})
	return decision, nil
}

// reviewOutcome is what the closing entry adds to the details.
type reviewOutcome struct {
	resolution   string
	denialReason *string
	decidedBy    DecisionSource
	width        SessionGrantWidth
}

// writeReviewEntry: the `waiting` entry carries no decidedBy, because nothing has decided yet and a null there would read as
// "decided by nobody" rather than "not yet".
func (p *PermissionPrompter) writeReviewEntry(event string, details *PromptPermissionDetails, outcome *reviewOutcome) {
	entry := newObject()
	if outcome != nil {
		if outcome.decidedBy.Kind != "" {
			entry.set("decidedBy", outcome.decidedBy.JS())
		}
		if outcome.width != "" {
			entry.set("sessionGrantWidth", string(outcome.width))
		}
	}
	entry.set("requestId", details.RequestID)
	entry.set("source", details.Source)
	entry.set("agentName", strOrNull(details.AgentName))
	facts := RenderReviewLogFacts(details.Payload)
	for _, k := range facts.keys {
		entry.set(k, facts.vals[k])
	}
	entry.set("toolCallId", strOrNull(details.ToolCallID))
	entry.set("toolName", strOrNull(details.ToolName))
	entry.set("skillName", strOrNull(details.SkillName))
	entry.set("path", strOrNull(details.Path))
	entry.set("command", strOrNull(details.Command))
	entry.set("target", strOrNull(details.Target))
	entry.set("toolInputPreview", strOrNull(details.ToolInputPreview))
	if outcome != nil {
		entry.set("resolution", outcome.resolution)
		entry.set("denialReason", strOrNull(outcome.denialReason))
	} else {
		entry.set("resolution", nil)
		entry.set("denialReason", nil)
	}
	p.deps.Logger.Review(event, entry)
}
