package pi_permission_system

// Port of src/presentation/tool-ask-payload.ts: the payload for the per-tool gate, a bash, MCP or generic-tool ask.

// ToolAskFacts are the facts the per-tool gate holds when it raises an ask.
type ToolAskFacts struct {
	// Check is the resolved check: the gated tool, the matched rule, the offending unit.
	Check     PermissionCheckResult
	AgentName *string
	// Surface is the gate surface the rule fired on: bash for a shell alias.
	Surface string
	// InvokedToolName is the tool the agent actually called, when a shell alias re-exposes bash (nil is undefined or null).
	InvokedToolName *string
	// Input is the raw tool input, the source of the input-preview evidence.
	Input any
	// Formatter renders the per-tool input preview; nil means no preview evidence.
	Formatter *ToolPreviewFormatter
}

// BuildToolAskPayload builds the payload for the per-tool gate. The branch decides only the payload's kind and which fact is the
// decision-relevant value; how any of it reads is a renderer's decision.
func BuildToolAskPayload(facts ToolAskFacts) PromptPayload {
	check := facts.Check
	bash := ClassifyToolKind(check.ToolName) == ToolKindBash
	mcpFields := McpKindFields{ToolName: check.ToolName, Source: check.Source}
	mcp := IsMcpCheck(mcpFields) && check.Target != nil

	kind := "tool"
	if bash {
		kind = "bash"
	} else if mcp {
		kind = "mcp"
	}
	toolName := check.ToolName
	var commandContext *string
	if check.CommandContext != "" {
		c := string(check.CommandContext)
		commandContext = &c
	}
	var evidence []PromptEvidence
	if bash {
		evidence = fullCommandEvidence(facts)
	} else {
		evidence = inputPreviewEvidence(facts, IsProxyMcpCheck(mcpFields))
	}
	return PromptPayload{
		Kind: kind,
		Request: PromptRequestFacts{
			Requester:       LocalRequester(facts.AgentName),
			Surface:         facts.Surface,
			ToolName:        &toolName,
			InvokedToolName: distinctInvokedName(facts),
			Value:           askValue(check, bash, mcp),
			MatchedPattern:  check.MatchedPattern,
			CommandContext:  commandContext,
			ExecutedUnit:    check.ExecutedUnit,
		},
		Evidence:    evidence,
		Annotations: []PromptAnnotation{},
	}
}

// askValue is the decision-relevant value: the offending command for bash, the qualified target for MCP, the tool name otherwise.
// A bash check with no command yields the empty string rather than the tool name: the ask is about a command, and naming the
// surface instead would assert a command that was never resolved.
func askValue(check PermissionCheckResult, bash, mcp bool) string {
	switch {
	case bash:
		if check.Command != nil {
			return *check.Command
		}
		return ""
	case mcp:
		return *check.Target
	}
	return check.ToolName
}

// distinctInvokedName is the invoked tool name, but only when it is a fact the gated name does not carry.
func distinctInvokedName(facts ToolAskFacts) *string {
	if facts.InvokedToolName == nil || *facts.InvokedToolName == facts.Check.ToolName {
		return nil
	}
	return facts.InvokedToolName
}

// fullCommandEvidence is the enclosing command, when the gated unit is only part of what will run.
func fullCommandEvidence(facts ToolAskFacts) []PromptEvidence {
	fullCommand, ok := recordString(ToRecord(facts.Input), "command")
	if !ok || (facts.Check.Command != nil && fullCommand == *facts.Check.Command) {
		return []PromptEvidence{}
	}
	return []PromptEvidence{{Label: "full command", Text: fullCommand}}
}

// inputPreviewEvidence is the per-tool input preview, when a formatter is registered and produces one. A proxied MCP ask previews
// under the mcp key rather than the qualified target, so a registered MCP formatter is consulted for every server; a Pi MCP tool's
// input is its own arguments, so it previews under its own name.
func inputPreviewEvidence(facts ToolAskFacts, proxyMcp bool) []PromptEvidence {
	if facts.Formatter == nil {
		return []PromptEvidence{}
	}
	toolName := facts.Check.ToolName
	if proxyMcp {
		toolName = "mcp"
	}
	if preview := facts.Formatter.FormatToolInputForPrompt(toolName, facts.Input); preview != "" {
		return []PromptEvidence{{Label: "input", Text: preview}}
	}
	return []PromptEvidence{}
}
