package pi_subagents

// The child system prompt (src/runs/shared/child-launch-plan.ts order); the refinement overlay and its file are
// agentmgmt_refine.go (src/agents/agent-refinements.ts).

// buildEffectiveSystemPrompt is the child system prompt in the order preflight and every execution path hash: base prompt,
// skills, memory, refinement overlay, output path. Runtime acceptance prose is appended later and stays outside launch identity.
func buildEffectiveSystemPrompt(agent AgentConfig, resolvedSkills []ResolvedSkill, cwd, outputPath string) string {
	prompt := jsTrim(agent.SystemPrompt)
	appendSection := func(section string) {
		if prompt != "" {
			prompt += "\n\n" + section
		} else {
			prompt = section
		}
	}
	if len(resolvedSkills) > 0 {
		appendSection(buildSkillInjection(resolvedSkills))
	}
	if memory := buildAgentMemoryInjection(agent, cwd); memory != "" {
		appendSection(memory)
	}
	prompt = appendAgentRefinementOverlay(prompt, cwd, agent.Name)
	return injectOutputPathSystemPrompt(prompt, outputPath, outputCapsOf(agent))
}

// outputCapsOf is the agent's tools as the output path instruction reads them.
func outputCapsOf(agent AgentConfig) *outputInstructionCapabilities {
	if agent.Tools != nil {
		return &outputInstructionCapabilities{Tools: agent.Tools, ToolsSet: true, McpDirectTools: agent.McpDirectTools}
	}
	if len(agent.McpDirectTools) > 0 {
		return &outputInstructionCapabilities{McpDirectTools: agent.McpDirectTools}
	}
	return nil
}
