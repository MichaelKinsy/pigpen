package pi_permission_system

// Port of src/handlers/gates/descriptor.ts: the pure output of a gate function, the early-allow bypass, and the readers the runner and the
// pipeline's pre-emption share. A GateResult is the original's `GateDescriptor | GateBypass | null`: a *GateDescriptor, a *GateBypass or nil.

// GateDecision is the surface and value of the decision event (may differ from the check surface).
type GateDecision struct{ Surface, Value string }

// GatePreResolved is a permission state the gate already resolved (a skill entry match); the runner uses it instead of a check.
type GatePreResolved struct{ State string }

// GateDescriptor is the pure output of a gate function: what to check and how to present it. The gate runner uses it to run the
// check, log, emit, approve cycle without the gate knowing about logging, event emission or session-rule recording.
type GateDescriptor struct {
	// Surface is the permission surface to check (for example "bash", "external_directory", "skill").
	Surface string
	// Input is passed to the resolver when the descriptor carries no check of its own.
	Input any
	// Payload is the complete structured description of this ask (ADR 0011 section 2): every render over it reads this and nothing else.
	Payload PromptPayload
	// SessionApproval is the "for this session" suggestion; nil when the gate offers none.
	SessionApproval *SessionApproval
	// PromptDetails are passed to the interactive prompt. The runner stamps RequestID and Payload; a gate supplies neither.
	PromptDetails PromptPermissionDetails
	// LogContext is extra context written to the review log alongside gate outcomes; nil is empty.
	LogContext *jsObject
	// Decision is the surface and value for the decision event.
	Decision GateDecision
	// PreResolved, when set, is the state the gate already resolved.
	PreResolved *GatePreResolved
	// PreCheck, when set, is a pre-computed check result the runner uses instead of resolving.
	PreCheck *PermissionCheckResult
}

// DecisionEventFacts is a decision event's facts before the runner stamps the request id it minted (Omit<PermissionDecisionEvent,
// "requestId">).
type DecisionEventFacts struct {
	Surface        string
	Value          string
	Result         string
	Resolution     PermissionDecisionResolution
	Origin         *string
	AgentName      *string
	MatchedPattern *string
	Forwarding     *ForwardedPromptContext
}

// GateBypassLog is the review-log entry a bypass emits.
type GateBypassLog struct {
	Event   string
	Details *jsObject
}

// GateBypass is an early allow: the gate determined the action without the runner (the Pi infrastructure read bypass).
type GateBypass struct {
	// DecidedBy is what decided this short-circuit: the bypassing gate is the decider and states its own provenance (#726).
	DecidedBy DecisionSource
	// Log is the optional review log entry to emit.
	Log *GateBypassLog
	// Decision is the optional decision event to emit.
	Decision *DecisionEventFacts
}

// GateResult is a *GateDescriptor, a *GateBypass or nil (the gate does not apply).
type GateResult interface{ gateResult() }

func (*GateDescriptor) gateResult() {}
func (*GateBypass) gateResult()     {}

// PreResolvedCheckOf is the permission check a descriptor already carries, or nil when it resolves nothing of its own. Every tool-call
// gate resolves its own state before the runner sees it; this is the one place that precedence is expressed. A nil answer is not
// "allow": the caller must resolve the descriptor itself.
func PreResolvedCheckOf(d *GateDescriptor) *PermissionCheckResult {
	if d.PreCheck != nil {
		return d.PreCheck
	}
	if d.PreResolved != nil {
		return &PermissionCheckResult{State: d.PreResolved.State, ToolName: d.Surface, Source: "tool", Origin: "builtin"}
	}
	return nil
}

// IsUnconditionalDeny reports whether this gate blocks without escalating, whatever the other gates say. A deny is absorbing, so
// running it first is an ordering change, not a semantic one; the same treatment is not extended to ask (#915). A session-sourced
// check is allowed by the runner, so it is not pre-emptive here.
func IsUnconditionalDeny(gate GateResult) bool {
	d, ok := gate.(*GateDescriptor)
	if !ok {
		return false
	}
	check := PreResolvedCheckOf(d)
	return check != nil && check.State == "deny" && check.Source != "session"
}

// OrderDenyFirst is the gates in run order with any unconditional deny moved to the front: a stable partition, so two denying gates
// keep their relative order. With no deny present the slice is returned unchanged.
func OrderDenyFirst(gates []GateResult) []GateResult {
	var denying, rest []GateResult
	for _, g := range gates {
		if IsUnconditionalDeny(g) {
			denying = append(denying, g)
		} else {
			rest = append(rest, g)
		}
	}
	if len(denying) == 0 {
		return gates
	}
	return append(denying, rest...)
}

// IsGateBypass reports an early allow.
func IsGateBypass(result GateResult) bool {
	_, ok := result.(*GateBypass)
	return ok
}

// IsGateDescriptor reports a descriptor that needs the runner.
func IsGateDescriptor(result GateResult) bool {
	_, ok := result.(*GateDescriptor)
	return ok
}
