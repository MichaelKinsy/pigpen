package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/commit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

// upstream: packages/coding-agent/src/slash-commands/helpers/context-report.ts
// upstream: packages/coding-agent/src/slash-commands/helpers/usage-report.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts
// upstream: packages/tui/src/overlays/copy-targets.ts

// renderASCIIBar mirrors packages/tui/src/chrome/format.ts:59-78 renderAsciiBar.
func renderASCIIBar(fraction float64, width int) string {
	if width <= 0 {
		width = 24
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(math.Round(fraction * float64(width)))
	if filled > width {
		filled = width
	}
	pct := int(math.Round(fraction * 100))
	return fmt.Sprintf("[%s%s] %d%%", strings.Repeat("█", filled), strings.Repeat("░", width-filled), pct)
}

// handleContextCommand implements `/context` using omp's ASCII-bar breakdown.
// upstream: packages/coding-agent/src/slash-commands/helpers/context-report.ts
func (m *manager) handleContextCommand(ctx sdk.Context, _ string) error {
	usage, err := ctx.GetContextUsage()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Context usage is unavailable: %v", err))
		return nil
	}
	if usage == nil || usage.ContextWindow <= 0 {
		notifyWarn(ctx, "Context usage is unavailable: no model is selected for this session.")
		return nil
	}

	window := usage.ContextWindow
	used := 0
	if usage.Tokens != nil {
		used = *usage.Tokens
	}
	sysTokens := 0
	if sp, err := ctx.GetSystemPrompt(); err == nil && sp != "" {
		sysTokens = estimateStringTokens(sp)
	}
	toolsTokens := 0
	if allTools, err := ctx.GetAllTools(); err == nil {
		active, _ := ctx.GetActiveTools()
		advertised := toolcmd.AdvertisedTools(ctx, active)
		activeSet := make(map[string]bool, len(advertised))
		for _, a := range advertised {
			activeSet[a] = true
		}
		for _, t := range allTools {
			if len(active) > 0 && !activeSet[t.Name] {
				continue
			}
			toolsTokens += estimateStringTokens(t.Name) + estimateStringTokens(t.Description) + estimateStringTokens(string(t.Parameters))
		}
	}
	msgTokens := 0
	if branch, err := ctx.GetBranch(); err == nil {
		for _, e := range branch {
			msgTokens += estimateStringTokens(e.Content) + estimateStringTokens(e.Thinking)
			for _, tc := range e.ToolCalls {
				msgTokens += estimateStringTokens(tc.Name) + estimateStringTokens(tc.Args)
			}
		}
	}
	if used == 0 {
		used = sysTokens + toolsTokens + msgTokens
	}
	if used > window {
		used = window
	}
	usedPct := int(math.Round(float64(used) / float64(window) * 100))

	lines := []string{
		fmt.Sprintf("Context window: %d tokens (%d%% used)", window, usedPct),
	}
	type cat struct {
		label  string
		tokens int
	}
	cats := []cat{
		{"System prompt", sysTokens},
		{"System tools", toolsTokens},
		{"Messages", msgTokens},
	}
	for _, c := range cats {
		if c.tokens <= 0 {
			continue
		}
		frac := float64(c.tokens) / float64(window)
		lines = append(lines, fmt.Sprintf("  %-16s %s  %d tokens", c.label, renderASCIIBar(frac, 24), c.tokens))
	}
	free := window - used
	if free > 0 {
		frac := float64(free) / float64(window)
		lines = append(lines, fmt.Sprintf("  %-16s %s  %d tokens", "Free", renderASCIIBar(frac, 24), free))
	}
	notifyInfo(ctx, strings.Join(lines, "\n"))
	return nil
}

func estimateStringTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// handleUsageCommand implements `/usage [show]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts:411-456
// upstream: packages/coding-agent/src/slash-commands/helpers/usage-report.ts:189-201
func (m *manager) handleUsageCommand(ctx sdk.Context, args string) error {
	verb, rest := parseSubcommand(args)
	if verb == "reset" {
		notifyWarn(ctx, "Provider rate-limit reset credits are not available in PiG's extension SDK.")
		return nil
	}
	if verb != "" && (verb != "show" || rest != "") {
		notifyInfo(ctx, "Usage: /usage [show|reset [provider/credential-id|provider/active]]")
		return nil
	}

	branch, err := ctx.GetBranch()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to read session usage: %v", err))
		return nil
	}
	var input, output, cacheRead, cacheWrite, total int
	for _, e := range branch {
		if e.Usage == nil {
			continue
		}
		input += e.Usage.Input
		output += e.Usage.Output
		cacheRead += e.Usage.CacheRead
		cacheWrite += e.Usage.CacheWrite
		if e.Usage.TotalTokens > 0 {
			total += e.Usage.TotalTokens
		} else {
			total += e.Usage.Input + e.Usage.Output + e.Usage.CacheRead + e.Usage.CacheWrite
		}
	}
	var cost float64
	if mi, err := ctx.GetModelInfo(); err == nil && mi != nil {
		cost = (float64(input)*mi.InputCostPer1M +
			float64(output)*mi.OutputCostPer1M +
			float64(cacheRead)*mi.CacheReadCostPer1M +
			float64(cacheWrite)*mi.CacheWriteCostPer1M) / 1_000_000
	}
	lines := []string{
		"Usage",
		fmt.Sprintf("Input tokens: %d", input),
		fmt.Sprintf("Output tokens: %d", output),
		fmt.Sprintf("Cache read tokens: %d", cacheRead),
		fmt.Sprintf("Cache write tokens: %d", cacheWrite),
		fmt.Sprintf("Total tokens: %d", total),
		fmt.Sprintf("Cost: $%.6f", cost),
	}
	notifyInfo(ctx, strings.Join(lines, "\n"))
	return nil
}

// handleHandoffCommand implements `/handoff [focus instructions]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:349-356
func (m *manager) handleHandoffCommand(ctx sdk.Context, args string) error {
	focus := strings.TrimSpace(args)
	instructions := RenderHandoffPrompt(focus)
	ctx.Compact(map[string]any{
		"customInstructions": instructions,
	})
	notifyInfo(ctx, "Handoff compaction requested.")
	return nil
}

// RenderHandoffPrompt renders omp's handoff-document.md compaction prompt.
func RenderHandoffPrompt(additionalFocus string) string {
	return renderTemplate(handoffDocumentTemplate, templateData{
		Vars: map[string]string{"additionalFocus": additionalFocus},
	})
}

// handleBranchCommand implements `/branch` and alias `/rewind` (rewind to a
// previous user message via PiG's Select + NavigateTree).
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts:633-641
func (m *manager) handleBranchCommand(ctx sdk.Context, _ string) error {
	branch, err := ctx.GetBranch()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to read session branch: %v", err))
		return nil
	}
	type userPoint struct {
		id      string
		parent  string
		preview string
		text    string
	}
	var points []userPoint
	for i, e := range branch {
		if e.Type == "message" && e.Role == "user" && e.ID != "" {
			parent := ""
			if i > 0 {
				parent = branch[i-1].ID
			}
			points = append(points, userPoint{
				id:      e.ID,
				parent:  parent,
				preview: sanitizeSingleLine(e.Content, 72),
				text:    e.Content,
			})
		}
	}
	if len(points) == 0 {
		notifyInfo(ctx, "No user messages to rewind to.")
		return nil
	}
	if !ctx.HasUI() {
		notifyWarn(ctx, "Rewind selector requires an interactive UI.")
		return nil
	}
	options := make([]string, len(points))
	for i := range points {
		idx := len(points) - 1 - i
		options[i] = fmt.Sprintf("%d. %s", idx+1, points[idx].preview)
	}
	pickedVal, ok, err := ctx.Select("Rewind to message", options)
	if err != nil || !ok {
		return err
	}
	picked, _ := pickedVal.(string)
	for i, opt := range options {
		if opt != picked {
			continue
		}
		pt := points[len(points)-1-i]
		target := pt.parent
		if target == "" {
			target = pt.id
		}
		res, err := ctx.NavigateTree(target, nil)
		if err != nil {
			notifyError(ctx, fmt.Sprintf("Rewind failed: %v", err))
			return nil
		}
		if res.Cancelled {
			notifyInfo(ctx, "Rewind cancelled.")
			return nil
		}
		ctx.SetEditorText(pt.text)
		notifyInfo(ctx, "Rewound to selected message.")
		return nil
	}
	return nil
}

// handleClearCommand implements `/clear` (starts a fresh session via NewSession;
// PiG SDK does not expose in-place context reset keeping the same session ID).
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:281-290
func (m *manager) handleClearCommand(ctx sdk.Context, _ string) error {
	if VibeActive != nil && VibeActive(ctx) {
		notifyWarn(ctx, "Exit vibe mode first.")
		return nil
	}
	if idle, err := ctx.IsIdle(); err == nil && !idle {
		notifyWarn(ctx, "Wait for the current response to finish or abort it before resetting the context.")
		return nil
	}
	res, err := ctx.NewSession(nil)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to clear session: %v", err))
		return nil
	}
	if res.Cancelled {
		notifyInfo(ctx, "Clear cancelled.")
		return nil
	}
	notifyInfo(ctx, "Context cleared.")
	return nil
}

// escapeToolsMarkdownCell sanitizes a tool description for a Markdown table cell.
// upstream: packages/tui/src/prompt/tools-markdown.ts:7-12
func escapeToolsMarkdownCell(value string) string {
	s := strings.ReplaceAll(value, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// BuildToolsMarkdown renders omp's Markdown table for interactive `/tools`.
// upstream: packages/tui/src/prompt/tools-markdown.ts:14-29
func BuildToolsMarkdown(active []sdk.ToolInfo, mounted []toolcmd.CommandEntry) string {
	if len(active) == 0 && len(mounted) == 0 {
		return "No tools are currently visible to the agent."
	}
	rows := make([]string, 0, len(active)+len(mounted)+2)
	rows = append(rows, "| Tool | Description |", "|------|-------------|")
	for _, t := range active {
		desc := escapeToolsMarkdownCell(t.Description)
		if desc == "" {
			desc = "No description provided."
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", t.Name, desc))
	}
	for _, m := range mounted {
		desc := escapeToolsMarkdownCell(m.Summary)
		if desc == "" {
			desc = "No description provided."
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", m.Name, desc))
	}
	return strings.Join(rows, "\n")
}

// handleToolsCommand implements `/tools`: in interactive UI renders omp's
// Markdown table (`buildToolsMarkdown`), and in non-UI / print mode lists
// top-level active (`*`) and inactive (`-`) tools followed by mounted bash
// tool commands (`~`).
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts:534-561,
//
//	packages/coding-agent/src/modes/controllers/command-controller.ts:992-998
func (m *manager) handleToolsCommand(ctx sdk.Context, _ string) error {
	all, err := ctx.GetAllTools()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to list tools: %v", err))
		return nil
	}
	activeList, _ := ctx.GetActiveTools()
	advertised := toolcmd.AdvertisedTools(ctx, activeList)
	activeSet := make(map[string]bool, len(advertised))
	for _, name := range advertised {
		activeSet[name] = true
	}
	mounted := toolcmd.MountedNames(ctx)

	if ctx.HasUI() {
		byName := make(map[string]sdk.ToolInfo, len(all))
		for _, t := range all {
			byName[t.Name] = t
		}
		activeInfos := make([]sdk.ToolInfo, 0, len(advertised))
		for _, name := range advertised {
			if info, ok := byName[name]; ok {
				activeInfos = append(activeInfos, info)
			} else {
				activeInfos = append(activeInfos, sdk.ToolInfo{Name: name})
			}
		}
		mountedEntries := make([]toolcmd.CommandEntry, 0, len(mounted))
		for _, name := range mounted {
			summary := toolcmd.BuiltinToolSummaries[name]
			if summary == "" {
				if info, ok := byName[name]; ok {
					summary = info.Description
				}
			}
			mountedEntries = append(mountedEntries, toolcmd.CommandEntry{
				Name:    name,
				Summary: summary,
			})
		}
		notifyInfo(ctx, BuildToolsMarkdown(activeInfos, mountedEntries))
		return nil
	}

	if len(all) == 0 {
		notifyInfo(ctx, "No tools are available.")
		return nil
	}
	lines := make([]string, 0, len(all)+len(mounted))
	for _, t := range all {
		prefix := "-"
		if activeSet[t.Name] {
			prefix = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s", prefix, t.Name))
	}
	for _, name := range mounted {
		lines = append(lines, fmt.Sprintf("~ %s", name))
	}
	notifyInfo(ctx, strings.Join(lines, "\n"))
	return nil
}

// handleEffortCommand implements `/effort [level]` (omp's thinking-level command).
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:851-903
func (m *manager) handleEffortCommand(ctx sdk.Context, args string) error {
	mi, err := ctx.GetModelInfo()
	if err != nil || mi == nil {
		notifyWarn(ctx, "The current model has no adjustable thinking level.")
		return nil
	}
	if !mi.Reasoning {
		notifyInfo(ctx, fmt.Sprintf("%s/%s has no adjustable thinking level.", mi.Provider, mi.ID))
		return nil
	}
	supported := supportedEffortsForContext(ctx, mi)
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "" {
		if ctx.HasUI() {
			pickedVal, ok, err := ctx.Select("Select reasoning effort", supported)
			if err != nil || !ok {
				return err
			}
			arg, _ = pickedVal.(string)
		} else {
			cur, _ := ctx.GetThinkingLevel()
			if cur == "" {
				cur = "model default"
			}
			notifyInfo(ctx, fmt.Sprintf("Thinking: %s\nAvailable: %s", cur, strings.Join(supported, ", ")))
			return nil
		}
	}
	if !containsString(supported, arg) {
		notifyError(ctx, fmt.Sprintf("Unknown thinking level: %s. Available: %s", arg, strings.Join(supported, ", ")))
		return nil
	}
	ctx.SetThinkingLevel(arg)
	notifyInfo(ctx, fmt.Sprintf("Thinking set to %s.", arg))
	return nil
}

func supportedEffortsForContext(ctx sdk.Context, mi *sdk.ModelInfo) []string {
	if mi != nil {
		if regModel, err := ctx.ModelRegistry().Find(mi.Provider, mi.ID); err == nil && regModel != nil {
			if levels := sdk.GetSupportedThinkingLevels(regModel); len(levels) > 0 {
				return levels
			}
		}
	}
	return []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
}

// completeRename performs the LLM completion for `/rename` auto-title generation.
// Overridable in unit tests so tests never make network calls.
var completeRename = func(ctx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error) {
	return ctx.Complete(modelObj, req, auth)
}

// renameTitleSystemPrompt mirrors omp's title-system.md + title-marker-instruction.md
// for concise session title generation.
// upstream: packages/coding-agent/src/prompts/system/title-system.md
// upstream: packages/coding-agent/src/prompts/system/title-marker-instruction.md
const renameTitleSystemPrompt = `You name coding-agent sessions. Write a 3-6 word title for the task in the next message.

Output exactly one line: the title inside <title> tags, or <title/> when there is no task to name.

Rules:
- Name the user's goal as a short imperative phrase built from the user's own key words.
- Answer <title/> for greetings, acknowledgements, gibberish, or requests too vague to name.
- No quotes, trailing punctuation, or explanations.`

var (
	renameTitleTagRE    = regexp.MustCompile(`(?is)<title>([\s\S]*?)</title>`)
	renameEmptyTagRE    = regexp.MustCompile(`(?i)<title\s*/>`)
	renameThinkTagRE    = regexp.MustCompile(`(?is)^\s*<(?:think|thinking|reasoning)>[\s\S]*?</(?:think|thinking|reasoning)>\s*`)
	renameTrailingPunct = regexp.MustCompile(`[.!?]+$`)
)

const renameTitleMaxChars = 80

// isRoleSelector reports whether spec is a role alias (`@role`, `pi/<role>`, `*`)
// with an optional `:level` thinking suffix.
func isRoleSelector(spec string) bool {
	return strings.HasPrefix(spec, "@") || strings.HasPrefix(spec, "pi/") || spec == "*"
}

// handleSwitchCommand implements `/switch [model[:level]]` including `@role`,
// `pi/<role>`, and `*` selectors (with optional `:level` thinking suffix).
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:457-506
func (m *manager) handleSwitchCommand(ctx sdk.Context, args string) error {
	selector := strings.TrimSpace(args)
	if selector == "" {
		qual := ctx.ModelQualified()
		if qual == "" {
			notifyInfo(ctx, "No model is currently selected.")
		} else {
			notifyInfo(ctx, fmt.Sprintf("Current model: %s", qual))
		}
		return nil
	}

	modelSpec, thinkingLevel := splitModelThinkingSuffix(selector)
	if isRoleSelector(modelSpec) {
		if ResolveRole == nil {
			notifyError(ctx, fmt.Sprintf("Unknown model: %s", selector))
			return nil
		}
		prov, id, roleThinking, resolved := ResolveRole(ctx, selector)
		if !resolved || id == "" {
			notifyError(ctx, fmt.Sprintf("Unknown model: %s", selector))
			return nil
		}
		if prov != "" {
			modelSpec = prov + "/" + id
		} else {
			modelSpec = id
		}
		if thinkingLevel == "" && roleThinking != "" {
			thinkingLevel = roleThinking
		}
	}

	ok, err := ctx.SetModelNamed(modelSpec)
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to switch model: %v", err))
		return nil
	}
	if !ok {
		notifyError(ctx, fmt.Sprintf("Unknown model: %s", modelSpec))
		return nil
	}
	if thinkingLevel != "" {
		ctx.SetThinkingLevel(thinkingLevel)
	}
	notifyInfo(ctx, fmt.Sprintf("Model switched to %s.", modelSpec))
	return nil
}

func splitModelThinkingSuffix(spec string) (model, level string) {
	idx := strings.LastIndexByte(spec, ':')
	if idx <= 0 {
		return spec, ""
	}
	cand := strings.ToLower(spec[idx+1:])
	if _, ok := thinkingLevelRank[cand]; ok {
		return spec[:idx], cand
	}
	return spec, ""
}

// handleRenameCommand implements `/rename [title]`.
// When no title argument is provided, generates a concise title from the
// session branch via the `@smol` role model (falling back to the active model).
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:613-689
// upstream: packages/coding-agent/src/utils/title-generator.ts:380-573
func (m *manager) handleRenameCommand(ctx sdk.Context, args string) error {
	title := strings.TrimSpace(args)
	if title == "" {
		generated := m.generateSessionTitle(ctx)
		if generated == "" {
			notifyInfo(ctx, "Could not generate a session title. Use /rename <title> to set one.")
			return nil
		}
		title = generated
	}
	if err := ctx.SetSessionName(title); err != nil {
		notifyError(ctx, fmt.Sprintf("Rename failed: %v", err))
		return nil
	}
	notifyInfo(ctx, fmt.Sprintf("Session renamed to %s.", title))
	return nil
}

// extractRenamePrompt builds a compact user-turn excerpt from the active branch
// for `/rename` auto-title generation. Returns "" when the branch has no user text.
func extractRenamePrompt(branch []sdk.BranchEntry) string {
	var firstUser string
	var recent []string
	for _, e := range branch {
		if e.Type != "message" {
			continue
		}
		txt := strings.TrimSpace(e.Content)
		if txt == "" {
			continue
		}
		if e.Role == "user" {
			if firstUser == "" {
				firstUser = txt
			}
			recent = append(recent, "<user>"+txt+"</user>")
		} else if e.Role == "assistant" {
			recent = append(recent, "<assistant>"+txt+"</assistant>")
		}
	}
	if firstUser == "" {
		return ""
	}
	if len(recent) <= 1 {
		return truncateRunes(firstUser, 1200)
	}
	const maxRecent = 4
	if len(recent) > maxRecent {
		recent = append([]string{recent[0]}, recent[len(recent)-(maxRecent-1):]...)
	}
	return truncateRunes("<chat>\n"+strings.Join(recent, "\n")+"\n</chat>", 1600)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// cleanGeneratedTitle extracts and sanitizes a title from model completion output.
// upstream: packages/coding-agent/src/utils/title-generator.ts:546-655
// upstream: packages/coding-agent/src/tiny/text.ts:204-242
func cleanGeneratedTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if m := renameTitleTagRE.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	} else if renameEmptyTagRE.MatchString(s) {
		return ""
	} else {
		for {
			next := renameThinkTagRE.ReplaceAllString(s, "")
			if next == s {
				break
			}
			s = next
		}
		s = strings.ReplaceAll(s, "<title>", "")
		s = strings.ReplaceAll(s, "</title>", "")
		s = strings.ReplaceAll(s, "<TITLE>", "")
		s = strings.ReplaceAll(s, "</TITLE>", "")
		s = strings.TrimSpace(s)
	}
	if s == "" {
		return ""
	}
	// Keep first non-empty line.
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			s = trimmed
			break
		}
	}
	// Strip surrounding quotes and trailing sentence punctuation.
	for len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '`' && s[len(s)-1] == '`')) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	s = strings.TrimSpace(renameTrailingPunct.ReplaceAllString(s, ""))
	if s == "" || strings.EqualFold(s, "none") {
		return ""
	}
	// Collapse internal whitespace and remove control characters.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if r == ' ' || r == '\t' {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" || strings.EqualFold(cleaned, "none") {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > renameTitleMaxChars {
		cleaned = strings.TrimSpace(string(runes[:renameTitleMaxChars]))
	}
	return cleaned
}

func (m *manager) generateSessionTitle(ctx sdk.Context) string {
	branch, err := ctx.GetBranch()
	if err != nil {
		return ""
	}
	promptText := extractRenamePrompt(branch)
	if promptText == "" {
		return ""
	}

	var provider, modelID string
	if ResolveRole != nil {
		if p, id, _, ok := ResolveRole(ctx, "@smol"); ok && id != "" {
			provider, modelID = p, id
		}
	}
	if modelID == "" {
		mi, err := ctx.GetModelInfo()
		if err != nil || mi == nil || mi.ID == "" {
			return ""
		}
		provider, modelID = mi.Provider, mi.ID
	}

	modelObj := map[string]any{
		"id":       modelID,
		"provider": provider,
	}
	if regModel, err := ctx.ModelRegistry().Find(provider, modelID); err == nil && regModel != nil {
		modelObj = regModel
	}

	var auth map[string]any
	if rawAuth, err := ctx.GetModelAuth(provider, modelID); err == nil && len(rawAuth) > 0 {
		_ = json.Unmarshal(rawAuth, &auth)
	}

	req := map[string]any{
		"systemPrompt": renameTitleSystemPrompt,
		"maxTokens":    1024,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": []map[string]any{{"type": "text", "text": promptText}},
			},
		},
	}

	raw, err := completeRename(ctx, modelObj, req, auth)
	if err != nil {
		return ""
	}
	text, errMsg := extractCompleteText(raw)
	if errMsg != "" || text == "" {
		return ""
	}
	return cleanGeneratedTitle(text)
}

// handleDumpCommand implements `/dump`: copies formatted session transcript to clipboard.
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:232-292
func (m *manager) handleDumpCommand(ctx sdk.Context, args string) error {
	verb, _ := parseSubcommand(args)
	if verb == "all" || verb == "anon" {
		notifyWarn(ctx, fmt.Sprintf("/dump %s archive generation is not supported via PiG's extension SDK.", verb))
		return nil
	}
	if verb != "" {
		notifyInfo(ctx, "Usage: /dump [all|anon]")
		return nil
	}
	branch, err := ctx.GetBranch()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to read session: %v", err))
		return nil
	}
	text := formatBranchAsText(branch)
	if text == "" {
		notifyInfo(ctx, "No messages to dump yet.")
		return nil
	}
	if err := ctx.CopyToClipboard(text); err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to copy session: %v", err))
		return nil
	}
	notifyInfo(ctx, "Session copied to clipboard")
	return nil
}

func formatBranchAsText(branch []sdk.BranchEntry) string {
	var b strings.Builder
	for _, e := range branch {
		if e.Type != "message" || strings.TrimSpace(e.Content) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "### %s\n%s", strings.ToUpper(e.Role), e.Content)
	}
	return b.String()
}

// handleJobsCommand implements `/jobs [full|kill <id>|kill all]` using internal/jobs.
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts:335-409
func (m *manager) handleJobsCommand(ctx sdk.Context, args string) error {
	verb, rest := parseSubcommand(args)
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyInfo(ctx, "No background jobs running.")
		return nil
	}
	all := jobs.List(sid)

	if verb == "kill" {
		target := strings.TrimSpace(rest)
		if target == "" {
			notifyInfo(ctx, "Usage: /jobs kill <id>|all")
			return nil
		}
		if target == "all" {
			cancelled := 0
			for _, j := range all {
				if j.Status() == "running" {
					j.Cancel()
					cancelled++
				}
			}
			if cancelled == 0 {
				notifyInfo(ctx, "No running background jobs to cancel.")
			} else {
				noun := "jobs"
				if cancelled == 1 {
					noun = "job"
				}
				notifyInfo(ctx, fmt.Sprintf("Cancelled %d background %s.", cancelled, noun))
			}
			return nil
		}
		j, ok := jobs.Get(sid, target)
		if !ok || j.Status() != "running" {
			notifyInfo(ctx, fmt.Sprintf("No running background job with id %q.", sanitizeSingleLine(target, 60)))
			return nil
		}
		j.Cancel()
		notifyInfo(ctx, fmt.Sprintf("Cancelled background job %s.", sanitizeSingleLine(target, 60)))
		return nil
	}

	if rest != "" || (verb != "" && verb != "full") {
		notifyInfo(ctx, "Usage: /jobs [full|kill <id>|kill all]")
		return nil
	}

	if len(all) == 0 {
		notifyInfo(ctx, "No background jobs running.")
		return nil
	}
	running := 0
	var rows []string
	for _, j := range all {
		st := j.Status()
		if st == "running" {
			running++
		}
		dur := formatCoarseDurationMs(time.Since(j.Started()).Milliseconds())
		rows = append(rows, fmt.Sprintf("[%s] %s (%s) — %s", j.ID(), j.Kind(), st, dur))
		if lbl := sanitizeSingleLine(j.Label(), 120); lbl != "" && lbl != j.ID() {
			rows = append(rows, "  "+lbl)
		}
	}
	out := fmt.Sprintf("Background Jobs\nRunning: %d\n\n%s", running, strings.Join(rows, "\n"))
	notifyInfo(ctx, out)
	return nil
}

// handleQueueCommand implements `/queue <message>` (follow-up behind active turn).
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:400-411
func (m *manager) handleQueueCommand(ctx sdk.Context, args string) error {
	msg := strings.TrimSpace(args)
	if msg == "" {
		notifyInfo(ctx, "Usage: /queue <message>")
		return nil
	}
	if err := ctx.SendUserMessage(msg, "followUp"); err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to queue message: %v", err))
		return nil
	}
	notifyInfo(ctx, "Queued follow-up message.")
	return nil
}

// handleForceCommand implements `/force <tool-name> [prompt]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts:44-92
func (m *manager) handleForceCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot force tool without an active session ID.")
		return nil
	}
	trimmed := strings.TrimSpace(args)
	toolName := trimmed
	prompt := ""
	if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
		toolName = trimmed[:idx]
		prompt = strings.TrimSpace(trimmed[idx+1:])
	}
	if toolName == "" {
		notifyError(ctx, "Usage: /force:<tool-name> [prompt]")
		return nil
	}
	active, _ := ctx.GetActiveTools()
	advertised := toolcmd.AdvertisedTools(ctx, active)
	if len(active) > 0 && !containsString(advertised, toolName) {
		notifyError(ctx, fmt.Sprintf("Tool %q is not currently active.", toolName))
		return nil
	}
	m.mu.Lock()
	m.stateLocked(sid).forcedTool = toolName
	m.mu.Unlock()
	if SetForcedToolChoice != nil {
		SetForcedToolChoice(sid, toolName)
	}
	notifyInfo(ctx, fmt.Sprintf("Next turn forced to use %s.", toolName))
	if prompt != "" {
		return sendPromptOrSteer(ctx, prompt)
	}
	return nil
}

// handleExitCommand implements `/exit` and `/q` aliases for quitting.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:830-833
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts:132-137
func (m *manager) handleExitCommand(ctx sdk.Context, _ string) error {
	ctx.Shutdown()
	return nil
}

// handleCopyWithArgs implements omp's `/copy [code|cmd|link|local]`
// extensions when intercepted via EventInput (because PiG's built-in `/copy`
// takes no arguments, so `/copy code` etc. reach EventInput).
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:576-635
func (m *manager) handleCopyWithArgs(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	branch, _ := ctx.GetBranch()
	switch arg {
	case "code":
		code := ExtractLastCodeBlock(branch)
		if code == "" {
			notifyInfo(ctx, "No code block to copy.")
			return nil
		}
		if err := ctx.CopyToClipboard(code); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to copy code block: %v", err))
			return nil
		}
		notifyInfo(ctx, "Copied code block to clipboard")
		return nil

	case "cmd", "command":
		kind, code := ExtractLastCommand(branch)
		if code == "" {
			notifyInfo(ctx, "No command to copy.")
			return nil
		}
		if err := ctx.CopyToClipboard(code); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to copy command: %v", err))
			return nil
		}
		label := "bash command"
		if kind == "eval" {
			label = "eval code"
		}
		notifyInfo(ctx, fmt.Sprintf("Copied %s to clipboard", label))
		return nil

	case "link", "url":
		href := ExtractLastLink(branch)
		if href == "" {
			notifyInfo(ctx, "No link to copy.")
			return nil
		}
		if err := ctx.CopyToClipboard(href); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to copy link: %v", err))
			return nil
		}
		notifyInfo(ctx, "Copied link to clipboard")
		return nil

	case "local":
		root := resolveLocalRootDir(ctx)
		if root == "" {
			notifyError(ctx, "Cannot resolve session local:// directory.")
			return nil
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to create local directory: %v", err))
			return nil
		}
		if err := ctx.CopyToClipboard(root); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to copy local path: %v", err))
			return nil
		}
		notifyInfo(ctx, fmt.Sprintf("Copied %s to clipboard", root))
		return nil

	default:
		notifyInfo(ctx, "Usage: /copy [code|cmd|link|local]")
		return nil
	}
}

// ExtractLastCodeBlock finds the last fenced code block in assistant messages.
// upstream: packages/tui/src/overlays/copy-targets.ts:70-79
func ExtractLastCodeBlock(branch []sdk.BranchEntry) string {
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Role != "assistant" || branch[i].Content == "" {
			continue
		}
		blocks := extractFencedCodeBlocks(branch[i].Content)
		if len(blocks) > 0 {
			return blocks[len(blocks)-1]
		}
	}
	return ""
}

func extractFencedCodeBlocks(md string) []string {
	var blocks []string
	var fenceChar byte
	fenceLen := 0
	var buf []string
	for _, line := range strings.Split(md, "\n") {
		open := fenceRE.FindStringSubmatch(line)
		if fenceChar != 0 {
			if open != nil && open[2][0] == fenceChar && len(open[2]) >= fenceLen &&
				strings.TrimSpace(line[len(open[1])+len(open[2]):]) == "" {
				blocks = append(blocks, strings.Join(buf, "\n"))
				buf = nil
				fenceChar = 0
				fenceLen = 0
				continue
			}
			buf = append(buf, line)
		} else if open != nil {
			ch := open[2][0]
			if !(ch == '`' && strings.ContainsRune(line[len(open[1])+len(open[2]):], '`')) {
				fenceChar = ch
				fenceLen = len(open[2])
				buf = nil
			}
		}
	}
	return blocks
}

// ExtractLastCommand finds the last bash/eval tool call in assistant messages.
// upstream: packages/tui/src/overlays/copy-targets.ts:156-179
func ExtractLastCommand(branch []sdk.BranchEntry) (kind, code string) {
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Role != "assistant" {
			continue
		}
		for j := len(branch[i].ToolCalls) - 1; j >= 0; j-- {
			tc := branch[i].ToolCalls[j]
			if tc.Name == "bash" {
				var args struct {
					Command string `json:"command"`
				}
				if json.Unmarshal([]byte(tc.Args), &args) == nil && args.Command != "" {
					return "bash", args.Command
				}
			}
			if tc.Name == "eval" {
				var args struct {
					Code string `json:"code"`
				}
				if json.Unmarshal([]byte(tc.Args), &args) == nil && args.Code != "" {
					return "eval", args.Code
				}
			}
		}
	}
	return "", ""
}

var httpLinkRE = regexp.MustCompile(`https?://[^\s<>\[\]()"'` + "`" + `]+`)

// ExtractLastLink finds the last http(s) URL in assistant messages.
// upstream: packages/tui/src/overlays/copy-targets.ts:105-125
func ExtractLastLink(branch []sdk.BranchEntry) string {
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Role != "assistant" || branch[i].Content == "" {
			continue
		}
		matches := httpLinkRE.FindAllString(branch[i].Content, -1)
		if len(matches) > 0 {
			return strings.TrimRight(matches[len(matches)-1], ".,;:!?")
		}
	}
	return ""
}

const commitRecentSubjects = 5

// RenderCommitRequest renders omp's commit-request.md prompt template with the
// given draft summary, recent commit subjects, and user instructions.
// upstream: packages/coding-agent/src/slash-commands/builtin-commit.ts:36
// upstream: packages/coding-agent/src/prompts/commit-request.md
func RenderCommitRequest(summary string, recent []string, instructions string) string {
	return renderTemplate(commitRequestTemplate, templateData{
		Vars: map[string]string{
			"summary":      summary,
			"instructions": strings.TrimSpace(instructions),
		},
		Lists: map[string][]string{
			"recent": recent,
		},
	})
}

// isCommitPreludeAdvertised reports whether `eval` is an active tool and the
// `commit` eval prelude is currently enabled for ctx.
// upstream: packages/coding-agent/src/slash-commands/builtin-commit.ts:21
func isCommitPreludeAdvertised(ctx sdk.Context) bool {
	activeTools, err := ctx.GetActiveTools()
	if err != nil {
		return false
	}
	evalActive := false
	for _, name := range activeTools {
		if name == "eval" {
			evalActive = true
			break
		}
	}
	if !evalActive {
		return false
	}
	for _, p := range eval.EnabledPreludes(ctx) {
		if p.Name == "commit" {
			return true
		}
	}
	return false
}

// handleCommitCommand implements `/commit [instructions]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-commit.ts:13-42
func (m *manager) handleCommitCommand(ctx sdk.Context, args string) error {
	if !isCommitPreludeAdvertised(ctx) {
		notifyWarn(ctx, "/commit needs the eval tool and the `commit.enabled` setting.")
		return nil
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-reqCtx.Done():
			}
		}()
	}
	id := commit.DefaultAgentIdentity(ctx)
	draft, err := commit.DraftFor(commit.DraftContext{
		Cwd:          ctx.Cwd(),
		ArtifactsDir: commit.DefaultArtifactsDir(ctx),
		Agent:        id.Agent,
		IsRunning: func(agentID string) bool {
			if commit.IsAgentRunning != nil {
				return commit.IsAgentRunning(ctx, agentID)
			}
			return false
		},
		Ctx: reqCtx,
	}, commit.ScopeDefault())
	if err != nil {
		notifyWarn(ctx, err.Error())
		return nil
	}
	recent, _ := commit.LogSubjects(reqCtx, draft.Root, commitRecentSubjects)
	promptText := RenderCommitRequest(draft.Summary(), recent, args)
	if err := sendPromptOrSteer(ctx, promptText); err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to send commit prompt: %v", err))
	}
	return nil
}

// ParseExportArgs parses `/export [--themes] [path]` matching upstream
// `packages/coding-agent/src/export/html/args.ts:14-20`.
func ParseExportArgs(args string) (outputPath string, useUserThemes bool, err error) {
	parts := strings.Fields(args)
	var paths []string
	for _, p := range parts {
		if p == "--themes" {
			useUserThemes = true
			continue
		}
		paths = append(paths, p)
	}
	if len(paths) > 1 {
		return "", false, fmt.Errorf("Usage: /export [--themes] [path]")
	}
	if len(paths) == 1 {
		outputPath = paths[0]
	}
	return outputPath, useUserThemes, nil
}

func isExportCopyArg(outputPath string) bool {
	switch strings.ToLower(strings.TrimSpace(outputPath)) {
	case "--copy", "copy", "clipboard":
		return true
	default:
		return false
	}
}

// shouldInterceptExport reports whether `/export <args>` should be handled by
// oh-my-pig (`--copy|copy|clipboard` guard or `--themes [path]`), leaving bare
// `/export` and `/export <path>` to PiG's built-in command handler.
func shouldInterceptExport(args string) bool {
	outputPath, useUserThemes, err := ParseExportArgs(args)
	if err != nil {
		return strings.Contains(args, "--themes") || strings.Contains(args, "--copy")
	}
	return useUserThemes || isExportCopyArg(outputPath)
}

// handleExportIntercept implements omp's `/export --copy` guard and
// `/export --themes [path]` dual-theme HTML export.
// upstream: packages/coding-agent/src/slash-commands/builtin-collaboration.ts:180-203
func (m *manager) handleExportIntercept(ctx sdk.Context, args string) error {
	outputPath, useUserThemes, err := ParseExportArgs(args)
	if err != nil {
		notifyInfo(ctx, err.Error())
		return nil
	}
	if isExportCopyArg(outputPath) {
		notifyInfo(ctx, "Use /dump to copy the session to clipboard.")
		return nil
	}
	if !useUserThemes {
		return nil
	}
	branch, err := ctx.GetBranch()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to export session: %v", err))
		return nil
	}
	target := strings.TrimSpace(outputPath)
	if target == "" {
		ts := time.UnixMilli(m.now()).UTC().Format("20060102-150405")
		target = filepath.Join(ctx.Cwd(), fmt.Sprintf("session-export-%s.html", ts))
	} else {
		if target == "~" || strings.HasPrefix(target, "~/") || strings.HasPrefix(target, `~\`) {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				if target == "~" {
					target = home
				} else {
					target = filepath.Join(home, target[2:])
				}
			}
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(ctx.Cwd(), target)
		}
	}
	sessionName := ""
	if sn, err := ctx.GetSessionName(); err == nil && sn != nil {
		sessionName = *sn
	}
	doc := RenderThemedSessionHTML(sessionName, branch)
	if err := atomicWriteFile(target, []byte(doc), 0o600); err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to export session: %v", err))
		return nil
	}
	notifyInfo(ctx, fmt.Sprintf("Session exported to: %s", target))
	return nil
}

// RenderThemedSessionHTML builds a self-contained dark/light HTML export of
// branch entries with strict HTML escaping.
func RenderThemedSessionHTML(title string, branch []sdk.BranchEntry) string {
	if strings.TrimSpace(title) == "" {
		title = "Session Export"
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\" data-theme=\"dark\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n<title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</title>\n<style>\n")
	b.WriteString(`:root{--bg:#0f1216;--card:#161b22;--fg:#e8ecf4;--muted:#9ca3b0;--border:#2a3038;--accent:#00b4ff}
@media (prefers-color-scheme: light){:root[data-theme="auto"]{--bg:#f8fafc;--card:#ffffff;--fg:#111827;--muted:#4b5563;--border:#d1d5db;--accent:#0284c7}}
:root[data-theme="light"]{--bg:#f8fafc;--card:#ffffff;--fg:#111827;--muted:#4b5563;--border:#d1d5db;--accent:#0284c7}
body{margin:0;padding:24px;background:var(--bg);color:var(--fg);font:14px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace}
header{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid var(--border);padding-bottom:12px;margin-bottom:20px}
button{background:var(--card);color:var(--fg);border:1px solid var(--border);border-radius:6px;padding:4px 10px;cursor:pointer}
.msg{background:var(--card);border:1px solid var(--border);border-radius:8px;padding:12px 16px;margin-bottom:12px}
.role{font-weight:700;color:var(--accent);text-transform:uppercase;font-size:12px;margin-bottom:6px}
pre{margin:0;white-space:pre-wrap;word-break:break-word}
`)
	b.WriteString("</style>\n</head>\n<body>\n<header><h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1><button id=\"theme-toggle\" type=\"button\" onclick=\"const r=document.documentElement;r.dataset.theme=r.dataset.theme==='light'?'dark':'light'\">Toggle theme</button></header>\n<main>\n")
	for _, e := range branch {
		if e.Type != "message" || (strings.TrimSpace(e.Content) == "" && len(e.ToolCalls) == 0) {
			continue
		}
		b.WriteString("<section class=\"msg\"><div class=\"role\">")
		b.WriteString(html.EscapeString(e.Role))
		b.WriteString("</div><pre>")
		b.WriteString(html.EscapeString(e.Content))
		for _, tc := range e.ToolCalls {
			b.WriteString("\n[tool: ")
			b.WriteString(html.EscapeString(tc.Name))
			b.WriteString(" ")
			b.WriteString(html.EscapeString(tc.Args))
			b.WriteString("]")
		}
		b.WriteString("</pre></section>\n")
	}
	b.WriteString("</main>\n</body>\n</html>\n")
	return b.String()
}

func atomicWriteFile(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-export-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}
