package powerline_footer

import (
	"fmt"
	"strconv"
	"sync"
)

// The prompt stash, the stash history and the copy/cut shortcuts, free of the SDK: keysHost is what they need from the host.
// upstream: index.ts 2204-2446 (stashOrRestoreEditorText, openStashHistory, runPowerlineShortcut, ...) and 1852-1901
// (stash state at session start and shutdown).

// keysHost is the part of the host the stash and shortcut code uses.
type keysHost interface {
	Cwd() string
	SessionID() string
	// EditorText is the text in the editor. The original asks the editor for getExpandedText() (pasted-text markers expanded)
	// first; the SDK only gives ctx.ui.getEditorText().
	EditorText() string
	SetEditorText(text string)
	SetStatus(key string, text *string)
	Notify(message, level string)
	// Select is ctx.ui.select: the chosen option, "" when cancelled.
	Select(title string, options []string) string
	// SelectOverlay is showSelectOverlay: nil when cancelled.
	SelectOverlay(title, hint string, items []selectItem, maxVisible int) *selectItem
	CopyToClipboard(text string) error
	// Hook returns what another feature runs for a shortcut action ("queueOpen", "reply", "bashMode"), or nil when that feature is
	// not available: the shortcut then does not match.
	Hook(action string) func()
}

// Shortcut actions (PowerlineShortcutAction.kind).
const (
	actionStashHistory = "stashHistory"
	actionCopyEditor   = "copyEditor"
	actionCutEditor    = "cutEditor"
	actionQueueOpen    = "queueOpen"
	actionReply        = "reply"
	actionBashMode     = "bashMode"
)

// keysState is the per-extension state of the stash and the shortcuts; mu guards it, and is never held across a host call that
// can wait for the user.
type keysState struct {
	mu          sync.Mutex
	shortcuts   powerlineShortcuts
	bash        bashModeSettings
	stashSharpS bool
	stashed     *string
	history     []string
}

func newKeysState(settings map[string]any, stashSharpS bool) *keysState {
	k := &keysState{}
	k.loadSettings(settings, stashSharpS)
	k.history = readPersistedStashHistory()
	return k
}

// loadSettings resolves the shortcut and bash-mode settings (session start re-reads them).
func (k *keysState) loadSettings(settings map[string]any, stashSharpS bool) {
	k.shortcuts = resolveShortcutConfig(settings)
	k.bash = parseBashModeSettings(settings, &k.shortcuts)
	k.stashSharpS = stashSharpS
}

// sessionStart is the stash part of session_start: a reload takes over the stash of the previous generation, anything else
// drops it. uiStatus is whether the host has a UI (ctx.hasUI).
func (k *keysState) sessionStart(h keysHost, reason string, hasUI bool) {
	k.mu.Lock()
	if reason == "reload" {
		k.stashed = takeActiveStashReload(h.SessionID())
	} else {
		k.stashed = nil
		clearActiveStashReload(h.SessionID())
	}
	k.history = readPersistedStashHistory()
	stashed := k.stashed
	k.mu.Unlock()
	if hasUI {
		var status *string
		if stashed != nil {
			status = new("stash")
		}
		h.SetStatus(statusStash, status)
	}
}

// sessionShutdown is the stash part of session_shutdown.
func (k *keysState) sessionShutdown(h keysHost, reason string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if reason == "reload" {
		transferActiveStashReload(h.SessionID(), k.stashed)
	} else {
		clearActiveStashReload(h.SessionID())
	}
	k.stashed = nil
}

// disable is what /powerline does to the stash when it turns the bar off.
func (k *keysState) disable(h keysHost) {
	k.mu.Lock()
	k.stashed = nil
	clearActiveStashReload(h.SessionID())
	k.mu.Unlock()
	h.SetStatus(statusStash, nil)
}

func (k *keysState) isStashShortcutInput(data string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return matchesStashShortcutInput(data, k.stashSharpS)
}

func (k *keysState) isPromptHistoryShortcutInput(data string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.promptHistoryMatch(data)
}

func (k *keysState) promptHistoryMatch(data string) bool {
	if matchesConfiguredShortcut(data, k.shortcuts.StashHistory) {
		return true
	}
	if k.shortcuts.StashHistory == nil || *k.shortcuts.StashHistory != "ctrl+alt+h" {
		return false
	}
	return ctrlAltHKitty.MatchString(data) || data == "\x1b[27;7;104~" || data == "\x1b[27;7;72~"
}

// shortcutAction is getPowerlineShortcutAction: the action a raw input chunk triggers, "" for none. An action whose feature
// has no hook (see keysHost.Hook) is not recognised.
func (k *keysState) shortcutAction(h keysHost, data string) string {
	if isKeyRelease(data) {
		return ""
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case k.promptHistoryMatch(data):
		return actionStashHistory
	case matchesConfiguredShortcut(data, k.shortcuts.CopyEditor):
		return actionCopyEditor
	case matchesConfiguredShortcut(data, k.shortcuts.CutEditor):
		return actionCutEditor
	case matchesConfiguredShortcut(data, k.shortcuts.QueueOpen):
		return hooked(h, actionQueueOpen)
	case k.shortcuts.Reply != nil && matchesConfiguredShortcut(data, k.shortcuts.Reply):
		return hooked(h, actionReply)
	case matchesConfiguredShortcut(data, k.bash.ToggleShortcut):
		return hooked(h, actionBashMode)
	}
	return ""
}

func hooked(h keysHost, action string) string {
	if h.Hook(action) == nil {
		return ""
	}
	return action
}

// run is runPowerlineShortcut. The stash history and the other pickers wait for the user: the caller runs those off the input
// path (the original calls them with `void`).
func (k *keysState) run(h keysHost, action string) {
	switch action {
	case actionStashHistory:
		k.openStashHistory(h)
	case actionCopyEditor, actionCutEditor:
		text := editorTextForClipboard(h)
		if text == "" {
			return
		}
		if err := h.CopyToClipboard(text); err != nil {
			h.Notify(err.Error(), "error")
			return
		}
		if action == actionCopyEditor {
			h.Notify("Copied editor text", "info")
			return
		}
		h.SetEditorText("")
		h.Notify("Cut editor text", "info")
	default:
		if hook := h.Hook(action); hook != nil {
			hook()
		}
	}
}

// editorTextForClipboard is the editor text, or "" (after saying so) when there is nothing worth copying.
func editorTextForClipboard(h keysHost) string {
	text := h.EditorText()
	if hasNonWhitespaceText(text) {
		return text
	}
	h.Notify("Editor is empty", "info")
	return ""
}

// stashOrRestoreEditorText stashes the editor's text, or brings the stash back when the editor is empty.
func (k *keysState) stashOrRestoreEditorText(h keysHost) {
	raw := h.EditorText()
	k.mu.Lock()
	hasStash := k.stashed != nil

	if !hasNonWhitespaceText(raw) {
		if !hasStash {
			k.mu.Unlock()
			h.Notify("Nothing to stash", "info")
			return
		}
		text := *k.stashed
		k.stashed = nil
		k.mu.Unlock()
		h.SetEditorText(text)
		h.SetStatus(statusStash, nil)
		h.Notify("Stash restored", "info")
		return
	}

	k.stashed = &raw
	if pushStashHistory(&k.history, raw) {
		persistStashHistory(k.history)
	}
	k.mu.Unlock()
	h.SetEditorText("")
	h.SetStatus(statusStash, new("stash"))
	if hasStash {
		h.Notify("Stash updated", "info")
	} else {
		h.Notify("Text stashed", "info")
	}
}

// buildStashPreview, selectStashedPromptFromHistory and the other pickers are defined with the select overlay: see
// select_overlay.go for selectItem and buildStashPreview.

func previewItems(entries []string) []selectItem {
	items := make([]selectItem, len(entries))
	for i, e := range entries {
		items[i] = selectItem{Value: strconv.Itoa(i), Label: fmt.Sprintf("#%d %s", i+1, buildStashPreview(e, stashPreviewWidth))}
	}
	return items
}

// pickEntry shows entries in an overlay and returns the one chosen, "" when cancelled.
func pickEntry(h keysHost, title string, entries []string) string {
	items := previewItems(entries)
	selected := h.SelectOverlay(title, "↑↓ navigate • enter insert • esc cancel", items, min(len(items), 10))
	if selected == nil {
		return ""
	}
	i, err := strconv.Atoi(selected.Value)
	if err != nil || i < 0 || i >= len(entries) {
		return ""
	}
	return entries[i]
}

// selectPromptHistorySource: "stash", "project" or "" (cancelled).
func selectPromptHistorySource(h keysHost, stashCount int) string {
	if stashCount == 0 {
		return "project"
	}
	items := []selectItem{
		{Value: "stash", Label: "Stashed prompts", Description: strconv.Itoa(stashCount) + " saved"},
		{Value: "project", Label: "Recent project prompts", Description: "load on demand"},
	}
	selected := h.SelectOverlay("Prompt history", "↑↓ navigate • enter open • esc cancel", items, len(items))
	if selected == nil {
		return ""
	}
	if selected.Value == "project" {
		return "project"
	}
	return "stash"
}

// insertSelectedPromptHistoryEntry puts a chosen prompt into the editor, asking what to do when the editor has text.
func insertSelectedPromptHistoryEntry(h keysHost, selected string) {
	current := h.EditorText()
	if !hasNonWhitespaceText(current) {
		h.SetEditorText(selected)
		h.Notify("Inserted prompt", "info")
		return
	}
	switch h.Select("Insert prompt", []string{"Replace", "Append", "Cancel"}) {
	case "Replace":
		h.SetEditorText(selected)
		h.Notify("Replaced editor with prompt", "info")
	case "Append":
		separator := "\n"
		if len(current) > 0 && current[len(current)-1] == '\n' || len(selected) > 0 && selected[0] == '\n' {
			separator = ""
		}
		h.SetEditorText(current + separator + selected)
		h.Notify("Appended prompt", "info")
	}
}

// openStashHistory is /stash-history: pick the source, then a prompt, then insert it.
func (k *keysState) openStashHistory(h keysHost) {
	k.mu.Lock()
	history := append([]string(nil), k.history...)
	k.mu.Unlock()

	source := selectPromptHistorySource(h, len(history))
	if source == "" {
		return
	}
	if source == "project" {
		prompts, err := readRecentProjectPrompts(h.Cwd(), projectPromptHistoryLimit)
		if err != nil {
			h.Notify("Failed to load project prompts: "+err.Error(), "warning")
			return
		}
		if len(prompts) == 0 {
			if len(history) == 0 {
				h.Notify("No prompt history yet", "info")
			} else {
				h.Notify("No recent project prompts", "info")
			}
			return
		}
		if selected := pickEntry(h, "Recent project prompts", prompts); selected != "" {
			insertSelectedPromptHistoryEntry(h, selected)
		}
		return
	}
	// The stash list is the snapshot taken when the picker opens (selectStashedPromptFromHistory copies it first).
	selected := pickEntry(h, "Stash history", history)
	if selected == "" {
		return
	}
	if h.Select("Stashed prompt", []string{"Insert", "Cancel"}) == "Insert" {
		insertSelectedPromptHistoryEntry(h, selected)
	}
}
