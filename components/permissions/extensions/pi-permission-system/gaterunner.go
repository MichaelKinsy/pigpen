package pi_permission_system

// Port of src/handlers/gates/runner.ts: executes the check, log, emit, approve cycle for one gate result.

// GateRunner executes permission gate checks for a single gate result (nil, bypass or descriptor). It is constructed once per handler
// with its four role collaborators and reused for every gate in a tool-call pipeline.
type GateRunner struct {
	resolver ScopedPermissionResolver
	recorder SessionApprovalRecorder
	prompter AskEscalator
	reporter DecisionReporter
	// isYoloEnabled is the live yolo reader, read per gate so a mid-session config change takes effect.
	isYoloEnabled func() bool
}

// NewGateRunner is `new GateRunner(resolver, recorder, prompter, reporter, isYoloEnabled)`.
func NewGateRunner(resolver ScopedPermissionResolver, recorder SessionApprovalRecorder, prompter AskEscalator, reporter DecisionReporter, isYoloEnabled func() bool) *GateRunner {
	return &GateRunner{resolver: resolver, recorder: recorder, prompter: prompter, reporter: reporter, isYoloEnabled: isYoloEnabled}
}

// Run executes a gate: nil allows; a bypass logs and emits then allows; a descriptor runs the full cycle. The request id is minted
// before the branch, so a request that never prompts is identified exactly as one that does. An error (a rejected escalation) or a
// panic propagates: the tool-call boundary turns either into a block.
func (r *GateRunner) Run(gate GateResult, agentName *string) (GateOutcome, error) {
	if gate == nil {
		return GateOutcome{Action: "allow"}, nil
	}
	requestID := CreatePermissionRequestId()
	if bypass, ok := gate.(*GateBypass); ok {
		if bypass.Log != nil {
			details := newObject()
			if bypass.Log.Details != nil {
				details = bypass.Log.Details.clone()
			}
			details.set("requestId", requestID)
			details.set("decidedBy", bypass.DecidedBy.JS())
			r.reporter.WriteReviewLog(bypass.Log.Event, details)
		}
		if bypass.Decision != nil {
			r.emitDecision(requestID, *bypass.Decision)
		}
		return GateOutcome{Action: "allow"}, nil
	}
	return r.runDescriptor(gate.(*GateDescriptor), agentName, requestID)
}

// emitDecision is the one place a decision event acquires its request id.
func (r *GateRunner) emitDecision(requestID string, f DecisionEventFacts) {
	r.reporter.EmitDecision(PermissionDecisionEvent{
		RequestID: requestID, Surface: f.Surface, Value: f.Value, Result: f.Result, Resolution: f.Resolution,
		Origin: f.Origin, AgentName: f.AgentName, MatchedPattern: f.MatchedPattern, Forwarding: f.Forwarding,
	})
}

func (r *GateRunner) runDescriptor(d *GateDescriptor, agentName *string, requestID string) (GateOutcome, error) {
	// 1. Resolve permission state: what the descriptor already carries, or via the resolver.
	var check PermissionCheckResult
	if pre := PreResolvedCheckOf(d); pre != nil {
		check = *pre
	} else {
		check = r.resolver.Resolve(ToolAccessIntent{Surface: d.Surface, Input: d.Input, AgentName: agentName})
	}

	// The fields every review-log write for this gate shares, built once so a field added here reaches all of them.
	logContext := newObject()
	if d.LogContext != nil {
		logContext = d.LogContext.clone()
	}
	facts := RenderReviewLogFacts(d.Payload)
	for _, k := range facts.keys {
		logContext.set(k, facts.vals[k])
	}
	logContext.set("agentName", strOrNull(agentName))
	logContext.set("requestId", requestID)

	// 2. Session-hit fast path.
	if check.Source == "session" {
		entry := logContext.clone()
		entry.set("resolution", "session_approved")
		entry.setOpt("sessionApprovalPattern", strOrNull(check.MatchedPattern))
		entry.set("decidedBy", DecisionSource{Kind: "session_approval", Surface: d.Surface, Pattern: check.MatchedPattern}.JS())
		r.reporter.WriteReviewLog("permission_request.session_approved", entry)
		r.emitDecision(requestID, BuildDecisionEvent(d.Decision, check, agentName, "allow", "session_approved"))
		return GateOutcome{Action: "allow"}, nil
	}

	// 2b. Yolo fast path: auto-approve without prompting, keeping the single auto_approved entry and decision event.
	if yoloGrant := ResolveYoloGrant(check, r.isYoloEnabled()); yoloGrant != nil {
		decidedByYolo := DecisionSource{Kind: "yolo", Pattern: check.MatchedPattern}
		entry := logContext.clone()
		entry.set("resolution", "auto_approved")
		entry.set("decidedBy", decidedByYolo.JS())
		r.reporter.WriteReviewLog("permission_request.auto_approved", entry)
		r.emitDecision(requestID, BuildDecisionEvent(d.Decision, *yoloGrant, agentName, "allow",
			ResolutionFor(decidedByYolo, DecisionOutcome{Approved: true, ForSession: false})))
		return GateOutcome{Action: "allow"}, nil
	}

	// 3. Apply the deny/ask/allow gate: always escalate on ask; the selected authorizer answers.
	payload := d.Payload
	messages := PermissionGateMessages{
		DenyReason: RenderPolicyDenial(payload, check.Reason, DefaultAgentRenderBudget),
		RefusedReason: func(decision PermissionPromptDecision) string {
			return RenderRefusal(payload, decision.DecidedBy, decision.DenialReason, DefaultAgentRenderBudget)
		},
	}
	// The rule that resolved this gate, and the decider for every arm that never escalates.
	var origin *string
	if check.Origin != "" {
		o := check.Origin
		origin = &o
	}
	decidedByRule := DecisionSource{Kind: "rule", Surface: d.Surface, Pattern: check.MatchedPattern, Origin: origin}
	gateResult, err := ApplyPermissionGate(PermissionGateParams{
		State:              check.State,
		CanGrantForSession: d.SessionApproval != nil && d.SessionApproval.IsRecordable(),
		PromptForApproval: func() (PermissionPromptDecision, error) {
			details := d.PromptDetails
			details.RequestID = requestID
			details.Payload = payload
			if d.SessionApproval != nil {
				forwarded := d.SessionApproval.ToForwardedData()
				details.SessionApproval = &forwarded
			}
			return r.prompter.Escalate(&details)
		},
		WriteLog:      func(event string, extra *jsObject) { r.reporter.WriteReviewLog(event, extra) },
		LogContext:    logContext,
		DecidedByRule: decidedByRule,
		Messages:      messages,
	})
	if err != nil {
		return GateOutcome{}, err
	}

	// 4. Whether a session approval was granted, and at what width.
	var sessionGrant *SessionGrant
	if gateResult.Action == "allow" {
		sessionGrant = gateResult.SessionGrant
	}

	// 5. Emit the decision event.
	result := "deny"
	if gateResult.Action == "allow" {
		result = "allow"
	}
	r.emitDecision(requestID, BuildDecisionEvent(d.Decision, check, agentName, result,
		ResolutionFor(gateResult.DecidedBy, DecisionOutcome{Approved: gateResult.Action == "allow", ForSession: sessionGrant != nil})))

	// 6. Record the session approval: tell the store; it owns the per-pattern loop.
	if sessionGrant != nil && d.SessionApproval != nil {
		r.recorder.RecordSessionApproval(d.SessionApproval.AtWidth(sessionGrant.Width))
	}

	if gateResult.Action == "block" {
		return GateOutcome{Action: "block", Reason: gateResult.Reason}, nil
	}
	return GateOutcome{Action: "allow"}, nil
}
