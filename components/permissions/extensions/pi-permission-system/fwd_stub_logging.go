package pi_permission_system

// LANE-LOCAL COPY for the forwarding lane (PermForwarding). These declarations are owned by the logging lane
// (PermLogging: permissionevents.go, decisionreporter.go, sessionlogger.go, logging_deps.go) and are copied here with the
// same names and shapes only so this lane compiles on its own. At integration delete this file; nothing here is extended.

// ReviewLogger is the narrowest logging seam: consumers that only write review-log entries.
type ReviewLogger interface {
	Review(event string, details *jsObject)
}

// DebugLogger is the narrowest logging seam: consumers that only write debug-log entries.
type DebugLogger interface {
	Debug(event string, details *jsObject)
}

// DebugReviewLogger writes to both logs.
type DebugReviewLogger interface {
	ReviewLogger
	DebugLogger
}

// PermissionDecisionResolution is how a permission decision was reached.
type PermissionDecisionResolution string

// ForwardedPromptContext is the forwarding context of a prompt or decision.
type ForwardedPromptContext struct {
	RequesterAgentName *string `json:"requesterAgentName"`
	RequesterSessionID *string `json:"requesterSessionId"`
}

// PermissionDecisionEvent is the payload emitted on permissions:decision. Nil pointers are null.
type PermissionDecisionEvent struct {
	RequestID      string                       `json:"requestId"`
	Surface        string                       `json:"surface"`
	Value          string                       `json:"value"`
	Result         string                       `json:"result"` // "allow" or "deny"
	Resolution     PermissionDecisionResolution `json:"resolution"`
	Origin         *string                      `json:"origin"`
	AgentName      *string                      `json:"agentName"`
	MatchedPattern *string                      `json:"matchedPattern"`
	Forwarding     *ForwardedPromptContext      `json:"forwarding,omitempty"`
}

// DecisionBroadcaster announces a terminal outcome on the permissions:decision channel.
type DecisionBroadcaster interface {
	EmitDecision(event PermissionDecisionEvent)
}

// PromptPayload is the complete, structured description of a permission ask (ADR 0011 section 2).
type PromptPayload struct {
	// Kind is one of bash, mcp, tool, path, external_directory, bash_external_directory, skill, skill_read, forwarded.
	Kind        string
	Request     PromptRequestFacts
	Evidence    []PromptEvidence
	Annotations []PromptAnnotation
}

// PromptRequestFacts is the invariant core of an ask. A nil pointer is the original's null.
type PromptRequestFacts struct {
	Requester       PromptRequester
	Surface         string
	ToolName        *string
	InvokedToolName *string
	Value           string
	MatchedPattern  *string
	// CommandContext is command_substitution, process_substitution or subshell; nil for a current-shell command.
	CommandContext *string
	ExecutedUnit   *string
}

// PromptRequester is who is asking, one hop below when the ask was forwarded.
type PromptRequester struct {
	AgentName *string
	Forwarded bool
	SessionID *string
}

// PromptEvidence is one piece of decision evidence.
type PromptEvidence struct {
	Label, Text string
	Detail      *string
}

// PromptAnnotation is a model-generated advisory.
type PromptAnnotation struct{ Source, Text string }
