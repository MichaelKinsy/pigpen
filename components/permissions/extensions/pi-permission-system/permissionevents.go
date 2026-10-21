package pi_permission_system

// The decision and ready slices of src/service/permission-events.ts: the channel constants, the events, and the best-effort emit
// helpers. (The ui_prompt half is in uiprompt.go.)

// PermissionsReadyChannel is emitted at session_start after the emitting node published its service, and again at that node's first
// before_agent_start (ADR 0012 decision 3). It fires at least once per session and may repeat, so a handler must be idempotent.
const PermissionsReadyChannel = "permissions:ready"

// PermissionsReadyEvent is the payload emitted on permissions:ready: plain facts about the node that emitted it, never a live
// capability (the service itself is fetched with GetPermissionsService(sessionID)).
type PermissionsReadyEvent struct {
	// SessionID is the emitting node's session id, the key for GetPermissionsService; nil (null) when the host exposed none, in
	// which case this node published no keyed service.
	SessionID *string `json:"sessionId"`
	// AdjudicatesLocally is whether this node adjudicates its own asks (its authorizer chain runs) or relays them to a serving node.
	AdjudicatesLocally bool `json:"adjudicatesLocally"`
}

// EmitReadyEvent broadcasts permissions:ready. Best effort: a failing or panicking listener must not block session startup.
func EmitReadyEvent(events PermissionEventBus, event PermissionsReadyEvent) {
	defer func() { _ = recover() }()
	_ = events.Emit(PermissionsReadyChannel, event)
}

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
