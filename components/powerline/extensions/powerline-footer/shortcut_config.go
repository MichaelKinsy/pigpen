package powerline_footer

import (
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Shortcut settings: powerlineShortcuts and bashMode in settings.json, with the conflict rules of the original.
// upstream: index.ts 100-170 (defaults, reserved lists) and 697-912 (resolveShortcutConfig, parseBashModeSettings).
// The original logs conflicts with console.debug; the port stays silent (a Go extension has no console, and fmt.Print* would
// write to the wire).

// powerlineShortcuts is PowerlineShortcuts: each binding is a shortcut id or nil (off).
type powerlineShortcuts struct {
	StashHistory, CopyEditor, CutEditor, QueueOpen, Reply, EditorStart, EditorEnd *string
}

// shortcutKeys is SHORTCUT_KEYS, in the original's order.
var shortcutKeys = []string{"stashHistory", "copyEditor", "cutEditor", "queueOpen", "reply", "editorStart", "editorEnd"}

// defaultShortcuts is DEFAULT_SHORTCUTS.
func defaultShortcuts() powerlineShortcuts {
	return powerlineShortcuts{
		StashHistory: new("ctrl+alt+h"), CopyEditor: new("ctrl+alt+c"), CutEditor: new("ctrl+alt+x"), QueueOpen: new("ctrl+alt+q"),
		Reply: nil, EditorStart: new("super+shift+up"), EditorEnd: new("super+shift+down"),
	}
}

// slot returns the binding named by one of shortcutKeys.
func (s *powerlineShortcuts) slot(key string) **string {
	switch key {
	case "stashHistory":
		return &s.StashHistory
	case "copyEditor":
		return &s.CopyEditor
	case "cutEditor":
		return &s.CutEditor
	case "queueOpen":
		return &s.QueueOpen
	case "reply":
		return &s.Reply
	case "editorStart":
		return &s.EditorStart
	case "editorEnd":
		return &s.EditorEnd
	}
	return nil
}

// values is Object.values(shortcuts).
func (s powerlineShortcuts) values() []*string {
	return []*string{s.StashHistory, s.CopyEditor, s.CutEditor, s.QueueOpen, s.Reply, s.EditorStart, s.EditorEnd}
}

// bashModeSettings is BashModeSettings (bash-mode/types.ts) as parsed from the `bashMode` setting.
type bashModeSettings struct {
	ToggleShortcut                         *string
	Completions                            bool
	TranscriptMaxLines, TranscriptMaxBytes int
}

const defaultBashToggleShortcut = "ctrl+shift+b"

// appReservedShortcuts is APP_RESERVED_SHORTCUTS and extraReservedShortcuts EXTRA_RESERVED_SHORTCUTS.
var appReservedShortcuts = []string{
	"escape", "ctrl+c", "ctrl+d", "ctrl+z", "shift+tab", "ctrl+p", "shift+ctrl+p", "ctrl+l", "ctrl+o", "shift+ctrl+o", "ctrl+t",
	"ctrl+n", "ctrl+g", "alt+enter", "alt+up", "alt+down", "ctrl+v", "alt+v", "shift+l", "shift+t", "ctrl+s", "ctrl+r",
	"ctrl+backspace", "ctrl+a", "ctrl+x", "ctrl+u",
}
var extraReservedShortcuts = []string{"alt+s"}

// tuiDefaultKeys are the default keys of every pi-tui keybinding (TUI_KEYBINDINGS[*].defaultKeys), which the original reserves.
var tuiDefaultKeys = []string{
	"up", "down", "left", "ctrl+b", "right", "ctrl+f", "alt+left", "ctrl+left", "alt+b", "alt+right", "ctrl+right", "alt+f", "home",
	"ctrl+a", "end", "ctrl+e", "ctrl+]", "ctrl+alt+]", "pageUp", "ctrl+pageUp", "pageDown", "ctrl+pageDown", "backspace", "delete",
	"ctrl+d", "ctrl+w", "alt+backspace", "alt+d", "alt+delete", "ctrl+u", "ctrl+k", "ctrl+y", "alt+y", "ctrl+-", "shift+enter",
	"ctrl+j", "enter", "tab", "ctrl+c", "escape", "ctrl+shift+up", "ctrl+up", "ctrl+shift+down", "ctrl+down", "ctrl+shift+f",
	"ctrl+g", "ctrl+shift+g", "ctrl+home", "ctrl+end",
}

var shortcutModifierOrder = []string{"ctrl", "alt", "super", "shift"}

var shortcutNamedKeys = []string{
	"escape", "esc", "enter", "return", "tab", "space", "backspace", "delete", "insert", "clear",
	"home", "end", "pageup", "pagedown", "up", "down", "left", "right",
}

const shortcutSymbolKeys = "`-=[]\\;',./!@#$%^&*()_|~{}:<>?"

var fKeyPart = regexp.MustCompile(`(?i)^f([1-9]|1[0-2])$`)

// normalizeShortcut puts modifiers in the canonical order ctrl, alt, super, shift.
func normalizeShortcut(value string) string {
	parts := strings.Split(strings.ToLower(jsTrim(value)), "+")
	if len(parts) <= 1 {
		return parts[0]
	}
	rank := func(m string) int {
		if i := slices.Index(shortcutModifierOrder, m); i >= 0 {
			return i
		}
		return 99
	}
	mods := slices.Clone(parts[:len(parts)-1])
	slices.SortStableFunc(mods, func(a, b string) int { return rank(a) - rank(b) })
	return strings.Join(append(mods, parts[len(parts)-1]), "+")
}

// reservedShortcuts is the set of shortcuts Pi or powerline already use.
func reservedShortcuts() map[string]bool {
	set := map[string]bool{}
	for _, list := range [][]string{extraReservedShortcuts, appReservedShortcuts, tuiDefaultKeys} {
		for _, k := range list {
			set[normalizeShortcut(k)] = true
		}
	}
	return set
}

func isValidShortcutKeyPart(keyPart string) bool {
	lower := strings.ToLower(keyPart)
	if len(keyPart) == 1 && (keyPart[0] >= 'a' && keyPart[0] <= 'z' || keyPart[0] >= 'A' && keyPart[0] <= 'Z' || keyPart[0] >= '0' && keyPart[0] <= '9') {
		return true
	}
	if fKeyPart.MatchString(keyPart) || slices.Contains(shortcutNamedKeys, lower) {
		return true
	}
	return len(keyPart) == 1 && strings.Contains(shortcutSymbolKeys, keyPart)
}

// parseShortcutOverride validates and normalizes one configured shortcut; ok is false for anything the original rejects.
func parseShortcutOverride(value any) (string, bool) {
	s, isString := value.(string)
	if !isString {
		return "", false
	}
	trimmed := jsTrim(s)
	if trimmed == "" || strings.IndexFunc(trimmed, isJSSpace) >= 0 {
		return "", false
	}
	parts := strings.Split(trimmed, "+")
	if slices.Contains(parts, "") {
		return "", false
	}
	mods := make([]string, 0, len(parts)-1)
	for _, p := range parts[:len(parts)-1] {
		m := strings.ToLower(p)
		if m == "cmd" || m == "command" {
			m = "super"
		}
		mods = append(mods, m)
	}
	seen := map[string]bool{}
	for _, m := range mods {
		if seen[m] || !slices.Contains(shortcutModifierOrder, m) {
			return "", false
		}
		seen[m] = true
	}
	keyPart := parts[len(parts)-1]
	if !isValidShortcutKeyPart(keyPart) {
		return "", false
	}
	key := strings.ToLower(keyPart)
	if len(keyPart) == 1 && strings.Contains(shortcutSymbolKeys, keyPart) {
		key = keyPart
	}
	normalized := normalizeShortcut(strings.Join(append(mods, key), "+"))
	if shortcutUsesSuper(normalized) && !isSupportedSuperShortcut(normalized) {
		return "", false
	}
	return normalized, true
}

func shortcutUsageKey(shortcut string) string {
	return shortcutConflictKey(normalizeShortcut(shortcut))
}

// parseShortcutSetting is parseShortcutSetting: defined is false (JavaScript undefined) for an invalid value; a null, absent or
// blank value is a defined nil binding (the shortcut is off).
func parseShortcutSetting(value any) (binding *string, defined bool) {
	if value == nil {
		return nil, true
	}
	if s, ok := value.(string); ok && jsTrim(s) == "" {
		return nil, true
	}
	if o, ok := parseShortcutOverride(value); ok {
		return &o, true
	}
	return nil, false
}

func shortcutBelongsToOtherDefault(key, shortcut string) bool {
	usage := shortcutUsageKey(shortcut)
	defaults := defaultShortcuts()
	for _, other := range shortcutKeys {
		d := *defaults.slot(other)
		if other != key && d != nil && shortcutUsageKey(*d) == usage {
			return true
		}
	}
	return false
}

func bashToggleShortcutReservation(settings map[string]any) *string {
	raw := asObject(settings["bashMode"])
	if raw == nil {
		return new(defaultBashToggleShortcut)
	}
	v, has := raw.get("toggleShortcut")
	if !has {
		return new(defaultBashToggleShortcut)
	}
	if parsed, defined := parseShortcutSetting(v); defined {
		return parsed
	}
	return new(defaultBashToggleShortcut)
}

// reservedUsage is the usage key set of reservedShortcuts, built once: it is constant, and normalising about 100 key names on
// every session start was most of the allocations of that event.
var reservedUsage = sync.OnceValue(func() map[string]bool { return usageKeys(reservedShortcuts()) })

func usageKeys(list map[string]bool) map[string]bool {
	used := map[string]bool{}
	for k := range list {
		used[shortcutUsageKey(k)] = true
	}
	return used
}

// resolveShortcutConfig applies the powerlineShortcuts setting over the defaults and resolves conflicts: a shortcut that
// another action or Pi uses falls back to its own default, or is switched off when that is taken too.
func resolveShortcutConfig(settings map[string]any) powerlineShortcuts {
	resolved := defaultShortcuts()
	if so := asObject(settings["powerlineShortcuts"]); so != nil {
		for _, key := range shortcutKeys {
			v, has := so.get(key)
			if !has {
				continue
			}
			if override, defined := parseShortcutSetting(v); defined {
				*resolved.slot(key) = override
			}
		}
	}

	used := maps.Clone(reservedUsage())
	if toggle := bashToggleShortcutReservation(settings); toggle != nil {
		used[shortcutUsageKey(*toggle)] = true
	}

	for _, key := range shortcutKeys {
		slot := resolved.slot(key)
		configured := *slot
		if configured == nil {
			continue
		}
		usage := shortcutUsageKey(*configured)
		if !used[usage] && !shortcutBelongsToOtherDefault(key, *configured) {
			used[usage] = true
			continue
		}
		// findShortcutReplacement: the action's own default, when nothing else has it.
		var replacement *string
		defaults := defaultShortcuts()
		if preferred := *defaults.slot(key); preferred != nil && !used[shortcutUsageKey(*preferred)] {
			replacement = preferred
		}
		*slot = replacement
		if replacement != nil {
			used[shortcutUsageKey(*replacement)] = true
		}
	}
	return resolved
}

// parseBashModeSettings reads the bashMode setting. shortcuts are the resolved powerline shortcuts the toggle must not collide
// with (nil: none, as when the original is called without them).
func parseBashModeSettings(settings map[string]any, shortcuts *powerlineShortcuts) bashModeSettings {
	raw := asObject(settings["bashMode"])
	if raw == nil {
		raw = newObject()
	}
	used := maps.Clone(reservedUsage())
	if shortcuts != nil {
		for _, s := range shortcuts.values() {
			if s != nil {
				used[shortcutUsageKey(*s)] = true
			}
		}
	}

	var configured *string
	configuredDefined := false
	if v, has := raw.get("toggleShortcut"); has {
		configured, configuredDefined = parseShortcutSetting(v)
	}
	var fallback *string
	if !used[shortcutUsageKey(defaultBashToggleShortcut)] {
		fallback = new(defaultBashToggleShortcut)
	}
	var toggle *string
	switch {
	case configuredDefined && configured == nil:
		toggle = nil
	case configured != nil && !used[shortcutUsageKey(*configured)]:
		toggle = configured
	default:
		toggle = fallback
	}

	out := bashModeSettings{ToggleShortcut: toggle, TranscriptMaxLines: 2000, TranscriptMaxBytes: 512 * 1024}
	if b, ok := raw.vals["completions"].(bool); ok {
		out.Completions = b
	}
	if n, ok := raw.vals["transcriptMaxLines"].(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
		out.TranscriptMaxLines = clampInt(math.Max(100, math.Floor(n)))
	}
	if n, ok := raw.vals["transcriptMaxBytes"].(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
		out.TranscriptMaxBytes = clampInt(math.Max(16*1024, math.Floor(n)))
	}
	return out
}

// clampInt converts a finite JavaScript number to an int, saturating at 2^53 (the largest exact integer).
func clampInt(f float64) int {
	return int(math.Min(f, 1<<53))
}
