package pi_subagents

import "encoding/json"

// The recovery half of the steering slice: what a steer-recovery revival takes from a run's persisted recovery descriptor
// (applySteeringRecoveryAgentConfig of src/runs/background/async-resume.ts) and the budgets the paused status leaves.

// recoveryInitialToolBudget is the descriptor's initialToolBudget (carried as raw JSON): the original's object and its resolved form.
func recoveryInitialToolBudget(d *RecoveryDescriptor) (raw any, budget *resolvedToolBudget) {
	if d == nil {
		return nil, nil
	}
	b, ok := d.Carried["initialToolBudget"]
	if !ok {
		return nil, nil
	}
	v, err := parseJSON(b)
	if err != nil || v == nil {
		return nil, nil
	}
	resolved, msg := validateToolBudgetConfig(v, "initialToolBudget", 1)
	if msg != "" || resolved == nil {
		return nil, nil
	}
	return v, resolved
}

// applySteeringRecoveryAgentConfig reconstructs the agent contract a run started with: every field the descriptor captured
// replaces the current definition's, whatever the current one says now (a field the descriptor does not carry is unset, not
// inherited). The thinking ceiling is the lower of the persisted one and the current definition's.
func applySteeringRecoveryAgentConfig(agent AgentConfig, d *RecoveryDescriptor) (AgentConfig, error) {
	out := agent
	maxThinking, err := intersectThinkingCeilings(d.ThinkingCeiling, agent.MaxThinking)
	if err != nil {
		return agent, err
	}
	copyStrings := func(s []string) []string {
		if s == nil {
			return nil
		}
		return append([]string{}, s...)
	}
	out.Model = d.Model
	out.ModelProvider = d.ModelProvider
	out.Thinking = nil
	if d.Thinking != "" {
		out.Thinking = d.Thinking
	}
	out.MaxThinking = maxThinking
	out.Tools = copyStrings(d.Tools)
	out.ExcludeTools = copyStrings(d.ExcludeTools)
	out.AllowNestedSubagents = d.AllowNestedSubagents
	out.AllowedAgents = nil
	if d.AllowedAgents != nil {
		out.AllowedAgents = copyStrings(*d.AllowedAgents)
		if out.AllowedAgents == nil {
			out.AllowedAgents = []string{}
		}
	}
	out.Extensions = copyStrings(d.Extensions)
	out.SubagentOnlyExtensions = copyStrings(d.SubagentOnlyExtensions)
	out.McpDirectTools = copyStrings(d.McpDirectTools)
	out.MutationTools = copyStrings(d.MutationTools)
	if d.SystemPrompt != "" {
		out.SystemPrompt = d.SystemPrompt
	}
	out.SystemPromptMode = d.SystemPromptMode
	out.InheritProjectContext = d.InheritProjectContext
	out.InheritGlobalContext = d.InheritGlobalContext
	out.InheritSkills = d.InheritSkills
	out.Skills = copyStrings(d.Skills)
	out.SkillPath = copyStrings(d.SkillPath)
	out.FilePath = d.AgentFilePath
	out.Memory = nil
	if d.Memory != nil {
		scope, _ := d.Memory["scope"].(string)
		path, _ := d.Memory["path"].(string)
		out.Memory = &AgentMemoryConfig{Scope: scope, Path: path}
	}
	out.Output = d.OutputPath
	out.ToolBudget = nil
	if raw, _ := recoveryInitialToolBudget(d); raw != nil {
		out.ToolBudget = raw
	}
	out.MaxSubagentDepth = new(d.MaxSubagentDepth)
	return out, nil
}

// statusConsumedToolCount is the tool count a paused run spent: its tool budget's count, else its plain tool count.
func statusConsumedToolCount(st *AsyncStatus) int {
	if raw, ok := st.Extra["toolBudget"]; ok {
		var tb struct {
			ToolCount *int `json:"toolCount"`
		}
		if json.Unmarshal(raw, &tb) == nil && tb.ToolCount != nil {
			return *tb.ToolCount
		}
	}
	if st.ToolCount != nil {
		return *st.ToolCount
	}
	return 0
}
