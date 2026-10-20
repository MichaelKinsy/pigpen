package pi_permission_system

// Port of src/presentation/skill-ask-payload.ts.

// BuildSkillAskPayload builds the payload of a request to load a skill.
func BuildSkillAskPayload(skillName string, agentName *string) PromptPayload {
	return skillPayload("skill", skillName, agentName, []PromptEvidence{})
}

// BuildSkillPathAskPayload builds the payload of a read that reaches a skill through one of its files. The skill is the
// decision-relevant value (it is what the policy names), and the path is the evidence for why this read counts as reaching it.
func BuildSkillPathAskPayload(skill SkillPromptEntry, readPath string, agentName *string) PromptPayload {
	return skillPayload("skill_read", skill.Name, agentName, []PromptEvidence{{Label: "read path", Text: readPath}})
}

func skillPayload(kind, skillName string, agentName *string, evidence []PromptEvidence) PromptPayload {
	return PromptPayload{
		Kind: kind,
		Request: PromptRequestFacts{
			Requester: LocalRequester(agentName),
			Surface:   "skill",
			Value:     skillName,
		},
		Evidence:    evidence,
		Annotations: []PromptAnnotation{},
	}
}
