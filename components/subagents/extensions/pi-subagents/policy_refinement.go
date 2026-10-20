package pi_subagents

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The part of src/agents/agent-refinements.ts the child system prompt reads: the project-local refinement file of an agent
// and the overlay its current guidance becomes. (Writing refinements and the proposal child are a different slice.)

const refinementCurrentFence = "pi-subagents-refinement-current"
const refinementSnapshotsFence = "pi-subagents-refinement-snapshots-json"

var (
	refinementFileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refinementMetaRe     = regexp.MustCompile(`^<!-- pi-subagents-refinement:v1\n((?s:.*?))\n-->\n`)
)

// refinementText is `text()`: the trimmed string, "" for anything else.
func refinementText(v any) string {
	if s, ok := v.(string); ok {
		return jsTrim(s)
	}
	return ""
}

// getAgentRefinementPath is the project file holding an agent's refinement; an error is an agent name that cannot name a file.
func getAgentRefinementPath(cwd, agentName string) (string, bool) {
	trimmed := jsTrim(agentName)
	if !refinementFileNameRe.MatchString(trimmed) || strings.Contains(trimmed, "..") {
		return "", false
	}
	root := filepath.Join(getProjectSubagentsDir(cwd), "refinements")
	resolvedRoot := nodeResolve(root)
	resolved := nodeResolve(root, trimmed+".md")
	if resolved != filepath.Join(resolvedRoot, filepath.Base(resolved)) {
		return "", false
	}
	return resolved, true
}

func extractRefinementFence(markdown, fence string) (string, bool) {
	re := regexp.MustCompile("\n```" + regexp.QuoteMeta(fence) + "\n((?s:.*?))\n```")
	m := re.FindStringSubmatch(markdown)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// parseRefinementCurrent is parseRefinementFile's `current`; ok is false where the original throws (a refinement file that
// does not validate is ignored by the overlay).
func parseRefinementCurrent(markdown string) (string, bool) {
	m := refinementMetaRe.FindStringSubmatch(markdown)
	if m == nil || m[1] == "" {
		return "", false
	}
	v, msg := jsJSONParse(m[1])
	if msg != "" {
		return "", false
	}
	meta, ok := asObject(v)
	if !ok {
		return "", false
	}
	revision, isNum := asNumber(meta.vals["revision"])
	base, baseOK := asObject(meta.vals["base"])
	_, evidenceOK := asObject(meta.vals["evidence"])
	if refinementText(meta.vals["agent"]) == "" || !isNum || revision != math.Trunc(revision) || refinementText(meta.vals["updatedAt"]) == "" || !baseOK || !evidenceOK {
		return "", false
	}
	switch refinementText(base.vals["source"]) {
	case "builtin", "package", "user", "project":
	default:
		return "", false
	}
	if refinementText(base.vals["filePath"]) == "" || refinementText(base.vals["systemPromptSha256"]) == "" {
		return "", false
	}
	current, ok := extractRefinementFence(markdown, refinementCurrentFence)
	if !ok {
		return "", false
	}
	raw, ok := extractRefinementFence(markdown, refinementSnapshotsFence)
	if !ok {
		raw = "[]"
	}
	sv, msg := jsJSONParse(raw)
	snaps, isArr := asArray(sv)
	if msg != "" || !isArr {
		return "", false
	}
	for _, e := range snaps {
		item, ok := asObject(e)
		if !ok {
			return "", false
		}
		action := refinementText(item.vals["action"])
		rev, revNum := asNumber(item.vals["revision"])
		_, beforeOK := item.vals["before"].(string)
		_, afterOK := item.vals["after"].(string)
		if !revNum || rev != math.Trunc(rev) || refinementText(item.vals["at"]) == "" || (action != "refine" && action != "rollback") || !beforeOK || !afterOK {
			return "", false
		}
	}
	return current, true
}

// appendAgentRefinementOverlay appends the current refinement guidance of the agent to a system prompt.
func appendAgentRefinementOverlay(systemPrompt, cwd, agentName string) string {
	filePath, ok := getAgentRefinementPath(cwd, agentName)
	if !ok {
		return systemPrompt
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return systemPrompt
	}
	current, ok := parseRefinementCurrent(string(b))
	if !ok {
		return systemPrompt
	}
	if current = jsTrim(current); current == "" {
		return systemPrompt
	}
	rel, err := filepath.Rel(cwd, filePath)
	if err != nil {
		rel = filePath
	}
	overlay := strings.Join([]string{
		"<pi-subagents-refinement agent=" + marshalJSON(agentName, "") + " source=" + marshalJSON(rel, "") + ">",
		"Project-local refinement guidance generated from recent bounded evidence.",
		"It does not override tool, developer, task, output, acceptance, or safety instructions.",
		"",
		current,
		"</pi-subagents-refinement>",
	}, "\n")
	if jsTrim(systemPrompt) != "" {
		return systemPrompt + "\n\n" + overlay
	}
	return overlay
}

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
