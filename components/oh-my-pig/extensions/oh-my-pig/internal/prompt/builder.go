// Package prompt ports omp's system prompt assembly, context-file deduplication,
// and date/cwd user-turn reminder to PiG's Go extension SDK.
//
// upstream: packages/coding-agent/src/system-prompt.ts
// upstream: packages/coding-agent/src/prompts/system/system-prompt.md
// upstream: packages/coding-agent/src/prompts/system/project-prompt.md
package prompt

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

//go:embed system-prompt.md
var systemPromptTemplate string

//go:embed project-prompt.md
var projectPromptTemplate string

//go:embed active-repo-context.md
var activeRepoContextTemplate string

//go:embed date-cwd-reminder.md
var dateCwdReminderTemplate string

//go:embed personalities/default.md
var defaultPersonality string

//go:embed personalities/friendly.md
var friendlyPersonality string

//go:embed personalities/pragmatic.md
var pragmaticPersonality string

// Personality selects one of omp's bundled personality presets (or "none").
// upstream: packages/coding-agent/src/system-prompt.ts:95-99
type Personality string

const (
	PersonalityDefault   Personality = "default"
	PersonalityFriendly  Personality = "friendly"
	PersonalityPragmatic Personality = "pragmatic"
	PersonalityNone      Personality = "none"
)

// ToolInfoEntry is one entry rendered under `# Tool Inventory`.
// upstream: packages/coding-agent/src/system-prompt.ts:900-904
type ToolInfoEntry struct {
	Name  string
	Label string
}

// BuildOptions controls BuildSystemPrompt and RenderTemplate.
// upstream: packages/coding-agent/src/system-prompt.ts:490-594
type BuildOptions struct {
	// Cwd is the working directory (used for active-repo detection and context discovery).
	Cwd string
	// AgentDir is the user agent directory (used for PERSONALITY.md / RULES.md / user AGENTS.md).
	AgentDir string
	// Home overrides the user home directory (for deterministic tests).
	Home string
	// ToolNames lists active tool names from the session at run time.
	ToolNames []string
	// ToolLabels optionally maps tool name -> human label for `# Tool Inventory`.
	ToolLabels map[string]string
	// ContextFiles overrides context-file discovery when non-nil (even if empty).
	ContextFiles []ContextFile
	// Skills lists discovered skills; omitted when no skill reader (read) is active or when Hide is true.
	Skills []SkillEntry
	// Rules lists domain-scoped rules rendered under <domain-rules>.
	Rules []Rule
	// AlwaysApplyRules lists always-apply rules rendered under <generic-rules>.
	AlwaysApplyRules []AlwaysApplyRule
	// InternalURLs lists supported internal URL doc lines; omitted when empty.
	InternalURLs []string
	// Model is the provider-qualified model identifier (e.g. "anthropic/claude-opus-4").
	Model string
	// Personality selects the personality block ("default", "friendly", "pragmatic", "none").
	// Empty defaults to PersonalityDefault.
	Personality Personality
	// Environment overrides the <workstation> OS/Arch rows (for deterministic golden tests).
	Environment []EnvEntry
	// ActiveRepoContext overrides auto-detection when SetActiveRepoContext is true.
	ActiveRepoContext    *ActiveRepoContext
	SetActiveRepoContext bool
	// AppendSystemPrompt is appended after the <critical> block in project-prompt.md.
	AppendSystemPrompt string
}

// templateSlots lists every placeholder recognized in piglets/oh-my-pig/prompts/system.md.
var templateSlots = []string{
	"{{personality}}",
	"{{skills_and_rules}}",
	"{{internal_urls}}",
	"{{tool_inventory}}",
	"{{scratchpad}}",
	"{{general_delegation}}",
	"{{specialized_tools}}",
	"{{exploration}}",
	"{{ast}}",
	"{{delegation}}",
	"{{scope_prereq}}",
	"{{research_lsp}}",
	"{{decompose_todo}}",
	"{{implement_destructive}}",
	"{{project_context}}",
}

// BuildSystemPrompt renders omp's system prompt and project-context footer using
// the embedded copy of piglets/oh-my-pig/prompts/system.md.
// upstream: packages/coding-agent/src/system-prompt.ts:630-1046
func BuildSystemPrompt(opts BuildOptions) string {
	return RenderTemplate(systemPromptTemplate, opts)
}

// RenderTemplate fills the dynamic slots in tmpl (typically the contents of
// piglets/oh-my-pig/prompts/system.md received from PiG's systemPromptOptions.customPrompt)
// and formats the output with omp's post-render formatter.
func RenderTemplate(tmpl string, opts BuildOptions) string {
	personality := opts.Personality
	if personality == "" {
		personality = PersonalityDefault
	}
	personalityText := resolvePersonality(personality, opts.AgentDir)

	toolNames := dedupeStrings(opts.ToolNames)
	hasTool := func(name string) bool { return slices.Contains(toolNames, name) }

	// Skills require an active reader tool (`read`) and drop hidden skills.
	// upstream: packages/coding-agent/src/system-prompt.ts:924-939
	var visibleSkills []SkillEntry
	if hasTool("read") {
		for _, s := range opts.Skills {
			if s.Hide {
				continue
			}
			visibleSkills = append(visibleSkills, SkillEntry{
				Name:        s.Name,
				Description: PreviewSkillDescription(s.Description),
				FilePath:    s.FilePath,
			})
		}
	}

	var contextFiles []ContextFile
	if opts.ContextFiles != nil {
		contextFiles = DedupeContainedContextFiles(opts.ContextFiles)
	} else if opts.Cwd != "" || opts.AgentDir != "" {
		contextFiles = DiscoverContextFiles(opts.Cwd, opts.AgentDir, opts.Home)
	}

	sources := make([]string, 0, len(contextFiles)+1)
	if opts.AppendSystemPrompt != "" {
		sources = append(sources, opts.AppendSystemPrompt)
	}
	for _, cf := range contextFiles {
		sources = append(sources, cf.Content)
	}
	alwaysApply := dedupeAlwaysApplyRules(opts.AlwaysApplyRules, sources)

	env := opts.Environment
	if env == nil {
		env = DefaultEnvironmentInfo()
	}

	var repoCtx *ActiveRepoContext
	if opts.SetActiveRepoContext {
		repoCtx = opts.ActiveRepoContext
	} else if opts.Cwd != "" {
		repoCtx = ResolveActiveRepoContext(opts.Cwd)
	}

	slots := map[string]string{
		"{{personality}}":           renderPersonalitySection(personalityText),
		"{{skills_and_rules}}":      renderSkillsAndRules(visibleSkills, alwaysApply, opts.Rules),
		"{{internal_urls}}":         renderInternalURLs(opts.InternalURLs),
		"{{tool_inventory}}":        renderToolInventory(toolNames, opts.ToolLabels),
		"{{scratchpad}}":            renderScratchpad(hasTool),
		"{{general_delegation}}":    renderGeneralDelegation(hasTool),
		"{{specialized_tools}}":     renderSpecializedTools(hasTool),
		"{{exploration}}":           renderExploration(hasTool),
		"{{ast}}":                   renderAST(hasTool),
		"{{delegation}}":            renderDelegation(hasTool),
		"{{scope_prereq}}":          renderScopePrereq(len(visibleSkills) > 0, len(opts.Rules) > 0),
		"{{research_lsp}}":          renderResearchLSP(hasTool),
		"{{decompose_todo}}":        renderDecomposeTodo(hasTool),
		"{{implement_destructive}}": renderImplementDestructive(hasTool),
		"{{project_context}}":       renderProjectPrompt(env, opts.Model, contextFiles, hasTool, repoCtx, opts.AppendSystemPrompt),
	}

	return formatPrompt(fillSlots(tmpl, slots))
}

// fillSlots replaces each slot line in tmpl; when a slot occupies its own line and
// expands to "", the line is dropped so surrounding blank lines do not double up
// (matching Handlebars standalone-tag line stripping).
func fillSlots(tmpl string, slots map[string]string) string {
	lines := strings.Split(tmpl, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if val, isSlot := slots[trimmed]; isSlot {
			if val == "" {
				continue
			}
			out = append(out, val)
			continue
		}
		replaced := line
		for k, v := range slots {
			if strings.Contains(replaced, k) {
				replaced = strings.ReplaceAll(replaced, k, v)
			}
		}
		out = append(out, replaced)
	}
	return strings.Join(out, "\n")
}

func renderPersonalitySection(text string) string {
	if text == "" {
		return ""
	}
	return "# Personality\n" + text
}

func renderSkillsAndRules(skills []SkillEntry, alwaysApply []AlwaysApplyRule, rules []Rule) string {
	if len(skills) == 0 && len(alwaysApply) == 0 && len(rules) == 0 {
		return ""
	}
	var parts []string
	var head strings.Builder
	head.WriteString("# Skills & Rules")
	if len(skills) > 0 {
		hasSkillURLs := true
		for _, s := range skills {
			if s.FilePath != "" {
				hasSkillURLs = false
				break
			}
		}
		if hasSkillURLs {
			head.WriteString("\nMatching skill → MUST read `skill://<name>` first.\n<skills>")
		} else {
			head.WriteString("\nMatching skill → MUST read its `SKILL.md` file first.\n<skills>")
		}
		for _, s := range skills {
			head.WriteString("\n- ")
			head.WriteString(s.Name)
			if s.FilePath != "" {
				head.WriteString(" (`")
				head.WriteString(NormalizePromptPath(s.FilePath))
				head.WriteString("`)")
			}
			head.WriteString(": ")
			head.WriteString(s.Description)
		}
		head.WriteString("\n</skills>")
	}
	parts = append(parts, head.String())
	if len(alwaysApply) > 0 {
		var b strings.Builder
		b.WriteString("<generic-rules>")
		for _, r := range alwaysApply {
			b.WriteByte('\n')
			b.WriteString(r.Content)
		}
		b.WriteString("\n</generic-rules>")
		parts = append(parts, b.String())
	}
	if len(rules) > 0 {
		var b strings.Builder
		b.WriteString("<domain-rules>")
		for _, r := range rules {
			b.WriteString("\n- ")
			b.WriteString(r.Name)
			b.WriteString(" (")
			b.WriteString(strings.Join(r.Globs, ", "))
			b.WriteString("): ")
			b.WriteString(r.Description)
		}
		b.WriteString("\n</domain-rules>")
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

func renderInternalURLs(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Internal URLs\nMost FS/bash tools resolve these; path selectors: `read` docs.")
	for _, u := range urls {
		b.WriteString("\n- ")
		b.WriteString(u)
	}
	return "\n" + b.String()
}

func renderToolInventory(toolNames []string, labels map[string]string) string {
	if len(toolNames) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Tool Inventory")
	for _, name := range toolNames {
		b.WriteString("\n- ")
		if labels != nil && labels[name] != "" {
			b.WriteString(labels[name])
			b.WriteString(": `")
			b.WriteString(name)
			b.WriteByte('`')
		} else {
			b.WriteByte('`')
			b.WriteString(name)
			b.WriteByte('`')
		}
	}
	return "\n" + b.String()
}

func renderScratchpad(hasTool func(string) bool) string {
	if !hasTool("think") {
		return ""
	}
	return "\n§ Scratchpad\n`think`: private scratchpad; not shown to user. MUST use for planning; other tools become callable when it completes."
}

func renderGeneralDelegation(hasTool func(string) bool) string {
	if !hasTool("task") {
		return ""
	}
	return "- User says `parallel` or `parallelize` → MUST use `task` subagents; parallel tool calls insufficient."
}

func renderSpecializedTools(hasTool func(string) bool) string {
	var lines []string
	if hasTool("read") {
		lines = append(lines, "- File/directory reads: `read` (directory lists entries).")
	}
	if hasTool("edit") {
		lines = append(lines, "- Surgical edits: `edit`.")
	}
	if hasTool("write") {
		lines = append(lines, "- Create/overwrite: `write`.")
	}
	if hasTool("lsp") {
		lines = append(lines, "- Language server available: MUST use `lsp` for definitions, type definitions, implementations, references, hover; code actions for refactors/imports/fixes. NEVER text-search/edit for code intelligence.")
	}
	if hasTool("find") {
		searchTargets := joinRegisteredPair(hasTool("grep"), "`grep`", hasTool("glob"), "`glob`")
		if searchTargets != "" {
			lines = append(lines, "- Unknown behavior/location: descriptive `find` FIRST; NEVER guess "+searchTargets+" targets.")
		} else {
			lines = append(lines, "- Unknown behavior/location: descriptive `find` FIRST.")
		}
	}
	if hasTool("grep") {
		kind := "target"
		if hasTool("find") {
			kind = "literal/known-symbol"
		}
		lines = append(lines, "- Regex/"+kind+" search: `grep`, NEVER shell `grep`/`rg`/`awk`.")
	}
	if hasTool("glob") {
		lines = append(lines, "- File structure/names: `glob`, NEVER `ls **/*.ext`/`fd`.")
	}
	if hasTool("bash") {
		lines = append(lines, "- `bash`: real binaries/short fact pipelines (counts, frequencies, set differences, checksums), NEVER specialized-tool work or paging/moving/trimming fetchable bytes.")
	}
	if hasTool("edit") {
		via := ""
		if hasTool("bash") {
			via = " via `bash`"
		}
		lines = append(lines, "<critical>\nNEVER use `sed`|`perl`|`python`"+via+" to issue individual edits; MUST use `edit`.\n</critical>")
	}
	if len(lines) == 0 {
		return ""
	}
	return "# Specialized Tools\nMUST use specialized tool over shell equivalent:\n" + strings.Join(lines, "\n") + "\n"
}

func renderExploration(hasTool func(string) bool) string {
	var b strings.Builder
	b.WriteString("NEVER open guessed files.")
	if hasTool("find") {
		b.WriteString(" Read `find` hits only.")
	}
	if hasTool("read") {
		b.WriteString(" Use `read` ranges, not whole files.")
	}
	return b.String()
}

func renderAST(hasTool func(string) bool) string {
	if !hasTool("ast_grep") && !hasTool("ast_edit") {
		return ""
	}
	var lines []string
	lines = append(lines, "# AST", "SHOULD use syntax-aware tools before text hacks:")
	if hasTool("ast_grep") {
		lines = append(lines, "- Structural discovery → `ast_grep`.")
	}
	if hasTool("ast_edit") {
		lines = append(lines, "- Codemods → `ast_edit`.")
	}
	return "\n" + strings.Join(lines, "\n")
}

func renderDelegation(hasTool func(string) bool) string {
	if !hasTool("task") {
		return ""
	}
	return "\n" + strings.Join([]string{
		"# Delegation",
		"- Map unknown code via `task`, not reading file after file yourself. NEVER abandon phases under scope pressure: delegate, don't shrink.",
		"## Delegation gates",
		"- Before spawning, map slices/shared contracts; user-enumerated 2+ self-contained runnable slices exempt. NEVER outsource top-level plan; slice design/competing plans allowed.",
		"- Fan genuine slices in one `tasks[]` batch. NEVER pad, serialize independent work, or spawn then idle; one read-only scout while working allowed.",
		"- Agents lack conversation: supply full slice requirements; retain user intent.",
		"- Shared prerequisite inline; sequence ONLY true dependencies.",
	}, "\n")
}

func renderScopePrereq(hasSkills, hasRules bool) string {
	switch {
	case hasSkills && hasRules:
		return "- Read relevant skills and rules first."
	case hasSkills:
		return "- Read relevant skills first."
	case hasRules:
		return "- Read relevant rules first."
	default:
		return ""
	}
}

func renderResearchLSP(hasTool func(string) bool) string {
	if !hasTool("lsp") {
		return ""
	}
	return "  - Exported symbol changes: MUST run `lsp` references first."
}

func renderDecomposeTodo(hasTool func(string) bool) string {
	if !hasTool("todo") {
		return ""
	}
	return "- Update todos; skip trivial requests.\n- NEVER make a todo-only turn: send `todo` in the same response as the turn's real call (parallel tool calls); `init` with first work, `done` with next action/verification."
}

func renderImplementDestructive(hasTool func(string) bool) string {
	if hasTool("ask") {
		return "- Ask before destructive commands or deleting unrelated code you didn't write; code made obsolete by cutover is in scope."
	}
	return "- NEVER run destructive git commands or delete unrelated code you didn't write; code made obsolete by cutover is in scope."
}

func renderProjectPrompt(env []EnvEntry, model string, contextFiles []ContextFile, hasTool func(string) bool, repoCtx *ActiveRepoContext, appendPrompt string) string {
	var wsLines []string
	for _, entry := range env {
		wsLines = append(wsLines, "- "+entry.Label+": "+entry.Value)
	}
	if model != "" {
		wsLines = append(wsLines, "- Model: "+model)
	}

	repoRules := ""
	if len(contextFiles) > 0 {
		var b strings.Builder
		b.WriteString("<repo-rules>\nMUST follow these context files for all tasks:")
		for _, cf := range contextFiles {
			b.WriteString("\n<file path=\"")
			b.WriteString(cf.Path)
			b.WriteString("\">\n")
			b.WriteString(cf.Content)
			b.WriteString("\n</file>")
		}
		b.WriteString("\n</repo-rules>\n\n")
		if searchTools := joinRegisteredPair(hasTool("grep"), "`grep`", hasTool("glob"), "`glob`"); searchTools != "" {
			b.WriteString("Context files above auto-loaded. NEVER ")
			b.WriteString(searchTools)
			b.WriteString(" for `AGENTS.md`, `CLAUDE.md`, `.cursorrules`, or similar agent/context files: relevant files already in context; others noise.")
		} else {
			b.WriteString("Context files above auto-loaded. NEVER search for `AGENTS.md`, `CLAUDE.md`, `.cursorrules`, or similar agent/context files: relevant files already in context; others noise.")
		}
		repoRules = b.String()
	}

	slots := map[string]string{
		"{{workstation}}":         strings.Join(wsLines, "\n"),
		"{{repo_rules}}":          repoRules,
		"{{active_repo_context}}": renderActiveRepoContext(repoCtx),
		"{{append_prompt}}":       strings.TrimSpace(appendPrompt),
	}
	return strings.TrimSpace(formatPrompt(fillSlots(projectPromptTemplate, slots)))
}

func joinRegisteredPair(hasA bool, a string, hasB bool, b string) string {
	switch {
	case hasA && hasB:
		return a + "/" + b
	case hasA:
		return a
	case hasB:
		return b
	default:
		return ""
	}
}

func resolvePersonality(preset Personality, agentDir string) string {
	if preset == PersonalityNone {
		return ""
	}
	if override := LoadPersonalityOverride(agentDir); override != "" {
		return override
	}
	switch preset {
	case PersonalityFriendly:
		return strings.TrimSpace(friendlyPersonality)
	case PersonalityPragmatic:
		return strings.TrimSpace(pragmaticPersonality)
	default:
		return strings.TrimSpace(defaultPersonality)
	}
}

func renderActiveRepoContext(ctx *ActiveRepoContext) string {
	if ctx == nil || ctx.RelativeRepoRoot == "" {
		return ""
	}
	rendered := strings.ReplaceAll(activeRepoContextTemplate, "{{relativeRepoRoot}}", NormalizePromptPath(ctx.RelativeRepoRoot))
	return strings.TrimSpace(formatPrompt(rendered))
}

func hasTemplateSlot(text string) bool {
	for _, slot := range templateSlots {
		if strings.Contains(text, slot) {
			return true
		}
	}
	return false
}

func dedupeStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

// Register wires omp's system prompt assembly and date/cwd user-turn reminder into e.
// It performs no I/O at registration time.
func Register(e *sdk.Extension) {
	registerWithClock(e, time.Now, nil)
}

func registerWithClock(e *sdk.Extension, now func() time.Time, envOverride []EnvEntry) {
	reminder := NewDateCwdReminderInjector()

	e.OnSessionStart(func(_ sdk.Context, _ map[string]any) (any, error) {
		reminder.Reset()
		return nil, nil
	})

	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		options, _ := data["systemPromptOptions"].(map[string]any)
		base, _ := options["customPrompt"].(string)

		// Preserve an explicit user override (--system-prompt or SYSTEM.md) that replaced
		// the Piglet's prompts/system.md template.
		if base != "" && !hasTemplateSlot(base) {
			return nil, nil
		}

		cwd := ctx.Cwd()
		if optCwd, ok := options["cwd"].(string); ok && optCwd != "" {
			cwd = optCwd
		}

		agentDir := ""
		if cfgHome, err := ctx.ConfigHome(); err == nil && cfgHome != "" {
			agentDir = filepath.Join(cfgHome, "agent")
		}

		activeTools := activeToolsFromSession(ctx, options)
		contextFiles := contextFilesFromOptions(options, cwd)
		skills := skillsFromOptions(options)
		appendPrompt, _ := options["appendSystemPrompt"].(string)
		alwaysApply := DiscoverStickyRules(cwd, agentDir, "")

		tmpl := base
		if tmpl == "" {
			tmpl = systemPromptTemplate
		}

		rendered := RenderTemplate(tmpl, BuildOptions{
			Cwd:                cwd,
			AgentDir:           agentDir,
			ToolNames:          activeTools,
			ContextFiles:       contextFiles,
			Skills:             skills,
			AlwaysApplyRules:   alwaysApply,
			Model:              ctx.ModelQualified(),
			Personality:        PersonalityDefault,
			Environment:        envOverride,
			AppendSystemPrompt: appendPrompt,
		})
		return map[string]any{"systemPrompt": rendered}, nil
	})

	e.OnEvent(sdk.EventContext, func(ctx sdk.Context, data map[string]any) (any, error) {
		messages, ok := data["messages"].([]any)
		if !ok || len(messages) == 0 {
			return nil, nil
		}
		updated, changed := reminder.Transform(messages, FormatLocalCalendarDate(now()), ctx.Cwd())
		if !changed {
			return nil, nil
		}
		return map[string]any{"messages": updated}, nil
	})
}

// activeToolsFromSession takes the live active tool names from the session at run time,
// falling back to systemPromptOptions["selectedTools"] when GetActiveTools fails, and
// excluding any hiddenTools.
func activeToolsFromSession(ctx sdk.Context, options map[string]any) []string {
	var active []string
	if tools, err := ctx.GetActiveTools(); err == nil {
		active = tools
	} else {
		active = stringSliceField(options, "selectedTools")
	}
	hidden := stringSliceField(options, "hiddenTools")
	if len(hidden) == 0 {
		return active
	}
	out := make([]string, 0, len(active))
	for _, name := range active {
		if !slices.Contains(hidden, name) {
			out = append(out, name)
		}
	}
	return out
}

// contextFilesFromOptions extracts PiG's already-discovered AGENTS.md/CLAUDE.md files
// (so they are never discovered or injected twice), expands omp-style @-imports,
// and assigns directory depth relative to cwd for omp's containment deduplication.
func contextFilesFromOptions(options map[string]any, cwd string) []ContextFile {
	raw, ok := options["contextFiles"].([]any)
	if !ok {
		return nil
	}
	files := make([]ContextFile, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		if path == "" && content == "" {
			continue
		}
		expanded := content
		if path != "" {
			expanded = ExpandAtImports(content, path, "")
		}
		depth, hasDepth := contextDepth(cwd, path)
		files = append(files, ContextFile{
			Path:     path,
			Content:  expanded,
			Depth:    depth,
			HasDepth: hasDepth,
		})
	}
	return files
}

// skillsFromOptions reads PiG's already-discovered skills from systemPromptOptions["skills"],
// preserving DisableModelInvocation as omp's Hide flag so skills are never injected twice.
func skillsFromOptions(options map[string]any) []SkillEntry {
	raw, ok := options["skills"].([]any)
	if !ok {
		return nil
	}
	skills := make([]SkillEntry, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := m["description"].(string)
		filePath, _ := m["filePath"].(string)
		disabled, _ := m["disableModelInvocation"].(bool)
		skills = append(skills, SkillEntry{
			Name:        name,
			Description: desc,
			FilePath:    filePath,
			Hide:        disabled,
		})
	}
	return skills
}

func stringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		return fmt.Sprint(x)
	}
}
