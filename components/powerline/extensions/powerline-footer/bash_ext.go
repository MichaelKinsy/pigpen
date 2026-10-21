package powerline_footer

import (
	"context"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of bash mode. upstream: index.ts 1354-1420 (the shell session and setBashModeActive), 1829-1867 and 1909-1915
// (the session lifecycle), 2593-2598 (/powerline off), 2683-2737 (/bash-mode, /bash-reset), 3154-3160 (the transcript widget),
// 3229-3320 (the editor and the mode-aware autocomplete provider). The logic is bash_mode.go; the editor's input handling is
// bash_editor.go.

// bashHooks is what the core calls on the bash slice, with a.mu held.
type bashHooks interface {
	sessionStart()
	powerlineDisabled()
	segmentData() shellSegmentData
	transcriptLines(width int, th theme) []string
}

const widgetBashTranscript = "powerline-bash-transcript"

type bashExt struct {
	a *app

	mu   sync.Mutex
	mode *bashMode
	ctx  sdk.Context // the latest handler's context: the shell's directory and the notifications follow it

	autocompleteOnce sync.Once
}

func newBashExt(a *app) *bashExt { return &bashExt{a: a} }

// registerBash wires the slice's commands and events (the slice's lines in Extension()).
func registerBash(e *sdk.Extension, a *app) {
	b := newBashExt(a)
	a.bash = b
	e.Command("bash-mode", "Toggle sticky bash mode (on, off, toggle)", b.modeCommand)
	e.Command("bash-reset", "Reset the managed bash session", b.resetCommand)
	e.OnSessionShutdown(b.onShutdown)
	a.keys.setHook(actionBashMode, b.toggle)
}

// bind records the context and returns the mode, created on first use with the settings' transcript limits.
func (b *bashExt) bind(ctx sdk.Context) *bashMode {
	b.mu.Lock()
	b.ctx = ctx
	mode := b.mode
	b.mu.Unlock()
	if mode != nil {
		return mode
	}
	limits := b.a.keys.ensure(ctx).bashLimits()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mode == nil {
		b.mode = newBashMode(bashModeEnv{
			Cwd:                 func() string { return b.context().Cwd() },
			ShellPath:           defaultShellPath,
			RequestStatusRender: b.scheduleRepaint,
			Notify:              func(message, level string) { b.context().Notify(message, level) },
		}, limits)
	}
	return b.mode
}

func (b *bashExt) context() sdk.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ctx
}

func (b *bashExt) current() *bashMode {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mode
}

// scheduleRepaint is requestStatusRender.
func (b *bashExt) scheduleRepaint() { b.a.scheduleRepaint() }

// The bashHooks the core calls.

func (b *bashExt) sessionStart() {
	ctx := b.a.host.ctx
	mode := b.bind(ctx)
	mode.sessionStart(b.a.keys.ensure(ctx).bashLimits())
	b.addAutocompleteProvider(ctx, mode)
}

func (b *bashExt) powerlineDisabled() {
	if mode := b.current(); mode != nil {
		mode.powerlineDisabled()
	}
}

func (b *bashExt) segmentData() shellSegmentData {
	if mode := b.current(); mode != nil {
		return mode.segmentData()
	}
	return shellSegmentData{}
}

func (b *bashExt) transcriptLines(width int, th theme) []string {
	mode := b.current()
	if mode == nil {
		return nil
	}
	lines, err := mode.renderBashTranscriptLines(width, th)
	if err != nil {
		return nil
	}
	return lines
}

// addAutocompleteProvider is attachAutocompleteProvider for the extension's autocomplete chain: in bash mode the bash provider
// answers, on a "!cmd" line the one-off provider, elsewhere the provider below. It is added once, when the settings turn the
// completions on (bashMode.completions).
func (b *bashExt) addAutocompleteProvider(ctx sdk.Context, mode *bashMode) {
	if !ctx.HasUI() || !b.a.keys.ensure(ctx).completionsEnabled() {
		return
	}
	b.autocompleteOnce.Do(func() {
		_ = ctx.AddAutocompleteProvider(func(_ sdk.Context, current *sdk.AutocompleteProvider) (*sdk.AutocompleteProvider, error) {
			return newModeAwareAutocompleteProvider(current, newBashAutocompleteProvider(), newOneOffBashAutocompleteProvider(), mode.isActive), nil
		})
	})
}

func (b *bashExt) onShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	if mode := b.current(); mode != nil {
		mode.sessionShutdown()
	}
	return nil, nil
}

// modeCommand is /bash-mode.
func (b *bashExt) modeCommand(ctx sdk.Context, args string) error {
	b.bind(ctx).handleBashModeCommand(context.Background(), args)
	b.refreshEditor(ctx)
	return nil
}

// resetCommand is /bash-reset.
func (b *bashExt) resetCommand(ctx sdk.Context, _ string) error {
	b.bind(ctx).handleBashResetCommand(context.Background())
	b.refreshEditor(ctx)
	return nil
}

// toggle is the bashMode shortcut action: void setBashModeActive(!bashModeActive, ctx).
func (b *bashExt) toggle(ctx sdk.Context) {
	mode := b.bind(ctx)
	mode.setBashModeActive(context.Background(), !mode.isActive())
	b.refreshEditor(ctx)
}

// refreshEditor draws the editor again after the mode changed (the prompt glyph is `$` in bash mode). The SDK gives an editor no
// way to ask for a render, but the host's setText reaches it and renders it, so an empty editor is set to the empty text; one
// with a draft keeps it untouched and shows the new glyph at its next key, rather than lose its cursor and undo history.
func (b *bashExt) refreshEditor(ctx sdk.Context) {
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		return
	}
	if text, err := ctx.GetEditorText(); err == nil && text == "" {
		ctx.SetEditorText("")
	}
}

// editorEnv is what the bash editor of one installed editor needs.
func (b *bashExt) editorEnv(ctx sdk.Context, mode *bashMode, state *keysState) bashEditorEnv {
	return bashEditorEnv{
		active:      mode.isActive,
		running:     mode.isShellRunning,
		completions: state.completionsEnabled,
		boundaryStart: func() *string {
			start, _ := state.editorBoundaries()
			return start
		},
		boundaryEnd: func() *string {
			_, end := state.editorBoundaries()
			return end
		},
		notify: func(message, level string) { ctx.Notify(message, level) },
		submit: func(command string) { go mode.runShellCommand(context.Background(), command) },
		exitMode: func() {
			go func() {
				mode.setBashModeActive(context.Background(), false)
				b.refreshEditor(ctx)
			}()
		},
		interrupt: func() {
			if session := mode.currentSession(); session != nil {
				session.interrupt()
			}
			ctx.Notify("Sent interrupt to shell", "info")
		},
		resolveGhost:   mode.resolveGhostSuggestion,
		historyEntries: mode.shellHistoryEntries,
	}
}

// bashLimits is the transcript limits of the settings.
func (k *keysState) bashLimits() bashTranscriptLimits {
	k.mu.Lock()
	defer k.mu.Unlock()
	return bashTranscriptLimits{MaxLines: k.bash.TranscriptMaxLines, MaxBytes: k.bash.TranscriptMaxBytes}
}

// editorBoundaries is the editorStart and editorEnd shortcuts.
func (k *keysState) editorBoundaries() (start, end *string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.shortcuts.EditorStart, k.shortcuts.EditorEnd
}
