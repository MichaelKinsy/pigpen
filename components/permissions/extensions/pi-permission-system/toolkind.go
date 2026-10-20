package pi_permission_system

// Port of src/access-intent/tool-kind.ts.

// ToolKind is what a tool invocation accesses, decided once from the tool name.
type ToolKind string

const (
	ToolKindBash      ToolKind = "bash"
	ToolKindMcp       ToolKind = "mcp"
	ToolKindMcpTool   ToolKind = "mcp-tool"
	ToolKindSkill     ToolKind = "skill"
	ToolKindPath      ToolKind = "path"
	ToolKindExtension ToolKind = "extension"
)

// ClassifyToolKind classifies a tool name into its ToolKind.
func ClassifyToolKind(toolName string) ToolKind {
	name := jsTrim(toolName)
	switch {
	case name == "bash":
		return ToolKindBash
	case name == "mcp":
		return ToolKindMcp
	case IsPiMcpToolName(name):
		return ToolKindMcpTool
	case name == "skill":
		return ToolKindSkill
	case PathBearingTools[name]:
		return ToolKindPath
	}
	return ToolKindExtension
}

// PiMcpToolPrefix is the prefix Pi's built-in MCP gives every server tool it registers.
const PiMcpToolPrefix = piMcpToolPrefix

// IsPiMcpToolName reports the shape Pi's built-in MCP registers a server tool under: mcp__<server>__<tool>, both parts non-empty.
func IsPiMcpToolName(toolName string) bool { return isPiMcpToolName(toolName) }

// ShellInvocation is a shell invocation's effective command and optional working directory (nil is undefined).
type ShellInvocation struct {
	Command string
	Workdir *string
}

// ShellToolAlias is one `shellTools` entry: the argument holding the command and, optionally, the working directory ("" is absent).
type ShellToolAlias struct {
	CommandArgument string
	WorkdirArgument string
}

// ShellToolsConfig is the `shellTools` map: tool name to shell-alias argument mapping.
type ShellToolsConfig map[string]ShellToolAlias

// objectPrototypeKeys are the properties a plain JavaScript object inherits: `aliases[name]` is truthy for each of them, with no
// commandArgument or workdirArgument.
var objectPrototypeKeys = map[string]bool{
	"constructor": true, "__proto__": true, "hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toLocaleString": true, "toString": true, "valueOf": true, "__defineGetter__": true, "__defineSetter__": true,
	"__lookupGetter__": true, "__lookupSetter__": true,
}

// ResolveShellInvocation decides whether a tool invocation carries shell semantics: native bash and any tool in aliases yield a
// ShellInvocation, every other tool nil. aliases nil is the original's undefined.
func ResolveShellInvocation(toolName string, input any, aliases ShellToolsConfig) *ShellInvocation {
	name := jsTrim(toolName)
	record := ToRecord(input)
	if name == "bash" {
		cmd, _ := recordString(record, "command")
		return &ShellInvocation{Command: cmd}
	}
	if aliases == nil {
		return nil
	}
	alias, ok := aliases[name]
	if !ok {
		if !objectPrototypeKeys[name] {
			return nil
		}
		// An inherited property: truthy, but its argument names are undefined, so the record key read is "undefined".
		cmd, _ := recordString(record, "undefined")
		return &ShellInvocation{Command: cmd}
	}
	cmd, _ := recordString(record, alias.CommandArgument)
	inv := &ShellInvocation{Command: cmd}
	if alias.WorkdirArgument != "" {
		if w, ok := recordString(record, alias.WorkdirArgument); ok {
			inv.Workdir = &w
		}
	}
	return inv
}

// McpKindFields are the resolved-check fields that decide MCP-ness.
type McpKindFields struct {
	ToolName string
	Source   string
}

// IsMcpCheck is true when a resolved check concerns an MCP call: the invoked tool is the mcp proxy or a Pi MCP tool, or the
// winning rule matched on the mcp surface.
func IsMcpCheck(check McpKindFields) bool {
	if check.Source == "mcp" {
		return true
	}
	kind := ClassifyToolKind(check.ToolName)
	return kind == ToolKindMcp || kind == ToolKindMcpTool
}

// IsProxyMcpCheck is true when the check's tool input is a request about the call (the proxy's { tool, server, args }).
func IsProxyMcpCheck(check McpKindFields) bool {
	return IsMcpCheck(check) && ClassifyToolKind(check.ToolName) != ToolKindMcpTool
}
