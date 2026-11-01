package pi_subagents

import (
	"strings"
)

// The agent file format the management actions write (the original's
// src/agents/agent-serializer.ts): frontmatter in the original's field order,
// then the system prompt. Declared keys the typed loader does not model are
// written back verbatim in file order, so an unrelated update never loses them.

// serializeAgent renders an agent definition file.
func serializeAgent(a *AgentConfig, preserve map[string]bool) string {
	preserving := func(fields ...string) bool {
		for _, f := range fields {
			if preserve[f] {
				return true
			}
		}
		return false
	}
	preserveMode := preserve != nil
	lines := []string{"---"}
	lines = append(lines, "name: "+frontmatterName(a.Name, a.LocalName, a.PackageName))
	if a.PackageName != "" {
		lines = append(lines, "package: "+a.PackageName)
	}
	lines = append(lines, "description: "+a.Description)
	if a.Advertise != nil || preserving("advertise") {
		lines = append(lines, "advertise: "+boolWord(a.Advertise))
	}
	if aliases := joinComma(a.Aliases); aliases != "" || preserving("alias", "aliases") {
		lines = append(lines, "aliases: "+joinComma(a.Aliases))
	}
	tools := append([]string{}, a.Tools...)
	for _, t := range a.McpDirectTools {
		tools = append(tools, "mcp:"+t)
	}
	if tv := joinComma(tools); tv != "" || preserving("tools") {
		lines = append(lines, "tools: "+tv)
	}
	if et := joinComma(a.ExcludeTools); et != "" || preserving("excludeTools") {
		lines = append(lines, "excludeTools: "+et)
	}
	if a.AllowNestedSubagents != nil || preserving("allowNestedSubagents") {
		lines = append(lines, "allowNestedSubagents: "+boolWord(a.AllowNestedSubagents))
	}
	if a.AllowedAgents != nil || preserving("allowedAgents") {
		lines = append(lines, "allowedAgents: "+joinComma(a.AllowedAgents))
	}
	if a.Model != "" || preserving("model") {
		lines = append(lines, "model: "+a.Model)
	}
	if a.Fast != nil || preserving("fast") {
		lines = append(lines, "fast: "+boolWord(a.Fast))
	}
	if thinking := thinkingWord(a); thinking != "" || preserving("thinking") {
		lines = append(lines, "thinking: "+thinking)
	}
	if !preserveMode || preserving("systemPromptMode") {
		lines = append(lines, "systemPromptMode: "+a.SystemPromptMode)
	}
	if !preserveMode || preserving("inheritProjectContext") {
		lines = append(lines, "inheritProjectContext: "+boolWordPtr(a.InheritProjectContext))
	}
	if a.InheritGlobalContext || preserving("inheritGlobalContext") {
		lines = append(lines, "inheritGlobalContext: "+boolWordPtr(a.InheritGlobalContext))
	}
	if !preserveMode || preserving("inheritSkills") {
		lines = append(lines, "inheritSkills: "+boolWordPtr(a.InheritSkills))
	}
	if a.DefaultContext != "" || preserving("defaultContext") {
		lines = append(lines, "defaultContext: "+a.DefaultContext)
	}
	if a.Runner != nil || preserving("runner") {
		lines = append(lines, "runner:")
		if a.Runner != nil {
			lines = append(lines, "  type: "+a.Runner.Type)
			if a.Runner.Type == "external-cli" && a.Runner.Command != "" {
				lines = append(lines, "  command: "+a.Runner.Command)
			}
			if a.Runner.Type == "external-job" && a.Runner.Command != "" {
				lines = append(lines, "  provider: "+a.Runner.Command)
			}
		}
	}
	if a.DefaultAsync != nil || preserving("async") {
		lines = append(lines, "async: "+boolWord(a.DefaultAsync))
	}
	if a.DefaultTimeoutMs != nil || preserving("timeoutMs") {
		lines = append(lines, "timeoutMs: "+int64Word(a.DefaultTimeoutMs))
	}
	if a.DefaultToolTimeoutMs != nil || preserving("toolTimeoutMs") {
		lines = append(lines, "toolTimeoutMs: "+int64Word(a.DefaultToolTimeoutMs))
	}
	if a.DefaultAcceptance != "" || preserving("acceptance") {
		lines = append(lines, "acceptance: "+a.DefaultAcceptance)
	}
	if a.AcceptanceRole != "" || preserving("acceptanceRole") {
		lines = append(lines, "acceptanceRole: "+a.AcceptanceRole)
	}
	if skills := joinComma(a.Skills); skills != "" || preserving("skill", "skills") {
		lines = append(lines, "skills: "+skills)
	}
	if sp := joinComma(a.SkillPath); sp != "" || preserving("skillPath") {
		lines = append(lines, "skillPath: "+sp)
	}
	extensions := a.Extensions
	if a.ExtensionsDeclared != nil {
		extensions = a.ExtensionsDeclared
	}
	subagentOnly := a.SubagentOnlyExtensions
	if a.SubagentOnlyExtensionsDeclared != nil {
		subagentOnly = a.SubagentOnlyExtensionsDeclared
	}
	if extensions != nil || preserving("extensions") {
		lines = append(lines, "extensions: "+joinComma(extensions))
	}
	if subagentOnly != nil || preserving("subagentOnlyExtensions") {
		lines = append(lines, "subagentOnlyExtensions: "+joinComma(subagentOnly))
	}
	if mt := joinComma(a.MutationTools); mt != "" || preserving("mutationTools") {
		lines = append(lines, "mutationTools: "+mt)
	}
	if a.Machine != "" || preserving("machine") {
		lines = append(lines, "machine: "+a.Machine)
	}
	if a.Output != "" || preserving("output") {
		lines = append(lines, "output: "+a.Output)
	}
	if a.OutputMode != "" || preserving("outputMode") {
		lines = append(lines, "outputMode: "+a.OutputMode)
	}
	if a.OutputSchema != "" || preserving("outputSchema") {
		lines = append(lines, "outputSchema: "+a.OutputSchema)
	}
	if reads := joinComma(a.DefaultReads); reads != "" || preserving("defaultReads") {
		lines = append(lines, "defaultReads: "+reads)
	}
	if a.DefaultProgress {
		lines = append(lines, "defaultProgress: true")
	}
	if a.Interactive {
		lines = append(lines, "interactive: true")
	}
	if a.MaxSubagentDepth != nil || preserving("maxSubagentDepth") {
		lines = append(lines, "maxSubagentDepth: "+intWord(a.MaxSubagentDepth))
	}
	if a.ToolBudget != "" || preserving("toolBudget") {
		lines = append(lines, "toolBudget: "+a.ToolBudget)
	}
	// Declared keys the typed loader does not model, in file order; a multiline
	// block value is re-indented under its key, as the original indents one.
	for _, e := range a.ExtraFrontmatter {
		if strings.Contains(e.Value, "\n") {
			lines = append(lines, e.Key+":")
			for _, blockLine := range strings.Split(e.Value, "\n") {
				lines = append(lines, "  "+blockLine)
			}
		} else {
			lines = append(lines, e.Key+": "+e.Value)
		}
	}
	lines = append(lines, "---")
	return strings.Join(lines, "\n") + "\n\n" + a.SystemPrompt + "\n"
}

// joinComma is the original's joinComma (empty for an empty list).
func joinComma(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, ", ")
}

func boolWord(v *bool) string {
	if v != nil && *v {
		return "true"
	}
	return "false"
}

func boolWordPtr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func int64Word(v *int64) string {
	if v == nil {
		return ""
	}
	return formatInt(*v)
}

func intWord(v *int) string {
	if v == nil {
		return ""
	}
	return formatInt(int64(*v))
}

// thinkingWord renders the thinking value the way the original's serializer does.
func thinkingWord(a *AgentConfig) string {
	switch t := a.Thinking.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}
