package pi_goal_x

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring: sdkHost adapts a Context to the host interface (host.go). goal.go installs one core for the extension process and
// binds the Context of each call (core.withCtx).

type sdkHost struct {
	ctx sdk.Context
	c   *core
}

func (h sdkHost) Cwd() string { return h.ctx.Cwd() }
func (h sdkHost) SessionID() string {
	id, _ := h.ctx.GetSessionID()
	return id
}
func (h sdkHost) SessionFile() string {
	f, err := h.ctx.GetSessionFile()
	if err != nil || f == nil {
		return ""
	}
	return *f
}
func (h sdkHost) HasUI() bool        { return h.ctx.HasUI() }
func (h sdkHost) Mode() string       { return h.ctx.Mode() }
func (h sdkHost) Notify(m, l string) { h.ctx.Notify(m, l) }
func (h sdkHost) IsIdle() bool {
	idle, err := h.ctx.IsIdle()
	return err != nil || idle
}
func (h sdkHost) HasPendingMessages() bool {
	pending, err := h.ctx.HasPendingMessages()
	return err == nil && pending
}
func (h sdkHost) Abort() { h.ctx.Abort() }
func (h sdkHost) Signal() context.Context {
	if s := h.ctx.Signal(); s != nil {
		return s
	}
	return context.Background()
}
func (h sdkHost) Branch() []any {
	raw, err := h.ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, e := range raw {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if v, err := parseJSON(b); err == nil {
			out = append(out, v)
		}
	}
	return out
}
func (h sdkHost) Model() *auditorModel {
	info, err := h.ctx.GetModelInfo()
	if err != nil || info == nil {
		return nil
	}
	return &auditorModel{provider: info.Provider, id: info.ID, name: info.Name}
}
func (h sdkHost) ModelRegistry() auditorModelRegistry { return sdkModelRegistry{h.ctx.ModelRegistry()} }
func (h sdkHost) ContextUsage() *goalContextUsage {
	u, err := h.ctx.GetContextUsage()
	if err != nil || u == nil {
		return nil
	}
	out := &goalContextUsage{contextWindow: float64(u.ContextWindow)}
	if u.Tokens != nil {
		out.tokens = new(float64(*u.Tokens))
	}
	return out
}

func (h sdkHost) SetStatus(k, t string) { h.ctx.SetStatus(k, t) }

// SetWidget drops non-clear pushes in RPC mode: Pi's RPC host shows no widget lines for a component factory, so the original
// shows nothing there (G3).
func (h sdkHost) SetWidget(key string, lines []string) {
	if lines == nil {
		_ = h.ctx.SetWidget(key, nil)
		return
	}
	if h.ctx.Mode() == "rpc" {
		return
	}
	// The wire carries a pushed widget without an answer the fake host records; tests read what was sent through this tap.
	if h.c != nil && h.c.widgetPushTap != nil {
		h.c.widgetPushTap(key, lines)
	}
	_ = h.ctx.SetWidget(key, lines)
}
func (h sdkHost) Width() int { return h.ctx.Width() }
func (h sdkHost) Height() (int, bool) {
	rows := h.ctx.Height()
	return rows, rows > 0
}
func (h sdkHost) OnWidthChange(fn func(width int)) func() {
	off, err := h.ctx.OnWidthChange(func(_ sdk.Context, width int) { fn(width) })
	if err != nil {
		return func() {}
	}
	return off
}
func (h sdkHost) Theme() goalHostTheme { return sdkTheme{h.ctx.UITheme()} }

// await runs a host call that waits for the user (a dialog): the core's lock is released meanwhile, as the original's other event
// handlers run at an `await`. Without it a dialog would hold up the key events that answer it. State the caller read before the call
// may have changed after it; revalidate (focus tokens) as the original does.
func (h sdkHost) await(fn func()) {
	if h.c != nil {
		h.c.mu.Unlock()
		defer h.c.mu.Lock()
	}
	fn()
}

func (h sdkHost) Confirm(t, m string) (ok bool) {
	h.await(func() { ok, _ = h.ctx.Confirm(t, m) })
	return ok
}
func (h sdkHost) Select(t string, o []string) (v string, ok bool) {
	h.await(func() {
		var err error
		v, ok, err = h.ctx.Select(t, o)
		ok = ok && err == nil
	})
	return v, ok
}
func (h sdkHost) Input(t, placeholder string) (v string, ok bool) {
	h.await(func() {
		var err error
		v, ok, err = h.ctx.Input(t, placeholder)
		ok = ok && err == nil
	})
	return v, ok
}
func (h sdkHost) Custom(factory goalCustomFactory, opts goalCustomOptions) (out any, err error) {
	if !h.ctx.HasUI() {
		return nil, nil
	}
	rc := &remoteDialog{}
	rc.comp = factory(rc, h.Theme(), func(v any) { rc.done, rc.value = true, v })
	if rc.comp == nil {
		return nil, nil
	}
	h.await(func() { out, err = h.ctx.Custom(rc, remoteOverlayOptions(opts)) })
	return out, err
}
func (h sdkHost) OnTerminalInput(cb func(data string) *goalInputResult) func() {
	off, err := h.ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		var out sdk.TerminalInputResult
		h.c.locked(func() {
			if r := cb(data); r != nil && r.consume {
				out.Consume = true
			}
		})
		return out
	})
	if err != nil {
		return func() {}
	}
	return off
}
func (h sdkHost) SetWorkingVisible(visible bool)    { h.ctx.SetWorkingVisible(visible) }
func (h sdkHost) AppendEntry(t string, d *jsObject) { _ = h.ctx.AppendEntry(t, d) }
func (h sdkHost) SendMessage(customType, content string, display bool, details *jsObject, triggerTurn bool, deliverAs string) error {
	msg := sdk.CustomMessage{CustomType: customType, Content: content, Display: display}
	if details != nil {
		msg.Details = details
	}
	return h.ctx.SendCustomMessage(msg, sdk.SendMessageOptions{TriggerTurn: &triggerTurn, DeliverAs: deliverAs})
}
func (h sdkHost) SendUserMessage(content, deliverAs string) error {
	return h.ctx.SendUserMessage(content, deliverAs)
}
func (h sdkHost) GetActiveTools() ([]string, error) { return h.ctx.GetActiveTools() }
func (h sdkHost) SetActiveTools(tools []string)     { h.ctx.SetActiveTools(tools) }

// OnEvent is pi.events.on: the listener runs as one unit of work with the emitting request's context bound as the core's host.
func (h sdkHost) OnEvent(channel string, fn func(payload any)) func() {
	off, err := h.ctx.Events().On(channel, func(evCtx sdk.Context, data any) error {
		h.c.withCtx(evCtx, func() { fn(data) })
		return nil
	})
	if err != nil {
		return func() {}
	}
	return off
}

// sdkTheme is ctx.ui.theme as the goal components use it.
type sdkTheme struct{ t sdk.UITheme }

func (s sdkTheme) fg(color, text string) string { return s.t.Fg(color, text) }
func (s sdkTheme) bg(color, text string) string { return s.t.Bg(color, text) }
func (s sdkTheme) bold(text string) string      { return s.t.Bold(text) }

// remoteDialog runs a goalDialogComponent as the SDK's remote component: input goes to the component, which closes the dialog by
// calling done. It is also the TUI object the factory gets: a render request invalidates the overlay.
type remoteDialog struct {
	comp       goalDialogComponent
	done       bool
	value      any
	invalidate func()
}

func (r *remoteDialog) Render(width int) []string { return r.comp.Render(width) }
func (r *remoteDialog) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	r.comp.HandleInput(data)
	return sdk.RemoteComponentResult{Done: r.done, Value: r.value}, nil
}
func (r *remoteDialog) SetInvalidate(f func()) { r.invalidate = f }
func (r *remoteDialog) Dispose()               { r.comp.dispose() }

func (r *remoteDialog) getShowHardwareCursor() bool { return false }
func (r *remoteDialog) setShowHardwareCursor(bool)  {}
func (r *remoteDialog) requestRender() {
	if r.invalidate != nil {
		r.invalidate()
	}
}
func (r *remoteDialog) terminalRows() (int, bool)     { return 0, false }
func (r *remoteDialog) previousLinesLen() (int, bool) { return 0, false }

func overlaySize(s string) *sdk.OverlaySize {
	if s == "" {
		return nil
	}
	if p, ok := strings.CutSuffix(s, "%"); ok {
		if f, err := strconv.ParseFloat(p, 64); err == nil {
			return sdk.OverlayPercent(f)
		}
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return sdk.OverlayCells(n)
	}
	return nil
}

func remoteOverlayOptions(o goalCustomOptions) any {
	if !o.overlay {
		return nil
	}
	return sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{
		Anchor: o.anchor, MinWidth: o.minWidth, Width: overlaySize(o.width), MaxHeight: overlaySize(o.maxHeight),
	}}
}

// sdkModelRegistry is ctx.modelRegistry over the SDK's registry.
type sdkModelRegistry struct{ r sdk.ModelRegistry }

func modelOf(m map[string]any) *auditorModel {
	provider, _ := m["provider"].(string)
	id, _ := m["id"].(string)
	name, _ := m["name"].(string)
	return &auditorModel{provider: provider, id: id, name: name}
}

func (s sdkModelRegistry) find(provider, id string) *auditorModel {
	all, err := s.r.GetAll()
	if err != nil {
		return nil
	}
	for _, m := range all {
		if got := modelOf(m); got.provider == provider && got.id == id {
			return got
		}
	}
	return nil
}

func (s sdkModelRegistry) getAvailable() []*auditorModel {
	all, err := s.r.GetAvailable()
	if err != nil {
		return nil
	}
	out := make([]*auditorModel, 0, len(all))
	for _, m := range all {
		out = append(out, modelOf(m))
	}
	return out
}

func (s sdkModelRegistry) runtime() any { return nil }
