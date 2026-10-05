package powerline_footer

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// hasNerdFonts guesses whether the terminal draws Nerd Font glyphs. upstream: icons.ts hasNerdFonts.
func hasNerdFonts() bool {
	switch os.Getenv("POWERLINE_NERD_FONTS") {
	case "1":
		return true
	case "0":
		return false
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	// TERM_PROGRAM wins when it is set, even to an empty string: `??` falls through only for undefined.
	term, ok := os.LookupEnv("TERM_PROGRAM")
	if !ok {
		term = os.Getenv("TERM")
	}
	term = strings.ToLower(term)
	for _, t := range []string{"iterm", "wezterm", "kitty", "ghostty", "alacritty", "kaku"} {
		if strings.Contains(term, t) {
			return true
		}
	}
	return false
}

// upstream: paths.ts. HOME wins over USERPROFILE, as in Pi.
func getHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func normalizeAgentDirPath(value string) string {
	trimmed := jsTrim(value)
	switch {
	case trimmed == "~":
		return getHomeDir()
	case strings.HasPrefix(trimmed, "~/"):
		return filepath.Join(getHomeDir(), trimmed[2:])
	case strings.HasPrefix(trimmed, "file://"):
		if u, err := url.Parse(trimmed); err == nil {
			return u.Path
		}
	}
	return trimmed
}

func getAgentDir() string {
	if configured := os.Getenv("PI_CODING_AGENT_DIR"); jsTrim(configured) != "" {
		return normalizeAgentDirPath(configured)
	}
	return filepath.Join(getHomeDir(), ".pi", "agent")
}

func getAgentPath(parts ...string) string {
	return filepath.Join(append([]string{getAgentDir()}, parts...)...)
}

func getLegacyPiPath(parts ...string) string {
	return filepath.Join(append([]string{getHomeDir(), ".pi"}, parts...)...)
}

func getAgentSessionDirs() []string {
	primary := getAgentPath("sessions")
	legacy := getLegacyPiPath("sessions")
	if _, err := os.Stat(legacy); err == nil && legacy != primary {
		return []string{primary, legacy}
	}
	return []string{primary}
}

// resolveThinkingLevelSelection prefers the live event's level over the context's. upstream: thinking-level.ts.
func resolveThinkingLevelSelection(eventLevel, contextLevel any) any {
	if s, ok := eventLevel.(string); ok {
		return s
	}
	return contextLevel
}
