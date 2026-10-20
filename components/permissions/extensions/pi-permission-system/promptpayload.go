package pi_permission_system

// Port of src/presentation/prompt-payload.ts: the complete, structured description of a permission ask, the constructor of a local
// requester, the tolerant reader (asPromptPayload) and the wire form the forwarded request carries.

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

// LocalRequester is localRequester: the requester facts for an ask raised by this session.
func LocalRequester(agentName *string) PromptRequester {
	return PromptRequester{AgentName: agentName}
}

var promptPayloadKinds = []string{"bash", "mcp", "tool", "path", "external_directory", "bash_external_directory", "skill", "skill_read", "forwarded"}

var bashCommandContexts = []string{"command_substitution", "process_substitution", "subshell"}

func inStrings(set []string, s string) bool {
	for _, m := range set {
		if m == s {
			return true
		}
	}
	return false
}

// AsPromptPayload is asPromptPayload: narrow a decoded JSON value (parseJSON output) to a PromptPayload, or nil. All-or-nothing:
// any malformed field yields nil rather than a half-payload (ADR 0011 section 9).
func AsPromptPayload(value any) *PromptPayload {
	c, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	kind, isKind := c.str("kind")
	if !isKind || !inStrings(promptPayloadKinds, kind) {
		return nil
	}
	request := asPromptRequestFacts(c.vals["request"])
	evidenceRaw, evOK := c.vals["evidence"].([]any)
	annotationsRaw, anOK := c.vals["annotations"].([]any)
	if request == nil || !evOK || !anOK {
		return nil
	}
	evidence := make([]PromptEvidence, 0, len(evidenceRaw))
	for _, e := range evidenceRaw {
		n, ok := asPromptEvidence(e)
		if !ok {
			return nil
		}
		evidence = append(evidence, n)
	}
	annotations := make([]PromptAnnotation, 0, len(annotationsRaw))
	for _, a := range annotationsRaw {
		n, ok := asPromptAnnotation(a)
		if !ok {
			return nil
		}
		annotations = append(annotations, n)
	}
	return &PromptPayload{Kind: kind, Request: *request, Evidence: evidence, Annotations: annotations}
}

func asPromptRequestFacts(value any) *PromptRequestFacts {
	c, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	requester, okRequester := asPromptRequester(c.vals["requester"])
	surface, okSurface := c.str("surface")
	val, okValue := c.str("value")
	toolName, okT := nullableString(c, "toolName")
	invoked, okI := nullableString(c, "invokedToolName")
	matched, okM := nullableString(c, "matchedPattern")
	executed, okE := nullableString(c, "executedUnit")
	// asNullableMember: null or a member of the context set; anything else (even undefined) is malformed.
	commandContext, okC := nullableString(c, "commandContext")
	if okC && commandContext != nil && !inStrings(bashCommandContexts, *commandContext) {
		okC = false
	}
	if !okRequester || !okC || !okSurface || !okValue || !okT || !okI || !okM || !okE {
		return nil
	}
	return &PromptRequestFacts{
		Requester: requester, Surface: surface, ToolName: toolName, InvokedToolName: invoked, Value: val,
		MatchedPattern: matched, CommandContext: commandContext, ExecutedUnit: executed,
	}
}

func asPromptRequester(value any) (PromptRequester, bool) {
	c, ok := value.(*jsObject)
	if !ok {
		return PromptRequester{}, false
	}
	forwarded, okF := c.flag("forwarded")
	agent, okA := nullableString(c, "agentName")
	session, okS := nullableString(c, "sessionId")
	if !okF || !okA || !okS {
		return PromptRequester{}, false
	}
	return PromptRequester{AgentName: agent, Forwarded: forwarded, SessionID: session}, true
}

func asPromptEvidence(value any) (PromptEvidence, bool) {
	c, ok := value.(*jsObject)
	if !ok {
		return PromptEvidence{}, false
	}
	label, okL := c.str("label")
	text, okT := c.str("text")
	detail, okD := nullableString(c, "detail")
	if !okL || !okT || !okD {
		return PromptEvidence{}, false
	}
	return PromptEvidence{Label: label, Text: text, Detail: detail}, true
}

func asPromptAnnotation(value any) (PromptAnnotation, bool) {
	c, ok := value.(*jsObject)
	if !ok {
		return PromptAnnotation{}, false
	}
	source, okS := c.str("source")
	text, okT := c.str("text")
	if !okS || !okT {
		return PromptAnnotation{}, false
	}
	return PromptAnnotation{Source: source, Text: text}, true
}

// JS is the payload as the ordered object JSON.stringify writes: the field order of the original's interfaces.
func (p PromptPayload) JS() *jsObject {
	requester := newObject()
	requester.set("agentName", strOrNull(p.Request.Requester.AgentName))
	requester.set("forwarded", p.Request.Requester.Forwarded)
	requester.set("sessionId", strOrNull(p.Request.Requester.SessionID))
	request := newObject()
	request.set("requester", requester)
	request.set("surface", p.Request.Surface)
	request.set("toolName", strOrNull(p.Request.ToolName))
	request.set("invokedToolName", strOrNull(p.Request.InvokedToolName))
	request.set("value", p.Request.Value)
	request.set("matchedPattern", strOrNull(p.Request.MatchedPattern))
	request.set("commandContext", strOrNull(p.Request.CommandContext))
	request.set("executedUnit", strOrNull(p.Request.ExecutedUnit))
	evidence := make([]any, len(p.Evidence))
	for i, e := range p.Evidence {
		o := newObject()
		o.set("label", e.Label)
		o.set("text", e.Text)
		o.set("detail", strOrNull(e.Detail))
		evidence[i] = o
	}
	annotations := make([]any, len(p.Annotations))
	for i, a := range p.Annotations {
		o := newObject()
		o.set("source", a.Source)
		o.set("text", a.Text)
		annotations[i] = o
	}
	out := newObject()
	out.set("kind", p.Kind)
	out.set("request", request)
	out.set("evidence", evidence)
	out.set("annotations", annotations)
	return out
}

// FindEvidence is findEvidence: the first evidence entry carrying the label, or nil.
func FindEvidence(payload PromptPayload, label string) *PromptEvidence {
	for i := range payload.Evidence {
		if payload.Evidence[i].Label == label {
			return &payload.Evidence[i]
		}
	}
	return nil
}

// AllEvidence is allEvidence: every evidence entry carrying the label, in payload order.
func AllEvidence(payload PromptPayload, label string) []PromptEvidence {
	out := []PromptEvidence{}
	for _, e := range payload.Evidence {
		if e.Label == label {
			out = append(out, e)
		}
	}
	return out
}
