package pi_permission_system

import "strings"

// Port of src/presentation/permission-prompts.ts: the pre-check reasons, refused before any payload exists to render. (The ask
// prompts are payload builders, and denial text is a render over the payload: agentrenderer.go.)

// FormatMissingToolNameReason is the block reason for a tool call with no tool name.
func FormatMissingToolNameReason() string {
	return "Tool call was blocked because no tool name was provided. Use a registered tool name from pi.getAllTools()."
}

// FormatUnknownToolReason is the block reason for a tool that is not registered, naming up to ten registered tools.
func FormatUnknownToolReason(toolName string, availableToolNames []string) string {
	preview := availableToolNames
	if len(preview) > 10 {
		preview = preview[:10]
	}
	suffix := ""
	if len(availableToolNames) > len(preview) {
		suffix = ", ..."
	}
	availableList := "none"
	if len(preview) > 0 {
		availableList = strings.Join(preview, ", ") + suffix
	}

	kind := ClassifyToolKind(toolName)
	mcpHint := ""
	if kind != ToolKindMcp && kind != ToolKindMcpTool {
		mcpHint = ` If this was intended as an MCP server tool, call the registered 'mcp' tool when available (for example: {"tool":"server:tool"}).`
	}

	return "Tool '" + toolName + "' is not registered in this runtime and was blocked before permission checks." + mcpHint + " Registered tools: " + availableList + "."
}
