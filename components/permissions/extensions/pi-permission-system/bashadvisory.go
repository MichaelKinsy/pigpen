package pi_permission_system

import "github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"

// Port of src/service/bash-advisory-check.ts.

// ResolveBashAdvisoryCheck resolves an advisory bash query at the gate's decomposed fidelity. When the parser is warm, the command is
// decomposed into its command-pattern units and routed through the same orchestrator the enforcement gate uses
// (ResolveBashCommandCheck), so a chained or nested command returns the most restrictive decision (deny, then ask, then allow) and
// inherits the opaque-wrapper floor (#481) and both fail-closed parse sentinels (#452, #840) at parity with the gate. In the pre-warm
// window (ParseBashCommandsSync answers false) it falls back to the whole-string match, so the answer is never weaker than before.
// Synchronous: the only asynchronous step, parser initialisation, happens earlier at before_agent_start.
func ResolveBashAdvisoryCheck(command string, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	commands, warm := bashanalysis.ParseBashCommandsSync(command)
	if !warm {
		o := newObject()
		o.set("command", command)
		return resolver.Resolve(ToolAccessIntent{Surface: "bash", Input: o, AgentName: agentName})
	}
	return ResolveBashCommandCheck(command, commands, agentName, resolver)
}
