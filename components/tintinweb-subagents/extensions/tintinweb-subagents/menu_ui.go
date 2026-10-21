package tintinweb_subagents

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// menuUI is ctx.ui as the `/agents` menus use it: the five dialogs and a blocking Custom. Every method returns when the
// user has answered; a cancel (Esc) is ok == false, a UI that cannot be reached is a cancel too. The menus are written
// against this, so a test drives them with scripted answers as the original's tests drive a ctx.ui stub.
type menuUI interface {
	selectUI
	Confirm(title, message string) (bool, error)
	Input(title, placeholder string) (text string, ok bool, err error)
	Editor(title, prefill string) (text string, ok bool, err error)
	Notify(message, level string)
	// Custom runs a component until it calls done(value) and returns that value (ok false when it never did: the
	// overlay was closed from outside, or could not be opened). A nil opts is an inline component in the editor's
	// slot; with opts it is an overlay.
	Custom(factory menuFactory, opts *menuOverlay) (value any, ok bool)
}

// menuFactory builds the component of a Custom call. upstream: the ctx.ui.custom factory (tui, theme, keybindings, done).
type menuFactory func(host viewerHost, th uiTheme, done func(value any)) viewerComponent

// menuOverlay is the overlay form of a Custom call. upstream: ctx.ui.custom options { overlay: true, overlayOptions, onHandle }.
type menuOverlay struct {
	overlayOptions
	// OnHandle receives the mounted overlay's handle once, so the component can hide and show itself.
	OnHandle func(h menuOverlayHandle)
}

// menuOverlayHandle is the part of Pi's OverlayHandle the menus use.
type menuOverlayHandle interface {
	SetHidden(hidden bool) error
}

// sdkMenuUI is menuUI over a command's context.
type sdkMenuUI struct {
	app *app
	ctx sdk.Context
}

func (u *sdkMenuUI) Select(title string, options []string) (choice string, ok bool, err error) {
	guard(func() { choice, ok, err = u.ctx.Select(title, options) })
	return choice, ok, err
}

func (u *sdkMenuUI) Confirm(title, message string) (ok bool, err error) {
	guard(func() { ok, err = u.ctx.Confirm(title, message) })
	return ok, err
}

func (u *sdkMenuUI) Input(title, placeholder string) (text string, ok bool, err error) {
	guard(func() { text, ok, err = u.ctx.Input(title, placeholder) })
	return text, ok, err
}

func (u *sdkMenuUI) Editor(title, prefill string) (text string, ok bool, err error) {
	guard(func() { text, ok, err = u.ctx.Editor(title, prefill) })
	return text, ok, err
}

func (u *sdkMenuUI) Notify(message, level string) {
	guard(func() { u.ctx.Notify(message, level) })
}

func (u *sdkMenuUI) Custom(factory menuFactory, opts *menuOverlay) (any, bool) {
	overlay := &overlayComponent{ui: u.app.ui.ui, label: "menu"}
	var th uiTheme
	guard(func() { th = sdkTheme{u.ctx.UITheme()} })
	if th == nil {
		return nil, false
	}
	if err := catch("opening a menu", func() { overlay.component = factory(overlay, th, overlay.closeWith) }); err != nil {
		u.Notify("Could not open the menu: "+err.Error(), "error")
		return nil, false
	}
	var options any
	if opts != nil {
		o := sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{Anchor: opts.Anchor,
			Width: sdk.OverlayPercent(float64(opts.WidthPercent)), MaxHeight: sdk.OverlayPercent(float64(opts.HeightPercent))}}
		if opts.OnHandle != nil {
			o.OnHandle = func(h *sdk.OverlayHandle) {
				overlay.setHandle(h)
				safeCall("overlay handle", func() { opts.OnHandle(h) })
			}
		} else {
			o.OnHandle = overlay.setHandle
		}
		options = o
	}
	var value any
	var ok bool
	guard(func() {
		v, err := u.ctx.Custom(overlay, options)
		value, ok = v, err == nil
	})
	return value, ok
}

// useMenuUI is the menus' UI for a command context.
func (a *app) menuUIFor(ctx sdk.Context) menuUI { return &sdkMenuUI{app: a, ctx: ctx} }
