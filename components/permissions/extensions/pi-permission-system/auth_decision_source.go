package pi_permission_system

// Port of src/authority/decision-source.ts.

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
