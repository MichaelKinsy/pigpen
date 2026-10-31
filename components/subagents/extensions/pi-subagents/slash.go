package pi_subagents

import (
	"fmt"
	"os"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The original's slash commands this port registers (src/slash/*): the ones
// whose subject it runs. The admin flow keeps the original's messages; a TUI
// picker replaces its selector list. Commands whose action the port refuses
// (cost, doctor, steer, profiles, prompt-workflow) are not registered.

const (
	slashTextResultType   = "subagent-slash-text"
	slashAdminMessageType = "subagents-admin"
	inheritModelChoice    = "(inherit)"
	inheritThinkingChoice = "(inherit)"
)

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.Command("subagents", "Administer subagents: inspect metadata and update models, thinking, or prompts", subagentsAdminCommand)
		e.Command("run", "Run one subagent: /run <agent> [output=file] [task] [--bg] [--fork]", runSlashCommand)
		e.Command("subagents-guide", "Show a packaged subagents guide topic", guideSlashCommand)
		e.Command("subagents-models", "Show model mappings for discovered subagents", modelsSlashCommand)
		e.Command("subagents-stop", "Stop a current-session async subagent run, or one child of it with /subagents-stop <run-id> <child-id>", stopSlashCommand)
		e.Command("subagents-steer", "Steer a live async subagent run with a message", steerSlashCommand)
		e.Command("subagents-fleet", "Show the live subagent fleet", fleetSlashCommand)
	})
}

// sendSlashText is the original's: the text lands in the thread as a message.
func sendSlashText(ctx sdk.Context, text string) {
	_ = ctx.SendMessage(slashTextResultType, text, true, sdk.SendMessageOptions{})
}

// sendAdminMessage is the original's admin result message.
func sendAdminMessage(ctx sdk.Context, text string) {
	_ = ctx.SendMessage(slashAdminMessageType, text, true, sdk.SendMessageOptions{})
}

// visibleAdminAgents are the agents the admin flow offers (disabled hidden).
func visibleAdminAgents(cwd string) []AgentConfig {
	d := DiscoverAgentsAll(cwd)
	agents := withoutDisabled(MergeAgentsForScope(ScopeBoth, d.User, d.Project, d.Builtin, d.Package))
	sort.SliceStable(agents, func(i, j int) bool { return localeLess(agents[i].Name, agents[j].Name) })
	return agents
}

// adminMetadataFor is the original's metadataFor (the admin's inspection text).
func adminMetadataFor(a *AgentConfig) string {
	tools := append([]string{}, a.Tools...)
	for _, t := range a.McpDirectTools {
		tools = append(tools, "mcp:"+t)
	}
	lines := []string{
		fmt.Sprintf("Agent: %s (%s)", a.Name, a.Source),
		"Path: " + a.FilePath,
		"Description: " + a.Description,
	}
	if a.PackageName != "" {
		lines = append(lines, "Local name: "+frontmatterName(a.Name, a.LocalName, a.PackageName))
		lines = append(lines, "Package: "+a.PackageName)
	}
	model := a.Model
	if model == "" {
		model = "default / inherit"
	}
	lines = append(lines, "Model: "+model)
	if a.Thinking != nil {
		lines = append(lines, "Thinking: "+thinkingWord(a))
	}
	if len(tools) > 0 {
		lines = append(lines, "Tools: "+strings.Join(tools, ", "))
	}
	if len(a.ExcludeTools) > 0 {
		lines = append(lines, "Excluded tools: "+strings.Join(a.ExcludeTools, ", "))
	}
	if len(a.Skills) > 0 {
		lines = append(lines, "Skills: "+strings.Join(a.Skills, ", "))
	}
	lines = append(lines, "System prompt mode: "+a.SystemPromptMode)
	lines = append(lines, "Inherit project context: "+boolWordPtr(a.InheritProjectContext))
	lines = append(lines, "Inherit global context: "+boolWordPtr(a.InheritGlobalContext))
	lines = append(lines, "Inherit skills: "+boolWordPtr(a.InheritSkills))
	if a.DefaultContext != "" {
		lines = append(lines, "Default context: "+a.DefaultContext)
	}
	if a.Output != "" {
		lines = append(lines, "Output: "+a.Output)
	}
	if len(a.DefaultReads) > 0 {
		lines = append(lines, "Reads: "+strings.Join(a.DefaultReads, ", "))
	}
	if a.DefaultProgress {
		lines = append(lines, "Progress: true")
	}
	if a.MaxSubagentDepth != nil {
		lines = append(lines, "Max subagent depth: "+formatInt(int64(*a.MaxSubagentDepth)))
	}
	if a.Source == SourceBuiltin {
		lines = append(lines, "Disabled: "+boolWordPtr(a.Disabled))
	}
	if a.OverrideScope != "" {
		lines = append(lines, "Override: "+a.OverrideScope+" ("+a.OverridePath+")")
	}
	if jsTrim(a.SystemPrompt) != "" {
		lines = append(lines, "", "System Prompt:", a.SystemPrompt)
	}
	return strings.Join(lines, "\n")
}

// subagentsAdminCommand is `/subagents [agent] [model|thinking|details]`.
func subagentsAdminCommand(ctx sdk.Context, args string) error {
	cwd := ctx.Cwd()
	agents := visibleAdminAgents(cwd)
	parts := strings.Fields(args)
	requested := ""
	if len(parts) > 0 {
		requested = parts[0]
	}
	requestedAction := ""
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "model":
			requestedAction = "Change model"
		case "thinking":
			requestedAction = "Change thinking level"
		case "prompt", "system-prompt", "edit":
			requestedAction = "Edit system prompt"
		case "details", "info":
			requestedAction = "Show details"
		}
	}
	if len(agents) == 0 {
		sendAdminMessage(ctx, notFoundAdminText(requested))
		return nil
	}
	var agent *AgentConfig
	if requested == "" {
		if !ctx.HasUI() {
			sendAdminMessage(ctx, agentListAdminText(agents))
			return nil
		}
		options := make([]string, 0, len(agents))
		for i := range agents {
			options = append(options, fmt.Sprintf("%s (%s)", agents[i].Name, agents[i].Source))
		}
		choice, ok, err := ctx.Select("Select subagent", options)
		if err != nil || !ok || choice == nil {
			return nil
		}
		picked, _ := choice.(string)
		for i := range agents {
			if fmt.Sprintf("%s (%s)", agents[i].Name, agents[i].Source) == picked {
				agent = &agents[i]
			}
		}
		if agent == nil {
			return nil
		}
	} else {
		var matches []AgentConfig
		for i := range agents {
			if m, _ := ResolveAgentName(requested, []AgentConfig{agents[i]}); m != nil {
				matches = append(matches, agents[i])
			}
		}
		switch {
		case len(matches) == 1:
			agent = &matches[0]
		case len(matches) > 1 && !ctx.HasUI():
			sendAdminMessage(ctx, ambiguousAdminText(requested, matches))
			return nil
		case len(matches) > 1:
			options := make([]string, 0, len(matches))
			for _, m := range matches {
				options = append(options, fmt.Sprintf("%s: %s", m.Source, m.FilePath))
			}
			choice, ok, err := ctx.Select(fmt.Sprintf("Choose a scope for '%s'", requested), options)
			if err != nil || !ok || choice == nil {
				return nil
			}
			picked, _ := choice.(string)
			for i := range matches {
				if fmt.Sprintf("%s: %s", matches[i].Source, matches[i].FilePath) == picked {
					agent = &matches[i]
				}
			}
			if agent == nil {
				return nil
			}
		default:
			sendAdminMessage(ctx, notFoundAdminText(requested))
			return nil
		}
	}
	action := requestedAction
	if action == "" {
		if !ctx.HasUI() {
			sendAdminMessage(ctx, adminMetadataFor(agent))
			return nil
		}
		choice, ok, err := ctx.Select(fmt.Sprintf("Administer %s\n%s", agent.Name, adminMetadataSummary(agent)),
			[]string{"Change model", "Change thinking level", "Edit system prompt", "Show details", "Done"})
		if err != nil || !ok || choice == nil {
			return nil
		}
		action, _ = choice.(string)
	}
	switch action {
	case "Change model":
		message := adminChooseModel(ctx, cwd, agent)
		if message == "" {
			return nil
		}
		ctx.Notify(message, "info")
		sendAdminMessage(ctx, message)
	case "Change thinking level":
		message := adminChooseThinking(ctx, cwd, agent)
		if message == "" {
			return nil
		}
		ctx.Notify(message, "info")
		sendAdminMessage(ctx, message)
	case "Edit system prompt":
		value, ok, err := ctx.Editor("System prompt: "+agent.Name, agent.SystemPrompt)
		if err != nil || !ok || value == nil {
			return nil
		}
		text, _ := value.(string)
		message := adminSaveSystemPrompt(cwd, agent, strings.TrimRight(text, " \t\n\r"))
		ctx.Notify(message, "info")
		sendAdminMessage(ctx, message)
	case "Show details":
		sendAdminMessage(ctx, adminMetadataFor(agent))
	}
	return nil
}

func notFoundAdminText(requested string) string {
	if requested == "" {
		return "Available subagents:\n" + agentListAdminText(visibleAdminAgents("."))
	}
	return fmt.Sprintf("Subagent '%s' not found.\n\nAvailable subagents:\n%s", requested, agentListAdminText(visibleAdminAgents(".")))
}

func agentListAdminText(agents []AgentConfig) string {
	if len(agents) == 0 {
		return "- (none)"
	}
	lines := make([]string, 0, len(agents))
	for i := range agents {
		lines = append(lines, fmt.Sprintf("- %s (%s)", agents[i].Name, agents[i].Source))
	}
	return strings.Join(lines, "\n")
}

func ambiguousAdminText(name string, matches []AgentConfig) string {
	lines := []string{fmt.Sprintf("Subagent '%s' is ambiguous. Choose a scope in interactive mode:", name)}
	for _, m := range matches {
		lines = append(lines, fmt.Sprintf("- %s: %s", m.Source, m.FilePath))
	}
	return strings.Join(lines, "\n")
}

// adminMetadataSummary is the original's one-line picker summary.
func adminMetadataSummary(a *AgentConfig) string {
	model := a.Model
	if model == "" {
		model = "default / inherit"
	}
	thinking := "default / inherit"
	if a.Thinking != nil {
		thinking = thinkingWord(a)
	}
	return fmt.Sprintf("Source: %s · Model: %s · Thinking: %s", a.Source, model, thinking)
}

// adminChooseModel is the original's chooseModel + saveAgentModel flow.
func adminChooseModel(ctx sdk.Context, cwd string, agent *AgentConfig) string {
	options := []string{inheritModelChoice}
	if agent.Model != "" {
		options = append([]string{agent.Model}, options...)
	}
	for _, m := range registryModels(ctx) {
		options = append(options, m.FullID)
	}
	choice, ok, err := ctx.Select("Select model for "+agent.Name, options)
	if err != nil || !ok || choice == nil {
		return ""
	}
	selected, _ := choice.(string)
	if selected == inheritModelChoice {
		selected = ""
	}
	if agent.Source == SourceBuiltin || agent.Source == SourcePackage {
		if selected == "" {
			path, _, _ := removeBuiltinAgentOverrideFields(cwd, agent.Name, "user", []string{"model"})
			return fmt.Sprintf("Cleared model settings override for '%s' in %s.", agent.Name, path)
		}
		path, errText := mergeBuiltinAgentOverride(cwd, agent.Name, "user", map[string]any{"model": selected})
		if errText != "" {
			return errText
		}
		return fmt.Sprintf("Saved user settings override for '%s' with model '%s' in %s.", agent.Name, selected, path)
	}
	path, errText := adminSaveCustomField(agent, map[string]any{"model": selected})
	if errText != "" {
		return errText
	}
	if selected == "" {
		return fmt.Sprintf("Cleared '%s' model in %s.", agent.Name, path)
	}
	return fmt.Sprintf("Updated '%s' model to '%s' in %s.", agent.Name, selected, path)
}

// adminChooseThinking is the original's chooseThinking + saveAgentThinking flow.
func adminChooseThinking(ctx sdk.Context, cwd string, agent *AgentConfig) string {
	current := inheritThinkingChoice
	if s, isString := agent.Thinking.(string); isString && s != "" {
		current = s
	} else if b, isBool := agent.Thinking.(bool); isBool && !b {
		current = "off"
	}
	options := []string{inheritThinkingChoice}
	if current != inheritThinkingChoice {
		options = append(options, current)
	}
	options = append(options, "off", "minimal", "low", "medium", "high", "xhigh", "max")
	modelNote := agent.Model
	if modelNote == "" {
		modelNote = jsTrim(ctx.ModelQualified())
	}
	if modelNote == "" {
		modelNote = "default / inherit"
	} else {
		modelNote = "Session model: " + modelNote
	}
	choice, ok, err := ctx.Select(fmt.Sprintf("Select thinking level for %s\n%s · %s", agent.Name, modelNote, current), options)
	if err != nil || !ok || choice == nil {
		return ""
	}
	selected, _ := choice.(string)
	if selected == inheritThinkingChoice {
		selected = ""
	}
	if agent.Source == SourceBuiltin || agent.Source == SourcePackage {
		if selected == "" {
			path, _, _ := removeBuiltinAgentOverrideFields(cwd, agent.Name, "user", []string{"thinking"})
			return fmt.Sprintf("Cleared thinking settings override for '%s' in %s.", agent.Name, path)
		}
		path, errText := mergeBuiltinAgentOverride(cwd, agent.Name, "user", map[string]any{"thinking": selected})
		if errText != "" {
			return errText
		}
		return fmt.Sprintf("Saved user settings override for '%s' with thinking '%s' in %s.", agent.Name, selected, path)
	}
	path, errText := adminSaveCustomField(agent, map[string]any{"thinking": selected})
	if errText != "" {
		return errText
	}
	if selected == "" {
		return fmt.Sprintf("Cleared '%s' thinking in %s.", agent.Name, path)
	}
	return fmt.Sprintf("Updated '%s' thinking to '%s' in %s.", agent.Name, selected, path)
}

// adminSaveSystemPrompt is the original's editSystemPrompt save half.
func adminSaveSystemPrompt(cwd string, agent *AgentConfig, text string) string {
	if agent.Source == SourceBuiltin || agent.Source == SourcePackage {
		path, errText := mergeBuiltinAgentOverride(cwd, agent.Name, "user", map[string]any{"systemPrompt": text})
		if errText != "" {
			return errText
		}
		return fmt.Sprintf("Saved user settings override for '%s' system prompt in %s.", agent.Name, path)
	}
	path, errText := adminSaveCustomField(agent, map[string]any{"systemPrompt": text})
	if errText != "" {
		return errText
	}
	return fmt.Sprintf("Updated '%s' system prompt in %s.", agent.Name, path)
}

// adminSaveCustomField writes a user/project agent's file through the update path.
func adminSaveCustomField(agent *AgentConfig, cfg map[string]any) (string, string) {
	if agent.Source != SourceUser && agent.Source != SourceProject {
		return "", fmt.Sprintf("Cannot update '%s' because that field is owned by its read-only package definition.", agent.Name)
	}
	preserve := preservedAgentFrontmatterFields(agent, cfg)
	updated := *agent
	if msg := applyAgentConfig(&updated, cfg); msg != "" {
		return "", msg
	}
	if err := writeFileAtomic(agent.FilePath, serializeAgent(&updated, preserve)); err != nil {
		return "", err.Error()
	}
	return agent.FilePath, ""
}

// runSlashCommand is `/run <agent> [task] [--bg] [--fork]`: one child, in the
// foreground unless --bg, through the same execution path as the tool.
func runSlashCommand(ctx sdk.Context, args string) error {
	bg, fork := false, false
	var kept []string
	for _, field := range strings.Fields(args) {
		switch field {
		case "--bg":
			bg = true
		case "--fork":
			fork = true
		default:
			kept = append(kept, field)
		}
	}
	if len(kept) == 0 {
		ctx.Notify("Usage: /run <agent> [task] [--bg] [--fork]", "error")
		return nil
	}
	agentToken := kept[0]
	task := strings.Join(kept[1:], " ")
	name, inline := parseAgentToken(agentToken)
	if name == "" {
		ctx.Notify("Usage: /run <agent> [task] [--bg] [--fork]", "error")
		return nil
	}
	d := DiscoverAgentsAll(ctx.Cwd())
	agents := MergeAgentsForScope(ScopeBoth, d.User, d.Project, d.Builtin, d.Package)
	if a, msg := ResolveAgentName(name, agents); a == nil {
		if msg == "" {
			msg = fmt.Sprintf("Unknown agent '%s'. Available: %s.", name, strings.Join(sortedNames(agents), ", "))
		}
		ctx.Notify(msg, "error")
		return nil
	}
	params := map[string]any{"agent": name, "task": task, "agentScope": "both", "async": bg}
	if fork {
		params["context"] = "fork"
	}
	for k, v := range inline {
		if params[k] == nil {
			params[k] = v
		}
	}
	res, err := executeSubagent(ctx, params)
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return nil
	}
	if ok, isText := res.(explicitOK); isText {
		sendSlashText(ctx, ok.Content)
	}
	return nil
}

// parseAgentToken splits `agent[key=value, ...]` into the name and its inline config.
func parseAgentToken(token string) (string, map[string]any) {
	open := strings.IndexByte(token, '[')
	if open == -1 || !strings.HasSuffix(token, "]") {
		return token, nil
	}
	name := token[:open]
	inline := map[string]any{}
	body := token[open+1 : len(token)-1]
	for _, pair := range strings.Split(body, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		k, v = jsTrim(k), jsTrim(v)
		if k != "" {
			inline[k] = v
		}
	}
	return name, inline
}

func guideSlashCommand(ctx sdk.Context, args string) error {
	if strings.Contains(jsTrim(args), " ") {
		ctx.Notify("Usage: /subagents-guide [topic]", "error")
		return nil
	}
	r := guideAction(map[string]any{"topic": jsTrim(args)})
	if r.isError {
		ctx.Notify(r.text, "error")
		return nil
	}
	sendSlashText(ctx, r.text)
	return nil
}

func modelsSlashCommand(ctx sdk.Context, args string) error {
	view := modelsView{current: jsTrim(ctx.ModelQualified()), models: registryModels(ctx)}
	switch parts := strings.Fields(args); len(parts) {
	case 0:
		sendSlashText(ctx, modelsAction(view, ctx.Cwd(), map[string]any{}).text)
	case 1:
		sendSlashText(ctx, modelsAction(view, ctx.Cwd(), map[string]any{"agent": parts[0]}).text)
	default:
		ctx.Notify("Usage: /subagents-models [agent-name]", "error")
	}
	return nil
}

func fleetSlashCommand(ctx sdk.Context, args string) error {
	sendSlashText(ctx, inspectFleet(sessionIDOf(ctx)).text)
	return nil
}

// stopSlashCommand is `/subagents-stop [run-id] [child-id]`.
func stopSlashCommand(ctx sdk.Context, args string) error {
	fields := strings.Fields(args)
	if len(fields) > 2 {
		sendSlashText(ctx, "Usage: /subagents-stop [run-id] [child-id]")
		return nil
	}
	if len(fields) >= 1 {
		params := map[string]any{"id": fields[0]}
		if len(fields) == 2 {
			params["childId"] = fields[1]
		}
		if _, err := stopAction(ctx, params); err != nil {
			ctx.Notify(err.Error(), "error")
		}
		return nil
	}
	targets := activeRunsForStop(ctx)
	if len(targets) == 0 {
		ctx.Notify("No active current-session async runs or scheduled subagent runs to stop.", "info")
		return nil
	}
	if !ctx.HasUI() {
		sendSlashText(ctx, stopFallbackText(targets))
		return nil
	}
	options := make([]string, 0, len(targets))
	for _, r := range targets {
		options = append(options, stopTargetLabel(r))
	}
	choice, ok, err := ctx.Select("Stop which run?", options)
	if err != nil || !ok || choice == nil {
		return nil
	}
	picked, _ := choice.(string)
	for i, r := range targets {
		if stopTargetLabel(r) == picked {
			if _, err := stopAction(ctx, map[string]any{"id": targets[i].ID}); err != nil {
				ctx.Notify(err.Error(), "error")
			}
			return nil
		}
	}
	return nil
}

func activeRunsForStop(ctx sdk.Context) []*asyncRun {
	return runs.activeForSession(sessionIDOf(ctx))
}

func stopTargetLabel(r *asyncRun) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("%s run %s (%s)", r.Agent, r.ID, r.State)
}

// stopFallbackText is the original's headless stop-target list.
func stopFallbackText(targets []*asyncRun) string {
	lines := []string{"Subagent stop targets:", ""}
	for _, r := range targets {
		r.mu.Lock()
		label := fmt.Sprintf("%s run %s", r.Agent, r.ID)
		detail := fmt.Sprintf("%s · %s", r.State, r.Mode)
		r.mu.Unlock()
		lines = append(lines, "- "+label)
		lines = append(lines, "  "+detail)
		lines = append(lines, fmt.Sprintf("  Stop run: subagent({ action: \"stop\", id: \"%s\" })", r.ID))
		lines = append(lines, "  slash: /subagents-stop "+r.ID)
	}
	return strings.Join(lines, "\n")
}

// writeFileAtomic writes a definition file through a temp file, so a reader
// never sees a half-written definition.
func writeFileAtomic(path, content string) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
