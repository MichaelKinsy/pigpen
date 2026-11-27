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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
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

// ToolCommands supplies mounted bash tool-command names and their rendered docs
// for `# Tool Commands`. Nil by default; the integrator wires toolcmd docs here.
// upstream: packages/coding-agent/src/system-prompt.ts:772-773,1020-1038
var ToolCommands func(ctx sdk.Context) (mounted []string, docs string)

// SubagentSession reports whether ctx belongs to a child (subagent) session.
// Nil by default; the integrator assigns it from internal/agents. When true,
// the hook renders with Subagent: true and PersonalityNone (upstream sdk.ts:4679-4680).
var SubagentSession func(ctx sdk.Context) bool

// Intent supplies whether intent tracing is active for ctx, the parameter field
// name, and optional custom guidance. Nil by default; the integrator assigns it
// from internal/intent. Must follow `tools.intentTracing` (or interactive host
// when unset) only, NOT `PI_NO_INTENT` (upstream sdk.ts:4655, agent-loop.ts:1085).
// PI_INTENT_PROMPT overrides guidance (upstream system-prompt.ts:1138-1140).
var Intent func(ctx sdk.Context) (enabled bool, field string, guidance string)

// SettingsApproval reports whether interactive `cfg://` writes can be approved in
// ctx, feeding urls.SchemeHost.SettingsApproval. Nil by default (false); the
// integrator assigns it (true in interactive top-level TUI sessions, false in
// print/RPC/subagent sessions; upstream sdk.ts:4673, cfg-protocol.ts:352-354,
// main.ts:2445).
var SettingsApproval func(ctx sdk.Context) bool

// OutReplies reports whether `out://` mid-turn replies are active in ctx, feeding
// urls.SchemeHost.OutReplies. Nil by default (false); the integrator assigns it
// (explicit `out.enabled` if configured, else interactive top-level TUI, never
// subagents; upstream sdk.ts:4674, out-protocol.ts:45-47,77-79, main.ts:2446).
var OutReplies func(ctx sdk.Context) bool

// TaskIRC reports whether subagent IRC peer messaging is enabled for ctx, gating
// the `B asks A via write agent://<id>` clause in Delegation gates. Nil by default
// (defaults to true when `task` is active, matching omp's default
// `task.maxRecursionDepth: 2` where top-level and subagent sessions both have
// isIrcEnabled=true); the integrator may assign it from settings/agents
// (`isIrcEnabled(settings, taskDepth)`: true when taskDepth > 0 or
// canSpawnAtDepth(task.maxRecursionDepth, taskDepth); upstream sdk.ts:4664,
// irc/messaging.ts:16-20).
var TaskIRC func(ctx sdk.Context) bool

// IncludeWorkspaceTree reports whether `<workspace-tree>` and `<dir-context>`
// should be scanned and injected into `<project-context>` (setting `includeWorkspaceTree`,
// default false in omp v19.1.1). Nil by default (false).
// upstream: packages/coding-agent/src/system-prompt.ts:765,904-908
var IncludeWorkspaceTree func(ctx sdk.Context) bool

// Skillful reports whether available skills should be listed in the system
// prompt for ctx's session (`skillful` setting, toggled via `/skillful`).
// When nil, defaults to true (preserving default-session behavior).
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:564-617
var Skillful func(ctx sdk.Context) bool

// Personality selects one of omp's bundled personality presets (or "none").
// upstream: packages/coding-agent/src/system-prompt.ts:125-130
type Personality string

const (
	PersonalityDefault   Personality = "default"
	PersonalityFriendly  Personality = "friendly"
	PersonalityPragmatic Personality = "pragmatic"
	PersonalityNone      Personality = "none"
)

// ToolInfoEntry is one entry rendered under `# Tool Inventory`.
// upstream: packages/coding-agent/src/system-prompt.ts:1034-1038
type ToolInfoEntry struct {
	Name  string
	Label string
}

// BuildOptions controls BuildSystemPrompt and RenderTemplate.
// upstream: packages/coding-agent/src/system-prompt.ts:526-650
type BuildOptions struct {
	// Cwd is the working directory (used for active-repo detection and context discovery).
	Cwd string
	// AgentDir is the user agent directory (used for PERSONALITY.md / RULES.md / user AGENTS.md).
	AgentDir string
	// Home overrides the user home directory (for deterministic tests).
	Home string
	// ToolNames lists active tool names from the session at run time.
	ToolNames []string
	// ToolLabels optionally maps tool name -> human label (retained for caller compatibility).
	ToolLabels map[string]string
	// MountedCommands lists tool names mounted as bash commands; they count as present
	// for {{#has tools ...}} gates but are excluded from # Tool Inventory.
	MountedCommands []string
	// ToolCommandDocs is the rendered documentation body under # Tool Commands.
	ToolCommandDocs string
	// ContextFiles overrides context-file discovery when non-nil (even if empty).
	ContextFiles []ContextFile
	// Skills lists discovered skills; omitted when no skill reader (read) is active or when Hide is true.
	Skills []SkillEntry
	// Rules lists domain-scoped rules rendered under <domain-rules>.
	Rules []Rule
	// AlwaysApplyRules lists always-apply rules rendered under <generic-rules>.
	AlwaysApplyRules []AlwaysApplyRule
	// InternalURLs overrides urls.Describe(host) when non-nil (even if empty).
	InternalURLs []string
	// InternalURLReference overrides urls.Reference() when non-nil; rendered when `read` is not active.
	InternalURLReference []string
	// EvalPreludes overrides eval.EnabledPreludes(ctx) when non-nil (even if empty).
	EvalPreludes []eval.Prelude
	// Context is the session sdk.Context passed to session-aware hooks such as
	// eval.Prelude.GuidanceText(ctx).
	Context sdk.Context
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
	// Subagent switches Workflow #2/#3 and project-prompt <critical> to subagent hand-off wording.
	Subagent bool
	// DelegationBias selects the Delegation wording ("balanced" default, "restrained", "eager", "gated").
	DelegationBias string
	// EagerTasks enables eager delegation wording when `task` is active.
	EagerTasks bool
	// EagerTasksAlways enables mandatory delegation wording when EagerTasks is also true.
	EagerTasksAlways bool
	// TaskBatch toggles "in one `tasks[]` batch" vs "in parallel calls" (default true when nil).
	TaskBatch *bool
	// ScoutAvailable toggles scout reference in balanced/eager delegation (default true when nil).
	ScoutAvailable *bool
	// TaskIRCEnabled appends the IRC peer-messaging clause in Delegation gates.
	TaskIRCEnabled bool
	// EncourageEdits controls the `<critical>` edit-tool block (default true when nil).
	EncourageEdits *bool
	// Visuals and reactions flags (all default false in oh-my-pig; omp's settings default renderMermaid/Svg/Embeds/reactions to true and autoGraph to "auto").
	RenderMermaid bool
	RenderSVG     bool
	RenderEmbeds  bool
	AutoGraph     bool
	Reactions     bool
	// IntentTracing enables the intent-field bullet under # Tool I/O.
	IntentTracing  bool
	IntentField    string
	IntentGuidance string
	// SecretsEnabled enables the opaque secret-token bullet under # Tool I/O.
	SecretsEnabled bool
	// ResultShaping is optional extra guidance under # Tool I/O.
	ResultShaping string
	// AutoQAEnabled enables the automated QA `<critical>` block.
	AutoQAEnabled bool
	// Ovnfs enables the `# Separate checkouts` section when `bash` is active.
	Ovnfs string
	// SettingsApproval advertises `cfg://` in # Internal URLs (feeds urls.SchemeHost).
	SettingsApproval bool
	// OutReplies advertises `out://` in # Internal URLs (feeds urls.SchemeHost).
	OutReplies bool
	// IncludeWorkspaceTree enables scanning and rendering <workspace-tree> and <dir-context>.
	// Defaults to false, matching omp's `includeWorkspaceTree` setting.
	IncludeWorkspaceTree bool
	// WorkspaceTree overrides BuildWorkspaceTree when non-nil (for deterministic tests).
	WorkspaceTree *WorkspaceTree
}

// templateSlots lists every placeholder recognized in piglets/oh-my-pig/prompts/system.md.
var templateSlots = []string{
	"{{visuals}}",
	"{{reactions}}",
	"{{personality}}",
	"{{skills_and_rules_heading}}",
	"{{skills_block}}",
	"{{always_apply_rules_block}}",
	"{{domain_rules_block}}",
	"{{internal_urls_body}}",
	"{{tool_inventory}}",
	"{{tool_commands}}",
	"{{scratchpad}}",
	"{{intent_line}}",
	"{{secrets_line}}",
	"{{result_shaping}}",
	"{{specialized_read}}",
	"{{specialized_write}}",
	"{{specialized_lsp}}",
	"{{specialized_find}}",
	"{{edit_critical}}",
	"{{auto_qa}}",
	"{{exploration}}",
	"{{ovnfs}}",
	"{{delegation}}",
	"{{before_editing_prereq}}",
	"{{before_editing_lsp}}",
	"{{before_editing_todo}}",
	"{{before_editing_destructive}}",
	"{{workflow_step2}}",
	"{{workflow_cleanup}}",
}

// inlineSlots are placeholders that correspond to inline Handlebars blocks
// (`{{#if ...}}...{{/if}}` on one line) which leave an empty line when untaken,
// unlike standalone blocks whose empty result drops the line.
var inlineSlots = map[string]bool{
	"{{intent_line}}":       true,
	"{{secrets_line}}":      true,
	"{{specialized_read}}":  true,
	"{{specialized_write}}": true,
	"{{model_line}}":        true,
}

// BuildSystemPrompt renders omp's system prompt, eval prelude guidance blocks,
// and project-context footer using the embedded copy of piglets/oh-my-pig/prompts/system.md.
// upstream: packages/coding-agent/src/system-prompt.ts:722-1206
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

	mountedCommands := dedupeStrings(opts.MountedCommands)
	// Mounted-only commands satisfy {{#has tools ...}} gates while staying out of
	// # Tool Inventory (matching system-prompt.ts:1020-1033).
	rawToolNames := dedupeStrings(opts.ToolNames)
	inventoryTools := make([]string, 0, len(rawToolNames))
	for _, name := range rawToolNames {
		if !slices.Contains(mountedCommands, name) {
			inventoryTools = append(inventoryTools, name)
		}
	}
	allTools := dedupeStrings(append(append([]string(nil), rawToolNames...), mountedCommands...))
	hasTool := func(name string) bool { return slices.Contains(allTools, name) }
	// Skills require an active reader tool (`read`) and drop hidden skills.
	// upstream: packages/coding-agent/src/system-prompt.ts:1058-1076
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

	internalURLs := opts.InternalURLs
	if internalURLs == nil {
		host := urls.SchemeHost{
			SkillURIAccess:   hasTool("read") && len(opts.Skills) > 0,
			RuleCount:        len(opts.Rules),
			SettingsApproval: opts.SettingsApproval,
			OutReplies:       opts.OutReplies,
			Subagent:         opts.Subagent,
		}
		internalURLs = urls.Describe(host)
	}
	internalURLRef := opts.InternalURLReference
	if internalURLRef == nil && !hasTool("read") {
		internalURLRef = urls.Reference()
	}

	preludes := opts.EvalPreludes
	browserEnabled := false
	computerEnabled := false
	for _, p := range preludes {
		if p.Name == "browser" {
			browserEnabled = true
		}
		if p.Name == "computer" {
			computerEnabled = true
		}
	}

	encourageEdits := true
	if opts.EncourageEdits != nil {
		encourageEdits = *opts.EncourageEdits
	}

	slots := map[string]string{
		"{{visuals}}":                    renderVisuals(opts),
		"{{reactions}}":                  renderReactions(opts.Reactions),
		"{{personality}}":                renderPersonalitySection(personalityText),
		"{{skills_and_rules_heading}}":   renderSkillsAndRulesHeading(visibleSkills, alwaysApply, opts.Rules),
		"{{skills_block}}":               renderSkillsBlock(visibleSkills),
		"{{always_apply_rules_block}}":   renderAlwaysApplyRulesBlock(alwaysApply),
		"{{domain_rules_block}}":         renderDomainRulesBlock(opts.Rules),
		"{{internal_urls_body}}":         renderInternalURLsBody(internalURLs, internalURLRef, hasTool("read")),
		"{{tool_inventory}}":             renderToolInventory(inventoryTools),
		"{{tool_commands}}":              renderToolCommands(mountedCommands, opts.ToolCommandDocs),
		"{{scratchpad}}":                 renderScratchpad(hasTool),
		"{{intent_line}}":                renderIntentLine(opts),
		"{{secrets_line}}":               renderSecretsLine(opts.SecretsEnabled),
		"{{result_shaping}}":             strings.TrimRight(opts.ResultShaping, "\n"),
		"{{specialized_read}}":           renderSpecializedRead(hasTool),
		"{{specialized_write}}":          renderSpecializedWrite(hasTool),
		"{{specialized_lsp}}":            renderSpecializedLSP(hasTool),
		"{{specialized_find}}":           renderSpecializedFind(hasTool),
		"{{edit_critical}}":              renderEditCritical(encourageEdits, hasTool),
		"{{auto_qa}}":                    renderAutoQA(opts.AutoQAEnabled),
		"{{exploration}}":                renderExploration(hasTool),
		"{{ovnfs}}":                      renderOvnfs(opts.Ovnfs, hasTool),
		"{{delegation}}":                 renderDelegation(opts, hasTool),
		"{{before_editing_prereq}}":      renderBeforeEditingPrereq(len(visibleSkills) > 0, len(opts.Rules) > 0),
		"{{before_editing_lsp}}":         renderBeforeEditingLSP(hasTool),
		"{{before_editing_todo}}":        renderBeforeEditingTodo(hasTool),
		"{{before_editing_destructive}}": renderBeforeEditingDestructive(hasTool),
		"{{workflow_step2}}":             renderWorkflowStep2(opts.Subagent, browserEnabled, computerEnabled),
		"{{workflow_cleanup}}":           renderWorkflowCleanup(opts.Subagent),
	}

	// Render system-prompt, prelude guidance, and project-prompt as separate blocks
	// matching upstream buildSystemPrompt (system-prompt.ts:1179-1196), then join
	// with "\n\n" for PiG's single-string systemPrompt hook.
	blocks := []string{formatPrompt(fillSlots(tmpl, slots))}
	for _, p := range preludes {
		if text := strings.TrimSpace(p.GuidanceText(opts.Context)); text != "" {
			blocks = append(blocks, text)
		}
	}
	var wsTree WorkspaceTree
	if opts.WorkspaceTree != nil {
		wsTree = *opts.WorkspaceTree
	} else if opts.IncludeWorkspaceTree && opts.Cwd != "" {
		wsTree = BuildWorkspaceTree(opts.Cwd, BuildWorkspaceTreeOptions{Timeout: 5 * time.Second})
	}
	if proj := renderProjectPrompt(env, opts.Model, contextFiles, opts.Subagent, repoCtx, opts.AppendSystemPrompt, opts.IncludeWorkspaceTree, wsTree, hasTool("glob"), hasTool("read")); proj != "" {
		blocks = append(blocks, proj)
	}
	return strings.Join(blocks, "\n\n")
}

// fillSlots replaces each slot line in tmpl. Standalone block slots drop their
// line when empty (matching Handlebars stripStandalone); inline block slots leave
// an empty line when empty, which formatPrompt then preserves or collapses.
func fillSlots(tmpl string, slots map[string]string) string {
	lines := strings.Split(tmpl, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if val, isSlot := slots[trimmed]; isSlot {
			if val == "" {
				if inlineSlots[trimmed] {
					out = append(out, "")
				}
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

func renderVisuals(opts BuildOptions) string {
	if !opts.RenderMermaid && !opts.RenderSVG && !opts.RenderEmbeds && !opts.AutoGraph {
		return ""
	}
	var b strings.Builder
	b.WriteString("- Visuals: first fit wins; one form per idea, NEVER the same content twice.")
	if opts.AutoGraph {
		b.WriteString("\n  - Numbers to compare? Markdown table; the UI charts it underneath. NEVER also chart that data in mermaid or svg.")
	}
	if opts.RenderMermaid {
		b.WriteString("\n  - Flow, sequence, state, or dependencies? ` ```mermaid `; terminal renders ASCII. Genuine structure only, not trivia.")
	}
	if opts.RenderSVG {
		var lead string
		switch {
		case opts.RenderMermaid && !opts.AutoGraph:
			lead = "Mermaid can't draw it (layout, mockup, geometry, chart)?"
		case opts.RenderMermaid:
			lead = "Mermaid can't draw it (layout, mockup, geometry)?"
		case opts.AutoGraph:
			lead = "Diagram or mockup says more than prose?"
		default:
			lead = "Diagram, chart, or mockup says more than prose?"
		}
		b.WriteString("\n  - " + lead + " You SHOULD draw it in a ` ```svg ` block; it renders inline as an image. Reader sees it: NEVER announce or restate it, add only what it doesn't say.")
		b.WriteString("\n    - viewBox sets size: 1 unit ≈ 1px, font-size 14 ≈ body text, ≤1000 wide fits the screen.")
		b.WriteString("\n    - Colors ONLY via currentColor and var(--fg), --muted, --border, --surface, --accent, --success, --warning, --error; series --c1…--c6: the reader's theme. NEVER paint a background.")
		b.WriteString("\n    - Static, self-contained: NEVER scripts, animation, foreignObject, external href.")
	}
	if opts.RenderEmbeds {
		b.WriteString("\n  - Picture that exists (file, URL) or a web page to show? `<img src=\"…\" alt=\"…\">` / `<iframe src=\"…\" title=\"…\"></iframe>`, alone on its line; renders inline, a page live or as one screenshot, plus a link.")
		b.WriteString("\n    - `src`: an http(s) URL, a data: URI, or a file path relative to the working directory; `<iframe>` takes a URL or a local HTML file.")
		b.WriteString("\n    - Size: OPTIONAL `width`/`height` in px, %, vw, vh, ch, em (`width=\"50%\"`); one side keeps the aspect ratio.")
	}
	b.WriteString("\n  - Otherwise: table or prose.")
	return b.String()
}

func renderReactions(enabled bool) string {
	if !enabled {
		return ""
	}
	return "- MAY react to the user when chatting: start reply with emoji."
}

func renderSkillsAndRulesHeading(skills []SkillEntry, alwaysApply []AlwaysApplyRule, rules []Rule) string {
	if len(skills) == 0 && len(alwaysApply) == 0 && len(rules) == 0 {
		return ""
	}
	return "# Skills & Rules"
}

func renderSkillsBlock(skills []SkillEntry) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Matching skill → MUST read skill://<name> first.\n<skills>")
	for _, s := range skills {
		b.WriteString("\n- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Description)
	}
	b.WriteString("\n</skills>")
	return b.String()
}

func renderAlwaysApplyRulesBlock(alwaysApply []AlwaysApplyRule) string {
	if len(alwaysApply) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<generic-rules>")
	for _, r := range alwaysApply {
		b.WriteByte('\n')
		b.WriteString(r.Content)
	}
	b.WriteString("\n</generic-rules>")
	return b.String()
}

func renderDomainRulesBlock(rules []Rule) string {
	if len(rules) == 0 {
		return ""
	}
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
	return b.String()
}

func renderInternalURLsBody(docLines, refLines []string, hasRead bool) string {
	var b strings.Builder
	b.WriteString("Most FS/bash tools resolve these; path selectors: `read` docs.")
	if hasRead {
		b.WriteString(" Further sub-paths, queries, limits: help://read/urls.")
	}
	for _, u := range docLines {
		b.WriteString("\n- ")
		b.WriteString(u)
	}
	// Upstream system-prompt.md:72 has `{{#has tools "read"}}{{else}}\n`: the
	// newline sits inside the else branch, so it is emitted only when `read` is
	// absent (creating a blank line before reference lines, or merging with the
	// following blank line into a stripped 2-blank run when refLines is empty).
	if !hasRead {
		b.WriteByte('\n')
		for _, ref := range refLines {
			b.WriteString("\n- ")
			b.WriteString(ref)
		}
	}
	return b.String()
}

func renderToolInventory(toolNames []string) string {
	if len(toolNames) == 0 {
		return ""
	}
	return "# Tool Inventory\n" + strings.Join(toolNames, ", ")
}

func renderToolCommands(mounted []string, docs string) string {
	if len(mounted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Tool Commands\n")
	b.WriteString("Each listed tool is also a command in `bash`: run `<tool> <positional> [--flag value]`; `<tool> --help` prints usage derived from its schema.\n")
	b.WriteString("Raw JSON arguments object: `-j '<json>'`, `@file`, or `-` (stdin); bad arguments exit 2 with a usage hint.\n")
	b.WriteString(docs)
	return b.String()
}

func renderScratchpad(hasTool func(string) bool) string {
	if !hasTool("think") {
		return ""
	}
	return "⁂ Scratchpad\n`think`: private scratchpad; not shown to user. MUST use for planning; other tools become callable when it completes."
}

func renderIntentLine(opts BuildOptions) string {
	if !opts.IntentTracing || opts.IntentField == "" {
		return ""
	}
	guidance := os.Getenv("PI_INTENT_PROMPT")
	if guidance == "" {
		guidance = opts.IntentGuidance
	}
	if guidance == "" {
		guidance = "capitalized 2–6-word present-participle intent (e.g. \"Reading model role settings\")."
	}
	return "- Most tools take `" + opts.IntentField + "`: " + guidance
}

func renderSecretsLine(enabled bool) string {
	if !enabled {
		return ""
	}
	return "- `$$HASH$$`, `$$HASH:CASE$$`, `$$NAME_HASH:CASE$$` output tokens: opaque strings."
}

func renderSpecializedRead(hasTool func(string) bool) string {
	if !hasTool("read") {
		return ""
	}
	return "- File/directory reads: `read` (directory lists entries)."
}

func renderSpecializedWrite(hasTool func(string) bool) string {
	if !hasTool("write") {
		return ""
	}
	return "- Create/overwrite: `write`."
}

func renderSpecializedLSP(hasTool func(string) bool) string {
	if !hasTool("lsp") {
		return ""
	}
	return "- Language server available: MUST use `lsp` for definitions, type definitions, implementations, references, hover; code actions for refactors/imports/fixes. NEVER text-search/edit for code intelligence."
}

func renderSpecializedFind(hasTool func(string) bool) string {
	if !hasTool("find") {
		return ""
	}
	return "- Unknown behavior/location: descriptive `find` FIRST; NEVER guess `grep`/`glob` targets."
}

func renderEditCritical(encourageEdits bool, hasTool func(string) bool) string {
	if !encourageEdits || !hasTool("edit") {
		return ""
	}
	suffix := ""
	if hasTool("bash") || hasTool("eval") {
		suffix = "; NEVER sed/perl/python edits"
	}
	return "<critical>\nFile edits: MUST use `edit`" + suffix + ".\n</critical>"
}

func renderAutoQA(enabled bool) string {
	if !enabled {
		return ""
	}
	return "<critical>\nAutomated QA: any tool output inconsistent with the described behavior for its parameters → run `report_issue '<tool>: <concise description>'`. False positives fine.\n</critical>"
}

func renderExploration(hasTool func(string) bool) string {
	var b strings.Builder
	b.WriteString("NEVER open guessed files.")
	if hasTool("find") {
		b.WriteString(" Read `find` hits only.")
	}
	if hasTool("read") {
		b.WriteString(" Read small files whole; ranges only for large files. NEVER re-read content you already have.")
	}
	return b.String()
}

// quotePosixArgument mirrors packages/coding-agent/src/utils/shell-quote.ts:4-6.
func quotePosixArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func renderOvnfs(ovnfs string, hasTool func(string) bool) string {
	if ovnfs == "" || !hasTool("bash") {
		return ""
	}
	quoted := quotePosixArgument(ovnfs)
	return strings.Join([]string{
		"# Separate checkouts",
		"Need a second copy of a folder (risky experiment, another branch, tests or review beside the user's work)? MUST use an ovnfs checkout, not git worktree add, git clone or cp -r, unless the user asks for those:",
		"- `cd <folder> && " + quoted + " new --name <name>` (lowercase a-z0-9._-) prints only the checkout's path, in about a second: the folder as it is now, uncommitted edits, untracked files and build dirs included. --rev <rev> starts tracked files at a commit or branch instead, without in-flight edits (to verify your patch); ignored build dirs stay.",
		"- Work in the printed path: writes and commits stay in the checkout; git commands that discard changes run there, saving the discards first.",
		"- Back: `" + quoted + " diff <name> [-p]` lists changes; `" + quoted + " apply <name> [--dry-run]` merges them into the folder as uncommitted edits; `" + quoted + " land <name>` puts the checkout's commits on the folder's branch, files included. Conflict? `" + quoted + " pull <name>` merges the folder's newer files into the checkout: resolve there, then apply or land again.",
		"- Done: `" + quoted + " rm <name>` discards what was not applied or landed; `" + quoted + " ls` lists checkouts; more in `" + quoted + " help`.",
	}, "\n")
}
func renderDelegation(opts BuildOptions, hasTool func(string) bool) string {
	if !hasTool("task") {
		return ""
	}
	bias := opts.DelegationBias
	if bias == "" {
		bias = "balanced"
	}
	tier := bias
	if opts.EagerTasks && bias != "gated" {
		tier = "eager"
	}
	taskBatch := true
	if opts.TaskBatch != nil {
		taskBatch = *opts.TaskBatch
	}
	scoutAvailable := true
	if opts.ScoutAvailable != nil {
		scoutAvailable = *opts.ScoutAvailable
	}

	var lines []string
	lines = append(lines, "# Delegation")
	if tier == "gated" {
		if opts.EagerTasks {
			lines = append(lines, "Proactive multi-agent delegation active; earlier explicit-user-request gates no longer apply. Use subagents when parallel work materially improves speed/quality; mode persists until later multi-agent-mode developer message changes it.")
		} else {
			lines = append(lines, "No subagents unless user or applicable AGENTS.md/skill explicitly requests subagents, delegation, or parallel agent work.")
		}
	} else {
		if opts.EagerTasks {
			if opts.EagerTasksAlways {
				lines = append(lines, "Delegation default. Once design settles, MUST fan work to `task`, except ONLY: approximately-under-30-line single-file edit; direct answer/explanation without code changes; or user explicitly asks you to run a command. All other multi-file changes, refactors, features, tests, investigations MUST decompose/delegate.")
			} else {
				lines = append(lines, "Delegation preferred. Once design settles, SHOULD fan substantial work to `task`; multi-file changes, refactors, features, tests, investigations strong candidates. Judge small single-file/interactive work.")
			}
		}
		switch tier {
		case "restrained":
			findPrefix := ""
			if hasTool("find") {
				findPrefix = "`find`/"
			}
			waitClause := ""
			if hasTool("wait") {
				waitClause = "; use `wait` only when completely blocked"
			}
			lines = append(lines,
				"Inline first. Fan out only when 2+ independent slices each cost more than a handful of your own calls, or the read set would flood context; decide after your own first "+findPrefix+"`grep`/`read`, never before it.",
				"- NEVER open with a scout. Scope with "+findPrefix+"`grep`/`read`/`glob` yourself; a scout is for a genuinely unmapped subsystem after inline scoping stalls.",
				"- NEVER delegate one slice. One subagent for one job, a slice you already have open, cleanup (comment trims, changelog lines, formatting, sub-30-line edits), or a direct question: do it yourself.",
				"- NEVER babysit. Spawn → keep working → read the auto-delivered result"+waitClause+".",
			)
		case "balanced":
			explorer := "a `task` subagent"
			if scoutAvailable {
				explorer = "a read-only `scout`"
			}
			lines = append(lines, "- Unfamiliar subsystem? Explore it with "+explorer+" while you keep working; read known files yourself.")
		default:
			lines = append(lines, "- Map unknown code via `task`, not reading file after file yourself. NEVER abandon phases under scope pressure: delegate, don't shrink.")
		}
	}

	fanMode := "in parallel calls"
	if taskBatch {
		fanMode = "in one `tasks[]` batch"
	}
	scoutSuffix := ""
	if scoutAvailable && tier == "eager" {
		scoutSuffix = "; one read-only scout while working allowed"
	}
	ircLine := "- Shared prerequisite inline; sequence ONLY true dependencies."
	if opts.TaskIRCEnabled {
		ircLine += " Small missing detail? Run parallel; B asks A via `write agent://<id>` only if blocked."
	}
	lines = append(lines,
		"## Delegation gates",
		"- Before spawning, map slices/shared contracts; user-enumerated 2+ self-contained runnable slices exempt. NEVER outsource top-level plan; slice design/competing plans allowed.",
		"- Fan genuine slices "+fanMode+". NEVER pad, serialize independent work, or spawn then idle"+scoutSuffix+".",
		"- Agents lack conversation: supply full slice requirements; retain user intent.",
		ircLine,
	)
	return strings.Join(lines, "\n")
}

func renderBeforeEditingPrereq(hasSkills, hasRules bool) string {
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

func renderBeforeEditingLSP(hasTool func(string) bool) string {
	if !hasTool("lsp") {
		return ""
	}
	return "- Exported symbol changes: MUST run `lsp` references first."
}

func renderBeforeEditingTodo(hasTool func(string) bool) string {
	if !hasTool("todo") {
		return ""
	}
	return "- Update todos; skip trivial requests."
}

func renderBeforeEditingDestructive(hasTool func(string) bool) string {
	if hasTool("ask") {
		return "- Ask before destructive commands or deleting unrelated code you didn't write; code your change makes obsolete is in scope."
	}
	return "- NEVER run destructive git commands or delete unrelated code you didn't write; code your change makes obsolete is in scope."
}

func renderWorkflowStep2(subagent, browserEnabled, computerEnabled bool) string {
	if subagent {
		return strings.Join([]string{
			"# 2. Hand-off",
			"- NEVER run builds, tests, linters, formatters or smoke runs unless your assignment says to; the main agent verifies once all subagents land.",
			"- Done → yield, naming the checks to run.",
		}, "\n")
	}
	lines := []string{
		"# 2. Verify",
		"Run the narrowest check that exercises the change, once. Investigation: the output is the proof; no tests.",
		"- UI: verify the actual surface.",
	}
	if browserEnabled {
		lines = append(lines, "  - Web: open it in a browser tab; visual proof.")
	}
	if computerEnabled {
		lines = append(lines, "  - Native desktop: JS/Python eval `computer` helpers; fresh screenshot/accessibility proof.")
	}
	lines = append(lines, "  - TUI/CLI: launch the program; observe output/state.")
	if !browserEnabled || !computerEnabled {
		lines = append(lines, "  - No runtime for changed surface: throwaway script; report visual limit.")
	}
	lines = append(lines,
		"- Bug: a failing test or user report is the repro; add a regression test only when no existing test covers the path.",
		"- Feature: prove via throwaway script; new tests ONLY for uncertain edges or on request.",
		"- New tests MUST catch plausible consumer-visible bugs; deterministic, isolated, full-suite-safe. NEVER test wiring, echoes, source text, incidental defaults, tautologies, bare not-throw, length-grew.",
		"- Tests your change breaks: update contract tests; delete wording/implementation ones regardless of author, NEVER re-pin.",
	)
	return strings.Join(lines, "\n")
}

func renderWorkflowCleanup(subagent bool) string {
	if subagent {
		return "Permanent fix/feature: update docs/changelog; delete throwaway scripts."
	}
	return "After smoke proof: permanent fix/feature: update docs/changelog; delete throwaway scripts."
}

func renderProjectPrompt(env []EnvEntry, model string, contextFiles []ContextFile, subagent bool, repoCtx *ActiveRepoContext, appendPrompt string, includeWorkspaceTree bool, wsTree WorkspaceTree, hasGlob, hasRead bool) string {
	var wsLines []string
	for _, entry := range env {
		wsLines = append(wsLines, "- "+entry.Label+": "+entry.Value)
	}
	modelLine := ""
	if model != "" {
		modelLine = "- Model: " + model
	}

	repoRules := ""
	if len(contextFiles) > 0 {
		var b strings.Builder
		b.WriteString("<repo-rules>\nMUST follow these context files for all tasks:")
		for _, cf := range contextFiles {
			b.WriteString("\n<file path=\"")
			b.WriteString(cf.Path)
			b.WriteString("\">\n")
			// Close any unclosed fence so the rest of the prompt stays formatted.
			// upstream: packages/coding-agent/src/system-prompt.ts:1092
			b.WriteString(CloseCodeFence(cf.Content))
			b.WriteString("\n</file>")
		}
		b.WriteString("\n</repo-rules>")
		repoRules = b.String()
	}

	dirContext := RenderDirContext(wsTree.AgentsMdFiles)
	autoloadNote := ""
	if len(contextFiles) > 0 || len(wsTree.AgentsMdFiles) > 0 {
		autoloadNote = "Context files above auto-loaded. NEVER `grep`/`glob` for `AGENTS.md`, `CLAUDE.md`, `.cursorrules`, or similar agent/context files: relevant files already in context; others noise."
	}
	workspaceTreeBlock := ""
	if includeWorkspaceTree {
		workspaceTreeBlock = RenderWorkspaceTreeBlock(wsTree, hasGlob, hasRead)
	}

	criticalTail := "- Before yielding, MUST verify significant behavioral changes: run the specific test, command, or scenario covering the change."
	if subagent {
		criticalTail = "- Changes complete → yield; verification is main agent's job. NEVER run it yourself unless your assignment explicitly instructs it."
	}

	slots := map[string]string{
		"{{workstation}}":           strings.Join(wsLines, "\n"),
		"{{model_line}}":            modelLine,
		"{{repo_rules}}":            repoRules,
		"{{dir_context}}":           dirContext,
		"{{context_autoload_note}}": autoloadNote,
		"{{workspace_tree}}":        workspaceTreeBlock,
		"{{workspace_roots}}":       "",
		"{{active_repo_context}}":   renderActiveRepoContext(repoCtx),
		"{{project_critical_tail}}": criticalTail,
		"{{append_prompt}}":         appendPrompt,
	}
	return strings.TrimSpace(formatPrompt(fillSlots(projectPromptTemplate, slots)))
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

// Package-level per-session reminder registry shared across extension instances
// so lifecycle.OnEnd("prompt", releaseSessionReminder) releases any session (top-level
// or child) even after Register has run again for later sessions.
var (
	sessionRemindersMu sync.Mutex
	sessionReminders   = map[string]*DateCwdReminderInjector{}
)

func reminderForSession(sessionID string) *DateCwdReminderInjector {
	sessionRemindersMu.Lock()
	defer sessionRemindersMu.Unlock()
	inj := sessionReminders[sessionID]
	if inj == nil {
		inj = NewDateCwdReminderInjector()
		sessionReminders[sessionID] = inj
	}
	return inj
}

func releaseSessionReminder(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionRemindersMu.Lock()
	delete(sessionReminders, sessionID)
	sessionRemindersMu.Unlock()
}

// Register wires omp's system prompt assembly and date/cwd user-turn reminder into e.
// It performs no I/O at registration time.
func Register(e *sdk.Extension) {
	registerWithClock(e, time.Now, nil)
}

func registerWithClock(e *sdk.Extension, now func() time.Time, envOverride []EnvEntry) {
	// Fallback injector for an extension instance whose host supplies no session ID;
	// kept strictly local to this Extension so it is never shared across sessions.
	fallbackReminder := NewDateCwdReminderInjector()

	lifecycle.OnEnd("prompt", releaseSessionReminder)

	injectorFor := func(ctx sdk.Context) *DateCwdReminderInjector {
		if id, err := ctx.GetSessionID(); err == nil && id != "" {
			return reminderForSession(id)
		}
		return fallbackReminder
	}

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.GetSessionID(); err == nil && id != "" {
			releaseSessionReminder(id)
		} else {
			fallbackReminder.Reset()
		}
		return nil, nil
	})

	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.GetSessionID(); err == nil && id != "" {
			lifecycle.End(id)
		} else {
			fallbackReminder.Reset()
		}
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

		cwd := StandardizePromptCwd(ctx.Cwd())
		if optCwd, ok := options["cwd"].(string); ok && optCwd != "" {
			cwd = StandardizePromptCwd(optCwd)
		}

		agentDir := ""
		if cfgHome, err := ctx.ConfigHome(); err == nil && cfgHome != "" {
			agentDir = filepath.Join(cfgHome, "agent")
		}

		// Sync tool commands before reading active tools so any SetActiveTools
		// changes performed by ToolCommands are visible in the inventory and gates.
		var mountedCommands []string
		var toolCommandDocs string
		if ToolCommands != nil {
			mountedCommands, toolCommandDocs = ToolCommands(ctx)
		}

		activeTools := activeToolsFromSession(ctx, options)
		contextFiles := contextFilesFromOptions(options, cwd)
		var skills []SkillEntry
		if Skillful == nil || Skillful(ctx) {
			skills = skillsFromOptions(options)
		}
		appendPrompt, _ := options["appendSystemPrompt"].(string)
		var ruleOpts RuleDiscoveryOptions
		if RuleOptions != nil {
			ruleOpts = RuleOptions(ctx)
		}
		discovered := DiscoverRules(cwd, agentDir, "", ruleOpts)
		alwaysApply := append(DiscoverStickyRules(cwd, agentDir, ""), discovered.AlwaysApply...)

		tmpl := base
		if tmpl == "" {
			tmpl = systemPromptTemplate
		}

		isSubagent := SubagentSession != nil && SubagentSession(ctx)
		personality := PersonalityDefault
		if isSubagent {
			// upstream: packages/coding-agent/src/sdk.ts:4679-4680
			personality = PersonalityNone
		}

		var intentTracing bool
		var intentField, intentGuidance string
		if Intent != nil {
			intentTracing, intentField, intentGuidance = Intent(ctx)
		}

		settingsApproval := SettingsApproval != nil && SettingsApproval(ctx)
		outReplies := OutReplies != nil && OutReplies(ctx)
		// upstream: sdk.ts:4664, irc/messaging.ts:16-20 — true for subagents and for
		// top-level sessions able to spawn peers (default task.maxRecursionDepth=2).
		taskIRCEnabled := true
		if TaskIRC != nil {
			taskIRCEnabled = TaskIRC(ctx)
		}
		includeWorkspaceTree := IncludeWorkspaceTree != nil && IncludeWorkspaceTree(ctx)

		rendered := RenderTemplate(tmpl, BuildOptions{
			Cwd:                  cwd,
			AgentDir:             agentDir,
			ToolNames:            activeTools,
			MountedCommands:      mountedCommands,
			ToolCommandDocs:      toolCommandDocs,
			ContextFiles:         contextFiles,
			Skills:               skills,
			Rules:                discovered.Rulebook,
			AlwaysApplyRules:     alwaysApply,
			EvalPreludes:         eval.EnabledPreludes(ctx),
			Context:              ctx,
			Model:                ctx.ModelQualified(),
			Personality:          personality,
			Subagent:             isSubagent,
			TaskIRCEnabled:       taskIRCEnabled,
			SettingsApproval:     settingsApproval,
			OutReplies:           outReplies,
			IntentTracing:        intentTracing,
			IntentField:          intentField,
			IntentGuidance:       intentGuidance,
			Environment:          envOverride,
			AppendSystemPrompt:   appendPrompt,
			IncludeWorkspaceTree: includeWorkspaceTree,
		})
		return map[string]any{"systemPrompt": rendered}, nil
	})

	e.OnEvent(sdk.EventContext, func(ctx sdk.Context, data map[string]any) (any, error) {
		messages, ok := data["messages"].([]any)
		if !ok || len(messages) == 0 {
			return nil, nil
		}
		updated, changed, replacedList := injectorFor(ctx).TransformInPlace(messages, FormatLocalCalendarDate(now()), StandardizePromptCwd(ctx.Cwd()))
		if !changed {
			return nil, nil
		}
		// When only existing message maps were edited in place, return (nil, nil) so
		// PiG's SDK wire layer leaves the message list identity intact (preserving
		// positional system messages). Only return {"messages": updated} when a new
		// synthetic user message was prepended.
		if !replacedList {
			return nil, nil
		}
		return map[string]any{"messages": updated}, nil
	})
}

// activeToolsFromSession takes the live active tool names from the session at run time,
// falling back to systemPromptOptions["selectedTools"] when GetActiveTools fails,
// filtering through toolcmd.AdvertisedTools (so mounted and special bash commands
// that remain in GetActiveTools for direct-call fallback stay out of the advertised
// tool set), and excluding any hiddenTools.
func activeToolsFromSession(ctx sdk.Context, options map[string]any) []string {
	var active []string
	if tools, err := ctx.GetActiveTools(); err == nil {
		active = tools
	} else {
		active = stringSliceField(options, "selectedTools")
	}
	active = toolcmd.AdvertisedTools(ctx, active)
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
