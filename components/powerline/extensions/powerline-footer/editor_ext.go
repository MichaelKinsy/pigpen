package powerline_footer

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The powerline editor: the SDK wiring of setupCustomEditor's editorFactory (index.ts 3230-3500) for what is ported, installed
// with Context.SetEditorComponent (PiG v0.5.0). It wraps the host's default editor: printable keys go straight to it, Ctrl+S and
// the powerline shortcuts are handled here (the original handles them only where the editor has focus, so Pi's selectors keep
// Ctrl+S), and render draws the chrome and the fast path of editor_chrome.go. The queue's submit and compaction hooks, the bash
// mode editor and the profiler wrap the same component in the slices that port them.
//
// Not reachable through the SDK: Pi's KeybindingsManager (keys are compared with Pi's default bindings), the editor's onSubmit
// property, and getEditorComponent of another extension's editor (the original passes the previous editor's autocomplete
// provider through; the SDK returns only this extension's own factory, so there is no previous editor to read).

// powerlineBase is the default editor as the powerline editor uses it; *sdk.Editor implements it.
type powerlineBase interface {
	bashEditorBase
	AddToHistory(text string) error
}

type powerlineEditor struct {
	*sdk.Editor // inherits HandleMouse, SetText, InsertTextAtCursor and AddToHistory
	base        powerlineBase
	mu          sync.Mutex          // one input at a time reaches the default editor: a key, or a delayed prompt being sent
	bash        *bashModeEditor     // nil until the bash slice is wired in (internal tests)
	perf        *editorPerfProfiler // nil unless POWERLINE_DEBUG_PERF=1
	ctx         sdk.Context
	k           *keysExt
	border      editorBorderColor
}

func (e *powerlineEditor) HandleInput(data string) error {
	if e.perf == nil {
		return e.handleInput(data)
	}
	err := measureEditorPerf(e.perf, "input.total", func() error { return e.handleInput(data) })
	if lines, lerr := e.base.GetLines(); lerr == nil {
		e.perf.observeDraft(lines)
	}
	return err
}

func (e *powerlineEditor) handleInput(data string) error {
	e.k.dismiss(e.ctx) // dismissWelcomeForInput
	if isPrintableInput(data) {
		return e.pass(data)
	}
	switch e.queueKey(data) {
	case editorHandled:
		return nil
	case editorPassThrough:
		return e.pass(data)
	}
	if e.k.editorInput(e.ctx, data) {
		return nil
	}
	return e.pass(data)
}

// pass is originalHandleInput: BashModeEditor.handleInput over the default editor.
func (e *powerlineEditor) pass(data string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.bash != nil {
		return e.bash.handleInput(data)
	}
	return e.base.HandleInput(data)
}

func (e *powerlineEditor) Render(width int) ([]string, error) {
	completions := e.k.ensure(e.ctx).completionsEnabled()
	var base editorBase = e.base
	active := false
	if e.bash != nil {
		base = ghostBase{base, e.bash}
		active = e.bash.env.active()
	}
	options := editorPerfOptions{EditorChrome: true, FastRender: true}
	if e.k.a != nil && e.k.a.perf != nil {
		options = e.k.a.perf.options
	}
	render := func() ([]string, error) {
		return renderPowerlineEditorWith(base, &e.border, width, active, completions, editorRenderOptions{chrome: options.EditorChrome, fast: options.FastRender, perf: e.perf})
	}
	if e.perf == nil {
		return render()
	}
	return measureEditorPerf2(e.perf, "editor.render.total", render)
}

// queueKey is the queue's part of handleInput (index.ts 3335-3384): Esc takes a delayed prompt back, /compact starts a compaction
// the queue knows of, a prompt submitted during a compaction is queued for after it, and a prompt submitted under
// powerline.sendDelayMs waits.
func (e *powerlineEditor) queueKey(data string) editorVerdict {
	a := e.k.a
	if a == nil {
		return editorContinue
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pl == nil {
		return editorContinue
	}
	bashActive := e.bash != nil && e.bash.env.active()
	return a.pl.queue.editorKey(editorQueueAdapter{e}, data, bashActive)
}

// editorQueueAdapter is the editor as the queue sees it (queueKeyEditor).
type editorQueueAdapter struct{ e *powerlineEditor }

func (a editorQueueAdapter) GetExpandedText() string {
	text, _ := a.e.base.GetExpandedText()
	return text
}
func (a editorQueueAdapter) SetText(text string)      { _ = a.e.base.SetText(text) }
func (a editorQueueAdapter) AddToHistory(text string) { _ = a.e.base.AddToHistory(text) }
func (a editorQueueAdapter) ShowingAutocomplete() bool {
	v, _ := a.e.base.IsShowingAutocomplete()
	return v
}

// SubmitText sends a delayed prompt through Pi's own submit path: its text goes into the editor and Enter is pressed there. A
// draft typed during the countdown is put back afterwards.
func (a editorQueueAdapter) SubmitText(text string) {
	a.e.mu.Lock()
	defer a.e.mu.Unlock()
	draft, _ := a.e.base.GetExpandedText()
	_ = a.e.base.SetText(text)
	_ = a.e.base.HandleInput("\r")
	if jsTrim(draft) != "" {
		_ = a.e.base.SetText(draft)
	}
}

// installEditor installs the powerline editor; with no UI or in RPC mode the SDK ignores it, as Pi does.
func (k *keysExt) installEditor(ctx sdk.Context) {
	var bash *bashExt
	if k.a != nil {
		bash = k.a.bash
	}
	_ = ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent {
		var pb powerlineBase = base
		var perf *editorPerfProfiler
		if k.a != nil && k.a.perf != nil && k.a.perf.options.Enabled {
			perf = k.a.perf
			pb = perfBase{base, perf}
		}
		e := &powerlineEditor{Editor: base, base: pb, ctx: ctx, k: k, perf: perf}
		if k.a != nil && k.a.pl != nil {
			k.a.pl.queue.editor = editorQueueAdapter{e} // called with a.mu held, from the core's setup
		}
		if bash != nil {
			mode := bash.bind(ctx)
			e.bash = newBashModeEditor(pb, bash.editorEnv(ctx, mode, k.ensure(ctx)))
			mode.setEditorHooks(bashEditorHooks{dismissBashModeUi: e.bash.dismissBashModeUi, refreshGhostSuggestion: e.bash.refreshGhostSuggestion})
		}
		return e
	})
}

// uninstallEditor restores the host's editor (ctx.ui.setEditorComponent(undefined)).
func (k *keysExt) uninstallEditor(ctx sdk.Context) {
	if k.a != nil && k.a.pl != nil {
		k.a.pl.queue.editor = nil
	}
	if k.a != nil && k.a.bash != nil {
		if mode := k.a.bash.current(); mode != nil {
			mode.setEditorHooks(bashEditorHooks{})
		}
	}
	_ = ctx.SetEditorComponent(nil)
}

// editorInput is the shortcut part of the editor's handleInput: Ctrl+S and the stash key restore or stash the draft, a
// powerline shortcut runs its action. It reports whether the key was consumed.
func (k *keysExt) editorInput(ctx sdk.Context, data string) bool {
	if !k.enabled.Load() || !ctx.HasUI() {
		return false
	}
	return k.ensure(ctx).editorInput(k.host(ctx), data)
}

// editorInput is the stash and shortcut branch of the editor's handleInput (index.ts 3388-3398).
func (k *keysState) editorInput(h keysHost, data string) bool {
	if k.isStashShortcutInput(data) || matchesCtrlSStashInput(data) {
		k.stashOrRestoreEditorText(h)
		return true
	}
	action := k.shortcutAction(h, data)
	if action == "" {
		return false
	}
	if action == actionCopyEditor || action == actionCutEditor {
		k.run(h, action)
	} else {
		go k.run(h, action)
	}
	return true
}

// completionsEnabled is bashModeSettings.completions.
func (k *keysState) completionsEnabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.bash.Completions
}
