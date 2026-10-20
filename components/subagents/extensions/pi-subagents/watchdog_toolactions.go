package pi_subagents

import (
	"strings"
)

// The `watchdog.*` actions of the subagent tool (watchdog/tool-actions.ts).

var watchdogToolActions = []string{"watchdog.status", "watchdog.check", "watchdog.configure", "watchdog.recommend-model"}

type watchdogToolParams struct {
	Scope  string
	Target string
	Agent  string
	Model  string
	// Thinking is what the model passed: nil, a string, or a boolean (the schema lets `true` through; the runtime does not).
	Thinking any
	Cwd      string
}

type watchdogToolResult struct {
	Text    string
	IsError bool
}

func wdToolOK(text string) watchdogToolResult  { return watchdogToolResult{Text: text} }
func wdToolErr(text string) watchdogToolResult { return watchdogToolResult{Text: text, IsError: true} }

func wdToolScope(raw string) (string, error) {
	if raw == "" || raw == "session" {
		return "session", nil
	}
	if raw == "user" || raw == "project" {
		return raw, nil
	}
	return "", wdErr("watchdog.configure scope must be 'session', 'user', or 'project'.")
}

func wdParseTarget(p watchdogToolParams) (wdTarget, error) {
	target := p.Target
	if target == "" {
		target = "main"
	}
	switch target {
	case "main", "children":
		return wdTarget{Kind: target}, nil
	case "child":
		agent := jsTrim(p.Agent)
		if agent == "" {
			return wdTarget{}, wdErr("watchdog.configure target='child' requires agent.")
		}
		return wdTarget{Kind: "child", Agent: agent}, nil
	}
	return wdTarget{}, wdErr("watchdog.configure target must be 'main', 'children', or 'child'.")
}

type wdErrString string

func (e wdErrString) Error() string { return string(e) }
func wdErr(s string) error          { return wdErrString(s) }

// wdConfiguredValue is the model and thinking a configure action sets: each is left alone, set or cleared.
type wdConfiguredValue struct {
	Model       wdPatch[string]
	Thinking    wdPatch[wdThinking]
	Description string
}

func wdThinkingDescription(p wdPatch[wdThinking]) string {
	switch {
	case p.mode == 2:
		return "inherit"
	case p.Value.Off:
		return "off"
	}
	return p.Value.Level
}

func wdResolveConfiguredValue(ctx wdContext, p watchdogToolParams, rt *watchdogRuntime) (wdConfiguredValue, error) {
	var thinking wdPatch[wdThinking]
	if s, ok := p.Thinking.(string); ok && s == "inherit" {
		thinking = wdClear[wdThinking]()
	} else {
		parsed, err := parseWatchdogThinkingInput(p.Thinking, "watchdog.configure thinking")
		if err != nil {
			return wdConfiguredValue{}, err
		}
		if parsed.set() {
			thinking = wdSetTo(parsed)
		}
	}
	rawModel := jsTrim(p.Model)
	if rawModel == "" {
		if thinking.mode == 0 {
			return wdConfiguredValue{}, wdErr("watchdog.configure requires model, thinking, or both.")
		}
		return wdConfiguredValue{Thinking: thinking, Description: "thinking " + wdThinkingDescription(thinking)}, nil
	}
	if rawModel == "inherit" {
		if thinking.mode == 0 {
			thinking = wdClear[wdThinking]()
		}
		return wdConfiguredValue{Model: wdClear[string](), Thinking: thinking, Description: "inherit"}, nil
	}
	if rawModel == "recommended" {
		var rec watchdogModelRecommendation
		var err error
		if p.Target == "" || p.Target == "main" {
			rec, err = recommendWatchdogModel(ctx, wdMainConfig(ctx, rt).Main)
		} else {
			rec, err = recommendStrongWatchdogModel(ctx)
		}
		if err != nil {
			return wdConfiguredValue{}, err
		}
		return wdConfiguredValue{Model: wdSetTo(rec.Model), Thinking: wdSetTo(wdThinking{Level: rec.Thinking}), Description: rec.Model + ":" + rec.Thinking}, nil
	}
	resolved, err := resolveWatchdogModelInput(ctx, rawModel)
	if err != nil {
		return wdConfiguredValue{}, err
	}
	value := wdConfiguredValue{Model: wdSetTo(resolved.Model), Thinking: thinking, Description: resolved.Model}
	if resolved.Thinking != "" {
		value.Thinking = wdSetTo(wdThinking{Level: resolved.Thinking})
		value.Description += ":" + resolved.Thinking
	} else if thinking.mode == 1 && thinking.Value.set() && !thinking.Value.Off {
		value.Description += ":" + thinking.Value.Level
	}
	return value, nil
}

// wdMainConfig is the config the watchdog runs with: the runtime's, else the settings'.
func wdMainConfig(ctx wdContext, rt *watchdogRuntime) *watchdogConfig {
	if rt != nil {
		return rt.getSnapshot(ctx.Cwd()).Config
	}
	return resolveWatchdogConfig(ctx.Cwd(), nil).Config
}

func wdBuildRecommendationText(ctx wdContext, rt *watchdogRuntime) (string, error) {
	rec, err := recommendWatchdogModel(ctx, wdMainConfig(ctx, rt).Main)
	if err != nil {
		return "", err
	}
	line := "Recommended: " + rec.Model + ":" + rec.Thinking
	if rec.Source == "configured" {
		line = formatWatchdogRecommendation(rec)
	}
	return strings.Join([]string{
		"Subagent watchdog recommended model", line, "Reason: " + rec.Reason,
		`Apply temporarily with subagent({ action: "watchdog.configure", scope: "session", model: "recommended" }).`,
		`Persist with scope: "project" or scope: "user" only when the user asks for that scope.`,
	}, "\n"), nil
}

func wdBuildCheckText(rt *watchdogRuntime, ctx wdContext) (string, error) {
	if rt == nil {
		return "Subagent watchdog runtime is unavailable.", nil
	}
	snapshot := rt.getSnapshot(ctx.Cwd())
	if !snapshot.ConfigOK {
		lines := []string{"Subagent watchdog config check", "Config errors:"}
		for _, e := range snapshot.Errors {
			lines = append(lines, "- "+e.Message)
		}
		return strings.Join(lines, "\n"), nil
	}
	lines := []string{"Subagent watchdog config check", "Config: ok"}
	if snapshot.Config.Main.Model != "" {
		resolved, err := resolveWatchdogModelInput(ctx, snapshot.Config.Main.Model)
		if err != nil {
			return "", err
		}
		lines = append(lines, "Main model: "+resolved.Model+" auth ok")
	} else {
		lines = append(lines, "Main model: current session")
	}
	onOff := "off"
	if snapshot.Lsp.Enabled {
		onOff = "on"
	}
	lines = append(lines, "LSP diagnostics: "+onOff+" · "+snapshot.Lsp.Status)
	if rec, err := recommendWatchdogModel(ctx, snapshot.Config.Main); err != nil {
		lines = append(lines, "Watchdog recommendation unavailable: "+err.Error())
	} else {
		lines = append(lines, formatWatchdogRecommendation(rec))
	}
	return strings.Join(lines, "\n"), nil
}

// handleWatchdogToolAction runs one watchdog action; a failure is an error result, never a thrown one.
func handleWatchdogToolAction(action string, p watchdogToolParams, ctx wdContext, rt *watchdogRuntime) watchdogToolResult {
	res, err := wdRunToolAction(action, p, ctx, rt)
	if err != nil {
		return wdToolErr("Subagent watchdog action failed: " + err.Error())
	}
	return res
}

func wdRunToolAction(action string, p watchdogToolParams, ctx wdContext, rt *watchdogRuntime) (watchdogToolResult, error) {
	switch action {
	case "watchdog.status":
		if rt == nil {
			return wdToolErr("Subagent watchdog runtime is unavailable."), nil
		}
		return wdToolOK(buildWatchdogStatus(rt.getSnapshot(ctx.Cwd()), ctx)), nil
	case "watchdog.recommend-model":
		text, err := wdBuildRecommendationText(ctx, rt)
		return wdToolOK(text), err
	case "watchdog.check":
		text, err := wdBuildCheckText(rt, ctx)
		return wdToolOK(text), err
	case "watchdog.configure":
	default:
		return wdToolErr("Unknown watchdog action: " + action), nil
	}
	scope, err := wdToolScope(p.Scope)
	if err != nil {
		return watchdogToolResult{}, err
	}
	target, err := wdParseTarget(p)
	if err != nil {
		return watchdogToolResult{}, err
	}
	value, err := wdResolveConfiguredValue(ctx, p, rt)
	if err != nil {
		return watchdogToolResult{}, err
	}
	if scope == "session" {
		if rt == nil {
			return wdToolErr("Subagent watchdog runtime is unavailable."), nil
		}
		if target.Kind != "main" {
			return wdToolErr("Session-scoped watchdog.configure currently supports target='main' only."), nil
		}
		rt.setSessionModel(wdSessionModelPatch{Model: value.Model, Thinking: value.Thinking}, ctx.Cwd())
		return wdToolOK(strings.Join([]string{
			"Subagent watchdog session model configured: " + value.Description + ".",
			"No settings files were changed.",
			"",
			buildWatchdogStatus(rt.getSnapshot(ctx.Cwd()), ctx),
		}, "\n")), nil
	}
	path, err := writeWatchdogModelSettings(wdModelWrite{Scope: scope, Cwd: ctx.Cwd(), Target: target, Model: value.Model, Thinking: value.Thinking})
	if err != nil {
		return watchdogToolResult{}, err
	}
	if rt != nil {
		rt.refreshConfig(ctx.Cwd())
	}
	label := target.Kind
	if target.Kind == "child" {
		label = "child " + target.Agent
	}
	return wdToolOK(strings.Join([]string{"Subagent watchdog " + label + " model configured: " + value.Description + ".", "Updated: " + path}, "\n")), nil
}
