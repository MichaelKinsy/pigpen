package tui

// Key identifiers: Go spelling of pi-tui's `Key` object (keys.ts).
const (
	KeyEscape    = "escape"
	KeyEsc       = "esc"
	KeyEnter     = "enter"
	KeyReturn    = "return"
	KeyTab       = "tab"
	KeySpace     = "space"
	KeyBackspace = "backspace"
	KeyDelete    = "delete"
	KeyInsert    = "insert"
	KeyClear     = "clear"
	KeyHome      = "home"
	KeyEnd       = "end"
	KeyPageUp    = "pageUp"
	KeyPageDown  = "pageDown"
	KeyUp        = "up"
	KeyDown      = "down"
	KeyLeft      = "left"
	KeyRight     = "right"
	KeyF1        = "f1"
	KeyF2        = "f2"
	KeyF3        = "f3"
	KeyF4        = "f4"
	KeyF5        = "f5"
	KeyF6        = "f6"
	KeyF7        = "f7"
	KeyF8        = "f8"
	KeyF9        = "f9"
	KeyF10       = "f10"
	KeyF11       = "f11"
	KeyF12       = "f12"
)

// Modifier helpers: Key.ctrl(k), Key.shift(k), ... in pi-tui.
func KeyCtrl(key string) string      { return "ctrl+" + key }
func KeyShift(key string) string     { return "shift+" + key }
func KeyAlt(key string) string       { return "alt+" + key }
func KeySuper(key string) string     { return "super+" + key }
func KeyCtrlShift(key string) string { return "ctrl+shift+" + key }
func KeyShiftCtrl(key string) string { return "shift+ctrl+" + key }
func KeyCtrlAlt(key string) string   { return "ctrl+alt+" + key }
func KeyAltCtrl(key string) string   { return "alt+ctrl+" + key }
func KeyShiftAlt(key string) string  { return "shift+alt+" + key }
func KeyAltShift(key string) string  { return "alt+shift+" + key }

// MatchesKey reports whether raw terminal input matches a key id such as
// "ctrl+c", "shift+tab" or "escape" (pi-tui matchesKey).
func MatchesKey(data, keyID string) bool { return matchesKeyID(data, keyID) }
