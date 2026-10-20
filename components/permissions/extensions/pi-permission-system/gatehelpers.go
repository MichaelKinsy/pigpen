package pi_permission_system

// Port of src/handlers/gates/helpers.ts: the fact builders the gates share and the yolo grant reader.

// PathGateRequestFacts are the identity fields every path-shaped tool gate reports about its call (Pick<ToolCallContext, "toolCallId" |
// "toolName" | "agentName">): who asked and which call it was, never the raw input or the cwd.
type PathGateRequestFacts struct {
	ToolCallID string
	ToolName   string
	AgentName  *string
}

// RequestFacts is the call's identity facts.
func (t ToolCallContext) RequestFacts() PathGateRequestFacts {
	return PathGateRequestFacts{ToolCallID: t.ToolCallID, ToolName: t.ToolName, AgentName: t.AgentName}
}

// BuildPathGateLogContext builds the review-log context for a path-shaped tool gate: the `path` and `external_directory` gates report
// the same five facts about a call. The request facts and the request id are stamped by the runner. pathSource adds `extractorSource`
// only when the path came from an inherited extractor.
func BuildPathGateLogContext(tcc PathGateRequestFacts, path string, pathSource ToolPathSource) *jsObject {
	o := newObject()
	o.set("source", "tool_call")
	o.set("toolCallId", tcc.ToolCallID)
	o.set("toolName", tcc.ToolName)
	o.set("agentName", strOrNull(tcc.AgentName))
	o.set("path", path)
	if pathSource == ToolPathInheritedExtractor {
		o.set("extractorSource", "inherited")
	}
	return o
}

// BuildPathGatePromptDetails builds the prompt details for a path-shaped tool gate: the same five facts plus the child-fixed access
// facts the ask carries onto the wire.
func BuildPathGatePromptDetails(tcc PathGateRequestFacts, path string, accessIntent ForwardedAccessFacts) PromptPermissionDetails {
	toolCallID, toolName := tcc.ToolCallID, tcc.ToolName
	return PromptPermissionDetails{
		Source:       "tool_call",
		AgentName:    tcc.AgentName,
		ToolCallID:   &toolCallID,
		ToolName:     &toolName,
		Path:         &path,
		AccessIntent: &accessIntent,
	}
}

// AccessFactsFromPath builds the child-fixed access facts for a path-shaped gate from its AccessPath: strings at the point of emission
// (ADR-0002), carrying the lexical and canonical match set. An empty boundary value (a literal-only path) becomes nil.
func AccessFactsFromPath(surface string, path *AccessPath) ForwardedAccessFacts {
	facts := ForwardedAccessFacts{Surface: surface, MatchValues: path.MatchValues()}
	if boundary := path.BoundaryValue(); boundary != "" {
		facts.BoundaryValue = &boundary
	}
	return facts
}

// AccessFactsFromValue builds the child-fixed access facts for a non-path gate from its already-portable single value.
func AccessFactsFromValue(surface, value string) ForwardedAccessFacts {
	return ForwardedAccessFacts{Surface: surface, MatchValues: []string{value}}
}

// DeriveDecisionValue is the human-readable value for a decision event: bash is the extracted command, MCP the qualified target,
// path-bearing tools the file path, others the tool name. An empty path falls through to the tool name.
func DeriveDecisionValue(toolName string, check PermissionCheckResult, path string) string {
	switch ClassifyToolKind(toolName) {
	case ToolKindBash:
		if check.Command != nil {
			return *check.Command
		}
		return toolName
	case ToolKindMcp, ToolKindMcpTool:
		if check.Target != nil {
			return *check.Target
		}
		return toolName
	}
	if path != "" {
		return path
	}
	return toolName
}

// BuildDecisionEvent builds a decision event's facts from the gate's inputs, centralising the origin, agentName and matchedPattern
// null normalisation. The request id is stamped by the runner, where it was minted.
func BuildDecisionEvent(decision GateDecision, check PermissionCheckResult, agentName *string, result string, resolution PermissionDecisionResolution) DecisionEventFacts {
	var origin *string
	if check.Origin != "" {
		o := check.Origin
		origin = &o
	}
	return DecisionEventFacts{
		Surface: decision.Surface, Value: decision.Value, Result: result, Resolution: resolution,
		Origin: origin, AgentName: agentName, MatchedPattern: check.MatchedPattern,
	}
}

// ResolveYoloGrant is the standing yolo grant covering a gate's resolved check, or nil when yolo does not answer it. An allow of origin
// yolo is recorded authority (the composition-stage ask rewrite, #526); under yolo an ask synthesized after resolution (the bash wrapper
// floor and the two fail-closed parse sentinels, #712) is granted with origin yolo, its matchedPattern preserved. A deny matches
// neither arm, so an explicit deny survives yolo.
func ResolveYoloGrant(check PermissionCheckResult, yoloEnabled bool) *PermissionCheckResult {
	if check.State == "allow" && check.Origin == "yolo" {
		return &check
	}
	if check.State == "ask" && yoloEnabled {
		grant := check
		grant.State, grant.Origin = "allow", "yolo"
		return &grant
	}
	return nil
}
