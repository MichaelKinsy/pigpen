package rpiv_todo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Configuration: <config home>/rpiv-todo/config.json, read fresh on every call (no caching, no /reload).
// upstream: config.ts, and rpiv-config config.ts (loadJsonConfigWithLegacyFallback) at the same commit.
const (
	// defaultMaxWidgetLines is the content-row budget when the config is missing or invalid.
	defaultMaxWidgetLines = 12
	// defaultCollapseKey is the collapse and expand shortcut when collapseKey is missing or invalid.
	defaultCollapseKey = "ctrl+shift+t"
	// collapseKeyOff disables the collapse shortcut entirely.
	collapseKeyOff = "off"
	configName     = "rpiv-todo"
)

type guidanceFields struct {
	PromptSnippet    string
	PromptGuidelines []string
	Description      string
}

// todoConfig is the file's content. The loader does not validate: each resolver decides.
type todoConfig struct {
	Guidance       any
	MaxWidgetLines any
	CollapseKey    any
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func defaultConfigDir() string { return filepath.Join(homeDir(), ".config") }

// resolveConfigDir is $XDG_CONFIG_HOME when it is set and absolute (a leading ~ is expanded), else ~/.config.
func resolveConfigDir() string {
	xdg := jsTrim(os.Getenv("XDG_CONFIG_HOME"))
	if xdg == "" {
		return defaultConfigDir()
	}
	switch {
	case xdg == "~":
		xdg = homeDir()
	case strings.HasPrefix(xdg, "~/"):
		xdg = filepath.Join(homeDir(), xdg[2:])
	}
	if filepath.IsAbs(xdg) {
		return xdg
	}
	return defaultConfigDir()
}

// loadJSONConfig reads a JSON object; a missing, unreadable, invalid or non-object file reads as empty.
func loadJSONConfig(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return map[string]any{}
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return m
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadConfig() todoConfig {
	xdgPath := filepath.Join(resolveConfigDir(), configName, "config.json")
	m := loadJSONConfig(filepath.Join(defaultConfigDir(), configName, "config.json"))
	if fileExists(xdgPath) {
		m = loadJSONConfig(xdgPath)
	}
	return todoConfig{Guidance: m["guidance"], MaxWidgetLines: m["maxWidgetLines"], CollapseKey: m["collapseKey"]}
}

// getMaxWidgetLines is the content-row budget of the overlay: a non-number or a value below the floor of 3
// falls back to the default; there is no ceiling.
func getMaxWidgetLines() int {
	if n, ok := loadConfig().MaxWidgetLines.(float64); ok && n >= 3 {
		return int(n)
	}
	return defaultMaxWidgetLines
}

var (
	specialKeys = map[string]bool{
		"escape": true, "esc": true, "enter": true, "return": true, "tab": true, "space": true, "backspace": true,
		"delete": true, "insert": true, "clear": true, "home": true, "end": true, "pageup": true, "pagedown": true,
		"up": true, "down": true, "left": true, "right": true,
		"f1": true, "f2": true, "f3": true, "f4": true, "f5": true, "f6": true, "f7": true, "f8": true,
		"f9": true, "f10": true, "f11": true, "f12": true,
	}
	modifierKeys = map[string]bool{"ctrl": true, "shift": true, "alt": true, "super": true}
)

const baseKeyChars = "abcdefghijklmnopqrstuvwxyz0123456789_-!@#$%^&*()|~`'\":;,./<>?[]{}=\\"

// isValidCollapseKeySpec validates a spec against pi-tui's KeyId grammar: zero or more distinct modifiers,
// then a base key that is one printable character or a named special key. A loose check is not enough: pi-tui
// takes the last `+` part as the key and ignores unknown parts, so a typo like `ctr+]` would match every bare
// `]` keypress. upstream: config.ts:78-93.
func isValidCollapseKeySpec(spec string) bool {
	if spec == "" || strings.HasPrefix(spec, "+") || strings.HasSuffix(spec, "+") || strings.Contains(spec, "++") {
		return false
	}
	parts := strings.Split(spec, "+")
	base := parts[len(parts)-1]
	modifiers := parts[:len(parts)-1]
	seen := map[string]bool{}
	for _, m := range modifiers {
		if seen[m] || !modifierKeys[m] {
			return false
		}
		seen[m] = true
	}
	if r := []rune(base); len(r) == 1 && r[0] < 0x10000 {
		return strings.ContainsRune(baseKeyChars, r[0])
	}
	return specialKeys[base]
}

// resolveCollapseKey is the collapse key: the default when the field is missing, not a string, blank or
// invalid; the sentinel "off"; or the lowercased validated spec.
func resolveCollapseKey() string {
	raw, ok := loadConfig().CollapseKey.(string)
	if !ok {
		return defaultCollapseKey
	}
	raw = strings.ToLower(jsTrim(raw))
	switch {
	case raw == "":
		return defaultCollapseKey
	case raw == collapseKeyOff:
		return collapseKeyOff
	case isValidCollapseKeySpec(raw):
		return raw
	}
	return defaultCollapseKey
}

// validateGuidanceFields keeps the guidance fields that have the right type and are not empty.
// upstream: rpiv-config config.ts validateGuidanceFields.
func validateGuidanceFields(fields any) guidanceFields {
	g, ok := fields.(map[string]any)
	if !ok {
		return guidanceFields{}
	}
	var out guidanceFields
	if s, ok := g["promptSnippet"].(string); ok && s != "" {
		out.PromptSnippet = s
	}
	if list, ok := g["promptGuidelines"].([]any); ok && len(list) > 0 {
		lines := make([]string, 0, len(list))
		for _, e := range list {
			s, ok := e.(string)
			if !ok || s == "" {
				lines = nil
				break
			}
			lines = append(lines, s)
		}
		out.PromptGuidelines = lines
	}
	if s, ok := g["description"].(string); ok && s != "" {
		out.Description = s
	}
	return out
}
