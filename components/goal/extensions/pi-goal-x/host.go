package pi_goal_x

import (
	"context"
)

// host is everything the kernel (core.go, goal_scheduler.go) and the slices built on it (goal_commands.go, goal_tools.go,
// goal_events.go) need from the host's `ctx` and `pi` objects in the original. The SDK adapter is sdkHost (extension.go); the tests
// use fakeHost (replay_test.go) and the goal rig (goal_rig_test.go). A method that can fail in the original returns the failure as
// the zero value: a host call that errors is a host without that capability.
type host interface {
	// ---- ctx ----
	Cwd() string
	// SessionID is ctx.sessionManager.getSessionId() ("" when the host has none).
	SessionID() string
	// SessionFile is ctx.sessionManager.getSessionFile() ("" when the session is not persisted).
	SessionFile() string
	HasUI() bool
	// Mode is ctx.mode: "tui", "rpc", "json" or "print".
	Mode() string
	Notify(msg, level string)
	// IsIdle is ctx.isIdle(); a failing host call reads as idle.
	IsIdle() bool
	HasPendingMessages() bool
	Abort()
	// Signal is ctx.signal: cancelled when the host aborts the current agent turn. Never nil.
	Signal() context.Context
	// Branch is ctx.sessionManager.getBranch() as decoded JSON objects.
	Branch() []any
	// Model is ctx.model; nil when no model is selected. ModelRegistry is ctx.modelRegistry.
	Model() *auditorModel
	ModelRegistry() auditorModelRegistry
	// ContextUsage is ctx.getContextUsage(); nil when unknown.
	ContextUsage() *goalContextUsage

	// ---- ctx.ui ----
	// SetStatus sets the footer status of a key; empty text clears it.
	SetStatus(key, text string)
	// SetWidget shows lines above the editor; nil lines clear the widget. A host in RPC mode drops non-clear pushes (Pi's RPC
	// host drops component factories too).
	SetWidget(key string, lines []string)
	// Width is the terminal width the widget lines are laid out for; Height the terminal rows (ok=false when unknown).
	Width() int
	Height() (int, bool)
	// OnWidthChange calls fn with the new width after each terminal resize.
	OnWidthChange(fn func(width int)) (unsubscribe func())
	// Theme is ctx.ui.theme.
	Theme() goalHostTheme
	Confirm(title, msg string) bool
	Select(title string, options []string) (string, bool)
	Input(title, placeholder string) (string, bool)
	// Custom is ctx.ui.custom(factory, options): the factory's component runs as a focused remote component.
	Custom(factory goalCustomFactory, opts goalCustomOptions) (any, error)
	OnTerminalInput(cb func(data string) *goalInputResult) (unsubscribe func())
	SetWorkingVisible(visible bool)

	// ---- pi ----
	AppendEntry(customType string, data *jsObject)
	// SendMessage is pi.sendMessage({customType, content, display, details}, {triggerTurn, deliverAs}); deliverAs "" is unset.
	SendMessage(customType, content string, display bool, details *jsObject, triggerTurn bool, deliverAs string) error
	// SendUserMessage is pi.sendUserMessage(content, {deliverAs}).
	SendUserMessage(content, deliverAs string) error
	// GetActiveTools errors when the host cannot say (the original then leaves the tool profile alone).
	GetActiveTools() ([]string, error)
	SetActiveTools(tools []string)
	// OnEvent is pi.events.on(channel, handler).
	OnEvent(channel string, fn func(payload any)) (unsubscribe func())
}

// goalHostTheme is the Theme the host UI hands out.
type goalHostTheme interface {
	goalTheme
	dialogTheme
}

// hostRuntimeCtx is the slice of the host the goal runtime reads (goalRuntimeCtx).
type hostRuntimeCtx struct{ h host }

func (r hostRuntimeCtx) isIdle() (bool, error)             { return r.h.IsIdle(), nil }
func (r hostRuntimeCtx) hasPendingMessages() (bool, error) { return r.h.HasPendingMessages(), nil }

// hostDialogCtx is the host as the dialogs, the widget keybindings and the questionnaire see it (goalDialogContext).
type hostDialogCtx struct{ h host }

func (d hostDialogCtx) hasUI() bool      { return d.h.HasUI() }
func (d hostDialogCtx) mode() string     { return d.h.Mode() }
func (d hostDialogCtx) cwd() string      { return d.h.Cwd() }
func (d hostDialogCtx) ui() goalDialogUI { return hostDialogUI{d.h} }

// hostDialogUI is the host's ctx.ui as goalDialogUI.
type hostDialogUI struct{ h host }

func (u hostDialogUI) hasCustom() bool { return true }
func (u hostDialogUI) hasSelect() bool { return true }
func (u hostDialogUI) hasInput() bool  { return true }
func (u hostDialogUI) custom(factory goalCustomFactory, opts goalCustomOptions) (any, error) {
	return u.h.Custom(factory, opts)
}
func (u hostDialogUI) selectOption(title string, options []string) (string, bool, error) {
	v, ok := u.h.Select(title, options)
	return v, ok, nil
}
func (u hostDialogUI) input(title, placeholder string) (string, bool, error) {
	v, ok := u.h.Input(title, placeholder)
	return v, ok, nil
}
func (u hostDialogUI) notify(message, level string)   { u.h.Notify(message, level) }
func (u hostDialogUI) setWorkingVisible(visible bool) { u.h.SetWorkingVisible(visible) }
func (u hostDialogUI) onTerminalInput(cb func(data string) *goalInputResult) func() {
	return u.h.OnTerminalInput(cb)
}

// hostAuditorCtx is the host as the auditor and the oracle see it (auditorContext).
type hostAuditorCtx struct{ h host }

func (a hostAuditorCtx) cwd() string                         { return a.h.Cwd() }
func (a hostAuditorCtx) model() *auditorModel                { return a.h.Model() }
func (a hostAuditorCtx) modelRegistry() auditorModelRegistry { return a.h.ModelRegistry() }
