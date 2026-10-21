package tintinweb_subagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsnumber"
)

// `/agents → Settings`: one row per key of subagents.json (24 keys, 23 rows: the join mode row is the defaultJoinMode
// key), changed in place with Enter or Space, each change written to the project file whole and announced. Numeric
// rows take a typed value instead. A key whose feature this port does not have is held, shown and written like the
// others, and the change also says that the port does not support it yet (the refusal of refuse.go), because the
// file is read back at the next session start and would refuse it then. upstream: src/index.ts showSettings,
// snapshotSettings, applyValue, notifyApplied, persistSettings; src/settings.ts saveAndEmitChanged.

// numericSettingIDs are the rows that open a typed prompt. upstream: NUMERIC_IDS.
var numericSettingIDs = map[string]bool{"maxConcurrent": true, "maxConcurrentForeground": true, "defaultMaxTurns": true, "graceTurns": true, "maxSubagentDepth": true}

func (h *heldSettings) get() heldSettings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return heldSettings{maxConcurrentForeground: h.maxConcurrentForeground, defaultJoinMode: h.defaultJoinMode, backgroundByDefault: h.backgroundByDefault,
		scopeModels: h.scopeModels, strictAgentFiles: h.strictAgentFiles, toolDescriptionMode: h.toolDescriptionMode, agentMentions: h.agentMentions,
		rememberAgents: h.rememberAgents, outputTranscript: h.outputTranscript, worktreeIsolation: h.worktreeIsolation,
		maxSubagentDepth: h.maxSubagentDepth, reportUsage: h.reportUsage}
}

func (h *heldSettings) update(fn func(h *heldSettings)) {
	h.mu.Lock()
	fn(h)
	h.mu.Unlock()
}

// setMaxConcurrent changes the background limit and starts the queued agents the new limit allows. upstream:
// agent-manager.ts setMaxConcurrent.
func (m *manager) setMaxConcurrent(n int) {
	m.mu.Lock()
	m.maxConcurrent = max(1, n)
	var start []*agentRecord
	for len(m.queue) > 0 && m.running < m.maxConcurrent {
		next := m.queue[0]
		m.queue = m.queue[1:]
		m.running++
		next.Status = statusRunning
		start = append(start, next)
	}
	m.mu.Unlock()
	for _, r := range start {
		safeGo("agent run", func() { m.run(r) })
	}
}

func (m *manager) getMaxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxConcurrent
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (a *app) graceTurnsValue() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.graceTurns > 0 {
		return a.graceTurns
	}
	return defaultGraceTurns
}

func (a *app) defaultMaxTurnsValue() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.defaultMaxTurns == nil {
		return 0
	}
	return *a.defaultMaxTurns
}

// snapshotSettings is every setting as it is now. Every settings mutation writes this WHOLE object back to disk, so a
// key missing here would be erased from the user's subagents.json the next time they toggled something unrelated.
func (a *app) snapshotSettings() subagentsSettings {
	h := a.held.get()
	ui := a.ui.get()
	maxConcurrent, maxTurns, grace := a.mgr.getMaxConcurrent(), a.defaultMaxTurnsValue(), a.graceTurnsValue()
	scheduling := a.schedulingOn()
	defaultsOff := isDefaultsDisabled()
	widgetMode, mentions, join, desc, markdown := string(ui.widgetMode), h.agentMentions, h.defaultJoinMode, h.toolDescriptionMode, string(ui.viewerMarkdown)
	s := subagentsSettings{
		MaxConcurrent: &maxConcurrent, MaxConcurrentForeground: &h.maxConcurrentForeground, DefaultMaxTurns: &maxTurns, GraceTurns: &grace,
		DefaultJoinMode: &join, BackgroundByDefault: &h.backgroundByDefault, SchedulingEnabled: &scheduling, ScopeModels: &h.scopeModels,
		StrictAgentFiles: &h.strictAgentFiles, DisableDefaultAgents: &defaultsOff, ToolDescriptionMode: &desc, FleetView: &ui.fleetView,
		AgentMentions: &mentions, RememberAgents: &h.rememberAgents, WidgetMode: &widgetMode, OutputTranscript: &h.outputTranscript,
		WorktreeIsolation: &h.worktreeIsolation, MaxSubagentDepth: &h.maxSubagentDepth, ReportUsage: &h.reportUsage,
		ShowCost: &ui.showCost, ShowModel: &ui.showModel, ViewerMarkdown: &markdown,
		// Deliberately not defaulted: every settings change writes the whole snapshot, and materializing the implicit
		// default would turn it into explicit configuration, which then fails loudly if general-purpose goes away.
		FallbackSubagent: getFallbackSubagent(),
	}
	// The user's answer, not the effective one: a stand-down for another extension's workflow tool is scoped to the
	// session it was detected in, and writing it would freeze it into the file as an explicit false.
	if value, write := a.workflows().settingValue(); write {
		s.WorkflowsEnabled = &value
	}
	return s
}

// saveAndEmitChanged persists the snapshot, emits `subagents:settings_changed` and returns the toast. The event fires
// whatever the write's outcome, so listeners see the in-memory change. upstream: settings.ts saveAndEmitChanged.
func (a *app) saveAndEmitChanged(snapshot subagentsSettings, successMsg string) (message, level string) {
	a.mu.Lock()
	cwd := a.cwd
	a.mu.Unlock()
	persisted := cwd != "" && settingsWriter(snapshot, cwd)
	if ctx, ok := a.context(); ok {
		guard(func() {
			_ = ctx.Events().Emit("subagents:settings_changed", map[string]any{"settings": settingsAsMap(snapshot), "persisted": persisted})
		})
	}
	return persistToastFor(successMsg, persisted)
}

// settingsAsMap is the snapshot as the event carries it: the keys that are set, with their values.
func settingsAsMap(s subagentsSettings) map[string]any {
	out := map[string]any{}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(s) != nil {
		return out
	}
	_ = json.Unmarshal(buf.Bytes(), &out)
	return out
}

// persistSettings persists and broadcasts the settings, silent on success: for a change whose feedback is the UI it
// just changed (the viewer's `m` key, where a notification per press would talk over the overlay it is describing). A
// failed write still speaks, so a preference does not look persisted when the next session will not have it.
func (a *app) persistSettings(notify func(message, level string), changeMsg string) {
	message, level := a.saveAndEmitChanged(a.snapshotSettings(), changeMsg)
	if level == "warning" && notify != nil {
		notify(message, level)
	}
}

// notifyApplied persists the snapshot, emits the event and surfaces the right toast: info on success, a warning when
// the value is session only.
func (a *app) notifyApplied(ui menuUI, successMsg string) {
	message, level := a.saveAndEmitChanged(a.snapshotSettings(), successMsg)
	ui.Notify(message, level)
}

// settingsItems are the rows of the menu, with the values as they are now. upstream: buildItems.
func (a *app) settingsItems() []*settingItem {
	h := a.held.get()
	ui := a.ui.get()
	// Label what unset actually does: it targets general-purpose even when that is unregistered (the permissive
	// hardcoded tier). `values` still offers only resolvable targets, so the user cannot persist a fallback that would
	// hard-error on every dispatch.
	fallbackValue := "general-purpose"
	if f := getFallbackSubagent(); f != nil {
		fallbackValue = *f
	}
	fallbackValues := append([]string{}, getAvailableTypes()...)
	if !inList(fallbackValues, noFallback) {
		fallbackValues = append(fallbackValues, noFallback)
	}
	mc, mcf, dmt, gt := a.mgr.getMaxConcurrent(), h.maxConcurrentForeground, a.defaultMaxTurnsValue(), a.graceTurnsValue()
	workflowsOn := a.workflows().isEnabled()
	num := func(id, label, description string, v int) *settingItem {
		return &settingItem{ID: id, Label: label, Description: description, CurrentValue: strconv.Itoa(v), Values: []string{strconv.Itoa(v)}}
	}
	toggle := func(id, label, description string, v bool) *settingItem {
		return &settingItem{ID: id, Label: label, Description: description, CurrentValue: onOff(v), Values: []string{"on", "off"}}
	}
	choice := func(id, label, description, v string, values []string) *settingItem {
		return &settingItem{ID: id, Label: label, Description: description, CurrentValue: v, Values: values}
	}
	return []*settingItem{
		num("maxConcurrent", "Max concurrency", "Max concurrent background agents (Enter to type)", mc),
		num("maxConcurrentForeground", "Max foreground concurrency", "Max concurrent foreground (blocking) agents (0 = unlimited, Enter to type)", mcf),
		num("defaultMaxTurns", "Default max turns", "Default max turns before wrap-up (0 = unlimited, Enter to type)", dmt),
		num("graceTurns", "Grace turns", "Grace turns after wrap-up steer (Enter to type)", gt),
		num("maxSubagentDepth", "Nested depth", "Hard cap on nested delegation — main is 0, its subagents 1 (0/1 = nesting off, Enter to type)", h.maxSubagentDepth),
		choice("joinMode", "Join mode", "Default join mode for background agents", h.defaultJoinMode, []string{"smart", "async", "group"}),
		toggle("backgroundByDefault", "Background by default", "An Agent call that doesn't say runs detached (off = blocks the turn and returns inline)", h.backgroundByDefault),
		toggle("schedulingEnabled", "Scheduling", "Schedule subagent feature (off removes `schedule` param from Agent tool spec on next pi session)", a.schedulingOn()),
		toggle("workflowsEnabled", "Workflows", "Scripted workflows, on unless another extension provides a workflow tool "+
			"(off keeps the SubagentWorkflow tool out of the tool spec; applies on next pi session)", workflowsOn),
		toggle("scopeModels", "Scope models", "Validate subagent models against scoped models (/scoped-models)", h.scopeModels),
		toggle("strictAgentFiles", "Strict agent files", "Fail startup on an unreadable/unparseable agent .md instead of skipping it with a warning", h.strictAgentFiles),
		toggle("disableDefaultAgents", "Disable defaults", "Hide built-in agents (general-purpose, Explore, Plan) — custom agents are unaffected", isDefaultsDisabled()),
		choice("fallbackSubagent", "Fallback agent", fmt.Sprintf("Agent used when subagent_type is unknown, disabled, or ambiguous; %q rejects the call instead (strict dispatch)", noFallback), fallbackValue, fallbackValues),
		toggle("outputTranscript", "Output transcript", "Write each subagent's .output transcript by default. A custom agent's output_transcript frontmatter overrides this.", h.outputTranscript),
		toggle("worktreeIsolation", "Worktree isolation", "Allow isolation: worktree to copy the repo. Off refuses worktrees on every path immediately — for repos where a copy costs too much time or disk — and drops the `isolation` param from the Agent tool spec on next pi session.", h.worktreeIsolation),
		toggle("reportUsage", "Report usage to session", "Add subagent tokens and cost to this session's own totals, so pi's footer and /cost stop reading a delegating session as nearly free. Reported on the next tool result (agents that finish in the background are counted on the one after). Context-window % is unaffected.", h.reportUsage),
		toggle("showCost", "Show cost", "Show an estimated `~$0.0042` beside subagent token counts in the widget, fleet view, results and notifications. Priced by pi from the model's rates — omitted entirely for a model it has no rates for.", ui.showCost),
		toggle("showModel", "Show model", "Name the model driving each agent, and the thinking level it is running at, on the widget's running rows. The Agent tool result and the conversation viewer show the pair either way — this adds it to the widget, where the row is already dense.", ui.showModel),
		choice("viewerMarkdown", "Viewer markdown", "How much of the conversation viewer renders as Markdown. assistant = assistant text only (default); all = tool results too, for tools that emit Markdown — accepting that a Markdown pass over a diff or a log eats `#` comments, swallows a `---` line and re-fences indented output; off = everything verbatim. `m` in the viewer cycles the same setting (footer: raw / md / md+).", string(ui.viewerMarkdown), []string{"off", "assistant", "all"}),
		toggle("fleetView", "Fleet view", "Claude Code-style main+subagents list below the editor (↓/← to navigate, Enter to view)", ui.fleetView),
		choice("agentMentions", "Agent mentions", "Route `@handle message` at the prompt to that agent. model = an off-screen clone of this conversation calls the Agent tool, so the agent gets a context-written prompt, a transcript and per-tool detail, and the chat stays clean; direct = started here from your text, no model call. Messaging and resuming are direct either way.", h.agentMentions, []string{"model", "direct", "off"}),
		toggle("rememberAgents", "Remember agents", "Persist subagent sessions so `@handle` can resume one long after it finished (they also appear in /resume)", h.rememberAgents),
		choice("widgetMode", "Widget", "Above-editor agent widget: all = every agent; background = hide foreground (they already render inline); off = hide the widget.", string(ui.widgetMode), []string{"all", "background", "off"}),
		choice("toolDescriptionMode", "Tool description", "Agent tool description sent to the LLM: full (rich, default), compact (~75% fewer tokens, for small/local models), or custom (.pi/agent-tool-description.md with {{placeholders}})", h.toolDescriptionMode, []string{"full", "compact", "custom"}),
	}
}

// unportedSettingNotice is the refusal for a change that asks for a feature this port does not have: the same text
// the next session start would announce for the file this change writes. "" when the value is one the port matches.
func unportedSettingNotice(key string, value any) string {
	for _, r := range settingRules {
		if r.key == key && r.unported(value) {
			return refusalText("this subagents.json setting (" + key + ": " + compactJSON(value) + ")")
		}
	}
	return ""
}

// applySetting applies one change: the value goes live where this port has the feature, is held where it does not,
// and the change is persisted and announced. The wording is the original's. upstream: index.ts applyValue.
func (a *app) applySetting(ui menuUI, id, value string) {
	on := value == "on"
	// refuse names the unported feature behind the change, after the original's own toast.
	refuse := func(key string, v any) {
		if text := unportedSettingNotice(key, v); text != "" {
			ui.Notify(text, "warning")
		}
	}
	switch id {
	case "maxConcurrent":
		if n, err := strconv.Atoi(value); err == nil && n >= 1 {
			a.mgr.setMaxConcurrent(n)
			a.notifyApplied(ui, fmt.Sprintf("Max concurrency set to %d", n))
		}
	case "maxConcurrentForeground":
		// 0 is meaningful here, unlike maxConcurrent: it means unlimited.
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			a.held.update(func(h *heldSettings) { h.maxConcurrentForeground = n })
			if n == 0 {
				a.notifyApplied(ui, "Max foreground concurrency set to unlimited")
			} else {
				a.notifyApplied(ui, fmt.Sprintf("Max foreground concurrency set to %d", n))
			}
			refuse(id, n)
		}
	case "defaultMaxTurns":
		n, err := strconv.Atoi(value)
		switch {
		case err != nil:
		case n == 0:
			a.mu.Lock()
			a.defaultMaxTurns = nil
			a.mu.Unlock()
			a.notifyApplied(ui, "Default max turns set to unlimited")
		case n >= 1:
			a.mu.Lock()
			a.defaultMaxTurns = &n
			a.mu.Unlock()
			a.notifyApplied(ui, fmt.Sprintf("Default max turns set to %d", n))
		}
	case "graceTurns":
		if n, err := strconv.Atoi(value); err == nil && n >= 1 {
			a.mu.Lock()
			a.graceTurns = n
			a.mu.Unlock()
			a.notifyApplied(ui, fmt.Sprintf("Grace turns set to %d", n))
		}
	case "maxSubagentDepth":
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			a.held.update(func(h *heldSettings) { h.maxSubagentDepth = n })
			if n <= 1 {
				a.notifyApplied(ui, "Nested delegation disabled")
			} else {
				a.notifyApplied(ui, fmt.Sprintf("Nested depth set to %d. Applies to agents started from now on.", n))
			}
			refuse(id, n)
		}
	case "joinMode":
		a.held.update(func(h *heldSettings) { h.defaultJoinMode = value })
		a.notifyApplied(ui, "Default join mode set to "+value)
		refuse("defaultJoinMode", value)
	case "backgroundByDefault":
		a.held.update(func(h *heldSettings) { h.backgroundByDefault = on })
		if on {
			a.notifyApplied(ui, "Agent calls run in the background unless they pass run_in_background: false")
		} else {
			a.notifyApplied(ui, "Agent calls block and return inline unless they pass run_in_background: true")
		}
		refuse(id, on)
	case "schedulingEnabled":
		if on == a.schedulingOn() {
			ui.Notify("Scheduling already "+map[bool]string{true: "enabled", false: "disabled"}[on]+".", "info")
			return
		}
		a.setSchedulingEnabled(on)
		if !on {
			a.scheduler().stop() // immediate kill: outstanding fires stop ticking
		}
		a.notifyApplied(ui, "Scheduling "+map[bool]string{true: "enabled", false: "disabled"}[on]+". Tool spec change takes effect on next pi session.")
	case "workflowsEnabled":
		w := a.workflows()
		if on == w.isEnabled() {
			ui.Notify("Workflows already "+map[bool]string{true: "enabled", false: "disabled"}[on]+".", "info")
			return
		}
		w.setEnabled(on)
		// Runs already in flight keep going: the switch governs whether the tool is offered, and killing live agents on
		// a settings toggle would lose work the user never asked to discard.
		a.notifyApplied(ui, "Workflows "+map[bool]string{true: "enabled", false: "disabled"}[on]+". Tool spec change takes effect on next pi session.")
	case "scopeModels":
		a.held.update(func(h *heldSettings) { h.scopeModels = on })
		a.notifyApplied(ui, "Scope models "+map[bool]string{true: "enabled", false: "disabled"}[on])
		refuse(id, on)
	case "strictAgentFiles":
		a.held.update(func(h *heldSettings) { h.strictAgentFiles = on })
		a.notifyApplied(ui, "Strict agent files "+map[bool]string{true: "enabled", false: "disabled"}[on]+". Takes effect on next pi session.")
		refuse(id, on)
	case "disableDefaultAgents":
		setDefaultsDisabled(on)
		a.reloadCustomAgents(a.cwdValue()) // re-register with the new setting
		a.notifyApplied(ui, "Default agents "+map[bool]string{true: "disabled", false: "enabled"}[on]+". Tool spec change takes effect on next pi session.")
	case "fallbackSubagent":
		v := value
		setFallbackSubagent(&v)
		if value == noFallback {
			a.notifyApplied(ui, "Unknown or disabled agent types will now be rejected")
		} else {
			a.notifyApplied(ui, "Unknown agent types will fall back to "+value)
		}
	case "outputTranscript":
		a.held.update(func(h *heldSettings) { h.outputTranscript = on })
		a.notifyApplied(ui, "Output transcript "+map[bool]string{true: "enabled", false: "disabled"}[on]+" by default")
		refuse(id, on)
	case "worktreeIsolation":
		a.held.update(func(h *heldSettings) { h.worktreeIsolation = on })
		// The refusal is live, but the tool schema is built at registration, so the isolation parameter only
		// appears/disappears next session.
		a.notifyApplied(ui, "Worktree isolation "+map[bool]string{true: "enabled", false: "disabled"}[on]+". Tool parameter updates on next pi session.")
		refuse(id, on)
	case "toolDescriptionMode":
		a.held.update(func(h *heldSettings) { h.toolDescriptionMode = value })
		a.notifyApplied(ui, "Tool description set to "+value+". Takes effect on next pi session.")
		refuse(id, value)
	case "reportUsage":
		a.held.update(func(h *heldSettings) { h.reportUsage = on })
		if on {
			a.notifyApplied(ui, "Subagent usage now counted in this session's totals")
		} else {
			a.notifyApplied(ui, "Subagent usage no longer counted in this session's totals")
		}
		refuse(id, on)
	case "showCost":
		a.ui.setDisplay(func(s *uiSettings) { s.showCost = on }, true)
		a.notifyApplied(ui, "Cost display "+map[bool]string{true: "enabled", false: "disabled"}[on])
	case "showModel":
		a.ui.setDisplay(func(s *uiSettings) { s.showModel = on }, false)
		a.notifyApplied(ui, "Model display "+map[bool]string{true: "enabled", false: "disabled"}[on])
	case "viewerMarkdown":
		a.ui.setDisplay(func(s *uiSettings) { s.viewerMarkdown = viewerMarkdownMode(value) }, false)
		a.notifyApplied(ui, "Viewer markdown set to "+value)
	case "fleetView":
		a.ui.setDisplay(func(s *uiSettings) { s.fleetView = on }, false)
		a.notifyApplied(ui, "Fleet view "+map[bool]string{true: "enabled", false: "disabled"}[on])
	case "agentMentions":
		a.held.update(func(h *heldSettings) { h.agentMentions = value })
		switch value {
		case "off":
			a.notifyApplied(ui, "Agent mentions disabled")
		case "model":
			a.notifyApplied(ui, "Agent mentions on — a conversation clone starts a mentioned agent off-screen")
		default:
			a.notifyApplied(ui, "Agent mentions on — a mentioned agent starts here, with no model call")
		}
		refuse(id, value)
	case "rememberAgents":
		a.held.update(func(h *heldSettings) { h.rememberAgents = on })
		a.notifyApplied(ui, "Remember agents "+map[bool]string{true: "enabled", false: "disabled"}[on])
		refuse(id, on)
	case "widgetMode":
		a.ui.setDisplay(func(s *uiSettings) { s.widgetMode = widgetMode(value) }, false)
		a.notifyApplied(ui, "Widget set to "+value)
	}
}

func (a *app) cwdValue() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cwd
}

// showSettings is the settings menu. Enter on a numeric row closes it and prompts for a typed value, then it opens again.
func (m *agentsMenu) showSettings() {
	for {
		result := m.settingsPanel()
		if !numericSettingIDs[result] {
			return
		}
		m.numericPrompt(result)
		if m.numericDone {
			m.numericDone = false
			continue
		}
		return
	}
}

// settingsPanel runs the list until it is closed and returns the numeric row Enter was pressed on ("" for Esc).
func (m *agentsMenu) settingsPanel() string {
	value, _ := m.ui.Custom(func(_ viewerHost, th uiTheme, done func(any)) viewerComponent {
		items := m.a.settingsItems()
		list := newSettingsList(items, len(items)+2, settingsListThemeFor(th),
			func(id, newValue string) { m.a.applySetting(m.ui, id, newValue) },
			func() { done(nil) })
		// The list does not expose its selection, so it is tracked here from the arrow keys (clamped, where the list
		// wraps): Enter on a numeric row uses this index.
		currentIndex := 0
		return &menuList{title: "⚙  Subagent Settings", list: list, handleKey: func(data string) bool {
			if tui.MatchesKey(data, "up") {
				currentIndex = max(0, currentIndex-1)
			} else if tui.MatchesKey(data, "down") {
				currentIndex = min(len(items)-1, currentIndex+1)
			}
			// Enter on a numeric field closes the list and prompts for typed input.
			if tui.MatchesKey(data, "enter") && numericSettingIDs[items[currentIndex].ID] {
				done(items[currentIndex].ID)
				return true
			}
			return false
		}}
	}, nil)
	id, _ := value.(string)
	return id
}

// numericPrompt asks for a typed integer for a numeric row until the user enters a valid one or cancels (Esc). Whitespace
// is trimmed; a non-numeric answer re-prompts with the user's last entry so they can edit it.
func (m *agentsMenu) numericPrompt(id string) {
	h := m.a.held.get()
	var current, label string
	switch id {
	case "maxConcurrent":
		current, label = strconv.Itoa(m.a.mgr.getMaxConcurrent()), "Max concurrency (1+)"
	case "maxConcurrentForeground":
		current, label = strconv.Itoa(h.maxConcurrentForeground), "Max foreground concurrency (0 = unlimited)"
	case "defaultMaxTurns":
		current, label = strconv.Itoa(m.a.defaultMaxTurnsValue()), "Default max turns (0 = unlimited)"
	case "maxSubagentDepth":
		current, label = strconv.Itoa(h.maxSubagentDepth), "Nested depth (0/1 = nesting off)"
	default:
		current, label = strconv.Itoa(m.a.graceTurnsValue()), "Grace turns (1+)"
	}
	input, ok, err := m.ui.Input(label, current)
	for err == nil && ok {
		trimmed := jsTrim(input)
		if n, ok := jsInteger(trimmed); ok {
			m.a.applySetting(m.ui, id, strconv.FormatInt(n, 10))
			m.numericDone = true
			return
		}
		// Invalid: re-prompt with the user's last entry so they can edit it.
		input, ok, err = m.ui.Input(label, trimmed)
	}
}

// jsInteger is `trimmed !== "" && Number.isInteger(Number(trimmed))` for the strings a person types.
func jsInteger(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	f := jsnumber.Parse(s)
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 9e15 {
		return 0, false
	}
	return int64(f), true
}
