package powerline_footer

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of the working vibes: the /vibe command and the before_agent_start, agent_start, tool_call and agent_end
// handlers. upstream: index.ts 1876-1878 (session_start), 1993-1999, 2067, 2149-2156, 2453-2466 and 2743-2855.

// sdkVibeRegistry is the host's model registry for one retained context (the original keeps the session_start ctx).
type sdkVibeRegistry struct{ ctx sdk.Context }

func (r sdkVibeRegistry) Find(provider, modelID string) (map[string]any, error) {
	return r.ctx.ModelRegistry().Find(provider, modelID), nil
}

func (r sdkVibeRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	return r.ctx.ModelRegistry().GetApiKeyAndHeaders(model)
}

func (r sdkVibeRegistry) GetProviderBaseURL(provider string) (string, error) {
	result, err := r.ctx.ModelRegistry().GetProviderAuth(provider)
	if err != nil {
		return "", err
	}
	auth, _ := result["auth"].(map[string]any)
	baseURL, _ := auth["baseUrl"].(string)
	return baseURL, nil
}

func (r sdkVibeRegistry) Complete(model, request, options map[string]any) map[string]any {
	return r.ctx.ModelRegistry().Complete(model, request, options)
}

// footerRendered reports whether the bar has built its render context: the original learns the theme for working messages
// there (buildSegmentContext), so a message before the first render is unstyled.
func (a *app) footerRendered() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pl != nil && a.pl.frame.Load() != nil
}

// registerVibes wires the working vibes into the extension.
func registerVibes(e *sdk.Extension, a *app) {
	m := newVibeManager()

	// styled hands the manager the theme the bar draws with, once the bar has drawn.
	styled := func(ctx sdk.Context) {
		if a.footerRendered() {
			m.setWorkingMessageTheme(strictTheme{ctx.UITheme()})
		}
	}

	e.Command("vibe", "Set working message theme. Usage: /vibe [theme|off|mode|model|generate]", func(ctx sdk.Context, args string) error {
		return m.command(args, ctx.Notify)
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		// initVibeManager(ctx); setVibeWorkingMessageColor(config.workingVibes.color)
		m.initVibeManager(sdkVibeRegistry{ctx})
		m.setWorkingMessageColor(parsePowerlineConfig(readSettings(ctx.Cwd())["powerline"], presetNames).WorkingVibesColor)
		return nil, nil
	})

	// Generate the themed working message before the agent starts (the user's prompt is known).
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		if ctx.HasUI() {
			styled(ctx)
			prompt, _ := data["prompt"].(string)
			m.onBeforeAgentStart(prompt, ctx.SetWorkingMessage)
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		m.onAgentStart()
		return nil, nil
	})

	// Refresh the vibe on tool calls when the rate limit allows, with the recent agent text as context.
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		if ctx.HasUI() {
			styled(ctx)
			var branch []map[string]any
			if m.toolCallDue() {
				branch, _ = ctx.SessionManager().GetBranch(nil)
			}
			toolName, _ := data["toolName"].(string)
			input, _ := data["input"].(map[string]any)
			m.onToolCall(toolName, input, ctx.SetWorkingMessage, getRecentAgentContext(branch))
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if ctx.HasUI() {
			m.onAgentEnd(ctx.SetWorkingMessage)
		}
		return nil, nil
	})
}
