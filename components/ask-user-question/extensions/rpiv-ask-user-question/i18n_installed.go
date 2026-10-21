package ask_user_question

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The original translates only when its optional peer @juicesharp/rpiv-i18n is installed; without it every string is
// the inline English fallback (state/i18n-bridge.ts: the dynamic import of the SDK fails and `t` returns the fallback).
// A Go extension cannot import that SDK, so "installed" is read from Pi's settings: a package or extension entry that
// names rpiv-i18n in the user's settings.json (the agent directory) or in the project's .pi/settings.json. Only then
// does the port resolve a locale (internal/ui, as the SDK does: locale.json, LANG, LC_ALL).
const i18nPackageName = "rpiv-i18n"

// rpivI18nInstalled reports whether Pi's settings install rpiv-i18n. cwd is the project directory ("" skips it).
func rpivI18nInstalled(cwd string) bool {
	paths := []string{filepath.Join(agentDir(), "settings.json")}
	if cwd != "" {
		paths = append(paths, filepath.Join(cwd, ".pi", "settings.json"))
	}
	for _, path := range paths {
		if settingsNameInstalled(path) {
			return true
		}
	}
	return false
}

// settingsNameInstalled reads one settings file: `packages` entries are a source string or an object with a `source`,
// `extensions` entries are paths; a malformed or missing file names nothing.
func settingsNameInstalled(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	for _, key := range []string{"packages", "extensions"} {
		entries, _ := settings[key].([]any)
		for _, entry := range entries {
			source, _ := entry.(string)
			if object, ok := entry.(map[string]any); ok {
				source, _ = object["source"].(string)
			}
			if strings.Contains(source, i18nPackageName) {
				return true
			}
		}
	}
	return false
}
