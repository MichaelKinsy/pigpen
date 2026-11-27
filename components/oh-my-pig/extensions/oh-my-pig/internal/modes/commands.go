package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
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
	instructions := renderHandoffPrompt(focus)
	ctx.Compact(map[string]any{
		"customInstructions": instructions,
	})
	notifyInfo(ctx, "Handoff compaction requested.")
	return nil
}

func renderHandoffPrompt(additionalFocus string) string {
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

// handleToolsCommand implements `/tools`: lists top-level active (`*`) and
// inactive (`-`) tools, followed by mounted bash tool commands (`~`).
// upstream: packages/coding-agent/src/slash-commands/builtin-session.ts:534-561
func (m *manager) handleToolsCommand(ctx sdk.Context, _ string) error {
	all, err := ctx.GetAllTools()
	if err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to list tools: %v", err))
		return nil
	}
	if len(all) == 0 {
		notifyInfo(ctx, "No tools are available.")
		return nil
	}
	activeList, _ := ctx.GetActiveTools()
	advertised := toolcmd.AdvertisedTools(ctx, activeList)
	activeSet := make(map[string]bool, len(advertised))
	for _, name := range advertised {
		activeSet[name] = true
	}
	mounted := toolcmd.MountedNames(ctx)
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

// handleSwitchCommand implements `/switch [model[:level]]`.
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

// handleRenameCommand implements `/rename <title>`.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:613-689
func (m *manager) handleRenameCommand(ctx sdk.Context, args string) error {
	title := strings.TrimSpace(args)
	if title == "" {
		notifyInfo(ctx, "Could not generate a session title. Use /rename <title> to set one.")
		return nil
	}
	if err := ctx.SetSessionName(title); err != nil {
		notifyError(ctx, fmt.Sprintf("Rename failed: %v", err))
		return nil
	}
	notifyInfo(ctx, fmt.Sprintf("Session renamed to %s.", title))
	return nil
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
