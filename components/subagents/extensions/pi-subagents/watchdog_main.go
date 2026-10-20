package pi_subagents

import (
	"regexp"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The main-session watchdog registration (watchdog/register-main.ts): the `/subagents-watchdog` command, the message and entry
// renderers of its warnings, and the session events that feed the runtime.

func wdBoolLabel(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func wdSourceLine(s wdSettingsSource) string {
	location := ""
	if s.Path != "" {
		location = " " + s.Path
	}
	state := "not found"
	if s.Exists {
		state = "found"
	}
	return "- " + s.Scope + location + ": " + state
}

func wdCurrentSessionModelLine(ctx wdContext) string {
	if m := ctx.CurrentModel(); m != nil {
		if p, id := wdModelProvider(m), wdModelID(m); p != "" && id != "" {
			return "current session (" + p + "/" + id + ")"
		}
	}
	return "current session (not configured)"
}

func wdMainThinkingLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	main := snapshot.Config.Main
	if main.Model != "" {
		var cfg any
		if main.Thinking.Off {
			cfg = false
		} else if main.Thinking.Level != "" {
			cfg = main.Thinking.Level
		}
		if eff := resolveEffectiveThinking(main.Model, cfg); eff != "" {
			return eff
		}
		return "off (default for explicit watchdog model)"
	}
	if main.Thinking.Off {
		return "off"
	}
	if main.Thinking.Level != "" {
		return main.Thinking.Level
	}
	if level := ctx.ThinkingLevel(); level != "" {
		return "current session (" + level + ")"
	}
	return "current session"
}

func wdMainModelLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	if model := snapshot.Config.Main.Model; model != "" {
		source := "configured"
		if snapshot.SessionModelOverride != nil && snapshot.SessionModelOverride.Model != "" {
			source = "session override"
		}
		base, _ := splitKnownThinkingSuffix(model)
		return "Main model: " + base + " (" + source + ")"
	}
	return "Main model: " + wdCurrentSessionModelLine(ctx)
}

func wdThinkingText(t wdThinking, unset string) string {
	switch {
	case t.Off:
		return "off"
	case t.Level != "":
		return t.Level
	}
	return unset
}

func wdChildrenLine(snapshot watchdogRuntimeSnapshot) string {
	c := snapshot.Config.Children
	model := "current child session"
	if c.Model != "" {
		model, _ = splitKnownThinkingSuffix(c.Model)
	}
	overrideText := ""
	if len(c.Overrides) > 0 {
		var parts []string
		for _, e := range c.Overrides {
			bits := []string{e.Agent}
			if e.Override.Enabled != nil {
				bits = append(bits, wdBoolLabel(*e.Override.Enabled))
			}
			if e.Override.Model != "" {
				base, _ := splitKnownThinkingSuffix(e.Override.Model)
				bits = append(bits, base)
			}
			if e.Override.Thinking.set() {
				bits = append(bits, "thinking "+wdThinkingText(e.Override.Thinking, ""))
			}
			parts = append(parts, strings.Join(bits, " "))
		}
		overrideText = " · overrides " + strings.Join(parts, "; ")
	}
	return "Children: " + wdBoolLabel(snapshot.Config.Enabled && c.Enabled) + " · model " + model + " · thinking " + wdThinkingText(c.Thinking, "current child session") + overrideText
}

func wdRecommendationLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	rec, err := recommendWatchdogModel(ctx, snapshot.Config.Main)
	if err != nil {
		return "Watchdog recommendation unavailable (" + err.Error() + ")"
	}
	return formatWatchdogRecommendation(rec)
}

func wdLspLine(snapshot watchdogRuntimeSnapshot) string {
	lsp := snapshot.Lsp
	provider, counts, message := "", "", ""
	if lsp.Provider != "" {
		provider = " · " + lsp.Provider
	}
	if lsp.DiagnosticCount > 0 || lsp.FreshDiagnosticCount > 0 {
		counts = " · " + itoa(lsp.FreshDiagnosticCount) + " new/" + itoa(lsp.DiagnosticCount) + " total"
	}
	if lsp.Message != "" {
		message = " · " + lsp.Message
	}
	return "LSP diagnostics: " + wdBoolLabel(lsp.Enabled) + " · " + lsp.Status + provider + counts + message
}

func buildWatchdogStatus(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	c := snapshot.Config
	main := wdBoolLabel(snapshot.Enabled)
	if !c.Enabled && snapshot.SessionOverride == nil {
		main += " (default off)"
	}
	runtime := strings.ReplaceAll(snapshot.Status, "-", " ")
	if snapshot.BufferedDeltas > 0 {
		runtime += " · buffered deltas " + itoa(snapshot.BufferedDeltas)
	}
	trigger := "every non-empty turn delta"
	if snapshot.ReviewTrigger == "repo-edits" {
		trigger = "repo edits only"
		if c.Clarification {
			trigger = "repo edits + bounded main orchestration activity"
		}
	}
	cadence := "boundary only"
	if c.Cadence.EveryNTools != nil {
		cadence = "every " + itoa(*c.Cadence.EveryNTools) + " tools + boundary"
	}
	sessionOverride := "none"
	if snapshot.SessionOverride != nil {
		sessionOverride = wdBoolLabel(*snapshot.SessionOverride)
	}
	stalemate := itoa(snapshot.BoundaryRepeats) + "/" + itoa(c.StalemateRepeats)
	if snapshot.Stalemate {
		stalemate += " · stopped"
	}
	rules := "none"
	if c.Rules != nil {
		rules = itoa(len(c.Rules.RoleModels)) + " role models · " + c.Rules.Action
	}
	lines := []string{
		"Subagent watchdog",
		"Main: " + main,
		"Runtime: " + runtime,
		"Review trigger: " + trigger,
		"Main clarification: " + wdBoolLabel(c.Clarification),
		"Scope context: " + wdBoolLabel(c.ScopeEnabled),
		"Cadence: " + cadence,
		wdLspLine(snapshot),
		"Session override: " + sessionOverride,
		wdMainModelLine(snapshot, ctx),
		"Main thinking: " + wdMainThinkingLine(snapshot, ctx),
		wdChildrenLine(snapshot),
		wdRecommendationLine(snapshot, ctx),
		"Agent-end timeout: " + itoa(c.AgentEndTimeoutMs) + "ms",
		"Stalemate: " + stalemate,
		"Rules: " + rules,
		"Review model call: " + snapshot.ReviewDescription,
	}
	if snapshot.FailedReviews > 0 {
		lines = append(lines, "Failed reviews: "+itoa(snapshot.FailedReviews))
	}
	if snapshot.StaleReviews > 0 {
		lines = append(lines, "Stale reviews: "+itoa(snapshot.StaleReviews))
	}
	if len(snapshot.ChangedPaths) > 0 {
		shown := snapshot.ChangedPaths
		if len(shown) > 8 {
			shown = shown[:8]
		}
		more := ""
		if len(snapshot.ChangedPaths) > 8 {
			more = ", +" + itoa(len(snapshot.ChangedPaths)-8) + " more"
		}
		lines = append(lines, "Changed paths: "+strings.Join(shown, ", ")+more)
	}
	if w := snapshot.LastWarning; w != nil {
		state := w.State
		if state == "" {
			state = "candidate"
		}
		lines = append(lines, "Last warning: "+w.Severity+" · "+state+" · "+w.Summary)
	}
	if snapshot.LastError != "" {
		lines = append(lines, "Last error: "+snapshot.LastError)
	}
	if !snapshot.ConfigOK {
		lines = append(lines, "", "Config errors:")
		for _, e := range snapshot.Errors {
			lines = append(lines, "- "+e.Message)
		}
		lines = append(lines, "Watchdog is disabled until the config is fixed.")
	} else {
		lines = append(lines, "", "Config: ok")
	}
	lines = append(lines, "Sources:")
	for _, s := range snapshot.Sources {
		lines = append(lines, wdSourceLine(s))
	}
	lines = append(lines, "", "Model commands:",
		"- /subagents-watchdog recommend-model",
		"- /subagents-watchdog model recommended (saves to user settings, not project settings)",
		"- /subagents-watchdog model <provider/model[:thinking]>",
		"- /subagents-watchdog model inherit",
		"- /subagents-watchdog session model recommended",
		`Agent action: subagent({ action: "watchdog.configure", model: "recommended", scope: "session" })`)
	return strings.Join(lines, "\n")
}

var wdTestCommandRe = regexp.MustCompile(`^test\s+(concern|blocker)\s+(low|medium|high)\s+([\s\S]+)$`)

func wdFormatThinking(t wdThinking) string { return wdThinkingText(t, "inherit") }

type wdModelCommandValue struct {
	model       wdPatch[string]
	thinking    wdPatch[wdThinking]
	description string
}

func wdResolveModelCommandValue(rt *watchdogRuntime, ctx wdContext, raw string) (wdModelCommandValue, error) {
	value := jsTrim(raw)
	if value == "" {
		return wdModelCommandValue{}, wdErr("Expected a model, 'recommended', or 'inherit'.")
	}
	if value == "inherit" {
		return wdModelCommandValue{model: wdClear[string](), thinking: wdClear[wdThinking](), description: "current session model and thinking"}, nil
	}
	if value == "recommended" {
		rec, err := recommendWatchdogModel(ctx, rt.getSnapshot(ctx.Cwd()).Config.Main)
		if err != nil {
			return wdModelCommandValue{}, err
		}
		return wdModelCommandValue{model: wdSetTo(rec.Model), thinking: wdSetTo(wdThinking{Level: rec.Thinking}), description: rec.Model + ":" + rec.Thinking + " (" + rec.Label + ")"}, nil
	}
	resolved, err := resolveWatchdogModelInput(ctx, value)
	if err != nil {
		return wdModelCommandValue{}, err
	}
	out := wdModelCommandValue{model: wdSetTo(resolved.Model), thinking: wdClear[wdThinking](), description: resolved.Model}
	if resolved.Thinking != "" {
		out.thinking = wdSetTo(wdThinking{Level: resolved.Thinking})
		out.description += ":" + resolved.Thinking
	}
	return out, nil
}

func wdBuildCommandRecommendation(rt *watchdogRuntime, ctx wdContext) (string, error) {
	rec, err := recommendWatchdogModel(ctx, rt.getSnapshot(ctx.Cwd()).Config.Main)
	if err != nil {
		return "", err
	}
	line := "Recommended: " + rec.Model + ":" + rec.Thinking
	if rec.Source == "configured" {
		line = formatWatchdogRecommendation(rec)
	}
	return strings.Join([]string{
		"Subagent watchdog recommended model",
		"Current session: " + wdCurrentSessionModelLine(ctx),
		line,
		"Reason: " + rec.Reason,
		"",
		"Apply for this session:",
		"/subagents-watchdog session model recommended",
		"",
		"Save as your user default (affects other projects without overrides; does not change project settings):",
		"/subagents-watchdog model recommended",
	}, "\n"), nil
}

func wdBuildCommandCheck(rt *watchdogRuntime, ctx wdContext) (string, error) {
	snapshot := rt.getSnapshot(ctx.Cwd())
	if !snapshot.ConfigOK {
		lines := []string{"Subagent watchdog config check", "", "Config errors:"}
		for _, e := range snapshot.Errors {
			lines = append(lines, "- "+e.Message)
		}
		return strings.Join(lines, "\n"), nil
	}
	lines := []string{"Subagent watchdog config check", "", "Config: ok"}
	if snapshot.Config.Main.Model != "" {
		resolved, err := resolveWatchdogModelInput(ctx, snapshot.Config.Main.Model)
		if err != nil {
			return "", err
		}
		lines = append(lines, "Main model: "+resolved.Model+" auth ok")
	} else {
		lines = append(lines, "Main model: "+wdCurrentSessionModelLine(ctx))
	}
	lines = append(lines, "Main thinking: "+wdMainThinkingLine(snapshot, ctx), wdLspLine(snapshot), wdRecommendationLine(snapshot, ctx))
	return strings.Join(lines, "\n"), nil
}

func wdTestWarning(severity, importance, text string) wdWarning {
	action := "Verify the renderer and transcript delivery; decide manually whether any action is needed."
	if severity == "blocker" {
		action = "Verify the renderer and transcript delivery."
	}
	return wdWarning{Severity: severity, Category: "other", Importance: importance, Source: "main", State: "displayed", Summary: text,
		Evidence: "Manual /subagents-watchdog test " + severity + " message from the main session.", RecommendedAction: action}
}

// watchdogCommandHost is what the command writes through: a text message and a notification.
type watchdogCommandHost struct {
	text   func(string)
	notify func(message, level string)
}

// handleWatchdogCommand runs `/subagents-watchdog <args>`.
func handleWatchdogCommand(host watchdogCommandHost, rt *watchdogRuntime, args string, ctx wdContext) {
	input := jsTrim(args)
	status := func() string { return buildWatchdogStatus(rt.getSnapshot(ctx.Cwd()), ctx) }
	switch {
	case input == "" || input == "status":
		host.text(status())
		return
	case input == "recommend-model":
		text, err := wdBuildCommandRecommendation(rt, ctx)
		if err != nil {
			text = "Subagent watchdog recommended model\n\n" + err.Error()
		}
		host.text(text)
		return
	case input == "check":
		text, err := wdBuildCommandCheck(rt, ctx)
		if err != nil {
			text = "Subagent watchdog config check\n\n" + err.Error()
		}
		host.text(text)
		return
	case input == "on" || input == "off":
		enabled := input == "on"
		path, err := writeUserWatchdogEnabled(enabled)
		if err != nil {
			host.text("Subagent watchdog\n\nCould not update " + getWatchdogUserSettingsPath() + ": " + err.Error())
			return
		}
		snapshot := rt.getSnapshot(ctx.Cwd())
		now := "Main now: " + wdBoolLabel(snapshot.Enabled)
		if snapshot.SessionOverride != nil {
			now += " (session override " + wdBoolLabel(*snapshot.SessionOverride) + ")"
		}
		host.text(strings.Join([]string{"Subagent watchdog " + wdBoolLabel(enabled) + " saved to user settings.", "Updated: " + path, now}, "\n"))
		return
	case input == "session on" || input == "session off":
		enabled := strings.HasSuffix(input, "on")
		snapshot := rt.setSessionEnabled(enabled, ctx.Cwd())
		host.text(strings.Join([]string{"Subagent watchdog session override: " + wdBoolLabel(enabled) + ".", "No settings files were changed.", "", buildWatchdogStatus(snapshot, ctx)}, "\n"))
		return
	case strings.HasPrefix(input, "session model "):
		value, err := wdResolveModelCommandValue(rt, ctx, input[len("session model "):])
		if err != nil {
			host.text("Subagent watchdog session model\n\n" + err.Error())
			return
		}
		var snapshot watchdogRuntimeSnapshot
		if value.model.mode == 2 {
			snapshot = rt.clearSessionModel(ctx.Cwd())
		} else {
			thinking := value.thinking
			if thinking.mode == 0 || thinking.mode == 2 {
				thinking = wdClear[wdThinking]()
			}
			snapshot = rt.setSessionModel(wdSessionModelPatch{Model: value.model, Thinking: thinking}, ctx.Cwd())
		}
		host.text(strings.Join([]string{"Subagent watchdog session model: " + value.description + ".", "No settings files were changed.", "", buildWatchdogStatus(snapshot, ctx)}, "\n"))
		return
	case strings.HasPrefix(input, "model "):
		value, err := wdResolveModelCommandValue(rt, ctx, input[len("model "):])
		if err != nil {
			host.text("Subagent watchdog model\n\n" + err.Error() + "\nNo settings files were changed.")
			return
		}
		path, err := writeWatchdogModelSettings(wdModelWrite{Scope: "user", Target: wdTarget{Kind: "main"}, Model: value.model, Thinking: value.thinking})
		if err != nil {
			host.text("Subagent watchdog model\n\n" + err.Error() + "\nNo settings files were changed.")
			return
		}
		rt.refreshConfig(ctx.Cwd())
		snapshot := rt.getSnapshot(ctx.Cwd())
		tail := "Run /subagents-watchdog on if the watchdog is still off."
		if value.model.mode == 2 {
			tail = "The watchdog now inherits the current session model and thinking."
		}
		host.text(strings.Join([]string{
			"Subagent watchdog model saved to user settings: " + value.description + ". Project and session overrides still take precedence.",
			"Updated: " + path, "Main now: " + wdBoolLabel(snapshot.Enabled), tail, "", buildWatchdogStatus(snapshot, ctx)}, "\n"))
		return
	case strings.HasPrefix(input, "thinking "):
		raw := jsTrim(input[len("thinking "):])
		var thinking wdPatch[wdThinking]
		if raw == "inherit" {
			thinking = wdClear[wdThinking]()
		} else {
			parsed, err := parseWatchdogThinkingInput(raw, "/subagents-watchdog thinking")
			if err != nil {
				host.text("Subagent watchdog thinking\n\n" + err.Error() + "\nNo settings files were changed.")
				return
			}
			if parsed.set() {
				thinking = wdSetTo(parsed)
			} else {
				thinking = wdClear[wdThinking]()
			}
		}
		path, err := writeWatchdogModelSettings(wdModelWrite{Scope: "user", Target: wdTarget{Kind: "main"}, Thinking: thinking})
		if err != nil {
			host.text("Subagent watchdog thinking\n\n" + err.Error() + "\nNo settings files were changed.")
			return
		}
		rt.refreshConfig(ctx.Cwd())
		saved := "inherit"
		if thinking.mode == 1 {
			saved = wdFormatThinking(thinking.Value)
		}
		host.text(strings.Join([]string{"Subagent watchdog thinking saved: " + saved + ".", "Updated: " + path, "", buildWatchdogStatus(rt.getSnapshot(ctx.Cwd()), ctx)}, "\n"))
		return
	}
	if m := wdTestCommandRe.FindStringSubmatch(input); m != nil {
		text := jsTrim(m[3])
		if text == "" {
			host.notify("Usage: /subagents-watchdog test concern|blocker low|medium|high <text>", "error")
			return
		}
		rt.displayRecordedWarning(wdTestWarning(m[1], m[2], text))
		return
	}
	host.notify("Usage: /subagents-watchdog [status|on|off|session on|session off|recommend-model|model recommended|model <provider/model[:thinking]>|model inherit|thinking "+strings.Join(thinkingLevelList, "|")+"|thinking inherit|session model recommended|check|test concern|blocker low|medium|high <text>]", "error")
}

// ── registration ──

// watchdogHost holds the state the registered handlers share: the runtime and the context of the latest event.
type watchdogHost struct {
	mu       sync.Mutex
	ctx      sdk.Context
	has      bool
	baseline *watchdogDiffBaseline
	rt       *watchdogRuntime
}

func (h *watchdogHost) remember(ctx sdk.Context) {
	h.mu.Lock()
	h.ctx, h.has = ctx, true
	h.mu.Unlock()
}

func (h *watchdogHost) current() (sdk.Context, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctx, h.has
}

func (h *watchdogHost) currentWd() wdContext {
	ctx, ok := h.current()
	if !ok {
		return nil
	}
	return newSDKWatchdogContext(ctx)
}

func (h *watchdogHost) diffBaseline() *watchdogDiffBaseline {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.baseline
}

func wdRunContext(ctx sdk.Context) wdRunCtx {
	rc := wdRunCtx{Cwd: ctx.Cwd()}
	if sig := ctx.Signal(); sig != nil {
		rc.Signal = sig.Done()
	}
	return rc
}

func wdSendWarning(ctx sdk.Context, details wdWarning, opts *wdSendOptions) {
	msg := createWatchdogWarningMessage(details, wdMessageOptions{Details: details})
	so := sdk.SendMessageOptions{}
	if opts != nil {
		so.DeliverAs, so.TriggerTurn = opts.DeliverAs, opts.TriggerTurn
	}
	_ = ctx.SendCustomMessage(sdk.CustomMessage{CustomType: msg.CustomType, Content: msg.Content, Display: msg.Display, Details: msg.Details}, so)
}

// watchdogRuntimeFor builds the main watchdog runtime wired to the host.
func newMainWatchdogHost() *watchdogHost {
	h := &watchdogHost{}
	h.rt = newWatchdogRuntime(h.runtimeOptions())
	return h
}

func (h *watchdogHost) runtimeOptions() watchdogRuntimeOptions {
	review := createMainWatchdogReview(h.currentWd, wdReviewOptions{
		GetThinkingLevel: func() string {
			if c := h.currentWd(); c != nil {
				return c.ThinkingLevel()
			}
			return ""
		},
		DiffBaseline: h.diffBaseline,
	})
	return watchdogRuntimeOptions{
		Review: review, ReviewDescription: "real model review", ReviewChangesOnly: true,
		DisplayWarning: func(details wdWarning, opts *wdSendOptions) {
			if ctx, ok := h.current(); ok {
				wdSendWarning(ctx, details, opts)
			}
		},
		DisplayUserWarning: func(details wdWarning) {
			if ctx, ok := h.current(); ok {
				_ = ctx.AppendEntry(subagentWatchdogWarningType, details)
			}
		},
		DisplayClarification: func(content string) {
			if ctx, ok := h.current(); ok {
				msg, opts := wdClarificationMessage(content)
				_ = ctx.SendCustomMessage(msg, opts)
			}
		},
	}
}

func wdRenderWarning(message map[string]any, expanded bool) []string {
	raw, _ := message["details"].(map[string]any)
	if raw == nil {
		raw, _ = message["data"].(map[string]any)
	}
	var d wdWarning
	if raw != nil {
		d = wdWarningFromMap(raw)
	}
	if d.Summary == "" || d.Evidence == "" || d.RecommendedAction == "" {
		return strings.Split(wdMessageText(message["content"]), "\n")
	}
	return renderWatchdogWarning(d, expanded)
}

func wdMessageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, e := range c {
			if m, ok := e.(map[string]any); ok && m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func wdWarningFromMap(m map[string]any) wdWarning {
	str := func(k string) string { s, _ := m[k].(string); return s }
	w := wdWarning{Severity: str("severity"), Importance: str("importance"), Summary: str("summary"), Evidence: str("evidence"), RecommendedAction: str("recommendedAction"),
		Category: str("category"), Source: str("source"), Agent: str("agent"), RunID: str("runId"), State: str("state"), Identity: str("identity"), DisplayedAt: str("displayedAt"), Error: str("error")}
	if b, ok := m["stale"].(bool); ok {
		w.Stale = &b
	}
	if f, ok := m["stalemateRepeats"].(float64); ok {
		n := int(f)
		w.StalemateRepeats = &n
	}
	return w
}

// dispatch is the body of the handler registered for a session event (register-main.ts's pi.on calls).
func (h *watchdogHost) dispatch(name string, event any, rc wdRunCtx) {
	switch name {
	case sdk.EventSessionStart:
		h.mu.Lock()
		h.baseline = captureWatchdogDiffBaseline(rc.Cwd)
		h.mu.Unlock()
		h.rt.bindSession(rc)
	case sdk.EventBeforeAgentStart:
		h.rt.handleBeforeAgentStart(event, rc)
	case sdk.EventTurnEnd:
		h.rt.handleTurnEnd(event, rc, false)
	case sdk.EventInput:
		if wdPropV(event, "source") != "extension" {
			h.rt.handleUserInput()
		}
	case sdk.EventModelSelect:
		h.rt.handleModelChange()
	case sdk.EventToolResult:
		h.rt.handleToolResult(rc)
	case sdk.EventAgentEnd:
		h.rt.handleAgentEnd(event, rc)
	case sdk.EventSessionBeforeSwitch, sdk.EventSessionBeforeFork:
		h.rt.reset(wdResetOptions{clearReviewInputSignature: true, clearLspLedger: true, clearScope: true})
	case sdk.EventSessionCompact:
		h.rt.reset(wdResetOptions{clearActivity: true})
	case sdk.EventSessionShutdown:
		h.mu.Lock()
		h.has = false
		h.mu.Unlock()
		h.rt.dispose()
	}
}

// wdMainEvents are the events the main watchdog listens to.
var wdMainEvents = []string{sdk.EventSessionStart, sdk.EventBeforeAgentStart, sdk.EventTurnEnd, sdk.EventInput, sdk.EventModelSelect, sdk.EventToolResult,
	sdk.EventAgentEnd, sdk.EventSessionBeforeSwitch, sdk.EventSessionBeforeFork, sdk.EventSessionCompact, sdk.EventSessionShutdown}

// wdRemembers are the events whose context the watchdog keeps for the reviews and messages it sends later.
var wdRemembers = map[string]bool{sdk.EventSessionStart: true, sdk.EventBeforeAgentStart: true, sdk.EventTurnEnd: true, sdk.EventToolResult: true, sdk.EventAgentEnd: true}

// wdClarificationMessage is how a question of the reviewer reaches the orchestrator: a visible steer that starts a turn.
func wdClarificationMessage(content string) (sdk.CustomMessage, sdk.SendMessageOptions) {
	trigger := true
	return sdk.CustomMessage{CustomType: "subagent_watchdog_clarification", Content: content, Display: true}, sdk.SendMessageOptions{DeliverAs: "steer", TriggerTurn: &trigger}
}

func registerMainWatchdog(e *sdk.Extension) *watchdogHost {
	h := newMainWatchdogHost()
	e.MessageRenderer(subagentWatchdogWarningType, func(_ sdk.Context, message map[string]any, opts sdk.MessageRenderOptions, _ int) ([]string, error) {
		return wdRenderWarning(message, opts.Expanded), nil
	})
	e.EntryRenderer(subagentWatchdogWarningType, func(_ sdk.Context, entry map[string]any, opts sdk.EntryRenderOptions, _ int) ([]string, error) {
		raw, _ := entry["data"].(map[string]any)
		d := wdWarningFromMap(raw)
		if raw == nil || d.Summary == "" || d.Evidence == "" || d.RecommendedAction == "" {
			return nil, nil
		}
		return renderWatchdogWarning(d, opts.Expanded), nil
	})
	e.RegisterCommand("subagents-watchdog", sdk.CommandOptions{
		Description: "Show or toggle the default-off subagent watchdog",
		Handler: func(ctx sdk.Context, args string) error {
			h.remember(ctx)
			handleWatchdogCommand(watchdogCommandHost{
				text:   func(s string) { slashText(ctx, s) },
				notify: func(m, l string) { ctx.Notify(m, l) },
			}, h.rt, args, newSDKWatchdogContext(ctx))
			return nil
		},
	})
	for _, name := range wdMainEvents {
		name := name
		e.OnEvent(name, func(ctx sdk.Context, data map[string]any) (any, error) {
			if wdRemembers[name] {
				h.remember(ctx)
			}
			h.dispatch(name, toDyn(data), wdRunContext(ctx))
			return nil, nil
		})
	}
	return h
}

// watchdogTool is the runtime the `subagent` tool's watchdog actions talk to.
var watchdogMain *watchdogHost

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) { watchdogMain = registerMainWatchdog(e) })
	for _, name := range watchdogToolActions {
		registerAction(name, []string{"scope", "target", "agent", "model", "thinking", "cwd"}, runWatchdogToolAction(name))
	}
}

func runWatchdogToolAction(name string) func(ctx sdk.Context, params map[string]any) (any, error) {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		p := watchdogToolParams{Thinking: params["thinking"]}
		p.Scope, _ = params["scope"].(string)
		p.Target, _ = params["target"].(string)
		p.Agent, _ = params["agent"].(string)
		p.Model, _ = params["model"].(string)
		p.Cwd, _ = params["cwd"].(string)
		wctx := newSDKWatchdogContext(ctx)
		var rt *watchdogRuntime
		if watchdogMain != nil {
			rt = watchdogMain.rt
			watchdogMain.remember(ctx)
		}
		if p.Cwd != "" {
			wctx = wdCwdOverride{wctx, nodeResolve(ctx.Cwd(), p.Cwd)}
		}
		res := handleWatchdogToolAction(name, p, wctx, rt)
		if res.IsError {
			return nil, sdk.NewToolError(res.Text)
		}
		return explicitOK{Content: res.Text, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
	}
}

// wdCwdOverride is a context whose cwd is the tool call's resolved cwd parameter.
type wdCwdOverride struct {
	wdContext
	cwd string
}

func (c wdCwdOverride) Cwd() string { return c.cwd }
