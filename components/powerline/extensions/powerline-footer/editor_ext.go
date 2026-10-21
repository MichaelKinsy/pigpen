package powerline_footer

import (
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

type powerlineEditor struct {
	*sdk.Editor // inherits HandleMouse, SetText, InsertTextAtCursor and AddToHistory
	base        editorBase
	ctx         sdk.Context
	k           *keysExt
	border      editorBorderColor
}

func (e *powerlineEditor) HandleInput(data string) error {
	e.k.dismiss(e.ctx) // dismissWelcomeForInput
	if isPrintableInput(data) {
		return e.base.HandleInput(data)
	}
	if e.k.editorInput(e.ctx, data) {
		return nil
	}
	return e.base.HandleInput(data)
}

func (e *powerlineEditor) Render(width int) ([]string, error) {
	completions := e.k.ensure(e.ctx).completionsEnabled()
	return renderPowerlineEditor(e.base, &e.border, width, false, completions)
}

// installEditor installs the powerline editor; with no UI or in RPC mode the SDK ignores it, as Pi does.
func (k *keysExt) installEditor(ctx sdk.Context) {
	_ = ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent {
		return &powerlineEditor{Editor: base, base: base, ctx: ctx, k: k}
	})
}

// uninstallEditor restores the host's editor (ctx.ui.setEditorComponent(undefined)).
func (k *keysExt) uninstallEditor(ctx sdk.Context) {
	_ = ctx.SetEditorComponent(nil)
}

// editorInput is the shortcut part of the editor's handleInput: Ctrl+S and the stash key restore or stash the draft, a
// powerline shortcut runs its action. It reports whether the key was consumed.
func (k *keysExt) editorInput(ctx sdk.Context, data string) bool {
	if !k.enabled.Load() || !ctx.HasUI() {
		return false
	}
	state := k.ensure(ctx)
	h := k.host(ctx)
	if state.isStashShortcutInput(data) || matchesCtrlSStashInput(data) {
		state.stashOrRestoreEditorText(h)
		return true
	}
	action := state.shortcutAction(h, data)
	if action == "" {
		return false
	}
	if action == actionCopyEditor || action == actionCutEditor {
		state.run(h, action)
	} else {
		go state.run(h, action)
	}
	return true
}

// completionsEnabled is bashModeSettings.completions.
func (k *keysState) completionsEnabled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.bash.Completions
}
