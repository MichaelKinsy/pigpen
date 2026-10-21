package tintinweb_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `/agents` interactive menu: the running agents, the agent types (with the enable/disable/eject/edit/delete file
// toggles), scheduled jobs, workflow runs, the create wizard and the settings. It is written against menuUI, so the
// same code runs over a command's context and over the scripted answers of a test. upstream: src/index.ts (showAgentsMenu
// and the functions it opens), src/ui/select-item.ts, src/ui/schedule-menu.ts.

// agentsMenu is one run of `/agents`.
type agentsMenu struct {
	a   *app
	ui  menuUI
	cwd string
	// models lists the available models, for the labels of the type list (nil: no registry, labels stay as configured).
	models func() []map[string]any
	// abandoned is closed when the command's context is, so a wait in the wizard ends with the session.
	abandoned <-chan struct{}
	// numericDone is set when a typed settings value was applied, so the settings menu opens again.
	numericDone bool
}

func (a *app) newAgentsMenu(ctx sdk.Context) *agentsMenu {
	return &agentsMenu{a: a, ui: a.menuUIFor(ctx), cwd: ctx.Cwd(), abandoned: ctx.Done(),
		models: func() (list []map[string]any) {
			guard(func() { list, _ = ctx.ModelRegistry().GetAvailable() })
			return list
		}}
}

// agentsCommand is `/agents`. upstream: pi.registerCommand("agents") -> showAgentsMenu.
func (a *app) agentsCommand(ctx sdk.Context, _ string) error {
	a.touch(ctx)
	a.newAgentsMenu(ctx).show()
	return nil
}

// reloadCustomAgents re-reads the agent files and registers them over the defaults. Only the agents: the settings are
// the menu's own live state, and a reload that re-applied the file would undo a change that failed to persist.
func (a *app) reloadCustomAgents(cwd string) {
	custom, warnings := loadCustomAgents(cwd)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "[pi-subagents] %s\n", w)
	}
	registerAgents(custom)
}

func (m *agentsMenu) reload() { m.a.reloadCustomAgents(m.cwd) }

// getModelLabel is the model column of the type list: what is configured, flagged when it cannot resolve and when it
// resolves to something else. upstream: index.ts getModelLabel.
func (m *agentsMenu) getModelLabel(typ string) string {
	cfg := getAgentConfig(typ)
	if cfg == nil || cfg.Model == "" {
		return "inherit" // no model configured: really inherits the parent
	}
	label := modelLabel(cfg.Model)
	if m.models == nil {
		return label
	}
	available := m.models()
	if available == nil {
		return label
	}
	resolved, msg := resolveModelAmong(cfg.Model, available)
	// Configured but unresolvable: the runtime silently falls back to the parent model, so flag it (and the fallback)
	// rather than hiding the config.
	if msg != "" {
		return label + " (unavailable, fallback: inherit)"
	}
	// Surface what it actually resolved to when that differs from the config, e.g. a provider fallback or a looser
	// version pin. Cosmetic separator and date differences are normalized away so an effectively identical match stays quiet.
	full := msgStr(resolved["provider"]) + "/" + msgStr(resolved["id"])
	norm := func(s string) string { return datedModel.ReplaceAllString(strings.ReplaceAll(strings.ToLower(s), ".", "-"), "") }
	if norm(cfg.Model) == norm(full) {
		return label
	}
	return label + " (→ " + datedModel.ReplaceAllString(full, "") + ")"
}

func registryKeys() []string { return append([]string(nil), currentRegistry().keys...) }

// show is showAgentsMenu: loop until the user leaves or starts creating an agent.
func (m *agentsMenu) show() {
	for m.once() {
	}
}

// once shows the menu once and runs the entry chosen; true means show it again.
func (m *agentsMenu) once() bool {
	m.reload()
	allNames := registryKeys()

	var options []string
	var agents []*agentView
	for _, v := range m.a.ui.listAgents() {
		if isTopLevelAgent(v) {
			agents = append(agents, v)
		}
	}
	if len(agents) > 0 {
		running, done := 0, 0
		for _, v := range agents {
			switch v.Status {
			case statusRunning, statusQueued:
				running++
			case statusCompleted, statusSteered:
				done++
			}
		}
		options = append(options, fmt.Sprintf("Running agents (%d) — %d running, %d done", len(agents), running, done))
	}
	if len(allNames) > 0 {
		options = append(options, fmt.Sprintf("Agent types (%d)", len(allNames)))
	}
	// Shown while the scheduler is active, so the menu never advertises something switched off.
	if entry, ok := m.a.scheduleMenuEntry(); ok {
		options = append(options, entry)
	}
	// Workflow runs, on the same terms as scheduled jobs.
	if entry := m.a.workflowsMenuEntry(); entry != "" {
		options = append(options, entry)
	}
	options = append(options, "Create new agent", "Settings")

	if len(allNames) == 0 && len(agents) == 0 {
		m.ui.Notify("No agents found. Create specialized subagents that can be delegated to.\n\n"+
			"Each subagent has its own context window, custom system prompt, and specific tools.\n\n"+
			"Try creating: Code Reviewer, Security Auditor, Test Writer, or Documentation Writer.\n\n", "info")
	}

	choice, ok, err := m.ui.Select("Agents", options)
	if err != nil || !ok || choice == "" {
		return false
	}
	switch {
	case strings.HasPrefix(choice, "Running agents ("):
		m.showRunningAgents()
	case strings.HasPrefix(choice, "Agent types ("):
		m.showAllAgentsList()
	case strings.HasPrefix(choice, "Scheduled jobs ("):
		m.showSchedulesMenu()
	case strings.HasPrefix(choice, "Workflows ("):
		showWorkflowsMenu(m.ui, m.a.workflowMenuDeps())
	case choice == "Create new agent":
		m.showCreateWizard()
		return false
	case choice == "Settings":
		m.showSettings()
	default:
		return false
	}
	return true
}

// showSchedulesMenu lists the scheduled jobs; selecting one opens a cancel confirm with its details. It returns when the
// user backs out or after a cancellation. upstream: ui/schedule-menu.ts showSchedulesMenu.
func (m *agentsMenu) showSchedulesMenu() {
	sched := m.a.scheduler()
	if !sched.isActive() {
		m.ui.Notify("Scheduler is not active in this session.", "warning")
		return
	}
	jobs := sched.list()
	if len(jobs) == 0 {
		m.ui.Notify("No scheduled jobs.", "info")
		return
	}
	// Numbered and item-paired: two jobs whose names agree in the first 18 characters format identically, and matching
	// the returned string back against a parallel label array would cancel whichever came first.
	now := wfNowMs()
	job, ok := selectItem(m.ui, fmt.Sprintf("Scheduled jobs (%d) — select to cancel", len(jobs)), jobs,
		func(j scheduledSubagent, _ int) string { return m.a.scheduleFormatJob(j, now) })
	if !ok {
		return
	}
	confirmed, err := m.ui.Confirm(fmt.Sprintf("Cancel %q?", job.Name), m.a.scheduleFormatDetails(job))
	if err != nil || !confirmed {
		return
	}
	_, _ = sched.removeJob(job.ID)
	m.ui.Notify(fmt.Sprintf("Cancelled %q.", job.Name), "info")
}

// showRunningAgents lists this session's agents; picking one opens its conversation, and the list comes back.
func (m *agentsMenu) showRunningAgents() {
	for {
		var agents []*agentView
		for _, v := range m.a.ui.listAgents() {
			if isTopLevelAgent(v) {
				agents = append(agents, v)
			}
		}
		if len(agents) == 0 {
			m.ui.Notify("No agents.", "info")
			return
		}
		// Numbered and item-paired: two same-type agents spawned together with the same description render identically
		// here, and resolving the choice by string match would open whichever came first.
		record, ok := selectItem(m.ui, "Running agents", agents, func(v *agentView, _ int) string {
			return fmt.Sprintf("%s (%s) · %d tools · %s · %s", getDisplayName(v.Type), v.Description, v.ToolUses, v.Status,
				formatDuration(v.StartedAt, v.CompletedAt))
		})
		if !ok {
			return
		}
		m.a.viewAgentConversation(m.ui, record)
		// Back-navigation: show the list again.
	}
}

// viewAgentConversation opens the live conversation overlay of an agent and returns when it has closed.
func (a *app) viewAgentConversation(ui menuUI, record *agentView) {
	if !record.HasSession {
		state := "expired"
		if record.Status == statusQueued {
			state = "queued"
		}
		ui.Notify("Agent is "+state+" — no session available.", "info")
		return
	}
	id, description := record.ID, record.Description
	showCost := a.ui.getShowCost()
	ui.Custom(func(host viewerHost, th uiTheme, done func(any)) viewerComponent {
		return newConversationViewer(host, a.ui.sessionOf(id), a.ui.fleet.recordOf(id), a.ui.fleet.activityOfFn(id), th, func() { done(nil) }, viewerOptions{
			// The abort is a round trip to the child: it runs off the viewer's input path.
			OnStop: func() {
				safeGo("viewer stop", func() {
					if a.ui.abort(id) {
						ui.Notify("Stopped \""+description+"\".", "info")
					}
				})
			},
			OnSteer:        func(message string) { _ = a.ui.steer(id, message) },
			ShowCost:       showCost,
			ViewerMarkdown: a.ui.getViewerMarkdown,
			OnMarkdownMode: a.ui.chooseViewerMarkdown,
		})
	}, &menuOverlay{overlayOptions: overlayOptions{Anchor: "center", WidthPercent: 90, HeightPercent: viewportHeightPct}})
}

// sourceIndicator: defaults unmarked, custom agents get • (project) or ◦ (global); disabled agents get a ✕ prefix.
func sourceIndicator(cfg *agentConfig) string {
	disabled := cfg != nil && !cfg.Enabled
	switch {
	case cfg != nil && cfg.Source == "project":
		if disabled {
			return "✕• "
		}
		return "•  "
	case cfg != nil && cfg.Source == "global":
		if disabled {
			return "✕◦ "
		}
		return "◦  "
	case disabled:
		return "✕  "
	}
	return "   "
}

// showAllAgentsList is the `Agent types` picker: one row per agent (name left, model right) with the full description
// below the highlighted row, exactly like the Settings menu, so long descriptions never wrap the list. Enter on a row
// opens that agent's detail.
func (m *agentsMenu) showAllAgentsList() {
	for {
		allNames := registryKeys()
		if len(allNames) == 0 {
			m.ui.Notify("No agents.", "info")
			return
		}
		items := make([]*settingItem, len(allNames))
		hasCustom, hasDisabled := false, false
		for i, name := range allNames {
			cfg := getAgentConfig(name)
			disabled := cfg != nil && !cfg.Enabled
			model := m.getModelLabel(name)
			description := name
			if disabled {
				description = "(disabled)"
			} else if cfg != nil {
				description = cfg.Description
			}
			// A single-value list, so Enter "activates" the row (fires onChange with the agent's id) without offering
			// anything to cycle.
			items[i] = &settingItem{ID: name, Label: sourceIndicator(cfg) + name, CurrentValue: model, Description: description, Values: []string{model}}
			if cfg != nil && !cfg.IsDefault && cfg.Enabled {
				hasCustom = true
			}
			hasDisabled = hasDisabled || disabled
		}
		var legend []string
		if hasCustom {
			legend = append(legend, "• = project  ◦ = global")
		}
		if hasDisabled {
			legend = append(legend, "✕ = disabled")
		}
		value, _ := m.ui.Custom(func(_ viewerHost, th uiTheme, done func(any)) viewerComponent {
			slTheme := settingsListThemeFor(th)
			list := newSettingsList(items, min(len(items), 12), slTheme,
				func(id, _ string) { done(id) }, // Enter/Space on a row returns that agent's name
				func() { done(nil) })            // Esc cancels
			hint := ""
			if len(legend) > 0 {
				hint = slTheme.Hint(strings.Join(legend, "  "))
			}
			return &menuList{title: "Agent types", hint: hint, list: list}
		}, nil)
		selected, _ := value.(string)
		if selected == "" || getAgentConfig(selected) == nil {
			return
		}
		m.showAgentDetail(selected)
	}
}

// agentFileOf is the file behind an agent type, nil for a built-in that has none.
func (m *agentsMenu) agentFileOf(name string, cfg *agentConfig) *agentFile {
	src := ""
	if cfg != nil {
		src = cfg.SourcePath
	}
	return locateAgentFile(name, src, m.cwd)
}

func (m *agentsMenu) showAgentDetail(name string) {
	cfg := getAgentConfig(name)
	if cfg == nil {
		m.ui.Notify(fmt.Sprintf("Agent config not found for %q.", name), "warning")
		return
	}
	file := m.agentFileOf(name, cfg)
	isDefault := cfg.IsDefault
	disabled := !cfg.Enabled

	var menuOptions []string
	switch {
	case disabled && file != nil:
		// Disabled agent with a file: offer Enable.
		if isDefault {
			menuOptions = []string{"Enable", "Edit", "Reset to default", "Delete", "Back"}
		} else {
			menuOptions = []string{"Enable", "Edit", "Delete", "Back"}
		}
	case isDefault && file == nil:
		// Default agent with no .md override.
		menuOptions = []string{"Eject (export as .md)", "Disable", "Back"}
	case isDefault && file != nil:
		// Default agent with .md override (ejected).
		menuOptions = []string{"Edit", "Disable", "Reset to default", "Delete", "Back"}
	default:
		// User-defined agent.
		menuOptions = []string{"Edit", "Disable", "Delete", "Back"}
	}

	choice, ok, err := m.ui.Select(name, menuOptions)
	if err != nil || !ok || choice == "" || choice == "Back" {
		return
	}
	switch {
	case choice == "Edit" && file != nil:
		data, err := os.ReadFile(file.Path)
		if err != nil {
			m.ui.Notify("Could not read "+file.Path+": "+err.Error(), "error")
			return
		}
		content := string(data)
		edited, ok, err := m.ui.Editor("Edit "+name, content)
		if err == nil && ok && edited != content {
			if err := os.WriteFile(file.Path, []byte(edited), 0o666); err != nil {
				m.ui.Notify("Could not write "+file.Path+": "+err.Error(), "error")
				return
			}
			m.reload()
			m.ui.Notify("Updated "+file.Path, "info")
		}
	case choice == "Delete":
		if file == nil {
			return
		}
		confirmed, err := m.ui.Confirm("Delete agent", fmt.Sprintf("Delete %s from %s (%s)?", name, file.Location, file.Path))
		if err == nil && confirmed {
			if err := os.Remove(file.Path); err != nil {
				m.ui.Notify("Could not delete "+file.Path+": "+err.Error(), "error")
				return
			}
			m.reload()
			m.ui.Notify("Deleted "+file.Path, "info")
		}
	case choice == "Reset to default" && file != nil:
		confirmed, err := m.ui.Confirm("Reset to default", fmt.Sprintf("Delete override %s and restore embedded default?", file.Path))
		if err == nil && confirmed {
			if err := os.Remove(file.Path); err != nil {
				m.ui.Notify("Could not delete "+file.Path+": "+err.Error(), "error")
				return
			}
			m.reload()
			m.ui.Notify("Restored default "+name, "info")
		}
	case strings.HasPrefix(choice, "Eject"):
		m.ejectAgent(name, cfg)
	case choice == "Disable":
		m.disableAgent(name)
	case choice == "Enable":
		m.enableAgent(name)
	}
}

// chooseAgentsDir asks where a new agent file goes. nil means the user backed out.
func (m *agentsMenu) chooseAgentsDir() (dir string, ok bool) {
	location, ok, err := m.ui.Select("Choose location", []string{"Project (.pi/agents/)", "Personal (" + personalAgentsDir() + ")"})
	if err != nil || !ok || location == "" {
		return "", false
	}
	if strings.HasPrefix(location, "Project") {
		return projectAgentsDir(m.cwd), true
	}
	return personalAgentsDir(), true
}

// ejectAgent writes a default agent's embedded config as a .md file.
func (m *agentsMenu) ejectAgent(name string, cfg *agentConfig) {
	targetDir, ok := m.chooseAgentsDir()
	if !ok {
		return
	}
	if !m.mkdir(targetDir) {
		return
	}
	targetPath := filepath.Join(targetDir, name+".md")
	if fileExists(targetPath) {
		if overwrite, err := m.ui.Confirm("Overwrite", targetPath+" already exists. Overwrite?"); err != nil || !overwrite {
			return
		}
	}
	if !m.write(targetPath, serializeAgentFile(cfg)) {
		return
	}
	m.reload()
	m.ui.Notify("Ejected "+name+" to "+targetPath, "info")
}

func (m *agentsMenu) mkdir(dir string) bool {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		m.ui.Notify("Could not create "+dir+": "+err.Error(), "error")
		return false
	}
	return true
}

func (m *agentsMenu) write(path, content string) bool {
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		m.ui.Notify("Could not write "+path+": "+err.Error(), "error")
		return false
	}
	return true
}

// disableAgent sets `enabled: false` in the agent's .md file, or creates a stub for a built-in default.
func (m *agentsMenu) disableAgent(name string) {
	cfg := getAgentConfig(name)
	if file := m.agentFileOf(name, cfg); file != nil {
		// Existing file: set enabled: false in the frontmatter (idempotent).
		data, err := os.ReadFile(file.Path)
		if err != nil {
			m.ui.Notify("Could not read "+file.Path+": "+err.Error(), "error")
			return
		}
		updated, outcome := disableInContent(string(data))
		switch outcome {
		case outcomeAlreadyDisabled:
			m.ui.Notify(name+" is already disabled.", "info")
			return
		case outcomeNoFrontmatter:
			// Nothing to edit: say so rather than rewriting the file unchanged and reporting success for a change that
			// never happened.
			m.ui.Notify(fmt.Sprintf("Cannot disable %s: %s has no frontmatter block.", name, file.Path), "error")
			return
		}
		if !m.write(file.Path, updated) {
			return
		}
		m.reload()
		m.ui.Notify(fmt.Sprintf("Disabled %s (%s)", name, file.Path), "info")
		return
	}
	// No file (a built-in default): create a stub.
	targetDir, ok := m.chooseAgentsDir()
	if !ok {
		return
	}
	if !m.mkdir(targetDir) {
		return
	}
	targetPath := filepath.Join(targetDir, name+".md")
	if !m.write(targetPath, "---\nenabled: false\n---\n") {
		return
	}
	m.reload()
	m.ui.Notify(fmt.Sprintf("Disabled %s (%s)", name, targetPath), "info")
}

// enableAgent removes `enabled: false` from a disabled agent's frontmatter.
func (m *agentsMenu) enableAgent(name string) {
	cfg := getAgentConfig(name)
	file := m.agentFileOf(name, cfg)
	if file == nil {
		return
	}
	data, err := os.ReadFile(file.Path)
	if err != nil {
		m.ui.Notify("Could not read "+file.Path+": "+err.Error(), "error")
		return
	}
	updated, changed := enableInContent(string(data))
	if !changed && !isEmptyStub(updated) {
		// The file carries no `enabled: false` to remove, so it was never disabled by us: reporting success here would
		// hide a no-op.
		m.ui.Notify(fmt.Sprintf("%s is not disabled in %s.", name, file.Path), "info")
		return
	}
	// If the file was just a stub ("---\n---\n"), delete it to restore the built-in default.
	if isEmptyStub(updated) {
		if err := os.Remove(file.Path); err != nil {
			m.ui.Notify("Could not delete "+file.Path+": "+err.Error(), "error")
			return
		}
		m.reload()
		m.ui.Notify(fmt.Sprintf("Enabled %s (removed %s)", name, file.Path), "info")
		return
	}
	if !m.write(file.Path, updated) {
		return
	}
	m.reload()
	m.ui.Notify(fmt.Sprintf("Enabled %s (%s)", name, file.Path), "info")
}

// thinkingLevels are the advertised levels. upstream: index.ts THINKING_LEVELS.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func (m *agentsMenu) showCreateWizard() {
	targetDir, ok := m.chooseAgentsDir()
	if !ok {
		return
	}
	method, ok, err := m.ui.Select("Creation method", []string{"Generate with Claude (recommended)", "Manual configuration"})
	if err != nil || !ok || method == "" {
		return
	}
	if strings.HasPrefix(method, "Generate") {
		m.showGenerateWizard(targetDir)
	} else {
		m.showManualWizard(targetDir)
	}
}

// input is ctx.ui.input answered, or "" when cancelled or left empty (the original treats both as backing out).
func (m *agentsMenu) input(title, placeholder string) string {
	text, ok, err := m.ui.Input(title, placeholder)
	if err != nil || !ok {
		return ""
	}
	return text
}

func (m *agentsMenu) showGenerateWizard(targetDir string) {
	description := m.input("Describe what this agent should do", "")
	if description == "" {
		return
	}
	name := m.input("Agent name (filename, no spaces)", "")
	if name == "" {
		return
	}
	if !m.mkdir(targetDir) {
		return
	}
	targetPath := filepath.Join(targetDir, name+".md")
	if fileExists(targetPath) {
		if overwrite, err := m.ui.Confirm("Overwrite", targetPath+" already exists. Overwrite?"); err != nil || !overwrite {
			return
		}
	}
	m.ui.Notify("Generating agent definition...", "info")

	// Offering the field on a project that turned worktrees off would bake a request that is refused at spawn time into
	// a file that outlives the session. Built per invocation, so this read is live.
	isolationLine := ""
	if m.a.held.get().worktreeIsolation {
		isolationLine = "\nisolation: <\"worktree\" to run in isolated git worktree; \"off\" to refuse one even when the caller asks. Omit for normal>"
	}
	prompt := generateAgentPrompt(description, targetPath, isolationLine)

	ctx, ok := m.a.context()
	if !ok {
		return
	}
	// Run from a modal wizard, not a tool call: it passes no signal, and Esc in the dialogs never reaches the manager.
	// Exempt from the foreground limit (this port has none) and a foreground agent, so it is not queued.
	r, err := m.a.start(ctx, "general-purpose", prompt, "Generate "+name+" agent", "", "", "", 5, false, false)
	if err != nil {
		m.ui.Notify("Generation failed: "+err.Error(), "warning")
		return
	}
	select {
	case <-r.done:
	case <-m.abandoned:
		return
	}
	m.a.mgr.mu.Lock()
	status, errText := r.Status, r.Error
	m.a.mgr.mu.Unlock()
	if status == statusError {
		m.ui.Notify("Generation failed: "+errText, "warning")
		return
	}
	m.reload()
	if fileExists(targetPath) {
		m.ui.Notify("Created "+targetPath, "info")
	} else {
		m.ui.Notify("Agent generation completed but file was not created. Check the agent output.", "warning")
	}
}

// generateAgentPrompt is the instruction the "Generate with Claude" wizard gives the general-purpose agent. The
// isolation field is only offered while worktree isolation is on. upstream: index.ts showGenerateWizard.
func generateAgentPrompt(description, targetPath, isolationLine string) string {
	return "Create a custom pi sub-agent definition file based on this description: \"" + description + "\"\n\n" +
		"Write a markdown file to: " + targetPath + "\n\n" +
		"The file format is a markdown file with YAML frontmatter and a system prompt body:\n\n" +
		"```markdown\n" +
		"---\n" +
		"description: <one-line description shown in UI>\n" +
		"color: <optional agent name badge color: red, blue, green, yellow, purple, orange, pink, cyan, an Agency Agents alias, or quoted \"#RRGGBB\">\n" +
		"tools: <comma-separated built-in tools: read, bash, edit, write, grep, find, ls. Use \"none\" for no tools. Omit for all tools>\n" +
		"model: <optional model as \"provider/modelId\", e.g. \"anthropic/claude-haiku-4-5\". Omit to inherit parent model>\n" +
		"thinking: <optional thinking level: " + strings.Join(thinkingLevels, ", ") + ". Omit to inherit>\n" +
		"max_turns: <optional max agentic turns. 0 or omit for unlimited (default)>\n" +
		"prompt_mode: <\"replace\" (body IS the full system prompt) or \"append\" (body is appended to default prompt). Default: replace>\n" +
		"extensions: <true (inherit all MCP/extension tools), false (none), or comma-separated names. Default: true>\n" +
		"skills: <true (inherit all), false (none), or comma-separated skill names to preload into prompt. Default: true>\n" +
		"disallowed_tools: <comma-separated tool names to block, even if otherwise available. Omit for none>\n" +
		"inherit_context: <true to fork parent conversation into agent so it sees chat history. Default: false>\n" +
		"run_in_background: <pin this agent to background (true) or foreground (false). Omit to follow the backgroundByDefault setting, which is background>\n" +
		"output_transcript: <false to write no transcript file or path for this agent. Independent of persist_session. Default: true>\n" +
		"isolated: <true for no extension/MCP tools, only built-in tools. Default: false>\n" +
		"memory: <\"user\" (global), \"project\" (per-project), or \"local\" (gitignored per-project) for persistent memory. Omit for none>" + isolationLine + "\n" +
		"---\n\n" +
		"<system prompt body — instructions for the agent>\n" +
		"```\n\n" +
		"Guidelines for choosing settings:\n" +
		"- For read-only tasks (review, analysis): tools: read, bash, grep, find, ls\n" +
		"- For code modification tasks: include edit, write\n" +
		"- Use prompt_mode: append if the agent should keep the default system prompt and add specialization on top\n" +
		"- Use prompt_mode: replace for fully custom agents with their own personality/instructions\n" +
		"- Set inherit_context: true if the agent needs to know what was discussed in the parent conversation\n" +
		"- Set isolated: true if the agent should NOT have access to MCP servers or other extensions\n" +
		"- Set output_transcript: false to skip writing this agent's transcript; this alone doesn't keep the run off disk (persist_session, isolation: worktree commits, and memory still write) — set those too if that's the goal\n" +
		"- Only include frontmatter fields that differ from defaults — omit fields where the default is fine\n\n" +
		"Write the file using the write tool. Only write the file, nothing else."
}

func (m *agentsMenu) showManualWizard(targetDir string) {
	// 1. Name.
	name := m.input("Agent name (filename, no spaces)", "")
	if name == "" {
		return
	}
	// 2. Description.
	description := m.input("Description (one line)", "")
	if description == "" {
		return
	}
	// 3. Tools.
	toolChoice, ok, err := m.ui.Select("Tools", []string{"all", "none", "read-only (read, bash, grep, find, ls)", "custom..."})
	if err != nil || !ok || toolChoice == "" {
		return
	}
	var tools string
	switch {
	case toolChoice == "all":
		tools = strings.Join(builtinToolNames, ", ")
	case toolChoice == "none":
		tools = "none"
	case strings.HasPrefix(toolChoice, "read-only"):
		tools = "read, bash, grep, find, ls"
	default:
		tools = m.input("Tools (comma-separated)", strings.Join(builtinToolNames, ", "))
		if tools == "" {
			return
		}
	}
	// 4. Model.
	modelChoice, ok, err := m.ui.Select("Model", []string{"inherit (parent model)", "haiku", "sonnet", "opus", "custom..."})
	if err != nil || !ok || modelChoice == "" {
		return
	}
	model := ""
	switch modelChoice {
	case "haiku":
		model = "anthropic/claude-haiku-4-5"
	case "sonnet":
		model = "anthropic/claude-sonnet-4-6"
	case "opus":
		model = "anthropic/claude-opus-4-6"
	case "custom...":
		model = m.input("Model (provider/modelId)", "")
	}
	// 5. Thinking: "inherit" is a UI-only pseudo-choice (omit the field); the rest mirror pi.
	thinkingChoice, ok, err := m.ui.Select("Thinking level", append([]string{"inherit"}, thinkingLevels...))
	if err != nil || !ok || thinkingChoice == "" {
		return
	}
	// 6. System prompt.
	systemPrompt, ok, err := m.ui.Editor("System prompt", "")
	if err != nil || !ok {
		return
	}
	thinking := thinkingChoice
	if thinkingChoice == "inherit" {
		thinking = ""
	}
	content := buildNewAgentFile(newAgentInput{Description: description, Tools: tools, Model: model, Thinking: thinking, SystemPrompt: systemPrompt})

	if !m.mkdir(targetDir) {
		return
	}
	targetPath := filepath.Join(targetDir, name+".md")
	if fileExists(targetPath) {
		if overwrite, err := m.ui.Confirm("Overwrite", targetPath+" already exists. Overwrite?"); err != nil || !overwrite {
			return
		}
	}
	if !m.write(targetPath, content) {
		return
	}
	m.reload()
	m.ui.Notify("Created "+targetPath, "info")
}
