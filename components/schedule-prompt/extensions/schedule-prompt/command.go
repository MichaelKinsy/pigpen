package schedule_prompt

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The /schedule-prompt command (upstream: index.ts registerCommand "schedule-prompt"): a menu with the Jobs overlay and Settings.

const commandDescription = "Manage scheduled prompts interactively"

// sdkFlowUI is ctx.ui as the add flow uses it.
type sdkFlowUI struct{ ctx sdk.Context }

func (u sdkFlowUI) Input(title, placeholder string) (string, bool, error) {
	return u.ctx.Input(title, placeholder)
}
func (u sdkFlowUI) Select(title string, options []string) (string, bool, error) {
	return u.ctx.Select(title, options)
}
func (u sdkFlowUI) Confirm(title, message string) (bool, error) { return u.ctx.Confirm(title, message) }
func (u sdkFlowUI) Notify(message, level string)                { u.ctx.Notify(message, level) }

func (a *app) currentSettings() settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *app) runCommand(ctx sdk.Context, _ string) error {
	a.mu.Lock()
	storage, sched := a.storage, a.sched
	a.mu.Unlock()
	if storage == nil || sched == nil {
		return errNoSession
	}
	mySessionID := sdkSession{ctx}.SessionID()

	action, ok, err := ctx.Select("Scheduled Prompts", []string{"Jobs", "Settings"})
	if err != nil || !ok || action == "" {
		return err
	}
	switch action {
	case "Jobs":
		return a.runJobs(ctx, storage, sched, mySessionID)
	case "Settings":
		return a.runSettings(ctx)
	}
	return nil
}

// overlayControl is the part of the overlay handle the add flow uses.
type overlayControl interface {
	SetHidden(hidden bool) error
	Focus() error
}

// hideWhileAdding wraps the add flow: the Jobs overlay is hidden while the flow's dialogs are open (it would sit on top of them and steal
// input) and shown and focused again afterwards. handle is read at call time: the host hands the overlay over after the command started.
func hideWhileAdding(handle func() overlayControl, add func() error) func() error {
	return func() error {
		if h := handle(); h != nil {
			_ = h.SetHidden(true)
		}
		defer func() {
			if h := handle(); h != nil {
				_ = h.SetHidden(false)
				_ = h.Focus()
			}
		}()
		return add()
	}
}

// jobsOverlayOptions: the overlay takes the whole terminal, as the original's ctx.ui.custom({overlay: true, overlayOptions: {width: "100%", maxHeight: "100%"}}).
func jobsOverlayOptions() *sdk.OverlayOptions {
	return &sdk.OverlayOptions{Width: sdk.OverlayPercent(100), MaxHeight: sdk.OverlayPercent(100)}
}

func (a *app) runJobs(ctx sdk.Context, storage jobStore, sched jobScheduler, mySessionID string) error {
	var overlay *sdk.OverlayHandle
	handle := func() overlayControl {
		if overlay == nil {
			return nil
		}
		return overlay
	}
	wrappedRunAdd := hideWhileAdding(handle, func() error {
		return runAddFlow(sdkFlowUI{ctx}, storage, sched, a.currentSettings(), mySessionID)
	})
	view := newJobsView(storage, sched, mySessionID, wrappedRunAdd, ctx.UITheme())
	_, err := ctx.Custom(view, sdk.RemoteOverlayOptions{
		Overlay:        true,
		OverlayOptions: jobsOverlayOptions(),
		OnHandle:       func(h *sdk.OverlayHandle) { overlay = h },
	})
	return err
}

// runSettings loops so the menu redraws with the current state after each change: the menu is the display, only a failed write needs a
// notification.
func (a *app) runSettings(ctx sdk.Context) error {
	for {
		widgetState := "hidden"
		if a.isWidgetVisible() {
			widgetState = "shown"
		}
		scope := a.currentSettings().defaultJobScope
		bound := scope == "" || scope == "session"
		boundText := "no"
		if bound {
			boundText = "yes"
		}
		choice, ok, err := ctx.Select("Settings", []string{"Widget visibility: " + widgetState, "Bind new jobs to session: " + boundText, "Back"})
		if err != nil || !ok || choice == "" || choice == "Back" {
			return err
		}
		switch {
		case strings.HasPrefix(choice, "Widget visibility:"):
			next := !a.isWidgetVisible()
			a.mu.Lock()
			a.settings.widgetVisible = &next
			widget := a.widget
			a.mu.Unlock()
			a.widgetOn.Store(next)
			if widget != nil {
				if next {
					widget.show(sdkWidgetUI{ctx})
				} else {
					widget.hide(sdkWidgetUI{ctx})
				}
			}
			if !saveSettings(ctx.Cwd(), &next, "") {
				word := "hidden"
				if next {
					word = "shown"
				}
				ctx.Notify("Widget "+word+" (session only; failed to persist)", "warning")
			}
		case strings.HasPrefix(choice, "Bind new jobs to session:"):
			// Affects newly created jobs only; existing jobs keep their binding.
			next := "session"
			if bound {
				next = "workdir"
			}
			a.mu.Lock()
			a.settings.defaultJobScope = next
			a.mu.Unlock()
			if !saveSettings(ctx.Cwd(), nil, next) {
				answer := "no"
				if next == "session" {
					answer = "yes"
				}
				ctx.Notify("Bind new jobs to session: "+answer+" (session only; failed to persist)", "warning")
			}
		}
	}
}
