package powerline_footer

import (
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of the keys slice: the terminal-input shortcut listener, /stash-history, and the stash's session lifecycle.
// upstream: index.ts setupCustomEditor (3190-3220: ctx.ui.onTerminalInput), registerCommand("stash-history") (2670), the stash
// parts of session_start and session_shutdown.
//
// Ctrl+S, which the original handles only inside the focused editor (index.ts 3388) so that Pi's selectors keep it, is handled
// by the powerline editor (editor_ext.go); the printable shortcuts are handled by the terminal listener.
// The reply, bashMode and queueOpen actions are filled by the features that own them (keysExt.setHook).

// keysHooks is what the core calls on the keys slice.
type keysHooks interface {
	sessionStart(reason string)
	setup()
	disable()
}

type keysExt struct {
	a *app

	mu    sync.Mutex
	state *keysState
	hooks map[string]func(sdk.Context)
	unsub func()

	// overlays counts the select overlays this slice has open: the original's listener stands down while `tui.hasOverlay()`,
	// which the SDK cannot ask, so overlays opened here are tracked and others are not seen.
	overlays atomic.Int32
	// overlayGuard, when set, also stands the listener down (for example while the welcome overlay is mounted). It runs on the
	// SDK's input goroutine and must not take a.mu: the host waits for the listener's verdict while a command holding a.mu may
	// wait for the host.
	overlayGuard func() bool
	// enabled mirrors the bar's `enabled` for the listener, which cannot take a.mu (see overlayGuard): true from setup until
	// disable.
	enabled atomic.Bool
	// dismissWelcome is dismissWelcomeForInput: run after a shortcut consumed a key.
	dismissWelcome func(ctx sdk.Context)
}

func registerKeys(e *sdk.Extension, a *app) {
	k := &keysExt{a: a, hooks: map[string]func(sdk.Context){}}
	a.keys = k
	e.Command("stash-history", "Open prompt history picker", k.stashHistoryCommand)
	e.OnSessionShutdown(k.onShutdown)
}

// setHook installs what a shortcut action runs ("queueOpen", "reply", "bashMode"). The action is recognised only once its hook is
// installed. fn runs off the input path and must not hold a.mu while it waits.
func (k *keysExt) setHook(action string, fn func(sdk.Context)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hooks[action] = fn
}

func (k *keysExt) ensure(ctx sdk.Context) *keysState {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state == nil {
		settings := readSettings(ctx.Cwd())
		k.state = newKeysState(settings, parsePowerlineConfig(settings["powerline"], presetNames).StashSharpSShortcut)
	}
	return k.state
}

// The keysHooks the core calls, with a.mu held.

func (k *keysExt) sessionStart(reason string) {
	ctx := k.a.host.ctx
	state := k.ensure(ctx)
	h := k.host(ctx)
	// The original resets the stash before it reads the settings.
	state.sessionStart(h, reason, ctx.HasUI())
	settings := readSettings(ctx.Cwd())
	state.mu.Lock()
	state.loadSettings(settings, k.a.pl.config.StashSharpSShortcut)
	state.mu.Unlock()
}

func (k *keysExt) setup() {
	ctx := k.a.host.ctx
	k.enabled.Store(true)
	k.unsubscribe()
	state := k.ensure(ctx)
	unsub, err := ctx.OnTerminalInput(k.onInput(ctx, state))
	if err != nil {
		return
	}
	k.mu.Lock()
	k.unsub = unsub
	k.mu.Unlock()
	k.installEditor(ctx)
}

func (k *keysExt) disable() {
	ctx := k.a.host.ctx
	k.enabled.Store(false)
	k.ensure(ctx).disable(k.host(ctx))
	k.unsubscribe()
	k.uninstallEditor(ctx)
}

func (k *keysExt) unsubscribe() {
	k.mu.Lock()
	unsub := k.unsub
	k.unsub = nil
	k.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

func (k *keysExt) onShutdown(ctx sdk.Context, data map[string]any) (any, error) {
	k.a.mu.Lock()
	defer k.a.mu.Unlock()
	k.mu.Lock()
	state := k.state
	k.mu.Unlock()
	if state != nil {
		reason, _ := data["reason"].(string)
		state.sessionShutdown(k.host(ctx), reason)
	}
	k.enabled.Store(false)
	k.unsubscribe()
	return nil, nil
}

// onInput is the listener of setupCustomEditor: it consumes the stash shortcut and the powerline shortcuts. The stash and the
// clipboard run inline, as in the original; the pickers wait for the user, so they run off the input path.
func (k *keysExt) onInput(ctx sdk.Context, state *keysState) sdk.TerminalInputHandler {
	return func(data string) sdk.TerminalInputResult {
		guarded := k.overlayGuard != nil && k.overlayGuard()
		if !k.enabled.Load() || !ctx.HasUI() || guarded || k.overlays.Load() > 0 {
			return sdk.TerminalInputResult{}
		}
		h := k.host(ctx)
		if state.isStashShortcutInput(data) {
			state.stashOrRestoreEditorText(h)
			k.dismiss(ctx)
			return sdk.TerminalInputResult{Consume: true}
		}
		action := state.shortcutAction(h, data)
		if action == "" {
			return sdk.TerminalInputResult{}
		}
		if action == actionCopyEditor || action == actionCutEditor {
			state.run(h, action)
		} else {
			go state.run(h, action)
		}
		k.dismiss(ctx)
		return sdk.TerminalInputResult{Consume: true}
	}
}

func (k *keysExt) dismiss(ctx sdk.Context) {
	k.mu.Lock()
	fn := k.dismissWelcome
	k.mu.Unlock()
	if fn != nil {
		fn(ctx)
	}
}

func (k *keysExt) stashHistoryCommand(ctx sdk.Context, _ string) error {
	if !ctx.HasUI() {
		return nil
	}
	k.a.mu.Lock()
	k.a.core(ctx)
	enabled := k.a.pl.enabled
	k.a.mu.Unlock()
	if !enabled {
		ctx.Notify("Powerline is disabled", "info")
		return nil
	}
	k.ensure(ctx).openStashHistory(k.host(ctx))
	return nil
}

// keysSDK is the keysHost over a Context.
type keysSDK struct {
	ctx sdk.Context
	k   *keysExt
}

func (k *keysExt) host(ctx sdk.Context) keysHost { return keysSDK{ctx, k} }

func (h keysSDK) Cwd() string { return h.ctx.Cwd() }
func (h keysSDK) SessionID() string {
	id, _ := h.ctx.GetSessionID()
	return id
}
func (h keysSDK) EditorText() string {
	text, _ := h.ctx.GetEditorText()
	return text
}
func (h keysSDK) SetEditorText(text string) { h.ctx.SetEditorText(text) }
func (h keysSDK) SetStatus(key string, text *string) {
	if text == nil {
		h.ctx.SetStatus(key, "")
		return
	}
	h.ctx.SetStatus(key, *text)
}
func (h keysSDK) Notify(message, level string) { h.ctx.Notify(message, level) }
func (h keysSDK) Select(title string, options []string) string {
	selected, ok, err := h.ctx.Select(title, options)
	if err != nil || !ok {
		return ""
	}
	return selected
}
func (h keysSDK) SelectOverlay(title, hint string, items []selectItem, maxVisible int) *selectItem {
	h.k.overlays.Add(1)
	defer h.k.overlays.Add(-1)
	selected, err := showSelectOverlay(h.ctx, title, hint, items, maxVisible)
	if err != nil {
		return nil
	}
	return selected
}
func (h keysSDK) CopyToClipboard(text string) error {
	return copyToClipboard(execClipboard{h.ctx}, osClipboardEnv(), text)
}
func (h keysSDK) Hook(action string) func() {
	h.k.mu.Lock()
	fn := h.k.hooks[action]
	h.k.mu.Unlock()
	if fn == nil {
		return nil
	}
	return func() { fn(h.ctx) }
}

// execClipboard runs clipboard commands through the host's exec.
type execClipboard struct{ ctx sdk.Context }

func (e execClipboard) run(command string, args []string, input string, timeoutMs float64) (string, bool) {
	name, argv := command, args
	if input != "" {
		name, argv = "sh", append([]string{"-c", clipboardScript(input, osClipboardEnv().goos), command}, args...)
	}
	res, err := e.ctx.ExecWithOptions(name, argv, sdk.ExecOptions{Timeout: timeoutMs})
	if err != nil || res == nil || res.ExitCode != 0 || res.Killed {
		return "", false
	}
	return res.Stdout, true
}
