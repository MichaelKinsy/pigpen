package pi_permission_system

// Port of src/handlers/gates/skill-input.ts.

// DescribeSkillInputGate builds a pure descriptor for the skill-input permission gate. It takes the pre-computed check so the caller
// can reuse the result it already obtained (to conditionally emit a deny warning) without re-running the check inside the runner.
func DescribeSkillInputGate(skillName string, agentName *string, preCheck PermissionCheckResult) *GateDescriptor {
	accessIntent := AccessFactsFromValue("skill", skillName)
	logContext := newObject()
	logContext.set("source", "skill_input")
	logContext.set("skillName", skillName)
	logContext.set("agentName", strOrNull(agentName))
	input := newObject()
	input.set("name", skillName)
	return &GateDescriptor{
		Surface:  "skill",
		Input:    input,
		PreCheck: &preCheck,
		Payload:  BuildSkillAskPayload(skillName, agentName),
		PromptDetails: PromptPermissionDetails{
			Source: "skill_input", AgentName: agentName, SkillName: &skillName, AccessIntent: &accessIntent,
		},
		LogContext: logContext,
		Decision:   GateDecision{Surface: "skill", Value: skillName},
	}
}
