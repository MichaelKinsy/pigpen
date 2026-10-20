package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Port of src/authority/permission-forwarding.ts: the forwarding constants, the wire types of a forwarded request and its
// response, the on-disk location of a session's inbox, and target resolution.

// Forwarding timing (permission-forwarding.ts:10-21). Milliseconds, as in the original.
const (
	PermissionForwardingPollIntervalMs = 250
	PermissionForwardingTimeoutMs      = 10 * 60 * 1000
	// PermissionForwardingServingGraceMs is how long an in-process forwarding target may go unserved before the child gives up on it:
	// eight poll ticks.
	PermissionForwardingServingGraceMs = 8 * PermissionForwardingPollIntervalMs
)

// SubagentParentSessionEnvCandidates are the env var names checked for the parent session id; the first match wins.
var SubagentParentSessionEnvCandidates = []string{
	// pi-agent-router (original)
	"PI_AGENT_ROUTER_PARENT_SESSION_ID",
	// Shared convention for CLI-based subagent extensions (nicobailon/pi-subagents, HazAT/pi-interactive-subagents, etc.)
	"PI_SUBAGENT_PARENT_SESSION",
}

// thirdPartySubagentEnvHints are the per-extension markers set by known process-based subagent extensions.
var thirdPartySubagentEnvHints = []string{
	// pi-agent-router (original)
	"PI_IS_SUBAGENT", "PI_SUBAGENT_SESSION_ID", "PI_AGENT_ROUTER_SUBAGENT",
	// nicobailon/pi-subagents
	"PI_SUBAGENT_CHILD", "PI_SUBAGENT_RUN_ID", "PI_SUBAGENT_CHILD_AGENT", "PI_SUBAGENT_DEPTH",
	// HazAT/pi-interactive-subagents
	"PI_SUBAGENT_NAME", "PI_SUBAGENT_ID", "PI_SUBAGENT_SESSION", "PI_SUBAGENT_ACTIVITY_FILE",
}

// SubagentEnvHintKeys are the env vars whose presence marks the current process as a subagent child: the per-extension markers
// followed by every parent-session candidate (a process that names a parent session is a child by definition).
var SubagentEnvHintKeys = append(append([]string(nil), thirdPartySubagentEnvHints...), SubagentParentSessionEnvCandidates...)

// SubagentParentSessionEnvKey is the deprecated alias of the first parent-session candidate.
var SubagentParentSessionEnvKey = SubagentParentSessionEnvCandidates[0]

const (
	sessionForwardingRootDirectoryName      = "sessions"
	sessionForwardingRequestsDirectoryName  = "requests"
	sessionForwardingResponsesDirectoryName = "responses"
)

// DisplayString is a `string | null | undefined` display field of a forwarded request: Set false is undefined, Set true with a
// nil Value is null.
type DisplayString struct {
	Set   bool
	Value *string
}

// DisplayStr is a display field holding s.
func DisplayStr(s string) DisplayString { return DisplayString{Set: true, Value: &s} }

// DisplayNull is a display field holding null.
var DisplayNull = DisplayString{Set: true}

// ForwardedSessionApproval is the child's session-approval suggestion, relayed to the serving node (a plain data shape).
type ForwardedSessionApproval struct{ Grants []ApprovalGrant }

// ForwardedAccessFacts are the child-fixed facts a gate emits: the surface it evaluated and the match set it computed. Strings
// only: an AccessPath never crosses onto the wire.
type ForwardedAccessFacts struct {
	Surface     string
	MatchValues []string
	// BoundaryValue is the canonical boundary of a path surface; nil (null) for a non-path surface.
	BoundaryValue *string
}

// ForwardedAccessIntent is the forwarded-wire access intent (ADR 0008 section 2): the child-fixed facts plus the requester
// identity the escalation edge stamps.
type ForwardedAccessIntent struct {
	ForwardedAccessFacts
	// RequesterCwd is the requester's cwd, for provenance only.
	RequesterCwd string
	Principal    ForwardedPrincipal
}

// ForwardedPrincipal is who is requesting.
type ForwardedPrincipal struct{ SessionID, AgentName string }

// ForwardedPermissionRequest is a request file a forwarding child writes into its parent's inbox. A nil pointer or empty Source
// is an absent (undefined) optional field.
type ForwardedPermissionRequest struct {
	ID                 string
	CreatedAt          float64
	RequesterSessionID string
	TargetSessionID    string
	RequesterAgentName string
	Payload            *PromptPayload
	// Source is a PermissionUiPromptSource (tool_call, skill_input, skill_read), "" when absent.
	Source          string
	Surface         DisplayString
	Value           DisplayString
	SessionApproval *ForwardedSessionApproval
	AccessIntent    *ForwardedAccessIntent
}

// ForwardedPermissionResponse is the response file the serving node writes. A nil DenialReason, empty SessionGrantWidth or nil
// DecidedBy is absent (undefined).
type ForwardedPermissionResponse struct {
	Approved           bool
	State              PermissionDecisionState
	DenialReason       *string
	ResponderSessionID string
	RespondedAt        float64
	DecidedBy          *DecisionSource
	SessionGrantWidth  SessionGrantWidth
}

// PermissionForwardingLocation is where one session's forwarded requests and responses live.
type PermissionForwardingLocation struct {
	SessionID      string
	SessionRootDir string
	RequestsDir    string
	ResponsesDir   string
	Label          string // always "primary"
}

// NormalizePermissionForwardingSessionID is normalizePermissionForwardingSessionId: the trimmed id, or false for an empty one or the
// "unknown" sentinel.
func NormalizePermissionForwardingSessionID(value string) (string, bool) {
	trimmed := jsTrim(value)
	if trimmed == "" || strings.ToLower(trimmed) == "unknown" {
		return "", false
	}
	return trimmed, true
}

// normalizeOptionalSessionID is the same for a value that may be undefined or null.
func normalizeOptionalSessionID(value *string) (string, bool) {
	if value == nil {
		return "", false
	}
	return NormalizePermissionForwardingSessionID(*value)
}

// EncodeSessionIDForPath is encodeSessionIdForPath: encodeURIComponent, so a session id names one path segment.
func EncodeSessionIDForPath(sessionID string) string { return encodeURIComponent(sessionID) }

const uriHex = "0123456789ABCDEF"

// encodeURIComponent is the JavaScript global: every UTF-8 byte but A-Z a-z 0-9 - _ . ! ~ * ' ( ) becomes %XX.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\uFFFD' {
			// A Go string may hold invalid UTF-8, which range yields as U+FFFD; JavaScript strings cannot, and encodeURIComponent of
			// the replacement character is the same three bytes.
			b.WriteString("%EF%BF%BD")
			continue
		}
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.!~*'()", r)) {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			b.WriteByte('%')
			b.WriteByte(uriHex[c>>4])
			b.WriteByte(uriHex[c&15])
		}
	}
	return b.String()
}

// ErrForwardingSessionID is the error createPermissionForwardingLocation throws for an unusable session id.
var ErrForwardingSessionID = errors.New("Permission forwarding session id must be a non-empty string.")

// CreatePermissionForwardingLocation is createPermissionForwardingLocation.
func CreatePermissionForwardingLocation(forwardingRootDir, sessionID string) (PermissionForwardingLocation, error) {
	normalized, ok := NormalizePermissionForwardingSessionID(sessionID)
	if !ok {
		return PermissionForwardingLocation{}, ErrForwardingSessionID
	}
	sessionRootDir := filepath.Join(forwardingRootDir, sessionForwardingRootDirectoryName, EncodeSessionIDForPath(normalized))
	return PermissionForwardingLocation{
		SessionID:      normalized,
		SessionRootDir: sessionRootDir,
		RequestsDir:    filepath.Join(sessionRootDir, sessionForwardingRequestsDirectoryName),
		ResponsesDir:   filepath.Join(sessionRootDir, sessionForwardingResponsesDirectoryName),
		Label:          "primary",
	}, nil
}

// PermissionForwardingTargetSource is how a forwarding target was resolved: "registry" (the requester is an in-process child of
// the session, so it may consult the serving-session registry) or "env" (the target lives in another process).
type PermissionForwardingTargetSource string

// The two ways a target is found.
const (
	TargetSourceRegistry PermissionForwardingTargetSource = "registry"
	TargetSourceEnv      PermissionForwardingTargetSource = "env"
)

// PermissionForwardingTarget is the resolved forwarding target together with how it was found.
type PermissionForwardingTarget struct {
	SessionID string
	Source    PermissionForwardingTargetSource
}

// ResolveTargetOptions are the inputs of ResolvePermissionForwardingTarget.
type ResolveTargetOptions struct {
	IsSubagent       bool
	CurrentSessionID string
	// Env is the environment to read; nil reads the process environment.
	Env map[string]string
	// SessionID is the child session id for the registry lookup.
	SessionID string
	// Registry is the in-process subagent session registry, checked before env vars.
	Registry ParentChainRegistry
}

// ResolvePermissionForwardingTarget is resolvePermissionForwardingTarget: the session this node relays its asks to, or false when
// it has none. It answers only "which other session", never "myself" (#907).
func ResolvePermissionForwardingTarget(o ResolveTargetOptions) (PermissionForwardingTarget, bool) {
	if !o.IsSubagent {
		return PermissionForwardingTarget{}, false
	}
	own, hasOwn := NormalizePermissionForwardingSessionID(o.CurrentSessionID)
	namesAnotherSession := func(candidate string) bool { return !hasOwn || candidate != own }

	// 1. Registry: in-process subagents register parentSessionId explicitly.
	if o.Registry != nil && o.SessionID != "" {
		if entry, ok := o.Registry.Get(o.SessionID); ok {
			if resolved, ok := normalizeOptionalSessionID(entry.ParentSessionID); ok && namesAnotherSession(resolved) {
				return PermissionForwardingTarget{SessionID: resolved, Source: TargetSourceRegistry}, true
			}
		}
	}

	// 2. Env vars: process-based subagent extensions.
	for _, key := range SubagentParentSessionEnvCandidates {
		var raw string
		if o.Env != nil {
			raw = o.Env[key]
		} else {
			raw = os.Getenv(key)
		}
		if resolved, ok := NormalizePermissionForwardingSessionID(raw); ok && namesAnotherSession(resolved) {
			return PermissionForwardingTarget{SessionID: resolved, Source: TargetSourceEnv}, true
		}
	}
	return PermissionForwardingTarget{}, false
}

// IsForwardedPermissionRequestForSession is isForwardedPermissionRequestForSession: whether the request targets sessionID.
func IsForwardedPermissionRequestForSession(targetSessionID, sessionID string) bool {
	requestID, okRequest := NormalizePermissionForwardingSessionID(targetSessionID)
	current, okCurrent := NormalizePermissionForwardingSessionID(sessionID)
	return okRequest && okCurrent && requestID == current
}

// ── wire forms ─────────────────────────────────────────────────────────────

func (d DisplayString) js() any {
	if d.Value == nil {
		return nil
	}
	return *d.Value
}

func (a ForwardedAccessFacts) js() *jsObject {
	values := make([]any, len(a.MatchValues))
	for i, v := range a.MatchValues {
		values[i] = v
	}
	o := newObject()
	o.set("surface", a.Surface)
	o.set("matchValues", values)
	o.set("boundaryValue", strOrNull(a.BoundaryValue))
	return o
}

// JS is the intent as the ordered object JSON.stringify writes.
func (a ForwardedAccessIntent) JS() *jsObject {
	o := a.ForwardedAccessFacts.js()
	o.set("requesterCwd", a.RequesterCwd)
	principal := newObject()
	principal.set("sessionId", a.Principal.SessionID)
	principal.set("agentName", a.Principal.AgentName)
	o.set("principal", principal)
	return o
}

// JS is the request as the ordered object JSON.stringify writes: undefined fields are left out.
func (r ForwardedPermissionRequest) JS() *jsObject {
	o := newObject()
	o.set("id", r.ID)
	o.set("createdAt", r.CreatedAt)
	o.set("requesterSessionId", r.RequesterSessionID)
	o.set("targetSessionId", r.TargetSessionID)
	o.set("requesterAgentName", r.RequesterAgentName)
	if r.Payload != nil {
		o.set("payload", r.Payload.JS())
	}
	if r.Source != "" {
		o.set("source", r.Source)
	}
	if r.Surface.Set {
		o.set("surface", r.Surface.js())
	}
	if r.Value.Set {
		o.set("value", r.Value.js())
	}
	if r.SessionApproval != nil {
		grants := make([]any, len(r.SessionApproval.Grants))
		for i, g := range r.SessionApproval.Grants {
			grant := newObject()
			grant.set("surface", g.Surface)
			grant.set("pattern", g.Pattern)
			grants[i] = grant
		}
		sa := newObject()
		sa.set("grants", grants)
		o.set("sessionApproval", sa)
	}
	if r.AccessIntent != nil {
		o.set("accessIntent", r.AccessIntent.JS())
	}
	return o
}

// JS is the response as the ordered object JSON.stringify writes: undefined fields are left out.
func (r ForwardedPermissionResponse) JS() *jsObject {
	o := newObject()
	o.set("approved", r.Approved)
	o.set("state", string(r.State))
	if r.DenialReason != nil {
		o.set("denialReason", *r.DenialReason)
	}
	o.set("responderSessionId", r.ResponderSessionID)
	o.set("respondedAt", r.RespondedAt)
	if r.DecidedBy != nil {
		o.set("decidedBy", r.DecidedBy.JS())
	}
	if r.SessionGrantWidth != "" {
		o.set("sessionGrantWidth", string(r.SessionGrantWidth))
	}
	return o
}
