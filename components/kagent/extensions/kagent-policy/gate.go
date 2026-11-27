package kagentpolicy

import (
	"errors"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// MCP resource tools reach the resources of every registered server. They are not tools of the kagent binding, and
// they would read from a server that requires approval without asking, so the gate refuses them.
var resourceTools = map[string]bool{
	"list_mcp_resources":          true,
	"list_mcp_resource_templates": true,
	"read_mcp_resource":           true,
}

// UnknownServerReason is the reason a call of an MCP tool of a server the policy does not name is refused.
const UnknownServerReason = "MCP server is not part of this agent."

// ResourceToolsReason is the reason a resource tool call is refused.
const ResourceToolsReason = "MCP resources are not available: an MCP server of this agent requires approval for its tool calls."

// gatedServer is one server of the policy.
type gatedServer struct {
	prefix   string
	approval bool
}

// gate is the tool_call handler of a loaded policy: approval for the servers that require it, the ask_user dialog, and
// a default deny for MCP tools of servers the policy does not name (a server from mcp.json or another extension would
// otherwise bypass both the allowlist and the approval).
type gate struct {
	servers []gatedServer
	// ask is the ask_user dialog; nil when the tool is not offered.
	ask *askState
}

// lookup reports whether a tool name belongs to a policy server and whether that server requires approval. Servers
// whose prefixes both match (names that differ only where PiG's sanitizing folds them) count as requiring approval if
// either does.
func (g *gate) lookup(toolName string) (known, approval bool) {
	for _, s := range g.servers {
		if strings.HasPrefix(toolName, s.prefix) {
			known = true
			approval = approval || s.approval
		}
	}
	return known, approval
}

func block(reason string) map[string]any {
	return map[string]any{"block": true, "reason": reason}
}

// onToolCall asks for approval before a call of a gated MCP tool. It keys on the tool name only, so a call a
// codemode script makes (the event carries parentToolCallId) is held like a direct one. A call it does not gate
// passes through untouched. An error from the dialog is returned, never read as an approval.
func (g *gate) onToolCall(ctx sdk.Context, data map[string]any) (any, error) {
	name, _ := data["toolName"].(string)
	if name == "" {
		return nil, errors.New("kagent-policy: tool_call event without a toolName")
	}
	if name == AskUserToolName && g.ask != nil {
		return g.ask.hook(ctx, data)
	}
	if resourceTools[name] {
		if g.anyApproval() {
			return block(ResourceToolsReason), nil
		}
		return nil, nil
	}
	known, approval := g.lookup(name)
	if !known && strings.HasPrefix(name, "mcp__") {
		return block(UnknownServerReason), nil
	}
	if !approval {
		return nil, nil
	}
	id, _ := data["toolCallId"].(string)
	if id == "" {
		return nil, errors.New("kagent-policy: tool_call event without a toolCallId")
	}
	// An MCP call always has an object; approving a dialog that shows other arguments than the call carries is worse
	// than refusing the call.
	input, ok := data["input"].(map[string]any)
	if !ok {
		return nil, errors.New("kagent-policy: tool_call event without an input object")
	}
	text, err := encodeMessage(message{V: messageVersion, Kind: kindApproval, ToolCallID: id, ToolName: name, Input: input})
	if err != nil {
		return nil, err
	}
	answer, err := ctx.Confirm(TitleApproval, text)
	if err != nil {
		return nil, err
	}
	if !approved(answer) {
		return block(RejectedReason), nil
	}
	return nil, nil
}

func (g *gate) anyApproval() bool {
	for _, s := range g.servers {
		if s.approval {
			return true
		}
	}
	return false
}
