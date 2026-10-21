package ask_user_question

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The user's keybinding overrides, read from <agent dir>/keybindings.json the way Pi's KeybindingsManager does:
// legacy short names are migrated to the namespaced ids, and a value is a key or a list of keys. Only the ids the
// questionnaire matches matter (tui.select.*, tui.input.*, tui.editor.*, app.editor.external), but the whole file
// is read so a legacy name for one of them is honoured. upstream: core/keybindings.ts, as PiG ports it.
var legacyKeybindingNames = map[string]string{
	"cursorUp": "tui.editor.cursorUp", "cursorDown": "tui.editor.cursorDown", "cursorLeft": "tui.editor.cursorLeft",
	"cursorRight": "tui.editor.cursorRight", "cursorWordLeft": "tui.editor.cursorWordLeft",
	"cursorWordRight": "tui.editor.cursorWordRight", "cursorLineStart": "tui.editor.cursorLineStart",
	"cursorLineEnd": "tui.editor.cursorLineEnd", "jumpForward": "tui.editor.jumpForward",
	"jumpBackward": "tui.editor.jumpBackward", "pageUp": "tui.editor.pageUp", "pageDown": "tui.editor.pageDown",
	"deleteCharBackward": "tui.editor.deleteCharBackward", "deleteCharForward": "tui.editor.deleteCharForward",
	"deleteWordBackward": "tui.editor.deleteWordBackward", "deleteWordForward": "tui.editor.deleteWordForward",
	"deleteToLineStart": "tui.editor.deleteToLineStart", "deleteToLineEnd": "tui.editor.deleteToLineEnd",
	"yank": "tui.editor.yank", "yankPop": "tui.editor.yankPop", "undo": "tui.editor.undo",
	"newLine": "tui.input.newLine", "submit": "tui.input.submit", "tab": "tui.input.tab", "copy": "tui.input.copy",
	"selectUp": "tui.select.up", "selectDown": "tui.select.down", "selectPageUp": "tui.select.pageUp",
	"selectPageDown": "tui.select.pageDown", "selectConfirm": "tui.select.confirm", "selectCancel": "tui.select.cancel",
	"externalEditor": "app.editor.external",
}

// loadKeybindingOverrides reads the overrides; a missing, unreadable or malformed file reads as none.
func loadKeybindingOverrides() map[string][]string {
	return readKeybindingOverrides(filepath.Join(agentDir(), "keybindings.json"))
}

func readKeybindingOverrides(path string) map[string][]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := map[string][]string{}
	for key, value := range raw {
		next := key
		if migrated, ok := legacyKeybindingNames[key]; ok {
			next = migrated
			if _, exists := raw[next]; exists {
				continue
			}
		}
		switch v := value.(type) {
		case string:
			out[next] = []string{v}
		case []any:
			keys := []string{}
			for _, item := range v {
				if s, ok := item.(string); ok {
					keys = append(keys, s)
				}
			}
			out[next] = keys
		}
	}
	return out
}
