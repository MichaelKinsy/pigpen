package tintinweb_subagents

import (
	"fmt"
	"sort"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The runtime of the subagent surfaces: it feeds the widget, the fleet list and the conversation viewer from the
// manager's records and the children's event streams, and draws them through the SDK (SetWidget, OnTerminalInput,
// Custom). upstream: the widget/fleet wiring of src/index.ts.

type uiHub struct {
	app *app

	mu       sync.Mutex
	agents   map[string]*uiAgent
	settings uiSettings
	// widthBound: the width-change subscription is taken; dispose clears it, so the next session subscribes again.
	widthBound bool
	unsubWidth func()

	widget *agentWidget
	fleet  *fleetList
	ui     *ctxUI
}

func newUIHub(a *app) *uiHub {
	h := &uiHub{app: a, agents: map[string]*uiAgent{}, settings: defaultUISettings()}
	h.ui = &ctxUI{hub: h, widgets: map[string]*uiWidgetState{}}
	h.widget = newAgentWidget(h, liveActivities{h}, h.getWidgetMode, h.getShowCost, h.getShowModel)
	h.fleet = newFleetList(h, liveActivities{h}, h.getShowCost, h.getViewerMarkdown, h.chooseViewerMarkdown)
	return h
}

func (h *uiHub) get() uiSettings { h.mu.Lock(); defer h.mu.Unlock(); return h.settings }

func (h *uiHub) getWidgetMode() widgetMode             { return h.get().widgetMode }
func (h *uiHub) getShowCost() bool                     { return h.get().showCost }
func (h *uiHub) getShowModel() bool                    { return h.get().showModel }
func (h *uiHub) getViewerMarkdown() viewerMarkdownMode { return h.get().viewerMarkdown }

// chooseViewerMarkdown is the viewer's `m` key: set the mode and persist it.
func (h *uiHub) chooseViewerMarkdown(mode viewerMarkdownMode) {
	h.mu.Lock()
	h.settings.viewerMarkdown = mode
	snapshot := h.settings
	h.mu.Unlock()
	h.app.mu.Lock()
	cwd := h.app.cwd
	h.app.mu.Unlock()
	if cwd == "" {
		return
	}
	persisted := persistViewerMarkdown(cwd, mode)
	ctx, ok := h.app.context()
	if !ok {
		return
	}
	// The event fires whatever the write's outcome, so listeners see the in-memory change.
	_ = ctx.Events().Emit("subagents:settings_changed", map[string]any{"settings": snapshot.asMap(), "persisted": persisted})
	// Silent on success (the overlay is the feedback); a failed write still speaks.
	if !persisted {
		ctx.Notify("Viewer markdown set to "+string(mode)+" (session only; failed to persist)", "warning")
	}
}

// loadSettings applies the display settings of a workspace.
func (h *uiHub) loadSettings(cwd string) {
	s := loadUISettings(cwd)
	h.mu.Lock()
	h.settings = s
	h.mu.Unlock()
	h.fleet.setEnabled(s.fleetView)
	h.widget.update()
}

// bind hands the surfaces the UI on every context that has one: a list disposed with its session is bound again by
// the next session's first event, as the original's setUICtx is called on every session_start and tool_execution_start
// (setUI is a no-op for a UI the surface already holds).
func (h *uiHub) bind(ctx sdk.Context) {
	if !safeHasUI(ctx) {
		return
	}
	h.widget.setUI(h.ui)
	h.fleet.setUI(h.ui)
	h.mu.Lock()
	subscribe := !h.widthBound
	h.widthBound = true
	h.mu.Unlock()
	if !subscribe {
		return
	}
	var unsub func()
	guard(func() {
		unsub, _ = ctx.OnWidthChange(func(sdk.Context, int) { safeCall("width change", h.ui.RequestRender) })
	})
	h.mu.Lock()
	h.unsubWidth = unsub
	h.mu.Unlock()
}

func safeHasUI(ctx sdk.Context) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ctx.HasUI()
}

// ---- agentLister / fleetManager / activitySource ----

// liveActivities is the activitySource of the surfaces: an agent's activity while it runs. upstream: the activity Map
// the widget and the fleet list read, from which index.ts deletes an entry the moment its agent finishes.
type liveActivities struct{ h *uiHub }

func (l liveActivities) activityOf(id string) *agentActivity {
	l.h.mu.Lock()
	u := l.h.agents[id]
	l.h.mu.Unlock()
	if u == nil || u.isSettled() {
		return nil
	}
	return u.snapshotActivity()
}

func (h *uiHub) listAgents() []*agentView {
	h.app.mgr.mu.Lock()
	var out []*agentView
	type pair struct {
		v *agentView
		u *uiAgent
	}
	var pairs []pair
	h.mu.Lock()
	for _, id := range h.app.mgr.order {
		r := h.app.mgr.records[id]
		v := &agentView{ID: r.ID, Type: r.Type, Description: r.Description, Status: r.Status, ToolUses: r.ToolUses,
			StartedAt: r.StartedAt.UnixMilli(), Error: r.Error}
		bg := r.Background
		v.IsBackground = &bg
		if !r.CompletedAt.IsZero() {
			v.CompletedAt = r.CompletedAt.UnixMilli()
		}
		pairs = append(pairs, pair{v, h.agents[r.ID]})
		out = append(out, v)
	}
	h.mu.Unlock()
	h.app.mgr.mu.Unlock()
	for _, p := range pairs {
		if p.u == nil {
			continue
		}
		p.u.mu.Lock()
		p.v.HasSession = p.u.hasSession
		p.v.Usage = p.u.usage
		p.v.CompactionCount = p.u.compactions
		if p.u.inv != nil {
			inv := *p.u.inv
			p.v.Invocation = &inv
		}
		if p.v.Status == statusRunning && p.u.act.ToolUses > p.v.ToolUses {
			p.v.ToolUses = p.u.act.ToolUses
		}
		p.u.mu.Unlock()
	}
	// Newest first, as the manager lists them.
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// activityOf is the activity of an agent, running or settled (the final result and notification read the turn count and
// limit after the agent has finished; upstream holds them in the state object the closure keeps).
func (h *uiHub) activityOf(id string) *agentActivity {
	h.mu.Lock()
	u := h.agents[id]
	h.mu.Unlock()
	if u == nil {
		return nil
	}
	return u.snapshotActivity()
}

func (h *uiHub) abort(id string) bool {
	r := h.app.mgr.get(id)
	return r != nil && h.app.mgr.abort(r)
}

func (h *uiHub) steer(id, message string) error {
	r := h.app.mgr.get(id)
	if r == nil {
		return fmt.Errorf("Agent %s is not running.", id)
	}
	// The steer is a round trip to the child; the viewer does not wait for it (the original ignores its promise).
	safeGo("viewer steer", func() { _ = h.app.mgr.steer(r, message) })
	return nil
}

func (h *uiHub) sessionOf(id string) viewerSession {
	h.mu.Lock()
	u := h.agents[id]
	h.mu.Unlock()
	if u == nil {
		return emptySession{}
	}
	return u
}

// ---- spawn / settle hooks ----

// newObserver is the state of an agent about to spawn; its events feed the surfaces.
func (h *uiHub) newObserver(inv *invocation, maxTurns int) *uiAgent {
	var mt *int
	if maxTurns > 0 {
		mt = &maxTurns
	}
	u := newUIAgent(inv, mt)
	// The first event is the session coming up: the fleet list lists the agent from then on (upstream: the
	// session-created callback of a foreground agent, `fleet.ensureTimer(); fleet.update()`).
	u.onFirstEvent = func() { h.fleet.ensureTimer(); h.fleet.update() }
	return u
}

// attach registers a spawned agent with the surfaces and starts their timers.
func (h *uiHub) attach(r *agentRecord, u *uiAgent) {
	h.mu.Lock()
	h.agents[r.ID] = u
	h.mu.Unlock()
	h.widget.markRunning(r.ID)
	h.widget.ensureTimer()
	h.widget.update()
	h.fleet.ensureTimer()
	h.fleet.update()
}

// onSettle runs when an agent has settled: its activity is dropped, it lingers in the widget and the fleet list.
func (h *uiHub) onSettle(r *agentRecord) {
	h.mu.Lock()
	u := h.agents[r.ID]
	h.mu.Unlock()
	if u != nil {
		u.settle()
	}
	h.widget.markFinished(r.ID)
	h.fleet.onAgentFinished(r.ID)
	h.widget.update()
}

// onTurnStart ages the finished agents. upstream: widget.onTurnStart, from the tool_execution_start handler.
func (h *uiHub) onTurnStart() { h.widget.onTurnStart() }

// dispose tears the fleet list down (the session ends). The widget stays: upstream only disposes the list, and the next
// session's context binds both again.
func (h *uiHub) dispose() {
	h.fleet.dispose()
	h.mu.Lock()
	unsub := h.unsubWidth
	h.unsubWidth, h.widthBound = nil, false
	h.mu.Unlock()
	if unsub != nil {
		guard(unsub)
	}
}

// ---- the SDK adapter ----

type uiWidgetState struct {
	render    widgetRender
	placement string
	last      []string
	pushed    bool
}

// ctxUI draws the surfaces through the latest context: widgets as pre-rendered rows at the terminal width (a kit
// view, which the host lays out verbatim), input through OnTerminalInput, the viewer through Custom.
type ctxUI struct {
	hub *uiHub
	mu  sync.Mutex
	// widgets are the registered widgets.
	widgets map[string]*uiWidgetState
	// unsubInput is the OnTerminalInput subscription.
}

func (u *ctxUI) ctx() (ctx sdk.Context, ok bool) {
	return u.hub.app.context()
}

// guard runs f, swallowing the panic a context stale after a session replacement raises. It is for host calls only: a
// panic of this package's own code goes through safeCall, which reports it.
func guard(f func()) {
	defer func() { _ = recover() }()
	f()
}

func (u *ctxUI) SetStatus(key string, text *string) {
	ctx, ok := u.ctx()
	if !ok {
		return
	}
	guard(func() {
		if text == nil {
			ctx.SetStatus(key, "")
		} else {
			ctx.SetStatus(key, *text)
		}
	})
}

func (u *ctxUI) SetWidget(key string, render widgetRender, placement string) {
	u.mu.Lock()
	if render == nil {
		delete(u.widgets, key)
		u.mu.Unlock()
		if ctx, ok := u.ctx(); ok {
			guard(func() { _ = ctx.SetWidget(key, nil) })
		}
		return
	}
	u.widgets[key] = &uiWidgetState{render: render, placement: placement}
	u.mu.Unlock()
	u.push(key)
}

func (u *ctxUI) RequestRender() {
	u.mu.Lock()
	keys := make([]string, 0, len(u.widgets))
	for k := range u.widgets {
		keys = append(keys, k)
	}
	u.mu.Unlock()
	for _, k := range keys {
		u.push(k)
	}
}

// push renders a widget at the current width and sends it when its rows changed.
func (u *ctxUI) push(key string) {
	ctx, ok := u.ctx()
	if !ok {
		return
	}
	u.mu.Lock()
	w := u.widgets[key]
	u.mu.Unlock()
	if w == nil {
		return
	}
	var width int
	var theme uiTheme
	guard(func() {
		width = ctx.Width()
		if width <= 0 {
			width = 80
		}
		theme = sdkTheme{ctx.UITheme()}
	})
	if theme == nil {
		return
	}
	var lines []string
	if err := catch("widget "+key+" render", func() { lines = w.render(width, theme) }); err != nil {
		return // keep the rows on screen: a render bug must not blank or crash the widget
	}
	guard(func() {
		u.mu.Lock()
		same := w.pushed && equalLines(w.last, lines)
		w.last, w.pushed = lines, true
		u.mu.Unlock()
		if same {
			return
		}
		_ = ctx.SetWidget(key, kit.View{Root: kit.NewLines(lines)}, sdk.WidgetOptions{"placement": w.placement})
	})
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (u *ctxUI) OnTerminalInput(handler func(data string) *inputResult) func() {
	ctx, ok := u.ctx()
	if !ok {
		return func() {}
	}
	var unsub func()
	guard(func() {
		unsub, _ = ctx.OnTerminalInput(func(data string) (out sdk.TerminalInputResult) {
			// A panic must never swallow a key or end the process: the key passes through.
			safeCall("terminal input", func() {
				if r := handler(data); r != nil {
					out = sdk.TerminalInputResult{Consume: r.Consume}
				}
			})
			return out
		})
	})
	if unsub == nil {
		return func() {}
	}
	return unsub
}

func (u *ctxUI) GetEditorText() string {
	ctx, ok := u.ctx()
	if !ok {
		return ""
	}
	var text string
	guard(func() { text, _ = ctx.GetEditorText() })
	return text
}

func (u *ctxUI) Notify(message, level string) {
	if ctx, ok := u.ctx(); ok {
		guard(func() { ctx.Notify(message, level) })
	}
}

// Custom opens the viewer as an overlay and calls onClose once it has closed.
func (u *ctxUI) Custom(factory viewerFactory, opts overlayOptions, onClose func()) {
	ctx, ok := u.ctx()
	if !ok {
		onClose()
		return
	}
	overlay := &overlayComponent{ui: u}
	var done func() = overlay.close
	if err := catch("opening the viewer", func() { overlay.component = factory(overlay, sdkTheme{ctx.UITheme()}, nil, done) }); err != nil {
		u.Notify("Could not open the agent viewer: "+err.Error(), "error")
		onClose()
		return
	}
	go func() {
		defer onClose()
		guard(func() {
			_, _ = ctx.Custom(overlay, sdk.RemoteOverlayOptions{
				Overlay: true,
				OverlayOptions: &sdk.OverlayOptions{
					Anchor: opts.Anchor, Width: sdk.OverlayPercent(float64(opts.WidthPercent)), MaxHeight: sdk.OverlayPercent(float64(opts.HeightPercent)),
				},
				OnHandle: overlay.setHandle,
			})
		})
	}()
}

// overlayComponent adapts a viewerComponent to the SDK's RemoteComponent and is the viewer's host.
type overlayComponent struct {
	ui        *ctxUI
	component viewerComponent

	mu         sync.Mutex
	closing    bool
	inInput    bool
	handle     *sdk.OverlayHandle
	invalidate func()
}

func (o *overlayComponent) Rows() int {
	if ctx, ok := o.ui.ctx(); ok {
		rows := 0
		guard(func() { rows = ctx.Height() })
		if rows > 0 {
			return rows
		}
	}
	return 24
}

func (o *overlayComponent) RequestRender() {
	o.mu.Lock()
	f := o.invalidate
	o.mu.Unlock()
	if f != nil {
		f()
	}
}

func (o *overlayComponent) Render(width int) (lines []string) {
	if err := catch("viewer render", func() { lines = o.component.Render(width) }); err != nil {
		return []string{"(the agent viewer failed to render; Esc closes it)"}
	}
	return lines
}

func (o *overlayComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	o.mu.Lock()
	o.inInput = true
	o.mu.Unlock()
	if err := catch("viewer input", func() { o.component.HandleInput(data) }); err != nil {
		// The viewer is in an unknown state: close it rather than leave an overlay that cannot answer.
		o.mu.Lock()
		o.closing = true
		o.mu.Unlock()
	}
	o.mu.Lock()
	o.inInput = false
	closing := o.closing
	o.mu.Unlock()
	return sdk.RemoteComponentResult{Done: closing}, nil
}

func (o *overlayComponent) SetInvalidate(f func()) {
	o.mu.Lock()
	o.invalidate = f
	o.mu.Unlock()
}

func (o *overlayComponent) Dispose() { safeCall("viewer dispose", o.component.Dispose) }

func (o *overlayComponent) setHandle(h *sdk.OverlayHandle) {
	o.mu.Lock()
	o.handle = h
	closing := o.closing
	o.mu.Unlock()
	if closing {
		_ = h.Hide()
	}
}

// close is the overlay's done: from the viewer's own input it ends the component (HandleInput reports Done); from
// anywhere else (the list is disposed) the overlay is hidden through its handle.
func (o *overlayComponent) close() {
	o.mu.Lock()
	o.closing = true
	inInput, h := o.inInput, o.handle
	o.mu.Unlock()
	if !inInput && h != nil {
		guard(func() { _ = h.Hide() })
	}
}
