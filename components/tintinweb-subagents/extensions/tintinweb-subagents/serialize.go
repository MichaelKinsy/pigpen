package tintinweb_subagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// serializeAgentFile writes an agent config as a full .md file (frontmatter and prompt), the format loadCustomAgents
// reads back. upstream: src/agent-file-toggle.ts serializeAgentFile.
func serializeAgentFile(c *agentConfig) string {
	jsonStr := func(s string) string {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.Encode(s)
		return strings.TrimSuffix(b.String(), "\n")
	}
	var f []string
	f = append(f, "description: "+jsonStr(c.Description))
	if c.DisplayName != "" {
		f = append(f, "display_name: "+c.DisplayName)
	}
	if c.Color != "" {
		f = append(f, "color: "+jsonStr(c.Color))
	}
	switch {
	case c.BuiltinToolNames == nil:
		f = append(f, "tools: all")
	case len(c.BuiltinToolNames) == 0:
		f = append(f, "tools: none")
	default:
		f = append(f, "tools: "+strings.Join(c.BuiltinToolNames, ", "))
	}
	if c.Model != "" {
		f = append(f, "model: "+c.Model)
	}
	if c.Thinking != "" {
		f = append(f, "thinking: "+c.Thinking)
	}
	if c.MaxTurns != nil && *c.MaxTurns != 0 {
		f = append(f, fmt.Sprintf("max_turns: %d", *c.MaxTurns))
	}
	switch v := c.AllowedSubagents.(type) {
	case string:
		f = append(f, "allowed_subagents: all")
	case []string:
		f = append(f, "allowed_subagents: "+strings.Join(v, ", "))
	}
	f = append(f, "prompt_mode: "+c.PromptMode)
	switch v := c.Extensions.(type) {
	case bool:
		if !v {
			f = append(f, "extensions: false")
		}
	case []string:
		f = append(f, "extensions: "+strings.Join(v, ", "))
	}
	if len(c.ExcludeExtensions) > 0 {
		f = append(f, "exclude_extensions: "+strings.Join(c.ExcludeExtensions, ", "))
	}
	switch v := c.Skills.(type) {
	case bool:
		if !v {
			f = append(f, "skills: false")
		}
	case []string:
		f = append(f, "skills: "+strings.Join(v, ", "))
	}
	if len(c.DisallowedTools) > 0 {
		f = append(f, "disallowed_tools: "+strings.Join(c.DisallowedTools, ", "))
	}
	if c.InheritContext != nil && *c.InheritContext {
		f = append(f, "inherit_context: true")
	}
	if c.RunInBackground != nil {
		f = append(f, fmt.Sprintf("run_in_background: %t", *c.RunInBackground))
	}
	if c.OutputTranscript != nil && !*c.OutputTranscript {
		f = append(f, "output_transcript: false")
	}
	if c.Isolated != nil && *c.Isolated {
		f = append(f, "isolated: true")
	}
	if c.Memory != "" {
		f = append(f, "memory: "+c.Memory)
	}
	if c.Isolation != "" {
		f = append(f, "isolation: "+c.Isolation)
	}
	return "---\n" + strings.Join(f, "\n") + "\n---\n\n" + c.SystemPrompt + "\n"
}
