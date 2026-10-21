package pi_permission_system

import "strings"

// Port of src/exposure/tool-surface-prompt.ts: the tool-surface region of a system prompt, which tools this session may call and
// the guidance those tools contribute. Pi renders that region itself from the active tool set this extension narrows, but only when
// it writes the preamble; a subagent child's prompt is always a customPrompt, under which Pi writes neither section, so a child
// states its own through systemPromptOptions.sections. Rendering follows buildSystemPrompt's own rules.

// ToolSurfaceInputs is what a session's tool surface renders from.
type ToolSurfaceInputs struct {
	// AllowedTools are the tools this session may call, in the order they should be listed.
	AllowedTools []string
	// ToolSnippets are Pi's one-line tool descriptions, keyed by tool name.
	ToolSnippets map[string]string
	// GuidelinesByTool are the guideline bullets each tool contributes, keyed by tool name.
	GuidelinesByTool map[string][]string
	// PromptGuidelines is systemPromptOptions.promptGuidelines: the bullets other extensions add to Pi's rules after the tools'
	// own. Only a bullet no registered tool contributes is carried, so a denied tool's bullet cannot return by this route.
	PromptGuidelines []string
}

// ToolSurfaceSections is this session's tool surface as the contents of Pi's tools and rules prompt sections, untagged: Pi wraps
// each sections entry in its own tags.
type ToolSurfaceSections struct {
	// Tools is the `- name: snippet` lines; nil when no allowed tool has a snippet.
	Tools *string
	// Rules is the `- rule` lines, in buildSystemPrompt's order.
	Rules string
}

// universalGuidelines are Pi's two unconditional guideline bullets, in the order it writes them.
var universalGuidelines = []string{"Be concise in your responses", "Show file paths clearly when working with files"}

// RenderToolSurfaceSections renders this session's tool surface as section contents, for a node that states it through
// systemPromptOptions.sections. The tools section is omitted when no allowed tool has a snippet, as Pi lists a tool only when it
// has one.
func RenderToolSurfaceSections(inputs ToolSurfaceInputs) ToolSurfaceSections {
	tools := toolBullets(inputs)
	rules := strings.Join(ruleBullets(inputs), "\n")
	if len(tools) == 0 {
		return ToolSurfaceSections{Rules: rules}
	}
	joined := strings.Join(tools, "\n")
	return ToolSurfaceSections{Tools: &joined, Rules: rules}
}

// toolBullets is one bullet per allowed tool that has a snippet. A tool without one is left unlisted rather than rendered bare.
func toolBullets(inputs ToolSurfaceInputs) []string {
	bullets := make([]string, 0, len(inputs.AllowedTools))
	for _, toolName := range inputs.AllowedTools {
		if snippet := inputs.ToolSnippets[toolName]; snippet != "" {
			bullets = append(bullets, "- "+toolName+": "+snippet)
		}
	}
	return bullets
}

// ruleBullets is the guideline bullets for the allowed set. It mirrors buildSystemPrompt's assembly: its conditional
// file-exploration bullet first, then each allowed tool's own contributions, then the bullets other extensions added, then its two
// unconditional bullets, de-duplicated in first-seen order.
func ruleBullets(inputs ToolSurfaceInputs) []string {
	size := len(inputs.AllowedTools) + len(inputs.PromptGuidelines) + len(universalGuidelines)
	bullets := make([]string, 0, size)
	seen := make(map[string]bool, size)
	addGuideline := func(guideline string) {
		normalized := jsTrim(guideline)
		if normalized == "" || seen[normalized] {
			return
		}
		seen[normalized] = true
		bullets = append(bullets, normalized)
	}

	allowed := make(map[string]bool, len(inputs.AllowedTools))
	for _, name := range inputs.AllowedTools {
		allowed[name] = true
	}
	if fileOperations, ok := fileExplorationGuideline(allowed); ok {
		addGuideline(fileOperations)
	}
	for _, toolName := range inputs.AllowedTools {
		for _, guideline := range inputs.GuidelinesByTool[toolName] {
			addGuideline(guideline)
		}
	}
	for _, guideline := range extensionGuidelines(inputs) {
		addGuideline(guideline)
	}
	for _, guideline := range universalGuidelines {
		addGuideline(guideline)
	}

	for i, bullet := range bullets {
		bullets[i] = "- " + bullet
	}
	return bullets
}

// extensionGuidelines is the promptGuidelines bullets no registered tool contributes.
func extensionGuidelines(inputs ToolSurfaceInputs) []string {
	toolGuidelines := map[string]bool{}
	for _, guidelines := range inputs.GuidelinesByTool {
		for _, guideline := range guidelines {
			toolGuidelines[jsTrim(guideline)] = true
		}
	}
	kept := []string{}
	for _, guideline := range inputs.PromptGuidelines {
		if !toolGuidelines[jsTrim(guideline)] {
			kept = append(kept, guideline)
		}
	}
	return kept
}

// fileExplorationGuideline is Pi's shell-only file-exploration bullet, ok false when it does not apply. Pi writes it only when a
// shell is available and none of the dedicated exploration tools is, so a session holding grep, find or ls is not told to reach for
// the shell instead.
func fileExplorationGuideline(allowedTools map[string]bool) (string, bool) {
	hasBash := allowedTools["bash"]
	hasPowerShell := allowedTools["powershell"]
	hasExplorationTool := allowedTools["grep"] || allowedTools["find"] || allowedTools["ls"]

	if (!hasBash && !hasPowerShell) || hasExplorationTool {
		return "", false
	}
	switch {
	case hasBash && hasPowerShell:
		return "Use bash or PowerShell for file operations like listing, searching, and finding files", true
	case hasPowerShell:
		return "Use PowerShell for file operations like listing, searching, and finding files", true
	}
	return "Use bash for file operations like ls, rg, find", true
}
