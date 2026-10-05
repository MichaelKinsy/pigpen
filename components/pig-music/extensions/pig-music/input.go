package pig_music

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/keys"
)

// keyMsg turns one input chunk from PiG into the key press the model expects. It
// reports false for chunks that are not key presses (mouse reports, focus events,
// Kitty key releases).
func keyMsg(data string) (tea.KeyPressMsg, bool) {
	k, ok := keys.Decode(data)
	if !ok || k.Release {
		return tea.KeyPressMsg{}, false
	}
	switch k.Name {
	case "text", "paste":
		r, _ := utf8.DecodeRuneInString(k.Text)
		if k.Text == "" || r == utf8.RuneError {
			return tea.KeyPressMsg{}, false
		}
		return tea.KeyPressMsg(tea.Key{Code: r, Text: k.Text}), true
	}
	var mod tea.KeyMod
	name := k.Name
	for {
		switch {
		case strings.HasPrefix(name, "ctrl+") && len(name) > 5:
			mod, name = mod|tea.ModCtrl, name[5:]
		case strings.HasPrefix(name, "alt+") && len(name) > 4:
			mod, name = mod|tea.ModAlt, name[4:]
		case strings.HasPrefix(name, "shift+") && len(name) > 6:
			mod, name = mod|tea.ModShift, name[6:]
		case strings.HasPrefix(name, "super+") && len(name) > 6:
			mod, name = mod|tea.ModSuper, name[6:]
		default:
			goto base
		}
	}
base:
	key := tea.Key{Mod: mod}
	switch name {
	case "up":
		key.Code = tea.KeyUp
	case "down":
		key.Code = tea.KeyDown
	case "left":
		key.Code = tea.KeyLeft
	case "right":
		key.Code = tea.KeyRight
	case "home":
		key.Code = tea.KeyHome
	case "end":
		key.Code = tea.KeyEnd
	case "pgup":
		key.Code = tea.KeyPgUp
	case "pgdn":
		key.Code = tea.KeyPgDown
	case "delete":
		key.Code = tea.KeyDelete
	case "insert":
		key.Code = tea.KeyInsert
	case "enter":
		key.Code = tea.KeyEnter
	case "escape":
		key.Code = tea.KeyEscape
	case "tab":
		key.Code = tea.KeyTab
	case "backspace":
		key.Code = tea.KeyBackspace
	case "space":
		key.Code = tea.KeySpace
		if mod == 0 {
			key.Text = " "
		}
	default:
		r, size := utf8.DecodeRuneInString(name)
		if size != len(name) || r == utf8.RuneError {
			return tea.KeyPressMsg{}, false // function keys and the like: not bound
		}
		key.Code = r
		if mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			key.Text = k.Text
		}
	}
	return tea.KeyPressMsg(key), true
}
