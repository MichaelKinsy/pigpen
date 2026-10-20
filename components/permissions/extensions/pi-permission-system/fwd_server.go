package pi_permission_system

import (
	"fmt"
	"path/filepath"
	"time"
)

// Port of src/authority/forwarded-request-server.ts: the serving-down role of forwarded permissions. It drains this session's
// inbox and answers each request the way the session resolves a local action: its ForwardedAccessIntent against recorded authority
// (ServingPolicy), then escalation to the selected authorizer (AskEscalator) on ask (ADR 0008).

// ServingPolicy is the recorded-authority view the serving node resolves a forwarded request against: answer one
// ForwardedAccessIntent on the serving session's composed ruleset, agent-scoped to the requester (principal.agentName, ADR 0008
// section 3). The child-fixed matchValues are used as-is, never re-derived through this session's PathNormalizer or cwd.
type ServingPolicy interface {
	Resolve(intent ForwardedAccessIntent) PermissionCheckResult
}

// ForwardedRequestServerDeps are the constructor config of ForwardedRequestServer.
type ForwardedRequestServerDeps struct {
	ForwardingDir string
	Logger        DebugReviewLogger
	// Policy is the recorded-authority resolution for a forwarded ForwardedAccessIntent.
	Policy ServingPolicy
	// Escalator is the escalation seam to the serving session's selected authorizer on ask.
	Escalator AskEscalator
	// Broadcaster is the terminal-decision broadcast for an ask this session served (#610): a forwarded ask is prompted here but gated
	// in the requesting session, so without it the parent's own consumers observe a prompt that never ends.
	Broadcaster DecisionBroadcaster
	// Recorder is the serving session's SessionRules: it records a whole-session grant when a human approves a forwarded request for
	// the entire serving session.
	Recorder SessionApprovalRecorder
	// Registry is the in-process subagent registry, read only by the one-hop canary. Optional.
	Registry ParentChainRegistry
}

// buildForwardedAskPayload is presentation/forwarded-ask-payload.ts buildForwardedAskPayload: a projection, not a synthesizer. The
// child ships its own complete payload; only the requester is re-stamped as forwarded (the request's own provenance is
// authoritative, #292). A request carrying no payload renders from the display fields it does hold (ADR 0011 section 9).
func buildForwardedAskPayload(request *ForwardedPermissionRequest) PromptPayload {
	agent := request.RequesterAgentName
	session := request.RequesterSessionID
	requester := PromptRequester{AgentName: &agent, Forwarded: true, SessionID: &session}
	if request.Payload != nil {
		payload := *request.Payload
		payload.Request.Requester = requester
		return payload
	}
	surface, value := "", ""
	if request.Surface.Value != nil {
		surface = *request.Surface.Value
	}
	if request.Value.Value != nil {
		value = *request.Value.Value
	}
	return PromptPayload{
		Kind: "forwarded",
		Request: PromptRequestFacts{
			Requester: requester, Surface: surface, Value: value,
		},
		// Nothing to carry: the wire no longer relays a sentence, and inventing evidence the child never sent is exactly the fiction
		// the bounded renderers would then have to trust.
		Evidence:    []PromptEvidence{},
		Annotations: []PromptAnnotation{},
	}
}

func nonEmptyOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// buildForwardedAskDetails maps a forwarded request onto the escalated ask's details, carrying the forwarded provenance so the
// serving node's authorizer emits a non-degraded broadcast (#292), plus the child-fixed access facts (ADR 0008; #635). The display
// surface and the fact surface are distinct and both belong here.
func buildForwardedAskDetails(request *ForwardedPermissionRequest) *PromptPermissionDetails {
	source := request.Source
	if source == "" {
		source = "tool_call"
	}
	details := &PromptPermissionDetails{
		RequestID: request.ID,
		Source:    source,
		AgentName: nonEmptyOrNil(request.RequesterAgentName),
		Payload:   buildForwardedAskPayload(request),
		Surface:   request.Surface.Value,
		Value:     request.Value.Value,
		Forwarding: &ForwardedAskProvenance{
			RequesterAgentName: nonEmptyOrNil(request.RequesterAgentName),
			RequesterSessionID: nonEmptyOrNil(request.RequesterSessionID),
		},
	}
	// Carries the child's suggestion so the authorizer can offer the whole-session grant scope; absent for a version-skew request.
	if request.SessionApproval != nil {
		details.SessionApproval = request.SessionApproval
	}
	// Absent for a version-skew request that carried no intent, which the delegation envelope reads as "surface undetermined" and
	// fail-safes to excluded, so absence must stay absence.
	if request.AccessIntent != nil {
		facts := toAccessFacts(*request.AccessIntent)
		details.AccessIntent = &facts
	}
	return details
}

// toAccessFacts projects the wire intent down to the child-fixed access facts an authorizer may see. Field by field, because this
// is a disclosure boundary: requesterCwd and principal are requester identity for the serving node's own resolution and stay off the
// ask details.
func toAccessFacts(intent ForwardedAccessIntent) ForwardedAccessFacts {
	return ForwardedAccessFacts{Surface: intent.Surface, MatchValues: intent.MatchValues, BoundaryValue: intent.BoundaryValue}
}

// buildServedDecisionEvent builds the terminal permissions:decision for an ask this session served, rendered from the same details
// the ui_prompt broadcast was built from. origin and matchedPattern are null by construction: an escalated request is one recorded
// authority did not decide, so no rule won. The decider stays off the bus (ADR 0011 section 6, #726).
func buildServedDecisionEvent(details *PromptPermissionDetails, decision PermissionPromptDecision) PermissionDecisionEvent {
	facts := details.Payload.Request
	surface, value := facts.Surface, facts.Value
	if details.Surface != nil {
		surface = *details.Surface
	}
	if details.Value != nil {
		value = *details.Value
	}
	result := "deny"
	if decision.Approved {
		result = "allow"
	}
	event := PermissionDecisionEvent{
		RequestID: details.RequestID,
		Surface:   surface,
		Value:     value,
		AgentName: details.AgentName,
		Result:    result,
		// The grant scope is reported as the human chose it: applyGrantScope rewrites a whole-serving-session grant to a plain
		// approval on the wire, but that is about what the child records, not about what was allowed here.
		Resolution: ResolutionFor(decision.DecidedBy, DecisionOutcome{
			Approved:   decision.Approved,
			ForSession: decision.State == DecisionApprovedForSession || decision.State == DecisionApprovedForServingSess,
		}),
	}
	if details.Forwarding != nil {
		event.Forwarding = &ForwardedPromptContext{
			RequesterAgentName: details.Forwarding.RequesterAgentName,
			RequesterSessionID: details.Forwarding.RequesterSessionID,
		}
	}
	return event
}

// ForwardedRequestServer owns the serving-down role of the forwarded-permission behavior.
type ForwardedRequestServer struct{ deps ForwardedRequestServerDeps }

// NewForwardedRequestServer is `new ForwardedRequestServer(deps)`.
func NewForwardedRequestServer(deps ForwardedRequestServerDeps) *ForwardedRequestServer {
	return &ForwardedRequestServer{deps: deps}
}

// ProcessInbox drains and responds to this session's forwarded-permission inbox, one request at a time.
func (s *ForwardedRequestServer) ProcessInbox(ctx ForwarderContext) {
	currentSessionID := GetSessionID(ctx)
	location, ok := GetExistingPermissionForwardingLocation(s.deps.ForwardingDir, currentSessionID)
	if !ok {
		return
	}
	requestFiles := ListRequestFiles(s.deps.Logger, location.RequestsDir)
	if len(requestFiles) == 0 {
		return
	}
	// Defensively recreate responses/ before writing any response: a concurrent cleanup pass may have removed it between the
	// requestsDir existence check above and the write inside processSingleForwardedRequest (the ENOENT write loop, #398).
	if !EnsureDirectoryExists(s.deps.Logger, location.ResponsesDir, "permission forwarding responses") {
		return
	}
	for _, fileName := range requestFiles {
		requestPath := filepath.Join(location.RequestsDir, fileName)
		request := ReadForwardedPermissionRequest(s.deps.Logger, requestPath)
		if request == nil {
			SafeDeleteFile(s.deps.Logger, requestPath, location.Label+" forwarded permission request")
			continue
		}
		s.processSingleForwardedRequest(request, location, requestPath, currentSessionID)
	}
	CleanupPermissionForwardingLocationIfEmpty(s.deps.Logger, location)
}

func (s *ForwardedRequestServer) processSingleForwardedRequest(request *ForwardedPermissionRequest, location PermissionForwardingLocation, requestPath, currentSessionID string) {
	if !IsForwardedPermissionRequestForSession(request.TargetSessionID, currentSessionID) {
		LogPermissionForwardingWarning(s.deps.Logger, fmt.Sprintf(
			"Ignoring forwarded permission request '%s' because it targets session '%s' instead of '%s'",
			request.ID, request.TargetSessionID, currentSessionID), nil)
		SafeDeleteFile(s.deps.Logger, requestPath, location.Label+" forwarded permission request")
		return
	}
	s.warnOnMultiHop(request, currentSessionID)

	logDetails := newObject()
	logDetails.set("requestId", request.ID)
	logDetails.set("source", location.Label)
	logDetails.set("requesterAgentName", request.RequesterAgentName)
	logDetails.set("requesterSessionId", request.RequesterSessionID)
	logDetails.set("targetSessionId", request.TargetSessionID)
	logDetails.set("requestPath", requestPath)

	decision := s.resolveDecision(request, logDetails)
	s.recordForwardedDecision(request, location, requestPath, currentSessionID, s.applyGrantScope(request, decision, logDetails))
}

// applyGrantScope applies the human's grant-scope choice on a forwarded approval. A whole-session grant
// (approved_for_serving_session) records the child's suggested pattern into this serving node's SessionRules, the single source of
// truth for the scope, and is then translated to a plain approved so the child records nothing (its next identical action
// re-forwards and resolves as recorded authority). Every other decision passes through unchanged (approved_for_session: the child
// records). The translation rewrites the grant's scope, never its decider (#726).
func (s *ForwardedRequestServer) applyGrantScope(request *ForwardedPermissionRequest, decision PermissionPromptDecision, logDetails *jsObject) PermissionPromptDecision {
	if decision.State != DecisionApprovedForServingSess {
		return decision
	}
	if request.SessionApproval != nil {
		width := decision.SessionGrantWidth
		if width == "" {
			width = "proven"
		}
		approval := SessionApprovalForGrants(request.SessionApproval.Grants).AtWidth(width)
		s.deps.Recorder.RecordSessionApproval(approval)
		details := logDetails.clone()
		grants := make([]any, len(approval.Grants))
		for i, g := range approval.Grants {
			o := newObject()
			o.set("surface", g.Surface)
			o.set("pattern", g.Pattern)
			grants[i] = o
		}
		details.set("grants", grants)
		s.deps.Logger.Review("forwarded_permission.session_recorded", details)
	}
	return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decision.DecidedBy}
}

// recordForwardedDecision persists the served decision: write the response file the child polls for, log the outcome, and delete the
// drained request. The "respond" half to resolveDecision's "decide" half.
func (s *ForwardedRequestServer) recordForwardedDecision(request *ForwardedPermissionRequest, location PermissionForwardingLocation, requestPath, currentSessionID string, decision PermissionPromptDecision) {
	responsePath := filepath.Join(location.ResponsesDir, request.ID+".json")
	event := "forwarded_permission.denied"
	if decision.Approved {
		event = "forwarded_permission.approved"
	}
	d := newObject()
	d.set("requestId", request.ID)
	d.set("source", location.Label)
	d.set("requesterAgentName", request.RequesterAgentName)
	d.set("requesterSessionId", request.RequesterSessionID)
	d.set("targetSessionId", request.TargetSessionID)
	d.set("responsePath", responsePath)
	d.set("resolution", string(decision.State))
	d.set("denialReason", strOrNull(decision.DenialReason))
	d.set("decidedBy", decision.DecidedBy.JS())
	s.deps.Logger.Review(event, d)

	decidedBy := decision.DecidedBy
	response := ForwardedPermissionResponse{
		Approved:           decision.Approved,
		State:              decision.State,
		DenialReason:       decision.DenialReason,
		ResponderSessionID: currentSessionID,
		RespondedAt:        float64(time.Now().UnixMilli()),
		// Carried onto the wire so the requester can name what decided inside this session, not merely that this session
		// answered (#726).
		DecidedBy: &decidedBy,
		// The child records a subagent-scoped grant itself, so the width the human chose has to reach it (#813).
		SessionGrantWidth: decision.SessionGrantWidth,
	}
	if err := WriteJSONFileAtomic(s.deps.Logger, responsePath, response.JS()); err != nil {
		LogPermissionForwardingError(s.deps.Logger, fmt.Sprintf("Failed to write %s forwarded permission response '%s'", location.Label, responsePath), err)
		return
	}
	SafeDeleteFile(s.deps.Logger, requestPath, location.Label+" forwarded permission request")
}

// resolveDecision resolves the request the way the session resolves a local action: recorded authority first (a request carrying an
// accessIntent resolves against the serving node's composed ruleset: allow, including yolo-rewritten, auto-approves; deny
// auto-denies), then escalate ask (or a request missing accessIntent, the version-skew floor, ADR 0008 section 4) to the selected
// authorizer.
func (s *ForwardedRequestServer) resolveDecision(request *ForwardedPermissionRequest, logDetails *jsObject) PermissionPromptDecision {
	if request.AccessIntent != nil {
		check := s.deps.Policy.Resolve(*request.AccessIntent)
		if check.State != "ask" {
			// The rule is carried in full rather than left to the event name: the response file has no surface, pattern, or origin
			// column for the requester's record to lean on.
			decidedBy := DecisionSource{Kind: "rule", Surface: request.AccessIntent.Surface, Pattern: check.MatchedPattern, Origin: check.Origin}
			approved := check.State == "allow"
			event := "forwarded_permission.auto_denied"
			if approved {
				event = "forwarded_permission.auto_approved"
			}
			details := logDetails.clone()
			details.set("decidedBy", decidedBy.JS())
			s.deps.Logger.Review(event, details)
			if approved {
				return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedBy}
			}
			// A deny-with-reason rule's text is the operator's own explanation, and the requesting session relays it to its agent, so
			// it travels with the verdict rather than stopping at the node that holds the config (#844).
			denied := CreateDeniedPermissionDecision(check.Reason)
			denied.DecidedBy = decidedBy
			return denied
		}
	}

	s.deps.Logger.Review("forwarded_permission.prompted", logDetails)
	details := buildForwardedAskDetails(request)
	decision := s.escalateAsk(details)
	// Announced before the grant-scope translation and before the response is written: the ask this session broadcast is over once
	// someone here has answered it, whatever becomes of the file the child polls for (#610).
	s.deps.Broadcaster.EmitDecision(buildServedDecisionEvent(details, decision))
	return decision
}

// escalateAsk escalates a forwarded ask to the serving session's selected authorizer, failing closed instead of failing out: an
// escalation that breaks is nobody's denial, so the node records itself as the decider.
func (s *ForwardedRequestServer) escalateAsk(details *PromptPermissionDetails) PermissionPromptDecision {
	decision, err := s.deps.Escalator.Escalate(details)
	if err == nil {
		return decision
	}
	reason := FormatUnknownErrorMessage(err)
	LogPermissionForwardingError(s.deps.Logger, fmt.Sprintf("Failed to escalate forwarded permission request '%s'", details.RequestID), err)
	return PermissionPromptDecision{Approved: false, State: DecisionDenied, DecidedBy: DecisionSource{Kind: "gate_error", Reason: &reason}}
}

// warnOnMultiHop is the one-hop canary: forwarding is depth-1 (child to root). If the requester is itself a registered subagent whose
// parent is not this serving session, the request came through more than one hop (or was misrouted); resolution is still
// well-defined, so keep serving, but warn loudly. Unregistered (external file-based) requesters have no recorded parent and are silent.
func (s *ForwardedRequestServer) warnOnMultiHop(request *ForwardedPermissionRequest, currentSessionID string) {
	if s.deps.Registry == nil {
		return
	}
	info, ok := s.deps.Registry.Get(request.RequesterSessionID)
	if !ok || info.ParentSessionID == nil || *info.ParentSessionID == "" || *info.ParentSessionID == currentSessionID {
		return
	}
	LogPermissionForwardingWarning(s.deps.Logger, fmt.Sprintf(
		"Forwarded permission request '%s' violates the one-hop "+
			"invariant: requester '%s' is a registered "+
			"subagent whose parent '%s' is not this "+
			"serving session '%s' (multi-hop or misrouted).",
		request.ID, request.RequesterSessionID, *info.ParentSessionID, currentSessionID), nil)
}
