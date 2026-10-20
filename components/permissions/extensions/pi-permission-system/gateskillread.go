package pi_permission_system

// Port of src/handlers/gates/skill-read.ts: the skill-read permission gate.

// DescribeSkillReadGate builds a pure descriptor for the skill-read gate. It returns nil when the gate does not apply (the tool is not
// `read`, no active skill entries, or the read path matches no skill) and a descriptor with the preResolved state of the matched skill
// entry otherwise.
func DescribeSkillReadGate(tcc ToolCallContext, normalizer *PathNormalizer, getActiveSkillEntries func() []SkillPromptEntry) *GateDescriptor {
	activeSkillEntries := getActiveSkillEntries()
	if tcc.ToolName != "read" || len(activeSkillEntries) == 0 {
		return nil
	}

	path, _ := ToRecord(tcc.Input).str("path")
	if path == "" {
		return nil
	}

	normalizedReadPath := normalizer.ComparableValue(path)
	matchedSkill := FindSkillPathMatch(normalizedReadPath, activeSkillEntries, normalizer)
	if matchedSkill == nil {
		return nil
	}

	payload := BuildSkillPathAskPayload(*matchedSkill, path, tcc.AgentName)
	toolCallID, toolName, skillName := tcc.ToolCallID, tcc.ToolName, matchedSkill.Name
	accessIntent := AccessFactsFromValue("skill", matchedSkill.Name)

	logContext := newObject()
	logContext.set("source", "skill_read")
	logContext.set("toolCallId", tcc.ToolCallID)
	logContext.set("skillName", matchedSkill.Name)
	logContext.set("agentName", strOrNull(tcc.AgentName))
	logContext.set("path", path)

	input := newObject()
	input.set("name", matchedSkill.Name)
	return &GateDescriptor{
		Surface: "skill",
		Input:   input,
		Payload: payload,
		PromptDetails: PromptPermissionDetails{
			Source: "skill_read", AgentName: tcc.AgentName, ToolCallID: &toolCallID, ToolName: &toolName,
			SkillName: &skillName, Path: &path, AccessIntent: &accessIntent,
		},
		LogContext:  logContext,
		Decision:    GateDecision{Surface: "skill", Value: matchedSkill.Name},
		PreResolved: &GatePreResolved{State: matchedSkill.State},
	}
}
