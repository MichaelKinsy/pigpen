package powerline_footer

import (
	"context"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The welcome lifecycle: when the header or the startup overlay is prepared, shown and dismissed.
// upstream: index.ts 2184-2202 (dismissWelcome, dismissWelcomeForInput), 3539-3666 (beginWelcomeRequest, canShowWelcome,
// setupWelcomeHeader, setupWelcomeOverlay), the session_start decision (1882-1890), and the dismissals in session_shutdown,
// agent_start, tool_call, the /powerline toggle and the editor's input path.
//
// Pi's editor is not reachable from a Go extension, so the overlay differs from the original in one place, on purpose: it is a
// non-capturing overlay that leaves keyboard focus in the editor. Every key then reaches the real editor, which is what the
// original does by forwarding the overlay's first key to currentEditor.handleInput, and a terminal-input listener (which only
// observes, it never consumes) dismisses the overlay on that key. See the README of this slice in PORT.md for the full account.

// welcomeTimers is the clock of the lifecycle (setTimeout and setInterval). The tests replace it.
type welcomeTimers interface {
	// After runs f once after d; cancel stops it (clearTimeout).
	After(d time.Duration, f func()) (cancel func())
	// Every runs f every d until cancel (setInterval, clearInterval).
	Every(d time.Duration, f func()) (cancel func())
}

type realWelcomeTimers struct{}

func (realWelcomeTimers) After(d time.Duration, f func()) func() {
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

func (realWelcomeTimers) Every(d time.Duration, f func()) func() {
	tk := time.NewTicker(d)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-tk.C:
				f()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { tk.Stop(); close(done) }) }
}

var welcomeClock welcomeTimers = realWelcomeTimers{}

// welcomeAfterHeaderMark, when set, runs between the moment the header is marked active and the moment it is installed. The tests
// dismiss the welcome there.
var welcomeAfterHeaderMark func()

// welcomeWatchHook, when set, is told when the welcome starts (true) and stops (false) watching the keyboard. The SDK shares one
// host subscription between all listeners of the extension, so the host's on/off calls do not show the welcome's own watch.
var welcomeWatchHook func(on bool)

// welcomeBeforeHeaderInstall, when set, runs after the install was checked and before it is made, with the header lock held.
var welcomeBeforeHeaderInstall func()

// welcomeRequest is the original's AbortController of one welcome attempt.
type welcomeRequest struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// welcomeState is the original's welcomeRequest, welcomeTimer, dismissWelcomeOverlay, welcomeHeaderActive and the generation of the
// session, with the terminal-input subscription that stands in for the editor's input path. mu guards the fields; host calls are
// never made under it (a terminal-input listener must not wait for the host, which may be waiting for its verdict).
type welcomeState struct {
	mu           sync.Mutex
	hdrMu        sync.Mutex // orders SetHeader calls: an install and a clear are applied in the order the state changed
	ctx          sdk.Context
	request      *welcomeRequest
	cancelTimer  func()
	overlay      *welcomeOverlay
	headerActive bool
	generation   int
	unsubInput   func()
}

// welcomeOverlay is the startup overlay's component (sdk.RemoteComponent) and its state. The view fields are guarded by viewMu (the
// SDK renders on its own goroutine); the rest by welcomeState.mu.
type welcomeOverlay struct {
	a   *app
	req *welcomeRequest

	viewMu     sync.Mutex
	comp       *welcomeComponent
	invalidate func()

	countdown  int
	cancelTick func()
	handle     *sdk.OverlayHandle
	dismissed  bool
}

func (o *welcomeOverlay) Render(width int) []string {
	o.viewMu.Lock()
	defer o.viewMu.Unlock()
	return o.comp.render(width)
}

func (o *welcomeOverlay) SetInvalidate(fn func()) {
	o.viewMu.Lock()
	o.invalidate = fn
	o.viewMu.Unlock()
}

// HandleInput is the original's handleInput: any key dismisses, and a key that is not a release goes on to the editor. It runs only
// when a host gives the overlay keyboard focus, which a non-capturing overlay does not take; then the editor never saw the key, so
// the text is pasted into it (the only editor input a Go extension has).
func (o *welcomeOverlay) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	w := &o.a.welcome
	w.mu.Lock()
	ctx := w.ctx
	fx := w.dismissOverlayLocked(o)
	w.mu.Unlock()
	runWelcomeEffects(fx, false)
	if !welcomeIsKeyRelease(data) {
		if text, ok := welcomeEditorText(data); ok {
			ctx.PasteToEditor(text)
		}
	}
	return sdk.RemoteComponentResult{Done: true}, nil
}

// Dispose is the original's `dispose: dismiss`.
func (o *welcomeOverlay) Dispose() {
	w := &o.a.welcome
	w.mu.Lock()
	fx := w.dismissOverlayLocked(o)
	w.mu.Unlock()
	runWelcomeEffects(fx, false)
}

// welcomeEditorText is the text of a chunk the editor would insert: printable characters or a bracketed paste.
func welcomeEditorText(data string) (string, bool) {
	if inner, ok := strings.CutPrefix(data, "\x1b[200~"); ok {
		if inner, ok := strings.CutSuffix(inner, "\x1b[201~"); ok {
			return inner, inner != ""
		}
		return "", false
	}
	if data == "" {
		return "", false
	}
	for _, r := range data {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "", false
		}
	}
	return data, true
}

// welcomeIsKeyRelease is pi-tui's isKeyRelease: a Kitty key-release chunk. Bracketed paste is never one.
func welcomeIsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, s := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, s) {
			return true
		}
	}
	return false
}

func runWelcomeEffects(fx []func(), async bool) {
	if len(fx) == 0 {
		return
	}
	run := func() {
		for _, f := range fx {
			f()
		}
	}
	if async {
		go run()
		return
	}
	run()
}

// dismissOverlayLocked is the overlay's own dismiss(): stop the countdown, abort its request, forget it and hide it. The returned
// effects are the host calls to make after mu is released.
func (w *welcomeState) dismissOverlayLocked(o *welcomeOverlay) []func() {
	if o.dismissed {
		return nil
	}
	o.dismissed = true
	if o.cancelTick != nil {
		o.cancelTick()
		o.cancelTick = nil
	}
	o.req.cancel()
	if w.request == o.req {
		w.request = nil
	}
	if w.overlay == o {
		w.overlay = nil
	}
	var fx []func()
	if h := o.handle; h != nil {
		fx = append(fx, func() { _ = h.Hide() })
	}
	if u := w.unsubInput; u != nil && w.request == nil && !w.headerActive {
		w.unsubInput = nil
		fx = append(fx, u)
	}
	return fx
}

// dismissLocked is dismissWelcome: abort the request, stop the timer, dismiss the overlay, clear the header.
func (w *welcomeState) dismissLocked() []func() {
	var fx []func()
	if w.request != nil {
		w.request.cancel()
		w.request = nil
	}
	if w.cancelTimer != nil {
		w.cancelTimer()
		w.cancelTimer = nil
	}
	if o := w.overlay; o != nil {
		fx = append(fx, w.dismissOverlayLocked(o)...)
	}
	if w.headerActive {
		w.headerActive = false
		ctx := w.ctx
		fx = append(fx, func() {
			w.hdrMu.Lock()
			defer w.hdrMu.Unlock()
			_ = ctx.SetHeader(nil)
		})
	}
	if u := w.unsubInput; u != nil {
		w.unsubInput = nil
		fx = append(fx, u)
	}
	return fx
}

// welcomeDismiss is dismissWelcome(ctx).
func (a *app) welcomeDismiss() {
	w := &a.welcome
	w.mu.Lock()
	fx := w.dismissLocked()
	w.mu.Unlock()
	runWelcomeEffects(fx, false)
}

// welcomeEndSession is the part of session_shutdown: a new generation, then dismissWelcome.
func (a *app) welcomeEndSession() {
	a.welcome.mu.Lock()
	a.welcome.generation++
	a.welcome.mu.Unlock()
	a.welcomeDismiss()
}

// welcomeBeginSession is the start of session_start: dismissWelcome, then a new generation.
func (a *app) welcomeBeginSession() {
	a.welcomeDismiss()
	a.welcome.mu.Lock()
	a.welcome.generation++
	a.welcome.mu.Unlock()
}

// welcomeOverlayMounted reports whether the startup overlay is on screen (what tui.hasOverlay() says for it in the original).
func (a *app) welcomeOverlayMounted() bool {
	a.welcome.mu.Lock()
	defer a.welcome.mu.Unlock()
	return a.welcome.overlay != nil
}

// welcomeInput observes every terminal chunk. It stands in for the editor's input path, which dismisses the welcome on any input
// (dismissWelcomeForInput) and, for the overlay, for the overlay's own handleInput. It never consumes a chunk and never calls the
// host while the host waits for its verdict.
func (a *app) welcomeInput(data string) sdk.TerminalInputResult {
	w := &a.welcome
	w.mu.Lock()
	if w.request == nil && w.overlay == nil && !w.headerActive {
		w.mu.Unlock()
		return sdk.TerminalInputResult{}
	}
	// The overlay has wantsKeyRelease: a release dismisses it. The editor never sees a release.
	if w.overlay == nil && welcomeIsKeyRelease(data) {
		w.mu.Unlock()
		return sdk.TerminalInputResult{}
	}
	fx := w.dismissLocked()
	w.mu.Unlock()
	runWelcomeEffects(fx, true)
	return sdk.TerminalInputResult{}
}

// welcomeBegin is beginWelcomeRequest: dismiss what is there and start a request, which also starts watching the keyboard.
func (a *app) welcomeBegin(ctx sdk.Context) *welcomeRequest {
	a.welcomeDismiss()
	rctx, cancel := context.WithCancel(context.Background())
	req := &welcomeRequest{ctx: rctx, cancel: cancel}
	w := &a.welcome
	w.mu.Lock()
	w.ctx = ctx
	w.request = req
	w.mu.Unlock()
	unsub, err := ctx.OnTerminalInput(a.welcomeInput)
	if err != nil {
		return req
	}
	if hook := welcomeWatchHook; hook != nil {
		hook(true)
		stop := unsub
		unsub = func() { stop(); hook(false) }
	}
	w.mu.Lock()
	if w.request == req {
		w.unsubInput = unsub
		unsub = nil
	}
	w.mu.Unlock()
	if unsub != nil {
		unsub() // dismissed while subscribing
	}
	return req
}

// welcomeCanShow is canShowWelcome. The caller holds a.mu (the core's state is read).
func (a *app) welcomeCanShow(ctx sdk.Context, req *welcomeRequest, generation int) bool {
	w := &a.welcome
	w.mu.Lock()
	current := w.request == req && w.generation == generation
	w.mu.Unlock()
	pl := a.pl
	if !current || req.ctx.Err() != nil || !pl.enabled || !pl.config.Welcome || !ctx.HasUI() || pl.isStreaming {
		return false
	}
	text, err := ctx.GetEditorText()
	if err != nil || text != "" {
		return false
	}
	var events []map[string]any
	if s := pl.h.Session(); s != nil {
		if events, err = s.GetBranch(nil); err != nil {
			return false // the original's getBranch would throw into its catch
		}
	}
	for _, entry := range events {
		switch entry["type"] {
		case "tool_call", "tool_result":
			return false
		case "message":
			if msg := asMap(entry["message"]); msg != nil && msg["role"] == "assistant" {
				return false
			}
		}
	}
	return true
}

// welcomeModelNames is `ctx.model?.name || ctx.model?.id || "No model"` and `ctx.model?.provider || "Unknown"`.
func welcomeModelNames(m *modelInfo) (modelName, providerName string) {
	modelName, providerName = "No model", "Unknown"
	if m == nil {
		return
	}
	if m.Name != "" {
		modelName = m.Name
	} else if m.ID != "" {
		modelName = m.ID
	}
	if m.Provider != "" {
		providerName = m.Provider
	}
	return
}

// welcomeCollect is the body both timers share: check, read the recent sessions (no lock held, abortable), check again, then
// gather the counts and the estimate. names, when set, are the model and provider read at setup (the overlay reads them there).
func (a *app) welcomeCollect(ctx sdk.Context, req *welcomeRequest, generation int, names *[2]string) (welcomeData, bool) {
	w := &a.welcome
	a.mu.Lock()
	w.mu.Lock()
	w.cancelTimer = nil
	w.mu.Unlock()
	ok := a.welcomeCanShow(ctx, req, generation)
	a.mu.Unlock()
	if !ok {
		return welcomeData{}, false
	}
	recent, err := getRecentSessions(req.ctx, 3)
	if err != nil {
		return welcomeData{}, false // aborted (dismissed or shut down); anything else is only a debug line in the original
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.welcomeCanShow(ctx, req, generation) {
		return welcomeData{}, false
	}
	var modelName, providerName string
	if names != nil {
		modelName, providerName = names[0], names[1]
	} else {
		modelName, providerName = welcomeModelNames(a.pl.h.Model())
	}
	commands, err := ctx.GetCommands()
	if err != nil {
		return welcomeData{}, false
	}
	slash := make([]slashCommand, len(commands))
	for i, c := range commands {
		slash[i] = slashCommand{c.Name, c.Source}
	}
	counts := discoverLoadedCounts(slash)
	var tokens *float64
	if prompt, err := ctx.GetSystemPrompt(); err == nil {
		if n := estimateInitialContextTokens(&prompt); n != nil {
			f := float64(*n)
			tokens = &f
		}
	}
	return welcomeData{modelName, providerName, recent, counts, tokens}, true
}

// welcomeSetupHeader is setupWelcomeHeader.
func (a *app) welcomeSetupHeader(ctx sdk.Context) {
	req := a.welcomeBegin(ctx)
	w := &a.welcome
	w.mu.Lock()
	generation := w.generation
	// Keep optional archive discovery off the session_start completion path.
	w.cancelTimer = welcomeClock.After(0, func() { a.welcomeHeaderWork(ctx, req, generation) })
	w.mu.Unlock()
}

func (a *app) welcomeHeaderWork(ctx sdk.Context, req *welcomeRequest, generation int) {
	data, ok := a.welcomeCollect(ctx, req, generation, nil)
	if !ok {
		return
	}
	header := newWelcomeHeader(data.ModelName, data.ProviderName, data.RecentSessions, data.LoadedCounts, data.InitialContextTokens)
	w := &a.welcome
	w.mu.Lock()
	if w.request != req || req.ctx.Err() != nil || w.generation != generation {
		w.mu.Unlock()
		return
	}
	w.headerActive = true
	w.mu.Unlock()
	if welcomeAfterHeaderMark != nil {
		welcomeAfterHeaderMark()
	}
	w.hdrMu.Lock()
	defer w.hdrMu.Unlock()
	w.mu.Lock()
	still := w.headerActive
	w.mu.Unlock()
	if welcomeBeforeHeaderInstall != nil {
		welcomeBeforeHeaderInstall()
	}
	if still {
		_ = ctx.SetHeaderRenderer(header.render)
	}
}

// welcomeSetupOverlay is setupWelcomeOverlay.
func (a *app) welcomeSetupOverlay(ctx sdk.Context) {
	req := a.welcomeBegin(ctx)
	modelName, providerName := welcomeModelNames(a.pl.h.Model())
	names := [2]string{modelName, providerName}
	w := &a.welcome
	w.mu.Lock()
	generation := w.generation
	// Small delay to let the host finish initialization.
	w.cancelTimer = welcomeClock.After(100*time.Millisecond, func() { a.welcomeOverlayWork(ctx, req, generation, &names) })
	w.mu.Unlock()
}

func (a *app) welcomeOverlayWork(ctx sdk.Context, req *welcomeRequest, generation int, names *[2]string) {
	data, ok := a.welcomeCollect(ctx, req, generation, names)
	if !ok {
		return
	}
	w := &a.welcome
	ov := &welcomeOverlay{a: a, req: req, countdown: 30,
		comp: newWelcomeComponent(data.ModelName, data.ProviderName, data.RecentSessions, data.LoadedCounts, data.InitialContextTokens)}
	w.mu.Lock()
	if w.request != req || req.ctx.Err() != nil || w.generation != generation {
		w.mu.Unlock()
		return
	}
	w.overlay = ov
	ov.cancelTick = welcomeClock.Every(time.Second, func() { a.welcomeTick(ov) })
	w.mu.Unlock()

	go func() {
		// void ctx.ui.custom(...).catch(console.debug): an error only ends the overlay.
		_, _ = ctx.Custom(ov, sdk.RemoteOverlayOptions{
			Overlay:        true,
			OverlayOptions: &sdk.OverlayOptions{NonCapturing: true},
			OnHandle: func(h *sdk.OverlayHandle) {
				w.mu.Lock()
				ov.handle = h
				hide := ov.dismissed
				w.mu.Unlock()
				if hide { // dismissed before the host mounted it
					_ = h.Hide()
				}
			},
		})
		ov.Dispose()
	}()
}

// welcomeTick is the countdown interval: one second less, repaint, dismiss at zero.
func (a *app) welcomeTick(ov *welcomeOverlay) {
	w := &a.welcome
	w.mu.Lock()
	if ov.dismissed {
		w.mu.Unlock()
		return
	}
	ov.countdown--
	left := ov.countdown
	ov.viewMu.Lock()
	ov.comp.setCountdown(left)
	invalidate := ov.invalidate
	ov.viewMu.Unlock()
	var fx []func()
	if left <= 0 {
		fx = w.dismissOverlayLocked(ov)
	}
	w.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	runWelcomeEffects(fx, false)
}

// welcomeSessionStart is the end of the session_start handler: the decision to show the welcome. The caller holds a.mu.
func (a *app) welcomeSessionStart(ctx sdk.Context, reason string) {
	pl := a.pl
	if !pl.enabled || !ctx.HasUI() {
		return
	}
	if shouldShowStartupWelcome(reason, pl.config.Welcome, ctx.Mode()) {
		if readSettings(ctx.Cwd())["quietStartup"] == true {
			a.welcomeSetupHeader(ctx)
		} else {
			a.welcomeSetupOverlay(ctx)
		}
	} else {
		a.welcomeDismiss()
	}
}

// registerWelcome registers the handlers only the welcome needs: tool_call and session_shutdown dismiss it.
func registerWelcome(e *sdk.Extension, a *app) {
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.welcomeDismiss()
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.welcomeEndSession()
		return nil, nil
	})
}
