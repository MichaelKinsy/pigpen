package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The /subagents admin command (src/slash/subagents-admin.ts): inspect an agent's metadata and change its model, thinking level or
// system prompt. The choices go through the host's select dialog (the flat path of the original, used when ui.custom is not
// available); the original's selector overlay component is not ported.

const (
	adminMessageType      = "subagents-admin"
	inheritModelChoice    = "Default / inherit session model"
	inheritThinkingChoice = "Default / inherit session thinking"
)

// adminHost is the session surface the admin command works against.
type adminHost struct {
	cwd           string
	hasUI         bool
	modelProvider string
	modelID       string
	// refresh reloads the local registry without network discovery: (aborted, first error as "provider: message", failure).
	refresh   func() (aborted bool, firstErrProvider, firstErr string, err error)
	available func() []ModelInfo
	notify    func(message, level string)
	send      func(content string)
	// sel is ui.select (ok is false when cancelled); edit is ui.editor.
	sel  func(title string, items []string) (string, bool)
	edit func(title, prefill string) (string, bool)
}

func adminSourceRank(s AgentSource) int {
	switch s {
	case SourceProject:
		return 0
	case SourceUser:
		return 1
	case SourcePackage:
		return 2
	}
	return 3
}

// allVisibleAgents: the configured agents that are not disabled (disabled definitions stay in collision checks of the runtime
// registry, which this port does not have).
func (h adminHost) allVisibleAgents() ([]AgentConfig, error) {
	all, err := discoverAgentsAll(h.cwd, h.modelProvider, adv.options())
	if err != nil {
		return nil, err
	}
	var agents []AgentConfig
	for _, group := range [][]AgentConfig{all.Project, all.User, all.Package, all.Builtin} {
		for _, a := range group {
			if a.Disabled == nil || !*a.Disabled {
				agents = append(agents, a)
			}
		}
	}
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Name != agents[j].Name {
			return localeLess(agents[i].Name, agents[j].Name)
		}
		return adminSourceRank(agents[i].Source) < adminSourceRank(agents[j].Source)
	})
	return agents, nil
}

func agentLabel(a AgentConfig) string {
	model := ""
	if a.Model != "" {
		model = " · " + a.Model
	}
	return fmt.Sprintf("%s [%s]%s — %s", a.Name, a.Source, model, a.Description)
}

// agentChoices labels agents for a select dialog; equal labels are told apart by the file path.
func agentChoices(agents []AgentConfig) ([]string, map[string]AgentConfig) {
	labels := make([]string, len(agents))
	counts := map[string]int{}
	for i, a := range agents {
		labels[i] = agentLabel(a)
		counts[labels[i]]++
	}
	byLabel := map[string]AgentConfig{}
	var order []string
	for i, a := range agents {
		l := labels[i]
		if counts[l] != 1 {
			l += " · " + a.FilePath
		}
		byLabel[l] = a
		order = append(order, l)
	}
	return order, byLabel
}

func adminMatches(a AgentConfig, raw string) bool {
	name := jsTrim(raw)
	return a.Name == name || frontmatterName(a.Name, a.LocalName, a.PackageName) == name
}

func adminMetadata(a AgentConfig) string {
	tools := append([]string(nil), a.Tools...)
	for _, t := range a.McpDirectTools {
		tools = append(tools, "mcp:"+t)
	}
	lines := []string{fmt.Sprintf("Agent: %s (%s)", a.Name, a.Source), "Path: " + a.FilePath, "Description: " + a.Description}
	if a.PackageName != "" {
		lines = append(lines, "Local name: "+frontmatterName(a.Name, a.LocalName, a.PackageName), "Package: "+a.PackageName)
	}
	model := a.Model
	if model == "" {
		model = "default / inherit"
	}
	lines = append(lines, "Model: "+model)
	switch t := a.Thinking.(type) {
	case string:
		lines = append(lines, "Thinking: "+t)
	case bool:
		if !t {
			lines = append(lines, "Thinking: off")
		}
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
	tf := func(b bool) string { return fmt.Sprint(b) }
	lines = append(lines, "System prompt mode: "+a.SystemPromptMode, "Inherit project context: "+tf(a.InheritProjectContext),
		"Inherit global context: "+tf(a.InheritGlobalContext), "Inherit skills: "+tf(a.InheritSkills))
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
		lines = append(lines, fmt.Sprintf("Max subagent depth: %d", *a.MaxSubagentDepth))
	}
	if a.Source == SourceBuiltin {
		lines = append(lines, "Disabled: "+tf(a.Disabled != nil && *a.Disabled))
	}
	if a.Override != nil {
		lines = append(lines, fmt.Sprintf("Override: %s (%s)", a.Override.Scope, a.Override.Path))
	}
	if jsTrim(a.SystemPrompt) != "" {
		lines = append(lines, "", "System Prompt:", a.SystemPrompt)
	}
	return strings.Join(lines, "\n")
}

func adminSummary(a AgentConfig) string {
	model := a.Model
	if model == "" {
		model = "default / inherit"
	}
	thinking := "default / inherit"
	switch t := a.Thinking.(type) {
	case string:
		thinking = t
	case bool:
		if !t {
			thinking = "off"
		}
	}
	return fmt.Sprintf("Source: %s · Model: %s · Thinking: %s", a.Source, model, thinking)
}

// liveAvailableModels reloads local configuration and cached catalogs without network discovery on picker open; a failed
// refresh warns and keeps the loaded choices.
func (h adminHost) liveAvailableModels() []ModelInfo {
	if h.refresh != nil {
		aborted, provider, msg, err := h.refresh()
		switch {
		case err != nil:
			h.notify("Could not refresh the model registry; using the last loaded choices. "+err.Error(), "warning")
		case provider != "":
			h.notify(fmt.Sprintf("Could not refresh %s; using the currently loaded choices. %s", provider, msg), "warning")
		case aborted:
			h.notify("Model registry refresh timed out; using the currently loaded choices.", "warning")
		}
	}
	return h.available()
}

func (h adminHost) selectFromList(title, subtitle string, values []string) (string, bool) {
	flat := title
	if subtitle != "" {
		flat = title + "\nCurrent: " + subtitle
	}
	choice, ok := h.sel(flat, values)
	if !ok || choice == "" {
		return "", false
	}
	for _, v := range values {
		if v == choice {
			return v, true
		}
	}
	return "", false
}

type editableField string

func savesThroughSettings(a AgentConfig, field editableField) bool {
	if a.Source == SourceBuiltin || a.Source == SourcePackage {
		return true
	}
	if a.Override == nil {
		return false
	}
	for _, f := range a.Override.Fields {
		if f == string(field) {
			return true
		}
	}
	// A lower-scope override can flow into a higher-scope custom agent with the same name; its edits stay in its own frontmatter.
	if string(a.Source) != a.Override.Scope {
		return false
	}
	base := a.Override.Base
	switch field {
	case "model":
		return a.Model != base.Model
	case "thinking":
		return a.Thinking != base.Thinking
	}
	return a.SystemPrompt != base.SystemPrompt
}

func isReadOnlyExtraAgent(a AgentConfig) bool {
	configured := os.Getenv("PI_SUBAGENT_EXTRA_AGENT_DIRS")
	if configured == "" || a.Source != SourceUser {
		return false
	}
	file, _ := filepath.Abs(a.FilePath)
	for _, d := range filepath.SplitList(configured) {
		d = jsTrim(d)
		if d == "" {
			continue
		}
		root, _ := filepath.Abs(d)
		rel, err := filepath.Rel(root, file)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func readOnlyAgentMessage(a AgentConfig, field editableField) string {
	switch {
	case a.Source == SourceRuntime:
		return fmt.Sprintf("Cannot update '%s' %s because that agent is runtime-registered by an extension; edit its source definition instead.", a.Name, field)
	case a.Source == SourcePackage:
		return fmt.Sprintf("Cannot update '%s' %s because that field is owned by its read-only package definition.", a.Name, field)
	case isReadOnlyExtraAgent(a):
		return fmt.Sprintf("Cannot update '%s' because its definition in PI_SUBAGENT_EXTRA_AGENT_DIRS is read-only.", a.Name)
	}
	return ""
}

func (h adminHost) selectAgent(args string) (kind string, agent AgentConfig, agents []AgentConfig, requested string, matches []AgentConfig, err error) {
	agents, err = h.allVisibleAgents()
	if err != nil {
		return
	}
	fields := strings.Fields(args)
	if len(fields) > 0 {
		requested = fields[0]
	}
	if len(agents) == 0 {
		kind = "not-found"
		return
	}
	if requested != "" {
		for _, a := range agents {
			if adminMatches(a, requested) {
				matches = append(matches, a)
			}
		}
		switch {
		case len(matches) == 1:
			kind, agent = "selected", matches[0]
		case len(matches) > 1 && !h.hasUI:
			kind = "ambiguous"
		case len(matches) > 1:
			order, by := agentChoices(matches)
			if c, ok := h.selectFromList(fmt.Sprintf("Multiple subagents named '%s'", requested), "", order); ok {
				kind, agent = "selected", by[c]
			} else {
				kind = "cancelled"
			}
		default:
			kind = "not-found"
		}
		return
	}
	if !h.hasUI {
		kind = "not-found"
		return
	}
	order, by := agentChoices(agents)
	if c, ok := h.selectFromList("Select subagent", "", order); ok {
		kind, agent = "selected", by[c]
	} else {
		kind = "cancelled"
	}
	return
}

// chooseModel returns (choice, cancelled); an empty choice is "inherit".
func (h adminHost) chooseModel(a AgentConfig) (string, bool) {
	models := h.liveAvailableModels()
	current := a.Model
	if current == "" {
		current = inheritModelChoice
	}
	values := []string{inheritModelChoice}
	known := false
	for _, m := range models {
		known = known || m.FullID == a.Model
	}
	if a.Model != "" && !known {
		values = append(values, a.Model)
	}
	for _, m := range models {
		values = append(values, m.FullID)
	}
	choice, ok := h.selectFromList("Select model for "+a.Name, current, values)
	if !ok {
		return "", true
	}
	if choice == inheritModelChoice {
		return "", false
	}
	return choice, false
}

func (h adminHost) chooseThinking(a AgentConfig) (string, bool) {
	available := h.liveAvailableModels()
	effective := a.Model
	if effective == "" && h.modelID != "" {
		effective = h.modelProvider + "/" + h.modelID
	}
	info := findModelInfo(effective, available, h.modelProvider)
	levels := getSupportedThinkingLevels(info)
	current := inheritThinkingChoice
	switch t := a.Thinking.(type) {
	case string:
		current = t
	case bool:
		if !t {
			current = "off"
		}
	}
	values := append([]string{inheritThinkingChoice}, levels...)
	has := false
	for _, v := range values {
		has = has || v == current
	}
	if current != inheritThinkingChoice && !has {
		values = append([]string{values[0], current}, values[1:]...)
	}
	note := "Model: default / inherit"
	switch {
	case a.Model != "":
		note = "Model: " + a.Model
	case effective != "":
		note = "Session model: " + effective
	}
	choice, ok := h.selectFromList("Select thinking level for "+a.Name, note+" · "+current, values)
	if !ok {
		return "", true
	}
	if choice == inheritThinkingChoice {
		return "", false
	}
	return choice, false
}

// chooseOverrideScope returns "" when the user cancels.
func (h adminHost) chooseOverrideScope(a AgentConfig) (string, error) {
	all, err := discoverAgentsAll(h.cwd, "", adv.options())
	if err != nil {
		return "", err
	}
	if a.Override != nil && a.Override.Scope != "" {
		if a.Source == SourceProject && a.Override.Scope == "user" && all.ProjectSettingsPath != "" {
			return "project", nil
		}
		return a.Override.Scope, nil
	}
	if all.ProjectSettingsPath == "" || !h.hasUI {
		return "user", nil
	}
	choice, ok := h.sel("Save subagent override for "+a.Name, []string{"user", "project"})
	if ok && (choice == "user" || choice == "project") {
		return choice, nil
	}
	return "", nil
}

func baseFieldEquals(base OverrideBase, field editableField, value *string) bool {
	if value == nil {
		return true
	}
	switch field {
	case "model":
		return base.Model != "" && base.Model == *value
	case "thinking":
		return base.Thinking != nil && base.Thinking == any(*value)
	}
	return base.SystemPrompt == *value
}

// persistSettingsField writes (or clears) one override field of an agent in the settings of a scope.
func (h adminHost) persistSettingsField(a AgentConfig, scope string, field editableField, value *string) (path string, overridden bool, err error) {
	base := cloneOverrideBase(a)
	if a.Override != nil {
		base = a.Override.Base
	}
	shadows := false
	if scope == "project" && a.Override != nil {
		for _, s := range a.Override.FieldScopes[string(field)] {
			shadows = shadows || s == "user"
		}
	}
	if shadows && (value == nil || baseFieldEquals(base, field, value)) {
		fields := newObject()
		switch field {
		case "model":
			switch {
			case value != nil:
				fields.set("model", *value)
			case base.Model != "":
				fields.set("model", base.Model)
			default:
				fields.set("model", false)
			}
		case "thinking":
			switch {
			case value != nil:
				fields.set("thinking", *value)
			case base.Thinking == false:
				fields.set("thinking", "off")
			case base.Thinking != nil:
				fields.set("thinking", base.Thinking)
			default:
				fields.set("thinking", false)
			}
		default:
			if value != nil {
				fields.set("systemPrompt", *value)
			} else {
				fields.set("systemPrompt", base.SystemPrompt)
			}
		}
		path, err = mergeBuiltinAgentOverride(h.cwd, a.Name, scope, fields)
		return path, true, err
	}
	if value == nil || baseFieldEquals(base, field, value) {
		path, _, err = removeBuiltinAgentOverrideFields(h.cwd, a.Name, scope, []string{string(field)})
		return path, false, err
	}
	path, err = mergeBuiltinAgentOverride(h.cwd, a.Name, scope, obj(string(field), *value))
	return path, true, err
}

// saveAgentField saves a changed model or thinking level; "" message with ok false means cancelled.
func (h adminHost) saveAgentField(a AgentConfig, field editableField, value *string) (string, bool, error) {
	if savesThroughSettings(a, field) {
		scope, err := h.chooseOverrideScope(a)
		if err != nil || scope == "" {
			return "", false, err
		}
		path, overridden, err := h.persistSettingsField(a, scope, field, value)
		if err != nil {
			return "", false, err
		}
		if overridden {
			shown := "undefined" // the original interpolates the unset choice
			if value != nil {
				shown = *value
			}
			return fmt.Sprintf("Saved %s settings override for '%s' with %s '%s' in %s.", scope, a.Name, field, shown, path), true, nil
		}
		return fmt.Sprintf("Cleared %s settings override for '%s' in %s.", field, a.Name, path), true, nil
	}
	if msg := readOnlyAgentMessage(a, field); msg != "" {
		return msg, true, nil
	}
	updated, err := editableAgentConfig(a)
	if err != nil {
		return "", false, err
	}
	cfg := newObject()
	switch field {
	case "model":
		updated.Model = ""
		if value != nil {
			updated.Model = *value
			cfg.set("model", *value)
		} else {
			cfg.set("model", undef{})
		}
	case "thinking":
		updated.Thinking = nil
		if value != nil {
			updated.Thinking = *value
			cfg.set("thinking", *value)
		} else {
			cfg.set("thinking", undef{})
		}
	}
	if err := os.WriteFile(updated.FilePath, []byte(serializeAgent(updated, SerializeAgentOptions{PreserveFrontmatterFields: preservedAgentFrontmatterFields(a, cfg)})), 0o644); err != nil {
		return "", false, err
	}
	if value != nil {
		return fmt.Sprintf("Updated '%s' %s to '%s' in %s.", a.Name, field, *value, updated.FilePath), true, nil
	}
	return fmt.Sprintf("Cleared '%s' %s in %s.", a.Name, field, updated.FilePath), true, nil
}

func (h adminHost) saveSystemPrompt(a AgentConfig, systemPrompt string) (string, bool, error) {
	next := strings.TrimRightFunc(systemPrompt, isJSSpace)
	if savesThroughSettings(a, "systemPrompt") {
		scope, err := h.chooseOverrideScope(a)
		if err != nil || scope == "" {
			return "", false, err
		}
		path, overridden, err := h.persistSettingsField(a, scope, "systemPrompt", &next)
		if err != nil {
			return "", false, err
		}
		if overridden {
			return fmt.Sprintf("Saved %s settings override for '%s' system prompt in %s.", scope, a.Name, path), true, nil
		}
		return fmt.Sprintf("Cleared system prompt settings override for '%s' in %s.", a.Name, path), true, nil
	}
	if msg := readOnlyAgentMessage(a, "systemPrompt"); msg != "" {
		return msg, true, nil
	}
	updated, err := editableAgentConfig(a)
	if err != nil {
		return "", false, err
	}
	updated.SystemPrompt = next
	cfg := obj("systemPrompt", next)
	if err := os.WriteFile(updated.FilePath, []byte(serializeAgent(updated, SerializeAgentOptions{PreserveFrontmatterFields: preservedAgentFrontmatterFields(a, cfg)})), 0o644); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("Updated '%s' system prompt in %s.", a.Name, updated.FilePath), true, nil
}

func (h adminHost) editSystemPrompt(a AgentConfig) (string, bool, error) {
	throughSettings := savesThroughSettings(a, "systemPrompt")
	if !throughSettings {
		if msg := readOnlyAgentMessage(a, "systemPrompt"); msg != "" {
			return msg, true, nil
		}
	}
	edited, ok := h.edit(fmt.Sprintf("Edit '%s' system prompt", a.Name), a.SystemPrompt)
	if !ok {
		return "", false, nil
	}
	projectOwns := false
	if a.Source == SourceProject && a.Override != nil && a.Override.Scope == "user" {
		if all, err := discoverAgentsAll(h.cwd, "", adv.options()); err == nil && all.ProjectSettingsPath != "" {
			projectOwns = true
		}
	}
	if strings.TrimRightFunc(edited, isJSSpace) == strings.TrimRightFunc(a.SystemPrompt, isJSSpace) && !(throughSettings && projectOwns) {
		return fmt.Sprintf("System prompt for '%s' left unchanged.", a.Name), true, nil
	}
	return h.saveSystemPrompt(a, edited)
}

// openSubagentsAdmin runs `/subagents [agent [model|thinking|prompt|details]]`.
func (h adminHost) openSubagentsAdmin(args string) error {
	kind, agent, agents, requested, matches, err := h.selectAgent(args)
	if err != nil {
		return err
	}
	list := func() string {
		var l []string
		for _, a := range agents {
			l = append(l, fmt.Sprintf("- %s (%s)", a.Name, a.Source))
		}
		if len(l) == 0 {
			return "- (none)"
		}
		return strings.Join(l, "\n")
	}
	switch kind {
	case "cancelled":
		return nil
	case "ambiguous":
		var l []string
		for _, m := range matches {
			l = append(l, fmt.Sprintf("- %s: %s", m.Source, m.FilePath))
		}
		h.send(fmt.Sprintf("Subagent '%s' is ambiguous. Choose a scope in interactive mode:\n%s", requested, strings.Join(l, "\n")))
		return nil
	case "not-found":
		if requested != "" {
			h.send(fmt.Sprintf("Subagent '%s' not found.\n\nAvailable subagents:\n%s", requested, list()))
		} else {
			h.send("Available subagents:\n" + list())
		}
		return nil
	}
	if !h.hasUI {
		h.send(adminMetadata(agent))
		return nil
	}
	action := ""
	if f := strings.Fields(args); len(f) > 1 {
		switch strings.ToLower(f[1]) {
		case "model":
			action = "Change model"
		case "thinking":
			action = "Change thinking level"
		case "prompt", "system-prompt", "edit":
			action = "Edit system prompt"
		case "details", "info":
			action = "Show details"
		}
	}
	if action == "" {
		action, _ = h.sel(fmt.Sprintf("Administer %s\n%s", agent.Name, adminSummary(agent)), []string{"Change model", "Change thinking level", "Edit system prompt", "Show details", "Done"})
	}
	var message string
	var saved bool
	switch action {
	case "Change model", "Change thinking level":
		field, choose := editableField("model"), h.chooseModel
		if action == "Change thinking level" {
			field, choose = "thinking", h.chooseThinking
		}
		choice, cancelled := choose(agent)
		if cancelled {
			return nil
		}
		var value *string
		if choice != "" {
			value = &choice
		}
		message, saved, err = h.saveAgentField(agent, field, value)
	case "Edit system prompt":
		message, saved, err = h.editSystemPrompt(agent)
	case "Show details":
		h.send(adminMetadata(agent))
		return nil
	default:
		return nil
	}
	if err != nil {
		h.notify(err.Error(), "error")
		h.send(fmt.Sprintf("Failed to update '%s': %s", agent.Name, err.Error()))
		return nil
	}
	if saved {
		h.notify(message, "info")
		h.send(message)
	}
	return nil
}

// newAdminHost binds the admin command to a session.
func newAdminHost(ctx sdk.Context) adminHost {
	h := adminHost{cwd: ctx.Cwd(), hasUI: ctx.HasUI(), modelProvider: ctx.ModelProvider(), modelID: ctx.Model(),
		available: func() []ModelInfo { return availableModels(ctx) },
		notify:    ctx.Notify,
		send:      func(content string) { _ = ctx.SendMessage(adminMessageType, content, true, sdk.SendMessageOptions{}) },
		sel: func(title string, items []string) (string, bool) {
			s, ok, err := ctx.Select(title, items)
			return s, ok && err == nil
		},
		edit: func(title, prefill string) (string, bool) {
			s, ok, err := ctx.Editor(title, prefill)
			return s, ok && err == nil
		},
	}
	h.refresh = func() (bool, string, string, error) {
		offline := false
		res, err := ctx.ModelRegistry().Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &offline})
		if err != nil {
			return false, "", "", err
		}
		providers := make([]string, 0, len(res.Errors))
		for p := range res.Errors {
			providers = append(providers, p)
		}
		sort.Strings(providers)
		if len(providers) > 0 {
			return res.Aborted, providers[0], res.Errors[providers[0]], nil
		}
		return res.Aborted, "", "", nil
	}
	return h
}
