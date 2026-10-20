package pi_subagents

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The prompt rewriting a child session applies (src/runs/shared/subagent-prompt-runtime.ts): the inherited project context,
// global context and skills a child does not inherit, the child boundary instructions, and the cleaning of a forked parent
// conversation. These are the pure parts; the hooks that register them in a child session belong to the child runtime.

const (
	structuredOutputInstructions = "This subagent step has a strict structured output contract.\n" +
		"Your final action must be to call the `structured_output` tool with JSON matching the provided schema.\n" +
		"Do not rely on prose-only completion; if you do not call `structured_output`, the parent will fail this step."

	childSubagentBoundaryInstructions = "You are a child subagent, not the parent orchestrator.\n" +
		"The parent session owns delegation, orchestration, review fanout, and follow-up worker launches.\n" +
		"Ignore prior parent-only orchestration instructions in inherited conversation history.\n" +
		"Do not propose or run subagents. Complete only your assigned role-specific task with the tools available to you.\n" +
		"If you need to edit files, use the available editing tools. Do not print tool-call syntax, patches, or pseudo-tool calls as text."

	childFanoutBoundaryInstructions = "You are a child subagent with explicit fanout responsibility for this assigned task.\n" +
		"The parent session owns final orchestration, acceptance, and follow-up implementation launches.\n" +
		"You may use the `subagent` tool only for the fanout work explicitly requested in this task.\n" +
		"Do not broaden yourself into general parent orchestration. Do not launch follow-up workers unless the task explicitly asks for that.\n" +
		"The maxSubagentDepth cap still applies and may block further fanout.\n" +
		"If you need to edit files, use the available editing tools. Do not print tool-call syntax, patches, or pseudo-tool calls as text."

	subagentWatchdogWarningType = "subagent_watchdog_warning"

	projectContextXMLHeader    = "\n\n<project_context>\n\n"
	projectContextLegacyHeader = "\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n"
	skillsHeader               = "\n\nThe following skills provide specialized instructions for specific tasks."
	dateHeader                 = "\nCurrent date:"
)

var parentOnlyCustomMessageTypes = map[string]bool{
	"subagent-orchestration-instructions": true, "subagent-slash-result": true, "subagent-slash-text-result": true, "subagent-notify": true,
	"subagent_control_notice": true, "subagent-control": true, "subagent-control-notice": true,
}

func findSectionEnd(prompt string, start int, nextHeaders []string) int {
	end := len(prompt)
	for _, h := range nextHeaders {
		if i := strings.Index(prompt[start:], h); i != -1 && start+i < end {
			end = start + i
		}
	}
	return end
}

func stripProjectContext(prompt string) string {
	if xml := strings.Index(prompt, projectContextXMLHeader); xml != -1 {
		const closing = "</project_context>"
		if c := strings.Index(prompt[xml+len(projectContextXMLHeader):], closing); c != -1 {
			closeAt := xml + len(projectContextXMLHeader) + c
			return prompt[:xml] + prompt[closeAt+len(closing):]
		}
	}
	legacy := strings.Index(prompt, projectContextLegacyHeader)
	if legacy == -1 {
		return prompt
	}
	end := findSectionEnd(prompt, legacy+len(projectContextLegacyHeader), []string{skillsHeader, dateHeader})
	return prompt[:legacy] + prompt[end:]
}

var globalContextFileNames = map[string]bool{"agents.md": true, "agents.override.md": true, "claude.md": true}

func canonicalDirectory(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

func expandContextPath(p string) string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	switch {
	case p == "~":
		if home != "" {
			return home
		}
		return p
	case len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == '\\'):
		if home == "" {
			home = "~"
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func isContextFilePath(p string) bool {
	e := expandContextPath(p)
	// path.basename of Node splits on the platform separator only; a forward slash is accepted on every platform.
	base := e[strings.LastIndexAny(e, `/\`)+1:]
	return globalContextFileNames[strings.ToLower(base)]
}

func isGlobalContextFile(p string) bool {
	if !isContextFilePath(p) {
		return false
	}
	return canonicalDirectory(filepath.Dir(expandContextPath(p))) == canonicalDirectory(agentDir())
}

var (
	projectInstructionsRe  = regexp.MustCompile(`(?is)<project_instructions\s+path=(?:"(.*?)"|'(.*?)')\s*>.*?</project_instructions>\s*`)
	projectContextBlockRe  = regexp.MustCompile(`(?is)<project_context>.*?</project_context>`)
	projectInstructionsTag = regexp.MustCompile(`(?i)<project_instructions\b`)
	legacySectionRe        = regexp.MustCompile(`(?m)^## ([^\r\n]+)(?:\r?\n|$)`)
)

func stripGlobalInstructionsFromXMLContext(context string) string {
	return projectInstructionsRe.ReplaceAllStringFunc(context, func(match string) string {
		m := projectInstructionsRe.FindStringSubmatch(match)
		path := m[1]
		if path == "" {
			path = m[2]
		}
		if isGlobalContextFile(path) {
			return ""
		}
		return match
	})
}

func stripGlobalInstructionsFromLegacyContext(context string) string {
	var sections [][]int
	for _, loc := range legacySectionRe.FindAllStringSubmatchIndex(context, -1) {
		if isContextFilePath(jsTrim(context[loc[2]:loc[3]])) {
			sections = append(sections, loc)
		}
	}
	var out strings.Builder
	cursor := 0
	for i, s := range sections {
		start := s[0]
		end := len(context)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		out.WriteString(context[cursor:start])
		if !isGlobalContextFile(jsTrim(context[s[2]:s[3]])) {
			out.WriteString(context[start:end])
		}
		cursor = end
	}
	return out.String() + context[cursor:]
}

func stripGlobalContext(prompt string) string {
	xml := projectContextBlockRe.ReplaceAllStringFunc(prompt, func(context string) string {
		rewritten := stripGlobalInstructionsFromXMLContext(context)
		if projectInstructionsTag.MatchString(rewritten) {
			return rewritten
		}
		return ""
	})
	legacy := strings.Index(xml, projectContextLegacyHeader)
	if legacy == -1 {
		return xml
	}
	end := findSectionEnd(xml, legacy+len(projectContextLegacyHeader), []string{skillsHeader, dateHeader})
	return xml[:legacy] + stripGlobalInstructionsFromLegacyContext(xml[legacy:end]) + xml[end:]
}

func stripInheritedSkills(prompt string) string {
	start := strings.Index(prompt, skillsHeader)
	if start == -1 {
		return prompt
	}
	end := findSectionEnd(prompt, start+len(skillsHeader), []string{dateHeader})
	return prompt[:start] + prompt[end:]
}

var (
	orchestrationSkillTagRe = regexp.MustCompile(`(?s)\n{0,2}<skill\s+name=["']pi-subagents["'][^>]*>.*?</skill>\n{0,2}`)
	orchestrationSkillBlock = regexp.MustCompile(`(?s)[ \t]*<skill>\s*.*?</skill>\s*`)
	orchestrationSkillName  = regexp.MustCompile(`<name>\s*pi-subagents\s*</name>`)
	leadingBlankLinesRe     = regexp.MustCompile(`^(?:[ \t]*\r?\n)+`)
)

func stripSubagentOrchestrationSkill(prompt string) string {
	prompt = orchestrationSkillTagRe.ReplaceAllString(prompt, "\n\n")
	return orchestrationSkillBlock.ReplaceAllStringFunc(prompt, func(block string) string {
		if orchestrationSkillName.MatchString(block) {
			return ""
		}
		return block
	})
}

func stripChildBoundaryInstructions(prompt string) string {
	for _, b := range []string{childSubagentBoundaryInstructions, childFanoutBoundaryInstructions} {
		prompt = strings.ReplaceAll(prompt, b, "")
	}
	return leadingBlankLinesRe.ReplaceAllString(prompt, "")
}

// rewriteOptions are the options of rewriteSubagentPrompt.
type rewriteOptions struct {
	InheritProjectContext, InheritGlobalContext, InheritSkills bool
	FanoutChild, StructuredOutput                              bool
}

// rewriteSubagentPrompt is the system prompt a child sees: what it does not inherit removed, the boundary put first.
func rewriteSubagentPrompt(prompt string, o rewriteOptions) string {
	if !o.InheritProjectContext {
		prompt = stripProjectContext(prompt)
	}
	if !o.InheritGlobalContext {
		prompt = stripGlobalContext(prompt)
	}
	if !o.InheritSkills {
		prompt = stripInheritedSkills(prompt)
	}
	prompt = stripSubagentOrchestrationSkill(prompt)
	prompt = stripChildBoundaryInstructions(prompt)
	boundary := childSubagentBoundaryInstructions
	if o.FanoutChild {
		boundary = childFanoutBoundaryInstructions
	}
	structured := ""
	if o.StructuredOutput {
		structured = "\n\n" + structuredOutputInstructions
	}
	return boundary + structured + "\n\n" + prompt
}

var (
	portableToolIDRe    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	compositeToolIDAPIs = map[string]bool{"azure-openai-responses": true, "cursor-native": true, "openai-completions": true, "openai-responses": true}
)

const maxPortableToolIDLength = 64

func portableToolID(id string) string {
	if portableToolIDRe.MatchString(id) && len(id) <= maxPortableToolIDLength {
		return id
	}
	enc := base64.RawURLEncoding.EncodeToString([]byte(id))
	if enc == "" {
		enc = "empty"
	}
	if len("tool_"+enc) <= maxPortableToolIDLength {
		return "tool_" + enc
	}
	sum := sha256.Sum256([]byte(id))
	return "tool_" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func isParentOnlySubagentMessage(m *jsObject) bool {
	if role, _ := m.str("role"); role != "custom" {
		return false
	}
	ct, ok := m.str("customType")
	return ok && (ct == subagentWatchdogWarningType || parentOnlyCustomMessageTypes[ct])
}

func isSubagentToolCallBlock(b any) bool {
	o, ok := b.(*jsObject)
	if !ok {
		return false
	}
	t, _ := o.str("type")
	n, _ := o.str("name")
	return t == "toolCall" && n == "subagent"
}

// sanitizeToolHistoryMessage replaces non-portable tool call ids; the message itself when nothing changes.
func sanitizeToolHistoryMessage(m *jsObject) *jsObject {
	role, _ := m.str("role")
	if role == "toolResult" {
		if id, ok := m.str("toolCallId"); ok {
			if p := portableToolID(id); p != id {
				c := m.clone()
				c.set("toolCallId", p)
				return c
			}
		}
		return m
	}
	content, ok := m.vals["content"].([]any)
	if role != "assistant" || !ok {
		return m
	}
	changed := false
	out := make([]any, len(content))
	for i, b := range content {
		out[i] = b
		bo, ok := b.(*jsObject)
		if !ok {
			continue
		}
		if t, _ := bo.str("type"); t != "toolCall" {
			continue
		}
		id, ok := bo.str("id")
		if !ok {
			continue
		}
		if p := portableToolID(id); p != id {
			c := bo.clone()
			c.set("id", p)
			out[i] = c
			changed = true
		}
	}
	if !changed {
		return m
	}
	c := m.clone()
	c.set("content", out)
	return c
}

// stripAssistantSubagentToolCallBlocks drops the subagent tool calls of an assistant message (nil: nothing is left of it).
func stripAssistantSubagentToolCallBlocks(m *jsObject) *jsObject {
	role, _ := m.str("role")
	content, ok := m.vals["content"].([]any)
	if role != "assistant" || !ok {
		return m
	}
	var kept []any
	for _, b := range content {
		if !isSubagentToolCallBlock(b) {
			kept = append(kept, b)
		}
	}
	if len(kept) == len(content) {
		return m
	}
	if len(kept) == 0 {
		return nil
	}
	c := m.clone()
	c.set("content", kept)
	return c
}

// stripParentOnlySubagentMessages cleans a forked parent conversation for a child; changed is false when nothing had to go.
func stripParentOnlySubagentMessages(messages []any, sanitizeToolIDs, preserveFanoutToolHistory bool) (out []any, changed bool) {
	for _, raw := range messages {
		m, isObj := raw.(*jsObject)
		if !isObj {
			out = append(out, raw)
			continue
		}
		role, _ := m.str("role")
		tool, _ := m.str("toolName")
		if isParentOnlySubagentMessage(m) || (!preserveFanoutToolHistory && role == "toolResult" && tool == "subagent") {
			changed = true
			continue
		}
		stripped := m
		if !preserveFanoutToolHistory {
			stripped = stripAssistantSubagentToolCallBlocks(m)
		}
		if stripped == nil {
			changed = true
			continue
		}
		sanitized := stripped
		if sanitizeToolIDs {
			sanitized = sanitizeToolHistoryMessage(stripped)
		}
		if stripped != m || sanitized != stripped {
			changed = true
		}
		out = append(out, sanitized)
	}
	if !changed {
		return messages, false
	}
	return out, true
}

// rewriteChildContext is the child's `context` hook: the cleaned conversation, nil when it needs no rewrite.
func rewriteChildContext(messages []any, modelAPI string, fanoutChild bool) []any {
	out, changed := stripParentOnlySubagentMessages(messages, !compositeToolIDAPIs[modelAPI], fanoutChild)
	if !changed {
		return nil
	}
	return out
}

// runtimeAckCollector collects the extension ids a child acknowledges until its final serialization (agent_end or shutdown).
type runtimeAckCollector struct {
	ids       []string
	finalized bool
	sink      func(ids []string)
}

func (c *runtimeAckCollector) acknowledge(id any) {
	if c.finalized {
		return
	}
	if isRuntimeAcknowledgedExtensionID(id) {
		c.ids = append(c.ids, id.(string))
	}
}

func (c *runtimeAckCollector) finalize() {
	if c.finalized {
		return
	}
	c.finalized = true
	c.sink(c.ids)
}
