package ui

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// i18n bridge. upstream: state/i18n-bridge.ts, backed here by a port of the resolution of @juicesharp/rpiv-i18n
// 2.13.9 (i18n.ts, loader.ts). Like the original, which translates only when that optional peer is installed and
// otherwise returns the inline English fallback at every call site, the bridge resolves a locale only after the root
// package reports rpiv-i18n installed (SetI18nInstalled); until then every string is English.

// I18nNamespace is the translation namespace. upstream: I18N_NAMESPACE.
const I18nNamespace = "@juicesharp/rpiv-ask-user-question"

// SupportedLocales are the codes the SDK registers, alphabetical. upstream: SUPPORTED_LOCALES.
var SupportedLocales = []string{"de", "en", "es", "fr", "pt", "pt-BR", "ru", "uk", "zh"}

const defaultFallbackLocale = "en"

//go:embed locales/*.json
var localeFS embed.FS

var (
	i18nMu       sync.Mutex
	localeLoaded bool
	localeTables map[string]map[string]string

	// localeOverride overrides locale detection when non-empty (stands in for the --locale flag; tests and the
	// root package set it through SetLocale).
	localeOverride string
	// i18nInstalled is whether the original's optional peer rpiv-i18n is installed; without it no locale is resolved.
	i18nInstalled bool
)

// SetI18nInstalled records whether rpiv-i18n is installed. Without it the locale resolves to English, as the original's
// bridge does when its dynamic import of the SDK fails. The override of SetLocale (tests, the --locale stand-in) still applies.
func SetI18nInstalled(installed bool) {
	i18nMu.Lock()
	i18nInstalled = installed
	i18nMu.Unlock()
}

// SetLocale sets the locale override ("" clears it).
func SetLocale(code string) {
	i18nMu.Lock()
	localeOverride = code
	i18nMu.Unlock()
}

func loadLocaleTables() map[string]map[string]string {
	if localeLoaded {
		return localeTables
	}
	localeLoaded = true
	localeTables = map[string]map[string]string{}
	for _, code := range SupportedLocales {
		m := map[string]string{}
		// A missing or malformed file records an empty map; lookups fall back to English.
		if data, err := localeFS.ReadFile("locales/" + code + ".json"); err == nil {
			if json.Unmarshal(data, &m) != nil {
				m = map[string]string{}
			}
		}
		localeTables[code] = m
	}
	return localeTables
}

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		if x == "~" || strings.HasPrefix(x, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, x[1:])
			}
		} else if filepath.IsAbs(x) {
			return x
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
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

// activeLocale resolves the locale: override, then (only with rpiv-i18n installed) ~/.config/rpiv-i18n/locale.json,
// then LANG, then LC_ALL; "" is English. upstream: detectLocaleFromConfigAndEnv.
func activeLocale() string {
	if localeOverride != "" {
		return localeOverride
	}
	if !i18nInstalled {
		return ""
	}
	if dir := configHome(); dir != "" {
		if data, err := os.ReadFile(filepath.Join(dir, "rpiv-i18n", "locale.json")); err == nil {
			var cfg struct {
				Locale string `json:"locale"`
			}
			if json.Unmarshal(data, &cfg) == nil && cfg.Locale != "" {
				return cfg.Locale
			}
		}
	}
	if l := parseLangEnv(os.Getenv("LANG")); l != "" {
		return l
	}
	return parseLangEnv(os.Getenv("LC_ALL"))
}

// T is the namespace-scoped lookup: the active locale's value, else English, else fallback. An empty value is
// absent. Resolved on every call. upstream: t.
func T(key, fallback string) string {
	i18nMu.Lock()
	defer i18nMu.Unlock()
	tables := loadLocaleTables()
	loc := activeLocale()
	if loc != "" && loc != defaultFallbackLocale {
		if overlay, ok := tables[loc]; ok {
			if v := overlay[key]; v != "" {
				return v
			}
		}
	}
	if v := tables[defaultFallbackLocale][key]; v != "" {
		return v
	}
	return fallback
}

// DisplayLabel resolves a sentinel kind to its locale-aware label. upstream: displayLabel.
func DisplayLabel(kind SentinelKind) string {
	return T("sentinel."+kind, RowIntentMetaTable[kind].Label)
}
