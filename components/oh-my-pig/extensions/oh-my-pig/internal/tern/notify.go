package tern

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// NotificationKind mirrors omp's structured notification `type` values.
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:3002-3032
// upstream: packages/coding-agent/src/tools/ask.ts:452
type NotificationKind string

const (
	NotifyKindCompletion NotificationKind = "completion"
	NotifyKindError      NotificationKind = "error"
	NotifyKindAsk        NotificationKind = "ask"
	NotifyKindConsole    NotificationKind = "console"
)

// Notification mirrors omp's `TerminalNotification`.
// upstream: packages/tui/src/terminal-capabilities.ts:1442
type Notification struct {
	Title string
	Body  string
	Kind  NotificationKind
}

// NotifyConfig controls omp's notification settings (`completion.notify`,
// `error.notify`, `ask.notify`). Defaults match omp: completion and ask on,
// error off (`modes/settings.ts:1404-1428,1460-1471`).
type NotifyConfig struct {
	CompletionOn bool
	ErrorOn      bool
	AskOn        bool
}

// DefaultNotifyConfig returns omp's default notification settings.
// upstream: packages/coding-agent/src/modes/settings.ts:1404-1428,1460-1471
func DefaultNotifyConfig() NotifyConfig {
	return NotifyConfig{
		CompletionOn: true,
		ErrorOn:      false,
		AskOn:        true,
	}
}

// NotificationLine collapses a structured notification to a single `title: body`
// line, matching omp's `notificationToLine`.
// upstream: packages/tui/src/terminal-capabilities.ts:1472
func NotificationLine(n Notification) string {
	if n.Title != "" && n.Body != "" {
		return n.Title + ": " + n.Body
	}
	if n.Title != "" {
		return n.Title
	}
	return n.Body
}

// NotifyAgentEnd dispatches omp's completion or error notification for an
// `agent_end` event.
//
// Behaviour matches omp (`event-controller.ts:2972-3033`):
//   - Suppressed when `PI_NOTIFICATIONS` is `"off"`, `"0"`, or `"false"`.
//   - Emitted only in interactive TUI mode (`ctx.Mode() == "tui" && ctx.HasUI()`).
//   - Aborted turns (`stopReason == "aborted"`) never notify.
//   - Error turns (`stopReason == "error"`) notify only when `cfg.ErrorOn` is
//     true (omp's `error.notify` defaults to `"off"`).
//   - Completion turns notify when `cfg.CompletionOn` is true (default `"on"`).
//
// Because PiG's extension SDK does not expose raw OSC 99 / OSC 9 writes (only
// `ctx.Bell()`), this degrades to `ctx.Bell()`. Note that in Tern, PiG's host +
// `pig-tern` frontend also emit OSC 7501 program status (`done` / `error`).
//
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:2972-3033
func NotifyAgentEnd(ctx sdk.Context, lookup EnvLookup, cfg NotifyConfig, data map[string]any) (Notification, bool) {
	if IsNotificationSuppressed(lookup) {
		return Notification{}, false
	}
	if ctx.Mode() != "tui" || !ctx.HasUI() {
		return Notification{}, false
	}

	stopReason := lastAssistantStopReason(data)
	if stopReason == "aborted" {
		return Notification{}, false
	}

	title := strings.TrimSpace(ctx.SessionName())
	if title == "" {
		title = NativeTerminalTitle
	}
	var n Notification
	if stopReason == "error" {
		if !cfg.ErrorOn {
			return Notification{}, false
		}
		n = Notification{
			Title: title,
			Body:  "Stopped with error",
			Kind:  NotifyKindError,
		}
	} else {
		if !cfg.CompletionOn {
			return Notification{}, false
		}
		n = Notification{
			Title: title,
			Body:  "Complete",
			Kind:  NotifyKindCompletion,
		}
	}

	_ = ctx.Bell()
	return n, true
}

// NotifyAsk signals that the `ask` tool is waiting for user input, matching
// omp's `AskTool.#sendAskNotification` (once per `ask` call, gated on
// `ask.notify` and interactive TUI mode).
// upstream: packages/coding-agent/src/tools/ask.ts:448-459
func NotifyAsk(ctx sdk.Context, lookup EnvLookup, cfg NotifyConfig) (Notification, bool) {
	if !cfg.AskOn || IsNotificationSuppressed(lookup) {
		return Notification{}, false
	}
	if ctx.Mode() != "tui" || !ctx.HasUI() {
		return Notification{}, false
	}
	n := Notification{
		Title: NativeTerminalTitle,
		Body:  "Waiting for input",
		Kind:  NotifyKindAsk,
	}
	_ = ctx.Bell()
	return n, true
}

// lastAssistantStopReason extracts the `stopReason` of the last assistant
// message in an `agent_end` event's `messages` slice.
// upstream: packages/coding-agent/src/modes/controllers/event-controller.ts:2998,3023
func lastAssistantStopReason(data map[string]any) string {
	raw, ok := data["messages"]
	if !ok || raw == nil {
		return ""
	}
	msgs, ok := raw.([]any)
	if !ok {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "assistant" {
			continue
		}
		reason, _ := m["stopReason"].(string)
		return reason
	}
	return ""
}
