package powerline_footer

import (
	"regexp"
	"strings"
)

// upstream: shortcuts.ts.

// superShortcutPatterns are the sequences terminals send for super (Cmd) shortcuts, by Kitty CSI-u, xterm modifyOtherKeys and
// legacy CSI forms.
var superShortcutPatterns = map[string]*regexp.Regexp{
	"super+up":         regexp.MustCompile(`^\x1b\[(?:1;9(?::[12])?[AH]|574(?:19|23);9(?::[12])?u|7;9(?::[12])?~|27;9;65~)$`),
	"super+down":       regexp.MustCompile(`^\x1b\[(?:1;9(?::[12])?[BF]|574(?:20|24);9(?::[12])?u|8;9(?::[12])?~|27;9;66~)$`),
	"super+home":       regexp.MustCompile(`^\x1b\[(?:1;9(?::[12])?H|57423;9(?::[12])?u|7;9(?::[12])?~)$`),
	"super+end":        regexp.MustCompile(`^\x1b\[(?:1;9(?::[12])?F|57424;9(?::[12])?u|8;9(?::[12])?~)$`),
	"super+pageup":     regexp.MustCompile(`^\x1b\[(?:5;9(?::[12])?~|57421;9(?::[12])?u)$`),
	"super+pagedown":   regexp.MustCompile(`^\x1b\[(?:6;9(?::[12])?~|57422;9(?::[12])?u)$`),
	"super+shift+up":   regexp.MustCompile(`^\x1b\[(?:1;10(?::[12])?[AH]|574(?:19|23);10(?::[12])?u|7;10(?::[12])?~|27;10;65~)$`),
	"super+shift+down": regexp.MustCompile(`^\x1b\[(?:1;10(?::[12])?[BF]|574(?:20|24);10(?::[12])?u|8;10(?::[12])?~|27;10;66~)$`),
	"super+shift+home": regexp.MustCompile(`^\x1b\[(?:1;10(?::[12])?H|57423;10(?::[12])?u|7;10(?::[12])?~)$`),
	"super+shift+end":  regexp.MustCompile(`^\x1b\[(?:1;10(?::[12])?F|57424;10(?::[12])?u|8;10(?::[12])?~)$`),
}

// shortcutUsesSuper: some modifier of the shortcut (every part but the last) is super.
func shortcutUsesSuper(shortcut string) bool {
	parts := strings.Split(strings.ToLower(shortcut), "+")
	for _, p := range parts[:len(parts)-1] {
		if p == "super" {
			return true
		}
	}
	return false
}

func isSupportedSuperShortcut(shortcut string) bool {
	_, ok := superShortcutPatterns[strings.ToLower(shortcut)]
	return ok
}

func shortcutConflictKey(shortcut string) string {
	switch strings.ToLower(shortcut) {
	case "super+home":
		return "super+up"
	case "super+end":
		return "super+down"
	case "super+shift+home":
		return "super+shift+up"
	case "super+shift+end":
		return "super+shift+down"
	}
	return shortcut
}

// matchesConfiguredShortcut: a nil or empty shortcut matches nothing.
func matchesConfiguredShortcut(data string, shortcut *string) bool {
	if shortcut == nil || *shortcut == "" {
		return false
	}
	normalized := strings.ToLower(*shortcut)
	if shortcutUsesSuper(normalized) {
		re := superShortcutPatterns[normalized]
		return re != nil && re.MatchString(data)
	}
	return matchesKey(data, *shortcut)
}

func matchesCtrlSStashInput(data string) bool {
	return !isKeyRelease(data) && matchesKey(data, "ctrl+s")
}

var altSKitty = regexp.MustCompile(`^\x1b\[(?:83|115)(?::\d*)?(?::\d*)?;3(?::\d+)?u$`)

// matchesStashShortcutInput: Alt+S in every encoding a terminal sends it; the printable sharp S only when asked.
func matchesStashShortcutInput(data string, includePrintableSharpS bool) bool {
	if isKeyRelease(data) {
		return false
	}
	return includePrintableSharpS && data == "ß" ||
		data == "\x1bs" || data == "\x1bS" ||
		altSKitty.MatchString(data) ||
		data == "\x1b[27;3;115~" || data == "\x1b[27;3;83~" ||
		matchesKey(data, "alt+s")
}

// ctrlAltHKitty is Ctrl+Alt+H in Kitty's CSI-u form, which the prompt history shortcut accepts besides matchesKey's.
var ctrlAltHKitty = regexp.MustCompile(`^\x1b\[104(?::\d*)?(?::\d*)?;7(?::\d+)?u$`)
