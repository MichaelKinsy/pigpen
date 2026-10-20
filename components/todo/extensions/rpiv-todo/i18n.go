package rpiv_todo

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// i18nNamespace is the namespace the original registers its locale strings under.
// upstream: state/i18n-bridge.ts:23.
const i18nNamespace = "@juicesharp/rpiv-todo"

// The i18n bridge. upstream: state/i18n-bridge.ts, backed here by a port of the locale resolution of
// @juicesharp/rpiv-i18n 2.13.9 (i18n.ts, loader.ts). Like the original, which translates only when that optional peer is
// installed and otherwise returns the inline English literal at every call site, the bridge resolves a locale only after
// Pi's settings name rpiv-i18n (setI18nInstalled at session_start); until then every string is English.
//
// Not ported: a live /languages switch (it travels through in-process JS state, globalThis[Symbol.for("rpiv-i18n.runtime")],
// which a Go process cannot read) and the --locale command-line flag (the host does not pass flags to extension
// processes). The locale is read from locale.json, LANG and LC_ALL on every lookup, so an edit of locale.json applies at once.

// supportedLocales are the codes the SDK registers. upstream: SUPPORTED_LOCALES.
var supportedLocales = []string{"de", "en", "es", "fr", "pt", "pt-BR", "ru", "uk", "zh"}

const defaultFallbackLocale = "en"

// i18nPackageName is how Pi's settings name the optional peer.
const i18nPackageName = "rpiv-i18n"

//go:embed locales/*.json
var localeFS embed.FS

var (
	i18nMu       sync.Mutex
	localeTables map[string]map[string]string
	// localeOverride overrides locale detection when non-empty (stands in for the --locale flag; tests set it).
	localeOverride string
	// i18nInstalled is whether the original's optional peer rpiv-i18n is installed; without it no locale is resolved.
	i18nInstalled bool
)

// setI18nInstalled records whether rpiv-i18n is installed. cwd is the project directory ("" skips its settings).
func setI18nInstalled(cwd string) {
	installed := rpivI18nInstalled(cwd)
	i18nMu.Lock()
	i18nInstalled = installed
	i18nMu.Unlock()
}

// setLocale sets the locale override ("" clears it).
func setLocale(code string) {
	i18nMu.Lock()
	localeOverride = code
	i18nMu.Unlock()
}

// loadLocaleTables reads the embedded files once; a missing or malformed file records an empty map, so lookups fall
// back to English. The caller holds i18nMu.
func loadLocaleTables() map[string]map[string]string {
	if localeTables != nil {
		return localeTables
	}
	localeTables = map[string]map[string]string{}
	for _, code := range supportedLocales {
		m := map[string]string{}
		if data, err := localeFS.ReadFile("locales/" + code + ".json"); err == nil {
			if json.Unmarshal(data, &m) != nil {
				m = map[string]string{}
			}
		}
		localeTables[code] = m
	}
	return localeTables
}

func parseLangEnv(value string) string {
	if value == "" {
		return ""
	}
	lang := strings.SplitN(value, "_", 2)[0]
	lang = strings.SplitN(lang, ".", 2)[0]
	if lang == "" || lang == "C" || lang == "POSIX" {
		return ""
	}
	return lang
}

// activeLocale resolves the locale: the override, then (only with rpiv-i18n installed) rpiv-i18n/locale.json under the
// config directory, then LANG, then LC_ALL; "" is English. upstream: detectLocaleFromConfigAndEnv.
func activeLocale() string {
	if localeOverride != "" {
		return localeOverride
	}
	if !i18nInstalled {
		return ""
	}
	if data, err := os.ReadFile(filepath.Join(resolveConfigDir(), "rpiv-i18n", "locale.json")); err == nil {
		var cfg struct {
			Locale string `json:"locale"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.Locale != "" {
			return cfg.Locale
		}
	}
	if l := parseLangEnv(os.Getenv("LANG")); l != "" {
		return l
	}
	return parseLangEnv(os.Getenv("LC_ALL"))
}

// tr is the original's `t(key, fallback)`: the active locale's value, else English, else the inline fallback. An empty
// value is absent. Resolved on every call. upstream: state/i18n-bridge.ts:36-45, rpiv-i18n scope().
func tr(key, fallback string) string {
	i18nMu.Lock()
	defer i18nMu.Unlock()
	if localeOverride == "" && !i18nInstalled {
		return fallback
	}
	tables := loadLocaleTables()
	if loc := activeLocale(); loc != "" && loc != defaultFallbackLocale {
		if v := tables[loc][key]; v != "" {
			return v
		}
	}
	if v := tables[defaultFallbackLocale][key]; v != "" {
		return v
	}
	return fallback
}

// rpivI18nInstalled reports whether Pi's settings install rpiv-i18n: a `packages` or `extensions` entry that names it
// in the agent directory's settings.json or the project's .pi/settings.json. A malformed or missing file names nothing.
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

// agentDir is PiG's agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent.
func agentDir() string {
	if dir := os.Getenv("PIG_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	if home := os.Getenv("PIG_HOME"); home != "" {
		return filepath.Join(home, "agent")
	}
	return filepath.Join(homeDir(), ".pig", "agent")
}
