package pi_subagents

import (
	"fmt"
	"strconv"
	"strings"
)

// Agent serialization (src/agents/agent-serializer.ts).

func joinComma(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, ", ")
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// runnerToJS is the runner as the original's object, keys in the order its parser builds them.
func runnerToJS(r *Runner) *jsObject {
	o := newObject()
	o.set("type", r.Type)
	switch r.Type {
	case "external-cli":
		if r.Adapter != "" {
			o.set("adapter", r.Adapter)
		}
		o.set("command", r.Command)
		if len(r.Args) > 0 {
			o.set("args", anyStrings(r.Args))
		}
		if r.PromptDelivery != "" {
			o.set("promptDelivery", r.PromptDelivery)
		}
		if r.Capabilities != nil {
			o.set("capabilities", r.Capabilities)
		}
	case "external-job":
		o.set("provider", r.Provider)
		if r.Options != nil {
			o.set("options", r.Options)
		}
	}
	return o
}

// SerializeAgentOptions is agent-serializer.ts SerializeAgentOptions: PreserveFrontmatterFields nil means the file is new.
type SerializeAgentOptions struct {
	PreserveFrontmatterFields map[string]bool
}

func indentLines(text string, prefix string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, " \t\r\n"), "\n") {
		out = append(out, prefix+line)
	}
	return out
}

// serializeAgent is agent-serializer.ts:serializeAgent.
func serializeAgent(c AgentConfig, opts SerializeAgentOptions) string {
	var lines []string
	preserving := opts.PreserveFrontmatterFields != nil
	preserve := func(fields ...string) bool {
		for _, f := range fields {
			if opts.PreserveFrontmatterFields[f] {
				return true
			}
		}
		return false
	}
	lines = append(lines, "---", "name: "+frontmatterName(c.Name, c.LocalName, c.PackageName))
	if c.PackageName != "" {
		lines = append(lines, "package: "+c.PackageName)
	}
	lines = append(lines, "description: "+c.Description)
	if (c.Advertise != nil && *c.Advertise) || preserve("advertise") {
		lines = append(lines, "advertise: "+boolText(c.Advertise != nil && *c.Advertise))
	}
	if v := joinComma(c.Aliases); v != "" || preserve("alias", "aliases") {
		lines = append(lines, "aliases: "+v)
	}
	tools := append(append([]string{}, c.Tools...), prefixAll("mcp:", c.McpDirectTools)...)
	if v := joinComma(tools); v != "" || preserve("tools") {
		lines = append(lines, "tools: "+v)
	}
	if v := joinComma(c.ExcludeTools); v != "" || preserve("excludeTools") {
		lines = append(lines, "excludeTools: "+v)
	}
	if (c.AllowNestedSubagents != nil && *c.AllowNestedSubagents) || preserve("allowNestedSubagents") {
		v := ""
		if c.AllowNestedSubagents != nil {
			v = boolText(*c.AllowNestedSubagents)
		}
		lines = append(lines, "allowNestedSubagents: "+v)
	}
	if c.AllowedAgents != nil || preserve("allowedAgents") {
		lines = append(lines, "allowedAgents: "+joinComma(c.AllowedAgents))
	}
	if c.Model != "" || preserve("model") {
		lines = append(lines, "model: "+c.Model)
	}
	if (c.Fast != nil && *c.Fast) || preserve("fast") {
		v := ""
		if c.Fast != nil {
			v = boolText(*c.Fast)
		}
		lines = append(lines, "fast: "+v)
	}
	thinkingTruthy := false
	thinkingText := ""
	switch x := c.Thinking.(type) {
	case string:
		thinkingTruthy = x != ""
		thinkingText = x
	case bool:
		thinkingTruthy = x
		thinkingText = boolText(x)
	}
	if (thinkingTruthy && (thinkingText != "off" || preserve("thinking"))) || (!thinkingTruthy && preserve("thinking")) {
		lines = append(lines, "thinking: "+thinkingText)
	}
	if !preserving || preserve("systemPromptMode") {
		lines = append(lines, "systemPromptMode: "+c.SystemPromptMode)
	}
	if !preserving || preserve("inheritProjectContext") {
		lines = append(lines, "inheritProjectContext: "+boolText(c.InheritProjectContext))
	}
	if c.InheritGlobalContext || preserve("inheritGlobalContext") {
		lines = append(lines, "inheritGlobalContext: "+boolText(c.InheritGlobalContext))
	}
	if !preserving || preserve("inheritSkills") {
		lines = append(lines, "inheritSkills: "+boolText(c.InheritSkills))
	}
	if c.DefaultContext != "" || preserve("defaultContext") {
		lines = append(lines, "defaultContext: "+c.DefaultContext)
	}
	if c.Runner != nil || preserve("runner") {
		lines = append(lines, "runner:")
		if c.Runner != nil {
			lines = append(lines, indentLines(stringifyYAML(runnerToJS(c.Runner)), "  ")...)
		}
	}
	if c.DefaultAsync != nil || preserve("async") {
		v := ""
		if c.DefaultAsync != nil {
			v = boolText(*c.DefaultAsync)
		}
		lines = append(lines, "async: "+v)
	}
	if c.DefaultTimeoutMs != nil || preserve("timeoutMs") {
		v := ""
		if c.DefaultTimeoutMs != nil {
			v = strconv.Itoa(*c.DefaultTimeoutMs)
		}
		lines = append(lines, "timeoutMs: "+v)
	}
	if c.DefaultToolTimeoutMs != nil || preserve("toolTimeoutMs") {
		v := ""
		if c.DefaultToolTimeoutMs != nil {
			v = strconv.Itoa(*c.DefaultToolTimeoutMs)
		}
		lines = append(lines, "toolTimeoutMs: "+v)
	}
	if c.DefaultAcceptance != nil || preserve("acceptance") {
		v := ""
		switch x := c.DefaultAcceptance.(type) {
		case nil:
		case *jsObject, []any:
			v = marshalJSON(x, "")
		default:
			v = jsString(x)
		}
		lines = append(lines, "acceptance: "+v)
	}
	if c.AcceptanceRole != "" || preserve("acceptanceRole") {
		lines = append(lines, "acceptanceRole: "+c.AcceptanceRole)
	}
	if v := joinComma(c.Skills); v != "" || preserve("skill", "skills") {
		lines = append(lines, "skills: "+v)
	}
	if v := joinComma(c.SkillPath); v != "" || preserve("skillPath") {
		lines = append(lines, "skillPath: "+v)
	}
	if c.Extensions != nil {
		lines = append(lines, "extensions: "+joinComma(c.Extensions))
	}
	if c.SubagentOnlyExtensions != nil || preserve("subagentOnlyExtensions") {
		lines = append(lines, "subagentOnlyExtensions: "+joinComma(c.SubagentOnlyExtensions))
	}
	if v := joinComma(c.MutationTools); v != "" || preserve("mutationTools") {
		lines = append(lines, "mutationTools: "+v)
	}
	if c.Machine != "" || preserve("machine") {
		lines = append(lines, "machine: "+c.Machine)
	}
	if c.Output != "" || preserve("output") {
		lines = append(lines, "output: "+c.Output)
	}
	if c.OutputMode != "" || preserve("outputMode") {
		lines = append(lines, "outputMode: "+c.OutputMode)
	}
	if c.OutputSchema != nil || preserve("outputSchema") {
		v := ""
		if c.OutputSchema != nil {
			v = marshalJSON(c.OutputSchema, "")
		}
		lines = append(lines, "outputSchema: "+v)
	}
	if v := joinComma(c.DefaultReads); v != "" || preserve("defaultReads") {
		lines = append(lines, "defaultReads: "+v)
	}
	if c.DefaultProgress {
		lines = append(lines, "defaultProgress: true")
	}
	if c.Interactive {
		lines = append(lines, "interactive: true")
	}
	if c.MaxSubagentDepth != nil && *c.MaxSubagentDepth >= 0 {
		lines = append(lines, fmt.Sprintf("maxSubagentDepth: %d", *c.MaxSubagentDepth))
	}
	if c.ToolBudget != nil || preserve("toolBudget") {
		v := ""
		if c.ToolBudget != nil {
			v = marshalJSON(c.ToolBudget, "")
		}
		lines = append(lines, "toolBudget: "+v)
	}
	if c.Permissions != nil || preserve("permission", "permissions") {
		key := "permissions"
		if preserve("permission") && !preserve("permissions") {
			key = "permission"
		}
		lines = append(lines, key+":")
		if c.Permissions != nil {
			lines = append(lines, indentLines(stringifyYAML(c.Permissions), "  ")...)
		}
	}
	if c.Memory != nil {
		lines = append(lines, "memory:", "  scope: "+c.Memory.Scope, "  path: "+c.Memory.Path)
	}
	for _, kv := range c.ExtraFields {
		if knownFields[kv.Key] {
			continue
		}
		if strings.Contains(kv.Value, "\n") {
			// Multi-line block value (e.g. permission: nested YAML)
			lines = append(lines, kv.Key+":")
			for _, bl := range strings.Split(kv.Value, "\n") {
				lines = append(lines, "  "+bl)
			}
		} else {
			lines = append(lines, kv.Key+": "+kv.Value)
		}
	}
	lines = append(lines, "---")
	return strings.Join(lines, "\n") + "\n\n" + c.SystemPrompt + "\n"
}

func prefixAll(prefix string, in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = prefix + s
	}
	return out
}
