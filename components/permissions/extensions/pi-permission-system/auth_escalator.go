package pi_permission_system

import (
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Port of src/authority/approval-escalator.ts: ParentAuthorizer, the authorizer for a subagent session, which escalates the ask up
// the tree to the parent's authority by writing a request file and polling for the parent session's response.

// getContextSystemPrompt reads ctx.getSystemPrompt when the host has one; a throwing accessor is a dropped warning (no logger).
func getContextSystemPrompt(getSystemPrompt func() (string, bool, error)) (string, bool) {
	if getSystemPrompt == nil {
		return "", false
	}
	systemPrompt, ok, err := getSystemPrompt()
	if err != nil {
		// No deps available in this helper: the warning is silently dropped.
		LogPermissionForwardingWarning(nil, "Failed to read context system prompt for forwarded permission metadata", err)
		return "", false
	}
	return systemPrompt, ok
}

// forwardedRequestFacts are the facts a forwarded request relays unchanged from the child's ask.
type forwardedRequestFacts struct {
	// requestID is the requester's own permission request id, adopted as the forwarded request's id so one id runs from the child's
	// gate to the serving node's decision.
	requestID string
	payload   PromptPayload
	display   *forwardedPromptDisplay
	// sessionApproval and accessIntent are nil when the details carry none.
	sessionApproval *ForwardedSessionApproval
	accessIntent    *ForwardedAccessFacts
}

// forwardedPromptDisplay is the display projection a forwarded request carries (ForwardedPromptDisplay).
type forwardedPromptDisplay struct {
	source  string
	surface *string
	value   *string
}

// ParentAuthorizerDeps are the constructor config of ParentAuthorizer.
type ParentAuthorizerDeps struct {
	ForwardingDir string
	// Registry is the in-process subagent session registry for forwarding target resolution; nil for none.
	Registry ParentChainRegistry
	// Serving says whether the resolved target is draining its inbox, on whichever channel can say.
	Serving TargetServingLookup
	// GetTimeoutMs is how long to wait for the target's answer, read live so config edits apply.
	GetTimeoutMs func() float64
	Logger       DebugReviewLogger
	// GetSystemPrompt is the host's optional ctx.getSystemPrompt accessor (nil when the host has none). ok false is a value that is
	// not a string; an error is the accessor's throw.
	GetSystemPrompt func() (systemPrompt string, ok bool, err error)
}

// abandon denies because no authority ever ruled: the request was never delivered, never answered, or answered unreadably.
// ConfirmationUnavailable keeps this out of the "User denied ..." message (#719). The provenance record reuses the same string
// rather than restating it, so what the model is told and what the log attributes cannot drift (#726).
func abandon(denialReason string) PermissionPromptDecision {
	return PermissionPromptDecision{
		Approved:                false,
		State:                   DecisionDenied,
		ConfirmationUnavailable: true,
		DenialReason:            &denialReason,
		DecidedBy:               DecisionSource{Kind: "unavailable", Reason: &denialReason},
	}
}

// relayDecision adopts the responder's answer, recording the hop it came through. Nesting keeps both the session that answered and
// what within it decided, rather than flattening the responder's source into this node's record, where it would read as a local
// decision (#726). A responder that sent no usable source yields Decision nil: the hop is still a fact, and an older parent is not
// an error.
func relayDecision(response ForwardedPermissionResponse) PermissionPromptDecision {
	responder := response.ResponderSessionID
	return PermissionPromptDecision{
		Approved:          response.Approved,
		State:             response.State,
		DenialReason:      response.DenialReason,
		SessionGrantWidth: response.SessionGrantWidth,
		DecidedBy:         DecisionSource{Kind: "forwarded", ResponderSessionID: &responder, Decision: response.DecidedBy},
	}
}

// filenameSafeRequestID is the ids this node is willing to use as a request/response filename.
var filenameSafeRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// forwardableRequestID is the id to write on the forwarded request: the requester's own, or a fresh mint when that id could not
// safely name a file. At a relay hop the adopted id came from a request file on disk, which the tolerant reader validates only as a
// string, so this is the boundary that keeps an inbound id from choosing an outbound path.
func forwardableRequestID(requesterRequestID string) string {
	if filenameSafeRequestID.MatchString(requesterRequestID) {
		return requesterRequestID
	}
	return CreatePermissionRequestId()
}

// ParentAuthorizer is the authorizer for a subagent session: escalate the ask up the tree to the parent's authority. ctx is bound
// once at construction: SelectAuthorizer only constructs a ParentAuthorizer for a context it has already established is a child,
// whether that child has a UI of its own (#909) or not, so Authorize never re-derives that dispatch.
type ParentAuthorizer struct {
	ctx             ForwarderContext
	forwardingDir   string
	registry        ParentChainRegistry
	serving         TargetServingLookup
	getTimeoutMs    func() float64
	logger          DebugReviewLogger
	getSystemPrompt func() (string, bool, error)
}

// NewParentAuthorizer is `new ParentAuthorizer(ctx, deps)`.
func NewParentAuthorizer(ctx ForwarderContext, deps ParentAuthorizerDeps) *ParentAuthorizer {
	return &ParentAuthorizer{ctx: ctx, forwardingDir: deps.ForwardingDir, registry: deps.Registry, serving: deps.Serving, getTimeoutMs: deps.GetTimeoutMs, logger: deps.Logger, getSystemPrompt: deps.GetSystemPrompt}
}

// Authorize implements TerminalAuthorizer. An error is the original's rejection (the session manager threw while the requester was
// being identified); every other way of giving up is an abandon decision, never an error and never an approval.
func (p *ParentAuthorizer) Authorize(details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	uiPrompt := BuildUiPromptFromDetails(details)
	facts := forwardedRequestFacts{
		requestID:       details.RequestID,
		payload:         details.Payload,
		display:         &forwardedPromptDisplay{source: uiPrompt.Source, surface: uiPrompt.Surface, value: uiPrompt.Value},
		sessionApproval: details.SessionApproval,
		accessIntent:    details.AccessIntent,
	}
	return p.waitForForwardedApproval(facts)
}

func (p *ParentAuthorizer) waitForForwardedApproval(facts forwardedRequestFacts) (PermissionPromptDecision, error) {
	ctx := p.ctx
	requesterSessionID := GetSessionID(ctx)
	target, found := ResolvePermissionForwardingTarget(ResolveTargetOptions{
		// Invariant: SelectAuthorizer only selects ParentAuthorizer for a context it has already established is a child, so no
		// detection is needed to re-derive it here.
		IsSubagent:       true,
		CurrentSessionID: requesterSessionID,
		SessionID:        requesterSessionID,
		Registry:         p.registry,
	})

	if !found {
		candidates := ""
		for i, c := range SubagentParentSessionEnvCandidates {
			if i > 0 {
				candidates += ", "
			}
			candidates += c
		}
		LogPermissionForwardingError(p.logger,
			"Permission forwarding target session could not be resolved. "+
				"Checked env vars: "+candidates+". "+
				"If you are using a subagent extension (nicobailon/pi-subagents, HazAT/pi-interactive-subagents, etc.), "+
				"ask its maintainer to set PI_SUBAGENT_PARENT_SESSION in the child process environment "+
				"(see https://github.com/gotgenes/pi-permission-system/issues/143).", nil)
		return abandon("Could not resolve a parent session to forward this permission request to"), nil
	}

	location, ok := EnsurePermissionForwardingLocation(p.logger, p.forwardingDir, target.SessionID)
	if !ok {
		LogPermissionForwardingError(p.logger, "Permission forwarding is unavailable because session-scoped directories could not be prepared for '"+target.SessionID+"'", nil)
		return abandon("Permission forwarding directories could not be prepared for session '" + target.SessionID + "'"), nil
	}

	request, err := p.buildForwardedRequest(facts, requesterSessionID, target.SessionID)
	if err != nil {
		return PermissionPromptDecision{}, err
	}
	requestPath := filepath.Join(location.RequestsDir, request.ID+".json")
	responsePath := filepath.Join(location.ResponsesDir, request.ID+".json")

	created := newObject()
	created.set("requestId", request.ID)
	created.set("requesterAgentName", request.RequesterAgentName)
	created.set("requesterSessionId", request.RequesterSessionID)
	created.set("targetSessionId", target.SessionID)
	created.set("requestPath", requestPath)
	created.set("responsePath", responsePath)
	p.logger.Review("forwarded_permission.request_created", created)

	if err := WriteJSONFileAtomic(p.logger, requestPath, request.JS()); err != nil {
		LogPermissionForwardingError(p.logger, "Failed to write forwarded permission request '"+requestPath+"'", err)
		CleanupPermissionForwardingLocationIfEmpty(p.logger, location)
		return abandon("The forwarded permission request could not be written"), nil
	}

	return p.pollForForwardedResponse(location, request, requestPath, responsePath, target), nil
}

func (p *ParentAuthorizer) buildForwardedRequest(facts forwardedRequestFacts, requesterSessionID, targetSessionID string) (ForwardedPermissionRequest, error) {
	ctx := p.ctx
	requestID := forwardableRequestID(facts.requestID)
	requesterAgentName, err := p.requesterAgentName()
	if err != nil {
		return ForwardedPermissionRequest{}, err
	}
	// Complete the child-fixed facts into a full ForwardedAccessIntent: the gate fixed the access facts; the edge stamps the
	// requester identity it alone knows (cwd and principal). The parent resolves against this intent and never re-derives the match
	// set (ADR 0008).
	var accessIntent *ForwardedAccessIntent
	if facts.accessIntent != nil {
		accessIntent = &ForwardedAccessIntent{
			ForwardedAccessFacts: *facts.accessIntent,
			RequesterCwd:         GetCwd(ctx),
			Principal:            ForwardedPrincipal{SessionID: requesterSessionID, AgentName: requesterAgentName},
		}
	}
	payload := facts.payload
	request := ForwardedPermissionRequest{
		ID:                 requestID,
		CreatedAt:          float64(time.Now().UnixMilli()),
		RequesterSessionID: requesterSessionID,
		TargetSessionID:    targetSessionID,
		RequesterAgentName: requesterAgentName,
		Payload:            &payload,
		SessionApproval:    facts.sessionApproval,
		AccessIntent:       accessIntent,
	}
	if facts.display != nil {
		request.Source = facts.display.source
		request.Surface = DisplayString{Set: true, Value: facts.display.surface}
		request.Value = DisplayString{Set: true, Value: facts.display.value}
	}
	return request, nil
}

// requesterAgentName is `getActiveAgentName(ctx) ?? getActiveAgentNameFromSystemPrompt(getContextSystemPrompt(ctx)) ?? "unknown"`.
func (p *ParentAuthorizer) requesterAgentName() (string, error) {
	if p.ctx.SessionManager != nil {
		name, err := GetActiveAgentName(p.ctx.SessionManager)
		if err != nil {
			return "", err
		}
		if name != nil {
			return *name, nil
		}
	}
	if systemPrompt, ok := getContextSystemPrompt(p.getSystemPrompt); ok {
		if name := GetActiveAgentNameFromSystemPrompt(systemPrompt); name != nil {
			return *name, nil
		}
	}
	return "unknown", nil
}

func (p *ParentAuthorizer) pollForForwardedResponse(location PermissionForwardingLocation, request ForwardedPermissionRequest, requestPath, responsePath string, target PermissionForwardingTarget) PermissionPromptDecision {
	requestID, requesterAgentName, targetSessionID := request.ID, request.RequesterAgentName, request.TargetSessionID
	timeoutMs := p.getTimeoutMs()
	deadline := float64(time.Now().UnixMilli()) + timeoutMs
	// unservedSince is the time the target first looked unserved, or -1 (the original's null) while it looks fine.
	unservedSince := int64(-1)

	for float64(time.Now().UnixMilli()) < deadline {
		if _, err := os.Stat(responsePath); err == nil {
			response := ReadForwardedPermissionResponse(p.logger, responsePath)
			var relayed *PermissionPromptDecision
			if response != nil {
				r := relayDecision(*response)
				relayed = &r
			}
			received := newObject()
			received.set("requestId", requestID)
			if response != nil {
				received.set("approved", response.Approved)
				received.set("state", string(response.State))
				received.set("denialReason", strOrNull(response.DenialReason))
				received.set("responderSessionId", response.ResponderSessionID)
			} else {
				received.set("approved", nil)
				received.set("state", nil)
				received.set("denialReason", nil)
				received.set("responderSessionId", nil)
			}
			received.set("targetSessionId", targetSessionID)
			received.set("responsePath", responsePath)
			if relayed != nil {
				received.set("decidedBy", relayed.DecidedBy.JS())
			} else {
				received.setOpt("decidedBy", nil)
			}
			p.logger.Review("forwarded_permission.response_received", received)
			p.discardRequest(location, requestPath, responsePath)
			if relayed != nil {
				return *relayed
			}
			return abandon("The parent session's permission response could not be read")
		}

		unservedSince = p.checkServingLiveness(target, unservedSince)
		if unservedSince >= 0 && time.Now().UnixMilli()-unservedSince >= PermissionForwardingServingGraceMs {
			observation := p.serving.Describe(target)
			entry := newObject()
			entry.set("requestId", requestID)
			entry.set("requesterSessionId", request.RequesterSessionID)
			entry.set("targetSessionId", targetSessionID)
			// Which channel answered, and what it saw: the difference between a parent that exited, one that was killed, and one
			// polling under a different session id is the whole diagnosis of a stalled forward.
			entry.set("servingChannel", observation.Channel)
			if observation.State == "" {
				entry.set("servingState", nil)
			} else {
				entry.set("servingState", string(observation.State))
			}
			ids := make([]any, len(observation.ServingIDs))
			for i, id := range observation.ServingIDs {
				ids[i] = id
			}
			entry.set("servingSessionIds", ids)
			p.logger.Review("forwarded_permission.no_serving_session", entry)
			p.discardRequest(location, requestPath, "")
			return abandon("Session '" + target.SessionID + "' is not serving forwarded permission requests")
		}

		time.Sleep(PermissionForwardingPollIntervalMs * time.Millisecond)
	}

	LogPermissionForwardingWarning(p.logger, "Timed out waiting for forwarded permission response '"+responsePath+"'", nil)
	timedOut := newObject()
	timedOut.set("requestId", requestID)
	timedOut.set("requesterAgentName", requesterAgentName)
	timedOut.set("targetSessionId", targetSessionID)
	timedOut.set("responsePath", responsePath)
	p.logger.Review("forwarded_permission.response_timed_out", timedOut)
	p.discardRequest(location, requestPath, "")
	return abandon("Session '" + target.SessionID + "' did not answer within " + jsNumber(timeoutMs/1000) + "s")
}

// checkServingLiveness tracks how long the target has looked unserved, or -1 while it looks fine. Which channel can answer for this
// target is the judge's decision, not this one's: a target it cannot judge answers unknown, which resets the window exactly as
// "serving" does, so an unjudgeable target waits out the timeout.
func (p *ParentAuthorizer) checkServingLiveness(target PermissionForwardingTarget, unservedSince int64) int64 {
	if serving, known := p.serving.IsServing(target); known && !serving {
		if unservedSince >= 0 {
			return unservedSince
		}
		return time.Now().UnixMilli()
	}
	return -1
}

// discardRequest drops this exchange's files and, if nothing else is pending, its directories. Deleting the request is what makes
// an abandonment final: a request left behind would be answered by the parent long after the child gave up. An empty responsePath
// is none.
func (p *ParentAuthorizer) discardRequest(location PermissionForwardingLocation, requestPath, responsePath string) {
	if responsePath != "" {
		SafeDeleteFile(p.logger, responsePath, "forwarded permission response")
	}
	SafeDeleteFile(p.logger, requestPath, "forwarded permission request")
	CleanupPermissionForwardingLocationIfEmpty(p.logger, location)
}
