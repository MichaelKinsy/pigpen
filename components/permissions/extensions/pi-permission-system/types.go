package pi_permission_system

// Port of src/types.ts and src/handlers/gates/types.ts. ScopeConfig lives with the config layer (policy loading); the config-shape
// types (PermissionState, DenyWithReason, FlatPermissionConfig) are the strings and *jsObject the rule engine already uses.

// BashCommandContext is the execution context of a bash command nested inside a substitution or subshell ("" for a current-shell
// command).
type BashCommandContext string

const (
	BashContextCommandSubstitution BashCommandContext = "command_substitution"
	BashContextProcessSubstitution BashCommandContext = "process_substitution"
	BashContextSubshell            BashCommandContext = "subshell"
)

// FloorExemption is why an indirection wrapper's floor did not apply after all (#803); "" is none.
type FloorExemption string

// FloorExemptionCoreReader: the command the wrapper runs is in the built-in pure-reader core.
const FloorExemptionCoreReader FloorExemption = "core-reader"

// PermissionCheckResult is the original's PermissionCheckResult. Optional fields are nil/"" when absent.
type PermissionCheckResult struct {
	ToolName string
	// State is "allow", "deny" or "ask".
	State string
	// Reason is the custom denial reason from a deny-with-reason pattern, when present.
	Reason         *string
	MatchedPattern *string
	Command        *string
	Target         *string
	// Source is "tool", "bash", "mcp", "skill", "special", "default" or "session".
	Source string
	// Origin is which source contributed the winning rule.
	Origin string
	// CommandContext is the context of the offending nested command ("" for a current-shell command).
	CommandContext BashCommandContext
	// ExecutedUnit is the command the winning unit actually runs when it is a wrapper whose inner command differs (display only).
	ExecutedUnit *string
	// FloorExemption names why the wrapper floor did not apply ("" is none).
	FloorExemption FloorExemption
}

// IsPermissionState reports value as "allow", "deny" or "ask".
func IsPermissionState(value any) bool {
	_, ok := isPermissionState(value)
	return ok
}

// IsDenyWithReason reports a raw `{ action: "deny" }` or `{ action: "deny", reason: "..." }` object; a non-string reason is not one.
func IsDenyWithReason(value any) bool {
	_, ok := denyWithReason(value)
	return ok
}

// GateOutcome is the outcome of a single permission gate evaluation: Action "allow", or "block" with a Reason.
type GateOutcome struct {
	Action string
	Reason string
}

// ToolCallContext is the pre-validated context shared across all gates. AgentName nil is the original's null.
type ToolCallContext struct {
	ToolName   string
	AgentName  *string
	Input      any
	ToolCallID string
	Cwd        string
}
