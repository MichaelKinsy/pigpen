package pi_permission_system

// The decision slice of src/service/permission-events.ts: the channel constant, the event, and the best-effort emit helper.
// (The ready and ui_prompt halves belong to the service lane.)

// PermissionsDecisionChannel is emitted after every permission gate resolution (PERMISSIONS_DECISION_CHANNEL).
const PermissionsDecisionChannel = "permissions:decision"

// PermissionEventBus is the minimal event bus the emit helpers need; the SDK's Context.Events() satisfies it. Payloads cross the
// bus as JSON.
type PermissionEventBus interface {
	Emit(channel string, data any) error
}

// PermissionDecisionResolution is how a permission decision was reached: policy_allow, policy_deny, session_approved,
// infrastructure_auto_allowed, user_approved, user_approved_for_session, user_denied, auto_approved, confirmation_unavailable,
// authorizer_allowed, authorizer_denied or gate_error.
type PermissionDecisionResolution string

// ForwardedPromptContext is the forwarding context of a prompt or decision.
type ForwardedPromptContext struct {
	RequesterAgentName *string `json:"requesterAgentName"`
	RequesterSessionID *string `json:"requesterSessionId"`
}

// PermissionDecisionEvent is the payload emitted on permissions:decision. Nil pointers are null.
type PermissionDecisionEvent struct {
	// RequestID identifies the permission request this decision resolves.
	RequestID      string                       `json:"requestId"`
	Surface        string                       `json:"surface"`
	Value          string                       `json:"value"`
	Result         string                       `json:"result"` // "allow" or "deny"
	Resolution     PermissionDecisionResolution `json:"resolution"`
	Origin         *string                      `json:"origin"`
	AgentName      *string                      `json:"agentName"`
	MatchedPattern *string                      `json:"matchedPattern"`
	// Forwarding is set for a decision made while serving another session's forwarded request; absent otherwise.
	Forwarding *ForwardedPromptContext `json:"forwarding,omitempty"`
}

// EmitDecisionEvent broadcasts a permissions:decision event. Best effort: a failing or panicking listener must not block the gate.
func EmitDecisionEvent(events PermissionEventBus, event PermissionDecisionEvent) {
	defer func() { _ = recover() }()
	_ = events.Emit(PermissionsDecisionChannel, event)
}
