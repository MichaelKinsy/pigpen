package websearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Port of gemini-web-config.ts: the opt-in for browser cookie access and the browserCookies
// selection of web-search.json.

// BrowserCookieSelection is the browserCookies section of web-search.json (the browser preset
// in its normalized lower-case form, and the profile directory name).
type BrowserCookieSelection struct {
	Browser string
	Profile string
}

var browserCookiePresets = []string{"helium", "chrome", "brave", "arc", "chromium", "edge"}

var (
	geminiWebConfigMu     sync.Mutex
	geminiWebConfigCached *BrowserCookieSelection
)

func resetGeminiWebConfig() {
	geminiWebConfigMu.Lock()
	geminiWebConfigCached = nil
	geminiWebConfigMu.Unlock()
}

// normalizeChromeProfile is normalizeChromeProfile: a trimmed non-empty string, else "".
func normalizeChromeProfile(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return jsTrim(s)
}

func parseBrowserCookies(value any, present bool, path string) (BrowserCookieSelection, error) {
	if !present {
		return BrowserCookieSelection{}, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return BrowserCookieSelection{}, fmt.Errorf("browserCookies in %s must be an object", path)
	}
	if _, has := raw["profilePath"]; has {
		return BrowserCookieSelection{}, fmt.Errorf("browserCookies.profilePath is not supported; use a browser preset and profile directory name in %s", path)
	}
	var sel BrowserCookieSelection
	if b, has := raw["browser"]; has {
		s, ok := b.(string)
		if !ok {
			return sel, fmt.Errorf("browserCookies.browser in %s must be a supported browser name", path)
		}
		normalized := strings.ToLower(jsTrim(s))
		if !sliceHas(browserCookiePresets, normalized) {
			return sel, fmt.Errorf("Unsupported browserCookies.browser %s in %s", jsonQuote(s), path)
		}
		sel.Browser = normalized
	}
	if p, has := raw["profile"]; has {
		if _, ok := p.(string); !ok {
			return sel, fmt.Errorf("browserCookies.profile in %s must be a profile directory name", path)
		}
	}
	profile := normalizeChromeProfile(raw["profile"])
	if profile != "" && (profile == "." || profile == ".." || strings.Contains(profile, "/") || strings.Contains(profile, `\`)) {
		return sel, fmt.Errorf("browserCookies.profile in %s must be a profile directory name, not a path", path)
	}
	sel.Profile = profile
	return sel, nil
}

func loadBrowserCookieSelection() (BrowserCookieSelection, error) {
	geminiWebConfigMu.Lock()
	defer geminiWebConfigMu.Unlock()
	if geminiWebConfigCached != nil {
		return *geminiWebConfigCached, nil
	}
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			geminiWebConfigCached = &BrowserCookieSelection{}
			return BrowserCookieSelection{}, nil
		}
		return BrowserCookieSelection{}, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return BrowserCookieSelection{}, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	raw, _ := parsed.(map[string]any)
	if _, has := raw["chromeProfile"]; has {
		return BrowserCookieSelection{}, fmt.Errorf("chromeProfile in %s is no longer supported; use browserCookies.profile", path)
	}
	value, present := raw["browserCookies"]
	sel, err := parseBrowserCookies(value, present, path)
	if err != nil {
		return BrowserCookieSelection{}, err
	}
	geminiWebConfigCached = &sel
	return sel, nil
}

// BrowserCookieSelectionFromConfig is getBrowserCookieSelectionFromConfig.
func BrowserCookieSelectionFromConfig() (BrowserCookieSelection, error) {
	return loadBrowserCookieSelection()
}

// IsBrowserCookieAccessAllowed is isBrowserCookieAccessAllowed: browser cookies are read only
// when the user switched them on, by environment or by allowBrowserCookies. It reads the
// configuration at every call and validates nothing but its JSON syntax.
func IsBrowserCookieAccessAllowed() (bool, error) {
	if os.Getenv("PI_ALLOW_BROWSER_COOKIES") == "1" || os.Getenv("FEYNMAN_ALLOW_BROWSER_COOKIES") == "1" {
		return true, nil
	}
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	raw, _ := parsed.(map[string]any)
	return raw["allowBrowserCookies"] == true, nil
}
