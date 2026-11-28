package toolcmd

import (
	_ "embed"
	"strings"
	"unicode"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/tools/tool-commands.ts
// upstream: packages/coding-agent/src/prompts/system/tool-command-catalog.md
// upstream: packages/coding-agent/src/prompts/system/tool-command-description-cut.md

//go:embed prompts/tool-command-catalog.md
var toolCommandCatalogTemplate string

//go:embed prompts/tool-command-description-cut.md
var toolCommandDescriptionCutTemplate string

// Prompt-doc budget constants matching omp v19.1.1 (31ffee513f).
// upstream: packages/coding-agent/src/tools/tool-commands.ts:160-164
const (
	// ToolCommandDocsTotalBudget is the full-help character budget for
	// system-prompt tool-command sections (TOOL_COMMAND_DOCS_TOTAL_BUDGET).
	ToolCommandDocsTotalBudget = 48_000
	// ToolCommandDocsPerToolCap prevents one pathological description from
	// starving later tools (TOOL_COMMAND_DOCS_PER_TOOL_CAP).
	ToolCommandDocsPerToolCap = 10_000
	// ToolCommandExternalDescriptionCap is the description cap (UTF-8 bytes
	// for catalog summaries, code units/chars for inlined descriptions) for
	// external mounted tools (TOOL_COMMAND_EXTERNAL_DESCRIPTION_CAP).
	ToolCommandExternalDescriptionCap = 200
)

// DocsMode controls which mounted tools get their help inlined into the system prompt.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:70
type DocsMode string

const (
	DocsModeInline   DocsMode = "inline"
	DocsModeBuiltins DocsMode = "builtins"
	DocsModeCatalog  DocsMode = "catalog"
)

// DefaultDocsMode is omp's default for `tools.commandDocs` ("catalog").
// upstream: packages/coding-agent/src/tools/settings.ts:1139
const DefaultDocsMode DocsMode = DocsModeCatalog

// ToolDescriptor is the read-only metadata of one tool for prompt docs and dispatch.
type ToolDescriptor struct {
	Name           string
	Label          string
	Summary        string
	Description    string
	HasDescription bool
	Parameters     any // json.RawMessage or map[string]any
	CustomWireName string
	BuiltIn        bool
	LoadMode       LoadMode
}

// CommandEntry is one `{name, summary, dynamic}` triple for prompt templates and `/tools`.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:265-279
type CommandEntry struct {
	Name    string
	Summary string
	Dynamic bool
}

// PromptDocsPlan holds mounted-tool placement in the system prompt: inlined
// help sections, then one-line catalog entries in presentation order.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:287-291
type PromptDocsPlan struct {
	Sections     []string
	CatalogOrder []string
	Catalog      map[string]string
}

// State is the shared tool state consumed by presentation, prompt docs, and dispatch.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:125-138
type State struct {
	Tools        map[string]*ToolDescriptor
	MountedOrder []string // canonical mounted tool names in presentation order
	MountedNames map[string]bool
	BuiltInNames map[string]bool
	IsActive     func(name string) bool
}

// ListToolCommands returns mounted tools in presentation order.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:257-262
func ListToolCommands(state *State) []*ToolDescriptor {
	if state == nil {
		return nil
	}
	out := make([]*ToolDescriptor, 0, len(state.MountedOrder))
	for _, name := range state.MountedOrder {
		if !state.MountedNames[name] {
			continue
		}
		if tool, ok := state.Tools[name]; ok && tool != nil {
			out = append(out, tool)
		}
	}
	return out
}

// ResolveToolWireName resolves exact canonical names first, then an unambiguous
// customWireName alias.
// upstream: packages/coding-agent/src/utils/tool-wire-name.ts:7-20
func ResolveToolWireName(tools map[string]*ToolDescriptor, name string) *ToolDescriptor {
	if exact, ok := tools[name]; ok && exact != nil {
		return exact
	}
	var resolved *ToolDescriptor
	for _, tool := range tools {
		if tool == nil || tool.CustomWireName != name {
			continue
		}
		if resolved != nil {
			return nil
		}
		resolved = tool
	}
	return resolved
}

// ResolveToolCommand resolves enabled canonical names or unambiguous wire aliases
// through the session map (mounted OR active top-level).
// upstream: packages/coding-agent/src/tools/tool-commands.ts:230-234
func ResolveToolCommand(state *State, name string) *ToolDescriptor {
	if state == nil {
		return nil
	}
	resolved := ResolveToolWireName(state.Tools, name)
	if resolved == nil {
		return nil
	}
	isMounted := state.MountedNames[resolved.Name]
	isActive := state.IsActive != nil && state.IsActive(resolved.Name)
	if !isMounted && !isActive {
		return nil
	}
	return resolved
}

// ResolveMountedToolCommand resolves a mounted tool by exact canonical name only.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:237-239
func ResolveMountedToolCommand(state *State, name string) *ToolDescriptor {
	if state == nil || !state.MountedNames[name] {
		return nil
	}
	return state.Tools[name]
}

func renderDescriptionCut(name string) string {
	tmpl := strings.TrimSuffix(toolCommandDescriptionCutTemplate, "\n")
	return strings.ReplaceAll(tmpl, "{{name}}", name)
}

// utf16Len returns the number of UTF-16 code units in s, matching JavaScript's
// `String.prototype.length` used by omp for `descriptionCap` and the
// 10_000 / 48_000 prompt-doc budgets (tool-commands.ts:169, 312-313).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUTF16 returns the longest prefix of s whose UTF-16 length is <= maxUnits
// without splitting a rune (avoids lone surrogates for astral code points).
func sliceUTF16(s string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > maxUnits {
			return s[:i]
		}
		units += w
	}
	return s
}

func renderHelpWithCap(tool *ToolDescriptor, descriptionCap int) string {
	desc := tool.Description
	hasDesc := tool.HasDescription || desc != ""
	if descriptionCap > 0 && hasDesc && utf16Len(desc) > descriptionCap {
		cut := sliceUTF16(desc, descriptionCap)
		desc = strings.TrimRightFunc(cut, unicode.IsSpace) + renderDescriptionCut(tool.Name)
	}
	return RenderToolHelp(ToolCliDefinition{
		Name:        tool.Name,
		Description: desc,
		HasDesc:     hasDesc,
		Schema:      tool.Parameters,
	})
}
func renderHelpSection(tool *ToolDescriptor, descriptionCap int) string {
	return "## " + tool.Name + "\n\n" + strings.TrimRightFunc(renderHelpWithCap(tool, descriptionCap), unicode.IsSpace)
}

// ToolCommandHelp returns the full `--help` text of one enabled tool.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:282-284
func ToolCommandHelp(state *State, name string) (string, bool) {
	tool := ResolveToolCommand(state, name)
	if tool == nil {
		return "", false
	}
	return renderHelpWithCap(tool, 0), true
}

func toolSummary(inst *ToolDescriptor) string {
	if inst.Summary != "" {
		return inst.Summary
	}
	for _, line := range strings.Split(inst.Description, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	if inst.Label != "" {
		return inst.Label
	}
	return inst.Name
}

const summaryEllipsis = "…"

// truncateHeadUTF8Bytes truncates s so its UTF-8 byte length is <= maxBytes
// without splitting a multi-byte UTF-8 rune.
func truncateHeadUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for i, r := range s {
		w := utf8.RuneLen(r)
		if w < 0 {
			w = 1
		}
		if i+w > maxBytes {
			break
		}
		end = i + w
	}
	return s[:end]
}

func isSummaryControlRune(r rune) bool {
	return (r >= 0x0000 && r <= 0x001f) ||
		(r >= 0x007f && r <= 0x009f) ||
		r == 0x2028 || r == 0x2029
}

// sanitizeCatalogSummary strips C0/C1 control characters and Unicode line/paragraph
// separators (collapsing runs to a single space) and bounds the result in UTF-8
// bytes on a code-point boundary.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:199-206
func sanitizeCatalogSummary(summary string, maxBytes int) string {
	var b strings.Builder
	b.Grow(len(summary))
	inControl := false
	for _, r := range summary {
		if isSummaryControlRune(r) {
			if !inControl {
				b.WriteByte(' ')
				inControl = true
			}
			continue
		}
		inControl = false
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())
	if maxBytes <= 0 || len(cleaned) <= maxBytes {
		return cleaned
	}
	ellipsisBytes := len(summaryEllipsis)
	if maxBytes < ellipsisBytes {
		return truncateHeadUTF8Bytes(cleaned, maxBytes)
	}
	body := strings.TrimRightFunc(truncateHeadUTF8Bytes(cleaned, maxBytes-ellipsisBytes), unicode.IsSpace)
	return body + summaryEllipsis
}

func promptCatalogSummary(inst *ToolDescriptor, maxBytes int) string {
	raw := toolSummary(inst)
	first := ""
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			first = trimmed
			break
		}
	}
	if first == "" {
		first = inst.Name
	}
	cleaned := sanitizeCatalogSummary(first, maxBytes)
	if cleaned == "" {
		return inst.Name
	}
	return cleaned
}

// ToolCommandEntries returns `{name, summary, dynamic}` triples in presentation order.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:265-279
func ToolCommandEntries(state *State) []CommandEntry {
	tools := ListToolCommands(state)
	entries := make([]CommandEntry, 0, len(tools))
	for _, tool := range tools {
		dynamic := !state.BuiltInNames[tool.Name]
		capBytes := 0
		if dynamic {
			capBytes = ToolCommandExternalDescriptionCap
		}
		entries = append(entries, CommandEntry{
			Name:    tool.Name,
			Summary: promptCatalogSummary(tool, capBytes),
			Dynamic: dynamic,
		})
	}
	return entries
}

// matchGlob matches glob patterns supporting `*`, `?`, and simple `{a,b}` brace
// alternation against a tool command name. Empty or malformed patterns return false.
func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	return globMatch(pattern, name)
}
func globMatch(pat, str string) bool {
	// Expand simple top-level `{a,b}` brace groups if present.
	if open := strings.IndexByte(pat, '{'); open >= 0 {
		if closeIdx := strings.IndexByte(pat[open:], '}'); closeIdx > 0 {
			closePos := open + closeIdx
			prefix := pat[:open]
			suffix := pat[closePos+1:]
			for _, alt := range strings.Split(pat[open+1:closePos], ",") {
				if globMatch(prefix+alt+suffix, str) {
					return true
				}
			}
			return false
		}
	}
	pi, si := 0, 0
	starIdx, matchIdx := -1, 0
	for si < len(str) {
		if pi < len(pat) && pat[pi] == '*' {
			starIdx = pi
			matchIdx = si
			pi++
		} else if pi < len(pat) && (pat[pi] == '?' || pat[pi] == str[si]) {
			pi++
			si++
		} else if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		} else {
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

func shouldInlineToolCommand(state *State, tool *ToolDescriptor, mode DocsMode, inlinePatterns []string) bool {
	if mode == DocsModeCatalog {
		return false
	}
	if mode == DocsModeInline || state.BuiltInNames[tool.Name] {
		return true
	}
	for _, pat := range inlinePatterns {
		if pat != "" && matchGlob(pat, tool.Name) {
			return true
		}
	}
	return false
}

// PlanToolCommandPromptDocs places mounted tools under the configured prompt-doc
// policy and budgets. A tool the policy does not inline, or whose help exceeds a
// cap, becomes a catalog entry.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:298-323
func PlanToolCommandPromptDocs(state *State, mode DocsMode, inlinePatterns []string) PromptDocsPlan {
	if mode == "" {
		mode = DocsModeInline
	}
	plan := PromptDocsPlan{
		Catalog: make(map[string]string),
	}
	used := 0
	for _, tool := range ListToolCommands(state) {
		descCap := ToolCommandExternalDescriptionCap
		if state.BuiltInNames[tool.Name] {
			descCap = 0
		}
		if shouldInlineToolCommand(state, tool, mode, inlinePatterns) {
			section := renderHelpSection(tool, descCap)
			sectionLen := utf16Len(section)
			if sectionLen <= ToolCommandDocsPerToolCap && used+sectionLen <= ToolCommandDocsTotalBudget {
				used += sectionLen
				plan.Sections = append(plan.Sections, section)
				continue
			}
		}
		plan.CatalogOrder = append(plan.CatalogOrder, tool.Name)
		plan.Catalog[tool.Name] = promptCatalogSummary(tool, descCap)
	}
	return plan
}

// RenderToolCommandCatalog renders the embedded `tool-command-catalog.md`
// template for the given entries.
func RenderToolCommandCatalog(entries []CommandEntry) string {
	// Parse header and footer directly from the embedded template so edits to
	// prompts/tool-command-catalog.md are always reflected faithfully.
	tmpl := strings.TrimRight(toolCommandCatalogTemplate, "\n")
	lines := strings.Split(tmpl, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "{{#each entries}}" {
			var body []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "{{/each}}"; i++ {
				body = append(body, lines[i])
			}
			for _, e := range entries {
				for _, bl := range body {
					line := strings.ReplaceAll(bl, "{{this.name}}", e.Name)
					line = strings.ReplaceAll(line, "{{this.summary}}", e.Summary)
					out = append(out, line)
				}
			}
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// RenderToolCommandPromptDocs renders planned tool-command prompt docs. Tools in
// listedElsewhere get no catalog line.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:329-339
func RenderToolCommandPromptDocs(plan PromptDocsPlan, listedElsewhere map[string]bool) string {
	var entries []CommandEntry
	for _, name := range plan.CatalogOrder {
		if listedElsewhere[name] {
			continue
		}
		entries = append(entries, CommandEntry{
			Name:    name,
			Summary: plan.Catalog[name],
		})
	}
	if len(entries) == 0 {
		return strings.Join(plan.Sections, "\n\n")
	}
	catalogBlock := RenderToolCommandCatalog(entries)
	parts := make([]string, 0, len(plan.Sections)+1)
	parts = append(parts, plan.Sections...)
	parts = append(parts, catalogBlock)
	return strings.Join(parts, "\n\n")
}

// ToolCommandDocsAll returns rendered help sections and catalog for mounted tools.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:342-348
func ToolCommandDocsAll(state *State, mode DocsMode, inlinePatterns []string) string {
	return RenderToolCommandPromptDocs(PlanToolCommandPromptDocs(state, mode, inlinePatterns), nil)
}

// ToolCommandDocsFor returns rendered help sections for selected mounted tools
// under the configured policy and budgets.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:351-371
func ToolCommandDocsFor(state *State, names []string, mode DocsMode, inlinePatterns []string) string {
	var sections []string
	used := 0
	for _, name := range names {
		tool := ResolveMountedToolCommand(state, name)
		if tool == nil || !shouldInlineToolCommand(state, tool, mode, inlinePatterns) {
			continue
		}
		descCap := ToolCommandExternalDescriptionCap
		if state.BuiltInNames[tool.Name] {
			descCap = 0
		}
		section := renderHelpSection(tool, descCap)
		sectionLen := utf16Len(section)
		if sectionLen > ToolCommandDocsPerToolCap || used+sectionLen > ToolCommandDocsTotalBudget {
			continue
		}
		used += sectionLen
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}
