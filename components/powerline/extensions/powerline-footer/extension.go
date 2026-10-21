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
	pl  *powerline
	// sent is what each widget shows on the host now: a repaint sends only the widgets whose lines or placement changed (a
	// blocking host call each). A widget the host cleared (ClearWidget) is forgotten.
	sent map[string]sentWidget
}

type sentWidget struct {
	placement string
	lines     []string
}

// setWidget sends lines to the host unless it already shows exactly these at this placement.
func (h *sdkHost) setWidget(key, placement string, lines []string) {
	if prev, ok := h.sent[key]; ok && prev.placement == placement && slices.Equal(prev.lines, lines) {
		return
	}
	if h.sent == nil {
		h.sent = map[string]sentWidget{}
	}
	h.sent[key] = sentWidget{placement, slices.Clone(lines)}
	_ = h.ctx.SetWidget(key, lines, sdk.WidgetOptions{"placement": placement})
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

// Queue: the host calls the queue needs. upstream: pi.sendUserMessage, ctx.isIdle, ctx.ui.setEditorText, ctx.compact.
func (h *sdkHost) Sender() func(text, deliverAs string) error {
	ctx := h.ctx
	return func(text, deliverAs string) error { return ctx.SendUserMessage(text, deliverAs) }
}

func (h *sdkHost) IdleCheck() func() (bool, error) {
	ctx := h.ctx
	return ctx.IsIdle
}

func (h *sdkHost) IsIdle() (bool, error)     { return h.ctx.IsIdle() }
func (h *sdkHost) SetEditorText(text string) { h.ctx.SetEditorText(text) }
func (h *sdkHost) Compact(onError func(error)) {
	h.ctx.CompactWithOptions(sdk.CompactOptions{OnError: onError})
}

func (h *sdkHost) Theme() theme { return strictTheme{h.ctx.UITheme()} }

func (h *sdkHost) Notify(message, level string) { h.ctx.Notify(message, level) }

func (h *sdkHost) SetStatus(key string, text *string) {
	if text == nil {
		h.ctx.SetStatus(key, "")
		return
	}
	h.ctx.SetStatus(key, *text)
}

func (h *sdkHost) ClearWidget(key string) {
	delete(h.sent, key)
	_ = h.ctx.SetWidget(key, nil)
}

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
	delete(h.sent, key)
	_ = h.ctx.SetWidget(key, nil)
}

// Repaint sends the bar's current rows: the primary bar and the notification statuses as widget lines at the terminal width
// (a widget takes lines, G1), and the secondary row through the footer renderer.
func (h *sdkHost) Repaint() {
	if h.ctx.Mode() == "rpc" {
		return
	}
	h.pl.refresh() // the host is asked here, in the handler, never while a row is rendered
	h.pl.queue.holdSession(h.pl.base.SessionID)
	defer h.pl.queue.releaseSession()
	ui := h.pl.installed()
	width := h.ctx.Width()
	if placement, ok := ui.Widgets[widgetTop]; ok {
		h.setWidget(widgetTop, placement, h.pl.renderTop(width))
	}
	if placement, ok := ui.Widgets[widgetStatus]; ok {
		h.setWidget(widgetStatus, placement, h.pl.renderStatus(width))
	}
	if placement, ok := ui.Widgets[widgetBashTranscript]; ok {
		h.setWidget(widgetBashTranscript, placement, h.pl.renderBashTranscript(width))
	}
	if placement, ok := ui.Widgets[widgetPendingSend]; ok {
		h.setWidget(widgetPendingSend, placement, h.pl.renderPendingSend(width))
	}
	if placement, ok := ui.Widgets[widgetQueuePreview]; ok {
		h.setWidget(widgetQueuePreview, placement, h.pl.renderQueuePreview(width))
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
	welcome   welcomeState // welcome.go, welcome_ext.go
	cdq       *cdQuote     // /cd, /reply and the last-prompt line (cdquote_ext.go)
	keys      *keysExt
	bash      *bashExt // bash mode (bash_ext.go)
}

func (a *app) core(ctx sdk.Context) *powerline {
	a.once.Do(func() {
		a.host = &sdkHost{ctx: ctx}
		a.pl = newPowerline(a.host)
		a.host.pl = a.pl
		a.pl.queue.mu = &a.mu
		if a.keys != nil {
			a.pl.keys = a.keys
		}
		if a.bash != nil {
			a.pl.bash = a.bash
		}
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
	a.welcomeBeginSession()
	pl := a.core(ctx)
	reason, _ := data["reason"].(string)
	pl.sessionStart(reason)
	pl.warnUnsupported()
	a.cdq.sessionStart(ctx, pl.enabled)
	defer a.welcomeSessionStart(ctx, reason) // after the bar is installed, as in index.ts
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
		if name == sdk.EventAgentStart {
			a.welcomeDismiss() // a prompt given on the command line starts a turn with no key press
		}
		return nil, nil
	}
}

func (a *app) command(ctx sdk.Context, args string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	pl := a.core(ctx)
	wasEnabled := pl.enabled
	pl.command(args)
	if wasEnabled && !pl.enabled {
		a.welcomeDismiss() // /powerline toggled off
	}
	a.cdq.sync(ctx, a.pl.enabled, true) // the core clears and installs the widgets again
	return nil
}

// Extension is the powerline status bar.
func Extension() *sdk.Extension {
	e := sdk.New("powerline-footer")
	a := &app{}
	e.Command("powerline", "Configure powerline status (toggle, preset)", a.command)
	for _, c := range refusedCommands {
		refused := refusal(c.feature)
		e.Command(c.name, c.description, func(ctx sdk.Context, _ string) error {
			ctx.Notify(refused, "error")
			return nil
		})
	}
	registerKeys(e, a)
	registerFollowUp(e, a)
	registerVibes(e, a)
	e.OnSessionStart(a.onSessionStart)
	registerWelcome(e, a)
	for _, ev := range []string{sdk.EventModelSelect, sdk.EventThinkingLevelSelect, sdk.EventSessionTree, sdk.EventSessionInfoChanged,
		sdk.EventToolResult, sdk.EventUserBash, sdk.EventAgentStart, sdk.EventMessageUpdate, sdk.EventMessageEnd, sdk.EventTurnEnd,
		sdk.EventSessionBeforeCompact, sdk.EventSessionCompact, sdk.EventAgentEnd} {
		e.OnEvent(ev, a.onEvent(ev))
	}
	registerQueue(e, a)
	registerCdQuote(e, a)
	registerBash(e, a)
	// The shortcut actions the keys slice leaves to the features that own them (index.ts runPowerlineShortcut 2379-2390).
	a.keys.setHook(actionQueueOpen, a.openQueuePicker)
	a.keys.setHook(actionReply, func(ctx sdk.Context) {
		if err := runReplyCommand(ctx, ""); err != nil {
			ctx.Notify(err.Error(), "error")
		}
	})
	return e
}

// renderBashTranscript is the powerline-bash-transcript widget at a width.
func (p *powerline) renderBashTranscript(width int) []string {
	if p.bash == nil {
		return nil
	}
	return p.bash.transcriptLines(width, p.h.Theme())
}
