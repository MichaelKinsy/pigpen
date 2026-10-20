package pi_permission_system

// LANE-LOCAL MINIMUM for the forwarding lane (PermForwarding): the authority-core, session and service types that the
// forwarding modules read or write, mirroring the original's names and shapes (cited per declaration). They are only what this
// lane's code and twins need. Whichever lane owns the real declaration replaces the matching one here; at integration delete the
// duplicates and keep the owner's.

// ── authority/decision-source.ts ───────────────────────────────────────────

// DecisionSource is what decided a permission request, recorded at the site that decided it. Kind selects which fields mean
// something: "user" (Via), "authorizer" (Name, Verdict, Reason), "rule" (Surface, Pattern, Origin), "session_approval" (Surface,
// Pattern), "yolo" (Pattern), "infrastructure_read", "unavailable" and "gate_error" (Reason, never nil), "forwarded"
// (ResponderSessionID, Decision). A nil pointer is the original's null.
type DecisionSource struct {
	Kind               string
	Via                string
	Name               string
	Verdict            string
	Reason             *string
	Surface            string
	Pattern            *string
	Origin             *string
	ResponderSessionID *string
	Decision           *DecisionSource
}

// MaxDecisionSourceDepth is MAX_DECISION_SOURCE_DEPTH.
const MaxDecisionSourceDepth = 4

// AsDecisionSource is asDecisionSource: narrow a decoded JSON value to a DecisionSource, or nil. All-or-nothing at every level.
func AsDecisionSource(v any) *DecisionSource { return narrowDecisionSource(v, MaxDecisionSourceDepth) }

func nullableString(o *jsObject, k string) (*string, bool) {
	v, ok := o.vals[k]
	if !ok {
		return nil, false
	}
	switch s := v.(type) {
	case nil:
		return nil, true
	case string:
		return &s, true
	}
	return nil, false
}

func narrowDecisionSource(v any, budget int) *DecisionSource {
	c, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	kind, _ := c.str("kind")
	if _, isStr := c.vals["kind"].(string); !isStr {
		return nil
	}
	switch kind {
	case "user":
		via, _ := c.str("via")
		if via == "dialog" || via == "select" {
			return &DecisionSource{Kind: "user", Via: via}
		}
	case "authorizer":
		verdict, _ := c.str("verdict")
		name, hasName := c.str("name")
		reason, okReason := nullableString(c, "reason")
		if (verdict == "allow" || verdict == "deny") && hasName && okReason {
			return &DecisionSource{Kind: "authorizer", Name: name, Verdict: verdict, Reason: reason}
		}
	case "rule":
		surface, hasSurface := c.str("surface")
		pattern, okP := nullableString(c, "pattern")
		origin, okO := nullableString(c, "origin")
		if hasSurface && okP && okO {
			return &DecisionSource{Kind: "rule", Surface: surface, Pattern: pattern, Origin: origin}
		}
	case "session_approval":
		surface, hasSurface := c.str("surface")
		pattern, okP := nullableString(c, "pattern")
		if hasSurface && okP {
			return &DecisionSource{Kind: "session_approval", Surface: surface, Pattern: pattern}
		}
	case "yolo":
		if pattern, okP := nullableString(c, "pattern"); okP {
			return &DecisionSource{Kind: "yolo", Pattern: pattern}
		}
	case "infrastructure_read":
		return &DecisionSource{Kind: "infrastructure_read"}
	case "unavailable", "gate_error":
		if reason, isStr := c.str("reason"); isStr {
			return &DecisionSource{Kind: kind, Reason: &reason}
		}
	case "forwarded":
		return narrowForwardedSource(c, budget)
	}
	return nil
}

// narrowForwardedSource narrows the inner decision against a decremented budget, so a chain deeper than MaxDecisionSourceDepth is
// rejected whole rather than truncated.
func narrowForwardedSource(c *jsObject, budget int) *DecisionSource {
	responder, okR := nullableString(c, "responderSessionId")
	if budget <= 0 || !okR {
		return nil
	}
	raw, present := c.vals["decision"]
	if present && raw == nil {
		return &DecisionSource{Kind: "forwarded", ResponderSessionID: responder}
	}
	inner := narrowDecisionSource(raw, budget-1)
	if inner == nil {
		return nil
	}
	return &DecisionSource{Kind: "forwarded", ResponderSessionID: responder, Decision: inner}
}

// EffectiveDecider is effectiveDecider: the innermost non-forwarded source, or the hop itself when the responder named none.
func EffectiveDecider(s DecisionSource) DecisionSource {
	decider := s
	for hop := 0; hop < MaxDecisionSourceDepth; hop++ {
		if decider.Kind != "forwarded" || decider.Decision == nil {
			return decider
		}
		decider = *decider.Decision
	}
	return decider
}

func strOrNull(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// JS is the source as the ordered object JSON.stringify writes (the key order of each variant in the original).
func (s DecisionSource) JS() *jsObject {
	o := newObject()
	o.set("kind", s.Kind)
	switch s.Kind {
	case "user":
		o.set("via", s.Via)
	case "authorizer":
		o.set("name", s.Name)
		o.set("verdict", s.Verdict)
		o.set("reason", strOrNull(s.Reason))
	case "rule":
		o.set("surface", s.Surface)
		o.set("pattern", strOrNull(s.Pattern))
		o.set("origin", strOrNull(s.Origin))
	case "session_approval":
		o.set("surface", s.Surface)
		o.set("pattern", strOrNull(s.Pattern))
	case "yolo":
		o.set("pattern", strOrNull(s.Pattern))
	case "unavailable", "gate_error":
		o.set("reason", strOrNull(s.Reason))
	case "forwarded":
		o.set("responderSessionId", strOrNull(s.ResponderSessionID))
		if s.Decision == nil {
			o.set("decision", nil)
		} else {
			o.set("decision", s.Decision.JS())
		}
	}
	return o
}

// ── authority/permission-dialog.ts ─────────────────────────────────────────

// PermissionDecisionState is the outcome of a permission prompt.
type PermissionDecisionState string

// The five decision states.
const (
	DecisionApproved               PermissionDecisionState = "approved"
	DecisionApprovedForSession     PermissionDecisionState = "approved_for_session"
	DecisionApprovedForServingSess PermissionDecisionState = "approved_for_serving_session"
	DecisionDenied                 PermissionDecisionState = "denied"
	DecisionDeniedWithReason       PermissionDecisionState = "denied_with_reason"
)

// IsPermissionDecisionState is isPermissionDecisionState.
func IsPermissionDecisionState(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	switch PermissionDecisionState(s) {
	case DecisionApproved, DecisionApprovedForSession, DecisionApprovedForServingSess, DecisionDenied, DecisionDeniedWithReason:
		return true
	}
	return false
}

// PermissionPromptDecision is the answer to an ask. DecidedBy is required; a nil DenialReason is undefined.
type PermissionPromptDecision struct {
	Approved                bool
	State                   PermissionDecisionState
	DenialReason            *string
	ConfirmationUnavailable bool
	// SessionGrantWidth is "" when the human chose no width (undefined).
	SessionGrantWidth SessionGrantWidth
	DecidedBy         DecisionSource
}

// NormalizePermissionDenialReason is normalizePermissionDenialReason for a string: trimmed, or nil when empty.
func NormalizePermissionDenialReason(v *string) *string {
	if v == nil {
		return nil
	}
	t := jsTrim(*v)
	if len(t) == 0 {
		return nil
	}
	return &t
}

// CreateDeniedPermissionDecision is createDeniedPermissionDecision: the decision before its decider is known (DecidedBy zero).
func CreateDeniedPermissionDecision(denialReason *string) PermissionPromptDecision {
	if reason := NormalizePermissionDenialReason(denialReason); reason != nil {
		return PermissionPromptDecision{Approved: false, State: DecisionDeniedWithReason, DenialReason: reason}
	}
	return PermissionPromptDecision{Approved: false, State: DecisionDenied}
}

// ── authority/decision-resolution.ts ───────────────────────────────────────

// DecisionOutcome is what became of the request, as the gate that ran it observed.
type DecisionOutcome struct{ Approved, ForSession bool }

// ResolutionFor is resolutionFor: name how a decision was reached, from the decider stamped on it.
func ResolutionFor(decidedBy DecisionSource, outcome DecisionOutcome) PermissionDecisionResolution {
	decider := EffectiveDecider(decidedBy)
	switch decider.Kind {
	case "rule":
		if outcome.Approved {
			return "policy_allow"
		}
		return "policy_deny"
	case "session_approval":
		return "session_approved"
	case "infrastructure_read":
		return "infrastructure_auto_allowed"
	case "yolo":
		return "auto_approved"
	case "authorizer":
		if outcome.Approved {
			return "authorizer_allowed"
		}
		return "authorizer_denied"
	case "unavailable":
		return "confirmation_unavailable"
	case "gate_error":
		return "gate_error"
	}
	// "user", or a "forwarded" frame whose responder named no decider (an older parent).
	if !outcome.Approved {
		return "user_denied"
	}
	if outcome.ForSession {
		return "user_approved_for_session"
	}
	return "user_approved"
}

// ── authority/permission-prompter.ts ───────────────────────────────────────

// ForwardedAskProvenance is who forwarded an ask.
type ForwardedAskProvenance struct{ RequesterAgentName, RequesterSessionID *string }

// PromptPermissionDetails are the details passed when prompting for a permission decision. A nil pointer is undefined (Surface
// and Value: undefined or null, which no reader here tells apart except through the serving node, which always sets them).
type PromptPermissionDetails struct {
	RequestID        string
	Source           string
	AgentName        *string
	Payload          PromptPayload
	ToolCallID       *string
	ToolName         *string
	SkillName        *string
	Path             *string
	Command          *string
	Target           *string
	ToolInputPreview *string
	SessionLabel     *string
	Surface          *string
	Value            *string
	Forwarding       *ForwardedAskProvenance
	SessionApproval  *ForwardedSessionApproval
	AccessIntent     *ForwardedAccessFacts
}

// AskEscalator is authorizer-selection.ts AskEscalator: escalate an ask to the selected authorizer. An error is the original's
// rejection.
type AskEscalator interface {
	Escalate(details *PromptPermissionDetails) (PermissionPromptDecision, error)
}

// ── authority/authorizer.ts, service.ts ────────────────────────────────────

// AuthorizerVerdict is a link's verdict: Kind "allow", "deny" (with Reason) or "defer".
type AuthorizerVerdict struct {
	Kind   string
	Reason *string
}

// PermissionQuery is the injected read-only query seam of an authorizer link (service.ts PermissionQuery).
type PermissionQuery interface{}

// AuthorizerLog is the review-log seam of an authorizer link (service.ts AuthorizerLog).
type AuthorizerLog interface{}

// AuthorizeFunc is Authorizer["authorize"].
type AuthorizeFunc func(details *PromptPermissionDetails, query PermissionQuery, log AuthorizerLog) (AuthorizerVerdict, error)

// ── types.ts ───────────────────────────────────────────────────────────────

// ── tool-input registries and service.ts ───────────────────────────────────

// ToolInputFormatter is a tool's preview formatter (tool-input-formatter-registry.ts), compared by identity like an extractor.
type ToolInputFormatter interface {
	Format(input *jsObject) (string, bool)
}

// ToolInputFormatterLookup is the read side of the formatter registry.
type ToolInputFormatterLookup interface {
	Get(toolName string) ToolInputFormatter
}

// ToolAccessExtractorRegistry is the node-local extractor registry.
type ToolAccessExtractorRegistry struct {
	extractors map[string]ToolAccessExtractor
}

// Register is register: one extractor per tool; the returned function unregisters it if it is still the registered one.
func (r *ToolAccessExtractorRegistry) Register(toolName string, e ToolAccessExtractor) (func(), error) {
	if r.extractors == nil {
		r.extractors = map[string]ToolAccessExtractor{}
	}
	if _, dup := r.extractors[toolName]; dup {
		return nil, errAlreadyRegistered("tool access extractor", toolName)
	}
	r.extractors[toolName] = e
	return func() {
		if r.extractors[toolName] == e {
			delete(r.extractors, toolName)
		}
	}, nil
}

// Resolve answers a local registration.
func (r *ToolAccessExtractorRegistry) Resolve(toolName string) *ResolvedToolAccessExtractor {
	if e, ok := r.extractors[toolName]; ok && e != nil {
		return &ResolvedToolAccessExtractor{Extractor: e, Origin: RegistrationLocal}
	}
	return nil
}

// ToolInputFormatterRegistry is the node-local formatter registry.
type ToolInputFormatterRegistry struct{ formatters map[string]ToolInputFormatter }

// Register is register: one formatter per tool.
func (r *ToolInputFormatterRegistry) Register(toolName string, f ToolInputFormatter) (func(), error) {
	if r.formatters == nil {
		r.formatters = map[string]ToolInputFormatter{}
	}
	if _, dup := r.formatters[toolName]; dup {
		return nil, errAlreadyRegistered("tool input formatter", toolName)
	}
	r.formatters[toolName] = f
	return func() {
		if r.formatters[toolName] == f {
			delete(r.formatters, toolName)
		}
	}, nil
}

// Get answers the registered formatter, or nil.
func (r *ToolInputFormatterRegistry) Get(toolName string) ToolInputFormatter {
	return r.formatters[toolName]
}

type registrationError struct{ what, tool string }

func (e registrationError) Error() string {
	return "A " + e.what + " is already registered for '" + e.tool + "'."
}

func errAlreadyRegistered(what, tool string) error { return registrationError{what, tool} }

// PermissionsService is the part of service.ts PermissionsService the cross-node lookups read.
type PermissionsService interface {
	GetToolAccessExtractor(toolName string) ToolAccessExtractor
	GetToolInputFormatter(toolName string) ToolInputFormatter
}
