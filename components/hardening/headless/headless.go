// Package headless holds what a Package does instead of opening a dialog when the headless flag is on.
//
// PiG 0.4.1 in RPC mode binds a UI context, so a Context.HasUI() is true on a host that never answers a dialog;
// the request is written to the client and waits for a response. Under the flag a Package treats HasUI as false,
// takes its existing no-UI path in the blocking variant, and where no such path exists returns the typed error
// from Blocking. The flag never answers a dialog with allow: a host that wants a tool class allowed says so in
// policy.
package headless

import (
	"context"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// UI is the HasUI a Package acts on: false when the headless flag is on, else the host's answer.
func UI(f profile.Flags, hostHasUI bool) bool { return hostHasUI && !f.Headless }

// Blocking returns the typed error a Package returns instead of opening a dialog: ui_unavailable, naming the
// Package and the place (what must be an identifier such as "setup-confirm"; anything else is dropped).
func Blocking(pkg, what string) error {
	return profile.NewError(profile.UIUnavailable, profile.FlagHeadless, pkg).WithSite(what)
}

// Cancelled returns the typed cancellation error when ctx is done, else nil. A cancelled call takes no action.
func Cancelled(ctx context.Context, pkg string) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return profile.NewError(profile.Cancelled, profile.FlagHeadless, pkg).WithCause(err)
	}
	return nil
}

// Dialog is the guard at a site that would open a dialog: a done context is Cancelled, otherwise Blocking.
// A Package calls it only when the flag is on.
func Dialog(ctx context.Context, pkg, what string) error {
	if err := Cancelled(ctx, pkg); err != nil {
		return err
	}
	return Blocking(pkg, what)
}

// Guard returns nil when the flag is off (the Package opens its dialog as before) and Dialog's error when it is on.
func Guard(ctx context.Context, f profile.Flags, pkg, what string) error {
	if !f.Headless {
		return nil
	}
	return Dialog(ctx, pkg, what)
}
