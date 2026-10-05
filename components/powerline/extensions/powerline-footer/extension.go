package powerline_footer

import (
	"slices"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring: sdkHost adapts a Context to the core's hostAPI.

// strictTheme fails on a token Pi's Theme does not know, as Theme.fg throws there; the SDK's UITheme leaves such text uncolored.
type strictTheme struct{ ui sdk.UITheme }

func (s strictTheme) Fg(token, text string) (string, error) {
	if !slices.Contains(piThemeTokens, token) {
		return "", &unknownTokenError{token}
	}
	return s.ui.Fg(token, text), nil
}

type unknownTokenError struct{ token string }

func (e *unknownTokenError) Error() string { return "Unknown theme color: " + e.token }

type sdkHost struct {
	ctx sdk.Context
	// Rows pushed to the host last, so a repaint at another width can replace them.
	pl *powerline
}

func (h *sdkHost) Cwd() string  { return h.ctx.Cwd() }
func (h *sdkHost) HasUI() bool  { return h.ctx.HasUI() }
func (h *sdkHost) Mode() string { return h.ctx.Mode() }

func (h *sdkHost) Model() *modelInfo {
	m, err := h.ctx.GetModelInfo()
	if err != nil || m == nil {
		return nil
	}
	return &modelInfo{ID: m.ID, Name: m.Name, Provider: m.Provider, Reasoning: m.Reasoning, ContextWindow: float64(m.ContextWindow)}
}

func (h *sdkHost) ThinkingLevel() string {
	l, _ := h.ctx.GetThinkingLevel()
	return l
}

func (h *sdkHost) SessionID() string {
	id, _ := h.ctx.GetSessionID()
	return id
}

func (h *sdkHost) SessionName() string {
	n, err := h.ctx.GetSessionName()
	if err != nil || n == nil {
		return ""
	}
	return *n
}

func (h *sdkHost) UsingOAuth() bool {
	m := h.Model()
	if m == nil {
		return false
	}
	ok, err := h.ctx.ModelRegistry().IsUsingOAuth(map[string]any{"id": m.ID, "provider": m.Provider})
	return err == nil && ok
}

// AutoCompactEnabled reads compaction.enabled from the effective settings (true when unset).
func (h *sdkHost) AutoCompactEnabled() bool {
	settings, err := h.ctx.GetSettings()
	if err != nil {
		return true
	}
	if c, ok := settings["compaction"].(map[string]any); ok {
		if enabled, ok := c["enabled"].(bool); ok {
			return enabled
		}
	}
	return true
}

func (h *sdkHost) Session() branchProvider { return h.ctx.SessionManager() }

func (h *sdkHost) ContextUsage() map[string]any {
	u, err := h.ctx.GetContextUsage()
	if err != nil || u == nil {
		return nil
	}
	m := map[string]any{"contextWindow": float64(u.ContextWindow), "tokens": nil, "percent": nil}
	if u.Tokens != nil {
		m["tokens"] = float64(*u.Tokens)
	}
	if u.Percent != nil {
		m["percent"] = *u.Percent
	}
	return m
}

// ExtensionStatuses: the Go SDK cannot read other extensions' statuses (ReadonlyFooterDataProvider.getExtensionStatuses, G11).
func (h *sdkHost) ExtensionStatuses() []statusEntry { return nil }

// ProviderGitBranch: the host's footer-data git branch is not exposed either (G11); git is read directly.
func (h *sdkHost) ProviderGitBranch() *string { return nil }

func (h *sdkHost) Theme() theme { return strictTheme{h.ctx.UITheme()} }

func (h *sdkHost) Notify(message, level string) { h.ctx.Notify(message, level) }

func (h *sdkHost) SetStatus(key string, text *string) {
	if text == nil {
		h.ctx.SetStatus(key, "")
		return
	}
	h.ctx.SetStatus(key, *text)
}

func (h *sdkHost) ClearWidget(key string) { _ = h.ctx.SetWidget(key, nil) }

// SetFooterInstalled and SetWidgetInstalled draw nothing in RPC mode: Pi drops the component factories there, and the traces
// recorded from Pi (which this port is checked against) show no row from them.
func (h *sdkHost) SetFooterInstalled(on bool) {
	if h.ctx.Mode() == "rpc" || on {
		return // installed by Repaint, once the frame is published
	}
	_ = h.ctx.SetFooterRenderer(nil)
}

func (h *sdkHost) SetWidgetInstalled(key, placement string, on bool) {
	if h.ctx.Mode() == "rpc" || on {
		return // pushed by Repaint, with content
	}
	_ = h.ctx.SetWidget(key, nil)
}

// Repaint sends the bar's current rows: the primary bar and the notification statuses as widget lines at the terminal width
// (a widget takes lines, G1), and the secondary row through the footer renderer.
func (h *sdkHost) Repaint() {
	if h.ctx.Mode() == "rpc" {
		return
	}
	h.pl.refresh() // the host is asked here, in the handler, never while a row is rendered
	ui := h.pl.installed()
	width := h.ctx.Width()
	if placement, ok := ui.Widgets[widgetTop]; ok {
		_ = h.ctx.SetWidget(widgetTop, h.pl.renderTop(width), sdk.WidgetOptions{"placement": placement})
	}
	if placement, ok := ui.Widgets[widgetStatus]; ok {
		_ = h.ctx.SetWidget(widgetStatus, h.pl.renderStatus(width), sdk.WidgetOptions{"placement": placement})
	}
	if ui.Footer {
		_ = h.ctx.SetFooterRenderer(h.pl.footerRows)
	}
}

// app serialises the core: events, commands and width changes run on different goroutines. The footer renderer is not
// serialised (the SDK may call it inside a handler that holds mu); it reads only the published frame.
type app struct {
	mu        sync.Mutex
	host      *sdkHost
	pl        *powerline
	once      sync.Once
	widthOnce sync.Once
}

func (a *app) core(ctx sdk.Context) *powerline {
	a.once.Do(func() {
		a.host = &sdkHost{ctx: ctx}
		a.pl = newPowerline(a.host)
		a.host.pl = a.pl
	})
	a.host.ctx = ctx
	return a.pl
}

// footerRows is the footer renderer: pure layout of the last published frame. It runs on the SDK's goroutine, so it touches
// nothing a handler writes.
func (p *powerline) footerRows(width int) []string {
	f := p.frame.Load()
	if f == nil {
		return []string{}
	}
	if _, sec := layoutSegments(f.ctx, f.ids, f.style, width); sec != "" {
		return []string{sec}
	}
	return []string{}
}

func (a *app) onSessionStart(ctx sdk.Context, data map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pl := a.core(ctx)
	reason, _ := data["reason"].(string)
	pl.sessionStart(reason)
	a.widthOnce.Do(func() {
		// The bar's widget rows are laid out at a width; lay them out again when the terminal changes size.
		_, _ = ctx.OnWidthChange(func(c sdk.Context, _ int) {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.core(c)
			if a.pl.enabled && c.HasUI() {
				a.host.Repaint()
			}
		})
	})
	return nil, nil
}

func (a *app) onEvent(name string) sdk.EventFunc {
	return func(ctx sdk.Context, data map[string]any) (any, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.core(ctx).event(name, data)
		return nil, nil
	}
}

func (a *app) command(ctx sdk.Context, args string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core(ctx).command(args)
	return nil
}

// Extension is the powerline status bar.
func Extension() *sdk.Extension {
	e := sdk.New("powerline-footer")
	a := &app{}
	e.Command("powerline", "Configure powerline status (toggle, preset)", a.command)
	e.OnSessionStart(a.onSessionStart)
	for _, ev := range []string{sdk.EventModelSelect, sdk.EventThinkingLevelSelect, sdk.EventSessionTree, sdk.EventSessionInfoChanged,
		sdk.EventToolResult, sdk.EventUserBash, sdk.EventAgentStart, sdk.EventMessageUpdate, sdk.EventMessageEnd, sdk.EventTurnEnd,
		sdk.EventSessionBeforeCompact, sdk.EventSessionCompact, sdk.EventAgentEnd} {
		e.OnEvent(ev, a.onEvent(ev))
	}
	return e
}
