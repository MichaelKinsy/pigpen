package pi_permission_system

// Port of src/handlers/gates/tool.ts: the normal per-tool permission gate.

// ToolPathAccess is a path-bearing tool call's resolved path, paired with the session scope approving it would grant. The pattern is
// derived by the pipeline's PathNormalizer, which owns the session's PathFlavor, so the gate carries the platform's separator
// semantics without holding them (#655).
type ToolPathAccess struct {
	Path             *AccessPath
	ApprovalPatterns []string
}

// deriveSuggestionValue is the value used for session-approval pattern suggestions: bash is the command string, MCP the qualified
// target, everything else the catch-all wildcard.
func deriveSuggestionValue(toolName string, check PermissionCheckResult) string {
	switch ClassifyToolKind(toolName) {
	case ToolKindBash:
		if check.Command != nil {
			return *check.Command
		}
		return ""
	case ToolKindMcp:
		if check.Target != nil {
			return *check.Target
		}
		return "mcp"
	}
	return "*"
}

// gateSurfaceOf is the surface a tool call is gated on: a shell invocation (native `bash` or an aliased shell tool) is gated on the
// `bash` surface and a Pi MCP tool on the `mcp` surface, while the invoked tool name is preserved in the prompt and review log (#574).
func gateSurfaceOf(toolName string, shell *ShellInvocation) string {
	if shell != nil {
		return "bash"
	}
	if ClassifyToolKind(toolName) == ToolKindMcpTool {
		return "mcp"
	}
	return toolName
}

// sessionOption is the approval to record and the label offering it.
type sessionOption struct {
	approval *SessionApproval
	label    string
}

func pathSessionOption(gateSurface string, approvalPatterns []string) sessionOption {
	s := SuggestPathSessionPattern(gateSurface, approvalPatterns)
	return sessionOption{approval: SessionApprovalForPatterns(s.Surface, s.Patterns), label: s.Label}
}

// valueSessionOption is the session option for a gate whose value is not a path. A Pi MCP tool is approved by its own full name,
// which is one of its `mcp` candidates: exactly the tool asked about, never the wider server prefix.
func valueSessionOption(toolName, gateSurface string, check PermissionCheckResult) sessionOption {
	var s SessionApprovalSuggestion
	if ClassifyToolKind(toolName) == ToolKindMcpTool {
		s = SuggestExactSessionPattern(gateSurface, jsTrim(toolName))
	} else {
		s = SuggestSessionPattern(gateSurface, deriveSuggestionValue(gateSurface, check))
	}
	return sessionOption{approval: SessionApprovalSingle(s.Surface, s.Pattern), label: s.Label}
}

// DescribeToolGate builds a pure descriptor for the normal tool permission gate from a pre-computed check. No side effects.
func DescribeToolGate(tcc ToolCallContext, check PermissionCheckResult, formatter *ToolPreviewFormatter, pathAccess *ToolPathAccess, shell *ShellInvocation) *GateDescriptor {
	gateSurface := gateSurfaceOf(tcc.ToolName, shell)
	permissionLogContext := formatter.GetPermissionLogContext(check, tcc.Input, PathBearingTools)

	var option sessionOption
	if pathAccess != nil {
		option = pathSessionOption(gateSurface, pathAccess.ApprovalPatterns)
	} else {
		option = valueSessionOption(tcc.ToolName, gateSurface, check)
	}

	toolName := tcc.ToolName
	payload := BuildToolAskPayload(ToolAskFacts{
		Check: check, AgentName: tcc.AgentName, Surface: gateSurface, InvokedToolName: &toolName, Input: tcc.Input, Formatter: formatter,
	})

	pathValue, _ := GetPathBearingToolPath(tcc.ToolName, tcc.Input)
	decisionValue := DeriveDecisionValue(gateSurface, check, pathValue)

	// A path-bearing tool carries the AccessPath's alias set; every other surface carries its already-portable value.
	var accessIntent ForwardedAccessFacts
	if pathAccess != nil {
		accessIntent = AccessFactsFromPath(gateSurface, pathAccess.Path)
	} else {
		accessIntent = AccessFactsFromValue(gateSurface, decisionValue)
	}

	toolCallID, label := tcc.ToolCallID, option.label
	logContext := newObject()
	logContext.set("source", "tool_call")
	logContext.set("toolCallId", tcc.ToolCallID)
	logContext.set("toolName", tcc.ToolName)
	logContext.setOpt("command", ptrAny(permissionLogContext.Command))
	logContext.setOpt("target", ptrAny(permissionLogContext.Target))
	logContext.setOpt("toolInputPreview", ptrAny(permissionLogContext.ToolInputPreview))
	if permissionLogContext.Origin == "" {
		logContext.set("origin", undef{})
	} else {
		logContext.set("origin", permissionLogContext.Origin)
	}
	if check.FloorExemption != "" {
		logContext.set("floorExemption", string(check.FloorExemption))
	}

	return &GateDescriptor{
		Surface:         gateSurface,
		Input:           tcc.Input,
		Payload:         payload,
		SessionApproval: option.approval,
		PromptDetails: PromptPermissionDetails{
			Source: "tool_call", AgentName: tcc.AgentName, ToolCallID: &toolCallID, ToolName: &toolName, SessionLabel: &label,
			AccessIntent: &accessIntent, Command: permissionLogContext.Command, Target: permissionLogContext.Target,
			ToolInputPreview: permissionLogContext.ToolInputPreview,
		},
		LogContext: logContext,
		Decision:   GateDecision{Surface: gateSurface, Value: decisionValue},
	}
}

// ptrAny is a *string as an optional value: nil is undefined (setOpt), a present string itself.
func ptrAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
