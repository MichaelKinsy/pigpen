package pi_permission_system

import (
	"strings"
	"testing"
)

func TestToolSurfacePromptTwins(t *testing.T) {
	const f = "exposure/tool-surface-prompt"
	// Pi's own snippets for the tools these tests use.
	snippets := map[string]string{
		"read": "Read file contents", "bash": "Execute bash commands (ls, grep, find, etc.)",
		"edit": "Make precise file edits with exact text replacement", "write": "Create or overwrite files",
		"grep": "Search file contents", "find": "Find files by name", "ls": "List directory contents", "powershell": "Execute PowerShell commands",
	}
	inputs := func(mod func(*ToolSurfaceInputs)) ToolSurfaceInputs {
		in := ToolSurfaceInputs{AllowedTools: []string{"read"}, ToolSnippets: snippets, GuidelinesByTool: map[string][]string{}, PromptGuidelines: []string{}}
		if mod != nil {
			mod(&in)
		}
		return in
	}
	rulesOf := func(mod func(*ToolSurfaceInputs)) []string {
		return strings.Split(RenderToolSurfaceSections(inputs(mod)).Rules, "\n")
	}
	lines := func(s ...string) string { return strings.Join(s, "\n") }
	S := func(s ...string) []string { return s }
	const concise, paths = "- Be concise in your responses", "- Show file paths clearly when working with files"

	tw(t, f, "states the allowed tools and their rules as untagged section contents", func(t *testing.T) {
		got := RenderToolSurfaceSections(inputs(func(in *ToolSurfaceInputs) {
			in.AllowedTools = S("read", "edit")
			in.GuidelinesByTool = map[string][]string{"read": {"Read before editing"}}
		}))
		eq(t, got, ToolSurfaceSections{
			Tools: sp(lines("- read: Read file contents", "- edit: Make precise file edits with exact text replacement")),
			Rules: lines("- Read before editing", concise, paths),
		}, "sections")
	})
	tw(t, f, "omits the tools section when no allowed tool has a snippet", func(t *testing.T) {
		got := RenderToolSurfaceSections(inputs(func(in *ToolSurfaceInputs) { in.AllowedTools = S("undescribed") }))
		eq(t, got, ToolSurfaceSections{Rules: lines(concise, paths)}, "sections")
	})

	tw(t, f, "lists the allowed tools with Pi's own snippets, in the allowed order", func(t *testing.T) {
		got := RenderToolSurfaceSections(inputs(func(in *ToolSurfaceInputs) { in.AllowedTools = S("read", "grep") })).Tools
		eq(t, got, sp(lines("- read: Read file contents", "- grep: Search file contents")), "tools")
	})
	tw(t, f, "omits a tool Pi supplied no snippet for", func(t *testing.T) {
		got := RenderToolSurfaceSections(inputs(func(in *ToolSurfaceInputs) {
			in.AllowedTools, in.ToolSnippets = S("read", "ask_parent"), map[string]string{"read": snippets["read"]}
		})).Tools
		eq(t, got, sp("- read: Read file contents"), "tools")
	})

	tw(t, f, "carries each allowed tool's own guideline bullets", func(t *testing.T) {
		got := rulesOf(func(in *ToolSurfaceInputs) {
			in.AllowedTools = S("read", "edit")
			in.GuidelinesByTool = map[string][]string{
				"read": {"Use read to examine files instead of cat or sed."}, "edit": {"Use edit for precise changes (old text must match exactly)"},
			}
		})
		eq(t, got, S("- Use read to examine files instead of cat or sed.", "- Use edit for precise changes (old text must match exactly)", concise, paths), "rules")
	})
	tw(t, f, "omits a denied tool's guideline bullets", func(t *testing.T) {
		got := rulesOf(func(in *ToolSurfaceInputs) {
			in.AllowedTools = S("read")
			in.GuidelinesByTool = map[string][]string{
				"read": {"Use read to examine files instead of cat or sed."}, "write": {"Use write only for new files or complete rewrites"},
			}
		})
		eq(t, inStrings(got, "- Use write only for new files or complete rewrites"), false, "denied bullet")
	})
	tw(t, f, "carries a third-party tool's guidelines", func(t *testing.T) {
		got := rulesOf(func(in *ToolSurfaceInputs) {
			in.AllowedTools, in.ToolSnippets = S("colgrep"), map[string]string{"colgrep": "Semantic code search"}
			in.GuidelinesByTool = map[string][]string{"colgrep": {"Prefer colgrep for intent-based searches."}}
		})
		eq(t, inStrings(got, "- Prefer colgrep for intent-based searches."), true, "bullet")
	})
	tw(t, f, "de-duplicates a bullet two tools both contribute", func(t *testing.T) {
		const shared = "Do not use emojis"
		count := 0
		for _, line := range rulesOf(func(in *ToolSurfaceInputs) {
			in.AllowedTools = S("read", "edit")
			in.GuidelinesByTool = map[string][]string{"read": {shared}, "edit": {shared}}
		}) {
			if line == "- "+shared {
				count++
			}
		}
		eq(t, count, 1, "occurrences")
	})
	tw(t, f, "always ends with Pi's two unconditional bullets", func(t *testing.T) {
		got := rulesOf(nil)
		eq(t, got[len(got)-2:], S(concise, paths), "tail")
	})

	const bashBullet = "- Use bash for file operations like ls, rg, find"
	tw(t, f, "is written first when bash is the only way to explore", func(t *testing.T) {
		eq(t, rulesOf(func(in *ToolSurfaceInputs) { in.AllowedTools = S("bash") })[0], bashBullet, "first")
	})
	tw(t, f, "is withheld when a dedicated exploration tool is allowed", func(t *testing.T) {
		eq(t, inStrings(rulesOf(func(in *ToolSurfaceInputs) { in.AllowedTools = S("bash", "grep") }), bashBullet), false, "bullet")
	})
	tw(t, f, "is withheld when no shell is allowed", func(t *testing.T) {
		eq(t, rulesOf(func(in *ToolSurfaceInputs) { in.AllowedTools = S("read") }), S(concise, paths), "rules")
	})
	tw(t, f, "names PowerShell when it is the only shell", func(t *testing.T) {
		eq(t, rulesOf(func(in *ToolSurfaceInputs) { in.AllowedTools = S("powershell") })[0],
			"- Use PowerShell for file operations like listing, searching, and finding files", "first")
	})
	tw(t, f, "names both shells when both are allowed", func(t *testing.T) {
		eq(t, rulesOf(func(in *ToolSurfaceInputs) { in.AllowedTools = S("bash", "powershell") })[0],
			"- Use bash or PowerShell for file operations like listing, searching, and finding files", "first")
	})

	tw(t, f, "carries a rule no tool contributes, after the tools' own and before Pi's two", func(t *testing.T) {
		got := rulesOf(func(in *ToolSurfaceInputs) {
			in.GuidelinesByTool = map[string][]string{"read": {"Read before editing"}}
			in.PromptGuidelines = S("An extension's rule")
		})
		eq(t, got, S("- Read before editing", "- An extension's rule", concise, paths), "rules")
	})
	tw(t, f, "does not carry a denied tool's rule back in by that route", func(t *testing.T) {
		got := rulesOf(func(in *ToolSurfaceInputs) {
			in.GuidelinesByTool = map[string][]string{"bash": {"  Use bash carefully"}}
			in.PromptGuidelines = S("Use bash carefully ")
		})
		eq(t, inStrings(got, "- Use bash carefully"), false, "bullet")
	})
}
