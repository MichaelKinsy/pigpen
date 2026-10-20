package pi_permission_system

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// The host adapter for the approval dialog: PermissionPromptUi over PiG's sdk.Context. ui.select, ui.input, getToolsExpanded and
// setToolsExpanded map one to one; ui.custom becomes Context.Custom over a sdk.RemoteComponent that renders the dialog locally and
// takes the keystrokes the host routes to it.
//
// What the SDK does not give an extension (reported, not worked around): tui.terminal.write (the prompt notification's raw bytes, so
// write is an optional callback and the notification is dropped when it is nil) and the host's KeybindingsManager (the tool-expansion
// action and pi-tui's global manager come from keys, which the caller builds from the user's keybindings file).

// SDKPromptUi is a PermissionPromptUi over one session context.
type SDKPromptUi struct {
	ctx   sdk.Context
	keys  tui.Keybindings
	write func(string)
}

// NewSDKPromptUi adapts ctx. keys answers the tool-expansion action ("app.tools.expand"); write sends raw bytes to the terminal and
// may be nil, which drops the prompt notification.
func NewSDKPromptUi(ctx sdk.Context, keys tui.Keybindings, write func(string)) *SDKPromptUi {
	return &SDKPromptUi{ctx: ctx, keys: keys, write: write}
}

// Select implements PermissionDecisionUi.
func (u *SDKPromptUi) Select(title string, options []string) (string, bool, error) {
	return u.ctx.Select(title, options)
}

// Input implements PermissionDecisionUi.
func (u *SDKPromptUi) Input(title, placeholder string) (string, bool, error) {
	return u.ctx.Input(title, placeholder)
}

// GetToolsExpanded reads the host's tool expansion; an unreadable host reads as collapsed.
func (u *SDKPromptUi) GetToolsExpanded() bool {
	expanded, err := u.ctx.GetToolsExpanded()
	return err == nil && expanded
}

// SetToolsExpanded sets the host's tool expansion.
func (u *SDKPromptUi) SetToolsExpanded(expanded bool) { u.ctx.SetToolsExpanded(expanded) }

// Custom mounts the dialog through Context.Custom and blocks until the component decides. ok is false when the host closed the dialog
// without a decision, which the dialog turns into an error, never an approval.
func (u *SDKPromptUi) Custom(factory PromptComponentFactory, options PromptCustomOptions) (UnattributedDecision, bool, error) {
	dialog := newRemoteDialog(factory, u.ctx.UITheme(), u.keys, u.write)
	result, err := u.ctx.Custom(dialog, sdk.RemoteOverlayOptions{Overlay: options.Overlay})
	if err != nil {
		return UnattributedDecision{}, false, err
	}
	if result == nil {
		return UnattributedDecision{}, false, nil
	}
	decision, decided := dialog.outcome()
	return decision, decided, nil
}

// remoteDialog is the sdk.RemoteComponent around a PromptComponent: it renders the component's lines, feeds it the keystrokes the host
// routes to it, and closes with the decision the component passed to done.
type remoteDialog struct {
	component PromptComponent
	// write sends raw bytes to the terminal (nil drops them: the SDK has no terminal write).
	write func(string)

	mu         sync.Mutex
	decision   UnattributedDecision
	decided    bool
	invalidate func()
}

type hostTUI struct{ dialog *remoteDialog }

func (h hostTUI) RequestRender() {
	h.dialog.mu.Lock()
	f := h.dialog.invalidate
	h.dialog.mu.Unlock()
	if f != nil {
		f()
	}
}

func (h hostTUI) Write(data string) {
	if h.dialog.write != nil {
		h.dialog.write(data)
	}
}

func newRemoteDialog(factory PromptComponentFactory, theme PromptTheme, keys tui.Keybindings, write func(string)) *remoteDialog {
	d := &remoteDialog{write: write}
	d.component = factory(hostTUI{d}, theme, keys, func(decision UnattributedDecision) {
		d.mu.Lock()
		if !d.decided {
			d.decision, d.decided = decision, true
		}
		d.mu.Unlock()
	})
	return d
}

// outcome is what the component decided, once it has.
func (d *remoteDialog) outcome() (UnattributedDecision, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.decision, d.decided
}

// Render implements sdk.RemoteComponent.
func (d *remoteDialog) Render(width int) []string { return d.component.Render(width) }

// HandleInput implements sdk.RemoteComponent. A Kitty key release is dropped, as pi-tui does for a component that did not ask for
// releases: a release of "y" must never count as the press.
func (d *remoteDialog) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	d.component.HandleInput(data)
	if decision, decided := d.outcome(); decided {
		return sdk.RemoteComponentResult{Done: true, Value: decisionWire(decision)}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// SetInvalidate implements sdk.RemoteComponentInvalidator.
func (d *remoteDialog) SetInvalidate(f func()) {
	d.mu.Lock()
	d.invalidate = f
	d.mu.Unlock()
}

// decisionWire is the decision as the value Context.Custom returns: it only has to be non-null, the decision itself stays in-process.
func decisionWire(d UnattributedDecision) map[string]any {
	return map[string]any{"approved": d.Approved, "state": string(d.State)}
}
