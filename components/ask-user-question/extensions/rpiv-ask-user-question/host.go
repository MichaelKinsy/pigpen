package ask_user_question

import (
	"os"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// host is what a questionnaire run needs of the PiG context. It is the Go form of the `ctx` and `ctx.ui` the
// original's execute() reads; the production implementation wraps sdk.Context, tests supply a fake that can also
// hand out an overlay handle (which the SDK cannot yet).
type host interface {
	HasUI() bool
	// Mode is "tui", "rpc", "json" or "print". upstream: ctx.mode.
	Mode() string
	Select(title string, options []string) (string, bool, error)
	Input(title, placeholder string) (string, bool, error)
	Emit(channel string, payload any)
	Notify(message, level string)
	// Width and Height are the terminal size (tui.terminal.columns and rows).
	Width() int
	Height() int
	Theme() ui.Theme
	Keybindings() tui.Keybindings
	// OnTerminalInput subscribes to raw terminal input. supported is false where the host has no such hook
	// (`typeof ctx.ui.onTerminalInput !== "function"`). The remover is idempotent.
	OnTerminalInput(handler func(data string) sdk.TerminalInputResult) (remove func(), supported bool)
	// Custom opens the focused component in an overlay and resolves with its result (nil when the host cannot render
	// it). onHandle receives the overlay handle once the overlay exists, where the host has one.
	Custom(component sdk.RemoteComponent, onHandle func(ui.OverlayHandle)) (any, error)
}

// ctxHost is the production host.
type ctxHost struct{ ctx sdk.Context }

func (h ctxHost) HasUI() bool  { return h.ctx.HasUI() }
func (h ctxHost) Mode() string { return h.ctx.Mode() }
func (h ctxHost) Select(title string, options []string) (string, bool, error) {
	return h.ctx.Select(title, options)
}
func (h ctxHost) Input(title, placeholder string) (string, bool, error) {
	return h.ctx.Input(title, placeholder)
}
func (h ctxHost) Emit(channel string, payload any) { emit(h.ctx, channel, payload) }
func (h ctxHost) Notify(message, level string)     { h.ctx.Notify(message, level) }
func (h ctxHost) Width() int                       { return h.ctx.Width() }
func (h ctxHost) Height() int                      { return h.ctx.Height() }
func (h ctxHost) Theme() ui.Theme                  { return h.ctx.UITheme() }

// Keybindings is pi-tui's default bindings with the user's overrides from <agent dir>/keybindings.json. The
// original receives Pi's KeybindingsManager from ctx.ui.custom; the SDK has no such accessor (SDK request: expose
// KeybindingsManager.matches), so this reads the same file Pi does.
func (h ctxHost) Keybindings() tui.Keybindings { return tui.NewKeybindings(loadKeybindingOverrides()) }

// OnTerminalInput is not offered: the raw listener only acts through an overlay handle, and the SDK's
// Context.Custom returns none (ctx.ui.custom options.onHandle is MISSING in the surface table). Registering it
// would make the host wait on this extension for a verdict on every keystroke and do nothing with it. Without a
// handle the original itself falls back to the visible one-line row, which the key router drives.
func (h ctxHost) OnTerminalInput(func(string) sdk.TerminalInputResult) (func(), bool) {
	return nil, false
}

// Custom opens the overlay the way the original does (bottom-anchored, full width, up to full height, no margin).
func (h ctxHost) Custom(component sdk.RemoteComponent, _ func(ui.OverlayHandle)) (any, error) {
	zero := 0
	return h.ctx.Custom(component, sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Anchor:    "bottom-center",
			Width:     sdk.OverlayPercent(100),
			MaxHeight: sdk.OverlayPercent(100),
			Margin:    &sdk.OverlayMargin{All: &zero},
		},
	})
}

// agentDir is PiG's agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent.
func agentDir() string {
	if dir := os.Getenv("PIG_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	if home := os.Getenv("PIG_HOME"); home != "" {
		return filepath.Join(home, "agent")
	}
	return filepath.Join(homeDir(), ".pig", "agent")
}
