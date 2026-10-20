package pi_permission_system

// Fail-closed stand-ins for the three bash gate producers the tool-call pipeline calls (src/handlers/gates/bash-command.ts,
// bash-path.ts, bash-external-directory.ts). They belong to the bash gate lane, which deletes this file in the commit that ports
// them under these exact names. Until then a call panics: the tool-call boundary turns a panic into a block, so a bash call is
// refused rather than allowed.

const pendingBashGates = "the bash gates are not ported yet"

// DescribeBashExternalDirectoryGate is describeBashExternalDirectoryGate (stand-in).
func DescribeBashExternalDirectoryGate(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult {
	panic(pendingBashGates)
}

// DescribeBashPathGate is describeBashPathGate (stand-in).
func DescribeBashPathGate(tcc ToolCallContext, program *BashProgram, resolver ScopedPermissionResolver, normalizer *PathNormalizer) GateResult {
	panic(pendingBashGates)
}

// ResolveBashCommandCheck is resolveBashCommandCheck (stand-in).
func ResolveBashCommandCheck(command string, commands []BashCommand, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult {
	panic(pendingBashGates)
}
