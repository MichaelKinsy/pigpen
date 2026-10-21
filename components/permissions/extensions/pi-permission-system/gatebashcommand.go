package pi_permission_system

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
)

// Port of src/handlers/gates/bash-command.ts: the bash command-pattern decision for a (possibly chained) command.

var noBashSpellings = []string{}

const (
	opaqueWrapperSentinel      = "<opaque-bash-wrapper>"
	indirectionWrapperSentinel = "<indirection-bash-wrapper>"
	unparsedSubtreeSentinel    = "<unparsed-bash-subtree>"
	unparseableSentinel        = "<unparseable-bash-command>"
)

// ResolveBashCommandCheck resolves the bash command-pattern decision for a (possibly chained) command. Every command unit of the
// parsed program is evaluated on the `bash` surface and the most restrictive result wins (deny over ask over allow), so a denied
// command cannot ride an allowed leading one (#301). The winner carries the offending sub-command, its rule and its execution
// context. A wrapper unit's allow is floored to a synthetic ask (#481, #490); a unit the parse could not resolve has its allow floored
// to an ask naming the whole command (#840). When the primary parse matched nothing the whole command is the only surface an explicit
// deny can reach, so it is resolved first (#712), and with no units at all the result fails closed to a synthetic ask (#452).
func ResolveBashCommandCheck(command string, commands []BashCommand, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	if isTriviallyEmptyBashCommand(command) {
		return resolveOnBashSurface(command, nil, agentName, resolver)
	}

	primary := false
	for i := range commands {
		if !commands[i].Salvaged {
			primary = true
			break
		}
	}
	if !primary {
		whole := resolveOnBashSurface(command, nil, agentName, resolver)
		if whole.State == "deny" {
			return whole
		}
		if len(commands) == 0 {
			pattern := unparseableSentinel
			return PermissionCheckResult{State: "ask", ToolName: "bash", Source: "bash", Origin: "builtin", Command: &command, MatchedPattern: &pattern}
		}
	}

	results := make([]PermissionCheckResult, len(commands))
	for i := range commands {
		results[i] = resolveBashCommandUnit(&commands[i], command, agentName, resolver)
	}
	if len(results) == 0 {
		return resolveOnBashSurface(command, nil, agentName, resolver)
	}
	return mostRestrictiveOf(results, func(c PermissionCheckResult) string { return c.State })
}

// resolveBashCommandUnit resolves one unit of the chain: its own rule, floored where the enumerator established a reason, then
// tagged with the facts the prompt and the session-approval suggestion read off the winner.
func resolveBashCommandUnit(cmd *BashCommand, command string, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	base := resolveOnBashSurface(cmd.Text, cmd.Spellings, agentName, resolver)
	floored := base
	if cmd.WrapperKind != "" && base.State == "allow" {
		floored = resolveBashWrapperUnit(cmd, base, agentName, resolver)
	}
	result := floorUnparsedBashUnit(cmd, command, floored)
	if cmd.Context != "" {
		result.CommandContext = BashCommandContext(cmd.Context)
	}
	if cmd.ExecutedUnit != "" {
		executed := cmd.ExecutedUnit
		result.ExecutedUnit = &executed
	}
	return result
}

// floorUnparsedBashUnit floors a unit the parse could not resolve (ADR 0013 section 10, #840). The result names the whole command, only
// an allow is floored, and the result is a copy of the resolved one so a session grant keeps its source.
func floorUnparsedBashUnit(cmd *BashCommand, command string, resolved PermissionCheckResult) PermissionCheckResult {
	if !cmd.ParseUnresolved || resolved.State != "allow" {
		return resolved
	}
	pattern := unparsedSubtreeSentinel
	resolved.State = "ask"
	resolved.Command = &command
	resolved.MatchedPattern = &pattern
	return resolved
}

// resolveBashWrapperUnit resolves a wrapper unit whose own text resolved to allow: floored to a synthetic ask naming the wrapper
// kind, unless the enumerator established that the floor has no reason left to hold, in which case the inner command's rule decides
// (ADR 0013 section 11, #803).
func resolveBashWrapperUnit(cmd *BashCommand, base PermissionCheckResult, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	if cmd.FloorExemption == "" || cmd.ExecutedUnit == "" {
		sentinel := opaqueWrapperSentinel
		if cmd.WrapperKind == bashanalysis.WrapperIndirection {
			sentinel = indirectionWrapperSentinel
		}
		base.State = "ask"
		base.MatchedPattern = &sentinel
		return base
	}
	inner := resolveOnBashSurface(cmd.ExecutedUnit, nil, agentName, resolver)
	inner.Command = base.Command
	inner.FloorExemption = FloorExemption(cmd.FloorExemption)
	return inner
}

// isTriviallyEmptyBashCommand is true when a command is empty, whitespace-only, or only comment lines.
func isTriviallyEmptyBashCommand(command string) bool {
	for _, line := range strings.Split(command, "\n") {
		line = jsTrim(line)
		if line != "" && line[0] != '#' {
			return false
		}
	}
	return true
}

// resolveOnBashSurface resolves one command string, and the other spellings the shell runs it by, against the `bash` surface.
func resolveOnBashSurface(command string, spellings []string, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	if spellings == nil {
		spellings = noBashSpellings
	}
	return resolver.Resolve(BashCommandAccessIntent{Surface: "bash", Command: command, Spellings: spellings, AgentName: agentName})
}
