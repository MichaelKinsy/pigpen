// Chromium browsers on this machine and their profiles, read from their user data directories without touching them:
// where oh-my-pig registers its native messaging host, and whether a profile already loads an oh-my-pig install folder.
//
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts (omp v19.1.1)
// Note: Chrome process management and unattended profile installation helpers (stopBrowser, startBrowser,
// seedPaks, installedChromiumBrowsers, lastUsedProfile) are intentionally omitted — unattended install is not ported.
package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type browserDefinition struct {
	name    string
	darwin  string
	linux   string
	windows string
}

// supportedBrowsers matches upstream BROWSERS in chromium-profiles.ts:25-86.
var supportedBrowsers = []browserDefinition{
	{
		name:    "Google Chrome",
		darwin:  "Google/Chrome",
		linux:   "google-chrome",
		windows: "Google/Chrome/User Data",
	},
	{
		name:    "Google Chrome Beta",
		darwin:  "Google/Chrome Beta",
		linux:   "google-chrome-beta",
		windows: "Google/Chrome Beta/User Data",
	},
	{
		name:    "Google Chrome Dev",
		darwin:  "Google/Chrome Dev",
		linux:   "google-chrome-unstable",
		windows: "Google/Chrome Dev/User Data",
	},
	{
		name:    "Google Chrome Canary",
		darwin:  "Google/Chrome Canary",
		linux:   "google-chrome-canary",
		windows: "Google/Chrome SxS/User Data",
	},
	{
		name:    "Chromium",
		darwin:  "Chromium",
		linux:   "chromium",
		windows: "Chromium/User Data",
	},
	{
		name:    "Brave",
		darwin:  "BraveSoftware/Brave-Browser",
		linux:   "BraveSoftware/Brave-Browser",
		windows: "BraveSoftware/Brave-Browser/User Data",
	},
}

// ChromiumLocations overrides where to look for browser user data; empty fields default to this user's.
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts:89-96
type ChromiumLocations struct {
	Platform     string // "darwin", "linux", "windows" (or "win32"); defaults to runtime.GOOS
	Home         string // defaults to os.UserHomeDir()
	ConfigHome   string // Linux $XDG_CONFIG_HOME
	LocalAppData string // Windows %LOCALAPPDATA%
}

// ChromiumUserData names a Chromium browser's user data directory (profiles, Local State,
// and on macOS/Linux NativeMessagingHosts).
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts:99-102
type ChromiumUserData struct {
	Browser string
	Dir     string
}

// ChromiumUserDataDirs returns the user data directory of every supported Chromium-family browser
// on the requested platform, whether or not the browser is installed.
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts:105-135
func ChromiumUserDataDirs(opts ChromiumLocations) []ChromiumUserData {
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	var base string
	var pick func(browserDefinition) string
	switch platform {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
		pick = func(b browserDefinition) string { return b.darwin }
	case "linux":
		base = opts.ConfigHome
		if base == "" {
			if opts.Home == "" {
				base = os.Getenv("XDG_CONFIG_HOME")
			}
			if base == "" {
				base = filepath.Join(home, ".config")
			}
		}
		pick = func(b browserDefinition) string { return b.linux }
	case "windows", "win32":
		base = opts.LocalAppData
		if base == "" {
			if opts.Home == "" {
				base = os.Getenv("LOCALAPPDATA")
			}
			if base == "" {
				base = filepath.Join(home, "AppData", "Local")
			}
		}
		pick = func(b browserDefinition) string { return b.windows }
	default:
		return nil
	}

	out := make([]ChromiumUserData, 0, len(supportedBrowsers))
	for _, b := range supportedBrowsers {
		parts := append([]string{base}, strings.Split(pick(b), "/")...)
		out = append(out, ChromiumUserData{
			Browser: b.name,
			Dir:     filepath.Join(parts...),
		})
	}
	return out
}

// ChromiumProfileInstall describes one browser profile and whether it has an unpacked extension loaded.
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts:138-148
type ChromiumProfileInstall struct {
	Browser       string
	Profile       string
	Loaded        bool
	Enabled       bool
	DeveloperMode bool
}

// unpackedLocation is Chrome's ManifestLocation::kUnpacked, recorded by Load unpacked.
const unpackedLocation = 4

// FindUnpackedInstalls inspects Local State, Preferences, and Secure Preferences across every
// supported Chromium browser without modifying any file. Unreadable files count as absent.
// upstream: packages/coding-agent/src/tools/browser/relay/chromium-profiles.ts:158-187
func FindUnpackedInstalls(extensionID string, opts ChromiumLocations) []ChromiumProfileInstall {
	var found []ChromiumProfileInstall
	for _, entry := range ChromiumUserDataDirs(opts) {
		localState := readJSONMap(filepath.Join(entry.Dir, "Local State"))
		profileMap, _ := localState["profile"].(map[string]any)
		infoCache, _ := profileMap["info_cache"].(map[string]any)
		if len(infoCache) == 0 {
			continue
		}
		profileDirs := make([]string, 0, len(infoCache))
		for dir := range infoCache {
			profileDirs = append(profileDirs, dir)
		}
		sort.Strings(profileDirs)
		for _, profileDir := range profileDirs {
			info, _ := infoCache[profileDir].(map[string]any)
			prefs := readJSONMap(filepath.Join(entry.Dir, profileDir, "Preferences"))
			secure := readJSONMap(filepath.Join(entry.Dir, profileDir, "Secure Preferences"))
			settings := extensionSettingsMap(secure, extensionID)
			if settings == nil {
				settings = extensionSettingsMap(prefs, extensionID)
			}
			loaded := jsonIntEqual(settings["location"], unpackedLocation)
			disabled := isExtensionDisabled(settings)
			devMode := false
			if v, ok := developerModePref(secure); ok {
				devMode = v
			} else if v, ok := developerModePref(prefs); ok {
				devMode = v
			}
			profileName := profileDir
			if name, ok := info["name"].(string); ok && name != "" {
				profileName = name
			}
			found = append(found, ChromiumProfileInstall{
				Browser:       entry.Browser,
				Profile:       profileName,
				Loaded:        loaded,
				Enabled:       loaded && !disabled,
				DeveloperMode: devMode,
			})
		}
	}
	return found
}

func developerModePref(prefs map[string]any) (bool, bool) {
	if prefs == nil {
		return false, false
	}
	exts, _ := prefs["extensions"].(map[string]any)
	ui, _ := exts["ui"].(map[string]any)
	val, ok := ui["developer_mode"].(bool)
	return val, ok
}

func extensionSettingsMap(prefs map[string]any, extensionID string) map[string]any {
	if prefs == nil {
		return nil
	}
	exts, _ := prefs["extensions"].(map[string]any)
	settings, _ := exts["settings"].(map[string]any)
	entry, _ := settings[extensionID].(map[string]any)
	return entry
}

func jsonIntEqual(v any, want int) bool {
	f, ok := v.(float64)
	return ok && f == float64(want)
}

func isExtensionDisabled(settings map[string]any) bool {
	if settings == nil {
		return false
	}
	if jsonIntEqual(settings["state"], 0) {
		return true
	}
	switch reasons := settings["disable_reasons"].(type) {
	case []any:
		return len(reasons) > 0
	case float64:
		return reasons != 0
	default:
		return false
	}
}

func readJSONMap(file string) map[string]any {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
